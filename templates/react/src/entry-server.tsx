// Prerendering entry: `vite build --ssr` bundles it, scripts/prerender.mjs
// calls render for every static path and writes dist/<path>/index.html.
// Data is not fetched here; pages render their loading state and fetch
// from /api after hydration.
import { StrictMode } from 'react'
import { renderToString } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { createAppRouter } from './router'

export async function render(path: string): Promise<string> {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }))
  await router.load()
  const queryClient = new QueryClient()
  return renderToString(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
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
