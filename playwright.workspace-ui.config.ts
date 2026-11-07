import { defineConfig, devices } from '@playwright/test';
import base from './playwright.config';

// Feature-scoped acceptance. Do not multiply unrelated opt-in fixture suites.
export default defineConfig({
  ...base,
  testMatch: 'workspace-ui-simplification.spec.ts',
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'webkit', use: { ...devices['Desktop Safari'] } }
  ]
});
