package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebSocketUpgradeDeadlineClassification(t *testing.T) {
	for _, test := range []struct {
		name, method, upgrade string
		connection            []string
		stream                bool
	}{
		{"upgrade", "GET", "websocket", []string{"Upgrade"}, true},
		{"mixed tokens", "GET", "WebSocket", []string{"keep-alive, uPgRaDe"}, true},
		{"multiple headers", "GET", "websocket", []string{"keep-alive", "Upgrade"}, true},
		{"missing connection", "GET", "websocket", nil, false},
		{"partial token", "GET", "websocket", []string{"not-upgrade"}, false},
		{"other protocol", "GET", "h2c", []string{"Upgrade"}, false},
		{"wrong method", "POST", "websocket", []string{"Upgrade"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/api/v1/realtime", nil)
			request.Header.Set("Upgrade", test.upgrade)
			request.Header["Connection"] = test.connection
			StreamTimeout(time.Second, time.Hour)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || (test.stream && remaining < 59*time.Minute) || (!test.stream && remaining > time.Second) {
					t.Fatalf("unexpected request lifetime: deadline=%v remaining=%v", ok, remaining)
				}
			})).ServeHTTP(httptest.NewRecorder(), request)
		})
	}
}

func TestWebSocketSurvivesOrdinaryDeadlineAndKeepsStreamBound(t *testing.T) {
	finished := make(chan error, 1)
	server := httptest.NewServer(StreamTimeout(20*time.Millisecond, time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			finished <- err
			return
		}
		defer conn.CloseNow()
		<-time.After(60 * time.Millisecond)
		if err := conn.Write(r.Context(), websocket.MessageText, []byte("still connected")); err != nil {
			finished <- err
			return
		}
		<-r.Context().Done()
		finished <- r.Context().Err()
	})))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+server.URL[4:], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_, message, err := conn.Read(ctx)
	if err != nil || string(message) != "still connected" {
		t.Fatalf("ordinary API deadline disconnected WebSocket: %s %v", message, err)
	}
	select {
	case err := <-finished:
		if err != context.DeadlineExceeded {
			t.Fatalf("stream did not retain its deadline: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("unbounded WebSocket lifetime")
	}
}
