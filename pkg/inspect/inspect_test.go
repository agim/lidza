package inspect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProject(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.27\n")
	write(t, dir, "routes.go", `package main

import (
	"net/http"

	"demo/handlers"
	"github.com/agim/lidza/pkg/router"
)

func routes(r *router.Router) {
	r.HandleFunc("GET /api/v1/hello/{name}", func(w http.ResponseWriter, req *http.Request) {})
	r.HandleFunc("POST /api/v1/posts", createPost)
	r.HandleFunc("/api/v1/any", handlers.Any)
	r.Handle("GET /api/v1/static", http.NotFoundHandler())
	r.HandleFunc(dynamic, createPost)
}

func createPost(w http.ResponseWriter, r *http.Request) {}

var dynamic = "GET /api/v1/dynamic"
`)
	write(t, dir, "routes_test.go", "package main\n\nfunc init() { _ = 0 }\n")
	write(t, dir, "handlers/any.go", "package handlers\n\nimport \"net/http\"\n\n// Any handles every method.\nfunc Any(w http.ResponseWriter, r *http.Request) {}\n")
	write(t, dir, "node_modules/x/x.go", "package x\n\nfunc F() { r.HandleFunc(\"GET /ignored\", nil) }\n")
	write(t, dir, "core/Cargo.toml", "[package]\nname = \"demo-core\"\nversion = \"0.1.0\"\n")
	write(t, dir, "core/src/lib.rs", `pub fn not_exported() {}

#[unsafe(no_mangle)]
pub extern "C" fn abi_version() -> u32 {
    1
}

#[no_mangle]
pub unsafe extern "C" fn sum(
    a: u32,
    b: u32,
) -> u32 { a + b }

pub extern "C" fn mangled() {}
`)

	cfg := config.Default("demo", "react")
	c, err := Project(dir, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.App.Name != "demo" || c.App.Module != "demo" || c.App.Template != "react" {
		t.Fatalf("app: %+v", c.App)
	}

	byPattern := map[string]Route{}
	for _, r := range c.Routes {
		byPattern[r.Pattern] = r
	}
	if len(c.Routes) != 5 {
		t.Fatalf("routes: %d %+v", len(c.Routes), c.Routes)
	}
	if r := byPattern["GET /api/v1/health"]; !r.Builtin || r.Method != "GET" || r.Path != "/api/v1/health" {
		t.Errorf("builtin: %+v", r)
	}
	if r := byPattern["GET /api/v1/hello/{name}"]; r.Handler.Name != "func literal" || r.Handler.File != "routes.go" || r.Handler.Line != 11 ||
		r.Handler.Signature != "func(w http.ResponseWriter, req *http.Request)" {
		t.Errorf("literal: %+v", r.Handler)
	}
	if r := byPattern["POST /api/v1/posts"]; r.Handler.Name != "createPost" || r.Handler.Line != 18 ||
		r.Handler.Signature != "func createPost(w http.ResponseWriter, r *http.Request)" {
		t.Errorf("named: %+v", r.Handler)
	}
	if r := byPattern["/api/v1/any"]; r.Method != "" || r.Handler.Name != "handlers.Any" || r.Handler.File != "handlers/any.go" || r.Handler.Line != 6 {
		t.Errorf("cross-package: %+v", r)
	}
	if r := byPattern["GET /api/v1/static"]; r.Handler.Name != "http.NotFoundHandler()" {
		t.Errorf("call handler: %+v", r.Handler)
	}

	if c.Rust == nil || c.Rust.Crate != "demo-core" || c.Rust.Dir != "core" {
		t.Fatalf("rust: %+v", c.Rust)
	}
	if len(c.Rust.Exports) != 2 {
		t.Fatalf("exports: %+v", c.Rust.Exports)
	}
	if e := c.Rust.Exports[0]; e.Name != "abi_version" || e.Line != 4 || e.File != "core/src/lib.rs" || e.Signature != `pub extern "C" fn abi_version() -> u32` {
		t.Errorf("export 0: %+v", e)
	}
	if e := c.Rust.Exports[1]; e.Name != "sum" || e.Signature != `pub unsafe extern "C" fn sum( a: u32, b: u32, ) -> u32` {
		t.Errorf("export 1: %+v", e)
	}

	if _, err := Write(dir, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}

	short, full := LLMS(c, &cfg, "# Guide\n\nRules.")
	for _, want := range []string{"# demo", "GET /api/v1/health (built in)", "GET /api/v1/hello/{name}: routes.go:11", "POST /api/v1/posts: createPost (routes.go:18)", "ANY /api/v1/any: handlers.Any", "/llms-full.txt"} {
		if !strings.Contains(short, want) {
			t.Errorf("llms.txt missing %q:\n%s", want, short)
		}
	}
	for _, want := range []string{"# Guide", "Rules.", "`func createPost(w http.ResponseWriter, r *http.Request)` at routes.go:18", "## Rust crate demo-core (core)", "`pub extern \"C\" fn abi_version() -> u32` at core/src/lib.rs:4", "template: react"} {
		if !strings.Contains(full, want) {
			t.Errorf("llms-full.txt missing %q:\n%s", want, full)
		}
	}
}

func TestSplitPattern(t *testing.T) {
	for in, want := range map[string][2]string{
		"GET /api/x":     {"GET", "/api/x"},
		"/api/x":         {"", "/api/x"},
		"DELETE /a/{id}": {"DELETE", "/a/{id}"},
		"/with space/x":  {"", "/with space/x"},
	} {
		m, p := splitPattern(in)
		if m != want[0] || p != want[1] {
			t.Errorf("%q: got %q %q", in, m, p)
		}
	}
}
