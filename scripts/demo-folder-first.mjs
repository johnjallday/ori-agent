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
  await page.reload();
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
  await page.locator('#personalAssistantHQReceipt li').first().waitFor({ timeout: 30000 });
  console.log('receipt:', await page.locator('#personalAssistantHQReceipt').innerText());
  await shot('03-hq-receipt');
  await page.goto(`${base}/?quest=build-hq`);
  console.log(
    'Map quest after built:',
    await page.evaluate(() => window.OriPersonalHQQuest?.isActive())
  );
  if (errors.length) throw new Error(`page errors: ${errors.join('; ')}`);
} finally {
  await browser.close();
}
