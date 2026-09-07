// Tests for workspace-palette.js — the shared workspace colour key
// (agents-page-ux FR-24).
//
// A classic deferred script, so it is evaluated in a node:vm sandbox with a
// minimal window, mirroring agent-avatar.test.js.
//   node --test internal/web/static/js/modules/workspace-palette.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-palette.js', import.meta.url), 'utf8');

function load() {
  const sandbox = { window: {}, Math, Object, Number, String, Array };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.window.WorkspacePalette;
}

const WorkspacePalette = load();

test('a workspace keeps its colour across calls', () => {
  const id = 'b51d6b4b-c841-4ccb-ab45-308858108a75';
  const first = WorkspacePalette.keyFor(id);
  for (let i = 0; i < 20; i++) {
    assert.equal(WorkspacePalette.keyFor(id), first);
  }
});

test('different workspaces are spread across the palette', () => {
  const seen = new Set();
  for (let i = 0; i < 200; i++) {
    seen.add(WorkspacePalette.keyFor(`workspace-${i}`));
  }
  // Not a distribution assertion — only that the hash is not collapsing every
  // id onto one colour, which is the failure that would make the whole palette
  // pointless while still "working".
  assert.equal(seen.size, WorkspacePalette.KEYS.length);
});

test('every key is a six-digit hex literal safe to hand to CSS', () => {
  for (const key of WorkspacePalette.KEYS) {
    assert.match(key, /^#[0-9a-f]{6}$/);
  }
});

test('an empty or missing id still resolves to a real colour', () => {
  for (const id of ['', null, undefined]) {
    assert.ok(WorkspacePalette.KEYS.includes(WorkspacePalette.keyFor(id)));
  }
});

// The roster's workspace sections must be the same colour as that workspace's
// cottage on the map. Until workspace-map.js is switched over to this module,
// the two lists are separate and can drift; this reads its PALETTE and pins
// them together, so drift fails here rather than showing up as two
// almost-matching greens.
test('the keys match the Workspace Map palette, in order', () => {
  const mapSource = readFileSync(new URL('./workspace-map.js', import.meta.url), 'utf8');
  const mapKeys = [...mapSource.matchAll(/^\s*key: '(#[0-9a-f]{6})',$/gm)].map(m => m[1]);
  // Array.from, because KEYS was built inside the vm context and so has that
  // realm's Array prototype: deepStrictEqual compares prototypes and would
  // fail on two arrays whose contents print identically.
  assert.deepEqual(mapKeys, Array.from(WorkspacePalette.KEYS));
});

test('hashKey stays FNV-1a, so no workspace silently changes colour', () => {
  // The empty-string case pins the FNV-1a offset basis; the other pins the
  // multiply-and-xor loop. Together they fail if anyone "simplifies" the hash,
  // which would recolour every existing workspace at once.
  assert.equal(WorkspacePalette.hashKey(''), 2166136261);
  assert.equal(WorkspacePalette.hashKey('studio'), 3210726659);
});
