// Tests for note-staging.js — run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { projectHunks, applyHunks, diffLines } from './note-staging.js';

const stagedCard = (overrides = {}) => ({
  id: overrides.id || 'c1',
  staged: true,
  status: 'ready',
  sourceRange: overrides.sourceRange || { start: 0, end: 5 },
  mode: overrides.mode || 'replace',
  output: overrides.output ?? 'NEW',
  originalText: overrides.originalText ?? 'OLD',
  action: 'rewrite',
  ...overrides
});

// =============================================================================
// projectHunks
// =============================================================================

test('projectHunks: ignores cards that are not staged or not ready', () => {
  const cards = [
    stagedCard({ id: '1', staged: false }),
    stagedCard({ id: '2', status: 'loading' }),
    stagedCard({ id: '3' })
  ];
  const hunks = projectHunks(cards);
  assert.equal(hunks.length, 1);
  assert.equal(hunks[0].id, '3');
});

test('projectHunks: ignores staged cards without a source range', () => {
  const hunks = projectHunks([
    stagedCard({ id: 'missing', sourceRange: null }),
    stagedCard({ id: 'invalid', sourceRange: { start: 5, end: 2 } }),
    stagedCard({ id: 'ok', sourceRange: { start: 1, end: 3 } })
  ]);
  assert.deepEqual(
    hunks.map(h => h.id),
    ['ok']
  );
  assert.deepEqual(hunks[0].sourceRange, { start: 1, end: 3 });
});

test('projectHunks: detects conflict on overlapping replace hunks', () => {
  const cards = [
    stagedCard({ id: 'a', sourceRange: { start: 0, end: 10 }, mode: 'replace' }),
    stagedCard({ id: 'b', sourceRange: { start: 5, end: 15 }, mode: 'replace' })
  ];
  const hunks = projectHunks(cards);
  assert.deepEqual(hunks[0].conflictsWith, ['b']);
  assert.deepEqual(hunks[1].conflictsWith, ['a']);
});

test('projectHunks: insert-before + insert-after at same offset do not conflict', () => {
  const cards = [
    stagedCard({ id: 'a', sourceRange: { start: 5, end: 10 }, mode: 'insert-before' }),
    stagedCard({ id: 'b', sourceRange: { start: 5, end: 10 }, mode: 'insert-after' })
  ];
  const hunks = projectHunks(cards);
  assert.deepEqual(hunks[0].conflictsWith, []);
  assert.deepEqual(hunks[1].conflictsWith, []);
});

test('projectHunks: replace + overlapping insert flags conflict', () => {
  const cards = [
    stagedCard({ id: 'a', sourceRange: { start: 0, end: 10 }, mode: 'replace' }),
    stagedCard({ id: 'b', sourceRange: { start: 5, end: 7 }, mode: 'insert-after' })
  ];
  const hunks = projectHunks(cards);
  assert.equal(hunks[0].conflictsWith.length, 1);
  assert.equal(hunks[1].conflictsWith.length, 1);
});

// =============================================================================
// applyHunks
// =============================================================================

test('applyHunks: single replace', () => {
  const src = 'Hello world';
  const hunks = projectHunks([
    stagedCard({
      sourceRange: { start: 6, end: 11 },
      mode: 'replace',
      originalText: 'world',
      output: 'there'
    })
  ]);
  assert.equal(applyHunks(src, hunks).content, 'Hello there');
});

test('applyHunks: multiple non-overlapping hunks (replace + insert-after)', () => {
  const src = 'A B C D E';
  // Replace "C" with "Z" and insert " — note" after the same position.
  const hunks = projectHunks([
    stagedCard({
      id: 'r',
      sourceRange: { start: 4, end: 5 },
      mode: 'replace',
      originalText: 'C',
      output: 'Z'
    }),
    stagedCard({
      id: 'i',
      sourceRange: { start: 0, end: 1 },
      mode: 'insert-after',
      originalText: 'A',
      output: 'note'
    })
  ]);
  const out = applyHunks(src, hunks);
  // Bottom-up: replace at 4-5 first, then insert-after at 0-1.
  assert.equal(out.content, 'A\n\nnote B Z D E');
  assert.equal(out.applied.length, 2);
});

test('applyHunks: insert-before vs insert-after at distinct offsets', () => {
  const src = 'one two three';
  const hunks = projectHunks([
    stagedCard({
      id: 'b',
      sourceRange: { start: 4, end: 7 },
      mode: 'insert-before',
      originalText: 'two',
      output: 'BEFORE'
    }),
    stagedCard({
      id: 'a',
      sourceRange: { start: 8, end: 13 },
      mode: 'insert-after',
      originalText: 'three',
      output: 'AFTER'
    })
  ]);
  const out = applyHunks(src, hunks).content;
  assert.equal(out, 'one BEFORE\n\ntwo three\n\nAFTER');
});

test('applyHunks: stale source range falls back to insert at start', () => {
  const src = 'something else entirely';
  const hunks = projectHunks([
    stagedCard({
      sourceRange: { start: 0, end: 5 },
      mode: 'replace',
      originalText: 'OLD', // doesn't match the actual slice "somet"
      output: 'NEW'
    })
  ]);
  const out = applyHunks(src, hunks);
  assert.equal(out.applied.length, 1);
  assert.equal(out.skipped.length, 1);
  assert.equal(out.skipped[0].reason, 'stale-range-fallback-insert');
  assert.equal(out.content, 'NEW\n\nsomething else entirely');
});

test('applyHunks: empty hunks list returns source unchanged', () => {
  const src = 'untouched';
  const out = applyHunks(src, []);
  assert.equal(out.content, src);
  assert.deepEqual(out.applied, []);
});

test('applyHunks: respects bottom-up ordering (offsets remain valid)', () => {
  // Two replaces — if applied top-down, the second one's offsets would be off.
  const src = '0123456789';
  const hunks = projectHunks([
    stagedCard({
      id: 'a',
      sourceRange: { start: 0, end: 2 },
      mode: 'replace',
      originalText: '01',
      output: 'AA'
    }),
    stagedCard({
      id: 'b',
      sourceRange: { start: 5, end: 7 },
      mode: 'replace',
      originalText: '56',
      output: 'BB'
    })
  ]);
  assert.equal(applyHunks(src, hunks).content, 'AA234BB789');
});

// =============================================================================
// diffLines
// =============================================================================

test('diffLines: replace produces removed-then-added lines', () => {
  const lines = diffLines('a\nb', 'A\nB', 'replace');
  assert.deepEqual(lines, [
    { kind: 'removed', text: 'a' },
    { kind: 'removed', text: 'b' },
    { kind: 'added', text: 'A' },
    { kind: 'added', text: 'B' }
  ]);
});

test('diffLines: insert-before yields only added lines', () => {
  const lines = diffLines('original', 'INSERTED', 'insert-before');
  assert.deepEqual(lines, [{ kind: 'added', text: 'INSERTED' }]);
});

test('diffLines: insert-after yields only added lines', () => {
  const lines = diffLines('original', 'one\ntwo', 'insert-after');
  assert.deepEqual(lines, [
    { kind: 'added', text: 'one' },
    { kind: 'added', text: 'two' }
  ]);
});
