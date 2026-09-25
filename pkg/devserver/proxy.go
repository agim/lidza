// Package devserver is the Līdza dev server: the reverse proxy that puts the
// Go control plane and the frontend dev server behind one port, the static
// file server used in production, and the coordinator that keeps the app
// binary rebuilt while `lidza dev` runs.
package devserver

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// NewProxy returns a handler that forwards every request to the frontend dev
// server at target, including WebSocket upgrades (Vite HMR). The Host header
// is rewritten to the target so Vite's allowed-hosts check passes.
func NewProxy(target string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("frontend url %q: must be an absolute http(s) URL", target)
	}
	p := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.Out.Host = u.Host
		},
		// Flush as bytes arrive: HMR and SSE streams must not be buffered.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "Līdza dev proxy: frontend dev server at %s is not reachable (%v)\n", target, err)
		},
	}
	return p, nil
}

// Split routes requests under apiPrefix to api and everything else to
// frontend. apiPrefix is matched as a path prefix, "/api/" also matching
// "/api" itself.
func Split(apiPrefix string, api, frontend http.Handler) http.Handler {
	bare := strings.TrimSuffix(apiPrefix, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == bare || strings.HasPrefix(r.URL.Path, apiPrefix) {
			api.ServeHTTP(w, r)
			return
		}
		frontend.ServeHTTP(w, r)
	})
}
