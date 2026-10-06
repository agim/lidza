import { expect, type Page } from '@playwright/test'

// Layout checks for specs, the ones lidza audit layout runs over every
// page: nothing scrolls sideways (a bug at any width), and, where the
// app's design says so, a page fits its viewport.
//
//   await page.setViewportSize({ width: 390, height: 844 })
//   await page.goto('/settings')
//   await expectNoSidewaysScroll(page)
//   await expectFitsViewport(page) // the app's rule, if it has one

/** overflow is how far the page scrolls each way, in pixels. */
export async function overflow(page: Page): Promise<{ sideways: number; down: number }> {
  return page.evaluate(() => {
    const root = document.scrollingElement ?? document.documentElement
    return { sideways: Math.max(0, root.scrollWidth - root.clientWidth), down: Math.max(0, root.scrollHeight - root.clientHeight) }
  })
}

/** expectNoSidewaysScroll fails when the page is wider than the viewport. */
export async function expectNoSidewaysScroll(page: Page): Promise<void> {
  expect((await overflow(page)).sideways, 'the page scrolls sideways').toBe(0)
}

/** expectFitsViewport fails when the page scrolls down more than allowance pixels. */
export async function expectFitsViewport(page: Page, allowance = 0): Promise<void> {
  expect((await overflow(page)).down, 'the page scrolls down').toBeLessThanOrEqual(allowance)
}
