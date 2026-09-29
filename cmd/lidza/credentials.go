package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/env"
)

// runCredentials is `lidza credentials init | set NAME=value ... | unset
// NAME ... | list | show [NAME] | edit`: the app's secrets, sealed in
// config/credentials.yml.enc with config/master.key. list, show and unset
// also cover the settings saved from the admin pages, which the db pack
// keeps in the app's database over the file.
func runCredentials(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("credentials: init, set NAME=value ..., unset NAME ..., list, show [NAME], or edit")
	}
	fs := flags("credentials " + args[0])
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	abs, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	rest := fs.Args()
	switch args[0] {
	case "init":
		created, err := credentials.Generate(abs)
		if err != nil {
			return err
		}
		if created {
			fmt.Printf("created %s (kept out of git) and %s; in production set %s to its contents\n", credentials.MasterKeyFile, credentials.File, credentials.EnvMasterKey)
		} else {
			fmt.Printf("%s exists; %s is in place\n", credentials.MasterKeyFile, credentials.File)
		}
		return nil
	case "set":
		values := map[string]string{}
		for _, kv := range rest {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("credentials set: %q is not NAME=value", kv)
			}
			values[k] = v
		}
		if len(values) == 0 {
			return errors.New("credentials set: give NAME=value pairs")
		}
		if err := ensureKey(abs); err != nil {
			return err
		}
		if err := credentials.Set(abs, values); err != nil {
			return err
		}
		fmt.Printf("sealed %d value(s) in %s; a running app reads them at its next start (the admin pages apply changes at once)\n", len(values), credentials.File)
		return nil
	case "unset":
		if len(rest) == 0 {
			return errors.New("credentials unset: give the names to remove")
		}
		if err := credentials.Unset(abs, rest...); err != nil {
			return err
		}
		fmt.Printf("removed %s from %s\n", strings.Join(rest, ", "), credentials.File)
		store, saved, done := savedSettings(ctx, abs)
		defer done()
		for _, n := range rest {
			if _, ok := saved[n]; !ok {
				continue
			}
			if err := store.Delete(ctx, n); err != nil {
				return err
			}
			fmt.Printf("removed %s from the settings saved from the admin pages\n", n)
		}
		return nil
	case "list":
		_, saved, done := savedSettings(ctx, abs)
		defer done()
		raw, err := credentials.Read(abs)
		if err != nil {
			return err
		}
		// As written: dev.NAME and production.NAME are one mode's.
		for _, n := range slices.Sorted(maps.Keys(raw)) {
			if _, ok := saved[n]; !ok {
				fmt.Println(n)
			}
		}
		for _, n := range slices.Sorted(maps.Keys(saved)) {
			fmt.Printf("%s (saved from the admin pages)\n", n)
		}
		return nil
	case "show":
		if len(rest) > 1 {
			return errors.New("credentials show: one NAME, or none for all of them")
		}
		vals, err := credentials.Read(abs)
		if err != nil {
			return err
		}
		_, saved, done := savedSettings(ctx, abs)
		defer done()
		if len(rest) == 0 {
			// Every value, decrypted, as edit shows the file.
			fmt.Print(credentials.Format(vals))
			if len(saved) > 0 {
				fmt.Print("\n# Saved from the admin pages, in the app's database; these win over the file.\n")
				fmt.Print(credentials.Format(saved))
			}
			return nil
		}
		if v, ok := saved[rest[0]]; ok {
			fmt.Println(v)
			return nil
		}
		// NAME is what this mode reads (dev: its section over the plain
		// value); dev.NAME is that entry as written.
		v, ok := vals[rest[0]]
		if !strings.Contains(rest[0], ".") {
			v, ok = credentials.Resolve(vals, credentials.Mode())[rest[0]]
		}
		if !ok {
			return fmt.Errorf("credentials: no %s", rest[0])
		}
		fmt.Println(v)
		return nil
	case "edit":
		if err := ensureKey(abs); err != nil {
			return err
		}
		vals, err := credentials.Read(abs)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp("", "lidza-credentials-*.yml")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		tmp.WriteString(credentials.Format(vals))
		tmp.Close()
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		cmd := exec.Command(editor, tmp.Name())
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", editor, err)
		}
		edited, err := os.ReadFile(tmp.Name())
		if err != nil {
			return err
		}
		parsed, err := credentials.Parse(string(edited))
		if err != nil {
			return err
		}
		if err := credentials.Write(abs, parsed); err != nil {
			return err
		}
		fmt.Printf("sealed %d value(s) in %s\n", len(parsed), credentials.File)
		return nil
	}
	return fmt.Errorf("credentials: unknown subcommand %q", args[0])
}

// savedSettings reads the settings saved from the admin pages: with the
// db pack they live, sealed, in the app database's credential table and
// win over the file. Empty when the app has no database, it is not
// reachable, or nothing was saved; done closes the connection.
func savedSettings(ctx context.Context, dir string) (store *db.CredentialStore, saved map[string]string, done func()) {
	done = func() {}
	values, err := env.Values(dir)
	if err != nil || values["DATABASE_URL"] == "" {
		return nil, nil, done
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pool, err := db.Open(cctx, db.Config{URL: values["DATABASE_URL"], MaxConns: 1, ConnectTimeout: 2 * time.Second})
	if err != nil {
		return nil, nil, done
	}
	var exists bool
	if err := pool.QueryRow(cctx, `SELECT to_regclass('credential') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		pool.Close()
		return nil, nil, done
	}
	store = db.NewCredentialStore(pool, dir)
	if err := store.Load(cctx); err != nil {
		fmt.Fprintf(os.Stderr, "note: the settings saved from the admin pages could not be read: %v\n", err)
		pool.Close()
		return nil, nil, done
	}
	return store, credentials.Overrides(), pool.Close
}

// ensureKey makes a master key when the app has none yet.
func ensureKey(dir string) error {
	if credentials.HasKey(dir) {
		return nil
	}
	created, err := credentials.Generate(dir)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("created %s (kept out of git)\n", filepath.FromSlash(credentials.MasterKeyFile))
	}
	return nil
}
