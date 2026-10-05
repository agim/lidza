package diag

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agim/lidza/pkg/schema"
)

// migrationNumbers reports two migrations in db/migrations that share a
// number (L019, an error). It happens when lidza gen ran on a checkout
// behind its branch: the number it picked was taken by a migration the
// pull then brought. The order of the two is then a matter of their
// names, and a database that applied one keeps a schema the other's
// author never saw.
func migrationNumbers(root string) []Diagnostic {
	entries, err := os.ReadDir(filepath.Join(root, schema.MigrationsDir))
	if err != nil {
		return nil
	}
	byNumber := map[string][]string{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".up.sql")
		if !ok {
			continue
		}
		num, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		byNumber[num] = append(byNumber[num], name)
	}
	var out []Diagnostic
	for _, names := range byNumber {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		for _, n := range names[1:] {
			out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "error", Code: "L019",
				File: schema.MigrationsDir + "/" + n + ".up.sql", Line: 1, Column: 1,
				Message: "migration " + n + " has the number of " + names[0] + ": one was generated on a checkout behind its branch. " +
					"If " + n + " is not applied anywhere but your development database, roll it back (lidza db rollback), delete its two files, and run lidza gen, which writes it again with the next number if it is still needed"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}
