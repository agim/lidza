package auth

// SessionTable is the DDL of the sessions table, matching the schema
// fragment `lidza pack add auth` appends to schema.lidza (model
// AuthSession, table auth_session). Tests create it directly.
const SessionTable = `CREATE TABLE IF NOT EXISTS auth_session (
  id text PRIMARY KEY,
  subject text NOT NULL,
  refresh_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS auth_session_subject_idx ON auth_session (subject);`
