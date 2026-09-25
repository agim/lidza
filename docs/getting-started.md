# Getting started

How a developer sets up Līdza and builds an app with an AI agent (Claude
Code, Codex CLI or Gemini CLI) doing the coding.

Status 2026-09-25: `install.sh`, the `lidza` CLI (`new`, `dev`, `build`,
`check`, `gen`, `context`, `mcp`), the agent interface, the four templates
and the generated TypeScript client work.

## Requirements

- Linux (apt, dnf or pacman) or macOS with Homebrew, amd64 or arm64.
  Windows: use WSL2.
- `curl`, to fetch the installer. Everything else the installer adds:
  `git`, a C toolchain, Go, Rust, Node, the helper tools, the CLI, and
  with `--services` Postgres and Valkey.
- One agent CLI: `claude`, `codex` or `gemini`.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/agim/lidza/master/install.sh | sh -s -- --services
```

Without `--services` the installer asks whether to install Postgres and
Valkey when a terminal is attached, and skips them otherwise; the `db`,
`auth`, `jobs`, `mail` and `analytics` packs need Postgres, `cache` and
`realtime` need Valkey, and `lidza doctor` says what is missing with the
command that fixes it.

What it does, skipping anything already present:

| Step | Where | Notes |
|---|---|---|
| git, curl, C toolchain | system packages | apt, dnf, pacman or Homebrew |
| Go (current stable) | `/usr/local/go` with sudo, else `~/.local/go` | |
| Rust via rustup | `~/.cargo`, `~/.rustup` | adds `wasm32-wasip1`, `wasm32-unknown-unknown` |
| Node 22 | `~/.local/opt/node-v22.x-<os>-<arch>` | the official tarball; needed by the react, svelte and astro templates |
| staticcheck, golangci-lint, sqlc | `~/go/bin` | pinned versions; skipped with `--minimal` |
| wasm-tools | `~/.cargo/bin` | skipped with `--minimal` |
| `lidza` CLI | `~/go/bin` | the release the installer was tested with; `LIDZA_VERSION=<tag>` or `master` overrides; an older release is replaced on rerun |
| Postgres, Valkey | system services | with `--services`: installed, started, a superuser role named after you |

PATH changes go to `~/.lidza/env`, sourced from `~/.profile`, `~/.bashrc`
and `~/.zshrc`. Nothing else in your shell config is touched. The
report at the end lists every item; `lidza doctor` repeats it any time,
with the command that fixes what is missing.

Flags: `--check` (report only, install nothing), `--services`,
`--minimal`, `--no-sudo` (root steps are printed, not run), `--yes`.

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

The app is its own Go module requiring `github.com/agim/lidza`, fetched
from GitHub by `lidza new`. To develop against a local checkout of the
framework, pass `--lidza-dir <checkout>`.

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
4. `lidza mcp` lets the agent ask for the route map, diagnostics, logs
   and the framework's API instead of grepping or guessing.
5. The app guide's recipes (add an API route, a resource, a page, a pack
   capability, an MCP tool, a test) are MCP prompts, skills for Claude
   Code and Codex, and Gemini CLI commands, so a task starts from the
   steps that work here.

### Against hallucination

Three things keep an agent from inventing what is not there:

- `lidza api [package] [--filter name]` (and the MCP tool `lidza_api`,
  resource `lidza://api`) prints exported functions, types and methods
  with their doc comments, rendered from the sources: the framework as
  the app depends on it (`lidza api pkg/router`), or the app's own
  packages (`lidza api app`, `lidza api ./handlers`). `--list` names
  both.
- `lidza check` rules: an import of a framework package that does not
  exist is an error with the closest real package (L004), as is a frontend
  import that `package.json` does not declare (L004); a hand-written
  `fetch` of `/api` (L003) and a handler type not declared in
  `schema.lidza` (L005) are warnings.
- The recipes: every step names the file, the command and the check.
  The framework's live under "Recipes" in the app guide and follow the
  framework version; the app's own live under "App recipes"
  (`lidza recipe add "Title"`, or the MCP tool `lidza_recipe_add`) and
  become prompts, skills and commands the same way.
- `lidza snippet [name]` (and the MCP tool `lidza_snippet`) prints a
  file of the reference app in `examples/notes`, verified by its own
  tests: auth routes, an owned resource, a page on the generated client,
  a handler test, a browser test, an MCP tool.
- `lidza verify`, run by the pre-commit hook `lidza new` installs:
  regenerate and refuse a commit whose generated files are not staged,
  `lidza check` clean, `go test ./...` green. `--json` for agents,
  `--install-hook` on an existing project.

### MCP server

Tools: every CLI command (`lidza_check`, `lidza_gen`, `lidza_gen_resource`,
`lidza_pack_add`, `lidza_pack_scaffold`, `lidza_pack_build`,
`lidza_db_migrate`, `lidza_db_rollback`, `lidza_db_status`, `lidza_test`,
`lidza_verify`, `lidza_build`, `lidza_doctor`), run in the project and
answered with one JSON result, so an agent needs no shell; `lidza_routes`,
`lidza_context`, `lidza_logs`, `lidza_config`, `lidza_api`, `lidza_snippet`,
`lidza_recipes`, `lidza_recipe_add`, `lidza_packs` and one tool per pack
capability, `lidza_errors` with the analytics pack, and the app's own
tools from `tools.go` as `app_<name>`; resources `lidza://llms.txt`,
`lidza://llms-full.txt`, `lidza://api` and `lidza://api/{package}`;
prompts, one per recipe in `docs/lidza-guide.md`. The recipes are also
on disk for each CLI: Claude Code reads `.claude/skills/<name>/SKILL.md`,
Codex reads `.agents/skills/<name>/SKILL.md` (type `$add-api-route`, or
`/skills`), Gemini CLI reads `.gemini/commands/lidza/<name>.toml` (type
`/lidza:add-api-route what to add`). `lidza gen` rewrites all three from
the guide. With `LIDZA_MCP_TOKEN`
set, the running binary serves the app's tools at `/mcp` to agents that
send the token. While `lidza dev` runs, the two llms documents are at
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

## From nothing to a configured app

```sh
lidza new tracker --packs db,auth,jobs,mail,realtime --agent claude
cd tracker
lidza dev
```

`lidza new` with `--packs` runs `lidza setup`: the packs are enabled,
`.env` is written from `.env.example` with a random `AUTH_SECRET` and a
socket `DATABASE_URL` named after the app (`tracker_dev`; pass
`--database-url` or set `LIDZA_DATABASE_URL` for a server elsewhere),
`.env.test` points at `tracker_test` with mail kept in the outbox, the
code is generated, both databases are created and migrated,
`node_modules` is installed, the agent CLI is installed when missing
(`--agent`; sign in once by running it), and the first commit is made
through the pre-commit hook. `lidza setup` does the same on an existing
app and is safe to rerun. When the app is ready for a server, `lidza
ship` runs verify, the browser suite and the production build in one go.

## Resources

With the `db` pack enabled, `lidza gen resource Post` turns a model into a
working resource: SQL queries, `CreatePost`/`UpdatePost`/`PostList` types
carrying the model's validation rules, `handlers/posts.go` with list, get,
create, patch and delete under `/api/v1/posts`, and the line in
`routes.go`. The client gets `api.listPosts()`, `api.createPost(body)` and
the rest on the next `lidza gen`. The handlers are yours after that.

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

## Flutter

Add `"sdk": {"dart": "clients/dart"}` to `lidza.json`; `lidza gen` then
keeps a Dart package there (`lidza_client`) with the same types and
operations as the TypeScript client. The Flutter app depends on it with
`lidza_client: {path: ../clients/dart}` and calls
`LidzaClient(baseUrl: ...)`.

## Frontend

The `react` template prerenders every parameterless route at build time
(`npm run build`: client build, SSR build, `scripts/prerender.mjs`), so
the binary serves complete HTML and the page hydrates in the browser.
Per-request rendering is optional: `LIDZA_SSR=1` at deploy time makes the
binary run the Node sidecar shipped in `dist/.server`; route loaders then
execute on the server with the visitor's cookies and the page arrives
complete. Without Node, or when a render fails, the static page is served.
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
- `go install` of the CLI fails: check network access to GitHub and the
  Go module proxy (`GOPROXY=direct` bypasses the proxy). The toolchain is
  still complete.
- Windows: run everything inside WSL2 (Ubuntu). Native Windows is not
  supported yet.
