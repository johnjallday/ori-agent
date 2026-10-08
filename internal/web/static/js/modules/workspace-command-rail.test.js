import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  railSectionStorageKey,
  readRailSection,
  resolveRailSection,
  writeRailSection
} from './workspace-command-rail.js';

// A plain workspace's rail, in order, with every section empty.
function sections(counts = {}, extra = []) {
  const keys = ['backlog', 'notes', 'schedules', 'sessions', 'folders', 'files', 'systems'];
  const rows = keys.map(key => ({
    key,
    count: counts[key] ?? (key === 'systems' ? 3 : 0)
  }));
  for (const row of extra) {
    const at = row.after ? rows.findIndex(existing => existing.key === row.after) + 1 : rows.length;
    rows.splice(at, 0, { key: row.key, count: row.count });
  }
  return rows;
}

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    values,
    getItem: key => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value))
  };
}

test('a saved section wins over the default', () => {
  const rows = sections({ notes: 4, folders: 2 });
  assert.equal(resolveRailSection({ saved: 'files', sections: rows }), 'files');
  // Even one with no content, and even Systems: the user chose it.
  assert.equal(resolveRailSection({ saved: 'schedules', sections: rows }), 'schedules');
  assert.equal(resolveRailSection({ saved: 'systems', sections: rows }), 'systems');
});

test('a saved "closed" is a choice and stays closed', () => {
  assert.equal(resolveRailSection({ saved: '', sections: sections({ notes: 4 }), isHQ: true }), '');
});

test('a saved section this workspace does not have is ignored', () => {
  const rows = sections({ sessions: 1 });
  // Detachment remembered on a workspace that is not a group.
  assert.equal(resolveRailSection({ saved: 'members', sections: rows }), 'sessions');
  // Stations remembered on a workspace with no stations.
  assert.equal(resolveRailSection({ saved: 'stations', sections: rows }), 'sessions');
  assert.equal(resolveRailSection({ saved: 'no-such-section', sections: rows }), 'sessions');
});

test('with nothing saved, the first content section with items opens, in rail order', () => {
  assert.equal(resolveRailSection({ sections: sections({ folders: 2, files: 9 }) }), 'folders');
  assert.equal(resolveRailSection({ sections: sections({ backlog: 1, notes: 5 }) }), 'backlog');
  assert.equal(resolveRailSection({ saved: null, sections: sections({ files: 1 }) }), 'files');
  // Detachment is content on a group, in its place between folders and files.
  const group = sections({ files: 3 }, [{ key: 'members', count: 2, after: 'folders' }]);
  assert.equal(resolveRailSection({ sections: group }), 'members');
});

test('Systems is never the default, whatever its count', () => {
  assert.equal(resolveRailSection({ sections: sections({ systems: 9 }) }), '');
  assert.equal(
    resolveRailSection({ sections: sections({ systems: 9 }), isHQ: true }),
    '',
    'not on an HQ without a Stations row either'
  );
});

test('an HQ with no content opens Stations; one with content opens the content', () => {
  const emptyHQ = sections({}, [{ key: 'stations', count: 4 }]);
  assert.equal(resolveRailSection({ sections: emptyHQ, isHQ: true }), 'stations');
  const busyHQ = sections({ notes: 1 }, [{ key: 'stations', count: 4 }]);
  assert.equal(resolveRailSection({ sections: busyHQ, isHQ: true }), 'notes');
  // A workspace that merely has stations is not the HQ: nothing opens.
  assert.equal(resolveRailSection({ sections: emptyHQ, isHQ: false }), '');
});

test('nothing saved and nothing to show opens nothing', () => {
  assert.equal(resolveRailSection({ sections: sections() }), '');
  assert.equal(resolveRailSection({ sections: [] }), '');
  assert.equal(resolveRailSection(), '');
});

test('the preference is stored per workspace', () => {
  assert.equal(railSectionStorageKey('ws-1'), 'ori:command-rail-section:ws-1');
  const storage = memoryStorage();
  writeRailSection(storage, 'ws-1', 'notes');
  writeRailSection(storage, 'ws-2', '');
  assert.equal(readRailSection(storage, 'ws-1'), 'notes');
  assert.equal(readRailSection(storage, 'ws-2'), '', 'closed is remembered as closed');
  assert.equal(readRailSection(storage, 'ws-3'), null, 'never chosen is not the same as closed');
  assert.deepEqual(
    [...storage.values.keys()],
    ['ori:command-rail-section:ws-1', 'ori:command-rail-section:ws-2']
  );
});

test('reading and writing are no-ops without storage or a workspace id', () => {
  assert.equal(readRailSection(null, 'ws-1'), null);
  assert.equal(readRailSection(undefined, 'ws-1'), null);
  assert.doesNotThrow(() => writeRailSection(null, 'ws-1', 'notes'));

  const storage = memoryStorage();
  writeRailSection(storage, '', 'notes');
  assert.equal(storage.values.size, 0, 'no key is written for an unknown workspace');
  assert.equal(readRailSection(storage, ''), null);
});

test('a storage that throws is swallowed', () => {
  const throwing = {
    getItem() {
      throw new Error('storage is blocked');
    },
    setItem() {
      throw new Error('quota exceeded');
    }
  };
  assert.equal(readRailSection(throwing, 'ws-1'), null);
  assert.doesNotThrow(() => writeRailSection(throwing, 'ws-1', 'notes'));
});
