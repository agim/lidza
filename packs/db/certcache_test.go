package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

func TestCertCache(t *testing.T) {
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
	pool.Exec(ctx, `DROP TABLE IF EXISTS tls_certificate`)
	var c autocert.Cache = NewCertCache(pool)
	if _, err := c.Get(ctx, "app.example.com"); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("miss: %v", err)
	}
	if err := c.Put(ctx, "app.example.com", []byte("pem-1")); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(ctx, "app.example.com", []byte("pem-2")); err != nil {
		t.Fatal(err)
	}
	if data, err := c.Get(ctx, "app.example.com"); err != nil || string(data) != "pem-2" {
		t.Fatalf("get: %q %v", data, err)
	}
	if err := c.Delete(ctx, "app.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "app.example.com"); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("after delete: %v", err)
	}
}
