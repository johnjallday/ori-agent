import { test } from 'node:test';
import assert from 'node:assert/strict';
import { HQ_LABELS, tacticalTermsIn } from './hq-labels.js';
import {
  createHQGreeting,
  greetingChoiceHref,
  greetingHolds,
  greetingLines,
  hqGreetingGate,
  joinNames
} from './personal-assistant-hq-greeting.js';
import { DOCUMENTS_FOLDER_URL, EXPLORE_FOLDER_URL } from './personal-assistant-panel.js';

// ── pure ───────────────────────────────────────────────────────────────────────

const READY = { show: true };
const GATE_BASE = { active: true, view: READY, personal: true, loading: false, hasMessages: false };

test('the gate: the first rule that matches wins', () => {
  const gate = patch => hqGreetingGate({ ...GATE_BASE, ...patch });
  assert.equal(gate({}), 'show');
  // Not a pending HQ, or nothing to greet: off, whatever else is true.
  assert.equal(gate({ active: false }), 'off');
  assert.equal(gate({ active: false, hasMessages: true }), 'off');
  assert.equal(gate({ view: { show: false } }), 'off');
  assert.equal(gate({ view: { show: false }, hasMessages: true }), 'off');
  // The person started talking.
  assert.equal(gate({ hasMessages: true }), 'hide');
  assert.equal(gate({ hasMessages: true, view: null, loading: true }), 'hide');
  // Not known yet: wait, and draw nothing.
  assert.equal(gate({ view: null }), 'wait');
  assert.equal(gate({ view: undefined }), 'wait');
  assert.equal(gate({ loading: true }), 'wait', 'a conversation still loading may hold messages');
  assert.equal(gate({ personal: false }), 'wait', 'a recipient that is not the assistant yet');
  assert.equal(hqGreetingGate(), 'off');
});

test('the Notes question is held while the greeting shows or may still show', () => {
  assert.equal(greetingHolds('show'), true);
  assert.equal(greetingHolds('wait'), true);
  assert.equal(greetingHolds('hide'), false, 'a message was sent: released');
  assert.equal(greetingHolds('off'), false, 'Hide suggestions, all resolved, not a pending HQ');
});

test('names join as "A", "A and B", "A, B and C"', () => {
  assert.equal(joinNames([]), '');
  assert.equal(joinNames(['A']), 'A');
  assert.equal(joinNames(['A', 'B']), 'A and B');
  assert.equal(joinNames(['A', 'B', 'C']), 'A, B and C');
  assert.equal(joinNames([' A ', '', null, 'B']), 'A and B');
  assert.equal(joinNames(null), '');
});

test('the lines: one name, several, none; the pointing line only follows a running line', () => {
  const lines = view => greetingLines(view);
  const many = lines({
    running: ['Notes', 'Watchtower', 'Daily Brief', 'Memory'],
    off: ['Email', 'Calendar Ops']
  });
  assert.equal(many.title, 'Your HQ is ready.');
  assert.equal(many.running, 'Notes, Watchtower, Daily Brief and Memory are already running.');
  assert.equal(many.point, "Point at one on the page and I'll tell you what it does.");
  assert.equal(many.off, 'Email and Calendar Ops are off until you set them up.');
  assert.equal(many.ask, 'Where would you like to start?');
  assert.equal(many.footer, 'Or just ask me. These stay under Things to try.');

  const one = lines({ running: ['Memory'], off: ['Email'] });
  assert.equal(one.running, 'Memory is already running.');
  assert.equal(one.off, 'Email is off until you set it up.');
  assert.ok(one.point);

  const none = lines({ running: [], off: ['Notes', 'Email'] });
  assert.equal(none.running, '', 'no names: the line is left out');
  assert.equal(none.point, '', 'and so is the line that points at them');
  assert.equal(none.off, 'Notes and Email are off until you set them up.');

  const allOn = lines({ running: ['Notes', 'Memory'], off: [] });
  assert.equal(allOn.off, '', 'nothing off: that line is left out');
  assert.ok(allOn.running && allOn.point);

  const empty = lines({});
  assert.deepEqual([empty.running, empty.point, empty.off], ['', '', '']);
  assert.deepEqual(
    [lines(null).running, lines(undefined).off],
    ['', ''],
    'a missing view says nothing rather than throw'
  );
});

test('no greeting string, with any names, carries a tactical word, "task" or "second brain"', () => {
  const view = {
    running: ['Notes', 'Watchtower', 'Daily Brief', 'Memory'],
    off: ['Email', 'Calendar Ops']
  };
  for (const [key, line] of Object.entries(greetingLines(view))) {
    assert.deepEqual(tacticalTermsIn(line), [], key);
    assert.doesNotMatch(line, /\btasks?\b|second brain/i, key);
  }
  for (const key of Object.keys(HQ_LABELS).filter(name => name.startsWith('greeting'))) {
    assert.deepEqual(tacticalTermsIn(HQ_LABELS[key]), [], key);
  }
});

test('a choice is a link only for the folder; the rest are buttons the page runs', () => {
  assert.equal(greetingChoiceHref({ action: 'folder' }), '/');
  assert.equal(greetingChoiceHref({ action: 'home' }), '/');
  for (const action of ['brief', 'note', 'notes', '', undefined])
    assert.equal(greetingChoiceHref({ action }), '', String(action));
  assert.equal(greetingChoiceHref(null), '');
  assert.equal(DOCUMENTS_FOLDER_URL, '/?panel=today&folder=show&suggest=documents');
  assert.equal(EXPLORE_FOLDER_URL, '/?panel=today&folder=explore');
});

// ── the controller, against a fake DOM ─────────────────────────────────────────

let focusLog = [];

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = tag.toUpperCase();
    this.attributes = new Map();
    this.children = [];
    this.parentNode = null;
    this.className = '';
    this.id = '';
    this.hidden = false;
    this._text = '';
    this.innerHTML = '';
    this.listeners = new Map();
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }
  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }
  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent).join('');
  }
  set textContent(value) {
    this.children = [];
    this._text = String(value);
  }
  contains(node) {
    for (let at = node; at; at = at.parentNode) if (at === this) return true;
    return false;
  }
  matchesSelector(selector) {
    const match = selector.match(/^\[([\w-]+)(?:="([^"]*)")?\]$/);
    if (!match) return false;
    const value = this.getAttribute(match[1]);
    return value !== null && (match[2] === undefined || value === match[2]);
  }
  closest(selector) {
    for (let at = this; at; at = at.parentNode) if (at.matchesSelector?.(selector)) return at;
    return null;
  }
  querySelector(selector) {
    for (const child of this.children) {
      if (child.matchesSelector(selector)) return child;
      const found = child.querySelector(selector);
      if (found) return found;
    }
    return null;
  }
  queryAll(selector) {
    return this.children.flatMap(child => [
      ...(child.matchesSelector(selector) ? [child] : []),
      ...child.queryAll(selector)
    ]);
  }
  addEventListener(type, fn) {
    this.listeners.set(type, fn);
  }
  click(target = this) {
    this.listeners.get('click')?.({ target });
  }
  focus() {
    focusLog.push(this);
    this.ownerDoc.activeElement = this;
  }
}

function setup(overrides = {}) {
  focusLog = [];
  const ids = new Map();
  const doc = {
    activeElement: null,
    createElement: tag => {
      const el = new FakeElement(tag);
      el.ownerDoc = doc;
      return el;
    },
    getElementById: id => ids.get(id) || null
  };
  const mount = doc.createElement('div');
  mount.hidden = true;
  const input = doc.createElement('textarea');
  const launcher = doc.createElement('button');
  ids.set('personalAssistantInput', input);
  ids.set('personalAssistantLauncher', launcher);
  const state = {
    view: {
      show: true,
      running: ['Notes', 'Watchtower', 'Daily Brief', 'Memory'],
      off: ['Email', 'Calendar Ops'],
      steps: [
        {
          key: 'folder',
          action: 'folder',
          title: 'Create a workspace from Documents',
          hint: 'Review the agent first.',
          state: 'open',
          resolved: false
        },
        {
          key: 'brief',
          action: 'brief',
          title: 'Read your Daily Brief',
          hint: 'A little perspective.',
          state: 'open',
          resolved: false
        },
        {
          key: 'note',
          action: 'note',
          title: 'Save an idea',
          hint: 'A quick note.',
          state: 'open',
          resolved: false
        }
      ]
    },
    personal: true,
    loading: false,
    messages: false,
    open: false,
    ran: [],
    closed: [],
    dispatched: [],
    queued: [],
    runResult: true
  };
  const greeting = createHQGreeting({
    mount,
    active: true,
    doc,
    view: () => state.view,
    personal: () => state.personal,
    loading: () => state.loading,
    hasMessages: () => state.messages,
    who: () => ({ name: 'Atlas', appearance: {} }),
    avatar: () => '<i>face</i>',
    run: (action, trigger) => {
      state.ran.push([action, trigger]);
      return state.runResult;
    },
    closeDrawer: options => state.closed.push(options),
    drawerOpen: () => state.open,
    focusInput: () => input.focus(),
    dispatch: holds => state.dispatched.push(holds),
    queue: fn => state.queued.push(fn),
    ...overrides
  });
  return { greeting, mount, doc, state, input, launcher };
}

const shown = mount => !mount.hidden && mount.textContent.length > 0;

test('it draws the card with its lines, its author and the three choices, and never takes focus', () => {
  const { greeting, mount, state } = setup();
  greeting.evaluate();
  assert.equal(shown(mount), true);
  const words = mount.textContent;
  assert.match(words, /Atlas/);
  assert.match(words, /Your HQ is ready\./);
  assert.match(words, /Notes, Watchtower, Daily Brief and Memory are already running\./);
  assert.match(words, /Point at one on the page and I'll tell you what it does\./);
  assert.match(words, /Email and Calendar Ops are off until you set them up\./);
  assert.match(words, /Where would you like to start\?/);
  assert.match(words, /Or just ask me\. These stay under Things to try\./);
  const choices = mount.queryAll('[data-hq-greeting-choice]');
  assert.deepEqual(
    choices.map(choice => choice.getAttribute('data-hq-greeting-choice')),
    ['folder', 'brief', 'note']
  );
  assert.equal(choices[0].tagName, 'A');
  assert.equal(choices[0].getAttribute('href'), '/');
  assert.equal(choices[1].tagName, 'BUTTON');
  assert.equal(choices[1].getAttribute('type'), 'button');
  assert.match(choices[1].textContent, /Read your Daily Brief/);
  assert.equal(focusLog.length, 0, 'showing it never calls focus()');
  assert.equal(state.dispatched.length, 1);
  // Landmark and naming: labelled by its title, nothing announces it.
  const card = mount.children[0];
  assert.equal(card.getAttribute('aria-labelledby'), 'paHQGreetingTitle');
  for (const el of [card, mount]) {
    assert.equal(el.getAttribute('role'), null);
    assert.equal(el.getAttribute('aria-live'), null);
    assert.equal(el.getAttribute('tabindex'), null);
  }
});

test('names are text, never markup', () => {
  const { greeting, mount, state } = setup();
  state.view = { ...state.view, running: ['<img src=x onerror=alert(1)>'], off: [] };
  greeting.evaluate();
  assert.match(mount.textContent, /<img src=x onerror=alert\(1\)> is already running\./);
  assert.equal(mount.queryAll('[src]').length, 0);
});

test('resolved choices are ticked, with the word for screen readers, and stay actionable', () => {
  const { greeting, mount, state } = setup();
  state.view = {
    ...state.view,
    steps: [
      {
        key: 'folder',
        action: 'home',
        title: 'Explore a folder with Ori',
        hint: 'Your folder is on the map.',
        state: 'completed',
        resolved: true
      },
      {
        key: 'brief',
        action: 'brief',
        title: 'Read your Daily Brief',
        hint: 'x',
        state: 'skipped',
        resolved: true
      },
      {
        key: 'note',
        action: 'note',
        title: 'Save an idea',
        hint: 'x',
        state: 'open',
        resolved: false
      }
    ]
  };
  greeting.evaluate();
  const words = mount.textContent;
  assert.match(words, /✓/);
  assert.match(words, /—/);
  assert.match(words, / — Completed/);
  assert.match(words, / — Skipped/);
  const rows = mount.queryAll('[data-hq-greeting-choice="home"]');
  assert.equal(rows[0].tagName, 'A', 'a finished folder step still opens Home');
  assert.equal(rows[0].getAttribute('href'), '/');
  assert.equal(mount.queryAll('[data-hq-greeting-choice="folder-other"]').length, 0);
  assert.equal(mount.queryAll('[data-hq-greeting-choice="brief"]').length, 1, 'still a button');
});

test('it waits, drawing nothing, until the page, the conversation and the recipient are known', () => {
  for (const patch of [{ view: null }, { loading: true }, { personal: false }]) {
    const { greeting, mount, state } = setup();
    Object.assign(state, patch);
    greeting.evaluate();
    assert.equal(shown(mount), false, JSON.stringify(Object.keys(patch)));
    assert.equal(mount.hidden, true);
    assert.equal(greeting.gate(), 'wait');
    assert.equal(greeting.holdsNotesQuestion(), true, 'still held: the greeting may show');
    assert.equal(focusLog.length, 0);
  }
});

test('a message, a finished visit or a "nothing to greet" answer takes it away and lets the Notes question go', () => {
  for (const [label, patch, gate] of [
    ['a message', { messages: true }, 'hide'],
    ['hidden or all resolved', { view: { show: false } }, 'off']
  ]) {
    const { greeting, mount, state } = setup();
    greeting.evaluate();
    assert.equal(shown(mount), true, label);
    Object.assign(state, patch);
    greeting.evaluate();
    assert.equal(shown(mount), false, label);
    assert.equal(mount.hidden, true, label);
    assert.equal(greeting.gate(), gate, label);
    assert.equal(greeting.holdsNotesQuestion(), false, label);
    assert.deepEqual(state.dispatched, [true, false], label + ': held, then released');
  }
});

test('a cleared conversation brings the greeting back while the visit is pending', () => {
  const { greeting, mount, state } = setup();
  greeting.evaluate();
  state.messages = true;
  greeting.evaluate();
  assert.equal(shown(mount), false);
  state.messages = false;
  greeting.evaluate();
  assert.equal(shown(mount), true);
  assert.deepEqual(state.dispatched, [true, false, true]);
});

test('a burst of events is answered once, and the Notes question hears only changes', () => {
  const { greeting, state } = setup();
  greeting.schedule();
  greeting.schedule();
  greeting.schedule();
  assert.equal(state.queued.length, 1, 'one evaluation for the burst, before the next paint');
  state.queued.shift()();
  assert.deepEqual(state.dispatched, [true]);
  greeting.schedule();
  assert.equal(state.queued.length, 1, 'and a later burst is answered again');
  state.queued.shift()();
  assert.deepEqual(state.dispatched, [true], 'nothing changed, so nothing is announced');
  state.messages = true;
  greeting.schedule();
  state.queued.shift()();
  assert.deepEqual(state.dispatched, [true, false]);
});

test('a redraw with the same words does nothing; changed words redraw and keep the focused choice', () => {
  const { greeting, mount, state } = setup();
  greeting.evaluate();
  const card = mount.children[0];
  greeting.evaluate();
  assert.equal(mount.children[0], card, 'unchanged: the same card, not a new one');

  // The person is on the Daily Brief choice when the page's answer changes.
  const brief = mount.queryAll('[data-hq-greeting-choice="brief"]')[0];
  brief.focus();
  focusLog = [];
  state.view = { ...state.view, running: ['Notes', 'Memory'], off: ['Email'] };
  greeting.evaluate();
  const again = mount.queryAll('[data-hq-greeting-choice="brief"]')[0];
  assert.notEqual(again, brief, 'redrawn');
  assert.deepEqual(focusLog, [again], 'focus follows the control to its replacement');
  assert.match(mount.textContent, /Notes and Memory are already running\./);
});

test('when it goes away with focus inside it, the person keeps their place in the open drawer', () => {
  const { greeting, mount, state, input } = setup();
  state.open = true;
  greeting.evaluate();
  mount.queryAll('[data-hq-greeting-choice="brief"]')[0].focus();
  focusLog = [];
  state.messages = true;
  greeting.evaluate();
  assert.deepEqual(focusLog, [input]);

  // A closed drawer, or focus elsewhere, is left alone.
  for (const [open, inside] of [
    [false, true],
    [true, false]
  ]) {
    const next = setup();
    next.state.open = open;
    next.greeting.evaluate();
    if (inside) next.mount.queryAll('[data-hq-greeting-choice="brief"]')[0].focus();
    else next.input.focus();
    focusLog = [];
    next.state.messages = true;
    next.greeting.evaluate();
    assert.equal(focusLog.length, 0, `open ${open}, focus inside ${inside}`);
  }
});

test('"Save an idea" closes the drawer first, with no focus restore, then the page runs it', () => {
  const { greeting, state } = setup();
  assert.equal(greeting.choose('note'), true);
  assert.deepEqual(state.closed, [{ restoreFocus: false }]);
  assert.deepEqual(state.ran, [['note', null]]);
});

test('the Daily Brief and Notes open above the drawer and leave it open', () => {
  const { greeting, mount, state, input, launcher } = setup();
  greeting.evaluate();
  assert.equal(greeting.choose('brief'), true);
  assert.equal(state.closed.length, 0, 'the stat modal sits above the drawer');
  const [action, trigger] = state.ran[0];
  assert.equal(action, 'brief');
  // Its trigger returns focus to the brief choice while it is drawn, else the composer.
  trigger.focus({ preventScroll: true });
  assert.equal(doc(mount).activeElement, mount.queryAll('[data-hq-greeting-choice="brief"]')[0]);
  mount.textContent = '';
  trigger.focus();
  assert.equal(doc(mount).activeElement, input);

  assert.equal(greeting.choose('notes'), true);
  assert.equal(state.closed.length, 0);
  assert.deepEqual(state.ran[1], ['notes', launcher], 'never the greeting’s own button');
});

const doc = mount => mount.ownerDoc;

test('a choice the page refuses, or one that is a link, does nothing here', () => {
  const { greeting, state } = setup();
  state.runResult = false;
  assert.equal(greeting.choose('brief'), false);
  assert.equal(greeting.choose('folder'), false, 'the browser follows the link');
  assert.equal(greeting.choose('home'), false);
  assert.equal(greeting.choose('nope'), false);
  assert.deepEqual(
    state.ran.map(([action]) => action),
    ['brief'],
    'only the owners the suggestions already use are ever asked'
  );
});

test('a click on a button choice runs it; a click on a link is left to the browser', () => {
  const { greeting, mount, state } = setup();
  greeting.evaluate();
  const [folder, brief, note] = mount.queryAll('[data-hq-greeting-choice]');
  mount.click(folder);
  assert.deepEqual(state.ran, []);
  assert.deepEqual(state.closed, [{ restoreFocus: false }]);
  mount.click(brief);
  mount.click(note);
  assert.deepEqual(
    state.ran.map(([action]) => action),
    ['brief', 'note']
  );
  assert.deepEqual(state.closed, [{ restoreFocus: false }, { restoreFocus: false }]);
  mount.click({ closest: () => null });
  assert.equal(state.ran.length, 2, 'a click elsewhere in the card does nothing');
});

test('it never opens the drawer: no call to open exists on its surface', () => {
  const { greeting } = setup();
  assert.deepEqual(Object.keys(greeting).sort(), [
    'choose',
    'evaluate',
    'gate',
    'holdsNotesQuestion',
    'schedule'
  ]);
});

test('inert on a done or non-HQ page, with no mount, or with no document: it holds nothing and draws nothing', () => {
  const state = { calls: 0 };
  const inert = [
    createHQGreeting({ mount: new FakeElement(), active: false, view: () => (state.calls += 1) }),
    createHQGreeting({ mount: null, active: true, view: () => (state.calls += 1) }),
    createHQGreeting({
      mount: new FakeElement(),
      active: true,
      doc: null,
      view: () => (state.calls += 1)
    }),
    createHQGreeting()
  ];
  for (const greeting of inert) {
    greeting.evaluate();
    greeting.schedule();
    assert.equal(greeting.gate(), 'off');
    assert.equal(greeting.holdsNotesQuestion(), false);
    assert.equal(greeting.choose('note'), false);
  }
  assert.equal(state.calls, 0, 'the page is never even asked');
});

test('a page that throws, or a getter that does, waits instead of breaking the drawer', () => {
  const { greeting, mount } = setup({
    view: () => {
      throw new Error('page not ready');
    }
  });
  assert.doesNotThrow(() => greeting.evaluate());
  assert.equal(greeting.gate(), 'wait');
  assert.equal(shown(mount), false);
});
