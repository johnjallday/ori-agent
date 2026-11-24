import { defineConfig, devices } from '@playwright/test';

// scripts/test-search-first.py supplies a distinct marked profile per browser.
export default defineConfig({
  testDir: './tests',
  testMatch: 'files-map-navigation.spec.ts',
  workers: 1,
  retries: 0,
  timeout: 90000,
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL, screenshot: 'only-on-failure' },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'], colorScheme: 'light' } },
    { name: 'webkit', use: { ...devices['Desktop Safari'], colorScheme: 'dark' } }
  ]
});
