import { defineConfig } from '@playwright/test';
import base from './playwright.ui-consistency.config';

// Use a separate owned, model-free, explicitly unscheduled HQ/vault per engine.
export default defineConfig({ ...base, testMatch: 'notes-station-panel.spec.ts' });
