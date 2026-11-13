import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';

// Separate fresh profile: standalone form deliberately does not authorize Notes.
test('standalone Personal Assistant build yields its modal and preserves omitted Notes', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } });
  const initial = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(initial.state).toBe('needs_hire');
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'alternate-hire',
      if_version: initial.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my week moving.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.ok()).toBeTruthy();
  await page.goto('/');
  await page.locator('[data-hq-site]').click();
  await page.locator('[data-hq-action="build"]').click();
  await expect(page.locator('#hqBuildModal')).toBeVisible();
  await expect(page.locator('#hqBuildSubmitBtn')).toHaveText('Build My HQ');
  const posted = page.waitForRequest(
    r => r.method() === 'POST' && new URL(r.url()).pathname === '/api/personal-assistant/hq'
  );
  await page.locator('#hqBuildSubmitBtn').click();
  expect((await posted).postDataJSON()).not.toHaveProperty('notes');
  await expect(page.locator('#hqBuildModal')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);
  await expect(page.locator('[data-creation-map-status]')).toContainText('Your HQ is ready');
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-2', { recursive: true });
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-2/alternate-ready.png',
    fullPage: true
  });
});
