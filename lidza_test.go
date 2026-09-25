package lidza

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agim/lidza/pkg/devserver"
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
