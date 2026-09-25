package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/validate"
)

type createIn struct {
	Title string `json:"title"`
}

func (c createIn) Validate() error {
	var errs validate.Errors
	if c.Title == "" {
		errs.Add("title", "required", "required")
	}
	return errs.Result()
}

type post struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func typedRouter() *Router {
	r := New()
	Route(r, "GET /api/v1/posts/{id}", func(ctx context.Context, req *Request[None]) (post, error) {
		if req.Param("id") == "missing" {
			return post{}, NotFound("post")
		}
		return post{ID: req.Param("id"), Title: "t" + req.Query("suffix")}, nil
	})
	Route(r, "POST /api/v1/posts", func(ctx context.Context, req *Request[createIn]) (post, error) {
		req.Status(http.StatusCreated)
		return post{ID: "new", Title: req.Body.Title}, nil
	})
	Route(r, "DELETE /api/v1/posts/{id}", func(ctx context.Context, req *Request[None]) (None, error) {
		return None{}, nil
	})
	Route(r, "GET /api/v1/boom", func(ctx context.Context, req *Request[None]) (post, error) {
		return post{}, errors.New("database exploded")
	})
	return r
}

func do(t *testing.T, r *Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRoute(t *testing.T) {
	r := typedRouter()
	cases := []struct {
		method, path, body string
		status             int
		want               string
	}{
		{"GET", "/api/v1/posts/42?suffix=x", "", 200, `{"id":"42","title":"tx"}`},
		{"GET", "/api/v1/posts/missing", "", 404, `{"error":"post not found"}`},
		{"POST", "/api/v1/posts", `{"title":"hello"}`, 201, `{"id":"new","title":"hello"}`},
		{"POST", "/api/v1/posts", `{"title":""}`, 422, `{"error":"validation","fields":[{"field":"title","rule":"required","message":"required"}]}`},
		{"POST", "/api/v1/posts", ``, 400, `{"error":"request body is empty; expected JSON"}`},
		{"POST", "/api/v1/posts", `{"title":`, 400, ``},
		{"POST", "/api/v1/posts", `{"title":"a"} {}`, 400, `{"error":"invalid JSON body: trailing data"}`},
		{"DELETE", "/api/v1/posts/1", "", 204, ``},
		{"GET", "/api/v1/boom", "", 500, `{"error":"internal error"}`},
	}
	for _, c := range cases {
		rec := do(t, r, c.method, c.path, c.body)
		got := strings.TrimSpace(rec.Body.String())
		if rec.Code != c.status || (c.want != "" && got != c.want) {
			t.Errorf("%s %s %q: got %d %s, want %d %s", c.method, c.path, c.body, rec.Code, got, c.status, c.want)
		}
	}
}

func TestRouteBodyLimit(t *testing.T) {
	r := typedRouter()
	big := `{"title":"` + strings.Repeat("x", MaxBodyBytes) + `"}`
	rec := do(t, r, "POST", "/api/v1/posts", big)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "larger than") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
