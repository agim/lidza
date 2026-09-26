package auth

// SessionTable is the DDL of the sessions table, matching the schema
// fragment `lidza pack add auth` appends to schema.lidza (model
// AuthSession, table auth_session). Tests create it directly.
const SessionTable = `CREATE TABLE IF NOT EXISTS auth_session (
  id text PRIMARY KEY,
  subject text NOT NULL,
  refresh_hash text NOT NULL UNIQUE,
  prev_refresh_hash text,
  rotated_at timestamptz,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS auth_session_subject_idx ON auth_session (subject);`

// AccountTable is the DDL of the accounts table (model AuthAccount,
// table auth_account in the schema fragment). Tests create it directly.
const AccountTable = `CREATE TABLE IF NOT EXISTS auth_account (
  subject text PRIMARY KEY,
  label text,
  disabled_at timestamptz,
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now()
);`

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

// UserTable is the DDL of the pack's own users (model AuthUser, table
// auth_user): the accounts Mount registers and signs in. An app with its
// own users table does not use it. Tests create it directly.
const UserTable = `CREATE TABLE IF NOT EXISTS auth_user (
  subject text PRIMARY KEY,
  email text UNIQUE,
  name text,
  password_hash text,
  verified_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);`

// IdentityTable is the DDL of the sign-in identities (model
// AuthIdentity, table auth_identity): one row per provider account
// linked to a subject. Tests create it directly.
const IdentityTable = `CREATE TABLE IF NOT EXISTS auth_identity (
  id text PRIMARY KEY,
  provider text NOT NULL,
  provider_subject text NOT NULL,
  subject text NOT NULL,
  email text,
  name text,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS auth_identity_provider_idx ON auth_identity (provider);
CREATE INDEX IF NOT EXISTS auth_identity_subject_idx ON auth_identity (subject);`
