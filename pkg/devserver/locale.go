package devserver

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
)

// LocalesDir is the directory inside dist where the prerender writes each
// page once per locale when the app has more than one (locales/*.json):
// .locales/<lang>/<path>/index.html, .locales/<lang>/shell.html for the
// paths it did not render, and .locales/manifest.json naming the locales
// and the default.
const LocalesDir = ".locales"

// LocaleFunc names the locale a request asks for, such as the i18n pack's
// negotiation. An empty result leaves the choice to the built-in one.
type LocaleFunc func(r *http.Request) string

// Option configures Static, NewSidecar and NewProxy.
type Option func(*options)

type options struct {
	locale LocaleFunc
	head   HeadFunc
}

// WithLocale picks the page variant with f instead of the built-in
// negotiation (?lang, the lang cookie, Accept-Language, the default).
func WithLocale(f LocaleFunc) Option {
	return func(o *options) { o.locale = f }
}

func collect(opts []Option) options {
	var o options
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// locales is the prerender's manifest with the negotiation to use.
type locales struct {
	def       string
	list      []string
	negotiate LocaleFunc
}

// loadLocales reads dist/.locales/manifest.json; nil when the build has
// no page variants.
func loadLocales(dist fs.FS, negotiate LocaleFunc) *locales {
	data, err := fs.ReadFile(dist, path.Join(LocalesDir, "manifest.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Default string   `json:"default"`
		Locales []string `json:"locales"`
	}
	if err := json.Unmarshal(data, &m); err != nil || len(m.Locales) == 0 {
		return nil
	}
	l := &locales{list: m.Locales, negotiate: negotiate}
	if l.def = l.match(m.Default); l.def == "" {
		l.def = m.Locales[0]
	}
	return l
}

// pick returns the locale of the variant to serve for r.
func (l *locales) pick(r *http.Request) string {
	if l.negotiate != nil {
		if m := l.match(l.negotiate(r)); m != "" {
			return m
		}
	}
	prefs := []string{r.URL.Query().Get("lang")}
	if c, err := r.Cookie("lang"); err == nil {
		prefs = append(prefs, c.Value)
	}
	prefs = append(prefs, acceptLanguage(r.Header.Get("Accept-Language"))...)
	for _, p := range prefs {
		if m := l.match(p); m != "" {
			return m
		}
	}
	return l.def
}

// match finds tag among the locales: the same tag, then the same base
// language (sq-AL finds sq, pt finds pt-BR).
func (l *locales) match(tag string) string {
	tag = strings.TrimSpace(strings.ReplaceAll(tag, "_", "-"))
	if tag == "" || tag == "*" {
		return ""
	}
	for _, loc := range l.list {
		if strings.EqualFold(loc, tag) {
			return loc
		}
	}
	for _, loc := range l.list {
		if strings.EqualFold(baseLanguage(loc), baseLanguage(tag)) {
			return loc
		}
	}
	return ""
}

func baseLanguage(tag string) string {
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		return tag[:i]
	}
	return tag
}

// acceptLanguage lists the header's tags by quality, highest first,
// leaving out q=0.
func acceptLanguage(header string) []string {
	type pref struct {
		tag string
		q   float64
	}
	var prefs []pref
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if tag == "" {
			continue
		}
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		if q > 0 {
			prefs = append(prefs, pref{tag, q})
		}
	}
	sort.SliceStable(prefs, func(a, b int) bool { return prefs[a].q > prefs[b].q })
	out := make([]string, len(prefs))
	for i, p := range prefs {
		out[i] = p.tag
	}
	return out
}
