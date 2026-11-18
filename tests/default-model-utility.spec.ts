import { test, expect, type APIRequestContext } from '@playwright/test';
import { assertSandbox, preparePage, guardReadTour, capture } from './helpers/ui-consistency';

test.skip(
  process.env.ORI_DEFAULT_MODEL_DEMO !== '1',
  'requires a separately owned disposable model-settings installation'
);
const restorations = new Map<string, (request: APIRequestContext) => Promise<void>>();
test.afterEach(async ({ request }, info) => {
  try {
    await restorations.get(info.testId)?.(request);
  } finally {
    restorations.delete(info.testId);
  }
});

test('Default model utility preserves long aliases, dirty drafts and genuine save/clear without hiring or schedules', async ({
  page,
  request
}, info) => {
  expect(info.config.workers).toBe(1);
  await assertSandbox(request);
  const relationship = async (client = request) =>
    (await (await client.get('/api/personal-assistant')).json()).personal_assistant;
  expect(
    (await relationship()).state,
    'do not bootstrap a scheduled HQ for this utility check'
  ).toBe('needs_hire');
  const original = await (await request.get('/api/settings/system-model')).json();
  const restore = {
    provider: original.provider || '',
    model: original.model || '',
    reasoning_effort: original.reasoning_effort || ''
  };
  restorations.set(info.testId, async client => {
    if (!page.isClosed()) await page.close();
    expect((await client.post('/api/settings/system-model', { data: restore })).ok()).toBeTruthy();
    expect(await (await client.get('/api/settings/system-model')).json()).toMatchObject({
      provider: restore.provider,
      model: restore.model
    });
    expect((await relationship(client)).state).toBe('needs_hire');
  });
  const longAlias = 'fictional/local-context/' + 'literal-model-alias-'.repeat(6);
  // This inspected handler only saves settings and aligns the built-in profile;
  // it does not request inference. Ollama is required by the existing contract.
  expect(
    (
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: longAlias }
      })
    ).ok()
  ).toBeTruthy();
  await preparePage(page, 'dark', 390);
  const unexpected = await guardReadTour(page);
  const allowed: unknown[] = [];
  let expectedPayload: object = {};
  await page.route('**/api/settings/system-model', async route => {
    if (route.request().method() !== 'POST') return route.fallback();
    try {
      expect(route.request().postDataJSON()).toEqual(expectedPayload);
    } catch (error) {
      await route.abort('blockedbyclient');
      throw error;
    }
    allowed.push(route.request().postDataJSON());
    return route.continue();
  });
  await page.goto('/settings#system-model');
  await expect(page.locator('#system-model h2')).toBeInViewport();
  const model = page.locator('#systemModelModel');
  await expect(model).toHaveValue(longAlias);
  page.once('dialog', dialog => dialog.dismiss());
  await page.locator('#clearSystemModelBtn').click();
  await expect(model).toHaveValue(longAlias);
  expect(allowed).toEqual([]);
  await page.locator('#systemModelProvider').selectOption('ollama');
  const listed = await model
    .locator('option')
    .evaluateAll(
      (options, alias) =>
        options
          .map(option => (option as HTMLOptionElement).value)
          .find(value => value && value !== alias),
      longAlias
    );
  expect(listed, 'needs one listed local model, but never invokes it').toBeTruthy();
  await model.selectOption(listed!);
  expect(
    (
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'external-tab-alias' }
      })
    ).ok()
  ).toBeTruthy();
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(page.locator('#systemModelStatusDetails')).toContainText('external-tab-alias');
  await expect(model).toHaveValue(listed!);
  await page.reload();
  await expect(model).toHaveValue('external-tab-alias');
  await page.locator('#systemModelProvider').selectOption('ollama');
  await model.selectOption(listed!);
  expectedPayload = { provider: 'ollama', model: listed, reasoning_effort: '' };
  await page.locator('#saveSystemModelBtn').click();
  await expect(page.locator('#systemModelStatusText')).toHaveText('Default model configured');
  expect(await (await request.get('/api/settings/system-model')).json()).toMatchObject({
    provider: 'ollama',
    model: listed,
    configured: true
  });
  await capture(
    page,
    info,
    'default-model-utility',
    {
      journey:
        'real settings writes and cancellation; local inventory reads only; no hire, HQ, schedules or inference'
    },
    { fullPage: false }
  );
  expectedPayload = { provider: '', model: '' };
  page.once('dialog', dialog => dialog.accept());
  await page.locator('#clearSystemModelBtn').click();
  await expect(page.locator('#systemModelStatusText')).toHaveText('Default model not configured');
  expect(await (await request.get('/api/settings/system-model')).json()).toMatchObject({
    provider: '',
    model: '',
    configured: false
  });
  expect(allowed).toEqual([
    { provider: 'ollama', model: listed, reasoning_effort: '' },
    { provider: '', model: '' }
  ]);
  expect(unexpected).toEqual([]);
  expect((await relationship()).state).toBe('needs_hire');
});
