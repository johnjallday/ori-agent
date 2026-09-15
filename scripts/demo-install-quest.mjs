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

try {
  if (stage === 'install') await installStage();
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
