/*
 * Drives "Build with your assistant" in a running demo server and screenshots
 * each step, for the Demo: checkpoints in tasks/tasks-build-with-your-assistant.md.
 *
 *   node scripts/demo-build-assistant.mjs <baseUrl> <outDir> [--size WxH] [--path /] <step> ...
 *
 * Steps run in order:
 *   open                 click Home's "New Workspace" and wait for the pane
 *   open:<selector>      click another opener instead
 *   say:<text>           type into the composer and press Enter, then wait
 *   chip:<label>         click the live chip whose label contains <label>
 *   click:<selector>     click any element
 *   type:<selector>=<v>  replace an input's value (a user's own form edit)
 *   wait:<ms>            pause
 *   shot:<name>          screenshot the page to <outDir>/<name>.png
 *   close                close the dialog with Escape
 *
 * Prints console errors, failed requests, and every build-session request, so
 * a flow that renders but is quietly broken does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const args = process.argv.slice(2);
const baseUrl = args.shift();
const outDir = args.shift();
let size = '1440x900';
let path = '/';
while (args[0]?.startsWith('--')) {
  const flag = args.shift();
  if (flag === '--size') size = args.shift();
  else if (flag === '--path') path = args.shift();
}
if (!baseUrl || !outDir || args.length === 0) {
  console.error(
    'usage: node scripts/demo-build-assistant.mjs <baseUrl> <outDir> [--size WxH] [--path /] <step> ...'
  );
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });
const [width, height] = size.split('x').map(Number);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width, height } });
const problems = [];
page.on('console', message => {
  if (message.type() === 'error') problems.push(`console: ${message.text()}`);
});
page.on('pageerror', error => problems.push(`page error: ${error.message}`));
page.on('requestfailed', request => problems.push(`request failed: ${request.url()}`));
page.on('response', response => {
  const url = response.url();
  if (url.includes('/build-sessions')) {
    console.log(
      `  ${response.request().method()} ${new URL(url).pathname} -> ${response.status()}`
    );
  }
  if (response.status() >= 400) problems.push(`HTTP ${response.status()}: ${url}`);
});

// A turn is done when the composer is enabled again.
async function settle() {
  await page.waitForTimeout(250);
  await page
    .waitForFunction(
      () => !document.getElementById('workspaceBuildComposerInput')?.disabled,
      null,
      {
        timeout: 90000
      }
    )
    .catch(() => problems.push('the pane stayed busy for 90s'));
  await page.waitForTimeout(900);
}

let exitCode = 0;
try {
  await page.goto(`${baseUrl}${path}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);
  for (const step of args) {
    const [kind, ...rest] = step.split(':');
    const value = rest.join(':');
    console.log(`step ${step}`);
    if (kind === 'open') {
      await page.click(value || '#cockpitCreateWorkspaceBtn');
      await page.waitForSelector('#addFolderModal.show', { timeout: 10000 });
      await page
        .waitForSelector('#workspaceBuildPane:not([hidden])', { timeout: 10000 })
        .catch(() => console.log('  (no build pane: manual wizard)'));
      await settle();
    } else if (kind === 'say') {
      await page.fill('#workspaceBuildComposerInput', value);
      await page.press('#workspaceBuildComposerInput', 'Enter');
      await settle();
    } else if (kind === 'chip') {
      const chip = page.locator('#workspaceBuildPane .workspace-build-chip:not([disabled])', {
        hasText: value
      });
      await chip.first().click();
      await settle();
    } else if (kind === 'click') {
      await page.click(value);
      await page.waitForTimeout(700);
    } else if (kind === 'type') {
      const [selector, text] = value.split('=');
      await page.fill(selector, text || '');
      await page.waitForTimeout(900);
    } else if (kind === 'wait') {
      await page.waitForTimeout(Number(value) || 500);
    } else if (kind === 'shot') {
      const file = join(resolve(outDir), `${value}.png`);
      await page.screenshot({ path: file });
      console.log(`  saved ${file}`);
    } else if (kind === 'close') {
      await page.keyboard.press('Escape');
      await page.waitForTimeout(800);
    } else {
      throw new Error(`unknown step ${step}`);
    }
  }
  const pane = await page.evaluate(() => {
    const root = document.getElementById('workspaceBuildPane');
    return root && !root.hidden
      ? Array.from(root.querySelectorAll('.workspace-build-entry')).map(entry =>
          entry.innerText.trim()
        )
      : null;
  });
  if (pane) {
    console.log('pane transcript:');
    for (const line of pane) console.log(`  | ${line.replace(/\n+/g, ' / ')}`);
  }
} catch (error) {
  exitCode = 1;
  console.error(`failed: ${error.message}`);
  await page.screenshot({ path: join(resolve(outDir), 'failure.png') }).catch(() => {});
} finally {
  if (problems.length) {
    console.log(`${problems.length} problem(s):`);
    for (const problem of [...new Set(problems)].slice(0, 20)) console.log(`  ${problem}`);
  }
  await browser.close();
}
process.exit(exitCode || (problems.length ? 2 : 0));
