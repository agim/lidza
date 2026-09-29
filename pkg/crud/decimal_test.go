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
	if _, err := Generate(root, Options{Model: "Order", Module: "app"}); err != nil {
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
	for _, args := range [][]string{{"sqlc", "generate"}, {"go", "mod", "tidy"}, {"go", "vet", "./handlers/", "./schema/", "./db/..."}} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			h, _ := os.ReadFile(filepath.Join(root, "handlers/order.go"))
			t.Fatalf("%v: %v\n%s\n--- handlers:\n%s", args, err, out, h)
		}
	}
}
