import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TicketCounts } from './ticket-count-line.js';

const loadCounts = () => TicketCounts;

test('importing the module registers window.OriTicketCounts for classic scripts', async () => {
  const previousWindow = globalThis.window;
  globalThis.window = {};
  try {
    // A query string makes a fresh instance, so the module body runs again
    // with a window present.
    const fresh = await import('./ticket-count-line.js?registers');
    assert.equal(globalThis.window.OriTicketCounts, fresh.TicketCounts);
    assert.equal(typeof globalThis.window.OriTicketCounts.format, 'function');
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
  }
});

test('importing the module without a window does not throw', async () => {
  const previousWindow = globalThis.window;
  delete globalThis.window;
  try {
    const fresh = await import('./ticket-count-line.js?no-window');
    assert.equal(typeof fresh.format, 'function');
  } finally {
    if (previousWindow !== undefined) globalThis.window = previousWindow;
  }
});

const counts = (overrides = {}) => ({
  backlog: 0,
  ready: 0,
  in_progress: 0,
  review: 0,
  done: 0,
  cancelled: 0,
  ...overrides
});

test('the line says review, then in progress, then ready', () => {
  const { format } = loadCounts();
  assert.equal(
    format(counts({ review: 1, in_progress: 2, ready: 3 })),
    '1 in review · 2 in progress · 3 ready'
  );
});

test('states at zero are left out', () => {
  const { format } = loadCounts();
  assert.equal(format(counts({ review: 1, ready: 3 })), '1 in review · 3 ready');
  assert.equal(format(counts({ in_progress: 2 })), '2 in progress');
  assert.equal(format(counts({ ready: 1 })), '1 ready');
});

test('with no open work the line says so', () => {
  const { format, NO_OPEN_WORK } = loadCounts();
  assert.equal(NO_OPEN_WORK, 'No open work');
  assert.equal(format(counts()), 'No open work');
});

test('backlog, done and cancelled never reach the line', () => {
  const { format, openCount } = loadCounts();
  const onlyClosed = counts({ backlog: 4, done: 9, cancelled: 2 });
  assert.equal(format(onlyClosed), 'No open work');
  assert.equal(openCount(onlyClosed), 0);
  assert.equal(format(counts({ backlog: 4, done: 9, cancelled: 2, ready: 1 })), '1 ready');
});

test('missing data is not "No open work"', () => {
  const { format, describe, isKnown } = loadCounts();
  for (const missing of [undefined, null, 'x', 3, [], true]) {
    assert.equal(format(missing), '', `format(${JSON.stringify(missing)})`);
    assert.equal(describe(missing), '', `describe(${JSON.stringify(missing)})`);
    assert.equal(isKnown(missing), false, `isKnown(${JSON.stringify(missing)})`);
  }
  assert.equal(isKnown(counts()), true, 'an all-zero object is known data');
  assert.equal(isKnown({}), true, 'an empty object from the server is known, and all zero');
  assert.equal(format({}), 'No open work');
});

test('a count the server could not make sensible counts as zero', () => {
  const { format } = loadCounts();
  assert.equal(
    format({ review: -2, in_progress: 'many', ready: NaN }),
    'No open work',
    'negative, text and NaN are not work'
  );
  assert.equal(format({ review: '2', ready: 1.9 }), '2 in review · 1 ready');
});

test('openCount adds review, in progress and ready', () => {
  const { openCount } = loadCounts();
  assert.equal(openCount(counts({ review: 1, in_progress: 2, ready: 3, backlog: 10 })), 6);
  assert.equal(openCount(counts()), 0);
  assert.equal(openCount(null), 0);
});

test('describe spells out the noun and agrees in number', () => {
  const { describe } = loadCounts();
  assert.equal(
    describe(counts({ review: 1, in_progress: 2, ready: 3 })),
    '1 ticket in review, 2 tickets in progress, 3 tickets ready'
  );
  assert.equal(describe(counts({ in_progress: 1 })), '1 ticket in progress');
  assert.equal(describe(counts()), 'No open work');
});

test('openParts lists only the open states above zero, in saying order', () => {
  const { openParts } = loadCounts();
  // Array.from builds the arrays in this realm: the formatter runs in a vm
  // context, and deepEqual refuses arrays from another realm.
  assert.deepEqual(
    Array.from(openParts(counts({ ready: 2, review: 1 })), part => [part.key, part.count]),
    [
      ['review', 1],
      ['ready', 2]
    ]
  );
  assert.equal(openParts(null).length, 0);
});

test('sum adds each state across workspaces', () => {
  const { sum, format } = loadCounts();
  const total = sum([
    counts({ review: 1, ready: 2, backlog: 3 }),
    counts({ in_progress: 4, ready: 1, done: 5 })
  ]);
  assert.equal(total.known, 2);
  assert.equal(total.missing, 0);
  assert.equal(format(total.counts), '1 in review · 4 in progress · 3 ready');
  assert.equal(total.counts.backlog, 3);
  assert.equal(total.counts.done, 5);
});

test('sum reports workspaces that did not report instead of counting them as zero', () => {
  const { sum, format } = loadCounts();
  const partial = sum([counts({ ready: 2 }), undefined, null, 'x']);
  assert.equal(partial.known, 1);
  assert.equal(partial.missing, 3);
  assert.equal(format(partial.counts), '2 ready');
});

test('sum is null when nothing reported, and an honest zero when there is nothing to count', () => {
  const { sum, format } = loadCounts();
  const none = sum([undefined, null]);
  assert.equal(none.counts, null);
  assert.equal(none.missing, 2);
  assert.equal(format(none.counts), '', 'unknown stays unknown');

  const empty = sum([]);
  assert.equal(empty.missing, 0);
  assert.equal(format(empty.counts), 'No open work');
  assert.equal(sum(undefined).missing, 0);
});

test('the exported object cannot be changed by a caller', () => {
  const api = loadCounts();
  assert.equal(Object.isFrozen(api), true);
});
