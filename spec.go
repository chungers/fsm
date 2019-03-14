package fsm // import "github.com/orkestr8/fsm"

import (
	"fmt"
)

// spec is a specification of all the rules for the fsm
type spec struct {
	states  map[Index]State
	signals map[Signal]Signal
	flaps   map[[2]Index]*Flap

	stateNames  map[Index]string  // optional
	signalNames map[Signal]string // optional
}

func newSpec() *spec {
	return &spec{
		states:  map[Index]State{},
		signals: map[Signal]Signal{},
		flaps:   map[[2]Index]*Flap{},
	}
}

// define performs basic validation, consistency checks and returns a compiled spec.
func (s *spec) build(state State, more ...State) (*spec, error) {
	states := map[Index]State{
		state.Index: state,
	}

	for _, st := range more {
		if _, has := states[st.Index]; has {
			err := ErrDuplicateState(st.Index)
			return s, err
		}
		states[st.Index] = st
	}

	// check referential integrity
	signals, err := s.compile(states)
	if err != nil {
		return s, err
	}

	s.states = states
	s.signals = signals
	return s, err
}

func (s *spec) compile(m map[Index]State) (map[Signal]Signal, error) {

	signals := map[Signal]Signal{}

	for _, st := range m {
		for _, transfer := range []map[Signal]Index{
			st.Transitions,
			st.Errors,
		} {
			for signal, next := range transfer {
				if _, has := m[next]; !has {
					return nil, ErrUnknownState(next)
				}
				signals[signal] = signal
			}
		}
	}

	// all signals must be known here

	for _, st := range m {
		// Check all the signal references in Actions must be in transitions
		for signal, action := range st.Actions {
			if _, has := st.Transitions[signal]; !has {
				return nil, ErrUnknownTransition{spec: s, Signal: signal, State: st.Index}
			}

			if action == nil {
				return nil, ErrNilAction(signal)
			}

			if _, has := signals[signal]; !has {
				return nil, ErrUnknownSignal{Signal: signal, State: st.Index}
			}
		}
	}

	// what's raised in the TTL and in the Visit limit must be defined as well

	for _, st := range m {
		if st.TTL.TTL > 0 {
			if _, has := st.Transitions[st.TTL.Raise]; !has {
				return nil, ErrUnknownSignal{
					spec: s, Signal: st.TTL.Raise, State: st.Index,
					Help: "expiry raises signal that's not in state's transitions",
				}
			}

			// register as valid signal
			signals[st.TTL.Raise] = st.TTL.Raise

		}
		if st.Visit.Value > 0 {
			if _, has := st.Transitions[st.Visit.Raise]; !has {
				return nil, ErrUnknownSignal{
					spec: s, Signal: st.Visit.Raise, State: st.Index,
					Help: "visit limit raises signal that's not in state's transitions",
				}
			}

			// register as valid signal
			signals[st.Visit.Raise] = st.Visit.Raise
		}
	}

	return signals, nil
}

// StateName returns the friendly name of the state, if defined
func (s *spec) stateName(i Index) (name string) {
	name = fmt.Sprintf("%v", i)
	if s == nil {
		return
	}
	if s.stateNames == nil {
		return
	}
	if v, has := s.stateNames[i]; has {
		name = v
	}
	return
}

// SignalName returns the friendly name of the signal, if defined
func (s *spec) signalName(signal Signal) (name string) {
	name = fmt.Sprintf("%v", signal)
	if s == nil {
		return
	}

	if s.signalNames == nil {
		return
	}
	if v, has := s.signalNames[signal]; has {
		name = v
	}
	return
}

// SetAction sets the action associated with a signal in a given state
func (s *spec) SetAction(state Index, signal Signal, action Action) error {
	st, has := s.states[state]
	if !has {
		return fmt.Errorf("no such state %v", state)
	}
	if st.Actions == nil {
		st.Actions = map[Signal]Action{}
	}
	st.Actions[signal] = action
	s.states[state] = st // Update the map because the map returned a copy of the state.
	return nil
}

// returns an expiry for the state.  if the TTL is 0 then there's no expiry for the state.
func (s *spec) expiry(current Index) (expiry *Expiry, err error) {
	state, has := s.states[current]
	if !has {
		err = ErrUnknownState(current)
		return
	}
	if state.TTL.TTL > 0 {
		expiry = &state.TTL
	}
	return
}

// returns the limit on visiting this state
func (s *spec) visit(next Index) (limit *Limit, err error) {
	state, has := s.states[next]
	if !has {
		err = ErrUnknownState(next)
		return
	}

	if state.Visit.Value > 0 {
		limit = &state.Visit
	}
	return
}

// returns an error handling rule
func (s *spec) error(current Index, signal Signal) (next Index, err error) {
	state, has := s.states[current]
	if !has {
		err = ErrUnknownState(current)
		return
	}

	_, has = s.signals[signal]
	if !has {
		err = ErrUnknownSignal{Signal: signal, State: current}
		return
	}

	v, has := state.Errors[signal]
	if !has {
		err = ErrUnknownTransition{Signal: signal, State: current}
		return
	}
	next = v
	return
}

// transition takes the fsm from a current state, with given signal, to the next state.
// returns error if the transition is not possible.
func (s *spec) transition(current Index, signal Signal) (next Index, action Action, err error) {

	next = -1

	state, has := s.states[current]
	if !has {
		err = ErrUnknownState(current)
		return
	}

	if len(state.Transitions) == 0 {
		err = ErrNoTransitions(*s)
		return
	}

	_, has = s.signals[signal]
	if !has {
		err = ErrUnknownSignal{Signal: signal}
		return
	}

	n, has := state.Transitions[signal]
	if !has {
		err = ErrUnknownTransition{Signal: signal, State: state.Index}
		return
	}
	next = n

	if a, has := state.Actions[signal]; has {
		action = a
	}

	return
}
