package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// runShip is `lidza ship [--no-e2e] [--out bin/<name>]`: everything that
// must be true before a deploy, in order: lidza verify (generated files
// staged, check, Go tests), the browser suite against the built binary,
// the production build. It stops at the first failure.
func runShip(ctx context.Context, args []string) error {
	fs := flags("ship")
	dir := fs.String("dir", ".", "project directory")
	noE2E := fs.Bool("no-e2e", false, "skip the browser suite")
	out := fs.String("out", "", "output binary (default bin/<name>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("ship needs a lidza.json project")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	stage := func(name string, args ...string) error {
		fmt.Printf("[ship] %s\n", name)
		if err := run(ctx, abs, exe, args...); err != nil {
			return fmt.Errorf("ship: %s failed", name)
		}
		return nil
	}
	if err := stage("lidza verify", "verify", "--dir", abs); err != nil {
		return err
	}
	if !*noE2E {
		if _, err := os.Stat(filepath.Join(abs, "playwright.config.ts")); err == nil {
			if err := stage("lidza test --e2e --install", "test", "--dir", abs, "--e2e", "--install"); err != nil {
				return err
			}
		} else {
			fmt.Println("[ship] no browser suite in this template (pages are tested in Go)")
		}
	}
	buildArgs := []string{"build", "--dir", abs}
	if *out != "" {
		buildArgs = append(buildArgs, "--out", *out)
	}
	if err := stage("lidza build", buildArgs...); err != nil {
		return err
	}
	bin := *out
	if bin == "" {
		bin = filepath.Join("bin", cfg.Name)
	}
	info, err := os.Stat(filepath.Join(abs, bin))
	if err != nil {
		return err
	}
	fmt.Printf("[ship] ready: %s (%.1f MB). Deploy it with deploy/%s.service or the Dockerfile; docs/deploy.md has the rest.\n", bin, float64(info.Size())/(1<<20), cfg.Name)
	return nil
}
