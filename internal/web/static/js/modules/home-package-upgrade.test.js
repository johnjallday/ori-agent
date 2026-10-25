import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

import {
  HomePackageUpgradeDialog,
  agentEffect,
  homeUpgradeRowNotice,
  providerUpgradeAgentLine,
  providerUpgradeImpact,
  openHomePackageUpgrade,
  pluginUpgradeTransport,
  providerUpgradeSummary,
  renderUpgradeReview,
  safeHomeLink,
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

  contains(node) {
    for (let current = node; current; current = current.parent) {
      if (current === this) return true;
    }
    return false;
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

function harness({ review = REVIEW, commit, status, outcome } = {}) {
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
  if (status) {
    transport.status = async () => {
      calls.push('status');
      return status();
    };
  }
  if (outcome) {
    transport.outcome = async token => {
      calls.push(`outcome ${token}`);
      return outcome(token);
    };
  }
  return { doc, trigger, calls, transport };
}

function lost() {
  const error = new Error('Ori could not be reached, so its answer was lost.');
  error.transport = true;
  return error;
}

// Answers in order, repeating the last one.
function answers(...items) {
  let index = 0;
  return () => {
    const item = items[Math.min(index++, items.length - 1)];
    if (item instanceof Error) throw item;
    return typeof item === 'function' ? item() : item;
  };
}

const QUICK = [0, 1, 1, 1];

test('the review names the Homes that move and says how much changes', () => {
  assert.equal(
    providerUpgradeSummary(REVIEW),
    'Portfolio Home and its linked projects move from 0.1.0 to 0.2.0. Only Home guidance changes.'
  );
  assert.equal(
    providerUpgradeSummary({ ...REVIEW, homes: [{ name: 'A' }, { name: 'B' }] }),
    '2 Homes and their linked projects move together from 0.1.0 to 0.2.0. Only Home guidance changes.'
  );
  assert.match(
    providerUpgradeSummary({ from_version: '1', to_version: '2' }),
    /^This Home and its/
  );
});

test('an agent whose two copies are treated differently is said to be, both before and after', () => {
  assert.equal(agentEffect({ profile: 'replace', home_copy: 'replace' }), 'replace');
  assert.equal(agentEffect({ profile: 'keep', home_copy: 'missing' }), 'keep');
  assert.equal(agentEffect({ profile: 'missing', home_copy: 'replace' }), 'replace');
  assert.equal(agentEffect({ profile: 'missing', home_copy: 'missing' }), 'missing');
  assert.equal(agentEffect({ profile: 'keep', home_copy: 'replace' }), 'mixed');
  assert.equal(agentEffect({ profile: 'replace', home_copy: 'keep' }), 'mixed');
  const keptProfile = { profile: 'keep', home_copy: 'replace' };
  assert.equal(
    providerUpgradeAgentLine(keptProfile),
    'keeps your edited profile prompt; its Home copy gets the new guidance'
  );
  assert.equal(
    providerUpgradeAgentLine(keptProfile, { done: true }),
    'kept your edited profile prompt; its Home copy got the new guidance'
  );
  assert.equal(
    providerUpgradeAgentLine({ profile: 'replace', home_copy: 'keep' }),
    'gets the new guidance in its profile; its Home copy keeps your edits'
  );
  assert.equal(
    providerUpgradeAgentLine({ profile: 'keep', home_copy: 'keep' }, { done: true }),
    'kept your edited prompt'
  );
});

test('the review leads with the version change and its size, with accurate plurals', () => {
  assert.equal(
    providerUpgradeImpact({ homes: [{ name: 'A', projects: [], agents: [{}] }] }),
    '1 Home, 0 linked projects and 1 staffed agent affected.'
  );
  assert.equal(
    providerUpgradeImpact({
      homes: [
        { name: 'A', projects: ['x'], agents: [{}, {}] },
        { name: 'B', projects: [], agents: [] }
      ]
    }),
    '2 Homes, 1 linked project and 2 staffed agents affected.'
  );

  const doc = fakeDocument();
  const content = renderUpgradeReview(
    {
      ...REVIEW,
      warnings: ['This release runs a new command.'],
      additions: [
        'Adds a Your studio card to this Home. Nothing is detected or read until you open it.'
      ],
      homes: [
        {
          name: 'North Studio',
          projects: ['First Song', 'Second Song'],
          agents: [
            {
              agent_name: 'North Manager',
              role_label: 'Guide',
              profile: 'replace',
              home_copy: 'replace'
            }
          ]
        },
        {
          name: 'South Label',
          projects: [],
          agents: [
            {
              agent_name: 'South Manager',
              role_label: 'Guide',
              profile: 'keep',
              home_copy: 'replace'
            }
          ]
        }
      ],
      trashed_homes: 2
    },
    doc
  );
  const overview = content.children[0];
  assert.equal(overview.className, 'home-package-upgrade__overview');
  // Read aloud as "0.1.0 to 0.2.0"; the arrow is decoration.
  assert.equal(overview.children[0].textContent, '0.1.0→ to 0.2.0');
  assert.equal(overview.children[0].children[1].getAttribute('aria-hidden'), 'true');
  assert.equal(
    overview.children[1].textContent,
    '2 Homes, 2 linked projects and 2 staffed agents affected.'
  );
  assert.deepEqual(
    content.all(node => node.tagName === 'H3').map(node => node.textContent),
    [
      'Before you confirm',
      'What this release adds',
      'Guidance changes',
      'Homes and projects',
      'Staffed agents',
      'What is cancelled',
      'What stays the same'
    ]
  );
  assert.match(content.textContent, /North Studio: 2 linked projects \(First Song, Second Song\)/);
  assert.match(content.textContent, /2 Homes in the Trash keep the old release\./);
  // With several Homes, each Home's agents are listed under its name.
  assert.deepEqual(
    content
      .all(node => node.className === 'home-package-upgrade__group')
      .map(node => node.textContent),
    ['North Studio', 'South Label']
  );
  assert.match(
    content.textContent,
    /South Manager \(Guide\): keeps your edited profile prompt; its Home copy gets the new guidance/
  );
  // What is cancelled is not presented as something that stays the same.
  const sections = content.all(node => node.tagName === 'SECTION');
  const cancelled = sections.find(node => node.children[0].textContent === 'What is cancelled');
  const stays = sections.find(node => node.children[0].textContent === 'What stays the same');
  assert.match(
    cancelled.textContent,
    /Pending library reviews and scans in these Homes are cancelled/
  );
  assert.doesNotMatch(stays.textContent, /cancelled/);
  assert.match(stays.textContent, /Approved project library folders stay approved\./);
  assert.match(
    renderUpgradeReview({ ...REVIEW, trashed_homes: 1 }, doc).textContent,
    /1 Home in the Trash keeps the old release\./
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
  // The review is read from its heading; Confirm update is one Tab away, so
  // Enter cannot confirm what was not read.
  assert.equal(doc.activeElement, dialog.parts.heading);
  assert.equal(dialog.parts.heading.getAttribute('tabindex'), '-1');
  assert.equal(element.getAttribute('aria-labelledby'), dialog.parts.heading.getAttribute('id'));

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
  // An operation that stopped partway holds the package; nothing here offers
  // to repair or retry it.
  assert.equal(dialog.state, 'held');
  assert.equal(dialog.parts.heading.textContent, 'This update needs attention');
  assert.match(dialog.parts.status.textContent, /stopped partway \(operation op-9\)/);
  assert.equal(button(dialog.parts.dialog, 'Check again'), null);
  assert.equal(button(dialog.parts.dialog, 'Review again'), null);
  assert.equal(button(dialog.parts.dialog, 'Close').disabled, false);
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

test('a review that cannot be made says why and offers only Close', async () => {
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
  assert.equal(doc.activeElement, button(dialog.parts.dialog, 'Close'));
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
function pluginsPage({ updateResponses, statusAnswers = {} }) {
  const doc = fakeDocument();
  doc.addEventListener = () => {};
  // The installed list is drawn as markup; its rows are read as that markup.
  const pluginList = doc.createElement('div');
  pluginList.querySelectorAll = () => [];
  doc.getElementById = id => (id === 'pluginList' ? pluginList : null);
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
    if (options.method === 'GET' && statusAnswers[name]) {
      const { status = 200, body } = statusAnswers[name];
      return { ok: status < 400, status, json: async () => body };
    }
    const payload = url.endsWith('/commit')
      ? { id: 'op-1', status: 'succeeded', to_version: '0.2.0', agents: [] }
      : { ...REVIEW, plugin_id: name };
    return { ok: true, json: async () => payload };
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
        createController: () => ({
          refresh() {
            requests.push('refresh update status');
          },
          start() {},
          stop() {}
        }),
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
        pluginUpgradeTransport: name => pluginUpgradeTransport(name, { fetchImpl: upgradeFetch }),
        homeUpgradeRowNotice
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
    pluginList,
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
  // The dialog reads the package's status before reviewing.
  assert.deepEqual(plugins.requests, [
    'POST /api/plugins/studio-home/update {"confirm":false}',
    'GET /api/plugins/studio-home/home-upgrade',
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

test('only a Home page path on this host is ever a link', () => {
  assert.equal(
    safeHomeLink('/workspaces/north-studio/assistant'),
    '/workspaces/north-studio/assistant'
  );
  for (const unsafe of [
    'javascript:alert(1)',
    '//evil.example/workspaces/x/assistant',
    'https://evil.example/workspaces/x/assistant',
    '/workspaces/x/assistant?upgrade=commit',
    '/workspaces/x/../settings/assistant',
    '/workspaces/a/b/assistant',
    '/workspaces/x\\y/assistant',
    '/workspaces/x/assistant#top',
    ''
  ]) {
    assert.equal(safeHomeLink(unsafe), '', unsafe);
  }
});

const MULTI_HOME_REVIEW = {
  ...REVIEW,
  roles: [
    { role_id: 'guide', label: 'Guide', old: 'a', new: 'b' },
    { role_id: 'label', label: 'Label Manager', old: 'c', new: 'd' }
  ],
  homes: [
    { name: 'North Studio', link: '/workspaces/north-studio/assistant', projects: ['First Song'] },
    { name: 'South Label', link: 'javascript:alert(1)', projects: [] },
    { name: 'East Room', link: '/workspaces/east-room/assistant', projects: [] }
  ]
};

test('a succeeded update shows what happened, links other Homes and guards leaving meanwhile', async () => {
  const pending = deferred();
  const { doc, trigger, transport } = harness({
    review: MULTI_HOME_REVIEW,
    commit: () => pending.promise
  });
  const listeners = new Map();
  const windowRef = {
    addEventListener: (type, listener) => listeners.set(type, listener),
    removeEventListener: (type, listener) => {
      if (listeners.get(type) === listener) listeners.delete(type);
    }
  };
  const dialog = new HomePackageUpgradeDialog({
    transport,
    trigger,
    documentRef: doc,
    windowRef,
    homeLinks: true,
    currentPath: '/workspaces/east-room/assistant'
  });
  await dialog.open();
  void dialog.commit();
  const leaving = { returnValue: undefined, preventDefault() {} };
  listeners.get('beforeunload')(leaving);
  assert.equal(leaving.returnValue, '');

  pending.resolve({
    id: 'op-7',
    status: 'succeeded',
    to_version: '0.2.0',
    agents: [
      { agent_name: 'North Manager', role_id: 'guide', profile: 'replace', home_copy: 'replace' },
      { agent_name: 'South Manager', role_id: 'label', profile: 'keep', home_copy: 'keep' }
    ]
  });
  await settle();
  const element = dialog.parts.dialog;
  assert.equal(dialog.state, 'succeeded');
  assert.equal(listeners.has('beforeunload'), false);
  assert.equal(dialog.parts.heading.textContent, 'studio-home is updated to 0.2.0');
  assert.match(element.textContent, /Homes now on 0\.2\.0/);
  assert.match(element.textContent, /North Manager \(Guide\): got the new guidance/);
  assert.match(element.textContent, /South Manager \(Label Manager\): kept your edited prompt/);
  assert.match(element.textContent, /Operation op-7/);
  // Each Home is named; only a safe page that is not this one is linked.
  const links = element.all(node => node.tagName === 'A');
  assert.deepEqual(
    links.map(link => [link.textContent, link.getAttribute('href')]),
    [['Open North Studio', '/workspaces/north-studio/assistant']]
  );
  assert.match(element.textContent, /South Label/);
  assert.match(element.textContent, /East Room/);
  assert.equal(doc.activeElement, button(element, 'Close'));
  button(element, 'Close').click();
  assert.equal(dialog.state, 'closed');
});

test('a page that cannot refresh after success does not call the update failed', async () => {
  const { doc, trigger, transport } = harness();
  const dialog = new HomePackageUpgradeDialog({
    transport,
    trigger,
    documentRef: doc,
    onSucceeded: () => {
      throw new Error('network down');
    }
  });
  await dialog.open();
  const operation = await dialog.commit();
  assert.equal(operation.status, 'succeeded');
  assert.equal(dialog.state, 'succeeded');
  assert.match(
    dialog.parts.error.textContent,
    /^The update succeeded, but this page could not refresh: network down/
  );
});

test('a confirmed update on the Plugins page commits the reviewed token once and refreshes in place', async () => {
  const plugins = pluginsPage({ updateResponses: [HOME_UPGRADE_REQUIRED] });
  const trigger = plugins.updateButton('studio-home');
  await plugins.page.pluginUpdate('studio-home', { trigger });
  await settle();
  const { dialog } = plugins.opened[0];
  button(dialog.parts.dialog, 'Confirm update').click();
  await settle();
  await settle();
  assert.equal(dialog.state, 'succeeded');
  const commits = plugins.requests.filter(request => request.includes('/commit'));
  assert.deepEqual(commits, ['POST /api/plugins/studio-home/home-upgrade/commit']);
  // The installed list and the cached update status read again; nothing
  // navigated.
  assert.ok(plugins.requests.some(request => request.startsWith('GET /api/plugins ')));
  assert.ok(plugins.requests.includes('refresh update status'));
  assert.deepEqual(plugins.navigations, []);
  assert.equal(plugins.location.href, 'http://ori.test/plugins');
});

test('only an answer without Ori’s own code counts as a lost answer', async () => {
  const fetchFrom = response => async () => response;
  const cases = [
    [
      async () => {
        throw new TypeError('Failed to fetch');
      },
      true
    ],
    [fetchFrom({ ok: false, status: 502, json: async () => JSON.parse('<html>') }), true],
    [fetchFrom({ ok: false, status: 504, json: async () => ({ error: 'gateway' }) }), true],
    [fetchFrom({ ok: true, status: 200, json: async () => JSON.parse('not json') }), true],
    [
      fetchFrom({
        ok: false,
        status: 503,
        json: async () => ({ code: 'home_upgrade_unavailable', message: 'Nothing was changed.' })
      }),
      false
    ],
    // The reset fence answers before any handler runs: Ori's own answer.
    [
      fetchFrom({
        ok: false,
        status: 503,
        json: async () => ({ error: 'reset_pending', message: 'Ordinary work is paused.' })
      }),
      false
    ]
  ];
  for (const [fetchImpl, transport] of cases) {
    await assert.rejects(
      () => upgradeRequest(fetchImpl, '/api/plugins/x/home-upgrade/commit', { method: 'POST' }),
      error => Boolean(error.transport) === transport
    );
  }
});

test('a lost commit answer is resolved from that review’s own operation, never by committing again', async () => {
  let succeeded = null;
  const { doc, trigger, calls, transport } = harness({
    commit: answers(lost()),
    outcome: answers(
      { review: 'pending' },
      { operation: { id: 'op-5', status: 'replaced', to_version: '0.2.0' } },
      { operation: { id: 'op-5', status: 'succeeded', to_version: '0.2.0', agents: [] } }
    )
  });
  const dialog = new HomePackageUpgradeDialog({
    transport,
    trigger,
    documentRef: doc,
    outcomeDelays: QUICK,
    onSucceeded: operation => {
      succeeded = operation;
    }
  });
  await dialog.open();
  const operation = await dialog.commit();
  assert.equal(operation.id, 'op-5');
  assert.equal(succeeded.id, 'op-5');
  assert.equal(dialog.state, 'succeeded');
  assert.deepEqual(calls, [
    'review',
    'commit review-token-1',
    'outcome review-token-1',
    'outcome review-token-1',
    'outcome review-token-1'
  ]);
});

test('an outcome that stays unknown says so, offers Check again, and never commits again', async () => {
  let outcome = answers({ review: 'pending' });
  const { doc, trigger, calls, transport } = harness({
    commit: answers(lost()),
    outcome: token => outcome(token)
  });
  const dialog = new HomePackageUpgradeDialog({
    transport,
    trigger,
    documentRef: doc,
    outcomeDelays: QUICK
  });
  await dialog.open();
  assert.equal(await dialog.commit(), null);
  assert.equal(dialog.state, 'unknown');
  assert.equal(dialog.parts.heading.textContent, 'The outcome is not known yet');
  assert.match(
    dialog.parts.status.textContent,
    /no record that this update started.*nothing will be committed again/
  );
  assert.equal(button(dialog.parts.dialog, 'Confirm update'), null);
  assert.equal(button(dialog.parts.dialog, 'Review again'), null);
  assert.equal(calls.filter(call => call.startsWith('commit')).length, 1);
  assert.equal(calls.filter(call => call.startsWith('outcome')).length, QUICK.length);

  outcome = answers({ operation: { id: 'op-6', status: 'succeeded', to_version: '0.2.0' } });
  button(dialog.parts.dialog, 'Check again').click();
  await settle();
  assert.equal(dialog.state, 'succeeded');
  assert.equal(calls.filter(call => call.startsWith('commit')).length, 1);
});

test('an outcome that cannot be read is unavailable, never success', async () => {
  const refused = new Error('The upgrade could not be completed. Nothing was changed; try again.');
  refused.code = 'home_upgrade_unavailable';
  const { doc, trigger, transport } = harness({
    commit: answers(lost()),
    outcome: answers(refused)
  });
  const dialog = new HomePackageUpgradeDialog({
    transport,
    trigger,
    documentRef: doc,
    outcomeDelays: QUICK
  });
  await dialog.open();
  await dialog.commit();
  assert.equal(dialog.state, 'unknown');
  assert.match(dialog.parts.status.textContent, /could not say what became of the update/);
});

test('a review that was never applied offers Review again with a fresh review and confirmation', async () => {
  for (const [answer, words] of [
    [
      {
        operation: {
          id: 'op-8',
          status: 'cancelled',
          reason: 'The download failed. Nothing changed.'
        }
      },
      /download failed.*op-8/
    ],
    [{ review: 'expired' }, /expired before Ori applied it/],
    [{ review: 'unknown' }, /no record of this review/]
  ]) {
    let reviews = 0;
    const { doc, trigger, calls, transport } = harness({
      review: () => ({ ...REVIEW, token: `review-token-${++reviews}` }),
      commit: answers(lost(), { id: 'op-9', status: 'succeeded', to_version: '0.2.0' }),
      outcome: answers(answer)
    });
    const dialog = new HomePackageUpgradeDialog({
      transport,
      trigger,
      documentRef: doc,
      outcomeDelays: QUICK
    });
    await dialog.open();
    await dialog.commit();
    assert.equal(dialog.state, 'settled');
    assert.equal(dialog.parts.heading.textContent, 'The update was not applied');
    assert.match(dialog.parts.status.textContent, words);

    button(dialog.parts.dialog, 'Review again').click();
    await settle();
    assert.equal(dialog.state, 'review');
    assert.equal(dialog.review.token, 'review-token-2');
    button(dialog.parts.dialog, 'Confirm update').click();
    await settle();
    assert.equal(dialog.state, 'succeeded');
    assert.deepEqual(
      calls.filter(call => call.startsWith('commit')),
      ['commit review-token-1', 'commit review-token-2']
    );
  }
});

test('a stale review offers Review again; a newer load wins over an older one', async () => {
  const slow = deferred();
  let reviews = 0;
  const stale = new Error(
    'The review expired or something changed since. Review the upgrade again.'
  );
  stale.code = 'home_upgrade_review_stale';
  const { doc, trigger, transport } = harness({
    review: () => {
      reviews += 1;
      if (reviews === 2) return slow.promise;
      return { ...REVIEW, token: `review-token-${reviews}` };
    },
    commit: answers(stale)
  });
  const dialog = new HomePackageUpgradeDialog({ transport, trigger, documentRef: doc });
  await dialog.open();
  await dialog.commit();
  assert.equal(dialog.state, 'commit-failed');
  const again = button(dialog.parts.dialog, 'Review again');
  assert.equal(doc.activeElement, again);
  again.click(); // review 2 hangs
  void dialog.load(); // review 3 answers first
  await settle();
  assert.equal(dialog.review.token, 'review-token-3');
  slow.resolve({ ...REVIEW, token: 'review-token-2' });
  await settle();
  assert.equal(dialog.review.token, 'review-token-3');
  assert.equal(dialog.parts.dialog.all(node => node.textContent === 'Confirm update').length, 1);
});

test('reopening while an operation holds the package shows it instead of a review', async () => {
  let status = answers({ operation: { id: 'op-4', status: 'claimed', to_version: '0.2.0' } });
  const { doc, trigger, calls, transport } = harness({ status: () => status() });
  const dialog = new HomePackageUpgradeDialog({ transport, trigger, documentRef: doc });
  await dialog.open();
  assert.equal(dialog.state, 'held');
  assert.equal(dialog.parts.heading.textContent, 'An update is in progress');
  assert.match(dialog.parts.status.textContent, /operation op-4/);
  assert.deepEqual(calls, ['status']);

  status = answers({ last_operation: { id: 'op-4', status: 'succeeded' } });
  button(dialog.parts.dialog, 'Check again').click();
  await settle();
  assert.equal(dialog.state, 'review');
  assert.deepEqual(calls, ['status', 'status', 'review']);

  // One that needs reconciliation is shown, with nothing to retry here.
  const held = harness({
    status: answers({
      operation: {
        id: 'op-2',
        status: 'reconcile_required',
        to_version: '0.2.0',
        reason: 'The plugin replacement stopped partway.'
      }
    })
  });
  const blocked = new HomePackageUpgradeDialog({
    transport: held.transport,
    trigger: held.trigger,
    documentRef: held.doc
  });
  await blocked.open();
  assert.equal(blocked.state, 'held');
  assert.match(
    blocked.parts.status.textContent,
    /op-2.*stopped partway.*Nothing is chosen automatically/
  );
  assert.equal(button(blocked.parts.dialog, 'Check again'), null);
  assert.deepEqual(held.calls, ['status']);
});

test('closing while the outcome is being checked stops checking', async () => {
  const { doc, trigger, calls, transport } = harness({
    commit: answers(lost()),
    outcome: answers({ review: 'pending' })
  });
  const dialog = new HomePackageUpgradeDialog({
    transport,
    trigger,
    documentRef: doc,
    outcomeDelays: [0, 60_000, 60_000]
  });
  await dialog.open();
  const committing = dialog.commit();
  await settle();
  assert.equal(dialog.state, 'checking');
  assert.equal(dialog.close(), true);
  await committing; // the pending wait is woken and the check ends
  assert.equal(dialog.timer, null);
  assert.equal(calls.filter(call => call.startsWith('outcome')).length, 1);
  assert.equal(dialog.state, 'closed');
});

test('a plugin row names what its Homes are going through', () => {
  assert.equal(homeUpgradeRowNotice(null), null);
  assert.equal(homeUpgradeRowNotice({ homes: [] }), null);
  assert.deepEqual(homeUpgradeRowNotice({ homes: [{ name: 'North Studio' }] }), {
    state: 'homes',
    text: 'Used by North Studio; Update moves it with the plugin.'
  });
  assert.equal(
    homeUpgradeRowNotice({ homes: [{ name: 'A' }, { name: 'B' }, { name: 'C' }] }).text,
    'Used by A, B and C; Update moves them together.'
  );
  assert.deepEqual(
    homeUpgradeRowNotice({
      homes: [{ name: 'A' }],
      operation: { id: 'op-3', status: 'claimed', to_version: '0.2.0' }
    }),
    { state: 'running', text: 'Home update to 0.2.0 is in progress (operation op-3).' }
  );
  // A finished operation is history: it does not hold the row.
  assert.equal(
    homeUpgradeRowNotice({
      homes: [{ name: 'A' }],
      last_operation: { id: 'op-3', status: 'succeeded' }
    }).state,
    'homes'
  );
  assert.equal(homeUpgradeRowNotice({ status_unavailable: true }).state, 'unavailable');
});

test('a plugin row keeps an operation holding its Homes visible without an update badge', async () => {
  const installed = name => ({
    name,
    version: '0.1.0',
    format: 'claude',
    enabled: true,
    workspace_surfaces: { assistant_program_homes: [{ id: 'studio-home' }] }
  });
  const plugins = pluginsPage({
    updateResponses: [
      {
        ok: true,
        data: {
          plugins: [
            installed('held-home'),
            installed('other-owners-home'),
            installed('unreadable-home'),
            installed('quiet-home'),
            { name: 'plain', version: '1.0.0', format: 'claude', enabled: true }
          ]
        }
      }
    ],
    statusAnswers: {
      'held-home': {
        body: {
          plugin_id: 'held-home',
          homes: [{ name: 'North Studio' }],
          operation: {
            id: 'op-4',
            status: 'reconcile_required',
            to_version: '0.2.0',
            reason: 'The plugin replacement stopped partway.'
          }
        }
      },
      'other-owners-home': {
        status: 404,
        body: { code: 'not_found', message: 'This plugin has no Home package upgrade' }
      },
      'unreadable-home': {
        status: 503,
        body: { code: 'home_upgrade_unavailable', message: 'unavailable' }
      },
      'quiet-home': {
        body: {
          plugin_id: 'quiet-home',
          homes: [{ name: 'North Studio' }, { name: 'South Label' }]
        }
      }
    }
  });
  await plugins.page.loadPlugins();
  await settle();
  await settle();
  const html = plugins.pluginList.innerHTML;
  assert.match(
    html,
    /data-plugin-home-upgrade="attention">Home update to 0\.2\.0 needs attention \(operation op-4\)\. The plugin replacement stopped partway\. Nothing is chosen automatically\./
  );
  assert.match(
    html,
    /data-plugin-home-upgrade="homes">Used by North Studio and South Label; Update moves them together\./
  );
  assert.match(
    html,
    /data-plugin-home-upgrade="unavailable">Home update status is unavailable right now\./
  );
  assert.equal((html.match(/data-plugin-home-upgrade=/g) || []).length, 3);
  // Only plugins that declare Homes are asked about.
  assert.ok(!plugins.requests.some(request => request.includes('/plain/home-upgrade')));
});
