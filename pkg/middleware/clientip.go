package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// EnvTrustedProxies names the proxies whose forwarded headers count:
// IPs and CIDRs, comma-separated, plus "loopback" (127.0.0.0/8, ::1) and
// "private" (10/8, 172.16/12, 192.168/16, fc00::/7). Empty trusts none.
const EnvTrustedProxies = "LIDZA_TRUSTED_PROXIES"

// maxHops and maxForwardedLen bound what a request may make the resolver
// read.
const (
	maxHops         = 32
	maxForwardedLen = 4096
)

// Proxies is the set of trusted proxies ClientIdentity resolves through.
type Proxies struct {
	prefixes []netip.Prefix
}

// ParseProxies reads LIDZA_TRUSTED_PROXIES' format. An entry that is
// neither an address, a CIDR nor a shorthand is an error, so a typo is not
// silently ignored.
func ParseProxies(s string) (*Proxies, error) {
	p := &Proxies{}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		switch strings.ToLower(f) {
		case "":
			continue
		case "loopback":
			p.prefixes = append(p.prefixes, netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"))
			continue
		case "private":
			for _, c := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"} {
				p.prefixes = append(p.prefixes, netip.MustParsePrefix(c))
			}
			continue
		}
		if strings.Contains(f, "/") {
			pre, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, fmt.Errorf("%s: %q is not an address, a CIDR, loopback or private", EnvTrustedProxies, f)
			}
			p.prefixes = append(p.prefixes, pre.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not an address, a CIDR, loopback or private", EnvTrustedProxies, f)
		}
		a = a.Unmap()
		p.prefixes = append(p.prefixes, netip.PrefixFrom(a, a.BitLen()))
	}
	return p, nil
}

// Empty reports whether no proxy is trusted.
func (p *Proxies) Empty() bool { return p == nil || len(p.prefixes) == 0 }

func (p *Proxies) trusted(a netip.Addr) bool {
	if p == nil {
		return false
	}
	for _, pre := range p.prefixes {
		if pre.Contains(a) {
			return true
		}
	}
	return false
}

// parseHop reads one address: "203.0.113.7", "203.0.113.7:4711",
// "[2001:db8::1]:443", "2001:db8::1", "fe80::1%eth0"; IPv4-mapped IPv6 is
// unmapped. Anything else ("unknown", an obfuscated "_id") is not one.
func parseHop(s string) (netip.Addr, bool) {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if s == "" || len(s) > 64 {
		return netip.Addr{}, false
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.WithZone("").Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().WithZone("").Unmap(), true
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		if a, err := netip.ParseAddr(s[1 : len(s)-1]); err == nil {
			return a.WithZone("").Unmap(), true
		}
	}
	return netip.Addr{}, false
}

// hops are the forwarded addresses, nearest last: X-Forwarded-For when
// sent, else RFC 7239 Forwarded's for= values. One header is used, never
// a mix; at most maxForwardedLen bytes and the nearest maxHops entries.
func hops(r *http.Request) []string {
	var list []string
	if v := r.Header.Values("X-Forwarded-For"); len(v) > 0 {
		list = strings.Split(bounded(strings.Join(v, ",")), ",")
	} else if v := r.Header.Values("Forwarded"); len(v) > 0 {
		for _, el := range strings.Split(bounded(strings.Join(v, ",")), ",") {
			found := ""
			for _, pair := range strings.Split(el, ";") {
				k, val, ok := strings.Cut(strings.TrimSpace(pair), "=")
				if ok && strings.EqualFold(strings.TrimSpace(k), "for") {
					found = val
				}
			}
			list = append(list, found)
		}
	}
	if len(list) > maxHops {
		list = list[len(list)-maxHops:]
	}
	return list
}

// bounded keeps the nearest maxForwardedLen bytes of a header: the end,
// which the trusted proxies appended.
func bounded(s string) string {
	if len(s) <= maxForwardedLen {
		return s
	}
	s = s[len(s)-maxForwardedLen:]
	if i := strings.IndexByte(s, ','); i >= 0 {
		return s[i+1:]
	}
	return ""
}

// Resolve returns the client's address. A peer that is not a trusted
// proxy is the client, whatever its headers say. From a trusted peer the
// forwarded addresses are read nearest first: trusted proxies are
// skipped, and the first address that is not one is the client. A
// malformed entry stops the walk at the trusted hop before it; when
// every hop is trusted, the farthest is the client.
func (p *Proxies) Resolve(r *http.Request) string {
	peer, ok := remoteAddr(r)
	if !ok {
		return r.RemoteAddr
	}
	if !p.trusted(peer) {
		return peer.String()
	}
	client := peer
	list := hops(r)
	for i := len(list) - 1; i >= 0; i-- {
		a, ok := parseHop(list[i])
		if !ok {
			break
		}
		client = a
		if !p.trusted(a) {
			break
		}
	}
	return client.String()
}

func remoteAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return parseHop(host)
}

type clientIPKey struct{}

// ClientIdentity resolves each request's client address once (Resolve)
// and puts it in the context, where ClientIP, the rate limiter, the auth
// throttles and the request log read it. lidza.App installs it with
// LIDZA_TRUSTED_PROXIES; with none trusted it is the connection's address.
func ClientIdentity(p *Proxies) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := p.Resolve(r)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip)))
		})
	}
}

// ClientIP is the request's client address: the one ClientIdentity
// resolved (through the trusted proxies), else the connection's. It is
// the default rate-limit key; never read X-Forwarded-For by hand.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok {
		return ip
	}
	if a, ok := remoteAddr(r); ok {
		return a.String()
	}
	return r.RemoteAddr
}
