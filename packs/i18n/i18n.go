// Package i18n is the official localization pack: message catalogs from
// locales/<lang>.json embedded in the binary, locale negotiation per
// request (query, cookie, Accept-Language), and formatting of numbers,
// currency and dates for the locale. The catalog is also served to the
// frontend.
package i18n

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/currency"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/middleware"
	"github.com/agim/lidza/pkg/router"
)

// Config comes from the environment.
type Config struct {
	// Default is the locale used when nothing better matches.
	Default string `env:"I18N_DEFAULT" default:"en"`
	// Timezone is the IANA zone used when the request names none.
	Timezone string `env:"I18N_TIMEZONE" default:"UTC"`
}

// I18n is the running pack.
type I18n struct {
	cfg      Config
	locales  fs.FS
	catalogs map[string]map[string]string
	tags     []language.Tag
	matcher  language.Matcher
	def      language.Tag
	defZone  *time.Location
}

// Pack returns the pack for packs.go; packs.go embeds locales/ and passes
// it here.
func Pack(locales fs.FS) lidza.Pack { return &I18n{locales: locales} }

// New loads the catalogs outside the lifecycle (tests).
func New(locales fs.FS, def string) (*I18n, error) {
	i := &I18n{locales: locales, cfg: Config{Default: def, Timezone: "UTC"}}
	return i, i.load()
}

// From returns the pack from a request context.
func From(ctx context.Context) *I18n { return lidza.Service[*I18n](ctx) }

// Name implements lidza.Pack.
func (i *I18n) Name() string { return "lidza/i18n" }

// Start loads the catalogs.
func (i *I18n) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &i.cfg); err != nil {
		return err
	}
	if err := i.load(); err != nil {
		return err
	}
	lidza.Provide(s, i)
	return nil
}

// Stop implements lidza.Pack.
func (i *I18n) Stop(context.Context) error { return nil }

// Middleware puts the negotiated locale and time zone in every API
// request's context.
func (i *I18n) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithLocale(r.Context(), i.Negotiate(r))
			ctx = WithTimezone(ctx, i.NegotiateTimezone(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// NegotiateTimezone picks the request's zone: ?tz, the tz cookie (the
// react template sets it from the browser), the X-Timezone header, then
// the default. Unknown names fall back to the default.
func (i *I18n) NegotiateTimezone(r *http.Request) *time.Location {
	candidates := []string{r.URL.Query().Get("tz")}
	if c, err := r.Cookie("tz"); err == nil {
		candidates = append(candidates, c.Value)
	}
	candidates = append(candidates, r.Header.Get("X-Timezone"))
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return i.defZone
}

type timezoneKey struct{}

// WithTimezone attaches a zone to ctx.
func WithTimezone(ctx context.Context, loc *time.Location) context.Context {
	return context.WithValue(ctx, timezoneKey{}, loc)
}

// Timezone returns the request's zone, or the default.
func (i *I18n) Timezone(ctx context.Context) *time.Location {
	if loc, ok := ctx.Value(timezoneKey{}).(*time.Location); ok && loc != nil {
		return loc
	}
	return i.defZone
}

// load reads locales/*.json. Nested objects flatten to dotted keys.
func (i *I18n) load() error {
	entries, err := fs.ReadDir(i.locales, ".")
	if err != nil {
		return fmt.Errorf("i18n: locales: %w", err)
	}
	i.catalogs = map[string]map[string]string{}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		lang := strings.TrimSuffix(e.Name(), ".json")
		tag, err := language.Parse(lang)
		if err != nil {
			return fmt.Errorf("i18n: %s: %w", e.Name(), err)
		}
		data, err := fs.ReadFile(i.locales, e.Name())
		if err != nil {
			return err
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("i18n: %s: %w", e.Name(), err)
		}
		flat := map[string]string{}
		flatten("", raw, flat)
		i.catalogs[tag.String()] = flat
		i.tags = append(i.tags, tag)
	}
	if len(i.tags) == 0 {
		return fmt.Errorf("i18n: no locales/*.json files")
	}
	sort.Slice(i.tags, func(a, b int) bool { return i.tags[a].String() < i.tags[b].String() })
	def, err := language.Parse(i.cfg.Default)
	if err != nil {
		return fmt.Errorf("i18n: I18N_DEFAULT: %w", err)
	}
	if _, ok := i.catalogs[def.String()]; !ok {
		return fmt.Errorf("i18n: default locale %s has no catalog", def)
	}
	// The default first: the matcher prefers it when nothing matches.
	ordered := []language.Tag{def}
	for _, t := range i.tags {
		if t != def {
			ordered = append(ordered, t)
		}
	}
	i.def = def
	i.matcher = language.NewMatcher(ordered)
	zone, err := time.LoadLocation(i.cfg.Timezone)
	if err != nil {
		return fmt.Errorf("i18n: I18N_TIMEZONE: %w", err)
	}
	i.defZone = zone
	return nil
}

func flatten(prefix string, in map[string]any, out map[string]string) {
	for k, v := range in {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch x := v.(type) {
		case map[string]any:
			flatten(key, x, out)
		case string:
			out[key] = x
		default:
			out[key] = fmt.Sprint(x)
		}
	}
}

// Negotiate picks the locale for a request: ?lang, the lang cookie, then
// Accept-Language, then the default.
func (i *I18n) Negotiate(r *http.Request) language.Tag {
	var prefs []language.Tag
	if q := r.URL.Query().Get("lang"); q != "" {
		if t, err := language.Parse(q); err == nil {
			prefs = append(prefs, t)
		}
	}
	if c, err := r.Cookie("lang"); err == nil {
		if t, err := language.Parse(c.Value); err == nil {
			prefs = append(prefs, t)
		}
	}
	if accepted, _, err := language.ParseAcceptLanguage(r.Header.Get("Accept-Language")); err == nil {
		prefs = append(prefs, accepted...)
	}
	tag, _, _ := i.matcher.Match(prefs...)
	// Report the catalog tag, not the matcher's refined one.
	base, _ := tag.Base()
	for _, t := range i.tags {
		if b, _ := t.Base(); b == base {
			return t
		}
	}
	return i.def
}

// Locales lists the available locales.
func (i *I18n) Locales() []string {
	out := make([]string, len(i.tags))
	for n, t := range i.tags {
		out[n] = t.String()
	}
	return out
}

// Catalog returns the messages of a locale (the default's when unknown).
func (i *I18n) Catalog(tag language.Tag) map[string]string {
	if c, ok := i.catalogs[tag.String()]; ok {
		return c
	}
	return i.catalogs[i.def.String()]
}

type localeKey struct{}

// WithLocale attaches a locale to ctx.
func WithLocale(ctx context.Context, tag language.Tag) context.Context {
	return context.WithValue(ctx, localeKey{}, tag)
}

// Locale returns the request's locale, or the default.
func (i *I18n) Locale(ctx context.Context) language.Tag {
	if t, ok := ctx.Value(localeKey{}).(language.Tag); ok {
		return t
	}
	return i.def
}

// T translates key for the request's locale, formatting args with
// fmt.Sprintf verbs in the message. Falls back to the default locale, then
// to the key itself.
func (i *I18n) T(ctx context.Context, key string, args ...any) string {
	tag := i.Locale(ctx)
	msg, ok := i.catalogs[tag.String()][key]
	if !ok {
		msg, ok = i.catalogs[i.def.String()][key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// Number formats n with the locale's separators.
func (i *I18n) Number(ctx context.Context, n any) string {
	return message.NewPrinter(i.Locale(ctx)).Sprint(number.Decimal(n))
}

// Currency formats an amount in the ISO 4217 code for the locale.
func (i *I18n) Currency(ctx context.Context, amount float64, code string) string {
	unit, err := currency.ParseISO(code)
	if err != nil {
		return fmt.Sprintf("%s %.2f", code, amount)
	}
	p := message.NewPrinter(i.Locale(ctx))
	return p.Sprint(currency.Symbol(unit.Amount(amount)))
}

// Date formats t in the request's zone with the catalog's "_formats.date"
// layout (Go layout), default 2006-01-02.
func (i *I18n) Date(ctx context.Context, t time.Time) string {
	return i.format(ctx, t, "_formats.date", "2006-01-02")
}

// Time formats the time of day, "_formats.time", default 15:04.
func (i *I18n) Time(ctx context.Context, t time.Time) string {
	return i.format(ctx, t, "_formats.time", "15:04")
}

// DateTime formats both, "_formats.datetime", default 2006-01-02 15:04.
func (i *I18n) DateTime(ctx context.Context, t time.Time) string {
	return i.format(ctx, t, "_formats.datetime", "2006-01-02 15:04")
}

func (i *I18n) format(ctx context.Context, t time.Time, key, fallback string) string {
	layout := i.T(ctx, key)
	if layout == key {
		layout = fallback
	}
	return t.In(i.Timezone(ctx)).Format(layout)
}

// Handler serves the catalog of a locale for the frontend:
// GET /api/v1/i18n/{lang} replies {"locale": "en", "messages": {...}}.
// Register with r.Handle("GET /api/v1/i18n/{lang}", i18n.Handler()).
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := From(r.Context())
		tag, err := language.Parse(r.PathValue("lang"))
		if err != nil {
			router.Error(w, http.StatusBadRequest, "unknown locale")
			return
		}
		resolved := i.def
		base, _ := tag.Base()
		for _, t := range i.tags {
			if b, _ := t.Base(); b == base {
				resolved = t
			}
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		router.JSON(w, http.StatusOK, map[string]any{"locale": resolved.String(), "messages": i.Catalog(resolved)})
	})
}
