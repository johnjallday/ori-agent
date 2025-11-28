import { apiGet, apiPost, apiPut } from './agent-canvas-api.js';
import { dedupeTasksById, removeSelfTask, buildCombinationInstruction } from './agent-canvas-helpers.js';

/**
 * Resolve combiner inputs from connections, supporting task and agent sources.
 */
export function resolveCombinerInputs(canvas, combiner) {
  const inputConnections = canvas.connections.filter(c => c.to === combiner.id);
  const inputTasks = [];
  const missingAgentInputs = [];

  for (const conn of inputConnections) {
    const nodeData = canvas.getNodeById(conn.from);
    if (nodeData && nodeData.type === 'task') {
      inputTasks.push(nodeData.node);
    } else if (nodeData && nodeData.type === 'agent') {
      const latestAgentTask = canvas.getLatestTaskForAgent(nodeData.node.name);
      if (latestAgentTask) {
        inputTasks.push(latestAgentTask);
      } else {
        missingAgentInputs.push(nodeData.node.name);
      }
    }
  }

  // Deduplicate and drop self-reference
  const tasks = removeSelfTask(dedupeTasksById(inputTasks), combiner.taskId);
  return { tasks, missingAgentInputs };
}

/**
 * Ensure combiner has backend task; fetch if present, recreate if missing.
 */
export async function ensureCombinerTask(canvas, combiner) {
  if (!combiner) return null;

  if (combiner.taskId && canvas.tasks && canvas.tasks.some(t => t.id === combiner.taskId)) {
    return combiner.taskId;
  }

  if (combiner.taskId) {
    try {
      const data = await apiGet(`/api/orchestration/tasks?id=${encodeURIComponent(combiner.taskId)}`);
      if (data && data.id) {
        canvas.tasks = canvas.tasks || [];
        const existing = canvas.tasks.find(t => t.id === data.id);
        if (!existing) {
          canvas.tasks.push(data);
        }
      }
      return combiner.taskId;
    } catch (err) {
      console.warn('Failed to fetch combiner task; will recreate:', err);
    }
  }

  combiner.taskId = null;
  const created = await createCombinerTask(canvas, combiner);
  return created;
}

/**
 * Create a backend task for a combiner node.
 */
export async function createCombinerTask(canvas, combinerNode) {
  const result = await apiPost('/api/orchestration/tasks', {
    studio_id: canvas.studioId,
    from: 'system',
    to: 'unassigned',
    description: `${combinerNode.name} operation`,
    priority: 3,
    result_combination_mode: combinerNode.resultCombinationMode
  });
  combinerNode.taskId = result.task.id;
  console.log(`📝 Created task ${result.task.id} for combiner ${combinerNode.id}`);
  await canvas.saveLayout();
  await canvas.init();
  return combinerNode.taskId;
}

/**
 * Execute a combiner: resolves inputs, configures task, executes.
 */
export async function executeCombiner(canvas, combiner) {
  // Combiner tasks are now created upfront, so just verify it exists
  if (!combiner.taskId) {
    canvas.showNotification('Combiner task not found. Please recreate the combiner node.', 'error');
    return;
  }

  console.log('🔀 Executing combiner:', combiner.id, 'Task:', combiner.taskId);

  const { tasks: inputTasks, missingAgentInputs } = resolveCombinerInputs(canvas, combiner);

  if (missingAgentInputs.length > 0) {
    canvas.showNotification(`No recent tasks found for agent(s): ${missingAgentInputs.join(', ')}`, 'warning');
  }

  if (inputTasks.length === 0) {
    canvas.showNotification('No input tasks connected to this combiner', 'warning');
    return;
  }

  const inputTaskIds = inputTasks.map(t => t.id);

  // Resolve downstream agent
  const outputConnection =
    canvas.connections.find(c => c.from === combiner.id && c.fromPort === 'output') ||
    canvas.connections.find(c => c.from === combiner.id);

  if (!outputConnection) {
    canvas.showNotification('MERGE output is not connected to an agent. Connect it before running.', 'warning');
    return;
  }

  let targetAgentName = 'unassigned';
  const targetNode = canvas.getNodeById(outputConnection.to);
  if (targetNode && targetNode.type === 'agent') {
    targetAgentName = targetNode.node.name;
  } else {
    targetAgentName = outputConnection.to;
  }

  // Execute pending/failed inputs assigned to agents
  const tasksToExecute = inputTasks.filter(t =>
    (t.status === 'pending' || t.status === 'failed') &&
    t.to && t.to !== 'unassigned'
  );

  if (tasksToExecute.length > 0) {
    canvas.showNotification(`Executing ${tasksToExecute.length} input task(s)...`, 'info');
    const execPromises = tasksToExecute.map(task =>
      apiPost('/api/orchestration/tasks/execute', { task_id: task.id }).catch(error => {
        throw new Error(`Failed to execute task ${task.id}: ${error.message}`);
      })
    );
    await Promise.all(execPromises);
    canvas.draw();
    canvas.showNotification('Waiting for input tasks to complete...', 'info');

    let waitTime = 0;
    const maxWaitTime = 30000;
    const pollInterval = 2000;

    while (waitTime < maxWaitTime) {
      await new Promise(resolve => setTimeout(resolve, pollInterval));
      waitTime += pollInterval;
      await canvas.init();

      const allCompleted = inputTasks.every(t => {
        const updatedTask = canvas.tasks.find(ut => ut.id === t.id);
        return updatedTask && updatedTask.status === 'completed';
      });

      if (allCompleted) {
        canvas.showNotification('All input tasks completed!', 'success');
        break;
      }

      const anyFailed = inputTasks.some(t => {
        const updatedTask = canvas.tasks.find(ut => ut.id === t.id);
        return updatedTask && updatedTask.status === 'failed';
      });

      if (anyFailed) {
        canvas.showNotification('Some input tasks failed', 'warning');
        break;
      }
    }

    if (waitTime >= maxWaitTime) {
      canvas.showNotification('Timeout waiting for input tasks', 'warning');
    }

    await canvas.init();
  }

  // Configure combiner task
  await apiPut(`/api/orchestration/tasks`, {
    task_id: combiner.taskId,
    to: targetAgentName,
    input_task_ids: inputTaskIds,
    result_combination_mode: combiner.resultCombinationMode,
    combination_instruction: buildCombinationInstruction(inputTasks),
    status: 'pending'
  });

  // Execute combiner task
  await apiPost('/api/orchestration/tasks/execute', { task_id: combiner.taskId });
  canvas.showNotification(`Combiner execution started!`, 'success');
  await canvas.init();
  canvas.draw();
}
