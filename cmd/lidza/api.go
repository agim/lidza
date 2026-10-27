package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/agim/lidza/pkg/apidoc"
)

// runAPI is `lidza api [package] [--filter name]`: the public Go API of
// the framework as the project resolves it, or of the project's own
// packages ("app", or "./handlers"), so an agent reads a signature
// instead of guessing it. `--list` names the packages.
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
	// Without a package or a filter, the list: rendering everything is
	// thousands of lines an agent then carries; "all" asks for it.
	if *list || (pkg == "" && *filter == "") {
		text, err := apidoc.Listing(ctx, abs)
		if err != nil {
			return err
		}
		fmt.Print(text)
		if !*list {
			fmt.Println("\nlidza api <package> renders one; --filter name searches them all; lidza api all renders every one.")
		}
		return nil
	}
	if pkg == "all" {
		pkg = ""
	}
	src, rels, err := apidoc.Resolve(ctx, abs, pkg)
	if err != nil {
		return err
	}
	text, err := src.Render(rels, *filter)
	if err != nil {
		return err
	}
	// A whole package too large to read: its signatures, and a filter for
	// the rest, as the MCP tool answers.
	if *filter == "" && len(text) > apidoc.Budget {
		if index, err := src.Index(rels, ""); err == nil && len(index) < len(text) {
			fmt.Printf("The full API is %d characters; these are its signatures. lidza api %s --filter <name> prints the doc comments of what you need.\n\n%s", len(text), pkgArg(pkg), index)
			return nil
		}
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, "no declaration matches")
		return nil
	}
	fmt.Print(text)
	return nil
}

// pkgArg is the package as the command line names it: "all" for every
// package.
func pkgArg(pkg string) string {
	if pkg == "" {
		return "all"
	}
	return pkg
}
