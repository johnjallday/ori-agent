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

// ---- Setup Wizard step ----
//
// The card and the wizard step run the same repairs, so what these cover is the
// step's own contract: it draws only its own blueprint's steps, only for the
// mode that has prerequisites, and it never records readiness itself.

function wizardSetup(readiness, options = {}) {
  const doc = setup();
  const posts = [];
  globalThis.fetch = async (url, init) => {
    if (init && init.method === 'POST') {
      posts.push({ url, body: init.body ? JSON.parse(init.body) : null });
      return { ok: true, json: async () => options.repairResult || {} };
    }
    return { ok: !options.readinessFails, json: async () => readiness };
  };
  const captured = new Map();
  globalThis.window.SetupWizard = {
    registerStepRenderer: (kind, renderer) => captured.set(kind, renderer)
  };
  panel.init('ws-1');
  const calls = { renderDefault: 0, recheck: 0, errors: [] };
  const ctx = (step, status) => ({
    step,
    status,
    setBusy: () => {},
    setError: message => calls.errors.push(message),
    recheck: async () => {
      calls.recheck += 1;
    },
    rememberReturn: () => {},
    renderDefault: () => {
      calls.renderDefault += 1;
    }
  });
  return { doc, posts, calls, ctx, renderer: captured.get('readiness') };
}

const assisted = {
  steps: [{ id: 'mode', selected_option: 'ori_assisted' }, { id: 'readiness' }]
};
const fileOnly = { steps: [{ id: 'mode', selected_option: 'file_only' }] };
const reaperStep = { id: 'readiness', kind: 'readiness', adapter: 'reaper_song', summary: 'x' };

test('the step renderer registers itself on the shared readiness kind', () => {
  const { renderer } = wizardSetup({});
  assert.equal(typeof renderer?.render, 'function');
});

test("another blueprint's readiness step is handed back, not blanked", async () => {
  // The kind is shared, so a renderer that drew REAPER's rows for everyone
  // would put this blueprint's prerequisites on another blueprint's step.
  const { renderer, calls, ctx, doc } = wizardSetup({ identified: true, status: 'plugin_missing' });
  const container = doc.createElement('div');
  await renderer.render(container, ctx({ ...reaperStep, adapter: 'downloads_janitor' }, assisted));
  assert.equal(calls.renderDefault, 1);
  assert.equal(container.children.length, 0);
});

test('file-only has nothing to repair, so the step stays the default summary', async () => {
  const { renderer, calls, ctx, doc } = wizardSetup({ status: 'plugin_missing' });
  const container = doc.createElement('div');
  await renderer.render(container, ctx(reaperStep, fileOnly));
  assert.equal(calls.renderDefault, 1);
  assert.equal(container.children.length, 0);
});

test('ori-assisted lists the prerequisites and offers the install that is missing', async () => {
  const { renderer, ctx, doc } = wizardSetup({
    identified: true,
    status: 'plugin_missing',
    plugin_installed: false
  });
  const container = doc.createElement('div');
  await renderer.render(container, ctx(reaperStep, assisted));
  const text = container.textContent;
  assert.match(text, /Plugin: Not installed/);
  assert.match(text, /Native CLI access/);
  // Said where a user would otherwise assume the opposite.
  assert.match(text, /Live REAPER session: Not checked here/);
  const labels = container.children
    .filter(child => child.className === 'setup-wizard-step-actions')[0]
    .children.map(b => b.textContent);
  assert.deepEqual(labels, ['Install the plugin']);
});

test('enabling a disabled plugin asks the server first and only then confirms', async () => {
  const { renderer, ctx, doc, posts, calls } = wizardSetup(
    {
      identified: true,
      status: 'plugin_disabled',
      plugin_installed: true,
      plugin_enabled: false
    },
    { repairResult: { needs_confirm: true } }
  );
  const container = doc.createElement('div');
  await renderer.render(container, ctx(reaperStep, assisted));
  const actions = container.children.filter(
    child => child.className === 'setup-wizard-step-actions'
  )[0];
  const enable = actions.children[0];
  assert.equal(enable.textContent, 'Enable and attach');
  await enable._listeners.click[0]();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(posts.length, 2, 'the unconfirmed call is what asks; the second one acts');
  assert.equal(posts[0].body.confirm_enable, false);
  assert.equal(posts[1].body.confirm_enable, true);
  // The browser does not decide the step passed — it asks the server again.
  assert.equal(calls.recheck, 1);
});

test('an unreadable readiness offers a retry instead of a wrong verdict', async () => {
  const { renderer, ctx, doc } = wizardSetup({}, { readinessFails: true });
  const container = doc.createElement('div');
  await renderer.render(container, ctx(reaperStep, assisted));
  assert.match(container.textContent, /Could not be read just now/);
  const actions = container.children.filter(
    child => child.className === 'setup-wizard-step-actions'
  )[0];
  assert.deepEqual(
    actions.children.map(b => b.textContent),
    ['Check again']
  );
});

test('where the wizard owns setup, the card reports and points at it', () => {
  const doc = setup();
  globalThis.window.SetupWizard = { getStatus: () => ({ applicable: true, state: 'in_progress' }) };
  panel.render({
    identified: true,
    status: 'plugin_missing',
    project_mode: 'file_only',
    plugin_installed: false,
    setup_agent_is_cli: false,
    has_pending_setup_task: true
  });
  const labels = doc
    .getElementById('reaperReadinessActions')
    .querySelectorAll('button')
    .map(b => b.textContent);
  // One entry point, not a second place to install and enable the same things.
  assert.deepEqual(labels, ['Open setup']);
  // The facts are still reported here.
  assert.ok(doc.getElementById('reaperReadinessRows').children.length >= 5);
  delete globalThis.window.SetupWizard;
});

test('a workspace with no wizard keeps the card’s own repairs', () => {
  const doc = setup();
  delete globalThis.window.SetupWizard;
  panel.render({
    identified: true,
    status: 'plugin_missing',
    project_mode: 'file_only',
    plugin_installed: false,
    has_pending_setup_task: true
  });
  const labels = doc
    .getElementById('reaperReadinessActions')
    .querySelectorAll('button')
    .map(b => b.textContent);
  assert.ok(labels.includes('Repair REAPER setup'));
  assert.ok(labels.includes('Install plugin'));
});
