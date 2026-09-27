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
	write("views/index.html", "page")
	write("schema.lidza", "type A { x int }")

	w := newWatcher(dir, "schema.lidza", "views")
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

	// Extra files and directories trigger.
	write("schema.lidza", "type A { x int y int }")
	os.Chtimes(filepath.Join(dir, "schema.lidza"), future, future)
	if !w.scan() {
		t.Fatal("schema change not reported")
	}
	write("views/partials/new.html", "x")
	if !w.scan() {
		t.Fatal("new file in watched dir not reported")
	}
}

// lidza db migrate asks a running lidza dev to restart through a stamp
// file; each request moves the stamp.
func TestRequestRestart(t *testing.T) {
	dir := t.TempDir()
	if !restartStamp(dir).IsZero() {
		t.Fatal("stamp without a request")
	}
	if err := RequestRestart(dir); err != nil {
		t.Fatal(err)
	}
	first := restartStamp(dir)
	if first.IsZero() {
		t.Fatal("no stamp after a request")
	}
	past := first.Add(-time.Minute)
	os.Chtimes(filepath.Join(dir, RestartFile), past, past)
	RequestRestart(dir)
	if restartStamp(dir).Equal(past) {
		t.Fatal("a second request did not move the stamp")
	}
}

// The pid file names a live dev server and its version; a dead pid or no
// file is no dev server.
func TestRunningDev(t *testing.T) {
	dir := t.TempDir()
	if _, _, ok := RunningDev(dir); ok {
		t.Fatal("dev server without a record")
	}
	if err := WritePID(dir, "v0.1.28"); err != nil {
		t.Fatal(err)
	}
	if pid, v, ok := RunningDev(dir); !ok || pid != os.Getpid() || v != "v0.1.28" {
		t.Fatalf("running: %d %s %v", pid, v, ok)
	}
	RemovePID(dir)
	if _, _, ok := RunningDev(dir); ok {
		t.Fatal("still recorded after RemovePID")
	}
	os.WriteFile(filepath.Join(dir, PIDFile), []byte("999999999 v0.1.1\n"), 0o644)
	if _, _, ok := RunningDev(dir); ok {
		t.Fatal("a dead pid counts as running")
	}
}
