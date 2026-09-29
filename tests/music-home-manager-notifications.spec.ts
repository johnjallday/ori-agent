import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// Run only in a disposable HOME through the reviewed Music journey:
//   ./scripts/music-home-demo.sh test --suite manager-notifications --provider reviewed \
//     --source <clean Music export> [--reaper-source <clean REAPER export>]
// The published Music release is installed through Ori's reviewed registry;
// the optional REAPER export only makes the digest's setup count non-zero.
const sandbox = process.env.ORI_MUSIC_HOME_SANDBOX || '';
const evidenceDir = process.env.ORI_MUSIC_HOME_EVIDENCE_DIR || '';
const reaperSource = process.env.ORI_REAPER_PLUGIN_PATH || '';
test.skip(
  process.env.ORI_MUSIC_HOME_ACCEPTANCE !== '1' ||
    process.env.ORI_MUSIC_PROVIDER_MODE !== 'reviewed' ||
    !sandbox,
  'requires music-home-demo.sh test --suite manager-notifications --provider reviewed'
);
test.setTimeout(240_000);

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

// The page scrolls smoothly unless reduced motion is on, so wait for the
// arrival scroll to settle with the heading on screen and not under the navbar.
async function expectHeadingUncovered(page: Page, selector: string) {
  await page.evaluate(
    () =>
      new Promise<void>(resolve => {
        let last = -1;
        let still = 0;
        const tick = () => {
          still = window.scrollY === last ? still + 1 : 0;
          last = window.scrollY;
          if (still >= 3) resolve();
          else setTimeout(tick, 100);
        };
        tick();
      })
  );
  await expect
    .poll(
      () =>
        page.evaluate(sel => {
          const heading = document.querySelector(sel);
          if (!heading) return 'missing';
          const box = heading.getBoundingClientRect();
          if (box.top < 0 || box.bottom > innerHeight) return 'offscreen';
          const hit = document.elementFromPoint(box.left + 4, box.top + box.height / 2);
          return hit && heading.contains(hit) ? 'uncovered' : 'covered';
        }, selector),
      { timeout: 10_000 }
    )
    .toBe('uncovered');
}

test('a completed scan leaves a model-free digest on the Home suggestions shelf', async ({
  page,
  request
}) => {
  // Five REAPER songs and one Ableton set: the digest should report six
  // projects, of which only the .rpp folders can be set up (with REAPER).
  const documents = join(sandbox, 'Documents');
  const projectFiles: string[] = [];
  for (let i = 0; i < 5; i++) {
    const folder = join(documents, `Album-${i}`);
    mkdirSync(folder, { recursive: true, mode: 0o750 });
    const song = join(folder, 'Song.rpp');
    writeFileSync(song, `fixture-${i}; metadata-only`, { mode: 0o600 });
    projectFiles.push(song);
  }
  const liveFolder = join(documents, 'Live Set');
  mkdirSync(liveFolder, { recursive: true, mode: 0o750 });
  const liveSet = join(liveFolder, 'Arrangement.als');
  writeFileSync(liveSet, 'fixture-live; metadata-only', { mode: 0o600 });
  projectFiles.push(liveSet);
  const before = projectFiles.map(hash);

  await json(await request.post('/api/onboarding/skip'));
  const relationship = (await json(await request.get('/api/personal-assistant')))
    .personal_assistant;
  await json(
    await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'notifications-hire',
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
      data: { request_id: 'notifications-hq', if_version: hired.state_version, name: 'My HQ' }
    })
  );
  const offer = (
    await json(
      await request.post('/api/personal-assistant/folder-digest/scan', {
        data: { chip: 'documents' }
      })
    )
  ).offer;
  expect(offer).toMatchObject({ status: 'pending', capability: { setup_source: 'portfolio' } });
  await page.goto('/?panel=today&folder=show');
  page.on('dialog', dialog => dialog.accept()); // Only inside the disposable HOME.
  await page
    .locator('#personalAssistantFolderOffer')
    .locator('[data-folder-action="setup"]')
    .click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible({ timeout: 90_000 });
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await creator.getByRole('button', { name: 'Review →' }).click();
  await creator.locator('#createFolderBtn').click();
  await page.waitForURL(/\/assistant\?folder_offer_id=/, { timeout: 30_000 });
  const homes = (await json(await request.get('/api/workspaces'))).folders.filter(
    (row: { kind: string }) => row.kind === 'group'
  );
  expect(homes).toHaveLength(1);
  const homeID = homes[0].id;
  const base = `/api/workspaces/${homeID}/assistant-program/library`;

  if (reaperSource) {
    // A paired run installs the clean REAPER export before the scan so the
    // digest can count songs an installed integration could set up.
    await json(
      await request.post('/api/plugins/install', {
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
        data: { source: reaperSource, confirm: true }
      })
    );
    await json(
      await request.post('/api/plugins/reaper-plugin/enable', {
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      })
    );
  }
  const workspacesBeforeScan = (await json(await request.get('/api/workspaces'))).folders.length;

  // Before any scan there is no digest and nothing on the shelf to review.
  expect(await json(await request.get(`${base}/summary`))).toMatchObject({
    initialized: false,
    digest: null,
    ready_proposals: 0
  });
  const shelf = page.locator('#projectLibraryPanel');
  await page.reload();
  await expect(shelf.locator('#projectLibraryStatus')).toContainText('No library yet');
  await shelf.locator('#projectLibraryInitialize').click();
  await page
    .getByRole('dialog', { name: 'Start a Home project library?' })
    .getByRole('button', { name: 'Start library' })
    .click();
  await page
    .getByRole('dialog', { name: 'Connect this discovery folder?' })
    .getByRole('button', { name: 'Connect folder' })
    .click();
  await page
    .getByRole('dialog', { name: 'Scan the folder now?' })
    .getByRole('button', { name: 'Review scan' })
    .click();
  // Reviewing the scan is not a completed scan: still no digest.
  expect((await json(await request.get(`${base}/summary`))).digest).toBeNull();
  await page
    .getByRole('dialog', { name: 'Scan this discovery folder once?' })
    .getByRole('button', { name: 'Scan metadata' })
    .click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('6 of 6 projects');

  const summary = await json(await request.get(`${base}/summary`));
  expect(summary).toMatchObject({
    initialized: true,
    ready_proposals: 0,
    route: `/workspaces/${homes[0].folder_slug}/assistant#projectLibraryProposals`,
    digest: { coverage: 'complete', projects: 6, new: 6, unavailable: 0 }
  });
  expect(JSON.stringify(summary)).not.toContain(documents);
  const digestLine = shelf.locator('#projectLibraryDigest');
  await expect(digestLine).toBeVisible();
  if (reaperSource) {
    expect(summary.digest).toMatchObject({ activatable: 5, unsupported_format: 1 });
    await expect(digestLine).toHaveText(
      /^Scanned Documents on .+: 6 projects, 6 new, 5 can be set up, 1 unsupported format\.$/
    );
  } else {
    expect(summary.digest).toMatchObject({
      activatable: 0,
      unsupported_format: 0,
      setup_note: 'project_provider_unavailable'
    });
    await expect(digestLine).toHaveText(
      /^Scanned Documents on .+: 6 projects, 6 new, project setup needs a compatible installed integration\.$/
    );
  }
  const suggestions = shelf.locator('#projectLibraryProposals');
  await expect(suggestions).toContainText('No Manager suggestions to review for this scan.');
  await suggestions.scrollIntoViewIfNeeded();
  await shot(page, '01-scan-digest-desktop');
  await page.setViewportSize({ width: 390, height: 844 });
  await suggestions.scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await shot(page, '02-scan-digest-390');
  await page.setViewportSize({ width: 1280, height: 900 });

  // The digest and summary never create workspaces or touch project files.
  expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
    workspacesBeforeScan
  );
  expect(projectFiles.map(hash)).toEqual(before);

  // The Home map badge and the Action Center card appear only when something
  // is ready, and both only navigate to the Home's suggestions shelf.
  const homeName = homes[0].name;
  const setTheme = async (theme: 'light' | 'dark') => {
    await page.evaluate(value => localStorage.setItem('ori-theme', value), theme);
    await page.reload();
  };
  if (!reaperSource) {
    expect((await json(await request.get('/api/action-center/library'))).items).toHaveLength(0);
    await page.goto('/');
    await expect(page.locator(`.ws-map-district[data-group-id="${homeID}"]`)).toBeVisible();
    await expect(page.locator('[data-library-badge]')).toHaveCount(0);
    return;
  }
  const cards = (await json(await request.get('/api/action-center/library'))).items;
  expect(cards).toEqual([
    expect.objectContaining({
      home_id: homeID,
      home_name: homeName,
      route: summary.route,
      new: 6,
      activatable: 5,
      ready_proposals: 0
    })
  ]);
  await page.goto('/');
  const badge = page.locator(`[data-library-badge="${homeID}"]`);
  await expect(badge).toHaveText('6 new · 5 ready');
  await expect(badge).toHaveAccessibleName(
    `${homeName} library: 6 new projects, 5 ready to set up. Open the suggestions shelf.`
  );
  await shot(page, '03-map-badge-light');
  await setTheme('dark');
  await expect(badge).toBeVisible();
  await shot(page, '04-map-badge-dark');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('[data-map-fit]').click();
  // Fit all must frame the badge inside the map, not above its top edge.
  const canvasBox = await page.locator('.ws-map-canvas').first().boundingBox();
  const badgeBox = await badge.boundingBox();
  expect(canvasBox && badgeBox).toBeTruthy();
  expect(badgeBox!.y).toBeGreaterThanOrEqual(canvasBox!.y);
  expect(badgeBox!.x).toBeGreaterThanOrEqual(canvasBox!.x);
  expect(badgeBox!.x + badgeBox!.width).toBeLessThanOrEqual(canvasBox!.x + canvasBox!.width);
  await shot(page, '05-map-badge-390-dark');
  await page.setViewportSize({ width: 1280, height: 900 });
  await setTheme('light');
  await badge.click();
  await page.waitForURL(/\/assistant#projectLibraryProposals$/);
  await expect(page.locator('#projectLibraryProposalsTitle')).toBeFocused();
  await expect(page.locator('#projectLibraryDigest')).toContainText('5 can be set up');
  await expectHeadingUncovered(page, '#projectLibraryProposalsTitle');
  await shot(page, '06-badge-arrival-focus');

  await page.goto('/action-center');
  const librarySection = page.locator('#action-center-library');
  await expect(librarySection).toBeVisible();
  await expect(librarySection).toContainText(homeName);
  await expect(librarySection).toContainText('6 new · 5 ready to set up');
  await expect(librarySection.getByRole('button', { name: /Dismiss|Snooze|Resolve/ })).toHaveCount(
    0
  );
  await shot(page, '07-action-center-card-light');
  await setTheme('dark');
  await expect(librarySection).toBeVisible();
  await shot(page, '08-action-center-card-dark');
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await shot(page, '09-action-center-card-390-dark');
  await page.setViewportSize({ width: 1280, height: 900 });
  await setTheme('light');
  await librarySection.getByRole('link', { name: `Open ${homeName} suggestions shelf` }).click();
  await page.waitForURL(/\/assistant#projectLibraryProposals$/);
  await expect(page.locator('#projectLibraryProposalsTitle')).toBeFocused();
  await expectHeadingUncovered(page, '#projectLibraryProposalsTitle');

  // Neither surface changed anything.
  expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
    workspacesBeforeScan
  );
  expect(projectFiles.map(hash)).toEqual(before);
});
