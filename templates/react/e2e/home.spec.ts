import { expect, test } from '@playwright/test'
import { expectNoSidewaysScroll } from './layout'

// The home page is prerendered, hydrates without discarding the markup,
// and shows data from /api. React reports hydration mismatches through
// window's error event, not the console, so both are collected.
test('home renders and loads data from the API', async ({ page }) => {
  const errors: string[] = []
  await page.addInitScript(() => {
    ;(window as unknown as { __errors: string[] }).__errors = []
    window.addEventListener('error', (event) => {
      ;(window as unknown as { __errors: string[] }).__errors.push(String(event.error?.message ?? event.message))
    })
  })
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(message.text())
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await expect(page.getByText('hello, world')).toBeVisible()
  await page.getByRole('link', { name: 'About' }).click()
  await expect(page.getByRole('heading', { name: 'About' })).toBeVisible()
  const windowErrors = await page.evaluate(() => (window as unknown as { __errors: string[] }).__errors)
  expect(errors).toEqual([])
  expect(windowErrors).toEqual([])
})

// Nothing scrolls sideways on a phone: lidza audit layout checks every
// page this way.
test('home fits a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await expectNoSidewaysScroll(page)
})

// /about is static (staticData.static in src/router.tsx): it arrives with
// no script at all, no React, router or query code, and reads the same.
test('about ships without the client runtime', async ({ page }) => {
  const scripts: string[] = []
  page.on('request', (request) => {
    if (request.resourceType() === 'script') scripts.push(request.url())
  })
  await page.goto('/about')
  await expect(page.getByRole('heading', { name: 'About' })).toBeVisible()
  await expect(page.locator('script:not([type="application/ld+json"])')).toHaveCount(0)
  expect(scripts).toEqual([])
})
