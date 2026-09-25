package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/diag"
	"github.com/agim/lidza/pkg/inspect"
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
	"github.com/agim/lidza/pkg/sdk"
)

func runGen(_ context.Context, args []string) error {
	fs := flags("gen")
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	return generateAll(abs, cfg, os.Stdout)
}

// generateAll runs the schema generators, the pack wrappers and builds,
// then the handler-derived outputs.
func generateAll(dir string, cfg *config.Config, out io.Writer) error {
	if err := generateSchema(dir, out); err != nil {
		return err
	}
	if err := generatePacks(context.Background(), dir, cfg, out); err != nil {
		return err
	}
	return generateClient(dir, cfg, out)
}

// generatePacks writes packs.go and each enabled pack's wrapper and
// schema.rs, then builds the modules that are missing or stale.
func generatePacks(ctx context.Context, dir string, cfg *config.Config, out io.Writer) error {
	if cfg == nil {
		return nil
	}
	module := inspect.ModulePath(dir)
	if module == "" {
		return nil
	}
	s, err := schema.Load(dir)
	if err != nil {
		return err
	}
	changed, err := pack.Generate(dir, module, cfg.Packs, s)
	if err != nil {
		return fmt.Errorf("packs: %w", err)
	}
	if len(changed) > 0 {
		fmt.Fprintf(out, "[lidza] packs: wrote %s\n", strings.Join(changed, ", "))
		// A new pack imports the engine; the app's go.sum must learn its
		// dependencies.
		tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
		tidy.Dir = dir
		if res, err := tidy.CombinedOutput(); err != nil {
			fmt.Fprintf(out, "[lidza] go mod tidy: %v\n%s", err, res)
		}
	}
	if err := pack.BuildStale(ctx, dir, cfg.Packs, out); err != nil {
		return err
	}
	return generateQueries(ctx, dir, out)
}

// generateQueries runs sqlc when the project has sqlc.yaml and a schema.
func generateQueries(ctx context.Context, dir string, out io.Writer) error {
	if _, err := os.Stat(filepath.Join(dir, pack.SQLCFile)); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, schema.SQLFile)); err != nil {
		return nil
	}
	if _, err := exec.LookPath("sqlc"); err != nil {
		fmt.Fprintln(out, "[lidza] sqlc is not installed; db/queries not generated (run install.sh)")
		return nil
	}
	cmd := exec.CommandContext(ctx, "sqlc", "generate")
	cmd.Dir = dir
	if res, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlc generate: %v\n%s", err, res)
	}
	return nil
}

// generateSchema turns schema.lidza into the Go package, the SQL schema, a
// migration when the models changed, and the Rust module when the project
// has a crate. A project without schema.lidza is left alone.
func generateSchema(dir string, out io.Writer) error {
	s, err := schema.Load(dir)
	if err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	cargoDir := diag.Detect(dir).CargoDir
	if cargoDir == "." {
		cargoDir = ""
	}
	res, err := schema.Generate(dir, s, cargoDir, "")
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if len(res.Files) > 0 {
		fmt.Fprintf(out, "[lidza] %s\n", res.Describe())
	}
	return nil
}

// generateClient refreshes the context files (context.json, openapi.json,
// llms) and writes the TypeScript client for projects with a frontend
// build. Type-check warnings are printed; the client covers what resolved.
func generateClient(dir string, cfg *config.Config, out io.Writer) error {
	c, err := inspect.Refresh(dir, cfg)
	if err != nil {
		return fmt.Errorf("context: %w", err)
	}
	for _, w := range c.Warnings {
		fmt.Fprintf(out, "[lidza] type-check: %s\n", w)
	}
	if cfg == nil || cfg.Frontend.Dist == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return nil
	}
	if err := sdk.WriteTypeScript(dir, c); err != nil {
		return fmt.Errorf("client: %w", err)
	}
	return nil
}
