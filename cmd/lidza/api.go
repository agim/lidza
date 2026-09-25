package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/agim/lidza/pkg/apidoc"
)

// runAPI is `lidza api [package] [--filter name]`: the framework's public
// Go API rendered from the sources the project resolves, so an agent
// reads a signature instead of guessing it. `--list` names the packages.
func runAPI(ctx context.Context, args []string) error {
	fs := flags("api")
	dir := fs.String("dir", ".", "project directory")
	filter := fs.String("filter", "", "only declarations whose name contains this text")
	list := fs.Bool("list", false, "list the packages instead of rendering them")
	var pkg string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pkg, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if pkg == "" && fs.NArg() == 1 {
		pkg = fs.Arg(0)
	}
	abs, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	moduleDir, err := apidoc.ModuleDir(ctx, abs)
	if err != nil {
		return err
	}
	if *list {
		pkgs, err := apidoc.Packages(moduleDir)
		if err != nil {
			return err
		}
		app := map[string]bool{}
		for _, p := range apidoc.AppPackages(moduleDir) {
			app[p] = true
		}
		for _, p := range pkgs {
			note := ""
			if !app[p] {
				note = "  (framework internal)"
			}
			fmt.Printf("%s%s\n", apidoc.ImportPath(p), note)
		}
		return nil
	}
	var rels []string
	if pkg != "" {
		rel, ok := apidoc.Rel(pkg)
		if !ok {
			rel = strings.TrimSuffix(pkg, "/")
		}
		if rel == "lidza" || rel == "." {
			rel = ""
		}
		if !apidoc.Exists(moduleDir, apidoc.ImportPath(rel)) {
			return fmt.Errorf("no package %s in this version of Līdza; `lidza api --list` names them", apidoc.ImportPath(rel))
		}
		rels = []string{rel}
	}
	text, err := apidoc.Render(moduleDir, rels, *filter)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, "no declaration matches")
		return nil
	}
	fmt.Print(text)
	return nil
}
