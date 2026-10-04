package mail

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agim/lidza/packs/jobs"
)

// Reconfigure while messages are sent and the configuration is read:
// race-free (go test -race), and every send sees one whole snapshot.
func TestReconfigureConcurrent(t *testing.T) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("MAIL_PROVIDER", "outbox")
	t.Setenv("MAIL_FROM", "alerts@example.com")
	t.Setenv("APP_URL", "https://a.example.com")
	m, err := New(Config{Provider: "outbox", From: "alerts@example.com"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if err := m.Reconfigure(context.Background()); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if _, err := m.Send(context.Background(), Message{To: "operator@example.com", Subject: "fixture", Text: "fixture"}); err != nil {
					t.Error(err)
					return
				}
				_ = m.Provider()
				_ = m.Link("/x")
				_ = m.Templates()
				m.TemplateFor("welcome", "de")
			}
		}()
	}
	wg.Wait()
}

// A reload that fails (an unknown provider, a broken template) keeps the
// previous provider, sender and templates.
func TestReconfigureFailureKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "welcome.txt.tmpl"), []byte(`Hello {{.Name}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_TEMPLATES", dir)
	t.Setenv("MAIL_FROM", "first@example.com")
	t.Setenv("MAIL_PROVIDER", "outbox")
	m, err := New(Config{Provider: "outbox", From: "first@example.com", TemplatesDir: dir}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_PROVIDER", "carrier-pigeon")
	t.Setenv("MAIL_FROM", "second@example.com")
	if err := m.Reconfigure(context.Background()); err == nil {
		t.Fatal("unknown provider accepted")
	}
	t.Setenv("MAIL_PROVIDER", "outbox")
	if err := os.WriteFile(filepath.Join(dir, "broken.txt.tmpl"), []byte(`{{.Name`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconfigure(context.Background()); err == nil {
		t.Fatal("broken template accepted")
	}
	if p := m.Provider(); p != "outbox" {
		t.Fatalf("provider %q after failed reloads", p)
	}
	msg := Message{To: "a@example.com", Template: "welcome", Data: map[string]string{"Name": "Ada"}, Subject: "Hi"}
	if err := m.prepare(context.Background(), &msg); err != nil || msg.From != "first@example.com" || msg.Text != "Hello Ada" {
		t.Fatalf("previous snapshot lost: %+v %v", msg, err)
	}
}

// Queued messages delivered while the provider changes are each sent
// once and none is lost.
func TestReconfigureDuringQueuedDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := schemaPool(ctx, t, "mail_reconf")
	for _, ddl := range []string{OutboxTable, jobs.JobTable} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("MAIL_PROVIDER", "outbox")
	t.Setenv("MAIL_FROM", "alerts@example.com")
	m, err := New(Config{Provider: "outbox", From: "alerts@example.com"}, pool, jobs.New(jobs.Config{}, pool))
	if err != nil {
		t.Fatal(err)
	}
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	const n = 40
	ids := make([]string, n)
	for i := range ids {
		if ids[i], err = m.Send(ctx, Message{To: "operator@example.com", Subject: fmt.Sprint("m", i), Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 50 {
			os.Setenv("MAIL_PROVIDER", []string{"outbox", "log"}[i%2])
			if err := m.Reconfigure(ctx); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; i < n; i += 4 {
				if err := m.deliverQueued(ctx, ids[i]); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	var sent, attempts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'sent'), coalesce(sum(attempts), 0) FROM mail_message`).Scan(&sent, &attempts); err != nil {
		t.Fatal(err)
	}
	if sent != n || attempts != n {
		t.Fatalf("sent %d of %d with %d attempts", sent, n, attempts)
	}
}
