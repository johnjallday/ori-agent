import { apiPost, apiPut } from './agent-canvas-api.js';
import { showCanvasConfirm, showCanvasAgentPicker } from './agent-canvas-dialogs.js';

export async function executeTask(canvas, task) {
  if (!task || !task.id) {
    console.error('Invalid task:', task);
    return;
  }

  // If unassigned, prompt assignment first
  if (task.to === 'unassigned') {
    if (!canvas.agents || canvas.agents.length === 0) {
      canvas.showNotification(
        'No agents in this workspace yet — add an agent before running tasks.',
        'error'
      );
      return;
    }
    const selectedAgent = await showCanvasAgentPicker({
      title: 'Assign an agent',
      message: `This task is unassigned. Pick an agent to run "${task.description || task.id}".`,
      agents: canvas.agents
    });
    if (!selectedAgent) return;
    await apiPut(`/api/orchestration/tasks/${task.id}`, {
      to: selectedAgent.name,
      status: 'pending'
    });
    task.to = selectedAgent.name;
  }

  await apiPost('/api/orchestration/tasks/execute', { task_id: task.id });
  task.status = 'in_progress';
  canvas.draw();
  setTimeout(() => canvas.init(), 1000);
}

export async function rerunTask(canvas, task) {
  if (!task || !task.id) {
    console.error('Invalid task:', task);
    return;
  }
  const label = task.description || 'this task';
  const confirmed = await showCanvasConfirm({
    title: task.status === 'failed' ? 'Rerun failed task?' : 'Rerun task?',
    message:
      task.status === 'failed'
        ? `"${label}" will execute again from scratch.`
        : `"${label}" will execute again with the same parameters.`,
    confirmLabel: 'Rerun',
    cancelLabel: 'Cancel'
  });
  if (!confirmed) return;

  // Update local task state
  task.status = 'pending';
  task.result = null;
  canvas.draw();

  // Execute the task (backend will handle status reset)
  await apiPost('/api/orchestration/tasks/execute', { task_id: task.id });
  task.status = 'in_progress';
  canvas.draw();
  canvas.showNotification(`Task "${task.description || task.id}" is being rerun`, 'success');
  setTimeout(() => canvas.init(), 1000);
}

export async function assignTaskToCombiner(canvas, combiner) {
  if (!combiner || !canvas.assignmentSourceTask) return;

  const sourceTask = canvas.assignmentSourceTask;

  // Check if source task is already in combiner's inputs
  const currentInputs = combiner.input_task_ids || [];
  if (currentInputs.includes(sourceTask.id)) {
    canvas.showNotification('This task is already an input to the combiner', 'warning');
    canvas.assignmentMode = false;
    canvas.assignmentSourceTask = null;
    canvas.canvas.style.cursor = 'grab';
    return;
  }

  // Add source task to combiner's input_task_ids array
  const newInputs = [...currentInputs, sourceTask.id];

  try {
    // Update the combiner task to include the new input
    await apiPut('/api/orchestration/tasks', {
      task_id: combiner.id,
      description: combiner.description,
      to: combiner.to || '',
      from: combiner.from || '',
      input_task_ids: newInputs,
      result_combination_mode:
        combiner.result_combination_mode || combiner.resultCombinationMode || 'merge'
    });

    // Update local state
    combiner.input_task_ids = newInputs;

    // Exit assignment mode
    canvas.assignmentMode = false;
    canvas.assignmentSourceTask = null;
    canvas.assignmentMouseX = 0;
    canvas.assignmentMouseY = 0;
    canvas.canvas.style.cursor = 'grab';

    // Immediate redraw
    canvas.draw();

    // Force another redraw after a short delay to ensure connections are visible
    setTimeout(() => {
      canvas.draw();
    }, 100);

    const combinerName = combiner.name || combiner.description || 'Merge';
    const sourceDesc = sourceTask.description?.substring(0, 30) || sourceTask.id;
    canvas.showNotification(`Added "${sourceDesc}" as input to ${combinerName}`, 'success');
  } catch (error) {
    console.error('Failed to add task to combiner:', error);
    canvas.showNotification(`Failed to add input: ${error.message}`, 'error');
    canvas.assignmentMode = false;
    canvas.assignmentSourceTask = null;
    canvas.canvas.style.cursor = 'grab';
  }
}

/**
 * Link a source task's result to a target task by adding it to input_task_ids.
 */
export async function linkTaskResult(canvas, sourceTaskId, targetTaskId) {
  if (!sourceTaskId || !targetTaskId || sourceTaskId === targetTaskId) return;

  const targetTask = canvas.tasks.find(t => t.id === targetTaskId);
  if (!targetTask) {
    canvas.showNotification('Target task not found', 'error');
    return;
  }

  const currentInputs = Array.isArray(targetTask.input_task_ids)
    ? [...targetTask.input_task_ids]
    : [];
  if (currentInputs.includes(sourceTaskId)) {
    canvas.showNotification('This task is already connected as an input', 'info');
    return;
  }

  const newInputs = [...currentInputs, sourceTaskId];

  try {
    await apiPut('/api/orchestration/tasks', {
      task_id: targetTaskId,
      input_task_ids: newInputs
    });

    targetTask.input_task_ids = newInputs;
    canvas.draw();
    canvas.showNotification('Linked task result to target task', 'success');
  } catch (error) {
    console.error('Failed to link task result:', error);
    canvas.showNotification('Failed to link tasks: ' + error.message, 'error');
  }
}
