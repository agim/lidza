# Account preferences

The auth pack stores bounded, non-sensitive display choices for the current
account. They belong to the account, not a refresh session or a browser. Use
preferences for layouts, sort order, filters and selections. Do not put secrets,
roles, resource permissions, commands or destructive-action confirmations here.
Applications validate the meaning and allowed values of their own keys.

`auth.Mount` exposes authenticated routes:

- `GET /api/v1/auth/preferences` returns `{"subject":"<current account>","values":{}}`.
- `PATCH /api/v1/auth/preferences` takes `{"values":{"products.view":"table"}}`.
  Null removes an individual key. Unrelated keys remain intact. The reply is the
  updated document. An optional `account` must match the authenticated subject;
  a mismatch returns 409. It guards a delayed write across a sign-in change and
  cannot select another owner. Both routes send `Cache-Control: private, no-store`.

The matching Go methods are `auth.From(ctx).Preferences(ctx)` and
`auth.From(ctx).PatchPreferences(ctx, auth.PreferencePatch{Values: ...})`.
They derive identity from `auth.CurrentUser(ctx)` and refuse anonymous, missing
and disabled accounts. HTTP routes also enforce the normal session/CSRF guards.
Preferences never enter session claims or authorize an operation.

The document allows 128 keys, ASCII namespaced keys of at most 96 characters,
4096 bytes per JSON value and 32768 bytes for the compact complete document.
Both the incoming patch and merged document are validated. Patches serialize on
the preference row in PostgreSQL and roll back if the merged limits fail, so
concurrent edits of unrelated keys are retained. Queries have a five-second
deadline (or the caller's earlier deadline); request bodies are bounded.

The auth schema fragment adds `AuthPreference`/`auth_preference`. Run normal
generation/migration before using these routes after an upgrade. Applications
that explicitly create the pack's DDL can use `auth.PreferenceTable` instead.
The table has no session foreign key and supports `Login` with an app-owned user
table. `DeleteUser`/`DeleteUserTx` remove preferences in the account transaction;
deletion still works on a schema that has not adopted preferences yet.

A frontend should load the account document before applying its defaults,
validate stored values, merge edits made during a slow initial read, serialize
pending patches, and flush pending display choices before ordinary navigation
or sign-out. If a retry buffer is used, scope it to the returned subject and
never display it before the current identity is known. Do not persist passwords,
tokens or form/action state alongside display preferences. Give an explicit URL
selection precedence over a saved list filter.

API inspection (`lidza api packs/auth`) documents the exported types/methods.
There is no MCP tool to inspect another account's personal display choices.
