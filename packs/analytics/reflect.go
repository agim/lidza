package analytics

import "reflect"

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// Tables is the DDL matching the schema fragment `lidza pack add analytics`
// appends to schema.lidza (models AppError and AppEvent). Tests create it.
const Tables = `CREATE TABLE IF NOT EXISTS app_error (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source text NOT NULL,
  message text NOT NULL,
  stack text,
  route text,
  method text,
  url text,
  request_id text,
  user_id text,
  user_agent text,
  fingerprint text NOT NULL,
  extra jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS app_error_fingerprint_idx ON app_error (fingerprint);
CREATE INDEX IF NOT EXISTS app_error_created_at_idx ON app_error (created_at);
CREATE TABLE IF NOT EXISTS app_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  props jsonb NOT NULL DEFAULT '{}',
  url text,
  session_id text,
  user_id text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS app_event_name_created_at_idx ON app_event (name, created_at);`
