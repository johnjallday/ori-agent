import test from 'node:test';
import assert from 'node:assert/strict';
import {
  folderResponseFixtures,
  onlySetupCandidate,
  syntheticFolderFiles,
  folderContentSentinel
} from './assistant-folder-response.js';

test('synthetic folder response matrix stays within the existing typed snapshot bounds', () => {
  assert.equal(Object.keys(folderResponseFixtures).length, 8);
  for (const observation of Object.values(folderResponseFixtures)) {
    assert.equal(observation.version, 1);
    assert.ok(Number.isFinite(Date.parse(observation.scanned_at)));
    assert.ok(observation.projects.length <= 8);
    assert.ok(observation.kinds.length <= 8);
    for (const name of [observation.folder, ...observation.projects.map(row => row.name)]) {
      assert.ok(Array.from(name).length <= 96);
      assert.ok(!name.includes('/') && !name.includes('\\'));
      assert.ok(Array.from(name).every(character => character.codePointAt(0) >= 32));
    }
    assert.equal(observation.coverage.max_depth, 3);
    assert.equal(observation.coverage.max_entries, 5000);
    assert.equal(observation.coverage.budget_seconds, 3);
  }
});

test('fixtures separate discussable observations from setup options and overlapping root counts', () => {
  const mixed = folderResponseFixtures.oneSetupOption;
  assert.ok(mixed.projects.some(row => row.id === onlySetupCandidate));
  assert.equal(mixed.projects.filter(row => !row.root).length, 3);
  assert.equal(folderResponseFixtures.album.projects[0].files, folderResponseFixtures.album.files);
  assert.equal(folderResponseFixtures.empty.files, 0);
  assert.equal(folderResponseFixtures.rootOnly.projects[0].root, true);
  assert.equal(folderResponseFixtures.partial.coverage.projects_omitted, 4);
  assert.equal(
    folderResponseFixtures.hostile.projects[0].name,
    folderResponseFixtures.hostile.projects[1].name
  );
});

test('disk fixture files are synthetic relative paths with content-only watched sentinels', () => {
  for (const [path, content] of syntheticFolderFiles) {
    assert.ok(!path.startsWith('/'));
    assert.ok(!path.split('/').includes('..'));
    assert.equal(content, folderContentSentinel);
    assert.ok(!path.includes(folderContentSentinel));
  }
  assert.ok(!JSON.stringify(folderResponseFixtures).includes(folderContentSentinel));
});
