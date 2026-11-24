import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
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
  const before = await workspaces();
  async function blank() {
    await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
    await page.locator('#wizardNextBtn').click();
  }
  async function choose(side: string) {
    if (side === 'files') {
      await page.goto('/search');
      await page.getByRole('button', { name: /Add folder/ }).click();
      await page.locator(`[data-kind="${kind}"]`).filter({ visible: true }).click();
    } else {
      await page.goto('/?create=1');
      await blank();
      await page.locator('#workspaceFolderModeAnchored').check();
      await page.locator(`[data-search-folder="${kind}"]`).click();
    }
  }
  await choose(entry);
  if (entry === 'files') await blank();
  const consent = page.locator('#workspaceSearchConsent');
  await expect(consent).toBeVisible();
  if (entry === 'workspace') {
    await expect(consent).not.toBeChecked();
    await consent.check();
  } else await expect(consent).toBeChecked();
  await page.locator('#folderNameInput').fill(name);
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toContainText('Index filenames');
  expect(await sources()).toEqual([]);
  await page.screenshot({ path: path('review') });
  await review.getByRole('button', { name: 'Use automatic placement' }).click();
  await review.locator('[data-placement-action="confirm"]').click();
  await review.getByRole('button', { name: 'Search this folder in Files' }).click();
  await expect(page.locator('#title')).toHaveText(folder);
  await expect(page.locator('#hits')).toContainText(
    kind === 'downloads' ? 'Ship.txt' : 'Harbor.txt'
  );
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
  await choose(entry === 'files' ? 'workspace' : 'files');
  if (entry === 'files') await expect(page).toHaveURL(new RegExp('/workspaces/' + ws.slug + '$'));
  else await expect(page.locator('#title')).toHaveText(folder);
  await inventory();
  await page.goto('/?home=1');
  await expect(page.locator(`.ws-map-tile[data-ws-id="${ws.id}"]`).first()).toBeVisible();
  await page.screenshot({ path: path('map') });
  await page.goto('/?view=tree');
  await expect(page.locator(`#cockpitTreeNav [data-tree-row="${ws.id}"]`)).toContainText(name);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/workspaces/${ws.slug}`);
  await page.getByRole('button', { name: `Search ${folder} in Files` }).press('Enter');
  await expect(page.locator('#scope')).toHaveValue(s.id);
  await expect(page.locator('#hits')).toContainText(
    kind === 'downloads' ? 'Ship.txt' : 'Harbor.txt'
  );
  await page.screenshot({ path: path('narrow-files') });
});
