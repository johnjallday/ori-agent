/*
 * Drives the Workspace Map's right-click context menu on a running demo server
 * and reports what it saw, for the #317 Demo: checkpoints.
 *
 *   node scripts/demo-map-menu.mjs [baseUrl] [outDir]
 *
 * Screenshots alone cannot show the thing this design is most at risk of
 * getting wrong: the menu is mounted outside the map's transformed world layer,
 * so it must stay anchored to the *cursor* at every zoom level and must not
 * drift when the canvas pans. This run opens the menu at 100%, 50% and 200%
 * zoom, checks the anchor each time, and then pans to confirm the menu closes
 * rather than sliding away from what it was opened on.
 *
 * Exits non-zero if anything it checked was wrong, so a broken demo cannot pass
 * as a clean one.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const base = process.argv[2] || 'http://localhost:8931';
const outDir = resolve(process.argv[3] || 'tmp-map-menu-demo');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const problems = [];
// Page-level noise is reported but does not fail the run on its own: Home logs
// a couple of pre-existing 404s (the chat web-search toggle) that have nothing
// to do with the map. A thrown page error is a different matter.
const noise = [];
page.on('pageerror', error => problems.push('page error: ' + error.message));
page.on('console', message => {
  if (message.type() === 'error') noise.push('console error: ' + message.text());
});

const shot = name => page.screenshot({ path: join(outDir, name + '.png'), fullPage: false });
const check = (ok, label) => {
  if (!ok) problems.push(label);
  console.log((ok ? '  ok   ' : '  FAIL ') + label);
};

const menuState = () =>
  page.evaluate(() => {
    const menu = document.querySelector('[data-ws-map-menu]');
    if (!menu) return null;
    const rect = menu.getBoundingClientRect();
    const host = menu.closest('[data-ws-map-menu-host]');
    return {
      left: Math.round(rect.left),
      top: Math.round(rect.top),
      width: Math.round(rect.width),
      height: Math.round(rect.height),
      role: menu.getAttribute('role'),
      label: menu.getAttribute('aria-label'),
      // The host must not live inside the pan/zoom transform, or the menu would
      // scale with the map and slide during a pan.
      insideWorld: !!(host && host.closest('.ws-map-world')),
      items: [...menu.querySelectorAll('[data-menu-action]')].map(item => ({
        action: item.getAttribute('data-menu-action'),
        label: item.textContent.trim(),
        disabled: item.getAttribute('aria-disabled') === 'true',
        role: item.getAttribute('role')
      }))
    };
  });

const camera = () => page.evaluate(() => window.OriWorkspaceMap.getCamera());

// The map persists its camera, so a run inherits wherever the last one left the
// view. Every stage starts from Fit All to make the framing deterministic, then
// zooms from there — zoom is applied around the viewport centre, so framed
// content stays framed.
const frame = async direction => {
  await page.click('[data-map-fit]', { force: true }).catch(() => {});
  await page.waitForTimeout(250);
  if (direction) {
    for (let i = 0; i < 6; i += 1) {
      await page.click('[data-map-' + direction + ']', { force: true }).catch(() => {});
    }
    await page.waitForTimeout(250);
  }
  return camera();
};

// The first tile that a click at its own centre would actually reach. Zooming
// moves buildings off-screen and Home's context rail overlays the right of the
// map, so a tile's coordinates are not proof the cursor can land on it —
// elementFromPoint is.
async function visibleTileBox() {
  const size = page.viewportSize();
  return page.evaluate(
    ({ w, h }) => {
      for (const tile of document.querySelectorAll('.ws-map-tile[data-ws-id]')) {
        const box = tile.getBoundingClientRect();
        const cx = box.left + box.width / 2;
        const cy = box.top + box.height / 2;
        // Keep clear of the floating control strip along the bottom.
        if (cx < 40 || cx > w - 40 || cy < 40 || cy > h - 140) continue;
        const hit = document.elementFromPoint(cx, cy);
        if (hit && tile.contains(hit)) {
          return { x: box.left, y: box.top, width: box.width, height: box.height };
        }
      }
      return null;
    },
    { w: size.width, h: size.height }
  );
}

// Right-click the centre of a tile and report where the menu landed relative to
// the cursor. The menu is allowed to flip near a viewport edge; what it may
// never do is drift by an amount that tracks the zoom level.
async function openOnTile(name) {
  const box = await visibleTileBox();
  if (!box) {
    problems.push('no on-screen tile to right-click for ' + name);
    return null;
  }
  const point = { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height / 2) };
  await page.mouse.click(point.x, point.y, { button: 'right' });
  await page.waitForTimeout(120);
  const menu = await menuState();
  if (menu) {
    menu.anchorDx = menu.left - point.x;
    menu.anchorDy = menu.top - point.y;
  }
  return { point, menu };
}

try {
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-tile[data-ws-id]', { timeout: 15000 });
  await page.waitForTimeout(500);
  const opening = await frame(null);
  console.log('framed at ' + Math.round(opening.zoom * 100) + '%');
  await shot('01-map');

  console.log('\n--- tile menu at 100% ---');
  const first = await openOnTile('100%');
  console.log(JSON.stringify(first?.menu, null, 2));
  check(!!first?.menu, 'right-click opens a menu');
  check(first?.menu?.role === 'menu', 'the container is role="menu"');
  check(
    (first?.menu?.items || []).every(item => item.role === 'menuitem'),
    'every entry is role="menuitem"'
  );
  check(first?.menu?.insideWorld === false, 'the menu is mounted outside the transformed world');
  check(Math.abs(first?.menu?.anchorDx ?? 999) <= 2, 'the menu opens at the cursor (x)');
  check(Math.abs(first?.menu?.anchorDy ?? 999) <= 2, 'the menu opens at the cursor (y)');
  check(
    (first?.menu?.items || []).some(item => item.action === 'delete'),
    'the tile menu offers Delete'
  );
  await shot('02-tile-menu');

  // The right-clicked tile becomes the selection, and nothing was opened.
  check(page.url().endsWith('/'), 'right-click did not navigate anywhere');
  const selected = await page.evaluate(() => window.OriWorkspaceMap.getSelectedId());
  check(!!selected, 'right-clicking an unselected tile selected it: ' + selected);

  console.log('\n--- dismissal ---');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(80);
  check((await menuState()) === null, 'Escape closes the menu');

  console.log('\n--- anchoring at 50% zoom ---');
  const zoomedOut = await frame('zoom-out');
  console.log('  zoom now ' + Math.round(zoomedOut.zoom * 100) + '%');
  const small = await openOnTile('50%');
  check(!!small?.menu, 'the menu still opens when zoomed out');
  check(Math.abs(small?.menu?.anchorDx ?? 999) <= 2, 'still anchored to the cursor at 50% (x)');
  check(Math.abs(small?.menu?.anchorDy ?? 999) <= 2, 'still anchored to the cursor at 50% (y)');
  check(
    small?.menu?.width === first?.menu?.width,
    'the menu did not scale with the zoom (' +
      small?.menu?.width +
      ' vs ' +
      first?.menu?.width +
      ')'
  );
  await shot('03-tile-menu-zoom-50');
  await page.keyboard.press('Escape');

  console.log('\n--- anchoring at 200% zoom ---');
  // Zooming in around the viewport centre can leave every building off-screen
  // on a spread-out map, so select one first and use Center Selected to bring it
  // back under the cursor.
  await frame(null);
  const toCenter = await visibleTileBox();
  if (toCenter) {
    await page.mouse.click(
      Math.round(toCenter.x + toCenter.width / 2),
      Math.round(toCenter.y + toCenter.height / 2)
    );
    await page.waitForTimeout(200);
  }
  for (let i = 0; i < 6; i += 1) {
    await page.click('[data-map-zoom-in]', { force: true }).catch(() => {});
  }
  await page.click('[data-map-center]', { force: true }).catch(() => {});
  await page.waitForTimeout(300);
  const zoomedIn = await camera();
  console.log('  zoom now ' + Math.round(zoomedIn.zoom * 100) + '%');
  const big = await openOnTile('200%');
  check(!!big?.menu, 'the menu still opens when zoomed in');
  check(Math.abs(big?.menu?.anchorDx ?? 999) <= 2, 'still anchored to the cursor at 200% (x)');
  check(Math.abs(big?.menu?.anchorDy ?? 999) <= 2, 'still anchored to the cursor at 200% (y)');
  check(big?.menu?.width === first?.menu?.width, 'the menu kept its size at 200%');
  await shot('04-tile-menu-zoom-200');

  console.log('\n--- panning while open ---');
  // Drag empty space far from any building: the camera moves, so a menu that is
  // still on screen would now be pointing at the wrong thing.
  await page.mouse.move(1200, 780);
  await page.mouse.down();
  await page.mouse.move(1000, 700, { steps: 8 });
  await page.mouse.up();
  await page.waitForTimeout(120);
  check((await menuState()) === null, 'a pan closes the menu instead of letting it drift');
  await shot('05-after-pan');

  console.log('\n--- Open runs the real action ---');
  await frame(null);
  const beforeOpen = await openOnTile('open');
  check(!!beforeOpen?.menu, 'menu open before choosing Open');
  await page.click('[data-menu-action="open"]');
  await page.waitForTimeout(800);
  check(/\/workspaces\//.test(page.url()), 'Open navigated to the workspace: ' + page.url());
  await shot('06-opened-workspace');

  // --- the checked set --------------------------------------------------
  //
  // Runs last, and mutates: it really groups workspaces on the demo server.
  // The sandbox is thrown away when the demo server stops.
  console.log('\n--- multi-select menu ---');
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-tile[data-ws-id]', { timeout: 15000 });
  await frame(null);

  const checked = await page.evaluate(() => {
    const picked = [];
    for (const tile of document.querySelectorAll('.ws-map-tile[data-ws-id]')) {
      if (picked.length === 3) break;
      const check = tile.querySelector('[data-ws-check]');
      if (!check) continue;
      check.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      picked.push(tile.getAttribute('data-ws-id'));
    }
    return picked;
  });
  await page.waitForTimeout(200);
  check(checked.length === 3, 'checked three workspaces: ' + checked.length);

  const bar = await page.evaluate(() => {
    const el = document.querySelector('[data-ws-selbar]');
    if (!el) return null;
    return {
      hidden: el.hidden,
      count: el.querySelector('[data-ws-selbar-count]')?.textContent,
      buttons: [...el.querySelectorAll('button')].map(b => b.textContent.trim())
    };
  });
  console.log('  selection bar: ' + JSON.stringify(bar));
  check(bar?.hidden === false, 'the selection bar is showing');
  check(bar?.count === '3 selected', 'it counts the checked set');
  check(
    JSON.stringify(bar?.buttons) === JSON.stringify(['Clear']),
    'and carries only Clear now: ' + JSON.stringify(bar?.buttons)
  );

  const bulk = await openOnTile('bulk');
  const bulkLabels = (bulk?.menu?.items || []).map(item => item.label);
  console.log('  menu: ' + JSON.stringify(bulkLabels));
  check(
    bulkLabels.includes('Delete 3 workspaces'),
    'the delete item names the blast radius before the click'
  );
  check(bulkLabels.includes('Group 3 workspaces'), 'so does the group item');
  await shot('07-multi-menu');

  // Group really runs: the existing flow asks for a name through a prompt.
  page.on('dialog', dialog => dialog.accept('Demo Group'));
  await page.click('[data-menu-action="group-multi"]');
  await page.waitForTimeout(1500);
  const districts = await page.evaluate(
    () => document.querySelectorAll('.ws-map-district[data-group-id]').length
  );
  check(districts > 0, 'grouping produced a district on the map: ' + districts);
  await shot('08-after-group');
} finally {
  console.log('\n--- page noise (not failures) ---');
  if (noise.length === 0) console.log('  none');
  noise.forEach(entry => console.log('  ' + entry));
  console.log('\n--- problems ---');
  if (problems.length === 0) console.log('  none');
  problems.forEach(problem => console.log('  ' + problem));
  await browser.close();
  process.exit(problems.length === 0 ? 0 : 1);
}
