package diag

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/agim/lidza/pkg/schema"
)

// migrationSafety is L021: a statement in a new migration that, applied
// while the app serves traffic, holds a table's lock for a scan or a
// rewrite, or breaks the version still running until the deploy ends.
// lidza gen writes the safe forms itself (indexes CONCURRENTLY,
// constraints NOT VALID then validated); what it cannot make safe (a
// rename, a type change, a drop) and hand-written SQL are reported.
//
// Only migrations git does not have yet (new or changed) are read: one
// that is committed has been reviewed, or deployed. Outside a git
// repository every migration is. "-- lidza:ignore L021" on the line or
// the one before accepts a statement (a table known to be small, a
// maintenance window).
func migrationSafety(root string) []Diagnostic {
	dir := filepath.Join(root, schema.MigrationsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	pending := uncommitted(root, schema.MigrationsDir)
	var out []Diagnostic
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		rel := schema.MigrationsDir + "/" + name
		if pending != nil && !pending[rel] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		out = append(out, unsafeStatements(rel, string(data))...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// uncommitted lists the files under rel that git reports new or
// changed; nil when root is not in a git repository.
func uncommitted(root, rel string) map[string]bool {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "--", rel)
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return nil
	}
	prefix := ""
	if top, err := exec.Command("git", "-C", root, "rev-parse", "--show-prefix").Output(); err == nil {
		prefix = strings.TrimSpace(string(top))
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.Trim(line[3:], `"`)
		if _, after, ok := strings.Cut(p, " -> "); ok {
			p = after
		}
		out[strings.TrimPrefix(p, prefix)] = true
	}
	return out
}

var (
	reCreated   = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?(\w+)"?`)
	reAlter     = regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?"?(\w+)"?`)
	reIndexOn   = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+(CONCURRENTLY\s+)?.*?\bON\s+(?:ONLY\s+)?"?(\w+)"?`)
	reRename    = regexp.MustCompile(`(?i)\bRENAME\s+(?:COLUMN\s+\S+\s+)?TO\b`)
	reType      = regexp.MustCompile(`(?i)\bALTER\s+COLUMN\s+\S+\s+(?:SET\s+DATA\s+)?TYPE\b`)
	reDropCol   = regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b`)
	reDropTable = regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)
	reNotNull   = regexp.MustCompile(`(?i)\bSET\s+NOT\s+NULL\b`)
	reCheckedFK = regexp.MustCompile(`(?i)\bADD\s+(?:CONSTRAINT\s+\S+\s+)?(?:FOREIGN\s+KEY|CHECK)\b`)
	reAddUnique = regexp.MustCompile(`(?i)\bADD\s+(?:CONSTRAINT\s+\S+\s+)?(?:UNIQUE|PRIMARY\s+KEY)\s*\(`)
	reVolatile  = regexp.MustCompile(`(?i)\bADD\s+COLUMN\b.*\bDEFAULT\s+(?:gen_random_uuid|uuid_generate_v4|random|clock_timestamp)\s*\(`)
)

// unsafeStatements reads one migration script.
func unsafeStatements(rel, sql string) []Diagnostic {
	lines := strings.Split(sql, "\n")
	noTx := strings.HasPrefix(strings.TrimSpace(sql), schema.NoTransaction)
	created := map[string]bool{}
	for _, m := range reCreated.FindAllStringSubmatch(sql, -1) {
		created[strings.ToLower(m[1])] = true
	}
	var out []Diagnostic
	report := func(i int, msg string) {
		if strings.Contains(lines[i], "lidza:ignore L021") || (i > 0 && strings.Contains(lines[i-1], "lidza:ignore L021")) {
			return
		}
		out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "warning", Code: "L021",
			File: rel, Line: i + 1, Column: 1, Message: msg + `; once that is the plan (a small table, a maintenance window), "-- lidza:ignore L021" on the line before accepts it`})
	}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		existing := ""
		if m := reAlter.FindStringSubmatch(t); m != nil && !created[strings.ToLower(m[1])] {
			existing = m[1]
		}
		switch {
		case reRename.MatchString(t) && reAlter.MatchString(t):
			report(i, "a rename breaks the version still running while the deploy rolls out: it reads the old name. Expand and contract: add the new column or table, write both and backfill, switch reads in a release, drop the old one in a later release")
		case existing != "" && reType.MatchString(t):
			report(i, "changing the type of "+existing+"'s column rewrites the table under an exclusive lock, and the running version may not read the new type. Expand and contract: add a column of the new type, write both and backfill in batches, switch reads, drop the old column in a later release")
		case existing != "" && reDropCol.MatchString(t), reDropTable.MatchString(t):
			report(i, "the version still running during the deploy reads what this drops. Release code that no longer uses it first, then drop it in a later migration")
		case existing != "" && reVolatile.MatchString(t):
			report(i, "a volatile default makes Postgres rewrite "+existing+" under an exclusive lock to fill the new column. Add the column without the default (or with a constant), backfill in batches, then set the default")
		case existing != "" && !noTx && reNotNull.MatchString(t):
			report(i, "SET NOT NULL scans "+existing+" while it holds an exclusive lock. Add CHECK (column IS NOT NULL) NOT VALID, VALIDATE it in a "+schema.NoTransaction+" migration, then SET NOT NULL (lidza gen writes this)")
		case existing != "" && reCheckedFK.MatchString(t) && !strings.Contains(strings.ToUpper(t), "NOT VALID"):
			report(i, "adding the constraint checks every row of "+existing+" while it blocks writes. Add it NOT VALID, then VALIDATE CONSTRAINT in a "+schema.NoTransaction+" migration (lidza gen writes this)")
		case existing != "" && reAddUnique.MatchString(t):
			report(i, "adding the constraint builds its index while it blocks writes to "+existing+". CREATE UNIQUE INDEX CONCURRENTLY in a "+schema.NoTransaction+" migration, then ADD CONSTRAINT ... USING INDEX (lidza gen writes this)")
		default:
			if m := reIndexOn.FindStringSubmatch(t); m != nil && m[1] == "" && !created[strings.ToLower(m[2])] {
				report(i, "building an index without CONCURRENTLY blocks writes to "+m[2]+" until it is done. Use CREATE INDEX CONCURRENTLY in a "+schema.NoTransaction+" migration (lidza gen writes this)")
			}
		}
	}
	return out
}
