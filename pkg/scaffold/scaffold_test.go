package scaffold

import (
	"context"
	"github.com/agim/lidza/packs/db"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/recipes"
)

func TestNewReact(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	err := New(context.Background(), Options{Name: "demo", Dir: dir, LidzaDir: "../..", SkipModTidy: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"go.mod", "main.go", "routes.go", "routes_test.go", "tools.go", "lidza.json",
		"CLAUDE.md", "AGENTS.md", "GEMINI.md", "docs/lidza-guide.md", "docs/decisions.md", "docs/brief.md", "README.md", ".github/workflows/ci.yml", ".github/dependabot.yml",
		".mcp.json", ".gemini/settings.json",
		"package.json", "index.html", "vite.config.ts", "tsconfig.json",
		"src/main.tsx", "src/router.tsx", "src/pages/Home.tsx", "src/ErrorBoundary.tsx", "playwright.config.ts", "e2e/home.spec.ts", "schema.lidza", "schema/schema.go", ".env.example", "packs.go", "benchmarks/scale_test.js",
		".gitignore", "dist/.gitkeep", "Dockerfile", ".dockerignore", "deploy/demo.service",
		".claude/skills/add-api-route/SKILL.md", ".claude/skills/add-resource/SKILL.md", ".claude/skills/add-page/SKILL.md",
		".claude/skills/add-pack-capability/SKILL.md", ".claude/skills/add-mcp-tool/SKILL.md", ".claude/skills/write-test/SKILL.md", ".claude/skills/add-recipe/SKILL.md",
		".agents/skills/add-api-route/SKILL.md",
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
	if !strings.Contains(read("AGENTS.md"), "docs/lidza-guide.md") {
		t.Errorf("AGENTS.md should point at the guide")
	}
	if !strings.Contains(read("AGENTS.md"), "docs/decisions.md") || !strings.HasPrefix(read("docs/decisions.md"), "# Decisions") {
		t.Errorf("AGENTS.md should point at the decision log")
	}
	if c := read("AGENTS.md"); !strings.Contains(c, "Read `docs/brief.md` first") || !strings.Contains(c, "<!-- lidza:agreements -->") || !strings.Contains(c, "## Team notes") {
		t.Errorf("AGENTS.md should point at the brief and end with the working agreements and team notes")
	}
	// One instructions file; CLAUDE.md and GEMINI.md import it.
	if read("CLAUDE.md") != "@AGENTS.md\n" || read("GEMINI.md") != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md and GEMINI.md should be the line @AGENTS.md: %q %q", read("CLAUDE.md"), read("GEMINI.md"))
	}
	if !strings.Contains(read("AGENTS.md"), "<!-- lidza:recipes -->`start-with-brief`, `add-api-route`, `add-resource`, `add-sign-in`, `scope-query-to-signed-in-user`, `add-page`, `set-head-of-page`, `add-pack-capability`, `add-mcp-tool`, `send-email`, `add-background-job`, `publish-live-updates`, `add-llm-feature`, `store-file`, `receive-webhook`, `connect-external-account`, `add-admin-pages`, `extend-admin-pages`, `add-recipe`, `write-test`, `organize-application-packages`<!-- /lidza:recipes -->") {
		t.Errorf("AGENTS.md should list the recipes: %s", read("AGENTS.md"))
	}
	// An app recipe: added to the guide, generated, listed in the agent files.
	if _, err := recipes.Add(dir, "Paginate a list", "Lists take limit and offset.", []string{"Use PageParams."}); err != nil {
		t.Fatal(err)
	}
	changed, err := Refresh(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed, ",") != "AGENTS.md" {
		t.Errorf("refresh changed %v", changed)
	}
	if !strings.Contains(read("AGENTS.md"), "`organize-application-packages`; this app's own: `paginate-list`<!-- /lidza:recipes -->") {
		t.Errorf("app recipe not listed: %s", read("AGENTS.md"))
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "paginate-list", "SKILL.md")); err != nil {
		t.Error("app recipe has no skill")
	}
	// A framework recipe edited by hand comes back on refresh; the app's stays.
	guide := read("docs/lidza-guide.md")
	os.WriteFile(filepath.Join(dir, "docs", "lidza-guide.md"), []byte(strings.Replace(guide, "### Add an MCP tool", "### Add an MCP tool (edited)", 1)), 0o644)
	if changed, _ := Refresh(dir, cfg); strings.Join(changed, ",") != "docs/lidza-guide.md (framework recipes)" {
		t.Errorf("framework recipes not refreshed: %v", changed)
	}
	if g := read("docs/lidza-guide.md"); strings.Contains(g, "(edited)") || !strings.Contains(g, "### Paginate a list") {
		t.Errorf("refresh: %s", g)
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
			for _, f := range []string{"pages.go", "pages_test.go", "views/layout.html", "views/partials/hello.html", "static/htmx.min.js", "static/analytics.js"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("htmx: missing %s", f)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "pages.go.tmpl")); err == nil {
				t.Error("htmx: .tmpl suffix not stripped")
			}
			main, _ := os.ReadFile(filepath.Join(dir, "main.go"))
			if !regexp.MustCompile(`Frontend:\s+pages\(\)`).MatchString(string(main)) || strings.Contains(string(main), "go:embed") {
				t.Errorf("htmx main.go:\n%s", main)
			}
			pages, _ := os.ReadFile(filepath.Join(dir, "pages.go"))
			if strings.Contains(string(pages), namePlaceholder) {
				t.Error("htmx: placeholder left in pages.go")
			}
		} else {
			for _, f := range []string{"package.json", "dist/.gitkeep", ".env.example", "playwright.config.ts", "e2e/home.spec.ts", "src/analytics.ts", "src/timezone.ts"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("%s: missing %s", tpl, f)
				}
			}
		}
		if tpl == "svelte" {
			if _, err := os.Stat(filepath.Join(dir, "scripts", "prerender.mjs")); err != nil {
				t.Error("svelte: missing scripts/prerender.mjs")
			}
		}
		if tpl == "astro" {
			if _, err := os.Stat(filepath.Join(dir, "eslint.config.js")); err != nil {
				t.Error("astro: missing eslint.config.js")
			}
		}
		guide, _ := os.ReadFile(filepath.Join(dir, "docs", "lidza-guide.md"))
		if !strings.Contains(string(guide), "This app uses the **"+tpl+"** template.") {
			t.Errorf("%s: guide does not name the template", tpl)
		}
	}
}

// Refresh (lidza gen, dev, test, update) repairs a DATABASE_URL written
// for another machine's socket directory, as setup does.
func TestRefreshRepairsSocket(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := New(context.Background(), Options{Name: "demo", Dir: dir, LidzaDir: "../..", SkipModTidy: true}); err != nil {
		t.Fatal(err)
	}
	sock := t.TempDir()
	os.WriteFile(filepath.Join(sock, ".s.PGSQL.5432"), nil, 0o600)
	old := db.SocketDirs
	db.SocketDirs = []string{sock}
	t.Cleanup(func() { db.SocketDirs = old })
	t.Setenv("PGHOST", "")
	t.Setenv("PGPORT", "")
	os.WriteFile(filepath.Join(dir, ".env"), []byte("DATABASE_URL=postgres:///demo_dev?host=/var/run/postgresql-absent\n"), 0o600)
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := Refresh(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if string(data) != "DATABASE_URL=postgres:///demo_dev?host="+sock+"\n" || !strings.Contains(strings.Join(changed, " "), ".env (DATABASE_URL now") {
		t.Fatalf("repair: %s %v", data, changed)
	}
}

// A new app tracks .env.test; Refresh gives an older app's ignore list
// the exception, once.
func TestRefreshTracksEnvTest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := New(context.Background(), Options{Name: "demo", Dir: dir, LidzaDir: "../..", SkipModTidy: true}); err != nil {
		t.Fatal(err)
	}
	gi := filepath.Join(dir, ".gitignore")
	if data, _ := os.ReadFile(gi); ignoresEnvTest(string(data)) {
		t.Fatalf("a new app ignores .env.test: %s", data)
	}
	os.WriteFile(gi, []byte("node_modules/\n.env\n.env.*\n!.env.example\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env.test"), []byte("CACHE_URL=memory\n"), 0o644)
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := Refresh(dir, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if data, _ := os.ReadFile(gi); string(data) != "node_modules/\n.env\n.env.*\n!.env.example\n!.env.test\n" {
		t.Fatalf("gitignore: %q", data)
	}
}

// Refresh adds the packs' test settings an older .env.test lacks, once.
func TestRefreshTestEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := New(context.Background(), Options{Name: "demo", Dir: dir, LidzaDir: "../..", SkipModTidy: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Packs = []string{"lidza/db", "lidza/cache", "lidza/auth", "lidza/analytics", "lidza/llm"}
	p := filepath.Join(dir, ".env.test")
	os.WriteFile(p, []byte("DATABASE_URL=postgres:///demo_test\n"), 0o644)
	for i := 0; i < 2; i++ {
		if _, err := Refresh(dir, cfg); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(p)
	if string(data) != "DATABASE_URL=postgres:///demo_test\nCACHE_URL=memory\nLLM_PROVIDER=fake\nEMBED_PROVIDER=fake\n" {
		t.Fatalf(".env.test: %q", data)
	}
}

func TestRefreshPackageGuidanceForAllAgents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := New(context.Background(), Options{Name: "demo", Dir: dir, LidzaDir: "../..", SkipModTidy: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// An app from before the stubs: three identical copies, without the
	// layout and guidance lines, with an app's own instruction.
	body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	old := strings.ReplaceAll(string(body), "- "+LayoutLine+"\n", "")
	old = strings.ReplaceAll(old, "- "+AgentGuidanceLine+"\n", "")
	old += "\n- Keep the application-specific instructions.\n"
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Refresh(dir, cfg); err != nil {
		t.Fatal(err)
	}
	agents := readFile(t, dir, "AGENTS.md")
	for _, want := range []string{LayoutLine, AgentGuidanceLine, "Keep the application-specific instructions."} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md missing %s", want)
		}
	}
	for _, name := range []string{"CLAUDE.md", "GEMINI.md"} {
		if got := readFile(t, dir, name); got != "@AGENTS.md\n" {
			t.Errorf("%s is not the stub: %q", name, got)
		}
	}
	for _, file := range []string{".claude/skills/organize-application-packages/SKILL.md", ".agents/skills/organize-application-packages/SKILL.md"} {
		body, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || !strings.Contains(string(body), "internal/providers/<vendor>/") {
			t.Errorf("recipe %s: %v", file, err)
		}
	}
	changed, err := Refresh(dir, cfg)
	if err != nil || len(changed) != 0 {
		t.Errorf("second refresh changed %v: %v", changed, err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
