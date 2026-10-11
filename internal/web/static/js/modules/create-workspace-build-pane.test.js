import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./create-workspace-build-pane.js', import.meta.url), 'utf8');

// A small DOM: enough elements, attributes, events, and simple selectors for
// the pane to render into and be driven the way a user drives it.
class FakeEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.detail = init.detail;
    this.key = init.key;
    this.shiftKey = Boolean(init.shiftKey);
    this.defaultPrevented = false;
    this.target = null;
  }
  preventDefault() {
    this.defaultPrevented = true;
  }
}

function camel(name) {
  return name.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
}

class FakeElement {
  constructor(tag, document) {
    this.tagName = String(tag).toUpperCase();
    this.ownerDocument = document;
    this.children = [];
    this.parentNode = null;
    this.attributes = new Map();
    this.dataset = {};
    this.listeners = new Map();
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this.id = '';
    this._text = '';
    this._className = '';
    const self = this;
    this.classList = {
      add: (...names) => names.forEach(name => self._classes().add(name) && self._syncClass()),
      remove: (...names) => {
        const set = self._classes();
        names.forEach(name => set.delete(name));
        self._className = [...set].join(' ');
      },
      toggle: (name, force) => {
        const set = self._classes();
        const on = force === undefined ? !set.has(name) : Boolean(force);
        if (on) set.add(name);
        else set.delete(name);
        self._className = [...set].join(' ');
        return on;
      },
      contains: name => self._classes().has(name)
    };
  }
  _classes() {
    if (!this._classSet) this._classSet = new Set(this._className.split(/\s+/).filter(Boolean));
    return this._classSet;
  }
  _syncClass() {
    this._className = [...this._classes()].join(' ');
    return true;
  }
  get className() {
    return this._className;
  }
  set className(value) {
    this._className = String(value || '');
    this._classSet = null;
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent).join('');
  }
  set textContent(value) {
    this._text = String(value ?? '');
    for (const child of this.children) child.parentNode = null;
    this.children = [];
  }
  set innerHTML(value) {
    this.textContent = '';
    this._html = String(value ?? '');
  }
  get innerHTML() {
    return this._html || '';
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
    if (name.startsWith('data-')) this.dataset[camel(name.slice(5))] = String(value);
  }
  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }
  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  prepend(child) {
    child.parentNode = this;
    this.children.unshift(child);
  }
  contains(node) {
    for (let current = node; current; current = current.parentNode) {
      if (current === this) return true;
    }
    return false;
  }
  matches(selector) {
    const attribute = selector.match(/^\[data-([a-z-]+)\]$/);
    if (attribute) return camel(attribute[1]) in this.dataset;
    if (selector.startsWith('.')) return this.classList.contains(selector.slice(1));
    return this.tagName === selector.toUpperCase();
  }
  closest(selector) {
    for (let current = this; current; current = current.parentNode) {
      if (current.matches?.(selector)) return current;
    }
    return null;
  }
  querySelectorAll(selector) {
    const found = [];
    const walk = node => {
      for (const child of node.children) {
        if (child.matches(selector)) found.push(child);
        walk(child);
      }
    };
    walk(this);
    return found;
  }
  addEventListener(type, handler) {
    const list = this.listeners.get(type) || [];
    list.push(handler);
    this.listeners.set(type, list);
  }
  removeEventListener(type, handler) {
    this.listeners.set(
      type,
      (this.listeners.get(type) || []).filter(candidate => candidate !== handler)
    );
  }
  dispatchEvent(event) {
    if (!event.target) event.target = this;
    for (const handler of [...(this.listeners.get(event.type) || [])]) handler(event);
    // Clicks and keys bubble to the pane root, which owns the delegated handlers.
    if (['click', 'keydown', 'submit'].includes(event.type) && this.parentNode) {
      this.parentNode.dispatchEvent(event);
    }
    return true;
  }
  click() {
    if (this.disabled) return;
    this.dispatchEvent(new FakeEvent('click'));
  }
  focus() {
    this.ownerDocument.activeElement = this;
  }
}

function loadPane() {
  const document = {
    activeElement: null,
    elements: new Map(),
    createElement(tag) {
      return new FakeElement(tag, document);
    },
    getElementById(id) {
      return document.elements.get(id) || null;
    }
  };
  document.body = new FakeElement('body', document);
  const modal = new FakeElement('div', document);
  const root = new FakeElement('aside', document);
  const buildWith = new FakeElement('button', document);
  document.elements.set('addFolderModal', modal);
  document.elements.set('workspaceBuildPane', root);
  document.elements.set('workspaceBuildWithBtn', buildWith);
  const window = {
    AgentAvatar: { markup: input => `<span class="agent-avatar">${input.name[0]}</span>` }
  };
  class CustomEvent extends FakeEvent {}
  vm.runInNewContext(source, { window, document, CustomEvent }, { filename: 'pane.js' });
  const events = [];
  for (const type of [
    'workspace-build-turn',
    'workspace-build-collapse',
    'workspace-build-expand',
    'workspace-build-start-over',
    'workspace-build-action'
  ]) {
    modal.addEventListener(type, event => events.push({ type, detail: event.detail }));
  }
  return { pane: window.WorkspaceBuildPane, document, root, modal, buildWith, events };
}

const assistant = { display_name: 'Luna', appearance: {} };

function entries(root) {
  return root.querySelectorAll('li');
}

test('the pane opens with the fixed opening line, the assistant, and a composer', () => {
  const { pane, root } = loadPane();
  assert.equal(pane.mount({ assistant }), true);
  assert.equal(root.hidden, false);
  const text = root.textContent;
  assert.match(text, /Luna/);
  assert.match(text, /Personal Assistant/);
  assert.match(text, /Set up manually/);
  assert.match(text, /Start over/);
  const [first] = entries(root);
  assert.match(first.textContent, /What should this workspace do\?/);
  const input = root.querySelectorAll('textarea')[0];
  assert.equal(input.id, 'workspaceBuildComposerInput');
  assert.equal(input.maxLength, 2000);
  const status = root.querySelectorAll('p').find(node => node.id === 'workspaceBuildStatus');
  assert.equal(status.getAttribute('role'), 'status');
  assert.equal(status.getAttribute('aria-live'), 'polite');
  // No id from the global assistant panel may appear here (#350).
  const ids = [];
  const walk = node => {
    if (node.id) ids.push(node.id);
    node.children.forEach(walk);
  };
  walk(root);
  assert.ok(
    ids.every(id => !id.startsWith('personalAssistant')),
    ids.join(',')
  );
});

test('a chip sends its server id once, and a sent chip cannot be sent again', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant });
  pane.applySession({
    transcript: [
      { role: 'user', text: 'a newsletter from my notes' },
      {
        role: 'assistant',
        text: 'Should I link your research folder?',
        choices: [
          { id: 'c1', label: 'Choose a folder' },
          { id: 'c2', label: 'No folder' }
        ]
      }
    ]
  });
  const chips = root.querySelectorAll('button').filter(button => button.dataset.buildChoice);
  assert.equal(chips.length, 2);
  assert.equal(chips[0].closest('.workspace-build-chips').getAttribute('role'), 'group');
  chips[1].click();
  chips[1].click();
  const turns = events.filter(event => event.type === 'workspace-build-turn');
  assert.equal(turns.length, 1);
  assert.equal(turns[0].detail.choiceId, 'c2');
  assert.equal(turns[0].detail.label, 'No folder');
  assert.equal(chips[0].disabled, true);
});

test('only the newest unanswered question keeps live chips', () => {
  const { pane, root } = loadPane();
  pane.mount({ assistant });
  pane.applySession({
    transcript: [
      { role: 'assistant', text: 'First?', choices: [{ id: 'a', label: 'A' }], chosen: 'a' },
      { role: 'user', text: 'A' },
      { role: 'assistant', text: 'Second?', choices: [{ id: 'b', label: 'B' }] }
    ]
  });
  const chips = root.querySelectorAll('button').filter(button => button.dataset.buildChoice);
  assert.deepEqual(
    chips.map(chip => [chip.dataset.buildChoice, chip.disabled]),
    [
      ['a', true],
      ['b', false]
    ]
  );
  assert.equal(chips[0].getAttribute('aria-pressed'), 'true');
});

test('Enter sends, Shift+Enter does not, and a busy pane disables the composer', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant });
  const input = root.querySelectorAll('textarea')[0];
  input.value = 'a newsletter';
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter', shiftKey: true }));
  assert.equal(events.filter(event => event.type === 'workspace-build-turn').length, 0);
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  const turns = events.filter(event => event.type === 'workspace-build-turn');
  assert.equal(turns.length, 1);
  assert.equal(turns[0].detail.text, 'a newsletter');
  assert.equal(input.value, '');

  pane.setBusy(true, { pendingText: 'a newsletter' });
  const send = root.querySelectorAll('button').find(button => button.type === 'submit');
  assert.equal(input.disabled, true);
  assert.equal(send.disabled, true);
  assert.match(root.textContent, /Setting up…/);
  assert.match(entries(root).at(-1).textContent, /a newsletter/, 'what was sent shows at once');
  input.value = 'again';
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  assert.equal(events.filter(event => event.type === 'workspace-build-turn').length, 1);
  pane.setBusy(false);
  assert.equal(input.disabled, false);
});

test('focus moves from the dialog to the composer, never out of a form field', () => {
  const { pane, document, root, modal } = loadPane();
  modal.className = 'modal';
  modal.appendChild(root);
  pane.mount({ assistant });
  const input = root.querySelectorAll('textarea')[0];
  modal.focus();
  assert.equal(pane.focusComposer(), true);
  assert.equal(document.activeElement, input, 'the dialog itself gives focus up');

  const nameField = new FakeElement('input', document);
  modal.appendChild(nameField);
  nameField.focus();
  assert.equal(pane.focusComposer(), false);
  pane.setBusy(true);
  pane.setBusy(false);
  assert.equal(
    document.activeElement,
    nameField,
    'a finished turn does not pull focus off the form'
  );

  pane.collapse();
  modal.focus();
  assert.equal(pane.focusComposer(), false, 'a collapsed pane takes no focus');
});

test('the transcript shows the last twelve entries until Show earlier is chosen', () => {
  const { pane, root } = loadPane();
  pane.mount({ assistant });
  const transcript = [];
  for (let index = 0; index < 20; index += 1) {
    transcript.push({ role: index % 2 ? 'assistant' : 'user', text: `message ${index}` });
  }
  pane.applySession({ transcript });
  assert.equal(entries(root).length, pane.VISIBLE_ENTRIES);
  assert.match(entries(root).at(-1).textContent, /message 19/);
  const earlier = root
    .querySelectorAll('button')
    .find(button => button.dataset.buildControl === 'show-earlier');
  assert.equal(earlier.hidden, false);
  earlier.click();
  assert.equal(entries(root).length, 21, 'the opening line plus every stored entry');
});

test('form edits are quiet notes, not bubbles', () => {
  const { pane, root } = loadPane();
  pane.mount({ assistant });
  pane.applySession({ transcript: [{ role: 'form', text: 'You renamed it to Field Notes' }] });
  const note = entries(root).at(-1);
  assert.equal(note.classList.contains('workspace-build-entry--form'), true);
  assert.equal(note.querySelectorAll('p').length, 0);
});

test('collapse and expand report the user’s choice and toggle the Build with button', () => {
  const { pane, root, buildWith, events } = loadPane();
  pane.mount({ assistant });
  const manual = root
    .querySelectorAll('button')
    .find(button => button.dataset.buildControl === 'collapse');
  manual.click();
  assert.deepEqual(
    events.map(event => event.type),
    ['workspace-build-collapse']
  );
  pane.collapse();
  assert.equal(root.hidden, true);
  assert.equal(buildWith.hidden, false);
  assert.equal(buildWith.textContent, 'Build with Luna');
  buildWith.click();
  assert.equal(events.at(-1).type, 'workspace-build-expand');
  pane.expand();
  assert.equal(root.hidden, false);
  assert.equal(buildWith.hidden, true);

  pane.collapse({ withdraw: true });
  assert.equal(buildWith.hidden, true, 'a withdrawn pane offers no way back in');
  const startOver = root
    .querySelectorAll('button')
    .find(button => button.dataset.buildControl === 'start-over');
  startOver.click();
  assert.equal(events.at(-1).type, 'workspace-build-start-over');
});

test('pane-only lines keep their place, carry local actions, and can be removed', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant });
  const id = pane.showLine('Resume building Field Notes?', {
    chips: [
      { id: 'resume', label: 'Resume' },
      { id: 'start_over', label: 'Start over' }
    ]
  });
  const resume = root
    .querySelectorAll('button')
    .find(button => button.dataset.buildChoice === 'resume');
  resume.click();
  assert.equal(events.at(-1).type, 'workspace-build-action');
  assert.equal(events.at(-1).detail.action, 'resume');
  pane.removeLine(id);
  assert.equal(/Resume building/.test(root.textContent), false);
  pane.applySession({ transcript: [{ role: 'user', text: 'hello' }] });
  assert.match(entries(root)[0].textContent, /What should this workspace do\?/);
});

test('destroy empties the pane and hides it', () => {
  const { pane, root, buildWith } = loadPane();
  pane.mount({ assistant });
  pane.collapse();
  pane.destroy();
  assert.equal(root.hidden, true);
  assert.equal(root.children.length, 0);
  assert.equal(buildWith.hidden, true);
  assert.equal(pane.isMounted(), false);
});
