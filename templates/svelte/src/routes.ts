// The app's routes, in the browser's history (src/router.svelte.ts). Add
// a page: create it under src/pages, add a line here. Every view has a
// path (docs/lidza-guide.md, "Navigation"): a record by its slug
// (/items/desk-lamp), a tab as a path (/items/desk-lamp/specs), a filter
// or a search as a search parameter (/items?category=seating). Never a
// hash route. Routes without :params are prerendered by `npm run build`
// unless prerender is false. The items routes show the conventions;
// replace them with the app's own. Paths under /api are the Go control
// plane's, never routes.
import type { Route } from './router.svelte'
import Home from './pages/Home.svelte'
import About from './pages/About.svelte'
import ItemList from './pages/items/ItemList.svelte'
import Item from './pages/items/Item.svelte'
import { findItem } from './pages/items/data'

const itemName = (params: Record<string, string>) => findItem(params.slug)?.name ?? 'Not found'

export const routes: Route[] = [
  { path: '/', component: Home },
  { path: '/about', component: About, title: () => ['About'] },
  { path: '/items', component: ItemList, title: () => ['Items'], prerender: false },
  { path: '/items/:slug', component: Item, title: (p) => [itemName(p)] },
  { path: '/items/:slug/specs', component: Item, title: (p) => ['Specs', itemName(p)] },
]
