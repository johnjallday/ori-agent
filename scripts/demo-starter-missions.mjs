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
 *
 * Every stage prints the missions it observed and any console errors or failed
 * requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
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
  const old = new Date(Date.now() - 6 * 60 * 60 * 1000);
  for (const name of ['invoice-march.pdf', 'holiday-photo.jpg']) {
    writeFileSync(join(root, name), `demo ${name}`);
  }
  console.log(`fixture folder: ${root} (files dated ${old.toISOString()})`);
  return root;
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

try {
  if (stage === 'card') await cardStage();
  else if (stage === 'tidy') await tidyStage();
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
