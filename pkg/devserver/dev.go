package devserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agim/lidza/pkg/config"
)

// Options configures a `lidza dev` run.
type Options struct {
	// Addr is where the app listens; the frontend is proxied behind it.
	Addr string
	// Out receives the interleaved, prefixed output of every process.
	Out io.Writer
	// Poll is how often the Go sources are checked for changes.
	Poll time.Duration
	// BeforeBuild runs before every build. `lidza dev` uses it to run the
	// schema generators; an error is logged and the build still runs.
	BeforeBuild func() error
	// AfterBuild runs after every successful build of the app, before the
	// restart. `lidza dev` uses it to refresh the agent context files and
	// the generated client.
	AfterBuild func()
	// Watch lists extra files or directories, relative to the project
	// root, whose change triggers a rebuild besides the Go sources.
	Watch []string
	// Rewatch, when set, is called after every build for the new list of
	// extra files (lidza.json may have gained a pack whose sources are
	// now watched); it replaces Watch.
	Rewatch func() []string
}

// RestartFile, under the project, restarts the app under `lidza dev`
// without a rebuild when its modification time changes. `lidza db
// migrate` and `rollback` touch it (RequestRestart), so the app does not
// keep running against the schema it started with.
var RestartFile = filepath.Join(BuildDir, "restart")

// RequestRestart asks a `lidza dev` running in dir to restart the app. It
// is a no-op in effect when none runs.
func RequestRestart(dir string) error {
	p := filepath.Join(dir, RestartFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(time.Now().Format(time.RFC3339Nano)+"\n"), 0o644)
}

// Environment the app binary reads, in dev (set by `lidza dev`) and in
// production.
const (
	EnvMode        = "LIDZA_MODE"         // "dev" or unset
	EnvAddr        = "LIDZA_ADDR"         // listen address
	EnvFrontendURL = "LIDZA_FRONTEND_URL" // dev only: the dev server to proxy to
)

// BuildDir holds the dev build of the app binary, inside the project.
const BuildDir = ".lidza"

const stopGrace = 3 * time.Second

// Dev runs the project in cfg.Dir until ctx is cancelled: starts the frontend
// dev server if the template has one, builds the app binary, runs it in dev
// mode, and rebuilds and restarts it whenever a Go source file changes.
func Dev(ctx context.Context, cfg *config.Config, opt Options) error {
	if opt.Out == nil {
		opt.Out = os.Stdout
	}
	if opt.Poll == 0 {
		opt.Poll = 500 * time.Millisecond
	}
	if opt.Addr == "" {
		opt.Addr = "127.0.0.1:3000"
	}
	if err := checkPortFree(opt.Addr); err != nil {
		return err
	}
	logf := func(format string, a ...any) { fmt.Fprintf(opt.Out, "[lidza] "+format+"\n", a...) }

	if cfg.Frontend.HasDevServer() {
		if err := EnsureNodeModules(ctx, cfg.Dir, opt.Out); err != nil {
			return err
		}
		cmd := exec.Command("sh", "-c", cfg.Frontend.Dev)
		cmd.Dir = cfg.Dir
		cmd.Env = append(os.Environ(), "FORCE_COLOR=0", "BROWSER=none")
		web, err := startProc(cmd, opt.Out, "[web]   ")
		if err != nil {
			return fmt.Errorf("frontend dev server: %w", err)
		}
		defer web.stop(stopGrace)
		logf("frontend: %s  (%s)", cfg.Frontend.Dev, cfg.Frontend.URL)
	}

	app := &appProcess{
		cfg: cfg,
		out: opt.Out,
		env: append(os.Environ(),
			EnvMode+"=dev",
			EnvAddr+"="+opt.Addr,
			EnvFrontendURL+"="+cfg.Frontend.URL,
		),
	}
	defer app.stop()

	afterBuild := func() {
		if opt.AfterBuild != nil {
			opt.AfterBuild()
		}
	}
	build := func() error {
		if opt.BeforeBuild != nil {
			if err := opt.BeforeBuild(); err != nil {
				logf("%v", err)
			}
		}
		return app.build(ctx)
	}
	if err := build(); err != nil {
		logf("build failed; fix the errors above, watching for changes")
	} else {
		afterBuild()
		if err := app.start(); err != nil {
			return err
		}
		logf("app: http://%s  (API under /api, frontend proxied from %s)", opt.Addr, cfg.Frontend.URL)
	}

	w := newWatcher(cfg.Dir, opt.Watch...)
	w.scan()
	restart := restartStamp(cfg.Dir)
	ticker := time.NewTicker(opt.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logf("stopping")
			return nil
		case <-ticker.C:
			if stamp := restartStamp(cfg.Dir); !stamp.Equal(restart) {
				restart = stamp
				// A running app restarts; one whose build failed waits for
				// the next change.
				if app.p != nil {
					app.stop()
					if err := app.start(); err != nil {
						logf("restart failed: %v", err)
					} else {
						logf("restarted (the database changed)")
					}
				}
				continue
			}
			if !w.scan() {
				continue
			}
			logf("sources changed, rebuilding")
			if err := build(); err != nil {
				logf("build failed; keeping the previous binary running")
				continue
			}
			afterBuild()
			if opt.Rewatch != nil {
				w = newWatcher(cfg.Dir, opt.Rewatch()...)
			}
			// Generators may have written Go files; record them so they do
			// not count as a second change.
			w.scan()
			app.stop()
			if err := app.start(); err != nil {
				logf("restart failed: %v", err)
			} else {
				logf("restarted")
			}
		}
	}
}

func restartStamp(dir string) time.Time {
	if info, err := os.Stat(filepath.Join(dir, RestartFile)); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// appProcess is the app binary under `lidza dev`: built into .lidza/app and
// restarted after each successful rebuild.
type appProcess struct {
	cfg *config.Config
	env []string
	out io.Writer
	p   *proc
}

func (a *appProcess) binPath() string {
	return filepath.Join(a.cfg.Dir, BuildDir, "app")
}

func (a *appProcess) build(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(a.cfg.Dir, BuildDir), 0o755); err != nil {
		return err
	}
	return runPrefixed(ctx, a.cfg.Dir, a.out, "[go]    ", "go", "build", "-o", a.binPath(), ".")
}

func (a *appProcess) start() error {
	cmd := exec.Command(a.binPath())
	cmd.Dir = a.cfg.Dir
	cmd.Env = a.env
	p, err := startProc(cmd, a.out, "[app]   ")
	if err != nil {
		return err
	}
	a.p = p
	return nil
}

func (a *appProcess) stop() {
	if a.p != nil {
		a.p.stop(stopGrace)
		a.p = nil
	}
}

// runPrefixed runs a command to completion with its output prefixed.
func runPrefixed(ctx context.Context, dir string, out io.Writer, prefix string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	w := prefixWriter(out, prefix)
	defer w.Close()
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

// KeepDist restores dist/.gitkeep after a frontend build: the bundler
// empties the directory, and the Go embed of dist needs it to exist in a
// fresh clone, so the placeholder stays tracked.
func KeepDist(dir, dist string) error {
	if dist == "" {
		return nil
	}
	p := filepath.Join(dir, dist, ".gitkeep")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, nil, 0o644)
}

// EnsureNodeModules runs `npm install` in dir when it has a package.json
// whose dependencies are not all in node_modules: a fresh clone, or a
// pull that added packages (another session's, a teammate's).
func EnsureNodeModules(ctx context.Context, dir string, out io.Writer) error {
	missing, err := missingPackages(dir)
	if err != nil || len(missing) == 0 {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		fmt.Fprintln(out, "[lidza] node_modules missing, running npm install")
	} else {
		more := ""
		if len(missing) > 3 {
			missing, more = missing[:3], fmt.Sprintf(" and %d more", len(missing)-3)
		}
		fmt.Fprintf(out, "[lidza] package.json needs %s%s, not installed: running npm install\n", strings.Join(missing, ", "), more)
	}
	if err := runPrefixed(ctx, dir, out, "[npm]   ", "npm", "install", "--no-fund", "--no-audit"); err != nil {
		return fmt.Errorf("npm install: %w", err)
	}
	return nil
}

// missingPackages lists the dependencies and devDependencies of dir's
// package.json with no node_modules/<name>/package.json; none without a
// package.json.
func missingPackages(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	var missing []string
	for _, deps := range []map[string]string{pkg.Dependencies, pkg.DevDependencies} {
		for name := range deps {
			if _, err := os.Stat(filepath.Join(dir, "node_modules", filepath.FromSlash(name), "package.json")); err != nil {
				missing = append(missing, name)
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func checkPortFree(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w (is another lidza dev running?)", addr, err)
	}
	return l.Close()
}

// watcher polls the modification times of the Go sources in a project. A
// poll keeps the dev server dependency-free and behaves the same on every
// filesystem; the tree is small enough that a 500ms scan is cheap.
type watcher struct {
	root      string
	extra     map[string]bool // files watched besides Go sources
	extraDirs []string        // directories whose every file is watched
	mtime     map[string]time.Time
}

func newWatcher(root string, extra ...string) *watcher {
	w := &watcher{root: root, extra: map[string]bool{}, mtime: map[string]time.Time{}}
	for _, e := range extra {
		p := filepath.Join(root, e)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			w.extraDirs = append(w.extraDirs, p+string(filepath.Separator))
		} else {
			w.extra[p] = true
		}
	}
	return w
}

func (w *watcher) watched(p string) bool {
	name := filepath.Base(p)
	if strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" || w.extra[p] {
		return true
	}
	for _, d := range w.extraDirs {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}

var skipDirs = map[string]bool{
	"node_modules": true, "dist": true, "bin": true, "target": true,
}

// scan walks the tree and reports whether any watched file changed since the
// last scan (the first scan only records state).
func (w *watcher) scan() bool {
	seen := make(map[string]time.Time, len(w.mtime))
	first := len(w.mtime) == 0
	changed := false
	_ = filepath.WalkDir(w.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != w.root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !w.watched(p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[p] = info.ModTime()
		if old, ok := w.mtime[p]; !ok || !old.Equal(info.ModTime()) {
			changed = true
		}
		return nil
	})
	if len(seen) != len(w.mtime) {
		changed = true
	}
	w.mtime = seen
	return changed && !first
}

// NewestSource is the latest modification time of the Go sources in dir
// (*.go, go.mod, go.sum), skipping what the dev watcher skips. `lidza mcp`
// compares it with its app build to rebuild a stale one.
func NewestSource(dir string) time.Time {
	w := newWatcher(dir)
	w.scan()
	var newest time.Time
	for _, t := range w.mtime {
		if t.After(newest) {
			newest = t
		}
	}
	return newest
}
