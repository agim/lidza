package cache

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T, name string) Store {
	t.Helper()
	if name == "memory" {
		return NewMemory(3)
	}
	url := os.Getenv("LIDZA_TEST_CACHE_URL")
	if url == "" {
		url = "redis://127.0.0.1:6379"
	}
	st, err := NewValkey(context.Background(), url)
	if err != nil {
		t.Skipf("no cache server: %v", err)
	}
	return st
}

func TestBackends(t *testing.T) {
	for _, name := range []string{"memory", "valkey"} {
		t.Run(name, func(t *testing.T) {
			st := testStore(t, name)
			defer st.Close()
			c := New(st, "lidzatest:"+name+":")
			ctx := context.Background()
			c.Invalidate(ctx, "")

			var s string
			if err := c.Get(ctx, "a", &s); !errors.Is(err, ErrMiss) {
				t.Fatalf("miss: %v", err)
			}
			if err := c.Set(ctx, "a", "one", time.Minute); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, "a", &s); err != nil || s != "one" {
				t.Fatalf("get: %q %v", s, err)
			}
			c.Set(ctx, "short", "x", 50*time.Millisecond)
			time.Sleep(80 * time.Millisecond)
			if err := c.Get(ctx, "short", &s); !errors.Is(err, ErrMiss) {
				t.Fatalf("ttl: %v", err)
			}
			calls := 0
			load := func(context.Context) (int, error) { calls++; return 42, nil }
			for i := 0; i < 3; i++ {
				if v, err := Remember(ctx, c, "n", time.Minute, load); err != nil || v != 42 {
					t.Fatalf("remember: %d %v", v, err)
				}
			}
			if calls != 1 {
				t.Fatalf("load called %d times", calls)
			}
			if _, err := Remember(ctx, c, "err", time.Minute, func(context.Context) (int, error) { return 0, errors.New("nope") }); err == nil {
				t.Fatal("load error swallowed")
			}
			c.Set(ctx, "user:1", 1, 0)
			c.Set(ctx, "user:2", 2, 0)
			if err := c.Invalidate(ctx, "user:"); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := c.Get(ctx, "user:1", &n); !errors.Is(err, ErrMiss) {
				t.Fatalf("invalidate: %v", err)
			}
			c.Delete(ctx, "a")
			if err := c.Get(ctx, "a", &s); !errors.Is(err, ErrMiss) {
				t.Fatal("delete")
			}
		})
	}
}

func TestMemoryBound(t *testing.T) {
	m := NewMemory(2)
	ctx := context.Background()
	m.Set(ctx, "a", []byte("1"), 0)
	m.Set(ctx, "b", []byte("2"), 0)
	m.Set(ctx, "c", []byte("3"), 0)
	if m.Len() != 2 {
		t.Fatalf("len %d", m.Len())
	}
	if _, err := m.Get(ctx, "a"); !errors.Is(err, ErrMiss) {
		t.Fatal("oldest not evicted")
	}
}

func TestIncr(t *testing.T) {
	for _, name := range []string{"memory", "valkey"} {
		t.Run(name, func(t *testing.T) {
			st := testStore(t, name)
			defer st.Close()
			if name == "memory" {
				st = NewMemory(100)
			}
			c := New(st, "lidzatest:incr:"+name+":")
			ctx := context.Background()
			c.Invalidate(ctx, "")

			// Concurrent increments lose nothing.
			var wg sync.WaitGroup
			for i := 0; i < 40; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := c.Incr(ctx, "hits", 1, time.Minute); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if n, err := c.Incr(ctx, "hits", 2, time.Minute); err != nil || n != 42 {
				t.Fatalf("after 40 concurrent: %d %v", n, err)
			}
			if n, err := c.Incr(ctx, "hits", -2, 0); err != nil || n != 40 {
				t.Fatalf("decrement: %d %v", n, err)
			}
			var got int64
			if err := c.Get(ctx, "hits", &got); err != nil || got != 40 {
				t.Fatalf("get: %d %v", got, err)
			}

			// The first increment sets the ttl; later ones keep it.
			if n, _ := c.Incr(ctx, "window", 1, 100*time.Millisecond); n != 1 {
				t.Fatalf("first: %d", n)
			}
			time.Sleep(60 * time.Millisecond)
			if n, _ := c.Incr(ctx, "window", 1, 100*time.Millisecond); n != 2 {
				t.Fatalf("second: %d", n)
			}
			time.Sleep(60 * time.Millisecond)
			if n, err := c.Incr(ctx, "window", 1, 100*time.Millisecond); err != nil || n != 1 {
				t.Fatalf("after the window: %d %v (the second increment extended it?)", n, err)
			}

			// No ttl: the counter stays.
			c.Incr(ctx, "total", 5, 0)
			time.Sleep(20 * time.Millisecond)
			if n, _ := c.Incr(ctx, "total", 0, 0); n != 5 {
				t.Fatalf("total: %d", n)
			}

			c.Set(ctx, "word", "hello", time.Minute)
			if _, err := c.Incr(ctx, "word", 1, 0); err == nil {
				t.Fatal("incremented a string")
			}
		})
	}
}

type plainStore struct{ Store }

func TestIncrWithoutCounter(t *testing.T) {
	c := New(plainStore{NewMemory(2)}, "")
	if _, err := c.Incr(context.Background(), "k", 1, 0); !errors.Is(err, ErrNotCounter) {
		t.Fatalf("got %v", err)
	}
}
