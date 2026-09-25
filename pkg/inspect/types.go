package inspect

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/agim/lidza/pkg/schema"
)

// routerPath is the package whose Route function makes a route typed.
const routerPath = "github.com/agim/lidza/pkg/router"

// Operation is a typed route (router.Route[In, Out]) with the JSON Schemas
// of its input and output, the basis of the OpenAPI document and the
// generated client.
type Operation struct {
	// ID is the operationId and the client method name: the handler's name
	// in lowerCamel, or method plus path for a func literal.
	ID     string `json:"id"`
	Method string `json:"method"`
	Path   string `json:"path"`
	// Params are the path parameters, in order.
	Params []string `json:"params"`
	// Input and Output name the component schema (empty for router.None).
	Input   string  `json:"input,omitempty"`
	Output  string  `json:"output,omitempty"`
	Handler Handler `json:"handler"`
	// Builtin marks operations the framework registers in every app.
	Builtin bool `json:"builtin,omitempty"`
}

// typedRoutes type-checks the project and returns its operations and the
// component schemas they use. Type errors do not stop it: the routes the
// checker could resolve are returned with the errors as warnings.
func typedRoutes(root string, lidzaSchema *schema.Schema, module string) ([]Operation, map[string]any, []string, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir: root,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("type-check: %w", err)
	}
	b := newSchemaBuilder(lidzaSchema, module)
	var warnings []string
	var ops []Operation
	var routerPkg *types.Package

	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			warnings = append(warnings, e.Error())
		}
		if pkg.TypesInfo == nil {
			continue
		}
		if imp, ok := pkg.Imports[routerPath]; ok && imp.Types != nil {
			routerPkg = imp.Types
		}
		for _, f := range pkg.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 3 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				fn, ok := pkg.TypesInfo.Uses[sel.Sel].(*types.Func)
				if !ok || fn.Pkg() == nil || fn.Pkg().Path() != routerPath || fn.Name() != "Route" {
					return true
				}
				pattern, ok := stringLit(call.Args[1])
				if !ok {
					warnings = append(warnings, fmt.Sprintf("%s: router.Route with a non-literal pattern is not documented", pkg.Fset.Position(call.Pos())))
					return true
				}
				inst, ok := pkg.TypesInfo.Instances[sel.Sel]
				if !ok || inst.TypeArgs.Len() != 2 {
					return true
				}
				method, path := splitPattern(pattern)
				op := Operation{Method: method, Path: path, Params: pathParams(path)}
				op.Input = b.component(inst.TypeArgs.At(0))
				op.Output = b.component(inst.TypeArgs.At(1))
				op.Handler = typedHandler(pkg, root, call.Args[2])
				op.ID = operationID(op.Handler.Name, method, path)
				ops = append(ops, op)
				return true
			})
		}
	}

	// Built-in operations: their output types live in the router package.
	if routerPkg != nil {
		for _, bi := range builtinOps() {
			op := Operation{ID: bi.id, Method: bi.method, Path: bi.path, Params: pathParams(bi.path), Builtin: true,
				Handler: Handler{Name: "lidza/pkg/router", Signature: "func(w http.ResponseWriter, r *http.Request)"}}
			if obj := routerPkg.Scope().Lookup(bi.output); obj != nil {
				op.Output = b.component(obj.Type())
			}
			ops = append(ops, op)
		}
	}
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops, b.defs, warnings, nil
}

type builtinOp struct{ id, method, path, output string }

// builtinOps mirrors router.Builtins with the operation names and output
// types the client needs.
func builtinOps() []builtinOp {
	return []builtinOp{{"health", "GET", "/api/v1/health", "Health"}}
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconvUnquote(lit.Value)
	return s, err == nil
}

func typedHandler(pkg *packages.Package, root string, arg ast.Expr) Handler {
	pos := pkg.Fset.Position(arg.Pos())
	h := Handler{File: relPath(root, pos.Filename), Line: pos.Line}
	switch a := arg.(type) {
	case *ast.FuncLit:
		h.Name = "func literal"
		h.Signature = exprString(pkg.Fset, a.Type)
	case *ast.Ident:
		h.Name = a.Name
		fillFromObj(&h, pkg, root, pkg.TypesInfo.Uses[a])
	case *ast.SelectorExpr:
		h.Name = exprString(pkg.Fset, a)
		fillFromObj(&h, pkg, root, pkg.TypesInfo.Uses[a.Sel])
	default:
		h.Name = exprString(pkg.Fset, a)
	}
	return h
}

func fillFromObj(h *Handler, pkg *packages.Package, root string, obj types.Object) {
	if obj == nil {
		return
	}
	pos := pkg.Fset.Position(obj.Pos())
	if pos.IsValid() {
		h.File, h.Line = relPath(root, pos.Filename), pos.Line
	}
	if fn, ok := obj.(*types.Func); ok {
		// Own-package types print bare, others with their package name.
		qualifier := func(p *types.Package) string {
			if p == pkg.Types {
				return ""
			}
			return p.Name()
		}
		h.Signature = "func " + fn.Name() + strings.TrimPrefix(types.TypeString(fn.Type(), qualifier), "func")
	}
}

var paramRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(\.\.\.)?\}`)

func pathParams(path string) []string {
	params := []string{}
	for _, m := range paramRe.FindAllStringSubmatch(path, -1) {
		params = append(params, m[1])
	}
	return params
}

// operationID derives the client method name: "createPost" from a handler
// called createPost or handlers.CreatePost; "getApiV1PostsId" for a literal.
func operationID(handler, method, path string) string {
	if handler != "" && handler != "func literal" && handler != "nil" && !strings.ContainsAny(handler, "() ") {
		name := handler[strings.LastIndex(handler, ".")+1:]
		return strings.ToLower(name[:1]) + name[1:]
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '{' || r == '}' || r == '.' || r == '-' || r == '$' }) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	if method == "" {
		return "any" + b.String()
	}
	return b.String()
}

// schemaBuilder converts Go types to JSON Schema. Named types become
// components; a component whose name matches a schema.lidza model or type
// takes that definition, which carries the validation rules.
type schemaBuilder struct {
	defs      map[string]any
	names     map[*types.TypeName]string
	taken     map[string]*types.TypeName
	overrides map[string]any
	schemaPkg string // import path of the generated schema package
}

func newSchemaBuilder(s *schema.Schema, module string) *schemaBuilder {
	b := &schemaBuilder{defs: map[string]any{}, names: map[*types.TypeName]string{}, taken: map[string]*types.TypeName{}, overrides: map[string]any{}}
	if s != nil {
		b.overrides = schema.JSONSchema(s)
		b.schemaPkg = module + "/schema"
		// Every schema.lidza definition is a component, used by a handler or
		// not, so the client exports the whole schema. The name is reserved
		// for the generated package's type of that name.
		for name, def := range b.overrides {
			b.defs[name] = def
			b.taken[name] = nil
		}
	}
	return b
}

// component returns the component name for a named type, registering its
// schema, or "" for router.None.
func (b *schemaBuilder) component(t types.Type) string {
	s := b.schema(t)
	if s == nil {
		return ""
	}
	if ref, ok := s["$ref"].(string); ok {
		return strings.TrimPrefix(ref, "#/components/schemas/")
	}
	// An anonymous type as In or Out gets a component named after its shape.
	name := fmt.Sprintf("Anonymous%d", len(b.defs)+1)
	b.defs[name] = s
	return name
}

func (b *schemaBuilder) schema(t types.Type) map[string]any {
	t = types.Unalias(t)
	switch u := t.(type) {
	case *types.Named:
		return b.named(u)
	case *types.Basic:
		return basicSchema(u)
	case *types.Pointer:
		return nullable(b.schema(u.Elem()))
	case *types.Slice:
		if e, ok := types.Unalias(u.Elem()).(*types.Basic); ok && e.Kind() == types.Byte {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return nullable(map[string]any{"type": "array", "items": b.schema(u.Elem())})
	case *types.Array:
		return map[string]any{"type": "array", "items": b.schema(u.Elem()), "minItems": u.Len(), "maxItems": u.Len()}
	case *types.Map:
		return nullable(map[string]any{"type": "object", "additionalProperties": b.schema(u.Elem())})
	case *types.Struct:
		return b.structSchema(u)
	case *types.Interface:
		return map[string]any{}
	}
	return map[string]any{}
}

func (b *schemaBuilder) named(n *types.Named) map[string]any {
	obj := n.Obj()
	if obj.Pkg() == nil {
		if obj.Name() == "error" {
			return map[string]any{"type": "string"}
		}
		return map[string]any{}
	}
	switch obj.Pkg().Path() + "." + obj.Name() {
	case "time.Time":
		return map[string]any{"type": "string", "format": "date-time"}
	case "encoding/json.RawMessage":
		return map[string]any{}
	case routerPath + ".None":
		return nil
	}
	if name, ok := b.names[obj]; ok {
		return ref(name)
	}
	name := obj.Name()
	fromSchema := obj.Pkg().Path() == b.schemaPkg
	if other, taken := b.taken[name]; taken && other != obj && !(other == nil && fromSchema) {
		name = obj.Pkg().Name() + "_" + name
	}
	b.names[obj] = name
	b.taken[name] = obj
	if fromSchema {
		if def, ok := b.overrides[obj.Name()]; ok {
			b.defs[name] = def
			return ref(name)
		}
	}
	// Register before descending so recursive types terminate.
	b.defs[name] = map[string]any{}
	if basic, ok := n.Underlying().(*types.Basic); ok && basic.Info()&types.IsString != 0 {
		if values := enumValues(obj.Pkg(), n); len(values) > 0 {
			b.defs[name] = map[string]any{"type": "string", "enum": values}
			return ref(name)
		}
	}
	b.defs[name] = b.schema(n.Underlying())
	return ref(name)
}

// enumValues lists the string constants of type n declared in its package.
func enumValues(pkg *types.Package, n *types.Named) []string {
	var values []string
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if ok && types.Identical(c.Type(), n) && c.Val().Kind() == constant.String {
			values = append(values, constant.StringVal(c.Val()))
		}
	}
	return values
}

func (b *schemaBuilder) structSchema(st *types.Struct) map[string]any {
	props := map[string]any{}
	required := []string{}
	b.addFields(st, props, &required)
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func (b *schemaBuilder) addFields(st *types.Struct, props map[string]any, required *[]string) {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" || (!f.Exported() && !f.Embedded()) {
			continue
		}
		if f.Embedded() && name == "" {
			if inner, ok := types.Unalias(f.Type()).Underlying().(*types.Struct); ok {
				b.addFields(inner, props, required)
				continue
			}
		}
		if name == "" {
			name = f.Name()
		}
		props[name] = b.schema(f.Type())
		if !strings.Contains(opts, "omitempty") {
			*required = append(*required, name)
		}
	}
}

func basicSchema(b *types.Basic) map[string]any {
	switch {
	case b.Kind() == types.Bool || b.Kind() == types.UntypedBool:
		return map[string]any{"type": "boolean"}
	case b.Info()&types.IsInteger != 0:
		s := map[string]any{"type": "integer"}
		switch b.Kind() {
		case types.Int64, types.Uint64, types.Int, types.Uint:
			s["format"] = "int64"
		case types.Int32, types.Uint32:
			s["format"] = "int32"
		}
		return s
	case b.Info()&types.IsFloat != 0:
		return map[string]any{"type": "number", "format": "double"}
	default:
		return map[string]any{"type": "string"}
	}
}

func nullable(s map[string]any) map[string]any {
	if s == nil {
		return nil
	}
	if r, ok := s["$ref"]; ok {
		return map[string]any{"oneOf": []any{map[string]any{"$ref": r}, map[string]any{"type": "null"}}}
	}
	if t, ok := s["type"].(string); ok {
		out := map[string]any{}
		for k, v := range s {
			out[k] = v
		}
		out["type"] = []any{t, "null"}
		return out
	}
	return s
}

func ref(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
