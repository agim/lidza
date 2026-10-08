package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenLLMS: lidza gen llms writes public/llms.txt with the app's name
// as the H1, a summary line and a link per prerendered page by its
// title; it keeps an existing file unless --force; the htmx template's
// goes in static/.
func TestGenLLMS(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lidza.json", `{"name": "shop", "frontend": {"template": "react", "dist": "dist"}}`)
	write("go.mod", "module shop\n")
	write("dist/index.html", "<html><head><title>Shop &amp; more</title></head></html>")
	write("dist/about/index.html", "<html><head><title>About us</title></head></html>")
	write("dist/.locales/sq/index.html", "<title>Dyqani</title>")
	ctx := context.Background()
	if err := runGenLLMS(ctx, []string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "public", "llms.txt"))
	for _, want := range []string{"# shop\n\n> ", "## Pages\n\n- [Shop & more](/): ", "- [About us](/about): "} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "Dyqani") {
		t.Errorf("a locale's copy listed:\n%s", got)
	}
	if err := runGenLLMS(ctx, []string{"--dir", dir}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("overwrote without --force: %v", err)
	}
	if err := runGenLLMS(ctx, []string{"--dir", dir, "--force"}); err != nil {
		t.Errorf("--force: %v", err)
	}

	write("static/htmx.min.js", "")
	if err := runGenLLMS(ctx, []string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "static", "llms.txt")); err != nil {
		t.Errorf("htmx app: %v", err)
	}
	write("lidza.json", `{"name":"shop","frontend":{"template":"htmx"},"appDir":"app"}`)
	if err := runGenLLMS(ctx, []string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app", "static", "llms.txt")); err != nil {
		t.Fatal(err)
	}
}
