/**
 * quick-task.js — create a task, and start it, without a workspace page
 * (tasks/prd-task-run-show.md FR49, FR50).
 *
 * These are the two server calls the Operations map's New Quest composer makes
 * through WorkspaceDetailPage.createTask and executeTask. The page methods wrap
 * them with page-only work — reloading the task list, toasts, the legacy
 * execution modal, and advisory "switch agent?" dialogs — and call these for the
 * request itself, so the Home map's "Give a task…" composer sends exactly the
 * same request without a page.
 *
 * Nothing here decides who does the work: a task created with no assignee is
 * stamped onto the workspace's entry agent by the server (entry_agent_default),
 * and every check that protects a run — the capability gate, a missing agent —
 * is enforced by the execute endpoint itself.
 *
 * @module quick-task
 */

async function readError(response, fallback) {
  try {
    const text = String(await response.text()).trim();
    if (!text) return fallback;
    try {
      const body = JSON.parse(text);
      return String(body.message || body.error || fallback);
    } catch {
      return text;
    }
  } catch {
    return fallback;
  }
}

/**
 * The request body POST /api/orchestration/tasks takes for a new task. Shared
 * by the page and by callers without one.
 */
export function taskCreateBody(workspaceId, description, options = {}) {
  return {
    workspace_id: String(workspaceId || '').trim(),
    description: String(description || '').trim(), // the task API's main field
    details: String(options.details || '').trim(),
    status: 'pending',
    to: String(options.assignee || '').trim() || undefined,
    assigned_node_id: String(options.assignedNodeId || '').trim() || undefined,
    input_task_ids: Array.isArray(options.inputTaskIDs)
      ? options.inputTaskIDs.filter(Boolean)
      : undefined,
    parent_task_id: String(options.parentTaskID || '').trim() || undefined,
    subtask_index: Number.isFinite(Number(options.subtaskIndex))
      ? Number(options.subtaskIndex)
      : undefined,
    // Runtime capabilities the task needs; the executing agent is granted
    // matching runtime tools only when the task declares them.
    required_capabilities:
      Array.isArray(options.requiredCapabilities) && options.requiredCapabilities.length
        ? options.requiredCapabilities
        : undefined
  };
}

/**
 * Create a task. Resolves to the created task; rejects with an Error whose
 * message is safe to show.
 */
export async function createTask(workspaceId, description, options = {}) {
  const body = taskCreateBody(workspaceId, description, options);
  if (!body.workspace_id || !body.description) {
    throw new Error('Describe the task first.');
  }
  const response = await fetch('/api/orchestration/tasks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  if (!response.ok) {
    throw new Error(await readError(response, 'Could not create the task.'));
  }
  const data = await response.json();
  const task = (data && data.task) || data;
  if (!task || !task.id) throw new Error('Could not create the task.');
  return task;
}

/**
 * Start a task through the manual execute endpoint. Rejects with an Error whose
 * message is safe to show.
 */
export async function startTask(taskId, options = {}) {
  const id = String(taskId || '').trim();
  if (!id) throw new Error('There is no task to start.');
  const response = await fetch('/api/orchestration/tasks/execute', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      task_id: id,
      step_action: options.stepAction || undefined,
      execution_mode: options.executionMode || undefined
    })
  });
  if (!response.ok) {
    throw new Error(await readError(response, 'The task was created but could not start.'));
  }
  return true;
}

// The Home map is a classic script and cannot import a module.
if (typeof window !== 'undefined') {
  window.OriQuickTask = Object.assign(window.OriQuickTask || {}, {
    createTask,
    startTask,
    taskCreateBody
  });
}
