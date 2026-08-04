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

test('setWorkspaceParentSelectState toggles disabled without aria-disabled', () => {
  const helpAttrs = {};
  const helpEl = {
    set textContent(value) {
      helpAttrs.text = value;
    }
  };
  const helpers = loadGroupOptions({
    getElementById: id => (id === 'folderParentHelp' ? helpEl : null)
  });

  const attrs = {};
  const select = {
    disabled: false,
    setAttribute: (name, value) => {
      attrs[name] = value;
    }
  };

  helpers.setWorkspaceParentSelectState(select, 0);
  assert.equal(select.disabled, true);
  assert.equal(attrs['aria-disabled'], undefined);
  assert.match(helpAttrs.text, /No groups yet/);

  helpers.setWorkspaceParentSelectState(select, 2);
  assert.equal(select.disabled, false);
  assert.equal(attrs['aria-disabled'], undefined);
  assert.match(helpAttrs.text, /Choose a group/);
});
