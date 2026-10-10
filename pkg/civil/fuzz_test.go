package civil

import "testing"

// FuzzParse: dates arrive in JSON bodies and query strings; what parses
// prints back as the same date.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"2026-10-10", "0000-01-01", "2026-02-30", "2026-10-10T07:30:00", "9999-12-31T23:59:59.999999999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if d, err := ParseDate(s); err == nil {
			if again, err := ParseDate(d.String()); err != nil || again != d {
				t.Fatalf("%q -> %v -> %v %v", s, d, again, err)
			}
		}
		if dt, err := ParseDateTime(s); err == nil {
			if again, err := ParseDateTime(dt.String()); err != nil || again != dt {
				t.Fatalf("%q -> %v -> %v %v", s, dt, again, err)
			}
		}
		var d Date
		d.UnmarshalJSON([]byte(s))
	})
}
