import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * The mission board, golden path (tasks/prd-starter-missions.md, reshaped by
 * tasks/prd-mission-quest-folder-refocus.md).
 *
 * Starter = Meet your assistant, Show your assistant a folder, See what your
 * assistant found. Daily loop = Connect one source, Read your first Daily Brief.
 *
 * Run against a FRESH isolated sandbox, serially:
 *   ./scripts/smoke.sh serve 8947 starter-e2e      (in another terminal)
 *   ./scripts/e2e.sh --port 8947 tests/starter-missions.spec.ts -- --workers=1
 *
 * A sandbox that already hired an assistant or finished a mission cannot show
 * the path from the start, so the first test refuses a used sandbox rather
 * than passing on stale state.
 *
 * What only a browser proves, and so what is here:
 *   - Mission 02 is Locked behind the HQ (also after Not now), with one action,
 *     Build My HQ, that opens the HQ card in the drawer.
 *   - A built HQ opens it; Start opens the one-card chooser (not the chat
 *     chooser) and scrubs the quest parameter.
 *   - The Daily loop is listed locked beneath Starter, and opens once Starter is
 *     resolved.
 *   - The first-day plan completes Connect one source; the brief completes
 *     Read your first Daily Brief.
 * Mission 01 (Meet your assistant) is the hire, made here through the API;
 * tests/personal-assistant-foundation.spec.ts drives it in the browser. The rest
 * of Mission 02 and Mission 03 (a scan, an offer, a workspace, the first look)
 * need folders under the server's own HOME and a model, which
 * `scripts/smoke.sh showfolder` seeds and drives and
 * tests/one-card-folder-setup.spec.ts covers.
 */

test.describe.configure({ mode: 'serial' });

// Messages the app logs on any fresh sandbox, unrelated to this feature: the
// update checker has no network, and some pages probe resources a fresh
// install does not have. The last one is the workspace page's setup monitor:
// a poll it had in flight is cut off when a test leaves My HQ for Home, and
// whether one is in flight at that moment is a matter of timing. Anything else
// is a failure.
const KNOWN_NOISE = [
  /Failed to load resource/,
  /Error checking for updates/,
  /Failed to monitor task execution: TypeError: Failed to fetch/
];

function watchErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(`pageerror: ${error.message}`));
  page.on('console', message => {
    if (message.type() !== 'error') return;
    const text = message.text();
    if (!KNOWN_NOISE.some(pattern => pattern.test(text))) errors.push(`console: ${text}`);
  });
  return errors;
}

type Mission = { id: string; order: number; status: string; title: string; in_progress?: boolean };

async function missions(request: APIRequestContext): Promise<Mission[]> {
  const res = await request.get('/api/progression');
  expect(res.ok()).toBeTruthy();
  return ((await res.json()).missions || []) as Mission[];
}

async function missionStatus(request: APIRequestContext, id: string): Promise<string> {
  return (await missions(request)).find(mission => mission.id === id)?.status || '';
}

async function openQuests(page: Page) {
  await page.goto('/');
  const toggle = page.locator('#cockpitQuestsToggle');
  await expect(toggle).toBeVisible({ timeout: 20000 });
  if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
  const card = page.locator('[data-role="first-mission"]');
  await expect(card).toBeVisible({ timeout: 15000 });
  return card;
}

async function expectNoHorizontalScroll(page: Page) {
  const width = await page.evaluate(() => ({
    page: document.documentElement.scrollWidth,
    viewport: window.innerWidth
  }));
  expect(width.page).toBeLessThanOrEqual(width.viewport + 1);
}

// Mission 01 completes from the hire. Show a folder needs the HQ: with none
// built the card says Locked and offers Build My HQ, never the chooser, and
// pressing it opens the drawer on the HQ card, even after Not now (FR21, FR22).
test('a fresh hire sees Mission 02 locked behind the HQ, and Build My HQ opens its card', async ({
  page,
  request
}) => {
  const errors = watchErrors(page);
  await request.post('/api/onboarding/skip');

  const assistant = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  test.skip(
    assistant?.state !== 'needs_hire',
    'this spec needs a fresh sandbox: the assistant is already hired'
  );
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: `e2e-hire-${Date.now()}`,
      if_version: assistant.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my week organised.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.ok(), await hire.text()).toBeTruthy();

  // Not now on the HQ card defers it. The folder mission stays locked.
  await page.goto('/?panel=today');
  await expect(page.locator('#personalAssistantHQCard')).toBeVisible({ timeout: 20000 });
  await page.locator('#personalAssistantHQNotNow').click();

  const card = await openQuests(page);
  await expect(card.locator('[data-role="first-mission-title"]')).toHaveText(
    'Show your assistant a folder'
  );
  await expect(card.locator('[data-role="first-mission-status"]')).toHaveText('Locked');
  await expect(card.locator('[data-role="first-mission-skip"]')).toBeHidden();
  const fix = card.locator('[data-role="first-mission-action"]');
  await expect(fix).toContainText('Build My HQ');
  // Locked, so Starter is still the current tier and the Daily loop waits.
  await expect(page.locator('[data-role="tier-name"]')).toHaveText('Missions · Starter');
  await expect(page.locator('[data-role="progress-label"]')).toBeHidden();

  await fix.click();
  await expect(page.locator('#personalAssistantHQCard')).toBeVisible({ timeout: 15000 });
  await expect(page.locator('#personalAssistantHQHeadline')).toContainText('home base');
  // Never the folder chooser while locked, and no raw 409.
  await expect(page.locator('#personalAssistantFolderChooser')).toBeHidden();
  expect(await missionStatus(request, 'pa-show-folder')).toBe('available');

  await page.setViewportSize({ width: 400, height: 860 });
  await expectNoHorizontalScroll(page);
  expect(errors).toEqual([]);
});

test('a built HQ opens Mission 02 and lists the Daily loop locked beneath Starter', async ({
  page,
  request
}) => {
  test.skip(
    (await missionStatus(request, 'pa-meet-assistant')) !== 'completed',
    'needs the hire from the earlier test'
  );
  const errors = watchErrors(page);
  const hired = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: `e2e-hq-${Date.now()}`,
      if_version: hired.state_version ?? 0,
      name: 'Demo HQ'
    }
  });
  expect(hq.ok(), await hq.text()).toBeTruthy();

  const card = await openQuests(page);
  await expect(card.locator('[data-role="first-mission-kicker"]')).toHaveText('Mission 02');
  await expect(card.locator('[data-role="first-mission-status"]')).toHaveText('Ready');
  await expect(card.locator('[data-role="first-mission-action"]')).toHaveAttribute(
    'href',
    '/?quest=show-folder'
  );
  // No offer yet, so the card carries no inline question.
  await expect(card.locator('[data-role="first-mission-offer"]')).toBeHidden();

  // The header and the compact button say the tier's name, never "Tier 1 of 2".
  await expect(page.locator('[data-role="tier-name"]')).toHaveText('Missions · Starter');
  await expect(page.locator('#cockpitQuestsToggle')).toContainText('Missions · 1/3');
  await expect(page.locator('#questLog')).not.toContainText(/Tier \d/);

  // Beneath the card: the other Starter missions (the hire, done, and the look,
  // locked), then the Daily loop, locked.
  const rows = page.locator('[data-role="quests"] .quest-item');
  await expect(rows).toHaveCount(4);
  await expect(rows.filter({ hasText: 'Meet your assistant' })).toHaveClass(/quest-item-done/);
  await expect(page.locator('[data-role="quests"] .quest-list-heading')).toHaveText('Daily loop');
  await expect(rows.filter({ hasText: 'See what your assistant found' })).toContainText(
    'Show your assistant a folder first'
  );
  for (const title of ['Plan my first day', 'Read your first Daily Brief']) {
    const row = rows.filter({ hasText: title });
    await expect(row).toContainText('Starter first');
    await expect(row).toHaveClass(/quest-item-locked/);
    // A locked row offers no link and no Skip.
    await expect(row.locator('a, button')).toHaveCount(0);
  }
  await expect(rows.filter({ hasText: 'Build My HQ' })).toHaveCount(0);
  // The card's mission is not repeated beneath it.
  await expect(
    rows.locator('.quest-title', { hasText: /^Show your assistant a folder$/ })
  ).toHaveCount(0);

  await page.setViewportSize({ width: 400, height: 860 });
  await expect(card).toBeVisible();
  await expectNoHorizontalScroll(page);
  expect(errors).toEqual([]);
});

test('Mission 02: Start opens the one-card chooser in the assistant panel', async ({
  page,
  request
}) => {
  test.skip(
    (await missionStatus(request, 'pa-show-folder')) !== 'available',
    'needs the HQ from the earlier test'
  );
  const errors = watchErrors(page);
  const card = await openQuests(page);
  await card.locator('[data-role="first-mission-action"]').click();

  // The drawer opens on the assistant's own chooser (chips and picker, then a
  // read-only scan and the verdict card), not the chat chooser; the quest
  // parameter is scrubbed so a reload does not open it again.
  await expect(page.locator('#personalAssistantToday')).toBeVisible({ timeout: 15000 });
  const chooser = page.locator('#personalAssistantFolderChooser');
  await expect(chooser).toBeVisible({ timeout: 15000 });
  await expect(chooser.locator('#personalAssistantFolderTitle')).toContainText(
    /Which folder should I explore\?|explore a folder/i
  );
  await expect(page.locator('#personalAssistantContextChooser')).toBeHidden();
  await expect(page).not.toHaveURL(/quest=/);
  // Opening the chooser is not doing the mission.
  expect(await missionStatus(request, 'pa-show-folder')).toBe('available');

  await page.setViewportSize({ width: 400, height: 860 });
  await expect(chooser).toBeVisible();
  await expectNoHorizontalScroll(page);
  expect(errors).toEqual([]);
});

test('deferring Mission 02 opens the Daily loop on Mission 04', async ({ page, request }) => {
  test.skip(
    (await missionStatus(request, 'pa-show-folder')) !== 'available',
    'needs the HQ from the earlier test'
  );
  const errors = watchErrors(page);
  const skipped = await request.post('/api/progression/skip', {
    data: { quest_id: 'pa-show-folder' }
  });
  expect(skipped.ok(), await skipped.text()).toBeTruthy();

  const card = await openQuests(page);
  await expect(card.locator('[data-role="first-mission-kicker"]')).toHaveText('Mission 04');
  await expect(page.locator('[data-role="tier-name"]')).toHaveText('Missions · Daily loop');
  const rows = page.locator('[data-role="quests"] .quest-item');
  await expect(
    rows.filter({
      has: page.locator('.quest-title', { hasText: /^Show your assistant a folder$/ })
    })
  ).toContainText('Skipped');
  // The first look cannot start without a folder, but it no longer holds Starter.
  await expect(rows.filter({ hasText: 'See what your assistant found' })).toHaveClass(
    /quest-item-locked/
  );
  expect(errors).toEqual([]);
});

test('Mission 04, plan branch: the first-day plan completes it', async ({ page, request }) => {
  test.skip(
    !['completed', 'skipped'].includes(await missionStatus(request, 'pa-show-folder')),
    'needs Mission 02 resolved by the earlier test'
  );
  const errors = watchErrors(page);

  const card = await openQuests(page);
  await expect(card.locator('[data-role="first-mission-title"]')).toHaveText('Plan my first day');
  await card.locator('[data-role="first-mission-action"]').click();

  await expect(page.locator('#onboardingPersonalAssistantAssignment')).toBeVisible({
    timeout: 15000
  });
  await page.locator('#pafPriorityRows [data-field="title"]').first().fill('Review the launch');
  await page.locator('#pafPreviewAssignmentBtn').click();
  await page
    .locator('#pafCommitmentRows [data-paf-assignment-row="i_owe"] [data-field="title"]')
    .fill('Send Maya the draft');
  await page
    .locator('#pafCommitmentRows [data-paf-assignment-row="i_owe"] [data-field="counterparty"]')
    .fill('Maya');
  await page.locator('#pafPreviewAssignmentBtn').click();
  await page.locator('#pafPreviewAssignmentBtn').click();
  await expect(page.locator('#pafAssignmentPreview')).toBeVisible({ timeout: 15000 });
  await page.locator('#pafAssignmentConfirm').check();
  await page.locator('#pafApplyAssignmentBtn').click();
  await expect(page.locator('#pafAssignmentResult')).toBeVisible({ timeout: 60000 });

  await expect
    .poll(() => missionStatus(request, 'pa-connect-source'), { timeout: 15000 })
    .toBe('completed');
  expect(errors).toEqual([]);
});

// The brief is read in the Daily Brief station in My HQ. Opening Home, which
// loads the assistant's Today, does not count as reading it; the station's
// panel showing the brief does.
test('Mission 05: Open Daily Brief goes to the station, and the panel showing a brief completes it', async ({
  page,
  request
}) => {
  test.skip(
    (await missionStatus(request, 'pa-connect-source')) !== 'completed',
    'needs Mission 04 completed by the earlier test'
  );
  const errors = watchErrors(page);

  // Home has been opened several times by now and Today has served the brief.
  const card = await openQuests(page);
  await expect(card.locator('[data-role="first-mission-title"]')).toHaveText(
    'Read your first Daily Brief'
  );
  expect(await missionStatus(request, 'pa-first-brief')).not.toBe('completed');

  const open = card.locator('[data-role="first-mission-action"]');
  await expect(open).toContainText('Open Daily Brief');
  await expect(open).toHaveAttribute('href', /^\/workspaces\/[a-z0-9-]+\?station=daily-brief$/);
  await open.click();

  await expect(page.locator('.ws-cmd-modal-panel.is-daily-brief')).toBeVisible({ timeout: 15000 });
  await expect(page.locator('[data-brief="body"]')).not.toContainText(/Loading|Generating/, {
    timeout: 30000
  });
  await expect
    .poll(() => missionStatus(request, 'pa-first-brief'), { timeout: 15000 })
    .toBe('completed');

  await page.goto('/');
  await page.locator('#cockpitQuestsToggle').click();
  await expect(page.locator('#questLog')).toContainText('Missions complete');
  await expect(
    page
      .locator('[data-role="quests"] .quest-item')
      .filter({ hasText: 'Read your first Daily Brief' })
  ).toBeVisible();
  expect(errors).toEqual([]);
});
