package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/scaffold"
)

func goMod(t *testing.T, dir, require string, replace string) {
	t.Helper()
	mod := "module example.com/shop\n\ngo 1.27\n\nrequire github.com/agim/lidza " + require + "\n"
	if replace != "" {
		mod += "\nreplace github.com/agim/lidza => " + replace + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFrameworkDrift: a CLI release other than go.mod's is reported with
// both versions and the command that fixes it; a match, a build from
// source and a local checkout are not.
func TestFrameworkDrift(t *testing.T) {
	dir := t.TempDir()
	release := "v0.1.107"
	cliRelease = func() string { return release }
	t.Cleanup(func() { cliRelease = scaffold.Release })

	goMod(t, dir, "v0.1.106", "")
	d := frameworkDrift(dir)
	if d == nil || !d.newer || !strings.Contains(d.String(), "this CLI is v0.1.107 but go.mod pins v0.1.106") || !strings.Contains(d.String(), "lidza update --cli-only --to v0.1.106") {
		t.Fatalf("newer CLI: %v", d)
	}
	goMod(t, dir, "v0.1.108", "")
	if d := frameworkDrift(dir); d == nil || d.newer || !strings.Contains(d.String(), "older than the v0.1.108") {
		t.Fatalf("older CLI: %v", d)
	}
	goMod(t, dir, "v0.1.107", "")
	if d := frameworkDrift(dir); d != nil {
		t.Fatalf("matching: %v", d)
	}
	goMod(t, dir, "v0.1.100", "../lidza")
	if d := frameworkDrift(dir); d != nil {
		t.Fatalf("local checkout: %v", d)
	}
	release = ""
	goMod(t, dir, "v0.1.100", "")
	if d := frameworkDrift(dir); d != nil {
		t.Fatalf("CLI built from source: %v", d)
	}
}

// TestGenHoldsAgentFilesOnDrift: with another release's CLI, gen leaves
// AGENTS.md and the guide as they are.
func TestGenHoldsAgentFilesOnDrift(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	if err := scaffold.New(context.Background(), scaffold.Options{Name: "shop", Dir: dir, Template: "htmx", SkipModTidy: true}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(dir, "AGENTS.md")
	os.WriteFile(agents, []byte("# shop\n<!-- lidza:framework v0.1.106 -->\nold release text\n<!-- /lidza:framework -->\n"), 0o644)
	goMod(t, dir, "v0.1.106", "")
	cliRelease = func() string { return "v0.1.107" }
	t.Cleanup(func() { cliRelease = scaffold.Release })
	generateAll(dir, cfg, io.Discard)
	if b, _ := os.ReadFile(agents); !strings.Contains(string(b), "old release text") {
		t.Fatalf("gen rewrote AGENTS.md across releases:\n%s", b)
	}
}
