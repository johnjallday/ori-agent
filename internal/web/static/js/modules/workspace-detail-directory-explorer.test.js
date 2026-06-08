import { test } from 'node:test';
import assert from 'node:assert/strict';
import { describeDirectoryEntry } from './workspace-detail-directory-explorer.js';

test('describeDirectoryEntry marks a registered workspace folder as openable', () => {
  const node = { name: 'member-folder', isWorkspace: true, workspaceId: 'ws-123', workspaceName: 'Clients' };
  const d = describeDirectoryEntry(node);
  assert.equal(d.isWorkspace, true);
  assert.equal(d.openHref, '/workspaces/ws-123');
  // Prefers the registered workspace name over the folder name.
  assert.equal(d.label, 'Clients');
});

test('describeDirectoryEntry falls back to the folder name when unnamed', () => {
  const d = describeDirectoryEntry({ name: 'member-folder', isWorkspace: true, workspaceId: 'ws-9' });
  assert.equal(d.label, 'member-folder');
  assert.equal(d.openHref, '/workspaces/ws-9');
});

test('describeDirectoryEntry leaves ordinary folders/files untouched', () => {
  for (const node of [
    { name: 'docs' },
    { name: 'flagged-but-no-id', isWorkspace: true },
    { name: 'notes.txt', isWorkspace: false },
    null,
  ]) {
    const d = describeDirectoryEntry(node);
    assert.equal(d.isWorkspace, false);
    assert.equal(d.openHref, '');
  }
  assert.equal(describeDirectoryEntry({ name: 'docs' }).label, 'docs');
  assert.equal(describeDirectoryEntry(null).label, 'Untitled');
});

test('describeDirectoryEntry encodes the workspace id in the href', () => {
  const d = describeDirectoryEntry({ isWorkspace: true, workspaceId: 'a b/c', name: 'x' });
  assert.equal(d.openHref, '/workspaces/a%20b%2Fc');
});
