package version

import "testing"

func TestModule(t *testing.T) {
	defer func(v string) { Version = v }(Version)
	for in, want := range map[string]string{
		"v0.1.2":                               "v0.1.2",
		"v0.1.2+dirty":                         "v0.1.2",
		"v0.1.2-0.20260925190849-4b19313a3c33": "v0.1.2-0.20260925190849-4b19313a3c33",
		"v0.1.2-0.20260925190849-4b19313a3c33+dirty": "v0.1.2-0.20260925190849-4b19313a3c33",
		"dev": "",
	} {
		Version = in
		if got := Module(); got != want {
			t.Errorf("Module() for %q = %q, want %q", in, got, want)
		}
	}
}
