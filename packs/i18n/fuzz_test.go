package i18n

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// FuzzNegotiateTimezone: the zone comes from the query, a cookie or a
// header. Anything else than a zone name falls back to the default.
func FuzzNegotiateTimezone(f *testing.F) {
	for _, s := range []string{"Europe/Berlin", "Local", "../../etc/passwd", "/usr/share/zoneinfo/UTC", "Etc/GMT+5", "UTC"} {
		f.Add(s)
	}
	i := &I18n{defZone: time.UTC}
	f.Fuzz(func(t *testing.T, name string) {
		r, _ := http.NewRequest("GET", "/?tz="+url.QueryEscape(name), nil)
		loc := i.NegotiateTimezone(r)
		if loc == nil {
			t.Fatal("no zone")
		}
		if loc != time.UTC && (strings.Contains(name, "..") || strings.HasPrefix(name, "/") || name == "Local") {
			t.Fatalf("%q loaded %v", name, loc)
		}
	})
}
