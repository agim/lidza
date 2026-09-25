package middleware

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChainOrderAndRequestID(t *testing.T) {
	var order []string
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	var seen string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = GetRequestID(r.Context())
	}), mk("a"), RequestID(), mk("b"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if strings.Join(order, "") != "ab" {
		t.Fatalf("order %v", order)
	}
	if seen == "" || rec.Header().Get("X-Request-ID") != seen {
		t.Fatalf("request id: ctx %q header %q", seen, rec.Header().Get("X-Request-ID"))
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "client-1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != "client-1" {
		t.Fatalf("client id not kept: %q", seen)
	}
}

func TestRecoverAndLogger(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			panic("kaboom")
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, "made")
	}), RequestID(), Logger(log), Recover(log))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/things", nil))
	if rec.Code != 201 || !strings.Contains(buf.String(), "status=201") || !strings.Contains(buf.String(), "bytes=4") {
		t.Fatalf("logger: %d %s", rec.Code, buf.String())
	}

	buf.Reset()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/boom", nil))
	if rec.Code != 500 || rec.Body.String() != "{\"error\":\"internal error\"}\n" {
		t.Fatalf("recover: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(buf.String(), "kaboom") || !strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("panic not logged: %s", buf.String())
	}
}

func TestTimeout(t *testing.T) {
	h := Timeout(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			io.WriteString(w, "cancelled")
		case <-time.After(time.Second):
			io.WriteString(w, "finished")
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Body.String() != "cancelled" {
		t.Fatalf("got %q", rec.Body.String())
	}
}

func TestMaxBody(t *testing.T) {
	h := MaxBody(4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(413)
			return
		}
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("12345")))
	if rec.Code != 413 {
		t.Fatalf("got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("1234")))
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestCORS(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := CORS(CORSOptions{Origins: []string{"https://app.example"}, Credentials: true})(ok)

	req := httptest.NewRequest("OPTIONS", "/api/v1/x", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" || !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") ||
		rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}

	req = httptest.NewRequest("GET", "/api/v1/x", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("foreign origin got CORS headers")
	}

	h = CORS(CORSOptions{Origins: []string{"*"}})(ok)
	req = httptest.NewRequest("GET", "/api/v1/x", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard: %v", rec.Header())
	}
}

func TestSecureHeaders(t *testing.T) {
	h := SecureHeaders(SecureHeadersOptions{CSP: "default-src 'self'", HSTS: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for k, v := range map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Content-Security-Policy":   "default-src 'self'",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	} {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q", k, got)
		}
	}
}

func TestStatusWriterFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	sw.Flush()
	if !rec.Flushed {
		t.Fatal("flush not forwarded")
	}
	var _ context.Context = context.Background()
}
