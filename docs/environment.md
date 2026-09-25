# Environment

Written 2026-09-25. Describes the development host for Līdza and the
toolchain it needs. **Nothing in "Toolchain plan" has been run yet**; the
install steps are the contract for Phase 0 and are executed only on Agim's
explicit go-ahead.

## Host

`ubuntu01`, Linux 7.0.0-31-generic, 4 vCPU, 15 GiB RAM, ~177 GB free on `/`.

### Present

| Tool | Version | Path / notes |
|---|---|---|
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

go, cargo, rustc, rustup, pnpm, bun, k6, hey, staticcheck, golangci-lint,
sqlc, wasm-tools, wasmtime, wasm-pack. No `~/go`, `~/.cargo`, `~/.rustup`,
`/usr/local/go`.

## Toolchain plan

Not yet run. Each step has a check command; Phase 0 is done when every
check passes.

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
cargo install wasm-tools
sudo gpg -k && sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg \
  --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" \
  | sudo tee /etc/apt/sources.list.d/k6.list
sudo apt-get update && sudo apt-get install -y k6
```

Check: `staticcheck -version`, `golangci-lint --version`, `sqlc version`,
`wasm-tools --version`, `k6 version`.

Optional, only when a phase needs them: `wasmtime` (run WASM outside Go),
`pnpm` (frontend templates).

### Version pinning

- `go.mod`: `go` directive set to the installed minor version; `toolchain`
  directive pins the patch.
- `core/rust-toolchain.toml`: `channel = "stable"` plus the two wasm
  targets, so `cargo` picks the same toolchain on every machine.

## PATH

Interactive shells (`~/.profile`), to add during Phase 0:

```sh
export PATH="$HOME/.cargo/bin:$HOME/go/bin:/usr/local/go/bin:$PATH"
```

The session unit already carries the same directories in
`Environment=PATH` (see "Agent session"), so it needs no edit after the
installs.

## Local services

| Service | Address | Notes |
|---|---|---|
| Postgres 17 | 127.0.0.1:5432 | cluster `main`, data on `/mnt/faststorage/pgdata`. Planned databases: `lidza_dev`, `lidza_test`. |
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

Target layout once code exists:

```
lidza/
├── cmd/lidza/        Go CLI entrypoint (builds the `lidza` binary)
├── pkg/
│   ├── devserver/    reverse proxy and hot-reload coordinator
│   ├── router/       control plane: HTTP, WebSockets, sessions
│   ├── engine/       binder to the Rust core (wazero, IPC, FFI)
│   └── sdk/          TypeScript / Dart client generator
├── core/             Rust crate `lidza-core` (Cargo.toml, src/)
├── templates/
│   ├── react/        default: Vite + React + TypeScript
│   ├── svelte/       Vite + Svelte 5 + TypeScript
│   ├── astro/        Astro, static output only
│   └── htmx/         Go templ + HTMX, no JS toolchain
├── docs/
├── go.mod, go.sum
└── README.md
```

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

Run after Phase 0 installs; all must pass.

```sh
go version
go env GOPATH
cargo --version && rustc --version
rustup target list --installed | grep -E 'wasm32-(wasip1|unknown-unknown)'
staticcheck -version && golangci-lint --version && sqlc version
wasm-tools --version && k6 version
psql -h 127.0.0.1 -U agim -d postgres -c 'select version();'
redis-cli -h 127.0.0.1 ping
sg docker -c 'docker info --format "{{.ServerVersion}}"'
```

Phase 1 adds: `go build -o bin/lidza ./cmd/lidza && bin/lidza dev` in one
terminal, `curl -i http://127.0.0.1:3000/api/v1/health` in another.

## Agent session

- Unit: `~/.config/systemd/user/claude-rc-lidza.service`, runs
  `claude rc --name lidza` with `WorkingDirectory=/home/agim/claude/lidza`.
  Start/stop with `systemctl --user {start,stop,status} claude-rc-lidza`;
  logs with `journalctl --user -u claude-rc-lidza`.
- Private memory: `~/.claude/projects/-home-agim-claude-lidza/memory/`.
  Never symlinked or shared with another session.
- Role brief: `CLAUDE.local.md` in the repo root, gitignored.
- Shared rules: `CLAUDE.md` (committed).
- Remote: `https://github.com/agim/lidza.git`, private, branch `master`.
  Push straight to `master`.
