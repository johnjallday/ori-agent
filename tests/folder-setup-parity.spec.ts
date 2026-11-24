import { test, expect } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { arrivalState, choiceInventory } from './helpers/workspace-choice-state';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const scenario = process.env.ORI_SEARCH_FIRST_SCENARIO || '';
const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
test.skip(
  !fixture || !/^parity-(files|workspace)-(documents|downloads|custom)$/.test(scenario),
  'needs a fresh, marked profile per direction and folder'
);

test(`${scenario}: both routes converge on one link and one index`, async ({
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
  const [, entry, kind] = scenario.split('-'),
    folder =
      kind === 'downloads' ? 'Downloads' : kind === 'documents' ? 'Documents' : 'Synthetic Project';
  const name = `Archive for ${folder}`;
  const path = (step: string) =>
    `tasks/evidence-folder-backed-search-locations/${browserName}/${scenario}-${step}.png`;
  const sources = async () =>
    await (
      await request.get('/api/file-search/sources', { headers: { 'X-Ori-Search': '1' } })
    ).json();
  const workspaces = async () => (await (await request.get('/api/workspaces')).json()).folders;
  const inspect = () => choiceInventory(request, join(fixture!, 'data'));
  const initial = await inspect();
  const before = await workspaces();
  async function blank() {
    await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
    await page.locator('#wizardNextBtn').click();
  }
  const file = kind === 'downloads' ? 'Ship.txt' : 'Harbor.txt';
  // Files first is the merged Files' Add folder… (PR 2, requirement 39).
  async function choose(side: string) {
    if (side === 'files') {
      await page.goto('/?view=tree');
      await page.locator('[data-tree-add-folder]').click();
      await page
        .getByRole('dialog', { name: 'Add a folder' })
        .locator(`[data-folder-choice="${kind}"]`)
        .click();
    } else {
      await page.goto('/?create=1');
      await blank();
      await page.locator('#workspaceFolderModeAnchored').check();
      await page.locator(`[data-search-folder="${kind}"]`).click();
    }
  }
  await choose(entry);
  const compact = page.locator('dialog.explore-card');
  const consent =
    entry === 'files' ? compact.getByRole('checkbox') : page.locator('#workspaceSearchConsent');
  await expect(consent).toBeVisible();
  await expect(consent).not.toBeChecked();
  await consent.check();
  const review = entry === 'files' ? compact : page.locator('#workspaceCreationReviewPanel');
  if (entry === 'files') await compact.getByLabel('Workspace name').fill(name);
  else {
    await page.locator('#folderNameInput').fill(name);
    await page.locator('#wizardNextBtn').click();
    await page.getByRole('checkbox', { name: 'Create without agents' }).check();
    await page.locator('#wizardNextBtn').click();
    await expect(review).toContainText('Index filenames');
  }
  expect(await sources()).toEqual([]);
  expect(await inspect()).toEqual(initial);
  await page.screenshot({ path: path('review') });
  if (entry === 'files') await review.getByRole('button', { name: 'Create workspace' }).click();
  else {
    await review.getByRole('button', { name: 'Use automatic placement' }).click();
    await review.locator('[data-placement-action="confirm"]').click();
  }
  // "Search this folder in Files" lands in the merged Files, searching only
  // that folder, with nothing about it in the address (requirement 41).
  async function expectFilesScoped() {
    await expect(page).toHaveURL(/\/\?view=tree$/);
    await expect(page.locator('#cockpitFilesSearchScope')).toContainText(`in ${folder}`);
    await page.locator('#cockpitFilesSearchInput').fill(file.replace('.txt', ''));
    await expect(
      page.locator('#cockpitTreePane [data-result-indexed]').filter({ hasText: file }).first()
    ).toContainText(folder);
  }
  await review.getByRole('button', { name: 'Search this folder in Files' }).click();
  await expectFilesScoped();
  const [s] = await sources();
  expect(s.workspace.name).toBe(name);
  const ws = s.workspace;
  const inventory = async () => {
    const all = await workspaces();
    expect(all).toHaveLength(before.length + 1);
    expect(all.filter((w: { id: string }) => w.id === ws.id)).toHaveLength(1);
    const refs = await (await request.get(`/api/workspaces/${ws.id}/directories`)).json();
    expect(refs.directories.map((v: { id: string }) => v.id)).toEqual([ws.link]);
    const current = await sources();
    expect(current).toHaveLength(1);
    expect(current[0]).toMatchObject({
      id: s.id,
      generation: s.generation,
      workspace: { id: ws.id, link: ws.link }
    });
  };
  await inventory();
  const receipt = await inspect();
  const beforeOpen = await arrivalState(request, ws.id);
  expect(beforeOpen).toMatchObject({ agents: [], pending: false, goalRuns: 0, inFlight: false });
  expect(receipt.hires).toEqual(initial.hires);
  await choose(entry === 'files' ? 'workspace' : 'files');
  await compact
    .getByRole('button', {
      name: entry === 'files' ? 'Open workspace' : 'Search this folder in Files'
    })
    .click();
  if (entry === 'files') await expect(page).toHaveURL(new RegExp('/workspaces/' + ws.slug + '$'));
  else await expectFilesScoped();
  await inventory();
  await page.goto('/?home=1');
  await expect(page.locator(`.ws-map-tile[data-ws-id="${ws.id}"]`).first()).toBeVisible();
  await page.screenshot({ path: path('map') });
  await page.goto('/?view=tree');
  await expect(page.locator(`#cockpitTreeNav [data-tree-row="${ws.id}"]`)).toContainText(name);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/workspaces/${ws.slug}`);
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', ws.id);
  const opened = await arrivalState(request, ws.id);
  expect(opened).toEqual(beforeOpen);
  expect(await inspect()).toEqual(receipt);
  writeFileSync(
    path('resources').replace(/\.png$/, '.json'),
    JSON.stringify({ initial, receipt, beforeOpen, opened })
  );
  await page.getByRole('button', { name: `Search ${folder} in Files` }).press('Enter');
  await expectFilesScoped();
  await page.screenshot({ path: path('narrow-files') });
});
