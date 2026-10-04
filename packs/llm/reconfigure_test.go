package llm

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
)

// Reconfigure while chats run and the configuration is read: race-free
// under go test -race.
func TestReconfigureConcurrent(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "fake")
	l, err := New(Config{Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	l.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			if err := l.Reconfigure(ctx); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if _, err := l.Chat(ctx, Request{Messages: []Message{{Role: User, Content: "hi"}}}); err != nil {
					t.Error(err)
					return
				}
				_, _ = l.Provider(), l.Model()
				l.Embeddings(ctx, EmbedRequest{Texts: []string{"a"}})
			}
		}()
	}
	wg.Wait()
}
