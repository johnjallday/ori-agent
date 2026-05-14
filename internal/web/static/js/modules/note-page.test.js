// Tests for note-page.js helpers that do not require a browser DOM.

import { test } from 'node:test';
import assert from 'node:assert/strict';

const {
  createWorkspaceNote,
  noteTabsStateKey,
} = await import('./note-page.js');

const {
  notePath,
  notePathForNote,
  readFocusedNoteRoute,
  readWorkspaceNotesRoute,
  workspaceNotePath,
  workspaceNotePathForNote,
  workspaceNotesPath,
} = await import('./note-routes.js');

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

test('readWorkspaceNotesRoute parses workspace notes route without note id', () => {
  assert.deepEqual(
    readWorkspaceNotesRoute('/workspaces/ws%201/notes'),
    { workspaceId: 'ws 1', noteId: '' },
  );
});

test('readWorkspaceNotesRoute parses workspace notes route with note id', () => {
  assert.deepEqual(
    readWorkspaceNotesRoute('/workspaces/ws%201/notes/note%2F1?tab=notes#Heading'),
    { workspaceId: 'ws 1', noteId: 'note/1' },
  );
});

test('readWorkspaceNotesRoute ignores non-notes routes', () => {
  assert.deepEqual(readWorkspaceNotesRoute('/workspaces/ws-1/canvas'), { workspaceId: '', noteId: '' });
  assert.deepEqual(readWorkspaceNotesRoute('/notes/note-1'), { workspaceId: '', noteId: '' });
});

test('readFocusedNoteRoute parses focused note routes only', () => {
  assert.deepEqual(readFocusedNoteRoute('/notes/note%201?tab=notes#Heading'), { noteId: 'note 1' });
  assert.deepEqual(readFocusedNoteRoute('/workspaces/ws-1/notes/note-1'), { noteId: '' });
  assert.deepEqual(readFocusedNoteRoute('/workspaces/ws-1/notes'), { noteId: '' });
});

test('focused note path builders encode note ids', () => {
  assert.equal(notePath('note 1'), '/notes/note%201');
  assert.equal(notePath('note/1', 'Heading One'), '/notes/note%2F1#Heading%20One');
  assert.equal(notePath('note/1', '#Already%20Encoded'), '/notes/note%2F1#Already%20Encoded');
  assert.equal(notePath(''), '/workspaces');
  assert.equal(notePathForNote({ id: 'note 1' }, 'Intro'), '/notes/note%201#Intro');
  assert.equal(notePathForNote({ workspace_id: 'ws-1' }), '');
});

test('workspace note path builders encode workspace and note ids', () => {
  assert.equal(workspaceNotesPath('ws 1'), '/workspaces/ws%201/notes');
  assert.equal(workspaceNotePath('ws 1', 'note/1'), '/workspaces/ws%201/notes/note%2F1');
  assert.equal(
    workspaceNotePath('ws 1', 'note/1', 'Heading One'),
    '/workspaces/ws%201/notes/note%2F1#Heading%20One',
  );
  assert.equal(
    workspaceNotePath('ws 1', 'note/1', '#Already%20Encoded'),
    '/workspaces/ws%201/notes/note%2F1#Already%20Encoded',
  );
  assert.equal(workspaceNotePath('', 'note-1'), '/workspaces');
});

test('workspaceNotePathForNote converts known notes to workspace-scoped URLs', () => {
  assert.equal(
    workspaceNotePathForNote({ id: 'note 1', workspace_id: 'ws 1' }),
    '/workspaces/ws%201/notes/note%201',
  );
  assert.equal(
    workspaceNotePathForNote({ id: 'note-1', folder_id: 'folder-1' }, 'Intro'),
    '/workspaces/folder-1/notes/note-1#Intro',
  );
  assert.equal(workspaceNotePathForNote({ id: 'note-1' }), '');
});
