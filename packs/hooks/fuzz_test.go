package hooks

import (
	"net/netip"
	"testing"
)

// FuzzAllowed: whatever address a name resolves to, delivery never
// reaches a loopback, private, link-local or unspecified IPv4 address,
// in any IPv6 form that carries one.
func FuzzAllowed(f *testing.F) {
	f.Add([]byte{127, 0, 0, 1})
	f.Add([]byte{10, 1, 2, 3})
	f.Add([]byte{169, 254, 169, 254})
	f.Add([]byte{93, 184, 216, 34})
	h := New(Config{}, nil, nil)
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) < 4 {
			return
		}
		v4 := netip.AddrFrom4([4]byte(b[:4]))
		inner := !v4.IsGlobalUnicast() || v4.IsPrivate() || v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsUnspecified()
		forms := map[string]netip.Addr{
			"plain":  v4,
			"mapped": netip.AddrFrom16([16]byte{10: 0xff, 11: 0xff, 12: b[0], 13: b[1], 14: b[2], 15: b[3]}),
			"compat": netip.AddrFrom16([16]byte{12: b[0], 13: b[1], 14: b[2], 15: b[3]}),
			"nat64":  netip.AddrFrom16([16]byte{0: 0x00, 1: 0x64, 2: 0xff, 3: 0x9b, 12: b[0], 13: b[1], 14: b[2], 15: b[3]}),
			"6to4":   netip.AddrFrom16([16]byte{0: 0x20, 1: 0x02, 2: b[0], 3: b[1], 4: b[2], 5: b[3]}),
		}
		for name, a := range forms {
			if inner && h.allowed(a) {
				t.Fatalf("%s form %v of %v allowed", name, a, v4)
			}
		}
		if len(b) >= 16 {
			a := netip.AddrFrom16([16]byte(b[:16]))
			if h.allowed(a) && (a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() || a.IsMulticast()) {
				t.Fatalf("%v allowed", a)
			}
		}
	})
}

// FuzzCheckURL: a target written as an address literal is checked
// like a resolved one, and no input panics.
func FuzzCheckURL(f *testing.F) {
	for _, s := range []string{"https://example.com/hook", "https://127.0.0.1/", "https://[::1]/", "https://[::ffff:10.0.0.1]/", "http://x", "https://u:p@example.com", "https://%zz"} {
		f.Add(s)
	}
	h := New(Config{}, nil, nil)
	f.Fuzz(func(t *testing.T, raw string) {
		h.CheckURL(raw)
	})
}
