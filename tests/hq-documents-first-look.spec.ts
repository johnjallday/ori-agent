import { expect, test } from '@playwright/test';
import { mkdir, readFile, readdir, realpath, rename, utimes, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { installLocalCdn } from './helpers/offline-cdn';
import { confirmReviewedCreation } from './helpers/confirm-reviewed-creation';
import { choiceInventory } from './helpers/workspace-choice-state';

// A split profile is essential: never authorize Documents under protected app data.
test('the manager offers a first look on Open; dismissal and return do not start work', async ({
  page,
  request
}, info) => {
  const liveModel = process.env.ORI_DOCUMENT_REVIEW_LIVE_MODEL;
  const legacy = process.env.ORI_GUIDED_LOOK_LEGACY === '1';
  const empty = process.env.ORI_GUIDED_LOOK_EMPTY === '1';
  expect(empty && Boolean(liveModel)).toBe(false);
  test.setTimeout(liveModel ? 240_000 : 120_000);
  const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
  test.skip(!sandbox || process.env.ORI_DEMO_SPLIT_PROFILE !== '1', 'requires fresh split sandbox');
  const base = await realpath(sandbox!);
  expect(base).toMatch(/\/ori-(?:demo|e2e)\.[^/]+$/);
  const home = join(base, 'home');
  const data = join(base, 'data');
  expect(
    (await (await request.get('/api/settings/workspace-root')).json()).default_workspace_root
  ).toBe(join(home, 'Ori Workspaces'));
  const hq = await freshGuidedHQ(request);
  const root = join(home, 'Documents');
  await mkdir(root, { mode: 0o750 });
  if (!empty)
    await writeFile(join(root, 'estimate.txt'), 'Synthetic estimate for cabinet order PO-4387.', {
      mode: 0o600
    });
  if (!empty) {
    const old = new Date(Date.now() - 3600000);
    await utimes(join(root, 'estimate.txt'), old, old);
  }
  const fixtures = {
    'estimate.txt': 'Synthetic estimate for cabinet order PO-4387.',
    'packing-slip.txt': 'Kitchen remodel at Maple Street. Packing slip for cabinet order PO-4387.',
    'invoice.txt': 'Kitchen remodel at Maple Street. Invoice for cabinet order PO-4387.'
  };
  if (liveModel) {
    const configured = await request.post('/api/settings/system-model', {
      data: { provider: 'claude', model: liveModel }
    });
    expect(configured.ok()).toBe(true);
    fixtures['estimate.txt'] =
      'Kitchen remodel at Maple Street. Estimate for cabinet order PO-4387.';
    for (const [name, body] of Object.entries(fixtures)) {
      const file = join(root, name);
      await writeFile(file, body, { mode: 0o600 });
      const old = new Date(Date.now() - 3600_000);
      await utimes(file, old, old);
    }
    await mkdir(join(root, 'Project receipts'), { mode: 0o750 });
  }
  const before = await choiceInventory(request, data);
  const starts: string[] = [];
  const effects: string[] = [];
  page.on('request', r => {
    if (r.method() === 'GET') return;
    const path = new URL(r.url()).pathname;
    if (path.endsWith('/folder-manager-look')) starts.push(path);
    if (
      /organization-reviews|organization-file|\/file-search\/(sources|scan)|\/api\/chat$/.test(path)
    )
      effects.push(path);
  });
  await installLocalCdn(page);
  await page.goto(hq + '?mode=details');
  const suggestion = page.locator('[data-hq-firstvisit]');
  await suggestion.getByRole('button', { name: /Create a workspace from Documents/ }).click();
  await suggestion.getByRole('link', { name: 'Go to Home' }).click();
  await expect(page.locator('#cockpitCreateWorkspaceBtn')).toBeFocused();
  await page.locator('#cockpitCreateWorkspaceBtn').press('Enter');
  await expect(page.locator('#workspaceAnchorPath')).toHaveValue(root);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#workspaceRoleRoster img[src*="/research-archivist/"]')).toBeVisible();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toContainText('Opening the workspace does not start it');
  const confirmation = page.waitForResponse(
    r => r.request().method() === 'POST' && /create-operations\/[^/]+\/confirm$/.test(r.url())
  );
  await confirmReviewedCreation(page);
  const folder = (await (await confirmation).json()).folder;
  const id = String(folder.id);
  expect(id).toMatch(/^[0-9a-f-]{36}$/);
  const verifyBytes = async () => {
    if (empty) {
      expect(await readdir(root)).toEqual([]);
      return;
    }
    for (const [name, body] of Object.entries(
      liveModel ? fixtures : { 'estimate.txt': fixtures['estimate.txt'] }
    ))
      expect(await readFile(join(root, name), 'utf8')).toBe(body);
  };
  await verifyBytes();
  if (legacy) {
    // Only this proven disposable install: restore the missing historical
    // witness, not its saved avatar, folder, grants or team.
    execFileSync('sqlite3', [
      join(data, 'sessions.db'),
      `UPDATE workspace_creation_operations SET documents_witness=NULL WHERE workspace_id='${id}' AND state='complete'`
    ]);
  }
  await review.getByRole('button', { name: 'Open workspace', exact: true }).click();
  const card = page.locator('#workspaceDocumentsLook');
  if (legacy) {
    await expect(card.getByRole('heading')).toHaveText('Verify the Documents folder');
    await card.getByRole('button', { name: 'Verify Documents folder' }).click();
    expect(starts).toEqual([]);
  }
  await expect(
    card.getByRole('heading', { name: 'Want help organizing Documents?' })
  ).toBeVisible();
  await expect(card).toContainText('Documents Manager');
  await expect(card.locator('img[src*="/research-archivist/"]')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await card.screenshot({ path: info.outputPath('first-open.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'dark' });
  await page.evaluate(() => {
    localStorage.setItem('ori-theme', 'dark');
    document.documentElement.setAttribute('data-bs-theme', 'dark');
  });
  await card.screenshot({ path: info.outputPath('narrow-invite.png') });
  await card.getByRole('button', { name: 'Not now' }).press('Enter');
  const manual = card.getByRole('button', { name: 'Show Documents first look' });
  await expect(manual).toBeFocused();
  await page.reload();
  await expect(manual).toBeVisible();
  await manual.press('Enter');
  const action = card.getByRole('button', { name: 'Look around Documents' });
  await expect(action).toBeFocused();
  expect(starts).toEqual([]);
  expect(effects).toEqual([]);
  await verifyBytes();
  await action.press('Enter');
  const report = card.getByRole('button', { name: 'View report' });
  await expect(report).toBeVisible({ timeout: 60000 });
  expect(starts).toHaveLength(1);
  await page.reload();
  await expect(report).toBeVisible();
  await card.screenshot({ path: info.outputPath('narrow-return.png') });
  await page.setViewportSize({ width: 1280, height: 900 });
  await card.screenshot({ path: info.outputPath('desktop-return.png') });
  const records = async () =>
    (await (await request.get('/api/personal-assistant/folder-agent')).json()).folder_agents.filter(
      (r: { workspace_id: string }) => r.workspace_id === id
    );
  const observed = await records();
  expect(observed).toHaveLength(1);
  const ticket = observed[0].ticket_id;
  expect(ticket).toBeTruthy();
  await report.click();
  const savedReport = page.locator('.folder-agent-card--report:visible');
  await expect(savedReport).toBeVisible();
  const suggestionButton = savedReport.getByRole('button', { name: 'Review filing suggestions' });
  if (empty) {
    await expect(suggestionButton).toHaveCount(0);
    await expect(savedReport).toContainText(
      'This look found no loose documents supported for filing review.'
    );
    await savedReport.screenshot({ path: info.outputPath('empty-report.png') });
    await verifyBytes();
    expect(effects).toEqual([]);
    return;
  }
  // New looks with a measured pile use one station build. A saved review is
  // still reachable after a grant narrows (covered below for legacy reports).
  if (!liveModel) {
    const offer = savedReport.getByRole('region', { name: 'Filing station offer' });
    await expect(offer).toContainText('1 loose file in Documents');
    await expect(offer).toContainText('estimate.txt');
    await offer.getByRole('button', { name: 'Not now' }).click();
    await expect(offer.getByRole('button', { name: 'Build a Filing station' })).toHaveCount(0);
    expect(await readdir(root)).toEqual(['estimate.txt']);
    expect(
      (await (await request.get(`/api/workspaces/${id}/file-janitor`)).json()).status.applies
    ).toBe(false);
    await offer.getByRole('button', { name: 'Show Filing station offer' }).click();
    const mapURL = new URL(page.url());
    mapURL.searchParams.set('mode', 'map');
    await page.goto(mapURL.toString());
    const closeReport = page.getByRole('button', { name: 'Close personal assistant' });
    if (await closeReport.isVisible()) await closeReport.click();
    await expect(page.locator('.filing-station-plot')).toHaveText('1');
    const pileLine = page
      .locator('.commander-balloon-line:visible')
      .filter({ hasText: '1 loose file in Documents' });
    if (!(await page.locator('.commander-balloon:visible .filing-station-offer').count()))
      await pileLine.click();
    const bubble = page.locator('.commander-balloon:visible .filing-station-offer');
    await expect(bubble).toContainText('estimate.txt');
    await page.screenshot({ path: info.outputPath('map-offer.png') });
    await bubble.getByRole('button', { name: 'Build a Filing station' }).click();
    const station = page.getByRole('dialog', { name: 'Filing', exact: true });
    await expect(
      station.getByRole('heading', { name: 'Build a Filing station for Documents' })
    ).toBeVisible();
    await expect(
      station.getByRole('checkbox', { name: 'Also watch for new files and look once a day' })
    ).not.toBeChecked();
    await station.screenshot({ path: info.outputPath('build-card.png') });
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    const building = page.locator('[data-cmd-hq-station="file-janitor"]:visible');
    const builtParts: string[] = [];
    await page.exposeFunction('observeBuildPart', (part: string) => {
      if (!builtParts.includes(part)) builtParts.push(part);
    });
    await page.evaluate(() => {
      new MutationObserver(() => {
        const part = document
          .querySelector('[data-cmd-hq-station="file-janitor"]')
          ?.getAttribute('data-station-part');
        if (part) void (window as any).observeBuildPart(part);
      }).observe(document.body, {
        subtree: true,
        childList: true,
        attributes: true,
        attributeFilter: ['data-station-part']
      });
    });
    const prepared = page.waitForResponse(
      r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/prepare-review')
    );
    await station.getByRole('button', { name: 'Build the station', exact: true }).click();
    const response = await prepared;
    expect(response.status(), await response.text()).toBe(200);
    const result = (await response.json()).preparation;
    expect(result.result.outcome).toBe('batch');
    expect(result.result.eligible_count).toBe(1);
    expect(await readdir(root)).toEqual(['estimate.txt']);
    await verifyBytes();
    await expect.poll(() => builtParts).toEqual(['foundation', 'walls', 'roof']);
    await expect(building).toContainText('Review 1 file');
    if (await closeReport.isVisible()) await closeReport.click();
    const collapse = page.locator('.commander-balloon:visible .commander-balloon__close');
    if (await collapse.isVisible()) await collapse.click();
    await building.screenshot({ path: info.outputPath('depot-open.png') });
    await building.click();
    await expect(station.getByRole('tab', { name: 'History' })).toBeVisible();
    await page.screenshot({ path: info.outputPath('station-built.png') });
    await page.reload();
    await expect(building).toContainText('Review 1 file');
    await expect(building).not.toHaveAttribute('data-station-part', /foundation|walls|roof/);
    return;
  }
  await expect(suggestionButton).toBeVisible();
  await savedReport.screenshot({ path: info.outputPath('filing-recommendation.png') });
  await suggestionButton.click();
  const scope = savedReport.locator('.folder-agent-card__organization');
  await expect(scope).toContainText('Nothing has been read or sent.');
  await expect(scope.getByRole('checkbox', { name: 'estimate.txt' })).toBeChecked();
  await scope.screenshot({ path: info.outputPath('filing-read-boundary.png') });
  expect(starts).toHaveLength(1);
  expect(effects).toEqual([]);
  const after = await choiceInventory(request, data);
  expect(after.hires).toEqual(before.hires);
  expect(after.sources).toEqual(before.sources);
  await verifyBytes();
  if (liveModel) {
    await expect(scope).toContainText(`Anthropic (api.anthropic.com) using ${liveModel}`);
    await scope.getByRole('button', { name: 'Approve this read' }).click();
    await expect(scope).toContainText('3 readable · 0 skipped', { timeout: 90000 });
    await scope.getByRole('button', { name: 'Keep as a view' }).first().click();
    await expect(scope).toContainText('Saved view · no files moved');
    await scope.getByText('Review filing', { exact: true }).first().click();
    const boxes = scope
      .getByRole('group', { name: 'Choose loose documents (up to eight)' })
      .getByRole('checkbox');
    expect(await boxes.count()).toBeGreaterThanOrEqual(2);
    await boxes.first().check();
    const check = scope.getByRole('button', { name: 'Check selected files' });
    await check.click();
    await expect(scope.getByRole('link', { name: 'Open workspace in a new tab' })).toBeVisible();
    // Simulate the owner's three separate Filing actions via the production
    // APIs. The report itself may not install, grant the folder or scan it.
    const post = async (path: string, body: unknown) => {
      const r = await request.post(`/api/workspaces/${id}/${path}`, { data: body });
      expect(r.ok(), `${path}: ${r.status()}`).toBe(true);
    };
    await post('capabilities/file-janitor/install', {});
    await check.click();
    await expect(scope.getByRole('link', { name: 'Open Filing in a new tab' })).toBeVisible();
    await post('file-janitor/setup', { path: root, paused: true });
    // Filing deliberately narrowed the original content grant. The saved
    // review can proceed to Filing, but cannot preview/approve another read.
    const org = `/api/personal-assistant/folder-agent/${id}/organization-`;
    expect((await request.get(org + 'preview')).status()).toBe(409);
    expect((await request.get(org + 'reviews')).ok()).toBe(true);
    await check.click();
    await expect(scope).toContainText('Run a manual scan of Documents');
    await post('file-janitor/scan', {});
    await check.click();
    await scope.getByLabel('Folder in Documents').fill('Project receipts');
    await scope.getByRole('button', { name: 'Preview filing' }).click();
    await expect(scope).toContainText('No file is replaced.');
    await verifyBytes();
    expect(await readdir(join(root, 'Project receipts'))).toEqual([]);
    await scope.screenshot({ path: info.outputPath('move-preview.png') });
    await scope.getByRole('button', { name: 'File 1 document into Project receipts' }).click();
    await expect(scope).toContainText('Filed 1 into Project receipts.');
    const moved = await readdir(join(root, 'Project receipts'));
    expect(moved).toHaveLength(1);
    const file = moved[0] as keyof typeof fixtures;
    expect(await readFile(join(root, 'Project receipts', file), 'utf8')).toBe(fixtures[file]);
    expect(await readdir(root)).not.toContain(file);
    await scope.screenshot({ path: info.outputPath('filing-receipt.png') });
    await scope.getByRole('button', { name: 'Put back', exact: true }).click();
    await expect.poll(() => readdir(join(root, 'Project receipts'))).toEqual([]);
    await verifyBytes();
    await expect(scope).toContainText('Put back 1 file.');
    await scope.screenshot({ path: info.outputPath('filing-undo.png') });
    await page.reload();
    await report.click();
    await savedReport.getByRole('button', { name: 'Open saved filing reviews' }).click();
    await expect(scope).toContainText('New reads are unavailable.');
    await expect(scope.getByRole('button', { name: 'Open original' })).toHaveCount(0);
  } else {
    await expect(scope).toContainText('No supported processor');
    await expect(scope.getByRole('button', { name: 'Approve this read' })).toHaveCount(0);
  }
  expect((await records())[0].ticket_id).toBe(ticket);
  await rename(root, join(home, 'Original Documents'));
  await mkdir(root, { mode: 0o750 });
  await page.reload();
  await expect(card).toBeHidden();
  const stale = await request.get(`/api/workspaces/${id}/folder-manager-draft`);
  expect(stale.status()).toBe(204);
});
