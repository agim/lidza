//go:build evals

// Package evals checks what the platform does for an agent: on a fresh
// app, each classic mistake (a hand-written fetch, an invented package, an
// undeclared import, a handler type outside the schema, global state, a
// frontend call that no longer matches the API, an inaccessible element)
// is reported by `lidza check` with the expected code, a clean app is
// silent, and the guidance surfaces (skills, prompts, API, snippets,
// verify) are there. Run with:
//
//	go test -tags evals ./evals -v
//
// It scaffolds an app in a temporary directory with this checkout as the
// framework and runs npm install once, so it takes a few minutes.
package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/diag"
	"github.com/agim/lidza/pkg/mcpserver"
)

// check runs `lidza check --json` on the app.
func check(t *testing.T) diag.Report {
	t.Helper()
	out, _ := command(app, lidza, "check", "--json")
	var r diag.Report
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("lidza check --json: %v\n%s", err, out)
	}
	return r
}

// edit applies file edits (content, or "" to delete) and restores the
// originals when the test ends. A "+" prefix in the path appends.
func edit(t *testing.T, edits map[string]string) {
	t.Helper()
	type saved struct {
		content []byte
		existed bool
	}
	originals := map[string]saved{}
	for path, content := range edits {
		appendTo := strings.HasPrefix(path, "+")
		path = strings.TrimPrefix(path, "+")
		p := filepath.Join(app, filepath.FromSlash(path))
		if _, ok := originals[p]; !ok {
			old, err := os.ReadFile(p)
			originals[p] = saved{old, err == nil}
		}
		switch {
		case content == "":
			os.Remove(p)
		case appendTo:
			os.WriteFile(p, append(append([]byte{}, originals[p].content...), []byte(content)...), 0o644)
		default:
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(content), 0o644)
		}
	}
	t.Cleanup(func() {
		for p, s := range originals {
			if s.existed {
				os.WriteFile(p, s.content, 0o644)
			} else {
				os.Remove(p)
			}
		}
	})
}

// replaceIn rewrites one file with old replaced by new, restored at the end.
func replaceIn(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(app, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	edit(t, map[string]string{path: strings.Replace(string(data), old, new, 1)})
}

type expectation struct {
	code     string
	severity string
	layer    string
	file     string
	message  string // substring
}

func expect(t *testing.T, r diag.Report, wants ...expectation) {
	t.Helper()
	for _, w := range wants {
		found := false
		for _, d := range r.Diagnostics {
			if (w.code == "" || d.Code == w.code) && (w.severity == "" || d.Severity == w.severity) &&
				(w.layer == "" || d.Layer == w.layer) && (w.file == "" || d.File == w.file) &&
				(w.message == "" || strings.Contains(d.Message, w.message)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no diagnostic matching %+v in:\n%s", w, dump(r))
		}
	}
}

func dump(r diag.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status %s\n", r.Status)
	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "  %s %s %s %s:%d %s\n", d.Layer, d.Severity, d.Code, d.File, d.Line, d.Message)
	}
	return b.String()
}

func TestCleanAppIsSilent(t *testing.T) {
	r := check(t)
	if r.Status != "ok" || len(r.Diagnostics) != 0 {
		t.Fatalf("fresh app:\n%s", dump(r))
	}
}

func TestHandWrittenFetch(t *testing.T) {
	edit(t, map[string]string{"src/pages/Raw.tsx": "export function Raw() {\n  fetch('/api/v1/hello/x')\n  return null\n}\n"})
	expect(t, check(t), expectation{code: "L003", severity: "warning", layer: "frontend", file: "src/pages/Raw.tsx", message: "@lidza/client"})
}

func TestUndeclaredNpmImport(t *testing.T) {
	edit(t, map[string]string{"src/pages/Dates.tsx": "import dayjs from 'dayjs'\n\nexport function Dates() {\n  return <p>{dayjs().format()}</p>\n}\n"})
	r := check(t)
	if r.Status != "error" {
		t.Errorf("status %s", r.Status)
	}
	expect(t, r, expectation{code: "L004", severity: "error", layer: "frontend", file: "src/pages/Dates.tsx", message: "npm install dayjs"})
}

func TestInventedFrameworkPackage(t *testing.T) {
	edit(t, map[string]string{"orm.go": "package main\n\nimport _ \"github.com/agim/lidza/pkg/orm\"\n"})
	r := check(t)
	expect(t, r, expectation{code: "L004", severity: "error", layer: "go", file: "orm.go", message: "does not exist in this version"})
	for _, d := range r.Diagnostics {
		if strings.Contains(d.Message, "go get") {
			t.Errorf("misleading advice survived: %s", d.Message)
		}
	}
}

func TestHandlerTypesOutsideSchema(t *testing.T) {
	edit(t, map[string]string{"+routes.go": `
type adHoc struct{ N int }

func leak(ctx context.Context, req *router.Request[adHoc]) (map[string]any, error) { return nil, nil }
`})
	expect(t, check(t),
		expectation{code: "L005", severity: "warning", file: "routes.go", message: "adHoc"},
		expectation{code: "L005", severity: "warning", file: "routes.go", message: "map[string]any"})
}

func TestGlobalStateAndGoroutines(t *testing.T) {
	edit(t, map[string]string{"+routes.go": `
var seen = map[string]int{}

func spawn(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	go func() { seen["x"]++ }()
	return router.None{}, nil
}
`})
	expect(t, check(t),
		expectation{code: "L001", severity: "warning", file: "routes.go"},
		expectation{code: "L002", severity: "warning", file: "routes.go"})
}

func TestFrontendCallsMissingOperation(t *testing.T) {
	edit(t, map[string]string{"src/pages/Wrong.tsx": "import { api } from '@lidza/client'\n\nexport function Wrong() {\n  void api.hallo({ name: 'x' })\n  return null\n}\n"})
	r := check(t)
	if r.Status != "error" {
		t.Errorf("status %s", r.Status)
	}
	expect(t, r, expectation{severity: "error", layer: "frontend", file: "src/pages/Wrong.tsx", message: "Property 'hallo' does not exist"})
}

func TestSchemaChangeReachesTheFrontend(t *testing.T) {
	// Renaming a field in schema.lidza regenerates the Go struct and the
	// client: the page that reads the old name fails the check.
	replaceIn(t, "schema.lidza", "message string", "text    string")
	replaceIn(t, "routes.go", "Message: ", "Text: ")
	r := check(t)
	if r.Status != "error" {
		t.Errorf("status %s", r.Status)
	}
	expect(t, r, expectation{code: "TS2339", layer: "frontend", file: "src/pages/Home.tsx", message: "message"})
}

func TestInaccessibleElement(t *testing.T) {
	edit(t, map[string]string{"src/pages/Pic.tsx": "export function Pic() {\n  return <img src=\"/logo.png\" />\n}\n"})
	r := check(t)
	if r.Status != "error" {
		t.Errorf("status %s", r.Status)
	}
	expect(t, r, expectation{severity: "error", layer: "frontend", file: "src/pages/Pic.tsx", code: "jsx-a11y/alt-text"})
}

func TestGuidanceSurfaces(t *testing.T) {
	for _, skill := range []string{"add-api-route", "add-resource", "scope-query-to-signed-in-user", "add-page", "add-pack-capability", "add-mcp-tool", "send-email", "add-background-job", "publish-live-updates", "add-llm-feature", "store-file", "add-admin-pages", "add-recipe", "write-test"} {
		for _, p := range []string{filepath.Join(".claude", "skills", skill, "SKILL.md"), filepath.Join(".agents", "skills", skill, "SKILL.md"), filepath.Join(".gemini", "commands", "lidza", skill+".toml")} {
			if _, err := os.Stat(filepath.Join(app, p)); err != nil {
				t.Errorf("%s missing", p)
			}
		}
	}
	for _, f := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		data, _ := os.ReadFile(filepath.Join(app, f))
		for _, want := range []string{"docs/lidza-guide.md", "lidza_api", "lidza_snippet", "lidza verify", "lidza_verify", "add-api-route", "docs/decisions.md"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not mention %s", f, want)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(app, ".githooks", "pre-commit")); err != nil {
		t.Error("pre-commit hook missing")
	}
	// The app owns its start hook; main.go stays generated.
	start, _ := os.ReadFile(filepath.Join(app, "start.go"))
	mainGo, _ := os.ReadFile(filepath.Join(app, "main.go"))
	if !strings.Contains(string(start), "func onStart(") || !strings.Contains(string(mainGo), "OnStart: onStart") {
		t.Error("start.go with onStart, wired in main.go, missing")
	}
	out, err := command(app, lidza, "api", "pkg/router", "--filter", "Route")
	if err != nil || !strings.Contains(string(out), "func Route[In, Out any](") {
		t.Errorf("lidza api: %v\n%s", err, out)
	}
	out, err = command(app, lidza, "api", "pkg/orm")
	if err == nil || !strings.Contains(string(out), "no package") {
		t.Errorf("lidza api on an invented package: %v\n%s", err, out)
	}
	out, err = command(app, lidza, "snippet", "resource-handlers")
	if err != nil || !strings.Contains(string(out), "auth.CurrentUser(ctx).ID") {
		t.Errorf("lidza snippet: %v\n%s", err, out)
	}

	cfg, err := config.Load(app)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewInProcessClient(mcpserver.New(app, cfg))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	prompts, err := c.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil || len(prompts.Prompts) != 14 {
		t.Errorf("prompts: %v %d", err, len(prompts.Prompts))
	}
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"lidza_routes", "lidza_check", "lidza_api", "lidza_snippet", "lidza_logs", "lidza_gen", "lidza_gen_resource", "lidza_verify", "lidza_test", "lidza_recipe_add", "lidza_recipes", "lidza_decision_add", "lidza_credentials_set", "lidza_credentials_list"} {
		if !names[want] {
			t.Errorf("tool %s missing", want)
		}
	}
	// A command tool runs the CLI and returns its report.
	req := mcp.CallToolRequest{}
	req.Params.Name = "lidza_check"
	checkRes, err := c.CallTool(ctx, req)
	if err != nil || checkRes.IsError {
		t.Fatalf("lidza_check through MCP: %v %+v", err, checkRes)
	}
	var cmdRes struct {
		Command string `json:"command"`
		OK      bool   `json:"ok"`
		Report  struct {
			Status string `json:"status"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(mcp.GetTextFromContent(checkRes.Content[0])), &cmdRes); err != nil || !cmdRes.OK || cmdRes.Report.Status != "ok" || cmdRes.Command != "lidza check --json" {
		t.Fatalf("lidza_check result: %v %+v\n%s", err, cmdRes, mcp.GetTextFromContent(checkRes.Content[0]))
	}
	res, err := c.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: "lidza://api/pkg/router"}})
	if err != nil {
		t.Fatal(err)
	}
	if txt, ok := res.Contents[0].(mcp.TextResourceContents); !ok || !strings.Contains(txt.Text, "func NotFound(") {
		t.Errorf("lidza://api/pkg/router: %+v", res.Contents)
	}
}

func TestVerifyPassesOnCleanApp(t *testing.T) {
	out, err := command(app, lidza, "verify", "--json")
	var rep struct {
		Status string `json:"status"`
		Steps  []struct {
			Name, Status, Reason string
		} `json:"steps"`
	}
	if jerr := json.Unmarshal(out, &rep); jerr != nil {
		t.Fatalf("verify --json: %v %v\n%s", err, jerr, out)
	}
	if rep.Status != "ok" {
		t.Fatalf("verify: %+v", rep)
	}
	for _, s := range rep.Steps {
		if s.Name == "test" && s.Status != "ok" {
			t.Errorf("tests should run and pass: %+v", s)
		}
	}
}

// TestReferenceApp runs the reference app's own verify (Go tests against
// Postgres), skipped when its test database is unreachable.
func TestReferenceApp(t *testing.T) {
	dir := filepath.Join(root, "examples", "notes")
	if out, err := command(dir, "pg_isready"); err != nil {
		t.Skipf("postgres not reachable: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		if out, err := command(dir, "npm", "install", "--no-fund", "--no-audit"); err != nil {
			t.Fatalf("npm install: %v\n%s", err, out)
		}
	}
	out, err := command(dir, lidza, "verify")
	if err != nil {
		t.Fatalf("examples/notes: lidza verify failed:\n%s", out)
	}
}

// TestPackCapability scaffolds a Rust pack: the check builds it, the
// generated wrapper compiles, and the capability is an MCP tool that
// runs. Skipped without cargo.
func TestPackCapability(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join(app, "packs", "echo", "pack.lidza.json")); err == nil {
		t.Fatal("pack already exists")
	}
	if out, err := command(app, "cargo", "--version"); err != nil {
		t.Skipf("cargo not installed: %s", out)
	}
	out, err := command(app, lidza, "pack", "scaffold", "echo")
	if err != nil {
		t.Fatalf("pack scaffold: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		os.RemoveAll(filepath.Join(app, "packs", "echo"))
		cfg, _ := os.ReadFile(filepath.Join(app, "lidza.json"))
		os.WriteFile(filepath.Join(app, "lidza.json"), []byte(strings.Replace(string(cfg), "\"echo\"", "", 1)), 0o644)
		command(app, lidza, "gen")
	})
	r := check(t)
	if r.Status != "ok" {
		t.Fatalf("check after pack scaffold:\n%s", dump(r))
	}
	for _, f := range []string{"packs/echo/pack.go", "packs/echo/echo.wasm", "packs/echo/rust/src/schema.rs"} {
		if _, err := os.Stat(filepath.Join(app, f)); err != nil {
			t.Errorf("%s missing", f)
		}
	}
	cfg, err := config.Load(app)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewInProcessClient(mcpserver.New(app, cfg))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = "pack_echo_reverse"
	req.Params.Arguments = map[string]any{"text": "abc"}
	res, err := c.CallTool(ctx, req)
	if err != nil || res.IsError || !strings.Contains(mcp.GetTextFromContent(res.Content[0]), `"cba"`) {
		t.Fatalf("pack_echo_reverse: %v %+v", err, res)
	}
}

// TestRecipeAdd: an app convention recorded with lidza recipe add becomes
// a skill, a Gemini command, a prompt after restart, and a line in the
// agent files; the framework's own recipes stay separate.
func TestRecipeAdd(t *testing.T) {
	edit(t, map[string]string{"docs/lidza-guide.md": mustRead(t, "docs/lidza-guide.md"), "CLAUDE.md": mustRead(t, "CLAUDE.md"), "AGENTS.md": mustRead(t, "AGENTS.md"), "GEMINI.md": mustRead(t, "GEMINI.md")})
	t.Cleanup(func() {
		os.RemoveAll(filepath.Join(app, ".claude", "skills", "paginate-list"))
		os.RemoveAll(filepath.Join(app, ".agents", "skills", "paginate-list"))
		os.Remove(filepath.Join(app, ".gemini", "commands", "lidza", "paginate-list.toml"))
	})
	out, err := command(app, lidza, "recipe", "add", "Paginate a list", "--description", "Lists take limit and offset.", "--step", "Read them with PageParams.", "--step", "`lidza check`.")
	if err != nil {
		t.Fatalf("recipe add: %v\n%s", err, out)
	}
	for _, p := range []string{".claude/skills/paginate-list/SKILL.md", ".agents/skills/paginate-list/SKILL.md", ".gemini/commands/lidza/paginate-list.toml"} {
		if _, err := os.Stat(filepath.Join(app, p)); err != nil {
			t.Errorf("%s missing", p)
		}
	}
	guide := mustRead(t, "docs/lidza-guide.md")
	if !strings.Contains(guide, "## App recipes") || !strings.Contains(guide, "### Paginate a list") || strings.Index(guide, "## Recipes") > strings.Index(guide, "### Paginate a list") {
		t.Errorf("guide:\n%s", guide)
	}
	if !strings.Contains(mustRead(t, "CLAUDE.md"), "this app's own: `paginate-list`") {
		t.Errorf("CLAUDE.md does not list the app recipe")
	}
	if out, _ := command(app, lidza, "recipe", "list"); !strings.Contains(string(out), "paginate-list") || !strings.Contains(string(out), "app") {
		t.Errorf("recipe list:\n%s", out)
	}
}

func mustRead(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(app, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSetup: lidza setup on a fresh app enables packs, writes the
// environment files with real values, creates and migrates the databases
// and installs node_modules. Skipped when Postgres is unreachable.
// A decision is recorded through the CLI and read back.
func TestDecisionAdd(t *testing.T) {
	edit(t, map[string]string{"docs/decisions.md": mustRead(t, "docs/decisions.md")})
	out, err := command(app, lidza, "decision", "add", "Thumbnails in a Rust pack", "--why", "Image bytes come from users: contained.", "--touches", "packs/media")
	if err != nil {
		t.Fatalf("decision add: %v\n%s", err, out)
	}
	log := mustRead(t, "docs/decisions.md")
	if !strings.HasPrefix(log, "# Decisions") || !strings.Contains(log, ": Thumbnails in a Rust pack\n\nWhy: Image bytes come from users: contained.\n\nTouches: packs/media") {
		t.Errorf("decisions.md:\n%s", log)
	}
	if out, _ := command(app, lidza, "decision", "list"); !strings.Contains(string(out), "Thumbnails in a Rust pack") {
		t.Errorf("decision list:\n%s", out)
	}
	if out, err := command(app, lidza, "decision", "add", "No reason"); err == nil {
		t.Errorf("decision without --why accepted:\n%s", out)
	}
}

// Secrets are sealed through the CLI and read back by name.
func TestCredentials(t *testing.T) {
	t.Cleanup(func() {
		os.Remove(filepath.Join(app, "config", "master.key"))
		os.Remove(filepath.Join(app, "config", "credentials.yml.enc"))
	})
	out, err := command(app, lidza, "credentials", "set", "MAIL_API_KEY=key-abc", "LLM_API_KEY=sk-1")
	if err != nil {
		t.Fatalf("credentials set: %v\n%s", err, out)
	}
	sealed, _ := os.ReadFile(filepath.Join(app, "config", "credentials.yml.enc"))
	if strings.Contains(string(sealed), "key-abc") {
		t.Fatal("credentials file is not sealed")
	}
	if out, _ := command(app, lidza, "credentials", "list"); !strings.Contains(string(out), "MAIL_API_KEY") || strings.Contains(string(out), "key-abc") {
		t.Errorf("credentials list:\n%s", out)
	}
	if out, _ := command(app, lidza, "credentials", "show", "LLM_API_KEY"); strings.TrimSpace(string(out)) != "sk-1" {
		t.Errorf("credentials show:\n%s", out)
	}
	ignore, _ := os.ReadFile(filepath.Join(app, ".gitignore"))
	if !strings.Contains(string(ignore), "config/master.key") {
		t.Error("master.key not ignored")
	}
}

func TestSetup(t *testing.T) {
	if out, err := command(app, "pg_isready"); err != nil {
		t.Skipf("postgres not reachable: %s", out)
	}
	tmp := t.TempDir()
	if out, err := command(tmp, lidza, "new", "setupapp", "--no-setup", "--lidza-dir", root); err != nil {
		t.Fatalf("lidza new: %v\n%s", err, out)
	}
	dir := filepath.Join(tmp, "setupapp")
	args := []string{"setup", "--packs", "auth,mail", "--no-commit"}
	if u := os.Getenv("DATABASE_URL"); u != "" {
		args = append(args, "--database-url", u)
	}
	out, err := command(dir, lidza, args...)
	if err != nil {
		t.Fatalf("lidza setup: %v\n%s", err, out)
	}
	for _, want := range []string{"[setup] pack db:", "[setup] pack auth:", "[setup] pack mail:", ".env written", ".env.test written", "database setupapp_dev: created if missing", "database setupapp_test: created if missing", "node_modules: installed"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("setup output lacks %q:\n%s", want, out)
		}
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "DATABASE_URL=") || !strings.Contains(string(env), "AUTH_SECRET=") || strings.Contains(string(env), "change-me") || !strings.Contains(string(env), "MAIL_FROM=\"setupapp <setupapp@example.com>\"") {
		t.Errorf(".env:\n%s", env)
	}
	envTest, _ := os.ReadFile(filepath.Join(dir, ".env.test"))
	if !strings.Contains(string(envTest), "setupapp_test") || !strings.Contains(string(envTest), "MAIL_PROVIDER=outbox") {
		t.Errorf(".env.test:\n%s", envTest)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		t.Error("node_modules missing")
	}
	// Idempotent: a second run changes nothing and says so.
	out, err = command(dir, lidza, args...)
	if err != nil || !strings.Contains(string(out), "pack auth: already enabled") || !strings.Contains(string(out), ".env exists, left alone") {
		t.Fatalf("second setup: %v\n%s", err, out)
	}
	// The app runs its tests against the databases setup created.
	if out, err := command(dir, lidza, "test"); err != nil {
		t.Fatalf("lidza test after setup: %v\n%s", err, out)
	}
}
