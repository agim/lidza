package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentFileSize(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) { os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644) }
	write("CLAUDE.md", "@AGENTS.md\n")
	write("AGENTS.md", "# app\n\n## Team notes\n\nLasting facts are in docs/notes.md.\n")
	if d := agentFileSize(dir); len(d) != 0 {
		t.Fatalf("small file: %+v", d)
	}
	// Notes still in the file: say so, with the move.
	write("AGENTS.md", "# app\n\n## Team notes\n\n- 2026-09-29: Prices are in euros.\n- 2026-10-01: Deploys on Fridays are fine.\n\n## After\n\n- 2026-10-02: not a note, another section.\n")
	d := agentFileSize(dir)
	if len(d) != 1 || d[0].Code != "L024" || !strings.Contains(d[0].Message, "2 team notes") || !strings.Contains(d[0].Message, "lidza gen") {
		t.Fatalf("notes: %+v", d)
	}
	// Too big: the budget, in KB and tokens, and where things go.
	write("AGENTS.md", "# app\n\n"+strings.Repeat("- a rule the app added, one of many.\n", 500))
	d = agentFileSize(dir)
	if len(d) != 1 || !strings.Contains(d[0].Message, "AGENTS.md is 18 KB") || !strings.Contains(d[0].Message, "docs/notes.md") {
		t.Fatalf("big: %+v", d)
	}
	// A GEMINI.md that is not the import counts too; the stub does not.
	write("GEMINI.md", strings.Repeat("x", 17*1024))
	if d := agentFileSize(dir); len(d) != 2 || d[1].File != "GEMINI.md" {
		t.Fatalf("gemini: %+v", d)
	}
}
