# Feature matrix

The capabilities a complete framework needs, and where Līdza provides each
one. Status: **done**, **phase N** (planned, see `docs/roadmap.md`),
**template** (the chosen frontend provides it; Līdza selects and configures,
does not reimplement).

| Capability | Status | Where |
|---|---|---|
| CLI tools | done | `lidza new`, `dev`, `build`, `check`; generators for handlers, pages and packs in phase 4 |
| Hot module replacement | done | Vite HMR through the `lidza dev` proxy; Go handlers rebuild and restart, frontend state is kept |
| API route parameter parsing | done | `net/http` patterns, `req.Param("id")` in typed handlers; the client takes them as a typed object |
| Automated asset bundling | done | Vite: minify, hash, code split, `dist/` embedded in the binary. Image optimization: phase 3 template build |
| Query caching | done | TanStack Query in the template; `cache` pack (Valkey, `Remember`, prefix invalidation) on the server |
| Virtual DOM / efficient diffing | template | React 19 (`react`), compiled updates (`svelte`) |
| Component lifecycle hooks (frontend) | template | React effects; Svelte lifecycle |
| Application lifecycle hooks (server) | done | `lidza.App.OnStart`, `OnReady`, `OnShutdown` |
| HTTP middleware pipeline | done | `pkg/middleware`; request id, log, recovery and deadline on every API route; `router.Use`, `App.Middleware` |
| CORS | done | `middleware.CORS`; off by default, same-origin is the normal case |
| Built-in vulnerability shields | done | secure headers on every response, CSP and HSTS opt-in; body limits; SQL injection prevented by parameterized queries only (`sqlc`, `pgx`); XSS by template escaping plus CSP; CSRF tokens arrive with cookie sessions |
| Environment configuration injection | done | `pkg/env`: typed struct from `.env`, `.env.<mode>` and the environment; secrets never in `lidza.json` |
| Form validation | done | rules in `schema.lidza`; generated `Validate()` runs before every typed handler (422 with field errors, `ApiError.fields` on the client); `validators.ts` in `@lidza/client` applies the same rules in the browser |
| Automatic error boundaries | done | React error boundary in the template; Go panic recovery returning a JSON error |
| Database schema migrations | done | `lidza gen` diffs `schema.lidza` against `db/schema.lock.json` into numbered up/down scripts; `lidza db migrate`, `rollback`, `status` apply them under an advisory lock |
| Database connection pooling | done | `pgxpool` in the `db` pack, bounded by `DB_MAX_CONNS`; `/readyz` pings it, `/metrics` reports it |
| Object-relational mapping | done | `sqlc` via the `db` pack: SQL in `db/queries/*.sql`, `lidza gen` writes typed Go; see "Decisions" |
| Dependency injection | done | `lidza.Services`: packs and `OnStart` provide values by type, handlers read them with `lidza.Service[T](ctx)`; explicit, no scanning |
| Data binding | done | handlers publish over the `realtime` pack; `useLive(topics)` in the template invalidates the matching queries |
| Session management and token auth | done | `auth` pack: argon2id, JWT access tokens, refresh sessions in Postgres, cookies or bearer, `auth.Require` |
| Job queues and background workers | done | `jobs` pack: Postgres queue, bounded workers, retries, scheduling; heavy work in a pack capability |
| Localization | done | `i18n` pack: catalogs embedded, locale per request, numbers, currency, dates, catalog endpoint for the frontend |
| Mocking and stubbing | done (harness) | `lidza test` with the test database created and migrated; `lidzatest.Start` boots the app on httptest with a JSON client; `CACHE_URL=memory`; fake clock and recorded fixtures not built |
| Accessibility checks | done | `eslint-plugin-jsx-a11y` in the `react` template; `lidza check` reports its findings as errors |
| State hydration and dehydration | not needed | prerendered pages carry markup, not data; the client fetches once after hydration. TanStack Query `dehydrate`/`hydrate` would come with per-request SSR |
| Observability | done | `/metrics`, `/healthz`, `/readyz`, request log with ids, `/debug/pprof/` in dev |
| Rate limiting and circuit breakers | done | `middleware.RateLimit` (token bucket per key, bounded table), `resilience.Breaker` |
| Load testing | done | `lidza benchmark` on k6 with heap and goroutine comparison; `benchmarks/scale_test.js` in every app |
| Server-side rendering | done (prerendering) | `react` and `astro` templates emit static HTML per route at build time, hydrated on the client, data from `/api`; see "Decisions" |

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
