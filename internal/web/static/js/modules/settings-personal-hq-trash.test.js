import assert from 'node:assert/strict';
import { test } from 'node:test';
import { wirePersonalHQTrash } from './settings-personal-hq-trash.js';

const statusURL = '/api/personal-hq/status';
const reviewURL = '/api/workspaces/hq-1?trash_personal_hq=true';
const folder = '/workspace/My HQ';

function harness({ allow = true, valid = true, trashed = true } = {}) {
  const calls = [];
  const prompts = [];
  const status = { textContent: '' };
  const button = {
    disabled: false,
    addEventListener(name, callback) {
      assert.equal(name, 'click');
      this.click = callback;
    }
  };
  wirePersonalHQTrash({
    button,
    status,
    request: async (url, options) => {
      calls.push({ url, ...options });
      const data = {
        [statusURL]: { status: { valid, workspace_id: 'hq-1' } },
        [reviewURL]: { name: 'My HQ', folder_path: folder },
        [`${reviewURL}&confirm=true`]: { trashed }
      }[url];
      assert.ok(data, `unexpected request: ${url}`);
      return { ok: true, json: async () => data };
    },
    confirm: message => {
      prompts.push(message);
      return allow;
    }
  });
  return { button, status, calls, prompts };
}

test('Settings confirms the exact HQ folder before requesting a trash-only move', async () => {
  const { button, status, calls, prompts } = harness();
  await button.click();
  assert.deepEqual(
    calls.map(c => c.url),
    [statusURL, reviewURL, `${reviewURL}&confirm=true`]
  );
  assert.match(prompts[0], /My HQ.*everything inside/s);
  assert.ok(prompts[0].includes(folder));
  assert.equal(calls[2].method, 'DELETE');
  assert.deepEqual(JSON.parse(calls[2].body), { folder_path: folder });
  assert.match(status.textContent, /moved to the system Trash/);
  assert.equal(button.disabled, false);
});

test('Settings leaves the reviewed HQ in place if the user cancels', async () => {
  const { button, status, calls } = harness({ allow: false });
  await button.click();
  assert.deepEqual(
    calls.map(c => c.url),
    [statusURL, reviewURL]
  );
  assert.match(status.textContent, /No files were moved/);
});

test('Settings does not guess an HQ folder when no current HQ is registered', async () => {
  const { button, status, calls, prompts } = harness({ valid: false });
  await button.click();
  assert.deepEqual(
    calls.map(c => c.url),
    [statusURL]
  );
  assert.equal(prompts.length, 0);
  assert.match(status.textContent, /No current Personal HQ/);
});

test('Settings reports uncertainty instead of claiming a move without confirmation', async () => {
  const { button, status } = harness({ trashed: false });
  await button.click();
  assert.match(status.textContent, /did not confirm the Trash move/);
});
