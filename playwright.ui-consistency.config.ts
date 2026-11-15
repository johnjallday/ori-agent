import { defineConfig, devices } from '@playwright/test';
import base from './playwright.config';

// Opt-in only: never multiply the default or unrelated provider-backed suites.
export default defineConfig({
  ...base,
  testMatch: 'ui-consistency-*.spec.ts',
  workers: 2,
  use: { ...base.use, viewport: { width: 1440, height: 900 } },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'webkit', use: { ...devices['Desktop Safari'] } }
  ]
});
