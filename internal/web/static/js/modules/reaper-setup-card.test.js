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

const flush = async () => {
  for (let i = 0; i < 8; i++) await new Promise(r => setTimeout(r, 0));
};

test('inline install: resolve from marketplace, show trust, install, enable, unblock', async () => {
  const doc = setup();
  let previewCall = 0;
  globalThis.fetch = async (url, opts) => {
    const method = (opts && opts.method) || 'GET';
    if (url === '/api/reaper-setup/preview') {
      previewCall += 1;
      // First check: missing. After install+enable: ready.
      const status = previewCall === 1 ? 'plugin_missing' : 'ready_to_attach';
      return {
        ok: true,
        json: async () => ({ status, would_attach: [{ name: 'reaper-session-setup' }] })
      };
    }
    if (url === '/api/plugins/marketplaces' && method === 'GET') {
      return {
        ok: true,
        json: async () => ({
          marketplaces: [{ name: 'my-mp', plugins: [{ name: 'reaper-plugin' }] }]
        })
      };
    }
    if (url === '/api/plugins/marketplaces/install' && method === 'POST') {
      const body = JSON.parse(opts.body);
      if (!body.confirm) {
        return {
          ok: true,
          json: async () => ({
            installed: false,
            trust: { Name: 'reaper-plugin', Skills: ['reaper-session-setup'], MCPCommands: [] }
          })
        };
      }
      return {
        ok: true,
        json: async () => ({ installed: true, plugin: { name: 'reaper-plugin' } })
      };
    }
    if (url === '/api/plugins/reaper-plugin/enable' && method === 'POST') {
      return { ok: true, json: async () => ({ enabled: true }) };
    }
    throw new Error('unexpected fetch ' + method + ' ' + url);
  };

  await mod.refresh(); // missing -> blocked, Install action present
  assert.equal(doc.getElementById('createFolderBtn').disabled, true);

  // Click Install plugin.
  const actions = doc.getElementById('reaperSetupActions');
  actions
    .querySelectorAll('button')
    .find(b => b.textContent === 'Install plugin')
    .click();
  await flush();

  // Trust disclosure is shown with an Install & enable confirm.
  const confirmBtn = doc
    .getElementById('reaperSetupActions')
    .querySelectorAll('button')
    .find(b => b.textContent === 'Install & enable');
  assert.ok(confirmBtn, 'expected an Install & enable confirm button');
  assert.match(doc.getElementById('reaperSetupDetail').textContent, /Skills: reaper-session-setup/);

  // Confirm install -> installs, enables, re-checks -> ready + unblocked.
  confirmBtn.click();
  await flush();
  assert.match(doc.getElementById('reaperSetupStatusText').textContent, /will attach/i);
  assert.equal(doc.getElementById('createFolderBtn').disabled, false);
});

test('inline install: no marketplace match falls back to a source input', async () => {
  const doc = setup();
  globalThis.fetch = async (url, opts) => {
    const method = (opts && opts.method) || 'GET';
    if (url === '/api/reaper-setup/preview') {
      return { ok: true, json: async () => ({ status: 'plugin_missing', would_attach: [] }) };
    }
    if (url === '/api/plugins/marketplaces' && method === 'GET') {
      return { ok: true, json: async () => ({ marketplaces: [{ name: 'empty', plugins: [] }] }) };
    }
    throw new Error('unexpected fetch ' + url);
  };
  await mod.refresh();
  doc
    .getElementById('reaperSetupActions')
    .querySelectorAll('button')
    .find(b => b.textContent === 'Install plugin')
    .click();
  await flush();
  const actions = doc.getElementById('reaperSetupActions');
  assert.ok(
    actions.children.some(c => c.tagName === 'INPUT'),
    'expected a source input'
  );
  assert.ok(
    actions
      .querySelectorAll('button')
      .map(b => b.textContent)
      .includes('Preview install')
  );
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

// Placed last: it sets the module's declaredSource via showForTemplate, which
// would otherwise steer earlier marketplace/paste tests down the source path.
test('template-declared source: one-click install skips the marketplace', async () => {
  const doc = setup();
  const SRC = 'https://github.com/johnjallday/reaper-plugin.git';
  let previewCall = 0;
  const seen = [];
  globalThis.fetch = async (url, opts) => {
    const method = (opts && opts.method) || 'GET';
    seen.push(method + ' ' + url);
    if (url === '/api/reaper-setup/preview') {
      previewCall += 1;
      const status = previewCall === 1 ? 'plugin_missing' : 'ready_to_attach';
      return {
        ok: true,
        json: async () => ({ status, would_attach: [{ name: 'reaper-session-setup' }] })
      };
    }
    if (url === '/api/plugins/install' && method === 'POST') {
      const body = JSON.parse(opts.body);
      assert.equal(body.source, SRC);
      if (!body.confirm) {
        return {
          ok: true,
          json: async () => ({
            installed: false,
            trust: { Name: 'reaper-plugin', Skills: ['reaper-session-setup'], MCPCommands: [] }
          })
        };
      }
      return { ok: true, json: async () => ({ installed: true }) };
    }
    if (url === '/api/plugins/reaper-plugin/enable' && method === 'POST') {
      return { ok: true, json: async () => ({ enabled: true }) };
    }
    throw new Error('unexpected fetch ' + method + ' ' + url);
  };

  mod.showForTemplate({ id: 'reaper-song', tools: { plugin_sources: { 'reaper-plugin': SRC } } });
  await flush();

  // Install uses the declared source directly — no marketplace lookup.
  doc
    .getElementById('reaperSetupActions')
    .querySelectorAll('button')
    .find(b => b.textContent === 'Install plugin')
    .click();
  await flush();
  assert.ok(
    !seen.includes('GET /api/plugins/marketplaces'),
    'must not consult the marketplace when a source is declared'
  );

  const confirmBtn = doc
    .getElementById('reaperSetupActions')
    .querySelectorAll('button')
    .find(b => b.textContent === 'Install & enable');
  assert.ok(confirmBtn, 'expected the trust-preview confirm button');
  confirmBtn.click();
  await flush();
  assert.equal(doc.getElementById('createFolderBtn').disabled, false);

  mod.showForTemplate({ id: 'blank' }); // reset declaredSource for isolation
});
