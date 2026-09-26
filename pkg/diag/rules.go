package diag

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/agim/lidza/pkg/apidoc"
	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/decisions"
	"github.com/agim/lidza/pkg/pack"
	"golang.org/x/mod/modfile"
)

// Rules are the checks `lidza check` applies to the app's own Go code
// (generated files, vendor and tests excluded):
//
//   - L001: a package-level variable of map or slice type is state that
//     grows with traffic and lives on one node; keep it in Postgres or
//     Valkey, or make it a bounded, guarded structure;
//   - L002: a goroutine started inside a request handler is unbounded
//     work; use the jobs pack or a worker pool with a size;
//   - L004: an import under the framework's module path that names no
//     package of the framework version the app uses (an error: it will
//     not compile, and `go get` cannot help);
//   - L005: a typed handler whose input or output type is not declared in
//     schema.lidza, so it is neither validated nor known to the client;
//   - L006: an import of an email vendor SDK; the mail pack speaks those
//     APIs already, with the outbox, templates and retries;
//   - L007: an import of a language-model vendor SDK or client library;
//     the llm pack speaks those APIs already, with structured output,
//     tools, retries and a fake for tests;
//   - L008: an import of an object-storage SDK; the storage pack speaks
//     the S3 API already, with a local provider for tests;
//   - L009: a file written to the local disk (os.WriteFile, os.Create,
//     os.OpenFile, os.MkdirAll); a node's disk is neither shared nor
//     kept, so uploads and generated files go through the storage pack;
//   - L010: a string literal shaped like an API key or token (AWS,
//     OpenAI, Anthropic, Google, SendGrid, Mailgun, Resend, Slack, a
//     private key); secrets go in the credentials, never in source;
//   - L011: a pack enabled in lidza.json that no app code imports; use
//     it or remove it, so the next reader is not misled;
//   - L012: a pack or a direct dependency (one the framework does not
//     bring itself) with no entry in docs/decisions.md naming it; the
//     why of every such choice is recorded there.
//
// Except for L004 the findings are warnings: they point at the pattern,
// the author decides. A comment "lidza:ignore L001" on the line, or the
// line before, exempts that line from the rule it names.
func Rules(ctx context.Context, root string) []Diagnostic {
	var out []Diagnostic
	fset := token.NewFileSet()
	imported := map[string]bool{}
	moduleDir, err := apidoc.ModuleDir(ctx, root)
	if err != nil {
		moduleDir = ""
	}
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
		for _, imp := range f.Imports {
			if path, err := strconv.Unquote(imp.Path.Value); err == nil {
				imported[path] = true
			}
		}
		out = append(out, checkFile(fset, f, rel, moduleDir)...)
		return nil
	})
	out = append(out, unusedPacks(root, imported)...)
	out = append(out, missingDecisions(root, moduleDir)...)
	return out
}

// missingDecisions flags packs and direct dependencies no decision names.
func missingDecisions(root, moduleDir string) []Diagnostic {
	cfg, err := config.Load(root)
	if err != nil {
		return nil
	}
	entries, err := decisions.Load(root)
	if err != nil {
		return nil
	}
	var text strings.Builder
	for _, e := range entries {
		text.WriteString(strings.ToLower(e.Title + " " + e.Why + " " + e.Touches + "\n"))
	}
	recorded := text.String()
	mentions := func(name string) bool {
		re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(strings.ToLower(name)) + `([^a-z0-9]|$)`)
		return re.MatchString(recorded)
	}
	var out []Diagnostic
	flag := func(what, name, hint string) {
		out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "warning", Code: "L012", File: decisions.File, Line: 1, Column: 1,
			Message: what + " " + name + " has no decision in " + decisions.File + ": " + hint})
	}
	for _, entry := range cfg.Packs {
		name := strings.TrimPrefix(entry, pack.OfficialPrefix)
		if !mentions(name) {
			flag("pack", name, "say why it is here (lidza decision add \"Pack "+name+" added\" --why \"...\", or --why on lidza pack add)")
		}
	}
	for _, dep := range directDependencies(root, moduleDir) {
		if !mentions(dep) && !mentions(path.Base(dep)) {
			flag("dependency", dep, "say why the app takes it and what it replaces (lidza decision add \"Dependency "+path.Base(dep)+"\" --why \"...\")")
		}
	}
	return out
}

// directDependencies lists the app's direct requirements that the
// framework does not require itself: the app's own choices.
func directDependencies(root, moduleDir string) []string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil
	}
	framework := map[string]bool{apidoc.Module: true}
	if moduleDir != "" {
		if fd, err := os.ReadFile(filepath.Join(moduleDir, "go.mod")); err == nil {
			if ff, err := modfile.Parse("go.mod", fd, nil); err == nil {
				for _, r := range ff.Require {
					framework[r.Mod.Path] = true
				}
			}
		}
	}
	var out []string
	for _, r := range f.Require {
		if r.Indirect || framework[r.Mod.Path] {
			continue
		}
		out = append(out, r.Mod.Path)
	}
	return out
}

// packsUsedByOthers are enabled for what other packs need, not for a
// handler to call: no L011 for them.
var packsUsedByOthers = map[string]bool{"db": true, "jobs": true, "analytics": true}

// unusedPacks flags official Go packs in lidza.json that no app file
// imports.
func unusedPacks(root string, imported map[string]bool) []Diagnostic {
	cfg, err := config.Load(root)
	if err != nil {
		return nil
	}
	var out []Diagnostic
	for _, entry := range cfg.Packs {
		name, ok := strings.CutPrefix(entry, pack.OfficialPrefix)
		if !ok || packsUsedByOthers[name] || !pack.IsOfficialGo(entry) {
			continue
		}
		if imported[apidoc.Module+"/packs/"+name] {
			continue
		}
		out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "warning", Code: "L011", File: config.FileName, Line: 1, Column: 1,
			Message: "pack " + name + " is enabled but no code imports " + apidoc.Module + "/packs/" + name + ": build the feature that needs it (the guide's recipes) or remove it from lidza.json and run lidza gen"})
	}
	return out
}

var skipRuleDirs = map[string]bool{"node_modules": true, "dist": true, "bin": true, "target": true, "vendor": true, "testdata": true}

func checkFile(fset *token.FileSet, f *ast.File, rel, moduleDir string) []Diagnostic {
	var out []Diagnostic
	ignored := ignoreComments(fset, f)
	report := func(pos token.Pos, severity, code, msg string) {
		p := fset.Position(pos)
		if ignored[p.Line][code] || ignored[p.Line-1][code] {
			return
		}
		out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: severity, Code: code, File: rel, Line: p.Line, Column: p.Column, Message: msg})
	}
	warn := func(pos token.Pos, code, msg string) { report(pos, "warning", code, msg) }
	schemaPkg, routerPkg := "", "router"
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch {
		case path == apidoc.Module+"/pkg/router":
			if name != "" {
				routerPkg = name
			}
		case strings.HasSuffix(path, "/schema") && !strings.HasPrefix(path, apidoc.Module+"/"):
			schemaPkg = "schema"
			if name != "" {
				schemaPkg = name
			}
		}
		if vendor := mailVendor(path); vendor != "" {
			warn(imp.Pos(), "L006", "import of the "+vendor+" SDK: use the mail pack instead (`lidza pack add mail`, MAIL_PROVIDER="+vendor+"), which speaks the API directly and adds the outbox, templates and retries")
		}
		if storageVendor(path) {
			warn(imp.Pos(), "L008", "import of "+path+": use the storage pack instead (`lidza pack add storage`, STORAGE_PROVIDER=s3 with any S3-compatible service), which speaks the API directly and adds a local provider for tests")
		}
		if vendor := llmVendor(path); vendor != "" {
			warn(imp.Pos(), "L007", "import of "+path+": use the llm pack instead (`lidza pack add llm`, LLM_PROVIDER="+vendor+"), which speaks the API directly and adds structured output, tools, retries and a fake for tests")
		}
		if moduleDir != "" {
			if _, ok := apidoc.Rel(path); ok && !apidoc.Exists(moduleDir, path) {
				msg := "package " + path + " does not exist in this version of Līdza"
				if s := apidoc.Suggest(moduleDir, path); s != "" {
					msg += "; did you mean " + s + "?"
				}
				report(imp.Pos(), "error", "L004", msg+" (`lidza api` lists the packages and their API)")
			}
		}
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
			for _, bad := range foreignTypes(d.Type, routerPkg, schemaPkg) {
				warn(bad.Pos(), "L005", "handler "+d.Name.Name+": "+exprText(bad)+" is not a type from schema.lidza; declare it there so it is validated and reaches the generated client")
			}
			ast.Inspect(d.Body, func(n ast.Node) bool {
				if g, ok := n.(*ast.GoStmt); ok {
					warn(g.Pos(), "L002", "goroutine started in handler "+d.Name.Name+": unbounded work per request; use the jobs pack or a worker pool with a size")
				}
				return true
			})
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if kind := secretShape(lit.Value); kind != "" {
				warn(lit.Pos(), "L010", "a string that looks like "+kind+": secrets never go in source; seal it with `lidza credentials set NAME=...` (MCP lidza_credentials_set) and read it by name from the pack's configuration")
			}
			return true
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn := diskWrite(call); fn != "" {
			warn(call.Pos(), "L009", "os."+fn+" writes to this node's disk, which is neither shared with other nodes nor kept across deploys: keep uploads and generated files in the storage pack (`lidza pack add storage`; storage.From(ctx).Put), a directory in development and S3-compatible storage in production")
		}
		return true
	})
	return out
}

// secretShapes are the token formats providers issue.
var secretShapes = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"an AWS access key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"an Anthropic API key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
	{"an OpenAI API key", regexp.MustCompile(`sk-(proj-)?[A-Za-z0-9_-]{20,}`)},
	{"a Google API key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
	{"a SendGrid API key", regexp.MustCompile(`SG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}`)},
	{"a Mailgun API key", regexp.MustCompile(`key-[0-9a-f]{32}`)},
	{"a Resend API key", regexp.MustCompile(`re_[A-Za-z0-9]{20,}`)},
	{"a Slack token", regexp.MustCompile(`xox[abpr]-[A-Za-z0-9-]{10,}`)},
	{"a GitHub token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`)},
	{"a private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
}

// secretShape names the kind of secret a string literal looks like, "".
func secretShape(lit string) string {
	for _, s := range secretShapes {
		if s.re.MatchString(lit) {
			return s.kind
		}
	}
	return ""
}

// diskWrite names the os function a call writes files with, or "".
func diskWrite(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "os" {
		return ""
	}
	switch sel.Sel.Name {
	case "WriteFile", "Create", "OpenFile", "MkdirAll", "Mkdir", "CreateTemp", "MkdirTemp":
		return sel.Sel.Name
	}
	return ""
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

// mailVendor names the mail provider whose SDK an import path belongs to,
// or "".
func mailVendor(path string) string {
	for prefix, vendor := range map[string]string{
		"github.com/mailgun/mailgun-go":            "mailgun",
		"github.com/sendgrid/sendgrid-go":          "sendgrid",
		"github.com/mattevans/postmark-go":         "postmark",
		"github.com/keighl/postmark":               "postmark",
		"github.com/resend/resend-go":              "resend",
		"github.com/aws/aws-sdk-go-v2/service/ses": "ses",
		"github.com/aws/aws-sdk-go/service/ses":    "ses",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return vendor
		}
	}
	return ""
}

// llmVendor names the provider a language-model SDK or client library
// import belongs to, or "" for any other import.
func llmVendor(path string) string {
	for prefix, vendor := range map[string]string{
		"github.com/anthropics/anthropic-sdk-go": "anthropic",
		"github.com/openai/openai-go":            "openai",
		"github.com/sashabaranov/go-openai":      "openai",
		"google.golang.org/genai":                "google",
		"github.com/google/generative-ai-go":     "google",
		"github.com/ollama/ollama":               "ollama",
		"github.com/tmc/langchaingo":             "anthropic|openai|google|ollama",
		"github.com/cloudwego/eino":              "anthropic|openai|google|ollama",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return vendor
		}
	}
	return ""
}

// storageVendor reports an object-storage SDK import.
func storageVendor(path string) bool {
	for _, prefix := range []string{
		"github.com/aws/aws-sdk-go-v2/service/s3", "github.com/aws/aws-sdk-go/service/s3", "github.com/aws/aws-sdk-go-v2/feature/s3",
		"github.com/minio/minio-go", "gocloud.dev/blob", "cloud.google.com/go/storage", "github.com/Azure/azure-sdk-for-go/sdk/storage",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// ignoreComments maps line numbers to the rule codes a "lidza:ignore"
// comment on that line names.
func ignoreComments(fset *token.FileSet, f *ast.File) map[int]map[string]bool {
	out := map[int]map[string]bool{}
	for _, g := range f.Comments {
		for _, c := range g.List {
			text := c.Text
			for {
				i := strings.Index(text, "lidza:ignore ")
				if i < 0 {
					break
				}
				text = text[i+len("lidza:ignore "):]
				code := strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == ')' || r == ',' || r == '\n' })
				if len(code) == 0 {
					break
				}
				line := fset.Position(c.Pos()).Line
				if out[line] == nil {
					out[line] = map[string]bool{}
				}
				out[line][code[0]] = true
			}
		}
	}
	return out
}

// foreignTypes returns the In and Out types of a typed handler signature
// that are not router.None, a schema.lidza type, or a basic type.
func foreignTypes(ft *ast.FuncType, routerPkg, schemaPkg string) []ast.Expr {
	var out []ast.Expr
	// A generic helper over *router.Request[In] is not a handler.
	typeParams := map[string]bool{}
	if ft.TypeParams != nil {
		for _, f := range ft.TypeParams.List {
			for _, n := range f.Names {
				typeParams[n.Name] = true
			}
		}
	}
	check := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok && typeParams[id.Name] {
			return
		}
		if e != nil && !contractType(e, routerPkg, schemaPkg) {
			out = append(out, e)
		}
	}
	for _, p := range ft.Params.List {
		star, ok := p.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		idx, ok := star.X.(*ast.IndexExpr)
		if !ok || exprText(idx.X) != routerPkg+".Request" {
			continue
		}
		check(idx.Index)
	}
	if ft.Results != nil && len(ft.Results.List) == 2 {
		check(ft.Results.List[0].Type)
	}
	return out
}

func contractType(e ast.Expr, routerPkg, schemaPkg string) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return basicTypes[x.Name]
	case *ast.SelectorExpr:
		pkg := exprText(x.X)
		return pkg == schemaPkg || (pkg == routerPkg && x.Sel.Name == "None")
	case *ast.StarExpr:
		return contractType(x.X, routerPkg, schemaPkg)
	case *ast.ArrayType:
		return contractType(x.Elt, routerPkg, schemaPkg)
	case *ast.MapType:
		return contractType(x.Value, routerPkg, schemaPkg)
	}
	return false
}

var basicTypes = map[string]bool{"string": true, "bool": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "float32": true, "float64": true, "byte": true, "rune": true}

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
	case *ast.ArrayType:
		return "[]" + exprText(x.Elt)
	case *ast.MapType:
		return "map[" + exprText(x.Key) + "]" + exprText(x.Value)
	case *ast.StructType:
		return "struct{...}"
	case *ast.InterfaceType:
		return "any"
	}
	return ""
}
