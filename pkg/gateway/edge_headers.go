package gateway

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// NativeEdgeHeaders is the explicit wire marker for a trusted TLS terminator
// unable to call Forward. It must overwrite every listed header from actual
// connection/request properties, never incoming forwarding headers. Verify must
// opt in with Policy.NativeEdges for the pinned immediate peer. Relays use the
// normal protocol; native edge headers cannot assert a prior gateway chain.
const NativeEdgeHeaders = "edge-headers-v1"

// EdgeHeaderNames is the complete native-terminator header list. A proxy must
// overwrite these fields and strip Header itself before creating its marker.
func EdgeHeaderNames() []string {
	return []string{"Lidza-Gateway-Tls-Version", "Lidza-Gateway-Authority", "Lidza-Gateway-Server-Name", "Lidza-Gateway-Method", "Lidza-Gateway-Target", "Lidza-Gateway-Issued", "Lidza-Gateway-Nonce"}
}

func nativeEnvelope(r *http.Request) (string, error) {
	names := EdgeHeaderNames()
	fields := make([]string, len(names))
	for i, name := range names {
		values := r.Header.Values(name)
		if len(values) != 1 || len(values[0]) > 2048 {
			return "", errEvidence
		}
		fields[i] = values[0]
	}
	version := uint16(0)
	switch fields[0] {
	case "tls1.3":
		version = tls.VersionTLS13
	case "tls1.2":
		version = tls.VersionTLS12
	default:
		return "", errEvidence
	}
	issued, err := strconv.ParseInt(fields[5], 10, 64)
	if err != nil {
		return "", errEvidence
	}
	// Native proxies commonly provide a generated UUID. Require canonical UUIDv4
	// with 122 random bits, not a client-provided request-id or timestamp.
	nonce := fields[6]
	if len(nonce) != 36 || nonce[8] != '-' || nonce[13] != '-' || nonce[18] != '-' || nonce[23] != '-' || nonce[14] != '4' || !strings.ContainsRune("89ab", rune(nonce[19])) {
		return "", errEvidence
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(nonce, "-", ""))
	if err != nil || len(raw) != 16 {
		return "", errEvidence
	}
	data, err := json.Marshal(envelope{Version: 1, TLSVersion: version, Authority: fields[1], ServerName: fields[2], Method: fields[3], Target: fields[4], Issued: issued, Nonce: base64.RawURLEncoding.EncodeToString(raw)})
	if err != nil {
		return "", errEvidence
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func stripHeaders(h http.Header) {
	h.Del(Header)
	for _, name := range EdgeHeaderNames() {
		h.Del(name)
	}
}
