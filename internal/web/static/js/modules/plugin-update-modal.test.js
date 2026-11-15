// Regression guard for the Plugins page update confirmation surface.
// Run with: node --test internal/web/static/js/modules/plugin-update-modal.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const PLUGINS_PAGE_SOURCE = readFileSync(new URL('../plugins.js', import.meta.url), 'utf8');

test('source preview opens its disclosure and cancel restores focus without confirming installation', async () => {
  const ids = [
    'pluginSource',
    'pluginFormat',
    'pluginInstallSection',
    'pluginTrust',
    'pluginTrustBody',
    'pluginTrustTitle',
    'pluginTrustConfirm',
    'pluginPreviewBtn'
  ];
  const nodes = new Map(ids.map(id => [id, { ...element(), style: {} }]));
  nodes.get('pluginSource').value = '/fictional/fixture';
  nodes.get('pluginFormat').value = 'claude';
  let titleFocus = 0;
  let launcherFocus = 0;
  nodes.get('pluginTrustTitle').focus = () => {
    titleFocus += 1;
  };
  nodes.get('pluginPreviewBtn').focus = () => {
    launcherFocus += 1;
  };
  const requests = [];
  const sandbox = vm.createContext({
    document: { activeElement: null, addEventListener() {}, getElementById: id => nodes.get(id) },
    alert(message) {
      throw new Error(message);
    },
    window: {
      PluginLifecycle: {
        async request(method, url, body) {
          requests.push({ method, url, body });
          return { ok: true, data: { trust: { Skills: ['fixture-skill'] } } };
        },
        renderTrustReport: report => ({ report })
      },
      PluginUpdateNotifications: {
        escapeHTML: String,
        createController: () => ({ refresh() {}, start() {}, stop() {} })
      }
    }
  });
  vm.runInContext(PLUGINS_PAGE_SOURCE, sandbox, { filename: 'plugins.js' });
  await sandbox.window.pluginConfirmInstall();
  assert.equal(requests.length, 0, 'no confirmation is possible before preview');
  await sandbox.window.pluginPreview();
  assert.equal(nodes.get('pluginInstallSection').open, true);
  assert.equal(nodes.get('pluginTrust').style.display, 'block');
  assert.equal(titleFocus, 1);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, '/api/plugins/install');
  assert.equal(requests[0].body.confirm, false);
  sandbox.window.pluginCancelInstall();
  assert.equal(nodes.get('pluginTrust').style.display, 'none');
  assert.equal(launcherFocus, 1);
  await sandbox.window.pluginConfirmInstall();
  assert.equal(requests.length, 1, 'cancel must invalidate the pending installation');
});

test('cached status redraws cannot erase a failed plugin-list refresh', async () => {
  const list = { ...element(), contains: () => false };
  let unavailable = true;
  let onSnapshot;
  const sandbox = vm.createContext({
    document: {
      activeElement: null,
      addEventListener() {},
      getElementById: id => (id === 'pluginList' ? list : null)
    },
    window: {
      PluginLifecycle: {
        request: async () =>
          unavailable
            ? { ok: false, error: 'List unavailable' }
            : { ok: true, data: { plugins: [] } }
      },
      PluginUpdateNotifications: {
        escapeHTML: String,
        indexUpdates: () => new Map(),
        createController(options) {
          onSnapshot = options.onSnapshot;
          return { refresh() {}, start() {}, stop() {} };
        }
      }
    }
  });
  vm.runInContext(PLUGINS_PAGE_SOURCE, sandbox, { filename: 'plugins.js' });
  for (const failed of [true, false, true, false]) {
    unavailable = failed;
    await sandbox.window.loadPlugins();
    onSnapshot({}, null);
    assert.match(
      list.innerHTML,
      failed ? /Failed to load plugins: List unavailable/ : /No plugins installed yet/
    );
  }
});

function element() {
  return {
    dataset: {},
    disabled: false,
    innerHTML: '',
    textContent: '',
    isConnected: true,
    appendChild(child) {
      this.child = child;
    },
    addEventListener() {},
    querySelectorAll() {
      return [];
    },
    focus() {}
  };
}

test('a changed plugin update opens the dedicated trust modal', async () => {
  const modal = element();
  const title = element();
  const trustBody = element();
  const confirm = element();
  const trigger = element();
  const elements = new Map([
    ['pluginUpdateModal', modal],
    ['pluginUpdateModalLabel', title],
    ['pluginUpdateTrustBody', trustBody],
    ['pluginUpdateConfirm', confirm]
  ]);
  const requests = [];
  let modalShows = 0;
  const trust = { Skills: ['example-skill'] };

  const sandbox = {
    alert() {},
    document: {
      activeElement: trigger,
      body: { classList: { add() {}, remove() {} } },
      addEventListener() {},
      getElementById(id) {
        return elements.get(id) || null;
      },
      querySelectorAll() {
        return [];
      }
    },
    window: {
      PluginLifecycle: {
        LIFECYCLE_LABELS: { ENABLED: 'enabled', DISABLED: 'disabled' },
        capitalize(value) {
          return value;
        },
        async request(method, url, body) {
          requests.push({ method, url, body });
          return { ok: true, data: { changed: true, trust } };
        },
        renderTrustReport(report) {
          return { report };
        }
      },
      PluginUpdateNotifications: {
        escapeHTML(value) {
          return String(value);
        },
        createController() {
          return { refresh() {}, start() {}, stop() {} };
        },
        indexUpdates() {
          return new Map();
        },
        pluginNotice() {
          return null;
        }
      },
      bootstrap: {
        Modal: {
          getOrCreateInstance(target) {
            assert.equal(target, modal);
            return {
              show() {
                modalShows += 1;
              },
              hide() {}
            };
          }
        }
      }
    }
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(PLUGINS_PAGE_SOURCE, sandbox, { filename: 'plugins.js' });

  await sandbox.window.pluginUpdate('reaper-plugin');

  assert.equal(requests.length, 1);
  assert.equal(requests[0].method, 'POST');
  assert.equal(requests[0].url, '/api/plugins/reaper-plugin/update');
  assert.equal(requests[0].body.confirm, false);
  assert.equal(modalShows, 1);
  assert.equal(title.textContent, 'Update reaper-plugin');
  assert.equal(trustBody.child.report, trust);
  assert.equal(confirm.textContent, 'Confirm update');
});
