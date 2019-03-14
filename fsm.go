package fsm // import "github.com/orkestr8/fsm"

// Define performs basic validation, consistency checks and returns a compiled spec.
func Define(s State, more ...State) (m Machines, err error) {
	return define(s, more...)
}

// define performs basic validation, consistency checks and returns a compiled spec.
func define(s State, more ...State) (m *machines, err error) {
	spec := newSpec()
	spec, err = spec.build(s, more...)
	if err != nil {
		return nil, err
	}

	return &machines{
		spec:   spec,
		States: append([]State{s}, more...),
	}, nil
}
