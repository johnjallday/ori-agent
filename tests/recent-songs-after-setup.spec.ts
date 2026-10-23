import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdirSync, utimesSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { revealFolderSetupDetails } from './helpers/folder-setup-card';

// Recent songs stay in the single setup card, alongside its canonical library
// link and disclosed receipt. Closed drawers do not read shortcuts or navigate
// on late replies; reopening reads the saved result, never repeats setup.
// Three real collections exercise closure, completed receipts, failures and
// project-file choice. The Home library retains the same Recently saved order.
//
// Run it against a fresh isolated sandbox with a model provider:
//
//   CODEX_HOME="$HOME/.codex" ./scripts/demo-server.sh 8932     # prints SANDBOX=…
//   ./scripts/e2e.sh --port 8932 --env ORI_RECENT_SONGS_SANDBOX=<sandbox dir> tests/recent-songs-after-setup.spec.ts
//
// The first run downloads the reviewed REAPER integration and the Home
// provider. No REAPER app is launched.
const sandbox = process.env.ORI_RECENT_SONGS_SANDBOX || '';
test.skip(!sandbox, 'requires ORI_RECENT_SONGS_SANDBOX (the demo sandbox directory)');
test.describe.configure({ mode: 'serial' });
test.setTimeout(360_000);

const folderApi = '/api/personal-assistant/folder-digest';
const ASSISTANT = 'REAPER Assistant';
const RECENT_QUERY = /\/assistant-program\/library\/projects\?sort=last_saved/;

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

// Local noon `days` ago, or a minute ago for today: the labels count calendar days.
function savedAt(days: number): Date {
  if (days === 0) return new Date(Date.now() - 60_000);
  const day = new Date();
  day.setHours(12, 0, 0, 0);
  day.setDate(day.getDate() - days);
  return day;
}

// Song folders in one chip, each project file saved `days[i]` days ago. The
// first `twoFiles` songs get a second, older project file. Every day is unique
// across collections, so the order never rests on a tie.
function seedCollection(chip: string, prefix: string, days: number[], twoFiles = 0) {
  days.forEach((ago, index) => {
    const name = `${prefix} ${String(index + 1).padStart(2, '0')}`;
    const folder = join(sandbox, chip, name);
    const project = join(folder, `${name}.rpp`);
    if (existsSync(project)) return;
    mkdirSync(join(folder, 'Media'), { recursive: true, mode: 0o750 });
    const at = savedAt(ago);
    writeFileSync(project, '<REAPER_PROJECT 0.1 "7.0"\n>\n', { mode: 0o600 });
    utimesSync(project, at, at);
    if (index < twoFiles) {
      const alt = join(folder, `${name} alt.rpp`);
      const earlier = new Date(at.getTime() - 60 * 60 * 1000);
      writeFileSync(alt, '<REAPER_PROJECT 0.1 "7.0"\n>\n', { mode: 0o600 });
      utimesSync(alt, earlier, earlier);
    }
    writeFileSync(join(folder, `${name}.rpp-bak`), '<REAPER_PROJECT>\n', { mode: 0o600 });
    writeFileSync(join(folder, 'Media', 'take-0.wav'), 'RIFF', { mode: 0o600 });
  });
}

async function scan(request: APIRequestContext, chip: string) {
  const offer = (await ok(await request.post(`${folderApi}/scan`, { data: { chip } }))).offer;
  expect(offer?.portfolio, `the ${chip} scan is a collection`).toBeTruthy();
  return offer as { id: string };
}

async function openCard(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel?.open?.(
      document.getElementById('personalAssistantLauncher')
    )
  );
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  await card.scrollIntoViewIfNeeded();
  return card;
}

async function offerStatus(request: APIRequestContext, offerID: string) {
  const digest = await ok(
    await request.get(`${folderApi}?offer_id=${encodeURIComponent(offerID)}`)
  );
  return String(digest.folder_digest?.offer?.status || '');
}

const songRows = (page: Page) =>
  page
    .locator('#personalAssistantFolderSongs li')
    .evaluateAll(items =>
      items.map(item => [
        item.querySelector('strong')?.textContent || '',
        item.querySelector('small')?.textContent || ''
      ])
    );

let modelReady = false;
let libraryURL = '';

test('closing a running collection stops background shortcut reads without stopping setup', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip');
  const relationship = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  if (relationship.state === 'needs_hire') {
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'recent-songs-hire',
          if_version: relationship.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my songs moving.',
          focus_areas: ['plan_my_day']
        }
      })
    );
    const hired = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
    await ok(
      await request.post('/api/personal-assistant/hq', {
        data: { request_id: 'recent-songs-hq', if_version: hired.state_version, name: 'My HQ' }
      })
    );
  }
  modelReady = (
    await request.post('/api/settings/system-model', {
      data: { provider: 'codex', model: 'gpt-5.6-luna' }
    })
  ).ok();
  test.skip(!modelReady, 'no model provider is available on this machine');

  seedCollection('Documents', 'Song', [3, 5, 8, 13, 21, 34, 55, 89, 144, 400]);
  const offer = await scan(request, 'documents');
  const asked: string[] = [];
  page.on('request', r => RECENT_QUERY.test(r.url()) && asked.push(r.url()));

  const card = await openCard(page);
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  await page.locator('#personalAssistantClose').click();
  const readsAtClose = asked.length;
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect
    .poll(() => offerStatus(request, offer.id), { timeout: 300_000, intervals: [2_000] })
    .toBe('resolved');
  expect(asked, 'a closed drawer starts no further shortcut reads').toHaveLength(readsAtClose);
  await expect(page.locator('#folderSetupRunModal')).toHaveCount(0);
  await page.reload();
  await page.waitForTimeout(2_000);
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  expect(asked).toHaveLength(readsAtClose);
});

test('closing the completed drawer keeps the receipt and never restores a nested modal', async ({
  page,
  request
}) => {
  test.skip(!modelReady, 'no model provider is available on this machine');
  seedCollection('Desktop', 'Mix', [10, 16, 26, 42, 68, 110]);
  await scan(request, 'desktop');
  const card = await openCard(page);
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  await expect(card.locator('#personalAssistantFolderSongs li')).toHaveCount(6, {
    timeout: 300_000
  });
  expect(await songRows(page)).toEqual([
    ['Song 01', 'Saved 3 days ago'],
    ['Song 02', 'Saved 5 days ago'],
    ['Song 03', 'Saved 8 days ago'],
    ['Mix 01', 'Saved 10 days ago'],
    ['Song 04', 'Saved 13 days ago'],
    ['Mix 02', 'Saved 2 weeks ago']
  ]);
  const more = card.locator('#personalAssistantFolderDetails');
  await expect(more).toBeVisible();
  await expect(more).not.toHaveAttribute('open', '');
  await expect(card.locator('#personalAssistantFolderReceipt')).toBeHidden();
  await revealFolderSetupDetails(card);
  await expect(card.locator('#personalAssistantFolderReceipt')).toContainText(
    'Listed 6 music projects in Desktop'
  );
  await expect(card.getByRole('link', { name: 'Browse all 16 songs' })).toBeVisible();
  await card.locator('[data-folder-run-open]').first().focus();
  await page.keyboard.press('Escape');
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await page.reload();
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await expect(page.locator('#folderSetupRunModal')).toHaveCount(0);
  await page.locator('#personalAssistantLauncher').click();
  await revealFolderSetupDetails(card);
  await expect(card.locator('#personalAssistantFolderReceipt')).toContainText(
    'Listed 6 music projects in Desktop'
  );
});

test('kept open, setup ends on the songs saved most recently and opens one', async ({
  page,
  request
}) => {
  test.skip(!modelReady, 'no model provider is available on this machine');
  seedCollection('Downloads', 'Demo', [0, 1, 2, 4, 6, 9, 15, 25], 1);
  await scan(request, 'downloads');
  const card = await openCard(page);
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toContainText('is ready', {
    timeout: 300_000
  });
  await expect(card.locator('#personalAssistantFolderSongs li')).toHaveCount(6);
  await expect(card.locator('#personalAssistantFolderSongsNote')).toHaveText(
    'Projects saved most recently'
  );
  expect(await songRows(page)).toEqual([
    ['Demo 01', 'Saved today'],
    ['Demo 02', 'Saved yesterday'],
    ['Demo 03', 'Saved 2 days ago'],
    ['Song 01', 'Saved 3 days ago'],
    ['Demo 04', 'Saved 4 days ago'],
    ['Song 02', 'Saved 5 days ago']
  ]);
  const opens = card.locator('[data-folder-run-open]');
  await expect(opens.first()).toHaveAttribute(
    'aria-label',
    'Open Demo 01: makes its workspace and adds your assistant'
  );
  const browse = card.getByRole('link', { name: 'Browse all 24 songs' });
  await expect(browse).toBeVisible();
  libraryURL = String(await browse.getAttribute('href'));
  expect(libraryURL).toMatch(/^\/workspaces\/[a-z0-9-]+\/assistant#projectLibraryPanel$/);

  // The single library action, songs, then the full receipt disclosure.
  await browse.focus();
  const order: string[] = [];
  for (let i = 0; i < 7; i++) {
    await page.keyboard.press('Tab');
    order.push(
      await page.evaluate(
        () =>
          document.activeElement?.getAttribute('aria-label') ||
          document.activeElement?.textContent?.trim() ||
          ''
      )
    );
  }
  expect(order.slice(0, 6).every(label => /^Open .*: makes its workspace/.test(label))).toBe(true);
  expect(order.slice(6)).toEqual(['Full setup details']);

  // Other failures retain the Home review route and re-enable the buttons.
  const failure = 'The REAPER integration changed. Review it on this Home, then open the song.';
  await page.route('**/assistant-program/library/projects/*/open', route =>
    route.fulfill({
      status: 409,
      contentType: 'application/json',
      body: JSON.stringify({ error: failure, reason: 'provider_unavailable' })
    })
  );
  await opens.nth(1).click();
  await expect(card.locator('#personalAssistantFolderSongsNote')).toHaveText(
    'Demo 02 could not be opened. Review it in the Home library.'
  );
  for (const button of await opens.all()) {
    await expect(button).toBeEnabled();
    await expect(button).toHaveText('Open');
  }
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toContainText('is ready');
  await page.unroute('**/assistant-program/library/projects/*/open');

  // Two project files: the names come back as chips under the song.
  await opens.first().click();
  const chips = card.locator('[data-folder-run-choice] button');
  await expect(chips).toHaveText(['Demo 01 alt.rpp', 'Demo 01.rpp']);
  await expect(chips.first()).toBeFocused();
  await expect(card.locator('#personalAssistantFolderSongsNote')).toHaveText(
    'Demo 01 has more than one project file. Choose the one to open.'
  );
  await expect(card.locator('#personalAssistantFolderSongsNote')).not.toContainText(
    'could not be opened'
  );
  await chips.filter({ hasText: /^Demo 01\.rpp$/ }).click();
  await page.waitForURL(/\/workspaces\/demo-01$/, { timeout: 180_000 });

  // The song has its workspace and the shared assistant.
  const detail = await ok(await request.get(`/api/agents/${encodeURIComponent(ASSISTANT)}/detail`));
  expect((detail.workspaces || []).map((ws: { name: string }) => ws.name)).toContain('Demo 01');
});

test('the Home library lists the same songs, re-sorts after a re-save, and opens one', async ({
  page
}) => {
  test.skip(!libraryURL, 'the kept-open run did not finish');
  await page.goto(libraryURL);
  const recent = page.locator('#projectLibraryRecent');
  await expect(recent).toBeVisible({ timeout: 60_000 });
  const cards = () =>
    page
      .locator('#projectLibraryRecentCards article')
      .evaluateAll(items =>
        items.map(item => [
          item.querySelector('h4')?.textContent || '',
          item.querySelector('small')?.textContent || ''
        ])
      );
  expect(await cards()).toEqual([
    ['Demo 01', 'Saved today'],
    ['Demo 02', 'Saved yesterday'],
    ['Demo 03', 'Saved 2 days ago'],
    ['Song 01', 'Saved 3 days ago'],
    ['Demo 04', 'Saved 4 days ago'],
    ['Song 02', 'Saved 5 days ago']
  ]);
  // Demo 01 is open now: its Open goes to its workspace.
  await expect(
    page.locator('#projectLibraryRecentCards [data-library-open]').first()
  ).toHaveAttribute('aria-label', 'Open Demo 01');

  // Re-save the oldest song, rescan its folder from the Home: it leads.
  const oldest = join(sandbox, 'Documents', 'Song 10', 'Song 10.rpp');
  const now = new Date();
  utimesSync(oldest, now, now);
  await page
    .locator('#projectLibraryRoots article.project-library-root', { hasText: '/Documents' })
    .getByRole('button', { name: 'Review scan' })
    .click();
  await page.getByRole('button', { name: 'Scan metadata' }).click();
  await expect
    .poll(async () => (await cards())[0], { timeout: 60_000 })
    .toEqual(['Song 10', 'Saved today']);

  // The table sorts by Last saved too, newest first, each row labelled.
  await page.locator('#projectLibrarySort').selectOption('last_saved');
  await expect(page.locator('#projectLibraryDirection')).toHaveValue('desc');
  await page.locator('#projectLibrarySearchForm').getByRole('button', { name: 'Find' }).click();
  const firstRow = page.locator('#projectLibraryRows tr').first();
  await expect(firstRow.locator('strong')).toHaveText('Song 10', { timeout: 30_000 });
  await expect(firstRow.locator('.project-library-saved')).toHaveText('Saved today');

  await page.locator('#projectLibraryRecentCards [data-library-open]').first().click();
  await page.waitForURL(/\/workspaces\/song-10$/, { timeout: 180_000 });
});
