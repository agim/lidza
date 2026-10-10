package diag

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/schema"
)

func TestUnsafeStatements(t *testing.T) {
	sql := `-- hand written
CREATE TABLE tag (id int);
CREATE INDEX tag_id_idx ON tag (id);
ALTER TABLE tag ADD CONSTRAINT tag_fk FOREIGN KEY (id) REFERENCES post(id);
ALTER TABLE post RENAME COLUMN title TO headline;
ALTER TABLE post ALTER COLUMN views TYPE bigint USING views::bigint;
ALTER TABLE post DROP COLUMN body; -- review: data loss
ALTER TABLE post ADD COLUMN token uuid DEFAULT gen_random_uuid();
ALTER TABLE post ADD COLUMN status text DEFAULT 'draft';
ALTER TABLE post ALTER COLUMN slug SET NOT NULL;
ALTER TABLE post ADD CONSTRAINT post_author_fkey FOREIGN KEY (author_id) REFERENCES author(id);
ALTER TABLE post ADD CONSTRAINT post_author2_fkey FOREIGN KEY (a2) REFERENCES author(id) NOT VALID;
ALTER TABLE post ADD CONSTRAINT post_slug_key UNIQUE (slug);
CREATE INDEX post_slug_idx ON post (slug);
CREATE INDEX CONCURRENTLY post_x_idx ON post (x);
-- lidza:ignore L021
CREATE INDEX post_small_idx ON post (y);
DROP TABLE old_things;
`
	got := unsafeStatements("db/migrations/x.up.sql", sql)
	var lines []int
	for _, d := range got {
		if d.Code != "L021" || d.Severity != "warning" {
			t.Fatalf("%+v", d)
		}
		lines = append(lines, d.Line)
	}
	want := []int{5, 6, 7, 8, 10, 11, 13, 14, 18}
	if len(lines) != len(want) {
		t.Fatalf("lines %v, want %v\n%+v", lines, want, got)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("lines %v, want %v", lines, want)
		}
	}
}

// TestGeneratedMigrationsAreSafe: what lidza gen writes for a table with
// rows passes the rule, but for the drop it cannot make safe.
func TestGeneratedMigrationsAreSafe(t *testing.T) {
	v1, _ := schema.Parse("model Author {\n  id uuid @id\n}\nmodel Post {\n  id uuid @id\n  title string?\n  slug string\n  body text?\n  authorId uuid?\n}\n")
	v2, _ := schema.Parse("model Author {\n  id uuid @id\n}\nmodel Post {\n  id uuid @id\n  title string @search\n  slug string @unique\n  authorId uuid? @ref(Author) @index\n}\nmodel Tag {\n  id uuid @id\n  name string @index\n}\n")
	m := schema.Diff(v1, v2, 2)
	up, _ := m.Files()
	after, _ := m.AfterFiles("0003")
	got := append(unsafeStatements("a", up), unsafeStatements("b", after)...)
	if len(got) != 1 || !strings.Contains(got[0].Message, "drop") {
		t.Fatalf("%+v\n%s\n%s", got, up, after)
	}
}

// TestMigrationSafetyReadsNewOnly: committed migrations are not read.
func TestMigrationSafetyReadsNewOnly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "db", "migrations")
	os.MkdirAll(dir, 0o755)
	write := func(name string) {
		os.WriteFile(filepath.Join(dir, name), []byte("ALTER TABLE post RENAME TO article;\n"), 0o644)
	}
	write("0001_old.up.sql")
	if got := migrationSafety(root); len(got) != 1 {
		t.Fatalf("outside git: %+v", got)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "x")
	write("0002_new.up.sql")
	got := migrationSafety(root)
	if len(got) != 1 || got[0].File != "db/migrations/0002_new.up.sql" {
		t.Fatalf("in git: %+v", got)
	}
}
