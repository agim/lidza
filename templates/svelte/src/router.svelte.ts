// Client-side routes in the browser's history, the conventions in
// docs/lidza-guide.md ("Navigation"): every view has a path, Back,
// Forward and reload land on the same view, links are plain <a href>.
//
//   src/routes.ts       the routes: { path: '/posts/:slug', component, title }
//   <a href="/posts/hello-world">   a link; startRouter() takes the click
//   <BackLink href="/posts">All posts</BackLink>
//                       back through history when the previous entry is
//                       this app's, else to the parent (src/BackLink.svelte)
//   searchParam('status', 'all'), setSearch({ status: 'draft' })
//                       a filter in the address; setSearch replaces the
//                       history entry, navigate() pushes one
//   setTitle(post.title)
//                       the document title, after data arrives
//
// The router numbers the entries it writes (history.state.lidzaIndex):
// 0 is the entry the app was opened on, so BackLink knows whether Back
// stays in the app. Each entry keeps its scroll position: a new view
// starts at the top (or at its #anchor), Back and Forward return to where
// it was. Paths under /api are never routes.

import { tick, type Component } from 'svelte'

export interface Route {
  /** A path with :name segments: '/items/:slug/specs'. */
  path: string
  /** The page, with or without a params prop: it receives the path's parameters. */
  component: Component<any>
  /** The document title's parts before the app's name, from the path's parameters. */
  title?: (params: Record<string, string>) => (string | undefined)[]
  /**
   * false: not prerendered, for a view whose first render reads the
   * search parameters (searchParam): its HTML would show the view without
   * them to every address that has them. A route with :params never is.
   */
  prerender?: boolean
}

export interface Match {
  route: Route | undefined
  params: Record<string, string>
}

interface Entry {
  lidzaIndex?: number
  scroll?: [number, number]
}

/** The app's name, the last part of every document title. */
export const appName = '__LIDZA_APP_NAME__'

let routes: Route[] = []

/** The current address; pages read it, the router changes it. */
export const address = $state({ pathname: '/', search: '', hash: '' })

/** match finds the route for a path and its parameters. */
export function match(pathname: string): Match {
  const parts = pathname.split('/').filter(Boolean)
  for (const route of routes) {
    const want = route.path.split('/').filter(Boolean)
    if (want.length !== parts.length) continue
    const params: Record<string, string> = {}
    if (want.every((w, i) => (w.startsWith(':') ? ((params[w.slice(1)] = decodeURIComponent(parts[i])), true) : w === parts[i]))) {
      return { route, params }
    }
  }
  return { route: undefined, params: {} }
}

/** staticPaths are the routes to prerender: no :params, not prerender false. */
export function staticPaths(all: Route[]): string[] {
  return all.filter((r) => !r.path.includes(':') && r.prerender !== false).map((r) => r.path)
}

/** setRoutes installs the route table; the server render passes the path it renders. */
export function setRoutes(all: Route[], path?: string) {
  routes = all
  if (path !== undefined) {
    const url = new URL(path, 'http://localhost')
    Object.assign(address, { pathname: url.pathname, search: url.search, hash: url.hash })
  }
}

/** setTitle sets the document title: "Specs · Oak chair · app", or the app's name alone. */
export function setTitle(...parts: (string | undefined)[]) {
  if (typeof document !== 'undefined') document.title = [...parts.filter(Boolean), appName].join(' · ')
}

function entry(): Entry {
  return (history.state as Entry | null) ?? {}
}

function read() {
  Object.assign(address, { pathname: window.location.pathname, search: window.location.search, hash: window.location.hash })
  const { route, params } = match(address.pathname)
  setTitle(...(route?.title?.(params) ?? []))
}

/** canGoBack says whether the previous history entry is this app's. */
export function canGoBack(): boolean {
  return (entry().lidzaIndex ?? 0) > 0
}

/**
 * navigate goes to an address in the app: a new history entry, or with
 * replace the current one (a filter, a search, a sort, a redirect).
 */
export async function navigate(href: string, options: { replace?: boolean } = {}) {
  const url = new URL(href, window.location.href)
  const index = entry().lidzaIndex ?? 0
  const sameView = url.pathname === window.location.pathname
  if (options.replace) {
    history.replaceState({ ...entry(), lidzaIndex: index }, '', url)
  } else {
    // The entry left keeps where it was scrolled, for Back.
    history.replaceState({ ...entry(), scroll: [scrollX, scrollY] }, '')
    history.pushState({ lidzaIndex: index + 1 }, '', url)
  }
  read()
  if (options.replace && sameView) return
  await tick()
  const anchor = url.hash && document.getElementById(decodeURIComponent(url.hash.slice(1)))
  if (anchor) anchor.scrollIntoView()
  else scrollTo(0, 0)
}

/** back goes back one entry, or to the parent when the app was opened here. */
export function back(parent: string) {
  // lidza:ignore L025 (the previous entry is this app's)
  if (canGoBack()) history.back()
  else void navigate(parent)
}

/** plainClick is a left click without a modifier on a link without a target: one the page may take over. */
export function plainClick(e: MouseEvent, a: HTMLAnchorElement): boolean {
  return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey && !e.defaultPrevented && (!a.target || a.target === '_self') && !a.hasAttribute('download')
}

/** searchParam is a search parameter of the current address, the fallback when absent. */
export function searchParam(name: string, fallback = ''): string {
  return new URLSearchParams(address.search).get(name) ?? fallback
}

/**
 * setSearch changes search parameters, replacing the history entry; a
 * value of '' or undefined, or the fallback given in defaults, is left out
 * of the address. push: true for a change that is a new view (a step).
 */
export function setSearch(patch: Record<string, string | number | undefined>, options: { push?: boolean; defaults?: Record<string, string> } = {}) {
  const p = new URLSearchParams(address.search)
  for (const [k, v] of Object.entries(patch)) {
    if (v === undefined || v === '' || String(v) === options.defaults?.[k]) p.delete(k)
    else p.set(k, String(v))
  }
  const query = p.toString()
  void navigate(address.pathname + (query ? '?' + query : '') + address.hash, { replace: !options.push })
}

/** isActive says whether a link's path is the current one, or a parent of it unless exact. */
export function isActive(href: string, exact = false): boolean {
  const path = address.pathname
  return path === href || (!exact && href !== '/' && path.startsWith(href.endsWith('/') ? href : href + '/'))
}

/**
 * startRouter reads the address and takes over clicks on links to the
 * app's own paths, Back and Forward. main.ts calls it before rendering.
 */
export function startRouter(all: Route[]) {
  routes = all
  history.scrollRestoration = 'manual'
  if (entry().lidzaIndex === undefined) history.replaceState({ ...entry(), lidzaIndex: 0 }, '')
  read()
  addEventListener('popstate', async () => {
    read()
    await tick()
    const [x, y] = entry().scroll ?? [0, 0]
    scrollTo(x, y)
  })
  // A reload keeps the position too.
  addEventListener('pagehide', () => history.replaceState({ ...entry(), scroll: [scrollX, scrollY] }, ''))
  document.addEventListener('click', (e) => {
    const a = (e.target as Element | null)?.closest?.('a')
    if (!a || !a.href || !plainClick(e, a) || a.hasAttribute('data-reload')) return
    const url = new URL(a.href)
    // Another site, the API or a file the server serves: the browser's.
    if (url.origin !== window.location.origin || url.pathname === '/api' || url.pathname.startsWith('/api/') || /\.\w+$/.test(url.pathname)) return
    e.preventDefault()
    void navigate(url.pathname + url.search + url.hash)
  })
}

/** restoreScroll returns a reloaded page to where it was, once it has rendered. */
export async function restoreScroll() {
  await tick()
  const scroll = entry().scroll
  if (scroll) scrollTo(scroll[0], scroll[1])
}
