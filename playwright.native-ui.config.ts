import { defineConfig } from '@playwright/test';
import base from './playwright.ui-consistency.config';

// Explicit opt-in: full Chromium with its own temporary, non-incognito profile
// is needed to operate Chrome's native Page zoom preference (not emulation).
export default defineConfig({
  ...base,
  testMatch: 'native-ui-consistency.spec.ts',
  timeout: 120_000,
  projects: [{ name: 'chromium-native' }]
});
