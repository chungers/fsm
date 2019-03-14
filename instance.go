package fsm // import "github.com/orkestr8/fsm"

import (
	"sync"
)

// implements FSM interface
type instance struct {
	id       ID
	state    Index
	data     interface{}
	parent   *runner
	error    error
	flaps    flaps
	start    Time
	deadline Time
	index    int // index used in the deadlines queue
	visits   map[Index]int

	lock sync.RWMutex
}

// ID returns the ID of the fsm instance
func (i *instance) ID() ID {
	i.lock.RLock()
	defer i.lock.RUnlock()
	return i.id
}

// Data returns a customer data value attached to this instance
func (i *instance) Data() interface{} {
	i.lock.RLock()
	defer i.lock.RUnlock()
	return i.data
}

const invalidState Index = -99999

// IsInvalidState returns true if the index is invalid
func IsInvalidState(s Index) bool {
	return s == invalidState
}

// State returns the state of the fsm instance
func (i *instance) State() (result Index) {
	done := make(chan struct{})

	result = invalidState

	// queue this and get a snapshot so that the read is consistent
	i.parent.reads <- func(view *runner) {
		defer close(done)
		result = i.state
	}
	<-done // finish waiting
	return
}

// Valid returns true if current state can receive the given signal
func (i *instance) CanReceive(s Signal) bool {
	_, _, err := i.parent.spec.transition(i.State(), s)
	return err == nil
}

// Signal sends a signal to the instance
func (i *instance) Signal(s Signal, optionalData ...interface{}) (err error) {
	return i.parent.signal(s, i, optionalData...)
}

func (i *instance) update(next Index, now Time, ttl Tick) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if i.visits == nil {
		i.visits = map[Index]int{}
	}

	i.visits[next] = i.visits[next] + 1
	i.state = next
	i.start = now
	if ttl > 0 {
		i.deadline = now + Time(ttl)
	} else {
		i.deadline = 0
	}
}
