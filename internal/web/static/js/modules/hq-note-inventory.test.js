import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  HQNoteInventory,
  NOTE_INVENTORY_CONNECTION_PATH,
  inboxLine,
  inventoryConnection
} from './hq-note-inventory.js';

// ---- a small element tree: enough of the DOM for the panel's own code -------

function makeDoc() {
  const doc = { activeElement: null };
  class Node {
    constructor(tag) {
      this.tagName = String(tag).toUpperCase();
      this.className = '';
      this.ownText = '';
      this.type = '';
      this.href = '';
      this.disabled = false;
      this.attributes = {};
      this.childNodes = [];
      this.parentNode = null;
      this.listeners = {};
    }
    get textContent() {
      return this.ownText + this.childNodes.map(child => child.textContent).join('');
    }
    set textContent(value) {
      this.childNodes.forEach(child => (child.parentNode = null));
      this.childNodes = [];
      this.ownText = String(value);
    }
    set innerHTML(_html) {
      this.childNodes = [];
    }
    get isConnected() {
      for (let at = this; at; at = at.parentNode) if (at === doc.body) return true;
      return false;
    }
    append(...nodes) {
      nodes.forEach(node => {
        if (node.parentNode) node.parentNode.removeChild(node);
        node.parentNode = this;
        this.childNodes.push(node);
      });
    }
    appendChild(node) {
      this.append(node);
      return node;
    }
    removeChild(node) {
      this.childNodes = this.childNodes.filter(child => child !== node);
      node.parentNode = null;
      if (doc.activeElement && node.contains(doc.activeElement)) doc.activeElement = doc.body;
    }
    replaceChildren(...nodes) {
      [...this.childNodes].forEach(child => this.removeChild(child));
      this.append(...nodes);
    }
    setAttribute(name, value) {
      this.attributes[name] = String(value);
    }
    getAttribute(name) {
      return name in this.attributes ? this.attributes[name] : null;
    }
    hasAttribute(name) {
      return name in this.attributes;
    }
    removeAttribute(name) {
      delete this.attributes[name];
    }
    addEventListener(type, fn) {
      (this.listeners[type] ||= []).push(fn);
    }
    dispatch(type, event = {}) {
      for (let at = this; at; at = at.parentNode) {
        (at.listeners[type] || []).forEach(fn => fn({ target: this, ...event }));
      }
    }
    click() {
      this.dispatch('click');
    }
    focus() {
      doc.activeElement = this;
    }
    contains(node) {
      for (let at = node; at; at = at.parentNode) if (at === this) return true;
      return false;
    }
    matches(selector) {
      return selector.split(',').some(part => matchOne(this, part.trim()));
    }
    closest(selector) {
      for (let at = this; at; at = at.parentNode) if (at.matches?.(selector)) return at;
      return null;
    }
    querySelectorAll(selector) {
      const out = [];
      const walk = node =>
        node.childNodes.forEach(child => {
          if (child.matches(selector)) out.push(child);
          walk(child);
        });
      walk(this);
      return out;
    }
    querySelector(selector) {
      return this.querySelectorAll(selector)[0] || null;
    }
  }
  function matchOne(node, selector) {
    const attr = selector.match(/^([a-z]*)\[([^=\]]+)(?:="([^"]*)")?\]$/);
    if (attr) {
      const [, tag, name, value] = attr;
      if (tag && node.tagName !== tag.toUpperCase()) return false;
      return value === undefined ? node.hasAttribute(name) : node.getAttribute(name) === value;
    }
    if (selector.startsWith('.')) return node.className.split(/\s+/).includes(selector.slice(1));
    return node.tagName === selector.toUpperCase();
  }
  doc.createElement = tag => new Node(tag);
  doc.body = new Node('body');
  doc.activeElement = doc.body;
  return doc;
}

const control = (root, name) => root.querySelector('[data-note-inventory-control="' + name + '"]');
const stateOf = root => root.getAttribute('data-note-inventory-phase');

const OWNED = {
  status: 'connected',
  source: { name: 'My Notes', origin: 'created_by_ori' },
  browse_ref: 'ref-a',
  inbox_count: 0
};

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// ---- reading the connection --------------------------------------------------

test('a connection reads as owned, external, missing, not connected or unknown', () => {
  assert.deepEqual(inventoryConnection(OWNED), {
    phase: 'ready',
    name: 'My Notes',
    owned: true,
    ref: 'ref-a',
    obsidian: false,
    inbox: 0
  });
  const external = inventoryConnection({
    status: 'connected',
    source: { name: 'Vault', origin: 'connected' },
    browse_ref: 'ref-b',
    inbox_count: 4
  });
  assert.equal(external.owned, false);
  assert.equal(external.inbox, null, 'a folder Ori did not set up has no Inbox count');
  assert.deepEqual(inventoryConnection({ status: 'missing', source: { name: 'Vault' } }), {
    phase: 'missing',
    name: 'Vault'
  });
  assert.equal(inventoryConnection({ status: 'not_connected' }).phase, 'not_connected');
  assert.equal(inventoryConnection(null).phase, 'unknown');
  assert.equal(inventoryConnection({ status: 'something-new' }).phase, 'unknown');
});

test('an empty Inbox is not an empty notebook, and a missing count is unknown', () => {
  assert.equal(inboxLine(inventoryConnection(OWNED)), 'Inbox empty');
  assert.equal(inboxLine(inventoryConnection({ ...OWNED, inbox_count: 3 })), '3 in Inbox');
  const noCount = { ...OWNED };
  delete noCount.inbox_count;
  assert.equal(inboxLine(inventoryConnection(noCount)), 'Inbox count not known');
  assert.equal(
    inboxLine(inventoryConnection({ ...OWNED, inbox_count: null })),
    'Inbox count not known'
  );
  assert.equal(inboxLine(inventoryConnection({ status: 'not_connected' })), '');
});

// ---- the panel's lifecycle ---------------------------------------------------

test('a settled map answer opens the panel without asking again', async () => {
  const doc = makeDoc();
  const requests = [];
  const view = new HQNoteInventory({ doc, request: async path => requests.push(path) });
  await view.open({ hqWorkspaceID: 'hq-1', known: OWNED });
  assert.deepEqual(requests, []);
  assert.equal(stateOf(view.root), 'ready');
  assert.match(view.root.textContent, /My Notes/);
  assert.match(view.root.textContent, /Inbox empty/);
  assert.equal(view.hqWorkspaceID, 'hq-1');
});

test('without a settled answer it reads ?view=connection once, saying it is checking', async () => {
  const doc = makeDoc();
  const requests = [];
  const reply = deferred();
  const view = new HQNoteInventory({
    doc,
    request: (path, options) => {
      requests.push({ path, signal: options?.signal });
      return reply.promise;
    }
  });
  const opening = view.open({ hqWorkspaceID: 'hq-1' });
  assert.equal(stateOf(view.root), 'checking');
  assert.equal(
    view.root.querySelector('[data-note-inventory-state]').getAttribute('role'),
    'status'
  );
  assert.deepEqual(
    requests.map(r => r.path),
    [NOTE_INVENTORY_CONNECTION_PATH]
  );
  reply.resolve({ second_brain: OWNED });
  await opening;
  assert.equal(stateOf(view.root), 'ready');
  assert.equal(requests.length, 1, 'no listing, look, capture or preview request');
});

test('a failed read offers Try again, and only Try again reads again', async () => {
  const doc = makeDoc();
  let calls = 0;
  const view = new HQNoteInventory({
    doc,
    request: async () => {
      calls += 1;
      if (calls === 1) throw new Error('Ori could not load your notes. Try again.');
      return { second_brain: OWNED };
    }
  });
  await view.open({ hqWorkspaceID: 'hq-1' });
  assert.equal(stateOf(view.root), 'error');
  assert.match(view.root.textContent, /couldn’t check your notebook/);
  const retry = control(view.root, 'retry');
  assert.ok(retry, 'a retry control');
  assert.equal(control(view.root, 'open-my-notes').href, '/my-notes');
  assert.equal(calls, 1);
  retry.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls, 2);
  assert.equal(stateOf(view.root), 'ready');
});

test('not connected and missing point to the notebook’s own controls, never set up here', async () => {
  const doc = makeDoc();
  const requests = [];
  const view = new HQNoteInventory({ doc, request: async path => requests.push(path) });
  await view.open({ hqWorkspaceID: 'hq-1', known: { status: 'not_connected' } });
  assert.equal(stateOf(view.root), 'not_connected');
  assert.equal(control(view.root, 'repair').href, '/my-notes');
  assert.match(control(view.root, 'repair').textContent, /Set up in My Notes/);

  await view.open({
    hqWorkspaceID: 'hq-1',
    known: { status: 'missing', source: { name: 'Vault' } }
  });
  assert.equal(stateOf(view.root), 'missing');
  assert.match(view.root.textContent, /Can’t find Vault/);
  assert.match(control(view.root, 'repair').textContent, /Repair in My Notes/);
  assert.deepEqual(requests, [], 'nothing is set up, looked at or listed');
});

test('closing drops a reply in flight; a reopened panel ignores the old session', async () => {
  const doc = makeDoc();
  const replies = [];
  const signals = [];
  const view = new HQNoteInventory({
    doc,
    request: (_path, options) => {
      const reply = deferred();
      replies.push(reply);
      signals.push(options?.signal);
      return reply.promise;
    }
  });
  const first = view.open({ hqWorkspaceID: 'hq-1' });
  view.close();
  assert.equal(signals[0].aborted, true, 'the pending read is cancelled');
  assert.equal(view.isOpen, false);
  replies[0].resolve({ second_brain: { status: 'missing', source: { name: 'Old' } } });
  await first;
  assert.equal(stateOf(view.root), 'checking', 'a late reply is not shown');

  const second = view.open({ hqWorkspaceID: 'hq-2' });
  replies[1].resolve({ second_brain: OWNED });
  await second;
  assert.equal(stateOf(view.root), 'ready');
  assert.equal(view.hqWorkspaceID, 'hq-2');
});

test('the head has a labelled close control that is the stat modal’s close action', async () => {
  const doc = makeDoc();
  const view = new HQNoteInventory({ doc, request: async () => ({ second_brain: OWNED }) });
  await view.open({ hqWorkspaceID: 'hq-1', known: OWNED });
  const close = control(view.root, 'close');
  assert.equal(close.tagName, 'BUTTON');
  assert.equal(close.getAttribute('aria-label'), 'Close note inventory');
  assert.equal(close.getAttribute('data-cmd-modal-action'), 'close');
  assert.match(close.textContent, /Close/);
  assert.equal(view.root.querySelector('h2').textContent, 'Note inventory');
});

test('Open My Notes is the plain notebook link in every state', async () => {
  const doc = makeDoc();
  const view = new HQNoteInventory({ doc, request: async () => ({ second_brain: OWNED }) });
  for (const known of [OWNED, { status: 'not_connected' }, { status: 'missing' }]) {
    await view.open({ hqWorkspaceID: 'hq-1', known });
    const open = control(view.root, 'open-my-notes');
    assert.equal(open.tagName, 'A');
    assert.equal(open.href, '/my-notes');
  }
});

test('the same panel element survives reopening', async () => {
  const doc = makeDoc();
  const view = new HQNoteInventory({ doc, request: async () => ({ second_brain: OWNED }) });
  await view.open({ hqWorkspaceID: 'hq-1', known: OWNED });
  const root = view.root;
  view.close();
  await view.open({ hqWorkspaceID: 'hq-1', known: OWNED });
  assert.equal(view.root, root);
});
