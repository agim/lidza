// Package tracing is Līdza's OpenTelemetry tracing: opt-in, configured by
// the standard OTEL_* environment variables, exported over OTLP/HTTP.
//
// With OTEL_EXPORTER_OTLP_ENDPOINT (or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT)
// set, Start installs a tracer provider and the W3C trace context
// propagator; the framework then records a span per request, database
// query, job run, mail send, LLM call and outbound HTTP call, carries
// traceparent across them (into jobs too), and puts trace_id and span_id
// in the logs. Without it nothing is recorded and the spans cost a
// no-op call.
//
// Span attributes never hold request bodies, query arguments, headers or
// message contents: a query's span carries its sqlc name, not its values.
package tracing

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// Instrumentation is the tracer name Līdza's spans carry.
const Instrumentation = "github.com/agim/lidza"

// on is set while Start's provider is installed.
var on atomic.Bool

// On reports whether spans are being recorded (Start installed a
// provider); packs skip work that only feeds spans when it is false.
func On() bool { return on.Load() }

// Enabled reports whether the environment asks for tracing.
func Enabled() bool {
	return os.Getenv("OTEL_SDK_DISABLED") != "true" &&
		(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "")
}

// Start installs the tracer provider when Enabled; service names the
// service unless OTEL_SERVICE_NAME does. The returned function flushes
// and stops it (bounded by its context); it is a no-op when tracing is
// off.
func Start(ctx context.Context, service string) (func(context.Context) error, error) {
	if !Enabled() {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	attrs := []attribute.KeyValue{}
	if os.Getenv("OTEL_SERVICE_NAME") == "" && service != "" {
		attrs = append(attrs, semconv.ServiceName(service))
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	if err != nil {
		return nil, err
	}
	if env, err := resource.New(ctx, resource.WithFromEnv()); err == nil {
		res, _ = resource.Merge(res, env)
	}
	// The sampler follows OTEL_TRACES_SAMPLER (parentbased_always_on by
	// default); the batcher bounds its queue, so a collector that is down
	// costs dropped spans, never memory or request time.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithMaxQueueSize(2048), sdktrace.WithExportTimeout(10*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	on.Store(true)
	return func(ctx context.Context) error {
		on.Store(false)
		return tp.Shutdown(ctx)
	}, nil
}

// Install makes tp the provider, with the W3C propagator, until the
// returned function restores the previous ones: for tests that record
// spans (go.opentelemetry.io/otel/sdk/trace/tracetest).
func Install(tp trace.TracerProvider) (restore func()) {
	prevTP, prevProp, prevOn := otel.GetTracerProvider(), otel.GetTextMapPropagator(), on.Load()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	on.Store(true)
	return func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
		on.Store(prevOn)
	}
}

// Tracer is Līdza's tracer from the installed provider.
func Tracer() trace.Tracer { return otel.Tracer(Instrumentation) }

// Span starts a span named name under ctx's: the helper the packs use.
// End it with End.
func Span(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

// End records err (if any) on span and ends it.
func End(span trace.Span, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// Inject writes ctx's trace context into a carrier (W3C traceparent).
func Inject(ctx context.Context, h http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(h))
}

// Extract reads a trace context from h into ctx.
func Extract(ctx context.Context, h http.Header) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(h))
}

// Traceparent is ctx's span as a W3C traceparent value, "" without a
// recording one: what a job row stores.
func Traceparent(ctx context.Context) string {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ""
	}
	h := http.Header{}
	Inject(ctx, h)
	return h.Get("traceparent")
}

// WithTraceparent continues the trace a traceparent value names.
func WithTraceparent(ctx context.Context, tp string) context.Context {
	if tp == "" {
		return ctx
	}
	return Extract(ctx, http.Header{"Traceparent": {tp}})
}

// Middleware records a server span per request, continuing a
// traceparent the client sent. Route, next to the router, names it by
// the matched pattern ("GET /api/v1/posts/{id}"), never by the raw path.
func Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := Extract(r.Context(), r.Header)
			ctx, span := Tracer().Start(ctx, r.Method, trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(semconv.HTTPRequestMethodKey.String(r.Method)))
			sw := &status{ResponseWriter: w}
			r = r.WithContext(ctx)
			defer func() {
				code := sw.code
				if code == 0 {
					code = http.StatusOK
				}
				span.SetAttributes(semconv.HTTPResponseStatusCode(code))
				if code >= 500 {
					span.SetStatus(codes.Error, http.StatusText(code))
				}
				span.End()
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// Route names the request's span by the pattern the router matched; it
// sits right before the router, where http.Request.Pattern is visible.
func Route() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if r.Pattern != "" {
				span := trace.SpanFromContext(r.Context())
				span.SetName(r.Pattern)
				span.SetAttributes(semconv.HTTPRoute(r.Pattern))
			}
		})
	}
}

type status struct {
	http.ResponseWriter
	code int
}

func (s *status) WriteHeader(code int) {
	if s.code == 0 {
		s.code = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *status) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *status) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Transport traces outbound calls: a client span per request, named by
// method and host, with traceparent sent along.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return transport{base}
}

type transport struct{ base http.RoundTripper }

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	ctx, span := Tracer().Start(r.Context(), r.Method+" "+r.URL.Host, trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(semconv.HTTPRequestMethodKey.String(r.Method), semconv.ServerAddress(r.URL.Hostname())))
	r = r.Clone(ctx)
	Inject(ctx, r.Header)
	res, err := t.base.RoundTrip(r)
	if res != nil {
		span.SetAttributes(semconv.HTTPResponseStatusCode(res.StatusCode))
		if res.StatusCode >= 500 && err == nil {
			span.SetStatus(codes.Error, res.Status)
		}
	}
	End(span, err)
	return res, err
}

// LogHandler adds trace_id and span_id to every record logged with a
// context that carries a recording span (slog.InfoContext and the like).
func LogHandler(h slog.Handler) slog.Handler { return logHandler{h} }

type logHandler struct{ slog.Handler }

func (l logHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return l.Handler.Handle(ctx, r)
}

func (l logHandler) WithAttrs(a []slog.Attr) slog.Handler { return logHandler{l.Handler.WithAttrs(a)} }
func (l logHandler) WithGroup(n string) slog.Handler      { return logHandler{l.Handler.WithGroup(n)} }
