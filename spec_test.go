package fsm // import "github.com/orkestr8/fsm"

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuild1(t *testing.T) {

	const (
		turnOn Signal = iota
		turnOff

		on Index = iota
		off
	)

	m := map[Index]State{
		on: {
			Index: on,
			Transitions: map[Signal]Index{
				turnOff: off,
			},
		},
	}

	_, err := newSpec().compile(m)
	require.Error(t, err)

	// add missing
	m[off] = State{
		Index: off,
		Transitions: map[Signal]Index{
			turnOn: on,
		},
		Visit: Limit{5, turnOn},
	}

	_, err = newSpec().compile(m)
	require.NoError(t, err)

	states := []State{}
	for _, s := range m {
		states = append(states, s)
	}

	machines, err := define(states[0], states[1:]...)
	require.NoError(t, err)
	require.Equal(t, 2, len(machines.spec.signals))
	require.Equal(t, 2, len(machines.spec.states))

	spec := machines.spec.compileFlappingMust([]Flap{
		{States: [2]Index{on, off}, Count: 100},
	})

	limit, err := spec.visit(off)
	require.NoError(t, err)
	require.Equal(t, 5, limit.Value)
	require.Equal(t, turnOn, limit.Raise)

	limit, err = spec.visit(on)
	require.NoError(t, err)
	require.Nil(t, limit)

	require.Equal(t, 1, len(spec.flaps))
	t.Log(spec)
}

func TestBuild2(t *testing.T) {

	const (
		on Index = iota
		off
		sleep
	)

	const (
		turnOn Signal = iota
		turnOff
		unplug
	)

	saidHi := make(chan struct{})
	var sayHi Action = func(FSM) error {
		close(saidHi)
		return nil
	}
	saidBye := make(chan struct{})
	var sayBye Action = func(FSM) error {
		close(saidBye)
		return nil
	}

	spec := newSpec()
	spec, err := spec.build(
		State{
			Index: off,
			Transitions: map[Signal]Index{
				turnOn: on,
			},
			Actions: map[Signal]Action{
				turnOn: sayHi,
			},
		},
		State{
			Index: on,
			Transitions: map[Signal]Index{
				turnOff: sleep,
				unplug:  off,
			},
			Actions: map[Signal]Action{
				turnOff: sayBye,
			},
		},
		State{
			Index: sleep,
			Transitions: map[Signal]Index{
				turnOn:  on,
				turnOff: off,
				unplug:  off,
			},
			Actions: map[Signal]Action{
				turnOn:  sayHi,
				turnOff: sayBye,
			},
		},
	)

	require.NoError(t, err)

	// check transitions
	next, action, err := spec.transition(on, turnOff)
	require.NoError(t, err)
	require.Equal(t, sleep, next)
	action(nil)
	<-saidBye

	// check transitions
	next, action, err = spec.transition(off, turnOn)
	require.NoError(t, err)
	require.Equal(t, on, next)
	action(nil)
	<-saidHi

	// not allowed transition
	_, _, err = spec.transition(on, turnOn)
	require.Error(t, err)
}
