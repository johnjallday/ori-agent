import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import {
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  statSync,
  utimesSync,
  writeFileSync
} from 'node:fs';
import { join } from 'node:path';

// Song facts and a collection brief: a collection set up through the one card
// reads each REAPER project's tempo, length and track count (the card says so),
// shows them wherever songs are listed, has a switch that stops the read and
// clears them, and the Manager's post-scan turn writes a collection brief (or
// the Home says why there is none). No project file is ever changed.
//
// Run it against a fresh isolated sandbox with a model provider:
//
//   CODEX_HOME="$HOME/.codex" ./scripts/demo-server.sh 8932     # prints SANDBOX=…
//   ./scripts/e2e.sh --port 8932 --env ORI_SONG_FACTS_SANDBOX=<sandbox dir> tests/song-facts-collection-brief.spec.ts
//
// The first run downloads the reviewed REAPER integration and the Home
// provider. No REAPER app is launched.
const sandbox = process.env.ORI_SONG_FACTS_SANDBOX || '';
test.skip(!sandbox, 'requires ORI_SONG_FACTS_SANDBOX (the demo sandbox directory)');
test.describe.configure({ mode: 'serial' });
test.setTimeout(360_000);

const folderApi = '/api/personal-assistant/folder-digest';
const CHIP = 'Desktop';
const READS_LINE =
  "Reads each REAPER project's tempo, length and track count. Nothing is moved, copied or changed.";

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

// A minimal project shaped like one REAPER saves.
function project(tempo: number, tracks: number, length: number, tempoChanges = false) {
  const lines = ['<REAPER_PROJECT 0.1 "7.27/macOS-arm64" 1727800000', `  TEMPO ${tempo} 4 4`];
  if (tempoChanges)
    lines.push('  <TEMPOENVEX', `    PT 0 ${tempo} 1`, `    PT 60 ${tempo + 8} 1`, '  >');
  for (let t = 0; t < tracks; t++) {
    lines.push('  <TRACK', `    NAME "Track ${t + 1}"`);
    if (t === 0) lines.push('    <ITEM', '      POSITION 0', `      LENGTH ${length}`, '    >');
    lines.push('  >');
  }
  lines.push('>');
  return lines.join('\n') + '\n';
}

// [name, days ago, tempo, tracks, length, tempo changes] → the facts line it shows.
const SONGS: [string, number, number, number, number, boolean, string][] = [
  ['Track 01', 0, 92, 6, 221, false, '6 tracks · 92 BPM · 3:41'],
  ['Track 02', 1, 120, 1, 95, false, '1 track · 120 BPM · 1:35'],
  ['Track 03', 2, 96, 14, 200, true, '14 tracks · 96 BPM, varies · 3:20'],
  ['Track 04', 4, 140, 3, 3725, false, '3 tracks · 140 BPM · 1:02:05'],
  ['Track 05', 7, 88, 9, 150, false, '9 tracks · 88 BPM · 2:30'],
  ['Track 06', 12, 128.5, 4, 260, false, '4 tracks · 128.5 BPM · 4:20'],
  ['Track 07', 20, 100, 2, 180, false, '2 tracks · 100 BPM · 3:00']
];

function savedAt(days: number): Date {
  if (days === 0) return new Date(Date.now() - 60_000);
  const day = new Date();
  day.setHours(12, 0, 0, 0);
  day.setDate(day.getDate() - days);
  return day;
}

function seed() {
  for (const [name, days, tempo, tracks, length, changes] of SONGS) {
    const folder = join(sandbox, CHIP, name);
    const file = join(folder, `${name}.rpp`);
    if (existsSync(file)) continue;
    mkdirSync(join(folder, 'Media'), { recursive: true, mode: 0o750 });
    writeFileSync(file, project(tempo, tracks, length, changes), { mode: 0o600 });
    utimesSync(file, savedAt(days), savedAt(days));
    writeFileSync(join(folder, `${name}.rpp-bak`), project(60, 1, 10), { mode: 0o600 });
    writeFileSync(join(folder, 'Media', 'take-0.wav'), 'RIFF', { mode: 0o600 });
  }
}

// Every file under the collection with its content hash.
function hashes(dir = join(sandbox, CHIP), out = new Map<string, string>()) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) hashes(path, out);
    else out.set(path, createHash('sha256').update(readFileSync(path)).digest('hex'));
  }
  return out;
}

const plain = (text: string | null) => (text || '').replaceAll(' ', ' ').trim();

async function openCard(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel?.open?.(
      document.getElementById('personalAssistantLauncher'),
      { view: 'today' }
    )
  );
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  await card.scrollIntoViewIfNeeded();
  return card;
}

async function openLibrary(page: Page) {
  await page.goto('about:blank');
  await page.goto(libraryURL);
  await expect(page.locator('#projectLibraryRows tr').first()).toBeVisible({ timeout: 60_000 });
}

const rowLines = (page: Page) =>
  page
    .locator('#projectLibraryRows tr')
    .evaluateAll(rows =>
      rows.map(row => [
        row.querySelector('strong')?.textContent || '',
        (row.querySelector('.project-library-saved')?.textContent || '').replaceAll(' ', ' ')
      ])
    );

const cardLines = (page: Page) =>
  page
    .locator('#projectLibraryRecentCards article')
    .evaluateAll(cards =>
      cards.map(card => (card.querySelector('small')?.textContent || '').replaceAll(' ', ' '))
    );

// The scan review: one native dialog per confirmation.
const scanDialog = (page: Page) => page.locator('dialog.project-library-dialog[open]');

// The Manager's turn after a scan saves to the Home while it runs, which moves
// the library on; a scan reviewed meanwhile is refused as stale (by design).
// Wait for the turn to end before reviewing another scan.
async function managerIdle(page: Page) {
  await expect
    .poll(
      async () => {
        await openLibrary(page);
        return plain(await page.locator('#projectLibraryRun').textContent());
      },
      { timeout: 120_000, intervals: [3_000] }
    )
    .not.toMatch(/in progress|is describing/);
}

// One reviewed scan of the collection; `reads` is what its review must disclose.
async function rescan(page: Page, reads: string) {
  await managerIdle(page);
  await page
    .locator('#projectLibraryRoots article.project-library-root', { hasText: `/${CHIP}` })
    .getByRole('button', { name: 'Review scan' })
    .click();
  await expect(scanDialog(page)).toContainText(reads);
  await page.getByRole('button', { name: 'Scan metadata' }).click();
  // The commit (and, with song details on, the fact pass) has answered.
  await expect(page.locator('#projectLibraryStatus')).toHaveText(
    /^complete scan · \d+ entries seen/,
    { timeout: 60_000 }
  );
}

let modelReady = false;
let libraryURL = '';
let before = new Map<string, string>();

test('the card says what the library reads, and setup ends on songs with their facts', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip');
  const relationship = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  if (relationship.state === 'needs_hire') {
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'song-facts-hire',
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
        data: { request_id: 'song-facts-hq', if_version: hired.state_version, name: 'My HQ' }
      })
    );
  }
  modelReady = (
    await request.post('/api/settings/system-model', {
      data: { provider: 'codex', model: 'gpt-5.6-luna' }
    })
  ).ok();
  test.skip(!modelReady, 'no model provider is available on this machine');

  seed();
  before = hashes();
  const offer = (await ok(await request.post(`${folderApi}/scan`, { data: { chip: 'desktop' } })))
    .offer;
  expect(offer?.portfolio, 'the Desktop scan is a collection').toBeTruthy();
  const library = offer.plan.lines.find((line: { kind: string }) => line.kind === 'library');
  expect(library.detail).toBe(READS_LINE);

  const card = await openCard(page);
  await expect(card).toContainText(READS_LINE);
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  const runModal = page.locator('#folderSetupRunModal');
  await expect(runModal.locator('#folderSetupRunTitle')).toHaveText('Pick a song to start with', {
    timeout: 300_000
  });
  const songs = await page
    .locator('#folderSetupRunSongs li')
    .evaluateAll(items =>
      items.map(item => [
        item.querySelector('strong')?.textContent || '',
        (item.querySelector('small')?.textContent || '').replaceAll(' ', ' ')
      ])
    );
  expect(songs).toEqual(
    SONGS.slice(0, 6).map(([name, , , , , , facts]) => [
      name,
      expect.stringMatching(
        new RegExp(`^Saved .+ · ${facts.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`)
      )
    ])
  );
  libraryURL = String(
    await runModal.getByRole('link', { name: /^Browse all/ }).getAttribute('href')
  );
  await expect(card.locator('#personalAssistantFolderReceipt')).toContainText(
    'tempo, length and track count'
  );
});

test('the Home shows the facts, the switch and the Manager section', async ({ page }) => {
  test.skip(!libraryURL, 'the setup did not finish');
  await openLibrary(page);
  await expect(page.locator('#projectLibraryIntro')).toContainText(
    "each REAPER project's tempo, length and track count"
  );
  expect(await cardLines(page)).toEqual(
    SONGS.slice(0, 6).map(([, , , , , , facts]) => expect.stringContaining(` · ${facts}`))
  );
  const rows = Object.fromEntries(await rowLines(page));
  for (const [name, , , , , , facts] of SONGS) expect(rows[name]).toContain(` · ${facts}`);

  const toggle = page.locator('#projectLibrarySongDetailsSwitch');
  await expect(toggle).toBeVisible();
  await expect(toggle).toBeChecked();
  await expect(page.locator('#projectLibrarySongDetailsNote')).toHaveText(
    "Ori reads each REAPER project's tempo, length and track count. Nothing is changed."
  );

  // The Manager's turn ends in a brief, or the section says why there is none.
  await expect(page.locator('#projectLibraryProposalsTitle')).toHaveText(/^From your .+/);
  await expect
    .poll(
      async () => {
        await openLibrary(page);
        if (await page.locator('#projectLibraryBrief').isVisible()) return 'brief';
        const run = plain(await page.locator('#projectLibraryRun').textContent());
        return run.startsWith('No brief') ? 'none' : run;
      },
      { timeout: 150_000, intervals: [5_000] }
    )
    .toMatch(/^(brief|none)$/);
  if (await page.locator('#projectLibraryBrief').isVisible()) {
    const text = plain(await page.locator('.project-library-brief-text').textContent());
    expect(text.length).toBeGreaterThan(0);
    expect(text.length).toBeLessThanOrEqual(500);
    expect(text).not.toMatch(/https?:|www\.|\/|\.rpp\b/i);
    await expect(page.locator('.project-library-brief-source')).toHaveText(
      /^Written by .+ after the scan on [A-Z][a-z]{2} \d{1,2}( · .+)?$/
    );
  }
});

test('switching off clears the facts; a rescan reads none; on again reads them next scan', async ({
  page
}) => {
  test.skip(!libraryURL, 'the setup did not finish');
  await openLibrary(page);
  await page.locator('#projectLibrarySongDetailsSwitch').click();
  await expect(page.locator('#projectLibraryStatus')).toHaveText(
    'Song details cleared. Scans no longer read project files.',
    { timeout: 30_000 }
  );
  const noFacts = async () => {
    for (const [, line] of await rowLines(page)) expect(line).not.toMatch(/BPM|tracks?\b/);
    for (const line of await cardLines(page)) expect(line).not.toMatch(/BPM|tracks?\b/);
  };
  await noFacts();
  await expect(page.locator('#projectLibraryBrief')).toBeHidden();

  // Off means off: a rescan reads nothing, and its review says names only.
  await rescan(page, '(no file contents)');
  await openLibrary(page);
  await noFacts();

  // On again: nothing is read until the next scan, which reads them.
  await managerIdle(page);
  await page.locator('#projectLibrarySongDetailsSwitch').click();
  await expect(page.locator('#projectLibraryStatus')).toHaveText(
    /^Song details are on\. The next scan reads them/,
    { timeout: 30_000 }
  );
  await noFacts();
  await rescan(page, 'tempo, length and track count');
  await expect
    .poll(
      async () => {
        await openLibrary(page);
        return Object.fromEntries(await rowLines(page))['Track 01'] || '';
      },
      { timeout: 60_000, intervals: [2_000] }
    )
    .toContain(' · 6 tracks · 92 BPM · 3:41');

  // No project file was changed, moved or added at any point.
  const after = hashes();
  expect([...after.keys()].sort()).toEqual([...before.keys()].sort());
  for (const [path, hash] of before) expect(after.get(path), path).toBe(hash);
});
