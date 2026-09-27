package schema

import (
	"sort"
	"strings"
	"unicode"
)

// Initialisms are the name parts Go spells in capitals: authorId is
// AuthorID, url is URL. Their plurals keep a lowercase s: artworkIds is
// ArtworkIDs, imageUrls is ImageURLs. The schema package and sqlc's
// db/queries/gen use the same names: `lidza gen` writes this list and the
// plurals into sqlc.yaml (SQLCNames).
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

// sqlcName is the name sqlc gives a column with Initialisms configured
// and no rename (its StructName): parts split on anything but letters
// and digits, initialisms in capitals, the rest title-cased.
func sqlcName(column string, initialisms map[string]bool) string {
	column = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, column)
	var b strings.Builder
	for _, p := range strings.Split(column, "_") {
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

// SQLCNames returns what sqlc.yaml needs so that db/queries/gen names
// columns as the schema package names fields: the initialisms, and a
// rename for each column whose name sqlc cannot spell from them alone
// (a plural: artwork_ids is ArtworkIDs).
func SQLCNames(s *Schema) (initialisms []string, rename map[string]string) {
	rename = map[string]string{}
	if s == nil {
		return Initialisms, rename
	}
	for _, m := range s.Models {
		for _, f := range m.Fields {
			col := snake(f.Name)
			if want := GoName(f.Name); sqlcName(col, initialismSet) != want {
				rename[col] = want
			}
		}
	}
	return Initialisms, rename
}

// Rename is a Go name that changed with a Līdza release.
type Rename struct {
	// Where is "schema.Artwork" or "queries (column artwork.artwork_ids)".
	Where    string
	Old, New string
}

func (r Rename) String() string { return r.Where + ": " + r.Old + " is now " + r.New }

// QueryRenames lists the db/queries/gen field names that change when
// sqlc.yaml first gets the names SQLCNames gives: before, sqlc knew only
// the initialism id.
func QueryRenames(s *Schema) []Rename {
	var out []Rename
	if s == nil {
		return out
	}
	seen := map[string]bool{}
	for _, m := range s.Models {
		for _, f := range m.Fields {
			col := snake(f.Name)
			old, cur := sqlcName(col, map[string]bool{"id": true}), GoName(f.Name)
			if old == cur || seen[col] {
				continue
			}
			seen[col] = true
			out = append(out, Rename{Where: "queries (column " + m.Table + "." + col + ")", Old: old, New: cur})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Old < out[j].Old })
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
