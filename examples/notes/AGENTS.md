# notes

A Līdza application: Go control plane, react frontend, one binary.
Read `docs/lidza-guide.md` before changing anything; it has the layout, the
commands and the rules.

<!-- lidza:framework -->
<!-- The framework writes this block: lidza gen and lidza update replace it. A wrong line here is a framework bug to report; the app's own instructions go below it. -->
- Read `docs/brief.md` first: what the app is for, who owns the data, the design, the services and the working agreements below. While it has open questions, start a session with the interview (recipe "Start with the brief": MCP `lidza_brief` and `lidza_brief_answer`; `lidza brief` in a terminal), asking the developer with suggestions, offering to skip any question or the whole brief, and never inventing an answer; a skipped question is never asked again. Lasting facts the developer states go in the Team notes below (`lidza note add`, MCP `lidza_note_add`), never in an agent's local memory: these files and `docs/` are shared through git.
- App-owned Go packages: business logic in `internal/<feature>/`, vendor clients in `internal/providers/<vendor>/`, shared infrastructure in `internal/platform/<name>/`. Keep handlers in `handlers/`; `appDir` holds application wiring and embedded assets. Models and API shapes stay in `schema.lidza`, SQL in `db/queries/*.sql`; generated `schema/` and `db/queries/gen/` are never moved or edited. Recipe: Organize application packages.
- Shared agent guidance: `AGENTS.md` is the one instructions file; `CLAUDE.md` and `GEMINI.md` are the line `@AGENTS.md`, which Claude Code and Gemini CLI expand, and stay that way. Update rules, working agreements and team notes here. Edit recipes in `docs/lidza-guide.md`, then run `lidza gen` to refresh the skills of Claude Code, Codex and Gemini CLI together.

- `lidza dev`: run on http://127.0.0.1:3000 with hot reload for Go and the frontend.
- `lidza check --json`: every Go, Rust and frontend error in one list; run it after each change until `"status": "ok"`. Every `lidza` command is also an MCP tool (`lidza_check`, `lidza_gen`, `lidza_gen_resource`, `lidza_pack_add`, `lidza_db_migrate`, `lidza_test`, `lidza_verify`, `lidza_build`, `lidza_audit_layout`, `lidza_audit_performance`): prefer the tool over a shell.
- `lidza test` for Go tests, `lidza test --e2e` for the browser suite, `lidza doctor` when something is missing on the machine.
- `lidza verify` before committing (the pre-commit hook runs it): generated files staged, no test deleted, skipped or stripped of assertions, check clean, tests green.
- Never weaken a test to make it pass: no deleting it, skipping it or removing its assertions. If a test is wrong, say so to the developer and let them confirm the change (`LIDZA_ALLOW_TEST_CHANGES=1 git commit`); do not confirm it yourself.
- `lidza build`: production binary at `bin/notes`.
- MCP server `lidza mcp` (configured in `.mcp.json`, `.gemini/settings.json` and `.codex/config.toml`): `lidza_routes`, `lidza_context`, `lidza_check`, `lidza_logs`, `lidza_config`, `lidza_api` (the framework's Go API; read it before calling a lidza function), `lidza_snippet` (verified code from the reference app; read it before writing a handler, page, test or tool).
- Task recipes, step by step, in `docs/lidza-guide.md` under "Recipes", as `lidza mcp` prompts, as skills in `.claude/skills/` (Claude Code) and `.agents/skills/` (Codex, `$name`; Gemini CLI, `/name`): <!-- lidza:recipes -->`start-with-brief`, `add-api-route`, `add-resource`, `add-sign-in`, `scope-query-to-signed-in-user`, `add-page`, `set-head-of-page`, `add-responsive-image`, `add-pack-capability`, `add-mcp-tool`, `send-email`, `add-background-job`, `publish-live-updates`, `add-llm-feature`, `store-file`, `receive-webhook`, `connect-external-account`, `add-paginated-filterable-list`, `dates-and-time-zones`, `roles-and-permissions`, `record-audit-event`, `add-admin-pages`, `extend-admin-pages`, `add-recipe`, `write-test`, `organize-application-packages`; this app's own: `scope-query-in-this-app`, `style-page-to-match-app`<!-- /lidza:recipes -->. A pattern this app uses twice is a recipe: `lidza recipe add "Title"`.
- Why the app is built a way (a pack added, Rust for a module, a dependency, a schema tradeoff) is recorded in `docs/decisions.md`: read it before working in those areas, and record yours in the same commit with `lidza decision add "Title" --why "..."` (MCP `lidza_decision_add`).
- Go owns `/api` (`routes.go`, `router.Route` with typed handlers); the frontend never defines API routes and calls them only through `@lidza/client`.
- Data shapes live in `schema.lidza`; `schema/`, `db/`, `packs.go`, `packs/*/pack.go` and `.lidza/` are generated, never edited.
- Add agent-callable functions in `tools.go` (`lidza.ToolFunc`); they show up in `lidza mcp` as `app_<name>`.
- Go first. Rust lives in packs (`lidza pack scaffold`, Rust to WASM) and is for code that must be contained (user-supplied input), a crate Go lacks, or heap pressure; not for speed (see "Rust: when and how" in the guide, with numbers). `lidza pack add db|auth|jobs|cache|i18n|realtime|analytics|mail|llm|storage|geo|media` enables the official ones. Email goes through the `mail` pack, language models through the `llm` pack (chat, structured output from schema types, the app's tools, embeddings; `LLM_PROVIDER=fake` in tests) and files through the `storage` pack (S3-compatible or local), never a vendor SDK. Operator screens go in the admin pages (`admin.Options.Pages` and `Sections`, recipe "Extend the admin pages"), never a second admin app. Sign-in goes through `auth.Mount` (email and password, plus `AUTH_PROVIDERS` for Google, GitHub, Microsoft or an OIDC issuer), never an OAuth library.
- Secrets (API keys, SMTP URLs, storage keys) go in the credentials, sealed with the master key: `lidza credentials set NAME=value` (MCP `lidza_credentials_set`), never in code or a committed file; every pack reads them by their environment name.
<!-- /lidza:framework -->

## Working agreements

<!-- lidza:agreements -->
From the brief (docs/brief.md); follow them in every session.

- Pushing: After every verified commit.
- Tests: A Go test for every handler and a browser test for every page.
- Reports: Per feature: what was built and how it is tested.
<!-- /lidza:agreements -->

## Team notes

Lasting facts about this app that the team and every agent should know, shared
through git: `lidza note add "..."` (MCP `lidza_note_add`). Record them here,
never in an agent's local memory.

- Mail inline files use `Attachment.ContentID` (a bare, unique safe ASCII ID,
  at most 127 bytes) referenced as `cid:<ID>` from HTML. `ReplyTo` accepts an
  address list bounded by `MAIL_MAX_RECIPIENTS`; custom header names are
  unique ignoring case. Keep fields intact through queued retries and test
  MIME nesting plus each provider mapping with local transports.
