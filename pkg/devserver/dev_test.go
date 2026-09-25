package devserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main")
	write("go.mod", "module x")
	write("node_modules/x/index.go", "ignored")
	write("src/App.tsx", "ignored")

	w := newWatcher(dir)
	if w.scan() {
		t.Fatal("first scan must not report a change")
	}
	if w.scan() {
		t.Fatal("no change, but scan reported one")
	}

	// Ignored files never trigger.
	write("src/App.tsx", "still ignored")
	write("node_modules/x/index.go", "still ignored")
	if w.scan() {
		t.Fatal("non-Go change reported")
	}

	// A new Go file triggers.
	write("routes.go", "package main")
	if !w.scan() {
		t.Fatal("new Go file not reported")
	}

	// A modified Go file triggers (bump mtime explicitly; coarse clocks).
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "main.go"), future, future); err != nil {
		t.Fatal(err)
	}
	if !w.scan() {
		t.Fatal("modified Go file not reported")
	}

	// A deleted Go file triggers.
	os.Remove(filepath.Join(dir, "routes.go"))
	if !w.scan() {
		t.Fatal("deleted Go file not reported")
	}
}
