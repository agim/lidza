# Navigation

Every view of a Līdza app has an address, and the browser's Back,
Forward, reload, bookmarks and shared links work on it as on any
website. The app guide's "Navigation" section carries the rules every
generated app follows, the recipe "Keep a view in the address" the
steps, and `lidza check` L025 flags the usual breaks. This page says why,
what each template ships, and how an existing app moves over.

## Why

An app written by an agent drifts toward state the address does not
hold: a tab, a filter or a wizard's step in component state, so Back
leaves the page; a "Back" link fixed to one page, so it lands somewhere
else than the browser's Back; a `#/path` router, so the server never sees
the route and a shared link opens the home page. Each one works in a
demo and fails the first time someone reloads, opens a link in a new tab
or presses Back. The rules are written into the guidance every app
inherits, the templates show them, and a check catches what slips.

## The rules

1. A screen or a record has a path with readable slugs
   (`/projects/acme-site/settings/members`); a record is found by its
   slug in a path segment, not by a hash or `?id=`. Slugs are lowercase
   letters, digits and hyphens: the binary answers a last segment that
   ends like a file (`node.js`) as a missing file.
2. What picks the view is in the address: a view of its own (a tab, a
   step, a record over a list) as a path segment, how a view is shown (a
   filter, a search, a sort, a page, the selected row) as a search
   parameter. Defaults stay out of the address.
3. A new view pushes a history entry; a filter, typing, sorting, paging
   and a redirect to a default replace it.
4. No hash routing. The fragment is for an in-page anchor and for a
   secret the server must not log (an invitation or share token), read
   once and dropped with `history.replaceState`.
5. Links are `<a href>`; buttons are for actions.
6. A back link goes back through history when the previous entry is the
   app's, and to the parent it names otherwise; it renders the parent's
   `<a href>`.
7. The document title follows the view.
8. A new view starts at the top; Back and Forward restore the scroll.
9. A deep link and a reload land on the same view; an unknown slug shows
   a not-found view linking to the parent.
10. A breadcrumb trail is links: every crumb but the last is an `<a href>`
   to that ancestor's address, the last is the view itself marked
   `aria-current="page"`, in a `<nav aria-label="Breadcrumb">`. The trail
   is the app's structure; it does not replace the back link, which is
   the visitor's history.

## What each template ships

| | Routes | Search parameters | Back link | Title | Scroll |
|---|---|---|---|---|---|
| react | TanStack Router, `src/router.tsx` | `validateSearch` with `searchText`, `searchChoice`, `searchNumber` and `stripSearchParams`; `useListState` for lists | `BackLink` and `Breadcrumbs` in `src/navigation.tsx` (TanStack's `useCanGoBack`) | `useTitle` | `scrollRestoration: true` |
| svelte | `src/router.svelte.ts` over `src/routes.ts`; plain `<a href>` | `searchParam`, `setSearch` | `src/BackLink.svelte` (the router numbers its entries in `history.state`) | the route's `title`, `setTitle` | kept per entry by the router |
| astro | a page per address, `getStaticPaths` | a `GET` form, `history.replaceState` in the page's script | `src/components/BackLink.astro`, `src/back.ts` (the Navigation API, else a same-origin referrer) | `<Base title>` | the browser |
| htmx | Go routes in `pages.go` | a `GET` form with `hx-replace-url`; `hx-push-url` for a new view | `data-back` on a link, `static/back.js` | `"Title"` in the page data | the browser |

Each template's items pages are the example: a list filtered by a search
parameter (`/items?category=seating`), an item at `/items/<slug>` with a
breadcrumb trail and a back link, and its tabs as paths (`/items/<slug>/specs`). The react and
svelte templates test them in `e2e/navigation.spec.ts` (filter in the
URL, back link to the filtered list, Back after a filter, tab and reload,
deep link, scroll, unknown slug); the astro template in its own
`e2e/navigation.spec.ts`; the htmx template in `tests/pages_test.go`.

## The server

The binary serves `/api` and every path under it to the API, an unknown
one included (its JSON 404), and every other path to the frontend:

- a prerendered page at `<path>/index.html`;
- a file the build has, and a 404 for a file it lacks (`/assets/old.js`,
  `/robots.txt`);
- the shell (`dist/.server/index.html`) for any other path of a client
  router: the react and svelte templates keep one;
- for a build of static pages with no shell and a `404.html` (the astro
  template), that page with status 404.

A route whose first render reads search parameters is not prerendered:
its HTML would show the view without them to every address that has
them, and hydration would not match. The react template leaves out any
route with `validateSearch` (and any with `staticData: { prerender:
false }`); the svelte template any route with `prerender: false`.

## L025

`lidza check` warns, in `src/`, the views and the static scripts (not in
`e2e/`, `tests/`, dependencies, builds or minified files), on:

- hash routing: an assignment to `location.hash`, a `hashchange`
  listener, a hash history or router, a link to `#/...`; reading the
  fragment is fine;
- `history.back()` or `history.go(-1)` by hand;
- a link labelled "Back" to one fixed page.

A comment line is skipped; `lidza:ignore L025` on the line or the line
before exempts it (the helpers themselves use it).

## Moving an existing app

`lidza update` brings the guide, the recipes, the skills and the
framework block of `AGENTS.md`, so the next agent session follows the
rules; `lidza check` then lists the L025 findings. The helpers are the
template's files, not the framework's, so an app takes them:

- react: `lidza snippet navigation > src/navigation.tsx` (set
  `appName`), and `scrollRestoration: true` in `createRouter`. Replace
  the `prerender` filter in `src/entry-server.tsx`'s `staticPaths` and
  `src/list.tsx` with a new app's (`lidza new tmp`) for the prerender
  rule and the router-aware `useListState`.
- svelte: take `src/router.svelte.ts`, `src/routes.ts`,
  `src/BackLink.svelte`, `src/App.svelte`, `src/main.ts`,
  `src/entry-server.ts` and `scripts/prerender.mjs` from a new app
  (`lidza new tmp --template svelte`) and move the views into
  `src/pages/`.
- astro: take `src/back.ts`, `src/components/BackLink.astro` and the
  layout's script lines, and add `src/pages/404.astro`.
- htmx: take `static/back.js` and its `<script>` line in the layout.

Then, view by view:

- Tab, step or open record in component state: give it a path segment
  (a child route in react, a route in `src/routes.ts` in svelte, a page in
  astro, a Go route in htmx) and make the tab a link to it.
- Filter, search, sort or page in component state: move it to a search
  parameter read on the first render (`validateSearch` and `useSearch`
  in react, `searchParam` in svelte, `r.URL.Query()` in htmx), and write
  it with replace (`navigate({ search, replace: true })`, `setSearch`,
  `hx-replace-url`).
- A record addressed by id in the hash or the query: a slug in the path,
  stored on the row.
- A "Back" link fixed to one page, or `history.back()`: `BackLink` with
  the parent's path, labelled with the parent's name.
- Hash routing: a path per route; keep a fragment only for an in-page
  anchor or a secret.
- Add a browser test per area: deep link and reload, filter in the URL,
  back link and `page.goBack()`.
