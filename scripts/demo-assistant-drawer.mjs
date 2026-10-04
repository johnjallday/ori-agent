// Drives the assistant-drawer redesign in a disposable demo sandbox and saves
// screenshots, one stage at a time (tasks/tasks-assistant-drawer-redesign.md).
//
//   node scripts/demo-assistant-drawer.mjs <base-url> <shots-dir> <stage> [theme]
//
// Stages:
//   station   the Daily Brief station in My HQ: map, Stations rail, panel,
//             Refresh, Brief settings, the direct link, a drag, narrow layout
//   brief-home  the station as the brief's home: the mission card's link, the
//             mission completing when the panel shows a brief, earlier briefs,
//             and the Action Center link
//   drawer    the assistant drawer on Home as one view: at rest, the More
//             menu, the progress row, Needs you (sample data), paused, narrow,
//             /?panel=today, and the brief row opening the station
//   drawer-setup  the drawer before the assistant can accept work: before
//             hiring, then hired with no HQ. Needs a FRESH sandbox.
//   folder    the folder flow in the conversation: the chip, the chooser, the
//             scene, the offer, Later, the summary strip, a reload onto an
//             unanswered offer, Set up, the toolbar button and the link.
//             Seed folders first:
//               ./scripts/smoke.sh showfolder <base-url> seed <sandbox>
//   folder-first  the assistant's one first-folder prompt. Run it against the
//             sandbox drawer-setup left (hired, no HQ).
//   other-pages  the drawer on a workspace page and on the Agents page: the
//             header, the "N need you" line, the chip landing on Home, and
//             sending a request.
//
// Seed the server first: ./scripts/smoke.sh showfolder <base-url> hq
// theme is "light" (default) or "dark".
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const [base, output, stage = 'station', theme = 'light'] = process.argv.slice(2);
if (!base || !output) {
  throw new Error('usage: demo-assistant-drawer.mjs <base-url> <shots-dir> <stage> [light|dark]');
}
mkdirSync(output, { recursive: true });

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
await context.addInitScript(value => {
  try {
    localStorage.setItem('ori-theme', value);
  } catch (_) {
    /* a page without storage keeps its default theme */
  }
}, theme);
const page = await context.newPage();
const problems = [];
page.on('pageerror', error => problems.push(`pageerror: ${error.message}`));
page.on('console', message => {
  // A failed request is reported below with its address, which this is not.
  if (message.type() === 'error' && !/Failed to load resource/.test(message.text())) {
    problems.push(`console: ${message.text()}`);
  }
});
page.on('response', response => {
  if (response.status() >= 400) {
    problems.push(`HTTP ${response.status()}: ${response.request().method()} ${response.url()}`);
  }
});

async function shot(name) {
  const path = join(output, `${stage}-${theme}-${name}.png`);
  await page.screenshot({ path });
  console.log(path);
}

function check(condition, message) {
  if (!condition) throw new Error(message);
  console.log(`ok   ${message}`);
}

// Two time zones that are one calendar day apart right now. A brief made in the
// earlier one and then moved to the later one gives two days of real history
// without waiting for midnight; dates only ever move forward.
const [ZONE_EARLIER, ZONE_LATER] =
  new Date().getUTCHours() < 11 ? ['Pacific/Pago_Pago', 'UTC'] : ['UTC', 'Pacific/Kiritimati'];

async function personalHQ() {
  const read = async () =>
    (await (await page.request.get(`${base}/api/personal-hq/status`)).json())?.status;
  let status = await read();
  if (!status?.workspace?.folder_slug) {
    // A fresh sandbox: hire the assistant and build the HQ, in the earlier zone.
    await page.request.post(`${base}/api/onboarding/skip`);
    await page.request.post(`${base}/api/settings/workspace-root`, {
      data: { workspace_root: '' }
    });
    const assistant = async () =>
      (await (await page.request.get(`${base}/api/personal-assistant`)).json()).personal_assistant;
    await page.request.post(`${base}/api/personal-assistant/hire`, {
      data: {
        request_id: 'drawer-demo-hire',
        if_version: (await assistant()).state_version ?? 0,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    });
    await page.request.post(`${base}/api/personal-assistant/hq`, {
      data: {
        request_id: 'drawer-demo-hq',
        if_version: (await assistant()).state_version,
        name: 'My HQ',
        timezone: ZONE_EARLIER
      }
    });
    status = await read();
    console.log(`seeded a hired assistant and My HQ (${ZONE_EARLIER})`);
  }
  const slug = status?.workspace?.folder_slug;
  if (!slug) throw new Error('could not find or build a Personal HQ in this sandbox');
  return { slug, id: status.workspace_id };
}

async function applyTheme() {
  await page.evaluate(value => {
    document.documentElement.setAttribute('data-bs-theme', value);
  }, theme);
}

async function station() {
  const { slug, id: hqId } = await personalHQ();
  const brief = '[data-cmd-hq-station="daily-brief"]';
  const panel = '.ws-cmd-modal-panel.is-daily-brief';

  // Map: the building sits in the fourth slot and the older three are unmoved.
  await page.goto(`${base}/workspaces/${slug}?mode=map`);
  await page.locator(brief).waitFor({ state: 'visible' });
  await applyTheme();
  const slots = await page.evaluate(() =>
    Object.fromEntries(
      Array.from(document.querySelectorAll('.ws-cmd-map-hq-station')).map(el => [
        el.getAttribute('data-cmd-hq-station'),
        [el.style.getPropertyValue('--station-x'), el.style.getPropertyValue('--station-y')]
      ])
    )
  );
  console.log('station slots:', JSON.stringify(slots));
  check(
    slots['daily-brief']?.[1] === '73.00%',
    'Daily Brief takes the fourth default slot (y 73%)'
  );
  check(
    slots.watchtower?.[1] === '22.00%' &&
      slots.email?.[1] === '39.00%' &&
      slots['calendar-ops']?.[1] === '56.00%',
    'Watchtower, Email and Calendar Ops keep their default slots'
  );
  check(
    (await page.locator(brief).getAttribute('data-station-visual')) === 'briefing',
    'the station uses the briefing building art'
  );
  await page.waitForFunction(
    selector => !/Loading/.test(document.querySelector(selector)?.textContent || ''),
    brief
  );
  console.log(
    'station status:',
    await page.locator(`${brief} .ws-cmd-map-hq-station-state`).textContent()
  );
  await page.locator(brief).scrollIntoViewIfNeeded();
  await shot('01-map');

  // Panel: heading, generated/next line, content, Refresh, settings, close.
  await page.locator(brief).click();
  await page.locator(panel).waitFor({ state: 'visible' });
  await page.waitForFunction(
    () =>
      !/Loading|Generating/.test(document.querySelector('[data-brief="body"]')?.textContent || ''),
    null,
    { timeout: 30000 }
  );
  check(
    (await page.locator('[data-brief="title"]').textContent()) === 'Today',
    'the panel heading is Today'
  );
  console.log('meta:', await page.locator('[data-brief="meta"]').textContent());
  await shot('02-panel');

  await page.getByRole('button', { name: 'Refresh' }).click();
  await shot('03-refreshing');
  await page.locator('[data-brief="refresh"]:not([disabled])').waitFor({ timeout: 30000 });
  console.log(
    'status after refresh:',
    await page.locator(`${brief} .ws-cmd-map-hq-station-state`).textContent()
  );

  // Brief settings: the existing form opens above the panel, and saving a
  // changed time moves the "next brief" line.
  await page.getByRole('button', { name: 'Brief settings' }).click();
  await page.locator('#homeDailyBriefSettingsModal').waitFor({ state: 'visible' });
  await page.waitForFunction(() => document.getElementById('homeDailyBriefTimezone')?.value);
  await page.waitForTimeout(450); // let the dialog finish fading in
  await shot('04-settings');
  await page.locator('#homeDailyBriefTime').fill('09:30');
  await page.locator('#homeDailyBriefSettingsForm button[type="submit"]').click();
  await page.waitForFunction(() =>
    /9:30/.test(document.querySelector('[data-brief="meta"]')?.textContent || '')
  );
  console.log('meta after save:', await page.locator('[data-brief="meta"]').textContent());
  await page.locator('#homeDailyBriefSettingsModal .btn-close').click();
  await page.locator('#homeDailyBriefSettingsModal').waitFor({ state: 'hidden' });
  // Bootstrap reports "hidden" after its backdrop has faded.
  await page
    .waitForFunction(
      () => document.activeElement?.getAttribute('data-brief') === 'settings',
      null,
      {
        timeout: 3000
      }
    )
    .catch(() => {});
  check(
    await page.evaluate(() => document.activeElement?.getAttribute('data-brief') === 'settings'),
    'focus returns to Brief settings when the dialog closes'
  );
  // Put the schedule back so a second run starts from the same place.
  await page.request.put(`${base}/api/personal-hq/brief/config`, {
    data: {
      timezone: await page.locator('#homeDailyBriefTimezone').inputValue(),
      schedule_time: '08:00',
      schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri'],
      schedule_enabled: true
    }
  });

  await page.keyboard.press('Escape');
  await page.locator(panel).waitFor({ state: 'hidden' });
  check(
    await page.evaluate(
      () => document.activeElement?.getAttribute('data-cmd-hq-station') === 'daily-brief'
    ),
    'Escape closes the panel and returns focus to the station'
  );

  // Details mode: the Stations rail lists it and opens the same panel.
  await page.goto(`${base}/workspaces/${slug}`);
  const row = page.locator(`.ws-cmd-panel.is-hq-stations ${brief}`);
  await row.waitFor({ state: 'visible' });
  await applyTheme();
  // The rail repaints as station statuses arrive, so wait for them to settle
  // and scroll by selector rather than holding one element.
  await page.waitForFunction(
    selector => !/Loading/.test(document.querySelector(selector)?.textContent || ''),
    `.ws-cmd-panel.is-hq-stations ${brief}`
  );
  await page.evaluate(
    selector => document.querySelector(selector)?.scrollIntoView({ block: 'center' }),
    `.ws-cmd-panel.is-hq-stations ${brief}`
  );
  await page.waitForTimeout(900); // the command view fades in
  await shot('05-details-rail');
  await row.click();
  await page.locator(panel).waitFor({ state: 'visible' });
  await page.getByRole('button', { name: 'Close Daily Brief' }).click();
  await page.locator(panel).waitFor({ state: 'hidden' });

  // Direct link: opens with the panel up, then leaves the address bar.
  await page.goto(`${base}/workspaces/${slug}?station=daily-brief`);
  await page.locator(panel).waitFor({ state: 'visible' });
  await page.waitForFunction(() => !window.location.search.includes('station='));
  check(
    true,
    `the direct link opened the panel and the address is now ${new URL(page.url()).pathname}${new URL(page.url()).search}`
  );
  await applyTheme();
  await shot('06-direct-link');
  await page.getByRole('button', { name: 'Close Daily Brief' }).click();

  // Drag the building, reload, and it stays put.
  await page.goto(`${base}/workspaces/${slug}?mode=map`);
  await page.locator(brief).waitFor({ state: 'visible' });
  // The map repaints as station statuses arrive; measure once they are in.
  await page.waitForFunction(
    () =>
      !Array.from(document.querySelectorAll('.ws-cmd-map-hq-station-state')).some(el =>
        /Loading|Scanning|—/.test(el.textContent || '')
      )
  );
  await page.waitForTimeout(500);
  await page.locator(brief).scrollIntoViewIfNeeded();
  const box = await page.locator(brief).boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x - 260, box.y - 120, { steps: 12 });
  await page.mouse.up();
  await page.waitForTimeout(600);
  const moved = await page.locator(brief).evaluate(el => el.style.getPropertyValue('--station-y'));
  await page.reload();
  await page.locator(brief).waitFor({ state: 'visible' });
  const kept = await page.locator(brief).evaluate(el => el.style.getPropertyValue('--station-y'));
  console.log('after drag:', moved, 'after reload:', kept);
  check(
    moved !== '73.00%' && kept === moved,
    `a dragged station keeps its place after reload (${kept})`
  );
  await applyTheme();
  await shot('07-dragged');
  // Back to the default slot, so a second run starts from the same place.
  await page.request.put(`${base}/api/orchestration/workspace/station-layout`, {
    data: { workspace_id: hqId, station_positions: {} }
  });

  // Narrow layout.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${base}/workspaces/${slug}?station=daily-brief`);
  await page.locator(panel).waitFor({ state: 'visible' });
  await applyTheme();
  await page.waitForTimeout(400);
  await shot('08-narrow');
}

// The station as the brief's home: the mission card points at it, the panel
// showing a brief completes the mission, earlier briefs open read-only, and
// the Action Center's "ready" item links to it.
async function briefHome() {
  const { slug } = await personalHQ();
  const panel = '.ws-cmd-modal-panel.is-daily-brief';
  const mission = async () =>
    ((await (await page.request.get(`${base}/api/progression`)).json()).missions || []).find(
      entry => entry.id === 'pa-first-brief'
    );

  // Home loads the assistant's Today, which used to complete the mission.
  await page.goto(`${base}/`);
  await page.waitForTimeout(1500);
  const before = await mission();
  console.log('mission before:', before.status, '|', before.action_label, '→', before.action_url);
  check(before.action_label === 'Open Daily Brief', 'the mission button reads Open Daily Brief');
  check(
    before.action_url === `/workspaces/${slug}?station=daily-brief`,
    'the mission button opens the station link'
  );

  // A second day of briefs without waiting for midnight. An HQ still in the
  // earlier zone gets a brief for that date, then moves to the zone that is
  // already on the next one. An HQ already in the later zone is left alone.
  const config = (await (await page.request.get(`${base}/api/personal-hq/brief/config`)).json())
    .config;
  if (config.timezone === ZONE_EARLIER) {
    const current = async () =>
      (await (await page.request.get(`${base}/api/personal-hq/brief/current`)).json()).revision;
    if (!(await current())) {
      await page.request.post(`${base}/api/personal-hq/brief/open`);
      for (let tries = 0; tries < 60 && !(await current()); tries++) {
        await page.waitForTimeout(500);
      }
    }
    console.log(`brief time zone: ${ZONE_EARLIER} → ${ZONE_LATER}`);
    await page.request.put(`${base}/api/personal-hq/brief/config`, {
      data: {
        timezone: ZONE_LATER,
        schedule_time: '08:00',
        schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri'],
        schedule_enabled: true
      }
    });
  }

  await page.goto(`${base}/workspaces/${slug}?station=daily-brief`);
  await page.locator(panel).waitFor({ state: 'visible' });
  await applyTheme();
  await page.waitForFunction(
    () =>
      !/Loading|Generating/.test(document.querySelector('[data-brief="body"]')?.textContent || ''),
    null,
    { timeout: 30000 }
  );
  await page.waitForTimeout(800);
  const after = await mission();
  console.log('mission after the panel showed a brief:', after.status);
  if (before.status !== 'completed') {
    check(after.status === 'completed', 'the panel showing a brief completed the mission');
  }
  await shot('01-panel-with-history');

  const items = page.locator('[data-brief="history-wrap"] button');
  console.log('earlier briefs:', JSON.stringify(await items.allTextContents()));
  if ((await items.count()) > 1) {
    await items.nth(1).click();
    await page.waitForFunction(
      () => !/Loading/.test(document.querySelector('[data-brief="body"]')?.textContent || '')
    );
    console.log(
      'earlier brief:',
      await page.locator('[data-brief="title"]').textContent(),
      '|',
      await page.locator('[data-brief="meta"]').textContent()
    );
    check(
      await page.locator('[data-brief="refresh"]').isHidden(),
      'an earlier brief has no Refresh'
    );
    await shot('02-earlier-brief');
    await items.nth(0).click();
    check(
      (await page.locator('[data-brief="title"]').textContent()) === 'Today',
      'Today returns to today’s brief'
    );
  } else {
    console.log('no earlier briefs in this sandbox yet (run the stage again after midnight UTC±)');
  }

  // Narrow: the list stacks under the brief.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(300);
  await page.evaluate(() =>
    document.querySelector('[data-brief="history-wrap"]')?.scrollIntoView({ block: 'end' })
  );
  await shot('03-narrow-history');
  await page.setViewportSize({ width: 1440, height: 900 });

  // The map's result cards for the brief. Only a scheduled generation makes
  // one, so a sandbox usually has none; the count is reported as it is.
  const cards = (
    (await (await page.request.get(`${base}/api/workspace-map/activity`)).json()).parcels || []
  ).filter(parcel => parcel.kind === 'daily_brief');
  console.log('Daily Brief result cards still waiting on the map:', cards.length);

  // The Action Center item. A scheduled run creates the real one; this shows
  // the list with that item so the link can be seen and followed.
  await page.route('**/api/action-center/opportunities*', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          {
            id: 'demo-brief-ready',
            workspace_id: 'demo',
            workspace_slug: slug,
            workspace_name: 'My HQ',
            title: 'Daily Brief ready — today',
            summary: 'Your scheduled Daily Brief has been generated.',
            priority: 'medium',
            status: 'new',
            source_url: `/workspaces/${slug}?station=daily-brief`,
            updated_at: new Date().toISOString()
          }
        ]
      })
    })
  );
  await page.goto(`${base}/action-center`);
  await applyTheme();
  const link = page.getByRole('link', { name: 'Open Daily Brief' });
  await link.waitFor({ state: 'visible' });
  await shot('04-action-center');
  await link.click();
  await page.locator(panel).waitFor({ state: 'visible' });
  check(true, 'the Action Center link opened the Daily Brief panel');
}

const PANEL = '#personalAssistantPanel';

// The idle filler the redesign removes: text whose only message is that
// nothing has happened yet (PRD requirement 22).
const IDLE_FILLER = [
  'is ready',
  'Ready for your next task',
  'Conversation History',
  'Progress updates will appear here'
];

async function openDrawer(path = '/') {
  await page.goto(`${base}${path}`);
  const launcher = page.locator('#personalAssistantLauncher');
  await launcher.waitFor({ state: 'visible', timeout: 20000 });
  await applyTheme();
  if (await page.locator(PANEL).isHidden()) await launcher.click();
  await page.locator(PANEL).waitFor({ state: 'visible' });
  await page.waitForTimeout(1500); // Today and the brief load after the drawer opens
}

const focusedId = () =>
  page.evaluate(() => document.activeElement?.id || document.activeElement?.tagName);

async function assistantState() {
  return (await (await page.request.get(`${base}/api/personal-assistant`)).json())
    .personal_assistant;
}

// The assistant drawer on Home: one view, composer always at the bottom.
async function drawer() {
  const { slug } = await personalHQ();

  // At rest.
  await openDrawer();
  check(
    (await focusedId()) === 'personalAssistantInput',
    'the composer has focus when the drawer opens'
  );
  check((await page.locator(`${PANEL} [role="tab"]`).count()) === 0, 'there are no tabs');
  const visibleText = await page.locator(PANEL).innerText();
  for (const filler of IDLE_FILLER) {
    check(!visibleText.includes(filler), `an idle drawer does not say "${filler}"`);
  }
  check(
    (await page.locator(`${PANEL} #homeDailyBrief, ${PANEL} .home-daily-brief-section`).count()) ===
      0,
    'the full Daily Brief is not rendered in the drawer'
  );
  console.log('header line:', await page.locator('#personalAssistantCheckIn').innerText());
  console.log(
    'brief row:',
    (await page.locator('#personalAssistantBriefRow').innerText()).replace(/\s+/g, ' ')
  );
  console.log(
    'progress row:',
    (await page.locator('#personalAssistantProgressRow').innerText()).replace(/\s+/g, ' ')
  );
  await shot('01-at-rest');

  // The More menu holds the links Today's own More used to.
  await page.locator('#personalAssistantMore > summary').click();
  console.log(
    'More:',
    JSON.stringify(await page.locator('#personalAssistantMore a:not([hidden])').allTextContents())
  );
  await shot('02-more-menu');
  await page.keyboard.press('Escape');
  check(await page.locator(PANEL).isVisible(), 'Escape closes the More menu, not the drawer');

  // Working agreement, from the More menu: a panel over the map with a
  // background of its own, and closing it goes back to the drawer.
  await page.locator('#personalAssistantMore > summary').click();
  await page.locator('#personalAssistantTodayAgreement').click();
  const agreement = page.locator('#personalAssistantContinuity');
  await agreement.waitFor({ state: 'visible' });
  const agreementBackground = await agreement.evaluate(el => getComputedStyle(el).backgroundColor);
  check(
    !/rgba\(0, 0, 0, 0\)|transparent/.test(agreementBackground),
    `the working agreement has an opaque background (${agreementBackground})`
  );
  await page.waitForTimeout(600);
  await shot('02b-working-agreement');
  await agreement.getByRole('button', { name: 'Close working agreement' }).click();
  await page.locator(PANEL).waitFor({ state: 'visible' });
  check(await agreement.isHidden(), 'closing the working agreement returns to the drawer');

  // The progress row expands Working on and Done in place, and collapses.
  const progress = page.locator('#personalAssistantProgressRow');
  if (await progress.isVisible()) {
    await progress.click();
    check((await progress.getAttribute('aria-expanded')) === 'true', 'the progress row expands');
    await page.locator('#personalAssistantProgressLists').scrollIntoViewIfNeeded();
    await shot('03-progress-expanded');
    await progress.click();
    check(await page.locator('#personalAssistantProgressLists').isHidden(), 'and collapses again');
  } else {
    console.log('progress row hidden: nothing in progress, nothing done today, no meetings');
  }

  // Sample data for the layout only: several things needing you, work under
  // way, and a meeting. The page, the drawer and its scripts are the real ones.
  const real = await (await page.request.get(`${base}/api/personal-assistant/today`)).json();
  const sample = structuredClone(real);
  sample.today.needs_you = {
    health: { status: 'available' },
    items: [
      {
        id: 'demo-build',
        kind: 'workspace_build',
        title: 'Finish building Song Sketches',
        detail: 'Workspace build paused at step 2 of 4',
        actions: ['resume', 'discard']
      },
      {
        id: 'demo-email',
        kind: 'follow_up',
        title: '3 emails are waiting for a reply',
        detail: 'Email Ops',
        route: `/workspaces/${slug}`
      },
      {
        id: 'demo-choice',
        kind: 'task',
        title: 'Pick a release date',
        detail: 'waiting_for_choice',
        route: `/workspaces/${slug}`
      }
    ]
  };
  sample.today.working_on.items = [
    ...(sample.today.working_on.items || []),
    {
      id: 'demo-w1',
      kind: 'folder_workspace',
      title: 'Sorting the Samples folder',
      detail: 'Music Production Library',
      route: `/workspaces/${slug}`
    },
    {
      id: 'demo-w2',
      kind: 'janitor_work',
      title: 'Drafting a reply to the studio booking',
      detail: 'Email Ops',
      route: `/workspaces/${slug}`
    }
  ];
  await page.route('**/api/personal-assistant/today', route =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(sample) })
  );
  await openDrawer();
  console.log(
    'needs you count (sample data):',
    await page.locator('#personalAssistantNeedsYouCount').innerText()
  );
  await shot('04-needs-you-sample');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(400);
  await shot('05-narrow-sample');
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.unroute('**/api/personal-assistant/today');

  // Paused: the banner says so, and so does the header line.
  const before = await assistantState();
  await page.request.post(`${base}/api/personal-assistant/pause`, {
    data: { if_version: before.state_version }
  });
  await openDrawer();
  console.log('paused header line:', await page.locator('#personalAssistantCheckIn').innerText());
  console.log('paused banner:', await page.locator('#personalAssistantTodayBanner').innerText());
  check(
    (await focusedId()) === 'personalAssistantInput',
    'a paused assistant still takes the composer focus'
  );
  await shot('06-paused');
  await page.request.post(`${base}/api/personal-assistant/resume`, {
    data: { if_version: (await assistantState()).state_version }
  });

  // /?panel=today still opens the drawer, and the parameter leaves the address.
  await page.goto(`${base}/?panel=today`);
  await page.locator(PANEL).waitFor({ state: 'visible', timeout: 20000 });
  await page.waitForFunction(() => !window.location.search.includes('panel='));
  check(true, '/?panel=today opens the drawer');

  // The brief row opens the Daily Brief station.
  await page.waitForFunction(() => !document.getElementById('personalAssistantBriefRow')?.hidden);
  await page.locator('#personalAssistantBriefRow').click();
  await page
    .locator('.ws-cmd-modal-panel.is-daily-brief')
    .waitFor({ state: 'visible', timeout: 20000 });
  check(true, `the brief row opened the Daily Brief panel at ${new URL(page.url()).pathname}`);
}

// The drawer before the assistant can accept work: not hired, then hired with
// no HQ. Run it against a FRESH sandbox.
async function drawerSetup() {
  await page.request.post(`${base}/api/onboarding/skip`);
  await page.request.post(`${base}/api/settings/workspace-root`, { data: { workspace_root: '' } });
  let assistant = await assistantState();
  if (assistant.state === 'needs_hire') {
    await page.goto(`${base}/?panel=today`);
    await page.waitForTimeout(2000);
    await applyTheme();
    check(
      await page.locator('#personalAssistantLauncher').isHidden(),
      'before hiring there is no assistant launcher'
    );
    check(
      await page.locator(PANEL).isHidden(),
      'and /?panel=today cannot open a drawer for an unhired assistant'
    );
    await shot('01-before-hiring');
    await page.request.post(`${base}/api/personal-assistant/hire`, {
      data: {
        request_id: 'drawer-setup-hire',
        if_version: assistant.state_version ?? 0,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    });
    assistant = await assistantState();
  }
  check(
    assistant.state === 'needs_hq',
    `this sandbox has a hired assistant and no HQ (${assistant.state})`
  );
  await openDrawer();
  await page.locator('#personalAssistantHQCard').waitFor({ state: 'visible', timeout: 15000 });
  console.log('focused on open:', await focusedId());
  check(
    await page.locator('#personalAssistantInput').isDisabled(),
    'the composer is disabled until HQ exists'
  );
  check(
    (await page.evaluate(
      () => document.activeElement?.closest('#personalAssistantPanel') !== null
    )) && (await focusedId()) !== 'personalAssistantInput',
    'focus goes to the first control in the drawer instead'
  );
  console.log(
    'needs you count:',
    await page.locator('#personalAssistantNeedsYouCount').innerText()
  );
  check(
    await page.locator('#personalAssistantBriefRow').isHidden(),
    'with no HQ there is no brief row'
  );
  await shot('02-hired-no-hq');
}

const FOLDER = '#personalAssistantFolder';
const CHIP = '#personalAssistantFolderChip';
const STRIP = '#personalAssistantSummary';
const SECTIONS = '#personalAssistantTodaySections';
const OFFER = '#personalAssistantFolderOffer';

const offerText = async () => (await page.locator(OFFER).innerText()).replace(/\s+/g, ' ').trim();

async function currentOffer() {
  return (await (await page.request.get(`${base}/api/personal-assistant/folder-digest`)).json())
    ?.folder_digest?.offer;
}

// The folder flow, as a turn in the conversation. Needs the sandbox's folders:
//   ./scripts/smoke.sh showfolder <base-url> seed <sandbox>
async function folder() {
  await personalHQ();
  // This stage is about a folder the user asks for; the assistant's own first
  // prompt has its own stage (folder-first), so a fresh sandbox records it as
  // already shown.
  await page.request.post(`${base}/api/personal-assistant/folder-digest/prompted`);
  const chip = page.locator(CHIP);
  const chooser = page.locator('#personalAssistantFolderChooser');
  const request = page.locator('#personalAssistantFolderRequest');
  const toggle = page.locator('#personalAssistantSummaryToggle');

  // At rest: the chip is there, the conversation is empty, nothing is folded.
  await openDrawer();
  check(await chip.isVisible(), 'the chip sits above the composer');
  check(await chip.isEnabled(), 'and can be pressed');
  const chipBox = await chip.boundingBox();
  const inputBox = await page.locator('#personalAssistantInput').boundingBox();
  check(
    chipBox.y + chipBox.height <= inputBox.y && inputBox.y - (chipBox.y + chipBox.height) < 24,
    'directly above it'
  );
  if (!(await currentOffer())) {
    check(await page.locator(FOLDER).isHidden(), 'at rest the conversation has no folder turn');
  }
  check(await page.locator(STRIP).isHidden(), 'and nothing is folded');
  await shot('01-at-rest');

  // The chip: the request, then the chooser as the assistant's reply.
  await chip.click();
  await chooser.waitFor({ state: 'visible' });
  check((await request.innerText()).includes('Explore a folder'), 'the request is shown');
  console.log('chooser:', (await chooser.innerText()).replace(/\s+/g, ' ').trim());
  check(
    (await chooser.innerText()).includes('A read-only peek. Nothing moves.'),
    'the chooser says nothing moves'
  );
  check(await chip.isDisabled(), 'the chip is disabled while a folder is being chosen');
  check(await page.locator(STRIP).isVisible(), 'the top folds into the summary strip');
  check(await page.locator(SECTIONS).isHidden(), 'which hides Needs you and the two rows');
  console.log('strip:', await page.locator('#personalAssistantSummaryText').innerText());
  await shot('02-chooser');

  // Show and Hide reverse the fold.
  await toggle.click();
  check((await toggle.getAttribute('aria-expanded')) === 'true', 'Show expands it');
  check(await page.locator(SECTIONS).isVisible(), 'and the sections are back');
  await shot('03-strip-shown');
  await toggle.click();
  check(await page.locator(SECTIONS).isHidden(), 'Hide folds it again');

  // Choosing a folder: the scene, then the offer, as further replies.
  await chooser.locator('[data-chip="downloads"]').click();
  await page.locator(OFFER).waitFor({ state: 'visible', timeout: 20000 });
  check(await page.locator('#personalAssistantFolderScene').isVisible(), 'the scene is shown');
  check(
    await page.locator(`${FOLDER} ${OFFER}`).isVisible(),
    'the offer is a reply in the conversation'
  );
  check(await chooser.isHidden(), 'the chooser is done');
  check(await chip.isEnabled(), 'and the chip can be pressed again');
  console.log('offer:', await offerText());
  await page.locator(OFFER).scrollIntoViewIfNeeded();
  await shot('04-offer');

  // Not now: the offer says so and stays where it was answered.
  await page.locator(`${OFFER} [data-folder-action="later"]`).click();
  await page.waitForFunction(
    selector => document.querySelector(selector)?.dataset.status === 'later',
    OFFER
  );
  console.log('after Later:', await offerText());
  await shot('05-later');

  // Again, for a folder with projects in it; this one is left unanswered.
  await chip.click();
  await chooser.waitFor({ state: 'visible' });
  console.log(
    'second chooser title:',
    await page.locator('#personalAssistantFolderTitle').innerText()
  );
  await chooser.locator('[data-chip="documents"]').click();
  await page.waitForFunction(
    selector => document.querySelector(selector)?.dataset.status === 'pending',
    OFFER,
    { timeout: 20000 }
  );
  console.log('offer:', await offerText());
  await shot('06-second-offer');

  // A request sent now goes below the folder turn: the latest is nearest the composer.
  const before = await page.evaluate(() =>
    Array.from(document.getElementById('personalAssistantThread').children, node => node.id)
  );
  console.log('conversation order while the folder turn is latest:', before.join(' → '));

  // Reload onto the unanswered offer: it is a card in Needs you, not lost.
  await openDrawer();
  check(
    await page.locator(`#personalAssistantNeedsYouCards ${OFFER}`).isVisible(),
    'after a reload the unanswered offer is a card in Needs you'
  );
  check(await page.locator(FOLDER).isHidden(), 'and the conversation is empty');
  check((await page.locator(OFFER).count()) === 1, 'there is one offer card, not two');
  check(await page.locator(STRIP).isHidden(), 'everything is shown in full again');
  console.log(
    'needs you count:',
    await page.locator('#personalAssistantNeedsYouCount').innerText()
  );
  await shot('07-reload-offer-in-needs-you');

  // Set up, from that card: the plan is confirmed first, then run.
  await page.locator(`${OFFER} [data-folder-action="tidy"]`).click();
  console.log('plan:', await offerText());
  await shot('08-plan');
  await page.locator(`${OFFER} [data-folder-action="setup"]`).click();
  await page.waitForFunction(
    async () => {
      const response = await fetch('/api/personal-assistant/folder-digest');
      return (await response.json())?.folder_digest?.offer?.status === 'resolved';
    },
    null,
    { timeout: 60000 }
  );
  await page.waitForTimeout(1500);
  console.log('after Set up:', new URL(page.url()).pathname + new URL(page.url()).search);
  await shot('09-set-up');

  // The toolbar's button starts the same turn.
  await page.goto(`${base}/`);
  await page.locator('#cockpitShowFolderBtn').waitFor({ state: 'visible', timeout: 20000 });
  await applyTheme();
  await page.locator('#cockpitShowFolderBtn').click();
  await chooser.waitFor({ state: 'visible' });
  check(await request.isVisible(), 'the toolbar button starts the same turn, with the request');
  check(await page.locator(SECTIONS).isHidden(), 'and folds the top');
  await shot('10-toolbar');

  // So does the link.
  await page.goto(`${base}/?panel=today&folder=show`);
  await chooser.waitFor({ state: 'visible', timeout: 20000 });
  await applyTheme();
  check(await request.isVisible(), '/?panel=today&folder=show starts it too');
  await page.waitForFunction(() => !window.location.search.includes('folder='));
  check(true, 'and the parameters leave the address');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(400);
  await shot('11-link-narrow');
  await page.setViewportSize({ width: 1440, height: 900 });

  // Reopening with the turn still going keeps the fold; a fresh page shows all.
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  check(await page.locator(SECTIONS).isHidden(), 'reopening onto the turn keeps it folded');
}

// The assistant's one first-folder prompt. Run it against a sandbox where the
// assistant is hired and My HQ has not been built (drawer-setup leaves one).
async function folderFirst() {
  let assistant = await assistantState();
  check(assistant.state === 'needs_hq', `this sandbox is hired with no HQ (${assistant.state})`);
  const built = await page.request.post(`${base}/api/personal-assistant/hq`, {
    data: {
      request_id: 'drawer-demo-first-folder',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  check(built.ok(), 'My HQ is built');
  await openDrawer();
  const chooser = page.locator('#personalAssistantFolderChooser');
  await chooser.waitFor({ state: 'visible', timeout: 20000 });
  console.log('first message:', (await chooser.innerText()).replace(/\s+/g, ' ').trim());
  check(
    await page.locator('#personalAssistantFolderRequest').isHidden(),
    'the assistant speaks first: there is no request above its prompt'
  );
  check(await page.locator(STRIP).isHidden(), 'and nothing is folded');
  check(await page.locator(CHIP).isDisabled(), 'the chip waits while a folder is being chosen');
  await shot('01-first-folder-prompt');

  // The server shows it once per assistant.
  await openDrawer();
  check(await chooser.isHidden(), 'a reload does not show the prompt again');
  check(await page.locator(CHIP).isEnabled(), 'and the chip is ready');
  await shot('02-after-reload');
}

// The drawer on pages that are not Home: the header, one "N need you" line,
// the conversation, the chip and the composer.
async function otherPages() {
  const { slug } = await personalHQ();
  const line = page.locator('#personalAssistantNeedsLine');
  const real = await (await page.request.get(`${base}/api/personal-assistant/today`)).json();
  const realCount = real.today?.needs_you?.items?.length ?? 0;
  console.log('needs_you items on the server:', realCount);

  for (const [name, path] of [
    ['workspace', `/workspaces/${slug}`],
    ['agents', '/agents']
  ]) {
    await openDrawer(path);
    check(
      (await page.locator('#personalAssistantToday').count()) === 0,
      `${name}: no Today of its own`
    );
    check(
      (await page
        .locator(
          '#personalAssistantBriefRow, #personalAssistantProgressRow, #personalAssistantFolder'
        )
        .count()) === 0,
      `${name}: no brief row, progress row or folder flow`
    );
    console.log(
      `${name} header line:`,
      await page.locator('#personalAssistantCheckIn').innerText()
    );
    check(
      (await page.locator('#personalAssistantCheckIn').innerText()) !== 'Personal Assistant',
      `${name}: the header says when the next check-in is`
    );
    await page.locator('#personalAssistantMore > summary').click();
    console.log(
      `${name} More:`,
      JSON.stringify(await page.locator('#personalAssistantMore a:not([hidden])').allTextContents())
    );
    await page.keyboard.press('Escape');
    check(
      (await line.isVisible()) === realCount > 0,
      `${name}: the needs-you line shows only when something needs you`
    );
    check(await page.locator(CHIP).isVisible(), `${name}: the chip is above the composer`);
    await page.locator('#personalAssistantInput').focus();
    await shot(`01-${name}`);
  }

  // Sample data for the line only: three things need the user.
  const sample = structuredClone(real);
  sample.today.needs_you = {
    health: { status: 'available' },
    items: ['Pick a release date', 'Approve the tidy of Downloads', 'Reply to the studio'].map(
      title => ({ kind: 'task', title })
    )
  };
  await page.route('**/api/personal-assistant/today', route =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(sample) })
  );
  await openDrawer('/agents');
  await line.waitFor({ state: 'visible' });
  console.log('line:', (await line.innerText()).trim(), '→', await line.getAttribute('href'));
  check((await line.innerText()).trim() === '3 need you', 'the line reads "3 need you"');
  await shot('02-agents-three-need-you');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(400);
  await shot('03-agents-narrow');
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.unroute('**/api/personal-assistant/today');

  // The line lands on Home with the drawer open, where the items are listed.
  await line.click();
  await page.locator('#personalAssistantToday').waitFor({ state: 'visible', timeout: 20000 });
  check(new URL(page.url()).pathname === '/', 'the line lands on Home with the drawer open');

  // The number cannot be read: the line still points at Home.
  await page.route('**/api/personal-assistant/today', route => route.fulfill({ status: 503 }));
  await openDrawer('/agents');
  await line.waitFor({ state: 'visible' });
  console.log('unreadable:', (await line.innerText()).trim());
  check(
    (await line.innerText()).trim() === 'Open Home to see what needs you',
    'when it cannot be read the line says to open Home'
  );
  check(
    (await page.locator('#personalAssistantCheckIn').innerText()) === 'Personal Assistant',
    'and the header falls back to the plain role'
  );
  await shot('04-agents-unreadable');
  await page.unroute('**/api/personal-assistant/today');

  // The chip lands on Home and starts the folder flow there.
  await openDrawer(`/workspaces/${slug}`);
  await page.locator(CHIP).click();
  await page
    .locator('#personalAssistantFolderChooser')
    .waitFor({ state: 'visible', timeout: 20000 });
  check(new URL(page.url()).pathname === '/', 'the chip lands on Home');
  check(
    await page.locator('#personalAssistantFolderRequest').isVisible(),
    'and starts the folder flow in the conversation'
  );
  await shot('05-chip-landed-on-home');

  // Sending a request from a workspace page, which has the work controller.
  const status = page.locator('#personalAssistantPanelStatus');
  await openDrawer(`/workspaces/${slug}`);
  await page.locator('#personalAssistantInput').fill('What should I look at first today?');
  await page.locator('#personalAssistantSend').click();
  await page.waitForTimeout(2500);
  console.log('workspace, after Send:', (await status.innerText()).trim());
  check(
    (await page.locator('#personalAssistantInput').inputValue()) === '',
    'the request was sent from a workspace page'
  );
  check(
    !(await status.innerText()).includes('unavailable on this page'),
    'and the work controller took it'
  );
  await page.locator('#personalAssistantActivityMount').scrollIntoViewIfNeeded();
  await shot('06-workspace-sent');

  // The Agents page has never loaded the work controller (dashboard.js), so
  // its composer says so instead of sending. That is not changed here.
  await openDrawer('/agents');
  await page.locator('#personalAssistantInput').fill('What should I look at first today?');
  await page.locator('#personalAssistantSend').click();
  await page.waitForTimeout(1000);
  console.log('agents, after Send:', (await status.innerText()).trim());
  await shot('07-agents-send');
}

try {
  if (stage === 'station') await station();
  else if (stage === 'brief-home') await briefHome();
  else if (stage === 'drawer') await drawer();
  else if (stage === 'drawer-setup') await drawerSetup();
  else if (stage === 'folder') await folder();
  else if (stage === 'folder-first') await folderFirst();
  else if (stage === 'other-pages') await otherPages();
  else throw new Error(`unknown stage: ${stage}`);
} finally {
  if (problems.length) {
    console.log(`\n${problems.length} page problem(s):`);
    for (const problem of [...new Set(problems)].slice(0, 15)) console.log(`  ${problem}`);
  } else {
    console.log('\nno page errors');
  }
  await browser.close();
}
