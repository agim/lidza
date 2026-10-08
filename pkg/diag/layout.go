package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agim/lidza/pkg/config"
)

var rootConfigFile = regexp.MustCompile(`^(vite|vitest|playwright|eslint|astro|svelte|postcss|tailwind)\.config\.(js|mjs|cjs|ts|mts|cts)$|^tsconfig(\.[a-zA-Z0-9_-]+)?\.json$`)
var rootTestFile = regexp.MustCompile(`_test\.go$|\.(test|spec)\.(js|jsx|mjs|cjs|ts|tsx|mts|cts)$`)

// rootLayout is L020: app roots contain entrypoints and tool configuration,
// not tests, feature code, notes, dumps or build artifacts. Directory contents
// are checked by their own tools; package unit tests remain beside their code.
func rootLayout(root string) []Diagnostic {
	cfg, err := config.Load(root)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return []Diagnostic{{Layer: "go", Tool: "lidza rules", Code: "L020", Severity: "error", Message: "cannot inspect project root: " + err.Error()}}
	}
	var out []Diagnostic
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		message := ""
		switch {
		case rootTestFile.MatchString(name):
			message = "test file at the project root: move integration tests to tests/ and start the importable app with lidzatest.Start(t, app.New(nil)); unit tests belong beside their package, browser specs in e2e/"
			if cfg.AppDir == "" {
				message += "; first move application wiring into app/ and set appDir in lidza.json (guide: The app in its own package) because Go cannot import package main"
			}
		case allowedRootFile(name, cfg.AppDir):
			continue
		case strings.HasSuffix(name, ".go"):
			message = "application code at the project root: keep main.go here, wiring in appDir, handlers in handlers/, and business code in internal/<feature>/ (guide: Organize application packages)"
		default:
			message = "unexpected file at the project root: keep documentation in docs/, scripts in scripts/, fixtures in testdata/, and temporary or build output in .lidza/ or bin/; remove obsolete files"
		}
		out = append(out, Diagnostic{Layer: "go", Tool: "lidza rules", Code: "L020", Severity: "error", File: filepath.ToSlash(name), Line: 1, Column: 1, Message: fmt.Sprintf("%s: %s", name, message)})
	}
	return out
}

func allowedRootFile(name, appDir string) bool {
	if rootConfigFile.MatchString(name) {
		return true
	}
	switch name {
	case ".git", "main.go", "lidza.json", "schema.lidza", "go.mod", "go.sum", "go.work", "go.work.sum",
		"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb",
		"index.html", "sqlc.yaml", "sqlc.yml", "Dockerfile", "Makefile", "LICENSE", "LICENSE.md", "LICENSE.txt",
		"README.md", "CHANGELOG.md", "AGENTS.md", "CLAUDE.md", "GEMINI.md",
		".gitignore", ".gitattributes", ".gitmodules", ".dockerignore", ".mcp.json", ".editorconfig", ".npmrc", ".nvmrc", ".node-version",
		".prettierrc", ".prettierrc.json", ".prettierignore",
		".env", ".env.local", ".env.example", ".env.test", ".env.test.local", ".env.development", ".env.development.local", ".env.production", ".env.production.local":
		return true
	case "routes.go", "start.go", "tools.go", "packs.go", "head.go", "pages.go":
		// Existing root-package apps keep their wiring until migrated. Their
		// tests and arbitrary source files are still rejected.
		return appDir == ""
	}
	return false
}
