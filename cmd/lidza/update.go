package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/scaffold"
	"github.com/agim/lidza/pkg/version"
)

// runUpdate is `lidza update [--to vX.Y.Z] [--cli-only] [--migrate] [--no-pull] [--allow-behind]`: the
// CLI to the newest release (or the one named), and, in a project, the
// framework module to the same version, go.mod tidied, the Dockerfile's
// pin rewritten, everything regenerated with the new CLI, then lidza
// install (.env, databases migrated, node_modules); --migrate lets a
// migration that drops data run too. A branch behind its upstream is
// pulled first when that is a clean fast-forward, refused otherwise.
func runUpdate(ctx context.Context, args []string) error {
	fs := flags("update")
	dir := fs.String("dir", ".", "project directory")
	to := fs.String("to", "", "the release to move to (default: the newest); a commit (hash or master) for a release not tagged yet")
	cliOnly := fs.Bool("cli-only", false, "the CLI only; leave the project alone")
	migrate := fs.Bool("migrate", false, "also apply migrations that drop data to the development database")
	behind := fs.Bool("allow-behind", false, "update even when the branch is behind its upstream, without pulling")
	noPull := fs.Bool("no-pull", false, "do not pull a branch that is behind; stop instead")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The app's branch first: a teammate's commits may move go.mod to a
	// newer framework, and updating a stale checkout regenerates from an
	// old schema whose migrations collide with the ones the pull brings.
	// A branch that is only behind, with nothing uncommitted, is
	// fast-forwarded; any other is left to the developer.
	if !*cliOnly && !*behind {
		if abs, _, err := loadProject(*dir); err == nil {
			if n, upstream := commitsBehind(ctx, abs); n > 0 {
				if *noPull {
					return fmt.Errorf("update: this branch is %d commit(s) behind %s; git pull first, then lidza update (--allow-behind updates anyway)", n, upstream)
				}
				if err := fastForward(ctx, abs, n, upstream); err != nil {
					return err
				}
			}
		}
	}
	current := version.String()
	var target string
	switch {
	case *to != "" && isCommit(*to):
		// A release commit not tagged yet: its pseudo-version.
		v, err := commitVersion(ctx, *to)
		if err != nil {
			return err
		}
		target = v
		fmt.Printf("[update] CLI %s, moving to %s (commit %s)\n", current, target, *to)
	case *to != "":
		target = *to
		if !strings.HasPrefix(target, "v") {
			target = "v" + target
		}
		fmt.Printf("[update] CLI %s, moving to %s\n", current, target)
	default:
		newest, err := latestVersion(ctx)
		if err != nil {
			return err
		}
		// Never a downgrade: a CLI or an app already past the newest tag
		// (a release commit not tagged yet) keeps the newer version.
		target = noDowngrade(newest, version.Module(), scaffold.AppVersions(*dir).Lidza)
		if target != newest {
			fmt.Printf("[update] CLI %s; the newest tag is %s, older than %s: keeping %s (--to names a release to move to)\n", current, newest, target, target)
		} else {
			fmt.Printf("[update] CLI %s, newest release %s\n", current, target)
		}
	}

	// 1. The CLI.
	exe, _ := os.Executable()
	if current == target || version.Module() == target {
		fmt.Println("[update] CLI already there")
	} else {
		fmt.Printf("[update] go install %s/cmd/lidza@%s\n", version.ModulePath, target)
		if err := run(ctx, ".", "go", "install", version.ModulePath+"/cmd/lidza@"+target); err != nil {
			return fmt.Errorf("update: installing the CLI failed (network, or is %s a release? see CHANGELOG.md)", target)
		}
		if p, err := exec.LookPath("lidza"); err == nil {
			exe = p
		}
	}
	if *cliOnly {
		return nil
	}

	// 2. The project.
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		fmt.Println("[update] not in a project; the CLI is updated")
		return nil
	}
	have, _ := moduleVersion(ctx, abs)
	if strings.HasPrefix(have, "dev") || strings.Contains(have, "=>") {
		fmt.Printf("[update] go.mod points at a local checkout (%s); module left alone\n", have)
	} else if have == target {
		fmt.Printf("[update] module already at %s\n", target)
	} else {
		fmt.Printf("[update] go get %s@%s (was %s)\n", version.ModulePath, target, have)
		if err := run(ctx, abs, "go", "get", version.ModulePath+"@"+target); err != nil {
			// The proxy may not have indexed a fresh tag yet: straight
			// from the repository, then.
			fmt.Println("[update] the module proxy does not know the release yet; fetching from the repository")
			get := exec.CommandContext(ctx, "go", "get", version.ModulePath+"@"+target)
			get.Dir = abs
			get.Env = append(os.Environ(), "GOPROXY=direct", "GONOSUMDB="+version.ModulePath)
			get.Stdout, get.Stderr = os.Stdout, os.Stderr
			if err := get.Run(); err != nil {
				return errors.New("update: go get failed")
			}
		}
		if err := run(ctx, abs, "go", "mod", "tidy"); err != nil {
			return errors.New("update: go mod tidy failed")
		}
	}
	if n, err := repin(filepath.Join(abs, "Dockerfile"), target); err == nil && n > 0 {
		fmt.Printf("[update] Dockerfile pinned to %s\n", target)
	}
	if cfg != nil {
		// The new CLI regenerates: pack schemas synced (a migration when a
		// pack's table changed), the guide's framework section, the skills.
		fmt.Println("[update] lidza gen")
		if err := run(ctx, abs, exe, "gen", "--dir", abs); err != nil {
			return errors.New("update: lidza gen failed")
		}
		// A release may rename generated Go names (lidza gen lists them):
		// say at once when the app's code has not followed.
		build := exec.CommandContext(ctx, "go", "build", "./...")
		build.Dir = abs
		if res, err := build.CombinedOutput(); err != nil {
			fmt.Printf("[update] go build ./... fails with %s; rename what lidza gen listed above (and see CHANGELOG.md):\n%s", target, res)
		}
		// The deployment files came from the templates of the release
		// that created the app; the new ones may carry a fix.
		if err := run(ctx, abs, exe, "gen", "deploy", "--dir", abs); err != nil {
			fmt.Println("[update] deployment files not refreshed:", err)
		}
		// Then everything a fresh clone or a pull needs, with the new
		// CLI: .env settings of packs enabled since, the databases
		// created and migrated (a migration dropping data waits for
		// --migrate), node_modules.
		fmt.Println("[update] lidza install")
		args := []string{"install", "--dir", abs, "--no-commit"}
		if *migrate {
			args = append(args, "--migrate")
		}
		if err := run(ctx, abs, exe, args...); err != nil {
			return errors.New("update: lidza install needs attention (above); fix it and run lidza install")
		}
	}
	if pid, v, ok := devserver.RunningDev(abs); ok && v != target {
		fmt.Printf("[update] lidza dev (pid %d) still runs %s and would regenerate with it: stop it and run lidza dev again\n", pid, v)
	}
	fmt.Printf("[update] done: %s; what changed is in CHANGELOG.md of the framework (https://github.com/agim/lidza/blob/%s/CHANGELOG.md)\n", target, target)
	if current != target {
		fmt.Println("[update] an agent session with the MCP server open still runs the old server: reconnect it (/mcp in Claude Code) or restart the agent to get the new tools")
	}
	return nil
}

// commitsBehind is how many commits the checkout at dir lacks from its
// branch's upstream, after a fetch; 0 outside git, without an upstream
// or when the fetch fails (said, not fatal: offline work goes on).
// fastForward brings a branch n commits behind upstream up to it, only
// when that cannot go wrong: nothing uncommitted (tracked files), and no
// local commit the upstream lacks, so the pull is a fast-forward with no
// merge and no conflict. Otherwise it says why and what to run.
func fastForward(ctx context.Context, dir string, n int, upstream string) error {
	git := func(args ...string) (string, error) {
		c, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		cmd := exec.CommandContext(c, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if dirty, err := git("status", "--porcelain", "--untracked-files=no"); err != nil || dirty != "" {
		return fmt.Errorf("update: this branch is %d commit(s) behind %s and has uncommitted changes; commit or stash them, then lidza update (it pulls), or git pull yourself", n, upstream)
	}
	if ahead, _ := git("rev-list", "--count", "@{u}..HEAD"); ahead != "" && ahead != "0" {
		return fmt.Errorf("update: this branch and %s have diverged (%d behind, %s ahead); git pull (merge or rebase, as the team does), then lidza update", upstream, n, ahead)
	}
	if out, err := git("merge", "--ff-only", "@{u}"); err != nil {
		return fmt.Errorf("update: git merge --ff-only %s failed: %s", upstream, out)
	}
	fmt.Printf("[update] pulled %d commit(s) from %s (fast-forward)\n", n, upstream)
	return nil
}

func commitsBehind(ctx context.Context, dir string) (int, string) {
	git := func(timeout time.Duration, args ...string) (string, error) {
		c, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(c, "git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	upstream, err := git(5*time.Second, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil || upstream == "" {
		return 0, ""
	}
	remote, _, _ := strings.Cut(upstream, "/")
	if _, err := git(30*time.Second, "fetch", "--quiet", remote); err != nil {
		fmt.Printf("[update] git fetch %s failed; not checking whether the branch is behind %s\n", remote, upstream)
		return 0, upstream
	}
	out, err := git(5*time.Second, "rev-list", "--count", "HEAD..@{u}")
	if err != nil {
		return 0, upstream
	}
	n := 0
	fmt.Sscan(out, &n)
	return n, upstream
}

// latestVersion asks the repository's tags for the newest release, and
// the module proxy when the repository is unreachable: the proxy caches
// its version list for up to half an hour, so a tag cut minutes ago is
// missing there while an explicit version is fetched on demand.
func latestVersion(ctx context.Context) (string, error) {
	var lastErr error
	for _, proxy := range []string{"direct", ""} {
		lctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		cmd := exec.CommandContext(lctx, "go", "list", "-m", "-versions", version.ModulePath+"@latest")
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
		if proxy != "" {
			cmd.Env = append(cmd.Env, "GOPROXY="+proxy, "GONOSUMDB="+version.ModulePath)
		}
		cmd.Dir = os.TempDir()
		out, err := cmd.Output()
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		if v, err := newestOf(string(out)); err == nil {
			return v, nil
		} else {
			lastErr = err
		}
	}
	return "", fmt.Errorf("update: cannot list releases of %s (network?): %v", version.ModulePath, lastErr)
}

// noDowngrade is the version to update to: the newest tag, unless the
// CLI or the app's module is already past it (a release commit not
// tagged yet), then the newest of those.
func noDowngrade(newest string, have ...string) string {
	target := newest
	for _, v := range have {
		if semver.IsValid(v) && semver.Compare(v, target) > 0 {
			target = v
		}
	}
	return target
}

// isCommit reports whether s names a commit (a hash of 7 to 40 hex
// characters, or a branch: master) rather than a release.
func isCommit(s string) bool {
	if s == "master" || s == "main" {
		return true
	}
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// commitVersion asks the repository (not the proxy, which may not have
// it yet) for the pseudo-version of a commit.
func commitVersion(ctx context.Context, commit string) (string, error) {
	lctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(lctx, "go", "list", "-m", "-f", "{{.Version}}", version.ModulePath+"@"+commit)
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=direct", "GONOSUMDB="+version.ModulePath)
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("update: commit %s of %s: %s", commit, version.ModulePath, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// newestOf picks the newest release from `go list -m -versions` output
// ("path v0.1.0 v0.1.1 ..."), ignoring pre-releases.
func newestOf(listed string) (string, error) {
	fields := strings.Fields(listed)
	var newest string
	for _, f := range fields[min(1, len(fields)):] {
		if !releaseRe.MatchString(f) {
			continue
		}
		if newest == "" || semverLess(newest, f) {
			newest = f
		}
	}
	if newest == "" {
		return "", errors.New("update: no release found")
	}
	return newest, nil
}

var releaseRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func semverLess(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	var out [3]int
	fmt.Sscanf(strings.TrimPrefix(v, "v"), "%d.%d.%d", &out[0], &out[1], &out[2])
	return out
}

// moduleVersion is the framework version the project's go.mod requires,
// with "dev (path)" for a replace.
func moduleVersion(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Version}}{{if .Replace}} => {{.Replace.Path}}{{end}}", version.ModulePath)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// repin rewrites the CLI version in a Dockerfile's go install line.
func repin(path, target string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	re := regexp.MustCompile(regexp.QuoteMeta(version.ModulePath+"/cmd/lidza@") + `[^\s]+`)
	next := re.ReplaceAll(data, []byte(version.ModulePath+"/cmd/lidza@"+target))
	if bytes.Equal(next, data) {
		return 0, nil
	}
	return 1, os.WriteFile(path, next, 0o644)
}
