import { defineConfig } from '@playwright/test'

// Run by `lidza test --e2e`, which builds the app, starts the binary with
// .env.test and sets BASE_URL. `npx playwright install --with-deps chromium`
// once per machine (or `lidza test --e2e --install`).
export default defineConfig({
  testDir: 'e2e',
  fullyParallel: true,
  retries: process.env.CI ? 2 : 0,
  reporter: 'list',
  use: {
    baseURL: process.env.BASE_URL ?? 'http://127.0.0.1:3000',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
})
