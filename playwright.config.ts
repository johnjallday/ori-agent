import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for Ori Agent frontend validation.
 *
 * Usage:
 *   npx playwright test                    # Run all tests
 *   npx playwright test tests/smoke.spec.ts # Run specific test
 *   npx playwright test --ui               # Open interactive UI
 *   npx playwright test --headed           # Run with visible browser
 */
export default defineConfig({
  testDir: './tests',

  // Run tests in parallel
  fullyParallel: true,

  // Fail the build on CI if you accidentally left test.only in the source code
  forbidOnly: !!process.env.CI,

  // Retry on CI only
  retries: process.env.CI ? 2 : 0,

  // Limit parallel workers on CI
  workers: process.env.CI ? 1 : undefined,

  // Reporter to use
  reporter: [
    ['list'],
    ['html', { open: 'never' }]
  ],

  // Shared settings for all projects
  use: {
    // Base URL for the Ori Agent server
    baseURL: process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:8765',

    // Collect trace on failure for debugging
    trace: 'on-first-retry',

    // Screenshot on failure
    screenshot: 'only-on-failure',
  },

  // Configure projects for different browsers
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    // Uncomment to test on more browsers:
    // {
    //   name: 'firefox',
    //   use: { ...devices['Desktop Firefox'] },
    // },
    // {
    //   name: 'webkit',
    //   use: { ...devices['Desktop Safari'] },
    // },
  ],

  // Run the local server before starting tests (optional)
  // Uncomment if you want tests to auto-start the server
  // webServer: {
  //   command: 'go run ./cmd/server',
  //   url: 'http://localhost:8765',
  //   reuseExistingServer: !process.env.CI,
  //   timeout: 120 * 1000,
  // },
});
