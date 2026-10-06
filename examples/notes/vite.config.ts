import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { imagetools } from 'vite-imagetools'

// The Vite dev server sits behind `lidza dev` on port 3000: Go answers
// /api, everything else (including the HMR WebSocket) is proxied here.
// @lidza/client is the generated API client in .lidza/client.
// `import hero from './hero.jpg?responsive'` is the image at the widths
// below that fit it (and its own), as AVIF and WebP with the original
// format as the fallback, content-hashed in dist/assets: the shape
// <Picture> renders. Other image imports are untouched.
const widths = [480, 800, 1200, 1600, 2400]
type ImageInfo = { width?: number; format?: string }
const responsive = async (url: URL, metadata: () => ImageInfo | Promise<ImageInfo>) => {
  if (!url.searchParams.has('responsive')) return new URLSearchParams()
  const { width = widths[0], format } = await metadata()
  const fit = widths.filter((w) => w < width)
  return new URLSearchParams({
    w: [...fit, width].join(';'),
    format: `avif;webp;${format === 'png' ? 'png' : 'jpg'}`,
    as: 'picture',
  })
}

export default defineConfig({
  plugins: [react(), tailwindcss(), imagetools({ defaultDirectives: responsive })],
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
  // The SSR bundle carries its dependencies, so the sidecar needs only
  // Node and the files under dist/.server.
  ssr: {
    noExternal: true,
  },
})
