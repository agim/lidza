package db

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/agim/lidza/pkg/tracing"
)

// queryTracer records a span per query under the caller's span (a
// request's, a job's); a query with none, such as the job poller's,
// starts no trace. The span is named by sqlc's query name ("-- name:
// GetPost :one") or the statement's first keyword; the SQL text and the
// arguments are never recorded.
type queryTracer struct{}

type spanKey struct{}

func (queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanFromContext(ctx).SpanContext().IsValid() {
		return ctx
	}
	name, op := queryName(data.SQL)
	ctx, span := tracing.Tracer().Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system.name", "postgresql"), attribute.String("db.operation.name", op)))
	return context.WithValue(ctx, spanKey{}, span)
}

func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(spanKey{}).(trace.Span)
	if !ok {
		return
	}
	span.SetAttributes(attribute.Int64("db.response.returned_rows", data.CommandTag.RowsAffected()))
	tracing.End(span, data.Err)
}

// queryName is the sqlc name and the operation of a statement.
func queryName(sql string) (name, op string) {
	s := strings.TrimSpace(sql)
	for strings.HasPrefix(s, "--") {
		line, rest, _ := strings.Cut(s, "\n")
		if n, ok := strings.CutPrefix(strings.TrimSpace(strings.TrimPrefix(line, "--")), "name:"); ok && name == "" {
			if f := strings.Fields(n); len(f) > 0 {
				name = f[0]
			}
		}
		s = strings.TrimSpace(rest)
	}
	op = "query"
	if f := strings.Fields(s); len(f) > 0 {
		op = strings.ToUpper(f[0])
		if len(op) > 16 {
			op = op[:16]
		}
	}
	if name == "" {
		name = op
	}
	return name, op
}
