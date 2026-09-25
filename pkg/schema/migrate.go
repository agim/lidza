package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

	for _, t := range cur.Models {
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
			if od, nd := defaultOf(prev, of), defaultOf(cur, f); od != nd {
				m.Up = append(m.Up, alterDefault(table, col, nd))
				m.Down = append([]string{alterDefault(table, col, od)}, m.Down...)
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
		if changed {
			desc = append(desc, "alter_"+t.Table)
		}
	}
	for _, old := range prev.Models {
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

func defaultOf(s *Schema, f *Field) string {
	if f.Default == "" {
		return ""
	}
	return sqlDefault(s, f)
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

// NextSeq returns the next migration number from the files in dir.
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
