package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/agim/lidza/pkg/decisions"
	"github.com/agim/lidza/pkg/pack"
)

const packUsage = `usage:
  lidza pack add <name> [--why "..."]   enable an official pack (db, auth, jobs, mail, llm, storage, ...) and record why
  lidza pack scaffold <name>   create packs/<name> with a crate and an example capability
  lidza pack build [name]      compile the crate(s) to WASM
  lidza pack list              show enabled packs, capabilities and build state
`

func runPack(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, packUsage)
		return errors.New("pack: subcommand required")
	}
	sub, rest := args[0], args[1:]
	fs := flags("pack " + sub)
	dir := fs.String("dir", ".", "project directory")
	why := fs.String("why", "", "pack add: why this pack, recorded in docs/decisions.md")
	var name string
	if len(rest) > 0 && rest[0][0] != '-' {
		name, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("pack: packs need a lidza.json project")
	}
	switch sub {
	case "add":
		if name == "" {
			return errors.New("pack add: name required")
		}
		entry, o, err := pack.Add(abs, name)
		if err != nil {
			return err
		}
		if !slices.Contains(cfg.Packs, entry) {
			cfg.Packs = append(cfg.Packs, entry)
			if err := cfg.Save(abs); err != nil {
				return err
			}
		}
		fmt.Printf("added %s: %s\n", entry, o.Description)
		if err := generateAll(abs, cfg, os.Stdout); err != nil {
			return err
		}
		if *why != "" {
			if _, err := decisions.Add(abs, "Pack "+o.Name+" added", *why, "lidza.json, packs.go"); err != nil {
				return err
			}
			fmt.Printf("recorded in %s\n", decisions.File)
		} else {
			fmt.Printf("  - record why: lidza pack add %s --why \"...\" next time, or lidza decision add \"Pack %s added\" --why \"...\"\n", o.Name, o.Name)
		}
		for _, n := range o.Notes {
			fmt.Println("  -", n)
		}
		return nil
	case "scaffold":
		if name == "" {
			return errors.New("pack scaffold: name required")
		}
		m, err := pack.Scaffold(abs, name)
		if err != nil {
			return err
		}
		if !slices.Contains(cfg.Packs, name) {
			cfg.Packs = append(cfg.Packs, name)
			if err := cfg.Save(abs); err != nil {
				return err
			}
		}
		fmt.Printf("created %s with capability %q\n", m.Path, m.Capabilities[0].Name)
		if err := generateAll(abs, cfg, os.Stdout); err != nil {
			return err
		}
		fmt.Printf("\nnext: edit %s and %s/src/lib.rs, then `lidza check`\n", m.Path, m.CrateDir())
		return nil
	case "build":
		names := cfg.Packs
		if name != "" {
			names = []string{name}
		}
		for _, n := range names {
			if pack.IsOfficialGo(n) {
				continue
			}
			m, err := pack.Load(abs, n)
			if err != nil {
				return err
			}
			if err := pack.Build(ctx, abs, m, os.Stdout); err != nil {
				return err
			}
		}
		return nil
	case "list":
		if len(cfg.Packs) == 0 {
			fmt.Println("no packs enabled; `lidza pack scaffold <name>` adds one")
			return nil
		}
		for _, n := range cfg.Packs {
			if o, ok := pack.FindOfficial(n); ok && pack.IsOfficialGo(n) {
				fmt.Printf("%s (Go, from the framework): %s\n", n, o.Description)
				continue
			}
			m, err := pack.Load(abs, n)
			if err != nil {
				return err
			}
			state := "built"
			if pack.NeedsBuild(abs, m) {
				state = "needs build"
			}
			fmt.Printf("%s %s (%s): %s\n", m.Name, m.Version, state, m.Description)
			for _, c := range m.Capabilities {
				fmt.Printf("  %s(%s) -> %s: %s\n", c.Name, c.Input, c.Output, c.Description)
			}
		}
		return nil
	default:
		fmt.Fprint(os.Stderr, packUsage)
		return fmt.Errorf("pack: unknown subcommand %q", sub)
	}
}
