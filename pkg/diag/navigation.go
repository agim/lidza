package diag

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// navigationRules is L025: browser navigation that breaks Back, reload or
// a shared link, in the app's frontend source, templates and scripts
// (the guide's "Navigation"):
//
//   - hash routing: an assignment to location.hash, a hashchange
//     listener, a hash history or router, a link to "#/..."; a view
//     gets a path through the router instead. Reading the fragment (an
//     in-page anchor, a secret kept out of the server's logs) is fine;
//   - history.back() or history.go(-1) by hand: it leaves the app when
//     the page was opened from elsewhere; BackLink goes back only when
//     the previous entry is the app's;
//   - a link labelled "Back" to a fixed page: it does not return where
//     the browser's Back would (the list with its filters and scroll).
//
// Comment lines are skipped; "lidza:ignore L025" on the line or the line
// before exempts it.
func navigationRules(root string) []Diagnostic {
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
		if !navigationExt[filepath.Ext(p)] || strings.HasSuffix(p, ".min.js") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || generatedWeb(string(data)) {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		out = append(out, checkNavigation(rel, string(data))...)
		return nil
	})
	return out
}

var (
	navigationExt = map[string]bool{
		".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".svelte": true, ".astro": true, ".vue": true,
		".html": true, ".tmpl": true, ".gohtml": true,
	}
	// Tests drive the browser's own Back (page.goBack) and may set a hash
	// on purpose; build output and dependencies are not the app's.
	skipNavigationDirs = map[string]bool{"node_modules": true, "dist": true, "bin": true, "vendor": true, "testdata": true, "e2e": true, "tests": true}

	navigationChecks = []struct {
		re      *regexp.Regexp
		message string
	}{
		{
			regexp.MustCompile(`\blocation\.hash\s*=[^=]|\bcreateHash(?:History|Router)\b|\bhashHistory\b|addEventListener\(\s*['"]hashchange['"]|\bonhashchange\b|svelte-spa-router|\b(?:href|to)\s*=\s*\{?\s*["'` + "`" + `]#/`),
			"hash routing: give the view a path through the router (/posts/hello-world, /posts/hello-world/comments, ?status=draft), so Back, reload and a shared link land on it. The fragment is for an anchor in the page or a secret kept from the server (read it, then drop it with history.replaceState). Guide: \"Navigation\"",
		},
		{
			regexp.MustCompile(`\bhistory\.(?:back\(\s*\)|go\(\s*-1\s*\))`),
			"history.back() by hand leaves the app when the page was opened from a link elsewhere or in a new tab: use BackLink with the parent's path (react: src/navigation.tsx; svelte: src/BackLink.svelte; astro: src/components/BackLink.astro; htmx: data-back on the link), which goes back only when the previous entry is this app's. Guide: \"Navigation\"",
		},
		{
			regexp.MustCompile(`(?i)<(?:a|Link)\b(?:[^>]|=>)*?>\s*(?:←|&larr;|‹|&lsaquo;|&lt;|<-|\{?\s*["']?←["']?\s*\}?)?\s*Back\b`),
			"a link labelled Back to one fixed page does not return where the browser's Back would (the list with its filters and scroll): use BackLink with the parent's path (react: src/navigation.tsx; svelte: src/BackLink.svelte; astro: src/components/BackLink.astro; htmx: data-back on the link) and name the parent (\"All posts\"). Guide: \"Navigation\"",
		},
	}
)

func checkNavigation(rel, content string) []Diagnostic {
	var out []Diagnostic
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "<!--") || strings.HasPrefix(t, "{/*") {
			continue
		}
		if strings.Contains(l, "lidza:ignore L025") || (i > 0 && strings.Contains(lines[i-1], "lidza:ignore L025")) {
			continue
		}
		for _, c := range navigationChecks {
			if loc := c.re.FindStringIndex(l); loc != nil {
				out = append(out, Diagnostic{Layer: "frontend", Tool: "lidza rules", Severity: "warning", Code: "L025", File: rel, Line: i + 1, Column: loc[0] + 1, Message: c.message})
			}
		}
	}
	return out
}
