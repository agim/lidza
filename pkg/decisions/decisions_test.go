package decisions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	dir := t.TempDir()
	if es, err := Load(dir); err != nil || es != nil {
		t.Fatalf("empty: %v %v", es, err)
	}
	if _, err := Add(dir, "Stats in Rust", "", ""); err == nil {
		t.Fatal("why not required")
	}
	e, err := Add(dir, "Stats in a Rust pack", "Word statistics run over user text: contained.", "packs/stats")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, "Postgres over SQLite", "The jobs and auth packs need it.", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "docs", "decisions.md"))
	if !strings.HasPrefix(string(data), "# Decisions") || !strings.Contains(string(data), "## "+e.Date+": Stats in a Rust pack\n\nWhy: Word statistics") || !strings.Contains(string(data), "Touches: packs/stats") {
		t.Fatalf("log:\n%s", data)
	}
	es, err := Load(dir)
	if err != nil || len(es) != 2 || es[0].Title != "Stats in a Rust pack" || es[0].Touches != "packs/stats" || es[1].Why != "The jobs and auth packs need it." || es[1].Touches != "" {
		t.Fatalf("load: %+v %v", es, err)
	}
	if created, err := Ensure(dir, "x"); err != nil || created {
		t.Fatalf("ensure on an existing log: %v %v", created, err)
	}
}
