package list

import (
	"strings"
	"testing"
)

// FuzzSplit: headlines hold stored text; no part is empty, and text
// without markers comes back unchanged.
func FuzzSplit(f *testing.F) {
	f.Add("a " + HighlightStart + "hit" + HighlightStop + " b")
	f.Add(HighlightStart + HighlightStart + HighlightStop)
	f.Add(HighlightStop + "x" + HighlightStart)
	f.Fuzz(func(t *testing.T, s string) {
		var b strings.Builder
		for _, p := range Split(s) {
			if p.Text == "" {
				t.Fatalf("empty part in %q", s)
			}
			b.WriteString(p.Text)
		}
		if b.Len() > len(s) {
			t.Fatalf("parts of %q are longer than it", s)
		}
		if !strings.Contains(s, HighlightStart) && b.String() != s {
			t.Fatalf("plain text %q changed to %q", s, b.String())
		}
	})
}
