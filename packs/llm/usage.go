package llm

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UsageTable is the DDL of the usage table, matching the schema fragment
// `lidza pack add llm` appends to schema.lidza (model LLMUsage, table
// llm_usage). Tests create it directly.
const UsageTable = `CREATE TABLE IF NOT EXISTS llm_usage (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  at timestamptz NOT NULL DEFAULT now(),
  provider text NOT NULL,
  model text NOT NULL,
  label text NOT NULL,
  input integer NOT NULL,
  output integer NOT NULL,
  ms integer NOT NULL,
  status text NOT NULL,
  error text
);
CREATE INDEX IF NOT EXISTS llm_usage_at_idx ON llm_usage (at);
CREATE INDEX IF NOT EXISTS llm_usage_label_idx ON llm_usage (label);`

// TrackUsage records every call in the llm_usage table of pool; Start
// does it when the db pack runs. Tests call it with their pool.
func (l *LLM) TrackUsage(pool *pgxpool.Pool) { l.pool = pool }

// record writes one call. A failure to record is logged, never returned:
// accounting must not break the feature.
func (l *LLM) record(ctx context.Context, req Request, res Response, elapsed time.Duration, err error) {
	if l.pool == nil {
		return
	}
	status, errText := "ok", (*string)(nil)
	if err != nil {
		status = "error"
		s := err.Error()
		if len(s) > 500 {
			s = s[:500]
		}
		errText = &s
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, werr := l.pool.Exec(wctx, `INSERT INTO llm_usage (provider, model, label, input, output, ms, status, error) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		l.cfg.Provider, or(res.Model, req.Model), req.Label, res.Usage.Input, res.Usage.Output, int(elapsed.Milliseconds()), status, errText); werr != nil {
		l.log.Warn("llm: usage not recorded", "error", werr)
	}
}

// UsageRow is one line of a usage report: a day, a provider, a model and
// a label with their calls and tokens.
type UsageRow struct {
	Day      string `json:"day"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Label    string `json:"label"`
	Calls    int64  `json:"calls"`
	Errors   int64  `json:"errors"`
	Input    int64  `json:"input"`
	Output   int64  `json:"output"`
}

// Usage reports the calls since a time, grouped by day, provider, model
// and label, newest day first. Needs the db pack.
func (l *LLM) Usage(ctx context.Context, since time.Time) ([]UsageRow, error) {
	if l.pool == nil {
		return nil, errNoUsage
	}
	rows, err := l.pool.Query(ctx, `SELECT to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day, provider, model, label,
		count(*), count(*) FILTER (WHERE status <> 'ok'), coalesce(sum(input), 0), coalesce(sum(output), 0)
		FROM llm_usage WHERE at >= $1 GROUP BY 1, 2, 3, 4 ORDER BY 1 DESC, 5 DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Day, &r.Provider, &r.Model, &r.Label, &r.Calls, &r.Errors, &r.Input, &r.Output); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Call is one recorded call.
type Call struct {
	At       time.Time `json:"at"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Label    string    `json:"label"`
	Input    int       `json:"input"`
	Output   int       `json:"output"`
	Ms       int       `json:"ms"`
	Status   string    `json:"status"`
	Error    *string   `json:"error,omitempty"`
}

// RecentCalls returns the newest recorded calls. Needs the db pack.
func (l *LLM) RecentCalls(ctx context.Context, limit int) ([]Call, error) {
	if l.pool == nil {
		return nil, errNoUsage
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := l.pool.Query(ctx, `SELECT at, provider, model, label, input, output, ms, status, error FROM llm_usage ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Call
	for rows.Next() {
		var c Call
		if err := rows.Scan(&c.At, &c.Provider, &c.Model, &c.Label, &c.Input, &c.Output, &c.Ms, &c.Status, &c.Error); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type usageError struct{}

func (usageError) Error() string {
	return "llm: usage is recorded through the db pack; list lidza/db before lidza/llm in lidza.json"
}

var errNoUsage = usageError{}
