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
    setSelectionRange(start, end) {
      this.selectionStart = start;
      this.selectionEnd = end;
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

// Answers and their batched repaints land on later turns of the event loop.
async function settle(turns = 8) {
  for (let i = 0; i < turns; i++) await new Promise(resolve => setImmediate(resolve));
}

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

const onlyListings = paths => paths.every(path => path.startsWith('/browse?'));

test('a settled map answer opens the panel without reading the connection again', async () => {
  const doc = makeDoc();
  const requests = [];
  const view = new HQNoteInventory({
    doc,
    request: async path => {
      requests.push(path);
      return { directory: { entries: [] } };
    }
  });
  await view.open({ hqWorkspaceID: 'hq-1', known: OWNED });
  await settle();
  assert.equal(requests.length, 3, 'its three sections, nothing else');
  assert.ok(onlyListings(requests), 'no connection read, look, capture or preview');
  assert.equal(stateOf(view.root), 'ready');
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
      if (path === NOTE_INVENTORY_CONNECTION_PATH) return reply.promise;
      return Promise.resolve({ directory: { entries: [] } });
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
  await settle();
  assert.equal(stateOf(view.root), 'ready');
  const after = requests.slice(1).map(r => r.path);
  assert.equal(after.length, 3);
  assert.ok(onlyListings(after), 'then names only: no look, capture or preview');
});

test('a failed read offers Try again, and only Try again reads again', async () => {
  const doc = makeDoc();
  let calls = 0;
  const view = new HQNoteInventory({
    doc,
    request: async path => {
      if (path !== NOTE_INVENTORY_CONNECTION_PATH) return { directory: { entries: [] } };
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
  await settle();
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

// ---- collections, slots and the inspector (group 2) -----------------------------

// A connection reference is 64 hex characters, like the server's.
const REF = 'e'.repeat(64);
const DEMO = {
  status: 'connected',
  source: { name: 'Demo Notes', origin: 'created_by_ori', kind: 'obsidian' },
  browse_ref: REF
};
const FIELD = {
  status: 'connected',
  source: { name: 'Field Notebook', origin: 'connected', kind: 'obsidian' },
  browse_ref: REF
};
const PLAIN = {
  status: 'connected',
  source: { name: 'Plain Folder', origin: 'connected', kind: 'markdown' },
  browse_ref: REF
};
const F = (...names) => names.map(name => ({ name, folder: false }));
const D = (...names) => names.map(name => ({ name, folder: true }));

// A notebook behind the endpoints: listings by folder, previews by path.
function server({ folders = {}, previews = {}, connection = null, hold = () => false } = {}) {
  const log = [];
  const held = [];
  const request = async (path, options = {}) => {
    log.push(path);
    if (hold(path)) {
      await new Promise((resolve, reject) => {
        held.push({ path, resolve });
        options.signal?.addEventListener?.('abort', () => {
          const error = new Error('aborted');
          error.name = 'AbortError';
          reject(error);
        });
      });
    }
    if (path === NOTE_INVENTORY_CONNECTION_PATH) return { second_brain: connection };
    if (path.startsWith('/browse?')) {
      const params = new URLSearchParams(path.slice(8));
      const key = params.getAll('part').join('/');
      const entry = folders[key];
      if (typeof entry === 'number') {
        const error = new Error('refused');
        error.status = entry;
        throw error;
      }
      if (!entry) {
        const error = new Error('That folder is no longer available');
        error.status = 404;
        throw error;
      }
      return { directory: Array.isArray(entry) ? { entries: entry } : entry };
    }
    const preview = previews[path];
    if (typeof preview === 'number') {
      const error = new Error(
        preview === 403 ? 'That note is yours; open it in your editor.' : 'x'
      );
      error.status = preview;
      throw error;
    }
    if (preview) return { preview };
    throw new Error('unexpected request ' + path);
  };
  return { request, log, held };
}

const HOUSE_FOLDERS = {
  Inbox: F('README.md'),
  Projects: [...F('README.md'), ...D('House', 'Studio')],
  'Projects/House': F('README.md'),
  'Projects/Studio': F('plan.md'),
  Daily: F('README.md', '2026-11-24.md', '2026-11-25.md')
};
const HOUSE_PREVIEW = '/preview/project/House?ref=' + REF;

const slots = root =>
  root
    .querySelectorAll('[data-note-inventory-action="select"]')
    .map(node => node.querySelector('.hq-note-inventory__slot-title').textContent);
const slotFor = (root, id) =>
  root
    .querySelectorAll('[data-note-inventory-action="select"]')
    .find(node => node.getAttribute('data-note-inventory-value') === id);
const tab = (root, key) => control(root, 'tab:' + key);
const inspector = root => root.querySelector('.hq-note-inventory__inspector');

test('an Ori notebook shows Inbox, Projects and Journal with known counts and real names', async () => {
  const doc = makeDoc();
  const notes = server({ folders: HOUSE_FOLDERS });
  const view = new HQNoteInventory({ doc, request: notes.request });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  const root = view.root;
  assert.match(tab(root, 'all').textContent, /All notes4/);
  assert.match(tab(root, 'inbox').textContent, /Inbox0/);
  assert.match(tab(root, 'projects').textContent, /Projects2/);
  assert.match(tab(root, 'journal').textContent, /Journal2/);
  assert.equal(tab(root, 'all').getAttribute('aria-pressed'), 'true');
  assert.match(
    root.textContent,
    /Not every file in the notebook/,
    'the combined view says what it covers'
  );
  assert.match(root.textContent, /Inbox empty/, 'a populated notebook with an empty Inbox');
  assert.deepEqual(slots(root), ['House', 'Studio', 'Wed 25 Nov', 'Tue 24 Nov']);
  assert.match(inspector(root).textContent, /Pick a note/);
  // Nothing read a note: three listings and one look inside each visible project.
  assert.ok(notes.log.every(path => path.startsWith('/browse?')));
  assert.equal(notes.log.length, 5);
  assert.match(slotFor(root, 'project:House').textContent, /Project note/);
  assert.match(slotFor(root, 'project:Studio').textContent, /No project note/);
});

test('selecting House reads its note once and shows it as plain text with its place', async () => {
  const doc = makeDoc();
  const notes = server({
    folders: HOUSE_FOLDERS,
    previews: {
      [HOUSE_PREVIEW]: {
        title: 'Before the second visit',
        body: '# Before the second visit\n\nBring a tape measure.\n\n<img src=x onerror="alert(1)"> stays text.',
        truncated: true
      }
    }
  });
  const view = new HQNoteInventory({ doc, request: notes.request, narrow: () => false });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  const before = notes.log.length;
  slotFor(view.root, 'project:House').click();
  await settle();
  assert.deepEqual(notes.log.slice(before), [HOUSE_PREVIEW], 'exactly one preview, for House');
  const pane = inspector(view.root);
  assert.equal(slotFor(view.root, 'project:House').getAttribute('aria-pressed'), 'true');
  assert.equal(pane.querySelector('h3').textContent, 'House');
  assert.match(pane.textContent, /Selected note \/ 01/);
  assert.match(pane.textContent, /Before the second visit/);
  const texts = pane.querySelectorAll('.hq-note-inventory__paper-text').map(n => n.textContent);
  assert.deepEqual(
    texts,
    ['Bring a tape measure.', '<img src=x onerror="alert(1)"> stays text.'],
    'the note’s own "# title" line is the title above, not repeated; the rest stays text'
  );
  assert.match(view.root.textContent, /All notes \/ 04/, 'an exact count reads like a slot number');
  assert.match(pane.textContent, /Showing the first part of this note/);
  assert.match(pane.textContent, /Demo Notes \/ Projects \/ House/);
  assert.ok(!/\/(?:Users|private|var)\//.test(view.root.textContent), 'no machine path');
  assert.equal(doc.activeElement, slotFor(view.root, 'project:House'), 'focus stays on the slot');
});

test('a project folder without a README is not given a note, and nothing is read', async () => {
  const doc = makeDoc();
  const notes = server({ folders: HOUSE_FOLDERS });
  const view = new HQNoteInventory({ doc, request: notes.request, narrow: () => false });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  const before = notes.log.length;
  slotFor(view.root, 'project:Studio').click();
  await settle();
  assert.deepEqual(notes.log.slice(before), [], 'no preview for a folder without a note');
  assert.match(inspector(view.root).textContent, /no project note \(README\.md\) yet/);
  assert.ok(
    control(view.root, 'obsidian').href.startsWith('obsidian://open?vault=Demo%20Notes&file='),
    'the existing Obsidian handoff'
  );
});

test('a project chosen before its folder is looked inside is read once the look finds its note', async () => {
  const doc = makeDoc();
  let release;
  const gate = new Promise(resolve => (release = resolve));
  const notes = server({
    folders: HOUSE_FOLDERS,
    previews: { [HOUSE_PREVIEW]: { title: 'House', body: 'Paint the porch.' } }
  });
  const request = async (path, options) => {
    if (path.includes('part=House')) await gate;
    return notes.request(path, options);
  };
  const view = new HQNoteInventory({ doc, request, narrow: () => false });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  slotFor(view.root, 'project:House').click();
  await settle();
  assert.match(inspector(view.root).textContent, /Looking for this project’s note/);
  assert.ok(!notes.log.includes(HOUSE_PREVIEW));
  release();
  await settle();
  assert.equal(notes.log.filter(path => path === HOUSE_PREVIEW).length, 1);
  assert.match(inspector(view.root).textContent, /Paint the porch/);
});

test('a section tab pages through names it already has, checking only the projects on screen', async () => {
  const projects = Array.from({ length: 20 }, (_, i) => 'Project ' + String(i).padStart(2, '0'));
  const folders = { Inbox: [], Daily: [], Projects: D(...projects) };
  for (const name of projects) folders['Projects/' + name] = F('README.md');
  const doc = makeDoc();
  const notes = server({ folders });
  const view = new HQNoteInventory({ doc, request: notes.request });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  const looks = () => notes.log.filter(path => path.includes('part=Projects&part=')).length;
  assert.equal(looks(), 4, 'All notes shows four projects and looks inside four');
  tab(view.root, 'projects').click();
  await settle();
  assert.match(view.root.textContent, /Project notes \/ 20/);
  assert.equal(slots(view.root).length, 8);
  assert.equal(looks(), 8);
  assert.equal(doc.activeElement, tab(view.root, 'projects'));
  const listings = notes.log.filter(path => /part=(Inbox|Projects|Daily)$/.test(path)).length;
  control(view.root, 'more:projects').click();
  await settle();
  assert.equal(slots(view.root).length, 16);
  assert.equal(looks(), 16);
  assert.equal(
    notes.log.filter(path => /part=(Inbox|Projects|Daily)$/.test(path)).length,
    listings,
    'showing more lists nothing again'
  );
  assert.match(control(view.root, 'more:projects').textContent, /4 more listed/);
});

test('a limited listing is a lower bound, and a missing section leaves the rest usable', async () => {
  const doc = makeDoc();
  const notes = server({
    folders: {
      Inbox: {
        entries: F(...Array.from({ length: 200 }, (_, i) => 'n' + i + '.md')),
        partial: true
      },
      Projects: D('House'),
      'Projects/House': F('README.md')
    }
  });
  const view = new HQNoteInventory({ doc, request: notes.request });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  assert.match(tab(view.root, 'inbox').textContent, /200\+/);
  assert.equal(tab(view.root, 'inbox').getAttribute('aria-label'), 'Inbox, at least 200');
  assert.match(tab(view.root, 'all').textContent, /201\+/);
  assert.match(view.root.textContent, /There is no Daily folder in this notebook/);
  assert.ok(control(view.root, 'retry:journal'), 'the missing section can be tried again');
  assert.ok(slotFor(view.root, 'project:House'), 'Projects still works');
  tab(view.root, 'inbox').click();
  await settle();
  assert.match(view.root.textContent, /Ori lists up to 200 names/);
});

test('fast A→B shows only B, and closing mid-read shows nothing late', async () => {
  const doc = makeDoc();
  const a = '/preview/daily/2026-11-24.md?ref=' + REF;
  const b = '/preview/daily/2026-11-25.md?ref=' + REF;
  const notes = server({
    folders: HOUSE_FOLDERS,
    previews: { [a]: { title: 'A', body: 'from A' }, [b]: { title: 'B', body: 'from B' } },
    hold: path => path === a
  });
  const view = new HQNoteInventory({ doc, request: notes.request, narrow: () => false });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  slotFor(view.root, 'daily:2026-11-24.md').click();
  slotFor(view.root, 'daily:2026-11-25.md').click();
  await settle();
  notes.held.forEach(entry => entry.resolve());
  await settle();
  assert.match(inspector(view.root).textContent, /from B/);
  assert.ok(!/from A/.test(inspector(view.root).textContent));

  const slow = server({
    folders: HOUSE_FOLDERS,
    previews: { [a]: { body: 'late' } },
    hold: p => p === a
  });
  const other = new HQNoteInventory({ doc, request: slow.request, narrow: () => false });
  await other.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  slotFor(other.root, 'daily:2026-11-24.md').click();
  other.close();
  slow.held.forEach(entry => entry.resolve());
  await settle();
  assert.ok(!/late/.test(other.root.textContent));
});

test('a refused or moved note says so and offers the existing doors; a changed connection is re-read', async () => {
  const doc = makeDoc();
  const day = '/preview/daily/2026-11-25.md?ref=' + REF;
  const notes = server({ folders: HOUSE_FOLDERS, previews: { [day]: 403 } });
  const view = new HQNoteInventory({ doc, request: notes.request, narrow: () => false });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  slotFor(view.root, 'daily:2026-11-25.md').click();
  await settle();
  assert.match(inspector(view.root).textContent, /That note is yours/);
  assert.ok(control(view.root, 'obsidian'));

  const changed = server({
    folders: HOUSE_FOLDERS,
    previews: { [day]: 409 },
    connection: { ...DEMO, browse_ref: 'n'.repeat(64) }
  });
  const again = new HQNoteInventory({ doc, request: changed.request, narrow: () => false });
  await again.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  slotFor(again.root, 'daily:2026-11-25.md').click();
  await settle();
  assert.ok(changed.log.includes(NOTE_INVENTORY_CONNECTION_PATH), 'the connection is read again');
  assert.ok(changed.log.some(path => path.startsWith('/browse?ref=' + 'n'.repeat(64))));
  assert.match(inspector(again.root).textContent, /Pick a note/, 'nothing from the old one');
});

test('a connected folder lists its own root and opened folders, never a body, with its own door', async () => {
  const doc = makeDoc();
  const notes = server({
    folders: {
      '': [...D('Expeditions', 'Field Log 2026'), ...F('Welcome.md')],
      Expeditions: F('Canyon.md')
    }
  });
  const view = new HQNoteInventory({ doc, request: notes.request, narrow: () => false });
  await view.open({ hqWorkspaceID: 'hq-1', known: FIELD });
  await settle();
  assert.equal(tab(view.root, 'all'), null, 'no Inbox, Projects or Journal assumed');
  assert.equal(view.root.getAttribute('data-note-inventory-mode'), 'folder');
  assert.deepEqual(slots(view.root), ['Expeditions', 'Field Log 2026', 'Welcome']);
  assert.match(view.root.textContent, /never changes it/);
  slotFor(view.root, 'folder:Expeditions').click();
  await settle();
  assert.deepEqual(slots(view.root), ['Canyon']);
  assert.equal(
    doc.activeElement,
    slotFor(view.root, 'file:Canyon.md'),
    'once the folder is listed, focus is on its first name'
  );
  slotFor(view.root, 'file:Canyon.md').click();
  await settle();
  assert.ok(!notes.log.some(path => path.startsWith('/preview')), 'no body is requested');
  assert.match(inspector(view.root).textContent, /Field Notebook \/ Expeditions \/ Canyon\.md/);
  assert.equal(
    control(view.root, 'obsidian').href,
    'obsidian://open?vault=Field%20Notebook&file=Expeditions%2FCanyon'
  );
  assert.ok(!control(view.root, 'quick-note'), 'nothing writable');
  control(view.root, 'crumb:0').click();
  await settle();
  assert.deepEqual(slots(view.root), ['Expeditions', 'Field Log 2026', 'Welcome']);
  assert.deepEqual(notes.log, ['/browse?ref=' + REF, '/browse?ref=' + REF + '&part=Expeditions']);
});

test('a plain Markdown folder uses the open-folder door, not an Obsidian link', async () => {
  const doc = makeDoc();
  const opened = [];
  const notes = server({ folders: { '': F('Welcome.md') } });
  const view = new HQNoteInventory({
    doc,
    request: notes.request,
    narrow: () => false,
    openFolder: async () => {
      opened.push('open');
      return { ok: true };
    }
  });
  await view.open({ hqWorkspaceID: 'hq-1', known: PLAIN });
  await settle();
  slotFor(view.root, 'file:Welcome.md').click();
  await settle();
  assert.equal(control(view.root, 'obsidian'), null);
  assert.deepEqual(opened, [], 'nothing opens by itself');
  control(view.root, 'open-folder').click();
  await settle();
  assert.deepEqual(opened, ['open']);
});

test('on a phone the slots give way to the inspector, and Back returns to the same slot', async () => {
  const doc = makeDoc();
  const notes = server({
    folders: HOUSE_FOLDERS,
    previews: { [HOUSE_PREVIEW]: { title: 'House', body: 'Paint the porch.' } }
  });
  const view = new HQNoteInventory({ doc, request: notes.request, narrow: () => true });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  assert.equal(view.root.getAttribute('data-note-inventory-pane'), 'slots');
  slotFor(view.root, 'project:House').click();
  await settle();
  assert.equal(view.root.getAttribute('data-note-inventory-pane'), 'inspector');
  assert.equal(doc.activeElement, control(view.root, 'inspector-title'));
  assert.match(control(view.root, 'back').textContent, /Back to All notes/);
  control(view.root, 'back').click();
  assert.equal(view.root.getAttribute('data-note-inventory-pane'), 'slots');
  assert.equal(doc.activeElement, slotFor(view.root, 'project:House'));
});

// ---- Quick note and the reader link (group 3) ----------------------------------

const live = root => root.querySelector('[data-note-inventory-live]').textContent;
const draftField = root => control(root, 'draft');
const captureForm = root => root.querySelector('[data-note-inventory-capture]');

function type(root, text) {
  const field = draftField(root);
  field.value = text;
  field.dispatch('input');
}

function submit(root) {
  captureForm(root).dispatch('submit', { preventDefault() {} });
}

// A notebook whose Inbox grows by one file per confirmed capture.
function capturingServer({ fail = () => null } = {}) {
  const inbox = ['README.md'];
  const folders = { ...HOUSE_FOLDERS };
  Object.defineProperty(folders, 'Inbox', { get: () => F(...inbox), enumerable: true });
  const notes = server({
    folders,
    previews: { [HOUSE_PREVIEW]: { title: 'House', body: 'Paint the porch.' } }
  });
  const posts = [];
  const post = async (path, body, id) => {
    posts.push({ path, body, id });
    const problem = fail(posts.length);
    if (problem) throw problem;
    const file = '20261126-1012-ask-about-the-roof-warranty.md';
    if (!inbox.includes(file)) inbox.push(file);
    return { capture: { note: 'Inbox/' + file, name: file.slice(0, -3) } };
  };
  return { ...notes, posts, post, inbox };
}

async function openWithHouse(srv, options = {}) {
  const doc = makeDoc();
  const saved = [];
  const view = new HQNoteInventory({
    doc,
    request: srv.request,
    post: srv.post,
    narrow: () => false,
    onSaved: () => saved.push('saved'),
    ...options
  });
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  slotFor(view.root, 'project:House').click();
  await settle();
  return { doc, view, saved };
}

test('Quick note appears only where Ori may write, and says where the note goes first', async () => {
  for (const known of [FIELD, PLAIN, { status: 'missing' }, { status: 'not_connected' }]) {
    const doc = makeDoc();
    const view = new HQNoteInventory({ doc, request: server({ folders: { '': [] } }).request });
    await view.open({ hqWorkspaceID: 'hq-1', known });
    await settle();
    assert.equal(control(view.root, 'quick-note'), null, JSON.stringify(known.source || known));
  }
  const srv = capturingServer();
  const { doc, view } = await openWithHouse(srv);
  const before = srv.log.length;
  control(view.root, 'quick-note').click();
  assert.match(view.root.querySelector('.is-capture').textContent, /Save to My Notes → Inbox/);
  assert.equal(doc.activeElement, draftField(view.root));
  assert.equal(srv.log.length, before, 'opening Quick note sends nothing');
  assert.deepEqual(srv.posts, []);
  assert.equal(view.root.querySelector('[id]'), null, 'no ids to collide with HQ Overview');
});

test('saving: one capture for a double submit, 0 → 1 in the real Inbox, House stays selected', async () => {
  const srv = capturingServer();
  const { doc, view, saved } = await openWithHouse(srv);
  assert.match(tab(view.root, 'inbox').textContent, /Inbox0/);
  control(view.root, 'quick-note').click();
  type(view.root, 'Ask about the roof warranty');
  submit(view.root);
  submit(view.root);
  assert.equal(control(view.root, 'save').disabled, true, 'Save is disabled while saving');
  assert.match(control(view.root, 'save').textContent, /Saving/);
  await settle();
  assert.equal(srv.posts.length, 1, 'one capture');
  assert.equal(srv.posts[0].path, '/capture');
  assert.deepEqual(srv.posts[0].body, { text: 'Ask about the roof warranty', source_ref: REF });
  assert.deepEqual(saved, ['saved'], 'the page hears about it once');
  assert.equal(live(view.root), 'Saved to My Notes → Inbox.');
  assert.match(tab(view.root, 'inbox').textContent, /Inbox1/, 'listed again, not counted up here');
  assert.equal(srv.inbox.length, 2, 'exactly one new file');
  assert.equal(slotFor(view.root, 'project:House').getAttribute('aria-pressed'), 'true');
  assert.match(
    inspector(view.root).textContent,
    /Paint the porch/,
    'the House inspector is unchanged'
  );
  assert.equal(doc.activeElement, slotFor(view.root, 'project:House'));
  // A repaint does not announce again.
  view.render();
  assert.equal(live(view.root), 'Saved to My Notes → Inbox.');
  slotFor(view.root, 'project:Studio').click();
  assert.equal(live(view.root), '');
});

test('a failed or uncertain save keeps the draft and retries with the same request id', async () => {
  const lost = new Error('Ori could not confirm the save. Your draft is still here.');
  const srv = capturingServer({ fail: n => (n === 1 ? lost : null) });
  const { doc, view, saved } = await openWithHouse(srv);
  control(view.root, 'quick-note').click();
  type(view.root, 'Ask about the roof warranty');
  submit(view.root);
  await settle();
  assert.equal(saved.length, 0);
  assert.equal(draftField(view.root).value, 'Ask about the roof warranty');
  assert.match(view.root.querySelector('[role="alert"]').textContent, /could not confirm/);
  assert.equal(doc.activeElement, draftField(view.root));
  assert.equal(draftField(view.root).selectionStart, 'Ask about the roof warranty'.length);
  assert.equal(live(view.root), '', 'no success without a receipt');
  submit(view.root);
  await settle();
  assert.equal(srv.posts.length, 2);
  assert.equal(srv.posts[1].id, srv.posts[0].id, 'the same request id');
  assert.deepEqual(saved, ['saved']);
});

test('a draft from another connection is never sent there: it can only be copied or discarded', async () => {
  const srv = capturingServer();
  const { view } = await openWithHouse(srv);
  control(view.root, 'quick-note').click();
  type(view.root, 'Ask about the roof warranty');
  // The notebook was reconnected (a new reference) while the draft waited.
  view.accept({ ...DEMO, browse_ref: 'n'.repeat(64) });
  await settle();
  control(view.root, 'quick-note').click();
  assert.match(view.root.textContent, /Draft from your previous notes connection/);
  assert.equal(draftField(view.root).readOnly, true);
  assert.equal(control(view.root, 'save'), null);
  submit(view.root);
  await settle();
  assert.deepEqual(srv.posts, []);
  control(view.root, 'discard-draft').click();
  assert.equal(view.draft.text, '');
});

test('cancel and Escape go back to the selected note; the draft stays for this HQ only', async () => {
  const srv = capturingServer();
  const { doc, view } = await openWithHouse(srv);
  control(view.root, 'quick-note').click();
  type(view.root, 'Half a thought');
  control(view.root, 'cancel-capture').click();
  assert.match(inspector(view.root).textContent, /Paint the porch/);
  assert.equal(doc.activeElement, slotFor(view.root, 'project:House'));
  assert.match(control(view.root, 'quick-note').textContent, /draft/);
  control(view.root, 'quick-note').click();
  assert.equal(draftField(view.root).value, 'Half a thought');
  let stopped = false;
  draftField(view.root).dispatch('keydown', {
    key: 'Escape',
    stopPropagation: () => (stopped = true)
  });
  assert.equal(stopped, true, 'Escape stays with the form, not the whole panel');
  assert.equal(view.root.querySelector('.is-capture'), null);

  view.close();
  await view.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  assert.equal(view.draft.text, 'Half a thought', 'kept in memory for this HQ');
  view.close();
  await view.open({ hqWorkspaceID: 'hq-2', known: DEMO });
  assert.equal(view.draft.text, '', 'another HQ starts clean');
  assert.deepEqual(srv.posts, []);
});

test('a save that finishes after closing is still announced to the page', async () => {
  let release;
  const srv = capturingServer();
  const slowPost = async (...args) => {
    await new Promise(resolve => (release = resolve));
    return srv.post(...args);
  };
  const { view, saved } = await openWithHouse(srv, { post: slowPost });
  control(view.root, 'quick-note').click();
  type(view.root, 'Ask about the roof warranty');
  submit(view.root);
  view.close();
  release();
  await settle();
  assert.deepEqual(saved, ['saved']);
  assert.equal(live(view.root), '');
});

test('Read full note opens that note in My Notes, worded for what My Notes will show', async () => {
  const srv = capturingServer();
  const { view } = await openWithHouse(srv);
  const read = control(view.root, 'read-note');
  assert.equal(read.textContent.trim().startsWith('Read full note'), true);
  assert.equal(read.href, '/my-notes#ref=' + REF + '&kind=project&name=House');
  assert.equal(control(view.root, 'open-my-notes').href, '/my-notes', 'Open My Notes stays plain');
  slotFor(view.root, 'project:Studio').click();
  await settle();
  assert.equal(control(view.root, 'read-note'), null, 'no page for a folder without a note');

  const long = server({
    folders: HOUSE_FOLDERS,
    previews: { [HOUSE_PREVIEW]: { title: 'House', body: 'x', truncated: true } }
  });
  const other = await openWithHouse({ ...long, post: async () => ({}) });
  assert.match(control(other.view.root, 'read-note').textContent, /Open in My Notes/);
  assert.ok(control(other.view.root, 'obsidian'), 'the whole file is a door away');

  const day = server({
    folders: HOUSE_FOLDERS,
    previews: { ['/preview/daily/2026-11-25.md?ref=' + REF]: { title: 'Day', body: 'x' } }
  });
  const doc = makeDoc();
  const journal = new HQNoteInventory({ doc, request: day.request, narrow: () => false });
  await journal.open({ hqWorkspaceID: 'hq-1', known: DEMO });
  await settle();
  slotFor(journal.root, 'daily:2026-11-25.md').click();
  await settle();
  assert.equal(
    control(journal.root, 'read-note').href,
    '/my-notes#ref=' + REF + '&kind=daily&name=2026-11-25.md'
  );
});
