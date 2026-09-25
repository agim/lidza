# Feature matrix

The capabilities a complete framework needs, and where Līdza provides each
one. Status: **done**, **phase N** (planned, see `docs/roadmap.md`),
**template** (the chosen frontend provides it; Līdza selects and configures,
does not reimplement).

| Capability | Status | Where |
|---|---|---|
| CLI tools | done | `lidza new`, `dev`, `build`, `check`; generators for handlers, pages and packs in phase 4 |
| Hot module replacement | done | Vite HMR through the `lidza dev` proxy; Go handlers rebuild and restart, frontend state is kept |
| API route parameter parsing | done | `net/http` patterns, `req.PathValue("id")`; typed parameters from the schema in phase 3 |
| Automated asset bundling | done | Vite: minify, hash, code split, `dist/` embedded in the binary. Image optimization: phase 3 template build |
| Query caching | done (client) / phase 6 (server) | TanStack Query in the template; server-side cache pack on Valkey |
| Virtual DOM / efficient diffing | template | React 19 (`react`), compiled updates (`svelte`) |
| Component lifecycle hooks (frontend) | template | React effects; Svelte lifecycle |
| Application lifecycle hooks (server) | phase 3 | `lidza.App` gains `OnStart`, `OnReady`, `OnShutdown`; `lidza dev` reload hooks |
| HTTP middleware pipeline | phase 3 | `router.Use(...)`; request log, panic recovery, request id, timeouts as the first middlewares |
| CORS | phase 3 | `middleware.CORS` with origins from `lidza.json`; off by default, same-origin is the normal case |
| Built-in vulnerability shields | phase 3 (headers, CSRF) / phase 4 (SQL) | secure headers and CSP middleware; CSRF tokens for cookie sessions; SQL injection prevented by parameterized queries only (`sqlc`, `pgx`), never string-built SQL; XSS by React escaping plus CSP |
| Environment configuration injection | phase 3 | `pkg/env`: typed config struct filled from the environment and `.env.<mode>` files; secrets never in `lidza.json` |
| Form validation | phase 3 | rules in the single schema source; Go validators and generated TypeScript validators from the same rules, one message catalog |
| Automatic error boundaries | phase 3 | React error boundary in the template; Go panic recovery middleware returning a JSON error |
| Database schema migrations | phase 3 / phase 4 | migrations generated from the schema source (phase 3); `lidza db migrate`, `rollback`, `status` in the `db` pack (phase 4) |
| Database connection pooling | phase 4 | `pgxpool` in the `db` pack, bounded by config; `/readyz` reports pool health |
| Object-relational mapping | phase 4 | `sqlc` in the `db` pack: SQL in `.sql` files, generated typed Go functions and structs; see "Decisions" |
| Dependency injection | phase 4 | `lidza.Services`: typed constructors registered once, resolved by type at startup and per request; packs register their services. Explicit, no reflection scanning |
| Data binding | phase 4 / phase 7 | server pushes invalidations over the `realtime` pack; TanStack Query refetches; a `useLive` hook in the template |
| Session management and token auth | phase 6 | `auth` pack: PASETO or JWT, refresh, Valkey-backed sessions, `lidza auth` commands |
| Job queues and background workers | phase 6 | `jobs` pack on River (Postgres-backed); Rust workers for compute |
| Localization | phase 6 | `i18n` pack: `Accept-Language` negotiation, message catalogs, `golang.org/x/text` for dates, numbers, currencies; template hook for the frontend |
| Mocking and stubbing | phase 6 | `lidza test`: `lidza_test` database per run, `httptest` helpers, fake clock, recorded HTTP fixtures |
| Accessibility checks | phase 7 | `eslint-plugin-jsx-a11y` in the template, findings ingested by `lidza check --json` |
| State hydration and dehydration | phase 7 | TanStack Query `dehydrate`/`hydrate`; only meaningful with SSR or prerendering |
| Server-side rendering | phase 7 | build-time prerendering, hydration on the client, dynamic data from `/api`; see "Decisions" |

## Decisions

Decided 2026-09-25.

**ORM: `sqlc`.** The developer (or agent) writes SQL in `.sql` files; `sqlc`
generates typed Go functions and structs checked against the schema. Rows
map to structs, nothing is hidden, output is deterministic. No query
builder, no lazy loading; a full ORM is not planned.

**SSR: build-time prerendering.** The `astro` template (phase 3) and Vite
prerendering for `react` produce static HTML at build time; the Go binary
serves it and the page hydrates on the client. Dynamic data comes from
`/api` (handlers on `sqlc` queries) through the generated client and
TanStack Query, so interactive apps work unchanged; prerendering only
replaces the empty `index.html` first paint. Per-request, per-user HTML
(complete before JavaScript runs) is not covered; an optional Node SSR
sidecar can be added later for apps that need it, at the cost of the
single binary for those apps.
