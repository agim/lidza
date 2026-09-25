// Package lidza is the runtime a Līdza application links against. The app's
// main.go embeds its frontend build and calls Run; `lidza dev` starts the
// same binary in dev mode with the frontend proxied from its dev server.
package lidza

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/router"
	"github.com/agim/lidza/pkg/telemetry"
)

// DefaultTimeout is the per-request deadline for API handlers.
const DefaultTimeout = 30 * time.Second

// App describes one application.
type App struct {
	// Name is shown in logs.
	Name string
	// Dist is the built frontend, served in production. Nil for templates
	// without a static build.
	Dist fs.FS
	// Frontend serves every non-API path when there is no Dist (the htmx
	// template renders pages in Go). Ignored while the dev proxy is active.
	Frontend http.Handler
	// Routes registers the app's API handlers. May be nil.
	Routes func(r *router.Router)
	// Middleware wraps the whole app, API and frontend alike, outermost
	// first. The API router already runs request ids, logging, panic
	// recovery and the timeout; add CORS or auth here or with router.Use.
	Middleware []middleware.Middleware
	// Timeout is the deadline every API request's context gets; default
	// DefaultTimeout.
	Timeout time.Duration
	// CSP is the Content-Security-Policy sent with every response. Empty
	// sends none; Vite's dev server needs inline scripts, so set it for
	// production builds only.
	CSP string
	// Logger receives request and error logs; default slog.Default().
	Logger *slog.Logger
	// Packs are started in order before OnStart and stopped in reverse
	// after OnShutdown. packs.go, generated from lidza.json, lists them.
	Packs []Pack

	// OnStart runs after the packs and before the listener opens: connect
	// what the packs do not, warm caches, Provide services. An error
	// aborts the start.
	OnStart func(ctx context.Context, s *Services) error
	// OnReady runs once the app is listening.
	OnReady func()
	// OnShutdown runs after in-flight requests finished, before exit:
	// close pools, flush queues.
	OnShutdown func(ctx context.Context) error

	// ReadyTimeout bounds the checks behind /readyz; default 3s.
	ReadyTimeout time.Duration
}

// Paths every app serves besides /api and the frontend.
const (
	MetricsPath = "/metrics"
	HealthzPath = "/healthz"
	ReadyzPath  = "/readyz"
)

// Run serves the app until SIGINT or SIGTERM and exits the process with a
// non-zero status on error. Configuration comes from the environment:
//
//	LIDZA_ADDR          listen address, default 127.0.0.1:3000
//	LIDZA_MODE          "dev" to proxy the frontend instead of serving Dist
//	LIDZA_FRONTEND_URL  the frontend dev server (dev mode; set by `lidza dev`)
func Run(app App) {
	if err := Serve(context.Background(), app); err != nil {
		fmt.Fprintln(os.Stderr, "lidza:", err)
		os.Exit(1)
	}
}

// Booted is an app with its packs started and its handler built, not yet
// listening: what Serve runs and what tests drive through lidzatest.
type Booted struct {
	Handler  http.Handler
	Services *Services
	app      App
	started  []Pack
	sidecar  *devserver.Sidecar
}

// Boot starts the packs in order, runs OnStart and builds the handler.
// Close stops everything in reverse.
func Boot(ctx context.Context, app App) (*Booted, error) {
	services := NewServices()
	h, sidecar, err := handler(app, services)
	if err != nil {
		return nil, err
	}
	b := &Booted{Handler: h, Services: services, app: app, sidecar: sidecar}
	if sidecar != nil {
		if err := sidecar.Start(ctx); err != nil {
			return nil, fmt.Errorf("ssr sidecar: %w", err)
		}
	}
	for _, p := range app.Packs {
		if err := p.Start(ctx, services); err != nil {
			b.Close(ctx)
			return nil, fmt.Errorf("pack %s: %w", p.Name(), err)
		}
		b.started = append(b.started, p)
	}
	if app.OnStart != nil {
		if err := app.OnStart(ctx, services); err != nil {
			b.Close(ctx)
			return nil, fmt.Errorf("start: %w", err)
		}
	}
	return b, nil
}

// Close runs OnShutdown, then stops the packs in reverse order. Every
// error is logged; the first is returned.
func (b *Booted) Close(ctx context.Context) error {
	log := b.app.logger()
	var first error
	if b.app.OnShutdown != nil && len(b.started) == len(b.app.Packs) {
		if err := b.app.OnShutdown(ctx); err != nil {
			first = fmt.Errorf("shutdown: %w", err)
		}
	}
	for i := len(b.started) - 1; i >= 0; i-- {
		if err := b.started[i].Stop(ctx); err != nil {
			log.Error("pack stop", "pack", b.started[i].Name(), "error", err)
			if first == nil {
				first = err
			}
		}
	}
	b.started = nil
	if b.sidecar != nil {
		if err := b.sidecar.Stop(ctx); err != nil && first == nil {
			first = err
		}
		b.sidecar = nil
	}
	return first
}

// Serve is Run without the process exit: it blocks until ctx is cancelled or
// a termination signal arrives, then shuts the server down.
func Serve(ctx context.Context, app App) error {
	log := app.logger()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	booted, err := Boot(ctx, app)
	if err != nil {
		return err
	}
	h := booted.Handler
	addr := envOr(devserver.EnvAddr, "127.0.0.1:3000")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		booted.Close(ctx)
		return err
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	mode := "production"
	if os.Getenv(devserver.EnvMode) == "dev" {
		mode = "dev"
	}
	log.Info("listening", "app", name(app), "addr", "http://"+ln.Addr().String(), "mode", mode)
	if app.OnReady != nil {
		app.OnReady()
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		booted.Close(shutdown)
		return err
	}
	return booted.Close(shutdown)
}

// Handler builds the app's http.Handler: the API router under /api with the
// standard pipeline and, for every other path, the proxy to the frontend
// dev server (dev mode), the embedded build, or the app's own Frontend.
// Packs are not started; Serve does that.
func Handler(app App) (http.Handler, error) {
	h, _, err := handler(app, NewServices())
	return h, err
}

// handler also returns the SSR sidecar when LIDZA_SSR=1 asks for one;
// Boot starts it.
func handler(app App, services *Services) (http.Handler, *devserver.Sidecar, error) {
	log := app.logger()
	r := router.New()
	if app.Routes != nil {
		app.Routes(r)
	}
	timeout := app.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	tel := telemetry.New(services.Each)
	chain := []middleware.Middleware{
		middleware.RequestID(),
		middleware.Logger(log),
		middleware.Recover(log),
		middleware.Timeout(timeout),
		tel.Middleware(),
	}
	for _, p := range app.Packs {
		if m, ok := p.(Middlewarer); ok {
			chain = append(chain, m.Middleware())
		}
	}
	api := middleware.Chain(r.Handler(), chain...)
	readyTimeout := app.ReadyTimeout
	if readyTimeout == 0 {
		readyTimeout = 3 * time.Second
	}
	ops := http.NewServeMux()
	ops.Handle("GET "+MetricsPath, tel.Metrics())
	ops.Handle("GET "+HealthzPath, telemetry.Healthz())
	ops.Handle("GET "+ReadyzPath, tel.Readyz(readyTimeout))
	if os.Getenv(devserver.EnvMode) == "dev" {
		ops.Handle("GET /debug/pprof/", http.HandlerFunc(pprof.Index))
		ops.Handle("GET /debug/pprof/profile", http.HandlerFunc(pprof.Profile))
		ops.Handle("GET /debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	}

	var frontend http.Handler
	var sidecar *devserver.Sidecar
	switch {
	case os.Getenv(devserver.EnvMode) == "dev" && os.Getenv(devserver.EnvFrontendURL) != "":
		p, err := devserver.NewProxy(os.Getenv(devserver.EnvFrontendURL))
		if err != nil {
			return nil, nil, err
		}
		frontend = devserver.AgentFiles(p)
	case app.Dist != nil:
		frontend = devserver.Static(app.Dist)
		if os.Getenv(devserver.EnvSSR) == "1" {
			s, err := devserver.NewSidecar(app.Dist, "http://"+envOr(devserver.EnvAddr, "127.0.0.1:3000"), log)
			if err != nil {
				return nil, nil, err
			}
			sidecar = s
			frontend = s.Handler(frontend)
		}
	case app.Frontend != nil:
		frontend = app.Frontend
	default:
		frontend = http.NotFoundHandler()
	}
	if os.Getenv(devserver.EnvMode) == "dev" && app.Dist == nil {
		frontend = devserver.AgentFiles(frontend)
	}
	all := devserver.Split(router.APIPrefix, api, opsThenFrontend(ops, frontend, os.Getenv(devserver.EnvMode) == "dev"))
	mw := append([]middleware.Middleware{
		middleware.SecureHeaders(middleware.SecureHeadersOptions{CSP: app.CSP}),
		servicesMiddleware(services),
	}, app.Middleware...)
	return middleware.Chain(all, mw...), sidecar, nil
}

// opsThenFrontend serves the operational endpoints and hands everything
// else to the frontend.
func opsThenFrontend(ops *http.ServeMux, frontend http.Handler, dev bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == MetricsPath, r.URL.Path == HealthzPath, r.URL.Path == ReadyzPath,
			dev && strings.HasPrefix(r.URL.Path, "/debug/pprof/"):
			ops.ServeHTTP(w, r)
		default:
			frontend.ServeHTTP(w, r)
		}
	})
}

// Sub returns the subdirectory dir of fsys, for `//go:embed all:dist`
// followed by lidza.Sub(dist, "dist"). It panics if dir does not exist,
// which for an embedded FS is a build mistake, not a runtime condition.
func Sub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("lidza.Sub(%q): %v", dir, err))
	}
	return sub
}

func (app App) logger() *slog.Logger {
	if app.Logger != nil {
		return app.Logger
	}
	return slog.Default()
}

func name(app App) string {
	if app.Name != "" {
		return app.Name
	}
	return "lidza app"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
