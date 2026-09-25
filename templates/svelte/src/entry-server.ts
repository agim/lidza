// Server rendering entry: `vite build --ssr` bundles it and
// scripts/prerender.mjs calls renderApp at build time, so the built
// index.html carries the page's markup (the loading state; data arrives
// in the browser through @lidza/client).
import { render } from 'svelte/server'
import App from './App.svelte'

export function renderApp(): string {
  return render(App).body
}
