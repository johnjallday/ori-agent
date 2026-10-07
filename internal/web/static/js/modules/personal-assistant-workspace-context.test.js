import test from 'node:test';
import assert from 'node:assert/strict';
import {
  collectWorkspaceContext,
  workspaceContextLabel,
  renderTurnWorkspace
} from './personal-assistant-workspace-context.js';

const page = {
  pathname: '/workspaces/album-1/assistant',
  workspaceId: 'uuid-a',
  workspaceSlug: 'album-1',
  selectionWorkspaceId: 'uuid-b'
};
test('page wins over Home selection and the route slug is not an ID', () => {
  const result = collectWorkspaceContext(page);
  assert.equal(result.context_version, 1);
  assert.equal(result.workspace_id, 'uuid-a');
  assert.equal(result.workspace_slug, 'album-1');
  assert.equal(result.selection_workspace_id, '');
  assert.equal(collectWorkspaceContext({ ...page, workspaceId: '' }).workspace_id, '');
});
test('in-page navigation/back-forward drops stale page IDs and app-wide selections', () => {
  const next = collectWorkspaceContext({ ...page, pathname: '/workspaces/second/canvas' });
  assert.equal(next.workspace_id, '');
  assert.equal(next.workspace_slug, 'second');
  assert.equal(next.surface, 'workspace_canvas');
  assert.equal(collectWorkspaceContext(page).workspace_id, 'uuid-a');
  const global = collectWorkspaceContext({ ...page, pathname: '/settings' });
  assert.equal(global.workspace_id, '');
  assert.equal(global.workspace_slug, '');
  assert.equal(global.selection_workspace_id, '');
});
test('Home has an explicit canonical selection; task routes use their own task only', () => {
  const home = collectWorkspaceContext({ ...page, pathname: '/', taskId: 'selected-task' });
  assert.equal(home.selection_workspace_id, 'uuid-b');
  assert.equal(home.task_id, 'selected-task');
  assert.equal(collectWorkspaceContext({ pathname: '/' }).workspace_id, '');
  const task = collectWorkspaceContext({
    ...page,
    pathname: '/workspaces/album-1/tasks/current-task',
    taskId: 'stale-task'
  });
  assert.equal(task.task_id, 'current-task');
});
test('display separates app-wide, group, Home, project, explicit subject and old unknown scope', () => {
  assert.equal(workspaceContextLabel({ version: 1, status: 'available' }), 'App-wide');
  assert.equal(workspaceContextLabel(null, { historical: true }), 'Earlier workspace unknown');
  assert.equal(
    workspaceContextLabel({ version: 1, status: 'denied' }),
    'Workspace context unavailable'
  );
  for (const [kind, label] of [
    ['home', 'Home'],
    ['group', 'Group'],
    ['project', 'Project']
  ]) {
    assert.match(
      workspaceContextLabel({ version: 1, status: 'available', subject: { name: 'Album', kind } }),
      new RegExp(label + ': Album')
    );
  }
  assert.equal(
    workspaceContextLabel({
      version: 1,
      status: 'available',
      subject: { id: 'b', name: 'B', kind: 'project' },
      parent: { name: 'Music' },
      location: { id: 'a', name: 'A' },
      selected_task_id: 'task'
    }),
    'Project: B · Music · Selected task · from A'
  );
});
test('a delayed/historical row uses its accepted attribution, never the current page', () => {
  const element = { dataset: {}, textContent: '' };
  const row = {
    ownerDocument: { createElement: () => element },
    querySelector: () => null,
    append: child => assert.equal(child, element)
  };
  renderTurnWorkspace(row, {
    version: 1,
    status: 'available',
    subject: { id: 'a', name: '<script>A</script>', kind: 'project' }
  });
  assert.equal(element.textContent, 'Project: <script>A</script>');
  assert.equal(element.dataset.workspaceId, 'a');
  assert.equal(element.innerHTML, undefined, 'names are text, never markup');
});
