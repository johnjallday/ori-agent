/*
 * Drives the plugin setup wizard on the assistant's folder setup card against a
 * running, isolated demo server and saves screenshots. Real UI and real
 * endpoints. Stages that confirm a plugin download real reviewed releases into
 * the demo sandbox, so never point this at real app data.
 *
 *   node scripts/demo-plugin-setup-wizard.mjs <baseUrl> <outDir> <sandbox> <stage>
 *
 * Start the server with: ./scripts/demo-server.sh <port> <sandbox>
 *
 * Stages:
 *   open     Attach a folder, open the wizard, Cancel, check the card is unchanged.
 */
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { chromium } from 'playwright';

const [baseUrl, outDir, sandbox, stage = 'open'] = process.argv.slice(2);
if (!baseUrl || !outDir || !sandbox) {
  console.error('usage: demo-plugin-setup-wizard.mjs <baseUrl> <outDir> <sandbox> <stage>');
  process.exit(2);
}
mkdirSync(outDir, { recursive: true });

const results = [];
function check(name, ok, detail = '') {
  results.push({ name, ok: Boolean(ok), detail });
  console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${detail ? ` — ${detail}` : ''}`);
}

async function api(path, data) {
  const response = await fetch(`${baseUrl}${path}`, {
    method: data ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json' },
    body: data ? JSON.stringify(data) : undefined
  });
  if (!response.ok) throw new Error(`${path}: ${response.status} ${await response.text()}`);
  return response.json();
}

async function seed() {
  await api('/api/onboarding/skip', {});
  await api('/api/settings/workspace-root', { workspace_root: '' });
  let { personal_assistant: assistant } = await api('/api/personal-assistant');
  if (assistant.state === 'needs_hire') {
    await api('/api/personal-assistant/hire', {
      request_id: 'wizard-demo-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    });
    ({ personal_assistant: assistant } = await api('/api/personal-assistant'));
  }
  if (assistant.state === 'needs_hq')
    await api('/api/personal-assistant/hq', {
      request_id: 'wizard-demo-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  mkdirSync(join(sandbox, 'Desktop'), { recursive: true, mode: 0o750 });
  writeFileSync(join(sandbox, 'Desktop', 'Session.rpp'), '<REAPER_PROJECT 0.1 "7.0"\n>\n', {
    mode: 0o600
  });
}

async function attachFolder(page) {
  await page.goto(`${baseUrl}/?panel=today`);
  await page.waitForFunction(() => Boolean(window.PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    window.PersonalAssistantPanel.open(document.getElementById('personalAssistantLauncher'))
  );
  await page.locator('#personalAssistantAttachMenu, [aria-label="Attach"]').first().click();
  await page.locator('#personalAssistantFolderChip:enabled').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Desktop', exact: true })
    .click();
  const requirements = page.getByRole('region', { name: 'Setup requirements' });
  await requirements.waitFor({ timeout: 60_000 });
  return requirements;
}

async function shot(page, name) {
  await page.screenshot({ path: join(outDir, `${name}.png`) });
}

const stages = {
  async open(page) {
    const requirements = await attachFolder(page);
    const before = await requirements.innerText();
    await shot(page, 'g1-card-before');
    check(
      'attaching a folder opens no wizard',
      !(await page.locator('#pluginSetupWizard[open]').count())
    );
    const install = requirements.getByRole('button', { name: 'Install REAPER', exact: true });
    await install.click();
    // The assistant panel is a role=dialog too, so name the wizard by its id.
    const dialog = page.locator('#pluginSetupWizard');
    await dialog.waitFor();
    const title = await page.locator('#pluginSetupWizardTitle').innerText();
    check('header counts the steps', title === 'Plugin 1 of 2 · REAPER', title);
    const clip = await page.locator('#pluginSetupWizard').evaluate(node => {
      const rect = node.getBoundingClientRect();
      const inside = (x, y) => node.contains(document.elementFromPoint(x, y));
      return {
        inViewport: rect.top >= 0 && rect.bottom <= innerHeight && rect.right <= innerWidth,
        corners:
          inside(rect.left + 4, rect.top + 4) &&
          inside(rect.right - 4, rect.top + 4) &&
          inside(rect.left + 4, rect.bottom - 4) &&
          inside(rect.right - 4, rect.bottom - 4),
        parent: node.parentElement.tagName
      };
    });
    check('dialog is not clipped by the panel or composer', clip.inViewport && clip.corners);
    check('dialog mounts on the body', clip.parent === 'BODY', clip.parent);
    await shot(page, 'g1-wizard-open');
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' });
    check('card text is unchanged after Cancel', (await requirements.innerText()) === before);
    const focused = await page.evaluate(() => document.activeElement?.textContent);
    check('focus returns to the launching button', focused === 'Install REAPER', focused);
    await shot(page, 'g1-card-after-cancel');
  }
};

await seed();
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
const consoleErrors = [];
page.on('pageerror', error => consoleErrors.push(error.message));
if (process.env.DEMO_TRACE) {
  page.on('request', request => {
    if (/folder|offers/.test(request.url())) console.log(`-> ${request.method()} ${request.url()}`);
  });
  page.on('response', response => {
    if (/folder|offers/.test(response.url()))
      console.log(`<- ${response.status()} ${response.url()}`);
  });
  page.on('console', message => {
    if (message.type() === 'error') console.log(`console: ${message.text()}`);
  });
}
page.on('requestfailed', request =>
  console.log(
    `request failed: ${request.method()} ${request.url()} ${request.failure()?.errorText}`
  )
);
try {
  await stages[stage](page);
  check('no uncaught page errors', consoleErrors.length === 0, consoleErrors.join(' | '));
} catch (error) {
  check(`stage ${stage} finished`, false, error.message);
  await shot(page, `${stage}-failure`).catch(() => {});
} finally {
  await browser.close();
}
writeFileSync(join(outDir, `${stage}-results.json`), JSON.stringify(results, null, 2));
process.exit(results.every(result => result.ok) ? 0 : 1);
