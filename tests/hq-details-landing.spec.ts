import { test, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// HQ Details landing (PRD hq-details-landing FR21, FR22, FR25): the Personal HQ
// with the second brain's suggested items accepted as follow-ups opens on its
// own header, with follow-ups after Notes in the HQ main column.
//
// Run against the sandbox scripts/second-brain-demo.sh prints:
//
//   ./scripts/second-brain-demo.sh 8947
//   ./scripts/e2e.sh --port 8947 --env SECOND_BRAIN_VAULT="<VAULT from READY>" \
//     tests/hq-details-landing.spec.ts
//
// beforeAll connects the fixture vault by API, waits for the look and accepts
// its suggested items, so the HQ holds the eight note follow-ups the music
// fixture yields. The last case completes one of them.
//
// The accessibility pass loads axe-core from jsdelivr like the other a11y
// specs. Where that CDN is unreachable, set AXE_CORE_FILE to a local
// axe.min.js of the same version and the request is served from it.

const vault = process.env.SECOND_BRAIN_VAULT || '';
const AXE_URL = 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js';
const BRAIN = '/api/personal-assistant/second-brain';
const EVIDENCE = 'tasks/evidence-hq-details-landing';

test.describe.configure({ mode: 'serial' });
test.skip(
  !vault,
  'Run against scripts/second-brain-demo.sh: set PLAYWRIGHT_BASE_URL and SECOND_BRAIN_VAULT from its READY line.'
);

type FollowUp = { id: string; title: string; due_at?: string; source: { type: string } };

let hqPath = '';
let followUps: FollowUp[] = [];

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
      request_id: `hq-details-landing-connect-${Date.now()}`
    });
  }
  let look: { look_id: string; state: string; result?: unknown } | null = null;
  await expect
    .poll(
      async () => {
        look = (await json(request, 'get', `${BRAIN}/look`)).look;
        return Boolean(look && look.result && look.state !== 'running');
      },
      { timeout: 90_000 }
    )
    .toBe(true);
  await json(request, 'post', `${BRAIN}/look/${look!.look_id}/follow-ups`, {
    items: 'suggested',
    request_id: `hq-details-landing-accept-${look!.look_id}`
  });

  const status = (await json(request, 'get', '/api/personal-hq/status')).status;
  hqPath = `/workspaces/${status.workspace.folder_slug}`;
  // This is the normal Overview regression, not the new-HQ welcome journey.
  if (status.first_visit_state) {
    await json(request, 'post', '/api/personal-hq/first-visit-state', { state: 'done' });
  }
  followUps = (await json(request, 'get', `/api/workspaces/${status.workspace_id}/followups`))
    .followups;
  expect(followUps.filter(item => item.source.type === 'note_item').length).toBeGreaterThanOrEqual(
    8
  );
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

const panel = (page: Page) => page.locator('#workspaceFollowupMount');
const topOf = (locator: Locator) =>
  locator.evaluate(node => node.getBoundingClientRect().top + window.scrollY);
const bottomOf = (locator: Locator) =>
  locator.evaluate(node => node.getBoundingClientRect().bottom + window.scrollY);

async function openHQ(page: Page, query = '', theme = 'light') {
  await page.addInitScript(value => {
    try {
      localStorage.setItem('ori-theme', value);
    } catch (_) {
      // The page falls back to its default theme.
    }
  }, theme);
  await page.goto(hqPath + query);
  await expect(page.locator('.ws-cmd-topbar')).toBeVisible({ timeout: 30_000 });
  // A fresh context opens in Details. Clicking the mode button here would take
  // focus away from a deep-linked card, so the mode is asserted, not set.
  await expect(page.locator('.ws-cmd-main')).toBeVisible();
  await expect(panel(page).locator('section.workspace-followups')).toBeVisible({ timeout: 30_000 });
}

async function expandPanel(page: Page) {
  const toggle = panel(page).locator('.workspace-followups-toggle');
  if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
}

test('the HQ opens on its header, with follow-ups below Notes', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page);

  // The header is in the first viewport, well under the old ~1,460px.
  const header = page.locator('.ws-cmd-topbar');
  expect(await topOf(header)).toBeLessThan(300);
  await expect(page.locator('#personalHQKnowledgeEntry')).toHaveCount(0);

  // One panel, in the main column, after Notes and before the stations.
  await expect(page.locator('[data-cmd-hq-attention]')).toHaveCount(0);
  await expect(page.locator('[data-cmd-hq-needs]')).toHaveCount(1);
  await expect(page.locator('#workspaceFollowupMount')).toHaveCount(1);
  await expect(
    page.locator('.ws-cmd-main [data-cmd-followups-host] #workspaceFollowupMount')
  ).toHaveCount(1);
  // Station sources hydrate independently of follow-ups; an empty host is
  // intentionally hidden until the configured/folded stations arrive.
  const stations = page.locator('.ws-hq-stations');
  await expect(stations).toBeVisible();
  const notesBottom = await bottomOf(page.locator('[data-cmd-hq-host="notes"]'));
  const panelTop = await topOf(panel(page));
  expect(panelTop).toBeGreaterThanOrEqual(notesBottom);
  expect(await topOf(stations)).toBeGreaterThan(panelTop);

  await expect(panel(page).locator('.workspace-followups-summary')).toContainText(/\d+ open/);
  await expandPanel(page);
  await expect(panel(page).locator('.workspace-followup-due').first()).toBeVisible();
  const link = panel(page).locator('.workspace-followup-source-link').first();
  await expect(link).toHaveAttribute('href', /^obsidian:\/\/open\?/);
  expect(await page.locator('body').innerText()).not.toContain('obsidian://');

  mkdirSync(EVIDENCE, { recursive: true });
  await page.screenshot({ path: join(EVIDENCE, 'spec-details-1440-light.png') });
});

test('keyboard: the toggle, the chips and the cards are reachable and operable', async ({
  page
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page);
  const toggle = panel(page).locator('.workspace-followups-toggle');
  await toggle.focus();
  const before = await toggle.getAttribute('aria-expanded');
  await page.keyboard.press('Enter');
  await expect(toggle).not.toHaveAttribute('aria-expanded', before!);
  await expect(toggle).toBeFocused();
  await expandPanel(page);

  await toggle.focus();
  await page.keyboard.press('Tab');
  const all = panel(page).locator('.workspace-followups-chip').first();
  await expect(all).toBeFocused();
  await page.keyboard.press('Tab');
  const notes = panel(page).locator('.workspace-followups-chip[data-filter="note_item"]');
  await expect(notes).toBeFocused();
  await page.keyboard.press(' ');
  await expect(notes).toHaveAttribute('aria-pressed', 'true');
  await expect(all).toHaveAttribute('aria-pressed', 'false');
  // The next stop is the first card's note link.
  await page.keyboard.press('Tab');
  await expect(panel(page).locator('.workspace-followup-source-link').first()).toBeFocused();
});

test('the follow_up deep link focuses its card in the new place', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const target = followUps[followUps.length - 1];
  // The quiet rail is short before Notes loads. Keep the selected follow-up
  // visible after that real read grows the panel above it, not only before.
  await page.route(`**${BRAIN}/look`, async route => {
    await new Promise(resolve => setTimeout(resolve, 500));
    await route.continue();
  });
  await openHQ(page, `?follow_up=${encodeURIComponent(target.id)}`, 'dark');
  await expect(page.locator('[data-cmd-hq-host="notes"] .ws-notes-section').first()).toBeVisible();
  const card = panel(page).locator(`[data-followup-id="${target.id}"]`);
  await expect(card).toBeFocused();
  await expect(card).toHaveClass(/is-deep-linked/);
  await expect(card).toBeInViewport();
  await page.screenshot({ path: join(EVIDENCE, 'spec-deeplink-1440-dark.png') });
});

for (const theme of ['light', 'dark']) {
  test(`axe finds no WCAG 2 A/AA violations on the panel at 390px (${theme})`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openHQ(page, '', theme);
    await expandPanel(page);
    await panel(page).scrollIntoViewIfNeeded();
    const width = await panel(page).evaluate(node => ({
      fits: node.scrollWidth <= node.clientWidth + 1,
      right: node.getBoundingClientRect().right
    }));
    expect(width.fits).toBeTruthy();
    expect(width.right).toBeLessThanOrEqual(391);
    await page.addScriptTag({ url: AXE_URL });
    const violations = await page.evaluate(async () =>
      (
        await (window as any).axe.run('#workspaceFollowupMount', {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        })
      ).violations.map((v: { id: string; nodes: { target: string[] }[] }) => ({
        id: v.id,
        targets: v.nodes.map(n => n.target.join(' '))
      }))
    );
    expect(violations).toEqual([]);
    await panel(page).screenshot({ path: join(EVIDENCE, `spec-panel-390-${theme}.png`) });
  });
}

test('Map keeps the header first and Details gets the panel back with working actions', async ({
  page
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHQ(page);
  const header = page.locator('.ws-cmd-topbar');

  await page.locator('[data-cmd-view-mode="map"]').first().click();
  await expect(page.locator('.ws-cmd-map-shell')).toBeVisible();
  expect(await topOf(header)).toBeLessThan(300);
  const inMap = page.locator(
    '.ws-cmd-map-shell ~ [data-cmd-followups-host] #workspaceFollowupMount'
  );
  await expect(inMap).toBeVisible();
  expect(await topOf(inMap)).toBeGreaterThanOrEqual(
    await bottomOf(page.locator('.ws-cmd-map-shell'))
  );

  await page.locator('[data-cmd-view-mode="tickets"]').first().click();
  await expect(panel(page)).toBeHidden();

  await page.locator('[data-cmd-view-mode="details"]').first().click();
  await expect(
    page.locator('.ws-cmd-main [data-cmd-followups-host] #workspaceFollowupMount')
  ).toBeVisible();
  await expect(page.locator('#workspaceFollowupMount')).toHaveCount(1);
  await expandPanel(page);

  const cards = panel(page).locator('.hq-followup-card');
  const before = await cards.count();
  const first = cards.first();
  const id = await first.getAttribute('data-followup-id');
  const completed = page.waitForResponse(
    response => response.url().endsWith('/api/personal-hq/followups/complete') && response.ok()
  );
  await first.getByRole('button', { name: 'Done' }).click();
  await completed;
  await expect(panel(page).locator(`[data-followup-id="${id}"]`)).toHaveCount(0);
  await expect(cards).toHaveCount(before - 1);
});
