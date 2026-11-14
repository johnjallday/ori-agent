import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// The Notes station's panel as the HQ page's home for the second brain (PRD
// notes-station-panel FR29): it lives inline on Overview and opens from the Free
// Map building, files a line as a follow-up, answers Look again inside the rate
// limit with the wait, and opens from ?station=second-brain. The building's
// badge agrees with the inline panel. The retired Stations rail is not used.
//
// Run against the sandbox scripts/second-brain-demo.sh prints:
//
//   ./scripts/second-brain-demo.sh 8947
//   PLAYWRIGHT_BASE_URL=http://localhost:8947 SECOND_BRAIN_VAULT="<VAULT from READY>" \
//     npx playwright test tests/notes-station-panel.spec.ts --project=chromium --workers=1
//
// beforeAll connects the fixture vault by API once, which is the one real look.
// Looks are rate limited to one per 10 minutes, so no case starts a second one:
// Look again is asserted to answer with the wait, which holds only within 10
// minutes of that look; run it against a fresh sandbox.
//
// The accessibility pass loads axe-core from jsdelivr like the other a11y
// specs. Where that CDN is unreachable, set AXE_CORE_FILE to a local
// axe.min.js of the same version and the request is served from it.

const vault = process.env.SECOND_BRAIN_VAULT || '';
const AXE_URL = 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js';
const BRAIN = '/api/personal-assistant/second-brain';
const EVIDENCE = 'tasks/evidence-notes-station-panel';

test.describe.configure({ mode: 'serial' });
test.skip(
  !vault,
  'Run against scripts/second-brain-demo.sh: set PLAYWRIGHT_BASE_URL and SECOND_BRAIN_VAULT from its READY line.'
);

let hqPath = '';

async function json(
  request: APIRequestContext,
  method: 'get' | 'post',
  url: string,
  data?: unknown
) {
  const response = await request[method](url, data === undefined ? undefined : { data });
  const body = await response.text();
  expect(response.ok(), `${method.toUpperCase()} ${url}: ${body}`).toBeTruthy();
  return body ? JSON.parse(body) : {};
}

test.beforeAll(async ({ request }) => {
  const brain = (await json(request, 'get', BRAIN)).second_brain;
  if (brain.status !== 'connected') {
    expect(brain.detected.length, 'the sandbox detects the fixture vault').toBeGreaterThan(0);
    await json(request, 'post', `${BRAIN}/connect`, {
      detected_id: brain.detected[0].id,
      request_id: `notes-station-panel-connect-${Date.now()}`
    });
  }
  await expect
    .poll(
      async () => {
        const look = (await json(request, 'get', `${BRAIN}/look`)).look;
        return look.state;
      },
      { timeout: 90_000 }
    )
    .toBe('finished');
  const status = (await json(request, 'get', '/api/personal-hq/status')).status;
  hqPath = `/workspaces/${status.workspace.folder_slug}`;
  if (status.first_visit_state) {
    await json(request, 'post', '/api/personal-hq/first-visit-state', { state: 'done' });
  }
  mkdirSync(EVIDENCE, { recursive: true });
});

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.addInitScript(() => {
    const slug = decodeURIComponent(location.pathname.split('/')[2] || '');
    if (slug) localStorage.setItem('oriWorkspaceMapArrangement:' + slug, 'free');
  });
  const axeFile = process.env.AXE_CORE_FILE || '';
  if (axeFile && existsSync(axeFile)) {
    await page.route(AXE_URL, route =>
      route.fulfill({
        status: 200,
        contentType: 'text/javascript',
        body: readFileSync(axeFile, 'utf8')
      })
    );
  }
});

const panel = (page: Page) =>
  page.locator('.ws-cmd-modal-panel.is-station:visible, [data-cmd-hq-host="notes"]:visible');
const section = (page: Page, key: string) => panel(page).locator(`[data-notes-section="${key}"]`);
const building = (page: Page) =>
  page.locator('.ws-cmd-map-hq-station[data-cmd-hq-station="second-brain"]');

async function openHQ(page: Page, query = '', theme = 'light') {
  await page.emulateMedia({ colorScheme: theme as 'light' | 'dark', reducedMotion: 'reduce' });
  await page.addInitScript(value => {
    try {
      localStorage.setItem('ori-theme', value);
    } catch (_) {
      // The page falls back to its default theme.
    }
  }, theme);
  await page.goto(hqPath + query);
  await expect(page.locator('.ws-cmd-topbar')).toBeVisible({ timeout: 30_000 });
}

async function expectNotesPanel(page: Page) {
  await expect(panel(page).locator('.ws-cmd-modal-title')).toHaveText('Notes');
  await expect(section(page, 'loops').locator('.ws-notes-item').first()).toBeVisible();
}

test('Overview owns the Notes panel inline, with the real read-only look', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page, '?mode=details');
  await expectNotesPanel(page);
  await expect(page.locator('[data-cmd-hq-host="notes"]')).toBeVisible();
  await expect(page.locator('[data-cmd-hq-station="second-brain"]')).toHaveCount(0);

  const footer = section(page, 'footer');
  await expect(footer).toContainText(/open items? in \d+ notes?/);
  await expect(footer).toContainText(/with dates/);
  await expect(footer).toContainText(
    'Ori keeps the folder path and these lines, never your notes.'
  );
  // Dated lines may already be follow-ups (a rerun on the same sandbox), so
  // the handled list is opened before the chip is looked for.
  const handled = section(page, 'loops').locator('details.ws-notes-handled');
  if (
    (await handled.count()) &&
    !(await handled.evaluate(node => (node as HTMLDetailsElement).open))
  ) {
    await handled.locator('summary').click();
  }
  await expect(section(page, 'loops').locator('.ws-notes-due').first()).toBeVisible();
  await expect(section(page, 'loops').locator('.ws-notes-due').first()).toHaveText(
    /^(Due today|Overdue · [A-Z][a-z]{2} \d{1,2}|Due [A-Z][a-z]{2} \d{1,2})$/
  );
  const link = section(page, 'loops').locator('.ws-notes-link').first();
  await expect(link).toHaveAttribute('href', /^obsidian:\/\/open\?/);
  await page.screenshot({ path: join(EVIDENCE, 'spec-overview-inline-1440-light.png') });
  await page.keyboard.press('Escape');
  await expect(panel(page)).toBeVisible();
  await expect(page.locator('.ws-cmd-modal-panel.is-station')).toHaveCount(0);
});

test('the Free Map building opens the same panel and its badge matches Overview', async ({
  page
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page, '?mode=details');
  const badge = page.locator('[data-cmd-hq-notes-meta] .ws-hq-notes-badge');
  await expect(badge).toHaveText(/\d+ open/);
  const value = (await badge.textContent()) || '';
  const description = (await badge.getAttribute('title')) || '';

  await page.locator('[data-cmd-view-mode="map"]').first().click();
  await expect(building(page)).toBeVisible();
  await expect(building(page).locator('.ws-cmd-map-hq-station-state')).toHaveText(value);
  expect(description).not.toBe('');
  await expect(building(page)).toHaveAttribute('aria-label', new RegExp(escapeRegExp(description)));

  await building(page).click();
  await expectNotesPanel(page);
  await page.screenshot({ path: join(EVIDENCE, 'spec-map-building-1440-light.png') });
  await page.keyboard.press('Escape');
  await expect(building(page)).toBeFocused();
});

test('?station=second-brain opens the panel and leaves the address bar clean', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page, '?station=second-brain');
  await expectNotesPanel(page);
  expect(page.url()).not.toContain('station=');
});

test('a line filed as a follow-up greys into handled, and adding the suggested says what it did', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page, '?station=second-brain');
  await expectNotesPanel(page);

  const loops = section(page, 'loops');
  const first = loops.locator('.ws-notes-list').first().locator('.ws-notes-item').first();
  const text = (await first.locator('.ws-notes-item-text').textContent()) || '';
  const itemID = (await first.getAttribute('data-notes-item')) || '';
  expect(itemID).not.toBe('');
  const filed = page.waitForResponse(
    response => response.url().includes(`/items/${itemID}`) && response.ok()
  );
  await first.getByRole('button', { name: `Follow up: ${text}` }).click();
  await filed;

  const handled = loops.locator('details.ws-notes-handled');
  await expect(handled).toBeVisible();
  if (!(await handled.evaluate(node => (node as HTMLDetailsElement).open))) {
    await handled.locator('summary').click();
  }
  const row = handled.locator(`[data-notes-item="${itemID}"]`);
  await expect(row).toContainText('Follow-up made');
  await expect(row.getByRole('button', { name: `Done: ${text}` })).toBeVisible();

  const look = (await json(request, 'get', `${BRAIN}/look`)).look;
  const item = look.result.items.find((entry: { id: string }) => entry.id === itemID);
  expect(item.accepted, 'the follow-up exists, so the line reads as accepted').toBe(true);
  expect(item.acted_as).toBe('follow_up');

  const bulk = loops.locator('.ws-notes-bulk');
  if (await bulk.count()) {
    await bulk.click();
    await expect(panel(page).locator('.ws-notes-receipt[role="status"]')).toContainText(/Added/);
  }
  await page.screenshot({ path: join(EVIDENCE, 'spec-filed-1440-light.png') });
});

// A connected vault (Issue 776): the panel says what it is under its title, Open
// folder opens the folder (the sandbox logs "Skipped desktop launch"), and no
// note of the user's is ever previewed.
test('a connected vault: one-liner, Open folder, and no preview of the user notes', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  // HQ Details shows the panel in its main column (#766), the modal elsewhere:
  // the case reads whichever copy is on screen.
  await openHQ(page, '?station=second-brain');
  const notes = page
    .locator(':is(.ws-cmd-modal-panel.is-station, [data-cmd-hq-host="notes"])')
    .filter({
      has: page.locator('[data-notes-one-liner]')
    })
    .first();
  await expect(notes.locator('[data-notes-one-liner]')).toHaveText(
    'Your notes, read-only. Ori keeps the open items it found, never your notes.'
  );
  await expect(notes.locator('[data-cmd-modal-action="notes-preview"]')).toHaveCount(0);
  const opened = page.waitForResponse(
    response =>
      response.url().endsWith(`${BRAIN}/open-folder`) && response.request().method() === 'POST'
  );
  await notes.locator('[data-cmd-modal-action="notes-open-folder"]').click();
  expect((await opened).status()).toBe(200);
  const preview = await request.get(`${BRAIN}/preview/inbox/anything.md`);
  expect(preview.status()).toBe(403);
  expect(await preview.text()).toContain('That note is yours; open it in your editor.');
});

test('Look again inside the rate limit names the wait', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page, '?station=second-brain');
  await expectNotesPanel(page);
  const refused = page.waitForResponse(
    response => response.url().endsWith(`${BRAIN}/look`) && response.request().method() === 'POST'
  );
  await section(page, 'footer').getByRole('button', { name: 'Look again' }).click();
  expect((await refused).status()).toBe(429);
  await expect(section(page, 'footer').locator('[role="status"]')).toHaveText(
    /^Looked .+\. Try again in \d+ minutes?\.$/
  );
});

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 768, 390]) {
    test(`the open panel at ${width}px (${theme}) fits and axe finds no WCAG 2 A/AA violations`, async ({
      page
    }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await openHQ(page, '?station=second-brain', theme);
      await expectNotesPanel(page);
      const fits = await panel(page).evaluate(node => ({
        fits: node.scrollWidth <= node.clientWidth + 1,
        right: node.getBoundingClientRect().right
      }));
      expect(fits.fits).toBeTruthy();
      expect(fits.right).toBeLessThanOrEqual(width + 1);
      if (width === 1440 || width === 390) {
        await page.addScriptTag({ url: AXE_URL });
        const violations = await panel(page).evaluate(async node =>
          (
            await (window as any).axe.run(node, {
              runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
            })
          ).violations.map((v: { id: string; nodes: { target: string[] }[] }) => ({
            id: v.id,
            targets: v.nodes.map(n => n.target.join(' '))
          }))
        );
        expect(violations).toEqual([]);
      }
      await page.screenshot({ path: join(EVIDENCE, `spec-panel-${width}-${theme}.png`) });
    });
  }
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
