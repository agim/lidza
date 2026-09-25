package inspect

import (
	"encoding/json"
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
	OpenAPIFile  = ".lidza/openapi.json"
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

// Refresh writes .lidza/context.json, .lidza/openapi.json, .lidza/llms.txt
// and .lidza/llms-full.txt, and returns the context so callers can generate
// clients from it. `lidza dev` calls it after every successful build.
func Refresh(dir string, cfg *config.Config) (*Context, error) {
	c, err := Write(dir, cfg)
	if err != nil {
		return nil, err
	}
	doc, err := json.MarshalIndent(OpenAPI(c), "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeIfChanged(filepath.Join(c.App.Dir, OpenAPIFile), append(doc, '\n')); err != nil {
		return nil, err
	}
	guide, _ := os.ReadFile(filepath.Join(c.App.Dir, GuideFile))
	short, full := LLMS(c, cfg, string(guide))
	if err := writeIfChanged(filepath.Join(c.App.Dir, LLMSFile), []byte(short)); err != nil {
		return nil, err
	}
	return c, writeIfChanged(filepath.Join(c.App.Dir, LLMSFullFile), []byte(full))
}

func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}
