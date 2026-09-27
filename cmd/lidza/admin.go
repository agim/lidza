package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agim/lidza/pkg/credentials"
)

// runAdmin is `lidza admin add EMAIL... | remove EMAIL... | list`: the
// ADMIN_USERS list in the sealed credentials, which the admin pages read
// within seconds, no restart. The first account to sign in is an admin
// too; this is how anyone else, or a developer whose first account was a
// test's, gets in.
func runAdmin(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("admin: add EMAIL..., remove EMAIL..., or list")
	}
	fs := flags("admin " + args[0])
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	abs, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	current := []string{}
	if vals, err := credentials.Read(abs); err == nil {
		current = splitList(vals[envAdminUsers])
	}
	rest := fs.Args()
	switch args[0] {
	case "list":
		if len(current) == 0 {
			fmt.Printf("%s is empty in %s; the first account to sign in is the only admin\n", envAdminUsers, credentials.File)
		}
		for _, a := range current {
			fmt.Println(a)
		}
		return nil
	case "add", "remove":
		if len(rest) == 0 {
			return fmt.Errorf("admin %s: give emails or user ids", args[0])
		}
		next := editList(current, rest, args[0] == "add")
		if err := ensureKey(abs); err != nil {
			return err
		}
		if err := credentials.Set(abs, map[string]string{envAdminUsers: strings.Join(next, ",")}); err != nil {
			return err
		}
		fmt.Printf("%s: %s (sealed in %s; the admin pages read it within seconds)\n", envAdminUsers, strings.Join(next, ", "), credentials.File)
		return nil
	}
	return fmt.Errorf("admin: unknown subcommand %q", args[0])
}

// envAdminUsers is the admin pack's list (admin.EnvAdminUsers), named
// here so the CLI does not carry the pack.
const envAdminUsers = "ADMIN_USERS"

// editList adds or removes entries, case-insensitively, keeping order.
func editList(list, entries []string, add bool) []string {
	out := []string{}
	has := func(l []string, e string) bool {
		for _, x := range l {
			if strings.EqualFold(x, e) {
				return true
			}
		}
		return false
	}
	for _, x := range list {
		if add || !has(entries, x) {
			out = append(out, x)
		}
	}
	if add {
		for _, e := range entries {
			if e = strings.TrimSpace(e); e != "" && !has(out, e) {
				out = append(out, e)
			}
		}
	}
	return out
}
