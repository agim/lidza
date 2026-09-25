import { hydrate, mount } from 'svelte'
import App from './App.svelte'
import { announceTimezone } from './timezone'
import { enableAnalytics } from './analytics'
import './app.css'

announceTimezone()
if (import.meta.env.VITE_ANALYTICS === '1') enableAnalytics()

// The page is prerendered by `npm run build` (scripts/prerender.mjs), so
// the markup is already there: hydrate it. In dev, or without a build,
// mount from scratch.
const target = document.getElementById('app')!
if (target.hasChildNodes()) {
  hydrate(App, { target })
} else {
  mount(App, { target })
}
