package main

import (
	"github.com/orkestr8/fsm"

	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// VERSION is the git version.
	VERSION string
	// REVISION is the git rev.
	REVISION string
	// HASH is a hash value that's computed based on some file at build time.
	HASH string
)

const (
	envSentryStartDelay   = "SENTRY_START_DELAY"
	envSentryPollInterval = "SENTRY_POLL_INTERVAL"
)

func main() {

	if len(os.Args) != 3 {
		log.Fatal("Must have two args:" + os.Args[0] + " config_file listen_port")
	}

	pwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	config := map[string]string{}

	if os.Args[1] != "-" {
		// Filepath as given
		bytes, err := ioutil.ReadFile(os.Args[1])

		if err != nil {

			// relative to current working directory
			parent := filepath.Dir(os.Args[0])
			bytes, err = ioutil.ReadFile(filepath.Join(parent, os.Args[1]))

			if err != nil {

				// try working directory
				bytes, err = ioutil.ReadFile(filepath.Join(pwd, os.Args[1]))

				if err != nil {
					log.Fatal(err)
				}

			}
		}

		if err := json.Unmarshal(bytes, &config); err != nil {
			log.Fatal(err)
		}
	}

	sentry := &sentry{
		targets: map[fsm.ID]string{},
		fsms:    map[string]fsm.FSM{},
		pwd:     pwd,
		listen:  os.Args[2],
		config:  config,
		stopC:   make(chan interface{}),
	}

	if delay := os.Getenv(envSentryStartDelay); delay == "" {
		sentry.delay = 3 * time.Second
	} else if sentry.delay, err = time.ParseDuration(delay); err != nil {
		log.Fatal(err)
	}

	if tick := os.Getenv(envSentryPollInterval); tick == "" {
		sentry.tickC = time.Tick(1 * time.Second)
	} else if t, err := time.ParseDuration(tick); err != nil {
		log.Fatal(err)
	} else {
		sentry.tickC = time.Tick(t)
	}

	if err := sentry.initialize(); err != nil {
		log.Fatal(err)
	}

	sentry.handleHTTP()

	log.Print("Starting up server with config of ", len(sentry.config), " targets.")
	log.Print("Server has start up delay of ", sentry.delay)

	time.Sleep(sentry.delay)

	go sentry.poll()

	log.Print("Listening on: ", sentry.listen)
	log.Fatal(http.ListenAndServe(sentry.listen, nil))
}

type serverError struct {
	*http.Response
}

func (e *serverError) Error() string {
	return fmt.Sprintf("HTTP_STATUS[%v]", e.Response.StatusCode)
}

type sentry struct {
	machines    fsm.Machines
	fsms        map[string]fsm.FSM
	targets     map[fsm.ID]string
	pwd         string
	listen      string
	config      map[string]string
	delay       time.Duration
	maintenance bool

	lock sync.RWMutex

	tickC <-chan time.Time
	stopC chan interface{}
}

const (
	targetUnknown fsm.Index = iota
	targetRunning
	targetError
	targetDown
	targetProvisioning
	targetStopping

	foundRunning fsm.Signal = iota
	foundError
	foundDown
	doKill
	doProvision
)

func (a *sentry) initialize() (err error) {
	a.machines, err = fsm.Define(
		fsm.State{
			Index: targetUnknown,
			Transitions: map[fsm.Signal]fsm.Index{
				foundRunning: targetRunning,
				foundError:   targetError,
				foundDown:    targetDown,
			},
		},
		fsm.State{
			Index: targetRunning,
			Transitions: map[fsm.Signal]fsm.Index{
				foundError: targetError,
				foundDown:  targetDown,
			},
		},
		fsm.State{
			Index: targetError,
			Transitions: map[fsm.Signal]fsm.Index{
				foundRunning: targetRunning,
				foundDown:    targetDown,
				doKill:       targetStopping,
			},
			TTL: fsm.Expiry{TTL: 5, Raise: doKill},
			Actions: map[fsm.Signal]fsm.Action{
				doKill: func(f fsm.FSM) error {

					target, has := a.targets[f.ID()]
					if !has {
						log.Fatal("Missing target for ID ", f.ID())
					}

					url := target + "/kill"
					log.Println(">>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>> kill: ", url)

					_, err := http.Get(url)
					return err
				},
			},
		},
		fsm.State{
			Index: targetDown,
			Transitions: map[fsm.Signal]fsm.Index{
				foundRunning: targetRunning,
				foundError:   targetError,
				doProvision:  targetRunning,
			},
			TTL: fsm.Expiry{TTL: 5, Raise: doProvision},
			Actions: map[fsm.Signal]fsm.Action{
				doProvision: func(f fsm.FSM) error {

					target, has := a.targets[f.ID()]
					if !has {
						log.Fatal("Missing target for ID ", f.ID())
					}
					log.Println(">>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>> provision: ", target)

					return a.provision(target)
				},
			},
		},
		fsm.State{
			Index: targetProvisioning,
			Transitions: map[fsm.Signal]fsm.Index{
				foundRunning: targetRunning,
				foundError:   targetError,
			},
		},
		fsm.State{
			Index: targetStopping,
			Transitions: map[fsm.Signal]fsm.Index{
				foundError: targetError,
				foundDown:  targetDown,
			},
		},
	)
	if err != nil {
		return err
	}

	options := fsm.DefaultOptions()
	options.StateNames = map[fsm.Index]string{
		targetUnknown:      "UNKNOWN",
		targetRunning:      "RUNNING",
		targetError:        "ERROR",
		targetDown:         "DOWN",
		targetProvisioning: "PROVISIONING",
		targetStopping:     "STOPPING",
	}
	options.SignalNames = map[fsm.Signal]string{
		foundRunning: "running",
		foundError:   "error",
		foundDown:    "down",
	}

	a.machines.Run(fsm.Wall(time.Tick(2*time.Second)), options)

	// for each target create an instance
	for target := range a.config {

		f, err := a.machines.New(targetUnknown)
		if err != nil {
			log.Fatal(err)
		}

		a.fsms[target] = f
		a.targets[f.ID()] = target
	}

	return nil
}

func (a *sentry) states() (states map[string]interface{}) {
	states = map[string]interface{}{}

	for target := range a.fsms {
		states[target] = a.machines.StateStringer(a.fsms[target].State())
	}
	return
}

func (a *sentry) handleHTTP() {

	http.HandleFunc("/",
		func(w http.ResponseWriter, r *http.Request) {
			log.Println(a.listen, "- PING", r.RemoteAddr)
			a.lock.RLock()
			defer a.lock.RUnlock()

			if a.maintenance {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf("%s,%s,%s", VERSION, REVISION, HASH)))
		},
	)
	http.HandleFunc("/kill",
		func(w http.ResponseWriter, r *http.Request) {
			log.Println("kill", r.RemoteAddr)
			close(a.stopC)
		},
	)
	http.HandleFunc("/maintenance",
		func(w http.ResponseWriter, r *http.Request) {
			log.Println("maintenance", r.RemoteAddr)
			a.lock.Lock()
			defer a.lock.Unlock()
			a.maintenance = !a.maintenance
		},
	)
	http.HandleFunc("/provision",
		func(w http.ResponseWriter, r *http.Request) {
			target := r.URL.Query().Get("target")
			if target == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			go a.provision(target)
		},
	)
	http.HandleFunc("/state",
		func(w http.ResponseWriter, r *http.Request) {
			log.Println("state", r.RemoteAddr)

			keys := []string{}
			all := a.states()
			for k := range all {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(w, "%s\t%s\n", k, all[k])
			}
		},
	)
}

func (a *sentry) poll() {

	targets := []string{}
	for k := range a.config {
		targets = append(targets, k)
	}
	sort.Strings(targets)
	log.Println("Polling for ", len(targets), " targets")

loop:
	for i := 0; ; i++ {

		select {
		case <-a.tickC:

			if len(a.config) == 0 {
				continue loop
			}

			target := targets[i%len(targets)]

			log.Println(target, "- Polling")

			resp, err := http.Get(target)
			if err == nil {

				switch resp.StatusCode {

				case http.StatusOK:
					log.Println(target, "- OK")
					a.signal(target, foundRunning)
					continue loop

				default:
					err = &serverError{resp}
				}
			}

			switch err := err.(type) {
			case *serverError:
				log.Println(target, "- SERVER_ERROR, err=", err)
				a.signal(target, foundError)

			case net.Error:
				log.Println(target, "- NETWORK_ERROR, timeout=", err.Timeout(), ", temporary=", err.Temporary())
				a.signal(target, foundDown)

			default:
				log.Println(target, "- UNKNOWN_ERROR, err=", err)
				a.signal(target, foundError)
			}
		case <-a.stopC:
			break loop
		}
	}

	log.Println("Stopped")
	os.Exit(0)

}

func (a *sentry) signal(target string, signal fsm.Signal) error {
	fsm, has := a.fsms[target]
	if !has {
		log.Println(target, "- NOT FOUND")
		return nil
	}
	return fsm.Signal(signal)
}

func (a *sentry) provision(target string) error {
	cmd, has := a.config[target]
	if !has {
		return fmt.Errorf("Target not found %v", target)
	}

	p := strings.Split(cmd, " ")

	if p[0] == "build/sentry" {
		// !!!! This works only if you run from the examples directory !!!
		p[0] = filepath.Join(a.pwd, p[0]) // make sure the path exists
	}

	x := exec.Command(p[0], p[1:]...)
	x.Dir = a.pwd

	log.Println(target, "- PROVISION, cmd=", cmd, "with", x.Path, x.Args)

	if err := x.Start(); err != nil {
		out, _ := x.CombinedOutput()
		log.Println(target, "- ERROR_PROVISION, err=", err, "out=", string(out))
		return err
	}

	return nil
}
