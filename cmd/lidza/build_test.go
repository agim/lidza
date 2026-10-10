package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildPrerequisites: an app that generates queries with sqlc cannot
// start a production build without sqlc, unless it builds with committed
// generated code (--pregenerated), which must exist; an app without
// sqlc.yaml builds as before (issue #47).
func TestBuildPrerequisites(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no sqlc
	t.Setenv(envPregenerated, "")
	plain := t.TempDir()
	if err := buildPrerequisites(plain); err != nil {
		t.Fatalf("an app without sqlc.yaml: %v", err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sqlc.yaml"), []byte("version: 2\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "db"), 0o755)
	os.WriteFile(filepath.Join(dir, "db", "schema.sql"), []byte("CREATE TABLE t (id int);\n"), 0o644)
	err := buildPrerequisites(dir)
	if err == nil || !strings.Contains(err.Error(), "go install github.com/sqlc-dev/sqlc/cmd/sqlc@") || !strings.Contains(err.Error(), "--pregenerated") {
		t.Fatalf("missing sqlc: %v", err)
	}
	t.Setenv(envPregenerated, "1")
	if err := buildPrerequisites(dir); err == nil || !strings.Contains(err.Error(), "no db/queries/gen") {
		t.Fatalf("pregenerated without the code: %v", err)
	}
	os.MkdirAll(filepath.Join(dir, "db", "queries", "gen"), 0o755)
	os.WriteFile(filepath.Join(dir, "db", "queries", "gen", "models.go"), []byte("package queries\n"), 0o644)
	if err := buildPrerequisites(dir); err != nil {
		t.Fatalf("pregenerated with the code: %v", err)
	}
}
