package snippets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInSync fails when a snippet differs from its file in examples/notes:
// run `go generate ./pkg/snippets`.
func TestInSync(t *testing.T) {
	for _, s := range Index {
		_, embedded, ok := Get(s.Name)
		if !ok || embedded == "" {
			t.Fatalf("%s: not embedded", s.Name)
		}
		src, err := os.ReadFile(filepath.Join("..", "..", "examples", "notes", filepath.FromSlash(s.File)))
		if err != nil {
			t.Fatal(err)
		}
		if string(src) != embedded {
			t.Errorf("%s (%s) is out of date; run go generate ./pkg/snippets", s.Name, s.File)
		}
	}
	if _, _, ok := Get("nope"); ok {
		t.Error("unknown snippet found")
	}
	if !strings.Contains(Catalog(), "resource-handlers (handlers/note.go)") {
		t.Errorf("catalog:\n%s", Catalog())
	}
}
