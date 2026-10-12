package diag

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/agim/lidza/pkg/env"
)

// envFiles are the environment files an app keeps: its development
// settings and the deployment's. .env.test is the test suite's fixture
// and is not read here.
var envFiles = []string{".env", ".env.local", ".env.dev", ".env.dev.local", ".env.production", ".env.production.local", ".env.example", "deploy/production.env"}

// appSettingsInEnv is L023: an app setting (a key, a provider, an
// address, a sender) in an environment file. The environment is the
// deployment's: the database, the master key, addresses, logging and
// tuning. The app's configuration lives in the app, in its database,
// so it moves with the database and a new server needs only those few
// variables. Commented lines are documentation and pass.
func appSettingsInEnv(root string) []Diagnostic {
	var out []Diagnostic
	for _, name := range envFiles {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			key, _, ok := strings.Cut(t, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" || env.Kind(key) != env.KindApp {
				continue
			}
			if strings.Contains(line, "lidza:ignore L023") || (i > 0 && strings.Contains(lines[i-1], "lidza:ignore L023")) {
				continue
			}
			out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Severity: "warning", Code: "L023", File: name, Line: i + 1, Column: 1,
				Message: key + " is the app's configuration, not the deployment's: keep it in the app's database (the admin pages, or lidza settings set " + key + "=...; lidza settings import moves this file's app settings there), so the app moves with its database and the environment holds only the database, the master key and tuning. In development, lidza credentials set dev." + key + "=... seeds it"})
		}
	}
	return out
}
