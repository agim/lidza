package lidza

import (
	"context"
	"github.com/agim/lidza/pkg/tracing"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"os"
	"strings"

	"github.com/agim/lidza/pkg/devserver"
	"github.com/agim/lidza/pkg/middleware"
)

// Logging environment.
const (
	// EnvLog selects the format: "json" or "text". Default: text under
	// lidza dev and lidza test, json otherwise.
	EnvLog = "LIDZA_LOG"
	// EnvLogLevel is debug, info, warn or error; default info.
	EnvLogLevel = "LIDZA_LOG_LEVEL"
)

// NewLogger builds the app logger from the environment.
func NewLogger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv(EnvLogLevel)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	format := strings.ToLower(os.Getenv(EnvLog))
	if format == "" {
		mode := os.Getenv(devserver.EnvMode)
		if mode == "dev" || mode == "test" {
			format = "text"
		} else {
			format = "json"
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	// A record logged with a request's context carries its trace_id and
	// span_id when tracing is on.
	return slog.New(tracing.LogHandler(h))
}

// Log returns the app logger for a request, with the request id attached
// so every line of one request can be found together. Outside a request
// it is the app logger.
func Log(ctx context.Context) *slog.Logger {
	log := slog.Default()
	if s, _ := ctx.Value(servicesKey{}).(*Services); s != nil {
		if l, ok := s.Lookup(typeOf[*slog.Logger]()); ok {
			log = l.(*slog.Logger)
		}
	}
	if id := middleware.GetRequestID(ctx); id != "" {
		log = log.With("request_id", id)
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		log = log.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	return log
}
