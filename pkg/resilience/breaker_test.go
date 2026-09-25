package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreaker(t *testing.T) {
	now := time.Unix(0, 0)
	b := New(Options{Failures: 2, Reset: time.Second})
	b.now = func() time.Time { return now }
	ctx := context.Background()
	boom := errors.New("boom")
	fail := func(context.Context) error { return boom }
	ok := func(context.Context) error { return nil }

	if err := b.Do(ctx, fail); err != boom || b.State() != Closed {
		t.Fatalf("first failure: %v %v", err, b.State())
	}
	if err := b.Do(ctx, fail); err != boom || b.State() != Open {
		t.Fatalf("second failure should open: %v %v", err, b.State())
	}
	if err := b.Do(ctx, ok); err != ErrOpen {
		t.Fatalf("open should refuse: %v", err)
	}
	now = now.Add(time.Second)
	if b.State() != HalfOpen {
		t.Fatalf("after reset: %v", b.State())
	}
	// One trial call; a second concurrent one is refused.
	if err := b.Do(ctx, func(context.Context) error {
		if err := b.Do(ctx, ok); err != ErrOpen {
			t.Errorf("second trial not refused: %v", err)
		}
		return boom
	}); err != boom || b.State() != Open {
		t.Fatalf("failed trial should reopen: %v %v", err, b.State())
	}
	now = now.Add(time.Second)
	if err := b.Do(ctx, ok); err != nil || b.State() != Closed {
		t.Fatalf("successful trial should close: %v %v", err, b.State())
	}
	// Cancellation is not a failure.
	if err := b.Do(ctx, func(context.Context) error { return context.Canceled }); err != context.Canceled || b.State() != Closed {
		t.Fatal("cancel counted as failure")
	}
}
