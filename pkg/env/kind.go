package env

import "strings"

// The two kinds of setting. An app's configuration (its API keys, the
// providers and models it uses, its address, sender, buckets, admins,
// signing secret) lives in the app: saved in its database (the admin
// pages, lidza settings set) and seeded for development by the sealed
// credentials file. The environment is for the deployment: where the
// database is, the master key, addresses and ports, logging and
// tracing, and performance tuning (pool sizes, workers, timeouts, rate
// limits, retention). An app moved to another server then takes its
// configuration along in its database, and the new environment says
// only how to run it.
const (
	KindInfra = "infra"
	KindApp   = "app"
)

// infraNames are deployment settings that no pattern below catches.
var infraNames = map[string]bool{
	"DATABASE_URL": true, "PGHOST": true, "PGPORT": true,
	"CACHE_URL": true, "CACHE_PREFIX": true, "REALTIME_BUS_URL": true,
	"STORAGE_DIR": true, "MAIL_TEMPLATES": true,
	"AUTH_COOKIE_SECURE": true, "AUTH_OWNER_CLAIM": true, "AUTH_OWNER_CLAIM_DIR": true, "AUTH_OWNER_CLAIM_TOKEN": true,
	"HOOKS_ALLOW_PRIVATE": true,
	// Endpoints tests point at stubs.
	"STRIPE_API_URL": true, "STORAGE_CDN_API_URL": true,
}

// infraPrefixes and infraSuffixes: the framework's own settings, the
// database's, tracing; and tuning.
var (
	infraPrefixes = []string{"LIDZA_", "DB_", "OTEL_"}
	infraSuffixes = []string{
		"_TIMEOUT", "_WORKERS", "_POLL", "_STALE", "_DRAIN", "_BUFFER", "_QUEUE",
		"_MAX_CONNS", "_MIN_CONNS", "_MAX_TOPICS", "_MEMORY_MAX", "_RPS", "_BURST",
		"_MAX_ATTEMPTS", "_MAX_SIZE", "_MAX_RECIPIENTS", "_MAX_ATTACHMENTS", "_MAX_ATTACHMENT_BYTES",
		"_MAX_ENDPOINTS", "_DISABLE_AFTER", "_MAX_TOKENS", "_MAX_TOOL_ROUNDS", "_RETENTION",
	}
)

// Kind says whether a setting belongs to the deployment's environment
// (KindInfra) or in the app (KindApp). Every name not recognised as the
// deployment's is the app's.
func Kind(name string) string {
	if infraNames[name] {
		return KindInfra
	}
	for _, p := range infraPrefixes {
		if strings.HasPrefix(name, p) {
			return KindInfra
		}
	}
	for _, s := range infraSuffixes {
		if strings.HasSuffix(name, s) {
			return KindInfra
		}
	}
	return KindApp
}

// Secret says whether a setting's value is a secret by its name: shown
// as saved, never echoed.
func Secret(name string) bool {
	for _, w := range []string{"KEY", "SECRET", "TOKEN", "PASSWORD"} {
		if strings.Contains(name, w) {
			return true
		}
	}
	return strings.HasSuffix(name, "_SMTP_URL") || name == "DATABASE_URL"
}

// AppSetting reports whether name is one of the framework's app
// settings (KindApp and named like the packs' settings), so unrelated
// variables (PATH, HOME) are never taken for one.
func AppSetting(name string) bool {
	if Kind(name) != KindApp {
		return false
	}
	if appNames[name] {
		return true
	}
	for _, p := range []string{"AUTH_", "STORAGE_", "MAIL_", "LLM_", "EMBED_", "STRIPE_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

var appNames = map[string]bool{"APP_URL": true, "SECURITY_CONTACT": true, "SECURITY_POLICY": true, "ADMIN_USERS": true, "ANALYTICS_OTLP_URL": true, "I18N_DEFAULT": true, "I18N_TIMEZONE": true}
