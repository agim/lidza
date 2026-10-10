package schema

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
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
		"ref type differs": "model B {\n id uuid @id\n}\nmodel A {\n id uuid @id\n b int @ref(B)\n}",
		"ref on array":     "model B {\n id int @id\n}\nmodel A {\n id uuid @id\n b int[] @ref(B)\n}",
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
	prev := Clock
	t.Cleanup(func() { Clock = prev })
	Clock = func() time.Time { return time.Date(2026, 10, 6, 14, 5, 12, 0, time.UTC) }
	s, _ := Parse(example)
	res, err := Generate(dir, s, "", "types.ts")
	if err != nil {
		t.Fatal(err)
	}
	if res.Migration != "20261006140512_init" || len(res.Files) != 6 {
		t.Fatalf("first run: %+v", res)
	}
	for _, f := range []string{GoFile, SQLFile, LockFile, "db/migrations/20261006140512_init.up.sql", "db/migrations/20261006140512_init.down.sql", "types.ts"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	res, err = Generate(dir, s, "", "types.ts")
	if err != nil || len(res.Files) != 0 || res.Migration != "" {
		t.Fatalf("second run should be a no-op: %+v %v", res, err)
	}
	// The same second (a fast second run): the next name is still later.
	v2, _ := Parse(strings.Replace(example, "  meta      json?\n", "  meta      json?\n  score     float?\n", 1))
	res, err = Generate(dir, v2, "", "types.ts")
	if err != nil || res.Migration != "20261006140513_alter_posts" {
		t.Fatalf("third run: %+v %v", res, err)
	}
	if got := Migrations(dir); len(got) != 2 || got[1] != "20261006140513_alter_posts" {
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
	for in, want := range map[string]string{
		"authorId": "AuthorID", "url": "URL", "createdAt": "CreatedAt", "id": "ID", "htmlBody": "HTMLBody", "x": "X",
		"productIds": "ProductIDs", "imageUrls": "ImageURLs", "ids": "IDs", "metId": "MetID", "https": "Https", "idsSeen": "IDsSeen",
		"product_ids": "ProductIDs",
	} {
		if got := GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
	if snake("createdAt") != "created_at" || snake("id") != "id" {
		t.Error("snake")
	}
}

// TestSQLCNames: sqlc gets the schema's initialisms, and a rename where
// they are not enough (plurals), so both packages spell a column alike.
func TestSQLCNames(t *testing.T) {
	s := mustParse(`enum Source { api ids plain }
model Product {
  id         uuid     @id
  url        string
  productIds uuid[]
  html       text?
  metId      int
}
model ApiKey {
  id uuid @id
}
type Search {
  pageIds int[]
}
`)
	initialisms, rename := SQLCNames(s)
	if strings.Join(initialisms, ",") != strings.Join(Initialisms, ",") {
		t.Errorf("initialisms %v", initialisms)
	}
	if len(rename) != 2 || rename["product_ids"] != "ProductIDs" || rename["source_ids"] != "SourceIDs" {
		t.Errorf("rename %v", rename)
	}
	if SQLCName("api_key") != "APIKey" || SQLCName("source_ids") != "SourceIDs" || SQLCName("note") != "Note" {
		t.Error("SQLCName")
	}
	for _, f := range s.Models[0].Fields {
		col := snake(f.Name)
		got := sqlcName(col, initialismSet)
		if r, ok := rename[col]; ok {
			got = r
		}
		if got != GoName(f.Name) {
			t.Errorf("%s: sqlc %s, schema %s", f.Name, got, GoName(f.Name))
		}
	}
	var renames []string
	for _, r := range QueryRenames(s) {
		renames = append(renames, r.String())
	}
	want := "queries (table api_key): ApiKey is now APIKey," +
		"queries (column product.html): Html is now HTML," +
		"queries (column product.product_ids): ProductIds is now ProductIDs," +
		"queries (enum value source.api): SourceApi is now SourceAPI," +
		"queries (enum value source.ids): SourceIds is now SourceIDs," +
		"queries (column product.url): Url is now URL"
	if strings.Join(renames, ",") != want {
		t.Errorf("query renames:\n%s\nwant\n%s", strings.Join(renames, ","), want)
	}
}

// TestSchemaRenames: Generate names what changed in schema/ since the
// file it replaces, and nothing on the next run.
func TestSchemaRenames(t *testing.T) {
	dir := t.TempDir()
	s := mustParse("enum Kind { urls plain }\n\ntype Search {\n  pageIds int[]\n  url string\n}\n")
	old := "package schema\n\nconst (\n\tKindUrls Kind = \"urls\"\n)\n\ntype Search struct {\n\tPageIds []int  `json:\"pageIds\"`\n\tURL     string `json:\"url\"`\n}\n"
	os.MkdirAll(filepath.Join(dir, "schema"), 0o755)
	os.WriteFile(filepath.Join(dir, GoFile), []byte(old), 0o644)
	res, err := Generate(dir, s, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.Renamed {
		got = append(got, r.String())
	}
	if strings.Join(got, ",") != "schema: KindUrls is now KindURLs,schema.Search: PageIds is now PageIDs" {
		t.Errorf("renamed: %v", got)
	}
	if res, _ := Generate(dir, s, "", ""); len(res.Renamed) != 0 {
		t.Errorf("second run: %v", res.Renamed)
	}
	if rs := GenerateRust(s); !strings.Contains(rs, "    Urls,") {
		t.Errorf("rust variants keep their names:\n%s", rs)
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

// TestRefKeyTypes: @ref takes the referenced id's type (int, bigint,
// string, uuid), names it when the field differs, and tables are created
// after the ones they reference and dropped before them.
func TestRefKeyTypes(t *testing.T) {
	s, err := Parse(`model Product {
  id           uuid    @id
  categoryId int     @ref(Category)
  accountKey   string? @ref(Account, setnull)
  objectId     bigint  @ref(MetObject, cascade)
}
model Category {
  id   int    @id
  name string
}
model Account {
  key text @id
}
model MetObject {
  id bigint @id
}`)
	if err != nil {
		t.Fatal(err)
	}
	ddl := GenerateSQL(s)
	for _, want := range []string{
		"category_id integer NOT NULL REFERENCES category(id)",
		"account_key text REFERENCES account(key) ON DELETE SET NULL",
		"object_id bigint NOT NULL REFERENCES met_object(id) ON DELETE CASCADE",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL lacks %s:\n%s", want, ddl)
		}
	}
	if strings.Index(ddl, "CREATE TABLE category") > strings.Index(ddl, "CREATE TABLE product") ||
		strings.Index(ddl, "CREATE TABLE met_object") > strings.Index(ddl, "CREATE TABLE product") {
		t.Errorf("referenced tables come first:\n%s", ddl)
	}
	up := strings.Join(Diff(nil, s, 1).Up, "\n")
	if strings.Index(up, "CREATE TABLE category") > strings.Index(up, "CREATE TABLE product") {
		t.Errorf("migration creates product before category:\n%s", up)
	}
	drop := strings.Join(Diff(s, &Schema{}, 2).Up, "\n")
	if strings.Index(drop, "DROP TABLE product") > strings.Index(drop, "DROP TABLE category") {
		t.Errorf("migration drops category before product:\n%s", drop)
	}
	if src := GenerateGo(s); !strings.Contains(src, "CategoryID int `json:\"categoryId\"") || !strings.Contains(src, "ObjectID int64") {
		t.Errorf("Go:\n%s", src)
	}

	_, err = Parse("model Category {\n  id int @id\n}\nmodel Product {\n  id uuid @id\n  categoryId uuid @ref(Category)\n}")
	if err == nil || !strings.Contains(err.Error(), "Product.categoryId: @ref(Category) needs type int, the type of Category.id, not uuid") {
		t.Errorf("mismatch: %v", err)
	}
	_, err = Parse("model Category {\n  id int @id\n}\nmodel Product {\n  id uuid @id\n  category Category\n}")
	if err == nil || !strings.Contains(err.Error(), "use int @ref(Category)") {
		t.Errorf("model-typed field: %v", err)
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
		"ALTER TABLE task ADD CONSTRAINT task_project_id_fkey FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE NOT VALID;",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up missing %q\n%s", want, up)
		}
	}
	// Existing rows are checked after, without blocking writes.
	if after := strings.Join(m.After, "\n"); after != "ALTER TABLE task VALIDATE CONSTRAINT task_project_id_fkey;" {
		t.Errorf("after:\n%s", after)
	}
	if down := strings.Join(m.Down, "\n"); !strings.Contains(down, "ADD CONSTRAINT task_project_id_fkey FOREIGN KEY (project_id) REFERENCES project(id);") {
		t.Errorf("down:\n%s", down)
	}
	if Diff(v2, v2, 3) != nil {
		t.Fatal("no change should give no migration")
	}
}

func TestOwner(t *testing.T) {
	s, err := Parse(`model User {
  id uuid @id
}
model Note {
  id      uuid @id
  ownerId uuid
  title   string
}
model Post {
  id       uuid @id
  editorId uuid? @ref(User, setnull)
  authorId uuid @ref(User)
}
model Membership {
  id        uuid @id
  userId    string
  projectId uuid
}
model Follow {
  id     uuid @id
  userId uuid @ref(User)
}
model Project @shared {
  id      uuid @id
  ownerId uuid
}
model Tag @public {
  id      uuid @id
  ownerId uuid
}
model Category {
  id   uuid @id
  name string
}
type CreateNote {
  ownerId uuid
}`)
	if err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string]string{"Note": "ownerId", "Post": "editorId", "Membership": "", "Follow": "userId", "Project": "", "Tag": "", "Category": "", "User": "", "CreateNote": ""} {
		got := ""
		if f := s.Owner(s.Model(model)); f != nil {
			got = f.Name
		}
		if got != want {
			t.Errorf("%s: owner %q, want %q", model, got, want)
		}
	}
	if !s.Model("Tag").Public {
		t.Error("@public not parsed")
	}
	if _, err := Parse("type T @public {\n  a string\n}"); err == nil {
		t.Error("@public accepted on a type")
	}
	if _, err := Parse("model T @public(x) {\n  id uuid @id\n}"); err == nil {
		t.Error("@public(x) accepted")
	}
}

// @public and @shared exclude each other.
func TestPublicSharedExclusive(t *testing.T) {
	if _, err := Parse("model Tag @public @shared {\n  id uuid @id\n}\n"); err == nil || !strings.Contains(err.Error(), "exclude each other") {
		t.Fatalf("both: %v", err)
	}
}

// A new migration sorts after every existing one: after the numbered
// ones of older releases, and after a later stamp another machine wrote
// (its clock ahead of this one).
func TestNextStamp(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC)
	if got := NextStamp(dir, now); got != "20261006140000" {
		t.Fatal(got)
	}
	for _, f := range []string{"0108_old.up.sql", "20261006150000_ahead.up.sql", "20261006150000_ahead.down.sql"} {
		os.WriteFile(filepath.Join(dir, f), nil, 0o644)
	}
	if got := NextStamp(dir, now); got != "20261006150001" {
		t.Fatal(got)
	}
	names := []string{"0108_old", "20261006150001_new", "0109_older"}
	sort.Strings(names)
	if names[2] != "20261006150001_new" {
		t.Fatalf("order %v", names)
	}
}

// Two branches' lock files merge model by model: one adds a model (and
// shifts every line below), the other changes another; both land. Two
// different changes to one model are a conflict.
func TestMergeLocks(t *testing.T) {
	lock := func(src string) []byte {
		t.Helper()
		s, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(Schema{Enums: s.Enums, Models: s.Models})
		return data
	}
	base := lock("model Post {\n  id int @id\n}\n\nmodel Tag {\n  id int @id\n}\n")
	ours := lock("model Comment {\n  id int @id\n}\n\nmodel Post {\n  id int @id\n}\n\nmodel Tag {\n  id int @id\n}\n")
	theirs := lock("model Post {\n  id int @id\n}\n\nmodel Tag {\n  id int @id\n  name string\n}\n")
	merged, conflicts, err := MergeLocks(base, ours, theirs)
	if err != nil || len(conflicts) != 0 {
		t.Fatal(conflicts, err)
	}
	var s Schema
	json.Unmarshal(merged, &s)
	if s.Model("Comment") == nil || s.Model("Post") == nil || s.Model("Tag") == nil || len(s.Model("Tag").Fields) != 2 {
		t.Fatalf("merged: %s", merged)
	}
	// Removed on one side, untouched on the other: removed.
	theirs2 := lock("model Post {\n  id int @id\n}\n")
	merged, _, _ = MergeLocks(base, base, theirs2)
	json.Unmarshal(merged, &s)
	if s.Model("Tag") != nil {
		t.Fatalf("removal lost: %s", merged)
	}
	// Both changed Tag, differently.
	ours3 := lock("model Post {\n  id int @id\n}\n\nmodel Tag {\n  id int @id\n  slug string\n}\n")
	if _, conflicts, _ := MergeLocks(base, ours3, theirs); strings.Join(conflicts, ",") != "model Tag" {
		t.Fatalf("conflicts: %v", conflicts)
	}
}

// TestSearchIndex: @search fields make one weighted GIN index in the
// model's configuration; a field added to the search recreates it in the
// next migration; @search on a non-string field and a bad weight or
// configuration are refused.
func TestSearchIndex(t *testing.T) {
	s, err := Parse(`model Post {
  id    uuid   @id @default(uuid())
  title string @search
  body  string? @search
  slug  string
  @@search("english")
}
`)
	if err != nil {
		t.Fatal(err)
	}
	m := s.Model("Post")
	want := `CREATE INDEX post_search_idx ON post USING gin ((setweight(to_tsvector('english'::regconfig, coalesce(title, '')), 'A') || setweight(to_tsvector('english'::regconfig, coalesce(body, '')), 'B')));`
	if got := createIndexes(m); len(got) != 1 || got[0] != want {
		t.Fatalf("indexes:\n%v\nwant\n%s", got, want)
	}
	next, err := Parse(`model Post {
  id    uuid   @id @default(uuid())
  title string @search(A)
  body  string? @search(C)
  slug  string @search(D)
  @@search("english")
}
`)
	if err != nil {
		t.Fatal(err)
	}
	mig := Diff(s, next, 2)
	up := strings.Join(mig.After, "\n")
	if !strings.Contains(up, "DROP INDEX CONCURRENTLY IF EXISTS post_search_idx;\nCREATE INDEX CONCURRENTLY post_search_idx ON post USING gin") || !strings.Contains(up, "coalesce(slug, '')), 'D')") || !strings.Contains(up, "coalesce(body, '')), 'C')") {
		t.Fatalf("search index not recreated:\n%s", up)
	}
	for _, bad := range []string{
		"model A {\n  id uuid @id\n  n int @search\n}\n",
		"model A {\n  id uuid @id\n  n string @search(E)\n}\n",
		"model A {\n  id uuid @id\n  n string @search\n  @@search(\"en glish\")\n}\n",
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
}
