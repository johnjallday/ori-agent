import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Real, credential-free journey on a fresh isolated server. Run twice to cover
// the two irreversible exits without resetting the user's persisted state:
// ./scripts/e2e-fresh.sh tests/hq-first-visit.spec.ts -- --workers=1
// ./scripts/e2e-fresh.sh --env ORI_HQ_FIRST_VISIT_HIDE=1 tests/hq-first-visit.spec.ts -- --workers=1
// Done uses the existing optional-folder mission's Skip, NOT a fake report or
// another model look. Actual folder completion is covered by the paid demo.
const hide = process.env.ORI_HQ_FIRST_VISIT_HIDE === '1';
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
const card = (page: Page) => page.locator('[data-hq-firstvisit]');
const action = (page: Page, name: string) => page.locator(`[data-hq-firstvisit-action="${name}"]`);
const state = async (request: APIRequestContext) =>
  (await json(request, 'get', '/api/personal-hq/status')).status.first_visit_state;
async function mission(request: APIRequestContext, id: string) {
  return (await json(request, 'get', '/api/progression')).missions.find(
    (item: { id: string }) => item.id === id
  )?.status;
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

test(`welcome, retry-safe note, real brief, preferences and ${hide ? 'Hide' : 'Done'}`, async ({
  page,
  request
}, testInfo) => {
  await page.addInitScript(() => localStorage.setItem('oriWorkspaceCommandViewMode', 'map'));
  await page.goto(hqPath);
  await expect(page.locator('body')).toHaveAttribute('data-hq-first-visit', 'pending');
  await expect(card(page)).toContainText('Welcome to your HQ.');
  await expect(card(page)).toContainText('Make it yours · 0 of 3');
  await expect(action(page, 'save')).toBeEnabled();
  expect(await page.evaluate(() => localStorage.getItem('oriWorkspaceCommandViewMode'))).toBe(
    'map'
  );
  await expect(page.locator('.ws-hq-rail > .ws-hq-team')).toBeVisible();
  await expect(page.locator('.ws-hq-rail > .ws-hq-rail-group')).toHaveCount(0);
  await expect(page.locator('[data-cmd-hq-rail-more]')).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('[data-cmd-hq-host="notes"], .ws-hq-stations')).toHaveCount(0);
  await expect(card(page).getByRole('link', { name: 'Choose a folder' })).toHaveAttribute(
    'href',
    '/?panel=today&folder=show'
  );
  await accessible(page);
  await page.screenshot({
    path: testInfo.outputPath('welcome.png'),
    fullPage: true,
    animations: 'disabled'
  });

  const before = (await json(request, 'get', `${BRAIN}/inbox`)).inbox.total;
  const captures: { text: string; request_id: string }[] = [];
  const receipts: string[] = [];
  await page.route(`**${BRAIN}/capture`, async route => {
    captures.push(route.request().postDataJSON());
    const response = await route.fetch(); // the server really writes, including the first attempt
    expect(response.ok(), await response.text()).toBeTruthy();
    receipts.push((await response.json()).capture.note);
    if (captures.length === 1)
      await route.abort('connectionreset'); // lose only the receipt
    else await route.fulfill({ response });
  });
  const text = 'Remember the quiet HQ journey.';
  const input = page.locator('[data-hq-firstvisit-note]');
  await input.fill(text);
  await action(page, 'save').click();
  await expect(card(page).locator('[aria-live="polite"]')).toContainText('could not confirm');
  await expect(input).toHaveValue(text);
  await expect(input).toHaveAttribute('readonly', '');
  await action(page, 'save').click();
  await expect(card(page)).toContainText('Make it yours · 1 of 3');
  expect(captures).toHaveLength(2);
  expect(captures[1]).toEqual(captures[0]);
  expect(receipts[1]).toBe(receipts[0]);
  expect((await json(request, 'get', `${BRAIN}/inbox`)).inbox.total).toBe(before + 1);
  await expect.poll(() => mission(request, 'pa-capture-first-note')).toBe('completed');
  await expect(page.locator('[data-cmd-hq-host="notes"]')).toContainText(text);
  await expect(action(page, 'brief')).toBeFocused();
  await page.reload();
  await expect(card(page)).toContainText('Your first note is in your Inbox.');
  await expect(card(page)).toContainText('Make it yours · 1 of 3');

  await action(page, 'show-all').click();
  await expect(card(page)).toHaveClass(/is-bar/);
  await expect(page.locator('.ws-hq-stations')).toBeVisible();
  await page.reload();
  await expect(card(page)).toHaveClass(/is-bar/);
  // Failure cannot hide the welcome or persist done. The successful exit is
  // exercised below (Hide and Done run against separate fresh installations).
  await page.route(`**${FINISH}`, route =>
    route.fulfill({ status: 503, json: { error: 'Temporarily unavailable' } })
  );
  await action(page, 'hide').click();
  await expect(card(page)).toHaveClass(/is-bar/);
  await expect(card(page)).toContainText('could not be finished');
  expect(await state(request)).toBe('pending');
  await page.unroute(`**${FINISH}`);
  await action(page, 'continue').click();
  await expect(card(page)).toHaveClass(/is-full/);
  await page.reload();
  await expect(card(page)).toHaveClass(/is-full/);

  await action(page, 'brief').click();
  const brief = page.locator('.ws-cmd-modal-panel.is-daily-brief');
  await expect(brief).toBeVisible();
  await expect(brief.locator('[data-brief="body"]')).not.toContainText(/Loading|Generating/, {
    timeout: 30_000
  });
  await expect(brief.locator('[data-brief="body"]')).not.toBeEmpty();
  await page.getByRole('button', { name: 'Close Daily Brief' }).click();
  await expect(card(page)).toContainText('Make it yours · 2 of 3');
  await expect(card(page)).toContainText('Your first brief is ready.');
  await expect(page.locator('[data-cmd-hq-card="daily-brief"]')).toBeVisible();
  await expect.poll(() => mission(request, 'pa-first-brief')).toBe('completed');
  await page.screenshot({
    path: testInfo.outputPath('two-steps.png'),
    fullPage: true,
    animations: 'disabled'
  });

  if (hide) {
    await action(page, 'show-all').click();
    await action(page, 'hide').click();
  } else {
    // Reopening the same real brief gives the existing close/refresh path a
    // canonical mission change during this visit. No folder result is faked.
    await action(page, 'brief').click();
    await expect(brief).toBeVisible();
    await json(request, 'post', '/api/progression/skip', { quest_id: 'pa-show-folder' });
    await page.getByRole('button', { name: 'Close Daily Brief' }).click();
    await expect(card(page)).toContainText('Your HQ is yours now.');
    await action(page, 'done').click();
  }
  await expect(card(page)).toHaveCount(0);
  expect(await state(request)).toBe('done');
  await page.reload();
  await expect(card(page)).toHaveCount(0);
  await expect(page.locator('body')).not.toHaveAttribute('data-hq-first-visit', 'pending');
});

test('a done HQ opens directly in the quiet Overview', async ({ page, request }) => {
  expect(await state(request)).toBe('done');
  await page.goto(hqPath);
  await expect(page.locator('.ws-hq-team')).toBeVisible();
  await expect(card(page)).toHaveCount(0);
  await expect(page.locator('.ws-hq-facts, #workspace-command-subtitle')).toHaveCount(0);
  await expect(page.locator('[data-cmd-hq-needs]')).toHaveCount(1);
  await expect(page.locator('[data-cmd-hq-host="notes"]')).toBeVisible();
  await expect(page.locator('.ws-hq-unused')).toContainText('Not in use yet');
  await expect(page.locator('[data-cmd-hq-rail-more]')).toHaveAttribute('aria-expanded', 'false');
  expect(await page.locator('.ws-hq-rail .ws-cmd-panel-count').allTextContents()).not.toContain(
    '0'
  );
});
