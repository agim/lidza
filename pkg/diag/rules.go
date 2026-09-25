package diag

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Rules are the scalability checks `lidza check` applies to the app's own
// Go code (generated files, vendor and tests excluded):
//
//   - a package-level variable of map or slice type is state that grows
//     with traffic and lives on one node; keep it in Postgres or Valkey,
//     or make it a bounded, guarded structure;
//   - a goroutine started inside a request handler is unbounded work;
//     use the jobs pack or a worker pool with a size.
//
// Findings are warnings: they point at the pattern, the author decides.
func Rules(root string) []Diagnostic {
	var out []Diagnostic
	fset := token.NewFileSet()
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (skipRuleDirs[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil || ast.IsGenerated(f) {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, root), string(filepath.Separator)))
		out = append(out, checkFile(fset, f, rel)...)
		return nil
	})
	return out
}

var skipRuleDirs = map[string]bool{"node_modules": true, "dist": true, "bin": true, "target": true, "vendor": true, "testdata": true}

func checkFile(fset *token.FileSet, f *ast.File, rel string) []Diagnostic {
	var out []Diagnostic
	warn := func(pos token.Pos, code, msg string) {
		p := fset.Position(pos)
		out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "warning", Code: code, File: rel, Line: p.Line, Column: p.Column, Message: msg})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue
			}
			for _, spec := range d.Specs {
				vs := spec.(*ast.ValueSpec)
				if !mutableCollection(vs) {
					continue
				}
				for _, name := range vs.Names {
					warn(name.Pos(), "L001", "package-level "+name.Name+" is a map or slice: state on one node that grows with traffic; keep it in Postgres or Valkey, or bound and guard it")
				}
			}
		case *ast.FuncDecl:
			if d.Body == nil || !isHandler(d.Type) {
				continue
			}
			ast.Inspect(d.Body, func(n ast.Node) bool {
				if g, ok := n.(*ast.GoStmt); ok {
					warn(g.Pos(), "L002", "goroutine started in handler "+d.Name.Name+": unbounded work per request; use the jobs pack or a worker pool with a size")
				}
				return true
			})
		}
	}
	return out
}

// mutableCollection reports whether a var spec declares a map or slice,
// by explicit type or by its initializer.
func mutableCollection(vs *ast.ValueSpec) bool {
	if isCollectionType(vs.Type) {
		return true
	}
	for _, v := range vs.Values {
		switch x := v.(type) {
		case *ast.CompositeLit:
			if isCollectionType(x.Type) {
				return true
			}
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "make" && len(x.Args) > 0 && isCollectionType(x.Args[0]) {
				return true
			}
		}
	}
	return false
}

func isCollectionType(e ast.Expr) bool {
	switch e.(type) {
	case *ast.MapType, *ast.ArrayType:
		return true
	}
	return false
}

// isHandler recognises net/http handlers and typed router handlers by
// their parameters.
func isHandler(ft *ast.FuncType) bool {
	for _, p := range ft.Params.List {
		s := exprText(p.Type)
		if strings.Contains(s, "http.Request") || strings.Contains(s, "router.Request") {
			return true
		}
	}
	return false
}

func exprText(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return "*" + exprText(x.X)
	case *ast.SelectorExpr:
		return exprText(x.X) + "." + x.Sel.Name
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return exprText(x.X) + "[" + exprText(x.Index) + "]"
	case *ast.IndexListExpr:
		return exprText(x.X) + "[...]"
	}
	return ""
}
