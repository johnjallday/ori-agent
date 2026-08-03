// Tests for note-rail-notes.js pure helpers.

import { test } from 'node:test';
import assert from 'node:assert/strict';

const { filterNotesByQuery, sortNotesForRail, renderListItem } =
  await import('./note-rail-notes.js');

const notes = [
  { id: 'a', name: 'Alpha', updated_at: '2026-01-01T00:00:00Z' },
  { id: 'b', name: 'Bravo', updated_at: '2026-03-01T00:00:00Z' },
  { id: 'c', name: 'Charlie', updated_at: '2026-02-15T00:00:00Z' }
];

test('filterNotesByQuery: empty query returns input', () => {
  assert.equal(filterNotesByQuery(notes, '').length, 3);
});

test('filterNotesByQuery: matches name substring case-insensitive', () => {
  const r = filterNotesByQuery(notes, 'BRA');
  assert.equal(r.length, 1);
  assert.equal(r[0].id, 'b');
});

test('filterNotesByQuery: filters by name only (not body)', () => {
  // Notes have no content here — ensure unmatched query returns empty.
  assert.equal(filterNotesByQuery(notes, 'unicorn').length, 0);
});

test('sortNotesForRail: most recently updated first', () => {
  const ids = sortNotesForRail(notes).map(n => n.id);
  assert.deepEqual(ids, ['b', 'c', 'a']);
});

test('sortNotesForRail: handles missing updated_at', () => {
  const mixed = [
    { id: 'a', name: 'A' },
    { id: 'b', name: 'B', updated_at: '2026-01-01T00:00:00Z' }
  ];
  const ids = sortNotesForRail(mixed).map(n => n.id);
  assert.equal(ids[0], 'b'); // dated one wins
});

test('renderListItem: marks active note', () => {
  const html = renderListItem(notes[0], true);
  assert.match(html, /is-active/);
  assert.match(html, /data-note-id="a"/);
  assert.match(html, /Alpha/);
});

test('renderListItem: non-active note has no is-active class', () => {
  const html = renderListItem(notes[0], false);
  assert.doesNotMatch(html, /is-active/);
});

test('renderListItem: escapes name', () => {
  const html = renderListItem({ id: 'x', name: '<script>1</script>' });
  assert.ok(!html.includes('<script>'));
  assert.match(html, /&lt;script&gt;/);
});

test('renderListItem: falls back to "Untitled" when name is missing', () => {
  const html = renderListItem({ id: 'x' });
  assert.match(html, /Untitled/);
});
