// Package realtime is the official WebSocket pack: clients subscribe to
// topics over one connection at GET /api/v1/realtime, handlers publish
// JSON to topics, and a Valkey bus fans messages out across nodes so the
// app stays stateless.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
)

// Config comes from the environment.
type Config struct {
	// MaxConns caps open WebSockets on this node; beyond it, 503.
	MaxConns int `env:"REALTIME_MAX_CONNS" default:"10000"`
	// Buffer is the per-connection outbound queue; a client that falls
	// this far behind is disconnected rather than growing memory.
	Buffer int `env:"REALTIME_BUFFER" default:"64"`
	// BusURL is a Valkey or Redis URL (redis://host:6379). Empty keeps
	// fan-out inside the node, which is right for one node only.
	BusURL string `env:"REALTIME_BUS_URL"`
	// WriteTimeout bounds each send to a client.
	WriteTimeout time.Duration `env:"REALTIME_WRITE_TIMEOUT" default:"5s"`
}

// Message is what clients receive.
type Message struct {
	Topic string          `json:"topic"`
	Data  json.RawMessage `json:"data"`
}

// Bus carries published messages between nodes.
type Bus interface {
	Publish(ctx context.Context, topic string, data []byte) error
	// Subscribe delivers every message published on any node until ctx
	// ends; the pack calls it once at start.
	Subscribe(ctx context.Context, deliver func(topic string, data []byte)) error
	Close() error
}

// Hub is the running pack.
type Hub struct {
	cfg  Config
	log  *slog.Logger
	bus  Bus
	stop context.CancelFunc

	mu     sync.RWMutex
	topics map[string]map[*client]struct{}
	conns  int
}

type client struct {
	send   chan Message
	topics map[string]bool
}

// Pack returns the pack for packs.go.
func Pack() lidza.Pack { return &Hub{log: slog.Default(), topics: map[string]map[*client]struct{}{}} }

// New builds a hub outside the pack lifecycle (tests, other transports).
func New(cfg Config, bus Bus) *Hub {
	h := &Hub{cfg: cfg, log: slog.Default(), bus: bus, topics: map[string]map[*client]struct{}{}}
	h.defaults()
	return h
}

func (h *Hub) defaults() {
	if h.cfg.MaxConns <= 0 {
		h.cfg.MaxConns = 10000
	}
	if h.cfg.Buffer <= 0 {
		h.cfg.Buffer = 64
	}
	if h.cfg.WriteTimeout <= 0 {
		h.cfg.WriteTimeout = 5 * time.Second
	}
}

// From returns the hub from a request context.
func From(ctx context.Context) *Hub { return lidza.Service[*Hub](ctx) }

// Name implements lidza.Pack.
func (h *Hub) Name() string { return "lidza/realtime" }

// Start reads the configuration, connects the bus when configured and
// registers the hub.
func (h *Hub) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &h.cfg); err != nil {
		return err
	}
	h.defaults()
	if h.cfg.BusURL != "" {
		bus, err := NewValkeyBus(ctx, h.cfg.BusURL)
		if err != nil {
			return err
		}
		h.bus = bus
	}
	if err := h.start(); err != nil {
		return err
	}
	lidza.Provide(s, h)
	return nil
}

// start subscribes to the bus; every message it delivers is fanned out to
// local subscribers.
func (h *Hub) start() error {
	if h.bus == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.stop = cancel
	ready := make(chan error, 1)
	go func() {
		err := h.bus.Subscribe(ctx, h.deliver)
		select {
		case ready <- err:
		default:
			if err != nil && ctx.Err() == nil {
				h.log.Error("realtime bus subscription ended", "error", err)
			}
		}
	}()
	select {
	case err := <-ready:
		return err
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

// Stop disconnects the bus; open sockets close with the server.
func (h *Hub) Stop(context.Context) error {
	if h.stop != nil {
		h.stop()
	}
	if h.bus != nil {
		return h.bus.Close()
	}
	return nil
}

// Publish sends data (encoded as JSON) to every subscriber of topic on
// every node.
func (h *Hub) Publish(ctx context.Context, topic string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if h.bus != nil {
		return h.bus.Publish(ctx, topic, raw)
	}
	h.deliver(topic, raw)
	return nil
}

// deliver fans a message out to this node's subscribers. A subscriber
// whose queue is full is dropped: it will reconnect, and the node keeps
// its memory bounded.
func (h *Hub) deliver(topic string, data []byte) {
	msg := Message{Topic: topic, Data: data}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.topics[topic] {
		select {
		case c.send <- msg:
		default:
			close(c.send)
			delete(h.topics[topic], c)
		}
	}
}

// Subscribers counts the local subscribers of a topic.
func (h *Hub) Subscribers(topic string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.topics[topic])
}

// Connections counts open sockets on this node.
func (h *Hub) Connections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns
}

func (h *Hub) subscribe(c *client, topics []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range topics {
		if t == "" {
			continue
		}
		if h.topics[t] == nil {
			h.topics[t] = map[*client]struct{}{}
		}
		h.topics[t][c] = struct{}{}
		c.topics[t] = true
	}
}

func (h *Hub) unsubscribe(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for t := range c.topics {
		delete(h.topics[t], c)
		if len(h.topics[t]) == 0 {
			delete(h.topics, t)
		}
	}
	h.conns--
}

// Handler returns the WebSocket endpoint. Register it with
// r.Handle("GET /api/v1/realtime", realtime.Handler()). Clients pass
// ?topics=a,b and may send {"subscribe":["c"]} later; they receive
// {"topic":"a","data":...} per message.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).ServeHTTP(w, r)
	})
}

// ServeHTTP upgrades the connection and streams messages until the client
// leaves or the server shuts down.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.conns >= h.cfg.MaxConns {
		h.mu.Unlock()
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	h.conns++
	h.mu.Unlock()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{r.Host}})
	if err != nil {
		h.mu.Lock()
		h.conns--
		h.mu.Unlock()
		return
	}
	c := &client{send: make(chan Message, h.cfg.Buffer), topics: map[string]bool{}}
	h.subscribe(c, strings.Split(r.URL.Query().Get("topics"), ","))
	defer h.unsubscribe(c)
	ctx := r.Context()

	// Reader: subscription changes from the client, and connection close.
	readErr := make(chan error, 1)
	go func() {
		for {
			var cmd struct {
				Subscribe []string `json:"subscribe"`
			}
			_, data, err := conn.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			if json.Unmarshal(data, &cmd) == nil && len(cmd.Subscribe) > 0 {
				h.subscribe(c, cmd.Subscribe)
			}
		}
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				conn.Close(websocket.StatusPolicyViolation, "too slow")
				return
			}
			data, _ := json.Marshal(msg)
			wctx, cancel := context.WithTimeout(ctx, h.cfg.WriteTimeout)
			err := conn.Write(wctx, websocket.MessageText, data)
			cancel()
			if err != nil {
				conn.Close(websocket.StatusGoingAway, "")
				return
			}
		case err := <-readErr:
			if !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 {
				conn.Close(websocket.StatusNormalClosure, "")
			}
			return
		case <-ctx.Done():
			conn.Close(websocket.StatusGoingAway, "server shutting down")
			return
		}
	}
}
