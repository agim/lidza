package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/agim/lidza"
)

func serve(t *testing.T, h *Hub) *httptest.Server {
	t.Helper()
	return serveWith(t, h, Handler(Authorize(AllowAll)))
}

func serveWith(t *testing.T, h *Hub, handler http.Handler) *httptest.Server {
	t.Helper()
	s := lidza.NewServices()
	lidza.Provide(s, h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(lidza.WithServices(r.Context(), s)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server, topics string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/realtime?topics="+topics, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func read(t *testing.T, conn *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPublishSubscribe(t *testing.T) {
	h := New(Config{Buffer: 4}, nil)
	srv := serve(t, h)
	a := dial(t, srv, "orders,alerts")
	b := dial(t, srv, "orders")
	for h.Subscribers("orders") < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if err := h.Publish(context.Background(), "orders", map[string]int{"id": 1}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*websocket.Conn{a, b} {
		m := read(t, c)
		if m.Topic != "orders" || string(m.Data) != `{"id":1}` {
			t.Fatalf("got %+v", m)
		}
	}
	h.Publish(context.Background(), "alerts", "boom")
	if m := read(t, a); m.Topic != "alerts" || string(m.Data) != `"boom"` {
		t.Fatalf("got %+v", m)
	}

	// Late subscription over the socket.
	b.Write(context.Background(), websocket.MessageText, []byte(`{"subscribe":["alerts"]}`))
	for h.Subscribers("alerts") < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	h.Publish(context.Background(), "alerts", 2)
	read(t, a)
	if m := read(t, b); m.Topic != "alerts" {
		t.Fatalf("late subscribe: %+v", m)
	}
	if h.Connections() != 2 {
		t.Fatalf("connections %d", h.Connections())
	}
	a.Close(websocket.StatusNormalClosure, "")
	for h.Connections() != 1 {
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLimits(t *testing.T) {
	h := New(Config{MaxConns: 1, Buffer: 2}, nil)
	srv := serve(t, h)
	c := dial(t, srv, "t")
	// Subscribed, not only counted: the hub counts a connection before the
	// upgrade, and a publish before the subscription reaches no one.
	for h.Subscribers("t") < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	res, err := http.Get(srv.URL + "/api/v1/realtime?topics=t")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("over the limit: %d", res.StatusCode)
	}
	// A slow client is dropped once its buffer overflows, not buffered forever.
	for i := 0; i < 10; i++ {
		h.Publish(context.Background(), "t", i)
	}
	for h.Subscribers("t") != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("expected policy violation close, got %v", err)
			}
			break
		}
	}
}

func TestValkeyBus(t *testing.T) {
	url := os.Getenv("LIDZA_TEST_BUS_URL")
	if url == "" {
		url = "redis://127.0.0.1:6379"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bus, err := NewValkeyBus(ctx, url)
	if err != nil {
		t.Skipf("no bus: %v", err)
	}
	defer bus.Close()
	h1 := New(Config{}, bus)
	if err := h1.start(); err != nil {
		t.Fatal(err)
	}
	defer h1.Stop(ctx)
	bus2, _ := NewValkeyBus(ctx, url)
	defer bus2.Close()
	h2 := New(Config{}, bus2)
	if err := h2.start(); err != nil {
		t.Fatal(err)
	}
	defer h2.Stop(ctx)

	srv := serve(t, h2)
	c := dial(t, srv, "cross")
	for h2.Subscribers("cross") < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // subscriptions settle on the bus
	if err := h1.Publish(ctx, "cross", "node"); err != nil {
		t.Fatal(err)
	}
	if m := read(t, c); m.Topic != "cross" || string(m.Data) != `"node"` {
		t.Fatalf("cross-node: %+v", m)
	}
}

// TestAuthorize: a refused topic is never delivered, on the query string
// or a later subscribe; allowed ones are.
func TestAuthorize(t *testing.T) {
	h := New(Config{Buffer: 8, WriteTimeout: time.Second}, nil)
	s := lidza.NewServices()
	lidza.Provide(s, h)
	handler := Handler(Authorize(func(r *http.Request, topic string) bool {
		return r.URL.Query().Get("user") == "u1" && strings.HasPrefix(topic, "public")
	}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(lidza.WithServices(r.Context(), s)))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/realtime?user=u1&topics=public,private", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"subscribe":["private2","public2"]}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (h.Subscribers("public2") == 0) {
		time.Sleep(10 * time.Millisecond)
	}
	if h.Subscribers("private") != 0 || h.Subscribers("private2") != 0 || h.Subscribers("public") != 1 || h.Subscribers("public2") != 1 {
		t.Fatalf("subscriptions: private %d private2 %d public %d public2 %d", h.Subscribers("private"), h.Subscribers("private2"), h.Subscribers("public"), h.Subscribers("public2"))
	}
	h.Publish(ctx, "private", "secret")
	h.Publish(ctx, "public2", "hello")
	if m := read(t, conn); m.Topic != "public2" {
		t.Fatalf("got %+v, want the public2 message first (private must not arrive)", m)
	}
}

// TestClosedByDefault: without Authorize, Handler and Hub.ServeHTTP
// accept the connection but subscribe it to nothing, on the query string
// or a later subscribe; server-side publishing still works.
func TestClosedByDefault(t *testing.T) {
	for name, handler := range map[string]func(*Hub) http.Handler{
		"Handler":   func(*Hub) http.Handler { return Handler() },
		"ServeHTTP": func(h *Hub) http.Handler { return h },
	} {
		t.Run(name, func(t *testing.T) {
			h := New(Config{Buffer: 8, WriteTimeout: time.Second}, nil)
			srv := serveWith(t, h, handler(h))
			conn := dial(t, srv, "a,b")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"subscribe":["c"]}`)); err != nil {
				t.Fatal(err)
			}
			// Give the reader time to handle the subscribe.
			time.Sleep(100 * time.Millisecond)
			for _, topic := range []string{"a", "b", "c"} {
				if n := h.Subscribers(topic); n != 0 {
					t.Fatalf("%s: %d subscribers, want 0", topic, n)
				}
			}
			if err := h.Publish(ctx, "a", "x"); err != nil {
				t.Fatalf("publish: %v", err)
			}
			rctx, rcancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer rcancel()
			if _, data, err := conn.Read(rctx); err == nil {
				t.Fatalf("received %s on a refused topic", data)
			}
		})
	}
}

// TestAllowAll opens every topic, on the query string and later.
func TestAllowAll(t *testing.T) {
	h := New(Config{Buffer: 8, WriteTimeout: time.Second}, nil)
	conn := dial(t, serve(t, h), "a")
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"subscribe":["b"]}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (h.Subscribers("a") == 0 || h.Subscribers("b") == 0) {
		time.Sleep(5 * time.Millisecond)
	}
	if h.Subscribers("a") != 1 || h.Subscribers("b") != 1 {
		t.Fatalf("subscribers: a %d b %d", h.Subscribers("a"), h.Subscribers("b"))
	}
	h.Publish(context.Background(), "b", 1)
	if m := read(t, conn); m.Topic != "b" {
		t.Fatalf("got %+v", m)
	}
}

// A slow subscriber of two topics, dropped for one, is gone from both:
// a publish on the other must not send on its closed queue (a panic).
func TestDropLeavesEveryTopic(t *testing.T) {
	h := New(Config{MaxConns: 4, Buffer: 1}, nil)
	c := &client{send: make(chan Message, 1), topics: map[string]bool{}}
	h.mu.Lock()
	h.conns++
	h.mu.Unlock()
	h.subscribe(c, []string{"a", "b"})
	h.Publish(context.Background(), "a", 1)
	h.Publish(context.Background(), "a", 2) // overflows: dropped
	if h.Subscribers("a") != 0 || h.Subscribers("b") != 0 {
		t.Fatalf("still subscribed: a=%d b=%d", h.Subscribers("a"), h.Subscribers("b"))
	}
	h.Publish(context.Background(), "b", 3) // would panic before
	h.subscribe(c, []string{"b"})
	if h.Subscribers("b") != 0 {
		t.Fatal("a dropped client subscribed again")
	}
}
