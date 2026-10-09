# Feature matrix

The capabilities a complete framework needs, and where Līdza provides each
one. Status: **done**, **phase N** (planned, see `docs/roadmap.md`),
**template** (the chosen frontend provides it; Līdza selects and configures,
does not reimplement).

| Capability | Status | Where |
|---|---|---|
| CLI tools | done | `lidza new` (with `--packs` and `--agent`), `setup`, `dev`, `build`, `check`, `gen`, `gen resource`, `pack`, `db`, `test`, `verify`, `ship`, `update`, `benchmark`, `doctor`, `api`, `snippet`, `recipe`, `decision`, `credentials`, `mcp` |
| CRUD | done | `lidza gen resource <Model>`: queries, Create/Update/List types with the model's rules, five typed routes, row mapping, registration behind `auth.Require()`; an owned model (`ownerId`, `@ref(User)`) scoped to the signed-in user with 404 for another's rows, `@shared` (`--shared`) for rows signed-in users share, `@public` (`--public`) for open ones; edited freely after; rule L018 |
| Hot module replacement | done | Vite HMR through the `lidza dev` proxy; Go handlers rebuild and restart, frontend state is kept |
| API route parameter parsing | done | `net/http` patterns, `req.Param("id")` in typed handlers; the client takes them as a typed object; a group's prefix may hold them too (`r.Group("/api/v1/posts/{id}")`) |
| Automated asset bundling | done | Vite: minify, hash, code split, `dist/` embedded in the binary. Image optimization: phase 3 template build |
| Query caching | done | TanStack Query in the template; `cache` pack (Valkey, `Remember`, prefix invalidation, atomic `Incr` counters shared across nodes) on the server |
| Styling | done | Tailwind v4 in the `react` template with a CSS-variable theme; no component library |
| Virtual DOM / efficient diffing | template | React 19 (`react`), compiled updates (`svelte`) |
| Component lifecycle hooks (frontend) | template | React effects; Svelte lifecycle |
| Application lifecycle hooks (server) | done | `lidza.App.OnStart`, `OnReady`, `OnShutdown` |
| HTTP middleware pipeline | done | `pkg/middleware`; request id, log, recovery and deadline on every API route; `router.Use`, `App.Middleware` |
| CORS | done | `middleware.CORS`; off by default, same-origin is the normal case |
| Built-in vulnerability shields | done | secure headers on every response, a strict CSP by default outside dev (`middleware.DefaultCSP`, extended with `middleware.AddCSP`; the served pages' own inline scripts allowed by hash), HSTS wherever `APP_URL` is `https://` (`App.HSTS`), `/.well-known/security.txt` from `SECURITY_CONTACT`, the Permissions-Policy set per app (`App.PermissionsPolicy`, `middleware.AllowGeolocation` for location); body limits; SQL injection prevented by parameterized queries only (`sqlc`, `pgx`); XSS by template escaping plus CSP; CSRF tokens arrive with cookie sessions |
| Environment configuration injection | done | `pkg/env`: typed struct from `.env`, `.env.<mode>` and the environment; secrets never in `lidza.json` |
| Form validation | done | rules in `schema.lidza`; generated `Validate()` runs before every typed handler (422 with field errors, `ApiError.fields` on the client); `validators.ts` in `@lidza/client` applies the same rules in the browser |
| Automatic error boundaries | done | React error boundary in the template; Go panic recovery returning a JSON error |
| Database schema migrations | done | `lidza gen` diffs `schema.lidza` against `db/schema.lock.json` into numbered up/down scripts; `lidza db migrate`, `rollback`, `status` apply them under an advisory lock. Identity ids (`@default(autoincrement())`) and exact decimals (`decimal(p, s)`: numeric in Postgres, `decimal.Decimal` in Go and sqlc, a string in the clients) |
| Database connection pooling | done | `pgxpool` in the `db` pack, bounded by `DB_MAX_CONNS`; `/readyz` pings it, `/metrics` reports it |
| Client SDKs | done | `@lidza/client` (TypeScript) always; `lidza_client` (Dart) when `lidza.json` names a directory under `sdk.dart`; a `router.Stream` route (server-sent events) is an async iterable in TypeScript and a `Stream` in Dart; a `router.File` route (upload) takes a File or Blob with upload progress in TypeScript, bytes in Dart |
| Object-relational mapping | done | `sqlc` via the `db` pack: SQL in `db/queries/*.sql`, `lidza gen` writes typed Go; see "Decisions" |
| Dependency injection | done | `lidza.Services`: packs and `OnStart` provide values by type, handlers read them with `lidza.Service[T](ctx)`; explicit, no scanning |
| Data binding | done | handlers publish over the `realtime` pack, `realtime.Authorize` decides per topic who may subscribe (closed without it); `useLive(topics)` in the template invalidates the matching queries |
| Session management and token auth | done | `auth` pack: argon2id, JWT access tokens, refresh sessions in Postgres, cookies or bearer, `auth.Require`; browser sessions slide (the middleware renews an expired access cookie from the refresh cookie) |
| Job queues and background workers | done | `jobs` pack: Postgres queue, bounded workers, retries, delayed runs, idempotency keys (`Unique`: one pending or running job per kind and key across nodes), a per-kind cap on running jobs across nodes (`Concurrency`), recurring schedules (`Every`, `Daily`, `DailyAt` for several times a day, `Weekly` in a time zone; one job per due time across nodes), release of running jobs at shutdown; heavy work in a pack capability |
| Localization | done | `i18n` pack: catalogs embedded, locale per request, numbers, currency, dates, catalog endpoint; react and svelte pages translate with `src/i18n.ts`, prerendered once per locale and served in the visitor's, catalog in the page; mail templates per language (`mail/<name>.<lang>.txt.tmpl`) in the request's locale or `Accept-Language` |
| Mocking and stubbing | done | `lidza test` with the test database created and migrated; `lidzatest.Start` boots the app with a JSON client, a controllable clock (`lidza.Now`) and recorded or stubbed outbound HTTP (`lidza.HTTPClient`); `lidza test --e2e` runs Playwright against the built binary; `CACHE_URL=memory` |
| Accessibility checks | done | `eslint-plugin-jsx-a11y-x` (the maintained fork of `eslint-plugin-jsx-a11y`) in the `react` and `astro` templates; `lidza check` reports its findings as errors |
| State hydration and dehydration | done | react: prerendered and server-rendered pages carry the router's hydration payload (loader data, serialized by TanStack Router), `RouterClient` hydrates it, loaders do not run again; svelte: prerendered markup hydrated |
| Agent tools | done | `lidza mcp` (framework tools, pack capabilities, `lidza_errors`, `lidza_api`) plus the app's own `lidza.Tool`s as `app_<name>`; the binary serves them at `/mcp` behind `LIDZA_MCP_TOKEN` |
| CLI over MCP | done | every `lidza` command is an MCP tool run in the project with one JSON result (`lidza_check`, `lidza_gen`, `lidza_gen_resource`, `lidza_pack_*`, `lidza_db_*`, `lidza_test`, `lidza_verify`, `lidza_build`, `lidza_doctor`); the agent eval runs with no shell access |
| App recipes | done | "App recipes" section of the guide, `lidza recipe add`, the MCP tool `lidza_recipe_add`; the framework's recipes refreshed by `lidza gen`, the app's never touched; recipe lists in the agent files kept current |
| Agent guidance | done | the app guide's recipes as MCP prompts and `.claude/skills`; `lidza api` and `lidza://api` for the framework's real signatures; `lidza snippet` and `lidza_snippet` for the reference app's verified code; rules L003 (hand-written `fetch`), L004 (nonexistent or undeclared imports), L005 (handler types outside the schema); `lidza verify` in the pre-commit hook |
| Rust compute plane | done (opt-in) | packs: `lidza_export!` capabilities over schema types, wazero pool with memory cap and deadline, `uninterruptible` for input-bounded loops; measured against Go in `pkg/engine` benchmarks; guidance with numbers in the app guide ("Rust: when and how"); error codes the caller can branch on (`abi::Error::code`, `engine.ErrorCode`); the official `media` pack resizes to fit or to cover and crop, and rejects HEIC, AVIF and TIFF with `unsupported_format`; the reference app's `stats` pack as snippets |
| Continuous integration | done | GitHub Actions: framework checks and tests, platform evals, the reference app's verify and browser test, with Postgres and Valkey |
| Deployment | done | `Dockerfile`, `.dockerignore`, `deploy/<name>.service` from `lidza new`; `docs/deploy.md`; `DB_MIGRATE=true` |
| Sign-in | done | `auth.Mount`: registration, login, logout, session, email verification, password reset and change on the pack's own `auth_user` table; sign-in providers from `AUTH_PROVIDERS` (Google, GitHub, Microsoft, any OIDC issuer with discovery) with PKCE, nonce and key verification, identities linked to accounts in `auth_identity`, the admin pages hold the credentials; `OnSignUp` and `OnDeleteUser` in the account's transaction, `OnSignIn` with the request and the reply's cookies, `OnEvent` for a security log (sign-ins and failures, sign-outs, password changes and resets, verifications, deletions), "remember me" (`remember: false` for session cookies), `NoVerifyEmail`, `DeleteUser` and a confirmed "delete my account" route; rule L013 |
| Auth hardening | done | `auth.Throttle()` and `auth.ThrottleSignIn()` (its own `AUTH_SIGNIN_RPS`), `ValidatePassword`, one-time tokens for verification and reset, sessions ended at once; `router.Route` per-route middleware |
| Transactional email | done | `mail` pack: `Send`, `SendTx` to commit app writes with the outbox and delivery job, To and Reply-To lists, Cc/Bcc and bounded byte/inline attachments preserved across retries, Mailgun, SendGrid, Postmark, Resend and SMTP spoken directly, `log` and `outbox` providers, templates in `mail/` with a copy per language and a template-defined subject, outbox table, delivery through the jobs pack with retries, `lidza_mail` MCP tool, rule L006 against vendor SDKs |
| Authorization | done | `auth.Require()` and `auth.Optional()` middleware, `r.Group(prefix, mw...)` for a protected sub-tree (its routes share the one ServeMux, so the most specific pattern wins across groups), `auth.CurrentUser(ctx)`; the CSRF guard accepts JSON content or a same-origin `Sec-Fetch-Site` |
| TLS built in | done | `LIDZA_TLS_DOMAINS`: HTTPS on 443 with Let's Encrypt through `autocert`, 80 redirected, certificates in Postgres through the db pack (every node shares them) or a directory for one node, `APP_URL` and Secure cookies derived, `lidza doctor` checks DNS, ports and the bind capability, the systemd unit grants it |
| File storage | done | `storage` pack: `Put`, `Get`, `Stat`, `List`, `Delete`, presigned URLs, any S3-compatible service through Signature V4 spoken directly, `local` provider for tests, `storage.Handler` for private files, typed uploads (`router.Route` with `router.File`, `router.UploadLimit`), rule L008; wire format proven by the AWS documentation example, a stub and MinIO live |
| App brief | done | `docs/brief.md` filled by an interview with suggestions (`lidza brief`, MCP `lidza_brief` and `lidza_brief_answer`, recipe "Start with the brief"); answers become decisions, working agreements in the agent files, palette tokens and seeded app recipes; Team notes (`lidza note add`) replace an agent's local memory; rule L015 (a note) |
| External accounts | done | `auth.Connection`: a signed-in user connects an account of another service (`AUTH_CONNECT=github`, `auth.GitHubConnect` with repository and webhook scopes, `auth.OAuth2Connect` for any OAuth 2.0 provider) through start and callback routes bound to the user and the browser, one-time, with PKCE; the grant sealed with the master key in `auth_connection`, owner-scoped, listed without tokens, renewed with bounded concurrent refreshes, `ErrReconnect` when revoked, revoked at the provider on disconnect; recipe "Connect an external account" |
| Dates and time zones | done | `time` (an instant, UTC in JSON), `date` (a calendar day, `civil.Date`, `"2026-10-06"` both ways, never shifted), `localtime` in API types (a typed wall-clock time, `civil.DateTime`) made an instant by `i18n.At` in the request's zone with the daylight-saving gap and overlap handled; the request's zone from the browser (`tz` cookie) or `X-Timezone`, an app's stored one through `i18n.WithTimezone`; `@timezone` validation; `i18n` `Today`, `Relative`, `Date`, `Time`, `DateTime`; `src/datetime.ts` formatting in pages; zone data embedded; recipe "Dates and time zones" |
| Lists | done | `pkg/list`: `list.Read` validates a list route's page (capped), search, time range, sort among allowed keys and equality filters (422 by parameter); `lidza gen resource` writes list and count queries that search the text fields, filter on enums, booleans and references, bound the time and sort by the plain fields; `src/list.tsx` in the react template keeps the state in the address (`useListState`, `listQuery`) and draws `ListToolbar`, `FilterSelect`, `SortHead`, `Pager`, with `pageRows` for lists in the browser; recipe "Add a paginated, filterable list" |
| Full-text search | done | `@search` fields and `@@search("language")` in `schema.lidza`: a weighted GIN index; `gen resource` lists rank by relevance, stem, take websearch syntax and return highlights as parts; `<Highlight>` in `src/list.tsx` |
| Layout audit | done | `lidza audit layout`: the built app on the test database, every prerendered page plus `--routes`, signed in as a throwaway user, at each viewport and theme; sideways scrolling is a fault with the elements past the edge named, scrolling down a fault past `--max-scroll`; `e2e/layout.ts` (`expectNoSidewaysScroll`, `expectFitsViewport`) for specs, and the templates' home spec checks a phone's width |
| Performance audit | done | `lidza audit performance`: every page cold on a throttled phone, several samples; median FCP, LCP, CLS, TBT, bytes by kind, unused JavaScript, advice (compression, caching, image sizes, LCP priority, SEO basics, `llms.txt`), faults over `--budget`; MCP `lidza_audit_performance` |
| Compressed delivery | done | `lidza build` stores Brotli and gzip copies of compressible frontend files; the binary negotiates `Accept-Encoding` with `Vary`, ETags, HEAD and 304, and gzips pages built per request |
| Static pages without a client runtime | done | react: `staticData: { static: true }` prerenders a page without React, the router or the query client; `src/enhance/` scripts add behaviour per page |
| Responsive images | done | react, svelte: `?responsive` imports build AVIF, WebP and fallback widths, `<Picture>` renders them; astro: `astro:assets` |
| Agent discovery file | done | `lidza gen llms` writes a public `llms.txt`; missing files answer 404, not the app shell |
| Roles and permissions | done | `auth.Roles`: the app's roles mapped to permissions in code (`name`, `prefix.*`, `*`), memberships per scope (a team, a workspace; `auth.AppWide` for all) in `auth_member`; `Roles.Require(permission, scope)` middleware (401, 403, 500 on a failed check, never through), `Check`, `Can`, `Grant`, `Revoke`, `RevokeAll`, `Of`, `Scopes`, paginated `Members`; checked against the database on every request, so a revocation or a deleted account is refused at once; recipe "Roles and permissions" |
| Teams and invitations | done | `auth.Teams`: workspaces on `auth.Roles` scopes, email invitations bound to the address, roles without escalation, the last owner kept, switching; `auth.RequireWorkspace` and `workspaceId` scoping in `gen resource`; admin Workspaces page |
| Audit log | done | `packs/audit`: `Record` and `RecordTx` (in the change's transaction) of action, resource, scope, outcome and up to 20 metadata strings, the actor from the signed-in user or `audit.System` for jobs; secret-named keys and secret-shaped values redacted (`pkg/secrets`, shared with rule L010); `List` with filters and keyset pages; `AUDIT_RETENTION` pruning; admin actions recorded when both packs run; recipe "Record an audit event" |
| Admin pages | done | `packs/admin` on Tabler, light and dark, served from the binary under a strict CSP: overview with a setup checklist, users and sign-in providers, mail, model and storage with per-provider settings saved sealed and applied live, token usage, outbox, jobs with retry and schedules, files; the app's own pages (`Options.Pages`) and settings (`Options.Sections`); `OnAudit` for a staff audit log of every form and download, secrets redacted; themed with `admin/theme.css` and `admin/layout.html`, embedded through `Options.Templates` or read from disk; rule L014 |
| Secrets | done | `config/credentials.yml.enc` sealed with AES-256-GCM under `config/master.key` or `LIDZA_MASTER_KEY`, merged into every pack's configuration by `pkg/env`, `lidza credentials` and MCP tools, runtime values in Postgres through the db pack, packs reconfigure on save |
| Language models | done | `llm` pack: one `Chat`, `Stream`, `Embed`, `Generate[T]` (structured output from schema types) and `Run` (the app's tools offered to the model); Anthropic, OpenAI, Google, Ollama and OpenAI-compatible servers spoken directly, `fake` for tests; embeddings from the chat provider or another (`EMBED_PROVIDER`, or `none`), retries with backoff, deadlines, token counts on `/metrics`, `lidza_llm` MCP tool, rule L007 against vendor SDKs; wire formats proven by stubs, Ollama live, hosted providers by recorded fixtures; every call, chat or embedding, recorded in `llm_usage` with a label and its tokens (`lidza_llm_usage`) |
| Error reporting and analytics | done (opt-in) | `analytics` pack: panics, 500s, frontend errors and events in Postgres, OTLP export, `lidza_errors` MCP tool; off unless added |
| Observability | done | `/metrics`, `/healthz`, `/readyz`, structured logs (`lidza.Log(ctx)` with request ids, JSON in production, `LIDZA_LOG_LEVEL`), `/debug/pprof/` in dev |
| Time zones | done | UTC in storage and on the wire; `i18n` formats `Date`, `Time`, `DateTime` in the visitor's zone (`tz` cookie set by the template, `X-Timezone` header, `I18N_TIMEZONE` default) |
| Rate limiting and circuit breakers | done | `middleware.RateLimit` (token bucket per key, bounded table), `resilience.Breaker` |
| Load testing | done | `lidza benchmark` on k6 with heap and goroutine comparison; `benchmarks/scale_test.js` in every app |
| Server-side rendering | done | build-time prerendering for `react` and `astro`; per-request rendering with `LIDZA_SSR=1` through the Node sidecar (loaders run on the server with the visitor's cookies), falling back to the static page; see "Decisions" |
| Per-page head (SEO, link previews) | done | `App.Head` sets title, description, canonical, Open Graph and Twitter tags, JSON-LD, noindex and the status per request, in Go, for the shell and prerendered pages alike (and the sidecar's and the dev server's pages); every value escaped; recipe "Set a page's head" |

## Inline mail and reply lists

`mail.Attachment.ContentID` embeds a byte file in an HTML body. Inline files
share the configured count and byte limits with ordinary attachments; IDs
are unique, safe ASCII strings of at most 127 bytes. `Message.ReplyTo` also
accepts an address list bounded independently by the recipient limit. These
values and custom headers survive queued delivery and retries.

SMTP builds related resources inside the HTML alternative and ordinary files
outside it in a mixed part. Mailgun's MIME upload keeps content IDs independent
of filenames and sends To/Cc/Bcc as envelope recipients without a Bcc header.
Other providers use their native inline and reply-list fields. This extends
the mail pack's common message contract; applications need no provider SDK
or separate MIME implementation. Existing messages without inline files keep
their provider request shape.

Wire contracts: [Mailgun MIME](https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/messages/post-v3--domain-name--messages-mime),
[SendGrid](https://www.twilio.com/docs/sendgrid/api-reference/mail-send/mail-send),
[Postmark](https://postmarkapp.com/developer/api/email-api),
[Resend](https://resend.com/docs/api-reference/emails/send-email) and
[Resend content IDs](https://resend.com/changelog/embed-images-using-cid).
Local transport/MIME tests and a database-backed failed delivery/retry test
verify preservation; they do not establish live provider delivery.

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
