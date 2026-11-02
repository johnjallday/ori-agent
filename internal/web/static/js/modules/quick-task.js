/**
 * quick-task.js: the request body for the kept POST /api/orchestration/tasks route.
 *
 * People give work through give-work.js, which creates a ticket. The remaining
 * callers are result follow-ups that link a new row to its source; a row with no
 * assignee is stamped onto the entry agent by the server (entry_agent_default).
 *
 * @module quick-task
 */
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
