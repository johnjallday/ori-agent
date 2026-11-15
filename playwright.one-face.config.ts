import { defineConfig, devices } from '@playwright/test';

// Isolated server supplied by wt demo / scripts/demo-server.sh.
// Run with scripts/e2e.sh tests/one-face-everywhere.spec.ts -- --config playwright.one-face.config.ts
export default defineConfig({
  testDir: './tests',
  testMatch: 'one-face-everywhere.spec.ts',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:8931',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure'
  },
  projects: ['chromium', 'webkit'].flatMap(browser =>
    (['light', 'dark'] as const).flatMap(colorScheme =>
      [1280, 760].map(width => ({
        name: `${browser}-${colorScheme}-${width}`,
        use: {
          ...devices[browser === 'webkit' ? 'Desktop Safari' : 'Desktop Chrome'],
          colorScheme,
          viewport: { width, height: 1000 }
        }
      }))
    )
  )
});
