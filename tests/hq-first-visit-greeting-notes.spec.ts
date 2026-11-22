import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// A fresh HQ without Notes: the invitation does not force a second greeting
// or claim the Notes question was seen. The drawer is still a normal surface.
test('one welcome outside the drawer; Notes keeps its own invitation', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  const post = async (path: string, data: unknown) => {
    const r = await request.post(path, { data });
    expect(r.ok(), path).toBeTruthy();
  };
  const get = async (path: string) => (await request.get(path)).json();
  await post('/api/onboarding/skip', {});
  await post('/api/settings/workspace-root', {
    workspace_root: (await get('/api/settings/workspace-root')).default_workspace_root
  });
  const pa = async () => (await get('/api/personal-assistant')).personal_assistant;
  const before = await pa();
  expect(before.state).toBe('needs_hire');
  await post('/api/personal-assistant/hire', {
    request_id: 'notes-hire',
    if_version: before.state_version,
    display_name: 'Atlas',
    mandate: 'Keep my week moving.',
    focus_areas: ['plan_my_day']
  });
  await post('/api/personal-assistant/hq', {
    request_id: 'notes-build',
    if_version: (await pa()).state_version,
    name: 'My HQ',
    timezone: 'UTC',
    notes: { include: false }
  });
  const hq = (await get('/api/personal-hq/status')).status;
  let prompted = 0;
  page.on('request', r => {
    if (r.url().endsWith('/second-brain/prompted') && r.method() === 'POST') prompted++;
  });
  await page.goto(`/workspaces/${encodeURIComponent(hq.workspace.folder_slug)}?mode=details`);
  const chooser = page.locator('[data-hq-firstvisit]');
  await expect(chooser).toBeFocused();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  expect(prompted).toBe(0);
  await chooser.getByRole('button', { name: 'Maybe later' }).click();
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantHQGreeting')).toBeHidden();
  await expect(chooser).toHaveCount(0);
  await expect(page.locator('#personalAssistantSecondBrain')).toBeVisible();
});
