import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  escapeHtml,
  normalizeWorkspaceKind,
  isGroupNode,
  findWorkspaceNode,
  directMembers,
  formatMemberCount,
  metadataChanges,
  flattenTree,
  collectDescendantIds,
  eligibleAddTargets,
  isOpenTask,
  isScheduledTask,
  taskCreatedCutoff,
  taskMatchesFilters,
  sortTasksForRollup,
  isFileAttachment,
  extractFileItems
} from './workspace-detail-members.js';

const tree = [
  {
    id: 'g1',
    kind: 'group',
    name: 'Clients',
    children: [
      { id: 'w1', kind: 'workspace', name: 'Acme' },
      {
        id: 'g2',
        kind: 'group',
        name: 'Archive',
        children: [{ id: 'w2', kind: 'workspace', name: 'Old' }]
      }
    ]
  },
  { id: 'w3', kind: 'workspace', name: 'Personal' }
];

test('normalizeWorkspaceKind maps only "group" to group', () => {
  assert.equal(normalizeWorkspaceKind('group'), 'group');
  assert.equal(normalizeWorkspaceKind(' GROUP '), 'group');
  assert.equal(normalizeWorkspaceKind('workspace'), 'workspace');
  assert.equal(normalizeWorkspaceKind(undefined), 'workspace');
});

test('isGroupNode reflects the node kind', () => {
  assert.equal(isGroupNode({ kind: 'group' }), true);
  assert.equal(isGroupNode({ kind: 'workspace' }), false);
  assert.equal(isGroupNode(null), false);
});

test('findWorkspaceNode locates nodes at any depth', () => {
  assert.equal(findWorkspaceNode(tree, 'g1').name, 'Clients');
  assert.equal(findWorkspaceNode(tree, 'w2').name, 'Old');
  assert.equal(findWorkspaceNode(tree, 'g2').name, 'Archive');
  assert.equal(findWorkspaceNode(tree, 'missing'), null);
  assert.equal(findWorkspaceNode(tree, ''), null);
});

test('directMembers returns the group children', () => {
  const g = findWorkspaceNode(tree, 'g1');
  assert.deepEqual(
    directMembers(g).map(m => m.id),
    ['w1', 'g2']
  );
  assert.deepEqual(directMembers({}), []);
});

test('formatMemberCount pluralizes', () => {
  assert.equal(formatMemberCount(0), '0 members');
  assert.equal(formatMemberCount(1), '1 member');
  assert.equal(formatMemberCount(3), '3 members');
});

test('escapeHtml escapes markup', () => {
  assert.equal(escapeHtml('<b>&"\''), '&lt;b&gt;&amp;&quot;&#39;');
});

test('isOpenTask / isScheduledTask classify the roll-up defaults', () => {
  assert.equal(isOpenTask({ status: 'in_progress' }), true);
  assert.equal(isOpenTask({ status: 'IN_PROGRESS' }), true);
  assert.equal(isOpenTask({ status: 'completed' }), false);
  assert.equal(isScheduledTask({ schedule: { cron: '* * * * *' } }), true);
  assert.equal(isScheduledTask({ status: 'completed' }), false);
});

test('taskCreatedCutoff returns range boundaries', () => {
  const now = Date.parse('2026-06-08T12:00:00Z');
  assert.equal(taskCreatedCutoff('any', now), null);
  assert.equal(taskCreatedCutoff('7d', now), now - 7 * 86400000);
  assert.equal(taskCreatedCutoff('30d', now), now - 30 * 86400000);
  assert.ok(taskCreatedCutoff('today', now) <= now);
});

test('taskMatchesFilters applies status, member, and created-date filters', () => {
  const now = Date.parse('2026-06-08T12:00:00Z');
  // created_at relative to `now` so date-range assertions are timezone-robust.
  const open = { status: 'pending', __workspaceId: 'w1', created_at: new Date(now).toISOString() };
  const done = { status: 'completed', __workspaceId: 'w2', created_at: '2026-01-01T00:00:00Z' };
  const sched = {
    status: 'completed',
    schedule: { cron: '0 9 * * *' },
    __workspaceId: 'w1',
    created_at: new Date(now).toISOString()
  };

  // Default = open + scheduled.
  assert.equal(taskMatchesFilters(open, { status: 'default' }, now), true);
  assert.equal(taskMatchesFilters(sched, { status: 'default' }, now), true);
  assert.equal(taskMatchesFilters(done, { status: 'default' }, now), false);

  // Explicit status.
  assert.equal(taskMatchesFilters(done, { status: 'completed' }, now), true);
  assert.equal(taskMatchesFilters(open, { status: 'completed' }, now), false);
  assert.equal(taskMatchesFilters(done, { status: 'all' }, now), true);

  // Member filter.
  assert.equal(taskMatchesFilters(open, { status: 'all', member: 'w1' }, now), true);
  assert.equal(taskMatchesFilters(open, { status: 'all', member: 'w2' }, now), false);

  // Created-date filter.
  assert.equal(taskMatchesFilters(open, { status: 'all', dateRange: 'today' }, now), true);
  assert.equal(taskMatchesFilters(done, { status: 'all', dateRange: '30d' }, now), false);
});

test('sortTasksForRollup orders newest-first by created_at', () => {
  const tasks = [
    { id: 'a', created_at: '2026-01-01T00:00:00Z' },
    { id: 'b', created_at: '2026-06-01T00:00:00Z' },
    { id: 'c', created_at: '2026-03-01T00:00:00Z' }
  ];
  assert.deepEqual(
    sortTasksForRollup(tasks).map(t => t.id),
    ['b', 'c', 'a']
  );
});

test('isFileAttachment / extractFileItems pick file attachments', () => {
  const attachments = [
    { id: 'n1', title: 'A note', type: 'note' },
    {
      id: 'f1',
      title: 'Spec',
      type: 'doc',
      file_meta: { name: 'spec.pdf', url: '/files/spec.pdf' }
    },
    { id: 'f2', type: 'image', file_meta: { name: 'pic.png' } },
    { id: 'l1', title: 'Link', type: 'link', link_url: 'https://example.com' }
  ];
  assert.equal(isFileAttachment(attachments[1]), true);
  assert.equal(isFileAttachment(attachments[0]), false);
  assert.equal(isFileAttachment(null), false);

  const files = extractFileItems(attachments);
  assert.deepEqual(files, [
    { id: 'f1', title: 'Spec', url: '/files/spec.pdf' },
    { id: 'f2', title: 'pic.png', url: '' }
  ]);
});

test('flattenTree and collectDescendantIds walk the hierarchy', () => {
  assert.deepEqual(
    flattenTree(tree).map(n => n.id),
    ['g1', 'w1', 'g2', 'w2', 'w3']
  );
  const g1 = findWorkspaceNode(tree, 'g1');
  assert.deepEqual([...collectDescendantIds(g1)].sort(), ['g2', 'w1', 'w2']);
  assert.deepEqual([...collectDescendantIds({ id: 'leaf' })], []);
});

test('eligibleAddTargets excludes the group, its descendants, and direct members', () => {
  const g1 = findWorkspaceNode(tree, 'g1');
  // g1 contains w1 + g2(>w2); eligible = everything else (w3 only).
  assert.deepEqual(
    eligibleAddTargets(tree, g1).map(n => n.id),
    ['w3']
  );

  const g2 = findWorkspaceNode(tree, 'g2');
  // g2 contains w2; eligible = g1, w1, w3 (g2 and w2 excluded).
  assert.deepEqual(
    eligibleAddTargets(tree, g2).map(n => n.id),
    ['g1', 'w1', 'w3']
  );

  assert.deepEqual(eligibleAddTargets(tree, null), []);
});

test('metadataChanges flags only the fields that changed', () => {
  const group = { name: 'Clients', description: 'VIP', color: '#3b82f6' };

  assert.deepEqual(
    metadataChanges(group, { name: 'Clients', description: 'VIP', color: '#3b82f6' }),
    {
      nameChanged: false,
      metaChanged: false
    }
  );
  assert.deepEqual(
    metadataChanges(group, { name: 'Renamed', description: 'VIP', color: '#3b82f6' }),
    {
      nameChanged: true,
      metaChanged: false
    }
  );
  assert.deepEqual(
    metadataChanges(group, { name: 'Clients', description: 'New', color: '#3b82f6' }),
    {
      nameChanged: false,
      metaChanged: true
    }
  );
  assert.deepEqual(metadataChanges(group, { name: 'Clients', description: 'VIP', color: '' }), {
    nameChanged: false,
    metaChanged: true
  });
  // Missing fields are treated as empty strings, not changes.
  assert.deepEqual(metadataChanges({ name: 'X' }, { name: 'X' }), {
    nameChanged: false,
    metaChanged: false
  });
});
