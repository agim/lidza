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

	"github.com/agim/lidza/pkg/version"
)

// runUpdate is `lidza update [--to vX.Y.Z] [--cli-only] [--migrate]`: the
// CLI to the newest release (or the one named), and, in a project, the
// framework module to the same version, go.mod tidied, the Dockerfile's
// pin rewritten, everything regenerated with the new CLI, and a note when
// migrations wait (--migrate applies them).
func runUpdate(ctx context.Context, args []string) error {
	fs := flags("update")
	dir := fs.String("dir", ".", "project directory")
	to := fs.String("to", "", "the release to move to (default: the newest)")
	cliOnly := fs.Bool("cli-only", false, "the CLI only; leave the project alone")
	migrate := fs.Bool("migrate", false, "apply the migrations the update brings")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := *to
	if target == "" {
		v, err := latestVersion(ctx)
		if err != nil {
			return err
		}
		target = v
	}
	if !strings.HasPrefix(target, "v") {
		target = "v" + target
	}
	current := version.String()
	fmt.Printf("[update] CLI %s, newest release %s\n", current, target)

	// 1. The CLI.
	exe, _ := os.Executable()
	if current == target {
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
		pending, err := pendingMigrations(ctx, abs, exe)
		switch {
		case err != nil:
			fmt.Printf("[update] migrations: %v\n", err)
		case pending > 0 && *migrate:
			fmt.Printf("[update] lidza db migrate (%d pending)\n", pending)
			if err := run(ctx, abs, exe, "db", "migrate", "--dir", abs); err != nil {
				return errors.New("update: lidza db migrate failed")
			}
		case pending > 0:
			fmt.Printf("[update] %d migration(s) wait: lidza db migrate (or lidza update --migrate); lidza test applies them to the test database itself\n", pending)
		}
	}
	fmt.Printf("[update] done: %s; what changed is in CHANGELOG.md of the framework (https://github.com/agim/lidza/blob/%s/CHANGELOG.md)\n", target, target)
	if current != target {
		fmt.Println("[update] an agent session with the MCP server open still runs the old server: reconnect it (/mcp in Claude Code) or restart the agent to get the new tools")
	}
	return nil
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

// pendingMigrations counts what `lidza db status` reports as pending.
func pendingMigrations(ctx context.Context, dir, exe string) (int, error) {
	cmd := exec.CommandContext(ctx, exe, "db", "status", "--dir", dir)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("lidza db status: %s", strings.TrimSpace(string(out)))
	}
	return strings.Count(string(out), "pending"), nil
}
