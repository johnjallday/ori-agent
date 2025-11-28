/**
 * Studios Canvas Helper Functions
 * Handles canvas view, agent management, task creation, and mission execution
 */

let currentStudioId = null;
let currentWorkspaceDashboard = null;

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
        window.agentCanvas.onMetricsUpdate = updateMetrics;
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
 * Show task details (placeholder)
 */
function showTaskDetails(task) {
    console.log('Show task details:', task);
    // Implementation depends on task data structure
}

/**
 * Update metrics (placeholder)
 */
function updateMetrics(metrics) {
    console.log('Update metrics:', metrics);
    // Implementation depends on metrics structure
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
