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

  const outputConnection = canvas.connections.find(c => c.from === combiner.id && c.fromPort === 'output');
  const existingInputConns = canvas.connections.filter(c => c.to === combiner.id && c.toPort.startsWith('input'));

  let targetInputPortId = null;
  const existingForTask = existingInputConns.find(c => c.from === canvas.assignmentSourceTask.id);
  if (existingForTask) {
    targetInputPortId = existingForTask.toPort;
  } else {
    const nextIndex = existingInputConns.length;
    targetInputPortId = `input-${nextIndex}`;
    canvas.ensureCombinerInputPort(combiner, targetInputPortId);
    canvas.createConnection(canvas.assignmentSourceTask.id, 'output', combiner.id, targetInputPortId);
  }

  const targetAgentName = outputConnection ? outputConnection.to : (canvas.assignmentSourceTask?.to || 'unassigned');

  const inputConnections = canvas.connections.filter(c => c.to === combiner.id);
  const inputTaskIds = [];
  for (const conn of inputConnections) {
    const nodeData = canvas.getNodeById(conn.from);
    if (nodeData && nodeData.type === 'task') {
      inputTaskIds.push(conn.from);
    }
  }

  const result = await apiPut(`/api/orchestration/tasks`, {
    task_id: canvas.assignmentSourceTask.id,
    to: targetAgentName,
    input_task_ids: inputTaskIds,
    result_combination_mode: combiner.resultCombinationMode || 'merge'
  });
  console.log('✅ Task assigned to combiner → agent:', result);

  canvas.assignmentMode = false;
  canvas.assignmentSourceTask = null;
  canvas.assignmentMouseX = 0;
  canvas.assignmentMouseY = 0;
  canvas.canvas.style.cursor = 'grab';

  const task = canvas.tasks.find(t => t.id === (result.id || canvas.assignmentSourceTask?.id));
  if (task) {
    task.to = targetAgentName;
    task.input_task_ids = inputTaskIds;
  }

  canvas.draw();
  canvas.addNotification(`✅ Task assigned via ${combiner.name} → ${targetAgentName}`, 'success');
  console.log(`📊 Task will receive combined results from: ${inputTaskIds.join(', ')}`);
}
