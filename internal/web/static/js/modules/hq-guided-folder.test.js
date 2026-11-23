import { test } from 'node:test';
import assert from 'node:assert/strict';
import { beginHQAttempt, recordFolderWorkspace } from './hq-guided-attempt.js';
import { folderReceipt } from './hq-guided-folder.js';

test('only the finished guided workspace can add project evidence to its receipt', async t => {
  let value;
  t.mock.method(globalThis, 'fetch', () => {
    throw Error('unexpected network');
  });
  const old = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: { getItem: () => value, setItem: (_, v) => (value = v) }
  });
  t.after(() =>
    old
      ? Object.defineProperty(globalThis, 'sessionStorage', old)
      : delete globalThis.sessionStorage
  );
  const hint = beginHQAttempt('hq', 'folder');
  const calls = [];
  let state = 'reported';
  const read = async url => {
    calls.push(url);
    return url.includes('/documents-offer?')
      ? { offer: { projects: [{ kind: 'manuscript', location: 'Documents/Thesis' }] } }
      : {
          folder_agent: {
            workspace_id: 'ws',
            folder_name: 'Documents',
            route: '/workspaces/docs',
            state
          }
        };
  };
  assert.match((await folderReceipt(hint, read)).text, /No folder exploration/);
  assert.equal(calls.length, 0);
  recordFolderWorkspace(hint, 'ws');
  const result = await folderReceipt(hint, read);
  assert.match(result.text, /manuscript in Documents\/Thesis/);
  assert.equal(result.label, 'Open folder workspace');
  assert.match(
    calls[1],
    /^\/api\/personal-assistant\/folder-agent\/ws\/documents-offer\?hq=hq&attempt=/
  );
  for (state of ['looking', 'failed', 'setting_up', 'unknown']) {
    calls.length = 0;
    assert.ok(!(await folderReceipt(hint, read))?.offer);
    assert.equal(calls.length, 1);
  }
  state = 'reported';
  assert.match(
    (
      await folderReceipt(hint, url =>
        url.includes('/documents-offer?') ? Promise.reject(Error()) : read(url)
      )
    ).text,
    /first look at Documents is ready/
  );
});
