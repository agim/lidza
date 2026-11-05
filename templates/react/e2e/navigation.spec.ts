import { expect, test, type Page } from '@playwright/test'

// The navigation conventions (docs/lidza-guide.md, "Navigation") on the
// items pages: every view has an address that survives a reload, filters
// replace the history entry, links and tabs push one, and the back link
// goes where the browser's Back would.

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
  await page.getByRole('combobox', { name: 'Category' }).selectOption('seating')
  await expect(page).toHaveURL(/\/items\?category=seating$/)
  await expect(page.getByRole('link', { name: 'Desk lamp' })).toHaveCount(0)

  await page.getByRole('link', { name: 'Oak chair' }).click()
  await expect(page).toHaveURL(/\/items\/oak-chair$/)
  await expect(page).toHaveTitle(/^Oak chair · /)
  const back = page.getByRole('link', { name: 'All items' })
  await expect(back).toHaveAttribute('href', '/items')
  await back.click()
  await expect(page).toHaveURL(/\/items\?category=seating$/)
  await expect(page.getByRole('combobox', { name: 'Category' })).toHaveValue('seating')

  // The filter replaced the entry: Back from the list goes home.
  await page.goBack()
  await expect(page).toHaveURL(/\/$/)
  expect(errors).toEqual([])
})

test('a tab is an address: reload keeps it, Back returns to the tab before', async ({ page }) => {
  const errors = collectErrors(page)
  await page.goto('/items/oak-chair')
  await page.getByRole('link', { name: 'Specs' }).click()
  await expect(page).toHaveURL(/\/items\/oak-chair\/specs$/)
  await expect(page.getByText('Seat height')).toBeVisible()
  await expect(page).toHaveTitle(/^Specs · Oak chair · /)
  await page.reload()
  await expect(page.getByText('Seat height')).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL(/\/items\/oak-chair$/)
  await expect(page.getByText('A chair in solid oak.')).toBeVisible()
  expect(errors).toEqual([])
})

test('opened directly, the back link goes to the parent', async ({ page }) => {
  await page.goto('/items/desk-lamp/specs?ref=mail')
  await page.getByRole('link', { name: 'All items' }).click()
  await expect(page).toHaveURL(/\/items$/)
  await expect(page.getByRole('link', { name: 'Desk lamp' })).toBeVisible()
})

test('a filtered list opened directly shows the filter', async ({ page }) => {
  const errors = collectErrors(page)
  await page.goto('/items?category=lighting&q=floor')
  await expect(page.getByRole('searchbox', { name: 'Search items' })).toHaveValue('floor')
  await expect(page.getByRole('link', { name: 'Floor lamp' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Desk lamp' })).toHaveCount(0)
  expect(errors).toEqual([])
})

test('Back returns to where the list was scrolled', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 300 })
  await page.goto('/items')
  const last = page.getByRole('link', { name: 'Wall shelf' })
  await last.scrollIntoViewIfNeeded()
  const y = await page.evaluate(() => scrollY)
  expect(y).toBeGreaterThan(0)
  await last.click()
  await expect(page.getByRole('heading', { name: 'Wall shelf' })).toBeVisible()
  await page.getByRole('link', { name: 'All items' }).click()
  await expect(page.getByRole('heading', { name: 'Items' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(y)
})

test('an unknown slug says so and links to the list', async ({ page }) => {
  await page.goto('/items/no-such-item')
  await expect(page.getByRole('heading', { name: 'No item at this address' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'All items' })).toHaveAttribute('href', '/items')
})
