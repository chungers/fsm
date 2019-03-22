package fsm // import "github.com/orkestr8/fsm"

import (
	"fmt"
)

// ErrDuplicateState is thrown when there are indexes of the same value
type ErrDuplicateState struct {
	*spec
	Index
}

func (e ErrDuplicateState) Error() string {
	return fmt.Sprintf("duplicated state index: %v", e.spec.stateName(e.Index))
}

// ErrUnknownState indicates the state referenced does not match a known state index
type ErrUnknownState struct {
	*spec
	Index
}

func (e ErrUnknownState) Error() string {
	return fmt.Sprintf("unknown state: %v", e.spec.stateName(e.Index))
}

// ErrUnknownTransition indicates an unknown signal while in given state is raised
type ErrUnknownTransition struct {
	spec   *spec
	Signal Signal
	State  Index
	Help   string
}

func (e ErrUnknownTransition) Error() string {
	return fmt.Sprintf("unknown stransition: signal=%v, state=%v", e.spec.signalName(e.Signal), e.spec.stateName(e.State))
}

// ErrUnknownSignal is raised when a undefined signal is received in the given state
type ErrUnknownSignal struct {
	spec *spec
	Signal
	Index
	Help string
}

func (e ErrUnknownSignal) Error() string {
	return fmt.Sprintf("unknown signal: signal=%v, state=%v", e.spec.signalName(e.Signal), e.spec.stateName(e.Index))
}

// ErrUnknownFSM is raised when the ID is does not match any thing in the set
type ErrUnknownFSM ID

func (e ErrUnknownFSM) Error() string {
	return fmt.Sprintf("unknown instance: %v", e)
}

// ErrNilAction is raised when an action is nil
type ErrNilAction Signal

func (e ErrNilAction) Error() string {
	return fmt.Sprintf("nil action corresponding to signal %d", e)
}

// ErrNoTransitions is raised when there are no transitions defined
type ErrNoTransitions spec

func (e ErrNoTransitions) Error() string {
	return fmt.Sprintf("no transitions defined: count(states)=%d", len(e.states))
}
