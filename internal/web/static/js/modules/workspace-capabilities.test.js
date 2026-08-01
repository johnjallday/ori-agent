// Tests for workspace-capabilities.js — the built-in Workspace Capabilities
// catalog.
//
// Run with: node --test internal/web/static/js/modules/workspace-capabilities.test.js
//
// These pin the contract the Map station and the compact card both depend on:
// installed state comes from the server's catalog response and nothing else,
// installing goes to the real endpoint, and a repeated install produces exactly
// one record and one station rather than a second of either.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-capabilities.js', import.meta.url), 'utf8');

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = {};
    this.style = {};
    this.className = '';
    this.dataset = {};
    this.hidden = false;
    this.disabled = false;
    this.listeners = {};
    this._text = '';
  }
  set textContent(value) {
    this._text = String(value ?? '');
    if (this._text === '') this.children = [];
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent).join(' ');
  }
  set innerHTML(value) {
    if (String(value) === '') this.children = [];
  }
  get innerHTML() {
    return '';
  }
  appendChild(child) {
    this.children.push(child);
    child.parentNode = this;
    return child;
  }
  insertBefore(child) {
    this.children.unshift(child);
    child.parentNode = this;
    return child;
  }
  remove() {
    if (!this.parentNode) return;
    this.parentNode.children = this.parentNode.children.filter(node => node !== this);
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  getAttribute(name) {
    return Object.prototype.hasOwnProperty.call(this.attributes, name)
      ? this.attributes[name]
      : null;
  }
  addEventListener(type, handler) {
    (this.listeners[type] = this.listeners[type] || []).push(handler);
  }
  // Minimal selector support: '.class' and '[attr]'.
  querySelector(selector) {
    return descendants(this).find(node => matches(node, selector)) || null;
  }
  querySelectorAll(selector) {
    return descendants(this).filter(node => matches(node, selector));
  }
  closest(selector) {
    let node = this;
    while (node) {
      if (matches(node, selector)) return node;
      node = node.parentNode;
    }
    return null;
  }
  get firstChild() {
    return this.children[0] || null;
  }
}

function descendants(node) {
  const out = [];
  for (const child of node.children || []) {
    out.push(child, ...descendants(child));
  }
  return out;
}

function matches(node, selector) {
  const value = String(selector || '');
  if (value.startsWith('.'))
    return String(node.className || '')
      .split(/\s+/)
      .includes(value.slice(1));
  if (value.startsWith('[') && value.endsWith(']')) {
    return Object.prototype.hasOwnProperty.call(node.attributes, value.slice(1, -1));
  }
  if (value.startsWith('#')) return node.id === value.slice(1);
  return false;
}

function fileJanitorItem(overrides = {}) {
  return {
    definition: {
      id: 'file-janitor',
      version: 1,
      display: {
        name: 'File Janitor',
        tagline: 'Review and file one intake folder safely.',
        summary: 'Watches one inbox-style folder.',
        highlights: [
          'Manages exactly one folder you choose.',
          'Reads names, types, sizes, and dates — not file contents.',
          'Never moves or trashes a file without your approval.',
          'Files only into <folder>/Filed/<category>, and never deletes permanently.'
        ]
      }
    },
    installed: false,
    available: true,
    ...overrides
  };
}

function load({ catalog = [fileJanitorItem()], installResponse, installStatus = 200 } = {}) {
  const host = new FakeElement('div');
  host.id = 'workspace-capabilities-list';

  const requests = [];
  let catalogState = catalog;

  const document = {
    readyState: 'complete',
    addEventListener() {},
    dispatchEvent() {
      return true;
    },
    getElementById: id => (id === 'workspace-capabilities-list' ? host : null),
    createElement: tag => new FakeElement(tag)
  };
  const window = {
    location: { pathname: '/workspaces/ws-1' },
    currentWorkspaceId: 'ws-1',
    CustomEvent: class {
      constructor(type, init) {
        this.type = type;
        this.detail = init && init.detail;
      }
    }
  };
  const context = {
    console,
    window,
    document,
    setTimeout,
    fetch: async (url, options = {}) => {
      requests.push({ url: String(url), method: options.method || 'GET' });
      if (String(url).endsWith('/install')) {
        // A real install flips the catalog to installed, which the module
        // re-reads rather than assuming.
        if (installStatus === 200) {
          catalogState = [
            fileJanitorItem({
              installed: true,
              status: {
                state: 'setup_needed',
                detail: 'Choose a folder to manage.',
                configured: false
              }
            })
          ];
        }
        return {
          ok: installStatus === 200,
          status: installStatus,
          json: async () => installResponse || { success: true, already_installed: false }
        };
      }
      return { ok: true, status: 200, json: async () => ({ capabilities: catalogState }) };
    }
  };

  vm.runInNewContext(source, context, { filename: 'workspace-capabilities.js' });
  return { api: window.WorkspaceCapabilities, host, requests, window };
}

test('the catalog renders each capability with the facts needed before installing', async () => {
  const { api, host } = load();
  await api.init();

  const card = host.querySelector('[data-capability-id]');
  assert.ok(card, 'expected a capability card');
  const text = card.textContent;

  assert.match(text, /File Janitor/);
  assert.match(text, /Review and file one intake folder safely\./);
  // FR-18: the one-folder limit, metadata-first behavior, the approval
  // requirement, and the fixed Filed/ destination must all be visible.
  assert.match(text, /exactly one folder/i);
  assert.match(text, /not file contents/i);
  assert.match(text, /without your approval/i);
  assert.match(text, /Filed\//);
});

test('an uninstalled capability offers Install and no open action', async () => {
  const { api, host } = load();
  await api.init();

  assert.ok(host.querySelector('[data-capability-install]'), 'expected an install button');
  assert.equal(host.querySelector('[data-capability-open]'), null);
  assert.equal(api.isInstalled('file-janitor'), false);
});

test('installing calls the real endpoint and re-reads installed state', async () => {
  const { api, host, requests } = load();
  await api.init();

  const result = await api.install('file-janitor');
  assert.equal(result.ok, true);

  const install = requests.find(req => req.url.endsWith('/install'));
  assert.ok(install, 'expected an install request');
  assert.equal(install.method, 'POST');
  assert.equal(install.url, '/api/workspaces/ws-1/capabilities/file-janitor/install');

  // State comes from a fresh catalog read, not from assuming success.
  assert.equal(api.isInstalled('file-janitor'), true);
  assert.equal(api.statusFor('file-janitor').state, 'setup_needed');

  api.renderInto(host);
  assert.ok(host.querySelector('[data-capability-open]'), 'expected an open/set-up action');
  assert.equal(host.querySelector('[data-capability-install]'), null);
});

test('an installed but unconfigured capability offers Set up rather than Open', async () => {
  const { api, host } = load({
    catalog: [
      fileJanitorItem({
        installed: true,
        status: { state: 'setup_needed', detail: 'Choose a folder.', configured: false }
      })
    ]
  });
  await api.init();

  const open = host.querySelector('[data-capability-open]');
  assert.ok(open);
  assert.match(open.textContent, /Set up File Janitor/);
});

test('a configured capability offers Open', async () => {
  const { api, host } = load({
    catalog: [
      fileJanitorItem({
        installed: true,
        status: { state: 'watching', detail: 'Watching', configured: true }
      })
    ]
  });
  await api.init();

  const open = host.querySelector('[data-capability-open]');
  assert.ok(open);
  assert.match(open.textContent, /Open File Janitor/);
});

// FR-8/FR-9 at the UI layer: repeatedly activating Install must not produce a
// second install record, a second card, or a second station.
test('repeated install activations produce exactly one install and one card', async () => {
  const { api, host, requests } = load({
    installResponse: { success: true, already_installed: true }
  });
  await api.init();

  await api.install('file-janitor');
  await api.install('file-janitor');
  await api.install('file-janitor');

  const installs = requests.filter(req => req.url.endsWith('/install'));
  assert.equal(installs.length, 3, 'each activation reaches the server');

  // ...but the workspace ends with exactly one capability record and the
  // catalog renders exactly one card for it.
  assert.equal(api.items().length, 1);
  api.renderInto(host);
  assert.equal(host.querySelectorAll('[data-capability-id]').length, 1);
});

test('a failed install reports an error and leaves the capability uninstalled', async () => {
  const { api, host } = load({
    installStatus: 500,
    installResponse: { message: 'This capability could not be installed.' }
  });
  await api.init();

  const result = await api.install('file-janitor');
  assert.equal(result.ok, false);
  assert.match(result.error, /could not be installed/);
  assert.equal(api.isInstalled('file-janitor'), false);

  api.renderInto(host);
  assert.ok(
    host.querySelector('[data-capability-install]'),
    'install remains offered after a failure'
  );
});

test('an unavailable capability is listed but offers no action', async () => {
  const { api, host } = load({
    catalog: [
      {
        definition: { id: 'future-capability', display: { name: 'future-capability' } },
        installed: true,
        available: false,
        unavailable: 'not available in this version of Ori'
      }
    ]
  });
  await api.init();

  const card = host.querySelector('[data-capability-id]');
  assert.ok(card, 'an unresolvable install must stay visible');
  assert.match(card.textContent, /not available in this version/);
  assert.equal(host.querySelector('[data-capability-install]'), null);
  assert.equal(host.querySelector('[data-capability-open]'), null);
});

test('the open action routes to whichever surface registered for the capability', async () => {
  const { api } = load({
    catalog: [
      fileJanitorItem({
        installed: true,
        status: { state: 'setup_needed', detail: 'Choose a folder.', configured: false }
      })
    ]
  });
  await api.init();

  let opened = 0;
  api.registerOpenHandler('file-janitor', () => {
    opened += 1;
  });

  assert.equal(api.onOpen('file-janitor'), true);
  assert.equal(opened, 1);
  // Case-insensitive, and unknown capabilities report that nothing handled them
  // rather than throwing.
  assert.equal(api.onOpen('FILE-JANITOR'), true);
  assert.equal(api.onOpen('nothing-registered'), false);
});

test('installing dispatches a change event so dependent surfaces refresh in place', async () => {
  const host = new FakeElement('div');
  host.id = 'workspace-capabilities-list';
  const events = [];
  let catalogState = [fileJanitorItem()];

  const document = {
    readyState: 'complete',
    addEventListener() {},
    dispatchEvent(event) {
      events.push(event.type);
      return true;
    },
    getElementById: id => (id === 'workspace-capabilities-list' ? host : null),
    createElement: tag => new FakeElement(tag)
  };
  const window = {
    location: { pathname: '/workspaces/ws-1' },
    CustomEvent: class {
      constructor(type, init) {
        this.type = type;
        this.detail = init && init.detail;
      }
    }
  };
  const context = {
    console,
    window,
    document,
    setTimeout,
    fetch: async url => {
      if (String(url).endsWith('/install')) {
        catalogState = [fileJanitorItem({ installed: true, status: { state: 'setup_needed' } })];
        return { ok: true, status: 200, json: async () => ({ success: true }) };
      }
      return { ok: true, status: 200, json: async () => ({ capabilities: catalogState }) };
    }
  };
  vm.runInNewContext(source, context, { filename: 'workspace-capabilities.js' });

  const api = window.WorkspaceCapabilities;
  await api.init();
  await api.install('file-janitor');

  assert.ok(
    events.includes('ori:capabilities-changed'),
    'dependent surfaces (Map station, compact card) refresh from this event rather than a page reload'
  );
});

test('a catalog request failure degrades to a message rather than an empty pane', async () => {
  const host = new FakeElement('div');
  host.id = 'workspace-capabilities-list';
  const document = {
    readyState: 'complete',
    addEventListener() {},
    dispatchEvent: () => true,
    getElementById: id => (id === 'workspace-capabilities-list' ? host : null),
    createElement: tag => new FakeElement(tag)
  };
  const window = { location: { pathname: '/workspaces/ws-1' } };
  const context = {
    console,
    window,
    document,
    setTimeout,
    fetch: async () => {
      throw new Error('network down');
    }
  };
  vm.runInNewContext(source, context, { filename: 'workspace-capabilities.js' });

  const api = window.WorkspaceCapabilities;
  await api.init();

  assert.match(host.textContent, /could not be loaded/i);
  assert.equal(api.items().length, 0);
});
