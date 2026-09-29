package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agim/lidza/pkg/schema"
)

// ownedQueries is rule L018: a query in db/queries/*.sql on an owned
// table (a model with an owner field, schema.Schema.Owner) that neither
// filters by the owner column in its WHERE clause nor sets it in an
// INSERT column list. Such a query reads or changes every user's rows.
// A comment "lidza:ignore L018" on the query's "-- name:" line, or the
// line before it, exempts a query meant to cross users (an admin page).
func ownedQueries(root string) []Diagnostic {
	s, err := schema.Load(root)
	if err != nil || s == nil {
		return nil
	}
	owners := map[string]string{} // table: owner column
	for _, m := range s.Models {
		if f := s.Owner(m); f != nil {
			owners[m.Table] = f.Column()
		}
	}
	if len(owners) == 0 {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(root, "db", "queries", "*.sql"))
	var out []Diagnostic
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, root), string(filepath.Separator)))
		out = append(out, checkOwnedSQL(rel, string(data), owners)...)
	}
	return out
}

var (
	queryName = regexp.MustCompile(`^\s*--\s*name:\s*(\w+)`)
	// sqlTable finds the tables a statement names after FROM, JOIN,
	// UPDATE or INTO, without a schema qualifier or quotes.
	sqlTable = regexp.MustCompile(`\b(?:from|join|update|into)\s+(?:only\s+)?(?:"?\w+"?\.)?"?(\w+)"?`)
)

// sqlQuery is one named query of a sqlc file.
type sqlQuery struct {
	name string
	line int // of the "-- name:" comment, 1-based
	body string
}

// checkOwnedSQL applies L018 to one queries file.
func checkOwnedSQL(rel, content string, owners map[string]string) []Diagnostic {
	lines := strings.Split(content, "\n")
	var queries []sqlQuery
	for i, l := range lines {
		if m := queryName.FindStringSubmatch(l); m != nil {
			if strings.Contains(l, "lidza:ignore L018") || (i > 0 && strings.Contains(lines[i-1], "lidza:ignore L018")) {
				queries = append(queries, sqlQuery{name: m[1], line: -1})
				continue
			}
			queries = append(queries, sqlQuery{name: m[1], line: i + 1})
			continue
		}
		if len(queries) == 0 {
			continue
		}
		if k := strings.Index(l, "--"); k >= 0 {
			l = l[:k]
		}
		q := &queries[len(queries)-1]
		q.body += " " + l
	}
	var out []Diagnostic
	for _, q := range queries {
		if q.line < 0 {
			continue
		}
		body := strings.ToLower(strings.Join(strings.Fields(q.body), " "))
		seen := map[string]bool{}
		for _, m := range sqlTable.FindAllStringSubmatch(body, -1) {
			table := m[1]
			column, owned := owners[table]
			if !owned || seen[table] {
				continue
			}
			seen[table] = true
			if scoped(body, table, column) {
				continue
			}
			out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "warning", Code: "L018", File: rel, Line: q.line, Column: 1,
				Message: fmt.Sprintf("query %s on %s neither filters by nor sets %s, the owner: it reaches every user's rows. Add %s = <the signed-in user's id> to its WHERE (auth.CurrentUser(ctx).ID; another user's row is then a 404), or, for a query meant to cross users (an admin page), a comment \"-- lidza:ignore L018\" on the line before", q.name, table, column, column)})
		}
	}
	return out
}

// scoped reports whether a normalised statement filters by the owner
// column after its WHERE or sets it in the INSERT column list of table.
func scoped(body, table, column string) bool {
	word := regexp.MustCompile(`(^|[^\w])"?` + regexp.QuoteMeta(column) + `"?([^\w]|$)`)
	if m := regexp.MustCompile(`\binsert into (?:"?\w+"?\.)?"?` + regexp.QuoteMeta(table) + `"?\s*\(([^)]*)\)`).FindStringSubmatch(body); m != nil && word.MatchString(m[1]) {
		return true
	}
	if i := strings.Index(body, " where "); i >= 0 && word.MatchString(body[i:]) {
		return true
	}
	return false
}
