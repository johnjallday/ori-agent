// Tests for home-welcome.js — the empty-Home welcome decision, its scoped
// "Explore first" preference and its markup.
//   node --test internal/web/static/js/modules/home-welcome.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  WELCOME_STORAGE_KEY,
  hasHomeIntent,
  homeWelcomeView,
  readExploredStage,
  rememberExplored,
  rescopeExplored,
  welcomeCardHTML,
  welcomeStage
} from './home-welcome.js';

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  const writes = [];
  return {
    writes,
    getItem: key => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => {
      writes.push(['set', key, value]);
      data.set(key, String(value));
    },
    removeItem: key => {
      writes.push(['remove', key]);
      data.delete(key);
    }
  };
}

const EMPTY = { areaState: 'empty-map', hqStatus: { valid: false, hq_onboarding_state: 'unseen' } };

test('welcomeStage maps only the four onboarding states to a stage', () => {
  assert.equal(welcomeStage('needs_hire'), 'meet');
  assert.equal(welcomeStage('hiring'), 'meet');
  assert.equal(welcomeStage('needs_hq'), 'hq');
  assert.equal(welcomeStage(' provisioning_hq '), 'hq');
  for (const other of ['active', 'paused', 'repair_needed', '', undefined, null, 'bogus']) {
    assert.equal(welcomeStage(other), '', String(other));
  }
});

test('a truly new Home shows the card with one action per stage', () => {
  const cases = [
    ['needs_hire', 'meet', 1],
    ['hiring', 'resume-meet', 1],
    ['needs_hq', 'build', 2],
    ['provisioning_hq', 'resume-hq', 2]
  ];
  for (const [relationshipState, action, step] of cases) {
    const view = homeWelcomeView({ relationshipState, ...EMPTY });
    assert.equal(view.show, true, relationshipState);
    assert.equal(view.compact, false, 'the card owns the action, so no compact duplicate');
    assert.equal(view.action, action);
    assert.equal(view.step, step);
  }
});

test('loading or a failed relationship read is not an empty account', () => {
  for (const relationshipState of [undefined, null, '']) {
    const view = homeWelcomeView({ relationshipState, ...EMPTY });
    assert.deepEqual([view.show, view.compact, view.reason], [false, false, 'unknown']);
  }
});

test('ready and repair relationships never show the card or a welcome compact entry', () => {
  for (const relationshipState of ['active', 'paused', 'repair_needed']) {
    const view = homeWelcomeView({ relationshipState, ...EMPTY });
    assert.equal(view.show, false);
    assert.equal(view.compact, false, 'the ordinary primary handles these');
  }
});

test('populated, loading, failed or HQ-unknown areas fall back to the compact entry', () => {
  const offs = [
    [{ areaState: 'ready', hqStatus: EMPTY.hqStatus }, 'not-empty'],
    [{ areaState: 'loading', hqStatus: EMPTY.hqStatus }, 'not-empty'],
    [{ areaState: 'error', hqStatus: EMPTY.hqStatus }, 'not-empty'],
    [{ areaState: 'onboarding-loading', hqStatus: EMPTY.hqStatus }, 'not-empty'],
    [{ areaState: 'empty-map', hqStatus: null }, 'hq-unknown'],
    [{ areaState: 'empty-map', hqStatus: { valid: true, workspace_id: 'hq' } }, 'hq-unknown']
  ];
  for (const [input, reason] of offs) {
    const view = homeWelcomeView({ relationshipState: 'needs_hire', ...input });
    assert.equal(view.show, false, JSON.stringify(input));
    assert.equal(view.compact, true);
    assert.equal(view.reason, reason);
  }
});

test('Tree, an explicit intent, or an open context modal yields to the compact entry', () => {
  const tree = homeWelcomeView({ relationshipState: 'needs_hire', ...EMPTY, view: 'tree' });
  assert.deepEqual([tree.show, tree.compact, tree.reason], [false, true, 'tree']);
  const intent = homeWelcomeView({ relationshipState: 'needs_hq', ...EMPTY, intent: true });
  assert.deepEqual([intent.show, intent.compact, intent.reason], [false, true, 'intent']);
  const modal = homeWelcomeView({ relationshipState: 'needs_hq', ...EMPTY, modalOpen: true });
  assert.deepEqual([modal.show, modal.compact, modal.reason], [false, true, 'modal']);
  const drawer = homeWelcomeView({ relationshipState: 'needs_hq', ...EMPTY, drawerOpen: true });
  assert.deepEqual([drawer.show, drawer.compact, drawer.reason], [false, true, 'drawer']);
});

test('Explore first hides the card for its own stage only', () => {
  const explored = homeWelcomeView({
    relationshipState: 'hiring',
    ...EMPTY,
    exploredStage: 'meet'
  });
  assert.deepEqual([explored.show, explored.compact, explored.reason], [false, true, 'explored']);
  const nextStage = homeWelcomeView({
    relationshipState: 'needs_hq',
    ...EMPTY,
    exploredStage: 'meet'
  });
  assert.equal(nextStage.show, true, 'a later stage is a new invitation');
});

test('hasHomeIntent recognizes every explicit Home intent and ignores plain visits', () => {
  for (const search of [
    '?quest=meet-assistant',
    '?focus=personal-hq',
    '?create=1',
    '?setup=notes',
    '?folder=show',
    '?hq_onboarding=1',
    '?panel=today'
  ]) {
    assert.equal(hasHomeIntent(search), true, search);
  }
  for (const search of ['', '?', '?view=map', '?view=tree', '?select=ws-1']) {
    assert.equal(hasHomeIntent(search), false, search);
  }
});

test('the Explore preference survives a reload, is stage-scoped, and rescopes on change', () => {
  const storage = memoryStorage();
  assert.equal(readExploredStage(storage), '');
  assert.equal(rememberExplored(storage, 'meet'), true);
  // A reload reads the same storage.
  assert.equal(readExploredStage(storage), 'meet');
  // Unknown relationship: not evidence of change; keep it.
  assert.equal(rescopeExplored(storage, ''), false);
  assert.equal(rescopeExplored(storage, 'hiring'), false, 'same stage keeps it');
  assert.equal(readExploredStage(storage), 'meet');
  // Hired: the meet-stage choice says nothing about the HQ.
  assert.equal(rescopeExplored(storage, 'needs_hq'), true);
  assert.equal(readExploredStage(storage), '');
  rememberExplored(storage, 'hq');
  assert.equal(rescopeExplored(storage, 'active'), true, 'a built HQ clears it');
  assert.equal(storage.getItem(WELCOME_STORAGE_KEY), null);
});

test('the Explore preference touches only its own key and refuses bogus stages', () => {
  const storage = memoryStorage();
  assert.equal(rememberExplored(storage, 'active'), false);
  assert.equal(rememberExplored(storage, ''), false);
  rememberExplored(storage, 'hq');
  assert.deepEqual(
    storage.writes.map(([kind, key]) => [kind, key]),
    [['set', WELCOME_STORAGE_KEY]]
  );
});

test('storage that is missing, throws, or holds junk degrades to no preference', () => {
  assert.equal(readExploredStage(null), '');
  assert.equal(rememberExplored(null, 'meet'), false);
  const throwing = {
    getItem() {
      throw new Error('denied');
    },
    setItem() {
      throw new Error('denied');
    },
    removeItem() {
      throw new Error('denied');
    }
  };
  assert.equal(readExploredStage(throwing), '');
  assert.equal(rememberExplored(throwing, 'meet'), false);
  assert.equal(readExploredStage(memoryStorage({ [WELCOME_STORAGE_KEY]: '{oops' })), '');
  assert.equal(
    readExploredStage(memoryStorage({ [WELCOME_STORAGE_KEY]: '{"explored":"active"}' })),
    ''
  );
});

test('the card renders one primary, Explore first, and a journey marking the current step', () => {
  const html = welcomeCardHTML(homeWelcomeView({ relationshipState: 'needs_hire', ...EMPTY }));
  assert.match(html, /aria-labelledby="homeWelcomeTitle"/);
  assert.match(html, /<h3 id="homeWelcomeTitle"[^>]*>Meet your assistant\.<\/h3>/);
  assert.equal(html.match(/data-home-welcome-action="primary"/g).length, 1);
  assert.equal(html.match(/data-home-welcome-action="explore"/g).length, 1);
  assert.match(html, /<li class="home-welcome-step is-current" aria-current="step">/);
  assert.equal(html.match(/aria-current="step"/g).length, 1);
  assert.equal(welcomeCardHTML({ show: false }), '');
});

test('the HQ-stage card names the assistant safely and marks Meet done', () => {
  const view = homeWelcomeView({ relationshipState: 'needs_hq', ...EMPTY });
  const html = welcomeCardHTML(view, { assistantName: '<b>Robin</b>' });
  assert.match(html, /&lt;b&gt;Robin&lt;\/b&gt; is ready/);
  assert.doesNotMatch(html, /<b>Robin<\/b>/);
  assert.match(html, /Build My HQ/);
  assert.match(html, /is-done[^>]*>.*✓.*Meet.*\(done\)/s);
  assert.match(html, /aria-hidden="true">&lt;</, 'the avatar initial is escaped');
});
