// Package apidoc renders the framework's public Go API from its sources as
// the app resolves them (its go.mod, replace directives included), so an
// agent reads the signatures it can call instead of guessing them. `lidza
// api` prints it; `lidza mcp` serves it as lidza://api and lidza_api.
package apidoc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Module is the framework's module path.
const Module = "github.com/agim/lidza"

// Public lists the packages app code imports, relative to the module root
// ("" is the root package). Every packs/<name> package is public too.
var Public = []string{"", "pkg/router", "pkg/middleware", "pkg/lidzatest", "pkg/report", "pkg/resilience", "pkg/env"}

// ModuleDir returns the directory holding the framework's sources for the
// project in dir: dir itself when it is the framework, otherwise what the
// go tool resolves from the project's go.mod.
func ModuleDir(ctx context.Context, dir string) (string, error) {
	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			l = strings.TrimSpace(l)
			if l == "module "+Module {
				return dir, nil
			}
			// A local checkout (lidza new --lidza-dir) is a replace
			// directive to a directory; use it without the go tool.
			if f := strings.Fields(l); len(f) >= 3 && f[0] == "replace" && f[1] == Module && f[2] == "=>" && len(f) == 4 && (strings.HasPrefix(f[3], "/") || strings.HasPrefix(f[3], ".")) {
				p := f[3]
				if !filepath.IsAbs(p) {
					p = filepath.Join(dir, p)
				}
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return p, nil
				}
			}
		}
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", Module)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("go list %s: %s", Module, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	d := strings.TrimSpace(string(out))
	if d == "" {
		return "", fmt.Errorf("%s is not a dependency of %s", Module, dir)
	}
	return d, nil
}

// Packages lists every importable package of the framework at moduleDir,
// relative to it, "" for the root. Commands, internal, test data and
// examples are left out.
func Packages(moduleDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(moduleDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != moduleDir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "cmd" || name == "internal" || name == "testdata" || name == "examples" || name == "node_modules" || name == "templates" || name == "core") {
				return filepath.SkipDir
			}
			if p != moduleDir {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(moduleDir, filepath.Dir(p))
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		if len(out) == 0 || out[len(out)-1] != rel {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return dedupe(out), nil
}

func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

// AppPackages is Public plus the pack packages present at moduleDir.
func AppPackages(moduleDir string) []string {
	out := append([]string(nil), Public...)
	entries, _ := os.ReadDir(filepath.Join(moduleDir, "packs"))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, "packs/"+e.Name())
		}
	}
	return out
}

// ImportPath is the import path of a package relative path.
func ImportPath(rel string) string {
	if rel == "" {
		return Module
	}
	return Module + "/" + rel
}

// Rel turns an import path under the module into its relative path; ok is
// false for paths outside the module.
func Rel(importPath string) (rel string, ok bool) {
	if importPath == Module {
		return "", true
	}
	if strings.HasPrefix(importPath, Module+"/") {
		return strings.TrimPrefix(importPath, Module+"/"), true
	}
	return "", false
}

// Render writes the public API of the packages (relative paths; nil means
// AppPackages) as markdown. filter, when set, keeps only the declarations
// whose name contains it (case-insensitive).
func Render(moduleDir string, rels []string, filter string) (string, error) {
	if rels == nil {
		rels = AppPackages(moduleDir)
	}
	var b strings.Builder
	for _, rel := range rels {
		text, err := renderPackage(moduleDir, rel, strings.ToLower(filter))
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}
	return b.String(), nil
}

func renderPackage(moduleDir, rel, filter string) (string, error) {
	dir := filepath.Join(moduleDir, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ImportPath(rel), err)
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	var parsed []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return "", fmt.Errorf("%s: %w", ImportPath(rel), err)
		}
		if f.Name.Name == "main" || strings.HasSuffix(f.Name.Name, "_test") {
			continue
		}
		files[filepath.Join(dir, name)] = f
		parsed = append(parsed, f)
	}
	if len(parsed) == 0 {
		return "", nil
	}
	d, err := doc.NewFromFiles(fset, parsed, ImportPath(rel))
	if err != nil {
		return "", fmt.Errorf("%s: %w", ImportPath(rel), err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s (package %s)\n\n", ImportPath(rel), d.Name)
	if s := strings.TrimSpace(d.Doc); s != "" {
		b.WriteString(s + "\n\n")
	}
	pr := &renderer{fset: fset, files: files, filter: filter, out: &b}
	for _, c := range d.Consts {
		pr.decl(c.Decl, c.Doc, c.Names...)
	}
	for _, v := range d.Vars {
		pr.decl(v.Decl, v.Doc, v.Names...)
	}
	for _, t := range d.Types {
		pr.typ(t)
	}
	for _, f := range d.Funcs {
		pr.fn(f)
	}
	return b.String(), nil
}

type renderer struct {
	fset   *token.FileSet
	files  map[string]*ast.File
	filter string
	out    *strings.Builder
}

func (r *renderer) matches(names ...string) bool {
	if r.filter == "" {
		return true
	}
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), r.filter) {
			return true
		}
	}
	return false
}

func (r *renderer) decl(d *ast.GenDecl, docText string, names ...string) {
	if !r.matches(names...) {
		return
	}
	r.code(r.print(d), docText)
}

func (r *renderer) typ(t *doc.Type) {
	names := []string{t.Name}
	for _, m := range t.Methods {
		names = append(names, m.Name)
	}
	for _, f := range t.Funcs {
		names = append(names, f.Name)
	}
	if !r.matches(names...) {
		return
	}
	if r.matches(t.Name) {
		r.code(r.print(t.Decl), t.Doc)
	}
	for _, f := range t.Funcs {
		r.fn(f)
	}
	for _, m := range t.Methods {
		r.fn(m)
	}
}

func (r *renderer) fn(f *doc.Func) {
	if !r.matches(f.Name) {
		return
	}
	decl := *f.Decl
	decl.Body = nil
	decl.Doc = nil
	r.code(r.print(&decl), f.Doc)
}

// print renders a declaration with its comments and without bodies.
func (r *renderer) print(n ast.Node) string {
	var buf bytes.Buffer
	file := r.files[r.fset.Position(n.Pos()).Filename]
	var node any = n
	if file != nil {
		node = &printer.CommentedNode{Node: n, Comments: file.Comments}
	}
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	if err := cfg.Fprint(&buf, r.fset, node); err != nil {
		return fmt.Sprintf("(%v)", err)
	}
	return buf.String()
}

func (r *renderer) code(code, docText string) {
	code = strings.TrimSpace(code)
	// A declaration's own doc comment is printed above it; drop it from
	// the code block so it is not shown twice.
	if docText != "" {
		if i := strings.Index(code, "\n"+firstCodeLine(code)); i > 0 && strings.HasPrefix(code, "//") {
			code = code[i+1:]
		}
	}
	fmt.Fprintf(r.out, "```go\n%s\n```\n", code)
	if s := strings.TrimSpace(docText); s != "" {
		fmt.Fprintln(r.out, s)
	}
	fmt.Fprintln(r.out)
}

func firstCodeLine(code string) string {
	for _, l := range strings.Split(code, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			return l
		}
	}
	return ""
}

// Exists reports whether importPath names a package of the framework at
// moduleDir.
func Exists(moduleDir, importPath string) bool {
	rel, ok := Rel(importPath)
	if !ok {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(moduleDir, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			return true
		}
	}
	return false
}

// Suggest returns the public package whose last element is closest to the
// one in importPath, or "".
func Suggest(moduleDir, importPath string) string {
	want := path.Base(importPath)
	best, bestScore := "", 0
	for _, rel := range AppPackages(moduleDir) {
		got := path.Base(ImportPath(rel))
		score := common(want, got)
		if score > bestScore && score >= 3 {
			best, bestScore = ImportPath(rel), score
		}
	}
	return best
}

func common(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
