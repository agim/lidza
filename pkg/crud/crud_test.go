package crud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/schema"
)

const src = `enum Status { draft published }

model Post @table("posts") {
  id        uuid    @id @default(uuid())
  title     string  @min(1) @max(200)
  body      text?
  status    Status  @default(draft)
  views     int     @default(0)
  tags      string[]
  meta      json?
  createdAt time    @default(now())
}
`

func TestGenerate(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(src), 0o644)
	os.WriteFile(filepath.Join(root, "sqlc.yaml"), []byte("version: 2\n"), 0o644)
	os.WriteFile(filepath.Join(root, "routes.go"), []byte("package main\n\nimport (\n\t\"github.com/agim/lidza/pkg/router\"\n)\n\nfunc routes(r *router.Router) {\n}\n"), 0o644)

	res, err := Generate(root, Options{Model: "Post", Module: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Registered {
		t.Fatal("routes not registered")
	}
	sql, _ := os.ReadFile(filepath.Join(root, "db/queries/posts.sql"))
	for _, want := range []string{
		"-- name: ListPosts :many\nSELECT * FROM posts ORDER BY created_at DESC LIMIT $1 OFFSET $2;",
		"-- name: GetPost :one\nSELECT * FROM posts WHERE id = $1;",
		"INSERT INTO posts (title, body, status, views, tags, meta) VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;",
		"title = COALESCE(sqlc.narg('title'), title)",
		"WHERE id = sqlc.arg('id') RETURNING *;",
		"-- name: DeletePost :execrows",
	} {
		if !strings.Contains(string(sql), want) {
			t.Errorf("sql missing %q\n%s", want, sql)
		}
	}
	h, _ := os.ReadFile(filepath.Join(root, "handlers/posts.go"))
	for _, want := range []string{
		"func PostRoutes(r *router.Router) {",
		`router.Route(r, "GET /api/v1/posts", listPosts)`,
		`router.Route(r, "PATCH /api/v1/posts/{id}", updatePost)`,
		"func listPosts(ctx context.Context, req *router.Request[router.None]) (schema.PostList, error) {",
		"Status: queries.Status(in.Status),",
		"Views: int32(in.Views),",
		"Meta: []byte(in.Meta),",
		"Views: PtrInt32(in.Views),",
		"Status: (*queries.Status)(in.Status),",
		"Status: schema.Status(row.Status),",
		"Views: int(row.Views),",
		"Meta: json.RawMessage(row.Meta),",
		"CreatedAt: row.CreatedAt,",
	} {
		// The output is gofmt-formatted, so struct fields are aligned.
		if !strings.Contains(collapse(string(h)), want) {
			t.Errorf("handlers missing %q\n%s", want, h)
		}
	}
	sch, _ := os.ReadFile(filepath.Join(root, schema.FileName))
	for _, want := range []string{
		"type CreatePost {\n  title string @min(1) @max(200)\n  body text?\n  status Status\n  views int\n  tags string[]\n  meta json?\n}",
		"type UpdatePost {\n  title string? @min(1) @max(200)",
		"type PostList {\n  items Post[]\n  total int\n}",
	} {
		if !strings.Contains(string(sch), want) {
			t.Errorf("schema missing %q\n%s", want, sch)
		}
	}
	if _, err := schema.Parse(string(sch)); err != nil {
		t.Fatalf("appended schema does not parse: %v", err)
	}
	routes, _ := os.ReadFile(filepath.Join(root, "routes.go"))
	if !strings.Contains(string(routes), "\thandlers.PostRoutes(r)\n") || !strings.Contains(string(routes), `"app/handlers"`) {
		t.Errorf("routes.go:\n%s", routes)
	}
	if _, err := Generate(root, Options{Model: "Post", Module: "app"}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("second run should refuse without --force: %v", err)
	}
	if _, err := Generate(root, Options{Model: "Post", Module: "app", Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, Options{Model: "Status", Module: "app"}); err == nil {
		t.Fatal("enum accepted as resource")
	}
	if plural("Category") != "Categories" || plural("Box") != "Boxes" || plural("Day") != "Days" {
		t.Error("plural")
	}
}

// collapse joins runs of spaces after a colon, undoing gofmt's alignment.
func collapse(s string) string {
	for strings.Contains(s, ":  ") {
		s = strings.ReplaceAll(s, ":  ", ": ")
	}
	return s
}
