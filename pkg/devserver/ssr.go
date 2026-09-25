package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// SSRDir is the directory inside dist that holds the server bundle and
// the sidecar script, written by the react template's build.
const SSRDir = ".server"

// EnvSSR enables per-request rendering: "1" starts the sidecar.
const EnvSSR = "LIDZA_SSR"

// Sidecar renders pages per request through a Node process running the
// app's SSR bundle, reached over a Unix socket. It is optional: without
// it, or when it fails, the prerendered or static page is served.
type Sidecar struct {
	dist     fs.FS
	log      *slog.Logger
	apiBase  string
	dir      string
	socket   string
	template []byte
	proc     *proc
	client   *http.Client
	timeout  time.Duration
}

// HasSidecar reports whether dist carries the sidecar files.
func HasSidecar(dist fs.FS) bool {
	for _, f := range []string{"ssr-server.mjs", "entry-server.js", "index.html"} {
		if _, err := fs.Stat(dist, path.Join(SSRDir, f)); err != nil {
			return false
		}
	}
	return true
}

// NewSidecar prepares a sidecar for dist. apiBase is the app's own
// address, where loaders reach the API during a render.
func NewSidecar(dist fs.FS, apiBase string, log *slog.Logger) (*Sidecar, error) {
	if !HasSidecar(dist) {
		return nil, fmt.Errorf("dist has no %s/ssr-server.mjs and entry-server.js (build with the react template's `npm run build`)", SSRDir)
	}
	if _, err := exec.LookPath("node"); err != nil {
		return nil, errors.New("LIDZA_SSR=1 needs node on PATH")
	}
	tpl, err := fs.ReadFile(dist, path.Join(SSRDir, "index.html"))
	if err != nil {
		return nil, err
	}
	return &Sidecar{dist: dist, log: log, apiBase: apiBase, template: tpl, timeout: 3 * time.Second}, nil
}

// Start extracts the bundle to a temporary directory and starts node.
func (s *Sidecar) Start(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "lidza-ssr-")
	if err != nil {
		return err
	}
	s.dir = dir
	entries, err := fs.ReadDir(s.dist, SSRDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(s.dist, path.Join(SSRDir, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	s.socket = filepath.Join(dir, "ssr.sock")
	cmd := exec.Command("node", filepath.Join(dir, "ssr-server.mjs"), s.socket)
	cmd.Dir = dir
	p, err := startProc(cmd, slogWriter{s.log}, "[ssr]   ")
	if err != nil {
		return err
	}
	s.proc = p
	s.client = &http.Client{
		Timeout: s.timeout,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", s.socket)
		}},
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p.exited() {
			return errors.New("ssr sidecar exited at start")
		}
		if conn, err := net.Dial("unix", s.socket); err == nil {
			conn.Close()
			s.log.Info("ssr sidecar ready", "socket", s.socket)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("ssr sidecar did not open its socket in 10s")
}

// Stop ends the process and removes the extracted files.
func (s *Sidecar) Stop(context.Context) error {
	if s.proc != nil {
		s.proc.stop(stopGrace)
	}
	if s.dir != "" {
		return os.RemoveAll(s.dir)
	}
	return nil
}

// Handler renders HTML page requests through the sidecar and passes
// everything else, and every failure, to next.
func (s *Sidecar) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isPageRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		html, err := s.render(r)
		if err != nil {
			s.log.Warn("ssr render failed, serving the static page", "path", r.URL.Path, "error", err)
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "Cookie, Accept-Language")
		_, _ = w.Write(html)
	})
}

func isPageRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/html") && r.Header.Get("Accept") != "" && r.Header.Get("Accept") != "*/*" {
		return false
	}
	last := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	return !strings.Contains(last, ".")
}

func (s *Sidecar) render(r *http.Request) ([]byte, error) {
	headers := map[string]string{}
	for _, h := range []string{"Cookie", "Accept-Language", "Authorization"} {
		if v := r.Header.Get(h); v != "" {
			headers[strings.ToLower(h)] = v
		}
	}
	body, _ := json.Marshal(map[string]any{"path": r.URL.RequestURI(), "headers": headers, "apiBase": s.apiBase})
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://ssr/render", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var out struct {
		HTML  string `json:"html"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK || out.Error != "" {
		return nil, fmt.Errorf("sidecar: %s", firstLineOf(out.Error))
	}
	// The sidecar returns the whole page (markup and hydration payload in
	// the template); a bare fragment is placed into the template here.
	if trimmed := bytes.TrimSpace([]byte(out.HTML)); bytes.HasPrefix(bytes.ToLower(trimmed), []byte("<!doctype")) || bytes.HasPrefix(trimmed, []byte("<html")) {
		return []byte(out.HTML), nil
	}
	marker := []byte(`<div id="root"></div>`)
	if !bytes.Contains(s.template, marker) {
		return nil, errors.New("index.html has no root element")
	}
	return bytes.Replace(s.template, marker, []byte(`<div id="root">`+out.HTML+`</div>`), 1), nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// slogWriter forwards the sidecar's output lines to the app log.
type slogWriter struct{ log *slog.Logger }

func (w slogWriter) Write(p []byte) (int, error) {
	w.log.Info(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
