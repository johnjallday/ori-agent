/*
 * Measures and screenshots the Issue #372 Workspace Map surfaces from a running
 * demo server.
 *
 *   node scripts/demo-372.mjs [baseUrl] [outDir]
 *
 * Expects ./scripts/demo-seed-map.sh to have run against the same server first.
 *
 * It exists because both halves of #372 are claims about measurements rather
 * than about pixels anyone can eyeball from a single screenshot:
 *
 *   #365 — the standalone /workspaces theatre must GROW with the viewport, so
 *          the evidence is the same box measured at two window heights, plus
 *          the Home cockpit measured alongside to prove it did not move.
 *   #367 — the ordinary in-canvas create pad must be GONE from populated maps
 *          while the topbar/Home actions survive, so the evidence is a count of
 *          each affordance rather than "it looks right".
 *
 * World anchors are printed with the measurements: a resize may change what is
 * visible, and must never change where anything is (#292 FR-13/FR-31).
 *
 * Prints a JSON report and writes screenshots. Exits non-zero only if the page
 * failed to render at all — the assertions belong to the reader (and to the
 * Playwright spec), not to a demo helper.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const base = process.argv[2] || 'http://localhost:8947';
const outDir = resolve(process.argv[3] || 'tmp-372');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 800 } });
const shot = name => page.screenshot({ path: join(outDir, name + '.png') });

/** The map's own measurements, read from the live DOM. */
async function measure() {
  return page.evaluate(() => {
    const box = selector => {
      const node = document.querySelector(selector);
      if (!node) return null;
      const rect = node.getBoundingClientRect();
      return {
        top: Math.round(rect.top),
        height: Math.round(rect.height),
        width: Math.round(rect.width)
      };
    };
    // Inline left/top on a tile IS its world anchor: the camera transform lives
    // on the world layer, so these must be identical before and after a resize.
    const anchors = {};
    document.querySelectorAll('.ws-map-world [data-ws-id]').forEach(node => {
      anchors[node.getAttribute('data-ws-id')] = node.style.left + ',' + node.style.top;
    });
    const doc = document.documentElement;
    return {
      viewport: { width: window.innerWidth, height: window.innerHeight },
      theatre: box('.ws-map-layout:not(.is-cockpit) .ws-map-theatre'),
      cockpitTheatre: box('.ws-map-layout.is-cockpit .ws-map-theatre'),
      canvas: box('.ws-map-canvas'),
      layoutColumns: getComputedStyle(document.querySelector('.ws-map-layout') || doc)
        .gridTemplateColumns,
      // Affordance census for #367.
      ordinaryPads: document.querySelectorAll('.ws-map-pad:not(.ws-map-pad--hero)').length,
      heroPads: document.querySelectorAll('.ws-map-pad--hero').length,
      topbarCreate: document.querySelectorAll('.ws-map-create').length,
      // The create paths that must SURVIVE the pad removal. A count of zero
      // here would mean the page has no way to make a workspace at all.
      createOutsideMap: [...document.querySelectorAll('button, a')]
        .filter(el => /new workspace|create workspace/i.test(el.textContent || ''))
        .filter(el => !el.closest('.ws-map') && (el.offsetWidth || el.offsetHeight))
        .map(el => (el.textContent || '').trim().slice(0, 30)),
      tiles: document.querySelectorAll('.ws-map-world .ws-map-tile').length,
      districts: document.querySelectorAll('.ws-map-world .ws-map-district').length,
      anchors,
      horizontalOverflow: doc.scrollWidth > doc.clientWidth + 1
    };
  });
}

async function openMap(path) {
  await page.goto(base + path, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-world .ws-map-tile', { timeout: 20000 });
  await page.waitForTimeout(900);
}

const report = {};

// ---- standalone /workspaces at two heights, WITHOUT reloading between them:
// the resize path under test is the live ResizeObserver, not a fresh render.
await openMap('/workspaces');
report.standaloneShort = await measure();
await shot('01-standalone-short-800');

await page.setViewportSize({ width: 1440, height: 1200 });
await page.waitForTimeout(700);
report.standaloneTall = await measure();
await shot('02-standalone-tall-1200');

// ---- narrow: the layout must stack and stay free of horizontal overflow.
await page.setViewportSize({ width: 720, height: 900 });
await page.waitForTimeout(700);
report.standaloneNarrow = await measure();
await shot('03-standalone-narrow-720');

// ---- Home cockpit: the control. Its map is sized by its ancestor chain and
// must not follow the standalone rule.
await page.setViewportSize({ width: 1440, height: 800 });
await openMap('/');
report.homeShort = await measure();
await shot('04-home-cockpit-800');

await page.setViewportSize({ width: 1440, height: 1200 });
await page.waitForTimeout(700);
report.homeTall = await measure();
await shot('05-home-cockpit-1200');

console.log(JSON.stringify(report, null, 2));
console.log('\nscreenshots written to', outDir);
await browser.close();
