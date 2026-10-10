package middleware

import (
	"net/http"
	"net/netip"
	"testing"
)

// FuzzResolve: forwarding headers are written by clients. An untrusted
// peer is always the client, whatever it sends; through a trusted proxy
// the result is an address, never header text.
func FuzzResolve(f *testing.F) {
	f.Add("10.0.0.1:443", "203.0.113.7, 10.0.0.2", "")
	f.Add("10.0.0.1:443", "", `for="[2001:db8::1]:80";proto=https, for=unknown`)
	f.Add("198.51.100.9:1", "1.2.3.4", "for=5.6.7.8")
	proxies, err := ParseProxies("private, loopback")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, remote, xff, fwd string) {
		r, _ := http.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if fwd != "" {
			r.Header.Set("Forwarded", fwd)
		}
		got := proxies.Resolve(r)
		peer, ok := remoteAddr(r)
		if !ok {
			if got != remote {
				t.Fatalf("unparsable peer %q resolved to %q", remote, got)
			}
			return
		}
		if !proxies.trusted(peer) && got != peer.String() {
			t.Fatalf("untrusted peer %v resolved to %q", peer, got)
		}
		if _, err := netip.ParseAddr(got); err != nil {
			t.Fatalf("resolved to %q, not an address", got)
		}
	})
}
