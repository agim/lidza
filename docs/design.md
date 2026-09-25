# Design

Why Līdza is built the way it is. The roadmap says what shipped when; this
says what the parts are for.

## Name

*Līdza* is the reconstructed Proto-Albanian verb "to bind, tie, connect"
(from Proto-Indo-European *leyǵ-), the ancestor of modern Albanian *lidh*
and a cognate of Latin *ligāre* (Vladimir Orel, *Albanian Etymological
Dictionary*, Brill 1998, p. 223). The framework binds a Go control plane,
a Rust compute core and a frontend into one application. The macron stays
in prose; every identifier, path, package and domain uses `lidza`, so the
CLI, the module path and the npm scope are plain ASCII.

## Two engines, one binary

- **Go control plane.** HTTP routing, sessions, WebSockets, the dev
  server, the CLI, the MCP server. Go's runtime holds many idle
  connections cheaply and the standard library covers the whole HTTP
  surface, so the router is `net/http` with typed handlers on top, no
  third-party web framework.
- **Rust compute core.** Anything that allocates heavily or burns CPU
  (media, geospatial indexes, graph work, vector math) runs in Rust,
  compiled to WebAssembly and executed by the Go process through wazero
  in a bounded pool. The Go garbage collector never sees that memory, a
  bad computation cannot crash the host, and a call that overruns its
  deadline is cut off and its instance replaced.
- **Any frontend.** React, Svelte and Astro through a dev proxy and a
  build embedded in the binary; htmx rendered by Go templates. The
  contract is the same for all: Go owns `/api`, the frontend calls it
  through a generated client, production is one binary with no Node at
  runtime (per-request SSR is the one optional exception).

## Agents as the primary developers

The design assumes an AI agent writes most of the code. Agents fail on
implicit magic, on types that drift between layers and on unstructured
error output, so:

- **One contract.** `schema.lidza` declares the data shapes once and
  generates the Go structs, the SQL and migrations, the Rust structs and
  the TypeScript and Dart clients. A handler's types are the API's types;
  `lidza check` fails when the frontend no longer matches.
- **Structured diagnostics.** `lidza check --json` merges `go vet`,
  `staticcheck`, `cargo check`, `tsc`, `svelte-check` and ESLint into one
  list with layer, file, line and message, plus the framework's own rules.
- **The project as data.** `lidza context` dumps routes, handler
  signatures, schemas and Rust exports; `lidza mcp` serves the same over
  MCP with diagnostics, logs and callable pack capabilities; `/llms.txt`
  describes the running app.
- **No hidden state.** Configuration is a JSON file and environment
  variables; generated files say so in their first line; conventions are
  written down in the app's own guide, which `lidza new` produces.

## Packs

A pack is the unit of capability: a manifest (`pack.lidza.json`) naming
capabilities with input and output types from the schema, a Rust crate
compiled to WASM, and a generated Go wrapper that exposes each capability
as a typed method. Official packs cover the infrastructure most apps need
(database, sessions, jobs, cache, localization, realtime, media,
geospatial); local packs hold domain logic. Every capability is also an
MCP tool, so an agent can run it before wiring it. Packs register their
services in a typed registry that handlers read from the request context.

## Scalable by default

Every package follows the same rules from its first commit: no request
state in process memory, every goroutine owned by a context, every
blocking call under a deadline, every collection that grows with traffic
bounded, every limit configurable. Sessions are signed tokens backed by
rows, fan-out goes through a Valkey bus, compute runs in bounded pools,
and each app exposes `/metrics`, `/healthz` and `/readyz`.
`docs/scalability.md` has the rules and the status of each primitive.

## Decisions

Recorded with their reasons in `docs/features.md`: `sqlc` instead of an
ORM, build-time prerendering with an optional per-request sidecar instead
of always-on SSR, sessions in Postgres instead of a second store, a small
Postgres queue instead of River, `html/template` instead of `templ` for
htmx.
