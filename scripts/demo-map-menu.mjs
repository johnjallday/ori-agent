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
// view; every stage below re-frames first.
//
// Fit all is a menu item since #317, and the map's own `f` shortcut never fires
// in the app — keyboard-navigation.js claims that key globally for its link
// hints and stops the event in the capture phase. Shift+F10 on the focused
// canvas opens the same menu and is the keyboard route that actually works.
const fitAll = async () => {
  await page.locator('[data-ws-map-viewport]').focus();
  await page.keyboard.press('Shift+F10');
  await page.locator('[data-menu-action="fit"]').click();
  await page.waitForTimeout(250);
  return camera();
};

// Center Selected is a menu item now too. Opened from the keyboard so it does
// not need an empty pixel to right-click.
const centerSelected = async () => {
  await page.locator('[data-ws-map-viewport]').focus();
  await page.keyboard.press('Shift+F10');
  const item = page.locator('[data-menu-action="center"]');
  if ((await item.count()) && (await item.getAttribute('aria-disabled')) !== 'true') {
    await item.click();
  } else {
    await page.keyboard.press('Escape');
  }
  await page.waitForTimeout(250);
};

// Reset view is the deterministic base: content centred at exactly 100%,
// whatever shape the fixture is in. (Fit all is content-dependent — it lands on
// 50% for a spread-out map and 200% for a compact one, which made every
// downstream coordinate in this run a guess.)
const resetView = async () => {
  await page.locator('[data-ws-map-viewport]').focus();
  await page.keyboard.press('0');
  await page.waitForTimeout(250);
};

const frame = async direction => {
  await resetView();
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

// A point on the first element matching `selector` that a click would actually
// reach. A district is large and its centre is usually covered by one of its own
// buildings, so several points inside it are tried before giving up.
async function visibleBox(selector) {
  const size = page.viewportSize();
  return page.evaluate(
    ({ sel, w, h }) => {
      const onScreen = (x, y) => x > 40 && x < w - 40 && y > 40 && y < h - 140;
      for (const el of document.querySelectorAll(sel)) {
        const box = el.getBoundingClientRect();
        const candidates = [
          [box.left + box.width / 2, box.top + box.height / 2],
          [box.left + 8, box.top + 8],
          [box.right - 8, box.top + 8],
          [box.left + 8, box.bottom - 8],
          [box.right - 8, box.bottom - 8],
          [box.left + box.width / 2, box.top + 6]
        ];
        for (const [x, y] of candidates) {
          if (!onScreen(x, y)) continue;
          const hit = document.elementFromPoint(x, y);
          if (hit && (el.contains(hit) || hit === el)) return { x, y };
        }
      }
      return null;
    },
    { sel: selector, w: size.width, h: size.height }
  );
}

// A point on the canvas with nothing on it: the hit test must land on the
// canvas or its world layer, not on a building, a district, or a control.
async function emptyCanvasPoint() {
  const size = page.viewportSize();
  return page.evaluate(
    ({ w, h }) => {
      for (let y = 120; y < h - 160; y += 40) {
        for (let x = 80; x < w - 500; x += 40) {
          const hit = document.elementFromPoint(x, y);
          if (!hit) continue;
          if (hit.classList.contains('ws-map-world') || hit.classList.contains('ws-map-canvas')) {
            return { x, y };
          }
        }
      }
      return null;
    },
    { w: size.width, h: size.height }
  );
}

// `optional` is for the sweep: on a spread-out map not every target is on
// screen at every zoom level, and that is a fact about the fixture, not a bug.
// Pan the camera by dragging empty ground, so a target that a zoom pushed
// off-screen can be brought back under the cursor. Returns how far the world
// actually moved; the drag is clamped to the clear part of the canvas.
async function panBy(dx, dy) {
  const size = page.viewportSize();
  const start = await emptyCanvasPoint();
  if (!start) return { dx: 0, dy: 0 };
  const endX = Math.max(60, Math.min(start.x + dx, size.width - 480));
  const endY = Math.max(120, Math.min(start.y + dy, size.height - 200));
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  await page.mouse.move(endX, endY, { steps: 10 });
  await page.mouse.up();
  await page.waitForTimeout(150);
  return { dx: endX - start.x, dy: endY - start.y };
}

// Drag the world until `selector` sits in the clear area, or give up. Used by
// the anchoring sweep: a target that is off-screen at 200% is a fact about the
// fixture, and panning to it is exactly what a user would do.
async function bringIntoView(selector) {
  const goal = { x: 400, y: 430 };
  for (let attempt = 0; attempt < 4; attempt += 1) {
    if (await visibleBox(selector)) return true;
    const at = await page.evaluate(sel => {
      const el = document.querySelector(sel);
      if (!el) return null;
      const box = el.getBoundingClientRect();
      return {
        x: box.left + Math.min(box.width, 176) / 2,
        y: box.top + Math.min(box.height, 150) / 2
      };
    }, selector);
    if (!at) return false;
    const moved = await panBy(goal.x - at.x, goal.y - at.y);
    if (moved.dx === 0 && moved.dy === 0) return false;
  }
  return !!(await visibleBox(selector));
}

async function openAtPoint(point, name, { optional = false } = {}) {
  if (!point) {
    if (!optional) problems.push('no point to right-click for ' + name);
    return null;
  }
  await page.mouse.click(point.x, point.y, { button: 'right' });
  await page.waitForTimeout(120);
  const menu = await menuState();
  if (menu) {
    menu.anchorDx = menu.left - point.x;
    menu.anchorDy = menu.top - point.y;
  }
  return { point, menu };
}

async function openOn(selector, name, options = {}) {
  const point = await visibleBox(selector);
  if (!point) {
    if (!options.optional) problems.push('no on-screen ' + name + ' to right-click');
    return null;
  }
  return openAtPoint({ x: Math.round(point.x), y: Math.round(point.y) }, name, options);
}

// Right-click the centre of a tile and report where the menu landed relative to
// the cursor. The menu is allowed to flip near a viewport edge; what it may
// never do is drift by an amount that tracks the zoom level.
async function openOnTile(name, { optional = false } = {}) {
  const box = await visibleTileBox();
  if (!box) {
    if (!optional) problems.push('no on-screen tile to right-click for ' + name);
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
  check(Math.abs(opening.zoom - 1) < 0.001, 'Reset view (the 0 key) lands at exactly 100%');
  await shot('01-map');

  // The control strip lost its three framing buttons (#317): they are menu
  // items now, and the keys are the direct route.
  const strip = await page.evaluate(() =>
    [...document.querySelectorAll('.ws-map-controls button, .ws-map-controls span')].map(el =>
      el.textContent.trim()
    )
  );
  console.log('  control strip: ' + JSON.stringify(strip));
  check(strip.length === 3, 'the strip is zoom out / readout / zoom in only');
  check(
    !strip.some(label => /Fit|Center|Reset/i.test(label)),
    'no framing buttons left on the strip'
  );
  const fitted = await fitAll();
  check(
    fitted.zoom !== opening.zoom,
    'Fit all still frames everything, from the keyboard: ' + fitted.zoom
  );
  await frame(null);

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
  await centerSelected();
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

  // --- keyboard only ------------------------------------------------------
  console.log('\n--- keyboard: Shift+F10, arrows, Enter, focus restore ---');
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-tile[data-ws-id]', { timeout: 15000 });
  await frame(null);

  // Focus a building the way a keyboard user reaches it, then open its menu.
  const focusedId = await page.evaluate(() => {
    const tile = document.querySelector('.ws-map-tile[data-ws-id]');
    tile.focus();
    return document.activeElement === tile ? tile.getAttribute('data-ws-id') : '';
  });
  check(!!focusedId, 'a building can take keyboard focus: ' + focusedId);

  await page.keyboard.press('Shift+F10');
  await page.waitForTimeout(150);
  const keyboardMenu = await menuState();
  check(!!keyboardMenu, 'Shift+F10 opens the menu');
  const activeIsItem = () =>
    page.evaluate(() => ({
      action: document.activeElement?.getAttribute('data-menu-action') || null,
      tabindex: document.activeElement?.getAttribute('tabindex') || null
    }));
  const firstFocus = await activeIsItem();
  console.log('  focus on open: ' + JSON.stringify(firstFocus));
  check(firstFocus.action === 'open', 'focus lands on the first item');
  check(firstFocus.tabindex === '0', 'and it is the tabbable one (roving tabindex)');
  await shot('07-keyboard-menu');

  await page.keyboard.press('ArrowDown');
  const second = await activeIsItem();
  check(second.action === 'open-backlog', 'ArrowDown moves to the next item');
  await page.keyboard.press('ArrowUp');
  await page.keyboard.press('ArrowUp');
  const wrapped = await activeIsItem();
  check(wrapped.action === 'delete', 'ArrowUp wraps to the last item: ' + wrapped.action);
  await page.keyboard.press('Home');
  check((await activeIsItem()).action === 'open', 'Home jumps to the first item');

  await page.keyboard.press('Escape');
  await page.waitForTimeout(120);
  check((await menuState()) === null, 'Escape closes the menu');
  const restored = await page.evaluate(
    () => document.activeElement?.getAttribute('data-ws-id') || ''
  );
  check(restored === focusedId, 'focus returned to the building: ' + restored);

  // Enter really runs the focused item — announced through the map's own live
  // region, with no second one anywhere on the page.
  await page.keyboard.press('Shift+F10');
  await page.waitForTimeout(120);
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  const beforeEnter = await activeIsItem();
  check(beforeEnter.action === 'toggle-selection', 'arrowed to Add to selection');
  await page.keyboard.press('Enter');
  await page.waitForTimeout(200);
  const announced = await page.evaluate(() => {
    const regions = document.querySelectorAll('[data-map-live]');
    return { count: regions.length, text: regions[0]?.textContent || '' };
  });
  console.log('  live region: ' + JSON.stringify(announced));
  check(announced.count === 1, 'exactly one live region, the map’s existing one');
  check(/selected/.test(announced.text), 'the action announced its result: ' + announced.text);
  await shot('08-keyboard-announced');

  // --- the other three targets ------------------------------------------
  console.log('\n--- district, HQ site, and empty canvas ---');
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-tile[data-ws-id]', { timeout: 15000 });
  await frame(null);

  await bringIntoView('.ws-map-district[data-group-id]');
  const district = await openOn('.ws-map-district[data-group-id]', 'group district');
  const districtLabels = (district?.menu?.items || []).map(item => item.label);
  console.log('  district: ' + JSON.stringify(districtLabels));
  check(
    JSON.stringify(districtLabels) === JSON.stringify(['Open group', 'Delete group']),
    'the district menu is Open group + Delete group'
  );
  await shot('09-district-menu');
  await page.keyboard.press('Escape');

  await bringIntoView('[data-hq-site]');
  const hq = await openOn('[data-hq-site]', 'HQ site');
  const hqLabels = (hq?.menu?.items || []).map(item => item.label);
  console.log('  HQ site: ' + JSON.stringify(hqLabels));
  check(hqLabels[0] === 'Build My HQ', 'the HQ menu leads with Build My HQ');
  check(hqLabels.includes('Import HQ'), 'and offers Import HQ');
  check(!hqLabels.includes('Clear broken HQ link'), 'no repair entry on a healthy site');
  await shot('10-hq-menu');
  await page.keyboard.press('Escape');

  const canvas = await openAtPoint(await emptyCanvasPoint(), 'empty canvas');
  const canvasItems = canvas?.menu?.items || [];
  console.log('  canvas: ' + JSON.stringify(canvasItems.map(item => item.label)));
  check(
    canvasItems.some(item => item.action === 'build'),
    'the canvas menu offers Build'
  );
  check(
    canvasItems.some(item => item.action === 'center' && item.disabled) ||
      (await page.evaluate(() => !!window.OriWorkspaceMap.getSelectedId())),
    'Center selected is disabled when nothing is selected'
  );
  await shot('11-canvas-menu');

  // Build takes the point that was right-clicked. Open the menu at a known spot,
  // read back the world coordinate the map is holding, and check it is the one
  // under the cursor rather than a default.
  const buildSpot = await emptyCanvasPoint();
  const expectedWorld = await page.evaluate(point => {
    const canvas = document.querySelector('.ws-map-canvas');
    const rect = canvas.getBoundingClientRect();
    const camera = window.OriWorkspaceMap.getCamera();
    const viewport = { width: canvas.clientWidth, height: canvas.clientHeight };
    const world = window.OriWorkspaceMap.camera.screenToWorld(
      { x: point.x - rect.left, y: point.y - rect.top },
      camera,
      viewport
    );
    return window.OriWorkspaceMap.snapPoint(world);
  }, buildSpot);

  await openAtPoint(buildSpot, 'build spot');
  const buildItem = await page.evaluate(
    () => document.querySelector('[data-menu-action="build"]')?.textContent.trim() || null
  );
  check(buildItem === 'Build', 'the canvas menu item reads Build: ' + buildItem);
  await page.click('[data-menu-action="build"]');
  await page.waitForTimeout(900);
  const modalOpen = await page.evaluate(
    () => !!document.querySelector('.modal.show, dialog[open], [data-create-workspace-open]')
  );
  check(modalOpen, 'Build opened the existing create flow');
  check(
    await page.evaluate(() => window.OriWorkspaceMap.hasPendingBuild()),
    'and is holding the coordinate for it'
  );
  console.log('  build point: ' + JSON.stringify(expectedWorld));
  await shot('12-create-modal');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(600);
  check(
    !(await page.evaluate(() => window.OriWorkspaceMap.hasPendingBuild())),
    'cancelling the modal forgets the coordinate'
  );

  // --- anchoring, every target, every zoom level --------------------------
  //
  // The tile checks above cover the case most likely to break; this sweep is
  // the one that proves the rule holds for all four targets, which is what the
  // "menu inside the world layer" mistake would violate.
  console.log('\n--- anchoring sweep: four targets x three zoom levels ---');
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-tile[data-ws-id]', { timeout: 15000 });
  // A wider window and the default (clustered) layout put all four targets on
  // screen at once, so the sweep measures anchoring rather than reachability.
  // Reset Layout moves buildings, never records, and this sandbox is discarded.
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.click('[data-map-reset-layout]', { force: true }).catch(() => {});
  await page.waitForTimeout(800);
  console.log(
    '  sites on the map: ' +
      JSON.stringify(
        await page.evaluate(() => ({
          tiles: document.querySelectorAll('.ws-map-tile[data-ws-id]').length,
          districts: document.querySelectorAll('.ws-map-district[data-group-id]').length,
          hq: document.querySelectorAll('[data-hq-site]').length
        }))
      )
  );

  const opt = { optional: true };
  const sweepTargets = [
    [
      'tile',
      async () => {
        await bringIntoView('.ws-map-tile[data-ws-id]');
        return openOnTile('sweep', opt);
      }
    ],
    [
      'district',
      async () => {
        await bringIntoView('.ws-map-district[data-group-id]');
        return openOn('.ws-map-district[data-group-id]', 'district', opt);
      }
    ],
    [
      'HQ site',
      async () => {
        await bringIntoView('[data-hq-site]');
        return openOn('[data-hq-site]', 'HQ site', opt);
      }
    ],
    ['canvas', async () => openAtPoint(await emptyCanvasPoint(), 'canvas', opt)]
  ];

  // Reset view is 100% by definition; zooming out from there clamps at 50%;
  // zooming in needs a selected building to centre on, or a spread-out map
  // leaves every target off-screen at 200%.
  const framings = {
    '100%': async () => {
      await resetView();
      return camera();
    },
    '50%': () => frame('zoom-out'),
    '200%': async () => {
      await frame(null);
      const box = await visibleTileBox();
      if (box) {
        await page.mouse.click(
          Math.round(box.x + box.width / 2),
          Math.round(box.y + box.height / 2)
        );
        await page.waitForTimeout(200);
      }
      for (let i = 0; i < 6; i += 1) {
        await page.click('[data-map-zoom-in]', { force: true }).catch(() => {});
      }
      await centerSelected();
      await page.waitForTimeout(300);
      return camera();
    }
  };

  for (const zoomName of ['100%', '50%', '200%']) {
    for (const [targetName, open] of sweepTargets) {
      const cam = await framings[zoomName]();
      const opened = await open();
      const label = targetName + ' at ' + zoomName + ' (zoom ' + Math.round(cam.zoom * 100) + '%)';
      if (!opened?.menu) {
        // Not every target is reachable at every framing on a spread-out map;
        // say so rather than silently passing.
        console.log('  skip   ' + label + ' — not on screen at this zoom');
        continue;
      }
      // Anchored means "at the cursor, or flipped back across it by exactly its
      // own width/height" — flipping near a viewport edge is the design, and it
      // is still anchored. Drift caused by the world transform would be some
      // other number entirely, and would scale with the zoom.
      const anchored = (delta, size) => Math.abs(delta) <= 2 || Math.abs(delta + size) <= 2;
      check(
        anchored(opened.menu.anchorDx, opened.menu.width) &&
          anchored(opened.menu.anchorDy, opened.menu.height),
        label +
          ' stays on the cursor (dx ' +
          opened.menu.anchorDx +
          ', dy ' +
          opened.menu.anchorDy +
          ')'
      );
      check(opened.menu.insideWorld === false, label + ' is outside the world layer');
      await page.keyboard.press('Escape');
    }
  }

  // --- the behaviour the menu sits next to --------------------------------
  console.log('\n--- regression: selection, checkboxes, and the rail ---');
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-tile[data-ws-id]', { timeout: 15000 });
  await frame(null);

  const leftClickBox = await visibleTileBox();
  await page.mouse.click(
    Math.round(leftClickBox.x + leftClickBox.width / 2),
    Math.round(leftClickBox.y + leftClickBox.height / 2)
  );
  await page.waitForTimeout(300);
  check(new URL(page.url()).pathname === '/', 'a left click still selects without navigating');
  check(
    await page.evaluate(() => !!window.OriWorkspaceMap.getSelectedId()),
    'and the selection reached the map'
  );
  check(
    await page.evaluate(() => !!document.querySelector('[data-cockpit-rail-open]')),
    'the rail still offers its own Open Workspace action'
  );

  const checkboxWorks = await page.evaluate(() => {
    const tile = document.querySelector('.ws-map-tile[data-ws-id]');
    const box = tile.querySelector('[data-ws-check]');
    box.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    const on = tile.classList.contains('is-multi');
    box.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    return on && !tile.classList.contains('is-multi');
  });
  check(checkboxWorks, 'the corner checkbox still toggles the bulk set both ways');

  // --- the checked set --------------------------------------------------
  //
  // Runs last, and mutates: it really groups workspaces on the demo server.
  // The sandbox is thrown away when the demo server stops.
  console.log('\n--- multi-select menu ---');
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.ws-map-tile[data-ws-id]', { timeout: 15000 });
  await frame(null);

  const checked = await page.evaluate(() => {
    // Start from an empty set: an earlier section in this run may have left a
    // building checked, and a count that is off by one would make every label
    // below look wrong for the wrong reason.
    const tiles = [...document.querySelectorAll('.ws-map-tile[data-ws-id]')];
    const click = el =>
      el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    tiles.forEach(tile => {
      const box = tile.querySelector('[data-ws-check]');
      if (box && tile.classList.contains('is-multi')) click(box);
    });
    const picked = [];
    for (const tile of tiles) {
      if (picked.length === 3) break;
      const box = tile.querySelector('[data-ws-check]');
      if (!box) continue;
      click(box);
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
  await shot('13-multi-menu');

  // Group really runs: the existing flow asks for a name through a prompt.
  page.on('dialog', dialog => dialog.accept('Demo Group'));
  await page.click('[data-menu-action="group-multi"]');
  await page.waitForTimeout(1500);
  const districts = await page.evaluate(
    () => document.querySelectorAll('.ws-map-district[data-group-id]').length
  );
  check(districts > 0, 'grouping produced a district on the map: ' + districts);
  await shot('14-after-group');
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
