package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIRules(t *testing.T) {
	flagged := map[string]string{
		"select":         `<select value={status} onChange={set}>`,
		"self-closing":   `<select/>`,
		"dialog":         `<dialog open>`,
		"confirm":        `if (!confirm('Delete?')) return`,
		"window confirm": `if (window.confirm('Delete?')) remove()`,
		"alert":          `alert('Saved')`,
		"prompt":         `const name = prompt('Name?')`,
	}
	for name, line := range flagged {
		if got := checkUI("src/pages/X.tsx", line+"\n"); len(got) != 1 || got[0].Code != "L026" {
			t.Errorf("%s: %d findings for %q: %+v", name, len(got), line, got)
		}
	}
	allowed := map[string]string{
		"app select":     `<Select label="Status" value={status} onChange={set} options={options} />`,
		"method":         `const ok = await api.confirm({ id })`,
		"property":       `this.alert(message)`,
		"identifier":     `const prompts = listPrompts()`,
		"selected prop":  `<option aria-selected="true">`,
		"comment":        `// a <select> would be the browser's`,
		"ignored":        `<select> {/* lidza:ignore L026 */}`,
		"ignored before": "// lidza:ignore L026\n<select>",
		"type reference": `type Props = { dialog: boolean }`,
		"string mention": `const hint = "the Select component"`,
	}
	for name, line := range allowed {
		if got := checkUI("src/pages/X.tsx", line+"\n"); len(got) != 0 {
			t.Errorf("%s: unexpected findings for %q: %+v", name, line, got)
		}
	}
}

func TestUIRulesWalk(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	write("src/pages/Posts.tsx", "<select>\n")
	write("e2e/posts.spec.ts", "page.locator('select')\n<select>\n")
	write("node_modules/x/index.js", "<select>\n")
	// Without the app's components the rule asks for nothing.
	if got := uiRules(dir); len(got) != 0 {
		t.Fatalf("without src/ui.tsx: %+v", got)
	}
	write("src/ui.tsx", "export function Select() { return <select /> }\n<dialog>\n")
	got := uiRules(dir)
	if len(got) != 1 || got[0].File != "src/pages/Posts.tsx" || !strings.Contains(got[0].Message, "Select from src/ui.tsx") {
		t.Fatalf("with src/ui.tsx: %+v", got)
	}
}
