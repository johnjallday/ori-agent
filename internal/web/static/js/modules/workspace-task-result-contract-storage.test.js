import { test } from 'node:test';
import assert from 'node:assert/strict';

// The page module reads window at load time; give it one, then take it away so
// nothing else in this test process sees a browser.
const hadWindow = 'window' in globalThis;
if (!hadWindow) globalThis.window = {};
const { WorkspaceTaskPage } = await import('./workspace-task.js');
if (!hadWindow) delete globalThis.window;

// Saving a ticket's output fields rewrites its result_storage. The rewrite must
// keep where the ticket saves: a workspace-folder target is what a retired store
// node on a workspace folder becomes, so losing it here would silently move a
// migrated ticket's files to the default output folder.
async function saveContractWith(resultStorage) {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url: String(url), method: options.method || 'GET', body: options.body });
    return { ok: true, status: 200, text: async () => '{}' };
  };
  try {
    const page = Object.create(WorkspaceTaskPage.prototype);
    Object.assign(page, {
      workspaceId: 'ws-1',
      taskId: 'task-1',
      resultContractSaving: false,
      resultContractDraft: [{ name: 'score', type: 'number', required: true }],
      resultOutputSpecDraft: null,
      syncResultContractDraftFromDOM() {},
      setResultContractError() {},
      getTaskResultStorageTask: () => ({ id: 'task-1', result_storage: resultStorage }),
      buildOutputSpecDraftFromColumns: () => ({ columns: [] }),
      refreshResultRender() {},
      closeColumnDesignerModal() {},
      loadData: async () => {},
      notify() {}
    });
    await page.saveResultContractDraft();
  } finally {
    globalThis.fetch = originalFetch;
  }
  const patch = requests.find(request => request.method === 'PATCH');
  assert.ok(patch, 'the ticket storage should be saved with a PATCH');
  return JSON.parse(patch.body).result_storage;
}

test('saving output fields keeps a ticket on its workspace folder', async () => {
  const saved = await saveContractWith({
    enabled: true,
    storage_target: 'workspace_folder',
    workspace_folder: 'reports/daily',
    file_path: 'runs.csv'
  });
  assert.equal(saved.storage_target, 'workspace_folder');
  assert.equal(saved.workspace_folder, 'reports/daily');
  assert.equal(saved.file_path, 'runs.csv');
});

test('saving output fields on a ticket with a file path keeps no folder target', async () => {
  const saved = await saveContractWith({ enabled: true, file_path: '/data/out/runs.csv' });
  assert.equal(saved.file_path, '/data/out/runs.csv');
  assert.equal(saved.storage_target, '');
  assert.equal(saved.workspace_folder, '');
});
