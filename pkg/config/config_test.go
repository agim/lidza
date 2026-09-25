package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Default("demo", "react")
	if err := want.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "demo" || got.Frontend != want.Frontend || got.Dir != dir {
		t.Fatalf("got %+v", got)
	}
	if !got.Frontend.HasDevServer() {
		t.Fatal("react template should have a dev server")
	}
}

func TestHTMXHasNoDevServer(t *testing.T) {
	c := Default("demo", "htmx")
	if c.Frontend.HasDevServer() || c.Frontend.Dist != "" {
		t.Fatalf("got %+v", c.Frontend)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Fatal("missing file should error")
	}
	cases := map[string]string{
		"no name":      `{"frontend":{"template":"react"}}`,
		"no template":  `{"name":"x","frontend":{}}`,
		"dev only":     `{"name":"x","frontend":{"template":"react","dev":"npm run dev"}}`,
		"bad url":      `{"name":"x","frontend":{"template":"react","dev":"x","url":"127.0.0.1:5173"}}`,
		"dist escapes": `{"name":"x","frontend":{"template":"react","dist":"../out"}}`,
		"not json":     `{`,
	}
	for name, body := range cases {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
