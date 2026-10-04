package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/agim/lidza/pkg/brief"
	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/crud"
	"github.com/agim/lidza/pkg/diag"
	"github.com/agim/lidza/pkg/inspect"
	"github.com/agim/lidza/pkg/pack"
	"github.com/agim/lidza/pkg/recipes"
	"github.com/agim/lidza/pkg/scaffold"
	"github.com/agim/lidza/pkg/schema"
	"github.com/agim/lidza/pkg/sdk"
	"github.com/agim/lidza/pkg/version"
)

func runGen(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "resource" {
		return runGenResource(ctx, args[1:])
	}
	if len(args) > 0 && args[0] == "deploy" {
		return runGenDeploy(ctx, args[1:])
	}
	fs := flags("gen")
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	return generateAll(abs, cfg, os.Stdout)
}

// runGenResource is `lidza gen resource <Model>`: queries, Create and
// Update types, handlers and the routes.go line, then the generators.
func runGenResource(_ context.Context, args []string) error {
	fs := flags("gen resource")
	dir := fs.String("dir", ".", "project directory")
	force := fs.Bool("force", false, "overwrite the handlers file")
	public := fs.Bool("public", false, "a resource anyone may read and write: marks the model @public, routes not behind sign-in, rows not scoped to a user")
	shared := fs.Bool("shared", false, "a resource every signed-in user shares: marks the model @shared, routes behind sign-in, rows not scoped to one user")
	var model string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		model, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if model == "" {
		return errors.New("gen resource: model name required (a model in schema.lidza)")
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("gen resource needs a lidza.json project")
	}
	if err := generationVersion(abs, version.Module()); err != nil {
		return err
	}
	res, err := crud.Generate(abs, crud.Options{Model: model, Module: inspect.ModulePath(abs), Force: *force, Public: *public, Shared: *shared, Auth: slices.Contains(cfg.Packs, "lidza/auth"), AppDir: cfg.AppDir})
	if err != nil {
		return err
	}
	fmt.Printf("resource %s: wrote %s\n", model, strings.Join(res.Files, ", "))
	switch {
	case res.Public:
		fmt.Printf("resource %s: public (@public): open to visitors, rows not scoped to a user\n", model)
	case res.Shared:
		fmt.Printf("resource %s: shared (@shared): routes behind auth.Require(), every signed-in user sees every row\n", model)
	case res.Owner != "":
		fmt.Printf("resource %s: owned by %s, the signed-in user; routes behind auth.Require(); another user's row is a 404\n", model, res.Owner)
	default:
		fmt.Printf("resource %s: routes behind auth.Require(); no owner field, so every signed-in user sees every row\n", model)
	}
	for _, t := range res.StaleInputs {
		fmt.Printf("schema.lidza: %s still takes %s, which the handlers ignore: remove it\n", t, res.Owner)
	}
	if !res.Registered {
		fmt.Printf("add to %s (func routes):\n\t%s\n", filepath.ToSlash(cfg.AppPath("routes.go")), strings.ReplaceAll(res.RoutesLine, "\n", "\n\t"))
	}
	if err := generateAll(abs, cfg, os.Stdout); err != nil {
		return err
	}
	fmt.Println("next: `lidza db migrate` if the model is new, then `lidza check`")
	return nil
}

// generateAll runs the schema generators, the pack wrappers and builds,
// then the handler-derived outputs.
func generateAll(dir string, cfg *config.Config, out io.Writer) error {
	if err := generationVersion(dir, version.Module()); err != nil {
		return err
	}
	if cfg != nil {
		added, err := pack.SyncFragments(dir, cfg.Packs)
		if err != nil {
			return fmt.Errorf("pack schemas: %w", err)
		}
		if len(added) > 0 {
			fmt.Fprintf(out, "[lidza] schema.lidza: %s synced from the packs' schemas\n", strings.Join(added, ", "))
		}
	}
	if err := generateSchema(dir, out); err != nil {
		return err
	}
	if err := generatePacks(context.Background(), dir, cfg, out); err != nil {
		return err
	}
	if cfg != nil {
		changed, err := scaffold.Refresh(dir, cfg)
		if err != nil {
			return fmt.Errorf("recipes: %w", err)
		}
		if len(changed) > 0 {
			fmt.Fprintf(out, "[lidza] recipes: updated %s\n", strings.Join(changed, ", "))
		}
		for _, name := range brief.AgentFiles(dir)[1:] {
			fmt.Fprintf(out, "[lidza] %s differs from %s, so it was kept: merge what it adds into %s, then make it the one line %s", name, brief.AgentFile, brief.AgentFile, brief.AgentStub)
		}
	} else if _, err := recipes.Sync(dir); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	return generateClient(dir, cfg, out)
}

// Released CLIs embed the schemas and templates of their own release.
// Generating against a different module can silently add or drop pack
// fields. Check the app's pin before any of those files are rewritten.
func generationVersion(dir, cli string) error {
	// Unreleased framework development: a CLI built from source carries a
	// pseudo-version (go build stamps one), not a release tag.
	if !semver.IsValid(cli) || module.IsPseudoVersion(cli) {
		return nil
	}
	path := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	mod, err := modfile.Parse(path, data, nil)
	if err != nil {
		return err
	}
	var project string
	for _, req := range mod.Require {
		if req.Mod.Path == version.ModulePath {
			project = req.Mod.Version
			break
		}
	}
	if project == "" {
		return nil
	}
	for _, rep := range mod.Replace {
		if rep.Old.Path == version.ModulePath && (rep.Old.Version == "" || rep.Old.Version == project) {
			if rep.New.Version == "" { // An explicit local framework checkout.
				return nil
			}
			project = rep.New.Version
			break
		}
	}
	if semver.Compare(cli, project) != 0 {
		return fmt.Errorf("framework module %s does not match CLI %s; generation stopped before rewriting pack schemas. Install the matching CLI: go install %s/cmd/lidza@%s (or update the app and CLI together with lidza update)", project, cli, version.ModulePath, project)
	}
	return nil
}

// generatePacks writes packs.go and each enabled pack's wrapper and
// schema.rs, then builds the modules that are missing or stale.
func generatePacks(ctx context.Context, dir string, cfg *config.Config, out io.Writer) error {
	if cfg == nil {
		return nil
	}
	module := inspect.ModulePath(dir)
	if module == "" {
		return nil
	}
	s, err := schema.Load(dir)
	if err != nil {
		return err
	}
	changed, err := pack.Generate(dir, module, cfg.AppDir, cfg.Packs, s)
	if err != nil {
		return fmt.Errorf("packs: %w", err)
	}
	if len(changed) > 0 {
		fmt.Fprintf(out, "[lidza] packs: wrote %s\n", strings.Join(changed, ", "))
		// A new pack imports the engine; the app's go.sum must learn its
		// dependencies.
		tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
		tidy.Dir = dir
		if res, err := tidy.CombinedOutput(); err != nil {
			fmt.Fprintf(out, "[lidza] go mod tidy: %v\n%s", err, res)
		}
	}
	if err := pack.BuildStale(ctx, dir, cfg.Packs, out); err != nil {
		return err
	}
	return generateQueries(ctx, dir, out)
}

// generateQueries runs sqlc when the project has sqlc.yaml and a schema.
func generateQueries(ctx context.Context, dir string, out io.Writer) error {
	if _, err := os.Stat(filepath.Join(dir, pack.SQLCFile)); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, schema.SQLFile)); err != nil {
		return nil
	}
	s, err := schema.Load(dir)
	if err != nil {
		return err
	}
	sync, err := pack.SyncSQLCNames(dir, s)
	if err != nil {
		return fmt.Errorf("%s: %w", pack.SQLCFile, err)
	}
	if sync.Note != "" {
		fmt.Fprintf(out, "[lidza] %s\n", sync.Note)
	}
	if sync.Added {
		fmt.Fprintf(out, "[lidza] %s: db/queries/gen now spells Go names as schema/ does (initialisms such as URL and HTML in capitals, their plurals as IDs and URLs)\n", pack.SQLCFile)
		if renames := schema.QueryRenames(s); len(renames) > 0 {
			printRenames(out, renames)
		}
	}
	if _, err := exec.LookPath("sqlc"); err != nil {
		fmt.Fprintln(out, "[lidza] sqlc is not installed; db/queries not generated (run install.sh)")
		return nil
	}
	cmd := exec.CommandContext(ctx, "sqlc", "generate")
	cmd.Dir = dir
	if res, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlc generate: %v\n%s", err, res)
	}
	return nil
}

// generateSchema turns schema.lidza into the Go package, the SQL schema, a
// migration when the models changed, and the Rust module when the project
// has a crate. A project without schema.lidza is left alone.
func generateSchema(dir string, out io.Writer) error {
	s, err := schema.Load(dir)
	if err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	cargoDir := diag.Detect(dir).CargoDir
	if cargoDir == "." {
		cargoDir = ""
	}
	res, err := schema.Generate(dir, s, cargoDir, "")
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if len(res.Files) > 0 {
		fmt.Fprintf(out, "[lidza] %s\n", res.Describe())
	}
	if len(res.Renamed) > 0 {
		fmt.Fprintf(out, "[lidza] schema/: plurals of initialisms are now spelled IDs and URLs\n")
		printRenames(out, res.Renamed)
	}
	return nil
}

// printRenames lists Go names a new release changed, for the app's code
// to follow.
func printRenames(out io.Writer, renames []schema.Rename) {
	fmt.Fprintln(out, "[lidza] rename in the app's code (go build ./... lists every use):")
	for _, r := range renames {
		fmt.Fprintf(out, "[lidza]   %s\n", r)
	}
}

// generateClient refreshes the context files (context.json, openapi.json,
// llms) and writes the TypeScript client for projects with a frontend
// build. Type-check warnings are printed; the client covers what resolved.
func generateClient(dir string, cfg *config.Config, out io.Writer) error {
	c, err := inspect.Refresh(dir, cfg)
	if err != nil {
		return fmt.Errorf("context: %w", err)
	}
	for _, w := range c.Warnings {
		fmt.Fprintf(out, "[lidza] type-check: %s\n", w)
	}
	if cfg == nil {
		return nil
	}
	if cfg.SDK.Dart != "" {
		if err := sdk.WriteDart(dir, cfg.SDK.Dart, c); err != nil {
			return fmt.Errorf("dart client: %w", err)
		}
	}
	if cfg.Frontend.Dist == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return nil
	}
	if err := sdk.WriteTypeScript(dir, c); err != nil {
		return fmt.Errorf("client: %w", err)
	}
	return nil
}

// runGenDeploy is `lidza gen deploy`: the Dockerfile, .dockerignore and
// deploy/<name>.service from the current templates. A file the app
// changed is kept and named; --force replaces it (git diff shows what
// changed).
func runGenDeploy(_ context.Context, args []string) error {
	fs := flags("gen deploy")
	dir := fs.String("dir", ".", "project directory")
	force := fs.Bool("force", false, "replace the deployment files the app changed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("gen deploy needs a lidza.json project")
	}
	written, kept, err := scaffold.DeployFiles(abs, cfg, *force)
	if err != nil {
		return err
	}
	for _, f := range written {
		fmt.Printf("[gen] wrote %s\n", f)
	}
	for _, f := range kept {
		fmt.Printf("[gen] %s differs from the current template; lidza gen deploy --force replaces it (review with git diff)\n", f)
	}
	if len(written)+len(kept) == 0 {
		fmt.Println("[gen] deployment files match the current templates")
	}
	if local := scaffold.AppVersions(abs).LocalPath; local != "" {
		fmt.Printf("[gen] go.mod replaces the framework with the local checkout %s, which a container build cannot reach: pin a release (go mod edit -dropreplace %s, then lidza update) before building the image\n", local, version.ModulePath)
	}
	return nil
}
