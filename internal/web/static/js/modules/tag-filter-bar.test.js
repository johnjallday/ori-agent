// Tests for tag-filter-bar.js pure helpers.

import { test } from 'node:test';
import assert from 'node:assert/strict';

const { itemTags, collectTags, matchesActiveTags, filterItems } =
  await import('./tag-filter-bar.js');

const items = [
  { id: 'a', tags: ['music', 'client'] },
  { id: 'b', tags: ['music'] },
  { id: 'c', tags: [] },
  { id: 'd' },
  { id: 'e', tags: [' archive '] }
];

test('itemTags trims and drops empty entries', () => {
  assert.deepEqual(itemTags({ tags: [' music ', '', null] }), ['music']);
  assert.deepEqual(itemTags(null), []);
  assert.deepEqual(itemTags({}), []);
});

test('collectTags returns sorted unique tags across items', () => {
  assert.deepEqual(collectTags(items), ['archive', 'client', 'music']);
});

test('matchesActiveTags uses AND logic', () => {
  assert.equal(matchesActiveTags(['music', 'client'], ['music']), true);
  assert.equal(matchesActiveTags(['music', 'client'], ['music', 'client']), true);
  assert.equal(matchesActiveTags(['music'], ['music', 'client']), false);
  assert.equal(matchesActiveTags([], []), true);
});

test('filterItems with no active tags returns all items', () => {
  assert.equal(filterItems(items, []).length, items.length);
  assert.equal(filterItems(items, new Set()).length, items.length);
});

test('filterItems narrows to items carrying every active tag', () => {
  const one = filterItems(items, ['music']);
  assert.deepEqual(
    one.map(item => item.id),
    ['a', 'b']
  );

  const both = filterItems(items, new Set(['music', 'client']));
  assert.deepEqual(
    both.map(item => item.id),
    ['a']
  );
});

test('filterItems supports a custom tag getter', () => {
  const rows = [
    { name: 'x', labels: ['alpha'] },
    { name: 'y', labels: ['beta'] }
  ];
  const filtered = filterItems(rows, ['beta'], row => row.labels || []);
  assert.deepEqual(
    filtered.map(row => row.name),
    ['y']
  );
});
