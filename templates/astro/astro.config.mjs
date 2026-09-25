import { fileURLToPath } from 'node:url'
import { defineConfig } from 'astro/config'

// Static output only: pages are HTML at build time, served by the Go
// binary; anything dynamic comes from /api through @lidza/client in
// client-side scripts. In dev, `lidza dev` proxies this server on 5173.
export default defineConfig({
  output: 'static',
  server: { host: '127.0.0.1', port: 5173 },
  vite: {
    resolve: {
      alias: {
        '@lidza/client': fileURLToPath(new URL('./.lidza/client/index.ts', import.meta.url)),
      },
    },
  },
})
