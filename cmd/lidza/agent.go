package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/inspect"
	"github.com/agim/lidza/pkg/mcpserver"
)

// loadProject reads lidza.json when there is one; a plain Go module (the
// framework itself, for instance) is inspectable too and yields a nil config.
func loadProject(dir string) (string, *config.Config, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, config.FileName)); err == nil {
		cfg, err := config.Load(abs)
		return abs, cfg, err
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		return "", nil, fmt.Errorf("%s: neither %s nor go.mod found", abs, config.FileName)
	}
	return abs, nil, nil
}

func runContext(_ context.Context, args []string) error {
	fs := flags("context")
	dir := fs.String("dir", ".", "project directory")
	stdout := fs.Bool("stdout", false, "print the JSON instead of writing "+inspect.FileName)
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if *stdout {
		c, err := inspect.Project(abs, cfg)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(c)
	}
	if err := inspect.Refresh(abs, cfg); err != nil {
		return err
	}
	c, err := inspect.Project(abs, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d route(s)", inspect.FileName, len(c.Routes))
	if c.Rust != nil {
		fmt.Printf(", %d Rust export(s)", len(c.Rust.Exports))
	}
	fmt.Println()
	return nil
}

func runMCP(_ context.Context, args []string) error {
	fs := flags("mcp")
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	// stdout carries JSON-RPC; anything for a human goes to stderr.
	fmt.Fprintf(os.Stderr, "lidza mcp: serving %s on stdio\n", abs)
	return mcpserver.Serve(abs, cfg)
}
