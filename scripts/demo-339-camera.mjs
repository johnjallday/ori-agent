/*
 * Drives the two Issue #339 acceptance paths on a running demo server and
 * reports what it saw, for the Demo: checkpoints.
 *
 *   node scripts/demo-339-camera.mjs [baseUrl] [outDir] [--fresh]
 *
 * Default run (#307): a map seeded wider than two viewports (see
 * `./scripts/smoke.sh wide`) is framed through the real canvas context menu,
 * then panned, zoomed, saved and reloaded — the whole path that used to stop
 * at the 50% floor.
 *
 * With --fresh (#329): a zero-workspace profile is opened at ?focus=personal-hq
 * and the reserved HQ landmark's rendered bounds are compared with the control
 * strip's, which is the occlusion the issue reports.
 *
 * Screenshots go through CDP rather than page.screenshot(): this app blocks on
 * Google Fonts, so `document.fonts.ready` never settles and Playwright's own
 * screenshot stabilization waits forever.
 */
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const args = process.argv.slice(2);
const fresh = args.includes('--fresh');
const positional = args.filter(arg => !arg.startsWith('--'));
const base = positional[0] || 'http://localhost:8931';
// Default under tmp/, which the repo already ignores, so a demo run never
// leaves untracked screenshots in `git status`.
const outDir = resolve(positional[1] || 'tmp/339-demo');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const problems = [];
page.on('pageerror', error => problems.push('page error: ' + error.message));

const cdp = await page.context().newCDPSession(page);
async function shot(name) {
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  const path = join(outDir, name + '.png');
  writeFileSync(path, Buffer.from(data, 'base64'));
  console.log('  shot:', path);
}

const camera = () => page.evaluate(() => window.OriWorkspaceMap.getCamera());
const announcement = () =>
  page.evaluate(() => {
    const live = document.querySelector('[data-map-live]');
    return live ? live.textContent.trim() : '(no live region)';
  });
const storedViewport = () =>
  page.evaluate(async () => {
    const response = await fetch('/api/workspace-map/layout');
    const body = await response.json();
    return (body.layout && body.layout.viewport) || null;
  });
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

// Every rendered building's on-screen box, plus the control strip's, so
// occlusion is measured rather than eyeballed.
const boxes = () =>
  page.evaluate(() => {
    const box = el => {
      const rect = el.getBoundingClientRect();
      return {
        left: Math.round(rect.left),
        top: Math.round(rect.top),
        right: Math.round(rect.right),
        bottom: Math.round(rect.bottom)
      };
    };
    const canvas = document.querySelector('.ws-map-canvas');
    const strip = document.querySelector('.ws-map-actions');
    return {
      canvas: canvas ? box(canvas) : null,
      strip: strip ? box(strip) : null,
      tiles: [...document.querySelectorAll('.ws-map-tile[data-ws-id]')].map(box),
      hq: (() => {
        const site = document.querySelector('[data-hq-site]');
        return site ? box(site) : null;
      })()
    };
  });

/**
 * A fresh sandbox opens on the onboarding wizard, which gates the workspace
 * list ("Finish setup to load your workspaces"). Choosing Set Up Later is what
 * a user does, and it persists, so later loads in this run are already clear.
 */
async function skipOnboarding() {
  const later = page.locator('#skipOnboardingLink');
  if (await later.isVisible().catch(() => false)) {
    await later.click();
    await page.waitForTimeout(800);
    console.log('  (chose Set Up Later)');
  }
}

/** Open the canvas context menu on empty space and run one of its actions. */
async function menuAction(action) {
  const canvas = await page.locator('.ws-map-canvas').boundingBox();
  // Bottom-right of the canvas is reliably empty space, and well clear of the
  // control strip along the bottom edge.
  await page.mouse.click(canvas.x + canvas.width - 80, canvas.y + 90, { button: 'right' });
  await page.locator(`[data-menu-action="${action}"]`).click();
  await page.waitForTimeout(300);
}

const problemsOrOK = () => (problems.length ? problems.join('; ') : 'none');

if (!fresh) {
  // --- #307: a wide layout, framed, used, saved and reopened ---------------
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  await skipOnboarding();
  await page.waitForSelector('.ws-map-world .ws-map-tile', { timeout: 15000 });
  await page.waitForTimeout(700);

  const baseline = await anchors();
  const short = rows => rows.map(row => ({ ...row, id: row.id.slice(0, 8) }));
  console.log('buildings on the map:', baseline.length, JSON.stringify(short(baseline)));
  console.log('opening camera:      ', await camera());
  await shot('01-opened');

  await menuAction('fit');
  const fitted = await camera();
  console.log('after Fit all:       ', fitted, '=>', Math.round(fitted.zoom * 100) + '%');
  console.log('announced:           ', await announcement());
  const framed = await boxes();
  const clearBottom = framed.strip ? framed.strip.top : framed.canvas.bottom;
  const offscreen = framed.tiles.filter(
    tile =>
      tile.left < framed.canvas.left ||
      tile.right > framed.canvas.right ||
      tile.top < framed.canvas.top ||
      tile.bottom > clearBottom
  );
  console.log('tiles rendered:      ', framed.tiles.length);
  console.log('tiles behind strip/off-screen:', offscreen.length);
  console.log('control strip top:   ', clearBottom, 'canvas:', JSON.stringify(framed.canvas));
  await shot('02-fitted');

  // A non-zoom camera action must not snap the fitted view back to 50%.
  const canvasBox = await page.locator('.ws-map-canvas').boundingBox();
  await page.mouse.move(canvasBox.x + canvasBox.width - 120, canvasBox.y + 120);
  await page.mouse.down();
  await page.mouse.move(canvasBox.x + canvasBox.width - 320, canvasBox.y + 180, { steps: 12 });
  await page.mouse.up();
  await page.waitForTimeout(200);
  const panned = await camera();
  console.log('after a pan:         ', panned);
  console.log(
    'anchors unchanged:   ',
    JSON.stringify(await anchors()) === JSON.stringify(baseline)
  );

  // Center Selected is the other non-zoom camera action, and the one most
  // likely to quietly re-clamp the zoom. Selected through the cockpit's own
  // API rather than by clicking: on a wide map at 26% the building this wants
  // is often outside the viewport, which is the very thing Center fixes.
  await page.evaluate(id => window.OriHomeCockpit.select(id), baseline[0].id);
  await page.waitForTimeout(200);
  await menuAction('center');
  const centered = await camera();
  console.log('after Center Selected:', centered);
  console.log(
    'zoom held through both non-zoom actions:',
    centered.zoom === panned.zoom ? 'yes' : 'NO'
  );

  const readout = await page.locator('[data-map-zoom-readout]').textContent();
  const zoomOutDisabled = await page.locator('[data-map-zoom-out]').isDisabled();
  console.log('readout:', readout.trim(), '| Zoom Out disabled:', zoomOutDisabled);

  await page.locator('[data-map-zoom-in]').click();
  await page.waitForTimeout(200);
  console.log('after Zoom In:       ', await camera());

  // A fitted view is not a dead end: the buttons share the framing floor, so
  // the user can keep zooming out by hand from wherever Fit All landed.
  await menuAction('fit');
  const beforeOut = await camera();
  if (!(await page.locator('[data-map-zoom-out]').isDisabled())) {
    await page.locator('[data-map-zoom-out]').click();
    await page.waitForTimeout(200);
    const out = await camera();
    console.log(
      'hand Zoom Out from fitted:',
      Math.round(beforeOut.zoom * 100) + '% ->',
      Math.round(out.zoom * 100) + '%'
    );
  } else {
    console.log('hand Zoom Out from fitted: already at the floor, button disabled');
  }

  await menuAction('fit');
  const refitted = await camera();
  await page.waitForTimeout(1200); // past the 600ms camera-save debounce
  const saved = await storedViewport();
  console.log('stored viewport:     ', saved);

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-world .ws-map-tile', { timeout: 15000 });
  await page.waitForTimeout(900);
  const restored = await camera();
  console.log('after reload:        ', restored, '=>', Math.round(restored.zoom * 100) + '%');
  console.log(
    'restored the fitted view:',
    Math.abs(restored.zoom - refitted.zoom) < 0.0002 ? 'yes' : 'NO'
  );
  await shot('03-reloaded');
  console.log('page errors:', problemsOrOK());
} else {
  // --- #329: a zero-workspace profile's reserved HQ landmark ---------------
  await page.goto(base + '/?focus=personal-hq', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  await skipOnboarding();
  await page.waitForSelector('[data-hq-site]', { timeout: 15000 });
  await page.waitForTimeout(900);

  console.log('workspaces on the map:', (await anchors()).length);
  console.log('opening camera:       ', await camera());
  const measured = await boxes();
  console.log('HQ landmark box:      ', JSON.stringify(measured.hq));
  console.log('control strip box:    ', JSON.stringify(measured.strip));
  const clearance = measured.strip && measured.hq ? measured.strip.top - measured.hq.bottom : null;
  console.log('clearance (strip.top - hq.bottom):', clearance, clearance > 0 ? 'CLEAR' : 'BEHIND');
  const captionText = await page.locator('[data-hq-site]').innerText();
  console.log('landmark reads:       ', JSON.stringify(captionText.replace(/\s+/g, ' ').trim()));
  await shot('10-fresh-hq');
  console.log('page errors:', problemsOrOK());
}

await browser.close();
