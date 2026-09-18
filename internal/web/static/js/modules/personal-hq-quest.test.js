// Tests for personal-hq-quest.js — Ori's deterministic Personal HQ walkthrough.
//   node --test internal/web/static/js/modules/personal-hq-quest.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const questSrc = readFileSync(new URL('./personal-hq-quest.js', import.meta.url), 'utf8');

// A relationship that is hired with no home base yet, plus a known-invalid HQ
// and finished onboarding: the only combination the walkthrough may run in.
function eligibleResponses({
  state = 'needs_hq',
  displayName = 'Atlas',
  needsOnboarding = false,
  hqValid = false
} = {}) {
  return {
    '/api/onboarding/status': { needs_onboarding: needsOnboarding },
    '/api/personal-assistant': {
      personal_assistant: { state, display_name: displayName, state_version: 2 }
    },
    '/api/personal-hq/status': { status: { valid: hqValid, hq_onboarding_state: 'unseen' } }
  };
}

function load({
  search = '',
  responses = eligibleResponses(),
  guideOverrides = {},
  // Ori's blocking layer (ori-spotlight.js), and whether it finds and fits
  // what it is asked to show.
  layer = false,
  layerFits = true
} = {}) {
  const calls = {
    fetches: [],
    setView: [],
    presented: [],
    cleared: 0,
    opened: 0,
    layer: [],
    layerClosed: 0
  };
  const dispatched = [];
  const listeners = { window: {}, document: {} };

  const guide = {
    open: (trigger, options) => {
      calls.opened += 1;
      calls.openOptions = options;
    },
    isOpen: () => false,
    presentQuestStep: step => {
      calls.presented.push(step);
      return { rendered: true, coachmarkResolved: true };
    },
    clearQuestStep: () => {
      calls.cleared += 1;
    },
    ...guideOverrides
  };

  const sandbox = {
    console: { warn() {}, error() {}, debug() {} },
    URL,
    URLSearchParams,
    Promise,
    Error,
    Object,
    String,
    Number,
    Array,
    JSON,
    CustomEvent: function CustomEvent(type, init) {
      this.type = type;
      this.detail = (init && init.detail) || {};
    },
    fetch: url => {
      calls.fetches.push(String(url));
      const body = responses[String(url)];
      if (body === undefined) return Promise.resolve({ ok: false, status: 500 });
      return Promise.resolve({ ok: true, json: () => Promise.resolve(body) });
    }
  };
  sandbox.window = {
    location: { href: 'http://localhost/' + search, search, pathname: '/' },
    history: { state: null, replaceState: () => {} },
    OriGuide: guide,
    OriHomeCockpit: {
      setView: (view, options) => calls.setView.push({ view, options })
    },
    addEventListener: (type, fn) => {
      listeners.window[type] = listeners.window[type] || [];
      listeners.window[type].push(fn);
    },
    dispatchEvent: event => {
      dispatched.push({ type: event.type, detail: event.detail });
      (listeners.window[event.type] || []).forEach(fn => fn(event));
      return true;
    }
  };
  sandbox.document = {
    readyState: 'complete',
    addEventListener: (type, fn) => {
      listeners.document[type] = listeners.document[type] || [];
      listeners.document[type].push(fn);
    },
    dispatchEvent: event => {
      dispatched.push({ type: event.type, detail: event.detail });
      (listeners.document[event.type] || []).forEach(fn => fn(event));
      return true;
    }
  };
  if (layer) {
    const show = kind => opts => {
      calls.layer.push({ kind, ...opts });
      return Promise.resolve(layerFits);
    };
    sandbox.window.OriSpotlight = {
      showBriefing: opts => {
        calls.layer.push({ kind: 'briefing', ...opts });
      },
      showSpotlight: show('spotlight'),
      showCallout: show('callout'),
      close: () => {
        calls.layerClosed += 1;
      }
    };
  }
  sandbox.globalThis = sandbox;
  sandbox.fetch = sandbox.fetch.bind(sandbox);
  vm.createContext(sandbox);
  vm.runInContext(questSrc, sandbox);

  return {
    quest: sandbox.window.OriPersonalHQQuest,
    calls,
    dispatched,
    sandbox,
    fireWindow(type, detail) {
      (listeners.window[type] || []).forEach(fn => fn({ type, detail: detail || {} }));
    },
    fireDocument(type, detail) {
      (listeners.document[type] || []).forEach(fn => fn({ type, detail: detail || {} }));
    }
  };
}

// settle lets the controller's awaited fetches resolve.
const settle = () => new Promise(resolve => setImmediate(resolve));

/* ---- activation ---------------------------------------------------------- */

test('the walkthrough does not start without the quest route', async () => {
  const { quest, calls } = load({ search: '' });
  await settle();
  assert.equal(quest.isActive(), false);
  assert.deepEqual(calls.presented, []);
  assert.deepEqual(calls.fetches, [], 'no eligibility request without the quest parameter');
});

test('the Email Ops setup quest link never starts the Build-HQ walkthrough', async () => {
  // Both use a `quest` parameter; only the exact Build-HQ value may start it.
  const { quest, calls } = load({ search: '?setup=quest&source=host&quest=email_ops_setup' });
  await settle();
  assert.equal(quest.isActive(), false);
  assert.deepEqual(calls.presented, []);
  assert.deepEqual(calls.fetches, [], 'no eligibility request for another quest link');
});

test('the walkthrough starts on the quest route for a hired assistant with no HQ', async () => {
  const { quest, calls } = load({ search: '?quest=build-hq' });
  await settle();
  assert.equal(quest.isActive(), true);
  assert.equal(calls.presented.length, 1);

  const step = calls.presented[0];
  assert.equal(step.quest, 'build-hq');
  assert.equal(step.index, 1);
  assert.equal(step.total, 3);
  assert.equal(step.coachmark, 'personal_hq_site');
  // Approved framing, naming the hired assistant as the subject.
  assert.match(step.answer, /Atlas is hired\./);
  assert.match(step.answer, /a home base/);
  // Element-wise: arrays created inside the vm context have a different
  // prototype, so deepEqual would reject a structurally identical value.
  assert.equal(step.choices.length, 1);
  assert.equal(step.choices[0].id, 'defer');
  assert.equal(step.choices[0].label, 'Do this later');
});

test('opening the guide skips its default greeting, since the walkthrough presents its own step immediately after', async () => {
  const { calls } = load({ search: '?quest=build-hq' });
  await settle();
  assert.equal(calls.opened, 1);
  // Without this, ori-guide.js's own async default-greeting fetch would land
  // after present() and silently overwrite the quest step.
  assert.equal(calls.openOptions && calls.openOptions.skipGreeting, true);
});

test('the walkthrough forces Map view without rewriting the URL or history', async () => {
  const { calls } = load({ search: '?quest=build-hq' });
  await settle();
  assert.equal(calls.setView.length, 1);
  assert.equal(calls.setView[0].view, 'map');
  assert.equal(calls.setView[0].options.pushUrl, false);
});

test('the quest route is not authorization: every ineligible state is refused', async () => {
  const ineligible = [
    ['onboarding unfinished', eligibleResponses({ needsOnboarding: true })],
    ['already active', eligibleResponses({ state: 'active' })],
    ['never hired', eligibleResponses({ state: 'needs_hire' })],
    ['mid-hire', eligibleResponses({ state: 'hiring' })],
    ['relationship needs repair', eligibleResponses({ state: 'repair_needed' })],
    ['HQ already valid', eligibleResponses({ hqValid: true })]
  ];
  for (const [name, responses] of ineligible) {
    const { quest, calls } = load({ search: '?quest=build-hq', responses });
    await settle();
    assert.equal(quest.isActive(), false, `${name} started the walkthrough`);
    assert.deepEqual(calls.presented, [], `${name} presented a step`);
  }
});

test('an unreadable HQ status refuses to claim the HQ is missing', async () => {
  const responses = eligibleResponses();
  delete responses['/api/personal-hq/status'];
  const { quest } = load({ search: '?quest=build-hq', responses });
  await settle();
  assert.equal(quest.isActive(), false);
});

test('resume applies the same server-side eligibility checks', async () => {
  const { quest } = load({ search: '', responses: eligibleResponses({ state: 'active' }) });
  await settle();
  assert.equal(await quest.resume(), false);

  const eligible = load({ search: '' });
  await settle();
  assert.equal(await eligible.quest.resume(), true);
  assert.equal(eligible.calls.presented.length, 1);
});

/* ---- step advancement ---------------------------------------------------- */

test('the walkthrough waits for a real user selection before step 2', async () => {
  const { quest, calls, fireWindow } = load({ search: '?quest=build-hq' });
  await settle();
  assert.equal(calls.presented.length, 1);

  // The site is selected but its dialog has not rendered: there is no Build
  // action to mark yet, so the step is re-presented rather than advanced.
  fireWindow('ori:hq-site-selected', { dialogOpened: false });
  assert.equal(calls.presented.at(-1).index, 1);

  fireWindow('ori:hq-site-selected', { dialogOpened: true });
  const step2 = calls.presented.at(-1);
  assert.equal(step2.index, 2);
  assert.equal(step2.coachmark, 'personal_hq_build');
  assert.match(step2.answer, /open Build My HQ/);
  assert.match(step2.answer, /Atlas/);
  assert.equal(quest.isActive(), true);
});

test('opening the build form advances to the confirmation explanation', async () => {
  const { calls, fireWindow } = load({ search: '?quest=build-hq' });
  await settle();
  fireWindow('ori:hq-site-selected', { dialogOpened: true });
  fireWindow('ori:hq-quest-signal', { stage: 'build-form-opened' });

  const step3 = calls.presented.at(-1);
  assert.equal(step3.index, 3);
  // The existing form owns focus at this point, so nothing is marked.
  assert.equal(step3.coachmark, '');
  assert.match(step3.note, /Nothing is created until you confirm/);
  // Do this later is withdrawn once the form is open; Cancel is the honest exit.
  assert.equal(step3.choices.length, 0);
});

test('steps never run backwards on a repeated event', async () => {
  const { calls, fireWindow } = load({ search: '?quest=build-hq' });
  await settle();
  fireWindow('ori:hq-site-selected', { dialogOpened: true });
  fireWindow('ori:hq-quest-signal', { stage: 'build-form-opened' });
  assert.equal(calls.presented.at(-1).index, 3);

  fireWindow('ori:hq-site-selected', { dialogOpened: true });
  assert.equal(calls.presented.at(-1).index, 3, 'a stale selection rewound the walkthrough');
});

/* ---- resilience --------------------------------------------------------- */

test('a Map remount re-presents the current step instead of leaving a stale mark', async () => {
  const { calls, fireWindow } = load({ search: '?quest=build-hq' });
  await settle();
  fireWindow('ori:hq-site-selected', { dialogOpened: true });
  const before = calls.presented.length;

  fireWindow('ori:hq-quest-signal', { stage: 'hq-status-changed', valid: false });
  assert.equal(calls.presented.length, before + 1);
  assert.equal(calls.presented.at(-1).index, 2, 'the current step should be re-presented');

  fireWindow('ori:workspaces-changed', {});
  assert.equal(calls.presented.at(-1).index, 2);
});

test('a now-valid HQ ends the walkthrough', async () => {
  const { quest, calls, fireWindow } = load({ search: '?quest=build-hq' });
  await settle();
  fireWindow('ori:hq-quest-signal', { stage: 'hq-status-changed', valid: true });
  assert.equal(quest.isActive(), false);
  assert.equal(calls.cleared, 1);
});

test('a successful setup ends the walkthrough without claiming the quest itself', async () => {
  const { quest, calls, dispatched, fireWindow } = load({ search: '?quest=build-hq' });
  await settle();
  fireWindow('ori:hq-quest-signal', { stage: 'setup-succeeded' });
  assert.equal(quest.isActive(), false);
  assert.equal(calls.cleared, 1);
  // No skip, no completion: the server owns the quest record.
  assert.equal(dispatched.filter(event => event.type === 'ori:personal-hq-action').length, 0);
});

test('Back and page-hide clear the coachmark without skipping the quest', async () => {
  for (const event of ['popstate', 'pagehide']) {
    const { quest, calls, dispatched, fireWindow } = load({ search: '?quest=build-hq' });
    await settle();
    fireWindow(event, {});
    assert.equal(quest.isActive(), false, `${event} left the walkthrough active`);
    assert.equal(calls.cleared, 1, `${event} left a stale coachmark`);
    assert.equal(
      dispatched.filter(item => item.type === 'ori:personal-hq-action').length,
      0,
      `${event} was recorded as a deferral`
    );
  }
});

test('stopping the walkthrough never reports completion or deferral', async () => {
  const { quest, dispatched } = load({ search: '?quest=build-hq' });
  await settle();
  quest.stop();
  assert.equal(quest.isActive(), false);
  assert.deepEqual(
    dispatched.filter(event => event.type.startsWith('ori:personal-hq')),
    []
  );
});

/* ---- Do this later ------------------------------------------------------ */

test('Do this later invokes the existing skip path exactly once', async () => {
  const { quest, calls, dispatched, fireDocument } = load({ search: '?quest=build-hq' });
  await settle();
  fireDocument('ori-guide:quest-choice', { quest: 'build-hq', choice: 'defer' });

  const skips = dispatched.filter(
    event => event.type === 'ori:personal-hq-action' && event.detail.action === 'skip'
  );
  assert.equal(skips.length, 1, 'defer must reuse the one existing skip path');
  assert.equal(quest.isActive(), false);
  assert.equal(calls.cleared, 1, 'deferring must clear the coachmark');
});

test('a choice for another quest or an unknown id is ignored', async () => {
  const { quest, dispatched, fireDocument } = load({ search: '?quest=build-hq' });
  await settle();
  fireDocument('ori-guide:quest-choice', { quest: 'some-other-quest', choice: 'defer' });
  fireDocument('ori-guide:quest-choice', { quest: 'build-hq', choice: 'not-a-choice' });
  assert.equal(quest.isActive(), true);
  assert.deepEqual(
    dispatched.filter(event => event.type === 'ori:personal-hq-action'),
    []
  );
});

/* ---- boundaries --------------------------------------------------------- */

test('the walkthrough never asks the guide API or a model', async () => {
  const { calls, fireWindow } = load({ search: '?quest=build-hq' });
  await settle();
  fireWindow('ori:hq-site-selected', { dialogOpened: true });
  fireWindow('ori:hq-quest-signal', { stage: 'build-form-opened' });

  for (const url of calls.fetches) {
    assert.doesNotMatch(url, /ori-guide/, 'a fixed step called the guide API');
    assert.doesNotMatch(url, /home-assistant/, 'a fixed step called the routing API');
  }
  // Only the three read-only eligibility checks, and nothing repeated per step.
  assert.deepEqual(calls.fetches, [
    '/api/onboarding/status',
    '/api/personal-assistant',
    '/api/personal-hq/status'
  ]);
});

test('the walkthrough makes no mutating request of its own', async () => {
  const requests = [];
  const { fireWindow, sandbox } = load({ search: '?quest=build-hq' });
  await settle();
  sandbox.fetch = (url, init) => {
    requests.push({ url: String(url), method: (init && init.method) || 'GET' });
    return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
  };
  fireWindow('ori:hq-site-selected', { dialogOpened: true });
  fireWindow('ori:hq-quest-signal', { stage: 'build-form-opened' });
  assert.deepEqual(
    requests.filter(request => request.method !== 'GET'),
    []
  );
});

test('every step is copy plus at most one registered coachmark key', async () => {
  const { quest } = load({ search: '' });
  for (const step of [quest.STEP_SELECT_SITE, quest.STEP_OPEN_BUILD, quest.STEP_CONFIRM]) {
    const key = quest._coachmarkFor(step);
    assert.ok(
      key === '' || ['personal_hq_site', 'personal_hq_build'].includes(key),
      `step ${step} names an unregistered coachmark ${key}`
    );
    // A key is a typed token, never a selector.
    for (const bad of ['#', '.', '[', ' ', '>']) {
      assert.ok(!key.includes(bad), `step ${step} coachmark looks like a selector`);
    }
  }
});

test('copy falls back to a neutral subject when the name is missing', async () => {
  const { quest } = load({ search: '' });
  const copy = quest._stepCopy(quest.STEP_SELECT_SITE, '');
  assert.match(copy.answer, /Your assistant is hired\./);
  assert.doesNotMatch(copy.answer, /undefined|null/);
});

test('the walkthrough works with no model configured', async () => {
  // Nothing in the eligibility set or the step copy depends on model
  // availability, and the fixture never provides a provider.
  const { quest, calls } = load({ search: '?quest=build-hq' });
  await settle();
  assert.equal(quest.isActive(), true);
  assert.equal(calls.presented.length, 1);
});

/* ---- hand-over from Mission 01 (meet-your-assistant FR37) ----------------- */

function sessionWith(entries) {
  const values = new Map(Object.entries(entries));
  return {
    values,
    getItem: key => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key)
  };
}

test('entered straight from the hire, step 1 opens with the hand-over line, once', async () => {
  const loaded = load({ search: '?quest=build-hq' });
  // Set before the eligibility requests settle, as the hire's page did.
  const session = sessionWith({ 'ori:assistant-just-hired': '1' });
  loaded.sandbox.window.sessionStorage = session;
  await settle();

  const first = loaded.calls.presented[0];
  assert.equal(first.index, 1);
  assert.equal(
    first.answer,
    'That’s your assistant. Now let’s give them a home. On the Map, select the highlighted Personal HQ site.'
  );
  assert.equal(session.values.has('ori:assistant-just-hired'), false, 'the flag was not cleared');

  // A Map remount re-presents step 1: what Ori said does not change under the user.
  loaded.fireWindow('ori:hq-quest-signal', { stage: 'hq-status-changed', valid: false });
  assert.equal(loaded.calls.presented.at(-1).answer, first.answer);

  // A later entry in the same session reads exactly as it always has.
  loaded.quest.stop();
  await loaded.quest.resume();
  assert.match(loaded.calls.presented.at(-1).answer, /^Atlas is hired\./);
});

test('every other entry to Build My HQ reads exactly as today', async () => {
  const loaded = load({ search: '?quest=build-hq' });
  loaded.sandbox.window.sessionStorage = sessionWith({});
  await settle();
  assert.equal(
    loaded.calls.presented[0].answer,
    'Atlas is hired. Let’s give Atlas a home base. On the Map, select the highlighted Personal HQ site.'
  );
});

test('the walkthrough is inert when the guide is not on the page', async () => {
  const { quest } = load({ search: '?quest=build-hq' });
  await settle();
  quest.stop();
  // Simulate a page without the guide: nothing may throw.
  const bare = load({ search: '?quest=build-hq', guideOverrides: { presentQuestStep: undefined } });
  await settle();
  assert.equal(bare.calls.presented.length, 0);
});

/* ---- Ori's layer ------------------------------------------------------------ */

const MISSION_BOARD = {
  missions: [
    { order: 1, action_url: '/?quest=meet-assistant', status: 'completed' },
    {
      order: 2,
      action_url: '/?quest=build-hq',
      status: 'available',
      title: 'Build My HQ',
      why: 'Give your assistant a home base.',
      reward_craft: 5
    }
  ]
};
const withBoard = () => ({ ...eligibleResponses(), '/api/progression': MISSION_BOARD });
const layerSteps = calls => calls.layer.map(entry => `${entry.kind}:${entry.index || ''}`);

test('straight from the hire: Ori’s Mission 02 briefing, in the centre, before step 1', async () => {
  const loaded = load({ search: '?quest=build-hq', layer: true, responses: withBoard() });
  loaded.sandbox.window.sessionStorage = sessionWith({ 'ori:assistant-just-hired': '1' });
  await settle();
  await settle();

  assert.deepEqual(layerSteps(loaded.calls), ['briefing:']);
  const briefing = loaded.calls.layer[0];
  assert.equal(briefing.done, '✓ Mission 01 complete');
  assert.equal(briefing.greeting, 'That’s your assistant. Now let’s give them a home.');
  assert.equal(briefing.kicker, 'Starter · Mission 02');
  assert.equal(briefing.reward, '+5 Craft');
  assert.equal(briefing.title, 'Build My HQ');
  assert.equal(briefing.why, 'Give your assistant a home base.');
  assert.deepEqual([...briefing.steps], ['Select the site', 'Open Build My HQ', 'Confirm']);
  assert.equal(briefing.startLabel, 'Start mission');
  assert.equal(briefing.laterLabel, 'Do this later');
  // Nothing in Ori's panel, and the panel stays shut.
  assert.equal(loaded.calls.opened, 0);
  assert.deepEqual(loaded.calls.presented, []);
  assert.deepEqual(
    loaded.calls.setView.map(call => call.view),
    ['map']
  );

  briefing.onStart();
  const site = loaded.calls.layer.at(-1);
  assert.equal(site.kind, 'spotlight');
  assert.equal(site.index, 1);
  assert.equal(site.total, 3);
  assert.equal(site.coachmark, 'personal_hq_site');
  assert.equal(site.title, 'Select the Personal HQ site');
  assert.equal(site.body, 'The dashed site on the Map is saved for Atlas’s home base.');
});

test('the briefing still reads well without the mission board', async () => {
  const loaded = load({ search: '?quest=build-hq', layer: true });
  loaded.sandbox.window.sessionStorage = sessionWith({ 'ori:assistant-just-hired': '1' });
  await settle();
  await settle();
  const briefing = loaded.calls.layer[0];
  assert.equal(briefing.done, '');
  assert.equal(briefing.reward, '');
  assert.equal(briefing.title, 'Build My HQ');
  assert.equal(briefing.why, 'Give Atlas a home base on the Map.');
});

test('from the mission card: step 1 at once, lit on the Map', async () => {
  const loaded = load({ search: '?quest=build-hq', layer: true, responses: withBoard() });
  loaded.sandbox.window.sessionStorage = sessionWith({});
  await settle();
  assert.deepEqual(layerSteps(loaded.calls), ['spotlight:1']);
  assert.equal(loaded.calls.layer[0].laterLabel, 'Do this later');
});

test('the dialogs’ steps are Ori’s callout beside them; the form is never marked', async () => {
  const loaded = load({ search: '?quest=build-hq', layer: true });
  await settle();
  loaded.fireWindow('ori:hq-site-selected', { dialogOpened: true });
  const build = loaded.calls.layer.at(-1);
  assert.equal(build.kind, 'callout');
  assert.equal(build.index, 2);
  assert.equal(build.coachmark, 'personal_hq_build');
  assert.equal(build.anchor, '#cockpitContextModal .modal-dialog');
  assert.equal(build.title, 'Open Build My HQ');
  // The dialog has its own Do this later: not a second one beside it.
  assert.equal(build.laterLabel, '');

  loaded.fireWindow('ori:hq-quest-signal', { stage: 'build-form-opened' });
  const confirm = loaded.calls.layer.at(-1);
  assert.equal(confirm.kind, 'callout');
  assert.equal(confirm.index, 3);
  assert.equal(confirm.coachmark, undefined, 'the form was marked');
  assert.equal(confirm.target, '#hqBuildModal .modal-dialog');
  assert.equal(confirm.focus, false);
  // The form's own Cancel is the way out.
  assert.equal(confirm.laterLabel, '');
  assert.equal(confirm.title, 'Review and confirm');
  assert.deepEqual(layerSteps(loaded.calls), ['spotlight:1', 'callout:2', 'callout:3']);
  assert.deepEqual(loaded.calls.presented, []);
});

test('a Map remount does not show the same step again', async () => {
  const loaded = load({ search: '?quest=build-hq', layer: true });
  await settle();
  loaded.fireWindow('ori:hq-quest-signal', { stage: 'hq-status-changed', valid: false });
  loaded.fireWindow('ori:workspaces-changed');
  assert.deepEqual(layerSteps(loaded.calls), ['spotlight:1']);
});

test('Do this later in Ori’s layer is the same one recorded deferral', async () => {
  for (const handOver of [true, false]) {
    const loaded = load({ search: '?quest=build-hq', layer: true, responses: withBoard() });
    loaded.sandbox.window.sessionStorage = sessionWith(
      handOver ? { 'ori:assistant-just-hired': '1' } : {}
    );
    await settle();
    await settle();
    loaded.calls.layer[0].onLater();
    const skips = loaded.dispatched.filter(
      event => event.type === 'ori:personal-hq-action' && event.detail.action === 'skip'
    );
    assert.equal(skips.length, 1, handOver ? 'from the briefing' : 'from step 1');
    assert.equal(loaded.quest.isActive(), false);
  }
});

test('the dialog’s own Do this later ends the walkthrough, with one deferral', async () => {
  for (const layer of [true, false]) {
    const loaded = load({ search: '?quest=build-hq', layer });
    await settle();
    loaded.fireWindow('ori:hq-site-selected', { dialogOpened: true });
    // What the Map's own button dispatches.
    loaded.sandbox.window.dispatchEvent({
      type: 'ori:personal-hq-action',
      detail: { action: 'skip' }
    });
    assert.equal(loaded.quest.isActive(), false, layer ? 'layer' : 'panel');
    const skips = loaded.dispatched.filter(event => event.type === 'ori:personal-hq-action');
    assert.equal(skips.length, 1, 'the walkthrough recorded a second deferral');
  }
});

test('Build and Import from the dialog do not end the walkthrough', async () => {
  const loaded = load({ search: '?quest=build-hq', layer: true });
  await settle();
  for (const action of ['build', 'import']) {
    loaded.fireWindow('ori:personal-hq-action', { action });
  }
  assert.equal(loaded.quest.isActive(), true);
});

test('no room beside a dialog: the rest of the walkthrough is in Ori’s panel', async () => {
  const loaded = load({ search: '?quest=build-hq', layer: true, layerFits: false });
  await settle();
  await settle();
  assert.deepEqual(layerSteps(loaded.calls), ['spotlight:1']);
  assert.equal(loaded.calls.opened, 1);
  assert.deepEqual(
    loaded.calls.presented.map(step => step.index),
    [1]
  );
  loaded.fireWindow('ori:hq-site-selected', { dialogOpened: true });
  assert.deepEqual(
    loaded.calls.presented.map(step => step.index),
    [1, 2]
  );
});

test('stop and a finished setup close Ori’s layer, briefing included', async () => {
  const loaded = load({ search: '?quest=build-hq', layer: true, responses: withBoard() });
  loaded.sandbox.window.sessionStorage = sessionWith({ 'ori:assistant-just-hired': '1' });
  await settle();
  await settle();
  loaded.fireWindow('ori:hq-quest-signal', { stage: 'setup-succeeded' });
  assert.equal(loaded.calls.layerClosed, 1);
  assert.equal(loaded.quest.isActive(), false);
});
