import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function loadGroupOptions(documentOverrides = {}) {
  const window = {};
  const document = {
    getElementById: () => null,
    ...documentOverrides
  };
  const context = { console, document, escapeHtml, window };

  const source = readFileSync(new URL('./workspace-group-options.js', import.meta.url), 'utf8');
  vm.runInNewContext(source, context, { filename: 'workspace-group-options.js' });
  return window.WorkspaceGroupOptions;
}

test('collectWorkspaceGroupOptions returns only groups with nesting depth', () => {
  const helpers = loadGroupOptions();
  const groups = helpers.collectWorkspaceGroupOptions([
    {
      id: 'group-1',
      kind: 'group',
      name: 'Clients',
      children: [
        { id: 'workspace-1', kind: 'workspace', name: 'Client A' },
        {
          id: 'group-2',
          kind: 'group',
          name: 'Archive',
          children: [{ id: 'workspace-2', kind: 'workspace', name: 'Old Work' }]
        }
      ]
    },
    { id: 'workspace-3', kind: 'workspace', name: 'Personal' }
  ]);

  assert.deepEqual(JSON.parse(JSON.stringify(groups)), [
    { id: 'group-1', name: 'Clients', depth: 0 },
    { id: 'group-2', name: 'Archive', depth: 1 }
  ]);
});

test('renderWorkspaceParentOptions escapes names and indents nested groups', () => {
  const helpers = loadGroupOptions();
  const html = helpers.renderWorkspaceParentOptions([
    { id: 'group-1', name: 'Clients', depth: 0 },
    { id: 'group-2', name: 'Nested & Saved', depth: 1 }
  ]);

  assert.match(html, /<option value="">No group<\/option>/);
  assert.match(html, /<option value="group-1">Clients<\/option>/);
  assert.match(html, /<option value="group-2">-- Nested &amp; Saved<\/option>/);
});

test('workspaceParentSelectState returns values without touching the control', () => {
  const lookups = [];
  const helpers = loadGroupOptions({
    getElementById: id => {
      lookups.push(id);
      return null;
    }
  });

  const empty = helpers.workspaceParentSelectState(0);
  assert.equal(empty.disabled, true);
  assert.match(empty.help, /No groups yet/);
  assert.equal('aria-disabled' in empty, false);

  const available = helpers.workspaceParentSelectState(2);
  assert.equal(available.disabled, false);
  assert.equal(available.help, '');
  assert.deepEqual(lookups, [], 'the owner in sessions.js applies the result');
});
