import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  type RouterHistory,
} from '@tanstack/react-router'
import { Home } from './pages/Home'
import { About } from './pages/About'
import { RouteError } from './ErrorBoundary'

// Code-based routes. Add a page: create it under src/pages, declare a route
// here, add it to the tree. Paths under /api are never routed here; they
// belong to the Go control plane. Routes without parameters are prerendered
// to static HTML by `npm run build` (scripts/prerender.mjs).
export const rootRoute = createRootRoute({
  component: () => (
    <>
      <nav className="flex gap-4 border-b border-line px-6 py-3">
        <Link to="/" className="text-ink no-underline [&.active]:font-semibold [&.active]:text-brand">
          Home
        </Link>
        <Link to="/about" className="text-ink no-underline [&.active]:font-semibold [&.active]:text-brand">
          About
        </Link>
      </nav>
      <main className="mx-auto max-w-3xl px-6 py-6">
        <Outlet />
      </main>
    </>
  ),
})

const homeRoute = createRoute({ getParentRoute: () => rootRoute, path: '/', component: Home })
const aboutRoute = createRoute({ getParentRoute: () => rootRoute, path: '/about', component: About })

const routeTree = rootRoute.addChildren([homeRoute, aboutRoute])

// createAppRouter builds a router for the browser (no history given) or for
// server rendering (a memory history at one path).
export function createAppRouter(history?: RouterHistory, options: { hydrating?: boolean } = {}) {
  const router = createRouter({ routeTree, defaultErrorComponent: RouteError, history })
  if (options.hydrating) {
    // The server renders without the router's top-level Suspense wrapper;
    // a client router marked as hydrating server markup renders the same
    // tree, so React keeps the prerendered DOM instead of rebuilding it.
    // Each route keeps its own pending boundary.
    Object.assign(router, { ssr: {} })
  }
  return router
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
