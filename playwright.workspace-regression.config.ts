import { defineConfig } from '@playwright/test';
import base from './playwright.ui-consistency.config';

// Existing workspace contracts only; fixture-backed Notes has a separate setup.
export default defineConfig({
  ...base,
  testMatch: [
    'workspace-header-phone.spec.ts',
    'workspace-detail.a11y.spec.js',
    'workspace-ticket-management.spec.ts',
    'workspace-backlog.spec.ts'
  ]
});
