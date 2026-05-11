// Tests for workspace-notes-hub.js pure helpers (no DOM).

import { test } from 'node:test';
import assert from 'node:assert/strict';

const {
  filterNotes,
  sortNotes,
  formatRelativeTime,
  snippetFromContent,
  renderRow,
} = await import('./workspace-notes-hub.js');

const notes = [
  { id: 'a', name: 'Alpha', content: 'first body', updated_at: '2026-01-01T00:00:00Z', created_at: '2025-12-01T00:00:00Z' },
  { id: 'b', name: 'Bravo', content: 'second body', updated_at: '2026-03-01T00:00:00Z', created_at: '2026-02-01T00:00:00Z' },
  { id: 'c', name: 'Charlie', content: 'third body about alpha topic', updated_at: '2026-02-15T00:00:00Z', created_at: '2025-11-01T00:00:00Z' },
];

test('filterNotes: empty query returns input', () => {
  assert.equal(filterNotes(notes, '').length, 3);
  assert.equal(filterNotes(notes, '   ').length, 3);
});

test('filterNotes: matches name', () => {
  const r = filterNotes(notes, 'bravo');
  assert.equal(r.length, 1);
  assert.equal(r[0].id, 'b');
});

test('filterNotes: matches content', () => {
  const r = filterNotes(notes, 'alpha topic');
  assert.equal(r.length, 1);
  assert.equal(r[0].id, 'c');
});

test('filterNotes: case insensitive', () => {
  assert.equal(filterNotes(notes, 'ALPHA').length, 2); // matches "Alpha" name + "alpha topic" content
});

test('sortNotes: by name asc', () => {
  const r = sortNotes(notes, 'name', 'asc').map((n) => n.id);
  assert.deepEqual(r, ['a', 'b', 'c']);
});

test('sortNotes: by name desc', () => {
  const r = sortNotes(notes, 'name', 'desc').map((n) => n.id);
  assert.deepEqual(r, ['c', 'b', 'a']);
});

test('sortNotes: by updated_at desc (default behavior)', () => {
  const r = sortNotes(notes, 'updated_at', 'desc').map((n) => n.id);
  assert.deepEqual(r, ['b', 'c', 'a']);
});

test('sortNotes: by created_at asc', () => {
  const r = sortNotes(notes, 'created_at', 'asc').map((n) => n.id);
  assert.deepEqual(r, ['c', 'a', 'b']);
});

test('sortNotes: stable for unknown key falls back to updated_at', () => {
  const r = sortNotes(notes, 'whatever', 'desc').map((n) => n.id);
  assert.deepEqual(r, ['b', 'c', 'a']);
});

test('formatRelativeTime: just now', () => {
  const now = Date.now();
  assert.equal(formatRelativeTime(new Date(now).toISOString(), now), 'just now');
});

test('formatRelativeTime: minutes', () => {
  const now = Date.now();
  const t = new Date(now - 5 * 60 * 1000).toISOString();
  assert.equal(formatRelativeTime(t, now), '5m ago');
});

test('formatRelativeTime: hours', () => {
  const now = Date.now();
  const t = new Date(now - 3 * 3600 * 1000).toISOString();
  assert.equal(formatRelativeTime(t, now), '3h ago');
});

test('formatRelativeTime: days', () => {
  const now = Date.now();
  const t = new Date(now - 2 * 86400 * 1000).toISOString();
  assert.equal(formatRelativeTime(t, now), '2d ago');
});

test('formatRelativeTime: returns empty string on invalid input', () => {
  assert.equal(formatRelativeTime(''), '');
  assert.equal(formatRelativeTime(null), '');
  assert.equal(formatRelativeTime('not a date'), '');
});

test('snippetFromContent: strips heading markers', () => {
  const s = snippetFromContent('# Heading\n\nBody text here');
  assert.equal(s, 'Heading Body text here');
});

test('snippetFromContent: truncates to max with ellipsis', () => {
  const long = 'word '.repeat(50);
  const s = snippetFromContent(long, 30);
  assert.ok(s.endsWith('…'));
  assert.ok(s.length <= 31);
});

test('snippetFromContent: wikilink shows target text', () => {
  const s = snippetFromContent('See [[Foo|the foo]] for more');
  assert.match(s, /Foo/);
  assert.doesNotMatch(s, /\[\[/);
});

test('snippetFromContent: empty input returns empty', () => {
  assert.equal(snippetFromContent(''), '');
  assert.equal(snippetFromContent(null), '');
});

test('renderRow: includes title, snippet, and link', () => {
  const html = renderRow(notes[0]);
  assert.match(html, /\/notes\/a/);
  assert.match(html, /Alpha/);
  assert.match(html, /first body/);
});

test('renderRow: escapes HTML in name and content', () => {
  const html = renderRow({
    id: 'x',
    name: '<script>alert(1)</script>',
    content: '<img onerror=1>',
    updated_at: '2026-01-01T00:00:00Z',
    created_at: '2026-01-01T00:00:00Z',
  });
  assert.ok(!html.includes('<script>'));
  assert.ok(!html.includes('<img'));
  assert.match(html, /&lt;script&gt;/);
});

test('renderRow: marks selected rows', () => {
  const html = renderRow(notes[0], true);
  assert.match(html, /is-selected/);
  assert.match(html, /checked/);
});
