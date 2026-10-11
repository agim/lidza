// lidza audit layout: visits each route at each viewport and theme and
// measures what scrolls: the document (sideways is a fault) and the
// containers inside it that scroll on their own (reported, not faults).
// With a stability wait it scrolls the document and those containers,
// waits (after an optional trigger, run once), and reports what lost
// its place: a container's position, the document's reading anchor, the
// focus.
// Written into .lidza/ and run with the app's own Playwright; the CLI
// reads the JSON it prints last.
import { chromium } from '@playwright/test'
import { base, routes, storageState, signIn } from './audit-common.mjs'

const viewports = JSON.parse(process.env.AUDIT_VIEWPORTS)
const themes = JSON.parse(process.env.AUDIT_THEMES)
const stabilityMs = Number(process.env.AUDIT_STABILITY_MS || 0)
const trigger = process.env.AUDIT_TRIGGER || ''
const allow = JSON.parse(process.env.AUDIT_ALLOW || 'null') || []

const browser = await chromium.launch()
const results = []
let signedIn = false
for (const theme of themes) {
  for (const [width, height] of viewports) {
    const context = await browser.newContext({ viewport: { width, height }, colorScheme: theme, storageState })
    signedIn = (await signIn(context)) || signedIn
    const page = await context.newPage()
    for (const route of routes) {
      const result = { route, viewport: `${width}x${height}`, theme }
      try {
        const res = await page.goto(base + route, { waitUntil: 'networkidle', timeout: 30000 })
        // No response: the browser moved within the same document (a
        // hash route), which is not an HTTP failure.
        if (res) {
          result.status = res.status()
          result.navigation = 'document'
        } else {
          result.navigation = 'same-document'
        }
      } catch (e) {
        results.push({ ...result, status: 0, navigation: 'failed', error: String(e.message || e).split('\n')[0] })
        continue
      }
      await page.waitForTimeout(150)
      const m = await page.evaluate(measure, allow)
      Object.assign(result, { sideways: m.sideways, vertical: m.vertical, offenders: m.offenders, scrollers: m.scrollers })
      // Every page, nested scrollers or not: a page that scrolls only as
      // a document (a list on a phone) is checked too.
      if (stabilityMs > 0) {
        result.stability = await stability(page, allow)
      }
      results.push(result)
    }
    await context.close()
  }
}
await browser.close()
console.log(JSON.stringify({ signedIn, results }))

// stability scrolls the document and each nested scroller part way,
// focuses a control (without scrolling to it), records where everything
// is and which element the reader is looking at, runs the trigger once
// (or only waits), and reports what moved: a container replaced or
// reset, the document's reading anchor displaced or gone, focus lost,
// and the layout shift meanwhile.
async function stability(page, allow) {
  await page.evaluate(() => {
    window.__lidzaShift = 0
    new PerformanceObserver((list) => {
      for (const e of list.getEntries()) if (!e.hadRecentInput) window.__lidzaShift += e.value
    }).observe({ type: 'layout-shift', buffered: false })
  })
  const before = await page.evaluate((allow) => {
    const scrollers = window.__lidzaScrollers || []
    for (const s of scrollers) {
      const el = s.el
      el.scrollLeft = el.scrollWidth - el.clientWidth > 1 ? Math.round((el.scrollWidth - el.clientWidth) / 2) : 0
      el.scrollTop = el.scrollHeight - el.clientHeight > 1 ? Math.round((el.scrollHeight - el.clientHeight) / 2) : 0
    }
    // The document, part way down, as a reader in the middle of a list.
    const root = document.scrollingElement || document.documentElement
    const maxY = root.scrollHeight - root.clientHeight
    if (maxY > 1) window.scrollTo(window.scrollX, Math.round(maxY / 2))
    // Focus a control as a user reaching a row's link would: in the
    // first scroller, else the first one in view. preventScroll, so the
    // focus itself moves nothing the baseline then records.
    const controls = 'a[href], button, input, select, textarea, [tabindex]'
    const inView = (el) => {
      const r = el.getBoundingClientRect()
      return r.bottom > 0 && r.top < innerHeight && r.width > 0 && r.height > 0
    }
    const first = scrollers[0]
    const focusable = (first && first.el.querySelector(controls)) || [...document.querySelectorAll(controls)].find(inView) || null
    if (focusable) focusable.focus({ preventScroll: true })
    window.__lidzaFocus = focusable
    window.__lidzaNodes = scrollers.map((s) => s.el)
    // The reading anchor: the element at the reader's eye line (a third
    // down the viewport), climbed to one that holds its own text, with
    // where it sits on screen.
    let anchor = null
    for (const y of [innerHeight / 3, innerHeight / 2, innerHeight / 5]) {
      let el = document.elementFromPoint(innerWidth / 2, y)
      while (el && el !== document.body && (el.textContent || '').trim().length === 0) el = el.parentElement
      if (el && el !== document.body && el !== document.documentElement) {
        anchor = el
        break
      }
    }
    window.__lidzaAnchor = anchor
    const text = anchor ? (anchor.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 80) : ''
    return {
      scrollers: scrollers.map((s) => ({
        path: s.path, selector: s.selector, x: s.el.scrollLeft, y: s.el.scrollTop,
        allowed: allow.some((a) => s.el.matches(a)) || s.el.closest('[data-audit-follow]') !== null,
      })),
      document: {
        x: Math.round(window.scrollX), y: Math.round(window.scrollY), scrolls: maxY > 1,
        allowed: allow.some((a) => root.matches(a) || document.body.matches(a)) || document.body.hasAttribute('data-audit-follow') || root.hasAttribute('data-audit-follow'),
        anchor: anchor ? { tag: anchor.tagName, text, top: Math.round(anchor.getBoundingClientRect().top), label: anchor.tagName.toLowerCase() + (anchor.id ? '#' + anchor.id : '') + (text ? ` "${text.slice(0, 40)}"` : '') } : null,
      },
    }
  }, allow)
  let triggered = false
  if (trigger) {
    try {
      await page.evaluate(trigger)
      triggered = true
    } catch (e) {
      return { error: 'trigger failed: ' + String(e.message || e).split('\n')[0], scrollers: before.scrollers.length }
    }
  }
  await page.waitForTimeout(stabilityMs)
  return await page.evaluate(({ before, triggered }) => {
    const resets = []
    const replaced = []
    before.scrollers.forEach((b, i) => {
      const old = window.__lidzaNodes[i]
      const el = old && old.isConnected ? old : document.querySelector(b.path)
      if (old !== el) replaced.push(b.selector)
      if (!el) return
      const moved = Math.abs(el.scrollLeft - b.x) > 1 || Math.abs(el.scrollTop - b.y) > 1
      if (moved && !b.allowed) {
        resets.push({ selector: b.selector, from: [b.x, b.y], to: [Math.round(el.scrollLeft), Math.round(el.scrollTop)] })
      }
    })
    // The document: judged by the reader's anchor, not the offset, so an
    // insertion above that the page compensates for (scroll anchoring, a
    // keyed list) passes while a refresh that throws the reader away
    // fails.
    const d = before.document
    const doc = { from: [d.x, d.y], to: [Math.round(window.scrollX), Math.round(window.scrollY)], anchor: d.anchor ? d.anchor.label : '' }
    if (d.anchor) {
      let el = window.__lidzaAnchor
      if (!el || !el.isConnected) {
        // Re-rendered: the same kind of element with the same text.
        el = [...document.getElementsByTagName(d.anchor.tag)].find((e) => (e.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 80) === d.anchor.text) || null
        doc.anchorReplaced = true
      }
      if (!el) {
        doc.anchorLost = true
      } else {
        doc.anchorShift = Math.round(el.getBoundingClientRect().top) - d.anchor.top
      }
    }
    const offsetMoved = Math.abs(doc.to[0] - doc.from[0]) > 1 || Math.abs(doc.to[1] - doc.from[1]) > 1
    doc.reset = !d.allowed && (doc.anchorLost ? offsetMoved : Math.abs(doc.anchorShift || 0) > 16)
    const f = window.__lidzaFocus
    const focusLost = f !== null && (!f.isConnected || document.activeElement !== f)
    return { triggered, scrollers: before.scrollers.length, resets, replaced, document: doc, focusLost, layoutShift: Math.round(window.__lidzaShift * 1000) / 1000 }
  }, { before, triggered })
}

// measure runs in the page: the document's overflow, the elements past
// its right edge, and the nested containers that scroll.
function measure(allow) {
  const root = document.scrollingElement || document.documentElement
  const vw = root.clientWidth
  const describe = (el) => {
    let s = el.tagName.toLowerCase()
    if (el.id) s += '#' + el.id
    const cls = typeof el.className === 'string' ? el.className.trim().split(/\s+/).filter(Boolean).slice(0, 3) : []
    if (cls.length) s += '.' + cls.join('.')
    const text = (el.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 40)
    return text ? `${s} "${text}"` : s
  }
  // A selector that finds the element again after a re-render that
  // replaced it: ids where there are, else positions.
  const pathOf = (el) => {
    const parts = []
    for (let e = el; e && e !== document.documentElement; e = e.parentElement) {
      if (e.id) {
        parts.unshift('#' + CSS.escape(e.id))
        break
      }
      let i = 1
      for (let s = e.previousElementSibling; s; s = s.previousElementSibling) if (s.tagName === e.tagName) i++
      parts.unshift(`${e.tagName.toLowerCase()}:nth-of-type(${i})`)
    }
    return parts.join(' > ')
  }
  // The elements past the right edge whose children are not: the ones
  // to fix, not their containers. Content inside a scroller is the
  // scroller's, reported below.
  const over = []
  const scrollers = []
  window.__lidzaScrollers = []
  for (const el of document.querySelectorAll('body *')) {
    const st = getComputedStyle(el)
    const scrollsX = /auto|scroll/.test(st.overflowX) && el.scrollWidth - el.clientWidth > 1
    const scrollsY = /auto|scroll/.test(st.overflowY) && el.scrollHeight - el.clientHeight > 1
    if ((scrollsX || scrollsY) && el.clientWidth > 0 && el.clientHeight > 0) {
      const selector = describe(el)
      scrollers.push({
        selector,
        client: [el.clientWidth, el.clientHeight],
        scroll: [el.scrollWidth, el.scrollHeight],
        sideways: scrollsX ? el.scrollWidth - el.clientWidth : 0,
        vertical: scrollsY ? el.scrollHeight - el.clientHeight : 0,
        allowed: allow.some((a) => el.matches(a)) || el.closest('[data-audit-follow]') !== null,
      })
      window.__lidzaScrollers.push({ el, path: pathOf(el), selector })
    }
    if (over.length >= 5) continue
    const r = el.getBoundingClientRect()
    if (r.width === 0 || r.right <= vw + 1 || st.position === 'fixed') continue
    let inside = false
    for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
      if (/auto|scroll|hidden|clip/.test(getComputedStyle(p).overflowX)) {
        inside = true
        break
      }
    }
    if (inside) continue
    let child = false
    for (const c of el.children) {
      if (c.getBoundingClientRect().right > vw + 1) {
        child = true
        break
      }
    }
    if (!child) over.push(`${describe(el)} (${Math.round(r.right - vw)}px)`)
  }
  return {
    sideways: Math.max(0, root.scrollWidth - root.clientWidth),
    vertical: Math.max(0, root.scrollHeight - root.clientHeight),
    offenders: over,
    scrollers: scrollers.slice(0, 20),
  }
}
