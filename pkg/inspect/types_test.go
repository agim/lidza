package inspect

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/config"
)

// TestTypedRoutes type-checks a small app against this checkout and
// checks the operations, schemas and OpenAPI output.
func TestTypedRoutes(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	root, _ := filepath.Abs("../..")
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.27\n\nrequire github.com/agim/lidza v0.0.0\n\nreplace github.com/agim/lidza => "+root+"\n")
	write(t, dir, "schema.lidza", "enum Status { draft published }\n\ntype CreatePost {\n  title string @min(1) @max(200)\n  status Status?\n}\n")
	write(t, dir, "schema/schema.go", `package schema

import "github.com/agim/lidza/pkg/validate"

type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
)

type CreatePost struct {
	Title  string  `+"`json:\"title\"`"+`
	Status *Status `+"`json:\"status\"`"+`
}

func (v CreatePost) Validate() error { var e validate.Errors; return e.Result() }
`)
	write(t, dir, "main.go", `package main

import (
	"context"
	"time"

	"demo/schema"
	"github.com/agim/lidza/pkg/router"
)

type Post struct {
	ID        string          `+"`json:\"id\"`"+`
	Title     string          `+"`json:\"title\"`"+`
	Status    schema.Status   `+"`json:\"status\"`"+`
	Body      *string         `+"`json:\"body,omitempty\"`"+`
	Tags      []string        `+"`json:\"tags\"`"+`
	CreatedAt time.Time       `+"`json:\"createdAt\"`"+`
	Counts    map[string]int  `+"`json:\"counts\"`"+`
	Meta      `+"`json:\"-\"`"+`
	secret    string
}

type Meta struct{ Source string `+"`json:\"source\"`"+` }

type PostList struct {
	Items []Post `+"`json:\"items\"`"+`
	Total int64  `+"`json:\"total\"`"+`
}

func routes(r *router.Router) {
	router.Route(r, "GET /api/v1/posts", listPosts)
	router.Route(r, "GET /api/v1/posts/{id}", getPost)
	router.Route(r, "POST /api/v1/posts", createPost)
	router.Route(r, "DELETE /api/v1/posts/{id}", func(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
		return router.None{}, nil
	})
	r.HandleFunc("GET /api/v1/raw", nil)
}

func listPosts(ctx context.Context, req *router.Request[router.None]) (PostList, error) { return PostList{}, nil }
func getPost(ctx context.Context, req *router.Request[router.None]) (Post, error)      { return Post{}, nil }
func createPost(ctx context.Context, req *router.Request[schema.CreatePost]) (Post, error) {
	return Post{}, nil
}

func main() { _ = routes }
`)
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}

	cfg := config.Default("demo", "react")
	c, err := Project(dir, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 0 {
		t.Fatalf("warnings: %v", c.Warnings)
	}
	byID := map[string]Operation{}
	for _, op := range c.Operations {
		byID[op.ID] = op
	}
	if len(c.Operations) != 5 {
		t.Fatalf("operations: %+v", c.Operations)
	}
	if op := byID["getPost"]; op.Method != "GET" || op.Path != "/api/v1/posts/{id}" || op.Params[0] != "id" || op.Input != "" || op.Output != "Post" ||
		op.Handler.Name != "getPost" || op.Handler.File != "main.go" || !strings.HasPrefix(op.Handler.Signature, "func getPost(ctx context.Context, req *router.Request[router.None]) (Post, error)") {
		t.Errorf("getPost: %+v", op)
	}
	if op := byID["createPost"]; op.Input != "CreatePost" || op.Output != "Post" {
		t.Errorf("createPost: %+v", op)
	}
	if op := byID["deleteApiV1PostsId"]; op.Method != "DELETE" || op.Output != "" || op.Input != "" || op.Handler.Name != "func literal" {
		t.Errorf("literal: %+v", op)
	}
	if op := byID["health"]; !op.Builtin || op.Output != "Health" {
		t.Errorf("health: %+v", op)
	}

	post := c.Schemas["Post"].(map[string]any)
	props := post["properties"].(map[string]any)
	if _, ok := props["secret"]; ok {
		t.Error("unexported field leaked")
	}
	if _, ok := props["Meta"]; ok {
		t.Error("json:\"-\" field leaked")
	}
	if props["createdAt"].(map[string]any)["format"] != "date-time" {
		t.Errorf("createdAt: %v", props["createdAt"])
	}
	if props["body"].(map[string]any)["type"].([]any)[1] != "null" {
		t.Errorf("body: %v", props["body"])
	}
	if props["status"].(map[string]any)["$ref"] != "#/components/schemas/Status" {
		t.Errorf("status: %v", props["status"])
	}
	if props["counts"].(map[string]any)["additionalProperties"].(map[string]any)["type"] != "integer" {
		t.Errorf("counts: %v", props["counts"])
	}
	required := post["required"].([]string)
	if strings.Join(required, ",") != "id,title,status,tags,createdAt,counts" {
		t.Errorf("required: %v", required)
	}
	if st := c.Schemas["Status"].(map[string]any); st["enum"].([]string)[1] != "published" {
		t.Errorf("Status enum: %v", st)
	}
	// CreatePost comes from schema.lidza, with its rules.
	if cp := c.Schemas["CreatePost"].(map[string]any); cp["properties"].(map[string]any)["title"].(map[string]any)["maxLength"] != 200.0 {
		t.Errorf("CreatePost should carry schema.lidza rules: %v", cp)
	}
	if h := c.Schemas["Health"].(map[string]any); h["properties"].(map[string]any)["version"] == nil {
		t.Errorf("Health: %v", h)
	}

	doc := OpenAPI(c)
	paths := doc["paths"].(map[string]any)
	post1 := paths["/api/v1/posts"].(map[string]any)["post"].(map[string]any)
	if post1["operationId"] != "createPost" || post1["requestBody"] == nil || post1["responses"].(map[string]any)["422"] == nil {
		t.Errorf("openapi createPost: %v", post1)
	}
	if raw := paths["/api/v1/raw"].(map[string]any)["get"].(map[string]any); raw["operationId"] != "getApiV1Raw" {
		t.Errorf("untyped route in openapi: %v", raw)
	}
	if del := paths["/api/v1/posts/{id}"].(map[string]any)["delete"].(map[string]any); del["responses"].(map[string]any)["204"] == nil {
		t.Errorf("delete: %v", del)
	}
	if _, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)["ValidationError"]; !ok {
		t.Error("ValidationError component missing")
	}

	// A type error yields warnings and still lists what resolved.
	write(t, dir, "broken.go", "package main\n\nvar x int = \"s\"\n")
	c2, err := Project(dir, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Warnings) == 0 || !strings.Contains(c2.Warnings[0], "broken.go") {
		t.Errorf("warnings: %v", c2.Warnings)
	}
	os.Remove(filepath.Join(dir, "broken.go"))
}

func TestOperationID(t *testing.T) {
	for _, c := range []struct{ handler, method, path, want string }{
		{"createPost", "POST", "/api/v1/posts", "createPost"},
		{"handlers.ListPosts", "GET", "/api/v1/posts", "listPosts"},
		{"func literal", "GET", "/api/v1/posts/{id}", "getApiV1PostsId"},
		{"http.NotFoundHandler()", "GET", "/api/v1/x", "getApiV1X"},
		{"func literal", "", "/api/v1/any", "anyApiV1Any"},
		{"func literal", "GET", "/api/v1/files/{path...}", "getApiV1FilesPath"},
	} {
		if got := operationID(c.handler, c.method, c.path); got != c.want {
			t.Errorf("%q %s %s: got %q, want %q", c.handler, c.method, c.path, got, c.want)
		}
	}
}

// A framework pack the app mounts (auth.Mount) registers routes the app
// never writes: the inspector reads them from the pack's source, so the
// client and the route listing carry them.
func TestMountedPackRoutes(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	root, _ := filepath.Abs("../..")
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.27\n\nrequire github.com/agim/lidza v0.0.0\n\nreplace github.com/agim/lidza => "+root+"\n")
	write(t, dir, "main.go", `package main

import (
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/router"
)

func routes(r *router.Router) {
	auth.Mount(r, auth.Options{})
}

func main() {}
`)
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	c, err := Project(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Operation{}
	for _, op := range c.Operations {
		byID[op.ID] = op
	}
	login, ok := byID["authLogin"]
	if !ok || login.Method != "POST" || login.Path != "/api/v1/auth/login" || login.Pack != "auth" || login.Input != "Credentials" || login.Output != "SignedIn" {
		t.Fatalf("authLogin: %+v (ops %v)", login, keys(byID))
	}
	if login.Handler.File != "lidza/packs/auth/signin.go" || login.Handler.Line == 0 {
		t.Fatalf("handler: %+v", login.Handler)
	}
	for _, id := range []string{"authRegister", "authLogout", "authSession", "authMe", "authVerify", "authForgot", "authReset", "authPassword", "authProviders"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("operation %s missing", id)
		}
	}
	if _, ok := c.Schemas["SignedIn"]; !ok {
		t.Errorf("schema SignedIn missing: %v", keys(c.Schemas))
	}
	var raw []string
	for _, r := range c.Routes {
		if r.Pack == "auth" && !r.Typed {
			raw = append(raw, r.Pattern)
		}
	}
	if fmt.Sprint(raw) != "[GET /api/v1/auth/{provider}/start GET /api/v1/auth/{provider}/callback]" {
		t.Errorf("raw pack routes: %v", raw)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
