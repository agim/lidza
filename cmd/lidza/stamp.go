package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agim/lidza/pkg/version"
)

// A stamp records that a tree passed a step, so the step is not repeated
// on the same tree: lidza check writes .lidza/checked.json, lidza test
// .lidza/tested.json, lidza verify .lidza/verified.json. verify reuses a
// check and a test that passed on the tree as it is, and the pre-commit
// hook a verify that passed on the tree being committed, so an agent's
// own check, test and verify count once each instead of running three
// times before a commit (minutes each on a large app).
type stamp struct {
	// Tree names the working tree's content (every tracked and untracked
	// file, ignored ones aside), as git would write it.
	Tree string `json:"tree"`
	// Staged names the index: what a commit made now would hold.
	Staged string    `json:"staged,omitempty"`
	At     time.Time `json:"at"`
	Lidza  string    `json:"lidza"`
}

func stampPath(dir, name string) string {
	return filepath.Join(dir, ".lidza", name+".json")
}

func readStamp(dir, name string) (stamp, bool) {
	data, err := os.ReadFile(stampPath(dir, name))
	if err != nil {
		return stamp{}, false
	}
	var st stamp
	if json.Unmarshal(data, &st) != nil || st.Tree == "" {
		return stamp{}, false
	}
	return st, true
}

func writeStamp(dir, name string, st stamp) {
	st.At = time.Now()
	st.Lidza = version.String()
	data, _ := json.Marshal(st)
	p := stampPath(dir, name)
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, data, 0o644)
	}
}

// workTree hashes the working tree: a temporary index with every file
// added, written as a tree, so the real index is untouched. Outside a
// repository, a hash of nothing: no stamp then matches.
func workTree(dir string) string {
	gitDir, err := git(dir, "rev-parse", "--git-dir")
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	tmp, err := os.CreateTemp(gitDir, "lidza-index-*")
	if err != nil {
		return ""
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	// Start from the real index so unchanged entries cost nothing.
	if data, err := os.ReadFile(filepath.Join(gitDir, "index")); err == nil {
		_ = os.WriteFile(tmp.Name(), data, 0o644)
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+tmp.Name())
	if _, err := gitEnv(dir, env, "add", "-A", "--", "."); err != nil {
		return ""
	}
	// Never .lidza/, which holds the stamps themselves and the scratch
	// output: an app that does not ignore it would never match twice.
	_, _ = gitEnv(dir, env, "rm", "-r", "-q", "--cached", "--ignore-unmatch", "--", ".lidza")
	tree, err := gitEnv(dir, env, "write-tree")
	if err != nil {
		return ""
	}
	return tree
}

// stagedTree hashes the index: what `git commit` would record now.
func stagedTree(dir string) string {
	tree, err := git(dir, "write-tree")
	if err != nil {
		return ""
	}
	return tree
}

func git(dir string, args ...string) (string, error) {
	return gitEnv(dir, nil, args...)
}

func gitEnv(dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// markChecked records that the tree passed lidza check.
func markChecked(dir string) {
	if t := workTree(dir); t != "" {
		writeStamp(dir, "checked", stamp{Tree: t})
	}
}

// markTested records that the tree's Go tests passed.
func markTested(dir string) {
	if t := workTree(dir); t != "" {
		writeStamp(dir, "tested", stamp{Tree: t})
	}
}

// markVerified records that the tree passed lidza verify, with the index
// the hook compares against.
func markVerified(dir string) {
	if t := workTree(dir); t != "" {
		writeStamp(dir, "verified", stamp{Tree: t, Staged: stagedTree(dir)})
	}
}

// passedOn reports whether the named step passed on this very tree, and
// how long ago.
func passedOn(dir, name, tree string) (time.Duration, bool) {
	st, ok := readStamp(dir, name)
	if !ok || tree == "" || st.Tree != tree || st.Lidza != version.String() {
		return 0, false
	}
	return time.Since(st.At), true
}

// inGitHook reports whether this process was started by git (the
// pre-commit hook): git exports its paths to hooks.
func inGitHook() bool {
	return os.Getenv("GIT_EXEC_PATH") != "" || os.Getenv("GIT_INDEX_FILE") != ""
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm ago", int(d.Hours()), int(d.Minutes())%60)
	}
}
