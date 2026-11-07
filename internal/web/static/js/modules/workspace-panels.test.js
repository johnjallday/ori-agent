import test from 'node:test';
import assert from 'node:assert/strict';
import { WorkspacePanels } from './workspace-panels.js';
import { OverlayCoordinator } from './workspace-overlay-coordinator.js';

function panel() {
  const command = {
    workspaceId: () => 'w',
    workspaceSlug: () => 'w',
    page: {
      workspace: { name: '<untrusted>', description: 'Purpose' },
      initialListsLoaded: true,
      notes: [],
      sessions: []
    },
    workspaceTags: () => ['one'],
    toolsTabs: () => [],
    workflowHref: () => '/workflows',
    workspaceRoute: suffix => '/workspaces/w' + suffix,
    folderRowData: () => [],
    buildSummaryHTML: () => '',
    groupTemplateStatusHTML: () => ''
  };
  const panels = new WorkspacePanels(command, new OverlayCoordinator());
  panels.id = 'w';
  return { panels, command };
}

test('settings fields use separate real saves and render untrusted identity as literal text', () => {
  const { panels } = panel();
  panels.kind = 'settings';
  panels.section = 'general';
  const html = panels.content();
  assert.match(html, /&lt;untrusted&gt;/);
  assert.doesNotMatch(html, /<untrusted>/);
  for (const field of ['name', 'description', 'tags'])
    assert.match(html, new RegExp('data-panel-field="' + field + '"'));
  assert.match(html, /Each field saves separately/);
});

test('configuration and resource navigation dispatch to existing owners, never a creator API', () => {
  const { panels, command } = panel();
  const calls = [];
  command.openStatModal = (...args) => calls.push(args);
  panels.run('owner', 'memory', 'opener');
  panels.run('tools', 'skills', 'opener');
  assert.deepEqual(calls, [
    ['memory', 'opener'],
    ['tools', 'opener']
  ]);
  assert.equal(command.activeToolsTab, 'skills');
  command.page.openWorkspaceFilesExplorer = () => calls.push('files');
  command.page.showFileModal = () => calls.push('upload');
  panels.run('files');
  panels.run('upload');
  assert.deepEqual(calls.slice(-2), ['files', 'upload']);
});

test('a failed or loading resource source is never presented as an empty collection', () => {
  const { panels, command } = panel();
  panels.kind = 'resources';
  for (const [section, key] of [
    ['notes', 'notes'],
    ['sessions', 'sessions'],
    ['files', 'directories']
  ]) {
    panels.section = section;
    command.page[key + 'LoadFailed'] = true;
    assert.match(panels.content(), /not yet available/);
    assert.doesNotMatch(panels.content(), /No (notes|conversations|linked folders) yet/);
    command.page[key + 'LoadFailed'] = false;
    command.page[key + 'Loading'] = true;
    assert.match(panels.content(), /not yet available/);
  }
});

test('failed field save retains the typed draft and busy guard; successful save clears dirtiness', async () => {
  const { panels, command } = panel();
  const button = { disabled: false };
  const form = {
    getAttribute: () => 'description',
    elements: { value: { value: 'Typed draft' } },
    querySelector: () => button
  };
  const messages = [];
  panels.status = value => messages.push(value);
  panels.dirty = true;
  command.page.saveWorkspaceIdentityField = async () => ({ error: true });
  await panels.save(form);
  assert.equal(panels.dirty, true);
  assert.equal(form.elements.value.value, 'Typed draft');
  assert.equal(button.disabled, false);
  command.page.saveWorkspaceIdentityField = async (...args) => {
    assert.deepEqual(args, ['description', 'Typed draft', { currentValue: 'Purpose' }]);
    return { changed: true };
  };
  await panels.save(form);
  assert.equal(panels.dirty, false);
  assert.equal(messages.at(-1), 'Saved.');
});

test('workspace identity changes retire the old draft instead of reusing its save target', async () => {
  const { panels, command } = panel();
  panels.kind = 'settings';
  panels.dirty = true;
  command.workspaceId = () => 'other';
  panels.syncIdentity();
  assert.equal(panels.id, '');
  assert.equal(panels.dirty, false);
  let writes = 0;
  command.page.saveWorkspaceIdentityField = () => {
    writes++;
  };
  await panels.save({});
  assert.equal(writes, 0);
});
