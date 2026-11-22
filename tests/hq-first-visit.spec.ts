import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

let base: string;
let other: string;
test.describe.configure({ mode: 'serial' });
test.beforeAll(async ({ request }) => {
  async function post(url: string, data: unknown) {
    expect((await request.post(url, { data })).ok(), url).toBeTruthy();
  }
  await post('/api/onboarding/skip', {});
  const { default_workspace_root } = await (
    await request.get('/api/settings/workspace-root')
  ).json();
  await post('/api/settings/workspace-root', { workspace_root: default_workspace_root });
  const pa = async () =>
    (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const before = await pa();
  expect(before.state, 'requires a fresh isolated server').toBe('needs_hire');
  await post('/api/personal-assistant/hire', {
    request_id: 'guided-hire',
    if_version: before.state_version,
    display_name: 'Atlas',
    mandate: 'Keep my week moving.',
    focus_areas: ['plan_my_day']
  });
  await post('/api/personal-assistant/hq', {
    request_id: 'guided-build',
    if_version: (await pa()).state_version,
    name: 'My HQ',
    timezone: 'UTC',
    scope: 'all',
    include_future_workspaces: true,
    notes: { include: false }
  });
  const { status } = await (await request.get('/api/personal-hq/status')).json();
  expect(status.first_visit_state).toBe('pending');
  base = '/workspaces/' + encodeURIComponent(status.workspace.folder_slug);
  const created = await request.post('/api/workspaces', { data: { name: 'Another workspace' } });
  expect(created.ok()).toBeTruthy();
  const body = await created.json();
  other = '/workspaces/' + encodeURIComponent((body.folder || body.workspace).folder_slug);
});

for (const mode of ['details', 'map'])
  test(`one invitation on ${mode}; voluntary reopen after leaving`, async ({ page }, info) => {
    await installLocalCdn(page);
    if (mode === 'map') await page.setViewportSize({ width: 390, height: 844 });
    const access: string[] = [];
    page.on('request', r => {
      if (
        r.method() === 'POST' &&
        /folder-agent\/(choose|start)|email-setup\/connect|onboarding\/(detect|personalize)/.test(
          r.url()
        )
      )
        access.push(r.url());
    });
    await page.goto(`${base}?mode=${mode}`);
    const card = page.locator('[data-hq-firstvisit]');
    await expect(card).toHaveAccessibleName('What would you like to do next?');
    await expect(card).toBeFocused();
    await page.screenshot({ path: info.outputPath('offer.png') });
    await card.getByRole('button', { name: 'Maybe later' }).click();
    const door = page.getByRole('button', { name: 'Things to try' });
    await expect(door).toBeFocused();
    await page.goto(other + '?mode=details');
    await expect(card).toHaveCount(0);
    await expect(door).toHaveCount(0);
    await page.goto(`${base}?mode=${mode}`);
    await expect(door).toBeVisible();
    await expect(card).toHaveCount(0);
    await door.click();
    await card.getByRole('button', { name: /Explore Documents/ }).click();
    await expect(card.getByRole('link', { name: 'Review Documents' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(door).toBeFocused();
    expect(access).toEqual([]);
  });
