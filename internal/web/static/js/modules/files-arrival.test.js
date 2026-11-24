// Tests for files-arrival.js — "Search <folder> in Files" lands in the merged
// Files, scoped to the folder, with nothing about it in the address.
//   node --test internal/web/static/js/modules/files-arrival.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { arrivalPlace, arrivalSaved, arrivalSource } from './files-arrival.js';
import { decodeScope } from './home-files-search.js';

test('an arrival is an empty search scoped to one folder, by its source id only', () => {
  const place = arrivalPlace('SRC123');
  assert.deepEqual(place, {
    term: '',
    scope: '{"kind":"arrival","sourceId":"SRC123"}',
    selected: '',
    pages: ''
  });
  // Nothing but the id: no path, no name, no search.
  assert.doesNotMatch(place.scope, /\/|name|term/);
  assert.equal(arrivalSource(place.scope), 'SRC123');
});

test('only an arrival is read as one', () => {
  assert.equal(arrivalSource(''), '');
  assert.equal(arrivalSource('not json'), '');
  assert.equal(arrivalSource('{"kind":"linked","sourceId":"SRC123","workspaceId":"w"}'), '');
  assert.equal(arrivalSource('{"kind":"arrival"}'), '');
  assert.equal(arrivalSource('{"kind":"arrival","sourceId":7}'), '');
});

test('a Back restore never mistakes an arrival for a scope it knows', () => {
  // PR 1's restore decodes the scope; an arrival is not one of its kinds, so a
  // Back into a tab that only ever arrived brings nothing back by that path.
  assert.deepEqual(decodeScope(arrivalPlace('SRC123').scope), { ok: false });
});

test('nothing has been sent until an arrival is saved', () => {
  assert.equal(arrivalSaved(), false);
});
