# Deployment

A Līdza app deploys as one binary. `lidza build` builds the frontend,
embeds it and compiles `bin/<name>`; the binary serves the API, the
frontend and the operational endpoints, and needs Node at runtime only
for per-request SSR. `lidza new` writes a `Dockerfile`, a `.dockerignore`
and `deploy/<name>.service`; this page is the rest.

## Managed deploys

To deploy from a portal instead of by hand, use
[Līdza Deploy](https://github.com/agim/lidza-deploy): a deployment agent
and a web control panel built with Līdza. One agent hosts many Līdza apps
on a Debian or Ubuntu server, each on its own domain with HTTPS through
Caddy. It deploys a GitHub repository and branch on push (signed
webhooks) or by hand, builds the release in Docker, checks `/readyz`
before traffic switches, and keeps deployment history, logs and rollback;
the panel has administrator, deployer and viewer roles. Install it on the
server with its `install.sh` (its README has the steps). It reads the
contracts this page describes: the binary's `/healthz` and `/readyz`,
`APP_URL`, `LIDZA_TRUSTED_PROXIES` (`loopback` behind its Caddy), and the
owner claim's token and status files.

The rest of this page is for running the binary yourself.

## Build

```sh
lidza build                     # bin/<name>, frontend embedded
docker build -t <name> .        # the same, in the image from the Dockerfile
```

The Dockerfile installs the CLI with `go install github.com/agim/lidza/cmd/lidza@<version>`,
the version of the CLI that wrote it (or `latest`). An app created with
`--lidza-dir` has a `replace` in `go.mod` pointing at a local checkout;
drop it, or copy the checkout into the build context, before building an
image. Rust packs need the Rust toolchain in the builder: uncomment the
two lines.

An app with a `sqlc.yaml` (the db pack) generates its queries with sqlc
during the build. The Dockerfile installs the release the framework
supports (`v1.31.1`, built with cgo for its Postgres parser) only when
the file is there, in a layer the app's sources do not invalidate. A
`lidza build` without sqlc stops before compiling anything and says how
to install it. A builder that deliberately builds with the committed
`db/queries/gen` (kept current by `lidza verify`) runs `lidza build
--pregenerated` (or sets `LIDZA_PREGENERATED=1`); it fails when that
code is missing. An app made before this has the old Dockerfile: `lidza
gen deploy` refreshes it.

The runtime image runs as distroless's `nonroot` user (65532) and its
files belong to it (`COPY --chown=65532:65532`), with the modes they had
in the build: a checkout made under umask 0077 (directories 0700, files
0600) still starts, and nothing is opened to other users. Run it with a
read-only root filesystem (`docker run --read-only`). The master key,
`.env` files and the owner claim never enter the image (`.dockerignore`);
the sealed `config/credentials.yml.enc` does, and `LIDZA_MASTER_KEY` in
the environment opens it. Apps with an older Dockerfile: `lidza gen
deploy`.

`lidza build` also stores a Brotli (`.br`) and a gzip (`.gz`) copy of
every compressible file of the frontend build (HTML, CSS, JavaScript,
JSON, SVG, text) of 1 KB or more, embedded with the rest. The binary
sends the copy a request accepts (`Accept-Encoding`, quality values
honoured), with the original's type, `Vary: Accept-Encoding`, its own
length and ETag; HEAD and 304s work for every representation, and a
range request gets the uncompressed file. Pages built per request (a
`Head`, a locale) are gzipped as they go out. API responses are not
compressed by the binary. A build made with `npm run build` alone has
no copies and is served uncompressed.

## Configuration

The binary reads `.env`, then `.env.<mode>`, then the process
environment (later wins). `LIDZA_MODE` unset is production. What every
app reads:

| Variable | Production value |
|---|---|
| `LIDZA_ADDR` | `127.0.0.1:3000` behind a proxy, `0.0.0.0:3000` in a container |
| `LIDZA_LOG` | `json` (the default outside `lidza dev`) |
| `LIDZA_LOG_LEVEL` | `info` |
| `LIDZA_SSR` | `1` to render pages per request (react template; needs Node and `dist/.server` next to the binary) |
| `LIDZA_MCP_TOKEN` | unset, unless agents should reach the app's tools at `/mcp` |

Packs add their own (`.env.example` lists them): `DATABASE_URL` and
`DB_MAX_CONNS` (`db`), `AUTH_SECRET` (32 random bytes or more, the same
on every node) and `AUTH_COOKIE_SECURE=true` behind TLS (`auth`),
`CACHE_URL` and `BUS_URL` for Valkey (`cache`, `realtime`), the
analytics retention and OTLP endpoint, `MAIL_PROVIDER`, `MAIL_FROM` and
`MAIL_API_KEY` (`mail`; keep `MAIL_PROVIDER=log` until the domain is
verified at the provider).

## Secrets

`config/credentials.yml.enc` ships with the app; the master key does
not. Set `LIDZA_MASTER_KEY` to the contents of `config/master.key` in the
environment of the process (the unit's `.env`, the container's
`--env-file`), and every pack reads the sealed values as if they were
in `.env`. Production reads the plain values and the file's
`production:` section (`lidza credentials set production.NAME=...`),
so one file holds the sandbox keys for development and the live ones,
and the master key is all a deploy adds. Values saved from the admin
pages live in the database, sealed with the same key, and every node
reads them.

## The first admin on a public deployment

By default the first account to sign in is the app's first admin. On a
deployment anyone can reach before its owner does, set
`AUTH_OWNER_CLAIM=true`: then the first admin is the signed-in account
that presents a one-time owner token, whoever signed in first.

- The token is `AUTH_OWNER_CLAIM_TOKEN` when the platform supplies one
  (at least 32 characters; the same on every node), else generated once
  into `<AUTH_OWNER_CLAIM_DIR>/token`, mode 0600 in a 0700 directory
  (`config/owner-claim/` by default; it ignores itself in git and the
  Dockerfile leaves it out of images). Only its SHA-256 is in the
  database (`auth_owner_claim`). It never appears in logs, responses or
  URLs.
- `<AUTH_OWNER_CLAIM_DIR>/status.json` is `{"state":"unclaimed"}` or
  `{"state":"claimed","claimedAt":"..."}`, written at start and on
  claim: what a hosting agent reads. The token file exists only while
  unclaimed.
- The owner signs in (any provider) and pastes the token on `/admin`
  (the page offers the form), or the app's own page posts it to `POST
  /api/v1/auth/owner/claim` (`{"token": ...}`; `GET /api/v1/auth/owner`
  says unclaimed or claimed). Wrong is 403, already claimed 409; both are
  throttled and reported as `owner_claim_failed` events without the
  token. The claim is one conditional update: of two at once, one wins.
- Ownership never reopens: the row stays when the owner's sessions end
  or the account is deleted. A lost owner is recovered as any admin is,
  through `ADMIN_USERS` (`lidza admin add`).
- `lidza admin owner status` says which state and where the token file
  is; `lidza admin owner rotate` replaces an unclaimed token (the file
  and the stored hash: the old one stops working at once, on every
  node). Add `--production` for a database on another host.
- Turning it on in an app that already has a first account records that
  account as the owner.

What takes effect when: `AUTH_OWNER_CLAIM`, `AUTH_OWNER_CLAIM_TOKEN` and
`AUTH_OWNER_CLAIM_DIR` are read at start (a restart applies a change); a
rotation and a claim apply at once, without a restart; `ADMIN_USERS` is
read within seconds.

## Database

Migrations are files under `db/migrations`, read from the working
directory, so they ship next to the binary (the Dockerfile copies `db/`,
and with it `mail/`, `admin/` and `config/credentials.yml.enc`; `.dockerignore`
keeps `config/master.key` out, so the image needs `LIDZA_MASTER_KEY`). The
local storage provider writes to `STORAGE_DIR`, which the read-only image
has no room for: production uses `STORAGE_PROVIDER=s3` or a volume there.
An app created by an earlier release gets the current templates with
`lidza gen deploy` (`lidza update` runs it; a file the app edited is
kept and named, `--force` replaces it).
Apply them either from a deploy step:

```sh
lidza db migrate --production   # or, without the CLI on the host:
DB_MIGRATE=true ./bin/<name>    # applies pending migrations at start, then serves
```

`lidza db migrate` and `lidza db rollback` refuse a `DATABASE_URL` on
another host (not a Unix socket, `localhost` or a loopback address)
without `--production`, so a production URL left in a shell does not
change that database by accident; the MCP tools never pass the flag.

`DB_MIGRATE=true` on every node is safe: migrations are applied in one
transaction each under a lock, so the second node finds nothing to do.
Prefer the deploy step when a migration is marked `-- review` (it can
lose data or fail on existing rows).

### Migrations without downtime

While a migration runs, the app keeps serving, and until the rollout
ends the previous version runs too. Līdza keeps both working:

- Every statement waits at most `DB_MIGRATE_LOCK_TIMEOUT` (default 5s)
  for a table lock, so a migration queued behind a long query never
  stalls the queries queued behind it. One that times out is retried
  three times (1s, 2s, 4s apart), then the migration stops with the
  reason and nothing of it applied.
- On a table that already exists, `lidza gen` writes what would scan it
  under a lock as a second migration, `<stamp>_..._concurrently`, that
  starts with `-- lidza:no-transaction` and runs statement by
  statement: indexes are built `CREATE INDEX CONCURRENTLY`, foreign keys
  and checks added `NOT VALID` in the first migration and validated in
  the second, `NOT NULL` set through a validated check, and a unique
  field built as a concurrent index that then becomes the constraint.
  Each statement there can run again, so a failed index build is simply
  retried by the next `lidza db migrate`.
- What cannot be made safe automatically is a warning in `lidza check`
  (L021) on migrations not yet committed, with the steps: a rename, a
  type change, a dropped column or table, a volatile default
  (`gen_random_uuid()`) on a new column. These need expand and contract
  across releases: add the new column, write both and backfill in
  batches, switch reads, and drop the old one in a later release, after
  every node runs code that no longer reads it.

A hand-written migration follows the same rules; `-- lidza:ignore L021`
on the line before a statement accepts it (a small table, a
maintenance window).

## Process

- **systemd**: `deploy/<name>.service` runs the binary from
  `/opt/<name>` as its own user with the environment from
  `/opt/<name>/.env`, restarts it on failure and hardens the sandbox.
  The comments at the top have the install commands.
- **Container**: the image runs as `nonroot` on port 3000; pass the
  environment at run time (`docker run --env-file .env.production`).
- **Shutdown**: SIGTERM drains in-flight requests up to the request
  timeout, then stops the packs: a running job gets `JOBS_DRAIN` (1s) to
  finish, then its context is cancelled and it goes back to pending, due
  at once for another node or the restart.

## TLS without a proxy

`lidza ship --domains app.example.com --email ops@example.com` records
the domains in `lidza.json` and writes them to `deploy/production.env`
on every ship, with `LIDZA_LOG=json` and `DB_MIGRATE=true`; that file
plus `DATABASE_URL` and `LIDZA_MASTER_KEY` is the process environment
(`/opt/<name>/.env` for the unit, `--env-file` for the container, which
exposes 80 and 443). Set `LIDZA_TLS_DOMAINS` and the binary serves
HTTPS itself:

```
LIDZA_TLS_DOMAINS=app.example.com,www.app.example.com
LIDZA_TLS_EMAIL=ops@example.com
```

It listens on 443 with certificates from Let's Encrypt, obtained on the
first request for each domain and renewed before they expire
(`golang.org/x/crypto/acme/autocert`; no vendor SDK), answers 80 only
to redirect to HTTPS and to prove ownership of the domain, and derives
`APP_URL` (links in emails, the sign-in providers' callback URL) and
`AUTH_COOKIE_SECURE=true` unless they are set. `LIDZA_ADDR` is ignored.

Certificates and the ACME account key are stored in Postgres through
the db pack, in the `tls_certificate` table the framework creates on
first use, so every node of the app serves the same certificates and
any node can renew them. A single node without the db pack sets
`LIDZA_TLS_CACHE_DIR=/opt/<name>/certs` instead.

What the server needs, and what `lidza doctor` checks in the project:

- **DNS**: an A record (and AAAA for IPv6) for every domain in the list
  pointing at the server's public address. Let's Encrypt connects to it
  on port 80 to validate; a domain that does not resolve to this server
  cannot get a certificate.
- **Ports 80 and 443 open** in the firewall and free on the machine.
- **The right to bind them**: `deploy/<name>.service` grants
  `CAP_NET_BIND_SERVICE` to the unprivileged user; by hand, `sudo setcap
  'cap_net_bind_service=+ep' bin/<name>`. In a container, publish 80 and
  443.
- **Hostnames added while the app runs**: `App.TLSHosts` approves a
  host outside `LIDZA_TLS_DOMAINS` (a customer's own domain, which
  points its DNS record at this server): `func(ctx, host) error`, nil
  approves, with the services in `ctx`. It is asked in the TLS
  handshake, the ACME challenges and before every certificate order,
  renewals included; the verdict is cached per node, an approval 5
  minutes and a refusal 1 minute (at most 10,000 hosts), so a host the
  app stops approving is refused within 5 minutes and its certificate
  is not renewed. Its certificate is kept with the others. Requests on
  it reach the whole app: serve its paths with `r.Mount` and keep the
  rest to the app's own domains with a middleware on `r.Host`.
  Approvals and refusals are logged (refusals at most 20 a minute) and
  the admin overview lists them for the node.
- **Rate limits**: Let's Encrypt issues a bounded number of certificates
  per domain per week; rehearse against its staging directory with
  `LIDZA_TLS_DIRECTORY=https://acme-staging-v02.api.letsencrypt.org/directory`
  (the browser will distrust those certificates, which is the point).

## In front of it

Or terminate TLS in a reverse proxy (Caddy, nginx, a load balancer) and
forward `Host`, `X-Forwarded-For` and `X-Forwarded-Proto`. A proxy may
compress too (Caddy `encode zstd gzip`, nginx `gzip on`); it passes an
already-compressed response through, so the two do not conflict, and it
is the way to compress API responses. Check what reaches the browser
through the proxy: `curl -sI -H 'Accept-Encoding: br, gzip'
https://app.example.com/` shows `Content-Encoding`.

Name the proxy in `LIDZA_TRUSTED_PROXIES`: `loopback` for a proxy on
the same host (Caddy, nginx), else its addresses or CIDRs
(`10.0.0.0/8`, `private` for the private ranges), comma-separated. Then
`middleware.ClientIP` is the visitor: from a trusted peer the binary
reads `X-Forwarded-For` (or `Forwarded`) from the right, skips trusted
hops and takes the first address that is not one, so a client cannot
spoof its way past (a left part it sent is never reached), and a
malformed entry stops the walk. Unset, no forwarded header is trusted
and every visitor behind a proxy is the proxy: one rate-limit bucket
for all, so one client can lock everyone out of sign-in. The rate
limiters, the auth throttles, `auth.Event.Request` and the request
log's `client_ip` all use it. The request log carries method, path (at
most 512 bytes, control characters escaped), status, bytes, duration,
request id and `client_ip`; never the query, headers, cookies or body. Route `/healthz` (liveness) and `/readyz`
(readiness: 503 while the database or the bus is unreachable) to the
orchestrator, and scrape `/metrics`. Keep `/mcp` and `/debug/pprof/`
(dev only) off the public side.

### Tracing

OpenTelemetry tracing is off until an OTLP/HTTP endpoint is set:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318   # Jaeger, Tempo, Honeycomb, any OTLP receiver
OTEL_SERVICE_NAME=shop                               # default: the app's name
OTEL_TRACES_SAMPLER=parentbased_traceidratio         # optional; with OTEL_TRACES_SAMPLER_ARG=0.1
```

The standard `OTEL_*` variables apply (`OTEL_EXPORTER_OTLP_HEADERS` for
a hosted backend's key, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_SDK_DISABLED`).
Recorded, each with the caller's span as parent:

- a server span per request, named by its route pattern (`GET
  /api/v1/posts/{id}`), continuing a `traceparent` the client sent;
- a span per database query under a request or a job, named by its sqlc
  query name (`GetPost`), without the SQL or its arguments;
- `enqueue <kind>` and a `job <kind>` span per run: the job row keeps
  the trace (`trace_parent`, added to the jobs pack's table by `lidza
  gen`), so the run continues the request's trace on whichever node
  takes it;
- `mail.send` with the provider, `llm.chat` and `llm.embed` with the
  model and token counts, never addresses, prompts or replies;
- a client span per call through `lidza.HTTPClient(ctx)`, which sends
  `traceparent` along.

Log records written with a request's context carry `trace_id` and
`span_id`, so a log line leads to its trace. Spans are batched with a
bounded queue: an unreachable collector costs dropped spans, not memory
or latency.

## Kubernetes

`lidza gen deploy --k8s` writes `deploy/k8s/<name>.yaml` from the same
contract as the Dockerfile: a Deployment (two replicas, the image's
nonroot user, a read-only root filesystem with all capabilities
dropped, `/tmp` the only writable path, startup and liveness probes on
`/healthz`, readiness on `/readyz`, settings from a Secret named after
the app), a ClusterIP Service on port 80 and a PodDisruptionBudget. Its
header lists the steps: push the image, create the Secret from
`deploy/production.env` plus `DATABASE_URL` and `LIDZA_MASTER_KEY`,
apply, then route an Ingress or Gateway to the Service (TLS ends there;
`LIDZA_TRUSTED_PROXIES=private` is set so the client address comes
through). Every replica runs `DB_MIGRATE=true`; the advisory lock lets
one apply the migrations. Once the file exists, `lidza gen deploy` and
`lidza update` keep it current like the Dockerfile. There is no Helm
chart.

## Scaling out

The app is stateless: sessions are rows, jobs are rows, the cache and
the realtime bus are Valkey. Run several instances behind the proxy;
give them the same `AUTH_SECRET` and the same database. `lidza
benchmark` (k6) reports the throughput and the heap of one instance;
`/metrics` shows the pool and connection gauges to size `DB_MAX_CONNS`.
