// Server rendering entry, used two ways:
// - build time: `vite build --ssr` bundles it and scripts/prerender.mjs
//   calls render for every static path (no request, loading state);
// - request time: the optional sidecar (scripts/ssr-server.mjs, started by
//   the Go binary with LIDZA_SSR=1) calls render with the request's path and
//   headers; route loaders then run on the server against the Go API, with
//   the visitor's cookies forwarded, so personalised pages arrive complete.
// The result is the whole page: the template with the app's markup in
// #root and, before </body>, the router's hydration payload (the loader
// data of the rendered matches), so the client does not run the loaders
// again at hydration.
import { StrictMode } from 'react'
import { renderToString } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory } from '@tanstack/react-router'
import { RouterServer } from '@tanstack/react-router/ssr/server'
import { createRequestHandler, renderSsrHtmlResponse } from '@tanstack/router-core/ssr/server'
import { configure } from '@lidza/client'
import { createAppRouter } from './router'

export interface RenderOptions {
  headers?: Record<string, string>
  apiBase?: string
  /** The built index.html; its `<div id="root"></div>` receives the markup. */
  template: string
}

const marker = '<div id="root"></div>'

export async function render(path: string, options: RenderOptions): Promise<string> {
  if (!options.template.includes(marker)) throw new Error('index.html has no <div id="root"></div>')
  const forwarded: Record<string, string> = {}
  for (const name of ['cookie', 'accept-language', 'authorization']) {
    const value = options.headers?.[name]
    if (value) forwarded[name] = value
  }
  configure({ baseUrl: options.apiBase ?? '', headers: forwarded })
  // The router injects "<!DOCTYPE html>" itself; the template's own is dropped.
  const template = options.template.replace(/^\s*<!doctype html>\s*/i, '')
  const request = new Request('http://localhost' + path, { headers: forwarded })
  const handler = createRequestHandler({ request, createRouter: () => createAppRouter() })
  const response = await handler(({ router, responseHeaders }) =>
    renderSsrHtmlResponse({
      router,
      responseHeaders,
      render: () => {
        const queryClient = new QueryClient()
        // No Suspense boundary of our own: with the hydration payload the
        // client router (RouterClient) renders its matches without one, as
        // the server does, so the trees match and hydration keeps the DOM.
        const app = renderToString(
          <StrictMode>
            <QueryClientProvider client={queryClient}>
              <RouterServer router={router} />
            </QueryClientProvider>
          </StrictMode>,
        )
        return template.replace(marker, `<div id="root">${app}</div>`)
      },
    }),
  )
  if (response.status >= 500) throw new Error(`render ${path}: ${response.status}`)
  return await response.text()
}

// staticPaths lists the routes without parameters.
export function staticPaths(): string[] {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/'] }))
  const byPath = (router as unknown as { routesByPath: Record<string, unknown> }).routesByPath ?? {}
  return Object.keys(byPath).filter((p) => !p.includes('$') && !p.includes('*'))
}
