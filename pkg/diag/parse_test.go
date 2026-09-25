package diag

import (
	"reflect"
	"testing"
)

func TestParseGoText(t *testing.T) {
	out := []byte("# demo\n# [demo]\nvet: ./routes.go:28:36: cannot use 1 (untyped int constant) as string value in variable declaration\n/abs/root/pkg/x.go:3:1: undefined: y\n\thave (int)\n\twant (string)\ntoo many errors\n")
	got := parseGoText("/abs/root", "go vet", out)
	want := []Diagnostic{
		{Layer: "go", Tool: "go vet", Severity: "error", File: "routes.go", Line: 28, Column: 36, Message: "cannot use 1 (untyped int constant) as string value in variable declaration"},
		{Layer: "go", Tool: "go vet", Severity: "error", File: "pkg/x.go", Line: 3, Column: 1, Message: "undefined: y\nhave (int)\nwant (string)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseGoVetJSON(t *testing.T) {
	out := []byte(`{}
{"demo": {"printf": [{"posn": "/r/routes.go:29:17", "message": "fmt.Sprintf format %d has arg \"s\" of wrong type string"}]}}
`)
	got := parseGoVetJSON("/r", out)
	want := []Diagnostic{{Layer: "go", Tool: "go vet", Severity: "warning", Code: "printf", File: "routes.go", Line: 29, Column: 17, Message: `fmt.Sprintf format %d has arg "s" of wrong type string`}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseStaticcheck(t *testing.T) {
	out := []byte(`{"code":"compile","severity":"error","location":{"file":"","line":0,"column":0},"end":{"file":"","line":0,"column":0},"message":"# demo\n./routes.go:29:17: declared and not used: x"}
{"code":"SA4006","severity":"error","location":{"file":"/r/main.go","line":10,"column":2},"end":{"file":"/r/main.go","line":10,"column":3},"message":"this value of x is never used"}
{"code":"U1000","severity":"ignored","location":{"file":"/r/main.go","line":1,"column":1},"end":{},"message":"ignored"}
`)
	got := parseStaticcheck("/r", out)
	want := []Diagnostic{
		{Layer: "go", Tool: "staticcheck", Severity: "error", File: "routes.go", Line: 29, Column: 17, Message: "declared and not used: x"},
		{Layer: "go", Tool: "staticcheck", Severity: "error", Code: "SA4006", File: "main.go", Line: 10, Column: 2, Message: "this value of x is never used"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseCargo(t *testing.T) {
	out := []byte(`{"reason":"compiler-artifact","target":{}}
{"reason":"compiler-message","message":{"level":"error","code":{"code":"E0308"},"message":"mismatched types","spans":[{"file_name":"src/lib.rs","line_start":34,"column_start":17,"is_primary":false,"label":"expected because of return type"},{"file_name":"src/lib.rs","line_start":34,"column_start":39,"is_primary":true,"label":"expected ` + "`u64`, found `i32`" + `"}]}}
{"reason":"compiler-message","message":{"level":"error","code":null,"message":"aborting due to 1 previous error","spans":[]}}
{"reason":"compiler-message","message":{"level":"failure-note","code":null,"message":"For more information","spans":[]}}
{"reason":"compiler-message","message":{"level":"warning","code":{"code":"unused_variables"},"message":"unused variable: x","spans":[{"file_name":"src/lib.rs","line_start":3,"column_start":9,"is_primary":true,"label":""}]}}
`)
	got := parseCargo("core", out)
	want := []Diagnostic{
		{Layer: "rust", Tool: "cargo check", Severity: "error", Code: "E0308", File: "core/src/lib.rs", Line: 34, Column: 39, Message: "mismatched types: expected `u64`, found `i32`"},
		{Layer: "rust", Tool: "cargo check", Severity: "warning", Code: "unused_variables", File: "core/src/lib.rs", Line: 3, Column: 9, Message: "unused variable: x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseTSC(t *testing.T) {
	out := []byte("src/bad.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.\n  Detail line.\nsrc/bad.ts(1,7): error TS6133: 'n' is declared but its value is never read.\n")
	got := parseTSC(out)
	want := []Diagnostic{
		{Layer: "frontend", Tool: "tsc", Severity: "error", Code: "TS2322", File: "src/bad.ts", Line: 1, Column: 7, Message: "Type 'string' is not assignable to type 'number'.\nDetail line."},
		{Layer: "frontend", Tool: "tsc", Severity: "error", Code: "TS6133", File: "src/bad.ts", Line: 1, Column: 7, Message: "'n' is declared but its value is never read."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseESLint(t *testing.T) {
	out := []byte(`[{"filePath":"/r/src/pages/Home.tsx","messages":[{"ruleId":"jsx-a11y/alt-text","severity":2,"message":"img elements must have an alt prop","line":7,"column":9},{"ruleId":"no-unused-vars","severity":1,"message":"x is unused","line":2,"column":7}]},{"filePath":"/r/src/ok.ts","messages":[]}]`)
	got := parseESLint("/r", out)
	want := []Diagnostic{
		{Layer: "frontend", Tool: "eslint", Severity: "error", Code: "jsx-a11y/alt-text", File: "src/pages/Home.tsx", Line: 7, Column: 9, Message: "img elements must have an alt prop"},
		{Layer: "frontend", Tool: "eslint", Severity: "warning", Code: "no-unused-vars", File: "src/pages/Home.tsx", Line: 2, Column: 7, Message: "x is unused"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestDedupe(t *testing.T) {
	in := []Diagnostic{
		{Tool: "go vet", File: "a.go", Line: 1, Message: "m"},
		{Tool: "staticcheck", File: "a.go", Line: 1, Message: "m"},
		{Tool: "go vet", File: "", Message: "no file"},
		{Tool: "staticcheck", File: "", Message: "no file"},
	}
	if got := dedupe(in); len(got) != 3 || got[0].Tool != "go vet" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseSvelteCheck(t *testing.T) {
	out := []byte("1790304146944 START \"/x\"\n1790304146947 ERROR \"src/App.svelte\" 2:9 \"Type 'string' is not assignable to type 'number'.\"\n1790304146947 WARNING \"src/A.svelte\" 5:1 \"unused\"\n1790304146947 COMPLETED 80 FILES 1 ERRORS 1 WARNINGS 2 FILES_WITH_PROBLEMS\n")
	got := parseSvelteCheck(out)
	want := []Diagnostic{
		{Layer: "frontend", Tool: "svelte-check", Severity: "error", File: "src/App.svelte", Line: 2, Column: 9, Message: "Type 'string' is not assignable to type 'number'."},
		{Layer: "frontend", Tool: "svelte-check", Severity: "warning", File: "src/A.svelte", Line: 5, Column: 1, Message: "unused"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}
