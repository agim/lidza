package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agim/lidza/pkg/config"
)

// llmsFile is where `lidza gen llms` writes the app's public llms.txt:
// the frontend's public directory, copied to the build's root (served at
// /llms.txt), or the htmx template's static directory, which its pages
// serve at /llms.txt.
func llmsFile(abs string) string {
	if cfg, err := config.Load(abs); err == nil && cfg.Frontend.Template == "htmx" {
		return filepath.Join(abs, cfg.AppPath("static"), "llms.txt")
	}
	if fileExists(filepath.Join(abs, "static", "htmx.min.js")) {
		return filepath.Join(abs, "static", "llms.txt")
	}
	return filepath.Join(abs, "public", "llms.txt")
}

var titleTag = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// runGenLLMS is `lidza gen llms`: a starting llms.txt (llmstxt.org) for
// the public site: the app's name as the H1, a summary to write, and a
// link per page the last build prerendered, by its title. It is the
// app's own public content: what a visiting agent may read, never the
// development guides, handlers or configuration.
func runGenLLMS(_ context.Context, args []string) error {
	fs := flags("gen llms")
	dir := fs.String("dir", ".", "project directory")
	force := fs.Bool("force", false, "overwrite an existing llms.txt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("gen llms needs a lidza.json project")
	}
	path := llmsFile(abs)
	if fileExists(path) && !*force {
		return fmt.Errorf("%s exists: edit it, or pass --force to start again", rel(abs, path))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", cfg.Name)
	b.WriteString("> One or two sentences: what this site is, who it is for, and what a visitor can do here.\n\n")
	b.WriteString("Write what an agent needs to use the site for a person: the main tasks, where they start,\nand anything to know first (sign-in, regions, prices). Keep internal APIs, handlers and\nconfiguration out: this file is public.\n\n")
	b.WriteString("## Pages\n\n")
	pages := 0
	if cfg.Frontend.Dist != "" {
		dist := filepath.Join(abs, cfg.Frontend.Dist)
		for _, route := range prerenderedRoutes(dist) {
			file := filepath.Join(dist, filepath.FromSlash(strings.TrimPrefix(route, "/")), "index.html")
			title := route
			if data, err := os.ReadFile(file); err == nil {
				if m := titleTag.FindSubmatch(data); m != nil {
					if t := strings.TrimSpace(html.UnescapeString(string(m[1]))); t != "" {
						title = t
					}
				}
			}
			fmt.Fprintf(&b, "- [%s](%s): what this page is for\n", title, route)
			pages++
		}
	}
	if pages == 0 {
		b.WriteString("- [Home](/): what a visitor finds first\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: fill in the summary and each page's line; served at /llms.txt after the next build\n", rel(abs, path))
	if cfg.Frontend.Dist != "" && pages == 0 {
		fmt.Println("(no build yet: run lidza build, then lidza gen llms --force to list the prerendered pages)")
	}
	return nil
}

func rel(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil {
		return r
	}
	return path
}
