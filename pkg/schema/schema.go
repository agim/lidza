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
//	  tags      string[]
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
// text, int, bigint, float, bool, time, date, uuid, json, bytes. Field
// attributes: @id, @unique, @index, @default(v), @ref(Model), @min(n),
// @max(n), @email, @url, @pattern("re"). Block attributes: @@index(a, b),
// @@unique(a, b); model attribute @table("name").
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
	// Default is the literal as written: 42, true, "x", draft, uuid(), now().
	Default string
	// Ref is the model a uuid field references.
	Ref     string
	Min     *float64
	Max     *float64
	Email   bool
	URL     bool
	Pattern string
	Line    int
}

// Index is a block-level @@index or @@unique.
type Index struct {
	Fields []string
	Unique bool
}

// Scalars are the built-in types.
var Scalars = map[string]bool{
	"string": true, "text": true, "int": true, "bigint": true, "float": true,
	"bool": true, "time": true, "date": true, "uuid": true, "json": true, "bytes": true,
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
					return fmt.Errorf("line %d: %s.%s: a model field cannot have type %s; use uuid @ref(%s)", f.Line, m.Name, f.Name, f.Type, f.Type)
				}
			default:
				return fmt.Errorf("line %d: %s.%s: unknown type %s", f.Line, m.Name, f.Name, f.Type)
			}
			if f.Ref != "" {
				target := s.Model(f.Ref)
				if target == nil || !target.Persisted {
					return fmt.Errorf("line %d: %s.%s: @ref(%s) is not a model", f.Line, m.Name, f.Name, f.Ref)
				}
				if f.Type != "uuid" {
					return fmt.Errorf("line %d: %s.%s: @ref needs type uuid", f.Line, m.Name, f.Name)
				}
			}
			if f.Default != "" {
				if e := s.Enum(f.Type); e != nil && !contains(e.Values, f.Default) {
					return fmt.Errorf("line %d: %s.%s: default %s is not a value of %s", f.Line, m.Name, f.Name, f.Default, f.Type)
				}
			}
			if (f.Email || f.URL || f.Pattern != "") && f.Type != "string" && f.Type != "text" {
				return fmt.Errorf("line %d: %s.%s: @email, @url and @pattern need a string field", f.Line, m.Name, f.Name)
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

var initialisms = map[string]bool{"id": true, "url": true, "api": true, "http": true, "json": true, "uuid": true, "sql": true, "ip": true, "html": true}

// exported converts a field name to an exported Go identifier with
// initialisms upper-cased: authorId -> AuthorID, url -> URL.
func exported(s string) string {
	parts := strings.Split(snake(s), "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if initialisms[p] {
			b.WriteString(strings.ToUpper(p))
		} else {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
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
