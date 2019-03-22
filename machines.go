package fsm // import "github.com/orkestr8/fsm"

import (
	"fmt"
)

type machines struct {
	*spec
	Options
	States []State

	clock  *Clock
	runner *runner
}

func (m *machines) New(initial Index) (FSM, error) {
	return m.runner.alloc(initial)
}

func (m *machines) Run(clock *Clock, options Options) error {

	m.Options = options

	m.clock = clock
	runner, err := newRunner(m.spec, m.clock, m.Options)
	if err != nil {
		return err
	}
	m.runner = runner
	m.runner.run()
	m.runner.running = true

	m.clock.Start()
	return nil
}

func (m *machines) Done() {
	if m.runner == nil {
		panic("Programming error. Must call Run() before Done()")
	}

	m.runner.Stop()
}

type stringer string

func (s stringer) GoString() string {
	return string(s)
}

func (m *machines) StateStringer(i Index) fmt.GoStringer {
	return stringer(m.spec.stateName(i))
}

func (m *machines) SignalStringer(s Signal) fmt.GoStringer {
	return stringer(m.spec.signalName(s))
}
