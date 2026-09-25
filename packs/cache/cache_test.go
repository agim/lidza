package cache

import (
	"context"
	"errors"
	"os"
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
