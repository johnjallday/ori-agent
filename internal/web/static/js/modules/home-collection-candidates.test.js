import test from 'node:test';
import assert from 'node:assert/strict';
import { collectionCandidatePage } from './home-collection-candidates.js';

const page = {
  arrangement_id: 'arrangement-a',
  review_token: 'review-a',
  catalog_revision: 7,
  operation: 0
};
const valid = () => ({
  ...page,
  query: 'song',
  offset: 0,
  total: 1,
  members: [{ id: 'entry-a', name: 'Song', availability: 'revoked_source' }]
});
test('candidate pages reject foreign, incomplete, duplicate and unbounded results', () => {
  assert.ok(collectionCandidatePage(valid(), page, 'song', 0));
  for (const value of [
    { ...valid(), catalog_revision: 8 },
    { ...valid(), review_token: 'other' },
    { ...valid(), operation: 1 },
    { ...valid(), query: 'other' },
    { ...valid(), offset: 20 },
    { ...valid(), total: 2 },
    { ...valid(), total: 5001 },
    { ...valid(), next_offset: 1 },
    { ...valid(), total: 2, members: [...valid().members, ...valid().members] },
    { ...valid(), members: [{ ...valid().members[0], availability: null }] }
  ])
    assert.equal(collectionCandidatePage(value, page, 'song', 0), null);
});
