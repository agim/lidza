import { expect, test } from '@playwright/test'

// The page is prerendered, hydrates without discarding the markup, and
// shows data from /api. Hydration mismatches surface through window's
// error event, not only the console, so both are collected.
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
  const windowErrors = await page.evaluate(() => (window as unknown as { __errors: string[] }).__errors)
  expect(errors).toEqual([])
  expect(windowErrors).toEqual([])
})
