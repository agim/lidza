// Package inspect reads a Līdza project's source and produces the context an
// agent needs: the API routes with their handler signatures, the Rust
// exports, and the configuration. `lidza context` writes it to
// .lidza/context.json; `lidza mcp` and the /llms.txt endpoints serve it.
package inspect

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/schema"
	"github.com/agim/lidza/pkg/version"
)

// FileName is the context dump, relative to the project root.
const FileName = ".lidza/context.json"

// Context is the machine-readable description of a project.
type Context struct {
	Generated time.Time `json:"generated"`
	// Lidza is the framework version that produced the dump.
	Lidza string `json:"lidza"`
	App   App    `json:"app"`
	// Routes lists every registration found in the source, typed or not.
	Routes []Route `json:"routes"`
	// Operations are the typed routes with their schemas; Schemas holds the
	// JSON Schema components they reference.
	Operations []Operation    `json:"operations"`
	Schemas    map[string]any `json:"schemas"`
	Rust       *Rust          `json:"rust,omitempty"`
	// Warnings are type-check errors: the operations may be incomplete
	// until they are fixed.
	Warnings []string `json:"warnings,omitempty"`
}

// App identifies the project.
type App struct {
	Name     string `json:"name"`
	Module   string `json:"module"`
	Template string `json:"template"`
	// Dir is the absolute project root.
	Dir string `json:"dir"`
}

// Route is one registered API route.
type Route struct {
	// Method is empty when the pattern matches every method.
	Method string `json:"method,omitempty"`
	Path   string `json:"path"`
	// Pattern is the net/http ServeMux pattern as written.
	Pattern string  `json:"pattern"`
	Handler Handler `json:"handler"`
	// Builtin marks routes the framework registers in every app.
	Builtin bool `json:"builtin,omitempty"`
	// Typed marks routes registered with router.Route; their schemas are
	// in Context.Operations.
	Typed bool `json:"typed,omitempty"`
	// Pack names the framework pack whose Mount registered the route.
	Pack string `json:"pack,omitempty"`
}

// Handler describes the function behind a route.
type Handler struct {
	// Name is the function name, "pkg.Func" for one from another package,
	// or "func literal" for an inline handler.
	Name string `json:"name"`
	// File and Line point at the registration for literals and at the
	// declaration for named functions. File is relative to the project root.
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// Rust describes the project's Rust crate.
type Rust struct {
	Crate string `json:"crate"`
	// Dir is the crate directory relative to the project root.
	Dir     string   `json:"dir"`
	Exports []Export `json:"exports"`
}

// Export is a `#[no_mangle] pub extern "C" fn`: what the Go side can call.
type Export struct {
	Name      string `json:"name"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Signature string `json:"signature"`
}

// Project reads everything in dir. cfg may be nil for a plain Go module.
func Project(dir string, cfg *config.Config) (*Context, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	c := &Context{Generated: time.Now().UTC(), Lidza: version.String(), App: App{Dir: abs}}
	if cfg != nil {
		c.App.Name = cfg.Name
		c.App.Template = cfg.Frontend.Template
	}
	c.App.Module = modulePath(abs)
	if c.App.Name == "" {
		c.App.Name = filepath.Base(c.App.Module)
	}

	for _, p := range router.Builtins() {
		method, path := splitPattern(p)
		c.Routes = append(c.Routes, Route{Method: method, Path: path, Pattern: p, Builtin: true, Typed: true,
			Handler: Handler{Name: "lidza/pkg/router", Signature: "func(w http.ResponseWriter, r *http.Request)"}})
	}
	routes, err := goRoutes(abs)
	if err != nil {
		return nil, err
	}
	c.Routes = append(c.Routes, routes...)

	lidzaSchema, err := schema.Load(abs)
	if err != nil {
		return nil, err
	}
	if c.App.Module != "" {
		ops, packRaw, defs, warnings, err := typedRoutes(abs, lidzaSchema, c.App.Module)
		if err != nil {
			return nil, err
		}
		c.Operations, c.Schemas, c.Warnings = ops, defs, warnings
		// A mounted pack's routes join the listing: the typed ones from
		// the operations, the raw ones as scanned.
		for _, op := range ops {
			if op.Pack != "" {
				c.Routes = append(c.Routes, Route{Method: op.Method, Path: op.Path, Pattern: op.Method + " " + op.Path, Handler: op.Handler, Typed: true, Pack: op.Pack})
			}
		}
		c.Routes = append(c.Routes, packRaw...)
	}
	if c.Operations == nil {
		c.Operations = []Operation{}
	}
	if c.Schemas == nil {
		c.Schemas = map[string]any{}
	}

	for _, d := range []string{"core", "."} {
		if _, err := os.Stat(filepath.Join(abs, d, "Cargo.toml")); err == nil {
			r, err := rustCrate(abs, d)
			if err != nil {
				return nil, err
			}
			c.Rust = r
			break
		}
	}
	return c, nil
}

// Write dumps the context to dir/.lidza/context.json and returns it.
func Write(dir string, cfg *config.Config) (*Context, error) {
	c, err := Project(dir, cfg)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(c.App.Dir, FileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return c, os.WriteFile(path, append(data, '\n'), 0o644)
}

var skipDirs = map[string]bool{
	"node_modules": true, "dist": true, "bin": true, "target": true,
	"vendor": true, "testdata": true,
}

// goRoutes finds every r.HandleFunc("PATTERN", h) and r.Handle("PATTERN", h)
// call with a literal pattern in the project's Go files.
func goRoutes(root string) ([]Route, error) {
	fset := token.NewFileSet()
	// Files grouped by directory and package clause; test files excluded.
	type pkg struct {
		name  string
		files []*ast.File
	}
	byKey := map[string]*pkg{}
	var pkgs []*pkg
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("%s: %w", relPath(root, p), err)
		}
		key := filepath.Dir(p) + ":" + f.Name.Name
		g, ok := byKey[key]
		if !ok {
			g = &pkg{name: f.Name.Name}
			byKey[key] = g
			pkgs = append(pkgs, g)
		}
		g.files = append(g.files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Named handlers are resolved by "pkg.Func" and, within a package, "Func".
	decls := map[string]*ast.FuncDecl{}
	for _, pkg := range pkgs {
		for _, f := range pkg.files {
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
					decls[pkg.name+"."+fd.Name.Name] = fd
				}
			}
		}
	}

	var routes []Route
	for _, pkg := range pkgs {
		for _, f := range pkg.files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				// r.HandleFunc(pattern, h), r.Handle(pattern, h) and
				// router.Route(r, pattern, h).
				patternArg, handlerArg := 0, 1
				switch sel.Sel.Name {
				case "HandleFunc", "Handle":
				case "Route":
					if len(call.Args) < 3 {
						return true
					}
					patternArg, handlerArg = 1, 2
				default:
					return true
				}
				lit, ok := call.Args[patternArg].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				pattern, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				method, path := splitPattern(pattern)
				h := describeHandler(fset, root, pkg.name, call.Args[handlerArg], decls)
				routes = append(routes, Route{Method: method, Path: path, Pattern: pattern, Handler: h, Typed: sel.Sel.Name == "Route"})
				return true
			})
		}
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes, nil
}

func describeHandler(fset *token.FileSet, root, pkgName string, arg ast.Expr, decls map[string]*ast.FuncDecl) Handler {
	pos := fset.Position(arg.Pos())
	h := Handler{File: relPath(root, pos.Filename), Line: pos.Line}
	switch a := arg.(type) {
	case *ast.FuncLit:
		h.Name = "func literal"
		h.Signature = exprString(fset, a.Type)
	case *ast.Ident:
		h.Name = a.Name
		if fd, ok := decls[pkgName+"."+a.Name]; ok {
			fill(&h, fset, root, fd)
		}
	case *ast.SelectorExpr:
		if x, ok := a.X.(*ast.Ident); ok {
			h.Name = x.Name + "." + a.Sel.Name
			if fd, ok := decls[x.Name+"."+a.Sel.Name]; ok {
				fill(&h, fset, root, fd)
			}
		} else {
			h.Name = exprString(fset, a)
		}
	case *ast.CallExpr:
		// A handler produced by a call, e.g. http.HandlerFunc(f) or
		// newHandler(db). Report the expression as written.
		h.Name = exprString(fset, a)
	default:
		h.Name = exprString(fset, a)
	}
	return h
}

func fill(h *Handler, fset *token.FileSet, root string, fd *ast.FuncDecl) {
	pos := fset.Position(fd.Pos())
	h.File, h.Line = relPath(root, pos.Filename), pos.Line
	h.Signature = "func " + fd.Name.Name + strings.TrimPrefix(exprString(fset, fd.Type), "func")
}

func exprString(fset *token.FileSet, e ast.Node) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		return ""
	}
	return b.String()
}

// splitPattern separates the optional method from a ServeMux pattern:
// "GET /api/x" -> ("GET", "/api/x"); "/api/x" -> ("", "/api/x").
func splitPattern(p string) (method, path string) {
	if i := strings.IndexByte(p, ' '); i > 0 && strings.ToUpper(p[:i]) == p[:i] {
		return p[:i], strings.TrimSpace(p[i+1:])
	}
	return "", p
}

var (
	crateNameRe = regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)
	noMangleRe  = regexp.MustCompile(`#\[(unsafe\()?no_mangle\)?\]`)
	externFnRe  = regexp.MustCompile(`^\s*pub\s+(?:unsafe\s+)?extern\s+"C"\s+fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

// rustCrate lists the crate's C-ABI exports by scanning its source: a
// `pub extern "C" fn` preceded by `#[no_mangle]` within the previous lines.
func rustCrate(root, dir string) (*Rust, error) {
	crateDir := filepath.Join(root, dir)
	manifest, err := os.ReadFile(filepath.Join(crateDir, "Cargo.toml"))
	if err != nil {
		return nil, err
	}
	r := &Rust{Dir: filepath.ToSlash(dir), Exports: []Export{}}
	if m := crateNameRe.FindSubmatch(manifest); m != nil {
		r.Crate = string(m[1])
	}
	err = filepath.WalkDir(filepath.Join(crateDir, "src"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".rs") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			m := externFnRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if !precededByNoMangle(lines, i) {
				continue
			}
			r.Exports = append(r.Exports, Export{
				Name:      m[1],
				File:      relPath(root, p),
				Line:      i + 1,
				Signature: signature(lines, i),
			})
		}
		return nil
	})
	return r, err
}

func precededByNoMangle(lines []string, i int) bool {
	for j := i - 1; j >= 0 && j >= i-4; j-- {
		if noMangleRe.MatchString(lines[j]) {
			return true
		}
		if t := strings.TrimSpace(lines[j]); t != "" && !strings.HasPrefix(t, "#[") && !strings.HasPrefix(t, "//") {
			return false
		}
	}
	return false
}

// signature joins the fn header from its line up to the body brace.
func signature(lines []string, i int) string {
	var parts []string
	for j := i; j < len(lines) && j < i+10; j++ {
		l := strings.TrimSpace(lines[j])
		if k := strings.Index(l, "{"); k >= 0 {
			parts = append(parts, strings.TrimSpace(l[:k]))
			break
		}
		parts = append(parts, l)
	}
	return strings.Join(parts, " ")
}

// ModulePath reads the module path from root/go.mod, or "".
func ModulePath(root string) string { return modulePath(root) }

func modulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func relPath(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(p)
}
