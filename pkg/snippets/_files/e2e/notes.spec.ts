import { expect, test } from '@playwright/test'
import { expectNoSidewaysScroll } from './layout'

// Register, add a note, see it after a reload (the session is a cookie),
// sign out. React reports hydration mismatches through window's error
// event, not the console, so both are collected and must stay empty.
test('register, add a note, keep it across a reload, sign out', async ({ page }) => {
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
  await page.getByLabel('Email').fill(`e2e-${Date.now()}@example.com`)
  await page.getByLabel('Password').fill('correct horse battery')
  await page.getByRole('button', { name: 'Register' }).click()
  await expect(page.getByRole('heading', { name: 'Your notes' })).toBeVisible()

  await page.getByLabel('Title').fill('Buy milk')
  await page.getByRole('button', { name: 'Add note' }).click()
  await expect(page.getByText('Buy milk')).toBeVisible()

  await page.reload()
  await expect(page.getByText('Buy milk')).toBeVisible()

  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('button', { name: 'Register' })).toBeVisible()

  const windowErrors = await page.evaluate(() => (window as unknown as { __errors: string[] }).__errors)
  expect(errors).toEqual([])
  expect(windowErrors).toEqual([])
})

// Nothing scrolls sideways on a phone (lidza audit layout checks every
// page this way).
test('the home page fits a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await expectNoSidewaysScroll(page)
})
