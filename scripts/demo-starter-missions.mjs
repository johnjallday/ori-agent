/*
 * Drives the starter missions (tasks/prd-starter-missions.md) against a
 * running, isolated demo server and saves screenshots. It uses real UI and
 * real endpoints. The shortcuts are skipping first-run onboarding and hiring
 * plus building HQ through the assistant API, all of which the demo sandbox
 * owns.
 *
 *   node scripts/demo-starter-missions.mjs <baseUrl> <outDir> <stage> [focus,...]
 *
 * Stages:
 *   card     Mission 01 on the card after a hire, then Mission 02 with the other
 *            missions beneath it once HQ is built. 1280px and 400px.
 *
 * Every stage prints the missions it observed and any console errors or failed
 * requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, stage = 'card', focusArg = ''] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error(
    'usage: node scripts/demo-starter-missions.mjs <baseUrl> <outDir> <stage> [focus,...]'
  );
  process.exit(1);
}
const out = resolve(outDir);
mkdirSync(out, { recursive: true });
const focusAreas = focusArg ? focusArg.split(',').filter(Boolean) : [];

const browser = await chromium.launch();
const problems = [];
let exitCode = 0;

async function newPage(width, height) {
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

try {
  if (stage === 'card') await cardStage();
  else throw new Error(`unknown stage ${stage}`);
} catch (error) {
  console.error(`FAIL: ${error.message}`);
  exitCode = 1;
} finally {
  if (problems.length) {
    console.log(`problems observed:\n  ${problems.join('\n  ')}`);
  } else {
    console.log('problems observed: none');
  }
  await browser.close();
  process.exit(exitCode);
}
