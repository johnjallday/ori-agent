// Tests for meet-assistant-home-prompt.js — Mission 01 arriving on Home.
//   node --test internal/web/static/js/modules/meet-assistant-home-prompt.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { clearQuestParams, decide, run } from './meet-assistant-home-prompt.js';
import { MEET_ASSISTANT_AGENTS_ROUTE } from './personal-assistant-hire.js';

const MISSIONS = [
  {
    order: 1,
    title: 'Meet your assistant',
    status: 'available',
    action_url: '/?quest=meet-assistant'
  }
];

function server({ needsOnboarding = false, relationship = 'needs_hire', failing = '' } = {}) {
  const requests = [];
  const bodies = {
    '/api/onboarding/status': { needs_onboarding: needsOnboarding },
    '/api/personal-assistant': { personal_assistant: { state: relationship } },
    '/api/progression': { missions: MISSIONS }
  };
  const fetchImpl = async url => {
    requests.push(url);
    if (url === failing) return { ok: false, status: 500 };
    return { ok: true, json: async () => bodies[url] };
  };
  return { fetchImpl, requests };
}

const home = (search = '') => ({ pathname: '/', search });
const ASKED = '?quest=meet-assistant';
const BRIEFING = '?quest=meet-assistant&briefing=1';

test('a plain Home visit does nothing and asks the server nothing', async () => {
  const { fetchImpl, requests } = server();
  for (const search of ['', '?view=map', '?briefing=1']) {
    assert.deepEqual(await decide({ fetchImpl, location: home(search) }), {
      action: 'none',
      requested: false
    });
  }
  assert.deepEqual(requests, []);
});

// Mission 01's Start, Today's banner, Ask Ori, /?hire=1 and Ori's help link here.
test('?quest=meet-assistant goes straight to the assistant configuration', async () => {
  for (const relationship of ['needs_hire', 'hiring']) {
    const { fetchImpl, requests } = server({ relationship });
    assert.deepEqual(await decide({ fetchImpl, location: home(ASKED) }), {
      action: 'agents',
      requested: true
    });
    assert.equal(
      requests.some(url => url.includes('/api/ori-guide')),
      false
    );
  }
});

test('onboarding’s hand-over stays on Home, where the welcome card is the briefing', async () => {
  const { fetchImpl } = server();
  assert.deepEqual(await decide({ fetchImpl, location: home(BRIEFING) }), {
    action: 'welcome',
    requested: true
  });
});

test('a repair asked for from Home goes straight to the Agents page', async () => {
  const { fetchImpl } = server({ relationship: 'repair_needed' });
  for (const search of [ASKED, BRIEFING]) {
    assert.equal((await decide({ fetchImpl, location: home(search) })).action, 'agents');
  }
});

test('nothing once the assistant is hired, and the URL is still tidied', async () => {
  for (const relationship of ['needs_hq', 'provisioning_hq', 'active', 'paused', 'bogus']) {
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

test('a failed read decides nothing rather than guessing an empty account', async () => {
  for (const failing of ['/api/onboarding/status', '/api/personal-assistant']) {
    const { fetchImpl } = server({ failing });
    assert.deepEqual(await decide({ fetchImpl, location: home(ASKED) }), {
      action: 'none',
      requested: true
    });
  }
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
    (await decide({ fetchImpl: quiet.fetchImpl, location: { pathname: '/agents', search: ASKED } }))
      .action,
    'none'
  );
  for (const intent of ['?quest=build-hq', '?focus=personal-hq', '?create=1', '?setup=quest']) {
    const outcome = await decide({ fetchImpl: quiet.fetchImpl, location: home(intent) });
    assert.deepEqual(outcome, { action: 'none', requested: false }, intent);
  }
  assert.deepEqual(quiet.requests, [], 'an ineligible page still made requests');
});

function fakeWindow(href) {
  const replaced = [];
  return {
    replaced,
    location: { href },
    history: {
      state: null,
      replaceState: (_state, _title, url) => replaced.push(url)
    }
  };
}

test('run navigates to the configuration for agents, and only tidies otherwise', () => {
  const navigated = [];
  const win = fakeWindow('http://ori.test/?quest=meet-assistant&briefing=1&view=map');
  assert.equal(
    run({ action: 'agents', requested: true }, { win, navigate: href => navigated.push(href) }),
    true
  );
  assert.deepEqual(navigated, [MEET_ASSISTANT_AGENTS_ROUTE]);
  assert.deepEqual(win.replaced, [], 'navigating away needs no tidy');

  assert.equal(
    run({ action: 'welcome', requested: true }, { win, navigate: href => navigated.push(href) }),
    false
  );
  assert.deepEqual(win.replaced, ['/?view=map']);
  assert.equal(navigated.length, 1, 'the welcome never navigates');

  const plain = fakeWindow('http://ori.test/');
  run({ action: 'none', requested: false }, { win: plain, navigate: () => assert.fail() });
  assert.deepEqual(plain.replaced, []);
});

test('clearQuestParams keeps other params and survives a broken history', () => {
  const win = fakeWindow('http://ori.test/?quest=meet-assistant&select=a#x');
  clearQuestParams(win);
  assert.deepEqual(win.replaced, ['/?select=a#x']);
  assert.doesNotThrow(() => clearQuestParams({ location: { href: 'nope' }, history: {} }));
  assert.doesNotThrow(() => clearQuestParams(null));
});

test('the Home arrival no longer dims the page or points at the Agents nav entry', () => {
  const source = readFileSync(new URL('./meet-assistant-home-prompt.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /ori-spotlight|showSpotlight|showBriefing|navAgentsLink/);
});
