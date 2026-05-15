import test from 'node:test';
import assert from 'node:assert/strict';
import { formatRelativeTime } from './relative-time.js';

const NOW = new Date('2026-05-14T12:00:00Z');

test('empty / invalid input returns empty string', () => {
  assert.equal(formatRelativeTime(null), '');
  assert.equal(formatRelativeTime(undefined), '');
  assert.equal(formatRelativeTime(''), '');
  assert.equal(formatRelativeTime('not-a-date'), '');
  assert.equal(formatRelativeTime(NaN), '');
});

test('past, under a minute → "just now"', () => {
  const d = new Date(NOW.getTime() - 30 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), 'just now');
});

test('past minutes', () => {
  const d = new Date(NOW.getTime() - 12 * 60 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), '12m ago');
});

test('past hours', () => {
  const d = new Date(NOW.getTime() - 2 * 60 * 60 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), '2h ago');
});

test('past days (within a week)', () => {
  const d = new Date(NOW.getTime() - 3 * 24 * 60 * 60 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), '3d ago');
});

test('past beyond a week → absolute date, no year', () => {
  // 2 weeks before NOW (2026-05-14) → 2026-04-30
  const d = new Date('2026-04-30T12:00:00Z');
  assert.equal(formatRelativeTime(d, { now: NOW }), 'Apr 30');
});

test('past beyond a year → absolute date with year', () => {
  const d = new Date('2025-03-12T12:00:00Z');
  assert.equal(formatRelativeTime(d, { now: NOW }), 'Mar 12, 2025');
});

test('future, under a minute → "soon"', () => {
  const d = new Date(NOW.getTime() + 30 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), 'soon');
});

test('future minutes', () => {
  const d = new Date(NOW.getTime() + 5 * 60 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), 'in 5m');
});

test('future hours', () => {
  const d = new Date(NOW.getTime() + 6 * 60 * 60 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), 'in 6h');
});

test('future days (within a week)', () => {
  const d = new Date(NOW.getTime() + 2 * 24 * 60 * 60 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), 'in 2d');
});

test('future beyond a week → absolute date', () => {
  // 10 days after NOW → 2026-05-24
  const d = new Date('2026-05-24T12:00:00Z');
  assert.equal(formatRelativeTime(d, { now: NOW }), 'May 24');
});

test('boundary: exactly 60 seconds past → "1m ago" not "just now"', () => {
  const d = new Date(NOW.getTime() - 60 * 1000);
  assert.equal(formatRelativeTime(d, { now: NOW }), '1m ago');
});

test('accepts ISO string input', () => {
  assert.equal(
    formatRelativeTime('2026-05-14T11:00:00Z', { now: NOW }),
    '1h ago',
  );
});
