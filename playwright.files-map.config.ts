import { defineConfig, devices } from '@playwright/test';

// scripts/test-search-first.py supplies a distinct marked profile per browser
// to the first four specs; the last three run on any isolated server.
export default defineConfig({
  testDir: './tests',
  testMatch: [
    'files-map-navigation.spec.ts',
    'files-search-folders.spec.ts',
    'folder-backed-search-locations.spec.ts',
    'folder-setup-parity.spec.ts',
    // Run on any isolated server (./scripts/e2e-fresh.sh <spec> -- --config
    // playwright.files-map.config.ts --project webkit): they need no marked profile.
    'files-name-search.spec.ts',
    'files-search.a11y.spec.ts',
    'home-file-tree.spec.ts'
  ],
  workers: 1,
  retries: 0,
  timeout: 90000,
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL, screenshot: 'only-on-failure' },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'], colorScheme: 'light' } },
    { name: 'webkit', use: { ...devices['Desktop Safari'], colorScheme: 'dark' } }
  ]
});
