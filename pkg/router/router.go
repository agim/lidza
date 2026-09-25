// Package router is the Līdza control plane's HTTP router: the API routes an
// application registers, plus the built-in ones every app serves.
package router

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/version"
)

// APIPrefix is the path prefix reserved for the Go control plane. Everything
// under it is handled by the Router; everything else belongs to the frontend.
const APIPrefix = "/api/"

// Router registers and serves API handlers. It wraps net/http's ServeMux, so
// patterns take the "METHOD /path/{param}" form.
type Router struct {
	mux  *http.ServeMux
	mw   []middleware.Middleware
	once sync.Once
	h    http.Handler
}

// builtins are the routes every app serves, registered by New.
var builtins = []struct {
	pattern string
	handler http.HandlerFunc
}{
	{"GET /api/v1/health", health},
}

// Builtins lists the patterns of the routes every app serves.
func Builtins() []string {
	out := make([]string, len(builtins))
	for i, b := range builtins {
		out[i] = b.pattern
	}
	return out
}

// New returns a Router with the built-in routes registered.
func New() *Router {
	r := &Router{mux: http.NewServeMux()}
	for _, b := range builtins {
		r.HandleFunc(b.pattern, b.handler)
	}
	return r
}

// Handle registers a handler for a ServeMux pattern.
func (r *Router) Handle(pattern string, h http.Handler) { r.mux.Handle(pattern, h) }

// HandleFunc registers a handler function for a ServeMux pattern.
func (r *Router) HandleFunc(pattern string, h http.HandlerFunc) { r.mux.HandleFunc(pattern, h) }

// Use adds middleware around every route, outermost first. Call it before
// the router serves its first request; later calls have no effect.
func (r *Router) Use(mw ...middleware.Middleware) { r.mw = append(r.mw, mw...) }

// Group returns a router for the routes at prefix and below it
// ("/api/v1/notes" covers "/api/v1/notes" and "/api/v1/notes/{id}") with
// middleware of its own, run after the parent's. Routes registered on the
// group use full patterns, as on the parent:
//
//	notes := r.Group("/api/v1/notes", auth.Require())
//	router.Route(notes, "GET /api/v1/notes", listNotes)
//
// A route the parent registers under the same prefix with a method wins
// over the group, being the more specific pattern.
func (r *Router) Group(prefix string, mw ...middleware.Middleware) *Router {
	g := &Router{mux: http.NewServeMux()}
	g.Use(mw...)
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		prefix = "/"
	}
	r.mux.Handle(prefix, g)
	if prefix != "/" {
		r.mux.Handle(prefix+"/", g)
	}
	return g
}

// Handler returns the router with its middleware applied.
func (r *Router) Handler() http.Handler {
	r.once.Do(func() { r.h = middleware.Chain(r.mux, r.mw...) })
	return r.h
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.Handler().ServeHTTP(w, req) }

// Health is the response body of GET /api/v1/health.
type Health struct {
	Status  string    `json:"status"`
	Version string    `json:"version"`
	Time    time.Time `json:"time"`
}

func health(w http.ResponseWriter, _ *http.Request) {
	JSON(w, http.StatusOK, Health{Status: "ok", Version: version.String(), Time: time.Now().UTC()})
}

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes a JSON error body: {"error": msg}.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}
