import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./create-workspace-build-pane.js', import.meta.url), 'utf8');
const planLinesSource = readFileSync(new URL('./folder-plan-lines.js', import.meta.url), 'utf8');

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
  replaceChildren() {
    this.textContent = '';
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
  // Loaded before the pane, as in base.tmpl.
  const context = vm.createContext({ window, document, CustomEvent });
  vm.runInContext(planLinesSource, context, { filename: 'folder-plan-lines.js' });
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
    'workspace-build-defer'
  ]) {
    modal.addEventListener(type, event => events.push({ type, detail: event.detail }));
  }
  return { pane: window.WorkspaceBuildPane, document, root, modal, buildWith, events };
}

const assistant = { display_name: 'Luna', appearance: {} };

function entries(root) {
  return root.querySelectorAll('li');
}

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
    .find(button => button.textContent === 'Create & prepare review');
  confirm.click();
  assert.equal(events[1].type, 'workspace-build-prepare');
  pane.renderCreationReview(host, { review, busy: true });
  assert.ok(host.querySelectorAll('button').every(button => button.disabled));
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

test('the attached folder comes from the session: chip, summary, empty Send, and remove', () => {
  const { pane, root, events } = loadPane();
  pane.mount({ assistant, folders: true });
  const controls = folderControls(root);
  const input = root.querySelectorAll('textarea')[0];
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
  assert.equal(controls.hint.hidden, false);

  input.value = 'a thing';
  input.dispatchEvent(new FakeEvent('input'));
  assert.equal(controls.hint.hidden, true);
  input.value = '';
  input.dispatchEvent(new FakeEvent('input'));
  assert.equal(controls.hint.hidden, false);

  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  same(events.at(-1), {
    type: 'workspace-build-turn',
    detail: { text: 'Set it up from the attached folder.' }
  });

  controls.remove.click();
  assert.equal(events.at(-1).type, 'workspace-build-folder-remove');

  // A pick in flight locks the attachments and says what is happening.
  pane.setFolderBusy(true, 'Looking at folder metadata locally…');
  assert.equal(controls.add.disabled, true);
  assert.equal(controls.remove.disabled, true);
  assert.equal(controls.summary.textContent, 'Looking at folder metadata locally…');
  controls.remove.click();
  assert.equal(events.at(-1).type, 'workspace-build-folder-remove'); // the earlier one; nothing new
  const count = events.length;
  controls.add.click();
  assert.equal(events.length, count);
  pane.setFolderBusy(false, 'No folder was chosen. Your draft is unchanged.');
  assert.equal(controls.add.disabled, false);
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
  assert.equal(controls.hint.hidden, true);
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
  assert.equal(pane.folderRecognitionText({ verdict: 'none' }), 'Not a project Ori recognizes yet');
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
  assert.equal(line().textContent, 'Not a project Ori recognizes yet');
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
  const why = root.querySelectorAll('details')[0];
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
    hints: spans.filter(node => node.classList.contains('workspace-build-folder__hint'))
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
  const input = root.querySelectorAll('textarea')[0];
  assert.equal(controls.chip.hidden, true);

  pane.applySession({ transcript: [], link: { observation: repoLink } });
  assert.equal(controls.chip.hidden, false);
  assert.equal(controls.name.textContent, 'acme/widgets');
  assert.equal(controls.remove.getAttribute('aria-label'), 'Remove link acme/widgets');
  assert.equal(controls.summary.textContent, 'GitHub repository · Go · 12 open issues');
  const [, linkHint] = controls.hints;
  assert.equal(linkHint.hidden, false);
  assert.equal(linkHint.textContent, 'Empty Send: “Set it up from the attached link”');

  input.value = '';
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  same(events.at(-1), {
    type: 'workspace-build-turn',
    detail: { text: 'Set it up from the attached link.' }
  });

  // With a folder too, one hint and one sentence speak for both.
  pane.applySession({ transcript: [], link: { observation: repoLink }, folder: { observation } });
  assert.equal(folder.hint.hidden, true);
  assert.equal(linkHint.hidden, false);
  assert.equal(linkHint.textContent, 'Empty Send: “Set it up from what I attached”');
  input.dispatchEvent(new FakeEvent('keydown', { key: 'Enter' }));
  same(events.at(-1), {
    type: 'workspace-build-turn',
    detail: { text: 'Set it up from what I attached.' }
  });

  controls.remove.click();
  assert.equal(events.at(-1).type, 'workspace-build-link-remove');

  // No link: the folder's own hint is back, and the link's chip is gone.
  pane.applySession({ transcript: [], folder: { observation } });
  assert.equal(controls.chip.hidden, true);
  assert.equal(controls.summary.hidden, true);
  assert.equal(linkHint.hidden, true);
  assert.equal(folder.hint.hidden, false);
});
