package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseProxies(t *testing.T) {
	p, err := ParseProxies(" loopback, private ,203.0.113.10, 198.51.100.0/24, 2001:db8::/32, ::ffff:192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "172.20.0.1": true, "192.168.1.1": true, "fd00::1": true,
		"203.0.113.10": true, "203.0.113.11": false, "198.51.100.77": true, "2001:db8::5": true, "192.0.2.1": true,
		"8.8.8.8": false, "172.32.0.1": false,
	} {
		a, _ := parseHop(addr)
		if got := p.trusted(a); got != want {
			t.Errorf("%s trusted %v, want %v", addr, got, want)
		}
	}
	for _, bad := range []string{"localhost", "10.0.0.0/33", "1.2.3", "all"} {
		if _, err := ParseProxies(bad); err == nil || !strings.Contains(err.Error(), EnvTrustedProxies) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if p, _ := ParseProxies(""); !p.Empty() {
		t.Error("empty trusts something")
	}
}

// TestResolve: a peer that is not trusted is the client whatever it
// sends; through trusted proxies the nearest untrusted hop is; spoofed
// left parts, malformed entries, Forwarded, ports, brackets, zones and
// mapped addresses are handled; huge headers are bounded.
func TestResolve(t *testing.T) {
	p, _ := ParseProxies("loopback,10.0.0.0/8")
	for _, c := range []struct {
		name, remote string
		headers      map[string]string
		want         string
	}{
		{"no proxy", "198.51.100.7:5000", nil, "198.51.100.7"},
		{"untrusted peer spoofs", "198.51.100.7:5000", map[string]string{"X-Forwarded-For": "1.2.3.4"}, "198.51.100.7"},
		{"untrusted peer spoofs Forwarded", "198.51.100.7:5000", map[string]string{"Forwarded": "for=1.2.3.4"}, "198.51.100.7"},
		{"caddy on loopback", "127.0.0.1:40000", map[string]string{"X-Forwarded-For": "203.0.113.9"}, "203.0.113.9"},
		{"client spoofs left part", "127.0.0.1:40000", map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.9"}, "203.0.113.9"},
		{"two trusted proxies", "127.0.0.1:40000", map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.9, 10.0.0.5"}, "203.0.113.9"},
		{"all trusted", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "10.0.0.9, 10.0.0.5"}, "10.0.0.9"},
		{"malformed stops at trusted", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "203.0.113.9, garbage, 10.0.0.5"}, "10.0.0.5"},
		{"trusted peer, no header", "127.0.0.1:1", nil, "127.0.0.1"},
		{"port and brackets", "[::1]:1", map[string]string{"X-Forwarded-For": "[2001:db8::7]:443"}, "2001:db8::7"},
		{"forwarded quoted v6", "127.0.0.1:1", map[string]string{"Forwarded": `for="[2001:db8::7]:4711";proto=https, for=10.0.0.3`}, "2001:db8::7"},
		{"forwarded obfuscated", "127.0.0.1:1", map[string]string{"Forwarded": "for=_hidden"}, "127.0.0.1"},
		{"zone and mapped", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "::ffff:203.0.113.9"}, "203.0.113.9"},
		{"xff wins over forwarded", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "203.0.113.9", "Forwarded": "for=6.6.6.6"}, "203.0.113.9"},
		{"huge header bounded", "127.0.0.1:1", map[string]string{"X-Forwarded-For": strings.Repeat("6.6.6.6, ", 5000) + "203.0.113.9"}, "203.0.113.9"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		if got := p.Resolve(r); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// Nothing trusted: headers never count.
	none, _ := ParseProxies("")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := none.Resolve(r); got != "127.0.0.1" {
		t.Errorf("unconfigured trusted a header: %s", got)
	}
}

// TestClientIdentityRateLimit: behind a trusted proxy, two clients get
// their own buckets; a client cannot escape its bucket by spoofing.
func TestClientIdentityRateLimit(t *testing.T) {
	p, _ := ParseProxies("loopback")
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ClientIdentity(p), RateLimit(RateLimitOptions{RPS: 0.001, Burst: 1}))
	send := func(xff string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:9999"
		r.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if send("203.0.113.1") != 200 || send("203.0.113.1") != 429 {
		t.Fatal("first client not limited")
	}
	if send("203.0.113.2") != 200 {
		t.Fatal("second client shared the first's bucket")
	}
	if send("9.9.9.9, 203.0.113.1") != 429 {
		t.Fatal("a spoofed left part escaped the bucket")
	}
}

// TestLoggerFields: the request line has the resolved client and the
// path only, bounded and escaped; never the query, cookies or the
// Authorization header.
func TestLoggerFields(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	p, _ := ParseProxies("loopback")
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }), ClientIdentity(p), Logger(log))
	r := httptest.NewRequest("GET", "/.env?token=s3cr3t-query", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.Header.Set("Authorization", "Bearer s3cr3t-bearer")
	r.Header.Set("Cookie", "lidza_access=s3cr3t-cookie")
	h.ServeHTTP(httptest.NewRecorder(), r)
	line := buf.String()
	for _, want := range []string{`"client_ip":"203.0.113.9"`, `"path":"/.env"`, `"status":404`, `"method":"GET"`} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %s in %s", want, line)
		}
	}
	if strings.Contains(line, "s3cr3t") {
		t.Errorf("a secret reached the log: %s", line)
	}
	buf.Reset()
	r = httptest.NewRequest("GET", "/x", nil)
	r.URL.Path = "/" + strings.Repeat("a", 2000) + "\n\x1b[31m"
	h.ServeHTTP(httptest.NewRecorder(), r)
	line = buf.String()
	if strings.Count(line, "\n") != 1 || !strings.Contains(line, "...(cut)") || len(line) > 1000 {
		t.Errorf("long path not bounded: %d bytes", len(line))
	}
	buf.Reset()
	r = httptest.NewRequest("GET", "/x", nil)
	r.URL.Path = "/a\nb\x1b"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if line := buf.String(); !strings.Contains(line, `\\u000a`) || !strings.Contains(line, `\\u001b`) {
		t.Errorf("control characters not escaped: %s", line)
	}
}
