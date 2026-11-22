// Tests for file-janitor-console.js — the File Janitor console and the
// panel. Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/file-janitor-console.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import {
  createProtectionTrips,
  protectionSentence,
  protectionStripSentence,
  TRIP_KEY
} from './file-janitor-protection.js';
import * as pictureModule from './file-janitor-picture.js';
import * as receiptModule from './file-janitor-receipt.js';

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
  get parentElement() {
    return this.parent;
  }
  closest(selector) {
    if (selector === '[data-fj-file-row]' && 'data-fj-file-row' in this._attrs) return this;
    return this.parent?.closest(selector) || null;
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
  dispatch(event, payload = {}) {
    (this._listeners[event] || []).forEach(fn => fn(payload));
  }
  focus() {
    // A disabled control or one inside a hidden view cannot take focus in a
    // real browser, and the panel relies on both being true.
    if (this.disabled) return;
    let node = this;
    while (node) {
      if (node.hidden) return;
      node = node.parent;
    }
    globalThis.document.activeElement = this;
  }
  removeAttribute(k) {
    delete this._attrs[k];
  }
  get isConnected() {
    return this._connected !== false;
  }
  querySelector(selector) {
    if (selector === 'button') return this.all(node => node.tagName === 'BUTTON')[0] || null;
    return null;
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

// Review pins a concrete batch after resolving latest. Detail mocks answer both
// forms, but not the distinct /groups or summary-only batch-list endpoints.
function isBatchDetailRequest(url) {
  return /\/batches\/[^/?]+(?:\?|$)/.test(String(url));
}

function setup() {
  const doc = new FakeDocument();
  // The global goes first: registering a node attaches it, and attachment
  // records ids against globalThis.document.
  globalThis.document = doc;
  doc.register('fileJanitorMount');
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
  // The page loads the picture module as a script of its own; the console reads
  // window.FileJanitorPicture when it draws.
  globalThis.FileJanitorPicture = pictureModule;
  globalThis.FileJanitorReceipt = receiptModule;
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
  return doc.getElementById('fileJanitorConsole') || doc.getElementById('fileJanitorMount');
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
  return doc.getElementById('fileJanitorMount').textContent;
}

function attachedReview(overrides = {}) {
  return {
    state: 'review',
    workspace_id: 'ws-1',
    reference_id: 'attached-1',
    review_revision: 'a'.repeat(64),
    monitoring: false,
    candidates: [
      { reference_id: 'attached-1', name: 'Synthetic Inbox', path: '/tmp/Synthetic Inbox' }
    ],
    terms: {
      watch_events: ['create', 'rename'],
      debounce_seconds: 300,
      daily_scan_local_time: '09:00',
      timezone: 'Asia/Tokyo'
    },
    ...overrides
  };
}

test('setup-review completion notifies subscribers with only the selected display name', async () => {
  setup();
  panel.render(setupRequiredStatus);
  const seen = [];
  panel.subscribe(snapshot => seen.push(snapshot.actionState));
  let review = attachedReview();
  globalThis.fetch = async url => ({
    ok: true,
    json: async () =>
      String(url).endsWith('/setup-review')
        ? { review }
        : String(url).endsWith('/file-janitor')
          ? { status: setupRequiredStatus }
          : { batches: [], actions: [], candidates: [] }
  });
  await panel.refresh();
  assert.ok(seen.some(snapshot => snapshot.linkedFolderName === 'Synthetic Inbox'));
  assert.equal(panel.getActionState().linkedFolderName, 'Synthetic Inbox');
  assert.doesNotMatch(JSON.stringify(seen), /\/tmp\/Synthetic Inbox/);
  for (const state of [
    'choose_folder',
    'prepared',
    'preparation_incomplete',
    'preparation_changed',
    'folder_unavailable',
    'folder_conflict'
  ]) {
    review = attachedReview({ state });
    await panel.refresh();
    assert.equal(panel.getActionState().linkedFolderName, '', state);
    assert.equal(seen.at(-1).linkedFolderName, '', state);
  }
  for (const overrides of [
    { reference_id: '' },
    { reference_id: 'missing' },
    { candidates: [] },
    { candidates: [...attachedReview().candidates, ...attachedReview().candidates] }
  ]) {
    review = attachedReview(overrides);
    await panel.refresh();
    assert.equal(panel.getActionState().linkedFolderName, '');
  }
});

test('attached folder review is default-off and discloses its actual filesystem change', () => {
  const doc = setup();
  panel._setSetupReview(attachedReview());
  const host = doc.createElement('div');
  let calls = 0;
  globalThis.fetch = async () => {
    calls++;
    throw new Error('a pure render must not call an endpoint');
  };
  assert.equal(panel._renderSetupReview(host), true);
  assert.match(host.textContent, /Allow filing in Synthetic Inbox/);
  assert.equal(
    host.all(node => node.tagName === 'H3')[0].textContent,
    'Allow filing in Synthetic Inbox'
  );
  assert.match(host.textContent, /Create or reuse Filed/);
  assert.match(host.textContent, /No file contents are opened/);
  assert.match(host.textContent, /separate approval/);
  assert.match(host.textContent, /No watcher or daily scan/);
  assert.equal(host.all(node => node.tagName === 'INPUT')[0].checked, false);
  assert.equal(calls, 0);
});

for (const source of ['workspace-next-action', 'map-station']) {
  test(`${source} opens and dismisses the linked review without initiating work`, async () => {
    const doc = setup();
    const trigger = doc.register('opener');
    const writes = [];
    globalThis.fetch = async (url, options) => {
      if (options?.method) writes.push([url, options.method]);
      return {
        ok: true,
        json: async () =>
          String(url).endsWith('/setup-review')
            ? { review: attachedReview() }
            : { status: setupRequiredStatus }
      };
    };
    await panel.refresh();
    if (source === 'map-station') panel.open({ source, trigger });
    else panel.focusSetup(trigger, { source });
    await settle();
    assert.match(text(doc), /Allow filing in Synthetic Inbox/);
    assert.match(text(doc), /Folder details/);
    assert.match(text(doc), /No watcher or daily scan/);
    panel.close();
    assert.equal(doc.getElementById('fileJanitorConsole').hidden, true);
    assert.equal(doc.activeElement, trigger);
    assert.deepEqual(writes, []);
  });
}

test('confirmation waits for the reviewed draft and posts its exact terms only once', async () => {
  const doc = setup();
  const initial = attachedReview();
  panel.render(setupRequiredStatus);
  panel._setSetupReview(initial);
  const reviewed = { ...initial, monitoring: true, review_revision: 'b'.repeat(64) };
  const choice = deferredReply();
  const preparation = deferredReply();
  const writes = [];
  globalThis.fetch = async (url, options) => {
    if (options?.method) writes.push([String(url).split('/').pop(), JSON.parse(options.body)]);
    if (String(url).endsWith('/setup-review') && options?.method) return choice.promise;
    if (String(url).endsWith('/prepare-review')) return preparation.promise;
    return {
      ok: true,
      json: async () =>
        String(url).endsWith('/setup-review')
          ? {
              review: writes.some(([route]) => route === 'prepare-review')
                ? attachedReview({ state: 'preparation_changed' })
                : initial
            }
          : { status: setupRequiredStatus }
    };
  };
  const confirm = () =>
    surface(doc).all(
      node => node.tagName === 'BUTTON' && node.textContent.startsWith('Allow filing in')
    )[0];
  panel.open();
  await settle();
  const monitor = doc.getElementById('fileJanitorReviewMonitoring');
  monitor.checked = true;
  monitor.dispatch('change');
  confirm().click();
  assert.deepEqual(writes, [
    ['setup-review', { reference_id: initial.reference_id, monitoring: true }]
  ]);
  choice.reply({ review: reviewed });
  await settle();
  assert.equal(doc.activeElement, doc.getElementById('fileJanitorReviewMonitoring'));
  const original = confirm();
  original.click();
  original.click();
  assert.deepEqual(writes.slice(1), [
    [
      'prepare-review',
      {
        reference_id: reviewed.reference_id,
        review_revision: reviewed.review_revision,
        monitoring: true
      }
    ]
  ]);
  preparation.reply({ error: { message: 'Permissions changed' } }, false);
  await settle();
  assert.match(text(doc), /Permissions changed/);
  assert.equal(confirm().disabled, true);
  confirm().click();
  assert.equal(writes.length, 2);
});

for (const state of ['folder_conflict', 'folder_unavailable']) {
  test(`${state} blocks linked confirmation`, () => {
    const doc = setup();
    panel._setSetupReview(attachedReview({ state }));
    const host = doc.createElement('div');
    panel._renderSetupReview(host);
    globalThis.fetch = () => assert.fail('blocked review cannot submit');
    const confirm = host.all(node => node.tagName === 'BUTTON')[0];
    assert.equal(confirm.disabled, true);
    confirm.click();
  });
}

test('a malformed success response cannot invent a prepared result', async () => {
  const doc = setup();
  panel.render(setupRequiredStatus);
  let sent = false;
  globalThis.fetch = async url => ({
    ok: true,
    json: async () => {
      if (String(url).endsWith('/prepare-review')) {
        sent = true;
        return { preparation: { workspace_id: 'ws-1' } };
      }
      if (String(url).endsWith('/setup-review'))
        return {
          review: sent ? { state: 'repair_required', workspace_id: 'ws-1' } : attachedReview()
        };
      return { status: setupRequiredStatus };
    }
  });
  panel.open();
  await settle();
  surface(doc)
    .all(node => node.tagName === 'BUTTON' && node.textContent.startsWith('Allow filing in'))[0]
    .click();
  await settle();
  assert.match(text(doc), /initial result could not be confirmed/);
  assert.doesNotMatch(text(doc), /Nothing moved\./);
});

for (const destination of ['stay', 'dismiss', 'reopen', 'workspace', 'root']) {
  test(`deferred preparation respects ${destination} without losing the saved authority`, async t => {
    const doc = setup();
    const priorLocation = window.location;
    const priorHistory = window.history;
    t.after(() => Object.assign(window, { location: priorLocation, history: priorHistory }));
    window.location = new URL('http://localhost/workspaces/synthetic?mode=map');
    window.history = {
      replaceState(_state, _title, url) {
        window.location = new URL(url);
      },
      pushState(_state, _title, url) {
        window.location = new URL(url);
      }
    };
    const trigger = doc.register('map-invite');
    const grant = deferredReply();
    const result = {
      privacy_mode: 'metadata_only',
      operation_id: 'accepted:scan',
      root_id: 'root-1',
      batch_id: 'batch-1',
      completed_at: '2026-10-26T12:00:00Z',
      outcome: 'batch',
      eligible_count: 1,
      ineligible_count: 0
    };
    let status = setupRequiredStatus;
    let review = attachedReview();
    const requests = [];
    globalThis.fetch = async (url, options) => {
      requests.push([String(url), options?.method]);
      if (String(url).endsWith('/prepare-review')) return grant.promise;
      return {
        ok: true,
        json: async () => {
          if (String(url).endsWith('/setup-review')) return { review };
          if (String(url).endsWith('/file-janitor')) return { status };
          if (isBatchDetailRequest(url))
            return { batch: { id: 'batch-1', summary: { proposed: 1 } } };
          return { batches: [], actions: [], candidates: [] };
        }
      };
    };
    panel.render(status);
    panel._setSetupReview(review);
    panel.focusSetup(trigger);
    await settle();
    surface(doc)
      .all(node => node.tagName === 'BUTTON' && node.textContent.startsWith('Allow filing in'))[0]
      .click();
    if (destination !== 'stay' && destination !== 'root') panel.close();
    if (destination === 'workspace') {
      panel.dispose();
      window.location = new URL('http://localhost/workspaces/other?mode=map');
      panel.init('ws-2');
    }
    if (destination === 'reopen') panel.open({ source: 'map-station', trigger });
    const focusBefore = doc.activeElement;
    const routeBefore = window.location.href;
    status = statusFixture();
    status.settings.root_id = destination === 'root' ? 'other-root' : 'root-1';
    status.settings.setup_preparation = { operation_id: 'accepted' };
    review = attachedReview({ state: 'prepared', operation_id: 'accepted', result });
    grant.reply({ preparation: { workspace_id: 'ws-1', operation_id: 'accepted', result } });
    await settle();
    await settle();
    if (destination === 'stay') {
      assert.equal(window.location.searchParams.get('preparation'), 'accepted');
      assert.equal(doc.activeElement?.id, 'fileJanitorConsoleTitle');
      assert.match(text(doc), /1 file ready for review.*Nothing moved/s);
    } else {
      assert.equal(window.location.href, routeBefore);
      if (destination === 'dismiss') {
        assert.equal(doc.activeElement, focusBefore);
        assert.equal(doc.getElementById('fileJanitorConsole').hidden, true);
        panel.open({ source: 'map-station', trigger });
        await settle();
        assert.equal(panel.getActionState().batchId, 'batch-1');
      } else if (destination === 'reopen') {
        assert.notEqual(doc.activeElement?.id, 'fileJanitorConsoleTitle');
        assert.match(text(doc), /Nothing moved/);
      } else {
        assert.doesNotMatch(text(doc), /Nothing moved|first review is ready/);
      }
    }
    assert.equal(requests.filter(([url]) => url.endsWith('/prepare-review')).length, 1);
    assert.ok(requests.every(([url]) => !url.endsWith('/apply')));
  });
}

test('a newly linked empty folder retains its verified initial result through status repaint', async t => {
  const doc = setup();
  const previousLocation = window.location;
  const previousHistory = window.history;
  t.after(() => {
    window.location = previousLocation;
    window.history = previousHistory;
  });
  window.location = new URL('http://localhost/workspaces/synthetic');
  window.history = {
    replaceState(_state, _title, url) {
      window.location = new URL(url);
    }
  };
  const result = {
    privacy_mode: 'metadata_only',
    operation_id: 'accepted:scan',
    root_id: 'root-1',
    completed_at: '2026-10-26T12:00:00Z',
    outcome: 'no_eligible',
    eligible_count: 0,
    ineligible_count: 0
  };
  let status = statusFixture();
  status.settings.root_id = 'root-1';
  status.settings.setup_preparation = { operation_id: 'accepted' };
  panel.render(setupRequiredStatus);
  let accepted = false;
  const calls = [];
  globalThis.fetch = async (url, options = {}) => {
    calls.push([String(url), options.method || 'GET']);
    return {
      ok: true,
      json: async () => {
        if (String(url).endsWith('/prepare-review')) {
          accepted = true;
          return { preparation: { workspace_id: 'ws-1', operation_id: 'accepted', result } };
        }
        if (String(url).endsWith('/setup-review'))
          return {
            review: accepted
              ? attachedReview({ state: 'prepared', operation_id: 'accepted', result })
              : attachedReview()
          };
        if (String(url).endsWith('/file-janitor'))
          return { status: accepted ? status : setupRequiredStatus };
        return { batches: [], actions: [], candidates: [] };
      }
    };
  };
  panel.open();
  await settle();
  surface(doc)
    .all(node => node.tagName === 'BUTTON' && node.textContent.startsWith('Allow filing in'))[0]
    .click();
  await settle();
  await settle();
  assert.match(text(doc), /Initial scan finished/);
  assert.doesNotMatch(text(doc), /Not scanned in this session/);
  assert.equal(window.location.searchParams.get('preparation'), 'accepted');
  await panel.refresh();
  assert.match(text(doc), /Initial scan finished/);
  // An old review reply cannot relabel a subsequently replaced managed root.
  status = { ...status, settings: { ...status.settings, root_id: 'replacement' } };
  await panel.refresh();
  assert.doesNotMatch(text(doc), /Initial scan finished/);
  assert.deepEqual(
    calls.filter(([, method]) => method !== 'GET'),
    [['/api/workspaces/ws-1/file-janitor/prepare-review', 'POST']]
  );
});

test('ambiguous attached folders require a choice before confirmation', () => {
  const doc = setup();
  panel._setSetupReview(
    attachedReview({
      state: 'choose_folder',
      reference_id: '',
      review_revision: '',
      candidates: [
        ...attachedReview().candidates,
        { reference_id: 'attached-2', name: 'Synthetic Inbox', path: '/tmp/Other Inbox' }
      ]
    })
  );
  const host = doc.createElement('div');
  panel._renderSetupReview(host);
  assert.equal(host.all(node => node.tagName === 'SELECT')[0].value, '');
  assert.equal(
    host.all(node => node.tagName === 'BUTTON' && node.textContent.startsWith('Allow filing in'))[0]
      .disabled,
    true
  );
});

test('changed preparation exposes Stop but cannot retry or silently re-enable permissions', () => {
  const doc = setup();
  panel._setSetupReview(
    attachedReview({ state: 'preparation_changed', operation_id: 'accepted-op' })
  );
  const host = doc.createElement('div');
  panel._renderSetupReview(host);
  assert.match(host.textContent, /old confirmation cannot continue/);
  const buttons = host.all(node => node.tagName === 'BUTTON');
  assert.equal(buttons.find(node => node.textContent === 'Retry preparation').disabled, true);
  assert.equal(
    buttons.find(node => node.textContent === 'Stop here — keep saved setup').disabled,
    false
  );
});

test('incomplete preparation offers an explicit stop without promising to undo saved effects', () => {
  const doc = setup();
  panel._setSetupReview(
    attachedReview({ state: 'preparation_incomplete', operation_id: 'accepted-operation' })
  );
  const host = doc.createElement('div');
  panel._renderSetupReview(host);
  const buttons = host.all(node => node.tagName === 'BUTTON');
  assert.ok(buttons.some(button => button.textContent === 'Retry preparation'));
  assert.ok(buttons.some(button => button.textContent === 'Stop here — keep saved setup'));
  assert.match(host.textContent, /saved access, Filed\/ and any existing result are kept/);
  assert.match(host.textContent, /nothing already done is undone/);
  assert.equal(host.all(node => node.tagName === 'INPUT')[0].disabled, true);
});

test('monitoring review states the server schedule and delay instead of inventing defaults', () => {
  const doc = setup();
  panel._setSetupReview(attachedReview({ monitoring: true }));
  const host = doc.createElement('div');
  panel._renderSetupReview(host);
  assert.match(host.textContent, /300-second delay/);
  assert.match(host.textContent, /09:00 \(Asia\/Tokyo\)/);
  assert.match(host.textContent, /skip Filed/);
  assert.match(host.textContent, /Nothing moves automatically/);
});

test('explicit manual-ready is not presented as user-paused or watching', () => {
  const doc = setup();
  const status = statusFixture();
  status.settings = {
    ...status.settings,
    root_id: 'root-1',
    paused: true,
    setup_decision: { mode: 'manual', root_id: 'root-1', decided_at: '2026-10-19T00:00:00Z' }
  };
  openConsole(status);
  assert.equal(panel.stationState().value, 'Ready');
  assert.equal(panel.stationState().description, 'Manual scans only');
  assert.match(text(doc), /Ready · Manual scans/);
  assert.doesNotMatch(text(doc), /Paused by you/);
});

test('a workspace without the capability renders nothing and opens no console', () => {
  const doc = setup();
  openConsole({ applies: false });
  const host = doc.getElementById('fileJanitorMount');
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
  const input = doc.getElementById('fileJanitorPath');
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
  const input = doc.getElementById('fileJanitorPath');
  assert.equal(input.value, '', 'a generic install must not invent a folder');
  const confirm = doc.getElementById('fileJanitorConfirm');
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
  const input = doc.getElementById('fileJanitorPath');
  const confirm = doc.getElementById('fileJanitorConfirm');
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
  const input = doc.getElementById('fileJanitorPath');
  assert.equal(input.value, '~/Downloads', 'the Downloads preset still suggests its folder');
  const confirm = doc.getElementById('fileJanitorConfirm');
  assert.equal(confirm.disabled, false);
});

test('the setup card is titled Filing, not Downloads Janitor', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  const title = doc.getElementById('fileJanitorTitle');
  assert.equal(title.textContent, 'Filing');
});

test('the folder input is labelled and described for screen readers', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  const host = surface(doc);
  const label = host.all(n => n.tagName === 'LABEL')[0];
  assert.ok(label, 'expected a label');
  assert.equal(label.getAttribute('for'), 'fileJanitorPath');
  const input = doc.getElementById('fileJanitorPath');
  assert.equal(input.getAttribute('aria-describedby'), 'fileJanitorDisclosure');
  const error = doc.getElementById('fileJanitorError');
  assert.equal(error.getAttribute('aria-live'), 'polite');
});

test('confirming with an empty folder reports an error and calls no endpoint', async () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  await settle(); // Drain open's read-only status/folder-review refresh first.
  let called = false;
  globalThis.fetch = async () => {
    called = true;
    return { ok: true, json: async () => ({}) };
  };
  doc.getElementById('fileJanitorPath').value = '   ';
  doc.getElementById('fileJanitorConfirm').click();
  await new Promise(r => setTimeout(r, 0));
  assert.equal(called, false, 'an empty selection must not reach the server');
  assert.equal(doc.getElementById('fileJanitorError').hidden, false);
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
  doc.getElementById('fileJanitorPath').value = '/tmp/Inbox';
  doc.getElementById('fileJanitorConfirm').click();
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
  doc.getElementById('fileJanitorPath').value = '/tmp/blocked';
  doc.getElementById('fileJanitorConfirm').click();
  await new Promise(r => setTimeout(r, 0));

  const error = doc.getElementById('fileJanitorError');
  assert.equal(error.hidden, false);
  assert.match(error.textContent, /permission/i);
  assert.equal(doc.getElementById('fileJanitorConfirm').disabled, false);
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
  const marks = host.all(n => n.className === 'fj-row-mark');
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
  return host.all(n => n.className && String(n.className).includes('fj-row-item'));
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

test('review rows present five decisions-first columns with expandable file details', () => {
  const doc = setup();
  const host = renderReview(doc);
  const headers = host.all(node => node.tagName === 'TH').map(node => node.textContent);
  assert.deepEqual(headers, ['Select', 'File', 'Destination', 'Why / Status', 'Actions']);

  const row = rowsIn(host).find(node => node.getAttribute('data-candidate-id') === 'c1');
  const disclosure = row.all(
    node => node.tagName === 'BUTTON' && node.getAttribute('data-fj-file-details') === 'c1'
  )[0];
  assert.ok(disclosure, 'the file cell has one details control');
  assert.equal(disclosure.getAttribute('aria-expanded'), 'false');
  assert.match(disclosure.getAttribute('aria-label'), /Show file details for invoice-2026-07.pdf/);

  disclosure.click();
  assert.equal(disclosure.getAttribute('aria-expanded'), 'true');
  const details = row.all(node => node.className === 'fj-file-details')[0];
  assert.equal(details.hidden, false);
  assert.match(details.textContent, /Type \.pdf/);
  assert.match(details.textContent, /Size 200 KB/);
  assert.match(details.textContent, /Modified/);

  panel.renderBatch();
  const refreshed = rowsIn(doc.getElementById('fileJanitorBatch')).find(
    node => node.getAttribute('data-candidate-id') === 'c1'
  );
  const refreshedDisclosure = refreshed.all(
    node => node.tagName === 'BUTTON' && node.getAttribute('data-fj-file-details') === 'c1'
  )[0];
  assert.equal(refreshedDisclosure.getAttribute('aria-expanded'), 'true');
  assert.equal(refreshed.all(node => node.className === 'fj-file-details')[0].hidden, false);

  panel._setBatch({ ...batchFixture(), id: 'batch-2' }, candidatesFixture(), CATEGORIES);
  panel.renderBatch();
  const replacement = rowsIn(doc.getElementById('fileJanitorBatch')).find(
    node => node.getAttribute('data-candidate-id') === 'c1'
  );
  assert.equal(
    replacement
      .all(
        node => node.tagName === 'BUTTON' && node.getAttribute('data-fj-file-details') === 'c1'
      )[0]
      .getAttribute('aria-expanded'),
    'false'
  );
});

test('whole-batch progress separates remaining, subset, outcomes, and ineligible observations', () => {
  const doc = setup();
  const batch = batchFixture({
    state: 'partially_applied',
    summary: {
      proposed: 3,
      needs_review: 2,
      skipped: 4,
      ineligible: 5,
      stale: 1,
      applied: 6,
      failed: 1,
      total: 15
    }
  });
  renderReview(doc, batch, candidatesFixture());

  const progress = surface(doc).all(node => node.className === 'fj-batch-progress')[0];
  assert.ok(progress, 'the review needs a whole-scan progress readout');
  assert.match(progress.textContent, /3 remaining of 15 files/);
  assert.match(progress.textContent, /2 need review within remaining/);
  assert.match(progress.textContent, /6 completed/);
  assert.match(progress.textContent, /4 skipped/);
  assert.match(progress.textContent, /2 problems/);
  assert.match(progress.textContent, /5 observed but not eligible/);
});

test('a resolved batch with failures reads as complete with problems, not all clear', () => {
  const doc = setup();
  const batch = batchFixture({
    state: 'resolved',
    summary: {
      proposed: 0,
      needs_review: 0,
      skipped: 1,
      ineligible: 2,
      stale: 1,
      applied: 3,
      failed: 1,
      total: 6
    }
  });
  renderReview(doc, batch, []);

  const body = text(doc);
  assert.match(body, /Review complete with problems/);
  assert.match(body, /2 problems/);
  assert.match(body, /No files are waiting, but 2 problems still need attention/);
  assert.doesNotMatch(body, /all clear/i);
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
  const boxes = host.all(n => n.className === 'fj-select');
  assert.equal(boxes.length, 3);
  boxes.forEach(box => assert.equal(box.checked, false));
  assert.deepEqual(panel._selected(), []);
  // With nothing ticked there is nothing to file, and the button says so.
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
});

test('a low-confidence candidate is flagged for review in text, not colour alone', () => {
  const doc = setup();
  const host = renderReview(doc);
  const flags = host.all(n => n.className === 'fj-flag');
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

  const select = host.all(n => n.className === 'fj-category')[0];
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
  const skipped = rowsIn(host).find(row => String(row.className).includes('fj-row-state-skipped'));
  assert.ok(skipped, 'expected the skipped row');
  assert.equal(skipped.all(n => n.className === 'fj-category').length, 0);
  assert.equal(skipped.all(n => n.tagName === 'BUTTON' && n.textContent === 'Skip').length, 0);
  const box = skipped.all(n => n.className === 'fj-select')[0];
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
    if (isBatchDetailRequest(target)) {
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
    isBatchDetailRequest(url)
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
  assert.equal(headers.length, 5);
  headers.forEach(header => assert.equal(header.getAttribute('scope'), 'col'));

  const box = host.all(n => n.className === 'fj-select')[0];
  assert.match(box.getAttribute('aria-label'), /Select invoice-2026-07\.pdf/);
  const select = host.all(n => n.className === 'fj-category')[0];
  assert.match(select.getAttribute('aria-label'), /Category for invoice-2026-07\.pdf/);

  const filterButton = host.all(
    n => n.tagName === 'BUTTON' && String(n.className).startsWith('fj-filter')
  )[0];
  assert.ok(filterButton.getAttribute('aria-pressed'), 'filters expose pressed state');
});

test('an empty workspace explains what will appear rather than showing a bare table', () => {
  const doc = setup();
  renderReview(doc, null, []);
  const body = text(doc);
  assert.match(body, /Not scanned in this session/);
  assert.match(body, /Nothing moves without your approval/);
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

  doc.getElementById('fileJanitorScan').click();
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  const error = doc.getElementById('fileJanitorError');
  assert.equal(error.hidden, true, 'an empty successful scan is not an error');
  // R59: nothing waiting is said once, with the button for the next scan.
  assert.match(text(doc), /Nothing to file\. Inbox is tidy\./);
  assert.match(text(doc), /No changes were made/);
  assert.ok(
    surface(doc).all(node => node.tagName === 'BUTTON' && node.textContent === 'Scan now').length >=
      2,
    'Scan now in the header and in the state itself'
  );
  // The button is usable again.
  assert.equal(doc.getElementById('fileJanitorScan').disabled, false);
});

test('a scan with no new proposals keeps an existing review batch and reports that fact', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.renderBatch();
  globalThis.fetch = async (url, opts) => {
    if (opts && opts.method === 'POST') {
      return { ok: true, json: async () => ({ success: true, created: false }) };
    }
    return {
      ok: true,
      json: async () => ({
        batch: batchFixture(),
        candidates: candidatesFixture(),
        total: 3,
        filtered_total: 3,
        counts: { all: 3, needs_review: 1, pending: 2, skipped: 1 }
      })
    };
  };

  doc.getElementById('fileJanitorScan').click();
  await settle();
  await settle();

  assert.match(text(doc), /Scan complete — nothing new to file/);
  assert.match(text(doc), /current review is unchanged/);
  assert.match(text(doc), /invoice-2026-07\.pdf/);
  assert.equal(doc.getElementById('fileJanitorError').hidden, true);
});

test('a resolved pending batch becomes a completed-batch empty state', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.renderBatch();
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({ batch: null, candidates: [], total: 0, filtered_total: 0, counts: {} })
  });

  await panel._reloadBatch();

  assert.match(text(doc), /Nothing to file\. Inbox is tidy\./);
  assert.match(text(doc), /Every file in the last scan has a recorded result/);
  assert.match(text(doc), /View History/);
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

  const select = host.all(n => n.className === 'fj-category')[0];
  select.value = 'archives';
  select.dispatch('change');
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));

  assert.match(doc.getElementById('fileJanitorError').textContent, /not allowed/);
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

// ------------------------------------------------------------------- Trash

function trashToggleFor(doc, name) {
  const host = surface(doc);
  return host.all(n => {
    const label = String(n.getAttribute('aria-label') || '');
    return n.tagName === 'BUTTON' && label.includes(name) && label.includes('for Trash');
  })[0];
}

// ------------------------------------------------------------------- Trash

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
});

test('a file marked for Trash cannot also be selected for a move', () => {
  const doc = setup();
  renderReview(doc);
  panel._select('c1');
  trashToggleFor(doc, 'invoice-2026-07.pdf').click();

  // The move selection released it, and its checkbox is no longer selectable.
  assert.deepEqual(panel._selected(), []);
  const host = surface(doc);
  const box = host.all(n => n.className === 'fj-select')[0];
  assert.equal(box.disabled, true, 'a file bound for Trash is not a move candidate');
});

// A removal mark belongs to the batch it was made in. Carrying one into a
// different set of files is how the wrong file gets thrown away.
test('Trash marks never survive a repaint into a different batch', () => {
  const doc = setup();
  renderReview(doc);
  trashToggleFor(doc, 'payload.bin').click();
  assert.equal(trashToggleFor(doc, 'payload.bin').getAttribute('aria-pressed'), 'true');

  // A fresh batch arrives.
  renderReview(doc, batchFixture(), candidatesFixture());
  assert.equal(trashToggleFor(doc, 'payload.bin').getAttribute('aria-pressed'), 'false');
  const approve = doc.getElementById('fileJanitorApprove');
  assert.equal(approve.disabled, true, 'nothing should be pending from the previous batch');
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
    doc.getElementById('fileJanitorHistoryStatus').textContent,
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
        daily_scan_local_time: '09:00',
        content_mode: 'metadata_only'
      },
      readiness: { state: 'ready', checks: [{ component: 'directory_access', status: 'ok' }] }
    },
    overrides
  );
}

function badgeText(doc) {
  const node = doc.getElementById('fileJanitorActivity');
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
  const control = doc.getElementById('fileJanitorPause');
  assert.match(control.textContent, /Resume watching/);
  // Scanning on demand is still available while paused.
  assert.equal(doc.getElementById('fileJanitorScan').disabled, false);
});

test('the pause control explains what is kept', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());
  const control = doc.getElementById('fileJanitorPause');
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

  doc.getElementById('fileJanitorPause').click();
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

  doc.getElementById('fileJanitorScan').click();
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
  assert.match(body, /Local only · file details/);
  assert.match(body, /Names, types, sizes and dates only/);
  assert.match(body, /No file contents are read/);
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
  assert.match(body, /Cloud with consent · SomeCloud/);
  assert.match(body, /Consent required before any extract is sent/);

  const host = doc.getElementById('fileJanitorMount');
  const consentReview = host.all(
    n => n.tagName === 'BUTTON' && /Review consent/.test(n.textContent)
  )[0];
  assert.ok(consentReview, 'an unconfirmed provider must make consent the primary next step');
});

test('compact privacy distinguishes local inspection and unavailable data', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  panel.render(
    privacyStatus({
      mode: 'local_model',
      provider: 'LocalModel',
      leaves_device: false
    })
  );
  assert.match(cardText(doc), /Local only · on-device text/);
  assert.match(cardText(doc), /stay on this device with LocalModel/);

  panel.render(
    statusFixture({
      settings: {
        ...statusFixture().settings,
        content_mode: 'future_unknown_mode'
      },
      privacy: { mode: 'future_unknown_mode' }
    })
  );
  assert.match(cardText(doc), /Privacy unavailable/);
  assert.match(cardText(doc), /Open Settings to confirm/);
});

test('compact server-derived privacy remains visible in the console on every tab', () => {
  const doc = setup();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  openConsole(
    privacyStatus({
      mode: 'cloud_model',
      provider: 'SomeCloud',
      leaves_device: true,
      consent_required: false
    })
  );

  for (const tab of ['review', 'history', 'settings']) {
    panel._selectTab(tab);
    const lines = surface(doc).all(node =>
      String(node.className || '')
        .split(/\s+/)
        .includes('fj-console-privacy')
    );
    assert.equal(lines.length, 1, `${tab} keeps the shared privacy line`);
    assert.match(lines[0].textContent, /Cloud with consent · SomeCloud/);
  }
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

  const input = doc.getElementById('fileJanitorDailyTime');
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

  const status = doc.getElementById('fileJanitorSettingsStatus').textContent;
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

  const warning = doc.getElementById('fileJanitorSettingsStatus').textContent;
  assert.match(warning, /Your files stay exactly where they are/);
  assert.match(warning, /history is kept/);
  assert.match(warning, /again to confirm/);

  stop.click();
  await new Promise(r => setTimeout(r, 0));
  assert.equal(called, true, 'the second press confirms');
});

test('console tabs expose their panel and support arrow, Home, and End navigation', () => {
  const doc = setup();
  renderReview(doc);
  const event = key => ({ key, preventDefault() {} });

  let review = doc.getElementById('fileJanitorTab-review');
  assert.equal(review.getAttribute('aria-selected'), 'true');
  assert.equal(review.getAttribute('tabindex'), '0');
  assert.equal(review.getAttribute('aria-controls'), 'fileJanitorConsoleBody');
  assert.equal(doc.getElementById('fileJanitorConsoleBody').getAttribute('role'), 'tabpanel');
  assert.equal(
    doc.getElementById('fileJanitorConsoleBody').getAttribute('aria-labelledby'),
    'fileJanitorTab-review'
  );

  review.dispatch('keydown', event('ArrowRight'));
  let history = doc.getElementById('fileJanitorTab-history');
  assert.equal(panel.activeTab(), 'history');
  assert.equal(history.getAttribute('aria-selected'), 'true');
  assert.equal(doc.activeElement, history);

  history.dispatch('keydown', event('End'));
  const settings = doc.getElementById('fileJanitorTab-settings');
  assert.equal(panel.activeTab(), 'settings');
  assert.equal(doc.activeElement, settings);

  settings.dispatch('keydown', event('Home'));
  review = doc.getElementById('fileJanitorTab-review');
  assert.equal(panel.activeTab(), 'review');
  assert.equal(doc.activeElement, review);
});

test('the Settings tab reports whether it is the selected tab', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  openConsole(statusFixture());

  const tabFor = name => surface(doc).all(n => n.getAttribute('data-fj-tab') === name)[0];

  assert.equal(tabFor('settings').getAttribute('role'), 'tab');
  assert.equal(tabFor('settings').getAttribute('aria-selected'), 'false');
  assert.equal(tabFor('review').getAttribute('aria-selected'), 'true');

  tabFor('settings').click();
  assert.equal(tabFor('settings').getAttribute('aria-selected'), 'true');
  assert.equal(tabFor('review').getAttribute('aria-selected'), 'false');
  assert.ok(doc.getElementById('fileJanitorSettingsHost'), 'the settings form is on screen');
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

// The server-rendered body carries UUID identity before deferred tooling runs.
test('the panel reads its UUID from server-rendered page data', async () => {
  const doc = setup();
  panel._forgetWorkspace();
  delete globalThis.window.currentWorkspaceId;
  doc.body.dataset = { workspaceId: 'ws-42' };
  globalThis.window.location = { pathname: '/workspaces/readable-slug' };

  const requested = [];
  globalThis.fetch = async url => {
    requested.push(String(url));
    return { ok: true, json: async () => ({ status: setupRequiredStatus }) };
  };

  panel.init();
  await new Promise(r => setTimeout(r, 0));

  assert.ok(
    requested.some(url => url.includes('/api/workspaces/ws-42/file-janitor')),
    'the panel must use server-rendered UUID identity: ' + JSON.stringify(requested)
  );
  const host = surface(doc);
  assert.equal(host.hidden, false, 'the setup card must actually be shown');
  assert.match(text(doc), /Setup required|Use this folder/);
});

test('an explicit workspace id still wins over page data', async () => {
  const doc = setup();
  panel._forgetWorkspace();
  delete globalThis.window.currentWorkspaceId;
  doc.body.dataset = { workspaceId: 'ws-from-page' };
  globalThis.window.location = { pathname: '/workspaces/readable-slug' };
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
  assert.ok(!requested.some(url => url.includes('ws-from-page')));
  assert.ok(!requested.some(url => url.includes('readable-slug')));
  assert.ok(doc);
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
  assert.equal(doc.getElementById('fileJanitorPath'), null);
  assert.equal(doc.getElementById('fileJanitorConfirm'), null);
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
  assert.ok(doc.getElementById('fileJanitorPath'), 'the legacy card still renders');
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
  assert.equal(doc.getElementById('fileJanitorPath'), null);
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

// Every shipped blueprint's setup adapter must reach these renderers.
//
// This step's renderer IS the folder picker. When ownsStep did not recognize an
// adapter id, render() drew nothing and primaryLabel() returned '' — so the
// wizard fell back to its own generic "Approve and continue" and a user
// creating a workspace from the generic blueprint had NO WAY to choose a
// folder. Nothing failed; the button just approved nothing.
//
// The ids come from the manifests rather than a list here, so a new blueprint
// with a new adapter fails in this test instead of in someone's wizard.
function shippedFileJanitorAdapterIds() {
  const starterDir = join(process.cwd(), 'internal/projecttemplates/starter');
  if (!existsSync(starterDir)) return [];
  const ids = new Set();
  for (const entry of readdirSync(starterDir)) {
    const manifestPath = join(starterDir, entry, 'template.json');
    if (!existsSync(manifestPath)) continue;
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
    const declaresFileJanitor = (manifest.capabilities || []).some(
      capability => capability && capability.id === 'file-janitor'
    );
    if (!declaresFileJanitor) continue;
    for (const step of manifest.setup_wizard?.steps || []) {
      if (step && step.adapter) ids.add(step.adapter);
    }
  }
  return Array.from(ids);
}

test('every shipped blueprint adapter reaches the folder picker', () => {
  const adapters = shippedFileJanitorAdapterIds();
  assert.ok(
    adapters.length >= 2,
    `expected the Downloads preset and the generic blueprint, got ${JSON.stringify(adapters)}`
  );
  assert.ok(adapters.includes('downloads_janitor'), 'the Downloads preset must be covered');
  assert.ok(adapters.includes('file_janitor'), 'the generic blueprint must be covered');

  for (const adapter of adapters) {
    const doc = setup();
    panel._setStatus({
      ...setupRequiredStatus,
      settings: { ...setupRequiredStatus.settings, root_path: '' }
    });
    const container = doc.createElement('div');
    panel._setupSteps.directory.render(container, {
      step: { id: 'folder', kind: 'directory', adapter }
    });

    assert.match(
      container.textContent,
      /Choose folder/,
      `adapter ${adapter} rendered no folder picker`
    );
    assert.match(
      panel._setupSteps.directory.primaryLabel({ step: { adapter } }),
      /Choose a folder to continue/,
      `adapter ${adapter} fell back to the wizard's generic button`
    );
    assert.equal(
      panel._setupSteps.directory.disablePrimary({ step: { adapter } }),
      true,
      `adapter ${adapter} let the user continue without choosing a folder`
    );
  }
});

// The other half: a step belonging to some other capability's adapter must not
// be hijacked into rendering File Janitor's picker.
test('a step from another adapter is left alone', () => {
  const doc = setup();
  panel._setStatus(setupRequiredStatus);
  const container = doc.createElement('div');
  panel._setupSteps.directory.render(container, {
    step: { id: 'folder', kind: 'directory', adapter: 'some_other_capability' }
  });
  assert.equal(container.textContent, '', 'File Janitor drew into another capability’s step');
  assert.equal(panel._setupSteps.directory.primaryLabel({ step: { adapter: 'some_other' } }), '');
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

  const control = doc.getElementById('fileJanitorPause');
  assert.ok(control, 'the control stays visible rather than disappearing');
  // FR-56: the action that needs the missing approval says where the decision
  // lives instead of quietly doing it.
  assert.equal(control.textContent, 'Approve in setup');
  assert.ok(doc.getElementById('fileJanitorScan'), 'scanning on demand is unaffected');
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
  assert.equal(doc.getElementById('fileJanitorPause').textContent, 'Resume watching');
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

test('a Downloads review refuses another grant without loading its batch', async () => {
  const doc = setup();
  const route = withLocation('?panel=file-janitor&review=downloads');
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push([String(url), options?.method || 'GET']);
    if (String(url).endsWith('?review=downloads'))
      return {
        ok: false,
        json: async () => ({
          error: { message: 'Filing in this workspace uses a different folder.' }
        })
      };
    return { ok: true, json: async () => ({ status: { applies: false } }) };
  };
  try {
    panel._applyUrlState();
    await settle();
    assert.match(surface(doc).textContent, /uses a different folder/);
    assert.equal(doc.getElementById('fileJanitorScan'), null);
    assert.deepEqual(calls, [['/api/workspaces/ws-1/file-janitor?review=downloads', 'GET']]);
    panel.close();
    assert.equal(route.search(), '');
    await settle();
    assert.ok(calls.every(([, method]) => method === 'GET'));
  } finally {
    panel.close();
    clearLocation();
  }
});

test('an unconfigured install opens straight into setup, with no empty tabs', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  assert.equal(panel.isOpen(), true);
  // The real folder chooser, not a placeholder.
  assert.ok(doc.getElementById('fileJanitorPath'), 'expected the real setup surface');
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
  const dialogs = doc
    .getElementById('fileJanitorConsole')
    .all(n => n.getAttribute('role') === 'dialog');
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
  assert.equal(doc.getElementById(labelledBy).textContent, 'Filing');
});

test('a late status repaint after closing preserves focus on the live compact opener', () => {
  const doc = setup();
  panel.render(statusFixture());
  const opener = doc.getElementById('fileJanitorCardOpen');
  opener.focus();
  panel.render(statusFixture());
  const replacement = doc.getElementById('fileJanitorCardOpen');
  assert.notEqual(replacement, opener);
  assert.equal(doc.activeElement, replacement);
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

test('closing during a recommendation read lands in its region, not the competing card', () => {
  const doc = setup();
  renderReview(doc);
  const opener = doc.register('workspaceNextAction');
  const region = doc.register('workspaceNextActionRegion');
  panel.open({ source: 'workspace-next-action', trigger: opener });
  opener.parent = null;
  opener._connected = false;
  doc.byId.delete(opener.id);
  panel.close();
  assert.equal(doc.activeElement, region);
});

test('closing after a view switch never restores focus to the now-hidden Details card', () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'details' };
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());
  const cardTrigger = doc.getElementById('fileJanitorCardOpen');

  const station = new FakeElement('button');
  station.setAttribute('data-cmd-hq-station', 'file-janitor');
  doc.body.appendChild(station);
  doc.querySelector = selector =>
    selector === '[data-cmd-hq-station="file-janitor"]' ? station : null;

  panel.open({ source: 'workspace-details', trigger: cardTrigger });
  doc.body.dataset.workspaceViewMode = 'map';
  panel._syncSummaryVisibility();
  assert.equal(doc.getElementById('fileJanitorMount').hidden, true);

  panel.close();
  assert.equal(doc.activeElement, station, 'the visible station is the live equivalent entry');
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
    return {
      ok: true,
      json: async () => ({ status: statusFixture(), batch: null, candidates: [] })
    };
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

  // Consent is an explicit prerequisite and leads to the Settings control,
  // before ordinary pending work.
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render(
    statusFixture({
      privacy: {
        mode: 'cloud_model',
        provider: 'Example Cloud',
        consent_required: true
      }
    })
  );
  assert.equal(panel.stationState().value, 'Consent required');
  assert.equal(panel.stationState().actionTab, 'settings');

  // Files waiting outrank both Paused and Watching once prerequisites are met.
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

test('the station keeps product identity, managed folder, depot art, and state distinct', () => {
  setup();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  const status = statusFixture();
  status.settings.root_path = '/tmp/A very long approved inbox folder';
  panel.render(status);
  globalThis.window.WorkspaceCapabilities = {
    find: () => ({
      installed: true,
      available: true,
      status: { folder_display_name: 'Previous folder before relink' }
    })
  };
  const adapter = globalThis.window.WorkspaceBuiltinStationAdapters.find(
    entry => entry.key === 'file-janitor'
  );

  const station = adapter.station();
  assert.equal(station.label, 'Filing');
  assert.equal(station.location, 'A very long approved inbox folder');
  assert.equal(station.visualVariant, 'depot');
  assert.equal(station.state().value, '2 files ready for review');
});

test('the station sends consent-required workspaces to the existing consent control', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  panel.render(
    statusFixture({
      privacy: {
        mode: 'cloud_model',
        headline: 'Ori may inspect a short text extract.',
        detail: 'Nothing has been sent yet.',
        provider: 'Example Cloud',
        leaves_device: true,
        consent_required: true
      }
    })
  );
  globalThis.window.WorkspaceCapabilities = {
    find: () => ({ installed: true, available: true, status: {} })
  };
  const adapter = globalThis.window.WorkspaceBuiltinStationAdapters.find(
    entry => entry.key === 'file-janitor'
  );
  const station = adapter.station();
  const trigger = new FakeElement('button');
  doc.body.appendChild(trigger);

  station.action(trigger);

  assert.equal(panel.activeTab(), 'settings');
  assert.match(surface(doc).textContent, /Confirm Example Cloud/);
});

test('Workspace Details carries no review table and no settings form', () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'details' };
  renderReview(doc);
  panel.render(statusFixture());
  const card = doc.getElementById('fileJanitorMount');

  // The compact card answers "is anything waiting?" and offers a way in.
  assert.doesNotMatch(card.textContent, /File depot/);
  assert.match(card.textContent, /Filing/);
  assert.match(card.textContent, /Current status Review ready/);
  assert.match(card.textContent, /Managed folder Inbox/);
  assert.match(card.textContent, /Review queue 2 files waiting for review/);
  assert.match(card.textContent, /Privacy mode Local only/);
  assert.equal(doc.getElementById('fileJanitorCardOpen').textContent, 'Review files · 2');

  // And nothing else. A hidden second copy of the review table or the settings
  // form is exactly what this split removes: two surfaces acting on the same
  // files, one of them stale and unseen.
  assert.equal(card.all(n => n.tagName === 'TABLE').length, 0);
  assert.equal(card.all(n => String(n.className).startsWith('fj-row-item')).length, 0);
  assert.equal(card.all(n => n.id === 'fileJanitorSettingsHost').length, 0);
  assert.equal(card.all(n => n.id === 'fileJanitorBatch').length, 0);
  assert.equal(card.all(n => n.id === 'fileJanitorHistoryHost').length, 0);
});

test('the compact card keeps a 179-file paused queue review-led', () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'details' };
  const batch = batchFixture();
  batch.summary = { ...batch.summary, proposed: 179 };
  panel._setBatch(batch, candidatesFixture(), CATEGORIES);
  panel.render(statusFixture({ settings: { ...statusFixture().settings, paused: true } }));

  assert.equal(badgeText(doc), 'Review ready');
  assert.match(cardText(doc), /179 files waiting for review/);
  assert.match(cardText(doc), /automatic scanning paused/);
  assert.equal(doc.getElementById('fileJanitorCardOpen').textContent, 'Review files · 179');
});

test('compact queue actions remain explicit at zero, one, and large counts', () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'details' };

  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());
  assert.equal(doc.getElementById('fileJanitorCardOpen').textContent, 'Open review');
  assert.equal(badgeText(doc), 'Watching');

  for (const count of [1, 12345]) {
    const batch = batchFixture();
    batch.summary = { ...batch.summary, proposed: count };
    panel._setBatch(batch, candidatesFixture(), CATEGORIES);
    panel.render(statusFixture());
    assert.equal(doc.getElementById('fileJanitorCardOpen').textContent, `Review files · ${count}`);
    assert.match(cardText(doc), new RegExp(`${count} files? waiting for review`));
  }
});

test('a batch loaded after status refreshes the compact review action', async () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'details' };
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());
  assert.equal(doc.getElementById('fileJanitorCardOpen').textContent, 'Open review');

  globalThis.fetch = async url => {
    const target = String(url);
    if (isBatchDetailRequest(target)) {
      return {
        ok: true,
        json: async () => ({
          batch: batchFixture(),
          candidates: candidatesFixture(),
          total: 2,
          filtered_total: 2,
          counts: { all: 2, needs_review: 1, pending: 2, skipped: 1 }
        })
      };
    }
    if (target.includes('/categories')) {
      return { ok: true, json: async () => ({ categories: CATEGORIES }) };
    }
    return { ok: true, json: async () => ({ status: statusFixture() }) };
  };

  panel.open({ source: 'workspace-details', tab: 'review' });
  await settle();
  await settle();

  assert.equal(doc.getElementById('fileJanitorCardOpen').textContent, 'Review files · 2');
  assert.equal(badgeText(doc), 'Review ready');
});

test('a review-labelled Details action explicitly leaves remembered History for Review', () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'details' };
  panel._setBatch(null, [], CATEGORIES);
  panel.render(statusFixture());
  panel.open({ source: 'test', tab: 'history' });
  panel._selectTab('history');
  panel.close();

  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render(statusFixture());
  const action = doc.getElementById('fileJanitorCardOpen');
  assert.equal(action.textContent, 'Review files · 2');
  action.click();

  assert.equal(panel.activeTab(), 'review');
});

test('the compact card sends consent-required work to Settings', () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'details' };
  panel._setBatch(null, [], CATEGORIES);
  panel.render(
    statusFixture({
      privacy: {
        mode: 'cloud_model',
        headline: 'Ori may read a short extract.',
        detail: 'Nothing has been sent yet.',
        provider: 'Example Cloud',
        leaves_device: true,
        consent_required: true
      }
    })
  );

  assert.equal(badgeText(doc), 'Consent required');
  assert.match(cardText(doc), /Privacy mode Cloud with consent · Example Cloud/);
  assert.match(cardText(doc), /Consent required before any extract is sent/);
  const action = doc.getElementById('fileJanitorCardOpen');
  assert.equal(action.textContent, 'Review consent');
  action.click();
  assert.equal(panel.activeTab(), 'settings');
});

test('the never-configured summary hides only when Next action actually shows the setup ask', () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'details' };
  const next = doc.register('workspaceNextActionRegion');
  next.setAttribute('data-filing-setup-ask', 'true');
  panel.render(setupRequiredStatus);
  const card = doc.getElementById('fileJanitorMount');
  assert.equal(card.hidden, true);
  // A ticket outranks setup; the fallback card is now the only setup ask.
  next.setAttribute('data-filing-setup-ask', 'false');
  panel._syncSummaryVisibility();
  assert.equal(card.hidden, false);
  panel._setSetupReview(attachedReview());
  panel.render(setupRequiredStatus);
  assert.match(card.textContent, /Synthetic Inbox · not allowed yet/);
  assert.match(
    card.textContent,
    /Filing is not allowed in Synthetic Inbox yet. Nothing is scanned or moved./
  );
  next.setAttribute('data-filing-setup-ask', 'true');
  panel.render(statusFixture());
  assert.equal(card.hidden, false, 'after setup the summary stays');
  panel.render({ ...setupRequiredStatus, settings: { access_revoked_at: '2026-11-14T12:00:00Z' } });
  assert.equal(card.hidden, false, 'revoked access keeps its fallback');
  panel.render({ ...setupRequiredStatus, readiness: { state: 'needs_attention' } });
  assert.equal(card.hidden, false, 'repair keeps its fallback');
});

test('the configured summary is visible only in Details across status refreshes', () => {
  const doc = setup();
  const status = statusFixture();
  const card = doc.getElementById('fileJanitorMount');

  doc.body.dataset = { workspaceViewMode: 'details' };
  panel.render(status);
  assert.equal(card.hidden, false, 'Details owns the compact fallback summary');

  for (const mode of ['map', 'tickets', 'dashboard']) {
    doc.body.dataset.workspaceViewMode = mode;
    // A status refresh is the path that used to set hidden=false regardless of
    // the active view, resurrecting the full-width banner over Map.
    panel.render(status);
    assert.equal(card.hidden, true, `${mode} must not inherit the Details summary`);
  }
});

test('slow capability status cannot reveal the summary in a direct Map entry', () => {
  const doc = setup();
  doc.body.dataset = { workspaceViewMode: 'map' };
  const card = doc.getElementById('fileJanitorMount');

  panel.render({ applies: false });
  assert.equal(card.hidden, true);
  // This render represents the delayed status response arriving after Command
  // has already resolved ?mode=map.
  panel.render(statusFixture());
  assert.equal(card.hidden, true, 'late installed state is not presentation permission');
  assert.ok(card.children.length > 0, 'the Details fallback remains ready for a later view switch');
});

test('the summary falls back to Details when Workspace Command is unavailable', () => {
  const doc = setup();
  delete doc.body.dataset;
  panel.render(statusFixture());
  assert.equal(doc.getElementById('fileJanitorMount').hidden, false);
});

test('the console header carries the real scan and pause controls', () => {
  const doc = setup();
  renderReview(doc);
  const header = surface(doc);
  assert.ok(header.all(n => n.id === 'fileJanitorScan')[0], 'expected a real Scan now');
  assert.ok(header.all(n => n.id === 'fileJanitorPause')[0], 'expected a real Pause');
  assert.match(surface(doc).textContent, /2 files ready for review/);
});

test('before setup the header offers no scan or pause', () => {
  const doc = setup();
  openConsole(setupRequiredStatus);
  // Neither applies to a folder that has not been chosen; offering them would
  // suggest File Janitor is already doing something.
  assert.equal(surface(doc).all(n => n.id === 'fileJanitorScan').length, 0);
  assert.equal(surface(doc).all(n => n.id === 'fileJanitorPause').length, 0);
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
  assert.equal(doc.getElementById('fileJanitorError').hidden, true, 'not an error');
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
    if (!isBatchDetailRequest(target)) {
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
test('a failed batch read is unavailable, not an empty folder, and keeps stale data inert', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render(statusFixture());
  panel.renderBatch();

  globalThis.fetch = async () => ({ ok: false, json: async () => ({}) });
  await panel._reloadBatch();

  const host = surface(doc);
  assert.match(host.textContent, /Review unavailable/);
  assert.match(host.textContent, /Showing the last known scan/);
  assert.match(host.textContent, /invoice-2026-07\.pdf/);
  assert.doesNotMatch(host.textContent, /Nothing to review/);
  assert.equal(host.all(node => node.className === 'fj-select')[0].disabled, true);
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
  assert.ok(host.all(node => node.tagName === 'BUTTON' && node.textContent === 'Retry')[0]);
});

test('an assistant first-review link loads its exact immutable batch', async () => {
  const doc = setup();
  renderReview(doc);
  let requested = '';
  globalThis.fetch = async url => {
    requested = String(url);
    return {
      ok: true,
      json: async () => ({
        batch: batchFixture(),
        candidates: candidatesFixture(),
        total: 2,
        filtered_total: 2,
        counts: { all: 2, needs_review: 0, pending: 2, skipped: 0 }
      })
    };
  };
  panel._setLinkedBatchForTest('batch-1');
  await panel._reloadBatch();
  assert.match(requested, /\/batches\/batch-1\?/);
  panel._setLinkedBatchForTest('');
});

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

  const next = surface(doc).all(n => n.id === 'fileJanitorPageNext')[0];
  assert.ok(next, 'expected a Next control');
  assert.equal(
    surface(doc).all(n => n.id === 'fileJanitorPagePrev')[0].disabled,
    true,
    'Previous is unavailable on the first page'
  );

  next.click();
  await settle();

  assert.match(surface(doc).textContent, /Showing 51/);
  assert.equal(rowsIn(surface(doc)).length, 50);
  assert.equal(surface(doc).all(n => n.id === 'fileJanitorPagePrev')[0].disabled, false);
});

test('a shrunken last page refetches the real final page instead of stranding an empty view', async () => {
  const doc = setup();
  renderReview(doc);
  globalThis.fetch = pagedFetch(120);
  await settle();
  await panel._reloadBatch();

  surface(doc)
    .all(n => n.id === 'fileJanitorPageNext')[0]
    .click();
  await settle();
  surface(doc)
    .all(n => n.id === 'fileJanitorPageNext')[0]
    .click();
  await settle();
  assert.match(surface(doc).textContent, /Showing 101–120/);

  const offsets = [];
  const shrunkFetch = pagedFetch(75);
  globalThis.fetch = async url => {
    if (isBatchDetailRequest(url)) {
      const params = new URLSearchParams(String(url).split('?')[1] || '');
      offsets.push(Number(params.get('offset')) || 0);
    }
    return shrunkFetch(url);
  };
  await panel._reloadBatch();

  assert.deepEqual(offsets, [100, 50]);
  assert.equal(rowsIn(surface(doc)).length, 25);
  assert.match(surface(doc).textContent, /Showing 51–75 of 75 files/);
  assert.doesNotMatch(surface(doc).textContent, /No files match this filter/);
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

  surface(doc)
    .all(n => n.id === 'fileJanitorPageNext')[0]
    .click();
  await settle();

  assert.deepEqual(
    panel._selected().sort(),
    ['c0', 'c1'],
    'turning a page must not discard decisions already made'
  );
  panel._select('c50');
  assert.deepEqual(
    panel._selected().sort(),
    ['c0', 'c1', 'c50'],
    'the selection may intentionally span server pages'
  );
  assert.ok(doc.getElementById('fileJanitorApprove'), 'the button stays on every page');
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
    isBatchDetailRequest(url)
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
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
});

// A different batch invalidates every decision made against the old one.
test('a new batch clears selections from the old one', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();
  panel._select('c1');

  globalThis.fetch = async url =>
    isBatchDetailRequest(url)
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
    isBatchDetailRequest(url)
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

  const box = flaggedRow.all(n => n.className === 'fj-select')[0];
  assert.equal(box.disabled, true, 'a flagged row must not be selectable on the guess');
  assert.match(box.getAttribute('title') || '', /Choose a category/i);

  // A confident row beside it is unaffected.
  const confidentRow = rows.find(r => r.textContent.includes('invoice-2026-07.pdf'));
  assert.equal(confidentRow.all(n => n.className === 'fj-select')[0].disabled, false);
});

test('a prior group destination does not count as an individual answer to Needs review', () => {
  const doc = setup();
  const candidates = [
    { ...candidatesFixture()[1], decision_category: 'documents', decision_from_group: true }
  ];
  renderReview(doc, batchFixture(), candidates);
  const box = surface(doc).all(node => node.className === 'fj-select')[0];
  assert.equal(box.disabled, true);
  assert.match(box.getAttribute('title'), /Choose a category/);
});

test('choosing a category makes a flagged row selectable and keeps focus', async () => {
  const doc = setup();
  renderReview(doc);
  await settle();
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  panel.render(statusFixture());
  panel.renderBatch();

  const resolved = candidatesFixture().map(candidate =>
    candidate.id === 'c2' ? { ...candidate, decision_category: 'documents' } : candidate
  );
  globalThis.fetch = async (_url, options) =>
    options?.method === 'POST'
      ? { ok: true, json: async () => ({}) }
      : {
          ok: true,
          json: async () => ({
            batch: batchFixture(),
            candidates: resolved,
            total: resolved.length,
            filtered_total: resolved.length,
            counts: { all: resolved.length, needs_review: 1, pending: 2, skipped: 1 }
          })
        };

  const before = rowsIn(surface(doc)).find(row => row.textContent.includes('payload.bin'));
  const category = before.all(node => node.className === 'fj-category')[0];
  category.focus();
  category.value = 'documents';
  category.dispatch('change');
  await settle();
  await settle();

  const flaggedRow = rowsIn(surface(doc)).find(row => row.textContent.includes('payload.bin'));
  assert.equal(
    flaggedRow.all(node => node.className === 'fj-select')[0].disabled,
    false,
    'the user has now said where it goes'
  );
  assert.equal(
    doc.activeElement,
    flaggedRow.all(node => node.className === 'fj-category')[0],
    'the server repaint must not drop focus from the decision control'
  );
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
          {
            component: 'directory_access',
            status: 'failed',
            message: 'Folder is missing.',
            repair: 'relink_folder'
          },
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
  assert.ok(doc.getElementById('fileJanitorAddCurator'), 'expected an add control');
  restore();
});

// Only the server's verified current attachment establishes availability.
test('Settings reports built-in guidance for the verified Curator', () => {
  const doc = setup();
  const restore = withCatalog({
    id: 'file-janitor',
    owned_resources: [{ kind: 'companion_agent', id: 'agent-1' }]
  });
  panel._setBatch(null, [], CATEGORIES);
  openConsole(
    statusFixture({
      guidance: {
        bundled: true,
        available: true,
        agent_instance_id: 'agent-1',
        agent_name: 'Downloads Manager'
      }
    })
  );
  panel._selectTab('settings');

  const body = text(doc);
  assert.match(body, /Downloads Manager is helping in this workspace/);
  assert.match(body, /cannot approve, move, or delete/);
  assert.match(body, /Built-in filing and backup guidance is available/);
  assert.match(body, /not an enabled user skill or permission/);
  assert.equal(doc.getElementById('fileJanitorAddCurator'), null, 'no second Curator offer');
  restore();
});

test('stale companion resource offers explicit association, not guidance availability', async () => {
  const doc = setup();
  const restore = withCatalog({
    id: 'file-janitor',
    owned_resources: [{ kind: 'companion_agent', id: 'gone' }]
  });
  panel._setBatch(null, [], CATEGORIES);
  openConsole(
    statusFixture({
      guidance: {
        bundled: true,
        available: false,
        candidates: [{ id: 'actual-id', name: 'My helper' }]
      }
    })
  );
  panel._selectTab('settings');
  assert.doesNotMatch(text(doc), /Built-in filing and backup guidance is available/);
  assert.match(text(doc), /could not verify a companion association/);
  const select = doc.getElementById('fileJanitorCuratorCandidate');
  select.value = 'actual-id';
  let posted;
  globalThis.fetch = async (url, opts) => {
    if (String(url).endsWith('/companion')) {
      posted = JSON.parse(opts.body);
      return { ok: true, json: async () => ({ display_name: 'My helper' }) };
    }
    return { ok: true, json: async () => ({ status: statusFixture() }) };
  };
  doc.getElementById('fileJanitorAssociateCurator').click();
  await settle();
  assert.deepEqual(posted, { agent_instance_id: 'actual-id' });
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

  doc.getElementById('fileJanitorAddCurator').click();
  await settle();

  assert.ok(posted, 'expected a request');
  assert.equal(posted.method, 'POST');
  assert.match(posted.url, /\/capabilities\/file-janitor\/companion$/);
  assert.match(doc.getElementById('fileJanitorSettingsStatus').textContent, /File Curator/);
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

  doc.getElementById('fileJanitorAddCurator').click();
  await settle();

  assert.match(
    doc.getElementById('fileJanitorSettingsStatus').textContent,
    /Agents are unavailable/
  );
  restore();
});

// ------------------------------------------------------------------ removal

const REMOVAL_SUMMARY = {
  capability_id: 'file-janitor',
  display_name: 'Filing',
  installed: true,
  managed_folder: 'Inbox',
  stops_automation: ['Watching this folder for new files.', 'The daily catch-up scan.'],
  retained_audit: ['The history of everything Filing filed, trashed, or restored.'],
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

  assert.ok(doc.getElementById('fileJanitorRemove'), 'expected a Remove Filing control');
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

  doc.getElementById('fileJanitorRemove').click();
  await settle();

  assert.ok(asked, 'expected the dry-run request');
  assert.match(asked, /\/capabilities\/file-janitor\/removal$/);
  const body = text(doc);
  assert.match(body, /stop managing Inbox/);
  assert.match(body, /Watching this folder for new files/);
  assert.match(body, /gives up its access/);
  assert.match(body, /No files are moved, renamed, deleted, or restored/);
  assert.match(body, /history of everything Filing filed/);
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

  doc.getElementById('fileJanitorRemove').click();
  await settle();
  doc.getElementById('fileJanitorRemoveCancel').click();

  assert.equal(deleted, false, 'cancelling must not remove anything');
  assert.ok(doc.getElementById('fileJanitorRemove'), 'back to the entry point');
  assert.equal(doc.getElementById('fileJanitorRemoveConfirm'), null);
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

  doc.getElementById('fileJanitorRemove').click();
  await settle();
  assert.ok(doc.getElementById('fileJanitorRemoveCompanion'), 'expected a separate choice');
  assert.equal(
    doc.getElementById('fileJanitorRemoveCompanion').checked,
    false,
    'the companion choice must default to off'
  );

  doc.getElementById('fileJanitorRemoveConfirm').click();
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
          reason: 'This agent existed before Filing was installed, so removing it leaves it alone.'
        }
      }
    })
  });

  doc.getElementById('fileJanitorRemove').click();
  await settle();

  assert.equal(doc.getElementById('fileJanitorRemoveCompanion'), null);
  assert.match(text(doc), /existed before Filing was installed/);
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

  doc.getElementById('fileJanitorRemove').click();
  await settle();
  doc.getElementById('fileJanitorRemoveConfirm').click();
  await settle();

  assert.equal(panel.isOpen(), false, 'the console must not linger over a removed capability');
  assert.equal(doc.getElementById('fileJanitorMount').hidden, true, 'the card goes too');
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

  doc.getElementById('fileJanitorRemove').click();
  await settle();
  doc.getElementById('fileJanitorRemoveConfirm').click();
  await settle();

  assert.match(
    doc.getElementById('fileJanitorSettingsStatus').textContent,
    /could not stop the background work/
  );
  assert.equal(panel.isOpen(), true, 'the console stays open after a failed removal');
  restore();
});

function deferredReply() {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, reply: (body, ok = true) => resolve({ ok, json: async () => body }) };
}

const receiptFixture = (id, completed_at) => ({
  id,
  completed_at,
  result: 'applied',
  operation: 'move',
  undo: 'available',
  source_name: 'private.txt',
  source_path: '/private/source',
  destination_relative: 'Filed/private.txt'
});

test('public action state distinguishes cold, failed, absent and stale capability reads', async () => {
  setup();
  assert.equal(panel.getActionState().applies, null);
  assert.equal(panel.getActionState().status.state, 'loading');
  globalThis.fetch = async () => {
    throw new Error('offline');
  };
  await panel.refresh();
  assert.equal(panel.getActionState().applies, null);
  assert.equal(panel.getActionState().status.state, 'unavailable');
  panel.render({ applies: false });
  assert.equal(panel.getActionState().status.state, 'not_applicable');
  const pending = deferredReply();
  globalThis.fetch = () => pending.promise;
  const refresh = panel.refresh();
  assert.deepEqual(panel.getActionState().status, { state: 'loading', stale: true });
  pending.reply({ status: { applies: false } });
  await refresh;
  assert.equal(panel.getActionState().status.state, 'not_applicable');
});

test('public snapshot is immutable, excludes paths, and uses the real manual-mode rule', async () => {
  setup();
  const status = statusFixture();
  status.settings.root_id = 'root-1';
  status.settings.paused = true;
  panel.render(status);
  panel._setBatch(batchFixture(), candidatesFixture());
  assert.equal(panel.getActionState().mode, 'paused');
  status.settings.setup_decision = { mode: 'manual', root_id: 'root-1' };
  assert.equal(panel.getActionState().mode, 'manual');
  status.settings.automation_approved_at = '2026-10-01T00:00:00Z';
  assert.equal(panel.getActionState().mode, 'paused');
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({ actions: [receiptFixture('receipt', '2026-10-02T00:00:00Z')] })
  });
  await panel._reloadHistory();
  const snapshot = panel.getActionState();
  assert.equal(snapshot.workspaceId, 'ws-1');
  assert.equal(snapshot.pendingCount, 2);
  assert.equal(snapshot.latestReceipt.undoable, true);
  assert.doesNotMatch(
    JSON.stringify(snapshot),
    /private|source_path|root_path|directory_reference/
  );
  assert.throws(() => {
    snapshot.latestReceipt.id = 'tampered';
  }, TypeError);
  assert.throws(() => {
    snapshot.actionKinds.push('scan');
  }, TypeError);
});

test('latest receipt reads unfiltered later pages and compares completion time, not journal order', async () => {
  setup();
  panel.render(statusFixture());
  const requests = [];
  globalThis.fetch = async url => {
    requests.push(url);
    return {
      ok: true,
      json: async () =>
        url.includes('offset=2')
          ? { actions: [receiptFixture('a', '2026-10-03T00:00:00Z')], total: 3 }
          : {
              actions: [
                receiptFixture('z', '2026-10-03T00:00:00Z'),
                receiptFixture('old', '2026-10-01T00:00:00Z')
              ],
              total: 3
            }
    };
  };
  await panel._reloadHistory();
  assert.equal(panel.getActionState().latestReceipt.id, 'a');
  // History also asks for its one "Last filing" line, a separate read.
  const historyReads = requests.filter(url => /\/history/.test(url));
  assert.equal(historyReads.length, 2);
  assert.match(historyReads[1], /offset=2/);
  assert.equal(requests.filter(url => /\/filings\?limit=1$/.test(url)).length, 1);
});

test('batch loading qualifies stale counts synchronously and ignores an older response', async () => {
  setup();
  panel.render(statusFixture());
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  const first = deferredReply();
  const second = deferredReply();
  let calls = 0;
  globalThis.fetch = () => (++calls === 1 ? first : second).promise;
  const seen = [];
  panel.subscribe(snapshot => seen.push(snapshot));
  const old = panel._reloadBatch();
  assert.deepEqual(panel.getActionState().batch, { state: 'loading', stale: true });
  await settle();
  const latest = panel._reloadBatch();
  await settle();
  second.reply({ batch: batchFixture({ id: 'new' }), candidates: [] });
  await latest;
  first.reply({ batch: batchFixture({ id: 'old' }), candidates: [] });
  await old;
  assert.equal(panel.getActionState().batchId, 'new');
  assert.ok(seen.every(snapshot => snapshot.workspaceId === 'ws-1'));
  assert.equal(seen.at(-1).actionState.batch.state, 'ready');
});

test('root changes invalidate in-flight batch/history and cached receipts', async () => {
  setup();
  panel.render(statusFixture());
  panel._setBatch(batchFixture(), candidatesFixture(), CATEGORIES);
  const pending = deferredReply();
  globalThis.fetch = () => pending.promise;
  const batch = panel._reloadBatch();
  const history = panel._reloadHistory();
  await settle();
  const changed = statusFixture();
  changed.settings.root_id = 'new-root';
  panel.render(changed);
  pending.reply({
    batch: batchFixture(),
    actions: [receiptFixture('old', '2026-10-01T00:00:00Z')]
  });
  await Promise.all([batch, history]);
  assert.equal(panel.getActionState().rootId, 'new-root');
  assert.equal(panel.getActionState().batchId, '');
  assert.equal(panel.getActionState().latestReceipt, null);
  assert.equal(panel.getActionState().pendingCount, null);
});

test('teardown rejects status/history callbacks and detaches subscribers', async () => {
  setup();
  panel.render(statusFixture());
  const pending = deferredReply();
  globalThis.fetch = () => pending.promise;
  let notifications = 0;
  panel.subscribe(() => notifications++);
  const status = panel.refresh();
  const history = panel._reloadHistory();
  panel.dispose();
  const atDispose = notifications;
  pending.reply({
    status: statusFixture(),
    actions: [receiptFixture('late', '2026-10-01T00:00:00Z')]
  });
  await Promise.all([status, history]);
  assert.equal(notifications, atDispose);
  assert.equal(panel.getActionState().latestReceipt, null);
  assert.equal(panel.getActionState().applies, null);
});

test('workspace switch rejects old status and uses only the new identity', async () => {
  setup();
  const pending = deferredReply();
  globalThis.fetch = url =>
    url.includes('/ws-2/')
      ? Promise.resolve({ ok: true, json: async () => ({ status: { applies: false } }) })
      : pending.promise;
  panel.init('ws-1');
  panel.init('ws-2');
  await settle();
  pending.reply({ status: statusFixture() });
  await settle();
  assert.equal(panel.getActionState().workspaceId, 'ws-2');
  assert.equal(panel.getActionState().applies, false);
});

test('history retry keeps last known receipt qualified and ignores out-of-order history', async () => {
  setup();
  panel.render(statusFixture());
  const first = deferredReply();
  const second = deferredReply();
  let calls = 0;
  globalThis.fetch = () => (++calls === 1 ? first : second).promise;
  const old = panel._reloadHistory();
  const latest = panel._reloadHistory();
  second.reply({ actions: [receiptFixture('latest', '2026-10-02T00:00:00Z')] });
  await latest;
  first.reply({ actions: [receiptFixture('old', '2026-10-01T00:00:00Z')] });
  await old;
  assert.equal(panel.getActionState().latestReceipt.id, 'latest');
  globalThis.fetch = async () => {
    throw new Error('offline');
  };
  const retry = panel._reloadHistory();
  assert.deepEqual(panel.getActionState().history, { state: 'loading', stale: true });
  await retry;
  assert.deepEqual(panel.getActionState().history, { state: 'unavailable', stale: true });
  assert.equal(panel.getActionState().latestReceipt.id, 'latest');
});

test('scan publishes start/end and cannot return into a disposed workspace', async () => {
  const doc = setup();
  openConsole(statusFixture());
  await settle();
  const pending = deferredReply();
  globalThis.fetch = () => pending.promise;
  const seen = [];
  panel.subscribe(snapshot => seen.push(snapshot.actionState.scanning));
  doc.getElementById('fileJanitorScan').click();
  assert.equal(seen.at(-1), true);
  panel.dispose();
  pending.reply({ created: true });
  await settle();
  assert.equal(panel.getActionState().scanning, false);
  assert.equal(seen.length, 1, 'no callback into disposed subscribers');
});

test('History filters and filtered failures do not change canonical latest result or availability', async () => {
  const doc = setup();
  openConsole(statusFixture(), 'history');
  await settle();
  const canonical = receiptFixture('canonical', '2026-10-04T00:00:00Z');
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ actions: [canonical] }) });
  await panel._reloadHistory();
  const before = panel.getActionState();
  globalThis.fetch = async url => {
    assert.match(url, /operation=trash/);
    throw new Error('filtered read failed');
  };
  surface(doc)
    .all(node => node.tagName === 'BUTTON' && node.textContent === 'Trashed')[0]
    .click();
  assert.deepEqual(panel.getActionState().history, before.history);
  await settle();
  assert.deepEqual(panel.getActionState().latestReceipt, before.latestReceipt);
  assert.deepEqual(panel.getActionState().history, before.history);
  assert.equal(panel.getActionState().historyList.state, 'unavailable');
  assert.match(text(doc), /Retry history/);
});

test('a list filter cannot cancel an in-flight canonical receipt refresh', async () => {
  const doc = setup();
  openConsole(statusFixture(), 'history');
  await settle();
  const pending = deferredReply();
  globalThis.fetch = url =>
    url.includes('operation=trash')
      ? Promise.resolve({ ok: true, json: async () => ({ actions: [] }) })
      : pending.promise;
  const refresh = panel._reloadHistory();
  surface(doc)
    .all(node => node.tagName === 'BUTTON' && node.textContent === 'Trashed')[0]
    .click();
  await settle();
  pending.reply({ actions: [receiptFixture('canonical', '2026-10-04T00:00:00Z')] });
  await refresh;
  assert.equal(panel.getActionState().history.state, 'ready');
  assert.equal(panel.getActionState().latestReceipt.id, 'canonical');
});

test('latest receipt summarizes partial outcomes from the same approval only', async () => {
  setup();
  panel.render(statusFixture());
  const success = {
    ...receiptFixture('last-success', '2026-10-04T00:00:00Z'),
    idempotency_key: 'approval-private'
  };
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({
      actions: [
        success,
        {
          ...receiptFixture('failed', '2026-10-03T00:00:00Z'),
          idempotency_key: 'approval-private',
          result: 'failed',
          undo: 'unavailable'
        },
        {
          ...receiptFixture('other', '2026-10-01T00:00:00Z'),
          idempotency_key: 'different',
          result: 'failed'
        }
      ]
    })
  });
  await panel._reloadHistory();
  assert.deepEqual(panel.getActionState().latestReceipt.counts, {
    applied: 1,
    failed: 1,
    stale: 0
  });
  assert.doesNotMatch(JSON.stringify(panel.getActionState()), /approval-private|idempotency/);
});

test('a receipt destination clears History filters and exposes a canonical later-page result', async () => {
  const doc = setup();
  openConsole(statusFixture(), 'history');
  await settle();
  globalThis.fetch = async url => ({
    ok: true,
    json: async () => (url.includes('/history') ? { actions: [] } : { status: statusFixture() })
  });
  surface(doc)
    .all(node => node.tagName === 'BUTTON' && node.textContent === 'Trashed')[0]
    .click();
  await settle();
  panel.close();
  const urls = [];
  globalThis.fetch = async url => {
    urls.push(url);
    return {
      ok: true,
      json: async () =>
        url.includes('/history')
          ? url.includes('offset=1')
            ? {
                total: 2,
                actions: [
                  {
                    ...receiptFixture('latest', '2026-10-04T00:00:00Z'),
                    source_name: 'linked-latest.txt'
                  }
                ]
              }
            : { total: 2, actions: [receiptFixture('first-page', '2026-10-01T00:00:00Z')] }
          : { status: statusFixture() }
    };
  };
  panel.open({ source: 'workspace-next-action', tab: 'history', item: 'latest' });
  await settle();
  assert.ok(urls.filter(url => url.includes('/history')).every(url => !url.includes('operation=')));
  assert.match(text(doc), /linked-latest.txt/);
});

test('failed explicit scans publish a failure without inventing an empty result', async () => {
  const doc = setup();
  openConsole(statusFixture());
  await settle();
  globalThis.fetch = async () => {
    throw new Error('scan offline');
  };
  doc.getElementById('fileJanitorScan').click();
  await settle();
  assert.equal(panel.getActionState().scanState, 'failed');
  assert.notEqual(panel.getActionState().emptyState, 'scan_empty');
  assert.match(text(doc), /scan offline/);
});

test('scan completion publishes its new batch and end state without hiding pending review', async () => {
  const doc = setup();
  openConsole(statusFixture());
  await settle();
  const pending = deferredReply();
  globalThis.fetch = url =>
    url.endsWith('/scan')
      ? pending.promise
      : Promise.resolve({
          ok: true,
          json: async () => ({ batch: batchFixture(), candidates: [], categories: CATEGORIES })
        });
  const seen = [];
  panel.subscribe(snapshot => seen.push(snapshot.actionState));
  doc.getElementById('fileJanitorScan').click();
  assert.equal(panel.getActionState().scanning, true);
  pending.reply({ created: true });
  await settle();
  assert.equal(seen.at(-1).scanning, false);
  assert.equal(seen.at(-1).pendingCount, 2);
  assert.ok(
    seen.some(state => state.scanning && state.batch.state === 'ready' && state.pendingCount === 2)
  );
});

async function groupedReviewFixture(count = 206) {
  const doc = setup();
  const status = statusFixture();
  status.settings.root_id = 'root-one';
  const calls = [];
  const batch = batchFixture({ summary: { total: count, proposed: count } });
  const candidates = Array.from({ length: count }, (_, i) => ({
    ...candidatesFixture()[0],
    id: 'group-member-' + i,
    name: 'image-' + i + '.png',
    category: 'images',
    destination: 'Filed/Images'
  }));
  const review = {
    batch_id: batch.id,
    revision: 'a'.repeat(64),
    rule_version: 1,
    counts: { candidates: count, filing: count, questions: 0 },
    groups: [
      {
        id: 'group-images',
        kind: 'filing',
        label: 'Images',
        count,
        category: 'images',
        destination: 'Filed/Images',
        reason: 'Saved proposed image category',
        samples: ['<img onerror=alert(1)>.png', 'image-1.png', 'image-2.png']
      }
    ]
  };
  const state = {
    // review is the server's grouping of the scan; the picture is drawn from it.
    review,
    failPicture: false,
    staleDraftOnce: false,
    batch,
    batches: [batch],
    generation: 0,
    // A scan nobody has touched opens with its confident folder ticked.
    choices: candidates.map(candidate => ({
      candidate_id: candidate.id,
      operation: 'move',
      source_group_id: 'group-images'
    })),
    failNextEdit: false,
    refuseBounds: false,
    refuseMemberChanged: false,
    removedStale: 0,
    couldNot: [],
    decorateRow: null,
    sectionFiles: null,
    editGate: null,
    // The plan check and the File request, answered like the server's.
    planCalls: 0,
    planGate: null,
    failPlan: false,
    overLimit: false,
    renamed: new Set(),
    changed: [],
    fileRequests: [],
    fileGate: null,
    // After a press: the batch has nothing left, or cannot be read again.
    emptied: false,
    failBatchRead: false,
    // What a scan answers, when a test needs a scan to find something new.
    scanReply: null,
    // The filings the server journaled, newest last, and how Undo all answers.
    filings: [],
    looseNow: 7,
    receiptGone: false,
    undoGate: null,
    undoReply: null,
    forgetFails: false
  };
  // What filing would do right now for the saved choices on screen.
  const planView = ids => {
    const draft = draftView();
    const moves = draft.choices.filter(choice => choice.operation === 'move');
    const trash = draft.choices.filter(choice => choice.operation === 'trash');
    const line = choice => {
      const candidate = candidates.find(item => item.id === choice.candidate_id);
      const renamed = state.renamed.has(candidate.id);
      return {
        candidate_id: candidate.id,
        name: candidate.name,
        operation: choice.operation,
        category: choice.category || undefined,
        final_name: renamed ? candidate.name.replace('.png', ' (2).png') : candidate.name,
        renamed: renamed || undefined,
        size: 10
      };
    };
    const asked = ids ? new Set(ids.split(',')) : null;
    return {
      batch_id: state.batch.id,
      revision: draft.revision,
      group_revision: draft.group_revision,
      plan_digest: state.overLimit ? '' : 'd'.repeat(60) + draft.revision.slice(-4),
      move_count: moves.length,
      trash_count: trash.length,
      total: moves.length + trash.length,
      over_limit: state.overLimit,
      destinations: [],
      trash: { count: trash.length, first: [] },
      changed: { total: state.changed.length, items: state.changed },
      items: asked ? draft.choices.filter(choice => asked.has(choice.candidate_id)).map(line) : []
    };
  };
  // A press that went through: every planned file filed, reported like the journal.
  state.fileReply = () => {
    const draft = draftView();
    // The destination is the file's own relative reference, as the server reports it.
    const outcomes = draft.choices.map(choice => {
      const name = candidates.find(item => item.id === choice.candidate_id).name;
      return {
        candidate_id: choice.candidate_id,
        name,
        operation: choice.operation,
        result: 'applied',
        destination: choice.operation === 'move' ? 'Filed/Images/' + name : ''
      };
    });
    for (const choice of draft.choices) {
      const candidate = candidates.find(item => item.id === choice.candidate_id);
      candidate.state = 'applied';
    }
    state.choices = [];
    state.review.counts = { candidates: count, filing: 0, questions: 0, applied: outcomes.length };
    state.review.groups = [];
    state.generation++;
    state.emptied = candidates.every(candidate => candidate.state !== 'pending');
    state.filings.push({
      id: 'filing-' + state.fileRequests.length,
      names: outcomes.filter(outcome => outcome.operation === 'move').map(outcome => outcome.name),
      trashed: outcomes.filter(outcome => outcome.operation === 'trash').length,
      undone: false
    });
    return {
      ok: true,
      json: async () => ({
        success: true,
        filing: {
          filing_id: 'filing-' + state.fileRequests.length,
          filed: outcomes.filter(outcome => outcome.operation === 'move').length,
          trashed: outcomes.filter(outcome => outcome.operation === 'trash').length,
          failed: 0,
          stale: 0,
          not_attempted: 0,
          outcomes
        }
      })
    };
  };
  const draftView = () => {
    const choices = state.choices.map(choice => ({
      ...choice,
      group_id: 'group-images',
      category:
        choice.operation === 'move'
          ? candidates.find(candidate => candidate.id === choice.candidate_id)?.decision_category ||
            'images'
          : ''
    }));
    const categories = new Set(
      candidates.map(candidate => candidate.decision_category || candidate.category)
    );
    return {
      batch_id: state.batch.id,
      revision: state.generation.toString(16).padStart(64, '0'),
      group_revision: state.review.revision,
      state: state.generation ? 'saved' : 'default',
      removed_stale: state.removedStale,
      choices,
      groups: state.review.groups.map(group => ({
        id: group.id,
        eligible: count - choices.filter(choice => choice.operation === 'trash').length,
        selected: choices.filter(choice => choice.operation === 'move').length,
        group_marked: choices.filter(choice => choice.source_group_id === group.id).length,
        trash: choices.filter(choice => choice.operation === 'trash').length,
        category: categories.size === 1 ? [...categories][0] : '',
        mixed_destinations: categories.size > 1
      }))
    };
  };
  // The server's placement of each waiting file, for the fixture's one group.
  const pictureLists = () => {
    const group = state.review.groups[0];
    const pending = group ? candidates.filter(candidate => candidate.state === 'pending') : [];
    const chosen = new Map(state.choices.map(choice => [choice.candidate_id, choice]));
    const moved = pending.filter(candidate => chosen.get(candidate.id)?.operation === 'move');
    const filing = group?.kind !== 'question';
    const rest = pending.filter(candidate => !chosen.has(candidate.id));
    return {
      group,
      pending,
      moved,
      filing,
      filed: filing ? moved : [],
      unfiled: filing && !moved.length ? rest : [],
      left_out: filing && moved.length ? rest : [],
      trash: pending.filter(candidate => chosen.get(candidate.id)?.operation === 'trash'),
      loose: filing ? [] : rest
    };
  };
  const pictureView = () => {
    const draft = draftView();
    const lists = pictureLists();
    const entries = (items, placement, limit) =>
      items.slice(0, limit).map(candidate => ({
        id: candidate.id,
        name: candidate.name,
        size: 10,
        placement
      }));
    // One card per destination that has a file in it. The fixture's one filing group
    // is Images; a file the person sent elsewhere (or a question row's files) makes
    // a card of its own, without a tick.
    const categoryOf = candidate => candidate.decision_category || 'images';
    const byCategory = new Map();
    const into = (items, key) => {
      for (const candidate of items) {
        const entry = byCategory.get(categoryOf(candidate)) || { filed: [], unfiled: [] };
        entry[key].push(candidate);
        byCategory.set(categoryOf(candidate), entry);
      }
    };
    into(lists.filing ? lists.filed : lists.moved, 'filed');
    into(lists.unfiled, 'unfiled');
    const folders = [...byCategory].map(([category, entry]) => {
      const ticked = lists.filing && category === 'images';
      return {
        category,
        destination: 'Filed/' + category.charAt(0).toUpperCase() + category.slice(1),
        group_id: ticked ? lists.group.id : '',
        group_state: !ticked
          ? 'none'
          : !lists.moved.length
            ? 'unticked'
            : lists.moved.length < lists.pending.length
              ? 'partial'
              : 'ticked',
        count: entry.filed.length,
        files: entries(entry.filed, 'filed', 5).map(item => ({ ...item, category })),
        unfiled_count: entry.unfiled.length,
        unfiled_files: entries(entry.unfiled, 'unfiled', 5)
      };
    });
    const placedCategories = new Set(lists.moved.map(categoryOf));
    const loose =
      !lists.filing && lists.group
        ? [
            {
              group_id: lists.group.id,
              key: lists.group.key || 'sensitive',
              count: lists.pending.length,
              placed_count: lists.moved.length,
              placed_category: placedCategories.size === 1 ? [...placedCategories][0] : '',
              trash_count: lists.trash.length,
              loose_count: lists.loose.length,
              singles: entries(lists.loose, 'loose', 12),
              pairs: [],
              bundles: [],
              more: Math.max(0, lists.loose.length - 12)
            }
          ]
        : [];
    // A test that wants pair or bundle cards draws them over the one row.
    if (state.decorateRow)
      for (let i = 0; i < loose.length; i++) loose[i] = state.decorateRow(loose[i], lists);
    const view = {
      batch_id: state.batch.id,
      revision: state.review.revision,
      draft_revision: draft.revision,
      draft_state: draft.state,
      rule_version: 3,
      counts: state.review.counts,
      waiting: state.review.counts.filing + state.review.counts.questions,
      removed_stale: 0,
      content_read: 'none',
      folders,
      loose,
      left_out: { count: lists.left_out.length, files: entries(lists.left_out, 'left_out', 12) },
      trash: { count: lists.trash.length, files: entries(lists.trash, 'trash', 12) },
      could_not_file: {
        count: state.couldNot.length,
        files: state.couldNot.map(candidate => ({
          id: candidate.id,
          name: candidate.name,
          size: 10,
          state: 'failed',
          reason: 'Ori could not move this file.',
          can_retry: true
        }))
      }
    };
    if (state.staleDraftOnce) {
      // A change in another window landed between the picture read and the
      // saved-choices read: the two name different revisions.
      state.staleDraftOnce = false;
      view.draft_revision = 'f'.repeat(64);
    }
    return view;
  };
  globalThis.fetch = async (url, options) => {
    const target = String(url);
    calls.push({ target, options });
    const reply = body => ({ ok: true, json: async () => body });
    if (state.scanReply && /\/scan$/.test(target)) return reply(state.scanReply);
    // The receipt, the list for History's line, Undo all and "forget", as the
    // server answers them.
    const filingsRoute = target.match(
      /\/filings(?:\/([^/?]+))?(\/undo|\/forget-restored)?(\?.*)?$/
    );
    if (filingsRoute) {
      const [, id, action] = filingsRoute;
      const filing = state.filings.find(item => item.id === id);
      const summary = item => ({
        filing_id: item.id,
        state: 'done',
        admitted_at: new Date().toISOString(),
        completed_at: new Date().toISOString(),
        filed: item.names.length,
        trashed: item.trashed,
        not_moved: 0,
        undo_available: !item.undone,
        undone: item.undone
      });
      if (!id) return reply({ success: true, filings: state.filings.slice(-1).map(summary) });
      if (action === '/forget-restored') {
        if (state.forgetFails)
          return {
            ok: false,
            status: 500,
            json: async () => ({ error: { message: 'Ori could not forget those files.' } })
          };
        return reply({ success: true, forgotten: filing ? filing.names.length : 0 });
      }
      if (action === '/undo') {
        if (state.undoGate) await state.undoGate;
        if (state.undoReply) return state.undoReply(filing);
        if (!filing || filing.undone)
          return {
            ok: false,
            status: 409,
            json: async () => ({
              error: { code: 'undo_unavailable', message: 'This filing cannot be undone.' }
            })
          };
        filing.undone = true;
        return reply({
          success: true,
          undo: {
            filing_id: filing.id,
            back: filing.names.length,
            not_back: 0,
            outcomes: filing.names.map(name => ({ name, result: 'undone' }))
          }
        });
      }
      if (!filing || state.receiptGone) return { ok: false, status: 404, json: async () => ({}) };
      return reply({
        success: true,
        receipt: {
          ...summary(filing),
          by_destination: [
            { category: 'images', destination: 'Filed/Images', count: filing.names.length }
          ],
          not_moved: [],
          not_moved_total: 0,
          not_attempted: 0,
          loose_now: state.looseNow,
          history_trimmed: false
        }
      });
    }
    if (target.includes('/picture/files?')) {
      const query = new URLSearchParams(target.split('?')[1]);
      const view = pictureView();
      if (
        query.get('revision') !== view.revision ||
        query.get('draft_revision') !== view.draft_revision
      )
        return { ok: false, status: 409, json: async () => ({ code: 'review_changed' }) };
      const section = query.get('section');
      const list = pictureLists()[section] || [];
      const offset = Number(query.get('offset'));
      const limit = Number(query.get('limit'));
      if (state.sectionFiles?.[section]) {
        // A card's whole list, each member with where it is now.
        const all = state.sectionFiles[section]();
        return reply({
          page: {
            batch_id: state.batch.id,
            revision: view.revision,
            draft_revision: view.draft_revision,
            section,
            key: query.get('key'),
            total: all.length,
            limit,
            offset,
            files: all.slice(offset, offset + limit)
          }
        });
      }
      return reply({
        page: {
          batch_id: state.batch.id,
          revision: view.revision,
          draft_revision: view.draft_revision,
          section,
          key: query.get('key'),
          total: list.length,
          limit,
          offset,
          files: list.slice(offset, offset + limit).map(candidate => ({
            id: candidate.id,
            name: candidate.name,
            size: 10,
            placement: section
          }))
        }
      });
    }
    if (target.endsWith('/picture'))
      return state.failPicture
        ? { ok: false, json: async () => ({}) }
        : reply({ picture: pictureView(), can_reveal: Boolean(state.canReveal) });
    if (target.includes('/reveal?') || target.includes('/open?')) return reply({ success: true });
    if (target.endsWith('/draft')) {
      if (options?.method !== 'POST') return reply({ draft: draftView() });
      if (state.editGate) await state.editGate;
      if (state.refuseMemberChanged)
        return {
          ok: false,
          status: 409,
          json: async () => ({
            code: 'review_member_changed',
            message: 'Ori could not tick that: a file changed since the scan.'
          })
        };
      if (state.refuseBounds)
        return {
          ok: false,
          status: 413,
          json: async () => ({
            code: 'review_bounds',
            message: 'These saved choices exceed the review limit.'
          })
        };
      if (state.failNextEdit) {
        state.failNextEdit = false;
        return {
          ok: false,
          status: 500,
          json: async () => ({ message: 'Synthetic save failure' })
        };
      }
      const request = JSON.parse(options.body);
      if (
        request.expected_revision !== draftView().revision ||
        request.expected_group_revision !== state.review.revision
      )
        return {
          ok: false,
          status: 409,
          json: async () => ({ message: 'Synthetic concurrent edit conflict' })
        };
      const chosen = new Map(state.choices.map(choice => [choice.candidate_id, choice]));
      switch (request.action) {
        case 'select_group':
        case 'select_filing_groups':
          if (state.review.groups[0]?.kind === 'question') {
            // A question row is placed with one destination; its own files follow a
            // later choice, a file with a choice of its own does not.
            for (const candidate of candidates) {
              const prior = chosen.get(candidate.id);
              if (
                candidate.state === 'pending' &&
                (!prior || prior.source_group_id === 'group-images')
              ) {
                candidate.decision_category = request.category;
                chosen.set(candidate.id, {
                  candidate_id: candidate.id,
                  operation: 'move',
                  source_group_id: 'group-images'
                });
              }
            }
            break;
          }
          for (const candidate of candidates)
            if (!chosen.has(candidate.id))
              chosen.set(candidate.id, {
                candidate_id: candidate.id,
                operation: 'move',
                source_group_id: 'group-images'
              });
          break;
        case 'select_files':
          for (const id of request.candidate_ids) {
            if (request.category)
              candidates.find(candidate => candidate.id === id).decision_category =
                request.category;
            chosen.set(id, { candidate_id: id, operation: 'move', source_group_id: '' });
          }
          break;
        case 'retry_files':
          for (const id of request.candidate_ids) {
            candidates.find(candidate => candidate.id === id).state = 'pending';
            state.couldNot = state.couldNot.filter(candidate => candidate.id !== id);
            chosen.delete(id);
          }
          break;
        case 'exclude_files':
        case 'unmark_trash':
          for (const id of request.candidate_ids) chosen.delete(id);
          break;
        case 'deselect_group':
          for (const [id, choice] of chosen) if (choice.operation === 'move') chosen.delete(id);
          break;
        case 'clear_group':
          for (const [id, choice] of chosen)
            if (choice.source_group_id === request.group_id) chosen.delete(id);
          break;
        case 'keep_group':
        case 'keep_files': {
          const ids =
            request.action === 'keep_group'
              ? candidates
                  .filter(candidate => candidate.state === 'pending')
                  .map(candidate => candidate.id)
              : request.candidate_ids;
          for (const candidate of candidates)
            if (ids.includes(candidate.id)) {
              candidate.state = 'skipped';
              candidate.skip_remembered = true;
              chosen.delete(candidate.id);
            }
          const pending = candidates.filter(candidate => candidate.state === 'pending').length;
          state.review.counts.kept = count - pending;
          state.review.counts.filing = state.review.groups[0]?.kind === 'question' ? 0 : pending;
          state.review.counts.questions = state.review.groups[0]?.kind === 'question' ? pending : 0;
          state.review.groups = pending ? [{ ...state.review.groups[0], count: pending }] : [];
          break;
        }
        case 'reconsider_files':
          for (const candidate of candidates)
            if (request.candidate_ids.includes(candidate.id)) candidate.skip_remembered = false;
          break;
        case 'clear_all':
          chosen.clear();
          break;
        case 'mark_trash':
          chosen.set(request.candidate_ids[0], {
            candidate_id: request.candidate_ids[0],
            operation: 'trash'
          });
          break;
        case 'set_category':
          for (const candidate of candidates) {
            const choice = chosen.get(candidate.id);
            if (
              request.group_id
                ? choice?.source_group_id === request.group_id
                : request.candidate_ids.includes(candidate.id)
            ) {
              candidate.decision_category = request.category;
              if (choice && !request.group_id) choice.source_group_id = '';
            }
          }
          break;
        default:
          throw new Error('Unexpected mock draft action: ' + request.action);
      }
      state.choices = [...chosen.values()];
      state.generation++;
      return reply({ draft: draftView() });
    }
    if (target.includes('/groups/') && target.includes('/members?')) {
      // The files still waiting in the row, as the server lists them.
      const query = new URLSearchParams(target.split('?')[1]);
      const pending = candidates.filter(candidate => candidate.state === 'pending');
      const offset = Number(query.get('offset'));
      const limit = Number(query.get('limit'));
      return reply({
        batch_id: state.batch.id,
        revision: state.review.revision,
        total: pending.length,
        offset,
        limit,
        candidates: pending.slice(offset, offset + limit).map(candidate => ({ id: candidate.id }))
      });
    }
    if (target.includes('/plan?')) {
      state.planCalls++;
      // The answer is worked out when the question arrives; a gate only delays it,
      // so a slow answer can arrive after a newer one (out of order).
      const gate = state.planGate;
      state.planGate = null;
      const query = new URLSearchParams(target.split('?')[1]);
      const draft = draftView();
      const answer = state.failPlan
        ? { ok: false, status: 500, json: async () => ({}) }
        : query.get('revision') !== draft.revision ||
            query.get('group_revision') !== draft.group_revision
          ? { ok: false, status: 409, json: async () => ({ code: 'review_conflict' }) }
          : reply({ plan: planView(query.get('ids')) });
      if (gate) await gate;
      return answer;
    }
    if (target.endsWith('/file')) {
      const request = JSON.parse(options.body);
      state.fileRequests.push(request);
      if (state.fileGate) await state.fileGate;
      return state.fileReply(request);
    }
    if (target.includes('/batches?'))
      return reply({ batches: state.batches, total: state.batches.length });
    if (isBatchDetailRequest(target)) {
      if (state.failBatchRead) return { ok: false, json: async () => ({}) };
      if (state.emptied) return reply({ batch: null, candidates: [] });
      const query = new URLSearchParams(target.split('?')[1]);
      const filter = query.get('filter');
      const offset = Number(query.get('offset'));
      const matching = filter
        ? candidates.filter(candidate => candidate.state === filter)
        : candidates;
      return reply({
        batch: state.batch,
        inspection_revision: state.inspectionRevision,
        candidates: matching.slice(offset, offset + 50),
        total: count,
        filtered_total: matching.length,
        counts: {
          all: count,
          pending: candidates.filter(candidate => candidate.state === 'pending').length,
          skipped: candidates.filter(candidate => candidate.state === 'skipped').length
        }
      });
    }
    return reply({ status, categories: CATEGORIES });
  };
  panel.render(status);
  panel.open({ source: 'folder-preparation', tab: 'review' });
  await panel._reloadBatch();
  // The plan check follows the picture; ask for it now rather than after its debounce.
  await panel._planNow();
  return { doc, calls, state, candidates, draftView };
}

// After a change the tests made through the console, the plan check runs again;
// this waits for it the way the page would, without the debounce.
async function planSettled() {
  await settle();
  await panel._planNow();
  await settle();
}

test('canonical protection return restores a pinned older batch from winning server intent, never browser selections', async t => {
  const { doc, state, calls } = await groupedReviewFixture(3);
  assert.equal(panel._selected().length, 3, 'the folder opens ticked');
  state.choices = [{ candidate_id: 'group-member-2', operation: 'move', source_group_id: '' }];
  state.generation++;
  state.batches.unshift({ ...state.batch, id: 'newer-batch' });
  const trip = {
    nonce: 'return-one',
    workspaceId: 'ws-1',
    rootGeneration: 'root-one',
    bindingId: 'a'.repeat(64),
    batchId: state.batch.id,
    moves: ['group-member-0'],
    trash: [],
    filter: '',
    page: 0
  };
  let current = trip;
  window.FileJanitorProtection = {
    read: () => current,
    consume: nonce => {
      if (current?.nonce !== nonce) return null;
      const saved = current;
      current = null;
      return saved;
    },
    scrub() {},
    isDismissed: () => true,
    sentence: protectionSentence
  };
  t.after(() => {
    delete window.FileJanitorProtection;
  });
  const original = globalThis.fetch;
  globalThis.fetch = async (url, opts) =>
    String(url).endsWith('/protection')
      ? {
          ok: true,
          json: async () => ({
            workspace_id: 'ws-1',
            root_generation: 'root-one',
            binding_id: 'a'.repeat(64),
            state: 'unknown',
            destination: { kind: 'chooser' }
          })
        }
      : original(url, opts);
  calls.length = 0;
  await panel.restoreProtectionTrip(trip);
  await settle();
  assert.deepEqual(panel._selected(), ['group-member-2']);
  assert.equal(doc.getElementById('fileJanitorConfirmApply'), null);
  assert.match(surface(doc).textContent, /No old selections or approval were replayed/);
  assert.equal(current, null);
  assert.ok(!calls.some(call => call.target.includes('/batches/latest')));
  assert.ok(!calls.some(call => call.options?.method === 'POST'));
});

test('unsupported saved lifecycle states are visible, not completed or fake all-clear', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  // Nothing is pending, so nothing is ticked and nothing is drawn as a folder.
  state.choices = [];
  state.review.groups = [];
  state.review.counts = { candidates: 3, filing: 0, questions: 0, unknown: 3 };
  state.batch.state = 'resolved';
  state.batch.summary.proposed = 0;
  await panel._reloadBatch();
  assert.match(surface(doc).textContent, /saved state this version does not understand/);
  assert.doesNotMatch(surface(doc).textContent, /Review complete|All clear/);
  assert.deepEqual(panel._selected(), []);
});

// ----------------------------------------------- the picture of the folder
//
// The Review tab draws the folder as it will look after filing. These tests
// drive the console against a stub that answers like the server's picture read;
// the pure drawing rules have their own tests in file-janitor-picture.test.js.

function folderCheckbox(doc, category = 'images') {
  return doc.getElementById('fileJanitorFolder-' + category);
}

function changeCheckbox(node, checked) {
  node.checked = checked;
  node.dispatch('change');
}

function pictureHost(doc) {
  return doc.getElementById('fileJanitorBatch');
}

function buttonNamed(root, label) {
  return root.all(node => node.tagName === 'BUTTON' && node.textContent === label)[0];
}

test('the picture is the default view: lead line, frame title, a ticked folder, five names', async () => {
  const { doc, calls } = await groupedReviewFixture();
  const batch = pictureHost(doc);
  // No separate progress, picker or view switch interrupts the picture.
  assert.deepEqual(
    batch.children.filter(node => node.className !== 'fj-footer').map(node => node.className),
    ['fj-picture-host']
  );
  const picture = batch.children[0].children[0];
  assert.equal(picture.id, 'fileJanitorPicture');
  assert.deepEqual(
    picture.children.map(node => node.className),
    ['fj-lead', 'fj-decision-summary', 'fj-frame']
  );
  assert.equal(
    picture.children[0].textContent,
    'This is how Inbox will look. Nothing moves until you press File. Ori reads names, types, sizes and dates, never what is inside.'
  );
  assert.equal(picture.children[2].children[0].textContent, 'Inbox, after filing');
  assert.equal(doc.getElementById('fileJanitorJumpFiled').textContent, '206 ready to file');
  assert.match(picture.children[1].textContent, /0 need a decision/);
  const images = folderCheckbox(doc);
  assert.equal(images.checked, true);
  assert.equal(images.getAttribute('aria-label'), 'Images, 206 files, will be filed');
  assert.deepEqual(
    surface(doc)
      .all(node => node.className === 'fj-folder-files')[0]
      .children.map(node => node.textContent),
    ['image-0.png', 'image-1.png', 'image-2.png', 'image-3.png', 'image-4.png']
  );
  assert.equal(buttonNamed(surface(doc), 'and 201 more').id, 'fileJanitorMore-filed-images');
  assert.equal(rowsIn(surface(doc)).length, 0, 'the picture must not render every member');
  // The folder opens ticked; the footer still files it through today's preview.
  assert.equal(panel._selected().length, 206);
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, false);
  // The old card grid, plan sidebar and counting text are gone from this view.
  for (const gone of [
    /Your selected changes/,
    /Select all filing groups/,
    /candidates in this saved batch/,
    /Not now/
  ])
    assert.doesNotMatch(surface(doc).textContent, gone);
  // Opening writes nothing and starts nothing.
  assert.ok(calls.every(call => !call.options?.method || call.options.method === 'GET'));
  assert.ok(calls.every(call => !/curator|agent|scan|preview|apply/.test(call.target)));
});

test('All files stays reachable from the header, keeps the saved-review picker, and returns', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.batches = [batchFixture({ id: 'batch-new' }), state.batch];
  await panel._reloadBatch();
  assert.equal(doc.getElementById('fileJanitorBatchChoice'), null, 'the picture has no picker');
  // In the picture the header offers All files; the Picture/All files switch is
  // not above the picture.
  assert.equal(doc.getElementById('fileJanitorView-groups'), null, 'no switch above the picture');
  const header = doc.getElementById('fileJanitorView-all');
  assert.match(header.className, /fj-btn/);
  header.click();
  assert.equal(rowsIn(surface(doc)).length, 3);
  // In All files the same id is the switch, pressed, and the picker is here.
  assert.match(doc.getElementById('fileJanitorView-all').className, /fj-filter/);
  assert.equal(doc.getElementById('fileJanitorView-all').getAttribute('aria-pressed'), 'true');
  const picker = doc.getElementById('fileJanitorBatchChoice');
  assert.ok(picker.children.some(option => option.value === 'batch-new'));
  assert.ok(picker.children.some(option => option.value === 'batch-1' && option.selected));
  doc.getElementById('fileJanitorView-groups').click();
  assert.ok(folderCheckbox(doc));
  assert.match(doc.getElementById('fileJanitorView-all').className, /fj-btn/);
});

test('with no saved review the header offers no All files control, and shows it once one loads', () => {
  const doc = setup();
  panel._setBatch(null, [], CATEGORIES);
  panel._reviewView('groups'); // the picture, as in production
  openConsole(statusFixture(), 'review');
  // Hidden, not merely disabled: there is nothing to list, and a control that
  // looks present but cannot be used would stall anything that probes for it.
  assert.equal(doc.getElementById('fileJanitorView-all').hidden, true);
  panel._setBatch(batchFixture(), candidatesFixture().slice(0, 1), CATEGORIES);
  panel._reviewView('groups');
  assert.equal(doc.getElementById('fileJanitorView-all').hidden, false);
});

test('and N more opens the whole list 50 at a time, pinned to the picture, and returns focus', async () => {
  const { doc, calls } = await groupedReviewFixture();
  doc.getElementById('fileJanitorMore-filed-images').click();
  await settle();
  const panelNode = doc.getElementById('fileJanitorMorePanel');
  assert.match(panelNode.textContent, /Showing 1–50 of 206/);
  assert.equal(panelNode.all(node => node.tagName === 'LI').length, 50);
  assert.equal(doc.activeElement.id, 'fileJanitorMorePanel');
  buttonNamed(surface(doc), 'Next files').click();
  await settle();
  assert.match(surface(doc).textContent, /Showing 51–100 of 206/);
  const paged = calls.filter(call => call.target.includes('/picture/files?'));
  assert.ok(paged[1].target.includes('offset=50'));
  assert.ok(paged.every(call => /revision=[a]{64}&draft_revision=[0-9a-f]{64}/.test(call.target)));
  // The list survives a trip to All files and back; closing returns to its trigger.
  doc.getElementById('fileJanitorView-all').click();
  doc.getElementById('fileJanitorView-groups').click();
  assert.match(surface(doc).textContent, /Showing 51–100 of 206/);
  buttonNamed(surface(doc), 'Close list').click();
  assert.equal(doc.activeElement.id, 'fileJanitorMore-filed-images');
  assert.equal(doc.getElementById('fileJanitorMorePanel'), null);
  assert.ok(calls.every(call => !call.options?.method || call.options.method === 'GET'));
});

test('an open list stays open across a save and reads the same page against the new choices', async () => {
  const { doc } = await groupedReviewFixture();
  doc.getElementById('fileJanitorMore-filed-images').click();
  await settle();
  assert.match(surface(doc).textContent, /Showing 1–50 of 206/);
  // Leave one file out from All files; the picture's choices change.
  doc.getElementById('fileJanitorView-all').click();
  const row = rowsIn(surface(doc)).find(
    row => row.getAttribute('data-candidate-id') === 'group-member-1'
  );
  changeCheckbox(row.all(node => node.className === 'fj-select')[0], false);
  await settle();
  doc.getElementById('fileJanitorView-groups').click();
  await settle();
  assert.ok(doc.getElementById('fileJanitorMorePanel'), 'the list was not closed by the save');
  assert.match(surface(doc).textContent, /Showing 1–50 of 205/);
});

test('a list closes when a save empties it, instead of saying 0 of 0', async () => {
  const { doc } = await groupedReviewFixture();
  doc.getElementById('fileJanitorMore-filed-images').click();
  await settle();
  assert.ok(doc.getElementById('fileJanitorMorePanel'));
  changeCheckbox(folderCheckbox(doc), false); // the open list's own folder
  await settle();
  assert.equal(doc.getElementById('fileJanitorMorePanel'), null);
  assert.doesNotMatch(surface(doc).textContent, /0 of 0/);
  const card = surface(doc).all(node => node.getAttribute('data-fj-folder') === 'images')[0];
  assert.match(card.className, /is-unticked/);
});

test('a list the server refuses says so and starts over from the saved review', async () => {
  const { doc, state } = await groupedReviewFixture();
  doc.getElementById('fileJanitorMore-filed-images').click();
  await settle();
  // Another window changed the saved choices after the list was drawn.
  state.generation++;
  const original = globalThis.fetch;
  globalThis.fetch = async (url, options) =>
    String(url).includes('/picture/files?')
      ? { ok: false, status: 409, json: async () => ({ code: 'review_changed' }) }
      : original(url, options);
  buttonNamed(surface(doc), 'Next files').click();
  await settle();
  assert.match(surface(doc).textContent, /could not be loaded/);
  assert.equal(
    doc.getElementById('fileJanitorMorePanel').all(node => node.tagName === 'LI').length,
    0
  );
  globalThis.fetch = original;
  buttonNamed(surface(doc), 'Retry').click();
  await settle();
  assert.equal(doc.getElementById('fileJanitorMorePanel'), null, 'a stale list is dropped');
  assert.ok(folderCheckbox(doc));
});

test('an unavailable picture is not drawn as a tidy folder and can retry without writes', async () => {
  const { doc, calls, state } = await groupedReviewFixture();
  state.failPicture = true;
  await panel._loadReviewPicture();
  const text = surface(doc).textContent;
  assert.match(text, /picture of your folder is unavailable/);
  assert.match(text, /does not mean the folder is tidy/);
  assert.equal(folderCheckbox(doc), null);
  assert.equal(doc.getElementById('fileJanitorPicture'), null);
  state.failPicture = false;
  buttonNamed(surface(doc), 'Retry').click();
  await settle();
  assert.ok(folderCheckbox(doc));
  assert.ok(calls.every(call => !call.options?.method || call.options.method === 'GET'));
});

// A3: a folder of any size is drawn as one card with five names and a count of the
// rest, never with every member, and one press files all of it up to the limit.
for (const count of [1, 5, 6, 50, 51, 200, 206, 1000]) {
  test(`a ${count}-file folder is one card, five names at most, and filed as ${count} (matrix)`, async () => {
    const { doc } = await groupedReviewFixture(count);
    const card = doc.getElementById('fileJanitorFolder-images');
    assert.ok(card, 'the folder card is drawn and ticked');
    assert.equal(card.checked, true);
    const named = surface(doc).all(
      node => node.className === 'fj-file-name' && node.id?.startsWith('fileJanitorFileRow-')
    );
    assert.equal(named.length, Math.min(count, 5), 'five names at most');
    if (count > 5) {
      assert.equal(
        buttonNamed(surface(doc), 'and ' + (count - 5) + ' more').id,
        'fileJanitorMore-filed-images'
      );
    } else {
      assert.equal(
        surface(doc).all(node => /^and \d+ more$/.test(node.textContent || '')).length,
        0,
        'nothing is left to open'
      );
    }
    assert.equal(rowsIn(surface(doc)).length, 0, 'the picture never draws every member');
    assert.equal(panel._selected().length, count);
    assert.equal(
      doc.getElementById('fileJanitorApprove').textContent,
      count === 1 ? 'File 1 file' : 'File ' + count + ' files'
    );
    assert.equal(doc.getElementById('fileJanitorApprove').disabled, false);
  });
}

test('a folder with nothing in it is tidy and offers a scan, not an empty frame (matrix, 0 files)', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  const filed = state.fileReply;
  state.fileReply = request => {
    const answer = filed(request);
    state.emptied = false;
    return answer;
  };
  pressButton(doc).click();
  await settle();
  await settle();
  await planSettled();
  assert.match(doc.getElementById('fileJanitorPicture').textContent, /Nothing to file\./);
  assert.equal(pressButton(doc).textContent, 'No changes selected');
});

test('a change is said to a screen reader from what the picture now shows (R66)', async () => {
  const { doc } = await groupedReviewFixture(3);
  const live = doc.getElementById('fileJanitorAnnounce');
  assert.equal(live.getAttribute('role'), 'status');
  assert.equal(live.getAttribute('aria-live'), 'polite');
  assert.equal(live.textContent, '', 'nothing is said before anything changes');
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  await settle();
  assert.equal(
    doc.getElementById('fileJanitorAnnounce').textContent,
    '3 files no longer set to be filed in Images.'
  );
  changeCheckbox(folderCheckbox(doc), true);
  await settle();
  await settle();
  assert.equal(doc.getElementById('fileJanitorAnnounce').textContent, '3 files moved to Images.');
  // The live region is not part of what is redrawn after a change.
  assert.equal(doc.getElementById('fileJanitorAnnounce'), live);
});

test('nothing waiting says the folder is tidy with a Scan now button, only for a picture that was read (R59)', async () => {
  const { doc, calls, state } = await groupedReviewFixture(3);
  // The server keeps the finished review as the open one after everything is filed.
  const filed = state.fileReply;
  state.fileReply = request => {
    const answer = filed(request);
    state.emptied = false;
    return answer;
  };
  pressButton(doc).click();
  await settle();
  await settle();
  await planSettled();
  const picturePanel = doc.getElementById('fileJanitorPicture');
  assert.ok(picturePanel, 'the tidy state stands where the picture was');
  assert.match(picturePanel.textContent, /Nothing to file\. Inbox is tidy\./);
  assert.equal(pressButton(doc).textContent, 'No changes selected');
  assert.equal(pressButton(doc).disabled, true);
  assert.equal(folderCheckbox(doc), null, 'no folder card is drawn');
  // Its one button asks for a scan, and nothing else.
  const before = calls.length;
  surface(doc)
    .all(node => node.tagName === 'BUTTON' && node.textContent === 'Scan now')
    .at(-1)
    .click();
  await settle();
  assert.deepEqual(
    calls
      .slice(before)
      .filter(call => call.options?.method === 'POST')
      .map(call => call.target.replace(/^.*\/file-janitor/, '')),
    ['/scan']
  );
  // A picture that could not be read never says it (R61).
  state.failPicture = true;
  await panel._loadReviewPicture();
  assert.doesNotMatch(text(doc), /Inbox is tidy/);
  assert.match(text(doc), /does not mean the folder is tidy/);
});

test('files the person told Ori to leave are not called tidy', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.batch.summary = { ...state.batch.summary, skipped: 2 };
  state.review.counts = { candidates: 3, filing: 0, questions: 0 };
  state.review.groups = [];
  state.choices = [];
  await panel._reloadBatch();
  await settle();
  const panelText = doc.getElementById('fileJanitorPicture').textContent;
  assert.match(panelText, /Nothing to file\. 2 files stay where you left them\./);
  assert.doesNotMatch(panelText, /tidy/);
});

// R58: the picture never uses these words, anywhere on the Review tab: not the
// picture, the footer, the receipt or the Undone view. A scan is a "scan".
const RETIRED_WORDS =
  /\b(?:batch(?:es)?|candidates?|proposals?|exact(?:ly)?|explicitly|drafts?)\b/i;

function retiredWordsIn(doc) {
  const found = text(doc).match(new RegExp(RETIRED_WORDS.source, 'gi'));
  return found ? [...new Set(found.map(word => word.toLowerCase()))] : [];
}

test('the Review tab never uses a retired word: picture, footer, receipt and Undone view (R58)', async () => {
  const { doc, state } = await groupedReviewFixture(206);
  assert.deepEqual(retiredWordsIn(doc), [], 'the picture with its footer');
  // The Trash tick and the changed-since-you-looked notice live in the footer too.
  state.choices[0] = { candidate_id: 'group-member-0', operation: 'trash' };
  state.generation++;
  await panel._reloadBatch();
  await panel._planNow();
  assert.deepEqual(retiredWordsIn(doc), [], 'with a file marked for Trash');
  changeCheckbox(doc.getElementById('fileJanitorTrashAck'), true);
  pressButton(doc).click();
  await settle();
  await settle();
  assert.ok(doc.getElementById('fileJanitorResultsSummary'), 'the receipt is showing');
  assert.deepEqual(retiredWordsIn(doc), [], 'the receipt');
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  assert.ok(doc.getElementById('fileJanitorUndoneTitle'), 'the Undone view is showing');
  assert.deepEqual(retiredWordsIn(doc), [], 'the Undone view');
});

test('the All files list and the empty and error states use no retired word either (R58)', async () => {
  const { doc, state } = await groupedReviewFixture(5);
  doc.getElementById('fileJanitorView-all').click();
  await settle();
  await settle();
  assert.match(text(doc), /image-0\.png/, 'the list is showing');
  assert.deepEqual(retiredWordsIn(doc), [], 'the All files list');
  // A review that cannot be read, a saved edit that failed, and a finished review.
  state.failBatchRead = true;
  await panel._reloadBatch();
  assert.match(text(doc), /Review unavailable/);
  assert.deepEqual(retiredWordsIn(doc), [], 'a review that could not be read');
  state.failBatchRead = false;
  state.emptied = true;
  await panel._reloadBatch();
  assert.deepEqual(retiredWordsIn(doc), [], 'a finished review');
});

test('the picture and the saved choices are read again once if they disagree', async () => {
  const { doc, calls, state } = await groupedReviewFixture(3);
  state.staleDraftOnce = true;
  calls.length = 0;
  await panel._reloadBatch();
  assert.equal(calls.filter(call => call.target.endsWith('/picture')).length, 2);
  assert.ok(folderCheckbox(doc));
  assert.equal(folderCheckbox(doc).disabled, false, 'the matching pair is actionable');
  // The two reads straddled a change from somewhere else, and the page says so (R63).
  assert.match(text(doc), /Updated from another window\./);
});

test('a newer scan is one line with one button, and the open picture is never swapped by itself (R62)', async () => {
  const { doc, calls, state } = await groupedReviewFixture(3);
  assert.equal(doc.getElementById('fileJanitorNewerScan'), null, 'no newer scan yet');
  const newer = batchFixture({
    id: 'batch-newer',
    started_at: '2099-01-01T00:00:00Z',
    summary: { total: 3, proposed: 3 }
  });
  state.batches = [newer, state.batch];
  calls.length = 0;
  await panel._reloadBatch();
  // Still the scan that was open; the line says a newer one exists.
  assert.ok(!calls.some(call => call.target.includes('/batches/latest')));
  const line = doc.getElementById('fileJanitorNewerScan');
  assert.ok(line, 'the newer scan is named');
  assert.match(line.textContent, /A newer scan is ready\./);
  assert.equal(doc.getElementById('fileJanitorShowNewerScan').textContent, 'Show the newer scan');
  assert.equal(panel._selected().length, 3, 'the open picture and its ticks are unchanged');
  // Only the button changes what is open.
  state.batch = newer;
  calls.length = 0;
  doc.getElementById('fileJanitorShowNewerScan').click();
  await settle();
  await settle();
  assert.ok(calls.some(call => /\/batches\/batch-newer\?/.test(call.target)));
  assert.equal(doc.getElementById('fileJanitorNewerScan'), null, 'the line goes once it is shown');
  // The button that was pressed is gone: focus goes to the picture, not the page.
  assert.notEqual(doc.activeElement, doc.body);
});

// A scan does not propose a file that an earlier scan still holds, so the files of the
// scan that is not open are in no picture. The page says where they are.
test('files still waiting in an earlier scan are named, with one button that opens it', async () => {
  const { doc, calls, state } = await groupedReviewFixture(3);
  assert.equal(doc.getElementById('fileJanitorOtherScan'), null, 'nothing is waiting elsewhere');
  state.batches = [
    state.batch,
    batchFixture({
      id: 'batch-earlier',
      started_at: '2001-01-01T00:00:00Z',
      // Two of the four need review; they are part of the four, not extra.
      summary: { total: 4, proposed: 4, needs_review: 2 }
    })
  ];
  await panel._reloadBatch();
  const line = doc.getElementById('fileJanitorOtherScan');
  assert.ok(line, 'the earlier scan is named');
  assert.match(line.textContent, /4 files are still waiting in an earlier scan\./);
  assert.equal(panel._selected().length, 3, 'the open picture is untouched');
  calls.length = 0;
  doc.getElementById('fileJanitorShowOtherScan').click();
  await settle();
  await settle();
  assert.ok(calls.some(call => /\/batches\/batch-earlier\?/.test(call.target)));
  assert.notEqual(doc.activeElement, doc.body, 'focus is not dropped to the page');
});

test('a finished earlier scan, or one with nothing waiting, adds no line', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.batches = [
    state.batch,
    batchFixture({
      id: 'b-done',
      started_at: '2001-01-01T00:00:00Z',
      state: 'resolved',
      summary: { proposed: 5 }
    }),
    batchFixture({
      id: 'b-empty',
      started_at: '2002-01-01T00:00:00Z',
      summary: { proposed: 0, needs_review: 0 }
    })
  ];
  await panel._reloadBatch();
  assert.equal(doc.getElementById('fileJanitorOtherScan'), null);
});

test('nothing to file in this scan is not called tidy while files wait in another scan', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  const filed = state.fileReply;
  state.fileReply = request => {
    const answer = filed(request);
    state.emptied = false;
    return answer;
  };
  state.batches = [
    state.batch,
    batchFixture({
      id: 'batch-earlier',
      started_at: '2001-01-01T00:00:00Z',
      summary: { proposed: 7 }
    })
  ];
  pressButton(doc).click();
  await settle();
  await settle();
  await planSettled();
  const picturePanel = doc.getElementById('fileJanitorPicture');
  assert.match(picturePanel.textContent, /Nothing to file in this scan\./);
  assert.doesNotMatch(picturePanel.textContent, /tidy/);
  assert.match(text(doc), /7 files are still waiting in an earlier scan\./);
});

test('what was said about one review is not left behind when another opens', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  await settle();
  assert.notEqual(doc.getElementById('fileJanitorAnnounce').textContent, '');
  state.batches = [
    batchFixture({ id: 'batch-newer', started_at: '2099-01-01T00:00:00Z' }),
    state.batch
  ];
  await panel._reloadBatch();
  state.batch = batchFixture({
    id: 'batch-newer',
    started_at: '2099-01-01T00:00:00Z',
    summary: { total: 3, proposed: 3 }
  });
  doc.getElementById('fileJanitorShowNewerScan').click();
  await settle();
  await settle();
  assert.equal(doc.getElementById('fileJanitorAnnounce').textContent, '');
});

test('an older saved scan is not called newer', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.batches = [
    state.batch,
    batchFixture({ id: 'batch-old', started_at: '2001-01-01T00:00:00Z' })
  ];
  await panel._reloadBatch();
  assert.equal(doc.getElementById('fileJanitorNewerScan'), null);
});

test('a resolved concrete batch stays pinned and newer saved batches remain discoverable', async () => {
  const { doc, calls, state } = await groupedReviewFixture();
  state.batches = [batchFixture({ id: 'batch-new' }), state.batch];
  calls.length = 0;
  await panel._reloadBatch();
  assert.ok(calls.some(call => /\/batches\/batch-1\?/.test(call.target)));
  assert.ok(!calls.some(call => call.target.includes('/batches/latest')));
  doc.getElementById('fileJanitorView-all').click();
  const picker = doc.getElementById('fileJanitorBatchChoice');
  assert.ok(picker.children.some(option => option.value === 'batch-new'));
  assert.ok(picker.children.some(option => option.value === 'batch-1' && option.selected));
  assert.equal(panel._selected().length, 206);
});

test('unticking a folder saves one request before the button can file again, and re-ticking restores it', async () => {
  const { doc, calls, state } = await groupedReviewFixture();
  let release;
  state.editGate = new Promise(resolve => {
    release = resolve;
  });
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  assert.equal(panel._selected().length, 206, 'a pending save changes nothing on screen');
  // A saved choice needs no label (R58): only the button says it is waiting.
  assert.doesNotMatch(surface(doc).textContent, /Saving exact|Your choices are saved/);
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
  assert.equal(
    doc.getElementById('fileJanitorSelection').textContent,
    'Saving…',
    'the button says why it waits'
  );
  release();
  await settle();
  // Nothing is ticked now: the card stays, drawn dashed, with its files listed as
  // not filed, and there is nothing to file.
  assert.deepEqual(panel._selected(), []);
  const card = surface(doc).all(node => node.getAttribute('data-fj-folder') === 'images')[0];
  assert.match(card.className, /is-unticked/);
  assert.match(card.textContent, /Not filed/);
  assert.equal(folderCheckbox(doc).checked, false);
  assert.equal(folderCheckbox(doc).getAttribute('aria-label'), 'Images, 206 files, not filed');
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
  changeCheckbox(folderCheckbox(doc), true);
  await settle();
  assert.equal(panel._selected().length, 206);
  assert.equal(folderCheckbox(doc).checked, true);
  // The button waits for the plan check of the new choices before it offers to file.
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
  await planSettled();
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, false);
  assert.equal(doc.getElementById('fileJanitorApprove').textContent, 'File 206 files');
  const edits = calls.filter(call => call.options?.method === 'POST');
  assert.deepEqual(
    edits.map(call => JSON.parse(call.options.body).action),
    ['deselect_group', 'select_group'],
    'one request per tick, never one per file'
  );
  const request = JSON.parse(edits[0].options.body);
  assert.equal(request.group_id, 'group-images');
  assert.equal(request.candidate_ids, undefined, 'the server resolves the displayed snapshot');
  assert.equal(request.expected_revision.length, 64);
  assert.ok(edits.every(call => call.target.endsWith('/draft')));
});

test('a file left out of a ticked folder is listed as left out and the folder shows partly ticked', async () => {
  const { doc } = await groupedReviewFixture(3);
  doc.getElementById('fileJanitorView-all').click();
  const row = rowsIn(surface(doc)).find(
    row => row.getAttribute('data-candidate-id') === 'group-member-1'
  );
  changeCheckbox(row.all(node => node.className === 'fj-select')[0], false);
  await settle();
  assert.equal(panel._selected().length, 2);
  doc.getElementById('fileJanitorView-groups').click();
  assert.equal(folderCheckbox(doc).indeterminate, true);
  assert.equal(folderCheckbox(doc).checked, true);
  assert.match(surface(doc).textContent, /Left out by you · 1/);
  assert.match(surface(doc).textContent, /image-1\.png/);
});

test('a failed folder save is explicitly unsaved, blocks the button and retries the same request', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.failNextEdit = true;
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  assert.equal(panel._selected().length, 3, 'nothing is shown as saved that is not');
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
  assert.match(surface(doc).textContent, /not confirmed saved/);
  assert.equal(folderCheckbox(doc).disabled, true, 'no further edit while one is unconfirmed');
  buttonNamed(surface(doc), 'Retry that change').click();
  await settle();
  assert.deepEqual(panel._selected(), []);
  assert.equal(folderCheckbox(doc).checked, false);
});

test('a malformed successful save response cannot invent durably saved choices', async () => {
  const { doc, draftView } = await groupedReviewFixture(3);
  const original = globalThis.fetch;
  globalThis.fetch = (url, options) =>
    String(url).endsWith('/draft') && options?.method === 'POST'
      ? Promise.resolve({
          ok: true,
          json: async () => ({ draft: { ...draftView(), revision: 'invalid' } })
        })
      : original(url, options);
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  assert.equal(panel._selected().length, 3);
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
  assert.match(surface(doc).textContent, /not confirmed saved/);
});

test('a change in another window is read again, said once, and never overwrites the winning choices (R63)', async () => {
  const { doc, calls, state } = await groupedReviewFixture(3);
  // Another window left out two files after this picture was drawn.
  state.generation++;
  state.choices = [{ candidate_id: 'group-member-1', operation: 'move', source_group_id: '' }];
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  await settle();
  // Nothing to press: the picture was read again by itself and says so.
  assert.match(text(doc), /Updated from another window\. Your last change was not saved\./);
  assert.equal(
    surface(doc).all(node => node.textContent === 'Reload saved choices').length,
    0,
    'no button to press'
  );
  assert.deepEqual(panel._selected(), ['group-member-1'], "the other window's choices won");
  assert.equal(folderCheckbox(doc).indeterminate, true);
  assert.match(surface(doc).textContent, /Left out by you · 2/);
  // The click that was refused was not tried a second time against the new picture.
  assert.equal(
    calls.filter(call => /\/draft$/.test(call.target) && call.options?.method === 'POST').length,
    1
  );
});

test('a detached folder control cannot change the same folder in another saved review', async () => {
  const { doc, calls, state } = await groupedReviewFixture(3);
  const old = folderCheckbox(doc);
  state.batch = batchFixture({ id: 'batch-new', summary: { total: 3, proposed: 3 } });
  state.batches = [state.batch];
  state.review = { ...state.review, batch_id: 'batch-new', revision: 'b'.repeat(64) };
  panel._setLinkedBatchForTest('batch-new');
  await panel._reloadBatch();
  calls.length = 0;
  changeCheckbox(old, false);
  await settle();
  assert.equal(panel._selected().length, 3, 'the new review keeps its own ticks');
  assert.ok(calls.every(call => call.options?.method !== 'POST'));
  assert.equal(folderCheckbox(doc).checked, true);
});

test('late saved-intent reads cannot overwrite a newer canonical return', async () => {
  const { calls, state, draftView } = await groupedReviewFixture(3);
  const original = globalThis.fetch;
  const first = deferredReply();
  const second = deferredReply();
  const stale = draftView();
  let reads = 0;
  globalThis.fetch = (url, options) =>
    String(url).endsWith('/draft') && !options?.method
      ? ++reads === 1
        ? first.promise
        : second.promise
      : original(url, options);
  const older = panel._reloadBatch();
  await settle();
  const newer = panel._reloadBatch();
  await settle();
  state.generation++;
  state.choices = [{ candidate_id: 'group-member-2', operation: 'move', source_group_id: '' }];
  second.reply({ draft: draftView() });
  await newer;
  first.reply({ draft: stale });
  await older;
  assert.deepEqual(panel._selected(), ['group-member-2']);
  assert.ok(calls.every(call => call.options?.method !== 'POST'));
});

test('close and reopen retain saved choices but never restore the Trash tick', async () => {
  const { doc, state } = await groupedReviewFixture(2);
  // One of the two files is marked for Trash, as the person saved it earlier.
  state.choices[0] = { candidate_id: 'group-member-0', operation: 'trash' };
  state.generation++;
  await panel._reloadBatch();
  await panel._planNow();
  const tick = doc.getElementById('fileJanitorTrashAck');
  assert.ok(tick, 'the Trash tick is offered');
  changeCheckbox(tick, true);
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, false);
  panel.close();
  panel.open({ source: 'folder-preparation', tab: 'review' });
  await panel._reloadBatch();
  await panel._planNow();
  assert.equal(panel._selected().length, 1, 'the saved choices come back');
  assert.equal(doc.getElementById('fileJanitorTrashAck').checked, false, 'the tick does not');
  assert.equal(doc.getElementById('fileJanitorApprove').disabled, true);
});

async function protectionReview(t, count = 3) {
  const doc = setup();
  const state = {
    fact: {
      workspace_id: 'ws-1',
      root_generation: 'root-one',
      binding_id: 'a'.repeat(64),
      state: 'unknown',
      destination: { kind: 'chooser' }
    },
    batch: batchFixture(),
    candidates: candidatesFixture(),
    opened: 0,
    calls: []
  };
  if (count > 3)
    state.candidates = Array.from({ length: count }, (_, i) => ({
      ...candidatesFixture()[0],
      id: 'c' + i
    }));
  const stored = new Map();
  const local = new Map();
  const storage = values => ({
    getItem: key => values.get(key) || null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key)
  });
  window.sessionStorage = storage(stored);
  window.localStorage = storage(local);
  window.location = { href: 'http://localhost/workspaces/filing', search: '' };
  window.history = {
    replaceState: (_, __, route) => {
      window.location.href = new URL(route, window.location.href).href;
      window.location.search = new URL(window.location.href).search;
    }
  };
  window.FileJanitorProtection = createProtectionTrips(window);
  window.FileJanitorProtection.sentence = protectionSentence;
  window.FileJanitorProtection.stripSentence = protectionStripSentence;
  window.OriProtectFolders = {
    open: async () => {
      state.opened++;
      return true;
    },
    close() {}
  };
  t.after(() => {
    delete window.FileJanitorProtection;
    delete window.OriProtectFolders;
  });
  const status = statusFixture();
  status.settings.root_id = 'root-one';
  panel.render(status);
  panel.open({ source: 'protection-return', tab: 'review' });
  panel._setBatch(state.batch, state.candidates.slice(0, 50), CATEGORIES);
  const reply = data => ({ ok: true, json: async () => data });
  state.fetch = async (url, opts) => {
    state.calls.push({ url, opts });
    if (url.endsWith('/protection')) return reply(state.fact);
    if (url.includes('/batches/')) {
      const query = new URL(url, 'http://localhost').searchParams;
      const offset = Number(query.get('offset') || 0);
      const limit = Number(query.get('limit') || 50);
      return reply({
        batch: state.batch,
        candidates: state.candidates.slice(offset, offset + limit),
        total: state.candidates.length,
        filtered_total: state.candidates.length,
        categories: CATEGORIES
      });
    }
    if (url.endsWith('/preview'))
      return reply({ preview: { ...PREVIEW, trash_count: panel._trashMarked().length } });
    return reply({ status, categories: CATEGORIES });
  };
  globalThis.fetch = state.fetch;
  await panel._loadProtection();
  return { doc, state, stored };
}

test('protection is optional: one sentence and two buttons, and Not now removes it from Review but keeps the choices', async t => {
  const { doc } = await protectionReview(t);
  panel._select('c1');
  const strip = () =>
    surface(doc).all(node => node.getAttribute?.('aria-label') === 'Optional folder protection')[0];
  assert.equal(
    strip().all(node => node.className === 'fj-protection-copy')[0].textContent,
    'Filing is not a backup, and Ori does not know of one for this folder.'
  );
  assert.deepEqual(
    strip()
      .all(node => node.tagName === 'BUTTON')
      .map(node => node.textContent),
    ['Check protection', 'Not now'],
    'exactly the two buttons'
  );
  surface(doc)
    .all(node => node.tagName === 'BUTTON' && node.textContent === 'Not now')[0]
    .click();
  assert.deepEqual(panel._selected(), ['c1']);
  // R54: once dismissed nothing about backups is on the Review tab.
  assert.doesNotMatch(text(doc), /Filing is not a backup/);
  assert.equal(strip(), undefined);
  assert.equal(doc.getElementById('fileJanitorCheckProtection'), null);
  assert.doesNotMatch(
    text(doc),
    /Protection prompt dismissed|Dismissed for this page only/,
    'no label for a choice that was kept'
  );
  panel.close();
  panel.open({ source: 'protection-return' });
  assert.doesNotMatch(text(doc), /Filing is not a backup/);
  assert.deepEqual(panel._selected(), ['c1'], 'the choices outlive the reminder');
  // Check protection stays on the Settings tab.
  panel._selectTab('settings');
  const settingsCheck = doc.getElementById('fileJanitorCheckProtectionSettings');
  assert.ok(settingsCheck, 'Check protection is reachable from Settings');
  assert.equal(settingsCheck.disabled, false);
});

test('the backup strip appears for each uncovered state with its sentence, and never for a covered or unreadable one (R53, R54)', async t => {
  const { doc, state } = await protectionReview(t);
  const sentences = {
    not_covered: 'Filing is not a backup, and this folder is not in the backups Ori can see.',
    unknown: 'Filing is not a backup, and Ori does not know of one for this folder.',
    configured: 'Filing is not a backup. A backup is set up, but Ori has not seen it finish.'
  };
  for (const [name, sentence] of Object.entries(sentences)) {
    state.fact.state = name;
    await panel._loadProtection();
    assert.match(text(doc), new RegExp(sentence.replace(/[.]/g, '\\.')), name);
  }
  for (const name of ['covered', 'unavailable']) {
    state.fact.state = name;
    await panel._loadProtection();
    assert.doesNotMatch(text(doc), /Filing is not a backup/, name);
    assert.equal(doc.getElementById('fileJanitorCheckProtection'), null, name);
  }
  // No information at all: no strip either.
  state.fact.state = 'unknown';
  delete state.fact.destination;
  await panel._loadProtection();
  assert.doesNotMatch(text(doc), /Filing is not a backup/);
});

test('when the choice cannot be kept past this page, Not now says so', async t => {
  const { doc } = await protectionReview(t);
  window.localStorage = {
    getItem: () => null,
    setItem() {
      throw new Error('denied');
    }
  };
  surface(doc)
    .all(node => node.tagName === 'BUTTON' && node.textContent === 'Not now')[0]
    .click();
  assert.match(text(doc), /Dismissed for this page only: browser storage is unavailable/);
  assert.doesNotMatch(text(doc), /Filing is not a backup/, 'it is still gone for this page');
});

test('the backup strip sits above the File button, below the picture', async t => {
  const { doc } = await protectionReview(t);
  const order = surface(doc).all(
    node =>
      node.getAttribute?.('aria-label') === 'Optional folder protection' ||
      node.id === 'fileJanitorApprove'
  );
  assert.equal(order[0].getAttribute('aria-label'), 'Optional folder protection');
  assert.equal(order[1].id, 'fileJanitorApprove');
});

test('Settings offers Check protection even when the strip was dismissed, and says why it waits without a review', async t => {
  const { doc } = await protectionReview(t);
  panel._selectTab('settings');
  assert.match(text(doc), /Protection/);
  assert.match(text(doc), /Protection is unknown/, 'Settings keeps the longer account');
  const check = doc.getElementById('fileJanitorCheckProtectionSettings');
  assert.ok(check);
  assert.equal(check.disabled, false);
});

test('protection departure keeps the saved choices, and the return carries no permission', async t => {
  const { doc, state, stored } = await protectionReview(t);
  panel._select('c1');
  surface(doc)
    .all(node => node.id?.startsWith('fileJanitorTrash-'))[1]
    .click();
  await panel._checkProtection();
  assert.equal(state.opened, 1);
  const trip = window.FileJanitorProtection.read();
  assert.equal(stored.has(TRIP_KEY), false, 'same-page trip was persisted unnecessarily');
  assert.deepEqual(trip.moves, ['c1']);
  assert.deepEqual(trip.trash, ['c2']);
  assert.doesNotMatch(JSON.stringify(trip), /token|acknowledg|plan_digest|invoice|Filed|category/);
  await panel.restoreProtectionTrip(trip);
  assert.deepEqual(panel._selected(), ['c1']);
  assert.deepEqual(panel._trashMarked(), ['c2']);
  // Coming back never files anything and never restores a tick: the person looks at
  // the picture again and presses File themselves.
  assert.match(text(doc), /Look at the picture again, then press File/);
  assert.equal(state.calls.filter(call => /\/file$/.test(call.url)).length, 0);
  assert.equal(doc.getElementById('fileJanitorTrashAck'), null);
});

test('return validates selected off-page candidates and preserves newer canonical category edits', async t => {
  const { doc, state } = await protectionReview(t, 250);
  panel._select('c1');
  panel._select('c240');
  panel._select('c249');
  await panel._checkProtection();
  const trip = window.FileJanitorProtection.read();
  state.candidates[1].decision_category = 'images';
  state.candidates[249].state = 'applied';
  await panel.restoreProtectionTrip(trip);
  assert.deepEqual(panel._selected(), ['c1', 'c240']);
  assert.ok(state.calls.some(call => call.url.includes('limit=200&offset=200')));
  assert.match(text(doc), /1 file you chose has changed or been filed, so it was taken out/);
  assert.equal(
    state.calls.filter(call => call.url.endsWith('/decisions')).length,
    0,
    'snapshot rewrote server decisions'
  );
  assert.equal(state.calls.filter(call => /\/file$/.test(call.url)).length, 0);
});

test('departure waits for pending category writes and a failed save keeps review in place', async t => {
  const { doc, state } = await protectionReview(t);
  const pending = deferredReply();
  globalThis.fetch = (url, opts) =>
    url.endsWith('/decisions') ? pending.promise : state.fetch(url, opts);
  const picker = surface(doc).all(node => node.className === 'fj-category')[0];
  picker.value = 'images';
  picker.dispatch('change');
  const departure = panel._checkProtection();
  await settle();
  assert.equal(state.opened, 0);
  pending.reply({ error: { message: 'save rejected' } }, false);
  await departure;
  assert.equal(state.opened, 0);
  assert.equal(panel.isOpen(), true);
  assert.match(
    doc.getElementById('fileJanitorError').textContent,
    /category change could not be saved/
  );
});

test('Check protection becomes usable when an explicit scan finishes', async t => {
  const { doc } = await protectionReview(t);
  doc.getElementById('fileJanitorScan').click();
  await settle();
  assert.equal(doc.getElementById('fileJanitorCheckProtection').disabled, false);
});

test('a failed canonical return keeps the descriptor and exposes a recoverable retry', async t => {
  const { doc, state } = await protectionReview(t);
  panel._select('c1');
  await panel._checkProtection();
  const trip = window.FileJanitorProtection.read();
  globalThis.fetch = (url, opts) =>
    url.includes('/batches/batch-1') ? Promise.resolve({ ok: false }) : state.fetch(url, opts);
  await panel.restoreProtectionTrip(trip);
  assert.equal(window.FileJanitorProtection.read().nonce, trip.nonce);
  assert.equal(doc.getElementById('fileJanitorCheckProtection').disabled, true);
  const retry = surface(doc).all(
    node => node.tagName === 'BUTTON' && node.textContent === 'Retry returning to review'
  )[0];
  assert.ok(retry);
  globalThis.fetch = state.fetch;
  retry.click();
  await settle();
  assert.equal(window.FileJanitorProtection.read(), null);
  assert.deepEqual(panel._selected(), ['c1']);
});

test('update navigation selects latest Review without scan, decisions, preview or apply', async t => {
  const { state } = await protectionReview(t);
  panel._setLinkedBatchForTest('old-preparation-batch');
  state.calls.length = 0;
  assert.equal(
    panel.openLatestReview({ source: 'workspace-updates', batchId: state.batch.id }),
    true
  );
  await settle();
  assert.equal(panel.activeTab(), 'review');
  assert.ok(state.calls.some(call => call.url.includes('/batches/latest')));
  assert.ok(state.calls.every(call => !call.opts?.method || call.opts.method === 'GET'));
  assert.ok(
    state.calls.every(call => !/\/(scan|decisions|preview|apply|undo)(?:\?|$)/.test(call.url))
  );
});

test('a different review cannot discard live choices; the same batch preserves them', async t => {
  const { state } = await protectionReview(t);
  panel._select('c1');
  state.calls.length = 0;
  assert.equal(
    panel.openLatestReview({ source: 'workspace-updates', batchId: 'replacement' }),
    false
  );
  assert.deepEqual(panel._selected(), ['c1']);
  assert.equal(state.calls.length, 0);
  assert.equal(
    panel.openLatestReview({ source: 'workspace-updates', batchId: state.batch.id }),
    true
  );
  await settle();
  assert.deepEqual(panel._selected(), ['c1']);
});

for (const changed of ['root', 'batch', 'permission']) {
  test('return rejects changed ' + changed + ' without restoring old choices', async t => {
    const { doc, state } = await protectionReview(t);
    panel._select('c1');
    await panel._checkProtection();
    const trip = window.FileJanitorProtection.read();
    if (changed === 'root') state.fact.root_generation = 'root-two';
    if (changed === 'batch') state.batch = batchFixture({ id: 'batch-two' });
    if (changed === 'permission')
      globalThis.fetch = (url, opts) =>
        url.endsWith('/protection') ? Promise.resolve({ ok: false }) : state.fetch(url, opts);
    await panel.restoreProtectionTrip(trip);
    assert.deepEqual(panel._selected(), []);
    assert.equal(window.FileJanitorProtection.read(), null);
    assert.match(doc.getElementById('fileJanitorError').textContent, /cannot be restored/);
    assert.equal(state.calls.filter(call => call.url.endsWith('/apply')).length, 0);
    assert.equal(state.calls.filter(call => /\/file$/.test(call.url)).length, 0);
  });
}

// --------------------------------------------------------- the plan and the press
//
// One main button files exactly what the picture shows. These tests drive the
// console against a stub that answers the plan check and the File request like the
// server; the label table and the Trash rules have their own tests in
// file-janitor-picture.test.js, and the server's refusals in the Go packages.

const CLIENT_KEY = /^[A-Za-z0-9_-]{16,128}$/;

function pressButton(doc) {
  return doc.getElementById('fileJanitorApprove');
}

test('the one main button states what pressing it will do and follows each saved change', async () => {
  const { doc, calls } = await groupedReviewFixture(3);
  assert.equal(pressButton(doc).textContent, 'File 3 files');
  assert.equal(pressButton(doc).disabled, false);
  changeCheckbox(folderCheckbox(doc), false);
  await planSettled();
  assert.equal(pressButton(doc).textContent, 'No changes selected');
  assert.equal(pressButton(doc).disabled, true);
  changeCheckbox(folderCheckbox(doc), true);
  await planSettled();
  assert.equal(pressButton(doc).textContent, 'File 3 files');
  assert.equal(pressButton(doc).disabled, false);
  // The plan check is a read: it names the choices it was asked about and writes
  // nothing.
  const checks = calls.filter(call => call.target.includes('/plan?'));
  assert.ok(checks.length >= 3);
  for (const check of checks) {
    assert.ok(!check.options?.method || check.options.method === 'GET');
    assert.match(check.target, /revision=[0-9a-f]{64}&group_revision=[0-9a-f]{64}/);
  }
});

test('a plan answer that arrives after a newer one is ignored', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  // The next question is answered late: its answer will name three files to file.
  let release;
  state.planGate = new Promise(resolve => {
    release = resolve;
  });
  const slow = panel._planNow();
  await settle();
  // Meanwhile the person unticks the folder, and the newer plan says there is
  // nothing to file.
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  await panel._planNow();
  await settle();
  assert.equal(pressButton(doc).textContent, 'No changes selected');
  release();
  await slow;
  await settle();
  assert.equal(
    pressButton(doc).textContent,
    'No changes selected',
    'the late answer was about choices that are gone'
  );
  assert.equal(doc.getElementById('fileJanitorJumpFiled'), null);
  assert.match(pictureHost(doc).textContent, /0 ready to file · 0 need a decision/);
  assert.equal(pressButton(doc).disabled, true);
});

test('while the plan is being checked the button waits instead of claiming there is nothing to file', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.planGate = new Promise(() => {});
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  const checking = panel._planNow();
  await settle();
  assert.equal(pressButton(doc).disabled, true);
  assert.equal(surface(doc).all(node => node.className === 'fj-decision-counts').length, 0);
  assert.equal(pressButton(doc).textContent, 'File');
  assert.equal(doc.getElementById('fileJanitorSelection').textContent, 'Checking…');
  void checking;
});

test('a plan that cannot be read leaves the button off and offers another look', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.failPlan = true;
  await panel._planNow();
  assert.equal(pressButton(doc).disabled, true);
  const retry = buttonNamed(surface(doc), 'Check again');
  assert.ok(retry, 'expected a way to check again');
  assert.equal(doc.getElementById('fileJanitorJumpFiled'), null);
  state.failPlan = false;
  retry.click();
  await planSettled();
  assert.equal(pressButton(doc).textContent, 'File 3 files');
  assert.equal(pressButton(doc).disabled, false);
});

test('a successful response without counts or with an old group revision cannot authorize File', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  const fixtureFetch = globalThis.fetch;
  for (const tamper of [
    plan => delete plan.move_count,
    plan => (plan.group_revision = '0'.repeat(64))
  ]) {
    globalThis.fetch = async (url, options) => {
      const result = await fixtureFetch(url, options);
      if (!url.includes('/plan?')) return result;
      const body = await result.json();
      tamper(body.plan);
      return { ok: true, json: async () => body };
    };
    await panel._planNow();
    assert.equal(pressButton(doc).textContent, 'File');
    assert.equal(pressButton(doc).disabled, true);
    assert.equal(doc.getElementById('fileJanitorJumpFiled'), null);
    pressButton(doc).click();
    await settle();
    assert.equal(state.fileRequests.length, 0);
  }
});

test('a file that will get a new name is shown under it, tagged renamed', async () => {
  const { doc, state, calls } = await groupedReviewFixture(3);
  state.renamed.add('group-member-0');
  await panel._planNow();
  const names = surface(doc)
    .all(node => node.className === 'fj-folder-files')[0]
    .children.map(node => node.textContent);
  assert.deepEqual(names, ['image-0 (2).png renamed', 'image-1.png', 'image-2.png']);
  const ask = calls.filter(call => call.target.includes('/plan?')).at(-1);
  assert.match(ask.target, /ids=group-member-0,group-member-1,group-member-2/);
});

test('pressing File sends the plan on screen once, and nothing else about the files', async () => {
  const { doc, calls, state } = await groupedReviewFixture(3);
  pressButton(doc).click();
  await settle();
  await settle();
  assert.equal(state.fileRequests.length, 1, 'one request per press');
  const [request] = state.fileRequests;
  assert.deepEqual(Object.keys(request).sort(), [
    'client_key',
    'expected_group_revision',
    'expected_revision',
    'plan_digest',
    'trash_acknowledged'
  ]);
  assert.match(request.client_key, CLIENT_KEY);
  assert.equal(request.expected_revision.length, 64);
  assert.equal(request.expected_group_revision.length, 64);
  assert.equal(request.trash_acknowledged, 0);
  // There is no preview step, no approval token and no second dialog.
  assert.ok(!calls.some(call => /\/(preview|apply)$/.test(call.target)));
  assert.equal(surface(doc).all(node => node.getAttribute('role') === 'dialog').length, 1);
  assert.equal(doc.getElementById('fileJanitorConfirmApply'), null);
  const summary = doc.getElementById('fileJanitorResultsSummary');
  assert.match(summary.textContent, /3 files filed/);
});

test('pressing File twice files once', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  let release;
  state.fileGate = new Promise(resolve => {
    release = resolve;
  });
  const button = pressButton(doc);
  button.click();
  button.click();
  await settle();
  assert.equal(button.disabled, true, 'the button is off while the request is in flight');
  release();
  await settle();
  await settle();
  assert.equal(state.fileRequests.length, 1);
});

test('a lost answer is retried under the same key, so it can never file twice', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  const filed = state.fileReply;
  state.fileReply = () => {
    throw new Error('offline');
  };
  pressButton(doc).click();
  await settle();
  await settle();
  await planSettled();
  assert.match(doc.getElementById('fileJanitorError').textContent, /offline|could not file/);
  state.fileReply = filed;
  pressButton(doc).click();
  await settle();
  await settle();
  assert.equal(state.fileRequests.length, 2);
  assert.equal(state.fileRequests[1].client_key, state.fileRequests[0].client_key);
  assert.equal(state.fileRequests[1].plan_digest, state.fileRequests[0].plan_digest);
});

test('different choices are a different press, under a new key', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  const filed = state.fileReply;
  state.fileReply = () => {
    throw new Error('offline');
  };
  pressButton(doc).click();
  await settle();
  await settle();
  state.fileReply = filed;
  changeCheckbox(folderCheckbox(doc), false);
  await planSettled();
  changeCheckbox(folderCheckbox(doc), true);
  await planSettled();
  pressButton(doc).click();
  await settle();
  await settle();
  assert.equal(state.fileRequests.length, 2);
  assert.notEqual(state.fileRequests[1].client_key, state.fileRequests[0].client_key);
});

test('a refused press moves nothing, says so, and marks the files that changed', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.fileReply = () => ({
    ok: false,
    status: 409,
    json: async () => ({
      code: 'plan_changed',
      message: 'The folder changed since you looked. Nothing was filed.',
      details: {
        plan: {
          changed: {
            total: 1,
            items: [{ candidate_id: 'group-member-1', name: 'image-1.png', reason: 'changed' }]
          }
        }
      }
    })
  });
  // A name clash appeared since the person looked: image-2 will now be renamed.
  state.renamed.add('group-member-2');
  pressButton(doc).click();
  await settle();
  await settle();
  await planSettled();
  assert.match(
    doc.getElementById('fileJanitorSelection').textContent,
    /Nothing was filed\. Look at the picture/
  );
  assert.equal(doc.getElementById('fileJanitorResultsSummary'), null, 'no results: nothing moved');
  // Two files are marked: the one the server named as changed, and the one whose new
  // name the person never saw (its name is now taken).
  const tagged = surface(doc).all(node => String(node.className).includes('fj-tag-changed'));
  assert.equal(tagged.length, 2);
  assert.ok(tagged.every(node => node.textContent === 'changed since you looked'));
  assert.deepEqual(
    tagged.map(node =>
      node.parent.textContent.replace('changed since you looked', '').replace('renamed', '').trim()
    ),
    ['image-1.png', 'image-2 (2).png']
  );
});

test('the next saved change clears what a refused press said', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.fileReply = () => ({
    ok: false,
    status: 409,
    json: async () => ({
      code: 'plan_changed',
      message: 'x',
      details: { plan: { changed: { total: 0, items: [] } } }
    })
  });
  pressButton(doc).click();
  await settle();
  await settle();
  assert.match(doc.getElementById('fileJanitorSelection').textContent, /Nothing was filed/);
  changeCheckbox(folderCheckbox(doc), false);
  await planSettled();
  assert.doesNotMatch(doc.getElementById('fileJanitorSelection').textContent, /Nothing was filed/);
});

test('Trash needs its own tick, naming the count; it is never pre-ticked and clears on any change', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  assert.equal(doc.getElementById('fileJanitorTrashAck'), null, 'no Trash, no tick');
  state.choices[0] = { candidate_id: 'group-member-0', operation: 'trash' };
  state.generation++;
  await panel._reloadBatch();
  await panel._planNow();
  const tick = doc.getElementById('fileJanitorTrashAck');
  assert.ok(tick);
  assert.equal(tick.checked, false, 'Ori never ticks it');
  assert.equal(
    doc.getElementById('fileJanitorTrashAckText').textContent,
    'Yes, move 1 file to Trash'
  );
  assert.equal(pressButton(doc).textContent, 'File 2 files · 1 to Trash');
  assert.equal(doc.getElementById('fileJanitorJumpFiled').textContent, '2 ready to file');
  assert.equal(doc.getElementById('fileJanitorJumpTrash').textContent, '1 marked for Trash');
  assert.equal(pressButton(doc).disabled, true, 'the button waits for the tick');
  changeCheckbox(tick, true);
  assert.equal(pressButton(doc).disabled, false);
  changeCheckbox(doc.getElementById('fileJanitorTrashAck'), false);
  assert.equal(pressButton(doc).disabled, true, 'unticking turns it off again');
  changeCheckbox(doc.getElementById('fileJanitorTrashAck'), true);
  // Any saved change starts over: the person ticks again for what is now planned.
  changeCheckbox(folderCheckbox(doc), false);
  await planSettled();
  changeCheckbox(folderCheckbox(doc), true);
  await planSettled();
  assert.equal(doc.getElementById('fileJanitorTrashAck').checked, false);
  assert.equal(pressButton(doc).disabled, true);
});

test('the Trash tick is sent as the count it was ticked for', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.choices[0] = { candidate_id: 'group-member-0', operation: 'trash' };
  state.choices[1] = { candidate_id: 'group-member-1', operation: 'trash' };
  state.generation++;
  await panel._reloadBatch();
  await panel._planNow();
  assert.equal(pressButton(doc).textContent, 'File 1 file · 2 to Trash');
  assert.equal(
    doc.getElementById('fileJanitorTrashAckText').textContent,
    'Yes, move 2 files to Trash'
  );
  changeCheckbox(doc.getElementById('fileJanitorTrashAck'), true);
  pressButton(doc).click();
  await settle();
  await settle();
  assert.equal(state.fileRequests[0].trash_acknowledged, 2);
  assert.match(
    doc.getElementById('fileJanitorResultsSummary').textContent,
    /1 file filed · 2 moved to Trash/
  );
});

test('a Trash-only press says what it will do', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.choices = [{ candidate_id: 'group-member-0', operation: 'trash' }];
  state.generation++;
  await panel._reloadBatch();
  await panel._planNow();
  assert.equal(pressButton(doc).textContent, 'Move 1 file to Trash');
  assert.equal(pressButton(doc).disabled, true);
});

test('more than one press can file: the button is off and says what to do', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.overLimit = true;
  await panel._planNow();
  assert.equal(
    pressButton(doc).textContent,
    'Ori files up to 1,000 files at a time. Untick a folder to continue.'
  );
  assert.equal(pressButton(doc).disabled, true);
});

test('a failed save turns the button off and says it is not saved', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.failNextEdit = true;
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  assert.equal(pressButton(doc).disabled, true);
  assert.equal(doc.getElementById('fileJanitorSelection').textContent, 'Not saved. Retry');
});

test('the same button, the same Trash tick and the same rules are in All files', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.choices[0] = { candidate_id: 'group-member-0', operation: 'trash' };
  state.generation++;
  await panel._reloadBatch();
  await panel._planNow();
  doc.getElementById('fileJanitorView-all').click();
  assert.equal(rowsIn(surface(doc)).length, 3, 'the table is showing');
  assert.equal(pressButton(doc).textContent, 'File 2 files · 1 to Trash');
  assert.equal(pressButton(doc).disabled, true);
  changeCheckbox(doc.getElementById('fileJanitorTrashAck'), true);
  assert.equal(pressButton(doc).disabled, false);
});

test('the renamed names come back after a trip to All files', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.renamed.add('group-member-0');
  await panel._planNow();
  doc.getElementById('fileJanitorView-all').click();
  await planSettled();
  doc.getElementById('fileJanitorView-groups').click();
  await planSettled();
  const names = surface(doc)
    .all(node => node.className === 'fj-folder-files')[0]
    .children.map(node => node.textContent);
  assert.equal(names[0], 'image-0 (2).png renamed');
});

test('focus stays on the control the person used when the plan check redraws', async () => {
  const { doc } = await groupedReviewFixture(3);
  const before = folderCheckbox(doc);
  before.focus();
  await panel._planNow();
  // The redraw made new elements: focus is on the new folder tick, not the old one.
  assert.notEqual(folderCheckbox(doc), before);
  assert.equal(doc.activeElement, folderCheckbox(doc));
  pressButton(doc).focus();
  await panel._planNow();
  assert.equal(doc.activeElement, pressButton(doc));
});

test('a scan with more confident files than one press can file is drawn, and says what to do', async () => {
  const { doc, state } = await groupedReviewFixture(1001);
  state.overLimit = true;
  await panel._planNow();
  // The default view carries all 1,001 ticks; it is not "unavailable".
  assert.equal(panel._selected().length, 1001);
  assert.doesNotMatch(surface(doc).textContent, /Saved choices are unavailable/);
  assert.equal(
    pressButton(doc).textContent,
    'Ori files up to 1,000 files at a time. Untick a folder to continue.'
  );
  assert.equal(pressButton(doc).disabled, true);
  // An edit that still leaves too many is refused: that is not a failed save.
  state.refuseBounds = true;
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  assert.equal(
    doc.getElementById('fileJanitorSelection').textContent,
    'Ori files up to 1,000 files at a time. Untick a folder to continue.'
  );
  assert.doesNotMatch(surface(doc).textContent, /not confirmed saved|Not saved/);
  assert.equal(folderCheckbox(doc).disabled, false, 'the person can try something else');
  // Starting from nothing is the way out when no one folder is enough.
  state.refuseBounds = false;
  state.overLimit = false;
  buttonNamed(surface(doc), 'Untick everything').click();
  await planSettled();
  assert.deepEqual(panel._selected(), []);
  assert.equal(pressButton(doc).textContent, 'No changes selected');
});

test('a folder that cannot be ticked because a file changed says so and is not a failed save', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  changeCheckbox(folderCheckbox(doc), false);
  await planSettled();
  state.refuseMemberChanged = true;
  changeCheckbox(folderCheckbox(doc), true);
  await settle();
  assert.equal(
    doc.getElementById('fileJanitorSelection').textContent,
    'Ori could not tick that: a file changed since the scan.'
  );
  assert.doesNotMatch(surface(doc).textContent, /Reload saved choices|not confirmed saved/);
  assert.equal(folderCheckbox(doc).disabled, false, 'nothing is stuck: another edit works');
  // The next edit clears the line.
  state.refuseMemberChanged = false;
  changeCheckbox(folderCheckbox(doc), true);
  await planSettled();
  assert.doesNotMatch(doc.getElementById('fileJanitorSelection').textContent, /could not tick/);
});

test('files taken out because they changed are said once, in plain words, until the person acts', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.removedStale = 1;
  state.generation++;
  await panel._reloadBatch();
  await planSettled();
  const status = () => doc.getElementById('fileJanitorDraftStatus').textContent;
  assert.match(status(), /1 file you ticked changed since you looked, so it was taken out\./);
  assert.doesNotMatch(status(), /eligible|exact/);
  // Closing and reopening does not bring it back once the person has acted.
  changeCheckbox(folderCheckbox(doc), false);
  await planSettled();
  assert.doesNotMatch(status(), /changed since you looked/);
  panel.close();
  panel.open({ source: 'folder-preparation', tab: 'review' });
  await panel._reloadBatch();
  await planSettled();
  assert.doesNotMatch(status(), /changed since you looked/);
  state.removedStale = 3;
  state.generation++;
  await panel._reloadBatch();
  assert.match(status(), /2 files you ticked changed since you looked, so they were taken out\./);
});

test('a change made elsewhere is said beside the button, not offered as a retry', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.generation++; // another window saved something after this picture was drawn
  changeCheckbox(folderCheckbox(doc), false);
  await settle();
  await settle();
  assert.equal(
    doc.getElementById('fileJanitorSelection').textContent,
    'Updated from another window. Your last change was not saved.'
  );
});

// ---------------------------------------------- answers on the loose rows

// A scan that has only a question group: its files are loose, nothing is ticked on
// open, and the row has one menu.
async function questionRowFixture(count = 3) {
  const fixture = await groupedReviewFixture(count);
  const { state } = fixture;
  Object.assign(state.review.groups[0], {
    kind: 'question',
    key: 'sensitive',
    label: 'Sensitive-looking names'
  });
  state.review.counts.filing = 0;
  state.review.counts.questions = count;
  state.choices = [];
  state.generation++;
  await panel._reloadBatch();
  await panel._planNow();
  return fixture;
}

test('an all-undecided review names the whole question count without offering File', async () => {
  const { doc, calls } = await questionRowFixture(53);
  const jump = doc.getElementById('fileJanitorJumpLoose');
  assert.equal(jump.textContent, '53 need a decision');
  assert.equal(doc.getElementById('fileJanitorJumpFiled'), null);
  jump.click();
  assert.equal(doc.activeElement.id, 'fileJanitorLooseTitle');
  assert.equal(pressButton(doc).disabled, true);
  assert.equal(draftEdits(calls).length, 0);
});

const rowMenu = doc => doc.getElementById('fileJanitorRowMenu-sensitive');
const draftEdits = calls =>
  calls
    .filter(call => call.options?.method === 'POST' && call.target.endsWith('/draft'))
    .map(call => JSON.parse(call.options.body));
function choose(select, value) {
  select.value = value;
  select.dispatch('change');
}

test('a question row is placed with one menu choice and the row says where its files went', async () => {
  const { doc, calls } = await questionRowFixture(3);
  assert.equal(pressButton(doc).textContent, 'No changes selected');
  assert.deepEqual(panel._selected(), [], 'Ori never ticks a question group');
  assert.deepEqual(
    rowMenu(doc).children.map(option => option.value),
    ['leave', 'stop', 'move:documents', 'move:images', 'move:archives', 'move:other']
  );
  choose(rowMenu(doc), 'move:documents');
  await planSettled();
  const [edit] = draftEdits(calls);
  assert.equal(edit.action, 'select_group');
  assert.equal(edit.category, 'documents');
  assert.ok(edit.group_id);
  assert.equal(edit.candidate_ids, undefined, 'the server resolves the row it showed');
  const note = surface(doc).all(node => node.className === 'fj-row-note')[0];
  assert.equal(note.textContent, 'All in Filed/Documents');
  assert.equal(rowMenu(doc).children.find(option => option.selected).value, 'move:documents');
  const card = surface(doc).all(node => node.getAttribute('data-fj-folder') === 'documents')[0];
  assert.match(card.textContent, /image-0\.png/);
  assert.equal(pressButton(doc).textContent, 'File 3 files');
  // Choosing a folder records a choice; nothing is filed until File is pressed.
  assert.ok(!calls.some(call => /\/file$/.test(call.target)));
});

test('"Leave here" takes a placed row back, and does nothing for a row that was never placed', async () => {
  const { doc, calls } = await questionRowFixture(2);
  choose(rowMenu(doc), 'leave');
  await settle();
  assert.deepEqual(draftEdits(calls), [], 'nothing to take back');
  choose(rowMenu(doc), 'move:images');
  await planSettled();
  choose(rowMenu(doc), 'leave');
  await planSettled();
  assert.deepEqual(
    draftEdits(calls).map(edit => edit.action),
    ['select_group', 'clear_group']
  );
  assert.deepEqual(panel._selected(), []);
  assert.equal(pressButton(doc).textContent, 'No changes selected');
});

test('"Leave here, stop asking" keeps the row\'s files, says so, and Review again brings them back', async () => {
  const { doc, calls } = await questionRowFixture(3);
  choose(rowMenu(doc), 'stop');
  await planSettled();
  const keep = draftEdits(calls).find(edit => edit.action === 'keep_group');
  assert.ok(keep, 'one keep request for the whole row');
  // The row is no longer waiting, so it is a note with a way back.
  const kept = surface(doc).all(node => node.getAttribute('data-fj-kept') === 'sensitive')[0];
  assert.match(kept.textContent, /Ori will not ask about these again\./);
  assert.equal(rowMenu(doc), null, 'nothing waiting, so no menu');
  surface(doc)
    .all(node => node.id === 'fileJanitorReviewAgain-sensitive')[0]
    .click();
  await planSettled();
  const again = draftEdits(calls).find(edit => edit.action === 'reconsider_files');
  assert.deepEqual(again.candidate_ids.sort(), [
    'group-member-0',
    'group-member-1',
    'group-member-2'
  ]);
  assert.equal(
    surface(doc).all(node => node.getAttribute('data-fj-kept') === 'sensitive').length,
    0
  );
  assert.match(surface(doc).textContent, /Ori will offer these again the next time it scans/);
});

test('stopping to ask leaves a file drawn elsewhere (a Trash mark) out of "Review again"', async () => {
  const { doc, calls, state } = await questionRowFixture(3);
  state.choices = [{ candidate_id: 'group-member-1', operation: 'trash' }];
  state.generation++;
  await panel._reloadBatch();
  await planSettled();
  choose(rowMenu(doc), 'stop');
  await planSettled();
  surface(doc)
    .all(node => node.id === 'fileJanitorReviewAgain-sensitive')[0]
    .click();
  await planSettled();
  const again = draftEdits(calls).find(edit => edit.action === 'reconsider_files');
  assert.deepEqual(again.candidate_ids.sort(), ['group-member-0', 'group-member-2']);
});

function fileControl(doc, index, kind = 'name', location = '') {
  return (
    surface(doc).all(
      node =>
        node.id?.startsWith('fileJanitorFileRow-' + location) &&
        node.id.endsWith(`--group-member-${index}-${kind}`)
    )[0] || null
  );
}

function openFileChoice(doc, index, location) {
  const name = fileControl(doc, index, 'name', location);
  name.focus();
  name.click();
  return fileControl(doc, index, 'choice', location);
}

test('focus stays on a row menu or an inline editor when the plan check redraws', async () => {
  const { doc } = await questionRowFixture(3);
  for (const before of [rowMenu(doc), fileControl(doc, 0)]) {
    doc.getElementById(before.id).focus();
    await panel._planNow();
    assert.equal(doc.activeElement, doc.getElementById(before.id));
  }
  const menu = openFileChoice(doc, 1);
  assert.equal(doc.activeElement, menu);
  await panel._planNow();
  assert.equal(doc.activeElement, fileControl(doc, 1, 'choice'));
});

test('when an inline choice removes a row, focus goes to the next file in that list', async () => {
  const { doc } = await groupedReviewFixture(3);
  choose(openFileChoice(doc, 1), 'trash');
  await planSettled();
  assert.equal(fileControl(doc, 1, 'choice'), null);
  assert.equal(doc.activeElement, fileControl(doc, 2, 'name', 'filed-images'));
});

test('cancel closes the inline editor without a write and returns focus to its filename', async () => {
  const { doc, calls } = await groupedReviewFixture(3);
  const menu = openFileChoice(doc, 2);
  assert.equal(menu.getAttribute('aria-label'), 'Filing choice for image-2.png');
  const footer = pictureHost(doc).children.find(node => node.className === 'fj-footer');
  assert.equal(footer.all(node => node.tagName === 'SELECT').length, 0);
  fileControl(doc, 2, 'cancel').click();
  assert.equal(doc.activeElement, fileControl(doc, 2));
  assert.equal(fileControl(doc, 2, 'choice'), null);
  assert.deepEqual(draftEdits(calls), []);
});

test('after "stop asking" focus is on the note that replaced the menu, and Review again says so beside the button', async () => {
  const { doc } = await questionRowFixture(2);
  choose(rowMenu(doc), 'stop');
  await planSettled();
  assert.equal(doc.activeElement, doc.getElementById('fileJanitorReviewAgain-sensitive'));
  doc.getElementById('fileJanitorReviewAgain-sensitive').click();
  await planSettled();
  // The sentence is in the footer next to the button (and announced), not at the top
  // of the page where the person is not looking; focus is not dropped.
  assert.equal(
    doc.getElementById('fileJanitorSelection').textContent,
    'Ori will offer these again the next time it scans.'
  );
  // Nothing is waiting, so the File button is disabled and cannot take focus: it
  // lands on the picture itself, and stays there through the next redraw.
  assert.equal(doc.activeElement, doc.getElementById('fileJanitorPicture'));
});

// ------------------------------------------ possible copies and music bundles

// Where each file of a card is now, from the fixture's saved choices.
function cardFile(state, candidates, index, extra = {}) {
  const candidate = candidates[index];
  const choice = state.choices.find(item => item.candidate_id === candidate.id);
  return {
    id: candidate.id,
    name: candidate.name,
    size: 2048,
    modified_at: '2026-11-10T12:00:00Z',
    placement: choice?.operation === 'trash' ? 'trash' : choice ? 'filed' : 'loose',
    category: candidate.decision_category || undefined,
    ...extra
  };
}

async function copiesFixture() {
  const fixture = await questionRowFixture(3);
  const { state, candidates } = fixture;
  Object.assign(state.review.groups[0], { key: 'versions', label: 'Possible copies' });
  const family = 'fam-' + 'a'.repeat(24);
  state.decorateRow = row => ({
    ...row,
    key: 'versions',
    singles: [],
    pairs: [
      {
        family_id: family,
        count: 2,
        more: 0,
        files: [
          cardFile(state, candidates, 0, { ending: 'none', family_id: family, viewer: 'Preview' }),
          cardFile(state, candidates, 1, { ending: 'number', family_id: family, viewer: 'Preview' })
        ]
      }
    ]
  });
  await panel._reloadBatch();
  await panel._planNow();
  return fixture;
}

test('Finder on either member of a pair reveals only that file, without redrawing or recording a choice', async () => {
  const { doc, state, calls, candidates } = await copiesFixture();
  const buttons = () =>
    surface(doc).all(node => node.textContent === 'Show in Finder' && node.tagName === 'BUTTON');
  assert.equal(buttons().length, 0, 'not offered without a local desktop');
  state.canReveal = true;
  await panel._reloadBatch();
  await panel._planNow();
  const rows = buttons();
  assert.equal(rows.length, 2);
  calls.length = 0;
  for (const button of rows) {
    button.click();
    await settle();
  }
  assert.deepEqual(
    calls.map(({ target, options }) => [
      target.split('/picture/files/')[1],
      options.method,
      options.headers['X-Ori-Filing-Action']
    ]),
    candidates
      .slice(0, 2)
      .map(c => [c.id + '/reveal?revision=' + state.review.revision, 'POST', 'reveal'])
  );
  const open = surface(doc).all(
    node =>
      node.textContent === 'Open' && node.tagName === 'BUTTON' && node.title === 'Open in Preview'
  );
  assert.equal(open.length, 2);
  calls.length = 0;
  open[1].click();
  await settle();
  assert.equal(calls.length, 1, 'open is not a draft edit, File press, or Finder reveal');
  assert.ok(
    calls[0].target.endsWith('/' + candidates[1].id + '/open?revision=' + state.review.revision)
  );
  assert.equal(calls[0].options.headers['X-Ori-Filing-Action'], 'open');
  assert.equal(rows[0], buttons()[0], 'preserves the list and its scroll position');
});

test('All files inspection uses its own page binding, does not write a choice, and ignores stale actions', async () => {
  const { doc, state, calls, candidates } = await groupedReviewFixture(53);
  Object.assign(candidates[50], { can_inspect: true, viewer: 'Preview' });
  Object.assign(candidates[51], { can_inspect: true });
  Object.assign(candidates[52], { can_inspect: true, viewer: 'TextEdit' });
  state.failPicture = true; // All files does not need a successful picture read.
  panel.close();
  panel.open({ source: 'folder-preparation', tab: 'review', item: candidates[0].id });
  await panel._reloadBatch();
  const controls = name =>
    surface(doc).all(node => node.tagName === 'BUTTON' && node.textContent === name);
  assert.equal(controls('Open').length, 0);
  state.inspectionRevision = state.review.revision;
  doc.getElementById('fileJanitorPageNext').click();
  await planSettled();
  assert.match(doc.getElementById('fileJanitorPagerStatus').textContent, /Showing 51–53/);
  assert.equal(controls('Open').length, 2);
  assert.equal(controls('Show in Finder').length, 3);
  const open = controls('Open')[0];
  const finder = controls('Show in Finder')[1];
  calls.length = 0;
  open.click();
  finder.click();
  await settle();
  assert.deepEqual(
    calls.map(({ target, options }) => [
      target.split('/picture/files/')[1],
      options.method,
      options.headers['X-Ori-Filing-Action']
    ]),
    [
      [`${candidates[50].id}/open?revision=${state.review.revision}`, 'POST', 'open'],
      [`${candidates[51].id}/reveal?revision=${state.review.revision}`, 'POST', 'reveal']
    ]
  );
  assert.equal(controls('Open')[0], open, 'inspection keeps page and row controls');

  const original = globalThis.fetch;
  let finish;
  globalThis.fetch = async (url, options) => {
    if (url.includes('/open?')) {
      return new Promise(resolve => {
        finish = () => resolve({ ok: false, status: 409 });
      });
    }
    return original(url, options);
  };
  open.click();
  doc.getElementById('fileJanitorPagePrev').click();
  await planSettled();
  finish();
  await settle();
  assert.doesNotMatch(surface(doc).textContent, /changed\. Scan again before inspecting/);
  globalThis.fetch = original;
  calls.length = 0;
  finder.click();
  await settle();
  assert.equal(calls.length, 0);

  state.inspectionRevision = 'b'.repeat(64);
  doc.getElementById('fileJanitorPageNext').click();
  await planSettled();
  assert.equal(controls('Open').length, 0, 'different membership from the draft');
});

test('Mark for Trash marks one file; Undo mark clears the choice without filing', async () => {
  const { doc, calls } = await copiesFixture();
  const card = surface(doc).all(node => String(node.className) === 'fj-pair')[0];
  assert.equal(
    card.all(node => node.className === 'fj-pair-hint')[0].textContent,
    'Same size, same day'
  );
  // The original has no button; the bracketed copy of the same size has one.
  assert.equal(fileControl(doc, 0, 'mark'), null);
  assert.equal(
    fileControl(doc, 0, 'choice').getAttribute('aria-label'),
    'Filing choice for image-0.png'
  );
  const markControl = fileControl(doc, 1, 'mark');
  markControl.focus();
  markControl.click();
  await planSettled();
  const [mark] = draftEdits(calls);
  assert.deepEqual([mark.action, mark.candidate_ids], ['mark_trash', ['group-member-1']]);
  assert.equal(mark.group_id, undefined, 'one file, never a row or a pair');
  const line = surface(doc).all(node => node.className === 'fj-pair-line')[1];
  assert.match(line.textContent, /Marked for Trash/);
  assert.match(surface(doc).textContent, /Trash · 1/);
  assert.equal(pressButton(doc).textContent, 'Move 1 file to Trash');
  assert.ok(
    !calls.some(call => /\/file$/.test(call.target)),
    'nothing goes to the Trash until File'
  );
  const undo = fileControl(doc, 1, 'undo');
  undo.focus();
  undo.click();
  await planSettled();
  assert.equal(draftEdits(calls)[1].action, 'unmark_trash');
  assert.doesNotMatch(surface(doc).textContent, /Trash · 1/);
  assert.ok(fileControl(doc, 1, 'mark'), 'the button is back');
  assert.equal(
    doc.activeElement.closest('[data-fj-file-row]').id,
    fileControl(doc, 1, 'choice').parentElement.id
  );
  assert.equal(pressButton(doc).textContent, 'No changes selected');
});

test('a copy of a different size gets no Trash button', async () => {
  const { doc, state, candidates } = await copiesFixture();
  const family = 'fam-' + 'a'.repeat(24);
  state.decorateRow = row => ({
    ...row,
    singles: [],
    pairs: [
      {
        family_id: family,
        count: 2,
        more: 0,
        files: [
          cardFile(state, candidates, 0, { ending: 'none', family_id: family }),
          cardFile(state, candidates, 1, { ending: 'number', family_id: family, size: 4096 })
        ]
      }
    ]
  });
  await panel._reloadBatch();
  assert.equal(fileControl(doc, 1, 'mark'), null);
  assert.match(surface(doc).textContent, /Different size/);
});

async function bundleFixtureOnPage(more = false) {
  const fixture = await questionRowFixture(3);
  const { state, candidates } = fixture;
  Object.assign(state.review.groups[0], { key: 'audio', label: 'Music and audio' });
  const bundleId = 'bun-' + 'b'.repeat(24);
  const members = () =>
    [0, 1, 2].map(index => cardFile(state, candidates, index, { bundle_id: bundleId }));
  state.decorateRow = row => {
    const all = members();
    const shown = more ? all.slice(0, 1) : all;
    const count = placement => all.filter(file => file.placement === placement).length;
    const placedIn = [
      ...new Set(all.filter(file => file.placement === 'filed').map(file => file.category))
    ];
    return {
      ...row,
      key: 'audio',
      singles: [],
      bundles: [
        {
          bundle_id: bundleId,
          name: 'Song 130',
          count: all.length,
          more: all.length - shown.length,
          files: shown,
          loose_count: count('loose'),
          placed_count: count('filed'),
          placed_category: placedIn.length === 1 ? placedIn[0] : '',
          trash_count: count('trash')
        }
      ]
    };
  };
  state.sectionFiles = { bundle: members };
  await panel._reloadBatch();
  await panel._planNow();
  return { ...fixture, bundleId };
}

test('one choice on a bundle moves every file in it to one folder, and the card says where they are (R32)', async () => {
  const { doc, calls, bundleId } = await bundleFixtureOnPage();
  const title = surface(doc).all(node => node.className === 'fj-bundle-title')[0];
  assert.equal(title.textContent, 'Song 130 · 3 files · moves or stays together');
  choose(doc.getElementById('fileJanitorBundleMenu-' + bundleId), 'move:documents');
  await planSettled();
  const [edit] = draftEdits(calls);
  assert.equal(edit.action, 'select_files');
  assert.deepEqual(edit.candidate_ids.sort(), [
    'group-member-0',
    'group-member-1',
    'group-member-2'
  ]);
  assert.equal(edit.category, 'documents');
  assert.equal(
    surface(doc).all(node => node.className === 'fj-row-note fj-bundle-note')[0].textContent,
    'All in Filed/Documents'
  );
  assert.equal(pressButton(doc).textContent, 'File 3 files');
});

test('a bundle with more files than the card shows still moves all of them', async () => {
  const { doc, calls, bundleId } = await bundleFixtureOnPage(true);
  choose(doc.getElementById('fileJanitorBundleMenu-' + bundleId), 'move:archives');
  await planSettled();
  assert.ok(
    calls.some(call => call.target.includes('/picture/files?section=bundle&key=' + bundleId)),
    'the rest of the bundle was read before the choice'
  );
  const [edit] = draftEdits(calls);
  assert.deepEqual(edit.candidate_ids.sort(), [
    'group-member-0',
    'group-member-1',
    'group-member-2'
  ]);
});

test('a bundle split by single-file choices says how it is split (R33), and Leave here takes it all back', async () => {
  const { doc, calls, bundleId } = await bundleFixtureOnPage();
  // One of the three goes to Documents by itself, from the one-file bar.
  choose(openFileChoice(doc, 0, 'bun-'), 'move:documents');
  await planSettled();
  const note = surface(doc).all(node => node.className === 'fj-row-note fj-bundle-note')[0];
  assert.equal(note.textContent, 'Split: 2 stay here, 1 in Filed/Documents');
  assert.equal(
    doc.getElementById('fileJanitorBundleMenu-' + bundleId).children.find(option => option.selected)
      .value,
    'mixed'
  );
  choose(doc.getElementById('fileJanitorBundleMenu-' + bundleId), 'leave');
  await planSettled();
  const edits = draftEdits(calls);
  assert.deepEqual(
    [edits[1].action, edits[1].candidate_ids],
    ['exclude_files', ['group-member-0']]
  );
  assert.equal(
    surface(doc).all(node => node.className === 'fj-row-note fj-bundle-note').length,
    0,
    'all together again: nothing to say'
  );
});

test('a bundle choice never overwrites a file already marked for Trash', async () => {
  const { doc, calls, state, bundleId } = await bundleFixtureOnPage();
  state.choices = [{ candidate_id: 'group-member-1', operation: 'trash' }];
  state.generation++;
  await panel._reloadBatch();
  await planSettled();
  choose(doc.getElementById('fileJanitorBundleMenu-' + bundleId), 'move:documents');
  await planSettled();
  const [edit] = draftEdits(calls);
  assert.deepEqual(edit.candidate_ids.sort(), ['group-member-0', 'group-member-2']);
});

test('a row menu is off while a save is in flight', async () => {
  const { doc, state } = await questionRowFixture(2);
  state.editGate = new Promise(() => {});
  choose(rowMenu(doc), 'move:documents');
  await settle();
  assert.equal(rowMenu(doc).disabled, true);
});

test('choosing a new destination beside a filename edits that file only', async () => {
  const { doc, calls } = await groupedReviewFixture(3);
  const menu = openFileChoice(doc, 1);
  assert.equal(fileControl(doc, 1).getAttribute('aria-expanded'), 'true');
  assert.deepEqual(
    menu.children.map(option => option.value),
    ['', 'move:documents', 'move:archives', 'move:other', 'leave', 'stop', 'trash']
  );
  assert.equal(doc.activeElement, menu, 'focus goes to the menu');
  choose(menu, 'move:documents');
  await planSettled();
  const [edit] = draftEdits(calls);
  assert.deepEqual(
    [edit.action, edit.candidate_ids, edit.category],
    ['select_files', ['group-member-1'], 'documents']
  );
  assert.equal(fileControl(doc, 1, 'choice'), null, 'the inline editor closes');
  // The file shows in its new folder, tagged as the person's choice.
  const documents = surface(doc).all(
    node => node.getAttribute('data-fj-folder') === 'documents'
  )[0];
  assert.match(documents.textContent, /image-1\.png/);
  assert.equal(pressButton(doc).textContent, 'File 3 files');
});

test('one file can be marked, unmarked, or left out from its inline choice', async () => {
  const { doc, calls } = await groupedReviewFixture(3);
  choose(openFileChoice(doc, 0), 'trash');
  await planSettled();
  assert.match(surface(doc).textContent, /Trash · 1/);
  assert.equal(pressButton(doc).textContent, 'File 2 files · 1 to Trash');
  // Putting it back is "Leave here" on a trashed file.
  const trashMenu = openFileChoice(doc, 0, 'trash');
  assert.ok(!trashMenu.children.some(option => option.value === 'trash'), 'already in Trash');
  choose(trashMenu, 'leave');
  await planSettled();
  assert.doesNotMatch(surface(doc).textContent, /Trash · /);
  // A file taken out of a ticked folder is listed as left out by the person.
  choose(openFileChoice(doc, 2), 'leave');
  await planSettled();
  assert.match(surface(doc).textContent, /Left out by you · 2/);
  assert.deepEqual(
    draftEdits(calls).map(edit => edit.action),
    ['mark_trash', 'unmark_trash', 'exclude_files']
  );
});

test('"Leave here, stop asking" on one file keeps that file only', async () => {
  const { doc, calls } = await groupedReviewFixture(3);
  choose(openFileChoice(doc, 1), 'stop');
  await planSettled();
  const [edit] = draftEdits(calls);
  assert.deepEqual([edit.action, edit.candidate_ids], ['keep_files', ['group-member-1']]);
  assert.equal(pressButton(doc).textContent, 'File 2 files');
});

test('a second-page filename keeps its own editor and stale choices cannot edit a newer revision', async () => {
  const { doc, calls, state } = await groupedReviewFixture(53);
  doc.getElementById('fileJanitorMore-filed-images').click();
  await settle();
  const page = () => doc.getElementById('fileJanitorMorePanel');
  const first = openFileChoice(doc, 0, 'page-');
  assert.notEqual(fileControl(doc, 0, 'name', 'filed').id, fileControl(doc, 0, 'name', 'page-').id);
  assert.equal(first.parentElement.parentElement.id.endsWith('--group-member-0'), true);
  assert.equal(fileControl(doc, 0, 'choice', 'filed'), null);
  buttonNamed(page(), 'Next files').click();
  await settle();
  const oldMenu = openFileChoice(doc, 51, 'page-');
  await panel._planNow();
  assert.equal(doc.activeElement, fileControl(doc, 51, 'choice', 'page-'));
  state.generation++;
  await panel._reloadBatch();
  choose(oldMenu, 'trash');
  await settle();
  assert.deepEqual(draftEdits(calls), []);
  const currentMenu = fileControl(doc, 51, 'choice', 'page-');
  currentMenu.focus();
  choose(currentMenu, 'stop');
  await planSettled();
  assert.deepEqual(draftEdits(calls)[0].candidate_ids, ['group-member-51']);
  assert.equal(doc.activeElement, fileControl(doc, 52, 'name', 'page-'));
});

test('a failed single-file save leaves its editor and the canonical plan unapplied', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.failNextEdit = true;
  const menu = openFileChoice(doc, 0);
  choose(menu, 'trash');
  await settle();
  assert.equal(fileControl(doc, 0, 'choice').disabled, true);
  assert.equal(pressButton(doc).disabled, true);
  assert.equal(state.fileRequests.length, 0);
  assert.equal(state.choices[0].operation, 'move');
});

test('a file an earlier attempt could not move offers Try again, and it comes back unticked', async () => {
  const { doc, state, candidates, calls } = await groupedReviewFixture(3);
  // One file failed on the way: it is out of the waiting lists and listed under
  // "Could not be filed" with its reason.
  const failed = candidates[0];
  failed.state = 'failed';
  state.couldNot.push(failed);
  state.choices = state.choices.filter(choice => choice.candidate_id !== failed.id);
  state.generation++;
  await panel._reloadBatch();
  await planSettled();
  assert.match(surface(doc).textContent, /Could not be filed · 1/);
  assert.match(surface(doc).textContent, /Ori could not move this file\./);
  const retry = doc.getElementById('fileJanitorRetry-group-member-0');
  assert.equal(retry.getAttribute('aria-label'), 'Try image-0.png again');
  assert.equal(
    pressButton(doc).textContent,
    'File 2 files',
    'a failed file is not part of the next press'
  );
  retry.click();
  await planSettled();
  const [edit] = draftEdits(calls);
  assert.deepEqual([edit.action, edit.candidate_ids], ['retry_files', ['group-member-0']]);
  assert.doesNotMatch(surface(doc).textContent, /Could not be filed/);
  // It is waiting again but not ticked: trying again never files anything by itself.
  assert.equal(panel._selected().length, 2);
  assert.match(surface(doc).textContent, /Left out by you · 1/);
  assert.equal(pressButton(doc).textContent, 'File 2 files');
});

test('an answer that arrives after the folder was replaced is ignored', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.fileGate = new Promise(() => {});
  pressButton(doc).click();
  await settle();
  const replacement = statusFixture();
  replacement.settings.root_id = 'replacement';
  panel.render(replacement);
  state.fileGate = null;
  await settle();
  assert.equal(doc.getElementById('fileJanitorResultsSummary'), null);
  assert.doesNotMatch(text(doc), /3 files filed/);
});

async function pressedFixture(count = 3) {
  const fixture = await groupedReviewFixture(count);
  pressButton(fixture.doc).click();
  await settle();
  await settle();
  return fixture;
}

const okReply = body => ({ ok: true, json: async () => body });

test('the receipt is the journal: count filed, where, how many stay loose, one row per destination (R43)', async () => {
  const { doc, calls } = await pressedFixture(3);
  assert.equal(doc.getElementById('fileJanitorResultsSummary').textContent, '3 files filed.');
  const body = text(doc);
  assert.match(body, /They are in Inbox\/Filed\. 7 files stay loose in Inbox\./);
  assert.match(body, /3 → Filed\/Images/);
  assert.ok(doc.getElementById('fileJanitorUndoAll'));
  assert.ok(doc.getElementById('fileJanitorReceiptDone'));
  assert.ok(
    calls.some(call => /\/filings\/filing-1$/.test(call.target)),
    'the numbers are read back from what happened, not recounted from the plan'
  );
});

test('when the receipt cannot be read, the answer to the press is drawn, without a loose count', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.receiptGone = true;
  pressButton(doc).click();
  await settle();
  await settle();
  assert.equal(doc.getElementById('fileJanitorResultsSummary').textContent, '3 files filed.');
  assert.doesNotMatch(text(doc), /stay loose/);
  assert.ok(doc.getElementById('fileJanitorUndoAll'));
});

test('Done closes the receipt and puts focus back on the picture', async () => {
  const { doc } = await pressedFixture(3);
  doc.getElementById('fileJanitorReceiptDone').click();
  assert.equal(doc.getElementById('fileJanitorResultsSummary'), null);
  assert.equal(doc.getElementById('fileJanitorUndoAll'), null);
  // Every file was filed, so there is no picture left to draw: focus is on a control
  // of the review (Scan now), not dropped to the page.
  assert.notEqual(doc.activeElement, doc.body);
  assert.equal(doc.activeElement, doc.getElementById('fileJanitorScan'));
});

test('focus follows the account: the receipt, then the Undone view, never the page top (R65)', async () => {
  const { doc } = await pressedFixture(3);
  assert.equal(doc.activeElement, doc.getElementById('fileJanitorReceipt'));
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  assert.equal(
    doc.activeElement,
    doc.getElementById('fileJanitorUndone'),
    'after Undo all focus is on the answer, not dropped to the page'
  );
});

test('a refused or lost Undo all leaves focus on the receipt, not the page top', async () => {
  const { doc, state } = await pressedFixture(3);
  state.undoReply = () => {
    throw new Error('Connection lost.');
  };
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  assert.equal(doc.activeElement, doc.getElementById('fileJanitorReceipt'));
});

test('Undo all asks once with no body, shows how many are back, and Ori scans only on the button (R47, R49, R52)', async () => {
  const { doc, calls } = await pressedFixture(3);
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  const undoCalls = calls.filter(call => /\/filings\/filing-1\/undo$/.test(call.target));
  assert.equal(undoCalls.length, 1);
  assert.equal(undoCalls[0].options.method, 'POST');
  assert.equal(undoCalls[0].options.body, undefined, 'nothing the page sends can widen it');
  assert.equal(
    doc.getElementById('fileJanitorUndoneTitle').textContent,
    '3 files are back where they were.'
  );
  assert.equal(doc.getElementById('fileJanitorUndoAll'), null, 'it is not offered twice');
  assert.equal(
    calls.filter(call => /\/scan$/.test(call.target)).length,
    0,
    'putting files back never scans'
  );
  const before = calls.length;
  doc.getElementById('fileJanitorScanAgain').click();
  await settle();
  await settle();
  const after = calls.slice(before).map(call => call.target);
  const forgetAt = after.findIndex(target => /\/filings\/filing-1\/forget-restored$/.test(target));
  const scanAt = after.findIndex(target => /\/scan$/.test(target));
  assert.ok(forgetAt >= 0, 'Ori first forgets that it put these files back');
  assert.ok(scanAt > forgetAt, 'and only then runs the ordinary scan');
  assert.equal(doc.getElementById('fileJanitorUndone'), null, 'the Undone view is closed');
});

test('Scan again after Undo all takes the new scan, not the finished review it was pinned to', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  // The server keeps the finished review as the open one; it is not "no review".
  const filed = state.fileReply;
  state.fileReply = request => {
    const answer = filed(request);
    state.emptied = false;
    return answer;
  };
  pressButton(doc).click();
  await settle();
  await settle();
  state.scanReply = { success: true, created: true, batch: { id: 'batch-after-undo' } };
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  doc.getElementById('fileJanitorScanAgain').click();
  await settle();
  await settle();
  // An open review is normally kept when a newer scan appears. Here the person
  // asked for the new scan, so it must not answer "a newer review is available".
  assert.doesNotMatch(text(doc), /A newer scan is ready/);
});

test('an ordinary Scan now still keeps the open review and says a newer one exists', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.scanReply = { success: true, created: true, batch: { id: 'batch-newer' } };
  doc.getElementById('fileJanitorScan').click();
  await settle();
  await settle();
  assert.match(text(doc), /A newer scan is ready/);
});

test('a failed forget does not scan, and the button stays', async () => {
  const { doc, calls, state } = await pressedFixture(3);
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  state.forgetFails = true;
  doc.getElementById('fileJanitorScanAgain').click();
  await settle();
  await settle();
  assert.equal(calls.filter(call => /\/scan$/.test(call.target)).length, 0);
  assert.ok(doc.getElementById('fileJanitorScanAgain'));
  assert.match(text(doc), /Ori could not forget those files\./);
});

test('one original name taken: that file is listed with why and where it is, and the rest are back (R49)', async () => {
  const { doc, state } = await pressedFixture(3);
  state.undoReply = filing =>
    okReply({
      success: true,
      undo: {
        filing_id: filing.id,
        back: 2,
        not_back: 1,
        outcomes: [
          {
            name: 'image-0.png',
            result: 'failed',
            message:
              'Something else is already using the original name, so Ori did not overwrite it.',
            location: 'filed',
            destination: 'Filed/Images/image-0.png'
          },
          { name: 'image-1.png', result: 'undone' },
          { name: 'image-2.png', result: 'undone' }
        ]
      }
    });
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  assert.equal(
    doc.getElementById('fileJanitorUndoneTitle').textContent,
    '2 files are back where they were.'
  );
  const body = text(doc);
  assert.match(body, /1 file could not be put back/);
  assert.match(body, /image-0\.png\s+— Something else is already using the original name/);
  assert.match(body, /It is still in Filed\/Images\/image-0\.png\./);
  assert.ok(doc.getElementById('fileJanitorScanAgain'), 'the files that came back can be reviewed');
});

test('Undo all is refused as unavailable: nothing is claimed, and the receipt stops offering it', async () => {
  const { doc, state } = await pressedFixture(3);
  state.filings[0].undone = true; // another window already did it
  state.undoReply = () => ({
    ok: false,
    status: 409,
    json: async () => ({ error: { code: 'undo_unavailable', message: 'x' } })
  });
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  assert.match(text(doc), /Undo all is not available for this filing any more/);
  assert.equal(doc.getElementById('fileJanitorUndoAll'), null);
  assert.equal(doc.getElementById('fileJanitorResultsSummary').textContent, '3 files filed.');
});

test('a lost Undo all request says so and leaves the button for a retry', async () => {
  const { doc, state } = await pressedFixture(3);
  state.undoReply = () => {
    throw new Error('Connection lost.');
  };
  doc.getElementById('fileJanitorUndoAll').click();
  await settle();
  await settle();
  assert.match(text(doc), /Connection lost\./);
  const again = doc.getElementById('fileJanitorUndoAll');
  assert.ok(again);
  assert.equal(again.disabled, false);
  assert.equal(doc.getElementById('fileJanitorUndone'), null, 'nothing was claimed as put back');
});

test('while Undo all runs the buttons wait and say so, and a second press does nothing', async () => {
  const { doc, calls, state } = await pressedFixture(3);
  let release;
  state.undoGate = new Promise(resolve => {
    release = resolve;
  });
  const undo = doc.getElementById('fileJanitorUndoAll');
  undo.click();
  await settle();
  const running = doc.getElementById('fileJanitorUndoAll');
  assert.equal(running.disabled, true);
  assert.equal(running.textContent, 'Putting files back…');
  assert.equal(doc.getElementById('fileJanitorReceiptDone').disabled, true);
  running.click();
  await settle();
  release();
  await settle();
  await settle();
  assert.equal(calls.filter(call => /\/undo$/.test(call.target)).length, 1);
  assert.ok(doc.getElementById('fileJanitorUndoneTitle'));
});

test('History says what the last filing was, and Undo all there works after the receipt is closed (R46)', async () => {
  const { doc, calls } = await pressedFixture(3);
  doc.getElementById('fileJanitorReceiptDone').click();
  panel._selectTab('history');
  await panel._reloadHistory();
  await settle();
  const line = doc.getElementById('fileJanitorLastFiling');
  assert.match(line.textContent, /^Last filing · 3 files · /);
  assert.ok(doc.getElementById('fileJanitorLastFilingUndo'));
  doc.getElementById('fileJanitorLastFilingUndo').click();
  await settle();
  await settle();
  assert.equal(calls.filter(call => /\/filings\/filing-1\/undo$/.test(call.target)).length, 1);
  // The answer is shown on the Review tab, where the one button for what comes
  // next lives.
  assert.equal(
    doc.getElementById('fileJanitorUndoneTitle').textContent,
    '3 files are back where they were.'
  );
  panel._selectTab('history');
  await panel._reloadHistory();
  await settle();
  assert.match(doc.getElementById('fileJanitorLastFiling').textContent, /put back$/);
  assert.equal(doc.getElementById('fileJanitorLastFilingUndo'), null);
});

test('results survive the batch they emptied, and the empty batch says why', async () => {
  const { doc } = await groupedReviewFixture(3);
  pressButton(doc).click();
  await settle();
  await settle();
  const body = text(doc);
  assert.match(body, /3 files filed/, 'the outcome must remain visible after the batch empties');
  assert.match(body, /3 → Filed\/Images/, 'one row for the destination, not one line per file');
  assert.match(body, /Nothing to file\. Inbox is tidy\./);
});

test('the account of a press survives a redraw of the console, and goes with the tab or a new press', async () => {
  const { doc } = await groupedReviewFixture(3);
  pressButton(doc).click();
  await settle();
  await settle();
  assert.match(doc.getElementById('fileJanitorResultsSummary').textContent, /3 files filed/);
  // A status refresh that lands after the press redraws the whole console.
  const status = statusFixture();
  status.settings.root_id = 'root-one';
  panel.render(status);
  await settle();
  assert.match(
    doc.getElementById('fileJanitorResultsSummary').textContent,
    /3 files filed/,
    'a redraw must not wipe what the press reported'
  );
  // Leaving the Review tab is the end of it.
  panel._selectTab('history');
  panel._selectTab('review');
  assert.equal(doc.getElementById('fileJanitorResultsSummary'), null);
});

test('authoritative results survive when their follow-up batch refresh fails', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  const filed = state.fileReply;
  state.fileReply = request => {
    const reply = filed(request);
    state.emptied = false;
    state.failBatchRead = true;
    return reply;
  };
  pressButton(doc).click();
  await settle();
  await settle();
  const body = text(doc);
  assert.match(body, /Review unavailable/);
  assert.match(body, /3 files filed/);
  assert.doesNotMatch(body, /Nothing to review/);
});

test('mixed outcomes name moves, Trash, failures, stale files, and replay truthfully', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.fileReply = () => ({
    ok: true,
    json: async () => ({
      filing: {
        filing_id: 'filing-1',
        replayed: true,
        outcomes: [
          {
            candidate_id: 'c1',
            name: 'invoice.pdf',
            operation: 'move',
            result: 'applied',
            destination: 'Filed/Documents/invoice.pdf'
          },
          { candidate_id: 'c2', name: 'old.zip', operation: 'trash', result: 'applied' },
          {
            candidate_id: 'legacy',
            name: 'older-response.dat',
            result: 'applied',
            destination: 'Filed/Documents/older-response.dat'
          },
          {
            candidate_id: 'c3',
            name: 'blocked.pdf',
            operation: 'move',
            result: 'failed',
            message: 'Permission denied.'
          },
          {
            candidate_id: 'c4',
            name: 'changed.pdf',
            operation: 'move',
            result: 'stale',
            message: 'Changed after review.'
          }
        ]
      }
    })
  });
  pressButton(doc).click();
  await settle();
  await settle();
  // The receipt could not be read here (this stub has no filings route for it), so
  // the answer to the press is drawn: the count of what actually happened, the
  // Trash and an unclassified change apart from "filed", and every file that did not
  // move listed with its reason.
  const body = text(doc);
  assert.equal(
    doc.getElementById('fileJanitorResultsSummary').textContent,
    '1 file filed · 1 moved to Trash · 1 change completed.'
  );
  assert.match(body, /1 → Filed\/Documents/);
  assert.match(body, /1 → Trash/);
  assert.match(body, /2 files were not moved/);
  assert.match(body, /blocked\.pdf\s+— Permission denied\./);
  assert.match(body, /changed\.pdf\s+— Changed after review\./);
  assert.match(body, /Already completed earlier; nothing ran again/);
  assert.doesNotMatch(body, /older-response\.dat/);
  assert.ok(doc.getElementById('fileJanitorUndoAll'), 'a filed file can be put back');
  assert.ok(doc.getElementById('fileJanitorReceiptDone'));
});

test('a file that changed since the press is listed as not moved, with the reason', async () => {
  const { doc, state } = await groupedReviewFixture(3);
  state.fileReply = () => ({
    ok: true,
    json: async () => ({
      filing: {
        filing_id: 'filing-1',
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
  });
  pressButton(doc).click();
  await settle();
  await settle();
  const body = text(doc);
  assert.match(body, /Nothing was filed/);
  assert.match(body, /1 file was not moved/);
  assert.match(body, /invoice-2026-07\.pdf\s+— This file changed after you approved it/);
  assert.match(body, /Ori left it alone/);
  // Nothing moved, so there is nothing to put back.
  assert.equal(doc.getElementById('fileJanitorUndoAll'), null);
  assert.ok(doc.getElementById('fileJanitorReceiptDone'));
});
