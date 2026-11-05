import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  STATE_WORDS,
  hqStationCards,
  stationCard,
  stationStateWord,
  stationsNeedingYou
} from './hq-stations-grid.js';

test('each tone maps to its state word', () => {
  assert.equal(stationStateWord({ value: 'All clear', tone: 'clear' }), 'Ready');
  assert.equal(stationStateWord({ value: '3 signals', tone: 'attention' }), 'Needs you');
  assert.equal(stationStateWord({ value: 'Unavailable', tone: 'degraded' }), 'Error');
  assert.equal(stationStateWord({ value: 'Ready · 7:00', tone: '' }), 'Ready');
  assert.equal(stationStateWord(null), 'Ready');
});

test('a station saying it is not set up is Not set up, whatever its tone', () => {
  // Email's portal with no Email Ops workspace.
  assert.equal(
    stationStateWord({ value: 'Set up Email Ops', description: 'no Email Ops workspace yet' }),
    'Not set up'
  );
  // Calendar Ops asks with an attention tone.
  assert.equal(
    stationStateWord({
      value: 'Set up',
      description: 'Calendar Ops is not set up yet',
      tone: 'attention'
    }),
    'Not set up'
  );
  assert.equal(
    stationStateWord({ value: 'Not set up', description: 'set up your notes' }),
    'Not set up'
  );
  // Half-way is not "not set up": it needs the user.
  assert.equal(
    stationStateWord({ value: 'Finish setup', description: 'one step left', tone: 'attention' }),
    'Needs you'
  );
});

test('loading is Running when the station says work is under way, else Checking', () => {
  assert.equal(
    stationStateWord({
      value: 'Preparing…',
      description: 'the Daily Brief is being prepared',
      tone: 'loading'
    }),
    'Running'
  );
  assert.equal(
    stationStateWord({
      value: 'Scanning…',
      description: 'loading attention queue',
      tone: 'loading'
    }),
    'Checking'
  );
  assert.equal(
    stationStateWord({
      value: 'Loading…',
      description: 'loading calendar summary',
      tone: 'loading'
    }),
    'Checking'
  );
});

test('a card carries the name, state word, two summary lines and one action', () => {
  assert.deepEqual(
    stationCard({
      key: 'daily-brief',
      label: 'Daily Brief',
      state: {
        value: 'Ready · 7:00 AM',
        description: 'the Daily Brief for today is ready',
        tone: 'clear'
      }
    }),
    {
      key: 'daily-brief',
      label: 'Daily Brief',
      stateWord: 'Ready',
      summary: ['Ready · 7:00 AM', 'The Daily Brief for today is ready'],
      action: 'Open',
      configured: true,
      tone: ''
    }
  );
  const email = stationCard({ key: 'email', label: 'Email', state: { value: 'Set up Email Ops' } });
  assert.equal(email.action, 'Set up');
  assert.equal(email.configured, false);
  assert.deepEqual(email.summary, ['Set up Email Ops']);
  assert.equal(
    stationCard({ key: 'w', label: 'W', state: { value: '2 signals', tone: 'attention' } }).action,
    'Review'
  );
  assert.equal(
    stationCard({ key: 'w', label: 'W', state: { value: 'x', tone: 'degraded' } }).tone,
    'error'
  );
  // The same words twice are said once.
  assert.deepEqual(
    stationCard({ key: 'm', label: 'M', state: { value: 'Memory', description: 'memory' } })
      .summary,
    ['Memory']
  );
});

test('cards sort Needs you, then Ready / Running, then Not set up, stable by registry order', () => {
  const stations = [
    { key: 'watchtower', label: 'Watchtower', state: { value: 'All clear', tone: 'clear' } },
    { key: 'email', label: 'Email', state: { value: 'Set up Email Ops' } },
    {
      key: 'calendar-ops',
      label: 'Calendar Ops',
      state: { value: 'Set up', description: 'Calendar Ops is not set up yet', tone: 'attention' }
    },
    { key: 'daily-brief', label: 'Daily Brief', state: { value: 'Preparing…', tone: 'loading' } },
    { key: 'memory', label: 'Memory', state: { value: '+3 new', tone: 'attention' } },
    { key: 'second-brain', label: 'Notes', state: { value: '4 open', tone: 'clear' } },
    { key: 'janitor', label: 'File Janitor', state: { value: 'Unavailable', tone: 'degraded' } }
  ];
  const cards = hqStationCards(stations);
  assert.deepEqual(
    cards.map(card => [card.key, card.stateWord]),
    [
      ['memory', 'Needs you'],
      ['janitor', 'Error'],
      ['watchtower', 'Ready'],
      ['daily-brief', 'Running'],
      ['email', 'Not set up'],
      ['calendar-ops', 'Not set up']
    ]
  );
  assert.ok(!cards.some(card => card.key === 'second-brain'), 'Notes is its own panel');
  assert.deepEqual(
    stationsNeedingYou(cards).map(card => card.key),
    ['memory'],
    'an Error is shown on its card, not counted as needing the user'
  );
  assert.deepEqual(hqStationCards(null), []);
  assert.equal(STATE_WORDS.notSetUp, 'Not set up');
});
