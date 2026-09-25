package diag

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRunGoModule checks the whole pipeline on a Go module with one type
// error: the error is reported once, with layer, file and line, even though
// go vet and staticcheck both see it.
func TestRunGoModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tvar s string = 1\n\t_ = s\n}\n"), 0o644)

	r := Run(context.Background(), Detect(dir))
	if r.Status != "error" || r.Errors() != 1 {
		t.Fatalf("report: %+v", r)
	}
	var d Diagnostic
	for _, x := range r.Diagnostics {
		if x.Severity == "error" {
			d = x
		}
	}
	if d.Layer != "go" || d.File != "main.go" || d.Line != 4 || d.Severity != "error" {
		t.Fatalf("diagnostic: %+v", d)
	}
	for _, tr := range r.Tools {
		switch tr.Tool {
		case "go vet":
			if tr.Skipped || tr.Failed {
				t.Errorf("go vet: %+v", tr)
			}
		case "cargo check", "tsc", "svelte-check", "eslint":
			if !tr.Skipped {
				t.Errorf("%s should be skipped: %+v", tr.Tool, tr)
			}
		}
	}

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	if r := Run(context.Background(), Detect(dir)); r.Status != "ok" || len(r.Diagnostics) != 0 {
		t.Fatalf("clean module: %+v", r)
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	if l := Detect(dir); l.Go || l.CargoDir != "" || l.TSConfig || l.NodeModules {
		t.Fatalf("empty dir: %+v", l)
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), nil, 0o644)
	os.MkdirAll(filepath.Join(dir, "core"), 0o755)
	os.WriteFile(filepath.Join(dir, "core", "Cargo.toml"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "tsconfig.json"), nil, 0o644)
	if l := Detect(dir); !l.Go || l.CargoDir != "core" || !l.TSConfig || l.NodeModules {
		t.Fatalf("got %+v", l)
	}
}

func TestDropSuperseded(t *testing.T) {
	in := []Diagnostic{
		{Tool: "go vet", Severity: "error", Message: "no required module provides package github.com/agim/lidza/pkg/orm; to add it:\ngo get github.com/agim/lidza/pkg/orm"},
		{Tool: "lidza rules", Layer: "go", Code: "L004", Severity: "error", Message: "package github.com/agim/lidza/pkg/orm does not exist in this version of Līdza"},
		{Tool: "go vet", Severity: "error", Message: "no required module provides package github.com/x/y; to add it:\ngo get github.com/x/y"},
	}
	out := dropSuperseded(in)
	if len(out) != 2 || out[0].Code != "L004" || out[1].Tool != "go vet" {
		t.Fatalf("got %+v", out)
	}
}
