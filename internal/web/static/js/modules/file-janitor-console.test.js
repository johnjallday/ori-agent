// Tests for file-janitor-console.js — the File Janitor console and the
// panel. Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/file-janitor-console.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = (tag || 'div').toUpperCase();
    this.hidden = false;
    this._text = '';
    this.className = '';
    this.value = '';
    this.disabled = false;
    this.children = [];
    this._attrs = {};
    this._listeners = {};
  }
  get textContent() {
    if (this.children.length) return this._text + this.children.map(c => c.textContent).join(' ');
    return this._text;
  }
  set textContent(v) {
    this._text = v;
    if (v !== '') return;
    // Clearing a node detaches its subtree, so the ids inside it stop
    // resolving — exactly as in a browser. Without this the id map kept
    // pointing at nodes that were no longer on screen, and a test could assert
    // that a control still existed after the surface holding it was rebuilt.
    this.children.forEach(child => child.forgetIDs());
    this.children = [];
  }
  forgetIDs() {
    if (this.id && globalThis.document.byId.get(this.id) === this) {
      globalThis.document.byId.delete(this.id);
    }
    this.children.forEach(child => child.forgetIDs());
  }
  appendChild(el) {
    this.children.push(el);
    el.parent = this;
    // Register the whole subtree: attaching a populated node puts every id
    // inside it into the document at once, which is what a browser does.
    el.registerIDs();
    return el;
  }
  registerIDs() {
    if (this.id) globalThis.document.byId.set(this.id, this);
    this.children.forEach(child => child.registerIDs());
  }
  // Reachable from <body>, i.e. actually in the document.
  get attached() {
    let node = this;
    while (node) {
      if (node === globalThis.document.body) return true;
      node = node.parent;
    }
    return false;
  }
  addEventListener(ev, fn) {
    (this._listeners[ev] ||= []).push(fn);
  }
  click() {
    (this._listeners.click || []).forEach(fn => fn());
  }
  setAttribute(k, v) {
    this._attrs[k] = v;
  }
  getAttribute(k) {
    return this._attrs[k];
  }
  get selected() {
    return this._selected === true;
  }
  set selected(v) {
    this._selected = v;
  }
  dispatch(event) {
    (this._listeners[event] || []).forEach(fn => fn());
  }
  focus() {
    // A disabled control cannot take focus in a real browser, and the panel
    // relies on that being true.
    if (this.disabled) return;
    globalThis.document.activeElement = this;
  }
  removeAttribute(k) {
    delete this._attrs[k];
  }
  get isConnected() {
    return this._connected !== false;
  }
  // Depth-first collection helpers used by the assertions below.
  all(predicate, out = []) {
    if (predicate(this)) out.push(this);
    this.children.forEach(c => c.all(predicate, out));
    return out;
  }
}

class FakeDocument {
  constructor() {
    this.byId = new Map();
    this.readyState = 'complete';
    this.body = new FakeElement('body');
    this.activeElement = this.body;
  }
  register(id) {
    const el = new FakeElement('div');
    el.id = id;
    // Attached to <body>, like the real template's mount point. A detached
    // fixture would be resolvable here and not in a browser.
    this.body.appendChild(el);
    return el;
  }
  // Only nodes actually in the document resolve.
  //
  // A flat id map resolved detached nodes too, which let real code pass here
  // and fail in a browser: the console populated its body BEFORE attaching the
  // dialog, so every getElementById inside the tab renderers returned null and
  // Settings and Review rendered empty on first paint. Nothing in this harness
  // could see it until attachment mattered.
  getElementById(id) {
    const el = this.byId.get(id);
    if (!el) return null;
    return el.attached ? el : null;
  }
  createElement(tag) {
    return new FakeElement(tag);
  }
  addEventListener() {}
}

function setup() {
  const doc = new FakeDocument();
  // The global goes first: registering a node attaches it, and attachment
  // records ids against globalThis.document.
  globalThis.document = doc;
  doc.register('downloadsJanitorMount');
  globalThis.window = globalThis;
  globalThis.window.currentWorkspaceId = 'ws-1';
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ status: { applies: false } }) });
  // A fresh document is a fresh page load; the module is a singleton that
  // remembers the tab it last showed, so it is reset alongside it.
  if (globalThis.window.FileJanitorConsole) globalThis.window.FileJanitorConsole._resetForTest();
  return doc;
}

// The review table, the setup card, history, and settings all live in the
// console now; Workspace Details carries a compact card only. openConsole
// drives the same path every entry point does — render the status, then open —
// so these tests exercise the surface a user actually reaches rather than a
// detached renderer.
//
// The fetch stub answers with the SAME status, because open() re-reads on the
// way in: a console opened from the Map may be showing minutes-old state. An
// answer that disagreed would repaint the assertions out from under the test.
function openConsole(status, tab) {
  const previousFetch = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ status }) });
  panel.render(status);
  panel.open({ source: 'test', tab });
  globalThis.fetch = previousFetch;
  return globalThis.document.getElementById('fileJanitorConsoleBody');
}

const panel = await (async () => {
  setup();
  await import('./file-janitor-console.js');
  return globalThis.window.DownloadsJanitorPanel;
})();

const setupRequiredStatus = {
  applies: true,
  settings: {
    workspace_id: 'ws-1',
    filing_root_name: 'Filed',
    daily_scan_local_time: '09:00',
    content_mode: 'metadata_only'
  },
  readiness: { state: 'setup_required', checks: [] },
  suggestion: {
    key: 'downloads-root',
    label: 'Downloads folder',
    suggested_path: '~/Downloads',
    access_disclosure: 'Ori can list files here.',
    filing_root_name: 'Filed',
    daily_scan_local_time: '09:00'
  }
};

// surface is whichever host is showing File Janitor right now: the console
// while it is open, the compact Workspace Details card otherwise.
// surface is whichever host is showing File Janitor right now: the whole
// console — header included, since the folder and the status live there —
// while it is open, and the compact Workspace Details card otherwise.
function surface(doc) {
  return doc.getElementById('fileJanitorConsole') || doc.getElementById('downloadsJanitorMount');
}

// settle drains the status re-read that open() fires. Opening the console
// re-reads status on the way in, so an async test that acts immediately is
// racing that reload — in a browser the user cannot click inside the same tick,
// but a test can. Await this before driving a flow that depends on the batch
// staying put.
function settle() {
  return new Promise(resolve => setTimeout(resolve, 0));
}

function text(doc) {
  return surface(doc).textContent;
}

// cardText reads only the compact Workspace Details card, for the assertions
// about what Details does and does not contain.
function cardText(doc) {
  return doc.getElementById('downloadsJanitorMount').textContent;
}

test('a workspace without the capability renders nothing and opens no console', () => {
  const doc = setup();
  openConsole({ applies: false });
  const host = doc.getElementById('downloadsJanitorMount');
  assert.equal(host.hidden, true);
  assert.equal(host.children.length, 0);
  // There is nothing to open: a console over a workspace that never installed
  // File Janitor would offer controls for a folder it does not manage.
  assert.equal(panel.isOpen(), false);
});

test('setup card pre-fills the suggested folder without selecting it', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  const host = surface(doc);
  assert.equal(host.hidden, false);
  const input = doc.getElementById('downloadsJanitorPath');
  assert.ok(input, 'expected a folder input');
  // Still the unresolved suggestion: the card offers it, the user confirms it.
  assert.equal(input.value, '~/Downloads');
  assert.match(text(doc), /Setup required/);
});

test('setup card discloses moves, Trash, no permanent deletion, and the daily time', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  const body = text(doc);
  assert.match(body, /Filed/);
  assert.match(body, /system Trash/i);
  assert.match(body, /never deletes anything permanently/i);
  assert.match(body, /09:00/);
  assert.match(body, /Nothing moves without your approval/i);
});

test('setup card states that content reading is off and separate', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  assert.match(text(doc), /Reading what is inside your files is off/i);
});

// FR-45: the user is told what kind of folder this is for, so they do not point
// it at a structured project tree and expect it to be reorganized.
test('setup card explains it is for an inbox-style folder, top level only', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  const body = text(doc);
  assert.match(body, /inbox-style/i);
  assert.match(body, /directly in it/i);
  assert.match(body, /never reorganizes folders inside it/i);
});

// FR-44/FR-50: only a preset may propose a path. Pre-filling a real folder the
// user never chose, beside a button that grants access to it, would turn an
// explicit approval into a default.
test('setup card proposes no folder when the server suggests none', () => {
  const doc = setup();
  openConsole({
    ...setupRequiredStatus,
    suggestion: {
      key: 'file-janitor-root',
      filing_root_name: 'Filed',
      daily_scan_local_time: '09:00'
    }
  });
  const input = doc.getElementById('downloadsJanitorPath');
  assert.equal(input.value, '', 'a generic install must not invent a folder');
  const confirm = doc.getElementById('downloadsJanitorConfirm');
  assert.equal(confirm.disabled, true, 'there is nothing to approve yet');
});

test('the confirm button enables once a folder is supplied', () => {
  const doc = setup();
  openConsole({
    ...setupRequiredStatus,
    suggestion: {
      key: 'file-janitor-root',
      filing_root_name: 'Filed',
      daily_scan_local_time: '09:00'
    }
  });
  const input = doc.getElementById('downloadsJanitorPath');
  const confirm = doc.getElementById('downloadsJanitorConfirm');
  assert.equal(confirm.disabled, true);

  input.value = '/Users/someone/Scans';
  input.dispatch('input');
  assert.equal(confirm.disabled, false, 'a supplied folder can be approved');

  input.value = '   ';
  input.dispatch('input');
  assert.equal(confirm.disabled, true, 'whitespace is not a folder');
});

test('a preset suggestion still pre-fills and is immediately approvable', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  const input = doc.getElementById('downloadsJanitorPath');
  assert.equal(input.value, '~/Downloads', 'the Downloads preset still suggests its folder');
  const confirm = doc.getElementById('downloadsJanitorConfirm');
  assert.equal(confirm.disabled, false);
});

test('the setup card is titled File Janitor, not Downloads Janitor', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  const title = doc.getElementById('downloadsJanitorTitle');
  assert.equal(title.textContent, 'File Janitor');
});

test('the folder input is labelled and described for screen readers', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  const host = surface(doc);
  const label = host.all(n => n.tagName === 'LABEL')[0];
  assert.ok(label, 'expected a label');
  assert.equal(label.getAttribute('for'), 'downloadsJanitorPath');
  const input = doc.getElementById('downloadsJanitorPath');
  assert.equal(input.getAttribute('aria-describedby'), 'downloadsJanitorDisclosure');
  const error = doc.getElementById('downloadsJanitorError');
  assert.equal(error.getAttribute('aria-live'), 'polite');
});

test('confirming with an empty folder reports an error and calls no endpoint', async () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  let called = false;
  globalThis.fetch = async () => {
    called = true;
    return { ok: true, json: async () => ({}) };
  };
  doc.getElementById('downloadsJanitorPath').value = '   ';
  doc.getElementById('downloadsJanitorConfirm').click();
  await new Promise(r => setTimeout(r, 0));
  assert.equal(called, false, 'an empty selection must not reach the server');
  assert.equal(doc.getElementById('downloadsJanitorError').hidden, false);
});

test('confirming posts the confirmed path and renders the returned status', async () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  let sent = null;
  globalThis.fetch = async (url, opts) => {
    sent = { url, body: JSON.parse(opts.body) };
    return {
      ok: true,
      json: async () => ({
        status: {
          applies: true,
          settings: {
            root_path: '/tmp/Inbox',
            directory_reference_id: 'ref-1',
            filing_root_name: 'Filed'
          },
          readiness: {
            state: 'needs_attention',
            checks: [
              { component: 'directory_access', status: 'ok' },
              {
                component: 'watcher',
                status: 'pending',
                message: 'Folder watching is not running yet.'
              }
            ]
          }
        }
      })
    };
  };
  doc.getElementById('downloadsJanitorPath').value = '/tmp/Inbox';
  doc.getElementById('downloadsJanitorConfirm').click();
  await new Promise(r => setTimeout(r, 0));

  assert.match(sent.url, /\/api\/workspaces\/ws-1\/file-janitor\/setup$/);
  assert.equal(sent.body.path, '/tmp/Inbox');
  const body = text(doc);
  assert.match(body, /\/tmp\/Inbox/);
  assert.match(body, /Needs attention/);
});

test('a setup failure shows the server message and re-enables the button', async () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  globalThis.fetch = async () => ({
    ok: false,
    json: async () => ({
      error: {
        code: 'permission_denied',
        message: 'Ori does not have permission to read that folder.'
      }
    })
  });
  doc.getElementById('downloadsJanitorPath').value = '/tmp/blocked';
  doc.getElementById('downloadsJanitorConfirm').click();
  await new Promise(r => setTimeout(r, 0));

  const error = doc.getElementById('downloadsJanitorError');
  assert.equal(error.hidden, false);
  assert.match(error.textContent, /permission/i);
  assert.equal(doc.getElementById('downloadsJanitorConfirm').disabled, false);
});

test('readiness rows carry a non-color mark and a status word per component', () => {
  const doc = setup();
  openConsole({
    applies: true,
    settings: { root_path: '/tmp/Inbox', directory_reference_id: 'ref-1' },
    readiness: {
      state: 'needs_attention',
      checks: [
        {
          component: 'directory_access',
          status: 'failed',
          message: 'The folder is no longer there.',
          repair: 'relink_folder'
        },
        { component: 'destination', status: 'ok' },
        { component: 'scheduler', status: 'pending', message: 'Not scheduled yet.' }
      ]
    }
  });
  const host = surface(doc);
  const marks = host.all(n => n.className === 'dj-row-mark');
  assert.equal(marks.length, 3);
  assert.deepEqual(
    marks.map(m => m.textContent),
    ['!', '✓', '–']
  );
  const body = text(doc);
  assert.match(body, /Folder access/);
  assert.match(body, /Needs attention/);
  assert.match(body, /Not running yet/);
  // A failing folder check offers the repair path.
  assert.match(body, /Choose the folder again/);
});

// ------------------------------------------------------------------- review

const CATEGORIES = [
  { id: 'documents', label: 'Documents' },
  { id: 'images', label: 'Images' },
  { id: 'archives', label: 'Archives' },
  { id: 'other', label: 'Other' }
];

function batchFixture(overrides = {}) {
  return Object.assign(
    {
      id: 'batch-1',
      source: 'manual',
      started_at: new Date().toISOString(),
      completed_at: new Date().toISOString(),
      state: 'pending',
      summary: { proposed: 2, needs_review: 1, skipped: 1, ineligible: 3, total: 3 }
    },
    overrides
  );
}

function candidatesFixture() {
  return [
    {
      id: 'c1',
      name: 'invoice-2026-07.pdf',
      extension: '.pdf',
      size: 204800,
      modified_at: new Date(Date.now() - 3600_000).toISOString(),
      category: 'documents',
      destination: 'Filed/Documents',
      reason: 'pdf file',
      confidence: 'high',
      state: 'pending'
    },
    {
      id: 'c2',
      name: 'payload.bin',
      extension: '.bin',
      size: 1024,
      modified_at: new Date().toISOString(),
      category: 'other',
      destination: 'Filed/Other',
      reason: '.bin files can hold anything',
      confidence: 'low',
      needs_review: true,
      state: 'pending'
    },
    {
      id: 'c3',
      name: 'ad.png',
      extension: '.png',
      size: 500,
      modified_at: new Date().toISOString(),
      category: 'images',
      destination: 'Filed/Images',
      reason: 'png file',
      confidence: 'high',
      state: 'skipped'
    }
  ];
}

// renderReview paints the configured card with a batch already loaded.
function renderReview(doc, batch = batchFixture(), candidates = candidatesFixture()) {
  panel._setBatch(batch, candidates, CATEGORIES);
  openConsole({
    applies: true,
    settings: {
      root_path: '/tmp/Inbox',
      directory_reference_id: 'ref-1',
      filing_root_name: 'Filed'
    },
    readiness: {
      state: 'needs_attention',
      checks: [{ component: 'directory_access', status: 'ok' }]
    }
  });
  return surface(doc);
}

function rowsIn(host) {
  return host.all(n => n.className && String(n.className).includes('dj-row-item'));
}

test('review table renders a row per candidate with the facts needed to judge it', () => {
  const doc = setup();
  const host = renderReview(doc);
  const rows = rowsIn(host);
  assert.equal(rows.length, 3);

  const body = text(doc);
  assert.match(body, /invoice-2026-07\.pdf/);
  assert.match(body, /Filed\/Documents/); // resolved destination
  assert.match(body, /pdf file/); // reason
  assert.match(body, /high confidence/);
  assert.match(body, /200 KB/); // human-readable size
});

test('the batch summary states counts, scan source, and when it ran', () => {
  const doc = setup();
  renderReview(doc);
  const body = text(doc);
  assert.match(body, /2 proposed/);
  assert.match(body, /1 needing review/);
  assert.match(body, /1 skipped/);
  assert.match(body, /3 not eligible/);
  assert.match(body, /Scan now/); // the source label
});

test('every row starts unselected', () => {
  const doc = setup();
  const host = renderReview(doc);
  const boxes = host.all(n => n.className === 'dj-select');
  assert.equal(boxes.length, 3);
  boxes.forEach(box => assert.equal(box.checked, false));
  assert.deepEqual(panel._selected(), []);
  assert.match(text(doc), /No files selected/);
});

test('a low-confidence candidate is flagged for review in text, not colour alone', () => {
  const doc = setup();
  const host = renderReview(doc);
  const flags = host.all(n => n.className === 'dj-flag');
  assert.equal(flags.length, 1);
  assert.match(flags[0].textContent, /Needs review/);
});

test('changing a category records the decision immediately', async () => {
  const doc = setup();
  const host = renderReview(doc);
  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') sent = { url, body: JSON.parse(opts.body) };
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  const select = host.all(n => n.className === 'dj-category')[0];
  select.value = 'archives';
  select.dispatch('change');
  await new Promise(r => setTimeout(r, 0));

  assert.match(sent.url, /\/file-janitor\/decisions$/);
  assert.equal(sent.body.decisions[0].decision, 'move');
  assert.equal(sent.body.decisions[0].category, 'archives');
  // IDs only: the browser never names a path.
  assert.equal(sent.body.decisions[0].candidate_id, 'c1');
  assert.ok(!JSON.stringify(sent.body).includes('/tmp/Inbox'));
});

test('Skip records a skip decision for that candidate only', async () => {
  const doc = setup();
  const host = renderReview(doc);
  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') sent = JSON.parse(opts.body);
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  const skip = host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Skip')[0];
  assert.ok(skip, 'expected a Skip control on a pending row');
  skip.click();
  await new Promise(r => setTimeout(r, 0));

  assert.equal(sent.decisions.length, 1);
  assert.equal(sent.decisions[0].decision, 'skip');
  assert.equal(sent.decisions[0].candidate_id, 'c1');
});

test('an already-skipped row offers no category control and no Skip', () => {
  const doc = setup();
  const host = renderReview(doc);
  const skipped = rowsIn(host).find(row => String(row.className).includes('dj-row-state-skipped'));
  assert.ok(skipped, 'expected the skipped row');
  assert.equal(skipped.all(n => n.className === 'dj-category').length, 0);
  assert.equal(skipped.all(n => n.tagName === 'BUTTON' && n.textContent === 'Skip').length, 0);
  const box = skipped.all(n => n.className === 'dj-select')[0];
  assert.equal(box.disabled, true, 'a resolved row cannot be selected for action');
});

// Filtering is the server's job now: it owns the vocabulary, and it returns
// the page plus the whole-batch counts. The button's job is to ASK for the
// right thing, which is what this pins — a client-side filter would make the
// row count disagree with the counts beside the filters as soon as a batch
// spanned more than one page.
test('a filter asks the server for that filter, starting at the first page', async () => {
  const doc = setup();
  renderReview(doc);

  const asked = [];
  globalThis.fetch = async url => {
    const target = String(url);
    if (target.includes('/batches/latest')) {
      asked.push(target);
      return {
        ok: true,
        json: async () => ({
          batch: batchFixture(),
          candidates: [candidatesFixture()[1]],
          total: 3,
          filtered_total: 1,
          counts: { all: 3, needs_review: 1, pending: 2, skipped: 1 }
        })
      };
    }
    return { ok: true, json: async () => ({ status: statusFixture(), categories: CATEGORIES }) };
  };
  // Let the status re-read that open() fires land against this stub, then
  // start counting from a settled surface.
  await settle();
  asked.length = 0;

  const needsReview = surface(doc).all(
    n => n.tagName === 'BUTTON' && n.textContent.startsWith('Needs review')
  )[0];
  needsReview.click();
  await settle();

  assert.equal(asked.length, 1, 'the filter must be applied server-side');
  assert.match(asked[0], /filter=needs_review/);
  assert.match(asked[0], /offset=0/, 'a new filter starts at the first page');
  assert.equal(rowsIn(surface(doc)).length, 1);
});

test('filter labels carry whole-batch counts, not the page count', async () => {
  const doc = setup();
  renderReview(doc);

  globalThis.fetch = async url =>
    String(url).includes('/batches/latest')
      ? {
          ok: true,
          json: async () => ({
            batch: batchFixture(),
            candidates: candidatesFixture(),
            total: 500,
            filtered_total: 500,
            counts: { all: 500, needs_review: 12, pending: 480, skipped: 8 }
          })
        }
      : { ok: true, json: async () => ({ status: statusFixture(), categories: CATEGORIES }) };

  await settle();
  await panel._reloadBatch();
  const body = surface(doc).textContent;
  assert.match(body, /500/, 'the All filter must report the whole batch');
  assert.match(body, /12/, 'Needs review must report the whole batch');
});

test('a filter matching nothing says so rather than showing an empty table', () => {
  const doc = setup();
  renderReview(doc, batchFixture(), []);
  assert.match(text(doc), /No files match this filter/);
});

test('the table and its controls carry screen-reader labels', () => {
  const doc = setup();
  const host = renderReview(doc);
  const table = host.all(n => n.tagName === 'TABLE')[0];
  assert.ok(table.getAttribute('aria-label'), 'the table needs a label');

  const headers = host.all(n => n.tagName === 'TH');
  assert.equal(headers.length, 9);
  headers.forEach(header => assert.equal(header.getAttribute('scope'), 'col'));

  const box = host.all(n => n.className === 'dj-select')[0];
  assert.match(box.getAttribute('aria-label'), /Select invoice-2026-07\.pdf/);
  const select = host.all(n => n.className === 'dj-category')[0];
  assert.match(select.getAttribute('aria-label'), /Category for invoice-2026-07\.pdf/);

  const filterButton = host.all(
    n => n.tagName === 'BUTTON' && String(n.className).startsWith('dj-filter')
  )[0];
  assert.ok(filterButton.getAttribute('aria-pressed'), 'filters expose pressed state');
});

test('an empty workspace explains what will appear rather than showing a bare table', () => {
  const doc = setup();
  renderReview(doc, null, []);
  const body = text(doc);
  assert.match(body, /Nothing to review/);
  assert.match(body, /Nothing is moved without your approval/);
  assert.match(cardText(doc), /No files waiting for review/);
});

test('the status line reports the pending count and the last scan', () => {
  const doc = setup();
  renderReview(doc);
  assert.match(cardText(doc), /2 files waiting for review/);
  assert.match(cardText(doc), /last scan/);
});

test('Scan now reports honestly when a scan finds nothing new', async () => {
  const doc = setup();
  renderReview(doc);
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') {
      return { ok: true, json: async () => ({ success: true, created: false }) };
    }
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorScan').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  const error = doc.getElementById('downloadsJanitorError');
  assert.equal(error.hidden, false);
  assert.match(error.textContent, /Nothing new to review/);
  // The button is usable again.
  assert.equal(doc.getElementById('downloadsJanitorScan').disabled, false);
});

test('a rejected decision is not left on screen as though it were saved', async () => {
  const doc = setup();
  const host = renderReview(doc);
  let reloaded = false;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') {
      return {
        ok: false,
        json: async () => ({ error: { message: 'That category is not allowed.' } })
      };
    }
    reloaded = true;
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  const select = host.all(n => n.className === 'dj-category')[0];
  select.value = 'archives';
  select.dispatch('change');
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.match(doc.getElementById('downloadsJanitorError').textContent, /not allowed/);
  assert.ok(reloaded, 'the panel must repaint from the server after a rejected change');
});

// -------------------------------------------------- confirmation and results

const PREVIEW = {
  batch_id: 'batch-1',
  token: 'tok-abc',
  move_count: 2,
  items: [
    {
      candidate_id: 'c1',
      name: 'invoice-2026-07.pdf',
      destination: 'Filed/Documents/invoice-2026-07.pdf',
      renamed: false
    },
    {
      candidate_id: 'c2',
      name: 'payload.bin',
      destination: 'Filed/Other/payload (2).bin',
      renamed: true
    }
  ]
};

test('the approve control states the count and is disabled until something is selected', () => {
  const doc = setup();
  renderReview(doc);
  const approve = doc.getElementById('downloadsJanitorApprove');
  assert.equal(approve.disabled, true);
  assert.match(approve.textContent, /Approve selected/);

  panel._select('c1');
  assert.equal(approve.disabled, false);
  assert.match(approve.textContent, /Approve 1 move$/);

  panel._select('c2');
  assert.match(approve.textContent, /Approve 2 moves$/);
});

test('approving previews the plan without moving anything', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');

  const calls = [];
  globalThis.fetch = async url => {
    calls.push(url);
    if (String(url).endsWith('/preview')) {
      return { ok: true, json: async () => ({ preview: PREVIEW }) };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  assert.ok(
    calls.some(url => String(url).endsWith('/preview')),
    'the preview endpoint should be called'
  );
  assert.ok(
    !calls.some(url => String(url).endsWith('/apply')),
    'previewing must not apply anything'
  );

  const body = text(doc);
  assert.match(body, /Confirm these moves/);
  assert.match(body, /Ori will move 2 files/);
  assert.match(body, /Nothing is deleted/);
});

test('the confirmation shows each resolved destination and flags forced renames', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? { ok: true, json: async () => ({ preview: PREVIEW }) }
      : {
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
        };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const body = text(doc);
  assert.match(body, /Filed\/Documents\/invoice-2026-07\.pdf/);
  assert.match(body, /Filed\/Other\/payload \(2\)\.bin/);
  // The user is told a name was taken rather than discovering it afterwards.
  assert.match(body, /renamed — a file with that name is already there/);
});

test('confirming sends the approval token and reports per-file results', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  let applyBody = null;
  globalThis.fetch = async (url, opts) => {
    if (String(url).endsWith('/preview')) {
      return { ok: true, json: async () => ({ preview: PREVIEW }) };
    }
    if (String(url).endsWith('/apply')) {
      applyBody = JSON.parse(opts.body);
      return {
        ok: true,
        json: async () => ({
          result: {
            applied: 1,
            failed: 1,
            stale: 0,
            outcomes: [
              {
                candidate_id: 'c1',
                name: 'invoice-2026-07.pdf',
                result: 'applied',
                destination: 'Filed/Documents/invoice-2026-07.pdf'
              },
              {
                candidate_id: 'c2',
                name: 'payload.bin',
                result: 'failed',
                message: 'Ori could not move this file.'
              }
            ]
          }
        })
      };
    }
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  doc.getElementById('downloadsJanitorConfirmApply').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.equal(applyBody.approval_token, 'tok-abc');
  assert.equal(applyBody.batch_id, 'batch-1');
  assert.equal(applyBody.decisions[0].candidate_id, 'c1');
  // Still IDs only — the browser never names a path.
  assert.ok(!JSON.stringify(applyBody).includes('/tmp/Inbox'));

  // A mixed result is stated as mixed, never as success.
  const body = text(doc);
  assert.match(body, /1 file filed/);
  assert.match(body, /1 could not be moved/);
  assert.match(body, /Ori could not move this file/);
  assert.doesNotMatch(body, /All files? moved/);
});

test('a stale result explains itself and offers a rescan', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  globalThis.fetch = async url => {
    if (String(url).endsWith('/preview'))
      return { ok: true, json: async () => ({ preview: PREVIEW }) };
    if (String(url).endsWith('/apply')) {
      return {
        ok: true,
        json: async () => ({
          result: {
            applied: 0,
            failed: 0,
            stale: 1,
            outcomes: [
              {
                candidate_id: 'c1',
                name: 'invoice-2026-07.pdf',
                result: 'stale',
                message: 'This file changed after you approved it, so Ori left it alone.'
              }
            ]
          }
        })
      };
    }
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  doc.getElementById('downloadsJanitorConfirmApply').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  const body = text(doc);
  assert.match(body, /changed since you approved/);
  assert.match(body, /Ori left it alone/);
  assert.match(body, /Scan again/);
});

test('cancelling abandons the approval and moves nothing', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(String(url));
    if (String(url).endsWith('/preview'))
      return { ok: true, json: async () => ({ preview: PREVIEW }) };
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const host = surface(doc);
  const cancel = host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Cancel')[0];
  assert.ok(cancel, 'expected a Cancel control');
  cancel.click();
  await new Promise(r => setTimeout(r, 0));

  assert.ok(!calls.some(url => url.endsWith('/apply')), 'cancelling must not apply anything');
  assert.doesNotMatch(text(doc), /Confirm these moves/);
});

test('a rejected approval is reported and nothing is left looking approved', async () => {
  const doc = setup();
  renderReview(doc);
  // Let the status re-read that open() fires land before building a selection,
  // so the batch under the selection is the one that stays on screen.
  await settle();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.renderBatch();
  panel._select('c1');
  globalThis.fetch = async url => {
    if (String(url).endsWith('/preview')) {
      return {
        ok: false,
        json: async () => ({
          error: {
            code: 'candidate_changed',
            message: 'report.pdf changed since it was proposed — rescan to review it again.'
          }
        })
      };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  assert.match(doc.getElementById('downloadsJanitorError').textContent, /rescan/);
  assert.doesNotMatch(text(doc), /Confirm these moves/);
  // The approve control is usable again.
  assert.equal(doc.getElementById('downloadsJanitorApprove').disabled, false);
});

// Applying every file in a batch empties it. The report of what happened must
// still be on screen afterwards — that moment is the whole point of the flow.
test('results survive the batch they emptied', async () => {
  const doc = setup();
  renderReview(doc, batchFixture(), [candidatesFixture()[0]]);
  panel._select('c1');
  globalThis.fetch = async url => {
    if (String(url).endsWith('/preview')) {
      return {
        ok: true,
        json: async () => ({ preview: { ...PREVIEW, move_count: 1, items: [PREVIEW.items[0]] } })
      };
    }
    if (String(url).endsWith('/apply')) {
      return {
        ok: true,
        json: async () => ({
          result: {
            applied: 1,
            failed: 0,
            stale: 0,
            outcomes: [
              {
                candidate_id: 'c1',
                name: 'invoice-2026-07.pdf',
                result: 'applied',
                destination: 'Filed/Documents/invoice-2026-07.pdf'
              }
            ]
          }
        })
      };
    }
    // The batch is now resolved: nothing pending remains.
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  doc.getElementById('downloadsJanitorConfirmApply').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  const body = text(doc);
  assert.match(body, /1 file filed/, 'the outcome must remain visible after the batch empties');
  assert.match(body, /Filed\/Documents\/invoice-2026-07\.pdf/);
  // And the now-empty batch still explains itself.
  assert.match(body, /Nothing to review/);
});

// ------------------------------------------------------------------- Trash

function trashToggleFor(doc, name) {
  const host = surface(doc);
  return host.all(
    n => n.tagName === 'BUTTON' && String(n.getAttribute('aria-label') || '').includes(name)
  )[0];
}

test('Trash is a per-file choice, never part of the move selection', () => {
  const doc = setup();
  renderReview(doc);

  const toggle = trashToggleFor(doc, 'invoice-2026-07.pdf');
  assert.ok(toggle, 'expected a Trash control on a pending row');
  assert.match(toggle.getAttribute('aria-label'), /Mark .* for Trash/);
  assert.equal(toggle.getAttribute('aria-pressed'), 'false');

  toggle.click();
  const marked = trashToggleFor(doc, 'invoice-2026-07.pdf');
  assert.equal(marked.getAttribute('aria-pressed'), 'true');
  // Marking for Trash does not put the file in the move selection.
  assert.deepEqual(panel._selected(), []);
  assert.match(text(doc), /1 file marked for Trash/);
});

test('a file marked for Trash cannot also be selected for a move', () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  trashToggleFor(doc, 'invoice-2026-07.pdf').click();

  // The move selection released it, and its checkbox is no longer selectable.
  assert.deepEqual(panel._selected(), []);
  const host = surface(doc);
  const box = host.all(n => n.className === 'dj-select')[0];
  assert.equal(box.disabled, true, 'a file bound for Trash is not a move candidate');
});

test('the approve control counts moves and removals separately', () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  trashToggleFor(doc, 'payload.bin').click();

  const approve = doc.getElementById('downloadsJanitorApprove');
  assert.match(approve.textContent, /1 move/);
  assert.match(approve.textContent, /1 to Trash/);
});

test('a batch containing a removal requires a separate acknowledgement of the exact count', async () => {
  const doc = setup();
  renderReview(doc);
  trashToggleFor(doc, 'payload.bin').click();
  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? {
          ok: true,
          json: async () => ({
            preview: {
              batch_id: 'batch-1',
              token: 'tok-abc',
              move_count: 0,
              trash_count: 1,
              items: [{ candidate_id: 'c2', name: 'payload.bin', operation: 'trash' }]
            }
          })
        }
      : {
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
        };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const body = text(doc);
  // The consequence is stated plainly, including that it is recoverable.
  assert.match(body, /system Trash/);
  assert.match(body, /restore them/);
  assert.match(body, /Nothing is deleted permanently/);
  assert.match(body, /Trash \(restorable\)/);

  // The confirm control is blocked until the removal is acknowledged by count.
  const confirm = doc.getElementById('downloadsJanitorConfirmApply');
  assert.equal(confirm.disabled, true, 'a removal must not be one click away');
  const ack = doc.getElementById('downloadsJanitorTrashAck');
  assert.ok(ack, 'expected a separate acknowledgement for the removal');
  assert.match(body, /Yes, move 1 file to the Trash/);

  ack.checked = true;
  ack.dispatch('change');
  assert.equal(confirm.disabled, false);
});

test('a move-only batch needs no extra acknowledgement', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? { ok: true, json: async () => ({ preview: { ...PREVIEW, trash_count: 0 } }) }
      : {
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
        };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  assert.equal(doc.getElementById('downloadsJanitorTrashAck'), null);
  assert.equal(doc.getElementById('downloadsJanitorConfirmApply').disabled, false);
  assert.match(text(doc), /Nothing is deleted/);
});

test('approving sends each file under the operation it was marked with', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  trashToggleFor(doc, 'payload.bin').click();
  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (String(url).endsWith('/preview')) {
      sent = JSON.parse(opts.body);
      return {
        ok: true,
        json: async () => ({ preview: { ...PREVIEW, trash_count: 1, move_count: 1 } })
      };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const byId = Object.fromEntries(sent.decisions.map(d => [d.candidate_id, d]));
  assert.equal(byId.c1.operation, 'move');
  assert.equal(byId.c2.operation, 'trash');
  assert.equal(byId.c2.category, '', 'a removal has no category');
});

// A removal mark belongs to the batch it was made in. Carrying one into a
// different set of files is how the wrong file gets thrown away.
test('Trash marks never survive a repaint into a different batch', () => {
  const doc = setup();
  renderReview(doc);
  trashToggleFor(doc, 'payload.bin').click();
  assert.match(text(doc), /1 file marked for Trash/);

  // A fresh batch arrives.
  renderReview(doc, batchFixture(), candidatesFixture());
  assert.doesNotMatch(text(doc), /marked for Trash/);
  const approve = doc.getElementById('downloadsJanitorApprove');
  assert.equal(approve.disabled, true, 'nothing should be pending from the previous batch');
});

// A batch of removals alone is still approvable — guarding on the move
// selection would make Trash unreachable.
test('a Trash-only batch can be approved', async () => {
  const doc = setup();
  renderReview(doc);
  trashToggleFor(doc, 'payload.bin').click();
  let previewed = false;
  globalThis.fetch = async url => {
    if (String(url).endsWith('/preview')) {
      previewed = true;
      return {
        ok: true,
        json: async () => ({
          preview: {
            batch_id: 'batch-1',
            token: 'tok',
            move_count: 0,
            trash_count: 1,
            items: [{ candidate_id: 'c2', name: 'payload.bin', operation: 'trash' }]
          }
        })
      };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  assert.ok(previewed, 'a Trash-only batch must reach the preview');
  assert.match(text(doc), /Confirm these moves/);
});

// ----------------------------------------------------------------- history

const HISTORY = [
  {
    id: 'a1',
    operation: 'move',
    source_name: 'report.pdf',
    destination_relative: 'Filed/Documents/report.pdf',
    result: 'applied',
    undo: 'available'
  },
  {
    id: 'a2',
    operation: 'trash',
    source_name: 'ad.png',
    result: 'applied',
    undo: 'available'
  },
  {
    id: 'a3',
    operation: 'move',
    source_name: 'blocked.pdf',
    result: 'failed',
    error_summary: 'Ori could not move this file.',
    undo: 'unavailable'
  },
  {
    id: 'a4',
    operation: 'move',
    source_name: 'restored.pdf',
    destination_relative: 'Filed/Documents/restored.pdf',
    result: 'applied',
    undo: 'undone'
  }
];

test('history lists what happened and names the reversal each entry offers', () => {
  const doc = setup();
  renderReview(doc);
  panel._selectTab('history');
  panel._setHistory(HISTORY);

  const body = text(doc);
  assert.match(body, /History/);
  assert.match(body, /report\.pdf\s+— filed to Filed\/Documents\/report\.pdf/);
  assert.match(body, /ad\.png\s+— moved to Trash/);
  // The control says what it will actually do, not a generic "undo".
  assert.match(body, /Undo move/);
  assert.match(body, /Restore from Trash/);
});

test('history explains entries that cannot be undone instead of hiding them', () => {
  const doc = setup();
  renderReview(doc);
  panel._selectTab('history');
  panel._setHistory(HISTORY);

  const body = text(doc);
  // A failed action is listed with its reason and offers no undo.
  assert.match(body, /blocked\.pdf\s+— not moved/);
  assert.match(body, /Ori could not move this file/);
  // An already-undone action says so.
  assert.match(body, /restored\.pdf/);
  assert.match(body, /put back/);
});

test('undoing calls the action-specific endpoint and reports the outcome', async () => {
  const doc = setup();
  renderReview(doc);
  panel._selectTab('history');
  panel._setHistory(HISTORY);

  let undoUrl = null;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST' && String(url).includes('/undo')) {
      undoUrl = String(url);
      return {
        ok: true,
        json: async () => ({
          undo: { result: 'undone', message: 'Put back in the folder.', restored_to: 'report.pdf' }
        })
      };
    }
    if (String(url).includes('/history'))
      return { ok: true, json: async () => ({ actions: HISTORY }) };
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  const host = surface(doc);
  host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Undo move')[0].click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.match(undoUrl, /\/file-janitor\/history\/a1\/undo$/);
});

test('a refused undo is reported with its reason, not as a failure of the app', async () => {
  const doc = setup();
  renderReview(doc);
  panel._selectTab('history');
  panel._setHistory(HISTORY);

  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST' && String(url).includes('/undo')) {
      return {
        ok: true,
        json: async () => ({
          undo: {
            result: 'failed',
            message:
              'Something else is already using the original name, so Ori did not overwrite it.'
          }
        })
      };
    }
    if (String(url).includes('/history'))
      return { ok: true, json: async () => ({ actions: HISTORY }) };
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  const host = surface(doc);
  host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Undo move')[0].click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.match(
    doc.getElementById('downloadsJanitorHistoryStatus').textContent,
    /already using the original name/
  );
});

test('history filters are exposed as pressed-state controls', () => {
  const doc = setup();
  renderReview(doc);
  panel._selectTab('history');
  panel._setHistory(HISTORY);

  const host = surface(doc);
  const filters = host.all(
    n => n.tagName === 'BUTTON' && ['Filed', 'Trashed', 'Can undo'].includes(n.textContent)
  );
  assert.equal(filters.length, 3);
  filters.forEach(control => assert.ok(control.getAttribute('aria-pressed')));
});

test('an empty history explains what will appear there', () => {
  const doc = setup();
  renderReview(doc);
  panel._selectTab('history');
  panel._setHistory([]);
  assert.match(text(doc), /Applied moves and Trash actions are listed here/);
});

// ------------------------------------------------------- workspace status

function statusFixture(overrides = {}) {
  return Object.assign(
    {
      applies: true,
      settings: {
        root_path: '/tmp/Inbox',
        directory_reference_id: 'ref-1',
        filing_root_name: 'Filed',
        daily_scan_local_time: '09:00'
      },
      readiness: { state: 'ready', checks: [{ component: 'directory_access', status: 'ok' }] }
    },
    overrides
  );
}

function badgeText(doc) {
  const node = doc.getElementById('downloadsJanitorActivity');
  return node ? node.textContent : '';
}

test('a configured, idle workspace reports that it is watching', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  assert.equal(badgeText(doc), 'Watching');
  assert.match(cardText(doc), /No files waiting for review/);
  assert.match(cardText(doc), /next catch-up at 09:00/);
});

test('files waiting outrank watching in the status', () => {
  const doc = setup();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  openConsole(statusFixture());
  assert.equal(badgeText(doc), 'Review ready');
  assert.match(cardText(doc), /2 files waiting for review/);
});

test('a problem outranks everything else', () => {
  const doc = setup();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  openConsole(
    statusFixture({
      readiness: {
        state: 'needs_attention',
        checks: [
          { component: 'directory_access', status: 'failed', message: 'The folder is gone.' }
        ]
      }
    })
  );
  assert.equal(badgeText(doc), 'Needs attention');
});

test('a paused workspace says so, and says what pausing does not stop', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture({ settings: { ...statusFixture().settings, paused: true } }));

  assert.equal(badgeText(doc), 'Paused');
  assert.match(cardText(doc), /automatic scanning paused/);
  const control = doc.getElementById('downloadsJanitorPause');
  assert.match(control.textContent, /Resume watching/);
  // Scanning on demand is still available while paused.
  assert.equal(doc.getElementById('downloadsJanitorScan').disabled, false);
});

test('the pause control explains what is kept', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  const control = doc.getElementById('downloadsJanitorPause');
  assert.match(control.textContent, /Pause watching/);
  assert.match(control.getAttribute('title'), /settings, pending review, and history are kept/);
});

test('pausing posts the new state and repaints from the server answer', async () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());

  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (String(url).endsWith('/pause')) {
      sent = JSON.parse(opts.body);
      return {
        ok: true,
        json: async () => ({
          status: statusFixture({ settings: { ...statusFixture().settings, paused: true } })
        })
      };
    }
    if (String(url).includes('/history')) return { ok: true, json: async () => ({ actions: [] }) };
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorPause').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.equal(sent.paused, true);
  assert.equal(badgeText(doc), 'Paused');
});

test('the header says Scanning while a scan the user started is running', async () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());

  let observedDuringScan = '';
  globalThis.fetch = async url => {
    if (String(url).endsWith('/scan')) {
      observedDuringScan = badgeText(doc);
      return { ok: true, json: async () => ({ success: true, created: true }) };
    }
    if (String(url).includes('/history')) return { ok: true, json: async () => ({ actions: [] }) };
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  doc.getElementById('downloadsJanitorScan').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.equal(observedDuringScan, 'Scanning…');
  // And it stops saying so once the scan finishes.
  assert.notEqual(badgeText(doc), 'Scanning…');
});

// -------------------------------------------------------- privacy & settings

function privacyStatus(privacy) {
  return statusFixture({ privacy });
}

test('the card always states what Ori reads, without opening settings', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(
    privacyStatus({
      mode: 'metadata_only',
      headline: 'Ori reads file names, types, sizes, and dates only.',
      detail: 'No file contents are opened or read.',
      leaves_device: false
    })
  );
  const body = cardText(doc);
  assert.match(body, /names, types, sizes, and dates only/);
  assert.match(body, /No file contents are opened or read/);
});

test('a cloud provider is named, and the pending confirmation is the action', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(
    privacyStatus({
      mode: 'cloud_model',
      headline: 'Ori reads file names, types, sizes, and dates, and may read a short extract.',
      detail:
        'Extracts are sent to SomeCloud, which is outside this device. Nothing has been sent yet.',
      provider: 'SomeCloud',
      leaves_device: true,
      consent_required: true
    })
  );
  const body = cardText(doc);
  assert.match(body, /SomeCloud/);
  assert.match(body, /outside this device/);
  assert.match(body, /Nothing has been sent yet/);

  const host = doc.getElementById('downloadsJanitorMount');
  const confirm = host.all(
    n => n.tagName === 'BUTTON' && /Confirm SomeCloud/.test(n.textContent)
  )[0];
  assert.ok(confirm, 'an unconfirmed provider must offer the confirmation');
});

test('settings spell out the consequence of each content option', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  const body = text(doc);
  assert.match(body, /Names and file details only/);
  assert.match(body, /Ori never opens your files\. This is the default/);
  assert.match(body, /Nothing leaves this device/);
  assert.match(body, /asks you to confirm before anything is sent/);
});

test('changing a setting patches only that field', async () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (String(url).endsWith('/settings')) {
      sent = { method: opts.method, body: JSON.parse(opts.body) };
      return { ok: true, json: async () => ({ status: statusFixture() }) };
    }
    return { ok: true, json: async () => ({ batch: null, candidates: [] }) };
  };

  const input = doc.getElementById('downloadsJanitorDailyTime');
  input.value = '07:30';
  input.dispatch('change');
  await new Promise(r => setTimeout(r, 0));

  assert.equal(sent.method, 'PATCH');
  assert.deepEqual(Object.keys(sent.body), ['daily_scan_local_time']);
  assert.equal(sent.body.daily_scan_local_time, '07:30');
});

test('a test scan reports what it would do and says nothing changed', async () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  globalThis.fetch = async url =>
    String(url).endsWith('/test-scan')
      ? { ok: true, json: async () => ({ report: { eligible_count: 3, ineligible_count: 2 } }) }
      : { ok: true, json: async () => ({ batch: null, candidates: [] }) };

  const host = surface(doc);
  host.all(n => n.tagName === 'BUTTON' && n.textContent === 'Run a test scan')[0].click();
  await new Promise(r => setTimeout(r, 0));

  const status = doc.getElementById('downloadsJanitorSettingsStatus').textContent;
  assert.match(status, /3 files would be proposed/);
  assert.match(status, /2 skipped/);
  assert.match(status, /Nothing was changed/);
});

// Disconnecting a folder asks once, and says exactly what is and is not lost.
test('stopping use of a folder confirms first and explains what is kept', async () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  let called = false;
  globalThis.fetch = async url => {
    if (String(url).endsWith('/revoke')) called = true;
    return { ok: true, json: async () => ({ status: statusFixture() }) };
  };

  const host = surface(doc);
  const stop = host.all(
    n => n.tagName === 'BUTTON' && /Stop using this folder/.test(n.textContent)
  )[0];
  assert.ok(stop, 'expected the disconnect control');

  stop.click();
  await new Promise(r => setTimeout(r, 0));
  assert.equal(called, false, 'the first press must only warn');

  const warning = doc.getElementById('downloadsJanitorSettingsStatus').textContent;
  assert.match(warning, /Your files stay exactly where they are/);
  assert.match(warning, /history is kept/);
  assert.match(warning, /again to confirm/);

  stop.click();
  await new Promise(r => setTimeout(r, 0));
  assert.equal(called, true, 'the second press confirms');
});

test('the Settings tab reports whether it is the selected tab', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());

  const tabFor = name =>
    surface(doc).all(n => n.getAttribute('data-fj-tab') === name)[0];

  assert.equal(tabFor('settings').getAttribute('role'), 'tab');
  assert.equal(tabFor('settings').getAttribute('aria-selected'), 'false');
  assert.equal(tabFor('review').getAttribute('aria-selected'), 'true');

  tabFor('settings').click();
  assert.equal(tabFor('settings').getAttribute('aria-selected'), 'true');
  assert.equal(tabFor('review').getAttribute('aria-selected'), 'false');
  assert.ok(doc.getElementById('downloadsJanitorSettingsHost'), 'the settings form is on screen');
});

// --- Accessibility ------------------------------------------------------
//
// These cover the parts of the review surface a keyboard or screen-reader user
// depends on, and that no visual check would catch.

test('a filename cannot smuggle control or bidi characters into the page', async () => {
  const doc = setup();
  const hostile = candidatesFixture();
  // A right-to-left override disguises the real extension: this renders as
  // "invoice exe.pdf" in any renderer that honours the character.
  hostile[0].name = 'invoice\u202Efdp.exe';
  hostile[0].display_name = undefined;
  // And a newline would forge a second line of output.
  hostile[1].name = 'payload\n(deleted 400 files).bin';
  hostile[1].display_name = undefined;
  renderReview(doc, batchFixture(), hostile);

  const body = text(doc);
  assert.ok(!body.includes('\u202E'), 'bidi overrides must never reach the DOM');
  assert.ok(!body.includes('\n(deleted'), 'a filename must not forge a second line');
  // The suspicious text itself survives, so a suspicious name still reads as one.
  assert.match(body, /invoice/);
  assert.match(body, /exe/);
});

test('the accessible name of a row control is the safe name, not the raw one', async () => {
  const doc = setup();
  const hostile = candidatesFixture();
  hostile[0].name = 'invoice\u202Efdp.exe';
  hostile[0].display_name = undefined;
  renderReview(doc, batchFixture(), hostile);

  const labels = surface(doc)
    .all(node => (node.getAttribute('aria-label') || '').startsWith('Select '))
    .map(node => node.getAttribute('aria-label'));
  assert.ok(labels.length > 0, 'expected labelled selection controls');
  labels.forEach(label => {
    assert.ok(
      !label.includes('\u202E'),
      'a screen reader must not be handed a bidi override: ' + label
    );
  });
});

test('the server display_name is preferred over the raw name', async () => {
  const doc = setup();
  const candidates = candidatesFixture();
  candidates[0].name = 'raw-on-disk-name.pdf';
  candidates[0].display_name = 'rendered-safe-name.pdf';
  renderReview(doc, batchFixture(), candidates);
  assert.match(text(doc), /rendered-safe-name\.pdf/);
});

test('focus goes to the acknowledgement, not the disabled button, on a removal', async () => {
  const doc = setup();
  renderReview(doc);
  trashToggleFor(doc, 'payload.bin').click();
  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? {
          ok: true,
          json: async () => ({
            preview: {
              batch_id: 'batch-1',
              token: 'tok-abc',
              move_count: 0,
              trash_count: 1,
              items: [{ candidate_id: 'c2', name: 'payload.bin', operation: 'trash' }]
            }
          })
        }
      : {
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
        };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  const ack = doc.getElementById('downloadsJanitorTrashAck');
  const confirm = doc.getElementById('downloadsJanitorConfirmApply');
  assert.equal(confirm.disabled, true);
  assert.equal(
    doc.activeElement,
    ack,
    'focusing the disabled confirm button would drop focus to the body'
  );
  // The button says why it is unavailable rather than being inert and silent.
  assert.equal(confirm.getAttribute('aria-describedby'), 'downloadsJanitorTrashAckText');

  // Acknowledging hands the user straight to the action it unlocks.
  ack.checked = true;
  ack.dispatch('change');
  assert.equal(confirm.disabled, false);
  assert.equal(doc.activeElement, confirm);
});

test('cancelling the confirmation returns focus to the control that opened it', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? { ok: true, json: async () => ({ preview: { ...PREVIEW, trash_count: 0 } }) }
      : {
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
        };

  const approve = doc.getElementById('downloadsJanitorApprove');
  approve.focus();
  approve.click();
  await new Promise(r => setTimeout(r, 0));

  const cancel = surface(doc).all(node => node.tagName === 'BUTTON' && node.textContent === 'Cancel')[0];
  assert.ok(cancel, 'expected a cancel control');
  cancel.click();

  assert.notEqual(doc.activeElement, doc.body, 'cancelling must not strand focus on the body');
});

// The panel is loaded as a classic `defer` script; the page sets
// window.currentWorkspaceId from a module script, which runs later. If the
// panel depends on that global existing at init() time it renders nothing at
// all — and every API call it would have made still looks correct, so this is
// invisible to server-side verification.
test('the panel finds its workspace without window.currentWorkspaceId', async () => {
  const doc = setup();
  panel._forgetWorkspace();
  delete globalThis.window.currentWorkspaceId;
  globalThis.window.location = { pathname: '/workspaces/ws-42' };

  const requested = [];
  globalThis.fetch = async url => {
    requested.push(String(url));
    return { ok: true, json: async () => ({ status: setupRequiredStatus }) };
  };

  panel.init();
  await new Promise(r => setTimeout(r, 0));

  assert.ok(
    requested.some(url => url.includes('/api/workspaces/ws-42/file-janitor')),
    'the panel must derive its workspace from the URL, not give up: ' + JSON.stringify(requested)
  );
  const host = surface(doc);
  assert.equal(host.hidden, false, 'the setup card must actually be shown');
  assert.match(text(doc), /Setup required|Use this folder/);
});

test('an explicit workspace id still wins over the URL', async () => {
  const doc = setup();
  panel._forgetWorkspace();
  delete globalThis.window.currentWorkspaceId;
  globalThis.window.location = { pathname: '/workspaces/ws-from-url' };
  const requested = [];
  globalThis.fetch = async url => {
    requested.push(String(url));
    return { ok: true, json: async () => ({ status: setupRequiredStatus }) };
  };
  panel.init('ws-explicit');
  await new Promise(r => setTimeout(r, 0));
  assert.ok(
    requested.some(url => url.includes('/ws-explicit/')),
    JSON.stringify(requested)
  );
  assert.ok(!requested.some(url => url.includes('ws-from-url')));
  assert.ok(doc);
});

// The confirm button appears as soon as the approval comes back. If the panel
// is still "busy" at that moment, pressing it does nothing at all — no request,
// no error, no feedback. A user who clicks promptly must not be ignored.
test('the confirm button works the instant it appears', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');

  const requested = [];
  globalThis.fetch = async url => {
    requested.push(String(url));
    if (String(url).endsWith('/preview')) {
      return { ok: true, json: async () => ({ preview: { ...PREVIEW, trash_count: 0 } }) };
    }
    if (String(url).endsWith('/apply')) {
      return { ok: true, json: async () => ({ result: { outcomes: [], applied: 1, failed: 0 } }) };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));

  // Press it immediately, with no intervening turns.
  doc.getElementById('downloadsJanitorConfirmApply').click();
  await new Promise(r => setTimeout(r, 0));

  assert.ok(
    requested.some(url => url.endsWith('/apply')),
    'pressing confirm must send the apply, not be swallowed: ' + JSON.stringify(requested)
  );
});

// The confirmation panel lives outside the batch container, so a batch reload
// leaves it on screen. If the reload also discards the approval behind it, the
// confirm button stays visible and enabled but does nothing at all.
test('a batch reload does not invalidate a confirmation already on screen', async () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');

  const requested = [];
  globalThis.fetch = async url => {
    requested.push(String(url));
    if (String(url).endsWith('/preview')) {
      return { ok: true, json: async () => ({ preview: { ...PREVIEW, trash_count: 0 } }) };
    }
    if (String(url).endsWith('/apply')) {
      return { ok: true, json: async () => ({ result: { outcomes: [], applied: 1, failed: 0 } }) };
    }
    return {
      ok: true,
      json: async () => ({ batch: batchFixture(), candidates: candidatesFixture() })
    };
  };

  doc.getElementById('downloadsJanitorApprove').click();
  await new Promise(r => setTimeout(r, 0));
  assert.ok(doc.getElementById('downloadsJanitorConfirmApply'), 'expected a confirmation');

  // A reload of the same batch lands while the confirmation is up — exactly
  // what an in-flight loadBatch from the preceding scan does.
  await panel._reloadBatch();

  doc.getElementById('downloadsJanitorConfirmApply').click();
  await new Promise(r => setTimeout(r, 0));
  assert.ok(
    requested.some(url => url.endsWith('/apply')),
    'the approval must survive a reload of the same batch: ' + JSON.stringify(requested)
  );
});

// ---------------------------------------------------------------------------
// Blueprint Setup Wizard migration (FR-82/83): the wizard owns setup; the panel
// keeps a compact entry into it and its operational surfaces are untouched.

function withSetupWizard(status) {
  globalThis.window.SetupWizard = {
    getStatus: () => status,
    open: () => {
      globalThis.window.__setupOpened = (globalThis.window.__setupOpened || 0) + 1;
    },
    registerStepRenderer: () => {}
  };
}

function withoutSetupWizard() {
  delete globalThis.window.SetupWizard;
}

test('a wizard-enabled workspace shows a setup entry, not a second folder chooser', () => {
  const doc = setup();
  withSetupWizard({ applicable: true, state: 'in_progress' });
  openConsole(setupRequiredStatus);

  // The retired card's controls are gone: one authoritative setup surface.
  assert.equal(doc.getElementById('downloadsJanitorPath'), null);
  assert.equal(doc.getElementById('downloadsJanitorConfirm'), null);
  assert.match(text(doc), /Continue setup/);
  assert.match(text(doc), /Setup is not finished/);
  withoutSetupWizard();
});

test('a regressed workspace is offered repair, in the wizard', () => {
  const doc = setup();
  withSetupWizard({ applicable: true, state: 'needs_attention' });
  openConsole(setupRequiredStatus);
  assert.match(text(doc), /Repair setup/);
  assert.match(text(doc), /stopped working/);
  withoutSetupWizard();
});

test('a workspace whose blueprint has no wizard keeps the original setup card', () => {
  const doc = setup();
  withoutSetupWizard();
  openConsole(setupRequiredStatus);
  // Nobody loses their way to set up because their workspace predates the
  // wizard.
  assert.ok(doc.getElementById('downloadsJanitorPath'), 'the legacy card still renders');
  assert.match(text(doc), /Use this folder/);
});

test('the directory step offers a picker and never an editable path field', () => {
  const doc = setup();
  panel._setStatus({
    ...setupRequiredStatus,
    settings: { ...setupRequiredStatus.settings, root_path: '' }
  });
  const container = doc.createElement('div');
  panel._setupSteps.directory.render(container, {
    step: { id: 'folder', kind: 'directory', adapter: 'downloads_janitor' }
  });

  assert.match(container.textContent, /Choose folder/);
  assert.match(container.textContent, /~\/Downloads/);
  // FR-52: the picker is the only way in. A text field would let a typo or a
  // paste become a grant the user did not mean to give.
  assert.equal(doc.getElementById('downloadsJanitorPath'), null);
  const label = panel._setupSteps.directory.primaryLabel({
    step: { adapter: 'downloads_janitor' }
  });
  assert.match(label, /Choose a folder to continue/);
});

test('the directory step shows the chosen folder once one is confirmed', () => {
  const doc = setup();
  panel._setStatus({
    ...setupRequiredStatus,
    settings: { ...setupRequiredStatus.settings, root_path: '/tmp/fixture-inbox' }
  });
  const container = doc.createElement('div');
  panel._setupSteps.directory.render(container, {
    step: { id: 'folder', kind: 'directory', adapter: 'downloads_janitor' }
  });
  assert.match(container.textContent, /\/tmp\/fixture-inbox/);
  assert.match(container.textContent, /Choose a different folder/);
  assert.equal(
    panel._setupSteps.directory.primaryLabel({ step: { adapter: 'downloads_janitor' } }),
    'Continue'
  );
});

test('the automation step states what will run, in the workspace’s own terms', () => {
  const doc = setup();
  panel._setStatus({
    ...setupRequiredStatus,
    settings: {
      ...setupRequiredStatus.settings,
      root_path: '/tmp/fixture-inbox',
      filing_root_name: 'Filed',
      daily_scan_local_time: '07:30'
    }
  });
  const container = doc.createElement('div');
  panel._setupSteps.automation.render(container, {
    step: { id: 'automation', kind: 'automation_review', adapter: 'downloads_janitor' }
  });

  assert.match(container.textContent, /five minutes/);
  assert.match(container.textContent, /Skips the Filed folder/);
  assert.match(container.textContent, /07:30/);
  assert.match(container.textContent, /Nothing moves until you approve it/);
  assert.equal(
    panel._setupSteps.automation.primaryLabel({ step: { adapter: 'downloads_janitor' } }),
    'Turn this on'
  );
});

test('another blueprint’s directory step is not drawn by this module', () => {
  const doc = setup();
  const container = doc.createElement('div');
  panel._setupSteps.directory.render(container, {
    step: { id: 'folder', kind: 'directory', adapter: 'calendar_ops' }
  });
  assert.equal(container.textContent, '');
  assert.equal(panel._setupSteps.directory.primaryLabel({ step: { adapter: 'calendar_ops' } }), '');
});

test('unattended watching cannot be resumed from the panel before setup approves it', () => {
  const doc = setup();
  withSetupWizard({ applicable: true, state: 'in_progress' });
  openConsole({
    applies: true,
    settings: {
      workspace_id: 'ws-1',
      root_path: '/tmp/fixture-inbox',
      directory_reference_id: 'dir-1',
      filing_root_name: 'Filed',
      daily_scan_local_time: '09:00',
      paused: true
    },
    readiness: { state: 'ready', checks: [] },
    privacy: {}
  });

  const control = doc.getElementById('downloadsJanitorPause');
  assert.ok(control, 'the control stays visible rather than disappearing');
  // FR-56: the action that needs the missing approval says where the decision
  // lives instead of quietly doing it.
  assert.equal(control.textContent, 'Approve in setup');
  assert.ok(doc.getElementById('downloadsJanitorScan'), 'scanning on demand is unaffected');
  withoutSetupWizard();
});

test('once setup is ready the panel resumes watching normally', () => {
  const doc = setup();
  withSetupWizard({ applicable: true, state: 'ready' });
  openConsole({
    applies: true,
    settings: {
      workspace_id: 'ws-1',
      root_path: '/tmp/fixture-inbox',
      directory_reference_id: 'dir-1',
      filing_root_name: 'Filed',
      daily_scan_local_time: '09:00',
      paused: true
    },
    readiness: { state: 'ready', checks: [] },
    privacy: {}
  });
  assert.equal(doc.getElementById('downloadsJanitorPause').textContent, 'Resume watching');
  withoutSetupWizard();
});

// ---------------------------------------------------------------- the console
//
// One console, reached from every entry point. These cover the shell itself:
// which face it opens on, that there is only ever one of it, how it is
// addressed, and how it gives focus back.

// withLocation gives the module a URL and a history to write to. The fake
// records what was pushed so Back can be replayed, which is the only way to
// test that Back closes the console instead of leaving the workspace.
function withLocation(search = '') {
  const entries = [{ search }];
  let index = 0;
  globalThis.window.location = {
    get search() {
      return entries[index].search;
    },
    get href() {
      return 'https://ori.test/workspaces/ws-1' + entries[index].search;
    },
    pathname: '/workspaces/ws-1'
  };
  globalThis.window.history = {
    pushState: (_state, _title, url) => {
      entries.length = index + 1;
      entries.push({ search: new URL(url).search });
      index = entries.length - 1;
    },
    replaceState: (_state, _title, url) => {
      entries[index] = { search: new URL(url).search };
    }
  };
  return {
    search: () => entries[index].search,
    back: () => {
      if (index > 0) index -= 1;
      panel._applyUrlState({ source: 'popstate' });
    }
  };
}

function clearLocation() {
  delete globalThis.window.location;
  delete globalThis.window.history;
}

test('an unconfigured install opens straight into setup, with no empty tabs', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  assert.equal(panel.isOpen(), true);
  // The real folder chooser, not a placeholder.
  assert.ok(doc.getElementById('downloadsJanitorPath'), 'expected the real setup surface');
  // Review/History/Settings would all be empty before there is a folder.
  const tabs = surface(doc).all(n => n.getAttribute('role') === 'tab');
  assert.equal(tabs.length, 0, 'tabs must not appear before setup finishes');
});

test('a configured install opens on Review when files are waiting', () => {
  const doc = setup();
  renderReview(doc);
  assert.equal(panel.activeTab(), 'review');
  const tabs = surface(doc).all(n => n.getAttribute('role') === 'tab');
  assert.deepEqual(
    tabs.map(t => t.getAttribute('data-fj-tab')),
    ['review', 'history', 'settings']
  );
});

// Work waiting for a decision outranks whatever was last looked at. A restored
// Settings tab would hide the one thing that actually needs the user.
test('pending files outrank a remembered tab', () => {
  const doc = setup();
  renderReview(doc);
  panel._selectTab('settings');
  assert.equal(panel.activeTab(), 'settings');

  panel.close();
  renderReview(doc);
  assert.equal(panel.activeTab(), 'review');
});

test('with nothing waiting, the remembered tab is restored', () => {
  setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('history');
  panel.close();

  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  assert.equal(panel.activeTab(), 'history');
});

test('an unknown tab falls back to Review rather than rendering nothing', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());
  panel.open({ source: 'test', tab: 'billing' });
  assert.equal(panel.activeTab(), 'review');
  assert.ok(surface(doc).textContent.length > 0, 'a bad tab must not empty the console');
});

test('opening twice leaves exactly one console', () => {
  const doc = setup();
  renderReview(doc);
  panel.open({ source: 'map-station' });
  panel.open({ source: 'workspace-details' });
  const dialogs = doc.getElementById('fileJanitorConsole').all(n => n.getAttribute('role') === 'dialog');
  assert.equal(dialogs.length, 1, 'every entry point shares one console');
});

test('the console is a labelled modal dialog', () => {
  const doc = setup();
  renderReview(doc);
  const dialog = surface(doc).all(n => n.getAttribute('role') === 'dialog')[0];
  assert.equal(dialog.getAttribute('aria-modal'), 'true');
  // The accessible name is the visible title, not a separate string that could
  // drift away from what is on screen.
  const labelledBy = dialog.getAttribute('aria-labelledby');
  assert.equal(doc.getElementById(labelledBy).textContent, 'File Janitor');
});

test('closing returns focus to the control that opened it', () => {
  const doc = setup();
  const station = new FakeElement('button');
  station.id = 'the-map-station';
  doc.body.appendChild(station);

  renderReview(doc);
  panel.open({ source: 'map-station', trigger: station });
  panel.close();
  assert.equal(doc.activeElement, station, 'focus must go back where the user left it');
});

test('opening puts focus inside the console', () => {
  const doc = setup();
  renderReview(doc);
  // Focus lands on the dialog itself, which puts a screen reader at the title
  // rather than partway into the controls.
  assert.equal(doc.activeElement.id, 'fileJanitorConsoleDialog');
});

test('a deep link opens the console on the requested tab', () => {
  setup();
  withLocation('?panel=file-janitor&tab=history');
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());
  panel._applyUrlState({ source: 'deep-link' });

  assert.equal(panel.isOpen(), true);
  assert.equal(panel.activeTab(), 'history');
  clearLocation();
});

test('a deep link with an unknown tab still opens the console', () => {
  setup();
  withLocation('?panel=file-janitor&tab=../../etc/passwd');
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());
  panel._applyUrlState({ source: 'deep-link' });

  assert.equal(panel.isOpen(), true, 'a bad value must not stop the workspace loading');
  assert.equal(panel.activeTab(), 'review');
  assert.equal(panel.focusedItem(), '');
  clearLocation();
});

test('opening and closing touch only File Janitor URL parameters', () => {
  const doc = setup();
  const url = withLocation('?view=map&task=t-9');
  renderReview(doc);
  panel.open({ source: 'map-station' });

  assert.match(url.search(), /panel=file-janitor/);
  assert.match(url.search(), /view=map/, 'unrelated state must survive opening');
  assert.match(url.search(), /task=t-9/);

  panel.close();
  assert.doesNotMatch(url.search(), /panel=/);
  assert.doesNotMatch(url.search(), /tab=/);
  assert.match(url.search(), /view=map/, 'unrelated state must survive closing');
  assert.match(url.search(), /task=t-9/);
  clearLocation();
});

test('Back closes a deep-linked console without leaving the workspace', () => {
  const doc = setup();
  const url = withLocation('?view=map');
  renderReview(doc);
  panel.open({ source: 'map-station' });
  assert.equal(panel.isOpen(), true);

  url.back();
  assert.equal(panel.isOpen(), false, 'Back must close the console');
  assert.match(url.search(), /view=map/, 'and must not navigate out of the workspace');
  clearLocation();
});

test('the console re-reads status on the way in', async () => {
  setup();
  panel.render(statusFixture());

  let asked = 0;
  globalThis.fetch = async url => {
    if (String(url).endsWith('/file-janitor')) asked += 1;
    return { ok: true, json: async () => ({ status: statusFixture(), batch: null, candidates: [] }) };
  };
  panel.open({ source: 'map-station' });
  await settle();
  assert.ok(asked > 0, 'a console opened from the Map may be showing minutes-old state');
});

test('subscribers hear about state changes', () => {
  setup();
  const seen = [];
  const unsubscribe = panel.subscribe(state => seen.push(state));
  panel.render(statusFixture());
  assert.ok(seen.length > 0, 'the station and the card update from one signal');
  assert.equal(seen[seen.length - 1].applies, true);

  unsubscribe();
  const before = seen.length;
  panel.render(statusFixture());
  assert.equal(seen.length, before, 'unsubscribing must actually detach');
});

// A listener that throws is a bug in that listener, not a reason for the
// console to stop working.
test('a broken subscriber cannot take the console down', () => {
  setup();
  let reached = 0;
  panel.subscribe(() => {
    throw new Error('boom');
  });
  panel.subscribe(() => {
    reached += 1;
  });
  panel.render(statusFixture());
  assert.equal(reached, 1);
});

test('station state follows the required priority order', () => {
  setup();

  // Needs attention outranks everything, including files waiting.
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render(
    statusFixture({
      readiness: {
        state: 'needs_attention',
        checks: [{ component: 'directory_access', status: 'failed', message: 'Folder is missing.' }]
      }
    })
  );
  assert.equal(panel.stationState().value, 'Needs attention');

  // Setup outranks a pending count, which can survive a folder being revoked.
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render({
    applies: true,
    settings: { filing_root_name: 'Filed' },
    readiness: { state: 'setup_required', checks: [] }
  });
  assert.equal(panel.stationState().value, 'Setup needed');

  // Files waiting outrank both Paused and Watching.
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render(statusFixture({ settings: { ...statusFixture().settings, paused: true } }));
  assert.equal(panel.stationState().value, '2 files ready for review');

  // Paused outranks Watching.
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture({ settings: { ...statusFixture().settings, paused: true } }));
  assert.equal(panel.stationState().value, 'Paused');

  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());
  assert.equal(panel.stationState().value, 'Watching');
});

test('Workspace Details carries no review table and no settings form', () => {
  const doc = setup();
  renderReview(doc);
  const card = doc.getElementById('downloadsJanitorMount');

  // The compact card answers "is anything waiting?" and offers a way in.
  assert.match(card.textContent, /File Janitor/);
  assert.match(card.textContent, /2 files waiting for review/);
  assert.ok(doc.getElementById('fileJanitorCardOpen'), 'expected a way into the console');

  // And nothing else. A hidden second copy of the review table or the settings
  // form is exactly what this split removes: two surfaces acting on the same
  // files, one of them stale and unseen.
  assert.equal(card.all(n => n.tagName === 'TABLE').length, 0);
  assert.equal(card.all(n => String(n.className).startsWith('dj-row-item')).length, 0);
  assert.equal(card.all(n => n.id === 'downloadsJanitorSettingsHost').length, 0);
  assert.equal(card.all(n => n.id === 'downloadsJanitorBatch').length, 0);
  assert.equal(card.all(n => n.id === 'downloadsJanitorHistoryHost').length, 0);
});

test('the console header carries the real scan and pause controls', () => {
  const doc = setup();
  renderReview(doc);
  const header = surface(doc);
  assert.ok(header.all(n => n.id === 'downloadsJanitorScan')[0], 'expected a real Scan now');
  assert.ok(header.all(n => n.id === 'downloadsJanitorPause')[0], 'expected a real Pause');
  assert.match(surface(doc).textContent, /2 files ready for review/);
});

test('before setup the header offers no scan or pause', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  // Neither applies to a folder that has not been chosen; offering them would
  // suggest File Janitor is already doing something.
  assert.equal(surface(doc).all(n => n.id === 'downloadsJanitorScan').length, 0);
  assert.equal(surface(doc).all(n => n.id === 'downloadsJanitorPause').length, 0);
  assert.ok(surface(doc).all(n => n.getAttribute('data-fj-console-close') === '')[0]);
});

test('Action Center findings open the same console on the requested tab', () => {
  setup();
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());

  panel.open({ source: 'action-center', tab: 'history', item: 'act-7' });
  assert.equal(panel.isOpen(), true);
  assert.equal(panel.activeTab(), 'history');
  assert.equal(panel.focusedItem(), 'act-7');
});

test('a stale Action Center item cannot open an invalid tab', () => {
  setup();
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());

  panel.open({ source: 'action-center', tab: 'nonsense', item: 'act-7' });
  assert.equal(panel.activeTab(), 'review');
});

test('DownloadsJanitorPanel is the same object, not a second controller', () => {
  assert.equal(globalThis.window.DownloadsJanitorPanel, globalThis.window.FileJanitorConsole);
});

test('a deep-linked candidate is focused in the review table', () => {
  const doc = setup();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render(statusFixture());
  panel.open({ source: 'action-center', tab: 'review', item: 'c1' });

  const row = doc.getElementById('fileJanitorLinkedRow');
  assert.ok(row, 'expected the named row to be marked');
  assert.equal(row.getAttribute('data-candidate-id'), 'c1');
  assert.equal(doc.activeElement, row, 'the row a notification named must be where focus lands');
});

// A finding can outlive the file it was about: it may have been filed,
// skipped, or scanned away. That is a normal outcome, not an error.
test('a deep link to a candidate that is gone still opens a working tab', () => {
  const doc = setup();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render(statusFixture());
  panel.open({ source: 'action-center', tab: 'review', item: 'c-vanished' });

  assert.equal(panel.isOpen(), true);
  assert.equal(panel.activeTab(), 'review');
  assert.equal(doc.getElementById('fileJanitorLinkedRow'), null);
  // Focus still lands inside the console rather than being stranded.
  // Focus lands on the dialog itself, which puts a screen reader at the title
  // rather than partway into the controls.
  assert.equal(doc.activeElement.id, 'fileJanitorConsoleDialog');
  assert.equal(doc.getElementById('downloadsJanitorError').hidden, true, 'not an error');
});

// ------------------------------------------------------ paging and selection

// pagedFetch answers /batches/latest from a deterministic pool of `total`
// candidates, honouring limit/offset the way the server does. It is how these
// tests exercise a 500-file batch without building 500 rows to assert against.
function pagedFetch(total, pageSize = 50) {
  const pool = Array.from({ length: total }, (_, i) => ({
    id: 'c' + i,
    name: 'file-' + String(i).padStart(3, '0') + '.pdf',
    display_name: 'file-' + String(i).padStart(3, '0') + '.pdf',
    extension: '.pdf',
    size: 1024,
    category: 'documents',
    destination: 'Filed/Documents',
    reason: 'pdf file',
    confidence: 'high',
    state: 'pending'
  }));
  return async url => {
    const target = String(url);
    if (!target.includes('/batches/latest')) {
      return { ok: true, json: async () => ({ status: statusFixture(), categories: CATEGORIES }) };
    }
    const params = new URLSearchParams(target.split('?')[1] || '');
    const offset = Number(params.get('offset')) || 0;
    const limit = Number(params.get('limit')) || pageSize;
    return {
      ok: true,
      json: async () => ({
        batch: batchFixture(),
        candidates: pool.slice(offset, offset + limit),
        total,
        filtered_total: total,
        counts: { all: total, needs_review: 0, pending: total, skipped: 0 }
      })
    };
  };
}

// The point of server-side paging: a 500-file batch must never become 500 DOM
// rows. This is the assertion that fails if anyone reintroduces client-side
// rendering of the whole batch (FR-150).
test('a 500-file batch renders one page of rows, not five hundred', async () => {
  const doc = setup();
  renderReview(doc);
  globalThis.fetch = pagedFetch(500);
  await settle();
  await panel._reloadBatch();

  const rows = rowsIn(surface(doc));
  assert.equal(rows.length, 50, 'only the requested page may be rendered');
  // And the surface still tells the truth about how much work is waiting.
  assert.match(surface(doc).textContent, /500/);
});

test('paging forward asks for the next page and keeps the counts', async () => {
  const doc = setup();
  renderReview(doc);
  globalThis.fetch = pagedFetch(500);
  await settle();
  await panel._reloadBatch();

  const next = surface(doc).all(n => n.id === 'downloadsJanitorPageNext')[0];
  assert.ok(next, 'expected a Next control');
  assert.equal(
    surface(doc).all(n => n.id === 'downloadsJanitorPagePrev')[0].disabled,
    true,
    'Previous is unavailable on the first page'
  );

  next.click();
  await settle();

  assert.match(surface(doc).textContent, /Showing 51/);
  assert.equal(rowsIn(surface(doc)).length, 50);
  assert.equal(surface(doc).all(n => n.id === 'downloadsJanitorPagePrev')[0].disabled, false);
});

// A user reviewing 300 files must not lose their work by turning a page.
test('selections survive a page change', async () => {
  const doc = setup();
  renderReview(doc);
  globalThis.fetch = pagedFetch(200);
  await settle();
  await panel._reloadBatch();

  panel._select('c0');
  panel._select('c1');
  assert.deepEqual(panel._selected().sort(), ['c0', 'c1']);

  surface(doc).all(n => n.id === 'downloadsJanitorPageNext')[0].click();
  await settle();

  assert.deepEqual(
    panel._selected().sort(),
    ['c0', 'c1'],
    'turning a page must not discard decisions already made'
  );
  // And the approve control still reflects them, from the new page.
  assert.equal(doc.getElementById('downloadsJanitorApprove').disabled, false);
});

// The other half of the rule: a file that has since been filed, skipped, or
// gone stale must not stay silently selected and ride along into an approval
// the user can no longer see.
test('a selection that is no longer eligible is dropped on refresh', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();

  const resolved = candidatesFixture().map(c => ({ ...c, state: 'skipped' }));
  globalThis.fetch = async url =>
    String(url).includes('/batches/latest')
      ? {
          ok: true,
          json: async () => ({
            batch: batchFixture(),
            candidates: resolved,
            total: resolved.length,
            filtered_total: resolved.length,
            counts: { all: resolved.length, needs_review: 0, pending: 0, skipped: resolved.length }
          })
        }
      : { ok: true, json: async () => ({ status: statusFixture(), categories: CATEGORIES }) };

  panel._select('c1');
  await panel._reloadBatch();

  assert.deepEqual(panel._selected(), [], 'a resolved file must not stay selected');
  assert.equal(doc.getElementById('downloadsJanitorApprove').disabled, true);
});

// A different batch invalidates every decision made against the old one.
test('a new batch clears selections from the old one', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();
  panel._select('c1');

  globalThis.fetch = async url =>
    String(url).includes('/batches/latest')
      ? {
          ok: true,
          json: async () => ({
            batch: { ...batchFixture(), id: 'batch-different' },
            candidates: candidatesFixture(),
            total: 3,
            filtered_total: 3,
            counts: { all: 3, needs_review: 1, pending: 2, skipped: 1 }
          })
        }
      : { ok: true, json: async () => ({ status: statusFixture(), categories: CATEGORIES }) };

  await panel._reloadBatch();
  assert.deepEqual(panel._selected(), [], 'decisions belong to the batch they were made in');
});

test('the pager states how much a filter hides', async () => {
  const doc = setup();
  renderReview(doc);
  globalThis.fetch = async url =>
    String(url).includes('/batches/latest')
      ? {
          ok: true,
          json: async () => ({
            batch: batchFixture(),
            candidates: Array.from({ length: 50 }, (_, i) => ({
              id: 'n' + i,
              name: 'x.pdf',
              display_name: 'x.pdf',
              state: 'pending',
              category: 'documents',
              needs_review: true
            })),
            total: 500,
            filtered_total: 120,
            counts: { all: 500, needs_review: 120, pending: 380, skipped: 0 }
          })
        }
      : { ok: true, json: async () => ({ status: statusFixture(), categories: CATEGORIES }) };
  await settle();
  panel._setFilter('needs_review');
  await settle();

  assert.match(surface(doc).textContent, /Showing 1–50 of 120 files \(filtered from 500\)/);
});

// ------------------------------------------- unresolved "Needs review" rows

// "Needs review" says Ori's proposal is not trustworthy. If the row stays
// selectable, the flag is decoration: it rides along in a bulk approval and is
// filed using the very guess that was flagged (FR-63, FR-64).
test('a flagged row cannot be selected until the user chooses a category', () => {
  const doc = setup();
  const host = renderReview(doc);

  const rows = rowsIn(host);
  const flaggedRow = rows.find(r => r.textContent.includes('payload.bin'));
  assert.ok(flaggedRow, 'expected the low-confidence fixture row');

  const box = flaggedRow.all(n => n.className === 'dj-select')[0];
  assert.equal(box.disabled, true, 'a flagged row must not be selectable on the guess');
  assert.match(box.getAttribute('title') || '', /Choose a category/i);

  // A confident row beside it is unaffected.
  const confidentRow = rows.find(r => r.textContent.includes('invoice-2026-07.pdf'));
  assert.equal(confidentRow.all(n => n.className === 'dj-select')[0].disabled, false);
});

test('choosing a category makes a flagged row selectable', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();

  const resolved = candidatesFixture().map(c =>
    c.id === 'c2' ? { ...c, decision_category: 'documents' } : c
  );
  panel._setBatch(batchFixture(), resolved, CATEGORIES);
  panel.renderBatch();

  const flaggedRow = rowsIn(surface(doc)).find(r => r.textContent.includes('payload.bin'));
  assert.equal(
    flaggedRow.all(n => n.className === 'dj-select')[0].disabled,
    false,
    'the user has now said where it goes'
  );
});

// The confirmation is a step inside the console, not a modal on top of it.
// Stacking a second dialog is what the single-active-modal rule exists to
// prevent, and it would put the destructive confirmation above its own context.
test('the confirmation renders inside the console, not as a second dialog', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.renderBatch();
  panel._select('c1');

  globalThis.fetch = async url =>
    String(url).endsWith('/preview')
      ? { ok: true, json: async () => ({ preview: PREVIEW }) }
      : { ok: true, json: async () => ({ status: statusFixture(), categories: CATEGORIES }) };

  doc.getElementById('downloadsJanitorApprove').click();
  await settle();

  const dialogs = surface(doc).all(n => n.getAttribute('role') === 'dialog');
  assert.equal(dialogs.length, 1, 'the console must remain the only dialog');
  const confirmHost = doc.getElementById('downloadsJanitorConfirmHost');
  assert.ok(confirmHost.textContent.length > 0, 'the confirmation belongs in the console body');
});

// ------------------------------------------------------------ settings tab

function withCatalog(record) {
  globalThis.window.WorkspaceCapabilities = {
    find: id => (id === 'file-janitor' ? { installed: true, available: true, record } : null),
    reload: async () => {}
  };
  return () => delete globalThis.window.WorkspaceCapabilities;
}

// Settings is where someone goes when something seems wrong, so the diagnosis
// belongs there and not only on a Review tab they may never open.
test('Settings states the folder, the watching state, and every readiness check', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(
    statusFixture({
      readiness: {
        state: 'needs_attention',
        checks: [
          { component: 'directory_access', status: 'failed', message: 'Folder is missing.', repair: 'relink_folder' },
          { component: 'watcher', status: 'ok' }
        ]
      }
    })
  );
  panel._selectTab('settings');

  const body = text(doc);
  assert.match(body, /Managing/);
  assert.match(body, /Inbox/);
  assert.match(body, /Watching this folder/);
  assert.match(body, /Folder access/);
  assert.match(body, /Folder is missing/);
  // And a way to fix it, not just a report that it is broken.
  assert.match(body, /Choose the folder again/);
});

test('a paused workspace says so in Settings, and says what still works', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture({ settings: { ...statusFixture().settings, paused: true } }));
  panel._selectTab('settings');
  assert.match(text(doc), /paused\. You can still scan on demand/i);
});

// The Curator is optional by design, so its absence is stated as a choice
// rather than presented as something missing.
test('Settings offers a Curator when there is none', () => {
  const doc = setup();
  const restore = withCatalog({ id: 'file-janitor', owned_resources: [] });
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  const body = text(doc);
  assert.match(body, /No Curator in this workspace/);
  assert.match(body, /works fully without one/);
  assert.match(body, /can never act on your files/);
  assert.ok(doc.getElementById('downloadsJanitorAddCurator'), 'expected an add control');
  restore();
});

// Presence comes from the install record's owned resources — the same
// association the server uses for idempotency — never from an agent's name.
test('Settings reports an existing Curator from the install record', () => {
  const doc = setup();
  const restore = withCatalog({
    id: 'file-janitor',
    owned_resources: [{ kind: 'companion_agent', id: 'agent-1' }]
  });
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  const body = text(doc);
  assert.match(body, /A Curator is helping in this workspace/);
  assert.match(body, /cannot approve, move, or delete/);
  assert.equal(doc.getElementById('downloadsJanitorAddCurator'), null, 'no second Curator offer');
  restore();
});

test('adding a Curator posts to the companion endpoint and reports the result', async () => {
  const doc = setup();
  const restore = withCatalog({ id: 'file-janitor', owned_resources: [] });
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  let posted = null;
  globalThis.fetch = async (url, opts) => {
    if (String(url).endsWith('/companion')) {
      posted = { url: String(url), method: opts.method };
      return {
        ok: true,
        json: async () => ({ success: true, display_name: 'File Curator', already_present: false })
      };
    }
    return { ok: true, json: async () => ({ status: statusFixture() }) };
  };

  doc.getElementById('downloadsJanitorAddCurator').click();
  await settle();

  assert.ok(posted, 'expected a request');
  assert.equal(posted.method, 'POST');
  assert.match(posted.url, /\/capabilities\/file-janitor\/companion$/);
  assert.match(doc.getElementById('downloadsJanitorSettingsStatus').textContent, /File Curator/);
  restore();
});

test('a refused Curator request is reported, not swallowed', async () => {
  const doc = setup();
  const restore = withCatalog({ id: 'file-janitor', owned_resources: [] });
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  globalThis.fetch = async url =>
    String(url).endsWith('/companion')
      ? { ok: false, json: async () => ({ message: 'Agents are unavailable right now.' }) }
      : { ok: true, json: async () => ({ status: statusFixture() }) };

  doc.getElementById('downloadsJanitorAddCurator').click();
  await settle();

  assert.match(
    doc.getElementById('downloadsJanitorSettingsStatus').textContent,
    /Agents are unavailable/
  );
  restore();
});

// ------------------------------------------------------------------ removal

const REMOVAL_SUMMARY = {
  capability_id: 'file-janitor',
  display_name: 'File Janitor',
  installed: true,
  managed_folder: 'Inbox',
  stops_automation: ['Watching this folder for new files.', 'The daily catch-up scan.'],
  retained_audit: ['The history of everything File Janitor filed, trashed, or restored.'],
  moves_files: false
};

function openSettings(catalogRecord = { id: 'file-janitor', owned_resources: [] }) {
  const restore = withCatalog(catalogRecord);
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');
  return restore;
}

test('Settings offers removal and says plainly that files are not touched', () => {
  const doc = setup();
  const restore = openSettings();

  assert.ok(doc.getElementById('downloadsJanitorRemove'), 'expected a Remove File Janitor control');
  assert.match(text(doc), /Your files are not moved or deleted/);
  restore();
});

// The confirmation must state what removal does to THIS workspace. Copy written
// in the browser cannot name the folder, and a user who cannot see which folder
// is losing access cannot evaluate the decision.
test('the confirmation is built from the server dry run', async () => {
  const doc = setup();
  const restore = openSettings();

  // Record only the removal request. Other reads (status, history) are in
  // flight around it and would otherwise be what this ends up asserting on.
  let asked = null;
  globalThis.fetch = async url => {
    const target = String(url);
    if (target.endsWith('/removal')) asked = target;
    return { ok: true, json: async () => ({ removal: REMOVAL_SUMMARY }) };
  };

  doc.getElementById('downloadsJanitorRemove').click();
  await settle();

  assert.ok(asked, 'expected the dry-run request');
  assert.match(asked, /\/capabilities\/file-janitor\/removal$/);
  const body = text(doc);
  assert.match(body, /stop managing Inbox/);
  assert.match(body, /Watching this folder for new files/);
  assert.match(body, /gives up its access/);
  assert.match(body, /No files are moved, renamed, deleted, or restored/);
  assert.match(body, /history of everything File Janitor filed/);
  restore();
});

test('cancelling removal changes nothing and returns to Settings', async () => {
  const doc = setup();
  const restore = openSettings();

  let deleted = false;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'DELETE') deleted = true;
    return { ok: true, json: async () => ({ removal: REMOVAL_SUMMARY }) };
  };

  doc.getElementById('downloadsJanitorRemove').click();
  await settle();
  doc.getElementById('downloadsJanitorRemoveCancel').click();

  assert.equal(deleted, false, 'cancelling must not remove anything');
  assert.ok(doc.getElementById('downloadsJanitorRemove'), 'back to the entry point');
  assert.equal(doc.getElementById('downloadsJanitorRemoveConfirm'), null);
  restore();
});

// Uninstalling a capability is not consent to delete an agent, so the request
// carries the companion decision explicitly and it defaults to false.
test('removal does not remove the Curator unless separately ticked', async () => {
  const doc = setup();
  const restore = openSettings({
    id: 'file-janitor',
    owned_resources: [{ kind: 'companion_agent', id: 'agent-1' }]
  });

  let sent = null;
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'DELETE') {
      sent = JSON.parse(opts.body);
      return { ok: true, json: async () => ({ removed: true }) };
    }
    if (String(url).endsWith('/removal')) {
      return {
        ok: true,
        json: async () => ({
          removal: {
            ...REMOVAL_SUMMARY,
            companion: { agent_instance_id: 'agent-1', removable: true }
          }
        })
      };
    }
    return { ok: true, json: async () => ({ status: { applies: false } }) };
  };

  doc.getElementById('downloadsJanitorRemove').click();
  await settle();
  assert.ok(doc.getElementById('downloadsJanitorRemoveCompanion'), 'expected a separate choice');
  assert.equal(
    doc.getElementById('downloadsJanitorRemoveCompanion').checked,
    false,
    'the companion choice must default to off'
  );

  doc.getElementById('downloadsJanitorRemoveConfirm').click();
  await settle();
  assert.deepEqual(sent, { remove_companion: false });
  restore();
});

test('an adopted Curator is described as left alone, with no checkbox', async () => {
  const doc = setup();
  const restore = openSettings();

  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({
      removal: {
        ...REMOVAL_SUMMARY,
        companion: {
          agent_instance_id: 'agent-1',
          removable: false,
          reason: 'This agent existed before File Janitor was installed, so removing it leaves it alone.'
        }
      }
    })
  });

  doc.getElementById('downloadsJanitorRemove').click();
  await settle();

  assert.equal(doc.getElementById('downloadsJanitorRemoveCompanion'), null);
  assert.match(text(doc), /existed before File Janitor was installed/);
  restore();
});

// After a successful removal the console is showing a capability the workspace
// no longer has, so it closes and the card goes with it.
test('a successful removal closes the console and clears the card', async () => {
  const doc = setup();
  let reloaded = 0;
  const restore = withCatalog({ id: 'file-janitor', owned_resources: [] });
  globalThis.window.WorkspaceCapabilities.reload = async () => {
    reloaded += 1;
  };
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  panel._selectTab('settings');

  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'DELETE') {
      return { ok: true, json: async () => ({ removed: true }) };
    }
    if (String(url).endsWith('/removal')) {
      return { ok: true, json: async () => ({ removal: REMOVAL_SUMMARY }) };
    }
    return { ok: true, json: async () => ({ status: { applies: false } }) };
  };

  doc.getElementById('downloadsJanitorRemove').click();
  await settle();
  doc.getElementById('downloadsJanitorRemoveConfirm').click();
  await settle();

  assert.equal(panel.isOpen(), false, 'the console must not linger over a removed capability');
  assert.equal(doc.getElementById('downloadsJanitorMount').hidden, true, 'the card goes too');
  assert.equal(panel.stationState().applies, false, 'and so does the Map station');
  assert.ok(reloaded > 0, 'the catalog must re-read so the capability offers itself again');
  restore();
});

// A failed removal must say so and leave the capability usable, not strand the
// user in a half-removed state with no way back.
test('a failed removal is reported and leaves File Janitor in place', async () => {
  const doc = setup();
  const restore = openSettings();

  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'DELETE') {
      return {
        ok: false,
        json: async () => ({ message: 'Ori could not stop the background work.' })
      };
    }
    return { ok: true, json: async () => ({ removal: REMOVAL_SUMMARY }) };
  };

  doc.getElementById('downloadsJanitorRemove').click();
  await settle();
  doc.getElementById('downloadsJanitorRemoveConfirm').click();
  await settle();

  assert.match(
    doc.getElementById('downloadsJanitorSettingsStatus').textContent,
    /could not stop the background work/
  );
  assert.equal(panel.isOpen(), true, 'the console stays open after a failed removal');
  restore();
});
