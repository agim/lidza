package diag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Test support under tests/ (helpers, provider stubs) is test code: the
// application rules (L001 state, L016 dropped errors, L017 duplicates)
// leave it alone, as they leave _test.go files.
func TestTestSupportIsNotApplicationCode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module app\n\ngo 1.24\n"), 0o644)
	stub := "package kit\n\nimport (\n\t\"encoding/json\"\n\t\"net/http\"\n)\n\n// Seen grows per request, which L001 flags in application code.\nvar Seen = map[string][]string{}\n\nfunc Stub(w http.ResponseWriter, r *http.Request) {\n\tSeen[r.URL.Path] = append(Seen[r.URL.Path], r.Method)\n\tjson.NewEncoder(w).Encode(map[string]any{\"ok\": true})\n}\n"
	for _, p := range []string{"tests/kit/stub.go", "internal/testkit/stub.go"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(stub), 0o644)
	}
	// The same code as application code is flagged.
	os.MkdirAll(filepath.Join(dir, "internal", "stubs"), 0o755)
	os.WriteFile(filepath.Join(dir, "internal", "stubs", "stub.go"), []byte(stub), 0o644)
	var support, app []Diagnostic
	for _, d := range Rules(context.Background(), dir) {
		switch {
		case d.File == "internal/stubs/stub.go":
			app = append(app, d)
		case d.Code == "L001" || d.Code == "L016" || d.Code == "L017":
			support = append(support, d)
		}
	}
	if len(support) != 0 {
		t.Errorf("test support flagged: %+v", support)
	}
	if len(app) == 0 {
		t.Error("the same code as application code was not flagged (the test proves nothing)")
	}
}
