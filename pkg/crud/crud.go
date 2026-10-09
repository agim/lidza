// Package crud generates a resource from a schema.lidza model: the sqlc
// queries, the Create and Update types, a handlers file with the five
// typed routes, and the registration in routes.go. The output is
// ordinary app code, generated once and edited freely.
//
// A resource is signed-in by default: its routes are registered on a
// group behind auth.Require(). An owned model (schema.Schema.Owner) is
// scoped to the signed-in user as well: every statement filters by the
// owner column, create sets it from auth.CurrentUser, the Create and
// Update types leave it out, and another user's row is a 404. A model
// marked @public (or generated with --public, which marks it) is neither.
package crud

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
)

// Options for Generate.
type Options struct {
	// Model is the model name in schema.lidza.
	Model string
	// Module is the app's Go module path.
	Module string
	// Force overwrites an existing handlers file.
	Force bool
	// Public marks the model @public in schema.lidza: its routes are open
	// to visitors and its rows are not scoped to a user.
	Public bool
	// Shared marks the model @shared: routes behind sign-in, rows every
	// signed-in user shares, not scoped to one.
	Shared bool
	// Auth is true when the app enables the auth pack, which a resource
	// that is not public needs.
	Auth bool
	// AppDir is lidza.json's appDir: the directory of the app's package,
	// whose routes.go gets the registration. Empty is the root.
	AppDir string
}

// Result lists what was written.
type Result struct {
	Files []string
	// RoutesLine is the registration code, one or two lines, for a
	// routes.go the generator could not edit.
	RoutesLine string
	Registered bool
	// Owner is the owner field of an owned model, "" otherwise; Public and
	// Shared are true for a @public or @shared model. Workspace is true
	// when Owner is the workspace field: rows scoped to the request's
	// workspace (auth.RequireWorkspace), not to the user.
	Owner          string
	Workspace      bool
	Public, Shared bool
	// StaleInputs are the Create and Update types in schema.lidza that
	// still take the owner field (written before the model was scoped);
	// the handlers ignore it, and the field should go.
	StaleInputs []string
}

// Generate writes the resource into root.
func Generate(root string, opt Options) (*Result, error) {
	s, err := schema.Load(root)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("no schema.lidza")
	}
	m := s.Model(opt.Model)
	if m == nil || !m.Persisted {
		return nil, fmt.Errorf("schema.lidza has no model %s (types are not resources)", opt.Model)
	}
	if _, err := os.Stat(filepath.Join(root, pack.SQLCFile)); err != nil {
		return nil, errors.New("the resource generator needs the db pack: run `lidza pack add db` first")
	}
	if opt.Public && opt.Shared {
		return nil, errors.New("--public and --shared exclude each other")
	}
	if !m.Public && !opt.Public && !opt.Auth {
		return nil, fmt.Errorf("resource %s: its routes go behind sign-in (auth.Require()) and the app has no auth pack: run `lidza pack add auth` first, or pass --public for a resource anyone may read and write", m.Name)
	}
	r := &Resource{Schema: s, Model: m, Module: opt.Module, Owner: s.Owner(m)}
	if ws := s.Workspace(m); ws != nil {
		// The rows belong to the workspace, not to whoever wrote them.
		r.Owner, r.Workspace = ws, true
	}
	if opt.Public || opt.Shared {
		r.Owner, r.Workspace = nil, false
	}
	if err := r.check(); err != nil {
		return nil, err
	}
	res := &Result{Public: m.Public || opt.Public, Shared: m.Shared || opt.Shared}
	if r.Owner != nil {
		res.Owner = r.Owner.Name
		res.Workspace = r.Workspace
	}
	handlersFile := filepath.Join("handlers", m.Table+".go")
	if _, err := os.Stat(filepath.Join(root, handlersFile)); err == nil && !opt.Force {
		return nil, fmt.Errorf("%s exists; pass --force to overwrite", filepath.ToSlash(handlersFile))
	}
	if opt.Public && !m.Public || opt.Shared && !m.Shared {
		if (opt.Public && m.Shared) || (opt.Shared && m.Public) {
			return nil, fmt.Errorf("model %s is marked @%s in %s; change the attribute there", m.Name, map[bool]string{true: "shared", false: "public"}[m.Shared], schema.FileName)
		}
		attr := "public"
		if opt.Shared {
			attr = "shared"
		}
		if err := markModel(root, m, attr); err != nil {
			return nil, err
		}
		m.Public, m.Shared = m.Public || opt.Public, m.Shared || opt.Shared
		res.Files = append(res.Files, schema.FileName)
	}
	write := func(rel, content string, overwrite bool) error {
		p := filepath.Join(root, rel)
		if _, err := os.Stat(p); err == nil && !overwrite {
			return fmt.Errorf("%s exists; pass --force to overwrite", rel)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		res.Files = append(res.Files, rel)
		return os.WriteFile(p, []byte(content), 0o644)
	}
	if err := write(filepath.Join(pack.QueriesDir, m.Table+".sql"), r.SQL(), true); err != nil {
		return nil, err
	}
	if err := write(handlersFile, schema.Gofmt(r.Handlers()), opt.Force); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, "handlers", "convert.go")); err != nil {
		if err := write(filepath.Join("handlers", "convert.go"), convertHelpers, true); err != nil {
			return nil, err
		}
	}
	added, err := appendTypes(root, s, r)
	if err != nil {
		return nil, err
	}
	if added && !slices.Contains(res.Files, schema.FileName) {
		res.Files = append(res.Files, schema.FileName)
	}
	res.StaleInputs = staleOwnerInputs(s, r)
	reg := r.registration()
	res.RoutesLine = strings.Join(reg.lines(), "\n")
	routesFile := filepath.ToSlash(filepath.Join(filepath.FromSlash(opt.AppDir), "routes.go"))
	registered, err := registerRoutes(filepath.Join(root, routesFile), opt.Module, reg, opt.Force)
	if err != nil {
		return nil, err
	}
	res.Registered = registered
	if registered {
		res.Files = append(res.Files, routesFile)
	}
	return res, nil
}

// Resource is one model with the derived names.
type Resource struct {
	Schema *schema.Schema
	Model  *schema.Model
	Module string
	// Workspace is true when Owner is a workspace field (schema
	// Schema.Workspace): its value is auth.WorkspaceID(ctx), the routes
	// sit behind auth.RequireWorkspace().
	Workspace bool
	// Owner is the field scoping the rows to the signed-in user, nil for
	// a model that is not owned.
	Owner *schema.Field
}

// Names.
func (r *Resource) Name() string   { return r.Model.Name }
func (r *Resource) Plural() string { return plural(r.Model.Name) }
func (r *Resource) Path() string   { return "/api/v1/" + strings.ToLower(plural(r.Model.Name)) }

// check rejects models the generator cannot map.
func (r *Resource) check() error {
	id := r.Model.IDField()
	if id.Type != "uuid" && id.Type != "string" && id.Type != "int" && id.Type != "bigint" {
		return fmt.Errorf("model %s: id must be uuid, string, int or bigint", r.Model.Name)
	}
	if o := r.Owner; o != nil && ((o.Type != "uuid" && o.Type != "string") || o.Optional) {
		if r.Workspace {
			return fmt.Errorf("model %s: the workspace field %s holds the workspace's id, a string: make it `%s uuid` or `%s string` (required)", r.Model.Name, o.Name, o.Name, o.Name)
		}
		return fmt.Errorf("model %s: the owner field %s holds the signed-in user's id, a string: make it `%s uuid` or `%s string` (required), or mark the model @public (--public) if its rows belong to no one", r.Model.Name, o.Name, o.Name, o.Name)
	}
	for _, f := range r.Model.Fields {
		if f.Array {
			switch f.Type {
			case "string", "text", "uuid", "int", "bigint", "float", "decimal", "bool":
			default:
				return fmt.Errorf("model %s: %s: arrays of %s are not supported by the resource generator; write that handler by hand", r.Model.Name, f.Name, f.Type)
			}
		}
	}
	return nil
}

// creatable fields: everything but the id, identity columns, timestamps
// the database fills and the owner, which comes from the signed-in user.
// editable reports whether the model has a field a client may set:
// without one there is no update, and create takes no body.
func (r *Resource) editable() bool { return len(r.creatable()) > 0 }

func (r *Resource) creatable() []*schema.Field {
	var out []*schema.Field
	for _, f := range r.Model.Fields {
		if f.ID || f == r.Owner || f.Autoincrement() || ((f.Type == "time" || f.Type == "date") && f.Default == "now()") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// listShape is what the list route offers, from the model: a search over
// its text fields, a range over its time (createdAt first), sorts over
// its plain fields (createdAt, newest first, by default; else the id),
// and equality filters on its enums, booleans and references.
type listShape struct {
	search  []*schema.Field
	span    *schema.Field
	sorts   []*schema.Field
	desc    bool
	filters []*schema.Field
}

func (r *Resource) listShape() listShape {
	var l listShape
	id := r.Model.IDField()
	var created *schema.Field
	for _, f := range r.Model.Fields {
		if f.Array || f == r.Owner {
			continue
		}
		enum := r.Schema.Enum(f.Type) != nil
		switch {
		case (f.Type == "string" || f.Type == "text") && !f.ID && f.Ref == "":
			l.search = append(l.search, f)
		}
		if f.Type == "time" && (l.span == nil || f.Name == "createdAt") {
			l.span = f
		}
		if f.Name == "createdAt" && f.Type == "time" {
			created = f
		}
		switch f.Type {
		case "string", "int", "bigint", "float", "decimal", "time", "date", "bool", "uuid":
			if f.Type != "uuid" || f.ID {
				l.sorts = append(l.sorts, f)
			}
		default:
			if enum {
				l.sorts = append(l.sorts, f)
			}
		}
		if !f.ID && (enum || f.Type == "bool" || f.Ref != "") {
			l.filters = append(l.filters, f)
		}
	}
	first := id
	if created != nil {
		first, l.desc = created, true
	}
	sorts := []*schema.Field{first}
	for _, f := range l.sorts {
		if f != first {
			sorts = append(sorts, f)
		}
	}
	l.sorts = sorts
	return l
}

// listConds are the WHERE conditions of the list and count queries.
func (r *Resource) listConds(l listShape) []string {
	var conds []string
	if o := r.Owner; o != nil {
		conds = append(conds, fmt.Sprintf("%s = sqlc.arg('%s')", col(o), snake(o.Name)))
	}
	if len(l.search) > 0 {
		cols := make([]string, len(l.search))
		for i, f := range l.search {
			cols[i] = col(f)
		}
		// position, not LIKE: a % or _ in the search is just a character.
		conds = append(conds, fmt.Sprintf("(sqlc.narg('q')::text IS NULL OR position(lower(sqlc.narg('q')::text) in lower(concat_ws(' ', %s))) > 0)", strings.Join(cols, ", ")))
	}
	if f := l.span; f != nil {
		conds = append(conds,
			fmt.Sprintf("(sqlc.narg('since')::timestamptz IS NULL OR %s >= sqlc.narg('since')::timestamptz)", col(f)),
			fmt.Sprintf("(sqlc.narg('until')::timestamptz IS NULL OR %s < sqlc.narg('until')::timestamptz)", col(f)))
	}
	for _, f := range l.filters {
		conds = append(conds, fmt.Sprintf("(sqlc.narg('%s')::text IS NULL OR %s::text = sqlc.narg('%s')::text)", snake(f.Name), col(f), snake(f.Name)))
	}
	return conds
}

// listSQL renders the list and count queries.
func (r *Resource) listSQL(table string) string {
	l := r.listShape()
	conds := r.listConds(l)
	where := ""
	if len(conds) > 0 {
		where = "\nWHERE " + strings.Join(conds, "\n  AND ")
	}
	var order []string
	for _, f := range l.sorts {
		order = append(order,
			fmt.Sprintf("CASE WHEN sqlc.arg('sort')::text = '%s' AND NOT sqlc.arg('desc')::bool THEN %s END ASC", f.Name, col(f)),
			fmt.Sprintf("CASE WHEN sqlc.arg('sort')::text = '%s' AND sqlc.arg('desc')::bool THEN %s END DESC", f.Name, col(f)))
	}
	order = append(order, col(r.Model.IDField()))
	var b strings.Builder
	b.WriteString("-- The list: search, range and filters are optional (NULL for none), sort\n-- one of the CASE keys; list.Read in the handler validates them.\n")
	fmt.Fprintf(&b, "-- name: List%s :many\nSELECT * FROM %s%s\nORDER BY\n  %s\nLIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;\n\n", r.Plural(), table, where, strings.Join(order, ",\n  "))
	fmt.Fprintf(&b, "-- name: Count%s :one\nSELECT count(*) FROM %s%s;\n\n", r.Plural(), table, where)
	return b.String()
}

// listHandler renders the body of the list handler.
func (r *Resource) listHandler(owner string) string {
	l := r.listShape()
	quote := func(fs []*schema.Field) string {
		q := make([]string, len(fs))
		for i, f := range fs {
			q[i] = strconv.Quote(f.Name)
		}
		return strings.Join(q, ", ")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\tp, err := list.Read(req, list.Options{Sorts: []string{%s}, Desc: %v", quote(l.sorts), l.desc)
	if len(l.filters) > 0 {
		fmt.Fprintf(&b, ", Filters: []string{%s}", quote(l.filters))
	}
	b.WriteString("})\n\tif err != nil {\n\t\treturn schema." + r.Name() + "List{}, err\n\t}\n")
	// The arguments both queries share, in sqlc's order of first use.
	type arg struct{ field, value string }
	var shared []arg
	if o := r.Owner; o != nil {
		shared = append(shared, arg{sqlcName(o), owner})
	}
	if len(l.search) > 0 {
		shared = append(shared, arg{"Q", "p.Search()"})
	}
	if l.span != nil {
		shared = append(shared, arg{"Since", "p.Since"}, arg{"Until", "p.Until"})
	}
	for _, f := range l.filters {
		shared = append(shared, arg{sqlcName(f), fmt.Sprintf("p.Filter(%q)", f.Name)})
	}
	fields := func(args []arg) string {
		out := make([]string, len(args))
		for i, a := range args {
			out[i] = a.field + ": " + a.value
		}
		return strings.Join(out, ", ")
	}
	listArgs := append(append([]arg{}, shared...), arg{"Sort", "p.Sort"}, arg{"Desc", "p.Desc"}, arg{"Lim", "p.Limit"}, arg{"Off", "p.Offset"})
	fmt.Fprintf(&b, "\tq := queries.New(db.From(ctx))\n\trows, err := q.List%s(ctx, queries.List%sParams{%s})\n\tif err != nil {\n\t\treturn schema.%sList{}, err\n\t}\n", r.Plural(), r.Plural(), fields(listArgs), r.Name())
	// sqlc passes a lone parameter bare, several in a Params struct.
	var countArgs string
	switch len(shared) {
	case 0:
	case 1:
		countArgs = ", " + shared[0].value
	default:
		countArgs = fmt.Sprintf(", queries.Count%sParams{%s}", r.Plural(), fields(shared))
	}
	fmt.Fprintf(&b, "\ttotal, err := q.Count%s(ctx%s)\n\tif err != nil {\n\t\treturn schema.%sList{}, err\n\t}\n", r.Plural(), countArgs, r.Name())
	return b.String()
}

// SQL renders db/queries/<table>.sql.
func (r *Resource) SQL() string {
	t := qid(r.Model.Table)
	id := r.Model.IDField()
	var b strings.Builder
	fmt.Fprintf(&b, "-- Queries for %s, generated by lidza gen resource. Edit freely; regenerate to reset.\n", r.Model.Name)
	if o := r.Owner; o != nil {
		// Owned: every statement filters by, or sets, the owner column.
		oc := col(o)
		if r.Workspace {
			fmt.Fprintf(&b, "-- Scoped to the request's workspace: every statement takes it (%s).\n\n", oc)
		} else {
			fmt.Fprintf(&b, "-- Scoped to the signed-in user: every statement takes the owner (%s).\n\n", oc)
		}
		b.WriteString(r.listSQL(t))
		fmt.Fprintf(&b, "-- name: Get%s :one\nSELECT * FROM %s WHERE %s = $1 AND %s = $2;\n\n", r.Name(), t, col(id), oc)
	} else {
		b.WriteString("\n")
		b.WriteString(r.listSQL(t))
		fmt.Fprintf(&b, "-- name: Get%s :one\nSELECT * FROM %s WHERE %s = $1;\n\n", r.Name(), t, col(id))
	}
	var cols, vals []string
	inserted := r.creatable()
	if r.Owner != nil {
		inserted = append([]*schema.Field{r.Owner}, inserted...)
	}
	for i, f := range inserted {
		cols = append(cols, col(f))
		vals = append(vals, fmt.Sprintf("$%d", i+1))
	}
	if len(cols) == 0 {
		fmt.Fprintf(&b, "-- name: Create%s :one\nINSERT INTO %s DEFAULT VALUES RETURNING *;\n\n", r.Name(), t)
	} else {
		fmt.Fprintf(&b, "-- name: Create%s :one\nINSERT INTO %s (%s) VALUES (%s) RETURNING *;\n\n", r.Name(), t, strings.Join(cols, ", "), strings.Join(vals, ", "))
	}
	var sets []string
	for _, f := range r.creatable() {
		sets = append(sets, fmt.Sprintf("%s = COALESCE(sqlc.narg('%s'), %s)", col(f), snake(f.Name), col(f)))
	}
	where := fmt.Sprintf("%s = sqlc.arg('%s')", col(id), snake(id.Name))
	if o := r.Owner; o != nil {
		where += fmt.Sprintf(" AND %s = sqlc.arg('%s')", col(o), snake(o.Name))
	}
	// A model with nothing to change (an id and timestamps) has no update.
	if len(sets) > 0 {
		fmt.Fprintf(&b, "-- name: Update%s :one\nUPDATE %s SET %s WHERE %s RETURNING *;\n\n", r.Name(), t, strings.Join(sets, ", "), where)
	}
	if o := r.Owner; o != nil {
		fmt.Fprintf(&b, "-- name: Delete%s :execrows\nDELETE FROM %s WHERE %s = $1 AND %s = $2;\n", r.Name(), t, col(id), col(o))
	} else {
		fmt.Fprintf(&b, "-- name: Delete%s :execrows\nDELETE FROM %s WHERE %s = $1;\n", r.Name(), t, col(id))
	}
	return b.String()
}

// Handlers renders handlers/<table>.go.
func (r *Resource) Handlers() string {
	name, plural := r.Name(), r.Plural()
	lower := lowerFirst(name)
	id := r.Model.IDField()
	var b strings.Builder
	fmt.Fprintf(&b, "// Generated once by lidza gen resource %s; edit freely.\n", name)
	switch {
	case r.Workspace:
		fmt.Fprintf(&b, "// Scoped to the request's workspace: every query takes %s from\n// auth.WorkspaceID (the routes sit behind auth.RequireWorkspace), so\n// another workspace's row is a 404.\n", r.Owner.Name)
	case r.Owner != nil:
		fmt.Fprintf(&b, "// Scoped to the signed-in user: every query takes %s from\n// auth.CurrentUser, so another user's row is a 404.\n", r.Owner.Name)
	}
	b.WriteString("\npackage handlers\n\n")
	b.WriteString("import (\n\t\"context\"\n\t\"errors\"\n\t\"net/http\"\n")
	if r.usesJSON() {
		b.WriteString("\t\"encoding/json\"\n")
	}
	if id.Type == "int" || id.Type == "bigint" {
		b.WriteString("\t\"strconv\"\n")
	}
	b.WriteString("\n\t\"github.com/jackc/pgx/v5\"\n\n")
	if r.Owner != nil {
		b.WriteString("\t\"github.com/agim/lidza/packs/auth\"\n")
	}
	b.WriteString("\t\"github.com/agim/lidza/packs/db\"\n\t\"github.com/agim/lidza/pkg/list\"\n\t\"github.com/agim/lidza/pkg/router\"\n\n")
	fmt.Fprintf(&b, "\t\"%s/db/queries/gen\"\n\t\"%s/schema\"\n)\n\n", r.Module, r.Module)

	fmt.Fprintf(&b, "// %sRoutes registers the %s resource under %s.\nfunc %sRoutes(r *router.Router) {\n", name, name, r.Path(), name)
	fmt.Fprintf(&b, "\trouter.Route(r, \"GET %s\", list%s)\n", r.Path(), plural)
	fmt.Fprintf(&b, "\trouter.Route(r, \"GET %s/{id}\", get%s)\n", r.Path(), name)
	fmt.Fprintf(&b, "\trouter.Route(r, \"POST %s\", create%s)\n", r.Path(), name)
	if r.editable() {
		fmt.Fprintf(&b, "\trouter.Route(r, \"PATCH %s/{id}\", update%s)\n", r.Path(), name)
	}
	fmt.Fprintf(&b, "\trouter.Route(r, \"DELETE %s/{id}\", delete%s)\n}\n\n", r.Path(), name)

	// id parsing
	idParse := "id := req.Param(\"id\")\n"
	if id.Type == "int" {
		idParse = "id64, err := strconv.ParseInt(req.Param(\"id\"), 10, 32)\n\tif err != nil {\n\t\treturn out, router.Errorf(http.StatusBadRequest, \"id must be a number\")\n\t}\n\tid := int32(id64)\n"
	} else if id.Type == "bigint" {
		idParse = "id, err := strconv.ParseInt(req.Param(\"id\"), 10, 64)\n\tif err != nil {\n\t\treturn out, router.Errorf(http.StatusBadRequest, \"id must be a number\")\n\t}\n"
	}

	// The owner: the signed-in user, never the request body. byID is the
	// argument of the get and delete queries.
	owner, ownerField, byID := "", "", "id"
	if o := r.Owner; o != nil {
		owner = "auth.CurrentUser(ctx).ID"
		if r.Workspace {
			owner = "auth.WorkspaceID(ctx)"
		}
		ownerField = fmt.Sprintf("\t\t%s: %s,\n", sqlcName(o), owner)
		byID = fmt.Sprintf("queries.Get%sParams{%s: id, %s: %s}", name, sqlcName(id), sqlcName(o), owner)
	}

	fmt.Fprintf(&b, "func list%s(ctx context.Context, req *router.Request[router.None]) (schema.%sList, error) {\n", plural, name)
	if r.Owner != nil {
		fmt.Fprintf(&b, "\towner := %s\n", owner)
		b.WriteString(r.listHandler("owner"))
	} else {
		b.WriteString(r.listHandler(""))
	}
	fmt.Fprintf(&b, "\titems := make([]schema.%s, len(rows))\n\tfor i, row := range rows {\n\t\titems[i] = to%s(row)\n\t}\n\treturn schema.%sList{Items: items, Total: int(total)}, nil\n}\n\n", name, name, name)

	fmt.Fprintf(&b, "func get%s(ctx context.Context, req *router.Request[router.None]) (schema.%s, error) {\n\tvar out schema.%s\n\t%s", name, name, name, idParse)
	fmt.Fprintf(&b, "\trow, err := queries.New(db.From(ctx)).Get%s(ctx, %s)\n\tif errors.Is(err, pgx.ErrNoRows) {\n\t\treturn out, router.NotFound(\"%s\")\n\t}\n\tif err != nil {\n\t\treturn out, err\n\t}\n\treturn to%s(row), nil\n}\n\n", name, byID, lower, name)

	// sqlc passes no argument for a query without parameters, the value
	// itself for one, and a Params struct for more.
	body, in := "router.None", ""
	if r.editable() {
		body, in = "schema.Create"+name, "\tin := req.Body\n"
	}
	fmt.Fprintf(&b, "func create%s(ctx context.Context, req *router.Request[%s]) (schema.%s, error) {\n%s\trow, err := queries.New(db.From(ctx)).Create%s(ctx", name, body, name, in, name)
	switch params := r.creatable(); {
	case len(params) == 0 && r.Owner == nil:
		b.WriteString(")\n")
	case len(params) == 0:
		fmt.Fprintf(&b, ", %s)\n", owner)
	case len(params) == 1 && r.Owner == nil:
		fmt.Fprintf(&b, ", %s)\n", r.toParam(params[0], "in."+exported(params[0].Name), false))
	default:
		fmt.Fprintf(&b, ", queries.Create%sParams{\n%s", name, ownerField)
		for _, f := range params {
			fmt.Fprintf(&b, "\t\t%s: %s,\n", sqlcName(f), r.toParam(f, "in."+exported(f.Name), false))
		}
		b.WriteString("\t})\n")
	}
	fmt.Fprintf(&b, "\tif err != nil {\n\t\treturn schema.%s{}, err\n\t}\n\treq.Status(http.StatusCreated)\n\treturn to%s(row), nil\n}\n\n", name, name)

	if r.editable() {
		fmt.Fprintf(&b, "func update%s(ctx context.Context, req *router.Request[schema.Update%s]) (schema.%s, error) {\n\tvar out schema.%s\n\t%s\tin := req.Body\n\trow, err := queries.New(db.From(ctx)).Update%s(ctx, queries.Update%sParams{\n\t\t%s: id,\n%s", name, name, name, name, idParse, name, name, sqlcName(id), ownerField)
		for _, f := range r.creatable() {
			fmt.Fprintf(&b, "\t\t%s: %s,\n", sqlcName(f), r.toParam(f, "in."+exported(f.Name), true))
		}
		fmt.Fprintf(&b, "\t})\n\tif errors.Is(err, pgx.ErrNoRows) {\n\t\treturn out, router.NotFound(\"%s\")\n\t}\n\tif err != nil {\n\t\treturn out, err\n\t}\n\treturn to%s(row), nil\n}\n\n", lower, name)
	}

	if r.Owner != nil {
		byID = strings.Replace(byID, "queries.Get", "queries.Delete", 1)
	}
	fmt.Fprintf(&b, "func delete%s(ctx context.Context, req *router.Request[router.None]) (router.None, error) {\n\tvar out router.None\n\t%s", name, idParse)
	fmt.Fprintf(&b, "\tn, err := queries.New(db.From(ctx)).Delete%s(ctx, %s)\n\tif err != nil {\n\t\treturn out, err\n\t}\n\tif n == 0 {\n\t\treturn out, router.NotFound(\"%s\")\n\t}\n\treturn out, nil\n}\n\n", name, byID, lower)

	fmt.Fprintf(&b, "// to%s maps a row to the API type.\nfunc to%s(row queries.%s) schema.%s {\n\treturn schema.%s{\n", name, name, schema.SQLCRowType(r.Model.Table), name, name)
	for _, f := range r.Model.Fields {
		fmt.Fprintf(&b, "\t\t%s: %s,\n", exported(f.Name), r.fromRow(f, "row."+sqlcName(f)))
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

func (r *Resource) usesJSON() bool {
	for _, f := range r.Model.Fields {
		if f.Type == "json" {
			return true
		}
	}
	return false
}

// fromRow renders the expression converting a sqlc row field to the
// schema type.
func (r *Resource) fromRow(f *schema.Field, expr string) string {
	isEnum := r.Schema.Enum(f.Type) != nil
	switch {
	case f.Array && f.Type == "int":
		return "IntsFrom(" + expr + ")"
	case f.Array:
		return expr
	case f.Type == "int" && f.Optional:
		return "PtrInt(" + expr + ")"
	case f.Type == "int":
		return "int(" + expr + ")"
	case f.Type == "json":
		return "json.RawMessage(" + expr + ")"
	case isEnum && f.Optional:
		return fmt.Sprintf("(*schema.%s)(%s)", f.Type, expr)
	case isEnum:
		return fmt.Sprintf("schema.%s(%s)", f.Type, expr)
	}
	return expr
}

// toParam renders the expression converting a schema value to the sqlc
// parameter type; forUpdate treats every field as optional (narg).
func (r *Resource) toParam(f *schema.Field, expr string, forUpdate bool) string {
	isEnum := r.Schema.Enum(f.Type) != nil
	optional := f.Optional || forUpdate
	switch {
	case f.Array && f.Type == "int":
		return "Int32sFrom(" + expr + ")"
	case f.Array:
		return expr
	case f.Type == "int" && optional:
		return "PtrInt32(" + expr + ")"
	case f.Type == "int":
		return "int32(" + expr + ")"
	case f.Type == "json":
		return "[]byte(" + expr + ")"
	case isEnum && optional:
		return fmt.Sprintf("(*queries.%s)(%s)", schema.SQLCName(snake(f.Type)), expr)
	case isEnum:
		return fmt.Sprintf("queries.%s(%s)", schema.SQLCName(snake(f.Type)), expr)
	case forUpdate && !f.Optional && !f.Array && f.Type != "bytes":
		// Update types make every field optional (pointer); sqlc nargs take
		// pointers too.
		return expr
	}
	return expr
}

const convertHelpers = `// Generated once by lidza gen resource; helpers shared by the resources.

package handlers

import (
	"strconv"

	"github.com/agim/lidza/pkg/router"
)

// PageParams reads limit and offset from the query with a default and a
// cap on the limit.
func PageParams[In any](req *router.Request[In], def, max int) (limit, offset int32) {
	limit = int32(def)
	if v, err := strconv.ParseInt(req.Query("limit"), 10, 32); err == nil && v > 0 {
		limit = int32(v)
		if int(limit) > max {
			limit = int32(max)
		}
	}
	if v, err := strconv.ParseInt(req.Query("offset"), 10, 32); err == nil && v >= 0 {
		offset = int32(v)
	}
	return limit, offset
}

// PtrInt converts a nullable int32 column to the schema's *int.
func PtrInt(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

// PtrInt32 converts the schema's *int to a nullable int32 parameter.
func PtrInt32(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}

// IntsFrom converts an integer[] column.
func IntsFrom(in []int32) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

// Int32sFrom converts to an integer[] parameter.
func Int32sFrom(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}
`

// appendTypes adds Create<Model>, Update<Model> and <Model>List to
// schema.lidza when missing.
func appendTypes(root string, s *schema.Schema, r *Resource) (bool, error) {
	var b strings.Builder
	name := r.Name()
	if s.Model("Create"+name) == nil && r.editable() {
		fmt.Fprintf(&b, "\n// Body of POST %s.\ntype Create%s {\n", r.Path(), name)
		for _, f := range r.creatable() {
			b.WriteString("  " + fieldLine(f, false) + "\n")
		}
		b.WriteString("}\n")
	}
	if s.Model("Update"+name) == nil && r.editable() {
		fmt.Fprintf(&b, "\n// Body of PATCH %s/{id}: every field optional, absent fields keep their value.\ntype Update%s {\n", r.Path(), name)
		for _, f := range r.creatable() {
			b.WriteString("  " + fieldLine(f, true) + "\n")
		}
		b.WriteString("}\n")
	}
	if s.Model(name+"List") == nil {
		fmt.Fprintf(&b, "\n// Reply of GET %s.\ntype %sList {\n  items %s[]\n  total int\n}\n", r.Path(), name, name)
	}
	if b.Len() == 0 {
		return false, nil
	}
	f, err := os.OpenFile(filepath.Join(root, schema.FileName), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return true, err
}

// fieldLine renders a field for a type block, keeping its validation
// rules and, for updates, making it optional.
func fieldLine(f *schema.Field, optional bool) string {
	t := f.TypeText()
	if f.Array {
		t += "[]"
	}
	if f.Optional || optional {
		t += "?"
	}
	parts := []string{f.Name, t}
	switch {
	case f.MinText != "":
		parts = append(parts, "@min("+f.MinText+")")
	case f.Min != nil:
		parts = append(parts, "@min("+fnum(*f.Min)+")")
	}
	switch {
	case f.MaxText != "":
		parts = append(parts, "@max("+f.MaxText+")")
	case f.Max != nil:
		parts = append(parts, "@max("+fnum(*f.Max)+")")
	}
	if f.Email {
		parts = append(parts, "@email")
	}
	if f.URL {
		parts = append(parts, "@url")
	}
	if f.Pattern != "" {
		parts = append(parts, fmt.Sprintf("@pattern(%q)", f.Pattern))
	}
	return strings.Join(parts, " ")
}

// registration is the routes.go code of a resource: for one that is not
// public, a group behind auth.Require() and the call on it.
type registration struct {
	// call is "handlers.<Model>Routes(", the mark of a registration.
	call string
	// group is the group's variable, "" for a public resource.
	group, prefix string
	// middleware guards the group: auth.Require(), or
	// auth.RequireWorkspace() for a workspace-scoped resource.
	middleware string
}

func (r *Resource) registration() registration {
	reg := registration{call: "handlers." + r.Name() + "Routes("}
	if !r.Model.Public {
		reg.group, reg.prefix, reg.middleware = lowerFirst(r.Plural()), r.Path(), "auth.Require()"
		if r.Workspace {
			reg.middleware = "auth.RequireWorkspace()"
		}
	}
	return reg
}

// lines renders the registration.
func (reg registration) lines() []string {
	if reg.group == "" {
		return []string{reg.call + "r)"}
	}
	return []string{
		fmt.Sprintf("%s := r.Group(%q, %s)", reg.group, reg.prefix, reg.middleware),
		reg.call + reg.group + ")",
	}
}

// registerRoutes adds the registration to routes.go with the imports it
// needs, unless the resource is registered already. With force, a
// registration without sign-in (handlers.<Model>Routes(r)) moves behind
// auth.Require(). It reports false when routes.go does not have the
// expected shape, so the caller prints the lines to add.
func registerRoutes(p, module string, reg registration, force bool) (bool, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return false, nil
	}
	src := string(data)
	if strings.Contains(src, reg.call) {
		old := "\t" + reg.call + "r)\n"
		if !force || reg.group == "" || !strings.Contains(src, old) {
			return true, nil
		}
		src = strings.Replace(src, old, "", 1)
	}
	marker := "func routes(r *router.Router) {\n"
	if !strings.Contains(src, marker) {
		return false, nil
	}
	if reg.group != "" {
		// A variable of that name in routes() already: take another.
		if strings.Contains(src, "\t"+reg.group+" := ") || strings.Contains(src, "\t"+reg.group+", ") {
			reg.group += "Group"
		}
		authImp := fmt.Sprintf("\t%q\n", "github.com/agim/lidza/packs/auth")
		routerImp := fmt.Sprintf("\t%q\n", "github.com/agim/lidza/pkg/router")
		if !strings.Contains(src, authImp) {
			if !strings.Contains(src, routerImp) {
				return false, nil
			}
			src = strings.Replace(src, routerImp, authImp+routerImp, 1)
		}
	}
	src = strings.Replace(src, marker, marker+"\t"+strings.Join(reg.lines(), "\n\t")+"\n", 1)
	imp := fmt.Sprintf("\t%q\n", module+"/handlers")
	if !strings.Contains(src, imp) {
		// Into the app's own import group when there is one, else a group
		// of its own at the end of the block.
		if own := fmt.Sprintf("\t%q\n", module+"/schema"); strings.Contains(src, own) {
			src = strings.Replace(src, own, imp+own, 1)
		} else if i := strings.Index(src, "import (\n"); i >= 0 {
			if end := strings.Index(src[i:], "\n)\n"); end >= 0 {
				src = src[:i+end] + "\n\n" + strings.TrimSuffix(imp, "\n") + src[i+end:]
			} else {
				return false, nil
			}
		} else {
			return false, nil
		}
	}
	return true, os.WriteFile(p, []byte(schema.Gofmt(src)), 0o644)
}

// markModel adds @attr (public or shared) to the model's line in schema.lidza.
func markModel(root string, m *schema.Model, attr string) error {
	p := filepath.Join(root, schema.FileName)
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	if m.Line < 1 || m.Line > len(lines) {
		return fmt.Errorf("%s: model %s not found", schema.FileName, m.Name)
	}
	line := lines[m.Line-1]
	brace := strings.Index(line, "{")
	if brace < 0 || !strings.Contains(line[:brace], m.Name) {
		return fmt.Errorf("%s: model %s not found on line %d", schema.FileName, m.Name, m.Line)
	}
	lines[m.Line-1] = strings.TrimRight(line[:brace], " \t") + " @" + attr + " " + line[brace:]
	return os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644)
}

// staleOwnerInputs names the Create and Update types in schema.lidza that
// still carry the owner field (written before the generator scoped the
// model): the handlers ignore it, and the body should not take it.
func staleOwnerInputs(s *schema.Schema, r *Resource) []string {
	if r.Owner == nil {
		return nil
	}
	var out []string
	for _, name := range []string{"Create" + r.Name(), "Update" + r.Name()} {
		if t := s.Model(name); t != nil {
			for _, f := range t.Fields {
				if f.Name == r.Owner.Name {
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// Naming helpers, matching pkg/schema for the schema side and sqlc for
// the query side.

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + 'a' - 'A')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// exported is the schema package's name for a field. sqlcName is sqlc's
// name for its column: the same, since lidza gen writes the schema's
// initialisms and their plurals into sqlc.yaml.
func exported(s string) string { return schema.GoName(s) }

func sqlcName(f *schema.Field) string { return schema.GoName(f.Name) }

var reserved = map[string]bool{"user": true, "order": true, "group": true, "table": true, "select": true, "from": true, "where": true, "limit": true, "offset": true, "default": true, "check": true, "primary": true, "references": true, "to": true, "in": true, "on": true, "or": true, "and": true, "not": true, "null": true, "with": true, "all": true, "any": true, "as": true, "asc": true, "desc": true, "column": true, "constraint": true, "create": true, "distinct": true, "do": true, "else": true, "end": true, "grant": true, "having": true, "into": true, "only": true, "then": true, "union": true, "unique": true, "using": true, "when": true}

func qid(name string) string {
	if reserved[name] {
		return `"` + name + `"`
	}
	return name
}

func col(f *schema.Field) string { return qid(snake(f.Name)) }

func plural(name string) string {
	switch {
	case strings.HasSuffix(name, "s"), strings.HasSuffix(name, "x"), strings.HasSuffix(name, "ch"), strings.HasSuffix(name, "sh"):
		return name + "es"
	case strings.HasSuffix(name, "y") && len(name) > 1 && !strings.ContainsRune("aeiou", rune(name[len(name)-2])):
		return name[:len(name)-1] + "ies"
	}
	return name + "s"
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func fnum(v float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
	return s
}
