package lidza

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/router"
)

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func TestHandlerProduction(t *testing.T) {
	t.Setenv(devserver.EnvMode, "")
	h, err := Handler(App{
		Dist: fstest.MapFS{"index.html": {Data: []byte("<h1>built</h1>")}},
		Routes: func(r *router.Router) {
			r.HandleFunc("GET /api/v1/hello", func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, "hi")
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, h, "/api/v1/health"); code != 200 || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("health: %d %s", code, body)
	}
	if code, body := get(t, h, "/api/v1/hello"); code != 200 || body != "hi" {
		t.Fatalf("hello: %d %s", code, body)
	}
	if code, body := get(t, h, "/some/route"); code != 200 || body != "<h1>built</h1>" {
		t.Fatalf("spa: %d %s", code, body)
	}
	if code, _ := get(t, h, "/api/v1/nope"); code != 404 {
		t.Fatalf("unknown api route: %d", code)
	}
}

func TestHandlerDev(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "vite:"+r.URL.Path)
	}))
	defer up.Close()
	t.Setenv(devserver.EnvMode, "dev")
	t.Setenv(devserver.EnvFrontendURL, up.URL)
	h, err := Handler(App{Dist: fstest.MapFS{"index.html": {Data: []byte("stale")}}})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, h, "/src/main.tsx"); code != 200 || body != "vite:/src/main.tsx" {
		t.Fatalf("dev proxy: %d %s", code, body)
	}
	if code, _ := get(t, h, "/api/v1/health"); code != 200 {
		t.Fatalf("health in dev: %d", code)
	}
}

func TestHandlerPipeline(t *testing.T) {
	t.Setenv(devserver.EnvMode, "")
	h, err := Handler(App{
		CSP: "default-src 'self'",
		Routes: func(r *router.Router) {
			r.HandleFunc("GET /api/v1/panic", func(w http.ResponseWriter, _ *http.Request) { panic("x") })
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("X-Api-Only", "1")
					next.ServeHTTP(w, req)
				})
			})
		},
		Middleware: []middleware.Middleware{middleware.CORS(middleware.CORSOptions{Origins: []string{"https://x.example"}})},
		Dist:       fstest.MapFS{"index.html": {Data: []byte("app")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://x.example")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("X-Request-ID") == "" || rec.Header().Get("X-Api-Only") != "1" ||
		rec.Header().Get("Content-Security-Policy") != "default-src 'self'" || rec.Header().Get("Access-Control-Allow-Origin") != "https://x.example" {
		t.Fatalf("api headers: %d %v", rec.Code, rec.Header())
	}
	if code, body := get(t, h, "/api/v1/panic"); code != 500 || !strings.Contains(body, "internal error") {
		t.Fatalf("panic: %d %s", code, body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Api-Only") != "" {
		t.Fatalf("frontend headers: %v", rec.Header())
	}
}

func TestHandlerCustomFrontend(t *testing.T) {
	t.Setenv(devserver.EnvMode, "")
	h, err := Handler(App{Frontend: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "page") })})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, h, "/about"); code != 200 || body != "page" {
		t.Fatalf("frontend: %d %s", code, body)
	}
}

func TestServeHooks(t *testing.T) {
	t.Setenv(devserver.EnvMode, "")
	t.Setenv(devserver.EnvAddr, "127.0.0.1:0")
	var events []string
	ctx, cancel := context.WithCancel(context.Background())
	err := Serve(ctx, App{
		OnStart:    func(context.Context) error { events = append(events, "start"); return nil },
		OnReady:    func() { events = append(events, "ready"); cancel() },
		OnShutdown: func(context.Context) error { events = append(events, "shutdown"); return nil },
	})
	if err != nil || strings.Join(events, ",") != "start,ready,shutdown" {
		t.Fatalf("err %v events %v", err, events)
	}
	err = Serve(context.Background(), App{OnStart: func(context.Context) error { return errors.New("no db") }})
	if err == nil || !strings.Contains(err.Error(), "no db") {
		t.Fatalf("start error: %v", err)
	}
}

func TestHandlerNoFrontend(t *testing.T) {
	t.Setenv(devserver.EnvMode, "")
	h, err := Handler(App{})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, h, "/"); code != 404 {
		t.Fatalf("no dist: %d", code)
	}
}
