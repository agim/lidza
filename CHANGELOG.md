# Changelog

Releases are git tags; `go install github.com/agim/lidza/cmd/lidza@<tag>`
installs that CLI, and `lidza version` prints it. Apps depend on the same
version in `go.mod`. `install.sh` pins the newest release here;
`scripts/release.sh vX.Y.Z` turns "Unreleased" into a release, bumps the
pin, tags and pushes. A change that breaks an app built on an earlier
release is listed first under its version as "Breaking:", with what to
change. A release without one is additive: an app updates with
`lidza update --migrate`.

## Unreleased

- `lidza db migrate`, `rollback` and `status` without a `DATABASE_URL`
  say where it is read from and that `lidza setup` adds it and creates
  the database, instead of a bare "env: DATABASE_URL is required".

## v0.1.65 (2026-10-04)

- After a pull that enabled packs or added frontend packages (another
  session's, a teammate's), `lidza dev` failed: `npm install` ran only
  when `node_modules` was missing, and `lidza setup` left an existing
  `.env` alone, so a pack enabled since had no `DATABASE_URL`. `lidza
  dev`, `setup`, `test` and `build` now install when a `package.json`
  dependency is not in `node_modules`, naming them; `lidza setup` adds
  to an existing `.env` the `DATABASE_URL`, `AUTH_SECRET` or `MAIL_FROM`
  an enabled pack needs and no layer sets, and leaves the rest. The db
  pack's start error says `lidza setup` adds the address.

## v0.1.64 (2026-10-04)

- The settings Save bar now really covers a secret field's Show button on
  a phone: z-index 20, above Tabler's `.input-group-text` (10, honoured on
  a flex item); v0.1.63's 6 was not enough.

## v0.1.63 (2026-10-04)

- The settings pages' sticky Save bar covers the fields scrolling under
  it on a phone: a secret field's Show button drew over it (z-index 6, above
  an input group's controls). `TestSaveBarAboveInputs` checks it.

## v0.1.62 (2026-10-04)

- Admin Settings and pack settings pages no longer scroll sideways on a
  phone: their grid's gutter is 1rem below the lg breakpoint (it was
  1.5rem, 4px wider than the page's 8px mobile padding).
  `TestRowGuttersFitPhones` keeps every admin template's rows within it.

## v0.1.61 (2026-10-04)

- External accounts (issue #23): a signed-in user connects an account of
  another service so the server calls its API as that user, apart from
  sign-in. `AUTH_CONNECT=github` with `AUTH_CONNECT_GITHUB_CLIENT_ID`
  and `_CLIENT_SECRET` (`_SCOPES`; by default `repo admin:repo_hook`,
  repositories with private ones and their webhooks), or
  `auth.Options.Connectors` with `auth.GitHubConnect` and
  `auth.OAuth2Connect` for any OAuth 2.0 provider. `auth.Mount` then
  serves `GET /api/v1/auth/connect/{provider}/start` and `/callback`
  (signed-in users only; the state a one-time token bound to the user,
  a cookie bound to the browser, ten minutes, PKCE), `GET
  /api/v1/auth/connections` (no token in it) and `DELETE
  /api/v1/auth/connections/{provider}` (revoked at the provider, then
  forgotten). A grant missing a requested scope is refused and
  withdrawn. The server calls the API with `auth.From(ctx).Connection(ctx,
  owner, "github")`: `Client()` sends the token and renews an expiring
  one (8 renewals at once per node, 15 seconds each, concurrent callers
  sharing one); a refused renewal is `auth.ErrReconnect` and the
  connection lists with `reconnect`. Grants are kept in `auth_connection`
  (model `AuthConnection`), the tokens sealed with the master key and
  scoped to their owner; `auth.Grant` prints, logs and marshals without
  them, and `auth.ConnectionStore` lets an app keep them its own way.
  `Options.ConnectAuthorize` may refuse a user; `OnEvent` reports
  `connected`, `connect_failed` and `disconnected`. Sign-in is
  unchanged. Existing apps: `lidza update` adds the model to
  schema.lidza (its `lidza gen`), then `lidza db migrate`. Recipe "Connect an external
  account".

## v0.1.60 (2026-10-03)

- Admin pages: `Options.OnAudit` reports every admin action for a staff
  audit log: each form sent on any page, the pack's own (admins, users,
  settings, credentials, jobs) and the app's, with its message or
  error, and each download with its status. The form comes with secret
  values redacted (secret settings, every credential, fields named like
  a password, secret, token, key or code) and long values cut; files
  by name and size.

## v0.1.59 (2026-10-03)

- Auth: `Options.OnEvent` reports each account event the routes handle,
  for an app's security log: sign-ins and failed sign-ins (with the
  reason, and the account when the address has one), sign-outs,
  sign-ups, password changes, wrong current passwords, reset requests,
  resets, verifications and deletions, each with the request. It cannot
  refuse anything; a panic in it is logged.

## v0.1.58 (2026-10-03)

- Recipes are no longer written as Gemini CLI commands
  (`.gemini/commands/lidza/<name>.toml`, `/lidza:<name>`). Gemini CLI
  reads the skills in `.agents/skills`, as Codex does, and offers each
  as `/<name>`, so every recipe showed twice there. `lidza gen` removes
  the commands it generated (a command the developer wrote stays) and
  the directories when empty; commit the deletions.

## v0.1.57 (2026-10-03)

- One agent instructions file: `AGENTS.md` holds them, and `CLAUDE.md`
  and `GEMINI.md` are the one line `@AGENTS.md`, an import Claude Code
  and Gemini CLI expand; Codex, which has no imports, reads `AGENTS.md`
  itself. The brief's working agreements, `lidza note` and the recipe
  list write `AGENTS.md` only. `lidza gen` and `lidza update` turn an
  app's three identical copies into the file and two stubs; a
  `CLAUDE.md` or `GEMINI.md` that differs is kept, still updated, and
  named with what to do: merge what it adds into `AGENTS.md`, then make
  it the one line `@AGENTS.md`.

## v0.1.56 (2026-10-03)

- Auth: `POST /api/v1/auth/verify/resend` emails the signed-in user a new
  verification link while the address is unverified (a verified address,
  or `NoVerifyEmail`, gets 204 and no mail). The guide's "Add sign-in"
  recipe says how a page offers it.

## v0.1.55 (2026-10-03)

- Admin pages: a form posts the frame's `.Back` as `back` to return to
  the view it was sent from (the same page and query, never another
  address), with the action's message. `Page.MaxUpload` lets a page's
  forms send files (multipart, limited to that many bytes; other pages
  refuse them), and `Page.Downloads` serves GET handlers at
  `<Path>/<name>` behind the admin gate, for exports and attachments.
  The guide's "Extend the admin pages" recipe says how.

## v0.1.54 (2026-10-02)

- Breaking: mail refuses custom `Message-ID` and `Date` headers; the pack
  sets both, and a custom Message-ID made the stored provider id wrong
  for bounce and reply correlation. Remove them from `Message.Headers`.
- Mail no longer sends a message twice: the queued job skips a row
  already marked sent (a stale claim taken over, a retry after the
  provider accepted it), and SMTP counts a message as sent once the
  server accepts DATA, whatever QUIT then does. `Deliver` called
  directly still resends.
- The CLI version guard treats a CLI built from source (a Go
  pseudo-version) as development, as the v0.1.47 notes said, so it no
  longer refuses `lidza gen`, `check`, `test` and `verify` against an
  app pinned to a release.

## v0.1.53 (2026-10-01)

- Mail supports bounded Reply-To address lists and inline byte attachments with
  independent filenames and content IDs. Keep MIME related resources inside
  the HTML alternative; preserve IDs, reply lists and headers in the outbox
  and retries. Mailgun uses its MIME endpoint for inline resources; SendGrid,
  Postmark and Resend use their documented attachment fields.
- Reject inline resources without HTML, invalid/duplicate content IDs and
  ambiguous custom header names differing only by case. Existing single
  Reply-To and ordinary attachment messages retain their wire formats.
- Cover all providers and SMTP with offline transport/MIME tests and real
  database-backed queued retry tests. Update the email recipe for all agents.

## v0.1.52 (2026-10-01)

- Encode empty attachment data as an empty base64 string instead of JSON
  null, matching the HTTP providers' content field contracts. Cover
  SendGrid, Postmark and Resend with local request regressions.

## v0.1.51 (2026-10-01)

- Breaking: reject duplicate recipients, control characters and reserved
  address/subject/MIME custom headers. Use `To`, `Cc`, `Bcc`, `From`,
  `ReplyTo`, `Subject` and attachment fields instead of overriding headers.
- Mail accepts a To address list, Cc/Bcc and byte attachments across
  Mailgun, SendGrid, Postmark, Resend and SMTP; queued messages and retries
  preserve them. Existing outbox rows with null new columns still deliver.
  `lidza update --migrate` adds the nullable outbox columns.
- Bound recipient counts and attachment count/total bytes with configurable
  defaults of 50 recipients, 10 files and 10 MiB. Validate filenames and
  content types before storing or contacting a provider.
- SMTP sends multipart attachments with hidden Bcc recipients, UTF-8 subjects
  and random message IDs. The whole exchange respects cancellation and
  `MAIL_SMTP_TIMEOUT` (30 seconds by default), including TLS and stalled peers.
- Record invalid outbox retries as failed attempts for operator visibility.
  Update the mail API and all three agents' generated email recipes.

## v0.1.50 (2026-10-01)

- Create the framework CI database when Postgres starts. Database-backed
  tests now run against the configured database; the transactional mail
  regression fails explicitly when a configured database is unavailable.

## v0.1.49 (2026-10-01)

- Mail `SendTx` stores the outbox row and delivery job in the caller's
  database transaction, so application writes and queued mail commit or
  roll back together. It requires the db and jobs packs.
- Queued `Send` now commits its outbox row and job atomically; a queue
  failure no longer leaves an orphaned outbox row. Synchronous delivery
  without jobs and direct delivery without db keep their existing behavior.
- Document transaction ownership and delivery retries in the mail API and
  all three agents' generated Send an email recipes.

## v0.1.48 (2026-10-01)

- Document application package placement under `internal/`, keeping models,
  handlers and generated contracts at their existing paths. Ship the Organize
  application packages recipe as MCP guidance and all three agents' recipes.
- API discovery lists and renders an app's own internal packages through CLI
  and MCP, while keeping framework internals outside its app-facing listing.
- Keep the framework's Claude Code, Codex and Gemini instructions identical;
  generated and refreshed apps receive the shared-guidance rule together.

## v0.1.47 (2026-10-01)

- Admin sidebars cover the full page while their navigation stays within the
  viewport. Long menus reveal the active page without moving page content;
  expanded mobile menus scroll within the available screen height.

- Generation refuses a released CLI that differs from the app's framework
  module before rewriting pack schemas, and shows how to install the
  matching CLI. Explicit local replacements and development builds remain
  supported. This prevents an old CLI from silently removing newer pack
  fields when running gen, setup, test, check or verify.

## v0.1.46 (2026-10-01)

- Refresh the reference app's generated mail recipe skills to match the guide;
  reference-app verification no longer reports unstaged generated recipes.

## v0.1.45 (2026-10-01)

- Mail outbox delivery preserves each message's sender, Reply-To and custom
  headers, including background delivery and retries. Previously the outbox
  dropped them. `lidza update --migrate` adds nullable fields to the mail
  schema; old queued rows use the configured sender.

## v0.1.44 (2026-09-30)

- `lidza.json` `"appDir"`: the app's package (`routes.go`, `start.go`,
  `tools.go`, the generated `packs.go`) can live in a directory, `app/`,
  instead of the root's package main, so it can be imported and the
  app's tests can live in `tests/`. `lidza gen` writes `packs.go` there
  (in that package), `lidza gen resource` registers in its `routes.go`,
  `lidza mcp` reads its `tools.go` and `lidza verify` checks its
  `packs.go`. Unset, nothing changes. The guide's Layout section has the
  steps to move an app.

## v0.1.43 (2026-09-29)

- `App.TLSHosts`: approve hostnames beyond `LIDZA_TLS_DOMAINS` while the
  app runs (a customer's own domain), `func(ctx, host) error`, nil
  approves, with the services in `ctx`. One policy decides in the TLS
  handshake, the ACME challenges (HTTP-01 on :80 and TLS-ALPN, whose
  challenge certificates autocert served without its policy) and before
  every certificate order: the ACME client's transport refuses an order
  naming a host the policy refuses, so a host the app stops approving is
  not renewed (autocert renews without asking its policy again). The
  verdict is cached per node, an approval 5 minutes and a refusal 1
  minute, at most 10,000 hosts. Approvals and refusals are logged
  (refusals at most 20 a minute); the admin overview gets a
  Certificates card for the node: the domains, the approved hosts with
  their expiry, the latest refusals (`lidza.TLSReporter`).

## v0.1.42 (2026-09-29)

- `lidza gen resource` and rule L018: a field named `userId` makes a
  model owned only with `@ref(User)` or `@ref(AuthUser)`; a bare
  `userId` (a membership table's) no longer does. `ownerId` still does.
- A new model attribute `@shared` (`lidza gen resource --shared`, MCP
  `shared`): rows every signed-in user shares, such as a team's
  projects or a membership table. Routes behind sign-in, queries not
  scoped, L018 silent, even with an owner-like field. `@public` stays
  "open to visitors"; the two exclude each other.

## v0.1.41 (2026-09-29)

- Breaking: `lidza verify`, and so the pre-commit hook, refuses a
  commit whose staged changes weaken the tests: a deleted test file
  (`*_test.go`, `*.spec.ts`, `*.test.ts(x)`), an added `t.Skip`,
  `t.Skipf`, `t.SkipNow`, `test.skip`, `it.skip`, `describe.skip`,
  `.only` or `.fixme`, or a test file that loses more assertions
  (`t.Error`, `t.Fatal`, `expect(`, `assert.`) than it gains. The new
  step "tests not weakened" compares the index with `HEAD` (skipped on
  the first commit) and names each file and line. Confirm a deliberate
  change with `LIDZA_ALLOW_TEST_CHANGES=1 git commit ...` or
  `lidza verify --allow-test-changes`; one skip with a stated reason
  passes with a `// lidza:allow-skip <reason>` comment on its line or
  the line above. The agent files and the guide say an agent never
  weakens a test to make it pass, and tells the developer when a test
  is wrong.
- Breaking: `lidza db migrate` and `lidza db rollback` refuse a
  `DATABASE_URL` on another host (not a Unix socket, `localhost` or a
  loopback address) unless `--production` is given; a deploy step that
  migrates a remote database adds the flag. `lidza db status`, `lidza
  test` and `DB_MIGRATE=true` are unchanged. The MCP tools never pass
  the flag. The command tools now carry MCP annotations:
  `lidza_db_migrate` and `lidza_db_rollback` are destructive (a client
  asks first), `lidza_db_status` and `lidza_doctor` read-only, the others
  additive; before, every tool had mcp-go's default, destructive.
- Breaking: `realtime.Handler()` without `realtime.Authorize` now refuses
  every topic (the connection opens, nothing is subscribed), and so does
  mounting the `Hub` itself. Pass `realtime.Authorize(fn)` deciding per
  topic, or `realtime.Authorize(realtime.AllowAll)` for the old
  behaviour. Publishing from the server is unchanged.
- Breaking: an app that sets no `App.CSP` now sends a strict
  Content-Security-Policy outside dev mode, `middleware.DefaultCSP`:
  `default-src 'self'; script-src 'self'; style-src 'self'; img-src
  'self' data: blob:; font-src 'self'; connect-src 'self'; object-src
  'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'`.
  `lidza dev` still sends none. Pages served from the build get the
  hashes of their own inline scripts and styles (the router's hydration
  payload) added to it; JSON-LD from `Head` needs none. An app that
  loads scripts, images, fonts or API calls from another origin extends
  it: `CSP: middleware.AddCSP(middleware.DefaultCSP, "script-src",
  "https://js.example.com")`, one call per directive (appending
  `"; script-src ..."` does not work: browsers keep the first). Inline
  `<script>` or `style=` in hand-written HTML moves to a file.
  `middleware.NoCSP` sends no policy.
- New apps' CI runs govulncheck and, with a frontend,
  `npm audit --omit=dev --audit-level=high`; new apps get
  `.github/dependabot.yml` (Go modules, npm with a frontend, the
  workflow's actions; weekly, minor and patch grouped; the framework
  left to `lidza update`). An existing app can copy both from a new one
  (`lidza new demo` in a scratch directory).
- Verified inbound webhooks: `pkg/webhook`. `webhook.Stripe("NAME",
  h)`, `webhook.Mailgun("NAME", h)` (JSON events and form posts),
  `webhook.HMAC("NAME", header, h)` (an HMAC-SHA256 of the body, hex
  or base64, after a `Prefix` such as `sha256=`), `webhook.Token` and
  `webhook.TokenField` (a shared token in a header or a JSON field)
  are http.Handlers for a route. The signature is checked on the raw
  body in constant time before anything decodes it; a signed timestamp
  more than 5 minutes off is refused; the body is capped at 1 MB and
  the handler has 10 seconds. The secret is read by name through the
  settings and credentials (`dev.NAME` / `production.NAME`); without
  it every delivery is refused with 503 and a log line naming it. Each
  delivery id (event id, token, `IDHeader`, or an `ID` func) is
  handled once: a repeat replies 200 without the handler, a failed
  handler releases the id for the retry. Ids live in the
  `webhook_delivery` table when the db pack runs (created on first
  use, 30 days, cleaned in bounded batches); dev and test fall back to
  a bounded map, production without the db pack refuses unless
  `webhook.WithStore` is given. `StripeSignature`, `MailgunSignature`
  and `HMACSignature` sign deliveries for tests. New recipe "Receive a
  webhook"; `lidza api` lists the package.
- The htmx template's time zone script is a file
  (`static/timezone.js`) and htmx's indicator styles live in
  `static/app.css`, so its pages hold under the default policy; the
  astro template writes styles and scripts as files instead of inlining
  them into the pages.
- install.sh in containers (cloud agents, CI images): with Go already
  installed, `lidza` landed in Go's bin directory, which nothing put on
  PATH, and the installer failed right after installing it; that
  directory is now on PATH and in `~/.lidza/env` whoever installed Go.
  `go install` runs with `GOTOOLCHAIN=auto`, so an image's older Go
  fetches the one the framework needs. `--services` refreshes apt's
  package lists first; a fresh container has none, and Valkey was not
  found. Tested as root in a container without systemd: Postgres and
  Valkey installed and started.
- Rule L010 reads the frontend source too (`src/`, generated files,
  `node_modules` and `dist` skipped): a key there ships to every
  browser. New shapes: Stripe secret and restricted keys (live and
  test; publishable `pk_` keys are public and pass), Stripe webhook
  secrets, JSON Web Tokens, and Postgres, MySQL and Redis URLs with a
  password in them (placeholders such as `user:password@` and
  `%s`/`${...}` interpolations pass).
- Rule L016: a call whose error is dropped, as a statement or assigned
  to `_`, type-checked. Deferred calls, a `Close` before a `return`,
  `fmt.Print*` and `fmt.Fprint*` to the terminal, and writes to a
  `bytes.Buffer`, a `strings.Builder` or a hash are exempt. A warning:
  `--strict` (CI, `lidza verify`) fails on it. The reference app logs a
  download cut short instead of dropping `io.Copy`'s error, and the
  `htmx` template checks `fs.Sub`'s.
- Rule L017: a run of at least 8 statements and 80 tokens that repeats
  another in the app, the function's own names and every literal
  aside (field, method and function names count, so resources of the
  same shape over different queries are not copies). Reported once at
  the copy, pointing at the first; `// lidza:ignore L017` above a
  function exempts it.
- `lidza gen resource` writes signed-in, owned resources by default.
  The routes are registered on a group behind `auth.Require()`; without
  the auth pack the generator says to add it or pass `--public`. A
  model with an owner field (the first of `ownerId`, `userId`, a field
  with `@ref(User)` or `@ref(AuthUser)`; `uuid` or `string`, required)
  is scoped to the signed-in user: list, count, get, update and delete
  filter by the owner column, create sets it from
  `auth.CurrentUser(ctx).ID`, the `Create` and `Update` types leave it
  out, and another user's row is a 404. `--public` (MCP: `public`)
  marks the model with the new schema attribute `@public`: routes open
  to visitors, rows not scoped. Files generated earlier are untouched;
  `--force` regenerates them this way, moves a `handlers.<Model>Routes(r)`
  line behind `auth.Require()` and names Create or Update types that
  still take the owner.
- `lidza gen resource` wrote handlers that did not compile for a model
  whose `@table` differs from its name (the row type is named after the
  table), for one with a single writable field (sqlc takes the value,
  not a Params struct), and for one with none (an UPDATE with nothing to
  set). The row type follows the table, a single field is passed as is,
  and a model with nothing to change gets no update route and inserts
  DEFAULT VALUES.
- Rule L018 (warning): a query in `db/queries/*.sql` on an owned table
  that neither filters by the owner column in its WHERE nor sets it in
  an INSERT. A query meant to cross users (an admin page) takes
  `-- lidza:ignore L018` on the line before its `-- name:`.

## v0.1.40 (2026-09-29)

- `lidza setup` in a clone of an app whose credentials are sealed (a
  teammate's checkout, CI, a cloud agent) created a new master key,
  which could not open the file: every `lidza credentials set` then
  failed with "wrong master key". No key is made when the sealed file
  exists or `LIDZA_MASTER_KEY` is set; setup says where the key goes
  and carries on, as the app and its tests run without it.

## v0.1.39 (2026-09-29)

- `lidza update` on an app without the db pack checked its migrations
  and reported "DATABASE_URL is required"; it skips them now. `lidza
  setup` said it wrote DATABASE_URL, AUTH_SECRET and MAIL_FROM into
  `.env` whatever the packs; it names only the values it wrote.

## v0.1.38 (2026-09-29)

- Credentials per mode: one sealed file holds values for every mode.
  `lidza credentials set dev.STRIPE_SECRET_KEY=sk_test_...
  production.STRIPE_SECRET_KEY=sk_live_...` writes them to the file's
  `dev:` and `production:` sections; a mode reads the plain values and
  its own section, which wins. Development keeps sandbox keys and
  production the live ones, and a deploy needs only `LIDZA_MASTER_KEY`.
  `list` shows the names as written; `show NAME` what the mode reads.
- The CLI reads the app's settings as `dev` when `LIDZA_MODE` is unset
  (`.env.dev`, the credentials' `dev:` section), so `lidza setup`,
  `db`, `doctor` and the MCP server's database tools never pick up a
  production value; `lidza ship` reads production's. `lidza setup`
  migrated the development database with no mode, which read
  `.env.production`; it now uses `dev`.

## v0.1.37 (2026-09-29)

- admin: settings a pack refuses are no longer kept. The Settings page
  said "saved, but a pack refused the new settings" and left the values
  in place, so the next start failed with the admin pages out of reach;
  the previous values are now put back and the page says "not saved".
- `lidza credentials list`, `show` and `unset` include the settings saved
  from the admin pages, which the db pack keeps in the app's database
  over the file: `unset NAME` clears a saved setting that stops the app
  from starting. A pack that fails to start names the saved settings in
  its error.

## v0.1.36 (2026-09-29)

- Templates: TypeScript 6.0, the newest that `typescript-eslint` supports.
  The tsconfigs drop `baseUrl`, which TypeScript 6 deprecates and 7
  removes; `paths` resolve from the tsconfig without it. Existing apps:
  delete the `"baseUrl": "."` line when moving to TypeScript 6.
- storage: `STORAGE_PREFIX` must be a plain folder name; one with `.` or
  `..` segments is refused at start instead of letting the local
  provider write outside `STORAGE_DIR`. The local provider also refuses
  any key outside its directory, whatever the caller.
- The paging helper `PageParams` (`handlers/convert.go`, written once by
  `lidza gen resource`) parsed `limit` and `offset` as `int` and
  converted them to `int32`: `?offset=2147483648` wrapped negative and
  the query failed with a 500. It now reads them as 32-bit numbers and
  ignores larger ones. Existing apps: copy the new `PageParams` from a
  fresh `lidza gen resource`, or change `strconv.Atoi` to
  `strconv.ParseInt(..., 10, 32)` as the template does.
- router: a cookie set with `Request.SetCookie` on a request that came
  over HTTPS is sent `Secure`. auth: the OAuth state cookie is cleared
  with the same `Secure` and `SameSite` it was set with.
- Templates: the analytics session id comes from
  `crypto.getRandomValues` instead of `Math.random`.
- CI runs with a read-only `GITHUB_TOKEN`.
- The official media pack pins its crates with a `Cargo.lock`, as geo does.

## v0.1.35 (2026-09-29)

- Templates: ESLint 10 in the `react` and `astro` templates. The a11y rules
  come from `eslint-plugin-jsx-a11y-x`, the maintained fork of
  `eslint-plugin-jsx-a11y` (which stops at ESLint 9), registered under the
  original name: rule ids stay `jsx-a11y/*`. `astro` moves to
  `eslint-plugin-astro` 3. Existing apps: swap the dependency and the
  import in `eslint.config.js` as the templates do.

## v0.1.34 (2026-09-29)

- `App.Head` (`devserver.WithHead`) sets each page's head per request
  without the SSR sidecar: title, description, canonical, Open Graph and
  Twitter tags, JSON-LD, noindex and the status, for the shell,
  prerendered, sidecar and dev pages, every value escaped. New recipe
  "Set the head of a page".
- The Permissions-Policy is set per app with `App.PermissionsPolicy`;
  `middleware.AllowGeolocation` lets the app's own pages ask for
  location. The default still turns camera, microphone and location off.
- cache: `Incr(ctx, key, by, ttl)` adds to a counter atomically (Valkey,
  Redis or memory) for rate limits and quotas shared across nodes.
- admin: `theme.css` and `layout.html` come from `Options.Templates`
  first, then `admin/` on disk, so an app that embeds `admin/` keeps its
  theme when the binary runs alone; the recipe embeds the folder.
- media: `fit: "cover"` fills the requested size and crops around the
  centre. HEIC, AVIF and TIFF fail with the code `unsupported_format`
  (keep the original). An app created before copies the pack's new
  source to use cover.
- packs: a Rust capability can return `abi::Error::code("kind", "msg")`;
  Go reads the code with `engine.ErrorCode(err)`.
- schema: `decimal(p, s)` fields are Postgres `numeric(p,s)` and
  `decimal.Decimal` in Go (`pkg/decimal`: string-backed, JSON as a
  string, no float rounding, exact `Cmp`, `Rat`/`FromRat` for
  arithmetic); a string in TypeScript, Dart and Rust. `Validate` and
  `validators.ts` check the value fits the column and apply `@min`/`@max`
  exactly. `lidza gen` maps sqlc's numeric to the same type in
  `sqlc.yaml` while a model has a decimal field.
- schema: `@default(autoincrement())` on an `int` or `bigint` is an
  identity column (`GENERATED BY DEFAULT AS IDENTITY`); `lidza gen
  resource` leaves it out of inserts, `@ref` to the model takes the id's
  type, and migrations add, drop or convert identities, moving the
  sequence past existing values.
- Breaking: `Router.Group` registers its routes on the root ServeMux
  with their full patterns and wraps the group's middleware around each
  route, so the most specific pattern wins across groups: a group's
  `GET /api/v1/things/prefill` is no longer answered by the parent's
  `GET /api/v1/things/{slug}`. What changes for an app: a path under a
  group's prefix that matches no route is a 404 (405 with `Allow`)
  without the group's middleware (it was a 401 behind `auth.Require()`),
  and a group pattern outside its prefix, or one that conflicts with
  another route, panics at start instead of being silently shadowed.
- `Router.Group` prefixes take wildcards: `r.Group("/api/v1/posts/{id}")`,
  with `req.Param("id")` in its handlers; groups nest.
- Typed uploads: `router.Route` with In = `router.File` passes the raw
  body to the handler (`Body`, `ContentType`, `Name`, `Size`), bounded by
  `router.UploadLimit(n)` (32 MB by default), 413 over it. The TypeScript
  client gets `api.uploadImage({ id }, file, { onProgress })`, the Dart
  client `uploadImage(bytes, id:, contentType:, filename:)`; OpenAPI
  documents the binary body. The "Store a file" recipe and the reference
  app use it. A handler error that is an `*http.MaxBytesError` replies
  413 instead of 500.
- `lidza check` L004 reads only real import and export statements,
  `import()` and `require()`: a JSX attribute such as
  `name="from" value={...}` is no longer reported as an undeclared
  package, and an import across several lines is read.
- auth: `OnSignIn` gets the request (`SignIn.Request`) and sets cookies
  on the reply (`s.SetCookie`), sent only when the sign-in succeeds.
- auth: "remember me": `remember: false` on `authLogin` or `authRegister`
  (or `?remember=false` on a provider's start) makes the auth cookies
  session cookies, on every renewal too; absent or `true` remembers as
  before. `LoginWith(ctx, id, claims, auth.SessionOptions{SessionOnly:
  true})` does the same for an app's own login. It needs
  `auth_session.remember`: run `lidza gen` and `lidza db migrate`; until
  then remembered sign-ins keep working.
- auth: `Options.NoVerifyEmail` stops registration from sending the
  "Verify your email" message; the verify route still redeems a link the
  app sends itself.
- auth: `auth.ThrottleSignIn()` gives the sign-in route its own limit,
  `AUTH_SIGNIN_RPS` and `AUTH_SIGNIN_BURST`, falling back to
  `AUTH_LOGIN_RPS` and `AUTH_LOGIN_BURST`; `auth.Mount`'s login uses it.
- jobs: `jobs.Unique(key)` makes an enqueue idempotent: while a job of
  the kind with that key is pending or running, on any node, Enqueue
  stores nothing and returns that job's id (`jobs.Existed(&found)` says
  so). The job table gets a `uniqueKey` column with a unique index on
  kind and key, cleared when the job finishes: run `lidza gen` and
  `lidza db migrate`; tables not yet migrated keep running jobs.
- jobs: `Handle(kind, fn, jobs.Concurrency(n))` caps how many jobs of a
  kind run at once across all nodes; jobs over the cap stay pending
  while workers take other kinds.
- jobs: `jobs.DailyAt(zone, "02:00", "10:00", "18:00")` runs at several
  clock times a day in a time zone, correct across daylight-saving
  changes.
- New apps get `appMiddleware()` in `start.go`, wired as
  `App.Middleware` in the generated `main.go`: middleware around the
  whole app (rate limits, a www redirect) without editing `main.go`. An
  older app adds `Middleware: appMiddleware(),` to `main.go` and the
  function to `start.go`. The scaffold writes its Go files through gofmt.
- `lidza.json`'s `deploy.env` holds the app's production settings that
  are not secrets (`MAIL_PROVIDER`, `STORAGE_BUCKET`): `lidza ship` writes
  them into `deploy/production.env` on every run (it rewrites the file)
  and counts them as set; a name that looks like a secret there is
  refused.
- `lidza test --e2e` loads `e2e/seed.sql` into the test database before
  the app starts, when the app has one: rows the browser suite needs that
  no page creates. It runs on every run, so it is written to be rerun.
- `lidza dev`: the framework's agent files are always at
  `/_lidza/llms.txt`, `/_lidza/llms-full.txt` and `/_lidza/openapi.json`,
  and at the root only until the app serves its own there: a product's
  `/llms.txt` for crawlers wins.
- `lidza mcp`: once the CLI on disk changes (`lidza update`), every tool
  of the running server refuses with the reconnect instruction instead
  of answering from the old release and regenerating files with its
  templates, which the new CLI then rewrote.
- Tests never read the sealed credentials file (`LIDZA_MODE=test`): it
  holds the deployment's real keys, which overrode `.env.test` and
  reached real services from tests. Values saved at runtime still count;
  a test that needs a key sets it with `t.Setenv` or in `.env.test`.

## v0.1.33 (2026-09-28)

- storage: `STORAGE_PREFIX` puts every key under a folder, for a bucket
  several apps share. The app's keys never carry it: `Put`, `Get`,
  `Stat`, `Delete`, `List`, the presigned URLs and `URL` add it, and the
  objects returned have it removed. The admin Storage settings have it
  as "Folder".

## v0.1.32 (2026-09-28)

- auth: `Options.OnSignIn(ctx, auth.SignIn{Profile, Method})` runs for
  every sign-in (a password, a provider, and the one after a sign-up),
  before the session opens, for work tied to the user arriving
  (claiming invitations sent to their address); an error refuses the
  sign-in (`?error=signin` for a provider). Refreshes do not run it.

## v0.1.31 (2026-09-27)

- `lidza credentials show` without a name prints every value, decrypted;
  `show NAME` still prints one.

## v0.1.30 (2026-09-27)

- llm: embeddings can come from a provider other than the chat one:
  `EMBED_PROVIDER` (openai, google, ollama, compatible, fake, or `none`
  to turn them off), `EMBED_MODEL`, `EMBED_API_KEY` (a secret) and
  `EMBED_BASE_URL`. Unset, Embed uses the chat provider as before. With
  an Anthropic chat and no `EMBED_PROVIDER`, Embed returns an error
  naming `EMBED_PROVIDER`. `LLM_EMBED_MODEL` is still read when
  `EMBED_MODEL` is empty. Errors when nothing can embed wrap
  `llm.ErrNoEmbeddings`.
- llm: `Embeddings(ctx, llm.EmbedRequest{Texts, Label})` returns vectors,
  model and tokens and records the call in `llm_usage` under its label,
  so usage per feature covers embeddings. `Embed(ctx, texts)` is
  unchanged and records the call unlabelled.
- admin: the Language model Settings tab has an Embeddings form (same as
  chat, off, OpenAI, Google Gemini, Ollama, custom server; model, key,
  address), applied without a restart.
- `lidza ship` flags `EMBED_PROVIDER=fake`; `pack.Setting.Optional`
  marks a production setting that may be unset.
- `.env.test` gets the test providers of every enabled pack, from setup
  and, for older apps, `lidza update`: `MAIL_PROVIDER=outbox`,
  `LLM_PROVIDER=fake`, `EMBED_PROVIDER=fake`, `STORAGE_PROVIDER=local`,
  `CACHE_URL=memory`. A provider set in `.env` (which tests read too)
  no longer reaches the tests.

## v0.1.29 (2026-09-27)

- sqlc names: a query's named parameters (`sqlc.arg`, `sqlc.narg`,
  `sqlc.slice`, `@name`) get the same renames as columns, so
  `sqlc.narg('ids')` is `IDs`, not `Ids`.
- `router.ErrorCode(status, code, ...)`: an error with a code beside the
  message (`{"error", "code"}`), read as `err.code` on the TypeScript
  `ApiError` and the Dart `ApiException`. The auth pack's 403s carry
  one: `wrong_password`, `reauthenticate` (the delete route),
  `email_not_verified`, `account_disabled`.
- `lidza dev` records itself in `.lidza/dev.pid`; `lidza update` names a
  dev server still running the old CLI. A dev server whose CLI was
  replaced on disk says so and stops generating, instead of rewriting
  generated files with the old release's names.
- The framework's TypeScript stream test compiles with TypeScript 7
  (v0.1.28's CI failed on it; the released code was not affected).

## v0.1.28 (2026-09-27)

- Admin: `ADMIN_USERS` counts every layer: the environment, the sealed
  credentials file deployed with the app (`lidza admin add`) and the
  list the Users page saved in the database. Before, a saved list hid
  the file's, so an admin added with `lidza admin add` and a deploy
  never arrived in production. Removing on the Users page someone
  another layer still lists says where to remove them.
- Translated pages from the first paint (react and svelte templates):
  with more than one `locales/<lang>.json`, `npm run build` prerenders
  every page once per locale (`dist/.locales/<lang>/`, the
  `I18N_DEFAULT` one also at the plain path). The binary serves the
  visitor's locale, negotiated as the i18n pack does (`?lang`, the
  `lang` cookie, `Accept-Language`), with `Vary: Accept-Language,
  Cookie`; `LIDZA_SSR=1` renders in the same locale. The page carries
  `<html lang>` and its catalog, so hydration matches and nothing
  flashes in the default language.
- New `src/i18n.ts` in the react and svelte templates: `t(key,
  ...args)`, `locale()`, `locales()`, `setLocale(lang)` (sets the
  `lang` cookie and reloads), over the same catalogs as the i18n pack.
  Apps created earlier copy it, `scripts/` and the i18n lines of
  `src/main.*` and `src/entry-server.*` from a new app.
- `lidza.LocaleNegotiator` (implemented by the i18n pack) and
  `devserver.WithLocale`; `devserver.Static` and `NewSidecar` take
  options.
- The SSR sidecar copies the whole `dist/.server` tree, so a server
  bundle split into chunks runs.

## v0.1.27 (2026-09-27)

- Breaking: Go names follow one rule, `schema.Initialisms` (id, url,
  api, http, json, uuid, sql, ip, html) in capitals and their plurals as
  `IDs`, `URLs`. `lidza gen` writes the list and the renames plurals
  need into `sqlc.yaml` (between `# lidza gen` comments; an app that
  sets `initialisms` or `rename` itself is told what to add) and lists
  every rename it causes; `lidza update` then runs `go build ./...`.
  Rename in app code: in `db/queries/gen` (models, query params and
  rows) `Url` to `URL`, `Html` to `HTML`, `Api` to `API`, `Http` to
  `HTTP`, `Json` to `JSON`, `Uuid` to `UUID`, `Sql` to `SQL`, `Ip` to
  `IP` inside any name (`ImageUrl` to `ImageURL`, `ApiKey` to `APIKey`
  for table `api_key`, enum constants such as `SourceApi` to
  `SourceAPI`), and `Ids` to `IDs`, `Urls` to `URLs` (`TagIds` to
  `TagIDs`); in `schema/` plurals only (`TagIds` to
  `TagIDs`, `KindUrls` to `KindURLs`).
- `router.Stream(r, pattern, h)`: a typed operation that replies with
  server-sent events; `send` flushes each event, and an error after the
  first reaches the client as an error event with its status. It is
  marked `stream` in `.lidza/context.json` and OpenAPI
  (`x-lidza-stream`); the TypeScript client returns an async iterable
  (`for await`; `options.signal` or leaving the loop closes it), the
  Dart client a `Stream`. A stream's deadline is `App.StreamTimeout`
  (10 minutes), not the 30 seconds of other requests. L005 checks the
  event type.
- The TypeScript client compiles under `noUnusedLocals` for an API
  without path parameters or schema rules; a test type-checks it.
- `schema.lidza`: `@ref(Model)` to a model whose id is `int`, `bigint`
  or `string`; the field takes the id's type, and another type is an
  error naming it. `@ref` on an array is an error. Tables are created
  after the ones they reference and dropped before them.
- The Dart client decodes an output that is not a named class (a list,
  a string) as that type.
- jobs: recurring schedules. `jobs.FromServices(s).Schedule(kind,
  jobs.Every(15*time.Minute) | jobs.Daily("06:30", zone) |
  jobs.Weekly(time.Monday, "09:00", "Europe/Tirane"), payload)` enqueues
  one job per due time however many nodes run, and after downtime one
  missed run. The `job_schedule` table comes from the jobs schema
  fragment: run `lidza gen` and `lidza db migrate`. The admin Jobs page
  lists the schedules with their next and last run.
- jobs: at shutdown, running jobs get `JOBS_DRAIN` (default 1s) to
  finish, then are cancelled and released back to pending with the
  attempt uncounted, so a restart (`lidza dev` rebuilds included) runs
  them at once instead of after `JOBS_STALE`. A job's result is written
  only while its claim holds.
- auth: `Options.OnSignUp(ctx, tx, auth.SignUp)` runs once per account
  the sign-in creates (a registration, or a provider's first sign-in
  that makes a new account), inside the transaction that inserts it,
  with the method, the provider's identity and the request's language;
  an error rolls the account back. Registration and provider sign-up now
  create the account in one transaction.
- auth: account deletion. `auth.From(ctx).DeleteUser(ctx, id)` removes
  the user, identities, sessions, tokens and account row in one
  transaction with `Options.OnDeleteUser(ctx, tx, subject)` for the
  app's rows (`DeleteUserTx` inside the app's own transaction). Setting
  `OnDeleteUser` also serves `POST /api/v1/auth/delete`
  (`api.authDelete`): the password for an account with one, else a
  sign-in within ten minutes (`ErrReauthenticate`). The first account
  keeps its admin marker, so admin does not pass to the next user.
- mail: templates per language. `mail/<name>.<lang>.txt.tmpl` (and
  `.html.tmpl`) is chosen for `Message.Lang`, else the request's
  language (the i18n pack's locale, else `Accept-Language`), falling
  back to the base language and then `<name>`; a template's
  `{{define "subject"}}` sets the subject. The auth pack's verification
  and reset emails use `auth_verify.<lang>` and `auth_reset.<lang>`.
- `lidza admin add EMAIL... | remove | list` edits `ADMIN_USERS` in the
  credentials, read within seconds. In development the "not an admin"
  page names the command: a test or screenshot script run against
  `lidza dev` may have signed up the first account, which is the
  automatic admin.
- `lidza dev` reads `lidza.json` before every build, so a pack added or
  scaffolded while it runs is generated into `packs.go` and watched;
  `lidza db migrate` and `rollback` restart the app it runs.
- `lidza mcp`: the app instance serving `app_*` tools runs no job
  workers (they ran old code against the dev database) and rebuilds
  itself when a Go source changed since its build.
- `lidza api` and MCP `lidza_api` without a package or filter list the
  packages instead of rendering every one (thousands of lines an agent
  then carries each turn); a filter alone searches them all; `all`
  renders everything.
- `lidza check` runs cargo check on the app's local pack crates (for
  wasm32-wasip1), not only a root `Cargo.toml`; `lidza pack build`
  regenerates `schema.rs` from `schema.lidza` first.
- `lidza ship` names the settings the enabled packs need in production
  that the credentials lack or hold with a development-only value
  (`CACHE_URL`, `APP_URL`, `LLM_PROVIDER=fake`), on screen and in
  `deploy/production.env`.
- `lidza test --e2e` raises the sign-in and analytics rate limits for the
  run (every account signs up from 127.0.0.1) unless `.env.test` sets
  them; Go tests keep the real limits.
- `.env.test` is tracked in new apps (it holds no secrets, and tests and
  CI need it); `lidza update` adds `!.env.test` to an older
  `.gitignore`. The guide explains the `.env` layers: `.env` applies to
  every mode, tests included, so development-only settings go in
  `.env.dev`.
- L005 no longer flags a function over `*http.Request` that is not a
  typed handler, such as an `http.RoundTripper`.
- Admin: a `multi` field renders as checkboxes in any section, not only
  as the section's selector; the `num` template function takes any
  number type.
- `lidza_brief` and `lidza brief` count an answer saved garbled before
  v0.1.26 (the pasted question, menu numbers) as open, so the agent asks
  again instead of building on it.
- realtime: a slow subscriber dropped on one topic is removed from all
  of its topics; before, a publish on another topic sent on its closed
  queue and panicked the node. The drop takes the hub's write lock.
- The repository no longer carries a 36 MB `lidza` binary committed by
  mistake in v0.1.23 to v0.1.25, which every app fetching the module
  downloaded; `scripts/release.sh` refuses tracked files over 2 MB.

## v0.1.26 (2026-09-26)

- `lidza brief`: several numbers combine suggestions on a free-text
  question too ("1,3" was saved as text); a pasted question header,
  question or explanation is removed from an answer, and an answer that
  was only the question is not saved (the interview asks again).

## v0.1.25 (2026-09-26)

- The brief can be skipped, whole or per question. In `lidza brief`,
  Enter still asks again later; `s` skips a question for good and `S`
  skips all the rest; `lidza brief skip [id...]` does it from a script
  (no ids: every open question), MCP `lidza_brief_skip` from an agent, and
  setup's offer takes "skip". A skipped question is shown as skipped, is
  never asked again and is not counted by L015; an answer given later
  replaces the skip. Agents offer "later", "skip" and "skip the rest"
  with every question.
- A new app's tests and CI pass on any machine: `.env.test` gets
  `CACHE_URL=memory` when the cache pack is enabled (setup writes it,
  `lidza gen` and `lidza update` add it to older apps), so tests need no
  Redis; and L011 (a pack no code uses yet) is a note, which a fresh app
  has for every pack and `lidza verify --strict` no longer fails on.

- `lidza update`, `lidza gen`, `lidza dev` and `lidza test` repair a
  `DATABASE_URL` written for another machine's Postgres socket (Linux's
  on a Mac), as `lidza setup` did; `lidza update` on a Mac no longer
  stops at "database not reachable" after an earlier failed setup.

## v0.1.24 (2026-09-26)

- The brief: every app has `docs/brief.md`, about thirty questions in
  eight areas (product, data, accounts, design, content, services,
  deployment, working agreements), each with suggested answers and room
  for the developer's own. `lidza brief` asks the open ones in a terminal
  (a number picks a suggestion, several for a "many" question, words are
  an answer of your own), and `lidza new` and `lidza setup` offer it
  before the first commit. An agent runs the same interview with the MCP
  tools `lidza_brief` and `lidza_brief_answer` (recipe "Start with the
  brief"), asking with its question tool and never inventing an answer.
- An answer lands where it acts: a decision for a real choice (sign-in,
  data ownership, palette, providers, hosting), the working agreements
  in `CLAUDE.md`, `AGENTS.md` and `GEMINI.md`, the chosen palette in
  `src/index.css` and `admin/theme.css` (four palettes of Līdza's own, or
  the app's brand colours), and a seeded app recipe for the ownership
  model ("Scope a query in this app"), the page style ("Style a page to
  match the app") or a dataset ("Import or seed data").
- Shared memory instead of an agent's local one: the agent files end with
  "Working agreements" and "Team notes"; `lidza note add` (MCP
  `lidza_note_add`) records a lasting fact there, in git for the team.
  The agent files' first line points every session at the brief.
- Rule L015, a note: the brief still has required questions open. A
  note is reported by `lidza check` but never fails `lidza verify
  --strict`. Existing apps get the brief and the two sections on
  `lidza update` or `lidza gen`.

## v0.1.23 (2026-09-26)

- `lidza setup`: a machine without a git author (a fresh CI runner, a new
  laptop) gets a note with the `git config` command instead of a failed
  setup; v0.1.22 counted the missing first commit as a problem and exited
  with an error there. A first commit that `lidza verify` refuses is
  still a problem.

## v0.1.22 (2026-09-26)

- macOS: `lidza setup` (and `lidza new --packs`) writes the address where
  this machine's Postgres actually listens: its Unix socket in
  /var/run/postgresql (Linux) or /tmp (Homebrew, Postgres.app), else TCP
  on 127.0.0.1:5432 (`db.LocalURL`). It was always the Linux socket, so
  setup failed on a Mac at the database step. Rerunning `lidza setup`
  repairs an `.env` and `.env.test` written that way (`db.RepairSocket`).
- `lidza setup` goes on past a failed step (the databases, npm install,
  the test browser, the agent CLI, the first commit), shows npm's own
  output when it fails, and lists what needs attention at the end; it
  used to stop at the first failure and hide npm's error, leaving the
  later steps silently undone.
- `lidza doctor` connects with the app's own `DATABASE_URL` (the port
  answering was not enough) and says how to fix it, and notes a Node major
  version other than the one the templates are tested on (22).
- Installer: says plainly at the end when Postgres and Valkey were not
  installed (piped without `--services`, it cannot ask) with the command
  that adds them; on macOS starts an installed Redis instead of failing on
  Homebrew's Valkey/Redis conflict; reports a failed service install
  instead of moving on silently.

## v0.1.21 (2026-09-26)

- A palette of Līdza's own instead of the stock blue: a mulberry accent
  on warm paper neutrals, and a warm charcoal dark mode, in the admin
  pages and the app templates (the React template's `brand` tokens; the
  Svelte, Astro and htmx templates' neutrals). Apps keep their own
  tokens; a new app starts from this one.
- Admin theme: `--admin-bg`, `--admin-surface`, `--admin-surface-2`,
  `--admin-line`, `--admin-fg`, `--admin-muted` and `--admin-accent-fg`
  apply again, per theme, mapped onto Tabler's variables. That undoes
  the part of v0.1.20's breaking note about them; a custom
  `admin/layout.html` still needs the two template lines.

## v0.1.20 (2026-09-26)

- Breaking: the admin pages are rebuilt on Tabler. An app's own
  `admin/layout.html` must now call `{{template "admin-head" .}}` in
  `<head>` and `{{template "admin-scripts" .}}` before `</body>`, or the
  pages render without styles. In `admin/theme.css`, `--admin-accent`,
  `--admin-font`, `--admin-mono` and `--admin-radius` still apply;
  `--admin-bg`, `--admin-surface`, `--admin-fg`, `--admin-muted` and
  `--admin-line` no longer do (the light and dark themes come from
  Tabler; set `--tblr-*` variables instead). A test that looked for the
  word "Credentials" on the Overview page needs another anchor. Apps
  without a custom layout change nothing.
- Admin pages: a sidebar with light, dark and system themes, a setup
  checklist on the Overview, avatars, status badges, row menus with
  confirmations, empty states, a token chart. The stylesheet, script
  and 59 icons are served from the binary, gzipped and cached by content
  hash, with no inline script or style, so a strict
  Content-Security-Policy holds.
- Admin settings live on each pack's page: Mail, Language model and
  Storage have a Settings tab and sign-in providers sit under Users. The
  provider is picked from named tiles and only its fields show, with
  placeholders per provider, links to where each provider issues keys,
  an Advanced fold, the origin of each value, and a lock on values the
  process environment sets. `/admin/credentials` redirects there.
- Admin extension points: `admin.Options.Pages` adds the app's own
  pages in the same frame (`admin.Page` with a template, a data loader
  and form actions), and `admin.Options.Sections` adds the app's own
  settings on a Settings page. Recipe "Extend the admin pages", snippets
  `admin-page` and `admin-page-template`, and rule L014 for an admin page
  no decision names.
- Mail: SMTP settings in parts (`MAIL_SMTP_HOST`, `MAIL_SMTP_PORT`,
  `MAIL_SMTP_USERNAME`, `MAIL_SMTP_PASSWORD`, `MAIL_SMTP_SECURITY` of
  starttls, tls or none; starttls now refuses a server that cannot
  upgrade), and `MAIL_REGION=eu` for Mailgun and SendGrid.
  `MAIL_SMTP_URL` keeps working and wins.
- Language model: provider `compatible` for any server speaking the
  OpenAI API (llama.cpp's llama-server, vLLM, LM Studio), with
  `LLM_BASE_URL` required and the key optional.
- Storage: providers `r2`, `spaces`, `b2`, `gcs` and `minio` fill in the
  address from `STORAGE_ACCOUNT_ID` or `STORAGE_REGION`; `s3` outside
  us-east-1 uses the regional address.
- `env.Origins` reports which layer each setting comes from; the jobs
  pack's `Counts` reports jobs by state.

## v0.1.19 (2026-09-26)

- Sign-in: `auth.Mount(r, auth.Options{})` serves registration, login,
  logout, session, email verification, password reset and change on the
  pack's own `auth_user` table, and sign-in providers from
  `AUTH_PROVIDERS`: Google, GitHub, Microsoft and any OIDC issuer with
  discovery (authorization code with PKCE, the nonce and the issuer's
  keys checked), identities linked to accounts in `auth_identity`, an
  identity with a vouched-for email joining the local account of that
  address. The generated client carries the routes (`api.authLogin`
  and the rest: the inspector reads a mounted pack's routes). The admin
  pages hold the provider credentials ("Sign-in providers") and the
  Users page shows how each account signs in. Rule L013 flags OAuth and
  OIDC client libraries. Recipe "Add sign-in".
- Compatibility: additive. An app with its own users table keeps calling
  `Login`, `Require` and `Optional` unchanged; the two new tables arrive
  as a migration with `lidza update --migrate` and stay empty until the
  app mounts the sign-in.
- Container image: the runtime stage copies `mail/`, `admin/` and the
  sealed credentials next to the binary, so templated mail, the admin
  theme and `config/credentials.yml.enc` work in a container;
  `.dockerignore` keeps `config/master.key` and `storage/` out.
- `lidza gen deploy [--force]` writes the Dockerfile, `.dockerignore` and
  the systemd unit from the current templates; `lidza update` runs it,
  so an app created by an earlier release gets the fixes (a file the app
  edited is kept and named).
- `lidza test --run Regexp` filters the Go tests; the MCP tool's `run`
  option works again.
- MCP: `lidza_ship` (`domains`, `email`, `no_e2e`).
- Generated client: `RequestOptions` takes `query` (a query string) and
  `body` with `contentType` (a raw body for an upload), so a list with
  filters and a file upload need no hand-written `fetch`.

## v0.1.18 (2026-09-26)

- Admin pages: the app's first account (the first user ever to sign in)
  is an admin; the Overview page adds and removes admins, saved sealed
  and applied at once; `ADMIN_USERS` still works. `auth.WithUser` and
  `auth.From(ctx).FirstSubject`.
- User management: the auth pack keeps every account that signed in
  (`auth_account`: subject, label, first and last seen, disabled);
  `Accounts`, `AccountOf`, `Disable` (ends the sessions, refuses sign-in
  and refresh), `Enable`, `Sessions`. The admin Users page lists and
  searches them with sign out everywhere, disable, enable, make and
  unmake admin. `lidza gen` writes the migration for existing apps.

## v0.1.17 (2026-09-26)

- `lidza update` reads the newest release from the repository's tags
  first; the module proxy's cached list lagged a fresh tag by up to half
  an hour. `go get` of a release the proxy has not indexed falls back to
  the repository.

## v0.1.16 (2026-09-26)

- `lidza mcp` refreshes its lists while it runs: the pack tools follow
  `lidza.json`, the prompts the guide, the app tools `tools.go`, at once
  after `lidza_pack_add` and `lidza_recipe_add` and within two seconds of
  any other change, with list-changed notifications to the client. A
  newer CLI binary is the one case that needs a reconnect: command
  results say so, and `lidza update` prints the hint.

## v0.1.15 (2026-09-26)

- `lidza pack add --why "..."` records the decision in
  `docs/decisions.md`; the MCP tool `lidza_pack_add` requires `why`.
  `lidza new --packs` records the packs chosen at creation.
- Rule L011: a pack enabled in `lidza.json` that no app code imports.
- `lidza new` writes a `README.md`: run, build with an agent, deploy.
- Rule L012: a pack or a direct dependency (one the framework does not
  bring itself) with no entry in `docs/decisions.md` naming it.
- `lidza verify --strict`: warnings fail the check step. `lidza new`
  writes `.github/workflows/ci.yml` running it and the browser suite
  against Postgres and Valkey, with the CLI at the version `go.mod`
  requires.

## v0.1.14 (2026-09-26)

- `lidza pack add` keeps a declaration `schema.lidza` already has (an
  add that stopped halfway, or a model `lidza gen` synced) and appends
  only what is missing, instead of refusing.

## v0.1.13 (2026-09-26)

- Rule L010: a string literal shaped like an API key or token (AWS,
  Anthropic, OpenAI, Google, SendGrid, Mailgun, Resend, Slack, GitHub, a
  private key) is flagged; secrets go in the credentials.

## v0.1.12 (2026-09-26)

- Rule L009: a file written to the local disk (`os.WriteFile`,
  `os.Create`, `os.OpenFile`, `os.MkdirAll`) is flagged; uploads and
  generated files go through the storage pack. The MCP tool
  `lidza_storage` lists and describes stored objects.

## v0.1.11 (2026-09-26)

- `lidza update [--to vX.Y.Z] [--cli-only] [--migrate]`: the CLI to the
  newest release and, in a project, the framework module to the same
  version (`go get`, `go mod tidy`, the Dockerfile pin, `lidza gen`), with
  a note when migrations wait. `lidza doctor` reports a CLI that differs
  from the project's module.

## v0.1.10 (2026-09-26)

- `lidza version` and the version an app shows (`/api/v1/health`, the
  admin header) read the framework's version from the app's dependency,
  not from the app's own version stamp.

## v0.1.9 (2026-09-25)

- Admin pages: `admin.Mount(r, admin.Options{})` serves `/admin` for the
  users `ADMIN_USERS` names: an overview of the packs, the credentials
  of the mail, language-model and storage providers (saved sealed with
  the master key, applied without a restart), token usage by day, model
  and label, the mail outbox, jobs with a retry, stored files. Themed by
  `admin/theme.css` (CSS variables) and `admin/layout.html`. The
  reference app mounts it; the recipe "Add the admin pages".
- Core: `router.Mount` serves a handler outside `/api` ahead of the
  frontend; `lidza.Optional`, `lidza.ServicesFrom`, `env.Values`; the
  jobs pack's `Recent` and `Retry`; `router.WriteError` for raw handlers.

## v0.1.8 (2026-09-25)

- `llm`: every call is recorded in `llm_usage` with the db pack
  (provider, model, `Request.Label`, tokens, time, status);
  `llm.From(ctx).Usage(ctx, since)` and `RecentCalls` report it, the MCP
  tool `lidza_llm_usage` too.
- Credentials: `config/credentials.yml.enc` sealed with AES-256-GCM under
  `config/master.key` (git-ignored) or `LIDZA_MASTER_KEY`; `lidza
  credentials init | set | unset | list | show | edit` and the MCP tools
  `lidza_credentials_set` and `lidza_credentials_list`. Every pack reads
  the sealed values by their environment names through `pkg/env`, between
  the `.env` files and the process environment. Values saved at runtime
  live in the `credential` table through the db pack, sealed with the
  same key, and `lidza.Reconfigure` lets the mail, llm and storage packs
  switch provider without a restart. `lidza setup` creates the key.
- `lidza ship --domains a.example.com --email ops@example.com` records
  the deployment in `lidza.json` (`deploy`) and writes
  `deploy/production.env` (`LIDZA_TLS_DOMAINS`, `LIDZA_TLS_EMAIL`,
  `LIDZA_LOG=json`, `DB_MIGRATE=true`) on every ship; the Dockerfile
  exposes 80 and 443; the report names the URL and the deploy commands.
- `storage` pack: `Put`, `Get`, `Stat`, `List`, `Delete`, `PresignGet`,
  `PresignPut`, `URL`, `Handler`; any S3-compatible service with
  Signature V4 spoken directly, and a `local` provider. Rule L008 against
  storage SDKs; the recipe "Store a file".

## v0.1.7 (2026-09-25)

- TLS without a proxy: `LIDZA_TLS_DOMAINS=app.example.com` makes the
  binary serve HTTPS on 443 with Let's Encrypt certificates
  (`golang.org/x/crypto/acme/autocert`), renewed on their own, and
  redirect 80. Certificates are stored in Postgres through the db pack
  (`tls_certificate`, shared by every node) or, for one node,
  `LIDZA_TLS_CACHE_DIR`. `APP_URL` and `AUTH_COOKIE_SECURE` follow from
  the domain unless set. `lidza doctor` checks the DNS record, the ports
  and the right to bind them; the systemd unit grants
  `CAP_NET_BIND_SERVICE`; `docs/deploy.md` has the section.

## v0.1.6 (2026-09-25)

- `llm` pack: language models behind one `Chat`, `Stream`, `Embed`,
  `Generate[T]` (structured output from a schema type, validated) and
  `Run` (the app's `lidza.Tool` values offered to the model, its calls
  run with the packs in the context). Anthropic, OpenAI, Google and
  Ollama spoken directly over HTTP; `fake` scripts replies in tests.
  Deadlines, retries on 429 and 5xx with Retry-After, token counts on
  `/metrics`, the MCP tool `lidza_llm`, rule L007 against vendor SDKs
  and client libraries, the recipe "Add an LLM feature", and the
  reference app's tag suggestion as the snippet `llm-handler`.

## v0.1.5 (2026-09-25)

- A per-app decision log, `docs/decisions.md`: why the app is built a
  way (a pack added, Rust for a module, a dependency, a schema tradeoff),
  written with `lidza decision add "Title" --why "..."` or the MCP tool
  `lidza_decision_add`, read as `lidza://decisions`. `lidza new` creates
  it, `lidza gen` adds it to an existing app with the line in its agent
  files; rule 12 and the pack recipe point at it.

## v0.1.4 (2026-09-25)

- A change to a foreign key's delete rule (`@ref(Model)` to `@ref(Model,
  cascade)` or back) gets a migration: the constraint is dropped and
  added again with the new rule.

## v0.1.3 (2026-09-25)

Lessons from the first app built by an agent on v0.1.2 (a team task
tracker): every gap it worked around by hand is closed here.

- `auth`: sessions slide. `Require` and `Optional` renew an expired
  access cookie from the refresh cookie on the request itself, claims
  carried over, and set both cookies again; a refresh token just
  replaced keeps working for a minute for requests in flight
  (`prevRefreshHash`, `rotatedAt` on `AuthSession`: `lidza gen` writes
  the migration). The refresh cookie's path is `/`; logout clears the
  old path too. No refresh route or client code is needed.
- `jobs`: a handler's context carries the packs (`db.From(ctx)`,
  `mail.From(ctx)`); `EnqueueTx` queues inside the caller's transaction.
- `realtime`: `Handler(realtime.Authorize(fn))` decides per topic who
  may subscribe.
- `mail`: `Link(path)` makes links absolute with `APP_URL`; `WaitFor`
  finds a message in the outbox for tests, waiting for one a job sends.
- `schema.lidza`: `@ref(Model, cascade)` and `@ref(Model, setnull)`.
- Apps own a start hook: `start.go` with `onStart`, wired in `main.go`
  by `lidza new`, where job handlers are registered.
- `lidzatest.Server.Context()`: a context with the app's services.
- `lidza test -v` and `lidza_test` `verbose`; `lidza setup` installs the
  browser for the e2e suite.
- Guide: recipes "Scope a query to the signed-in user" (ownership and
  membership), "Add a background job", "Publish live updates"; a
  reference of schema types and attributes; sessions, links, `WaitFor`,
  `exact: true` locators. The reference app follows all of it.
- `scripts/release.sh`, a test that the installer's pin is the newest
  release in this file, and a CI job on tags that installs the tag.

## v0.1.2 (2026-09-25)

- `lidza setup` and `lidza new --packs ... --agent ...`: packs, `.env` with
  a random `AUTH_SECRET`, `.env.test`, generation, databases created and
  migrated, `node_modules`, the agent CLI, the first commit.
- `lidza ship`: verify, the browser suite, the production build.
- `install.sh` installs the CLI release it was tested with
  (`LIDZA_VERSION`, overridable) instead of `@latest`, and replaces an
  older release on rerun.
- An app's `go.mod` and Dockerfile pin the framework version the CLI was
  built from, a pseudo-version for a build from an untagged commit, so
  the module matches the generated code.

## v0.1.1 (2026-09-25)

- `install.sh` installs the prerequisites (git, curl, C toolchain), Node
  22, and with `--services` Postgres and Valkey with a role for the user;
  `lidza doctor` names the fix per package manager.
- `lidza test` and `DB_MIGRATE` no longer fail on an app whose schema has
  no model yet (no `db/migrations` directory): nothing to apply.
- Every CLI command is an MCP tool (`lidza_gen`, `lidza_gen_resource`,
  `lidza_pack_*`, `lidza_db_*`, `lidza_test`, `lidza_verify`,
  `lidza_build`, `lidza_doctor`, `lidza_recipes`); `lidza_check` runs the
  CLI's check.
- App-scoped recipes: "App recipes" in the guide, `lidza recipe add`,
  `lidza_recipe_add`; the framework's recipes are refreshed by `lidza gen`,
  the app's are not; agent files list both.
- `mail` pack: transactional email behind one `Send` (Mailgun, SendGrid,
  Postmark, Resend, SMTP; `log` and `outbox` providers), templates in
  `mail/`, outbox table, delivery through the jobs pack, `lidza_mail`,
  rule L006 against vendor SDKs; the reference app uses it.

## v0.1.0 (2026-09-25)

The first release: everything in `docs/roadmap.md` phases 0 to 7 and what
followed.

- **CLI**: `new`, `dev`, `build`, `check`, `gen`, `gen resource`, `pack`,
  `db`, `test` (`--e2e`), `verify` (with the pre-commit hook), `benchmark`,
  `doctor`, `context`, `api`, `snippet`, `mcp`, `version`.
- **Contract**: `schema.lidza` generates Go structs with validation, SQL
  and migrations, Rust structs, JSON Schema, the TypeScript client
  `@lidza/client` with validators, the Dart client, OpenAPI 3.1.
- **Control plane**: typed routes (`router.Route`, groups, per-route
  middleware), the middleware pipeline, `/metrics`, `/healthz`, `/readyz`,
  structured logs with request ids, services registry, lifecycle hooks,
  `lidzatest`.
- **Packs**: official Go packs `db`, `auth` (sessions, throttling,
  password policy, one-time tokens, revocation), `jobs`, `cache`, `i18n`
  (time zones), `realtime`, `analytics`; Rust packs `geo` and `media`
  under wazero with memory caps and deadlines, `uninterruptible` for
  input-bounded loops; `lidza pack scaffold` for local packs.
- **Frontends**: react (TanStack Router and Query, Tailwind v4,
  prerendering with the hydration payload, per-request SSR sidecar),
  svelte (prerendered and hydrated), astro (static), htmx (Go templates);
  accessibility lint as errors, browser tests, time zone cookie, opt-in
  analytics reporter in each.
- **Agents**: `lidza check --json` across every layer with rules L001 to
  L005; `lidza mcp` with routes, context, diagnostics, logs, the
  framework's API, the reference app's snippets, pack capabilities and
  the app's own tools; the guide's recipes as MCP prompts and as skills
  for Claude Code and Codex and commands for Gemini CLI; `/llms.txt`;
  `.claude/skills`, `.agents/skills`, `.gemini/commands` written by
  `lidza new` and `lidza gen`.
- **Reference app** `examples/notes`; **evals** (`go test -tags evals`)
  and agent-driven evals (`-tags agenteval`); CI on GitHub Actions.
- **Deployment**: `Dockerfile`, `.dockerignore`, `deploy/<name>.service`
  from `lidza new`; `docs/deploy.md`.
