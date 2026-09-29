// The visitor's time zone, read by the i18n pack for Date, Time and
// DateTime (tz cookie). A file, not an inline script, so the default
// Content-Security-Policy (script-src 'self') holds.
try { document.cookie = "tz=" + encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone) + "; path=/; max-age=31536000; SameSite=Lax" } catch (e) {}
