import { expect, test } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { installLocalCdn } from './helpers/offline-cdn';

// Creates disposable groups using the same reviewed plan as the group creator.
test.skip(!process.env.ORI_MAP_DECK_DEMO, 'requires an isolated demo server');
test('a nested group has its own map team; disclosure and chat are separate doors', async ({
  page
}) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
  const create = async (name: string, parent_id = '') => {
    const plan = await (
      await page.request.post('/api/workspaces/template-agent-plan', {
        data: { group_roster: true, group_name: name }
      })
    ).json();
    const res = await page.request.post('/api/workspaces', {
      data: {
        name,
        parent_id,
        kind: 'group',
        group_roster: true,
        create_template_agents: true,
        template_agent_review: {
          version: 1,
          plan_revision: plan.revision,
          expectations: plan.agents.map((a: { name: string; action: string }, index: number) => ({
            index,
            name: a.name,
            action: a.action
          }))
        }
      }
    });
    expect(res.ok(), await res.text()).toBe(true);
    return (await res.json()).folder;
  };
  const suffix = randomUUID().slice(0, 6);
  const home = await create('Studio ' + suffix);
  const room = await create('Edit room ' + suffix, home.id);
  const tab = page.locator('.map-team-tab');
  const deck = page.getByRole('region', { name: 'Map team' });
  for (const [ws, count] of [
    [room, 0],
    [home, 1]
  ] as const) {
    await page.goto(`/workspaces/${ws.folder_slug}?mode=map`);
    const close = page.locator('#personalAssistantClose');
    if (await close.isVisible()) await close.click();
    await expect(tab).toHaveText(`Roster ${count}`);
    await expect(page.locator('.ws-map-unit.is-commander')).toBeVisible();
    await tab.focus();
    await page.keyboard.press('Enter');
    await expect(deck).toBeVisible();
    if (count) await expect(deck).toContainText(`Edit room ${suffix} Manager`);
    else await expect(deck).toContainText('No other agents');
    await page.screenshot({
      path: `tasks/evidence-map-agent-deck/${count ? 'nested-members' : 'commander-only'}.png`
    });
    await page.keyboard.press('Escape');
    await expect(tab).toBeFocused();
    await expect(deck).toBeHidden();
  }
  const moved: string[] = [];
  page.on('request', r => {
    if (r.method() !== 'GET' && r.url().includes('/layout')) moved.push(r.method());
  });
  const camera = () => page.evaluate(() => (window as any).OriWorkspaceMap.getCamera());
  const before = await camera();
  await tab.click();
  await tab.click();
  expect(await camera()).toEqual(before);
  await tab.click();
  await deck.getByRole('button', { name: /Edit room/ }).click();
  await expect(deck).toBeHidden();
  await expect(page.locator(`[data-ws-id="${room.id}"][aria-pressed="true"]`)).toBeVisible();
  expect((await camera()).zoom).toBeGreaterThanOrEqual(0.65);
  expect(moved).toEqual([]);
  await expect(page.locator('.is-just-placed')).toHaveCount(0);
  await page.screenshot({ path: 'tasks/evidence-map-agent-deck/selected-place.png' });
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(tab).toBeHidden();
});
