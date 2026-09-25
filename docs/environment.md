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
| lidza | from this checkout | `~/go/bin/lidza`; `go install ./cmd/lidza` after pulling |
| node | 22.23.2 | `/home/agim/.local/bin/node` |
| npm | 10.9.8 | |
| ruby | 3.4.5 | rbenv; not used by Līdza |
| psql | 17.11 | client for the local Postgres 17 |
| docker | 29.8.1 | daemon running; `agim` is not in the `docker` group, run `sg docker -c '...'` |
| gh | 2.45.0 | authenticated as `agim` |
| tmux | system | |
| claude | | `/home/agim/.local/bin/claude` |
| sudo | | passwordless for `agim` |

### Missing

k6 and hey (Phase 5 benchmarks), wasmtime, wasm-pack, pnpm, bun. None is
needed before Phase 5.

## Toolchain plan

Automated by `install.sh` at the repo root (`sh install.sh`; `--check` to
report without installing). Run on `ubuntu01` 2026-09-25. The manual steps
below are the reference the script implements; each has a check command.

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
go install honnef.co/go/tools/cmd/staticcheck@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
cargo install wasm-tools --locked
```

Check: `staticcheck -version`, `golangci-lint --version`, `sqlc version`,
`wasm-tools --version`.

### k6 (Phase 5, not installed)

`install.sh` does not install it. When Phase 5 starts:

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
├── cmd/lidza/        CLI: new, dev, build, version
├── pkg/
│   ├── config/       lidza.json
│   ├── devserver/    reverse proxy, static SPA server, hot-reload coordinator
│   ├── router/       control plane: HTTP router, /api/v1/health, JSON helpers
│   ├── scaffold/     `lidza new`: template copy plus generated Go and agent files
│   ├── version/      build version
│   ├── engine/       (Phase 4) binder to the Rust core (wazero)
│   └── sdk/          (Phase 3) TypeScript / Dart client generator
├── core/             Rust crate `lidza-core` (Cargo.toml, rust-toolchain.toml, src/)
├── templates/
│   ├── embed.go      embeds the template directories into the CLI
│   ├── react/        default: Vite + React + TypeScript, TanStack Router and Query
│   ├── svelte/       (Phase 3) Vite + Svelte 5 + TypeScript
│   ├── astro/        (Phase 3) Astro, static output only
│   └── htmx/         (Phase 3) Go templ + HTMX, no JS toolchain
├── docs/
├── install.sh
├── go.mod            no dependencies outside the standard library so far
└── README.md
```

An app created by `lidza new` is a separate Go module that requires
`github.com/agim/lidza`: `main.go` embeds `dist/` and calls `lidza.Run`,
`routes.go` registers `/api` handlers, and the frontend lives at the app
root (`package.json`, `src/`). `lidza dev` builds that module into
`.lidza/app` and runs it with `LIDZA_MODE=dev`, so dev and production run
the same binary.

## Frontend

Default template: **Vite + React + TypeScript SPA** with TanStack Router and
Query. Chosen at `lidza new --template <name>`; `react` when omitted.
Package manager: npm (already installed; no pnpm).

| Template | Dev server | Production |
|---|---|---|
| `react` | Vite on 5173, proxied by `lidza dev` | `dist/` embedded in the Go binary (`embed.FS`) |
| `svelte` | Vite on 5173, proxied | same |
| `astro` | Astro dev on 5173, proxied; `output: 'static'` only | same |
| `htmx` | none; Go renders `templ` views directly | Go binary only |

Flutter/Dart is a client SDK target (`lidza-dart`), not a web template.

Contract every template follows:

- The frontend never defines `/api` routes; Go owns them.
- Types come only from the generated client (`@lidza/client`); no hand-written
  fetch wrappers.
- Frontend build errors are ingested by `lidza check --json` alongside Go and
  Rust diagnostics.
- One binary in production: Go serves the built assets, no Node at runtime.

`lidza.json`, `frontend` block:

```json
{ "template": "react", "dev": "npm run dev -- --port 5173",
  "url": "http://127.0.0.1:5173", "dist": "dist" }
```

For `htmx`: `{ "template": "htmx" }`.

## Naming

| Boundary | Form | Example |
|---|---|---|
| Brand, docs, prose | Līdza | "Līdza dev server" |
| CLI binary | `lidza` | `lidza dev`, `lidza build`, `lidza new` |
| Go module | `github.com/agim/lidza` | `import "github.com/agim/lidza/pkg/router"` |
| Rust crate | `lidza-core` | `core/Cargo.toml` |
| Project config | `lidza.json` | routes, frontend proxy target |
| Client SDKs | `@lidza/client`, `lidza-dart` | generated |
| Pack manifest | `pack.lidza.json` | per pack |
| Context dump | `.lidza/context.json` | gitignored |

Never put `ī` in a path, identifier, package name or domain.

## Verification

Phase 0, all passing since 2026-09-25:

```sh
sh install.sh --check          # every Toolchain and Helper tools line [ok]
go version
go env GOPATH
cargo --version && rustc --version
rustup target list --installed | grep -E 'wasm32-(wasip1|unknown-unknown)'
staticcheck -version && golangci-lint --version && sqlc version
wasm-tools --version
psql -U agim -d postgres -c 'select version();'
redis-cli -h 127.0.0.1 ping
sg docker -c 'docker info --format "{{.ServerVersion}}"'
```

Framework build, from the repo root:

```sh
gofmt -l . && go vet ./... && staticcheck ./... && go test ./...
(cd core && cargo test && cargo build --target wasm32-wasip1)
go build -o bin/lidza ./cmd/lidza
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
- Remote: `https://github.com/agim/lidza.git`, private, branch `master`.
  Push straight to `master`.
