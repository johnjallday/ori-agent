import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
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
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
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
  await page.goto('/search');
  await page.getByRole('button', { name: /Add folder/ }).click();
  await page.getByRole('button', { name: 'Choose another folder…', exact: true }).click();
  const review = page.locator('dialog.explore-card');
  await expect(review.getByRole('heading')).toHaveText('Explore Synthetic Project');
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
  await review.getByRole('button', { name: 'Search this folder in Files' }).click();
  await expect(page).toHaveURL(/\/search$/);
  await expect(page.locator('#title')).toHaveText('Synthetic Project');
  await expect(page.locator('#hits')).toContainText('Harbor.txt');
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
  await host.getByRole('button', { name: 'Documents', exact: true }).click();
  const card = host.locator('section.explore-card');
  await expect(card.getByRole('heading')).toHaveText('Explore Documents');
  await card.getByRole('button', { name: 'Cancel' }).click();
  await host.getByRole('button', { name: 'Downloads', exact: true }).click();
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
  await card.getByRole('button', { name: 'Open workspace' }).click();
  await expect(page).toHaveURL(/\/workspaces\//);
  const id = await page.locator('body').getAttribute('data-workspace-id');
  await page.goto('/search');
  await page.getByRole('button', { name: /Add folder/ }).click();
  await page.getByRole('button', { name: 'Downloads', exact: true }).click();
  const match = page.locator('dialog.explore-card');
  await expect(match).toContainText('already linked to Reading inbox');
  await match.getByRole('button', { name: 'Open workspace' }).click();
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', id!);
  expect(await sources()).toEqual([]);
  await page.goto('/search');
  await page.getByRole('button', { name: /Add folder/ }).click();
  await page.getByRole('button', { name: 'Downloads', exact: true }).click();
  await match.getByRole('button', { name: 'Allow filename search' }).click();
  await expect(page.locator('#title')).toHaveText('Downloads');
  expect((await sources())[0].workspace.id).toBe(id);
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
  await page.locator('dialog.explore-card').getByRole('button', { name: 'Open workspace' }).click();
  await expect(page).toHaveURL(new RegExp('/workspaces/' + project.workspace.slug + '$'));
  await page.goto('/search');
  await page.getByText('Manage search locations', { exact: true }).click();
  await page.getByRole('button', { name: 'Connect to workspace', exact: true }).click();
  const review = page.locator('dialog.explore-card');
  await expect(review.getByRole('checkbox', { name: /existing filename index/ })).toBeChecked();
  await review.getByLabel('Workspace name').fill('My downloads');
  await review.getByRole('button', { name: 'Create workspace' }).click();
  await expect(review.getByRole('button', { name: 'Search this folder in Files' })).toBeVisible();
  const [connected, same] = await sources();
  expect(same.workspace.id).toBe(project.workspace.id);
  expect(connected.id).toBe(legacy.id);
  expect(connected.generation).toBe(legacy.generation);
  expect(connected.workspace.name).toBe('My downloads');
  await review.getByRole('button', { name: 'Search this folder in Files' }).click();
  await expect(page.locator('#title')).toHaveText('Downloads');
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
  await page.getByRole('button', { name: /Add folder/ }).click();
  await page.getByRole('button', { name: 'Documents', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Explore Documents' });
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
  await expect(page.locator('#title')).toHaveText('Documents');
  expect(await sources()).toHaveLength(3);
  const projectID = project.workspace.id;
  await page.goto('/?home=1');
  await expect(page.locator(`.ws-map-tile[data-ws-id="${projectID}"]`).first()).toBeVisible();
  await page.goto('/?view=tree');
  await expect(page.locator(`#cockpitTreeNav [data-tree-row="${projectID}"]`)).toContainText(
    'Research archive'
  );
  await page.goto('/search');
  await page.locator('#manage summary').click();
  await page.getByRole('button', { name: 'Open Research archive', exact: true }).first().click();
  await expect(page.locator('#workspaceSearchLocations')).toBeVisible();
  await page.getByRole('button', { name: 'Search Synthetic Project in Files' }).press('Enter');
  await expect(page.locator('#title')).toHaveText('Synthetic Project');
  await expect(page.locator('#hits')).toContainText('Harbor.txt');
  const note = await request.post(`/api/workspaces/${projectID}/notes`, {
    data: { name: 'Keep this note', content: 'Not part of the filename index.' }
  });
  expect(note.ok()).toBeTruthy();
  const noteID = (await note.json()).note.id;
  await page.locator('#manage summary').click();
  await page
    .locator('#sources li')
    .filter({ hasText: 'Synthetic Project' })
    .getByRole('button', { name: 'Remove from search' })
    .click();
  await expect
    .poll(async () => (await sources()).map((s: { id: string }) => s.id))
    .not.toContain(project.id);
  await expect(page.locator('#hits')).not.toContainText('Harbor.txt');
  expect(readFileSync(join(fixture!, 'home/Synthetic Project/Harbor.txt'), 'utf8')).toContain(
    'Synthetic project fixture'
  );
  await page.goto('/?view=tree');
  const row = page.locator(`#cockpitTreeNav [data-tree-row="${projectID}"]`);
  await row.locator('[data-tree-toggle]').click();
  await expect(page.locator(`[data-tree-row="${projectID}/n/${noteID}"]`)).toContainText(
    'Keep this note'
  );
  await page.screenshot({
    path: `tasks/evidence-folder-backed-search-locations/${browserName}/removed-note-survives.png`
  });
});
