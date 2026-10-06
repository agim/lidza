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
	"regexp"
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

// A fresh app has nothing to fix; the one note is the brief, still open,
// which asks the agent to interview the developer (L015).
func TestCleanAppIsSilent(t *testing.T) {
	r := check(t)
	if r.Status != "ok" || len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "L015" || r.Diagnostics[0].Severity != "note" {
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

// A "from" that is not an import (a JSX attribute) is no L004; a declared
// import written across lines is read as one.
func TestJSXFromIsNotAnImport(t *testing.T) {
	edit(t, map[string]string{"src/pages/Range.tsx": `import {
  useState,
} from 'react'

export function Range() {
  const [v, setV] = useState('start')
  return (
    <label>
      <input type="radio" name="from" value={v} onChange={(e) => setV(e.target.value)} />
      Start
    </label>
  )
}
`})
	r := check(t)
	for _, d := range r.Diagnostics {
		if d.Code == "L004" {
			t.Errorf("L004 on a JSX attribute or a declared import:\n%s", dump(r))
		}
	}
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

// An app admin page without a decision naming it: an agent that adds one
// is told to record what it lets admins do (L014).
func TestAdminPageWithoutDecision(t *testing.T) {
	edit(t, map[string]string{"adminpages.go": `package main

import "github.com/agim/lidza/packs/admin"

var adminPages = []admin.Page{{Name: "Refunds", Path: "refunds", Template: "refunds.html"}}
`})
	expect(t, check(t), expectation{code: "L014", severity: "warning", file: "adminpages.go", message: "Admin page Refunds"})
}

// A secret pasted into the frontend ships to every browser (L010); a
// publishable key is public and passes.
func TestSecretInFrontend(t *testing.T) {
	edit(t, map[string]string{"src/pages/Pay.tsx": "const secret = '" + "sk_" + "live_4eC39HqLyjWDarjtT1zdp7dc0123'\nconst publishable = 'pk_live_51H8abcdefghijklmnopqrstuv'\n\nexport function Pay() {\n  return <p>{secret.length + publishable.length}</p>\n}\n"})
	r := check(t)
	expect(t, r, expectation{code: "L010", severity: "warning", layer: "frontend", file: "src/pages/Pay.tsx", message: "Stripe secret key"})
	for _, d := range r.Diagnostics {
		if d.Code == "L010" && d.Line != 1 {
			t.Errorf("L010 on the publishable key:\n%s", dump(r))
		}
	}
}

// A query on an owned table (a model with ownerId) that does not filter
// by the owner reaches every user's rows (L018); one exempted with
// lidza:ignore is not reported.
func TestUnscopedOwnedQuery(t *testing.T) {
	if _, err := os.Stat(filepath.Join(app, "db")); err == nil {
		t.Fatal("the eval app has a db directory; this case assumes none")
	}
	// The check regenerates db/ (DDL, lock, a migration) from the model:
	// all of it goes when the test ends, and schema/schema.go is restored.
	t.Cleanup(func() { os.RemoveAll(filepath.Join(app, "db")) })
	goFile, err := os.ReadFile(filepath.Join(app, "schema", "schema.go"))
	if err != nil {
		t.Fatal(err)
	}
	edit(t, map[string]string{
		"schema/schema.go": string(goFile),
		"+schema.lidza":    "\nmodel Product {\n  id      uuid   @id @default(uuid())\n  ownerId uuid   @index\n  name    string\n}\n",
		"db/queries/product.sql": "-- name: ListProducts :many\nSELECT * FROM product ORDER BY name;\n\n" +
			"-- name: MyProducts :many\nSELECT * FROM product WHERE owner_id = $1;\n\n" +
			"-- lidza:ignore L018 (the admin page)\n-- name: AllProducts :many\nSELECT * FROM product;\n",
	})
	r := check(t)
	expect(t, r, expectation{code: "L018", severity: "warning", file: "db/queries/product.sql", message: "ListProducts on product"})
	for _, d := range r.Diagnostics {
		if d.Code == "L018" && !strings.Contains(d.Message, "ListProducts") {
			t.Errorf("unexpected: %s:%d %s", d.File, d.Line, d.Message)
		}
	}
}

// A dropped error is a warning (L016); a handled one is not.
func TestDroppedError(t *testing.T) {
	edit(t, map[string]string{"cleanup.go": `package main

import "os"

func cleanup(path string) error {
	os.Remove(path + ".tmp")
	return os.Remove(path)
}

var _ = cleanup
`})
	r := check(t)
	expect(t, r, expectation{code: "L016", severity: "warning", layer: "go", file: "cleanup.go", message: "os.Remove"})
	for _, d := range r.Diagnostics {
		if d.Code == "L016" && d.Line != 6 {
			t.Errorf("L016 on a returned error:\n%s", dump(r))
		}
	}
}

// A function copied with its names changed is a warning at the copy,
// pointing at the first (L017).
func TestDuplicatedCode(t *testing.T) {
	body := func(name, v string) string {
		return `func ` + name + `(items []string) (int, error) {
	total := 0
	for _, ` + v + ` := range items {
		if ` + v + ` == "" {
			continue
		}
		n, err := strconv.Atoi(` + v + `)
		if err != nil {
			return 0, fmt.Errorf("` + name + `: %w", err)
		}
		if n < 0 {
			return 0, errors.New("negative")
		}
		total += n
	}
	if total > 100 {
		total = 100
	}
	return total, nil
}

var _ = ` + name + `
`
	}
	imports := "package main\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n\t\"strconv\"\n)\n\n"
	edit(t, map[string]string{"posts.go": imports + body("sumPosts", "p"), "tags.go": imports + body("sumTags", "tag")})
	expect(t, check(t), expectation{code: "L017", severity: "warning", layer: "go", file: "tags.go", message: "posts.go:10"})
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
	// Gemini CLI reads .agents/skills; no commands of its own.
	if _, err := os.Stat(filepath.Join(app, ".gemini", "commands")); !os.IsNotExist(err) {
		t.Errorf(".gemini/commands written: %v", err)
	}
	for _, skill := range []string{"start-with-brief", "add-api-route", "add-resource", "scope-query-to-signed-in-user", "add-page", "set-head-of-page", "add-pack-capability", "add-mcp-tool", "send-email", "add-background-job", "publish-live-updates", "add-llm-feature", "store-file", "receive-webhook", "connect-external-account", "dates-and-time-zones", "roles-and-permissions", "record-audit-event", "add-admin-pages", "extend-admin-pages", "add-recipe", "write-test", "organize-application-packages"} {
		for _, p := range []string{filepath.Join(".claude", "skills", skill, "SKILL.md"), filepath.Join(".agents", "skills", skill, "SKILL.md")} {
			if _, err := os.Stat(filepath.Join(app, p)); err != nil {
				t.Errorf("%s missing", p)
			}
			if skill == "send-email" {
				data, err := os.ReadFile(filepath.Join(app, p))
				for _, want := range []string{"SendTx(ctx, tx", "must roll back", "exactly-once", "mail.Attachment", "MAIL_MAX_ATTACHMENT_BYTES", "MAIL_SMTP_TIMEOUT", "`Cc`", "`Bcc`"} {
					if err != nil || !strings.Contains(string(data), want) {
						t.Errorf("%s: transactional mail guidance missing %q: %v", p, want, err)
					}
				}
			}
		}
	}
	data, _ := os.ReadFile(filepath.Join(app, "AGENTS.md"))
	for _, want := range []string{"docs/lidza-guide.md", "lidza_api", "lidza_snippet", "lidza verify", "lidza_verify", "add-api-route", "docs/decisions.md"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("AGENTS.md does not mention %s", want)
		}
	}
	// Claude Code and Gemini CLI read it through a one-line import.
	for _, f := range []string{"CLAUDE.md", "GEMINI.md"} {
		if s := mustRead(t, f); s != "@AGENTS.md\n" {
			t.Errorf("%s is not the line @AGENTS.md: %q", f, s)
		}
	}
	if _, err := os.Stat(filepath.Join(app, ".githooks", "pre-commit")); err != nil {
		t.Error("pre-commit hook missing")
	}
	// The app owns its start hook; main.go stays generated.
	start, _ := os.ReadFile(filepath.Join(app, "start.go"))
	mainGo, _ := os.ReadFile(filepath.Join(app, "main.go"))
	if !strings.Contains(string(start), "func onStart(") || !regexp.MustCompile(`OnStart:\s+onStart`).MatchString(string(mainGo)) {
		t.Error("start.go with onStart, wired in main.go, missing")
	}
	out, err := command(app, lidza, "api", "pkg/router", "--filter", "Route")
	if err != nil || !strings.Contains(string(out), "func Route[In, Out any](") {
		t.Errorf("lidza api: %v\n%s", err, out)
	}
	// Without a package: the list and how to ask for one, never every
	// package rendered (an agent carries that output every turn).
	out, err = command(app, lidza, "api")
	if err != nil || !strings.Contains(string(out), "pkg/router") || strings.Contains(string(out), "func Route[") || len(out) > 20000 {
		t.Errorf("lidza api without a package: %v (%d bytes)\n%.2000s", err, len(out), out)
	}
	out, err = command(app, lidza, "admin", "add", "dev@example.com")
	if err != nil {
		t.Errorf("lidza admin add: %v\n%s", err, out)
	}
	if out, err = command(app, lidza, "admin", "list"); err != nil || strings.TrimSpace(string(out)) != "dev@example.com" {
		t.Errorf("lidza admin list: %v\n%s", err, out)
	}
	// Without a name, show prints every value.
	if out, err = command(app, lidza, "credentials", "show"); err != nil || !strings.Contains(string(out), "ADMIN_USERS: dev@example.com") {
		t.Errorf("lidza credentials show: %v\n%s", err, out)
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
	c, err := client.NewInProcessClient(mcpserver.New(app, cfg).MCPServer)
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
	if err != nil || len(prompts.Prompts) != 24 {
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
	for _, want := range []string{"lidza_routes", "lidza_check", "lidza_api", "lidza_snippet", "lidza_logs", "lidza_gen", "lidza_gen_resource", "lidza_verify", "lidza_test", "lidza_ship", "lidza_recipe_add", "lidza_recipes", "lidza_decision_add", "lidza_brief", "lidza_brief_answer", "lidza_brief_skip", "lidza_note_add", "lidza_credentials_set", "lidza_credentials_list"} {
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
	c, err := client.NewInProcessClient(mcpserver.New(app, cfg).MCPServer)
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
// a skill for each agent, a prompt after restart, and a line in
// AGENTS.md; the framework's own recipes stay separate.
func TestRecipeAdd(t *testing.T) {
	edit(t, map[string]string{"docs/lidza-guide.md": mustRead(t, "docs/lidza-guide.md"), "CLAUDE.md": mustRead(t, "CLAUDE.md"), "AGENTS.md": mustRead(t, "AGENTS.md"), "GEMINI.md": mustRead(t, "GEMINI.md")})
	t.Cleanup(func() {
		os.RemoveAll(filepath.Join(app, ".claude", "skills", "paginate-list"))
		os.RemoveAll(filepath.Join(app, ".agents", "skills", "paginate-list"))
	})
	out, err := command(app, lidza, "recipe", "add", "Paginate a list", "--description", "Lists take limit and offset.", "--step", "Read them with PageParams.", "--step", "`lidza check`.")
	if err != nil {
		t.Fatalf("recipe add: %v\n%s", err, out)
	}
	for _, p := range []string{".claude/skills/paginate-list/SKILL.md", ".agents/skills/paginate-list/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(app, p)); err != nil {
			t.Errorf("%s missing", p)
		}
	}
	guide := mustRead(t, "docs/lidza-guide.md")
	if !strings.Contains(guide, "## App recipes") || !strings.Contains(guide, "### Paginate a list") || strings.Index(guide, "## Recipes") > strings.Index(guide, "### Paginate a list") {
		t.Errorf("guide:\n%s", guide)
	}
	if !strings.Contains(mustRead(t, "AGENTS.md"), "this app's own: `paginate-list`") {
		t.Errorf("AGENTS.md does not list the app recipe")
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

// TestSetup: lidza install on a fresh app enables packs, writes the
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

// The brief through the CLI: an answer is saved and applied (a decision,
// the working agreements in every agent file), a team note lands in the
// agent files, and the check's note names what is still open.
func TestBrief(t *testing.T) {
	restore := map[string]string{}
	for _, f := range []string{"docs/brief.md", "docs/decisions.md", "CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		restore[f] = mustRead(t, f)
	}
	edit(t, restore)
	if out, err := command(app, lidza, "brief", "answer", "purpose", "A place to keep project notes"); err != nil {
		t.Fatalf("brief answer: %v\n%s", err, out)
	}
	if out, err := command(app, lidza, "brief", "answer", "signin", "Email and password, Google"); err != nil || !strings.Contains(string(out), "decision recorded: Sign-in: Email and password, Google") {
		t.Fatalf("brief answer signin: %v\n%s", err, out)
	}
	if out, err := command(app, lidza, "brief", "answer", "push", "After every verified commit"); err != nil {
		t.Fatalf("brief answer push: %v\n%s", err, out)
	}
	if out, err := command(app, lidza, "note", "add", "Prices are shown in euros"); err != nil {
		t.Fatalf("note add: %v\n%s", err, out)
	}
	if b := mustRead(t, "docs/brief.md"); !strings.Contains(b, "A place to keep project notes") {
		t.Errorf("brief.md:\n%s", b)
	}
	if s := mustRead(t, "AGENTS.md"); !strings.Contains(s, "- Pushing: After every verified commit.") || !strings.Contains(s, ": Prices are shown in euros.") {
		t.Errorf("AGENTS.md:\n%s", s)
	}
	for _, f := range []string{"CLAUDE.md", "GEMINI.md"} {
		if s := mustRead(t, f); s != "@AGENTS.md\n" {
			t.Errorf("%s grew past the import: %q", f, s)
		}
	}
	if out, _ := command(app, lidza, "brief", "--list"); !strings.Contains(string(out), "A place to keep project notes") || !strings.Contains(string(out), "(open)") {
		t.Errorf("brief --list:\n%s", out)
	}
	// Without a terminal the interview points at the tools.
	if out, err := command(app, lidza, "brief"); err == nil || !strings.Contains(string(out), "lidza_brief") {
		t.Errorf("brief without a terminal: %v\n%s", err, out)
	}
	expect(t, check(t), expectation{code: "L015", severity: "note", file: "docs/brief.md", message: "users"})
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
	args := []string{"install", "--packs", "auth,mail", "--no-commit"}
	if u := os.Getenv("DATABASE_URL"); u != "" {
		args = append(args, "--database-url", u)
	}
	out, err := command(dir, lidza, args...)
	if err != nil {
		t.Fatalf("lidza install: %v\n%s", err, out)
	}
	for _, want := range []string{"[install] pack db:", "[install] pack auth:", "[install] pack mail:", ".env written", ".env.test written", "database setupapp_dev: created if missing", "database setupapp_test: created if missing", "node_modules: installed"} {
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

func TestInternalApplicationAPISurfaces(t *testing.T) {
	edit(t, map[string]string{"internal/orders/orders.go": "package orders\n\n// Submit validates an order.\nfunc Submit() {}\n"})
	for _, args := range [][]string{{"api", "--list"}, {"api", "app", "--filter", "Submit"}, {"api", "./internal/orders", "--filter", "Submit"}} {
		out, err := command(app, lidza, args...)
		if err != nil || !strings.Contains(string(out), "evalapp/internal/orders") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if len(args) > 2 && args[2] != "--list" && !strings.Contains(string(out), "func Submit()") {
			t.Errorf("API declaration missing: %s", out)
		}
	}
	cfg, err := config.Load(app)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewInProcessClient(mcpserver.New(app, cfg).MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = "lidza_api"
	req.Params.Arguments = map[string]any{"package": "app", "filter": "Submit"}
	res, err := c.CallTool(ctx, req)
	if err != nil || res.IsError || len(res.Content) == 0 || !strings.Contains(mcp.GetTextFromContent(res.Content[0]), "func Submit()") {
		t.Fatalf("MCP app API: %v %+v", err, res)
	}
	resource, err := c.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: "lidza://api/./internal/orders"}})
	if err != nil || len(resource.Contents) == 0 {
		t.Fatalf("internal resource: %v %+v", err, resource)
	}
	text, ok := resource.Contents[0].(mcp.TextResourceContents)
	if !ok || !strings.Contains(text.Text, "func Submit()") {
		t.Fatalf("internal resource text: %+v", resource.Contents)
	}
	// One instructions file; Claude Code and Gemini CLI import it.
	for _, name := range []string{"CLAUDE.md", "GEMINI.md"} {
		if body := mustRead(t, name); body != "@AGENTS.md\n" {
			t.Errorf("%s is not the line @AGENTS.md: %q", name, body)
		}
	}
}

// Two migrations sharing a number, one generated on a checkout behind its
// branch, are an error on the later name (L019).
func TestDuplicateMigrationNumber(t *testing.T) {
	if _, err := os.Stat(filepath.Join(app, "db")); err == nil {
		t.Fatal("the eval app has a db directory; this case assumes none")
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Join(app, "db")) })
	edit(t, map[string]string{
		"db/migrations/0001_create_note.up.sql":    "CREATE TABLE note (id uuid PRIMARY KEY);\n",
		"db/migrations/0001_create_note.down.sql":  "DROP TABLE note;\n",
		"db/migrations/0001_create_label.up.sql":   "CREATE TABLE label (id uuid PRIMARY KEY);\n",
		"db/migrations/0001_create_label.down.sql": "DROP TABLE label;\n",
	})
	expect(t, check(t), expectation{code: "L019", severity: "error", file: "db/migrations/0001_create_note.up.sql", message: "0001_create_label"})
}
