# Changelog

Releases are git tags; `go install github.com/agim/lidza/cmd/lidza@<tag>`
installs that CLI, and `lidza version` prints it. Apps depend on the same
version in `go.mod`. `install.sh` pins the newest release here;
`scripts/release.sh vX.Y.Z` turns "Unreleased" into a release, bumps the
pin, tags and pushes.

## v0.1.7 (2026-09-25)

- TLS without a proxy: `LIDZA_TLS_DOMAINS=app.example.com` makes the
  binary serve HTTPS on 443 with Let's Encrypt certificates
  (`golang.org/x/crypto/acme/autocert`), renewed on their own, and
  redirect 80. Certificates are stored in Postgres through the db pack
  (`tls_certificate`, shared by every node) or, for one node,
  `LIDZA_TLS_CACHE_DIR`. `APP_URL` and `AUTH_COOKIE_SECURE` follow from
  the domain unless set. `lidza doctor` checks the DNS record, the ports
  and the right to bind them; the systemd unit grants
  `CAP_NET_BIND_SERVICE`; `docs/deploy.md` has the section.

## v0.1.6 (2026-09-25)

- `llm` pack: language models behind one `Chat`, `Stream`, `Embed`,
  `Generate[T]` (structured output from a schema type, validated) and
  `Run` (the app's `lidza.Tool` values offered to the model, its calls
  run with the packs in the context). Anthropic, OpenAI, Google and
  Ollama spoken directly over HTTP; `fake` scripts replies in tests.
  Deadlines, retries on 429 and 5xx with Retry-After, token counts on
  `/metrics`, the MCP tool `lidza_llm`, rule L007 against vendor SDKs
  and client libraries, the recipe "Add an LLM feature", and the
  reference app's tag suggestion as the snippet `llm-handler`.

## v0.1.5 (2026-09-25)

- A per-app decision log, `docs/decisions.md`: why the app is built a
  way (a pack added, Rust for a module, a dependency, a schema tradeoff),
  written with `lidza decision add "Title" --why "..."` or the MCP tool
  `lidza_decision_add`, read as `lidza://decisions`. `lidza new` creates
  it, `lidza gen` adds it to an existing app with the line in its agent
  files; rule 12 and the pack recipe point at it.

## v0.1.4 (2026-09-25)

- A change to a foreign key's delete rule (`@ref(Model)` to `@ref(Model,
  cascade)` or back) gets a migration: the constraint is dropped and
  added again with the new rule.

## v0.1.3 (2026-09-25)

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
