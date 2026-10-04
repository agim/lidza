package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
)

// testPool connects with a schema of its own on the search path.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	admin, err := db.Open(ctx, db.Config{URL: dsn, MaxConns: 1, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(admin.Close)
	schema := pgx.Identifier{fmt.Sprintf("audit_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = strings.Trim(schema, `"`)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, Table); err != nil {
		t.Fatal(err)
	}
	return pool
}

// as is a context with a signed-in user, as auth.Require leaves it.
func as(subject string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: subject})
}

func TestRecordAndList(t *testing.T) {
	pool := testPool(t)
	a := New(Config{}, pool)
	ctx := as("u1")

	// No actor, no record: identity never comes from the caller.
	if err := a.Record(context.Background(), Event{Action: "deploy.run"}); !errors.Is(err, ErrNoActor) {
		t.Fatalf("no actor: %v", err)
	}
	for _, bad := range []Event{{Action: ""}, {Action: "Deploy Run"}, {Action: "x", Outcome: "maybe"}, {Action: "x", Meta: map[string]string{"bad key": "v"}}} {
		if err := a.Record(ctx, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	tooMany := map[string]string{}
	for i := range MaxMeta + 1 {
		tooMany[fmt.Sprint("k", i)] = "v"
	}
	if err := a.Record(ctx, Event{Action: "x", Meta: tooMany}); err == nil {
		t.Error("accepted too much metadata")
	}

	// Secrets are never stored: by key, and by shape whatever the key.
	stripe := "sk_" + "live_" + strings.Repeat("A", 24)
	err := a.Record(ctx, Event{Action: "settings.save", Resource: "settings", Meta: map[string]string{
		"apiToken": "plain-looking", "password": "hunter2", "note": "rotated " + stripe,
		"database": "postgres://app:s3cretpw@db.example.com/app", "version": "v1.2.3", "long": strings.Repeat("x", 600),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	pool.QueryRow(context.Background(), `SELECT meta::text FROM audit_event WHERE action = 'settings.save'`).Scan(&raw)
	for _, leaked := range []string{"plain-looking", "hunter2", stripe, "s3cretpw"} {
		if strings.Contains(raw, leaked) {
			t.Errorf("stored %q: %s", leaked, raw)
		}
	}
	if !strings.Contains(raw, `"version": "v1.2.3"`) || strings.Contains(raw, strings.Repeat("x", 501)) {
		t.Errorf("meta: %s", raw)
	}

	// A job's records name a system actor; a user in the context wins.
	if err := a.Record(System(context.Background(), "nightly"), Event{Action: "export.run", Outcome: Failed}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(System(as("u2"), "nightly"), Event{Action: "deploy.run", Scope: "team-a", Outcome: Denied}); err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		if err := a.Record(ctx, Event{Action: "deploy.run", Resource: fmt.Sprint("app/", i), Scope: "team-a"}); err != nil {
			t.Fatal(err)
		}
	}

	// Pages of 3, newest first, no record twice or missed.
	var seen []string
	q := Query{Scope: "team-a", Limit: 3}
	for pages := 0; ; pages++ {
		p, err := a.List(context.Background(), q)
		if err != nil || pages > 5 {
			t.Fatal(err, pages)
		}
		for _, r := range p.Records {
			seen = append(seen, r.Actor+":"+r.Resource+":"+r.Outcome)
		}
		if p.Next == "" {
			break
		}
		q.Cursor = p.Next
	}
	want := "u1:app/6:ok,u1:app/5:ok,u1:app/4:ok,u1:app/3:ok,u1:app/2:ok,u1:app/1:ok,u1:app/0:ok,u2::denied"
	if got := strings.Join(seen, ","); got != want {
		t.Fatalf("pages:\n%s\nwant\n%s", got, want)
	}
	if p, _ := a.List(context.Background(), Query{Actor: "system:nightly"}); len(p.Records) != 1 || p.Records[0].Outcome != Failed {
		t.Fatalf("system actor: %+v", p.Records)
	}
	if _, err := a.List(context.Background(), Query{Cursor: "forged"}); err == nil {
		t.Fatal("forged cursor accepted")
	}

	// RecordTx commits and rolls back with the caller's change.
	tx, _ := pool.Begin(context.Background())
	if err := a.RecordTx(ctx, tx, Event{Action: "member.grant"}); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(context.Background())
	if p, _ := a.List(context.Background(), Query{Action: "member.grant"}); len(p.Records) != 0 {
		t.Fatal("rolled back record kept")
	}

	// A write that fails is the caller's error, never silent.
	pool.Exec(context.Background(), `ALTER TABLE audit_event RENAME TO audit_event_away`)
	if err := a.Record(ctx, Event{Action: "deploy.run"}); err == nil {
		t.Fatal("failed write not reported")
	}
	pool.Exec(context.Background(), `ALTER TABLE audit_event_away RENAME TO audit_event`)
}

func TestPrune(t *testing.T) {
	pool := testPool(t)
	a := New(Config{Retention: time.Hour}, pool)
	ctx := as("u1")
	for range 3 {
		if err := a.Record(ctx, Event{Action: "old"}); err != nil {
			t.Fatal(err)
		}
	}
	a.Record(ctx, Event{Action: "new"})
	pool.Exec(context.Background(), `UPDATE audit_event SET at = now() - interval '2 hours' WHERE action = 'old'`)
	n, err := a.Prune(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if p, _ := a.List(context.Background(), Query{}); len(p.Records) != 1 || p.Records[0].Action != "new" {
		t.Fatalf("left: %+v", p.Records)
	}
}

// Records written in one transaction keep their order.
func TestOrderInOneTransaction(t *testing.T) {
	pool := testPool(t)
	a := New(Config{}, pool)
	ctx := as("u1")
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := a.RecordTx(ctx, tx, Event{Action: "step", Resource: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ := a.List(context.Background(), Query{Action: "step"})
	var got []string
	for _, r := range p.Records {
		got = append(got, r.Resource)
	}
	if strings.Join(got, ",") != "4,3,2,1,0" {
		t.Fatalf("order: %v", got)
	}
}
