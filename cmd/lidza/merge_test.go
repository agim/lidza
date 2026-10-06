package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/scaffold"
	"github.com/agim/lidza/pkg/schema"
)

// Two branches that each change the schema and record a decision merge
// without a conflict: the lock by the merge driver, the decisions by
// git's union merge; the migrations, named by time, are both there.
func TestMergeBranches(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	bin := filepath.Join(t.TempDir(), "lidza")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
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
	gen := func(src string) {
		t.Helper()
		os.WriteFile(filepath.Join(dir, schema.FileName), []byte(src), 0o644)
		s, err := schema.Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schema.Generate(dir, s, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	decide := func(entry string) {
		p := filepath.Join(dir, "docs", "decisions.md")
		data, _ := os.ReadFile(p)
		os.WriteFile(p, append(data, []byte(entry)...), 0o644)
	}
	git("init", "-q", "-b", "master")
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	decide("# Decisions\n\n")
	gen("model Post {\n  id int @id\n  title string\n}\n")
	if _, err := scaffold.EnsureMerging(dir); err != nil {
		t.Fatal(err)
	}
	git("config", "merge.lidza-lock.driver", bin+" gen --merge-lock %O %A %B")
	git("add", ".")
	git("commit", "-q", "-m", "base")

	git("checkout", "-q", "-b", "tags")
	gen("model Tag {\n  id int @id\n}\n\nmodel Post {\n  id int @id\n  title string\n}\n")
	decide("## Tags\n\nWhy tags.\n\n")
	git("add", ".")
	git("commit", "-q", "-m", "tags")

	git("checkout", "-q", "master")
	gen("model Post {\n  id int @id\n  title string\n  body text\n}\n")
	decide("## Bodies\n\nWhy bodies.\n\n")
	git("add", ".")
	git("commit", "-q", "-m", "bodies")

	git("merge", "-q", "--no-edit", "tags")
	lock, _ := schema.LoadLock(dir)
	if lock.Model("Tag") == nil || len(lock.Model("Post").Fields) != 3 {
		t.Fatalf("merged lock: %+v", lock)
	}
	dec, _ := os.ReadFile(filepath.Join(dir, "docs", "decisions.md"))
	if !strings.Contains(string(dec), "Why tags.") || !strings.Contains(string(dec), "Why bodies.") || strings.Contains(string(dec), "<<<<<<<") {
		t.Fatalf("decisions:\n%s", dec)
	}
	if n := len(schema.Migrations(dir)); n != 3 {
		t.Fatalf("migrations: %v", schema.Migrations(dir))
	}
}
