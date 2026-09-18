// Tests for meet-assistant-home-prompt.js — Ori's nudge on Home before the hire.
//   node --test internal/web/static/js/modules/meet-assistant-home-prompt.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CHOICE_GO, PROMPT_QUEST, shouldPrompt, start } from './meet-assistant-home-prompt.js';

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
        { order: 1, status: missionStatus, action_url: '/agents?quest=meet-assistant' },
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

test('Home before the hire prompts, from server state alone', async () => {
  for (const relationship of ['needs_hire', 'hiring']) {
    const { fetchImpl, requests } = server({ relationship });
    assert.equal(await shouldPrompt({ fetchImpl, location: home() }), true, relationship);
    assert.equal(
      requests.some(url => url.includes('/api/ori-guide')),
      false,
      'the prompt asked the guide endpoint'
    );
  }
});

test('no prompt once the assistant is hired, or while it needs a repair', async () => {
  for (const relationship of ['needs_hq', 'provisioning_hq', 'active', 'paused', 'repair_needed']) {
    const { fetchImpl } = server({ relationship });
    assert.equal(await shouldPrompt({ fetchImpl, location: home() }), false, relationship);
  }
});

test('no prompt when Mission 01 is already complete', async () => {
  const { fetchImpl } = server({ missionStatus: 'completed' });
  assert.equal(await shouldPrompt({ fetchImpl, location: home() }), false);
});

test('an unreadable mission board still prompts on an unhired relationship', async () => {
  const { fetchImpl } = server({ progressionFails: true });
  assert.equal(await shouldPrompt({ fetchImpl, location: home() }), true);
});

test('no prompt while onboarding owns the screen, off Home, or under another walkthrough', async () => {
  assert.equal(
    await shouldPrompt({
      fetchImpl: server({ needsOnboarding: true }).fetchImpl,
      location: home()
    }),
    false
  );
  const quiet = server();
  assert.equal(
    await shouldPrompt({
      fetchImpl: quiet.fetchImpl,
      location: { pathname: '/agents', search: '' }
    }),
    false
  );
  for (const intent of ['?quest=build-hq', '?focus=personal-hq', '?create=1', '?setup=quest']) {
    assert.equal(
      await shouldPrompt({ fetchImpl: quiet.fetchImpl, location: home(intent) }),
      false,
      intent
    );
  }
  assert.deepEqual(quiet.requests, [], 'an ineligible page still made requests');
  // A plain Home visit with harmless state in the URL still prompts.
  assert.equal(
    await shouldPrompt({ fetchImpl: server().fetchImpl, location: home('?view=map') }),
    true
  );
});

test('the prompt presents once, points at the Agents nav entry, and offers Take me there', () => {
  const { guide, calls } = fakeGuide();
  const doc = fakeDocument();
  assert.equal(start({ guide, doc, navigate: () => {} }), true);

  assert.equal(calls.presented.length, 1);
  const step = calls.presented[0];
  assert.equal(step.quest, PROMPT_QUEST);
  assert.equal(step.answer, 'Your assistant works from the Agents page. Let’s go meet them.');
  assert.equal(step.coachmark, 'nav_agents');
  assert.deepEqual(
    step.choices.map(choice => [choice.id, choice.label]),
    [[CHOICE_GO, 'Take me there']]
  );
  // skipGreeting: the greeting would otherwise land later and replace the prompt.
  assert.equal(calls.opened.length, 1);
  assert.equal(calls.opened[0].skipGreeting, true);
});

test('Take me there navigates to Mission 01; other choices do nothing', () => {
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
