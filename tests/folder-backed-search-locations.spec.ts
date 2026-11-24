import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
test.skip(
  !fixture || process.env.ORI_SEARCH_FIRST_SCENARIO !== 'folder-backed',
  'requires an isolated split profile'
);
test('Files uses the workspace review; selection alone makes nothing', async ({
  page,
  baseURL,
  request,
  browserName
}) => {
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
  expect(await sources()).toEqual([]);
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
  expect((await sources()).map((s: { id: string }) => s.id)).toEqual([body.filename_source_id]);
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
