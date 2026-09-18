import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdtempSync, writeFileSync, utimesSync, existsSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';

/**
 * Starter missions, the golden path (tasks/prd-starter-missions.md).
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
 *     opens the creator with File Janitor preselected and Ori's mark on Create.
 *   - The File Janitor setup wizard reaching ready completes Mission 03 on the
 *     server, and Home then shows Mission 04 with Mission 03 checked.
 *   - A real approved move appears on Today as "Filed 1 file into …".
 *   - The first-day plan completes Mission 04.
 * Mission 01 (Meet your assistant) is the hire, made here through the API;
 * tests/personal-assistant-foundation.spec.ts drives it in the browser.
 *
 * The one thing mocked is the operating system's folder dialog: its endpoint
 * answers with a throwaway fixture path, and every request after it is real.
 */

test.describe.configure({ mode: 'serial' });

const OLD = new Date(Date.now() - 6 * 60 * 60 * 1000);

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

function fixtureFolder(files: string[]): string {
  const root = mkdtempSync(join(tmpdir(), 'starter-missions-'));
  // The feature really moves files: never anywhere near a real Downloads folder.
  if (!root.startsWith(tmpdir()) || root.startsWith(join(homedir(), 'Downloads'))) {
    throw new Error(`refusing to use ${root} as a fixture`);
  }
  for (const name of files) {
    writeFileSync(join(root, name), `fixture ${name}`);
    utimesSync(join(root, name), OLD, OLD);
  }
  return root;
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

let janitorSlug = '';
let janitorRoot = '';

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
  await expect(card.locator('[data-role="first-mission-title"]')).toHaveText('Tidy your Downloads');
  await expect(card.locator('[data-role="first-mission-status"]')).toHaveText('Ready');
  await expect(card.locator('[data-role="first-mission-action"]')).toHaveAttribute(
    'href',
    '/?quest=tidy-downloads'
  );
  // The card's mission is the only Starter mission not repeated beneath it:
  // four of the five, Meet your assistant and Build My HQ among them, done.
  const rows = page.locator('[data-role="quests"] .quest-item');
  await expect(rows).toHaveCount(4);
  await expect(page.locator('[data-role="quests"] .quest-item-locked')).toHaveCount(0);
  await expect(rows.filter({ hasText: 'Tidy your Downloads' })).toHaveCount(0);

  await page.setViewportSize({ width: 400, height: 860 });
  await expect(card).toBeVisible();
  await expectNoHorizontalScroll(page);
  expect(errors).toEqual([]);
});

test('Mission 03: Start guides to Create, and File Janitor setup completes it', async ({
  page,
  request
}) => {
  const errors = watchErrors(page);
  const card = await openQuests(page);
  await card.locator('[data-role="first-mission-action"]').click();

  // The creator opens with File Janitor preselected and Ori explains the step.
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible({ timeout: 15000 });
  await expect
    .poll(() => page.evaluate(() => window.ProjectTemplateCard?.getSelectedTemplate?.()?.id))
    .toBe('file-janitor');
  await expect(page).not.toHaveURL(/quest=/);
  await expect(page.locator('#oriGuideReply')).toContainText('Step 1 of 2');
  await expect(page.locator('#oriGuideReply')).not.toContainText('cannot point');

  // Walk the creator as a user does; the Team step needs its File Curator.
  for (let i = 0; i < 6 && !(await page.locator('#createFolderBtn').isVisible()); i++) {
    const role = page.locator(
      '#wizardStep3:not([hidden]) #workspaceRoleRoster .ws-role-row button.btn-primary'
    );
    if (
      await role
        .first()
        .isVisible()
        .catch(() => false)
    ) {
      await role.first().click();
      await expect(page.locator('#addAgentModal')).toBeVisible({ timeout: 10000 });
      await page.locator('#createAgentBtn').click();
      await expect(page.locator('#addAgentModal')).toBeHidden({ timeout: 10000 });
      await expect(creator).toBeVisible({ timeout: 10000 });
      continue;
    }
    await page.locator('#wizardNextBtn').click();
  }
  const create = page.locator('#createFolderBtn');
  await expect(create).toBeVisible();
  await expect(create).toHaveClass(/is-ori-coachmark/, { timeout: 5000 });

  await create.click();
  await expect(page).toHaveURL(/\/workspaces\/[^/?#]+/, { timeout: 30000 });
  janitorSlug = decodeURIComponent(new URL(page.url()).pathname.split('/')[2] || '');
  const wizard = page.locator('#setupWizardDialog');
  await expect(wizard).toBeVisible({ timeout: 20000 });
  await expect(page.locator('#setupWizardStepTitle')).toHaveText('Choose the folder to tidy');

  // Mid-wizard, the card says so and points back here.
  expect(await missionStatus(request, 'pa-tidy-downloads')).toBe('available');
  const inProgress = (await missions(request)).find(m => m.id === 'pa-tidy-downloads');
  expect(inProgress?.in_progress).toBe(true);

  janitorRoot = fixtureFolder(['invoice.pdf']);
  await page.route('**/api/folder-picker/select-path', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, selected: true, path: janitorRoot })
    })
  );
  await page.locator('#fileJanitorWizardPick').click();

  // The wizard's own primary action through automation, readiness, and summary.
  const primary = page.locator('#setupWizardPrimary');
  for (let i = 0; i < 8 && (await wizard.isVisible()); i++) {
    await expect(primary).toBeEnabled({ timeout: 15000 });
    await primary.click();
    await page.waitForTimeout(800);
  }
  await expect
    .poll(() => missionStatus(request, 'pa-tidy-downloads'), { timeout: 15000 })
    .toBe('completed');

  const home = await openQuests(page);
  await expect(home.locator('[data-role="first-mission-kicker"]')).toHaveText('Mission 04');
  await expect(
    page.locator('[data-role="quests"] .quest-item.quest-item-done').filter({
      hasText: 'Tidy your Downloads'
    })
  ).toHaveCount(1);
  expect(errors).toEqual([]);
});

test('an approved move appears on Today and links to History', async ({ page }) => {
  test.skip(!janitorSlug || !janitorRoot, 'needs the File Janitor workspace from Mission 03');
  const errors = watchErrors(page);

  await page.goto(`/workspaces/${encodeURIComponent(janitorSlug)}`);
  await page.locator('#fileJanitorCardOpen').click();
  const consoleBody = page.locator('#fileJanitorConsoleBody');
  await expect(page.locator('#fileJanitorConsole')).toBeVisible({ timeout: 15000 });
  await page.locator('#fileJanitorScan').click();
  const row = consoleBody.locator('.fj-row-item').filter({ hasText: 'invoice.pdf' });
  await expect(row).toBeVisible({ timeout: 20000 });
  await row.locator('.fj-select').check();
  await page.locator('#fileJanitorApprove').click();
  await consoleBody
    .getByRole('button', { name: /Move|Apply|Confirm these/ })
    .first()
    .click();
  await expect
    .poll(() => existsSync(join(janitorRoot, 'Filed', 'Documents', 'invoice.pdf')), {
      timeout: 30000
    })
    .toBe(true);

  await page.goto('/');
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantTodayPanel')).toBeVisible({ timeout: 15000 });
  const line = page
    .locator('#personalAssistantTodayResults li')
    .filter({ hasText: /Filed 1 file into .+\/Filed/ });
  await expect(line).toBeVisible({ timeout: 15000 });
  await expect(line).toContainText('Undo from History');

  await page.setViewportSize({ width: 400, height: 860 });
  await expect(line).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.setViewportSize({ width: 1280, height: 800 });

  await line.locator('a').click();
  await expect(page).toHaveURL(/panel=file-janitor&tab=history/);
  await expect(page.locator('#fileJanitorConsole [data-fj-tab="history"]')).toHaveAttribute(
    'aria-selected',
    'true',
    { timeout: 15000 }
  );
  await expect(page.getByText('Some link details were out of date')).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('Mission 04, plan branch: the first-day plan completes it', async ({ page, request }) => {
  test.skip(
    (await missionStatus(request, 'pa-tidy-downloads')) !== 'completed',
    'needs Mission 03 completed by the earlier test'
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
