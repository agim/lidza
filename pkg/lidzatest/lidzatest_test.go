package lidzatest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/router"
)

func TestClockAndRecorder(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Authorization", "secret")
		w.Header().Set("X-Upstream", "yes")
		body, _ := io.ReadAll(r.Body)
		io.WriteString(w, "echo:"+r.Method+":"+string(body))
	}))
	defer upstream.Close()

	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "lidza.json"), []byte(`{"name":"x","frontend":{"template":"htmx"}}`), 0o644)
	t.Chdir(root)

	app := lidza.App{Routes: func(r *router.Router) {
		r.HandleFunc("GET /api/v1/now", func(w http.ResponseWriter, req *http.Request) {
			io.WriteString(w, lidza.Now(req.Context()).UTC().Format(time.RFC3339))
		})
		r.HandleFunc("POST /api/v1/proxy", func(w http.ResponseWriter, req *http.Request) {
			res, err := lidza.HTTPClient(req.Context()).Post(upstream.URL+"/x", "text/plain", strings.NewReader("hi"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer res.Body.Close()
			w.Header().Set("X-Upstream", res.Header.Get("X-Upstream"))
			io.Copy(w, res.Body)
		})
	}}

	// Record.
	t.Setenv(RecordEnv, "1")
	srv := Start(t, app, WithRecorder("proxy"))
	srv.Clock.Set(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	var body string
	res := srv.JSON(t, "GET", "/api/v1/now", nil, nil)
	raw, _ := io.ReadAll(res.Body)
	if string(raw) != "2030-01-02T03:04:05Z" {
		t.Fatalf("frozen clock: %s", raw)
	}
	srv.Clock.Advance(time.Hour)
	res = srv.JSON(t, "GET", "/api/v1/now", nil, nil)
	raw, _ = io.ReadAll(res.Body)
	if string(raw) != "2030-01-02T04:04:05Z" {
		t.Fatalf("advanced clock: %s", raw)
	}
	res = srv.JSON(t, "POST", "/api/v1/proxy", nil, nil)
	raw, _ = io.ReadAll(res.Body)
	body = string(raw)
	if res.StatusCode != 200 || body != "echo:POST:hi" || res.Header.Get("X-Upstream") != "yes" {
		t.Fatalf("record: %d %s", res.StatusCode, body)
	}
	fixture, err := os.ReadFile(filepath.Join(root, FixtureDir, "proxy.json"))
	if err != nil || !strings.Contains(string(fixture), "echo:POST:hi") || strings.Contains(string(fixture), "secret") {
		t.Fatalf("fixture: %v\n%s", err, fixture)
	}
	srv.Close()

	// Replay, upstream gone.
	upstream.Close()
	t.Setenv(RecordEnv, "")
	srv2 := Start(t, app, WithRecorder("proxy"))
	res = srv2.JSON(t, "POST", "/api/v1/proxy", nil, nil)
	raw, _ = io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(raw) != "echo:POST:hi" {
		t.Fatalf("replay: %d %s", res.StatusCode, raw)
	}
	if _, err := NewRecorder("missing"); err == nil || !strings.Contains(err.Error(), RecordEnv) {
		t.Fatalf("missing fixture: %v", err)
	}
	_ = context.Background
}

func TestClockResume(t *testing.T) {
	c := &Clock{}
	c.Set(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	c.Resume()
	if d := time.Until(c.Now()); d < 365*24*time.Hour {
		t.Fatalf("offset lost after resume: %v", d)
	}
}
