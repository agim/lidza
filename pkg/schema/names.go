package schema

import (
	"sort"
	"strings"
	"unicode"
)

// Initialisms are the name parts Go spells in capitals: authorId is
// AuthorID, url is URL. Their plurals keep a lowercase s: artworkIds is
// ArtworkIDs, imageUrls is ImageURLs. The schema package and sqlc's
// db/queries/gen follow the same rule: `lidza gen` writes this list and
// the plurals into sqlc.yaml (SQLCNames). Model and enum names are the
// ones schema.lidza gives; sqlc derives its struct and enum names from
// the table and the Postgres type (SQLCName).
var Initialisms = []string{"id", "url", "api", "http", "json", "uuid", "sql", "ip", "html"}

var initialismSet = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range Initialisms {
		m[s] = true
	}
	return m
}()

// GoName is the exported Go name of a schema name, camelCase or
// snake_case: authorId -> AuthorID, image_urls -> ImageURLs.
func GoName(name string) string { return joinParts(snake(name), true) }

func exported(s string) string { return GoName(s) }

// joinParts title-cases the parts of a snake_case name, initialisms in
// capitals and, with plurals, their plurals as IDs and URLs.
func joinParts(s string, plurals bool) string {
	var b strings.Builder
	for _, p := range strings.Split(s, "_") {
		if p == "" {
			continue
		}
		switch {
		case initialismSet[p]:
			b.WriteString(strings.ToUpper(p))
		case plurals && pluralInitialism(p):
			b.WriteString(strings.ToUpper(p[:len(p)-1]) + "s")
		default:
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

// pluralInitialism reports whether p is an initialism with an s: ids,
// urls. https is the scheme, not a plural.
func pluralInitialism(p string) bool {
	return len(p) > 1 && p[len(p)-1] == 's' && initialismSet[p[:len(p)-1]] && p != "https"
}

// SQLCName is the Go name sqlc gives a Postgres name (a column, a table
// in the singular, an enum type, or an enum type and value joined by _)
// once sqlc.yaml carries SQLCNames: api_key is APIKey, artwork_ids is
// ArtworkIDs, status_draft is StatusDraft.
func SQLCName(name string) string { return joinParts(sqlcParts(name), true) }

// sqlcParts replaces what is not a letter or digit with _, as sqlc does.
func sqlcParts(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, name)
}

// sqlcName is the name sqlc gives a Postgres name with these initialisms
// and no rename (its StructName): parts split on anything but letters
// and digits, initialisms in capitals, the rest title-cased.
func sqlcName(name string, initialisms map[string]bool) string {
	var b strings.Builder
	for _, p := range strings.Split(sqlcParts(name), "_") {
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

// SQLCNames returns what sqlc.yaml needs so that db/queries/gen follows
// the naming of the schema package: the initialisms, and a rename for
// each name sqlc cannot spell from them alone (a plural: artwork_ids is
// ArtworkIDs). A column's name is then its field's name in schema/.
func SQLCNames(s *Schema) (initialisms []string, rename map[string]string) {
	rename = map[string]string{}
	for _, k := range sqlcKeys(s) {
		if want := SQLCName(k.name); sqlcName(k.name, initialismSet) != want {
			rename[k.name] = want
		}
	}
	return Initialisms, rename
}

// SQLCRenames returns, for snake_case names sqlc turns into Go names
// outside the schema (a query's sqlc.arg('ids') or @ids parameter), the
// ones its initialisms alone spell differently from SQLCName.
func SQLCRenames(names []string) map[string]string {
	out := map[string]string{}
	for _, n := range names {
		if want := SQLCName(n); sqlcName(n, initialismSet) != want {
			out[n] = want
		}
	}
	return out
}

// sqlcKey is a Postgres name sqlc turns into a Go name, and where it is.
type sqlcKey struct{ name, where string }

// sqlcKeys lists the names of s that sqlc turns into Go names: columns,
// tables (sqlc makes them singular; a trailing s is dropped here), enum
// types and enum values.
func sqlcKeys(s *Schema) []sqlcKey {
	var out []sqlcKey
	if s == nil {
		return out
	}
	for _, e := range s.Enums {
		name := snake(e.Name)
		out = append(out, sqlcKey{name, "enum type " + name})
		for _, v := range e.Values {
			out = append(out, sqlcKey{name + "_" + v, "enum value " + name + "." + v})
		}
	}
	for _, m := range s.Models {
		singular := m.Table
		if strings.HasSuffix(singular, "s") && !strings.HasSuffix(singular, "ss") {
			singular = strings.TrimSuffix(singular, "s")
		}
		out = append(out, sqlcKey{singular, "table " + m.Table})
		for _, f := range m.Fields {
			out = append(out, sqlcKey{snake(f.Name), "column " + m.Table + "." + snake(f.Name)})
		}
	}
	return out
}

// Rename is a Go name that changed with a Līdza release.
type Rename struct {
	// Where is "schema.Artwork" or "queries (column artwork.artwork_ids)".
	Where    string
	Old, New string
}

func (r Rename) String() string { return r.Where + ": " + r.Old + " is now " + r.New }

// QueryRenames lists the db/queries/gen names that change when sqlc.yaml
// first gets the names SQLCNames gives: before, sqlc knew only the
// initialism id. Fields of the query rows and parameters change with the
// columns they come from.
func QueryRenames(s *Schema) []Rename {
	var out []Rename
	seen := map[string]bool{}
	for _, k := range sqlcKeys(s) {
		old, cur := sqlcName(k.name, map[string]bool{"id": true}), SQLCName(k.name)
		if old == cur || seen[old] {
			continue
		}
		seen[old] = true
		out = append(out, Rename{Where: "queries (" + k.where + ")", Old: old, New: cur})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Old < out[j].Old })
	return out
}

// schemaRenames lists the names in the schema package that differ from
// the ones before plurals of initialisms were spelled IDs and URLs, and
// that the previous schema/schema.go (old) still has.
func schemaRenames(s *Schema, old string) []Rename {
	var out []Rename
	if old == "" {
		return out
	}
	for _, e := range s.Enums {
		for _, v := range e.Values {
			was, cur := e.Name+joinParts(snake(v), false), e.Name+GoName(v)
			if was != cur && strings.Contains(old, "\t"+was+" "+e.Name+" = ") {
				out = append(out, Rename{Where: "schema", Old: was, New: cur})
			}
		}
	}
	for _, m := range append(append([]*Model{}, s.Models...), s.Types...) {
		for _, f := range m.Fields {
			was, cur := joinParts(snake(f.Name), false), GoName(f.Name)
			if was != cur && strings.Contains(old, "\t"+was+" ") && strings.Contains(old, "`json:\""+f.Name+"\"") {
				out = append(out, Rename{Where: "schema." + m.Name, Old: was, New: cur})
			}
		}
	}
	return out
}
