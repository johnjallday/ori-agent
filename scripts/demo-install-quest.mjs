/*
 * Drives the generated install quest (setup-quest-install-split) against a
 * running, isolated demo server and saves screenshots. It uses real UI and real
 * endpoints. The one shortcut is setting up the hired assistant, its HQ and
 * the accepted music specialist through their own APIs, which the demo sandbox
 * owns.
 *
 *   node scripts/demo-install-quest.mjs <baseUrl> <outDir> [stage]
 *
 * Stages:
 *   install  (default) Home specialist card → install quest modal; Plugins →
 *            Available integrations → Guided Setup → the same modal.
 *   plugin   Needs scripts/reaper-demo.sh (plugin installed and enabled) and a
 *            reviewed pin matching the staged candidate's version. Install
 *            quest handoff → two-screen plugin quest → Build Group → Create
 *            New Workspace → disable the plugin → precondition panel → install
 *            quest.
 *
 * Every stage prints what it observed plus any console errors or failed
 * requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, stage = 'install'] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error('usage: node scripts/demo-install-quest.mjs <baseUrl> <outDir> [install]');
  process.exit(1);
}
const out = resolve(outDir);
mkdirSync(out, { recursive: true });

const browser = await chromium.launch();
const problems = [];

async function newPage(width = 1280, height = 900) {
  const page = await browser.newPage({ viewport: { width, height } });
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
  await page.screenshot({ path: file });
  console.log(`screenshot ${file}`);
}

async function api(page, method, path, data) {
  const response = await page.request.fetch(`${baseUrl}${path}`, { method, data });
  let json = null;
  try {
    json = await response.json();
  } catch (_) {}
  return { status: response.status(), json };
}

async function assistant(page) {
  const { json } = await api(page, 'GET', '/api/personal-assistant');
  return json?.personal_assistant || json?.data?.personal_assistant || {};
}

// acceptMusicSpecialist hires the assistant, builds its HQ and accepts the
// music specialist, skipping only steps the demo sandbox owns.
async function acceptMusicSpecialist(page) {
  await api(page, 'POST', '/api/onboarding/skip');
  let current = await assistant(page);
  if (current.state === 'needs_hire') {
    const hire = await api(page, 'POST', '/api/personal-assistant/hire', {
      request_id: `demo-hire-${Date.now()}`,
      if_version: current.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my music week organised.',
      focus_areas: []
    });
    console.log(`hire: HTTP ${hire.status}`);
    current = await assistant(page);
  }
  if (current.state === 'needs_hq') {
    const hq = await api(page, 'POST', '/api/personal-assistant/hq', {
      request_id: `demo-hq-${Date.now()}`,
      if_version: current.state_version ?? 0,
      name: 'Demo HQ',
      timezone: 'UTC',
      schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri'],
      schedule_time: '08:00'
    });
    console.log(`hq: HTTP ${hq.status}`);
    current = await assistant(page);
  }
  const accepted = await api(page, 'POST', '/api/personal-assistant/specialist', {
    if_version: current.state_version ?? 0,
    decision: 'accepted',
    slug: 'music_production'
  });
  current = await assistant(page);
  console.log(
    `specialist: HTTP ${accepted.status} state=${current.state} offer=${current.specialist_offer_state} slug=${current.specialist_slug}`
  );
}

async function describeModal(page, label) {
  const modal = page.locator('#specialistSetupJourneyModal');
  await modal.waitFor({ state: 'visible', timeout: 20000 });
  await page.locator('#specialistSetupJourneyBusy').waitFor({ state: 'hidden', timeout: 20000 });
  // Let Bootstrap's fade finish so screenshots show the settled modal.
  await page
    .locator('#specialistSetupJourneyModal.show')
    .waitFor({ state: 'visible', timeout: 20000 });
  await page.waitForTimeout(600);
  const title = await page.locator('#specialistSetupJourneyTitle').innerText();
  const rail = await page
    .locator('.setup-journey__step-button')
    .evaluateAll(buttons =>
      buttons.map(
        b =>
          `${b.innerText.replace(/\s+/g, ' ').trim()} [${b.dataset.status}${b.disabled ? ', disabled' : ''}]`
      )
    );
  const disabled = await modal.locator('button:disabled').count();
  const stepTitle = await page.locator('#specialistSetupJourneyStepTitle').innerText();
  const stepState = await page.locator('#specialistSetupJourneyStepState').innerText();
  const actions = await page.locator('#specialistSetupJourneyActions button').allTextContents();
  console.log(
    `${label}: title=${JSON.stringify(title)} rail=${JSON.stringify(rail)} disabled_buttons=${disabled}`
  );
  console.log(
    `${label}: step=${JSON.stringify(stepState)} ${JSON.stringify(stepTitle)} actions=${JSON.stringify(actions)}`
  );
  return { title, rail, disabled };
}

async function installStage() {
  const page = await newPage();
  await page.goto(baseUrl, { waitUntil: 'domcontentloaded' });
  await acceptMusicSpecialist(page);

  // Home's specialist setup card, in the assistant's Today panel.
  await page.goto(baseUrl, { waitUntil: 'domcontentloaded' });
  const launcher = page.locator('#personalAssistantLauncher');
  await launcher.waitFor({ state: 'visible', timeout: 20000 });
  if (await page.locator('#personalAssistantPanel').isHidden()) await launcher.click();
  const todayTab = page.locator('#personalAssistantTodayTab');
  if ((await todayTab.getAttribute('aria-selected')) !== 'true') await todayTab.click();
  const card = page.locator('#personalAssistantSpecialistSetup');
  await card.waitFor({ state: 'visible', timeout: 20000 });
  await card.scrollIntoViewIfNeeded();
  console.log(
    `home card: title=${JSON.stringify(await page.locator('#personalAssistantSpecialistSetupTitle').innerText())} actions=${JSON.stringify(await page.locator('#personalAssistantSpecialistSetupActions > *').allTextContents())}`
  );
  await shot(page, '01-home-setup-card');
  await page.locator('#personalAssistantSpecialistSetupActions button').first().click();
  const fromHome = await describeModal(page, 'from Home');
  await shot(page, '02-install-quest-from-home');
  await page.locator('.setup-journey__step-button').nth(1).click();
  await describeModal(page, 'summary step selected');
  await shot(page, '03-install-quest-summary-step');

  // Plugins page: the reviewed integration listed as available.
  const plugins = await newPage();
  await plugins.goto(`${baseUrl}/plugins`, { waitUntil: 'domcontentloaded' });
  const available = plugins.locator('#availableIntegrationsCard');
  await available.waitFor({ state: 'visible', timeout: 20000 });
  await available.scrollIntoViewIfNeeded();
  console.log(
    `available integrations: ${JSON.stringify((await available.innerText()).replace(/\s+/g, ' '))}`
  );
  const guided = available.locator('a', { hasText: 'Guided Setup' });
  console.log(`guided setup href=${await guided.getAttribute('href')}`);
  await shot(plugins, '04-plugins-available-integrations');
  await guided.click();
  const fromPlugins = await describeModal(plugins, 'from Plugins');
  await shot(plugins, '05-install-quest-from-plugins');

  const status = await api(plugins, 'GET', '/api/host-setup-quests/install_ori_reaper/status');
  console.log(
    `install quest status: exists=${status.json?.exists} run=${status.json?.setup_journey?.run_id}`
  );
  if (fromHome.title !== fromPlugins.title)
    problems.push('Home and Plugins opened different quests');
}

async function clickAndSettle(page, locator, label) {
  await locator.waitFor({ state: 'visible', timeout: 20000 });
  console.log(`click: ${label}`);
  await locator.click();
  await page.waitForTimeout(800);
}

// pluginStage runs against scripts/reaper-demo.sh, which installs and enables
// the staged plugin candidate: install quest handoff, the two-screen plugin
// quest, group and workspace creation, the workspace Setup Wizard, then the
// integration precondition after disabling the plugin.
async function pluginStage() {
  const page = await newPage();
  await page.goto(baseUrl, { waitUntil: 'domcontentloaded' });
  await acceptMusicSpecialist(page);
  const setup = page.locator('#specialistSetupJourneyModal');

  // 1. The install quest is ready and hands off to the plugin's own quest.
  await page.goto(`${baseUrl}/?setup=quest&source=host&quest=install_ori_reaper`, {
    waitUntil: 'domcontentloaded'
  });
  await describeModal(page, 'install quest');
  console.log(
    `install receipt: ${JSON.stringify((await page.locator('#specialistSetupJourneyReceipt').innerText()).replace(/\s+/g, ' '))}`
  );
  await shot(page, '10-install-quest-ready-handoff');
  const handoff = setup.getByRole('button', { name: /^Continue: / });
  await clickAndSettle(page, handoff, await handoff.innerText());
  const quest = await describeModal(page, 'plugin quest');
  console.log(
    `plugin quest description: ${JSON.stringify(await page.locator('#specialistSetupJourneyDescription').innerText())}`
  );
  if ((await setup.innerText()).includes('Set Up REAPER'))
    problems.push('a "Set Up REAPER" preparation screen is still shown');
  await shot(page, '11-plugin-quest-two-screens');

  // 2. Build the group through the shared creator (skipped when a previous run
  // in this sandbox already created it).
  const creator = page.locator('#addFolderModal');
  const buildGroup = setup.getByRole('button', { name: 'Build Group', exact: true });
  if (await buildGroup.isVisible()) {
    await clickAndSettle(page, buildGroup, 'Build Group');
    await creator.waitFor({ state: 'visible', timeout: 20000 });
    await shot(page, '12-group-creator');
    await clickAndSettle(page, creator.getByRole('button', { name: 'Review →' }), 'Review →');
    await clickAndSettle(
      page,
      creator.getByRole('button', { name: 'Review group', exact: true }),
      'Review group'
    );
    await clickAndSettle(
      page,
      creator.getByRole('button', { name: /^Create group .* only$/ }),
      'Create group only'
    );
    await creator.waitFor({ state: 'hidden', timeout: 30000 });
  } else {
    console.log('group already exists in this sandbox');
  }
  const afterGroup = await describeModal(page, 'after group');
  await shot(page, '13-plugin-quest-after-group');

  // 3. Create the workspace through the shared creator with a new project.
  const createWorkspace = setup.getByRole('button', { name: 'Create New Workspace', exact: true });
  if (!(await createWorkspace.isVisible())) {
    await clickAndSettle(
      page,
      setup.locator('.setup-journey__step-button').nth(1),
      'Create New Workspace screen'
    );
  }
  await clickAndSettle(page, createWorkspace, 'Create New Workspace');
  await creator.waitFor({ state: 'visible', timeout: 20000 });
  await creator.getByRole('textbox', { name: 'Workspace name', exact: true }).fill('Demo Song');
  const projectChoice = creator.getByRole('combobox', { name: 'Project', exact: true });
  if (await projectChoice.isVisible()) await projectChoice.selectOption('new_project');
  await shot(page, '14-workspace-creator');
  for (let guard = 0; guard < 6; guard++) {
    const create = creator.locator('#createFolderBtn');
    if (await create.isVisible()) {
      if (await create.isEnabled()) break;
    }
    const next = creator.locator('#wizardNextBtn');
    if (!(await next.isVisible()) || !(await next.isEnabled())) break;
    await clickAndSettle(page, next, 'wizard next');
  }
  await shot(page, '15-workspace-review');
  let afterWorkspace = { title: '(not created)' };
  let receipts = {};
  if (await creator.locator('#createFolderBtn').isVisible()) {
    await clickAndSettle(page, creator.locator('#createFolderBtn'), 'create workspace');
    await creator.waitFor({ state: 'hidden', timeout: 60000 });
    afterWorkspace = await describeModal(page, 'after workspace');
    await shot(page, '16-plugin-quest-after-workspace');
    const statusAfter = await api(page, 'GET', '/api/setup-quests/reaper-plugin/reaper_setup');
    receipts = statusAfter.json?.setup_journey?.receipts || {};
    console.log(
      `plugin quest receipts: ${JSON.stringify(receipts)} steps=${JSON.stringify((statusAfter.json?.setup_journey?.steps || []).map(s => `${s.id}=${s.status}`))}`
    );
  } else {
    // The creator's Team step requires every role filled, which needs a
    // configured model. A fresh demo sandbox has none, so stop here.
    const attention = await creator.innerText();
    console.log(
      `workspace creation not reachable: ${JSON.stringify((attention.match(/NEEDS ATTENTION.{0,120}/i) || ['team step'])[0].replace(/\s+/g, ' '))}`
    );
    await clickAndSettle(
      page,
      creator.getByRole('button', { name: 'Cancel', exact: true }),
      'Cancel creator'
    );
  }

  // 4. The workspace's own Setup Wizard is where live control is asked.
  const workspace = receipts.project_workspace_id
    ? await api(page, 'GET', `/api/workspaces/${receipts.project_workspace_id}`)
    : { json: null };
  const slug = workspace.json?.folder_slug;
  if (slug) {
    await page.goto(`${baseUrl}/workspaces/${slug}?panel=settings`, {
      waitUntil: 'domcontentloaded'
    });
    await page.waitForTimeout(3000);
    const text = (await page.locator('body').innerText()).replace(/\s+/g, ' ');
    const match = text.match(/.{0,80}(live control|Live control|REAPER control).{0,120}/);
    console.log(
      `workspace setup mentions: ${JSON.stringify(match ? match[0] : 'no live-control text found')}`
    );
    await shot(page, '17-workspace-setup-wizard');
  } else if (receipts.project_workspace_id) {
    problems.push(`workspace route unavailable: ${JSON.stringify(workspace)}`);
  }

  // 5. Disabling the plugin puts the quest behind its integration precondition.
  const disabled = await api(page, 'POST', '/api/plugins/reaper-plugin/disable');
  console.log(`disable plugin: HTTP ${disabled.status}`);
  await page.goto(`${baseUrl}/?setup=quest&plugin=reaper-plugin&quest=reaper_setup`, {
    waitUntil: 'domcontentloaded'
  });
  const blocked = await describeModal(page, 'precondition');
  console.log(
    `precondition panel: ${JSON.stringify((await page.locator('#specialistSetupJourneyContent').innerText()).replace(/\s+/g, ' '))}`
  );
  await shot(page, '18-precondition-panel');
  const openInstall = setup.getByRole('button', { name: 'Open install quest', exact: true });
  await clickAndSettle(page, openInstall, 'Open install quest');
  const reopened = await describeModal(page, 'install quest from precondition');
  await shot(page, '19-install-quest-from-precondition');
  console.log(
    `titles: install→plugin=${quest.title} afterGroup=${afterGroup.title} afterWorkspace=${afterWorkspace.title} blocked=${blocked.title} reopened=${reopened.title}`
  );
}

try {
  if (stage === 'install') await installStage();
  else if (stage === 'plugin') await pluginStage();
  else throw new Error(`unknown stage ${stage}`);
} catch (error) {
  problems.push(`stage failed: ${error.message}`);
} finally {
  await browser.close();
}
if (problems.length) {
  console.log(`PROBLEMS (${problems.length}):\n${problems.join('\n')}`);
  process.exit(1);
}
console.log('no console errors or failed requests');
