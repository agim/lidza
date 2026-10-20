import { expect, test } from '@playwright/test'
import { expectNoSidewaysScroll } from './layout'

// On a phone the admin pages' tables stack, a block of label and value
// per row: neither the page nor a table scrolls sideways.
test('admin tables stack on a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  // ADMIN_USERS in .env.test, with the Go tests' password: registered
  // by an earlier run or by them, or now.
  await page.getByLabel('Email').fill('admin@example.com')
  await page.getByLabel('Password').fill('correct horse battery staple')
  await page.getByRole('button', { name: 'Register' }).click()
  const notes = page.getByRole('heading', { name: 'Your notes' })
  if (!(await notes.isVisible({ timeout: 2000 }).catch(() => false))) {
    await page.getByRole('button', { name: 'Sign in' }).click()
  }
  await expect(notes).toBeVisible()

  for (const path of ['/admin/', '/admin/users', '/admin/mail', '/admin/llm', '/admin/storage']) {
    await page.goto(path)
    await expectNoSidewaysScroll(page)
    const tables = page.locator('table.table')
    for (let i = 0; i < (await tables.count()); i++) {
      const table = tables.nth(i)
      await expect(table, `${path}: table ${i} stacks`).toHaveClass(/admin-stack/)
      const box = table.locator('xpath=ancestor::div[contains(@class,"table-responsive")][1]')
      if ((await box.count()) > 0) {
        const sideways = await box.evaluate((el) => el.scrollWidth - el.clientWidth)
        expect(sideways, `${path}: table ${i} scrolls sideways`).toBeLessThanOrEqual(0)
      }
      // No value starts under its label.
      const overlaps = await table.evaluate((el) =>
        Array.from(el.querySelectorAll('tbody td')).filter((td) => {
          const label = getComputedStyle(td, '::before')
          if (label.content === 'none' || label.content === 'normal') return false
          const labelRight = td.getBoundingClientRect().left + parseFloat(label.left) + parseFloat(label.width)
          return Array.from(td.children).some((child) => child.getBoundingClientRect().left < labelRight - 1)
        }).length,
      )
      expect(overlaps, `${path}: table ${i} values under their labels`).toBe(0)
      const cell = table.locator('tbody td[data-label]:not([data-label=""])').first()
      if ((await cell.count()) > 0) {
        expect(await cell.evaluate((el) => getComputedStyle(el).display), `${path}: cells stack`).toBe('block')
      }
    }
  }
})
