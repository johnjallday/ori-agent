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
 *   full     Real install of both plugins from a fresh sandbox: provider, then
 *            REAPER install chained to its enable review, then the summary.
 *   stop     Fresh sandbox. Install REAPER, Cancel on the enable step, read the
 *            summary and the card, then reopen and finish both plugins.
 *   matrix   No install. Layout at each viewport and theme, then a keyboard pass.
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

const WIZARD = '#pluginSetupWizard';
const INSTALL_TIMEOUT = 180_000;

const wizardButton = (page, name) =>
  page.locator(WIZARD).getByRole('button', { name, exact: true });

async function waitForTitle(page, text) {
  await page
    .locator('#pluginSetupWizardTitle', { hasText: text })
    .waitFor({ timeout: INSTALL_TIMEOUT });
}

async function clickWhenEnabled(page, name) {
  const control = wizardButton(page, name);
  await control.waitFor({ timeout: INSTALL_TIMEOUT });
  await page.waitForFunction(
    ([selector, label]) =>
      [...document.querySelectorAll(`${selector} button`)].some(
        node => node.textContent === label && !node.disabled
      ),
    [WIZARD, name],
    { timeout: INSTALL_TIMEOUT }
  );
  await control.click();
}

// The primary decision must be on screen with no scrolling, at this viewport.
async function layoutFacts(page, name) {
  return page.locator(WIZARD).evaluate((dialog, label) => {
    const buttons = [...dialog.querySelectorAll('footer button')];
    const primary = buttons.find(node => node.textContent === label);
    const rect = primary?.getBoundingClientRect();
    return {
      primaryVisible: Boolean(rect && rect.top >= 0 && rect.bottom <= innerHeight),
      tallEnough: buttons.every(node => node.getBoundingClientRect().height >= 44),
      noSidewaysScroll: dialog.scrollWidth <= dialog.clientWidth + 1,
      inViewport: dialog.getBoundingClientRect().right <= innerWidth
    };
  }, name);
}

async function pluginStates() {
  const { plugins } = await api('/api/plugins');
  return Object.fromEntries(plugins.map(plugin => [plugin.name, Boolean(plugin.enabled)]));
}

const stages = {
  async full(page) {
    const before = (await api('/api/workspaces')).folders.length;
    const requirements = await attachFolder(page);
    await requirements
      .getByRole('button', { name: 'Install Music Project Management', exact: true })
      .click();
    await waitForTitle(page, 'What this setup needs');
    await shot(page, 'g4-intro');
    const intro = await page.locator(WIZARD).innerText();
    check('intro frames plugins as app-wide', /Ori plugins are app-wide/.test(intro));
    await wizardButton(page, 'Continue').click();
    await waitForTitle(page, 'Plugin 1 of 2 · Music Project Management');
    await wizardButton(page, 'Confirm installation').waitFor({ timeout: 60_000 });
    let facts = await layoutFacts(page, 'Confirm installation');
    check('Confirm is visible at 1280x800', facts.primaryVisible, JSON.stringify(facts));
    await shot(page, 'g2-provider-review-1280');
    await page.setViewportSize({ width: 390, height: 844 });
    facts = await layoutFacts(page, 'Confirm installation');
    check('Confirm is visible at 390x844', facts.primaryVisible, JSON.stringify(facts));
    await shot(page, 'g2-provider-review-390');
    await page.setViewportSize({ width: 1280, height: 800 });
    const review = await page.locator(WIZARD).innerText();
    check('review reads Enabled after: Yes', /Enabled after this action: Yes/.test(review));
    await clickWhenEnabled(page, 'Confirm installation');
    await waitForTitle(page, 'Plugin 2 of 2 · REAPER');
    check(
      'provider enabled by its one confirm',
      (await pluginStates())['music-project-management']
    );
    await wizardButton(page, 'Confirm installation').waitFor({ timeout: 60_000 });
    await shot(page, 'g3-reaper-install-review');
    const reaper = await page.locator(WIZARD).innerText();
    check('install says the next step enables', /No — the next step enables it/.test(reaper));
    await clickWhenEnabled(page, 'Confirm installation');
    await wizardButton(page, 'Confirm enablement').waitFor({ timeout: INSTALL_TIMEOUT });
    check('enable review opened by itself', true);
    const states = await pluginStates();
    check('REAPER installed and still disabled before consent', states['reaper-plugin'] === false);
    await waitForTitle(page, 'Plugin 2 of 2 · REAPER');
    const progress = await page.locator('.plugin-setup-wizard__progress').innerText();
    check(
      'progress names both plugins',
      /REAPER: /.test(progress) && /Music/.test(progress),
      progress
    );
    await shot(page, 'g3-enable-review-chained');
    await clickWhenEnabled(page, 'Confirm enablement');
    await waitForTitle(page, 'Plugin setup summary');
    const summary = await page.locator(WIZARD).innerText();
    check(
      'summary reports both as installed and enabled',
      (summary.match(/Installed and enabled/g) || []).length === 2,
      summary
    );
    check(
      'summary says nothing was created',
      /No workspace, Home, agent or model task/.test(summary)
    );
    await shot(page, 'g3-summary');
    await wizardButton(page, 'Back to setup').click();
    await page.locator(WIZARD).waitFor({ state: 'hidden' });
    const finalStates = await pluginStates();
    check(
      'both plugins are enabled on the server',
      finalStates['reaper-plugin'] && finalStates['music-project-management'],
      JSON.stringify(finalStates)
    );
    check('no workspace was created', (await api('/api/workspaces')).folders.length === before);
    await shot(page, 'g3-card-after-finish');
  },

  async stop(page) {
    const requirements = await attachFolder(page);
    await requirements.getByRole('button', { name: 'Install REAPER', exact: true }).click();
    await waitForTitle(page, 'What this setup needs');
    await wizardButton(page, 'Continue').click();
    await clickWhenEnabled(page, 'Confirm installation');
    await wizardButton(page, 'Confirm enablement').waitFor({ timeout: INSTALL_TIMEOUT });
    await wizardButton(page, 'Cancel').click();
    await waitForTitle(page, 'Plugin setup summary');
    const summary = await page.locator(WIZARD).innerText();
    check('summary reads Installed, not enabled', /Installed, not enabled/.test(summary), summary);
    check('summary never claims an enable', !/Installed and enabled/.test(summary));
    check('the untouched plugin reads Not changed', /Not changed/.test(summary));
    await shot(page, 'g3-summary-stopped');
    await wizardButton(page, 'Back to setup').click();
    await page.locator(WIZARD).waitFor({ state: 'hidden' });
    const row = requirements.locator('[data-requirement-kind="integration"]');
    await row
      .getByRole('button', { name: 'Enable REAPER', exact: true })
      .waitFor({ timeout: 60_000 });
    const card = await row.innerText();
    check('card shows Disabled · Enable', /Disabled · Enable/.test(card), card);
    check('REAPER is disabled on the server', (await pluginStates())['reaper-plugin'] === false);
    await shot(page, 'g3-card-disabled');
    // Reopen and finish: the stop-here exit lost nothing.
    await row.getByRole('button', { name: 'Enable REAPER', exact: true }).click();
    await waitForTitle(page, 'What this setup needs');
    await shot(page, 'g4-reopened-intro');
    await wizardButton(page, 'Continue').click();
    await clickWhenEnabled(page, 'Confirm enablement');
    await waitForTitle(page, 'Plugin 2 of 2 · Music Project Management');
    await clickWhenEnabled(page, 'Confirm installation');
    await waitForTitle(page, 'Plugin setup summary');
    const finished = await page.locator(WIZARD).innerText();
    check(
      'reopened run finishes both',
      (finished.match(/Installed and enabled/g) || []).length === 2,
      finished
    );
    await shot(page, 'g4-summary-finished');
    await wizardButton(page, 'Back to setup').click();
  },

  async matrix(page) {
    const requirements = await attachFolder(page);
    await page.evaluate(() => document.fonts.ready);
    const launch = requirements.getByRole('button', { name: 'Install REAPER', exact: true });
    for (const theme of ['light', 'dark']) {
      await page.evaluate(value => {
        document.documentElement.setAttribute('data-bs-theme', value);
      }, theme);
      for (const [width, height] of [
        [1280, 800],
        [1024, 700],
        [390, 844],
        [1024, 500]
      ]) {
        await page.setViewportSize({ width, height });
        await launch.scrollIntoViewIfNeeded();
        await launch.click();
        await waitForTitle(page, 'What this setup needs');
        await wizardButton(page, 'Continue').click();
        await wizardButton(page, 'Confirm installation').waitFor({ timeout: 60_000 });
        const facts = await layoutFacts(page, 'Confirm installation');
        check(
          `layout ${width}x${height} ${theme}`,
          facts.primaryVisible && facts.tallEnough && facts.noSidewaysScroll && facts.inViewport,
          JSON.stringify(facts)
        );
        await shot(page, `g5-${theme}-${width}x${height}`);
        await wizardButton(page, 'Cancel').click();
        await page.locator(WIZARD).waitFor({ state: 'hidden' });
      }
    }
    await page.setViewportSize({ width: 1280, height: 800 });
    await launch.focus();
    await page.keyboard.press('Enter');
    await waitForTitle(page, 'What this setup needs');
    const active = () => page.evaluate(() => document.activeElement?.textContent || '');
    check('opening focuses the title', /What this setup needs/.test(await active()));
    await page.keyboard.press('Tab');
    check('Tab reaches Continue first', (await active()) === 'Continue', await active());
    await page.keyboard.press('Enter');
    await wizardButton(page, 'Confirm installation').waitFor({ timeout: 60_000 });
    await page.keyboard.press('Tab');
    const first = await active();
    check('the decision precedes the report in Tab order', /Back|Confirm/.test(first), first);
    await page.keyboard.press('Escape');
    await page.locator(WIZARD).waitFor({ state: 'hidden' });
    check(
      'Escape returns focus to the launcher',
      (await active()) === 'Install REAPER',
      await active()
    );
    const name = await page
      .locator(WIZARD)
      .evaluate(node => document.getElementById(node.getAttribute('aria-labelledby'))?.id);
    check('dialog is labelled by its title', name === 'pluginSetupWizardTitle', name);
  },

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
