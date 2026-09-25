package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/diag"
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/recipes"
	"github.com/agim/lidza/pkg/schema"
)

// HookDir is the git hooks directory `lidza new` writes and points
// core.hooksPath at.
const HookDir = ".githooks"

// hookScript is .githooks/pre-commit.
const hookScript = `#!/bin/sh
# Written by lidza new. Runs lidza verify before every commit: generated
# files current and staged, lidza check clean, tests passing. Skip once
# with git commit --no-verify.
exec lidza verify
`

// verifyStep is one stage of `lidza verify`.
type verifyStep struct {
	Name string `json:"name"`
	// Status is "ok", "failed" or "skipped".
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type verifyReport struct {
	Status      string            `json:"status"`
	Steps       []verifyStep      `json:"steps"`
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
}

// runVerify is the one command before a commit: regenerate and require
// the generated files to be staged, run `lidza check`, run the Go tests.
// The pre-commit hook runs it; `--install-hook` sets the hook up on an
// existing project.
func runVerify(ctx context.Context, args []string) error {
	fs := flags("verify")
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON report instead of text")
	noTest := fs.Bool("no-test", false, "skip the Go tests")
	installHook := fs.Bool("install-hook", false, "write .githooks/pre-commit and point git at it, then exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if *installHook {
		return installGitHook(ctx, abs, os.Stdout, true)
	}
	out := io.Writer(os.Stdout)
	if *asJSON {
		out = io.Discard
	}
	rep := verifyReport{Status: "ok"}
	step := func(name string, run func() (string, error)) bool {
		start := time.Now()
		reason, err := run()
		s := verifyStep{Name: name, Status: "ok", Reason: reason, DurationMS: time.Since(start).Milliseconds()}
		switch {
		case errors.Is(err, errSkipped):
			s.Status = "skipped"
		case err != nil:
			s.Status = "failed"
			s.Reason = err.Error()
			rep.Status = "failed"
		}
		rep.Steps = append(rep.Steps, s)
		fmt.Fprintf(out, "[%s] %s", s.Status, name)
		if s.Reason != "" {
			fmt.Fprintf(out, ": %s", s.Reason)
		}
		fmt.Fprintf(out, " (%dms)\n", s.DurationMS)
		return s.Status != "failed"
	}

	step("generate", func() (string, error) {
		if cfg == nil {
			return "plain Go module", errSkipped
		}
		return "", generateAll(abs, cfg, io.Discard)
	})
	step("generated files staged", func() (string, error) {
		return staleGenerated(ctx, abs, cfg)
	})
	os.Setenv("LIDZA_MODE", "test")
	checkOK := step("check", func() (string, error) {
		layers := diag.Detect(abs)
		r := diag.Run(ctx, layers)
		if cfg != nil {
			r.Diagnostics = append(r.Diagnostics, packDiagnostics(ctx, abs, cfg)...)
		}
		rep.Diagnostics = r.Diagnostics
		if !*asJSON {
			printReport(r)
		}
		if r.Errors() > 0 {
			return "", fmt.Errorf("%d error(s)", r.Errors())
		}
		return fmt.Sprintf("%d warning(s)", len(r.Diagnostics)), nil
	})
	step("test", func() (string, error) {
		if *noTest {
			return "--no-test", errSkipped
		}
		if !checkOK {
			return "check failed", errSkipped
		}
		return "", goTest(ctx, abs, cfg, nil, out)
	})

	if *asJSON {
		if rep.Diagnostics == nil {
			rep.Diagnostics = []diag.Diagnostic{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "%s\n", rep.Status)
	}
	if rep.Status != "ok" {
		return errCheckFailed
	}
	return nil
}

var errSkipped = errors.New("skipped")

// generatedPaths are the tracked outputs of lidza gen: a commit that
// changes their inputs without them is incomplete.
func generatedPaths(cfg *config.Config) []string {
	paths := append([]string{filepath.Dir(schema.GoFile), schema.SQLFile, schema.MigrationsDir, schema.LockFile, pack.PacksFile, pack.Dir}, recipes.OutputDirs...)
	if cfg != nil && cfg.SDK.Dart != "" {
		paths = append(paths, cfg.SDK.Dart)
	}
	return paths
}

// staleGenerated reports tracked generated files that differ from the git
// index after regeneration: the commit in progress would miss them.
func staleGenerated(ctx context.Context, dir string, cfg *config.Config) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "git not installed", errSkipped
	}
	top := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	top.Dir = dir
	if _, err := top.Output(); err != nil {
		return "not a git repository", errSkipped
	}
	args := append([]string{"diff", "--name-only", "--"}, generatedPaths(cfg)...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	outb, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(string(outb)), "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	if len(files) == 0 {
		return "", nil
	}
	sort.Strings(files)
	return "", fmt.Errorf("regenerated files are not staged: git add %s", strings.Join(files, " "))
}

// goTest prepares the test database when the db pack is enabled and runs
// `go test ./...` with extra arguments.
func goTest(ctx context.Context, dir string, cfg *config.Config, extra []string, out io.Writer) error {
	diag.IsolateNodeModules(dir)
	if cfg != nil {
		for _, p := range cfg.Packs {
			if p == pack.OfficialPrefix+"db" {
				if err := prepareTestDB(ctx, dir); err != nil {
					return err
				}
			}
		}
	}
	fmt.Fprintln(out, "[lidza] go test ./...")
	cmd := exec.CommandContext(ctx, "go", append([]string{"test", "./..."}, extra...)...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return errors.New("go tests failed")
	}
	return nil
}

// installGitHook writes .githooks/pre-commit and sets core.hooksPath for
// the repository rooted at dir. With init, a directory outside any
// repository becomes one first.
func installGitHook(ctx context.Context, dir string, out io.Writer, init bool) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed")
	}
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		b, err := cmd.Output()
		return strings.TrimSpace(string(b)), err
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		if !init {
			return errors.New("not a git repository")
		}
		if _, err := git("init", "-q"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
		top = dir
	}
	if topAbs, _ := filepath.EvalSymlinks(top); topAbs != "" {
		if dirAbs, _ := filepath.EvalSymlinks(dir); dirAbs != topAbs {
			return fmt.Errorf("the repository root is %s, not the project; add `cd %s && lidza verify` to its pre-commit hook instead", top, dir)
		}
	}
	hook := filepath.Join(dir, HookDir, "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(hook); err != nil {
		if err := os.WriteFile(hook, []byte(hookScript), 0o755); err != nil {
			return err
		}
	}
	if _, err := git("config", "core.hooksPath", HookDir); err != nil {
		return fmt.Errorf("git config core.hooksPath: %w", err)
	}
	fmt.Fprintf(out, "pre-commit hook: %s (git config core.hooksPath %s)\n", filepath.ToSlash(filepath.Join(HookDir, "pre-commit")), HookDir)
	return nil
}
