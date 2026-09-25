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
	"github.com/agim/lidza/pkg/scaffold"
	"github.com/agim/lidza/pkg/schema"
)

func runNew(ctx context.Context, args []string) error {
	fs := flags("new")
	template := fs.String("template", "react", "template name")
	lidzaDir := fs.String("lidza-dir", os.Getenv("LIDZA_DIR"), "local checkout of the framework to use instead of the published module (env LIDZA_DIR)")
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
	fmt.Printf("\nnext:\n  cd %s\n  lidza dev\n", name)
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
	return devserver.Dev(ctx, cfg, devserver.Options{
		Addr:        *addr,
		Out:         out,
		Watch:       []string{schema.FileName},
		BeforeBuild: func() error { return generateSchema(cfg.Dir, out) },
		AfterBuild: func() {
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
