package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/agim/lidza/pkg/schema"
)

// The lines of sqlc.yaml that lidza gen keeps sit between these comments.
const (
	sqlcNamesStart = "# lidza gen: Go names as in schema/ (from schema.lidza; edit that, not these lines)"
	sqlcNamesEnd   = "# lidza gen: end of Go names"
)

// SQLCSync says what SyncSQLCNames did.
type SQLCSync struct {
	// Changed is true when sqlc.yaml was written.
	Changed bool
	// Added is true when the file had no Go names before: made by a
	// release that left sqlc on its default, so the names of
	// db/queries/gen change (Url becomes URL).
	Added bool
	// Note explains why the file was left alone, "" otherwise.
	Note string
}

// SyncSQLCNames writes the schema's initialisms and the renames sqlc
// needs for plurals (schema.SQLCNames) into the go options of root's
// sqlc.yaml, so db/queries/gen names a column as schema/ names the field:
// URL, ProviderID, TagIDs. A file without sqlc.yaml is left alone,
// and so are the names of one that sets initialisms or rename itself.
// While a model has a decimal field, the go options' overrides also map
// numeric to decimal.Decimal, the type schema/ uses (syncSQLCTypes).
func SyncSQLCNames(root string, s *schema.Schema) (SQLCSync, error) {
	var res SQLCSync
	p := filepath.Join(root, SQLCFile)
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	initialisms, rename := schema.SQLCNames(s)
	// Query parameters are named in the queries, not the schema:
	// sqlc.narg('ids') is spelled Ids unless renamed too.
	for k, v := range schema.SQLCRenames(queryParams(root)) {
		if _, ok := rename[k]; !ok {
			rename[k] = v
		}
	}
	block := func(indent string) []string {
		quoted := make([]string, len(initialisms))
		for i, v := range initialisms {
			quoted[i] = fmt.Sprintf("%q", v)
		}
		out := []string{indent + sqlcNamesStart, indent + "initialisms: [" + strings.Join(quoted, ", ") + "]"}
		if len(rename) > 0 {
			out = append(out, indent+"rename:")
			cols := make([]string, 0, len(rename))
			for c := range rename {
				cols = append(cols, c)
			}
			sort.Strings(cols)
			for _, c := range cols {
				out = append(out, fmt.Sprintf("%s  %s: %q", indent, c, rename[c]))
			}
		}
		return append(out, indent+sqlcNamesEnd)
	}

	text := string(data)
	lines := strings.Split(text, "\n")
	start, end := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case sqlcNamesStart:
			start = i
		case sqlcNamesEnd:
			if start >= 0 && end < 0 {
				end = i
			}
		}
	}
	out := lines
	switch {
	case start >= 0 && end > start:
		indent := lines[start][:len(lines[start])-len(strings.TrimLeft(lines[start], " "))]
		out = concat(lines[:start], block(indent), lines[end+1:])
	default:
		at := -1
		for i, l := range lines {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "initialisms:") || strings.HasPrefix(t, "rename:") {
				want := block("")
				res.Note = SQLCFile + " sets initialisms or rename itself, so lidza gen leaves it alone; for db/queries/gen to use the names of schema/, its go options need: " + strings.Join(want[1:len(want)-1], " ")
				at = -2
				break
			}
			if at == -1 && t == "go:" {
				at = i
			}
		}
		if at == -1 {
			res.Note = SQLCFile + " has no go: options, so lidza gen cannot give db/queries/gen the names of schema/"
		}
		if at < 0 {
			break
		}
		own := lines[at][:len(lines[at])-len(strings.TrimLeft(lines[at], " "))]
		indent := own + "  "
		for _, l := range lines[at+1:] {
			if strings.TrimSpace(l) == "" {
				continue
			}
			if child := l[:len(l)-len(strings.TrimLeft(l, " "))]; len(child) > len(own) {
				indent = child
			}
			break
		}
		out = concat(lines[:at+1], block(indent), lines[at+1:])
		res.Added = true
	}
	out = syncSQLCTypes(out, usesDecimal(s))
	updated := strings.Join(out, "\n")
	if updated == text {
		return res, nil
	}
	res.Changed = true
	return res, os.WriteFile(p, []byte(updated), 0o644)
}

// The overrides of sqlc.yaml that lidza gen keeps sit between these
// comments: numeric columns as decimal.Decimal, the type schema/ gives a
// decimal(p, s) field, written while schema.lidza has one.
const (
	sqlcTypesStart = "# lidza gen: types as in schema/ (from schema.lidza; edit that, not these lines)"
	sqlcTypesEnd   = "# lidza gen: end of types"
)

// sqlcDecimal is the override list; the indent of each line is added.
var sqlcDecimal = []string{
	`- db_type: "pg_catalog.numeric"`,
	`  go_type: "github.com/agim/lidza/pkg/decimal.Decimal"`,
	`- db_type: "pg_catalog.numeric"`,
	`  nullable: true`,
	`  go_type:`,
	`    import: "github.com/agim/lidza/pkg/decimal"`,
	`    type: "Decimal"`,
	`    pointer: true`,
}

// usesDecimal reports whether a model has a decimal field.
func usesDecimal(s *schema.Schema) bool {
	if s == nil {
		return false
	}
	for _, m := range s.Models {
		for _, f := range m.Fields {
			if f.Type == "decimal" {
				return true
			}
		}
	}
	return false
}

// syncSQLCTypes puts the managed overrides into the go options' override
// list (adding the list when there is none) when want, and takes them out
// otherwise. A file without go: options is left as is.
func syncSQLCTypes(lines []string, want bool) []string {
	start, end := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case sqlcTypesStart:
			start = i
		case sqlcTypesEnd:
			if start >= 0 && end < 0 {
				end = i
			}
		}
	}
	block := func(indent string) []string {
		out := []string{indent + sqlcTypesStart}
		for _, l := range sqlcDecimal {
			out = append(out, indent+l)
		}
		return append(out, indent+sqlcTypesEnd)
	}
	indentOf := func(l string) string { return l[:len(l)-len(strings.TrimLeft(l, " "))] }
	if start >= 0 && end > start {
		if !want {
			return concat(lines[:start], lines[end+1:])
		}
		return concat(lines[:start], block(indentOf(lines[start])), lines[end+1:])
	}
	if !want {
		return lines
	}
	goAt := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "go:" {
			goAt = i
			break
		}
	}
	if goAt < 0 {
		return lines
	}
	own := indentOf(lines[goAt])
	// The go options run while lines are indented deeper than go:.
	optIndent := ""
	for i := goAt + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		ind := indentOf(l)
		if len(ind) <= len(own) {
			break
		}
		if optIndent == "" {
			optIndent = ind
		}
		if ind == optIndent && strings.TrimSpace(l) == "overrides:" {
			item := optIndent + "  "
			for _, n := range lines[i+1:] {
				if strings.TrimSpace(n) != "" {
					if strings.HasPrefix(strings.TrimSpace(n), "-") && len(indentOf(n)) >= len(optIndent) {
						item = indentOf(n)
					}
					break
				}
			}
			return concat(lines[:i+1], block(item), lines[i+1:])
		}
	}
	if optIndent == "" {
		optIndent = own + "  "
	}
	return concat(lines[:goAt+1], []string{optIndent + "overrides:"}, block(optIndent+"  "), lines[goAt+1:])
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// paramRe matches a named query parameter: sqlc.arg('x'), sqlc.narg(x),
// sqlc.slice("x") or @x.
var paramRe = regexp.MustCompile(`sqlc\.(?:arg|narg|slice)\(\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?\s*\)|(?:^|[^@:\w])@([A-Za-z_][A-Za-z0-9_]*)`)

// queryParams lists the named parameters of root's queries (db/queries,
// or the queries path sqlc.yaml names).
func queryParams(root string) []string {
	dirs := []string{filepath.Join(root, "db", "queries")}
	if data, err := os.ReadFile(filepath.Join(root, SQLCFile)); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "queries" {
				if v = strings.Trim(strings.TrimSpace(v), `"'`); v != "" {
					dirs = []string{filepath.Join(root, v)}
				}
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.sql"))
		if st, err := os.Stat(d); err == nil && !st.IsDir() {
			files = []string{d}
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, m := range paramRe.FindAllStringSubmatch(string(data), -1) {
				name := strings.ToLower(m[1] + m[2])
				if name != "" && !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
