// Tests for reaper-readiness-panel.js — the durable workspace-detail REAPER
// readiness card + compact indicator. Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/reaper-readiness-panel.test.js

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
    this.children = [];
    this._attrs = {};
    this._listeners = {};
    this.classList = {
      _set: new Set(),
      toggle: (c, on) => {
        if (on) this.classList._set.add(c);
        else this.classList._set.delete(c);
      },
      contains: c => this.classList._set.has(c)
    };
    this.tabIndex = 0;
  }
  get textContent() {
    if (this.children.length) return this._text + this.children.map(c => c.textContent).join('');
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
  setAttribute(k, v) {
    this._attrs[k] = v;
  }
  scrollIntoView() {}
  focus() {}
  querySelectorAll(sel) {
    if (sel === 'button') return this.children.filter(c => c.tagName === 'BUTTON');
    return [];
  }
}

class FakeDocument {
  constructor() {
    this.byId = new Map();
    this.readyState = 'complete';
  }
  register(id) {
    const el = new FakeElement('div');
    el.id = id;
    this.byId.set(id, el);
    return el;
  }
  getElementById(id) {
    return this.byId.get(id) || null;
  }
  createElement(tag) {
    return new FakeElement(tag);
  }
  addEventListener() {}
}

function setup() {
  const doc = new FakeDocument();
  [
    'reaperReadinessCard',
    'reaperReadinessStatus',
    'reaperReadinessBadge',
    'reaperReadinessRows',
    'reaperReadinessActions',
    'reaperReadinessChip'
  ].forEach(id => doc.register(id));
  globalThis.document = doc;
  globalThis.window = globalThis;
  globalThis.window.currentWorkspaceId = 'ws-1';
  return doc;
}

const panel = await (async () => {
  setup();
  await import('./reaper-readiness-panel.js');
  return globalThis.window.ReaperReadinessPanel;
})();

test('unidentified workspace hides card and chip', () => {
  const doc = setup();
  panel.render({ identified: false });
  assert.equal(doc.getElementById('reaperReadinessCard').hidden, true);
  assert.equal(doc.getElementById('reaperReadinessChip').hidden, true);
});

test('plugin_missing renders rows, repair action, and setup-needed chip', () => {
  const doc = setup();
  panel.render({
    identified: true,
    status: 'plugin_missing',
    project_mode: 'file_only',
    plugin_installed: false,
    plugin_enabled: false,
    plugin_attached: false,
    setup_agent: 'Reaper Producer',
    setup_agent_is_cli: true,
    workspace_native_cli_enabled: false,
    agent_native_cli_enabled: false,
    has_pending_setup_task: true,
    explanation: 'File-only project'
  });
  const card = doc.getElementById('reaperReadinessCard');
  const rows = doc.getElementById('reaperReadinessRows');
  const actions = doc.getElementById('reaperReadinessActions');
  const chip = doc.getElementById('reaperReadinessChip');
  assert.equal(card.hidden, false);
  assert.equal(rows.children.length, 7); // all readiness rows present
  const labels = actions.querySelectorAll('button').map(b => b.textContent);
  assert.ok(labels.includes('Repair REAPER setup'));
  assert.ok(labels.includes('Install plugin'));
  assert.equal(chip.hidden, false);
  assert.match(chip.textContent, /setup needed/i);
});

test('live verification row is always present and labeled not-checked', () => {
  const doc = setup();
  panel.render({
    identified: true,
    status: 'ori_ready',
    project_mode: 'ori_ready',
    plugin_installed: true,
    plugin_enabled: true,
    plugin_attached: true,
    setup_agent_is_cli: true,
    workspace_native_cli_enabled: true,
    agent_native_cli_enabled: true,
    has_pending_setup_task: false
  });
  const rows = doc.getElementById('reaperReadinessRows');
  const liveRow = rows.children[rows.children.length - 1];
  assert.match(liveRow.textContent, /not checked yet/i);
});

test('ori_ready shows a compact success chip, no repair button', () => {
  const doc = setup();
  panel.render({
    identified: true,
    status: 'ori_ready',
    project_mode: 'ori_ready',
    plugin_installed: true,
    plugin_enabled: true,
    plugin_attached: true,
    setup_agent_is_cli: true,
    workspace_native_cli_enabled: true,
    agent_native_cli_enabled: true,
    has_pending_setup_task: false
  });
  const chip = doc.getElementById('reaperReadinessChip');
  assert.match(chip.textContent, /ready/i);
  assert.ok(chip.classList.contains('reaper-setup-chip-ready'));
  const labels = doc
    .getElementById('reaperReadinessActions')
    .querySelectorAll('button')
    .map(b => b.textContent);
  assert.ok(!labels.includes('Repair REAPER setup'));
});

test('ori_ready with a pending setup task offers Check again and start setup', () => {
  const doc = setup();
  panel.render({
    identified: true,
    status: 'ori_ready',
    project_mode: 'ori_ready',
    plugin_installed: true,
    plugin_enabled: true,
    plugin_attached: true,
    setup_agent_is_cli: true,
    workspace_native_cli_enabled: true,
    agent_native_cli_enabled: true,
    has_pending_setup_task: true
  });
  const labels = doc
    .getElementById('reaperReadinessActions')
    .querySelectorAll('button')
    .map(b => b.textContent);
  assert.ok(labels.includes('Check again and start setup'));
});
