import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-map.js', import.meta.url), 'utf8');
const buildingArt = readFileSync(new URL('./workspace-building-art.js', import.meta.url), 'utf8');
const parts = ['foundation', 'walls', 'roof', 'finish'];

function harness({ reduced = false } = {}) {
  let now = 0;
  let observer;
  const timers = new Set();
  const window = { addEventListener() {}, matchMedia: () => ({ matches: reduced }) };
  const sandbox = {
    window,
    document: { getElementById: () => null },
    fetch: () => new Promise(() => {}),
    Date: { now: () => now },
    setTimeout: (fn, ms) => {
      const timer = { fn, at: now + ms };
      timers.add(timer);
      return timer;
    },
    clearTimeout: timer => timers.delete(timer),
    MutationObserver: class {
      constructor(fn) {
        observer = fn;
      }
      observe() {}
      disconnect() {
        observer = null;
      }
    }
  };
  vm.runInNewContext(source, sandbox);
  const api = window.OriWorkspaceMap;
  let tiles = [];
  const container = {
    innerHTML: '',
    clientWidth: 1200,
    isConnected: true,
    querySelector: () => null,
    querySelectorAll: selector =>
      selector.includes('data-ws-id') || selector.includes('.ws-map-tile') ? tiles : []
  };
  api.mount(container, { workspaces: [], hideChrome: true, noAutoSelect: true });
  function tile(id, hasParts = true) {
    const classes = new Set();
    const properties = new Map();
    const art = {
      classList: {
        add: c => classes.add(c),
        remove: c => classes.delete(c),
        contains: c => classes.has(c)
      },
      style: {
        setProperty: (k, v) => properties.set(k, v),
        removeProperty: k => properties.delete(k)
      }
    };
    const el = {
      dataset: {},
      isConnected: true,
      art,
      properties,
      classList: { add() {}, remove() {} },
      getAttribute: name => (name === 'data-ws-id' ? id : null),
      querySelector: selector => (hasParts && selector.startsWith('.ws-map-struct') ? art : null)
    };
    tiles.push(el);
    return el;
  }
  return {
    api,
    tile,
    advance(ms) {
      now += ms;
      for (const timer of [...timers])
        if (timer.at <= now) {
          timers.delete(timer);
          timer.fn();
        }
    },
    replace(old, replacement) {
      old.isConnected = false;
      tiles = tiles.filter(t => t !== old);
      if (!tiles.includes(replacement)) tiles.push(replacement);
      observer?.();
    }
  };
}

test('generic house keeps the exact original geometry and paint order inside four part groups', () => {
  const window = {};
  vm.runInNewContext(source, { window, document: {} });
  const html = window.OriWorkspaceMap.tileHTML({
    ws: { id: 'ordinary', name: 'Ordinary' },
    col: 0,
    row: 0
  });
  const svg = html.match(/<svg class="ws-map-struct"[\s\S]*?<\/svg>/)[0];
  for (const part of parts) assert.equal(svg.split(`data-build-part="${part}"`).length, 2);
  const plain = svg.replace(/<g data-build-part="[^"]+">|<\/g>/g, '');
  // The pre-assembly cottage: literal snapshot, including shape/paint order.
  // Colours are palette-dependent; normalize only their values, not geometry.
  assert.equal(
    plain.replace(/(fill|stroke)="#[^"]+"/g, '$1="COLOR"'),
    '<svg class="ws-map-struct" width="112" height="92" viewBox="0 0 118 96" aria-hidden="true">' +
      '<polygon points="59,86 110,60 59,34 8,60" fill="COLOR" stroke="COLOR" stroke-opacity=".45"/>' +
      '<polygon points="34,40 34,54 59,68 59,54" fill="COLOR"/>' +
      '<polygon points="84,40 84,54 59,68 59,54" fill="COLOR"/>' +
      '<polygon points="34,40 59,54 59,16" fill="COLOR"/>' +
      '<polygon points="84,40 59,54 59,16" fill="COLOR"/>' +
      '<polyline points="34,40 59,54 84,40" fill="none" stroke="COLOR" stroke-opacity=".55" stroke-width="1.5"/>' +
      '<polygon points="42,48 50,52 50,63 42,59" fill="COLOR"/>' +
      '<polygon points="66,52 74,48 74,56 66,60" fill="COLOR" fill-opacity=".85"/>' +
      '<polygon points="70,25 76,28 76,36 70,33" fill="COLOR"/>' +
      '<polygon points="70,25 76,28 79,26 73,23" fill="COLOR"/></svg>'
  );
});

test('both HQ renderers declare the same four assembly parts', () => {
  const window = {};
  vm.runInNewContext(buildingArt, { window });
  vm.runInNewContext(source, { window, document: {} });
  const api = window.OriWorkspaceMap;
  api._setHQWorkspaceIdForTest('hq');
  const hq = api.tileHTML({ ws: { id: 'hq', name: 'HQ' }, col: 0, row: 0 });
  for (const part of parts) assert.match(hq, new RegExp(`data-build-part="${part}"`));
  for (const part of parts) assert.match(buildingArt, new RegExp(`data-build-part="${part}"`));
});

for (const id of ['generic', 'hq'])
  test(`${id} assembly settles at 1600ms`, async () => {
    const h = harness();
    h.api._setHQWorkspaceIdForTest('hq');
    const tile = h.tile(id);
    const motion = id === 'hq' ? h.api.playHQAssembly(id) : h.api.playAssembly(id);
    assert.equal(tile.dataset.buildReveal, 'playing');
    assert.equal(tile.art.classList.contains('is-assembling'), true);
    h.advance(1599);
    assert.equal(tile.dataset.buildReveal, 'playing');
    h.advance(1);
    await motion.done;
    assert.equal(tile.dataset.buildReveal, 'settled');
    assert.equal(tile.art.classList.contains('is-assembling'), false);
  });

test('HQ entry refuses other workspaces; missing tiles and art without parts have no assembly', () => {
  const h = harness();
  h.tile('plain', false);
  h.tile('generic');
  assert.equal(h.api.playAssembly('missing'), null);
  assert.equal(h.api.playAssembly('plain'), null);
  assert.equal(h.api.playHQAssembly('generic'), null);
});

test('reduced motion settles immediately', async () => {
  const h = harness({ reduced: true });
  const tile = h.tile('generic');
  await h.api.playAssembly('generic').done;
  assert.equal(tile.dataset.buildReveal, 'settled');
  assert.equal(tile.art.classList.contains('is-assembling'), false);
});

test('a halfway re-mount carries elapsed time, and skip resolves once on the replacement', async () => {
  const h = harness();
  const old = h.tile('generic');
  const motion = h.api.playAssembly('generic');
  h.advance(800);
  const next = h.tile('generic');
  h.replace(old, next);
  assert.equal(next.dataset.buildReveal, 'playing');
  assert.equal(next.properties.get('--build-assembly-offset'), '-800ms');
  motion.skip();
  motion.skip();
  await motion.done;
  assert.equal(next.dataset.buildReveal, 'settled');
  assert.equal(next.art.classList.contains('is-assembling'), false);
  h.advance(1600);
  assert.equal(next.dataset.buildReveal, 'settled');
});
