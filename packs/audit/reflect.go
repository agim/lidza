package audit

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

// Table is the DDL matching the schema fragment `lidza pack add audit`
// appends to schema.lidza (model AuditEvent). Tests create it.
const Table = `CREATE TABLE IF NOT EXISTS audit_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  at timestamptz NOT NULL DEFAULT now(),
  actor text NOT NULL,
  action text NOT NULL,
  resource text NOT NULL DEFAULT '',
  scope text NOT NULL DEFAULT '',
  outcome text NOT NULL,
  request_id text NOT NULL DEFAULT '',
  meta jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_event_at_id_idx ON audit_event (at, id);
CREATE INDEX IF NOT EXISTS audit_event_actor_at_idx ON audit_event (actor, at);
CREATE INDEX IF NOT EXISTS audit_event_scope_at_idx ON audit_event (scope, at);`
