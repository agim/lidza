// Server rendering entry, used two ways:
// - build time: `vite build --ssr` bundles it and scripts/prerender.mjs
//   calls render for every static path (no request, loading state);
// - request time: the optional sidecar (scripts/ssr-server.mjs, started by
//   the Go binary with LIDZA_SSR=1) calls render with the request's path and
//   headers; route loaders then run on the server against the Go API, with
//   the visitor's cookies forwarded, so personalised pages arrive complete.
import { StrictMode, Suspense } from 'react'
import { renderToString } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { configure } from '@lidza/client'
import { createAppRouter } from './router'

export interface RenderOptions {
  headers?: Record<string, string>
  apiBase?: string
}

export async function render(path: string, options: RenderOptions = {}): Promise<string> {
  const forwarded: Record<string, string> = {}
  for (const name of ['cookie', 'accept-language', 'authorization']) {
    const value = options.headers?.[name]
    if (value) forwarded[name] = value
  }
  configure({ baseUrl: options.apiBase ?? '', headers: forwarded })
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }))
  await router.load()
  const queryClient = new QueryClient()
  // The router wraps its matches in a Suspense boundary in the browser but
  // not on the server. This boundary sits at the same DOM position (context
  // providers add no nodes), so the markup carries the marker the client
  // expects and hydration keeps the prerendered DOM.
  return renderToString(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <Suspense fallback={null}>
          <RouterProvider router={router} />
        </Suspense>
      </QueryClientProvider>
    </StrictMode>,
  )
}

// staticPaths lists the routes without parameters.
export function staticPaths(): string[] {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/'] }))
  const byPath = (router as unknown as { routesByPath: Record<string, unknown> }).routesByPath ?? {}
  return Object.keys(byPath).filter((p) => !p.includes('$') && !p.includes('*'))
}
