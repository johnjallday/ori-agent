/**
 * Studios Canvas Helper Functions
 * Handles canvas view, agent management, task creation, and mission execution
 */

let currentStudioId = null;
let currentWorkspaceDashboard = null;

/**
 * Show agent details in the sidebar
 */
function showAgentDetails(agent) {
  console.log('[SIDEBAR] showAgentDetails called for:', agent.name);

  // Hide task details if showing
  hideTaskDetails();

  // Force close all canvas panels
  if (window.agentCanvas && window.agentCanvas.state) {
    console.log('[SIDEBAR] Closing canvas panels');
    window.agentCanvas.state.expandedPanelWidth = 0;
    window.agentCanvas.state.expandedTask = null;
    window.agentCanvas.state.expandedAgentPanelWidth = 0;
    window.agentCanvas.state.expandedAgent = null;
    window.agentCanvas.state.expandedCombinerPanelWidth = 0;
    window.agentCanvas.state.expandedCombiner = null;
    if (window.agentCanvas.draw) window.agentCanvas.draw();
  }

  const panel = document.getElementById('agent-details-panel');
  const content = document.getElementById('agent-details-content');

  if (!panel || !content) {
    console.error('[SIDEBAR] Panel or content not found!');
    return;
  }

  // Show panel and populate immediately
  panel.style.display = 'block';

  const html = `
    <div class="mb-3">
      <strong style="color: var(--text-primary);">Name:</strong>
      <div style="color: var(--text-secondary);">${agent.name || 'Unknown'}</div>
    </div>
    <div class="mb-3">
      <strong style="color: var(--text-primary);">Status:</strong>
      <div>
        <span class="badge ${agent.status === 'active' ? 'bg-success' : 'bg-secondary'}">
          ${agent.status || 'idle'}
        </span>
      </div>
    </div>
    <div class="mb-3">
      <strong style="color: var(--text-primary);">Position:</strong>
      <div style="color: var(--text-secondary);">x: ${Math.round(agent.x || 0)}, y: ${Math.round(agent.y || 0)}</div>
    </div>
    ${agent.config ? `
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Model:</strong>
        <div style="color: var(--text-secondary);">${agent.config.model || 'default'}</div>
      </div>
    ` : ''}
  `;
  content.innerHTML = html;
  console.log('[SIDEBAR] Agent details populated');
}

/**
 * Hide agent details panel
 */
function hideAgentDetails() {
  const panel = document.getElementById('agent-details-panel');
  if (panel) {
    panel.style.display = 'none';
  }
}

/**
 * Show task details in the sidebar
 */
function showTaskDetails(task) {
  console.log('[SIDEBAR] showTaskDetails called for:', task.description);

  // Hide agent details if showing
  hideAgentDetails();

  // Force close all canvas panels
  if (window.agentCanvas && window.agentCanvas.state) {
    console.log('[SIDEBAR] Closing canvas panels');
    window.agentCanvas.state.expandedPanelWidth = 0;
    window.agentCanvas.state.expandedTask = null;
    window.agentCanvas.state.expandedAgentPanelWidth = 0;
    window.agentCanvas.state.expandedAgent = null;
    window.agentCanvas.state.expandedCombinerPanelWidth = 0;
    window.agentCanvas.state.expandedCombiner = null;
    if (window.agentCanvas.draw) window.agentCanvas.draw();
  }

  const panel = document.getElementById('task-details-panel');
  const content = document.getElementById('task-details-content');

  if (!panel || !content) {
    console.error('[SIDEBAR] Panel or content not found!');
    return;
  }

  // Show panel and populate immediately
  panel.style.display = 'block';

  const statusBadge = {
    'pending': '<span class="badge bg-warning">Pending</span>',
    'in_progress': '<span class="badge bg-primary">In Progress</span>',
    'completed': '<span class="badge bg-success">Completed</span>',
    'failed': '<span class="badge bg-danger">Failed</span>'
  }[task.status] || '<span class="badge bg-secondary">Unknown</span>';

  // Check if this is a combiner task
  const isCombinerTask = task.combiner_type || task.combinerType;

  let html = '';

  if (isCombinerTask) {
    // Combiner task details
    const combinerTypes = {
      'merge': { icon: '🔀', name: 'Merge', description: 'Combines multiple inputs into a single context' },
      'sequence': { icon: '⛓️', name: 'Sequence', description: 'Executes inputs in order' },
      'parallel': { icon: '⚡', name: 'Parallel', description: 'Runs all inputs simultaneously' },
      'vote': { icon: '🗳️', name: 'Vote', description: 'Selects best result via voting' }
    };

    const combinerType = combinerTypes[task.combiner_type || task.combinerType] ||
                        { icon: '🔧', name: 'Combiner', description: 'Custom combiner' };

    html = `
      <div class="mb-3">
        <div style="font-size: 32px; text-align: center; margin-bottom: 10px;">${combinerType.icon}</div>
        <strong style="color: var(--text-primary); font-size: 1.1rem;">${combinerType.name} Task</strong>
        <div style="color: var(--text-secondary); font-size: 0.85rem; margin-top: 5px;">${combinerType.description}</div>
      </div>
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Status:</strong>
        <div>${statusBadge}</div>
      </div>
      ${task.to ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Assigned To:</strong>
          <div style="color: var(--text-secondary);">${task.to}</div>
        </div>
      ` : ''}
      ${task.input_task_ids && task.input_task_ids.length > 0 ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Input Tasks:</strong>
          <div style="color: var(--text-secondary); font-size: 0.85rem;">
            ${task.input_task_ids.map((id, idx) => `Input ${idx + 1}: ${id.substring(0, 8)}...`).join('<br>')}
          </div>
        </div>
      ` : ''}
      ${task.result_combination_mode || task.resultCombinationMode ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Combination Mode:</strong>
          <div style="color: var(--text-secondary);">${task.result_combination_mode || task.resultCombinationMode}</div>
        </div>
      ` : ''}
      ${task.result ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Combined Output:</strong>
          <div style="color: var(--text-primary); white-space: pre-wrap; font-family: monospace; font-size: 0.85rem; background: #0a0f1a; padding: 10px; border-radius: 4px; border: 1px solid var(--border-color); max-height: 200px; overflow-y: auto;">${task.result}</div>
        </div>
      ` : ''}
      <div class="mb-3">
        <button class="btn btn-sm btn-primary w-100 mb-2" onclick="addCombinerInput('${task.id}')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="5" x2="12" y2="19"></line>
            <line x1="5" y1="12" x2="19" y2="12"></line>
          </svg>
          Add Input Task
        </button>
        <button class="btn btn-sm btn-success w-100 mb-2" onclick="assignCurrentTask()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M9,10H7V12H9V10M13,10H11V12H13V10M17,10H15V12H17V10M19,3H18V1H16V3H8V1H6V3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5A2,2 0 0,0 19,3M19,19H5V8H19V19Z"/>
          </svg>
          Assign to Agent
        </button>
        <button class="btn btn-sm btn-warning w-100" onclick="executeCombinerTask('${task.id}')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M8,5.14V19.14L19,12.14L8,5.14Z"/>
          </svg>
          Run Merge
        </button>
      </div>
    `;
  } else {
    // Regular task details
    html = `
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Description:</strong>
        <div style="color: var(--text-secondary);">${task.description || 'No description'}</div>
      </div>
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Status:</strong>
        <div>${statusBadge}</div>
      </div>
      ${task.to ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Assigned To:</strong>
          <div style="color: var(--text-secondary);">${task.to}</div>
        </div>
      ` : ''}
      ${task.result ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Result:</strong>
          <div style="color: var(--text-primary); white-space: pre-wrap; font-family: monospace; font-size: 0.85rem; background: #0a0f1a; padding: 10px; border-radius: 4px; border: 1px solid var(--border-color); max-height: 200px; overflow-y: auto;">${task.result}</div>
        </div>
      ` : ''}
      ${task.created_at ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Created:</strong>
          <div style="color: var(--text-secondary); font-size: 0.8rem;">${new Date(task.created_at).toLocaleString()}</div>
        </div>
      ` : ''}
      ${task.completed_at ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Completed:</strong>
          <div style="color: var(--text-secondary); font-size: 0.8rem;">${new Date(task.completed_at).toLocaleString()}</div>
        </div>
      ` : ''}
    `;
  }

  content.innerHTML = html;

  // Show task action buttons
  const actionsDiv = document.getElementById('task-actions');
  if (actionsDiv) {
    actionsDiv.style.display = 'block';
  }

  // Store the current task for actions (edit, delete, etc.)
  if (window.agentCanvas && window.agentCanvas.state) {
    window.agentCanvas.state.expandedTask = task;
  }

  console.log('[SIDEBAR] Task details populated');
}

/**
 * Hide task details panel
 */
function hideTaskDetails() {
  const panel = document.getElementById('task-details-panel');
  if (panel) {
    panel.style.display = 'none';
  }

  // Hide task action buttons
  const actionsDiv = document.getElementById('task-actions');
  if (actionsDiv) {
    actionsDiv.style.display = 'none';
  }

  // Clear the current task
  if (window.agentCanvas && window.agentCanvas.state) {
    window.agentCanvas.state.expandedTask = null;
  }
}

/**
 * Show combiner details in the sidebar
 */
function showCombinerDetails(combiner) {
  console.log('[SIDEBAR] showCombinerDetails called for:', combiner.name);

  // Hide other panels
  hideAgentDetails();
  hideTaskDetails();

  // Force close all canvas panels
  if (window.agentCanvas && window.agentCanvas.state) {
    console.log('[SIDEBAR] Closing canvas panels');
    window.agentCanvas.state.expandedPanelWidth = 0;
    window.agentCanvas.state.expandedTask = null;
    window.agentCanvas.state.expandedAgentPanelWidth = 0;
    window.agentCanvas.state.expandedAgent = null;
    window.agentCanvas.state.expandedCombinerPanelWidth = 0;
    window.agentCanvas.state.expandedCombiner = null;
    if (window.agentCanvas.draw) window.agentCanvas.draw();
  }

  const panel = document.getElementById('combiner-details-panel');
  const content = document.getElementById('combiner-details-content');

  if (!panel || !content) {
    console.error('[SIDEBAR] Combiner panel or content not found!');
    return;
  }

  // Show panel
  panel.style.display = 'block';

  // Get combiner type info
  const typeInfo = {
    'merge': { icon: '🔀', name: 'Merge', description: 'Combines multiple inputs into a single context' },
    'sequence': { icon: '⛓️', name: 'Sequence', description: 'Executes inputs in order, each seeing previous results' },
    'parallel': { icon: '⚡', name: 'Parallel', description: 'Runs all inputs simultaneously' }
  };

  const info = typeInfo[combiner.combinerType] || { icon: '🔧', name: 'Combiner', description: 'Combines inputs' };

  // Get connected tasks
  const connections = window.agentCanvas?.state?.connections || [];
  const inputConnections = connections.filter(c =>
    c.to === combiner.id && c.toPort && c.toPort.startsWith('input-')
  );

  const html = `
    <div class="mb-3 text-center">
      <div style="font-size: 2.5rem; margin-bottom: 10px;">${info.icon}</div>
      <strong style="color: var(--text-primary); font-size: 1.1rem;">${info.name}</strong>
    </div>
    <div class="mb-3">
      <strong style="color: var(--text-primary);">Type:</strong>
      <div style="color: var(--text-secondary);">${combiner.combinerType}</div>
    </div>
    <div class="mb-3">
      <strong style="color: var(--text-primary);">Description:</strong>
      <div style="color: var(--text-secondary); font-size: 0.85rem;">${info.description}</div>
    </div>
    <div class="mb-3">
      <strong style="color: var(--text-primary);">Input Ports:</strong>
      <div style="color: var(--text-secondary);">${combiner.inputPorts?.length || 0} ports</div>
    </div>
    ${inputConnections.length > 0 ? `
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Connected Inputs:</strong>
        <div style="color: var(--text-secondary); font-size: 0.85rem;">
          ${inputConnections.map((c, i) => `
            <div style="padding: 5px 0;">
              ${i + 1}. Port ${c.toPort.replace('input-', '')} ← ${c.from}
            </div>
          `).join('')}
        </div>
      </div>
    ` : ''}
    ${combiner.taskId ? `
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Associated Task:</strong>
        <div style="color: var(--text-secondary); font-size: 0.85rem; font-family: monospace;">${combiner.taskId.substring(0, 8)}...</div>
      </div>
    ` : ''}
  `;

  content.innerHTML = html;
  console.log('[SIDEBAR] Combiner details populated');
}

/**
 * Hide combiner details panel
 */
function hideCombinerDetails() {
  const panel = document.getElementById('combiner-details-panel');
  if (panel) {
    panel.style.display = 'none';
  }
}

/**
 * Show add task modal
 */
async function showAddTaskModal() {
  const description = prompt('Enter task description:');

  if (!description || !description.trim()) {
    return;
  }

  if (!currentStudioId) {
    alert('No studio loaded');
    return;
  }

  const task = {
    description: description.trim(),
    from: null,
    to: null,
    status: 'pending',
    x: 100,  // Default position - top left area
    y: 100
  };

  try {
    const response = await fetch(`/api/studios/${currentStudioId}/tasks`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(task)
    });

    if (response.ok) {
      console.log('Task created successfully');
      // Reload the page to show the new task
      window.location.reload();
    } else {
      const error = await response.text();
      alert(`Failed to create task: ${error}`);
    }
  } catch (error) {
    console.error('Error creating task:', error);
    alert(`Error creating task: ${error.message}`);
  }
}

/**
 * Delete current task
 */
async function deleteCurrentTask() {
  if (!window.agentCanvas) {
    alert('Canvas not initialized');
    return;
  }

  const task = window.agentCanvas.state.expandedTask;
  if (!task) {
    alert('No task selected');
    return;
  }

  // Confirm deletion
  if (!confirm(`Are you sure you want to delete this task?\n\n"${task.description || 'Task'}"\n\nThis action cannot be undone.`)) {
    return;
  }

  try {
    const response = await fetch(`/api/studios/${currentStudioId}/tasks/${task.id}`, {
      method: 'DELETE'
    });

    if (response.ok) {
      console.log('Task deleted successfully');

      // Remove from local tasks array
      const taskIndex = window.agentCanvas.state.tasks.findIndex(t => t.id === task.id);
      if (taskIndex !== -1) {
        const tasks = [...window.agentCanvas.state.tasks];
        tasks.splice(taskIndex, 1);
        window.agentCanvas.state.setTasks(tasks);
      }

      // Hide the task details panel
      hideTaskDetails();

      // Redraw canvas
      window.agentCanvas.draw();
    } else {
      const error = await response.text();
      alert(`Failed to delete task: ${error}`);
    }
  } catch (error) {
    console.error('Error deleting task:', error);
    alert(`Error deleting task: ${error.message}`);
  }
}

/**
 * Edit current task
 */
async function editCurrentTask() {
  if (!window.agentCanvas) {
    alert('Canvas not initialized');
    return;
  }

  const task = window.agentCanvas.state.expandedTask;
  if (!task) {
    alert('No task selected');
    return;
  }

  // Prompt for new description
  const newDescription = prompt('Enter new task description:', task.description);
  if (!newDescription || newDescription.trim() === '') {
    return;
  }

  // Prompt for agent assignment (optional)
  const agents = window.agentCanvas.state.agents.map(a => a.name);
  let assignTo = task.to || '';

  if (agents.length > 0) {
    const agentList = agents.map((a, i) => `${i + 1}. ${a}`).join('\n');
    const choice = prompt(
      `Assign to agent (leave empty for unassigned):\n\n${agentList}\n\nEnter agent number or name:`,
      assignTo
    );

    if (choice !== null) {
      // Check if it's a number (agent index)
      const index = parseInt(choice);
      if (!isNaN(index) && index > 0 && index <= agents.length) {
        assignTo = agents[index - 1];
      } else if (choice.trim() === '') {
        assignTo = '';
      } else {
        assignTo = choice.trim();
      }
    }
  }

  try {
    const response = await fetch(`/api/studios/${currentStudioId}/tasks/${task.id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: newDescription.trim(),
        to: assignTo,
        from: task.from || ''
      })
    });

    if (response.ok) {
      console.log('Task updated successfully');
      // Update the task locally
      task.description = newDescription.trim();
      task.to = assignTo;

      // Refresh the task details panel
      showTaskDetails(task);

      // Redraw canvas
      window.agentCanvas.draw();
    } else {
      const error = await response.text();
      alert(`Failed to update task: ${error}`);
    }
  } catch (error) {
    console.error('Error updating task:', error);
    alert(`Error updating task: ${error.message}`);
  }
}

// Make functions globally available
window.showAgentDetails = showAgentDetails;
window.hideAgentDetails = hideAgentDetails;
window.showTaskDetails = showTaskDetails;
window.hideTaskDetails = hideTaskDetails;
window.showCombinerDetails = showCombinerDetails;
window.hideCombinerDetails = hideCombinerDetails;
window.showAddTaskModal = showAddTaskModal;
window.editCurrentTask = editCurrentTask;
window.deleteCurrentTask = deleteCurrentTask;

/**
 * Add input task to combiner
 */
async function addCombinerInput(combinerTaskId) {
  if (!window.agentCanvas) {
    alert('Canvas not initialized');
    return;
  }

  const combinerTask = window.agentCanvas.state.tasks.find(t => t.id === combinerTaskId);
  if (!combinerTask) {
    alert('Combiner task not found');
    return;
  }

  // Get list of all non-combiner tasks
  const availableTasks = window.agentCanvas.state.tasks.filter(t =>
    !t.combiner_type && !t.combinerType && t.id !== combinerTaskId
  );

  if (availableTasks.length === 0) {
    alert('No tasks available to add as input. Create some tasks first.');
    return;
  }

  // Show selection UI
  const taskList = availableTasks.map((t, i) =>
    `${i + 1}. ${t.description.substring(0, 50)}${t.description.length > 50 ? '...' : ''} (${t.status})`
  ).join('\n');

  const choice = prompt(
    `Select task to add as input:\n\n${taskList}\n\nEnter task number:`,
    ''
  );

  if (!choice) return;

  const index = parseInt(choice) - 1;
  if (isNaN(index) || index < 0 || index >= availableTasks.length) {
    alert('Invalid selection');
    return;
  }

  const selectedTask = availableTasks[index];

  // Update combiner task with new input
  const currentInputs = combinerTask.input_task_ids || [];
  if (currentInputs.includes(selectedTask.id)) {
    alert('This task is already an input');
    return;
  }

  const newInputs = [...currentInputs, selectedTask.id];

  try {
    const response = await fetch(`/api/studios/${currentStudioId}/tasks/${combinerTaskId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: combinerTask.description,
        to: combinerTask.to || '',
        from: combinerTask.from || '',
        input_task_ids: newInputs
      })
    });

    if (!response.ok) {
      throw new Error(`Failed to update task: ${response.statusText}`);
    }

    // Update local state
    combinerTask.input_task_ids = newInputs;

    // Refresh task details
    if (window.showTaskDetails) {
      window.showTaskDetails(combinerTask);
    }

    // Redraw canvas
    if (window.agentCanvas && window.agentCanvas.draw) {
      window.agentCanvas.draw();
    }

    alert(`Added "${selectedTask.description.substring(0, 30)}..." as input`);
  } catch (error) {
    console.error('Failed to add input:', error);
    alert(`Failed to add input: ${error.message}`);
  }
}

window.addCombinerInput = addCombinerInput;

/**
 * Assign current task to an agent
 */
function assignCurrentTask() {
  if (!window.agentCanvas) {
    alert('Canvas not initialized');
    return;
  }

  const task = window.agentCanvas.state.expandedTask;
  if (!task) {
    alert('No task selected');
    return;
  }

  // Enter assignment mode
  window.agentCanvas.state.assignmentMode = true;
  window.agentCanvas.state.assignmentSourceTask = task;
  window.agentCanvas.canvas.style.cursor = 'crosshair';

  // Show notification
  if (window.agentCanvas.showNotification) {
    window.agentCanvas.showNotification('Click an agent to assign this task', 'info');
  }

  // Close the task details panel to see the canvas
  hideTaskDetails();
}

window.assignCurrentTask = assignCurrentTask;

/**
 * Execute combiner task
 */
async function executeCombinerTask(combinerTaskId) {
  if (!window.agentCanvas) {
    alert('Canvas not initialized');
    return;
  }

  const combinerTask = window.agentCanvas.state.tasks.find(t => t.id === combinerTaskId);
  if (!combinerTask) {
    alert('Combiner task not found');
    return;
  }

  // Validate combiner has inputs
  if (!combinerTask.input_task_ids || combinerTask.input_task_ids.length === 0) {
    alert('Please add input tasks before running the merge');
    return;
  }

  // Validate combiner has output agent
  if (!combinerTask.to || combinerTask.to === '' || combinerTask.to === 'unassigned') {
    alert('Please assign the merge task to an agent before running');
    return;
  }

  try {
    // Execute the task
    const response = await fetch('/api/orchestration/tasks/execute', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ task_id: combinerTaskId })
    });

    if (!response.ok) {
      throw new Error(`Failed to execute combiner: ${response.statusText}`);
    }

    alert(`Executing merge task...\n\nInputs: ${combinerTask.input_task_ids.length} tasks\nOutput: ${combinerTask.to}`);

    // Close task details
    hideTaskDetails();

    // Refresh canvas
    if (window.agentCanvas && window.agentCanvas.init) {
      await window.agentCanvas.init();
    }
  } catch (error) {
    console.error('Failed to execute combiner:', error);
    alert(`Failed to execute merge: ${error.message}`);
  }
}

window.executeCombinerTask = executeCombinerTask;

/**
 * View workspace (redirect to workspace dashboard)
 */
async function viewWorkspace(workspaceId) {
    window.location.href = `/studios/${workspaceId}`;
}

/**
 * Open workspace in canvas mode
 */
function openWorkspaceCanvas(workspaceId) {
    // Switch to canvas view and load the specific workspace
    if (typeof switchView === 'function') {
        switchView('canvas');
    }

    // Wait a bit for the select to be populated, then select and load the workspace
    setTimeout(() => {
        const select = document.getElementById('canvas-studio-select');
        if (select) {
            select.value = workspaceId;
            loadCanvasStudio(workspaceId);
        }
    }, 100);
}

/**
 * View switching between grid and canvas
 */
function switchView(view) {
    const gridView = document.getElementById('grid-view');
    const canvasView = document.getElementById('canvas-view');

    if (view === 'canvas') {
        gridView.style.display = 'none';
        canvasView.style.display = 'block';
        populateCanvasStudioSelect();
    } else {
        gridView.style.display = 'block';
        canvasView.style.display = 'none';
    }
}

/**
 * Populate canvas studio select dropdown
 */
function populateCanvasStudioSelect() {
    const select = document.getElementById('canvas-studio-select');
    if (!select) return;

    fetch('/api/orchestration/workspace')
        .then(res => res.json())
        .then(data => {
            const workspaces = data.workspaces || [];
            select.innerHTML = '<option value="">Choose a studio...</option>' +
                workspaces.map(ws => `<option value="${ws.id}">${escapeHtml(ws.name || ws.id)}</option>`).join('');
        })
        .catch(err => console.error('Error loading studios:', err));
}

/**
 * Load a canvas studio
 */
function loadCanvasStudio(studioId) {
    if (!studioId) {
        document.getElementById('canvas-info').textContent = 'No studio selected';
        // Show the label when no studio is selected
        const label = document.getElementById('canvas-studio-label');
        if (label) {
            label.style.display = '';
        }
        return;
    }

    // Hide the "Select Studio:" label once a studio is loaded
    const label = document.getElementById('canvas-studio-label');
    if (label) {
        label.style.display = 'none';
    }

    currentStudioId = studioId;

    // Initialize canvas visualization
    if (window.agentCanvas) {
        window.agentCanvas.destroy();
    }

    if (typeof AgentCanvas !== 'undefined') {
        window.agentCanvas = new AgentCanvas('agent-canvas', studioId);
        window.agentCanvas.init();

        // Load saved background color
        loadCanvasBackground();

        // Set up event listeners for canvas clicks
        window.agentCanvas.onAgentClick = showAgentDetails;
        window.agentCanvas.onTaskClick = showTaskDetails;
        window.agentCanvas.onCombinerClick = showCombinerDetails;
        window.agentCanvas.onTimelineEvent = addTimelineEvent;

        // Load available agents and update current list
        setTimeout(() => {
            loadAvailableAgents();
            updateCurrentAgentsList();
            updateTaskAgentSelectors();
        }, 500);
    }
}

/**
 * Execute mission
 */
async function executeMission() {
    if (!currentStudioId) {
        alert('Please select a studio first');
        return;
    }

    const mission = document.getElementById('mission-input').value.trim();
    if (!mission) {
        alert('Please enter a mission description');
        return;
    }

    const btn = document.getElementById('execute-mission-btn');
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span>Executing...';

    try {
        const response = await fetch(`/api/studios/${currentStudioId}/mission`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ mission })
        });

        const result = await response.json();

        if (result.message) {
            // Add to timeline
            addTimelineEvent({
                type: 'mission_started',
                data: { mission }
            });

            // Set mission on canvas directly
            if (window.agentCanvas) {
                window.agentCanvas.setMission(mission);
            }

            document.getElementById('mission-input').value = '';
        }
    } catch (error) {
        console.error('Failed to execute mission:', error);
        alert('Failed to execute mission');
    } finally {
        btn.disabled = false;
        btn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M8.59,16.58L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.58Z"/></svg>Set Mission';
    }
}

/**
 * Load available agents
 */
async function loadAvailableAgents() {
    try {
        const select = document.getElementById('available-agents');

        // Element doesn't exist in canvas view, skip update
        if (!select) {
            return;
        }

        const response = await fetch('/api/agents');
        const data = await response.json();

        // Get current workspace agents
        const currentAgents = window.agentCanvas ? window.agentCanvas.agents.map(a => a.name) : [];

        // Filter out already added agents
        const availableAgents = (data.agents || []).filter(agent => !currentAgents.includes(agent.name));

        select.innerHTML = '<option value="">Select agent to add...</option>' +
            availableAgents.map(agent => `<option value="${agent.name}">${escapeHtml(agent.name)}</option>`).join('');
    } catch (error) {
        console.error('Failed to load agents:', error);
    }
}

/**
 * Add agent to canvas
 */
async function addAgentToCanvas() {
    const select = document.getElementById('available-agents');
    const agentName = select.value;

    if (!agentName) {
        alert('Please select an agent to add');
        return;
    }

    if (!currentStudioId) {
        alert('Please select a studio first');
        return;
    }

    try {
        const response = await fetch(`/api/studios/${currentStudioId}/agents`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ agent_name: agentName })
        });

        if (response.ok) {
            // Reload the canvas to show new agent
            loadCanvasStudio(currentStudioId);
            select.value = '';
        } else {
            alert('Failed to add agent');
        }
    } catch (error) {
        console.error('Failed to add agent:', error);
        alert('Failed to add agent');
    }
}

/**
 * Remove agent from canvas
 */
async function removeAgentFromCanvas(agentName) {
    if (!confirm(`Remove agent "${agentName}" from this workspace?`)) {
        return;
    }

    if (!currentStudioId) {
        return;
    }

    try {
        const response = await fetch(`/api/studios/${currentStudioId}/agents/${agentName}`, {
            method: 'DELETE'
        });

        if (response.ok) {
            // Reload the canvas to update
            loadCanvasStudio(currentStudioId);
        } else {
            alert('Failed to remove agent');
        }
    } catch (error) {
        console.error('Failed to remove agent:', error);
        alert('Failed to remove agent');
    }
}

/**
 * Update current agents list
 */
function updateCurrentAgentsList() {
    const listDiv = document.getElementById('current-agents-list');

    // Element doesn't exist in canvas view, skip update
    if (!listDiv) {
        return;
    }

    if (!window.agentCanvas || !window.agentCanvas.agents) {
        listDiv.innerHTML = '<p style="color: var(--text-muted); font-style: italic;">No agents in workspace</p>';
        return;
    }

    const agents = window.agentCanvas.agents;
    if (agents.length === 0) {
        listDiv.innerHTML = '<p style="color: var(--text-muted); font-style: italic;">No agents in workspace</p>';
        return;
    }

    listDiv.innerHTML = `
        <div style="border-top: 1px solid var(--border-color); padding-top: 0.75rem; margin-top: 0.5rem;">
            <small style="color: var(--text-secondary); font-weight: 600; text-transform: uppercase;">Current Agents:</small>
            <div class="mt-2">
                ${agents.map(agent => `
                    <div class="d-flex justify-content-between align-items-center mb-1 p-2" style="background: rgba(255,255,255,0.03); border-radius: 4px;">
                        <div class="d-flex align-items-center">
                            <span style="display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: ${agent.color}; margin-right: 8px;"></span>
                            <span style="color: var(--text-primary);">${escapeHtml(agent.name)}</span>
                        </div>
                        <button class="btn btn-sm" onclick="removeAgentFromCanvas('${escapeHtml(agent.name)}')" style="padding: 2px 6px; font-size: 0.75rem; color: var(--danger-color);" title="Remove agent">
                            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                                <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
                            </svg>
                        </button>
                    </div>
                `).join('')}
            </div>
        </div>
    `;
}

/**
 * Update task agent selectors
 */
function updateTaskAgentSelectors() {
    const toSelect = document.getElementById('task-to-agent');

    // Element doesn't exist in canvas view, skip update
    if (!toSelect) {
        return;
    }

    if (!window.agentCanvas || !window.agentCanvas.agents) {
        toSelect.innerHTML = '<option value="">Select agent...</option>';
        return;
    }

    const agents = window.agentCanvas.agents;
    const options = '<option value="">Select agent...</option>' +
        agents.map(agent => `<option value="${escapeHtml(agent.name)}">${escapeHtml(agent.name)}</option>`).join('');

    toSelect.innerHTML = options;
}

/**
 * Create task
 */
async function createTask() {
    const description = document.getElementById('task-description').value.trim();
    const toAgent = document.getElementById('task-to-agent').value;

    if (!description) {
        alert('Please enter a task description');
        return;
    }

    if (!toAgent) {
        alert('Please select an agent to assign the task to');
        return;
    }

    if (!currentStudioId) {
        alert('Please select a studio first');
        return;
    }

    try {
        const response = await fetch(`/api/studios/${currentStudioId}/tasks`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                description: description,
                from: 'system',
                to: toAgent,
                priority: 1
            })
        });

        if (response.ok) {
            // Clear form
            document.getElementById('task-description').value = '';
            document.getElementById('task-to-agent').value = '';

            // Reload canvas to show new task
            loadCanvasStudio(currentStudioId);
        } else {
            const error = await response.text();
            alert('Failed to create task: ' + error);
        }
    } catch (error) {
        console.error('Failed to create task:', error);
        alert('Failed to create task');
    }
}

/**
 * Show agent details panel
 */
async function showAgentDetails(agent) {
    const panel = document.getElementById('agent-details-panel');
    const content = document.getElementById('agent-details-content');

    if (!panel || !content) return;

    panel.style.display = 'block';

    const statusBadge = agent.status === 'active' ? 'badge-success' :
                       agent.status === 'busy' ? 'badge-warning' : 'badge-secondary';

    // Show loading state
    content.innerHTML = `
        <div class="text-center py-3">
            <div class="spinner-border spinner-border-sm text-primary" role="status">
                <span class="visually-hidden">Loading...</span>
            </div>
            <p class="mt-2 small" style="color: var(--text-muted);">Loading agent details...</p>
        </div>
    `;

    try {
        // Fetch full agent details from settings file
        const response = await fetch(`/agents/${agent.name}/agent_settings.json`);
        let agentSettings = null;
        if (response.ok) {
            agentSettings = await response.json();
        }

        // Fetch enabled plugins
        let enabledPlugins = [];
        if (agentSettings && agentSettings.Plugins) {
            enabledPlugins = Object.keys(agentSettings.Plugins);
        }

        const agentType = agentSettings?.type || 'tool-calling';
        const model = agentSettings?.Settings?.model || 'N/A';
        const temperature = agentSettings?.Settings?.temperature || 1.0;

        content.innerHTML = `
            <div class="mb-3">
                <div class="d-flex justify-content-between align-items-center mb-2">
                    <strong style="color: var(--text-primary); font-size: 1rem;">${escapeHtml(agent.name)}</strong>
                    <span class="modern-badge ${statusBadge}">${agent.status}</span>
                </div>
                <div class="small mb-2" style="color: var(--text-secondary); font-style: italic;">
                    Color: <span style="display: inline-block; width: 14px; height: 14px; border-radius: 50%; background: ${agent.color}; vertical-align: middle; border: 1px solid rgba(0,0,0,0.2);"></span>
                </div>
            </div>

            <div class="mb-3" style="border-top: 1px solid var(--border-color); padding-top: 0.75rem;">
                <h6 style="color: var(--text-primary); font-size: 0.875rem; font-weight: 600; margin-bottom: 0.5rem;">Agent Configuration</h6>
                <div class="small" style="color: var(--text-secondary);">
                    <div class="mb-1"><strong>Type:</strong> ${escapeHtml(agentType)}</div>
                    <div class="mb-1"><strong>Model:</strong> ${escapeHtml(model)}</div>
                    <div class="mb-1"><strong>Temperature:</strong> ${temperature}</div>
                </div>
            </div>

            ${enabledPlugins.length > 0 ? `
                <div style="border-top: 1px solid var(--border-color); padding-top: 0.75rem;">
                    <h6 style="color: var(--text-primary); font-size: 0.875rem; font-weight: 600; margin-bottom: 0.5rem;">Enabled Plugins (${enabledPlugins.length})</h6>
                    <div class="small" style="color: var(--text-secondary);">
                        ${enabledPlugins.map(plugin => `
                            <div class="mb-1 p-1" style="background: rgba(255,255,255,0.05); border-radius: 3px;">
                                ${escapeHtml(plugin)}
                            </div>
                        `).join('')}
                    </div>
                </div>
            ` : ''}
        `;
    } catch (error) {
        console.error('Failed to load agent details:', error);
        content.innerHTML = `
            <div class="alert alert-danger small">Failed to load agent details</div>
        `;
    }
}


/**
 * Add timeline event (placeholder)
 */
function addTimelineEvent(event) {
    console.log('Add timeline event:', event);
    // Implementation depends on timeline structure
}

/**
 * Load canvas background color from localStorage
 */
function loadCanvasBackground() {
    const savedColor = localStorage.getItem('canvas-bg-color');
    if (!savedColor) return;

    // Newer AgentCanvas may not expose setBackgroundColor; guard it
    if (window.agentCanvas && typeof window.agentCanvas.setBackgroundColor === 'function') {
        window.agentCanvas.setBackgroundColor(savedColor);
    }

    const colorPicker = document.getElementById('canvas-bg-color');
    if (colorPicker) {
        colorPicker.value = savedColor;
    }
}

/**
 * Change canvas background color
 */
function changeCanvasBackground(color) {
    if (window.agentCanvas) {
        window.agentCanvas.setBackgroundColor(color);
        localStorage.setItem('canvas-bg-color', color);
    }
}

/**
 * Utility function to escape HTML (uses global from studios-workspace.js)
 */
function escapeHtml(text) {
    if (text == null) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

/**
 * Connect current selection to merge node
 */
function connectToMerge() {
    if (!window.agentCanvas) {
        alert('Please select a workspace first!');
        return;
    }

    const canvas = window.agentCanvas;

    // Find the merge node
    const mergeNode = canvas.combinerNodes.find(n => n.combinerType === 'merge');
    if (!mergeNode) {
        alert('No MERGE node found! Please add a MERGE combiner node first using the palette on the left.');
        return;
    }

    // Find the selected agent
    const selectedAgent = canvas.selectedAgent || canvas.agents[0];
    if (!selectedAgent) {
        alert('No agent available! Please add an agent first.');
        return;
    }

    // Determine next available input port
    const existingInputs = canvas.connections.filter(c => c.to === mergeNode.id);
    const nextInputPort = `input-${existingInputs.length}`;

    // Create connection
    canvas.createConnection(selectedAgent.name, 'output', mergeNode.id, nextInputPort);
    canvas.draw();

    canvas.showNotification(`✅ Connected ${selectedAgent.name} to MERGE node (${nextInputPort})`, 'success');
}

/**
 * Create workflow using merge combiner
 */
async function createMergeWorkflowTasks() {
    if (!window.agentCanvas) {
        alert('Please select a workspace first!');
        return;
    }

    const canvas = window.agentCanvas;

    // Find the merge node and connected agents
    const mergeNode = canvas.combinerNodes.find(n => n.combinerType === 'merge');
    if (!mergeNode) {
        alert('No MERGE node found! Click "Setup Merge" first to create the workflow structure.');
        return;
    }

    // Find input connections to merge node
    const inputConnections = canvas.connections.filter(c => c.to === mergeNode.id);
    if (inputConnections.length === 0) {
        alert('No agents connected to MERGE node! Connect agents first.');
        return;
    }

    // Find output connection from merge node
    const outputConnection = canvas.connections.find(c => c.from === mergeNode.id);
    if (!outputConnection) {
        alert('MERGE node has no output connection! Connect it to a target agent.');
        return;
    }

    const targetAgentName = outputConnection.to;

    console.log('📊 Creating merge workflow tasks...');
    console.log('   Input agents:', inputConnections.map(c => c.from).join(', '));
    console.log('   Target agent:', targetAgentName);
    console.log('');
    console.log('💡 Instructions:');
    console.log('   1. Create tasks for the input agents (e.g., "1+3")');
    console.log('   2. After those tasks complete, their results are stored');
    console.log('   3. Create a task for the target agent that references those results');
    console.log('   4. The task description can say: "Use the results from previous tasks"');
    console.log('');
    console.log('   The MERGE node visually shows how data flows,');
    console.log('   but execution happens on the agents themselves.');

    alert(`✅ Merge Workflow Ready!\n\n` +
          `Input Agents: ${inputConnections.map(c => c.from).join(', ')}\n` +
          `Target Agent: ${targetAgentName}\n\n` +
          `Next Steps:\n` +
          `1. Create tasks for input agents\n` +
          `2. Run those tasks to completion\n` +
          `3. Create a task for ${targetAgentName}\n` +
          `4. That task can reference previous results\n\n` +
          `Check the console (F12) for more details!`);
}

/**
 * Add a combiner node to the canvas
 * @param {string} type - Type of combiner (merge, vote, etc.)
 */
async function addCombinerNode(type) {
    const canvas = window.agentCanvas;
    if (!canvas) {
        alert('Canvas not initialized. Please open a workspace first.');
        return;
    }

    // Calculate center position on canvas (accounting for offset and scale)
    const centerX = (window.innerWidth / 2 - canvas.offsetX) / canvas.scale;
    const centerY = (window.innerHeight / 2 - canvas.offsetY) / canvas.scale;

    try {
        await canvas.createCombinerNode(type, centerX, centerY);
        console.log(`✨ Added ${type.toUpperCase()} combiner node to canvas`);
    } catch (error) {
        console.error('Error adding combiner node:', error);
        alert(`Failed to add ${type} combiner node: ${error.message}`);
    }
}

/**
 * Toggle canvas sidebar visibility
 */
function toggleCanvasSidebar() {
  const sidebar = document.getElementById('canvas-sidebar');
  const mainArea = document.getElementById('canvas-main-area');

  if (!sidebar || !mainArea) return;

  if (sidebar.style.display === 'none') {
    // Show sidebar
    sidebar.style.display = 'block';
    mainArea.classList.remove('col-lg-12');
    mainArea.classList.add('col-lg-9');
  } else {
    // Hide sidebar
    sidebar.style.display = 'none';
    mainArea.classList.remove('col-lg-9');
    mainArea.classList.add('col-lg-12');
  }

  // Trigger canvas resize if canvas exists
  if (window.currentCanvas) {
    setTimeout(() => {
      window.currentCanvas.handleResize();
    }, 100);
  }
}

// Export functions for global access
window.viewWorkspace = viewWorkspace;
window.openWorkspaceCanvas = openWorkspaceCanvas;
window.switchView = switchView;
window.loadCanvasStudio = loadCanvasStudio;
window.executeMission = executeMission;
window.addAgentToCanvas = addAgentToCanvas;
window.removeAgentFromCanvas = removeAgentFromCanvas;
window.createTask = createTask;
window.changeCanvasBackground = changeCanvasBackground;
window.connectToMerge = connectToMerge;
window.createMergeWorkflowTasks = createMergeWorkflowTasks;
window.addCombinerNode = addCombinerNode;
window.toggleCanvasSidebar = toggleCanvasSidebar;
