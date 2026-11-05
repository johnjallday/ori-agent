import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ProjectLibraryPanel,
  BULK_UNDO_MS,
  bulkCountText,
  bulkResultText,
  bulkSkippedText
} from './project-library.js';

// Library upkeep FR16-FR21: Set stage / status / priority for selected songs.

function bulkDom() {
  const elements = new Map();
  for (const id of [
    'projectLibraryBulk',
    'projectLibraryBulkCount',
    'projectLibraryBulkStage',
    'projectLibraryBulkStatus',
    'projectLibraryBulkPriority',
    'projectLibraryBulkResult',
    'projectLibraryBulkResultText',
    'projectLibraryBulkUndo'
  ])
    elements.set(id, { id, hidden: true, disabled: false, textContent: '', value: '' });
  return { elements, document: { getElementById: id => elements.get(id) || null } };
}

function song(id, extra = {}) {
  return {
    id,
    name: `Song ${id}`,
    stage: 'unknown',
    status: 'unknown',
    connection: 'catalog_only',
    fields_revision: 0,
    ...extra
  };
}

function bulkPanel(rows) {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.rows = rows;
  for (const row of rows) {
    panel.selectedProjects.add(row.id);
    panel.selectedRows.set(row.id, row);
  }
  const log = { posts: [], statuses: [], renders: 0 };
  panel.status = message => log.statuses.push(message);
  panel.renderRows = () => log.renders++;
  return { panel, log };
}

function withTimers(fn) {
  const originalSet = globalThis.setTimeout;
  const originalClear = globalThis.clearTimeout;
  const timers = [];
  globalThis.setTimeout = (callback, ms) => {
    timers.push({ callback, ms });
    return timers.length;
  };
  globalThis.clearTimeout = () => {};
  return Promise.resolve(fn(timers)).finally(() => {
    globalThis.setTimeout = originalSet;
    globalThis.clearTimeout = originalClear;
  });
}

test('bulk result text names the value, the count and why songs were skipped', () => {
  assert.equal(bulkResultText('stage', 'mixing', 14, []), 'Set stage to Mixing for 14 songs');
  assert.equal(
    bulkResultText('status', 'active', 13, [{ entry_id: 'a', reason: 'revision_changed' }]),
    'Set status to Active for 13 songs · 1 skipped (changed elsewhere)'
  );
  assert.equal(bulkResultText('status', 'on_hold', 1, []), 'Set status to On hold for 1 song');
  assert.equal(bulkResultText('priority', '2', 3, []), 'Set priority to 2 for 3 songs');
  assert.equal(bulkResultText('priority', 'clear', 2, []), 'Cleared priority for 2 songs');
  assert.equal(
    bulkSkippedText([
      { reason: 'revision_changed' },
      { reason: 'not_found' },
      { reason: 'revision_changed' }
    ]),
    ' · 3 skipped (2 changed elsewhere, 1 no longer in the library)'
  );
  assert.equal(bulkSkippedText(null), '');
  assert.equal(bulkCountText(-1), '0 songs');
  assert.equal(/ready/i.test(bulkResultText('stage', 'mastering', 2, [])), false);
});

test('bulk controls appear from one selected song and never on a read-only Home', () => {
  const original = globalThis.document;
  const dom = bulkDom();
  globalThis.document = dom.document;
  try {
    const { panel } = bulkPanel([song('a')]);
    panel.renderBulkControls();
    assert.equal(dom.elements.get('projectLibraryBulk').hidden, false);
    assert.equal(dom.elements.get('projectLibraryBulkCount').textContent, '1 song');
    assert.equal(dom.elements.get('projectLibraryBulkStage').disabled, false);
    panel.clearSelection();
    panel.renderBulkControls();
    assert.equal(dom.elements.get('projectLibraryBulk').hidden, true);
    const readOnly = bulkPanel([song('b')]).panel;
    readOnly.state = { provider_read_only: true };
    readOnly.renderBulkControls();
    assert.equal(dom.elements.get('projectLibraryBulk').hidden, true);
  } finally {
    globalThis.document = original;
  }
});

test('one bulk request updates every selected row in place, clears the selection and offers Undo', async () => {
  const original = globalThis.document;
  const dom = bulkDom();
  globalThis.document = dom.document;
  try {
    await withTimers(async timers => {
      const rows = [
        song('a', { status: 'planning', fields_revision: 3 }),
        song('b', { fields_revision: 1 }),
        song('c', { status: 'on_hold', fields_revision: 7, connection: 'connected' })
      ];
      const { panel, log } = bulkPanel(rows);
      panel.post = async (path, body) => {
        log.posts.push({ path, body: JSON.parse(JSON.stringify(body)) });
        return {
          updated: [
            { entry_id: 'a', fields: { status: 'active', revision: 4 } },
            { entry_id: 'b', fields: { status: 'active', revision: 2 } }
          ],
          skipped: [{ entry_id: 'c', reason: 'revision_changed' }],
          replay: false
        };
      };
      await panel.applyBulk('status', 'active', null);
      assert.equal(log.posts.length, 1, 'one request for every selected song');
      assert.equal(log.posts[0].path, '/projects/fields/bulk');
      assert.deepEqual(log.posts[0].body.entries, [
        { entry_id: 'a', if_fields_revision: 3 },
        { entry_id: 'b', if_fields_revision: 1 },
        { entry_id: 'c', if_fields_revision: 7 }
      ]);
      assert.deepEqual(log.posts[0].body.patch, { status: 'active' });
      assert.match(log.posts[0].body.idempotency_key, /^library-bulk-fields-/);
      assert.equal(rows[0].status, 'active');
      assert.equal(rows[0].fields_revision, 4);
      assert.equal(rows[2].status, 'on_hold', 'a skipped song keeps what the shelf showed');
      assert.equal(panel.selectedProjects.size, 0);
      assert.equal(log.renders > 0, true, 'rows re-render from memory, no reload');
      assert.equal(
        dom.elements.get('projectLibraryBulkResultText').textContent,
        'Set status to Active for 2 songs · 1 skipped (changed elsewhere)'
      );
      assert.equal(dom.elements.get('projectLibraryBulkResult').hidden, false);
      assert.equal(dom.elements.get('projectLibraryBulkUndo').hidden, false);
      assert.equal(timers.at(-1).ms, BULK_UNDO_MS);
      // After thirty seconds Undo is gone and does nothing.
      timers.at(-1).callback();
      assert.equal(dom.elements.get('projectLibraryBulkUndo').hidden, true);
      await panel.undoBulk(null);
      assert.equal(log.posts.length, 1);
    });
  } finally {
    globalThis.document = original;
  }
});

test('Undo sends each previous value with the fresh revision and reports skipped songs', async () => {
  const original = globalThis.document;
  const dom = bulkDom();
  globalThis.document = dom.document;
  try {
    await withTimers(async () => {
      const rows = [
        song('a', { priority: 1, fields_revision: 1 }),
        song('b', { priority: 1, fields_revision: 5 }),
        song('c', { fields_revision: 2 })
      ];
      const { panel, log } = bulkPanel(rows);
      panel.post = async (path, body) => {
        log.posts.push(JSON.parse(JSON.stringify(body)));
        if (log.posts.length === 1)
          return {
            updated: body.entries.map(entry => ({
              entry_id: entry.entry_id,
              fields: { priority: 4, revision: entry.if_fields_revision + 1 }
            })),
            skipped: []
          };
        // Undo: song b was edited again in another tab.
        return {
          updated: body.entries
            .filter(entry => entry.entry_id !== 'b')
            .map(entry => ({
              entry_id: entry.entry_id,
              fields: {
                ...(body.patch.priority != null ? { priority: body.patch.priority } : {}),
                revision: entry.if_fields_revision + 1
              }
            })),
          skipped: body.entries
            .filter(entry => entry.entry_id === 'b')
            .map(entry => ({ entry_id: entry.entry_id, reason: 'revision_changed' }))
        };
      };
      await panel.applyBulk('priority', '4', null);
      assert.deepEqual(log.posts[0].patch, { priority: 4 });
      assert.equal(rows[2].priority, 4);
      await panel.undoBulk(null);
      // Songs that shared a previous value go together; no previous value clears.
      assert.deepEqual(log.posts.slice(1), [
        {
          entries: [
            { entry_id: 'a', if_fields_revision: 2 },
            { entry_id: 'b', if_fields_revision: 6 }
          ],
          patch: { priority: 1 },
          idempotency_key: log.posts[1].idempotency_key
        },
        {
          entries: [{ entry_id: 'c', if_fields_revision: 3 }],
          patch: { clear_priority: true },
          idempotency_key: log.posts[2].idempotency_key
        }
      ]);
      assert.equal(rows[0].priority, 1);
      assert.equal('priority' in rows[2], false);
      assert.equal(rows[1].priority, 4, 'the song changed elsewhere is left as the server has it');
      assert.equal(
        dom.elements.get('projectLibraryBulkResultText').textContent,
        'Undid: restored 2 songs · 1 skipped (changed elsewhere)'
      );
      assert.equal(dom.elements.get('projectLibraryBulkUndo').hidden, true, 'no undo of an undo');
    });
  } finally {
    globalThis.document = original;
  }
});

test('a failed bulk request changes no row and keeps the selection', async () => {
  const original = globalThis.document;
  const dom = bulkDom();
  globalThis.document = dom.document;
  try {
    const rows = [song('a', { stage: 'idea', fields_revision: 2 })];
    const { panel, log } = bulkPanel(rows);
    panel.post = async () => {
      throw Object.assign(new Error('Project library changed; review again'), { status: 409 });
    };
    panel.refresh = async () => {};
    await panel.applyBulk('stage', 'mixing', null);
    assert.equal(rows[0].stage, 'idea');
    assert.equal(panel.selectedProjects.size, 1);
    assert.equal(dom.elements.get('projectLibraryBulkResult').hidden, true);
    assert.match(log.statuses.at(-1), /changed/);
  } finally {
    globalThis.document = original;
  }
});

test('the setup queue takes only the catalog-only part of a mixed selection', async () => {
  const { panel } = bulkPanel([
    song('a'),
    song('b', { connection: 'connected' }),
    song('c'),
    song('d', { connection: 'needs_review' })
  ]);
  assert.deepEqual(panel.queueIds(), ['a', 'c']);
  const posts = [];
  panel.post = async (path, body) => {
    posts.push({ path, ids: [...body.ids] });
    throw new Error('stop here');
  };
  panel.restoreQueue = async () => {};
  panel.renderQueueControls = () => {};
  await panel.startQueue(null);
  assert.deepEqual(JSON.parse(JSON.stringify(posts)), [{ path: '/queue', ids: ['a', 'c'] }]);
  // One catalog-only song is not a queue.
  const single = bulkPanel([song('a'), song('b', { connection: 'connected' })]).panel;
  single.post = async () => {
    throw new Error('must not post');
  };
  await single.startQueue(null);
});
