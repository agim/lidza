# Environment

Written 2026-09-25. Describes the development host for Līdza and the
toolchain it needs. The toolchain plan was run on 2026-09-25
(`sh install.sh`, on Agim's go-ahead); Phase 0 is done and every command
under "Verification" passes.

## Host

`ubuntu01`, Linux 7.0.0-31-generic, 4 vCPU, 15 GiB RAM, ~177 GB free on `/`.

### Present

| Tool | Version | Path / notes |
|---|---|---|
| go | 1.27.1 | `/usr/local/go/bin/go`; `GOPATH=/home/agim/go` |
| rustc, cargo | 1.98.1 | rustup, `stable`; `~/.cargo/bin`; targets `wasm32-wasip1`, `wasm32-unknown-unknown` |
| staticcheck | 2026.2.1 | `~/go/bin` |
| golangci-lint | 2.14.0 | `~/go/bin` |
| sqlc | 1.31.1 | `~/go/bin` |
| wasm-tools | 1.259.0 | `~/.cargo/bin` |
| k6 | 2.3.0 | apt, `dl.k6.io` repository (installed 2026-09-25 for Phase 5) |
| lidza | from this checkout | `~/go/bin/lidza`; `go install ./cmd/lidza` after pulling |
| node | 22.23.2 | `/home/agim/.local/bin/node` |
| codex | 0.157.0 | `npm install -g @openai/codex` (installed 2026-09-25 for the agent evals); needs a ChatGPT login or `OPENAI_API_KEY` to run |
| gemini | 0.61.0 | `npm install -g @google/gemini-cli` (installed 2026-09-25 for the agent evals); needs a Google login or `GEMINI_API_KEY` to run |
| npm | 10.9.8 | |
| ruby | 3.4.5 | rbenv; not used by Līdza |
| psql | 17.11 | client for the local Postgres 17 |
| docker | 29.8.1 | daemon running; `agim` is not in the `docker` group, run `sg docker -c '...'` |
| gh | 2.45.0 | authenticated as `agim` |
| tmux | system | |
| claude | | `/home/agim/.local/bin/claude` |
| sudo | | passwordless for `agim` |

### Missing

hey, wasmtime, wasm-pack, pnpm, bun. None is needed.

## Toolchain plan

Automated by `install.sh` at the repo root (`sh install.sh`; `--check` to
report without installing; `--services` to also install and start
Postgres and Valkey and create a database role for the current user;
`--minimal` to skip the helper tools; `--no-sudo` to print the root
steps instead of running them). Run on `ubuntu01` 2026-09-25, and
verified the same day in a clean `ubuntu:24.04` container as a non-root
user with sudo (`--yes --services --minimal`, then `lidza new`, `lidza
pack add db`, `lidza test` against the installed Postgres). The `apt`
branch is the one exercised; the `dnf`, `pacman` and Homebrew branches
follow those systems' documented commands and have not been run here.
The manual steps below are the reference the script implements; each
has a check command.

### Prerequisites

`git`, `curl` and a C toolchain (Rust needs a linker): `build-essential`
on Debian and Ubuntu, `gcc make` on Fedora, `base-devel` on Arch, the
Xcode command line tools on macOS. The installer adds them when missing.

### Node → `~/.local/opt/node-v22.23.2-<os>-<arch>`

The official tarball from nodejs.org, pinned in the installer
(`NODE_VERSION`), its `bin` on the PATH through `~/.lidza/env`. Check:
`node --version`.

### Services (optional, `--services`)

Postgres from the system package manager, started (systemd, or the
`service` scripts where systemd does not run), with a superuser role
named after the current user so the templates' socket DSN
`postgres:///app_dev?host=/var/run/postgresql` needs no password; Valkey
(Redis where Valkey is not packaged) on 6379. `lidza doctor` prints the
one-line install for the machine's package manager when a service is
missing.

### Go → `/usr/local/go`

```sh
GO_VERSION=$(curl -s 'https://go.dev/VERSION?m=text' | head -1)   # e.g. go1.27.1
curl -fsSLO "https://go.dev/dl/${GO_VERSION}.linux-amd64.tar.gz"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "${GO_VERSION}.linux-amd64.tar.gz"
rm "${GO_VERSION}.linux-amd64.tar.gz"
```

Check: `go version` and `go env GOPATH` (expect `/home/agim/go`).

### Rust → `~/.cargo`, `~/.rustup`

```sh
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile default
rustup target add wasm32-wasip1 wasm32-unknown-unknown
```

Check: `cargo --version`, `rustc --version`,
`rustup target list --installed` (expect both wasm targets).

### Helper tools

```sh
go install honnef.co/go/tools/cmd/staticcheck@2026.2.1
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
cargo install wasm-tools --version 1.259.0 --locked
```

Check: `staticcheck -version`, `golangci-lint --version`, `sqlc version`,
`wasm-tools --version`.

### k6

`install.sh` does not install it; `lidza benchmark` needs it. Installed
on `ubuntu01` 2026-09-25 with:

```sh
sudo gpg -k && sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg \
  --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" \
  | sudo tee /etc/apt/sources.list.d/k6.list
sudo apt-get update && sudo apt-get install -y k6
```

Check: `k6 version`.

Optional, only when a phase needs them: `wasmtime` (run WASM outside Go),
`pnpm` (frontend templates).

### Version pinning

- `go.mod`: `go` directive set to the installed minor version; `toolchain`
  directive pins the patch.
- `core/rust-toolchain.toml`: `channel = "stable"` plus the two wasm
  targets, so `cargo` picks the same toolchain on every machine.

## PATH

`install.sh` wrote `~/.lidza/env`:

```sh
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export PATH="$HOME/.cargo/bin:$PATH"
```

and sources it from `~/.profile` and `~/.bashrc`. The session unit carries
the same directories in `Environment=PATH` (see "Agent session"). A shell
that predates the install needs `. ~/.lidza/env`.

## Local services

| Service | Address | Notes |
|---|---|---|
| Postgres 17 | 127.0.0.1:5432 | cluster `main`, data on `/mnt/faststorage/pgdata`. Databases `lidza_dev` and `lidza_test` (created 2026-09-25). Auth: the Unix socket is `peer`, so `psql -U agim` works (`agim` is a superuser); TCP on 127.0.0.1 is `scram-sha-256` and `agim` has no password set yet. |
| Redis | 127.0.0.1:6379 | stand-in for Valkey; same protocol, `valkey-go` works against it. |
| Pebble | 127.0.0.1:14000 | Let's Encrypt's ACME test authority in a Docker container (`docker run -d --name pebble -e PEBBLE_VA_ALWAYS_VALID=1 -p 127.0.0.1:14000:14000 -p 127.0.0.1:15000:15000 ghcr.io/letsencrypt/pebble:latest -config /test/config/pebble-config.json`); the TLS issuance test (`go test . -run TestServeTLSIssues`) obtains a real certificate from it and skips when it is down. Check: `curl -sk https://localhost:14000/dir`. |
| RustFS | 127.0.0.1:9000 | an S3-compatible server in a Docker container (`docker run -d --name rustfs -p 127.0.0.1:9000:9000 -e RUSTFS_ACCESS_KEY=lidza -e RUSTFS_SECRET_KEY=lidzalidza ghcr.io/rustfs/rustfs:latest`; MinIO's images were not pullable from this host); the storage pack's live test (`go test ./packs/storage -run TestS3Live`) runs against it and skips when it is down. Check: `curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9000/` (403). |
| Ollama | 127.0.0.1:11434 | a Docker container (`docker run -d --name ollama -p 127.0.0.1:11434:11434 -v ollama:/root/.ollama ollama/ollama`) with `qwen2.5:0.5b` and `all-minilm` pulled; the llm pack's live test runs against it and skips when it is down. Check: `curl -s http://127.0.0.1:11434/api/tags`. |
| Docker | `/var/run/docker.sock` | root:docker; use `sg docker -c '<cmd>'`. |

Nothing else runs on ports 3000 or 5173.

## Ports

| Port | Use |
|---|---|
| 3000 | `lidza dev` proxy (127.0.0.1) |
| 5173 | frontend dev server (Vite/Astro default) |
| 5432 | Postgres |
| 6379 | Redis |

## Repository layout

```
lidza/
├── lidza.go          package lidza: the runtime an app binary calls (lidza.Run)
├── cmd/lidza/        CLI: new, dev, build, check, gen, pack, db, test, verify, benchmark, doctor, context, api, snippet, mcp, version
├── pkg/
│   ├── config/       lidza.json
│   ├── devserver/    reverse proxy, static SPA server, hot-reload coordinator
│   ├── diag/         `lidza check`: go vet, staticcheck, cargo check, tsc, svelte-check
│   ├── engine/       wazero host: compiled module, bounded instance pool, JSON calls
│   ├── env/          typed configuration from .env files and the environment
│   ├── inspect/      routes and typed operations via go/ast and go/packages, OpenAPI, llms.txt
│   ├── lidzatest/    boots an app for Go tests: packs, httptest server, JSON client
│   ├── mcpserver/    `lidza mcp`
│   ├── apidoc/       the framework's public API rendered from its sources (`lidza api`, lidza://api)
│   ├── recipes/      the guide's recipes as MCP prompts and Claude Code skills
│   ├── snippets/     the reference app's files embedded for lidza_snippet (`go generate` syncs _files/)
│   ├── middleware/   request id, log, recovery, timeout, body limit, CORS, secure headers
│   ├── pack/         pack manifests, validator, generators, scaffold, build, official pack sources
│   ├── router/       control plane: HTTP router, typed Route[In, Out], /api/v1/health
│   ├── resilience/   circuit breaker for outbound calls
│   ├── scaffold/     `lidza new`: template copy plus generated Go and agent files
│   ├── schema/       schema.lidza parser and generators (Go, SQL, migrations, Rust, JSON Schema)
│   ├── sdk/          client generators: @lidza/client (TypeScript), lidza_client (Dart)
│   ├── telemetry/    /metrics, /healthz, /readyz, request metrics
│   ├── validate/     rule helpers and the 422 error shape
│   └── version/      build version
├── packs/            official Go packs
│   ├── auth/         passwords, tokens, sessions, Require middleware
│   ├── cache/        Valkey cache with Remember; memory backend for tests
│   ├── db/           pgxpool, migrations
│   ├── i18n/         catalogs, locale negotiation, formatting
│   ├── jobs/         Postgres job queue with bounded workers
│   └── realtime/     WebSocket topics, Valkey bus
├── core/             Rust crate `lidza-core`: the WASM ABI (src/abi.rs)
├── examples/notes/   the reference app (its own module, replace => ../..): auth, an owned resource, a page, tests, a tool
├── evals/            platform evals (build tag evals): each agent mistake caught, guidance present, verify green
├── templates/
│   ├── embed.go      embeds the template directories into the CLI
│   ├── react/        default: Vite + React + TypeScript, TanStack Router and Query, Tailwind, prerendering, eslint with jsx-a11y
│   ├── svelte/       Vite + Svelte 5 + TypeScript
│   ├── astro/        Astro, static output only
│   └── htmx/         Go html/template views, htmx vendored, no JS toolchain
├── docs/
├── install.sh
├── go.mod            dependencies: mcp-go, x/tools, wazero, pgx, coder/websocket, valkey-go, prometheus/client_golang, golang-jwt, x/crypto, x/text
└── README.md
```

An app created by `lidza new` is a separate Go module that requires
`github.com/agim/lidza`: `main.go` embeds `dist/` and calls `lidza.Run`,
`routes.go` registers `/api` handlers, `packs.go` (generated) lists the
packs, and the frontend lives at the app root (`package.json`, `src/`).
Packs live under `packs/<name>` with their Rust crate and built module;
official Go packs are imported from this repo. `lidza dev` builds that
module into `.lidza/app` and runs it with `LIDZA_MODE=dev`, so dev and
production run the same binary.

## Frontend

Default template: **Vite + React + TypeScript SPA** with TanStack Router and
Query. Chosen at `lidza new --template <name>`; `react` when omitted.
Package manager: npm (already installed; no pnpm).

| Template | Dev server | Production |
|---|---|---|
| `react` | Vite on 5173, proxied by `lidza dev` | `dist/` embedded in the Go binary, parameterless routes prerendered to `dist/<path>/index.html`; `LIDZA_SSR=1` renders per request through the Node sidecar in `dist/.server` |
| `svelte` | Vite on 5173, proxied | same |
| `astro` | Astro dev on 5173, proxied; `output: 'static'` only | same |
| `htmx` | none; Go renders `html/template` views from `views/` | Go binary only |

Flutter/Dart is a client SDK target (`lidza-dart`), not a web template.

Contract every template follows:

- The frontend never defines `/api` routes; Go owns them.
- Types come only from the generated client (`@lidza/client`, in
  `.lidza/client`, aliased in the template); no hand-written fetch wrappers.
- Frontend build errors are ingested by `lidza check --json` alongside Go and
  Rust diagnostics.
- One binary in production: Go serves the built assets, no Node at runtime.

`lidza.json`, `frontend` block:

```json
{ "template": "react", "dev": "npm run dev -- --port 5173",
  "url": "http://127.0.0.1:5173", "dist": "dist" }
```

For `htmx`: `{ "template": "htmx", "watch": ["views", "static"] }`; `watch`
lists directories whose changes make `lidza dev` rebuild.

## Naming

| Boundary | Form | Example |
|---|---|---|
| Brand, docs, prose | Līdza | "Līdza dev server" |
| CLI binary | `lidza` | `lidza dev`, `lidza build`, `lidza new` |
| Go module | `github.com/agim/lidza` | `import "github.com/agim/lidza/pkg/router"` |
| Rust crate | `lidza-core` | `core/Cargo.toml` |
| Project config | `lidza.json` | routes, frontend proxy target |
| Client SDKs | `@lidza/client`, `lidza_client` (Dart) | generated |
| Pack manifest | `pack.lidza.json` | per pack |
| Context dump | `.lidza/context.json` | gitignored |

Never put `ī` in a path, identifier, package name or domain.

## Verification

Phase 0, all passing since 2026-09-25:

```sh
sh install.sh --check          # every Toolchain and Helper tools line [ok]
lidza doctor                   # the same from the CLI, plus services and project checks
go version
go env GOPATH
cargo --version && rustc --version
rustup target list --installed | grep -E 'wasm32-(wasip1|unknown-unknown)'
staticcheck -version && golangci-lint --version && sqlc version
wasm-tools --version && k6 version
psql -U agim -d postgres -c 'select version();'
redis-cli -h 127.0.0.1 ping
sg docker -c 'docker info --format "{{.ServerVersion}}"'
```

Framework build, from the repo root:

```sh
gofmt -l . && go vet ./... && staticcheck ./... && go test ./...
(cd core && cargo test && cargo build --target wasm32-wasip1)
go build -o bin/lidza ./cmd/lidza
go test -tags evals ./evals -v      # platform evals on a fresh app, a few minutes
(cd examples/notes && lidza verify && lidza test --e2e)   # the reference app
```

Phase 1 acceptance, against this checkout rather than the published module:

```sh
bin/lidza new demo --lidza-dir "$PWD" && cd demo
lidza dev                                   # then, in another terminal:
curl -i http://127.0.0.1:3000/api/v1/health # JSON from Go
curl -s http://127.0.0.1:3000/ | grep vite  # React app through the proxy
lidza build && ./bin/demo                   # one binary, Node not running
```

## Agent session

- Unit: `~/.config/systemd/user/claude-rc-lidza.service`, runs
  `claude rc --name lidza --permission-mode bypassPermissions` with
  `WorkingDirectory=/home/agim/claude/lidza`. The session has full freedom
  on this host: passwordless sudo, and it may edit its own unit and
  `.claude/settings.local.json` (gitignored, also sets `bypassPermissions`).
  Start/stop with `systemctl --user {start,stop,status} claude-rc-lidza`;
  logs with `journalctl --user -u claude-rc-lidza`.
- Private memory: `~/.claude/projects/-home-agim-claude-lidza/memory/`.
  Never symlinked or shared with another session.
- Role brief: `CLAUDE.local.md` in the repo root, gitignored.
- Shared rules: `CLAUDE.md` (committed).
- Remote: `https://github.com/agim/lidza.git`, public, branch `master`.
  Push straight to `master`.
