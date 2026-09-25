# Roadmap

Each phase has an acceptance check. A phase is done when its check passes on
`ubuntu01` from a clean checkout. Design background for phases 1–5 is in
`docs/research/2026-09-lidza-design-notes.md`.

## Phase 0: Environment

Install the toolchain described in `docs/environment.md`; add the PATH line
to `~/.profile`; create the `lidza_dev` and `lidza_test` databases.

Check: every command in the "Verification" section of
`docs/environment.md` passes.

## Phase 1: CLI and dev proxy

- `go mod init github.com/agim/lidza`, `cargo new --lib core` (crate
  `lidza-core`), `rust-toolchain.toml`.
- `cmd/lidza`: `lidza dev`, `lidza version`.
- `pkg/devserver`: reverse proxy to the frontend dev server with WebSocket
  (HMR) passthrough and `/api` routed to the in-process router.
- `pkg/router`: `/api/v1/health`.

Check: `bin/lidza dev` running, `curl -i http://127.0.0.1:3000/api/v1/health`
returns JSON, a Vite app on 5173 renders through port 3000 with HMR working.

## Phase 2: Agent interface

- `lidza context`: JSON dump of routes, handler signatures and Rust exports
  to `.lidza/context.json` (`go/ast`, `syn`).
- `lidza check --json`: one JSON diagnostics stream merging `go vet`,
  `staticcheck`, `cargo check --message-format=json` and frontend build
  errors, each with layer, file, line, message.
- `lidza mcp`: MCP server (`mark3labs/mcp-go`) exposing route map, schema
  and runtime logs.
- `/llms.txt` and `/llms-full.txt` served by `lidza dev`.

Check: an agent connected to `lidza mcp` can list routes; introducing a Rust
type error makes `lidza check --json` report it with the right file and line.

## Phase 3: Schema and SDKs

- Single schema source generating Go structs, Rust serde structs and
  migrations.
- OpenAPI 3.1 and JSON Schema regenerated on every reload.
- `pkg/sdk`: TypeScript client (`@lidza/client`) first, Dart second.

Check: changing a handler's response struct updates the generated TypeScript
types without manual steps; `tsc` on the template app fails if the frontend
is out of date.

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
