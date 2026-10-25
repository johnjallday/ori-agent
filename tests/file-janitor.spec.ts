import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdtempSync, mkdirSync, writeFileSync, existsSync, utimesSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { basename, join } from 'node:path';

/**
 * File Janitor as a workspace capability, end to end (PRD task 8.9).
 *
 * Run with:
 *   ./scripts/e2e.sh tests/file-janitor.spec.ts
 *
 * against an isolated smoke server (`wt demo 8931`, or the HOME/ORI_DATA_DIR
 * recipe in CLAUDE.md). The server must be able to read the fixture folders
 * this spec creates under the OS temp dir, so it has to run on this machine.
 *
 * NEVER point this at a real Downloads folder. Every test builds its own
 * throwaway fixture directory and passes an absolute path; the feature really
 * moves and really trashes files.
 *
 * What only a browser proves, and therefore what is here:
 *
 *   - The capability installs into an ORDINARY workspace — no blueprint, no
 *     template provenance — and a station and card appear for it. Server tests
 *     found this exact path broken (AppliesTo ignored the install record) while
 *     every one of them passed.
 *   - Pressing the station opens the console IN PLACE over the Map. The old
 *     behavior scrolled to an inline mount that is not on screen in Map mode,
 *     so the press appeared to do nothing — and no server assertion can see
 *     the difference.
 *   - Workspace Details carries a summary only: the review table and the
 *     settings form exist in the console and nowhere else.
 *   - Removal really makes the station and card disappear, and reinstall
 *     really requires a new folder choice.
 *
 * Classification, settling, approval-token binding, the adversarial filesystem
 * matrix, and paging arithmetic are covered by fast Go tests. Re-proving them
 * through a browser would be slow and no more convincing.
 *
 * This suite drives the CANONICAL `/file-janitor` routes. The legacy alias has
 * its own suite (tests/downloads-janitor.spec.ts); the two together are what
 * make retiring the alias later a deliberate act rather than a silent break.
 */

const RUN = Date.now().toString(36);
const OLD = new Date(Date.now() - 6 * 60 * 60 * 1000);
const workspaceSlugs = new Map<string, string>();

function workspacePath(workspaceId: string, query = ''): string {
  const slug = workspaceSlugs.get(workspaceId) || workspaceId;
  return `/workspaces/${encodeURIComponent(slug)}${query}`;
}

function fixtureFolder(label: string, files: Record<string, string>): string {
  const root = mkdtempSync(join(tmpdir(), `fj-${label}-`));
  // This feature really moves and really trashes files, so every fixture is
  // asserted to be a throwaway temp directory before anything is written into
  // it. The failure this prevents is not subtle: a fixture that resolved under
  // the developer's home would file their actual Downloads folder.
  if (!root.startsWith(tmpdir()) || root === tmpdir()) {
    throw new Error(`refusing to use ${root} as a fixture: not a temp directory`);
  }
  if (homedir() !== tmpdir() && root.startsWith(join(homedir(), 'Downloads'))) {
    throw new Error(`refusing to use ${root} as a fixture: it is inside a real Downloads folder`);
  }
  for (const [name, contents] of Object.entries(files)) {
    const path = join(root, name);
    mkdirSync(join(path, '..'), { recursive: true });
    writeFileSync(path, contents);
    utimesSync(path, OLD, OLD);
  }
  return root;
}

/**
 * Creates a plain workspace — no blueprint, no template provenance — with one
 * ordinary agent already on its roster.
 *
 * The agent exists purely to keep two unrelated dialogs off the screen. A
 * workspace with no agents offers "Hire a Commander for this workspace?", and
 * hiring that Commander in the browser then starts the workspace's first task,
 * which raises the Execution Check autonomy gate on top. Both cover the
 * surface under test and swallow every click.
 *
 * This is a real finding about the path to a newly installed capability, not
 * test scaffolding — it is recorded in the manual test guide. File Janitor
 * itself needs no agent (FR-37), and nothing below ever uses this one.
 */
async function createPlainWorkspace(request: APIRequestContext, name: string): Promise<string> {
  const res = await request.post('/api/workspaces', { data: { name, description: '' } });
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  const workspaceId = (body.folder?.id || body.workspace?.id) as string;
  const workspaceSlug = (body.folder?.folder_slug || body.workspace?.folder_slug) as string;
  workspaceSlugs.set(workspaceId, workspaceSlug || workspaceId);

  const agentName = `${name} Manager`;
  const created = await request.post('/api/agents', {
    data: { name: agentName, type: 'general', model: 'gpt-4o-mini' }
  });
  expect(created.ok(), await created.text()).toBeTruthy();

  const assigned = await request.put(`/api/agents/${encodeURIComponent(agentName)}/workspaces`, {
    data: { workspace_ids: [workspaceId] }
  });
  expect(assigned.ok(), await assigned.text()).toBeTruthy();

  return workspaceId;
}

async function installCapability(request: APIRequestContext, workspaceId: string) {
  const res = await request.post(
    `/api/workspaces/${workspaceId}/capabilities/file-janitor/install`,
    { data: { source: 'in-place' } }
  );
  expect(res.ok(), await res.text()).toBeTruthy();
}

/**
 * Grants the folder.
 *
 * One step cannot be automated: choosing the folder opens the operating
 * system's own picker, and no browser test can drive that dialog. The
 * confirmation the picker produces is posted directly instead — the same
 * request, with the same fixture path.
 */
async function grantFolder(request: APIRequestContext, workspaceId: string, root: string) {
  const res = await request.post(`/api/workspaces/${workspaceId}/file-janitor/setup`, {
    data: { path: root }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
}

test('managed protection endpoint is agentless, path-free and invalidated by revoke/relink', async ({
  request
}) => {
  const created = await request.post('/api/workspaces', {
    data: { name: `Protection facts ${RUN}` }
  });
  expect(created.ok()).toBeTruthy();
  const body = await created.json();
  const id = body.folder?.id || body.workspace?.id;
  await installCapability(request, id);
  const endpoint = `/api/workspaces/${id}/file-janitor/protection`;
  const initial = await (await request.get(endpoint)).json();
  expect(initial.state).toBe('unavailable');
  const root = fixtureFolder('stored-protection', {});
  await grantFolder(request, id, root);
  const first = await (await request.get(endpoint)).json();
  expect(first.state).toBe('not_covered');
  expect(first.reason).toBe('no_configuration');
  expect(first.root_generation).toBeTruthy();
  expect(first.binding_id).toMatch(/^[a-f0-9]{64}$/);
  expect(first.destination.kind).toBe('chooser');
  expect(JSON.stringify(first)).not.toContain(root);
  expect(first.last_completed_at).toBeUndefined();
  const revoked = await request.post(`/api/workspaces/${id}/file-janitor/revoke`, { data: {} });
  expect(revoked.ok(), await revoked.text()).toBeTruthy();
  const unavailable = await (await request.get(endpoint)).json();
  expect(unavailable.state).toBe('unavailable');
  expect(unavailable.root_generation).toBeUndefined();
  await grantFolder(request, id, fixtureFolder('stored-protection-relink', {}));
  const relinked = await (await request.get(endpoint)).json();
  expect(relinked.state).toBe('not_covered');
  expect(relinked.root_generation).not.toBe(first.root_generation);
  expect(relinked.binding_id).not.toBe(first.binding_id);
  const alias = await request.get(`/api/workspaces/${id}/downloads-janitor/protection`);
  expect((await alias.json()).binding_id).toBe(relinked.binding_id);
  const evidence = 'tasks/evidence-file-curator-backup-guidance/group-2';
  mkdirSync(evidence, { recursive: true });
  writeFileSync(
    join(evidence, 'stored-endpoint.json'),
    JSON.stringify({ initial, first, unavailable, relinked }, null, 2)
  );
});

const console_ = (page: Page) => page.locator('#fileJanitorConsole');
const consoleBody = (page: Page) => page.locator('#fileJanitorConsoleBody');

async function captureCuratorSettings(page: Page, path: string) {
  // The page has a persistent event stream, so networkidle is not a useful
  // readiness condition. Await the console's own canonical refresh instead.
  await page.evaluate(() => (window as any).FileJanitorConsole.refresh());
  await page.locator('.fj-setting-curator').screenshot({ path, animations: 'disabled' });
}

/**
 * Opens a workspace and clears the modals a brand-new one puts in the way.
 *
 * Two of them, neither belonging to File Janitor:
 *
 *   1. "Hire a Commander for this workspace?" — a workspace with no agents
 *      offers this once per session; "Not now" declines it.
 *   2. The Execution Check autonomy gate — hiring that Commander starts the
 *      workspace's first task, which asks for confirmation before it runs.
 *
 * Both sit over the surface under test and swallow every click, so the spec
 * clears them exactly as a user would. The capability itself needs no agent
 * (FR-37), and these tests prove it by declining the Commander.
 */
async function clearBlockingModals(page: Page) {
  const gate = page.locator('#workspace-detail-task-confirm-modal');
  const notNow = gate.getByRole('button', { name: 'Not now' });
  if (await notNow.isVisible().catch(() => false)) {
    await notNow.click();
    await expect(gate).toBeHidden({ timeout: 15000 });
  }
  if (await gate.isVisible().catch(() => false)) {
    await page.locator('#workspace-detail-task-confirm-close').click();
    await expect(gate).toBeHidden({ timeout: 15000 });
  }
}

/**
 * Keeps first-run onboarding out of the way.
 *
 * A sandbox server has never been onboarded, so it opens the "Hi there! I'm
 * Ori" wizard over every page. That is unrelated to this feature and would
 * otherwise be the only thing these tests measured.
 */
async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );
}

async function clearCommanderNudge(page: Page, url: string) {
  await skipOnboarding(page);
  await page.goto(url);
  await clearBlockingModals(page);
}

async function openWorkspace(page: Page, workspaceId: string, query = '') {
  await clearCommanderNudge(page, workspacePath(workspaceId, query));
}

async function openConsoleFromCard(page: Page) {
  await page.locator('#fileJanitorCardOpen').click();
  await expect(console_(page)).toBeVisible({ timeout: 15000 });
}

test.describe('File Janitor capability', () => {
  test('built-in Curator guidance follows a renamed template role under another lead', async ({
    page,
    request
  }, testInfo) => {
    const lead = `Guidance Lead ${RUN}`;
    const helper = `Guidance Helper ${RUN}`;
    const created = await request.post('/api/agents', {
      data: { name: lead, type: 'general', model: 'gpt-4o-mini' }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const response = await request.post('/api/workspaces', {
      data: {
        name: `Guidance Template ${RUN}`,
        template_id: 'file-janitor',
        existing_agent_names: [lead],
        entry_agent_name: lead,
        template_agent_overrides: [{ index: 0, name: helper }]
      }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    const { folder } = await response.json();
    workspaceSlugs.set(folder.id, folder.folder_slug);
    const statusResponse = await request.get(`/api/workspaces/${folder.id}/file-janitor`);
    const { status } = await statusResponse.json();
    expect(status.guidance.bundled).toBe(true);
    expect(status.guidance.available).toBe(true);
    const instance = folder.agent_instances.find(
      (item: { id: string }) => item.id === status.guidance.agent_instance_id
    );
    expect(instance.name).toBe(helper);
    expect(instance.entry_point).not.toBe(true);
    await grantFolder(request, folder.id, fixtureFolder('guidance-template', {}));
    await openWorkspace(page, folder.id, '?view=details&panel=file-janitor&tab=settings');
    await expect(console_(page)).toBeVisible();
    await expect(consoleBody(page)).toContainText(
      'Built-in filing and backup guidance is available'
    );
    await expect(consoleBody(page)).toContainText('not an enabled user skill or permission');
    await captureCuratorSettings(page, testInfo.outputPath('curator-template-guidance.png'));
  });

  test('Curator settings explicitly recover an unassociated agent and support in-place add', async ({
    page,
    request
  }, testInfo) => {
    const id = await createPlainWorkspace(request, `Guidance Recovery ${RUN}`);
    await installCapability(request, id);
    await grantFolder(request, id, fixtureFolder('guidance-recovery', {}));
    await openWorkspace(page, id, '?view=details&panel=file-janitor&tab=settings');
    await expect(consoleBody(page)).toContainText('could not verify a companion association');
    await captureCuratorSettings(page, testInfo.outputPath('curator-association-recovery.png'));
    await page.locator('#fileJanitorAssociateCurator').click();
    await expect(consoleBody(page)).toContainText(
      'Built-in filing and backup guidance is available'
    );
    await captureCuratorSettings(page, testInfo.outputPath('curator-associated-guidance.png'));

    const addedID = await createPlainWorkspace(request, `Guidance Added ${RUN}`);
    await installCapability(request, addedID);
    await grantFolder(request, addedID, fixtureFolder('guidance-added', {}));
    await openWorkspace(page, addedID, '?view=details&panel=file-janitor&tab=settings');
    await page.locator('#fileJanitorAddCurator').click();
    await expect(consoleBody(page)).toContainText(
      'Built-in filing and backup guidance is available'
    );
    await captureCuratorSettings(page, testInfo.outputPath('curator-in-place-guidance.png'));
  });

  test('next action follows setup, scan, shared review, partial receipts and undo', async ({
    page,
    request
  }, testInfo) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const capture = (name: string) =>
      page.screenshot({ path: testInfo.outputPath(name), fullPage: true, animations: 'disabled' });
    const fixtureName = `Next Action Curator ${RUN}`;
    const workspaceId = await createPlainWorkspace(request, fixtureName);
    await installCapability(request, workspaceId);
    await openWorkspace(page, workspaceId, '?mode=details&source=next-action');
    const area = page.getByRole('region', { name: 'Next action', exact: true });
    await expect(area).toContainText('Choose the folder to tidy');
    await capture('next-action-setup.png');
    const root = fixtureFolder('next-action', {
      'first.pdf': 'synthetic',
      'second.csv': 'synthetic'
    });
    await grantFolder(request, workspaceId, root);
    await request.post(`/api/workspaces/${workspaceId}/file-janitor/pause`, {
      data: { paused: true }
    });
    await page.evaluate(() => (window as any).FileJanitorConsole.refresh());
    await expect(area).toContainText('File Janitor is paused');
    await page.locator('#workspaceNextAction').click();
    await page.locator('#fileJanitorScan').click();
    await expect(consoleBody(page).locator('.fj-select')).toHaveCount(2);
    await page.keyboard.press('Escape');
    await expect(area).toContainText('2 files ready for review');
    await page.locator('[data-cmd-view-mode="map"]').click();
    await expect(area).toContainText('2 files ready for review');
    await expect(page.locator('[data-cmd-map-select-agent]').first()).toContainText(
      'Ready to help'
    );
    await expect(page.locator('[data-cmd-hq-station="memory"]')).toBeVisible();
    await capture('next-action-review-map.png');
    // Queued work is a labelled response fixture; no task is started or written.
    const tasksRoute = '**/api/orchestration/tasks?**';
    await page.route(tasksRoute, route =>
      route.fulfill({
        json: {
          tasks: [
            {
              id: 'queued-demo',
              status: 'pending',
              to: fixtureName + ' Manager',
              description: 'Synthetic queued task'
            }
          ]
        }
      })
    );
    await page.evaluate(() => (window as any).workspaceDetail.loadTasks());
    await expect(page.locator('[data-cmd-map-select-agent]').first()).toContainText('Waiting');
    await capture('next-action-queued-fixture.png');
    await page.unroute(tasksRoute);
    await page.evaluate(() => (window as any).workspaceDetail.loadTasks());
    const operating: string[] = [];
    page.on('request', req => {
      if (req.method() === 'POST' && /file-janitor\/(scan|preview|apply|decisions)/.test(req.url()))
        operating.push(req.url());
    });
    await page.locator('#workspaceNextAction').click();
    await expect(consoleBody(page).locator('.fj-select').first()).toBeEnabled();
    await consoleBody(page).locator('.fj-select').first().check();
    const choice = await consoleBody(page).locator('.fj-select').first().getAttribute('id');
    await page.getByRole('button', { name: 'Mark second.csv for Trash', exact: true }).click();
    await page.evaluate(() => (window as any).FileJanitorConsole.refresh());
    await expect(
      page.getByRole('button', { name: 'Unmark second.csv for Trash', exact: true })
    ).toHaveAttribute('aria-pressed', 'true');
    await page.keyboard.press('Escape');
    await expect(page.locator('#workspaceNextAction')).toBeFocused();
    await page.locator('[data-cmd-view-mode="details"]').click();
    await page.locator('#workspaceNextAction').click();
    await expect(page.locator('#' + choice)).toBeChecked();
    expect(operating).toEqual([]);
    expect(new URL(page.url()).searchParams.get('source')).toBe('next-action');
    await page.getByRole('button', { name: 'Unmark second.csv for Trash', exact: true }).click();
    await consoleBody(page).locator('.fj-select').nth(1).check();
    await page.locator('#fileJanitorApprove').click();
    await expect(page.locator('#fileJanitorConfirmApply')).toBeVisible();
    await page.evaluate(() => (window as any).FileJanitorConsole.refresh());
    await page.keyboard.press('Escape');
    await expect(page.locator('#fileJanitorConfirmApply')).toBeVisible();
    await page.goBack();
    await expect(page.locator('#fileJanitorConsole')).toBeHidden();
    await expect(page.locator('#workspaceNextAction')).toBeFocused();
    await page.locator('#workspaceNextAction').click();
    await expect(page.locator('#fileJanitorConfirmApply')).toBeVisible();
    expect(operating.filter(url => /\/(apply|scan|decisions)$/.test(url))).toEqual([]);
    // Only a synthetic fixture is changed after preview: the real apply must
    // refuse that one file while recording the other successful move.
    writeFileSync(join(root, 'second.csv'), 'changed after preview');
    await page.locator('#fileJanitorConfirmApply').click();
    await expect(consoleBody(page).locator('.fj-results')).toContainText('1 file filed');
    await expect
      .poll(() =>
        page.evaluate(
          () => (window as any).FileJanitorConsole.getActionState().latestReceipt?.counts
        )
      )
      .toEqual({ applied: 1, failed: 0, stale: 1 });
    await expect(page.locator('#fileJanitorConsoleStatus')).not.toContainText(
      'files ready for review'
    );
    await capture('next-action-partial-result.png');
    await page.keyboard.press('Escape');
    await expect(area).toContainText('not applied');
    await page.locator('#workspaceNextAction').click();
    await page.locator('[data-fj-tab="history"]').click();
    await expect(consoleBody(page)).toContainText('first.pdf');
    await consoleBody(page).getByRole('button', { name: 'Undo move', exact: true }).click();
    await expect.poll(() => existsSync(join(root, 'first.pdf'))).toBe(true);
    await expect(consoleBody(page)).toContainText('put back');
    await capture('next-action-undo.png');
    const emptyWorkspace = await createPlainWorkspace(request, `Next Action Empty ${RUN}`);
    await installCapability(request, emptyWorkspace);
    await grantFolder(request, emptyWorkspace, fixtureFolder('next-action-empty', {}));
    await request.post(`/api/workspaces/${emptyWorkspace}/file-janitor/pause`, {
      data: { paused: true }
    });
    await openWorkspace(page, emptyWorkspace, '?mode=map');
    await page.locator('#workspaceNextAction').click();
    await page.locator('#fileJanitorScan').click();
    await expect
      .poll(() =>
        page.evaluate(() => (window as any).FileJanitorConsole.getActionState().emptyState)
      )
      .toBe('scan_empty');
    await page.keyboard.press('Escape');
    await expect(area).toContainText('last scan found no eligible files');
    await capture('next-action-empty.png');
  });
  test('installs into an ordinary workspace and shows a card that opens the console', async ({
    page,
    request
  }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ Install ${RUN}`);
    await installCapability(request, workspaceId);

    await openWorkspace(page, workspaceId);

    // The compact card appears on nothing but the persisted install record —
    // this workspace has no blueprint, no folder, and no janitor state.
    const card = page.locator('#fileJanitorMount');
    await expect(card).toBeVisible({ timeout: 15000 });
    await expect(card).toContainText('File Janitor');
    await expect(card).toContainText('No folder chosen yet');

    // And Details carries a summary only: no review table, no settings form.
    await expect(card.locator('table')).toHaveCount(0);
    await expect(page.locator('#fileJanitorSettingsHost')).toHaveCount(0);
    await expect(page.locator('#fileJanitorBatch')).toHaveCount(0);

    // The single action leads into the real setup surface.
    await expect(page.locator('#fileJanitorCardOpen')).toHaveText('Set up File Janitor');
    await openConsoleFromCard(page);
    await expect(consoleBody(page).locator('#fileJanitorPath')).toBeVisible();
  });

  test('keeps the summary in Details while Map remains station-first across slow status and refresh', async ({
    page,
    request
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const workspaceId = await createPlainWorkspace(request, `FJ Views ${RUN}`);
    const root = fixtureFolder('views-with-an-intentionally-long-managed-folder-name', {
      'map-entry.pdf': 'fixture'
    });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);
    const scanned = await request.post(`/api/workspaces/${workspaceId}/file-janitor/scan`);
    expect(scanned.ok(), await scanned.text()).toBeTruthy();

    await skipOnboarding(page);
    let releaseStatus!: () => void;
    const statusGate = new Promise<void>(resolve => {
      releaseStatus = resolve;
    });
    let heldStatus = false;
    await page.route(`**/api/workspaces/${workspaceId}/file-janitor`, async route => {
      if (!heldStatus) {
        heldStatus = true;
        await statusGate;
      }
      await route.continue();
    });

    await page.goto(workspacePath(workspaceId, '?mode=map'), {
      waitUntil: 'domcontentloaded'
    });
    await expect(page.locator('#fileJanitorMount')).toBeHidden();
    releaseStatus();
    await clearBlockingModals(page);

    const station = page.locator('[data-cmd-hq-station="file-janitor"]');
    await expect(station).toBeVisible({ timeout: 15000 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect(station.locator('[data-building-variant="depot"]')).toBeVisible();
    await expect(station.locator('.ws-cmd-map-hq-station-label')).toHaveText('File Janitor');
    await expect(station.locator('.ws-cmd-map-hq-station-location')).toHaveAttribute(
      'title',
      basename(root)
    );
    await expect(station).toHaveAttribute(
      'aria-label',
      new RegExp(`managed folder ${basename(root)}.*ready for review`)
    );
    await expect(page.locator('#fileJanitorMount')).toBeHidden();

    // The curated depot art resolves through Command's theme token in both
    // themes instead of freezing one swatch onto the map. Drive the real theme
    // control so its in-memory preference cannot overwrite a bare DOM change.
    if ((await page.locator('html').getAttribute('data-bs-theme')) !== 'light') {
      await page.locator('#darkModeToggle').click();
    }
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', 'light');
    await page.waitForTimeout(100);
    const lightDepotColor = await station
      .locator('[data-building-variant="depot"]')
      .evaluate(node => getComputedStyle(node).color);
    await page.locator('#darkModeToggle').click();
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', 'dark');
    await page.waitForTimeout(100);
    const darkDepotColor = await station
      .locator('[data-building-variant="depot"]')
      .evaluate(node => getComputedStyle(node).color);
    expect(lightDepotColor).not.toBe(darkDepotColor);
    await expect(station).toBeVisible();

    // The larger illustrated station remains inside the map at desktop width,
    // including when its visible folder line has to truncate.
    const [stationBox, worldBox] = await Promise.all([
      station.boundingBox(),
      page.locator('.ws-cmd-map-world').boundingBox()
    ]);
    expect(stationBox).toBeTruthy();
    expect(worldBox).toBeTruthy();
    expect(stationBox!.x + stationBox!.width).toBeLessThanOrEqual(
      worldBox!.x + worldBox!.width + 1
    );
    expect(stationBox!.x).toBeGreaterThanOrEqual(worldBox!.x - 1);

    // A later capability status refresh may update station text, but it cannot
    // grant presentation permission to the Details-only summary.
    await page.evaluate(async () => {
      await (window as any).FileJanitorConsole.refresh();
    });
    await expect(page.locator('#fileJanitorMount')).toBeHidden();

    // The station is the real Map entry point, not decoration.
    await station.click();
    await expect(console_(page)).toBeVisible({ timeout: 15000 });
    await page.locator('[data-fj-console-close]').click();
    await expect(station).toBeFocused();

    await page.getByRole('button', { name: 'Tickets', exact: true }).click();
    await expect(page.locator('#fileJanitorMount')).toBeHidden();

    await page.getByRole('button', { name: 'Details', exact: true }).click();
    const summary = page.getByRole('group', { name: 'File Janitor' });
    await expect(summary).toBeVisible();
    const summaryBox = await summary.boundingBox();
    expect(summaryBox).toBeTruthy();
    expect(summaryBox!.height).toBeLessThanOrEqual(120);
    await expect(summary).toContainText('Managed folder');
    await expect(summary).toContainText(basename(root));
    await expect(summary).toContainText('1 file waiting for review');
    await expect(page.locator('#fileJanitorActivity')).toHaveText('Review ready');
    await expect(summary).toHaveClass(/is-review-ready/);
    await expect(summary).toContainText('Privacy mode');
    await expect(summary).toContainText('Local only');
    await expect(
      summary.getByRole('button', { name: 'Review files · 1', exact: true })
    ).toBeVisible();

    await page.getByRole('button', { name: 'Map', exact: true }).click();
    await expect(page.locator('#fileJanitorMount')).toBeHidden();
    await page.goBack();
    await expect(page.getByRole('button', { name: 'Details', exact: true })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    await expect(page.locator('#fileJanitorMount')).toBeVisible();
    await page.goForward();
    await expect(station).toBeVisible();
    await expect(page.locator('#fileJanitorMount')).toBeHidden();
  });

  // A generic in-place install must NOT propose a folder. Pre-filling a real
  // path next to a button that grants access to it turns an explicit approval
  // into a default.
  test('generic setup proposes no folder and will not confirm an empty one', async ({
    page,
    request
  }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ Empty ${RUN}`);
    await installCapability(request, workspaceId);

    await openWorkspace(page, workspaceId);
    await openConsoleFromCard(page);

    const input = consoleBody(page).locator('#fileJanitorPath');
    await expect(input).toHaveValue('');
    await expect(consoleBody(page).locator('#fileJanitorConfirm')).toBeDisabled();

    // The disclosure is present before anything is granted.
    await expect(consoleBody(page)).toContainText('never deletes anything permanently');
    await expect(consoleBody(page)).toContainText('Nothing moves without your approval');
  });

  test('the console reviews, approves, and records a real move', async ({ page, request }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ Review ${RUN}`);
    const root = fixtureFolder('review', {
      'invoice.pdf': 'invoice',
      'holiday.png': 'photo'
    });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);

    await openWorkspace(page, workspaceId);
    await openConsoleFromCard(page);

    // Scan from the console header.
    await page.locator('#fileJanitorScan').click();
    await expect(consoleBody(page).locator('.fj-row-item').first()).toBeVisible({ timeout: 15000 });
    const progress = consoleBody(page).locator('.fj-batch-progress');
    await expect(progress).toContainText('2 remaining of 2 candidates');
    await expect(progress).toContainText('need review within remaining');

    await expect(consoleBody(page).locator('.fj-table th')).toHaveText([
      'Select',
      'File',
      'Destination',
      'Why / Status',
      'Actions'
    ]);
    const row = consoleBody(page).locator('.fj-row-item').filter({ hasText: 'invoice.pdf' });
    await expect(row).toBeVisible();
    // The row shows what the decision rests on: where it would go.
    await expect(row).toContainText('Filed/Documents');
    const details = row.locator('.fj-file-details-toggle');
    await expect(details).toHaveAccessibleName('Show file details for invoice.pdf');
    await details.click();
    await expect(details).toHaveAttribute('aria-expanded', 'true');
    await expect(details).toHaveAccessibleName('Hide file details for invoice.pdf');
    const fileDetails = row.locator('.fj-file-details');
    await expect(fileDetails.locator('dt')).toHaveText(['Type', 'Size', 'Modified']);
    await expect(fileDetails.locator('dd').nth(0)).toHaveText('.pdf');
    await expect(fileDetails.locator('dd').nth(1)).toHaveText('7 B');
    const [detailsBox, footerBox] = await Promise.all([
      fileDetails.boundingBox(),
      page.locator('.fj-footer').boundingBox()
    ]);
    expect(detailsBox).toBeTruthy();
    expect(footerBox).toBeTruthy();
    expect(detailsBox!.y + detailsBox!.height).toBeLessThanOrEqual(footerBox!.y);

    await row.locator('.fj-select').check();
    await expect(page.locator('.fj-footer')).toBeInViewport();
    await expect(page.locator('#fileJanitorApprove')).toHaveText('Review 1 move');
    await page.locator('#fileJanitorApprove').click();

    // Nothing has moved yet: this is the confirmation, in the console.
    await expect(consoleBody(page)).toContainText('Confirm', { timeout: 15000 });
    expect(existsSync(join(root, 'invoice.pdf'))).toBe(true);

    await consoleBody(page)
      .getByRole('button', { name: /Move|Apply|Confirm these/ })
      .first()
      .click();

    // 30s, matching the History test: on a cold server this may be the first
    // apply the process has ever done, and it flaked at 15s. Waiting longer
    // costs nothing when it passes.
    await expect
      .poll(() => existsSync(join(root, 'Filed', 'Documents', 'invoice.pdf')), { timeout: 30000 })
      .toBe(true);
    expect(existsSync(join(root, 'invoice.pdf'))).toBe(false);
    // The file nobody approved is untouched.
    expect(existsSync(join(root, 'holiday.png'))).toBe(true);

    const results = consoleBody(page).locator('.fj-results');
    await expect(results).toContainText('1 file filed');
    await expect(results).toContainText('invoice.pdf');
    await results.getByRole('button', { name: 'View History' }).click();
    await expect(console_(page).locator('[data-fj-tab="history"]')).toHaveAttribute(
      'aria-selected',
      'true'
    );
    await expect(consoleBody(page)).toContainText('invoice.pdf');
  });

  test('a failed batch refresh keeps the last known review visible but inert', async ({
    page,
    request
  }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ Batch failure ${RUN}`);
    const root = fixtureFolder('batch-failure', { 'invoice.pdf': 'invoice' });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);

    await openWorkspace(page, workspaceId);
    await openConsoleFromCard(page);
    await page.locator('#fileJanitorScan').click();
    const row = consoleBody(page).locator('.fj-row-item').filter({ hasText: 'invoice.pdf' });
    await expect(row).toBeVisible({ timeout: 15000 });

    const latestBatch = `**/api/workspaces/${workspaceId}/file-janitor/batches/latest*`;
    await page.route(latestBatch, route =>
      route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: { message: 'Simulated batch read failure.' } })
      })
    );
    await page.evaluate(async () => {
      await (window as any).FileJanitorConsole._reloadBatch();
    });

    const unavailable = consoleBody(page).locator('.fj-batch-load-error');
    await expect(unavailable).toContainText('Review unavailable');
    await expect(unavailable).toContainText('Showing the last known batch');
    await expect(row).toBeVisible();
    await expect(row.locator('.fj-select')).toBeDisabled();
    await expect(page.locator('#fileJanitorApprove')).toBeDisabled();
    await expect(consoleBody(page)).not.toContainText('Nothing to review');

    await page.unroute(latestBatch);
    await unavailable.getByRole('button', { name: 'Retry' }).click();
    await expect(row.locator('.fj-select')).toBeEnabled({ timeout: 15000 });
  });

  test('History records the move and offers to undo it', async ({ page, request }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ History ${RUN}`);
    const root = fixtureFolder('history', { 'report.pdf': 'report' });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);

    // File one thing through the UI, because that is the path that produces a
    // journal entry the way a user does. Reconstructing preview/apply over HTTP
    // means re-deriving the exact decision the approval was issued for, and
    // getting it slightly wrong tests the reconstruction rather than History.
    await openWorkspace(page, workspaceId);
    await openConsoleFromCard(page);
    await page.locator('#fileJanitorScan').click();

    const row = consoleBody(page).locator('.fj-row-item').filter({ hasText: 'report.pdf' });
    await expect(row).toBeVisible({ timeout: 15000 });
    await row.locator('.fj-select').check();
    await page.locator('#fileJanitorApprove').click();
    await expect(consoleBody(page)).toContainText('Confirm', { timeout: 15000 });
    await consoleBody(page)
      .getByRole('button', { name: /Move|Apply|Confirm these/ })
      .first()
      .click();
    // A generous window: on a cold server this is the first apply the process
    // has ever done, and it flaked once at 15s against a brand-new sandbox.
    // Waiting longer costs nothing when it passes.
    await expect
      .poll(() => existsSync(join(root, 'Filed', 'Documents', 'report.pdf')), { timeout: 30000 })
      .toBe(true);

    await console_(page).locator('[data-fj-tab="history"]').click();

    await expect(consoleBody(page)).toContainText('report.pdf', { timeout: 15000 });
    await expect(consoleBody(page)).toContainText('Filed/Documents');

    // Undo puts the file back where it was.
    await consoleBody(page).getByRole('button', { name: /Undo/ }).first().click();
    await expect.poll(() => existsSync(join(root, 'report.pdf')), { timeout: 15000 }).toBe(true);
  });

  test('pause and resume are reachable from the console header', async ({ page, request }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ Pause ${RUN}`);
    const root = fixtureFolder('pause', { 'a.pdf': 'a' });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);

    await openWorkspace(page, workspaceId);
    await openConsoleFromCard(page);

    const pause = page.locator('#fileJanitorPause');
    await expect(pause).toBeVisible();
    const initial = (await pause.textContent())?.trim();
    await pause.click();
    await expect(pause).not.toHaveText(initial ?? '', { timeout: 15000 });
  });

  // Deep links are how a notification points at this console.
  test('a deep link opens the console, and Back closes it without leaving', async ({
    page,
    request
  }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ Link ${RUN}`);
    const root = fixtureFolder('link', { 'a.pdf': 'a' });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);

    // Open the workspace normally FIRST, so Back has somewhere to go that is
    // not about:blank. A notification arriving in an already-open browser is
    // the real case anyway.
    await openWorkspace(page, workspaceId);
    await openConsoleFromCard(page);
    await console_(page).locator('[data-fj-tab="settings"]').click();
    await expect(console_(page).locator('[data-fj-tab="settings"]')).toHaveAttribute(
      'aria-selected',
      'true'
    );
    await expect(page).toHaveURL(/panel=file-janitor/, { timeout: 15000 });

    // Opening pushed one entry, and choosing Settings pushed another. So the
    // first Back restores the previous TAB rather than closing — which is the
    // specified behavior (FR-124: Back closes a deep-linked console or restores
    // its previous valid tab state).
    await page.goBack();
    await expect(console_(page)).toBeVisible();
    await expect(console_(page).locator('[data-fj-tab="review"]')).toHaveAttribute(
      'aria-selected',
      'true',
      { timeout: 15000 }
    );

    // The next Back leaves the console entirely, without leaving the workspace.
    await page.goBack();
    await expect(console_(page)).toBeHidden({ timeout: 15000 });
    expect(page.url()).toContain(workspacePath(workspaceId));
    await expect(page).not.toHaveURL(/panel=file-janitor/);
  });

  // An unknown tab must not stop the workspace loading.
  test('a deep link with a bad tab still opens a working console', async ({ page, request }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ BadLink ${RUN}`);
    const root = fixtureFolder('badlink', { 'a.pdf': 'a' });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);

    await clearCommanderNudge(
      page,
      workspacePath(workspaceId, '?panel=file-janitor&tab=not-a-tab')
    );
    await expect(console_(page)).toBeVisible({ timeout: 15000 });
    await expect(console_(page).locator('[data-fj-tab="review"]')).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  test('removal confirms with this workspace facts, then clears the card', async ({
    page,
    request
  }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ Remove ${RUN}`);
    const root = fixtureFolder('remove', { 'keep-me.pdf': 'keep' });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);

    await openWorkspace(page, workspaceId);
    await openConsoleFromCard(page);
    await console_(page).locator('[data-fj-tab="settings"]').click();

    await page.locator('#fileJanitorRemove').click();

    // The confirmation names the folder and states the thing that matters most.
    const folderName = root.split('/').pop() as string;
    await expect(consoleBody(page)).toContainText(folderName, { timeout: 15000 });
    await expect(consoleBody(page)).toContainText('No files are moved, renamed, deleted');

    await page.locator('#fileJanitorRemoveConfirm').click();

    // The console closes and the card goes with it.
    await expect(console_(page)).toBeHidden({ timeout: 15000 });
    await expect(page.locator('#fileJanitorMount')).toBeHidden({ timeout: 15000 });

    // And the user's file is exactly where it was.
    expect(existsSync(join(root, 'keep-me.pdf'))).toBe(true);
  });

  test('reinstall requires a new folder choice rather than resuming the old one', async ({
    page,
    request
  }) => {
    const workspaceId = await createPlainWorkspace(request, `FJ Reinstall ${RUN}`);
    const root = fixtureFolder('reinstall', { 'a.pdf': 'a' });
    await installCapability(request, workspaceId);
    await grantFolder(request, workspaceId, root);

    const removed = await request.delete(
      `/api/workspaces/${workspaceId}/capabilities/file-janitor`,
      { data: { remove_companion: false } }
    );
    expect(removed.ok(), await removed.text()).toBeTruthy();

    await installCapability(request, workspaceId);
    await openWorkspace(page, workspaceId);

    const card = page.locator('#fileJanitorMount');
    await expect(card).toBeVisible({ timeout: 15000 });
    await expect(card).toContainText('Folder access was revoked');
    await expect(page.locator('#fileJanitorCardOpen')).toHaveText('Set up File Janitor');
  });

  // The generic blueprint's Setup Wizard must offer a way to choose a folder.
  //
  // This is the gap that let a real blocker ship: every other test here grants
  // the folder over HTTP, so none of them ever opened the wizard's folder step.
  // The step's renderer IS the picker, and it was keyed to the Downloads
  // preset's adapter id alone — so on the generic blueprint it drew nothing, the
  // wizard fell back to its own "Approve and continue", and there was no way to
  // choose a folder at all. Nothing errored; the button simply approved nothing.
  test('the generic blueprint wizard offers a folder picker', async ({ page, request }) => {
    const created = await request.post('/api/workspaces', {
      data: {
        name: `FJ Blueprint ${RUN}`,
        description: '',
        template_id: 'file-janitor',
        create_template_agents: true
      }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const body = await created.json();
    const workspaceId = (body.folder?.id || body.workspace?.id) as string;
    const workspaceSlug = (body.folder?.folder_slug || body.workspace?.folder_slug) as string;
    workspaceSlugs.set(workspaceId, workspaceSlug || workspaceId);

    await skipOnboarding(page);
    await page.goto(workspacePath(workspaceId));

    const dialog = page.locator('#setupWizardDialog');
    if (!(await dialog.isVisible().catch(() => false))) {
      await page.locator('#setupWizardBannerAction').click();
    }
    await expect(dialog).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Choose the folder to tidy', {
      timeout: 15000
    });

    // The picker exists, and the primary button says a folder is still needed
    // rather than offering to approve nothing.
    await expect(page.locator('#fileJanitorWizardPick')).toBeVisible();
    await expect(page.locator('#setupWizardPrimary')).toHaveText(/Choose a folder to continue/);
    await expect(page.locator('#setupWizardPrimary')).toBeDisabled();

    // And still no editable path field: the picker is the only way in (FR-52).
    await expect(page.locator('#fileJanitorPath')).toHaveCount(0);
  });
});
