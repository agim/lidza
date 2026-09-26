// Package scaffold implements `lidza new`: it copies a template and writes
// the Go entrypoint, lidza.json and the agent instruction files.
package scaffold

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"text/template"
	"unicode/utf8"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/decisions"
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/recipes"
	"github.com/agim/lidza/pkg/schema"
	"github.com/agim/lidza/pkg/version"
	"github.com/agim/lidza/templates"
)

// Module is the framework's Go module path, required by every app.
const Module = "github.com/agim/lidza"

// namePlaceholder is replaced with the app name in every text file copied
// from a template.
const namePlaceholder = "__LIDZA_APP_NAME__"

//go:embed files
var files embed.FS

// Options for New.
type Options struct {
	// Name is the app name: also the directory, the Go module path and the
	// npm package name.
	Name string
	// Dir is where the app is created. Defaults to Name under the current
	// directory.
	Dir string
	// Template is the template name (config.Default knows them).
	Template string
	// LidzaDir, when set, is a local checkout of the framework that the app's
	// go.mod points at with a replace directive, instead of the published
	// module. Used while developing Līdza itself.
	LidzaDir string
	// Out receives progress output. Defaults to io.Discard.
	Out io.Writer
	// SkipModTidy leaves go.mod without running the go tool (tests).
	SkipModTidy bool
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// New creates an app.
func New(ctx context.Context, opt Options) error {
	if opt.Out == nil {
		opt.Out = io.Discard
	}
	if !nameRe.MatchString(opt.Name) {
		return fmt.Errorf("app name %q: use lowercase letters, digits and dashes, starting with a letter", opt.Name)
	}
	if opt.Template == "" {
		opt.Template = "react"
	}
	if !slices.Contains(templates.Names, opt.Template) {
		return fmt.Errorf("template %q does not exist yet; available: %s", opt.Template, strings.Join(templates.Names, ", "))
	}
	if opt.Dir == "" {
		opt.Dir = opt.Name
	}
	if err := ensureEmptyDir(opt.Dir); err != nil {
		return err
	}
	if opt.LidzaDir != "" {
		abs, err := filepath.Abs(opt.LidzaDir)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
			return fmt.Errorf("--lidza-dir %s: no go.mod there", abs)
		}
		opt.LidzaDir = abs
	}

	cfg := config.Default(opt.Name, opt.Template)

	if err := copyTemplate(opt.Template, opt.Dir, opt.Name); err != nil {
		return err
	}
	if err := cfg.Save(opt.Dir); err != nil {
		return err
	}
	data := dataFor(&cfg, opt.LidzaDir)
	for _, f := range []struct{ src, dst string }{
		{"go.mod.tmpl", "go.mod"},
		{"main.go.tmpl", "main.go"},
		{"start.go.tmpl", "start.go"},
		{"routes.go.tmpl", "routes.go"},
		{"routes_test.go.tmpl", "routes_test.go"},
		{"tools.go.tmpl", "tools.go"},
		{"lidza-guide.md.tmpl", filepath.FromSlash(recipes.GuideFile)},
		{"decisions.md.tmpl", filepath.FromSlash(decisions.File)},
		{"README.md.tmpl", "README.md"},
		{"ci.yml.tmpl", filepath.Join(".github", "workflows", "ci.yml")},
		{"mcp.json.tmpl", ".mcp.json"},
		{"gemini-settings.json.tmpl", filepath.Join(".gemini", "settings.json")},
		{"schema.lidza.tmpl", schema.FileName},
		{"scale_test.js.tmpl", filepath.Join("benchmarks", "scale_test.js")},
		{"Dockerfile.tmpl", "Dockerfile"},
		{"dockerignore.tmpl", ".dockerignore"},
		{"systemd.service.tmpl", filepath.Join("deploy", opt.Name+".service")},
	} {
		if err := render(f.src, filepath.Join(opt.Dir, f.dst), data); err != nil {
			return err
		}
	}
	// The guide's recipes become skills, and the agent files list them.
	rs, err := recipes.Sync(opt.Dir)
	if err != nil {
		return err
	}
	data.Recipes = RecipesLine(rs)
	for _, dst := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		if err := render("agent.md.tmpl", filepath.Join(opt.Dir, dst), data); err != nil {
			return err
		}
	}
	// The generated schema package must exist before the app compiles.
	parsed, err := schema.Load(opt.Dir)
	if err != nil {
		return err
	}
	if _, err := schema.Generate(opt.Dir, parsed, "", ""); err != nil {
		return err
	}
	if _, err := pack.Generate(opt.Dir, opt.Name, nil, parsed); err != nil {
		return err
	}
	fmt.Fprintf(opt.Out, "created %s (%s template)\n", opt.Dir, opt.Template)

	if opt.SkipModTidy {
		return nil
	}
	return resolveModule(ctx, opt)
}

type templateData struct {
	Name, Module, LidzaDir, Template, Dist, DevCmd, DevURL, Go string
	// LidzaVersion is the framework version for `go install` in the
	// Dockerfile and `go get` in go.mod: the release or commit the CLI was
	// built from, else latest.
	LidzaVersion string
	// DecisionsLine tells the agent about the decision log.
	DecisionsLine string
	// Recipes is the comma-separated list of recipe names from the guide.
	Recipes string
}

// dataFor builds the template data for an app from its configuration.
func dataFor(cfg *config.Config, lidzaDir string) templateData {
	return templateData{
		Name:          cfg.Name,
		Module:        Module,
		LidzaDir:      lidzaDir,
		Template:      cfg.Frontend.Template,
		Dist:          cfg.Frontend.Dist,
		DevCmd:        cfg.Frontend.Dev,
		DevURL:        cfg.Frontend.URL,
		Go:            goMinor(),
		LidzaVersion:  moduleVersion(),
		DecisionsLine: DecisionsLine,
	}
}

// moduleVersion is the framework version an app created by this CLI
// depends on: the release or commit the CLI was built from, so the module
// matches the code the CLI generates; "latest" for a build with no
// version.
func moduleVersion() string {
	if v := version.Module(); v != "" {
		return v
	}
	return "latest"
}

// DecisionsLine is the agent files' line about the decision log; Refresh
// adds it to apps that predate it.
const DecisionsLine = "Why the app is built a way (a pack added, Rust for a module, a dependency, a schema tradeoff) is recorded in `docs/decisions.md`: read it before working in those areas, and record yours in the same commit with `lidza decision add \"Title\" --why \"...\"` (MCP `lidza_decision_add`)."

// RecipesLine lists recipe names for the agent files: the framework's,
// then the app's own.
func RecipesLine(rs []recipes.Recipe) string {
	var fw, app []string
	for _, r := range rs {
		if r.Scope == recipes.ScopeApp {
			app = append(app, "`"+r.Name+"`")
		} else {
			fw = append(fw, "`"+r.Name+"`")
		}
	}
	line := strings.Join(fw, ", ")
	if len(app) > 0 {
		line += "; this app's own: " + strings.Join(app, ", ")
	}
	return line
}

// Agent-file markers around the recipe list, rewritten by lidza gen.
const (
	recipesOpen  = "<!-- lidza:recipes -->"
	recipesClose = "<!-- /lidza:recipes -->"
)

// FrameworkRecipes renders the guide template for an app and returns the
// body of its "## Recipes" section: the framework's recipes for this
// framework version and template.
func FrameworkRecipes(cfg *config.Config) (string, error) {
	body, err := files.ReadFile("files/lidza-guide.md.tmpl")
	if err != nil {
		return "", err
	}
	t, err := template.New("guide").Parse(string(body))
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, dataFor(cfg, "")); err != nil {
		return "", err
	}
	guide := buf.String()
	start := strings.Index(guide, "\n"+recipes.Heading+"\n")
	if start < 0 {
		return "", errors.New("guide template has no Recipes section")
	}
	start += len(recipes.Heading) + 2
	end := len(guide)
	if next := strings.Index(guide[start:], "\n## "); next >= 0 {
		end = start + next + 1
	}
	return strings.TrimSpace(guide[start:end]), nil
}

// Refresh brings an existing app up to date after the framework or the
// guide changed: the framework's recipes in the guide are replaced from
// the template (the app's own section is untouched), the skills and
// commands are rewritten, and the recipe list in the agent files is
// updated between its markers. It returns what changed, for the log.
func Refresh(dir string, cfg *config.Config) ([]string, error) {
	var changed []string
	if created, err := decisions.Ensure(dir, cfg.Name); err != nil {
		return nil, err
	} else if created {
		changed = append(changed, decisions.File)
	}
	if fw, err := FrameworkRecipes(cfg); err == nil {
		replaced, err := recipes.ReplaceFramework(dir, fw)
		if err != nil {
			return nil, err
		}
		if replaced {
			changed = append(changed, recipes.GuideFile+" (framework recipes)")
		}
	}
	rs, err := recipes.Sync(dir)
	if err != nil {
		return nil, err
	}
	line := recipesOpen + RecipesLine(rs) + recipesClose
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text := string(data)
		i, j := strings.Index(text, recipesOpen), strings.Index(text, recipesClose)
		if i < 0 || j < i {
			continue
		}
		next := text[:i] + line + text[j+len(recipesClose):]
		// An app from before the decision log gets the line once.
		if !strings.Contains(next, decisions.File) {
			if k := strings.Index(next[i:], "\n"); k >= 0 {
				next = next[:i+k+1] + "- " + DecisionsLine + "\n" + next[i+k+1:]
			}
		}
		if next == text {
			continue
		}
		if err := os.WriteFile(p, []byte(next), 0o644); err != nil {
			return nil, err
		}
		changed = append(changed, name)
	}
	return changed, nil
}

func render(src, dst string, data templateData) error {
	out, err := renderBytes(src, data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, out, 0o644)
}

func renderBytes(src string, data templateData) ([]byte, error) {
	body, err := files.ReadFile("files/" + src)
	if err != nil {
		return nil, err
	}
	t, err := template.New(src).Parse(string(body))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DeployFiles renders the deployment files from the current templates:
// the Dockerfile, .dockerignore and deploy/<name>.service. A missing file
// is written; one that matches the template is left; one the app edited
// (or an older template wrote) is replaced only with force, else
// reported in kept, so `lidza update` can say which deployment files
// the new release would change.
func DeployFiles(dir string, cfg *config.Config, force bool) (written, kept []string, err error) {
	data := dataFor(cfg, "")
	for _, f := range []struct{ src, dst string }{
		{"Dockerfile.tmpl", "Dockerfile"},
		{"dockerignore.tmpl", ".dockerignore"},
		{"systemd.service.tmpl", filepath.Join("deploy", cfg.Name+".service")},
	} {
		want, err := renderBytes(f.src, data)
		if err != nil {
			return nil, nil, err
		}
		p := filepath.Join(dir, f.dst)
		have, readErr := os.ReadFile(p)
		switch {
		case readErr == nil && bytes.Equal(have, want):
			continue
		case readErr == nil && !force:
			kept = append(kept, f.dst)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(p, want, 0o644); err != nil {
			return nil, nil, err
		}
		written = append(written, f.dst)
	}
	return written, kept, nil
}

// copyTemplate copies templates/<name> into dst, substituting the app name in
// text files.
func copyTemplate(name, dst, appName string) error {
	root := name
	return fs.WalkDir(templates.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		target := filepath.Join(dst, strings.TrimSuffix(rel, ".tmpl"))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := templates.FS.ReadFile(p)
		if err != nil {
			return err
		}
		if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
			data = bytes.ReplaceAll(data, []byte(namePlaceholder), []byte(appName))
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// resolveModule makes go.mod complete: with a local framework checkout a
// tidy is enough; otherwise the published module is fetched. Failure is
// reported, not fatal: the files are all there and the user can rerun.
func resolveModule(ctx context.Context, opt Options) error {
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = opt.Dir
		cmd.Stdout = opt.Out
		cmd.Stderr = opt.Out
		return cmd.Run()
	}
	var err error
	if opt.LidzaDir == "" {
		err = run("get", Module+"@"+moduleVersion())
	}
	if err == nil {
		err = run("mod", "tidy")
	}
	if err != nil {
		fmt.Fprintf(opt.Out, "warning: resolving the %s module failed (%v)\n", Module, err)
		fmt.Fprintf(opt.Out, "         run `go mod tidy` in %s once the module is reachable (private repo: set GOPRIVATE=%s)\n", opt.Dir, Module)
	}
	return nil
}

func ensureEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s exists and is not empty", dir)
	}
	return nil
}

// goMinor returns the running toolchain's "1.N" for the go directive.
func goMinor() string {
	v := strings.TrimPrefix(runtime.Version(), "go")
	if i := strings.LastIndex(v, "."); i > strings.Index(v, ".") {
		v = v[:i]
	}
	return v
}
