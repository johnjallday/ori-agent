import test from 'node:test';
import assert from 'node:assert/strict';
import { previewHTML, readPreview, WorkspaceFilesView } from './workspace-files-view.js';
import { filesToRows, FILE_PREVIEW_LIMIT, loadSection } from './home-tree-sources.js';

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
    ...Array.from({ length: 110 }, (_, i) => ({ relative_path: `Samples/Take ${i}.txt` }))
  ];
  const listing = await loadSection('album', 'files', {
    limit: Infinity,
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
