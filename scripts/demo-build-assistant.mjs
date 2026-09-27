/*
 * Drives "Build with your assistant" in a running demo server and screenshots
 * each step, for the Demo: checkpoints in tasks/tasks-build-with-your-assistant.md.
 *
 *   node scripts/demo-build-assistant.mjs <baseUrl> <outDir> [--size WxH] [--path /]
 *        [--reduced-motion] <step> ...
 *
 * A bare <outDir> name (no slash) goes under the temp dir, $TMPDIR/build-demo/<name>,
 * so an invocation needs no shell variable in front of it.
 *
 * Steps run in order:
 *   open                 click Home's "New Workspace" and wait for the pane
 *   open:<selector>      click another opener instead
 *   say:<text>           type into the composer and press Enter, then wait
 *   chip:<label>         click the live chip whose label contains <label>
 *   click:<selector>     click any element
 *   rclick:<sel>[@x,y]   right-click an element, optionally at a point (the Map's menu)
 *   pane                 wait for the dialog and build pane an earlier step opened
 *   type:<selector>=<v>  replace an input's value (a user's own form edit)
 *   key:<key>            press a key on whatever has focus (keyboard-only walks);
 *                        key:Enter!  also waits for the turn it sends to settle
 *   focus                print the element that has focus
 *   style:<sel>=<prop>   print a computed style (e.g. animation-name)
 *   wait:<ms>            pause
 *   shot:<name>          screenshot the page to <outDir>/<name>.png
 *   close                close the dialog with Escape
 *
 * Prints console errors, failed requests, and every build-session request, so
 * a flow that renders but is quietly broken does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const args = process.argv.slice(2);
const baseUrl = args.shift();
let outDir = args.shift();
if (outDir && !outDir.includes('/')) outDir = join(tmpdir(), 'build-demo', outDir);
let size = '1440x900';
let path = '/';
let reducedMotion = 'no-preference';
while (args[0]?.startsWith('--')) {
  const flag = args.shift();
  if (flag === '--size') size = args.shift();
  else if (flag === '--path') path = args.shift();
  else if (flag === '--reduced-motion') reducedMotion = 'reduce';
}
if (!baseUrl || !outDir || args.length === 0) {
  console.error(
    'usage: node scripts/demo-build-assistant.mjs <baseUrl> <outDir> [--size WxH] [--path /] [--reduced-motion] <step> ...'
  );
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });
console.log(`screenshots: ${resolve(outDir)}`);
const [width, height] = size.split('x').map(Number);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width, height }, reducedMotion });
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
    } else if (kind === 'rclick') {
      // rclick:<selector>@x,y right-clicks at a point inside the element.
      const [selector, at] = value.split('@');
      const [x, y] = (at || '').split(',').map(Number);
      const options = { button: 'right' };
      if (Number.isFinite(x) && Number.isFinite(y)) options.position = { x, y };
      await page.click(selector, options);
      await page.waitForTimeout(500);
    } else if (kind === 'pane') {
      // After an opener other than "open" (a menu item, the Ask tab): wait for
      // the build pane and the first turn to settle.
      await page.waitForSelector('#addFolderModal.show', { timeout: 15000 });
      await page
        .waitForSelector('#workspaceBuildPane:not([hidden])', { timeout: 15000 })
        .catch(() => console.log('  (no build pane: manual wizard)'));
      await settle();
    } else if (kind === 'type') {
      const [selector, text] = value.split('=');
      await page.fill(selector, text || '');
      await page.waitForTimeout(900);
    } else if (kind === 'key') {
      const waitForTurn = value.endsWith('!');
      await page.keyboard.press(waitForTurn ? value.slice(0, -1) : value);
      if (waitForTurn) await settle();
      else await page.waitForTimeout(250);
    } else if (kind === 'focus') {
      const focused = await page.evaluate(() => {
        const el = document.activeElement;
        if (!el || el === document.body) return '(body)';
        const label = (el.getAttribute('aria-label') || el.innerText || el.value || '')
          .trim()
          .slice(0, 50);
        const id = el.id ? `#${el.id}` : '';
        const cls = el.classList.length ? `.${[...el.classList].slice(0, 2).join('.')}` : '';
        return `${el.tagName.toLowerCase()}${id}${cls} "${label}"`;
      });
      console.log(`  focus: ${focused}`);
    } else if (kind === 'style') {
      const [selector, prop] = value.split('=');
      const values = await page.$$eval(
        selector,
        (els, name) => els.map(el => getComputedStyle(el).getPropertyValue(name)),
        prop
      );
      console.log(`  ${selector} ${prop}: ${[...new Set(values)].join(' | ') || '(no match)'}`);
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
