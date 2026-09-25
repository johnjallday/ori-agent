// Drive the real hire → Today HQ card → HQ build in a disposable demo sandbox.
// Usage: node scripts/demo-folder-first.mjs http://localhost:8931 /tmp/ori-folder-first-shots
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const [base, output] = process.argv.slice(2);
if (!base || !output) throw new Error('usage: demo-folder-first.mjs <base-url> <shots-dir>');
mkdirSync(output, { recursive: true });
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
const errors = [];
page.on('pageerror', error => errors.push(error.message));
async function shot(name) {
  const path = join(output, `${name}.png`);
  await page.screenshot({ path });
  console.log(path);
}
try {
  await page.goto(`${base}/agents`);
  const complete = await page.evaluate(async () => {
    const res = await fetch('/api/onboarding/complete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}'
    });
    return res.status;
  });
  if (complete !== 200) throw new Error(`onboarding completion failed: ${complete}`);
  await page.goto(`${base}/`);
  if (!(await page.locator('#cockpitShowFolderBtn').isHidden())) {
    throw new Error('Explore a folder appeared before hiring');
  }
  await shot('00-before-hire');
  await page.goto(`${base}/agents`);
  await page.locator('#newAgentBtn').click();
  await page.locator('#cr-name').fill('Atlas');
  await shot('01-hire');
  await page.locator('#createSubmit').click();
  await page.waitForURL(url => url.searchParams.get('panel') === 'today' || url.pathname === '/', {
    timeout: 30000
  });
  await page.locator('#personalAssistantHQCard').waitFor({ state: 'visible' });
  if (!(await page.locator('#personalAssistantPanel').isVisible())) {
    throw new Error('Today panel did not open on hire');
  }
  if (await page.evaluate(() => window.OriPersonalHQQuest?.isActive())) {
    throw new Error('Map quest started on the plain Today hand-over');
  }
  await shot('02-hq-card');
  await page.locator('#personalAssistantClose').click();
  await page.locator('#cockpitShowFolderBtn').waitFor({ state: 'visible' });
  await page.locator('#cockpitShowFolderBtn').click();
  await page.locator('#personalAssistantHQCard').waitFor({ state: 'visible' });
  await shot('02c-home-action-opens-hq');
  await page.goto(`${base}/?quest=build-hq`);
  await page.waitForFunction(() => window.OriPersonalHQQuest?.isActive() === true);
  await shot('02a-alternate-map-quest');
  await page.goto(`${base}/?panel=today`);
  await page.locator('#personalAssistantHQCard').waitFor({ state: 'visible' });
  const rootStatus = await page.locator('#personalAssistantHQRootStatus').textContent();
  console.log(
    'directory:',
    await page.locator('#personalAssistantHQRoot').inputValue(),
    rootStatus
  );
  if (await page.locator('#personalAssistantHQBuild').isDisabled()) {
    // Headless demo sandboxes cannot use the native folder picker. Confirm the
    // sandbox's server-suggested directory via Settings, then reload the card.
    const confirmed = await page.evaluate(async () => {
      const path = document.getElementById('personalAssistantHQRoot').value;
      const res = await fetch('/api/settings/workspace-root', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workspace_root: path })
      });
      return { status: res.status, body: await res.json() };
    });
    if (confirmed.status !== 200)
      throw new Error(`directory confirmation: ${JSON.stringify(confirmed)}`);
    await page.evaluate(() => window.PersonalAssistantHQCard.load());
    await page.locator('#personalAssistantHQBuild').waitFor({ state: 'visible' });
    if (await page.locator('#personalAssistantHQBuild').isDisabled()) {
      throw new Error('Build remained disabled after directory confirmation');
    }
    await shot('02b-confirmed-directory');
  }
  await page.locator('#personalAssistantHQBuild').click();
  const hqReceipt = page.locator(
    '#personalAssistantDoneItems .personal-assistant-today__hq-receipt'
  );
  await hqReceipt.locator('summary').waitFor({ timeout: 30000 });
  await hqReceipt.locator('summary').click();
  await hqReceipt.locator('li').first().waitFor({ timeout: 30000 });
  if (!(await hqReceipt.innerText()).includes('Directory ·'))
    throw new Error('fresh HQ build did not show the selected directory in Done');
  console.log('receipt:', await hqReceipt.innerText());
  await hqReceipt.locator('li').last().scrollIntoViewIfNeeded();
  await shot('03-hq-receipt');
  await page.locator('#personalAssistantFolderChooser').waitFor({ state: 'visible' });
  await page.locator('#personalAssistantFolderChooser').scrollIntoViewIfNeeded();
  if (
    !(await page.locator('#personalAssistantFolderTitle').textContent()).includes(
      "Now let's explore a folder"
    )
  ) {
    throw new Error('first-folder hand-over did not appear after HQ build');
  }
  await shot('04-first-folder-prompt');
  await page.locator('#darkModeToggle').click();
  await page.waitForTimeout(350);
  await shot('04d-inline-chooser-dark');
  await page.locator('#personalAssistantTodayMore > summary').click();
  await page.locator('#personalAssistantTodayAgreement').waitFor({ state: 'visible' });
  await shot('04e-more-menu-dark');
  await page.locator('#personalAssistantTodayMore > summary').click();
  await page.locator('#darkModeToggle').click();
  await page.waitForTimeout(350);
  await page.goto(`${base}/?panel=today`);
  await page.locator('#personalAssistantFolder').waitFor({ state: 'visible' });
  await page.locator('#personalAssistantFolderChooser').waitFor({ state: 'visible' });
  if (
    (await page.locator('#personalAssistantFolderTitle').textContent()).includes(
      "Now let's explore"
    )
  ) {
    throw new Error('first-folder hand-over repeated on reload');
  }
  await page.locator('#personalAssistantClose').click();
  await page.locator('#cockpitShowFolderBtn').click();
  await page.locator('#personalAssistantFolderChooser').waitFor({ state: 'visible' });
  await shot('04b-home-action-opens-chooser');
  await page.goto(`${base}/?panel=today&folder=show`);
  await page.locator('#personalAssistantFolderChooser').waitFor({ state: 'visible' });
  if (new URL(page.url()).searchParams.has('folder'))
    throw new Error('folder deep link was not consumed');
  await page.locator('#personalAssistantClose').click();
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await page.locator('#addFolderModal').waitFor({ state: 'visible' });
  await shot('04c-new-workspace-remains');
  await page.locator('#addFolderModal [aria-label="Close create workspace"]').click();
  await page.locator('#addFolderModal').waitFor({ state: 'hidden' });
  await page.locator('#cockpitQuestsToggle').click();
  await page.locator('#questLog [data-role="quests"] li').first().waitFor();
  if (
    (await page.locator('#questLog [data-role="quests"] li').count()) !== 3 ||
    (await page.locator('#questLog [data-role="progress-label"]').isVisible())
  ) {
    throw new Error('Quests did not show four missions without a tier label');
  }
  await shot('05-four-missions');
  await page.goto(`${base}/settings`);
  page.once('dialog', dialog => dialog.accept());
  await page.locator('#resetGettingStartedBtn').click();
  await page.locator('#resetGettingStartedStatus').getByText('Getting Started reset').waitFor();
  await page.goto(`${base}/?panel=today`);
  await page.locator('#personalAssistantFolderChooser').waitFor({ state: 'visible' });
  await shot('06-reset-rearms-prompt');
  // Hold the real request briefly so the visual intake can be captured while
  // the server is genuinely still scanning. No response body is fabricated.
  await page.route(
    '**/api/personal-assistant/folder-digest/scan',
    async route => {
      await new Promise(resolve => setTimeout(resolve, 1500));
      await route.continue();
    },
    { times: 1 }
  );
  await page.locator('#personalAssistantFolderChips button[data-chip="documents"]').click();
  await page.locator('#personalAssistantFolderScene[data-phase="scanning"]').waitFor();
  await page.waitForTimeout(240);
  await shot('06a-avatar-digests-folder');
  await page.locator('#personalAssistantFolderOffer').waitFor({ state: 'visible' });
  await page.locator('#personalAssistantFolderOfferActions').getByText('Start with Thesis').click();
  await shot('07-project-confirm');
  let setupRequest;
  page.on('request', request => {
    if (request.url().includes('/folder-digest/offers/') && request.url().endsWith('/decide')) {
      const body = request.postDataJSON();
      if (body?.create === true) setupRequest = { url: request.url(), body };
    }
  });
  await page
    .locator('#personalAssistantFolderOfferActions')
    .getByText('Set up', { exact: true })
    .click();
  await page.locator('#personalAssistantFolderReceipt li').first().waitFor({ timeout: 30000 });
  console.log(
    'project receipt:',
    await page.locator('#personalAssistantFolderReceipt').innerText()
  );
  await shot('08-project-receipt');
  if (!setupRequest) throw new Error('The Set up request was not captured');
  const replay = await page.evaluate(async ({ url, body }) => {
    const response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    return { status: response.status, data: await response.json() };
  }, setupRequest);
  if (replay.status !== 200 || !replay.data?.offer?.outcome?.receipt?.length) {
    throw new Error(`The repeated Set up click lost its receipt: ${JSON.stringify(replay)}`);
  }
  console.log('Repeated request preserved receipt:', replay.data.offer.outcome.receipt[0].name);
  if (await page.locator('#personalAssistantFolderShowBtn').count()) {
    throw new Error('The redundant inline Explore a folder button returned');
  }
  await page.locator('#personalAssistantFolderChooser').waitFor({ state: 'visible' });
  await page.locator('#personalAssistantFolderChips button[data-chip="desktop"]').click();
  await page.locator('#personalAssistantFolderOffer').waitFor({ state: 'visible' });
  await page
    .locator('#personalAssistantFolderOfferActions')
    .getByText('Set up', { exact: true })
    .click();
  await page.locator('#personalAssistantFolderReceipt li').first().waitFor({ timeout: 30000 });
  console.log('corpus receipt:', await page.locator('#personalAssistantFolderReceipt').innerText());
  await shot('09-corpus-receipt');
  await page.reload();
  await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantDoneItems li').first().waitFor({ timeout: 30000 });
  console.log('Today results:', await page.locator('#personalAssistantDoneItems').innerText());
  await page.locator('#personalAssistantDoneItems').scrollIntoViewIfNeeded();
  await shot('10-seven-day-receipts');
  await page.setViewportSize({ width: 390, height: 844 });
  await shot('11-today-phone');
  await page.locator('#personalAssistantTodayMore > summary').click();
  await shot('11b-more-menu-phone');
  await page.locator('#personalAssistantTodayMore > summary').click();
  await page.setViewportSize({ width: 1280, height: 900 });
  // Force only one read to degrade, keeping the real server's other rows.
  await page.route('**/api/personal-assistant/today', async route => {
    const response = await route.fetch();
    const payload = await response.json();
    payload.data ??= {};
    const today = payload.today || payload.data.today;
    if (today) today.unavailable_sources = ['meetings'];
    await route.fulfill({ response, body: JSON.stringify(payload) });
  });
  await page.reload();
  await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantTodayFooter:not([hidden])').waitFor({ timeout: 30000 });
  console.log(
    'Today degraded footer:',
    await page.locator('#personalAssistantTodayFooter').innerText()
  );
  await page.locator('#personalAssistantTodayFooter').scrollIntoViewIfNeeded();
  await shot('12-today-degraded');
  await page.unroute('**/api/personal-assistant/today');
  await page.goto(`${base}/?quest=build-hq`);
  console.log(
    'Map quest after built:',
    await page.evaluate(() => window.OriPersonalHQQuest?.isActive())
  );
  if (errors.length) throw new Error(`page errors: ${errors.join('; ')}`);
} finally {
  await browser.close();
}
