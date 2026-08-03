// Tests for tag-input.js pure helpers.

import { test } from 'node:test';
import assert from 'node:assert/strict';

const {
  normalizeTagValue,
  normalizeTagList,
  filterSuggestions,
  topSuggestions,
  fetchTagPool,
  clearTagPoolCache
} = await import('./tag-input.js');

const pool = [
  { name: 'music', total: 9, counts: { workspaces: 3 } },
  { name: 'mixing', total: 5, counts: { workspaces: 1 } },
  { name: 'mastering', total: 5, counts: { notes: 5 } },
  { name: 'client', total: 2, counts: { sessions: 2 } },
  { name: 'archive', total: 1, counts: { templates: 1 } }
];

test('normalizeTagValue trims and lowercases', () => {
  assert.equal(normalizeTagValue('  Deep Work '), 'deep work');
  assert.equal(normalizeTagValue(null), '');
});

test('normalizeTagList dedupes after normalization and drops empties', () => {
  const result = normalizeTagList([' Music ', 'music', '', 'Client:Acme']);
  assert.deepEqual(result, { tags: ['music', 'client:acme'], error: '' });
});

test('normalizeTagList reports overlong tags', () => {
  const result = normalizeTagList(['x'.repeat(65)]);
  assert.match(result.error, /64 character limit/);
});

test('normalizeTagList enforces the tag cap', () => {
  const result = normalizeTagList(Array.from({ length: 21 }, (_, i) => `tag-${i}`));
  assert.match(result.error, /At most 20 tags/);
});

test('filterSuggestions matches substrings and ranks prefix matches first', () => {
  const names = filterSuggestions(pool, { query: 'm' });
  // Prefix matches (music, mixing, mastering) before substring-only ones.
  assert.deepEqual(names, ['music', 'mastering', 'mixing']);
});

test('filterSuggestions excludes already-selected tags', () => {
  const names = filterSuggestions(pool, { query: 'm', exclude: ['Music'] });
  assert.ok(!names.includes('music'));
});

test('filterSuggestions respects the limit', () => {
  const names = filterSuggestions(pool, { query: 'm', limit: 1 });
  assert.deepEqual(names, ['music']);
});

test('filterSuggestions with empty query returns nothing-filtered list by usage', () => {
  const names = filterSuggestions(pool, { query: '', limit: 3 });
  assert.deepEqual(names, ['music', 'mastering', 'mixing']);
});

test('topSuggestions returns most-used tags excluding applied, capped at 10', () => {
  const bigPool = Array.from({ length: 15 }, (_, i) => ({
    name: `tag-${String(i).padStart(2, '0')}`,
    total: 100 - i
  }));
  const names = topSuggestions(bigPool, { exclude: ['tag-00'] });
  assert.equal(names.length, 10);
  assert.equal(names[0], 'tag-01');
});

test('fetchTagPool returns pool tags and caches them', async () => {
  clearTagPoolCache();
  let calls = 0;
  const fetcher = async () => {
    calls++;
    return { ok: true, json: async () => ({ tags: pool }) };
  };
  const first = await fetchTagPool({ fetcher });
  const second = await fetchTagPool({ fetcher });
  assert.equal(first.length, pool.length);
  assert.equal(second.length, pool.length);
  assert.equal(calls, 1, 'second call should hit the cache');

  const forced = await fetchTagPool({ fetcher, force: true });
  assert.equal(forced.length, pool.length);
  assert.equal(calls, 2, 'force bypasses the cache');
  clearTagPoolCache();
});

test('fetchTagPool degrades to an empty pool on failure', async () => {
  clearTagPoolCache();
  const failing = async () => {
    throw new Error('network down');
  };
  assert.deepEqual(await fetchTagPool({ fetcher: failing }), []);

  const notOK = async () => ({ ok: false, json: async () => ({}) });
  assert.deepEqual(await fetchTagPool({ fetcher: notOK }), []);
  clearTagPoolCache();
});
