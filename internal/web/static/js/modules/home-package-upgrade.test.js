import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

import {
  HomePackageUpgradeDialog,
  openHomePackageUpgrade,
  pluginUpgradeTransport,
  providerUpgradeSummary,
  renderUpgradeReview,
  upgradeRequest
} from './home-package-upgrade.js';

const PLUGINS_PAGE_SOURCE = readFileSync(new URL('../plugins.js', import.meta.url), 'utf8');

// A small DOM: enough structure, text, focus, events and <dialog> state to
// drive the dialog the way a browser would, without a browser.
class FakeNode {
  constructor(doc, tag) {
    this.ownerDocument = doc;
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.parent = null;
    this.attributes = new Map();
    this.dataset = {};
    this.listeners = new Map();
    this.ownText = '';
    this.className = '';
    this.disabled = false;
    this.open = false;
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }

  append(...nodes) {
    for (const node of nodes) {
      node.remove();
      node.parent = this;
      this.children.push(node);
    }
  }

  replaceChildren(...nodes) {
    for (const child of this.children) child.parent = null;
    this.children = [];
    this.append(...nodes);
  }

  remove() {
    if (!this.parent) return;
    this.parent.children = this.parent.children.filter(child => child !== this);
    this.parent = null;
  }

  get isConnected() {
    let node = this;
    while (node.parent) node = node.parent;
    return node === this.ownerDocument.body;
  }

  set textContent(value) {
    for (const child of this.children) child.parent = null;
    this.children = [];
    this.ownText = String(value);
  }

  get textContent() {
    return this.ownText + this.children.map(child => child.textContent).join('');
  }

  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(listener);
  }

  dispatch(type) {
    const event = {
      type,
      defaultPrevented: false,
      preventDefault() {
        this.defaultPrevented = true;
      }
    };
    for (const listener of this.listeners.get(type) || []) listener(event);
    return event;
  }

  click() {
    if (!this.disabled) this.dispatch('click');
  }

  focus() {
    this.ownerDocument.activeElement = this;
  }

  showModal() {
    this.open = true;
  }

  close() {
    this.open = false;
  }

  all(predicate) {
    return this.children.flatMap(child => [
      ...(predicate(child) ? [child] : []),
      ...child.all(predicate)
    ]);
  }
}

function fakeDocument() {
  const doc = { activeElement: null };
  doc.createElement = tag => new FakeNode(doc, tag);
  doc.body = new FakeNode(doc, 'body');
  return doc;
}

function button(root, label) {
  return root.all(node => node.tagName === 'BUTTON' && node.textContent === label)[0] || null;
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

const settle = () => new Promise(resolve => setImmediate(resolve));

const REVIEW = {
  token: 'review-token-1',
  plugin_id: 'studio-home',
  from_version: '0.1.0',
  to_version: '0.2.0',
  roles: [{ role_id: 'guide', label: 'Guide', old: 'Coordinate.', new: 'Coordinate the library.' }],
  homes: [
    {
      name: 'Portfolio Home',
      projects: ['Song One'],
      agents: [
        { agent_name: 'Guide', role_label: 'Guide', profile: 'replace', home_copy: 'replace' }
      ]
    }
  ],
  skills: ['studio-home']
};

function harness({ review = REVIEW, commit } = {}) {
  const doc = fakeDocument();
  const trigger = doc.createElement('button');
  doc.body.append(trigger);
  trigger.focus();
  const calls = [];
  const transport = {
    review: async () => {
      calls.push('review');
      return typeof review === 'function' ? review() : review;
    },
    commit: async token => {
      calls.push(`commit ${token}`);
      return commit ? commit(token) : { id: 'op-1', status: 'succeeded', to_version: '0.2.0' };
    }
  };
  return { doc, trigger, calls, transport };
}

test('the review names the Homes that move and says how much changes', () => {
  assert.equal(
    providerUpgradeSummary(REVIEW),
    'Portfolio Home and its linked projects move from 0.1.0 to 0.2.0. Only Home guidance changes.'
  );
  assert.equal(
    providerUpgradeSummary({ ...REVIEW, homes: [{ name: 'A' }, { name: 'B' }] }),
    '2 Homes and their linked projects move from 0.1.0 to 0.2.0. Only Home guidance changes.'
  );
  assert.match(
    providerUpgradeSummary({ from_version: '1', to_version: '2' }),
    /^This Home and its/
  );
});

test('names, prompts and warnings are shown as text, never as markup', () => {
  const doc = fakeDocument();
  const hostile = '<img src=x onerror=alert(1)>';
  const content = renderUpgradeReview(
    {
      ...REVIEW,
      roles: [{ role_id: 'guide', label: hostile, old: hostile, new: hostile }],
      homes: [{ name: hostile, projects: [hostile], agents: [] }],
      warnings: [hostile]
    },
    doc
  );
  assert.ok(content.textContent.includes(hostile));
  assert.equal(content.all(node => node.tagName === 'IMG').length, 0);
});

test('cancelling the review commits nothing and returns focus to Update', async () => {
  const { doc, trigger, calls, transport } = harness();
  const dialog = new HomePackageUpgradeDialog({ transport, trigger, documentRef: doc });
  await dialog.open();
  const element = dialog.parts.dialog;
  assert.equal(element.open, true);
  assert.equal(element.dataset.state, 'review');
  assert.match(element.textContent, /Update studio-home to 0\.2\.0\?/);
  assert.match(element.textContent, /Portfolio Home: 1 linked project \(Song One\)/);
  assert.match(element.textContent, /Guide \(Guide\): gets the new guidance/);
  assert.equal(doc.activeElement, button(element, 'Confirm update'));

  button(element, 'Cancel').click();
  assert.equal(dialog.state, 'closed');
  assert.equal(element.isConnected, false);
  assert.equal(doc.activeElement, trigger);
  assert.deepEqual(calls, ['review']);
});

test('Escape closes a review but not a commit in flight, which commits once', async () => {
  const pending = deferred();
  const { doc, trigger, calls, transport } = harness({ commit: () => pending.promise });
  let succeeded = null;
  const dialog = new HomePackageUpgradeDialog({
    transport,
    trigger,
    documentRef: doc,
    onSucceeded: operation => {
      succeeded = operation;
    }
  });
  await dialog.open();
  const element = dialog.parts.dialog;
  const confirm = button(element, 'Confirm update');
  confirm.click();
  confirm.disabled = false; // even a re-enabled button cannot commit twice
  confirm.click();
  void dialog.commit();
  assert.equal(dialog.state, 'committing');
  assert.equal(element.getAttribute('aria-busy'), 'true');

  const escape = element.dispatch('cancel');
  assert.equal(escape.defaultPrevented, true);
  assert.equal(dialog.state, 'committing');
  assert.equal(element.isConnected, true);
  assert.match(element.textContent, /would not stop the update/);

  pending.resolve({ id: 'op-1', status: 'succeeded', to_version: '0.2.0' });
  await settle();
  assert.deepEqual(calls, ['review', 'commit review-token-1']);
  assert.equal(dialog.state, 'succeeded');
  assert.equal(succeeded.id, 'op-1');
  assert.match(element.textContent, /Updated to 0\.2\.0\./);
  assert.equal(button(element, 'Confirm update'), null);

  element.dispatch('cancel');
  assert.equal(dialog.state, 'closed');
});

test('only a succeeded operation is success', async () => {
  const { doc, trigger, transport } = harness({
    commit: () => ({ id: 'op-9', status: 'reconcile_required', to_version: '0.2.0' })
  });
  let succeeded = false;
  const dialog = new HomePackageUpgradeDialog({
    transport,
    trigger,
    documentRef: doc,
    onSucceeded: () => {
      succeeded = true;
    }
  });
  await dialog.open();
  await dialog.commit();
  assert.equal(succeeded, false);
  assert.equal(dialog.state, 'commit-failed');
  assert.match(dialog.parts.error.textContent, /did not finish \(reconcile_required\).*op-9/);
  assert.equal(button(dialog.parts.dialog, 'Cancel').disabled, false);
});

test('a refused commit reports the server message and operation', async () => {
  const { doc, trigger, transport } = harness({
    commit: () => {
      const error = new Error('The review expired or something changed since.');
      error.code = 'home_upgrade_review_stale';
      error.details = { id: 'op-3' };
      throw error;
    }
  });
  const dialog = new HomePackageUpgradeDialog({ transport, trigger, documentRef: doc });
  await dialog.open();
  await dialog.commit();
  assert.equal(dialog.state, 'commit-failed');
  assert.equal(
    dialog.parts.error.textContent,
    'The review expired or something changed since. Operation op-3.'
  );
});

test('a review that arrives after the dialog closed changes nothing', async () => {
  const late = deferred();
  const { doc, trigger, calls, transport } = harness({ review: () => late.promise });
  const dialog = new HomePackageUpgradeDialog({ transport, trigger, documentRef: doc });
  const opening = dialog.open();
  assert.equal(dialog.state, 'loading');
  assert.equal(dialog.close(), true);
  late.resolve(REVIEW);
  await opening;
  assert.equal(dialog.state, 'closed');
  assert.equal(dialog.review, null);
  assert.equal(doc.body.all(node => node.tagName === 'DIALOG').length, 0);
  assert.equal(await dialog.commit(), null);
  assert.deepEqual(calls, ['review']);
});

test('a review that cannot be made says why and offers only Cancel', async () => {
  const { doc, trigger, transport } = harness({
    review: () => {
      throw new Error('No newer release of this plugin is available.');
    }
  });
  const dialog = new HomePackageUpgradeDialog({ transport, trigger, documentRef: doc });
  await dialog.open();
  assert.equal(dialog.state, 'failed');
  assert.equal(dialog.parts.error.getAttribute('role'), 'alert');
  assert.equal(dialog.parts.error.textContent, 'No newer release of this plugin is available.');
  assert.equal(button(dialog.parts.dialog, 'Confirm update'), null);
  assert.equal(doc.activeElement, button(dialog.parts.dialog, 'Cancel'));
});

test('the plugin transport names the plugin in the path and the token only in the body', async () => {
  const requests = [];
  const transport = pluginUpgradeTransport('studio home/x', {
    fetchImpl: async (url, options) => {
      requests.push({ url, options });
      return { ok: true, json: async () => ({ status: 'succeeded' }) };
    }
  });
  await transport.status();
  await transport.review();
  await transport.commit('secret-token');
  assert.deepEqual(
    requests.map(request => `${request.options.method} ${request.url}`),
    [
      'GET /api/plugins/studio%20home%2Fx/home-upgrade',
      'POST /api/plugins/studio%20home%2Fx/home-upgrade/review',
      'POST /api/plugins/studio%20home%2Fx/home-upgrade/commit'
    ]
  );
  assert.equal(requests[2].options.body, '{"token":"secret-token"}');
  assert.ok(requests.every(request => !request.url.includes('secret-token')));
});

test('a refusal carries the server code and operation details', async () => {
  await assert.rejects(
    () =>
      upgradeRequest(
        async () => ({
          ok: false,
          status: 409,
          json: async () => ({
            code: 'home_upgrade_active',
            message: 'Another upgrade of this plugin is in progress or needs attention.',
            details: { id: 'op-2', status: 'claimed' }
          })
        }),
        '/api/plugins/x/home-upgrade/review',
        { method: 'POST', body: '{}' }
      ),
    error =>
      error.status === 409 &&
      error.code === 'home_upgrade_active' &&
      error.details.id === 'op-2' &&
      /in progress/.test(error.message)
  );
});

const HOME_UPGRADE_REQUIRED = {
  ok: false,
  status: 409,
  error: 'A Home uses this plugin.',
  data: {
    code: 'home_upgrade_required',
    details: {
      home_name: 'Portfolio Home',
      home_path: '/workspaces/portfolio-home/assistant?upgrade=review'
    }
  }
};

// pluginsPage runs the real Plugins page script against the real shared
// dialog, recording every request, toast and navigation it makes.
function pluginsPage({ updateResponses }) {
  const doc = fakeDocument();
  doc.addEventListener = () => {};
  doc.getElementById = () => null;
  doc.querySelectorAll = selector =>
    selector === '[data-plugin-action="update"]'
      ? doc.body.all(node => node.getAttribute('data-plugin-action') === 'update')
      : [];
  const updateButton = name => {
    const node = doc.createElement('button');
    node.setAttribute('data-plugin-action', 'update');
    node.setAttribute('data-plugin-name', name);
    doc.body.append(node);
    return node;
  };
  const requests = [];
  const toasts = [];
  const navigations = [];
  const opened = [];
  const location = {
    href: 'http://ori.test/plugins',
    assign: url => navigations.push(url),
    replace: url => navigations.push(url)
  };
  const upgradeFetch = async (url, options) => {
    requests.push(`${options.method} ${url}`);
    const name = decodeURIComponent(url.split('/')[3]);
    return { ok: true, json: async () => ({ ...REVIEW, plugin_id: name }) };
  };
  const toast = type => (message, options) => toasts.push({ type, message, options });
  const responses = [...updateResponses];
  const sandbox = {
    alert() {},
    document: doc,
    location,
    window: {
      location,
      Toast: {
        info: toast('info'),
        warning: toast('warning'),
        error: toast('error'),
        success: toast('success')
      },
      PluginLifecycle: {
        LIFECYCLE_LABELS: { ENABLED: 'enabled', DISABLED: 'disabled' },
        capitalize: value => value,
        async request(method, url, body) {
          requests.push(`${method} ${url} ${JSON.stringify(body)}`);
          const next = responses.shift();
          return typeof next === 'function' ? next() : next;
        },
        renderTrustReport: report => ({ report })
      },
      PluginUpdateNotifications: {
        escapeHTML: value => String(value),
        createController: () => ({ refresh() {}, start() {}, stop() {} }),
        indexUpdates: () => new Map(),
        pluginNotice: () => null
      },
      bootstrap: {
        Modal: {
          getOrCreateInstance() {
            throw new Error('the ordinary update modal must not open for a Home upgrade');
          }
        }
      },
      HomePackageUpgrade: {
        openHomePackageUpgrade: options => {
          const dialog = openHomePackageUpgrade(options);
          opened.push({ options, dialog });
          return dialog;
        },
        pluginUpgradeTransport: name => pluginUpgradeTransport(name, { fetchImpl: upgradeFetch })
      }
    }
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(PLUGINS_PAGE_SOURCE, sandbox, { filename: 'plugins.js' });
  return {
    page: sandbox.window,
    doc,
    location,
    updateButton,
    requests,
    toasts,
    navigations,
    opened
  };
}

test('Update of a plugin a Home uses opens the shared review in place; Cancel commits nothing', async () => {
  const plugins = pluginsPage({ updateResponses: [HOME_UPGRADE_REQUIRED] });
  const trigger = plugins.updateButton('studio-home');
  trigger.focus();
  await plugins.page.pluginUpdate('studio-home', { trigger });
  await settle();

  assert.equal(plugins.opened.length, 1);
  const { dialog } = plugins.opened[0];
  assert.equal(dialog.state, 'review');
  assert.equal(dialog.parts.dialog.open, true);
  assert.match(dialog.parts.dialog.textContent, /Update studio-home to 0\.2\.0\?/);
  assert.deepEqual(plugins.requests, [
    'POST /api/plugins/studio-home/update {"confirm":false}',
    'POST /api/plugins/studio-home/home-upgrade/review'
  ]);
  // No navigation, no hand-off toast, and the page URL is what it was.
  assert.deepEqual(plugins.navigations, []);
  assert.deepEqual(plugins.toasts, []);
  assert.equal(plugins.location.href, 'http://ori.test/plugins');

  button(dialog.parts.dialog, 'Cancel').click();
  assert.equal(dialog.state, 'closed');
  assert.equal(plugins.doc.activeElement, trigger);
  assert.ok(plugins.requests.every(request => !/\/commit|"confirm":true/.test(request)));
  assert.deepEqual(plugins.navigations, []);
});

test('a switch to the plugin list version stays refused while a Home uses the plugin', async () => {
  const plugins = pluginsPage({ updateResponses: [HOME_UPGRADE_REQUIRED] });
  await plugins.page.pluginUpdate('studio-home', { fromList: true });
  await settle();
  assert.equal(plugins.opened.length, 0);
  assert.deepEqual(plugins.requests, [
    'POST /api/plugins/studio-home/update {"confirm":false,"from_list":true}'
  ]);
  assert.equal(plugins.toasts.length, 1);
  assert.equal(plugins.toasts[0].type, 'warning');
  assert.match(
    plugins.toasts[0].message,
    /plugin list's version while a Home uses it\. Nothing was changed\./
  );
  assert.equal(plugins.toasts[0].options?.action, undefined);
  assert.deepEqual(plugins.navigations, []);
});

test('only the newest Update click opens a review, for that plugin', async () => {
  const first = deferred();
  const plugins = pluginsPage({ updateResponses: [() => first.promise, HOME_UPGRADE_REQUIRED] });
  const older = plugins.page.pluginUpdate('first-home', {
    trigger: plugins.updateButton('first-home')
  });
  await plugins.page.pluginUpdate('second-home', { trigger: plugins.updateButton('second-home') });
  first.resolve(HOME_UPGRADE_REQUIRED);
  await older;
  await settle();
  assert.equal(plugins.opened.length, 1);
  assert.match(plugins.opened[0].dialog.parts.dialog.textContent, /Update second-home to/);
  assert.ok(plugins.requests.every(request => !request.includes('/first-home/home-upgrade')));
});

test('focus returns to the plugin row Update button even after the list re-rendered', async () => {
  const plugins = pluginsPage({ updateResponses: [HOME_UPGRADE_REQUIRED] });
  const trigger = plugins.updateButton('studio-home');
  await plugins.page.pluginUpdate('studio-home', { trigger });
  await settle();
  trigger.remove();
  const rerendered = plugins.updateButton('studio-home');
  button(plugins.opened[0].dialog.parts.dialog, 'Cancel').click();
  assert.equal(plugins.doc.activeElement, rerendered);
});
