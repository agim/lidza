import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  stripSearchParams,
  type RouterHistory,
  type SearchSchemaInput,
} from '@tanstack/react-router'
import { Home } from './pages/Home'
import { About } from './pages/About'
import { ItemList } from './pages/items/ItemList'
import { ItemOverview, ItemPage, ItemSpecs } from './pages/items/Item'
import { categories } from './pages/items/data'
import { RouteError } from './ErrorBoundary'
import { searchChoice, searchText } from './navigation'

// Code-based routes. Add a page: create it under src/pages, declare a route
// here, add it to the tree. Paths under /api are never routed here; they
// belong to the Go control plane. Routes without parameters are prerendered
// to static HTML by `npm run build` (scripts/prerender.mjs). A page that
// needs no React in the browser (text, links, images) sets
// `staticData: { static: true }`: its prerendered HTML ships without the
// client runtime, and `enhance: ['name']` adds src/enhance/name.ts alone.
//
// Every view has a path (docs/lidza-guide.md, "Navigation"): a record by
// its slug (/items/desk-lamp), a tab as a child route
// (/items/desk-lamp/specs), a filter or a search as a search parameter
// validated here (/items?category=seating). Never a hash route. The items
// routes show this; replace them with the app's own.
export const rootRoute = createRootRoute({
  component: () => (
    <>
      <nav className="flex gap-4 border-b border-line px-6 py-3">
        <Link to="/" className="text-ink no-underline [&.active]:font-semibold [&.active]:text-brand">
          Home
        </Link>
        <Link to="/items" className="text-ink no-underline [&.active]:font-semibold [&.active]:text-brand">
          Items
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
const aboutRoute = createRoute({ getParentRoute: () => rootRoute, path: '/about', component: About, staticData: { static: true } })

// The list's search parameters, typed; a default is left out of the
// address (/items, not /items?category=all&q=).
const itemsSearchDefaults = { category: 'all' as const, q: '' }
const itemsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/items',
  // The input type (marked SearchSchemaInput) makes each parameter
  // optional in a link to /items.
  validateSearch: (search: { category?: string; q?: string } & SearchSchemaInput) => ({
    category: searchChoice(search.category, ['all', ...categories], 'all'),
    q: searchText(search.q),
  }),
  search: { middlewares: [stripSearchParams(itemsSearchDefaults)] },
  component: ItemList,
})
const itemRoute = createRoute({ getParentRoute: () => rootRoute, path: '/items/$slug', component: ItemPage })
const itemOverviewRoute = createRoute({ getParentRoute: () => itemRoute, path: '/', component: ItemOverview })
const itemSpecsRoute = createRoute({ getParentRoute: () => itemRoute, path: '/specs', component: ItemSpecs })

const routeTree = rootRoute.addChildren([homeRoute, aboutRoute, itemsRoute, itemRoute.addChildren([itemOverviewRoute, itemSpecsRoute])])

// createAppRouter builds a router for the browser (no history given) or for
// server rendering (a memory history at one path). A new view starts at
// the top; Back and Forward return to where the page was scrolled.
export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, defaultErrorComponent: RouteError, history, scrollRestoration: true })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
  interface StaticDataRouteOption {
    /**
     * Prerendered without the client runtime: the page's HTML and CSS,
     * no React, router or query code, no hydration (scripts/prerender.mjs).
     * Its links are plain links; it shows what it renders at build time.
     */
    static?: boolean
    /** With static: scripts from src/enhance/ the page loads, by name. */
    enhance?: string[]
    /**
     * false: not prerendered, for a page whose first render depends on
     * the address's search parameters without validateSearch (useListState);
     * a route with validateSearch is skipped anyway.
     */
    prerender?: boolean
  }
}
