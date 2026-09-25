package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
)

func TestProductionEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Name: "tracker"}
	p, err := writeProductionEnv(dir, cfg)
	if err != nil || p != filepath.Join("deploy", "production.env") {
		t.Fatal(p, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, p))
	if !strings.Contains(string(data), "LIDZA_ADDR=0.0.0.0:3000") || !strings.Contains(string(data), "# LIDZA_TLS_DOMAINS=") || strings.Contains(string(data), "DATABASE_URL=") {
		t.Fatalf("without domains:\n%s", data)
	}
	cfg.Deploy = config.Deploy{Domains: splitDomains(" App.Example.com, www.app.example.com "), Email: "ops@example.com"}
	writeProductionEnv(dir, cfg)
	data, _ = os.ReadFile(filepath.Join(dir, p))
	for _, want := range []string{"LIDZA_TLS_DOMAINS=app.example.com,www.app.example.com\n", "LIDZA_TLS_EMAIL=ops@example.com\n", "LIDZA_LOG=json\n", "DB_MIGRATE=true\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "LIDZA_ADDR=") {
		t.Errorf("LIDZA_ADDR with TLS on:\n%s", data)
	}
}
