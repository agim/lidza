package env

import "testing"

func TestKind(t *testing.T) {
	for name, want := range map[string]string{
		"DATABASE_URL": KindInfra, "LIDZA_MASTER_KEY": KindInfra, "DB_MAX_CONNS": KindInfra, "JOBS_WORKERS": KindInfra,
		"LLM_TIMEOUT": KindInfra, "OTEL_SERVICE_NAME": KindInfra, "CACHE_URL": KindInfra, "AUTH_LOGIN_RPS": KindInfra,
		"MAIL_API_KEY": KindApp, "MAIL_FROM": KindApp, "LLM_PROVIDER": KindApp, "APP_URL": KindApp, "AUTH_SECRET": KindApp,
		"STRIPE_SECRET_KEY": KindApp, "STORAGE_MEDIA_BUCKET": KindApp, "STORAGE_MEDIA_TIMEOUT": KindInfra, "AUTH_PROVIDERS": KindApp,
	} {
		if got := Kind(name); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	if !Secret("MAIL_API_KEY") || !Secret("AUTH_GOOGLE_CLIENT_SECRET") || Secret("MAIL_FROM") {
		t.Error("Secret")
	}
}
