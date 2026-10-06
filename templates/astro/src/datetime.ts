// Dates and times for pages, in the page's language and the visitor's
// time zone. The API sends instants (a schema time field) in UTC,
// "2026-10-06T14:30:00Z", calendar days (a date field) as "2026-10-06",
// and takes what a datetime-local input holds (a localtime field) as
// typed: the server reads it in the visitor's zone.
//
//   import { formatDate, formatDateTime, formatRelative, toLocalInput, today } from '../datetime'
//   <time dateTime={post.createdAt}>{formatRelative(post.createdAt)}</time>
//   <td>{formatDate(event.day)}</td>              // a day stays that day everywhere
//   <input type="datetime-local" defaultValue={toLocalInput(event.startAt)} />
//
// Format in the browser (an effect, an event, a component that renders
// after load), not while a page is prerendered: the build machine has
// neither the visitor's language nor zone.

let zone: string | undefined

/**
 * setTimeZone makes the helpers use a zone the app stored for the user
 * ("Europe/Paris") instead of the browser's; undefined returns to the
 * browser's.
 */
export function setTimeZone(name?: string): void {
  zone = name
}

/** The zone the helpers use: the one set, else the browser's. */
export function timeZone(): string {
  return zone ?? Intl.DateTimeFormat().resolvedOptions().timeZone
}

function lang(): string {
  return (typeof document !== 'undefined' && document.documentElement.lang) || (typeof navigator !== 'undefined' && navigator.language) || 'en'
}

const dayOnly = /^\d{4}-\d{2}-\d{2}$/

/** A calendar day ("2026-10-06") is read as that day in UTC and shown in UTC, so no zone moves it. */
function parse(value: string | Date): { date: Date; day: boolean } {
  if (value instanceof Date) return { date: value, day: false }
  if (dayOnly.test(value)) return { date: new Date(value + 'T00:00:00Z'), day: true }
  return { date: new Date(value), day: false }
}

function format(value: string | Date, defaults: Intl.DateTimeFormatOptions, options?: Intl.DateTimeFormatOptions): string {
  const { date, day } = parse(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat(lang(), { ...defaults, ...options, timeZone: day ? 'UTC' : options?.timeZone ?? timeZone() }).format(date)
}

/** formatDate shows the day: "Oct 6, 2026" in English. A calendar day is never shifted. */
export function formatDate(value: string | Date, options?: Intl.DateTimeFormatOptions): string {
  return format(value, { dateStyle: 'medium' }, options)
}

/** formatTime shows the time of day in the zone: "2:30 PM". */
export function formatTime(value: string | Date, options?: Intl.DateTimeFormatOptions): string {
  return format(value, { timeStyle: 'short' }, options)
}

/** formatDateTime shows both: "Oct 6, 2026, 2:30 PM". */
export function formatDateTime(value: string | Date, options?: Intl.DateTimeFormatOptions): string {
  return format(value, { dateStyle: 'medium', timeStyle: 'short' }, options)
}

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 86400],
  ['month', 30 * 86400],
  ['week', 7 * 86400],
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
  ['second', 1],
]

/** formatRelative says how far away it is: "3 hours ago", "in 2 days", "now", in the page's language. */
export function formatRelative(value: string | Date, now: Date = new Date()): string {
  const { date } = parse(value)
  if (Number.isNaN(date.getTime())) return ''
  const seconds = Math.round((date.getTime() - now.getTime()) / 1000)
  const rtf = new Intl.RelativeTimeFormat(lang(), { numeric: 'auto' })
  if (Math.abs(seconds) < 45) return rtf.format(0, 'second')
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return rtf.format(Math.trunc(seconds / size), unit)
  }
  return rtf.format(seconds, 'second')
}

function parts(date: Date): Record<string, string> {
  const out: Record<string, string> = {}
  const f = new Intl.DateTimeFormat('en-US', {
    timeZone: timeZone(),
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
  for (const p of f.formatToParts(date)) out[p.type] = p.value
  return out
}

/** toLocalInput turns an instant into a datetime-local input's value in the zone: "2026-10-06T10:30". */
export function toLocalInput(value: string | Date): string {
  const { date } = parse(value)
  if (Number.isNaN(date.getTime())) return ''
  const p = parts(date)
  return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}`
}

/** today is the date in the zone, "2026-10-06": a date input's value. */
export function today(now: Date = new Date()): string {
  const p = parts(now)
  return `${p.year}-${p.month}-${p.day}`
}
