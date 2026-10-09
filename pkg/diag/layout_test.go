package diag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
)

func TestRootLayout(t *testing.T) {
	for _, appDir := range []string{"", "app", "internal/app"} {
		t.Run("appDir="+appDir, func(t *testing.T) {
			dir := t.TempDir()
			cfg := config.Default("demo", "react")
			cfg.AppDir = appDir
			if err := cfg.Save(dir); err != nil {
				t.Fatal(err)
			}
			write := func(name string) {
				t.Helper()
				p := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("not valid source"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"main.go", "go.mod", "go.sum", "schema.lidza", "README.md", "AGENTS.md", "CLAUDE.md", "GEMINI.md", ".env.test.local", ".gitignore", ".mcp.json", "Dockerfile", "package-lock.json", "playwright.config.ts", "vitest.config.ts", "eslint.config.mjs", "tsconfig.app.json", "sqlc.yaml", "tests/api_test.go", "internal/orders/orders_test.go", "handlers/orders_test.go", "e2e/home.spec.ts", "docs/notes.md", "scripts/seed.sh", ".lidza/dump.sql", "bin/demo", "node_modules/stray_test.go"} {
				write(name)
			}
			// A linked directory (a worktree's node_modules) is a directory.
			shared := filepath.Join(t.TempDir(), "node_modules")
			os.MkdirAll(shared, 0o755)
			if err := os.Symlink(shared, filepath.Join(dir, "vendor-link")); err != nil {
				t.Fatal(err)
			}
			if got := rootLayout(dir); len(got) != 0 {
				t.Fatalf("valid layout: %+v", got)
			}
			bad := []string{"routes_test.go", "home.spec.ts", "page.test.tsx", "page.test.js", "calendar.go", "scratch.md", "dump.sql", ".hidden.tmp", ".env.test.bak", "app.log", "demo", "dangling"}
			for _, name := range bad[:len(bad)-1] {
				write(name)
			}
			// A link to nothing is a stray file, not a directory.
			os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "dangling"))
			// Files remain findings even when git would ignore them.
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.sql\n*.log\n*_test.go\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got := rootLayout(dir)
			if len(got) != len(bad) {
				t.Fatalf("bad root files: %+v", got)
			}
			seen := map[string]bool{}
			for _, d := range got {
				if d.Code != "L020" || d.Severity != "error" || d.Line != 1 {
					t.Fatalf("diagnostic: %+v", d)
				}
				seen[d.File] = true
				if d.File == "routes_test.go" && appDir == "" && !strings.Contains(d.Message, "cannot import package main") {
					t.Fatalf("missing migration advice: %+v", d)
				}
			}
			for _, name := range bad {
				if !seen[name] {
					t.Errorf("missing %s", name)
				}
			}
			write("routes.go")
			want := len(bad)
			if appDir != "" {
				want++
			}
			if got := rootLayout(dir); len(got) != want {
				t.Fatalf("wiring: %+v", got)
			}
		})
	}
}

func TestRootLayoutRequiresApp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "runtime_test.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := rootLayout(dir); len(got) != 0 {
		t.Fatalf("framework/library: %+v", got)
	}
}

func TestRootLayoutFailsReport(t *testing.T) {
	dir := t.TempDir()
	if err := config.Default("demo", "htmx").Save(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "api_test.go"} {
		data := "package main\n"
		if name == "go.mod" {
			data = "module demo\ngo 1.27\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := Run(context.Background(), Detect(dir))
	if r.Status != "error" || r.Errors() == 0 {
		t.Fatalf("root test did not fail report: %+v", r)
	}
	for _, d := range r.Diagnostics {
		if d.Code == "L020" && d.File == "api_test.go" {
			return
		}
	}
	t.Fatalf("missing L020: %+v", r)
}
