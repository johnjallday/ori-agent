// Tests for workspace-role-roster.js — the shared roster component.
//
// The component is a classic deferred script, so it is evaluated in a node:vm
// sandbox with a minimal window, mirroring agent-avatar.test.js. These tests
// cover the pure projection → rows mapping: both empty states, the display
// order, and the copy the two surfaces share.
//   node --test internal/web/static/js/modules/workspace-role-roster.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-role-roster.js', import.meta.url), 'utf8');

function load() {
  const sandbox = { window: {}, Object, Array, String, Number, Boolean, JSON };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.window.WorkspaceRoleRoster;
}

const Roster = load();

function role(overrides = {}) {
  return {
    role_id: 'mix-engineer',
    label: 'Mix Engineer',
    description: 'You balance the mix.',
    scope: 'project',
    required: true,
    primary: false,
    state: 'empty',
    ...overrides
  };
}

function filled(overrides = {}) {
  return role({
    state: 'filled',
    source: 'created',
    agent: { name: 'Mix Engineer', role: 'specialist' },
    ...overrides
  });
}

test('an empty required role reads Missing, and an empty optional role reads Optional', () => {
  const rows = Roster.rowsFrom({
    roles: [role(), role({ role_id: 'songwriter', label: 'Songwriter', required: false })]
  });
  assert.equal(rows[0].tag.label, 'Missing');
  assert.equal(rows[0].tag.key, 'missing');
  assert.equal(rows[1].tag.label, 'Optional');
  assert.equal(rows[1].tag.key, 'optional');
});

// FR6: Missing is an invitation, not an error. Nothing in the row model may
// classify it as one, or the styling and the announcement follow.
test('neither empty state is modelled as an error', () => {
  const rows = Roster.rowsFrom({ roles: [role(), role({ role_id: 'x', required: false })] });
  for (const row of rows) {
    assert.equal(row.tag.key === 'danger' || row.tag.key === 'error', false);
    assert.equal('alert' in row, false);
  }
  assert.equal(Roster.COPY.missing, 'Missing');
  assert.equal(Roster.COPY.optional, 'Optional');
});

test('a filled role names its agent and how it was filled', () => {
  const rows = Roster.rowsFrom({
    roles: [
      filled(),
      filled({
        role_id: 'producer',
        label: 'Producer',
        source: 'assigned',
        agent: { name: 'My Producer' }
      })
    ]
  });
  assert.equal(rows[0].state, 'filled');
  assert.equal(rows[0].tag.label, 'Mix Engineer');
  assert.equal(rows[0].sourceLabel, 'New agent');
  assert.equal(rows[1].sourceLabel, 'Your saved agent');
  const groupHolder = Roster.rowsFrom({
    roles: [filled({ scope: 'home', source: 'group', agent: { name: 'Shared Coordinator' } })]
  })[0];
  assert.equal(groupHolder.sourceLabel, 'Existing group holder');
});

// A projection claiming "filled" with nobody in it would draw a row with an
// action set the user cannot act on. Treat it as empty instead.
test('a filled role with no agent falls back to empty', () => {
  const rows = Roster.rowsFrom({ roles: [role({ state: 'filled' })] });
  assert.equal(rows[0].state, 'empty');
  assert.equal(rows[0].tag.label, 'Missing');
});

test('rows carry designation and scope in words', () => {
  const rows = Roster.rowsFrom({
    roles: [
      role({ role_id: 'lead', primary: true }),
      role({ role_id: 'coordinator', scope: 'home', primary: false })
    ]
  });
  assert.equal(rows[0].designation, 'PROJECT LEAD');
  assert.equal(rows[0].scopeLine, 'this workspace only');
  assert.equal(rows[1].designation, 'SPECIALIST');
  assert.equal(rows[1].scopeLine, 'group scope only');

  const coordinator = Roster.rowsFrom({
    roles: [role({ role_id: 'home-lead', scope: 'home', primary: true })]
  })[0];
  assert.equal(coordinator.designation, 'GROUP COORDINATOR');
});

test('mixed scopes become separately named sections with required-only progress', () => {
  const rows = Roster.rowsFrom({
    roles: [
      filled({ role_id: 'home-lead', scope: 'home', primary: true }),
      role({ role_id: 'home-extra', scope: 'home', required: false }),
      role({ role_id: 'project-lead', primary: true }),
      role({ role_id: 'project-extra', required: false })
    ]
  });
  const sections = Roster.sectionsFrom(rows, {
    groupTitle: 'Group coordination — Research Home',
    projectTitle: 'Team for Field Notes'
  });
  assert.equal(sections.length, 2);
  assert.equal(sections[0].title, 'Group coordination — Research Home');
  assert.equal(sections[1].title, 'Team for Field Notes');
  assert.equal(Roster.requiredProgress(sections[0].rows), '1 of 1 required role filled');
  assert.equal(Roster.requiredProgress(sections[1].rows), '0 of 1 required role filled');
});

test('a project-only roster keeps one unlabelled section', () => {
  const rows = Roster.rowsFrom({ roles: [role({ primary: true }), role()] });
  const sections = Roster.sectionsFrom(rows, {});
  assert.equal(sections.length, 1);
  assert.equal(sections[0].scope, 'project');
  assert.equal(sections[0].title, '');
});

// FR40: primary first, then the required roles still waiting, then the settled
// ones, and last the optional roles nobody has to fill.
test('rows sort primary, then required-empty, then filled, then optional-empty', () => {
  const rows = Roster.rowsFrom({
    roles: [
      role({ role_id: 'optional-empty', required: false }),
      filled({ role_id: 'filled-required' }),
      role({ role_id: 'required-empty' }),
      role({ role_id: 'primary-empty', primary: true }),
      filled({ role_id: 'filled-optional', required: false })
    ]
  });
  assert.deepEqual(
    rows.map(row => row.roleId),
    ['primary-empty', 'required-empty', 'filled-required', 'filled-optional', 'optional-empty']
  );
});

test('declaration order breaks ties inside a bucket', () => {
  const rows = Roster.rowsFrom({
    roles: [
      role({ role_id: 'second' }),
      role({ role_id: 'third' }),
      role({ role_id: 'first', primary: true })
    ]
  });
  assert.deepEqual(
    rows.map(row => row.roleId),
    ['first', 'second', 'third']
  );
});

test('an empty optional role is marked quiet and a Missing one is not', () => {
  const rows = Roster.rowsFrom({
    roles: [
      role(),
      role({ role_id: 'songwriter', required: false }),
      filled({ role_id: 'p', required: false })
    ]
  });
  assert.equal(rows[0].quiet, false);
  assert.equal(rows[2].quiet, true, 'an empty optional role is quiet');
  assert.equal(rows[1].quiet, false, 'a filled optional role is not quiet');
});

test('a stale holder is explicit and must be cleared before another fill', () => {
  const row = Roster.rowsFrom({ roles: [role({ needs_clear: true })] })[0];
  assert.equal(row.state, 'empty');
  assert.equal(row.needsClear, true);
  assert.equal(row.tag.key, 'stale');
  assert.equal(row.tag.label, 'Needs clear');
});

test('an out-of-scope role is projected read-only with its reason', () => {
  const rows = Roster.rowsFrom({
    roles: [
      role({
        role_id: 'coordinator',
        scope: 'home',
        read_only: true,
        read_only_reason: 'Belongs to the group.'
      })
    ]
  });
  assert.equal(rows[0].readOnly, true);
  assert.equal(rows[0].readOnlyReason, 'Belongs to the group.');
});

test('the header count reports the projection, pluralized', () => {
  assert.equal(
    Roster.headerCount({
      roles: [filled(), role(), role({ role_id: 'a' }), role({ role_id: 'b' })]
    }),
    '1 of 4 roles filled'
  );
  assert.equal(Roster.headerCount({ roles: [role()] }), '0 of 1 role filled');
  assert.equal(Roster.headerCount({ roles: [] }), '0 of 0 roles filled');
});

// FR63: an agent in the workspace holding no declared role is listed, not
// dropped. A workspace created before this feature has all of its agents here.
test('the copy for unbound agents is shared, not invented per surface', () => {
  assert.equal(Roster.COPY.alsoHere, 'Also in this workspace');
  assert.equal(Roster.COPY.giveRole, 'Give a role…');
});

test('a missing or malformed projection renders no rows rather than throwing', () => {
  // Arrays built inside the vm sandbox belong to another realm, so length is
  // the assertion here rather than deep equality against a host [].
  assert.equal(Roster.rowsFrom(null).length, 0);
  assert.equal(Roster.rowsFrom({}).length, 0);
  assert.equal(Roster.rowsFrom({ roles: 'nope' }).length, 0);
});
