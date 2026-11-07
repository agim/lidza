package diag

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// uiRules is L026: the browser's own controls in a page of an app that
// has the app's components (src/ui.tsx or src/ui/): a <select> (the
// browser's dropdown, which no theme reaches), a <dialog> written by
// hand, and confirm(), alert() or prompt(). The app's Select, Dialog and
// Drawer keep the keyboard and screen-reader behaviour with the app's
// look (the guide's "Pages and components"). The components themselves
// are exempt, as are tests, build output and dependencies. Comment lines
// are skipped; "lidza:ignore L026" on the line or the line before
// exempts it. An app without src/ui.tsx is not checked: the rule asks
// for what the app has.
func uiRules(root string) []Diagnostic {
	if !hasAppUI(root) {
		return nil
	}
	var out []Diagnostic
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (skipNavigationDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(p)
		if (ext != ".tsx" && ext != ".jsx" && ext != ".ts" && ext != ".js") || strings.HasSuffix(p, ".min.js") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		if rel == "src/ui.tsx" || strings.HasPrefix(rel, "src/ui/") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || generatedWeb(string(data)) {
			return nil
		}
		out = append(out, checkUI(rel, string(data))...)
		return nil
	})
	return out
}

// hasAppUI reports whether the app has the components the rule asks for.
func hasAppUI(root string) bool {
	if _, err := os.Stat(filepath.Join(root, "src", "ui.tsx")); err == nil {
		return true
	}
	if st, err := os.Stat(filepath.Join(root, "src", "ui")); err == nil && st.IsDir() {
		return true
	}
	return false
}

var uiChecks = []struct {
	re      *regexp.Regexp
	message string
}{
	{regexp.MustCompile(`<select[\s>/]`), "<select> is the browser's dropdown, which no theme reaches: use Select from src/ui.tsx (the guide's \"Pages and components\")"},
	{regexp.MustCompile(`<dialog[\s>/]`), "a <dialog> written by hand: use Dialog or Drawer from src/ui.tsx, which carry the title, the actions and the close behaviour"},
	{regexp.MustCompile(`(^|[^\w.$])(window\.)?(confirm|alert|prompt)\(`), "the browser's own dialog (confirm, alert or prompt): use Dialog from src/ui.tsx with the question and its actions"},
}

func checkUI(rel, content string) []Diagnostic {
	var out []Diagnostic
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "{/*") {
			continue
		}
		if strings.Contains(l, "lidza:ignore L026") || (i > 0 && strings.Contains(lines[i-1], "lidza:ignore L026")) {
			continue
		}
		for _, c := range uiChecks {
			if loc := c.re.FindStringIndex(l); loc != nil {
				out = append(out, Diagnostic{Layer: "frontend", Tool: "lidza rules", Severity: "warning", Code: "L026", File: rel, Line: i + 1, Column: loc[0] + 1, Message: c.message})
			}
		}
	}
	return out
}
