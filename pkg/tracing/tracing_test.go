package tracing

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func record(t *testing.T) *tracetest.SpanRecorder {
	rec := tracetest.NewSpanRecorder()
	t.Cleanup(Install(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))))
	return rec
}

// TestRequestSpan: a request continues the client's trace, is named by
// the route pattern, and an outbound call from it carries the trace on.
func TestRequestSpan(t *testing.T) {
	rec := record(t)
	var gotParent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotParent = r.Header.Get("traceparent")
	}))
	defer upstream.Close()

	var buf bytes.Buffer
	log := slog.New(LogHandler(slog.NewJSONHandler(&buf, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/posts/{id}", func(w http.ResponseWriter, r *http.Request) {
		client := &http.Client{Transport: Transport(nil)}
		req, _ := http.NewRequestWithContext(r.Context(), "GET", upstream.URL, nil)
		res, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		res.Body.Close()
		log.InfoContext(r.Context(), "handled")
		w.WriteHeader(http.StatusTeapot)
	})
	h := Middleware()(Route()(mux))
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	r := httptest.NewRequest("GET", "/api/v1/posts/42", nil)
	r.Header.Set("traceparent", parent)
	h.ServeHTTP(httptest.NewRecorder(), r)

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("%d spans", len(spans))
	}
	server, client := spans[1], spans[0]
	if server.Name() != "GET /api/v1/posts/{id}" || server.SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || server.Parent().SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("server span %q trace %s parent %s", server.Name(), server.SpanContext().TraceID(), server.Parent().SpanID())
	}
	if client.Parent().SpanID() != server.SpanContext().SpanID() || !strings.Contains(gotParent, "4bf92f3577b34da6a3ce929d0e0e4736") {
		t.Fatalf("client span parent %s, sent %q", client.Parent().SpanID(), gotParent)
	}
	for _, a := range server.Attributes() {
		if string(a.Key) == "url.path" || strings.Contains(a.Value.String(), "42") && string(a.Key) != "http.response.status_code" {
			t.Errorf("raw path recorded: %v", a)
		}
	}
	if !strings.Contains(buf.String(), `"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`) {
		t.Fatalf("log without trace_id: %s", buf.String())
	}
}

func TestTraceparentRoundTrip(t *testing.T) {
	record(t)
	if Traceparent(context.Background()) != "" {
		t.Fatal("traceparent without a span")
	}
	ctx, span := Span(context.Background(), "enqueue")
	tp := Traceparent(ctx)
	span.End()
	_, child := Span(WithTraceparent(context.Background(), tp), "run")
	defer child.End()
	if child.SpanContext().TraceID() != span.SpanContext().TraceID() {
		t.Fatal("the run did not continue the trace")
	}
}

func TestOffByDefault(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	stop, err := Start(context.Background(), "app")
	if err != nil || On() {
		t.Fatalf("on without an endpoint: %v", err)
	}
	stop(context.Background())
}
