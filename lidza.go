// Package lidza is the runtime a Līdza application links against. The app's
// main.go embeds its frontend build and calls Run; `lidza dev` starts the
// same binary in dev mode with the frontend proxied from its dev server.
package lidza

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/router"
)

// App describes one application.
type App struct {
	// Name is shown in logs.
	Name string
	// Dist is the built frontend, served in production. Nil for templates
	// with no static build (htmx).
	Dist fs.FS
	// Routes registers the app's API handlers. May be nil.
	Routes func(r *router.Router)
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
	h, err := Handler(app)
	if err != nil {
		return err
	}
	addr := envOr(devserver.EnvAddr, "127.0.0.1:3000")
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		mode := "production"
		if os.Getenv(devserver.EnvMode) == "dev" {
			mode = "dev"
		}
		log.Printf("%s listening on http://%s (%s)", name(app), addr, mode)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// Handler builds the app's http.Handler: the API router under /api and, for
// every other path, either the proxy to the frontend dev server (dev mode)
// or the embedded build.
func Handler(app App) (http.Handler, error) {
	r := router.New()
	if app.Routes != nil {
		app.Routes(r)
	}

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
	default:
		frontend = http.NotFoundHandler()
	}
	return devserver.Split(router.APIPrefix, r, frontend), nil
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
