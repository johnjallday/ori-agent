import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./create-workspace-build-pane.js', import.meta.url), 'utf8');
const visualSource = readFileSync(new URL('./folder-setup-visual.js', import.meta.url), 'utf8');
const planLinesSource = readFileSync(new URL('./folder-plan-lines.js', import.meta.url), 'utf8');
const recognitionLinesSource = readFileSync(
  new URL('./folder-recognition-lines.js', import.meta.url),
  'utf8'
);

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
  // Text becomes a text node, as it does in the DOM.
  append(...nodes) {
    for (const node of nodes) {
      if (typeof node === 'string') {
        const textNode = new FakeElement('#text', this.ownerDocument);
        textNode.textContent = node;
        this.appendChild(textNode);
      } else this.appendChild(node);
    }
  }
  replaceChildren(...nodes) {
    this.textContent = '';
    this.append(...nodes);
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

function loadPane(extras = {}) {
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
  // Loaded before the pane, as in base.tmpl.
  const context = vm.createContext({ window, document, CustomEvent, ...extras });
  vm.runInContext(planLinesSource, context, { filename: 'folder-plan-lines.js' });
  vm.runInContext(visualSource, context, { filename: 'folder-setup-visual.js' });
  vm.runInContext(recognitionLinesSource, context, { filename: 'folder-recognition-lines.js' });
  vm.runInContext(source, context, { filename: 'pane.js' });
  const events = [];
  for (const type of [
    'workspace-build-turn',
    'workspace-build-collapse',
    'workspace-build-expand',
    'workspace-build-start-over',
    'workspace-build-action',
    'workspace-build-folder-open',
    'workspace-build-folder',
    'workspace-build-folder-remove',
    'workspace-build-link',
    'workspace-build-link-remove',
    'workspace-build-review',
    'workspace-build-prepare',
    'workspace-build-defer',
    'workspace-build-setup',
    'workspace-build-prepare-group',
    'workspace-build-install-provider',
    'workspace-build-install-provider-confirm',
    'workspace-build-install-provider-cancel'
  ]) {
    modal.addEventListener(type, event => events.push({ type, detail: event.detail }));
  }
  return { pane: window.WorkspaceBuildPane, document, root, modal, buildWith, events };
}

const assistant = { display_name: 'Luna', appearance: {} };

function entries(root) {
  return root.querySelectorAll('li');
}

// The composer, by its id: a plan's brief field is a textarea too.
function composerOf(root) {
  return root.querySelectorAll('textarea').find(node => node.id === 'workspaceBuildComposerInput');
}

test('folder review clarification is text-only, does not touch unsent text, and sends one choice', () => {
  const { pane, document, events, root } = loadPane();
  pane.mount({ assistant: { display_name: 'Atlas' } });
  const host = document.createElement('section');
  const input = composerOf(root);
  input.value = 'Unsent thought';
  const question = {
    question: 'What should this folder do?',
    choices: [{ id: 'choice-1', label: '<img src=x>' }]
  };
  pane.renderFolderQuestion(host, question);
  const choice = host.querySelectorAll('button')[0];
  assert.equal(choice.textContent, '<img src=x>');
  pane.renderFolderQuestion(host, question);
  assert.equal(
    host.querySelectorAll('button')[0],
    choice,
    'unchanged projection keeps the focused control'
  );
  choice.click();
  choice.click();
  assert.equal(events.filter(event => event.type === 'workspace-build-turn').length, 1);
  assert.equal(input.value, 'Unsent thought');
});

test('creation permission review is host-rendered, default-off and explicitly confirmed', () => {
  const { pane, document, events } = loadPane();
  const host = document.createElement('section');
  const review = {
    review_revision: 'opaque',
    workspace: { name: '<edited name>', description: 'Edited description', mission: 'Edited goal' },
    team: [{ name: 'Edited Curator', action: 'create', model: 'local-model' }],
    folder: { name: 'Synthetic Inbox', path: '/temporary/synthetic-inbox' },
    terms: {
      watch_events: ['create', 'rename'],
      debounce_seconds: 300,
      daily_scan_local_time: '09:00',
      timezone: 'Local'
    }
  };
  pane.renderCreationReview(host, { review });
  assert.match(host.textContent, /<edited name>/);
  assert.match(host.textContent, /Edited Curator · create · local-model/);
  assert.match(host.textContent, /This changes the folder on disk/);
  assert.match(host.textContent, /No file contents are opened/);
  assert.match(host.textContent, /separate approval/);
  const checkbox = host.querySelectorAll('input')[0];
  assert.equal(checkbox.checked, false);
  assert.equal(events.length, 0);
  checkbox.checked = true;
  checkbox.dispatchEvent(new FakeEvent('change'));
  assert.equal(events[0].type, 'workspace-build-review');
  assert.equal(events[0].detail.monitoring, true);
  const confirm = host
    .querySelectorAll('button')
    .find(button => button.textContent === 'Create & scan folder');
  confirm.click();
  assert.equal(events[1].type, 'workspace-build-prepare');
  pane.renderCreationReview(host, { review, busy: true });
  assert.ok(host.querySelectorAll('button').every(button => button.disabled));
});

test('compact permissions retain focused monitoring and open details without a duplicate primary action', () => {
  const { pane, document, events } = loadPane();
  const host = document.createElement('section');
  const actionsHost = document.createElement('div');
  const review = {
    workspace: { name: 'Inbox' },
    folder: { name: 'Inbox', path: '/synthetic/inbox' },
    terms: {},
    review_revision: 'current'
  };
  pane.renderCreationReview(host, { review, compact: true, actionsHost });
  const checkbox = host.querySelectorAll('input')[0];
  checkbox.focus();
  host.querySelectorAll('details')[0].open = true;
  pane.renderCreationReview(host, { review, compact: true, actionsHost });
  assert.equal(host.querySelectorAll('input')[0], checkbox, 'a repaint retains controls');
  pane.renderCreationReview(host, {
    review: null,
    compact: true,
    actionsHost,
    monitoring: true,
    message: 'Checking current terms'
  });
  assert.equal(document.activeElement, host.querySelectorAll('input')[0]);
  assert.equal(host.querySelectorAll('input')[0].checked, true);
  assert.equal(host.querySelectorAll('button').length, 0);
  assert.equal(actionsHost.querySelectorAll('button').length, 0);
  pane.renderCreationReview(host, { review, compact: true, actionsHost });
  assert.equal(
    host.querySelectorAll('details')[0].open,
    true,
    'details stay open through a preview refresh'
  );
  assert.equal(document.activeElement, host.querySelectorAll('input')[0]);
  host.querySelectorAll('summary')[0].focus();
  pane.renderCreationReview(host, { review, compact: true, actionsHost, message: 'Updated terms' });
  assert.equal(document.activeElement, host.querySelectorAll('summary')[0]);
  const otherControl = document.createElement('button');
  otherControl.focus();
  pane.renderCreationReview(host, { review, compact: true, actionsHost, message: 'Current terms' });
  assert.equal(
    document.activeElement,
    otherControl,
    'no focus change outside the refreshed review'
  );
  assert.equal(events.length, 0, 'rendering cannot request or confirm anything');
});

test('a refreshed recovery retains focus on its existing workspace link', () => {
  const { pane, document } = loadPane();
  const host = document.createElement('section');
  const actionsHost = document.createElement('div');
  const review = { state: 'accepted', review_revision: 'accepted', folder_slug: 'kept-workspace' };
  pane.renderCreationReview(host, { review, compact: true, accepted: true, actionsHost });
  actionsHost.querySelectorAll('a')[0].focus();
  pane.renderCreationReview(host, {
    review,
    compact: true,
    accepted: true,
    actionsHost,
    busy: true
  });
  assert.equal(document.activeElement, actionsHost.querySelectorAll('a')[0]);
});

test('accepted preparation can be explicitly deferred without offering ordinary recreation', () => {
  const { pane, document, events } = loadPane();
  const host = document.createElement('section');
  const review = {
    state: 'accepted',
    review_revision: 'accepted-review',
    workspace_id: 'reserved-workspace'
  };
  pane.renderCreationReview(host, { review, accepted: true });
  assert.equal(host.querySelectorAll('a').length, 0, 'a reserved ID is not a page route');
  pane.renderCreationReview(host, {
    review: { ...review, folder_slug: 'canonical-folder' },
    accepted: true
  });
  assert.equal(
    host.querySelectorAll('a')[0].href,
    '/workspaces/canonical-folder?panel=file-janitor'
  );
  assert.match(
    host.textContent,
    /Any existing workspace, saved access, Filed\/ and results are kept/
  );
  host
    .querySelectorAll('button')
    .find(button => button.textContent === 'Defer preparation')
    .click();
  assert.equal(events[0].type, 'workspace-build-defer');
  pane.renderCreationReview(host, { review: { ...review, state: 'deferred' }, accepted: true });
  assert.match(host.textContent, /Preparation deferred/);
  assert.equal(host.querySelectorAll('button').length, 0);
});

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
  const input = composerOf(root);
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
  const input = composerOf(root);
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
  const input = composerOf(root);
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

// ----- Add folder -----

// Objects born inside the pane's vm realm have another Object prototype, so
// they are compared by value, not by deepEqual's prototype check.
function same(actual, expected) {
  assert.equal(JSON.stringify(actual), JSON.stringify(expected));
}

function folderControls(root) {
  const buttons = root.querySelectorAll('button');
  const byControl = kind => buttons.find(button => button.dataset.buildControl === kind);
  return {
    add: byControl('add-folder'),
    remove: byControl('remove-folder'),
    cancel: byControl('folder-cancel'),
    usePath: byControl('folder-path'),
    chooser: root
      .querySelectorAll('section')
      .find(node => node.id === 'workspaceBuildFolderChooser'),
    chip: root.querySelectorAll('span').find(node => node.id === 'workspaceBuildFolder'),
    name: root.querySelectorAll('span').find(node => node.id === 'workspaceBuildFolderName'),
    summary: root.querySelectorAll('p').find(node => node.id === 'workspaceBuildFolderSummary'),
    path: root.querySelectorAll('input').find(node => node.id === 'workspaceBuildFolderPath'),
    pathStatus: root
      .querySelectorAll('p')
      .find(node => node.id === 'workspaceBuildFolderPathStatus'),
    hint: root
      .querySelectorAll('span')
      .find(node => node.classList.contains('workspace-build-folder__hint')),
    choices: () => root.querySelectorAll('button').filter(button => button.dataset.buildFolderMode)
  };
}

const observation = {
  id: 'obs-1',
  folder: 'Elite School',
  files: 42,
  coverage: { partial: true }
};

test('the chooser view restates the composer’s rule: chips, the picker, and the note', () => {
  const { pane } = loadPane();
  const view = pane.folderChooserView({
    chips: [
      { id: 'documents', label: 'Documents' },
      { id: ' ', label: 'blank' },
      { id: 'desktop' }
    ],
    picker_available: true,
    picker_note: 'desktop_off'
  });
  same(view.choices, [
    { label: 'Documents', mode: 'chip', chip: 'documents' },
    { label: 'desktop', mode: 'chip', chip: 'desktop' },
    { label: 'Pick another folder…', mode: 'picker' }
  ]);
  assert.equal(view.note, '');
  assert.equal(
    pane.folderChooserView({ chips: [{ id: 'documents', label: 'Documents' }] }).note,
    'Pick a folder from the list for now.'
  );
  assert.equal(pane.folderChooserView({}).note, 'No folder can be chosen here.');
  assert.equal(
    pane.folderChooserView({ picker_note: 'The dialog is off here.' }).note,
    'The dialog is off here.'
  );
});

test('Add folder is offered only when the server wires it, and the sheet sends one choice', () => {
  const first = loadPane();
  first.pane.mount({ assistant });
  assert.equal(folderControls(first.root).add.hidden, true);

  const { pane, root, events } = loadPane();
  pane.mount({ assistant, folders: true });
  const controls = folderControls(root);
  assert.equal(controls.add.hidden, false);
  assert.equal(controls.chooser.hidden, true);
  controls.add.click();
  assert.equal(events.at(-1).type, 'workspace-build-folder-open');

  // While choices load, the sheet is open with Cancel holding focus.
  assert.equal(pane.openFolderChooser(), true);
  assert.equal(controls.chooser.hidden, false);
  assert.equal(pane.folderChooserOpen(), true);
  assert.match(controls.chooser.textContent, /Loading folder choices/);
  assert.match(controls.chooser.textContent, /File contents are not read/);
  assert.equal(controls.add.getAttribute('aria-expanded'), 'true');

  pane.openFolderChooser({
    chips: [{ id: 'documents', label: 'Documents' }],
    picker_available: true
  });
  const choices = controls.choices();
  same(
    choices.map(button => button.textContent),
    ['Documents', 'Pick another folder…']
  );
  choices[0].click();
  same(events.at(-1), {
    type: 'workspace-build-folder',
    detail: { mode: 'chip', chip: 'documents' }
  });
  choices[1].click();
  same(events.at(-1), { type: 'workspace-build-folder', detail: { mode: 'picker' } });

  controls.cancel.click();
  assert.equal(controls.chooser.hidden, true);
  assert.equal(pane.folderChooserOpen(), false);
  assert.equal(controls.add.getAttribute('aria-expanded'), 'false');
});

test('a typed path is reported as a path for the controller to check, never as a turn', () => {
  const { pane, root, events, document } = loadPane();
  pane.mount({ assistant, folders: true });
  const controls = folderControls(root);
  pane.openFolderChooser({ chips: [], picker_available: false });
  assert.match(controls.chooser.textContent, /No folder can be chosen here/);

  controls.usePath.click();
  assert.equal(controls.pathStatus.textContent, 'Type or paste a folder path first.');
  assert.equal(controls.pathStatus.classList.contains('is-error'), true);
  assert.equal(document.activeElement, controls.path);
  assert.equal(
    events.some(event => event.type === 'workspace-build-folder'),
    false
  );

  const before = events.length;
  controls.path.value = '~/Projects/app';
  controls.path.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  same(events.at(-1), {
    type: 'workspace-build-folder',
    detail: { mode: 'path', path: '~/Projects/app' }
  });
  assert.equal(
    events.slice(before).some(event => event.type === 'workspace-build-turn'),
    false
  );

  pane.folderChooserStatus('That folder could not be used.', true);
  assert.equal(controls.pathStatus.hidden, false);
  assert.equal(controls.pathStatus.classList.contains('is-error'), true);

  // While the controller checks a path, the field is locked and a second
  // Enter sends nothing; the sheet stays open for the answer.
  const sent = events.length;
  pane.setFolderPathBusy(true);
  assert.equal(controls.path.disabled, true);
  assert.equal(controls.usePath.disabled, true);
  assert.equal(controls.usePath.textContent, 'Checking…');
  controls.path.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  assert.equal(events.length, sent);
  assert.equal(controls.chooser.hidden, false);
  pane.setFolderPathBusy(false);
  assert.equal(controls.usePath.disabled, false);
  assert.equal(controls.usePath.textContent, 'Use this path');
  pane.closeFolderChooser();
  assert.equal(controls.chooser.hidden, true);
  assert.equal(document.activeElement, controls.add);
});

test('the attached folder shows metadata without inventing an empty Send turn', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant, folders: true });
  const controls = folderControls(root);
  const input = composerOf(root);
  assert.equal(controls.chip.hidden, true);
  assert.equal(controls.summary.hidden, true);

  // Without a folder an empty Send is nothing.
  input.value = '';
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  assert.equal(
    events.some(event => event.type === 'workspace-build-turn'),
    false
  );

  pane.applySession({ transcript: [], folder: { observation } });
  assert.equal(controls.chip.hidden, false);
  assert.equal(controls.name.textContent, 'Elite School');
  assert.equal(controls.remove.getAttribute('aria-label'), 'Remove folder Elite School');
  assert.equal(controls.summary.hidden, false);
  assert.equal(
    controls.summary.textContent,
    '42 files observed · partial look · contents not read'
  );
  assert.equal(controls.hint, undefined);

  input.value = 'a thing';
  input.dispatchEvent(new FakeEvent('input'));
  assert.equal(controls.hint, undefined);
  input.value = '';
  input.dispatchEvent(new FakeEvent('input'));
  assert.equal(controls.hint, undefined);

  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  assert.equal(
    events.some(event => event.type === 'workspace-build-turn'),
    false
  );

  controls.remove.click();
  assert.equal(events.at(-1).type, 'workspace-build-folder-remove');

  // A pick in flight locks the attachments and says what is happening; Send
  // waits for it too, since a turn sent now would be dropped.
  const send = root.querySelectorAll('button').find(button => button.type === 'submit');
  assert.equal(send.disabled, false);
  pane.setFolderBusy(true, 'Looking at folder metadata locally…');
  assert.equal(controls.add.disabled, true);
  assert.equal(controls.remove.disabled, true);
  assert.equal(send.disabled, true);
  assert.equal(controls.summary.textContent, 'Looking at folder metadata locally…');
  controls.remove.click();
  assert.equal(events.at(-1).type, 'workspace-build-folder-remove'); // the earlier one; nothing new
  const count = events.length;
  controls.add.click();
  assert.equal(events.length, count);
  pane.setFolderBusy(false, 'No folder was chosen. Your draft is unchanged.');
  assert.equal(controls.add.disabled, false);
  assert.equal(send.disabled, false);
  assert.equal(controls.summary.textContent, 'No folder was chosen. Your draft is unchanged.');

  // A new folder starts clean; no folder clears the chip.
  pane.applySession({
    transcript: [],
    folder: { observation: { ...observation, id: 'obs-2', files: 1, coverage: {} } }
  });
  assert.equal(controls.summary.textContent, '1 file observed · bounded look · contents not read');
  pane.applySession({ transcript: [] });
  assert.equal(controls.chip.hidden, true);
  assert.equal(controls.summary.hidden, true);
  assert.equal(controls.hint, undefined);
});

test('the recognition line says what Ori recognized, from server facts only', () => {
  const { pane } = loadPane();
  const reaper = {
    verdict: 'project',
    label: 'REAPER project',
    marker_noun: 'session',
    marker_count: 3,
    project_count: 1
  };
  assert.equal(pane.folderRecognitionText(reaper), 'Recognized: REAPER project · 3 session files');
  assert.equal(
    pane.folderRecognitionText({ ...reaper, marker_count: 1 }),
    'Recognized: REAPER project · 1 session file'
  );
  // A marker that is a folder (a repository) has no count to give.
  assert.equal(
    pane.folderRecognitionText({ verdict: 'project', label: 'software project', project_count: 1 }),
    'Recognized: software project'
  );
  assert.equal(
    pane.folderRecognitionText({ verdict: 'none' }),
    'Folder metadata attached. Choose the purpose that fits.'
  );
  assert.equal(
    pane.folderRecognitionText({ verdict: 'collection', project_count: 7 }),
    'Holds 7 projects · Explore a folder on Home sets them up together'
  );
  // Nothing said, or something the pane cannot vouch for, says nothing.
  for (const silent of [
    null,
    undefined,
    {},
    { verdict: 'project' },
    { verdict: 'collection', project_count: 1 },
    { verdict: 'maybe', label: 'x' }
  ]) {
    assert.equal(pane.folderRecognitionText(silent), '');
  }
});

test('an attached folder shows its recognition line under the chip', () => {
  const { pane, root } = loadPane();
  pane.mount({ assistant, folders: true });
  const line = () =>
    root.querySelectorAll('p').find(node => node.id === 'workspaceBuildFolderRecognition');
  assert.equal(line().hidden, true);

  pane.applySession({
    transcript: [],
    folder: {
      observation: { ...observation, folder: 'Ori Music Demo' },
      recognition: {
        verdict: 'project',
        label: 'REAPER project',
        marker_noun: 'session',
        marker_count: 3
      }
    }
  });
  assert.equal(line().hidden, false);
  assert.equal(line().textContent, 'Recognized: REAPER project · 3 session files');
  assert.equal(line().classList.contains('is-recognized'), true);

  pane.applySession({ transcript: [], folder: { observation, recognition: { verdict: 'none' } } });
  assert.equal(line().textContent, 'Folder metadata attached. Choose the purpose that fits.');
  assert.equal(line().classList.contains('is-recognized'), false);

  // An older build kept no recognition: the folder shows without a line.
  pane.applySession({ transcript: [], folder: { observation } });
  assert.equal(line().hidden, true);
  pane.applySession({ transcript: [] });
  assert.equal(line().hidden, true);
});

test('a recognized folder’s plan is the host’s lines, with the reason behind it', () => {
  const { pane, root } = loadPane();
  pane.mount({ assistant, folders: true });
  const find = (tag, id) => root.querySelectorAll(tag).find(node => node.id === id);
  const block = () => find('section', 'workspaceBuildPlan');
  assert.equal(block().hidden, true);

  const plan = {
    ready: true,
    digest: 'digest-1',
    evidence: 'Has a REAPER session, edited yesterday',
    lines: [
      { kind: 'provider', name: 'Installs the Music Project Management plugin' },
      {
        kind: 'home',
        name: 'Creates Home “Music Production Home”',
        detail: 'Your later projects of this kind join it.'
      },
      { kind: 'workspace', name: '  ' }
    ]
  };
  pane.applySession({ transcript: [], folder: { observation }, plan });
  assert.equal(block().hidden, false);
  const items = find('ul', 'workspaceBuildPlanLines').children;
  // The card's own normalizer drops a line with no name.
  assert.equal(items.length, 2);
  assert.equal(items[1].dataset.kind, 'home');
  assert.equal(
    items[1].textContent,
    '•Creates Home “Music Production Home”Your later projects of this kind join it.'
  );
  // The checklist leads each line with its kind's icon, hidden from assistive tech.
  assert.deepEqual(
    items.map(item => item.children[0].className),
    ['pa-folder__plan-kind', 'pa-folder__plan-kind']
  );
  assert.deepEqual(
    items.map(item => item.children[0].children[0].className),
    ['bi bi-box-seam', 'bi bi-house-door']
  );
  assert.equal(items[1].children[0].getAttribute('aria-hidden'), 'true');
  const why = root
    .querySelectorAll('details')
    .find(node => node.classList.contains('workspace-build-plan__why'));
  assert.equal(why.hidden, false);
  assert.equal(why.textContent, 'Why this suggestion?Has a REAPER session, edited yesterday');

  // A plan that cannot run says so, names what it waits on, and lists nothing.
  pane.applySession({
    transcript: [],
    folder: { observation },
    plan: {
      ready: false,
      lines: [],
      unavailable: 'provider_unverified',
      unavailable_subject: 'Music Project Management'
    }
  });
  assert.equal(block().hidden, false);
  assert.equal(find('ul', 'workspaceBuildPlanLines').hidden, true);
  assert.equal(why.hidden, true);
  assert.equal(
    root.querySelectorAll('p').find(node => node.classList.contains('workspace-build-plan__note'))
      .textContent,
    'Set it up isn’t available: the installed Music Project Management isn’t the reviewed release. The form still works.'
  );

  // Each reason says what it waits on; one the pane does not know says only
  // that Set it up is unavailable.
  assert.equal(
    pane.COPY.planUnavailable('provider_not_installed', 'Music Project Management'),
    'Set it up needs the Music Project Management plugin installed first: Ori makes the Home from it. The form still works.'
  );
  assert.equal(
    pane.COPY.planUnavailable('integration_not_installed', 'REAPER'),
    'Set it up isn’t available: REAPER has no reviewed install here. The form still works.'
  );
  assert.equal(
    pane.COPY.planUnavailable('plan_unavailable', 'REAPER'),
    'Set it up isn’t available for this folder right now. The form still works.'
  );

  pane.applySession({ transcript: [], folder: { observation } });
  assert.equal(block().hidden, true);
});

// The sketch above the lines: the Home and the workspace Set it up leaves
// behind, from the plan's facts and the kinds of its lines, never the model.
test('legacy build plans keep full lines and parent facts without guessing a visual destination', () => {
  const { pane, root } = loadPane();
  pane.mount({ assistant, folders: true });
  const sketch = () =>
    root
      .querySelectorAll('div')
      .find(node => node.classList.contains('workspace-build-plan__sketch'));
  const byClass = (tag, name) =>
    root.querySelectorAll(tag).filter(node => node.classList.contains(name));
  const lines = [
    { kind: 'integration', name: 'REAPER integration is installed' },
    { kind: 'home', name: 'Creates Home “Music Production Home”' },
    { kind: 'workspace', name: 'Creates a REAPER song project workspace named Album-2' },
    { kind: 'folder', name: 'Links Album-2 where it is', detail: 'Nothing is moved or copied.' },
    { kind: 'mode', name: 'Uses File-only mode' },
    { kind: 'agents', name: 'Adds the agents this blueprint requires' },
    { kind: 'task', name: 'Queues a first read-only task for you to start' }
  ];
  const song = {
    ...observation,
    projects: [{ id: 'song-1', name: 'Album-2' }]
  };
  const folder = { observation: song, recognition: { verdict: 'project', candidate_id: 'song-1' } };

  // Nothing to sketch without a ready plan.
  assert.equal(sketch().hidden, true);
  pane.applySession({
    transcript: [],
    folder,
    plan: { ready: false, lines: [], unavailable: 'plan_unavailable' }
  });
  assert.equal(sketch().hidden, true);

  // A new Home holds the workspace, which is named after the recognized
  // project while the draft has no name; its parts come from the lines. The
  // integration the plan reports as ready is checked off before Set it up.
  pane.applySession({
    transcript: [],
    draft: {},
    folder,
    plan: {
      ready: true,
      digest: 'd',
      home: { name: 'Music Production Home', status: 'new' },
      needs: [{ kind: 'integration', name: 'REAPER integration', state: 'ready' }],
      lines
    }
  });
  assert.equal(sketch().hidden, false);
  assert.notEqual(sketch().getAttribute('aria-hidden'), 'true');
  assert.equal(byClass('p', 'workspace-build-plan__kicker')[0].textContent, 'You’ll get');
  assert.equal(
    byClass('p', 'workspace-build-plan__kicker')[1].textContent,
    'Ori will · 7 steps · 1 already done'
  );
  assert.equal(byClass('p', 'workspace-build-plan__kicker')[1].hidden, false);
  const items = root.querySelectorAll('li').filter(node => node.dataset.kind);
  assert.deepEqual(
    items.map(item => [item.dataset.kind, item.dataset.state]),
    [
      ['integration', 'already'],
      ['home', undefined],
      ['workspace', undefined],
      ['folder', undefined],
      ['mode', undefined],
      ['agents', undefined],
      ['task', undefined]
    ]
  );
  assert.equal(items[0].textContent, '✓REAPER integration is installed — Already in place');
  assert.match(sketch().textContent, /Proposed new Home · Music Production Home/);
  assert.match(sketch().textContent, /No destination proposed/);
  assert.doesNotMatch(sketch().textContent, /Confirmed member/);

  // The draft's own name wins; an existing Home says so; without a Home the
  // workspace stands alone, and parts the lines do not promise are not drawn.
  pane.applySession({
    transcript: [],
    draft: { name: 'Ori Music Demo' },
    folder,
    plan: {
      ready: true,
      digest: 'd',
      home: { name: 'Music Production Home', status: 'existing' },
      lines: lines.filter(line => line.kind !== 'task')
    }
  });
  assert.match(sketch().textContent, /Existing Home · Music Production Home/);
  assert.doesNotMatch(sketch().textContent, /Ori Music Demo/);
  pane.applySession({
    transcript: [],
    draft: {},
    folder: { observation: { ...observation, projects: [] } },
    plan: { ready: true, digest: 'd', lines: lines.filter(line => line.kind === 'workspace') }
  });
  assert.equal(byClass('div', 'workspace-build-sketch__home').length, 0);
  assert.match(sketch().textContent, /No destination proposed/);
  assert.equal(byClass('p', 'workspace-build-plan__kicker')[1].textContent, 'Ori will · 1 step');

  // The sketch stays while Set it up runs: the lines carry the progress.
  pane.applySession({
    transcript: [],
    status: 'preparing',
    draft: { name: 'Ori Music Demo' },
    folder,
    setup: { plan_digest: 'd' },
    setup_run: { status: 'running', lines: lines.map(line => ({ ...line, state: 'waiting' })) },
    plan: {
      ready: true,
      digest: 'd',
      home: { name: 'Music Production Home', status: 'new' },
      lines
    }
  });
  assert.equal(sketch().hidden, false);

  // An unnamed Home is not turned into a named destination by old sketch logic.
  pane.applySession({
    transcript: [],
    plan: { ready: true, digest: 'd', home: { name: ' ' }, lines }
  });
  assert.doesNotMatch(sketch().textContent, /Existing Home|Proposed new Home/);
  pane.destroy();
  assert.equal(sketch(), undefined);
});

test('build and review adapters share recorded visual facts and saved run milestones', () => {
  const { pane, root, document } = loadPane();
  pane.mount({ assistant, folders: true });
  const folder = { observation, recognition: { verdict: 'project' } };
  const plan = {
    ready: true,
    digest: 'd',
    visual: {
      entity: { kind: 'workspace', name: 'Reviewed name', operation: 'create' },
      placement: 'standalone'
    },
    lines: [
      { kind: 'workspace', name: 'Creates a workspace' },
      { kind: 'folder', name: 'Links without moving' },
      { kind: 'task', name: 'First look is separate' }
    ]
  };
  pane.applySession({ transcript: [], folder, draft: { name: 'Unsaved client name' }, plan });
  const diagram = root.querySelectorAll('figure')[0];
  assert.match(diagram.textContent, /Reviewed name/);
  assert.doesNotMatch(diagram.textContent, /Unsaved client name|Confirmed member/);
  const review = document.createElement('div');
  pane.renderVisualPlan(review);
  assert.equal(review.textContent, diagram.textContent);
  pane.applySession({
    transcript: [],
    folder,
    status: 'preparing',
    setup: { plan_digest: 'd' },
    setup_run: {
      visual: plan.visual,
      status: 'running',
      lines: plan.lines.map(line => ({ ...line, state: 'working' }))
    }
  });
  assert.match(root.querySelectorAll('figure')[0].textContent, /Reviewed name/);
  const milestones = root
    .querySelectorAll('ul')
    .find(node => node.classList.contains('folder-setup-milestones'));
  assert.match(milestones.textContent, /Workspace · In progress/);
  assert.doesNotMatch(milestones.textContent, /First look/);
});

test('a plan that names a hire shows it above the sketch and Hire and start sends the brief', () => {
  const { pane, root, events, document } = loadPane();
  pane.mount({ assistant, folders: true });
  const byId = (tag, id) => root.querySelectorAll(tag).find(node => node.id === id);
  const control = kind =>
    root.querySelectorAll('button').find(button => button.dataset.buildControl === kind);
  const plan = {
    ready: true,
    digest: 'd',
    lines: [{ kind: 'workspace', name: 'Creates a REAPER song workspace named Ori Music Demo' }],
    hire: { mode: 'create', agent_name: 'Song Assistant', role_label: 'Song', model: 'gpt-5' },
    brief_default: 'Keep the session tidy.'
  };
  const session = {
    id: 'build-1',
    status: 'open',
    transcript: [],
    folder: { observation: { ...observation, folder: 'Ori Music Demo' } },
    folder_expires_at: new Date(Date.now() + 20 * 60000).toISOString(),
    plan,
    draft: {}
  };
  pane.applySession(session);
  const title = byId('h3', 'workspaceBuildPlanTitle');
  const brief = byId('textarea', 'workspaceBuildBrief');
  const hire = brief.parentNode.parentNode;
  assert.equal(title.textContent, 'Hire Song Assistant for Ori Music Demo');
  assert.equal(title.querySelectorAll('strong').length, 2);
  assert.equal(hire.hidden, false);
  assert.match(hire.textContent, /SA/);
  assert.match(hire.textContent, /Song role · uses gpt-5/);
  assert.match(hire.textContent, /What should it do with it\?/);
  assert.match(hire.textContent, /This becomes the workspace goal/);
  // The question comes before the sketch.
  const body = hire.parentNode;
  assert.ok(
    body.children.indexOf(hire) <
      body.children.findIndex(node => node.classList.contains('workspace-build-plan__sketch'))
  );
  assert.equal(brief.value, 'Keep the session tidy.');
  assert.equal(control('setup').textContent, 'Hire & start');
  // The draft's goal stands in for the plan's own until the user types.
  pane.applySession({ ...session, draft: { mission: 'Finish three demos.' } });
  assert.equal(brief.value, 'Finish three demos.');
  brief.value = 'Mix the chorus first.';
  brief.dispatchEvent(new FakeEvent('input'));
  pane.applySession({ ...session, draft: { mission: 'Something else.' } });
  assert.equal(brief.value, 'Mix the chorus first.');
  assert.equal(pane.setupBrief(), 'Mix the chorus first.');
  control('setup').click();
  same(events.at(-1), {
    type: 'workspace-build-setup',
    detail: { brief: 'Mix the chorus first.' }
  });

  // The folder review draws the same hire, and both fields edit one brief.
  const host = document.createElement('div');
  const view = pane.renderReviewHire(host);
  assert.equal(
    view.headline.map(part => part.text).join(''),
    'Hire Song Assistant for Ori Music Demo'
  );
  assert.equal(host.hidden, false);
  const reviewBrief = host
    .querySelectorAll('textarea')
    .find(node => node.id === 'workspaceBuildPlanReviewBrief');
  assert.equal(reviewBrief.value, 'Mix the chorus first.');
  reviewBrief.value = 'Write the bridge.';
  reviewBrief.dispatchEvent(new FakeEvent('input'));
  assert.equal(brief.value, 'Write the bridge.');
  assert.equal(pane.setupBrief(), 'Write the bridge.');
  pane.renderReviewHire(host);
  assert.equal(host.querySelectorAll('textarea').length, 1, 'a repaint keeps one field');

  // Once the setup runs, the plan's title is back and nothing is asked.
  pane.applySession({ ...session, status: 'preparing', setup: { plan_digest: 'd' } });
  assert.equal(hire.hidden, true);
  assert.equal(title.textContent, 'The setup plan');
  assert.equal(pane.setupBrief(), null);
  assert.equal(pane.renderReviewHire(host), null);
  assert.equal(host.hidden, true);
  // A plan without a hire keeps Set it up and sends no brief.
  pane.applySession({ ...session, id: 'build-2', plan: { ...plan, hire: undefined } });
  assert.equal(hire.hidden, true);
  assert.equal(control('setup').textContent, 'Set it up');
  control('setup').click();
  same(events.at(-1), { type: 'workspace-build-setup', detail: {} });
});

test('Set it up is offered on a ready, named plan and follows the run it starts', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant, folders: true });
  const buttons = () => root.querySelectorAll('button');
  const control = kind => buttons().find(button => button.dataset.buildControl === kind);
  const status = () =>
    root
      .querySelectorAll('p')
      .find(node => node.classList.contains('workspace-build-plan__status'));
  const expiry = () =>
    root
      .querySelectorAll('p')
      .find(node => node.classList.contains('workspace-build-plan__expiry'));
  const later = new Date(Date.now() + 20 * 60000).toISOString();
  const plan = {
    ready: true,
    digest: 'd',
    lines: [{ kind: 'home', name: 'Creates Home “Music Production Home”' }]
  };
  const session = {
    status: 'open',
    transcript: [],
    folder: { observation },
    folder_expires_at: later,
    plan,
    draft: {}
  };

  // Unnamed, it is named after its folder as the plan says: still pressable.
  pane.applySession(session);
  assert.equal(control('setup').hidden, false);
  assert.equal(control('setup').disabled, false);
  pane.applySession({ ...session, draft: { name: 'Ori Music Demo' } });
  assert.equal(control('setup').disabled, false);
  // Set it up is the plan's action; an empty Send is not offered as one.
  assert.equal(
    root
      .querySelectorAll('span')
      .find(node => node.classList.contains('workspace-build-folder__hint')),
    undefined
  );
  assert.equal(expiry().hidden, true);
  control('setup').click();
  same(events.at(-1), { type: 'workspace-build-setup', detail: {} });

  // While it runs: the run's own lines, a status, and no action at all.
  pane.applySession({
    ...session,
    draft: { name: 'Ori Music Demo' },
    status: 'preparing',
    setup: { plan_digest: 'd' },
    setup_run: {
      status: 'running',
      lines: [{ kind: 'home', name: 'Creates Home “Music Production Home”', state: 'working' }]
    }
  });
  assert.equal(control('setup').hidden, true);
  assert.equal(control('setup-retry').hidden, true);
  // The build takes no turn while it runs.
  assert.equal(composerOf(root).disabled, true);
  assert.equal(status().hidden, false);
  assert.equal(status().textContent, 'Setting it up… Ori is running the plan, line by line.');
  const item = root.querySelectorAll('li').find(node => node.dataset.kind === 'home');
  assert.equal(item.dataset.state, 'working');

  // Stopped: Retry continues it; a project-file question offers the files.
  const stopped = {
    ...session,
    draft: { name: 'Ori Music Demo' },
    setup: { plan_digest: 'd', stop_reason: 'install_failed' },
    setup_run: { status: 'stopped', stop_reason: 'install_failed', lines: plan.lines }
  };
  pane.applySession(stopped);
  assert.equal(status().hidden, true);
  assert.equal(control('setup').hidden, true);
  assert.equal(control('setup-retry').hidden, false);
  control('setup-retry').click();
  same(events.at(-1), { type: 'workspace-build-setup', detail: {} });
  pane.applySession({
    ...stopped,
    setup_run: {
      status: 'stopped',
      stop_reason: 'needs_choice',
      entry_candidates: ['Take One.rpp', 'Take Two.rpp'],
      lines: plan.lines
    }
  });
  assert.equal(control('setup-retry').hidden, true);
  const entries = buttons().filter(button => button.dataset.buildControl === 'setup-entry');
  assert.deepEqual(
    entries.map(button => button.textContent),
    ['Use Take One.rpp', 'Use Take Two.rpp']
  );
  entries[1].click();
  same(events.at(-1), { type: 'workspace-build-setup', detail: { entry_name: 'Take Two.rpp' } });

  // A pick about to expire says so; an expired one asks for the folder again.
  pane.applySession({
    ...session,
    draft: { name: 'Ori Music Demo' },
    folder_expires_at: new Date(Date.now() + 3.5 * 60000).toISOString()
  });
  assert.equal(expiry().hidden, false);
  assert.equal(expiry().textContent, 'Folder pick expires in 4 min');
  assert.equal(control('folder-again').hidden, true);
  pane.applySession({
    ...session,
    draft: { name: 'Ori Music Demo' },
    folder_expires_at: new Date(Date.now() - 60000).toISOString()
  });
  assert.equal(expiry().textContent, 'The folder pick expired. Add the folder again to set it up.');
  assert.equal(control('setup').disabled, true);
  assert.equal(control('folder-again').hidden, false);
  control('folder-again').click();
  same(events.at(-1), { type: 'workspace-build-folder-open', detail: {} });
});

// PRD user story 2: a plan waiting on its Home's plugin offers the install,
// discloses the reviewed release first, and installs only on Install and
// continue.
test('a plan waiting on its Home’s plugin offers the reviewed install, disclosed before anything is installed', () => {
  const drawn = [];
  const { pane, root, events, document } = loadPane({
    PluginLifecycle: {
      renderTrustReport: report => {
        drawn.push(report);
        const node = new FakeElement('div', null);
        node.textContent = 'Adds these Homes: Music Production Home';
        return node;
      }
    }
  });
  pane.mount({ assistant, folders: true });
  const control = kind =>
    root.querySelectorAll('button').find(button => button.dataset.buildControl === kind);
  const byClass = name => root.querySelectorAll('p').find(node => node.classList.contains(name));
  const waiting = {
    status: 'open',
    transcript: [],
    draft: {},
    folder: { observation },
    plan: {
      ready: false,
      lines: [],
      unavailable: 'provider_not_installed',
      unavailable_subject: 'Music Project Management'
    }
  };

  // Without the server's offer there is nothing to press.
  const offer = () =>
    root
      .querySelectorAll('div')
      .find(node => node.classList.contains('workspace-build-plan__install'));
  pane.applySession(waiting);
  assert.equal(offer().hidden, true);

  pane.applySession({ ...waiting, install_provider: { name: 'Music Project Management' } });
  assert.equal(control('install-provider').textContent, 'Install Music Project Management');
  control('install-provider').click();
  same(events.at(-1), { type: 'workspace-build-install-provider', detail: {} });

  pane.setProviderInstall({ phase: 'previewing' });
  assert.equal(control('install-provider').disabled, true);
  assert.equal(
    byClass('workspace-build-plan__install-status').textContent,
    'Checking the reviewed release… Nothing is installed yet.'
  );

  const preview = {
    version: '0.3.0',
    source: 'https://github.com/example/music@abc',
    disclosure: { name: 'music-project-management' }
  };
  const planBlock = () =>
    root.querySelectorAll('section').find(node => node.classList.contains('workspace-build-plan'));
  const reviewing = () => planBlock().classList.contains('workspace-build-plan--install-review');
  assert.equal(reviewing(), false);
  pane.setProviderInstall({ phase: 'review', preview });
  assert.equal(reviewing(), true, 'the review stands in for the conversation');
  assert.equal(document.activeElement, control('install-provider-confirm'), 'focus moves into it');
  assert.equal(control('install-provider').hidden, true);
  assert.equal(
    byClass('workspace-build-plan__install-title').textContent,
    'Install Music Project Management 0.3.0?'
  );
  assert.equal(
    byClass('workspace-build-plan__install-source').textContent,
    'From https://github.com/example/music@abc'
  );
  assert.deepEqual(drawn, [preview.disclosure], 'the Plugins page’s own trust report, drawn once');
  control('install-provider-confirm').click();
  same(events.at(-1), { type: 'workspace-build-install-provider-confirm', detail: {} });

  pane.setProviderInstall({ phase: 'installing', preview });
  assert.equal(control('install-provider-confirm').disabled, true);
  assert.equal(control('install-provider-cancel').hidden, true);
  assert.equal(
    byClass('workspace-build-plan__install-status').textContent,
    'Installing Music Project Management… Then Ori reads the plan again.'
  );

  // A refusal is said under it; the offer stays.
  pane.setProviderInstall({
    note: 'Music Project Management can’t be installed right now. Nothing was changed.'
  });
  assert.equal(control('install-provider').hidden, false);
  assert.equal(reviewing(), false);
  assert.equal(document.activeElement, control('install-provider'), 'focus returns to Install');
  assert.match(byClass('workspace-build-plan__install-status').textContent, /can’t be installed/);

  // Once the plan is ready the offer is gone and Set it up is there.
  pane.applySession({
    ...waiting,
    draft: { name: 'Album-1' },
    plan: {
      ready: true,
      digest: 'd',
      lines: [{ kind: 'home', name: 'Creates Home “Music Production Home”' }]
    }
  });
  assert.equal(control('setup').hidden, false);
});

test('a blueprint’s group is said before Create, with Prepare the group first until it exists', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant });
  const control = kind =>
    root.querySelectorAll('button').find(button => button.dataset.buildControl === kind);
  const block = () =>
    root.querySelectorAll('section').find(node => node.classList.contains('workspace-build-group'));
  const line = () =>
    root.querySelectorAll('p').find(node => node.classList.contains('workspace-build-group__line'));
  const note = () =>
    root.querySelectorAll('p').find(node => node.classList.contains('workspace-build-group__note'));
  const session = {
    status: 'open',
    transcript: [],
    draft: { name: 'Field Notes' },
    needs_home: { label: 'Music Production Home' }
  };

  // No prerequisite: nothing is said.
  pane.applySession(session);
  assert.equal(block().hidden, true);

  const needed = {
    state: 'home_creation_review_required',
    home: { exists: false, proposed_name: 'Music Production Home' },
    actions: ['review_create_home', 'open_guided_setup']
  };
  pane.applySession({ ...session, group_prerequisite: needed });
  assert.equal(block().hidden, false);
  assert.equal(
    line().textContent,
    'This blueprint needs Music Production Home first. Ori prepares the group with your confirmation, then creates the workspace inside it.'
  );
  assert.equal(control('prepare-group').hidden, false);
  assert.equal(control('prepare-group').textContent, 'Prepare the group first');
  control('prepare-group').click();
  same(events.at(-1), { type: 'workspace-build-prepare-group', detail: {} });

  // While it prepares the chip cannot be pressed twice; a stop says why.
  pane.setGroupBusy(true);
  assert.equal(control('prepare-group').disabled, true);
  assert.equal(control('prepare-group').textContent, 'Preparing the group…');
  const before = events.length;
  control('prepare-group').click();
  assert.equal(events.length, before);
  pane.setGroupBusy(false, 'The group setup review failed. Nothing was changed.');
  assert.equal(note().hidden, false);
  assert.equal(note().textContent, 'The group setup review failed. Nothing was changed.');

  // Once the Home exists: Group ready, and no chip.
  pane.setGroupBusy(false);
  pane.applySession({
    ...session,
    group_prerequisite: {
      state: 'ready_grouped',
      home: { exists: true, workspace_id: 'ws-home', name: 'Music Production Home' }
    }
  });
  assert.equal(
    line().textContent,
    'Group ready: Music Production Home. The workspace is created inside it.'
  );
  assert.equal(control('prepare-group').hidden, true);
  assert.equal(note().hidden, true);

  // A group Ori may not create here is said, never offered.
  pane.applySession({
    ...session,
    group_prerequisite: { state: 'home_required', home: { exists: false } }
  });
  assert.match(line().textContent, /can’t prepare from here/);
  assert.equal(control('prepare-group').hidden, true);

  // A closed build says nothing about its group.
  pane.applySession({ ...session, status: 'created', group_prerequisite: needed });
  assert.equal(block().hidden, true);
});

test('the form’s own placement review is said in the pane until the session carries it', () => {
  const { pane, root } = loadPane();
  pane.mount({ assistant });
  const line = () =>
    root.querySelectorAll('p').find(node => node.classList.contains('workspace-build-group__line'));
  const session = { status: 'open', transcript: [], draft: {} };
  pane.applySession(session);
  pane.noteGroupPrerequisite({
    state: 'home_creation_review_required',
    home: { exists: false, proposed_name: 'Neutral Program Home' },
    actions: ['review_create_home']
  });
  assert.match(line().textContent, /^This blueprint needs Neutral Program Home first\./);
  pane.applySession({
    ...session,
    group_prerequisite: {
      state: 'ready_grouped',
      home: { exists: true, name: 'Neutral Program Home' }
    }
  });
  assert.match(line().textContent, /^Group ready: Neutral Program Home\./);
});

// ----- Add link -----

function linkControls(root) {
  const buttons = root.querySelectorAll('button');
  const byControl = kind => buttons.find(button => button.dataset.buildControl === kind);
  const spans = root.querySelectorAll('span');
  return {
    add: byControl('add-link'),
    remove: byControl('remove-link'),
    attach: byControl('link-attach'),
    cancel: byControl('link-cancel'),
    field: root.querySelectorAll('section').find(node => node.id === 'workspaceBuildLinkField'),
    input: root.querySelectorAll('input').find(node => node.id === 'workspaceBuildLinkInput'),
    status: root.querySelectorAll('p').find(node => node.id === 'workspaceBuildLinkStatus'),
    disclosure: root.querySelectorAll('p').find(node => node.id === 'workspaceBuildLinkDisclosure'),
    chip: spans.find(node => node.id === 'workspaceBuildLink'),
    name: spans.find(node => node.id === 'workspaceBuildLinkName'),
    summary: root.querySelectorAll('p').find(node => node.id === 'workspaceBuildLinkSummary'),
    hints: spans.filter(node => node.classList.contains('workspace-build-link__hint'))
  };
}

const repoLink = {
  id: 'link-1',
  url: 'https://github.com/acme/widgets',
  host: 'github.com',
  kind: 'github_repo',
  title: 'acme/widgets',
  readable: true,
  repo: { full_name: 'acme/widgets', language: 'Go', open_issues: 12, reachable: 'yes' }
};

test('the link summary says what the link is and what Ori could see of it', () => {
  const { pane } = loadPane();
  assert.equal(pane.linkSummaryText(repoLink), 'GitHub repository · Go · 12 open issues');
  assert.equal(
    pane.linkSummaryText({
      ...repoLink,
      repo: { ...repoLink.repo, open_issues: 1, archived: true }
    }),
    'GitHub repository · Go · 1 open issue · archived'
  );
  assert.equal(
    pane.linkSummaryText({ ...repoLink, repo: { ...repoLink.repo, reachable: 'no' } }),
    'GitHub repository · not verified'
  );
  assert.equal(
    pane.linkSummaryText({ kind: 'page', host: 'example.com', readable: true }),
    'Page · example.com'
  );
  assert.equal(
    pane.linkSummaryText({ ...repoLink, repo: { ...repoLink.repo, open_issues: 10354 } }),
    'GitHub repository · Go · 10,354 open issues'
  );
  // A document is never opened: it reads the same whether or not the address
  // said anything about it.
  for (const readable of [true, false]) {
    assert.equal(
      pane.linkSummaryText({ kind: 'document', host: 'arxiv.org', readable }),
      'Document · arxiv.org · not read'
    );
  }
  assert.equal(
    pane.linkSummaryText({ kind: 'page', host: 'paywall.example', readable: false }),
    'Could only read the address · paywall.example'
  );
  assert.equal(pane.linkSummaryText(null), '');
});

test('Add link is offered only when the server wires it, and the field sends one address', () => {
  const first = loadPane();
  first.pane.mount({ assistant, folders: true });
  assert.equal(linkControls(first.root).add.hidden, true);

  const { pane, root, events, document } = loadPane();
  pane.mount({ assistant, folders: true, links: true });
  const controls = linkControls(root);
  assert.equal(controls.add.hidden, false);
  assert.equal(controls.field.hidden, true);
  assert.match(controls.disclosure.textContent, /title and description once/);
  assert.equal(
    controls.input.getAttribute('aria-describedby'),
    'workspaceBuildLinkDisclosure workspaceBuildLinkStatus'
  );

  controls.add.click();
  assert.equal(pane.linkFieldOpen(), true);
  assert.equal(controls.field.hidden, false);
  assert.equal(controls.add.getAttribute('aria-expanded'), 'true');
  assert.equal(document.activeElement, controls.input);

  // An empty address is refused in place; nothing is sent.
  const before = events.length;
  controls.attach.click();
  assert.equal(events.length, before);
  assert.equal(controls.status.textContent, 'Paste a link first.');
  assert.equal(controls.status.classList.contains('is-error'), true);

  // Typing clears the refusal; Enter attaches and never submits the reply.
  controls.input.value = ' https://github.com/acme/widgets ';
  controls.input.dispatchEvent(new FakeEvent('input'));
  assert.equal(controls.status.hidden, true);
  controls.input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  same(events.at(-1), {
    type: 'workspace-build-link',
    detail: { url: 'https://github.com/acme/widgets' }
  });
  assert.equal(
    events.some(event => event.type === 'workspace-build-turn'),
    false
  );

  // Escape closes only the field and gives focus back to Add link.
  controls.input.dispatchEvent(new FakeEvent('keydown', { key: 'Escape' }));
  assert.equal(pane.linkFieldOpen(), false);
  assert.equal(controls.field.hidden, true);
  assert.equal(document.activeElement, controls.add);

  // Opening Add folder closes the link field: one control open at a time.
  controls.add.click();
  linkControls(root);
  folderControls(root).add.click();
  assert.equal(pane.linkFieldOpen(), false);
  assert.equal(events.at(-1).type, 'workspace-build-folder-open');
});

test('an attach in flight locks the field and the attachments, and a refusal keeps the address', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant, folders: true, links: true });
  const controls = linkControls(root);
  controls.add.click();
  controls.input.value = 'http://127.0.0.1/admin';
  controls.attach.click();
  const sent = events.length;

  pane.setLinkBusy(true);
  pane.linkFieldStatus('Checking…');
  assert.equal(controls.input.disabled, true);
  assert.equal(controls.attach.disabled, true);
  assert.equal(controls.attach.textContent, 'Checking…');
  assert.equal(controls.add.disabled, true);
  assert.equal(folderControls(root).add.disabled, true);
  controls.attach.click();
  assert.equal(events.length, sent);

  pane.setLinkBusy(false);
  pane.linkFieldStatus('That address can’t be used here.', true);
  assert.equal(pane.linkFieldOpen(), true);
  assert.equal(controls.input.value, 'http://127.0.0.1/admin');
  assert.equal(controls.input.disabled, false);
  assert.equal(controls.attach.textContent, 'Attach');
  assert.equal(controls.status.textContent, 'That address can’t be used here.');

  // Success clears and closes the field.
  pane.linkAttachedDone();
  assert.equal(pane.linkFieldOpen(), false);
  assert.equal(controls.input.value, '');
});

test('the attached link comes from the session: chip, summary, empty Send, and remove', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant, folders: true, links: true });
  const controls = linkControls(root);
  const folder = folderControls(root);
  const input = composerOf(root);
  assert.equal(controls.chip.hidden, true);

  pane.applySession({ transcript: [], link: { observation: repoLink } });
  assert.equal(controls.chip.hidden, false);
  assert.equal(controls.name.textContent, 'acme/widgets');
  assert.equal(controls.remove.getAttribute('aria-label'), 'Remove link acme/widgets');
  assert.equal(controls.summary.textContent, 'GitHub repository · Go · 12 open issues');
  const [linkHint] = controls.hints;
  assert.equal(linkHint.hidden, false);
  assert.equal(linkHint.textContent, 'Empty Send: “Set it up from the attached link”');

  input.value = '';
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  same(events.at(-1), {
    type: 'workspace-build-turn',
    detail: { text: 'Set it up from the attached link.' }
  });

  // A folder proposal includes both observations without a synthetic user turn.
  pane.applySession({ transcript: [], link: { observation: repoLink }, folder: { observation } });
  assert.equal(folder.hint, undefined);
  assert.equal(linkHint.hidden, true);
  const count = events.length;
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  assert.equal(events.length, count);

  controls.remove.click();
  assert.equal(events.at(-1).type, 'workspace-build-link-remove');

  // No link: the folder still needs no empty Send, and the link's chip is gone.
  pane.applySession({ transcript: [], folder: { observation } });
  assert.equal(controls.chip.hidden, true);
  assert.equal(controls.summary.hidden, true);
  assert.equal(linkHint.hidden, true);
  assert.equal(folder.hint, undefined);
});

test('suggestion activity keeps unsent text and focus, and retry is a system action', () => {
  const { pane, root, events, document } = loadPane();
  pane.mount({ assistant, folders: true });
  const input = composerOf(root);
  input.value = 'A thought I have not sent';
  input.focus();
  pane.applySession({
    transcript: [],
    folder: { observation },
    proposal: { id: 'p', status: 'running', attempt: 1 }
  });
  pane.setSuggestion({ message: 'Suggesting a workspace from folder metadata…' });
  assert.equal(input.value, 'A thought I have not sent');
  assert.equal(input.disabled, false);
  assert.equal(document.activeElement, input);
  assert.equal(events.length, 0);
  pane.setSuggestion(null);
  pane.applySession({
    transcript: [],
    folder: { observation },
    proposal: { id: 'p', status: 'failed', attempt: 1 }
  });
  const retry = root
    .querySelectorAll('button')
    .find(button => button.dataset.buildControl === 'suggest-retry');
  assert.equal(retry.hidden, false);
  retry.click();
  same(events.at(-1), { type: 'workspace-build-action', detail: { action: 'suggest_again' } });
  assert.equal(input.value, 'A thought I have not sent');
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  same(events.at(-1), {
    type: 'workspace-build-turn',
    detail: { text: 'A thought I have not sent' }
  });
  assert.equal(input.value, '');
});
