// Package schema parses schema.lidza, the single source of truth for an
// application's data shapes, and generates Go structs with validation, the
// Postgres DDL and migrations, Rust serde structs and TypeScript types
// from it.
//
// The file format:
//
//	enum Status { draft published }
//
//	model Post @table("posts") {
//	  id        uuid     @id @default(uuid())
//	  title     string   @min(1) @max(200)
//	  body      text?
//	  status    Status   @default(draft)
//	  authorId  uuid     @ref(User)
//	  topicId   int?     @ref(Topic, setnull)
//	  tags      string[]
//	  price     decimal(12, 2)? @min(0)
//	  createdAt time     @default(now())
//	  @@index(status, createdAt)
//	}
//
//	type CreatePost {
//	  title string @min(1) @max(200)
//	  body  text?
//	}
//
// A model becomes a table and a struct; a type becomes a struct only. "?"
// marks an optional (nullable) field, "[]" an array. Scalar types: string,
// text, int, bigint, float, decimal(p, s), bool, time, date, uuid, json,
// bytes. Field attributes: @id, @unique, @index, @default(v) (uuid(),
// now(), autoincrement() on an int or bigint, or a literal),
// @ref(Model), @min(n), @max(n), @email, @url, @pattern("re"). Block
// attributes: @@index(a, b),
// @@unique(a, b); model attributes @table("name"), @public and @shared. A @ref
// field has the type of the referenced model's id: uuid, int, bigint or
// string.
//
// A model is owned when a field ties each row to a user (Schema.Owner):
// `lidza gen resource` scopes its queries to the signed-in user and
// `lidza check` (L018) flags a query that does not. @public marks a model
// whose rows anyone may read and write: not owned, and its generated
// routes are not behind sign-in. @shared marks one whose rows every
// signed-in user shares: routes behind sign-in, queries not scoped, even
// with an owner-like field (a team's projects with a createdBy user).
package schema

import (
	"fmt"
	"sort"
	"strings"
)

// FileName is the schema source, at the project root.
const FileName = "schema.lidza"

// Schema is a parsed schema.lidza.
type Schema struct {
	Enums  []*Enum
	Models []*Model // persisted: table and struct
	Types  []*Model // API shapes: struct only
}

// Enum is a named set of string values.
type Enum struct {
	Name   string
	Values []string
	Line   int
}

// Model is a model or type block.
type Model struct {
	Name string
	// Table is the Postgres table name; empty for types.
	Table   string
	Fields  []*Field
	Indexes []Index
	Line    int
	// Persisted is true for "model", false for "type".
	Persisted bool
	// Public is the model attribute @public: the rows are not owned and
	// the generated routes are open to visitors.
	Public bool `json:",omitempty"`
	// Shared is the model attribute @shared: the rows are not owned and
	// the generated routes are behind sign-in.
	Shared bool `json:",omitempty"`
}

// Field is one line of a block.
type Field struct {
	Name string
	// Type is a scalar name, an enum name or (for types) another type name.
	Type     string
	Optional bool
	Array    bool
	ID       bool
	Unique   bool
	Index    bool
	// Default is the literal as written: 42, true, "x", draft, uuid(),
	// now(), autoincrement().
	Default string
	// Ref is the model the field references (the field has the type of
	// that model's id); OnDelete is what happens
	// to the row when the referenced one is deleted: "cascade" (delete
	// it), "setnull" (clear the field; it must be optional) or "" (the
	// delete fails while the row exists).
	Ref      string
	OnDelete string
	Min      *float64
	Max      *float64
	// MinText and MaxText are @min and @max as written, for a decimal
	// field, whose rules compare exactly.
	MinText string `json:",omitempty"`
	MaxText string `json:",omitempty"`
	// Precision and Scale are the arguments of decimal(p, s).
	Precision int `json:",omitempty"`
	Scale     int `json:",omitempty"`
	Email     bool
	URL       bool
	// Timezone requires an IANA zone name ("Europe/Paris").
	Timezone bool
	Pattern  string
	Line     int
}

// Index is a block-level @@index or @@unique.
type Index struct {
	Fields []string
	Unique bool
}

// Scalars are the built-in types.
var Scalars = map[string]bool{
	"string": true, "text": true, "int": true, "bigint": true, "float": true,
	"decimal": true, "bool": true, "time": true, "date": true, "uuid": true, "json": true, "bytes": true,
	// localtime is a wall-clock date and time with no zone, for API
	// types: the server reads it in the visitor's zone (i18n At).
	"localtime": true,
}

// Enum returns the enum called name, or nil.
func (s *Schema) Enum(name string) *Enum {
	for _, e := range s.Enums {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Model returns the model or type called name, or nil.
func (s *Schema) Model(name string) *Model {
	for _, m := range s.Models {
		if m.Name == name {
			return m
		}
	}
	for _, m := range s.Types {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// IDField returns the model's @id field, or nil.
func (m *Model) IDField() *Field {
	for _, f := range m.Fields {
		if f.ID {
			return f
		}
	}
	return nil
}

// Autoincrement reports whether the field is an identity column,
// @default(autoincrement()): the database numbers the rows and inserts
// leave it out.
func (f *Field) Autoincrement() bool { return f.Default == "autoincrement()" }

// TypeText is the field's type as written, without ? and []:
// "decimal(12, 2)" for a decimal, else Type.
func (f *Field) TypeText() string {
	if f.Type == "decimal" {
		return fmt.Sprintf("decimal(%d, %d)", f.Precision, f.Scale)
	}
	return f.Type
}

// validate checks references and rules after parsing.
func (s *Schema) validate() error {
	names := map[string]int{}
	for _, e := range s.Enums {
		if names[e.Name] > 0 {
			return fmt.Errorf("line %d: %s declared twice", e.Line, e.Name)
		}
		names[e.Name] = e.Line
		if len(e.Values) == 0 {
			return fmt.Errorf("line %d: enum %s has no values", e.Line, e.Name)
		}
	}
	for _, m := range append(append([]*Model{}, s.Models...), s.Types...) {
		if names[m.Name] > 0 {
			return fmt.Errorf("line %d: %s declared twice", m.Line, m.Name)
		}
		names[m.Name] = m.Line
	}
	for _, m := range append(append([]*Model{}, s.Models...), s.Types...) {
		seen := map[string]bool{}
		ids := 0
		for _, f := range m.Fields {
			if seen[f.Name] {
				return fmt.Errorf("line %d: field %s.%s declared twice", f.Line, m.Name, f.Name)
			}
			seen[f.Name] = true
			if f.ID {
				ids++
			}
			switch {
			case Scalars[f.Type]:
			case s.Enum(f.Type) != nil:
			case s.Model(f.Type) != nil:
				if m.Persisted {
					key := "uuid"
					if id := s.Model(f.Type).IDField(); id != nil {
						key = id.Type
					}
					return fmt.Errorf("line %d: %s.%s: a model field cannot have type %s; use %s @ref(%s)", f.Line, m.Name, f.Name, f.Type, key, f.Type)
				}
			default:
				return fmt.Errorf("line %d: %s.%s: unknown type %s", f.Line, m.Name, f.Name, f.Type)
			}
			if f.Ref != "" {
				target := s.Model(f.Ref)
				if target == nil || !target.Persisted {
					return fmt.Errorf("line %d: %s.%s: @ref(%s) is not a model", f.Line, m.Name, f.Name, f.Ref)
				}
				if f.OnDelete == "setnull" && !f.Optional {
					return fmt.Errorf("line %d: %s.%s: @ref(%s, setnull) needs an optional field (%s?)", f.Line, m.Name, f.Name, f.Ref, f.Type)
				}
				if f.Array {
					return fmt.Errorf("line %d: %s.%s: @ref(%s) on an array; a foreign key holds one id (a join model holds many)", f.Line, m.Name, f.Name, f.Ref)
				}
				// The target's own check reports a model without an @id.
				if id := target.IDField(); id != nil && !sameKey(f.Type, id.Type) {
					return fmt.Errorf("line %d: %s.%s: @ref(%s) needs type %s, the type of %s.%s, not %s", f.Line, m.Name, f.Name, f.Ref, id.Type, target.Name, id.Name, f.Type)
				}
			}
			if f.Default != "" {
				if e := s.Enum(f.Type); e != nil && !contains(e.Values, f.Default) {
					return fmt.Errorf("line %d: %s.%s: default %s is not a value of %s", f.Line, m.Name, f.Name, f.Default, f.Type)
				}
			}
			if f.Autoincrement() {
				if f.Type != "int" && f.Type != "bigint" || f.Optional || f.Array || !m.Persisted {
					return fmt.Errorf("line %d: %s.%s: @default(autoincrement()) needs an int or bigint field of a model, neither optional nor an array", f.Line, m.Name, f.Name)
				}
			}
			if f.Type == "decimal" {
				for _, lit := range []string{f.MinText, f.MaxText, strings.Trim(f.Default, `"`)} {
					if lit != "" && !decimalLit(lit) {
						return fmt.Errorf("line %d: %s.%s: %s is not a decimal number", f.Line, m.Name, f.Name, lit)
					}
				}
			}
			if (f.Email || f.URL || f.Pattern != "" || f.Timezone) && f.Type != "string" && f.Type != "text" {
				return fmt.Errorf("line %d: %s.%s: @email, @url, @pattern and @timezone need a string field", f.Line, m.Name, f.Name)
			}
			if f.Type == "localtime" && m.Persisted {
				return fmt.Errorf("line %d: %s.%s: localtime is for API types (what a form sends); a model stores the instant as time, from i18n.From(ctx).At(ctx, value)", f.Line, m.Name, f.Name)
			}
			if !m.Persisted && (f.ID || f.Unique || f.Index || f.Ref != "") {
				return fmt.Errorf("line %d: %s.%s: @id, @unique, @index and @ref apply to models only", f.Line, m.Name, f.Name)
			}
		}
		if m.Persisted && ids != 1 {
			return fmt.Errorf("line %d: model %s needs exactly one @id field", m.Line, m.Name)
		}
		for _, ix := range m.Indexes {
			for _, name := range ix.Fields {
				if !seen[name] {
					return fmt.Errorf("line %d: model %s: index on unknown field %s", m.Line, m.Name, name)
				}
			}
		}
	}
	return nil
}

// decimalLit reports whether s is a plain decimal literal: 12, -0.5.
func decimalLit(s string) bool {
	s = strings.TrimPrefix(s, "-")
	whole, frac, point := strings.Cut(s, ".")
	digits := func(t string) bool {
		for _, r := range t {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return whole != "" && digits(whole) && digits(frac) && (!point || frac != "")
}

// sameKey reports whether a foreign key of type field can reference an
// id of type id: the same type, or string and text (both text columns).
func sameKey(field, id string) bool {
	text := func(t string) bool { return t == "string" || t == "text" }
	return field == id || (text(field) && text(id))
}

// refOrder returns the models with every referenced model before the ones
// that reference it (for CREATE TABLE, which needs the table a REFERENCES
// names), or with drop, after them (for DROP TABLE); otherwise in
// declaration order. A cycle keeps declaration order.
func refOrder(models []*Model, drop bool) []*Model {
	out := make([]*Model, 0, len(models))
	seen := map[*Model]bool{}
	// before lists the models that must come before m.
	before := func(m *Model) []*Model {
		var list []*Model
		for _, o := range models {
			if o == m {
				continue
			}
			if drop {
				for _, f := range o.Fields {
					if f.Ref == m.Name {
						list = append(list, o)
						break
					}
				}
				continue
			}
			for _, f := range m.Fields {
				if f.Ref == o.Name {
					list = append(list, o)
					break
				}
			}
		}
		return list
	}
	var visit func(m *Model)
	visit = func(m *Model) {
		if seen[m] {
			return
		}
		seen[m] = true
		for _, o := range before(m) {
			visit(o)
		}
		out = append(out, m)
	}
	for _, m := range models {
		visit(m)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// snake converts camelCase to snake_case: authorId -> author_id.
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

// sortedFields returns the map's keys sorted, for deterministic output
// where order is not semantic.
func sortedFields[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
