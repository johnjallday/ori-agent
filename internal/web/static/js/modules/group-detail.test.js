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
} from './group-detail.js';

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
        children: [{ id: 'w2', kind: 'workspace', name: 'Old' }],
      },
    ],
  },
  { id: 'w3', kind: 'workspace', name: 'Personal' },
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
  assert.deepEqual(directMembers(g).map((m) => m.id), ['w1', 'g2']);
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

test('metadataChanges flags only the fields that changed', () => {
  const group = { name: 'Clients', description: 'VIP', color: '#3b82f6' };

  assert.deepEqual(metadataChanges(group, { name: 'Clients', description: 'VIP', color: '#3b82f6' }), {
    nameChanged: false,
    metaChanged: false,
  });
  assert.deepEqual(metadataChanges(group, { name: 'Renamed', description: 'VIP', color: '#3b82f6' }), {
    nameChanged: true,
    metaChanged: false,
  });
  assert.deepEqual(metadataChanges(group, { name: 'Clients', description: 'New', color: '#3b82f6' }), {
    nameChanged: false,
    metaChanged: true,
  });
  assert.deepEqual(metadataChanges(group, { name: 'Clients', description: 'VIP', color: '' }), {
    nameChanged: false,
    metaChanged: true,
  });
  // Missing fields are treated as empty strings, not changes.
  assert.deepEqual(metadataChanges({ name: 'X' }, { name: 'X' }), {
    nameChanged: false,
    metaChanged: false,
  });
});
