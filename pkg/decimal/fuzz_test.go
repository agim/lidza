package decimal

import "testing"

// FuzzParse: decimals arrive in JSON bodies. Parsing never panics or
// expands without bound, and a parsed value reparses to itself with the
// same value.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"12.50", "-0.0", "+1e3", "1.25E-3", "1e10000", "1e-10000", "00012", ".5", "1."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := Parse(s)
		if err != nil {
			return
		}
		if len(d) > len(s)+10002 {
			t.Fatalf("%q grew to %d bytes", s, len(d))
		}
		again, err := Parse(string(d))
		if err != nil || again != d {
			t.Fatalf("%q -> %q -> %q %v", s, d, again, err)
		}
		if d.Cmp(again) != 0 || !d.Valid() {
			t.Fatalf("%q is not equal to itself", d)
		}
		d.Fits(38, 10)
	})
}
