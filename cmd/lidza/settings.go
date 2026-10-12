package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/env"
)

const settingsUsage = `usage:
  lidza settings list [--json]               the app settings: where each one's value comes from (names, never values)
  lidza settings get NAME                    a value saved in the app's database
  lidza settings set NAME=value ...          save app settings in the app's database (sealed with the master key)
  lidza settings unset NAME ...              remove saved values
  lidza settings import [--dry-run] [--overwrite]
                                             copy the app settings .env, .env.<mode>, deploy/production.env,
                                             the credentials file and this process's environment hold into the database
Every subcommand takes --production (the production database: LIDZA_MODE=production,
and allowed on another host) and --dir. App settings are the app's configuration (keys,
providers, addresses, senders): kept in its database they move with it, and a new
server's environment needs only the database, the master key and tuning. The admin
pages save the same values. An environment value still wins over a saved one.
`

func runSettings(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, settingsUsage)
		return errors.New("settings: subcommand required")
	}
	sub, rest := args[0], args[1:]
	fs := flags("settings " + sub)
	dir := fs.String("dir", ".", "project directory")
	production := fs.Bool("production", false, "the production database (LIDZA_MODE=production), also on another host")
	asJSON := fs.Bool("json", false, "list: JSON")
	dry := fs.Bool("dry-run", false, "import: say what would be imported, change nothing")
	overwrite := fs.Bool("overwrite", false, "import: replace values already saved")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if *production {
		os.Setenv(devserver.EnvMode, "production")
	} else if os.Getenv(devserver.EnvMode) == "" {
		os.Setenv(devserver.EnvMode, "dev")
	}
	store, done, err := openSettings(ctx, abs, sub, *production)
	if err != nil {
		return err
	}
	defer done()
	switch sub {
	case "list":
		return settingsList(abs, *asJSON)
	case "get":
		if fs.NArg() != 1 {
			return errors.New("settings get NAME")
		}
		v, ok := credentials.Overrides()[fs.Arg(0)]
		if !ok {
			return fmt.Errorf("settings: %s is not saved in the app's database (lidza settings list says where it comes from)", fs.Arg(0))
		}
		fmt.Println(v)
		return nil
	case "set":
		values, err := settingPairs(fs)
		if err != nil {
			return err
		}
		for _, k := range sortedKeys(values) {
			if err := store.Save(ctx, k, values[k]); err != nil {
				return err
			}
		}
		fmt.Printf("saved %s in the app's database; the running app reads them at its next start (the admin pages apply at once)\n", strings.Join(sortedKeys(values), ", "))
		warnShadowed(abs, sortedKeys(values))
		return nil
	case "unset":
		if fs.NArg() == 0 {
			return errors.New("settings unset NAME [NAME...]")
		}
		for _, k := range fs.Args() {
			if err := store.Delete(ctx, k); err != nil {
				return err
			}
		}
		fmt.Printf("removed %s from the app's database\n", strings.Join(fs.Args(), ", "))
		return nil
	case "import":
		return settingsImport(ctx, abs, store, *dry, *overwrite)
	}
	fmt.Fprint(os.Stderr, settingsUsage)
	return fmt.Errorf("settings: unknown subcommand %q", sub)
}

// openSettings connects to the mode's database and loads what it holds.
func openSettings(ctx context.Context, dir, sub string, production bool) (*db.CredentialStore, func(), error) {
	var cfg db.Config
	if err := env.Load(dir, &cfg); err != nil {
		return nil, nil, fmt.Errorf("settings %s: %w (the app's database: DATABASE_URL)", sub, err)
	}
	if sub != "list" && sub != "get" && !production {
		if host := remoteDBHost(cfg.URL); host != "" {
			return nil, nil, fmt.Errorf("settings %s: DATABASE_URL points at %s, not this machine; run lidza settings %s --production to change that database", sub, host, sub)
		}
	}
	if _, err := credentials.Key(dir); err != nil {
		return nil, nil, fmt.Errorf("settings %s: no master key (%s or config/master.key): saved settings are sealed with it", sub, credentials.EnvMasterKey)
	}
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	store := db.NewCredentialStore(pool, dir)
	if err := store.Load(ctx); err != nil {
		pool.Close()
		return nil, nil, err
	}
	return store, pool.Close, nil
}

type settingRow struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
}

// settingsList shows each app setting and the layer its value comes
// from; values never.
func settingsList(dir string, asJSON bool) error {
	origins, err := env.Origins(dir)
	if err != nil {
		return err
	}
	var rows []settingRow
	for name, origin := range origins {
		if env.AppSetting(name) {
			rows = append(rows, settingRow{name, origin})
		}
	}
	slices.SortFunc(rows, func(a, b settingRow) int { return strings.Compare(a.Name, b.Name) })
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": env.Mode(), "settings": rows})
	}
	if len(rows) == 0 {
		fmt.Println("no app settings are set; defaults apply")
		return nil
	}
	for _, r := range rows {
		where := r.Origin
		switch r.Origin {
		case env.OriginSaved:
			where = "saved in the app's database"
		case env.OriginCredentials:
			where = "credentials file (development seed)"
		case env.OriginProcess:
			where = "process environment: lidza settings import moves it"
		default:
			where += ": lidza settings import moves it"
		}
		fmt.Printf("  %-32s %s\n", r.Name, where)
	}
	return nil
}

// settingPairs reads NAME=value arguments; infrastructure settings are
// refused: they are the deployment's.
func settingPairs(fs *flag.FlagSet) (map[string]string, error) {
	if fs.NArg() == 0 {
		return nil, errors.New("settings set NAME=value [NAME=value...]")
	}
	values := map[string]string{}
	for _, a := range fs.Args() {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("settings set: %q is not NAME=value", a)
		}
		if env.Kind(k) == env.KindInfra {
			return nil, fmt.Errorf("settings set: %s is the deployment's (the database, the master key, addresses, tuning): set it in the environment where the app runs", k)
		}
		values[k] = v
	}
	return values, nil
}

// warnShadowed says when the environment would still win.
func warnShadowed(dir string, names []string) {
	for _, k := range names {
		if _, ok := os.LookupEnv(k); ok {
			fmt.Printf("note: %s is also set in this process's environment, which wins over the saved value; remove it there\n", k)
		}
	}
}

// settingsImport copies the app settings other layers supply into the
// database.
func settingsImport(ctx context.Context, dir string, store *db.CredentialStore, dry, overwrite bool) error {
	origins, err := env.Origins(dir)
	if err != nil {
		return err
	}
	values, err := env.Values(dir)
	if err != nil {
		return err
	}
	// deploy/production.env is read by the deployment, not by the app:
	// its app settings are imported too when importing for production.
	if env.Mode() == "production" {
		extra := map[string]string{}
		if data, err := os.ReadFile(filepath.Join(dir, "deploy", "production.env")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				t := strings.TrimSpace(line)
				if k, v, ok := strings.Cut(t, "="); ok && !strings.HasPrefix(t, "#") {
					extra[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
				}
			}
		}
		for k, v := range extra {
			if _, ok := origins[k]; !ok {
				origins[k], values[k] = "deploy/production.env", v
			}
		}
	}
	saved := credentials.Overrides()
	var moved, skipped []string
	for _, name := range sortedKeys(origins) {
		origin := origins[name]
		if !env.AppSetting(name) || origin == env.OriginSaved || values[name] == "" {
			continue
		}
		if _, ok := saved[name]; ok && !overwrite {
			skipped = append(skipped, name)
			continue
		}
		if !dry {
			if err := store.Save(ctx, name, values[name]); err != nil {
				return err
			}
		}
		moved = append(moved, name+" (from "+origin+")")
	}
	verb := "imported"
	if dry {
		verb = "would import"
	}
	if len(moved) == 0 {
		fmt.Println("nothing to import: every app setting is saved in the app's database already, or none is set")
	} else {
		fmt.Printf("%s into the app's database (%s): %s\n", verb, env.Mode(), strings.Join(moved, ", "))
		fmt.Println("then remove them from the server's environment and the .env files; an environment value still wins over the saved one")
	}
	if len(skipped) > 0 {
		fmt.Printf("already saved, kept (--overwrite replaces): %s\n", strings.Join(skipped, ", "))
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
