package lidzatest

import (
	"sync"
	"time"
)

// Clock is the controllable clock lidzatest.Start provides: it follows
// real time until Set freezes it; Advance moves it forward either way.
// Handlers read it through lidza.Now(ctx).
type Clock struct {
	mu     sync.Mutex
	frozen *time.Time
	offset time.Duration
}

// Now implements lidza.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen != nil {
		return *c.frozen
	}
	return time.Now().Add(c.offset)
}

// Set freezes the clock at t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frozen = &t
}

// Advance moves the clock forward by d, frozen or not.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen != nil {
		t := c.frozen.Add(d)
		c.frozen = &t
		return
	}
	c.offset += d
}

// Resume unfreezes the clock, keeping the offset it had gained.
func (c *Clock) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen != nil {
		c.offset = time.Until(*c.frozen)
		c.frozen = nil
	}
}
