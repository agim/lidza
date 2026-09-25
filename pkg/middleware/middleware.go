// Package middleware holds the request pipeline every Līdza app runs:
// panic recovery, request ids, request logging, per-request deadlines,
// body limits, CORS and secure headers. Each middleware is a plain
// func(http.Handler) http.Handler with no state beyond its options.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so that the first one listed is the outermost.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID reads X-Request-ID from the request or generates one, stores
// it in the context and echoes it in the response.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" || len(id) > 128 {
				id = newID()
			}
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		})
	}
}

// GetRequestID returns the request id set by RequestID, or "".
func GetRequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// Recover turns a panic into a 500 JSON error and logs the stack, so one
// bad request never takes the process down.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					if p == http.ErrAbortHandler {
						panic(p)
					}
					log.Error("panic", "error", fmt.Sprint(p), "method", r.Method, "path", r.URL.Path,
						"request_id", GetRequestID(r.Context()), "stack", string(debug.Stack()))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal error"}` + "\n"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Logger writes one structured line per request: method, path, status,
// bytes, duration and request id.
func Logger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status(),
				"bytes", sw.bytes, "ms", time.Since(start).Milliseconds(), "request_id", GetRequestID(r.Context()))
		})
	}
}

// statusWriter records the status and byte count; it forwards Flush,
// Hijack and Unwrap so streaming and WebSocket handlers keep working.
type statusWriter struct {
	http.ResponseWriter
	code  int
	bytes int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.code == 0 {
		s.code = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusWriter) status() int {
	if s.code == 0 {
		return http.StatusOK
	}
	return s.code
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Timeout gives every request a deadline through its context. Handlers
// and the calls they make (database, HTTP, Rust pool) stop at the deadline;
// the response is not buffered, so streaming still works.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MaxBody caps the request body; reading past n fails with a
// *http.MaxBytesError, which the typed router reports as 400.
func MaxBody(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORSOptions configures CORS. Same-origin apps (the default: the binary
// serves both the frontend and /api) need none.
type CORSOptions struct {
	// Origins are the allowed origins; "*" allows any (not with credentials).
	Origins []string
	Methods []string // default GET, POST, PUT, PATCH, DELETE
	Headers []string // default Content-Type, Authorization, X-Request-ID
	// Credentials allows cookies and Authorization on cross-origin calls.
	Credentials bool
	// MaxAge is how long browsers cache the preflight, default 10 minutes.
	MaxAge time.Duration
}

// CORS answers preflights and sets the allow headers for the listed
// origins. Requests from other origins pass through without CORS headers,
// which the browser then blocks.
func CORS(o CORSOptions) Middleware {
	if len(o.Methods) == 0 {
		o.Methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	}
	if len(o.Headers) == 0 {
		o.Headers = []string{"Content-Type", "Authorization", "X-Request-ID"}
	}
	if o.MaxAge == 0 {
		o.MaxAge = 10 * time.Minute
	}
	any := false
	allowed := map[string]bool{}
	for _, org := range o.Origins {
		if org == "*" {
			any = true
		}
		allowed[org] = true
	}
	methods := strings.Join(o.Methods, ", ")
	headers := strings.Join(o.Headers, ", ")
	maxAge := strconv.Itoa(int(o.MaxAge.Seconds()))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" || !(any || allowed[origin]) {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			if any && !o.Credentials {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}
			if o.Credentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			h.Set("Access-Control-Expose-Headers", "X-Request-ID")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", methods)
				h.Set("Access-Control-Allow-Headers", headers)
				h.Set("Access-Control-Max-Age", maxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SecureHeadersOptions configures SecureHeaders.
type SecureHeadersOptions struct {
	// CSP is the Content-Security-Policy value; empty sends none. Vite's
	// dev server injects inline scripts, so a strict policy is for
	// production builds.
	CSP string
	// HSTS enables Strict-Transport-Security for a year; only behind TLS.
	HSTS bool
}

// SecureHeaders sets the response headers every app should send.
func SecureHeaders(o SecureHeadersOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if o.CSP != "" {
				h.Set("Content-Security-Policy", o.CSP)
			}
			if o.HSTS {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
