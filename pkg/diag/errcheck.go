package diag

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"
)

// discardedErrors type-checks the app's packages and flags each call whose
// error result is dropped (L016): a call as a statement, or its error
// assigned to _. Deferred and go calls, test files, generated files and
// the calls that cannot fail in practice are left alone.
func discardedErrors(ctx context.Context, root string) []Diagnostic {
	cfg := &packages.Config{
		Context: ctx,
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:     root,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil
	}
	var out []Diagnostic
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, f := range pkg.Syntax {
			name := pkg.Fset.Position(f.Pos()).Filename
			rel, err := filepath.Rel(root, name)
			if err != nil || seen[rel] || strings.HasSuffix(name, "_test.go") || ast.IsGenerated(f) || skippedPath(rel) {
				continue
			}
			seen[rel] = true
			out = append(out, discardedInFile(pkg.Fset, f, pkg.TypesInfo, filepath.ToSlash(rel))...)
		}
	}
	return out
}

// skippedPath reports a path under a directory the rules skip.
func skippedPath(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, dir := range parts[:len(parts)-1] {
		if skipRuleDirs[dir] || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "_") {
			return true
		}
	}
	return parts[0] == ".."
}

func discardedInFile(fset *token.FileSet, f *ast.File, info *types.Info, rel string) []Diagnostic {
	var out []Diagnostic
	ignored := ignoreComments(fset, f)
	flag := func(call *ast.CallExpr) {
		p := fset.Position(call.Pos())
		if ignored[p.Line]["L016"] || ignored[p.Line-1]["L016"] {
			return
		}
		out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "warning", Code: "L016", File: rel, Line: p.Line, Column: p.Column,
			Message: "the error from " + types.ExprString(call.Fun) + " is dropped: handle it or return it (a failure here goes unseen)"})
	}
	// A Close on the way out of a failed step (x.Close(); return err)
	// cleans up; the error being returned is the one that matters.
	cleanup := map[ast.Stmt]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		list := stmtList(n)
		if len(list) == 0 {
			return true
		}
		if _, ok := list[len(list)-1].(*ast.ReturnStmt); !ok {
			return true
		}
		for _, st := range list {
			if es, ok := st.(*ast.ExprStmt); ok {
				if call, ok := ast.Unparen(es.X).(*ast.CallExpr); ok && len(call.Args) == 0 {
					if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == "Close" {
						cleanup[st] = true
					}
				}
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.DeferStmt, *ast.GoStmt:
			// defer f.Close(), defer tx.Rollback(ctx): the idiom.
			return false
		case *ast.ExprStmt:
			if call, ok := ast.Unparen(s.X).(*ast.CallExpr); ok && !cleanup[s] && errorResult(info, call) != noError && !cannotFail(info, call) {
				flag(call)
			}
		case *ast.AssignStmt:
			if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
				// v, _ := f(): the last left-hand side takes the error.
				if call, ok := ast.Unparen(s.Rhs[0]).(*ast.CallExpr); ok && isBlank(s.Lhs[len(s.Lhs)-1]) && errorResult(info, call) == lastResult && !cannotFail(info, call) {
					flag(call)
				}
				return true
			}
			for i, rhs := range s.Rhs {
				if i >= len(s.Lhs) || !isBlank(s.Lhs[i]) {
					continue
				}
				if call, ok := ast.Unparen(rhs).(*ast.CallExpr); ok && errorResult(info, call) == onlyResult && !cannotFail(info, call) {
					flag(call)
				}
			}
		}
		return true
	})
	return out
}

const (
	noError = iota
	onlyResult
	lastResult
)

// errorResult says whether a call returns an error alone (onlyResult),
// last of several (lastResult), or not at all.
func errorResult(info *types.Info, call *ast.CallExpr) int {
	tv, ok := info.Types[call]
	if !ok || tv.IsType() || tv.IsBuiltin() || tv.Type == nil {
		return noError
	}
	errType := types.Universe.Lookup("error").Type()
	switch t := tv.Type.(type) {
	case *types.Tuple:
		if t.Len() > 0 && types.Identical(t.At(t.Len()-1).Type(), errType) {
			return lastResult
		}
	default:
		if types.Identical(t, errType) {
			return onlyResult
		}
	}
	return noError
}

func isBlank(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "_"
}

// cannotFail recognises the calls whose error is nil in practice: printing
// to the terminal, and writes to an in-memory buffer or a hash.
func cannotFail(info *types.Info, call *ast.CallExpr) bool {
	fn, ok := typeutil.Callee(info, call).(*types.Func)
	if !ok {
		return false
	}
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil {
		return false
	}
	if recv := sig.Recv(); recv != nil {
		t := recv.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil {
			switch named.Obj().Pkg().Path() + "." + named.Obj().Name() {
			case "bytes.Buffer", "strings.Builder":
				return true
			}
		}
		// A hash's Write never returns an error (hash.Hash documents it).
		if fn.Name() == "Write" {
			if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
				return isHash(info.TypeOf(sel.X))
			}
		}
		return false
	}
	if fn.Pkg() == nil || fn.Pkg().Path() != "fmt" {
		return false
	}
	switch fn.Name() {
	case "Print", "Printf", "Println":
		return true
	case "Fprint", "Fprintf", "Fprintln":
		if len(call.Args) == 0 {
			return false
		}
		w := ast.Unparen(call.Args[0])
		if sel, ok := w.(*ast.SelectorExpr); ok {
			if obj, ok := info.Uses[sel.Sel].(*types.Var); ok && obj.Pkg() != nil && obj.Pkg().Path() == "os" && (obj.Name() == "Stdout" || obj.Name() == "Stderr") {
				return true
			}
		}
		t := info.TypeOf(w)
		if t == nil {
			return false
		}
		if p, ok := t.(*types.Pointer); ok {
			if named, ok := p.Elem().(*types.Named); ok && named.Obj().Pkg() != nil {
				switch named.Obj().Pkg().Path() + "." + named.Obj().Name() {
				case "bytes.Buffer", "strings.Builder":
					return true
				}
			}
		}
		return isHash(t)
	}
	return false
}

// isHash reports a type with hash.Hash's method set (Write, Sum, Reset,
// Size, BlockSize).
func isHash(t types.Type) bool {
	if t == nil {
		return false
	}
	ms := types.NewMethodSet(t)
	if _, isPtr := t.(*types.Pointer); !isPtr && !types.IsInterface(t) {
		ms = types.NewMethodSet(types.NewPointer(t))
	}
	for _, m := range []string{"Write", "Sum", "Reset", "Size", "BlockSize"} {
		if ms.Lookup(nil, m) == nil {
			return false
		}
	}
	return true
}
