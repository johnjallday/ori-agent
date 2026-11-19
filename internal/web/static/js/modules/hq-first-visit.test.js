import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  firstVisitView,
  firstVisitResolved,
  hqGreetingView,
  notesKind,
  FIRST_VISIT_MISSIONS
} from './hq-first-visit.js';

const notes = { status: 'connected', source: { origin: 'created_by_ori' } };
const missions = (note = 'open', brief = 'open', folder = 'open') =>
  Object.entries({ note, brief, folder, notes: 'completed' }).map(([key, status]) => ({
    id: FIRST_VISIT_MISSIONS[key],
    status
  }));
const view = (options = {}) =>
  firstVisitView({ state: 'pending', missions: missions(), notes, ...options });

test('suggestions offer Documents first and never gate the normal HQ', () => {
  const first = view();
  assert.equal(first.card, 'full');
  assert.deepEqual(
    first.steps.map(s => s.key),
    ['folder', 'brief', 'note']
  );
  assert.deepEqual(first.layout, {
    guided: false,
    notes: true,
    hideEmptyNeeds: false,
    railAllUnderMore: false,
    foldedStations: true,
    stationKeys: null,
    hideNotesChecklist: true
  });
  for (const status of ['open', 'completed', 'skipped'])
    assert.deepEqual(view({ missions: missions(status) }).layout, first.layout);
});

test('canonical completion and Skip resolve suggestions without claiming a completed action', () => {
  const all = missions('completed', 'completed', 'skipped');
  const result = view({ missions: all });
  assert.equal(result.card, 'finished');
  assert.equal(result.count, 3);
  assert.equal(result.steps[0].state, 'skipped');
  assert.equal(result.steps[0].action, 'folder');
  assert.equal(result.finishSilently, false);
  const returning = view({ missions: all, resolvedOnLoad: true });
  assert.equal(returning.card, 'none');
  assert.equal(returning.finishSilently, true);
  assert.equal(firstVisitResolved(all), true);
  assert.equal(firstVisitResolved(null), false);
  assert.equal(firstVisitResolved([]), false);
});

test('old browser presentation preferences cannot open suggestions or enroll existing HQs', () => {
  assert.equal(view({ showAll: true }).card, 'full', 'obsolete bar preference is ignored');
  for (const state of ['', 'done']) {
    const result = view({ state, showAll: true });
    assert.equal(result.card, 'none');
    assert.equal(result.finishSilently, false);
    assert.equal(result.layout.hideNotesChecklist, false);
  }
});

test('loading cannot manufacture ticks or finish; mission tiers never block real actions', () => {
  const result = view({ missions: null });
  assert.equal(result.count, 0);
  assert.ok(result.steps.every(s => s.disabled && !s.resolved));
  assert.equal(result.finishSilently, false);
  assert.ok(
    view({ missions: missions('locked-tier', 'locked-tier') }).steps.every(s => !s.disabled)
  );
});

test('a note suggestion points to the ordinary capture, including after natural completion', () => {
  assert.equal(view().steps[2].action, 'note');
  const done = view({ missions: missions('completed') }).steps[2];
  assert.equal(done.action, 'note');
  assert.equal(done.hint, 'Your first note is in your Inbox.');
});

test('unavailable or personal vaults point to connection management, never a second editor', () => {
  for (const state of [{ status: 'not_connected' }, { ...notes, status: 'missing' }]) {
    const step = view({ notes: state }).steps[2];
    assert.equal(step.action, 'notes');
    assert.equal(step.title, 'Set up your notes');
  }
  const own = view({ notes: { source: { origin: 'connected' } }, missions: missions('skipped') })
    .steps[2];
  assert.equal(own.action, 'notes');
  assert.equal(own.hint, 'Your own vault is connected. Ori does not write there.');
  assert.equal(view({ notes: null }).steps[2].disabled, true);
});

test('notesKind names the Notes the suggestions and the tips both read', () => {
  assert.equal(notesKind({ status: 'connected', source: { origin: 'created_by_ori' } }), 'created');
  assert.equal(notesKind({ status: 'missing', source: { origin: 'created_by_ori' } }), 'unset');
  assert.equal(notesKind({ source: { origin: 'connected' } }), 'own');
  assert.equal(
    notesKind({ source: {} }),
    'own',
    'a source that is not Ori-created is the own vault'
  );
  assert.equal(notesKind({ status: 'unset' }), 'unset');
  assert.equal(notesKind(null), 'unset');
  assert.equal(notesKind(undefined), 'unset');
});

const card = (label, stateWord) => ({ label, stateWord });
const greeting = (options = {}) =>
  hqGreetingView({
    state: 'pending',
    missions: missions(),
    notes,
    cards: [card('Watchtower', 'Ready'), card('Daily Brief', 'Ready'), card('Memory', 'Ready')],
    folded: [card('Email', 'Not set up'), card('Calendar Ops', 'Not set up')],
    ...options
  });

test('the greeting is nothing unless the visit is pending and its progress was read', () => {
  for (const state of ['done', '', 'dismissed', undefined])
    assert.deepEqual(greeting({ state }), { show: false }, String(state));
  assert.deepEqual(greeting({ failed: true }), { show: false }, 'progress could not be read');
  // Not known yet is not "nothing": the drawer waits instead of guessing.
  assert.equal(greeting({ missions: null }), null);
  assert.equal(greeting({ missions: 'x' }), null);
  assert.equal(greeting({ notes: null }), null, 'it needs the Notes to name what runs');
  assert.deepEqual(hqGreetingView(), { show: false });
});

test('the greeting is gone once all three suggestions are resolved', () => {
  assert.deepEqual(greeting({ missions: missions('completed', 'completed', 'completed') }), {
    show: false
  });
  assert.deepEqual(greeting({ missions: missions('skipped', 'completed', 'skipped') }), {
    show: false
  });
  const some = greeting({ missions: missions('completed', 'open', 'open') });
  assert.equal(some.show, true, 'two still open: still greeting');
});

test('the greeting names what runs and what is off, in page order, Notes first', () => {
  const view = greeting();
  assert.equal(view.show, true);
  assert.deepEqual(view.running, ['Notes', 'Watchtower', 'Daily Brief', 'Memory']);
  assert.deepEqual(view.off, ['Email', 'Calendar Ops']);
  // The three existing suggestions, untouched: same titles and hints, same owners.
  assert.deepEqual(
    view.steps.map(step => [step.key, step.action, step.title]),
    [
      ['folder', 'folder', 'Explore Documents'],
      ['brief', 'brief', 'Read your Daily Brief'],
      ['note', 'note', 'Save an idea']
    ]
  );
  assert.deepEqual(
    view.steps,
    firstVisitView({ state: 'pending', missions: missions(), notes }).steps
  );
});

test('Notes runs for a folder Ori set up and for the own vault, and is off when unset', () => {
  const kinds = [
    [notes, 'running'],
    [{ status: 'connected', source: { origin: 'obsidian_vault' } }, 'running'],
    [{ status: 'missing', source: { origin: 'created_by_ori' } }, 'off'],
    [{ status: 'unset' }, 'off']
  ];
  for (const [source, where] of kinds) {
    const view = greeting({ notes: source });
    assert.equal(view.running.includes('Notes'), where === 'running', JSON.stringify(source));
    assert.equal(view.off.includes('Notes'), where === 'off', JSON.stringify(source));
  }
  assert.equal(
    greeting({ notes: { status: 'unset' } }).off[0],
    'Notes',
    'Notes leads the off line'
  );
});

test('a card that needs you runs; one still being checked, or in error, is in neither line', () => {
  const view = greeting({
    cards: [
      card('Watchtower', 'Needs you'),
      card('Daily Brief', 'Running'),
      card('Memory', 'Checking'),
      card('Goal', 'Error')
    ],
    folded: []
  });
  assert.deepEqual(view.running, ['Notes', 'Watchtower', 'Daily Brief']);
  assert.deepEqual(view.off, []);
});

test('with nothing folded and nothing drawn the lines are empty, not invented', () => {
  const view = greeting({ cards: [], folded: [], notes: { status: 'unset' } });
  assert.deepEqual(view.running, []);
  assert.deepEqual(view.off, ['Notes']);
  const bare = hqGreetingView({ state: 'pending', missions: missions(), notes });
  assert.deepEqual(bare.running, ['Notes']);
  assert.deepEqual(bare.off, []);
});

test('brief and folder use canonical results, never the chooser or a browser completion flag', () => {
  const result = view({ missions: missions('open', 'completed', 'completed') });
  assert.equal(result.steps[1].action, 'brief');
  assert.equal(result.steps[1].hint, 'Your first brief is ready.');
  assert.equal(result.steps[0].action, 'home');
  assert.equal(result.steps[0].hint, 'Your folder is on the map.');
});
