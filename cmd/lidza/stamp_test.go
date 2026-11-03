package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "one")
	return dir
}

// A stamp names the tree a step passed on: the same tree matches, any
// change (staged or not, a new file too) does not, and the real index is
// never touched by the hashing.
func TestStamps(t *testing.T) {
	dir := gitRepo(t)
	tree := workTree(dir)
	if tree == "" || tree != workTree(dir) {
		t.Fatalf("tree %q unstable", tree)
	}
	markChecked(dir)
	if _, ok := passedOn(dir, "checked", workTree(dir)); !ok {
		t.Fatal("the check stamp does not match the unchanged tree")
	}
	if _, ok := passedOn(dir, "tested", workTree(dir)); ok {
		t.Fatal("no test ran, yet a test stamp matched")
	}
	// An unstaged edit changes the tree.
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644)
	if _, ok := passedOn(dir, "checked", workTree(dir)); ok {
		t.Fatal("an edited tree matched the stamp")
	}
	// The index is untouched: nothing staged.
	if out, _ := exec.Command("git", "-C", dir, "diff", "--cached", "--name-only").Output(); len(out) != 0 {
		t.Fatalf("the hashing staged files: %s", out)
	}
	// A new file counts.
	markChecked(dir)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644)
	if _, ok := passedOn(dir, "checked", workTree(dir)); ok {
		t.Fatal("a new file did not change the tree")
	}
	// verify records the index too: the hook compares what is committed.
	markVerified(dir)
	st, _ := readStamp(dir, "verified")
	if st.Staged == "" || st.Staged != stagedTree(dir) {
		t.Fatalf("staged %q vs %q", st.Staged, stagedTree(dir))
	}
	exec.Command("git", "-C", dir, "add", "-A").Run()
	if st.Staged == stagedTree(dir) {
		t.Fatal("staging changed nothing in the index hash")
	}
	// Outside a repository: no tree, so nothing matches.
	if workTree(t.TempDir()) != "" {
		t.Fatal("a tree outside git")
	}
}

// --install-hook writes lidza's hook, rewrites it when the mode changes
// (--full), and leaves a hook the developer wrote alone.
func TestInstallGitHookModes(t *testing.T) {
	dir := gitRepo(t)
	ctx := context.Background()
	hook := filepath.Join(dir, HookDir, "pre-commit")
	read := func() string { b, _ := os.ReadFile(hook); return strings.TrimSpace(string(b)) }
	if err := installGitHook(ctx, dir, io.Discard, false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(read(), "exec lidza verify --hook") {
		t.Fatalf("fast hook: %q", read())
	}
	installGitHook(ctx, dir, io.Discard, false, true)
	if !strings.HasSuffix(read(), "exec lidza verify --hook --full") {
		t.Fatalf("full hook: %q", read())
	}
	installGitHook(ctx, dir, io.Discard, false, false)
	if !strings.HasSuffix(read(), "exec lidza verify --hook") {
		t.Fatalf("back to fast: %q", read())
	}
	os.WriteFile(hook, []byte("#!/bin/sh\necho mine\n"), 0o755)
	installGitHook(ctx, dir, io.Discard, false, true)
	if read() != "#!/bin/sh\necho mine" {
		t.Fatalf("the developer's hook was replaced: %q", read())
	}
}
