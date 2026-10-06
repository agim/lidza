package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/agim/lidza/pkg/decisions"
	"github.com/agim/lidza/pkg/schema"
)

// gitAttributes are the merge rules every app keeps in .gitattributes:
// the append-only logs keep both branches' entries (git's union merge),
// the schema lock merges model by model (lidza gen --merge-lock).
var gitAttributes = []string{
	decisions.File + " merge=union",
	"CHANGELOG.md merge=union",
	schema.LockFile + " merge=lidza-lock",
}

// MergeDriver is the git configuration of the lock's merge driver: git
// does not version it, so every clone gets it from lidza gen.
const MergeDriver = "lidza gen --merge-lock %O %A %B"

// EnsureMerging adds the merge rules .gitattributes lacks and, in a git
// checkout without it, registers the lock's merge driver. It returns
// what it changed.
func EnsureMerging(dir string) ([]string, error) {
	var changed []string
	p := filepath.Join(dir, ".gitattributes")
	data, _ := os.ReadFile(p)
	text := string(data)
	var missing []string
	for _, rule := range gitAttributes {
		if !strings.Contains("\n"+text+"\n", "\n"+rule+"\n") {
			missing = append(missing, rule)
		}
	}
	if len(missing) > 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if !strings.Contains(text, "# lidza:") {
			text += "# lidza: decisions and the changelog keep both branches' entries; the schema lock merges model by model.\n"
		}
		text += strings.Join(missing, "\n") + "\n"
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return nil, err
		}
		changed = append(changed, ".gitattributes")
	}
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git("rev-parse", "--git-dir"); err != nil {
		return changed, nil
	}
	if have, _ := git("config", "--get", "merge.lidza-lock.driver"); have != MergeDriver {
		if _, err := git("config", "merge.lidza-lock.name", "Līdza schema lock, model by model"); err == nil {
			if _, err := git("config", "merge.lidza-lock.driver", MergeDriver); err == nil {
				changed = append(changed, "git config merge.lidza-lock")
			}
		}
	}
	return changed, nil
}
