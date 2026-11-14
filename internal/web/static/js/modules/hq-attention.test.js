import { test } from 'node:test';
import assert from 'node:assert/strict';
import { attentionSentence, NEEDS_YOU_LIMIT, hqNeedsYouRows } from './hq-attention.js';

const keys = rows => rows.map(row => row.key);
const mount = (key, sentence = 'Finish setup') => ({
  key,
  sentence,
  actions: [{ label: 'Continue', kind: 'mount', key, index: 0 }]
});
const rows = state => hqNeedsYouRows({ ...state, limit: Infinity }).rows;

test('nothing actionable means no rows', () => {
  assert.deepEqual(hqNeedsYouRows(), { rows: [], total: 0, more: 0 });
  assert.deepEqual(
    rows({
      waiting: [],
      review: [],
      goal: { reason: '' },
      brief: { readyToday: false },
      github: { installed: true, count: 0 },
      notes: { stale: false, cameDue: 0 }
    }),
    []
  );
});

test('each waiting ticket has its own Answer action, with the lead first', () => {
  const result = rows({
    waiting: [
      { agent: 'Builder', title: 'Pick a font', taskId: 't2' },
      { agent: 'Atlas', isLead: true, title: 'Approve the plan', taskId: 't1' }
    ]
  });
  assert.deepEqual(keys(result), ['ticket:t1', 'ticket:t2']);
  assert.equal(result[0].category, 'Lead');
  assert.equal(result[0].sentence, 'Atlas (Lead) is waiting on you: Approve the plan.');
  assert.equal(result[1].sentence, 'Builder is waiting on you: Pick a font.');
  assert.deepEqual(result[0].actions, [{ label: 'Answer', kind: 'ticket', id: 't1' }]);
});

test('Review opens the specific ticket; missing IDs and duplicate tickets are ignored', () => {
  const result = rows({
    waiting: [{ taskId: 't1', title: 'Waiting' }],
    review: [
      { taskId: 't1', title: 'Duplicate' },
      { taskId: 't2', title: 'Set up your Personal HQ' },
      { title: 'No ID' },
      null
    ]
  });
  assert.deepEqual(keys(result), ['ticket:t1', 'ticket:t2']);
  assert.equal(result[1].category, 'Ticket');
  assert.equal(result[1].sentence, 'Ready for review: Set up your Personal HQ.');
  assert.deepEqual(result[1].actions, [{ label: 'Review', kind: 'ticket', id: 't2' }]);
});

test('one priority order: waiting, Review, mail, stations, notes, setup, brief, GitHub', () => {
  const result = rows({
    waiting: [{ taskId: 'answer', title: 'Answer' }],
    review: [{ taskId: 'review', title: 'Review' }],
    mail: [{ index: 0, subject: 'Which mix?' }],
    stations: [
      { key: 'calendar-ops', label: 'Calendar Ops', summary: ['2 conflicts'], action: 'Review' }
    ],
    notes: { stale: true, days: 9 },
    setup: [mount('email'), mount('wizard')],
    goal: { reason: 'Set up a model to get suggestions.' },
    brief: { readyToday: true },
    github: { installed: true, count: 1 }
  });
  assert.deepEqual(keys(result), [
    'ticket:answer',
    'ticket:review',
    'mail:0',
    'station:calendar-ops',
    'notes',
    'setup:wizard',
    'setup:email',
    'setup:goal',
    'brief',
    'github'
  ]);
  assert.ok(result.every(row => row.category && row.sentence && row.actions.length));
});

test('setup order and its original controls survive the merge', () => {
  const result = rows({
    setup: [
      mount('first-task'),
      { ...mount('email', 'Connect your inbox.'), category: 'Email' },
      mount('group'),
      mount('intake'),
      mount('wizard')
    ],
    goal: { reason: 'Set up a model to get suggestions.' }
  });
  assert.deepEqual(keys(result), [
    'setup:wizard',
    'setup:intake',
    'setup:group',
    'setup:email',
    'setup:first-task',
    'setup:goal'
  ]);
  assert.equal(result[3].category, 'Email');
  assert.deepEqual(result[5].actions, [{ label: 'Set up a model', kind: 'model' }]);
  assert.deepEqual(rows({ goal: { reason: 'Classify MCP bindings first.' } })[0].actions, [
    { label: 'Open goal', kind: 'goal' }
  ]);
});

test('setup keeps at most two labeled actions; non-actionable progress is not Needs you', () => {
  const result = rows({
    setup: [
      {
        key: 'wizard',
        sentence: 'Finish setup',
        actions: [{ label: '' }, { label: 'A' }, { label: 'B' }, { label: 'C' }]
      },
      { key: 'first-task', sentence: 'Reading the folder', actions: [] },
      { key: 'unknown', sentence: 'Unknown source' },
      { ...mount('email'), sentence: '  ' }
    ]
  });
  assert.equal(result.length, 1);
  assert.deepEqual(
    result[0].actions.map(action => action.label),
    ['A', 'B']
  );
});

test('today’s brief appears unless the welcome already offers its unresolved step', () => {
  const brief = { readyToday: true, time: '7:00 AM' };
  assert.equal(rows({ brief })[0].sentence, "Today's Daily Brief is ready (7:00 AM).");
  assert.deepEqual(rows({ brief })[0].actions, [{ label: 'Read', kind: 'brief' }]);
  assert.deepEqual(rows({ brief, suppressBrief: true }), []);
  assert.deepEqual(rows({ brief: { readyToday: false, time: '7:00 AM' } }), []);
});

test('GitHub proposals only show when installed and waiting', () => {
  assert.deepEqual(rows({ github: { installed: false, count: 3 } }), []);
  assert.deepEqual(rows({ github: { installed: true, count: 0 } }), []);
  assert.equal(
    rows({ github: { installed: true, count: 1 } })[0].sentence,
    '1 GitHub change waits for your approval.'
  );
  assert.equal(
    rows({ github: { installed: true, count: 4 } })[0].sentence,
    '4 GitHub changes wait for your approval.'
  );
});

test('mail, stations and Notes keep their original actions', () => {
  const result = rows({
    mail: [
      {
        from: 'Marco',
        subject: 'Which mix?',
        why: 'He asked a question',
        action: 'Reply',
        index: 0
      }
    ],
    stations: [
      {
        key: 'calendar-ops',
        label: 'Calendar Ops',
        summary: ['2 conflicts', 'Tomorrow'],
        action: 'Review'
      }
    ],
    notes: { stale: true, days: 9, cameDue: 2 }
  });
  assert.deepEqual(
    result.map(row => [row.category, row.sentence]),
    [
      ['Email', 'Marco: Which mix? — He asked a question'],
      ['Calendar', 'Calendar Ops: 2 conflicts · Tomorrow'],
      ['Notes', 'Your notes deserve another look: 2 items came due, the last look was 9 days ago.']
    ]
  );
  assert.deepEqual(
    result.map(row => row.actions[0]),
    [
      { label: 'Reply', kind: 'mail', index: 0 },
      { label: 'Review', kind: 'station', key: 'calendar-ops' },
      { label: 'Look again', kind: 'notes' }
    ]
  );
});

test('five rows, with the total counting rows across all sources; Show all lifts the cap', () => {
  const state = {
    waiting: [{ taskId: 'a' }],
    review: [{ taskId: 'b' }],
    mail: Array.from({ length: 3 }, (_, index) => ({ subject: 'S' + index, index })),
    goal: { reason: 'Set up a model' },
    brief: { readyToday: true }
  };
  const result = hqNeedsYouRows(state);
  assert.equal(NEEDS_YOU_LIMIT, 5);
  assert.equal(result.rows.length, 5);
  assert.equal(result.total, 7);
  assert.equal(result.more, 2);
  const all = hqNeedsYouRows({ ...state, limit: Infinity });
  assert.equal(all.rows.length, 7);
  assert.equal(all.more, 0);
});

test('a long sentence is cut at a word', () => {
  const cut = attentionSentence('word '.repeat(60));
  assert.ok(cut.length <= 160);
  assert.ok(cut.endsWith('word…'));
  assert.equal(attentionSentence('  two\n  lines  '), 'two lines');
});
