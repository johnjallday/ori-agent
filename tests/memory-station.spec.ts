import { test, expect, type Page } from '@playwright/test';
import { clearBlockingModals, skipOnboarding } from './helpers/onboarding';

// The Memory station: every workspace's MEMORY.md as a building on the Map,
// whose badge reads the memory's state and whose panel is the memory manager
// (tasks/prd-memory-station.md §8).
//
// Runs against an isolated real server (`./scripts/e2e-fresh.sh`, `wt demo`).
// No model is used; entries are written through the memory API. The only mock
// keeps first-run onboarding out of the way.
test.describe.serial('Memory station', () => {
  const workspaceName = 'Memory Station Album';
  const agentName = 'Scout';
  const building = '.ws-cmd-map-hq-station[data-cmd-hq-station="memory"]';
  const panel = '.ws-cmd-modal-panel.is-memory';

  async function albumWorkspace(page: Page) {
    const listed = (await (await page.request.get('/api/workspaces')).json()).workspaces as Array<{
      id: string;
      name: string;
      folder_slug: string;
    }>;
    const existing = listed.find(ws => ws.name === workspaceName);
    if (existing) return existing;
    const agent = await page.request.post('/api/agents', {
      data: { name: agentName, model: 'gpt-4o-mini' }
    });
    expect(agent.ok(), await agent.text()).toBeTruthy();
    const created = await page.request.post('/api/workspaces', {
      data: { name: workspaceName, entry_agent_name: agentName }
    });
    expect(created.status(), await created.text()).toBeLessThan(300);
    return (await created.json()).folder as { id: string; name: string; folder_slug: string };
  }

  async function addEntry(page: Page, workspaceID: string, text: string, type = 'fact') {
    const added = await page.request.post(`/api/workspaces/${workspaceID}/memory/entries`, {
      data: { text, type }
    });
    expect(added.status(), await added.text()).toBe(200);
  }

  async function openMap(page: Page, slug: string) {
    await page.goto(`/workspaces/${slug}?mode=map`);
    await clearBlockingModals(page);
    await expect(page.locator(building)).toBeVisible();
  }

  const badge = (page: Page) => page.locator(`${building} .ws-cmd-map-hq-station-state`);

  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('a new workspace has a Memory building that counts its entries', async ({ page }) => {
    const album = await albumWorkspace(page);
    await openMap(page, album.folder_slug);
    await expect(page.locator(building)).toHaveAttribute('data-station-visual', 'archive');
    await expect(badge(page)).toHaveText('Empty');
    await expect(page.locator(building)).toHaveAttribute(
      'aria-label',
      'Memory station, Empty, nothing recorded yet'
    );

    await addEntry(page, album.id, 'Build baseline is about 7 minutes.', 'watch');
    await addEntry(page, album.id, 'Masters at -14 LUFS.');
    await page.reload();
    await expect(badge(page)).toHaveText('2 entries');
    await expect(page.locator(building)).toHaveAttribute(
      'aria-label',
      'Memory station, 2 entries, 1 watching'
    );
  });

  test('entries recorded after the panel was last opened read as new', async ({ page }) => {
    const album = await albumWorkspace(page);
    await openMap(page, album.folder_slug);
    await page.locator(building).click();
    await expect(page.locator(`${panel} #workspace-detail-config-memory-pane`)).toBeVisible();
    await expect(page.locator(`${panel} .ws-cmd-modal-title`)).toHaveText('Memory');
    await page.keyboard.press('Escape');
    await expect(page.locator(panel)).toBeHidden();
    await expect(page.locator(building)).toBeFocused();

    await addEntry(page, album.id, 'Mix bus uses a gentle glue compressor.');
    await page.reload();
    await expect(badge(page)).toHaveText('+1 new');

    // Opening the panel is looking: the count is plain again afterwards.
    await page.locator(building).click();
    await page.keyboard.press('Escape');
    await expect(badge(page)).toHaveText('3 entries');
  });

  test('a dragged Memory building keeps its place after a reload', async ({ page }) => {
    const album = await albumWorkspace(page);
    await openMap(page, album.folder_slug);
    const station = page.locator(building);
    const before = await station.evaluate(el =>
      (el as HTMLElement).style.getPropertyValue('--station-x')
    );
    const box = await station.boundingBox();
    expect(box).not.toBeNull();
    const x = box!.x + box!.width / 2;
    const y = box!.y + box!.height / 2;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x - 120, y + 10, { steps: 8 });
    await page.mouse.move(x - 240, y + 20, { steps: 8 });
    await page.mouse.up();
    await expect
      .poll(async () => {
        const workspace = await (
          await page.request.get(`/api/orchestration/workspace?id=${album.id}`)
        ).json();
        return workspace?.layout?.station_positions?.memory?.x ?? null;
      })
      .not.toBeNull();

    await page.reload();
    const after = await page
      .locator(building)
      .evaluate(el => (el as HTMLElement).style.getPropertyValue('--station-x'));
    expect(after).not.toBe(before);
  });

  test('past the budget the building says so and the panel marks what agents are not shown', async ({
    page
  }) => {
    const album = await albumWorkspace(page);
    const filler =
      ' Lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua ut enim ad minim veniam quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.';
    for (let i = 1; i <= 25; i++) {
      await addEntry(
        page,
        album.id,
        `Seed fact ${String(i).padStart(2, '0')}:${filler}`.slice(0, 400)
      );
    }
    const memory = await (await page.request.get(`/api/workspaces/${album.id}/memory`)).json();
    expect(memory.over_budget).toBe(true);
    const dropped = memory.entries.filter((entry: { in_prompt: boolean }) => !entry.in_prompt);
    expect(dropped.length).toBeGreaterThan(0);

    await openMap(page, album.folder_slug);
    await expect(badge(page)).toHaveText('Over budget');
    await expect(page.locator(building)).toHaveAttribute(
      'aria-label',
      `Memory station, Over budget, ${dropped.length} ${dropped.length === 1 ? 'entry is' : 'entries are'} not shown to agents`
    );

    await page.locator(building).click();
    const meter = page.locator(`${panel} [role="meter"]`);
    await expect(meter).toHaveAttribute('aria-valuenow', '100');
    await expect(page.locator(`${panel} .workspace-detail-memory-meter-text`)).toHaveText(
      '100% of what agents are shown'
    );
    await expect(
      page.locator(`${panel} .workspace-detail-memory-entry.is-not-in-prompt`)
    ).toHaveCount(dropped.length);
    await expect(
      page.locator(`${panel} .workspace-detail-memory-entry.is-not-in-prompt`).first()
    ).toContainText('not shown to agents');
  });

  // The run record's "Open workspace memory" link and the Profile's copy
  // confirmation both point here (stationLink); a run that writes memory needs
  // a model, so this follows the address they build.
  test('the station address opens the Memory panel and leaves the address bar clean', async ({
    page
  }) => {
    const album = await albumWorkspace(page);
    await page.goto(`/workspaces/${album.folder_slug}?station=memory`);
    await clearBlockingModals(page);
    await expect(page.locator(`${panel} #workspace-detail-config-memory-pane`)).toBeVisible();
    await expect(page).not.toHaveURL(/station=/);
    await page.keyboard.press('Escape');
    await expect(page.locator(panel)).toBeHidden();
  });
});
