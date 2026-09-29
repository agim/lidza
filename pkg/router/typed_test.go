package router

import (
	"context"
	"errors"
	"io"
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
		req.Header().Set("Location", "/api/v1/posts/new")
		req.SetCookie(&http.Cookie{Name: "seen", Value: "1"})
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

func TestRouteReplyHeaders(t *testing.T) {
	rec := do(t, typedRouter(), "POST", "/api/v1/posts", `{"title":"x"}`)
	if rec.Header().Get("Location") != "/api/v1/posts/new" || len(rec.Result().Cookies()) != 1 || rec.Result().Cookies()[0].Name != "seen" {
		t.Fatalf("headers %v cookies %v", rec.Header(), rec.Result().Cookies())
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

// ErrorCode adds a code the client can switch on beside the message.
func TestErrorCode(t *testing.T) {
	r := New()
	Route(r, "POST /x", func(ctx context.Context, req *Request[None]) (None, error) {
		return None{}, ErrorCode(http.StatusForbidden, "wrong_password", "password does not match")
	})
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != 403 || strings.TrimSpace(rec.Body.String()) != `{"code":"wrong_password","error":"password does not match"}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// An upload route (In = File) hands the raw body to the handler with its
// type and name, and bounds it: 413 from Content-Length up front, or from
// the read error the handler returns.
func TestUpload(t *testing.T) {
	type stored struct {
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
		Bytes       string `json:"bytes"`
		ID          string `json:"id"`
	}
	r := New()
	Route(r, "PUT /api/v1/posts/{id}/image", func(ctx context.Context, req *Request[File]) (stored, error) {
		data, err := io.ReadAll(req.Body.Body)
		if err != nil {
			return stored{}, err
		}
		req.Status(http.StatusCreated)
		return stored{Name: req.Body.Name, ContentType: req.Body.ContentType, Bytes: string(data), ID: req.Param("id")}, nil
	}, UploadLimit(12))

	put := func(body io.Reader, header map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/posts/7/image", body)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := put(strings.NewReader("{not json"), map[string]string{"Content-Type": "image/png", "Content-Disposition": `attachment; filename*=UTF-8''caf%C3%A9%20%281%29.png`})
	want := `{"name":"café (1).png","contentType":"image/png","bytes":"{not json","id":"7"}`
	if rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	rec = put(strings.NewReader("abc"), nil)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"contentType":"application/octet-stream"`) {
		t.Fatalf("no type: %d %s", rec.Code, rec.Body)
	}
	if rec = put(strings.NewReader("1234567890abc"), nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the limit by Content-Length: %d %s", rec.Code, rec.Body)
	}
	// No Content-Length: the limit shows when the handler reads.
	if rec = put(io.MultiReader(strings.NewReader("1234567"), strings.NewReader("890abc")), nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the limit while reading: %d %s", rec.Code, rec.Body)
	}
}

// A cookie set over HTTPS is sent Secure; over plain HTTP it is as set.
func TestSetCookieSecureOverTLS(t *testing.T) {
	r := typedRouter()
	for _, tc := range []struct {
		url    string
		secure bool
	}{{"https://example.test/api/v1/posts", true}, {"http://example.test/api/v1/posts", false}} {
		req := httptest.NewRequest("POST", tc.url, strings.NewReader(`{"title":"hello"}`))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		cs := rec.Result().Cookies()
		if len(cs) != 1 || cs[0].Secure != tc.secure {
			t.Errorf("%s: cookies %+v, want Secure=%v", tc.url, cs, tc.secure)
		}
	}
}
