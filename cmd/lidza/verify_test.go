package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/scaffold"
	"github.com/agim/lidza/pkg/version"
)

// The agent files are a pre-commit requirement: stale ones are
// refreshed and the commit waits for them; current ones pass.
func TestAgentFilesCurrent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := scaffold.New(context.Background(), scaffold.Options{Name: "demo", Dir: dir, LidzaDir: "../..", SkipModTidy: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentFilesCurrent(dir, cfg); err != nil {
		t.Fatalf("new app: %v", err)
	}
	// An older release's guidance and a missing Codex config.
	agents := filepath.Join(dir, "AGENTS.md")
	data, _ := os.ReadFile(agents)
	os.WriteFile(agents, []byte(strings.Replace(string(data), "Never weaken a test", "Try not to weaken a test", 1)), 0o644)
	os.Remove(filepath.Join(dir, ".codex", "config.toml"))
	_, err = agentFilesCurrent(dir, cfg)
	if err == nil || !strings.Contains(err.Error(), "git add .codex/config.toml AGENTS.md") {
		t.Fatalf("stale: %v", err)
	}
	if _, err := agentFilesCurrent(dir, cfg); err != nil {
		t.Fatalf("after the refresh: %v", err)
	}
}

// A release records itself in the framework marker; the hook then passes
// the agent files without refreshing them until the CLI moves on.
func TestAgentFilesStamp(t *testing.T) {
	prev := version.Version
	t.Cleanup(func() { version.Version = prev })
	version.Version = "v9.9.0"
	dir := filepath.Join(t.TempDir(), "demo")
	// An app on a released framework (no local checkout in go.mod).
	if err := scaffold.New(context.Background(), scaffold.Options{Name: "demo", Dir: dir, SkipModTidy: true}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(dir)
	if s := scaffold.AgentStamp(dir); s != "v9.9.0" {
		t.Fatalf("stamp %q", s)
	}
	if why, err := agentFilesCurrent(dir, cfg); err != nil || why != "refreshed for v9.9.0" {
		t.Fatalf("current: %q %v", why, err)
	}
	// The next release refreshes: nothing else changed, the marker does,
	// and the commit carries it.
	version.Version = "v9.9.1"
	if _, err := agentFilesCurrent(dir, cfg); err == nil || !strings.Contains(err.Error(), "refreshed for v9.9.1: review them, then git add AGENTS.md") {
		t.Fatalf("next release: %v", err)
	}
	if why, err := agentFilesCurrent(dir, cfg); err != nil || why != "refreshed for v9.9.1" {
		t.Fatalf("after staging: %q %v", why, err)
	}
	// A build from source records nothing.
	version.Version = "v9.9.2-0.20261004120000-abcdefabcdef"
	if _, err := agentFilesCurrent(dir, cfg); err != nil || scaffold.AgentStamp(dir) != "v9.9.1" {
		t.Fatalf("source build: %v, stamp %q", err, scaffold.AgentStamp(dir))
	}
	// An app on a local framework checkout never records a release: its
	// code is the checkout's, whatever the CLI says.
	local := filepath.Join(t.TempDir(), "local")
	if err := scaffold.New(context.Background(), scaffold.Options{Name: "local", Dir: local, LidzaDir: "../..", SkipModTidy: true}); err != nil {
		t.Fatal(err)
	}
	lcfg, _ := config.Load(local)
	if _, err := agentFilesCurrent(local, lcfg); err != nil || scaffold.AgentStamp(local) != "" {
		t.Fatalf("local checkout: %v, stamp %q", err, scaffold.AgentStamp(local))
	}
}

// Test processes never see the master key, the key file, or a variable
// named like one of the app's credentials.
func TestTestEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(credentials.EnvMasterKey, strings.Repeat("cd", 32))
	if err := credentials.Set(dir, map[string]string{"dev.INVOICE_ISSUER_NAME": "Real Co", "API_TOKEN": "real"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INVOICE_ISSUER_NAME", "Real Co")
	t.Setenv("API_TOKEN", "real")
	t.Setenv("UNRELATED_SETTING", "kept")
	env := strings.Join(testEnv(dir), "\n") + "\n"
	for _, gone := range []string{credentials.EnvMasterKey + "=", "INVOICE_ISSUER_NAME=", "API_TOKEN="} {
		if strings.Contains(env, "\n"+gone) || strings.HasPrefix(env, gone) {
			t.Errorf("test env has %s", gone)
		}
	}
	if !strings.Contains(env, "UNRELATED_SETTING=kept\n") || !strings.Contains(env, credentials.EnvKeyOff+"=1\n") {
		t.Errorf("test env:\n%s", env)
	}
}
