import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MEMORY_STATION,
  memoryNewSinceBaseline,
  memoryStationSeenKey,
  memoryStationState,
  memoryStationSummary,
  readMemoryStationBaseline,
  writeMemoryStationBaseline
} from './memory-station.js';

function entries(types) {
  return types.map((type, index) => ({ type, text: 'entry ' + index, provenance: 'user' }));
}

function summaryOf(payload) {
  return memoryStationSummary(payload);
}

function memoryStorage() {
  const values = new Map();
  return {
    getItem: key => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value)),
    values
  };
}

test('the station key is memory', () => {
  assert.equal(MEMORY_STATION, 'memory');
});

test('summary counts own entries, watching, and entries agents are not shown', () => {
  const summary = summaryOf({
    entries: [
      ...entries(['fact', 'watch', 'thread', 'decision']),
      { type: 'fact', text: 'reviewed', provenance: 'ori-hq:item:rev', managed: true },
      { type: 'fact', text: 'old', provenance: 'user', in_prompt: false }
    ],
    raw_size: 9100,
    over_budget: true
  });
  assert.deepEqual(summary, { count: 5, watching: 2, dropped: 1, overBudget: true });
  assert.deepEqual(summaryOf(null), { count: 0, watching: 0, dropped: 0, overBudget: false });
});

test('every badge row of PRD FR5', () => {
  assert.deepEqual(memoryStationState({ status: 'loading' }), {
    value: '—',
    description: 'loading memory',
    tone: 'loading'
  });
  assert.deepEqual(memoryStationState({ status: 'idle' }), {
    value: '—',
    description: 'loading memory',
    tone: 'loading'
  });
  assert.deepEqual(memoryStationState({ status: 'error' }), {
    value: 'Memory',
    description: 'status unavailable',
    tone: 'degraded'
  });
  assert.deepEqual(memoryStationState({ status: 'ready', summary: summaryOf({ entries: [] }) }), {
    value: 'Empty',
    description: 'nothing recorded yet',
    tone: ''
  });
  assert.deepEqual(
    memoryStationState({
      status: 'ready',
      summary: summaryOf({ entries: entries(['fact', 'watch', 'thread', 'fact']) })
    }),
    { value: '4 entries', description: '4 entries, 2 watching', tone: '' }
  );
  assert.deepEqual(
    memoryStationState({ status: 'ready', summary: summaryOf({ entries: entries(['fact']) }) }),
    { value: '1 entry', description: '1 entry', tone: '' },
    'singular, and "W watching" is omitted when W is 0'
  );
  assert.deepEqual(
    memoryStationState({
      status: 'ready',
      summary: summaryOf({ entries: entries(['fact', 'fact', 'fact']) }),
      baseline: { count: 1 }
    }),
    {
      value: '+2 new',
      description: '2 entries recorded since you last looked',
      tone: 'attention'
    }
  );
  assert.deepEqual(
    memoryStationState({
      status: 'ready',
      summary: summaryOf({
        entries: [
          ...entries(['fact', 'fact']),
          ...entries(['fact', 'fact', 'fact']).map(e => ({ ...e, in_prompt: false }))
        ],
        over_budget: true
      })
    }),
    {
      value: 'Over budget',
      description: '3 entries are not shown to agents',
      tone: 'attention'
    }
  );
  assert.equal(
    memoryStationState({
      status: 'ready',
      summary: summaryOf({ entries: [{ type: 'fact', in_prompt: false }], over_budget: true })
    }).description,
    '1 entry is not shown to agents'
  );
});

test('over budget beats new; an unavailable read beats both', () => {
  const summary = summaryOf({
    entries: [...entries(['fact', 'fact']), { type: 'fact', in_prompt: false }],
    over_budget: true
  });
  const baseline = { count: 1 };
  assert.equal(memoryStationState({ status: 'ready', summary, baseline }).value, 'Over budget');
  assert.equal(memoryStationState({ status: 'error', summary, baseline }).value, 'Memory');
  assert.equal(memoryStationState({ status: 'loading', summary, baseline }).value, '—');
});

test('"+K new" needs a baseline and growth; deletions never read as new (FR7)', () => {
  const summary = summaryOf({ entries: entries(['fact', 'fact', 'fact']) });
  assert.equal(memoryNewSinceBaseline(summary, null), 0);
  assert.equal(memoryNewSinceBaseline(summary, { count: 1 }), 2);
  assert.equal(memoryNewSinceBaseline(summary, { count: 3 }), 0);
  assert.equal(memoryNewSinceBaseline(summary, { count: 5 }), 0);
  assert.equal(memoryStationState({ status: 'ready', summary }).value, '3 entries');
  assert.equal(
    memoryStationState({ status: 'ready', summary, baseline: { count: 5 } }).value,
    '3 entries'
  );
});

test('the seen baseline round-trips per workspace and survives storage that throws', () => {
  const storage = memoryStorage();
  assert.equal(memoryStationSeenKey('ws-1'), 'ori:memory-station-seen:ws-1');
  assert.equal(readMemoryStationBaseline(storage, 'ws-1'), null);
  assert.equal(writeMemoryStationBaseline(storage, 'ws-1', { count: 4, watching: 1 }), true);
  assert.equal(storage.values.get(memoryStationSeenKey('ws-1')), JSON.stringify({ count: 4 }));
  assert.deepEqual(readMemoryStationBaseline(storage, 'ws-1'), { count: 4 });
  assert.equal(readMemoryStationBaseline(storage, 'ws-2'), null, 'baselines are per workspace');

  storage.values.set(memoryStationSeenKey('ws-3'), '{not json');
  assert.equal(readMemoryStationBaseline(storage, 'ws-3'), null);
  storage.values.set(memoryStationSeenKey('ws-4'), JSON.stringify({ count: -1 }));
  assert.equal(readMemoryStationBaseline(storage, 'ws-4'), null);

  const throwing = {
    getItem() {
      throw new Error('SecurityError');
    },
    setItem() {
      throw new Error('QuotaExceededError');
    }
  };
  assert.equal(readMemoryStationBaseline(throwing, 'ws-1'), null);
  assert.equal(writeMemoryStationBaseline(throwing, 'ws-1', { count: 1 }), false);
  assert.equal(readMemoryStationBaseline(null, 'ws-1'), null);
  const summary = summaryOf({ entries: entries(['fact']) });
  assert.equal(
    memoryStationState({
      status: 'ready',
      summary,
      baseline: readMemoryStationBaseline(throwing, 'ws-1')
    }).value,
    '1 entry',
    'unavailable storage shows the plain count'
  );
});

test('the Personal HQ badge says global memory and ranks "to review" between over budget and new', () => {
  const summary = summaryOf({ entries: entries(['fact', 'watch', 'fact']) });
  assert.deepEqual(memoryStationState({ status: 'ready', summary, personalHQ: true }), {
    value: '3 entries',
    description: 'global memory, 3 entries, 1 watching',
    tone: ''
  });
  assert.deepEqual(
    memoryStationState({
      status: 'ready',
      summary,
      personalHQ: true,
      reviewCount: 2,
      baseline: { count: 1 }
    }),
    {
      value: '2 to review',
      description: 'global memory, 2 to review, 2 proposals waiting on Profile',
      tone: 'attention'
    }
  );
  assert.equal(
    memoryStationState({
      status: 'ready',
      summary: { ...summary, overBudget: true, dropped: 1 },
      personalHQ: true,
      reviewCount: 2
    }).value,
    'Over budget'
  );
  assert.equal(
    memoryStationState({
      status: 'ready',
      summary,
      personalHQ: true,
      reviewCount: 0,
      baseline: { count: 1 }
    }).description,
    'global memory, +2 new, 2 entries recorded since you last looked'
  );
  assert.equal(
    memoryStationState({ status: 'loading', personalHQ: true }).description,
    'global memory, loading memory'
  );
  assert.equal(
    memoryStationState({ status: 'error', personalHQ: true }).description,
    'global memory, status unavailable'
  );
  assert.deepEqual(
    memoryStationState({
      status: 'ready',
      summary: memoryStationSummary({ entries: [] }),
      personalHQ: true
    }),
    { value: 'Empty', description: 'global memory, Empty, nothing recorded yet', tone: '' }
  );
  assert.equal(
    memoryStationState({ status: 'ready', summary, reviewCount: 2 }).value,
    '3 entries',
    'an ordinary workspace never shows a review count'
  );
});
