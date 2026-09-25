package jobs

import "reflect"

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// JobTable is the DDL of the job table, matching the schema fragment
// `lidza pack add jobs` appends to schema.lidza (model Job). Tests create
// it directly.
const JobTable = `CREATE TABLE IF NOT EXISTS job (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind text NOT NULL,
  payload jsonb NOT NULL,
  state text NOT NULL DEFAULT 'pending',
  run_at timestamptz NOT NULL DEFAULT now(),
  attempts integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 5,
  locked_at timestamptz,
  finished_at timestamptz,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS job_state_run_at_idx ON job (state, run_at);`
