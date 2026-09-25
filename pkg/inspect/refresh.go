package inspect

import (
	"os"
	"path/filepath"

	"github.com/agim/lidza/pkg/config"
)

// GuideFile is the app's guide, written by `lidza new`, relative to the
// project root. It opens /llms-full.txt.
const GuideFile = "docs/lidza-guide.md"

// Files under .lidza that Refresh writes and the dev server serves.
const (
	LLMSFile     = ".lidza/llms.txt"
	LLMSFullFile = ".lidza/llms-full.txt"
)

// LLMSFor inspects the project and renders both llms documents.
func LLMSFor(dir string, cfg *config.Config) (short, full string, err error) {
	c, err := Project(dir, cfg)
	if err != nil {
		return "", "", err
	}
	guide, _ := os.ReadFile(filepath.Join(c.App.Dir, GuideFile))
	short, full = LLMS(c, cfg, string(guide))
	return short, full, nil
}

// Refresh writes .lidza/context.json, .lidza/llms.txt and
// .lidza/llms-full.txt. `lidza dev` calls it after every successful build.
func Refresh(dir string, cfg *config.Config) error {
	c, err := Write(dir, cfg)
	if err != nil {
		return err
	}
	guide, _ := os.ReadFile(filepath.Join(c.App.Dir, GuideFile))
	short, full := LLMS(c, cfg, string(guide))
	if err := os.WriteFile(filepath.Join(c.App.Dir, LLMSFile), []byte(short), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.App.Dir, LLMSFullFile), []byte(full), 0o644)
}
