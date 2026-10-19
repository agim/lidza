# Configuration

Every setting a Līdza app reads, from the framework's source (`go generate ./pkg/configref` writes this file).
An app reads `.env`, then `.env.<mode>`, then the sealed credentials file and the values saved in its database, then the process environment; later wins.
A pack's settings matter only when the pack is enabled in `lidza.json`.

**Kind** says where a setting belongs. `app`: the app's configuration (keys, providers, addresses, senders), stored in the app's
database (the admin pages, `lidza settings set`), so the app moves with its database; the sealed credentials file seeds development.
`infra`: the deployment's environment: the database, the master key, addresses, logging, tracing and tuning.

## Core

| Setting | Kind | Default | Description |
|---|---|---|---|
| `APP_URL` | app |  | The app's public address (https://app.example.com): links in mail, sign-in redirects, billing return pages, security.txt, HSTS when https. |
| `LIDZA_LOG` | infra |  | Selects the format: "json" or "text". Default: text under lidza dev and lidza test, json otherwise. |
| `LIDZA_LOG_LEVEL` | infra |  | Debug, info, warn or error; default info. |
| `LIDZA_MCP` | infra |  | Set to "stdio" makes the binary serve its tools over stdio instead of HTTP; `lidza mcp` uses it. |
| `LIDZA_MCP_TOKEN` | infra |  | Enables /mcp on the running binary for clients that send it as a bearer token. |
| `LIDZA_TLS_ADDR` | infra |  | LIDZA_TLS_ADDR and LIDZA_TLS_HTTP_ADDR are the HTTPS and HTTP listen addresses, :443 and :80 by default. |
| `LIDZA_TLS_CACHE_DIR` | infra |  | Stores certificates on disk instead of Postgres: one node only. |
| `LIDZA_TLS_DIRECTORY` | infra |  | The ACME directory URL; Let's Encrypt by default, its staging directory for rehearsals. |
| `LIDZA_TLS_DOMAINS` | infra |  | Lists the domains to serve, comma-separated; the first is the app's public name (APP_URL when unset). |
| `LIDZA_TLS_EMAIL` | infra |  | The ACME account contact (expiry warnings). |
| `LIDZA_TLS_HTTP_ADDR` | infra |  | LIDZA_TLS_ADDR and LIDZA_TLS_HTTP_ADDR are the HTTPS and HTTP listen addresses, :443 and :80 by default. |
| `SECURITY_CONTACT` | app |  | Where to report a vulnerability (mailto: or https:); with it the app serves /.well-known/security.txt. |
| `SECURITY_POLICY` | app |  | The URL of the security policy security.txt links to. |

## `pkg/credentials`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `LIDZA_MASTER_KEY` | infra |  | Carries the key where no file should exist: production. |
| `LIDZA_MASTER_KEY_OFF` | infra |  | LIDZA_MASTER_KEY_OFF=1 makes config/master.key unreadable to the process: lidza test and verify set it, so tests never open the app's real credentials. A test that needs a key sets LIDZA_MASTER_KEY itself. |

## `pkg/devserver`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `LIDZA_ADDR` | infra |  | listen address |
| `LIDZA_FRONTEND_URL` | infra |  | dev only: the dev server to proxy to |
| `LIDZA_MODE` | infra |  | "dev" or unset |
| `LIDZA_SSR` | infra |  | Enables per-request rendering: "1" starts the sidecar. |

## `pkg/middleware`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `LIDZA_TRUSTED_PROXIES` | infra |  | Names the proxies whose forwarded headers count: IPs and CIDRs, comma-separated, plus "loopback" (127.0.0.0/8, ::1) and "private" (10/8, 172.16/12, 192.168/16, fc00::/7). Empty trusts none. |

## `pkg/tracing`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | infra |  | OTLP/HTTP collector address (http://collector:4318); setting it turns tracing on. The other standard OTEL_* variables apply (OTEL_SERVICE_NAME, OTEL_TRACES_SAMPLER, OTEL_EXPORTER_OTLP_HEADERS, OTEL_RESOURCE_ATTRIBUTES). |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | infra |  | The traces endpoint alone, in place of OTEL_EXPORTER_OTLP_ENDPOINT. |
| `OTEL_SDK_DISABLED` | infra |  | true turns tracing off even with an endpoint set. |
| `OTEL_SERVICE_NAME` | infra |  | The service name on spans; default: the app's name. |

## `packs/admin`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `ADMIN_USERS` | app |  | Lists who may open the admin pages. |

## `packs/analytics`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `ANALYTICS_CLIENT_RPS` | infra | `5` | Limits what one client may post per second. |
| `ANALYTICS_OTLP_URL` | app |  | An OTLP/HTTP logs endpoint (http://collector:4318/v1/logs). Empty keeps everything in Postgres. |
| `ANALYTICS_QUEUE` | infra | `1024` | Bounds records waiting to be written; the rest are dropped and counted. |
| `ANALYTICS_RETENTION` | infra | `720h` | Drops rows older than this on start; 0 keeps them. |

## `packs/audit`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `AUDIT_RETENTION` | infra | `8760h` | Drops records older than this, at start and then daily; 0 keeps them all. |

## `packs/auth`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `AUTH_ACCESS_TTL` | app | `15m` | Bounds an access token; AUTH_REFRESH_TTL a session. |
| `AUTH_CONNECT` | app |  | The external accounts a signed-in user can connect, comma-separated; each needs AUTH_CONNECT_<NAME>_CLIENT_ID and AUTH_CONNECT_<NAME>_CLIENT_SECRET. |
| `AUTH_COOKIE_SECURE` | infra |  | Marks cookies Secure; set it in production behind TLS. |
| `AUTH_ISSUER` | app | `lidza` | The JWT iss claim. |
| `AUTH_LOGIN_BURST` | infra | `5` | AUTH_LOGIN_RPS and AUTH_LOGIN_BURST bound credential attempts per client address on the routes wrapped with Throttle (register, verify, forgot, reset) and, unless AUTH_SIGNIN_RPS is set, ThrottleSignIn. |
| `AUTH_LOGIN_RPS` | infra | `1` | AUTH_LOGIN_RPS and AUTH_LOGIN_BURST bound credential attempts per client address on the routes wrapped with Throttle (register, verify, forgot, reset) and, unless AUTH_SIGNIN_RPS is set, ThrottleSignIn. |
| `AUTH_MIN_PASSWORD` | app | `10` | The floor ValidatePassword applies. |
| `AUTH_OWNER_CLAIM` | infra |  | Makes the app's first account (FirstSubject, an admin) the account that presents a one-time token, not the first to sign in (owner.go). For a deployment anyone can reach before its owner. |
| `AUTH_OWNER_CLAIM_DIR` | infra |  | Holds the token file (while unclaimed) and status.json; config/owner-claim by default. |
| `AUTH_OWNER_CLAIM_TOKEN` | infra |  | The token when the deployment platform supplies it (at least 32 characters; the same on every node); generated into AUTH_OWNER_CLAIM_DIR/token otherwise. |
| `AUTH_PROVIDERS` | app |  | The sign-in providers auth.Mount serves, comma-separated (google, github, microsoft, or any name with AUTH_<NAME>_ISSUER for an OIDC issuer); each needs AUTH_<NAME>_CLIENT_ID and AUTH_<NAME>_CLIENT_SECRET, and Microsoft may take AUTH_MICROSOFT_TENANT. |
| `AUTH_REFRESH_TTL` | app | `720h` | AUTH_ACCESS_TTL bounds an access token; AUTH_REFRESH_TTL a session. |
| `AUTH_SECRET` | app |  | Signs access tokens; at least 32 bytes, the same on every node. Unset, the app generates one on its first start and keeps it in its database, sealed with the master key. Rotating it logs everyone out. |
| `AUTH_SIGNIN_BURST` | infra |  | AUTH_SIGNIN_RPS and AUTH_SIGNIN_BURST bound password sign-ins per client address on the routes wrapped with ThrottleSignIn (Mount's login), apart from the other credential routes; zero uses AUTH_LOGIN_RPS and AUTH_LOGIN_BURST. |
| `AUTH_SIGNIN_RPS` | infra |  | AUTH_SIGNIN_RPS and AUTH_SIGNIN_BURST bound password sign-ins per client address on the routes wrapped with ThrottleSignIn (Mount's login), apart from the other credential routes; zero uses AUTH_LOGIN_RPS and AUTH_LOGIN_BURST. |
| `AUTH_TOKEN_TTL` | app | `1h` | Bounds the one-time tokens IssueToken creates (email verification, password reset) unless the call says otherwise. |

## `packs/billing`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `STRIPE_API_URL` | infra | `https://api.stripe.com` | Stripe's API; tests point it at a fake. |
| `STRIPE_SECRET_KEY` | app |  | The Stripe secret key: sk_test_... in development, sk_live_... in production (in the credentials). |
| `STRIPE_WEBHOOK_SECRET` | app |  | Names the setting holding the endpoint's signing secret (whsec_...), which webhook.Stripe reads. |

## `packs/cache`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `CACHE_MEMORY_MAX` | infra | `10000` | Bounds the in-process backend's entries. |
| `CACHE_PREFIX` | infra | `lidza:` | Namespaces keys, so several apps can share a server. |
| `CACHE_URL` | infra | required | redis://host:port, valkey://..., or "memory". |

## `packs/db`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | infra | required | The connection string, e.g. postgres://user:pass@host/db or postgres:///db?host=/var/run/postgresql for a Unix socket (/tmp on macOS; LocalURL finds this machine's). |
| `DB_CONNECT_TIMEOUT` | infra | `5s` | Bounds opening a connection and the first ping. |
| `DB_CREDENTIALS_POLL` | infra | `10s` | How often each node checks the settings saved in the database for a change made on another node (the admin pages save on one), then loads them and reconfigures the packs; 0 turns it off. |
| `DB_MAX_CONNS` | infra | `10` | Bounds the pool; size it below Postgres max_connections divided by the number of app nodes. |
| `DB_MIGRATE` | infra |  | Applies pending migrations from db/migrations at start. |
| `DB_MIGRATE_LOCK_TIMEOUT` | infra | `5s` | Bounds how long each migration statement waits for a table lock before it gives up and retries, so a migration queued behind a long query never stalls the queries queued behind it. |
| `DB_MIGRATIONS_DIR` | infra | `db/migrations` | Where the migration files live, relative to the working directory. |
| `DB_MIN_CONNS` | infra | `0` | Keeps that many connections open while idle. |
| `PGHOST` | infra |  | Read by the local database lookup (LocalURL) when DATABASE_URL is unset in development. |
| `PGPORT` | infra |  | Read with PGHOST by the local database lookup. |

## `packs/hooks`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `HOOKS_ALLOW_PRIVATE` | infra |  | Lets endpoints point at loopback and private networks: for development and tests only. |
| `HOOKS_DISABLE_AFTER` | infra | `20` | Disables an endpoint after this many deliveries in a row that failed every attempt. |
| `HOOKS_MAX_ENDPOINTS` | infra | `20` | Bounds the endpoints of one owner. |
| `HOOKS_RETENTION` | infra | `720h` | Drops deliveries older than this, daily; 0 keeps them. |
| `HOOKS_TIMEOUT` | infra | `10s` | Bounds one delivery attempt, connection to response. |

## `packs/i18n`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `I18N_DEFAULT` | app | `en` | The locale used when nothing better matches. |
| `I18N_TIMEZONE` | app | `UTC` | The IANA zone used when the request names none. |

## `packs/jobs`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `JOBS_DRAIN` | infra | `1s` | How long running jobs may finish at shutdown before they are cancelled and released back to pending, to run again at once on another node or after the restart (the attempt is not counted). The shutdown deadline caps it; 0 releases at once. |
| `JOBS_MAX_ATTEMPTS` | infra | `5` | The default retry budget per job. |
| `JOBS_POLL` | infra | `1s` | How often idle workers look for work. |
| `JOBS_STALE` | infra | `10m` | How long a running job may go without finishing before another worker takes it over (a crashed node). |
| `JOBS_TIMEOUT` | infra | `5m` | Bounds one execution. |
| `JOBS_WORKERS` | infra | `4` | Bounds concurrent jobs on this node; 0 disables running (enqueue only, for nodes that just serve HTTP). |

## `packs/llm`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `EMBED_API_KEY` | app |  | Authenticates EMBED_PROVIDER (openai, google; optional for compatible). When EMBED_PROVIDER is the chat provider, empty uses LLM_API_KEY. |
| `EMBED_BASE_URL` | app |  | Overrides EMBED_PROVIDER's API base, as LLM_BASE_URL does for chat; required for compatible. When EMBED_PROVIDER is the chat provider, empty uses LLM_BASE_URL. |
| `EMBED_MODEL` | app |  | The embedding model; each provider has a default. |
| `EMBED_PROVIDER` | app |  | Serves Embed: openai, google, ollama, compatible or fake, or none to turn embeddings off. Empty uses the chat provider (LLM_PROVIDER), which cannot embed when it is anthropic. |
| `LLM_API_KEY` | app |  | Authenticates anthropic, openai and google. |
| `LLM_BASE_URL` | app |  | Overrides the provider's API base (a proxy, a region, Ollama on another host: http://127.0.0.1:11434 by default). |
| `LLM_EMBED_MODEL` | app |  | The older name of EMBED_MODEL (LLM_EMBED_MODEL), read when EMBED_MODEL is empty. |
| `LLM_MAX_ATTEMPTS` | infra | `3` | How many times a call is tried on 429, 5xx or a network error, with backoff (1s, 2s, 4s) and Retry-After honoured. |
| `LLM_MAX_TOKENS` | infra | `4096` | Bounds a reply unless the request says otherwise. The newest models think before answering, inside this bound. |
| `LLM_MAX_TOOL_ROUNDS` | infra | `8` | Bounds Run: how many times the model may call tools before it must answer. |
| `LLM_MODEL` | app |  | The chat model; each provider has a default. |
| `LLM_PROVIDER` | app | `fake` | Fake (default; scripted replies for tests and a first run), ollama (a local model), anthropic, openai, google (Gemini) or compatible (any server speaking the OpenAI API: llama.cpp's llama-server, vLLM, LM Studio; LLM_BASE_URL required, the key optional). |
| `LLM_TIMEOUT` | infra | `60s` | Bounds one attempt of one call. |

## `packs/mail`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `MAIL_API_KEY` | app |  | Authenticates the HTTP providers. |
| `MAIL_BASE_URL` | app |  | Overrides a provider's API base outright (a proxy); it wins over MAIL_REGION. |
| `MAIL_DOMAIN` | app |  | The sending domain (Mailgun). |
| `MAIL_FROM` | app |  | The default sender, "Name <address>" or an address. |
| `MAIL_MAX_ATTACHMENTS` | infra | `10` | Bounds the number of files per message. |
| `MAIL_MAX_ATTACHMENT_BYTES` | infra | `10485760` | Bounds the total decoded attachment bytes. |
| `MAIL_MAX_ATTEMPTS` | infra | `5` | Bounds delivery retries through the jobs pack. |
| `MAIL_MAX_RECIPIENTS` | infra | `50` | Bounds the total To, Cc and Bcc addresses per message. |
| `MAIL_PROVIDER` | app | `log` | Log (default), outbox (record only; tests), smtp, mailgun, sendgrid, postmark or resend. |
| `MAIL_REGION` | app |  | Picks a provider's regional API: "eu" for Mailgun (api.eu.mailgun.net) and SendGrid (api.eu.sendgrid.com); "us", the default, otherwise. |
| `MAIL_SMTP_HOST` | app |  | MAIL_SMTP_HOST and MAIL_SMTP_PORT address the server; the port defaults to 587 (465 with MAIL_SMTP_SECURITY "tls", 25 with "none"). |
| `MAIL_SMTP_PASSWORD` | app |  | MAIL_SMTP_USERNAME and MAIL_SMTP_PASSWORD authenticate (PLAIN); both empty sends without authentication. |
| `MAIL_SMTP_PORT` | app |  | MAIL_SMTP_HOST and MAIL_SMTP_PORT address the server; the port defaults to 587 (465 with MAIL_SMTP_SECURITY "tls", 25 with "none"). |
| `MAIL_SMTP_SECURITY` | app | `starttls` | "starttls" (the default: the connection is upgraded, and refused when the server cannot), "tls" (encrypted from the start, usually port 465) or "none" (plain text; a local relay only). |
| `MAIL_SMTP_TIMEOUT` | infra | `30s` | Bounds connection setup and sending, including a stalled peer. |
| `MAIL_SMTP_URL` | app |  | smtp://user:pass@host:587 (STARTTLS) or smtps://user:pass@host:465 (TLS). The SMTP fields below are the same setting in parts; the URL wins when both are set. |
| `MAIL_SMTP_USERNAME` | app |  | MAIL_SMTP_USERNAME and MAIL_SMTP_PASSWORD authenticate (PLAIN); both empty sends without authentication. |
| `MAIL_TEMPLATES` | infra | `mail` | Holds <name>.txt.tmpl and <name>.html.tmpl. |
| `MAIL_TRACKING` | app |  | Lets the provider's click and open tracking apply. Off (the default), Mailgun, SendGrid and Postmark are told on every message not to rewrite links or add a tracking pixel, whatever the account's settings: a verification or reset link stays the app's own https link, and a link scanner cannot spend it through a tracker. Resend has no per-message switch: turn tracking off for the domain in its dashboard. |

## `packs/realtime`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `REALTIME_BUFFER` | infra | `64` | The per-connection outbound queue; a client that falls this far behind is disconnected rather than growing memory. |
| `REALTIME_BUS_URL` | infra |  | A Valkey or Redis URL (redis://host:6379). Empty keeps fan-out inside the node, which is right for one node only. |
| `REALTIME_MAX_CONNS` | infra | `10000` | Caps open WebSockets on this node; beyond it, 503. |
| `REALTIME_MAX_TOPICS` | infra | `100` | Caps the topics one connection holds; further subscriptions are ignored. Topics longer than 256 bytes are refused. |
| `REALTIME_WRITE_TIMEOUT` | infra | `5s` | Bounds each send to a client. |

## `packs/storage`

| Setting | Kind | Default | Description |
|---|---|---|---|
| `STORAGE_ACCESS_KEY` | app |  | STORAGE_ACCESS_KEY and STORAGE_SECRET_KEY authenticate; keep them in the credentials. |
| `STORAGE_ACCOUNT_ID` | app |  | The Cloudflare account of an R2 bucket. |
| `STORAGE_BUCKET` | app |  | The S3 bucket. |
| `STORAGE_CDN` | app |  | The STORAGE_CDN in front of the bucket whose cache Purge empties: digitalocean (the Spaces STORAGE_CDN), or empty for none. |
| `STORAGE_CDN_API_URL` | infra | `https://api.digitalocean.com` | The STORAGE_CDN's API; tests point it at a stub. |
| `STORAGE_CDN_ENDPOINT_ID` | app |  | The STORAGE_CDN endpoint's id (DigitalOcean: the UUID of the Spaces STORAGE_CDN endpoint). |
| `STORAGE_CDN_TOKEN` | app |  | Authenticates the STORAGE_CDN's API (DigitalOcean: a personal access token with STORAGE_CDN write scope); keep it in the credentials. |
| `STORAGE_DIR` | infra | `storage` | Holds the files of the local provider. |
| `STORAGE_ENDPOINT` | app | `https://s3.amazonaws.com` | The service: https://s3.amazonaws.com (default), https://<account>.r2.cloudflarestorage.com, http://127.0.0.1:9000 for MinIO. |
| `STORAGE_MAX_SIZE` | infra | `104857600` | Bounds one Put. |
| `STORAGE_PATH_STYLE` | app |  | Puts the bucket in the path (http://host/bucket/key) instead of the host; on by default for every endpoint but AWS. |
| `STORAGE_PREFIX` | app |  | A folder every key lives under ("myapp" stores avatars/1.png as myapp/avatars/1.png), for a bucket several apps share. The app's keys never carry it: Put, Get, List and the rest add it, and the objects they return have it removed. |
| `STORAGE_PROVIDER` | app | `local` | Local (default: files under STORAGE_DIR), s3 (Amazon S3, or any S3-compatible service at STORAGE_ENDPOINT), or a named S3-compatible service whose address follows from STORAGE_REGION or STORAGE_ACCOUNT_ID: r2 (Cloudflare R2), spaces (DigitalOcean Spaces), b2 (Backblaze B2), gcs (Google Cloud Storage with HMAC keys), minio (MinIO, RustFS or another server at STORAGE_ENDPOINT). |
| `STORAGE_PUBLIC_URL` | app |  | Where objects are readable without signing (a STORAGE_CDN, a public bucket); URL returns it plus the key. |
| `STORAGE_REGION` | app | `us-east-1` | Signs the requests; us-east-1 by default (R2 and MinIO accept auto or us-east-1). |
| `STORAGE_SECRET_KEY` | app |  | STORAGE_ACCESS_KEY and STORAGE_SECRET_KEY authenticate; keep them in the credentials. |
| `STORAGE_STORES` | app |  | Names further stores, comma-separated ("media,exports"), each with its own bucket: Named(ctx, "media") is configured by STORAGE_MEDIA_BUCKET, _PREFIX, _PUBLIC_URL, _DIR and _CDN_ENDPOINT_ID, and takes the provider, address, region, keys and STORAGE_CDN token from STORAGE_MEDIA_* when set, else from the main store. Public files go in one, so they never share a bucket and prefix with private ones. |
| `STORAGE_TIMEOUT` | infra | `60s` | Bounds one operation. |
