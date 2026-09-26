package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewestOf(t *testing.T) {
	v, err := newestOf("github.com/agim/lidza v0.1.0 v0.1.10 v0.1.2 v0.1.9 v0.2.0-rc1")
	if err != nil || v != "v0.1.10" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := newestOf("github.com/agim/lidza"); err == nil {
		t.Fatal("no versions accepted")
	}
}

func TestRepin(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Dockerfile")
	os.WriteFile(p, []byte("RUN go install github.com/agim/lidza/cmd/lidza@v0.1.2\nRUN echo done\n"), 0o644)
	if n, err := repin(p, "v0.1.11"); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "lidza@v0.1.11\n") {
		t.Fatalf("%s", data)
	}
	if n, _ := repin(p, "v0.1.11"); n != 0 {
		t.Fatal("rewrote an unchanged file")
	}
}
