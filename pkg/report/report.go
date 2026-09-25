// Package report is the seam between the places errors surface (panic
// recovery, typed handlers, the frontend) and whatever collects them (the
// analytics pack). Without a reporter, Capture does nothing.
package report

import (
	"context"
	"time"
)

// Error is one captured failure.
type Error struct {
	// Source is "server" or "client".
	Source    string         `json:"source"`
	Message   string         `json:"message"`
	Stack     string         `json:"stack,omitempty"`
	Route     string         `json:"route,omitempty"`
	Method    string         `json:"method,omitempty"`
	URL       string         `json:"url,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
	UserID    string         `json:"userId,omitempty"`
	UserAgent string         `json:"userAgent,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
	At        time.Time      `json:"at"`
}

// Reporter receives captured errors. Implementations must not block the
// request: queue and return.
type Reporter interface {
	Report(ctx context.Context, e Error)
}

type lookupKey struct{}

// WithLookup attaches a function that resolves the reporter at capture
// time, so a reporter registered after the handler was built is found.
func WithLookup(ctx context.Context, lookup func() Reporter) context.Context {
	return context.WithValue(ctx, lookupKey{}, lookup)
}

// From returns the reporter for ctx, or nil.
func From(ctx context.Context) Reporter {
	if f, ok := ctx.Value(lookupKey{}).(func() Reporter); ok && f != nil {
		return f()
	}
	return nil
}

// Capture sends e to the reporter, if any.
func Capture(ctx context.Context, e Error) {
	r := From(ctx)
	if r == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	r.Report(ctx, e)
}
