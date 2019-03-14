package fsm // import "github.com/orkestr8/fsm"

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func first(a, b interface{}) interface{} {
	return a
}

func TestSetDeadlineTransition(t *testing.T) {

	const (
		running Index = iota
		wait
	)

	const (
		start Signal = iota
	)

	started := 0
	startAction := func(FSM) error {
		started++
		return nil
	}

	machines, err := define(
		State{
			Index: wait,
			Transitions: map[Signal]Index{
				start: running,
			},
			Actions: map[Signal]Action{
				start: startAction,
			},
			TTL: Expiry{5, start},
		},
		State{
			Index: running,
		},
	)

	require.NoError(t, err)

	options := DefaultOptions()
	options.StateNames = map[Index]string{
		running: "running",
		wait:    "wait",
	}
	options.SignalNames = map[Signal]string{
		start: "start",
	}

	clock := NewClock()

	// gp is a collection of fsm intances that follow the same rules.
	gp, err := newRunner(machines.spec, clock, options)
	require.NoError(t, err)
	gp.run()

	stats := func(instances []FSM) (waiters int, runners int) {
		for i := 0; i < len(instances); i++ {
			switch instances[i].State() {
			case wait:
				waiters += 1
			case running:
				runners += 1
			}
		}
		return
	}

	defer gp.Stop()

	// add a few instances
	instances := []FSM{}

	for i := 0; i < 100; i++ {
		n, err := gp.alloc(wait)
		require.NoError(t, err)
		instances = append(instances, n)
	}

	// Expect all 100 to be added to the deadlines queue
	require.Equal(t, 100, len(instances))
	require.Equal(t, 100, gp.deadlines.Len())

	// Expect every one in wait state
	for _, instance := range instances {
		require.Equal(t, wait, instance.State())
	}

	// advance the clock
	clock.Tick() // t = 1

	waiters, runners := stats(instances)
	require.Equal(t, 100, waiters)
	require.Equal(t, 0, runners)
	require.Equal(t, 100, gp.deadlines.Len())

	clock.Tick() // t = 2

	// transition a few instances
	for i := 10; i < 20; i++ {

		instance := instances[i]

		if state := instance.State(); state == wait {
			require.NoError(t, instance.Signal(start))
		}
	}

	waiters, runners = stats(instances)
	require.Equal(t, 10, runners)
	require.Equal(t, 90, waiters)

	clock.Tick() // t = 3

	waiters, runners = stats(instances)
	require.Equal(t, 10, runners)
	require.Equal(t, 90, waiters)

	clock.Tick() // t = 4

	waiters, runners = stats(instances)
	require.Equal(t, 10, runners)
	require.Equal(t, 90, waiters)

	clock.Tick() // t = 5

	time.Sleep(3 * time.Second) // give a little time for the gp to settle

	waiters, runners = stats(instances)
	require.Equal(t, 100, runners)
	require.Equal(t, 0, waiters)
	require.Equal(t, 0, gp.deadlines.Len())

	clock.Tick() // t = 6

	waiters, runners = stats(instances)
	require.Equal(t, 100, runners)
	require.Equal(t, 0, waiters)

}

func TestSetFlapping(t *testing.T) {

	const (
		boot Index = iota
		running
		down
		cordoned
	)

	const (
		start Signal = iota
		ping
		timeout
		cordon
	)

	machines, err := define(
		State{
			Index: boot,
			Transitions: map[Signal]Index{
				start: running,
			},
			TTL: Expiry{3, start},
		},
		State{
			Index: running,
			Transitions: map[Signal]Index{
				timeout: down,
				cordon:  cordoned,
			},
		},
		State{
			Index: down,
			Transitions: map[Signal]Index{
				ping:   running,
				cordon: cordoned,
			},
		},
		State{
			Index: cordoned,
		},
	)
	require.NoError(t, err)

	spec := machines.spec

	_, err = spec.compileFlapping([]Flap{
		{States: [2]Index{running, down}, Count: 3, Raise: cordon},
	})
	require.NoError(t, err)

	clock := NewClock()

	// gp is a collection of fsm intances that follow the same rules.
	gp, err := newRunner(spec, clock, Options{
		IgnoreUndefinedStates:      true,
		IgnoreUndefinedSignals:     true,
		IgnoreUndefinedTransitions: true,
	})

	require.NoError(t, err)
	gp.run()

	defer gp.Stop()

	instances := []FSM{}
	stats := func() (out map[Index]int) {
		out = map[Index]int{}
		for i := 0; i < len(instances); i++ {
			s := instances[i].State()
			if _, has := out[s]; !has {
				out[s] = 0
			}
			out[s] += 1
		}
		return
	}

	// Add an instance
	instance, err := gp.alloc(boot)
	require.NoError(t, err)
	instances = append(instances, instance)

	require.Equal(t, boot, instance.State())
	require.Equal(t, 1, stats()[boot])

	clock.Tick()
	clock.Tick()
	clock.Tick()

	// A slight delay here to let states settle
	time.Sleep(100 * time.Millisecond)

	require.Equal(t, 1, stats()[running])
	require.Equal(t, running, instance.State())

	t.Log("************************* running -> down")

	instance.Signal(timeout) // flap 1 - a

	require.Equal(t, 1, stats()[down])
	require.Equal(t, down, instance.State())
	clock.Tick()

	require.Equal(t, 1, stats()[down])
	require.Equal(t, down, instance.State())

	clock.Tick()

	t.Log("************************* down -> running")

	instance.Signal(ping) // flap 1 - b

	require.Equal(t, 1, stats()[running])
	require.Equal(t, running, instance.State())

	t.Log("************************* running -> down")

	instance.Signal(timeout) // flap 2

	require.Equal(t, 1, stats()[down])
	require.Equal(t, down, instance.State())

	t.Log("************************* running -> down")

	require.False(t, instance.CanReceive(timeout))

	err = instance.Signal(timeout)
	require.NoError(t, err) // This does no checking

	t.Log("************************* down -> running")

	instance.Signal(ping) // flap 2

	t.Log("************************* running -> down")

	instance.Signal(timeout) // flap 2

	t.Log("************************* down -> running")

	instance.Signal(ping) // flap 3

	t.Log("************************* running -> down")

	instance.Signal(timeout) // flap 3

	instance.Signal(ping) // flap 3

	// note that there's a transition that will be triggered
	time.Sleep(500 * time.Millisecond)

	require.Equal(t, 0, stats()[running])
	require.Equal(t, 1, stats()[cordoned])
	require.Equal(t, cordoned, instance.State())

	gp.Stop()
}

func TestMaxVisits(t *testing.T) {
	const (
		up Index = iota
		down
		unavailable
	)

	const (
		startup Signal = iota
		shutdown
		error
	)

	machines, err := define(
		State{
			Index: up,
			Transitions: map[Signal]Index{
				shutdown: down,
			},
		},
		State{
			Index: down,
			Transitions: map[Signal]Index{
				startup: up,
				error:   unavailable,
			},
			Visit: Limit{2, error},
		},
		State{
			Index: unavailable,
		},
	)

	require.NoError(t, err)
	spec := machines.spec

	options := DefaultOptions()
	options.SignalNames = map[Signal]string{
		startup:  "start_up",
		shutdown: "shut_down",
	}

	options.StateNames = map[Index]string{
		up:   "UP",
		down: "DOWN",
	}

	clock := Wall(time.Tick(1 * time.Second))

	// gp is a collection of fsm intances that follow the same rules.
	gp, err := newRunner(spec, clock, options)

	require.NoError(t, err)
	gp.run()

	require.Equal(t, "start_up", spec.signalName(startup))
	require.Equal(t, "2", spec.signalName(error))

	require.Equal(t, "UP", spec.stateName(up))
	require.Equal(t, "2", spec.stateName(unavailable))

	defer gp.Stop()

	instance, err := gp.alloc(up)
	require.NoError(t, err)

	err = instance.Signal(shutdown)
	require.NoError(t, err)
	require.Equal(t, down, instance.State()) // 1

	err = instance.Signal(startup)
	require.NoError(t, err)
	require.Equal(t, up, instance.State())

	err = instance.Signal(shutdown)
	require.NoError(t, err)
	require.Equal(t, down, instance.State()) // 2

	// then automatically triggered to the unavailable state
	require.Equal(t, unavailable, instance.State())
}

func TestActionErrors(t *testing.T) {
	const (
		up Index = iota
		retrying
		down
		unavailable
	)

	const (
		startup Signal = iota
		shutdown
		warn
		cordon
	)

	machines, err := define(
		State{
			Index: up,
			Transitions: map[Signal]Index{
				shutdown: down,
			},
		},
		State{
			Index: down,
			Transitions: map[Signal]Index{
				startup: up,
				warn:    retrying,
				cordon:  unavailable,
			},
			Actions: map[Signal]Action{
				startup: func(FSM) error {
					return fmt.Errorf("error")
				},
			},
			Errors: map[Signal]Index{
				startup: retrying,
			},
			Visit: Limit{2, cordon},
		},
		State{
			Index: retrying,
			Transitions: map[Signal]Index{
				warn:    retrying,
				startup: up,
				cordon:  unavailable,
			},
			Actions: map[Signal]Action{
				startup: func(FSM) error {
					return fmt.Errorf("error- retrying")
				},
			},
			Errors: map[Signal]Index{
				startup: retrying,
			},
			Visit: Limit{2, cordon},
		},
		State{
			Index: unavailable,
		},
	)
	require.NoError(t, err)

	spec := machines.spec

	clock := Wall(time.Tick(1 * time.Second))

	// gp is a collection of fsm intances that follow the same rules.
	gp, err := newRunner(spec, clock, Options{
		StateNames: map[Index]string{
			up:          "up",
			retrying:    "retrying",
			down:        "down",
			unavailable: "unavailable",
		},
		SignalNames: map[Signal]string{
			startup:  "start_up",
			shutdown: "shut_down",
			warn:     "warn",
			cordon:   "cordon",
		},
		IgnoreUndefinedTransitions: true,
	})
	require.NoError(t, err)
	gp.run()

	defer gp.Stop()

	instance, err := gp.alloc(up)
	require.NoError(t, err)

	err = instance.Signal(shutdown)
	require.NoError(t, err)
	require.Equal(t, down, instance.State())

	err = instance.Signal(startup)
	require.NoError(t, err)
	require.Equal(t, retrying, instance.State()) // visit 1

	// try 1
	err = instance.Signal(startup)
	require.NoError(t, err)
	require.Equal(t, retrying, instance.State()) // visit 2

	// try 2
	err = instance.Signal(startup)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// then automatically triggered to the unavailable state
	require.Equal(t, unavailable, instance.State())

	t.Log("stopping")
}
