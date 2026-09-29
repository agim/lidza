package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The CI workflow and the Dependabot configuration of a new app parse as
// YAML; npm is audited and updated only where there is a package.json.
func TestGitHubFiles(t *testing.T) {
	for _, tpl := range []string{"react", "htmx"} {
		t.Run(tpl, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "app")
			if err := New(context.Background(), Options{Name: "app", Dir: dir, Template: tpl, LidzaDir: "../..", SkipModTidy: true}); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(filepath.Join(dir, "package.json"))
			npm := err == nil

			var ci struct {
				Jobs map[string]struct {
					Steps []struct {
						Name string `yaml:"name"`
						Run  string `yaml:"run"`
						Uses string `yaml:"uses"`
					} `yaml:"steps"`
				} `yaml:"jobs"`
			}
			parseYAML(t, filepath.Join(dir, ".github", "workflows", "ci.yml"), &ci)
			var runs []string
			for _, s := range ci.Jobs["verify"].Steps {
				runs = append(runs, s.Run)
			}
			all := strings.Join(runs, "\n")
			if !strings.Contains(all, "golang.org/x/vuln/cmd/govulncheck@") || !strings.Contains(all, "lidza verify --strict") {
				t.Errorf("ci.yml steps:\n%s", all)
			}
			if got := strings.Contains(all, "npm audit --omit=dev --audit-level=high"); got != npm {
				t.Errorf("npm audit in ci.yml: %v, package.json: %v", got, npm)
			}

			var dep struct {
				Version int `yaml:"version"`
				Updates []struct {
					Ecosystem string `yaml:"package-ecosystem"`
					Directory string `yaml:"directory"`
					Schedule  struct {
						Interval string `yaml:"interval"`
					} `yaml:"schedule"`
					Ignore []struct {
						Name string `yaml:"dependency-name"`
					} `yaml:"ignore"`
					Groups map[string]struct {
						UpdateTypes []string `yaml:"update-types"`
					} `yaml:"groups"`
				} `yaml:"updates"`
			}
			parseYAML(t, filepath.Join(dir, ".github", "dependabot.yml"), &dep)
			if dep.Version != 2 {
				t.Errorf("dependabot version %d", dep.Version)
			}
			seen := map[string]bool{}
			for _, u := range dep.Updates {
				seen[u.Ecosystem] = true
				if u.Directory != "/" || u.Schedule.Interval != "weekly" {
					t.Errorf("%s: directory %q, interval %q", u.Ecosystem, u.Directory, u.Schedule.Interval)
				}
				grouped := false
				for _, g := range u.Groups {
					if strings.Join(g.UpdateTypes, ",") == "minor,patch" {
						grouped = true
					}
				}
				if !grouped {
					t.Errorf("%s: minor and patch not grouped", u.Ecosystem)
				}
				if u.Ecosystem == "gomod" && (len(u.Ignore) == 0 || u.Ignore[0].Name != Module) {
					t.Errorf("gomod: the framework should be left to lidza update, ignore %+v", u.Ignore)
				}
			}
			if !seen["gomod"] || !seen["github-actions"] || seen["npm"] != npm {
				t.Errorf("ecosystems %v, package.json: %v", seen, npm)
			}
		})
	}
}

func parseYAML(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, data)
	}
}
