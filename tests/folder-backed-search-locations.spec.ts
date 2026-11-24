import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
test.skip(
  !fixture ||
    !['folder-backed', 'folder-return'].includes(process.env.ORI_SEARCH_FIRST_SCENARIO || ''),
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
  const modal = page.locator('#addFolderModal');
  await expect(modal).toBeVisible();
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#workspaceSearchConsent')).toBeChecked();
  await page.locator('#folderNameInput').fill('Research archive');
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toContainText('Index filenames');
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
  await review.getByRole('button', { name: 'Use automatic placement' }).click();
  const result = page.waitForResponse(
    r => r.url().includes('/create-operations/') && r.url().endsWith('/confirm')
  );
  await review.locator('[data-placement-action="confirm"]').click();
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
  await expect(page).toHaveURL(new RegExp('/workspaces/' + project.workspace.slug + '$'));
  await page.goto('/search');
  await page.getByText('Manage search locations', { exact: true }).click();
  await page.getByRole('button', { name: 'Connect to workspace', exact: true }).click();
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#workspaceSearchConsentLabel')).toContainText(
    'existing filename index'
  );
  await page.locator('#folderNameInput').fill('My downloads');
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toContainText('without rescanning');
  await review.getByRole('button', { name: 'Use automatic placement' }).click();
  await review.locator('[data-placement-action="confirm"]').click();
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
  const dialog = page.getByRole('dialog', { name: 'Connect folder to workspace' });
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
