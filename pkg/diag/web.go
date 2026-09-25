package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// WebRules are the checks `lidza check` applies to the frontend source
// (src/), against the two ways a frontend drifts from the API:
//
//   - L003: a hand-written fetch of /api; the generated client in
//     @lidza/client is typed from the handlers, so a call through it fails
//     `lidza check` when the API changes, a fetch does not;
//   - L004: an import of a package that package.json does not declare; it
//     resolves on one machine through a transitive dependency and breaks
//     on the next, or does not exist at all.
//
// A line, or the line after a comment, containing "lidza:ignore L003" is
// exempt from that rule.
func WebRules(root string) []Diagnostic {
	src := filepath.Join(root, "src")
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	declared := declaredPackages(root)
	var out []Diagnostic
	_ = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !webExt[filepath.Ext(p)] {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		out = append(out, checkWebFile(rel, string(data), declared)...)
		return nil
	})
	return out
}

var webExt = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".svelte": true, ".astro": true, ".vue": true}

var (
	fetchAPI = regexp.MustCompile("\\bfetch\\(\\s*[`'\"]/api")
	// import x from 'y', import 'y', export ... from 'y', import('y'),
	// require('y').
	importFrom = regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*\(?\s*|\brequire\(\s*|^\s*import\s+)['"]([^'"\n]+)['"]`)
)

func checkWebFile(rel, content string, declared map[string]bool) []Diagnostic {
	var out []Diagnostic
	lines := strings.Split(content, "\n")
	ignored := func(i int, code string) bool {
		for _, j := range []int{i, i - 1} {
			if j >= 0 && strings.Contains(lines[j], "lidza:ignore "+code) {
				return true
			}
		}
		return false
	}
	for i, l := range lines {
		if loc := fetchAPI.FindStringIndex(l); loc != nil && !ignored(i, "L003") {
			out = append(out, Diagnostic{Layer: "frontend", Tool: "lidza rules", Severity: "warning", Code: "L003", File: rel, Line: i + 1, Column: loc[0] + 1,
				Message: "hand-written fetch of /api: call the API through @lidza/client (api.<operation>), generated from the handlers and checked by lidza check"})
		}
		if declared == nil {
			continue
		}
		for _, m := range importFrom.FindAllStringSubmatchIndex(l, -1) {
			spec := l[m[2]:m[3]]
			name, bare := packageName(spec)
			if !bare || declared[name] || ignored(i, "L004") {
				continue
			}
			out = append(out, Diagnostic{Layer: "frontend", Tool: "lidza rules", Severity: "error", Code: "L004", File: rel, Line: i + 1, Column: m[2] + 1,
				Message: "import of " + name + ", which package.json does not declare: add it with `npm install " + name + "` (or `--save-dev`), or use a declared package"})
		}
	}
	return out
}

// packageName returns the npm package of an import specifier and whether
// the specifier is a bare one (not relative, absolute, an alias or a
// scheme).
func packageName(spec string) (string, bool) {
	switch {
	case spec == "", strings.HasPrefix(spec, "."), strings.HasPrefix(spec, "/"), strings.HasPrefix(spec, "#"),
		strings.HasPrefix(spec, "@lidza/"), strings.HasPrefix(spec, "@/"), strings.HasPrefix(spec, "~"), strings.HasPrefix(spec, "$"),
		strings.Contains(spec, ":"):
		return "", false
	}
	parts := strings.Split(spec, "/")
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		return parts[0] + "/" + parts[1], true
	}
	return parts[0], true
}

// declaredPackages reads every dependency section of package.json; nil
// when there is no package.json, which disables L004.
func declaredPackages(root string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, section := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"} {
		var deps map[string]string
		if raw, ok := pkg[section]; ok && json.Unmarshal(raw, &deps) == nil {
			for name := range deps {
				out[name] = true
			}
		}
	}
	return out
}

// DeclaredPackages lists the packages package.json declares, sorted.
func DeclaredPackages(root string) []string {
	m := declaredPackages(root)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
