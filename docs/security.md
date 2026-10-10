# Security model

What Līdza defends against, where, and what is left to the app. Report a
vulnerability as `SECURITY.md` says; this page is for reviewers,
auditors and app authors.

## Trust boundaries

- **Untrusted:** everything in a request (path, query, headers, cookies,
  body, WebSocket frames), webhook deliveries before their signature is
  checked, the addresses a webhook subscriber's name resolves to, and
  uploaded files.
- **Trusted after a check:** a request with a verified session or
  bearer token (auth); a delivery whose signature verified (webhook); a
  forwarding header from a peer listed in `LIDZA_TRUSTED_PROXIES`.
- **Trusted:** the app's code and configuration, the database, Valkey,
  the master key and the sealed credentials file, and the WASM packs
  the app ships (they are contained, not trusted with the host; see
  below).

## Per area

### HTTP and the router

- Typed handlers read at most 1 MiB of JSON (`router.MaxBodyBytes`),
  refuse trailing data, and validate input before the handler runs. A
  500 never sends the error text to the client.
- Default headers: a strict `Content-Security-Policy` (`default-src
  'self'`, `frame-ancestors 'none'`, `object-src 'none'`), HSTS when
  `APP_URL` is https, `nosniff` and a referrer policy.
- The client address is the connection's, unless the peer is a trusted
  proxy; forwarding headers are bounded and read nearest first
  (`middleware.ClientIdentity`). Rate limits key on it.
- Static files: a missing asset is a 404, never the app shell; files
  are served from the embedded build only.

### Auth (`packs/auth`)

- Passwords: argon2id (64 MiB, 3 passes, 2 lanes). A stored hash with
  parameters out of range is a mismatch, not a crash or an unbounded
  computation.
- Access tokens: HS256 JWTs under `AUTH_SECRET` (32 bytes at least),
  issuer and expiry required, any other algorithm refused. Refresh
  tokens are stored hashed and rotate; logout and `RevokeAll` end the
  session server-side.
- Cookies: HttpOnly, `Secure` in production, the access cookie
  SameSite=Lax and the refresh cookie Strict. A cookie-authenticated
  state-changing request must be `application/json` or same-origin by
  `Sec-Fetch-Site` (CSRF).
- Sign-in with a provider: authorization code with PKCE, a sealed state
  cookie (HMAC, expiring), the ID token's signature (RS256, ES256),
  nonce, issuer and audience checked.
- Credential routes are rate-limited per client (`AUTH_LOGIN_RPS`).
- Invitations are signed, single-use, expiring, and bound to the
  invited address. The owner claim is opt-in and its token single-use.

### Data access

- Generated queries are parameterized (sqlc, pgx); no SQL is built from
  input. Owned models scope every read and write to the user or the
  workspace in the generated handlers.
- Full-text search goes through `websearch_to_tsquery`; highlights come
  back as text parts the page escapes, never as HTML.

### Webhooks

- Incoming (`pkg/webhook`): HMAC verification in constant time, every
  candidate signature compared, a replay window and a delivery id that
  drops repeats; the body is bounded.
- Outgoing (`packs/hooks`): Standard Webhooks signatures; targets must
  be https and public. The address is checked after DNS at every
  connection, so a name that resolves inward is refused; loopback,
  private, link-local, carrier-grade NAT, documentation ranges and IPv6
  forms that carry an IPv4 address (mapped, compatible, NAT64, 6to4,
  Teredo) are blocked. No redirects, no proxy.

### Billing (`packs/billing`)

- Plan changes come only from signed Stripe events, applied in event
  order (an older event never overwrites a newer state). Checkout and
  portal return URLs are paths on `APP_URL`, never another host.

### Storage, mail, realtime, i18n

- Storage keys cannot hold `..`, a leading slash, backslashes or
  control characters, and the local provider opens files through
  `os.Root`, so a key cannot leave its directory.
- Mail refuses line breaks in address headers, duplicate and excess
  recipients, and custom `Message-ID` or `Date` headers.
- Realtime refuses every topic unless the app passes `Authorize`,
  checks the origin, and bounds connections, the per-connection queue
  and the topics one connection holds.
- A client's timezone is only ever an IANA zone name.

### Secrets

- `credentials.yml.enc` is sealed with AES-256-GCM under the master key
  (`LIDZA_MASTER_KEY` or `config/master.key`, never committed). Docker
  images carry neither the key nor `.env` files.
- The MCP server's inspection tools are read-only and never return
  secret values.

### WASM packs

- Rust packs run in wazero with WASI but no mounted files, no network
  and wazero's fixed clock; memory capped per instance (64 MB by
  default), a deadline per call, a bounded pool.

## Left to the app

- Deciding who may do what: roles, `Authorize` for realtime topics,
  which routes `Require` guards.
- Validating business rules beyond the schema's constraints.
- Keeping `AUTH_SECRET`, the master key and provider keys out of the
  repository and rotating them when exposed.
- Running behind TLS in production.

## How this is checked

- Unit and integration tests per pack, and the platform evals.
- Fuzz tests on every parser of untrusted input: webhook signatures,
  tokens, sealed cookies, stored password hashes, provider keys,
  `schema.lidza`, the credentials file, decimals, dates, highlights,
  forwarding headers, storage keys, webhook targets, timezones and mail
  addresses. Their seeds run with `go test`; `scripts/fuzz.sh` runs
  each one for a while.
- `govulncheck` before every release and in CI; CodeQL code scanning per
  release tag and weekly.
- No external audit has been done yet.
