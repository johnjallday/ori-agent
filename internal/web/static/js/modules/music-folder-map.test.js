import test from 'node:test';
import assert from 'node:assert/strict';
import { folderMapTree } from './music-folder-map.js';

test('interleaved saved tiles remain unique at their actual parent, without layout writes', () => {
  const members = [{ id: 'home' }, { id: 'fed', name: 'Demo', parent_id: 'home' }];
  const catalog = [];
  for (let i = 0; i < 7; i++) {
    members.push({ id: `ws-${i}`, name: 'Song', parent_id: 'fed', left: i % 2, top: i * 3 });
    const name = i === 1 || i === 3 ? 'Picker Songs' : 'Albums';
    catalog.push({
      workspace_id: `ws-${i}`,
      folder_section: { id: name, label: name },
      connection: 'connected'
    });
  }
  catalog.push({
    name: 'Logic Sketch',
    connection: 'catalog_only',
    folder_section: { id: 'Albums', label: 'Albums' }
  });
  const snapshot = JSON.stringify(members);
  const tree = folderMapTree('home', members, catalog);
  assert.deepEqual(
    tree.children[0].sections.map(s => [s.label, s.tiles.length]),
    [
      ['Albums', 5],
      ['Picker Songs', 2]
    ]
  );
  assert.equal(new Set(tree.children[0].sections.flatMap(s => s.tiles.map(t => t.id))).size, 7);
  assert.equal(JSON.stringify(members), snapshot);
  members[2].parent_id = 'home';
  const moved = folderMapTree('home', members, catalog);
  assert.equal(moved.sections[0].tiles[0].id, 'ws-0');
  assert.equal(moved.children[0].sections.flatMap(s => s.tiles).length, 6);
});
