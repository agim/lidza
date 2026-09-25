package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// testURL is the database the tests use; the framework host's lidza_test
// over the Unix socket by default.
func testURL(t *testing.T) string {
	if u := os.Getenv("LIDZA_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return "postgres:///lidza_test?host=/var/run/postgresql"
}

func TestMigrations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := Open(ctx, Config{URL: testURL(t), MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	defer pool.Close()
	pool.Exec(ctx, `DROP TABLE IF EXISTS lidza_test_things; DROP TABLE IF EXISTS `+Table)

	fsys := fstest.MapFS{
		"0001_init.up.sql":     {Data: []byte("-- init\nCREATE TABLE lidza_test_things (id serial PRIMARY KEY, name text NOT NULL);\n")},
		"0001_init.down.sql":   {Data: []byte("DROP TABLE lidza_test_things;\n")},
		"0002_alter.up.sql":    {Data: []byte("ALTER TABLE lidza_test_things ADD COLUMN note text;\n")},
		"0002_alter.down.sql":  {Data: []byte("ALTER TABLE lidza_test_things DROP COLUMN note;\n")},
		"0003_broken.up.sql":   {Data: []byte("ALTER TABLE nope ADD COLUMN x int;\n")},
		"0003_broken.down.sql": {Data: []byte("")},
	}
	good := fstest.MapFS{}
	for k, v := range fsys {
		if !strings.HasPrefix(k, "0003") {
			good[k] = v
		}
	}

	// No migrations directory yet (a fresh app whose schema has no model):
	// nothing to apply, not an error.
	if applied, err := Migrate(ctx, pool, os.DirFS(filepath.Join(t.TempDir(), "missing"))); err != nil || len(applied) != 0 {
		t.Fatalf("missing directory: %v %v", applied, err)
	}

	applied, err := Migrate(ctx, pool, good)
	if err != nil || strings.Join(applied, ",") != "0001_init,0002_alter" {
		t.Fatalf("migrate: %v %v", applied, err)
	}
	if again, err := Migrate(ctx, pool, good); err != nil || len(again) != 0 {
		t.Fatalf("second migrate: %v %v", again, err)
	}
	status, _ := Status(ctx, pool, good)
	if len(status) != 2 || !status[0].Applied || !status[1].Applied || status[1].AppliedAt.IsZero() {
		t.Fatalf("status: %+v", status)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO lidza_test_things (name, note) VALUES ('a', 'b')`); err != nil {
		t.Fatalf("schema not applied: %v", err)
	}

	// A failing migration rolls back and is not recorded.
	if _, err := Migrate(ctx, pool, fsys); err == nil || !strings.Contains(err.Error(), "0003_broken") {
		t.Fatalf("broken migration: %v", err)
	}
	status, _ = Status(ctx, pool, fsys)
	if status[2].Applied {
		t.Fatal("broken migration recorded as applied")
	}

	reverted, err := Rollback(ctx, pool, good, 1)
	if err != nil || strings.Join(reverted, ",") != "0002_alter" {
		t.Fatalf("rollback: %v %v", reverted, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO lidza_test_things (name, note) VALUES ('a', 'b')`); err == nil {
		t.Fatal("column still present after rollback")
	}
	reverted, err = Rollback(ctx, pool, good, 5)
	if err != nil || strings.Join(reverted, ",") != "0001_init" {
		t.Fatalf("rollback all: %v %v", reverted, err)
	}
	status, _ = Status(ctx, pool, good)
	if status[0].Applied || status[1].Applied {
		t.Fatalf("status after rollback: %+v", status)
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, Config{}); err == nil {
		t.Fatal("empty URL accepted")
	}
	if _, err := Open(ctx, Config{URL: "postgres://x@127.0.0.1:1/x", ConnectTimeout: 500 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("unreachable: %v", err)
	}
}
