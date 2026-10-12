package diag

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// formTargets is L022: a form that submits to something other than an
// https or same-origin address, in the app's templates and components.
// action="mailto:..." on an https page is reported by browsers as mixed
// content on load (Lighthouse best practices fail), and sends nothing
// without a mail client; an http: target sends the data unencrypted. A
// mailto: link is not a form and is fine.
func formTargets(root string) []Diagnostic {
	var out []Diagnostic
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (skipFormDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !formExt[filepath.Ext(p)] {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || generatedWeb(string(data)) {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		out = append(out, checkFormTargets(rel, string(data))...)
		return nil
	})
	return out
}

var (
	formExt      = map[string]bool{".html": true, ".tmpl": true, ".gohtml": true, ".tsx": true, ".jsx": true, ".svelte": true, ".astro": true, ".vue": true}
	skipFormDirs = map[string]bool{"node_modules": true, "dist": true, "bin": true, "vendor": true, "testdata": true}
	// A form's action, across lines and past arrow functions in JSX
	// attributes; a formaction on any element.
	formAction = regexp.MustCompile(`(?is)<form\b(?:[^>]|=>)*?\baction\s*=\s*\{?\s*["'` + "`" + `]([^"'` + "`" + `]*)`)
	formAttr   = regexp.MustCompile(`(?i)\bformaction\s*=\s*\{?\s*["'` + "`" + `]([^"'` + "`" + `]*)`)
	schemeRe   = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*):`)
)

func checkFormTargets(rel, content string) []Diagnostic {
	var out []Diagnostic
	lines := strings.Split(content, "\n")
	lineOf := func(off int) int { return strings.Count(content[:off], "\n") }
	for _, re := range []*regexp.Regexp{formAction, formAttr} {
		for _, m := range re.FindAllStringSubmatchIndex(content, -1) {
			target := strings.TrimSpace(content[m[2]:m[3]])
			sm := schemeRe.FindStringSubmatch(target)
			if sm == nil {
				continue // a path, a template expression: same origin
			}
			scheme := strings.ToLower(sm[1])
			msg := ""
			switch scheme {
			case "https":
				continue
			case "http":
				msg = "a form that submits to " + target + " over http: the data travels unencrypted and an https page reports mixed content; use https or a same-origin path"
			default:
				msg = "a form that submits to " + scheme + ": (" + target + "): an https page reports it as mixed content on load (Lighthouse best practices), and it sends nothing without a local mail client. Submit to a same-origin path whose handler does the work (the mail pack sends email), or keep a same-origin action and handle the submit in script; a mailto: link is fine"
			}
			i := lineOf(m[2])
			if strings.Contains(lines[i], "lidza:ignore L022") || (i > 0 && strings.Contains(lines[i-1], "lidza:ignore L022")) {
				continue
			}
			out = append(out, Diagnostic{Layer: "frontend", Tool: "lidza rules", Severity: "warning", Code: "L022",
				File: rel, Line: i + 1, Column: m[2] - strings.LastIndex(content[:m[2]], "\n"), Message: msg})
		}
	}
	return out
}
