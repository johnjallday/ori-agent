import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  initialNotesFolder,
  NotebookState,
  notebookFolderTree,
  notebookListView,
  notebookNoteActions,
  notebookReaderView,
  notesDirectoryQuery
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

// Each request waits until the test answers it. Abort signals are ignored on
// purpose: the browser must refuse late answers without relying on them.
function deferredRequests() {
  const calls = [];
  const request = (path, { signal } = {}) =>
    new Promise((resolve, reject) => calls.push({ path, signal, resolve, reject }));
  const answer = (path, value) => {
    const call = calls.find(entry => entry.path === path && !entry.done);
    assert.ok(call, `no open request for ${path}; saw ${calls.map(c => c.path).join(', ')}`);
    call.done = true;
    if (value instanceof Error) call.reject(value);
    else call.resolve(value);
  };
  return { calls, request, answer, paths: () => calls.map(call => call.path) };
}

const listing = (entries, partial = false) => ({ directory: { entries, partial } });
const folder = name => ({ name, folder: true });
const file = name => ({ name, folder: false });
const httpError = (status, message = '') => Object.assign(new Error(message), { status });
const settle = () => new Promise(resolve => setImmediate(resolve));
const ROOT = '/browse?ref=ref';
const INBOX = '/browse?ref=ref&part=Inbox';

function browserFor(view, options = {}) {
  const requests = deferredRequests();
  let changes = 0;
  let stale = 0;
  const state = new NotebookState({
    request: requests.request,
    onChange: () => changes++,
    onStale: () => stale++,
    ...options
  });
  state.start(view);
  return { state, requests, changes: () => changes, stale: () => stale };
}

test('an Ori notebook lands on Inbox, listing only the root and Inbox', async () => {
  const { state, requests } = browserFor(notebook);
  assert.deepEqual(requests.paths(), [ROOT, INBOX]);
  assert.deepEqual(notebookListView(state), {
    title: 'Inbox',
    status: 'loading',
    message: 'Loading names…',
    notes: []
  });
  requests.answer(ROOT, listing([folder('Inbox'), folder('Projects'), file('Start here.md')]));
  requests.answer(INBOX, listing([file('20261125-0900-idea.md'), file('personal.md')]));
  await settle();
  const tree = notebookFolderTree(state);
  assert.equal(tree.name, 'My Notes');
  assert.deepEqual(
    tree.children.map(row => [row.name, row.selected, row.expanded, row.expandable]),
    [
      ['Inbox', true, true, false],
      ['Projects', false, false, true]
    ]
  );
  const list = notebookListView(state);
  assert.equal(list.status, 'ready');
  assert.deepEqual(
    list.notes.map(note => note.name),
    ['20261125-0900-idea.md', 'personal.md']
  );
  assert.equal(notebookReaderView(state).status, 'none');
  assert.equal(requests.calls.length, 2, 'nothing else is listed or read on arrival');
});

test('an external folder lands on its own root with one listing', async () => {
  const { state, requests } = browserFor(vault);
  assert.deepEqual(requests.paths(), ['/browse?ref=vault']);
  requests.answer('/browse?ref=vault', listing([folder('Expeditions'), file('Welcome.md')]));
  await settle();
  const list = notebookListView(state);
  assert.equal(list.title, 'Research & Notes');
  assert.deepEqual(
    list.notes.map(note => note.name),
    ['Welcome.md']
  );
  assert.deepEqual(
    notebookFolderTree(state).children.map(row => row.name),
    ['Expeditions']
  );
});

test('choosing or expanding a folder lists that folder only, never a recursive scan', async () => {
  const { state, requests } = browserFor(notebook);
  requests.answer(ROOT, listing([folder('Inbox'), folder('Projects')]));
  requests.answer(INBOX, listing([]));
  await settle();
  state.openFolder(['Projects']);
  assert.deepEqual(requests.paths().slice(2), ['/browse?ref=ref&part=Projects']);
  requests.answer('/browse?ref=ref&part=Projects', listing([folder('House'), folder('Garden')]));
  await settle();
  state.toggleFolder(['Projects', 'House']);
  assert.deepEqual(requests.paths().slice(3), ['/browse?ref=ref&part=Projects&part=House']);
  requests.answer('/browse?ref=ref&part=Projects&part=House', listing([file('README.md')]));
  await settle();
  // Collapsing and expanding again reuses the listing it has.
  state.toggleFolder(['Projects', 'House']);
  state.toggleFolder(['Projects', 'House']);
  assert.equal(requests.calls.length, 4);
  const projects = notebookFolderTree(state).children.find(row => row.name === 'Projects');
  assert.deepEqual(
    projects.children.map(row => [row.name, row.expanded, row.expandable]),
    [
      ['House', true, false],
      ['Garden', false, true]
    ]
  );
  assert.equal(notebookListView(state).title, 'Projects', 'expanding does not change the folder');
});

test('empty, folders-only and capped folders say what they hold', async () => {
  const { state, requests } = browserFor(vault);
  requests.answer('/browse?ref=vault', listing([]));
  await settle();
  assert.equal(notebookListView(state).message, 'No folders or Markdown notes here.');

  state.refresh([]);
  requests.answer('/browse?ref=vault', listing([folder('Expeditions')]));
  await settle();
  assert.equal(
    notebookListView(state).message,
    'No notes directly in Research & Notes. Choose one of its folders.'
  );

  state.refresh([]);
  requests.answer('/browse?ref=vault', listing([file('a.md')], true));
  await settle();
  const capped = notebookListView(state);
  assert.match(capped.message, /^Only some names could be listed/);
  assert.equal(capped.partial, true);
  assert.deepEqual(
    capped.notes.map(note => note.name),
    ['a.md']
  );
  assert.equal(notebookFolderTree(state).partial, true);
});

test('fast navigation: a late listing never replaces the folder chosen since', async () => {
  const { state, requests } = browserFor(notebook);
  requests.answer(ROOT, listing([folder('Inbox'), folder('Daily')]));
  await settle();
  state.openFolder(['Daily']);
  requests.answer('/browse?ref=ref&part=Daily', listing([file('2026-11-25.md')]));
  await settle();
  // Inbox's landing answer arrives last. It lists Inbox, not the chosen folder.
  requests.answer(INBOX, listing([file('late.md')]));
  await settle();
  assert.equal(notebookListView(state).title, 'Daily');
  assert.deepEqual(
    notebookListView(state).notes.map(note => note.name),
    ['2026-11-25.md']
  );
});

test('an older listing of the same folder cannot overwrite a newer one', async () => {
  const { state, requests } = browserFor(notebook);
  requests.answer(ROOT, listing([folder('Inbox')]));
  requests.answer(INBOX, listing([file('first.md')]));
  await settle();
  state.refresh(['Inbox']);
  state.refresh(['Inbox']);
  const [older, newer] = requests.calls.filter(call => call.path === INBOX && !call.done);
  newer.done = true;
  newer.resolve(listing([file('first.md'), file('second.md')]));
  await settle();
  older.done = true;
  older.resolve(listing([file('first.md')]));
  await settle();
  assert.deepEqual(
    notebookListView(state).notes.map(note => note.name),
    ['first.md', 'second.md']
  );
  assert.equal(older.signal.aborted, true);
});

test('disconnect or reconnect drops every listing and preview still in flight', async () => {
  const { state, requests, changes } = browserFor(notebook);
  requests.answer(ROOT, listing([folder('Inbox')]));
  requests.answer(INBOX, listing([file('a.md')]));
  await settle();
  void state.openNote('a.md');
  state.refresh(['Inbox']);
  const before = changes();
  const inFlight = requests.calls.filter(call => !call.done);
  assert.equal(inFlight.length, 2);
  state.clear();
  requests.answer('/preview/inbox/a.md?ref=ref', { preview: { title: 'A', body: 'Private' } });
  requests.answer(INBOX, listing([file('a.md'), file('b.md')]));
  await settle();
  assert.equal(changes(), before, 'nothing repaints after the connection is gone');
  assert.equal(state.view, null);
  assert.equal(notebookListView(state), null);
  assert.equal(notebookReaderView(state), null);
  assert.ok(inFlight.every(call => call.signal.aborted));

  // Reconnecting opens a new generation; the old connection's answers stay out.
  state.start({ ...notebook, browse_ref: 'new' });
  state.start({ ...notebook, browse_ref: 'newer' });
  requests.answer('/browse?ref=new&part=Inbox', listing([file('old-generation.md')]));
  requests.answer('/browse?ref=newer&part=Inbox', listing([file('current.md')]));
  await settle();
  assert.deepEqual(
    notebookListView(state).notes.map(note => note.name),
    ['current.md']
  );
});

test('the reader shows only the note chosen last', async () => {
  const { state, requests } = browserFor(notebook);
  requests.answer(ROOT, listing([folder('Inbox'), folder('Daily')]));
  requests.answer(INBOX, listing([file('a.md'), file('b.md')]));
  await settle();
  void state.openNote('a.md');
  assert.equal(notebookReaderView(state).status, 'loading');
  void state.openNote('b.md');
  requests.answer('/preview/inbox/b.md?ref=ref', {
    preview: { title: 'B note', body: 'First line\n\nSecond paragraph', truncated: true }
  });
  await settle();
  requests.answer('/preview/inbox/a.md?ref=ref', { preview: { title: 'A note', body: 'Late' } });
  await settle();
  const reader = notebookReaderView(state);
  assert.equal(reader.title, 'B note');
  assert.deepEqual(reader.paragraphs, ['First line', 'Second paragraph']);
  assert.equal(reader.truncated, true);
  assert.equal(reader.place, 'Inbox / b.md');
  assert.deepEqual(
    notebookListView(state)
      .notes.filter(note => note.selected)
      .map(note => note.name),
    ['b.md']
  );

  // A note whose first line is its title heading shows the title once.
  void state.openNote('a.md');
  requests.answer('/preview/inbox/a.md?ref=ref', {
    preview: { title: 'House', body: '# House\n\nRepaint the hallway.\n\n# House again' }
  });
  await settle();
  assert.deepEqual(notebookReaderView(state).paragraphs, ['Repaint the hallway.', '# House again']);

  // Leaving the folder closes the note; its late preview does not reopen it.
  void state.openNote('a.md');
  state.openFolder(['Daily']);
  requests.answer('/preview/inbox/a.md?ref=ref', { preview: { title: 'A note', body: 'Late' } });
  await settle();
  assert.equal(notebookReaderView(state).status, 'none');
});

test('a missing note says so and lists its folder again', async () => {
  const { state, requests } = browserFor(notebook);
  requests.answer(ROOT, listing([folder('Inbox')]));
  requests.answer(INBOX, listing([file('gone.md')]));
  await settle();
  void state.openNote('gone.md');
  requests.answer('/preview/inbox/gone.md?ref=ref', httpError(404, 'That note is no longer there'));
  await settle();
  const reader = notebookReaderView(state);
  assert.equal(reader.status, 'missing');
  assert.equal(reader.message, 'This note may have been moved or removed.');
  assert.equal(requests.paths().at(-1), INBOX);
  requests.answer(INBOX, listing([]));
  await settle();
  assert.equal(notebookListView(state).notes.length, 0);
  assert.equal(notebookReaderView(state).status, 'missing');
});

test('notes without an inline preview get a truthful way out, without a request', async () => {
  const { state, requests } = browserFor(notebook);
  requests.answer(ROOT, listing([folder('Inbox'), folder('Projects')]));
  requests.answer(INBOX, listing([file('personal.md')]));
  await settle();
  void state.openNote('personal.md');
  requests.answer(
    '/preview/inbox/personal.md?ref=ref',
    httpError(403, 'That note is yours; open it in your editor.')
  );
  await settle();
  assert.equal(notebookReaderView(state).message, 'That note is yours; open it in your editor.');
  assert.equal(notebookReaderView(state).external, '', 'a plain folder has no Obsidian link');

  state.openFolder(['Projects', 'Research']);
  const sent = requests.calls.length;
  void state.openNote('scratch.md');
  const reader = notebookReaderView(state);
  assert.equal(reader.status, 'unsupported');
  assert.match(reader.message, /notes it wrote in Inbox, Daily/);
  assert.equal(requests.calls.length, sent, 'an unsupported note is never fetched');

  const external = browserFor(vault);
  external.requests.answer('/browse?ref=vault', listing([file('Canyon.md')]));
  await settle();
  void external.state.openNote('Canyon.md');
  const vaultReader = notebookReaderView(external.state);
  assert.match(vaultReader.message, /connected folder/);
  assert.equal(vaultReader.external, 'obsidian://open?vault=Research%20%26%20Notes&file=Canyon');
  assert.equal(external.requests.calls.length, 1);
});

test('a missing Inbox lands on the real root; a moved folder is an error, not a fallback', async () => {
  const { state, requests } = browserFor(notebook);
  requests.answer(INBOX, httpError(404));
  await settle();
  assert.deepEqual(state.folder, []);
  assert.deepEqual(requests.paths(), [ROOT, INBOX], 'the root listing in flight is reused');
  requests.answer(ROOT, listing([folder('Projects')]));
  await settle();
  assert.equal(notebookListView(state).title, 'My Notes');

  state.openFolder(['Projects']);
  requests.answer('/browse?ref=ref&part=Projects', httpError(404));
  await settle();
  const list = notebookListView(state);
  assert.equal(list.status, 'error');
  assert.match(list.message, /moved or removed/);
  assert.deepEqual(state.folder, ['Projects']);
});

test('a changed connection or unreachable root asks for the connection again, once', async () => {
  const { state, requests, stale } = browserFor(notebook);
  requests.answer(ROOT, httpError(404));
  requests.answer(INBOX, httpError(409));
  await settle();
  assert.equal(stale(), 1);
  assert.match(notebookListView(state).message, /connection changed/);
  state.refresh(['Inbox']);
  requests.answer(INBOX, httpError(409));
  await settle();
  assert.equal(stale(), 1);
});

test('a failed listing keeps no earlier names', async () => {
  const { state, requests } = browserFor(vault);
  requests.answer('/browse?ref=vault', listing([file('secret.md')]));
  await settle();
  state.refresh([]);
  requests.answer('/browse?ref=vault', httpError(403));
  await settle();
  const list = notebookListView(state);
  assert.equal(list.status, 'error');
  assert.deepEqual(list.notes, []);
  assert.match(list.message, /cannot list this folder/);
});
