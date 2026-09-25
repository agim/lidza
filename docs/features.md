# Feature matrix

The capabilities a complete framework needs, and where Līdza provides each
one. Status: **done**, **phase N** (planned, see `docs/roadmap.md`),
**template** (the chosen frontend provides it; Līdza selects and configures,
does not reimplement).

| Capability | Status | Where |
|---|---|---|
| CLI tools | done | `lidza new`, `dev`, `build`, `check`, `gen`, `gen resource`, `pack`, `db`, `test`, `verify`, `benchmark`, `doctor`, `api`, `mcp` |
| CRUD | done | `lidza gen resource <Model>`: queries, Create/Update/List types with the model's rules, five typed routes, row mapping, registration; edited freely after |
| Hot module replacement | done | Vite HMR through the `lidza dev` proxy; Go handlers rebuild and restart, frontend state is kept |
| API route parameter parsing | done | `net/http` patterns, `req.Param("id")` in typed handlers; the client takes them as a typed object |
| Automated asset bundling | done | Vite: minify, hash, code split, `dist/` embedded in the binary. Image optimization: phase 3 template build |
| Query caching | done | TanStack Query in the template; `cache` pack (Valkey, `Remember`, prefix invalidation) on the server |
| Styling | done | Tailwind v4 in the `react` template with a CSS-variable theme; no component library |
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
| Client SDKs | done | `@lidza/client` (TypeScript) always; `lidza_client` (Dart) when `lidza.json` names a directory under `sdk.dart` |
| Object-relational mapping | done | `sqlc` via the `db` pack: SQL in `db/queries/*.sql`, `lidza gen` writes typed Go; see "Decisions" |
| Dependency injection | done | `lidza.Services`: packs and `OnStart` provide values by type, handlers read them with `lidza.Service[T](ctx)`; explicit, no scanning |
| Data binding | done | handlers publish over the `realtime` pack; `useLive(topics)` in the template invalidates the matching queries |
| Session management and token auth | done | `auth` pack: argon2id, JWT access tokens, refresh sessions in Postgres, cookies or bearer, `auth.Require` |
| Job queues and background workers | done | `jobs` pack: Postgres queue, bounded workers, retries, scheduling; heavy work in a pack capability |
| Localization | done | `i18n` pack: catalogs embedded, locale per request, numbers, currency, dates, catalog endpoint for the frontend |
| Mocking and stubbing | done | `lidza test` with the test database created and migrated; `lidzatest.Start` boots the app with a JSON client, a controllable clock (`lidza.Now`) and recorded or stubbed outbound HTTP (`lidza.HTTPClient`); `lidza test --e2e` runs Playwright against the built binary; `CACHE_URL=memory` |
| Accessibility checks | done | `eslint-plugin-jsx-a11y` in the `react` template; `lidza check` reports its findings as errors |
| State hydration and dehydration | partial | prerendered pages carry markup; SSR pages carry markup rendered from loader data, and the client runs the loaders again on navigation (no dehydrated payload) |
| Agent tools | done | `lidza mcp` (framework tools, pack capabilities, `lidza_errors`, `lidza_api`) plus the app's own `lidza.Tool`s as `app_<name>`; the binary serves them at `/mcp` behind `LIDZA_MCP_TOKEN` |
| Agent guidance | done | the app guide's recipes as MCP prompts and `.claude/skills`; `lidza api` and `lidza://api` for the framework's real signatures; rules L003 (hand-written `fetch`), L004 (nonexistent or undeclared imports), L005 (handler types outside the schema) |
| Error reporting and analytics | done (opt-in) | `analytics` pack: panics, 500s, frontend errors and events in Postgres, OTLP export, `lidza_errors` MCP tool; off unless added |
| Observability | done | `/metrics`, `/healthz`, `/readyz`, structured logs (`lidza.Log(ctx)` with request ids, JSON in production, `LIDZA_LOG_LEVEL`), `/debug/pprof/` in dev |
| Time zones | done | UTC in storage and on the wire; `i18n` formats `Date`, `Time`, `DateTime` in the visitor's zone (`tz` cookie set by the template, `X-Timezone` header, `I18N_TIMEZONE` default) |
| Rate limiting and circuit breakers | done | `middleware.RateLimit` (token bucket per key, bounded table), `resilience.Breaker` |
| Load testing | done | `lidza benchmark` on k6 with heap and goroutine comparison; `benchmarks/scale_test.js` in every app |
| Server-side rendering | done | build-time prerendering for `react` and `astro`; per-request rendering with `LIDZA_SSR=1` through the Node sidecar (loaders run on the server with the visitor's cookies), falling back to the static page; see "Decisions" |

## Decisions

Decided 2026-09-25.

**ORM: `sqlc`.** The developer (or agent) writes SQL in `.sql` files; `sqlc`
generates typed Go functions and structs checked against the schema. Rows
map to structs, nothing is hidden, output is deterministic. No query
builder, no lazy loading; a full ORM is not planned.

**SSR: build-time prerendering, per-request rendering optional.** The
`astro` template and Vite prerendering for `react` produce static HTML at
build time; the Go binary serves it and the page hydrates on the client.
Dynamic data comes from `/api` through the generated client and TanStack
Query. Apps that need per-user HTML before JavaScript runs set
`LIDZA_SSR=1`: the binary runs the Node sidecar from `dist/.server` and
falls back to the static page whenever it cannot render. Those
deployments need Node beside the binary.
