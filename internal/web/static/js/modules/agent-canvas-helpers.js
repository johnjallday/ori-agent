/**
 * Helpers for AgentCanvas shared logic.
 */

/**
 * Deduplicate tasks by id, keeping first occurrence.
 * @param {Array} tasks
 * @returns {Array}
 */
export function dedupeTasksById(tasks) {
  if (!Array.isArray(tasks)) return [];
  const seen = new Set();
  return tasks.filter(task => {
    if (!task || !task.id) return false;
    if (seen.has(task.id)) return false;
    seen.add(task.id);
    return true;
  });
}

/**
 * Remove the combiner's own task from a list of tasks.
 * @param {Array} tasks
 * @param {string} combinerTaskId
 * @returns {Array}
 */
export function removeSelfTask(tasks, combinerTaskId) {
  if (!Array.isArray(tasks)) return [];
  return tasks.filter(task => task && task.id !== combinerTaskId);
}

/**
 * Build a combination instruction that includes available results or descriptions.
 * @param {Array} tasks
 * @returns {string}
 */
export function buildCombinationInstruction(tasks) {
  if (!Array.isArray(tasks) || tasks.length === 0) return '';

  const lines = tasks.map(task => {
    if (task?.result) {
      return `Task ${task.id}: "${task.description || 'task'}" -> Result: ${task.result}`;
    }
    return `Task ${task?.id || 'unknown'}: "${task?.description || 'task'}" (no result yet, use the prompt/description)`;
  });

  return `Combine the following inputs (use description when result is missing):\n- ${lines.join('\n- ')}`;
}
