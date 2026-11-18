import { defineConfig, devices } from '@playwright/test';
import base from './playwright.config';

// Opt-in only: never multiply the default or unrelated provider-backed suites.
export default defineConfig({
  ...base,
  testMatch: 'ui-consistency-*.spec.ts',
  // Ordinary Playwright output cleanup must not erase README capture staging.
  outputDir: '.tmp/ui-consistency/playwright-output',
  // Utility journeys restore singleton Profile/Settings values in the owned
  // installation. Serialize engines as well as cases to avoid racing that state.
  workers: 1,
  use: { ...base.use, viewport: { width: 1440, height: 900 } },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'webkit', use: { ...devices['Desktop Safari'] } }
  ]
});
