import { hydrate, mount } from 'svelte'
import App from './App.svelte'
import { routes } from './routes'
import { restoreScroll, startRouter } from './router.svelte'
import { announceTimezone } from './timezone'
import { enableAnalytics } from './analytics'
import { startI18n } from './i18n'
import './app.css'

announceTimezone()
if (import.meta.env.VITE_ANALYTICS === '1') enableAnalytics()
// The catalog of the locale the page was rendered in, before the first
// render, so t() matches the server's markup (a no-op without locales/).
await startI18n()

// The router reads the address before the first render, so a deep link
// or a reload renders its own view.
startRouter(routes)

// A page prerendered by `npm run build` (scripts/prerender.mjs) arrives
// with its markup: hydrate it. Any other path gets the bare shell, as in
// dev: mount from scratch.
const target = document.getElementById('app')!
if (target.hasChildNodes()) {
  hydrate(App, { target })
} else {
  mount(App, { target })
}
void restoreScroll()
