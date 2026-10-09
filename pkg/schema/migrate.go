package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Migration files, relative to the project root. The lock file is the
// schema as of the last generated migration and is committed; the next
// `lidza gen` diffs the current schema against it.
const (
	MigrationsDir = "db/migrations"
	LockFile      = "db/schema.lock.json"
)

// Migration is one generated pair of up and down scripts.
type Migration struct {
	// Name is "0003_add_post"; the files are Name+".up.sql" and ".down.sql".
	Name string
	Up   []string
	Down []string
}

// Diff computes the migration from prev (nil for a fresh project) to cur.
// It returns nil when nothing changed. Handled: enums created, dropped or
// given new values; tables created or dropped; columns added, dropped, or
// changed in type, nullability or default; indexes created or dropped.
// A dropped or narrowed column loses data; the statement is generated and
// marked with a comment so it is reviewed before it runs.
func Diff(prev, cur *Schema, seq int) *Migration {
	if prev == nil {
		prev = &Schema{}
	}
	m := &Migration{}
	var desc []string

	prevEnums := map[string]*Enum{}
	for _, e := range prev.Enums {
		prevEnums[e.Name] = e
	}
	for _, e := range cur.Enums {
		old, ok := prevEnums[e.Name]
		if !ok {
			m.Up = append(m.Up, createEnum(e))
			m.Down = append([]string{fmt.Sprintf("DROP TYPE %s;", snake(e.Name))}, m.Down...)
			desc = append(desc, "create_"+snake(e.Name))
			continue
		}
		for _, v := range e.Values {
			if !contains(old.Values, v) {
				m.Up = append(m.Up, fmt.Sprintf("ALTER TYPE %s ADD VALUE %s;", snake(e.Name), quoteLit(v)))
				m.Down = append([]string{fmt.Sprintf("-- Postgres cannot remove enum value %s from %s; recreate the type by hand if needed.", quoteLit(v), snake(e.Name))}, m.Down...)
				desc = append(desc, "extend_"+snake(e.Name))
			}
		}
		for _, v := range old.Values {
			if !contains(e.Values, v) {
				m.Up = append(m.Up, fmt.Sprintf("-- enum value %s was removed from %s in %s; Postgres cannot drop it. Recreate the type by hand.", quoteLit(v), snake(e.Name), FileName))
			}
		}
	}

	prevModels := map[string]*Model{}
	for _, t := range prev.Models {
		prevModels[t.Name] = t
	}
	curModels := map[string]*Model{}
	for _, t := range cur.Models {
		curModels[t.Name] = t
	}

	for _, t := range refOrder(cur.Models, false) {
		old, ok := prevModels[t.Name]
		if !ok {
			m.Up = append(m.Up, createTable(cur, t))
			m.Up = append(m.Up, createIndexes(t)...)
			m.Down = append([]string{fmt.Sprintf("DROP TABLE %s;", qid(t.Table))}, m.Down...)
			desc = append(desc, "create_"+t.Table)
			continue
		}
		if old.Table != t.Table {
			m.Up = append(m.Up, fmt.Sprintf("ALTER TABLE %s RENAME TO %s;", qid(old.Table), qid(t.Table)))
			m.Down = append([]string{fmt.Sprintf("ALTER TABLE %s RENAME TO %s;", qid(t.Table), qid(old.Table))}, m.Down...)
			desc = append(desc, "rename_"+old.Table)
		}
		changed := false
		oldFields := map[string]*Field{}
		for _, f := range old.Fields {
			oldFields[f.Name] = f
		}
		for _, f := range t.Fields {
			of, ok := oldFields[f.Name]
			if !ok {
				m.Up = append(m.Up, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", qid(t.Table), columnDef(cur, f)))
				m.Down = append([]string{fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", qid(t.Table), col(f))}, m.Down...)
				changed = true
				continue
			}
			col := col(f)
			table := qid(t.Table)
			if ot, nt := sqlType(prev, of), sqlType(cur, f); ot != nt {
				m.Up = append(m.Up, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s USING %s::%s; -- review: was %s", table, col, nt, col, nt, ot))
				m.Down = append([]string{fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s USING %s::%s;", table, col, ot, col, ot)}, m.Down...)
				changed = true
			}
			if of.Optional != f.Optional && !f.ID {
				if f.Optional {
					m.Up = append(m.Up, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;", table, col))
					m.Down = append([]string{fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;", table, col)}, m.Down...)
				} else {
					m.Up = append(m.Up, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL; -- review: fails on existing NULLs", table, col))
					m.Down = append([]string{fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;", table, col)}, m.Down...)
				}
				changed = true
			}
			if oref, nref := references(prev, of), references(cur, f); oref != nref {
				// The constraint carries Postgres' name for an inline REFERENCES.
				name := t.Table + "_" + col + "_fkey"
				var up, down []string
				if oref != "" {
					up = append(up, fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", table, name))
					down = append(down, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) %s;", table, name, col, oref))
				}
				if nref != "" {
					up = append(up, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) %s;", table, name, col, nref))
					down = append([]string{fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", table, name)}, down...)
				}
				m.Up = append(m.Up, up...)
				m.Down = append(down, m.Down...)
				changed = true
			}
			// An identity replaces the default: the old default goes first
			// on the way in, the identity first on the way out.
			addIdentity := []string{fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s ADD %s;", table, col, identity), restartIdentity(t.Table, f)}
			dropIdentity := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP IDENTITY;", table, col)
			if of.Autoincrement() && !f.Autoincrement() {
				m.Up = append(m.Up, dropIdentity)
				m.Down = append(addIdentity, m.Down...)
				changed = true
			}
			if od, nd := defaultOf(prev, of), defaultOf(cur, f); od != nd {
				m.Up = append(m.Up, alterDefault(table, col, nd))
				m.Down = append([]string{alterDefault(table, col, od)}, m.Down...)
				changed = true
			}
			if !of.Autoincrement() && f.Autoincrement() {
				m.Up = append(m.Up, addIdentity...)
				m.Down = append([]string{dropIdentity}, m.Down...)
				changed = true
			}
			if of.Unique != f.Unique && !f.ID {
				name := t.Table + "_" + snake(f.Name) + "_key"
				if f.Unique {
					m.Up = append(m.Up, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s);", table, name, col))
					m.Down = append([]string{fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", table, name)}, m.Down...)
				} else {
					m.Up = append(m.Up, fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", table, name))
					m.Down = append([]string{fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s);", table, name, col)}, m.Down...)
				}
				changed = true
			}
		}
		curFields := map[string]bool{}
		for _, f := range t.Fields {
			curFields[f.Name] = true
		}
		for _, of := range old.Fields {
			if !curFields[of.Name] {
				m.Up = append(m.Up, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s; -- review: data loss", qid(t.Table), col(of)))
				m.Down = append([]string{fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", qid(t.Table), columnDef(prev, of))}, m.Down...)
				changed = true
			}
		}
		oldIdx := indexSet(old)
		newIdx := indexSet(t)
		for _, name := range sortedFields(newIdx) {
			if _, ok := oldIdx[name]; !ok {
				m.Up = append(m.Up, newIdx[name])
				m.Down = append([]string{fmt.Sprintf("DROP INDEX %s;", name)}, m.Down...)
				changed = true
			}
		}
		for _, name := range sortedFields(oldIdx) {
			if _, ok := newIdx[name]; !ok {
				m.Up = append(m.Up, fmt.Sprintf("DROP INDEX %s;", name))
				m.Down = append([]string{oldIdx[name]}, m.Down...)
				changed = true
			}
		}
		// The same name with another definition (a field added to the
		// search): drop and create it again.
		for _, name := range sortedFields(newIdx) {
			if before, ok := oldIdx[name]; ok && before != newIdx[name] {
				m.Up = append(m.Up, fmt.Sprintf("DROP INDEX %s;", name), newIdx[name])
				m.Down = append([]string{fmt.Sprintf("DROP INDEX %s;", name), before}, m.Down...)
				changed = true
			}
		}
		if changed {
			desc = append(desc, "alter_"+t.Table)
		}
	}
	// Dropped tables go the other way: the ones that reference first.
	for _, old := range refOrder(prev.Models, true) {
		if _, ok := curModels[old.Name]; !ok {
			m.Up = append(m.Up, fmt.Sprintf("DROP TABLE %s; -- review: data loss", qid(old.Table)))
			m.Down = append([]string{createTable(prev, old)}, m.Down...)
			desc = append(desc, "drop_"+old.Table)
		}
	}
	for _, old := range prev.Enums {
		if cur.Enum(old.Name) == nil {
			m.Up = append(m.Up, fmt.Sprintf("DROP TYPE %s;", snake(old.Name)))
			m.Down = append([]string{createEnum(old)}, m.Down...)
			desc = append(desc, "drop_"+snake(old.Name))
		}
	}

	if len(m.Up) == 0 {
		return nil
	}
	name := "init"
	if prev != nil && (len(prev.Models) > 0 || len(prev.Enums) > 0) {
		name = strings.Join(desc, "_")
		if len(name) > 60 {
			name = name[:60]
		}
	}
	m.Name = fmt.Sprintf("%04d_%s", seq, name)
	return m
}

// defaultOf is the column's DEFAULT: "" without one, and for an
// identity, which is not a default.
func defaultOf(s *Schema, f *Field) string {
	if f.Default == "" || f.Autoincrement() {
		return ""
	}
	return sqlDefault(s, f)
}

// restartIdentity moves the identity of a column that already holds
// values past the largest, so the next insert does not collide.
func restartIdentity(table string, f *Field) string {
	return fmt.Sprintf("SELECT setval(pg_get_serial_sequence(%s, %s), coalesce(max(%s), 0) + 1, false) FROM %s;", quoteLit(qid(table)), quoteLit(snake(f.Name)), col(f), qid(table))
}

func alterDefault(table, col, def string) string {
	if def == "" {
		return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;", table, col)
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;", table, col, def)
}

// indexSet maps index name to its CREATE statement.
func indexSet(m *Model) map[string]string {
	out := map[string]string{}
	for _, stmt := range createIndexes(m) {
		name := strings.Fields(strings.TrimPrefix(strings.TrimPrefix(stmt, "CREATE UNIQUE INDEX "), "CREATE INDEX "))[0]
		out[name] = stmt
	}
	return out
}

// Files renders the migration's two scripts.
func (m *Migration) Files() (up, down string) {
	return "-- " + m.Name + " (generated by lidza gen from " + FileName + ")\n" + strings.Join(m.Up, "\n") + "\n",
		"-- " + m.Name + " (generated by lidza gen from " + FileName + ")\n" + strings.Join(m.Down, "\n") + "\n"
}

// Clock is the time new migrations are named by; tests set it.
var Clock = time.Now

// StampLayout names a migration by the UTC time it was written:
// 20261006140512_create_post. Two branches that each add one no longer
// pick the same number, and the existing numbered migrations (0001_...)
// sort before every timestamped one.
const StampLayout = "20060102150405"

// NextStamp is the name prefix of a new migration in dir: now in UTC,
// moved past the newest migration there (a clock behind another
// machine's must not run a migration before one it builds on) and past
// any name taken.
func NextStamp(dir string, now time.Time) string {
	t := now.UTC().Truncate(time.Second)
	entries, _ := os.ReadDir(dir)
	taken := map[string]bool{}
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok || len(prefix) != len(StampLayout) {
			continue
		}
		taken[prefix] = true
		if last, err := time.Parse(StampLayout, prefix); err == nil && !t.After(last) {
			t = last.Add(time.Second)
		}
	}
	for taken[t.Format(StampLayout)] {
		t = t.Add(time.Second)
	}
	return t.Format(StampLayout)
}

// NextSeq returns the next migration number from the files in dir: the
// numbering before migrations were named by time.
func NextSeq(dir string) int {
	entries, _ := os.ReadDir(dir)
	max := 0
	for _, e := range entries {
		var n int
		if _, err := fmt.Sscanf(e.Name(), "%04d_", &n); err == nil && n > max {
			max = n
		}
	}
	return max + 1
}

// LoadLock reads the lock file; nil when there is none.
func LoadLock(root string) (*Schema, error) {
	data, err := os.ReadFile(filepath.Join(root, LockFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", LockFile, err)
	}
	return &s, nil
}

// SaveLock writes the schema as the lock file. Types are omitted: they have
// no tables.
func SaveLock(root string, s *Schema) error {
	locked := Schema{Enums: s.Enums, Models: s.Models}
	data, err := json.MarshalIndent(locked, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(LockFile)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, LockFile), append(data, '\n'), 0o644)
}

// sortedMigrations lists the migration names in dir in order.
func sortedMigrations(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			out = append(out, strings.TrimSuffix(e.Name(), ".up.sql"))
		}
	}
	sort.Strings(out)
	return out
}

// MergeLocks merges two branches' lock files with their common ancestor,
// model by model and enum by enum: an entry one branch changed (or added,
// or removed) and the other left as it was takes the change; the same
// change on both is taken once. Line numbers are ignored: an edit higher
// up in schema.lidza moves them in every entry below it. Conflicts name
// the entries both branches changed differently; the result then keeps
// ours for them, and the developer merges schema.lidza and runs lidza gen.
func MergeLocks(base, ours, theirs []byte) (merged []byte, conflicts []string, err error) {
	var o, a, b Schema
	for _, x := range []struct {
		data []byte
		into *Schema
	}{{base, &o}, {ours, &a}, {theirs, &b}} {
		if len(x.data) == 0 {
			continue
		}
		if err := json.Unmarshal(x.data, x.into); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", LockFile, err)
		}
	}
	key := func(v any) string {
		data, _ := json.Marshal(v)
		return lineless.ReplaceAllString(string(data), "")
	}
	type entry struct {
		name string
		val  any
	}
	merge := func(kind string, base, ours, theirs []entry) []entry {
		find := func(list []entry, name string) (any, bool) {
			for _, e := range list {
				if e.name == name {
					return e.val, true
				}
			}
			return nil, false
		}
		var out []entry
		take := func(name string) {
			ov, inO := find(base, name)
			av, inA := find(ours, name)
			bv, inB := find(theirs, name)
			same := func(x, y any, inX, inY bool) bool { return inX == inY && (!inX || key(x) == key(y)) }
			switch {
			case same(av, bv, inA, inB):
				if inA {
					out = append(out, entry{name, av})
				}
			case same(av, ov, inA, inO): // only theirs changed it
				if inB {
					out = append(out, entry{name, bv})
				}
			case same(bv, ov, inB, inO): // only ours changed it
				if inA {
					out = append(out, entry{name, av})
				}
			default:
				conflicts = append(conflicts, kind+" "+name)
				if inA {
					out = append(out, entry{name, av})
				}
			}
		}
		seen := map[string]bool{}
		for _, list := range [][]entry{ours, theirs, base} {
			for _, e := range list {
				if !seen[e.name] {
					seen[e.name] = true
					take(e.name)
				}
			}
		}
		return out
	}
	models := func(s Schema) []entry {
		out := make([]entry, len(s.Models))
		for i, m := range s.Models {
			out[i] = entry{m.Name, m}
		}
		return out
	}
	enums := func(s Schema) []entry {
		out := make([]entry, len(s.Enums))
		for i, e := range s.Enums {
			out[i] = entry{e.Name, e}
		}
		return out
	}
	var result Schema
	for _, e := range merge("enum", enums(o), enums(a), enums(b)) {
		result.Enums = append(result.Enums, e.val.(*Enum))
	}
	for _, e := range merge("model", models(o), models(a), models(b)) {
		result.Models = append(result.Models, e.val.(*Model))
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(data, '\n'), conflicts, nil
}

// lineless drops the "Line" fields from a lock entry's JSON.
var lineless = regexp.MustCompile(`"Line":\d+,?`)
