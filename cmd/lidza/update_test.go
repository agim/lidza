package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
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

// Update never moves back: a CLI or an app on a release commit past the
// newest tag keeps it.
func TestNoDowngrade(t *testing.T) {
	pseudo := "v0.1.76-0.20261005203514-35633f34042e"
	for _, c := range []struct {
		newest string
		have   []string
		want   string
	}{
		{"v0.1.75", []string{pseudo, ""}, pseudo},
		{"v0.1.75", []string{"v0.1.74", "v0.1.73"}, "v0.1.75"},
		{"v0.1.77", []string{pseudo, pseudo}, "v0.1.77"},
		{"v0.1.75", []string{"", "dev (../lidza)"}, "v0.1.75"},
		{"v0.1.75", []string{"v0.1.75", "v0.1.76"}, "v0.1.76"},
	} {
		if got := noDowngrade(c.newest, c.have...); got != c.want {
			t.Errorf("noDowngrade(%s, %v) = %s, want %s", c.newest, c.have, got, c.want)
		}
	}
	for s, want := range map[string]bool{"35633f3": true, "35633f34042e": true, "master": true, "v0.1.77": false, "0.1.77": false, "zzzzzzz": false} {
		if isCommit(s) != want {
			t.Errorf("isCommit(%q) != %v", s, want)
		}
	}
}

// A branch only behind, with nothing uncommitted, is fast-forwarded;
// uncommitted changes or a diverged branch stop the update untouched.
func TestFastForward(t *testing.T) {
	root := t.TempDir()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin, a, b := filepath.Join(root, "origin"), filepath.Join(root, "a"), filepath.Join(root, "b")
	git(root, "init", "-q", "--bare", "-b", "master", origin)
	git(root, "clone", "-q", origin, a)
	os.WriteFile(filepath.Join(a, "schema.lidza"), []byte("v1\n"), 0o644)
	git(a, "add", ".")
	git(a, "commit", "-q", "-m", "one")
	git(a, "push", "-q", "origin", "master")
	git(root, "clone", "-q", origin, b)
	push := func(content string) {
		os.WriteFile(filepath.Join(a, "schema.lidza"), []byte(content), 0o644)
		git(a, "commit", "-q", "-am", content)
		git(a, "push", "-q", "origin", "master")
	}
	ctx := context.Background()

	// Uncommitted work: stopped, nothing pulled.
	push("v2\n")
	os.WriteFile(filepath.Join(b, "schema.lidza"), []byte("mine\n"), 0o644)
	n, up := commitsBehind(ctx, b)
	if err := fastForward(ctx, b, n, up); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(b, "schema.lidza")); string(data) != "mine\n" {
		t.Fatalf("work in progress touched: %q", data)
	}
	git(b, "checkout", "--", "schema.lidza")

	// Only behind: fast-forwarded.
	if err := fastForward(ctx, b, n, up); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(b, "schema.lidza")); string(data) != "v2\n" {
		t.Fatalf("not pulled: %q", data)
	}

	// Diverged: stopped, the local commit kept, no merge made.
	push("v3\n")
	git(b, "commit", "-q", "--allow-empty", "-m", "local work")
	n, up = commitsBehind(ctx, b)
	if err := fastForward(ctx, b, n, up); err == nil || !strings.Contains(err.Error(), "diverged (1 behind, 1 ahead)") {
		t.Fatalf("diverged: %v", err)
	}
	if msg := git(b, "log", "-1", "--format=%s"); msg != "local work" {
		t.Fatalf("head moved: %s", msg)
	}
}

// The update's own files: uncommitted ones are found (staged or not, a
// file elsewhere ignored), and --commit commits them, new files included.
func TestUpdateDirtyAndCommit(t *testing.T) {
	dir := t.TempDir()
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(k, v)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "master")
	for _, f := range []string{"go.mod", "Dockerfile", "AGENTS.md", "main.go"} {
		os.WriteFile(filepath.Join(dir, f), []byte(f+"\n"), 0o644)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	cfg := config.Default("demo", "react")
	ctx := context.Background()
	if d := dirtyFiles(ctx, dir, updatePaths(&cfg)); len(d) != 0 {
		t.Fatalf("clean tree: %v", d)
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("changed\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("staged\n"), 0o644)
	git("add", "AGENTS.md")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("app work\n"), 0o644)
	if d := strings.Join(dirtyFiles(ctx, dir, updatePaths(&cfg)), ","); d != "AGENTS.md,go.mod" {
		t.Fatalf("dirty: %s", d)
	}
	os.MkdirAll(filepath.Join(dir, "db", "migrations"), 0o755)
	os.WriteFile(filepath.Join(dir, "db", "migrations", "0001_x.up.sql"), []byte("SELECT 1;\n"), 0o644)
	if err := commitUpdate(ctx, dir, updatePaths(&cfg), "Līdza v9.9.9"); err != nil {
		t.Fatal(err)
	}
	if files := git("show", "--name-only", "--format=%s", "HEAD"); files != "Līdza v9.9.9\n\nAGENTS.md\ndb/migrations/0001_x.up.sql\ngo.mod" {
		t.Fatalf("commit:\n%s", files)
	}
	if st := git("status", "--porcelain"); st != "M main.go" {
		t.Fatalf("the app's own work was committed or lost: %q", st)
	}
}
