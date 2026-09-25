package devserver

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSplitAndProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Host", r.Host)
		io.WriteString(w, "frontend:"+r.URL.Path)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "api:"+r.URL.Path) })
	h := Split("/api/", api, proxy)

	for path, want := range map[string]string{
		"/":              "frontend:/",
		"/about":         "frontend:/about",
		"/apiary":        "frontend:/apiary",
		"/api":           "api:/api",
		"/api/v1/health": "api:/api/v1/health",
		"/src/main.tsx":  "frontend:/src/main.tsx",
		"/@vite/client":  "frontend:/@vite/client",
		"/api/../secret": "api:/api/../secret",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Body.String(); got != want {
			t.Errorf("%s: got %q, want %q", path, got, want)
		}
		if strings.HasPrefix(want, "frontend:") && rec.Header().Get("X-Host") != strings.TrimPrefix(upstream.URL, "http://") {
			t.Errorf("%s: Host not rewritten: %q", path, rec.Header().Get("X-Host"))
		}
	}
}

func TestProxyUpstreamDown(t *testing.T) {
	proxy, err := NewProxy("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "not reachable") {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestProxyBadURL(t *testing.T) {
	for _, u := range []string{"", "5173", "127.0.0.1:5173", "://x"} {
		if _, err := NewProxy(u); err == nil {
			t.Errorf("%q: expected error", u)
		}
	}
}

// TestProxyWebSocket checks that an Upgrade request is tunneled end to end,
// which is what Vite's HMR client needs through port 3000.
func TestProxyWebSocket(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "not an upgrade", http.StatusBadRequest)
			return
		}
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		buf.Flush()
		line, _ := buf.ReadString('\n')
		buf.WriteString("echo:" + line)
		buf.Flush()
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(proxy)
	defer front.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /?token=abc HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d", resp.StatusCode)
	}
	io.WriteString(conn, "hello\n")
	line, err := r.ReadString('\n')
	if err != nil || line != "echo:hello\n" {
		t.Fatalf("got %q, %v", line, err)
	}
}

func TestStatic(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":         {Data: []byte("<html>app</html>")},
		"assets/app-1a2b.js": {Data: []byte("js")},
		"favicon.svg":        {Data: []byte("<svg/>")},
		"about/index.html":   {Data: []byte("<html>about</html>")},
		".server/entry.js":   {Data: []byte("secret")},
	}
	h := Static(dist)
	cases := []struct{ path, body, cache string }{
		{"/", "<html>app</html>", "no-cache"},
		{"/index.html", "<html>app</html>", "no-cache"},
		{"/about", "<html>about</html>", "no-cache"},
		{"/about/", "<html>about</html>", "no-cache"},
		{"/about/deep/route", "<html>app</html>", "no-cache"},
		{"/assets/app-1a2b.js", "js", "public, max-age=31536000, immutable"},
		{"/favicon.svg", "<svg/>", "no-cache"},
		{"/assets/", "<html>app</html>", "no-cache"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != c.body {
			t.Errorf("%s: got %d %q", c.path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != c.cache {
			t.Errorf("%s: Cache-Control %q, want %q", c.path, got, c.cache)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.server/entry.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("dot directory served: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	Static(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("empty dist: got %d", rec.Code)
	}
}
