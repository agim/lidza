package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/agim/lidza/pkg/credentials"
)

// runCredentials is `lidza credentials init | set NAME=value ... | unset
// NAME ... | list | show NAME | edit`: the app's secrets, sealed in
// config/credentials.yml.enc with config/master.key.
func runCredentials(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("credentials: init, set NAME=value ..., unset NAME ..., list, show NAME, or edit")
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
		return nil
	case "list":
		for _, n := range credentials.Names(abs) {
			fmt.Println(n)
		}
		return nil
	case "show":
		if len(rest) != 1 {
			return errors.New("credentials show: NAME")
		}
		vals, err := credentials.Read(abs)
		if err != nil {
			return err
		}
		v, ok := vals[rest[0]]
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
