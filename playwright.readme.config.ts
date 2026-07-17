import { defineConfig } from '@playwright/test';

const runDirectory = process.env.README_CAPTURE_RUN_DIR || `${process.env.TMPDIR || '/tmp'}/ori-readme-playwright`;

export default defineConfig({
  testDir: './tests',
  testMatch: 'readme-capture.spec.ts',
  fullyParallel: false,
  workers: 1,
  forbidOnly: true,
  retries: 0,
  timeout: 45_000,
  outputDir: process.env.README_CAPTURE_PLAYWRIGHT_OUTPUT || `${runDirectory}/playwright-output`,
  reporter: [['list'], ['json', { outputFile: `${runDirectory}/playwright-results.json` }]],
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL || 'http://127.0.0.1:8765',
    browserName: 'chromium',
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: 'dark',
    locale: 'en-US',
    timezoneId: 'UTC',
    reducedMotion: 'reduce',
    screenshot: 'off',
    video: 'off',
    trace: 'off',
  },
});
