import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// The Vite dev server sits behind `lidza dev` on port 3000: Go answers
// /api, everything else (including the HMR WebSocket) is proxied here.
// @lidza/client is the generated API client in .lidza/client.
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: {
      '@lidza/client': fileURLToPath(new URL('./.lidza/client/index.ts', import.meta.url)),
    },
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
