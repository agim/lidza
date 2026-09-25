package inspect

import (
	"fmt"
	"strings"

	"github.com/agim/lidza/pkg/config"
)

// LLMS renders /llms.txt (short: what the app is, its routes, its commands)
// and /llms-full.txt (the guide, every handler signature, the Rust exports
// and the configuration). guide is the app's docs/lidza-guide.md, may be
// empty.
func LLMS(c *Context, cfg *config.Config, guide string) (short, full string) {
	var s strings.Builder
	fmt.Fprintf(&s, "# %s\n\n", c.App.Name)
	fmt.Fprintf(&s, "> Līdza application: Go control plane serving /api, %s frontend for every other path, one binary. Dev server: http://127.0.0.1:3000.\n\n", templateName(cfg))
	s.WriteString("## API routes\n\n")
	for _, r := range c.Routes {
		s.WriteString("- " + routeLine(r) + "\n")
	}
	s.WriteString("\n## Commands\n\n")
	s.WriteString("- `lidza check --json`: one JSON list of Go, Rust and frontend diagnostics with file and line\n")
	s.WriteString("- `lidza context`: this project as JSON in .lidza/context.json\n")
	s.WriteString("- `lidza mcp`: MCP server with the same data plus runtime logs\n")
	s.WriteString("- `lidza dev`, `lidza build`\n")
	s.WriteString("\n## Files\n\n")
	s.WriteString("- routes.go: API handlers (add routes here)\n")
	s.WriteString("- docs/lidza-guide.md: the rules; also at /llms-full.txt\n")
	if c.Rust != nil {
		fmt.Fprintf(&s, "- %s: Rust crate %s, %d C-ABI export(s)\n", c.Rust.Dir, c.Rust.Crate, len(c.Rust.Exports))
	}
	s.WriteString("\n## More\n\n- /llms-full.txt: guide, handler signatures, Rust exports, configuration\n")

	var f strings.Builder
	if guide != "" {
		f.WriteString(strings.TrimSpace(guide) + "\n\n")
	} else {
		fmt.Fprintf(&f, "# %s\n\n", c.App.Name)
	}
	f.WriteString("## API routes with handlers\n\n")
	for _, r := range c.Routes {
		f.WriteString("- " + routeLine(r) + "\n")
		if r.Handler.Signature != "" {
			fmt.Fprintf(&f, "  - handler: `%s`", r.Handler.Signature)
			if r.Handler.File != "" {
				fmt.Fprintf(&f, " at %s:%d", r.Handler.File, r.Handler.Line)
			}
			f.WriteString("\n")
		}
	}
	if c.Rust != nil {
		fmt.Fprintf(&f, "\n## Rust crate %s (%s)\n\n", c.Rust.Crate, c.Rust.Dir)
		if len(c.Rust.Exports) == 0 {
			f.WriteString("No C-ABI exports.\n")
		}
		for _, e := range c.Rust.Exports {
			fmt.Fprintf(&f, "- `%s` at %s:%d\n", e.Signature, e.File, e.Line)
		}
	}
	if cfg != nil {
		f.WriteString("\n## lidza.json\n\n")
		fmt.Fprintf(&f, "- name: %s\n- template: %s\n", cfg.Name, cfg.Frontend.Template)
		if cfg.Frontend.HasDevServer() {
			fmt.Fprintf(&f, "- frontend dev server: `%s` at %s\n- frontend build output: %s/\n", cfg.Frontend.Dev, cfg.Frontend.URL, cfg.Frontend.Dist)
		}
	}
	return s.String(), f.String()
}

func routeLine(r Route) string {
	line := r.Pattern
	if r.Method == "" {
		line = "ANY " + r.Path
	}
	switch {
	case r.Builtin:
		line += " (built in)"
	case r.Handler.Name == "func literal":
		line += fmt.Sprintf(": %s:%d", r.Handler.File, r.Handler.Line)
	default:
		line += ": " + r.Handler.Name
		if r.Handler.File != "" {
			line += fmt.Sprintf(" (%s:%d)", r.Handler.File, r.Handler.Line)
		}
	}
	return line
}

func templateName(cfg *config.Config) string {
	if cfg == nil || cfg.Frontend.Template == "" {
		return "no"
	}
	return cfg.Frontend.Template
}
