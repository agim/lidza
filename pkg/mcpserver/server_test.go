package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/inspect"
)

func TestServer(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.27\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "routes.go"), []byte(`package main

import "net/http"

func routes(r interface{ HandleFunc(string, http.HandlerFunc) }) {
	r.HandleFunc("GET /api/v1/hello/{name}", hello)
}

func hello(w http.ResponseWriter, r *http.Request) {}

func main() {}
`), 0o644)
	os.MkdirAll(filepath.Join(dir, ".lidza"), 0o755)
	os.WriteFile(filepath.Join(dir, LogFile), []byte("[lidza] app: http://127.0.0.1:3000\n[app] listening\n[web] vite ready\n[app] GET /api/v1/hello/x 200\n"), 0o644)
	cfg := config.Default("demo", "react")

	c, err := client.NewInProcessClient(New(dir, &cfg).MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
		// A client asks before a tool that can lose data; the others
		// say they are read-only or additive.
		want := map[string][2]bool{
			"lidza_db_rollback": {false, true},
			"lidza_db_migrate":  {false, true},
			"lidza_db_status":   {true, false},
			"lidza_doctor":      {true, false},
			"lidza_gen":         {false, false},
			"lidza_ship":        {false, false},
		}
		if w, ok := want[tl.Name]; ok {
			a := tl.Annotations
			if a.ReadOnlyHint == nil || a.DestructiveHint == nil || *a.ReadOnlyHint != w[0] || *a.DestructiveHint != w[1] {
				t.Errorf("%s: annotations %+v, want readOnly %v, destructive %v", tl.Name, a, w[0], w[1])
			}
		}
	}
	for _, want := range []string{"lidza_routes", "lidza_context", "lidza_check", "lidza_logs", "lidza_config", "lidza_gen", "lidza_gen_resource", "lidza_pack_add", "lidza_db_migrate", "lidza_test", "lidza_verify", "lidza_build", "lidza_ship", "lidza_doctor", "lidza_recipes", "lidza_decision_add", "lidza_brief", "lidza_brief_answer", "lidza_brief_skip", "lidza_note_add", "lidza_credentials_set", "lidza_credentials_list"} {
		if !names[want] {
			t.Errorf("tool %s missing; have %v", want, names)
		}
	}

	call := func(name string, args map[string]any) string {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args
		res, err := c.CallTool(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s: tool error: %s", name, mcp.GetTextFromContent(res.Content[0]))
		}
		return mcp.GetTextFromContent(res.Content[0])
	}

	var routes []inspect.Route
	if err := json.Unmarshal([]byte(call("lidza_routes", nil)), &routes); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[1].Pattern != "GET /api/v1/hello/{name}" || routes[1].Handler.Name != "hello" || routes[1].Handler.File != "routes.go" {
		t.Fatalf("routes: %+v", routes)
	}

	var cx inspect.Context
	if err := json.Unmarshal([]byte(call("lidza_context", nil)), &cx); err != nil {
		t.Fatal(err)
	}
	if cx.App.Name != "demo" || cx.App.Module != "demo" {
		t.Fatalf("context: %+v", cx.App)
	}

	logs := call("lidza_logs", map[string]any{"lines": 2})
	if logs != "[web] vite ready\n[app] GET /api/v1/hello/x 200" {
		t.Fatalf("logs: %q", logs)
	}
	if got := call("lidza_logs", map[string]any{"filter": "[app]"}); strings.Contains(got, "[web]") || !strings.Contains(got, "listening") {
		t.Fatalf("filtered logs: %q", got)
	}

	if got := call("lidza_config", nil); !strings.Contains(got, `"template":"react"`) {
		t.Fatalf("config: %s", got)
	}

	res, err := c.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: "lidza://llms.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if txt, ok := res.Contents[0].(mcp.TextResourceContents); !ok || !strings.Contains(txt.Text, "GET /api/v1/hello/{name}: hello") {
		t.Fatalf("llms.txt: %+v", res.Contents)
	}

}

// TestAgentDocs covers lidza_api, lidza://api and the recipe prompts on a
// project that depends on a local checkout of the framework.
func TestAgentDocs(t *testing.T) {
	dir := t.TempDir()
	root, _ := filepath.Abs("../..")
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.27\n\nrequire github.com/agim/lidza v0.0.0\n\nreplace github.com/agim/lidza => "+root+"\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "lidza-guide.md"), []byte("# Guide\n\n## Recipes\n\n### Add an API route\n\nExpose an operation.\n\n1. Declare the shapes.\n"), 0o644)
	c, err := client.NewInProcessClient(New(dir, nil).MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any) string {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args
		res, err := c.CallTool(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s: tool error: %s", name, mcp.GetTextFromContent(res.Content[0]))
		}
		return mcp.GetTextFromContent(res.Content[0])
	}
	if got := call("lidza_api", map[string]any{"package": "pkg/router", "filter": "notfound"}); !strings.Contains(got, "func NotFound(") || strings.Contains(got, "func Route[") {
		t.Fatalf("lidza_api: %s", got)
	}
	res, err := c.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: "lidza://api/packs/auth"}})
	if err != nil {
		t.Fatal(err)
	}
	if txt, ok := res.Contents[0].(mcp.TextResourceContents); !ok || !strings.Contains(txt.Text, "func Require(") {
		t.Fatalf("lidza://api/packs/auth: %+v", res.Contents)
	}
	// Without arguments, the list, never every package rendered; a filter
	// alone searches them all.
	if got := call("lidza_api", nil); !strings.Contains(got, "pkg/router") || strings.Contains(got, "func Require(") || !strings.Contains(got, "with filter") {
		t.Fatalf("lidza_api without arguments: %.500s", got)
	}
	if got := call("lidza_api", map[string]any{"filter": "notfound"}); !strings.Contains(got, "func NotFound(") || strings.Contains(got, "func Require(") {
		t.Fatalf("lidza_api filter alone: %.500s", got)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = "lidza_api"
	req.Params.Arguments = map[string]any{"package": "pkg/orm"}
	if r, err := c.CallTool(ctx, req); err != nil || !r.IsError || !strings.Contains(mcp.GetTextFromContent(r.Content[0]), "no package") {
		t.Fatalf("missing package: %v %+v", err, r)
	}

	if got := call("lidza_snippet", map[string]any{"name": "routes"}); !strings.HasPrefix(got, "From examples/notes/app/routes.go") || !strings.Contains(got, "auth.Require()") {
		t.Fatalf("lidza_snippet: %s", got)
	}
	if got := call("lidza_snippet", nil); !strings.Contains(got, "handler-test (tests/routes_test.go)") {
		t.Fatalf("snippet catalog: %s", got)
	}

	if got := call("lidza_recipes", nil); !strings.Contains(got, `"Name":"add-api-route"`) || !strings.Contains(got, `"Scope":"framework"`) {
		t.Fatalf("lidza_recipes: %s", got)
	}

	prompts, err := c.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil || len(prompts.Prompts) != 1 || prompts.Prompts[0].Name != "add-api-route" || prompts.Prompts[0].Description != "Expose an operation." {
		t.Fatalf("prompts: %v %+v", err, prompts)
	}
	pr := mcp.GetPromptRequest{}
	pr.Params.Name = "add-api-route"
	pr.Params.Arguments = map[string]string{"task": "POST /api/v1/things"}
	got, err := c.GetPrompt(ctx, pr)
	if err != nil {
		t.Fatal(err)
	}
	if text := mcp.GetTextFromContent(got.Messages[0].Content); !strings.HasPrefix(text, "# Add an API route\n\nExpose an operation.\n\n1. Declare the shapes.") || !strings.HasSuffix(text, "Task: POST /api/v1/things") {
		t.Fatalf("prompt: %q", text)
	}
}

func TestTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	if _, err := tail(p, 10, ""); err == nil {
		t.Fatal("missing file should error")
	}
	os.WriteFile(p, []byte("a\nb\nc\n"), 0o644)
	if got, _ := tail(p, 2, ""); got != "b\nc" {
		t.Fatalf("got %q", got)
	}
	if got, _ := tail(p, 0, ""); got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
	os.WriteFile(p, nil, 0o644)
	if got, _ := tail(p, 5, ""); !strings.HasPrefix(got, "(empty") {
		t.Fatalf("got %q", got)
	}
}

// TestRefresh: a recipe recorded in the guide and a pack enabled in
// lidza.json show up in the prompt and tool lists without a restart.
func TestRefresh(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.27\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "routes.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	cfg := config.Default("demo", "react")
	if err := cfg.Save(dir); err != nil {
		t.Fatal(err)
	}
	srv := New(dir, &cfg)
	c, err := client.NewInProcessClient(srv.MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	names := func() (tools, prompts map[string]bool) {
		tools, prompts = map[string]bool{}, map[string]bool{}
		tl, _ := c.ListTools(ctx, mcp.ListToolsRequest{})
		for _, x := range tl.Tools {
			tools[x.Name] = true
		}
		pl, _ := c.ListPrompts(ctx, mcp.ListPromptsRequest{})
		for _, x := range pl.Prompts {
			prompts[x.Name] = true
		}
		return
	}
	tools, prompts := names()
	if tools["lidza_llm"] || prompts["add-thing"] {
		t.Fatal("present before the change")
	}
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "lidza-guide.md"), []byte("# demo\n\n## Recipes\n\n### Add a thing\n\nHow things are added.\n\n1. Add it.\n\n## App recipes\n"), 0o644)
	cfg.Packs = []string{"lidza/db", "lidza/llm"}
	if err := cfg.Save(dir); err != nil {
		t.Fatal(err)
	}
	srv.Refresh()
	tools, prompts = names()
	if !tools["lidza_llm"] || !prompts["add-thing"] {
		t.Fatalf("after refresh: llm tool %v, prompt %v", tools["lidza_llm"], prompts["add-thing"])
	}
	// Refresh again with nothing changed keeps them, once.
	srv.Refresh()
	pl, _ := c.ListPrompts(ctx, mcp.ListPromptsRequest{})
	n := 0
	for _, p := range pl.Prompts {
		if p.Name == "add-thing" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("prompt listed %d times", n)
	}
}

// Once the CLI on disk changes (lidza update), every tool refuses with
// the reconnect instruction instead of answering from the old release.
func TestStaleServer(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "lidza")
	os.WriteFile(exe, []byte("v1"), 0o755)
	old := executable
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = old })
	c, err := client.NewInProcessClient(New(t.TempDir(), nil).MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = "lidza_snippet"
	if res, err := c.CallTool(ctx, req); err != nil || res.IsError {
		t.Fatalf("before the update: %v %+v", err, res)
	}
	os.WriteFile(exe, []byte("v2, a longer binary"), 0o755)
	res, err := c.CallTool(ctx, req)
	if err != nil || !res.IsError || !strings.Contains(mcp.GetTextFromContent(res.Content[0]), "reconnect") {
		t.Fatalf("after the update: %v %+v", err, res)
	}
}

// Every framework tool says what it does: only the three that can lose
// data are destructive (an unannotated tool counts as destructive, so a
// new tool without a hint fails here), and the readers say so.
func TestToolHints(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.27\n"), 0o644)
	cfg := config.Default("demo", "react")
	cfg.Packs = []string{"lidza/db", "lidza/analytics", "lidza/mail", "lidza/llm", "lidza/storage"}
	c, err := client.NewInProcessClient(New(dir, &cfg).MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	destructive := map[string]bool{"lidza_db_migrate": true, "lidza_db_rollback": true, "lidza_credentials_set": true}
	seen := map[string]bool{}
	for _, tl := range tools.Tools {
		if !builtin(tl.Name) {
			continue
		}
		seen[tl.Name] = true
		a := tl.Annotations
		if a.DestructiveHint == nil || a.ReadOnlyHint == nil {
			t.Errorf("%s: no hints", tl.Name)
			continue
		}
		if *a.DestructiveHint != destructive[tl.Name] {
			t.Errorf("%s: destructive %v", tl.Name, *a.DestructiveHint)
		}
		if h, ok := hints[tl.Name]; ok && *a.ReadOnlyHint != h.readOnly {
			t.Errorf("%s: read-only %v", tl.Name, *a.ReadOnlyHint)
		}
	}
	for _, name := range []string{"lidza_api", "lidza_snippet", "lidza_routes", "lidza_errors", "lidza_llm", "lidza_storage", "lidza_mail"} {
		if !seen[name] {
			t.Errorf("%s not listed", name)
		}
	}
}

// A whole large package comes back as its signatures; a filter brings
// the docs.
func TestAPIBudget(t *testing.T) {
	root, _ := filepath.Abs("../..")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.27\n\nrequire github.com/agim/lidza v0.0.0\n\nreplace github.com/agim/lidza => "+root+"\n"), 0o644)
	cfg := config.Default("demo", "react")
	c, err := client.NewInProcessClient(New(dir, &cfg).MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) string {
		req := mcp.CallToolRequest{}
		req.Params.Name, req.Params.Arguments = "lidza_api", args
		res, err := c.CallTool(ctx, req)
		if err != nil || res.IsError {
			t.Fatalf("%v: %v %+v", args, err, res)
		}
		return res.Content[0].(mcp.TextContent).Text
	}
	whole := call(map[string]any{"package": "packs/auth"})
	if len(whole) > apiBudget || !strings.Contains(whole, "these are its signatures") || !strings.Contains(whole, "func (r Roles) Check(") {
		t.Fatalf("whole package: %d chars", len(whole))
	}
	if one := call(map[string]any{"package": "packs/auth", "filter": "Roles"}); !strings.Contains(one, "Every check reads the database") {
		t.Fatal("filter lost the docs")
	}
	if small := call(map[string]any{"package": "pkg/secrets"}); strings.Contains(small, "signatures") || !strings.Contains(small, "Package secrets") {
		t.Fatalf("small package: %s", small)
	}
}
