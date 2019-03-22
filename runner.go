package fsm // import "github.com/orkestr8/fsm"

import (
	"fmt"
	"time"
)

const (
	defaultBufferSize = 1 << 8
)

// runner manages the channels used to receive state transition signals
type runner struct {
	options      Options
	reads        chan func(*runner) // given a view which is a copy of the runner
	spec         spec
	now          Time
	next         ID
	clock        *Clock
	stop         chan struct{}
	errors       chan error
	events       chan *event
	transactions chan *txn
	deadlines    *queue
	running      bool
	log          Logger
}

func newRunner(spec *spec, clock *Clock, optional ...Options) (*runner, error) {

	options := Options{}
	if len(optional) > 0 {
		options = optional[0]
	}

	if options.BufferSize == 0 {
		options.BufferSize = defaultBufferSize
	}

	if len(options.StateNames) > 0 {
		spec.stateNames = options.StateNames
	}
	if len(options.SignalNames) > 0 {
		spec.signalNames = options.SignalNames
	}
	if len(options.Limits) > 0 {
		_, err := spec.compileFlapping(options.Limits)
		if err != nil {
			return nil, err
		}
	}

	logger := options.Logger
	if logger == nil {
		logger = &nilLogger{}
	}

	gp := &runner{
		log:          logger,
		options:      options,
		spec:         *spec,
		stop:         make(chan struct{}),
		clock:        clock,
		reads:        make(chan func(*runner)),
		errors:       make(chan error),
		events:       make(chan *event),
		transactions: make(chan *txn, options.BufferSize),
		deadlines:    newQueue(),
	}

	// TODO - add validation error here
	return gp, nil
}

// Stop stops the state machine loop
func (g *runner) Stop() {
	if g.running {
		close(g.stop)
		g.clock.Stop()
		g.running = false
	}
}

// Errors returns the errors encountered during async processing of events
func (g *runner) Errors() <-chan error {
	return g.errors
}

type event struct {
	instance ID
	ref      *instance
	signal   Signal
	data     []interface{}
}

func (g *runner) handleError(tid int64, err error, ctx interface{}) {

	message := err.Error()
	switch err := err.(type) {
	case ErrUnknownState:
		if g.options.IgnoreUndefinedStates {
			return
		}
		message = fmt.Sprintf("Unknown: %v", err)

	case ErrUnknownTransition:
		if g.options.IgnoreUndefinedTransitions {
			return
		}
		message = fmt.Sprintf("%s: state(%v) on signal(%v)", err.Error(),
			g.spec.stateName(err.State), g.spec.signalName(err.Signal))

	case ErrUnknownSignal:
		if g.options.IgnoreUndefinedSignals {
			return
		}
		message = fmt.Sprintf("UnknownSignal: %v, state(%v) on signal(%v)", err,
			g.spec.stateName(Index(err.Index)), g.spec.signalName(Signal(err.Signal)))

	case ErrDuplicateState:
		message = fmt.Sprintf("Duplicate: %v", err)

	case ErrUnknownFSM:
		message = fmt.Sprintf("%s: %v", err.Error(), err)
	}

	defer g.log.Error("error", "tid", tid, "err", message, "context", ctx)
	select {
	case g.errors <- err: // non-blocking send
	default:
	}
}

func (g *runner) signal(signal Signal, instance *instance, optionalData ...interface{}) error {
	if _, has := g.spec.signals[signal]; !has {
		return ErrUnknownSignal{Signal: signal}
	}

	g.log.Debug("Signal", "signal", g.spec.signalName(signal), "instance", instance)
	g.events <- &event{instance: instance.id, ref: instance, signal: signal, data: optionalData}
	return nil
}

func (g *runner) alloc(initial Index) (FSM, error) {

	tid := g.tid()

	// add a new instance
	id := g.next
	g.next++

	new := &instance{
		id:     id,
		state:  initial,
		index:  -1,
		parent: g,
		flaps:  *newFlaps(),
		visits: map[Index]int{
			initial: 1,
		},
	}

	if err := g.processDeadline(tid, new, initial); err != nil {
		g.log.Error("error process deadline", "err", err)
		return nil, err
	}
	if new.index > -1 {
		g.log.Debug("runner deadline",
			"tid", tid, "id", id, "initial", g.spec.stateName(initial),
			"deadline", new.deadline, "queuePosition", new.index)
	}

	return new, nil
}

func (g *runner) tick() {
	g.now++
}

func (g *runner) ct() Time {
	return g.now
}

func (g *runner) handleClockTick(tid int64) error {

	g.tick()
	now := g.ct()

	g.log.Debug("Clock tick", "tid", tid, "now", now)
	for g.deadlines.Len() > 0 {

		instance := g.deadlines.peek()
		if instance == nil {
			return nil
		}

		if instance.deadline > now {
			return nil
		}

		instance = g.deadlines.dequeue()

		// check > 0 here because we could have already raised the signal
		// when a real event came in.
		if instance.deadline > 0 {

			// raise the signal
			if ttl, err := g.spec.expiry(instance.state); err != nil {

				return err

			} else if ttl != nil {

				g.log.Error("deadline exceeded", "tid", tid, "id", instance.id,
					"raise", g.spec.signalName(ttl.Raise), "now", now)

				g.raise(tid, instance, ttl.Raise, instance.state)
			}
		}
		// reset the state for future queueing
		instance.deadline = -1
		instance.index = -1

	}
	return nil
}

func (g *runner) processDeadline(tid int64, instance *instance, state Index) error {
	now := g.ct()
	ttl := Tick(0)
	// check for TTL
	if exp, err := g.spec.expiry(state); err != nil {
		return err
	} else if exp != nil {
		ttl = exp.TTL
	}

	instance.update(state, now, ttl)

	if instance.index > -1 {
		// case where this instance is in the deadlines queue (since it has a > -1 index)
		if instance.deadline > 0 {
			// in the queue and deadline is different now
			g.log.Debug("Deadline updating", "now", now, "tid", tid,
				"instance", instance.id, "deadline", instance.deadline,
				"deadline-queue-index", instance.index)
			g.deadlines.update(instance)
		} else {
			g.log.Debug("Deadline removing", "now", now, "tid", tid,
				"instance", instance.id, "deadline", instance.deadline,
				"deadline-queue-index", instance.index)
			g.deadlines.remove(instance)
		}
	} else if instance.deadline > 0 {
		// index == -1 means it's not in the queue yet and we have a deadline
		g.log.Debug("Deadline enqueuing", "now", now, "tid", tid,
			"instance", instance.id, "deadline", instance.deadline,
			"deadline-queue-index", instance.index)
		g.deadlines.enqueue(instance)
	}

	return nil
}

func (g *runner) processVisitLimit(tid int64, instance *instance, state Index) error {
	// have we visited next state too many times?
	if limit, err := g.spec.visit(state); err != nil {

		return err

	} else if limit != nil {

		if limit.Value > 0 && instance.visits[state] == limit.Value {

			g.log.Debug("Max visit limit hit", "tid", tid,
				"instance", instance.id, "state", g.spec.stateName(instance.state),
				"raise", g.spec.signalName(limit.Raise))

			g.raise(tid, instance, limit.Raise, instance.state)

			return nil
		}
	}
	return nil
}

// raises a signal by placing directly on the txn queue
func (g *runner) raise(tid int64, instance *instance, signal Signal, current Index) (err error) {
	defer func() {
		g.log.Debug("instance.signal", "instance", instance.ID(),
			"signal", g.spec.signalName(signal), "state", g.spec.stateName(current), "err", err)
	}()

	if _, has := g.spec.signals[signal]; !has {
		err = ErrUnknownSignal{Signal: signal}
		return
	}

	event := &event{instance: instance.id, ref: instance, signal: signal}

	g.transactions <- &txn{
		Func: func(tid int64) (interface{}, error) {
			return event, g.handleEvent(tid, instance, event)
		},
		tid: tid,
	}
	return nil
}

func (g *runner) handleEvent(tid int64, instance *instance, event *event) error {

	now := g.ct()

	// instance, has := g.members[event.instance]
	// if !has {
	// 	return ErrUnknownFSM(event.instance)
	// }

	current := instance.state
	next, action, err := g.spec.transition(current, event.signal)
	if err != nil {
		return err
	}

	g.log.Debug("Transition",
		"now", now,
		"tid", tid,
		"instance", instance.id,
		"state", g.spec.stateName(current),
		"signal", g.spec.signalName(event.signal),
		"next", g.spec.stateName(next),
		"deadline", instance.deadline, "deadlineQueueIndex", instance.index)

	// any flap detection?
	limit := g.spec.flap(current, next)
	if limit != nil && limit.Count > 0 {

		instance.flaps.record(current, next)
		flaps := instance.flaps.count(current, next)

		if flaps >= limit.Count {

			g.log.Debug("Flapping", "tid", tid, "flaps", flaps,
				"instance", instance.id, "state", instance.state, "raise", limit.Raise)
			g.raise(tid, instance, limit.Raise, instance.state)

			return nil // done -- another transition
		}
	}

	// Associate custom data - do this before calling on the action so action can do something with it.
	if event.data != nil {
		instance.data = event.data
	}

	// call action before transitiion
	if action != nil {

		g.log.Debug("Invoking action",
			"now", now,
			"tid", tid,
			"instance", instance.id,
			"state", g.spec.stateName(current),
			"signal", g.spec.signalName(event.signal),
			"next", g.spec.stateName(next),
			"deadline", instance.deadline, "deadlineQueueIndex", instance.index)

		if err := action(instance); err != nil {

			g.log.Debug("Error transition", "err", err)

			if alternate, err := g.spec.error(current, event.signal); err != nil {

				g.handleError(tid, err, []interface{}{current, event, instance})

			} else {

				g.log.Debug("Err executing action", "tid", tid, "instance", instance.id,
					"state", current, "signal", event.signal, "alternate", alternate, "next", next)

				next = alternate
			}
		}
	}

	// Action has been run... We landed in the new state (next)

	// process deadline, if any
	if err := g.processDeadline(tid, instance, next); err != nil {
		return err
	}

	// update the index
	// BYSTATE
	// delete(g.bystate[current], instance.id)
	// g.bystate[next][instance.id] = instance

	// visits limit trigger
	return g.processVisitLimit(tid, instance, next)
}

func (g *runner) tid() int64 {
	return time.Now().UnixNano()
}

type txn struct {
	Func func(int64) (interface{}, error)
	tid  int64
}

func (g *runner) run() {

	stopTransactions := make(chan struct{})

	// Core processing
	go func() {
		defer func() {
			g.log.Info("Shutting down")
			close(g.transactions)
		}()

		for {
			select {
			case <-stopTransactions:
				return

			case t := <-g.transactions:
				if t == nil {
					return
				}
				if ctx, err := t.Func(t.tid); err != nil {
					g.handleError(t.tid, err, ctx)
				}

			}
		}
	}()

	// Input events

	go func() {

	loop:
		for {

			var tx *txn
			tid := g.tid()

			select {

			case <-g.clock.C:
				tx = &txn{
					tid: g.tid(),
					Func: func(tid int64) (interface{}, error) {
						return nil, g.handleClockTick(tid)
					},
				}

			case <-g.stop:
				break loop

			case event, ok := <-g.events:
				// state transition events
				if !ok {
					break loop
				}

				copy := event
				tx = &txn{
					tid: tid,
					Func: func(tid int64) (interface{}, error) {
						return copy, g.handleEvent(tid, event.ref, copy)
					},
				}

			case reader := <-g.reads:
				tx = &txn{
					tid: tid,
					Func: func(tid int64) (interface{}, error) {
						// For reads on the runner itself.  All the reads are serialized.
						reader(g)
						return nil, nil
					},
				}
			}

			// send to transaction processing pipeline
			g.transactions <- tx

		}

	}()
}
