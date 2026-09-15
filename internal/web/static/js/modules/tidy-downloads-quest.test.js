// Tests for tidy-downloads-quest.js — Ori's deterministic Mission 02 walkthrough.
//   node --test internal/web/static/js/modules/tidy-downloads-quest.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const questSrc = readFileSync(new URL('./tidy-downloads-quest.js', import.meta.url), 'utf8');
const hqQuestSrc = readFileSync(new URL('./personal-hq-quest.js', import.meta.url), 'utf8');
const setupJourneySrc = readFileSync(new URL('./setup-journey.js', import.meta.url), 'utf8');
const onboardingSrc = readFileSync(new URL('./onboarding.js', import.meta.url), 'utf8');

// Onboarding finished and Mission 02 open with its walkthrough action: the
// state the Quests card's Start is offered in.
function eligibleResponses({
  needsOnboarding = false,
  status = 'available',
  actionURL = '/?quest=tidy-downloads',
  missing = false
} = {}) {
  const missions = missing
    ? []
    : [
        { id: 't2-build-hq', order: 1, status: 'completed', action_url: '/?quest=build-hq' },
        { id: 'pa-tidy-downloads', order: 2, status, action_url: actionURL }
      ];
  return {
    '/api/onboarding/status': { needs_onboarding: needsOnboarding },
    '/api/progression': { missions }
  };
}

function load({
  src = questSrc,
  search = '',
  responses = eligibleResponses(),
  guideOverrides = {},
  withCreator = true
} = {}) {
  const calls = {
    fetches: [],
    posts: [],
    presented: [],
    cleared: 0,
    opened: 0,
    creatorOpens: [],
    creatorHides: 0,
    replaced: []
  };
  const dispatched = [];
  const listeners = { window: {}, document: {}, modal: {} };

  const guide = {
    open: (trigger, options) => {
      calls.opened += 1;
      calls.openOptions = options;
    },
    isOpen: () => false,
    presentQuestStep: step => {
      calls.presented.push(step);
      return { rendered: true, coachmarkResolved: false };
    },
    clearQuestStep: () => {
      calls.cleared += 1;
    },
    ...guideOverrides
  };

  const modal = {
    dataset: {},
    addEventListener: (type, fn) => {
      listeners.modal[type] = listeners.modal[type] || [];
      listeners.modal[type].push(fn);
    }
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
    fetch: (url, init) => {
      const method = (init && init.method) || 'GET';
      if (method !== 'GET') {
        calls.posts.push({ url: String(url), method, body: JSON.parse(init.body || '{}') });
        return Promise.resolve({ ok: true, status: 200, json: () => ({}) });
      }
      calls.fetches.push(String(url));
      const body = responses[String(url)];
      if (body === undefined) return Promise.resolve({ ok: false, status: 500 });
      return Promise.resolve({ ok: true, json: () => Promise.resolve(body) });
    }
  };
  sandbox.window = {
    location: { href: 'http://localhost/' + search, search, pathname: '/' },
    history: {
      state: null,
      replaceState: (_state, _title, url) => calls.replaced.push(url)
    },
    OriGuide: guide,
    OriHomeCockpit: { setView() {} },
    sessionManager: withCreator
      ? {
          showAddWorkspaceModal: options => calls.creatorOpens.push(options),
          hideWorkspaceCreatorModal: () => {
            calls.creatorHides += 1;
          }
        }
      : undefined,
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
    getElementById: id => (id === 'addFolderModal' ? modal : null),
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
  vm.runInContext(src, sandbox);

  return {
    quest: sandbox.window.OriTidyDownloadsQuest,
    hqQuest: sandbox.window.OriPersonalHQQuest,
    calls,
    dispatched,
    modal,
    fireWindow(type, detail) {
      (listeners.window[type] || []).forEach(fn => fn({ type, detail: detail || {} }));
    },
    fireDocument(type, detail) {
      (listeners.document[type] || []).forEach(fn => fn({ type, detail: detail || {} }));
    },
    fireModal(type) {
      (listeners.modal[type] || []).forEach(fn => fn({ type }));
    }
  };
}

// settle lets the controller's awaited fetches resolve.
const settle = () => new Promise(resolve => setImmediate(resolve));

/* ---- activation ---------------------------------------------------------- */

test('the walkthrough is inert without its quest route: no request at all', async () => {
  const { quest, calls } = load({ search: '' });
  await settle();
  assert.equal(quest.isActive(), false);
  assert.deepEqual(calls.presented, []);
  assert.deepEqual(calls.fetches, [], 'a plain Home load must add no request');
  assert.deepEqual(calls.creatorOpens, []);
});

test('the walkthrough opens the creator with File Janitor and presents step 1 of 2', async () => {
  const { quest, calls } = load({ search: '?quest=tidy-downloads' });
  await settle();
  assert.equal(quest.isActive(), true);

  assert.equal(calls.creatorOpens.length, 1);
  const options = calls.creatorOpens[0];
  assert.equal(options.blueprint, 'file-janitor');
  assert.equal(options.entryPoint, 'tidy_downloads_quest');
  assert.equal(typeof options.onCreated, 'function');

  assert.equal(calls.presented.length, 1);
  const step = calls.presented[0];
  assert.equal(step.quest, 'tidy-downloads');
  assert.equal(step.index, 1);
  assert.equal(step.total, 2);
  // Create is hidden until the creator's last step, so nothing is marked yet
  // and the guide never says it cannot point at the control.
  assert.equal(step.coachmark, '');
  assert.equal(
    step.answer,
    'Let’s give Ori one folder to tidy. File Janitor is already selected. Review the name and team, then Create.'
  );
  assert.equal(step.note, 'Nothing runs until you approve it in the next screen.');
  assert.equal(step.choices.length, 0);

  assert.equal(calls.opened, 1);
  assert.equal(calls.openOptions.skipGreeting, true);
  assert.deepEqual(calls.replaced, ['/'], '?quest= is dropped without a history entry');
});

test('eligibility comes from the server, never from the URL alone', async () => {
  const cases = [
    ['onboarding still open', eligibleResponses({ needsOnboarding: true }), false],
    ['mission completed', eligibleResponses({ status: 'completed' }), false],
    [
      'a File Janitor workspace already exists',
      eligibleResponses({ actionURL: '/workspaces/tidy-downloads' }),
      false
    ],
    ['mission absent from the graph', eligibleResponses({ missing: true }), false],
    ['happy path', eligibleResponses(), true],
    // Resume from the Quests list uses this same URL, so a deferred mission
    // must still start.
    ['deferred mission resumed', eligibleResponses({ status: 'skipped' }), true]
  ];
  for (const [name, responses, want] of cases) {
    const { quest, calls } = load({ search: '?quest=tidy-downloads', responses });
    await settle();
    assert.equal(quest.isActive(), want, `${name}: active`);
    assert.equal(calls.creatorOpens.length, want ? 1 : 0, `${name}: creator opened`);
    assert.equal(calls.presented.length, want ? 1 : 0, `${name}: step presented`);
  }
});

test('an unreadable status refuses to start', async () => {
  const responses = eligibleResponses();
  delete responses['/api/progression'];
  const { quest, calls } = load({ search: '?quest=tidy-downloads', responses });
  await settle();
  assert.equal(quest.isActive(), false);
  assert.deepEqual(calls.creatorOpens, []);
});

test('a page without the creator does nothing', async () => {
  const { quest, calls } = load({ search: '?quest=tidy-downloads', withCreator: false });
  await settle();
  assert.equal(quest.isActive(), false);
  assert.deepEqual(calls.presented, []);
});

/* ---- creator steps ------------------------------------------------------- */

test('each creator step re-presents step 1, and the mark lands on Create at the last step', async () => {
  const { calls, fireWindow } = load({ search: '?quest=tidy-downloads' });
  await settle();
  fireWindow('ori:workspace-creator-step', { step: 2, entryPoint: 'tidy_downloads_quest' });
  fireWindow('ori:workspace-creator-step', {
    step: 4,
    entryPoint: 'tidy_downloads_quest',
    final: true
  });
  assert.equal(calls.presented.length, 3);
  assert.deepEqual(
    calls.presented.map(step => [step.index, step.coachmark]),
    [
      [1, ''],
      [1, ''],
      [1, 'create_workspace_submit']
    ]
  );

  // Going Back from Review withdraws the mark again.
  fireWindow('ori:workspace-creator-step', { step: 3, entryPoint: 'tidy_downloads_quest' });
  assert.equal(calls.presented.at(-1).coachmark, '');
});

test("another caller's creator steps are ignored", async () => {
  const { calls, fireWindow } = load({ search: '?quest=tidy-downloads' });
  await settle();
  fireWindow('ori:workspace-creator-step', { step: 4, entryPoint: 'home_cockpit_create' });
  assert.equal(calls.presented.length, 1);
});

test('a successful create presents step 2 of 2 and ends without clearing it', async () => {
  const { quest, calls, fireModal, fireWindow } = load({ search: '?quest=tidy-downloads' });
  await settle();
  calls.creatorOpens[0].onCreated({ workspaceId: 'ws-1', folder: { slug: 'tidy-downloads' } });

  const step2 = calls.presented.at(-1);
  assert.equal(step2.index, 2);
  assert.equal(step2.total, 2);
  assert.equal(step2.answer, 'Created. Opening its setup so you can choose the folder.');
  assert.equal(step2.coachmark, '');
  assert.equal(step2.choices.length, 0);
  assert.equal(quest.isActive(), false);
  // The page navigates to the workspace next; the message stays until then.
  assert.equal(calls.cleared, 0);

  // The dialog closing after the create, or a later creator event, changes
  // nothing.
  fireModal('hidden.bs.modal');
  fireWindow('ori:workspace-creator-step', { step: 1, entryPoint: 'tidy_downloads_quest' });
  assert.equal(calls.cleared, 0);
  assert.equal(calls.presented.length, 2);
});

test('closing the creator without creating pauses the presentation only', async () => {
  const { quest, calls, fireModal } = load({ search: '?quest=tidy-downloads' });
  await settle();
  fireModal('hidden.bs.modal');
  assert.equal(quest.isActive(), false);
  assert.equal(calls.cleared, 1);
  assert.deepEqual(calls.posts, [], 'closing must not defer or complete the mission');
});

test('a mid-draft suspension of the creator is not a close', async () => {
  const { quest, calls, fireModal, modal } = load({ search: '?quest=tidy-downloads' });
  await settle();
  modal.dataset.suspendedForAgentSetup = 'true';
  fireModal('hidden.bs.modal');
  assert.equal(quest.isActive(), true);
  assert.equal(calls.cleared, 0);
});

test('Back and page-hide clear the mark without deferring the mission', async () => {
  for (const event of ['popstate', 'pagehide']) {
    const { quest, calls, fireWindow } = load({ search: '?quest=tidy-downloads' });
    await settle();
    fireWindow(event, {});
    assert.equal(quest.isActive(), false, `${event} left the walkthrough active`);
    assert.equal(calls.cleared, 1, `${event} left a stale mark`);
    assert.deepEqual(calls.posts, [], `${event} was recorded as a deferral`);
  }
});

/* ---- exits --------------------------------------------------------------- */

// Every step is shown while the creator dialog owns the screen, where a panel
// button could not be pressed. The reachable exits are the creator's Cancel
// (which pauses, above) and the Quests card's own Do this later.
test('no panel choice is ever offered, so nothing unreachable is promised', async () => {
  const { calls, fireWindow } = load({ search: '?quest=tidy-downloads' });
  await settle();
  fireWindow('ori:workspace-creator-step', {
    step: 4,
    entryPoint: 'tidy_downloads_quest',
    final: true
  });
  calls.creatorOpens[0].onCreated({ workspaceId: 'ws-1' });
  assert.ok(calls.presented.length >= 3);
  for (const step of calls.presented) {
    assert.equal(step.choices.length, 0, `step ${step.index} offered a choice`);
  }
});

test('a panel choice event for this quest changes nothing', async () => {
  const { quest, calls, fireDocument } = load({ search: '?quest=tidy-downloads' });
  await settle();
  fireDocument('ori-guide:quest-choice', { quest: 'tidy-downloads', choice: 'defer' });
  await settle();
  assert.equal(quest.isActive(), true);
  assert.deepEqual(calls.posts, []);
  assert.equal(calls.creatorHides, 0);
});

/* ---- boundaries --------------------------------------------------------- */

test('the walkthrough never asks the guide API, a model, or the janitor API', async () => {
  const { calls, fireWindow } = load({ search: '?quest=tidy-downloads' });
  await settle();
  fireWindow('ori:workspace-creator-step', { step: 4, entryPoint: 'tidy_downloads_quest' });
  calls.creatorOpens[0].onCreated({ workspaceId: 'ws-1' });

  assert.deepEqual(calls.fetches, ['/api/onboarding/status', '/api/progression']);
  assert.deepEqual(calls.posts, [], 'the walkthrough makes no write request');
  assert.doesNotMatch(questSrc, /ori-guide['"`]|home-assistant|file-janitor\/|janitor\/api/);
  // It opens the creator; it never selects, submits, or clicks.
  assert.doesNotMatch(questSrc, /\.click\(|\.submit\(|requestSubmit|dispatchEvent\(new MouseEvent/);
});

test('every step is copy plus at most one registered coachmark key', async () => {
  const { quest } = load({ search: '' });
  for (const [step, final] of [
    [quest.STEP_CREATE, false],
    [quest.STEP_CREATE, true],
    [quest.STEP_OPENING, true]
  ]) {
    const key = quest._coachmarkFor(step, final);
    assert.ok(key === '' || key === 'create_workspace_submit', `step ${step} names ${key}`);
    for (const bad of ['#', '.', '[', ' ', '>']) {
      assert.ok(!key.includes(bad), `step ${step} coachmark looks like a selector`);
    }
  }
});

/* ---- parameter isolation ------------------------------------------------ */

test('other quest links never start this walkthrough', async () => {
  for (const search of [
    '?quest=build-hq',
    '?quest=plan-first-day',
    '?setup=quest&source=host&quest=email_ops_setup'
  ]) {
    const { quest, calls } = load({ search });
    await settle();
    assert.equal(quest.isActive(), false, `${search} started Mission 02`);
    assert.deepEqual(calls.fetches, [], `${search} made an eligibility request`);
    assert.deepEqual(calls.creatorOpens, [], `${search} opened the creator`);
  }
});

test('?quest=tidy-downloads never starts the Personal HQ walkthrough', async () => {
  const { hqQuest, calls } = load({ src: hqQuestSrc, search: '?quest=tidy-downloads' });
  await settle();
  assert.equal(hqQuest.isActive(), false);
  assert.deepEqual(calls.fetches, [], 'the HQ walkthrough made an eligibility request');
  assert.deepEqual(calls.presented, []);
});

test('?quest=tidy-downloads never starts the setup journey or the first-day plan', () => {
  // The setup journey opens only on ?setup=quest (or ?setup=specialist).
  assert.match(setupJourneySrc, /if \(params\.get\('setup'\) === 'quest'\) \{/);
  assert.doesNotMatch(setupJourneySrc, /get\('quest'\) === 'tidy-downloads'/);
  // The first-day plan opens only on its own exact value.
  assert.match(onboardingSrc, /if \(requested !== 'plan-first-day'\) return false;/);
});
