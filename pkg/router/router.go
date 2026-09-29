// Package router is the Līdza control plane's HTTP router: the API routes an
// application registers, plus the built-in ones every app serves.
package router

import (
	"encoding/json"
	"fmt"
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
	mux    *http.ServeMux
	mw     []middleware.Middleware
	once   sync.Once
	h      http.Handler
	mounts []Mount
	// parent and prefix are set on a group: its routes go to the
	// parent with the group's middleware around each one.
	parent *Router
	prefix string
}

// Mount is a handler served outside /api, at a path prefix, before the
// frontend: the admin pages, a webhook endpoint, a file server.
type Mount struct {
	Prefix  string
	Handler http.Handler
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

// Handle registers a handler for a ServeMux pattern. On a group the
// pattern's path must lie at the group's prefix or below it.
func (r *Router) Handle(pattern string, h http.Handler) {
	if r.parent == nil {
		r.mux.Handle(pattern, h)
		return
	}
	if !under(patternPath(pattern), r.prefix) {
		panic(fmt.Sprintf("router: pattern %q is outside the group %q", pattern, r.prefix))
	}
	r.parent.Handle(pattern, &groupRoute{group: r, h: h})
}

// HandleFunc registers a handler function for a ServeMux pattern.
func (r *Router) HandleFunc(pattern string, h http.HandlerFunc) { r.Handle(pattern, h) }

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
// Every route lands on the root router's ServeMux with its full pattern,
// the group's middleware wrapped around that route alone. ServeMux
// precedence therefore holds across groups: the more specific pattern
// wins wherever it was registered ("GET /api/v1/notes/recent" in a group
// over "GET /api/v1/notes/{id}" on the parent), and two conflicting
// patterns panic at registration. A path no route matches is a 404 (a
// 405 with Allow for a wrong method) without the group's middleware.
//
// The prefix may hold wildcards; the group's patterns repeat them and the
// handlers read their values:
//
//	post := r.Group("/api/v1/posts/{id}", auth.Require())
//	router.Route(post, "GET /api/v1/posts/{id}/tags", listTags) // req.Param("id")
//
// A pattern outside the prefix panics.
func (r *Router) Group(prefix string, mw ...middleware.Middleware) *Router {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		panic(fmt.Sprintf("router: group prefix %q does not start with /", prefix))
	}
	if r.parent != nil && !under(prefix, r.prefix) {
		panic(fmt.Sprintf("router: group %q is outside the group %q", prefix, r.prefix))
	}
	g := &Router{parent: r, prefix: prefix}
	g.Use(mw...)
	return g
}

// groupRoute is one route of a group: the group's middleware around the
// handler, chained on the first request so a Use on the group before
// serving still counts.
type groupRoute struct {
	group *Router
	h     http.Handler
	once  sync.Once
	chain http.Handler
}

func (g *groupRoute) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	g.once.Do(func() { g.chain = middleware.Chain(g.h, g.group.mw...) })
	g.chain.ServeHTTP(w, req)
}

// patternPath is the path of a ServeMux pattern, without the method and
// the host: "GET example.com/api/x" is "/api/x".
func patternPath(pattern string) string {
	p := strings.TrimSpace(pattern)
	if i := strings.IndexAny(p, " \t"); i >= 0 {
		p = strings.TrimSpace(p[i+1:])
	}
	if i := strings.IndexByte(p, '/'); i > 0 {
		p = p[i:]
	}
	return p
}

// under reports whether path lies at prefix or below it, whole segments
// only; a wildcard is compared as written ("{id}" is not "{slug}").
func under(path, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// root is the router that owns the ServeMux.
func (r *Router) root() *Router {
	for r.parent != nil {
		r = r.parent
	}
	return r
}

// Mount serves h at prefix and below it, outside /api, ahead of the
// frontend ("/admin/" for the admin pages). The app's middleware and
// services apply; the router's own do not, nor a group's.
func (r *Router) Mount(prefix string, h http.Handler) {
	root := r.root()
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	root.mounts = append(root.mounts, Mount{Prefix: prefix, Handler: h})
}

// Mounts lists what Mount registered.
func (r *Router) Mounts() []Mount { return r.root().mounts }

// Handler returns the router with its middleware applied. A group serves
// through the root router: its Handler is the root's.
func (r *Router) Handler() http.Handler {
	if r.parent != nil {
		return r.root().Handler()
	}
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
