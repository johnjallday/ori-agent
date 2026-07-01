import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-map.js', import.meta.url), 'utf8');

// Load the IIFE in a sandbox and grab the exposed pure layout function.
function loadComputeLayout() {
  const window = {};
  vm.runInNewContext(source, { window, document: {} }, { filename: 'workspace-map.js' });
  return window.OriWorkspaceMap.computeLayout;
}

function posById(layout) {
  const map = {};
  layout.tiles.forEach((t) => {
    map[t.id] = { col: t.col, row: t.row };
  });
  return map;
}

test('layout is deterministic regardless of input order (refresh stability)', () => {
  const computeLayout = loadComputeLayout();
  const wss = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }, { id: 'e' }];
  const forward = computeLayout(wss);
  const reversed = computeLayout([...wss].reverse());
  assert.deepEqual(posById(forward), posById(reversed));
});

test('no two tiles overlap', () => {
  const computeLayout = loadComputeLayout();
  const wss = Array.from({ length: 14 }, (_, i) => ({ id: 'w' + i }));
  const layout = computeLayout(wss, { maxCols: 5 });
  const seen = new Set();
  layout.tiles.forEach((t) => {
    const key = t.col + ',' + t.row;
    assert.ok(!seen.has(key), 'overlap at ' + key);
    seen.add(key);
  });
});

test('group members cluster inside their district; group is not a tile', () => {
  const computeLayout = loadComputeLayout();
  const wss = [
    { id: 'g', kind: 'group', name: 'Marketing' },
    { id: 'm1', parent_id: 'g' },
    { id: 'm2', parent_id: 'g' },
    { id: 'm3', parent_id: 'g' },
    { id: 's' } // standalone
  ];
  const layout = computeLayout(wss);
  assert.equal(layout.districts.length, 1);
  const d = layout.districts[0];
  const members = layout.tiles.filter((t) => t.groupId === 'g');
  assert.equal(members.length, 3);
  members.forEach((t) => {
    assert.ok(t.col >= d.col && t.col < d.col + d.w, 'member col within district');
    assert.ok(t.row >= d.row && t.row < d.row + d.h, 'member row within district');
  });
  assert.ok(layout.tiles.some((t) => t.id === 's'), 'standalone rendered');
  assert.ok(!layout.tiles.some((t) => t.id === 'g'), 'group itself is a district, not a tile');
});

test('empty input yields no tiles but a non-zero grid', () => {
  const computeLayout = loadComputeLayout();
  const layout = computeLayout([]);
  assert.equal(layout.tiles.length, 0);
  assert.ok(layout.rows >= 1);
});

test('single workspace sits at the origin cell', () => {
  const computeLayout = loadComputeLayout();
  const layout = computeLayout([{ id: 'only' }]);
  assert.equal(layout.tiles.length, 1);
  assert.equal(layout.tiles[0].col, 0);
  assert.equal(layout.tiles[0].row, 0);
});

test('many workspaces wrap within maxCols across rows', () => {
  const computeLayout = loadComputeLayout();
  const many = Array.from({ length: 12 }, (_, i) => ({ id: 'w' + i }));
  const layout = computeLayout(many, { maxCols: 5 });
  assert.equal(layout.tiles.length, 12);
  assert.ok(layout.cols <= 5, 'never exceeds maxCols');
  assert.ok(layout.rows >= 3, '12 tiles across 5 cols needs 3 rows');
  layout.tiles.forEach((t) => assert.ok(t.col < 5, 'col within bounds'));
});

test('a member whose parent is missing degrades to a standalone tile', () => {
  const computeLayout = loadComputeLayout();
  const layout = computeLayout([{ id: 'orphan', parent_id: 'ghost' }]);
  assert.equal(layout.tiles.length, 1);
  assert.equal(layout.tiles[0].groupId, '');
});
