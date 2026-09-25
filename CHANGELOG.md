# Changelog

Releases are git tags; `go install github.com/agim/lidza/cmd/lidza@<tag>`
installs that CLI, and `lidza version` prints it. Apps depend on the same
version in `go.mod`.

## Unreleased

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
