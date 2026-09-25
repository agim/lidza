# Roadmap

Each phase has an acceptance check. A phase is done when its check passes on
`ubuntu01` from a clean checkout. The reasoning behind the design is in
`docs/design.md`.

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
- Publishing: the repository is public since 2026-09-25, so
  `go install github.com/agim/lidza/cmd/lidza@latest` works from
  `install.sh` and apps resolve the module without a local checkout;
  `--lidza-dir` remains for framework development.

Check (passes): `lidza new demo --lidza-dir <checkout>` then `lidza dev`,
`curl -i http://127.0.0.1:3000/api/v1/health` returns JSON, the React app
on 5173 renders through port 3000 with HMR working, and `lidza build`
yields one binary that serves the app with no Node running.

## Phase 2: Agent interface

Done 2026-09-25.

- `lidza check --json` (`pkg/diag`): `go vet`, `staticcheck`, `cargo check`
  and `tsc` run in parallel; one list of diagnostics with layer, tool,
  severity, file, line, column, code, message. Findings two tools report
  identically (Go compile errors) appear once. Missing tools are reported
  as skipped, not errors. Exit 1 while errors remain.
- `lidza context` (`pkg/inspect`): `.lidza/context.json` with routes found
  by `go/ast` (`HandleFunc`/`Handle` calls with literal patterns; handler
  name, file, line, signature; built-ins marked) and the Rust crate's
  `#[no_mangle] pub extern "C" fn` exports found by scanning the source.
  `syn`-based parsing waits for Phase 4, when pack validation needs full
  Rust signatures.
- `lidza mcp` (`pkg/mcpserver`, `mark3labs/mcp-go`, the first dependency):
  tools `lidza_routes`, `lidza_context`, `lidza_check`, `lidza_logs`
  (tail of `.lidza/dev.log`, which `lidza dev` now writes), `lidza_config`;
  resources `lidza://llms.txt` and `lidza://llms-full.txt`.
- `/llms.txt` and `/llms-full.txt` served in dev mode from `.lidza/`,
  regenerated after every Go rebuild.
- `lidza new` writes `.mcp.json` and `.gemini/settings.json`; the guide
  documents the tools; Codex CLI snippet in `docs/getting-started.md`.

Check (passes): an agent connected to `lidza mcp` can list routes;
introducing a Rust type error makes `lidza check --json` report it with the
right file and line.

## Feature matrix

`docs/features.md` maps every capability a complete framework needs to the
phase that delivers it and records the decisions taken (`sqlc`, build-time
prerendering). The phases below carry the items it assigns them.

## Phase 3: Schema and SDKs

Done 2026-09-25, except the Dart client.

- `schema.lidza` (`pkg/schema`): enums, models (tables) and types (API
  shapes) with validation rules. `lidza gen` writes `schema/schema.go`
  (structs, enum constants, `Validate()`), `db/schema.sql`, a numbered
  migration pair when the models changed since `db/schema.lock.json`
  (statements that lose data carry a `-- review` comment), and
  `core/src/schema.rs` when the project has a crate.
- Typed handlers: `router.Route[In, Out]` decodes and validates the body
  (422 with field errors), maps `HTTPError` and `validate.Errors` to
  statuses, hides other errors behind a 500.
- `pkg/inspect` type-checks the app with `go/packages`; each typed route
  becomes an operation with JSON Schemas (schema.lidza definitions carry
  their rules); `.lidza/openapi.json` (OpenAPI 3.1) and `/openapi.json`
  in dev, regenerated after every rebuild.
- `pkg/sdk`: `@lidza/client` in `.lidza/client` (types.ts, client.ts):
  one typed function per operation, `ApiError` with the field errors.
  `lidza check` regenerates before `tsc`, so a frontend call that no
  longer matches a handler fails with file and line. Dart client: deferred
  until a Flutter consumer exists.
- Templates: `svelte` (Vite + Svelte 5 + TypeScript, `svelte-check` in
  `lidza check`), `astro` (static output, client-side scripts use the
  client), `htmx` (Go `html/template` views embedded in the binary, htmx
  vendored, partials call the same handler functions as the API; `lidza.json`
  `watch` rebuilds on view changes). `html/template` replaces `templ` from
  the plan: no extra toolchain, and the views are plain files.
- Middleware pipeline (`pkg/middleware`): request id, request log, panic
  recovery, per-request deadline on every API route; secure headers on
  every response; `CORS`, `MaxBody`, `SecureHeaders` with CSP and HSTS
  opt-in. `router.Use` for API-only middleware, `App.Middleware` for all.
- App lifecycle hooks: `OnStart`, `OnReady`, `OnShutdown`.
- `pkg/env`: typed configuration from `.env`, `.env.<mode>` and the
  process environment.
- React error boundary around the app and per route.

Check (passes): changing a handler's response struct updates the generated
TypeScript types without manual steps; `tsc` (and `svelte-check`) fail if
the frontend is out of date; every template passes the Phase 1 check (HMR
through the proxy where applicable, single binary in production).

## Phase 4: Packs

Done 2026-09-25.

- ABI (`core/src/abi.rs`, copied into every pack crate): JSON in linear
  memory, `lidza_alloc`/`lidza_free`, `lidza_export!(name, |In| ->
  Result<Out, String>)`; errors travel as `{"$error": ...}`.
- `pkg/engine`: wazero module compiled once, bounded pool of pre-warmed
  instances, per-call deadline that cuts a runaway call off and replaces
  its instance, memory cap per instance.
- `pack.lidza.json` and `pkg/pack`: manifest (capabilities typed by
  `schema.lidza` types, pool size, timeout, memory), validator (names,
  types, module exports), generated `packs/<name>/pack.go` (lifecycle plus
  one typed method per capability) and `packs.go` from `lidza.json`;
  `lidza pack scaffold` writes a crate with an example capability, `lidza
  pack build` compiles to `wasm32-wasip1`; `lidza dev` rebuilds a pack when
  its crate changes, `lidza check` validates manifests against the built
  module.
- `lidza.Services`: typed registry filled by packs and `OnStart`, read in
  handlers with `lidza.Service[T](ctx)` or a pack's `From(ctx)`.
- `lidza mcp` exposes `lidza_packs` and one callable tool per capability
  with the input type's JSON Schema, so an agent can run a capability
  before wiring it.
- Official packs, `lidza pack add <name>`: `db` (Go: bounded `pgxpool`
  from `DATABASE_URL`, migrations with an advisory lock, `lidza db
  migrate|rollback|status`, sqlc configuration and `lidza gen` running
  `sqlc generate`), `realtime` (Go: WebSocket topics at
  `/api/v1/realtime`, per-connection buffers, connection cap, Valkey bus
  for multi-node fan-out), `geo` (Rust: haversine, R-tree nearest,
  bounding box) and `media` (Rust: image info, resize and re-encode).
  Rust packs are copied into the app as sources and built there; the
  first `media` build takes about a minute, later ones seconds.

Check (passes): `lidza pack scaffold demo`, fill in one Rust function, and
the capability shows up in the MCP tool list with a matching Go call.

## Phase 5: Scale primitives

Done 2026-09-25.

- `pkg/telemetry`: `/metrics` (Prometheus exposition via client_golang:
  Go runtime, process, requests by method, route pattern and status, a
  duration histogram, in-flight gauge, and `lidza_service_stat` gauges
  from every service that reports stats: engine pools, the db pool, the
  realtime hub); `/healthz`; `/readyz` runs every service's `Ready` check
  with a deadline and replies 503 while one fails. `/debug/pprof/` in dev.
- `middleware.RateLimit`: token bucket per key (client IP by default),
  429 with Retry-After, bounded key table. `pkg/resilience.Breaker`:
  circuit breaker for outbound calls (closed, open, half-open with one
  trial call).
- `lidza check` rules (warnings): L001 package-level map or slice, L002
  goroutine started inside a handler. Generated files and tests excluded.
- `pkg/engine.Pool` counters (calls, errors, timeouts, busy) exposed as
  stats; the bounded pool with per-call deadlines itself arrived in
  Phase 4.
- `lidza benchmark [scenario] [--vus 500] [--duration 1m] [--warmup 10s]`:
  runs `benchmarks/<scenario>.js` with k6 after a warm-up at the same load
  and compares heap (after a forced GC in dev) and goroutines before and
  after; `lidza new` writes `benchmarks/scale_test.js`. k6 installed on
  `ubuntu01`.

Check (passes): `lidza benchmark --vus 500 --duration 1m` against `lidza
dev` on `ubuntu01`: 273,030 requests at 4,535 req/s, p95 140 ms, no
failures, heap +0.8 MB, goroutines +0.

## Phase 6: Application services

Done 2026-09-25.

- `auth` pack: argon2id password hashing, JWT HS256 access tokens
  (short-lived, `AUTH_SECRET` shared by every node), refresh sessions in
  Postgres (`auth_session` from a schema fragment; rotate, revoke one,
  revoke all), HttpOnly cookies or bearer header, `auth.Require`
  middleware with `auth.CurrentUser(ctx)`; cookie-authenticated
  state-changing requests must send `Content-Type: application/json`,
  which is the CSRF shield. Sessions live in Postgres rather than Valkey
  so the pack needs no second store; a login survives a restart because
  the token is signed with the shared secret and the session is a row.
- `jobs` pack: a Postgres queue (`job` table from a schema fragment,
  `FOR UPDATE SKIP LOCKED`), bounded workers per node, retries with
  exponential backoff, scheduling, takeover of jobs whose node died,
  panics recorded as failures. Built in place of River: no second
  migration system, one table the schema owns.
- `cache` pack: Valkey or Redis with TTL, `Remember` read-through,
  prefix invalidation; `CACHE_URL=memory` is a bounded in-process cache
  for tests and single-node development.
- `i18n` pack: `locales/<lang>.json` embedded by `packs.go`, locale per
  request (`?lang`, cookie, `Accept-Language`) through a pack-provided
  middleware, `T`, `Number`, `Currency`, `Date`, and the catalog served
  at `/api/v1/i18n/{lang}`.
- `lidza test`: `LIDZA_MODE=test`, the test database from `.env.test`
  created and migrated, `go test ./...`, then the frontend check.
  `pkg/lidzatest` boots the app with its packs on an httptest server
  with a JSON client and cookie jar; `lidza new` writes `routes_test.go`.
  `lidza.Boot` is the piece Serve and the tests share. Not done: a fake
  clock and recorded HTTP fixtures; a test that needs them writes its
  own for now.
- Typed handlers can set reply headers and cookies (`req.Header()`,
  `req.SetCookie`); packs can contribute API middleware
  (`lidza.Middlewarer`).

Check (passes): the template app with `db`, `auth`, `jobs`, `cache` and
`i18n` added passes `lidza test` against the local services, and a login
(bearer and cookie) survives a restart of the binary.

## Phase 7: Frontend depth

Done 2026-09-25.

- Prerendering for `react`: `npm run build` runs the client build, a
  Vite SSR build of `src/entry-server.tsx`, and `scripts/prerender.mjs`,
  which writes `dist/<path>/index.html` for every parameterless route
  (TanStack Router memory history, `renderToString`). `main.tsx` hydrates
  when markup is present and renders otherwise; the Go static server
  serves `<path>/index.html` when it exists. Pages render their loading
  state at build time and fetch from `/api` after hydration, so there is
  no server-side data and no dehydrate/hydrate step. `astro` prerenders
  by itself; `svelte` and `htmx` are unchanged.
- Accessibility: ESLint 9 with `eslint-plugin-jsx-a11y` (recommended,
  errors) and `typescript-eslint` in the `react` template; `lidza check`
  runs `eslint --format json` and reports the findings with file and line.
- `useLive(topics)` in the template: a WebSocket to the `realtime` pack
  that invalidates the TanStack queries keyed by the topic; reconnects
  with backoff.
- Client-side validators: `@lidza/client` gains `validators.ts`, one
  function per `schema.lidza` type with rules (required, min, max,
  email, url, pattern, enum), the same checks and messages as the Go
  `Validate`, so a form can show the server's field errors before the
  request.

Check (passes): the template app's `/about` is served as complete HTML by
the binary and hydrates in Chromium without console errors, then fetches
its data once; an `img` without `alt` fails `lidza check`.

## Beyond the roadmap

Every phase of the plan is delivered. Follow-up work, in Agim's order.

### Test harness (done 2026-09-25)

- `lidza.Now(ctx)` reads the app's clock; `lidzatest.Start` provides a
  `Clock` the test freezes (`Set`) or moves (`Advance`).
- `lidza.HTTPClient(ctx)` is the client for outbound calls;
  `lidzatest.WithRecorder("name")` records them to
  `testdata/http/name.json` with `LIDZA_RECORD=1` and replays them after,
  so tests run offline. Authorization, Cookie and API-key headers are
  never stored. `WithTransport` plugs any stub.
- `lidza test --e2e`: builds the app, starts the binary on a free port
  with `.env.test`, runs the Playwright suite in `e2e/` (the `react`
  template ships `playwright.config.ts` and `e2e/home.spec.ts`, with
  `@playwright/test` pinned). The browser is resolved by the app's own
  Playwright, never by a guessed path: a missing build is reported with
  `npx playwright install --with-deps chromium`, or installed with
  `--install`.
- `lidza doctor`: toolchain, services and, in a project, `node_modules`,
  pack builds and the e2e browser, each missing item with its fix.

### Dart client (done 2026-09-25)

`lidza.json` `"sdk": {"dart": "clients/dart"}` makes `lidza gen` write a
Dart package (`lidza_client`, on `package:http`) there: enums with wire
values, classes with `fromJson`/`toJson` (`DateTime` for date-time),
`LidzaClient` with one method per operation, `ApiException` with the
field errors of a 422. A Flutter app depends on it by path. No Dart
toolchain runs during `lidza gen`; the generated package is analyzed in
the Flutter project. Validators are TypeScript only.

### Per-request SSR sidecar (done 2026-09-25)

`LIDZA_SSR=1` makes the binary start a Node sidecar (`dist/.server`,
extracted to a temporary directory, reached over a Unix socket) that
renders HTML page requests with the visitor's cookies, `Accept-Language`
and `Authorization` forwarded to route loaders calling the API over
loopback. Pages come back complete and personalised, `Cache-Control:
no-store`. Anything else, and any failure or timeout (3 s), falls back to
the prerendered or static page, so SSR is an optimisation the app never
depends on. The SSR bundle carries its dependencies (`ssr.noExternal`),
so production needs Node and `dist/.server` only, no `node_modules`.
Renders run one at a time in the sidecar; loader data is not
dehydrated, the client runs loaders again on navigation.

### Publishing (done 2026-09-25)

The repository is public; `install.sh` installs the CLI with `go install`
and `lidza new` fetches the module. The design notes the project started
from were replaced by `docs/design.md` before publishing.

### Logging and time zones (done 2026-09-25)

`lidza.Log(ctx)` returns the app logger with the request id attached;
`LIDZA_LOG` picks JSON or text (text under dev and test), `LIDZA_LOG_LEVEL`
the level; the same logger is slog's default, so packs log alike. The
`i18n` pack negotiates a time zone per request (`?tz`, `tz` cookie,
`X-Timezone`, then `I18N_TIMEZONE`) and formats `Date`, `Time` and
`DateTime` in it; the `react` template sets the cookie from `Intl`.
Storage and the API stay in UTC.

### Resource generator (done 2026-09-25)

`lidza gen resource <Model>` turns a model into a resource: sqlc queries
(list with limit and offset, count, get, create, update with COALESCE so
absent fields keep their value, delete), the `Create<Model>`,
`Update<Model>` (every field optional) and `<Model>List` types appended
to `schema.lidza` with the model's validation rules, `handlers/<table>.go`
with the five typed routes under `/api/v1/<plural>` and the row mapping,
shared helpers in `handlers/convert.go`, and the registration line in
`routes.go`. The handlers file is generated once and edited freely
(`--force` overwrites). Needs the `db` pack; sqlc maps uuid and
timestamps to `string` and `time.Time` so rows and schema types line up.

### Tailwind (done 2026-09-25)

The `react` template uses Tailwind v4 through `@tailwindcss/vite`:
`src/index.css` imports Tailwind and declares the theme as CSS variables
(`--color-brand`, `--color-ink`, ...), pages use utilities, no component
library. `svelte` and `astro` keep plain CSS; the same two lines (the
plugin and the import) add Tailwind there.

### Analytics and error reporting (done 2026-09-25)

`lidza pack add analytics` (needs `db`): server panics and handler 500s
are captured through `pkg/report` (a seam the recovery middleware and
the typed router call), frontend errors and named events arrive at
`POST /api/v1/analytics/{errors|events}` (size-capped, rate-limited), and
everything is written to `app_error` and `app_event` by one bounded
writer goroutine (drops are counted, never block a request). Optional
OTLP/HTTP export of errors to any collector. `VITE_ANALYTICS=1` turns on
the frontend reporter (errors, unhandled rejections, the error boundary,
pageviews, `track(name, props)`); no third-party script, no IP stored,
retention configurable. `lidza mcp` gains `lidza_errors`.

Found by the analytics pack on its first run: the prerendered pages were
being discarded at hydration (React error 418, reported through
`window`'s error event, which the earlier browser checks did not watch).
The router adds a Suspense boundary around its matches in the browser
only; the server entry now renders inside a boundary at the same DOM
position, so the prerendered HTML carries the marker the client expects
and React keeps the DOM. The template's e2e test asserts no window
errors.

### App-defined MCP tools (done 2026-09-25)

`lidza.Tool` and `lidza.ToolFunc(name, description, func(ctx, In) (Out,
error))`: the app lists its tools in `tools.go` (written by `lidza new`),
input schemas come from the Go types (`pkg/jsonschema`), inputs that
implement `Validate` are validated, and handlers run inside the app with
its packs. `lidza mcp` builds the app, starts it in tool-serving mode
(`LIDZA_MCP=stdio`) and mirrors its tools as `app_<name>` next to the
framework's; the running binary serves the same tools at `/mcp`
(Streamable HTTP) when `LIDZA_MCP_TOKEN` is set, for agents that present
the token. No child MCP server is needed.

### Agent platform: recipes, API, rules (done 2026-09-25)

The app guide has a "Recipes" section (add an API route, a resource, a
page, a pack capability, an MCP tool, a test); `pkg/recipes` parses it
and `lidza mcp` serves each recipe as a prompt with an optional task
argument, while `lidza new` and `lidza gen` write each as a Claude Code
skill in `.claude/skills/<name>/SKILL.md`; `CLAUDE.md`, `AGENTS.md` and
`GEMINI.md` list them. `pkg/apidoc` renders the framework's exported API
from the sources the app resolves (a `replace` to a checkout or the
module cache): `lidza api [package] [--filter name] [--list]`, the MCP
tool `lidza_api`, the resources `lidza://api` and `lidza://api/{package}`.
`lidza check` gained L003 (hand-written `fetch` of `/api` in `src/`,
warning; `lidza:ignore L003` on the line before exempts one), L004 (an
import of a framework package that does not exist, error with the closest
real package, replacing the go tool's "go get" advice; a frontend import
that `package.json` does not declare, error), L005 (a typed handler whose
input or output type is not from `schema.lidza`, warning).

### lidza verify and the pre-commit hook (done 2026-09-25)

`lidza verify [--json] [--no-test]`: regenerate, fail when a tracked
generated file differs from the git index (the commit would miss it),
`lidza check` with pack diagnostics, `go test ./...` with the test
database prepared. `lidza new` runs `git init`, writes
`.githooks/pre-commit` (`exec lidza verify`) and sets `core.hooksPath`;
inside an existing repository it explains what to add to that
repository's hook instead. `lidza verify --install-hook` does the setup on
an existing project.

### Reference app and snippets (done 2026-09-25)

`examples/notes` is a real app in its own module (`replace => ../..`):
the `db` and `auth` packs, registration and login with cookies and a
bearer token, a `Note` resource generated and then scoped to its owner,
a React page on the generated client and validators, a handler test
covering 401, 409, 422 and 404, a browser test, an MCP tool. Its files
are embedded in the CLI (`pkg/snippets`, synced by `go generate`, a test
fails when they drift) and served by `lidza snippet [name]` and the MCP
tool `lidza_snippet` with a catalog resource. Building it surfaced and
fixed: `Router.Group(prefix, mw...)` for protected sub-trees (the old
advice re-registered builtins on a second router), `auth.Optional()`,
the CSRF guard rejecting body-less POSTs from the generated clients (the
clients now always declare JSON on state-changing requests, and the
guard accepts a same-origin `Sec-Fetch-Site`), optional schema fields
required in TypeScript, generated Go never gofmt-formatted, and
`go test ./...` descending into a Go package shipped inside
`node_modules` (isolated with a `go.mod` there).

### Next

Pending: platform evals.
