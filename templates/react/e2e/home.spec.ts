import { expect, test } from '@playwright/test'

// The home page is prerendered, hydrates, and shows data from /api.
test('home renders and loads data from the API', async ({ page }) => {
  const errors: string[] = []
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(message.text())
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await expect(page.getByText('hello, world')).toBeVisible()
  await page.getByRole('link', { name: 'About' }).click()
  await expect(page.getByRole('heading', { name: 'About' })).toBeVisible()
  expect(errors).toEqual([])
})
