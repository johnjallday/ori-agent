// Tests for meet-assistant-quest.js — Ori's deterministic Mission 01 walkthrough.
//   node --test internal/web/static/js/modules/meet-assistant-quest.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const questSrc = readFileSync(new URL('./meet-assistant-quest.js', import.meta.url), 'utf8');

function load({
  search = '',
  relationship = 'needs_hire',
  presetOpen = false,
  guideOpen = false
} = {}) {
  const calls = { fetches: [], presented: [], cleared: 0, opened: 0, replaced: [] };
  const listeners = { window: {}, document: {} };

  const guide = {
    open: (trigger, options) => {
      calls.opened += 1;
      calls.openOptions = options;
    },
    isOpen: () => guideOpen,
    presentQuestStep: step => {
      calls.presented.push(step);
      return { rendered: true, coachmarkResolved: true };
    },
    clearQuestStep: () => {
      calls.cleared += 1;
    }
  };

  // Just enough of the preset for presetOpen() and the form's change events.
  const page = {
    panelHidden: !presetOpen,
    presetMounted: presetOpen
  };
  const elements = {
    createPanel: () => ({ hidden: page.panelHidden }),
    'cr-focus-group': () => (page.presetMounted ? {} : null)
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
      if (String(url) === '/api/personal-assistant') {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ personal_assistant: { state: relationship } })
        });
      }
      return Promise.resolve({ ok: false, status: 500 });
    }
  };
  sandbox.window = {
    location: { href: 'http://localhost/agents' + search, search, pathname: '/agents' },
    history: {
      state: { agent: null },
      replaceState: (stateObj, _title, url) => calls.replaced.push({ stateObj, url }),
      pushState: () => {
        throw new Error('the walkthrough must never add a history entry');
      }
    },
    OriGuide: guide,
    addEventListener: (type, fn) => {
      listeners.window[type] = listeners.window[type] || [];
      listeners.window[type].push(fn);
    }
  };
  sandbox.document = {
    readyState: 'complete',
    getElementById: id => (elements[id] ? elements[id]() : null),
    addEventListener: (type, fn) => {
      listeners.document[type] = listeners.document[type] || [];
      listeners.document[type].push(fn);
    }
  };
  sandbox.globalThis = sandbox;
  sandbox.fetch = sandbox.fetch.bind(sandbox);
  vm.createContext(sandbox);
  vm.runInContext(questSrc, sandbox);

  const fire = (target, type, detail) =>
    (listeners[target][type] || []).forEach(fn => fn({ type, detail: detail || {} }));

  return {
    quest: sandbox.window.OriMeetAssistantQuest,
    calls,
    page,
    // The roster announces every create-panel render.
    openPanel(mode = 'assistant') {
      page.panelHidden = false;
      page.presetMounted = mode === 'assistant';
      fire('window', 'ori:agent-create-opened', { mode });
    },
    // A change event from a control inside the preset form.
    change(control, value = 'x') {
      const inFocus = control === 'focus';
      const target = {
        id: inFocus ? '' : control,
        value,
        closest: selector => {
          if (selector === '#createForm') return {};
          if (selector === '#cr-focus-group') return inFocus ? {} : null;
          return null;
        }
      };
      (listeners.document.change || []).forEach(fn => fn({ type: 'change', target }));
    },
    choose(choice, quest = 'meet-assistant') {
      fire('document', 'ori-guide:quest-choice', { quest, choice });
    },
    closeGuide() {
      fire('document', 'ori-guide:dismiss');
    },
    openGuide() {
      fire('document', 'ori-guide:open');
    }
  };
}

const settle = () => new Promise(resolve => setImmediate(resolve));
const steps = calls => calls.presented.map(step => step.index);

/* ---- activation ---------------------------------------------------------- */

test('the walkthrough starts on the Agents page while the assistant is unhired', async () => {
  for (const relationship of ['needs_hire', 'hiring']) {
    const { quest, calls } = load({ relationship });
    await settle();
    assert.equal(quest.isActive(), true, relationship);
    assert.deepEqual(steps(calls), [1]);
    const first = calls.presented[0];
    assert.equal(first.quest, 'meet-assistant');
    assert.equal(first.total, 5);
    assert.equal(first.coachmark, 'new_agent');
    assert.match(first.answer, /^Press New Agent\./);
    // skipGreeting: the guide's own greeting would land after and replace the step.
    assert.equal(calls.opened, 1);
    assert.equal(calls.openOptions.skipGreeting, true);
  }
});

test('a hired or repair-needed relationship has no walkthrough, even when asked for', async () => {
  for (const relationship of ['needs_hq', 'provisioning_hq', 'active', 'paused', 'repair_needed']) {
    const { quest, calls } = load({ search: '?quest=meet-assistant', relationship });
    await settle();
    assert.equal(quest.isActive(), false, relationship);
    assert.deepEqual(calls.presented, []);
    assert.equal(calls.opened, 0);
  }
});

test('the quest param is read at load and stripped on start without a history entry', async () => {
  const { quest, calls } = load({ search: '?quest=meet-assistant&view=list' });
  assert.equal(quest.requestedAtLoad(), true);
  await settle();
  assert.equal(calls.replaced.length, 1);
  assert.equal(calls.replaced[0].url, '/agents?view=list');
  // The roster's own history state is kept, not replaced with null.
  assert.deepEqual(calls.replaced[0].stateObj, { agent: null });
});

test('the walkthrough never asks /api/ori-guide or any model', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  loaded.change('cr-name', 'Atlas');
  loaded.change('focus');
  loaded.change('cr-mandate', 'Keep it light.');
  assert.deepEqual(loaded.calls.fetches, ['/api/personal-assistant']);
});

test('a New Agent press that beat the start begins at the name step', async () => {
  const { calls } = load({ presetOpen: true });
  await settle();
  assert.deepEqual(steps(calls), [2]);
  // It is not a user action on the guide, so focus stays where the roster put it.
  assert.equal(calls.presented[0].focus, false);
});

/* ---- advancement --------------------------------------------------------- */

test('each form signal advances exactly one step, in order, to Hire', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  assert.deepEqual(steps(loaded.calls), [1, 2]);
  assert.equal(loaded.calls.presented.at(-1).coachmark, 'assistant_name');

  loaded.change('cr-name', 'Atlas');
  assert.deepEqual(steps(loaded.calls), [1, 2, 3]);
  assert.equal(loaded.calls.presented.at(-1).coachmark, 'assistant_face');

  loaded.change('focus');
  assert.deepEqual(steps(loaded.calls), [1, 2, 3, 4]);
  assert.equal(loaded.calls.presented.at(-1).coachmark, 'assistant_focus');

  loaded.change('cr-mandate', 'Keep it light.');
  assert.deepEqual(steps(loaded.calls), [1, 2, 3, 4, 5]);
  const hire = loaded.calls.presented.at(-1);
  assert.equal(hire.coachmark, 'assistant_hire');
  assert.match(hire.answer, /no workspace, no permissions, no accounts/);

  // Nothing moves past Hire.
  loaded.change('focus');
  assert.deepEqual(steps(loaded.calls), [1, 2, 3, 4, 5]);
});

test('a step the form advanced to never takes focus out of the form', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  loaded.change('cr-name', 'Atlas');
  loaded.change('focus');
  loaded.change('focus');
  // Steps 3, 4 and 5 came from the form: a Space meant for a checkbox must
  // never land on a focused Hire button.
  for (const step of loaded.calls.presented.slice(2)) {
    assert.equal(step.focus, false, `step ${step.index} moved focus`);
  }
});

test('the choices advance one step each and focus the control they name', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  // Spread: the step objects come from the vm realm, so their arrays are not
  // this realm's Array.
  assert.deepEqual(
    [...loaded.calls.presented.at(-1).choices.map(choice => choice.id)],
    ['keep-name']
  );
  loaded.choose('keep-name');
  loaded.choose('keep-face');
  loaded.choose('done-choosing');
  assert.deepEqual(steps(loaded.calls), [1, 2, 3, 4, 5]);
  assert.equal(loaded.calls.presented.at(-1).focus, true, 'Done choosing hands focus to Hire');
  assert.equal(loaded.calls.presented.at(-1).choices.length, 0);
});

test('a stale signal never moves the walkthrough backwards or sideways', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  loaded.change('focus');
  loaded.change('focus');
  loaded.change('focus');
  assert.equal(loaded.quest._state.step, 5);

  const before = loaded.calls.presented.length;
  loaded.change('cr-name', 'Renamed');
  loaded.choose('keep-name');
  loaded.choose('keep-face');
  loaded.choose('done-choosing');
  loaded.choose('keep-name', 'build-hq');
  assert.equal(loaded.calls.presented.length, before);
  assert.equal(loaded.quest._state.step, 5);
});

test('an empty name or mandate is not progress', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  loaded.change('cr-name', '   ');
  loaded.change('cr-mandate', '  ');
  assert.equal(loaded.quest._state.step, 2);
});

test('a re-render re-anchors the current step instead of restarting it', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  loaded.change('cr-name', 'Atlas');
  loaded.openPanel();
  assert.deepEqual(steps(loaded.calls), [1, 2, 3, 3]);
  assert.equal(loaded.calls.presented.at(-1).focus, false);
});

/* ---- detours and pauses -------------------------------------------------- */

test('choosing the ordinary form stops pointing without skipping, and coming back resumes', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  loaded.change('cr-name', 'Atlas');

  loaded.openPanel('standard');
  assert.equal(loaded.calls.cleared, 1, 'the mark stays on a form that is gone');
  assert.equal(loaded.quest.isActive(), true, 'the mission was abandoned');
  // The ordinary form has a name field too; editing it is not Mission 01 progress.
  loaded.change('cr-name', 'Scout');
  loaded.change('focus');
  assert.equal(loaded.quest._state.step, 3);

  const before = loaded.calls.presented.length;
  loaded.openPanel('assistant');
  assert.equal(loaded.calls.presented.length, before + 1);
  assert.equal(loaded.calls.presented.at(-1).index, 3);
});

test('with Ori’s panel closed the form alone still advances, and reopening resumes there', async () => {
  const loaded = load();
  await settle();
  loaded.openPanel();
  loaded.closeGuide();

  const before = loaded.calls.presented.length;
  loaded.change('cr-name', 'Atlas');
  loaded.change('focus');
  assert.equal(loaded.calls.presented.length, before, 'a closed panel was presented to');
  assert.equal(loaded.quest._state.step, 4);

  loaded.openGuide();
  assert.equal(loaded.calls.presented.at(-1).index, 4);
  assert.equal(loaded.calls.presented.at(-1).focus, false);
});

test('stop ends the presentation and reports nothing to the server', async () => {
  const loaded = load();
  await settle();
  loaded.quest.stop();
  assert.equal(loaded.quest.isActive(), false);
  assert.equal(loaded.calls.cleared, 1);
  assert.deepEqual(loaded.calls.fetches, ['/api/personal-assistant']);
});

test('the walkthrough is inert when the guide is not on the page', async () => {
  const loaded = load();
  loaded.quest.stop();
  await settle();
  const bare = load();
  bare.calls.presented.length = 0;
  // A page without the guide: nothing may throw.
  assert.doesNotThrow(() => bare.openPanel());
});
