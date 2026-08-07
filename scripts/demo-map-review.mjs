/*
 * Captures the review screenshots for #292 from a running demo server.
 *
 *   node scripts/demo-map-review.mjs [baseUrl] [outDir]
 *
 * Expects ./scripts/demo-seed-map.sh to have run first. Drives the real
 * surfaces — wide Home, a district, Build mode, a save failure, Reset/Undo, and
 * the narrow layout — so the PR shows what the feature actually looks like
 * rather than a static first paint.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const base = process.argv[2] || 'http://localhost:8931';
const outDir = resolve(process.argv[3] || 'tmp-map-review');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const shot = name => page.screenshot({ path: join(outDir, name + '.png') });
const api = path => base + path;

async function open() {
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-world .ws-map-tile', { timeout: 15000 });
  await page.waitForTimeout(900);
}

// Put one workspace inside the group so a real district is drawn.
const folders = (await (await page.request.get(api('/api/workspaces'))).json()).folders || [];
const group = folders.find(f => f.kind === 'group');
const child = folders.find(f => f.name === 'Delta Yard');
if (group && child) {
  await page.request.put(api(`/api/workspaces/${child.id}`), { data: { parent_id: group.id } });
  await page.request.patch(api('/api/workspace-map/layout'), {
    data: {
      operations: [
        {
          op: 'set_positions',
          positions: { [group.id]: { x: 76, y: 456 }, [child.id]: { x: 152, y: 532 } }
        }
      ]
    }
  });
}

await open();
// Fit all lives on the empty-ground context menu since #317; Shift+F10 on the
// focused canvas is the keyboard route to it.
await page.locator('[data-ws-map-viewport]').focus();
await page.keyboard.press('Shift+F10');
await page.locator('[data-menu-action="fit"]').click();
await page.waitForTimeout(400);
await shot('01-home-wide');

// Build: right-click empty ground and the menu's Build item takes that point
// (#317 retired the Build mode this used to drive).
const canvas = await page.locator('.ws-map-canvas').boundingBox();
await page.mouse.click(canvas.x + canvas.width * 0.7, canvas.y + canvas.height * 0.3, {
  button: 'right'
});
await page.waitForTimeout(300);
await shot('02-build-menu');
await page.keyboard.press('Escape');

// The district and its handle.
if (group) {
  const handle = page.locator(`[data-group-drag="${group.id}"]`);
  if (await handle.count()) {
    const box = await handle.boundingBox();
    if (box) {
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      await page.mouse.down();
      await page.mouse.move(box.x + box.width / 2 + 152, box.y + box.height / 2 + 38, {
        steps: 12
      });
      await shot('03-district-moving');
      await page.mouse.up();
      await page.waitForTimeout(800);
    }
  }
}

// A save failure: the building goes back and says so.
await page.route('**/api/workspace-map/layout', async route => {
  if (route.request().method() === 'PATCH') {
    await route.fulfill({ status: 500, contentType: 'application/json', body: '{}' });
    return;
  }
  await route.fallback();
});
const tile = page.locator('.ws-map-tile[data-ws-id]').first();
const tileBox = await tile.boundingBox();
if (tileBox) {
  await page.mouse.move(tileBox.x + tileBox.width / 2, tileBox.y + tileBox.height / 4);
  await page.mouse.down();
  await page.mouse.move(tileBox.x + tileBox.width / 2 + 120, tileBox.y + tileBox.height / 4 + 80, {
    steps: 10
  });
  await page.mouse.up();
  await page.waitForTimeout(900);
  await shot('04-save-failed');
}
await page.unroute('**/api/workspace-map/layout');

// Help.
await page.click('[data-map-help]');
await page.waitForTimeout(300);
await shot('05-help');
await page.click('[data-map-help]');

// Reset Layout, then Undo.
page.once('dialog', dialog => dialog.accept());
await page.click('[data-map-reset-layout]');
await page.waitForTimeout(900);
await shot('06-after-reset');
await page.click('[data-map-undo-reset]');
await page.waitForTimeout(900);
await shot('07-after-undo');

// Narrow / touch layout, same coordinates.
await page.setViewportSize({ width: 390, height: 844 });
await page.waitForTimeout(600);
await shot('08-narrow');

console.log('review screenshots written to', outDir);
await browser.close();
