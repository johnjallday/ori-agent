import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  firstVisitView,
  firstVisitResolved,
  firstVisitShowAll,
  rememberFirstVisitShowAll,
  FIRST_VISIT_KEY,
  FIRST_VISIT_MISSIONS
} from './hq-first-visit.js';
import { JOURNEY_STEPS } from './home-welcome.js';

const notes = { status: 'connected', source: { origin: 'created_by_ori' } };
const missions = (note = 'open', brief = 'open', folder = 'open') =>
  Object.entries({ note, brief, folder, notes: 'completed' }).map(([key, status]) => ({
    id: FIRST_VISIT_MISSIONS[key],
    status
  }));
const view = (options = {}) =>
  firstVisitView({ state: 'pending', missions: missions(), notes, ...options });

test('pending first visit reveals only completed notes and resolved stations', () => {
  const first = view();
  assert.equal(first.card, 'full');
  assert.equal(first.count, 0);
  assert.equal(first.journey, JOURNEY_STEPS);
  assert.deepEqual(first.layout, {
    guided: true,
    notes: false,
    hideEmptyNeeds: true,
    railAllUnderMore: true,
    foldedStations: false,
    stationKeys: [],
    hideNotesChecklist: true
  });
  const one = view({ missions: missions('completed') });
  assert.equal(one.layout.notes, true);
  assert.equal(one.count, 1);
  assert.deepEqual(
    view({ missions: missions('open', 'completed', 'skipped') }).layout.stationKeys,
    ['daily-brief', 'watchtower']
  );
  assert.equal(view({ missions: missions('skipped') }).layout.notes, false);
});

test('show everything keeps a bar but uses the quiet layout', () => {
  const result = view({ showAll: true });
  assert.equal(result.card, 'bar');
  assert.equal(result.layout.guided, false);
  assert.equal(result.layout.notes, true);
  assert.equal(result.layout.foldedStations, true);
  assert.equal(result.layout.stationKeys, null);
  assert.equal(result.layout.hideNotesChecklist, true);
});

test('finishing during the visit shows the finished card; pre-resolved visits finish silently', () => {
  const all = missions('completed', 'completed', 'completed');
  for (const showAll of [false, true]) {
    const result = view({ missions: all, showAll });
    assert.equal(result.card, 'finished');
    assert.equal(result.count, 3);
    assert.equal(result.layout.guided, false);
    assert.equal(result.layout.hideNotesChecklist, false);
    assert.equal(result.finishSilently, false);
    const returning = view({ missions: all, showAll, resolvedOnLoad: true });
    assert.equal(returning.card, 'none');
    assert.equal(returning.finishSilently, true);
  }
  assert.equal(view({ missions: missions('skipped', 'completed', 'completed') }).card, 'finished');
});

test('done and never-offered HQs do not acquire the card from browser preferences', () => {
  for (const state of ['', 'done']) {
    const result = view({ state, showAll: true });
    assert.equal(result.card, 'none');
    assert.equal(result.layout.guided, false);
    assert.equal(result.finishSilently, false);
    assert.equal(result.layout.hideEmptyNeeds, false);
    assert.equal(result.layout.railAllUnderMore, false);
  }
});

test('loading cannot manufacture ticks or silently finish; tier locks do not disable real actions', () => {
  const result = view({ missions: null });
  assert.equal(result.card, 'full');
  assert.equal(result.count, 0);
  assert.ok(result.steps.every(s => s.disabled && !s.resolved));
  assert.equal(result.finishSilently, false);
  const locked = missions('locked-tier', 'locked-tier');
  locked.forEach(m => {
    m.locked = true;
  });
  assert.ok(view({ missions: locked }).steps.every(s => !s.disabled));
});

test('note step saves into Ori notes and distinguishes current-visit receipt from later visits', () => {
  assert.equal(view().steps[0].action, 'save');
  assert.equal(view().steps[0].title, 'Save a first note');
  assert.equal(
    view({ missions: missions('completed'), savedText: '<a note>' }).steps[0].hint,
    'Saved to your Inbox: “<a note>”'
  );
  assert.equal(
    view({ missions: missions('completed') }).steps[0].hint,
    'Your first note is in your Inbox.'
  );
  assert.equal(view({ missions: missions('completed') }).steps[0].action, '');
});

test('note setup and skipped variants never offer a write to someone else’s vault', () => {
  const setup = view({ notes: { status: 'not_connected' } }).steps[0];
  assert.equal(setup.title, 'Set up your notes');
  assert.equal(setup.button, 'Set up notes');
  assert.equal(setup.action, 'notes');
  const skipped = view({ missions: missions('skipped'), notes: { status: 'declined' } }).steps[0];
  assert.equal(skipped.hint, 'Notes are off. Set them up any time.');
  assert.equal(skipped.action, 'notes');
  const own = view({ missions: missions('skipped'), notes: { source: { origin: 'connected' } } })
    .steps[0];
  assert.equal(own.hint, 'Your own vault is connected. Ori does not write there.');
  assert.equal(own.action, '');
  assert.equal(view({ notes: { ...notes, status: 'missing' } }).steps[0].action, 'notes');
});

test('brief and folder actions follow canonical outcomes, not presentation locks', () => {
  assert.equal(view().steps[1].button, 'Write my first brief');
  assert.equal(view({ hasBrief: true }).steps[1].button, 'Read your first brief');
  const result = view({ missions: missions('open', 'completed', 'completed') });
  assert.equal(result.steps[1].button, 'Open it');
  assert.equal(result.steps[1].action, 'brief');
  assert.equal(result.steps[2].button, 'Open Home');
  assert.equal(result.steps[2].action, 'home');
  assert.equal(firstVisitResolved(missions('skipped', 'completed', 'completed')), true);
  assert.equal(firstVisitResolved(null), false);
  assert.equal(firstVisitResolved([]), false);
});

test('footer uses seeded non-setup ticket titles only, bounded to three', () => {
  const starter = description => ({ description, context: { template_starter_task: true } });
  const result = view({
    tickets: [
      { description: 'Setup', context: { template_starter_task: true, template_setup: true } },
      { description: 'Ordinary ticket' },
      starter('Morning briefing'),
      starter('End-of-day journal')
    ]
  });
  assert.deepEqual(result.ticketTitles, ['Morning briefing', 'End-of-day journal']);
  assert.equal(
    result.footer,
    'I also added two tickets you can run when you like: Morning briefing and End-of-day journal.'
  );
  assert.equal(view().footer, '');
  assert.equal(view({ tickets: ['A', 'B', 'C', 'D'].map(starter) }).ticketTitles.length, 3);
});

test('show-all preference survives reload, is workspace scoped and tolerates unavailable storage', () => {
  const values = new Map();
  const storage = {
    getItem: key => values.get(key),
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key)
  };
  assert.equal(firstVisitShowAll(storage, 'hq'), false);
  rememberFirstVisitShowAll(storage, 'hq', true);
  assert.deepEqual(JSON.parse(values.get(FIRST_VISIT_KEY)), { workspaceId: 'hq', showAll: true });
  assert.equal(view({ showAll: firstVisitShowAll(storage, 'hq') }).card, 'bar');
  assert.equal(firstVisitShowAll(storage, 'other'), false);
  rememberFirstVisitShowAll(storage, 'hq', false);
  assert.equal(view({ showAll: firstVisitShowAll(storage, 'hq') }).card, 'full');
  const broken = {
    getItem() {
      throw Error('blocked');
    },
    setItem() {
      throw Error('quota');
    },
    removeItem() {
      throw Error('blocked');
    }
  };
  assert.equal(firstVisitShowAll(broken, 'hq'), false);
  assert.doesNotThrow(() => rememberFirstVisitShowAll(broken, 'hq', true));
  assert.doesNotThrow(() => rememberFirstVisitShowAll(broken, 'hq', false));
  values.set(FIRST_VISIT_KEY, '{');
  assert.equal(firstVisitShowAll(storage, 'hq'), false);
});
