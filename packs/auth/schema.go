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

// TokenTable is the DDL of the one-time tokens table (model AuthToken,
// table auth_token in the schema fragment). Tests create it directly.
const TokenTable = `CREATE TABLE IF NOT EXISTS auth_token (
  id text PRIMARY KEY,
  purpose text NOT NULL,
  subject text NOT NULL,
  hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS auth_token_purpose_idx ON auth_token (purpose);
CREATE INDEX IF NOT EXISTS auth_token_subject_idx ON auth_token (subject);`
