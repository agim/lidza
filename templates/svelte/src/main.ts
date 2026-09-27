import { hydrate, mount } from 'svelte'
import App from './App.svelte'
import { announceTimezone } from './timezone'
import { enableAnalytics } from './analytics'
import { startI18n } from './i18n'
import './app.css'

announceTimezone()
if (import.meta.env.VITE_ANALYTICS === '1') enableAnalytics()
// The catalog of the locale the page was rendered in, before the first
// render, so t() matches the server's markup (a no-op without locales/).
await startI18n()

// The page is prerendered by `npm run build` (scripts/prerender.mjs), so
// the markup is already there: hydrate it. In dev, or without a build,
// mount from scratch.
const target = document.getElementById('app')!
if (target.hasChildNodes()) {
  hydrate(App, { target })
} else {
  mount(App, { target })
}
