//go:build evals

package evals

import (
	"strings"
	"testing"
)

func TestLiveUpdateAcceptanceGuidance(t *testing.T) {
	for _, path := range []string{"AGENTS.md", "docs/lidza-guide.md", ".agents/skills/publish-live-updates/SKILL.md", ".claude/skills/publish-live-updates/SKILL.md"} {
		body := mustRead(t, path)
		for _, phrase := range []string{"Live-update acceptance gate", "desktop", "phone", "slow", "failed", "reconnect", "pause/resume", "drafts", "unverified"} {
			if !strings.Contains(body, phrase) {
				t.Errorf("%s does not carry required live-update acceptance: %s", path, phrase)
			}
		}
	}
}
