// Drives the assistant-drawer redesign in a disposable demo sandbox and saves
// screenshots, one stage at a time (tasks/tasks-assistant-drawer-redesign.md).
//
//   node scripts/demo-assistant-drawer.mjs <base-url> <shots-dir> <stage> [theme]
//
// Stages:
//   station   the Daily Brief station in My HQ: map, Stations rail, panel,
//             Refresh, Brief settings, the direct link, a drag, narrow layout
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

async function personalHQ() {
  const status = await (await page.request.get(`${base}/api/personal-hq/status`)).json();
  const slug = status?.status?.workspace?.folder_slug;
  if (!slug) throw new Error('no Personal HQ in this sandbox; run: smoke.sh showfolder <url> hq');
  return { slug, id: status.status.workspace_id };
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

try {
  if (stage === 'station') await station();
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
