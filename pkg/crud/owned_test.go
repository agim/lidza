package crud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
)

// A product belongs to the user who created it (ownerId); a tag is
// public; a category has no owner.
const ownedSrc = `model Product {
  id        uuid   @id @default(uuid())
  ownerId   uuid   @index
  name      string @min(1) @max(200)
  price     int?
  createdAt time   @default(now())
}

model Tag {
  id      uuid   @id @default(uuid())
  ownerId uuid
  name    string
}
`

const routesSrc = "package main\n\nimport (\n\t\"github.com/agim/lidza/pkg/router\"\n)\n\nfunc routes(r *router.Router) {\n}\n"

func ownedApp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(ownedSrc), 0o644)
	os.WriteFile(filepath.Join(root, "sqlc.yaml"), []byte("version: 2\n"), 0o644)
	os.WriteFile(filepath.Join(root, "routes.go"), []byte(routesSrc), 0o644)
	return root
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestGenerateOwned: every statement of an owned model filters by the
// owner or sets it, the handlers take it from auth.CurrentUser, the
// Create and Update types leave it out, and the routes are behind
// auth.Require().
func TestGenerateOwned(t *testing.T) {
	root := ownedApp(t)
	res, err := Generate(root, Options{Model: "Product", Module: "app", Auth: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Owner != "ownerId" || res.Public || !res.Registered {
		t.Fatalf("result %+v", res)
	}
	sql := read(t, root, "db/queries/product.sql")
	for _, want := range []string{
		"-- name: ListProducts :many\nSELECT * FROM product\nWHERE owner_id = sqlc.arg('owner_id')\n",
		"-- name: CountProducts :one\nSELECT count(*) FROM product\nWHERE owner_id = sqlc.arg('owner_id')\n",
		"-- name: GetProduct :one\nSELECT * FROM product WHERE id = $1 AND owner_id = $2;",
		"-- name: CreateProduct :one\nINSERT INTO product (owner_id, name, price) VALUES ($1, $2, $3) RETURNING *;",
		"WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id') RETURNING *;",
		"-- name: DeleteProduct :execrows\nDELETE FROM product WHERE id = $1 AND owner_id = $2;",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("sql missing %q\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "owner_id = COALESCE") {
		t.Errorf("update sets the owner from the body:\n%s", sql)
	}
	h := collapse(read(t, root, "handlers/product.go"))
	for _, want := range []string{
		`"github.com/agim/lidza/packs/auth"`,
		"owner := auth.CurrentUser(ctx).ID",
		"q.ListProducts(ctx, queries.ListProductsParams{OwnerID: owner, Q: p.Search(), Since: p.Since, Until: p.Until, Sort: p.Sort, Desc: p.Desc, Lim: p.Limit, Off: p.Offset})",
		"q.CountProducts(ctx, queries.CountProductsParams{OwnerID: owner, Q: p.Search(), Since: p.Since, Until: p.Until})",
		"GetProduct(ctx, queries.GetProductParams{ID: id, OwnerID: auth.CurrentUser(ctx).ID})",
		"DeleteProduct(ctx, queries.DeleteProductParams{ID: id, OwnerID: auth.CurrentUser(ctx).ID})",
		"CreateProductParams{\n\t\tOwnerID: auth.CurrentUser(ctx).ID,\n",
		"UpdateProductParams{\n\t\tID: id,\n\t\tOwnerID: auth.CurrentUser(ctx).ID,\n",
		`router.NotFound("product")`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("handlers missing %q\n%s", want, h)
		}
	}
	if strings.Contains(h, "in.OwnerID") {
		t.Errorf("the owner comes from the body:\n%s", h)
	}
	src := read(t, root, schema.FileName)
	for _, want := range []string{
		"type CreateProduct {\n  name string @min(1) @max(200)\n  price int?\n}",
		"type UpdateProduct {\n  name string? @min(1) @max(200)\n  price int?\n}",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("schema missing %q\n%s", want, src)
		}
	}
	routes := read(t, root, "routes.go")
	for _, want := range []string{
		"\tproducts := r.Group(\"/api/v1/products\", auth.Require())\n\thandlers.ProductRoutes(products)\n",
		"\t\"github.com/agim/lidza/packs/auth\"\n\t\"github.com/agim/lidza/pkg/router\"\n",
		"\t\"app/handlers\"\n",
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("routes.go missing %q\n%s", want, routes)
		}
	}
	// A second resource joins the imports once.
	if _, err := Generate(root, Options{Model: "Tag", Module: "app", Auth: true}); err != nil {
		t.Fatal(err)
	}
	routes = read(t, root, "routes.go")
	if strings.Count(routes, "packs/auth") != 1 || !strings.Contains(routes, "handlers.TagRoutes(tags)") {
		t.Errorf("routes.go:\n%s", routes)
	}
}

// TestGenerateOwnedStaleRoutes: --force moves a registration written
// before resources were signed-in behind auth.Require() and names the
// input types that still take the owner.
func TestGenerateOwnedStaleRoutes(t *testing.T) {
	root := ownedApp(t)
	os.WriteFile(filepath.Join(root, "routes.go"), []byte(strings.Replace(routesSrc, "{\n}", "{\n\thandlers.ProductRoutes(r)\n}", 1)), 0o644)
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(ownedSrc+"\ntype CreateProduct {\n  ownerId uuid\n  name string\n}\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "handlers"), 0o755)
	os.WriteFile(filepath.Join(root, "handlers/product.go"), []byte("package handlers\n"), 0o644)
	if _, err := Generate(root, Options{Model: "Product", Module: "app", Auth: true}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("without --force: %v", err)
	}
	if strings.Contains(read(t, root, "routes.go"), "auth.Require") {
		t.Fatal("routes.go changed without --force")
	}
	res, err := Generate(root, Options{Model: "Product", Module: "app", Auth: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	routes := read(t, root, "routes.go")
	if strings.Contains(routes, "ProductRoutes(r)") || !strings.Contains(routes, "handlers.ProductRoutes(products)") {
		t.Errorf("routes.go:\n%s", routes)
	}
	if strings.Join(res.StaleInputs, ",") != "CreateProduct" {
		t.Errorf("stale inputs %v", res.StaleInputs)
	}
}

// TestGeneratePublic: --public marks the model @public; its queries are
// unscoped and its routes open, and the auth pack is not needed.
func TestGeneratePublic(t *testing.T) {
	root := ownedApp(t)
	if _, err := Generate(root, Options{Model: "Tag", Module: "app"}); err == nil || !strings.Contains(err.Error(), "lidza pack add auth") || !strings.Contains(err.Error(), "--public") {
		t.Fatalf("without the auth pack: %v", err)
	}
	res, err := Generate(root, Options{Model: "Tag", Module: "app", Public: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Public || res.Owner != "" {
		t.Fatalf("result %+v", res)
	}
	src := read(t, root, schema.FileName)
	if !strings.Contains(src, `model Tag @public {`) {
		t.Errorf("not marked @public:\n%s", src)
	}
	s, err := schema.Parse(src)
	if err != nil || !s.Model("Tag").Public {
		t.Fatalf("schema: %v", err)
	}
	if !strings.Contains(src, "type CreateTag {\n  ownerId uuid\n  name string\n}") {
		t.Errorf("a public model keeps ownerId as input:\n%s", src)
	}
	if sql := read(t, root, "db/queries/tag.sql"); !strings.Contains(sql, "SELECT * FROM tag WHERE id = $1;") {
		t.Errorf("sql:\n%s", sql)
	}
	routes := read(t, root, "routes.go")
	if !strings.Contains(routes, "\thandlers.TagRoutes(r)\n") || strings.Contains(routes, "auth") {
		t.Errorf("routes.go:\n%s", routes)
	}
	// Marked once: regenerating without the flag stays public.
	if _, err := Generate(root, Options{Model: "Tag", Module: "app", Force: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(read(t, root, schema.FileName), "@public") != 1 {
		t.Error("@public added twice")
	}
}

// TestGenerateOwnerType: the owner holds the user's id, a string.
func TestGenerateOwnerType(t *testing.T) {
	root := ownedApp(t)
	os.WriteFile(filepath.Join(root, schema.FileName), []byte("model Product {\n  id int @id @default(autoincrement())\n  ownerId int\n  name string\n}\n"), 0o644)
	if _, err := Generate(root, Options{Model: "Product", Module: "app", Auth: true}); err == nil || !strings.Contains(err.Error(), "@public") {
		t.Fatalf("int owner: %v", err)
	}
}

// TestGenerateOwnedCompiles: with sqlc, the owned handlers compile
// against the generated queries; with the test database, another user's
// row is a 404 on get, update and delete and absent from the list.
func TestGenerateOwnedCompiles(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(ownedSrc), 0o644)
	if _, _, err := pack.Add(root, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, Options{Model: "Product", Module: "app", Auth: true}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "handlers/owned_test.go"), []byte(ownedHandlersTest), 0o644)
	compileApp(t, root, "product.go", "./handlers/")
}

// ownedHandlersTest runs in the generated app: two users against the
// generated routes, the user set by a header in place of a session.
const ownedHandlersTest = `package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/router"
)

func TestOwnedRows(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres:///lidza_test?host=/var/run/postgresql"
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.Config{URL: url, MaxConns: 2, ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	defer pool.Close()
	ddl, err := os.ReadFile("../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, "DROP TABLE IF EXISTS product, tag")
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DROP TABLE IF EXISTS product, tag")

	services := lidza.NewServices()
	lidza.Provide(services, &db.DB{Pool: pool})
	signedIn := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := lidza.WithServices(r.Context(), services)
			ctx = auth.WithUser(ctx, &auth.User{ID: r.Header.Get("X-User")})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	r := router.New()
	ProductRoutes(r.Group("/api/v1/products", signedIn))
	h := r.Handler()
	call := func(user, method, path, body string, out any) int {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User", user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if out != nil {
			json.Unmarshal(w.Body.Bytes(), out)
		}
		return w.Code
	}
	const alice, bob = "00000000-0000-0000-0000-00000000000a", "00000000-0000-0000-0000-00000000000b"
	var p struct {
		ID      string
		OwnerID string
	}
	// The body cannot name another owner: the type has no ownerId.
	if code := call(alice, "POST", "/api/v1/products", ` + "`" + `{"name": "lamp"}` + "`" + `, &p); code != http.StatusCreated || p.OwnerID != alice {
		t.Fatalf("create: %d %+v", code, p)
	}
	if code := call(alice, "GET", "/api/v1/products/"+p.ID, "", nil); code != http.StatusOK {
		t.Fatalf("owner get: %d", code)
	}
	for _, c := range []struct{ method, body string }{{"GET", ""}, {"PATCH", ` + "`" + `{"name": "mine"}` + "`" + `}, {"DELETE", ""}} {
		if code := call(bob, c.method, "/api/v1/products/"+p.ID, c.body, nil); code != http.StatusNotFound {
			t.Errorf("another user's %s: %d, want 404", c.method, code)
		}
	}
	var list struct{ Total int }
	if code := call(bob, "GET", "/api/v1/products", "", &list); code != http.StatusOK || list.Total != 0 {
		t.Errorf("another user's list: %d %+v", code, list)
	}
	if code := call(alice, "GET", "/api/v1/products", "", &list); code != http.StatusOK || list.Total != 1 {
		t.Errorf("owner list: %d %+v", code, list)
	}
	if code := call(alice, "DELETE", "/api/v1/products/"+p.ID, "", nil); code >= 300 {
		t.Errorf("owner delete: %d", code)
	}
}
`

// TestGenerateShared: --shared marks the model @shared; its routes are
// behind sign-in and its queries are not scoped, owner-like field or not.
func TestGenerateShared(t *testing.T) {
	root := ownedApp(t)
	res, err := Generate(root, Options{Model: "Product", Module: "app", Auth: true, Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Shared || res.Owner != "" {
		t.Fatalf("result: %+v", res)
	}
	if src := read(t, root, schema.FileName); !strings.Contains(src, "model Product @shared {") {
		t.Fatalf("not marked:\n%s", src)
	}
	if q := read(t, root, "db/queries/product.sql"); strings.Contains(q, "owner_id = $") {
		t.Errorf("shared queries are scoped:\n%s", q)
	}
	if routes := read(t, root, "routes.go"); !strings.Contains(routes, "auth.Require()") {
		t.Errorf("shared routes not behind sign-in:\n%s", routes)
	}
	if _, err := Generate(ownedApp(t), Options{Model: "Product", Module: "app", Auth: true, Shared: true, Public: true}); err == nil {
		t.Error("--public with --shared accepted")
	}
}

// TestGenerateWorkspace: a model with workspaceId is scoped to the
// request's workspace, not the user: every statement takes it, the
// handlers read auth.WorkspaceID, and the routes sit behind
// auth.RequireWorkspace(). workspaceId wins over ownerId.
func TestGenerateWorkspace(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(`model Project {
  id          uuid   @id @default(uuid())
  workspaceId uuid   @index
  ownerId     uuid
  name        string @min(1)
  createdAt   time   @default(now())
}
`), 0o644)
	os.WriteFile(filepath.Join(root, "sqlc.yaml"), []byte("version: 2\n"), 0o644)
	os.WriteFile(filepath.Join(root, "routes.go"), []byte(routesSrc), 0o644)
	res, err := Generate(root, Options{Model: "Project", Module: "app", Auth: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Owner != "workspaceId" || !res.Workspace {
		t.Fatalf("result %+v", res)
	}
	sql := read(t, root, "db/queries/project.sql")
	for _, want := range []string{"Scoped to the request's workspace", "WHERE id = $1 AND workspace_id = $2", "workspace_id = sqlc.arg('workspace_id')"} {
		if !strings.Contains(sql, want) {
			t.Errorf("sql lacks %q:\n%s", want, sql)
		}
	}
	h := read(t, root, "handlers/project.go")
	if !strings.Contains(h, "auth.WorkspaceID(ctx)") || strings.Contains(h, "auth.CurrentUser(ctx).ID") {
		t.Errorf("handlers do not take the workspace:\n%s", h)
	}
	if routes := read(t, root, "routes.go"); !strings.Contains(routes, `r.Group("/api/v1/projects", auth.RequireWorkspace())`) {
		t.Errorf("routes:\n%s", routes)
	}
}
