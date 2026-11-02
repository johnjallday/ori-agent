import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// The Notes station's panel as the HQ page's home for the second brain (PRD
// notes-station-panel FR29): it opens from the Details rail row and from the Map
// building, files a line as a follow-up, answers a Look again inside the rate
// limit with the wait, opens from ?station=second-brain, and the badge under the
// building reads like the rail row.
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
  mkdirSync(EVIDENCE, { recursive: true });
});

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
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

const panel = (page: Page) => page.locator('.ws-cmd-modal-panel.is-station');
const section = (page: Page, key: string) => panel(page).locator(`[data-notes-section="${key}"]`);
const railRow = (page: Page) =>
  page.locator('.ws-cmd-rail-item[data-cmd-hq-station="second-brain"]');
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

async function showStationsRail(page: Page) {
  await page.locator('[data-cmd-view-mode="details"]').first().click();
  const row = railRow(page);
  if (!(await row.isVisible())) {
    await page.locator('[data-cmd-rail-section="stations"]').first().click();
  }
  await expect(row).toBeVisible();
  // The badge has read the connection once the row names an open count.
  await expect(row.locator('.ws-cmd-rail-m')).toHaveText(/\d+ open/);
  return row;
}

async function expectNotesPanel(page: Page) {
  await expect(panel(page).locator('.ws-cmd-modal-title')).toHaveText('Notes');
  await expect(section(page, 'loops').locator('.ws-notes-item').first()).toBeVisible();
}

test('the Details rail row opens the panel with the look, and Escape returns to the row', async ({
  page
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page, '?mode=details');
  const row = await showStationsRail(page);
  await row.click();
  await expectNotesPanel(page);

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
  await page.screenshot({ path: join(EVIDENCE, 'spec-details-rail-1440-light.png') });

  await page.keyboard.press('Escape');
  await expect(panel(page)).toBeHidden();
  await expect(railRow(page)).toBeFocused();
});

test('the Map building opens the same panel, and its badge reads like the rail row', async ({
  page
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page, '?mode=details');
  const rail = (await (await showStationsRail(page)).locator('.ws-cmd-rail-m').textContent()) || '';
  const railName = (await railRow(page).getAttribute('aria-label')) || '';

  await page.locator('[data-cmd-view-mode="map"]').first().click();
  await expect(building(page)).toBeVisible();
  await expect(building(page).locator('.ws-cmd-map-hq-station-state')).toHaveText(rail);
  const description = railName.replace(/^Notes station, /, '');
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
        const violations = await page.evaluate(async () =>
          (
            await (window as any).axe.run('.ws-cmd-modal-panel.is-station', {
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
