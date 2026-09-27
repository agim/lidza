package pack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/schema"
)

const artworkSchema = `model Artwork {
  id         uuid     @id
  url        string
  artworkIds uuid[]
  html       text?
  providerId string?
}
`

// TestSyncSQLCNames: the go options of sqlc.yaml get the schema's
// initialisms and renames, once, between markers kept in step; a file
// that sets its own is left alone with a note. With sqlc installed, the
// generated models use the names of schema/.
func TestSyncSQLCNames(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, schema.FileName), []byte(artworkSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Add(root, "db"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, SQLCFile)
	cfg, _ := os.ReadFile(p)
	for _, want := range []string{
		"      go:\n        " + sqlcNamesStart + "\n        initialisms: [\"id\", \"url\", ",
		"        rename:\n          artwork_ids: \"ArtworkIDs\"\n        " + sqlcNamesEnd + "\n        package: \"queries\"",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("sqlc.yaml missing %q:\n%s", want, cfg)
		}
	}
	s, err := schema.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := SyncSQLCNames(root, s); err != nil || res.Changed || res.Added {
		t.Fatalf("second sync: %+v %v", res, err)
	}

	// A file from before: the block is added, and kept in step after.
	old := strings.Replace(string(cfg), string(cfg[strings.Index(string(cfg), "        "+sqlcNamesStart):strings.Index(string(cfg), sqlcNamesEnd)+len(sqlcNamesEnd)+1]), "", 1)
	os.WriteFile(p, []byte(old), 0o644)
	if res, err := SyncSQLCNames(root, s); err != nil || !res.Changed || !res.Added {
		t.Fatalf("old file: %+v %v", res, err)
	}
	s2 := *s
	s2.Models = []*schema.Model{{Name: "Page", Table: "page", Fields: []*schema.Field{{Name: "imageUrls", Type: "string", Array: true}}}}
	if res, err := SyncSQLCNames(root, &s2); err != nil || !res.Changed || res.Added {
		t.Fatalf("schema change: %+v %v", res, err)
	}
	if cfg, _ := os.ReadFile(p); !strings.Contains(string(cfg), "image_urls: \"ImageURLs\"") || strings.Contains(string(cfg), "artwork_ids") || strings.Count(string(cfg), "initialisms:") != 1 {
		t.Fatalf("kept in step:\n%s", cfg)
	}

	custom := filepath.Join(t.TempDir(), SQLCFile)
	os.WriteFile(custom, []byte("version: \"2\"\nsql:\n  - gen:\n      go:\n        initialisms: [\"id\"]\n"), 0o644)
	if res, err := SyncSQLCNames(filepath.Dir(custom), s); err != nil || res.Changed || !strings.Contains(res.Note, "ArtworkIDs") {
		t.Fatalf("custom file: %+v %v", res, err)
	}

	if _, err := exec.LookPath("sqlc"); err != nil {
		t.Skip("sqlc not installed")
	}
	SyncSQLCNames(root, s)
	os.MkdirAll(filepath.Join(root, "db"), 0o755)
	os.WriteFile(filepath.Join(root, schema.SQLFile), []byte(schema.GenerateSQL(s)), 0o644)
	os.WriteFile(filepath.Join(root, QueriesDir, "queries.sql"), []byte("-- name: GetArtwork :one\nSELECT * FROM artwork WHERE id = $1;\n\n-- name: SetURL :exec\nUPDATE artwork SET url = $1, artwork_ids = $2 WHERE id = $3;\n"), 0o644)
	cmd := exec.Command("sqlc", "generate")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlc: %v\n%s", err, out)
	}
	models, _ := os.ReadFile(filepath.Join(root, "db", "queries", "gen", "models.go"))
	queries, _ := os.ReadFile(filepath.Join(root, "db", "queries", "gen", "queries.sql.go"))
	for _, f := range s.Models[0].Fields {
		if !strings.Contains(string(models), "\t"+schema.GoName(f.Name)+" ") {
			t.Errorf("models.go lacks %s:\n%s", schema.GoName(f.Name), models)
		}
	}
	if !strings.Contains(string(queries), "\tArtworkIDs ") || !strings.Contains(string(queries), "\tURL ") {
		t.Errorf("query params:\n%s", queries)
	}
}
