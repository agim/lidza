// Package config reads lidza.json, the per-project configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the project configuration file, at the project root.
const FileName = "lidza.json"

// Config is the parsed lidza.json.
type Config struct {
	// Name is the application name; also the output binary name.
	Name     string   `json:"name"`
	Frontend Frontend `json:"frontend"`

	// Dir is the project root the file was read from. Not serialized.
	Dir string `json:"-"`
}

// Frontend is the "frontend" block: which template the app uses and how the
// dev proxy reaches its dev server.
type Frontend struct {
	// Template is the template name: react, svelte, astro or htmx.
	Template string `json:"template"`
	// Dev is the shell command that starts the frontend dev server. Empty
	// for templates without one (htmx).
	Dev string `json:"dev,omitempty"`
	// URL is where the dev server listens; `lidza dev` proxies to it.
	URL string `json:"url,omitempty"`
	// Dist is the build output directory, relative to the project root,
	// embedded into the production binary.
	Dist string `json:"dist,omitempty"`
	// Watch lists directories, relative to the project root, whose files
	// `lidza dev` rebuilds on besides the Go sources: views and static
	// files a Go-rendered frontend embeds.
	Watch []string `json:"watch,omitempty"`
}

// HasDevServer reports whether the template runs a separate dev server that
// `lidza dev` must start and proxy to.
func (f Frontend) HasDevServer() bool { return f.Dev != "" && f.URL != "" }

// Default returns the configuration `lidza new` writes for a template.
func Default(name, template string) Config {
	c := Config{Name: name, Frontend: Frontend{Template: template}}
	switch template {
	case "htmx":
		c.Frontend.Watch = []string{"views", "static"}
	default:
		c.Frontend.Dev = "npm run dev -- --port 5173"
		c.Frontend.URL = "http://127.0.0.1:5173"
		c.Frontend.Dist = "dist"
	}
	return c
}

// Load reads lidza.json from dir.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s not found in %s (not a Līdza project?)", FileName, dir)
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	c.Dir = abs
	return &c, nil
}

// Save writes the configuration to dir/lidza.json.
func (c Config) Save(dir string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), append(data, '\n'), 0o644)
}

func (c Config) validate() error {
	if c.Name == "" {
		return errors.New(`"name" is required`)
	}
	if c.Frontend.Template == "" {
		return errors.New(`"frontend.template" is required`)
	}
	f := c.Frontend
	if (f.Dev == "") != (f.URL == "") {
		return errors.New(`"frontend.dev" and "frontend.url" must be set together`)
	}
	if f.URL != "" && !strings.HasPrefix(f.URL, "http://") && !strings.HasPrefix(f.URL, "https://") {
		return fmt.Errorf(`"frontend.url" must start with http:// or https://, got %q`, f.URL)
	}
	if f.Dist != "" && (filepath.IsAbs(f.Dist) || strings.HasPrefix(f.Dist, "..")) {
		return fmt.Errorf(`"frontend.dist" must be a relative path inside the project, got %q`, f.Dist)
	}
	return nil
}
