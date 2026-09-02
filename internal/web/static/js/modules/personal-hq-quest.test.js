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

function load({ search = '', responses = eligibleResponses(), guideOverrides = {} } = {}) {
  const calls = { fetches: [], setView: [], presented: [], cleared: 0, opened: 0 };
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

test('the walkthrough is inert when the guide is not on the page', async () => {
  const { quest } = load({ search: '?quest=build-hq' });
  await settle();
  quest.stop();
  // Simulate a page without the guide: nothing may throw.
  const bare = load({ search: '?quest=build-hq', guideOverrides: { presentQuestStep: undefined } });
  await settle();
  assert.equal(bare.calls.presented.length, 0);
});
