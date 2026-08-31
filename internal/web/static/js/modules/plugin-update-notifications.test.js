// Tests for the cached Plugins-page update notification helper.
// Run with: node --test internal/web/static/js/modules/plugin-update-notifications.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const SOURCE = readFileSync(new URL('./plugin-update-notifications.js', import.meta.url), 'utf8');

function loadModule() {
  const sandbox = {
    window: {
      setInterval: () => 1,
      clearInterval: () => {}
    },
    console,
    Map,
    Object,
    Array,
    Number,
    String,
    Boolean,
    Promise,
    JSON,
    Error
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(SOURCE, sandbox, { filename: 'plugin-update-notifications.js' });
  return sandbox.window.PluginUpdateNotifications;
}

test('normalization rejects malformed rows, deduplicates names, and sorts deterministically', () => {
  const updates = loadModule();
  const snapshot = updates.normalize({
    checking: true,
    last_successful_check_at: '2026-08-31T12:00:00Z',
    updates: [
      null,
      { name: '', available: true },
      { name: 'zulu', installed_version: '1', available_version: '2', available: true },
      { name: 'alpha', components_changed: true, available: true },
      { name: 'zulu', installed_version: '1', available_version: '3', available: true }
    ]
  });

  assert.equal(snapshot.checking, true);
  assert.equal(snapshot.lastSuccessfulCheckAt, '2026-08-31T12:00:00Z');
  assert.deepEqual(
    Array.from(snapshot.updates, update => update.name),
    ['alpha', 'zulu']
  );
  assert.equal(snapshot.updates[1].availableVersion, '3');

  const index = updates.indexUpdates(snapshot);
  assert.equal(index.get('alpha').componentsChanged, true);
  assert.equal(index.get('zulu').availableVersion, '3');
});

test('pending, empty, singular, and plural presentation copy is explicit', () => {
  const updates = loadModule();
  assert.equal(updates.presentation({ checking: true }).state, 'pending');
  assert.match(updates.presentation({ checking: true }).title, /Checking/);

  const empty = updates.presentation({ updates: [], checking: false });
  assert.equal(empty.state, 'empty');
  assert.equal(empty.count, 0);
  assert.match(empty.title, /No plugin updates/);

  const singular = updates.presentation({
    updates: [
      {
        name: 'demo',
        installed_version: '1.0.0',
        available_version: '2.0.0',
        available: true
      }
    ]
  });
  assert.equal(singular.count, 1);
  assert.match(singular.title, /^1 plugin update is available$/);
  assert.match(singular.detail, /demo 2\.0\.0/);

  const plural = updates.presentation({
    updates: [
      { name: 'alpha', available: true },
      { name: 'bravo', components_changed: true, available: true }
    ]
  });
  assert.equal(plural.count, 2);
  assert.match(plural.title, /^2 plugin updates are available$/);
});

test('plugin notices distinguish source versions from footprint-only changes', () => {
  const updates = loadModule();
  const versionNotice = updates.pluginNotice({
    installedVersion: '1.0.0',
    availableVersion: '2.0.0',
    componentsChanged: false,
    available: true
  });
  assert.equal(versionNotice.label, 'Update available · 2.0.0');
  assert.equal(versionNotice.detail, 'Source version 2.0.0 is available.');

  const footprintNotice = updates.pluginNotice({
    installedVersion: '',
    availableVersion: '',
    componentsChanged: true,
    available: true
  });
  assert.equal(footprintNotice.label, 'Update available · components changed');
  assert.equal(footprintNotice.detail, 'The plugin’s trusted component footprint changed.');
  assert.equal(updates.pluginNotice({ available: false }), null);
});

test('untrusted plugin names and versions remain inert presentation text', () => {
  const updates = loadModule();
  const hostile = `"><img src=x onerror=alert('x')>`;
  const escaped = updates.escapeHTML(hostile);
  assert.equal(escaped, '&quot;&gt;&lt;img src=x onerror=alert(&#39;x&#39;)&gt;');
  assert.doesNotMatch(escaped, /<img/);

  const model = updates.presentation({
    updates: [
      {
        name: hostile,
        installed_version: '1',
        available_version: '<script>',
        available: true
      }
    ]
  });
  assert.match(model.detail, /<script>/, 'models stay plain text until the renderer escapes them');
  assert.doesNotMatch(updates.escapeHTML(model.detail), /<script>/);

  const index = updates.indexUpdates({ updates: [{ name: '__proto__', available: true }] });
  assert.equal(index.get('__proto__').available, true, 'Map indexing is safe for special names');
});

test('a status endpoint failure is isolated and does not reject neighboring page work', async () => {
  const updates = loadModule();
  let snapshots = 0;
  let errors = 0;
  const controller = updates.createController({
    load: async () => {
      throw new Error('status unavailable');
    },
    onSnapshot: () => snapshots++,
    onError: () => errors++,
    setIntervalFn: () => 1,
    clearIntervalFn: () => {}
  });

  const outcomes = await Promise.allSettled([
    Promise.resolve('plugin list loaded'),
    controller.refresh()
  ]);
  assert.equal(outcomes[0].status, 'fulfilled');
  assert.equal(outcomes[0].value, 'plugin list loaded');
  assert.equal(outcomes[1].status, 'fulfilled');
  assert.equal(outcomes[1].value, null);
  assert.equal(snapshots, 0);
  assert.equal(errors, 1);
});

test('controller starts one modest polling interval and stops it once', async () => {
  const updates = loadModule();
  const scheduled = [];
  const cancelled = [];
  let loads = 0;
  const controller = updates.createController({
    load: async () => {
      loads++;
      return { updates: [] };
    },
    setIntervalFn: (callback, delay) => {
      scheduled.push({ callback, delay });
      return 17;
    },
    clearIntervalFn: timer => cancelled.push(timer)
  });

  assert.equal(controller.start(), true);
  assert.equal(controller.start(), false);
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(scheduled.length, 1);
  assert.ok(scheduled[0].delay >= 60_000, 'page-local cache polling should be modest');
  assert.equal(loads, 1);
  assert.equal(controller.stop(), true);
  assert.equal(controller.stop(), false);
  assert.deepEqual(cancelled, [17]);
});
