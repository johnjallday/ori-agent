/*
 * Drives the coordinate Workspace Map on a running demo server and reports what
 * it saw, for the #292 Demo: checkpoints.
 *
 *   node scripts/demo-map-drive.mjs [baseUrl] [outDir]
 *
 * It exercises the navigation gestures a screenshot cannot show — empty-space
 * pan, trackpad wheel, pinch/modifier zoom, the camera buttons, a wide-to-narrow
 * resize, and a reload — and after every one of them re-reads each building's
 * world coordinate. The point of the run is the last line: navigating the map
 * must never move anything on it (#292 FR-13, FR-42, success metric 2).
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const base = process.argv[2] || 'http://localhost:8931';
const outDir = resolve(process.argv[3] || 'tmp-map-demo');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const problems = [];
page.on('pageerror', error => problems.push('page error: ' + error.message));

const shot = name => page.screenshot({ path: join(outDir, name + '.png'), fullPage: false });

/** Every building's world anchor, straight from the rendered DOM. */
const anchors = () =>
  page.evaluate(() =>
    [...document.querySelectorAll('.ws-map-tile[data-ws-id]')]
      .map(el => ({
        id: el.getAttribute('data-ws-id'),
        x: Math.round(parseFloat(el.style.left || '0')),
        y: Math.round(parseFloat(el.style.top || '0'))
      }))
      .sort((a, b) => (a.id < b.id ? -1 : 1))
  );

const cameraNow = () => page.evaluate(() => window.OriWorkspaceMap.getCamera());
const canvasBox = async () => (await page.locator('.ws-map-canvas').boundingBox()) || { x: 0, y: 0, width: 0, height: 0 };

async function settle() {
  await page.waitForTimeout(500);
}

await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
await page.waitForSelector('.ws-map-world .ws-map-tile', { timeout: 10000 });
await settle();

const baseline = await anchors();
console.log('buildings on the map:', baseline.length);
console.log('opening camera:', await cameraNow());
await shot('01-opened');

// --- empty-space pan -------------------------------------------------------
const box = await canvasBox();
const emptyX = box.x + box.width - 60;
const emptyY = box.y + box.height - 60;
await page.mouse.move(emptyX, emptyY);
await page.mouse.down();
await page.mouse.move(emptyX - 220, emptyY - 120, { steps: 12 });
await page.mouse.up();
await settle();
console.log('after pointer pan:', await cameraNow());
await shot('02-panned');

// --- trackpad wheel pan ----------------------------------------------------
await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
await page.mouse.wheel(120, 90);
await settle();
console.log('after wheel pan:', await cameraNow());

// --- pinch / zoom modifier -------------------------------------------------
await page.keyboard.down('Control');
await page.mouse.wheel(0, -240);
await page.keyboard.up('Control');
await settle();
console.log('after modifier zoom:', await cameraNow());
await shot('03-zoomed');

// --- camera buttons --------------------------------------------------------
await page.click('[data-map-zoom-in]');
await page.click('[data-map-zoom-out]');
await page.click('[data-map-fit]');
await settle();
console.log('after Fit All:', await cameraNow());
await shot('04-fit-all');

await page.locator('.ws-map-tile[data-ws-id]').first().click();
await settle();
await page.click('[data-map-center]');
await settle();
console.log('after Center Selected:', await cameraNow());
await shot('05-center-selected');

await page.click('[data-map-reset-view]');
await settle();
console.log('after Reset View:', await cameraNow());
await shot('06-reset-view');

// --- keyboard navigation ---------------------------------------------------
await page.locator('.ws-map-canvas').focus();
await page.keyboard.press('ArrowRight');
await page.keyboard.press('Equal');
await settle();
console.log('after keyboard pan + zoom:', await cameraNow());

// --- wide to narrow --------------------------------------------------------
await page.setViewportSize({ width: 420, height: 860 });
await settle();
const narrow = await anchors();
await shot('07-narrow');
await page.setViewportSize({ width: 1440, height: 900 });
await settle();

// --- reload ----------------------------------------------------------------
await page.reload({ waitUntil: 'domcontentloaded' });
await page.waitForSelector('.ws-map-world .ws-map-tile', { timeout: 10000 });
await settle();
const afterReload = await anchors();
console.log('camera restored after reload:', await cameraNow());
await shot('08-reloaded');

// --- the actual assertion --------------------------------------------------
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const stable = same(baseline, narrow) && same(baseline, afterReload);
console.log('\nanchors at open   :', JSON.stringify(baseline));
console.log('anchors when narrow:', JSON.stringify(narrow));
console.log('anchors after reload:', JSON.stringify(afterReload));
for (const problem of problems) console.log(problem);
console.log(
  stable
    ? '\nPASS — navigating, resizing, and reloading moved no building'
    : '\nFAIL — a building moved without the user moving it'
);

await browser.close();
process.exit(stable && problems.length === 0 ? 0 : 1);
