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

// ship names what the enabled packs need in production and the
// credentials do not hold, including a development-only value.
func TestProductionNeeds(t *testing.T) {
	cfg := &config.Config{Name: "galeria", Packs: []string{"lidza/db", "lidza/cache", "lidza/auth", "lidza/mail", "lidza/llm", "packs/palette"}}
	needs := strings.Join(productionNeeds(cfg, map[string]string{"AUTH_SECRET": "x", "MAIL_PROVIDER": "mailgun", "MAIL_FROM": "a@b.c", "LLM_PROVIDER": "Fake"}), "\n")
	for _, want := range []string{"CACHE_URL (lidza/cache)", "APP_URL (lidza/mail)", `LLM_PROVIDER (lidza/llm) is "Fake", which suits development only`} {
		if !strings.Contains(needs, want) {
			t.Errorf("missing %q in:\n%s", want, needs)
		}
	}
	for _, not := range []string{"DATABASE_URL", "AUTH_SECRET", "MAIL_PROVIDER", "MAIL_FROM", "STORAGE"} {
		if strings.Contains(needs, not) {
			t.Errorf("%s flagged:\n%s", not, needs)
		}
	}
	dir := t.TempDir()
	writeProductionEnv(dir, cfg)
	if data, _ := os.ReadFile(filepath.Join(dir, "deploy", "production.env")); !strings.Contains(string(data), "# CACHE_URL (lidza/cache): a Valkey or Redis address") {
		t.Fatalf("production.env:\n%s", data)
	}
}
