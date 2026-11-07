import { getRouteApi, Link } from '@tanstack/react-router'
import { useTitle } from '../../navigation'
import { Empty, Page, Row, SearchInput, Select } from '../../ui'
import { categories, items } from './data'

// The list's filter and search are search parameters (/items?category=seating&q=oak),
// validated on the route (validateSearch in src/router.tsx), so the view
// can be linked, reloaded and gone back to. Changing them replaces the
// history entry: Back leaves the list instead of undoing each keystroke.
const route = getRouteApi('/items')

export function ItemList() {
  const { category, q } = route.useSearch()
  const navigate = route.useNavigate()
  useTitle('Items')
  const shown = items.filter((item) => (category === 'all' || item.category === category) && item.name.toLowerCase().includes(q.toLowerCase()))

  return (
    <Page title="Items">
      <Row>
        <SearchInput label="Search items" value={q} onChange={(q) => navigate({ search: (prev) => ({ ...prev, q }), replace: true })} className="flex-1" />
        <Select
          label="Category"
          prefix
          value={category}
          options={[{ value: 'all', label: 'All' }, ...categories.map((c) => ({ value: c, label: c }))]}
          onChange={(category) => navigate({ search: (prev) => ({ ...prev, category }), replace: true })}
        />
      </Row>
      <ul className="divide-y divide-line rounded-lg border border-line bg-white">
        {shown.map((item) => (
          <li key={item.slug} className="px-4 py-3">
            <Link to="/items/$slug" params={{ slug: item.slug }} className="font-medium text-ink hover:text-brand">
              {item.name}
            </Link>
            <p className="text-sm text-muted">{item.summary}</p>
          </li>
        ))}
      </ul>
      {shown.length === 0 && <Empty title="No items match." />}
    </Page>
  )
}
