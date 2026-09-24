// Tests for the Plugins page banner built from the Workspace Directory's
// plugin list. Run with:
//   node --test internal/web/static/js/modules/plugin-workspace-list.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const SOURCE = readFileSync(new URL('./plugin-workspace-list.js', import.meta.url), 'utf8');

function loadModule() {
  const sandbox = { window: {}, Set, Array, String, Object };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(SOURCE, sandbox, { filename: 'plugin-workspace-list.js' });
  return sandbox.window.PluginWorkspaceList;
}

const payload = {
  pending: [
    {
      kind: 'install',
      name: 'reaper-plugin',
      listed_version: '0.8.0',
      source: 'https://github.com/johnjallday/reaper-plugin#sha=abc',
      format: 'claude',
      installable: true,
      fingerprint: 'f1'
    },
    {
      kind: 'switch',
      name: 'notes',
      listed_version: '1.2.0',
      installed_version: '1.1.0',
      direction: 'update',
      source: 'https://example.test/notes#sha=def',
      installable: true,
      fingerprint: 'f2'
    },
    { kind: 'uninstall', name: 'old-tool', installed_version: '2.0.0', installable: true },
    {
      kind: 'install',
      name: 'local-thing',
      source: '/Users/someone/plugins/local-thing',
      installable: false,
      reason: "Can't install on this Mac: it was installed from a local folder."
    },
    { kind: 'bogus', name: 'ignored' },
    null
  ]
};

test('the banner titles the count and offers each change with its own button', () => {
  const list = loadModule();
  const state = list.normalize(payload);
  assert.equal(state.pending.length, 4);
  const html = list.renderBanner(state);

  assert.match(html, /Your Workspace Directory lists 4 plugin changes for this Mac/);
  assert.match(
    html,
    /data-list-action="install" data-list-name="reaper-plugin">Review and install</
  );
  assert.match(html, /Update notes from 1\.1\.0 to 1\.2\.0/);
  assert.match(html, /data-list-action="switch" data-list-name="notes">Review update</);
  assert.match(html, /data-list-action="uninstall" data-list-name="old-tool">Uninstall</);
  assert.equal((html.match(/Skip on this Mac/g) || []).length, 4);
});

test('an entry this Mac cannot install shows the reason and no install button', () => {
  const list = loadModule();
  const html = list.renderBanner(list.normalize(payload));
  assert.match(html, /Can&#39;t install on this Mac: it was installed from a local folder\./);
  assert.doesNotMatch(html, /data-list-action="install" data-list-name="local-thing"/);
});

test('the banner is empty when nothing is pending, and one change reads in the singular', () => {
  const list = loadModule();
  assert.equal(list.renderBanner(list.normalize({ pending: [] })), '');
  assert.equal(list.title(1), 'Your Workspace Directory lists 1 plugin change for this Mac');
});

test('skipped changes leave the banner and the badge count', () => {
  const list = loadModule();
  const skipped = {
    pending: payload.pending.filter(Boolean).map(change => ({
      ...change,
      skipped: change.name !== 'notes'
    }))
  };
  assert.equal(list.badgeCount(skipped), 1);
  assert.match(list.renderBanner(list.normalize(skipped)), /lists 1 plugin change for this Mac/);
  assert.equal(list.badgeCount({}), 0);
});

test('a list that cannot be read shows its message instead of changes', () => {
  const list = loadModule();
  const html = list.renderBanner(
    list.normalize({
      read_error: 'Your plugin list (Plugins.json) could not be read: it is not valid JSON (<eof>)',
      pending: payload.pending
    })
  );
  assert.match(
    html,
    /Your plugin list \(Plugins\.json\) could not be read: it is not valid JSON \(&lt;eof&gt;\)/
  );
  assert.doesNotMatch(html, /Review and install/);
});

test('the menu badge counts un-skipped changes and hides at zero or on failure', async () => {
  const badge = {
    textContent: '',
    hidden: true,
    attributes: {},
    setAttribute(name, value) {
      this.attributes[name] = value;
    }
  };
  const sandbox = {
    window: {},
    Set,
    Array,
    String,
    Object,
    document: {
      readyState: 'complete',
      querySelector: () => null,
      querySelectorAll: () => [badge]
    }
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(SOURCE, sandbox, { filename: 'plugin-workspace-list.js' });
  const list = sandbox.window.PluginWorkspaceList;

  const answer = body => async () => ({ ok: true, json: async () => body });
  assert.equal(await list.refreshBadge(answer(payload)), 4);
  assert.equal(badge.textContent, '4');
  assert.equal(badge.hidden, false);
  assert.equal(badge.attributes['aria-label'], '4 plugin changes for this Mac');

  assert.equal(await list.refreshBadge(answer({ pending: [] })), 0);
  assert.equal(badge.hidden, true);

  await list.refreshBadge(answer(payload));
  assert.equal(
    await list.refreshBadge(async () => {
      throw new Error('offline');
    }),
    0
  );
  assert.equal(badge.hidden, true);
});
