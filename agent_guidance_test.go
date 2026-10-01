package lidza

import (
	"bytes"
	"os"
	"testing"
)

func TestSharedAgentGuidance(t *testing.T) {
	canonical, err := os.ReadFile("CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "GEMINI.md"} {
		guidance, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(guidance, canonical) {
			t.Errorf("%s must carry the same instructions as CLAUDE.md", name)
		}
	}
}
