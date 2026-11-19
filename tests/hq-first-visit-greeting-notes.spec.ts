import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// An HQ built with Notes left for later: the assistant's greeting goes first in
// the empty drawer, and the Notes question (the personal notes ask) waits behind
// it. While it waits it is not recorded as shown; once the greeting is gone it
// shows itself, with no reload, and is recorded the first time it is really seen.
// Needs its own fresh sandbox because it builds an HQ; no model runs.
//
//   ./scripts/e2e-fresh.sh --port <free port> tests/hq-first-visit-greeting-notes.spec.ts -- --workers=1

const BRAIN = '/api/personal-assistant/second-brain';
let overviewPath = '';

test.describe.configure({ mode: 'serial' });
test.use({ viewport: { width: 1280, height: 900 } });
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
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

const panel = (page: Page) => page.locator('#personalAssistantPanel');
const greeting = (page: Page) => page.locator('#personalAssistantHQGreeting');
const question = (page: Page) => page.locator('#personalAssistantSecondBrain');

async function openDrawer(page: Page) {
  const launcher = page.locator('#personalAssistantLauncher');
  await expect
    .poll(async () => (await panel(page).isVisible()) || (await launcher.isVisible()))
    .toBe(true);
  if (!(await panel(page).isVisible())) await launcher.click();
  await expect(panel(page)).toBeVisible();
}

/** Counts the first-showing receipts ("prompted") the page posts. */
function countPrompted(page: Page) {
  const seen = { count: 0 };
  page.on('request', request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === `${BRAIN}/prompted`)
      seen.count += 1;
  });
  return seen;
}

test.beforeAll(async ({ request }) => {
  await json(request, 'post', '/api/onboarding/skip', {});
  const root = (await json(request, 'get', '/api/settings/workspace-root')).default_workspace_root;
  await json(request, 'post', '/api/settings/workspace-root', { workspace_root: root });
  const before = (await json(request, 'get', '/api/personal-assistant')).personal_assistant;
  expect(before.state, 'requires its own fresh sandbox').toBe('needs_hire');
  await json(request, 'post', '/api/personal-assistant/hire', {
    request_id: 'greeting-notes-hire',
    if_version: before.state_version,
    display_name: 'Atlas',
    mandate: 'Keep my week moving.',
    focus_areas: ['plan_my_day']
  });
  const hired = (await json(request, 'get', '/api/personal-assistant')).personal_assistant;
  // Notes left for later: nothing is written, and the Notes question is still due.
  await json(request, 'post', '/api/personal-assistant/hq', {
    request_id: 'greeting-notes-build',
    if_version: hired.state_version,
    name: 'My HQ',
    timezone: 'UTC',
    scope: 'all',
    include_future_workspaces: true,
    notes: { include: false }
  });
  const status = (await json(request, 'get', '/api/personal-hq/status')).status;
  expect(status.first_visit_state).toBe('pending');
  overviewPath = `/workspaces/${encodeURIComponent(status.workspace.folder_slug)}?mode=details`;
});

test('the greeting comes first; the Notes question waits and is not recorded as shown', async ({
  page
}) => {
  const prompted = countPrompted(page);
  await page.goto(overviewPath);
  await expect(page.locator('body')).toHaveAttribute('data-hq-first-visit', 'pending');
  await openDrawer(page);
  await expect(greeting(page)).toBeVisible();
  // Notes are off, so the greeting says so and offers to set them up.
  await expect(greeting(page)).toContainText(/Notes.* off until you set them up\./);
  await expect(greeting(page).locator('[data-hq-greeting-choice="notes"]')).toContainText(
    'Set up your notes'
  );
  // The question is behind it, and nothing was recorded as shown: give the page
  // time to have done it if it were going to.
  await expect(question(page)).toBeHidden();
  await page.waitForTimeout(2000);
  await expect(question(page)).toBeHidden();
  expect(prompted.count, 'held: never recorded as shown').toBe(0);
  // It never took focus.
  expect(
    await page.evaluate(() =>
      Boolean(
        document.activeElement?.closest(
          '#personalAssistantHQGreeting, #personalAssistantSecondBrain'
        )
      )
    )
  ).toBe(false);
});

test('"Set up your notes" in the greeting opens Notes setup in the drawer, an explicit ask', async ({
  page
}) => {
  const prompted = countPrompted(page);
  await page.goto(overviewPath);
  await openDrawer(page);
  await expect(greeting(page)).toBeVisible();
  await greeting(page).locator('[data-hq-greeting-choice="notes"]').click();
  await expect(question(page)).toBeVisible();
  // The drawer stayed open, and the greeting is still the first thing in it.
  await expect(panel(page)).toBeVisible();
  await expect(greeting(page)).toBeVisible();
  expect(prompted.count, 'an explicit open is not a first showing of the ask').toBe(0);
});

test('a message releases the question: it shows itself without a reload and is recorded once seen', async ({
  page
}) => {
  await page.route('**/api/chat', route => route.fulfill({ json: { response: 'Fixture reply.' } }));
  const prompted = countPrompted(page);
  await page.goto(overviewPath);
  await openDrawer(page);
  await expect(greeting(page)).toBeVisible();
  await expect(question(page)).toBeHidden();
  await page.locator('#personalAssistantInput').fill('Hello');
  await page.locator('#personalAssistantSend').click();
  await expect(greeting(page)).toBeHidden();
  // Released: the Notes question takes its turn, and only now is it recorded.
  await expect(question(page)).toBeVisible();
  await expect.poll(() => prompted.count).toBe(1);
  // It showed itself: focus is where the person left it, in the composer.
  expect(
    await page.evaluate(() =>
      Boolean(document.activeElement?.closest('#personalAssistantSecondBrain'))
    )
  ).toBe(false);
});
