import { expect, test, type Page } from '@playwright/test'

// The navigation conventions (docs/lidza-guide.md, "Navigation") on the
// items pages: every view has an address that survives a reload, the
// filter replaces the history entry, links and tabs push one, and the
// back link goes where the browser's Back would.

function collectErrors(page: Page): string[] {
  const errors: string[] = []
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(message.text())
  })
  page.on('pageerror', (error) => errors.push(error.message))
  return errors
}

test('a filter lives in the address and the back link returns to it', async ({ page }) => {
  const errors = collectErrors(page)
  await page.goto('/')
  await page.getByRole('link', { name: 'Items', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Items' })).toBeVisible()
  await page.getByLabel('Category').selectOption('seating')
  await expect(page).toHaveURL(/\/items\/?\?category=seating$/)
  await expect(page.getByRole('link', { name: 'Desk lamp' })).toBeHidden()

  await page.getByRole('link', { name: 'Oak chair' }).click()
  await expect(page).toHaveURL(/\/items\/oak-chair\/?$/)
  await expect(page).toHaveTitle(/^Oak chair · /)
  const back = page.getByRole('link', { name: 'All items' })
  await expect(back).toHaveAttribute('href', '/items')
  await back.click()
  await expect(page).toHaveURL(/\/items\/?\?category=seating$/)
  await expect(page.getByLabel('Category')).toHaveValue('seating')
  await expect(page.getByRole('link', { name: 'Desk lamp' })).toBeHidden()

  // The filter replaced the entry: Back from the list goes home.
  await page.goBack()
  await expect(page.getByRole('heading', { level: 1 })).not.toHaveText('Items')
  expect(errors).toEqual([])
})

test('a tab is an address and Back returns to the tab before', async ({ page }) => {
  await page.goto('/items/oak-chair')
  await page.getByRole('link', { name: 'Specs' }).click()
  await expect(page).toHaveURL(/\/items\/oak-chair\/specs\/?$/)
  await expect(page).toHaveTitle(/^Specs · Oak chair · /)
  await expect(page.getByText('Seat height')).toBeVisible()
  await page.goBack()
  await expect(page.getByText('A chair in solid oak.')).toBeVisible()
})

test('opened directly, the back link goes to the parent', async ({ page }) => {
  await page.goto('/items/desk-lamp/specs')
  await page.getByRole('link', { name: 'All items' }).click()
  await expect(page).toHaveURL(/\/items\/?$/)
  await expect(page.getByRole('link', { name: 'Desk lamp' })).toBeVisible()
})

test('an unknown address is a 404 page', async ({ page }) => {
  const response = await page.goto('/items/no-such-item')
  expect(response?.status()).toBe(404)
  await expect(page.getByRole('heading', { name: 'Nothing at this address' })).toBeVisible()
})
