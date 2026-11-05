//go:build evals

package evals

import (
	"strings"
	"testing"
)

// Every surface an agent reads carries the navigation rules: views have
// addresses with slugs, filters are search parameters that replace the
// history entry, no hash routing, BackLink for back links, the title
// follows the view; the rule that flags the breaks is named.
func TestNavigationGuidance(t *testing.T) {
	for _, path := range []string{"AGENTS.md", "docs/lidza-guide.md", "docs/lidza-recipes.md", ".agents/skills/keep-view-in-address/SKILL.md", ".claude/skills/keep-view-in-address/SKILL.md"} {
		body := mustRead(t, path)
		for _, phrase := range []string{"slug", "search parameter", "replace", "hash", "BackLink", "L025"} {
			if !strings.Contains(body, phrase) {
				t.Errorf("%s does not carry the navigation rule: %s", path, phrase)
			}
		}
	}
	guide := mustRead(t, "docs/lidza-guide.md")
	for _, phrase := range []string{"## Navigation", "push", "secret", "<a href>", "title follows the view", "Scroll", "A deep link and a reload", "validateSearch", "useTitle", "scrollRestoration: true", "page.goBack()"} {
		if !strings.Contains(guide, phrase) {
			t.Errorf("the guide's Navigation section lacks %q", phrase)
		}
	}
	// The helpers and their browser test ship with the app.
	for _, path := range []string{"src/navigation.tsx", "e2e/navigation.spec.ts", "src/pages/items/Item.tsx"} {
		if body := mustRead(t, path); body == "" {
			t.Errorf("%s is empty", path)
		}
	}
	if body := mustRead(t, "src/navigation.tsx"); !strings.Contains(body, "export const BackLink") || !strings.Contains(body, "export function useTitle") {
		t.Errorf("src/navigation.tsx lacks BackLink or useTitle")
	}
}

// Hash routing, history.back() by hand and a Back link fixed to one page
// are L025 warnings; an in-page anchor, reading a token from the fragment
// and BackLink are not.
func TestNavigationBreaks(t *testing.T) {
	edit(t, map[string]string{
		"src/pages/Broken.tsx": `import { Link } from '@tanstack/react-router'
import { BackLink } from '../navigation'

export function Broken() {
  const token = location.hash.slice(1)
  const open = (id: string) => {
    location.hash = '#/posts/' + id
  }
  return (
    <div>
      <Link to="/">Back</Link>
      <button type="button" onClick={() => history.back()}>Cancel</button>
      <button type="button" onClick={() => open(token)}>Open</button>
      <a href="#details">Details</a>
      <BackLink to="/">Home</BackLink>
    </div>
  )
}
`,
	})
	r := check(t)
	expect(t, r,
		expectation{code: "L025", severity: "warning", file: "src/pages/Broken.tsx", message: "hash routing"},
		expectation{code: "L025", severity: "warning", file: "src/pages/Broken.tsx", message: "history.back()"},
		expectation{code: "L025", severity: "warning", file: "src/pages/Broken.tsx", message: "labelled Back"},
	)
	n := 0
	for _, d := range r.Diagnostics {
		if d.Code == "L025" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d L025 findings, want 3:\n%s", n, dump(r))
	}
}
