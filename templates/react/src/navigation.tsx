// Browser navigation, the conventions in docs/lidza-guide.md
// ("Navigation"): every view has an address, Back and reload land on the
// same view, and links are links.
//
//   <BackLink to="/posts">All posts</BackLink>
//     back through history when the previous entry is this app's (the
//     list with its filters and scroll, as the browser's Back), else to
//     the parent; an <a href="/posts">, so a new tab opens the parent.
//   useTitle(post.title)
//     the document title follows the route: "Hello world · app".
//   <Breadcrumbs><Link to="/posts">Posts</Link><span>{post.title}</span></Breadcrumbs>
//     the trail of a view's ancestors, each a link to its own address,
//     the view itself last; where a back link is the visitor's history,
//     the trail is the app's structure.
//   validateSearch: (s: { status?: string; q?: string; page?: number } & SearchSchemaInput) =>
//     ({ status: searchChoice(s.status, statuses, 'all'), q: searchText(s.q), page: searchNumber(s.page, 1) })
//     a route's search parameters, typed, each optional in a link (the
//     input type, marked SearchSchemaInput, says so); read them with
//     getRouteApi('/posts').useSearch() and change them with
//     navigate({ search: (prev) => ({ ...prev, status }), replace: true }).
//
// A filter, a search box, a sort or a page replaces the history entry;
// another view (a record, a tab, a wizard step) is a link and pushes one.

import { Children, cloneElement, forwardRef, isValidElement, useEffect, type AnchorHTMLAttributes, type MouseEvent, type ReactNode } from 'react'
import { createLink, useCanGoBack, useRouter } from '@tanstack/react-router'

/** The app's name, the last part of every document title. */
export const appName = '__LIDZA_APP_NAME__'

/** useTitle sets the document title for the page: "Members · Acme site · app", or the app's name alone. */
export function useTitle(...parts: (string | undefined)[]) {
  const title = [...parts.filter(Boolean), appName].join(' · ')
  useEffect(() => {
    document.title = title
  }, [title])
}

/** plainClick is a left click without a modifier: one the page may take over. */
export function plainClick(e: MouseEvent<HTMLAnchorElement>): boolean {
  const target = e.currentTarget.getAttribute('target')
  return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey && !e.defaultPrevented && (!target || target === '_self')
}

const BackAnchor = forwardRef<HTMLAnchorElement, AnchorHTMLAttributes<HTMLAnchorElement>>(function BackAnchor({ onClick, href, children, ...props }, ref) {
  const router = useRouter()
  // The router numbers its history entries in history.state; 0 is the
  // entry the app was opened on (a deep link, a new tab, a reload of it).
  const canGoBack = useCanGoBack()
  return (
    <a
      ref={ref}
      href={href}
      {...props}
      onClick={(e) => {
        if (canGoBack && plainClick(e)) {
          e.preventDefault()
          // lidza:ignore L025 (the previous entry is this app's)
          router.history.back()
          return
        }
        onClick?.(e)
      }}
    >
      {children}
    </a>
  )
})

/**
 * BackLink takes Link's props (`to`, `params`, `search`): the parent it
 * goes to when there is no entry of this app to go back to. It renders
 * that parent's <a href>, so middle-click and "open in new tab" work.
 */
export const BackLink = createLink(BackAnchor)

/** searchText is a search parameter as text, "" when absent. */
export function searchText(value: unknown): string {
  return typeof value === 'string' ? value : typeof value === 'number' ? String(value) : ''
}

/** searchChoice is a search parameter that is one of choices, the fallback otherwise. */
export function searchChoice<T extends string>(value: unknown, choices: readonly T[], fallback: T): T {
  return choices.includes(value as T) ? (value as T) : fallback
}

/** searchNumber is a whole number at least 1 (a page), the fallback otherwise. */
export function searchNumber(value: unknown, fallback: number): number {
  const n = typeof value === 'number' ? value : Number(value)
  return Number.isInteger(n) && n >= 1 ? n : fallback
}

/**
 * Breadcrumbs is the trail from the app's root to this view: every crumb
 * but the last is a `Link` to that ancestor's address (never text, never
 * a button), the last is the view itself, marked the current page. Place
 * it above the heading. It does not replace a back link: the trail is
 * where the view sits, the back link where the visitor was.
 */
export function Breadcrumbs({ children, className }: { children: ReactNode; className?: string }) {
  const crumbs = Children.toArray(children)
  return (
    <nav aria-label="Breadcrumb" className={className}>
      <ol className="flex flex-wrap items-center gap-1 text-sm text-muted">
        {crumbs.map((crumb, i) => {
          const last = i === crumbs.length - 1
          const current = last && isValidElement<{ 'aria-current'?: string; className?: string }>(crumb) ? cloneElement(crumb, { 'aria-current': 'page', className: [crumb.props.className, 'text-ink'].filter(Boolean).join(' ') }) : crumb
          return (
            <li key={i} className="flex items-center gap-1">
              {i > 0 && <span aria-hidden="true">/</span>}
              {current}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
