package main

import (
	"context"
	"os"
	"os/exec"
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

// TestCommitsBehind: a checkout behind its upstream says by how much (after
// a fetch); one that is level, or has no upstream, is 0.
func TestCommitsBehind(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	origin, a, b := filepath.Join(root, "origin"), filepath.Join(root, "a"), filepath.Join(root, "b")
	git(root, "init", "-q", "--bare", "-b", "master", origin)
	git(root, "clone", "-q", origin, a)
	git(a, "commit", "-q", "--allow-empty", "-m", "one")
	git(a, "push", "-q", "origin", "master")
	git(root, "clone", "-q", origin, b)
	ctx := context.Background()
	if n, _ := commitsBehind(ctx, b); n != 0 {
		t.Fatalf("level checkout behind by %d", n)
	}
	git(a, "commit", "-q", "--allow-empty", "-m", "two")
	git(a, "commit", "-q", "--allow-empty", "-m", "three")
	git(a, "push", "-q", "origin", "master")
	if n, upstream := commitsBehind(ctx, b); n != 2 || upstream != "origin/master" {
		t.Fatalf("behind %d of %q, want 2 of origin/master", n, upstream)
	}
	git(b, "checkout", "-q", "-b", "local")
	if n, _ := commitsBehind(ctx, b); n != 0 {
		t.Fatalf("branch without upstream behind by %d", n)
	}
	if n, _ := commitsBehind(ctx, root); n != 0 {
		t.Fatalf("outside git behind by %d", n)
	}
}
