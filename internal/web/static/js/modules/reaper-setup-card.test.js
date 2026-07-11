// Tests for reaper-setup-card.js — the Create Workspace REAPER Setup card.
// Uses a small inline DOM stub (no jsdom), matching the rest of the suite.
//
// Run with: node --test internal/web/static/js/modules/reaper-setup-card.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = (tag || 'div').toUpperCase();
    this.hidden = false;
    this._text = '';
    this.className = '';
    this.style = {};
    this.disabled = false;
    this.type = '';
    this.children = [];
    this._listeners = {};
    this.dataset = {};
    this._attrs = {};
    this.title = '';
  }
  setAttribute(k, v) {
    this._attrs[k] = v;
  }
  removeAttribute(k) {
    delete this._attrs[k];
  }
  get textContent() {
    return this._text;
  }
  set textContent(v) {
    this._text = v;
    if (v === '') this.children = [];
  }
  appendChild(el) {
    this.children.push(el);
    return el;
  }
  addEventListener(ev, fn) {
    (this._listeners[ev] ||= []).push(fn);
  }
  click() {
    (this._listeners.click || []).forEach(fn => fn());
  }
  querySelectorAll(sel) {
    if (sel === 'button') return this.children.filter(c => c.tagName === 'BUTTON');
    return [];
  }
}

class FakeDocument {
  constructor() {
    this.byId = new Map();
  }
  register(el) {
    this.byId.set(el.id, el);
  }
  getElementById(id) {
    return this.byId.get(id) || null;
  }
  createElement(tag) {
    return new FakeElement(tag);
  }
}

function setup() {
  const doc = new FakeDocument();
  [
    'reaperSetupCard',
    'reaperSetupStatusText',
    'reaperSetupBadge',
    'reaperSetupDetail',
    'reaperSetupActions',
    'createFolderBtn'
  ].forEach(id => {
    const el = new FakeElement(id === 'createFolderBtn' ? 'button' : 'div');
    el.id = id;
    doc.register(el);
  });
  globalThis.document = doc;
  globalThis.window = globalThis;
  return doc;
}

const mod = await (async () => {
  setup();
  await import('./reaper-setup-card.js');
  return globalThis.window.ReaperSetupCard;
})();

test('non-reaper template hides the card and never blocks creation', () => {
  const doc = setup();
  doc.getElementById('createFolderBtn').disabled = true; // pretend a stale block
  mod.showForTemplate({ id: 'writing-project' });
  assert.equal(doc.getElementById('reaperSetupCard').hidden, true);
  assert.equal(doc.getElementById('createFolderBtn').disabled, false);
});

test('ready_to_attach shows attach message and no action', async () => {
  const doc = setup();
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({
      status: 'ready_to_attach',
      would_attach: [{ name: 'reaper-session-setup' }, { name: 'reaper-web-remote' }]
    })
  });
  await mod.refresh();
  const card = doc.getElementById('reaperSetupCard');
  const status = doc.getElementById('reaperSetupStatusText');
  const actions = doc.getElementById('reaperSetupActions');
  assert.equal(card.hidden, false);
  assert.match(status.textContent, /will attach/i);
  assert.equal(actions.querySelectorAll('button').length, 0);
  // Plugin ready: creation is allowed.
  assert.equal(doc.getElementById('createFolderBtn').disabled, false);
});

test('plugin_disabled offers Enable and blocks creation', async () => {
  const doc = setup();
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({ status: 'plugin_disabled', would_attach: [] })
  });
  await mod.refresh();
  const actions = doc.getElementById('reaperSetupActions');
  const labels = actions.querySelectorAll('button').map(b => b.textContent);
  assert.ok(labels.includes('Enable plugin'));
  // Required plugin disabled: creation is blocked until it's enabled.
  assert.equal(doc.getElementById('createFolderBtn').disabled, true);
});

test('plugin_missing shows Install and blocks creation', async () => {
  const doc = setup();
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({ status: 'plugin_missing', would_attach: [] })
  });
  await mod.refresh();
  const status = doc.getElementById('reaperSetupStatusText');
  const actions = doc.getElementById('reaperSetupActions');
  assert.match(status.textContent, /required and not installed/i);
  assert.ok(
    actions
      .querySelectorAll('button')
      .map(b => b.textContent)
      .includes('Install plugin')
  );
  // Required plugin missing: creation is blocked until it's installed.
  assert.equal(doc.getElementById('createFolderBtn').disabled, true);
});

test('fetch failure renders Retry and does not hard-block creation', async () => {
  const doc = setup();
  globalThis.fetch = async () => ({ ok: false });
  await mod.refresh();
  const actions = doc.getElementById('reaperSetupActions');
  assert.equal(actions.querySelectorAll('button')[0].textContent, 'Retry');
  // On a transient preview error the backend guard still enforces the rule, so
  // the button is not left permanently disabled by the card.
  assert.equal(doc.getElementById('createFolderBtn').disabled, false);
});
