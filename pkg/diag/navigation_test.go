package diag

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/templates"
)

func TestNavigationRules(t *testing.T) {
	flagged := map[string]string{
		"hash assignment":      "location.hash = '#/posts/' + id",
		"window hash":          "window.location.hash = `#/settings`",
		"hash listener":        "window.addEventListener('hashchange', route)",
		"onhashchange":         "window.onhashchange = route",
		"hash history":         "const history = createHashHistory()",
		"hash link":            `<a href="#/posts/1">Post</a>`,
		"hash Link":            `<Link to="#/settings">Settings</Link>`,
		"history.back":         "<button onClick={() => history.back()}>Back</button>",
		"router back":          "router.history.back()",
		"go -1":                "window.history.go(-1)",
		"fixed back link":      `<Link to="/posts" className="text-sm">Back</Link>`,
		"arrow back link":      `<a href="/posts">← Back to posts</a>`,
		"entity back link":     `<a href="/posts">&larr; Back</a>`,
		"back after arrow fn":  `<Link to="/posts" onClick={() => track('back')}>Back</Link>`,
		"svelte fixed back":    `<a href="/posts" class="back">Back</a>`,
		"hash router package":  "import Router from 'svelte-spa-router'",
		"lowercase back label": `<a href="/">back</a>`,
	}
	for name, line := range flagged {
		if got := checkNavigation("src/x.tsx", "const a = 1\n"+line+"\n"); len(got) != 1 || got[0].Code != "L025" || got[0].Line != 2 {
			t.Errorf("%s: %q gave %v, want one L025 on line 2", name, line, got)
		}
	}
	allowed := map[string]string{
		"in-page anchor":      `<a href="#install">Install</a>`,
		"reading a token":     "const token = location.hash.slice(1)",
		"comparing the hash":  "if (location.hash === '#new') open()",
		"dropping the secret": "history.replaceState(null, '', location.pathname + location.search)",
		"BackLink":            `<BackLink to="/posts">All posts</BackLink>`,
		"data-back link":      `<a href="/posts" class="back" data-back>All posts</a>`,
		"backlog link":        `<a href="/backlog">Backlog</a>`,
		"background link":     `<Link to="/settings/background">Background</Link>`,
		"comment":             "// never location.hash = '#/x', never history.back()",
		"jsdoc":               " * history.back() leaves the app",
		"html comment":        "<!-- <a href=\"/\">Back</a> -->",
		"ignored":             "history.back() // lidza:ignore L025 (checked above)",
		"playwright":          "await page.goBack()",
	}
	for name, line := range allowed {
		if got := checkNavigation("src/x.tsx", line+"\n"); len(got) != 0 {
			t.Errorf("%s: %q gave %v, want none", name, line, got)
		}
	}
	if got := checkNavigation("src/x.ts", "// lidza:ignore L025 (the previous entry is this app's)\nhistory.back()\n"); len(got) != 0 {
		t.Errorf("ignore on the line before: %v", got)
	}
}

// The walk covers the frontend, the htmx views and static scripts, and
// leaves out dependencies, builds, tests and minified files.
func TestNavigationRulesWalk(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bad := "location.hash = '#/x'\n"
	write("src/pages/Posts.tsx", bad)
	write("app/views/post.html", `<a href="/posts">Back</a>`+"\n")
	write("app/static/nav.js", "history.back()\n")
	write("node_modules/router/index.js", bad)
	write("dist/assets/index.js", bad)
	write("e2e/nav.spec.ts", bad)
	write("app/static/htmx.min.js", bad)
	got := map[string]bool{}
	for _, d := range navigationRules(dir) {
		got[d.File] = true
	}
	want := map[string]bool{"src/pages/Posts.tsx": true, "app/views/post.html": true, "app/static/nav.js": true}
	if len(got) != len(want) {
		t.Errorf("flagged %v, want %v", got, want)
	}
	for f := range want {
		if !got[f] {
			t.Errorf("%s not flagged", f)
		}
	}
}

// What the templates ship follows its own rule: an app starts with no
// L025 finding.
func TestTemplatesFollowNavigationRules(t *testing.T) {
	err := fs.WalkDir(templates.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := filepath.Ext(strings.TrimSuffix(p, ".tmpl"))
		if !navigationExt[ext] || strings.HasSuffix(p, ".min.js") || strings.Contains(p, "/e2e/") {
			return nil
		}
		data, err := fs.ReadFile(templates.FS, p)
		if err != nil {
			return err
		}
		for _, d := range checkNavigation(p, string(data)) {
			t.Errorf("%s:%d: %s", p, d.Line, d.Message)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
