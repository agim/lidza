// Tells the server the browser's time zone through the tz cookie, which the
// i18n pack reads for Date, Time and DateTime. Runs once per page load;
// nothing is rendered from it, so prerendered markup stays identical.
export function announceTimezone() {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (zone) document.cookie = `tz=${encodeURIComponent(zone)}; path=/; max-age=31536000; SameSite=Lax`
  } catch {
    // No Intl support: the server keeps its default zone.
  }
}
