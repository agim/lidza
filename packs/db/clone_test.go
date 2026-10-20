package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestCloneName(t *testing.T) {
	for _, c := range []struct{ name, key, want string }{
		{"app_test", "tests/billing", "app_pkg_tests_billing_test"},
		{"app_test", ".", "app_pkg_root_test"},
		{"app_test", "", "app_pkg_root_test"},
	} {
		if got := cloneName(c.name, c.key); got != c.want {
			t.Errorf("cloneName(%q, %q) = %q, want %q", c.name, c.key, got, c.want)
		}
	}
	long := strings.Repeat("area/", 30)
	a, b := cloneName("app_test", long+"one"), cloneName("app_test", long+"two")
	if len(a) > 63 || a == b || !strings.HasSuffix(a, "_test") || !strings.HasPrefix(a, "app_pkg_") {
		t.Errorf("long keys: %q %q", a, b)
	}
}

func TestWithDatabase(t *testing.T) {
	for in, want := range map[string]string{
		"postgres:///app_test?host=/var/run/postgresql":          "postgres:///app_pkg_x_test?host=/var/run/postgresql",
		"postgres://u:p@localhost:5432/app_test?sslmode=disable": "postgres://u:p@localhost:5432/app_pkg_x_test?sslmode=disable",
		"host=/tmp dbname=app_test user=me":                      "host=/tmp dbname=app_pkg_x_test user=me",
		"host=/tmp user=me":                                      "host=/tmp user=me dbname=app_pkg_x_test",
	} {
		got, err := withDatabase(in, "app_pkg_x_test")
		if err != nil || got != want {
			t.Errorf("withDatabase(%q) = %q %v, want %q", in, got, err, want)
		}
	}
}

func TestCloneTestDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// A template of its own: other packages hold connections to the
	// shared test database while the suite runs.
	src, err := withDatabase(testURL(t), "lidza_clonesrc_test")
	if err != nil {
		t.Fatal(err)
	}
	if err := DropTestDatabase(ctx, src); err != nil {
		t.Skipf("no test database: %v", err)
	}
	if err := EnsureDatabase(ctx, src); err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(func() { DropTestDatabase(context.Background(), src) })
	conn, err := pgx.Connect(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	conn.Exec(ctx, `CREATE TABLE things (name text); INSERT INTO things VALUES ('from the template')`)
	conn.Close(ctx)

	clone, err := CloneTestDatabase(ctx, src, "tests/billing")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clone, "lidza_clonesrc_pkg_tests_billing_test") {
		t.Fatalf("clone url %s", clone)
	}
	c, err := pgx.Connect(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	c.QueryRow(ctx, `SELECT name FROM things`).Scan(&name)
	c.Exec(ctx, `INSERT INTO things VALUES ('only in the clone')`)
	c.Close(ctx)
	if name != "from the template" {
		t.Fatalf("clone holds %q", name)
	}
	// Cloned again: fresh from the template.
	if _, err := CloneTestDatabase(ctx, src, "tests/billing"); err != nil {
		t.Fatal(err)
	}
	c, _ = pgx.Connect(ctx, clone)
	var n int
	c.QueryRow(ctx, `SELECT count(*) FROM things`).Scan(&n)
	c.Close(ctx)
	if n != 1 {
		t.Fatalf("a re-clone kept %d rows", n)
	}
	if dropped, err := DropTestClones(ctx, src); err != nil || dropped != 1 {
		t.Fatalf("drop clones: %d %v", dropped, err)
	}
	// Only test databases on this machine.
	if _, err := CloneTestDatabase(ctx, "postgres://db.example.com/app_test", "x"); err == nil {
		t.Error("cloned a remote database")
	}
	if _, err := CloneTestDatabase(ctx, "postgres:///app?host=/tmp", "x"); err == nil {
		t.Error("cloned a database not named _test")
	}
}
