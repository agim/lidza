package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agim/lidza/pkg/decisions"
)

// runDecision is `lidza decision add "<title>" --why ... [--touches ...]`
// and `lidza decision list`: the app's decision log, docs/decisions.md.
func runDecision(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("decision: add \"<title>\" --why \"...\" or list")
	}
	switch args[0] {
	case "list":
		fs := flags("decision list")
		dir := fs.String("dir", ".", "project directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		abs, _, err := loadProject(*dir)
		if err != nil {
			return err
		}
		es, err := decisions.Load(abs)
		if err != nil {
			return err
		}
		for _, e := range es {
			fmt.Printf("%s  %s\n", e.Date, e.Title)
		}
		return nil
	case "add":
		fs := flags("decision add")
		dir := fs.String("dir", ".", "project directory")
		why := fs.String("why", "", "the reason, one or two sentences (required)")
		touches := fs.String("touches", "", "the files, packs or tables it concerns")
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
		abs, cfg, err := loadProject(*dir)
		if err != nil {
			return err
		}
		if cfg == nil {
			return errors.New("decision add needs a lidza.json project")
		}
		e, err := decisions.Add(abs, title, *why, *touches)
		if err != nil {
			return err
		}
		fmt.Printf("recorded in %s: %s: %s\n", decisions.File, e.Date, e.Title)
		return nil
	}
	return fmt.Errorf("decision: unknown subcommand %q (add, list)", args[0])
}
