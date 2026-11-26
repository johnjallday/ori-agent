import { test, expect, type Page, type Request } from '@playwright/test';
import { readFileSync, existsSync, renameSync, mkdtempSync, realpathSync } from 'node:fs';
import { arrivalState, choiceInventory } from './helpers/workspace-choice-state';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
test.skip(
  !fixture ||
    !['folder-backed', 'folder-return', 'folder-assistant'].includes(
      process.env.ORI_SEARCH_FIRST_SCENARIO || ''
    ),
  'requires an isolated split profile'
);
test('Files uses the workspace review; selection alone makes nothing', async ({
  page,
  baseURL,
  request,
  browserName
}) => {
  test.skip(process.env.ORI_SEARCH_FIRST_SCENARIO !== 'folder-backed', 'initial launch only');
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  await installLocalCdn(page);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
  const inventory = async () =>
    (await (await request.get('/api/workspaces')).json()).folders
      .map((w: { id: string }) => w.id)
      .sort();
  const initial = await inventory();
  await page.goto('/?create=1');
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#workspaceFolderModeAnchored').check();
  await page.locator('[data-search-folder="downloads"]').click();
  await expect(page.locator('#workspaceSearchConsent')).toBeVisible();
  await expect(page.locator('#workspaceSearchConsent')).not.toBeChecked();
  await page.locator('#workspaceSearchConsent').check();
  await page.locator('#folderNameInput').fill('Cancelled workspace');
  await page.locator('#wizardNextBtn').click();
  const agentless = page.getByRole('checkbox', { name: 'Create without agents' });
  await expect(agentless).not.toBeChecked();
  await agentless.click();
  await expect(agentless).toBeChecked();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#workspaceCreationReviewPanel')).toContainText('Downloads');
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/downloads-before-cancel.png`
  });
  await page
    .locator('#addFolderModal [data-bs-dismiss="modal"]')
    .filter({ visible: true })
    .first()
    .click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  expect(await inventory()).toEqual(initial);
  // Set up with no workspace yet: /search is still the first-run page, with
  // the folder choices and no list of locations to manage (requirement 42).
  await page.goto('/search');
  await expect(page.locator('#manage')).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Add folder/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Choose another folder…', exact: true }).click();
  const review = page.locator('dialog.explore-card');
  await expect(review.getByRole('heading')).toHaveText(
    'Create a workspace from Synthetic Project?'
  );
  await expect(review.getByRole('checkbox')).not.toBeChecked();
  await review.getByRole('checkbox').check();
  await review.getByLabel('Workspace name').fill('Research archive');
  const sources = async () =>
    await (
      await request.get('/api/file-search/sources', { headers: { 'X-Ori-Search': '1' } })
    ).json();
  const [legacy] = await sources();
  expect(legacy.label).toBe('Downloads');
  expect(legacy.workspace).toBeUndefined();
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/project-review.png`
  });
  const result = page.waitForResponse(
    r => r.url().includes('/create-operations/') && r.url().endsWith('/confirm')
  );
  await review.getByRole('button', { name: 'Create workspace' }).click();
  const response = await result;
  expect(response.status()).toBe(201);
  const body = await response.json();
  expect(body.folder.name).toBe('Research archive');
  expect(body.filename_source_id).toBeTruthy();
  expect((await sources()).map((s: { id: string }) => s.id)).toEqual([
    legacy.id,
    body.filename_source_id
  ]);
  await expect(review.getByRole('button', { name: 'Open workspace', exact: true })).toBeVisible();
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/project-created.png`
  });
  // A workspace exists now: the folder is searched in the merged Files, scoped
  // to it through the tab's own place, with nothing in the address.
  await review.getByRole('button', { name: 'Search this folder in Files' }).click();
  await expect(page).toHaveURL(/\/\?view=tree$/);
  await expect(page.locator('#cockpitFilesSearchScope')).toContainText('in Synthetic Project');
  await page.locator('#cockpitFilesSearchInput').fill('Harbor');
  await expect(page.locator('#cockpitTreePane [data-result-indexed]')).toContainText([
    'Harbor.txt'
  ]);
  // /search itself now opens Files.
  await page.goto('/search');
  await expect(page).toHaveURL(/\/\?view=tree$/);
  await page.goto(`/workspaces/${encodeURIComponent(body.folder.folder_slug)}`);
  await expect(page).toHaveURL(
    new RegExp('/workspaces/' + body.folder.folder_slug + '(?:[/?#]|$)')
  );
  await expect(page.getByText('Research archive', { exact: true }).first()).toBeVisible();
});

test('the assistant links a folder without starting a hire or filename scan', async ({
  page,
  request,
  baseURL,
  browserName
}) => {
  test.skip(
    process.env.ORI_SEARCH_FIRST_SCENARIO !== 'folder-assistant',
    'dedicated established assistant'
  );
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  const api = '/api/personal-assistant';
  let assistant = (await (await request.get(api)).json()).personal_assistant;
  const hired = await request.post(api + '/hire', {
    data: {
      request_id: 'explore-fixture',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help with folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.ok(), await hired.text()).toBe(true);
  assistant = (await hired.json()).personal_assistant;
  const hq = await request.post(api + '/hq', {
    data: {
      request_id: 'explore-hq',
      if_version: assistant.state_version,
      name: 'Fixture HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.ok(), await hq.text()).toBe(true);
  const staffing: string[] = [];
  page.on('request', r => {
    if (r.method() === 'POST' && /\/folder-agent\/(choose|start)$/.test(r.url()))
      staffing.push(r.url());
  });
  await page.goto('/?panel=today&folder=explore');
  const host = page.locator('#assistantExploreFolder');
  const inventory = () => choiceInventory(request, join(fixture!, 'data'));
  const initial = await inventory();
  const audit = () =>
    existsSync(join(fixture!, 'provider-audit.json'))
      ? JSON.parse(readFileSync(join(fixture!, 'provider-audit.json'), 'utf8'))
      : { requests: 0, hits: [] };
  const baseline = audit(); // model configuration may have made its own readiness call
  const pickAlbums = () => host.getByRole('button', { name: 'Choose another folder…' }).click();
  await pickAlbums();
  const chip = page.locator('#personalAssistantActiveFolder');
  const input = page.locator('#personalAssistantInput');
  const picked = page.locator('#personalAssistantPickedFolder');
  await expect(chip).toHaveText(/Albums/);
  await expect(picked).toContainText('What would you like to do with Albums?');
  await expect(page.locator('#personalAssistantFolderPreview')).toBeHidden();
  await expect(page.locator('section.explore-card')).toHaveCount(0);
  await input.fill('What’s in here?');
  await chip.getByRole('button').click();
  await expect(chip).toBeHidden();
  await expect(input).toHaveValue('What’s in here?');
  await page.locator('#personalAssistantAttachMenu').click();
  await page.locator('#personalAssistantFolderChip').click();
  await pickAlbums();
  await expect(chip).toContainText('Albums');
  await expect(input).toHaveValue('What’s in here?');
  await page.screenshot({
    path: `tasks/evidence-assistant-folder-handoff/${browserName}/attachment.png`
  });
  expect(await inventory()).toEqual(initial);

  const homeButton = page.locator('#cockpitCreateWorkspaceBtn');
  const modal = page.locator('#addFolderModal');
  const hint = page.locator('#chatWorkspaceHint');
  const guide = () => picked.getByRole('button', { name: 'Show me where' }).click();
  const cancelWizard = async () => {
    await modal.locator('[data-bs-dismiss="modal"]').filter({ visible: true }).first().click();
    await expect(modal).toBeHidden();
  };
  const showPrefill = async () => {
    await modal.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
    await page.locator('#wizardNextBtn').click();
    await expect(page.locator('#workspaceAnchorPath')).toHaveValue('Albums');
    await expect(page.locator('#workspaceSearchConsent')).toBeVisible();
    await expect(page.locator('#workspaceSearchConsent')).not.toBeChecked();
  };
  const attachmentActions: string[] = [];
  page.on('request', r => {
    if (r.url().endsWith('/folder-context/attachment'))
      attachmentActions.push(r.postDataJSON().action);
  });
  await guide();
  await page.keyboard.press('Escape');
  await expect(hint).toBeHidden();
  await homeButton.press('Enter');
  await expect(modal).toBeVisible();
  await expect(page.locator('#workspaceAnchorPath')).toHaveValue('');
  await cancelWizard();
  await page.locator('#personalAssistantLauncher').click();
  expect(attachmentActions).toEqual([]);
  await guide();
  await expect(homeButton).toBeFocused();
  await expect(hint).toContainText('Start here to use Albums.');
  await expect(modal).toBeHidden();
  await page.screenshot({
    path: `tasks/evidence-assistant-folder-handoff/${browserName}/hint.png`
  });
  await homeButton.press('Enter');
  await showPrefill();
  expect(attachmentActions).toEqual(['resume']);
  await page.locator('#workspaceSearchConsent').scrollIntoViewIfNeeded();
  await page.screenshot({
    path: `tasks/evidence-assistant-folder-handoff/${browserName}/prefill.png`
  });
  await cancelWizard();
  await homeButton.click();
  await expect(page.locator('#workspaceAnchorPath')).toHaveValue('');
  await cancelWizard();
  await page.locator('#personalAssistantLauncher').click();
  await expect(input).toHaveValue('What’s in here?');
  await expect(chip).toContainText('Albums');
  expect(await inventory()).toEqual(initial);

  // Replace only the marked fixture folder and restore it after the refusal.
  const syntheticHome = join(fixture!, 'home');
  const { default_workspace_root: workspaceRoot } = await (
    await request.get('/api/settings/workspace-root')
  ).json();
  expect(realpathSync(join(workspaceRoot, '..'))).toBe(realpathSync(syntheticHome));
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  const albums = join(syntheticHome, 'Albums');
  const parked = join(fixture!, 'original-albums');
  const replacement = mkdtempSync(join(fixture!, 'replacement-'));
  await guide();
  renameSync(albums, parked);
  try {
    renameSync(replacement, albums);
    try {
      const refusal = page.waitForResponse(r => r.url().endsWith('/folder-context/attachment'));
      await homeButton.press('Space');
      expect((await refusal).status()).toBe(409);
      await expect(modal).toBeHidden();
      await page.locator('#personalAssistantLauncher').click();
      await expect(input).toHaveValue('What’s in here?');
      await expect(chip).toBeHidden();
      const repick = page.getByRole('button', { name: 'Choose a folder again' });
      await expect(repick).toBeVisible();
      await page.screenshot({
        path: `tasks/evidence-assistant-folder-handoff/${browserName}/repick.png`
      });
      expect(await inventory()).toEqual(initial);
      await repick.click();
    } finally {
      renameSync(albums, replacement);
    }
  } finally {
    renameSync(parked, albums);
  }
  await pickAlbums();
  await expect(page.locator('#personalAssistantFolderPreview')).toBeHidden();

  const look = picked.getByRole('button', { name: 'Look at names and counts' });
  await look.click();
  await expect(picked).toContainText('one-time local look');
  await picked.getByRole('button', { name: 'Cancel' }).click();
  await expect(input).toHaveValue('What’s in here?');
  await look.click();
  await page.screenshot({
    path: `tasks/evidence-assistant-folder-handoff/${browserName}/consent.png`
  });
  await picked.getByRole('button', { name: 'Preview locally' }).click();
  const preview = page.locator('#personalAssistantFolderPreview');
  await expect(preview).toBeVisible();
  await expect(preview).toContainText('Send shares');
  await expect(page.locator('#personalAssistantReviewFolderSetup')).toBeHidden();
  await expect(input).toHaveValue('What’s in here?');
  expect(audit()).toEqual(baseline);
  // Local preview works even with no model configured. Enable the isolated
  // loopback model only now, for the explicit Send.
  expect(
    (
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'ori-workspace-fixture' }
      })
    ).ok()
  ).toBe(true);
  await page.locator('#personalAssistantSend').click();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'Fixture observation of Albums: Daybreak.wav'
  );
  expect(audit()).toEqual({ requests: baseline.requests + 1, hits: [1] });
  expect(await inventory()).toEqual(initial);
  await page.screenshot({
    path: `tasks/evidence-assistant-folder-handoff/${browserName}/answer.png`
  });

  await page.locator('#personalAssistantAttachMenu').click();
  await page.locator('#personalAssistantFolderChip').click();
  await pickAlbums();
  await guide();
  await homeButton.evaluate(button => {
    button.click();
    button.click();
  });
  await showPrefill();
  await page.locator('#folderNameInput').fill('Music Library');
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).click();
  await page.locator('#wizardNextBtn').click();
  const creation = page.locator('#workspaceCreationReviewPanel');
  await expect(creation).toContainText('Albums');
  expect(await inventory()).toEqual(initial);
  await creation.getByRole('button', { name: 'Use automatic placement' }).click();
  await creation.locator('[data-placement-action="confirm"]').click();
  await expect(creation.getByRole('heading', { name: 'Workspace created' })).toBeVisible();
  await creation.getByRole('button', { name: 'Open workspace', exact: true }).click();
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', /.+/);
  await expect
    .poll(async () => (await inventory()).workspaces.length)
    .toBe(initial.workspaces.length + 1);

  const music = (await page.locator('body').getAttribute('data-workspace-id'))!;
  const links = (await (await request.get(`/api/workspaces/${music}/directories`)).json())
    .directories;
  expect(links.filter((link: { path: string }) => link.path === albums)).toHaveLength(1);
  const createdInventory = await inventory();
  await page.goto('/?panel=today&folder=explore');
  await pickAlbums();
  await expect(picked).toContainText('Albums is linked to Music Library.');
  await expect(picked.getByRole('button', { name: 'Show me where' })).toBeHidden();
  await expect(look).toBeVisible();
  await page.screenshot({
    path: `tasks/evidence-assistant-folder-handoff/${browserName}/linked.png`
  });
  await picked.getByRole('button', { name: 'Open Music Library' }).press('Enter');
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', music);
  expect(await inventory()).toEqual(createdInventory);

  // The Files card still owns compact creation, receipt recovery and optional staffing.
  await page.goto('/?view=tree');
  await page.locator('[data-tree-add-folder]').click();
  await page
    .getByRole('dialog', { name: 'Choose a folder' })
    .getByRole('button', { name: 'Downloads', exact: true })
    .click();
  const card = page.locator('dialog.explore-card');
  await expect(card.getByRole('checkbox')).not.toBeChecked();
  await card.getByLabel('Workspace name').fill('Reading inbox');
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/assistant-explore.png`
  });
  // Lose the reply, not the accepted operation. Recovery must read the
  // journal and never send a second confirmation.
  let confirms = 0,
    reads = 0;
  page.on('request', r => {
    if (r.method() === 'GET' && r.url().includes('/create-operations/')) reads++;
  });
  await page.route('**/create-operations/*/confirm', async route => {
    confirms++;
    const applied = await route.fetch();
    expect(applied.status()).toBe(201);
    await route.fulfill({ status: 503, body: 'Receipt temporarily unavailable' });
  });
  await card.getByRole('button', { name: 'Create workspace' }).click();
  await expect(card.getByRole('heading')).toHaveText('Workspace created');
  expect(confirms).toBe(1);
  expect(reads).toBe(1);
  await expect(card.getByRole('button', { name: 'Search this folder in Files' })).toHaveCount(0);
  expect(staffing).toEqual([]);
  const sources = async () =>
    await (
      await request.get('/api/file-search/sources', { headers: { 'X-Ori-Search': '1' } })
    ).json();
  expect(await sources()).toEqual([]);
  await expect(card.getByRole('button', { name: 'Open workspace' })).toBeVisible();
  const receipt = await inventory();
  await card.getByRole('button', { name: 'Add an agent…' }).click();
  const rosterForm = page.locator('#workspace-detail-add-agent-modal');
  await expect(rosterForm).toBeVisible();
  const id = await page.locator('body').getAttribute('data-workspace-id');
  expect(receipt.workspaces).toContain(id);
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/optional-staffing.png`
  });
  await rosterForm.getByRole('button', { name: 'Cancel' }).click();
  await expect(rosterForm).toBeHidden();
  expect(await inventory()).toEqual(receipt);
  expect(await arrivalState(request, id!)).toMatchObject({
    agents: [],
    filled: 0,
    roles: 1,
    goalRuns: 0,
    pending: false
  });
  // With a workspace, folders are added in Files (requirement 39).
  const addDownloadsFromFiles = async () => {
    await page.goto('/?view=tree');
    await page.locator('[data-tree-add-folder]').click();
    await page
      .getByRole('dialog', { name: 'Choose a folder' })
      .getByRole('button', { name: 'Downloads', exact: true })
      .click();
  };
  await addDownloadsFromFiles();
  const match = page.getByRole('dialog', { name: 'Linked workspaces for Downloads' });
  await expect(match).toContainText('already linked to Reading inbox');
  await match.getByRole('button', { name: 'Open Reading inbox' }).click();
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', id!);
  expect(await sources()).toEqual([]);
  await addDownloadsFromFiles();
  await match.getByRole('button', { name: 'Allow filename search' }).click();
  await expect(page.locator('#cockpitFilesSearchScope')).toContainText('in Downloads');
  expect((await sources())[0].workspace.id).toBe(id);
  // A second exact link leaves the existing Files connection with Reading inbox.
  expect(
    (
      await request.post(`/api/workspaces/${music}/directories`, {
        data: { name: 'Downloads', path: join(syntheticHome, 'Downloads') }
      })
    ).ok()
  ).toBeTruthy();
  const linkedInventory = await inventory();
  const [index] = await sources();
  await page.goto('/?panel=today&folder=explore');
  await host.getByRole('button', { name: 'Downloads', exact: true }).click();
  await expect(picked).toContainText('Choose one to open.');
  await expect(picked.getByRole('button', { name: 'Open Reading inbox' })).toBeVisible();
  await expect(look).toBeVisible();
  await page.screenshot({
    path: `tasks/evidence-assistant-folder-handoff/${browserName}/multiple-links.png`
  });
  await picked.getByRole('button', { name: 'Open Music Library' }).click();
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', music);
  expect(await inventory()).toEqual(linkedInventory);
  expect(await sources()).toMatchObject([
    { id: index.id, workspace: index.workspace, generation: index.generation }
  ]);
  await page.goto('/?panel=today&folder=explore');
  await host.getByRole('button', { name: /Hire an agent or explore a file/ }).click();
  await page.locator('#personalAssistantFolderChips [data-chip="downloads"]').click();
  await expect(page.locator('#personalAssistantFolderAgent')).toContainText('Hire & build');
  expect(staffing.filter(u => u.endsWith('/start'))).toEqual([]);
});

test('Restart keeps workspace identity and does not adopt an independent grant', async ({
  page,
  request,
  baseURL,
  browserName
}) => {
  test.skip(process.env.ORI_SEARCH_FIRST_SCENARIO !== 'folder-return', 'restart only');
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  await installLocalCdn(page);
  const sources = async () =>
    await (
      await request.get('/api/file-search/sources', { headers: { 'X-Ori-Search': '1' } })
    ).json();
  const [legacy, project] = await sources();
  expect(project.workspace.name).toBe('Research archive');
  expect(legacy.label).toBe('Downloads');
  expect(legacy.workspace).toBeUndefined();
  await page.goto('/?create=1');
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#workspaceFolderModeAnchored').check();
  await page.locator('[data-search-folder="custom"]').click();
  await page
    .locator('dialog.explore-card')
    .getByRole('button', { name: 'Open Research archive' })
    .click();
  await expect(page).toHaveURL(new RegExp('/workspaces/' + project.workspace.slug + '$'));
  // The independent grant is a search-only folder: it sits in Files' group at
  // the foot of the tree (PR 2, requirement 38), and connects from there.
  const workspaceIds = async () =>
    ((await (await request.get('/api/workspaces')).json()).folders as { id: string }[])
      .map(w => w.id)
      .sort();
  await page.goto('/?view=tree');
  const group = treeRow(page, 'search-only');
  await expect(group).toContainText('Search-only folders');
  await expect(group).toHaveAttribute('aria-expanded', 'true');
  const legacyRow = treeRow(page, `search-only/${legacy.id}`);
  await expect(legacyRow).toContainText('Downloads');
  await expect(legacyRow).not.toHaveAttribute('aria-expanded', /.*/);
  await expect(legacyRow.locator('[data-folder-search]')).toHaveText('Searchable');
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/search-only-group.png`
  });
  const openLegacyMenu = async () => {
    await legacyRow.hover();
    await legacyRow.locator('[data-tree-menu-for]').click();
  };
  await openLegacyMenu();
  await expect(page.getByRole('menuitem')).toHaveText([
    'Search in this folder',
    'Connect to workspace',
    'Refresh index',
    'Remove from search'
  ]);
  // Cancelling the review links and makes nothing.
  const beforeConnect = await workspaceIds();
  await page.getByRole('menuitem', { name: 'Connect to workspace: Downloads' }).click();
  const review = page.locator('dialog.explore-card');
  await expect(review.getByRole('checkbox', { name: /existing filename index/ })).toBeChecked();
  await review.getByRole('button', { name: 'Cancel' }).click();
  await expect(review).toHaveCount(0);
  expect(await workspaceIds()).toEqual(beforeConnect);
  expect((await sources())[0].workspace).toBeUndefined();
  await openLegacyMenu();
  await page.getByRole('menuitem', { name: 'Connect to workspace: Downloads' }).click();
  await review.getByLabel('Workspace name').fill('My downloads');
  await review.getByRole('button', { name: 'Create workspace' }).click();
  await expect(review.getByRole('button', { name: 'Search this folder in Files' })).toBeVisible();
  const [connected, same] = await sources();
  expect(same.workspace.id).toBe(project.workspace.id);
  expect(connected.id).toBe(legacy.id);
  expect(connected.generation).toBe(legacy.generation);
  expect(connected.count).toBe(legacy.count);
  expect(connected.workspace.name).toBe('My downloads');
  await review.getByRole('button', { name: 'Search this folder in Files' }).click();
  // It left the group (now empty, so gone) and is under its workspace's Linked
  // folders: the same source and index generation, shown, and searched.
  await expect(group).toHaveCount(0);
  const movedRow = treeRow(page, `${connected.workspace.id}/l/${connected.workspace.link}`);
  await expect(movedRow).toBeVisible();
  await expect(movedRow.locator('[data-folder-search]')).toHaveText('Searchable');
  await expect(page.locator('#cockpitFilesSearchScope')).toContainText('in Downloads');
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/legacy-connected.png`
  });
  // Seed two real pre-feature user links to the same separate folder. Neither
  // one is authority to start search until the owner chooses that exact link.
  for (const ws of [connected.workspace, same.workspace]) {
    expect(
      (
        await request.post(`/api/workspaces/${ws.id}/directories`, {
          data: { name: 'Documents', path: join(fixture!, 'home', 'Documents') }
        })
      ).ok()
    ).toBeTruthy();
  }
  // Add folder… in Files (requirement 39): the first-run page's choices, then
  // the same Explore card. Cancelling either step makes nothing.
  const addFolder = page.locator('[data-tree-add-folder]');
  const chooser = page.getByRole('dialog', { name: 'Choose a folder' });
  await addFolder.click();
  await expect(chooser.getByRole('group', { name: 'Folder' }).getByRole('button')).toHaveText([
    'Downloads',
    'Documents',
    'Choose another folder…'
  ]);
  await chooser.getByRole('button', { name: 'Cancel' }).click();
  await expect(chooser).toHaveCount(0);
  await expect(addFolder).toBeFocused();
  const beforeAdd = await workspaceIds();
  const sourcesBeforeAdd = (await sources()).map((s: { id: string }) => s.id);
  await addFolder.click();
  await chooser.getByRole('button', { name: 'Documents', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Linked workspaces for Documents' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toHaveCount(0);
  expect(await workspaceIds()).toEqual(beforeAdd);
  expect((await sources()).map((s: { id: string }) => s.id)).toEqual(sourcesBeforeAdd);
  await addFolder.click();
  await chooser.getByRole('button', { name: 'Documents', exact: true }).click();
  await expect(dialog).toBeVisible();
  const allow = dialog.getByRole('button', { name: 'Allow filename search' });
  await expect(allow).toBeDisabled();
  await expect(dialog.getByRole('combobox').locator('option')).toHaveCount(3);
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/choose-workspace.png`
  });
  await dialog.getByRole('combobox').selectOption({ index: 1 });
  await expect(allow).toBeDisabled();
  await dialog.getByRole('checkbox').check();
  await allow.click();
  // Back in Files, searching the folder just added; nothing new was made.
  await expect(page.locator('#cockpitFilesSearchScope')).toContainText('in Documents');
  expect(await sources()).toHaveLength(3);
  expect(await workspaceIds()).toEqual(beforeAdd);
  const projectID = project.workspace.id;
  await page.goto('/?home=1');
  await expect(page.locator(`.ws-map-tile[data-ws-id="${projectID}"]`).first()).toBeVisible();
  await page.goto('/?view=tree');
  await expect(page.locator(`#cockpitTreeNav [data-tree-row="${projectID}"]`)).toContainText(
    'Research archive'
  );
  // Each linked folder's row says whether it is searchable (PR 2, req. 32-33).
  // Both workspaces link Documents; only the link the owner chose is the
  // source's connection, so only that row is searchable. Matched by ids.
  const documents = (await sources()).find((s: { label: string }) => s.label === 'Documents');
  expect(documents.workspace).toBeTruthy();
  const linkOf = async (workspaceId: string, folder: string) =>
    (
      (await (await request.get(`/api/workspaces/${workspaceId}/directories`)).json())
        .directories as { id: string; path: string }[]
    ).find(d => d.path.endsWith(folder))!.id;
  const otherID =
    documents.workspace.id === connected.workspace.id ? same.workspace.id : connected.workspace.id;
  // Opening another matching workspace must not move the existing index.
  await addFolder.click();
  await chooser.getByRole('button', { name: 'Documents', exact: true }).click();
  const choices = dialog.getByRole('combobox');
  await expect(choices).toBeFocused();
  await expect(dialog.getByRole('button', { name: 'Open workspace' })).toBeDisabled();
  const other = choices.locator('option').filter({ hasText: otherID.slice(0, 8) });
  await choices.selectOption((await other.getAttribute('value'))!);
  await expect(dialog.getByRole('button', { name: 'Search this folder in Files' })).toHaveCount(0);
  await dialog.getByRole('button', { name: /^Open / }).click();
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', otherID);
  const reopened = (await sources()).find((s: { id: string }) => s.id === documents.id);
  expect(reopened.workspace).toEqual(documents.workspace);
  expect(reopened.generation).toBe(documents.generation);
  expect(await workspaceIds()).toEqual(beforeAdd);
  await page.goto('/?view=tree');
  const chosenLink = await linkOf(documents.workspace.id, '/home/Documents');
  const otherLink = await linkOf(otherID, '/home/Documents');
  expect(chosenLink).toBe(documents.workspace.link);
  await openLinkedFolders(page, documents.workspace.id);
  await openLinkedFolders(page, otherID);
  const chosenRow = treeRow(page, `${documents.workspace.id}/l/${chosenLink}`);
  const otherRow = treeRow(page, `${otherID}/l/${otherLink}`);
  await expect(chosenRow.locator('[data-folder-search]')).toHaveAttribute(
    'data-folder-search',
    'searchable'
  );
  await expect(chosenRow.locator('[data-folder-search]')).toHaveText('Searchable');
  await expect(otherRow.locator('[data-folder-search]')).toHaveText('Not searchable');
  // The menu starts with the status: state, file count, when it was indexed.
  await chosenRow.hover();
  await chosenRow.locator('[data-tree-menu-for]').click();
  const status = page.locator('[data-tree-menu-status]');
  await expect(status).toContainText('Searchable');
  await expect(status).toContainText(/\d[\d,]* filenames? · indexed /);
  await expect(page.getByRole('menu')).toHaveAttribute('aria-describedby', 'cockpitTreeMenuStatus');
  await expect(page.getByRole('menuitem')).toHaveText([
    'Search in this folder',
    'Refresh index',
    'Remove from search'
  ]);
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/row-status.png`
  });
  await page.keyboard.press('Escape');
  await expect(page.getByRole('menu')).toHaveCount(0);
  // The other workspace's link to the same folder: already searchable through
  // the chosen one, so Make searchable says so and asks nothing.
  await otherRow.hover();
  await otherRow.locator('[data-tree-menu-for]').click();
  await page.getByRole('menuitem', { name: /^Make searchable/ }).click();
  const elsewhere = page.getByRole('dialog', { name: 'Documents is already searchable' });
  await expect(elsewhere).toContainText(`Files searches it through ${documents.workspace.name}.`);
  await elsewhere.getByRole('button', { name: 'OK' }).click();
  await expect(otherRow.locator('[data-folder-search]')).toHaveText('Not searchable');
  expect(
    (await sources()).find((s: { label: string }) => s.label === 'Documents').workspace.id
  ).toBe(documents.workspace.id);
  // The workspace page's "Search Synthetic Project in Files" opens the merged
  // Files scoped to that folder, with nothing in the address (requirement 41).
  await page.goto(`/workspaces/${encodeURIComponent(project.workspace.slug)}`);
  await expect(page.locator('#workspaceSearchLocations')).toBeVisible();
  await page.getByRole('button', { name: 'Search Synthetic Project in Files' }).press('Enter');
  await expect(page).toHaveURL(/\/\?view=tree$/);
  await expect(page.locator('#cockpitFilesSearchScope')).toContainText('in Synthetic Project');
  await page.locator('#cockpitFilesSearchInput').fill('Harbor');
  const harborHits = page.locator('#cockpitTreePane [data-result-indexed]');
  await expect(harborHits).toContainText(['Harbor.txt']);
  const note = await request.post(`/api/workspaces/${projectID}/notes`, {
    data: { name: 'Keep this note', content: 'Not part of the filename index.' }
  });
  expect(note.ok()).toBeTruthy();
  const noteID = (await note.json()).note.id;
  // Remove from search, from the folder's own row.
  await openLinkedFolders(page, projectID);
  const projectRow = treeRow(page, `${projectID}/l/${project.workspace.link}`);
  const projectMark = projectRow.locator('[data-folder-search]');
  await projectRow.hover();
  await projectRow.locator('[data-tree-menu-for]').click();
  await page.getByRole('menuitem', { name: 'Remove from search: Synthetic Project' }).click();
  await page
    .getByRole('dialog', { name: 'Remove Synthetic Project from search?' })
    .getByRole('button', { name: 'Remove from search' })
    .click();
  await expect
    .poll(async () => (await sources()).map((s: { id: string }) => s.id))
    .not.toContain(project.id);
  // The search that looked only there looks everywhere, without its files.
  await expect(page.locator('#cockpitFilesSearchScope')).toBeHidden();
  await expect(harborHits.filter({ hasText: 'Synthetic Project' })).toHaveCount(0);
  expect(readFileSync(join(fixture!, 'home/Synthetic Project/Harbor.txt'), 'utf8')).toContain(
    'Synthetic project fixture'
  );
  await page.goto('/?view=tree');
  // The tree remembers rows opened earlier in this test; open it only if shut.
  await expandRow(page, projectID);
  await expect(page.locator(`[data-tree-row="${projectID}/n/${noteID}"]`)).toContainText(
    'Keep this note'
  );
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/removed-note-survives.png`
  });
  // Removed from search, the project's own folder reads Not searchable.
  await openLinkedFolders(page, projectID);
  await expect(projectMark).toHaveText('Not searchable');

  // --- managed from its row (PR 2, requirements 33-37) ---------------------
  const ids = async () => (await sources()).map((s: { id: string }) => s.id).sort();
  const before = await ids();
  const openRowMenu = async () => {
    await projectRow.hover();
    await projectRow.locator('[data-tree-menu-for]').click();
    await expect(page.getByRole('menu')).toBeVisible();
  };
  // Make searchable asks first, naming the folder in the page's own words.
  await openRowMenu();
  await expect(page.getByRole('menuitem')).toHaveText(['Make searchable']);
  await page.getByRole('menuitem', { name: 'Make searchable: Synthetic Project' }).click();
  const consent = page.getByRole('dialog', { name: 'Allow Ori to search Synthetic Project?' });
  await expect(consent).toBeVisible();
  await expect(consent).toContainText(
    'Ori will index filenames, locations and dates in this folder on your Mac. It won’t read file contents or give agents access.'
  );
  await expect(consent.getByRole('button', { name: 'Cancel' })).toBeFocused();
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/row-consent.png`
  });
  // Cancel changes nothing.
  await consent.getByRole('button', { name: 'Cancel' }).click();
  await expect(consent).toHaveCount(0);
  await expect(projectRow).toBeFocused();
  expect(await ids()).toEqual(before);
  await expect(projectMark).toHaveText('Not searchable');
  // Allow: indexed, found by name, connected to this very link. Consent is the
  // filename index and nothing else (requirement 35): the only writes it makes
  // are the review and the connect; no agent, hire, Filing or model is asked.
  const writes: string[] = [];
  const noteWrite = (r: Request) => {
    if (r.method() !== 'GET') writes.push(new URL(r.url()).pathname);
  };
  page.on('request', noteWrite);
  await openRowMenu();
  await page.getByRole('menuitem', { name: 'Make searchable: Synthetic Project' }).click();
  await consent.getByRole('button', { name: 'Allow search' }).click();
  await expect(projectMark).toHaveText('Searchable', { timeout: 15000 });
  page.off('request', noteWrite);
  expect(writes.filter(path => path.startsWith('/api/file-search/'))).toEqual([
    '/api/file-search/review-link',
    '/api/file-search/connect'
  ]);
  expect(
    writes.filter(path =>
      /agent|hire|filing|janitor|chat|session|message|model|premise|folder-agent/i.test(path)
    )
  ).toEqual([]);
  const regranted = (await sources()).find(
    (s: { workspace?: { id: string; link: string } }) =>
      s.workspace?.id === projectID && s.workspace.link === project.workspace.link
  );
  expect(regranted).toBeTruthy();
  expect(regranted.id).not.toBe(project.id);
  await page.locator('#cockpitFilesSearchInput').fill('Harbor');
  // A result from this folder says where it lives: Synthetic Project.
  const projectHits = page
    .locator('#cockpitTreePane [data-result-indexed]')
    .filter({ hasText: 'Synthetic Project' });
  await expect(projectHits).toHaveCount(1);
  await expect(projectHits).toContainText('Harbor.txt');
  // Refresh index: indexed again, same source.
  await openRowMenu();
  await expect(page.getByRole('menuitem')).toHaveText([
    'Search in this folder',
    'Refresh index',
    'Remove from search'
  ]);
  await page.getByRole('menuitem', { name: 'Refresh index: Synthetic Project' }).click();
  await expect(projectMark).toHaveText('Searchable', { timeout: 15000 });
  expect((await sources()).map((s: { id: string }) => s.id)).toContain(regranted.id);
  // Remove from search says what stays, and clears only the index.
  await openRowMenu();
  await page.getByRole('menuitem', { name: 'Remove from search: Synthetic Project' }).click();
  const remove = page.getByRole('dialog', { name: 'Remove Synthetic Project from search?' });
  await expect(remove).toContainText(
    'Only this folder’s filename index is cleared. The workspace, its link to the folder, its notes, tickets and files all stay'
  );
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/row-remove.png`
  });
  await remove.getByRole('button', { name: 'Remove from search' }).click();
  await expect(projectMark).toHaveText('Not searchable');
  expect(await ids()).toEqual(before);
  // The open results drop that folder's files (Documents has a Harbor.txt too).
  await expect(projectHits).toHaveCount(0);
  await expect(page.locator(`[data-tree-row="${projectID}/n/${noteID}"]`)).toContainText(
    'Keep this note'
  );
  expect(readFileSync(join(fixture!, 'home/Synthetic Project/Harbor.txt'), 'utf8')).toContain(
    'Synthetic project fixture'
  );
  // Nothing regrants it on the way back from Map or after a reload.
  await page.locator('[data-desk-destination="map"]').click();
  await page.locator('[data-desk-destination="tree"]').click();
  await expect(projectMark).toHaveText('Not searchable');
  await page.reload();
  await openLinkedFolders(page, projectID);
  await expect(projectMark).toHaveText('Not searchable');
  expect(await ids()).toEqual(before);
});

const treeRow = (page: Page, key: string) =>
  page.locator(`#cockpitTreeNav [data-tree-row="${key}"]`);

/** Open a row in Files unless it is open already (the tree remembers it). */
async function expandRow(page: Page, key: string) {
  const row = treeRow(page, key);
  await row.waitFor();
  if ((await row.getAttribute('aria-expanded')) !== 'true') {
    await row.locator('[data-tree-toggle]').click();
  }
  await expect(row).toHaveAttribute('aria-expanded', 'true');
}

/** Open a workspace's row and its Linked folders section in Files. */
async function openLinkedFolders(page: Page, workspaceId: string) {
  await expandRow(page, workspaceId);
  await expandRow(page, `${workspaceId}/s/linked`);
}
