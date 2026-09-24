// Tests for plugin-recovery-client.js — the one request shape the Create
// Workspace card and the setup quest send to a blueprint's plugin-recovery
// endpoint.
//
// Run with: node --test internal/web/static/js/modules/plugin-recovery-client.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const LIFECYCLE_SOURCE = readFileSync(new URL('./plugin-lifecycle.js', import.meta.url), 'utf8');
const CLIENT_SOURCE = readFileSync(new URL('./plugin-recovery-client.js', import.meta.url), 'utf8');

function load(respond) {
  const requests = [];
  const sandbox = {
    window: {},
    console,
    JSON,
    Number,
    String,
    encodeURIComponent,
    fetch: async (url, options) => {
      requests.push({ url, method: options.method, body: JSON.parse(options.body) });
      const { status = 200, body = {} } = respond(url, requests.length) || {};
      return { ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body) };
    }
  };
  vm.createContext(sandbox);
  vm.runInContext(LIFECYCLE_SOURCE, sandbox, { filename: 'plugin-lifecycle.js' });
  vm.runInContext(CLIENT_SOURCE, sandbox, { filename: 'plugin-recovery-client.js' });
  return { client: sandbox.window.PluginRecoveryClient, requests };
}

test('the endpoint is built from the qualified template ID', () => {
  const { client } = load(() => ({}));
  assert.equal(
    client.endpoint('plugin:reaper-plugin:reaper-song'),
    '/api/project-templates/plugin%3Areaper-plugin%3Areaper-song/plugin-recovery'
  );
});

test('a preview sends an action and a plugin name, never a source, and changes nothing', async () => {
  const { client, requests } = load(() => ({
    body: { release: '0.1.0', source: 'https://example.invalid/release', trust: {} }
  }));
  const result = await client.previewRecovery(
    'plugin:reaper-plugin:reaper-song',
    'install_plugin',
    'music-project-management',
    0
  );
  assert.equal(result.ok, true);
  assert.equal(result.data.release, '0.1.0');
  assert.equal(requests.length, 1);
  assert.equal(requests[0].method, 'POST');
  assert.deepEqual(JSON.parse(JSON.stringify(requests[0].body)), {
    action: 'install_plugin',
    plugin: 'music-project-management',
    confirm: false,
    generation: 0
  });
});

test('a confirmation echoes the generation and the previewed release', async () => {
  const { client, requests } = load(() => ({ body: { outcome: { completed: true } } }));
  await client.confirmRecovery(
    'plugin:x:y',
    'install_plugin',
    'music-project-management',
    7,
    '0.1.0'
  );
  assert.deepEqual(JSON.parse(JSON.stringify(requests[0].body)), {
    action: 'install_plugin',
    plugin: 'music-project-management',
    confirm: true,
    generation: 7,
    release: '0.1.0'
  });
});

test('a confirmation without a previewed release omits the field', async () => {
  const { client, requests } = load(() => ({ body: {} }));
  await client.confirmRecovery('plugin:x:y', 'enable_plugin', 'music-project-management', 3, '');
  assert.equal('release' in requests[0].body, false);
  assert.equal(requests[0].body.confirm, true);
});

test('a refused confirmation comes back as a structured result, not a throw', async () => {
  const { client } = load(() => ({
    status: 409,
    body: {
      error: 'This plugin changed while you were reviewing it.',
      outcome: { summary: 'This plugin changed while you were reviewing it.' }
    }
  }));
  const result = await client.confirmRecovery('plugin:x:y', 'install_plugin', 'p', 1, '0.1.0');
  assert.equal(result.ok, false);
  assert.equal(result.status, 409);
  assert.equal(result.data.outcome.summary, 'This plugin changed while you were reviewing it.');
});
