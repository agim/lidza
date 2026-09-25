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
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/schema"
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
	data := templateData{
		Name:     opt.Name,
		Module:   Module,
		LidzaDir: opt.LidzaDir,
		Template: opt.Template,
		Dist:     cfg.Frontend.Dist,
		DevCmd:   cfg.Frontend.Dev,
		DevURL:   cfg.Frontend.URL,
		Go:       goMinor(),
	}
	for _, f := range []struct{ src, dst string }{
		{"go.mod.tmpl", "go.mod"},
		{"main.go.tmpl", "main.go"},
		{"routes.go.tmpl", "routes.go"},
		{"routes_test.go.tmpl", "routes_test.go"},
		{"agent.md.tmpl", "CLAUDE.md"},
		{"agent.md.tmpl", "AGENTS.md"},
		{"agent.md.tmpl", "GEMINI.md"},
		{"lidza-guide.md.tmpl", filepath.Join("docs", "lidza-guide.md")},
		{"mcp.json.tmpl", ".mcp.json"},
		{"gemini-settings.json.tmpl", filepath.Join(".gemini", "settings.json")},
		{"schema.lidza.tmpl", schema.FileName},
		{"scale_test.js.tmpl", filepath.Join("benchmarks", "scale_test.js")},
	} {
		if err := render(f.src, filepath.Join(opt.Dir, f.dst), data); err != nil {
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
}

func render(src, dst string, data templateData) error {
	body, err := files.ReadFile("files/" + src)
	if err != nil {
		return err
	}
	t, err := template.New(src).Parse(string(body))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
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
		err = run("get", Module+"@latest")
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
