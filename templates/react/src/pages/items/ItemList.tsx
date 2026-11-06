import { getRouteApi, Link } from '@tanstack/react-router'
import { useTitle } from '../../navigation'
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
    <div className="space-y-4">
      <h1 className="text-3xl font-semibold tracking-tight">Items</h1>
      <div className="flex flex-wrap gap-2">
        <input
          type="search"
          aria-label="Search items"
          placeholder="Search items"
          value={q}
          onChange={(e) => navigate({ search: (prev) => ({ ...prev, q: e.target.value }), replace: true })}
          className="min-w-48 flex-1 rounded border border-line bg-white px-2 py-1 text-sm"
        />
        <select
          aria-label="Category"
          value={category}
          onChange={(e) => navigate({ search: (prev) => ({ ...prev, category: e.target.value as typeof category }), replace: true })}
          className="rounded border border-line bg-white px-2 py-1 text-sm"
        >
          <option value="all">Category: All</option>
          {categories.map((c) => (
            <option key={c} value={c}>
              Category: {c}
            </option>
          ))}
        </select>
      </div>
      <ul className="divide-y divide-line rounded-lg border border-line bg-white">
        {shown.map((item) => (
          <li key={item.slug} className="px-4 py-3">
            <Link to="/items/$slug" params={{ slug: item.slug }} className="font-medium text-ink hover:text-brand">
              {item.name}
            </Link>
            <p className="text-sm text-muted">{item.summary}</p>
          </li>
        ))}
        {shown.length === 0 && <li className="px-4 py-3 text-muted">No items match.</li>}
      </ul>
    </div>
  )
}
