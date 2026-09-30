import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// Run only in a disposable HOME, with NO local provider preinstalled. The UI
// must preview and install the published release using Ori's reviewed registry.
// The exported --source candidate is validated but never installed here.
const sandbox = process.env.ORI_MUSIC_HOME_SANDBOX || '';
const evidenceDir = process.env.ORI_MUSIC_HOME_EVIDENCE_DIR || '';
test.skip(
  process.env.ORI_MUSIC_HOME_ACCEPTANCE !== '1' ||
    process.env.ORI_MUSIC_PROVIDER_MODE !== 'reviewed' ||
    !sandbox,
  'requires music-home-demo.sh test --suite portfolio-reviewed --provider reviewed'
);
test.setTimeout(180_000); // Released-source resolution and optional process restart require time.

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function shot(page: Page, name: string) {
  mkdirSync(evidenceDir, { recursive: true });
  await page.screenshot({ path: join(evidenceDir, `${name}.png`) });
}

test('the reviewed release resolves a real portfolio offer, then separate reviews grant and scan its folder', async ({
  page,
  request,
  browser
}) => {
  const documents = join(sandbox, 'Documents');
  const songs: string[] = [];
  for (let i = 0; i < 5; i++) {
    const folder = join(documents, `Album-${i}`);
    mkdirSync(folder, { recursive: true, mode: 0o750 });
    const song = join(folder, 'Song.rpp');
    writeFileSync(song, `fixture-${i}; metadata-only`, { mode: 0o600 });
    songs.push(song);
  }
  const before = songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex'));
  const initialPlugins = await json(await request.get('/api/plugins'));
  expect(
    (initialPlugins.plugins || []).some(
      (row: { name: string }) => row.name === 'music-project-management'
    )
  ).toBe(false);
  await json(await request.post('/api/onboarding/skip'));
  const relationship = (await json(await request.get('/api/personal-assistant')))
    .personal_assistant;
  await json(
    await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'reviewed-portfolio-hire',
        if_version: relationship.state_version ?? 0,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    })
  );
  const hired = (await json(await request.get('/api/personal-assistant'))).personal_assistant;
  await json(
    await request.post('/api/personal-assistant/hq', {
      data: { request_id: 'reviewed-portfolio-hq', if_version: hired.state_version, name: 'My HQ' }
    })
  );
  const scanned = await json(
    await request.post('/api/personal-assistant/folder-digest/scan', {
      data: { chip: 'documents' }
    })
  );
  expect(scanned.offer).toMatchObject({
    status: 'pending',
    portfolio: { projects: 5 },
    capability: { setup_source: 'portfolio' }
  });
  const offerID = scanned.offer.id;
  await page.goto('/?panel=today&folder=show');
  const offerCard = page.locator('#personalAssistantFolderOffer');
  await expect(offerCard).toContainText('5 music projects');
  await shot(page, '16-reviewed-release-offer');
  const disclosures: string[] = [];
  page.on('dialog', async dialog => {
    disclosures.push(dialog.message());
    await dialog.accept(); // Only inside the disposable HOME; the test asserts consequences below.
  });
  await offerCard.locator('[data-folder-action="setup"]').click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible({ timeout: 90_000 });
  expect(disclosures.join('\n')).toContain(
    'Reviewed source: https://github.com/johnjallday/music-project-management#sha='
  );
  const plugins = await json(await request.get('/api/plugins'));
  const installed = (plugins.plugins || []).find(
    (row: { name: string }) => row.name === 'music-project-management'
  );
  expect(installed).toMatchObject({ name: 'music-project-management', enabled: true });
  expect(installed.source).toMatch(
    /^https:\/\/github\.com\/johnjallday\/music-project-management#sha=[0-9a-f]{40}$/
  );
  expect(installed.source.startsWith(sandbox)).toBe(false);
  expect(
    (plugins.plugins || []).some((row: { name: string }) => row.name === 'reaper-plugin')
  ).toBe(false);
  await shot(page, '17-reviewed-release-home-creator');
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await expect(creator.locator('.ws-role-row[data-role-id="portfolio_manager"]')).toContainText(
    'Music Portfolio Manager'
  );
  await creator.getByRole('button', { name: 'Review →' }).click();
  await shot(page, '18-reviewed-release-home-review');
  await creator.locator('#createFolderBtn').click();
  await page.waitForURL(/\/assistant\?folder_offer_id=/, { timeout: 30_000 });
  const homes = (await json(await request.get('/api/workspaces'))).folders.filter(
    (row: { kind: string }) => row.kind === 'group'
  );
  expect(homes).toHaveLength(1);
  const homeID = homes[0].id;
  // The digest projects active offers only; a resolved offer disappears from
  // its current card. The canonical resolution is proved by the exact offer
  // navigation and the server-held root selection below.
  expect(page.url()).toContain(`folder_offer_id=${encodeURIComponent(offerID)}`);
  const base = `/api/workspaces/${homeID}/assistant-program/library`;
  const shelf = page.locator('#projectLibraryPanel');
  await expect(shelf).toBeVisible();
  await expect(shelf.locator('#projectLibraryStatus')).toContainText('No library yet');
  // A fresh Home lands on its unfinished setup, not on the progression header:
  // the card names the carried collection, is in the first viewport, holds focus
  // (arrival after async render, even before the library exists), and compacts
  // the hero while it is shown.
  const setupCard = page.locator('#projectSetupNext');
  await expect(setupCard).toBeVisible();
  await expect(setupCard.locator('#projectSetupNextTitle')).toContainText('Documents');
  await expect(setupCard.locator('#projectSetupNextTitle')).toBeFocused();
  await expect(setupCard).toBeInViewport();
  await expect(setupCard.locator('#projectSetupNextAction')).toHaveText(
    'Start library and review Documents'
  );
  await expect(page.locator('#assistantProgramPage')).toHaveClass(/is-setup-first/);
  await shot(page, '19a-fresh-home-setup-first');
  expect((await json(await request.get(`${base}/roots`))).initialized).toBe(false);
  await shot(page, '19-reviewed-home-before-library-consent');
  // The card's button announced that it starts the library for this collection,
  // so the exact folder's own review follows directly.
  await setupCard.locator('#projectSetupNextAction').click();
  const grant = page.getByRole('dialog', { name: 'Connect this discovery folder?' });
  await expect(grant).toContainText(documents);
  expect((await json(await request.get(`${base}/roots`))).total_roots).toBe(0);
  await shot(page, '20-reviewed-portfolio-root-grant');
  await grant.getByRole('button', { name: 'Connect folder' }).click();
  // The grant goes straight to the scan review; no separate "scan now?" stop.
  const scan = page.getByRole('dialog', { name: 'Scan this discovery folder once?' });
  await expect(scan).toBeVisible();
  expect((await json(await request.get(`${base}/roots`))).total_roots).toBe(1);
  await expect(scan).toContainText(documents);
  await shot(page, '21-reviewed-portfolio-scan-review');
  await scan.getByRole('button', { name: 'Scan metadata' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
  const projects = await json(await request.get(`${base}/projects`));
  expect(projects.total).toBe(5);
  expect(projects.rows).toHaveLength(5);
  const approvedRoots = await json(await request.get(`${base}/roots`));
  expect(approvedRoots.total_roots).toBe(1);
  await shot(page, '22-reviewed-portfolio-populated-shelf');
  await expect(shelf.locator('#projectLibraryRoot option')).toHaveCount(2);
  await shelf.locator('#projectLibraryRoot').selectOption(approvedRoots.roots[0].id);
  await shelf.locator('#projectLibraryFormat').selectOption('reaper');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
  await shelf.locator('#projectLibraryFormat').selectOption('logic');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('0 of 0 projects');
  await shelf.locator('#projectLibraryRoot').selectOption('');
  await shelf.locator('#projectLibraryFormat').selectOption('');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
  const results = shelf.locator('#projectLibraryRows');
  await expect(results.locator('tr')).toHaveCount(5);
  await shelf.locator('#projectLibrarySearch').fill('Album-3');
  await shelf.locator('#projectLibraryConnection').selectOption('catalog_only');
  await shelf.locator('#projectLibraryAvailability').selectOption('available');
  await shelf.locator('#projectLibraryStage').selectOption('unknown');
  await shelf.locator('#projectLibrarySort').selectOption('scanned_at');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('1 of 1 projects');
  await expect(results.locator('tr')).toHaveCount(1);
  await expect(results.locator('tr')).toContainText('Album-3');
  await page.setViewportSize({ width: 390, height: 844 });
  await shelf.locator('#projectLibrarySearch').focus();
  await page.keyboard.press('Enter');
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('1 of 1 projects');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(
    await shelf
      .locator('#projectLibraryDirection')
      .evaluate(element => element.getBoundingClientRect().width)
  ).toBeGreaterThan(250);
  await shelf.locator('#projectLibrarySearchForm').scrollIntoViewIfNeeded();
  await shot(page, '22a1-reviewed-portfolio-mobile-filter-controls');
  await results.locator('tr').first().scrollIntoViewIfNeeded();
  await shot(page, '22a-reviewed-portfolio-filtered-mobile');
  await shelf.locator('#projectLibrarySearch').fill('');
  await shelf.locator('#projectLibraryConnection').selectOption('');
  await shelf.locator('#projectLibraryAvailability').selectOption('');
  await shelf.locator('#projectLibraryStage').selectOption('');
  await shelf.locator('#projectLibrarySort').selectOption('name');
  await shelf.locator('#projectLibrarySearch').focus();
  await page.keyboard.press('Enter');
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
  await page.setViewportSize({ width: 1280, height: 900 });
  await shelf
    .locator('#projectLibraryRows')
    .getByRole('button', { name: 'Review Album-3' })
    .click();
  const setup = page.getByRole('dialog', { name: 'Album-3' });
  await expect(setup.getByRole('heading', { name: 'Project setup' })).toBeVisible();
  await expect(setup).toContainText(
    'A compatible installed project integration with existing-project support is required'
  );
  await expect(setup.getByRole('button', { name: /Work on this project/i })).toHaveCount(0);
  await shot(page, '22b-reviewed-portfolio-inert-project-setup');

  // music-setup-onboarding, group 3. A song whose format a reviewed integration
  // supports is labelled plainly and, where this computer can complete it, can
  // review that integration and come back to the same song. Chip/fake chooser
  // evidence; the install itself is NOT confirmed here.
  await expect(setup).toContainText('Needs a project integration');
  const offerSupported = process.platform === 'darwin' && process.arch === 'arm64';
  const album3Entry = projects.rows.find((row: { name: string }) => row.name === 'Album-3')?.id;
  const album3Activation = await json(
    await request.get(`${base}/projects/${album3Entry}/activation`)
  );
  expect(album3Activation.observed_format).toBe('reaper');
  expect(album3Activation.integration_offer ?? null).toEqual(
    offerSupported
      ? { key: 'ori_reaper', quest_id: 'install_ori_reaper', display_name: 'REAPER' }
      : null
  );
  const reviewIntegration = setup.getByRole('button', { name: 'Review the REAPER integration' });
  await expect(reviewIntegration).toHaveCount(offerSupported ? 1 : 0);
  if (offerSupported) {
    const pluginsBefore = (await json(await request.get('/api/plugins'))).plugins.map(
      (row: { name: string; enabled: boolean }) => `${row.name}:${row.enabled}`
    );
    const workspacesBefore = (await json(await request.get('/api/workspaces'))).folders.length;
    const homePath = new URL(page.url()).pathname;
    await reviewIntegration.click();
    await page.waitForURL(/\/\?setup=quest&source=host&quest=install_ori_reaper/);
    await expect(page.locator('#specialistSetupJourneyModal')).toBeVisible({ timeout: 30_000 });
    await expect(page.locator('#specialistSetupJourneyModal')).toContainText('REAPER');
    await shot(page, '22b1-reviewed-integration-install-quest');
    // Nothing was confirmed: no install, no enable, no workspace.
    expect(
      (await json(await request.get('/api/plugins'))).plugins.map(
        (row: { name: string; enabled: boolean }) => `${row.name}:${row.enabled}`
      )
    ).toEqual(pluginsBefore);
    // Leave without confirming (or come back after finishing) and return.
    await page.goto(homePath);
    await expect(page.getByRole('dialog', { name: 'Album-3' })).toBeVisible({ timeout: 30_000 });
    await expect(setup).toContainText('Needs a project integration');
    await expect(shelf.locator('#projectLibraryStatus')).toContainText('Back on your song');
    await shot(page, '22b2-back-on-the-same-song');
    expect(await page.evaluate(() => sessionStorage.getItem('ori:library-return'))).toBeNull();
    expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspacesBefore
    );
    expect(
      (await json(await request.get('/api/plugins'))).plugins.map(
        (row: { name: string; enabled: boolean }) => `${row.name}:${row.enabled}`
      )
    ).toEqual(pluginsBefore);
  }
  await setup.getByRole('button', { name: 'Plan a session' }).click();
  const planner = page.getByRole('dialog', { name: 'Plan a studio session' });
  await planner.getByRole('textbox', { name: 'Session goal' }).fill('Listen to the rough mix');
  await planner.getByRole('textbox', { name: 'Desired outcome' }).fill('Choose one vocal take');
  await planner.getByRole('spinbutton', { name: /Available minutes/ }).fill('30');
  await planner.getByRole('button', { name: 'Review session' }).click();
  const goalReview = page.getByRole('dialog', { name: 'Save this goal?' });
  await expect(goalReview).toContainText(
    'No project connection, task, or DAW operation is created'
  );
  await shot(page, '22c-reviewed-portfolio-session-goal-review');
  await goalReview.getByRole('button', { name: 'Save goal' }).click();
  await expect(shelf.locator('#projectLibraryStatus')).toContainText('Goal saved');
  await shelf
    .locator('#projectLibraryRows')
    .getByRole('button', { name: 'Review Album-3' })
    .click();
  const sessionDetail = page.getByRole('dialog', { name: 'Album-3' });
  await expect(sessionDetail).toContainText('Listen to the rough mix');
  await sessionDetail.getByRole('button', { name: 'Wrap up session' }).click();
  const wrap = page.getByRole('dialog', { name: 'Wrap up studio session' });
  await wrap.getByRole('textbox', { name: 'Your recap' }).fill('Kept the quieter chorus');
  await wrap.getByRole('textbox', { name: /Decisions/ }).fill('Keep the bridge');
  await wrap.getByRole('textbox', { name: /Blockers/ }).fill('Need vocals');
  await wrap.getByRole('textbox', { name: 'Proposed next action' }).fill('Record a scratch vocal');
  await wrap.getByRole('checkbox', { name: /Also update this project/ }).check();
  await wrap.getByRole('button', { name: 'Review session' }).click();
  const recapReview = page.getByRole('dialog', { name: 'Save this recap?' });
  await expect(recapReview).toContainText('Also replaces the project’s saved next action');
  await recapReview.getByRole('button', { name: 'Save recap' }).click();
  await expect(shelf.locator('#projectLibraryStatus')).toContainText('Recap saved');
  await page.reload();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
  const resume = shelf.locator('#projectLibraryResume');
  await expect(resume).toContainText('Pick up where you left off');
  await expect(resume).toContainText('User-authored note');
  await expect(resume).toContainText('Current saved project next action: Record a scratch vocal');
  await resume.scrollIntoViewIfNeeded();
  await shot(page, '22d-reviewed-portfolio-home-resume-after-reload');
  await resume.getByRole('button', { name: 'View Album-3 session' }).click();
  const resumed = page.getByRole('dialog', { name: 'Album-3' });
  await expect(resumed).toContainText('User recap: Kept the quieter chorus');
  await expect(resumed).toContainText('Next action: Record a scratch vocal');
  await shot(page, '22d-reviewed-portfolio-resume-after-reload');
  await resumed.getByRole('button', { name: 'View full session' }).click();
  const fullSession = page.getByRole('dialog', { name: 'Studio session · Album-3' });
  await expect(fullSession).toContainText('Keep the bridge');
  await expect(fullSession).toContainText('Need vocals');
  await shot(page, '22e-reviewed-portfolio-saved-session-detail');
  await fullSession.getByRole('button', { name: 'Close' }).click();

  // Metadata labels are explicit user edits, never inferred from the scan or
  // recap. Verify populated results and the independent stage/status filters.
  await shelf
    .locator('#projectLibraryRows')
    .getByRole('button', { name: 'Review Album-3' })
    .click();
  await page
    .getByRole('dialog', { name: 'Album-3' })
    .getByRole('button', { name: 'Edit project notes' })
    .click();
  const editor = page.getByRole('dialog', { name: 'Edit Album-3' });
  await editor.getByRole('combobox', { name: 'Production stage' }).selectOption('mixing');
  await editor.getByRole('combobox', { name: 'Administrative status' }).selectOption('active');
  await editor.getByRole('combobox', { name: 'Priority' }).selectOption('4');
  await editor.getByRole('button', { name: 'Review changes' }).click();
  const editReview = page.getByRole('dialog', { name: 'Save these project notes?' });
  await expect(editReview).toContainText('stage: mixing');
  await expect(editReview).toContainText('status: active');
  await shot(page, '22g-reviewed-portfolio-explicit-stage-status-review');
  await editReview.getByRole('button', { name: 'Save notes' }).click();
  await shelf.locator('#projectLibraryStage').selectOption('mixing');
  await shelf.locator('#projectLibraryStatusFilter').selectOption('active');
  await shelf.locator('#projectLibraryAvailability').selectOption('available');
  await shelf.locator('#projectLibraryPriority').selectOption('4');
  await shelf.locator('#projectLibrarySort').selectOption('priority');
  await shelf.locator('#projectLibraryDirection').selectOption('desc');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('1 of 1 projects');
  await expect(shelf.locator('#projectLibraryRows')).toContainText('Album-3');
  await expect(shelf.locator('#projectLibraryRows').locator('td').nth(2)).toContainText('mixing');
  await expect(shelf.locator('#projectLibraryRows').locator('td').nth(2)).toContainText('active');
  await shelf.locator('#projectLibraryStatusFilter').selectOption('unknown');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('0 of 0 projects');
  await shelf.locator('#projectLibraryStatusFilter').selectOption('active');
  await shelf.locator('#projectLibraryPriority').selectOption('0');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('0 of 0 projects');
  await shelf.locator('#projectLibraryPriority').selectOption('4');
  await shelf.locator('#projectLibraryAvailability').selectOption('unavailable');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('0 of 0 projects');
  await shelf.locator('#projectLibraryStage').selectOption('');
  await shelf.locator('#projectLibraryStatusFilter').selectOption('');
  await shelf.locator('#projectLibraryPriority').selectOption('');
  await shelf.locator('#projectLibraryAvailability').selectOption('');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
  await expect(shelf.locator('#projectLibraryRows tr').first()).toContainText('Album-3');
  await shelf.locator('#projectLibrarySort').selectOption('name');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryRows tr').first()).toContainText('Album-4');
  await shelf.locator('#projectLibraryDirection').selectOption('asc');
  await shelf.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  await expect(shelf.locator('#projectLibraryRows tr').first()).toContainText('Album-0');
  await shot(page, '22h-reviewed-portfolio-sort-direction');

  // Losing the installed Home provider ends new notes/scans, not the user's
  // accepted session history. Re-enabling this same installation restores
  // ordinary editing without granting another scan or opening a project.
  const albumID = projects.rows.find((row: { name: string }) => row.name === 'Album-3')?.id;
  expect(albumID).toBeTruthy();
  const savedSessions = await json(await request.get(`${base}/projects/${albumID}/sessions`));
  expect(savedSessions.total).toBe(1);
  const disable = await request.post('/api/plugins/music-project-management/disable', {
    headers: { 'X-Requested-With': 'XMLHttpRequest' }
  });
  expect(disable.ok(), await disable.text()).toBeTruthy();
  await page.reload();
  await expect(shelf.locator('#projectLibraryStatus')).toContainText('Saved records are readable');
  await expect(shelf.locator('#projectLibraryResume')).toContainText('Kept the quieter chorus');
  await expect(shelf.locator('#projectLibraryResume')).toContainText('Record a scratch vocal');
  const readOnlySessions = await json(await request.get(`${base}/projects/${albumID}/sessions`));
  expect(readOnlySessions.total).toBe(1);
  expect(readOnlySessions.rows[0].id).toBe(savedSessions.rows[0].id);
  const refusedGoal = await request.post(`${base}/projects/${albumID}/sessions/goals/review`, {
    data: { if_entry_revision: 1, goal: { goal: 'Unauthorized after provider loss' } }
  });
  expect(refusedGoal.status()).toBe(409);
  expect((await json(await request.get(`${base}/projects/${albumID}/sessions`))).total).toBe(1);
  await shelf
    .locator('#projectLibraryResume')
    .getByRole('button', { name: 'View Album-3 session' })
    .click();
  const pausedDetail = page.getByRole('dialog', { name: 'Album-3' });
  await expect(pausedDetail).toContainText('User recap: Kept the quieter chorus');
  await expect(pausedDetail.getByRole('button', { name: 'Plan a session' })).toHaveCount(0);
  await expect(pausedDetail.getByRole('button', { name: 'Wrap up session' })).toHaveCount(0);
  await shot(page, '22f-reviewed-portfolio-resume-with-provider-disabled');
  await pausedDetail.getByRole('button', { name: 'Close' }).click();
  const reenable = await request.post('/api/plugins/music-project-management/enable', {
    headers: { 'X-Requested-With': 'XMLHttpRequest' }
  });
  expect(reenable.ok(), await reenable.text()).toBeTruthy();
  await page.reload();
  await expect(shelf.locator('#projectLibraryStatus')).not.toContainText(
    'Saved records are readable'
  );
  await shelf
    .locator('#projectLibraryRoots')
    .getByRole('button', { name: 'Review smaller folder' })
    .click();
  const scopes = page.getByRole('dialog', { name: 'Choose a smaller folder' });
  await expect(scopes).toBeVisible();
  await scopes.getByRole('button', { name: 'Album-0' }).click();
  const narrower = page.getByRole('dialog', { name: 'Scan this selected folder once?' });
  await expect(narrower).toContainText('Album-0');
  await shot(page, '23-reviewed-portfolio-narrow-scope-review');
  await narrower.getByRole('button', { name: 'Scan metadata' }).click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
  expect((await json(await request.get(`${base}/projects`))).total).toBe(5);

  // Only an opt-in paired run stages a clean REAPER source export. Installing
  // that export changes no real user plugin store and is not a reviewed
  // published-REAPER release or permission to staff/open an application.
  const reaperSource = process.env.ORI_REAPER_PLUGIN_PATH || '';
  if (reaperSource) {
    const installedReaper = await json(
      await request.post('/api/plugins/install', {
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
        data: { source: reaperSource, confirm: true }
      })
    );
    expect(installedReaper.plugin).toMatchObject({ name: 'reaper-plugin' });
    expect(installedReaper.plugin.source).toBe(reaperSource);
    await json(
      await request.post('/api/plugins/reaper-plugin/enable', {
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      })
    );
    const workspacesBefore = (await json(await request.get('/api/workspaces'))).folders;
    await page.reload();
    await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-3' })
      .click();
    const eligible = page.getByRole('dialog', { name: 'Album-3' });
    await expect(eligible).toContainText('Installed project roles: REAPER Assistant');
    await expect(eligible).toContainText('Song.rpp');
    await eligible.getByRole('button', { name: 'Review project setup' }).click();
    const setupForm = page.getByRole('dialog', { name: 'Set up Album-3' });
    await expect(
      setupForm.getByRole('combobox', { name: 'Authoritative project file' })
    ).toHaveValue('Song.rpp');
    await setupForm.getByRole('button', { name: 'Cancel' }).click();
    expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspacesBefore.length
    );
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-3' })
      .click();
    await page
      .getByRole('dialog', { name: 'Album-3' })
      .getByRole('button', { name: 'Review project setup' })
      .click();
    await page
      .getByRole('dialog', { name: 'Set up Album-3' })
      .getByRole('button', { name: 'Review this project' })
      .click();
    const projectReview = page.getByRole('dialog', { name: 'Connect this one project?' });
    await expect(projectReview).toContainText('No other catalog projects are created');
    await expect(projectReview).toContainText('Starts File-only');
    await expect(projectReview).toContainText(
      'Project-role staffing and live access require separate reviews'
    );
    await shot(page, '24-reviewed-single-song-activation');
    const commitReply = page.waitForResponse(
      response =>
        response.url().endsWith(`/projects/${albumID}/activation/commit`) &&
        response.request().method() === 'POST'
    );
    await projectReview.getByRole('button', { name: 'Connect project' }).click();
    const committedResponse = await commitReply;
    expect(committedResponse.ok(), await committedResponse.text()).toBeTruthy();
    const committed = await committedResponse.json();
    const connected = page.getByRole('dialog', { name: 'Album-3 is connected' });
    // A workspace page is routed by its folder slug. /workspaces/<id> is a 404,
    // which the library's links used to point at; the links must use the slug.
    const childSlug = String(
      (await json(await request.get(`/api/workspaces/${committed.workspace_id}`))).folder_slug
    );
    expect(childSlug).not.toBe('');
    expect(childSlug).not.toBe(committed.workspace_id);
    const childRoute = `/workspaces/${encodeURIComponent(childSlug)}`;
    await expect(connected.getByRole('link', { name: 'Open project workspace' })).toHaveAttribute(
      'href',
      childRoute
    );
    await shot(page, '25-reviewed-single-song-connected');
    // music-setup-onboarding, group 4. A library-created child has not chosen how
    // it works yet, so its own page opens the mode wizard first (File-only is the
    // starting option) and its team comes right after. The dialog says so and does
    // not request the team form on top of that wizard.
    await expect(connected).toContainText('choose how it works');
    await expect(connected.getByRole('link', { name: 'Set up the project team' })).toHaveCount(0);
    await connected.getByRole('button', { name: 'Stay in library' }).click();
    const workspacesAfter = (await json(await request.get('/api/workspaces'))).folders;
    expect(workspacesAfter).toHaveLength(workspacesBefore.length + 1);
    const child = await json(await request.get(`/api/workspaces/${committed.workspace_id}`));
    expect(child.agent_instances || []).toHaveLength(0);
    expect(
      (await json(await request.get(`${base}/projects`))).rows.filter(
        (row: { connection: string }) => row.connection === 'connected'
      )
    ).toHaveLength(1);
    expect(
      songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex'))
    ).toEqual(before);

    // The child's own roster lists its project role, unfilled (not the Home's).
    const childRolesURL = `/api/workspaces/${committed.workspace_id}/roles`;
    const rosterBefore = (await json(await request.get(childRolesURL))).roles;
    expect(rosterBefore.roles.map((role: { role_id: string }) => role.role_id)).toEqual([
      'reaper-assistant'
    ]);
    expect(rosterBefore.filled_count).toBe(0);
    const homeRosterBefore = (
      await json(await request.get(`/api/workspaces/${homeID}/assistant-program`))
    ).roster;
    // Opening the child by its real route shows the project, not a 404, and
    // starts with its own mode wizard. Looking at it creates and staffs nothing.
    const childPage = await page.context().newPage();
    try {
      await childPage.goto(childRoute);
      await expect(childPage.getByRole('heading', { name: 'Set up Reaper Song' })).toBeVisible({
        timeout: 30_000
      });
      await expect(childPage.locator('body')).not.toContainText('404 page not found');
      await shot(childPage, '25a-library-child-mode-first');
    } finally {
      await childPage.close();
    }
    expect(
      (
        (await json(await request.get(`/api/workspaces/${committed.workspace_id}`)))
          .agent_instances || []
      ).length
    ).toBe(0);
    expect((await json(await request.get(childRolesURL))).roles.filled_count).toBe(0);
    expect(
      (await json(await request.get(`/api/workspaces/${homeID}/assistant-program`))).roster
    ).toEqual(homeRosterBefore);
    expect(
      songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex')),
      'opening the project never touched a project file'
    ).toEqual(before);
    await page.reload();
    const afterRestartView = await json(await request.get(`${base}/projects`));
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-3' })
      .click();
    const linkedDetail = page.getByRole('dialog', { name: 'Album-3' });
    await expect(
      linkedDetail.getByRole('link', { name: 'Open connected workspace' })
    ).toHaveAttribute('href', childRoute);
    await linkedDetail.getByRole('button', { name: 'Close' }).click();
    const returnToHome = page.url();
    await shelf
      .locator('#projectLibraryResume')
      .getByRole('button', { name: 'Open Album-3 workspace' })
      .click();
    await page.waitForURL(new RegExp(`${childRoute.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`));
    // The address matching is not enough: an unroutable path also matches it.
    // The page itself must be the project, not a 404.
    await expect(page.locator('body')).not.toContainText('404 page not found');
    await expect(page.locator('body')).toContainText('Album-3');
    await page.goto(returnToHome);
    await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
    expect(
      afterRestartView.rows.filter((row: { connection: string }) => row.connection === 'connected')
    ).toHaveLength(1);
    expect((await json(await request.get(`${base}/projects/${albumID}/sessions`))).total).toBe(1);

    // Home owns queue order and skip receipts. A browser key is only an
    // optional same-tab creator retry; clearing it must not lose the next
    // song after a skip. Each song still needs its own review/confirmation.
    await shelf.getByRole('checkbox', { name: 'Select Album-1 for serial project review' }).check();
    await shelf.getByRole('checkbox', { name: 'Select Album-2 for serial project review' }).check();
    await shelf.locator('#projectLibraryQueueStart').click();
    const firstInQueue = page.getByRole('dialog', { name: 'Song 1 of 2 · Album-1' });
    await expect(firstInQueue).toContainText('Skipping creates nothing');
    await firstInQueue.getByRole('button', { name: 'Skip this song' }).click();
    const secondInQueue = page.getByRole('dialog', { name: 'Song 2 of 2 · Album-2' });
    await secondInQueue.getByRole('button', { name: 'Pause queue' }).click();
    await expect(shelf.locator('#projectLibraryQueueStatus')).toContainText('1 of 2 handled');
    const savedQueue = await json(await request.get(`${base}/queue`));
    expect(savedQueue.queue).toMatchObject({
      ids: [
        projects.rows.find((r: { name: string }) => r.name === 'Album-1')?.id,
        projects.rows.find((r: { name: string }) => r.name === 'Album-2')?.id
      ],
      index: 1,
      skipped: [projects.rows.find((r: { name: string }) => r.name === 'Album-1')?.id]
    });
    expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspacesBefore.length + 1
    );
    await shot(page, '26-reviewed-queue-paused-after-skip');
    // A distinct browser context has no sessionStorage or creator retry key.
    // The exact Home still owns order and skip receipts, not the old tab.
    const freshContext = await browser.newContext();
    try {
      const freshPage = await freshContext.newPage();
      await freshPage.goto(page.url());
      await expect(freshPage.locator('#projectLibraryQueueStatus')).toContainText('1 skipped');
      await expect(freshPage.locator('#projectLibraryQueueResume')).toBeVisible();
      const newHint = await freshPage.evaluate(
        id => sessionStorage.getItem(`ori:library-queue:${id}`),
        homeID
      );
      expect(JSON.parse(newHint || 'null')).toMatchObject({ index: 1, pending: null });
    } finally {
      await freshContext.close();
    }
    if (process.env.ORI_MUSIC_RESTART_TEST === '1') {
      // Only the opt-in wrapper restarts its own disposable server process.
      // Leave the browser open with a pending item; no creator confirmation
      // or source access is stored in the Home queue.
      writeFileSync(join(sandbox, 'evidence', 'restart.request'), 'pending browser queue\n', {
        mode: 0o600
      });
      await expect
        .poll(
          () => {
            if (existsSync(join(sandbox, 'evidence', 'restart.failed'))) return 'failed';
            if (existsSync(join(sandbox, 'evidence', 'restart.done'))) return 'ready';
            return 'pending';
          },
          { timeout: 45_000 }
        )
        .toBe('ready');
      const processEvidence = readFileSync(join(sandbox, 'evidence', 'restart.done'), 'utf8');
      const processIDs = processEvidence.match(/same HOME and ORI_DATA_DIR: (\d+) -> (\d+)/);
      expect(processIDs).toBeTruthy();
      expect(processIDs?.[1]).not.toBe(processIDs?.[2]);
      await page.reload();
      await expect(shelf.locator('#projectLibraryQueueStatus')).toContainText('1 skipped');
      expect((await json(await request.get(`${base}/queue`))).queue).toMatchObject({
        id: savedQueue.queue.id,
        index: 1,
        skipped: savedQueue.queue.skipped
      });
      expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
        workspacesBefore.length + 1
      );
      expect(
        songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex'))
      ).toEqual(before);
      await expect(page.locator('#assistantProgramPage')).toHaveAttribute('aria-busy', 'false');
      await expect(shelf.locator('#projectLibraryQueueResume')).toBeVisible();
      await shelf.locator('#projectLibraryQueueStatus').scrollIntoViewIfNeeded();
      await shot(page, '39-actual-server-restart-queue-pending');
    }
    await page.evaluate(() => sessionStorage.clear()); // no local queue survives
    await page.reload();
    await expect(shelf.locator('#projectLibraryQueueStatus')).toContainText('1 skipped');
    await expect(shelf.locator('#projectLibraryQueueResume')).toBeVisible();
    await shelf.locator('#projectLibraryQueueResume').click();
    await page
      .getByRole('dialog', { name: 'Song 2 of 2 · Album-2' })
      .getByRole('button', { name: 'Review this song' })
      .click();
    await page
      .getByRole('dialog', { name: 'Set up Album-2' })
      .getByRole('button', { name: 'Review this project' })
      .click();
    const queuedReview = page.getByRole('dialog', { name: 'Connect this one project?' });
    await expect(queuedReview).toContainText('Queue item 2 of 2');
    await expect(queuedReview).toContainText('Starts File-only');
    await shot(page, '27-reviewed-second-song-in-serial-queue');
    const album2ID = projects.rows.find((row: { name: string }) => row.name === 'Album-2')?.id;
    expect(album2ID).toBeTruthy();
    const commitRoute = `**/projects/${album2ID}/activation/commit`;
    // Let the real creator and Home association finish, but drop the reply to
    // this browser tab. A confirmed pending retry survives reload; the fresh
    // reciprocal-link read must advance without submitting another creator.
    await page.route(commitRoute, async route => {
      const serverReply = await route.fetch();
      expect(serverReply.ok(), await serverReply.text()).toBeTruthy();
      await route.abort('failed');
    });
    await queuedReview.getByRole('button', { name: 'Connect project' }).click();
    await expect(shelf.locator('#projectLibraryStatus')).toContainText('Queue paused');
    await expect
      .poll(async () => {
        const page = await json(await request.get(`${base}/projects`));
        return page.rows.find((row: { id: string }) => row.id === album2ID)?.connection;
      })
      .toBe('connected');
    await expect(
      shelf.locator('#projectLibraryRows tr').filter({ hasText: 'Album-2' })
    ).toContainText('connected');
    await page.evaluate(() => {
      const status = document.getElementById('projectLibraryQueueStatus');
      if (status) scrollTo(0, scrollY + status.getBoundingClientRect().top - 160);
    });
    await shot(page, '27b-confirmed-project-lost-browser-reply');
    await page.unroute(commitRoute);
    await page.reload();
    await expect(shelf.locator('#projectLibraryQueueResume')).toBeVisible();
    await shelf.locator('#projectLibraryQueueResume').click();
    await expect(shelf.locator('#projectLibraryQueueStatus')).toContainText('Choose at least two');
    const firstOutcome = await json(await request.get(`${base}/queue`));
    expect(firstOutcome.queue).toBeUndefined();
    expect(firstOutcome.recent[0]).toMatchObject({
      status: 'complete',
      selected_count: 2,
      connected_count: 1,
      skipped_count: 1
    });
    await expect(shelf.locator('#projectLibraryQueueHistory')).toContainText(
      '1 connected · 1 skipped'
    );
    const queueProjects = await json(await request.get(`${base}/projects`));
    expect(
      queueProjects.rows.filter((row: { connection: string }) => row.connection === 'connected')
    ).toHaveLength(2);
    expect(
      queueProjects.rows.filter((row: { connection: string }) => row.connection === 'catalog_only')
    ).toHaveLength(3);
    const afterQueue = (await json(await request.get('/api/workspaces'))).folders;
    expect(afterQueue).toHaveLength(workspacesBefore.length + 2);
    await shot(page, '28-serial-queue-confirmed-second-song');
    await page.reload();
    await expect(shelf.locator('#projectLibraryQueueResume')).toBeHidden();
    expect((await json(await request.get(`${base}/projects/${albumID}/sessions`))).total).toBe(1);

    // A later separately reviewed rescan finds an alternative .rpp in the
    // *same* project folder, not an extra song. The browser must not silently
    // select the first filename or create a child when the review is canceled.
    const alternate = join(documents, 'Album-4', 'Alternate.rpp');
    writeFileSync(alternate, 'fixture-alternate; metadata-only', { mode: 0o600 });
    const alternateHash = createHash('sha256').update(readFileSync(alternate)).digest('hex');
    await shelf
      .locator('#projectLibraryRoots')
      .getByRole('button', { name: 'Review scan' })
      .click();
    const rescanReview = page.getByRole('dialog', { name: 'Scan this discovery folder once?' });
    await expect(rescanReview).toBeVisible();
    expect((await json(await request.get(`${base}/projects`))).total).toBe(5);
    await rescanReview.getByRole('button', { name: 'Scan metadata' }).click();
    await expect(shelf.locator('#projectLibraryStatus')).toContainText(
      'complete scan · 11 entries seen'
    );
    await expect(shelf).toHaveAttribute('aria-busy', 'false');
    await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-4' })
      .click();
    const ambiguous = page.getByRole('dialog', { name: 'Album-4' });
    await expect(ambiguous).toContainText('Choose the authoritative project file');
    await expect(ambiguous).toContainText('Alternate.rpp');
    await expect(ambiguous).toContainText('Song.rpp');
    await ambiguous.getByRole('button', { name: 'Review project setup' }).click();
    const fileForm = page.getByRole('dialog', { name: 'Set up Album-4' });
    const fileChoice = fileForm.getByRole('combobox', { name: 'Authoritative project file' });
    await expect(fileChoice).toHaveValue('');
    await fileForm.getByRole('button', { name: 'Review this project' }).click();
    await expect(fileForm).toBeVisible(); // Browser validation requires an explicit choice.
    await fileChoice.selectOption('Alternate.rpp');
    await fileForm.getByRole('button', { name: 'Review this project' }).click();
    const alternateReview = page.getByRole('dialog', { name: 'Connect this one project?' });
    await expect(alternateReview).toContainText('Authoritative file: Alternate.rpp');
    await shot(page, '29-reviewed-ambiguous-authoritative-file');
    await alternateReview.getByRole('button', { name: 'Cancel' }).click();
    expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspacesBefore.length + 2
    );
    expect((await json(await request.get(`${base}/projects`))).total).toBe(5);
    expect(createHash('sha256').update(readFileSync(alternate)).digest('hex')).toBe(alternateHash);

    // The same Home owns user notes even for a catalog-only, ambiguous song.
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-4' })
      .click();
    await page
      .getByRole('dialog', { name: 'Album-4' })
      .getByRole('button', { name: 'Edit project notes' })
      .click();
    const notes = page.getByRole('dialog', { name: 'Edit Album-4' });
    await notes
      .getByRole('textbox', { name: 'Project purpose (user-entered)' })
      .fill('Record a scratch vocal');
    await notes.getByLabel('Session date (user-entered)').fill('2026-10-12');
    await notes
      .getByRole('textbox', { name: /Blockers/ })
      .fill('Need a microphone\nConfirm the key');
    await notes.getByRole('textbox', { name: /Deliverables/ }).fill('One scratch take');
    await notes.getByRole('combobox', { name: /Archive review/ }).selectOption('ready');
    await notes.getByRole('button', { name: 'Add milestone' }).click();
    await notes.getByRole('textbox', { name: 'Milestone', exact: true }).fill('Record guide vocal');
    await notes.getByLabel('Due date').fill('2026-10-13');
    await notes.getByRole('button', { name: 'Review changes' }).click();
    const notesReview = page.getByRole('dialog', { name: 'Save these project notes?' });
    await expect(notesReview).toContainText('Record a scratch vocal');
    await expect(notesReview).toContainText('2026-10-12');
    await expect(notesReview).toContainText('Need a microphone; Confirm the key');
    await expect(notesReview).toContainText('Record guide vocal · 2026-10-13 · open');
    await expect(notesReview).toContainText('Archive review is a note, not a file move.');
    await shot(page, '30-reviewed-portfolio-metadata-fields');
    await notesReview.getByRole('button', { name: 'Save notes' }).click();
    const savedEntry = (await json(await request.get(`${base}/projects`))).rows.find(
      (row: { name: string }) => row.name === 'Album-4'
    );
    await expect
      .poll(async () => {
        const state = await json(await request.get(`${base}/projects/${savedEntry.id}`));
        return state.fields.purpose;
      })
      .toBe('Record a scratch vocal');
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-4' })
      .click();
    const savedNotes = page.getByRole('dialog', { name: 'Album-4' });
    await expect(savedNotes).toContainText('Next action: Not set');
    const savedFields = (await json(await request.get(`${base}/projects/${savedEntry.id}`))).fields;
    expect(savedFields).toMatchObject({
      purpose: 'Record a scratch vocal',
      session_date: '2026-10-12',
      blockers: ['Need a microphone', 'Confirm the key'],
      deliverables: ['One scratch take'],
      milestones: [{ label: 'Record guide vocal', due_date: '2026-10-13' }],
      archive_review_state: 'ready'
    });
    await savedNotes.getByRole('button', { name: 'Close' }).click();
    const milestoneID = savedFields.milestones[0].id;
    await page.reload();
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-4' })
      .click();
    await page
      .getByRole('dialog', { name: 'Album-4' })
      .getByRole('button', { name: 'Edit project notes' })
      .click();
    const milestoneEdit = page.getByRole('dialog', { name: 'Edit Album-4' });
    await expect(
      milestoneEdit.getByRole('textbox', { name: 'Milestone', exact: true })
    ).toHaveValue('Record guide vocal');
    await milestoneEdit.getByRole('checkbox', { name: 'Complete' }).check();
    const changedRequest = page.waitForRequest(
      req =>
        req.url().endsWith(`/projects/${savedEntry.id}/fields/review`) && req.method() === 'POST'
    );
    await milestoneEdit.getByRole('button', { name: 'Review changes' }).click();
    const sparse = (await changedRequest).postDataJSON();
    expect(Object.keys(sparse.patch)).toEqual(['milestones']);
    const milestoneReview = page.getByRole('dialog', { name: 'Save these project notes?' });
    await expect(milestoneReview).toContainText('Record guide vocal · 2026-10-13 · complete');
    await milestoneReview.getByRole('button', { name: 'Save notes' }).click();
    await expect
      .poll(async () => {
        const response = await request.get(`${base}/projects/${savedEntry.id}`);
        if (!response.ok()) return false; // Retry a transient workspace-mirror read while the UI commits.
        const state = await json(response);
        return state.fields.milestones[0].complete;
      })
      .toBe(true);
    const updatedFields = (await json(await request.get(`${base}/projects/${savedEntry.id}`)))
      .fields;
    expect(updatedFields.milestones[0].id).toBe(milestoneID);
    expect(updatedFields.purpose).toBe('Record a scratch vocal');

    // Disconnecting discovery is not a project revoke or deletion. Cancel is
    // inert; confirmation ends future root reads but retains saved Home notes
    // and the independently reviewed Album-3/Album-2 workspace links.
    await shelf.locator('#projectLibraryRoots').getByRole('button', { name: 'Disconnect' }).click();
    const disconnect = page.getByRole('dialog', { name: 'Disconnect this discovery folder?' });
    await expect(disconnect).toContainText('5 catalog records retain their historical notes');
    await shot(page, '31-reviewed-discovery-disconnect-impact');
    await disconnect.getByRole('button', { name: 'Cancel' }).click();
    expect((await json(await request.get(`${base}/roots`))).roots[0].revoked_at).toBeFalsy();
    await shelf.locator('#projectLibraryRoots').getByRole('button', { name: 'Disconnect' }).click();
    await page
      .getByRole('dialog', { name: 'Disconnect this discovery folder?' })
      .getByRole('button', { name: 'Disconnect folder' })
      .click();
    await expect(shelf.locator('#projectLibraryRoots')).toContainText(
      'Disconnected · historical entries retained'
    );
    const afterDisconnect = await json(await request.get(`${base}/projects`));
    expect(afterDisconnect.total).toBe(5);
    expect(
      afterDisconnect.rows.filter((row: { connection: string }) => row.connection === 'connected')
    ).toHaveLength(2);
    const blockedSetup = await json(
      await request.get(`${base}/projects/${savedEntry.id}/activation`)
    );
    expect(blockedSetup.state).toBe('revoked_source');
    const linkedSetup = await json(await request.get(`${base}/projects/${albumID}/activation`));
    expect(linkedSetup).toMatchObject({ state: 'connected', workspace_id: committed.workspace_id });
    await expect(
      shelf.locator('#projectLibraryResume').getByRole('button', { name: 'Open Album-3 workspace' })
    ).toBeVisible();
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}`))).fields
    ).toMatchObject({
      purpose: 'Record a scratch vocal',
      milestones: [{ id: milestoneID, complete: true }]
    });
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-4' })
      .click();
    const revokedDetails = page.getByRole('dialog', { name: 'Album-4' });
    await expect(revokedDetails).toContainText('Discovery consent ended');
    await revokedDetails.getByRole('button', { name: 'Plan a session' }).click();
    const revokedPlan = page.getByRole('dialog', { name: 'Plan a studio session' });
    await revokedPlan
      .getByRole('textbox', { name: 'Session goal' })
      .fill('Review my saved notes without the folder');
    await revokedPlan.getByRole('button', { name: 'Review session' }).click();
    await page
      .getByRole('dialog', { name: 'Save this goal?' })
      .getByRole('button', { name: 'Save goal' })
      .click();
    await expect(shelf.locator('#projectLibraryResume')).toContainText(
      'Review my saved notes without the folder'
    );

    // A direct native tool invocation exercises the production chat/tool
    // adapter without a model. The Manager only saves an inert suggestion;
    // the Home owner must separately review and confirm the canonical note.
    const home = await json(await request.get(`/api/workspaces/${homeID}`));
    const manager = home.agent_instances.find(
      (instance: { role_id: string }) => instance.role_id === 'portfolio_manager'
    );
    expect(manager?.name).toBeTruthy();
    const beforeSuggestion = await json(await request.get(`${base}/projects/${savedEntry.id}`));
    const suggestedAction = 'Choose one vocal take to revisit';
    const direct = await json(
      await request.post('/api/chat', {
        data: {
          agent_name: manager.name,
          route_context: {
            surface: 'workspace_detail',
            workspace_id: homeID,
            page_path: `/workspaces/${homes[0].folder_slug}/assistant`
          },
          question: `/tool home_library_propose_next_action ${JSON.stringify({
            entry_id: savedEntry.id,
            fields_revision: beforeSuggestion.row.fields_revision,
            next_action: suggestedAction,
            reason: 'Only a Home suggestion; no DAW progress inferred.',
            request_key: `browser-suggestion-${homeID}`
          })}`
        }
      })
    );
    expect(direct.success, JSON.stringify(direct)).toBe(true);
    expect(direct.response).toContain('Suggestion saved only');
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}`))).fields.next_action
    ).toBe(beforeSuggestion.fields.next_action);
    await page.reload();
    const suggestions = page.locator('#projectLibraryProposals');
    await expect(suggestions).toContainText(suggestedAction);
    await expect(suggestions).toContainText('Only a Home suggestion; no DAW progress inferred.');
    await suggestions.getByRole('button', { name: 'Review Album-4 suggestion' }).click();
    const proposalReview = page.getByRole('dialog', {
      name: "Save Album-4's suggested next action?"
    });
    await expect(proposalReview).toContainText(beforeSuggestion.fields.next_action || 'Not set');
    await shot(page, '33-reviewed-manager-suggestion');
    await proposalReview.getByRole('button', { name: 'Cancel' }).click();
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}`))).fields.next_action
    ).toBe(beforeSuggestion.fields.next_action);
    await suggestions.getByRole('button', { name: 'Review Album-4 suggestion' }).click();
    await page
      .getByRole('dialog', { name: "Save Album-4's suggested next action?" })
      .getByRole('button', { name: 'Save next action' })
      .click();
    await expect(suggestions).toContainText('Not actionable (stale)');
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}`))).fields.next_action
    ).toBe(suggestedAction);
    await expect(shelf.locator('#projectLibraryResume')).toContainText(
      'Review my saved notes without the folder'
    );

    // A second direct Manager tool can suggest only Details navigation. A
    // revoked source still cannot produce a project-setup review or child.
    const beforeNavigation = await json(await request.get(`${base}/projects/${savedEntry.id}`));
    const childCountBeforeNavigation = (await json(await request.get('/api/workspaces'))).folders
      .length;
    const navigation = await json(
      await request.post('/api/chat', {
        data: {
          agent_name: manager.name,
          route_context: {
            surface: 'workspace_detail',
            workspace_id: homeID,
            page_path: `/workspaces/${homes[0].folder_slug}/assistant`
          },
          question: `/tool home_library_propose_project_review ${JSON.stringify({
            entry_id: savedEntry.id,
            entry_revision: beforeNavigation.entry_revision,
            reason: 'Look at current setup options; no source permission implied.',
            request_key: `browser-navigation-${homeID}`
          })}`
        }
      })
    );
    expect(navigation.success, JSON.stringify(navigation)).toBe(true);
    expect(navigation.response).toContain('Navigation suggestion saved only');
    await page.reload();
    const navSuggestions = page.locator('#projectLibraryProposals');
    await expect(navSuggestions).toContainText('No setup was reviewed or authorized');
    await navSuggestions.getByRole('button', { name: 'View Album-4 setup options' }).click();
    const navDetails = page.getByRole('dialog', { name: 'Album-4' });
    await expect(navDetails).toContainText('Discovery consent ended');
    await expect(navDetails.getByRole('button', { name: 'Review project setup' })).toHaveCount(0);
    await shot(page, '38-reviewed-manager-navigation-revoked');
    await navDetails.getByRole('button', { name: 'Close' }).click();
    expect((await json(await request.get('/api/workspaces'))).folders.length).toBe(
      childCountBeforeNavigation
    );
    await expect(shelf.locator('#projectLibraryResume')).toContainText(
      'Review my saved notes without the folder'
    );

    // A direct Manager suggestion is only an editable Home draft. The owner
    // separately opens the canonical goal form, cancels once, edits the goal
    // and explicitly reviews/confirms it; a revoked source grants no access.
    const beforeGoalSuggestion = await json(await request.get(`${base}/projects/${savedEntry.id}`));
    const sessionsBeforeGoal = await json(
      await request.get(`${base}/projects/${savedEntry.id}/sessions`)
    );
    const goalDraft = 'Map a short vocal take review';
    const goalReply = await json(
      await request.post('/api/chat', {
        data: {
          agent_name: manager.name,
          route_context: {
            surface: 'workspace_detail',
            workspace_id: homeID,
            page_path: `/workspaces/${homes[0].folder_slug}/assistant`
          },
          question: `/tool home_library_propose_session_goal ${JSON.stringify({
            entry_id: savedEntry.id,
            entry_revision: beforeGoalSuggestion.entry_revision,
            goal: goalDraft,
            desired_outcome: 'List candidate takes without opening a DAW',
            time_minutes: 20,
            reason: 'An optional planning draft; no session has been saved.',
            request_key: `browser-goal-draft-${homeID}`
          })}`
        }
      })
    );
    expect(goalReply.success, JSON.stringify(goalReply)).toBe(true);
    expect(goalReply.response).toContain('Session goal suggestion saved only');
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}/sessions`))).total
    ).toBe(sessionsBeforeGoal.total);
    await page.reload();
    const goalSuggestions = page.locator('#projectLibraryProposals');
    const goalButton = goalSuggestions.getByRole('button', { name: 'Edit Album-4 suggested goal' });
    await expect(goalSuggestions).toContainText(goalDraft);
    await goalButton.click();
    const draftedGoal = page.getByRole('dialog', { name: 'Plan a studio session' });
    await expect(draftedGoal).toContainText('Manager suggestion (untrusted draft)');
    await expect(draftedGoal.getByRole('textbox', { name: 'Session goal' })).toHaveValue(goalDraft);
    await expect(draftedGoal.getByRole('textbox', { name: 'Desired outcome' })).toHaveValue(
      'List candidate takes without opening a DAW'
    );
    await expect(
      draftedGoal.getByRole('spinbutton', { name: 'Available minutes (optional, 1–480)' })
    ).toHaveValue('20');
    await shot(page, '40-reviewed-manager-editable-goal-draft');
    await draftedGoal.getByRole('button', { name: 'Cancel' }).click();
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}/sessions`))).total
    ).toBe(sessionsBeforeGoal.total);
    await goalButton.click();
    const editedGoal = page.getByRole('dialog', { name: 'Plan a studio session' });
    await editedGoal
      .getByRole('textbox', { name: 'Session goal' })
      .fill('Review two vocal takes together');
    await editedGoal.getByRole('button', { name: 'Review session' }).click();
    const ownerGoalReview = page.getByRole('dialog', { name: 'Save this goal?' });
    await expect(ownerGoalReview).toContainText('Review two vocal takes together');
    await ownerGoalReview.getByRole('button', { name: 'Cancel' }).click();
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}/sessions`))).total
    ).toBe(sessionsBeforeGoal.total);
    await goalButton.click();
    await page
      .getByRole('dialog', { name: 'Plan a studio session' })
      .getByRole('textbox', { name: 'Session goal' })
      .fill('Review two vocal takes together');
    await page
      .getByRole('dialog', { name: 'Plan a studio session' })
      .getByRole('button', { name: 'Review session' })
      .click();
    await page
      .getByRole('dialog', { name: 'Save this goal?' })
      .getByRole('button', { name: 'Save goal' })
      .click();
    await expect
      .poll(
        async () =>
          (await json(await request.get(`${base}/projects/${savedEntry.id}/sessions`))).total
      )
      .toBe(sessionsBeforeGoal.total + 1);
    await expect(
      goalSuggestions.locator('.project-library-resume-card').filter({ hasText: goalDraft })
    ).toContainText('Not actionable (stale)');
    expect((await json(await request.get('/api/workspaces'))).folders.length).toBe(
      childCountBeforeNavigation
    );
    expect(
      songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex'))
    ).toEqual(before);

    // An exact saved session may receive only an inert recap draft. The
    // Manager cannot supply dates, decisions, a project next-action patch or
    // a completion claim. The owner edits and separately confirms the recap.
    const savedAfterGoal = await json(
      await request.get(`${base}/projects/${savedEntry.id}/sessions`)
    );
    const savedGoal = savedAfterGoal.rows.find(
      (row: { goal: string }) => row.goal === 'Review two vocal takes together'
    );
    expect(savedGoal?.id).toBeTruthy();
    const beforeRecapDetail = await json(await request.get(`${base}/projects/${savedEntry.id}`));
    const recapDraft = 'Consider writing down what to check on the next take';
    const recapSuggestion = await json(
      await request.post('/api/chat', {
        data: {
          agent_name: manager.name,
          route_context: {
            surface: 'workspace_detail',
            workspace_id: homeID,
            page_path: `/workspaces/${homes[0].folder_slug}/assistant`
          },
          question: `/tool home_library_propose_session_recap ${JSON.stringify({
            entry_id: savedEntry.id,
            entry_revision: beforeRecapDetail.entry_revision,
            fields_revision: beforeRecapDetail.row.fields_revision,
            session_id: savedGoal.id,
            session_revision: savedGoal.revision,
            recap: recapDraft,
            reason: 'An editable note only; no work or DAW progress observed.',
            request_key: `browser-recap-draft-${homeID}`
          })}`
        }
      })
    );
    expect(recapSuggestion.success, JSON.stringify(recapSuggestion)).toBe(true);
    expect(recapSuggestion.response).toContain('Session recap suggestion saved only');
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}/sessions/${savedGoal.id}`)))
        .recap
    ).toBeFalsy();
    await page.reload();
    const recapSuggestions = page.locator('#projectLibraryProposals');
    const recapButton = recapSuggestions.getByRole('button', {
      name: 'Edit Album-4 suggested recap'
    });
    await expect(recapSuggestions).toContainText(recapDraft);
    await recapButton.click();
    const recapForm = page.getByRole('dialog', { name: 'Wrap up studio session' });
    await expect(recapForm).toContainText(
      'Manager suggestion (untrusted draft, not evidence of work)'
    );
    await expect(recapForm.getByRole('textbox', { name: 'Your recap' })).toHaveValue(recapDraft);
    await expect(
      recapForm.getByRole('checkbox', {
        name: 'Also update this project’s saved next action (separate from the session note)'
      })
    ).not.toBeChecked();
    await expect(recapForm.getByRole('textbox', { name: 'Proposed next action' })).toHaveValue('');
    await shot(page, '42-reviewed-manager-editable-recap-draft');
    await recapForm.getByRole('button', { name: 'Cancel' }).click();
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}/sessions/${savedGoal.id}`)))
        .recap
    ).toBeFalsy();
    await recapButton.click();
    await page
      .getByRole('dialog', { name: 'Wrap up studio session' })
      .getByRole('textbox', { name: 'Your recap' })
      .fill('User chose two take names to revisit');
    await page
      .getByRole('dialog', { name: 'Wrap up studio session' })
      .getByRole('button', { name: 'Review session' })
      .click();
    const ownerRecapReview = page.getByRole('dialog', { name: 'Save this recap?' });
    await expect(ownerRecapReview).toContainText('User chose two take names to revisit');
    await ownerRecapReview.getByRole('button', { name: 'Cancel' }).click();
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}/sessions/${savedGoal.id}`)))
        .recap
    ).toBeFalsy();
    await recapButton.click();
    await page
      .getByRole('dialog', { name: 'Wrap up studio session' })
      .getByRole('textbox', { name: 'Your recap' })
      .fill('User chose two take names to revisit');
    await page
      .getByRole('dialog', { name: 'Wrap up studio session' })
      .getByRole('button', { name: 'Review session' })
      .click();
    await page
      .getByRole('dialog', { name: 'Save this recap?' })
      .getByRole('button', { name: 'Save recap' })
      .click();
    await expect
      .poll(
        async () =>
          (
            await json(
              await request.get(`${base}/projects/${savedEntry.id}/sessions/${savedGoal.id}`)
            )
          ).recap
      )
      .toBe('User chose two take names to revisit');
    await expect(
      recapSuggestions.locator('.project-library-resume-card').filter({ hasText: recapDraft })
    ).toContainText('Not actionable (stale)');
    expect(
      (await json(await request.get(`${base}/projects/${savedEntry.id}`))).fields.next_action
    ).toBe(beforeRecapDetail.fields.next_action);
    expect((await json(await request.get('/api/workspaces'))).folders.length).toBe(
      childCountBeforeNavigation
    );
    expect(
      songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex'))
    ).toEqual(before);

    // The bound Manager may point to discovery controls, but never pass a
    // path, root ID, picker receipt or scan grant. The owner views the
    // current revoked root; nothing is selected or reviewed automatically.
    const discoverySuggestion = await json(
      await request.post('/api/chat', {
        data: {
          agent_name: manager.name,
          route_context: {
            surface: 'workspace_detail',
            workspace_id: homeID,
            page_path: `/workspaces/${homes[0].folder_slug}/assistant`
          },
          question: `/tool home_library_propose_root_review ${JSON.stringify({
            reason: 'Check current discovery controls; do not renew consent.',
            request_key: `browser-root-navigation-${homeID}`
          })}`
        }
      })
    );
    expect(discoverySuggestion.success, JSON.stringify(discoverySuggestion)).toBe(true);
    expect(discoverySuggestion.response).toContain('Navigation suggestion saved only');
    await page.reload();
    const rootSuggestions = page.locator('#projectLibraryProposals');
    await expect(rootSuggestions).toContainText('No folder was selected, approved or scanned');
    await rootSuggestions.getByRole('button', { name: 'View discovery folder options' }).click();
    const rootOptions = page.locator('#projectLibraryRoots');
    await expect(rootOptions).toBeFocused();
    await expect(rootOptions).toContainText('Disconnected · historical entries retained');
    await expect(rootOptions.getByRole('button', { name: 'Review scan' })).toHaveCount(0);
    await shot(page, '41-reviewed-manager-discovery-navigation-revoked');
    expect((await json(await request.get('/api/workspaces'))).folders.length).toBe(
      childCountBeforeNavigation
    );
    expect(
      songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex'))
    ).toEqual(before);

    // A revoked root blocks project review, not an explicit queue Skip. Save
    // the later Album-4 item, then forget it separately below and require a
    // second Skip rather than silently deleting the remaining queue order.
    await shelf.getByRole('checkbox', { name: 'Select Album-1 for serial project review' }).check();
    await shelf.getByRole('checkbox', { name: 'Select Album-4 for serial project review' }).check();
    await shelf.locator('#projectLibraryQueueStart').click();
    const revokedQueue = page.getByRole('dialog', { name: 'Song 1 of 2 · Album-1' });
    await expect(revokedQueue).toContainText('Project setup is unavailable');
    await expect(revokedQueue.getByRole('button', { name: 'Review this song' })).toHaveCount(0);
    await shot(page, '34-revoked-queue-skip-only');
    await expect(revokedQueue.getByRole('button', { name: 'Pause queue' })).toBeFocused();
    await revokedQueue.getByRole('button', { name: 'Pause queue' }).press('Enter');
    expect((await json(await request.get(`${base}/queue`))).queue).toMatchObject({ index: 0 });
    await expect(shelf).toHaveAttribute('aria-busy', 'false');
    await expect(shelf.locator('#projectLibraryQueueResume')).toBeEnabled();
    await shelf.locator('#projectLibraryQueueResume').press('Enter');
    await expect(page.getByRole('dialog', { name: 'Song 1 of 2 · Album-1' })).toBeVisible();
    await page
      .getByRole('dialog', { name: 'Song 1 of 2 · Album-1' })
      .getByRole('button', { name: 'Skip this song' })
      .press('Enter');
    const pausedForForget = page.getByRole('dialog', { name: 'Song 2 of 2 · Album-4' });
    await expect(pausedForForget.getByRole('button', { name: 'Review this song' })).toHaveCount(0);
    await pausedForForget.getByRole('button', { name: 'Pause queue' }).click();
    expect((await json(await request.get(`${base}/queue`))).queue).toMatchObject({
      index: 1,
      skipped: [afterDisconnect.rows.find((r: { name: string }) => r.name === 'Album-1')?.id]
    });
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-4' })
      .click();
    await page
      .getByRole('dialog', { name: 'Album-4' })
      .getByRole('button', { name: 'Review forgetting this Home record' })
      .click();
    const forget = page.getByRole('dialog', { name: 'Forget Album-4 from this Home?' });
    await expect(forget).toContainText('2 saved studio session(s)');
    await expect(forget).toContainText('song 2 in the saved review queue');
    await expect(forget.getByRole('button', { name: 'Cancel' })).toBeFocused();
    await shot(page, '32-reviewed-historical-record-forget-impact');
    await forget.getByRole('button', { name: 'Cancel' }).press('Enter');
    expect((await json(await request.get(`${base}/projects`))).total).toBe(5);
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-4' })
      .click();
    await page
      .getByRole('dialog', { name: 'Album-4' })
      .getByRole('button', { name: 'Review forgetting this Home record' })
      .click();
    await page
      .getByRole('dialog', { name: 'Forget Album-4 from this Home?' })
      .getByRole('button', { name: 'Forget saved Home record' })
      .press('Enter');
    await expect(shelf.locator('#projectLibraryCount')).toHaveText('4 of 4 projects');
    expect((await json(await request.get(`${base}/projects`))).total).toBe(4);
    expect((await json(await request.get(`${base}/queue`))).queue).toMatchObject({
      index: 1,
      ids: [
        afterDisconnect.rows.find((r: { name: string }) => r.name === 'Album-1')?.id,
        savedEntry.id
      ]
    });
    await shelf.locator('#projectLibraryQueueResume').click();
    const forgottenQueue = page.getByRole('dialog', {
      name: 'Song 2 of 2 · No longer in this Home library'
    });
    await expect(forgottenQueue).toContainText('Project setup is unavailable');
    await expect(forgottenQueue.getByRole('button', { name: 'Review this song' })).toHaveCount(0);
    await shot(page, '35-forgotten-queue-skip-only');
    await expect(forgottenQueue.getByRole('button', { name: 'Pause queue' })).toBeFocused();
    await forgottenQueue.getByRole('button', { name: 'Skip this song' }).press('Enter');
    await expect(shelf.locator('#projectLibraryQueueResume')).toBeHidden();
    const secondOutcome = await json(await request.get(`${base}/queue`));
    expect(secondOutcome.queue).toBeUndefined();
    expect(secondOutcome.recent.slice(0, 2)).toMatchObject([
      { status: 'complete', selected_count: 2, connected_count: 0, skipped_count: 2 },
      { status: 'complete', selected_count: 2, connected_count: 1, skipped_count: 1 }
    ]);
    await expect(shelf.locator('#projectLibraryQueueHistory')).toContainText(
      '0 connected · 2 skipped'
    );

    // A provider loss cannot authorize new setup/skip, but the owner must
    // still be able to discard *only* the pending Home navigation metadata.
    await shelf.getByRole('checkbox', { name: 'Select Album-0 for serial project review' }).check();
    await shelf.getByRole('checkbox', { name: 'Select Album-1 for serial project review' }).check();
    await shelf.locator('#projectLibraryQueueStart').click();
    await page
      .getByRole('dialog', { name: 'Song 1 of 2 · Album-0' })
      .getByRole('button', { name: 'Pause queue' })
      .click();
    await expect(shelf).toHaveAttribute('aria-busy', 'false');
    const disableForQueue = await request.post('/api/plugins/music-project-management/disable', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' }
    });
    expect(disableForQueue.ok(), await disableForQueue.text()).toBeTruthy();
    await page.reload();
    await expect(shelf.locator('#projectLibraryQueueStatus')).toContainText('0 of 2 handled');
    await expect(shelf.locator('#projectLibraryQueueResume')).toBeDisabled();
    await shelf.locator('#projectLibraryQueueStatus').scrollIntoViewIfNeeded();
    await shot(page, '36-provider-disabled-queue-discard');
    await shelf.locator('#projectLibraryQueueDiscard').click();
    const discardReview = page.getByRole('dialog', { name: 'Discard this review queue?' });
    await expect(discardReview).toContainText(
      'Any project you separately confirmed stays connected'
    );
    await discardReview.getByRole('button', { name: 'Discard queue' }).click();
    await expect(shelf.locator('#projectLibraryQueueResume')).toBeHidden();
    const discardedOutcome = await json(await request.get(`${base}/queue`));
    expect(discardedOutcome.queue).toBeUndefined();
    expect(discardedOutcome.recent.slice(0, 3)).toMatchObject([
      { status: 'discarded', selected_count: 2, connected_count: 0, skipped_count: 0 },
      { status: 'complete', connected_count: 0, skipped_count: 2 },
      { status: 'complete', connected_count: 1, skipped_count: 1 }
    ]);
    await expect(shelf.locator('#projectLibraryQueueHistory')).toContainText(
      'Unreviewed songs were not activated'
    );
    await shelf.locator('#projectLibraryQueueHistory').scrollIntoViewIfNeeded();
    await shot(page, '37-reviewed-queue-outcome-history');
    const reenableAfterQueue = await request.post('/api/plugins/music-project-management/enable', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' }
    });
    expect(reenableAfterQueue.ok(), await reenableAfterQueue.text()).toBeTruthy();
    await page.reload();
    await expect(shelf.locator('#projectLibraryResume')).not.toContainText(
      'Review my saved notes without the folder'
    );
    expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspacesBefore.length + 2
    );
    expect(createHash('sha256').update(readFileSync(alternate)).digest('hex')).toBe(alternateHash);
  }

  // music-setup-onboarding, group 1. A Home reopened WITHOUT its original
  // address (a new tab, a bookmark) still knows which collection it was created
  // from, and a folder this Home already approved goes to its scan review rather
  // than a second folder pick or a refused duplicate grant. Evidence label: this
  // offer came from the documents chip, so it is fake/chip-chooser evidence, NOT
  // the native macOS dialog.
  const continuations = await json(
    await request.get(
      `/api/personal-assistant/folder-digest/continuations?home_id=${encodeURIComponent(homeID)}`
    )
  );
  expect(continuations.continuations).toMatchObject([{ offer_id: offerID, state: 'ready' }]);
  expect(JSON.stringify(continuations)).not.toContain(documents);
  const homeRoute = new URL(page.url()).pathname;
  const reopenBare = async () => {
    const bare = await page.context().newPage();
    const libraryPosts: string[] = [];
    bare.on('request', apiRequest => {
      const pathname = new URL(apiRequest.url()).pathname;
      if (apiRequest.method() === 'POST' && pathname.startsWith(`${base}/roots/`)) {
        libraryPosts.push(pathname.slice(base.length));
      }
    });
    await bare.goto(homeRoute);
    expect(new URL(bare.url()).search).toBe('');
    await expect(bare.locator('#projectLibraryPanel')).toBeVisible();
    return { bare, shelf: bare.locator('#projectLibraryPanel'), libraryPosts };
  };
  const activeRoots = async () => (await json(await request.get(`${base}/roots`))).total_roots;

  // 1. This spec revoked the root earlier, so the restored collection needs a
  //    fresh, separately confirmed grant: reviewed from the server's own record,
  //    with no second native pick.
  //    Only the paired run disconnects the folder; an unpaired run still has it
  //    approved, so the re-grant and unfinished-scan checks below do not apply.
  const rootsBeforeRegrant = await activeRoots();
  const revokedEarlier = Boolean(reaperSource);
  if (revokedEarlier) {
    const first = await reopenBare();
    await first.shelf.locator('#projectLibraryAdd').click();
    const regrant = first.bare.getByRole('dialog', { name: 'Connect this discovery folder?' });
    await expect(regrant).toBeVisible();
    await expect(regrant).toContainText(documents);
    expect(await activeRoots()).toBe(rootsBeforeRegrant); // reviewing grants nothing
    await shot(first.bare, '38-bare-home-reopen-regrant-review');
    await regrant.getByRole('button', { name: 'Connect folder' }).click();
    const laterScan = first.bare.getByRole('dialog', { name: 'Scan this discovery folder once?' });
    await expect(laterScan).toBeVisible();
    await first.bare.keyboard.press('Escape'); // decline the scan; the grant is kept
    await expect(laterScan).toBeHidden();
    expect(first.libraryPosts).toEqual([
      '/roots/pick-offer',
      '/roots/review',
      '/roots/commit',
      expect.stringMatching(/^\/roots\/[^/]+\/scans\/review$/)
    ]);
    expect(await activeRoots()).toBe(rootsBeforeRegrant + 1);
    // Declining left a connected, unscanned folder: the setup card offers the scan.
    await expect(first.bare.locator('#projectSetupNextTitle')).toContainText('Scan Documents once');
    await shot(first.bare, '38b-setup-card-scan-after-declined-review');
    await first.bare.close();

    // The same unfinished step on a phone: reachable, in the first screen, no
    // sideways scrolling, and the hash arrival still lands on it.
    const phone = await page.context().newPage();
    await phone.setViewportSize({ width: 390, height: 844 });
    await phone.goto(`${homeRoute}#projectLibraryPanel`);
    const phoneCard = phone.locator('#projectSetupNext');
    await expect(phoneCard).toBeVisible();
    await expect(phoneCard.locator('#projectSetupNextTitle')).toContainText('Scan Documents once');
    await expect(phoneCard.locator('#projectSetupNextTitle')).toBeFocused();
    await expect(phoneCard).toBeInViewport();
    await expect(phoneCard.locator('#projectSetupNextAction')).toBeVisible();
    expect(
      await phone.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true);
    // Partial visibility is not enough on a phone. Once the arrival scroll has
    // settled, the whole step must be usable: its heading below the fixed
    // navigation and its button clear of the floating help and assistant widgets
    // that sit in the bottom corner.
    const settled = () =>
      phone.evaluate(() => {
        const nav = document.querySelector('nav.navbar') as HTMLElement | null;
        const title = document.querySelector('#projectSetupNextTitle') as HTMLElement;
        const button = document.querySelector('#projectSetupNextAction') as HTMLElement;
        return {
          navBottom: nav ? nav.getBoundingClientRect().bottom : 0,
          titleTop: title.getBoundingClientRect().top,
          buttonBottom: button.getBoundingClientRect().bottom,
          viewport: window.innerHeight
        };
      });
    await expect
      .poll(async () => {
        const box = await settled();
        return box.titleTop >= box.navBottom && box.buttonBottom <= box.viewport - 96;
      })
      .toBe(true);
    await shot(phone, '38c-setup-card-390px');
    // Keyboard only: Tab reaches the step's button, Enter opens its review, Escape
    // cancels it and returns focus to the button. Reviewing scans nothing.
    await phone.keyboard.press('Tab');
    await expect(phoneCard.locator('#projectSetupNextAction')).toBeFocused();
    await phone.keyboard.press('Enter');
    const phoneScan = phone.getByRole('dialog', { name: 'Scan this discovery folder once?' });
    await expect(phoneScan).toBeVisible();
    await phone.keyboard.press('Escape');
    await expect(phoneScan).toBeHidden();
    await expect(phoneCard.locator('#projectSetupNextAction')).toBeFocused();
    await phone.close();
  }

  // 2. Now the folder is approved. A Home reopened bare finds the same collection
  //    and goes straight to that root's scan review: never a fresh native pick, a
  //    second root review, or a duplicate grant that would be refused.
  const second = await reopenBare();
  await second.shelf.locator('#projectLibraryAdd').click();
  const rescan = second.bare.getByRole('dialog', { name: 'Scan this discovery folder once?' });
  await expect(rescan).toBeVisible();
  await expect(rescan).toContainText(documents);
  await expect(second.shelf.locator('#projectLibraryStatus')).toContainText('already connected');
  await shot(second.bare, '39-bare-home-reopen-already-connected');
  await second.bare.keyboard.press('Escape');
  await expect(rescan).toBeHidden();
  await expect(second.shelf.locator('#projectLibraryStatus')).toContainText('Scan canceled');
  expect(second.libraryPosts).toEqual([
    '/roots/pick-offer',
    expect.stringMatching(/^\/roots\/[^/]+\/scans\/review$/)
  ]);
  expect(await activeRoots()).toBe(rootsBeforeRegrant + (revokedEarlier ? 1 : 0));
  await second.bare.close();

  expect(songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex'))).toEqual(
    before
  );
});
