package admin

import (
	"context"
	"slices"
	"strings"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
)

// appSetting is one app setting of the generated table.
type appSetting struct {
	Name, Area, Default, Doc string
}

// areaSections name the Settings page's sections for the app settings
// the pack pages do not hold: the app's own, then each pack's.
var areaSections = []struct {
	area, key, title, icon, pack string
}{
	{"core", "settings-app", "Application", "settings", ""},
	{"packs/auth", "settings-signin", "Sign-in policy", "user-shield", "lidza/auth"},
	{"packs/billing", "settings-billing", "Billing", "building", "lidza/billing"},
	{"packs/mail", "settings-mail", "Mail", "mail", "lidza/mail"},
	{"packs/llm", "settings-llm", "Models", "sparkles", "lidza/llm"},
	{"packs/storage", "settings-storage", "Storage CDN", "folder", "lidza/storage"},
	{"packs/i18n", "settings-i18n", "Language and time zone", "world", "lidza/i18n"},
	{"packs/analytics", "settings-analytics", "Analytics", "chart-bar", "lidza/analytics"},
	{"packs/admin", "settings-admins", "Administrators", "user-shield", ""},
}

// appSettingSections are sections for every app setting no other
// section edits, so each one can be stored in the app instead of the
// environment. A pack's section shows while the pack runs.
func appSettingSections(covered map[string]bool) []Section {
	byArea := map[string][]Field{}
	for _, s := range appSettings {
		// AUTH_SECRET is the app's own, generated once: changing it from
		// a page would sign everyone out (lidza settings set does it on
		// purpose).
		if covered[s.Name] || s.Name == "AUTH_SECRET" {
			continue
		}
		kind := "text"
		if env.Secret(s.Name) {
			kind = "secret"
		}
		byArea[s.Area] = append(byArea[s.Area], Field{Name: s.Name, Label: s.Name, Kind: kind, Default: s.Default, Help: s.Doc})
	}
	var out []Section
	for _, a := range areaSections {
		fields := byArea[a.area]
		if len(fields) == 0 {
			continue
		}
		slices.SortFunc(fields, func(x, y Field) int { return strings.Compare(x.Name, y.Name) })
		sec := Section{Key: a.key, Title: a.title, Icon: a.icon, Fields: fields,
			Description: "Stored in the app's database, so the app moves with it; an environment variable of the same name wins over it.",
			Optional:    true}
		if pack := a.pack; pack != "" {
			sec.Enabled = func(ctx context.Context) bool { return packRunning(ctx, pack) }
		}
		out = append(out, sec)
	}
	return out
}

// packRunning reports whether a pack of that name runs in the app.
func packRunning(ctx context.Context, name string) bool {
	s := lidza.ServicesFrom(ctx)
	if s == nil {
		return false
	}
	found := false
	s.Each(func(v any) {
		if p, ok := v.(interface{ Name() string }); ok && p.Name() == name {
			found = true
		}
	})
	return found
}

// coveredNames are the settings the built-in sections already edit.
func coveredNames() map[string]bool {
	out := map[string]bool{}
	for _, s := range builtinSections() {
		if s.Selector != "" {
			out[s.Selector] = true
		}
		for _, f := range s.Fields {
			out[f.Name] = true
		}
	}
	return out
}
