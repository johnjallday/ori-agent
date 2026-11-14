import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Own fresh server; the compatibility form is explicitly opened without a PAF
// hire. It must keep the legacy request/reload path, not acquire a celebration.
test('legacy HQ creation and returning visits never start the PAF reveal', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  const before = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(before.state).toBe('needs_hire');
  const mutations: string[] = [];
  page.on('request', r => {
    if (
      r.method() === 'POST' &&
      /personal-assistant\/(hq|hire)|personal-hq\/setup|second-brain/.test(r.url())
    )
      mutations.push(new URL(r.url()).pathname);
  });
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalHQOnboarding?.openBuildModal));
  await page.evaluate(() => (window as any).PersonalHQOnboarding.openBuildModal());
  await page.locator('#hqBuildName').fill('Legacy HQ');
  await page.locator('#hqBuildSubmitBtn').click();
  await expect
    .poll(async () => (await (await request.get('/api/personal-hq/status')).json()).status.valid)
    .toBe(true);
  await expect(page.locator('#hqBuildModal')).toBeHidden();
  await page.waitForTimeout(1200); // Existing legacy toast/reload lifecycle, not reveal timing.
  await expect(page.locator('.is-assembling')).toHaveCount(0);
  await expect(page.locator('#personalHQResultHost')).toBeHidden();
  await expect(page.locator('[data-creation-map-status]')).toHaveCount(0);
  expect(mutations).toEqual(['/api/personal-hq/setup']);
  const hq = (await (await request.get('/api/personal-hq/status')).json()).status;
  await page.goto('/');
  await expect(page.locator('.ws-map-tile.is-hq')).toBeVisible();
  await expect(page.locator('.is-assembling')).toHaveCount(0);
  await expect(page.locator('#personalHQResultHost')).toBeHidden();
  expect((await (await request.get('/api/personal-hq/status')).json()).status.workspace.id).toBe(
    hq.workspace.id
  );
  expect(mutations).toEqual(['/api/personal-hq/setup']);
});
