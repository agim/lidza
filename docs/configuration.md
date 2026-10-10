# Configuration

Every setting a Līdza app reads, from the framework's source (`go generate ./pkg/configref` writes this file).
An app reads `.env`, then `.env.<mode>`, then the sealed credentials, then the process environment; later wins.
A pack's settings matter only when the pack is enabled in `lidza.json`.

## Core

| Setting | Default | Description |
|---|---|---|
| `APP_URL` |  | The app's public address (https://app.example.com): links in mail, sign-in redirects, billing return pages, security.txt, HSTS when https. |
| `LIDZA_LOG` |  | Selects the format: "json" or "text". Default: text under lidza dev and lidza test, json otherwise. |
| `LIDZA_LOG_LEVEL` |  | Debug, info, warn or error; default info. |
| `LIDZA_MCP` |  | Set to "stdio" makes the binary serve its tools over stdio instead of HTTP; `lidza mcp` uses it. |
| `LIDZA_MCP_TOKEN` |  | Enables /mcp on the running binary for clients that send it as a bearer token. |
| `LIDZA_TLS_ADDR` |  | LIDZA_TLS_ADDR and LIDZA_TLS_HTTP_ADDR are the HTTPS and HTTP listen addresses, :443 and :80 by default. |
| `LIDZA_TLS_CACHE_DIR` |  | Stores certificates on disk instead of Postgres: one node only. |
| `LIDZA_TLS_DIRECTORY` |  | The ACME directory URL; Let's Encrypt by default, its staging directory for rehearsals. |
| `LIDZA_TLS_DOMAINS` |  | Lists the domains to serve, comma-separated; the first is the app's public name (APP_URL when unset). |
| `LIDZA_TLS_EMAIL` |  | The ACME account contact (expiry warnings). |
| `LIDZA_TLS_HTTP_ADDR` |  | LIDZA_TLS_ADDR and LIDZA_TLS_HTTP_ADDR are the HTTPS and HTTP listen addresses, :443 and :80 by default. |
| `SECURITY_CONTACT` |  | Where to report a vulnerability (mailto: or https:); with it the app serves /.well-known/security.txt. |
| `SECURITY_POLICY` |  | The URL of the security policy security.txt links to. |

## `pkg/credentials`

| Setting | Default | Description |
|---|---|---|
| `LIDZA_MASTER_KEY` |  | Carries the key where no file should exist: production. |
| `LIDZA_MASTER_KEY_OFF` |  | LIDZA_MASTER_KEY_OFF=1 makes config/master.key unreadable to the process: lidza test and verify set it, so tests never open the app's real credentials. A test that needs a key sets LIDZA_MASTER_KEY itself. |

## `pkg/devserver`

| Setting | Default | Description |
|---|---|---|
| `LIDZA_ADDR` |  | listen address |
| `LIDZA_FRONTEND_URL` |  | dev only: the dev server to proxy to |
| `LIDZA_MODE` |  | "dev" or unset |
| `LIDZA_SSR` |  | Enables per-request rendering: "1" starts the sidecar. |

## `pkg/middleware`

| Setting | Default | Description |
|---|---|---|
| `LIDZA_TRUSTED_PROXIES` |  | Names the proxies whose forwarded headers count: IPs and CIDRs, comma-separated, plus "loopback" (127.0.0.0/8, ::1) and "private" (10/8, 172.16/12, 192.168/16, fc00::/7). Empty trusts none. |

## `pkg/tracing`

| Setting | Default | Description |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` |  | OTLP/HTTP collector address (http://collector:4318); setting it turns tracing on. The other standard OTEL_* variables apply (OTEL_SERVICE_NAME, OTEL_TRACES_SAMPLER, OTEL_EXPORTER_OTLP_HEADERS, OTEL_RESOURCE_ATTRIBUTES). |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` |  | The traces endpoint alone, in place of OTEL_EXPORTER_OTLP_ENDPOINT. |
| `OTEL_SDK_DISABLED` |  | true turns tracing off even with an endpoint set. |
| `OTEL_SERVICE_NAME` |  | The service name on spans; default: the app's name. |

## `packs/admin`

| Setting | Default | Description |
|---|---|---|
| `ADMIN_USERS` |  | Lists who may open the admin pages. |

## `packs/analytics`

| Setting | Default | Description |
|---|---|---|
| `ANALYTICS_CLIENT_RPS` | `5` | Limits what one client may post per second. |
| `ANALYTICS_OTLP_URL` |  | An OTLP/HTTP logs endpoint (http://collector:4318/v1/logs). Empty keeps everything in Postgres. |
| `ANALYTICS_QUEUE` | `1024` | Bounds records waiting to be written; the rest are dropped and counted. |
| `ANALYTICS_RETENTION` | `720h` | Drops rows older than this on start; 0 keeps them. |

## `packs/audit`

| Setting | Default | Description |
|---|---|---|
| `AUDIT_RETENTION` | `8760h` | Drops records older than this, at start and then daily; 0 keeps them all. |

## `packs/auth`

| Setting | Default | Description |
|---|---|---|
| `AUTH_ACCESS_TTL` | `15m` | Bounds an access token; AUTH_REFRESH_TTL a session. |
| `AUTH_COOKIE_SECURE` |  | Marks cookies Secure; set it in production behind TLS. |
| `AUTH_ISSUER` | `lidza` | The JWT iss claim. |
| `AUTH_LOGIN_BURST` | `5` | AUTH_LOGIN_RPS and AUTH_LOGIN_BURST bound credential attempts per client address on the routes wrapped with Throttle (register, verify, forgot, reset) and, unless AUTH_SIGNIN_RPS is set, ThrottleSignIn. |
| `AUTH_LOGIN_RPS` | `1` | AUTH_LOGIN_RPS and AUTH_LOGIN_BURST bound credential attempts per client address on the routes wrapped with Throttle (register, verify, forgot, reset) and, unless AUTH_SIGNIN_RPS is set, ThrottleSignIn. |
| `AUTH_MIN_PASSWORD` | `10` | The floor ValidatePassword applies. |
| `AUTH_OWNER_CLAIM` |  | Makes the app's first account (FirstSubject, an admin) the account that presents a one-time token, not the first to sign in (owner.go). For a deployment anyone can reach before its owner. |
| `AUTH_OWNER_CLAIM_DIR` |  | Holds the token file (while unclaimed) and status.json; config/owner-claim by default. |
| `AUTH_OWNER_CLAIM_TOKEN` |  | The token when the deployment platform supplies it (at least 32 characters; the same on every node); generated into AUTH_OWNER_CLAIM_DIR/token otherwise. |
| `AUTH_REFRESH_TTL` | `720h` | AUTH_ACCESS_TTL bounds an access token; AUTH_REFRESH_TTL a session. |
| `AUTH_SECRET` | required | Signs access tokens; at least 32 bytes, the same on every node. Rotating it logs everyone out. |
| `AUTH_SIGNIN_BURST` |  | AUTH_SIGNIN_RPS and AUTH_SIGNIN_BURST bound password sign-ins per client address on the routes wrapped with ThrottleSignIn (Mount's login), apart from the other credential routes; zero uses AUTH_LOGIN_RPS and AUTH_LOGIN_BURST. |
| `AUTH_SIGNIN_RPS` |  | AUTH_SIGNIN_RPS and AUTH_SIGNIN_BURST bound password sign-ins per client address on the routes wrapped with ThrottleSignIn (Mount's login), apart from the other credential routes; zero uses AUTH_LOGIN_RPS and AUTH_LOGIN_BURST. |
| `AUTH_TOKEN_TTL` | `1h` | Bounds the one-time tokens IssueToken creates (email verification, password reset) unless the call says otherwise. |

## `packs/billing`

| Setting | Default | Description |
|---|---|---|
| `STRIPE_API_URL` | `https://api.stripe.com` | Stripe's API; tests point it at a fake. |
| `STRIPE_SECRET_KEY` |  | The Stripe secret key: sk_test_... in development, sk_live_... in production (in the credentials). |
| `STRIPE_WEBHOOK_SECRET` |  | Names the setting holding the endpoint's signing secret (whsec_...), which webhook.Stripe reads. |

## `packs/cache`

| Setting | Default | Description |
|---|---|---|
| `CACHE_MEMORY_MAX` | `10000` | Bounds the in-process backend's entries. |
| `CACHE_PREFIX` | `lidza:` | Namespaces keys, so several apps can share a server. |
| `CACHE_URL` | required | redis://host:port, valkey://..., or "memory". |

## `packs/db`

| Setting | Default | Description |
|---|---|---|
| `DATABASE_URL` | required | The connection string, e.g. postgres://user:pass@host/db or postgres:///db?host=/var/run/postgresql for a Unix socket (/tmp on macOS; LocalURL finds this machine's). |
| `DB_CONNECT_TIMEOUT` | `5s` | Bounds opening a connection and the first ping. |
| `DB_MAX_CONNS` | `10` | Bounds the pool; size it below Postgres max_connections divided by the number of app nodes. |
| `DB_MIGRATE` |  | Applies pending migrations from db/migrations at start. |
| `DB_MIGRATE_LOCK_TIMEOUT` | `5s` | Bounds how long each migration statement waits for a table lock before it gives up and retries, so a migration queued behind a long query never stalls the queries queued behind it. |
| `DB_MIGRATIONS_DIR` | `db/migrations` | Where the migration files live, relative to the working directory. |
| `DB_MIN_CONNS` | `0` | Keeps that many connections open while idle. |
| `PGHOST` |  | Read by the local database lookup (LocalURL) when DATABASE_URL is unset in development. |
| `PGPORT` |  | Read with PGHOST by the local database lookup. |

## `packs/hooks`

| Setting | Default | Description |
|---|---|---|
| `HOOKS_ALLOW_PRIVATE` |  | Lets endpoints point at loopback and private networks: for development and tests only. |
| `HOOKS_DISABLE_AFTER` | `20` | Disables an endpoint after this many deliveries in a row that failed every attempt. |
| `HOOKS_MAX_ENDPOINTS` | `20` | Bounds the endpoints of one owner. |
| `HOOKS_RETENTION` | `720h` | Drops deliveries older than this, daily; 0 keeps them. |
| `HOOKS_TIMEOUT` | `10s` | Bounds one delivery attempt, connection to response. |

## `packs/i18n`

| Setting | Default | Description |
|---|---|---|
| `I18N_DEFAULT` | `en` | The locale used when nothing better matches. |
| `I18N_TIMEZONE` | `UTC` | The IANA zone used when the request names none. |

## `packs/jobs`

| Setting | Default | Description |
|---|---|---|
| `JOBS_DRAIN` | `1s` | How long running jobs may finish at shutdown before they are cancelled and released back to pending, to run again at once on another node or after the restart (the attempt is not counted). The shutdown deadline caps it; 0 releases at once. |
| `JOBS_MAX_ATTEMPTS` | `5` | The default retry budget per job. |
| `JOBS_POLL` | `1s` | How often idle workers look for work. |
| `JOBS_STALE` | `10m` | How long a running job may go without finishing before another worker takes it over (a crashed node). |
| `JOBS_TIMEOUT` | `5m` | Bounds one execution. |
| `JOBS_WORKERS` | `4` | Bounds concurrent jobs on this node; 0 disables running (enqueue only, for nodes that just serve HTTP). |

## `packs/llm`

| Setting | Default | Description |
|---|---|---|
| `EMBED_API_KEY` |  | Authenticates EMBED_PROVIDER (openai, google; optional for compatible). When EMBED_PROVIDER is the chat provider, empty uses LLM_API_KEY. |
| `EMBED_BASE_URL` |  | Overrides EMBED_PROVIDER's API base, as LLM_BASE_URL does for chat; required for compatible. When EMBED_PROVIDER is the chat provider, empty uses LLM_BASE_URL. |
| `EMBED_MODEL` |  | The embedding model; each provider has a default. |
| `EMBED_PROVIDER` |  | Serves Embed: openai, google, ollama, compatible or fake, or none to turn embeddings off. Empty uses the chat provider (LLM_PROVIDER), which cannot embed when it is anthropic. |
| `LLM_API_KEY` |  | Authenticates anthropic, openai and google. |
| `LLM_BASE_URL` |  | Overrides the provider's API base (a proxy, a region, Ollama on another host: http://127.0.0.1:11434 by default). |
| `LLM_EMBED_MODEL` |  | The older name of EMBED_MODEL (LLM_EMBED_MODEL), read when EMBED_MODEL is empty. |
| `LLM_MAX_ATTEMPTS` | `3` | How many times a call is tried on 429, 5xx or a network error, with backoff (1s, 2s, 4s) and Retry-After honoured. |
| `LLM_MAX_TOKENS` | `1024` | Bounds a reply unless the request says otherwise. |
| `LLM_MAX_TOOL_ROUNDS` | `8` | Bounds Run: how many times the model may call tools before it must answer. |
| `LLM_MODEL` |  | The chat model; each provider has a default. |
| `LLM_PROVIDER` | `fake` | Fake (default; scripted replies for tests and a first run), ollama (a local model), anthropic, openai, google (Gemini) or compatible (any server speaking the OpenAI API: llama.cpp's llama-server, vLLM, LM Studio; LLM_BASE_URL required, the key optional). |
| `LLM_TIMEOUT` | `60s` | Bounds one attempt of one call. |

## `packs/mail`

| Setting | Default | Description |
|---|---|---|
| `MAIL_API_KEY` |  | Authenticates the HTTP providers. |
| `MAIL_BASE_URL` |  | Overrides a provider's API base outright (a proxy); it wins over MAIL_REGION. |
| `MAIL_DOMAIN` |  | The sending domain (Mailgun). |
| `MAIL_FROM` |  | The default sender, "Name <address>" or an address. |
| `MAIL_MAX_ATTACHMENTS` | `10` | Bounds the number of files per message. |
| `MAIL_MAX_ATTACHMENT_BYTES` | `10485760` | Bounds the total decoded attachment bytes. |
| `MAIL_MAX_ATTEMPTS` | `5` | Bounds delivery retries through the jobs pack. |
| `MAIL_MAX_RECIPIENTS` | `50` | Bounds the total To, Cc and Bcc addresses per message. |
| `MAIL_PROVIDER` | `log` | Log (default), outbox (record only; tests), smtp, mailgun, sendgrid, postmark or resend. |
| `MAIL_REGION` |  | Picks a provider's regional API: "eu" for Mailgun (api.eu.mailgun.net) and SendGrid (api.eu.sendgrid.com); "us", the default, otherwise. |
| `MAIL_SMTP_HOST` |  | MAIL_SMTP_HOST and MAIL_SMTP_PORT address the server; the port defaults to 587 (465 with MAIL_SMTP_SECURITY "tls", 25 with "none"). |
| `MAIL_SMTP_PASSWORD` |  | MAIL_SMTP_USERNAME and MAIL_SMTP_PASSWORD authenticate (PLAIN); both empty sends without authentication. |
| `MAIL_SMTP_PORT` |  | MAIL_SMTP_HOST and MAIL_SMTP_PORT address the server; the port defaults to 587 (465 with MAIL_SMTP_SECURITY "tls", 25 with "none"). |
| `MAIL_SMTP_SECURITY` | `starttls` | "starttls" (the default: the connection is upgraded, and refused when the server cannot), "tls" (encrypted from the start, usually port 465) or "none" (plain text; a local relay only). |
| `MAIL_SMTP_TIMEOUT` | `30s` | Bounds connection setup and sending, including a stalled peer. |
| `MAIL_SMTP_URL` |  | smtp://user:pass@host:587 (STARTTLS) or smtps://user:pass@host:465 (TLS). The SMTP fields below are the same setting in parts; the URL wins when both are set. |
| `MAIL_SMTP_USERNAME` |  | MAIL_SMTP_USERNAME and MAIL_SMTP_PASSWORD authenticate (PLAIN); both empty sends without authentication. |
| `MAIL_TEMPLATES` | `mail` | Holds <name>.txt.tmpl and <name>.html.tmpl. |

## `packs/realtime`

| Setting | Default | Description |
|---|---|---|
| `REALTIME_BUFFER` | `64` | The per-connection outbound queue; a client that falls this far behind is disconnected rather than growing memory. |
| `REALTIME_BUS_URL` |  | A Valkey or Redis URL (redis://host:6379). Empty keeps fan-out inside the node, which is right for one node only. |
| `REALTIME_MAX_CONNS` | `10000` | Caps open WebSockets on this node; beyond it, 503. |
| `REALTIME_MAX_TOPICS` | `100` | Caps the topics one connection holds; further subscriptions are ignored. Topics longer than 256 bytes are refused. |
| `REALTIME_WRITE_TIMEOUT` | `5s` | Bounds each send to a client. |

## `packs/storage`

| Setting | Default | Description |
|---|---|---|
| `STORAGE_ACCESS_KEY` |  | STORAGE_ACCESS_KEY and STORAGE_SECRET_KEY authenticate; keep them in the credentials. |
| `STORAGE_ACCOUNT_ID` |  | The Cloudflare account of an R2 bucket. |
| `STORAGE_BUCKET` |  | The S3 bucket. |
| `STORAGE_DIR` | `storage` | Holds the files of the local provider. |
| `STORAGE_ENDPOINT` | `https://s3.amazonaws.com` | The service: https://s3.amazonaws.com (default), https://<account>.r2.cloudflarestorage.com, http://127.0.0.1:9000 for MinIO. |
| `STORAGE_MAX_SIZE` | `104857600` | Bounds one Put. |
| `STORAGE_PATH_STYLE` |  | Puts the bucket in the path (http://host/bucket/key) instead of the host; on by default for every endpoint but AWS. |
| `STORAGE_PREFIX` |  | A folder every key lives under ("myapp" stores avatars/1.png as myapp/avatars/1.png), for a bucket several apps share. The app's keys never carry it: Put, Get, List and the rest add it, and the objects they return have it removed. |
| `STORAGE_PROVIDER` | `local` | Local (default: files under STORAGE_DIR), s3 (Amazon S3, or any S3-compatible service at STORAGE_ENDPOINT), or a named S3-compatible service whose address follows from STORAGE_REGION or STORAGE_ACCOUNT_ID: r2 (Cloudflare R2), spaces (DigitalOcean Spaces), b2 (Backblaze B2), gcs (Google Cloud Storage with HMAC keys), minio (MinIO, RustFS or another server at STORAGE_ENDPOINT). |
| `STORAGE_PUBLIC_URL` |  | Where objects are readable without signing (a CDN, a public bucket); URL returns it plus the key. |
| `STORAGE_REGION` | `us-east-1` | Signs the requests; us-east-1 by default (R2 and MinIO accept auto or us-east-1). |
| `STORAGE_SECRET_KEY` |  | STORAGE_ACCESS_KEY and STORAGE_SECRET_KEY authenticate; keep them in the credentials. |
| `STORAGE_TIMEOUT` | `60s` | Bounds one operation. |
