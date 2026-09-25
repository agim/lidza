package schema

import (
	"fmt"
	"os"
	"path/filepath"
)

// Result says what Generate wrote.
type Result struct {
	// Files are the paths written, relative to the project root.
	Files []string
	// Migration is the new migration's name, empty when nothing changed.
	Migration string
}

// Generate runs every generator for the project in root: the Go package,
// the SQL schema, a migration when the models changed since the lock, the
// Rust module when the project has a crate at cargoDir (empty to skip), and
// the TypeScript types at tsFile (relative to root, empty to skip).
func Generate(root string, s *Schema, cargoDir, tsFile string) (*Result, error) {
	res := &Result{}
	write := func(rel, content string) error {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if old, err := os.ReadFile(p); err == nil && string(old) == content {
			return nil
		}
		res.Files = append(res.Files, rel)
		return os.WriteFile(p, []byte(content), 0o644)
	}

	if err := write(GoFile, GenerateGo(s)); err != nil {
		return nil, err
	}
	if len(s.Models) > 0 || len(s.Enums) > 0 {
		if err := write(SQLFile, GenerateSQL(s)); err != nil {
			return nil, err
		}
		prev, err := LoadLock(root)
		if err != nil {
			return nil, err
		}
		if m := Diff(prev, s, NextSeq(filepath.Join(root, MigrationsDir))); m != nil {
			up, down := m.Files()
			if err := write(filepath.Join(MigrationsDir, m.Name+".up.sql"), up); err != nil {
				return nil, err
			}
			if err := write(filepath.Join(MigrationsDir, m.Name+".down.sql"), down); err != nil {
				return nil, err
			}
			if err := SaveLock(root, s); err != nil {
				return nil, err
			}
			res.Files = append(res.Files, LockFile)
			res.Migration = m.Name
		}
	}
	if cargoDir != "" {
		if err := write(filepath.Join(cargoDir, RustFile), GenerateRust(s)); err != nil {
			return nil, err
		}
	}
	if tsFile != "" {
		if err := write(tsFile, GenerateTS(s)); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// Migrations lists the migration names in root, in order.
func Migrations(root string) []string {
	return sortedMigrations(filepath.Join(root, MigrationsDir))
}

// Describe returns a one-line summary for logs.
func (r *Result) Describe() string {
	if len(r.Files) == 0 {
		return "schema: up to date"
	}
	s := fmt.Sprintf("schema: wrote %d file(s)", len(r.Files))
	if r.Migration != "" {
		s += ", migration " + r.Migration
	}
	return s
}
