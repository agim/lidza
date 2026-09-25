package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/agim/lidza/packs/db"
)

func TestUsage(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	pool.Exec(ctx, `DROP TABLE IF EXISTS llm_usage`)
	if _, err := pool.Exec(ctx, UsageTable); err != nil {
		t.Fatal(err)
	}
	l, f := fakeLLM(t)
	if _, err := l.Usage(ctx, time.Now()); err == nil {
		t.Fatal("usage without the db pack should say so")
	}
	l.TrackUsage(pool)
	f.Reply("four words in reply")
	if _, err := l.Chat(ctx, Request{Label: "note.tags", Messages: []Message{{Role: User, Content: "one two three"}}}); err != nil {
		t.Fatal(err)
	}
	f.Reply("ok")
	if _, err := Generate[tags](ctx, l, Request{Label: "note.tags", Messages: []Message{{Role: User, Content: "x"}}}); err == nil {
		t.Fatal("expected a decode failure to record an error")
	}
	if _, err := l.Chat(ctx, Request{Messages: []Message{{Role: User, Content: "unlabelled"}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := l.Usage(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	byLabel := map[string]UsageRow{}
	for _, r := range rows {
		byLabel[r.Label] = r
	}
	// A reply the caller could not decode still cost tokens: it is a call,
	// not an error; only a failed provider call is.
	if r := byLabel["note.tags"]; r.Calls != 2 || r.Errors != 0 || r.Input == 0 || r.Output == 0 || r.Model != "test-model" || r.Provider != "fake" {
		t.Fatalf("note.tags: %+v", r)
	}
	if r := byLabel[""]; r.Calls != 1 {
		t.Fatalf("unlabelled: %+v", r)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer broken.Close()
	l2, _ := New(Config{Provider: "ollama", BaseURL: broken.URL, Model: "missing", MaxAttempts: 1})
	l2.TrackUsage(pool)
	if _, err := l2.Chat(ctx, Request{Label: "broken", Messages: []Message{{Role: User, Content: "x"}}}); err == nil {
		t.Fatal("expected the provider error")
	}
	rows, _ = l.Usage(ctx, time.Now().Add(-time.Hour))
	for _, r := range rows {
		byLabel[r.Label] = r
	}
	if r := byLabel["broken"]; r.Calls != 1 || r.Errors != 1 || r.Provider != "ollama" || r.Model != "missing" {
		t.Fatalf("broken: %+v", r)
	}
	calls, err := l.RecentCalls(ctx, 2)
	if err != nil || len(calls) != 2 || calls[0].Label != "broken" || calls[0].Status != "error" || calls[0].Error == nil || calls[1].Label != "" {
		t.Fatalf("recent: %+v %v", calls, err)
	}
}
