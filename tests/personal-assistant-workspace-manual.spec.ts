import { test, expect } from '@playwright/test';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

test('no model still permits explicit blank workspace review from the assistant menu', async ({
  page,
  request
}) => {
  test.skip(!process.env.ORI_DISCOVERY_DISCUSSION_SANDBOX, 'Owned offline discussion sandbox only');
  await installLocalCdn(page);
  const before = (await (await request.get('/api/workspaces')).json()).folders.length;
  expect(
    (await request.post('/api/settings/system-model', { data: { provider: '', model: '' } })).ok()
  ).toBe(true);
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/');
    await page.locator('#personalAssistantLauncher').click();
    const input = page.locator('#personalAssistantInput');
    await input.fill('Keep this OK Go question while I review the form.');
    const ask = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const refusal = await (await ask).json();
    expect(refusal.model_unavailable).toBe(true);
    expect(refusal.failure_reason).toBe('model_not_configured');
    await expect(input).toHaveValue('Keep this OK Go question while I review the form.');
    await expect(
      page
        .locator('#personalAssistantPanel')
        .getByText('Answered from your app data.', { exact: true })
    ).toHaveCount(0);
    // Use the always-available menu rather than the failure's extra action.
    await page.getByLabel('More assistant options', { exact: true }).click();
    const writes: string[] = [];
    page.on('request', req => {
      if (
        req.method() === 'POST' &&
        /\/api\/(?:home-assistant\/ask|workspaces(?:$|\/build-sessions))/.test(
          new URL(req.url()).pathname
        )
      )
        writes.push(req.url());
    });
    const manual = page.locator('#personalAssistantCreateWorkspace');
    const box = await manual.boundingBox();
    expect(box?.height).toBeGreaterThanOrEqual(44);
    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
    const violations = await page.evaluate(
      async () =>
        (
          await (window as any).axe.run(document.getElementById('personalAssistantPanel'), {
            runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
          })
        ).violations
    );
    expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
    await manual.focus();
    await page.keyboard.press('Enter');
    const modal = page.locator('#addFolderModal');
    await expect(modal).toBeVisible();
    await expect(page.locator('#workspaceBuildPane')).toBeHidden();
    await expect(page.locator('#folderDescriptionInput')).toHaveValue('');
    await modal.screenshot({
      path: join(process.env.ORI_ASSISTANT_EVIDENCE_DIR!, 'workspace-no-model-manual-review.png')
    });
    await modal.getByRole('button', { name: 'Close create workspace', exact: true }).click();
    await expect(modal).toBeHidden();
    await page.locator('#personalAssistantLauncher').click();
    await expect(input).toHaveValue('Keep this OK Go question while I review the form.');
    expect(writes).toEqual([]);
    expect((await (await request.get('/api/workspaces')).json()).folders.length).toBe(before);
  } finally {
    expect(
      (
        await request.post('/api/settings/system-model', {
          data: { provider: 'ollama', model: 'ori-workspace-fixture' }
        })
      ).ok()
    ).toBe(true);
  }
});
