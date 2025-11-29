import { apiPost, apiPut } from './agent-canvas-api.js';

export async function executeTask(canvas, task) {
  if (!task || !task.id) {
    console.error('Invalid task:', task);
    return;
  }

  // If unassigned, prompt assignment first
  if (task.to === 'unassigned') {
    if (!canvas.agents || canvas.agents.length === 0) {
      alert('No agents available. Please add agents to the workspace first.');
      return;
    }
    let agentOptions = canvas.agents.map((a, i) => `${i + 1}. ${a.name}`).join('\n');
    const selection = prompt(`This task is unassigned. Select an agent to execute it:\n\n${agentOptions}\n\nEnter agent number (1-${canvas.agents.length}):`);
    if (!selection) return;
    const agentIndex = parseInt(selection) - 1;
    if (agentIndex < 0 || agentIndex >= canvas.agents.length) {
      alert('Invalid agent selection');
      return;
    }
    const selectedAgent = canvas.agents[agentIndex];
    await apiPut(`/api/orchestration/tasks/${task.id}`, {
      to: selectedAgent.name,
      status: 'pending'
    });
    task.to = selectedAgent.name;
  }

  const result = await apiPost('/api/orchestration/tasks/execute', { task_id: task.id });
  console.log('✅ Task execution started:', result);
  task.status = 'in_progress';
  canvas.draw();
  setTimeout(() => canvas.init(), 1000);
}

export async function rerunTask(canvas, task) {
  if (!task || !task.id) {
    console.error('Invalid task:', task);
    return;
  }
  const confirmMsg = task.status === 'failed'
    ? `Rerun this failed task?\n\n"${task.description || 'Task'}"\n\nThis will execute the task again.`
    : `Rerun this task?\n\n"${task.description || 'Task'}"\n\nThis will execute the task again with the same parameters.`;
  if (!confirm(confirmMsg)) return;

  await apiPut(`/api/orchestration/tasks/${task.id}`, {
    status: 'pending',
    result: null
  });

  task.status = 'pending';
  task.result = null;
  canvas.draw();

  const result = await apiPost('/api/orchestration/tasks/execute', { task_id: task.id });
  console.log('✅ Task rerun started:', result);
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
    const response = await apiPut(`/api/orchestration/tasks`, {
      task_id: combiner.id,
      description: combiner.description,
      to: combiner.to || '',
      from: combiner.from || '',
      input_task_ids: newInputs,
      result_combination_mode: combiner.result_combination_mode || combiner.resultCombinationMode || 'merge'
    });

    console.log('✅ Added task to combiner inputs:', response);

    // Update local state
    combiner.input_task_ids = newInputs;

    // Exit assignment mode
    canvas.assignmentMode = false;
    canvas.assignmentSourceTask = null;
    canvas.assignmentMouseX = 0;
    canvas.assignmentMouseY = 0;
    canvas.canvas.style.cursor = 'grab';

    // Redraw canvas
    canvas.draw();

    const combinerName = combiner.name || combiner.description || 'Merge';
    const sourceDesc = sourceTask.description?.substring(0, 30) || sourceTask.id;
    canvas.showNotification(`✅ Added "${sourceDesc}" as input to ${combinerName}`, 'success');
    console.log(`📊 Combiner now has ${newInputs.length} input task(s)`);
  } catch (error) {
    console.error('Failed to add task to combiner:', error);
    canvas.showNotification(`Failed to add input: ${error.message}`, 'error');
    canvas.assignmentMode = false;
    canvas.assignmentSourceTask = null;
    canvas.canvas.style.cursor = 'grab';
  }
}
