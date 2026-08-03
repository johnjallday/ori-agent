import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function loadWorkspaceHubTasks() {
  const source = readFileSync(new URL('./workspace-hub-tasks.js', import.meta.url), 'utf8');
  const state = {
    tasks: [],
    taskHierarchy: null,
    selectedItems: { tasks: new Set() }
  };
  const window = {
    WorkspaceHubUtils: {
      formatDate: value => String(value || ''),
      buildTaskHierarchy: tasks => ({
        rootTasks: tasks.filter(task => !task.parent_task_id),
        subtasksByParent: new Map(),
        taskById: new Map(tasks.map(task => [task.id, task]))
      }),
      getDisplayStatus: task => task?.status || 'pending',
      getDisplayResult: () => ({ label: '', text: '' }),
      computeTaskStats: () => ({})
    },
    WorkspaceHubState: {
      getElements: () => ({}),
      getState: () => state
    },
    WorkspaceHubSelection: {
      isSelectionModeEnabled: () => false
    },
    setTimeout,
    clearTimeout
  };
  const context = {
    console,
    document: { getElementById: () => null },
    window,
    fetch: async () => ({ ok: true, json: async () => ({}) }),
    bootstrap: {},
    escapeHtml: value =>
      String(value ?? '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;')
  };
  vm.runInNewContext(source, context, { filename: 'workspace-hub-tasks.js' });
  return window.WorkspaceHubTasks.__test;
}

test('workspace hub task helpers count needs-review validation runs', () => {
  const helpers = loadWorkspaceHubTasks();
  const task = {
    execution_history: [
      { validation_result: { validation_status: 'needs_review' } },
      { validation: { validation_status: 'dismissed' } },
      { validation: { validation_status: 'needs_review' } }
    ]
  };

  assert.equal(helpers.countNeedsReviewRuns(task), 2);
  assert.match(helpers.renderNeedsReviewBadge(task), /Needs Review: 2/);
  assert.equal(helpers.renderNeedsReviewBadge({ execution_history: [] }), '');
});

test('workspace hub needs-attention filter includes review tasks and workflow parents', () => {
  const helpers = loadWorkspaceHubTasks();
  const tasks = [
    { id: 'parent', status: 'pending' },
    {
      id: 'child-review',
      parent_task_id: 'parent',
      status: 'completed',
      execution_history: [{ validation: { validation_status: 'needs_review' } }]
    },
    { id: 'blocked', status: 'waiting_for_choice' },
    { id: 'done', status: 'completed' }
  ];

  helpers.taskFilterState.status = 'needs_attention';
  helpers.taskFilterState.query = '';
  const filtered = helpers.applyTaskFilters(tasks);

  assert.deepEqual(
    filtered.map(task => task.id),
    ['parent', 'child-review', 'blocked']
  );
});
