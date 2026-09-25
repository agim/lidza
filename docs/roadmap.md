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

- `lidza context`: JSON dump of routes, handler signatures and Rust exports
  to `.lidza/context.json` (`go/ast`, `syn`).
- `lidza check --json`: one JSON diagnostics stream merging `go vet`,
  `staticcheck`, `cargo check --message-format=json` and frontend build
  errors, each with layer, file, line, message.
- `lidza mcp`: MCP server (`mark3labs/mcp-go`) exposing route map, schema
  and runtime logs.
- `/llms.txt` and `/llms-full.txt` served by `lidza dev`.
- `lidza new` writes `.mcp.json` (Claude Code) and `.gemini/settings.json`
  (Gemini CLI) pointing at `lidza mcp`; Codex CLI snippet in the docs.

Check: an agent connected to `lidza mcp` can list routes; introducing a Rust
type error makes `lidza check --json` report it with the right file and line.

## Phase 3: Schema and SDKs

- Single schema source generating Go structs, Rust serde structs and
  migrations.
- OpenAPI 3.1 and JSON Schema regenerated on every reload.
- `pkg/sdk`: TypeScript client (`@lidza/client`) first, Dart second.
- Remaining templates, each using `@lidza/client` where it has a JS
  toolchain: `svelte` (Vite + Svelte 5), `astro` (static output only),
  `htmx` (Go `templ`, no proxy).

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
- First official packs: `db` (pgx + sqlc / sqlx), `realtime`
  (coder/websocket), `geo` (geozero + rstar), `media` (image + zune-jpeg).

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
