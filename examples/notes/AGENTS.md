# notes

A Līdza application: Go control plane, react frontend, one binary.
`docs/lidza-guide.md` has the layout, the commands and the rules; read the
section a task needs, not the file (its recipes are the skills). This file is
read whole at the start of every session: keep it short, and put lasting
facts in `docs/notes.md`, reasons in `docs/decisions.md`, area notes in `docs/`.

<!-- lidza:framework -->
<!-- The framework writes this block: lidza gen and lidza update replace it. A wrong line here is a framework bug to report; the app's own instructions go below it. -->
- Read `docs/brief.md` first: what the app is for, who owns the data, the design, the services and the working agreements below. While it has open questions, start a session with the interview (recipe "Start with the brief": MCP `lidza_brief` and `lidza_brief_answer`; `lidza brief` in a terminal), asking the developer with suggestions, offering to skip any question or the whole brief, and never inventing an answer; a skipped question is never asked again. Lasting facts the developer states go in `docs/notes.md` (`lidza note add`, MCP `lidza_note_add`; `lidza note list --grep word`, MCP `lidza_notes`, finds the ones for an area), never in an agent's local memory: these files and `docs/` are shared through git.
- App-owned Go packages: business logic in `internal/<feature>/`, vendor clients in `internal/providers/<vendor>/`, shared infrastructure in `internal/platform/<name>/`. Keep handlers in `handlers/`; `appDir` holds application wiring and embedded assets. Keep the root clean: integration tests in `tests/`, unit tests beside their non-root package, browser specs in `e2e/`, documentation in `docs/`, scripts in `scripts/`, scratch/build output in `.lidza/` or `bin/`. Root tests and stray files fail L020; use the importable app factory, never import package main. Models and API shapes stay in `schema.lidza`, SQL in `db/queries/*.sql`; generated `schema/` and `db/queries/gen/` are never moved or edited. Recipe: Organize application packages.
- Shared agent guidance: `AGENTS.md` is the one instructions file; `CLAUDE.md` and `GEMINI.md` are the line `@AGENTS.md`, which Claude Code and Gemini CLI expand, and stay that way. Update rules, working agreements and team notes here. Edit recipes in `docs/lidza-recipes.md`, then run `lidza gen` to refresh the skills of Claude Code, Codex and Gemini CLI together.

- `lidza dev`: run on http://127.0.0.1:3000 with hot reload for Go and the frontend.
- `lidza check --json`: every Go, Rust and frontend error in one list; run it once after a batch of edits (it type-checks the whole program), until `"status": "ok"`, then `lidza test`. The dependency scanners run only when a lock file changed or a day passed (`--security` forces them). The `lidza` commands are MCP tools too (`lidza_check`, `lidza_gen`, `lidza_gen_resource`, `lidza_pack_add`, `lidza_db_migrate`, `lidza_test`, `lidza_verify`, `lidza_build`, `lidza_audit_layout`, `lidza_audit_performance`): prefer the tool over a shell. `new`, `install`, `dev`, `update` and `benchmark` stay in the developer's terminal. Each enabled pack has a tool showing what it holds: `lidza_jobs`, `lidza_mail`, `lidza_hooks`, `lidza_billing`, `lidza_audit`, `lidza_workspaces`, `lidza_storage`, `lidza_errors`.
- `lidza test` for Go tests, `lidza test --e2e` for the browser suite, `lidza doctor` when something is missing on the machine.
- `lidza verify` before committing (the pre-commit hook runs it again, and skips what already passed on the same tree): generated files staged, no test deleted, skipped or stripped of assertions, check clean, tests green. A check or a test run that passed on the unchanged tree is reused, not repeated.
- Live-update acceptance gate: before calling realtime/polling UI work ready, test repeated updates, slow and failed reads, short and sustained reconnects, and pause/resume. Assert retained values, stable DOM identity/order/geometry and chart colors, scroll/focus/drafts, and absence of transient fetch-state text on desktop and phone throughout the refresh interval. New data must actually appear and real failures remain visible. Run `lidza test --e2e` with those fixtures as well as `lidza verify`; respect explicit browser restrictions and report missing rendered evidence as unverified instead of silently waiving the gate. Recipe: Publish live updates.
- Never weaken a test to make it pass: no deleting it, skipping it or removing its assertions. If a test is wrong, say so to the developer and let them confirm the change (`LIDZA_ALLOW_TEST_CHANGES=1 git commit`); do not confirm it yourself.
- `lidza build`: production binary at `bin/notes`.
- MCP server `lidza mcp` (configured in `.mcp.json`, `.gemini/settings.json` and `.codex/config.toml`): `lidza_routes`, `lidza_context`, `lidza_check`, `lidza_logs`, `lidza_config`, `lidza_api` (the framework's Go API; read it before calling a lidza function), `lidza_snippet` (verified code from the reference app; read it before writing a handler, page, test or tool).
- Task recipes, step by step, in `docs/lidza-recipes.md`, as `lidza mcp` prompts, as skills in `.claude/skills/` (Claude Code) and `.agents/skills/` (Codex, `$name`; Gemini CLI, `/name`): <!-- lidza:recipes -->`start-with-brief`, `add-api-route`, `add-resource`, `add-sign-in`, `scope-query-to-signed-in-user`, `add-page`, `set-head-of-page`, `add-responsive-image`, `add-pack-capability`, `add-mcp-tool`, `send-email`, `add-background-job`, `publish-live-updates`, `add-llm-feature`, `store-file`, `receive-webhook`, `send-webhooks`, `connect-external-account`, `add-paginated-filterable-list`, `dates-and-time-zones`, `roles-and-permissions`, `add-teams-and-invitations`, `add-payments`, `sell-feature-to-teams`, `record-audit-event`, `add-admin-pages`, `extend-admin-pages`, `add-recipe`, `write-test`, `organize-application-packages`; this app's own: `scope-query-in-this-app`, `style-page-to-match-app`<!-- /lidza:recipes -->. A pattern this app uses twice is a recipe: `lidza recipe add "Title"`.
- Why the app is built a way (a pack added, Rust for a module, a dependency, a schema tradeoff) is recorded in `docs/decisions.md`: `lidza decision list --grep <area>` (MCP `lidza_decisions`) finds the ones for an area before you work in it, and record yours in the same commit with `lidza decision add "Title" --why "..."` (MCP `lidza_decision_add`).
- Go owns `/api` (`app/routes.go`, `router.Route` with typed handlers); the frontend never defines API routes and calls them only through `@lidza/client`.
- Data shapes live in `schema.lidza`; `schema/`, `db/`, `app/packs.go`, `packs/*/pack.go` and `.lidza/` are generated, never edited.
- Add agent-callable functions in `app/tools.go` (`lidza.ToolFunc`); they show up in `lidza mcp` as `app_<name>`.
- Go first. Rust lives in packs (`lidza pack scaffold`, Rust to WASM) and is for code that must be contained (user-supplied input), a crate Go lacks, or heap pressure; not for speed (see "Rust: when and how" in the guide, with numbers). `lidza pack add db|auth|jobs|cache|i18n|realtime|analytics|mail|llm|storage|geo|media` enables the official ones. Email goes through the `mail` pack, language models through the `llm` pack (chat, structured output from schema types, the app's tools, embeddings; `LLM_PROVIDER=fake` in tests) and files through the `storage` pack (S3-compatible or local), never a vendor SDK. Operator screens go in the admin pages (`admin.Options.Pages` and `Sections`, recipe "Extend the admin pages"), never a second admin app. Sign-in goes through `auth.Mount` (email and password, plus `AUTH_PROVIDERS` for Google, GitHub, Microsoft or an OIDC issuer), never an OAuth library.
- The app's settings (API keys, providers, addresses, senders) can be kept in the app: in its database, sealed with the master key and switched on the admin pages or with `lidza settings set NAME=value` (MCP `lidza_settings_set`), or in the sealed credentials file; the environment works too and wins over both. Never put a secret in code or a plain committed file (`docs/configuration.md` marks each setting `app` or `infra`). A sandbox key for `lidza dev` alone can seed the credentials file: `lidza credentials set dev.NAME=value`.
<!-- /lidza:framework -->

## Working agreements

<!-- lidza:agreements -->
From the brief (docs/brief.md); follow them in every session.

- Pushing: After every verified commit.
- Tests: A Go test for every handler and a browser test for every page.
- Reports: Per feature: what was built and how it is tested.
<!-- /lidza:agreements -->

## Team notes

Lasting facts about this app that the team and every agent should know are in
`docs/notes.md`, shared through git: `lidza note add "..."` (MCP `lidza_note_add`)
records one; `lidza note list --grep word` (MCP `lidza_notes`) finds the ones for an
area. Never in an agent's local memory.

- Mail inline files use `Attachment.ContentID` (a bare, unique safe ASCII ID,
  at most 127 bytes) referenced as `cid:<ID>` from HTML. `ReplyTo` accepts an
  address list bounded by `MAIL_MAX_RECIPIENTS`; custom header names are
  unique ignoring case. Keep fields intact through queued retries and test
  MIME nesting plus each provider mapping with local transports.
