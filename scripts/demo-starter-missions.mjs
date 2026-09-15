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
 *   card     Mission 01 on the card after a hire, then Mission 02 with the other
 *            missions beneath it once HQ is built. 1280px and 400px.
 *   tidy     Mission 02 end to end: Start on the card, the creator with File
 *            Janitor preselected, Ori's mark on Create, the workspace's setup
 *            wizard, "In progress · Finish setup" on Home mid-wizard, a folder
 *            under --sandbox, Turn this on, readiness, and Mission 03 on the
 *            card afterwards. The OS folder dialog is the one thing mocked: its
 *            endpoint answers with the fixture path, and every request after it
 *            is real.
 *   email    Mission 03 for a --focus=help_with_email hire: Set up email opens
 *            the guided setup, and closing it mid-way reads In progress.
 *   plan     Mission 03 for a --focus=plan_my_day hire: the first-day plan
 *            completes it and Mission 04 takes the card; then the Calendar
 *            capability card opens the creator on Calendar Ops.
 *   results  Today Results after File Janitor files files: continue a tidy
 *            sandbox with --workspace=<slug> --folder=<its folder>. Scans,
 *            approves, confirms, reads the Today line, follows it to History,
 *            then requests a brief and checks Mission 04 completes on Today.
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
    await missions(page);
    const after = await openQuests(page);
    expect(after.kicker === 'Mission 02', 'Mission 02 is on the card once HQ is designated');
    expect(after.title === 'Tidy your Downloads', 'Mission 02 reads Tidy your Downloads');
    expect(after.rows.length === 3, 'three Starter rows sit beneath the card');
    expect(
      !after.rows.some(r => r.includes('Tidy your Downloads')),
      'the card mission is not repeated'
    );
    await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
    await shot(page, `g1-mission02-${width}`);
    await page.close();
  }
}

// guidePanelText reads Ori's panel, where walkthrough steps render.
async function guidePanelText(page) {
  const reply = page.locator('#oriGuideReply');
  if (!(await reply.count())) return '';
  return ((await reply.textContent()) || '').replace(/\s+/g, ' ').trim();
}

// fixtureFolder writes a throwaway folder for File Janitor to tidy. It must sit
// inside the server's sandbox: the feature really moves files.
function fixtureFolder() {
  if (!sandboxDir) throw new Error('--sandbox=<server sandbox dir> is required for this stage');
  const root = join(resolve(sandboxDir), `Downloads-demo-${Date.now().toString(36)}`);
  if (!root.startsWith(resolve(sandboxDir))) throw new Error(`refusing fixture ${root}`);
  mkdirSync(root, { recursive: true });
  writeSettledFiles(root, ['invoice-march.pdf', 'holiday-photo.jpg']);
  return root;
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
  writeSettledFiles(root, ['receipt-april.pdf', 'screenshot-notes.png']);

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
  await line.first().scrollIntoViewIfNeeded();
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

async function tidyStage() {
  const width = stageWidth;
  const page = await newPage(width, width < 600 ? 860 : 800);
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await api(page, 'POST', '/api/onboarding/skip');
  await hire(page);
  await buildHQ(page);
  await missions(page);

  // Start on the card.
  const card = await openQuests(page);
  expect(card.kicker === 'Mission 02', 'Mission 02 is on the card');
  await Promise.all([
    page.waitForURL(url => url.search === '' || !url.search.includes('quest='), {
      timeout: 15000
    }),
    page.locator('[data-role="first-mission-action"]').click()
  ]);

  // The creator opens with File Janitor preselected, and Ori explains step 1.
  const creator = page.locator('#addFolderModal');
  await creator.waitFor({ state: 'visible', timeout: 15000 });
  await page.waitForFunction(
    () => window.ProjectTemplateCard?.getSelectedTemplate?.()?.id === 'file-janitor',
    null,
    { timeout: 15000 }
  );
  expect(true, 'the creator opened with File Janitor preselected');
  expect(!page.url().includes('quest='), '?quest= was dropped from the URL');
  await page.waitForTimeout(1500);
  const step1 = await guidePanelText(page);
  console.log(`guide: ${step1}`);
  expect(step1.includes('Step 1 of 2'), 'Ori presents step 1 of 2');
  expect(!step1.includes('cannot point'), 'Ori does not claim it cannot point at Create');
  // Ori's panel sits beneath the dialog's backdrop, so a panel button there
  // could not be pressed. The walkthrough must not offer one.
  expect(
    (await page.locator('[data-ori-quest="tidy-downloads"][data-ori-quest-choice]').count()) === 0,
    'no unreachable panel choice is offered over the creator'
  );
  await shot(page, `g2-creator-blueprint-${width}`);

  // Advance the creator to its last step; the hand lands on Create there. The
  // Team step needs its File Curator role filled first, as a user would: the
  // role's Create opens the agent dialog over a suspended creator.
  for (let i = 0; i < 6 && !(await page.locator('#createFolderBtn').isVisible()); i++) {
    const missingRole = page.locator(
      '#wizardStep3:not([hidden]) #workspaceRoleRoster .ws-role-row button.btn-primary'
    );
    if (
      await missingRole
        .first()
        .isVisible()
        .catch(() => false)
    ) {
      await shot(page, `g2-creator-team-${width}`);
      await missingRole.first().click();
      await page.locator('#addAgentModal').waitFor({ state: 'visible', timeout: 10000 });
      await page.locator('#createAgentBtn').click();
      await page.locator('#addAgentModal').waitFor({ state: 'hidden', timeout: 10000 });
      await creator.waitFor({ state: 'visible', timeout: 10000 });
      await page.waitForTimeout(700);
      console.log(`guide after agent setup: ${await guidePanelText(page)}`);
      continue;
    }
    await page.locator('#wizardNextBtn').click();
    await page.waitForTimeout(700);
  }
  const create = page.locator('#createFolderBtn');
  expect(await create.isVisible(), 'the creator reached its Create step');
  await page.waitForFunction(
    () => document.getElementById('createFolderBtn')?.classList.contains('is-ori-coachmark'),
    null,
    { timeout: 5000 }
  );
  expect(true, "Ori's mark is on Create");
  await shot(page, `g2-creator-create-marked-${width}`);

  await Promise.all([page.waitForURL(/\/workspaces\/[^/?#]+/, { timeout: 30000 }), create.click()]);
  const slug = decodeURIComponent(new URL(page.url()).pathname.split('/')[2] || '');
  console.log(`created workspace page: ${page.url()}`);

  const wizard = page.locator('#setupWizardDialog');
  await wizard.waitFor({ state: 'visible', timeout: 20000 });
  expect(
    ((await page.locator('#setupWizardStepTitle').textContent()) || '').includes('folder'),
    'the setup wizard opened on its folder step'
  );
  await shot(page, `g2-wizard-folder-${width}`);

  // Mid-wizard, Home says the mission is in progress and links back here.
  const home = await newPage(width, width < 600 ? 860 : 800);
  const mid = await openQuests(home);
  expect(mid.status === 'In progress', 'mid-wizard the card reads In progress');
  expect(
    mid.action === `Finish setup /workspaces/${encodeURIComponent(slug)}`,
    'mid-wizard the card offers Finish setup on the new workspace'
  );
  await home.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
  await shot(home, `g2-card-in-progress-${width}`);
  await home.close();

  // Choose the folder. Only the OS dialog is mocked.
  const root = fixtureFolder();
  await page.route('**/api/folder-picker/select-path', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, selected: true, path: root })
    })
  );
  await page.locator('#fileJanitorWizardPick').click();

  // Walk the remaining steps with the wizard's own primary action.
  let lastTitle = '';
  for (let i = 0; i < 8; i++) {
    if (!(await wizard.isVisible())) break;
    const title = ((await page.locator('#setupWizardStepTitle').textContent()) || '').trim();
    const primary = page.locator('#setupWizardPrimary');
    await primary.waitFor({ state: 'visible', timeout: 10000 });
    await page
      .waitForFunction(() => !document.getElementById('setupWizardPrimary')?.disabled, null, {
        timeout: 15000
      })
      .catch(() => {});
    const label = ((await primary.textContent()) || '').trim();
    console.log(`wizard: "${title}" -> ${label}`);
    if (title !== lastTitle && /working|ready/i.test(title)) {
      await shot(page, `g2-wizard-${title.toLowerCase().replace(/\W+/g, '-')}-${width}`);
    }
    lastTitle = title;
    await primary.click();
    await page.waitForTimeout(1500);
  }

  const status = await missions(page);
  const tidy = (status.missions || []).find(m => m.id === 'pa-tidy-downloads');
  expect(tidy?.status === 'completed', 'Mission 02 completed when the wizard reached ready');

  const after = await openQuests(page);
  expect(after.kicker === 'Mission 03', 'Mission 03 is on the card afterwards');
  expect(
    after.rows.some(row => row.includes('✓') && row.includes('Tidy your Downloads')),
    'Mission 02 shows ✓ beneath the card'
  );
  await page.locator('[data-role="first-mission"]').scrollIntoViewIfNeeded();
  await shot(page, `g2-card-mission03-${width}`);
  await page.close();
}

// missionThreeOnCard hires with --focus, builds HQ, and defers Mission 02 the
// way a user would, so Mission 03 is the mission on the card.
async function missionThreeOnCard(width) {
  const page = await newPage(width, width < 600 ? 860 : 800);
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await api(page, 'POST', '/api/onboarding/skip');
  await hire(page);
  await buildHQ(page);
  const skipped = await api(page, 'POST', '/api/progression/skip', {
    quest_id: 'pa-tidy-downloads'
  });
  console.log(`deferred Mission 02: HTTP ${skipped.status}`);
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

try {
  if (stage === 'card') await cardStage();
  else if (stage === 'tidy') await tidyStage();
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
