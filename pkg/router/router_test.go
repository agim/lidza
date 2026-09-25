package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
