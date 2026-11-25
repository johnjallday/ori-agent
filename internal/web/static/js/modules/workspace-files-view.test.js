import test from 'node:test';
import assert from 'node:assert/strict';
import { previewHTML, readPreview, WorkspaceFilesView } from './workspace-files-view.js';
import { filesToRows, FILE_PREVIEW_LIMIT, loadWorkspaceContents } from './home-tree-sources.js';

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
