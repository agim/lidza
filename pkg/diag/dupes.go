package diag

import (
	"go/ast"
	"go/scanner"
	"go/token"
	"hash/fnv"
	"sort"
	"strconv"
)

// A duplicate (L017) is a run of statements whose tokens, with the
// function's own names (parameters, variables, local types) and every
// literal replaced by a placeholder, match a run seen earlier in the app.
// Field, method, package and function names are kept, so two handlers of
// the same shape over different queries are not copies of each other.
const (
	dupMinTokens = 80
	dupMinStmts  = 8
)

// sourceFile is a parsed app file with its source, for L017.
type sourceFile struct {
	rel string
	src []byte
	f   *ast.File
}

// dupSite is where a run of statements was first seen.
type dupSite struct {
	file     string
	line     int
	from, to int // byte offsets of the run
}

// stmtList returns the statements a block, case or select clause holds.
func stmtList(n ast.Node) []ast.Stmt {
	switch b := n.(type) {
	case *ast.BlockStmt:
		return b.List
	case *ast.CaseClause:
		return b.Body
	case *ast.CommClause:
		return b.Body
	}
	return nil
}

// duplicates flags each run of statements that repeats an earlier one,
// once per statement list and original file. Every run is hashed once
// per window start, so the cost grows with the size of the code, not
// with the number of pairs.
func duplicates(fset *token.FileSet, files []sourceFile) []Diagnostic {
	first := map[uint64]dupSite{}
	var out []Diagnostic
	for _, sf := range files {
		tf := fset.File(sf.f.Pos())
		if tf == nil {
			continue
		}
		toks := scanFile(sf.src)
		ignored := ignoreComments(fset, sf.f)
		for _, decl := range sf.f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			locals := localNames(fd)
			// An ignore above the function covers all of it.
			fnLine := fset.Position(fd.Pos()).Line
			fnIgnored := ignored[fnLine]["L017"] || ignored[fnLine-1]["L017"]
			// The body's normalised tokens and their offsets.
			lo, hi := tf.Offset(fd.Body.Lbrace), tf.Offset(fd.Body.Rbrace)
			start := sort.Search(len(toks), func(i int) bool { return toks[i].off > lo })
			var norm []string
			var offs []int
			for i := start; i < len(toks) && toks[i].off < hi; i++ {
				norm = append(norm, normalise(toks, i, locals))
				offs = append(offs, toks[i].off)
			}
			index := func(off int) int { return sort.SearchInts(offs, off) }
			var reported [][2]int
			inReported := func(off int) bool {
				for _, r := range reported {
					if off >= r[0] && off < r[1] {
						return true
					}
				}
				return false
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				list := stmtList(n)
				if len(list) == 0 {
					return true
				}
				if inReported(tf.Offset(n.Pos())) {
					return false
				}
				from := make([]int, len(list))
				to := make([]int, len(list))
				stmts := make([]int, len(list))
				for k, s := range list {
					from[k], to[k] = index(tf.Offset(s.Pos())), index(tf.Offset(s.End()))
					stmts[k] = countStmts(s)
				}
				// A copy is reported once per list and original file, at
				// its first statement, with the matching windows that
				// follow it counted in.
				done := map[string]bool{}
				var run *Diagnostic
				runFile, runEnd, runStmts := "", -1, 0
				flush := func() {
					if run != nil {
						run.Message = "these " + strconv.Itoa(runStmts) + " statements repeat " + run.Message
						out = append(out, *run)
					}
					run = nil
				}
				for i := 0; i < len(list); {
					j, ntok, nst := i, 0, 0
					for j < len(list) && (ntok < dupMinTokens || nst < dupMinStmts) {
						ntok += to[j] - from[j]
						nst += stmts[j]
						j++
					}
					if ntok < dupMinTokens || nst < dupMinStmts {
						break
					}
					h := hashTokens(norm[from[i]:to[j-1]])
					here := dupSite{sf.rel, fset.Position(list[i].Pos()).Line, tf.Offset(list[i].Pos()), tf.Offset(list[j-1].End())}
					orig, seen := first[h]
					if !seen {
						first[h] = here
						i++
						continue
					}
					// A run overlapping the one it matches is a table of
					// like entries (cases, fields), not a copy.
					if orig.file == here.file && here.from < orig.to {
						i++
						continue
					}
					reported = append(reported, [2]int{here.from, here.to})
					switch {
					case run != nil && i == runEnd && orig.file == runFile:
						runStmts += nst
					case !done[orig.file] && !fnIgnored && !ignored[here.line]["L017"] && !ignored[here.line-1]["L017"]:
						flush()
						done[orig.file] = true
						run = &Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "warning", Code: "L017", File: sf.rel, Line: here.line, Column: fset.Position(list[i].Pos()).Column,
							Message: orig.file + ":" + strconv.Itoa(orig.line) + " (names and literals aside): reuse that code or extract a function both call"}
						runFile, runStmts = orig.file, nst
					}
					runEnd = j
					i = j
				}
				flush()
				return true
			})
		}
	}
	return out
}

type scanned struct {
	off int
	tok token.Token
	lit string
}

// scanFile returns a file's tokens without comments.
func scanFile(src []byte) []scanned {
	fset := token.NewFileSet()
	tf := fset.AddFile("", -1, len(src))
	var s scanner.Scanner
	s.Init(tf, src, nil, 0)
	var out []scanned
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		out = append(out, scanned{tf.Offset(pos), tok, lit})
	}
}

// normalise gives token i's form for comparison: a placeholder for a
// literal or a local name, the token itself otherwise.
func normalise(toks []scanned, i int, locals map[string]bool) string {
	t := toks[i]
	switch t.tok {
	case token.INT, token.FLOAT, token.IMAG, token.CHAR, token.STRING:
		return "#"
	case token.IDENT:
		if locals[t.lit] && (i == 0 || toks[i-1].tok != token.PERIOD) {
			return "$"
		}
		return t.lit
	case token.SEMICOLON:
		return ";"
	}
	return t.tok.String()
}

// localNames lists the names a function declares: receiver, parameters,
// results, variables and types, its function literals' included.
func localNames(fd *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	fields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	fields(fd.Recv)
	ast.Inspect(fd, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncType:
			fields(x.TypeParams)
			fields(x.Params)
			fields(x.Results)
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, e := range x.Lhs {
					if id, ok := e.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.RangeStmt:
			if x.Tok == token.DEFINE {
				for _, e := range []ast.Expr{x.Key, x.Value} {
					if id, ok := e.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range x.Names {
				out[id.Name] = true
			}
		case *ast.TypeSpec:
			out[x.Name.Name] = true
		}
		return true
	})
	delete(out, "_")
	return out
}

// countStmts counts the statements in s, s included; blocks and empty
// statements do not count.
func countStmts(s ast.Stmt) int {
	n := 0
	ast.Inspect(s, func(x ast.Node) bool {
		switch x.(type) {
		case *ast.BlockStmt, *ast.EmptyStmt:
		case ast.Stmt:
			n++
		}
		return true
	})
	return n
}

func hashTokens(toks []string) uint64 {
	h := fnv.New64a()
	for _, t := range toks {
		h.Write([]byte(t))
		h.Write([]byte{0})
	}
	return h.Sum64()
}
