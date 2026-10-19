import assert from 'node:assert/strict';
import test from 'node:test';

import {
  chipOptions,
  entriesQuery,
  entryRowModel,
  promoteTextState,
  resultsLine,
  sourceLabel
} from './connected-memory-browser.js';

test('the browser query carries only what is set', () => {
  assert.equal(entriesQuery({}), 'limit=25');
  assert.equal(
    entriesQuery({ q: '  fee sensitivity ', source: 'codex', category: 'task', offset: 25 }),
    'q=fee+sensitivity&source=codex&category=task&limit=25&offset=25'
  );
  assert.equal(entriesQuery({ q: '결과' }), `q=${encodeURIComponent('결과')}&limit=25`);
});

test('chips start with All and order values by count', () => {
  const chips = chipOptions({ codex: 3, claude_code: 7 }, 'codex', sourceLabel);
  assert.deepEqual(
    chips.map(c => [c.value, c.label, c.count, c.active]),
    [
      ['', 'All', 10, false],
      ['claude_code', 'Claude Code', 7, false],
      ['codex', 'Codex', 3, true]
    ]
  );
  // A selected value with no matches stays so it can be cleared.
  const stale = chipOptions({ task: 2 }, 'thread');
  assert.deepEqual(
    stale.map(c => [c.value, c.count, c.active]),
    [
      ['', 2, false],
      ['task', 2, false],
      ['thread', 0, true]
    ]
  );
  assert.deepEqual(chipOptions(undefined, '')[0], {
    value: '',
    label: 'All',
    count: 0,
    active: true
  });
});

test('a row shows source, category, short folder, date, and the promote state', () => {
  const row = entryRowModel({
    id: 'e1',
    source: 'codex',
    source_label: 'Codex',
    title: 'Exits on EMA20',
    category: 'reusable-knowledge',
    folder: '/Users/me/Projects/chart-lab',
    folder_display: '~/Projects/chart-lab',
    date: '2026-10-05',
    excerpt: '…fee sensitivity…',
    origin_display: '~/.codex/memories/MEMORY.md',
    suggested_text: 'Exits on EMA20 beat a fixed stop.',
    suggested_category: 'projects'
  });
  assert.deepEqual(row.meta, ['reusable-knowledge', '~/Projects/chart-lab', '2026-10-05']);
  assert.equal(row.source, 'Codex');
  assert.equal(row.promoted, false);
  assert.equal(row.suggestedCategory, 'projects');
  assert.equal(row.origin, '~/.codex/memories/MEMORY.md');
  const promoted = entryRowModel({
    id: 'e2',
    promoted_item_id: 'item-1',
    suggested_category: 'nope'
  });
  assert.equal(promoted.promoted, true);
  assert.equal(promoted.title, 'Untitled');
  assert.equal(promoted.suggestedCategory, 'projects', 'unknown categories fall back');
  const unverified = entryRowModel({ folder: '-Users-me-gone', folder_unverified: true });
  assert.match(unverified.folderTitle, /could not find this folder/);
});

test('the promote wording follows the global-memory fact rules', () => {
  assert.deepEqual(promoteTextState('Report results net of fees.'), {
    counter: '27 / 500 UTF-8 bytes',
    valid: true
  });
  assert.equal(promoteTextState('').valid, false);
  assert.equal(promoteTextState('two\nlines').valid, false);
  assert.equal(promoteTextState('가'.repeat(166)).valid, true, '498 bytes fits');
  assert.equal(promoteTextState('가'.repeat(167)).valid, false, '501 bytes is over the limit');
});

test('the results line explains an empty list', () => {
  assert.equal(
    resultsLine({ total: 0, shown: 0, q: '' }),
    'No entries yet. Switch a source on above, or wait for its first sync.'
  );
  assert.equal(resultsLine({ total: 0, shown: 0, q: 'x' }), 'No entries match.');
  assert.equal(resultsLine({ total: 0, shown: 0, q: '', filtered: true }), 'No entries match.');
  assert.equal(resultsLine({ total: 1, shown: 1 }), '1 entry');
  assert.equal(resultsLine({ total: 40, shown: 25 }), 'Showing 25 of 40 entries');
});
