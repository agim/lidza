package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/agim/lidza/pkg/pack"
)

// runDoctor reports the toolchain, the services and, inside a project,
// the app's own prerequisites: node_modules, built packs, the browser for
// end-to-end tests. Every missing item comes with the command that fixes
// it. Exit 1 when a required item is missing.
func runDoctor(ctx context.Context, args []string) error {
	fs := flags("doctor")
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	missing := 0
	ok := func(s string) { fmt.Printf("  [ok]   %s\n", s) }
	todo := func(s, fix string) {
		missing++
		fmt.Printf("  [todo] %s\n         %s\n", s, fix)
	}
	note := func(s string) { fmt.Printf("  [--]   %s\n", s) }

	fmt.Println("Toolchain:")
	tool := func(name, versionArg, fix string, required bool) {
		p, err := exec.LookPath(name)
		if err != nil {
			if required {
				todo(name+": not installed", fix)
			} else {
				note(name + ": not installed (" + fix + ")")
			}
			return
		}
		out, _ := exec.CommandContext(ctx, name, versionArg).CombinedOutput()
		v := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if len(v) > 60 {
			v = v[:60]
		}
		ok(fmt.Sprintf("%s %s (%s)", name, v, p))
	}
	tool("go", "version", "sh install.sh", true)
	tool("cargo", "--version", "sh install.sh", true)
	if out, err := exec.CommandContext(ctx, "rustup", "target", "list", "--installed").Output(); err == nil {
		for _, t := range []string{"wasm32-wasip1", "wasm32-unknown-unknown"} {
			if strings.Contains(string(out), t) {
				ok("rust target " + t)
			} else {
				todo("rust target "+t, "rustup target add "+t)
			}
		}
	}
	tool("node", "--version", "sh install.sh (installs Node 22 into ~/.local/opt)", true)
	tool("staticcheck", "-version", "go install honnef.co/go/tools/cmd/staticcheck@latest", false)
	tool("sqlc", "version", "go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest", false)
	tool("wasm-tools", "--version", "cargo install wasm-tools --locked", false)
	tool("k6", "version", "see docs/environment.md (needed by lidza benchmark)", false)

	fmt.Println("Services (Postgres for db, auth, jobs, mail, analytics; Valkey for cache, realtime):")
	for _, s := range []struct{ name, addr, fix string }{
		{"postgres", "127.0.0.1:5432", "sh install.sh --services, or " + serviceHint("postgres")},
		{"valkey", "127.0.0.1:6379", "sh install.sh --services, or " + serviceHint("valkey")},
	} {
		conn, err := net.DialTimeout("tcp", s.addr, time.Second)
		if err != nil {
			note(s.name + " not reachable on " + s.addr + " (" + s.fix + ")")
			continue
		}
		conn.Close()
		ok(s.name + " on " + s.addr)
	}

	abs, cfg, err := loadProject(*dir)
	if err == nil && cfg != nil {
		fmt.Printf("Project %s:\n", cfg.Name)
		if cfg.Frontend.Dist != "" {
			if _, err := os.Stat(filepath.Join(abs, "node_modules")); err == nil {
				ok("node_modules")
			} else {
				todo("node_modules missing", "npm install")
			}
		}
		for _, name := range cfg.Packs {
			if pack.IsOfficialGo(name) {
				continue
			}
			m, err := pack.Load(abs, name)
			switch {
			case err != nil:
				todo("pack "+name+": "+err.Error(), "lidza pack list")
			case pack.NeedsBuild(abs, m):
				todo("pack "+name+" not built", "lidza pack build "+name)
			default:
				ok("pack " + name + " built")
			}
		}
		if _, err := os.Stat(filepath.Join(abs, "playwright.config.ts")); err == nil {
			exe, err := browserPath(ctx, abs)
			switch {
			case err != nil:
				todo("playwright: "+err.Error(), "npm install")
			case !fileExists(exe):
				todo("browser for e2e tests missing ("+exe+")", "npx playwright install --with-deps chromium")
			default:
				ok("browser for e2e tests: " + exe)
			}
		}
	}
	if missing > 0 {
		return fmt.Errorf("%d item(s) to fix", missing)
	}
	fmt.Println("everything in place")
	return nil
}

// browserPath asks the app's Playwright where its Chromium build lives.
// The answer is a path that may not exist yet; the caller checks.
func browserPath(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "node", "-e", "const {chromium} = require('playwright'); process.stdout.write(chromium.executablePath())")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("playwright is not installed in node_modules")
	}
	return strings.TrimSpace(string(out)), nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// serviceHint is the one-line install of a service for this machine's
// package manager; the installer's --services does the same with the
// service started and the database role created.
func serviceHint(service string) string {
	pm := ""
	switch {
	case runtime.GOOS == "darwin":
		pm = "brew"
	case lookPath("apt-get"):
		pm = "apt"
	case lookPath("dnf"):
		pm = "dnf"
	case lookPath("pacman"):
		pm = "pacman"
	}
	hints := map[string]string{
		"apt:postgres":    "sudo apt-get install -y postgresql && sudo -u postgres createuser -s $USER",
		"dnf:postgres":    "sudo dnf install -y postgresql-server && sudo postgresql-setup --initdb && sudo systemctl enable --now postgresql && sudo -u postgres createuser -s $USER",
		"pacman:postgres": "sudo pacman -S postgresql && sudo -u postgres initdb -D /var/lib/postgres/data && sudo systemctl enable --now postgresql && sudo -u postgres createuser -s $USER",
		"brew:postgres":   "brew install postgresql@17 && brew services start postgresql@17",
		"apt:valkey":      "sudo apt-get install -y valkey-server (or redis-server)",
		"dnf:valkey":      "sudo dnf install -y valkey && sudo systemctl enable --now valkey",
		"pacman:valkey":   "sudo pacman -S valkey && sudo systemctl enable --now valkey",
		"brew:valkey":     "brew install valkey && brew services start valkey",
	}
	if h, ok := hints[pm+":"+service]; ok {
		return h
	}
	if service == "postgres" {
		return "install Postgres 15+ and create a superuser role named after your user"
	}
	return "install Valkey or Redis on 6379 (docker run -d -p 6379:6379 valkey/valkey:8)"
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
