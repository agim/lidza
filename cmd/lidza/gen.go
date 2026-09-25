package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/diag"
	"github.com/agim/lidza/pkg/inspect"
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

// generateAll runs the schema generators, then the handler-derived outputs.
func generateAll(dir string, cfg *config.Config, out io.Writer) error {
	if err := generateSchema(dir, out); err != nil {
		return err
	}
	return generateClient(dir, cfg, out)
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
