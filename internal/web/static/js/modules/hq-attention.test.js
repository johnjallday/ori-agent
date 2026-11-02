import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ATTENTION_LIMIT,
  attentionSentence,
  NEEDS_YOU_LIMIT,
  capAttentionLines,
  hqAttentionLines,
  hqNeedsYouRows
} from './hq-attention.js';

const keys = lines => lines.map(item => item.key);

test('nothing to say is no lines', () => {
  assert.deepEqual(hqAttentionLines(), []);
  assert.deepEqual(
    hqAttentionLines({
      waiting: [],
      setup: [],
      goal: { reason: '' },
      brief: { readyToday: false },
      github: { installed: true, count: 0 }
    }),
    []
  );
});

test('the lead waiting on input is one line naming the lead and the ticket', () => {
  const [lead] = hqAttentionLines({
    waiting: [
      { agent: 'Builder', isLead: false, title: 'Pick a font', taskId: 't2' },
      { agent: 'Atlas', isLead: true, title: 'Approve the plan', taskId: 't1' }
    ]
  });
  assert.equal(lead.key, 'lead');
  assert.equal(lead.category, 'Lead');
  assert.equal(
    lead.sentence,
    'Atlas (Lead) is waiting on you: Approve the plan, and 1 other ticket.'
  );
  assert.deepEqual(lead.actions, [
    { label: 'Answer', kind: 'ticket', id: 't1' },
    { label: 'See all', kind: 'tickets-waiting' }
  ]);

  const [solo] = hqAttentionLines({
    waiting: [{ agent: 'Builder', isLead: false, title: 'Pick a font', taskId: 't2' }]
  });
  assert.equal(solo.sentence, 'Builder is waiting on you: Pick a font.');
  assert.equal(solo.actions.length, 1);
});

test('each setup source becomes a Setup line, in the PRD order', () => {
  const action = label => ({ label, kind: 'mount', key: label, index: 0 });
  const lines = hqAttentionLines({
    // Given out of order on purpose.
    setup: [
      { key: 'first-task', sentence: 'Atlas is reading your folder.', actions: [] },
      {
        key: 'email',
        category: 'Email',
        sentence: 'Connect your inbox.',
        actions: [action('Connect')]
      },
      { key: 'group', sentence: 'This group needs a Reviewer.', actions: [] },
      {
        key: 'intake',
        sentence: '2 changes from updated course materials.',
        actions: [action('Review changes')]
      },
      { key: 'wizard', sentence: 'Setup · 2 of 4 steps done', actions: [action('Continue setup')] }
    ],
    goal: { reason: 'Set up a model to get suggestions.' }
  });
  assert.deepEqual(keys(lines), [
    'setup:wizard',
    'setup:intake',
    'setup:group',
    'setup:email',
    'setup:first-task',
    'setup:goal'
  ]);
  assert.equal(lines[3].category, 'Email');
  assert.equal(lines[0].category, 'Setup');
  // No model: the strip's action is "Set up a model" (FR8).
  assert.deepEqual(lines[5].actions, [{ label: 'Set up a model', kind: 'model' }]);
});

test('a goal problem that is not about a model opens the goal', () => {
  const [goal] = hqAttentionLines({ goal: { reason: 'Classify MCP bindings first.' } });
  assert.deepEqual(goal.actions, [{ label: 'Open goal', kind: 'goal' }]);
});

test('a setup line keeps at most two actions and drops unlabeled ones', () => {
  const [wizard] = hqAttentionLines({
    setup: [
      {
        key: 'wizard',
        sentence: 'Finish setup',
        actions: [{ label: '' }, { label: 'A' }, { label: 'B' }, { label: 'C' }]
      }
    ]
  });
  assert.deepEqual(
    wizard.actions.map(item => item.label),
    ['A', 'B']
  );
});

test('an unknown setup key or an empty sentence is ignored', () => {
  assert.deepEqual(
    hqAttentionLines({
      setup: [
        { key: 'janitor', sentence: 'Files to review' },
        { key: 'wizard', sentence: '   ' }
      ]
    }),
    []
  );
});

test('a brief ready today is a line; an earlier brief is not', () => {
  const [brief] = hqAttentionLines({ brief: { readyToday: true, time: '7:00 AM' } });
  assert.equal(brief.category, 'Brief');
  assert.equal(brief.sentence, "Today's Daily Brief is ready (7:00 AM).");
  assert.deepEqual(brief.actions, [{ label: 'Read', kind: 'brief' }]);
  assert.deepEqual(hqAttentionLines({ brief: { readyToday: false, time: '7:00 AM' } }), []);
});

test('GitHub proposals show only when installed and waiting', () => {
  assert.deepEqual(hqAttentionLines({ github: { installed: false, count: 3 } }), []);
  assert.deepEqual(hqAttentionLines({ github: { installed: true, count: 0 } }), []);
  const [one] = hqAttentionLines({ github: { installed: true, count: 1 } });
  assert.equal(one.sentence, '1 GitHub change waits for your approval.');
  const [many] = hqAttentionLines({ github: { installed: true, count: 4 } });
  assert.equal(many.sentence, '4 GitHub changes wait for your approval.');
});

test('the strip lists by priority: lead, setup, brief, GitHub', () => {
  const lines = hqAttentionLines({
    github: { installed: true, count: 2 },
    brief: { readyToday: true },
    setup: [{ key: 'email', sentence: 'Connect your inbox.' }],
    waiting: [{ agent: 'Atlas', isLead: true, title: 'Approve', taskId: 't1' }]
  });
  assert.deepEqual(keys(lines), ['lead', 'setup:email', 'brief', 'github']);
});

test('the strip shows three lines and counts the rest behind "and N more"', () => {
  const lines = hqAttentionLines({
    github: { installed: true, count: 2 },
    brief: { readyToday: true },
    setup: [
      { key: 'email', sentence: 'Connect your inbox.' },
      { key: 'wizard', sentence: 'Finish setup.' }
    ],
    waiting: [{ agent: 'Atlas', isLead: true, title: 'Approve', taskId: 't1' }]
  });
  assert.equal(ATTENTION_LIMIT, 3);
  const capped = capAttentionLines(lines);
  assert.deepEqual(keys(capped.shown), ['lead', 'setup:wizard', 'setup:email']);
  assert.equal(capped.hidden, 2);
  const open = capAttentionLines(lines, { expanded: true });
  assert.equal(open.shown.length, 5);
  assert.equal(open.hidden, 0);
  assert.deepEqual(capAttentionLines(lines.slice(0, 3)), { shown: lines.slice(0, 3), hidden: 0 });
  assert.deepEqual(capAttentionLines(null), { shown: [], hidden: 0 });
});

test('Needs you rows: mail first, then stations, then the stale-notes nudge (FR14)', () => {
  const { rows, total, more } = hqNeedsYouRows({
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
      },
      { key: 'watchtower', label: 'Watchtower', summary: ['3 signals'], action: 'Review' }
    ],
    notes: { stale: true, days: 9, cameDue: 2 }
  });
  assert.equal(total, 4);
  assert.equal(more, 0);
  assert.deepEqual(
    rows.map(row => [row.category, row.sentence]),
    [
      ['Email', 'Marco: Which mix? — He asked a question'],
      ['Calendar', 'Calendar Ops: 2 conflicts · Tomorrow'],
      ['Watchtower', 'Watchtower: 3 signals'],
      ['Notes', 'Your notes deserve another look: 2 items came due, the last look was 9 days ago.']
    ]
  );
  assert.deepEqual(rows[0].actions, [{ label: 'Reply', kind: 'mail', index: 0 }]);
  assert.deepEqual(rows[1].actions, [{ label: 'Review', kind: 'station', key: 'calendar-ops' }]);
  assert.deepEqual(rows[3].actions, [{ label: 'Look again', kind: 'notes' }]);
});

test('Needs you shows five rows and counts the rest; nothing is no rows', () => {
  const mail = Array.from({ length: 7 }, (_, index) => ({ subject: 'S' + index, index }));
  const result = hqNeedsYouRows({ mail });
  assert.equal(result.rows.length, NEEDS_YOU_LIMIT);
  assert.equal(result.total, 7);
  assert.equal(result.more, 2);
  assert.deepEqual(hqNeedsYouRows(), { rows: [], total: 0, more: 0 });
  assert.deepEqual(hqNeedsYouRows({ notes: { stale: false, cameDue: 0 } }).rows, []);
});

test('a long sentence is cut at a word', () => {
  const long = 'word '.repeat(60);
  const cut = attentionSentence(long);
  assert.ok(cut.length <= 160);
  assert.ok(cut.endsWith('word…'));
  assert.equal(attentionSentence('  two\n  lines  '), 'two lines');
});
