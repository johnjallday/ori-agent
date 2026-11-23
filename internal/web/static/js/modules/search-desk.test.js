import test from 'node:test';
import assert from 'node:assert/strict';
import { scopeNames, request } from './search-desk.js';

test('equal folder labels stay distinguishable without disclosing absolute paths', () => {
  const labels = scopeNames([
    { id: 'abcdef1', label: 'Music' },
    { id: 'bbccdd2', label: 'Music' },
    { id: 'test', label: 'Downloads' }
  ]);
  assert.notEqual(labels.get('abcdef1'), labels.get('bbccdd2'));
  assert.equal(labels.get('test'), 'Downloads');
});
test('filename queries use an uncached POST body, never a note search URL', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => new Response('{}'));
  await request('query', { term: 'Harbor' });
  const [url, init] = fetch.mock.calls[0].arguments;
  assert.equal(url, '/api/file-search/query');
  assert.equal(init.method, 'POST');
  assert.equal(init.cache, 'no-store');
  assert.deepEqual(JSON.parse(init.body), { term: 'Harbor' });
});
