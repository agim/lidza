package sdk

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agim/lidza/pkg/inspect"
)

// TestTypeScriptCompiles type-checks generated clients with the compiler
// options of the react template (strict, noUnusedLocals,
// noUnusedParameters): an API without path parameters or schema rules,
// and the one TestTypeScript uses. It needs tsc: examples/notes after
// `npm ci` there, or tsc on PATH.
func TestTypeScriptCompiles(t *testing.T) {
	root, _ := filepath.Abs("../..")
	tsc := filepath.Join(root, "examples", "notes", "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		p, err := exec.LookPath("tsc")
		if err != nil {
			t.Skip("tsc not found (npm ci in examples/notes)")
		}
		tsc = p
	}
	data, err := os.ReadFile(filepath.Join(root, "templates", "react", "tsconfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	opts := cfg["compilerOptions"].(map[string]any)
	// The app's own settings: vite's types and the @lidza/client alias.
	delete(opts, "paths")
	delete(opts, "baseUrl")
	opts["types"] = []string{}

	dir := t.TempDir()
	clients := map[string]*inspect.Context{
		"noparams": {
			Operations: []inspect.Operation{
				{ID: "health", Method: "GET", Path: "/api/v1/health", Params: []string{}, Output: "Health", Builtin: true},
				{ID: "ping", Method: "POST", Path: "/api/v1/ping", Params: []string{}},
			},
			Schemas: map[string]any{
				"Health": map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string"}}, "required": []string{"status"}},
			},
		},
		"full": sampleContext(),
	}
	var include []string
	for name, c := range clients {
		for file, content := range TypeScript(c) {
			p := filepath.Join(dir, name, file)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		include = append(include, name)
	}
	out, _ := json.Marshal(map[string]any{"compilerOptions": opts, "include": include})
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := exec.Command(tsc, "-p", dir).CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, res)
	}
}
