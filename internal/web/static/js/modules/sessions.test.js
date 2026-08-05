import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./sessions.js', import.meta.url), 'utf8');

function loadSessionManager(fetchImpl = async () => ({ ok: true, json: async () => ({}) })) {
  const window = {};
  const document = { addEventListener() {} };
  vm.runInNewContext(source, { window, document, fetch: fetchImpl }, { filename: 'sessions.js' });
  return window.sessionManager;
}

// loadSessionManagerWithModal adds just enough DOM for showAddWorkspaceModal:
// the modal element it reads and a Bootstrap stub that records the show call.
function loadSessionManagerWithModal() {
  const window = {};
  const modalElement = { dataset: {} };
  const shown = [];
  const document = {
    addEventListener() {},
    getElementById: id => (id === 'addFolderModal' ? modalElement : null)
  };
  const bootstrap = {
    Modal: class {
      constructor(element) {
        this.element = element;
      }
      show() {
        shown.push(this.element);
      }
    }
  };
  vm.runInNewContext(
    source,
    { window, document, bootstrap, fetch: async () => ({ ok: true, json: async () => ({}) }) },
    { filename: 'sessions.js' }
  );
  return { manager: window.sessionManager, modalElement, shown };
}

test('a Map-origin create flags the existing modal rather than opening a second form (#292 FR-51)', () => {
  const { manager, modalElement, shown } = loadSessionManagerWithModal();

  manager.showAddWorkspaceModal({ mapOrigin: true, entryPoint: 'workspace_map_build' });
  assert.equal(modalElement.dataset.pendingMapOrigin, 'true');
  assert.equal(modalElement.dataset.pendingEntryPoint, 'workspace_map_build');
  assert.equal(shown.length, 1, 'the one existing Create Workspace modal was shown');

  // An ordinary create must not inherit the flag from a previous map build.
  manager.showAddWorkspaceModal({});
  assert.equal(
    'pendingMapOrigin' in modalElement.dataset,
    false,
    'a normal create is not a map build'
  );
});

test('workspace post-create action keeps the standard workspace destination by default', async () => {
  const manager = loadSessionManager();
  const result = await manager.applyWorkspacePostCreateAction('workspace 1');

  assert.equal(result.applied, false);
  assert.equal(result.destination, '/workspaces/workspace%201');
});

test('Personal HQ import designates the imported workspace and completes onboarding', async () => {
  const calls = [];
  const manager = loadSessionManager(async (url, options = {}) => {
    calls.push({ url, options });
    return { ok: true, json: async () => ({ success: true }) };
  });
  manager.workspacePostCreateAction = 'designate_personal_hq';

  const result = await manager.applyWorkspacePostCreateAction('hq-workspace');

  assert.equal(result.applied, true);
  assert.equal(result.destination, '/workspaces?view=map&focus=personal-hq');
  assert.equal(calls.length, 2);
  assert.equal(calls[0].url, '/api/personal-hq/replace');
  assert.deepEqual(JSON.parse(calls[0].options.body), { workspace_id: 'hq-workspace' });
  assert.equal(calls[1].url, '/api/personal-hq/onboarding-state');
  assert.deepEqual(JSON.parse(calls[1].options.body), { state: 'completed' });
});
