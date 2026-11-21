import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  initialNotesFolder,
  notesDirectoryQuery,
  notebookNoteActions
} from './my-notes-browser.js';

const notebook = {
  status: 'connected',
  browse_ref: 'ref',
  source: { origin: 'created_by_ori', kind: 'markdown', name: 'My Notes' }
};
const vault = {
  status: 'connected',
  browse_ref: 'vault',
  source: { origin: 'connected', kind: 'obsidian', name: 'Research & Notes' }
};

test('browsing starts at the real Inbox or connection root, with separate components', () => {
  assert.deepEqual(initialNotesFolder(notebook), ['Inbox']);
  assert.deepEqual(initialNotesFolder(vault), []);
  assert.equal(
    notesDirectoryQuery('ref', ['Projects', 'Sound & Light']),
    '/browse?ref=ref&part=Projects&part=Sound+%26+Light'
  );
});

test('preview checks are for the existing managed-note kinds and use the generation ref', () => {
  assert.equal(
    notebookNoteActions(notebook, ['Inbox'], 'a.md').preview,
    '/preview/inbox/a.md?ref=ref'
  );
  assert.equal(
    notebookNoteActions(notebook, ['Daily'], '2026-01-21.md').preview,
    '/preview/daily/2026-01-21.md?ref=ref'
  );
  assert.equal(
    notebookNoteActions(notebook, ['Projects', 'A & B'], 'README.md').preview,
    '/preview/project/A%20%26%20B?ref=ref'
  );
  assert.deepEqual(notebookNoteActions(notebook, ['Projects', 'A & B', 'Nested'], 'README.md'), {
    preview: '',
    external: ''
  });
  assert.deepEqual(notebookNoteActions(notebook, ['Resources'], 'mine.md'), {
    preview: '',
    external: ''
  });
});

test('connected vaults never offer Ori preview; plain Markdown has no Obsidian link', () => {
  assert.deepEqual(notebookNoteActions(vault, ['Inbox'], 'My # note.MARKDOWN'), {
    preview: '',
    external: 'obsidian://open?vault=Research%20%26%20Notes&file=Inbox%2FMy%20%23%20note'
  });
  assert.deepEqual(
    notebookNoteActions({ ...vault, source: { ...vault.source, kind: 'markdown' } }, [], 'note.md'),
    { preview: '', external: '' }
  );
});
