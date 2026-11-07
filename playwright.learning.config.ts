import { defineConfig } from '@playwright/test';
import base from './playwright.config';

// Opt-in only: normal repository runs retain their existing Chromium project.
// Each engine gets a fresh owned installation through the learning demo helper.
export default defineConfig({
  ...base,
  testMatch: 'adaptive-learning-layout.spec.ts',
  projects: ['chromium', 'firefox', 'webkit'].map(name => ({
    name,
    use: { browserName: name as 'chromium' | 'firefox' | 'webkit' }
  }))
});
