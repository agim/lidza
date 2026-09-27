package router

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza/pkg/middleware"
)

type chunk struct {
	Text string `json:"text"`
}

func TestStream(t *testing.T) {
	release := make(chan struct{})
	r := New()
	Stream(r, "POST /api/v1/echo", func(ctx context.Context, req *Request[createIn], send func(chunk) error) error {
		req.Header().Set("X-Echo", "1")
		if req.Body.Title == "missing" {
			return NotFound("title")
		}
		if err := send(chunk{Text: req.Body.Title}); err != nil {
			return err
		}
		// The first event reaches the client before the handler goes on.
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if req.Body.Title == "fail" {
			return errors.New("secret detail")
		}
		return send(chunk{Text: "done"})
	})
	Stream(r, "GET /api/v1/empty", func(ctx context.Context, req *Request[None], send func(string) error) error {
		return nil
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(middleware.Chain(r, middleware.RequestID(), middleware.Logger(logger), middleware.Timeout(5*time.Second)))
	defer srv.Close()

	post := func(body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/echo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	res := post(`{"title":"hello"}`)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" || res.Header.Get("X-Echo") != "1" {
		t.Fatalf("status %d, headers %v", res.StatusCode, res.Header)
	}
	rd := bufio.NewReader(res.Body)
	if got := readEvent(t, rd); got != "data: {\"text\":\"hello\"}" {
		t.Fatalf("first event %q", got)
	}
	close(release)
	if got := readEvent(t, rd); got != "data: {\"text\":\"done\"}" {
		t.Fatalf("second event %q", got)
	}
	if got := readEvent(t, rd); got != "event: end\ndata: {}" {
		t.Fatalf("end event %q", got)
	}
	res.Body.Close()

	// An error before the first event is an ordinary reply.
	res = post(`{"title":""}`)
	if b, _ := io.ReadAll(res.Body); res.StatusCode != 422 || !strings.Contains(string(b), `"validation"`) {
		t.Fatalf("invalid body: %d %s", res.StatusCode, b)
	}
	res = post(`{"title":"missing"}`)
	if b, _ := io.ReadAll(res.Body); res.StatusCode != 404 || !strings.Contains(string(b), "title not found") {
		t.Fatalf("not found: %d %s", res.StatusCode, b)
	}

	// After it, an error event with the status; the text stays on the server.
	res = post(`{"title":"fail"}`)
	rd = bufio.NewReader(res.Body)
	readEvent(t, rd)
	if got := readEvent(t, rd); got != `event: error`+"\n"+`data: {"error":"internal error","status":500}` {
		t.Fatalf("error event %q", got)
	}
	res.Body.Close()

	// A handler that sends nothing still ends the stream.
	res, err := http.Get(srv.URL + "/api/v1/empty")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(res.Body); string(b) != "event: end\ndata: {}\n\n" {
		t.Fatalf("empty stream %q", b)
	}
}

// readEvent reads one event: its lines up to the blank one.
func readEvent(t *testing.T, rd *bufio.Reader) string {
	t.Helper()
	var lines []string
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (so far %q)", err, lines)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return strings.Join(lines, "\n")
		}
		lines = append(lines, line)
	}
}
