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
    // Pin font positioning and repaint whole tiles. Linux's incremental raster
    // can retain a differently rounded button edge after the Map's Summary
    // modal resizes it. Neither flag changes the UI or comparison tolerance.
    launchOptions: {
      args: ['--disable-font-subpixel-positioning', '--disable-partial-raster'],
    },
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
