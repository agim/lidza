# Getting started

How a developer sets up Līdza and builds an app with an AI agent (Claude
Code, Codex CLI or Gemini CLI) doing the coding.

Status 2026-09-25: `install.sh` works. The `lidza` CLI lands in roadmap
Phase 1 and the agent interface (`lidza mcp`, `lidza check --json`) in
Phase 2; steps that need them are marked.

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

`lidza dev` starts the Go control plane on http://127.0.0.1:3000 and the
frontend dev server on 5173 behind it. API routes live under `/api`;
everything else is the frontend with hot reload.

`lidza new` writes the agent files for you:

| File | Read by |
|---|---|
| `CLAUDE.md` | Claude Code |
| `AGENTS.md` | Codex CLI (and other AGENTS.md-aware tools) |
| `GEMINI.md` | Gemini CLI |
| `.mcp.json` | Claude Code, project-scoped MCP server (Phase 2) |
| `.gemini/settings.json` | Gemini CLI MCP server (Phase 2) |
| `docs/lidza-guide.md` | all three; the framework rules in one place |

The three instruction files are short and point at `docs/lidza-guide.md`,
so there is one source of truth.

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
   layers, with file and line (Phase 2).
3. `lidza dev` hot-reloads; the generated client keeps frontend types in sync
   with the Go handlers (Phase 3).
4. `lidza mcp` lets the agent ask for the route map, schema and logs instead
   of grepping (Phase 2).

### MCP server (Phase 2)

`lidza new` configures it for Claude Code and Gemini CLI. Codex CLI reads a
user-level file; add:

```toml
# ~/.codex/config.toml
[mcp_servers.lidza]
command = "lidza"
args = ["mcp"]
```

To add it to Claude Code by hand: `claude mcp add lidza -- lidza mcp`.

## Local services

The first app runs without a database. When you need one:

```sh
docker run -d --name lidza-pg -e POSTGRES_PASSWORD=lidza -p 5432:5432 postgres:17
docker run -d --name lidza-redis -p 6379:6379 redis:7
```

`lidza.json` holds the connection settings; `install.sh --check` reports
whether both are reachable.

## Troubleshooting

- `lidza: command not found`: open a new shell or `. ~/.lidza/env`.
- Installer asks for a password: it found `sudo` but not passwordless; rerun
  with `--no-sudo` to keep everything under `$HOME`.
- `go install` of the CLI fails: the CLI is not published yet (Phase 1) or
  the repo is private for your account. The toolchain is still complete.
- Windows: run everything inside WSL2 (Ubuntu). Native Windows is not
  supported yet.
