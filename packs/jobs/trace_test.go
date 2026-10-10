package jobs

import (
	"context"
	"encoding/json"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/agim/lidza/pkg/tracing"
)

// TestJobContinuesTrace: with tracing on, a job enqueued under a
// request's span runs in the same trace, its queries traced under the
// run; a job table without trace_parent (an app not yet migrated) still
// enqueues and runs.
func TestJobContinuesTrace(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	t.Cleanup(tracing.Install(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))))
	q := testQueue(t, 1)
	ran := make(chan trace.SpanContext, 2)
	q.Handle("report", func(ctx context.Context, _ json.RawMessage) error {
		var n int
		q.pool.QueryRow(ctx, "-- name: CountReports :one\nSELECT 1").Scan(&n)
		ran <- trace.SpanContextFromContext(ctx)
		return nil
	})
	q.Run()
	defer q.Stop(context.Background())

	ctx, request := tracing.Span(context.Background(), "GET /api/v1/reports")
	id, err := q.Enqueue(ctx, "report", nil)
	request.End()
	if err != nil {
		t.Fatal(err)
	}
	run := <-ran
	waitState(t, q, id, "done")
	if run.TraceID() != request.SpanContext().TraceID() {
		t.Fatal("the job did not continue the request's trace")
	}
	var job, query bool
	for _, s := range rec.Ended() {
		switch s.Name() {
		case "job report":
			job = s.SpanContext().TraceID() == run.TraceID()
		case "CountReports":
			query = s.Parent().SpanID() == run.SpanID()
		}
	}
	if !job || !query {
		t.Fatalf("job span %v, query span under it %v", job, query)
	}

	// Before the migration that adds the column.
	if _, err := q.pool.Exec(context.Background(), `ALTER TABLE job DROP COLUMN trace_parent`); err != nil {
		t.Fatal(err)
	}
	q.traceColumn.Store(0)
	ctx, other := tracing.Span(context.Background(), "POST /api/v1/reports")
	id, err = q.Enqueue(ctx, "report", nil)
	other.End()
	if err != nil {
		t.Fatal(err)
	}
	<-ran
	waitState(t, q, id, "done")
}
