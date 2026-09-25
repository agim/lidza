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

	c, err := client.NewInProcessClient(New(dir, &cfg))
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
	}
	for _, want := range []string{"lidza_routes", "lidza_context", "lidza_check", "lidza_logs", "lidza_config"} {
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

	if got := call("lidza_config", nil); !strings.Contains(got, `"template": "react"`) {
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
