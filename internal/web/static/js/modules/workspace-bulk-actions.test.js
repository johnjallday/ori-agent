import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  descendantCount,
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

/**
 * A scripted stand-in for workspace-delete-dialog.js.
 *
 * Every question the controller asks consumes the next entry of `answers`:
 * `chooseDelete` expects `{ mode }` or null, `confirmCount` and `review` expect
 * a boolean. An exhausted queue declines, exactly like Escape. Every step is
 * logged so a test can assert what the user was shown, not only what was sent.
 */
function fakeDialog(answers = []) {
  const queue = [...answers];
  const steps = [];
  const next = declined => (queue.length ? queue.shift() : declined);
  const open = ({ title }) => {
    steps.push({ step: 'open', title });
    return {
      chooseDelete(spec) {
        steps.push({ step: 'choose', ...spec });
        return Promise.resolve(next(null));
      },
      confirmCount(spec) {
        steps.push({ step: 'count', ...spec });
        return Promise.resolve(next(false));
      },
      busy(text) {
        steps.push({ step: 'busy', text });
      },
      async review({ heading, summary, impact, confirmLabel, confirm }) {
        steps.push({ step: 'review', heading, summary, impact, confirmLabel });
        if (!next(false)) return false;
        try {
          await confirm();
          return true;
        } catch (error) {
          // The real dialog shows the error inline and waits for another click.
          steps.push({ step: 'review-error', message: error.message });
          return next(false)
            ? this.review({ heading, summary, impact, confirmLabel, confirm })
            : false;
        }
      },
      notice({ message, action }) {
        steps.push({ step: 'notice', message, action: action || null });
        return Promise.resolve();
      },
      close() {
        steps.push({ step: 'close' });
      }
    };
  };
  return { open, steps };
}

function ctxFor({ answers = [], responses, rows = ROWS, dialog } = {}) {
  const announced = [];
  const toasted = [];
  const trashed = [];
  let changed = 0;
  const { calls, fetchImpl } = recorder(responses);
  const ui = dialog || fakeDialog(answers);
  return {
    calls,
    steps: ui.steps,
    stepsOf: name => ui.steps.filter(entry => entry.step === name),
    announced,
    toasted,
    trashed,
    changedCount: () => changed,
    ctx: {
      rows,
      openDialog: ui.open,
      fetch: fetchImpl,
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
// topLevelIds / descendantCount
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

test('descendantCount counts every nesting level once and survives a cycle', () => {
  const nested = [
    ...ROWS,
    { id: 'g2', name: 'Inner', kind: 'group', parent_id: 'g1' },
    { id: 'w4', name: 'Delta', parent_id: 'g2' }
  ];
  assert.equal(descendantCount(nested, 'g1'), 4);
  assert.equal(descendantCount(nested, 'g2'), 1);
  assert.equal(descendantCount(nested, 'w3'), 0);
  const cyclic = [
    { id: 'a', parent_id: 'b' },
    { id: 'b', parent_id: 'a' }
  ];
  assert.equal(descendantCount(cyclic, 'a'), 1);
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

test('a declined confirmation deletes nothing and closes the dialog', async () => {
  const h = ctxFor({ answers: [null] });
  const done = await deleteWorkspace('w3', h.ctx);
  assert.equal(done, false);
  assert.equal(h.calls.length, 0);
  assert.equal(h.stepsOf('close').length, 1);
});

test('deleting a group shows the two modes as a choice and sends the chosen one', async () => {
  const h = ctxFor({ answers: [{ mode: 'contents' }] });
  await deleteWorkspace('g1', h.ctx);

  const [asked] = h.stepsOf('choose');
  assert.equal(asked.group, true);
  assert.equal(asked.memberCount, 2, 'the dialog can say how many workspaces ride along');
  assert.equal(asked.name, 'Marketing');
  assert.equal(h.calls.length, 1);
  assert.match(h.calls[0].url, /delete_mode=contents/);
});

test('choosing "group only" sends group_only', async () => {
  const h = ctxFor({ answers: [{ mode: 'group_only' }] });
  await deleteWorkspace('g1', h.ctx);
  assert.match(h.calls[0].url, /delete_mode=group_only/);
});

test('a plain workspace delete asks once with no group choice and sends no mode', async () => {
  const h = ctxFor({ answers: [{ mode: '' }] });
  await deleteWorkspace('w3', h.ctx);

  const asked = h.stepsOf('choose');
  assert.equal(asked.length, 1);
  assert.equal(asked[0].group, false);
  assert.ok(!h.calls[0].url.includes('delete_mode'), 'no mode for a non-group');
  assert.deepEqual(h.stepsOf('close').length, 1, 'the dialog closes on success');
  assert.deepEqual(h.announced, ['Gamma deleted.']);
});

test('a trashed delete becomes an undo entry; a permanent one does not', async () => {
  const trashedReply = {
    ok: true,
    status: 200,
    json: () => Promise.resolve({ trashed: true })
  };
  const h = ctxFor({ answers: [{ mode: '' }], responses: { default: trashedReply } });
  await deleteWorkspace('w3', h.ctx);
  assert.deepEqual(h.trashed, [{ id: 'w3', name: 'Gamma' }]);

  const gone = ctxFor({
    answers: [{ mode: '' }],
    responses: { default: { ok: true, status: 204 } }
  });
  await deleteWorkspace('w3', gone.ctx);
  assert.deepEqual(gone.trashed, [], 'a 204 carries no restore point');
});

test('a single-item batch still gets the per-item group question', async () => {
  const h = ctxFor({ answers: [{ mode: 'contents' }] });
  await deleteWorkspaces(['g1'], h.ctx);

  assert.equal(h.stepsOf('choose')[0].group, true, 'a lone group must not take a silent default');
  assert.equal(h.stepsOf('count').length, 0);
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

  assert.equal(h.stepsOf('count').length, 1, 'one batch confirmation, not one per item');
  assert.equal(h.stepsOf('count')[0].count, 3);
  assert.equal(deleted, 2);
  const said = h.announced.join(' ');
  assert.match(said, /Deleted 2 of 3/);
  assert.match(said, /Beta/, 'the failed item is named');
  assert.equal(h.toasted[0].variant, 'error');
  assert.equal(h.stepsOf('close').length, 1);
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

test('nothing is deleted when there is no dialog to ask with', async () => {
  const h = ctxFor();
  h.ctx.openDialog = () => null;
  assert.equal(await deleteWorkspace('w3', h.ctx), false);
  assert.equal(await deleteWorkspaces(['w1', 'w3'], h.ctx), 0);
  assert.equal(h.calls.length, 0);
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
    answers: [{ mode: '' }],
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
  assert.equal(h.stepsOf('close').length, 1, 'a plain failure closes the dialog and toasts');
});

test('a non-JSON failure body is passed through verbatim', async () => {
  const h = ctxFor({
    answers: [{ mode: '' }],
    responses: {
      default: { ok: false, status: 502, text: () => Promise.resolve('upstream unavailable') }
    }
  });

  await deleteWorkspace('w3', h.ctx);
  assert.equal(h.toasted[0].message, 'upstream unavailable');
});

// ---------------------------------------------------------------------------
// Assistant Home review, resolved inside the dialog
// ---------------------------------------------------------------------------

const HOME_ROWS = [
  { id: 'home', name: 'Music Home', kind: 'group' },
  { id: 'song', name: 'Song', parent_id: 'home' },
  ...ROWS
];

function reviewRequired({
  station = 'home',
  action = 'Review Home removal',
  slug = 'music-home',
  message = 'Use Review Home removal in Music Home before deleting.'
} = {}) {
  return {
    ok: false,
    status: 409,
    text: async () =>
      JSON.stringify({
        code: 'assistant_program_review_required',
        message,
        details: {
          workspace_id: station,
          station_workspace_id: station,
          review_action: action,
          ...(slug ? { review_home_slug: slug } : {})
        }
      })
  };
}

const json = data => ({ ok: true, status: 200, json: async () => data });

function homeResponses({
  linked = 2,
  roles = 1,
  impact = ['Projects stay.', 'Folders stay.']
} = {}) {
  return {
    'GET /api/workspaces/home/assistant-program': json({
      available: true,
      is_station: true,
      state_revision: 7,
      declaration: { station_name: 'Music Home' }
    }),
    'POST /api/workspaces/home/assistant-program/remove-home/review': json({
      token: 'tok-1',
      linked_project_count: linked,
      home_role_count: roles,
      impact
    }),
    'POST /api/workspaces/home/assistant-program/remove-home/commit': json({
      station_workspace_id: 'home'
    })
  };
}

test('a protected group is reviewed and removed inside the same dialog', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [{ mode: 'group_only' }, true],
    responses: { 'DELETE /api/workspaces/home': reviewRequired(), ...homeResponses() }
  });

  assert.equal(await deleteWorkspace('home', h.ctx), true);

  const review = h.calls.find(c => c.url.endsWith('/remove-home/review'));
  assert.deepEqual(review.body, { state_revision: 7 }, 'the review is bound to the live revision');
  const commit = h.calls.find(c => c.url.endsWith('/remove-home/commit'));
  assert.deepEqual(commit.body, { token: 'tok-1' });

  const [shown] = h.stepsOf('review');
  assert.equal(shown.heading, 'Remove "Music Home"?');
  assert.match(shown.summary, /2 linked projects will be kept as standalone workspaces/);
  assert.match(shown.summary, /permanently rather than moved to the Trash/);
  assert.deepEqual(shown.impact, ['Projects stay.', 'Folders stay.']);
  assert.equal(shown.confirmLabel, 'Remove Home');

  assert.equal(h.stepsOf('notice').length, 0, 'no detour through another page');
  assert.ok(h.announced.includes('Music Home removed.'));
  assert.deepEqual(h.trashed, [], 'a Home removal is not a Trash entry');
  assert.equal(h.changedCount(), 1);
  assert.equal(h.stepsOf('close').length, 1);
  assert.equal(h.calls.filter(c => c.method === 'DELETE').length, 1, 'the Home is gone, no retry');
});

test('an empty Home reads like a plain delete', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [{ mode: 'group_only' }, true],
    responses: {
      'DELETE /api/workspaces/home': reviewRequired(),
      ...homeResponses({ linked: 0, roles: 0, impact: ['Every linked project is preserved.'] })
    }
  });

  assert.equal(await deleteWorkspace('home', h.ctx), true);
  const [shown] = h.stepsOf('review');
  assert.match(shown.summary, /no linked projects, so removing it deletes only the group\./);
  assert.deepEqual(shown.impact, [], 'nothing to preserve, so no preservation notes');
});

test('an empty Home with roles says the roles go with it', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [{ mode: 'group_only' }, true],
    responses: {
      'DELETE /api/workspaces/home': reviewRequired(),
      ...homeResponses({ linked: 0, roles: 2 })
    }
  });
  await deleteWorkspace('home', h.ctx);
  assert.match(h.stepsOf('review')[0].summary, /deletes only the group and its 2 Home roles/);
});

test('declining the Home review removes nothing', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [{ mode: 'group_only' }, false],
    responses: { 'DELETE /api/workspaces/home': reviewRequired(), ...homeResponses() }
  });

  assert.equal(await deleteWorkspace('home', h.ctx), false);
  assert.equal(
    h.calls.some(c => c.url.endsWith('/remove-home/commit')),
    false
  );
  assert.equal(h.changedCount(), 0);
  assert.equal(h.stepsOf('close').length, 1);
});

test('a failed commit stays in the dialog with the reason instead of closing', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [{ mode: 'group_only' }, true, false],
    responses: {
      'DELETE /api/workspaces/home': reviewRequired(),
      ...homeResponses(),
      'POST /api/workspaces/home/assistant-program/remove-home/commit': {
        ok: false,
        status: 409,
        text: async () => JSON.stringify({ error: 'The review expired. Review again.' })
      }
    }
  });

  assert.equal(await deleteWorkspace('home', h.ctx), false);
  assert.deepEqual(
    h.stepsOf('review-error').map(entry => entry.message),
    ['The review expired. Review again.']
  );
  assert.equal(h.changedCount(), 0);
});

test('a Home nested inside the group is removed, then the group delete continues', async () => {
  const rows = [
    { id: 'g1', name: 'Marketing', kind: 'group' },
    { id: 'home', name: 'Music Home', kind: 'group', parent_id: 'g1' },
    { id: 'w3', name: 'Gamma' }
  ];
  let attempts = 0;
  const h = ctxFor({
    rows,
    answers: [{ mode: 'contents' }, true],
    responses: {
      'DELETE /api/workspaces/g1': () =>
        ++attempts === 1 ? reviewRequired() : { ok: true, status: 204 },
      ...homeResponses({ linked: 0, roles: 0 })
    }
  });

  assert.equal(await deleteWorkspace('g1', h.ctx), true);

  const deletes = h.calls.filter(c => c.method === 'DELETE');
  assert.equal(deletes.length, 2, 'the original delete is retried once the blocker is gone');
  assert.ok(
    deletes.every(c => c.url.includes('delete_mode=contents')),
    'with the mode the user chose'
  );
  assert.equal(h.calls.filter(c => c.url.endsWith('/remove-home/commit')).length, 1);
  const [shown] = h.stepsOf('review');
  assert.equal(shown.heading, 'Remove the Assistant Home in "Marketing"?');
  assert.match(shown.summary, /"Marketing" contains the Assistant Home "Music Home"/);
  assert.match(shown.summary, /Deleting "Marketing" then continues/);
  assert.ok(h.announced.includes('Marketing deleted.'));
});

test('the review-and-retry loop is bounded', async () => {
  const rows = [
    { id: 'g1', name: 'Marketing', kind: 'group' },
    { id: 'home', name: 'Music Home', kind: 'group', parent_id: 'g1' }
  ];
  const h = ctxFor({
    rows,
    answers: [{ mode: 'group_only' }, true, true, true, true, true],
    responses: { 'DELETE /api/workspaces/g1': reviewRequired(), ...homeResponses() }
  });

  assert.equal(await deleteWorkspace('g1', h.ctx), false);
  assert.equal(h.calls.filter(c => c.method === 'DELETE').length, 4, 'three reviews, then stop');
  assert.equal(h.toasted.at(-1).variant, 'error');
  assert.equal(h.stepsOf('close').length, 1);
});

test('a disconnect review is offered as a link, never performed automatically', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [{ mode: '' }],
    responses: {
      'DELETE /api/workspaces/song': reviewRequired({
        station: 'home',
        action: 'Review disconnect',
        message: 'Use Review disconnect in Music Home before deleting.'
      })
    }
  });

  assert.equal(await deleteWorkspace('song', h.ctx), false);
  const [notice] = h.stepsOf('notice');
  assert.match(notice.message, /Review disconnect/);
  assert.deepEqual(notice.action, {
    label: 'Open Assistant Home',
    href: '/workspaces/music-home/assistant'
  });
  assert.equal(h.calls.length, 1, 'no disconnect, no delete retry');
  assert.equal(h.changedCount(), 0);
});

test('a trashed Home gives recovery instructions rather than a broken review', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [{ mode: 'group_only' }, true],
    responses: {
      'DELETE /api/workspaces/home': reviewRequired({
        slug: '',
        message: 'Restore Music Home from Trash first.'
      })
    }
  });

  assert.equal(await deleteWorkspace('home', h.ctx), false);
  const [notice] = h.stepsOf('notice');
  assert.match(notice.message, /Restore Music Home/);
  assert.equal(notice.action, null);
  assert.equal(h.calls.length, 1, 'nothing is fetched for a Home that has no page');
});

test('a Home the assistant API cannot read is reported, not guessed at', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [{ mode: 'group_only' }, true],
    responses: {
      'DELETE /api/workspaces/home': reviewRequired(),
      'GET /api/workspaces/home/assistant-program': json({ available: false })
    }
  });

  assert.equal(await deleteWorkspace('home', h.ctx), false);
  assert.match(h.stepsOf('notice')[0].message, /could not be read/);
  assert.equal(
    h.calls.some(c => c.url.endsWith('/remove-home/review')),
    false
  );
});

test('bulk deletion keeps the review reason and reviews a shared Home once', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [true, true],
    responses: {
      'DELETE /api/workspaces/w1': reviewRequired(),
      'DELETE /api/workspaces/w2': reviewRequired(),
      ...homeResponses(),
      default: { ok: true, status: 204 }
    }
  });

  assert.equal(await deleteWorkspaces(['w1', 'w2', 'w3'], h.ctx), 1);
  assert.match(h.announced[0], /Deleted 1 of 3/);
  assert.match(h.announced[0], /Review Home removal/);
  assert.equal(h.stepsOf('review').length, 1, 'one review for the one Home');
  assert.equal(h.calls.filter(c => c.url.endsWith('/remove-home/commit')).length, 1);
  assert.equal(h.stepsOf('close').length, 1);
});

test('bulk deletion counts a removed Home that was itself selected', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [true, true],
    responses: {
      'DELETE /api/workspaces/home': reviewRequired(),
      ...homeResponses(),
      default: { ok: true, status: 204 }
    }
  });
  assert.equal(await deleteWorkspaces(['home', 'w3'], h.ctx), 2);
});

test('bulk deletion does not choose arbitrarily between multiple Assistant Homes', async () => {
  const h = ctxFor({
    rows: HOME_ROWS,
    answers: [true, true],
    responses: {
      'DELETE /api/workspaces/w1': reviewRequired({ station: 'home', slug: 'music-home' }),
      'DELETE /api/workspaces/w2': reviewRequired({ station: 'other', slug: 'another-home' })
    }
  });
  assert.equal(await deleteWorkspaces(['w1', 'w2'], h.ctx), 0);
  assert.equal(h.stepsOf('review').length, 0);
  assert.equal(h.stepsOf('notice').length, 0);
  assert.equal(h.calls.length, 2);
  assert.equal(h.stepsOf('close').length, 1);
});
