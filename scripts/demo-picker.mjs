/*
 * Drives the character picker on a running demo server and screenshots it.
 *
 *   node scripts/demo-picker.mjs <baseUrl> <outDir> [agentName]
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, agent = 'Field Notes'] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error('usage: node scripts/demo-picker.mjs <baseUrl> <outDir> [agentName]');
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 950 } });
const problems = [];
page.on('console', m => m.type() === 'error' && problems.push(m.text()));
page.on('response', r => r.status() >= 400 && problems.push(`HTTP ${r.status()}: ${r.url()}`));

const shot = name => page.screenshot({ path: join(resolve(outDir), `${name}.png`) });

try {
  await page.route('**/api/onboarding/status', r =>
    r.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );

  // --- Inspector: change an existing agent's identity ---
  await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(agent)}`, {
    waitUntil: 'domcontentloaded'
  });
  await page.locator('#ov-character-btn').waitFor({ state: 'visible', timeout: 15000 });
  await shot('01-identity-section');

  await page.locator('#ov-character-btn').click();
  await page.locator('#charPicker').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(600);
  await shot('02-picker-open');

  // Filter to one family, then pick a specific character.
  await page.locator('.char-picker__family[data-family="construct"]').click();
  await page.waitForTimeout(400);
  await shot('03-family-filter');

  await page.locator('.char-picker__family[data-family=""]').click();
  await page.locator('.char-card', { hasText: 'Decision Strategist' }).click();
  await page.locator('#charPickerVoice').check();
  await page.waitForTimeout(400);
  await shot('04-selected-with-voice');

  await page.locator('#charPickerConfirm').click();
  await page.waitForTimeout(1500);
  await shot('05-saved');

  const label = await page
    .locator('#stageCharacter')
    .innerText()
    .catch(() => '(none)');
  console.log(`hero character line: ${JSON.stringify(label)}`);

  // --- New Agent: choose during creation ---
  await page.locator('#newAgentBtn').click();
  await page.locator('#cr-character-btn').waitFor({ state: 'visible', timeout: 10000 });
  await shot('06-new-agent-panel');

  await page.locator('#cr-character-btn').click();
  await page.locator('#charPicker').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(500);
  await page.locator('#charPickerSkip').click();
  await page.waitForTimeout(400);
  const skipped = await page.locator('#cr-character-state').innerText();
  console.log(`after Skip: ${JSON.stringify(skipped)}`);
  await shot('07-after-skip');

  console.log(
    problems.length ? `problems:\n  ${[...new Set(problems)].join('\n  ')}` : 'no errors'
  );
} finally {
  await browser.close();
}
