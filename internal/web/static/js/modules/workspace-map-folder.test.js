import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import {
  buildMapMetadata,
  flattenWorkspaceTree,
  folderIndependentMapKey
} from './workspace-map-snapshot.js';

function mapAPI() {
  const window = {};
  vm.runInNewContext(readFileSync(new URL('./workspace-map.js', import.meta.url), 'utf8'), {
    window,
    document: {}
  });
  return window.OriWorkspaceMap;
}

test('only explicit anchored/imported workspace buildings get the decorative folder', () => {
  const map = mapAPI();
  for (const [row, expected] of [
    [{ shape: 'managed' }, false],
    [{ shape: 'anchored' }, true],
    [{ shape: 'imported' }, true],
    [{ shape: 'future' }, false],
    [{}, false],
    [
      {
        shared_data: { primary_directory_id: 'external' },
        directory_references: [{ id: 'external', path: '/external', is_primary: true }]
      },
      false
    ],
    [
      {
        shape: 'managed',
        directory_references: [{ path: '/external', is_primary: true }, { path: '/own' }]
      },
      false
    ],
    [
      {
        shape: 'managed',
        directory_references: [{ path: '/own' }, { path: '/external', is_primary: true }]
      },
      false
    ],
    [{ shape: 'anchored', kind: 'group' }, false],
    [{ shape: 'anchored', kind: 'agent' }, false],
    [{ shape: 'imported', kind: 'empty' }, false]
  ]) {
    const ws = { id: 'w', name: 'A <long> name', ...row };
    assert.equal(map.usesExistingWorkFolder(ws), expected);
    const html = map.tileHTML({ ws }, 'w');
    assert.equal(html.includes('<svg class="ws-map-work-folder"'), expected);
    assert.match(html, /A &lt;long&gt; name/);
    for (const activity of [null, { working: true, agents: ['Mira'] }, { working: false }]) {
      const label = map.activity.tileAccessibleName(ws, true, activity);
      assert.equal(label.split('Uses an existing work folder').length - 1, expected ? 1 : 0);
    }
  }
  assert.equal(map.usesExistingWorkFolder({ shape: 'anchored' }), false);
});

test('both hosts keep explicit shapes in nested snapshots without detail requests', () => {
  const rows = flattenWorkspaceTree([
    {
      id: 'g',
      kind: 'group',
      children: [
        { id: 'a', shape: 'managed' },
        { id: 'b', shape: 'anchored' },
        {
          id: 'c',
          shape: 'managed',
          directory_references: [{ path: '/reference', is_primary: true }]
        },
        { id: 'd', shape: 'imported' }
      ]
    }
  ]);
  const map = mapAPI(); // No fetch exists in this sandbox: render must be synchronous.
  buildMapMetadata(rows, []);
  assert.deepEqual(
    rows.filter(row => map.usesExistingWorkFolder(row)).map(row => row.id),
    ['b', 'd']
  );
  rows.forEach(ws => map.tileHTML({ ws }));
  const changed = rows.map(row => ({
    ...row,
    shape: 'imported',
    updated_at: 'later',
    directory_references: []
  }));
  assert.equal(folderIndependentMapKey(rows), folderIndependentMapKey(changed));
  assert.notEqual(
    folderIndependentMapKey(rows),
    folderIndependentMapKey([...changed, { id: 'new' }])
  );
  assert.notEqual(
    folderIndependentMapKey(rows),
    folderIndependentMapKey(rows.map(row => ({ ...row, name: 'changed' })))
  );
});
