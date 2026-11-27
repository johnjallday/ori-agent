import { test } from 'node:test';
import assert from 'node:assert/strict';
import { groundsLayout } from './hq-grounds-layout.js';

const stations = [
  'watchtower',
  'email',
  'calendar-ops',
  'daily-brief',
  'file-janitor',
  'memory',
  'second-brain'
].map(key => ({
  key,
  state() {
    throw Error('layout must not read status');
  }
}));

test('named lots surround HQ and do not depend on registry order or Filing being installed', () => {
  const { rows, positions } = groundsLayout(stations);
  assert.equal(rows, 3);
  const without = groundsLayout(stations.filter(s => s.key !== 'file-janitor').reverse());
  assert.equal(new Set([...positions.values()].map(p => `${p.x}:${p.y}`)).size, 7);
  assert.deepEqual(positions.get('watchtower'), { x: 0.5, y: 1 / 6 });
  for (const [key, point] of without.positions) assert.deepEqual(point, positions.get(key));
});

test('a saved station wins its chosen lot; other buildings claim clear lots, not the lead court', () => {
  const additional = Array.from({ length: 10 }, (_, i) => ({ key: `plugin-${i}` }));
  const { rows, positions } = groundsLayout([...stations, ...additional], {
    memory: { x: 0.5, y: 0.12 },
    stale: { x: 1, y: 1 }
  });
  const memory = positions.get('memory');
  assert.deepEqual(memory, { x: 0.5, y: 0.12 });
  assert.equal(positions.size, 17);
  for (const [key, p] of positions) {
    if (key === 'memory') continue;
    assert.ok(Math.abs(memory.x - p.x) >= 0.31 || Math.abs(memory.y - p.y) >= 0.77 / rows);
    assert.ok(p.x !== 0.5 || ![1, 2].includes(Math.floor(p.y * rows)));
  }
});
