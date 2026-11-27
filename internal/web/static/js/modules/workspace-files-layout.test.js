import assert from 'node:assert/strict';
import test from 'node:test';
import { diskLayout, placedEntries } from './workspace-files-layout.js';
import { loadFilesLayout } from './home-tree-sources.js';

test('only verified members are promoted; nested link contents keep their own owner and the outer action stays intact', async () => {
  const { folders } = await loadFilesLayout('group', {
    fetchImpl: async () =>
      Response.json({
        folders: [
          { workspace_id: 'shelf', id: 'a', name: 'Music' },
          {
            workspace_id: 'song',
            id: 'b',
            name: 'Project',
            inside: { workspace_id: 'shelf', id: 'a', path: 'Demos/Track' }
          }
        ]
      })
  });
  const layout = diskLayout(folders, () => true),
    [root] = layout.roots;
  const demos = { kind: 'linkedFolder', workspaceId: 'shelf', meta: { dirId: 'a', path: 'Demos' } };
  assert.deepEqual(placedEntries(root, [demos], layout), [demos]);
  const track = { ...demos, meta: { dirId: 'a', path: 'Demos/Track', moveSelection: 'existing' } };
  const [nested] = placedEntries(demos, [track], layout);
  assert.equal(nested.contentsSource.id, 'song/l/b');
  assert.equal(nested.meta, track.meta);
  assert.deepEqual(placedEntries(demos, [], layout), [nested.contentsSource]);
  assert.deepEqual(
    placedEntries(
      demos,
      [track],
      diskLayout(folders, id => id === 'shelf')
    ),
    [track]
  );
});
