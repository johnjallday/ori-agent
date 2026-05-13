// Tests for note-page.js helpers that do not require a browser DOM.

import { test } from 'node:test';
import assert from 'node:assert/strict';

const {
  createWorkspaceNote,
  noteTabsStateKey,
} = await import('./note-page.js');

test('createWorkspaceNote posts an empty note to the workspace notes endpoint', async () => {
  const calls = [];
  const note = await createWorkspaceNote('ws 1', async (url, options) => {
    calls.push({ url, options });
    return {
      ok: true,
      async json() {
        return { note: { id: 'note-1', name: 'Untitled', content: '' } };
      },
    };
  });

  assert.deepEqual(note, { id: 'note-1', name: 'Untitled', content: '' });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/workspaces/ws%201/notes');
  assert.equal(calls[0].options.method, 'POST');
  assert.equal(calls[0].options.headers['Content-Type'], 'application/json');
  assert.deepEqual(JSON.parse(calls[0].options.body), { name: 'Untitled', content: '' });
});

test('createWorkspaceNote throws on failed responses', async () => {
  await assert.rejects(
    () => createWorkspaceNote('ws-1', async () => ({ ok: false, status: 500 })),
    /Failed to create note: 500/
  );
});

test('createWorkspaceNote requires a workspace id', async () => {
  await assert.rejects(
    () => createWorkspaceNote('', async () => ({ ok: true })),
    /No workspace selected/
  );
});

test('noteTabsStateKey scopes persisted tab state by workspace', () => {
  assert.equal(noteTabsStateKey('ws-1'), 'note.tabs.workspace.ws-1');
  assert.equal(noteTabsStateKey(''), 'note.tabs');
});
