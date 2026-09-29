package crud

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
)

const orderSrc = `model Customer {
  id   bigint @id @default(autoincrement())
  name string
}

model Order {
  id         int              @id @default(autoincrement())
  number     bigint           @default(autoincrement()) @unique
  customerId bigint           @ref(Customer)
  total      decimal(12, 2)   @min(0.01)
  discount   decimal(5, 4)?
  rates      decimal(8, 3)[]
}
`

// TestGenerateDecimal: a resource over an identity id and decimal fields
// leaves the identity columns out of inserts and updates, keeps the
// decimal type and its bounds in the Create and Update types, and, with
// sqlc installed, the handlers compile against the generated queries.
func TestGenerateDecimal(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(orderSrc), 0o644)
	if _, _, err := pack.Add(root, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, Options{Model: "Order", Module: "app", Auth: true}); err != nil {
		t.Fatal(err)
	}
	sql, _ := os.ReadFile(filepath.Join(root, "db/queries/order.sql"))
	if !strings.Contains(string(sql), `INSERT INTO "order" (customer_id, total, discount, rates) VALUES ($1, $2, $3, $4) RETURNING *;`) {
		t.Errorf("insert:\n%s", sql)
	}
	src, _ := os.ReadFile(filepath.Join(root, schema.FileName))
	for _, want := range []string{
		"type CreateOrder {\n  customerId bigint\n  total decimal(12, 2) @min(0.01)\n  discount decimal(5, 4)?\n  rates decimal(8, 3)[]\n}",
		"  total decimal(12, 2)? @min(0.01)\n",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("schema lacks %q:\n%s", want, src)
		}
	}
	compileApp(t, root, "order.go")
}

// compileApp generates the schema's Go and SQL and the sqlc queries in
// the app at root, then vets it (and runs its tests, when test is set).
// It skips without sqlc.
func compileApp(t *testing.T, root, handlersFile string, test ...string) {
	t.Helper()
	src, _ := os.ReadFile(filepath.Join(root, schema.FileName))
	s, err := schema.Parse(string(src))
	if err != nil {
		t.Fatalf("appended schema: %v", err)
	}
	if _, err := exec.LookPath("sqlc"); err != nil {
		t.Skip("sqlc not installed")
	}
	lidza, _ := filepath.Abs("../..")
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module app\n\ngo 1.27\n\nrequire github.com/agim/lidza v0.0.0\n\nreplace github.com/agim/lidza => "+lidza+"\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "schema"), 0o755)
	os.WriteFile(filepath.Join(root, schema.GoFile), []byte(schema.Gofmt(schema.GenerateGo(s))), 0o644)
	os.WriteFile(filepath.Join(root, schema.SQLFile), []byte(schema.GenerateSQL(s)), 0o644)
	if _, err := pack.SyncSQLCNames(root, s); err != nil {
		t.Fatal(err)
	}
	steps := [][]string{{"sqlc", "generate"}, {"go", "mod", "tidy"}, {"go", "vet", "./handlers/", "./schema/", "./db/..."}}
	if len(test) > 0 {
		steps = append(steps, append([]string{"go", "test", "-count=1"}, test...))
	}
	for _, args := range steps {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			h, _ := os.ReadFile(filepath.Join(root, "handlers", handlersFile))
			t.Fatalf("%v: %v\n%s\n--- handlers:\n%s", args, err, out, h)
		}
	}
}

// TestGenerateCustomTable: a model whose @table differs from its name
// maps rows of the type sqlc names after the table (store_products is
// StoreProduct), and the handlers compile.
func TestGenerateCustomTable(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte("model Product @table(\"store_products\") @public {\n  id   uuid   @id @default(uuid())\n  name string\n  sku  string\n}\n"), 0o644)
	if _, _, err := pack.Add(root, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root, Options{Model: "Product", Module: "app"}); err != nil {
		t.Fatal(err)
	}
	if h := read(t, root, "handlers/store_products.go"); !strings.Contains(h, "row queries.StoreProduct)") {
		t.Fatalf("row type:\n%s", h)
	}
	compileApp(t, root, "store_products.go")
}

// TestGenerateThinModels: a model with one writable field passes it to
// the query as is (sqlc makes no Params struct), and one with none has
// no update and inserts DEFAULT VALUES; each compiles, owned or not.
func TestGenerateThinModels(t *testing.T) {
	for _, tc := range []struct{ name, src, file string }{
		{"one field", "model Tag @public {\n  id   uuid   @id @default(uuid())\n  name string\n}\n", "tag.go"},
		{"no field", "model Visit @public {\n  id        uuid      @id @default(uuid())\n  createdAt time      @default(now())\n}\n", "visit.go"},
		{"owned only", "model Bookmark {\n  id      uuid   @id @default(uuid())\n  ownerId uuid   @index\n}\n", "bookmark.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, schema.FileName), []byte(tc.src), 0o644)
			if _, _, err := pack.Add(root, "db"); err != nil {
				t.Fatal(err)
			}
			model := strings.Fields(tc.src)[1]
			if _, err := Generate(root, Options{Model: model, Module: "app", Auth: true}); err != nil {
				t.Fatal(err)
			}
			h := read(t, root, "handlers/"+tc.file)
			if tc.name != "one field" && strings.Contains(h, "func update") {
				t.Errorf("an update with nothing to set:\n%s", h)
			}
			compileApp(t, root, tc.file)
		})
	}
}
