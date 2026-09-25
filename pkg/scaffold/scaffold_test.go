package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
)

func TestNewReact(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	err := New(context.Background(), Options{Name: "demo", Dir: dir, LidzaDir: "../..", SkipModTidy: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"go.mod", "main.go", "routes.go", "routes_test.go", "tools.go", "lidza.json",
		"CLAUDE.md", "AGENTS.md", "GEMINI.md", "docs/lidza-guide.md",
		".mcp.json", ".gemini/settings.json",
		"package.json", "index.html", "vite.config.ts", "tsconfig.json",
		"src/main.tsx", "src/router.tsx", "src/pages/Home.tsx", "src/ErrorBoundary.tsx", "playwright.config.ts", "e2e/home.spec.ts", "schema.lidza", "schema/schema.go", ".env.example", "packs.go", "benchmarks/scale_test.js",
		".gitignore", "dist/.gitkeep",
		".claude/skills/add-api-route/SKILL.md", ".claude/skills/add-resource/SKILL.md", ".claude/skills/add-page/SKILL.md",
		".claude/skills/add-pack-capability/SKILL.md", ".claude/skills/add-mcp-tool/SKILL.md", ".claude/skills/write-test/SKILL.md",
		".agents/skills/add-api-route/SKILL.md", ".gemini/commands/lidza/add-api-route.toml", ".gemini/commands/lidza/write-test.toml",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "demo" || cfg.Frontend.Template != "react" || cfg.Frontend.Dist != "dist" {
		t.Fatalf("config %+v", cfg)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if pkg := read("package.json"); !strings.Contains(pkg, `"name": "demo"`) || strings.Contains(pkg, namePlaceholder) {
		t.Errorf("package.json name not substituted: %s", pkg)
	}
	if html := read("index.html"); !strings.Contains(html, "<title>demo</title>") {
		t.Errorf("index.html title not substituted")
	}
	gomod := read("go.mod")
	if !strings.Contains(gomod, "module demo\n") || !strings.Contains(gomod, "replace "+Module+" => ") {
		t.Errorf("go.mod: %s", gomod)
	}
	if !strings.Contains(read("main.go"), "//go:embed all:dist") {
		t.Errorf("main.go should embed dist")
	}
	if !strings.Contains(read("CLAUDE.md"), "docs/lidza-guide.md") {
		t.Errorf("CLAUDE.md should point at the guide")
	}
	if read("CLAUDE.md") != read("AGENTS.md") || read("CLAUDE.md") != read("GEMINI.md") {
		t.Errorf("agent files should be identical")
	}
	if !strings.Contains(read("CLAUDE.md"), "`add-api-route`, `add-resource`, `add-page`, `add-pack-capability`, `add-mcp-tool`, `write-test`") {
		t.Errorf("CLAUDE.md should list the recipes: %s", read("CLAUDE.md"))
	}
	if skill := read(".claude/skills/add-page/SKILL.md"); !strings.Contains(skill, "name: add-page\n") || !strings.Contains(skill, "src/router.tsx") || strings.Contains(skill, "{{") {
		t.Errorf("skill: %s", skill)
	}
}

func TestNewRejects(t *testing.T) {
	base := t.TempDir()
	ctx := context.Background()
	for _, name := range []string{"", "Demo", "1app", "my app", "līdza", "-x"} {
		if err := New(ctx, Options{Name: name, Dir: filepath.Join(base, "x"), SkipModTidy: true}); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	if err := New(ctx, Options{Name: "ok", Dir: filepath.Join(base, "y"), Template: "vue", SkipModTidy: true}); err == nil || !strings.Contains(err.Error(), "vue") {
		t.Errorf("unknown template: %v", err)
	}
	full := filepath.Join(base, "full")
	os.MkdirAll(full, 0o755)
	os.WriteFile(filepath.Join(full, "f"), nil, 0o644)
	if err := New(ctx, Options{Name: "ok", Dir: full, SkipModTidy: true}); err == nil {
		t.Error("non-empty dir accepted")
	}
}

func TestGoMinor(t *testing.T) {
	if v := goMinor(); !strings.HasPrefix(v, "1.") || strings.Count(v, ".") != 1 {
		t.Fatalf("goMinor = %q", v)
	}
}

func TestNewEveryTemplate(t *testing.T) {
	for _, tpl := range []string{"svelte", "astro", "htmx"} {
		dir := filepath.Join(t.TempDir(), tpl)
		if err := New(context.Background(), Options{Name: "app", Dir: dir, Template: tpl, LidzaDir: "../..", SkipModTidy: true}); err != nil {
			t.Fatalf("%s: %v", tpl, err)
		}
		cfg, err := config.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Frontend.Template != tpl {
			t.Errorf("%s: template %q", tpl, cfg.Frontend.Template)
		}
		if tpl == "htmx" {
			for _, f := range []string{"pages.go", "views/layout.html", "views/partials/hello.html", "static/htmx.min.js"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("htmx: missing %s", f)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "pages.go.tmpl")); err == nil {
				t.Error("htmx: .tmpl suffix not stripped")
			}
			main, _ := os.ReadFile(filepath.Join(dir, "main.go"))
			if !strings.Contains(string(main), "Frontend: pages()") || strings.Contains(string(main), "go:embed") {
				t.Errorf("htmx main.go:\n%s", main)
			}
			pages, _ := os.ReadFile(filepath.Join(dir, "pages.go"))
			if strings.Contains(string(pages), namePlaceholder) {
				t.Error("htmx: placeholder left in pages.go")
			}
		} else {
			for _, f := range []string{"package.json", "dist/.gitkeep", ".env.example"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("%s: missing %s", tpl, f)
				}
			}
		}
	}
}
