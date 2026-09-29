import { fileURLToPath } from 'node:url'
import { defineConfig } from 'astro/config'

// Static output only: pages are HTML at build time, served by the Go
// binary; anything dynamic comes from /api through @lidza/client in
// client-side scripts. In dev, `lidza dev` proxies this server on 5173.
// Styles and scripts are files, never inlined into the pages, so the
// binary's default Content-Security-Policy ('self') holds and they cache.
export default defineConfig({
  output: 'static',
  server: { host: '127.0.0.1', port: 5173 },
  build: { inlineStylesheets: 'never' },
  vite: {
    build: { assetsInlineLimit: 0 },
    resolve: {
      alias: {
        '@lidza/client': fileURLToPath(new URL('./.lidza/client/index.ts', import.meta.url)),
      },
    },
  },
})
