package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteDBHost(t *testing.T) {
	t.Setenv("PGHOST", "")
	for url, want := range map[string]string{
		"postgres://app@localhost:5432/app":                       "",
		"postgres://app@127.0.0.1/app":                            "",
		"postgres://app@127.0.0.2/app":                            "",
		"postgres://app@[::1]:5432/app":                           "",
		"postgres:///app?host=/var/run/postgresql":                "",
		"postgres:///app?host=/tmp":                               "",
		"host=/tmp dbname=app":                                    "",
		"host=localhost dbname=app":                               "",
		"postgresql://app@LOCALHOST/app":                          "",
		"postgres://app@db.example.com/app?host=/var/run/pg-sock": "",
		"postgres://app:secret@db.example.com:5432/app":           "db.example.com",
		"postgres://app@10.0.0.5/app?sslmode=require":             "10.0.0.5",
		"postgres://app@localhost,db.example.com/app":             "db.example.com",
		"host=db.internal dbname=app":                             "db.internal",
	} {
		if got := remoteDBHost(url); got != want {
			t.Errorf("remoteDBHost(%q) = %q, want %q", url, got, want)
		}
	}
}

// migrate and rollback refuse another host before connecting, and name
// the flag that allows it.
func TestDBRefusesRemoteHost(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DATABASE_URL=postgres://app@db.example.com:5432/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "")
	os.Unsetenv("DATABASE_URL")
	for _, sub := range []string{"migrate", "rollback"} {
		err := runDB(context.Background(), []string{sub, "--dir", dir})
		if err == nil || !strings.Contains(err.Error(), "db.example.com") || !strings.Contains(err.Error(), "lidza db "+sub+" --production") {
			t.Errorf("%s: %v", sub, err)
		}
	}
}

// A missing DATABASE_URL (a pack enabled after the .env was written)
// names the command that adds it.
func TestDBMissingURLNamesSetup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_NAME=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "")
	os.Unsetenv("DATABASE_URL")
	err := runDB(context.Background(), []string{"migrate", "--dir", dir})
	if err == nil || !strings.Contains(err.Error(), "run lidza install") {
		t.Fatalf("got %v", err)
	}
}

// A data migration is a hand-written file named after the newest
// migration, which lidza gen leaves alone.
func TestDBNew(t *testing.T) {
	dir := t.TempDir()
	migrations := filepath.Join(dir, "db", "migrations")
	os.MkdirAll(migrations, 0o755)
	os.WriteFile(filepath.Join(migrations, "29991231235959_ahead.up.sql"), nil, 0o644)
	if err := runDB(context.Background(), []string{"new", "--dir", dir, "Backfill post slugs!"}); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile(filepath.Join(migrations, "30000101000000_backfill_post_slugs.up.sql"))
	if err != nil || !strings.Contains(string(up), "lidza gen never changes it") {
		t.Fatalf("%s %v", up, err)
	}
	if _, err := os.Stat(filepath.Join(migrations, "30000101000000_backfill_post_slugs.down.sql")); err != nil {
		t.Fatal(err)
	}
	if err := runDB(context.Background(), []string{"new", "--dir", dir}); err == nil {
		t.Fatal("no description accepted")
	}
}
