// A signed-in account's display choices (a view, a sort order, a collapsed
// panel) kept by the auth pack (GET and PATCH /api/v1/auth/preferences),
// so they follow the account across browsers and devices. The document
// is read once into the query cache; a change shows at once, is sent in
// order behind the one before it, and is rolled back if the server
// refuses it. Choices that belong in the address (a list's page, search,
// filters: src/list.tsx) or in the component (an open menu) stay there.
//
//   const prefs = usePreferences(
//     { read: api.authPreferences, write: api.authPatchPreferences },
//     session.userId,
//     { 'posts.view': 'list' as 'list' | 'grid', 'posts.showDrafts': false },
//   )
//   <select value={prefs.values['posts.view']} onChange={(e) => prefs.set({ 'posts.view': e.target.value as 'list' | 'grid' })}>
//   await prefs.flush()   // before sign-out, so the last change is not lost
//
// Keys are namespaced per area ("posts.view"), values are JSON; a stored
// value whose type differs from the default's (an old shape) is ignored,
// and `accept` refuses values outside an enumeration. An app with its own
// sign-in routes wraps auth.From(ctx).Preferences and PatchPreferences in
// two routes behind auth.Require() and passes those operations instead.

import { useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

/** The account's preference document as the routes reply with it. */
export interface PreferenceDocument {
  subject: string
  values: Record<string, unknown> | null
}

/** The two operations of the generated client that read and patch it. */
export interface PreferenceSource {
  read: (options?: { signal?: AbortSignal }) => Promise<PreferenceDocument>
  write: (body: { account?: string; values: Record<string, unknown> }) => Promise<PreferenceDocument>
}

export interface PreferenceOptions<T> {
  /** Refuses a stored value for a key: `(key, value) => allowed`; a
   * refused value reads as the default. For enumerations. */
  accept?: (key: keyof T & string, value: unknown) => boolean
}

export interface Preferences<T> {
  /** The defaults under the account's stored choices. */
  values: T
  /** False until the document was read (the values are the defaults). */
  loaded: boolean
  /** Changes one or more choices: shown at once, sent in order. */
  set: (patch: Partial<T>) => void
  /** Removes stored choices, back to the defaults. */
  reset: (...keys: (keyof T & string)[]) => void
  /** Resolves when every change so far is stored (or refused). */
  flush: () => Promise<void>
  /** The last refused write, until the next one succeeds. */
  error: Error | null
}

/** The query key of an account's document, for an invalidation elsewhere. */
export const preferencesKey = (account: string) => ['preferences', account] as const

/** The stored choices of `account` (the signed-in user's id, '' for a
 * visitor: then the choices live in the page until it is left) over
 * `defaults`, and a way to change them. */
export function usePreferences<T extends Record<string, unknown>>(source: PreferenceSource, account: string, defaults: T, options: PreferenceOptions<T> = {}): Preferences<T> {
  const queryClient = useQueryClient()
  const key = preferencesKey(account)
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => source.read({ signal }),
    enabled: account !== '',
    staleTime: Infinity,
    retry: false,
  })
  // Writes go out one after another, so replies arrive in order and the
  // reply of an earlier write never overwrites a later choice: the keys
  // still in flight are laid over every reply.
  const chain = useRef<Promise<void>>(Promise.resolve())
  const pending = useRef<Record<string, unknown>>({})
  const errorRef = useRef<Error | null>(null)
  const { accept } = options

  const stored = query.data?.values ?? {}
  const values = { ...defaults } as Record<string, unknown>
  for (const name of Object.keys(defaults)) {
    const value = stored[name]
    if (value === undefined || value === null) continue
    if (typeof value !== typeof defaults[name] || Array.isArray(value) !== Array.isArray(defaults[name])) continue
    if (accept && !accept(name, value)) continue
    values[name] = value
  }

  const apply = (patch: Record<string, unknown>) => {
    queryClient.setQueryData<PreferenceDocument>(key, (doc) => {
      const next = { ...(doc?.values ?? {}) }
      for (const [name, value] of Object.entries(patch)) {
        if (value === null) delete next[name]
        else next[name] = value
      }
      return { subject: doc?.subject ?? account, values: next }
    })
    if (account === '') return
    for (const name of Object.keys(patch)) pending.current[name] = patch[name]
    chain.current = chain.current.then(async () => {
      try {
        const doc = await source.write({ account, values: patch })
        for (const name of Object.keys(patch)) {
          if (pending.current[name] === patch[name]) delete pending.current[name]
        }
        errorRef.current = null
        queryClient.setQueryData<PreferenceDocument>(key, () => {
          const next = { ...(doc.values ?? {}) }
          for (const [name, value] of Object.entries(pending.current)) {
            if (value === null) delete next[name]
            else next[name] = value
          }
          return { subject: doc.subject, values: next }
        })
      } catch (err) {
        // The server's document is the truth again: the refused change
        // disappears from the page with the refetch.
        for (const name of Object.keys(patch)) delete pending.current[name]
        errorRef.current = err instanceof Error ? err : new Error(String(err))
        await queryClient.invalidateQueries({ queryKey: key })
      }
    })
  }

  return {
    values: values as T,
    loaded: account === '' || query.isSuccess,
    set: (patch) => apply(patch),
    reset: (...keys) => apply(Object.fromEntries(keys.map((name) => [name, null]))),
    flush: () => chain.current,
    error: errorRef.current,
  }
}
