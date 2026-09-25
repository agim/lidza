# Getting started

How a developer sets up Līdza and builds an app with an AI agent (Claude
Code, Codex CLI or Gemini CLI) doing the coding.

Status 2026-09-25: `install.sh`, the `lidza` CLI (`new`, `dev`, `build`,
`check`, `gen`, `context`, `mcp`), the agent interface, the four templates
and the generated TypeScript client work.

## Requirements

- Linux or macOS, amd64 or arm64. Windows: use WSL2.
- `curl` and `git`.
- Node 20+ for the `react`, `svelte` and `astro` templates (not for `htmx`).
- Optional: Docker, to run Postgres and Redis locally.
- One agent CLI: `claude`, `codex` or `gemini`.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/agim/lidza/master/install.sh | sh
```

What it does, skipping anything already present:

| Step | Where | Notes |
|---|---|---|
| Go (current stable) | `/usr/local/go` with sudo, else `~/.local/go` | |
| Rust via rustup | `~/.cargo`, `~/.rustup` | adds `wasm32-wasip1`, `wasm32-unknown-unknown` |
| staticcheck, golangci-lint, sqlc | `~/go/bin` | skipped with `--minimal` |
| wasm-tools | `~/.cargo/bin` | skipped with `--minimal` |
| `lidza` CLI | `~/go/bin` | `go install github.com/agim/lidza/cmd/lidza@latest` (Phase 1) |

PATH changes go to `~/.lidza/env`, sourced from `~/.profile`, `~/.bashrc`
and `~/.zshrc`. Nothing else in your shell config is touched. Node,
Postgres and Redis are checked, not installed; the report says what to run.

Flags: `--check` (report only, install nothing), `--minimal`, `--no-sudo`,
`--yes`.

Verify:

```sh
. ~/.lidza/env
sh install.sh --check
```

Every line under "Toolchain" should read `[ok]`.

## Create an app

```sh
lidza new myapp                    # default template: react
lidza new myapp --template svelte  # or astro, htmx
cd myapp
lidza dev
```

`lidza dev` starts the frontend dev server on 5173, builds the app and runs
it on http://127.0.0.1:3000 with the frontend proxied behind it. API routes
live under `/api` (`routes.go`); everything else is the frontend with hot
reload. A change to a Go file rebuilds and restarts the app; `npm install`
runs on first start. `lidza build` produces `bin/myapp`, one binary with
the frontend embedded.

The app is its own Go module requiring `github.com/agim/lidza`. While the
repo is private, `go get` needs `GOPRIVATE=github.com/agim/lidza` and git
access, or pass `--lidza-dir <checkout>` to `lidza new` to use a local
copy of the framework.

`lidza new` writes the agent files for you:

| File | Read by |
|---|---|
| `CLAUDE.md` | Claude Code |
| `AGENTS.md` | Codex CLI (and other AGENTS.md-aware tools) |
| `GEMINI.md` | Gemini CLI |
| `.mcp.json` | Claude Code, project-scoped MCP server |
| `.gemini/settings.json` | Gemini CLI MCP server |
| `docs/lidza-guide.md` | all three; the framework rules in one place |

The three instruction files are short and point at `docs/lidza-guide.md`,
so there is one source of truth. `lidza new` also writes `main.go` (do not
edit), `routes.go` (API handlers), `schema.lidza` (data shapes) and
`lidza.json`.

## Build with an agent

Start the agent in the project directory. It reads its instruction file and
knows the layout, the commands and the rules.

Claude Code:

```sh
claude
```

Codex CLI:

```sh
codex
```

Gemini CLI:

```sh
gemini
```

Then describe the feature. Example prompt:

> Add `GET /api/v1/posts` returning the latest 20 posts from Postgres, and a
> page that lists them. Run `lidza check --json` until it is clean.

The agent loop Līdza is built for:

1. The agent edits Go, Rust or frontend code.
2. `lidza check --json` returns one JSON list of errors across all three
   layers, with file and line.
3. `lidza dev` hot-reloads; the generated client (`@lidza/client`) keeps
   frontend types in sync with the Go handlers, and `schema.lidza` is the
   one place data shapes are declared.
4. `lidza mcp` lets the agent ask for the route map, diagnostics and logs
   instead of grepping.

### MCP server

Tools: `lidza_routes`, `lidza_context`, `lidza_check`, `lidza_logs`,
`lidza_config`; resources `lidza://llms.txt` and `lidza://llms-full.txt`.
While `lidza dev` runs, the same two documents are at
http://127.0.0.1:3000/llms.txt and `/llms-full.txt`.

`lidza new` configures it for Claude Code and Gemini CLI. Codex CLI reads a
user-level file; add:

```toml
# ~/.codex/config.toml
[mcp_servers.lidza]
command = "lidza"
args = ["mcp"]
```

To add it to Claude Code by hand: `claude mcp add lidza -- lidza mcp`.

## Packs

Packs add capabilities: official ones with `lidza pack add <name>` (`db`,
`auth`, `jobs`, `cache`, `i18n`, `realtime`, `geo`, `media`), your own with
`lidza pack scaffold <name>`.
A pack is a Rust crate compiled to WASM and run by the Go binary in a
bounded pool; its manifest `pack.lidza.json` names each capability with
input and output types from `schema.lidza`, and `lidza gen` writes the Go
wrapper (`packs/<name>/pack.go`) so a handler calls
`geo.From(ctx).GeoDistance(ctx, in)`. Go packs (`db`, `realtime`) are
imported from the framework. `lidza mcp` exposes every capability as a
tool, so an agent can try one before wiring it. The first build of a pack
downloads and compiles its crates (about a minute for `media`); later
builds take seconds.

## Frontend

The `react` template prerenders every parameterless route at build time
(`npm run build`: client build, SSR build, `scripts/prerender.mjs`), so
the binary serves complete HTML and the page hydrates in the browser.
`lidza check` runs ESLint with `jsx-a11y`: an inaccessible element is an
error. `useLive(['topic'])` keeps queries fresh from the `realtime` pack,
and `@lidza/client` exports `validators` with the schema rules for forms.

## Tests

`lidza test` runs the Go tests with `LIDZA_MODE=test`: the database named
in `.env.test` is created and migrated first, then `go test ./...`, then
the frontend check. A test boots the whole app with
`lidzatest.Start(t, app())` and talks to it over HTTP with a JSON client
that keeps cookies; `lidza new` writes `routes_test.go` as the example.
`CACHE_URL=memory` in `.env.test` keeps the cache in-process. In handlers,
read time with `lidza.Now(ctx)` and call other services with
`lidza.HTTPClient(ctx)`: the test then freezes the clock
(`srv.Clock.Set`) and replays recorded HTTP (`lidzatest.WithRecorder`,
recorded once with `LIDZA_RECORD=1`).

Browser tests: `lidza test --e2e` builds the app, starts the binary and
runs the Playwright suite in `e2e/`. Playwright pins a browser build per
package version and finds it itself (`PLAYWRIGHT_BROWSERS_PATH` or its
cache); when the build is missing, the command says so and prints the
install command, or installs it with `--install`. On CI, run
`npx playwright install --with-deps chromium` after `npm ci`, and cache
`~/.cache/ms-playwright` keyed by the Playwright version.

`lidza doctor` reports the toolchain, the services and the project's
prerequisites (`node_modules`, built packs, the e2e browser), each missing
item with the command that fixes it; run it first on a new machine.

## Operations

Every app serves `/healthz`, `/readyz` (every pack's readiness check) and
`/metrics` (Prometheus). `lidza benchmark` runs `benchmarks/scale_test.js`
with k6 against the running app, warms it up, then reports requests,
latency, heap and goroutines before and after; a release should read
"memory flat".

## Local services

The first app runs without a database. When you need one:

```sh
docker run -d --name lidza-pg -e POSTGRES_PASSWORD=lidza -p 5432:5432 postgres:17
docker run -d --name lidza-redis -p 6379:6379 redis:7
```

`DATABASE_URL` and `REALTIME_BUS_URL` go in `.env` (see `.env.example`
after `lidza pack add`); `install.sh --check` reports whether both services
are reachable.

## Troubleshooting

- `lidza: command not found`: open a new shell or `. ~/.lidza/env`.
- Installer asks for a password: it found `sudo` but not passwordless; rerun
  with `--no-sudo` to keep everything under `$HOME`.
- `go install` of the CLI fails: the CLI is not published yet (Phase 1) or
  the repo is private for your account. The toolchain is still complete.
- Windows: run everything inside WSL2 (Ubuntu). Native Windows is not
  supported yet.
