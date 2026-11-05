import { test, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';
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

// Connects one catalog row the way bulk setup did before this fix: activation
// review and commit only, no assistant.
async function connectOnly(request: APIRequestContext, homeID: string, entryID: string) {
  const base = `/api/workspaces/${homeID}/assistant-program/library/projects/${entryID}`;
  const detail = await json(await request.get(base));
  const eligibility = await json(await request.get(`${base}/activation`));
  const review = await json(
    await request.post(`${base}/activation/review`, {
      data: {
        workspace_name: detail.row.name,
        project_file: eligibility.project_files[0],
        if_revision: detail.revision
      }
    })
  );
  const committed = await request.post(`${base}/activation/commit`, {
    data: { review_token: review.token, idempotency_key: `pre-fix-${entryID}`, confirm: true }
  });
  expect(committed.ok()).toBe(true);
  return (await json(committed)).workspace_id as string;
}

test.describe('library setup staffing repair', () => {
  test.skip(
    process.env.LIBRARY_SETUP_STAFFING !== 'repair',
    'LIBRARY_SETUP_STAFFING=repair runs in the staffing-repair candidate sandbox'
  );
  const repairEvidence = 'tasks/evidence-library-setup-staffing/group-3';

  test('repair existing projects from the Home and from a project page', async ({
    page,
    request
  }) => {
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1440, height: 1000 });
    const home = await homeOf(request);
    const rows = await libraryRows(request, home.id);
    const ids: Record<string, string> = {};
    for (const name of ['Album-1', 'Album-2', 'Album-3', 'Album-4'])
      ids[name] = await connectOnly(request, home.id, rows.get(name).id);
    // The reported shape: approval on, four songs on the map, nobody hired.
    expect(reaperAgents((await snapshot(request)).agents)).toEqual([]);
    const tickets = async (id: string) =>
      ((await json(await request.get(`/api/workspaces/${id}`))).tasks || []).map((t: any) => [
        t.id,
        t.status,
        t.to || ''
      ]);
    const before: Record<string, any> = {};
    for (const [name, id] of Object.entries(ids)) before[name] = await tickets(id);

    // From the Home: its Details page opens the setup sheet even though
    // nothing is left to place; two of the four are chosen.
    await page.goto(`/workspaces/${home.folder_slug}`);
    const setup = page.locator('#workspace-detail-setup-projects-btn');
    await expect(setup).toBeVisible({ timeout: 30_000 });
    await setup.click();
    const sheet = page.locator(`[data-library-sheet="${home.id}"]`);
    await expect(sheet.locator('[data-sheet-repair]')).toHaveCount(4, { timeout: 30_000 });
    for (const box of await sheet.locator('[data-sheet-repair]').all())
      await expect(box).not.toBeChecked();
    for (const name of ['Album-1', 'Album-2'])
      await sheet.getByRole('checkbox', { name: `Add the shared assistant to ${name}` }).check();
    await page.screenshot({ path: `${repairEvidence}/01-sheet-repair-selection.png` });
    await sheet.getByRole('button', { name: 'Add the assistant to 2' }).click();
    await expect(sheet).toContainText('Add the shared assistant to 2 projects already on the map');
    await expect(sheet).toContainText('Nothing is connected again and no other project changes.');
    await page.screenshot({ path: `${repairEvidence}/02-sheet-repair-confirm.png` });
    await sheet.getByRole('button', { name: 'Add the assistant', exact: true }).click();
    await expect(sheet.locator('[role="status"]')).toContainText(
      '2 projects now have your shared REAPER Assistant.',
      { timeout: 60_000 }
    );
    // The results stay: each repaired project, already on the map, with its
    // assistant; nothing is closed or navigated for the person.
    const results = sheet.locator('[data-sheet-results]');
    await expect(results.locator('[data-sheet-result]')).toHaveCount(2);
    await expect(results).toContainText('Already on the map');
    await expect(results).toContainText('assistant joined');
    await page.screenshot({ path: `${repairEvidence}/03-sheet-repair-result.png` });
    await sheet.getByRole('button', { name: 'Back to the list' }).click();
    await expect(sheet).toContainText('on the map · has REAPER Assistant');

    // From the project itself: Album-3's empty roster offers the shared one
    // beside the ordinary Hire.
    const album3 = (await snapshot(request)).workspaces.find((w: any) => w.id === ids['Album-3']);
    await page.goto(`/workspaces/${album3.folder_slug}`);
    // A song connected without a mode opens its own Setup Wizard; the person
    // closes it (the repair neither needs nor answers it).
    const wizard = page.locator('#setupWizardDialog[open]');
    const opened = await wizard
      .waitFor({ state: 'visible', timeout: 10_000 })
      .then(() => true)
      .catch(() => false);
    if (opened) {
      await page.locator('#setupWizardClose').click();
      await expect(wizard).toHaveCount(0);
    }
    const add = page.locator('[data-shared-assistant-add]:visible').first();
    await expect(add).toBeVisible({ timeout: 30_000 });
    await expect(add).toHaveText('Add the shared REAPER Assistant');
    await expect(
      page.getByRole('button', { name: /Hire for REAPER Assistant/ }).first()
    ).toBeVisible();
    await expect(page.locator('[data-shared-assistant-offer]').first()).toContainText(
      'No new agent is made, no ticket runs and no model is called.'
    );
    await page.screenshot({ path: `${repairEvidence}/04-project-offer.png` });
    await add.click();
    await expect
      .poll(
        async () =>
          (await json(await request.get(`/api/workspaces/${ids['Album-3']}`))).agent_instances?.map(
            (a: any) => a.name
          ),
        { timeout: 60_000 }
      )
      .toEqual(['REAPER Assistant']);
    await expect(page.locator('[data-shared-assistant-add]')).toHaveCount(0, { timeout: 30_000 });
    await page.screenshot({ path: `${repairEvidence}/05-project-repaired.png` });

    // One shared definition, only the chosen projects changed, and nothing
    // was connected again or started.
    expect(reaperAgents((await snapshot(request)).agents)).toEqual(['REAPER Assistant']);
    for (const name of ['Album-1', 'Album-2', 'Album-3']) {
      const ws = await json(await request.get(`/api/workspaces/${ids[name]}`));
      expect(
        ws.agent_instances.map((a: any) => a.name),
        name
      ).toEqual(['REAPER Assistant']);
    }
    const untouched = await json(await request.get(`/api/workspaces/${ids['Album-4']}`));
    expect(untouched.agent_instances || []).toEqual([]);
    for (const [name, id] of Object.entries(ids))
      expect(await tickets(id), `${name} tickets`).toEqual(before[name]);
    const children = (await snapshot(request)).workspaces.filter(
      (w: any) => w.parent_id === home.id
    );
    expect(children.map((w: any) => w.id).sort()).toEqual(Object.values(ids).sort());
    expect((await libraryRows(request, home.id)).get('Album-5').connection).toBe('catalog_only');
  });
});

// The setup rows in the order the sheet lists (and so runs) them: by last save,
// not by name.
async function setupOrder(sheet: Locator, rows: Map<string, any>) {
  const ids: string[] = await sheet
    .locator('[data-sheet-row]')
    .evaluateAll(els => els.map(el => el.getAttribute('data-sheet-row') || ''));
  const names = new Map([...rows.values()].map((row: any) => [row.id, row.name]));
  return ids.map(id => ({ id, name: names.get(id) as string }));
}

// Faults are injected at the network edge only: the real server does the work,
// the browser is made to lose an answer or the owner changes the Home mid-run.
test.describe('library setup staffing recovery', () => {
  test.skip(
    process.env.LIBRARY_SETUP_STAFFING !== 'recovery',
    'LIBRARY_SETUP_STAFFING=recovery runs in the staffing-recovery candidate sandbox'
  );
  test.describe.configure({ mode: 'serial' });
  const recoveryEvidence = 'tasks/evidence-library-setup-staffing/group-4';

  test('recovery: a lost answer is retried without a second assistant', async ({
    page,
    request
  }) => {
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1440, height: 1000 });
    const home = await homeOf(request);
    const rows = await libraryRows(request, home.id);
    const sheet = await openSheet(page, home.id);
    const order = await setupOrder(sheet, rows);
    expect(order.length).toBe(5);
    const [first, second, third, fourth, fifth] = order;
    for (const row of [fourth, fifth])
      await sheet.locator(`[data-sheet-row="${row.id}"]`).uncheck();
    let lost = 0;
    // The second project's assistant is really added, but the answer never arrives.
    await page.route(`**/assistant-program/library/projects/${second.id}/staffing`, async route => {
      if (route.request().method() !== 'POST' || lost > 0) return route.continue();
      lost++;
      await route.fetch();
      await route.fulfill({ status: 502, body: '{}', contentType: 'application/json' });
    });
    await sheet.getByRole('button', { name: 'Put 3 on the map' }).click();
    await sheet.getByRole('button', { name: 'Put on the map and add the assistant' }).click();
    const results = sheet.locator('[data-sheet-results]');
    await expect(results).toBeVisible({ timeout: 120_000 });
    // Honest partial results: the first is done, the second is on the map
    // without a confirmed assistant, the third was never reached.
    await expect(results.locator(`[data-sheet-result="${first.id}"]`)).toContainText(
      'assistant added'
    );
    const secondRow = results.locator(`[data-sheet-result="${second.id}"]`);
    await expect(secondRow).toContainText('Put on the map');
    await expect(secondRow).toContainText('assistant not added');
    await expect(results.locator(`[data-sheet-result="${third.id}"]`)).toContainText('Not reached');
    await page.screenshot({ path: `${recoveryEvidence}/01-lost-answer-partial.png` });
    expect(reaperAgents((await snapshot(request)).agents)).toEqual(['REAPER Assistant']);
    // The explicit retry observes what happened and finishes the rest.
    await sheet.getByRole('button', { name: `Try ${second.name} again` }).click();
    await expect(sheet.locator('[role="status"]')).toContainText(
      '3 projects are on the map. 3 have your shared REAPER Assistant.',
      { timeout: 120_000 }
    );
    await expect(results.locator('.is-complete')).toHaveCount(3);
    await page.screenshot({ path: `${recoveryEvidence}/02-retried-complete.png` });
    expect(lost).toBe(1);
    const after = await snapshot(request);
    expect(reaperAgents(after.agents)).toEqual(['REAPER Assistant']);
    expect(after.workspaces.filter((w: any) => w.parent_id === home.id)).toHaveLength(3);
    await page.unroute(`**/assistant-program/library/projects/${second.id}/staffing`);
  });

  test('recovery: sharing switched off mid-batch stops with its review', async ({
    page,
    request
  }) => {
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1440, height: 1000 });
    const home = await homeOf(request);
    const rows = await libraryRows(request, home.id);
    const roots = await json(
      await request.get(`/api/workspaces/${home.id}/assistant-program/library/roots`)
    );
    const digest = roots.sharing.team_digest;
    const sharing = (enabled: boolean, id: string) =>
      request.post(`/api/workspaces/${home.id}/assistant-program/library/sharing`, {
        data: { request_id: id, enabled, team_digest: digest }
      });
    const sheet = await openSheet(page, home.id);
    const order = await setupOrder(sheet, rows);
    expect(order.length).toBe(2);
    const [first, second] = order;
    // The owner switches sharing off (in another tab) just as the first is placed.
    await page.route(`**/assistant-program/library/projects/${first.id}/staffing`, async route => {
      expect((await sharing(false, 'off-mid-batch')).ok()).toBe(true);
      await route.continue();
    });
    await sheet.getByRole('button', { name: 'Put 2 on the map' }).click();
    await sheet.getByRole('button', { name: 'Put on the map and add the assistant' }).click();
    const results = sheet.locator('[data-sheet-results]');
    await expect(results).toBeVisible({ timeout: 120_000 });
    const firstRow = results.locator(`[data-sheet-result="${first.id}"]`);
    await expect(firstRow).toContainText('Put on the map');
    await expect(firstRow).toContainText('switched off');
    await expect(firstRow.getByRole('link', { name: 'Review sharing on the Home' })).toBeVisible();
    await expect(results.locator(`[data-sheet-result="${second.id}"]`)).toContainText(
      'Not reached'
    );
    await page.screenshot({ path: `${recoveryEvidence}/03-sharing-off-mid-batch.png` });
    expect((await child(request, home.id, first.name)).agent_instances || []).toEqual([]);
    await page.unroute(`**/assistant-program/library/projects/${first.id}/staffing`);
    // Sharing back on: the project already on the map is repaired, never placed again.
    expect((await sharing(true, 'on-again')).ok()).toBe(true);
    await sheet.getByRole('button', { name: 'Back to the list' }).click();
    await sheet
      .getByRole('checkbox', { name: `Add the shared assistant to ${first.name}` })
      .check();
    await sheet.getByRole('button', { name: 'Add the assistant to 1' }).click();
    await sheet.getByRole('button', { name: 'Add the assistant', exact: true }).click();
    await expect(sheet.locator('[role="status"]')).toContainText(
      '1 project now has your shared REAPER Assistant.',
      { timeout: 60_000 }
    );
    expect(reaperAgents((await snapshot(request)).agents)).toEqual(['REAPER Assistant']);
    expect((await libraryRows(request, home.id)).get(second.name).connection).toBe('catalog_only');
  });

  test('recovery: closing during a request stops the rest and keeps what was done', async ({
    page,
    request
  }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width: 1440, height: 1000 });
    const home = await homeOf(request);
    const rows = await libraryRows(request, home.id);
    const sheet = await openSheet(page, home.id);
    const order = await setupOrder(sheet, rows);
    expect(order.length).toBe(1);
    const [only] = order;
    let release: () => void = () => {};
    const held = new Promise<void>(resolve => (release = resolve));
    // The last project's assistant request is held while the person closes the sheet.
    await page.route(`**/assistant-program/library/projects/${only.id}/staffing`, async route => {
      await held;
      await route.continue();
    });
    await sheet.getByRole('button', { name: 'Put 1 on the map' }).click();
    await sheet.getByRole('button', { name: 'Put on the map and add the assistant' }).click();
    await expect
      .poll(async () => (await libraryRows(request, home.id)).get(only.name).connection, {
        timeout: 60_000
      })
      .toBe('connected');
    await sheet.getByRole('button', { name: 'Close' }).click();
    await expect(page.locator(`[data-library-sheet="${home.id}"]`)).toHaveCount(0);
    release();
    // The request already sent still completes on the server; nothing reopens.
    await expect
      .poll(async () => (await child(request, home.id, only.name))?.agent_instances?.length || 0, {
        timeout: 60_000
      })
      .toBe(1);
    await expect(page.locator(`[data-library-sheet="${home.id}"]`)).toHaveCount(0);
    for (const name of ['Album-1', 'Album-2', 'Album-3', 'Album-4', 'Album-5']) {
      const ws = await child(request, home.id, name);
      for (const ticket of ws.tasks || []) expect(ticket.status, name).toBe('pending');
    }
    expect(reaperAgents((await snapshot(request)).agents)).toEqual(['REAPER Assistant']);
  });
});

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
