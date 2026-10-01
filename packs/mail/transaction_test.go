package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/packs/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Embedding the interface makes any accidental database call fail.
type unusedTx struct{ pgx.Tx }

func TestSendTxRequiresDependencies(t *testing.T) {
	for _, tc := range []struct {
		name string
		mail *Mail
		tx   pgx.Tx
	}{
		{"transaction", &Mail{}, nil},
		{"outbox", &Mail{queue: jobs.New(jobs.Config{}, nil)}, &unusedTx{}},
		{"queue", &Mail{pool: &pgxpool.Pool{}}, &unusedTx{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if id, err := tc.mail.SendTx(context.Background(), tc.tx, Message{}); id != "" || err == nil || !strings.Contains(err.Error(), "requires") {
				t.Fatalf("missing dependency: id=%q err=%v", id, err)
			}
		})
	}
}

func TestSendTx(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dsn := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	admin, err := db.Open(ctx, db.Config{URL: dsn, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		if os.Getenv("LIDZA_TEST_DATABASE_URL") != "" {
			t.Fatal(err)
		}
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(admin.Close)
	// Other packs test their public tables in the same database.
	namespace := pgx.Identifier{fmt.Sprintf("mail_tx_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+namespace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+namespace+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["search_path"] = namespace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, ddl := range []string{OutboxTable, jobs.JobTable, "CREATE TABLE review (id text PRIMARY KEY)"} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notice.de.txt.tmpl"), []byte(`{{define "subject"}}Nachricht {{.Title}}{{end}}Hallo {{.Name}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notice.de.html.tmpl"), []byte(`<p>Hallo {{.Name}}</p>`), 0o600); err != nil {
		t.Fatal(err)
	}
	mailCfg := Config{Provider: "outbox", From: "default@example.com", TemplatesDir: dir, MaxAttempts: 3}
	m, err := New(mailCfg, pool, jobs.New(jobs.Config{}, pool))
	if err != nil {
		t.Fatal(err)
	}
	message := Message{To: "ada@example.com", From: "sender@example.com", ReplyTo: "reply@example.com",
		Cc: []string{"cc@example.com"}, Bcc: []string{"blind@example.com"}, Attachments: []Attachment{{Name: "report.txt", Data: []byte("report")}},
		Template: "notice", Lang: "de-CH", Data: map[string]string{"Title": "A", "Name": "Ada"}, Headers: map[string]string{"X-Tag": "notice"}}
	begin := func(t *testing.T) pgx.Tx {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
		return tx
	}
	count := func(t *testing.T, want int) {
		t.Helper()
		for _, table := range []string{"review", "mail_message", "job"} {
			var got int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
				t.Fatalf("%s: count=%d want=%d err=%v", table, got, want, err)
			}
		}
	}
	reset := func(t *testing.T) {
		t.Helper()
		if _, err := pool.Exec(ctx, "TRUNCATE review, mail_message, job"); err != nil {
			t.Fatal(err)
		}
	}
	write := func(t *testing.T, tx pgx.Tx) {
		t.Helper()
		if _, err := tx.Exec(ctx, "INSERT INTO review VALUES ('approved')"); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("commit and delivery", func(t *testing.T) {
		reset(t)
		delivered := make(chan map[string]any, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			delivered <- body
			io.WriteString(w, `{"id":"sent-1"}`)
		}))
		defer server.Close()
		deliveryCfg := mailCfg
		deliveryCfg.Provider, deliveryCfg.APIKey, deliveryCfg.BaseURL = "resend", "test", server.URL
		m, err := New(deliveryCfg, pool, jobs.New(jobs.Config{}, pool))
		if err != nil {
			t.Fatal(err)
		}
		rctx, stop := context.WithTimeout(ctxWith(t, server), 10*time.Second)
		defer stop()
		tx := begin(t)
		write(t, tx)
		id, err := m.SendTx(rctx, tx, message)
		if err != nil || id == "" {
			t.Fatal(id, err)
		}
		count(t, 0) // Neither business state nor job is visible before commit.
		select {
		case <-delivered:
			t.Fatal("provider called before commit")
		default:
		}
		var payloadID, kind, subject, text, html, template, from, replyTo, status, tag string
		var attempts int
		err = tx.QueryRow(ctx, `SELECT j.payload->>'id', j.kind, j.max_attempts, m.subject, m.text, m.html, m.template, m.from_address, m.reply_to, m.status, m.headers->>'X-Tag'
			FROM job j JOIN mail_message m ON m.id::text = j.payload->>'id'`).Scan(&payloadID, &kind, &attempts, &subject, &text, &html, &template, &from, &replyTo, &status, &tag)
		if err != nil || payloadID != id || kind != JobKind || attempts != 3 || subject != "Nachricht A" || text != "Hallo Ada" || html != "<p>Hallo Ada</p>" || template != "notice.de" || from != message.From || replyTo != message.ReplyTo || status != StatusQueued || tag != "notice" {
			t.Fatalf("stored metadata: %s %s %d %s %s %s %s %s %s %s %s: %v", payloadID, kind, attempts, subject, text, html, template, from, replyTo, status, tag, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		count(t, 1)
		if err := m.Deliver(rctx, id); err != nil {
			t.Fatal(err)
		}
		body := <-delivered
		if body["from"] != message.From || body["reply_to"] != message.ReplyTo || body["subject"] != "Nachricht A" || body["text"] != "Hallo Ada" || body["html"] != "<p>Hallo Ada</p>" {
			t.Fatalf("delivered metadata: %+v", body)
		}
		if body["cc"].([]any)[0] != message.Cc[0] || body["bcc"].([]any)[0] != message.Bcc[0] {
			t.Fatalf("queued recipients lost: %+v", body)
		}
		files := body["attachments"].([]any)
		file := files[0].(map[string]any)
		data, err := base64.StdEncoding.DecodeString(file["content"].(string))
		if err != nil || file["filename"] != "report.txt" || !bytes.Equal(data, message.Attachments[0].Data) {
			t.Fatal("queued attachment lost", body, err)
		}
		// Failed attempts retain the original payload for a later retry.
		if _, err := pool.Exec(ctx, `UPDATE mail_message SET status='failed', error='temporary failure' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if err := m.Deliver(rctx, id); err != nil {
			t.Fatal(err)
		}
		if retry := <-delivered; retry["attachments"].([]any)[0].(map[string]any)["content"] != file["content"] {
			t.Fatal("retry changed attachment")
		}
	})
	t.Run("legacy null columns and invalid retry", func(t *testing.T) {
		reset(t)
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO mail_message(recipient,subject,text,status,attempts) VALUES('ada@example.com','Hi','hello','queued',0) RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := m.Deliver(ctx, id); err != nil {
			t.Fatal("legacy message", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE mail_message SET headers='{"Bcc":"hidden@example.com"}' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if err := m.Deliver(ctx, id); err == nil {
			t.Fatal("unsafe legacy header accepted")
		}
		var status, failure string
		var attempts int
		if err := pool.QueryRow(ctx, `SELECT status,error,attempts FROM mail_message WHERE id=$1`, id).Scan(&status, &failure, &attempts); err != nil || status != StatusFailed || failure == "" || attempts != 2 {
			t.Fatal(status, failure, attempts, err)
		}
	})
	t.Run("rollback", func(t *testing.T) {
		reset(t)
		tx := begin(t)
		write(t, tx)
		if _, err := m.SendTx(ctx, tx, message); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		count(t, 0)
	})
	t.Run("validation and closed transaction", func(t *testing.T) {
		reset(t)
		for _, invalid := range []Message{
			{To: "invalid", Subject: "A", Text: "body"},
			{To: "ada@example.com", Text: "body"},
			{To: "ada@example.com", Subject: "A"},
			{To: "ada@example.com", Template: "missing"},
		} {
			tx := begin(t)
			if id, err := m.SendTx(ctx, tx, invalid); err == nil || id != "" {
				t.Fatalf("invalid message: id=%q err=%v", id, err)
			}
			// Validation must have made no writes, even if a caller commits.
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			count(t, 0)
		}
		tx := begin(t)
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if id, err := m.SendTx(ctx, tx, message); id != "" || !errors.Is(err, pgx.ErrTxClosed) {
			t.Fatalf("closed transaction: id=%q err=%v", id, err)
		}
	})
	t.Run("enqueue failure never leaves an orphan", func(t *testing.T) {
		reset(t)
		if _, err := pool.Exec(ctx, `ALTER TABLE job ADD CONSTRAINT reject_mail CHECK (kind <> 'mail.send')`); err != nil {
			t.Fatal(err)
		}
		tx := begin(t)
		write(t, tx)
		if id, err := m.SendTx(ctx, tx, message); id != "" || err == nil || !strings.Contains(err.Error(), "delivery job") {
			t.Fatalf("enqueue failure: id=%q err=%v", id, err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		count(t, 0)
		if id, err := m.Send(ctx, message); id != "" || err == nil {
			t.Fatalf("Send enqueue failure: id=%q err=%v", id, err)
		}
		count(t, 0)
		if _, err := pool.Exec(ctx, "ALTER TABLE job DROP CONSTRAINT reject_mail"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ordinary Send commits outbox and job", func(t *testing.T) {
		reset(t)
		id, err := m.Send(ctx, Message{To: "ada@example.com", Subject: "A", Text: "body"})
		if err != nil || id == "" {
			t.Fatal(id, err)
		}
		var jobID, sender string
		if err := pool.QueryRow(ctx, `SELECT j.payload->>'id', m.from_address FROM job j JOIN mail_message m ON m.id::text = j.payload->>'id'`).Scan(&jobID, &sender); err != nil || jobID != id || sender != "default@example.com" {
			t.Fatalf("Send: id=%q sender=%q err=%v", jobID, sender, err)
		}
	})
	reset(t)
}
