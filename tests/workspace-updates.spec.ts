import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdtempSync, readFileSync, utimesSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Real host sources, sessions, filing and navigation. The existing goal wrapper
// supplies only a deterministic provider and disposable HOME/data:
// ./scripts/e2e-goal-suggestions.sh --lead-welcome -- tests/workspace-updates.spec.ts --grep 'Home updates:'
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
test.skip(!sandbox, 'requires the explicitly isolated goal fixture wrapper');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);

type Workspace = {
  id: string;
  folder_slug: string;
  name: string;
  agent_instances: { id: string; name: string }[];
};
let letters: Workspace;
let filing: Workspace;
let providerCallsBefore = 0;
const navigationWrites: string[] = [];

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), `${response.status()}: ${await response.text()}`).toBeTruthy();
  return response.json();
}
const updates = async (request: APIRequestContext, id: string) =>
  ok(await request.get(`/api/workspaces/${id}/updates`));
function modelCalls() {
  const path = join(sandbox, 'fixture-requests.jsonl');
  return existsSync(path)
    ? readFileSync(path, 'utf8').trim().split('\n').filter(Boolean).length
    : 0;
}
function recordNavigation(page: Page) {
  page.on('request', req => {
    if (req.method() === 'GET') return;
    const path = new URL(req.url()).pathname;
    if (
      /\/file-janitor\/(?:scan|prepare-review|decisions|preview|apply|undo)$|^\/api\/chat$|\/tasks\/execute$/.test(
        path
      )
    )
      navigationWrites.push(`${req.method()} ${path}`);
  });
}
async function home(page: Page) {
  await page.goto('/');
  await expect(page.locator('#cockpitMap .ws-map-tile').first()).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('[data-cockpit-workspace-update-badge]')).toBeVisible();
  // The existing economy/layout bootstrap mounts the world independently.
  // Exercise update refreshes after those original mount owners have settled.
  await expect(page.locator('#cockpitEconomy')).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => window.OriWorkspaceMap?.getLayoutState?.().status))
    .toBe('ready');
}

test.beforeAll(async ({ request }) => {
  await ok(await request.post('/api/onboarding/skip'));
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  await ok(
    await request.post('/api/settings/system-model', {
      data: { provider: 'ollama', model: 'fixture-model:latest' }
    })
  );
  letters = (
    await ok(
      await request.post('/api/workspaces', {
        data: { name: 'Updates · Letters', entry_agent_name: 'Ask Ori' }
      })
    )
  ).folder;
  filing = (
    await ok(
      await request.post('/api/workspaces', {
        data: { name: 'Updates · Filing', entry_agent_name: 'Ask Ori' }
      })
    )
  ).folder;
  await ok(
    await request.post(`/api/workspaces/${filing.id}/capabilities/file-janitor/install`, {
      data: { source: 'in-place' }
    })
  );
  await ok(
    await request.post(`/api/workspaces/${filing.id}/capabilities/file-janitor/companion`, {
      data: {
        agent_instance_id: filing.agent_instances.find(agent => agent.name === 'Ask Ori')!.id
      }
    })
  );
  const root = mkdtempSync(join(sandbox, 'updates-inbox-'));
  const old = new Date(Date.now() - 3_600_000);
  for (const name of ['report.pdf', 'uncertain.qqq']) {
    writeFileSync(join(root, name), 'synthetic workspace updates fixture', { mode: 0o600 });
    utimesSync(join(root, name), old, old);
  }
  await ok(
    await request.post(`/api/workspaces/${filing.id}/file-janitor/setup`, {
      data: { path: root, paused: true }
    })
  );
  await ok(await request.post(`/api/workspaces/${filing.id}/file-janitor/scan`, { data: {} }));
  // Explicit fixture bookkeeping, not execution: a waiting ticket provides an
  // independently owned attention count beside the new message/file labels.
  const { task } = await ok(
    await request.post('/api/orchestration/tasks', {
      data: { workspace_id: letters.id, description: 'Choose the synthetic letter outline' }
    })
  );
  await ok(
    await request.post(`/api/workspaces/${letters.id}/tickets/${task.id}/transition`, {
      data: { to: 'in_progress' }
    })
  );
  await ok(
    await request.put('/api/orchestration/tasks', {
      data: {
        task_id: task.id,
        status: 'waiting_for_choice',
        context: {
          human_loop: {
            state: 'waiting_for_choice',
            question: 'Which outline?',
            choices: [{ id: 'short', label: 'Short outline' }]
          }
        }
      }
    })
  );
  await ok(
    await request.patch('/api/workspace-map/layout', {
      data: {
        operations: [
          {
            op: 'set_positions',
            positions: { [letters.id]: { x: 380, y: 380 }, [filing.id]: { x: 608, y: 380 } }
          }
        ]
      }
    })
  );
  for (const ws of [letters, filing])
    await expect
      .poll(async () => (await updates(request, ws.id)).welcome.unread_count, { timeout: 45_000 })
      .toBeGreaterThan(0);
  expect((await updates(request, filing.id)).review.pending_count).toBe(2);
  providerCallsBefore = modelCalls();
});

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  recordNavigation(page);
});
test.afterEach(async ({ page }) => {
  // Delayed real-read fixtures must finish before Playwright disposes their
  // request context; cancellation on teardown is not a product response.
  await page.unrouteAll({ behavior: 'wait' });
  expect(navigationWrites).toEqual([]);
  expect(modelCalls()).toBe(providerCallsBefore);
});

test('Home updates: distinct rows open current Review without opening or acknowledging chat', async ({
  page,
  request
}, testInfo) => {
  const unread = (await updates(request, filing.id)).welcome.unread_count;
  await home(page);
  const lettersTile = page.locator(`#cockpitMap [data-ws-id="${letters.id}"]`);
  const filingTile = page.locator(`#cockpitMap [data-ws-id="${filing.id}"]`);
  await expect(lettersTile).toContainText('New message');
  await expect(filingTile).toContainText('2 files to review');
  await expect(page.locator('[data-cockpit-rail-toggle-count]')).toHaveText('1');
  await page.screenshot({
    path: testInfo.outputPath('home-map-distinct-updates.png'),
    fullPage: true
  });
  await page.getByRole('button', { name: 'Show Updates', exact: true }).click();
  const row = page.locator(`#cockpitWorkspaceUpdates [data-update-row="${filing.id}"]`);
  await expect(row).toContainText('New message');
  await expect(row).toContainText('2 files to review');
  await expect(page.locator('#cockpitTodayAttention')).toContainText('1 ticket needing attention');
  await page.screenshot({ path: testInfo.outputPath('home-update-rows.png'), fullPage: true });
  await row.locator('[data-workspace-update="review"]').click();
  await expect(page.locator('#fileJanitorConsoleDialog')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('report.pdf');
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  expect(new URL(page.url()).searchParams.has('update')).toBe(false);
  expect((await updates(request, filing.id)).welcome.unread_count).toBe(unread);
  expect((await updates(request, filing.id)).review.pending_count).toBe(2);
  await page.screenshot({
    path: testInfo.outputPath('home-arrival-review-chat-closed.png'),
    fullPage: true
  });
});

test('Home updates: keyboard Map message arrival exposes only its saved conversation; return keeps filing and ticket counts', async ({
  page,
  request
}, testInfo) => {
  // Reproduce an arrival faster than the recipient's Personal HQ lookup. It
  // must wait for identity readiness, not consume the intent as unavailable.
  await page.route(/\/api\/personal-assistant$/, async route => {
    const arrival =
      new URL(route.request().frame().url()).pathname === `/workspaces/${letters.folder_slug}`;
    const response = await route.fetch();
    const reply = {
      status: response.status(),
      headers: response.headers(),
      body: await response.body()
    };
    if (arrival) await new Promise(resolve => setTimeout(resolve, 500));
    await route.fulfill(reply);
  });
  const before = await updates(request, letters.id);
  const otherUnread = (await updates(request, filing.id)).welcome.unread_count;
  await home(page);
  const tile = page.locator(`#cockpitMap .ws-map-tile[data-ws-id="${letters.id}"]`);
  await tile.focus();
  await page.keyboard.press('Shift+F10');
  const message = page.getByRole('menuitem', { name: 'New message', exact: true });
  await expect(message).toBeVisible();
  const announcement = await page.locator('#cockpitRailLive').textContent();
  await page.evaluate(async () => {
    const { workspaceUpdates } = await import('/js/modules/workspace-updates.js');
    await workspaceUpdates.refreshPage();
  });
  await expect(message).toBeVisible();
  await expect(page.locator('#cockpitRailLive')).toHaveText(announcement || '');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  await expect(message).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('#personalAssistantPanel')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('#personalAssistantWorkspaceContext')).toHaveText(
    `Context · ${letters.name}`
  );
  await expect(page.locator('#homeAssistantConversation')).toContainText('Hi, I’m Ask Ori');
  await expect(
    page.locator(`[data-lead-session-id="${before.welcome.session_id}"]`).first()
  ).toBeVisible();
  await expect.poll(async () => (await updates(request, letters.id)).welcome.unread_count).toBe(0);
  expect((await updates(request, filing.id)).welcome.unread_count).toBe(otherUnread);
  await page.goBack();
  await expect(
    page.locator(`#cockpitMap .ws-map-tile[data-ws-id="${letters.id}"]`)
  ).not.toContainText('New message');
  await expect(page.locator(`#cockpitMap .ws-map-tile[data-ws-id="${filing.id}"]`)).toContainText(
    '2 files to review'
  );
  await expect(page.locator('[data-cockpit-rail-toggle-count]')).toHaveText('1');
  await page.screenshot({
    path: testInfo.outputPath('home-return-independent-signals.png'),
    fullPage: true
  });
});

test('Home updates: existing library presentation remains separate from real message, filing and ticket sources', async ({
  page
}, testInfo) => {
  // Controlled fixture for the UNCHANGED library surface only. No plugin is
  // installed, and the new workspace-update sources still use the real host.
  const libraryID = 'library-baseline-fixture';
  await page.route('**/api/workspaces?tree=true', async route => {
    const response = await route.fetch();
    const body = await response.json();
    body.folders.push({
      id: libraryID,
      name: 'Library baseline fixture',
      folder_slug: libraryID,
      kind: 'group',
      children: [],
      group_template: { kind: 'managed_home' }
    });
    await route.fulfill({ response, json: body });
  });
  await page.route('**/api/workspace-map/layout', async route => {
    if (route.request().method() !== 'GET') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    body.layout.groups = {
      ...body.layout.groups,
      [libraryID]: { sizing_mode: 'custom', frame: { x: 1000, y: 380, width: 450, height: 300 } }
    };
    await route.fulfill({ response, json: body });
  });
  await page.route(`**/api/workspaces/${libraryID}/assistant-program/library/summary`, route =>
    route.fulfill({
      json: {
        digest: { activatable: 4 },
        ready_proposals: 3,
        route: `/workspaces/${libraryID}/assistant#projectLibraryProposals`,
        last_complete_scan_at: new Date().toISOString()
      }
    })
  );
  await home(page);
  const library = page.locator(`[data-library-badge="${libraryID}"]`);
  await expect(library).toContainText('4 to set up · 3 to review');
  await page.getByRole('button', { name: 'Fit all', exact: true }).click();
  await expect(library).toHaveAttribute(
    'data-library-route',
    `/workspaces/${libraryID}/assistant#projectLibraryProposals`
  );
  await expect(page.locator(`#cockpitMap .ws-map-tile[data-ws-id="${filing.id}"]`)).toContainText(
    '2 files to review'
  );
  await expect(page.locator('[data-cockpit-rail-toggle-count]')).toHaveText('1');
  await page.screenshot({
    path: testInfo.outputPath('library-baseline-coexistence.png'),
    fullPage: true
  });
});

test('Home updates: Tree keyboard navigation survives a qualified outage and retry without clearing pending work', async ({
  page,
  request
}, testInfo) => {
  let failed = false;
  await page.route('**/api/workspace-updates?*', async route => {
    if (failed) await route.fulfill({ status: 503, json: { error: 'fixture unavailable' } });
    else await route.continue();
  });
  await home(page);
  await page.getByRole('button', { name: 'Show Updates', exact: true }).click();
  await expect(page.locator('#cockpitWorkspaceUpdates')).toContainText('2 files to review');
  failed = true;
  await page.locator('[data-workspace-updates-refresh]').click();
  await expect(page.locator('#cockpitWorkspaceUpdates')).toContainText(
    'Previous results may be out of date'
  );
  await expect(page.locator('#cockpitWorkspaceUpdates')).toContainText(
    '2 files to review · last known'
  );
  await expect(
    page.locator('#cockpitWorkspaceUpdates [data-workspace-update="review"]')
  ).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath('home-qualified-unavailable.png'),
    fullPage: true
  });
  failed = false;
  await page.locator('[data-workspace-updates-refresh]').click();
  await expect(
    page.locator(
      `#cockpitWorkspaceUpdates [data-update-row="${filing.id}"] [data-workspace-update="review"]`
    )
  ).toHaveCount(1);
  await page.getByRole('button', { name: 'Hide Updates', exact: true }).click();
  await page.locator('[data-cockpit-view="tree"]').click();
  const row = page.locator(`#cockpitTreeNav [data-tree-row="${filing.id}"]`);
  await row.focus();
  const review = row.locator('[data-workspace-update="review"]');
  // Roving row navigation makes its two separate update links reachable by Tab.
  await page.keyboard.press('ArrowUp');
  await row.focus();
  await page.keyboard.press('Tab');
  if (!(await review.evaluate(el => el === document.activeElement)))
    await page.keyboard.press('Tab');
  await expect(review).toBeFocused();
  await page.screenshot({ path: testInfo.outputPath('tree-keyboard-updates.png'), fullPage: true });
  const unread = (await updates(request, filing.id)).welcome.unread_count;
  await page.keyboard.press('Enter');
  await expect(page.locator('#fileJanitorConsoleDialog')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  expect((await updates(request, filing.id)).welcome.unread_count).toBe(unread);
  expect((await updates(request, filing.id)).review.pending_count).toBe(2);
  writeFileSync(
    testInfo.outputPath('navigation-operations.json'),
    JSON.stringify(
      {
        authorized_fixture_setup: 1,
        authorized_fixture_scans: 1,
        navigation_execution_writes: navigationWrites,
        provider_calls_before_navigation: providerCallsBefore,
        provider_calls_after_navigation: modelCalls()
      },
      null,
      2
    )
  );
});
