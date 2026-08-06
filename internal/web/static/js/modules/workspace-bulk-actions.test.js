import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  confirmDelete,
  createGroupFrom,
  deleteWorkspace,
  deleteWorkspaces,
  isGroupRow,
  topLevelIds
} from './workspace-bulk-actions.js';

// A small tree: one group holding two workspaces, plus a standalone.
const ROWS = [
  { id: 'g1', name: 'Marketing', kind: 'group' },
  { id: 'w1', name: 'Alpha', parent_id: 'g1' },
  { id: 'w2', name: 'Beta', parent_id: 'g1' },
  { id: 'w3', name: 'Gamma' }
];

/** Record every request, and answer each one from `responses`. */
function recorder(responses = {}) {
  const calls = [];
  const fetchImpl = (url, init = {}) => {
    calls.push({
      url,
      method: init.method || 'GET',
      body: init.body ? JSON.parse(init.body) : null
    });
    const key = `${init.method || 'GET'} ${String(url).split('?')[0]}`;
    const reply = responses[key] || responses.default;
    if (typeof reply === 'function') return Promise.resolve(reply(url, init));
    return Promise.resolve(reply || { ok: true, status: 204 });
  };
  return { calls, fetchImpl };
}

function ctxFor({ answers = [], promptWith = 'New group', responses, rows = ROWS } = {}) {
  const asked = [];
  const announced = [];
  const toasted = [];
  const trashed = [];
  let changed = 0;
  const { calls, fetchImpl } = recorder(responses);
  const queue = [...answers];
  return {
    calls,
    asked,
    announced,
    toasted,
    trashed,
    changedCount: () => changed,
    ctx: {
      rows,
      fetch: fetchImpl,
      confirm: message => {
        asked.push(message);
        return queue.length ? queue.shift() : false;
      },
      prompt: () => promptWith,
      announce: message => announced.push(message),
      toast: (message, variant) => toasted.push({ message, variant }),
      onTrashed: (id, name) => trashed.push({ id, name }),
      onChanged: async () => {
        changed += 1;
      }
    }
  };
}

// ---------------------------------------------------------------------------
// topLevelIds
// ---------------------------------------------------------------------------

test('topLevelIds drops a child whose ancestor is also selected', () => {
  assert.deepEqual(topLevelIds(['g1', 'w1', 'w3'], ROWS).sort(), ['g1', 'w3']);
});

test('topLevelIds keeps children whose parent is not selected', () => {
  assert.deepEqual(topLevelIds(['w1', 'w2'], ROWS).sort(), ['w1', 'w2']);
});

test('topLevelIds survives a parent cycle in malformed data', () => {
  const cyclic = [
    { id: 'a', parent_id: 'b' },
    { id: 'b', parent_id: 'a' }
  ];
  // The guarantee is that it returns rather than hanging the click.
  assert.deepEqual(topLevelIds(['a'], cyclic), ['a']);
});

test('isGroupRow reads kind case-insensitively', () => {
  assert.equal(isGroupRow({ kind: 'GROUP' }), true);
  assert.equal(isGroupRow({ kind: 'workspace' }), false);
  assert.equal(isGroupRow(null), false);
});

// ---------------------------------------------------------------------------
// Grouping — the reason topLevelIds exists
// ---------------------------------------------------------------------------

test('grouping a parent and its own child never lifts the child out of the parent', async () => {
  const created = { ok: true, status: 201, json: () => Promise.resolve({ folder: { id: 'g2' } }) };
  const h = ctxFor({
    responses: { 'POST /api/workspaces': created, default: { ok: true, status: 204 } }
  });

  await createGroupFrom(['g1', 'w1'], h.ctx);

  const patched = h.calls.filter(c => c.method === 'PATCH').map(c => c.url);
  assert.equal(patched.length, 1, 'only the top-level group moves: ' + JSON.stringify(patched));
  assert.match(patched[0], /g1$/);
  assert.ok(
    !patched.some(url => url.endsWith('w1')),
    'a child of a moving group must not be reparented out of it'
  );
});

test('a cancelled name prompt creates nothing', async () => {
  const h = ctxFor({ promptWith: '   ' });
  const groupId = await createGroupFrom(['w3'], h.ctx);
  assert.equal(groupId, null);
  assert.equal(h.calls.length, 0, 'an empty name must not hit the network');
});

test('a failed reparent still reports that the group exists', async () => {
  const h = ctxFor({
    responses: {
      'POST /api/workspaces': {
        ok: true,
        status: 201,
        json: () => Promise.resolve({ folder: { id: 'g2' } })
      },
      default: { ok: false, status: 500, text: () => Promise.resolve('boom') }
    }
  });

  await createGroupFrom(['w3'], h.ctx);

  assert.equal(h.toasted.length, 1, 'the user is told');
  assert.match(h.toasted[0].message, /boom/);
  assert.equal(h.toasted[0].variant, 'error');
  assert.ok(h.changedCount() > 0, 'state is refreshed so the half-done group is visible');
});

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

test('a declined confirmation deletes nothing', async () => {
  const h = ctxFor({ answers: [false] });
  const done = await deleteWorkspace('w3', h.ctx);
  assert.equal(done, false);
  assert.equal(h.calls.length, 0);
});

test('deleting a group offers the two-mode choice and sends the chosen mode', async () => {
  const h = ctxFor({ answers: [true] }); // OK on "delete group AND contents"
  await deleteWorkspace('g1', h.ctx);

  assert.equal(h.calls.length, 1);
  assert.match(h.calls[0].url, /delete_mode=contents/);
  assert.match(h.asked[0], /everything inside it/);
});

test('declining "with contents" falls through to the group-only question', async () => {
  const h = ctxFor({ answers: [false, true] });
  await deleteWorkspace('g1', h.ctx);

  assert.equal(h.asked.length, 2, 'the second question must be asked');
  assert.match(h.calls[0].url, /delete_mode=group_only/);
});

test('a plain workspace delete asks once and sends no mode', async () => {
  const h = ctxFor({ answers: [true] });
  await deleteWorkspace('w3', h.ctx);

  assert.equal(h.asked.length, 1);
  assert.ok(!h.calls[0].url.includes('delete_mode'), 'no mode for a non-group');
});

test('a trashed delete becomes an undo entry; a permanent one does not', async () => {
  const trashedReply = {
    ok: true,
    status: 200,
    json: () => Promise.resolve({ trashed: true })
  };
  const h = ctxFor({ answers: [true], responses: { default: trashedReply } });
  await deleteWorkspace('w3', h.ctx);
  assert.deepEqual(h.trashed, [{ id: 'w3', name: 'Gamma' }]);

  const gone = ctxFor({ answers: [true], responses: { default: { ok: true, status: 204 } } });
  await deleteWorkspace('w3', gone.ctx);
  assert.deepEqual(gone.trashed, [], 'a 204 carries no restore point');
});

test('a single-item batch still gets the per-item group question', async () => {
  const h = ctxFor({ answers: [true] });
  await deleteWorkspaces(['g1'], h.ctx);

  assert.match(h.asked[0], /everything inside it/, 'a lone group must not take a silent default');
  assert.match(h.calls[0].url, /delete_mode=contents/);
});

test('a batch delete confirms once and reports a partial failure honestly', async () => {
  const h = ctxFor({
    answers: [true],
    responses: {
      default: url =>
        String(url).includes('w2')
          ? { ok: false, status: 500, text: () => Promise.resolve('nope') }
          : { ok: true, status: 204 }
    }
  });

  const deleted = await deleteWorkspaces(['w1', 'w2', 'w3'], h.ctx);

  assert.equal(h.asked.length, 1, 'one batch confirmation, not one per item');
  assert.equal(deleted, 2);
  const said = h.announced.join(' ');
  assert.match(said, /Deleted 2 of 3/);
  assert.match(said, /Beta/, 'the failed item is named');
  assert.equal(h.toasted[0].variant, 'error');
});

test('a batch delete that fully succeeds reports the plain count', async () => {
  const h = ctxFor({ answers: [true] });
  const deleted = await deleteWorkspaces(['w1', 'w3'], h.ctx);

  assert.equal(deleted, 2);
  assert.deepEqual(h.announced, ['2 items deleted.']);
  assert.deepEqual(h.toasted, [], 'a clean run raises no error toast');
});

test('a declined batch confirmation deletes nothing', async () => {
  const h = ctxFor({ answers: [false] });
  const deleted = await deleteWorkspaces(['w1', 'w3'], h.ctx);
  assert.equal(deleted, 0);
  assert.equal(h.calls.length, 0);
});

test('confirmDelete never proceeds when there is no way to ask', () => {
  assert.equal(
    confirmDelete({ name: 'X' }, false, () => false),
    null
  );
});

// ---------------------------------------------------------------------------
// Error reporting
// ---------------------------------------------------------------------------

test('a failure surfaces the API error text, not the raw JSON envelope', async () => {
  // The real shape returned by a folder-slug conflict, which is the most
  // common way creating a group actually fails.
  const conflict = {
    ok: false,
    status: 409,
    text: () =>
      Promise.resolve(
        JSON.stringify({
          success: false,
          error: 'A workspace folder named "verified-group" already exists.',
          conflict: { type: 'folder_slug', suggested_slug: 'verified-group-2' }
        })
      )
  };
  const h = ctxFor({ responses: { 'POST /api/workspaces': conflict } });

  await createGroupFrom(['w3'], h.ctx);

  assert.equal(h.toasted.length, 1);
  assert.equal(
    h.toasted[0].message,
    'A workspace folder named "verified-group" already exists.',
    'the user must see the sentence, not the envelope'
  );
  assert.ok(!h.toasted[0].message.includes('{'), 'no JSON leaks into the toast');
});

test('a failure with only a message field still reads correctly', async () => {
  const h = ctxFor({
    answers: [true],
    responses: {
      default: {
        ok: false,
        status: 500,
        text: () => Promise.resolve(JSON.stringify({ message: 'Disk is full' }))
      }
    }
  });

  await deleteWorkspace('w3', h.ctx);
  assert.equal(h.toasted[0].message, 'Disk is full');
});

test('a non-JSON failure body is passed through verbatim', async () => {
  const h = ctxFor({
    answers: [true],
    responses: {
      default: { ok: false, status: 502, text: () => Promise.resolve('upstream unavailable') }
    }
  });

  await deleteWorkspace('w3', h.ctx);
  assert.equal(h.toasted[0].message, 'upstream unavailable');
});
