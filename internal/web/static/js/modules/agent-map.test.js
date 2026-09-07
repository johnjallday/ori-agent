// Tests for agent-map.js — the Agent Map's geometry
// (agents-page-ux FR-63, FR-66 through FR-69).
//
// A classic deferred script, so it is evaluated in a node:vm sandbox with a
// minimal window, mirroring agent-avatar.test.js. Only the pure geometry is
// exercised here: placement, camera, overlap and membership text are all
// functions of their inputs, which is what makes them assertable exactly rather
// than eyeballed in a browser.
//   node --test internal/web/static/js/modules/agent-map.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./agent-map.js', import.meta.url), 'utf8');

function load() {
  const sandbox = {
    window: { addEventListener() {} },
    document: { addEventListener() {} },
    Math,
    Object,
    Number,
    String,
    Array,
    JSON,
    isFinite,
    console
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.window.OriAgentMap._geometry;
}

const G = load();

// Points come back from the vm context, so they carry THAT realm's Object
// prototype and deepStrictEqual refuses them against a test-realm literal even
// when every field matches. Compare the numbers.
function assertPoint(actual, expected, message) {
  assert.ok(actual, message || 'expected a point');
  assert.equal(actual.x, expected.x, message);
  assert.equal(actual.y, expected.y, message);
}

function assertSamePlacements(actual, expected, message) {
  assert.deepEqual(Object.keys(actual).sort(), Object.keys(expected).sort(), message);
  Object.keys(actual).forEach(name => assertPoint(actual[name], expected[name], message));
}

/* ---- automatic placement (FR-68, FR-69) ---------------------------------- */

test('automatic placement is deterministic regardless of window size', () => {
  const names = ['Delta', 'Atlas', 'Cinder', 'Beacon'];
  // The function never consults a viewport, which is the property under test:
  // the same inputs must give the same coordinates every time.
  const first = G.resolvePlacements(names, {});
  const second = G.resolvePlacements(names, {});
  assertSamePlacements(first, second);

  // And it does not depend on the order the names arrive in, because it sorts.
  const shuffled = G.resolvePlacements(['Cinder', 'Beacon', 'Delta', 'Atlas'], {});
  assertSamePlacements(shuffled, first);
});

test('automatic placement never overlaps two tiles', () => {
  const names = [];
  for (let i = 0; i < 40; i++) names.push('Agent ' + String(i).padStart(2, '0'));
  const placements = G.resolvePlacements(names, {});

  const points = Object.values(placements);
  for (let i = 0; i < points.length; i++) {
    for (let j = i + 1; j < points.length; j++) {
      assert.ok(
        !G.footprintsOverlap(points[i], points[j]),
        `tiles ${i} and ${j} overlap at ${JSON.stringify(points[i])} / ${JSON.stringify(points[j])}`
      );
    }
  }
});

test('a saved anchor is kept, and automatic placement flows around it', () => {
  const saved = { Atlas: { x: 500, y: 500 } };
  const placements = G.resolvePlacements(['Atlas', 'Beacon', 'Cinder'], saved);

  assertPoint(placements.Atlas, { x: 500, y: 500 });
  for (const name of ['Beacon', 'Cinder']) {
    assert.ok(
      !G.footprintsOverlap(placements[name], placements.Atlas),
      `${name} was placed on top of the saved Atlas anchor`
    );
  }
});

test('a coordinate outside the safe world falls back to automatic placement', () => {
  const saved = {
    Atlas: { x: 9e9, y: 0 },
    Beacon: { x: NaN, y: 2 },
    Cinder: { x: 40, y: 40 }
  };
  const placements = G.resolvePlacements(['Atlas', 'Beacon', 'Cinder'], saved);

  assertPoint(placements.Cinder, { x: 40, y: 40 }, 'the usable anchor survives');
  assert.notEqual(placements.Atlas.x, 9e9);
  assert.ok(G.safePoint(placements.Atlas), 'the out-of-world anchor is replaced by a usable one');
  assert.ok(G.safePoint(placements.Beacon), 'the non-finite anchor is replaced by a usable one');
});

test('an agent with no saved anchor starts below existing content, not at the origin', () => {
  // Otherwise a new agent appears at a corner the user may have panned far away
  // from years ago.
  const saved = { Atlas: { x: 900, y: 900 } };
  const placements = G.resolvePlacements(['Atlas', 'Newcomer'], saved);
  assert.ok(
    placements.Newcomer.y > placements.Atlas.y,
    'a new agent should appear below the arranged content'
  );
});

/* ---- overlap (FR-69) ------------------------------------------------------ */

test('footprints that merely touch are not overlapping', () => {
  const a = { x: 0, y: 0 };
  assert.ok(!G.footprintsOverlap(a, { x: G.CELL_W, y: 0 }), 'exactly abutting is not an overlap');
  assert.ok(!G.footprintsOverlap(a, { x: 0, y: G.CELL_H }), 'exactly abutting is not an overlap');
  assert.ok(G.footprintsOverlap(a, { x: G.CELL_W - 1, y: 0 }), 'one unit of shared area overlaps');
  assert.ok(G.footprintsOverlap(a, { x: 0, y: G.CELL_H - 1 }), 'one unit of shared area overlaps');
});

test('wouldOverlap ignores the tile being moved', () => {
  const placements = { Atlas: { x: 0, y: 0 }, Beacon: { x: 400, y: 0 } };
  // Dropping Atlas exactly where Atlas already is is not a collision with
  // itself, or no tile could ever be released without moving.
  assert.ok(!G.wouldOverlap({ x: 0, y: 0 }, 'Atlas', placements));
  assert.ok(G.wouldOverlap({ x: 400, y: 0 }, 'Atlas', placements), 'landing on Beacon is blocked');
});

/* ---- camera (FR-66, FR-67) ------------------------------------------------ */

test('one zoom floor is applied to gestures and framing alike', () => {
  assert.equal(G.clampZoom(0.0001), G.MIN_ZOOM);
  assert.equal(G.clampZoom(99), G.MAX_ZOOM);
  assert.equal(G.clampZoom(NaN), 1, 'a non-number falls back to 100%, not to the floor');

  // A layout far too wide to show at 50% must still be framable, and the camera
  // framing produces must be one a gesture can leave.
  const wide = {};
  for (let i = 0; i < 60; i++) wide['A' + i] = { x: i * 4000, y: 0 };
  const camera = G.fitAllCamera(wide, { width: 900, height: 600 });
  assert.ok(camera.zoom >= G.MIN_ZOOM, 'framing never goes below the floor');
  assert.equal(G.clampZoom(camera.zoom), camera.zoom, 'framing lands on a storable zoom');
});

test('the camera is a look-at point plus zoom, not a scroll offset', () => {
  const viewport = { width: 800, height: 600 };
  const camera = { centerX: 100, centerY: 50, zoom: 2 };

  // The world point at the camera centre renders at the viewport centre,
  // whatever the container's size — which is what makes a saved camera portable
  // between containers.
  const screen = G.worldToScreen({ x: 100, y: 50 }, camera, viewport);
  assertPoint(screen, { x: 400, y: 300 });

  const bigger = { width: 1600, height: 1200 };
  const inBigger = G.worldToScreen({ x: 100, y: 50 }, camera, bigger);
  assertPoint(inBigger, { x: 800, y: 600 }, 'still the centre of a different container');
});

test('screenToWorld and worldToScreen are inverses', () => {
  const viewport = { width: 1024, height: 768 };
  const camera = { centerX: -30, centerY: 220, zoom: 0.75 };
  const world = { x: 512, y: -64 };

  const roundTripped = G.screenToWorld(G.worldToScreen(world, camera, viewport), camera, viewport);
  assert.ok(Math.abs(roundTripped.x - world.x) < 1e-9);
  assert.ok(Math.abs(roundTripped.y - world.y) < 1e-9);
});

test('zooming about a point keeps that point visually still', () => {
  const viewport = { width: 800, height: 600 };
  const camera = { centerX: 0, centerY: 0, zoom: 1 };
  const cursor = { x: 200, y: 150 };

  const before = G.screenToWorld(cursor, camera, viewport);
  const zoomed = G.zoomAroundPoint(camera, viewport, cursor, 1.5);
  const after = G.screenToWorld(cursor, zoomed, viewport);

  assert.ok(Math.abs(after.x - before.x) < 1e-9, 'the world point under the cursor must not move');
  assert.ok(Math.abs(after.y - before.y) < 1e-9);
});

test('zooming about the centre does not drift the camera', () => {
  const camera = { centerX: 42, centerY: -17, zoom: 1 };
  const zoomed = G.zoomAroundCenter(camera, 1.25);
  assert.equal(zoomed.centerX, 42);
  assert.equal(zoomed.centerY, -17);
  assert.equal(zoomed.zoom, 1.25);
});

test('fit-to-screen centres on the content', () => {
  const placements = { A: { x: 0, y: 0 }, B: { x: 400, y: 200 } };
  const bounds = G.contentBounds(placements);
  const camera = G.fitAllCamera(placements, { width: 1200, height: 800 });

  assert.equal(camera.centerX, bounds.minX + (bounds.maxX - bounds.minX) / 2);
  assert.equal(camera.centerY, bounds.minY + (bounds.maxY - bounds.minY) / 2);
});

test('fit-to-screen on an empty map is still a usable camera', () => {
  const camera = G.fitAllCamera({}, { width: 900, height: 600 });
  assert.ok(Number.isFinite(camera.centerX) && Number.isFinite(camera.centerY));
  assert.equal(G.clampZoom(camera.zoom), camera.zoom);
});

/* ---- membership text (FR-63) ---------------------------------------------- */

test('a tile names the entry-agent workspace first', () => {
  const label = G.membershipLabel({
    workspaces: [
      { name: 'Alpha', entry_point: false },
      { name: 'Beta', entry_point: true }
    ]
  });
  assert.equal(label, 'Beta · Alpha');
});

test('membership keeps the existing name-sorted order when nothing is an entry point', () => {
  const label = G.membershipLabel({
    workspaces: [
      { name: 'Alpha', entry_point: false },
      { name: 'Beta', entry_point: false }
    ]
  });
  assert.equal(label, 'Alpha · Beta');
});

test('membership shows at most two names plus an overflow count', () => {
  const label = G.membershipLabel({
    workspaces: [{ name: 'A' }, { name: 'B' }, { name: 'C' }, { name: 'D' }]
  });
  assert.equal(label, 'A · B · +2');
});

test('an agent in no workspace reads as Library only', () => {
  assert.equal(G.membershipLabel({ workspaces: [] }), 'Library only');
  assert.equal(G.membershipLabel({}), 'Library only');
  // A membership list of nameless refs is not a membership.
  assert.equal(G.membershipLabel({ workspaces: [{ name: '  ' }] }), 'Library only');
});

/* ---- safe bounds ---------------------------------------------------------- */

test('safePoint refuses anything unrenderable', () => {
  assertPoint(G.safePoint({ x: 1, y: 2 }), { x: 1, y: 2 });
  assert.equal(G.safePoint({ x: NaN, y: 0 }), null);
  assert.equal(G.safePoint({ x: Infinity, y: 0 }), null);
  assert.equal(G.safePoint({ x: 2000000, y: 0 }), null);
  assert.equal(G.safePoint({ x: 0, y: -2000000 }), null);
  assert.equal(G.safePoint(null), null);
  assert.equal(G.safePoint({ x: '10', y: 10 }), null, 'a string is not a coordinate');
});
