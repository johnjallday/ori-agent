import { defineConfig } from '@playwright/test';
import base from './playwright.ui-consistency.config';

// Real installed repository-plugin fixtures require their own retained,
// ownership-verified sandbox. Never feed them the normal workspace fixtures.
export default defineConfig({
  ...base,
  testMatch: 'resource-*-ui-consistency.spec.ts'
});
