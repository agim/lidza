package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/credentials"
	"github.com/agim/lidza/pkg/pack"
)

// runShip is `lidza ship [--no-e2e] [--out bin/<name>]`: everything that
// must be true before a deploy, in order: lidza verify (generated files
// staged, check, Go tests), the browser suite against the built binary,
// the production build. It stops at the first failure.
func runShip(ctx context.Context, args []string) error {
	fs := flags("ship")
	dir := fs.String("dir", ".", "project directory")
	noE2E := fs.Bool("no-e2e", false, "skip the browser suite")
	out := fs.String("out", "", "output binary (default bin/<name>)")
	domains := fs.String("domains", "", "domains the deployed binary serves over TLS (LIDZA_TLS_DOMAINS), comma-separated; remembered in lidza.json")
	email := fs.String("email", "", "ACME account contact (LIDZA_TLS_EMAIL); remembered in lidza.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("ship needs a lidza.json project")
	}
	if *domains != "" || *email != "" {
		if *domains != "" {
			cfg.Deploy.Domains = splitDomains(*domains)
		}
		if *email != "" {
			cfg.Deploy.Email = *email
		}
		if err := cfg.Save(abs); err != nil {
			return err
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	stage := func(name string, args ...string) error {
		fmt.Printf("[ship] %s\n", name)
		if err := run(ctx, abs, exe, args...); err != nil {
			return fmt.Errorf("ship: %s failed", name)
		}
		return nil
	}
	if err := stage("lidza verify", "verify", "--dir", abs); err != nil {
		return err
	}
	if !*noE2E {
		if _, err := os.Stat(filepath.Join(abs, "playwright.config.ts")); err == nil {
			if err := stage("lidza test --e2e --install", "test", "--dir", abs, "--e2e", "--install"); err != nil {
				return err
			}
		} else {
			fmt.Println("[ship] no browser suite in this template (pages are tested in Go)")
		}
	}
	buildArgs := []string{"build", "--dir", abs}
	if *out != "" {
		buildArgs = append(buildArgs, "--out", *out)
	}
	if err := stage("lidza build", buildArgs...); err != nil {
		return err
	}
	bin := *out
	if bin == "" {
		bin = filepath.Join("bin", cfg.Name)
	}
	info, err := os.Stat(filepath.Join(abs, bin))
	if err != nil {
		return err
	}
	if bad := secretNames(cfg.Deploy.Env); len(bad) > 0 {
		return fmt.Errorf("ship: lidza.json's deploy.env holds %s, which look like secrets; lidza.json is committed, so seal them instead (lidza credentials set NAME=value) and remove them from deploy.env", strings.Join(bad, ", "))
	}
	envFile, err := writeProductionEnv(abs, cfg)
	if err != nil {
		return err
	}
	for _, line := range productionNeeds(cfg, deployValues(cfg, productionCredentials(abs))) {
		fmt.Println("[ship] set before deploying: " + line)
	}
	fmt.Printf("[ship] ready: %s (%.1f MB); %s carries the deployment settings\n", bin, float64(info.Size())/(1<<20), envFile)
	if len(cfg.Deploy.Domains) > 0 {
		fmt.Printf("[ship] serves https://%s (ports 80 and 443; the DNS record and the master key are the operator's: docs/deploy.md)\n", cfg.Deploy.Domains[0])
	} else {
		fmt.Println("[ship] no domain recorded: lidza ship --domains app.example.com makes the binary serve TLS itself; otherwise put a proxy in front")
	}
	fmt.Printf("[ship] systemd: copy %s to /opt/%s/.env with DATABASE_URL and LIDZA_MASTER_KEY added, then deploy/%s.service\n", envFile, cfg.Name, cfg.Name)
	fmt.Printf("[ship] container: docker build -t %s . && docker run --env-file %s -p 80:80 -p 443:443 %s\n", cfg.Name, envFile, cfg.Name)
	return nil
}

// splitDomains normalizes a comma-separated list.
func splitDomains(raw string) []string {
	var out []string
	for _, d := range strings.Split(raw, ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// writeProductionEnv writes deploy/production.env from lidza.json: the
// settings a deployment needs that are not secrets (domains, log format,
// migrations at start). The operator adds DATABASE_URL and
// LIDZA_MASTER_KEY where the process runs. It is rewritten on every ship.
func writeProductionEnv(dir string, cfg *config.Config) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Deployment settings of %s, written by lidza ship from lidza.json (deploy).\n", cfg.Name)
	b.WriteString("# Add DATABASE_URL and LIDZA_MASTER_KEY (the contents of config/master.key) where\n# the process runs; never commit them.\n")
	if len(cfg.Deploy.Domains) > 0 {
		fmt.Fprintf(&b, "LIDZA_TLS_DOMAINS=%s\n", strings.Join(cfg.Deploy.Domains, ","))
		if cfg.Deploy.Email != "" {
			fmt.Fprintf(&b, "LIDZA_TLS_EMAIL=%s\n", cfg.Deploy.Email)
		}
	} else {
		b.WriteString("# LIDZA_TLS_DOMAINS=app.example.com   # lidza ship --domains records it here\n")
		b.WriteString("LIDZA_ADDR=0.0.0.0:3000\n")
	}
	b.WriteString("LIDZA_LOG=json\nDB_MIGRATE=true\n")
	if len(cfg.Deploy.Env) > 0 {
		b.WriteString("\n# The app's production settings, from lidza.json (deploy.env):\n")
		names := make([]string, 0, len(cfg.Deploy.Env))
		for k := range cfg.Deploy.Env {
			names = append(names, k)
		}
		slices.Sort(names)
		for _, k := range names {
			fmt.Fprintf(&b, "%s=%s\n", k, cfg.Deploy.Env[k])
		}
	}
	if needs := productionNeeds(cfg, deployValues(cfg, productionCredentials(dir))); len(needs) > 0 {
		b.WriteString("\n# The enabled packs need these, and neither the credentials nor lidza.json's\n# deploy.env hold them (lidza credentials set NAME=value for a secret,\n# deploy.env for a setting, or the environment where the process runs):\n")
		for _, line := range needs {
			b.WriteString("# " + line + "\n")
		}
	}
	p := filepath.Join(dir, "deploy", "production.env")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return filepath.Join("deploy", "production.env"), os.WriteFile(p, []byte(b.String()), 0o644)
}

// deployValues are what production will have: the sealed credentials,
// with lidza.json's deploy.env over them.
func deployValues(cfg *config.Config, sealed map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range sealed {
		out[k] = v
	}
	for k, v := range cfg.Deploy.Env {
		out[k] = v
	}
	return out
}

// productionNeeds lists the settings the enabled official packs need in
// production that sealed holds no usable value for: missing, or set to
// a development-only value (the log mailer, the fake model).
// DATABASE_URL is left out: the operator adds it with the master key.
func productionNeeds(cfg *config.Config, sealed map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range cfg.Packs {
		short, ok := strings.CutPrefix(name, pack.OfficialPrefix)
		if !ok {
			continue
		}
		for _, o := range pack.Officials {
			if o.Name != short {
				continue
			}
			for _, s := range o.Production {
				if s.Name == "DATABASE_URL" || seen[s.Name] {
					continue
				}
				seen[s.Name] = true
				v := strings.TrimSpace(sealed[s.Name])
				switch {
				case v == "" && s.Optional:
				case v == "":
					out = append(out, fmt.Sprintf("%s (%s): %s", s.Name, name, s.Why))
				case slices.Contains(s.Dev, strings.ToLower(v)):
					out = append(out, fmt.Sprintf("%s (%s) is %q, which suits development only: %s", s.Name, name, v, s.Why))
				}
			}
		}
	}
	return out
}

// secretNames lists the names of env that look like secrets: keys,
// tokens, passwords, secrets, a database address with its password.
func secretNames(env map[string]string) []string {
	var out []string
	for k := range env {
		u := strings.ToUpper(k)
		for _, w := range []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "DATABASE_URL"} {
			if strings.Contains(u, w) {
				out = append(out, k)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// productionCredentials is what production reads from the sealed file:
// the plain values and the production: section, whatever mode the CLI
// runs in.
func productionCredentials(dir string) map[string]string {
	raw, err := credentials.Read(dir)
	if err != nil {
		return map[string]string{}
	}
	return credentials.Resolve(raw, "production")
}
