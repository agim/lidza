package schema

import "testing"

// FuzzParse: schema.lidza is written by people and agents; any text is
// a schema or an error, never a panic, and parsing is deterministic.
func FuzzParse(f *testing.F) {
	f.Add("model Post {\n  title string @search @max(200)\n  body text?\n  price decimal(10,2)\n  tags []string\n}\n")
	f.Add("enum Status { draft published }\nmodel A { s Status @default(draft) }\n")
	f.Add("type Input {\n  n int @min(1) @max(x)\n}\n")
	f.Add("model { }")
	f.Fuzz(func(t *testing.T, src string) {
		a, errA := Parse(src)
		b, errB := Parse(src)
		if (errA == nil) != (errB == nil) || (a == nil) != (b == nil) {
			t.Fatalf("Parse is not deterministic for %q", src)
		}
		if errA == nil && a != nil {
			// What parses generates SQL, Go and TypeScript without panicking.
			GenerateSQL(a)
			GenerateGo(a)
			GenerateTS(a)
		}
	})
}
