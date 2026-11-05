import test from 'node:test';
import assert from 'node:assert/strict';
import { STALE_SCAN_MS, libraryFreshness, scanAgeText } from './library-freshness.js';

const NOW = Date.parse('2026-11-05T12:00:00Z');
const at = ms => ({ last_complete_scan_at: new Date(NOW - ms).toISOString() });
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

test('freshness reads each age bucket and turns stale only past a day', () => {
  const cases = [
    [0, 'Last scanned just now', false],
    [59_000, 'Last scanned just now', false],
    [MIN, 'Last scanned 1 minute ago', false],
    [42 * MIN, 'Last scanned 42 minutes ago', false],
    [HOUR, 'Last scanned 1 hour ago', false],
    [23 * HOUR, 'Last scanned 23 hours ago', false],
    [DAY, 'Last scanned 1 day ago', false],
    [DAY + 1, 'Last scanned 1 day ago', true],
    [3 * DAY, 'Last scanned 3 days ago', true],
    [30 * DAY, 'Last scanned 30 days ago', true]
  ];
  for (const [age, text, stale] of cases) {
    const view = libraryFreshness(at(age), NOW);
    assert.equal(view.text, text, `age ${age}`);
    assert.equal(view.stale, stale, `stale at ${age}`);
    assert.equal(view.age_ms, age);
  }
  assert.equal(STALE_SCAN_MS, DAY);
});

test('freshness past thirty days shows the date, and a clock behind the server reads just now', () => {
  const old = libraryFreshness({ last_complete_scan_at: '2026-09-01T12:00:00Z' }, NOW);
  assert.match(old.text, /^Last scanned on .*2026$/);
  assert.equal(old.stale, true);
  const ahead = libraryFreshness(at(-5 * MIN), NOW);
  assert.equal(JSON.parse(JSON.stringify(ahead)).text, 'Last scanned just now');
  assert.equal(ahead.stale, false);
});

test('no complete scan reads "Not scanned yet" and is never stale', () => {
  for (const summary of [null, {}, { last_complete_scan_at: null }, { last_complete_scan_at: 'x' }])
    assert.equal(
      JSON.stringify(libraryFreshness(summary, NOW)),
      JSON.stringify({ text: 'Not scanned yet', stale: false, age_ms: null })
    );
});

test('a partial newer scan leaves the age on the older complete one (FR6)', () => {
  // The digest names the newest scan, partial here; the age follows only the
  // server's last_complete_scan_at.
  const summary = {
    ...at(3 * DAY),
    digest: { scan_id: 'p', coverage: 'partial', scanned_at: new Date(NOW).toISOString() }
  };
  assert.equal(libraryFreshness(summary, NOW).text, 'Last scanned 3 days ago');
  assert.equal(libraryFreshness(summary, NOW).stale, true);
});

test('scanAgeText guards against bad input', () => {
  assert.equal(scanAgeText(NaN, ''), 'just now');
  assert.equal(scanAgeText(40 * DAY, 'not a date'), '40 days ago');
});
