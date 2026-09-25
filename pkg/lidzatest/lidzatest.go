// Package lidzatest starts an app for a Go test: packs started against
// .env.test, the handler on an httptest server, and a JSON client. The
// scaffolded routes_test.go shows the shape.
package lidzatest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
)

// Server is a running app under test.
type Server struct {
	*httptest.Server
	// Services holds what the packs provided.
	Services *lidza.Services
	client   *http.Client
}

// Start boots app with LIDZA_MODE=test from the project root (found by
// walking up to lidza.json, so tests in sub-packages work) and serves it.
// Everything stops when the test ends.
func Start(t testing.TB, app lidza.App) *Server {
	t.Helper()
	if root := projectRoot(); root != "" {
		t.Chdir(root)
	}
	t.Setenv(devserver.EnvMode, "test")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	booted, err := lidza.Boot(ctx, app)
	if err != nil {
		t.Fatalf("lidzatest: start app: %v", err)
	}
	srv := httptest.NewServer(booted.Handler)
	jar, _ := cookiejar.New(nil)
	s := &Server{Server: srv, Services: booted.Services, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
	t.Cleanup(func() {
		srv.Close()
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := booted.Close(stop); err != nil {
			t.Errorf("lidzatest: stop app: %v", err)
		}
	})
	return s
}

// Client is an HTTP client with a cookie jar, so cookie sessions persist
// across calls.
func (s *Server) Client() *http.Client { return s.client }

// Option adjusts a request.
type Option func(*http.Request)

// Bearer sets an Authorization header.
func Bearer(token string) Option {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// Header sets a header.
func Header(key, value string) Option {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

// JSON sends body (encoded as JSON when not nil) and decodes the reply
// into out (when not nil). It returns the response with its body already
// read; Body holds the raw bytes again for inspection.
func (s *Server) JSON(t testing.TB, method, path string, body any, out any, opts ...Option) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, s.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	res, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	res.Body = io.NopCloser(bytes.NewReader(raw))
	if out != nil && len(raw) > 0 && res.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return res
}

// projectRoot walks up from the working directory to the lidza.json.
func projectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, config.FileName)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
