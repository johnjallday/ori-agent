// Tests for meet-assistant-home-prompt.js — Mission 01's first step, on Home.
//   node --test internal/web/static/js/modules/meet-assistant-home-prompt.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CHOICE_GO,
  PROMPT_QUEST,
  TOTAL_STEPS,
  decide,
  start
} from './meet-assistant-home-prompt.js';

function server({
  needsOnboarding = false,
  relationship = 'needs_hire',
  missionStatus = 'available',
  progressionFails = false
} = {}) {
  const requests = [];
  const bodies = {
    '/api/onboarding/status': { needs_onboarding: needsOnboarding },
    '/api/personal-assistant': { personal_assistant: { state: relationship } },
    '/api/progression': {
      missions: [
        { order: 1, status: missionStatus, action_url: '/?quest=meet-assistant' },
        { order: 2, status: 'available', action_url: '/?quest=build-hq', locked: true }
      ]
    }
  };
  const fetchImpl = async url => {
    requests.push(url);
    if (url === '/api/progression' && progressionFails) return { ok: false, status: 500 };
    return { ok: true, json: async () => bodies[url] };
  };
  return { fetchImpl, requests };
}

const home = (search = '') => ({ pathname: '/', search });

function fakeGuide({ coachmarkResolved = true, open = false } = {}) {
  const calls = { presented: [], opened: [] };
  return {
    calls,
    guide: {
      isOpen: () => open,
      open: (trigger, options) => calls.opened.push(options),
      presentQuestStep: step => {
        calls.presented.push(step);
        return { rendered: true, coachmarkResolved };
      }
    }
  };
}

function fakeDocument() {
  const listeners = {};
  return {
    addEventListener: (type, fn) => {
      listeners[type] = listeners[type] || [];
      listeners[type].push(fn);
    },
    fire: (type, detail) => (listeners[type] || []).forEach(fn => fn({ type, detail }))
  };
}

test('Home before the hire shows the first step, from server state alone', async () => {
  for (const relationship of ['needs_hire', 'hiring']) {
    const { fetchImpl, requests } = server({ relationship });
    const plain = await decide({ fetchImpl, location: home() });
    assert.equal(plain.action, 'present', relationship);
    assert.equal(plain.requested, false);
    assert.equal(
      requests.some(url => url.includes('/api/ori-guide')),
      false,
      'the step asked the guide endpoint'
    );
  }
});

// Mission 01's Start, Today's banner and Ask Ori all link here, so the user is
// shown the first click instead of being moved past it.
test('?quest=meet-assistant asks for the first step and is reported for tidying', async () => {
  const { fetchImpl } = server();
  assert.deepEqual(await decide({ fetchImpl, location: home('?quest=meet-assistant') }), {
    action: 'present',
    requested: true
  });
});

test('no step once the assistant is hired', async () => {
  for (const relationship of ['needs_hq', 'provisioning_hq', 'active', 'paused']) {
    const { fetchImpl } = server({ relationship });
    const plain = await decide({ fetchImpl, location: home() });
    const asked = await decide({ fetchImpl, location: home('?quest=meet-assistant') });
    assert.equal(plain.action, 'none', relationship);
    assert.deepEqual(asked, { action: 'none', requested: true }, relationship);
  }
});

// A repair has nothing to walk through: the Agents page opens its one-button
// view on arrival. A plain visit stays quiet; Home's own banner offers it.
test('a repair asked for from Home goes straight to the Agents page', async () => {
  const { fetchImpl } = server({ relationship: 'repair_needed' });
  assert.equal(
    (await decide({ fetchImpl, location: home('?quest=meet-assistant') })).action,
    'agents'
  );
  assert.equal((await decide({ fetchImpl, location: home() })).action, 'none');
});

test('no step when Mission 01 is already complete', async () => {
  const { fetchImpl } = server({ missionStatus: 'completed' });
  assert.equal((await decide({ fetchImpl, location: home() })).action, 'none');
});

test('an unreadable mission board still shows the step on an unhired relationship', async () => {
  const { fetchImpl } = server({ progressionFails: true });
  assert.equal((await decide({ fetchImpl, location: home() })).action, 'present');
});

test('no step while onboarding owns the screen, off Home, or under another intent', async () => {
  assert.equal(
    (
      await decide({
        fetchImpl: server({ needsOnboarding: true }).fetchImpl,
        location: home()
      })
    ).action,
    'none'
  );
  const quiet = server();
  assert.equal(
    (
      await decide({
        fetchImpl: quiet.fetchImpl,
        location: { pathname: '/agents', search: '' }
      })
    ).action,
    'none'
  );
  for (const intent of ['?quest=build-hq', '?focus=personal-hq', '?create=1', '?setup=quest']) {
    const outcome = await decide({ fetchImpl: quiet.fetchImpl, location: home(intent) });
    assert.deepEqual(outcome, { action: 'none', requested: false }, intent);
  }
  assert.deepEqual(quiet.requests, [], 'an ineligible page still made requests');
  // A plain Home visit with harmless state in the URL still gets the step.
  assert.equal(
    (await decide({ fetchImpl: server().fetchImpl, location: home('?view=map') })).action,
    'present'
  );
});

test('the first step points at the Agents nav entry for the user to click', () => {
  const { guide, calls } = fakeGuide();
  const doc = fakeDocument();
  assert.equal(start({ guide, doc, navigate: () => {} }), true);

  assert.equal(calls.presented.length, 1);
  const step = calls.presented[0];
  assert.equal(step.quest, PROMPT_QUEST);
  assert.equal(step.index, 1);
  assert.equal(step.total, TOTAL_STEPS);
  assert.equal(TOTAL_STEPS, 6);
  assert.equal(
    step.answer,
    'Your assistant works from the Agents page. Click Agents to go meet them.'
  );
  assert.equal(step.coachmark, 'nav_agents');
  assert.deepEqual(
    step.choices.map(choice => [choice.id, choice.label]),
    [[CHOICE_GO, 'Take me there']]
  );
  // skipGreeting: the greeting would otherwise land later and replace the step.
  assert.equal(calls.opened.length, 1);
  assert.equal(calls.opened[0].skipGreeting, true);
});

test('Take me there goes to the Agents page leg; other choices do nothing', () => {
  const { guide } = fakeGuide();
  const doc = fakeDocument();
  const visits = [];
  start({ guide, doc, navigate: href => visits.push(href) });

  doc.fire('ori-guide:quest-choice', { quest: 'build-hq', choice: CHOICE_GO });
  doc.fire('ori-guide:quest-choice', { quest: PROMPT_QUEST, choice: 'something-else' });
  assert.deepEqual(visits, []);

  doc.fire('ori-guide:quest-choice', { quest: PROMPT_QUEST, choice: CHOICE_GO });
  assert.deepEqual(visits, ['/agents?quest=meet-assistant']);
});

test('a collapsed navbar still offers the way there', () => {
  // The Agents link cannot be marked when the navbar is collapsed; the choice
  // is what carries the user on.
  const { guide, calls } = fakeGuide({ coachmarkResolved: false });
  start({ guide, doc: fakeDocument(), navigate: () => {} });
  assert.equal(calls.presented[0].choices.length, 1);
});

test('an already-open panel is not reopened, and a page without the guide is inert', () => {
  const open = fakeGuide({ open: true });
  start({ guide: open.guide, doc: fakeDocument(), navigate: () => {} });
  assert.equal(open.calls.opened.length, 0);
  assert.equal(start({ guide: {}, doc: fakeDocument(), navigate: () => {} }), false);
});
