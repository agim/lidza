package sdk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/inspect"
	"github.com/agim/lidza/pkg/schema"
)

const orderSchema = `model Order {
  id       int            @id @default(autoincrement())
  total    decimal(12, 2) @min(0.01) @max(99999999.99)
  discount decimal(5, 4)?
}

type CreateOrder {
  total    decimal(12, 2) @min(0.01) @max(99999999.99)
  discount decimal(5, 4)?
}
`

func orderContext(t *testing.T) *inspect.Context {
	t.Helper()
	s, err := schema.Parse(orderSchema)
	if err != nil {
		t.Fatal(err)
	}
	return &inspect.Context{
		App: inspect.App{Name: "demo"},
		Operations: []inspect.Operation{
			{ID: "createOrder", Method: "POST", Path: "/api/v1/orders", Params: []string{}, Input: "CreateOrder", Output: "Order"},
		},
		Schemas: schema.JSONSchema(s),
	}
}

// TestDecimalClients: a decimal is a string in both clients, with the
// digits noted in TypeScript, and validators.ts checks the digits and the
// bounds exactly. With tsc and node, the validator runs.
func TestDecimalClients(t *testing.T) {
	c := orderContext(t)
	files := TypeScript(c)
	for _, want := range []string{
		"  /** Exact decimal as a string, \"12.50\" (12 digits, 2 after the point). */\n  total: string\n",
		"  discount?: string | null\n",
		"  id: number\n",
	} {
		if !strings.Contains(files["types.ts"], want) {
			t.Errorf("types.ts lacks %q:\n%s", want, files["types.ts"])
		}
	}
	v := files["validators.ts"]
	for _, want := range []string{
		`if (!decimalFits(v.total, 12, 2)) errs.push({ field: "total", rule: "decimal", message: "not a number with at most 10 digit(s) before the point and 2 after" })`,
		`if (DECIMAL.test(v.total) && decimalCmp(v.total, "0.01") < 0) errs.push({ field: "total", rule: "min", message: "at least 0.01" })`,
		`if (DECIMAL.test(v.total) && decimalCmp(v.total, "99999999.99") > 0)`,
		"  if (v.discount !== null && v.discount !== undefined) {\n    if (!decimalFits(v.discount, 5, 4))",
		"function decimalCmp(a: string, b: string): number {",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("validators.ts lacks %q:\n%s", want, v)
		}
	}
	dart := Dart(c)["lib/src/types.dart"]
	for _, want := range []string{"final String total;", "final String? discount;", "final int id;"} {
		if !strings.Contains(dart, want) {
			t.Errorf("types.dart lacks %q:\n%s", want, dart)
		}
	}

	tsc := findTSC(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
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
	script := `const { validateCreateOrder } = require('./js/index.js')
const cases = [
  { total: '0.01' }, { total: '0.009' }, { total: '0.00' }, { total: '99999999.99' },
  { total: '99999999.991' }, { total: '100000000' }, { total: '12,5' }, { total: '-1' },
  { total: '1', discount: '1.00005' }, { total: '1', discount: null }, { total: '0000000012.50' },
]
console.log(cases.map((c) => validateCreateOrder(c).map((e) => e.field + ':' + e.rule).join(' ')).join('|'))
`
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(script), 0o644)
	cmd := exec.Command(node, "main.js")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := "|total:decimal total:min|total:min||total:decimal total:max|total:max|total:decimal|total:min|discount:decimal||"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
