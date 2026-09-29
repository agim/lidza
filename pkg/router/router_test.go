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

// A group's specific route beats the parent's wildcard route for the same
// prefix: every route is on one ServeMux, so its precedence applies.
func TestGroupRouteNotShadowed(t *testing.T) {
	r := New()
	var guarded bool
	require := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			guarded = true
			next.ServeHTTP(w, req)
		})
	}
	Route(r, "GET /api/v1/things/{slug}", func(ctx context.Context, req *Request[None]) (string, error) { return "slug " + req.Param("slug"), nil })
	g := r.Group("/api/v1/things", require)
	Route(g, "GET /api/v1/things/prefill", func(ctx context.Context, req *Request[None]) (string, error) { return "prefill", nil })

	get := func(path string) (int, string) {
		guarded = false
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	if code, body := get("/api/v1/things/prefill"); code != 200 || body != `"prefill"` || !guarded {
		t.Fatalf("prefill: %d %s guarded=%v", code, body, guarded)
	}
	if code, body := get("/api/v1/things/lamp"); code != 200 || body != `"slug lamp"` || guarded {
		t.Fatalf("slug: %d %s guarded=%v", code, body, guarded)
	}
	// A wrong method is a 405 with Allow, an unknown path a 404; the
	// group's middleware runs for neither.
	rec := httptest.NewRecorder()
	guarded = false
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/things/prefill", nil))
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "GET") || guarded {
		t.Fatalf("POST: %d allow=%q guarded=%v", rec.Code, rec.Header().Get("Allow"), guarded)
	}
	if code, _ := get("/api/v1/things/prefill/x"); code != 404 || guarded {
		t.Fatalf("unknown: %d guarded=%v", code, guarded)
	}
}

// A group prefix may hold wildcards; the handlers read their values, and
// a nested group runs its middleware after its parent's.
func TestGroupPatternPrefix(t *testing.T) {
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
	r.Use(tag("root"))
	post := r.Group("/api/v1/posts/{id}", tag("post"))
	Route(post, "GET /api/v1/posts/{id}", func(ctx context.Context, req *Request[None]) (string, error) { return "post " + req.Param("id"), nil })
	tags := post.Group("/api/v1/posts/{id}/tags", tag("tags"))
	Route(tags, "GET /api/v1/posts/{id}/tags/{tag}", func(ctx context.Context, req *Request[None]) (string, error) {
		return req.Param("id") + "/" + req.Param("tag"), nil
	})

	get := func(path string) (int, string) {
		seen = nil
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	if code, body := get("/api/v1/posts/7"); code != 200 || body != `"post 7"` || strings.Join(seen, ",") != "root,post" {
		t.Fatalf("post: %d %s %v", code, body, seen)
	}
	if code, body := get("/api/v1/posts/7/tags/go"); code != 200 || body != `"7/go"` || strings.Join(seen, ",") != "root,post,tags" {
		t.Fatalf("tag: %d %s %v", code, body, seen)
	}
}

// A group's pattern must lie under its prefix, wildcards written alike.
func TestGroupPatternOutsidePrefix(t *testing.T) {
	r := New()
	g := r.Group("/api/v1/posts/{id}")
	for _, pattern := range []string{"GET /api/v1/other", "GET /api/v1/posts/{slug}/tags", "GET /api/v1/postsx"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", pattern)
				}
			}()
			g.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
		}()
	}
	g.HandleFunc("DELETE /api/v1/posts/{id}", func(http.ResponseWriter, *http.Request) {})
	g.HandleFunc("/api/v1/posts/{id}/files/", func(http.ResponseWriter, *http.Request) {})
}
