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
  }
  get textContent() { return this._text; }
  set textContent(v) { this._text = v; if (v === '') this.children = []; }
  appendChild(el) { this.children.push(el); return el; }
  addEventListener(ev, fn) { (this._listeners[ev] ||= []).push(fn); }
  click() { (this._listeners.click || []).forEach((fn) => fn()); }
  querySelectorAll(sel) {
    if (sel === 'button') return this.children.filter((c) => c.tagName === 'BUTTON');
    return [];
  }
}

class FakeDocument {
  constructor() { this.byId = new Map(); }
  register(el) { this.byId.set(el.id, el); }
  getElementById(id) { return this.byId.get(id) || null; }
  createElement(tag) { return new FakeElement(tag); }
}

function setup() {
  const doc = new FakeDocument();
  ['reaperSetupCard', 'reaperSetupStatusText', 'reaperSetupBadge', 'reaperSetupDetail', 'reaperSetupActions'].forEach((id) => {
    const el = new FakeElement('div');
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

test('non-reaper template hides the card', () => {
  const doc = setup();
  mod.showForTemplate({ id: 'writing-project' });
  assert.equal(doc.getElementById('reaperSetupCard').hidden, true);
});

test('ready_to_attach shows attach message and no action', async () => {
  const doc = setup();
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({ status: 'ready_to_attach', would_attach: [{ name: 'reaper-session-setup' }, { name: 'reaper-web-remote' }] }),
  });
  await mod.refresh();
  const card = doc.getElementById('reaperSetupCard');
  const status = doc.getElementById('reaperSetupStatusText');
  const actions = doc.getElementById('reaperSetupActions');
  assert.equal(card.hidden, false);
  assert.match(status.textContent, /will attach/i);
  assert.equal(actions.querySelectorAll('button').length, 0);
});

test('plugin_disabled offers an Enable action', async () => {
  const doc = setup();
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ status: 'plugin_disabled', would_attach: [] }) });
  await mod.refresh();
  const actions = doc.getElementById('reaperSetupActions');
  const buttons = actions.querySelectorAll('button');
  assert.equal(buttons.length, 1);
  assert.equal(buttons[0].textContent, 'Enable plugin');
});

test('plugin_missing shows file-only + Install action, never blocks creation', async () => {
  const doc = setup();
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ status: 'plugin_missing', would_attach: [] }) });
  await mod.refresh();
  const status = doc.getElementById('reaperSetupStatusText');
  const actions = doc.getElementById('reaperSetupActions');
  assert.match(status.textContent, /file-only/i);
  assert.equal(actions.querySelectorAll('button')[0].textContent, 'Install plugin');
});

test('fetch failure renders a Retry control', async () => {
  const doc = setup();
  globalThis.fetch = async () => ({ ok: false });
  await mod.refresh();
  const actions = doc.getElementById('reaperSetupActions');
  assert.equal(actions.querySelectorAll('button')[0].textContent, 'Retry');
});
