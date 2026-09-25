import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The Vite dev server sits behind `lidza dev` on port 3000: Go answers
// /api, everything else (including the HMR WebSocket) is proxied here.
export default defineConfig({
  plugins: [react()],
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
