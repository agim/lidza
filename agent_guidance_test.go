package lidza

import (
	"os"
	"strings"
	"testing"
)

// The repository's agent instructions live in AGENTS.md, which Codex
// reads; CLAUDE.md and GEMINI.md import it (@AGENTS.md), so the three
// agents read one text and it cannot drift.
func TestSharedAgentGuidance(t *testing.T) {
	if data, err := os.ReadFile("AGENTS.md"); err != nil || !strings.Contains(string(data), "Working agreements") {
		t.Fatalf("AGENTS.md holds the instructions: %v", err)
	}
	for _, name := range []string{"CLAUDE.md", "GEMINI.md"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "@AGENTS.md\n" {
			t.Errorf("%s must be the one line @AGENTS.md, so the instructions stay in AGENTS.md", name)
		}
	}
}
