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
	"sync"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/db"
	"github.com/agim/lidza/pkg/config"
	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/env"
)

// Server is a running app under test.
type Server struct {
	*httptest.Server
	// Services holds what the packs provided.
	Services *lidza.Services
	// Clock is what lidza.Now(ctx) reads inside handlers; freeze or
	// advance it from the test.
	Clock  *Clock
	client *http.Client
}

// Option configures Start.
type StartOption func(*lidza.App, *startOptions)

type startOptions struct {
	transport http.RoundTripper
}

// WithRecorder routes the app's outbound HTTP (lidza.HTTPClient) through
// the fixture called name: recorded with LIDZA_RECORD=1, replayed after.
func WithRecorder(name string) StartOption {
	return func(_ *lidza.App, o *startOptions) {
		r, err := NewRecorder(name)
		if err != nil {
			panic(err)
		}
		o.transport = r
	}
}

// WithTransport routes outbound HTTP through any RoundTripper (a stub).
func WithTransport(rt http.RoundTripper) StartOption {
	return func(_ *lidza.App, o *startOptions) { o.transport = rt }
}

// Start boots app with LIDZA_MODE=test from the project root (found by
// walking up to lidza.json, so tests in sub-packages work) and serves it.
// Each test package gets a database of its own, a copy of the test
// database (see EnvSharedDatabase), so go test runs packages in parallel
// without their rows colliding. Everything stops when the test ends.
func Start(t testing.TB, app lidza.App, opts ...StartOption) *Server {
	t.Helper()
	pkgDir, _ := os.Getwd()
	root := projectRoot()
	if root != "" {
		t.Chdir(root)
	}
	t.Setenv(devserver.EnvMode, "test")
	if url := packageDatabase(t, root, pkgDir); url != "" {
		t.Setenv("DATABASE_URL", url)
	}
	var o startOptions
	for _, opt := range opts {
		opt(&app, &o)
	}
	clock := &Clock{}
	userStart := app.OnStart
	app.OnStart = func(ctx context.Context, s *lidza.Services) error {
		lidza.Provide[lidza.Clock](s, clock)
		if o.transport != nil {
			lidza.Provide[http.RoundTripper](s, o.transport)
		}
		if userStart != nil {
			return userStart(ctx, s)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	booted, err := lidza.Boot(ctx, app)
	if err != nil {
		t.Fatalf("lidzatest: start app: %v", err)
	}
	srv := httptest.NewServer(booted.Handler)
	jar, _ := cookiejar.New(nil)
	s := &Server{Server: srv, Services: booted.Services, Clock: clock, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
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

// Context returns a context carrying the app's services, for calling a
// pack from the test the way a handler does: mail.From(srv.Context()),
// jobs.From(srv.Context()).
func (s *Server) Context() context.Context {
	return lidza.WithServices(context.Background(), s.Services)
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
	// As the generated clients do: state-changing requests are JSON even
	// without a body, which the auth pack's cookie sessions require.
	if body != nil || (method != http.MethodGet && method != http.MethodHead) {
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

// EnvSharedDatabase, set to 1, keeps every test package on the test
// database itself (DATABASE_URL in .env.test) instead of a copy of its
// own. A copy is made once per test package and run, from the test
// database as lidza test migrated it, and only for a database named
// *_test on this machine; lidza test drops the copies when it ends.
const EnvSharedDatabase = "LIDZA_TEST_SHARED_DB"

var pkgDB struct {
	once sync.Once
	url  string
	err  error
}

// packageDatabase is this test package's copy of the test database, or
// "" to use the test database itself (shared, no database, or a copy
// that could not be made, said once).
func packageDatabase(t testing.TB, root, pkgDir string) string {
	pkgDB.once.Do(func() {
		if os.Getenv(EnvSharedDatabase) == "1" || root == "" {
			return
		}
		url := os.Getenv("DATABASE_URL")
		if url == "" {
			vals, err := env.Values(root)
			if err != nil {
				return
			}
			url = vals["DATABASE_URL"]
		}
		if url == "" {
			return
		}
		key, err := filepath.Rel(root, pkgDir)
		if err != nil {
			key = filepath.Base(pkgDir)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		pkgDB.url, pkgDB.err = db.CloneTestDatabase(ctx, url, filepath.ToSlash(key))
		if pkgDB.err != nil {
			t.Logf("lidzatest: this package shares the test database (%s=1 says so on purpose): %v", EnvSharedDatabase, pkgDB.err)
		}
	})
	return pkgDB.url
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
