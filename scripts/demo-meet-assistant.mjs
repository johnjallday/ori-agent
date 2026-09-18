/*
 * Drives Mission 01, "Meet your assistant" (tasks/prd-meet-your-assistant-mission.md),
 * against a running, isolated demo server and saves screenshots. Real UI and
 * real endpoints throughout; the only shortcut is closing first-run onboarding
 * through the API where a stage is not about the modal.
 *
 *   node scripts/demo-meet-assistant.mjs <baseUrl> <outDir> <stage> [--width=1280]
 *
 * Stages (each needs a FRESH sandbox: a hire cannot be undone):
 *   preset   New Agent opens the assistant preset while unhired; Hire lands on
 *            /?quest=build-hq with the relationship needs_hq; afterwards New
 *            Agent opens the ordinary form again.
 *
 * Every stage prints what it observed and any console errors or failed
 * requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, stage = 'preset', ...flags] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error('usage: node scripts/demo-meet-assistant.mjs <baseUrl> <outDir> <stage> [--width=n]');
  process.exit(1);
}
const flag = name => {
  const found = flags.find(value => value.startsWith(`--${name}=`));
  return found ? found.slice(name.length + 3) : '';
};
const out = resolve(outDir);
mkdirSync(out, { recursive: true });
const width = Number(flag('width')) || 1280;

const browser = await chromium.launch();
const problems = [];
let exitCode = 0;

// Noise every page on this build produces, unrelated to Mission 01: the update
// checker's request is cut off when a demo navigates away, and the Ask Ori
// launcher looks up an agent profile that a fresh sandbox does not have.
const KNOWN_NOISE = [
  /Error checking for updates: TypeError: Failed to fetch/,
  /\/api\/agents\?name=Ask%20Ori/,
  /Failed to load resource: the server responded with a status of 404/
];
const isNoise = text => KNOWN_NOISE.some(pattern => pattern.test(text));

function check(ok, message) {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${message}`);
  if (!ok) exitCode = 1;
}

async function newPage(viewportWidth = width) {
  const page = await browser.newPage({ viewport: { width: viewportWidth, height: 900 } });
  page.on('console', m => {
    if (m.type() === 'error') problems.push(`console: ${m.text()}`);
  });
  page.on('pageerror', e => problems.push(`pageerror: ${e.message}`));
  page.on('requestfailed', r => {
    // A navigation away cancels in-flight polls; that is not a broken page.
    if (!/ERR_ABORTED/.test(r.failure()?.errorText || '')) {
      problems.push(`request failed: ${r.url()}`);
    }
  });
  page.on('response', r => {
    if (r.status() >= 400) problems.push(`HTTP ${r.status()}: ${r.url()}`);
  });
  return page;
}

async function api(page, path, options = {}) {
  return page.evaluate(
    async ([p, o]) => {
      const response = await fetch(p, o);
      return { status: response.status, body: await response.json().catch(() => ({})) };
    },
    [path, options]
  );
}

async function relationshipState(page) {
  const { body } = await api(page, '/api/personal-assistant');
  return body?.personal_assistant?.state || '';
}

async function shot(page, name) {
  const file = join(out, `${name}.png`);
  await page.screenshot({ path: file, fullPage: false });
  console.log(`shot ${file}`);
}

async function closeOnboarding(page) {
  await page.goto(`${baseUrl}/agents`);
  const { status } = await api(page, '/api/onboarding/complete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}'
  });
  check(status === 200, `onboarding closed through the API (${status})`);
}

async function stagePreset() {
  const page = await newPage();
  await closeOnboarding(page);
  check((await relationshipState(page)) === 'needs_hire', 'relationship starts needs_hire');

  // /agents/create points at Mission 01 while unhired (FR40).
  await page.goto(`${baseUrl}/agents/create`);
  await page.locator('#assistantPointer').waitFor({ state: 'visible', timeout: 5000 }).catch(() => {});
  check(await page.locator('#assistantPointer').isVisible(), '/agents/create shows the pointer line');
  check(
    (await page.locator('#assistantPointerLink').getAttribute('href')) === '/agents?quest=meet-assistant',
    'the pointer links to Mission 01'
  );
  await shot(page, 'create-page-pointer');

  await page.goto(`${baseUrl}/agents`);
  await page.locator('#newAgentBtn').click();
  await page.locator('#cr-name').waitFor();
  await page.locator('#cr-appearance-host *').first().waitFor();
  for (const id of ['#cr-name', '#cr-appearance-host', '#cr-focus-group', '#cr-mandate', '#createSubmit', '#cr-standard-form']) {
    check((await page.locator(id).count()) === 1, `preset renders ${id}`);
  }
  for (const id of ['#cr-role', '#cr-model', '#cr-description', '#cr-favorite', '#cr-tags-host']) {
    check((await page.locator(id).count()) === 0, `preset omits ${id}`);
  }
  check((await page.locator('#cr-name').inputValue()) === 'Assistant', 'name is prefilled "Assistant"');
  check(
    await page.locator('#cr-name').evaluate(el => el === document.activeElement),
    'focus starts in the name field'
  );
  check((await page.locator('#createSubmit').textContent()).trim() === 'Hire assistant', 'button reads Hire assistant');
  await shot(page, 'preset-open');
  await page.locator('#cr-standard-form').scrollIntoViewIfNeeded();
  await shot(page, 'preset-bottom');

  // The swap to the standard form and back keeps the mission open.
  await page.locator('#cr-standard-form').click();
  await page.locator('#cr-role').waitFor();
  check((await page.locator('#cr-assistant-form').count()) === 1, 'standard form offers the way back');
  await page.locator('#cr-assistant-form').click();
  await page.locator('#cr-focus-group').waitFor();
  check(true, 'swapped back to the preset');

  await page.locator('#cr-name').fill('Atlas');
  await page.locator('#cr-mandate').fill('Keep this week realistic.');
  await page.locator('#createSubmit').click();
  await page.waitForURL(url => url.pathname === '/' && url.search.includes('quest=build-hq'), {
    timeout: 20000
  });
  check(true, `hire landed on ${new URL(page.url()).pathname}${new URL(page.url()).search}`);
  check((await relationshipState(page)) === 'needs_hq', 'relationship is needs_hq after the hire');
  const flag = await page.evaluate(() => sessionStorage.getItem('ori:assistant-just-hired'));
  check(flag === '1' || flag === null, `hand-over flag set for the HQ walkthrough (${flag})`);
  await page.waitForTimeout(1500);
  await shot(page, 'after-hire-home');

  await page.goto(`${baseUrl}/agents`);
  await page.locator('#newAgentBtn').click();
  await page.locator('#cr-role').waitFor();
  check((await page.locator('#cr-focus-group').count()) === 0, 'after the hire New Agent opens the standard form');
  check((await page.locator('#cr-assistant-form').count()) === 0, 'no hire link once hired');
  await shot(page, 'standard-after-hire');

  await page.goto(`${baseUrl}/agents/create`);
  await page.waitForTimeout(1000);
  check(!(await page.locator('#assistantPointer').isVisible()), '/agents/create drops the pointer once hired');
  await page.close();
}

try {
  if (stage === 'preset') await stagePreset();
  else throw new Error(`unknown stage ${stage}`);
} catch (error) {
  console.log(`FAIL ${error.message}`);
  exitCode = 1;
} finally {
  await browser.close();
}
const real = problems.filter(problem => !isNoise(problem));
for (const problem of real) console.log(`problem: ${problem}`);
if (real.length) exitCode = 1;
process.exit(exitCode);
