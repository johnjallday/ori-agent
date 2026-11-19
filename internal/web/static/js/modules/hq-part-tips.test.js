import { test } from 'node:test';
import assert from 'node:assert/strict';
import { HQ_LABELS, tacticalTermsIn } from './hq-labels.js';
import {
  FLOW_NODE_PARTS,
  FLOW_STATION_PARTS,
  MAP_HINT_PART,
  NOTES_PART,
  OVERVIEW_CARD_PARTS,
  partKeyForCard,
  partKeyForFlowNode,
  partTipsView,
  tipSentence,
  whatIsLabel
} from './hq-part-tips.js';

const created = { status: 'connected', source: { origin: 'created_by_ori' } };
const ownVault = { status: 'connected', source: { origin: 'obsidian_vault' } };

// Every part with a sentence, in each state that has one.
const ALL = [
  ['watchtower', {}],
  ['daily-brief', {}],
  ['memory', {}],
  [NOTES_PART, { notes: 'created' }],
  [NOTES_PART, { notes: 'own' }],
  [NOTES_PART, { notes: 'unset' }],
  ['email', { configured: false }],
  ['calendar-ops', { configured: false }],
  ['needs-you', {}],
  ['follow-ups', {}],
  ['tickets', {}],
  [MAP_HINT_PART, {}]
];

test('every sentence is plain: no tactical word, no work word the product avoids', () => {
  for (const [key, context] of ALL) {
    const sentence = tipSentence(key, context);
    assert.ok(sentence, key);
    assert.deepEqual(tacticalTermsIn(sentence), [], `${key}: ${sentence}`);
    assert.doesNotMatch(sentence, /\btasks?\b|second brain/i, `${key}: ${sentence}`);
  }
});

test('the sentences are the approved copy, one per part', () => {
  assert.equal(
    tipSentence('watchtower'),
    'Watches your other workspaces and tells you when one needs you.'
  );
  assert.equal(tipSentence('daily-brief'), 'A short read of your day, on the schedule you chose.');
  assert.equal(
    tipSentence('memory'),
    'What agents here remember between conversations. You can read and edit every entry.'
  );
  assert.equal(
    tipSentence(NOTES_PART, { notes: 'created' }),
    'A folder you own. I write there, you edit, and I never read your own notes back.'
  );
  assert.equal(
    tipSentence(NOTES_PART, { notes: 'own' }),
    'Your own vault. I read it and never write there.'
  );
  assert.equal(
    tipSentence(NOTES_PART, { notes: 'unset' }),
    'Off for now. Set it up and I keep what I learn in plain Markdown you own.'
  );
  assert.equal(
    tipSentence('email', { configured: false }),
    'Off for now. Set it up and your brief shows what is waiting on you.'
  );
  assert.equal(
    tipSentence('calendar-ops', { configured: false }),
    "Off for now. Connect it and your brief prepares you for today's meetings."
  );
  assert.equal(tipSentence('needs-you'), 'Anything waiting on your answer or review lands here.');
  assert.equal(
    tipSentence('follow-ups'),
    'Replies and promises to keep. I collect them so nothing slips.'
  );
  assert.equal(tipSentence('tickets'), 'Work handed to an agent. Open one to run it or review it.');
  assert.equal(
    tipSentence(MAP_HINT_PART),
    "Point at any part of the map and I'll tell you what it does."
  );
});

test('an unknown part has nothing to say', () => {
  assert.equal(tipSentence('nope'), '');
  assert.equal(tipSentence(''), '');
  assert.equal(tipSentence(undefined), '');
});

test('Email and Calendar Ops explain themselves only while they are not set up', () => {
  assert.equal(tipSentence('email', { configured: true }), '');
  assert.equal(tipSentence('calendar-ops', { configured: true }), '');
  assert.ok(tipSentence('email', { configured: false }));
  assert.ok(tipSentence('calendar-ops', { configured: false }));
});

test('nothing is returned unless the first visit is pending', () => {
  const parts = [{ key: 'watchtower' }, { key: 'daily-brief' }];
  for (const state of ['done', '', 'dismissed', 'never', undefined, null]) {
    assert.deepEqual(partTipsView({ state, parts }), { active: false, parts: [] }, String(state));
  }
  assert.deepEqual(partTipsView(), { active: false, parts: [] });
  assert.deepEqual(partTipsView({ state: 'done', parts, looked: {} }).parts, []);
});

test('a pending visit marks each part with a sentence until it is looked at', () => {
  const parts = [{ key: 'watchtower' }, { key: 'daily-brief' }, { key: 'memory' }];
  const first = partTipsView({ state: 'pending', parts });
  assert.equal(first.active, true);
  assert.deepEqual(
    first.parts.map(p => [p.key, p.marked]),
    [
      ['watchtower', true],
      ['daily-brief', true],
      ['memory', true]
    ]
  );
  assert.equal(first.parts[1].sentence, HQ_LABELS.tipDailyBrief);

  const afterOne = partTipsView({ state: 'pending', parts, looked: { 'daily-brief': true } });
  assert.deepEqual(
    afterOne.parts.map(p => [p.key, p.marked]),
    [
      ['watchtower', true],
      ['daily-brief', false],
      ['memory', true]
    ]
  );
  // A looked-at part keeps its sentence: hover and focus still show it.
  assert.equal(afterOne.parts[1].sentence, HQ_LABELS.tipDailyBrief);
});

test('"looked at" is read only as a map of part keys', () => {
  const parts = [{ key: 'memory' }];
  for (const looked of [null, undefined, 'memory', 3, []]) {
    assert.equal(partTipsView({ state: 'pending', parts, looked }).parts[0].marked, true);
  }
  assert.equal(
    partTipsView({ state: 'pending', parts, looked: { memory: 'yes' } }).parts[0].marked,
    true,
    'only `true` clears a mark'
  );
});

test('the Notes sentence follows the Notes the first visit reads', () => {
  const view = notes =>
    partTipsView({ state: 'pending', parts: [{ key: NOTES_PART }], notes }).parts[0].sentence;
  assert.equal(view(created), HQ_LABELS.tipNotesCreated);
  assert.equal(view(ownVault), HQ_LABELS.tipNotesOwn);
  assert.equal(view({ ...created, status: 'missing' }), HQ_LABELS.tipNotesUnset);
  assert.equal(view(null), HQ_LABELS.tipNotesUnset);
  assert.equal(view({}), HQ_LABELS.tipNotesUnset);
});

test('a part without a sentence is not listed, so it cannot carry a mark', () => {
  const view = partTipsView({
    state: 'pending',
    parts: [
      { key: 'email', configured: true },
      // No flag means connected: only an explicit `configured: false` is "off".
      { key: 'email' },
      { key: 'calendar-ops', configured: false },
      { key: 'unknown' },
      {}
    ]
  });
  assert.deepEqual(
    view.parts.map(p => p.key),
    ['calendar-ops']
  );
});

test('only the Overview cards with a sentence map to a part key', () => {
  assert.equal(partKeyForCard('watchtower'), 'watchtower');
  assert.equal(partKeyForCard('daily-brief'), 'daily-brief');
  assert.equal(partKeyForCard('memory'), 'memory');
  assert.equal(partKeyForCard('email'), 'email');
  assert.equal(partKeyForCard('calendar-ops'), 'calendar-ops');
  // The Notes station is a panel, not a card; inherited keys are not cards.
  assert.equal(partKeyForCard('second-brain'), '');
  assert.equal(partKeyForCard('toString'), '');
  assert.equal(partKeyForCard(''), '');
  assert.ok(Object.isFrozen(OVERVIEW_CARD_PARTS));
});

test('Flow nodes map to the same part keys the Overview uses', () => {
  const station = key => partKeyForFlowNode({ key, station: key });
  // Source nodes: the station is the key; the Notes station is `second-brain`.
  assert.equal(station('second-brain'), 'notes');
  assert.equal(station('watchtower'), 'watchtower');
  assert.equal(station('email'), 'email');
  assert.equal(station('calendar-ops'), 'calendar-ops');
  assert.equal(station('memory'), 'memory');
  // The right lane: the Daily Brief node is `brief` and also carries its station.
  assert.equal(partKeyForFlowNode({ key: 'brief', station: 'daily-brief' }), 'daily-brief');
  assert.equal(partKeyForFlowNode({ key: 'brief' }), 'daily-brief', 'also when not set up');
  assert.equal(partKeyForFlowNode({ key: 'needs-you' }), 'needs-you');
  assert.equal(partKeyForFlowNode({ key: 'follow-ups' }), 'follow-ups');
  assert.equal(partKeyForFlowNode({ key: 'tasks' }), 'tickets');
  // Not parts: the lead card (its hint is page markup), the unknown, the inherited.
  for (const node of [{ key: 'lead' }, { key: 'nope' }, { key: 'toString' }, {}, null, undefined])
    assert.equal(partKeyForFlowNode(node), '', JSON.stringify(node));
  assert.equal(partKeyForFlowNode({ key: 'x', station: 'toString' }), '');
  // One key per part: nothing maps two nodes onto the same anchor.
  assert.ok(Object.isFrozen(FLOW_STATION_PARTS) && Object.isFrozen(FLOW_NODE_PARTS));
  // Every key it can return has a sentence (a part with no words could carry no mark).
  for (const key of [...Object.values(FLOW_STATION_PARTS), ...Object.values(FLOW_NODE_PARTS)]) {
    assert.ok(tipSentence(key, { notes: 'created', configured: false }), key);
  }
});

test('on the Map every node has a sentence, and a not-set-up node says it is off', () => {
  const view = partTipsView({
    state: 'pending',
    notes: { status: 'connected', source: { origin: 'created_by_ori' } },
    parts: [
      { key: 'notes' },
      { key: 'email', configured: false },
      { key: 'calendar-ops', configured: false },
      { key: 'memory' },
      { key: 'needs-you' },
      { key: 'follow-ups' },
      { key: 'daily-brief' },
      { key: 'tickets' }
    ]
  });
  assert.deepEqual(
    view.parts.map(p => p.key),
    [
      'notes',
      'email',
      'calendar-ops',
      'memory',
      'needs-you',
      'follow-ups',
      'daily-brief',
      'tickets'
    ]
  );
  assert.ok(view.parts.every(p => p.marked));
});

test('the `?` button names the part it explains', () => {
  assert.equal(whatIsLabel('Daily Brief'), 'What is Daily Brief?');
  assert.equal(whatIsLabel('  Notes '), 'What is Notes?');
  assert.deepEqual(tacticalTermsIn(whatIsLabel('Memory')), []);
});
