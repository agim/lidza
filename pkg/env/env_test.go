package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type cfg struct {
	URL     string        `env:"T_URL" required:"true"`
	Workers int           `env:"T_WORKERS" default:"4"`
	Timeout time.Duration `env:"T_TIMEOUT" default:"30s"`
	Debug   bool          `env:"T_DEBUG"`
	Origins []string      `env:"T_ORIGINS"`
	Ratio   float64       `env:"T_RATIO" default:"0.5"`
	skip    string        `env:"T_SKIP"` // unexported: must be ignored
	NoTag   string
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("# base\nT_URL=postgres://base\nT_WORKERS=2\nexport T_DEBUG=true\nT_ORIGINS=\"a, b\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env.dev"), []byte("T_URL='postgres://dev' # comment\nT_TIMEOUT=5s\n"), 0o644)
	t.Setenv("LIDZA_MODE", "dev")
	t.Setenv("T_WORKERS", "8")

	var c cfg
	if err := Load(dir, &c); err != nil {
		t.Fatal(err)
	}
	if c.skip != "" {
		t.Fatal("unexported field was set")
	}
	if c.URL != "postgres://dev" || c.Workers != 8 || c.Timeout != 5*time.Second || !c.Debug || strings.Join(c.Origins, "|") != "a|b" || c.Ratio != 0.5 {
		t.Fatalf("got %+v", c)
	}
	if Mode() != "dev" {
		t.Fatal("mode")
	}
}

func TestErrors(t *testing.T) {
	var c cfg
	err := Fill(&c, map[string]string{"T_WORKERS": "many", "T_TIMEOUT": "soon"})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"T_URL is required", "T_WORKERS: expected an integer", "T_TIMEOUT: expected a duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	if err := Fill(c, nil); err == nil {
		t.Fatal("non-pointer accepted")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("NOT A PAIR\n"), 0o644)
	t.Setenv("LIDZA_MODE", "")
	if err := Load(dir, &c); err == nil || !strings.Contains(err.Error(), ".env:1") {
		t.Fatalf("bad line: %v", err)
	}
	if Mode() != "production" {
		t.Fatal("default mode")
	}
}
