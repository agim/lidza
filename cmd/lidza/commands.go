package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/mcpserver"
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/scaffold"
	"github.com/agim/lidza/pkg/schema"
	"github.com/agim/lidza/pkg/version"
)

func runNew(ctx context.Context, args []string) error {
	fs := flags("new")
	template := fs.String("template", "react", "template name")
	lidzaDir := fs.String("lidza-dir", os.Getenv("LIDZA_DIR"), "local checkout of the framework to use instead of the published module (env LIDZA_DIR)")
	packs := fs.String("packs", "", "official packs to enable, comma-separated (db is added when a pack needs it); runs lidza setup")
	agent := fs.String("agent", "", "agent CLI to install when missing: claude, codex or gemini")
	dbURL := fs.String("database-url", os.Getenv("LIDZA_DATABASE_URL"), "development DATABASE_URL for setup (default: a local socket database named after the app)")
	noSetup := fs.Bool("no-setup", false, "scaffold only: no .env, databases, node_modules or first commit")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: lidza new <name> [flags]")
		fs.PrintDefaults()
	}
	// Accept `lidza new demo --template x` as well as `lidza new --template x demo`:
	// the flag package stops at the first positional argument.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	} else if fs.NArg() != 0 {
		fs.Usage()
		return errors.New("new: exactly one app name expected")
	}
	if name == "" {
		fs.Usage()
		return errors.New("new: app name required")
	}
	err := scaffold.New(ctx, scaffold.Options{
		Name:     name,
		Template: *template,
		LidzaDir: *lidzaDir,
		Out:      os.Stdout,
	})
	if err != nil {
		return err
	}
	// A repository with the pre-commit hook, unless the app is created
	// inside an existing one.
	abs, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	if err := installGitHook(ctx, abs, os.Stdout, true); err != nil {
		fmt.Printf("pre-commit hook not installed: %v\n", err)
	}
	if *noSetup {
		fmt.Printf("\nnext:\n  cd %s\n  lidza setup --packs db,auth   # .env, databases, node_modules, first commit\n  lidza dev\n", name)
		return nil
	}
	cfg, err := config.Load(abs)
	if err != nil {
		return err
	}
	fmt.Println()
	if err := setup(ctx, abs, cfg, setupOptions{Packs: splitList(*packs), Agent: *agent, DatabaseURL: *dbURL, Commit: true, Out: os.Stdout, Interview: isTerminal(os.Stdin), In: os.Stdin}); err != nil {
		return err
	}
	fmt.Printf("  (in %s)\n", name)
	return nil
}

func runDev(ctx context.Context, args []string) error {
	fs := flags("dev")
	dir := fs.String("dir", ".", "project directory")
	addr := fs.String("addr", "127.0.0.1:3000", "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}
	// Everything printed also goes to .lidza/dev.log for `lidza mcp`.
	if err := os.MkdirAll(filepath.Join(cfg.Dir, devserver.BuildDir), 0o755); err != nil {
		return err
	}
	logFile, err := os.Create(filepath.Join(cfg.Dir, mcpserver.LogFile))
	if err != nil {
		return err
	}
	defer logFile.Close()
	out := io.MultiWriter(os.Stdout, logFile)
	// Recorded for lidza update, which names a dev server still on an
	// older CLI.
	if err := devserver.WritePID(cfg.Dir, version.String()); err == nil {
		defer devserver.RemovePID(cfg.Dir)
	}
	// An update replaces this CLI on disk: from then on this process would
	// regenerate with the old release's generators and undo the new ones,
	// so it stops generating and says to restart.
	exe, _ := os.Executable()
	exeStamp := fileStamp(exe)
	stale := false
	cliChanged := func() bool {
		if stale || exe == "" || fileStamp(exe) == exeStamp {
			return stale
		}
		stale = true
		fmt.Fprintf(out, "[lidza] the lidza CLI changed on disk (lidza update?); this dev server is %s and stops generating: stop it and run lidza dev again\n", version.String())
		return true
	}
	// lidza.json is read again before every build: a pack added or
	// scaffolded while dev runs is generated into packs.go and watched.
	watch := func() []string {
		return append(append([]string{schema.FileName, config.FileName}, cfg.Frontend.Watch...), packWatch(cfg)...)
	}
	return devserver.Dev(ctx, cfg, devserver.Options{
		Addr:    *addr,
		Out:     out,
		Watch:   watch(),
		Rewatch: watch,
		BeforeBuild: func() error {
			if cliChanged() {
				return nil
			}
			if fresh, err := config.Load(cfg.Dir); err != nil {
				fmt.Fprintf(out, "[lidza] lidza.json: %v (keeping the previous one)\n", err)
			} else {
				cfg = fresh
			}
			if err := generateSchema(cfg.Dir, out); err != nil {
				return err
			}
			return generatePacks(ctx, cfg.Dir, cfg, out)
		},
		AfterBuild: func() {
			if cliChanged() {
				return
			}
			if err := generateClient(cfg.Dir, cfg, out); err != nil {
				fmt.Fprintf(out, "[lidza] %v\n", err)
			}
		},
	})
}

func runBuild(ctx context.Context, args []string) error {
	fs := flags("build")
	dir := fs.String("dir", ".", "project directory")
	out := fs.String("out", "", "output binary (default bin/<name>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Join("bin", cfg.Name)
	}
	if !filepath.IsAbs(*out) {
		*out = filepath.Join(cfg.Dir, *out)
	}

	if err := generateAll(cfg.Dir, cfg, os.Stdout); err != nil {
		return err
	}
	if cfg.Frontend.Dist != "" {
		if err := devserver.EnsureNodeModules(ctx, cfg.Dir, os.Stdout); err != nil {
			return err
		}
		fmt.Println("[lidza] npm run build")
		if err := run(ctx, cfg.Dir, "npm", "run", "build"); err != nil {
			return fmt.Errorf("frontend build failed: %w", err)
		}
		if err := devserver.KeepDist(cfg.Dir, cfg.Frontend.Dist); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(cfg.Dir, cfg.Frontend.Dist, "index.html")); err != nil {
			return fmt.Errorf("frontend build produced no %s/index.html", cfg.Frontend.Dist)
		}
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	fmt.Println("[lidza] go build")
	if err := run(ctx, cfg.Dir, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", *out, "."); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}
	info, err := os.Stat(*out)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(cfg.Dir, *out)
	if rel == "" || strings.HasPrefix(rel, "..") {
		rel = *out
	}
	fmt.Printf("[lidza] built %s (%.1f MB)\n", rel, float64(info.Size())/1e6)
	return nil
}

func run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// packWatch lists the enabled packs' crates and manifests, so a Rust edit
// rebuilds the module and the app.
func packWatch(cfg *config.Config) []string {
	var out []string
	for _, name := range cfg.Packs {
		out = append(out, filepath.Join(pack.Dir, name, pack.ManifestFile), filepath.Join(pack.Dir, name, "rust", "src"), filepath.Join(pack.Dir, name, "rust", "Cargo.toml"))
	}
	return out
}

// fileStamp identifies a file's version by size and modification time;
// "" when it cannot be read.
func fileStamp(p string) string {
	info, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d %d", info.Size(), info.ModTime().UnixNano())
}
