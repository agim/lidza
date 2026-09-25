# Līdza

Līdza is a web framework with a Go control plane (HTTP routing, dev proxy,
CLI, MCP server), an optional Rust compute plane (packs compiled to WASM,
run by wazero in a bounded, deadlined pool, for code that must be
contained), and any frontend (Vite, Astro, Svelte, HTMX, Flutter). It is
built for AI agents as the primary developers: machine-readable contracts,
structured diagnostics, no implicit magic.

## Name

- **Līdza** in prose, docs and branding.
- **`lidza`** in every identifier: CLI binary, Go module, crate prefix,
  config file, npm scope.

## Quick start

```sh
curl -fsSL https://raw.githubusercontent.com/agim/lidza/master/install.sh | sh -s -- --services
lidza new myapp && cd myapp
lidza dev
claude        # or: codex, gemini
```

Releases are tags (`CHANGELOG.md`): `go install github.com/agim/lidza/cmd/lidza@v0.1.0`
installs a specific CLI, and an app's `go.mod` pins the same version.

The installer sets up Go, Rust with wasm targets, the helper tools and the
`lidza` CLI, skipping what you already have. `lidza new` writes `CLAUDE.md`,
`AGENTS.md` and `GEMINI.md`, so any of the three agents can start building
at once. Full guide: `docs/getting-started.md`.

## Frontend

Default template: Vite + React + TypeScript. `svelte`, `astro` (static) and
`htmx` (Go-rendered) are selectable at `lidza new --template <name>`. All
follow the same contract: Go owns `/api`, types come from the generated
client, production is one binary.

## Status

Phases 1 to 7 done: `lidza new` with the `react`, `svelte`, `astro` and
`htmx` templates, `lidza dev` (Go and frontend hot reload behind one port),
`lidza build` (one binary), the agent interface (`lidza check --json`,
`lidza context`, `lidza mcp`, `/llms.txt`), `schema.lidza` with generated
Go, SQL, migrations and Rust, typed handlers, OpenAPI and the generated
`@lidza/client`, the middleware pipeline, and packs: Rust capabilities
run as WASM in a bounded pool, official `db`, `realtime`, `geo` and
`media`; scale primitives: `/metrics`, `/healthz`, `/readyz`, rate
limiting, circuit breaker, `lidza check` rules for unbounded state,
`lidza benchmark` on k6; application services: `auth`, `jobs`, `cache`,
`i18n` packs and `lidza test`; frontend depth: prerendered routes,
accessibility in `lidza check`, `useLive`, client-side validators. Every
roadmap phase is delivered. See
`docs/roadmap.md`;
`docs/features.md` maps every capability to a phase.

## Developing the framework

```sh
go vet ./... && staticcheck ./... && go test ./...
(cd core && cargo test)
go build -o bin/lidza ./cmd/lidza
bin/lidza new demo --lidza-dir "$PWD"   # an app pointed at this checkout
```

`docs/environment.md` has the full check list and the host setup.

## Docs

- `docs/getting-started.md`: install, create an app, build it with Claude Code, Codex or Gemini CLI.
- `docs/environment.md`: host inventory, toolchain plan, services, layout, naming, verification.
- `docs/roadmap.md`: phases and acceptance checks.
- `docs/features.md`: feature matrix, what is done, what comes when, open decisions.
- `docs/scalability.md`: the scalable-by-default primitives and the rules every change follows.
- `docs/design.md`: why Līdza is built the way it is.
