//go:build evals

package evals

import (
	"strings"
	"testing"
)

func TestAccountPreferenceAPISurface(t *testing.T) {
	out, err := command(app, lidza, "api", "packs/auth", "--filter", "Preference")
	if err != nil {
		t.Fatalf("preference API inspection: %v\n%s", err, out)
	}
	for _, name := range []string{"Preferences", "PatchPreferences", "PreferenceState", "PreferencePatch", "PreferenceTable", "MaxPreferenceBytes"} {
		if !strings.Contains(string(out), name) {
			t.Errorf("auth API inspection missing %s", name)
		}
	}
}
