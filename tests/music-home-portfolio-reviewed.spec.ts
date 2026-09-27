import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
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
test.setTimeout(120_000); // Released-source resolution and clone require network.

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
  request
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
  expect((await json(await request.get(`${base}/roots`))).initialized).toBe(false);
  await shot(page, '19-reviewed-home-before-library-consent');
  await shelf.locator('#projectLibraryInitialize').click();
  await page
    .getByRole('dialog', { name: 'Start a Home project library?' })
    .getByRole('button', { name: 'Start library' })
    .click();
  const grant = page.getByRole('dialog', { name: 'Connect this discovery folder?' });
  await expect(grant).toContainText(documents);
  expect((await json(await request.get(`${base}/roots`))).total_roots).toBe(0);
  await shot(page, '20-reviewed-portfolio-root-grant');
  await grant.getByRole('button', { name: 'Connect folder' }).click();
  const scanPrompt = page.getByRole('dialog', { name: 'Scan the folder now?' });
  await expect(scanPrompt).toBeVisible();
  expect((await json(await request.get(`${base}/roots`))).total_roots).toBe(1);
  await scanPrompt.getByRole('button', { name: 'Review scan' }).click();
  const scan = page.getByRole('dialog', { name: 'Scan this discovery folder once?' });
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
    await expect(connected.getByRole('link', { name: 'Open project workspace' })).toHaveAttribute(
      'href',
      `/workspaces/${committed.workspace_id}`
    );
    await shot(page, '25-reviewed-single-song-connected');
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
    await page.reload();
    const afterRestartView = await json(await request.get(`${base}/projects`));
    await shelf
      .locator('#projectLibraryRows')
      .getByRole('button', { name: 'Review Album-3' })
      .click();
    const linkedDetail = page.getByRole('dialog', { name: 'Album-3' });
    await expect(
      linkedDetail.getByRole('link', { name: 'Open connected workspace' })
    ).toHaveAttribute('href', `/workspaces/${committed.workspace_id}`);
    await linkedDetail.getByRole('button', { name: 'Close' }).click();
    const returnToHome = page.url();
    await shelf
      .locator('#projectLibraryResume')
      .getByRole('button', { name: 'Open Album-3 workspace' })
      .click();
    await page.waitForURL(new RegExp(`/workspaces/${committed.workspace_id}$`));
    await page.goto(returnToHome);
    await expect(shelf.locator('#projectLibraryCount')).toHaveText('5 of 5 projects');
    expect(
      afterRestartView.rows.filter((row: { connection: string }) => row.connection === 'connected')
    ).toHaveLength(1);
    expect((await json(await request.get(`${base}/projects/${albumID}/sessions`))).total).toBe(1);

    // The browser queue is only a navigation aid. Skip and pause must not
    // prepare placeholder workspaces; resuming asks for the *next* song's
    // independent review and confirmation, even after reloading the tab.
    await shelf.getByRole('checkbox', { name: 'Select Album-1 for serial project review' }).check();
    await shelf.getByRole('checkbox', { name: 'Select Album-2 for serial project review' }).check();
    await shelf.locator('#projectLibraryQueueStart').click();
    const firstInQueue = page.getByRole('dialog', { name: 'Song 1 of 2 · Album-1' });
    await expect(firstInQueue).toContainText('Skipping creates nothing');
    await firstInQueue.getByRole('button', { name: 'Skip this song' }).click();
    const secondInQueue = page.getByRole('dialog', { name: 'Song 2 of 2 · Album-2' });
    await secondInQueue.getByRole('button', { name: 'Pause queue' }).click();
    await expect(shelf.locator('#projectLibraryQueueStatus')).toContainText('1 of 2 handled');
    expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspacesBefore.length + 1
    );
    await shot(page, '26-reviewed-queue-paused-after-skip');
    await page.reload();
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
    await shot(page, '27b-confirmed-project-lost-browser-reply');
    await page.unroute(commitRoute);
    await page.reload();
    await expect(shelf.locator('#projectLibraryQueueResume')).toBeVisible();
    await shelf.locator('#projectLibraryQueueResume').click();
    await expect(shelf.locator('#projectLibraryQueueStatus')).toContainText('Choose at least two');
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
        const state = await json(await request.get(`${base}/projects/${savedEntry.id}`));
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
    expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
      workspacesBefore.length + 2
    );
    expect(createHash('sha256').update(readFileSync(alternate)).digest('hex')).toBe(alternateHash);
  }
  expect(songs.map(path => createHash('sha256').update(readFileSync(path)).digest('hex'))).toEqual(
    before
  );
});
