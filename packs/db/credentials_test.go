package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/credentials"
)

func TestCredentialStore(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := Open(ctx, Config{URL: url, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	t.Cleanup(pool.Close)
	pool.Exec(ctx, `DROP TABLE IF EXISTS credential`)
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, "")
	t.Cleanup(func() { credentials.SetOverrides(nil) })
	store := NewCredentialStore(pool, dir)
	if err := store.Load(ctx); err != nil {
		t.Fatalf("load without a key must be a no-op: %v", err)
	}
	if err := store.Save(ctx, "MAIL_API_KEY", "x"); err == nil {
		t.Fatal("save without a key")
	}
	if _, err := credentials.Generate(dir); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, "MAIL_API_KEY", "key-1"); err != nil {
		t.Fatal(err)
	}
	if v := credentials.Overrides()["MAIL_API_KEY"]; v != "key-1" {
		t.Fatalf("override after save: %q", v)
	}
	var sealed string
	pool.QueryRow(ctx, `SELECT sealed FROM credential WHERE name = 'MAIL_API_KEY'`).Scan(&sealed)
	if strings.Contains(sealed, "key-1") {
		t.Fatal("stored in clear")
	}
	credentials.SetOverrides(nil)
	if err := store.Load(ctx); err != nil || credentials.Overrides()["MAIL_API_KEY"] != "key-1" {
		t.Fatalf("load: %v %v", err, credentials.Overrides())
	}
	// Another node saves and removes: Refresh sees each change once.
	other := NewCredentialStore(pool, dir)
	if changed, err := store.Refresh(ctx); err != nil || changed {
		t.Fatalf("refresh with no change: %v %v", changed, err)
	}
	if err := other.Save(ctx, "MAIL_DOMAIN", "example.com"); err != nil {
		t.Fatal(err)
	}
	credentials.SetOverrides(map[string]string{"MAIL_API_KEY": "key-1"})
	if changed, err := store.Refresh(ctx); err != nil || !changed || credentials.Overrides()["MAIL_DOMAIN"] != "example.com" {
		t.Fatalf("refresh after a save elsewhere: %v %v %v", changed, err, credentials.Overrides())
	}
	if changed, _ := store.Refresh(ctx); changed {
		t.Fatal("refresh twice")
	}
	pool.Exec(ctx, `DELETE FROM credential WHERE name = 'MAIL_DOMAIN'`)
	if changed, err := store.Refresh(ctx); err != nil || !changed || credentials.Overrides()["MAIL_DOMAIN"] != "" {
		t.Fatalf("refresh after a delete elsewhere: %v %v %v", changed, err, credentials.Overrides())
	}
	if names, err := store.Names(ctx); err != nil || strings.Join(names, ",") != "MAIL_API_KEY" {
		t.Fatalf("names: %v %v", names, err)
	}
	if err := store.Delete(ctx, "MAIL_API_KEY"); err != nil || credentials.Overrides()["MAIL_API_KEY"] != "" {
		t.Fatalf("delete: %v", err)
	}
}
