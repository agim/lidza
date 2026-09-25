package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var h Health
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
	}
	if h.Status != "ok" || h.Version == "" {
		t.Fatalf("unexpected body: %+v", h)
	}
}

func TestHealthMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/health", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
}

func TestAppRoute(t *testing.T) {
	r := New()
	r.HandleFunc("GET /api/v1/hello/{name}", func(w http.ResponseWriter, req *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"hello": req.PathValue("name")})
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/hello/agim", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"hello\":\"agim\"}\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestGroup(t *testing.T) {
	r := New()
	var seen []string
	tag := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				seen = append(seen, name)
				next.ServeHTTP(w, req)
			})
		}
	}
	r.Use(tag("parent"))
	g := r.Group("/api/v1/notes", tag("group"))
	Route(g, "GET /api/v1/notes", func(ctx context.Context, req *Request[None]) ([]string, error) { return []string{"a"}, nil })
	Route(g, "GET /api/v1/notes/{id}", func(ctx context.Context, req *Request[None]) (string, error) { return req.Param("id"), nil })
	Route(r, "GET /api/v1/hello", func(ctx context.Context, req *Request[None]) (string, error) { return "hi", nil })

	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	if code, body := get("/api/v1/notes"); code != 200 || body != `["a"]` {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, body := get("/api/v1/notes/7"); code != 200 || body != `"7"` {
		t.Fatalf("get: %d %s", code, body)
	}
	if strings.Join(seen, ",") != "parent,group,parent,group" {
		t.Fatalf("middleware order: %v", seen)
	}
	seen = nil
	if code, _ := get("/api/v1/hello"); code != 200 || strings.Join(seen, ",") != "parent" {
		t.Fatalf("outside the group: %d %v", code, seen)
	}
	if code, _ := get("/api/v1/notes/7/x"); code != 404 {
		t.Fatalf("unknown path in group: %d", code)
	}
}
