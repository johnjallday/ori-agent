import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * Today's meetings on Today and in the Daily Brief (Issue #533), end to end.
 *
 * One fresh, isolated server for the whole file (describe.serial): a hire that
 * asked to be prepared for meetings sees Mission 04's "Connect your calendar"
 * nudge; after the in-repo fake calendar is connected through Calendar Ops,
 * Today lists today's meetings with the overlapping pair flagged, a meeting
 * opens in the Calendar console's drawer with "Prepare me", and the Daily
 * Brief leads with Today's Meetings.
 *
 * Run (sandbox-off; Chromium cannot launch inside the agent sandbox):
 *
 *   eval "$(./scripts/demo-calendar-fixture.sh --build-only)"
 *   FAKE_CALENDAR_MCP_BIN="$FAKE_CALENDAR_MCP_BIN" \
 *     ./scripts/e2e-fresh.sh tests/personal-assistant-meetings.spec.ts -- --workers=1
 *
 * Without FAKE_CALENDAR_MCP_BIN only the not-connected test runs.
 *
 * The fixture builds its meetings in the server's local time, so the calendar
 * display timezone defaults to this machine's zone (the spec and the server run
 * on the same machine); CALENDAR_DISPLAY_TZ overrides it, as in the script.
 */

const CONNECTOR = 'fake-calendar';
const FIXTURE_BIN = process.env.FAKE_CALENDAR_MCP_BIN || '';
const DISPLAY_TZ =
  process.env.CALENDAR_DISPLAY_TZ || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

// stubDetection keeps the domain-specialist offer out of Today, so nothing
// here depends on what happens to be installed on the host.
async function stubDetection(page: Page) {
  await page.route('**/api/onboarding/detect', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, platform: 'darwin', apps: [] })
    })
  );
}

async function openToday(page: Page) {
  const launcher = page.locator('#personalAssistantLauncher');
  await expect(launcher).toBeVisible();
  if (await page.locator('#personalAssistantPanel').isHidden()) await launcher.click();
  const todayTab = page.locator('#personalAssistantTodayTab');
  if ((await todayTab.getAttribute('aria-selected')) !== 'true') await todayTab.click();
  await expect(page.locator('#personalAssistantToday')).toBeVisible();
}

// connectFakeCalendar is scripts/demo-calendar-fixture.sh in API calls: the
// connector, a Calendar Ops workspace, and tests/calendar-ops.spec.ts's mapping.
async function connectFakeCalendar(request: APIRequestContext) {
  const add = await request.post('/api/mcp/servers', {
    data: { name: CONNECTOR, transport: 'stdio', command: FIXTURE_BIN, enabled: true }
  });
  expect(add.ok(), await add.text()).toBeTruthy();
  await request.post(`/api/mcp/servers/${CONNECTOR}/connect`);
  await expect
    .poll(
      async () => (await (await request.get(`/api/mcp/servers/${CONNECTOR}/status`)).json()).status,
      {
        timeout: 15000
      }
    )
    .toBe('running');

  const created = await request.post('/api/workspaces', {
    data: {
      name: 'Calendar Ops',
      description: '',
      template_id: 'calendar-ops',
      create_template_agents: true
    }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const workspaceId = (await created.json()).folder.id as string;

  const bind = await request.post('/api/calendar-ops/setup/connector', {
    data: { workspace_id: workspaceId, server_name: CONNECTOR }
  });
  expect(bind.ok(), await bind.text()).toBeTruthy();
  const save = await request.post('/api/calendar-ops/setup/save', {
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
          }
        }
      },
      selected_calendar_ids: ['primary', 'team'],
      display_time_zone: DISPLAY_TZ
    }
  });
  expect(save.ok(), await save.text()).toBeTruthy();
  expect((await save.json()).state).toBe('ready');
}

test.describe.serial("Today's meetings", () => {
  test.beforeAll(async ({ request }) => {
    expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
    expect(
      (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
    ).toBeTruthy();
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'meetings-e2e-hire',
        if_version: 0,
        display_name: 'Atlas',
        mandate: 'Get me ready for my day.',
        focus_areas: ['prepare_for_meetings']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    const hired = (await hire.json()).personal_assistant;
    const hq = await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'meetings-e2e-hq',
        if_version: hired.state_version,
        name: 'My HQ',
        timezone: DISPLAY_TZ
      }
    });
    expect(hq.status(), await hq.text()).toBe(201);
  });

  test('before a calendar is connected, Today nudges toward Mission 04 without going partial', async ({
    page
  }) => {
    await stubDetection(page);
    await page.goto('/');
    await openToday(page);
    const meetings = page.locator('#personalAssistantTodayMeetingsSection');
    await expect(meetings).toBeVisible();
    await expect(meetings).toContainText('Connect a calendar and Today will list your meetings.');
    await expect(meetings.getByRole('link', { name: 'Connect your calendar' })).toHaveAttribute(
      'href',
      '/?create=1&blueprint=calendar-ops'
    );
    await expect(page.locator('#personalAssistantToday')).not.toHaveAttribute(
      'data-state',
      'partial'
    );
  });

  test('a connected calendar lists today with overlaps, and a meeting opens with Prepare me', async ({
    page,
    request
  }) => {
    test.skip(
      !FIXTURE_BIN,
      'set FAKE_CALENDAR_MCP_BIN (./scripts/demo-calendar-fixture.sh --build-only)'
    );
    await connectFakeCalendar(request);

    await stubDetection(page);
    await page.goto('/');
    await openToday(page);
    const meetings = page.locator('#personalAssistantTodayMeetingsSection');
    await expect(page.locator('#personalAssistantTodayMeetingsTitle')).toContainText('2 overlaps');
    await expect(
      meetings.locator('.personal-assistant-today__badge[data-state="conflict"]')
    ).toHaveCount(2);
    await expect(meetings).toContainText('Private event');
    await expect(meetings).not.toContainText('Dentist');
    // Tomorrow's Retro is on the connector but not on today's agenda.
    await expect(meetings).not.toContainText('Retro');

    await meetings.getByRole('link', { name: 'Design review' }).click();
    await expect(page).toHaveURL(/\/workspaces\/calendar-ops\?/);
    const drawer = page.locator('#calendarConsoleDrawer');
    await expect(drawer).toBeVisible({ timeout: 15000 });
    await expect(drawer).toContainText('Design review');
    await expect(drawer.getByRole('button', { name: 'Prepare me' })).toBeVisible();
  });

  test("the Daily Brief leads with Today's Meetings once a calendar is connected", async ({
    page
  }) => {
    test.skip(
      !FIXTURE_BIN,
      'set FAKE_CALENDAR_MCP_BIN (./scripts/demo-calendar-fixture.sh --build-only)'
    );
    await stubDetection(page);
    await page.goto('/');
    await openToday(page);
    await page.locator('#homeDailyBriefRefreshBtn').click();
    const body = page.locator('#homeDailyBriefBody');
    await expect(body).toContainText("Today's Meetings", { timeout: 30000 });
    await expect(body).toContainText('Design review');
    await expect(body.locator('.home-daily-brief-badge[data-state="conflict"]')).toHaveCount(2);
    await expect(body).not.toContainText('Dentist');
  });
});
