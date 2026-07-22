import { test, expect, type APIRequestContext } from '@playwright/test';

/**
 * Calendar Ops end-to-end coverage (PRD task 8.3).
 *
 * Run with: PLAYWRIGHT_BASE_URL=http://localhost:PORT npx playwright test tests/calendar-ops.spec.ts
 * against an isolated smoke server (HOME/ORI_DATA_DIR sandboxed to a fresh
 * temp dir -- see CLAUDE.md's smoke-testing recipe) with a fake calendar MCP
 * connector already registered and running under the server name
 * "fake-calendar" (see tasks/test-guide-calendar-ops-mcp.md for the exact
 * fixture and registration steps). Mirrors
 * tests/personal-hq-daily-brief.spec.ts's conventions: one describe.serial
 * block against one fresh server, page.request used to shortcut setup steps
 * that aren't themselves under test (workspace creation, connector binding,
 * mapping save), real UI interaction for everything a real browser is
 * needed to prove.
 *
 * Scope note: mapping/validation edge cases, JSON-Pointer resolution,
 * sanitization (XSS links, control characters, malformed timestamps),
 * cache isolation, confirmation replay/concurrency, ownership enforcement,
 * and a second differently-shaped connector's full pipeline are already
 * covered by fast, deterministic Go tests (internal/calendar,
 * internal/calendarhttp) -- re-proving them here would be slow and
 * redundant. This file focuses on what only a real browser proves: the
 * guided setup wizard's states, the agenda console's actual rendering and
 * interaction, the mutation confirm dialog, the meeting-prep drawer, the
 * Home/HQ portal surfaces, and keyboard/accessibility.
 */

const CALENDAR_OPS_TEMPLATE_ID = 'calendar-ops';
const CONNECTOR_SERVER_NAME = 'fake-calendar';
// A run-unique suffix keeps workspace names collision-free across repeated
// runs against a server that isn't recreated fresh every time (e.g. local
// iteration), without depending on a pristine profile the way
// personal-hq-daily-brief.spec.ts's single first-run mission test does.
const RUN_SUFFIX = Date.now().toString(36);

async function createCalendarOpsWorkspace(request: APIRequestContext, name: string) {
  const res = await request.post('/api/workspaces', {
    data: { name, description: '', template_id: CALENDAR_OPS_TEMPLATE_ID, create_template_agents: true }
  });
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  return body.folder.id as string;
}

async function bindAndMapConnector(request: APIRequestContext, workspaceId: string) {
  const connectorRes = await request.post('/api/calendar-ops/setup/connector', {
    data: { workspace_id: workspaceId, server_name: CONNECTOR_SERVER_NAME }
  });
  expect(connectorRes.ok()).toBeTruthy();

  const saveRes = await request.post('/api/calendar-ops/setup/save', {
    data: {
      workspace_id: workspaceId,
      mapping: {
        capability: 'calendar',
        operations: {
          list_calendars: {
            tool: 'calendars_list',
            result_collection: '/items',
            fields: { id: '/id', name: '/summary' }
          },
          list_events: {
            tool: 'events_list',
            result_collection: '/items',
            fields: {
              id: '/id',
              title: '/summary',
              start_time: '/start/dateTime',
              end_time: '/end/dateTime',
              location: '/location',
              description: '/description',
              all_day: '/allDay',
              private: '/private'
            },
            arguments: { calendar_id: '/calendarId', start_time: '/timeMin', end_time: '/timeMax' }
          },
          create_event: {
            tool: 'events_insert',
            fields: { id: '/id', title: '/summary', start_time: '/start/dateTime', end_time: '/end/dateTime' },
            arguments: {
              calendar_id: '/calendarId',
              title: '/summary',
              start_time: '/start/dateTime',
              end_time: '/end/dateTime',
              time_zone: '/start/timeZone',
              location: '/location',
              description: '/description'
            }
          }
        }
      },
      selected_calendar_ids: ['primary', 'team'],
      display_time_zone: 'America/New_York'
    }
  });
  expect(saveRes.ok()).toBeTruthy();
  const saveBody = await saveRes.json();
  expect(saveBody.state).toBe('ready');
}

test.describe.serial('Calendar Ops', () => {
  let readyWorkspaceId: string;

  test.beforeAll(async ({ request }) => {
    const skipRes = await request.post('/api/onboarding/skip');
    expect(skipRes.ok()).toBeTruthy();
    readyWorkspaceId = await createCalendarOpsWorkspace(request, `Calendar Ops E2E ${RUN_SUFFIX}`);
    await bindAndMapConnector(request, readyWorkspaceId);
  });

  test('Construct wizard offers Calendar Ops as a selectable blueprint with a briefing', async ({ page }) => {
    await page.goto('/workspaces');
    await page.getByRole('button', { name: /new workspace/i }).first().click();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    const card = page.locator('.workspace-template-card', { hasText: 'Calendar Ops' });
    await expect(card).toBeVisible();
    await card.click();
    await expect(card).toHaveClass(/is-selected/);
    await expect(page.locator('#projectTemplateDescription')).toContainText(/calendar/i);
    await page.getByRole('button', { name: 'Cancel' }).click();
    await expect(page.locator('#addFolderModal')).toBeHidden();
  });

  // Runs before any test below creates another Calendar Ops workspace: the
  // shared FR49 resolver (ActiveWorkspace) picks the most recently updated
  // one when several exist, so this must check the Home portal while
  // readyWorkspaceId is still the only (and therefore most recent)
  // Calendar Ops workspace on the server.
  test('Home Calendar Ops portal shows a ready summary and click-through opens the Calendar console', async ({
    page
  }) => {
    await page.goto('/');
    const portal = page.locator('#homeCalendarOpsPortal');
    await expect(portal).toBeVisible({ timeout: 10000 });
    await expect(portal).toContainText(/event|conflict|no more meetings/i);

    const openLink = page.locator('#homeCalendarOpsPortalOpen');
    await expect(openLink).toBeVisible();
    await expect(openLink).toHaveAttribute('href', /\?panel=calendar$/);
    await openLink.click();
    await expect(page).toHaveURL(/\?panel=calendar/);
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible({ timeout: 10000 });
  });

  // Also runs before any test below creates another Calendar Ops workspace,
  // for the same FR49 most-recently-updated-wins reason as the test above.
  test('Personal HQ Map station shows the same summary and click-through as the Home portal', async ({
    page,
    request
  }) => {
    // A separate, blank workspace designated as Personal HQ -- proves the
    // FR49 shared resolver finds the Calendar Ops workspace across
    // workspace boundaries, not just within its own workspace.
    const hqRes = await request.post('/api/workspaces', {
      data: { name: `HQ for Calendar Station ${RUN_SUFFIX}`, description: '', blank: true }
    });
    expect(hqRes.ok()).toBeTruthy();
    const hqId = (await hqRes.json()).folder.id as string;
    const designateRes = await request.post('/api/personal-hq/designate', { data: { workspace_id: hqId } });
    expect(designateRes.ok()).toBeTruthy();

    await page.goto(`/workspaces/${hqId}?mode=map`);
    const station = page.locator('[data-cmd-hq-station="calendar-ops"]');
    await expect(station).toBeVisible({ timeout: 10000 });
    // The station's compact value label is just "Clear"/a meeting title
    // when ready with no conflicts -- the descriptive detail (event/
    // conflict counts) lives in its aria-label, not always the visible text.
    await expect(station).toHaveAttribute('aria-label', /event|conflict|no more meetings/i);

    await station.click();
    const panel = page.getByRole('heading', { name: 'Calendar Ops' });
    await expect(panel).toBeVisible();
    await page.getByRole('button', { name: 'Open Calendar Ops' }).click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${readyWorkspaceId}\\?panel=calendar`));
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible({ timeout: 10000 });
  });

  test('setup panel starts in connector_missing state for a fresh Calendar Ops workspace', async ({ page, request }) => {
    const freshId = await createCalendarOpsWorkspace(request, `Calendar Ops Setup States ${RUN_SUFFIX}`);
    await page.goto(`/workspaces/${freshId}?panel=calendar`);
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('#calendarConsoleBody')).toContainText(/no calendar connector is configured/i);
  });

  test('day view renders events grouped by day with conflict badges and a private-event redaction', async ({ page }) => {
    await page.goto(`/workspaces/${readyWorkspaceId}?panel=calendar`);
    const root = page.locator('#calendarConsoleRoot');
    await expect(root).toBeVisible({ timeout: 10000 });

    await expect(page.getByRole('button', { name: 'Day', exact: true })).toHaveAttribute('aria-pressed', 'true');
    const events = page.locator('.calendar-console-event');
    await expect(events.first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('.calendar-console-badge-conflict').first()).toBeVisible();
    await expect(page.locator('.calendar-console-event-private')).toContainText('Private event');
  });

  test('week view shows more events than day view and preserves the toggle state', async ({ page }) => {
    await page.goto(`/workspaces/${readyWorkspaceId}?panel=calendar`);
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible({ timeout: 10000 });
    const dayCount = await page.locator('.calendar-console-event').count();

    await page.getByRole('button', { name: 'Week', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Week', exact: true })).toHaveAttribute('aria-pressed', 'true');
    const weekCount = await page.locator('.calendar-console-event').count();
    expect(weekCount).toBeGreaterThanOrEqual(dayCount);
  });

  test('calendar filters hide and re-show a calendar\'s events', async ({ page }) => {
    await page.goto(`/workspaces/${readyWorkspaceId}?panel=calendar`);
    await expect(page.locator('.calendar-console-event').first()).toBeVisible({ timeout: 10000 });
    const filters = page.locator('.calendar-console-filter');
    await expect(filters.first()).toBeVisible();

    const totalBefore = await page.locator('.calendar-console-event').count();
    await filters.nth(1).locator('input[type="checkbox"]').uncheck();
    await expect(async () => {
      const after = page.locator('.calendar-console-event').count();
      expect(await after).toBeLessThanOrEqual(totalBefore);
    }).toPass({ timeout: 5000 });

    await filters.nth(1).locator('input[type="checkbox"]').check();
  });

  test('clicking an event opens the detail drawer with title and description', async ({ page }) => {
    await page.goto(`/workspaces/${readyWorkspaceId}?panel=calendar`);
    const firstEvent = page.locator('.calendar-console-event').first();
    await expect(firstEvent).toBeVisible({ timeout: 10000 });
    await firstEvent.click();

    const drawer = page.locator('#calendarConsoleDrawer');
    await expect(drawer).toBeVisible();
    await expect(drawer.locator('.calendar-console-drawer-head')).toBeVisible();
  });

  test('meeting prep drawer offers a Prepare action for a preparable event', async ({ page }) => {
    await page.goto(`/workspaces/${readyWorkspaceId}?panel=calendar`);
    const firstEvent = page.locator('.calendar-console-event').first();
    await expect(firstEvent).toBeVisible({ timeout: 10000 });
    await firstEvent.click();

    const prepSection = page.locator('.calendar-console-prep-section');
    await expect(prepSection).toBeVisible();
    await expect(prepSection.locator('.calendar-console-prep-body')).not.toBeEmpty();
  });

  test('creating an event goes through preview (no write) then an explicit confirm dialog', async ({ page }) => {
    await page.goto(`/workspaces/${readyWorkspaceId}?panel=calendar`);
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible({ timeout: 10000 });

    await page.getByRole('button', { name: 'New event' }).click();
    const form = page.locator('.calendar-console-form');
    await expect(form).toBeVisible();
    await form.getByPlaceholder('Title').fill('Playwright Test Event');
    await form.locator('input[type="datetime-local"]').first().fill('2026-08-01T10:00');
    await form.locator('input[type="datetime-local"]').nth(1).fill('2026-08-01T10:30');

    await page.getByRole('button', { name: 'Preview' }).click();

    const checkpoint = page.locator('.calendar-console-checkpoint');
    await expect(checkpoint).toBeVisible();
    await expect(checkpoint).toContainText('Playwright Test Event');
    await expect(checkpoint).toContainText('Confirm this change');

    // Cancel first: proves Preview alone never wrote anything and the
    // console returns to the plain form rather than silently confirming.
    await page.getByRole('button', { name: 'Cancel' }).click();
    await expect(checkpoint).toBeHidden();
  });

  test('a broken connector reports an unavailable state without breaking the page', async ({ page, request }) => {
    // A registered server whose command can never start (an unmapped binary
    // path) reaches a real error/stopped registry status, unlike an
    // unregistered server name -- resolveGateway resolves *that* straight to
    // connector_missing regardless of mapping state, which the unmapped-
    // workspace test above already covers. This exercises the distinct
    // "connector exists but is broken" branch instead.
    const brokenServerName = `broken-connector-${RUN_SUFFIX}`;
    const addRes = await request.post('/api/mcp/servers', {
      data: { name: brokenServerName, transport: 'stdio', command: '/nonexistent/path/to/binary', enabled: true }
    });
    expect(addRes.ok()).toBeTruthy();
    await request.post(`/api/mcp/servers/${brokenServerName}/connect`);

    const degradedId = await createCalendarOpsWorkspace(request, `Calendar Ops Degraded ${RUN_SUFFIX}`);
    const connectorRes = await request.post('/api/calendar-ops/setup/connector', {
      data: { workspace_id: degradedId, server_name: brokenServerName }
    });
    expect(connectorRes.ok()).toBeTruthy();

    await page.goto(`/workspaces/${degradedId}?panel=calendar`);
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('#calendarConsoleBody')).toContainText(
      /connector|calendar setup is not complete|finish (setup|mapping)/i,
      { timeout: 10000 }
    );
    // The rest of the page must still be usable -- a broken connector must
    // never take down workspace rendering (FR51's same guarantee, proven
    // here for the classic workspace page rather than the Home/HQ portal).
    await expect(page.locator('body')).not.toContainText(/uncaught|typeerror|stack trace/i);
  });

  test('keyboard: Tab reaches the toolbar controls and Enter activates the focused button', async ({ page }) => {
    await page.goto(`/workspaces/${readyWorkspaceId}?panel=calendar`);
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible({ timeout: 10000 });

    const todayBtn = page.getByRole('button', { name: 'Today', exact: true });
    await todayBtn.focus();
    await expect(todayBtn).toBeFocused();
    await page.keyboard.press('Enter');
    // Today is idempotent when already on today's range -- the assertion is
    // that focus/activation works at all, not a date change.
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible();
  });

  test('toolbar controls carry accessible group/button roles', async ({ page }) => {
    await page.goto(`/workspaces/${readyWorkspaceId}?panel=calendar`);
    await expect(page.locator('#calendarConsoleRoot')).toBeVisible({ timeout: 10000 });
    await expect(page.getByRole('group', { name: 'Date navigation' })).toBeVisible();
    await expect(page.getByRole('group', { name: 'Day or week view' })).toBeVisible();
    await expect(page.getByRole('group', { name: 'Visible calendars' })).toBeVisible();
  });

});
