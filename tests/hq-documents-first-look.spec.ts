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
  // The station acceptance starts with four real shelves and thirteen loose
  // files. Eight have unambiguous names; the others need a person's choice.
  const stationFiles: Record<string, string> = { 'estimate.txt': fixtures['estimate.txt'] };
  const stationFolders = ['Orders', 'Photos', 'Trips', 'Work'];
  if (!empty && !liveModel) {
    for (const folder of stationFolders) await mkdir(join(root, folder), { mode: 0o750 });
    const old = new Date(Date.now() - 3600_000);
    for (let i = 1; i <= 12; i++) {
      const name = i <= 8 ? `order-${i}.txt` : `note-${i - 8}.txt`;
      stationFiles[name] = `Synthetic ${name}`;
      await writeFile(join(root, name), stationFiles[name], { mode: 0o600 });
      await utimes(join(root, name), old, old);
    }
  }
  const stationTree = [...Object.keys(stationFiles), ...stationFolders].sort();
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
    for (const [name, body] of Object.entries(liveModel ? fixtures : stationFiles))
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
  await page.locator('#toastContainer').getByRole('button', { name: 'Open workspace' }).click();
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
  const theme = process.env.ORI_STATION_THEME === 'light' ? 'light' : 'dark';
  await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: theme });
  await page.evaluate(value => {
    localStorage.setItem('ori-theme', value);
    document.documentElement.setAttribute('data-bs-theme', value);
  }, theme);
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
  const historical = process.env.ORI_STATION_SAVED_REVIEW === '1';
  if (historical)
    execFileSync('python3', ['tests/helpers/seed-saved-documents-review.py', base, id], {
      input: JSON.stringify(folder)
    });
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
    await expect(offer).toContainText('13 loose files in Documents');
    await expect(offer).toContainText('and 7 more');
    await offer.getByRole('button', { name: 'Not now' }).click();
    await expect(offer.getByRole('button', { name: 'Build a Filing station' })).toHaveCount(0);
    expect((await readdir(root)).sort()).toEqual(stationTree);
    expect(
      (await (await request.get(`/api/workspaces/${id}/file-janitor`)).json()).status.applies
    ).toBe(false);
    await offer.getByRole('button', { name: 'Show Filing station offer' }).click();
    const mapURL = new URL(page.url());
    mapURL.searchParams.set('mode', 'map');
    await page.goto(mapURL.toString());
    const closeReport = page.getByRole('button', { name: 'Close personal assistant' });
    if (await closeReport.isVisible()) await closeReport.click();
    await expect(page.locator('.filing-station-plot')).toHaveText('13');
    const pileLine = page
      .locator('.commander-balloon-line:visible')
      .filter({ hasText: '13 loose files in Documents' });
    if (!(await page.locator('.commander-balloon:visible .filing-station-offer').count()))
      await pileLine.click();
    const bubble = page.locator('.commander-balloon:visible .filing-station-offer');
    await expect(bubble).toContainText('and 7 more');
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
    const reduced = historical || process.env.ORI_STATION_REDUCED === '1';
    await page.emulateMedia({ reducedMotion: reduced ? 'reduce' : 'no-preference' });
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
    expect(result.result.eligible_count).toBe(13);
    expect((await readdir(root)).sort()).toEqual(stationTree);
    await verifyBytes();
    if (!reduced) await expect.poll(() => builtParts).toEqual(['foundation', 'walls', 'roof']);
    await expect(building).toContainText('Review 13 files');
    if (await closeReport.isVisible()) await closeReport.click();
    const collapse = page.locator('.commander-balloon:visible .commander-balloon__close');
    if (await collapse.isVisible()) await collapse.click();
    await building.screenshot({ path: info.outputPath('depot-open.png') });
    await building.click();
    await expect(station.getByRole('tab', { name: 'History' })).toBeVisible();
    await page.screenshot({ path: info.outputPath('station-built.png') });
    await page.reload();
    await expect(building).toContainText('Review 13 files');
    await expect(building).not.toHaveAttribute('data-station-part', /foundation|walls|roof/);
    // A shelf is an existing folder the person chooses, not one created by
    // setup or by selecting a chip. Exercise the admitted File and Undo too.
    expect((await readdir(root)).sort()).toEqual(stationTree);
    if (!(await station.isVisible())) await building.press('Enter');
    const method = station.getByRole('region', { name: 'Filing method' });
    await expect(method).toContainText('How should Documents be filed?');
    await expect(method).toContainText('Suggested · This folder already has shelves.');
    await expect(method.getByRole('button', { name: 'Send to another workspace' })).toBeDisabled();
    await method.screenshot({ path: info.outputPath('method-chooser.png') });
    await method.getByRole('button', { name: 'Into my folders', exact: true }).press('Enter');
    const shelves = station.locator('.fj-shelves');
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
    await expect(shelves).toContainText('8 ready to file · 5 stay loose');
    await page.reload();
    if (!(await station.isVisible())) await building.press('Enter');
    await expect(shelves).toContainText('8 ready to file · 5 stay loose');
    await expect(method).toHaveCount(0);
    const readCard = shelves.getByRole('region', { name: 'Optional first-lines read' });
    const ask = readCard.getByRole('button', { name: /Read the first lines of the 5 loose files/ });
    await ask.press('Enter');
    await expect(readCard).toContainText('No supported model');
    await expect(readCard.getByRole('button', { name: 'Approve this read' })).toHaveCount(0);
    await readCard.getByRole('button', { name: 'Back to picture' }).press('Enter');
    await expect(ask).toBeFocused();
    await verifyBytes();
    if (historical) {
      await station.getByRole('tab', { name: 'History', exact: true }).click();
      const old = station.locator('[data-saved-document-reviews]');
      await old.getByRole('button', { name: 'Earlier Documents reviews' }).press('Enter');
      await expect(old).toContainText('Earlier trip notes');
      await expect(old.getByRole('button', { name: 'Open original' })).toHaveCount(0);
      await old.getByText('Review filing', { exact: true }).click();
      await old.getByRole('checkbox', { name: 'note-4.txt', exact: true }).check();
      await old.getByRole('button', { name: 'Check selected files' }).click();
      await old.getByLabel('Folder in Documents').fill('Trips');
      await old.getByRole('button', { name: 'Preview filing' }).click();
      await verifyBytes();
      await old.getByRole('button', { name: 'File 1 document into Trips' }).click();
      await expect(old).toContainText('Filed 1 into Trips.');
      expect(await readFile(join(root, 'Trips', 'note-4.txt'), 'utf8')).toBe(
        stationFiles['note-4.txt']
      );
      await station.screenshot({ path: info.outputPath('history-earlier-review.png') });
      await old.getByRole('button', { name: 'Put back', exact: true }).click();
      await expect.poll(() => readdir(join(root, 'Trips'))).toEqual([]);
      await verifyBytes();
      return;
    }
    // A controlled processor card exercises disclosure and decline without a
    // live account. Go tests separately run the actual one-use/version checks.
    const pictureUrl = `/api/workspaces/${id}/file-janitor/batches/${result.result.batch_id}/picture`;
    const picture = (await (await request.get(pictureUrl + '?layout=shelves')).json()).picture;
    const estimate = picture.files.find((f: { name: string }) => f.name === 'estimate.txt');
    const trips = picture.folders.find((f: { name: string }) => f.name === 'Trips');
    let approvals = 0;
    await page.route('**/picture/read-review', r =>
      r.fulfill({
        json: {
          review: {
            files: ['estimate.txt'],
            folders: ['Trips'],
            remaining: 4,
            processor: { provider: 'Example cloud', model: 'Pinned model', off_device: true },
            approval: 'test-card'
          }
        }
      })
    );
    await page.route('**/picture/read-lines', r => {
      approvals++;
      return r.fulfill({
        json: { result: { read: 1, remaining: 4, placements: { [estimate.id]: trips.key } } }
      });
    });
    await ask.press('Enter');
    await expect(readCard).toContainText('Example cloud · Pinned model');
    await expect(readCard).toContainText('leave this device');
    await expect(readCard).toContainText('4 loose files are outside this read');
    await readCard.screenshot({ path: info.outputPath('first-lines-consent.png') });
    await readCard.getByRole('button', { name: 'Not now' }).press('Enter');
    expect(approvals).toBe(0);
    await ask.press('Enter');
    await readCard.getByRole('button', { name: 'Approve this read' }).press('Enter');
    await expect(shelves).toContainText(
      '1 read · 4 not read · 1 placed in the picture. Nothing moved.'
    );
    await verifyBytes();
    expect(approvals).toBe(1);
    await shelves.getByRole('button', { name: 'estimate.txt', exact: true }).press('Enter');
    await shelves.getByRole('button', { name: 'Leave loose', exact: true }).press('Enter');
    await expect(shelves).toContainText('8 ready to file · 5 stay loose');
    await shelves.getByRole('textbox', { name: /Existing folder name/ }).fill('Trips');
    await shelves.getByRole('button', { name: 'Add named shelf' }).press('Enter');
    await expect(shelves).toContainText('Trips is in the picture.');
    await shelves.getByRole('button', { name: 'estimate.txt', exact: true }).press('Enter');
    await shelves.getByRole('button', { name: 'Move here: Trips', exact: true }).press('Enter');
    await expect(shelves).toContainText('9 ready to file · 4 stay loose');
    await shelves.getByRole('button', { name: 'Filing into my folders · Change' }).press('Enter');
    await expect(method).toContainText('Changing the method replaces your unfiled picture.');
    await method.getByRole('button', { name: 'Keep current picture' }).press('Enter');
    await expect(shelves).toContainText('9 ready to file · 4 stay loose');
    await verifyBytes();
    await page.screenshot({ path: info.outputPath('own-shelves.png') });
    const loose = shelves.getByRole('region', { name: 'Still loose in Documents' });
    const tripShelf = shelves.getByRole('region', { name: 'Trips', exact: true });
    const dragged = shelves.getByRole('button', { name: 'note-2.txt', exact: true });
    await dragged.dragTo(tripShelf.getByRole('heading', { name: 'Trips' }));
    await expect(tripShelf.getByRole('button', { name: 'note-2.txt' })).toBeVisible();
    await dragged.dragTo(loose.getByRole('heading'));
    await expect(loose.getByRole('button', { name: 'note-2.txt' })).toBeVisible();
    // A coarse pointer has no drag dependency. At a narrow viewport and
    // 200% scale both the loose chip and its separate Move here work by tap.
    const viewport = page.viewportSize()!;
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    await page.setViewportSize({ width: 480, height: 800 });
    await page.evaluate(() => {
      document.documentElement.style.zoom = '2';
    });
    const tap = async (loc: typeof dragged) => {
      await loc.evaluate(el =>
        el.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' })
      );
      const b = await loc.boundingBox();
      expect(b).toBeTruthy();
      const point = { x: b!.x + b!.width / 2, y: b!.y + b!.height / 2 };
      expect(
        await loc.evaluate((el, p) => el.contains(document.elementFromPoint(p.x, p.y)), point)
      ).toBe(true);
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [point] });
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    };
    await tap(dragged);
    const moveHere = tripShelf.getByRole('button', { name: 'Move here: Trips' });
    await expect(moveHere).toBeEnabled();
    expect(await shelves.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await tap(moveHere);
    await expect(tripShelf.getByRole('button', { name: 'note-2.txt' })).toBeVisible();
    await verifyBytes();
    await page.screenshot({ path: info.outputPath('shelf-touch-zoom.png') });
    await page.evaluate(() => {
      document.documentElement.style.zoom = '';
    });
    await page.setViewportSize(viewport);
    await cdp.send('Emulation.setTouchEmulationEnabled', { enabled: false });
    await cdp.detach();
    // Redraw with the current pointer capabilities, then use the new shelf
    // drop target. Checking its name still creates nothing on disk.
    await dragged.press('Enter');
    await dragged.press('Enter');
    await shelves
      .getByRole('button', { name: 'note-1.txt', exact: true })
      .dragTo(shelves.getByRole('heading', { name: 'New folder', exact: true }));
    await shelves.getByRole('textbox', { name: 'New folder name' }).fill('Orders');
    await shelves.getByRole('button', { name: 'Use this folder name' }).press('Enter');
    await expect(shelves).toContainText('That name is not available here');
    await expect(
      shelves.getByRole('button', { name: 'File 10 files', exact: true })
    ).toBeDisabled();
    await shelves.getByRole('textbox', { name: 'New folder name' }).fill('Invoices');
    await shelves.getByRole('button', { name: 'Use this folder name' }).press('Enter');
    await expect(shelves).toContainText('No folder has been created');
    expect((await readdir(root)).sort()).toEqual(stationTree);
    await verifyBytes();
    await expect(shelves).toContainText('11 ready to file · 2 stay loose');
    await shelves.getByRole('button', { name: 'File 11 files', exact: true }).press('Enter');
    const undo = station.getByRole('button', { name: 'Undo all', exact: true });
    await expect(undo).toBeVisible();
    await expect(station.locator('#fileJanitorReceipt')).toContainText('Created “Invoices”');
    await expect(station.locator('#fileJanitorReceipt')).toContainText('11 files filed.');
    await expect(station.locator('#fileJanitorReceipt')).toContainText(
      '2 files stay loose in Documents.'
    );
    expect(await readFile(join(root, 'Invoices', 'note-1.txt'), 'utf8')).toBe(
      stationFiles['note-1.txt']
    );
    expect(await readFile(join(root, 'Trips', 'estimate.txt'), 'utf8')).toBe(
      stationFiles['estimate.txt']
    );
    expect((await readdir(join(root, 'Orders'))).sort()).toEqual(
      Object.keys(stationFiles)
        .filter(n => n.startsWith('order-'))
        .sort()
    );
    expect((await readdir(root)).sort()).toEqual(
      [...stationFolders, 'Invoices', 'note-3.txt', 'note-4.txt'].sort()
    );
    const rulesUrl = `/api/workspaces/${id}/file-janitor/placements`;
    const rules = async () => (await (await request.get(rulesUrl)).json()).placements.rows;
    const remember = station.getByRole('group', { name: 'Start from these placements next time?' });
    await expect(remember.getByRole('checkbox', { name: /Files named "order"/ })).toBeChecked();
    await expect(remember.getByRole('checkbox', { checked: true })).toHaveCount(1);
    expect(await rules()).toEqual([]);
    await station.screenshot({ path: info.outputPath('shelf-receipt.png') });
    await station.locator('#fileJanitorReceiptDone').press('Enter');
    await expect.poll(rules).toHaveLength(1);
    const additions = ['order-20.txt', 'order-21.txt', 'order-22.txt', 'postcard.txt'];
    const oldTime = new Date(Date.now() - 3600_000);
    for (const name of additions) {
      await writeFile(join(root, name), `Added ${name}`, { mode: 0o600 });
      await utimes(join(root, name), oldTime, oldTime);
    }
    await station.getByRole('button', { name: 'Scan now', exact: true }).click();
    await station.getByRole('button', { name: 'Show the newer scan' }).press('Enter');
    await expect(shelves.getByText('From last time', { exact: true })).toHaveCount(3);
    await expect(
      shelves
        .getByRole('region', { name: 'Orders', exact: true })
        .getByRole('button', { name: 'order-22.txt' })
    ).toBeVisible();
    for (const name of additions)
      expect(await readFile(join(root, name), 'utf8')).toBe(`Added ${name}`);
    await station.getByRole('button', { name: 'Back to map' }).click();
    await expect(building.locator('.fj-station-stack')).toHaveText('4');
    await expect(
      page
        .locator('.commander-balloon-line:visible')
        .filter({ hasText: '3 already have a place from last time' })
    ).toBeVisible();
    await page.screenshot({ path: info.outputPath('remembered-pile.png') });
    await building.press('Enter');
    await station.getByRole('tab', { name: 'History', exact: true }).click();
    await station.locator('#fileJanitorLastFilingUndo').press('Enter');
    await expect(station.locator('#fileJanitorUndone')).toContainText(
      'Undo did not remove “Invoices”'
    );
    await expect.poll(() => readdir(join(root, 'Invoices'))).toEqual([]);
    await page.screenshot({ path: info.outputPath('undo-retained-folder.png') });
    await verifyBytes();
    expect((await readdir(root)).sort()).toEqual([...stationTree, ...additions, 'Invoices'].sort());
    await rename(join(root, 'Orders'), join(root, 'Orders moved'));
    await station.getByRole('tab', { name: 'Settings', exact: true }).click();
    const saved = station.getByRole('region', { name: 'Saved placements' });
    await expect(saved).toContainText('Broken: the original folder or placement is unavailable.');
    await saved.getByRole('button', { name: /Remove Files named/ }).press('Enter');
    await expect.poll(rules).toHaveLength(0);
    await rename(join(root, 'Orders moved'), join(root, 'Orders'));
    await station.locator('#fileJanitorChangeMethod').press('Enter');
    await method.getByRole('button', { name: 'By kind', exact: true }).press('Enter');
    await expect(station).toContainText('Filing by kind');
    expect((await readdir(root)).sort()).toEqual([...stationTree, ...additions, 'Invoices'].sort());
    return;
  }
  // A fresh counted look now offers a station, not the retired one-click
  // report entry. Exercise its real, separately approved direct processor.
  const liveOffer = savedReport.getByRole('button', { name: 'Build a Filing station' });
  if (liveModel && (await liveOffer.count())) {
    await liveOffer.click();
    const station = page.getByRole('dialog', { name: 'Filing', exact: true });
    await station.getByRole('button', { name: 'Build the station', exact: true }).click();
    const choose = station.getByRole('button', { name: 'Into my folders', exact: true });
    if (!(await choose.isVisible())) {
      await expect
        .poll(
          async () =>
            (await (await request.get(`/api/workspaces/${id}/file-janitor`)).json()).status.applies
        )
        .toBe(true);
      if (!(await station.isVisible())) await page.locator('#fileJanitorCardOpen').click();
    }
    await choose.press('Enter');
    const shelves = station.locator('.fj-shelves');
    const ask = shelves.getByRole('button', { name: /Read the first lines of the 3 loose files/ });
    await ask.press('Enter');
    const seal = shelves.getByRole('region', { name: 'Optional first-lines read' });
    await expect(seal).toContainText('Anthropic (api.anthropic.com)');
    await expect(seal).toContainText(liveModel);
    await expect(seal).toContainText('leave this device');
    await verifyBytes();
    await seal.getByRole('button', { name: 'Not now' }).click();
    await ask.press('Enter');
    const done = page.waitForResponse(
      r => new URL(r.url()).pathname.endsWith('/picture/read-lines'),
      { timeout: 100_000 }
    );
    await seal.getByRole('button', { name: 'Approve this read' }).press('Enter');
    const r = await done;
    expect(r.status(), (await r.json()).code || 'approved station read').toBe(200);
    await expect(shelves).toContainText('3 read · 0 not read');
    await verifyBytes();
    expect(await readdir(join(root, 'Project receipts'))).toEqual([]);
    await station.screenshot({ path: info.outputPath('approved-station-read.png') });
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
