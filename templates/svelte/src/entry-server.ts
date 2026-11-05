// Server rendering entry: `vite build --ssr` bundles it and
// scripts/prerender.mjs calls renderPage at build time for every route
// without :params (src/routes.ts), so each one's index.html carries its
// markup (the loading state; data arrives in the browser through
// @lidza/client), and renderShell for the bare page the binary serves to
// every other path. With locales/<lang>.json the page is rendered in the
// given locale and carries <html lang> and that catalog (src/i18n.ts).
import { render } from 'svelte/server'
import App from './App.svelte'
import { routes } from './routes'
import { setRoutes, staticPaths } from './router.svelte'
import { loadLocale, localizePage } from './i18n'

export { locales } from './i18n'

const marker = '<div id="app"></div>'

/** paths are the addresses to prerender. */
export function paths(): string[] {
  return staticPaths(routes)
}

export async function renderPage(template: string, locale?: string, path = '/'): Promise<string> {
  if (!template.includes(marker)) throw new Error('index.html has no <div id="app"></div>')
  await loadLocale(locale)
  setRoutes(routes, path)
  return localizePage(template.replace(marker, `<div id="app">${render(App).body}</div>`))
}

/** renderShell is the page without markup, in a locale: the client renders it. */
export async function renderShell(template: string, locale?: string): Promise<string> {
  await loadLocale(locale)
  return localizePage(template)
}
