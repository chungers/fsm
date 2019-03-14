package fsm // import "github.com/orkestr8/fsm"

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsage(t *testing.T) {

	const (
		signalSpecified Signal = iota
		signalCreate
		signalFound
		signalHealthy
		signalUnhealthy
		signalStartOver
		signalStop

		specified Index = iota
		creating
		up
		running
		down
		decommissioned
	)

	createFSM := func(FSM) error {
		t.Log("creating instance")
		return nil
	}
	deleteFSM := func(FSM) error {
		t.Log("delete instance")
		return nil
	}
	cleanup := func(FSM) error {
		t.Log("cleanup")
		return nil
	}
	recordFlapping := func(FSM) error {
		t.Log("flap is if this happens more than multiples of 2 calls")
		return nil
	}
	sendAlert := func(FSM) error {
		t.Log("alert")
		return nil
	}

	machines, err := define(
		State{
			Index: specified,
			Transitions: map[Signal]Index{
				signalCreate:    creating,
				signalFound:     up,
				signalHealthy:   running,
				signalUnhealthy: down,
			},
			Actions: map[Signal]Action{
				signalCreate: createFSM,
			},
			TTL: Expiry{1000, signalCreate},
		},
		State{
			Index: creating,
			Transitions: map[Signal]Index{
				signalFound:     up,
				signalStartOver: specified,
			},
			Actions: map[Signal]Action{
				signalStartOver: cleanup,
			},
			TTL: Expiry{1000, signalStartOver},
		},
		State{
			Index: up,
			Transitions: map[Signal]Index{
				signalHealthy:   running,
				signalUnhealthy: down,
			},
			Actions: map[Signal]Action{
				signalUnhealthy: recordFlapping, // note flapping between up and down
			},
		},
		State{
			Index: down,
			Transitions: map[Signal]Index{
				signalStartOver: specified,
				signalHealthy:   running,
			},
			Actions: map[Signal]Action{
				signalStartOver: cleanup,
				signalHealthy:   recordFlapping, // note flapping between up and down
			},
			TTL: Expiry{10, signalStartOver},
		},
		State{
			Index: running,
			Transitions: map[Signal]Index{
				signalHealthy:   running,
				signalUnhealthy: down, // do we want threshold e.g. more than N signals?
				signalStop:      decommissioned,
			},
			Actions: map[Signal]Action{
				signalUnhealthy: sendAlert,
				signalStop:      deleteFSM,
			},
		},
		State{
			Index: decommissioned,
		},
	)

	require.NoError(t, err)

	options := DefaultOptions()
	options.StateNames = map[Index]string{
		specified:      "specified",
		creating:       "creating",
		up:             "up",
		running:        "running",
		down:           "down",
		decommissioned: "decommissioned",
	}
	options.SignalNames = map[Signal]string{
		signalSpecified: "specified",
		signalCreate:    "create",
		signalFound:     "found",
		signalHealthy:   "healthy",
		signalUnhealthy: "unhealthy",
		signalStartOver: "start_over",
		signalStop:      "stop",
	}
	options.Limits = []Flap{
		{States: [2]Index{running, down}, Count: 10},
	}

	clock := Wall(time.Tick(1 * time.Second))

	// gp is a collection of fsm intances that follow the same rules.
	gp, err := newRunner(machines.spec, clock, options)
	require.NoError(t, err)
	gp.run()

	// allocates a new instance of a fsm with an initial state.
	instance, err := gp.alloc(specified)
	require.NotNil(t, instance)
	require.NoError(t, err)

	gp.Stop()
}
