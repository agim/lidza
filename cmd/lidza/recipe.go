package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agim/lidza/pkg/recipes"
	"github.com/agim/lidza/pkg/scaffold"
)

// runRecipe is `lidza recipe add "<title>" [--description ...] [--step ...]`
// and `lidza recipe list`: the app's recipes in docs/lidza-guide.md, from
// which the prompts, skills and commands are generated.
func runRecipe(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("recipe: add \"<title>\" or list")
	}
	switch args[0] {
	case "list":
		fs := flags("recipe list")
		dir := fs.String("dir", ".", "project directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		abs, _, err := loadProject(*dir)
		if err != nil {
			return err
		}
		rs, err := recipes.Load(abs)
		if err != nil {
			return err
		}
		for _, r := range rs {
			fmt.Printf("%-24s %-9s %s\n", r.Name, r.Scope, r.Title)
		}
		return nil
	case "add":
		fs := flags("recipe add")
		dir := fs.String("dir", ".", "project directory")
		description := fs.String("description", "", "when the recipe applies, one or two sentences")
		var steps stepList
		fs.Var(&steps, "step", "a step, in order (repeatable)")
		var title string
		rest := args[1:]
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			title, rest = rest[0], rest[1:]
		}
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if title == "" && fs.NArg() == 1 {
			title = fs.Arg(0)
		}
		if title == "" {
			return errors.New("recipe add: title required, e.g. lidza recipe add \"Paginate a list\"")
		}
		abs, cfg, err := loadProject(*dir)
		if err != nil {
			return err
		}
		if cfg == nil {
			return errors.New("recipe add needs a lidza.json project")
		}
		r, err := recipes.Add(abs, title, *description, steps)
		if err != nil {
			return err
		}
		if _, err := scaffold.Refresh(abs, cfg); err != nil {
			return err
		}
		fmt.Printf("added %s (%s) to %s under \"%s\"; skills and commands written", r.Name, r.Title, recipes.GuideFile, strings.TrimPrefix(recipes.AppHeading, "## "))
		if len(steps) == 0 {
			fmt.Print("; fill in the steps")
		}
		fmt.Println()
		return nil
	}
	return fmt.Errorf("recipe: unknown subcommand %q (add, list)", args[0])
}

type stepList []string

func (s *stepList) String() string     { return strings.Join(*s, "; ") }
func (s *stepList) Set(v string) error { *s = append(*s, v); return nil }
