// Package router is the Līdza control plane's HTTP router: the API routes an
// application registers, plus the built-in ones every app serves.
package router

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/agim/lidza/pkg/version"
)

// APIPrefix is the path prefix reserved for the Go control plane. Everything
// under it is handled by the Router; everything else belongs to the frontend.
const APIPrefix = "/api/"

// Router registers and serves API handlers. It wraps net/http's ServeMux, so
// patterns take the "METHOD /path/{param}" form.
type Router struct {
	mux *http.ServeMux
}

// New returns a Router with the built-in routes registered.
func New() *Router {
	r := &Router{mux: http.NewServeMux()}
	r.HandleFunc("GET /api/v1/health", health)
	return r
}

// Handle registers a handler for a ServeMux pattern.
func (r *Router) Handle(pattern string, h http.Handler) { r.mux.Handle(pattern, h) }

// HandleFunc registers a handler function for a ServeMux pattern.
func (r *Router) HandleFunc(pattern string, h http.HandlerFunc) { r.mux.HandleFunc(pattern, h) }

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }

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
