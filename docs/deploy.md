# Deployment

A Līdza app deploys as one binary. `lidza build` builds the frontend,
embeds it and compiles `bin/<name>`; the binary serves the API, the
frontend and the operational endpoints, and needs Node at runtime only
for per-request SSR. `lidza new` writes a `Dockerfile`, a `.dockerignore`
and `deploy/<name>.service`; this page is the rest.

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
in `.env`. Values saved from the admin pages live in the database,
sealed with the same key, and every node reads them.

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
lidza db migrate          # or, without the CLI on the host:
DB_MIGRATE=true ./bin/<name>   # applies pending migrations at start, then serves
```

`DB_MIGRATE=true` on every node is safe: migrations are applied in one
transaction each under a lock, so the second node finds nothing to do.
Prefer the deploy step when a migration is marked `-- review` (it can
lose data or fail on existing rows).

## Process

- **systemd**: `deploy/<name>.service` runs the binary from
  `/opt/<name>` as its own user with the environment from
  `/opt/<name>/.env`, restarts it on failure and hardens the sandbox.
  The comments at the top have the install commands.
- **Container**: the image runs as `nonroot` on port 3000; pass the
  environment at run time (`docker run --env-file .env.production`).
- **Shutdown**: SIGTERM drains in-flight requests up to the request
  timeout, then stops the packs (jobs finish their current run).

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
`APP_URL` (links in emails) and `AUTH_COOKIE_SECURE=true` unless they are
set. `LIDZA_ADDR` is ignored.

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
- **Rate limits**: Let's Encrypt issues a bounded number of certificates
  per domain per week; rehearse against its staging directory with
  `LIDZA_TLS_DIRECTORY=https://acme-staging-v02.api.letsencrypt.org/directory`
  (the browser will distrust those certificates, which is the point).

## In front of it

Or terminate TLS in a reverse proxy (Caddy, nginx, a load balancer) and
forward `Host`, `X-Forwarded-For` and `X-Forwarded-Proto`. The rate
limiters key on the client address by default; behind a proxy, key on
the forwarded address (`middleware.RateLimitOptions.Key`,
`auth.ThrottleBy`). Route `/healthz` (liveness) and `/readyz`
(readiness: 503 while the database or the bus is unreachable) to the
orchestrator, and scrape `/metrics`. Keep `/mcp` and `/debug/pprof/`
(dev only) off the public side.

## Scaling out

The app is stateless: sessions are rows, jobs are rows, the cache and
the realtime bus are Valkey. Run several instances behind the proxy;
give them the same `AUTH_SECRET` and the same database. `lidza
benchmark` (k6) reports the throughput and the heap of one instance;
`/metrics` shows the pool and connection gauges to size `DB_MAX_CONNS`.
