import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  firstVisitView,
  firstVisitResolved,
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

test('brief and folder use canonical results, never the chooser or a browser completion flag', () => {
  const result = view({ missions: missions('open', 'completed', 'completed') });
  assert.equal(result.steps[1].action, 'brief');
  assert.equal(result.steps[1].hint, 'Your first brief is ready.');
  assert.equal(result.steps[0].action, 'home');
  assert.equal(result.steps[0].hint, 'Your folder is on the map.');
});
