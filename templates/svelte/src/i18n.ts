// Translated strings for pages, from the same locales/<lang>.json catalogs
// the i18n pack serves to handlers. Without a locales/ directory every
// function here is a no-op and t() returns the key.
//
// A page is rendered in one locale from the first paint: `npm run build`
// prerenders each page once per locale, the binary serves the visitor's
// (?lang, the lang cookie, Accept-Language, as the i18n pack negotiates),
// and the page carries its catalog, so startI18n() has it before hydration.
//
//   import { t, locale, setLocale } from './i18n'
//   <h1>{t('home.title')}</h1>
//   <p>{t('home.greeting', name)}</p>          <!-- "Hello, %s" -->
//   new Intl.NumberFormat(locale()).format(n)
//   <button onclick={() => setLocale('sq')}>Shqip</button>

export type Messages = Record<string, string>

export interface Catalog {
  locale: string
  messages: Messages
}

// Each catalog is its own chunk: the browser loads one only when the page
// carries none (the dev server).
const files = import.meta.glob<Record<string, unknown>>('../locales/*.json', { import: 'default' })
const byLocale = new Map(Object.entries(files).map(([file, load]) => [file.replace(/^.*\/|\.json$/g, ''), load]))

const scriptId = 'lidza-i18n'
let current: Catalog = { locale: '', messages: {} }

/** The locales the app has, one per locales/<lang>.json. */
export function locales(): string[] {
  return [...byLocale.keys()].sort()
}

/** The locale the page is rendered in; '' without locales. */
export function locale(): string {
  return current.locale
}

/**
 * t returns the message for key in the current locale (nested keys joined
 * with dots, as in the Go pack), replacing %s, %d and %v with args in
 * order; the key itself when the catalog has no such message.
 */
export function t(key: string, ...args: unknown[]): string {
  const message = current.messages[key]
  if (message === undefined) return key
  let n = 0
  return message.replace(/%[sdv%]/g, (verb) => (verb === '%%' ? '%' : String(args[n++] ?? '')))
}

/** match finds tag among the locales: the same tag, then the same language (sq-AL finds sq). */
export function match(tag: string | null | undefined): string | undefined {
  if (!tag) return undefined
  const all = locales()
  const lower = tag.replace('_', '-').toLowerCase()
  const base = (s: string) => s.toLowerCase().split('-')[0]
  return all.find((l) => l.toLowerCase() === lower) ?? all.find((l) => base(l) === base(lower))
}

/** The locale used when nothing matches: en when there is one, else the first. */
export function defaultLocale(): string {
  return match('en') ?? locales()[0] ?? ''
}

/** loadLocale makes lang's catalog current (the default's when lang is unknown). */
export async function loadLocale(lang?: string): Promise<void> {
  const name = match(lang) ?? defaultLocale()
  const load = byLocale.get(name)
  if (!load) return
  const messages: Messages = {}
  flatten('', await load(), messages)
  current = { locale: name, messages }
}

/**
 * localizePage puts the current locale in <html lang> and its catalog in
 * the page (a JSON script, which a Content-Security-Policy does not block),
 * for startI18n in the browser. Used by the server render.
 */
export function localizePage(html: string): string {
  if (!current.locale) return html
  const json = JSON.stringify(current).replace(/</g, '\\u003c')
  const lang = /<html[^>]*\slang="[^"]*"/i.test(html)
    ? html.replace(/(<html[^>]*\slang=")[^"]*"/i, `$1${current.locale}"`)
    : html.replace(/<html/i, `<html lang="${current.locale}"`)
  return lang.replace('</head>', `<script type="application/json" id="${scriptId}">${json}</script></head>`)
}

/**
 * startI18n runs in the browser before the first render: it takes the
 * catalog the page carries, or (dev server, pages rendered in the browser)
 * loads the one for ?lang, the lang cookie or the browser's languages.
 */
export async function startI18n(): Promise<void> {
  const embedded = document.getElementById(scriptId)?.textContent
  if (embedded) {
    current = JSON.parse(embedded) as Catalog
    return
  }
  if (byLocale.size === 0) return
  const cookie = document.cookie.match(/(?:^|;\s*)lang=([^;]*)/)?.[1]
  const wanted = [new URLSearchParams(location.search).get('lang'), cookie && decodeURIComponent(cookie), ...navigator.languages]
  await loadLocale(wanted.map(match).find(Boolean))
  document.documentElement.lang = current.locale
}

/**
 * setLocale stores the choice in the lang cookie, which the binary and the
 * i18n pack read before Accept-Language, and reloads the page in it.
 */
export function setLocale(lang: string): void {
  document.cookie = `lang=${encodeURIComponent(lang)}; path=/; max-age=31536000; SameSite=Lax`
  const url = new URL(location.href)
  if (url.searchParams.has('lang')) {
    url.searchParams.delete('lang')
    location.replace(url)
  } else {
    location.reload()
  }
}

function flatten(prefix: string, value: Record<string, unknown>, out: Messages) {
  for (const [k, v] of Object.entries(value)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v !== null && typeof v === 'object') flatten(key, v as Record<string, unknown>, out)
    else out[key] = String(v)
  }
}
