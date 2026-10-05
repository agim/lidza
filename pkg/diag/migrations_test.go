package diag

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrationNumbers: two migrations sharing a number are an error on
// the later name; distinct numbers and down scripts are not.
func TestMigrationNumbers(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "db", "migrations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"0001_create_post.up.sql", "0001_create_post.down.sql",
		"0002_alter_post.up.sql", "0002_alter_post.down.sql",
		"0002_create_comment.up.sql", "0002_create_comment.down.sql",
		"0003_create_tag.up.sql",
	} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("SELECT 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := migrationNumbers(root)
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", got)
	}
	d := got[0]
	if d.Code != "L019" || d.Severity != "error" || d.File != "db/migrations/0002_create_comment.up.sql" {
		t.Fatalf("finding: %+v", d)
	}
	if migrationNumbers(t.TempDir()) != nil {
		t.Fatal("finding without a migrations directory")
	}
}
