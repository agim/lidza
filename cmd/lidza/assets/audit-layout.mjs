// lidza audit layout: visits each route at each viewport and theme and
// measures what scrolls. Written into .lidza/ and run with the app's own
// Playwright; the CLI reads the JSON it prints.
import { chromium } from '@playwright/test'

const base = process.env.BASE_URL
const routes = JSON.parse(process.env.AUDIT_ROUTES)
const viewports = JSON.parse(process.env.AUDIT_VIEWPORTS)
const themes = JSON.parse(process.env.AUDIT_THEMES)
const signIn = process.env.AUDIT_SIGN_IN === '1'

const browser = await chromium.launch()
const results = []
let signedIn = false
for (const theme of themes) {
  for (const [width, height] of viewports) {
    const context = await browser.newContext({ viewport: { width, height }, colorScheme: theme })
    if (signIn) {
      // A throwaway user in the test database: the pages behind sign-in
      // are measured as a user sees them.
      const email = `audit-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@example.com`
      const res = await context.request.post(base + '/api/v1/auth/register', {
        data: { email, password: 'audit-layout-password', name: 'Layout audit' },
      })
      signedIn = res.ok()
    }
    const page = await context.newPage()
    for (const route of routes) {
      let status = 0
      try {
        const res = await page.goto(base + route, { waitUntil: 'networkidle', timeout: 30000 })
        status = res ? res.status() : 0
      } catch (e) {
        results.push({ route, viewport: `${width}x${height}`, theme, status: 0, error: String(e.message || e).split('\n')[0] })
        continue
      }
      await page.waitForTimeout(150)
      const m = await page.evaluate(() => {
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
        // The elements past the right edge whose children are not: the
        // ones to fix, not their containers.
        const over = []
        for (const el of document.querySelectorAll('body *')) {
          const r = el.getBoundingClientRect()
          if (r.width === 0 || r.right <= vw + 1) continue
          const pos = getComputedStyle(el).position
          if (pos === 'fixed') continue
          let child = false
          for (const c of el.children) {
            if (c.getBoundingClientRect().right > vw + 1) {
              child = true
              break
            }
          }
          if (!child) over.push(`${describe(el)} (${Math.round(r.right - vw)}px)`)
          if (over.length >= 5) break
        }
        return { sw: root.scrollWidth, cw: root.clientWidth, sh: root.scrollHeight, ch: root.clientHeight, over }
      })
      results.push({
        route,
        viewport: `${width}x${height}`,
        theme,
        status,
        sideways: Math.max(0, m.sw - m.cw),
        vertical: Math.max(0, m.sh - m.ch),
        offenders: m.over,
      })
    }
    await context.close()
  }
}
await browser.close()
console.log(JSON.stringify({ signedIn, results }))
