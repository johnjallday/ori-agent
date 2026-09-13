import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  confirmDelete,
  moveMembersIntoGroup,
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

function ctxFor({ answers = [], responses, rows = ROWS } = {}) {
  const asked = [];
  const announced = [];
  const toasted = [];
  const trashed = [];
  const navigated = [];
  let changed = 0;
  const { calls, fetchImpl } = recorder(responses);
  const queue = [...answers];
  return {
    calls,
    asked,
    announced,
    toasted,
    trashed,
    navigated,
    changedCount: () => changed,
    ctx: {
      rows,
      navigate: url => navigated.push(url),
      fetch: fetchImpl,
      confirm: message => {
        asked.push(message);
        return queue.length ? queue.shift() : false;
      },
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
// Member moves — the reason topLevelIds exists
// ---------------------------------------------------------------------------

const CREATED_GROUP = { id: 'g2', name: 'New group' };

test('moving a parent and its own child never lifts the child out of the parent', async () => {
  const h = ctxFor({ responses: { default: { ok: true, status: 204 } } });

  await moveMembersIntoGroup(CREATED_GROUP, ['g1', 'w1'], h.ctx);

  const patched = h.calls.filter(c => c.method === 'PATCH').map(c => c.url);
  assert.equal(patched.length, 1, 'only the top-level group moves: ' + JSON.stringify(patched));
  assert.match(patched[0], /g1$/);
  assert.ok(!patched.some(url => url.endsWith('w1')));
  assert.equal(h.calls.filter(c => c.method === 'POST').length, 0, 'moves never create a group');
});

test('a failed reparent still reports the durable group identity', async () => {
  const h = ctxFor({
    responses: { default: { ok: false, status: 500, text: () => Promise.resolve('boom') } }
  });
  const result = await moveMembersIntoGroup(CREATED_GROUP, ['w3'], h.ctx);

  assert.equal(h.toasted.length, 1, 'the user is told');
  assert.match(h.toasted[0].message, /could not be moved/);
  assert.equal(h.toasted[0].variant, 'error');
  assert.ok(h.changedCount() > 0, 'state is refreshed so the half-done group is visible');
  assert.equal(result.groupId, 'g2', 'and the caller still learns the group exists');
  assert.deepEqual(result.failed, ['w3']);
});

// ---------------------------------------------------------------------------
// Grouping outcome (#346 FR-13, FR-14, FR-28)
// ---------------------------------------------------------------------------

test('a successful member move reports its group id and every member the hierarchy placed (#346 FR-28)', async () => {
  const h = ctxFor({ responses: { default: { ok: true, status: 204 } } });
  const result = await moveMembersIntoGroup(CREATED_GROUP, ['w1', 'w3'], h.ctx);

  assert.equal(result.groupId, 'g2');
  assert.equal(result.name, 'New group');
  assert.deepEqual(result.placed, ['w1', 'w3']);
  assert.deepEqual(result.failed, []);
  assert.deepEqual(result.uncertain, []);
  assert.equal(result.partial, false);
});

test('a partly failed group reports which members actually moved (#346 FR-28)', async () => {
  const h = ctxFor({
    responses: {
      default: (url, init) =>
        init.method === 'PATCH' && String(url).endsWith('w3')
          ? { ok: false, status: 500, text: () => Promise.resolve('boom') }
          : { ok: true, status: 204 }
    }
  });
  const result = await moveMembersIntoGroup(CREATED_GROUP, ['w1', 'w3'], h.ctx);

  assert.equal(result.groupId, 'g2', 'the group still exists and is still reported');
  assert.deepEqual(result.placed, ['w1']);
  assert.deepEqual(result.failed, ['w3']);
  assert.equal(result.partial, true);
  assert.equal(h.toasted.length, 1, 'the partial outcome is surfaced');
  assert.match(h.toasted[0].message, /could not be moved/i);
});

test('a lost member response is verified after refresh before it is reported', async () => {
  const h = ctxFor({ responses: { default: () => Promise.reject(new Error('connection lost')) } });
  h.ctx.verifyMembership = ({ groupId, memberIds }) => {
    assert.equal(groupId, 'g2');
    assert.deepEqual(memberIds, ['w3']);
    return new Set(['w3']);
  };
  const result = await moveMembersIntoGroup(CREATED_GROUP, ['w3'], h.ctx);
  assert.deepEqual(result.placed, ['w3']);
  assert.deepEqual(result.uncertain, []);
  assert.equal(result.partial, false);
  assert.ok(h.changedCount() >= 1, 'refresh happens before verification');
});

test('an unverifiable lost member response stays uncertain without a retry', async () => {
  const h = ctxFor({ responses: { default: () => Promise.reject(new Error('connection lost')) } });
  h.ctx.verifyMembership = () => new Set();
  const result = await moveMembersIntoGroup(CREATED_GROUP, ['w3'], h.ctx);
  assert.deepEqual(result.placed, []);
  assert.deepEqual(result.uncertain, ['w3']);
  assert.equal(result.partial, true);
  assert.equal(h.calls.filter(call => call.method === 'PATCH').length, 1, 'no automatic retry');
});

test('moving members requires the already-created group identity', async () => {
  const h = ctxFor();
  await assert.rejects(() => moveMembersIntoGroup({}, ['w1', 'w3'], h.ctx), /identity/);
  assert.equal(h.calls.length, 0);
});

test('member moves never touch the Map layout or a coordinate (#346 FR-13, FR-14)', async () => {
  const h = ctxFor({ responses: { default: { ok: true, status: 204 } } });

  await moveMembersIntoGroup(CREATED_GROUP, ['w1', 'w3'], h.ctx);

  assert.equal(
    h.calls.filter(c => String(c.url).includes('workspace-map')).length,
    0,
    'the hierarchy mutation must not repack, snap, or relocate anything'
  );
  const bodies = h.calls.map(c => JSON.stringify(c.body || {}));
  assert.ok(
    !bodies.some(body => body.includes('"x"') || body.includes('"y"')),
    'no coordinate is proposed by the grouping flow'
  );
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

test('member moves never send a group creation request', async () => {
  const h = ctxFor({ responses: { default: { ok: true, status: 204 } } });
  await moveMembersIntoGroup(CREATED_GROUP, ['w3'], h.ctx);
  assert.equal(h.calls.filter(call => call.method === 'POST').length, 0);
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

function reviewRequired(slug = 'music-home') {
  return {
    ok: false,
    status: 409,
    text: async () =>
      JSON.stringify({
        code: 'assistant_program_review_required',
        message: slug
          ? 'Use Review disconnect in Music Home before deleting.'
          : 'Restore Music Home from Trash first.',
        details: slug ? { review_home_slug: slug } : {}
      })
  };
}

test('protected deletion offers the Home review without deleting or retrying', async () => {
  const h = ctxFor({ answers: [true, true], responses: { default: reviewRequired() } });
  assert.equal(await deleteWorkspace('w3', h.ctx), false);
  assert.deepEqual(h.navigated, ['/workspaces/music-home/assistant']);
  assert.match(h.toasted[0].message, /Review disconnect/);
  assert.equal(h.calls.length, 1, 'no automatic disconnect or delete retry');
  assert.equal(h.changedCount(), 0);
  assert.deepEqual(h.trashed, []);
});

test('declining the review navigation leaves the protected workspace alone', async () => {
  const h = ctxFor({ answers: [true, false], responses: { default: reviewRequired() } });
  await deleteWorkspace('w3', h.ctx);
  assert.deepEqual(h.navigated, []);
  assert.equal(h.calls.length, 1);
});

test('a trashed Home gives recovery instructions rather than a broken review link', async () => {
  const h = ctxFor({ answers: [true, true], responses: { default: reviewRequired('') } });
  await deleteWorkspace('w3', h.ctx);
  assert.match(h.toasted[0].message, /Restore Music Home/);
  assert.equal(h.asked.length, 1);
  assert.deepEqual(h.navigated, []);
});

test('bulk deletion keeps the review reason and offers a shared Home only once', async () => {
  const h = ctxFor({
    answers: [true, true],
    responses: {
      'DELETE /api/workspaces/w1': reviewRequired(),
      'DELETE /api/workspaces/w2': reviewRequired(),
      default: { ok: true, status: 204 }
    }
  });
  assert.equal(await deleteWorkspaces(['w1', 'w2', 'w3'], h.ctx), 1);
  assert.match(h.announced[0], /Deleted 1 of 3/);
  assert.match(h.announced[0], /Review disconnect/);
  assert.deepEqual(h.navigated, ['/workspaces/music-home/assistant']);
  assert.equal(h.asked.length, 2);
  assert.equal(h.calls.length, 3);
});

test('bulk deletion does not choose arbitrarily between multiple Assistant Homes', async () => {
  const h = ctxFor({
    answers: [true, true],
    responses: {
      'DELETE /api/workspaces/w1': reviewRequired('music-home'),
      'DELETE /api/workspaces/w2': reviewRequired('another-home')
    }
  });
  assert.equal(await deleteWorkspaces(['w1', 'w2'], h.ctx), 0);
  assert.equal(h.asked.length, 1);
  assert.deepEqual(h.navigated, []);
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
