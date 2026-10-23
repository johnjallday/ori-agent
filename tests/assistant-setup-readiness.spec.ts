import { test, expect, type APIRequestContext } from '@playwright/test';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

// Real host, no model and no preinstalled plugins. No response fixtures in this
// phase: the registry may report availability or a precise unavailable reason.
const sandbox = process.env.ORI_READINESS_SANDBOX || '';
const evidence = process.env.ORI_READINESS_EVIDENCE || '';
test.skip(!sandbox, 'requires an explicitly isolated ORI_READINESS_SANDBOX');
test.describe.configure({ mode: 'serial' });
test.setTimeout(90_000);
const source = '<REAPER_PROJECT 0.1 "7.0"\n>\n';

async function json(request: APIRequestContext, path: string, data?: object) {
  const response = data ? await request.post(path, { data }) : await request.get(path);
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

test.beforeAll(async ({ request }) => {
  await json(request, '/api/onboarding/skip', {});
  expect((await json(request, '/api/plugins')).plugins).toEqual([]);
  expect((await json(request, '/api/settings/system-model')).configured).toBe(false);
  await json(request, '/api/settings/workspace-root', { workspace_root: '' });
  let assistant = (await json(request, '/api/personal-assistant')).personal_assistant;
  if (assistant.state === 'needs_hire') {
    await json(request, '/api/personal-assistant/hire', {
      request_id: 'readiness-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    });
    assistant = (await json(request, '/api/personal-assistant')).personal_assistant;
  }
  if (assistant.state === 'needs_hq')
    await json(request, '/api/personal-assistant/hq', {
      request_id: 'readiness-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  mkdirSync(join(sandbox, 'Desktop'), { recursive: true, mode: 0o750 });
  writeFileSync(join(sandbox, 'Desktop', 'Session.rpp'), source, { mode: 0o600 });
  if (evidence) mkdirSync(evidence, { recursive: true, mode: 0o750 });
});

for (const route of ['/?panel=today', '/settings']) {
  test(`real fresh attachment and review are reachable without a model on ${route}`, async ({
    page,
    request
  }) => {
    const forbidden: string[] = [];
    const before = await json(request, '/api/workspaces');
    page.on('request', request => {
      const path = new URL(request.url()).pathname;
      if (
        /\/home-assistant\/(ask|route)$|\/offers\/[^/]+\/(setup|decide)$|\/tasks.*execute/.test(
          path
        ) ||
        (request.method() !== 'GET' && /\/plugins\/.*(install|enable|update)/.test(path))
      )
        forbidden.push(`${request.method()} ${path}`);
    });
    await page.goto(route);
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.evaluate(() =>
      (window as any).PersonalAssistantPanel.open(
        document.getElementById('personalAssistantLauncher')
      )
    );
    const draft = '  Keep 🎼 this exact\ndraft  ';
    await page.locator('#personalAssistantInput').fill(draft);
    await page.locator('#personalAssistantFolderChip:enabled').click();
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Desktop', exact: true })
      .click();
    const back = page.locator('#personalAssistantExplorerBack');
    await expect(back).toBeVisible({ timeout: 60_000 });
    await back.click();
    const preview = page.locator('#personalAssistantFolderPreview');
    await expect(preview).toBeVisible();
    const review = page.locator('#personalAssistantReviewFolderSetup');
    await expect(preview.locator('#personalAssistantReviewFolderSetup')).toBeVisible();
    await expect(preview.locator('figure')).toHaveCount(0);
    await expect(preview).toContainText('No model is needed to review');
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    if (evidence)
      await page.screenshot({
        path: join(
          evidence,
          `${route.startsWith('/settings') ? 'settings' : 'home'}-fresh-attached.png`
        )
      });
    await review.click();
    // The missing Home can be explained by contextual placement or a saved
    // canonical card; both must expose the host's existing guided continuation.
    await expect(
      page
        .getByRole('link', { name: /Guided Setup/ })
        .or(page.getByRole('button', { name: 'Open Guided Setup', exact: true }))
    ).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    if (evidence)
      await page.screenshot({
        path: join(
          evidence,
          `${route.startsWith('/settings') ? 'settings' : 'home'}-fresh-review.png`
        )
      });
    expect(forbidden).toEqual([]);
    expect((await json(request, '/api/plugins')).plugins).toEqual([]);
    expect(await json(request, '/api/workspaces')).toEqual(before);
    expect(readFileSync(join(sandbox, 'Desktop', 'Session.rpp'), 'utf8')).toBe(source);
  });
}
