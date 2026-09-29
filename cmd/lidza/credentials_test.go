package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/credentials"
)

// stdout runs f and returns what it printed.
func stdout(t *testing.T, f func() error) string {
	t.Helper()
	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w
	err := f()
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return string(out)
}

// A setting saved from the admin pages lives in the app's database over
// the file: list, show and unset see it, so a value that stops the app
// from starting can be cleared without the pages.
func TestCredentialsSavedSettings(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	admin, err := db.Open(ctx, db.Config{URL: url, MaxConns: 1, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	defer admin.Close()
	// A schema of its own: the db pack's tests drop the shared table.
	schema := fmt.Sprintf("cli_credentials_%d", os.Getpid())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
		t.Skipf("no schema: %v", err)
	}
	t.Cleanup(func() { admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	appURL := url + sep + "search_path=" + schema

	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	os.WriteFile(filepath.Join(dir, "lidza.json"), []byte(`{"name":"demo","frontend":{"template":"htmx"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("DATABASE_URL="+appURL+"\n"), 0o600)
	if _, err := credentials.Generate(dir); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Set(dir, map[string]string{"MAIL_API_KEY": "from-file"}); err != nil {
		t.Fatal(err)
	}
	// No table yet: the file alone.
	if out := stdout(t, func() error { return runCredentials(ctx, []string{"list", "--dir", dir}) }); out != "MAIL_API_KEY\n" {
		t.Fatalf("list before anything was saved: %q", out)
	}

	pool, err := db.Open(ctx, db.Config{URL: appURL, MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.NewCredentialStore(pool, dir).Save(ctx, "STORAGE_PROVIDER", "spaces"); err != nil {
		t.Fatal(err)
	}
	credentials.SetOverrides(nil)

	if out := stdout(t, func() error { return runCredentials(ctx, []string{"list", "--dir", dir}) }); out != "MAIL_API_KEY\nSTORAGE_PROVIDER (saved from the admin pages)\n" {
		t.Fatalf("list: %q", out)
	}
	if out := stdout(t, func() error { return runCredentials(ctx, []string{"show", "--dir", dir}) }); !strings.Contains(out, "from-file") || !strings.Contains(out, "Saved from the admin pages") || !strings.Contains(out, "spaces") {
		t.Fatalf("show: %q", out)
	}
	if out := stdout(t, func() error { return runCredentials(ctx, []string{"show", "--dir", dir, "STORAGE_PROVIDER"}) }); out != "spaces\n" {
		t.Fatalf("show STORAGE_PROVIDER: %q", out)
	}
	if out := stdout(t, func() error { return runCredentials(ctx, []string{"unset", "--dir", dir, "STORAGE_PROVIDER"}) }); !strings.Contains(out, "removed STORAGE_PROVIDER from the settings saved from the admin pages") {
		t.Fatalf("unset: %q", out)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM credential").Scan(&n); err != nil || n != 0 {
		t.Fatalf("still saved: %d %v", n, err)
	}
}
