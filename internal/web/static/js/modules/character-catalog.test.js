// Tests for character-catalog.js — the browser's read-only catalog view
// (cozy-character-experience FR-14, FR-19, FR-52, FR-71, FR-74, FR-101, FR-120).
//   node --test internal/web/static/js/modules/character-catalog.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./character-catalog.js', import.meta.url), 'utf8');

// Arrays created inside the vm realm carry that realm's Array.prototype, which
// assert/strict treats as a mismatch. Copy them into this realm before
// comparing so the assertions test values rather than provenance.
const ids = list => [...list].map(c => c.id);

function load({ fetchImpl, matchMedia } = {}) {
  const sandbox = {
    window: { matchMedia },
    console: { warn() {}, debug() {} },
    fetch: fetchImpl,
    Object,
    Number,
    String,
    Array,
    Promise,
    Error
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.window.CharacterCatalog;
}

const payload = {
  catalog_version: '1.0.0',
  reserved_guide_id: 'ori-guide',
  guide: {
    id: 'ori-guide',
    kind: 'guide',
    name: 'Ori',
    entry_version: 1,
    assets: {
      portrait: '/characters/ori-guide/portrait.svg',
      sprite: '/characters/ori-guide/sprite.svg',
      static: '/characters/ori-guide/static.svg'
    },
    palette: { base: '#bd7c24', accent: '#bd7c24', ink: '#2a1a06' }
  },
  characters: [
    {
      id: 'research-archivist',
      kind: 'working',
      name: 'Research Archivist',
      entry_version: 1,
      family: 'resident',
      family_label: 'Resident',
      assets: {
        portrait: '/characters/research-archivist/portrait.svg',
        sprite: '/characters/research-archivist/sprite.svg',
        static: '/characters/research-archivist/static.svg'
      },
      palette: { base: '#4f744a', accent: '#4f744a', ink: '#0f1a0e' }
    }
  ]
};

function ready(extra) {
  const cat = load();
  cat._ingest(extra || payload);
  return cat;
}

/* ---- lookup ---------------------------------------------------------------- */

test('a known character resolves with its visual assets', () => {
  const archivist = ready().get('research-archivist');
  assert.equal(archivist.name, 'Research Archivist');
  assert.equal(archivist.familyLabel, 'Resident');
  assert.equal(archivist.assets.portrait, '/characters/research-archivist/portrait.svg');
  // Nothing tone-shaped comes back, because the catalog no longer serves it
  // and this module has no field to put it in (FR-22).
  assert.equal(archivist.toneTraits, undefined);
  assert.equal(archivist.sampleLine, undefined);
});

test('an unknown or withdrawn character resolves to null so the caller falls back', () => {
  const cat = ready();
  for (const id of ['nope', '', null, undefined, '   ', 'Research Archivist']) {
    assert.equal(cat.get(id), null, `expected null for ${JSON.stringify(id)}`);
  }
});

test('lookups before the catalog loads return null rather than throwing', () => {
  const cat = load();
  assert.equal(cat.status(), 'idle');
  assert.equal(cat.get('research-archivist'), null);
});

/* ---- the reserved guide identity (FR-19/FR-71) ------------------------------ */

test('the guide is not offered among the assignable characters', () => {
  const cat = ready();
  const list = ids(cat.working());
  assert.ok(!list.includes('ori-guide'));
  assert.deepEqual(list, ['research-archivist']);
});

test('the guide identity is reachable on its own, and reported as reserved', () => {
  const cat = ready();
  assert.equal(cat.guide().name, 'Ori');
  assert.ok(cat.isReserved('ori-guide'));
  assert.ok(!cat.isReserved('research-archivist'));
  assert.ok(!cat.isReserved(''));
});

/* ---- reduced motion (FR-120) ------------------------------------------------ */

test('a reduced-motion viewer gets the static sprite, not a frozen animation', () => {
  const cat = ready();
  const archivist = cat.get('research-archivist');
  assert.equal(
    cat.assetFor(archivist, 'sprite', false),
    '/characters/research-archivist/sprite.svg'
  );
  assert.equal(
    cat.assetFor(archivist, 'sprite', true),
    '/characters/research-archivist/static.svg'
  );
});

test('portraits need no motion variant', () => {
  const cat = ready();
  const archivist = cat.get('research-archivist');
  assert.equal(
    cat.assetFor(archivist, 'portrait', true),
    '/characters/research-archivist/portrait.svg'
  );
  assert.equal(
    cat.assetFor(archivist, 'portrait', false),
    '/characters/research-archivist/portrait.svg'
  );
});

test('assetFor degrades safely for a missing character or variant', () => {
  const cat = ready();
  assert.equal(cat.assetFor(null, 'portrait', false), '');
  assert.equal(cat.assetFor({}, 'portrait', false), '');
  assert.equal(
    cat.assetFor(cat.get('research-archivist'), 'unknown-variant', false),
    '/characters/research-archivist/portrait.svg'
  );
});

test('reduced-motion detection survives an environment without matchMedia', () => {
  assert.equal(load().prefersReducedMotion(), false);
  assert.equal(load({ matchMedia: () => ({ matches: true }) }).prefersReducedMotion(), true);
  assert.equal(
    load({
      matchMedia: () => {
        throw new Error('nope');
      }
    }).prefersReducedMotion(),
    false
  );
});

/* ---- resilience (FR-14/FR-101/FR-124) --------------------------------------- */

test('a failed catalog request resolves rather than rejecting', async () => {
  const cat = load({ fetchImpl: () => Promise.resolve({ ok: false, status: 500 }) });
  await cat.load();
  // The page must still render: every agent simply uses its fallback identity.
  assert.equal(cat.status(), 'error');
  assert.equal(cat.get('research-archivist'), null);
  assert.equal(cat.working().length, 0);
});

test('a network error resolves rather than rejecting', async () => {
  const cat = load({ fetchImpl: () => Promise.reject(new Error('offline')) });
  await cat.load();
  assert.equal(cat.status(), 'error');
});

test('the catalog is fetched once however many surfaces ask for it', async () => {
  let calls = 0;
  const cat = load({
    fetchImpl: () => {
      calls++;
      return Promise.resolve({ ok: true, json: () => Promise.resolve(payload) });
    }
  });
  await Promise.all([cat.load(), cat.load(), cat.load()]);
  assert.equal(calls, 1);
  assert.equal(cat.status(), 'ready');
  assert.equal(cat.get('research-archivist').name, 'Research Archivist');
});

test('subscribers are notified once the catalog arrives so portraits can fill in', async () => {
  let notified = 0;
  const cat = load({
    fetchImpl: () => Promise.resolve({ ok: true, json: () => Promise.resolve(payload) })
  });
  cat.onChange(() => notified++);
  await cat.load();
  assert.equal(notified, 1);
});

test('a failing subscriber does not stop the others', async () => {
  const seen = [];
  const cat = load({
    fetchImpl: () => Promise.resolve({ ok: true, json: () => Promise.resolve(payload) })
  });
  cat.onChange(() => {
    throw new Error('bad subscriber');
  });
  cat.onChange(() => seen.push('second'));
  await cat.load();
  assert.deepEqual(seen, ['second']);
});

test('unsubscribing stops further notifications', async () => {
  let notified = 0;
  const cat = load({
    fetchImpl: () => Promise.resolve({ ok: true, json: () => Promise.resolve(payload) })
  });
  const off = cat.onChange(() => notified++);
  off();
  await cat.load();
  assert.equal(notified, 0);
});

/* ---- malformed data ---------------------------------------------------------- */

test('a malformed entry is dropped rather than half-rendered', () => {
  const cat = load();
  cat._ingest({
    catalog_version: '1.0.0',
    reserved_guide_id: 'ori-guide',
    characters: [
      null,
      { name: 'No ID' },
      { id: '   ' },
      { id: 'good', name: 'Good', assets: { portrait: '/characters/good/portrait.svg' } }
    ]
  });
  assert.deepEqual(ids(cat.working()), ['good']);
});

test('an entry missing optional fields still resolves with safe defaults', () => {
  const cat = load();
  cat._ingest({ characters: [{ id: 'bare' }] });
  const bare = cat.get('bare');
  assert.equal(bare.name, 'bare'); // falls back to the id rather than blank
  assert.equal(bare.assets.portrait, '');
});

test('an empty payload leaves the catalog usable and empty', () => {
  const cat = load();
  cat._ingest({});
  assert.equal(cat.working().length, 0);
  assert.equal(cat.guide(), null);
  assert.equal(cat.get('research-archivist'), null);
});
