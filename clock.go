package lidza

import (
	"context"
	"net/http"
	"time"
)

// Clock is where the app reads the time, so tests can set it. The real
// clock is the default; lidzatest provides a controllable one.
type Clock interface {
	Now() time.Time
}

// RealClock is time.Now.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// Now returns the current time from the clock in ctx (the services'
// Clock, or the real one). Handlers use it instead of time.Now so a test
// can freeze or advance time.
func Now(ctx context.Context) time.Time {
	if s, _ := ctx.Value(servicesKey{}).(*Services); s != nil {
		if c, ok := s.Lookup(typeOf[Clock]()); ok {
			return c.(Clock).Now()
		}
	}
	return time.Now()
}

// HTTPClient returns the client for outbound HTTP calls: the services'
// http.RoundTripper (a recorder or replayer under test) behind a
// 30-second timeout, or a plain client. Handlers use it for every
// external call so tests run offline against recorded fixtures.
func HTTPClient(ctx context.Context) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}
	if s, _ := ctx.Value(servicesKey{}).(*Services); s != nil {
		if rt, ok := s.Lookup(typeOf[http.RoundTripper]()); ok {
			client.Transport = rt.(http.RoundTripper)
		}
	}
	return client
}
