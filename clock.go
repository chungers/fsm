package fsm // import "github.com/orkestr8/fsm"

import (
	"sync"
	"time"
)

// Clock adapts a timer tick
type Clock struct {
	C      <-chan Tick
	c      chan<- Tick
	stop   chan struct{}
	start  chan struct{}
	driver func()
	lock   sync.Mutex
}

// NewClock returns a clock
func NewClock() *Clock {
	c := make(chan Tick)
	stop := make(chan struct{})
	clock := &Clock{
		C:     c,
		c:     c,
		stop:  stop,
		start: make(chan struct{}),
	}
	clock.driver = func() {
		<-clock.start
		clock.synchronized(func(c *Clock) { c.start = nil })

		<-clock.stop
		clock.synchronized(func(c *Clock) { close(clock.c); c.start = nil })

	}
	return clock.run()
}

func (t *Clock) synchronized(f func(*Clock)) {
	t.lock.Lock()
	defer t.lock.Unlock()
	f(t)
}

// Start starts the clock
func (t *Clock) Start() {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.start == nil {
		return
	}

	// Start should be idempotent.
	// Here we read from the start channel without blocking.
	// Assumption here is that this reader quickly goes away and won't interfere
	// with the main goroutine. We do this here so that we don't end up
	// with panic when Start is called multiple times by mistake.
	select {
	case _, open := <-t.start:
		if !open {
			return
		}
	default:
	}
	close(t.start)
}

// Stop stops the ticks
func (t *Clock) Stop() {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.stop == nil {
		return
	}
	select {
	case _, open := <-t.stop:
		if !open {
			return
		}
	default:
	}
	close(t.stop)
}

// Tick makes one tick of the clock
func (t *Clock) Tick() {
	t.c <- Tick(1)
}

// Ticks makes multiple ticks
func (t *Clock) Ticks(ticks int) {
	for i := 0; i < ticks; i++ {
		t.Tick()
	}
}

func (t *Clock) run() *Clock {
	if t.driver != nil {
		go t.driver()
	}
	return t
}

// Wall adapts a regular time.Tick to return a clock
func Wall(tick <-chan time.Time) *Clock {
	out := make(chan Tick)
	stop := make(chan struct{})
	clock := &Clock{
		C:     out,
		c:     out,
		stop:  stop,
		start: make(chan struct{}),
	}

	clock.driver = func() {
		<-clock.start

		clock.lock.Lock()
		clock.start = nil
		clock.lock.Unlock()

		for {
			select {
			case <-clock.stop:
				close(clock.c)
				return
			case <-tick:
				// note that golang's time ticker won't close the channel when stopped.
				// so we will do the closing ourselves to avoid leaking the goroutine
				clock.c <- Tick(1)
			}
		}
	}

	return clock.run()
}
