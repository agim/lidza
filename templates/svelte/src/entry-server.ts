// Server rendering entry: `vite build --ssr` bundles it and
// scripts/prerender.mjs calls renderPage at build time, so the built
// index.html carries the page's markup (the loading state; data arrives
// in the browser through @lidza/client). With locales/<lang>.json the page
// is rendered in the given locale and carries <html lang> and that catalog
// (src/i18n.ts).
import { render } from 'svelte/server'
import App from './App.svelte'
import { loadLocale, localizePage } from './i18n'

export { locales } from './i18n'

const marker = '<div id="app"></div>'

export async function renderPage(template: string, locale?: string): Promise<string> {
  if (!template.includes(marker)) throw new Error('index.html has no <div id="app"></div>')
  await loadLocale(locale)
  return localizePage(template.replace(marker, `<div id="app">${render(App).body}</div>`))
}
