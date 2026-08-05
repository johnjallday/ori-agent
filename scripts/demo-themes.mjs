/*
 * Screenshots a page in both themes and at both widths, for the visual
 * before/after comparisons in the cozy-character-experience Demo: checkpoints.
 *
 *   node scripts/demo-themes.mjs <baseUrl> <outDir> <prefix> [path]
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, prefix, path = '/'] = process.argv.slice(2);
if (!baseUrl || !outDir || !prefix) {
  console.error('usage: node scripts/demo-themes.mjs <baseUrl> <outDir> <prefix> [path]');
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });

const VIEWS = [
  { name: 'wide', width: 1440, height: 950 },
  { name: 'narrow', width: 390, height: 844 }
];

const browser = await chromium.launch();
const problems = [];

try {
  for (const theme of ['light', 'dark']) {
    for (const view of VIEWS) {
      const page = await browser.newPage({
        viewport: { width: view.width, height: view.height }
      });
      page.on(
        'console',
        m => m.type() === 'error' && problems.push(`${theme}/${view.name}: ${m.text()}`)
      );

      await page.addInitScript(t => window.localStorage.setItem('ori-theme', t), theme);
      await page.route('**/api/onboarding/status', r =>
        r.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
        })
      );
      await page.goto(`${baseUrl}${path}`, { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(1600);

      await page.screenshot({
        path: join(resolve(outDir), `${prefix}-${theme}-${view.name}.png`),
        fullPage: false
      });

      // A page that scrolls sideways is broken however good it looks.
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth
      );
      if (overflow > 1) {
        problems.push(`${theme}/${view.name}: horizontal overflow of ${overflow}px`);
      }
      await page.close();
    }
  }
  console.log(problems.length ? `problems:\n  ${problems.join('\n  ')}` : 'no errors, no overflow');
} finally {
  await browser.close();
}
