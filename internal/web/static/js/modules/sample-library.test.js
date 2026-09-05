import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('sample library renders untrusted catalog values only as text', async () => {
  const source = await readFile(new URL('./sample-library.js', import.meta.url), 'utf8');
  assert.equal(source.includes('innerHTML'), false);
  assert.equal(source.includes('insertAdjacentHTML'), false);
  assert.match(source, /textContent/);
});

test('sample library keeps folder, analysis, indexing, copy, and revocation as separate actions', async () => {
  const source = await readFile(new URL('./sample-library.js', import.meta.url), 'utf8');
  for (const boundary of [
    '/roots/review',
    '/analysis/review',
    '/index',
    '/copies/review',
    '/revoke/review'
  ]) {
    assert.ok(source.includes(boundary), `missing boundary ${boundary}`);
  }
  assert.match(source, /review\.disclosure/);
});

test('sample library prevents duplicate actions and restores focus after reviews', async () => {
  const source = await readFile(new URL('./sample-library.js', import.meta.url), 'utf8');
  assert.match(source, /if \(this\.busy\) return false/);
  assert.match(source, /setAttribute\('aria-busy', 'true'\)/);
  assert.match(source, /this\.busyControls/);
  assert.match(source, /target\?\.focus/);
  assert.match(source, /aria-labelledby/);
});
