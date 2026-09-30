import { test } from 'node:test';
import assert from 'node:assert/strict';

// Import the real page controllers without mounting them or issuing requests.
globalThis.window = {};
globalThis.document = { getElementById: () => null };
const { WorkspaceDetailPage } = await import('./workspace-detail.js');
const { WorkspaceTaskPage, getStatusClass } = await import('./workspace-task.js');
const { resolveTaskPresentation } = await import('./task-presentation.js');

function makePages() {
  return {
    detail: new WorkspaceDetailPage('ws-fixture', 'fixture'),
    taskPage: new WorkspaceTaskPage('ws-fixture', 'task-fixture', 'fixture')
  };
}

const fixtures = [
  { task: { status: 'pending' }, className: 'pending' },
  { task: { status: 'pending', to: 'Ori' }, className: 'pending' },
  { task: { status: 'assigned', to: 'Ori' }, className: 'pending' },
  { task: { status: 'in_progress', to: 'Ori' }, className: 'in_progress' },
  { task: { status: 'blocked' }, className: 'blocked' },
  { task: { status: 'waiting_for_choice' }, className: 'blocked' },
  { task: { status: 'failed' }, className: 'failed' },
  { task: { status: 'error' }, className: 'failed' },
  { task: { status: 'timeout' }, className: 'failed' },
  { task: { status: 'completed' }, className: 'completed' },
  { task: { status: 'success' }, className: 'completed' },
  { task: { status: 'done' }, className: 'completed' },
  { task: { status: 'cancelled' }, className: 'cancelled' },
  { task: { status: 'skipped' }, className: 'cancelled' },
  { task: { status: 'future_status' }, className: 'unknown' },
  { task: { status: '__proto__' }, className: 'unknown' },
  { task: { status: 'constructor' }, className: 'unknown' },
  { task: { status: ' COMPLETED ' }, className: 'completed' },
  {
    task: {
      status: 'in_progress',
      context: { human_loop: { state: 'blocked', reason: 'Fix it' } }
    },
    className: 'blocked'
  },
  {
    task: { status: 'blocked', context: { human_loop: { state: 'waiting_for_choice' } } },
    className: 'blocked'
  }
];

for (const { task, className } of fixtures) {
  test(`task pages use the canonical status for ${JSON.stringify(task)}`, () => {
    const { detail, taskPage } = makePages();
    const expected = resolveTaskPresentation(task);
    const detailStatus = detail.getTaskStatusPresentation(task);
    const pageStatus = taskPage.getTaskStatusPresentation(task);
    assert.equal(detailStatus.label, expected.label);
    assert.equal(pageStatus.label, expected.label);
    assert.equal(detailStatus.className, className);
    assert.equal(pageStatus.className, className);
    assert.deepEqual(detailStatus, pageStatus);
  });
}

for (const status of ['completed', 'success', 'failed', 'timeout', 'cancelled', 'skipped']) {
  test(`${status} outranks leftover human-loop and step-waiting context on both pages`, () => {
    const { detail, taskPage } = makePages();
    const task = {
      status,
      execution_mode: 'step_through',
      context: {
        execution_step_waiting: true,
        human_loop: { state: 'waiting_for_choice', reason: 'An earlier request' }
      }
    };
    for (const page of [detail, taskPage]) {
      const presentation = page.getTaskStatusPresentation(task);
      assert.equal(presentation.label, resolveTaskPresentation(task).label);
      assert.equal(presentation.isBlocked, false);
      assert.equal(presentation.reason, '', 'do not expose an obsolete intervention reason');
    }
    assert.equal(detail.getTaskExecutionState(task), status);
  });
}

test('reason text alone does not override the task state', () => {
  const { detail, taskPage } = makePages();
  const task = {
    status: 'in_progress',
    context: { human_loop: { reason: 'A previous attempt', question: 'Old question' } }
  };
  for (const page of [detail, taskPage]) {
    const presentation = page.getTaskStatusPresentation(task);
    assert.equal(presentation.label, 'Running');
    assert.equal(presentation.isBlocked, false);
    assert.equal(presentation.reason, '');
  }
});

test('a step-through pause keeps Next Step available without opening a guidance form', () => {
  const { detail, taskPage } = makePages();
  const task = {
    status: 'in_progress',
    execution_mode: 'step_through',
    context: { execution_step_waiting: true }
  };
  assert.equal(detail.isTaskAwaitingNextStep(task), true);
  for (const page of [detail, taskPage]) {
    const presentation = page.getTaskStatusPresentation(task);
    assert.equal(presentation.label, resolveTaskPresentation(task).label);
    assert.equal(presentation.isBlocked, false);
  }
});

test('a human-loop request takes precedence over a step-through pause', () => {
  const { detail, taskPage } = makePages();
  const task = {
    status: 'in_progress',
    execution_mode: 'step_through',
    context: { execution_step_waiting: true, human_loop: { state: 'waiting_for_choice' } }
  };
  assert.equal(detail.isTaskAwaitingNextStep(task), false);
  for (const page of [detail, taskPage]) {
    assert.equal(page.getTaskStatusPresentation(task).isBlocked, true);
  }
});

test('unknown raw states never receive pending styling', () => {
  assert.equal(getStatusClass('future_status'), 'unknown');
});

test('the execution modal uses the same label as the task row', () => {
  const { detail } = makePages();
  detail.elements.taskExecutionStatus = {};
  for (const { task } of fixtures) {
    detail.setExecutionModalStatus(task);
    const presentation = detail.getTaskStatusPresentation(task);
    assert.equal(detail.elements.taskExecutionStatus.textContent, presentation.label);
    assert.equal(
      detail.elements.taskExecutionStatus.className,
      `workspace-detail-task-status ${presentation.className}`
    );
  }
});

test('realtime input and step-pause events keep the execution modal in sync', () => {
  const { detail } = makePages();
  detail.currentExecutionTaskId = 'task-fixture';
  detail.elements.taskExecutionStatus = {};
  detail.appendExecutionLog = () => {};
  detail.stopExecutionMonitor = () => {};
  detail.setExecutionViewResultEnabled = () => {};
  const events = [
    {
      type: 'task.blocked',
      data: {
        status: 'waiting_for_choice',
        human_loop: { state: 'waiting_for_choice' }
      },
      task: {
        status: 'waiting_for_choice',
        context: { human_loop: { state: 'waiting_for_choice' } }
      }
    },
    {
      type: 'task.progress',
      data: { waiting_for_next_step: true },
      task: { status: 'in_progress', context: { execution_step_waiting: true } }
    },
    {
      type: 'task.progress',
      data: { waiting_for_next_step: false },
      task: { status: 'in_progress' }
    }
  ];
  for (const event of events) {
    detail.handleTaskExecutionRealtimeEvent({
      type: event.type,
      data: { ...event.data, task_id: 'task-fixture' }
    });
    assert.equal(
      detail.elements.taskExecutionStatus.textContent,
      resolveTaskPresentation(event.task).label
    );
  }
});

test('the task hero renders the canonical label and CSS bucket', () => {
  const { taskPage } = makePages();
  taskPage.elements.status = { dataset: {} };
  taskPage.syncTaskTagsWidget = () => {};
  taskPage.renderLiveBadge = () => {};
  const task = { id: 'task-fixture', status: 'skipped' };
  taskPage.task = task;
  taskPage.renderHero(taskPage.getTaskStatusPresentation());
  assert.equal(taskPage.elements.status.textContent, 'Skipped');
  assert.equal(taskPage.elements.status.dataset.state, 'cancelled');
});

test('task rows render canonical labels, including safe unknown-state text', () => {
  const { detail } = makePages();
  detail.escapeHtml = value => String(value);
  for (const { task, className } of fixtures) {
    const html = detail.renderTaskItem({ ...task, id: 'task-fixture', name: 'Fixture' });
    assert.ok(
      html.includes(
        `<span class="workspace-detail-task-status ${className}">${resolveTaskPresentation(task).label}</span>`
      )
    );
  }
  const unknown = '<img src=x onerror=alert(1)>';
  const html = detail.renderTaskItem({ id: 'task-fixture', status: unknown });
  assert.match(html, /workspace-detail-task-status unknown">Unknown<\/span>/);
  assert.ok(!html.includes(unknown));
});

test('relationship graph titles use canonical labels for neighboring tasks', () => {
  const { taskPage } = makePages();
  taskPage.escapeHtml = value => String(value);
  taskPage.task = { id: 'task-fixture', name: 'Current', status: 'in_progress' };
  const parentTask = {
    id: 'parent',
    name: 'Parent',
    status: 'in_progress',
    context: { human_loop: { state: 'blocked' } }
  };
  const html = taskPage.renderRelationshipsGraph({ parentTask });
  assert.match(html, /<title>Parent · Blocked<\/title>/);
  assert.match(html, /<title>Current · Running<\/title>/);
});

test('the execution breakdown keeps timeout distinct from failed', () => {
  const { taskPage } = makePages();
  taskPage.elements.trace = {};
  taskPage.escapeHtml = value => String(value);
  taskPage.getSubtasks = () => [];
  taskPage.getExecutionBreakdownSteps = () => [{ status: 'timeout', title: 'Run 1' }];
  taskPage.renderExecutionBreakdown();
  assert.match(taskPage.elements.trace.innerHTML, /Timed Out/);
  assert.doesNotMatch(taskPage.elements.trace.innerHTML, />Failed</);
});
