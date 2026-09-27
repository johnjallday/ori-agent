import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// Run only through the Music candidate sandbox. Five disposable project folders
// under its HOME make the actual chip scanner offer a portfolio. No native
// picker, browser path, transport mock, or user's real Documents is involved.
const enabled = process.env.ORI_MUSIC_HOME_ACCEPTANCE === '1';
const sandbox = process.env.ORI_MUSIC_HOME_SANDBOX || '';
const evidenceDir = process.env.ORI_MUSIC_HOME_EVIDENCE_DIR || '';
test.skip(!enabled || !sandbox, 'requires the isolated music-home-demo.sh portfolio suite');

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function shot(page: Page, name: string) {
  mkdirSync(evidenceDir, { recursive: true });
  await page.screenshot({ path: join(evidenceDir, `${name}.png`) });
}

function hash(path: string) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

test('a local-only provider cannot resolve a portfolio offer or grant a discovery root', async ({
  page,
  request
}) => {
  const documents = join(sandbox, 'Documents');
  const songs: string[] = [];
  for (let i = 0; i < 5; i++) {
    const folder = join(documents, `Album-${i}`);
    mkdirSync(folder, { recursive: true, mode: 0o750 });
    const song = join(folder, 'Song.rpp');
    writeFileSync(song, `fixture-${i}; read only metadata`, { mode: 0o600 });
    songs.push(song);
  }
  const before = songs.map(hash);
  await json(await request.post('/api/onboarding/skip'));
  const relationship = (await json(await request.get('/api/personal-assistant')))
    .personal_assistant;
  expect(relationship.state).toBe('needs_hire');
  await json(
    await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'portfolio-hire',
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
      data: { request_id: 'portfolio-hq', if_version: hired.state_version, name: 'My HQ' }
    })
  );
  const digest = await json(await request.get('/api/personal-assistant/folder-digest'));
  expect(digest.folder_digest.chips).toEqual(
    expect.arrayContaining([expect.objectContaining({ id: 'documents' })])
  );
  const scanned = await json(
    await request.post('/api/personal-assistant/folder-digest/scan', {
      data: { chip: 'documents' }
    })
  );
  const offer = scanned.offer;
  expect(offer).toMatchObject({
    status: 'pending',
    portfolio: { projects: 5 },
    capability: { setup_source: 'portfolio' }
  });
  const beforeHome = (await json(await request.get('/api/workspaces'))).folders;
  expect(beforeHome).toHaveLength(1); // My HQ, not an implicit music Home.
  await page.goto('/?panel=today&folder=show');
  const offerCard = page.locator('#personalAssistantFolderOffer');
  await expect(offerCard).toContainText('5 music projects');
  await shot(page, '12-portfolio-observed-offer');
  await offerCard.locator('[data-folder-action="setup"]').click();
  // This local clean-candidate installation is not a *published reviewed
  // release*. The offer setup must fail closed without contacting GitHub or
  // silently treating a same-name local plugin as the reviewed release.
  await expect(page.locator('#personalAssistantFolderOfferError')).toContainText(
    'Home provider is unavailable'
  );
  expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(1);
  await page.getByRole('button', { name: 'Close personal assistant' }).click();
  await page.getByRole('button', { name: 'Tree', exact: true }).click();
  await page.locator('[data-tree-new-group]').click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  const music = creator.locator('.workspace-group-template-option', {
    hasText: 'Music Production Home'
  });
  await music.locator('input').check();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await expect(creator.locator('.ws-role-row[data-role-id="portfolio_manager"]')).toContainText(
    'Music Portfolio Manager'
  );
  await creator.getByRole('button', { name: 'Review →' }).click();
  await shot(page, '13-portfolio-home-review');
  await creator.locator('#createFolderBtn').click();
  await expect(creator).toBeHidden();
  const homes = (await json(await request.get('/api/workspaces'))).folders.filter(
    (row: { kind: string; id: string }) => row.kind === 'group'
  );
  expect(homes).toHaveLength(1);
  const homeID = homes[0].id;
  // Manual creation cannot repair the missing reviewed release by claiming an
  // arbitrary Home ID. The offer and its source selection remain unavailable.
  const resolve = await request.post(
    `/api/personal-assistant/folder-digest/offers/${offer.id}/resolve`,
    { data: { home_id: homeID, request_id: 'portfolio-attempt-unreviewed-home' } }
  );
  expect(resolve.status()).toBe(409);
  const pending = (await json(await request.get('/api/personal-assistant/folder-digest')))
    .folder_digest.offer;
  expect(pending).toMatchObject({ status: 'awaiting_outcome' });
  const base = `/api/workspaces/${homeID}/assistant-program/library`;
  await page.goto(`/workspaces/${homes[0].folder_slug}/assistant`);
  const shelf = page.locator('#projectLibraryPanel');
  await expect(shelf).toBeVisible();
  await expect(shelf.locator('#projectLibraryStatus')).toContainText('No library yet');
  await shot(page, '14-portfolio-home-without-inherited-consent');
  await shelf.locator('#projectLibraryInitialize').click();
  await page
    .getByRole('dialog', { name: 'Start a Home project library?' })
    .getByRole('button', { name: 'Start library' })
    .click();
  await expect(shelf.locator('#projectLibraryContent')).toBeVisible();
  await expect(shelf.locator('#projectLibraryAdd')).toBeDisabled();
  expect((await json(await request.get(`${base}/roots`))).total_roots).toBe(0);
  expect((await json(await request.get(`${base}/projects`))).total).toBe(0);
  await shot(page, '15-portfolio-home-empty-library-after-refusal');
  expect(songs.map(hash)).toEqual(before);
});
