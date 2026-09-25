# Changelog

Releases are git tags; `go install github.com/agim/lidza/cmd/lidza@<tag>`
installs that CLI, and `lidza version` prints it. Apps depend on the same
version in `go.mod`. `install.sh` pins the newest release here;
`scripts/release.sh vX.Y.Z` turns "Unreleased" into a release, bumps the
pin, tags and pushes.

## Unreleased

Lessons from the first app built by an agent on v0.1.2 (a team task
tracker): every gap it worked around by hand is closed here.

- `auth`: sessions slide. `Require` and `Optional` renew an expired
  access cookie from the refresh cookie on the request itself, claims
  carried over, and set both cookies again; a refresh token just
  replaced keeps working for a minute for requests in flight
  (`prevRefreshHash`, `rotatedAt` on `AuthSession`: `lidza gen` writes
  the migration). The refresh cookie's path is `/`; logout clears the
  old path too. No refresh route or client code is needed.
- `jobs`: a handler's context carries the packs (`db.From(ctx)`,
  `mail.From(ctx)`); `EnqueueTx` queues inside the caller's transaction.
- `realtime`: `Handler(realtime.Authorize(fn))` decides per topic who
  may subscribe.
- `mail`: `Link(path)` makes links absolute with `APP_URL`; `WaitFor`
  finds a message in the outbox for tests, waiting for one a job sends.
- `schema.lidza`: `@ref(Model, cascade)` and `@ref(Model, setnull)`.
- Apps own a start hook: `start.go` with `onStart`, wired in `main.go`
  by `lidza new`, where job handlers are registered.
- `lidzatest.Server.Context()`: a context with the app's services.
- `lidza test -v` and `lidza_test` `verbose`; `lidza setup` installs the
  browser for the e2e suite.
- Guide: recipes "Scope a query to the signed-in user" (ownership and
  membership), "Add a background job", "Publish live updates"; a
  reference of schema types and attributes; sessions, links, `WaitFor`,
  `exact: true` locators. The reference app follows all of it.
- `scripts/release.sh`, a test that the installer's pin is the newest
  release in this file, and a CI job on tags that installs the tag.

## v0.1.2 (2026-09-25)

- `lidza setup` and `lidza new --packs ... --agent ...`: packs, `.env` with
  a random `AUTH_SECRET`, `.env.test`, generation, databases created and
  migrated, `node_modules`, the agent CLI, the first commit.
- `lidza ship`: verify, the browser suite, the production build.
- `install.sh` installs the CLI release it was tested with
  (`LIDZA_VERSION`, overridable) instead of `@latest`, and replaces an
  older release on rerun.
- An app's `go.mod` and Dockerfile pin the framework version the CLI was
  built from, a pseudo-version for a build from an untagged commit, so
  the module matches the generated code.

## v0.1.1 (2026-09-25)

- `install.sh` installs the prerequisites (git, curl, C toolchain), Node
  22, and with `--services` Postgres and Valkey with a role for the user;
  `lidza doctor` names the fix per package manager.
- `lidza test` and `DB_MIGRATE` no longer fail on an app whose schema has
  no model yet (no `db/migrations` directory): nothing to apply.
- Every CLI command is an MCP tool (`lidza_gen`, `lidza_gen_resource`,
  `lidza_pack_*`, `lidza_db_*`, `lidza_test`, `lidza_verify`,
  `lidza_build`, `lidza_doctor`, `lidza_recipes`); `lidza_check` runs the
  CLI's check.
- App-scoped recipes: "App recipes" in the guide, `lidza recipe add`,
  `lidza_recipe_add`; the framework's recipes are refreshed by `lidza gen`,
  the app's are not; agent files list both.
- `mail` pack: transactional email behind one `Send` (Mailgun, SendGrid,
  Postmark, Resend, SMTP; `log` and `outbox` providers), templates in
  `mail/`, outbox table, delivery through the jobs pack, `lidza_mail`,
  rule L006 against vendor SDKs; the reference app uses it.

## v0.1.0 (2026-09-25)

The first release: everything in `docs/roadmap.md` phases 0 to 7 and what
followed.

- **CLI**: `new`, `dev`, `build`, `check`, `gen`, `gen resource`, `pack`,
  `db`, `test` (`--e2e`), `verify` (with the pre-commit hook), `benchmark`,
  `doctor`, `context`, `api`, `snippet`, `mcp`, `version`.
- **Contract**: `schema.lidza` generates Go structs with validation, SQL
  and migrations, Rust structs, JSON Schema, the TypeScript client
  `@lidza/client` with validators, the Dart client, OpenAPI 3.1.
- **Control plane**: typed routes (`router.Route`, groups, per-route
  middleware), the middleware pipeline, `/metrics`, `/healthz`, `/readyz`,
  structured logs with request ids, services registry, lifecycle hooks,
  `lidzatest`.
- **Packs**: official Go packs `db`, `auth` (sessions, throttling,
  password policy, one-time tokens, revocation), `jobs`, `cache`, `i18n`
  (time zones), `realtime`, `analytics`; Rust packs `geo` and `media`
  under wazero with memory caps and deadlines, `uninterruptible` for
  input-bounded loops; `lidza pack scaffold` for local packs.
- **Frontends**: react (TanStack Router and Query, Tailwind v4,
  prerendering with the hydration payload, per-request SSR sidecar),
  svelte (prerendered and hydrated), astro (static), htmx (Go templates);
  accessibility lint as errors, browser tests, time zone cookie, opt-in
  analytics reporter in each.
- **Agents**: `lidza check --json` across every layer with rules L001 to
  L005; `lidza mcp` with routes, context, diagnostics, logs, the
  framework's API, the reference app's snippets, pack capabilities and
  the app's own tools; the guide's recipes as MCP prompts and as skills
  for Claude Code and Codex and commands for Gemini CLI; `/llms.txt`;
  `.claude/skills`, `.agents/skills`, `.gemini/commands` written by
  `lidza new` and `lidza gen`.
- **Reference app** `examples/notes`; **evals** (`go test -tags evals`)
  and agent-driven evals (`-tags agenteval`); CI on GitHub Actions.
- **Deployment**: `Dockerfile`, `.dockerignore`, `deploy/<name>.service`
  from `lidza new`; `docs/deploy.md`.
