import { getRouteApi, Link, Outlet } from '@tanstack/react-router'
import { BackLink, Breadcrumbs, useTitle } from '../../navigation'
import { Page } from '../../ui'
import { findItem } from './data'

// One item at /items/<slug>, its tabs as child routes: /items/<slug> is
// the overview, /items/<slug>/specs the specs. A tab is a link, so it
// pushes a history entry and Back returns to the tab before; a reload or
// a shared link opens the same tab. The tab (the leaf route) sets the
// title; a layout around routes never does, or it would override them.
const route = getRouteApi('/items/$slug')

const tab = 'border-b-2 border-transparent px-1 pb-2 text-ink no-underline [&.active]:border-brand [&.active]:font-semibold [&.active]:text-brand'

export function ItemPage() {
  const { slug } = route.useParams()
  const item = findItem(slug)
  if (!item) return <ItemNotFound />
  return (
    <Page
      title={item.name}
      above={
        /* Where this view sits: each ancestor a link to its own address. */
        <Breadcrumbs>
          <Link to="/items">Items</Link>
          <span>{item.name}</span>
        </Breadcrumbs>
      }
      actions={
        /* Where the visitor was: the list, filtered as they left it; /items when opened directly. */
        <BackLink to="/items" className="text-sm text-muted hover:text-brand">
          All items
        </BackLink>
      }
    >
      <nav aria-label="Item sections" className="flex gap-4 border-b border-line">
        <Link to="/items/$slug" params={{ slug }} activeOptions={{ exact: true }} className={tab}>
          Overview
        </Link>
        <Link to="/items/$slug/specs" params={{ slug }} className={tab}>
          Specs
        </Link>
      </nav>
      <Outlet />
    </Page>
  )
}

// An unknown slug is a page too: it says so and links to the parent.
function ItemNotFound() {
  useTitle('Not found')
  return (
    <Page title="No item at this address">
      <Link to="/items" className="text-brand">
        All items
      </Link>
    </Page>
  )
}

export function ItemOverview() {
  const { slug } = route.useParams()
  const item = findItem(slug)
  useTitle(item?.name)
  return <p>{item?.summary}</p>
}

export function ItemSpecs() {
  const { slug } = route.useParams()
  const item = findItem(slug)
  useTitle('Specs', item?.name)
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
      {item?.specs.map((s) => (
        <div key={s.label} className="contents">
          <dt className="text-muted">{s.label}</dt>
          <dd>{s.value}</dd>
        </div>
      ))}
    </dl>
  )
}
