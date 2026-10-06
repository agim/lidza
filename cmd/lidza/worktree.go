package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agim/lidza/pkg/credentials"
)

// EnvDBSuffix names this checkout's databases apart from the others
// (<app>_<suffix>_dev, <app>_<suffix>_test): set it, or let a git
// worktree name them after itself.
const EnvDBSuffix = "LIDZA_DB_SUFFIX"

// worktree is a checkout that is not the repository's main one: a git
// worktree (parallel agents, a second branch).
type worktree struct {
	// suffix is "_<name>" for its databases; "" for the main checkout.
	suffix string
	// main is the main checkout's directory of the same project; "" when
	// unknown.
	main string
}

var nonIdent = regexp.MustCompile(`[^a-z0-9]+`)

// worktreeOf tells a linked git worktree from the main checkout. Its
// databases are suffixed with its directory's name, or LIDZA_DB_SUFFIX
// (which also names the main checkout's apart, when set).
func worktreeOf(dir string) worktree {
	var wt worktree
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	gitDir, common := git("rev-parse", "--absolute-git-dir"), git("rev-parse", "--path-format=absolute", "--git-common-dir")
	top := git("rev-parse", "--show-toplevel")
	if gitDir != "" && common != "" && gitDir != common && top != "" {
		// The project's place inside the repository, the same in both
		// (symlinks resolved: macOS's /tmp is /private/tmp to git).
		real := func(p string) string {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				return r
			}
			return p
		}
		rel, err := filepath.Rel(real(top), real(dir))
		if err == nil {
			wt.main = filepath.Join(filepath.Dir(common), rel)
		}
		wt.suffix = "_" + strings.Trim(nonIdent.ReplaceAllString(strings.ToLower(filepath.Base(top)), "_"), "_")
	}
	if s := os.Getenv(EnvDBSuffix); s != "" {
		wt.suffix = "_" + strings.Trim(nonIdent.ReplaceAllString(strings.ToLower(s), "_"), "_")
	}
	if wt.suffix == "_" {
		wt.suffix = ""
	}
	return wt
}

// prepare brings from the main checkout what git does not carry: .env
// (this checkout's databases then go in .env.local), the master key, and
// node_modules as a link when the lockfile is the same (else lidza
// install runs npm install as anywhere). It returns what it did.
func (wt worktree) prepare(dir string) []string {
	if wt.main == "" || wt.main == dir {
		return nil
	}
	var did []string
	copyIfMissing := func(rel string, perm os.FileMode) {
		dst := filepath.Join(dir, rel)
		if _, err := os.Stat(dst); err == nil {
			return
		}
		data, err := os.ReadFile(filepath.Join(wt.main, rel))
		if err != nil {
			return
		}
		if os.MkdirAll(filepath.Dir(dst), 0o755) == nil && os.WriteFile(dst, data, perm) == nil {
			did = append(did, rel+": copied from the main checkout ("+wt.main+")")
		}
	}
	copyIfMissing(".env", 0o600)
	copyIfMissing(filepath.FromSlash(credentials.MasterKeyFile), 0o600)
	nm := filepath.Join(dir, "node_modules")
	if _, err := os.Lstat(nm); err != nil {
		mainNM := filepath.Join(wt.main, "node_modules")
		a, errA := os.ReadFile(filepath.Join(dir, "package-lock.json"))
		b, errB := os.ReadFile(filepath.Join(wt.main, "package-lock.json"))
		if _, err := os.Stat(mainNM); err == nil && errA == nil && errB == nil && bytes.Equal(a, b) {
			if os.Symlink(mainNM, nm) == nil {
				did = append(did, "node_modules: linked to the main checkout's (the same package-lock.json)")
			}
		}
	}
	return did
}

// setEnvValue sets key=value in an env file (created if missing),
// replacing the key's line or appending one; it reports a change.
func setEnvValue(path, key, value string) (bool, error) {
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(data) == 0 {
		lines = nil
	}
	line := key + "=" + value
	for i, l := range lines {
		if k, _, ok := strings.Cut(strings.TrimSpace(l), "="); ok && strings.TrimSpace(k) == key {
			if l == line {
				return false, nil
			}
			lines[i] = line
			return true, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "# This checkout's own settings (never committed): lidza install writes its databases here.")
	}
	lines = append(lines, line)
	return true, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
