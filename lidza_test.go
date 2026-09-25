package lidza

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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
		Packs: []Pack{&testPack{events: &events}},
		OnStart: func(ctx context.Context, s *Services) error {
			if _, ok := s.Lookup(reflect.TypeFor[*testPack]()); !ok {
				return errors.New("pack service missing at OnStart")
			}
			events = append(events, "start")
			return nil
		},
		OnReady:    func() { events = append(events, "ready"); cancel() },
		OnShutdown: func(context.Context) error { events = append(events, "shutdown"); return nil },
	})
	if err != nil || strings.Join(events, ",") != "pack-start,start,ready,shutdown,pack-stop" {
		t.Fatalf("err %v events %v", err, events)
	}
	err = Serve(context.Background(), App{OnStart: func(context.Context, *Services) error { return errors.New("no db") }})
	if err == nil || !strings.Contains(err.Error(), "no db") {
		t.Fatalf("start error: %v", err)
	}
}

type testPack struct{ events *[]string }

func (p *testPack) Name() string { return "test" }
func (p *testPack) Start(ctx context.Context, s *Services) error {
	*p.events = append(*p.events, "pack-start")
	Provide(s, p)
	return nil
}
func (p *testPack) Stop(context.Context) error {
	*p.events = append(*p.events, "pack-stop")
	return nil
}

func TestServices(t *testing.T) {
	s := NewServices()
	Provide(s, 42)
	Provide[string](s, "x")
	ctx := WithServices(context.Background(), s)
	if Service[int](ctx) != 42 || Service[string](ctx) != "x" {
		t.Fatal("lookup")
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "no service of type float64") {
			t.Fatalf("missing service should panic with the type: %v", r)
		}
	}()
	Service[float64](ctx)
}

func TestHandlerInjectsServices(t *testing.T) {
	t.Setenv(devserver.EnvMode, "")
	s := NewServices()
	Provide(s, "injected")
	h, err := handler(App{Routes: func(r *router.Router) {
		r.HandleFunc("GET /api/v1/svc", func(w http.ResponseWriter, req *http.Request) {
			io.WriteString(w, Service[string](req.Context()))
		})
	}}, s)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, h, "/api/v1/svc"); code != 200 || body != "injected" {
		t.Fatalf("%d %s", code, body)
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
