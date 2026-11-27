import test from 'node:test';
import assert from 'node:assert/strict';
import { previewHTML, readPreview, WorkspaceFilesView } from './workspace-files-view.js';
import {
  filesToRows,
  FILE_PREVIEW_LIMIT,
  loadWorkspaceContents,
  loadLinkedPage
} from './home-tree-sources.js';

const text = body => async () => new Response(body);
const note = {
  id: 'album/n/1',
  workspaceId: 'album',
  kind: 'note',
  label: 'Session',
  meta: { noteId: '1' }
};
const file = (path, size = 10) =>
  filesToRows('album', { files: [{ relative_path: path, size }] }).rows[0];

test('the full workspace listing keeps nested, empty and beyond-cap entries in place', async () => {
  const files = [
    { relative_path: 'Samples/Empty', is_dir: true },
    { relative_path: 'Samples/Z-last.txt', status: 'missing' },
    ...Array.from({ length: 110 }, (_, i) => ({ relative_path: `Samples/Take ${i}.txt` }))
  ];
  const listing = await loadWorkspaceContents('album', 'files', {
    fetchImpl: text(JSON.stringify({ files }))
  });
  const rows = listing.rows[0].children;
  assert.deepEqual(rows[0].children, []);
  assert.equal(rows.at(-1).id, 'album/f/Samples/Take 109.txt');
  assert.equal(listing.rows.length, 1);
});

test('sources and same-named nested files stay qualified and load one folder at a time', async () => {
  const urls = [];
  const fetchImpl = async url => {
    urls.push(url);
    const payload = url.includes('linked_sources=1')
      ? {
          linked_sources: [{ id: 'ref', name: 'Project' }],
          files: [{ relative_path: 'Notes.txt' }]
        }
      : url.includes('?path=')
        ? {
            entries: [{ kind: 'file', path: 'Sessions/Song.RPP', name: 'Song.RPP' }],
            partial: true
          }
        : { entries: [{ kind: 'folder', path: 'Sessions', name: 'Sessions' }] };
    return new Response(JSON.stringify(payload));
  };
  for (const owner of ['track-1', 'track-2']) {
    const [stored, root] = (await loadWorkspaceContents(owner, 'files', { fetchImpl })).rows;
    assert.equal(stored.label, 'Stored in Ori');
    assert.equal(urls.at(-1), `/api/workspaces/${owner}/files/tree?linked_sources=1`);
    const [folder] = (await loadLinkedPage(root, { fetchImpl })).rows;
    const [song, partial] = (await loadLinkedPage(folder, { fetchImpl })).rows;
    assert.equal(song.id, `${owner}/lf/ref/Sessions/Song.RPP`);
    assert.match(partial.label, /Partial listing/);
    assert.equal((await readPreview(song, { fetchImpl })).kind, 'none');
    assert.deepEqual(urls.splice(0), [
      `/api/workspaces/${owner}/files/tree?linked_sources=1`,
      `/api/workspaces/${owner}/directories/ref/entries`,
      `/api/workspaces/${owner}/directories/ref/entries?path=Sessions`
    ]);
  }
});

test('an invalidated linked-folder listing cannot repopulate the reader', async () => {
  const view = new WorkspaceFilesView({}, { id: 'track' });
  view.controller = new AbortController();
  view.paintTree = view.syncExpanded = () => {};
  let finish;
  view.scopedFetch = () => () =>
    new Promise(resolve => {
      finish = resolve;
    });
  const pending = view.load({
    id: 'track/l/ref',
    workspaceId: 'track',
    kind: 'linked',
    meta: { dirId: 'ref' }
  });
  view.refreshLinks('track');
  finish(new Response(JSON.stringify({ entries: [] })));
  await pending;
  assert.equal(view.sections.size, 0);
});

test('the reader uses only qualified reads, rejects moved notes and shows failed reads', async () => {
  const urls = [];
  const fetchImpl = async (url, init) => {
    urls.push(url);
    assert.equal(init?.method, undefined);
    return new Response(
      url.includes('/notes/')
        ? JSON.stringify({ id: '1', workspace_id: 'other', content: 'Not here' })
        : 'Owned'
    );
  };
  assert.equal((await readPreview(file('Mix notes.md'), { fetchImpl })).text, 'Owned');
  await assert.rejects(readPreview(note, { fetchImpl }), /no longer/);
  assert.deepEqual(urls, ['/api/workspaces/album/files/Mix%20notes.md', '/api/notes/1']);
  await assert.rejects(
    readPreview(note, { fetchImpl: async () => new Response(null, { status: 404 }) }),
    /404/
  );
});

test('unsupported and oversized files never fetch; document text is inert', async () => {
  const fetchImpl = () => assert.fail('unexpected read');
  for (const [row, expected] of [
    [file('Mix v03.wav'), /No preview available/],
    [file('mix.txt', FILE_PREVIEW_LIMIT + 1), /too large/]
  ])
    assert.match(previewHTML(await readPreview(row, { fetchImpl }), row.label), expected);
  for (const kind of ['text', 'markdown'])
    assert.doesNotMatch(previewHTML({ kind, text: '<form>hello</form>' }, 'Form'), /<form>/);
});

test('only the explicit open sends the current owner, ref and selected document, once while pending', async () => {
  const view = new WorkspaceFilesView({}, { id: 'group' });
  view.members.set('album', { id: 'album' });
  const song = {
    id: 'album/lf/ref/Song.rpp',
    kind: 'linkedFile',
    workspaceId: 'album',
    label: 'Song.rpp',
    meta: { dirId: 'ref', path: 'Song.rpp', selection: 'primary' }
  };
  const alt = {
    ...song,
    id: 'album/lf/ref/Alternate.rpp',
    meta: { ...song.meta, path: 'Alternate.rpp', selection: 'alternate' }
  };
  view.rows = [song, alt];
  view.controller = new AbortController();
  view.paintTree = view.paintPreview = () => {};
  let finish;
  const calls = [];
  view.scopedFetch = () => (url, init) => {
    calls.push([url, init.method, JSON.parse(init.body)]);
    return new Promise(r => {
      finish = r;
    });
  };
  await view.select(song);
  await view.select(alt, { restore: true });
  assert.deepEqual(calls, []);
  const pending = view.openSelected();
  await view.openSelected();
  assert.deepEqual(calls, [
    [
      '/api/workspaces/album/directories/ref/open',
      'POST',
      { path: 'Alternate.rpp', selection: 'alternate' }
    ]
  ]);
  await view.select(song);
  finish(new Response('', { status: 409 }));
  await pending;
  assert.equal(view.openResult, null);
  view.members.clear();
  await view.openSelected();
  assert.equal(calls.length, 1);
});

test('a failed optional Home marks read leaves the directory listing and its selections alone', async () => {
  const view = new WorkspaceFilesView({}, { id: 'home' });
  view.controller = new AbortController();
  view.home = true;
  view.layout = { folders: [{ id: 'ref' }] };
  view.marks = { can_set_up: true };
  const state = { rows: [{ id: 'song' }] };
  view.sections.set('folder', state);
  view.paintTree = view.paintPreview = view.syncExpanded = () => {};
  view.scopedFetch = () => async () => new Response(null, { status: 503 });
  await view.readMarks();
  assert.equal(view.marks, undefined);
  assert.equal(view.sections.get('folder'), state);
});

test('restoring keyboard focus alone does not read the focused note', () => {
  const view = new WorkspaceFilesView({}, { id: 'album' });
  view.tree = {};
  view.rows = [note];
  view.saved = { key: note.id, ancestors: [], hadPreview: false, scroll: 20 };
  view.paintTree = () => {};
  view.select = () => assert.fail('focus cannot open a preview');
  view.restoreSelection();
  assert.equal(view.focusKey, note.id);
  assert.equal(view.branchSelection, null);
  assert.equal(view.tree.scrollTop, 20);
});

test('membership loss or failure revokes even a pending member preview', async () => {
  for (const snapshot of [null, { id: 'home', kind: 'group', children: [] }]) {
    const root = { id: 'home', kind: 'group' };
    const view = new WorkspaceFilesView({}, root);
    view.controller = new AbortController();
    view.membershipStatus = 'loading';
    view.paintTree = view.paintPreview = () => {};
    view.membershipRead('home', { ...root, children: [{ id: 'album', name: 'Album' }] });
    view.sections.set('album/s/notes', { rows: [note] });
    let finish;
    view.scopedFetch = () => () =>
      new Promise(r => {
        finish = r;
      });
    const pending = view.select(note);
    view.membershipRead('home', snapshot);
    finish(new Response(JSON.stringify({ id: '1', workspace_id: 'album', content: 'Late' })));
    await pending;
    assert.equal(view.sections.size, 0);
    assert.equal(view.selected, null);
    assert.equal(view.value, null);
    assert.equal(view.membershipStatus, snapshot ? 'ready' : 'failed');
  }
});

test('an earlier selection or unmounted note cannot repaint the active preview', async () => {
  const view = new WorkspaceFilesView({ replaceChildren() {} }, { id: 'album' });
  view.paintTree = view.paintPreview = () => {};
  view.controller = new AbortController();
  let finish;
  view.scopedFetch = () => () =>
    new Promise(r => {
      finish = r;
    });
  const first = view.select(note);
  await view.select(file('Mix.wav'));
  finish(new Response(JSON.stringify({ id: '1', workspace_id: 'album', content: 'Late' })));
  await first;
  assert.equal(view.value.kind, 'none');
  const pending = view.select(note);
  view.leave();
  finish(new Response(JSON.stringify({ id: '1', workspace_id: 'album', content: 'Later' })));
  await pending;
  assert.equal(view.value, null);
});
