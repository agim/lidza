package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/devserver"
)

// TestSettings: app settings go into the app's database (sealed), list
// says where each value comes from without showing it, import moves the
// .env's app settings there, and infrastructure settings are refused.
func TestSettings(t *testing.T) {
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
	const schema = "lidza_test_settings"
	admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE; CREATE SCHEMA `+schema)
	defer admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	sep := "&"
	if !strings.Contains(url, "?") {
		sep = "?"
	}
	dir := t.TempDir()
	if _, err := credentials.Generate(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "lidza.json"), []byte(`{"name":"shop"}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("DATABASE_URL="+url+sep+"search_path="+schema+"\nDB_MAX_CONNS=4\nMAIL_PROVIDER=mailgun\nMAIL_API_KEY=key-from-env-file\n"), 0o600)
	t.Setenv(devserver.EnvMode, "")
	t.Setenv(credentials.EnvKeyOff, "")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	run := func(args ...string) (string, error) {
		var err error
		out := stdout(t, func() error {
			err = runSettings(ctx, append([]string{args[0], "--dir", dir}, args[1:]...))
			return nil
		})
		return out, err
	}

	if _, err := run("set", "DB_MAX_CONNS=20"); err == nil || !strings.Contains(err.Error(), "deployment") {
		t.Fatalf("an infra setting was accepted: %v", err)
	}
	if out, err := run("set", "LLM_PROVIDER=anthropic", "LLM_API_KEY=sk-ant-secret"); err != nil || !strings.Contains(out, "LLM_API_KEY, LLM_PROVIDER") {
		t.Fatalf("set: %q %v", out, err)
	}
	var sealed string
	admin.QueryRow(ctx, `SELECT sealed FROM `+schema+`.credential WHERE name = 'LLM_API_KEY'`).Scan(&sealed)
	if sealed == "" || strings.Contains(sealed, "sk-ant") {
		t.Fatalf("not sealed in the database: %q", sealed)
	}
	if out, err := run("get", "LLM_API_KEY"); err != nil || out != "sk-ant-secret\n" {
		t.Fatalf("get: %q %v", out, err)
	}
	out, err := run("list")
	if err != nil || strings.Contains(out, "sk-ant") || strings.Contains(out, "key-from-env-file") ||
		!strings.Contains(out, "LLM_API_KEY") || !strings.Contains(out, "saved in the app's database") || !strings.Contains(out, "MAIL_API_KEY") || strings.Contains(out, "DB_MAX_CONNS") {
		t.Fatalf("list: %q %v", out, err)
	}
	if out, err := run("import", "--dry-run"); err != nil || !strings.Contains(out, "would import") || !strings.Contains(out, "MAIL_API_KEY (from .env)") {
		t.Fatalf("dry run: %q %v", out, err)
	}
	if out, err := run("import"); err != nil || !strings.Contains(out, "MAIL_PROVIDER (from .env)") {
		t.Fatalf("import: %q %v", out, err)
	}
	if out, _ := run("get", "MAIL_API_KEY"); out != "key-from-env-file\n" {
		t.Fatalf("imported value: %q", out)
	}
	if out, _ := run("import"); !strings.Contains(out, "nothing to import") {
		t.Fatalf("second import: %q", out)
	}
	if _, err := run("unset", "LLM_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("get", "LLM_API_KEY"); err == nil {
		t.Fatal("unset value still saved")
	}
}
