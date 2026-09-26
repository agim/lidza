# Feature matrix

The capabilities a complete framework needs, and where Līdza provides each
one. Status: **done**, **phase N** (planned, see `docs/roadmap.md`),
**template** (the chosen frontend provides it; Līdza selects and configures,
does not reimplement).

| Capability | Status | Where |
|---|---|---|
| CLI tools | done | `lidza new` (with `--packs` and `--agent`), `setup`, `dev`, `build`, `check`, `gen`, `gen resource`, `pack`, `db`, `test`, `verify`, `ship`, `update`, `benchmark`, `doctor`, `api`, `snippet`, `recipe`, `decision`, `credentials`, `mcp` |
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
| Data binding | done | handlers publish over the `realtime` pack, `realtime.Authorize` decides per topic who may subscribe; `useLive(topics)` in the template invalidates the matching queries |
| Session management and token auth | done | `auth` pack: argon2id, JWT access tokens, refresh sessions in Postgres, cookies or bearer, `auth.Require`; browser sessions slide (the middleware renews an expired access cookie from the refresh cookie) |
| Job queues and background workers | done | `jobs` pack: Postgres queue, bounded workers, retries, scheduling; heavy work in a pack capability |
| Localization | done | `i18n` pack: catalogs embedded, locale per request, numbers, currency, dates, catalog endpoint for the frontend |
| Mocking and stubbing | done | `lidza test` with the test database created and migrated; `lidzatest.Start` boots the app with a JSON client, a controllable clock (`lidza.Now`) and recorded or stubbed outbound HTTP (`lidza.HTTPClient`); `lidza test --e2e` runs Playwright against the built binary; `CACHE_URL=memory` |
| Accessibility checks | done | `eslint-plugin-jsx-a11y` in the `react` template; `lidza check` reports its findings as errors |
| State hydration and dehydration | done | react: prerendered and server-rendered pages carry the router's hydration payload (loader data, serialized by TanStack Router), `RouterClient` hydrates it, loaders do not run again; svelte: prerendered markup hydrated |
| Agent tools | done | `lidza mcp` (framework tools, pack capabilities, `lidza_errors`, `lidza_api`) plus the app's own `lidza.Tool`s as `app_<name>`; the binary serves them at `/mcp` behind `LIDZA_MCP_TOKEN` |
| CLI over MCP | done | every `lidza` command is an MCP tool run in the project with one JSON result (`lidza_check`, `lidza_gen`, `lidza_gen_resource`, `lidza_pack_*`, `lidza_db_*`, `lidza_test`, `lidza_verify`, `lidza_build`, `lidza_doctor`); the agent eval runs with no shell access |
| App recipes | done | "App recipes" section of the guide, `lidza recipe add`, the MCP tool `lidza_recipe_add`; the framework's recipes refreshed by `lidza gen`, the app's never touched; recipe lists in the agent files kept current |
| Agent guidance | done | the app guide's recipes as MCP prompts and `.claude/skills`; `lidza api` and `lidza://api` for the framework's real signatures; `lidza snippet` and `lidza_snippet` for the reference app's verified code; rules L003 (hand-written `fetch`), L004 (nonexistent or undeclared imports), L005 (handler types outside the schema); `lidza verify` in the pre-commit hook |
| Rust compute plane | done (opt-in) | packs: `lidza_export!` capabilities over schema types, wazero pool with memory cap and deadline, `uninterruptible` for input-bounded loops; measured against Go in `pkg/engine` benchmarks; guidance with numbers in the app guide ("Rust: when and how"); the reference app's `stats` pack as snippets |
| Continuous integration | done | GitHub Actions: framework checks and tests, platform evals, the reference app's verify and browser test, with Postgres and Valkey |
| Deployment | done | `Dockerfile`, `.dockerignore`, `deploy/<name>.service` from `lidza new`; `docs/deploy.md`; `DB_MIGRATE=true` |
| Sign-in | done | `auth.Mount`: registration, login, logout, session, email verification, password reset and change on the pack's own `auth_user` table; sign-in providers from `AUTH_PROVIDERS` (Google, GitHub, Microsoft, any OIDC issuer with discovery) with PKCE, nonce and key verification, identities linked to accounts in `auth_identity`, the admin pages hold the credentials; rule L013 |
| Auth hardening | done | `auth.Throttle()`, `ValidatePassword`, one-time tokens for verification and reset, sessions ended at once; `router.Route` per-route middleware |
| Transactional email | done | `mail` pack: one `Send`, Mailgun, SendGrid, Postmark, Resend and SMTP spoken directly, `log` and `outbox` providers, templates in `mail/`, outbox table, delivery through the jobs pack with retries, `lidza_mail` MCP tool, rule L006 against vendor SDKs |
| Authorization | done | `auth.Require()` and `auth.Optional()` middleware, `r.Group(prefix, mw...)` for a protected sub-tree, `auth.CurrentUser(ctx)`; the CSRF guard accepts JSON content or a same-origin `Sec-Fetch-Site` |
| TLS built in | done | `LIDZA_TLS_DOMAINS`: HTTPS on 443 with Let's Encrypt through `autocert`, 80 redirected, certificates in Postgres through the db pack (every node shares them) or a directory for one node, `APP_URL` and Secure cookies derived, `lidza doctor` checks DNS, ports and the bind capability, the systemd unit grants it |
| File storage | done | `storage` pack: `Put`, `Get`, `Stat`, `List`, `Delete`, presigned URLs, any S3-compatible service through Signature V4 spoken directly, `local` provider for tests, `storage.Handler` for private files, rule L008; wire format proven by the AWS documentation example, a stub and MinIO live |
| Admin pages | done | `packs/admin` on Tabler, light and dark, served from the binary under a strict CSP: overview with a setup checklist, users and sign-in providers, mail, model and storage with per-provider settings saved sealed and applied live, token usage, outbox, jobs with retry, files; the app's own pages (`Options.Pages`) and settings (`Options.Sections`); themed with `admin/theme.css` and `admin/layout.html`; rule L014 |
| Secrets | done | `config/credentials.yml.enc` sealed with AES-256-GCM under `config/master.key` or `LIDZA_MASTER_KEY`, merged into every pack's configuration by `pkg/env`, `lidza credentials` and MCP tools, runtime values in Postgres through the db pack, packs reconfigure on save |
| Language models | done | `llm` pack: one `Chat`, `Stream`, `Embed`, `Generate[T]` (structured output from schema types) and `Run` (the app's tools offered to the model); Anthropic, OpenAI, Google and Ollama spoken directly, `fake` for tests, retries with backoff, deadlines, token counts on `/metrics`, `lidza_llm` MCP tool, rule L007 against vendor SDKs; wire formats proven by stubs, Ollama live, hosted providers by recorded fixtures; every call recorded in `llm_usage` with a label (`lidza_llm_usage`) |
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
