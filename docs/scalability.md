# Scalability

Līdza is scalable by default: the rules below hold for every package from
the first commit, not as a later phase. Source: "Scalable-by-Default
Architecture Primitives" in `docs/research/2026-09-lidza-design-notes.md`.
Target: 100 million registered users, about one million concurrent, on
stateless Go nodes behind a load balancer, with Rust compute pools and a
distributed state tier (Postgres, Valkey, a queue).

## Primitives

### 1. Stateless control plane

- No request state lives in process memory between requests: no session
  maps, no per-user caches, no in-memory queues. Sessions are signed
  tokens (PASETO or JWT) or rows in Valkey. State goes to Postgres, Valkey
  or the queue.
- Any Go node can serve any request; nodes are added and removed without
  draining state. WebSocket fan-out goes through Valkey pub/sub, never
  node-to-node.
- Package-level variables are constants, registries filled at init, or
  guarded pools with a bound. `lidza check` flags global mutable maps and
  unbounded goroutine spawns (Phase 5).

### 2. Bounded compute

- Rust runs in a bounded pool of pre-warmed wazero instances
  (`pkg/engine/pool.go`, Phase 5). The pool size is configuration, never
  "one per request".
- Every call into the pool carries a `context.Context` with a deadline; a
  slow computation is cancelled, its instance recycled, and the request gets
  an error. Memory stays flat under load.
- Heavy allocation (media, geo, graph, vectors) belongs in Rust, so the Go
  garbage collector never sees it and p99 latency stays low.

### 3. Bounded I/O

- Database access is `pgxpool` with an explicit maximum, and queries are
  generated (`sqlc`) with static shapes. The pool size is part of
  `lidza.json`, and `/readyz` reports pool health.
- Every outbound call (database, Valkey, HTTP, queue) has a timeout from
  the request context. No call without a deadline.
- Request bodies are size-limited; every handler has a timeout; long work
  goes to the queue.

### 4. Observability and resilience

- Every app exposes `/metrics` (Prometheus, OpenTelemetry), `/healthz`
  (liveness) and `/readyz` (readiness that checks the pools) from
  `pkg/telemetry` (Phase 5).
- Token-bucket rate limiting on routes and circuit breakers on outbound
  dependencies shed load before saturation.
- `lidza benchmark` runs k6 scenarios from `benchmarks/` against `lidza dev`
  and compares heap profiles before and after; flat memory is a release
  gate.

## Rules for every change

Apply to every Go package, Rust module, pack and template, now.

1. No mutable package-level state except a bounded, guarded pool or a
   registry that is complete after init.
2. Every goroutine is owned: started with a context, stopped when it is
   cancelled, and counted (a pool or a `WaitGroup`). No fire-and-forget.
3. Every blocking call takes a context and respects its deadline.
4. Every collection that grows with traffic has a bound and an eviction
   rule. A map keyed by user, session or connection is a bug unless it is
   the connection registry of one node and drops entries on close.
5. Every handler is idempotent about node identity: nothing depends on the
   same node seeing the next request.
6. Configuration sets every limit (pool sizes, timeouts, rate limits, body
   sizes); defaults are conservative and documented in `lidza.json`.
7. Every pack ships the k6 scenario that exercises it, and Phase 5's memory
   check must stay flat with the pack enabled.

## Status by phase

| Primitive | State |
|---|---|
| Stateless handlers, `net/http` router, no global state | in force since Phase 1: `pkg/router` holds only the mux; `pkg/devserver` is dev-only |
| Dev-only process supervision bounded by contexts | Phase 1 (`pkg/devserver`) |
| Owned goroutines, contexts on every command | Phase 1 and 2 (`diag`, `devserver`) |
| Middleware pipeline: timeouts, body limits, request ids | in force since Phase 3 (`pkg/middleware`) |
| Sessions as tokens or Valkey rows | Phase 6 (`auth` pack) |
| `pgxpool` bounds, generated queries | in force since Phase 4 (`db` pack) |
| Bounded wazero pool with per-call deadlines | in force since Phase 4 (`pkg/engine`) |
| `/metrics`, `/healthz`, `/readyz` | Phase 5 |
| Rate limiting, circuit breakers | Phase 5 |
| `lidza check` rules for global state and unbounded goroutines | Phase 5 |
| `lidza benchmark`, flat-memory gate | Phase 5 |
| Valkey pub/sub fan-out for WebSockets | in force since Phase 4 (`realtime` pack, `REALTIME_BUS_URL`) |
| Queue-backed background work | Phase 6 (`jobs` pack) |

## Capacity reference

From the design notes, for planning, not a promise:

| Metric | Target | Sizing |
|---|---|---|
| Peak concurrent users | 1,000,000 | 10 to 15 Go nodes (8 vCPU, 16 GiB) |
| Peak API throughput | 150,000 req/s | CDN serves about 80 percent; Go nodes see about 30,000 req/s |
| Rust compute | 10,000 ops/s | 5 to 8 workers (16 vCPU, 32 GiB) |
| Database | 20,000 queries/s | one primary (32 vCPU, 128 GiB), three read replicas, Valkey in front |
