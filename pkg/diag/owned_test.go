package diag

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOwnedQueries(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	write("schema.lidza", `model User @table("app_user") {
  id uuid @id
}
model Post {
  id       uuid @id
  authorId uuid @ref(User)
  title    string
}
model Product @table("products") {
  id      uuid @id
  ownerId uuid
  name    string
}
model Tag @public {
  id      uuid @id
  ownerId uuid
}
model Category {
  id   uuid @id
  name string
}
`)
	write("db/queries/post.sql", `-- Queries for Post.

-- name: ListPosts :many
SELECT * FROM post WHERE author_id = $1 ORDER BY id LIMIT $2;

-- name: CountPosts :one
SELECT count(*) FROM post;

-- name: CreatePost :one
INSERT INTO post (author_id, title) VALUES ($1, $2) RETURNING *;

-- name: UpdatePost :one
UPDATE post SET title = $2
WHERE id = $1 AND author_id = $3 RETURNING *;

-- name: StealPost :one
UPDATE post SET author_id = $2 WHERE id = $1 RETURNING *;

-- An admin page lists every user's posts.
-- lidza:ignore L018
-- name: AdminPosts :many
SELECT p.id, u.id FROM post p JOIN app_user u ON u.id = p.author_id;
`)
	write("db/queries/products.sql", `-- name: GetProduct :one
SELECT * FROM "products" WHERE id = $1; -- owner_id in a comment does not count

-- name: DeleteProduct :execrows
DELETE FROM products WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id');

-- name: ProductsInCategory :many
SELECT p.* FROM category c JOIN products p ON p.name = c.name WHERE p.owner_id = $1;

-- name: ListTags :many
SELECT * FROM tag;

-- name: ListCategories :many
SELECT * FROM category;
`)
	var got []string
	for _, d := range ownedQueries(dir) {
		if d.Code != "L018" || d.Severity != "warning" {
			t.Errorf("diagnostic %+v", d)
		}
		got = append(got, d.File+":"+strconv.Itoa(d.Line))
	}
	want := "db/queries/post.sql:6,db/queries/post.sql:16,db/queries/products.sql:1"
	if strings.Join(got, ",") != want {
		t.Errorf("L018 at %s, want %s", strings.Join(got, ","), want)
	}
	d := ownedQueries(dir)[0]
	if !strings.Contains(d.Message, "CountPosts on post") || !strings.Contains(d.Message, "author_id") || !strings.Contains(d.Message, "lidza:ignore L018") {
		t.Errorf("message: %s", d.Message)
	}

	// No owned model, no finding.
	write("schema.lidza", "model Post {\n  id uuid @id\n  title string\n}\n")
	if got := ownedQueries(dir); len(got) != 0 {
		t.Errorf("without owned models: %+v", got)
	}
}
