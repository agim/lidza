// Package resilience holds the circuit breaker outbound calls run through,
// so a failing dependency sheds load fast instead of tying up every
// request until its timeout.
package resilience

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrOpen is returned while the breaker refuses calls.
var ErrOpen = errors.New("circuit open")

// Options configures a Breaker.
type Options struct {
	// Failures opens the breaker after this many consecutive failures;
	// default 5.
	Failures int
	// Reset is how long the breaker stays open before letting one trial
	// call through; default 10s.
	Reset time.Duration
	// IsFailure decides which errors count; default: any non-nil error
	// except context cancellation.
	IsFailure func(error) bool
}

// State of a breaker.
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string { return [...]string{"closed", "open", "half-open"}[s] }

// Breaker guards one dependency.
type Breaker struct {
	opt      Options
	mu       sync.Mutex
	state    State
	failures int
	openedAt time.Time
	trial    bool
	now      func() time.Time
}

// New returns a closed breaker.
func New(opt Options) *Breaker {
	if opt.Failures <= 0 {
		opt.Failures = 5
	}
	if opt.Reset <= 0 {
		opt.Reset = 10 * time.Second
	}
	if opt.IsFailure == nil {
		opt.IsFailure = func(err error) bool { return err != nil && !errors.Is(err, context.Canceled) }
	}
	return &Breaker{opt: opt, now: time.Now}
}

// State reports the current state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	return b.state
}

// advance moves Open to HalfOpen once the reset period passed. Caller
// holds the lock.
func (b *Breaker) advance() {
	if b.state == Open && b.now().Sub(b.openedAt) >= b.opt.Reset {
		b.state = HalfOpen
		b.trial = false
	}
}

// Do runs fn unless the breaker is open. In half-open state one call is
// let through; its result closes or reopens the breaker.
func (b *Breaker) Do(ctx context.Context, fn func(context.Context) error) error {
	b.mu.Lock()
	b.advance()
	switch b.state {
	case Open:
		b.mu.Unlock()
		return ErrOpen
	case HalfOpen:
		if b.trial {
			b.mu.Unlock()
			return ErrOpen
		}
		b.trial = true
	}
	b.mu.Unlock()

	err := fn(ctx)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.opt.IsFailure(err) {
		b.failures++
		if b.state == HalfOpen || b.failures >= b.opt.Failures {
			b.state = Open
			b.openedAt = b.now()
		}
		return err
	}
	b.failures = 0
	b.state = Closed
	return err
}
