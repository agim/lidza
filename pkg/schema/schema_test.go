package schema

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const example = `// blog
enum Status { draft published }

model User {
  id    uuid   @id @default(uuid())
  email string @unique @email @max(254)
  name  string @min(1) @max(100)
}

model Post @table("posts") {
  id        uuid     @id @default(uuid())
  title     string   @min(1) @max(200)
  body      text?
  status    Status   @default(draft) @index
  authorId  uuid     @ref(User)
  tags      string[] @max(10)
  views     int      @default(0) @min(0)
  meta      json?
  createdAt time     @default(now())
  @@index(status, createdAt)
  @@unique(authorId, title)
}

type CreatePost {
  title  string   @min(1) @max(200)
  body   text?
  tags   string[]
  status Status?
}

type PostList {
  items Post[]
  total int
}
`

func TestParse(t *testing.T) {
	s, err := Parse(example)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Enums) != 1 || len(s.Models) != 2 || len(s.Types) != 2 {
		t.Fatalf("counts: %d %d %d", len(s.Enums), len(s.Models), len(s.Types))
	}
	post := s.Model("Post")
	if post.Table != "posts" || s.Model("User").Table != "user" {
		t.Fatalf("tables: %s %s", post.Table, s.Model("User").Table)
	}
	if post.IDField().Name != "id" || post.IDField().Default != "uuid()" {
		t.Fatalf("id: %+v", post.IDField())
	}
	body := post.Fields[2]
	if body.Name != "body" || !body.Optional || body.Type != "text" {
		t.Fatalf("body: %+v", body)
	}
	tags := post.Fields[5]
	if !tags.Array || tags.Max == nil || *tags.Max != 10 {
		t.Fatalf("tags: %+v", tags)
	}
	author := post.Fields[4]
	if author.Ref != "User" {
		t.Fatalf("author: %+v", author)
	}
	if len(post.Indexes) != 2 || !post.Indexes[1].Unique || post.Indexes[0].Fields[1] != "createdAt" {
		t.Fatalf("indexes: %+v", post.Indexes)
	}
	if s.Types[1].Fields[0].Type != "Post" || !s.Types[1].Fields[0].Array {
		t.Fatalf("nested type: %+v", s.Types[1].Fields[0])
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"unknown type":     "model A {\n id uuid @id\n x thing\n}",
		"no id":            "model A {\n x int\n}",
		"two ids":          "model A {\n a uuid @id\n b uuid @id\n}",
		"bad ref":          "model A {\n id uuid @id\n b uuid @ref(Nope)\n}",
		"ref not uuid":     "model B {\n id uuid @id\n}\nmodel A {\n id uuid @id\n b int @ref(B)\n}",
		"model in model":   "model B {\n id uuid @id\n}\nmodel A {\n id uuid @id\n b B\n}",
		"bad default enum": "enum E { a b }\nmodel A {\n id uuid @id\n e E @default(c)\n}",
		"email on int":     "model A {\n id uuid @id\n n int @email\n}",
		"id in type":       "type A {\n id uuid @id\n}",
		"missing brace":    "model A {\n id uuid @id\n",
		"unknown attr":     "model A {\n id uuid @id @nope\n}",
		"unknown keyword":  "table A {\n}",
		"index unknown":    "model A {\n id uuid @id\n @@index(zzz)\n}",
		"dup field":        "model A {\n id uuid @id\n x int\n x int\n}",
		"dup model":        "model A {\n id uuid @id\n}\nmodel A {\n id uuid @id\n}",
		"lowercase model":  "model a {\n id uuid @id\n}",
		"uppercase field":  "model A {\n Id uuid @id\n}",
		"min not number":   "model A {\n id uuid @id\n s string @min(x)\n}",
		"optional id":      "model A {\n id uuid? @id\n}",
		"fields on brace":  "model A { id uuid @id }",
	}
	for name, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestGenerateGoCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	s, err := Parse(example)
	if err != nil {
		t.Fatal(err)
	}
	src := GenerateGo(s)
	for _, want := range []string{
		"type Status string",
		`StatusDraft Status = "draft"`,
		"AuthorID string `json:\"authorId\" db:\"author_id\"`",
		"Body *string `json:\"body\" db:\"body\"`",
		"Tags []string `json:\"tags\" db:\"tags\"`",
		"Meta json.RawMessage `json:\"meta\" db:\"meta\"`",
		"CreatedAt time.Time `json:\"createdAt\" db:\"created_at\"`",
		"Items []Post `json:\"items\"`",
		"func (v CreatePost) Validate() error",
		`validate.Email(v.Email)`,
		"if len(v.Tags) > 10",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated Go missing %q", want)
		}
	}

	root, _ := filepath.Abs("../..")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module app\n\ngo 1.27\n\nrequire github.com/agim/lidza v0.0.0\n\nreplace github.com/agim/lidza => "+root+"\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "schema"), 0o755)
	os.WriteFile(filepath.Join(dir, GoFile), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"fmt"

	"app/schema"
	"github.com/agim/lidza/pkg/validate"
)

func main() {
	bad := schema.CreatePost{Title: "", Tags: nil}
	err := bad.Validate()
	errs, ok := err.(*validate.Errors)
	if !ok || len(errs.Fields) != 2 || errs.Fields[0].Rule != "required" || errs.Fields[1].Rule != "min" {
		panic(fmt.Sprintf("unexpected: %v", err))
	}
	s := schema.StatusDraft
	good := schema.CreatePost{Title: "hi", Status: &s}
	if err := good.Validate(); err != nil {
		panic(err)
	}
	x := schema.Status("nope")
	withEnum := schema.CreatePost{Title: "hi", Status: &x}
	if err := withEnum.Validate(); err == nil {
		panic("enum not checked")
	}
	u := schema.User{ID: "x", Email: "not-an-email", Name: "n"}
	if err := u.Validate(); err == nil || !strings.Contains(err.Error(), "email") {
		panic(fmt.Sprintf("email: %v", err))
	}
	fmt.Println("ok")
}
`), 0o644)
	// strings is used in main.go; add the import via a second file to keep the snippet simple.
	os.WriteFile(filepath.Join(dir, "strings.go"), []byte("package main\n\nimport \"strings\"\n\nvar _ = strings.Contains\n"), 0o644)
	content, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(strings.Replace(string(content), "\"fmt\"\n", "\"fmt\"\n\t\"strings\"\n", 1)), 0o644)

	for _, args := range [][]string{{"mod", "tidy"}, {"vet", "./..."}, {"run", "."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s\n--- generated:\n%s", args, err, out, src)
		}
		if args[0] == "run" && strings.TrimSpace(string(out)) != "ok" {
			t.Fatalf("run: %s", out)
		}
	}
}

func TestGenerateSQL(t *testing.T) {
	s, _ := Parse(example)
	sql := GenerateSQL(s)
	for _, want := range []string{
		"CREATE TYPE status AS ENUM ('draft', 'published');",
		"CREATE TABLE \"user\" (",
		"id uuid PRIMARY KEY DEFAULT gen_random_uuid()",
		"email varchar(254) NOT NULL UNIQUE",
		"CREATE TABLE posts (",
		"body text,",
		"status status NOT NULL DEFAULT 'draft'",
		"author_id uuid NOT NULL REFERENCES \"user\"(id)",
		"tags text[] NOT NULL",
		"views integer NOT NULL DEFAULT 0",
		"meta jsonb,",
		"created_at timestamptz NOT NULL DEFAULT now()",
		"CREATE INDEX posts_status_idx ON posts (status);",
		"CREATE INDEX posts_status_created_at_idx ON posts (status, created_at);",
		"CREATE UNIQUE INDEX posts_author_id_title_key ON posts (author_id, title);",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL missing %q\n%s", want, sql)
		}
	}
}

func TestDiff(t *testing.T) {
	v1, _ := Parse(example)
	if m := Diff(nil, v1, 1); m == nil || m.Name != "0001_init" || len(m.Up) < 4 || m.Down[0] != "DROP TABLE posts;" {
		t.Fatalf("init: %+v", m)
	}
	if Diff(v1, v1, 2) != nil {
		t.Fatal("no change should give no migration")
	}
	v2src := strings.Replace(example, "  views     int      @default(0) @min(0)\n", "  views     bigint   @default(0) @min(0)\n  slug      string?  @unique\n", 1)
	v2src = strings.Replace(v2src, "  meta      json?\n", "", 1)
	v2src = strings.Replace(v2src, "enum Status { draft published }", "enum Status { draft published archived }", 1)
	v2, err := Parse(v2src)
	if err != nil {
		t.Fatal(err)
	}
	m := Diff(v1, v2, 2)
	if m == nil {
		t.Fatal("expected a migration")
	}
	up := strings.Join(m.Up, "\n")
	for _, want := range []string{
		"ALTER TYPE status ADD VALUE 'archived';",
		"ALTER TABLE posts ALTER COLUMN views TYPE bigint USING views::bigint; -- review: was integer",
		"ALTER TABLE posts ADD COLUMN slug text UNIQUE;",
		"ALTER TABLE posts DROP COLUMN meta; -- review: data loss",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up missing %q\n%s", want, up)
		}
	}
	down := strings.Join(m.Down, "\n")
	for _, want := range []string{"ALTER TABLE posts ADD COLUMN meta jsonb;", "ALTER TABLE posts DROP COLUMN slug;", "TYPE integer USING views::integer"} {
		if !strings.Contains(down, want) {
			t.Errorf("down missing %q\n%s", want, down)
		}
	}
	if m.Name != "0002_extend_status_alter_posts" {
		t.Errorf("name %q", m.Name)
	}
}

func TestGenerateEndToEnd(t *testing.T) {
	dir := t.TempDir()
	s, _ := Parse(example)
	res, err := Generate(dir, s, "", "types.ts")
	if err != nil {
		t.Fatal(err)
	}
	if res.Migration != "0001_init" || len(res.Files) != 6 {
		t.Fatalf("first run: %+v", res)
	}
	for _, f := range []string{GoFile, SQLFile, LockFile, "db/migrations/0001_init.up.sql", "db/migrations/0001_init.down.sql", "types.ts"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	res, err = Generate(dir, s, "", "types.ts")
	if err != nil || len(res.Files) != 0 || res.Migration != "" {
		t.Fatalf("second run should be a no-op: %+v %v", res, err)
	}
	v2, _ := Parse(strings.Replace(example, "  meta      json?\n", "  meta      json?\n  score     float?\n", 1))
	res, err = Generate(dir, v2, "", "types.ts")
	if err != nil || res.Migration != "0002_alter_posts" {
		t.Fatalf("third run: %+v %v", res, err)
	}
	if got := Migrations(dir); len(got) != 2 || got[1] != "0002_alter_posts" {
		t.Fatalf("migrations: %v", got)
	}
	lock, _ := LoadLock(dir)
	if lock.Model("Post").Fields[8].Name != "score" {
		t.Fatalf("lock not updated: %+v", lock.Model("Post").Fields)
	}
}

func TestGenerateTSAndJSONSchema(t *testing.T) {
	s, _ := Parse(example)
	ts := GenerateTS(s)
	for _, want := range []string{
		`export type Status = "draft" | "published"`,
		"export interface Post {",
		"  body: string | null",
		"  tags: string[]",
		"  meta: unknown | null",
		"  createdAt: string",
		"  status: Status | null",
		"  items: Post[]",
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("TS missing %q\n%s", want, ts)
		}
	}
	js := JSONSchema(s)
	post := js["Post"].(map[string]any)["properties"].(map[string]any)
	if body := post["body"].(map[string]any); body["type"].([]any)[1] != "null" {
		t.Errorf("body: %v", body)
	}
	if tags := post["tags"].(map[string]any); tags["type"] != "array" || tags["maxItems"] != 10.0 {
		t.Errorf("tags: %v", tags)
	}
	if st := post["status"].(map[string]any); st["$ref"] != "#/components/schemas/Status" {
		t.Errorf("status: %v", st)
	}
	if email := js["User"].(map[string]any)["properties"].(map[string]any)["email"].(map[string]any); email["format"] != "email" || email["maxLength"] != 254.0 {
		t.Errorf("email: %v", email)
	}
}

func TestGenerateRust(t *testing.T) {
	s, _ := Parse(example + "\ntype Blob {\n  data bytes\n  thumb bytes?\n}\n")
	rs := GenerateRust(s)
	for _, want := range []string{"mod b64 {", "#[serde(with = \"b64\")]\n    pub data: Vec<u8>,", "#[serde(with = \"b64opt\", default)]\n    pub thumb: Option<Vec<u8>>,"} {
		if !strings.Contains(rs, want) {
			t.Errorf("Rust missing %q\n%s", want, rs)
		}
	}
	if strings.Contains(GenerateRust(mustParse(example)), "mod b64") {
		t.Error("b64 helpers emitted without bytes fields")
	}
	for _, want := range []string{
		"pub enum Status {", `#[serde(rename = "draft")]`, "    Draft,",
		"pub struct Post {", `#[serde(rename = "authorId")]`, "pub author_id: String,",
		"pub body: Option<String>,", "pub tags: Vec<String>,", "pub meta: Option<serde_json::Value>,", "pub views: i32,",
		"pub items: Vec<Post>,",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("Rust missing %q\n%s", want, rs)
		}
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{"authorId": "AuthorID", "url": "URL", "createdAt": "CreatedAt", "id": "ID", "htmlBody": "HTMLBody", "x": "X"} {
		if got := exported(in); got != want {
			t.Errorf("exported(%q) = %q, want %q", in, got, want)
		}
	}
	if snake("createdAt") != "created_at" || snake("id") != "id" {
		t.Error("snake")
	}
}

func mustParse(src string) *Schema {
	s, err := Parse(src)
	if err != nil {
		panic(err)
	}
	return s
}

func TestRefOnDelete(t *testing.T) {
	s, err := Parse(`model Project {
  id uuid @id
}
model Task {
  id         uuid  @id
  projectId  uuid  @ref(Project, cascade)
  assigneeId uuid? @ref(Project, setnull)
}`)
	if err != nil {
		t.Fatal(err)
	}
	ddl := GenerateSQL(s)
	for _, want := range []string{"REFERENCES project(id) ON DELETE CASCADE", "REFERENCES project(id) ON DELETE SET NULL"} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL lacks %s:\n%s", want, ddl)
		}
	}
	for _, bad := range []string{
		"model Project {\n  id uuid @id\n}\nmodel Task {\n  id uuid @id\n  projectId uuid @ref(Project, setnull)\n}",
		"model Project {\n  id uuid @id\n}\nmodel Task {\n  id uuid @id\n  projectId uuid @ref(Project, drop)\n}",
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
}

func TestDiffRef(t *testing.T) {
	v1, err := Parse("model Project {\n  id uuid @id\n}\nmodel Task {\n  id uuid @id\n  projectId uuid @ref(Project)\n}")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := Parse("model Project {\n  id uuid @id\n}\nmodel Task {\n  id uuid @id\n  projectId uuid @ref(Project, cascade)\n}")
	if err != nil {
		t.Fatal(err)
	}
	m := Diff(v1, v2, 2)
	if m == nil {
		t.Fatal("expected a migration")
	}
	up := strings.Join(m.Up, "\n")
	for _, want := range []string{
		"ALTER TABLE task DROP CONSTRAINT task_project_id_fkey;",
		"ALTER TABLE task ADD CONSTRAINT task_project_id_fkey FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE;",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up missing %q\n%s", want, up)
		}
	}
	if down := strings.Join(m.Down, "\n"); !strings.Contains(down, "ADD CONSTRAINT task_project_id_fkey FOREIGN KEY (project_id) REFERENCES project(id);") {
		t.Errorf("down:\n%s", down)
	}
	if Diff(v2, v2, 3) != nil {
		t.Fatal("no change should give no migration")
	}
}
