import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

test.skip(process.env.ORI_DEFAULT_MODEL_DEMO !== '1', 'requires an isolated wt demo server');
const evidence = process.env.ORI_DEFAULT_MODEL_EVIDENCE || '';

test('long model remains readable and keyboard-accessible across drawer layouts', async ({
  page,
  request
}) => {
  test.setTimeout(90_000);
  await installLocalCdn(page);
  await page.addInitScript(() => localStorage.setItem('ori-theme', 'light'));
  if (evidence) mkdirSync(evidence, { recursive: true });
  const post = async (path: string, data: object) => {
    const response = await request.post(path, { data });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  await post('/api/onboarding/skip', {});
  await post('/api/settings/workspace-root', { workspace_root: '' });
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (assistant.state === 'needs_hire')
    assistant = (
      await post('/api/personal-assistant/hire', {
        request_id: 'model-layout-hire',
        if_version: assistant.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help me plan.',
        focus_areas: ['plan_my_day']
      })
    ).personal_assistant;
  if (assistant.state === 'needs_hq')
    await post('/api/personal-assistant/hq', {
      request_id: 'model-layout-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  const model = 'local/long-context-instruct-organization/' + 'literal-model-alias-'.repeat(5);
  await post('/api/settings/system-model', { provider: 'ollama', model });
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  const line = page.locator('#personalAssistantModel');
  const text = page.locator('#personalAssistantModelText');
  const link = page.getByRole('link', { name: 'Edit Default model', exact: true });
  await expect(text).toHaveText(`Model: ollama / ${model}`);
  await expect(text).toHaveAttribute('title', `Model: ollama / ${model}`);
  await expect(text.locator('*')).toHaveCount(0);
  const checkBounds = async () => {
    await expect(link).toBeInViewport();
    const bounds = await line.boundingBox();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
    const anchor = await link.boundingBox();
    const menu = await page.locator('#personalAssistantMore').boundingBox();
    expect(anchor!.x + anchor!.width).toBeLessThanOrEqual(menu!.x);
    await link.focus();
    await expect(link).toBeFocused();
  };
  let index = 13;
  for (const theme of ['light', 'dark']) {
    if (theme === 'dark') await page.locator('#darkModeToggle').click();
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
    for (const expanded of [false, true]) {
      if (expanded) await page.getByRole('button', { name: 'Expand', exact: true }).click();
      await expect(page.locator('#personalAssistantExpand')).toHaveAttribute(
        'aria-pressed',
        String(expanded)
      );
      await checkBounds();
      if (evidence)
        await page.screenshot({
          path: join(
            evidence,
            `${index++}-${theme}-${expanded ? 'expanded' : 'normal'}-long-model.png`
          ),
          animations: 'disabled'
        });
    }
    await page.locator('#personalAssistantExpand').click();
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await checkBounds();
  if (evidence)
    await page.screenshot({
      path: join(evidence, '17-dark-mobile-long-model.png'),
      animations: 'disabled'
    });
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/settings#system-model$/);
  await expect(page.locator('#system-model h2')).toBeInViewport();
  await expect(page.locator('#systemModelModel')).toHaveValue(model);

  // Real cross-tab readback preserves a dirty Settings draft, then reload
  // shows the persisted value rather than treating the old draft as saved.
  await page.locator('#systemModelProvider').selectOption('ollama');
  const draft = await page
    .locator('#systemModelModel option')
    .evaluateAll(options =>
      options.map(option => (option as HTMLOptionElement).value).find(Boolean)
    );
  await page.locator('#systemModelModel').selectOption(draft!);
  await post('/api/settings/system-model', { provider: 'ollama', model: 'external-tab-alias' });
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(page.locator('#systemModelStatusDetails')).toContainText('external-tab-alias');
  await expect(page.locator('#systemModelModel')).toHaveValue(draft!);
  await page.reload();
  await expect(page.locator('#systemModelModel')).toHaveValue('external-tab-alias');

  // Controlled metadata transport/state fixtures, not provider executions.
  await page.route('**/api/home-assistant/model*', route =>
    route.fulfill({
      json: {
        state: 'unavailable',
        source: 'default',
        recipient: 'personal',
        settings_href: '/settings#system-model'
      }
    })
  );
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(text).toHaveText('Model: unavailable');
  await expect(link).toHaveAttribute('href', '/settings#system-model');
  if (evidence)
    await page.screenshot({
      path: join(evidence, '18-unavailable-metadata-fixture.png'),
      animations: 'disabled'
    });
  await page.unroute('**/api/home-assistant/model*');
  await page.route('**/api/home-assistant/model*', route => route.abort());
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(text).toHaveText('Model: unknown');
  await expect(page.locator('#personalAssistantModelSettings')).toBeHidden();
  await page.unroute('**/api/home-assistant/model*');
});
