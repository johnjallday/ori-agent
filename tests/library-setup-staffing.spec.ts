import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { musicHomeSnapshot as snapshot } from './helpers/music-home-placement';

// library-setup-staffing: confirmed library setup adds the Home's one shared
// assistant under the approval the owner already gave, and placement-only
// stays placement-only. Runs only in a fresh candidate sandbox whose Home was
// listed by the collection setup (which records that approval):
//   ./scripts/music-home-placement-demo.sh --case staffing --port P \
//     --reaper-source DIR --music-source DIR
// The sandbox model endpoint is a refused loopback: nothing here can run a
// model, and nothing here should try.

const enabled = process.env.LIBRARY_SETUP_STAFFING === '1';
const evidence = 'tasks/evidence-library-setup-staffing/group-2';

async function json(response: { json(): Promise<any> }) {
  return response.json();
}

async function homeOf(request: APIRequestContext) {
  const state = await snapshot(request);
  const home = state.workspaces.find((w: any) => w.kind === 'group');
  expect(home, 'the collection setup made one Home').toBeTruthy();
  return home;
}

async function libraryRows(request: APIRequestContext, homeID: string) {
  const page = await json(
    await request.get(`/api/workspaces/${homeID}/assistant-program/library/projects?page_size=50`)
  );
  return new Map<string, any>(page.rows.map((row: any) => [row.name, row]));
}

async function child(request: APIRequestContext, homeID: string, name: string) {
  const state = await snapshot(request);
  const found = state.workspaces.find((w: any) => w.parent_id === homeID && w.name === name);
  return found ? json(await request.get(`/api/workspaces/${found.id}`)) : null;
}

async function openSheet(page: Page, homeID: string) {
  await page.goto('/?view=map');
  const badge = page.locator(`[data-library-badge="${homeID}"]`).first();
  await expect(badge).toBeVisible({ timeout: 30_000 });
  await badge.click();
  const sheet = page.locator(`[data-library-sheet="${homeID}"]`);
  await expect(sheet).toBeVisible();
  await expect(sheet.locator('.ws-map-library-sheet-rows')).toBeVisible({ timeout: 30_000 });
  return sheet;
}

function reaperAgents(agents: any[]) {
  return agents.filter(a => /^REAPER Assistant/.test(a.name)).map(a => a.name);
}

test.describe('library setup staffing', () => {
  test.skip(!enabled, 'LIBRARY_SETUP_STAFFING=1 runs in the staffing candidate sandbox');
  test.describe.configure({ mode: 'serial' });

  test('map setup staffing: four Albums share one assistant created once', async ({
    page,
    request
  }) => {
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1440, height: 1000 });
    const home = await homeOf(request);
    const before = await snapshot(request);
    expect(reaperAgents(before.agents)).toEqual([]);
    const sharing = (
      await json(await request.get(`/api/workspaces/${home.id}/assistant-program/library/roots`))
    ).sharing;
    expect(sharing.state).toBe('on');
    expect(sharing.agent_name || '').toBe('');
    const rows = await libraryRows(request, home.id);
    const album5 = rows.get('Album-5');

    const sheet = await openSheet(page, home.id);
    // The plan is read, not acted on: opening the sheet adds nobody.
    expect(reaperAgents((await snapshot(request)).agents)).toEqual([]);
    await sheet.locator(`[data-sheet-row="${album5.id}"]`).uncheck();
    await sheet.getByRole('button', { name: 'Put 4 on the map' }).click();
    const staff = sheet.locator('[data-sheet-staffing-choice="staff"]');
    await expect(staff).toBeChecked();
    await expect(sheet.locator('[data-sheet-staffing="available"]')).toContainText(
      'Creates your Home\'s shared REAPER Assistant ("REAPER Assistant") once'
    );
    await expect(sheet).toContainText('no ticket runs and no model is called');
    for (const name of ['Album-1', 'Album-2', 'Album-3', 'Album-4'])
      await expect(sheet).toContainText(`${name} — `);
    await page.screenshot({ path: `${evidence}/01-confirm-with-assistant.png` });
    // Confirming is the only thing that does anything.
    expect((await snapshot(request)).workspaces).toHaveLength(before.workspaces.length);

    await sheet.getByRole('button', { name: 'Put on the map and add the assistant' }).click();
    await expect(sheet.locator('[role="status"]')).toContainText(
      '4 projects are on the map. 4 have your shared REAPER Assistant.',
      { timeout: 120_000 }
    );
    await page.screenshot({ path: `${evidence}/02-result-with-assistant.png` });

    const after = await snapshot(request);
    expect(reaperAgents(after.agents)).toEqual(['REAPER Assistant']);
    expect(after.workspaces.filter((w: any) => w.parent_id === home.id)).toHaveLength(4);
    for (const name of ['Album-1', 'Album-2', 'Album-3', 'Album-4']) {
      const ws = await child(request, home.id, name);
      expect(ws, name).toBeTruthy();
      expect(ws.agent_instances.map((a: any) => a.name)).toEqual(['REAPER Assistant']);
      // Staffing started nothing: every ticket is still waiting, none assigned by it.
      for (const ticket of ws.tasks || []) {
        expect(ticket.status, `${name}: ${ticket.description}`).toBe('pending');
      }
    }
    const recorded = (
      await json(await request.get(`/api/workspaces/${home.id}/assistant-program/library/roots`))
    ).sharing;
    expect(recorded.agent_name).toBe('REAPER Assistant');
    expect((await libraryRows(request, home.id)).get('Album-5').connection).toBe('catalog_only');
  });

  test('map setup staffing: placement only adds no assistant', async ({ page, request }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width: 1440, height: 1000 });
    const home = await homeOf(request);
    const before = await snapshot(request);
    const sheet = await openSheet(page, home.id);
    await sheet.getByRole('button', { name: 'Put 1 on the map' }).click();
    // The existing assistant would join: still a choice, and the person declines.
    await expect(sheet.locator('[data-sheet-staffing="available"]')).toContainText(
      'existing shared REAPER Assistant'
    );
    await sheet.locator('[data-sheet-staffing-choice="placement"]').check();
    await expect(sheet.getByRole('button', { name: 'Put on the map', exact: true })).toBeVisible();
    await page.screenshot({ path: `${evidence}/03-confirm-placement-only.png` });
    await sheet.getByRole('button', { name: 'Put on the map', exact: true }).click();
    await expect
      .poll(async () => (await libraryRows(request, home.id)).get('Album-5').connection, {
        timeout: 60_000
      })
      .toBe('connected');
    const ws = await child(request, home.id, 'Album-5');
    expect(ws.agent_instances || []).toEqual([]);
    const after = await snapshot(request);
    expect(reaperAgents(after.agents)).toEqual(reaperAgents(before.agents));
  });
});
