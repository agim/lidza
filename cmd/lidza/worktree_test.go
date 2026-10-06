package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/env"
)

// A git worktree of an app gets its own databases and what git does not
// carry from the main checkout: .env, the master key, node_modules.
func TestWorktree(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(k, v)
	}
	t.Setenv(EnvDBSuffix, "")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	main := filepath.Join(root, "repo")
	app := filepath.Join(main, "app") // the app in a subdirectory of the repository
	os.MkdirAll(filepath.Join(app, "config"), 0o755)
	os.MkdirAll(filepath.Join(app, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(app, "lidza.json"), []byte(`{"name":"shop","frontend":{"template":"htmx"},"packs":["lidza/db"]}`), 0o644)
	os.WriteFile(filepath.Join(app, ".gitignore"), []byte(".env\n.env.*\n!.env.test\nconfig/master.key\nnode_modules/\n"), 0o644)
	os.WriteFile(filepath.Join(app, ".env"), []byte("DATABASE_URL=postgres:///shop_dev?host=/tmp\nAPI_MODE=sandbox\n"), 0o600)
	os.WriteFile(filepath.Join(app, ".env.test"), []byte("DATABASE_URL=postgres:///shop_test?host=/tmp\n"), 0o644)
	os.WriteFile(filepath.Join(app, "config", "master.key"), []byte("ab\n"), 0o600)
	os.WriteFile(filepath.Join(app, "package-lock.json"), []byte("{}\n"), 0o644)
	git(main, "init", "-q", "-b", "master")
	git(main, "add", ".")
	git(main, "commit", "-q", "-m", "init")
	if wt := worktreeOf(app); wt.suffix != "" {
		t.Fatalf("main checkout has a suffix %q", wt.suffix)
	}

	tree := filepath.Join(root, "Feature-X")
	git(main, "worktree", "add", "-q", "-b", "feature", tree)
	wtApp := filepath.Join(tree, "app")
	wt := worktreeOf(wtApp)
	if wt.suffix != "_feature_x" || wt.main != app {
		t.Fatalf("worktree: %+v", wt)
	}
	did := strings.Join(wt.prepare(wtApp), "\n")
	for _, want := range []string{".env: copied", "master.key: copied", "node_modules: linked"} {
		if !strings.Contains(did, want) {
			t.Errorf("prepare lacks %q:\n%s", want, did)
		}
	}
	if fi, err := os.Lstat(filepath.Join(wtApp, "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("node_modules not linked")
	}

	// The test database: the worktree's own, over the committed one.
	ensureWorktreeTestDB(wtApp)
	t.Setenv("LIDZA_MODE", "test")
	v, _ := env.Values(wtApp)
	if !strings.Contains(v["DATABASE_URL"], "/shop_feature_x_test?") {
		t.Fatalf("test database: %s", v["DATABASE_URL"])
	}
	// The main checkout keeps the shared one.
	if v, _ := env.Values(app); !strings.Contains(v["DATABASE_URL"], "/shop_test?") {
		t.Fatalf("main checkout: %s", v["DATABASE_URL"])
	}
	// LIDZA_DB_SUFFIX names any checkout apart.
	t.Setenv(EnvDBSuffix, "Agent 2")
	if wt := worktreeOf(app); wt.suffix != "_agent_2" {
		t.Fatalf("suffix: %q", wt.suffix)
	}
}
