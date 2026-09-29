package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/inspect"
	"github.com/agim/lidza/pkg/router"
)

// TestTypeScriptCompiles type-checks generated clients with the compiler
// options of the react template (strict, noUnusedLocals,
// noUnusedParameters): an API without path parameters or schema rules,
// and the one TestTypeScript uses. It needs tsc: examples/notes after
// `npm ci` there, or tsc on PATH.
func TestTypeScriptCompiles(t *testing.T) {
	tsc := findTSC(t)
	opts := templateOptions(t)
	dir := t.TempDir()
	clients := map[string]*inspect.Context{
		"noparams": {
			Operations: []inspect.Operation{
				{ID: "health", Method: "GET", Path: "/api/v1/health", Params: []string{}, Output: "Health", Builtin: true},
				{ID: "ping", Method: "POST", Path: "/api/v1/ping", Params: []string{}},
			},
			Schemas: map[string]any{
				"Health": map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string"}}, "required": []string{"status"}},
			},
		},
		"full": sampleContext(),
	}
	var include []string
	for name, c := range clients {
		writeClient(t, filepath.Join(dir, name), c)
		include = append(include, name)
	}
	runTSC(t, tsc, dir, opts, include)
}

// TestTypeScriptStream runs the generated client in node against
// router.Stream handlers: the events in order, an error event as an
// ApiError with its status, and leaving the loop closing the request.
func TestTypeScriptStream(t *testing.T) {
	tsc := findTSC(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	type title struct {
		Title string `json:"title"`
	}
	closed := make(chan struct{})
	r := router.New()
	router.Stream(r, "POST /api/v1/summarize", func(ctx context.Context, req *router.Request[title], send func(string) error) error {
		for _, s := range []string{req.Body.Title, "b"} {
			if err := send(s); err != nil {
				return err
			}
		}
		return nil
	})
	router.Stream(r, "GET /api/v1/fail", func(ctx context.Context, req *router.Request[router.None], send func(string) error) error {
		if err := send("first"); err != nil {
			return err
		}
		return router.Errorf(409, "conflict")
	})
	router.Stream(r, "GET /api/v1/forever", func(ctx context.Context, req *router.Request[router.None], send func(string) error) error {
		for {
			if err := send("tick"); err != nil {
				close(closed)
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	c := &inspect.Context{
		Operations: []inspect.Operation{
			{ID: "summarize", Method: "POST", Path: "/api/v1/summarize", Params: []string{}, Input: "Title", Output: "SummarizeEvent", Stream: true},
			{ID: "fail", Method: "GET", Path: "/api/v1/fail", Params: []string{}, Output: "SummarizeEvent", Stream: true},
			{ID: "forever", Method: "GET", Path: "/api/v1/forever", Params: []string{}, Output: "SummarizeEvent", Stream: true},
		},
		Schemas: map[string]any{
			"Title":          map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}, "required": []string{"title"}},
			"SummarizeEvent": map[string]any{"type": "string"},
		},
	}
	dir := t.TempDir()
	writeClient(t, filepath.Join(dir, "client"), c)
	opts := templateOptions(t)
	// Emit CommonJS that node runs as is.
	opts["noEmit"] = false
	opts["outDir"] = "js"
	// node16 without "type": "module" emits CommonJS; node10, which
	// commonjs implies, is gone from newer TypeScript (TS5108).
	opts["module"] = "node16"
	opts["moduleResolution"] = "node16"
	opts["rootDir"] = "client"
	opts["verbatimModuleSyntax"] = false
	runTSC(t, tsc, dir, opts, []string{"client"})

	script := `const { api, configure } = require('./js/index.js')
configure({ baseUrl: process.argv[2] })
;(async () => {
  const out = []
  for await (const e of api.summarize({ title: 'a' })) out.push(e)
  let err = ''
  try {
    for await (const e of api.fail()) out.push(e)
  } catch (e) {
    err = e.status + ' ' + e.message
  }
  for await (const e of api.forever()) {
    out.push(e)
    break
  }
  console.log(JSON.stringify({ out, err }))
})()
`
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "main.js", srv.URL)
	cmd.Dir = dir
	res, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, res)
	}
	want := `{"out":["a","b","first","tick"],"err":"409 GET /api/v1/fail: 409 conflict"}`
	if got := strings.TrimSpace(string(res)); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("leaving the loop did not close the request")
	}
}

// TestTypeScriptUpload runs the generated client in node against a
// router.File route in a group with a wildcard prefix: the file goes out
// raw with its type and name, and an over-limit body is an ApiError 413.
func TestTypeScriptUpload(t *testing.T) {
	tsc := findTSC(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	type stored struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
		Text        string `json:"text"`
	}
	r := router.New()
	post := r.Group("/api/v1/posts/{id}")
	router.Route(post, "PUT /api/v1/posts/{id}/image", func(ctx context.Context, req *router.Request[router.File]) (stored, error) {
		data, err := io.ReadAll(req.Body.Body)
		if err != nil {
			return stored{}, err
		}
		return stored{ID: req.Param("id"), Name: req.Body.Name, ContentType: req.Body.ContentType, Text: string(data)}, nil
	}, router.UploadLimit(16))
	srv := httptest.NewServer(r)
	defer srv.Close()

	c := &inspect.Context{
		Operations: []inspect.Operation{
			{ID: "uploadImage", Method: "PUT", Path: "/api/v1/posts/{id}/image", Params: []string{"id"}, Output: "Stored", Upload: true},
		},
		Schemas: map[string]any{
			"Stored": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
		},
	}
	dir := t.TempDir()
	writeClient(t, filepath.Join(dir, "client"), c)
	opts := templateOptions(t)
	opts["noEmit"] = false
	opts["outDir"] = "js"
	opts["module"] = "node16"
	opts["moduleResolution"] = "node16"
	opts["rootDir"] = "client"
	opts["verbatimModuleSyntax"] = false
	runTSC(t, tsc, dir, opts, []string{"client"})

	script := `const { api, configure } = require('./js/index.js')
configure({ baseUrl: process.argv[2] })
;(async () => {
  const file = new File(['hello'], "café (1)'s.txt", { type: 'text/plain' })
  const out = await api.uploadImage({ id: 'a b' }, file)
  const blob = await api.uploadImage({ id: '7' }, new Blob([new Uint8Array([1, 2])]))
  let err = ''
  try {
    await api.uploadImage({ id: '7' }, new Blob(['x'.repeat(17)]))
  } catch (e) {
    err = e.status + ' ' + e.message
  }
  console.log(JSON.stringify({ out, type: blob.contentType, name: blob.name, err }))
})()
`
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "main.js", srv.URL)
	cmd.Dir = dir
	res, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, res)
	}
	want := `{"out":{"id":"a b","name":"café (1)'s.txt","contentType":"text/plain","text":"hello"},"type":"application/octet-stream","name":"","err":"413 PUT /api/v1/posts/7/image: 413 request body larger than 16 bytes"}`
	if got := strings.TrimSpace(string(res)); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// findTSC is LIDZA_TEST_TSC when set (to try another TypeScript),
// examples/notes' tsc, else tsc on PATH.
func findTSC(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("LIDZA_TEST_TSC"); p != "" {
		return p
	}
	root, _ := filepath.Abs("../..")
	tsc := filepath.Join(root, "examples", "notes", "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err == nil {
		return tsc
	}
	p, err := exec.LookPath("tsc")
	if err != nil {
		t.Skip("tsc not found (npm ci in examples/notes)")
	}
	return p
}

// templateOptions are the react template's compiler options without the
// app's own settings (vite's types, the @lidza/client alias).
func templateOptions(t *testing.T) map[string]any {
	t.Helper()
	root, _ := filepath.Abs("../..")
	data, err := os.ReadFile(filepath.Join(root, "templates", "react", "tsconfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	opts := cfg["compilerOptions"].(map[string]any)
	delete(opts, "paths")
	delete(opts, "baseUrl")
	opts["types"] = []string{}
	return opts
}

func writeClient(t *testing.T, dir string, c *inspect.Context) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, content := range TypeScript(c) {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runTSC(t *testing.T, tsc, dir string, opts map[string]any, include []string) {
	t.Helper()
	out, _ := json.Marshal(map[string]any{"compilerOptions": opts, "include": include})
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := exec.Command(tsc, "-p", dir).CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, res)
	}
}
