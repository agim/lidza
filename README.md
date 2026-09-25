# Līdza

Līdza is a web framework with two engines: a Go control plane (HTTP routing,
dev proxy, CLI, MCP server) bound to a Rust compute core (native or WASM via
wazero), serving any frontend (Vite, Astro, Svelte, HTMX, Flutter). It is
built for AI agents as the primary developers: machine-readable contracts,
structured diagnostics, no implicit magic.

## Name

- **Līdza** in prose, docs and branding.
- **`lidza`** in every identifier: CLI binary, Go module, crate prefix,
  config file, npm scope.

*Līdza* is the reconstructed Proto-Albanian verb "to bind, tie, connect",
ancestor of modern Albanian *lidh* (Vladimir Orel, *Albanian Etymological
Dictionary*, Brill 1998, p. 223). The framework's job is to bind Go, Rust
and a frontend into one application.

## Quick start

```sh
curl -fsSL https://raw.githubusercontent.com/agim/lidza/master/install.sh | sh
lidza new myapp && cd myapp
lidza dev
claude        # or: codex, gemini
```

The installer sets up Go, Rust with wasm targets, the helper tools and the
`lidza` CLI, skipping what you already have. `lidza new` writes `CLAUDE.md`,
`AGENTS.md` and `GEMINI.md`, so any of the three agents can start building
at once. Full guide: `docs/getting-started.md`.

## Frontend

Default template: Vite + React + TypeScript. `svelte`, `astro` (static) and
`htmx` are selectable at `lidza new --template <name>`. All follow the same
contract: Go owns `/api`, types come from the generated client, production
is one binary.

## Status

Phase 0: environment. `install.sh` works; the `lidza` CLI ships in Phase 1.
See `docs/roadmap.md`.

## Docs

- `docs/getting-started.md`: install, create an app, build it with Claude Code, Codex or Gemini CLI.
- `docs/environment.md`: host inventory, toolchain plan, services, layout, naming, verification.
- `docs/roadmap.md`: phases and acceptance checks.
- `docs/research/`: design notes the project started from.
