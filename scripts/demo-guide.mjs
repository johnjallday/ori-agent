/*
 * Drives the Ori Guide panel on a running demo server and screenshots it, for
 * the Group 2/3 Demo: checkpoints.
 *
 *   node scripts/demo-guide.mjs <baseUrl> <outDir> [route] [width] [height]
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, route = '/', w = '1440', h = '900'] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error('usage: node scripts/demo-guide.mjs <baseUrl> <outDir> [route] [w] [h]');
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: Number(w), height: Number(h) } });
const problems = [];
page.on('console', m => m.type() === 'error' && problems.push(m.text()));
page.on('requestfailed', r => problems.push(`failed: ${r.url()}`));
page.on('response', r => r.status() >= 400 && problems.push(`HTTP ${r.status()}: ${r.url()}`));

const shot = name => page.screenshot({ path: join(resolve(outDir), `${name}.png`) });

try {
  // Home keeps long-lived connections open, so networkidle never fires here.
  await page.goto(`${baseUrl}${route}`, { waitUntil: 'domcontentloaded' });
  await page.locator('#oriGuideLauncher').waitFor({ state: 'visible', timeout: 15000 });
  await page.waitForTimeout(900);
  await shot('01-closed');

  await page.locator('#oriGuideLauncher').click();
  await page.waitForTimeout(600);
  await shot('02-open');

  await page.locator('#oriGuideInput').fill('what is a vault');
  await page.locator('#oriGuideSend').click();
  await page.waitForTimeout(700);
  await shot('03-explain');

  await page.locator('#oriGuideInput').fill('send an email to the whole team');
  await page.locator('#oriGuideSend').click();
  await page.waitForTimeout(700);
  await shot('04-work-handoff');

  // The handoff must fill the work surface without submitting it.
  const handoff = page.locator('.ori-guide__action', { hasText: 'Workspace Manager' });
  if (await handoff.count()) {
    await handoff.first().click();
    await page.waitForTimeout(600);
    const filled = await page.locator('#homeAssistantInput').inputValue();
    console.log(`work surface prefilled with: ${JSON.stringify(filled)}`);
    await shot('05-prefilled-not-sent');
  }

  await page.locator('#oriGuideLauncher').click();
  await page.locator('#oriGuideInput').fill('zzz nonsense question');
  await page.locator('#oriGuideSend').click();
  await page.waitForTimeout(600);
  await shot('06-unknown-topic');

  console.log(problems.length ? `problems:\n  ${problems.join('\n  ')}` : 'no console/network errors');
} finally {
  await browser.close();
}
