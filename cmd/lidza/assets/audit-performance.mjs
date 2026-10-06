// lidza audit performance: loads each route cold on a throttled phone
// (Lighthouse's mobile settings: 4x CPU slowdown, 562.5 ms round trips,
// 1.47 Mbps down), several times, and measures what a visitor waits for
// (FCP, LCP, CLS, TBT), what the page downloads by kind, the JavaScript
// it never runs, and the headers and markup that cost a visitor time.
// Written into .lidza/ and run with the app's own Playwright; the CLI
// applies the budgets to the JSON it prints last.
import { chromium } from '@playwright/test'
import { base, routes, storageState, signIn } from './audit-common.mjs'

const samples = Number(process.env.AUDIT_SAMPLES || 3)
const width = 412
const height = 823
const dpr = 1.75

// Registered before any page script: the observers see every entry.
const observe = () => {
  window.__perf = { lcp: 0, lcpEl: '', cls: 0, long: [] }
  const describe = (el) => {
    if (!el) return ''
    let s = el.tagName.toLowerCase()
    if (el.id) s += '#' + el.id
    if (el.tagName === 'IMG') s += ` src=${(el.currentSrc || el.src || '').split('/').pop()}`
    return s
  }
  new PerformanceObserver((l) => {
    for (const e of l.getEntries()) {
      window.__perf.lcp = e.startTime
      window.__perf.lcpEl = describe(e.element)
      window.__perf.lcpImg = e.element && e.element.tagName === 'IMG' ? { fetchpriority: e.element.getAttribute('fetchpriority') || '', loading: e.element.getAttribute('loading') || '' } : null
    }
  }).observe({ type: 'largest-contentful-paint', buffered: true })
  new PerformanceObserver((l) => {
    for (const e of l.getEntries()) if (!e.hadRecentInput) window.__perf.cls += e.value
  }).observe({ type: 'layout-shift', buffered: true })
  new PerformanceObserver((l) => {
    for (const e of l.getEntries()) window.__perf.long.push([e.startTime, e.duration])
  }).observe({ type: 'longtask', buffered: true })
}

// usedBytes is how much of a script ran: V8's block coverage, nested
// ranges applied after the ranges that hold them.
function usedBytes(entry) {
  const marks = new Uint8Array(entry.source.length)
  const ranges = entry.functions.flatMap((f) => f.ranges)
  ranges.sort((a, b) => a.startOffset - b.startOffset || b.endOffset - a.endOffset)
  for (const r of ranges) marks.fill(r.count > 0 ? 1 : 0, r.startOffset, r.endOffset)
  let used = 0
  for (const m of marks) used += m
  return used
}

const kind = (type, mime, url) => {
  if (type === 'Document') return 'document'
  if (type === 'Script') return 'script'
  if (type === 'Stylesheet') return 'stylesheet'
  if (type === 'Image' || /^image\//.test(mime)) return 'image'
  if (type === 'Font' || /font/.test(mime) || /\.(woff2?|ttf|otf)(\?|$)/.test(url)) return 'font'
  return 'other'
}
const textual = (mime) => /^text\/|javascript|json|xml|svg/.test(mime || '')
const median = (xs) => {
  const s = [...xs].sort((a, b) => a - b)
  return s.length ? s[Math.floor((s.length - 1) / 2)] : 0
}

const browser = await chromium.launch()
const results = []
let signedIn = false
for (const route of routes) {
  const runs = []
  let detail = null
  for (let i = 0; i < samples; i++) {
    const context = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: dpr, isMobile: true, hasTouch: true, storageState })
    signedIn = (await signIn(context)) || signedIn
    const page = await context.newPage()
    await page.addInitScript(observe)
    const cdp = await context.newCDPSession(page)
    await cdp.send('Network.enable')
    await cdp.send('Network.setCacheDisabled', { cacheDisabled: true })
    await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 562.5, downloadThroughput: (1474.56 * 1024) / 8, uploadThroughput: (675 * 1024) / 8 })
    await cdp.send('Emulation.setCPUThrottlingRate', { rate: 4 })
    const requests = new Map()
    cdp.on('Network.responseReceived', (e) => {
      const h = Object.fromEntries(Object.entries(e.response.headers).map(([k, v]) => [k.toLowerCase(), v]))
      requests.set(e.requestId, { url: e.response.url, type: e.type, mime: e.response.mimeType, status: e.response.status, headers: h, bytes: 0, size: 0 })
    })
    cdp.on('Network.dataReceived', (e) => {
      const r = requests.get(e.requestId)
      if (r) r.size += e.dataLength
    })
    cdp.on('Network.loadingFinished', (e) => {
      const r = requests.get(e.requestId)
      if (r) r.bytes = e.encodedDataLength
    })
    const first = i === 0
    if (first) await page.coverage.startJSCoverage({ resetOnNavigation: false })
    let status = 0
    try {
      const res = await page.goto(base + route, { waitUntil: 'load', timeout: 120000 })
      status = res ? res.status() : 0
      await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {})
      await page.waitForTimeout(500)
    } catch (e) {
      results.push({ route, error: String(e.message || e).split('\n')[0] })
      await context.close()
      runs.length = 0
      break
    }
    const m = await page.evaluate(() => {
      const fcp = performance.getEntriesByName('first-contentful-paint')[0]?.startTime ?? 0
      const nav = performance.getEntriesByType('navigation')[0]
      const p = window.__perf
      const tbt = p.long.filter(([start]) => start >= fcp).reduce((sum, [, d]) => sum + Math.max(0, d - 50), 0)
      return { fcp, lcp: p.lcp || fcp, cls: p.cls, tbt, load: nav ? nav.loadEventEnd : 0, lcpEl: p.lcpEl, lcpImg: p.lcpImg }
    })
    runs.push(m)
    if (first) {
      const coverage = await page.coverage.stopJSCoverage()
      const page_ = await page.evaluate(() => {
        const images = [...document.images].map((img) => ({
          src: (img.currentSrc || img.src || '').split('/').pop(),
          natural: img.naturalWidth,
          drawn: Math.round(img.getBoundingClientRect().width),
          sized: img.hasAttribute('width') && img.hasAttribute('height'),
        }))
        const meta = (name) => document.querySelector(`meta[name="${name}"]`)?.getAttribute('content') || ''
        return {
          title: document.title,
          lang: document.documentElement.getAttribute('lang') || '',
          viewport: meta('viewport'),
          description: meta('description'),
          images,
        }
      })
      detail = { status, requests: [...requests.values()], coverage, page: page_, lcpEl: m.lcpEl, lcpImg: m.lcpImg }
    }
    await context.close()
  }
  if (!runs.length || !detail) continue

  const origin = new URL(base).origin
  const bytes = { document: 0, script: 0, stylesheet: 0, image: 0, font: 0, other: 0, total: 0 }
  const findings = []
  for (const r of detail.requests) {
    const k = kind(r.type, r.mime, r.url)
    bytes[k] += r.bytes
    bytes.total += r.bytes
    const own = r.url.startsWith(origin)
    const path = own ? r.url.slice(origin.length) : r.url
    if (own && r.status === 200 && textual(r.mime) && r.size >= 1024 && !r.headers['content-encoding']) {
      findings.push({ level: 'warn', check: 'compression', message: `${path} (${Math.round(r.size / 1024)} KB) is sent uncompressed: build with lidza build, or compress in the proxy` })
    }
    if (own && r.status === 200 && /\/assets\//.test(path) && !/immutable|max-age=(3\d{7}|[4-9]\d{7}|\d{9,})/.test(r.headers['cache-control'] || '')) {
      findings.push({ level: 'warn', check: 'cache', message: `${path} is content-hashed but cached as "${r.headers['cache-control'] || 'nothing'}": serve it with a year's immutable caching` })
    }
  }
  // The JavaScript the page downloads but does not run on load.
  let unusedJS = 0
  for (const c of detail.coverage) {
    if (!c.url.startsWith(origin) || !c.source) continue
    const req = detail.requests.find((r) => r.url === c.url)
    const unusedShare = 1 - usedBytes(c) / c.source.length
    unusedJS += Math.round((req ? req.bytes : c.source.length) * unusedShare)
  }
  if (unusedJS > 20 * 1024) {
    findings.push({ level: 'warn', check: 'unused-js', message: `${Math.round(unusedJS / 1024)} KB of the JavaScript this page downloads does not run on load: load parts on demand (a dynamic import), or render the page without a client runtime` })
  }
  for (const img of detail.page.images) {
    if (img.drawn > 0 && img.natural > img.drawn * dpr * 1.5) {
      findings.push({ level: 'warn', check: 'image-size', message: `${img.src} is ${img.natural}px wide, drawn at ${img.drawn}px: build it responsive (recipe "Add a responsive image")` })
    }
    if (!img.sized) findings.push({ level: 'warn', check: 'image-dimensions', message: `${img.src} has no width and height: the page shifts while it loads` })
  }
  if (detail.lcpImg && (detail.lcpImg.loading === 'lazy' || detail.lcpImg.fetchpriority !== 'high')) {
    findings.push({ level: 'warn', check: 'lcp-image', message: `the largest element, ${detail.lcpEl}, is an image loaded ${detail.lcpImg.loading === 'lazy' ? 'lazily' : 'without fetchpriority="high"'}: give it priority` })
  }
  const p = detail.page
  if (!p.title) findings.push({ level: 'warn', check: 'seo', message: 'the page has no <title>' })
  if (!p.lang) findings.push({ level: 'warn', check: 'seo', message: '<html> has no lang' })
  if (!/width=device-width/.test(p.viewport)) findings.push({ level: 'warn', check: 'seo', message: 'no <meta name="viewport" content="width=device-width, ...">' })
  if (!p.description) findings.push({ level: 'warn', check: 'seo', message: 'no <meta name="description">' })

  results.push({
    route,
    status: detail.status,
    samples: runs.length,
    fcp: Math.round(median(runs.map((r) => r.fcp))),
    lcp: Math.round(median(runs.map((r) => r.lcp))),
    cls: Math.round(median(runs.map((r) => r.cls)) * 1000) / 1000,
    tbt: Math.round(median(runs.map((r) => r.tbt))),
    lcpElement: detail.lcpEl,
    bytes,
    unusedJS,
    findings,
  })
}

// llms.txt: none is fine; served, it is text with an H1.
let llms = { status: 0 }
try {
  const ctx = await browser.newContext()
  const res = await ctx.request.get(base + '/llms.txt')
  const type = res.headers()['content-type'] || ''
  const body = await res.text()
  llms = { status: res.status(), type }
  if (res.status() === 200) {
    if (/html/.test(type) || /^\s*<!doctype|^\s*<html/i.test(body)) llms.problem = 'served as HTML (the app shell?), not text'
    else if (!/^# \S/.test(body)) llms.problem = 'does not start with an H1 ("# Name")'
  }
  await ctx.close()
} catch (e) {
  llms = { status: 0, problem: String(e.message || e).split('\n')[0] }
}
await browser.close()
console.log(JSON.stringify({ signedIn, device: `${width}x${height}@${dpr}x, 4x CPU, slow 4G`, llms, results }))
