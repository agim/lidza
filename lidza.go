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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/router"
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

	// OnStart runs before the listener opens: connect pools, warm caches.
	// An error aborts the start.
	OnStart func(ctx context.Context) error
	// OnReady runs once the app is listening.
	OnReady func()
	// OnShutdown runs after in-flight requests finished, before exit:
	// close pools, flush queues.
	OnShutdown func(ctx context.Context) error
}

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

// Serve is Run without the process exit: it blocks until ctx is cancelled or
// a termination signal arrives, then shuts the server down.
func Serve(ctx context.Context, app App) error {
	log := app.logger()
	h, err := Handler(app)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if app.OnStart != nil {
		if err := app.OnStart(ctx); err != nil {
			return fmt.Errorf("start: %w", err)
		}
	}
	addr := envOr(devserver.EnvAddr, "127.0.0.1:3000")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
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
		return err
	}
	if app.OnShutdown != nil {
		if err := app.OnShutdown(shutdown); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}

// Handler builds the app's http.Handler: the API router under /api with the
// standard pipeline and, for every other path, the proxy to the frontend
// dev server (dev mode), the embedded build, or the app's own Frontend.
func Handler(app App) (http.Handler, error) {
	log := app.logger()
	r := router.New()
	if app.Routes != nil {
		app.Routes(r)
	}
	timeout := app.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	api := middleware.Chain(r.Handler(),
		middleware.RequestID(),
		middleware.Logger(log),
		middleware.Recover(log),
		middleware.Timeout(timeout),
	)

	var frontend http.Handler
	switch {
	case os.Getenv(devserver.EnvMode) == "dev" && os.Getenv(devserver.EnvFrontendURL) != "":
		p, err := devserver.NewProxy(os.Getenv(devserver.EnvFrontendURL))
		if err != nil {
			return nil, err
		}
		frontend = devserver.AgentFiles(p)
	case app.Dist != nil:
		frontend = devserver.Static(app.Dist)
	case app.Frontend != nil:
		frontend = app.Frontend
	default:
		frontend = http.NotFoundHandler()
	}
	if os.Getenv(devserver.EnvMode) == "dev" && app.Dist == nil {
		frontend = devserver.AgentFiles(frontend)
	}
	all := devserver.Split(router.APIPrefix, api, frontend)
	mw := append([]middleware.Middleware{middleware.SecureHeaders(middleware.SecureHeadersOptions{CSP: app.CSP})}, app.Middleware...)
	return middleware.Chain(all, mw...), nil
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
