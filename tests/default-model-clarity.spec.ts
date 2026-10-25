import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

// Mutates settings only in an explicitly opted-in disposable wt demo server.
// No provider inference is requested by this suite.
test.skip(process.env.ORI_DEFAULT_MODEL_DEMO !== '1', 'requires an isolated wt demo server');
test.describe.configure({ mode: 'serial' });
const evidence = process.env.ORI_DEFAULT_MODEL_EVIDENCE || '';

test('Default model recovery keeps the old anchor and save/clear contract', async ({ page, request }) => {
  expect((await request.post('/api/onboarding/skip', { data: {} })).ok()).toBeTruthy();
  expect((await request.post('/api/settings/system-model', {
    data: { provider: '', model: '' }
  })).ok()).toBeTruthy();
  if (evidence) mkdirSync(evidence, { recursive: true });

  await page.goto('/agents/create');
  await page.locator('label[for="createConfigModeAuto"]').click();
  await expect(page.locator('#createLlmWarningMessage')).toContainText('Default model');
  if (evidence) await page.screenshot({ path: join(evidence, '01-setup-recovery.png') });
  await page.locator('#createLlmWarningSettingsLink').click();
  await expect(page).toHaveURL(/\/settings#system-model$/);
  await expect(page.locator('#system-model h2')).toBeInViewport();
  await expect(page.locator('#systemModelStatusText')).toHaveText('Default model not configured');
  await expect(page.locator('#system-model')).toContainText(
    "Used by your Assistant and Ori's AI features. Individual agents can use their own models."
  );
  await page.locator('#system-model h2').scrollIntoViewIfNeeded();
  if (evidence) await page.screenshot({ path: join(evidence, '02-settings-unconfigured.png') });

  await page.locator('#systemModelProvider').selectOption('ollama');
  await expect(page.locator('#systemModelModel')).toBeEnabled();
  const model = await page.locator('#systemModelModel option').evaluateAll(options =>
    options.map(option => (option as HTMLOptionElement).value).find(Boolean)
  );
  expect(model, 'demo needs one locally listed Ollama model; no inference is run').toBeTruthy();
  await page.locator('#systemModelModel').selectOption(model!);
  await page.locator('#saveSystemModelBtn').click();
  await expect(page.locator('#systemModelStatusText')).toHaveText('Default model configured');
  const saved = await (await request.get('/api/settings/system-model')).json();
  expect(saved).toMatchObject({ provider: 'ollama', model, configured: true });
  if (evidence) await page.screenshot({ path: join(evidence, '03-settings-configured.png') });
  await page.reload();
  await expect(page.locator('#systemModelModel')).toHaveValue(model!);

  page.once('dialog', dialog => dialog.accept());
  await page.locator('#clearSystemModelBtn').click();
  await expect(page.locator('#systemModelStatusText')).toHaveText('Default model not configured');
  expect(await (await request.get('/api/settings/system-model')).json()).toMatchObject({
    provider: '', model: '', configured: false
  });
});
