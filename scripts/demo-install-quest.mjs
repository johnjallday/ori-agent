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
 *   restart  After plugin, with the sandbox sessions.db path as the fourth
 *            argument: primary Continue styling, then a saved declaration
 *            set to version 1 → Start over → fresh quest keeps the group.
 *
 * Every stage prints what it observed plus any console errors or failed
 * requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, stage = 'install', dbPath = ''] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error(
    'usage: node scripts/demo-install-quest.mjs <baseUrl> <outDir> [install|plugin|templates|restart <sessions.db>]'
  );
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

// restartStage runs against scripts/reaper-demo.sh after the plugin stage. It
// checks the install summary's primary Continue styling, marks the started
// plugin quest's saved declaration as version 1 in the sandbox database, then
// uses Start over and checks the fresh quest keeps the existing group.
async function restartStage() {
  if (!dbPath) throw new Error('restart stage needs the sandbox sessions.db path');
  const page = await newPage();
  await page.goto(baseUrl, { waitUntil: 'domcontentloaded' });
  await acceptMusicSpecialist(page);
  const enabled = await api(page, 'POST', '/api/plugins/reaper-plugin/enable');
  console.log(`enable plugin: HTTP ${enabled.status}`);
  const setup = page.locator('#specialistSetupJourneyModal');

  // 1. The install summary's Continue button is the primary action.
  await page.goto(`${baseUrl}/?setup=quest&source=host&quest=install_ori_reaper`, {
    waitUntil: 'domcontentloaded'
  });
  await describeModal(page, 'install quest');
  const styles = await page.locator('#specialistSetupJourneyActions button').evaluateAll(buttons =>
    buttons.map(b => {
      const style = getComputedStyle(b);
      return `${b.innerText.trim()}: primary=${b.dataset.primary || 'false'} bg=${style.backgroundColor} color=${style.color}`;
    })
  );
  console.log(`install summary buttons: ${JSON.stringify(styles)}`);
  await shot(page, '20-install-summary-primary-continue');

  // 2. Simulate a pre-split saved declaration on the started plugin quest.
  const status = await api(page, 'GET', '/api/setup-quests/reaper-plugin/reaper_setup');
  const runID = status.json?.setup_journey?.run_id || '';
  const groupBefore = (status.json?.setup_journey?.steps || []).find(
    step => step.kind === 'project_connect'
  )?.preparation?.exists;
  if (!/^[0-9a-f-]{36}$/.test(runID)) throw new Error(`no started plugin quest: ${runID}`);
  // Bump the revision too, so a reconcile already in flight cannot write the
  // current version back over the seed.
  execFileSync('sqlite3', [
    dbPath,
    `UPDATE setup_journey_run SET declaration_version = 1, state_revision = state_revision + 1 WHERE id = '${runID}'`
  ]);
  const seeded = await api(page, 'GET', '/api/setup-quests/reaper-plugin/reaper_setup');
  console.log(
    `seeded declaration_version=1 on ${runID} (group exists=${groupBefore}) incompatible=${Boolean(seeded.json?.setup_journey?.declaration_incompatible)}`
  );

  await page.goto(`${baseUrl}/?setup=quest&plugin=reaper-plugin&quest=reaper_setup`, {
    waitUntil: 'domcontentloaded'
  });
  await describeModal(page, 'incompatible');
  const incompatibleText = (
    await page.locator('#specialistSetupJourneyContent').innerText()
  ).replace(/\s+/g, ' ');
  console.log(`incompatible panel: ${JSON.stringify(incompatibleText)}`);
  if (!incompatibleText.includes('Only your setup progress is reset.'))
    problems.push('the Start over explanation is missing');
  await shot(page, '21-incompatible-start-over');

  // 3. Start over creates a fresh root that still sees the existing group.
  const startOver = setup.getByRole('button', { name: 'Start over', exact: true });
  await clickAndSettle(page, startOver, 'Start over');
  const fresh = await describeModal(page, 'after start over');
  const after = await api(page, 'GET', '/api/setup-quests/reaper-plugin/reaper_setup');
  const journey = after.json?.setup_journey || {};
  console.log(
    `after start over: run=${journey.run_id} incompatible=${Boolean(journey.declaration_incompatible)} steps=${JSON.stringify((journey.steps || []).map(s => `${s.id}=${s.status}`))} group exists=${(journey.steps || []).find(s => s.kind === 'project_connect')?.preparation?.exists}`
  );
  if (journey.run_id === runID || journey.declaration_incompatible)
    problems.push('Start over did not create a fresh compatible root');
  await shot(page, '22-after-start-over');
  console.log(`titles: fresh=${fresh.title}`);
}

// templatesStage imports the quest-eligible fixture as a user template, authors
// its setup quest on the Templates page, and checks the four-step editor and
// that Guided Setup stays hidden while the integration is not installed.
async function templatesStage() {
  const page = await newPage();
  await page.goto(baseUrl, { waitUntil: 'domcontentloaded' });
  await api(page, 'POST', '/api/onboarding/skip');
  const fixture = resolve('internal/projecttemplates/testdata/user-setup-quest-eligible');
  const imported = await api(page, 'POST', '/api/project-templates/import', {
    path: fixture,
    name: 'Eligible local project'
  });
  const templateID = imported.json?.template?.id || imported.json?.id || '';
  console.log(`import fixture: HTTP ${imported.status} id=${templateID}`);

  const selectTemplate = async () => {
    await page.goto(`${baseUrl}/templates`, { waitUntil: 'domcontentloaded' });
    const row = page.locator('#tplList button', { hasText: 'Eligible local project' }).first();
    await clickAndSettle(page, row, 'select template');
  };
  await selectTemplate();
  await clickAndSettle(page, page.locator('#tplTabSetupQuest'), 'Setup quest tab');
  const create = page.locator('#tplUserQuestCreate');
  if (await create.isVisible()) await clickAndSettle(page, create, 'Create setup quest');
  await page.locator('#tplUserQuestForm').waitFor({ state: 'visible', timeout: 20000 });
  const stepRows = await page
    .locator('#tplUserQuestSteps [data-quest-step]')
    .evaluateAll(rows => rows.map(row => row.dataset.questStep));
  const launchInputs = await page
    .locator(
      '#tplUserQuestForm input[id^="tplUserQuestGroup"], #tplUserQuestForm [id^="tplUserQuestRuntime"]'
    )
    .evaluateAll(inputs => inputs.map(input => input.id));
  console.log(
    `editor steps=${JSON.stringify(stepRows)} launch inputs=${JSON.stringify(launchInputs)}`
  );
  if (stepRows.length !== 4 || launchInputs.some(id => id.includes('Runtime')))
    problems.push('editor is not the four-step, group-only form');
  await page.locator('#tplUserQuestForm').scrollIntoViewIfNeeded();
  await shot(page, '30-templates-quest-editor');
  const integration = page.locator('#tplUserQuestIntegration');
  if (await integration.isVisible()) await integration.selectOption('ori_reaper').catch(() => {});
  await clickAndSettle(page, page.locator('#tplUserQuestSaveBtn'), 'Save setup quest');
  await page.waitForTimeout(1500);
  console.log(
    `after save: ${JSON.stringify((await page.locator('#tplUserQuestStatus').innerText()).replace(/\s+/g, ' '))}`
  );
  const saved = await api(page, 'GET', `/api/project-templates/${templateID}/setup-quest`);
  const steps = saved.json?.user_setup_quest?.steps || saved.json?.setup_quest?.steps || [];
  console.log(
    `saved quest: HTTP ${saved.status} steps=${JSON.stringify(steps.map(step => step.kind))}`
  );
  await shot(page, '31-templates-quest-saved');

  const catalog = await api(page, 'GET', '/api/setup-quests');
  const listed = (catalog.json?.quests || []).filter(quest => quest.source === 'user_template');
  console.log(`catalog user-template quests while not installed: ${listed.length}`);
  await selectTemplate();
  const open = page.locator('#tplQuestOpen');
  await page.waitForTimeout(1500);
  console.log(`Open Guided Setup visible: ${await open.isVisible()}`);
  if (await open.isVisible())
    problems.push('Open Guided Setup shown while the integration is not installed');
  await shot(page, '32-templates-overview-no-guided-setup');
}

try {
  if (stage === 'install') await installStage();
  else if (stage === 'plugin') await pluginStage();
  else if (stage === 'templates') await templatesStage();
  else if (stage === 'restart') await restartStage();
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
