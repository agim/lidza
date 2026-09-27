package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/validate"
)

// StreamHandler is a typed streaming handler: the request as Route decodes
// it, and send, which writes one event to the client and flushes it.
type StreamHandler[In, Event any] func(ctx context.Context, req *Request[In], send func(Event) error) error

// Stream registers a streaming handler at a ServeMux pattern. The body is
// decoded and validated as Route does it. The reply is server-sent events
// (text/event-stream): each send is one event whose data is the Event as
// JSON, and the stream closes with an "end" event once the handler
// returns nil. The generated clients read it as an async iterable (TS)
// or a Stream (Dart):
//
//	router.Stream(r, "POST /api/v1/curate", curate)
//
//	func curate(ctx context.Context, req *router.Request[schema.CurateInput], send func(string) error) error {
//		_, err := llm.From(ctx).Stream(ctx, llm.Request{
//			Messages: []llm.Message{{Role: llm.User, Content: req.Body.Prompt}},
//		}, send)
//		return err
//	}
//
// An error the handler returns before its first send replies as Route
// does, with the status and a JSON body. After it the headers are gone:
// the error is sent as an "error" event, its data the same body plus
// "status", and the stream closes. send fails once the client has gone
// or the request's deadline (App.StreamTimeout for a client that
// accepts text/event-stream) has passed; return then. send
// may be called from several goroutines, not after the handler returns.
func Stream[In, Event any](r *Router, pattern string, h StreamHandler[In, Event], mw ...middleware.Middleware) {
	_, noBody := any(*new(In)).(None)
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		typed := &Request[In]{Raw: req}
		if !noBody && hasBody(req) {
			if err := decodeBody(req, &typed.Body); err != nil {
				Error(w, http.StatusBadRequest, err.Error())
				return
			}
			if v, ok := any(typed.Body).(validate.Validator); ok {
				if err := v.Validate(); err != nil {
					writeError(w, req, err)
					return
				}
			}
		}
		s := &sseWriter{w: w, rc: http.NewResponseController(w), ctx: req.Context()}
		err := h(req.Context(), typed, func(ev Event) error {
			data, err := json.Marshal(ev)
			if err != nil {
				return fmt.Errorf("stream event: %w", err)
			}
			return s.event("", data, typed.apply)
		})
		s.mu.Lock()
		defer s.mu.Unlock()
		s.done = true
		if err != nil {
			if !s.started {
				writeError(w, req, err)
				return
			}
			status, body := errorReply(req, err)
			_ = s.writeLocked("error", streamError(status, body))
			return
		}
		if !s.started {
			typed.apply(w)
			s.start()
		}
		_ = s.writeLocked("end", []byte("{}"))
	})
	r.Handle(pattern, middleware.Chain(handler, mw...))
}

// sseWriter writes server-sent events, starting the reply on the first.
type sseWriter struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	rc      *http.ResponseController
	ctx     context.Context
	started bool
	done    bool
}

func (s *sseWriter) start() {
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	// Proxies such as nginx buffer a reply unless told not to.
	h.Set("X-Accel-Buffering", "no")
	s.w.WriteHeader(http.StatusOK)
	s.started = true
}

func (s *sseWriter) event(name string, data []byte, apply func(http.ResponseWriter)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return errors.New("stream: send after the handler returned")
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if !s.started {
		apply(s.w)
		s.start()
	}
	return s.writeLocked(name, data)
}

// writeLocked writes one event and flushes it; data is one line of JSON.
func (s *sseWriter) writeLocked(name string, data []byte) error {
	buf := make([]byte, 0, len(data)+32)
	if name != "" {
		buf = append(buf, "event: "+name+"\n"...)
	}
	buf = append(buf, "data: "...)
	buf = append(buf, data...)
	buf = append(buf, "\n\n"...)
	if _, err := s.w.Write(buf); err != nil {
		return err
	}
	return s.rc.Flush()
}

// streamError is the data of an "error" event: the error body Route
// would send, with the status it would have had.
func streamError(status int, body any) []byte {
	data, _ := json.Marshal(body)
	var m map[string]any
	if json.Unmarshal(data, &m) != nil || m == nil {
		m = map[string]any{"error": http.StatusText(status)}
	}
	m["status"] = status
	out, _ := json.Marshal(m)
	return out
}
