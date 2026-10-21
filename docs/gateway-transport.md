# Verified gateway transport

Package `github.com/agim/lidza/pkg/gateway` supplies an explicit opt-in protocol
for applications behind a trusted TLS terminator. It does not modify `r.TLS` or
trust `X-Forwarded-Proto`, source IP, loopback, bearer headers or backend TLS as
proof of the original connection. Existing direct-TLS applications are unchanged.

## Trust boundary

All hops use TLS 1.3 with verified server certificates and required verified
client certificates. Use `ServerTLS` and `ClientTransport`; no plaintext fallback,
unverified certificates, environment proxies or HTTP redirects. Set read-header,
body, handler and shutdown bounds on the HTTP server. `App.PrivateTLS` accepts a `ServerTLS` config and serves the normal `LIDZA_ADDR`
listener as private mTLS, with no plaintext listener. Alternatively, set
`App.PrivateTLSAddr` to a separate private address/port: that listener serves the
same handler under mTLS while ordinary internal HTTP remains on LIDZA_ADDR.
Protected protocol routes must enforce `Verify` on both; HTTP never receives
verified evidence. This supports internal HTTP services alongside a dedicated
protected ingress. The mode is mutually exclusive
with ACME TLS. This only protects the backend hop: install the gateway verifier
on protocol routes before using original-client evidence. Private listeners must not
be reachable as a substitute unauthenticated public endpoint.

The original public edge is trusted to capture its actual incoming TLS version,
SNI, authority, method and target. Every relay is trusted to verify its incoming
hop and forward only verified context over another protected hop. A compromised
authorized gateway can lie: this is explicit delegated gateway trust, not
end-to-end client TLS, TLS passthrough or a client-signed attestation.

`Policy.Peers` pins authorized immediate peer SPKI hashes in addition to normal
CA verification. `Delegates` lists authorized earlier gateways asserted by that
peer. Both must be explicit. `Authorize` must validate the exact original
canonical authority using its bounded context; nil allows nothing. SNI must match
the authority host. This prevents a trusted proxy from serving an unapproved
request authority and rejects domain fronting.

`Policy.Claim` must atomically reserve each nonce across all replicas. It gets a
2-second context and a 30-second TTL. Store outages fail closed. `Claims` adapts
a cache pack `Incr` counter; use a stable app-specific prefix and production
Valkey/Redis, not independent memory caches. Never implement Claim as Get then
Set. Keys are public random nonce identifiers, not authentication secrets.

## API and wire protocol

At a Go public edge, `Edge(authorize)` discards incoming transport headers and
captures actual direct TLS in context. A relay/application uses `Verify(policy)`.
A reverse proxy calls `Forward(out, in)` after changing only the upstream URL
scheme/address; preserve public Host, method and request target. Use the returned
`ClientTransport` to ensure verified mTLS on the outgoing hop. Forwarding a header
alone does not establish trust.

Applications use `From(ctx)` for a read-only evidence copy and
`RequireTLS13(request)` for verified original TLS 1.3, after either middleware.
The latter deliberately has no fallback to backend `r.TLS`. Preserve application
identity/signatures, protocol nonces, authorization, idempotency and replay checks.

The `Lidza-Gateway-Transport` value is base64url canonical compact JSON, version1.
It binds original TLS version/SNI/authority and current method/target, a generated
192-bit nonce, issue time and at most three previous gateway pins. Only one value
is allowed, at most4096 bytes. Unknown fields, duplicate/noncanonical JSON,
versions, malformed values, request changes and unapproved chains fail closed.
Evidence expires after15 seconds, permits at most1 second future clock skew, and
cannot be reused even over another authenticated connection. Keep clocks synced.
Each relay consumes its incoming nonce and creates a new nonce for its outgoing
hop. Metadata and request body share the same authenticated encrypted HTTP stream;
this is not a detachable signed proof and must never cross a plaintext hop.

## Native TLS terminators

A native proxy may use `edge-headers-v1` only when its immediate client-certificate
pin is explicitly listed in `Policy.NativeEdges` and `Peers`. It cannot assert
prior gateways. It must overwrite every native field from real connection/request
properties, including any client-supplied value. A Caddy2.10 example:

```caddyfile
reverse_proxy https://127.0.0.1:8444 {
 header_up Lidza-Gateway-Transport edge-headers-v1
 header_up Lidza-Gateway-Tls-Version {http.request.tls.version}
 header_up Lidza-Gateway-Authority {http.request.hostport}
 header_up Lidza-Gateway-Server-Name {http.request.tls.server_name}
 header_up Lidza-Gateway-Method {http.request.method}
 header_up Lidza-Gateway-Target {http.request.uri}
 header_up Lidza-Gateway-Issued {time.now.unix}
 header_up Lidza-Gateway-Nonce {http.request.uuid}
 transport http {
  tls_server_name relay.internal
  tls_trust_pool file /etc/gateway/relay-ca.pem
  tls_client_auth /etc/gateway/edge.pem /etc/gateway/edge-key.pem
 }
}
```

Native nonce must be a newly generated canonical UUIDv4. Do not use an incoming
request-id. Do not combine a wildcard delete of these headers with these sets:
Caddy applies wildcard deletion after setting them. Every recognized field above
is overwritten; other client headers never create transport evidence.

## Lifecycle and validation

Keep keys/CA private material in the deployment's encrypted credential store,
not manifests, build arguments, logs or public settings. For rotation first add
new CA trust and peer/delegate pins on receivers; issue/switch sender credentials;
drain old connections and then remove old pins/CA. Retain nonce storage and its
prefix throughout rotation. There is no TOFU, unknown-peer grace or fallback to
forwarding headers. Rollback must preserve overlapping credentials or fail closed.

`go test -race ./pkg/gateway` exercises real TLS edge/relay/app traffic plus
forgery, changed requests, freshness/replay, unknown peers, bounds, store failure
and overlapping-key rotation. Set `LIDZA_TEST_CADDY=/path/to/caddy` for the actual
native-edge test. It runs on isolated loopback listeners with a test CA and no
production configuration, DNS or ACME. Original TLS1.2 is rejected by the app even
though both backend hops use TLS1.3. This is framework evidence; an orchestrator
must separately provision and test all protected hops before enabling the mode.
