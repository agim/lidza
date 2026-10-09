package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/env"
)

// runAdmin is `lidza admin add EMAIL... | remove EMAIL... | list`: the
// ADMIN_USERS list in the sealed credentials, which the admin pages read
// within seconds, no restart. The first account to sign in is an admin
// too; this is how anyone else, or a developer whose first account was a
// test's, gets in.
func runAdmin(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("admin: add EMAIL..., remove EMAIL..., list, or owner status|rotate")
	}
	if args[0] == "owner" {
		return runAdminOwner(ctx, args[1:])
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

// runAdminOwner is `lidza admin owner status|rotate`: the owner claim
// (AUTH_OWNER_CLAIM) of this app's database. status says whether the app
// waits for its owner and where the token file is (never the token);
// rotate replaces an unclaimed token, file and stored hash, at once for
// every node.
func runAdminOwner(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "status" && args[0] != "rotate") {
		return errors.New("admin owner: status or rotate")
	}
	fs := flags("admin owner " + args[0])
	dir := fs.String("dir", ".", "project directory")
	production := fs.Bool("production", false, "allow a database on another host")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	abs, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	var dbcfg db.Config
	if err := env.Load(abs, &dbcfg); err != nil {
		if values, verr := env.Values(abs); verr == nil && values["DATABASE_URL"] == "" {
			return errors.New("admin owner: DATABASE_URL is not set in .env, .env.<mode> or the environment; on the server, run it where the app's environment is")
		}
		return err
	}
	if host := remoteDBHost(dbcfg.URL); host != "" && !*production {
		return fmt.Errorf("admin owner: DATABASE_URL points at %s, not this machine; add --production to use that database", host)
	}
	var cfg auth.Config
	if err := env.Load(abs, &cfg); err != nil {
		return err
	}
	if !cfg.OwnerClaim {
		return errors.New("admin owner: the owner claim is off; set AUTH_OWNER_CLAIM=true (then the first account is the one that claims with the token)")
	}
	if cfg.OwnerClaimDir == "" {
		cfg.OwnerClaimDir = auth.OwnerClaimDir
	}
	if !filepath.IsAbs(cfg.OwnerClaimDir) {
		cfg.OwnerClaimDir = filepath.Join(abs, cfg.OwnerClaimDir)
	}
	pool, err := db.Open(ctx, dbcfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	a, err := auth.New(cfg, pool)
	if err != nil {
		return err
	}
	if args[0] == "rotate" {
		if err := a.RotateOwnerToken(ctx); err != nil {
			return fmt.Errorf("admin owner rotate: %w", err)
		}
		fmt.Printf("rotated: the new token is in %s (the old one no longer works)\n", a.OwnerClaimFile())
		return nil
	}
	st, err := a.OwnerClaimStatus(ctx)
	if err != nil {
		return err
	}
	switch {
	case st.State == "claimed":
		fmt.Printf("claimed %s\n", st.ClaimedAt.Format(time.RFC3339))
	case a.OwnerClaimFile() == "":
		fmt.Println("unclaimed: the token is AUTH_OWNER_CLAIM_TOKEN, from the deployment")
	case !fileExists(a.OwnerClaimFile()):
		fmt.Printf("unclaimed: the app writes the token to %s when it starts (or lidza admin owner rotate does now)\n", a.OwnerClaimFile())
	default:
		fmt.Printf("unclaimed: the token is in %s\n", a.OwnerClaimFile())
	}
	return nil
}
