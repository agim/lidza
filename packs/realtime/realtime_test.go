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
	s := lidza.NewServices()
	lidza.Provide(s, h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Handler().ServeHTTP(w, r.WithContext(lidza.WithServices(r.Context(), s)))
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
	for h.Connections() < 1 {
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
