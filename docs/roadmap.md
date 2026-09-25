# Roadmap

Each phase has an acceptance check. A phase is done when its check passes on
`ubuntu01` from a clean checkout. Design background for phases 1–5 is in
`docs/research/2026-09-lidza-design-notes.md`.

## Phase 0: Environment

Done 2026-09-25. `install.sh` ran on `ubuntu01` (Go 1.27.1, Rust 1.98.1
with both wasm targets, staticcheck, golangci-lint, sqlc, wasm-tools);
`lidza_dev` and `lidza_test` created; the `lidza` CLI installed from the
checkout with `go install ./cmd/lidza`.

Check (passes): `sh install.sh --check` shows `[ok]` for every toolchain
line, and every command in the "Verification" section of
`docs/environment.md` passes.

## Phase 1: CLI and dev proxy

Done 2026-09-25, except publishing.

- `go.mod` (`github.com/agim/lidza`, no dependencies), `core/` (crate
  `lidza-core`, `rust-toolchain.toml` pinning stable plus the wasm targets).
- `cmd/lidza`: `new`, `dev`, `build`, `version`. `lidza new` copies the
  template, writes `main.go`, `routes.go`, `lidza.json`, `CLAUDE.md`,
  `AGENTS.md`, `GEMINI.md`, `docs/lidza-guide.md`, and resolves `go.mod`
  (`--lidza-dir <checkout>` adds a `replace` for framework development).
- `templates/react`: Vite 8 + React 19 + TypeScript, TanStack Router and
  Query, `src/api.ts` as the single place that calls `/api` until the
  generated client exists.
- `pkg/devserver`: reverse proxy with WebSocket passthrough (HMR verified
  through port 3000), static SPA server with `index.html` fallback, and the
  coordinator: frontend dev command, `go build` into `.lidza/app`, restart
  on Go changes (500 ms mtime poll, no watcher dependency).
- `pkg/router`: `GET /api/v1/health`, `router.JSON`, `router.Error`.
- Not done: publishing so `go install github.com/agim/lidza/cmd/lidza@latest`
  works from `install.sh`. The repo is private; Agim decides between making
  it public and documenting a `GOPRIVATE` setup. Until then `install.sh`
  reports `[todo] lidza CLI` on any other machine.

Check (passes): `lidza new demo --lidza-dir <checkout>` then `lidza dev`,
`curl -i http://127.0.0.1:3000/api/v1/health` returns JSON, the React app
on 5173 renders through port 3000 with HMR working, and `lidza build`
yields one binary that serves the app with no Node running.

## Phase 2: Agent interface

Done 2026-09-25.

- `lidza check --json` (`pkg/diag`): `go vet`, `staticcheck`, `cargo check`
  and `tsc` run in parallel; one list of diagnostics with layer, tool,
  severity, file, line, column, code, message. Findings two tools report
  identically (Go compile errors) appear once. Missing tools are reported
  as skipped, not errors. Exit 1 while errors remain.
- `lidza context` (`pkg/inspect`): `.lidza/context.json` with routes found
  by `go/ast` (`HandleFunc`/`Handle` calls with literal patterns; handler
  name, file, line, signature; built-ins marked) and the Rust crate's
  `#[no_mangle] pub extern "C" fn` exports found by scanning the source.
  `syn`-based parsing waits for Phase 4, when pack validation needs full
  Rust signatures.
- `lidza mcp` (`pkg/mcpserver`, `mark3labs/mcp-go`, the first dependency):
  tools `lidza_routes`, `lidza_context`, `lidza_check`, `lidza_logs`
  (tail of `.lidza/dev.log`, which `lidza dev` now writes), `lidza_config`;
  resources `lidza://llms.txt` and `lidza://llms-full.txt`.
- `/llms.txt` and `/llms-full.txt` served in dev mode from `.lidza/`,
  regenerated after every Go rebuild.
- `lidza new` writes `.mcp.json` and `.gemini/settings.json`; the guide
  documents the tools; Codex CLI snippet in `docs/getting-started.md`.

Check (passes): an agent connected to `lidza mcp` can list routes;
introducing a Rust type error makes `lidza check --json` report it with the
right file and line.

## Feature matrix

`docs/features.md` maps every capability a complete framework needs to the
phase that delivers it, and lists the two open decisions (ORM, SSR). The
phases below carry the items it assigns them.

## Phase 3: Schema and SDKs

- Single schema source generating Go structs, Rust serde structs and
  migrations.
- OpenAPI 3.1 and JSON Schema regenerated on every reload.
- `pkg/sdk`: TypeScript client (`@lidza/client`) first, Dart second.
- Remaining templates, each using `@lidza/client` where it has a JS
  toolchain: `svelte` (Vite + Svelte 5), `astro` (static output only),
  `htmx` (Go `templ`, no proxy).
- Middleware pipeline: `router.Use`; request log, panic recovery (JSON
  error), request id, timeouts, CORS, secure headers and CSP; CSRF for
  cookie sessions.
- App lifecycle hooks on `lidza.App`: `OnStart`, `OnReady`, `OnShutdown`.
- `pkg/env`: typed configuration from the environment and `.env.<mode>`.
- Validation rules in the schema source; Go and generated TypeScript
  validators share them.
- Migrations generated from the schema source.
- React error boundary in the template.

Check: changing a handler's response struct updates the generated TypeScript
types without manual steps; `tsc` on the `react` and `svelte` templates
fails if the frontend is out of date; every template passes the Phase 1
check (HMR through the proxy where applicable, single binary in production).

## Phase 4: Packs

- `pack.lidza.json` manifest format; `pkg/pack` parser and validator
  (manifest vs WASM exports vs Go signatures).
- `lidza pack add <name>` (official packs) and `lidza pack scaffold <name>`
  (local packs).
- Packs auto-register into `lidza mcp` on change.
- First official packs: `db` (pgx + sqlc / sqlx, `pgxpool`, `lidza db
  migrate|rollback|status`), `realtime` (coder/websocket, query
  invalidations pushed to the client), `geo` (geozero + rstar), `media`
  (image + zune-jpeg).
- `lidza.Services`: typed service registry (constructors registered once,
  resolved by type at startup and per request); packs register into it.

Check: `lidza pack scaffold demo`, fill in one Rust function, and the
capability shows up in the MCP tool list with a matching Go call.

## Phase 5: Scale primitives

- `pkg/engine/pool.go`: bounded pool of pre-warmed wazero instances with
  per-call timeouts.
- `pkg/telemetry`: `/metrics` (Prometheus/OpenTelemetry), `/healthz`,
  `/readyz`.
- Token-bucket rate limiting on routes; `lidza check` flags global mutable
  maps and unbounded goroutine spawns.
- `lidza benchmark` wrapping k6 scenarios in `benchmarks/`.

Check: `k6 run --vus 500 --duration 1m benchmarks/scale_test.js` against
`lidza dev` completes with flat memory (`go tool pprof` heap before and
after within noise).

## Phase 6: Application services

Official packs on the Phase 4 model:

- `auth`: PASETO or JWT with refresh, Valkey-backed sessions, `lidza auth`
  commands.
- `jobs`: River (Postgres-backed queue), Rust workers for compute.
- `cache`: server-side query cache on Valkey with invalidation hooks.
- `i18n`: `Accept-Language` negotiation, message catalogs, dates, numbers
  and currencies via `golang.org/x/text`, frontend catalog export.
- `lidza test`: `lidza_test` database per run, `httptest` helpers, fake
  clock, recorded HTTP fixtures.

Check: the template app with `auth`, `jobs` and `cache` added passes
`lidza test` offline, and a login survives a restart of the binary.

## Phase 7: Frontend depth

- Prerendering for `react` (static HTML at build time, hydration on the
  client) with TanStack Query dehydrate/hydrate; the SSR decision in
  `docs/features.md` may extend this.
- Accessibility: `eslint-plugin-jsx-a11y` in the templates, findings in
  `lidza check --json`.
- `useLive` in the template: data bound to `realtime` invalidations.

Check: a content page of the template app is served as complete HTML by the
binary and becomes interactive without a second data fetch; an a11y
violation in a page fails `lidza check`.
