package i18n

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agim/lidza"
)

func TestI18n(t *testing.T) {
	locales := fstest.MapFS{
		"en.json": {Data: []byte(`{"greeting": "Hello, %s", "items": {"one": "one item"}, "_formats": {"date": "Jan 2, 2006"}}`)},
		"de.json": {Data: []byte(`{"greeting": "Hallo, %s", "_formats": {"date": "02.01.2006"}}`)},
	}
	i, err := New(locales, "en")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(i.Locales(), ",") != "de,en" {
		t.Fatalf("locales %v", i.Locales())
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "de-CH, fr;q=0.8")
	ctx := WithLocale(req.Context(), i.Negotiate(req))
	if i.T(ctx, "greeting", "Agim") != "Hallo, Agim" {
		t.Fatalf("de: %q", i.T(ctx, "greeting", "Agim"))
	}
	if i.T(ctx, "items.one") != "one item" {
		t.Fatal("fallback to default locale")
	}
	if i.T(ctx, "missing") != "missing" {
		t.Fatal("fallback to key")
	}
	if got := i.Date(ctx, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)); got != "25.09.2026" {
		t.Fatalf("date %q", got)
	}
	// Time zone: cookie, then header; unknown names fall back to UTC.
	tzReq := httptest.NewRequest("GET", "/", nil)
	tzReq.AddCookie(&http.Cookie{Name: "tz", Value: "Europe/Tirane"})
	tzCtx := WithTimezone(ctx, i.NegotiateTimezone(tzReq))
	stamp := time.Date(2026, 9, 25, 22, 30, 0, 0, time.UTC)
	if got := i.DateTime(tzCtx, stamp); got != "2026-09-26 00:30" {
		t.Fatalf("datetime in zone: %q", got)
	}
	if got := i.Time(tzCtx, stamp); got != "00:30" {
		t.Fatalf("time in zone: %q", got)
	}
	if got := i.Date(tzCtx, stamp); got != "26.09.2026" {
		t.Fatalf("date crosses midnight in zone: %q", got)
	}
	hdr := httptest.NewRequest("GET", "/", nil)
	hdr.Header.Set("X-Timezone", "America/New_York")
	if i.NegotiateTimezone(hdr).String() != "America/New_York" {
		t.Fatal("header zone")
	}
	bad := httptest.NewRequest("GET", "/?tz=Mars/Olympus", nil)
	if i.NegotiateTimezone(bad).String() != "UTC" {
		t.Fatal("unknown zone should fall back")
	}
	if got := i.Number(ctx, 1234567.5); got != "1.234.567,5" {
		t.Fatalf("number %q", got)
	}
	en := WithLocale(req.Context(), i.def)
	if got := i.Number(en, 1234567.5); got != "1,234,567.5" {
		t.Fatalf("number en %q", got)
	}
	if got := i.Currency(en, 12.5, "EUR"); !strings.Contains(got, "12.50") {
		t.Fatalf("currency %q", got)
	}

	// ?lang beats the header; unknown falls back to the default.
	req = httptest.NewRequest("GET", "/?lang=de", nil)
	req.Header.Set("Accept-Language", "en")
	if i.Negotiate(req).String() != "de" {
		t.Fatal("query param")
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "ja")
	if i.Negotiate(req).String() != "en" {
		t.Fatal("default")
	}

	s := lidza.NewServices()
	lidza.Provide(s, i)
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/i18n/{lang}", Handler())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/i18n/de-AT", nil)
	mux.ServeHTTP(rec, r.WithContext(lidza.WithServices(r.Context(), s)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"locale":"de"`) || !strings.Contains(rec.Body.String(), "Hallo") {
		t.Fatalf("handler: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := New(fstest.MapFS{}, "en"); err == nil {
		t.Fatal("empty locales accepted")
	}
}
