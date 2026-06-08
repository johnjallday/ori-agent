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

function loadWorkspaceCreate() {
  const source = readFileSync(new URL('./workspace-create.js', import.meta.url), 'utf8');
  const window = {
    addEventListener: () => {},
    availableAgents: [],
    selectedAgents: new Set()
  };
  const document = {
    addEventListener: () => {},
    getElementById: () => null,
    querySelector: () => null,
    querySelectorAll: () => []
  };
  const context = {
    bootstrap: {},
    console,
    document,
    escapeHtml,
    requestAnimationFrame: () => {},
    window
  };

  // workspace-create.js delegates to the shared WorkspaceGroupOptions module,
  // so load it into the same context first.
  const sharedSource = readFileSync(new URL('./workspace-group-options.js', import.meta.url), 'utf8');
  vm.runInNewContext(sharedSource, context, { filename: 'workspace-group-options.js' });

  vm.runInNewContext(source, context, { filename: 'workspace-create.js' });
  return window.WorkspaceCreate.__test;
}

test('workspace create group options include only organization groups with depth', () => {
  const helpers = loadWorkspaceCreate();
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
          children: [
            { id: 'workspace-2', kind: 'workspace', name: 'Old Work' }
          ]
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

test('workspace create group options render safe select options', () => {
  const helpers = loadWorkspaceCreate();
  const html = helpers.renderWorkspaceParentOptions([
    { id: 'group-1', name: 'Clients', depth: 0 },
    { id: 'group-2', name: 'Nested & Saved', depth: 1 }
  ]);

  assert.match(html, /<option value="">No group<\/option>/);
  assert.match(html, /<option value="group-1">Clients<\/option>/);
  assert.match(html, /<option value="group-2">-- Nested &amp; Saved<\/option>/);
});
