import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * Starter missions, the golden path (tasks/prd-starter-missions.md, with
 * Mission 03 replaced by tasks/prd-show-me-a-folder.md).
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
 *   - The Quests card follows the server's missions, and Mission 03's Start
 *     opens the assistant panel on Today with the folder chooser unfolded and
 *     the quest parameter scrubbed.
 *   - Deferring Mission 03 moves the card on to Mission 04.
 *   - The first-day plan completes Mission 04.
 * Mission 01 (Meet your assistant) is the hire, made here through the API;
 * tests/personal-assistant-foundation.spec.ts drives it in the browser. The
 * rest of Mission 03 — a scan, an offer, a workspace linked to the folder or a
 * tidy — needs folders under the server's own HOME, which
 * `scripts/smoke.sh showfolder` seeds and drives.
 */

test.describe.configure({ mode: 'serial' });

// Messages the app logs on any fresh sandbox, unrelated to this feature: the
// update checker has no network, and some pages probe resources a fresh
// install does not have. Anything else is a failure.
const KNOWN_NOISE = [/Failed to load resource/, /Error checking for updates/];

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

// Mission 01 (Meet your assistant) completes from the hire itself and Mission
// 02 from Build My HQ, so the card opens on Mission 03.
test('a fresh hire with HQ sees Mission 03 on the card', async ({ page, request }) => {
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
  await expect(card.locator('[data-role="first-mission-kicker"]')).toHaveText('Mission 03');
  await expect(card.locator('[data-role="first-mission-title"]')).toHaveText(
    'Show your assistant a folder'
  );
  await expect(card.locator('[data-role="first-mission-status"]')).toHaveText('Ready');
  await expect(card.locator('[data-role="first-mission-action"]')).toHaveAttribute(
    'href',
    '/?quest=show-folder'
  );
  // No offer yet, so the card carries no inline question.
  await expect(card.locator('[data-role="first-mission-offer"]')).toBeHidden();
  // The card's mission is the only Starter mission not repeated beneath it:
  // four of the five, Meet your assistant and Build My HQ among them, done.
  const rows = page.locator('[data-role="quests"] .quest-item');
  await expect(rows).toHaveCount(4);
  await expect(page.locator('[data-role="quests"] .quest-item-locked')).toHaveCount(0);
  await expect(rows.filter({ hasText: 'Show your assistant a folder' })).toHaveCount(0);
  await expect(rows.filter({ hasText: 'Tidy your Downloads' })).toHaveCount(0);
  await expect(rows.filter({ hasText: 'Start a project workspace' })).toHaveCount(0);

  await page.setViewportSize({ width: 400, height: 860 });
  await expect(card).toBeVisible();
  await expectNoHorizontalScroll(page);
  expect(errors).toEqual([]);
});

test('Mission 03: Start opens the folder chooser in the assistant panel', async ({
  page,
  request
}) => {
  const errors = watchErrors(page);
  const card = await openQuests(page);
  await card.locator('[data-role="first-mission-action"]').click();

  // The assistant panel opens on Today with the chooser unfolded; the quest
  // parameter is scrubbed so a reload does not open it again.
  await expect(page.locator('#personalAssistantTodayPanel')).toBeVisible({ timeout: 15000 });
  const chooser = page.locator('#personalAssistantFolderChooser');
  await expect(chooser).toBeVisible({ timeout: 15000 });
  await expect(chooser.locator('button').first()).toBeVisible();
  await expect(page).not.toHaveURL(/quest=/);
  // Opening the chooser is not doing the mission.
  expect(await missionStatus(request, 'pa-show-folder')).toBe('available');

  await page.setViewportSize({ width: 400, height: 860 });
  await expect(chooser).toBeVisible();
  await expectNoHorizontalScroll(page);
  expect(errors).toEqual([]);
});

test('deferring Mission 03 moves the card on to Mission 04', async ({ page, request }) => {
  const errors = watchErrors(page);
  const skipped = await request.post('/api/progression/skip', {
    data: { quest_id: 'pa-show-folder' }
  });
  expect(skipped.ok(), await skipped.text()).toBeTruthy();

  const card = await openQuests(page);
  await expect(card.locator('[data-role="first-mission-kicker"]')).toHaveText('Mission 04');
  await expect(
    page.locator('[data-role="quests"] .quest-item').filter({
      hasText: 'Show your assistant a folder'
    })
  ).toContainText('Resume');
  expect(errors).toEqual([]);
});

test('Mission 04, plan branch: the first-day plan completes it', async ({ page, request }) => {
  test.skip(
    !['completed', 'skipped'].includes(await missionStatus(request, 'pa-show-folder')),
    'needs Mission 03 resolved by the earlier test'
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
  const after = await openQuests(page);
  await expect(after.locator('[data-role="first-mission-kicker"]')).toHaveText('Mission 05');
  expect(errors).toEqual([]);
});
