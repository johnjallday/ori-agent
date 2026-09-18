// Tests for meet-assistant-home-prompt.js — the start of Mission 01, on Home.
//   node --test internal/web/static/js/modules/meet-assistant-home-prompt.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  FIRST_STEP,
  STEP_LABELS,
  TOTAL_STEPS,
  briefingCopy,
  decide,
  run
} from './meet-assistant-home-prompt.js';

const MISSIONS = [
  {
    order: 1,
    title: 'Meet your assistant',
    why: 'Your assistant is the one agent that owns your ongoing work. Make them yours.',
    status: 'available',
    action_url: '/?quest=meet-assistant',
    reward_craft: 5
  },
  { order: 2, title: 'Build My HQ', status: 'available', locked: true },
  { order: 3, title: 'Tidy your Downloads', status: 'available', locked: true },
  { order: 4, title: 'Plan my first day', status: 'available', locked: true },
  { order: 5, title: 'Read your first Daily Brief', status: 'available', locked: true }
];

function server({
  needsOnboarding = false,
  relationship = 'needs_hire',
  missionStatus = 'available',
  progressionFails = false
} = {}) {
  const requests = [];
  const missions = MISSIONS.map(mission =>
    mission.order === 1 ? { ...mission, status: missionStatus } : mission
  );
  const bodies = {
    '/api/onboarding/status': {
      needs_onboarding: needsOnboarding,
      user_name: 'Sam',
      assistant_name: 'Ori'
    },
    '/api/personal-assistant': { personal_assistant: { state: relationship } },
    '/api/progression': { missions }
  };
  const fetchImpl = async url => {
    requests.push(url);
    if (url === '/api/progression' && progressionFails) return { ok: false, status: 500 };
    return { ok: true, json: async () => bodies[url] };
  };
  return { fetchImpl, requests };
}

const home = (search = '') => ({ pathname: '/', search });
const ASKED = '?quest=meet-assistant';
const BRIEFING = '?quest=meet-assistant&briefing=1';

// A stand-in for ori-spotlight.js that records what it was asked to show.
function fakeLayer({ found = true } = {}) {
  const calls = { briefing: [], spotlight: [], closed: 0 };
  return {
    calls,
    showBriefing: opts => calls.briefing.push(opts),
    showSpotlight: async opts => {
      calls.spotlight.push(opts);
      return found;
    },
    close: () => {
      calls.closed += 1;
    }
  };
}

function fakePage() {
  const store = new Map();
  const listeners = [];
  const link = {
    addEventListener: (type, fn) => listeners.push({ type, fn }),
    removeEventListener: (type, fn) => {
      const at = listeners.findIndex(l => l.type === type && l.fn === fn);
      if (at >= 0) listeners.splice(at, 1);
    },
    click: () => listeners.filter(l => l.type === 'click').forEach(l => l.fn())
  };
  return {
    link,
    store,
    doc: { getElementById: id => (id === 'navAgentsLink' ? link : null) },
    win: {
      sessionStorage: {
        setItem: (key, value) => store.set(key, String(value)),
        getItem: key => (store.has(key) ? store.get(key) : null)
      }
    }
  };
}

test('a plain Home visit does nothing and asks the server nothing', async () => {
  const { fetchImpl, requests } = server();
  assert.deepEqual(await decide({ fetchImpl, location: home() }), {
    action: 'none',
    requested: false
  });
  assert.deepEqual(await decide({ fetchImpl, location: home('?view=map') }), {
    action: 'none',
    requested: false
  });
  assert.deepEqual(requests, []);
});

// Mission 01's Start, Today's banner, Ask Ori and /?hire=1 all link here.
test('?quest=meet-assistant asks for the first step, from server state alone', async () => {
  for (const relationship of ['needs_hire', 'hiring']) {
    const { fetchImpl, requests } = server({ relationship });
    const decision = await decide({ fetchImpl, location: home(ASKED) });
    assert.equal(decision.action, 'step', relationship);
    assert.equal(decision.requested, true);
    assert.equal(decision.mission.title, 'Meet your assistant');
    assert.equal(
      requests.some(url => url.includes('/api/ori-guide')),
      false,
      'the step asked the guide endpoint'
    );
  }
});

// First-run onboarding hands over with &briefing=1.
test('onboarding’s hand-over asks for Ori’s briefing first', async () => {
  const { fetchImpl } = server();
  const decision = await decide({ fetchImpl, location: home(BRIEFING) });
  assert.equal(decision.action, 'briefing');
  assert.equal(decision.onboarding.user_name, 'Sam');
  // briefing=1 on its own is not a request for Mission 01.
  assert.equal((await decide({ fetchImpl, location: home('?briefing=1') })).action, 'none');
});

test('nothing once the assistant is hired, and the URL is still tidied', async () => {
  for (const relationship of ['needs_hq', 'provisioning_hq', 'active', 'paused']) {
    const { fetchImpl } = server({ relationship });
    for (const search of [ASKED, BRIEFING]) {
      assert.deepEqual(
        await decide({ fetchImpl, location: home(search) }),
        { action: 'none', requested: true },
        `${relationship} ${search}`
      );
    }
  }
});

// A repair has nothing to walk through: the Agents page opens its one-button
// view on arrival.
test('a repair asked for from Home goes straight to the Agents page', async () => {
  const { fetchImpl } = server({ relationship: 'repair_needed' });
  assert.equal((await decide({ fetchImpl, location: home(ASKED) })).action, 'agents');
});

test('nothing when Mission 01 is already complete', async () => {
  const { fetchImpl } = server({ missionStatus: 'completed' });
  assert.equal((await decide({ fetchImpl, location: home(ASKED) })).action, 'none');
});

test('an unreadable mission board still shows the step on an unhired relationship', async () => {
  const { fetchImpl } = server({ progressionFails: true });
  const decision = await decide({ fetchImpl, location: home(ASKED) });
  assert.equal(decision.action, 'step');
  assert.equal(decision.mission, null);
});

test('nothing while onboarding owns the screen, off Home, or under another intent', async () => {
  assert.equal(
    (
      await decide({
        fetchImpl: server({ needsOnboarding: true }).fetchImpl,
        location: home(ASKED)
      })
    ).action,
    'none'
  );
  const quiet = server();
  assert.equal(
    (
      await decide({
        fetchImpl: quiet.fetchImpl,
        location: { pathname: '/agents', search: ASKED }
      })
    ).action,
    'none'
  );
  for (const intent of ['?quest=build-hq', '?focus=personal-hq', '?create=1', '?setup=quest']) {
    const outcome = await decide({ fetchImpl: quiet.fetchImpl, location: home(intent) });
    assert.deepEqual(outcome, { action: 'none', requested: false }, intent);
  }
  assert.deepEqual(quiet.requests, [], 'an ineligible page still made requests');
});

test('the briefing names the user and Ori, the mission, its six steps and what it unlocks', () => {
  const copy = briefingCopy({
    onboarding: { user_name: 'Sam', assistant_name: 'Ori' },
    mission: MISSIONS[0],
    missions: MISSIONS
  });
  assert.equal(copy.guideName, 'Ori');
  assert.equal(copy.greeting, 'Hi Sam. Before anything else, let’s meet your assistant.');
  assert.equal(copy.kicker, 'Starter · Mission 01');
  assert.equal(copy.reward, '+5 Craft');
  assert.equal(copy.title, 'Meet your assistant');
  assert.equal(copy.unlocks, 'Unlocks Build My HQ and 3 more missions.');
  assert.deepEqual(copy.steps, ['Open Agents', 'New Agent', 'Name', 'Face', 'Focus', 'Hire']);
  assert.equal(STEP_LABELS.length, TOTAL_STEPS);
  assert.equal(copy.startLabel, 'Start mission');
  assert.equal(copy.laterLabel, 'Not now');
  assert.equal(copy.note, 'Nothing is created until you press Hire.');
});

test('the briefing still reads well with nothing to go on', () => {
  const copy = briefingCopy({ onboarding: { assistant_name: 'Nova' } });
  assert.equal(copy.guideName, 'Nova', 'the guide is called what the user named it');
  assert.equal(copy.greeting, 'Before anything else, let’s meet your assistant.');
  assert.equal(copy.reward, '');
  assert.equal(copy.unlocks, '');
  assert.equal(copy.title, 'Meet your assistant');
  assert.match(copy.why, /owns your ongoing work/);
  assert.equal(briefingCopy({}).guideName, 'Ori');
  assert.equal(
    briefingCopy({ missions: [{ title: 'Build My HQ', locked: true }] }).unlocks,
    'Unlocks Build My HQ.'
  );
  assert.equal(
    briefingCopy({
      missions: [
        { title: 'Build My HQ', locked: true },
        { title: 'Tidy your Downloads', locked: true }
      ]
    }).unlocks,
    'Unlocks Build My HQ and 1 more mission.'
  );
});

test('the first step lights the Agents nav entry, and says why', async () => {
  const layer = fakeLayer();
  const page = fakePage();
  const visits = [];
  const shown = await run(
    { action: 'step', onboarding: { assistant_name: 'Ori' } },
    { layer, doc: page.doc, win: page.win, navigate: href => visits.push(href) }
  );
  assert.equal(shown, true);
  assert.equal(layer.calls.briefing.length, 0);
  const step = layer.calls.spotlight[0];
  assert.equal(step.coachmark, 'nav_agents');
  assert.equal(step.index, 1);
  assert.equal(step.total, 6);
  assert.equal(step.title, 'Click Agents');
  assert.equal(step.body, 'Your assistant works from the Agents page.');
  assert.equal(step.guideName, 'Ori');
  assert.equal(FIRST_STEP.laterLabel, 'Not now');
  assert.deepEqual(visits, []);
});

// The user pressing Agents is what tells the Agents page to keep the spotlight.
test('pressing Agents from the first step carries the mission to the Agents page', async () => {
  const layer = fakeLayer();
  const page = fakePage();
  await run({ action: 'step' }, { layer, doc: page.doc, win: page.win, navigate: () => {} });
  assert.equal(page.store.get('ori:meet-assistant-guided'), undefined, 'set before the press');
  page.link.click();
  assert.equal(page.store.get('ori:meet-assistant-guided'), '1');
});

test('Not now leaves nothing behind for a later visit to the Agents page', async () => {
  const layer = fakeLayer();
  const page = fakePage();
  await run({ action: 'step' }, { layer, doc: page.doc, win: page.win, navigate: () => {} });
  layer.calls.spotlight[0].onLater();
  page.link.click();
  assert.equal(page.store.get('ori:meet-assistant-guided'), undefined);
});

// Never a dimmed page with nothing lit.
test('when Agents cannot be lit, the user is taken to the Agents page instead', async () => {
  const layer = fakeLayer({ found: false });
  const page = fakePage();
  const visits = [];
  const shown = await run(
    { action: 'step' },
    { layer, doc: page.doc, win: page.win, navigate: href => visits.push(href) }
  );
  assert.equal(shown, false);
  assert.equal(layer.calls.closed, 1);
  assert.deepEqual(visits, ['/agents?quest=meet-assistant']);
});

test('the briefing comes first, and Start mission opens the first step', async () => {
  const layer = fakeLayer();
  const page = fakePage();
  await run(
    {
      action: 'briefing',
      onboarding: { user_name: 'Sam', assistant_name: 'Ori' },
      mission: MISSIONS[0],
      missions: MISSIONS
    },
    { layer, doc: page.doc, win: page.win, navigate: () => {} }
  );
  assert.equal(layer.calls.briefing.length, 1);
  assert.equal(layer.calls.spotlight.length, 0, 'the step waits for Start mission');
  const briefing = layer.calls.briefing[0];
  assert.equal(briefing.greeting, 'Hi Sam. Before anything else, let’s meet your assistant.');
  assert.equal(briefing.onLater, undefined, 'Not now needs nothing from Home');

  briefing.onStart();
  await Promise.resolve();
  assert.equal(layer.calls.spotlight.length, 1);
  assert.equal(layer.calls.spotlight[0].coachmark, 'nav_agents');
});

test('nothing is shown for any other decision', async () => {
  const layer = fakeLayer();
  for (const action of ['none', 'agents']) {
    assert.equal(await run({ action }, { layer, navigate: () => {} }), false);
  }
  assert.equal(await run(null, { layer }), false);
  assert.deepEqual(layer.calls, { briefing: [], spotlight: [], closed: 0 });
});
