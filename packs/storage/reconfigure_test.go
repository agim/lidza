package storage

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
)

// Reconfigure while objects are written and read: race-free under go
// test -race.
func TestReconfigureConcurrent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STORAGE_PROVIDER", "local")
	t.Setenv("STORAGE_DIR", dir)
	s, err := New(Config{Provider: "local", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			if err := s.Reconfigure(ctx); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if _, err := s.Put(ctx, "a/b.txt", bytes.NewReader([]byte("x")), PutOptions{}); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.Stat(ctx, "a/b.txt"); err != nil {
					t.Error(err)
					return
				}
				_ = s.Provider()
				_ = s.URL("a/b.txt")
			}
		}()
	}
	wg.Wait()
}
