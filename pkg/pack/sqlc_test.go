package pack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/schema"
)

const artworkSchema = `enum Source { api ids plain }
model Artwork {
  id         uuid     @id
  url        string
  artworkIds uuid[]
  html       text?
  providerId string?
  source     Source
}
model ApiKey {
  id uuid @id
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
		"        rename:\n          artwork_ids: \"ArtworkIDs\"\n          source_ids: \"SourceIDs\"\n        " + sqlcNamesEnd + "\n        package: \"queries\"",
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
	for _, want := range []string{"type APIKey struct", "type Source string", "\tSourceAPI ", "\tSourceIDs ", "\tSourcePlain "} {
		if !strings.Contains(string(models), want) || schema.SQLCName("api_key") != "APIKey" {
			t.Errorf("models.go lacks %q:\n%s", want, models)
		}
	}
	if !strings.Contains(string(queries), "\tArtworkIDs ") || !strings.Contains(string(queries), "\tURL ") {
		t.Errorf("query params:\n%s", queries)
	}
}

// Named query parameters get renames too: sqlc.narg('ids') is spelled
// IDs, not Ids, like a column would be.
func TestSQLCParamNames(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, schema.FileName), []byte(artworkSchema), 0o644)
	if _, _, err := Add(root, "db"); err != nil {
		t.Fatal(err)
	}
	q := "-- name: ListArtworks :many\nSELECT * FROM artwork WHERE (sqlc.narg('ids')::uuid[] IS NULL OR id = ANY(sqlc.narg('ids')::uuid[])) AND url <> @skip_url AND html <> 'a@example.com';\n"
	os.WriteFile(filepath.Join(root, QueriesDir, "queries.sql"), []byte(q), 0o644)
	if got := strings.Join(queryParams(root), ","); got != "ids,skip_url" {
		t.Fatalf("params: %s", got)
	}
	s, _ := schema.Load(root)
	if _, err := SyncSQLCNames(root, s); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(root, SQLCFile))
	if !strings.Contains(string(cfg), "ids: \"IDs\"") {
		t.Fatalf("no rename for the parameter:\n%s", cfg)
	}
	if _, err := exec.LookPath("sqlc"); err != nil {
		t.Skip("sqlc not installed")
	}
	os.WriteFile(filepath.Join(root, schema.SQLFile), []byte(schema.GenerateSQL(s)), 0o644)
	cmd := exec.Command("sqlc", "generate")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlc: %v\n%s", err, out)
	}
	queries, _ := os.ReadFile(filepath.Join(root, "db", "queries", "gen", "queries.sql.go"))
	if !strings.Contains(string(queries), "\tIDs ") || !strings.Contains(string(queries), "\tSkipURL ") {
		t.Fatalf("params:\n%s", queries)
	}
}
