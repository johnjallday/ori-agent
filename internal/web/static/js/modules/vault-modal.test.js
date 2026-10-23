// Tests for the on-demand Vaults launcher in the top bar (nav-collapse FR6).
// Run with:
//   node --test internal/web/static/js/modules/vault-modal.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const SOURCE = readFileSync(new URL('./vault-modal.js', import.meta.url), 'utf8');

// Loads vault-modal.js against a page that has only the launcher and its
// divider; the modal itself is absent, so init stops after the launcher check.
async function loadPage({ launcherHidden, vaultsResponse }) {
  const launcher = { hidden: launcherHidden };
  const divider = { hidden: launcherHidden };
  const requests = [];
  const document = {
    readyState: 'complete',
    addEventListener() {},
    getElementById(id) {
      if (id === 'vaultLauncherButton') return launcher;
      if (id === 'vaultLauncherDivider') return divider;
      return null;
    }
  };
  const fetch = async url => {
    requests.push(url);
    if (vaultsResponse instanceof Error) throw vaultsResponse;
    return {
      ok: true,
      headers: { get: () => 'application/json' },
      json: async () => vaultsResponse
    };
  };
  const sandbox = { document, fetch, window: { FormData: class {} }, Map, Set, Number, Array };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(SOURCE, sandbox, { filename: 'vault-modal.js' });
  await new Promise(resolve => setImmediate(resolve));
  return { launcher, divider, requests };
}

test('the launcher stays hidden while no vault holds a record', async () => {
  const page = await loadPage({
    launcherHidden: true,
    vaultsResponse: { vaults: [{ id: 'a', record_count: 0 }] }
  });
  assert.deepEqual(page.requests, ['/api/vault/vaults']);
  assert.equal(page.launcher.hidden, true);
  assert.equal(page.divider.hidden, true);
});

test('the launcher and its divider appear once any vault holds a record', async () => {
  const page = await loadPage({
    launcherHidden: true,
    vaultsResponse: {
      vaults: [
        { id: 'a', record_count: 0 },
        { id: 'b', record_count: 1 }
      ]
    }
  });
  assert.equal(page.launcher.hidden, false);
  assert.equal(page.divider.hidden, false);
});

test('the vault page renders the launcher shown and never checks or hides it', async () => {
  const page = await loadPage({ launcherHidden: false, vaultsResponse: { vaults: [] } });
  assert.deepEqual(page.requests, []);
  assert.equal(page.launcher.hidden, false);
});

test('an unavailable vault store leaves the launcher hidden', async () => {
  const page = await loadPage({
    launcherHidden: true,
    vaultsResponse: new Error('secret store unavailable')
  });
  assert.equal(page.launcher.hidden, true);
  assert.equal(page.divider.hidden, true);
});
