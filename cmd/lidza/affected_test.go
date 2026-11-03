package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A module with a library, two test packages (one importing it, one
// not) and a test-only helper: the selection follows the imports.
func affectedRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	dir, git := guardRepo(t)
	write := func(p, s string) {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	write("go.mod", "module app\n\ngo 1.24\n")
	write("internal/lib/lib.go", "package lib\n\nfunc One() int { return 1 }\n")
	write("internal/other/other.go", "package other\n\nfunc Two() int { return 2 }\n")
	write("tests/orders/orders_test.go", "package orders\n\nimport (\n\t\"testing\"\n\n\t\"app/internal/lib\"\n)\n\nfunc TestOne(t *testing.T) { if lib.One() != 1 { t.Fatal() } }\n")
	write("tests/sites/sites_test.go", "package sites\n\nimport (\n\t\"testing\"\n\n\t\"app/internal/other\"\n)\n\nfunc TestTwo(t *testing.T) { if other.Two() != 2 { t.Fatal() } }\n")
	write("docs/notes.md", "# notes\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return dir, git
}

func TestAffectedPackages(t *testing.T) {
	dir, git := affectedRepo(t)
	ctx := context.Background()
	// Nothing changed: nothing to test.
	a, err := affectedPackages(ctx, dir, "", true)
	if err != nil || a.All || len(a.Packages) != 0 || a.Total != 2 {
		t.Fatalf("clean: %+v %v", a, err)
	}
	// A library change reaches the package that imports it, staged or not.
	os.WriteFile(filepath.Join(dir, "internal/lib/lib.go"), []byte("package lib\n\nfunc One() int { return 1 + 0 }\n"), 0o644)
	a, _ = affectedPackages(ctx, dir, "", false)
	if a.All || strings.Join(a.Packages, ",") != "app/tests/orders" {
		t.Fatalf("working tree: %+v", a)
	}
	if a, _ := affectedPackages(ctx, dir, "", true); a.All || len(a.Packages) != 0 {
		t.Fatalf("not staged yet: %+v", a)
	}
	git("add", "-A")
	if a, _ := affectedPackages(ctx, dir, "", true); a.All || strings.Join(a.Packages, ",") != "app/tests/orders" || !strings.Contains(a.String(), "1 of 2") {
		t.Fatalf("staged: %+v", a)
	}
	git("commit", "-q", "-m", "lib")
	// A docs change reaches no package.
	os.WriteFile(filepath.Join(dir, "docs/notes.md"), []byte("# notes\n\n- one\n"), 0o644)
	git("add", "-A")
	if a, _ := affectedPackages(ctx, dir, "", true); a.All || len(a.Packages) != 0 || !strings.Contains(a.String(), "no Go package") {
		t.Fatalf("docs: %+v", a)
	}
	// A test package's own file reaches itself; a new untracked test too.
	os.WriteFile(filepath.Join(dir, "tests/sites/more_test.go"), []byte("package sites\n\nimport \"testing\"\n\nfunc TestMore(t *testing.T) {}\n"), 0o644)
	if a, _ := affectedPackages(ctx, dir, "", false); strings.Join(a.Packages, ",") != "app/tests/sites" {
		t.Fatalf("own file: %+v", a)
	}
	// go.mod reaches everything; a base ref compares against it.
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module app\n\ngo 1.24\n\n// toolchain note\n"), 0o644)
	if a, _ := affectedPackages(ctx, dir, "HEAD", false); !a.All || !strings.Contains(a.Why, "go.mod") {
		t.Fatalf("go.mod: %+v", a)
	}
	// Outside git history: everything.
	if a, _ := affectedPackages(ctx, t.TempDir(), "", true); !a.All {
		t.Fatalf("no git: %+v", a)
	}
}
