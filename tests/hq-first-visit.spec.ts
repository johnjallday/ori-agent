import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

// Fresh, credential-free installations; no folder look is started.
// ./scripts/e2e-fresh.sh --sandbox-env ORI_HQ_FIRST_VISIT_SANDBOX tests/hq-first-visit.spec.ts -- --workers=1
// Add --env ORI_HQ_FIRST_VISIT_HIDE=1 for missing Documents + untouched dismissal,
// or --env ORI_HQ_FIRST_VISIT_NATURAL=1 for completion outside the suggestions.
// The default finish uses canonical Skip, NOT a fabricated folder report.
const hide = process.env.ORI_HQ_FIRST_VISIT_HIDE === '1';
const natural = process.env.ORI_HQ_FIRST_VISIT_NATURAL === '1';
const BRAIN = '/api/personal-assistant/second-brain';
const FINISH = '/api/personal-hq/first-visit-state';
let hqPath = '';
test.describe.configure({ mode: 'serial' });
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.setViewportSize({ width: 1440, height: 1100 });
});
async function json(
  request: APIRequestContext,
  method: 'get' | 'post',
  url: string,
  data?: unknown
) {
  const response = await request[method](url, data === undefined ? undefined : { data });
  expect(response.ok(), `${method} ${url}: ${await response.text()}`).toBeTruthy();
  return response.json();
}
const popover = (page: Page) => page.locator('[data-hq-firstvisit]');
const action = (page: Page, name: string) => page.locator(`[data-hq-firstvisit-action="${name}"]`);
const state = async (request: APIRequestContext) =>
  (await json(request, 'get', '/api/personal-hq/status')).status.first_visit_state;
async function mission(request: APIRequestContext, id: string) {
  return (await json(request, 'get', '/api/progression')).missions.find(
    (item: { id: string }) => item.id === id
  )?.status;
}
async function open(page: Page) {
  await action(page, 'open').click();
  await expect(popover(page)).toBeVisible();
  await expect(action(page, 'brief')).toBeEnabled();
}
async function closed(page: Page) {
  await expect(popover(page)).toHaveCount(0);
  await expect(action(page, 'open')).toHaveAttribute('aria-expanded', 'false');
}
async function accessible(page: Page) {
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  const violations = await page.evaluate(
    async () =>
      (
        await (window as any).axe.run('[data-hq-firstvisit]', {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        })
      ).violations
  );
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
}

test.beforeAll(async ({ request }) => {
  const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
  expect(sandbox, 'use e2e-fresh --sandbox-env ORI_HQ_FIRST_VISIT_SANDBOX').toBeTruthy();
  if (!hide) mkdirSync(join(sandbox!, 'Documents'), { recursive: true, mode: 0o750 });
  await json(request, 'post', '/api/onboarding/skip', {});
  const root = (await json(request, 'get', '/api/settings/workspace-root')).default_workspace_root;
  await json(request, 'post', '/api/settings/workspace-root', { workspace_root: root });
  const before = (await json(request, 'get', '/api/personal-assistant')).personal_assistant;
  expect(before.state, 'requires its own fresh sandbox').toBe('needs_hire');
  await json(request, 'post', '/api/personal-assistant/hire', {
    request_id: 'first-visit-hire',
    if_version: before.state_version,
    display_name: 'Atlas',
    mandate: 'Keep my week moving.',
    focus_areas: ['plan_my_day']
  });
  const hired = (await json(request, 'get', '/api/personal-assistant')).personal_assistant;
  const notes = (await json(request, 'get', BRAIN)).second_brain;
  await json(request, 'post', '/api/personal-assistant/hq', {
    request_id: 'first-visit-build',
    if_version: hired.state_version,
    name: 'My HQ',
    timezone: 'UTC',
    scope: 'all',
    include_future_workspaces: true,
    notes: { include: true, witness: notes.setup_default.path }
  });
  const status = (await json(request, 'get', '/api/personal-hq/status')).status;
  expect(status.first_visit_state).toBe('pending');
  hqPath = `/workspaces/${encodeURIComponent(status.workspace.folder_slug)}?mode=details`;
});

test(`on-demand suggestions: ${hide ? 'missing Documents and untouched dismissal' : natural ? 'natural Notes completion' : 'existing-flow handoffs and completion'}`, async ({
  page,
  request
}, testInfo) => {
  await page.addInitScript(() => {
    localStorage.setItem('oriWorkspaceCommandViewMode', 'map');
    localStorage.setItem('ori.hq.firstVisit.v1', JSON.stringify({ showAll: false }));
  });
  await page.goto(hqPath);
  await expect(page.locator('body')).toHaveAttribute('data-hq-first-visit', 'pending');
  await closed(page);
  await expect(page.locator('.ws-hq-main [data-hq-firstvisit]')).toHaveCount(0);
  await expect(page.locator('[data-cmd-hq-host="notes"]')).toBeVisible();
  await expect(page.locator('.ws-hq-stations')).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem('oriWorkspaceCommandViewMode'))).toBe(
    'map'
  );
  const needs = page.locator('[data-cmd-hq-needs]');
  await expect(needs).toBeVisible();
  // Compare within one event turn: unrelated asynchronous setup/status updates
  // must not be mistaken for layout changes caused by opening the popover.
  const positions = await page.evaluate(() => {
    const needs = document.querySelector('[data-cmd-hq-needs]')!;
    const before = needs.getBoundingClientRect().top;
    (document.querySelector('[data-hq-firstvisit-action="open"]') as HTMLButtonElement).click();
    return [before, needs.getBoundingClientRect().top];
  });
  expect(positions[1]).toBe(positions[0]);
  await expect(popover(page)).toBeVisible();
  await expect(action(page, 'brief')).toBeEnabled();
  await expect(popover(page)).not.toContainText(/of 3/);
  await expect(popover(page).locator('textarea, input')).toHaveCount(0);
  await expect(popover(page)).toBeFocused();
  await action(page, 'brief').focus();
  const survivesRepaint = await page.evaluate(() => {
    const overlay = document.querySelector('[data-hq-firstvisit]');
    (window as any).workspaceCommand.render();
    return document.querySelector('[data-hq-firstvisit]') === overlay;
  });
  expect(survivesRepaint).toBeTruthy();
  await expect(action(page, 'brief')).toBeFocused();
  await accessible(page);
  await page.screenshot({ path: testInfo.outputPath('popover.png') });
  await page.keyboard.press('Escape');
  await closed(page);
  await expect(action(page, 'open')).toBeFocused();
  await open(page);
  await action(page, 'close').click();
  await closed(page);
  await expect(action(page, 'open')).toBeFocused();
  await open(page);
  await page.locator('.ws-cmd-title h2').click(); // light dismiss, no forced focus return
  await closed(page);
  expect(await state(request)).toBe('pending');
  await page.reload();
  await closed(page);

  // Phone sheet owns focus/inert; repeated opening/closing releases both.
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  await expect(popover(page)).toHaveAttribute('aria-modal', 'true');
  await action(page, 'hide').focus();
  await page.keyboard.press('Tab');
  await expect(action(page, 'close')).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(action(page, 'hide')).toBeFocused();
  await accessible(page);
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)
  ).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('sheet.png') });
  await page.keyboard.press('Escape');
  await closed(page);
  await expect(action(page, 'open')).toBeFocused();
  expect(await page.locator('[data-overlay-inert]').count()).toBe(0);
  await open(page);
  await page.mouse.click(5, 5);
  await closed(page);
  await expect(action(page, 'open')).toBeFocused();
  expect(await page.locator('[data-overlay-inert]').count()).toBe(0);
  await page.setViewportSize({ width: 1440, height: 1100 });

  const folderCalls: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().includes('/folder-agent/'))
      folderCalls.push(req.url());
  });
  await open(page);
  await popover(page).getByRole('link', { name: 'Explore Documents' }).click();
  if (hide) {
    await expect(
      page.getByText('Documents could not be chosen. Choose another folder, or try again.')
    ).toBeVisible();
    await expect(page.locator('#personalAssistantFolderPathEntry input')).toBeVisible();
  } else {
    const consent = page.locator('[data-folder-agent-card="choose"]');
    await expect(consent).toContainText('Start an agent on Documents');
    await expect(consent).toContainText('Nothing is created until you hire');
    await expect(consent.getByRole('button', { name: 'Hire & start' })).toBeVisible();
  }
  expect(folderCalls).toHaveLength(1);
  expect(folderCalls[0]).toContain('/choose');
  expect(await mission(request, 'pa-show-folder')).not.toBe('completed');
  await page.locator('#personalAssistantClose').click();
  await page.goto(hqPath);
  await closed(page);

  const notes = page.locator('[data-cmd-hq-host="notes"]');
  if (natural) {
    await notes.locator('[data-notes-capture]').fill('An idea saved through normal Notes.');
    await notes.locator('[data-cmd-modal-action="notes-capture"]').click();
    await expect(notes).toContainText('An idea saved through normal Notes.');
    await closed(page);
    await open(page);
    await expect(popover(page)).toContainText('Your first note is in your Inbox.');
  } else if (!hide) {
    await open(page);
    await action(page, 'brief').click();
    await expect(popover(page)).toHaveCount(0);
    const brief = page.locator('.ws-cmd-modal-panel.is-daily-brief');
    await expect(brief).toBeVisible();
    await expect(brief.locator('[data-brief="body"]')).not.toContainText(/Loading|Generating/, {
      timeout: 30_000
    });
    await expect(brief.locator('[data-brief="body"]')).not.toBeEmpty();
    await page.evaluate(() => (window as any).workspaceCommand.render());
    await page.getByRole('button', { name: 'Close Daily Brief' }).click();
    await closed(page);
    await expect(action(page, 'open')).toBeFocused();
    await expect.poll(() => mission(request, 'pa-first-brief')).toBe('completed');
    expect(await mission(request, 'pa-capture-first-note')).not.toBe('completed');

    const draft = 'Keep this existing Notes draft.';
    await notes.locator('[data-notes-capture]').fill(draft);
    await open(page);
    await expect(popover(page)).toContainText('Your first brief is ready.');
    await action(page, 'note').click();
    await closed(page);
    const input = notes.locator('[data-notes-capture]');
    await expect(input).toBeFocused();
    await expect(input).toHaveValue(draft);
    await expect(page.locator('[data-hq-firstvisit-note]')).toHaveCount(0);
    await page.keyboard.press('Enter');
    await expect(notes).toContainText(draft);
    await expect.poll(() => mission(request, 'pa-capture-first-note')).toBe('completed');
    await closed(page);
    await page.reload();
    await closed(page);
    await open(page);
    await expect(popover(page)).toContainText('Your first note is in your Inbox.');
    await page.route(`**${FINISH}`, route =>
      route.fulfill({ status: 503, json: { error: 'Temporarily unavailable' } })
    );
    await action(page, 'hide').click();
    await expect(popover(page)).toContainText('could not be dismissed');
    expect(await state(request)).toBe('pending');
    await page.unroute(`**${FINISH}`);
    await action(page, 'close').click();
    await json(request, 'post', '/api/progression/skip', { quest_id: 'pa-show-folder' });
    await open(page);
    await expect(popover(page).locator('.is-resolved')).toHaveCount(3);
    await expect(popover(page)).toContainText('Skipped');
  }
  if (!(await popover(page).count())) await open(page);
  const beforeMissions = (await json(request, 'get', '/api/progression')).missions;
  await action(page, 'hide').click();
  await expect(popover(page)).toHaveCount(0);
  await expect(action(page, 'open')).toHaveCount(0);
  expect(await state(request)).toBe('done');
  expect((await json(request, 'get', '/api/progression')).missions).toEqual(beforeMissions);
  await page.reload();
  await expect(action(page, 'open')).toHaveCount(0);
});

test('a done HQ has no suggestion door and keeps the quiet Overview', async ({ page, request }) => {
  expect(await state(request)).toBe('done');
  await page.goto(hqPath);
  await expect(page.locator('.ws-hq-team')).toBeVisible();
  await expect(popover(page)).toHaveCount(0);
  await expect(action(page, 'open')).toHaveCount(0);
  await expect(page.locator('.ws-hq-facts, #workspace-command-subtitle')).toHaveCount(0);
  await expect(page.locator('[data-cmd-hq-needs]')).toHaveCount(1);
  await expect(page.locator('[data-cmd-hq-host="notes"]')).toBeVisible();
  await expect(page.locator('.ws-hq-unused')).toContainText('Not in use yet');
  await expect(page.locator('[data-cmd-hq-rail-more]')).toHaveAttribute('aria-expanded', 'false');
  expect(await page.locator('.ws-hq-rail .ws-cmd-panel-count').allTextContents()).not.toContain(
    '0'
  );
});
