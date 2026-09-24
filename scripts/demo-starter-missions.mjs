/*
 * Drives the starter missions (tasks/prd-starter-missions.md) against a
 * running, isolated demo server and saves screenshots. It uses real UI and
 * real endpoints. The shortcuts are skipping first-run onboarding and hiring
 * plus building HQ through the assistant API, all of which the demo sandbox
 * owns.
 *
 *   node scripts/demo-starter-missions.mjs <baseUrl> <outDir> <stage> \
 *     [--focus=a,b] [--sandbox=<server sandbox dir>] [--width=1280]
 *
 * Stages:
 *   card     Mission 01 on the card after a hire, then Mission 03 (Show your
 *            assistant a folder) with the other missions beneath it once HQ is
 *            built. 1280px and 400px.
 *   folder   Mission 03's start: Start on the card opens the assistant panel
 *            on Today with the folder chooser unfolded and the quest parameter
 *            scrubbed, and the mission stays open. The rest of the mission — a
 *            scan, the offer, a workspace or a tidy — is driven by
 *            `scripts/smoke.sh showfolder`, which seeds folders under the
 *            server's HOME.
 *   email    Mission 03 for a --focus=help_with_email hire: Set up email opens
 *            the guided setup, and closing it mid-way reads In progress.
 *   plan     Mission 03 for a --focus=plan_my_day hire: the first-day plan
 *            completes it and Mission 04 takes the card; then the Calendar
 *            capability card opens the creator on Calendar Ops.
 *   results  Today Results after File Janitor files files: continue a tidy
 *            sandbox with --workspace=<slug> --folder=<its folder>. Scans,
 *            approves, confirms, reads the Today line, follows it to History,
 *            then requests a brief and checks Mission 04 completes on Today.
 *   states   The card at rest: every mission deferred reads Saved for later with
 *            Resume, then a served brief turns Mission 04 Complete.
 *
 * Every stage prints the missions it observed and any console errors or failed
 * requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { mkdirSync, utimesSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, stage = 'card', ...flags] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error(
    'usage: node scripts/demo-starter-missions.mjs <baseUrl> <outDir> <stage> [--focus=a,b] [--sandbox=dir] [--width=n]'
  );
  process.exit(1);
}
const flag = name => {
  const found = flags.find(value => value.startsWith(`--${name}=`));
  return found ? found.slice(name.length + 3) : '';
};
const out = resolve(outDir);
mkdirSync(out, { recursive: true });
const focusAreas = flag('focus').split(',').filter(Boolean);
const sandboxDir = flag('sandbox');
const stageWidth = Number(flag('width')) || 1280;

const browser = await chromium.launch();
const problems = [];
let exitCode = 0;

let lastPage = null;

async function newPage(width, height) {
  const page = await browser.newPage({ viewport: { width, height } });
  lastPage = page;
  page.on('console', m => {
    if (m.type() === 'error') problems.push(`console: ${m.text()}`);
  });
  page.on('pageerror', e => problems.push(`pageerror: ${e.message}`));
  page.on('response', r => {
    if (r.status() >= 400) problems.push(`HTTP ${r.status()}: ${r.request().method()} ${r.url()}`);
  });
  return page;
}

async function shot(page, name) {
  const file = join(out, `${name}.png`);
  await page.screenshot({ path: file, fullPage: false });
  console.log(`screenshot: ${file}`);
}

async function api(page, method, url, body) {
  return page.evaluate(
    async ({ method, url, body }) => {
      const response = await fetch(url, {
        method,
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        ...(body ? { body: JSON.stringify(body) } : {})
      });
      let json = null;
      try {
        json = await response.json();
      } catch (_) {}
      return { status: response.status, json };
    },
    { method, url, body }
  );
}

async function assistant(page) {
  return (await api(page, 'GET', '/api/personal-assistant')).json?.personal_assistant || {};
}

async function hire(page) {
  let current = await assistant(page);
  if (current.state === 'needs_hire') {
    const res = await api(page, 'POST', '/api/personal-assistant/hire', {
      request_id: `demo-hire-${Date.now()}`,
      if_version: current.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my week organised.',
      focus_areas: focusAreas
    });
    console.log(`hire: HTTP ${res.status} focus=${JSON.stringify(focusAreas)}`);
    current = await assistant(page);
  }
  console.log(`assistant state: ${current.state}`);
  return current;
}

async function buildHQ(page) {
  let current = await assistant(page);
  if (current.state === 'needs_hq') {
    const res = await api(page, 'POST', '/api/personal-assistant/hq', {
      request_id: `demo-hq-${Date.now()}`,
      if_version: current.state_version ?? 0,
      name: 'Demo HQ'
    });
    console.log(`hq: HTTP ${res.status} ${res.json?.code || ''}`);
    current = await assistant(page);
  }
  console.log(`assistant state: ${current.state}`);
  return current;
}

async function missions(page) {
  const status = (await api(page, 'GET', '/api/progression')).json || {};
  const rows = (status.missions || []).map(
    m =>
      `${m.order}:${m.id}:${m.status}${m.in_progress ? ':in_progress' : ''} "${m.title}" -> ${m.action_label} ${m.action_url}`
  );
  console.log(`missions (tier ${status.current_tier}):\n  ${rows.join('\n  ')}`);
  return status;
}

// openQuests opens Home's Quests flyout and reports the featured card and the
// checklist rows beneath it.
async function openQuests(page) {
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  const toggle = page.locator('#cockpitQuestsToggle');
  await toggle.waitFor({ state: 'visible', timeout: 20000 });
  if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
  await page.locator('#cockpitQuestsFlyout').waitFor({ state: 'visible', timeout: 10000 });
  const card = page.locator('[data-role="first-mission"]');
  await card.waitFor({ state: 'visible', timeout: 10000 });
  // textContent, not innerText: the kicker and pill are uppercased by CSS.
  const read = async role =>
    ((await page.locator(`[data-role="${role}"]`).textContent()) || '').trim();
  const view = {
    kicker: await read('first-mission-kicker'),
    status: await read('first-mission-status'),
    title: await read('first-mission-title'),
    action: (await page.locator('[data-role="first-mission-action"]').isVisible())
      ? `${await read('first-mission-action-label')} ${await page
          .locator('[data-role="first-mission-action"]')
          .getAttribute('href')}`
      : '(no action)',
    rows: (await page.locator('[data-role="quests"] .quest-item').allInnerTexts()).map(t =>
      t.replace(/\s+/g, ' ').trim()
    )
  };
  console.log(`card: ${JSON.stringify(view)}`);
  return view;
}

function expect(condition, message) {
  if (!condition) throw new Error(message);
  console.log(`ok   ${message}`);
}

async function cardStage() {
  for (const [width, height] of [
    [1280, 800],
    [400, 860]
  ]) {
    const page = await newPage(width, height);
    await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
    await api(page, 'POST', '/api/onboarding/skip');
    await hire(page);
    const relationship = await assistant(page);
    if (relationship.state === 'needs_hq') {
      await missions(page);
      const before = await openQuests(page);
      expect(before.kicker === 'Mission 01', 'Mission 01 is on the card before HQ exists');
      expect(
        before.action.includes('/?quest=build-hq'),
        'Mission 01 routes to the guided HQ walkthrough'
      );
      await shot(page, `g1-mission01-${width}`);
      await buildHQ(page);
    }
    const status = await missions(page);
    expect(
      !(status.missions || []).some(m => m.id === 'pa-tidy-downloads'),
      'Tidy your Downloads is no longer a mission'
    );
    const after = await openQuests(page);
    expect(after.kicker === 'Mission 03', 'Mission 03 is on the card once HQ is designated');
    expect(
      after.title === 'Show your assistant a folder',
      'Mission 03 reads Show your assistant a folder'
    );
    expect(after.rows.length === 4, 'four Starter rows sit beneath the card');
    expect(
      !after.rows.some(r => r.includes('Show your assistant a folder')),
      'the card mission is not repeated'
    );
    expect(
      !after.rows.some(r => r.includes('Start a project workspace')),
      'Start a project workspace is not a mission'
    );
    await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
    await shot(page, `g1-mission03-${width}`);
    await page.close();
  }
}

// writeSettledFiles drops files dated hours ago, so the janitor's settle window
// (it ignores a file that may still be downloading) never hides them.
function writeSettledFiles(root, names) {
  const old = new Date(Date.now() - 6 * 60 * 60 * 1000);
  for (const name of names) {
    writeFileSync(join(root, name), `demo ${name}`);
    utimesSync(join(root, name), old, old);
  }
  console.log(`files in ${root}: ${names.join(', ')} (dated ${old.toISOString()})`);
}

async function resultsStage() {
  const slug = flag('workspace');
  const root = flag('folder');
  if (!slug || !root || !sandboxDir || !resolve(root).startsWith(resolve(sandboxDir))) {
    throw new Error(
      'results needs --workspace=<janitor slug> and --folder=<its folder inside --sandbox>'
    );
  }
  const width = stageWidth;
  const page = await newPage(width, width < 600 ? 860 : 800);
  // Fresh names each run: the janitor does not re-propose a name it already
  // handled, so a rerun with the same names would find nothing to file.
  const stamp = Date.now().toString(36);
  writeSettledFiles(root, [`receipt-${stamp}.pdf`, `screenshot-${stamp}.png`]);

  // Scan, select every proposal, review, confirm: the console's own flow.
  await page.goto(`${baseUrl}/workspaces/${encodeURIComponent(slug)}`, {
    waitUntil: 'domcontentloaded'
  });
  const wizard = page.locator('#setupWizardDialog');
  if (await wizard.isVisible().catch(() => false)) await page.locator('#setupWizardClose').click();
  await page.locator('#fileJanitorCardOpen').click();
  await page.locator('#fileJanitorConsole').waitFor({ state: 'visible', timeout: 15000 });
  await page.locator('#fileJanitorScan').click();
  const rows = page.locator('#fileJanitorConsoleBody .fj-row-item');
  await rows.first().waitFor({ state: 'visible', timeout: 20000 });
  const count = await rows.count();
  for (let i = 0; i < count; i++) {
    const select = rows.nth(i).locator('.fj-select');
    if (await select.isEnabled().catch(() => false)) await select.check();
  }
  await shot(page, `g4-janitor-review-${width}`);
  await page.locator('#fileJanitorApprove').click();
  await page
    .locator('#fileJanitorConsoleBody')
    .getByRole('button', { name: /Move|Apply|Confirm these/ })
    .first()
    .click();
  const results = page.locator('#fileJanitorConsoleBody .fj-results');
  await results.waitFor({ state: 'visible', timeout: 30000 });
  console.log(
    `janitor results: ${((await results.textContent()) || '').replace(/\s+/g, ' ').trim()}`
  );

  // Today shows what was filed, where the user looks every day.
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantTodayPanel').waitFor({ state: 'visible', timeout: 15000 });
  const line = page.locator('#personalAssistantTodayResults li', {
    hasText: /Filed \d+ files? into/
  });
  await line.first().waitFor({ state: 'visible', timeout: 15000 });
  const text = ((await line.first().textContent()) || '').replace(/\s+/g, ' ').trim();
  console.log(`today line: ${text}`);
  expect(
    /Filed \d+ files? into .+\/Filed/.test(text),
    'Today Results shows what File Janitor filed'
  );
  expect(text.includes('Undo from History'), 'the line points at History for undo');
  // Today re-renders when its reads settle; wait for that, then scroll.
  await page.waitForTimeout(1500);
  await line
    .first()
    .scrollIntoViewIfNeeded()
    .catch(() => {});
  await shot(page, `g4-today-results-${width}`);

  await Promise.all([
    page.waitForURL(/panel=file-janitor&tab=history/, { timeout: 15000 }),
    line.first().locator('a').click()
  ]);
  await page.locator('#fileJanitorConsole').waitFor({ state: 'visible', timeout: 15000 });
  await page
    .locator('#fileJanitorConsole [data-fj-tab="history"][aria-selected="true"]')
    .waitFor({ timeout: 15000 });
  expect(true, "the line's link opens the console on History");
  await page.waitForTimeout(1500);
  expect(
    !(await page
      .getByText('Some link details were out of date')
      .isVisible()
      .catch(() => false)),
    'arriving from Today does not claim the link was out of date'
  );
  await shot(page, `g4-history-${width}`);

  // Mission 04: before any brief, the card asks for a model when none is set.
  const before = await missions(page);
  const brief = (before.missions || []).find(m => m.id === 'pa-first-brief');
  console.log(`Mission 04 before a brief: ${brief?.status} "${brief?.why}"`);
  if (brief?.status !== 'completed') {
    const card = await openQuests(page);
    if (card.kicker === 'Mission 04') {
      await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
      await shot(page, `g4-mission04-no-brief-${width}`);
    }
    const refresh = await api(page, 'POST', '/api/personal-hq/brief/refresh');
    console.log(`brief refresh: HTTP ${refresh.status}`);
    let revision = '';
    for (let i = 0; i < 30 && !revision; i++) {
      await page.waitForTimeout(1000);
      const current = await api(page, 'GET', '/api/personal-hq/brief/current');
      revision = current.json?.revision?.id || current.json?.id || '';
    }
    console.log(`brief revision: ${revision || '(none)'}`);
    // Today serves the brief; that first view completes Mission 04.
    await api(page, 'GET', '/api/personal-assistant/today');
    const after = await missions(page);
    const done = (after.missions || []).find(m => m.id === 'pa-first-brief');
    expect(done?.status === 'completed', 'Today served with a brief completed Mission 04');
    const final = await openQuests(page);
    await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
    console.log(`final card: ${final.kicker} ${final.status}`);
    await shot(page, `g4-mission04-after-brief-${width}`);
  }
  await page.close();
}

async function folderStage() {
  const width = stageWidth;
  const page = await newPage(width, width < 600 ? 860 : 800);
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await api(page, 'POST', '/api/onboarding/skip');
  await hire(page);
  await buildHQ(page);
  await missions(page);

  // Start on the card.
  const card = await openQuests(page);
  expect(card.kicker === 'Mission 03', 'Mission 03 is on the card');
  expect(card.action.includes('/?quest=show-folder'), 'Start opens the folder chooser');
  await Promise.all([
    page.waitForURL(url => url.search === '' || !url.search.includes('quest='), {
      timeout: 15000
    }),
    page.locator('[data-role="first-mission-action"]').click()
  ]);

  // The assistant panel opens on Today with the chooser unfolded.
  const chooser = page.locator('#personalAssistantFolderChooser');
  await chooser.waitFor({ state: 'visible', timeout: 15000 });
  expect(
    await page.locator('#personalAssistantTodayPanel').isVisible(),
    'the assistant panel opened on Today'
  );
  expect(!page.url().includes('quest='), '?quest= was dropped from the URL');
  const chips = await chooser.locator('button').allTextContents();
  console.log(`chips: ${chips.join(' | ')}`);
  expect(chips.length > 0, 'the chooser offers folder chips');
  await shot(page, `g2-folder-chooser-${width}`);

  // Opening the chooser is not doing the mission.
  const status = await missions(page);
  const folder = (status.missions || []).find(m => m.id === 'pa-show-folder');
  expect(folder?.status === 'available', 'Mission 03 stays open until an offer is accepted');
  await page.close();
}

// missionThreeOnCard hires with --focus, builds HQ, and defers Show your
// assistant a folder the way a user would, so Connect one source is the
// mission on the card.
async function missionThreeOnCard(width) {
  const page = await newPage(width, width < 600 ? 860 : 800);
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await api(page, 'POST', '/api/onboarding/skip');
  await hire(page);
  await buildHQ(page);
  const skipped = await api(page, 'POST', '/api/progression/skip', {
    quest_id: 'pa-show-folder'
  });
  console.log(`deferred Show your assistant a folder: HTTP ${skipped.status}`);
  await missions(page);
  return page;
}

async function emailStage() {
  const page = await missionThreeOnCard(stageWidth);
  const card = await openQuests(page);
  expect(card.kicker === 'Mission 03', 'Mission 03 is on the card');
  expect(card.title === 'Set up email', 'a help-with-email hire is offered Set up email');
  expect(
    card.action === 'Start /?setup=quest&source=host&quest=email_ops_setup',
    'Start opens the guided email setup'
  );
  await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
  await shot(page, `g3-email-ready-${stageWidth}`);

  await page.locator('[data-role="first-mission-action"]').click();
  await page
    .locator('#specialistSetupJourneyModal.show')
    .waitFor({ state: 'visible', timeout: 15000 });
  await page.waitForTimeout(800);
  await shot(page, `g3-email-quest-open-${stageWidth}`);
  await page.locator('#specialistSetupJourneyClose').click();
  await page.locator('#specialistSetupJourneyModal').waitFor({ state: 'hidden', timeout: 10000 });

  await missions(page);
  const after = await openQuests(page);
  expect(after.status === 'In progress', 'after closing mid-setup the card reads In progress');
  expect(after.action.startsWith('Resume '), 'the in-progress email card offers Resume');
  const guided = page.locator('#emailSetupQuestCard');
  console.log(`Email Ops guided-setup card also visible: ${await guided.isVisible()}`);
  await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
  await shot(page, `g3-email-in-progress-${stageWidth}`);
  await page.close();
}

async function planStage() {
  const page = await missionThreeOnCard(stageWidth);
  const card = await openQuests(page);
  expect(card.kicker === 'Mission 03', 'Mission 03 is on the card');
  expect(card.title === 'Plan my first day', 'a plan-my-day hire is offered Plan my first day');
  await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
  await shot(page, `g3-plan-ready-${stageWidth}`);

  await page.locator('[data-role="first-mission-action"]').click();
  await page
    .locator('#onboardingPersonalAssistantAssignment')
    .waitFor({ state: 'visible', timeout: 15000 });
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
  await page.locator('#pafAssignmentPreview').waitFor({ state: 'visible', timeout: 15000 });
  await page.locator('#pafAssignmentConfirm').check();
  await page.locator('#pafApplyAssignmentBtn').click();
  await page.locator('#pafAssignmentResult').waitFor({ state: 'visible', timeout: 60000 });
  console.log(
    `first assignment result: ${((await page.locator('#pafAssignmentResultSummary').textContent()) || '').trim()}`
  );
  await shot(page, `g3-plan-applied-${stageWidth}`);

  const status = await missions(page);
  const connect = (status.missions || []).find(m => m.id === 'pa-connect-source');
  expect(connect?.status === 'completed', 'applying the first-day plan completed Mission 03');
  const after = await openQuests(page);
  expect(after.kicker === 'Mission 04', 'Mission 04 is on the card afterwards');
  expect(
    after.rows.some(row => row.includes('✓') && row.includes('Plan my first day')),
    'Mission 03 shows ✓ beneath the card'
  );
  await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
  await shot(page, `g3-plan-mission04-${stageWidth}`);

  // The Calendar capability card's Set up opens the creator on Calendar Ops.
  await page.goto(`${baseUrl}/?personal-assistant=working-agreement`, {
    waitUntil: 'domcontentloaded'
  });
  const setUpCalendar = page.locator('#personalAssistantCapabilities a', {
    hasText: 'Set up Calendar Ops'
  });
  await setUpCalendar.waitFor({ state: 'visible', timeout: 20000 });
  expect(
    (await setUpCalendar.getAttribute('href')) === '/?create=1&blueprint=calendar-ops',
    'the Calendar card links to the creator deep link'
  );
  await setUpCalendar.scrollIntoViewIfNeeded();
  await shot(page, `g3-calendar-card-${stageWidth}`);
  await setUpCalendar.click();
  await page.locator('#addFolderModal').waitFor({ state: 'visible', timeout: 15000 });
  await page.waitForFunction(
    () => window.ProjectTemplateCard?.getSelectedTemplate?.()?.id === 'calendar-ops',
    null,
    { timeout: 15000 }
  );
  expect(true, 'the creator opened with Calendar Ops preselected');
  await shot(page, `g3-calendar-creator-${stageWidth}`);
  await page.close();
}

// statesStage captures the card at rest: every mission deferred ("Saved for
// later", still resumable from the card), then the last one completed by Today
// serving a brief ("Complete", no action).
async function statesStage() {
  const width = stageWidth;
  const page = await missionThreeOnCard(width);
  for (const id of ['pa-connect-source', 'pa-first-brief']) {
    const skipped = await api(page, 'POST', '/api/progression/skip', { quest_id: id });
    console.log(`deferred ${id}: HTTP ${skipped.status}`);
  }
  const saved = await openQuests(page);
  expect(saved.kicker === 'Mission 04', 'with every mission resolved the card rests on Mission 04');
  expect(saved.status === 'Saved for later', 'a deferred last mission reads Saved for later');
  expect(saved.action.startsWith('Resume quest'), 'and stays resumable from the card');
  await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
  await shot(page, `g5-card-saved-for-later-${width}`);

  const refresh = await api(page, 'POST', '/api/personal-hq/brief/refresh');
  console.log(`brief refresh: HTTP ${refresh.status}`);
  let revision = '';
  for (let i = 0; i < 30 && !revision; i++) {
    await page.waitForTimeout(1000);
    const current = await api(page, 'GET', '/api/personal-hq/brief/current');
    revision = current.json?.revision?.id || current.json?.id || current.json?.revision_id || '';
  }
  console.log(`brief revision: ${revision || '(none)'}`);
  await api(page, 'GET', '/api/personal-assistant/today');
  const done = await openQuests(page);
  expect(done.status === 'Complete', 'Today serving a brief replaces the deferral with Complete');
  expect(done.action === '(no action)', 'a completed card offers no action');
  await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
  await shot(page, `g5-card-complete-${width}`);
  await page.close();
}

try {
  if (stage === 'card') await cardStage();
  else if (stage === 'states') await statesStage();
  else if (stage === 'folder') await folderStage();
  else if (stage === 'email') await emailStage();
  else if (stage === 'plan') await planStage();
  else if (stage === 'results') await resultsStage();
  else throw new Error(`unknown stage ${stage}`);
} catch (error) {
  console.error(`FAIL: ${error.message}`);
  exitCode = 1;
  if (lastPage && !lastPage.isClosed()) {
    await shot(lastPage, `failure-${stage}`).catch(() => {});
  }
} finally {
  if (problems.length) {
    console.log(`problems observed:\n  ${problems.join('\n  ')}`);
  } else {
    console.log('problems observed: none');
  }
  await browser.close();
  process.exit(exitCode);
}
