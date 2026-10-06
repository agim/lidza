// A list page's search, filters, date range, sort and paging, kept in the
// address (so a list can be linked, reloaded and gone back to) and sent to
// a list route that reads them with list.Read (pkg/list): the routes
// lidza gen resource writes do.
//
//   const [state, set] = useListState({ filters: ['status'] })
//   const posts = useQuery({ queryKey: ['posts', state], queryFn: () => api.listPosts({ query: listQuery(state) }) })
//   <ListToolbar state={state} set={set} search="Search posts" range="Created">
//     <FilterSelect label="Status" name="status" options={statuses} state={state} set={set} />
//   </ListToolbar>
//   <table><thead><tr><SortHead label="Title" field="title" state={state} set={set} />…</tr></thead>…</table>
//   <Pager total={posts.data?.total ?? 0} state={state} set={set} />
//
// A list already in the browser pages the same way with pageRows.

import { useEffect, useState, type ReactNode } from 'react'

export interface ListState {
  q: string
  sort: string
  order: '' | 'asc' | 'desc'
  /** Days, "2026-10-06", as the date inputs hold them; inclusive. */
  since: string
  until: string
  offset: number
  limit: number
  filters: Record<string, string>
}

function read(filters: string[], limit: number): ListState {
  const p = new URLSearchParams(typeof location === 'undefined' ? '' : location.search)
  const order = p.get('order')
  return {
    q: p.get('q') ?? '',
    sort: p.get('sort') ?? '',
    order: order === 'asc' || order === 'desc' ? order : '',
    since: p.get('since') ?? '',
    until: p.get('until') ?? '',
    offset: Math.max(0, Number(p.get('offset')) || 0),
    limit: Math.max(1, Number(p.get('limit')) || limit),
    filters: Object.fromEntries(filters.map((f) => [f, p.get(f) ?? ''])),
  }
}

/**
 * useListState is the list's state in the address. set changes part of it
 * and goes back to the first page unless the change is the page itself.
 */
export function useListState(options: { filters?: string[]; limit?: number } = {}): [ListState, (patch: Partial<ListState>) => void] {
  const filters = options.filters ?? []
  const limit = options.limit ?? 50
  const [state, setState] = useState(() => read(filters, limit))
  useEffect(() => {
    const back = () => setState(read(filters, limit))
    addEventListener('popstate', back)
    return () => removeEventListener('popstate', back)
    // The filters' names are fixed for a page.
  }, [])
  const set = (patch: Partial<ListState>) => {
    const next = { ...state, ...patch, filters: { ...state.filters, ...patch.filters } }
    if (patch.offset === undefined) next.offset = 0
    const p = new URLSearchParams(location.search)
    const put = (k: string, v: string | number) => (v === '' || v === 0 || (k === 'limit' && v === limit) ? p.delete(k) : p.set(k, String(v)))
    put('q', next.q)
    put('sort', next.sort)
    put('order', next.order)
    put('since', next.since)
    put('until', next.until)
    put('offset', next.offset)
    put('limit', next.limit)
    for (const [k, v] of Object.entries(next.filters)) put(k, v)
    const search = p.toString()
    history.replaceState(history.state, '', location.pathname + (search ? '?' + search : '') + location.hash)
    setState(next)
  }
  return [state, set]
}

/** The start of a day in the visitor's zone, as an instant. */
function startOf(day: string, addDays = 0): string {
  const d = new Date(day + 'T00:00')
  d.setDate(d.getDate() + addDays)
  return d.toISOString()
}

/**
 * listQuery is the query string of a list route: the days of the range
 * become instants in the visitor's zone, until the end of its last day.
 */
export function listQuery(s: ListState): Record<string, string | number | undefined> {
  const q: Record<string, string | number | undefined> = {
    q: s.q || undefined,
    sort: s.sort || undefined,
    order: s.order || undefined,
    since: s.since ? startOf(s.since) : undefined,
    until: s.until ? startOf(s.until, 1) : undefined,
    offset: s.offset || undefined,
    limit: s.limit,
  }
  for (const [k, v] of Object.entries(s.filters)) if (v) q[k] = v
  return q
}

const field = 'rounded border border-line bg-white px-2 py-1 text-sm'

/**
 * ListToolbar is one compact row: the search (sent as you stop typing),
 * the date range labelled by its field, and the page's filters.
 */
export function ListToolbar(props: {
  state: ListState
  set: (patch: Partial<ListState>) => void
  /** The search box's label; none without it. */
  search?: string
  /** The field the range is over ("Created"); no range without it. */
  range?: string
  children?: ReactNode
}) {
  const { state, set } = props
  const [q, setQ] = useState(state.q)
  useEffect(() => setQ(state.q), [state.q])
  useEffect(() => {
    if (q === state.q) return
    const t = setTimeout(() => set({ q }), 300)
    return () => clearTimeout(t)
    // set changes with every state; q is what drives this.
  }, [q])
  return (
    <div className="flex flex-wrap items-center gap-2">
      {props.search && (
        <input type="search" aria-label={props.search} placeholder={props.search} value={q} onChange={(e) => setQ(e.target.value)} className={field + ' min-w-48 flex-1'} />
      )}
      {props.range && (
        <span className="flex items-center gap-1 text-sm text-muted">
          <label>
            {props.range} from <input type="date" value={state.since} onChange={(e) => set({ since: e.target.value })} className={field} />
          </label>
          <label>
            to <input type="date" value={state.until} min={state.since || undefined} onChange={(e) => set({ until: e.target.value })} className={field} />
          </label>
        </span>
      )}
      {props.children}
    </div>
  )
}

/** FilterSelect is a filter whose trigger reads "Status: Draft", or "Status: All". */
export function FilterSelect(props: {
  label: string
  name: string
  options: { value: string; label: string }[]
  state: ListState
  set: (patch: Partial<ListState>) => void
}) {
  const value = props.state.filters[props.name] ?? ''
  return (
    <select aria-label={props.label} value={value} onChange={(e) => props.set({ filters: { [props.name]: e.target.value } })} className={field}>
      <option value="">{props.label}: All</option>
      {props.options.map((o) => (
        <option key={o.value} value={o.value}>
          {props.label}: {o.label}
        </option>
      ))}
    </select>
  )
}

/** SortHead is a column header that sorts by its field, again to reverse. */
export function SortHead(props: { label: string; field: string; state: ListState; set: (patch: Partial<ListState>) => void }) {
  const { state, set } = props
  const active = state.sort === props.field
  const desc = active && state.order === 'desc'
  return (
    <th aria-sort={active ? (desc ? 'descending' : 'ascending') : 'none'} className="text-left font-medium">
      <button type="button" onClick={() => set({ sort: props.field, order: active && !desc ? 'desc' : 'asc' })} className="hover:text-brand">
        {props.label}
        {active && <span aria-hidden="true">{desc ? ' ↓' : ' ↑'}</span>}
      </button>
    </th>
  )
}

/** Pager says where the page is ("51–100 of 230") with the previous and next pages. */
export function Pager(props: { total: number; state: ListState; set: (patch: Partial<ListState>) => void }) {
  const { total, state, set } = props
  const first = total === 0 ? 0 : state.offset + 1
  const last = Math.min(state.offset + state.limit, total)
  return (
    <nav aria-label="Pages" className="flex items-center justify-between gap-2 text-sm text-muted">
      <span>
        {first}–{last} of {total}
      </span>
      <span className="flex gap-2">
        <button type="button" disabled={state.offset === 0} onClick={() => set({ offset: Math.max(0, state.offset - state.limit) })} className={field + ' disabled:opacity-40'}>
          Previous
        </button>
        <button type="button" disabled={last >= total} onClick={() => set({ offset: state.offset + state.limit })} className={field + ' disabled:opacity-40'}>
          Next
        </button>
      </span>
    </nav>
  )
}

/**
 * pageRows applies the state to a list already in the browser: the search
 * over each row's text, the filters by equality, the sort by the field,
 * then the page. It returns the page and the total that match.
 */
export function pageRows<T extends object>(rows: T[], state: ListState): { items: T[]; total: number } {
  const q = state.q.toLowerCase()
  let out = rows.filter((r) => {
    if (q && !Object.values(r).some((v) => typeof v === 'string' && v.toLowerCase().includes(q))) return false
    return Object.entries(state.filters).every(([k, v]) => !v || String((r as Record<string, unknown>)[k]) === v)
  })
  if (state.sort) {
    const key = state.sort
    const dir = state.order === 'desc' ? -1 : 1
    out = [...out].sort((a, b) => {
      const x = (a as Record<string, unknown>)[key]
      const y = (b as Record<string, unknown>)[key]
      return (x === y ? 0 : x === undefined || x === null ? 1 : y === undefined || y === null ? -1 : x < y ? -1 : 1) * dir
    })
  }
  return { items: out.slice(state.offset, state.offset + state.limit), total: out.length }
}
