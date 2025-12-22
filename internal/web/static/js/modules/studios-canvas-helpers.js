/**
 * Studios Canvas Helper Functions
 * Handles canvas view, agent management, task creation, and mission execution
 */

let currentStudioId = null;
let currentWorkspaceDashboard = null;

/**
 * Hide agent details panel
 */
function hideAgentDetails() {
  console.log('[SIDEBAR] hideAgentDetails called');
  const panel = document.getElementById('agent-details-panel');
  if (panel) {
    console.log('[SIDEBAR] Hiding agent panel, current display:', panel.style.display);
    panel.style.display = 'none';
    console.log('[SIDEBAR] Agent panel hidden');
  } else {
    console.error('[SIDEBAR] Agent panel not found!');
  }
}

/**
 * Hide attachment details panel
 */
function hideAttachmentDetails() {
  const panel = document.getElementById('attachment-details-panel');
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
  hideAttachmentDetails();

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
            ${task.input_task_ids.map((id, idx) => {
              const inputTask = window.agentCanvas?.state?.tasks?.find(t => t.id === id);
              return `
                <div class="mb-1 d-flex align-items-center justify-content-between" style="background: rgba(155, 89, 182, 0.1); padding: 6px 8px; border-radius: 4px;">
                  <span>🔗 Input ${idx + 1}: ${inputTask ? inputTask.description.substring(0, 30) : id.substring(0, 8)}...</span>
                  <button class="btn btn-sm btn-outline-danger" style="padding: 2px 8px; font-size: 0.75rem; line-height: 1;" onclick="removeTaskInput('${task.id}', '${id}')" title="Remove this input">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <line x1="18" y1="6" x2="6" y2="18"></line>
                      <line x1="6" y1="6" x2="18" y2="18"></line>
                    </svg>
                  </button>
                </div>
              `;
            }).join('')}
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
    // Check if this task is an input to any combiner
    let combinerAssignment = '';
    let combinersUsingThisTask = [];
    if (window.agentCanvas && window.agentCanvas.state && window.agentCanvas.state.tasks) {
      combinersUsingThisTask = window.agentCanvas.state.tasks.filter(t =>
        (t.combiner_type || t.combinerType) &&
        (t.input_task_ids || []).includes(task.id)
      );

      if (combinersUsingThisTask.length > 0) {
        const combinerNames = combinersUsingThisTask.map(c => c.description || c.id).join(', ');
        combinerAssignment = `
          <div class="mb-3">
            <strong style="color: var(--text-primary);">Input to Combiner:</strong>
            <div style="color: var(--text-secondary);">
              <span style="color: #8b5cf6;">🔀 ${combinerNames}</span>
            </div>
          </div>
        `;
      }
    }

    html = `
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Task ID:</strong>
        <div style="color: var(--text-secondary); font-family: monospace; font-size: 0.85rem;">${task.id || 'N/A'}</div>
      </div>
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Description:</strong>
        <div style="color: var(--text-secondary);">${task.description || 'No description'}</div>
      </div>
      <div class="mb-3">
        <strong style="color: var(--text-primary);">Status:</strong>
        <div>${statusBadge}</div>
      </div>
      ${task.input_task_ids && task.input_task_ids.length > 0 ? `
        <div class="mb-3">
          <strong style="color: var(--text-primary);">Input Tasks (${task.input_task_ids.length}):</strong>
          <div style="color: var(--text-secondary); font-size: 0.85rem;">
            ${task.input_task_ids.map((id, idx) => {
              const inputTask = window.agentCanvas?.state?.tasks?.find(t => t.id === id);
              return `
                <div class="mb-1 d-flex align-items-center justify-content-between" style="background: rgba(155, 89, 182, 0.1); padding: 6px 8px; border-radius: 4px;">
                  <span>🔗 ${inputTask ? inputTask.description : id.substring(0, 8) + '...'}</span>
                  <button class="btn btn-sm btn-outline-danger" style="padding: 2px 8px; font-size: 0.75rem; line-height: 1;" onclick="removeTaskInput('${task.id}', '${id}')" title="Remove this input">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <line x1="18" y1="6" x2="6" y2="18"></line>
                      <line x1="6" y1="6" x2="18" y2="18"></line>
                    </svg>
                  </button>
                </div>
              `;
            }).join('')}
          </div>
          <button class="btn btn-sm btn-outline-primary mt-2 w-100" onclick="addTaskInput('${task.id}')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="12" y1="5" x2="12" y2="19"></line>
              <line x1="5" y1="12" x2="19" y2="12"></line>
            </svg>
            Add Another Input
          </button>
        </div>
      ` : `
        <div class="mb-3">
          <button class="btn btn-sm btn-outline-primary w-100" onclick="addTaskInput('${task.id}')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="12" y1="5" x2="12" y2="19"></line>
              <line x1="5" y1="12" x2="19" y2="12"></line>
            </svg>
            Add Input Task
          </button>
        </div>
      `}
      ${combinerAssignment}
      ${(() => {
        // Determine what to show for "Assigned To"
        let assignedTo = null;

        // Priority 1: If task has a regular agent assignment (not "unassigned")
        if (task.to && task.to !== 'unassigned') {
          // Try to find the specific agent node instance
          const assignedNodeId = task.assigned_node_id || task.assignedNodeId;

          if (window.agentCanvas && window.agentCanvas.agents) {
            let agentNode = null;

            // First, try to find by nodeId (for tasks assigned after nodeId feature)
            if (assignedNodeId) {
              agentNode = window.agentCanvas.agents.find(a => a.nodeId === assignedNodeId);
            }

            // If no nodeId or not found, try to match by agent name (for old tasks)
            if (!agentNode) {
              // Find all agents with matching name
              const matchingAgents = window.agentCanvas.agents.filter(a => a.name === task.to);
              if (matchingAgents.length > 0) {
                // Use the first matching agent (by instance number)
                matchingAgents.sort((a, b) => (a.instanceNumber || 0) - (b.instanceNumber || 0));
                agentNode = matchingAgents[0];
              }
            }

            if (agentNode && agentNode.instanceNumber) {
              // Show agent name with instance number (e.g., "default #1")
              assignedTo = `${agentNode.name} #${agentNode.instanceNumber}`;
            } else if (agentNode) {
              // Agent found but no instance number
              assignedTo = agentNode.name;
            } else {
              // No matching agent node found, show agent name
              assignedTo = task.to;
            }
          } else {
            // Canvas not available, just show agent name
            assignedTo = task.to;
          }
        }
        // Priority 2: If task is feeding into a combiner, show combiner name
        else if (combinersUsingThisTask.length > 0) {
          const combinerNames = combinersUsingThisTask.map(c => c.description || c.id).join(', ');
          assignedTo = `<span style="color: #8b5cf6;">🔀 ${combinerNames}</span>`;
        }

        // Only show "Assigned To" if we have something to show
        return assignedTo ? `
          <div class="mb-3">
            <strong style="color: var(--text-primary);">Assigned To:</strong>
            <div style="color: var(--text-secondary);">${assignedTo}</div>
          </div>
        ` : '';
      })()}
      ${(() => {
        // Find scheduler targeting this task
        const schedulers = window.agentCanvas?.state?.schedulerNodes || [];
        const scheduler = schedulers.find(s => s.target_task_id === task.id);
        if (!scheduler) return '';

        const scheduleType = scheduler.schedule?.type || 'unknown';
        const interval = scheduler.schedule?.interval;
        const cronExpr = scheduler.schedule?.cron_expression;
        let scheduleDesc = '';
        if (scheduleType === 'interval' && interval) {
          const mins = Math.floor(interval / 60000000000);
          scheduleDesc = `Every ${mins} minute${mins !== 1 ? 's' : ''}`;
        } else if (scheduleType === 'cron' && cronExpr) {
          scheduleDesc = `Cron: ${cronExpr}`;
        }
        const nextRun = scheduler.next_run ? new Date(scheduler.next_run).toLocaleString() : 'N/A';
        const lastRun = scheduler.last_run ? new Date(scheduler.last_run).toLocaleString() : 'Never';

        return `
          <div class="mb-3">
            <div class="collapsible-header" onclick="this.parentElement.classList.toggle('expanded')" style="cursor: pointer; display: flex; align-items: center; justify-content: space-between; padding: 8px; background: rgba(16, 185, 129, 0.1); border-radius: 6px; border: 1px solid rgba(16, 185, 129, 0.3);">
              <span style="color: #10b981; font-weight: 600;">
                <span style="margin-right: 6px;">⏰</span> Schedule
                <span class="badge bg-success ms-2" style="font-size: 0.7rem;">${scheduler.enabled ? 'Active' : 'Disabled'}</span>
              </span>
              <svg class="collapse-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#10b981" stroke-width="2" style="transition: transform 0.2s;">
                <polyline points="6 9 12 15 18 9"></polyline>
              </svg>
            </div>
            <div class="collapsible-content" style="max-height: 0; overflow: hidden; transition: max-height 0.3s ease;">
              <div style="padding: 10px; background: rgba(16, 185, 129, 0.05); border-radius: 0 0 6px 6px; border: 1px solid rgba(16, 185, 129, 0.2); border-top: none;">
                <div style="font-size: 0.85rem; color: var(--text-secondary);">
                  <div class="mb-1"><strong>Name:</strong> ${scheduler.name || 'Unnamed'}</div>
                  <div class="mb-1"><strong>Type:</strong> ${scheduleDesc}</div>
                  <div class="mb-1"><strong>Next Run:</strong> ${nextRun}</div>
                  <div class="mb-1"><strong>Last Run:</strong> ${lastRun}</div>
                  <div><strong>Executions:</strong> ${scheduler.execution_count || 0} (${scheduler.failure_count || 0} failed)</div>
                </div>
              </div>
            </div>
          </div>
          <style>
            .collapsible-header:hover { background: rgba(16, 185, 129, 0.15) !important; }
            .expanded .collapsible-content { max-height: 200px !important; }
            .expanded .collapse-icon { transform: rotate(180deg); }
          </style>
        `;
      })()}
      ${(() => {
        // Find store node connected to this task's agent
        const assignedNodeId = task.assigned_node_id || task.assignedNodeId;
        if (!assignedNodeId) return '';

        const storeNodes = window.agentCanvas?.state?.storeNodes || [];
        const store = storeNodes.find(s => s.agent_node_id === assignedNodeId);
        if (!store) return '';

        const lastWrite = store.last_write_time && store.last_write_time !== '0001-01-01T00:00:00Z'
          ? new Date(store.last_write_time).toLocaleString()
          : 'Never';

        return `
          <div class="mb-3">
            <div class="collapsible-header-store" onclick="this.parentElement.classList.toggle('expanded-store')" style="cursor: pointer; display: flex; align-items: center; justify-content: space-between; padding: 8px; background: rgba(20, 184, 166, 0.1); border-radius: 6px; border: 1px solid rgba(20, 184, 166, 0.3);">
              <span style="color: #14b8a6; font-weight: 600;">
                <span style="margin-right: 6px;">💾</span> Storage
                <span class="badge ms-2" style="font-size: 0.7rem; background: #14b8a6;">${store.write_count || 0} writes</span>
              </span>
              <svg class="collapse-icon-store" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#14b8a6" stroke-width="2" style="transition: transform 0.2s;">
                <polyline points="6 9 12 15 18 9"></polyline>
              </svg>
            </div>
            <div class="collapsible-content-store" style="max-height: 0; overflow: hidden; transition: max-height 0.3s ease;">
              <div style="padding: 10px; background: rgba(20, 184, 166, 0.05); border-radius: 0 0 6px 6px; border: 1px solid rgba(20, 184, 166, 0.2); border-top: none;">
                <div style="font-size: 0.85rem; color: var(--text-secondary);">
                  <div class="mb-1"><strong>Store:</strong> ${store.name || 'Unnamed'}</div>
                  <div class="mb-1"><strong>Path:</strong> <span style="font-family: monospace; font-size: 0.8rem;">${store.base_dir}</span></div>
                  <div class="mb-1"><strong>Format:</strong> ${(store.format || 'json').toUpperCase()}</div>
                  <div class="mb-1"><strong>Last Write:</strong> ${lastWrite}</div>
                  ${store.last_file_path ? `<div><strong>Last File:</strong> <span style="font-family: monospace; font-size: 0.8rem;">${store.last_file_path}</span></div>` : ''}
                </div>
              </div>
            </div>
          </div>
          <style>
            .collapsible-header-store:hover { background: rgba(20, 184, 166, 0.15) !important; }
            .expanded-store .collapsible-content-store { max-height: 200px !important; }
            .expanded-store .collapse-icon-store { transform: rotate(180deg); }
          </style>
        `;
      })()}
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

  // Show/hide unassign button based on task assignment
  const unassignBtn = document.getElementById('unassign-task-btn');
  if (unassignBtn) {
    // Check if this task is feeding into a combiner
    const isCombinerInput = window.agentCanvas?.state?.tasks?.some(t =>
      (t.combiner_type || t.combinerType) &&
      (t.input_task_ids || []).includes(task.id)
    );

    // Show button only if task is assigned to an agent (not empty, not 'unassigned', and not a combiner input)
    if (task.to && task.to !== 'unassigned' && !isCombinerInput) {
      unassignBtn.style.display = 'block';
    } else {
      unassignBtn.style.display = 'none';
    }
  }

  // Store the current task for actions (edit, delete, etc.)
  if (window.agentCanvas && window.agentCanvas.state) {
    window.agentCanvas.state.expandedTask = task;
  }

  console.log('[SIDEBAR] Task details populated');
}

function escapeHTML(str) {
  if (!str) return '';
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

/**
 * Show attachment details in the sidebar
 */
function showAttachmentDetails(att) {
  // Hide other panels
  hideAgentDetails();
  const taskPanel = document.getElementById('task-details-panel');
  if (taskPanel) taskPanel.style.display = 'none';

  // Close canvas panels
  if (window.agentCanvas && window.agentCanvas.state) {
    window.agentCanvas.state.expandedPanelWidth = 0;
    window.agentCanvas.state.expandedTask = null;
    window.agentCanvas.state.expandedAgentPanelWidth = 0;
    window.agentCanvas.state.expandedAgent = null;
    window.agentCanvas.state.expandedCombinerPanelWidth = 0;
    window.agentCanvas.state.expandedCombiner = null;
    if (window.agentCanvas.draw) window.agentCanvas.draw();
  }

  const panel = document.getElementById('attachment-details-panel');
  const content = document.getElementById('attachment-details-content');
  if (!panel || !content) return;

  panel.style.display = 'block';

  const badgeColor = att.color || '#3b82f6';
  const typeLabel = (att.type || 'other').toUpperCase();
  const fileMeta = att.file || att.file_meta;
  const bodyHtml = att.body
    ? (window.marked ? window.marked.parse(att.body) : `<pre style="white-space: pre-wrap; margin:0;">${escapeHTML(att.body)}</pre>`)
    : '<div class="text-muted" style="font-size: 0.85rem;">No body</div>';

  const linkHtml = att.link_url
    ? `<a href="${att.link_url}" target="_blank" rel="noopener" style="font-size: 0.85rem;">${att.link_url}</a>`
    : '<span class="text-muted" style="font-size: 0.85rem;">No link</span>';

  // Helper function to check if URL is a web URL (http/https)
  const isWebUrl = (url) => url && (url.startsWith('http://') || url.startsWith('https://'));

  const fileHtml = fileMeta && (fileMeta.name || fileMeta.url)
    ? `<div style="font-size: 0.85rem;">${fileMeta.name || 'File'}${fileMeta.size ? ` • ${(fileMeta.size/1024).toFixed(1)} KB` : ''}${fileMeta.url ? (isWebUrl(fileMeta.url) ? ` • <a href="${fileMeta.url}" target="_blank" rel="noopener">open</a>` : ` • <span style="color: var(--text-secondary); font-family: monospace;">${fileMeta.url}</span>`) : ''}</div>`
    : '<span class="text-muted" style="font-size: 0.85rem;">No file</span>';

  const created = att.created_at ? new Date(att.created_at).toLocaleString() : '—';
  const updated = att.updated_at ? new Date(att.updated_at).toLocaleString() : '—';

  content.innerHTML = `
    <div class="mb-2 d-flex align-items-center gap-2">
      <div style="width: 12px; height: 12px; border-radius: 50%; background: ${badgeColor};"></div>
      <strong style="color: var(--text-primary);">${escapeHTML(att.title || 'Attachment')}</strong>
    </div>
    <div class="mb-3">
      <span class="badge" style="background:${badgeColor}; color: #fff; font-size: 0.7rem;">${typeLabel}</span>
    </div>
    <div class="mb-3 d-flex gap-2">
      <button class="btn btn-sm btn-outline-primary flex-fill" onclick="editAttachment('${att.id || ''}')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="vertical-align: text-bottom;">
          <path d="M12 20h9"></path>
          <path d="M16.5 3.5a2.121 2.121 0 1 1 3 3L7 19l-4 1 1-4 12.5-12.5z"></path>
        </svg>
        Edit
      </button>
      <button class="btn btn-sm btn-outline-secondary flex-fill" onclick="editAttachmentColor('${att.id || ''}')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="vertical-align: text-bottom;">
          <circle cx="12" cy="12" r="10"></circle>
          <path d="M12 2v20"></path>
          <path d="M2 12h20"></path>
        </svg>
        Color
      </button>
    </div>
    <div class="mb-3">
      <strong style="color: var(--text-primary);">Link:</strong><br>${linkHtml}
    </div>
    <div class="mb-3">
      <strong style="color: var(--text-primary);">File:</strong><br>${fileHtml}
    </div>
    <div class="mb-3">
      <strong style="color: var(--text-primary);">Body:</strong>
      <div class="mt-2" style="max-height: 240px; overflow-y: auto;">${bodyHtml}</div>
    </div>
    <div class="mb-2 text-muted" style="font-size: 0.8rem;">
      Created: ${created}<br>
      Updated: ${updated}
    </div>
  `;
}

/**
 * Hide task details panel
 */
function hideTaskDetails() {
  console.log('[SIDEBAR] hideTaskDetails called');
  const panel = document.getElementById('task-details-panel');
  if (panel) {
    console.log('[SIDEBAR] Hiding task panel, current display:', panel.style.display);
    panel.style.display = 'none';
    console.log('[SIDEBAR] Task panel hidden');
  } else {
    console.error('[SIDEBAR] Task panel not found!');
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
function showAddTaskModal() {
  const modal = new bootstrap.Modal(document.getElementById('addTaskNodeModal'));
  const descriptionInput = document.getElementById('taskDescription');
  const priorityInput = document.getElementById('taskPriority');

  // Reset form
  if (descriptionInput) descriptionInput.value = '';
  if (priorityInput) priorityInput.value = 'medium';

  modal.show();

  // Focus on description after modal is shown
  setTimeout(() => {
    if (descriptionInput) descriptionInput.focus();
  }, 500);
}

/**
 * Show add attachment modal
 */
function showAddAttachmentModal() {
  const modal = new bootstrap.Modal(document.getElementById('addAttachmentNodeModal'));
  const titleInput = document.getElementById('attachmentTitle');
  const bodyInput = document.getElementById('attachmentBody');
  const linkInput = document.getElementById('attachmentLink');
  const filePathInput = document.getElementById('attachmentFilePath');

  // Reset form
  if (titleInput) titleInput.value = '';
  if (bodyInput) bodyInput.value = '';
  if (linkInput) linkInput.value = '';
  if (filePathInput) filePathInput.value = '';

  modal.show();

  // Focus on title after modal is shown
  setTimeout(() => {
    if (titleInput) titleInput.focus();
  }, 500);
}

function guessAttachmentType(body, link, fileMeta) {
  const lowerLink = (link || '').toLowerCase();
  const lowerName = (fileMeta?.name || '').toLowerCase();
  const imageExts = ['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.tif', '.tiff', '.svg'];

  const isImage = (src) => imageExts.some(ext => src.endsWith(ext));

  if (fileMeta && isImage(lowerName)) return 'image';
  if (lowerLink && isImage(lowerLink)) return 'image';
  if (fileMeta && (fileMeta.mime || '').toLowerCase().startsWith('image/')) return 'image';

  // Fallback: doc for text-ish, other otherwise
  if (body && body.length > 0) return 'doc';
  return 'other';
}

function getFileNameFromPath(path) {
  if (!path) return '';
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

/**
 * Basic prompt-based editor for an attachment (excludes color)
 */
async function editAttachment(attachmentId) {
  if (!attachmentId || !window.agentCanvas) return;
  const existing = window.agentCanvas.state.attachments.find(a => a.id === attachmentId);
  if (!existing) {
    alert('Attachment not found');
    return;
  }

  const title = prompt('Title', existing.title || '') ?? existing.title;
  const body = prompt('Body (markdown ok)', existing.body || '') ?? existing.body;
  const link = prompt('Link URL', existing.link_url || '') ?? existing.link_url;
  const filePath = prompt('File path/URL', (existing.file || existing.file_meta || {}).url || '') || '';
  const fileMeta = filePath ? { name: getFileNameFromPath(filePath), url: filePath } : (existing.file || existing.file_meta || null);

  try {
    const resp = await fetch(`/api/studios/${window.agentCanvas.studioId}/attachments/${attachmentId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title,
        body,
        link_url: link,
        file_meta: fileMeta,
        type: guessAttachmentType(body, link, fileMeta)
      })
    });
    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(text || 'Failed to update attachment');
    }
    // Refresh
    if (window.agentCanvas && window.agentCanvas.init) {
      await window.agentCanvas.init();
    }
    if (window.showAttachmentDetails) {
      const updated = window.agentCanvas.state.attachments.find(a => a.id === attachmentId);
      if (updated) window.showAttachmentDetails(updated);
    }
  } catch (err) {
    console.error('Failed to update attachment', err);
    alert('Failed to update attachment: ' + err.message);
  }
}

/**
 * Edit attachment node color only
 */
async function editAttachmentColor(attachmentId) {
  if (!attachmentId || !window.agentCanvas) return;
  const existing = window.agentCanvas.state.attachments.find(a => a.id === attachmentId);
  if (!existing) {
    alert('Attachment not found');
    return;
  }

  // Show color picker modal
  showColorPickerModal(attachmentId, existing.color || '#e2e8f0');
}

/**
 * Show color picker modal with color palette
 */
function showColorPickerModal(attachmentId, currentColor) {
  // Define color palette (nice selection of colors)
  const colors = [
    '#ef4444', '#f97316', '#f59e0b', '#eab308', '#84cc16', '#22c55e',
    '#10b981', '#14b8a6', '#06b6d4', '#0ea5e9', '#3b82f6', '#6366f1',
    '#8b5cf6', '#a855f7', '#d946ef', '#ec4899', '#f43f5e', '#be123c',
    '#dc2626', '#ea580c', '#d97706', '#ca8a04', '#65a30d', '#16a34a',
    '#059669', '#0d9488', '#0891b2', '#0284c7', '#2563eb', '#4f46e5',
    '#7c3aed', '#9333ea', '#c026d3', '#db2777', '#e11d48', '#9f1239',
    '#991b1b', '#c2410c', '#b45309', '#a16207', '#4d7c0f', '#15803d',
    '#047857', '#115e59', '#155e75', '#075985', '#1e40af', '#3730a3',
    '#6d28d9', '#7e22ce', '#a21caf', '#be185d', '#be123c', '#7f1d1d',
    '#78350f', '#92400e', '#713f12', '#365314', '#14532d', '#064e3b',
    '#134e4a', '#164e63', '#1e3a8a', '#312e81', '#4c1d95', '#581c87',
    '#701a75', '#831843', '#881337', '#e2e8f0', '#cbd5e1', '#94a3b8',
    '#64748b', '#475569', '#334155', '#1e293b', '#0f172a', '#020617'
  ];

  // Populate color palette grid
  const grid = document.getElementById('color-palette-grid');
  grid.innerHTML = colors.map(color => `
    <div class="color-palette-item ${color === currentColor ? 'selected' : ''}"
         style="background-color: ${color};"
         data-color="${color}"
         onclick="selectColor('${color}', '${attachmentId}')">
    </div>
  `).join('');

  // Set current color in custom input
  document.getElementById('custom-color-input').value = currentColor;

  // Store attachment ID for later use
  window.currentColorPickerAttachmentId = attachmentId;

  // Show modal
  const modal = new bootstrap.Modal(document.getElementById('colorPickerModal'));
  modal.show();
}

/**
 * Select a color from the palette
 */
async function selectColor(color, attachmentId) {
  // Update selected state in UI
  document.querySelectorAll('.color-palette-item').forEach(item => {
    item.classList.remove('selected');
  });
  document.querySelector(`[data-color="${color}"]`)?.classList.add('selected');

  // Update the color
  await updateAttachmentColor(attachmentId, color);

  // Close modal
  const modal = bootstrap.Modal.getInstance(document.getElementById('colorPickerModal'));
  if (modal) modal.hide();
}

/**
 * Apply custom color from input field
 */
async function applyCustomColor() {
  const input = document.getElementById('custom-color-input');
  const color = input.value.trim();
  const attachmentId = window.currentColorPickerAttachmentId;

  // Validate hex color
  if (!/^#[0-9A-Fa-f]{6}$/.test(color)) {
    alert('Please enter a valid hex color (e.g., #3b82f6)');
    return;
  }

  await updateAttachmentColor(attachmentId, color);

  // Close modal
  const modal = bootstrap.Modal.getInstance(document.getElementById('colorPickerModal'));
  if (modal) modal.hide();
}

/**
 * Update attachment color via API
 */
async function updateAttachmentColor(attachmentId, color) {
  if (!attachmentId || !window.agentCanvas) return;

  try {
    const resp = await fetch(`/api/studios/${window.agentCanvas.studioId}/attachments/${attachmentId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        color: color
      })
    });
    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(text || 'Failed to update attachment color');
    }
    // Refresh
    if (window.agentCanvas && window.agentCanvas.init) {
      await window.agentCanvas.init();
    }
    if (window.showAttachmentDetails) {
      const updated = window.agentCanvas.state.attachments.find(a => a.id === attachmentId);
      if (updated) window.showAttachmentDetails(updated);
    }
  } catch (err) {
    console.error('Failed to update attachment color', err);
    alert('Failed to update attachment color: ' + err.message);
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

  // Keep existing assignment; do not prompt for agent
  const assignTo = task.to || '';
  const assignedNodeId = task.assigned_node_id || '';

  try {
    const response = await fetch(`/api/studios/${currentStudioId}/tasks/${task.id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: newDescription.trim(),
        to: assignTo,
        assigned_node_id: assignedNodeId,
        from: task.from || ''
      })
    });

    if (response.ok) {
      console.log('Task updated successfully');
      // Update the task locally
      task.description = newDescription.trim();
      task.to = assignTo;
      task.assigned_node_id = assignedNodeId;

      // Refresh the task details panel
      showTaskDetails(task);

      // Re-initialize canvas to fetch updated task data from backend
      window.agentCanvas.init();
    } else {
      const error = await response.text();
      alert(`Failed to update task: ${error}`);
    }
  } catch (error) {
    console.error('Error updating task:', error);
    alert(`Error updating task: ${error.message}`);
  }
}

/**
 * Unassign current task
 */
async function unassignCurrentTask() {
  if (!window.agentCanvas) {
    alert('Canvas not initialized');
    return;
  }

  const task = window.agentCanvas.state.expandedTask;
  if (!task) {
    alert('No task selected');
    return;
  }

  if (!task.to || task.to === 'unassigned') {
    alert('Task is not assigned to any agent');
    return;
  }

  try {
    // Use orchestration endpoint so subsequent assignments work the same way
    const response = await fetch('/api/orchestration/tasks', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        task_id: task.id,
        to: 'unassigned',
        status: 'pending',
        result: null,
        error: null,
        progress: 0
      })
    });

    if (response.ok) {
      console.log('Task unassigned successfully');
      // Update the task locally
      task.to = 'unassigned';
      task.status = 'pending';
      task.result = null;
      task.error = null;
      task.progress = 0;
      task.started_at = null;
      task.completed_at = null;
      // Keep state array in sync in case we're not holding the same object
      const stateTask = window.agentCanvas?.state?.tasks?.find(t => t.id === task.id);
      if (stateTask) {
        stateTask.to = 'unassigned';
        stateTask.status = 'pending';
        stateTask.result = null;
        stateTask.error = null;
        stateTask.progress = 0;
        stateTask.started_at = null;
        stateTask.completed_at = null;
      }

      // Refresh the task details panel
      showTaskDetails(task);

      // Redraw canvas
      window.agentCanvas.draw();
    } else {
      const error = await response.text();
      alert(`Failed to unassign task: ${error}`);
    }
  } catch (error) {
    console.error('Error unassigning task:', error);
    alert(`Error unassigning task: ${error.message}`);
  }
}

function showSchedulerDetails(schedulerNode) {
  console.log('[SIDEBAR] showSchedulerDetails called for:', schedulerNode.name);
  const canvas = window.agentCanvas;
  const tasks = canvas?.state?.tasks || [];
  const targetTask = tasks.find(t => t.id === schedulerNode.target_task_id);

  // Hide other details
  hideAgentDetails();
  hideAttachmentDetails();
  const taskPanel = document.getElementById('task-details-panel');
  if (taskPanel) taskPanel.style.display = 'none';

  // Use the same task details panel but rename title
  const panel = document.getElementById('task-details-panel');
  const content = document.getElementById('task-details-content');

  if (!panel || !content) {
    console.error('[SIDEBAR] Panel or content not found!');
    return;
  }

  // Show panel
  panel.style.display = 'block';

  // Change title
  const titleElement = panel.querySelector('h6');
  if (titleElement) {
    titleElement.textContent = 'Scheduler Details';
  }

  // Extract current schedule values
  const sched = schedulerNode.schedule || {};
  let schedType = sched.type || 'interval';
  // Map relative_delay to interval for UI display (merged types)
  const isRelativeDelay = schedType === 'relative_delay';
  if (isRelativeDelay) {
    schedType = 'interval';
  }
  // Get interval minutes from either interval or delay_duration field
  const intervalMins = isRelativeDelay
    ? Math.floor((sched.delay_duration || 0) / (60 * 1e9))
    : (schedType === 'interval' ? Math.floor((sched.interval || 0) / (60 * 1e9)) : 60);
  const runOnce = isRelativeDelay && sched.trigger_once;
  const timeOfDay = sched.time_of_day || '09:00';
  const dayOfWeek = sched.day_of_week || 0;
  const cronExpr = sched.cron_expr || '0 9 * * *';
  // For 'once' type, format the execute_at datetime for the input
  const executeAt = sched.execute_at ? new Date(sched.execute_at).toISOString().slice(0, 16) : '';

  // Build task options for dropdown
  let taskOptions = '<option value="">-- Not linked --</option>';
  tasks.forEach(t => {
    const selected = t.id === schedulerNode.target_task_id ? 'selected' : '';
    const label = t.description || t.id;
    taskOptions += `<option value="${t.id}" ${selected}>${label}</option>`;
  });

  // Day of week options
  const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
  let dayOptions = '';
  days.forEach((day, idx) => {
    const selected = idx === dayOfWeek ? 'selected' : '';
    dayOptions += `<option value="${idx}" ${selected}>${day}</option>`;
  });

  const html = `
    <div class="mb-3">
      <div style="font-size: 24px; text-align: center; margin-bottom: 10px;">⏰</div>
    </div>

    <!-- Editable Name -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Name</label>
      <input type="text" id="scheduler-name" class="form-control form-control-sm"
             value="${schedulerNode.name || 'Scheduler'}"
             style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);">
    </div>

    <!-- Enabled Toggle -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Status</label>
      <div class="form-check form-switch">
        <input class="form-check-input" type="checkbox" id="scheduler-enabled"
               ${schedulerNode.enabled !== false ? 'checked' : ''}>
        <label class="form-check-label" for="scheduler-enabled" style="color: var(--text-secondary);">
          ${schedulerNode.enabled !== false ? 'Enabled' : 'Paused'}
        </label>
      </div>
    </div>

    <!-- Linked Task -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Linked Task</label>
      <select id="scheduler-target-task" class="form-select form-select-sm"
              style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);">
        ${taskOptions}
      </select>
    </div>

    <!-- Schedule Type -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Schedule Type</label>
      <select id="scheduler-type" class="form-select form-select-sm"
              style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);"
              onchange="updateSchedulerTypeFields()">
        <option value="once" ${schedType === 'once' ? 'selected' : ''}>One-time (specific date/time)</option>
        <option value="interval" ${schedType === 'interval' ? 'selected' : ''}>Interval</option>
        <option value="daily" ${schedType === 'daily' ? 'selected' : ''}>Daily</option>
        <option value="weekly" ${schedType === 'weekly' ? 'selected' : ''}>Weekly</option>
        <option value="cron" ${schedType === 'cron' ? 'selected' : ''}>Cron Expression</option>
      </select>
    </div>

    <!-- Once (specific date/time) fields -->
    <div id="scheduler-once-fields" class="mb-3" style="display: ${schedType === 'once' ? 'block' : 'none'};">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Execute At</label>
      <input type="datetime-local" id="scheduler-execute-at" class="form-control form-control-sm"
             value="${executeAt}"
             style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);">
      <small style="color: var(--text-muted);">Select the exact date and time for execution</small>
    </div>

    <!-- Interval fields -->
    <div id="scheduler-interval-fields" style="display: ${schedType === 'interval' ? 'block' : 'none'};">
      <div class="mb-3">
        <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Interval (minutes)</label>
        <input type="number" id="scheduler-interval" class="form-control form-control-sm"
               value="${intervalMins}" min="1"
               style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);">
      </div>
      <div class="mb-3">
        <div class="form-check">
          <input class="form-check-input" type="checkbox" id="scheduler-run-once"
                 ${runOnce ? 'checked' : ''}>
          <label class="form-check-label" for="scheduler-run-once" style="color: var(--text-secondary);">
            Run once only (don't repeat)
          </label>
        </div>
      </div>
    </div>

    <!-- Daily fields -->
    <div id="scheduler-daily-fields" class="mb-3" style="display: ${schedType === 'daily' ? 'block' : 'none'};">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Time of Day</label>
      <input type="time" id="scheduler-time-daily" class="form-control form-control-sm"
             value="${timeOfDay}"
             style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);">
    </div>

    <!-- Weekly fields -->
    <div id="scheduler-weekly-fields" style="display: ${schedType === 'weekly' ? 'block' : 'none'};">
      <div class="mb-3">
        <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Day of Week</label>
        <select id="scheduler-day-of-week" class="form-select form-select-sm"
                style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);">
          ${dayOptions}
        </select>
      </div>
      <div class="mb-3">
        <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Time of Day</label>
        <input type="time" id="scheduler-time-weekly" class="form-control form-control-sm"
               value="${timeOfDay}"
               style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);">
      </div>
    </div>

    <!-- Cron fields -->
    <div id="scheduler-cron-fields" class="mb-3" style="display: ${schedType === 'cron' ? 'block' : 'none'};">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Cron Expression</label>
      <input type="text" id="scheduler-cron" class="form-control form-control-sm"
             value="${cronExpr}" placeholder="0 9 * * *"
             style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);">
      <small style="color: var(--text-muted);">Format: minute hour day month weekday</small>
    </div>


    <!-- Prompt -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Task Prompt</label>
      <textarea id="scheduler-prompt" class="form-control form-control-sm" rows="3"
                style="background: var(--input-bg); border-color: var(--border-color); color: var(--text-primary);"
                placeholder="Optional prompt for scheduled task">${schedulerNode.prompt || ''}</textarea>
    </div>

    <!-- Read-only stats -->
    ${schedulerNode.next_run ? `
      <div class="mb-2">
        <small style="color: var(--text-muted);">Next Run:</small>
        <div style="color: var(--text-secondary); font-size: 0.85rem;">${new Date(schedulerNode.next_run).toLocaleString()}</div>
      </div>
    ` : ''}
    ${schedulerNode.last_run ? `
      <div class="mb-2">
        <small style="color: var(--text-muted);">Last Run:</small>
        <div style="color: var(--text-secondary); font-size: 0.85rem;">${new Date(schedulerNode.last_run).toLocaleString()}</div>
      </div>
    ` : ''}
    <div class="mb-3">
      <small style="color: var(--text-muted);">Execution Count:</small>
      <div style="color: var(--text-secondary); font-size: 0.85rem;">${schedulerNode.execution_count || 0}</div>
    </div>

    <!-- Save Button -->
    <div class="mt-3">
      <button class="btn btn-primary btn-sm w-100" onclick="saveSchedulerDetails(window.currentSchedulerNode)">
        Save Changes
      </button>
    </div>
  `;

  content.innerHTML = html;

  // Store the scheduler node reference for saving
  window.currentSchedulerNode = schedulerNode;

  // Update enabled label on toggle change
  const enabledCheckbox = document.getElementById('scheduler-enabled');
  if (enabledCheckbox) {
    enabledCheckbox.addEventListener('change', function() {
      const label = this.nextElementSibling;
      if (label) {
        label.textContent = this.checked ? 'Enabled' : 'Paused';
      }
    });
  }
}

/**
 * Update schedule type-specific fields visibility
 */
function updateSchedulerTypeFields() {
  const schedType = document.getElementById('scheduler-type')?.value || 'interval';

  // Hide all type-specific fields
  const fieldGroups = ['once', 'interval', 'daily', 'weekly', 'cron'];
  fieldGroups.forEach(group => {
    const el = document.getElementById(`scheduler-${group}-fields`);
    if (el) el.style.display = 'none';
  });

  // Show relevant fields
  switch (schedType) {
    case 'once':
      document.getElementById('scheduler-once-fields').style.display = 'block';
      break;
    case 'interval':
      document.getElementById('scheduler-interval-fields').style.display = 'block';
      break;
    case 'daily':
      document.getElementById('scheduler-daily-fields').style.display = 'block';
      break;
    case 'weekly':
      document.getElementById('scheduler-weekly-fields').style.display = 'block';
      break;
    case 'cron':
      document.getElementById('scheduler-cron-fields').style.display = 'block';
      break;
  }
}

/**
 * Save scheduler node details
 */
async function saveSchedulerDetails(schedulerNode) {
  const canvas = window.agentCanvas;
  if (!canvas || !schedulerNode) {
    console.error('[SIDEBAR] Cannot save: missing canvas or schedulerNode');
    return;
  }

  const nodeId = schedulerNode.canvas_node_id;
  if (!nodeId) {
    console.error('[SIDEBAR] Cannot save: missing canvas_node_id');
    return;
  }

  // Gather form values
  const name = document.getElementById('scheduler-name')?.value || 'Scheduler';
  const enabled = document.getElementById('scheduler-enabled')?.checked ?? true;
  const targetTaskId = document.getElementById('scheduler-target-task')?.value || '';
  const schedType = document.getElementById('scheduler-type')?.value || 'interval';
  const prompt = document.getElementById('scheduler-prompt')?.value || '';

  // Build schedule config based on type
  const schedule = { type: schedType };

  switch (schedType) {
    case 'once':
      const executeAtStr = document.getElementById('scheduler-execute-at')?.value;
      if (executeAtStr) {
        schedule.execute_at = new Date(executeAtStr).toISOString();
      }
      break;
    case 'interval':
      const intervalMins = parseInt(document.getElementById('scheduler-interval')?.value) || 60;
      const runOnce = document.getElementById('scheduler-run-once')?.checked || false;
      if (runOnce) {
        // Use relative_delay type with trigger_once=true for one-shot execution
        schedule.type = 'relative_delay';
        schedule.delay_duration = intervalMins * 60 * 1e9; // Convert to nanoseconds
        schedule.trigger_once = true;
      } else {
        schedule.interval = intervalMins * 60 * 1e9; // Convert to nanoseconds
      }
      break;
    case 'daily':
      schedule.time_of_day = document.getElementById('scheduler-time-daily')?.value || '09:00';
      break;
    case 'weekly':
      schedule.day_of_week = parseInt(document.getElementById('scheduler-day-of-week')?.value) || 0;
      schedule.time_of_day = document.getElementById('scheduler-time-weekly')?.value || '09:00';
      break;
    case 'cron':
      schedule.cron_expr = document.getElementById('scheduler-cron')?.value || '0 9 * * *';
      break;
  }

  // Build update request
  const updateData = {
    name: name,
    enabled: enabled,
    schedule: schedule,
    prompt: prompt
  };

  // Only include target_task_id if set
  if (targetTaskId) {
    updateData.target_task_id = targetTaskId;
  }

  console.log('[SIDEBAR] Saving scheduler details:', updateData);

  try {
    const response = await fetch(`/api/orchestration/workspaces/${canvas.studioId}/scheduler-nodes/${nodeId}?studio_id=${canvas.studioId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updateData)
    });

    if (response.ok) {
      console.log('[SIDEBAR] Scheduler updated successfully');

      // Update local scheduler node data
      schedulerNode.name = name;
      schedulerNode.enabled = enabled;
      schedulerNode.schedule = schedule;
      schedulerNode.prompt = prompt;
      if (targetTaskId) {
        schedulerNode.target_task_id = targetTaskId;
      }

      // Update in canvas state
      if (canvas.state?.layout?.scheduler_nodes) {
        const idx = canvas.state.layout.scheduler_nodes.findIndex(sn => sn.canvas_node_id === nodeId);
        if (idx >= 0) {
          Object.assign(canvas.state.layout.scheduler_nodes[idx], schedulerNode);
        }
      }

      // Redraw canvas
      canvas.draw();

      // Show success feedback
      const saveBtn = document.querySelector('#task-details-content button.btn-primary');
      if (saveBtn) {
        const originalText = saveBtn.textContent;
        saveBtn.textContent = 'Saved!';
        saveBtn.classList.remove('btn-primary');
        saveBtn.classList.add('btn-success');
        setTimeout(() => {
          saveBtn.textContent = originalText;
          saveBtn.classList.remove('btn-success');
          saveBtn.classList.add('btn-primary');
        }, 1500);
      }
    } else {
      const error = await response.text();
      console.error('[SIDEBAR] Failed to save scheduler:', error);
      alert(`Failed to save scheduler: ${error}`);
    }
  } catch (error) {
    console.error('[SIDEBAR] Error saving scheduler:', error);
    alert(`Error saving scheduler: ${error.message}`);
  }
}

/**
 * Show Store Details in the sidebar with edit capabilities
 */
function showStoreDetails(storeNode) {
  console.log('[SIDEBAR] showStoreDetails called for:', storeNode.name);

  // Hide other details
  hideAgentDetails();
  hideAttachmentDetails();
  const taskPanel = document.getElementById('task-details-panel');
  if (taskPanel) taskPanel.style.display = 'none';

  // Use the same task details panel but rename title
  const panel = document.getElementById('task-details-panel');
  const content = document.getElementById('task-details-content');

  if (!panel || !content) {
    console.error('[SIDEBAR] Panel or content not found!');
    return;
  }

  // Show panel
  panel.style.display = 'block';

  // Change title
  const titleElement = panel.querySelector('h6');
  if (titleElement) {
    titleElement.textContent = 'Store Details';
  }

  // Find assigned agent if any
  const canvas = window.agentCanvas;
  const agents = canvas?.state?.agents || [];
  const assignedAgent = storeNode.agent_node_id
    ? agents.find(a => a.nodeId === storeNode.agent_node_id || a.id === storeNode.agent_node_id)
    : null;
  const assignedAgentLabel = assignedAgent
    ? `${assignedAgent.name} #${assignedAgent.instanceNumber || 1}`
    : null;

  // Build agent options for dropdown
  const agentOptions = agents.map(a => {
    const nodeId = a.nodeId || a.id;
    const label = `${a.name} #${a.instanceNumber || 1}`;
    const selected = nodeId === storeNode.agent_node_id ? 'selected' : '';
    return `<option value="${nodeId}" ${selected}>${label}</option>`;
  }).join('');

  const html = `
    <div class="mb-3">
      <div style="font-size: 24px; text-align: center; margin-bottom: 10px;">💾</div>
    </div>

    <!-- Editable Name -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Name</label>
      <input type="text" id="store-edit-name" class="form-control form-control-sm"
             value="${storeNode.name || 'Store'}"
             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
    </div>

    <!-- Editable Base Directory -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Base Directory</label>
      <input type="text" id="store-edit-basedir" class="form-control form-control-sm"
             value="${storeNode.base_dir || ''}"
             placeholder="/path/to/store"
             style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary); font-family: monospace; font-size: 0.85rem;">
    </div>

    <!-- Assigned Agent Dropdown -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Assigned Agent</label>
      <select id="store-edit-agent" class="form-select form-select-sm"
              style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
        <option value="">Not assigned</option>
        ${agentOptions}
      </select>
    </div>

    <!-- Format Dropdown -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Format</label>
      <select id="store-edit-format" class="form-select form-select-sm"
              style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
        <option value="json" ${storeNode.format === 'json' ? 'selected' : ''}>JSON</option>
        <option value="text" ${storeNode.format === 'text' ? 'selected' : ''}>Text</option>
        <option value="markdown" ${storeNode.format === 'markdown' ? 'selected' : ''}>Markdown</option>
      </select>
    </div>

    <!-- Write Mode Dropdown -->
    <div class="mb-3">
      <label style="color: var(--text-primary); font-weight: 600; font-size: 0.85rem;">Write Mode</label>
      <select id="store-edit-writemode" class="form-select form-select-sm"
              style="background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary);">
        <option value="overwrite" ${storeNode.write_mode === 'overwrite' ? 'selected' : ''}>Overwrite</option>
        <option value="append" ${storeNode.write_mode === 'append' ? 'selected' : ''}>Append</option>
      </select>
    </div>

    <!-- Toggles -->
    <div class="mb-3">
      <div class="form-check form-switch">
        <input class="form-check-input" type="checkbox" id="store-edit-autocreate" ${storeNode.auto_create_dir ? 'checked' : ''}>
        <label class="form-check-label" for="store-edit-autocreate" style="color: var(--text-primary); font-size: 0.85rem;">
          Auto-create directory
        </label>
      </div>
    </div>

    <div class="mb-3">
      <div class="form-check form-switch">
        <input class="form-check-input" type="checkbox" id="store-edit-autostore" ${storeNode.auto_store !== false ? 'checked' : ''}>
        <label class="form-check-label" for="store-edit-autostore" style="color: var(--text-primary); font-size: 0.85rem;">
          Auto-store on task completion
        </label>
      </div>
    </div>

    <!-- Save Button -->
    <div class="mb-3">
      <button id="store-save-btn" class="btn btn-sm w-100" style="background: #14b8a6; color: white; font-weight: 500;">
        Save Changes
      </button>
    </div>

    <hr style="border-color: var(--border-color); margin: 1rem 0;">

    <!-- Stats (read-only) -->
    <div class="mb-2">
      <small style="color: var(--text-muted);">Statistics</small>
    </div>
    <div class="mb-2" style="font-size: 0.85rem;">
      <span style="color: var(--text-secondary);">Write Count:</span>
      <span style="color: #10b981; font-weight: bold;">${storeNode.write_count || 0}</span>
    </div>
    ${storeNode.last_write_time ? `
      <div class="mb-2" style="font-size: 0.85rem;">
        <span style="color: var(--text-secondary);">Last Write:</span>
        <span style="color: var(--text-primary);">${new Date(storeNode.last_write_time).toLocaleString()}</span>
      </div>
    ` : ''}
    ${storeNode.last_file_path ? `
      <div class="mb-2" style="font-size: 0.85rem;">
        <span style="color: var(--text-secondary);">Last File:</span>
        <div style="color: var(--text-primary); font-family: monospace; font-size: 0.75rem; word-break: break-all;">
          ${storeNode.last_file_path}
        </div>
      </div>
    ` : ''}
    ${storeNode.last_error && storeNode.last_error !== '' ? `
      <div class="mb-2">
        <div style="color: #ef4444; font-size: 0.85rem; padding: 8px; background: rgba(239, 68, 68, 0.1); border-radius: 4px;">
          ⚠️ ${storeNode.last_error}
        </div>
      </div>
    ` : ''}
    <div class="mb-2" style="font-size: 0.75rem;">
      <span style="color: var(--text-muted);">ID:</span>
      <span style="color: var(--text-secondary); font-family: monospace;">
        ${storeNode.canvas_node_id || storeNode.id || 'N/A'}
      </span>
    </div>
  `;

  content.innerHTML = html;

  // Attach save handler
  const saveBtn = document.getElementById('store-save-btn');
  if (saveBtn) {
    saveBtn.addEventListener('click', () => saveStoreDetails(storeNode));
  }
}

/**
 * Save store node details
 */
async function saveStoreDetails(storeNode) {
  const canvas = window.agentCanvas;
  if (!canvas || !canvas.studioId) {
    alert('Canvas not available');
    return;
  }

  const name = document.getElementById('store-edit-name')?.value?.trim();
  const baseDir = document.getElementById('store-edit-basedir')?.value?.trim();
  const agentNodeId = document.getElementById('store-edit-agent')?.value || null;
  const format = document.getElementById('store-edit-format')?.value;
  const writeMode = document.getElementById('store-edit-writemode')?.value;
  const autoCreateDir = document.getElementById('store-edit-autocreate')?.checked;
  const autoStore = document.getElementById('store-edit-autostore')?.checked;

  const nodeId = storeNode.canvas_node_id || storeNode.id;

  try {
    const response = await fetch(`/api/agentstudio/canvas/store-nodes/${nodeId}?studio_id=${canvas.studioId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: name,
        base_dir: baseDir,
        agent_node_id: agentNodeId,
        format: format,
        write_mode: writeMode,
        auto_create_dir: autoCreateDir,
        auto_store: autoStore
      })
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to update store node');
    }

    // Update local state
    storeNode.name = name;
    storeNode.base_dir = baseDir;
    storeNode.agent_node_id = agentNodeId;
    storeNode.format = format;
    storeNode.write_mode = writeMode;
    storeNode.auto_create_dir = autoCreateDir;
    storeNode.auto_store = autoStore;

    // Redraw canvas
    canvas.draw();

    // Show success
    if (canvas.notifications?.showNotification) {
      canvas.notifications.showNotification('Store node updated', 'success');
    }
  } catch (err) {
    console.error('Failed to save store details:', err);
    alert('Failed to save: ' + err.message);
  }
}

// Make functions globally available
window.showAgentDetails = showAgentDetails;
window.hideAgentDetails = hideAgentDetails;
window.showTaskDetails = showTaskDetails;
window.showSchedulerDetails = showSchedulerDetails;
window.showStoreDetails = showStoreDetails;
window.hideAttachmentDetails = hideAttachmentDetails;
window.showAttachmentDetails = showAttachmentDetails;
window.editAttachment = editAttachment;
window.editAttachmentColor = editAttachmentColor;
window.selectColor = selectColor;
window.applyCustomColor = applyCustomColor;
window.hideTaskDetails = hideTaskDetails;
window.showCombinerDetails = showCombinerDetails;
window.hideCombinerDetails = hideCombinerDetails;
  window.showAddTaskModal = showAddTaskModal;
  window.showAddAttachmentModal = showAddAttachmentModal;
window.editCurrentTask = editCurrentTask;
window.deleteCurrentTask = deleteCurrentTask;
window.unassignCurrentTask = unassignCurrentTask;

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
 * Add input task to any task (regular or combiner)
 * This is a unified function that works for all task types
 */
async function addTaskInput(taskId) {
  if (!window.agentCanvas) {
    alert('Canvas not initialized');
    return;
  }

  const task = window.agentCanvas.state.tasks.find(t => t.id === taskId);
  if (!task) {
    alert('Task not found');
    return;
  }

  // Get list of all other tasks (exclude self)
  const availableTasks = window.agentCanvas.state.tasks.filter(t => t.id !== taskId);

  if (availableTasks.length === 0) {
    alert('No other tasks available to add as input. Create some tasks first.');
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

  // Update task with new input
  const currentInputs = task.input_task_ids || [];
  if (currentInputs.includes(selectedTask.id)) {
    alert('This task is already an input');
    return;
  }

  const newInputs = [...currentInputs, selectedTask.id];

  try {
    const response = await fetch(`/api/studios/${currentStudioId}/tasks/${taskId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: task.description,
        to: task.to || '',
        from: task.from || '',
        input_task_ids: newInputs,
        ...(task.combiner_type && { combiner_type: task.combiner_type }),
        ...(task.result_combination_mode && { result_combination_mode: task.result_combination_mode })
      })
    });

    if (!response.ok) {
      throw new Error(`Failed to update task: ${response.statusText}`);
    }

    // Update local state
    task.input_task_ids = newInputs;

    // Refresh task details
    if (window.showTaskDetails) {
      window.showTaskDetails(task);
    }

    // Immediate redraw
    if (window.agentCanvas && window.agentCanvas.draw) {
      window.agentCanvas.draw();

      // Force another redraw after a short delay to ensure connections are visible
      setTimeout(() => {
        window.agentCanvas.draw();
      }, 100);
    }

    alert(`Added "${selectedTask.description.substring(0, 30)}..." as input`);
  } catch (error) {
    console.error('Failed to add input:', error);
    alert(`Failed to add input: ${error.message}`);
  }
}

window.addTaskInput = addTaskInput;

/**
 * Remove an input task from a task's input_task_ids array
 */
async function removeTaskInput(taskId, inputTaskIdToRemove) {
  if (!currentStudioId) {
    alert('No studio selected');
    return;
  }

  if (!window.agentCanvas || !window.agentCanvas.state || !window.agentCanvas.state.tasks) {
    alert('Canvas not initialized');
    return;
  }

  const task = window.agentCanvas.state.tasks.find(t => t.id === taskId);
  if (!task) {
    alert('Task not found');
    return;
  }

  if (!task.input_task_ids || !task.input_task_ids.includes(inputTaskIdToRemove)) {
    alert('Input task not found in task inputs');
    return;
  }

  // Confirm removal
  const inputTask = window.agentCanvas.state.tasks.find(t => t.id === inputTaskIdToRemove);
  const inputDesc = inputTask ? inputTask.description.substring(0, 40) : inputTaskIdToRemove.substring(0, 8);
  if (!confirm(`Remove input connection?\n\n"${inputDesc}..."\n\nThis will disconnect this input from the task.`)) {
    return;
  }

  // Remove the input task from the array
  const currentInputs = task.input_task_ids || [];
  const newInputs = currentInputs.filter(id => id !== inputTaskIdToRemove);

  try {
    const response = await fetch(`/api/studios/${currentStudioId}/tasks/${taskId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: task.description,
        to: task.to || '',
        from: task.from || '',
        input_task_ids: newInputs,
        ...(task.combiner_type && { combiner_type: task.combiner_type }),
        ...(task.result_combination_mode && { result_combination_mode: task.result_combination_mode })
      })
    });

    if (!response.ok) {
      throw new Error(`Failed to update task: ${response.statusText}`);
    }

    // Update local state
    task.input_task_ids = newInputs;

    // Refresh task details
    if (window.showTaskDetails) {
      window.showTaskDetails(task);
    }

    // Immediate redraw to remove the connection line
    if (window.agentCanvas && window.agentCanvas.draw) {
      window.agentCanvas.draw();

      // Force another redraw after a short delay to ensure connections are updated
      setTimeout(() => {
        window.agentCanvas.draw();
      }, 100);
    }

    console.log(`Removed input "${inputDesc}..." from task`);
  } catch (error) {
    console.error('Failed to remove input:', error);
    alert(`Failed to remove input: ${error.message}`);
  }
}

window.removeTaskInput = removeTaskInput;

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

        // Show all available agents (allow adding same agent multiple times for multiple instances)
        select.innerHTML = '<option value="">Select agent to add...</option>' +
            (data.agents || []).map(agent => `<option value="${agent.name}">${escapeHtml(agent.name)}</option>`).join('');
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
  console.log('[SIDEBAR] showAgentDetails (async) called for:', agent.name);

    // Hide task details if showing
    hideTaskDetails();
    hideAttachmentDetails();

    // Hide combiner details if showing
    if (typeof hideCombinerDetails === 'function') {
        hideCombinerDetails();
    }

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

  // Defaults to keep UI responsive even if fetch fails
  let agentSettings = null;
  let enabledPlugins = [];
  let agentType = 'tool-calling';
  let model = 'N/A';
  let temperature = 1.0;
  const lastResult = agent.lastResult || '';
  let systemPrompt = '';

  try {
    // Fetch agent details from API (returns actual runtime state with enabled plugins)
    const response = await fetch(`/api/agents/${encodeURIComponent(agent.name)}`);
    if (response.ok) {
      agentSettings = await response.json();
      // API returns enabled_plugins as an array
      enabledPlugins = agentSettings.enabled_plugins || [];
      agentType = agentSettings.type || 'tool-calling';
      model = agentSettings.model || 'N/A';
      temperature = agentSettings.temperature || 1.0;
      systemPrompt = agentSettings.system_prompt || '';
    }
  } catch (error) {
    console.error('Failed to load agent details:', error);
  }

  // Always render content (even if fetch failed)
  content.innerHTML = `
    <div class="mb-3">
      <div class="d-flex justify-content-between align-items-center mb-2">
        <strong style="color: var(--text-primary); font-size: 1rem;">${escapeHtml(agent.name)}</strong>
        <span class="modern-badge ${statusBadge}">${agent.status}</span>
      </div>
      <div class="small mb-2" style="color: var(--text-secondary); font-style: italic;">
        Color: <span style="display: inline-block; width: 14px; height: 14px; border-radius: 50%; background: ${agent.color}; vertical-align: middle; border: 1px solid rgba(0,0,0,0.2);"></span>
      </div>
      ${agent.nodeId ? `
        <div class="small mb-1" style="color: var(--text-secondary);">
          Node ID: <span style="font-family: monospace;">${escapeHtml(agent.nodeId)}</span>
        </div>
      ` : ''}
    </div>

    <div class="mb-3" style="border-top: 1px solid var(--border-color); padding-top: 0.75rem;">
      <div class="d-flex justify-content-between align-items-center mb-2">
        <h6 style="color: var(--text-primary); font-size: 0.875rem; font-weight: 600; margin: 0;">Agent Configuration</h6>
        <button class="modern-btn modern-btn-secondary" style="padding: 4px 8px; font-size: 0.75rem;" onclick="editAgentSettings('${escapeHtml(agent.name)}', '${escapeHtml(agentType)}', '${escapeHtml(model)}', ${temperature})">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
          </svg>
          Edit
        </button>
      </div>
      <div class="small" style="color: var(--text-secondary);" id="agent-config-display">
        <div class="mb-1"><strong>Type:</strong> ${escapeHtml(agentType)}</div>
        <div class="mb-1"><strong>Model:</strong> ${escapeHtml(model)}</div>
        <div class="mb-1"><strong>Temperature:</strong> ${temperature}</div>
      </div>
      <div id="agent-config-edit" style="display: none;">
        <div class="mb-2">
          <label class="form-label small" style="color: var(--text-primary); margin-bottom: 0.25rem;">Type:</label>
          <select class="form-select form-select-sm" id="edit-agent-type">
            <option value="tool-calling" ${agentType === 'tool-calling' ? 'selected' : ''}>Tool-Calling</option>
            <option value="conversational" ${agentType === 'conversational' ? 'selected' : ''}>Conversational</option>
          </select>
        </div>
        <div class="mb-2">
          <label class="form-label small" style="color: var(--text-primary); margin-bottom: 0.25rem;">Model:</label>
          <input type="text" class="form-control form-control-sm" id="edit-agent-model" value="${escapeHtml(model)}">
        </div>
        <div class="mb-2">
          <label class="form-label small" style="color: var(--text-primary); margin-bottom: 0.25rem;">Temperature (0-2):</label>
          <input type="number" class="form-control form-control-sm" id="edit-agent-temperature" value="${temperature}" min="0" max="2" step="0.1">
        </div>
        <div class="d-flex gap-2 mt-2">
          <button class="modern-btn modern-btn-primary" style="padding: 4px 12px; font-size: 0.75rem; flex: 1;" onclick="saveAgentSettings('${escapeHtml(agent.name)}')">Save</button>
          <button class="modern-btn modern-btn-secondary" style="padding: 4px 12px; font-size: 0.75rem; flex: 1;" onclick="cancelEditAgentSettings()">Cancel</button>
        </div>
      </div>
    </div>

    ${lastResult ? `
      <div class="mb-3" style="border-top: 1px solid var(--border-color); padding-top: 0.75rem;">
        <h6 style="color: var(--text-primary); font-size: 0.875rem; font-weight: 600; margin-bottom: 0.5rem;">Last Result</h6>
        <div class="p-2" style="background: #0b1525; border: 1px solid var(--border-color); border-radius: 6px; color: var(--text-primary); font-family: monospace; font-size: 0.85rem; max-height: 200px; overflow-y: auto;">
          ${escapeHtml(lastResult.toString())}
        </div>
      </div>
    ` : ''}

    ${systemPrompt ? `
      <div class="mb-3" style="border-top: 1px solid var(--border-color); padding-top: 0.75rem;">
        <h6 style="color: var(--text-primary); font-size: 0.875rem; font-weight: 600; margin-bottom: 0.5rem;">System Prompt</h6>
        <div class="p-2" style="background: #0b1525; border: 1px solid var(--border-color); border-radius: 6px; color: var(--text-primary); font-family: monospace; font-size: 0.85rem; max-height: 240px; overflow-y: auto; white-space: pre-wrap;">
          ${escapeHtml(systemPrompt)}
        </div>
      </div>
    ` : ''}

    <div style="border-top: 1px solid var(--border-color); padding-top: 0.75rem;">
      <div class="d-flex justify-content-between align-items-center mb-2">
        <h6 style="color: var(--text-primary); font-size: 0.875rem; font-weight: 600; margin: 0;">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="vertical-align: text-bottom;">
            <path d="M22.7,19L13.6,9.9C14.5,7.6 14,4.9 12.1,3C10.1,1 7.1,0.6 4.7,1.7L9,6L6,9L1.6,4.7C0.4,7.1 0.9,10.1 2.9,12.1C4.8,14 7.5,14.5 9.8,13.6L18.9,22.7C19.3,23.1 19.9,23.1 20.3,22.7L22.6,20.4C23.1,20 23.1,19.3 22.7,19Z"/>
          </svg>
          Enabled Tools ${enabledPlugins.length > 0 ? `(${enabledPlugins.length})` : ''}
        </h6>
        <a href="/agents-detail.html?name=${encodeURIComponent(agent.name)}" class="modern-btn modern-btn-secondary" style="padding: 4px 8px; font-size: 0.7rem; text-decoration: none;" title="Configure plugins for this agent">
          <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.21,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.21,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.67 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
          </svg>
          Configure
        </a>
      </div>
      <div class="small" style="color: var(--text-secondary);">
        ${enabledPlugins.length > 0 ? enabledPlugins.map(plugin => `
          <div class="mb-1 p-2" style="background: rgba(124, 58, 237, 0.1); border: 1px solid rgba(124, 58, 237, 0.2); border-radius: 6px; display: flex; align-items: center;">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" style="color: #7c3aed; margin-right: 8px;">
              <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4M11,16.5L6.5,12L7.91,10.59L11,13.67L16.59,8.09L18,9.5L11,16.5Z"/>
            </svg>
            <span style="color: #7c3aed; font-weight: 500;">${escapeHtml(plugin)}</span>
          </div>
        `).join('') : `
          <div class="text-center py-3" style="color: var(--text-muted);">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="currentColor" class="mb-2" style="opacity: 0.4;">
              <path d="M22.7,19L13.6,9.9C14.5,7.6 14,4.9 12.1,3C10.1,1 7.1,0.6 4.7,1.7L9,6L6,9L1.6,4.7C0.4,7.1 0.9,10.1 2.9,12.1C4.8,14 7.5,14.5 9.8,13.6L18.9,22.7C19.3,23.1 19.9,23.1 20.3,22.7L22.6,20.4C23.1,20 23.1,19.3 22.7,19Z"/>
            </svg>
            <div style="font-style: italic;">No tools enabled</div>
            <div class="mt-2">
              <a href="/agents-detail.html?name=${encodeURIComponent(agent.name)}" class="modern-btn modern-btn-primary" style="padding: 6px 12px; font-size: 0.75rem; text-decoration: none;">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
                </svg>
                Enable Plugins
              </a>
            </div>
          </div>
        `}
      </div>
    </div>
  `;
}

/**
 * Open plugin configuration for a specific agent
 * Switches to the agent first, then navigates to plugins page
 */
async function openPluginConfigForAgent(agentName) {
  try {
    // Switch to this agent first
    const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
      method: 'PUT'
    });

    if (!response.ok) {
      console.error('Failed to switch agent:', await response.text());
      alert(`Failed to switch to agent "${agentName}". Please try again.`);
      return;
    }

    // Navigate to plugins page
    window.location.href = '/plugins';
  } catch (error) {
    console.error('Error switching agent:', error);
    alert(`Error: ${error.message}`);
  }
}
window.openPluginConfigForAgent = openPluginConfigForAgent;

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

    // Use backgroundColor property directly
    if (window.agentCanvas) {
        window.agentCanvas.backgroundColor = savedColor;
        window.agentCanvas.draw();
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
        window.agentCanvas.backgroundColor = color;
        window.agentCanvas.draw();
        localStorage.setItem('canvas-bg-color', color);
    }
}

/**
 * Return tasks that are ready to run: assigned, pending, and dependencies completed
 */
function getExecutableTasks() {
  if (!window.agentCanvas || !window.agentCanvas.state || !Array.isArray(window.agentCanvas.state.tasks)) {
    return [];
  }

  const tasks = window.agentCanvas.state.tasks;
  const tasksById = tasks.reduce((acc, task) => {
    if (task && task.id) acc[task.id] = task;
    return acc;
  }, {});

  return tasks.filter(task => {
    if (!task || !task.id) return false;
    const status = (task.status || '').toLowerCase();
    if (status !== 'pending') return false;
    if (!task.to || task.to === 'unassigned') return false;

    const deps = Array.isArray(task.input_task_ids) ? task.input_task_ids : [];
    return deps.every(depId => {
      const dep = tasksById[depId];
      return !dep || (dep.status && dep.status.toLowerCase() === 'completed');
    });
  });
}

/**
 * Execute all runnable tasks on the canvas (assigned + dependencies done)
 */
async function executeExecutableNodes() {
  const executable = getExecutableTasks();
  if (executable.length === 0) {
    window.agentCanvas?.showNotification?.('No executable nodes found', 'info');
    console.log('[CANVAS] No executable nodes to run');
    return;
  }

  console.log(`[CANVAS] Executing ${executable.length} runnable node(s)`);
  for (const task of executable) {
    try {
      await window.agentCanvas.executeTask(task);
    } catch (error) {
      console.error('Failed to execute task', task?.id, error);
      window.agentCanvas?.showNotification?.(
        `Failed to execute "${task?.description || task?.id || 'task'}": ${error.message}`,
        'error'
      );
    }
  }
}

/**
 * Pause/resume canvas animation loop and update the toggle button icon/title
 */
async function toggleCanvasAnimation() {
  if (!window.agentCanvas) {
    console.error('Canvas not initialized');
    return;
  }

  window.agentCanvas.animationPaused = !window.agentCanvas.animationPaused;
  const isPaused = window.agentCanvas.animationPaused;

  const btn = document.getElementById('animation-toggle');
  if (btn) {
    if (isPaused) {
      btn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M8,5.14V19.14L19,12.14L8,5.14Z"/></svg>';
      btn.title = 'Resume Animation';
    } else {
      btn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M14,19H18V5H14M6,19H10V5H6V19Z"/></svg>';
      btn.title = 'Pause Animation';
    }
  }

  // Always try to run runnable tasks when the control is used
  await executeExecutableNodes();
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

/**
 * Zoom to fit all nodes (agents and tasks) on canvas
 */
function zoomToFitCanvas() {
  if (!window.agentCanvas) {
    console.error('Canvas not initialized');
    return;
  }

  // Use zoomToFitContent which includes both tasks and agents
  window.agentCanvas.zoomToFitContent();
  console.log('🔍 Zoomed to fit all nodes');
}

/**
 * Reset canvas view to default zoom and position
 */
function resetCanvasView() {
  if (!window.agentCanvas) {
    console.error('Canvas not initialized');
    return;
  }

  window.agentCanvas.state.offsetX = 0;
  window.agentCanvas.state.offsetY = 0;
  window.agentCanvas.state.scale = 1;
  window.agentCanvas.draw();
  console.log('🔄 Reset canvas view to default');
}

// Export functions for global access
window.viewWorkspace = viewWorkspace;
window.openWorkspaceCanvas = openWorkspaceCanvas;
window.switchView = switchView;
/**
 * Edit agent settings
 */
function editAgentSettings(agentName, currentType, currentModel, currentTemp) {
    console.log('Editing agent settings:', agentName);
    const displayDiv = document.getElementById('agent-config-display');
    const editDiv = document.getElementById('agent-config-edit');

    if (displayDiv && editDiv) {
        displayDiv.style.display = 'none';
        editDiv.style.display = 'block';
    }
}

/**
 * Cancel editing agent settings
 */
function cancelEditAgentSettings() {
    const displayDiv = document.getElementById('agent-config-display');
    const editDiv = document.getElementById('agent-config-edit');

    if (displayDiv && editDiv) {
        displayDiv.style.display = 'block';
        editDiv.style.display = 'none';
    }
}

/**
 * Save agent settings
 */
async function saveAgentSettings(agentName) {
    const typeInput = document.getElementById('edit-agent-type');
    const modelInput = document.getElementById('edit-agent-model');
    const tempInput = document.getElementById('edit-agent-temperature');

    if (!typeInput || !modelInput || !tempInput) {
        alert('Error: Could not find input fields');
        return;
    }

    const newType = typeInput.value;
    const newModel = modelInput.value.trim();
    const newTemp = parseFloat(tempInput.value);

    // Validate inputs
    if (!newModel) {
        alert('Model cannot be empty');
        return;
    }

    if (isNaN(newTemp) || newTemp < 0 || newTemp > 2) {
        alert('Temperature must be between 0 and 2');
        return;
    }

    try {
        // Update agent via API
        const response = await fetch('/api/agents', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                name: agentName,
                type: newType,
                settings: {
                    model: newModel,
                    temperature: newTemp
                }
            })
        });

        if (!response.ok) {
            const error = await response.text();
            throw new Error(error);
        }

        // Success - update the display
        const displayDiv = document.getElementById('agent-config-display');
        if (displayDiv) {
            displayDiv.innerHTML = `
                <div class="mb-1"><strong>Type:</strong> ${escapeHtml(newType)}</div>
                <div class="mb-1"><strong>Model:</strong> ${escapeHtml(newModel)}</div>
                <div class="mb-1"><strong>Temperature:</strong> ${newTemp}</div>
            `;
        }

        // Hide edit form, show display
        cancelEditAgentSettings();

        // Show success notification if canvas is available
        if (window.agentCanvas && window.agentCanvas.showNotification) {
            window.agentCanvas.showNotification(`✅ Updated ${agentName} settings`, 'success');
        } else {
            alert(`Successfully updated ${agentName} settings`);
        }

    } catch (error) {
        console.error('Failed to update agent settings:', error);
        alert(`Failed to update agent: ${error.message}`);
    }
}

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
window.toggleCanvasAnimation = toggleCanvasAnimation;
window.executeExecutableNodes = executeExecutableNodes;
window.zoomToFitCanvas = zoomToFitCanvas;
window.resetCanvasView = resetCanvasView;
window.editAgentSettings = editAgentSettings;
window.cancelEditAgentSettings = cancelEditAgentSettings;
window.saveAgentSettings = saveAgentSettings;

// ============================================================================
// Scheduler Node Functions
// ============================================================================

/**
 * Show add scheduler node modal
 */
async function showAddSchedulerNodeModal() {
  const modal = new bootstrap.Modal(document.getElementById('addSchedulerNodeModal'));
  modal.show();
}

/**
 * Update schedule input fields based on selected schedule type
 */
function updateScheduleInputs() {
  const scheduleType = document.getElementById('scheduler-type').value;

  // Hide all config sections
  document.querySelectorAll('.schedule-type-config').forEach(el => {
    el.style.display = 'none';
  });

  // Show relevant config section
  const configMap = {
    'once': 'once-config',
    'interval': 'interval-config',
    'daily': 'daily-config',
    'weekly': 'weekly-config',
    'cron': 'cron-config'
  };

  const configId = configMap[scheduleType];
  if (configId) {
    document.getElementById(configId).style.display = 'block';
  }
}

/**
 * Toggle end date input visibility
 */
function toggleEndDate() {
  const checkbox = document.getElementById('has-end-date');
  const input = document.getElementById('end-date');
  input.style.display = checkbox.checked ? 'block' : 'none';
}

/**
 * Build schedule config object from form inputs
 */
function buildScheduleConfig() {
  const scheduleType = document.getElementById('scheduler-type').value || 'interval';
  const config = {
    type: scheduleType  // Backend expects 'type', not 'schedule_type'
  };

  switch (scheduleType) {
    case 'once':
      // One-time execution at a specific date/time
      const executeAtStr = document.getElementById('execute-at').value;
      if (!executeAtStr) {
        throw new Error('Please select a date and time for the one-time execution');
      }
      const executeAt = new Date(executeAtStr);
      if (executeAt <= new Date()) {
        throw new Error('Execution time must be in the future');
      }
      config.execute_at = executeAt.toISOString();
      break;

    case 'interval':
      // Convert minutes to nanoseconds (time.Duration is int64 nanoseconds in JSON)
      let intervalMinutes = parseInt(document.getElementById('interval-minutes').value, 10);
      if (isNaN(intervalMinutes) || intervalMinutes <= 0) {
        intervalMinutes = 60; // sensible default
      }
      const runOnce = document.getElementById('interval-run-once')?.checked || false;
      if (runOnce) {
        // Use relative_delay type with trigger_once=true for one-shot execution
        config.type = 'relative_delay';
        config.delay_duration = intervalMinutes * 60 * 1000000000;  // minutes to nanoseconds
        config.trigger_once = true;
      } else {
        config.interval = intervalMinutes * 60 * 1000000000;  // minutes to nanoseconds
      }
      break;

    case 'daily':
      // Backend expects time_of_day as "HH:MM" string
      config.time_of_day = document.getElementById('daily-time').value;
      break;

    case 'weekly':
      // Backend expects time_of_day as "HH:MM" string
      config.time_of_day = document.getElementById('weekly-time').value;
      config.day_of_week = parseInt(document.getElementById('weekly-day').value);
      break;

    case 'cron':
      config.cron_expr = document.getElementById('cron-expr').value.trim();
      if (!config.cron_expr) {
        throw new Error('Cron expression is required');
      }
      break;
  }

  // Add end date if specified
  if (document.getElementById('has-end-date').checked) {
    const endDateStr = document.getElementById('end-date').value;
    if (endDateStr) {
      config.end_date = new Date(endDateStr).toISOString();
    }
  }

  return config;
}

/**
 * Submit scheduler node creation
 */
async function submitSchedulerNode() {
  try {
    const name = document.getElementById('scheduler-name').value.trim();
    if (!name) {
      alert('Please enter a name for the scheduler');
      return;
    }

    const studioId = currentStudioId || (window.agentCanvas && window.agentCanvas.studioId);
    if (!studioId) {
      alert('No workspace loaded');
      return;
    }

    // Build schedule config
    let scheduleConfig;
    try {
      scheduleConfig = buildScheduleConfig();
    } catch (err) {
      alert(err.message || 'Invalid schedule configuration');
      return;
    }

    const enabled = document.getElementById('scheduler-enabled').checked;

    // Create scheduler node (without 'to' - assign agent later using ASSIGN button)
    const schedulerNode = {
      name: name,
      to: '',  // Empty - use ASSIGN button to set target agent
      from: 'scheduler',  // Scheduler nodes always use 'scheduler' as the source
      schedule: scheduleConfig,
      enabled: enabled,
      x: 300,  // Default position
      y: 300
    };

    console.log('Creating scheduler node:', schedulerNode);

    const response = await fetch(`/api/orchestration/workspaces/${studioId}/scheduler-nodes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(schedulerNode)
    });

    if (response.ok) {
      console.log('Scheduler node created successfully');

      // Close modal
      const modal = bootstrap.Modal.getInstance(document.getElementById('addSchedulerNodeModal'));
      modal.hide();

      // Reset form
      document.getElementById('schedulerNodeForm').reset();

      // Reload page to show new scheduler node
      window.location.reload();
    } else {
      const error = await response.text();
      alert(`Failed to create scheduler node: ${error}`);
    }
  } catch (error) {
    console.error('Error creating scheduler node:', error);
    alert(`Error creating scheduler node: ${error.message}`);
  }
}

// ==================== STORE NODE FUNCTIONS ====================

/**
 * Show the Add Store Node modal
 */
async function showAddStoreNodeModal() {
  const modal = new bootstrap.Modal(document.getElementById('addStoreNodeModal'));
  modal.show();
}

/**
 * Submit the Store Node creation form
 */
async function submitStoreNode() {
  try {
    // Get form values
    const name = document.getElementById('store-name').value.trim();
    const baseDir = document.getElementById('store-base-dir').value.trim();
    const format = document.getElementById('store-format').value;
    const writeMode = document.getElementById('store-write-mode').value;
    const autoCreate = document.getElementById('store-auto-create').checked;

    // Validate required fields
    if (!name) {
      alert('Please enter a node name');
      return;
    }

    if (!baseDir) {
      alert('Please enter a base directory');
      return;
    }

    // Get studio ID from global variable (set by template)
    const studioId = window.currentStudioId || (window.agentCanvas && window.agentCanvas.studioId);

    if (!studioId) {
      alert('Error: Workspace ID not found. Please refresh the page.');
      console.error('Cannot create store node: studioId is missing');
      return;
    }

    console.log('Using studioId:', studioId);

    // Calculate position (center of viewport)
    // Note: This will be replaced with proper canvas position calculation
    const x = 400;
    const y = 400;

    // Create store node object
    const storeNode = {
      name: name,
      base_dir: baseDir,
      format: format,
      write_mode: writeMode,
      auto_create_dir: autoCreate,
      x: x,
      y: y
    };

    console.log('Creating store node:', storeNode);

    const response = await fetch(`/api/studios/${studioId}/store-nodes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(storeNode)
    });

    if (response.ok) {
      console.log('Store node created successfully');

      // Close modal
      const modal = bootstrap.Modal.getInstance(document.getElementById('addStoreNodeModal'));
      modal.hide();

      // Reset form
      document.getElementById('storeNodeForm').reset();

      // Reload page to show new store node
      window.location.reload();
    } else {
      const error = await response.text();
      alert(`Failed to create store node: ${error}`);
    }
  } catch (error) {
    console.error('Error creating store node:', error);
    alert(`Error creating store node: ${error.message}`);
  }
}

/**
 * Set a cron preset value
 */
function setCronPreset(cronExpr) {
  document.getElementById('cron-expr').value = cronExpr;
  updateCronDescription();
}

/**
 * Toggle the cron builder panel
 */
function toggleCronBuilder() {
  const builder = document.getElementById('cron-builder');
  const icon = document.getElementById('cron-builder-icon');

  if (builder.style.display === 'none') {
    builder.style.display = 'block';
    // Change icon to collapse (up arrow)
    icon.innerHTML = '<path d="M7.41,15.41L12,10.83L16.59,15.41L18,14L12,8L6,14L7.41,15.41Z"/>';

    // Populate fields from current expression
    const cronExpr = document.getElementById('cron-expr').value.trim();
    if (cronExpr) {
      const parts = cronExpr.split(/\s+/);
      if (parts.length === 5) {
        document.getElementById('cron-minute').value = parts[0];
        document.getElementById('cron-hour').value = parts[1];
        document.getElementById('cron-day').value = parts[2];
        document.getElementById('cron-month').value = parts[3];
        document.getElementById('cron-weekday').value = parts[4];
      }
    }
  } else {
    builder.style.display = 'none';
    // Change icon to expand (down arrow)
    icon.innerHTML = '<path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>';
  }
}

/**
 * Build cron expression from individual fields
 */
function buildCronFromFields() {
  const minute = document.getElementById('cron-minute').value.trim() || '*';
  const hour = document.getElementById('cron-hour').value.trim() || '*';
  const day = document.getElementById('cron-day').value.trim() || '*';
  const month = document.getElementById('cron-month').value.trim() || '*';
  const weekday = document.getElementById('cron-weekday').value.trim() || '*';

  const cronExpr = `${minute} ${hour} ${day} ${month} ${weekday}`;
  document.getElementById('cron-expr').value = cronExpr;
  updateCronDescription();
}

/**
 * Update the human-readable cron description
 */
function updateCronDescription() {
  const cronExpr = document.getElementById('cron-expr').value.trim();
  const descriptionDiv = document.getElementById('cron-description');
  const descriptionText = document.getElementById('cron-description-text');

  if (!cronExpr) {
    descriptionDiv.style.display = 'none';
    return;
  }

  const description = parseCronExpression(cronExpr);
  if (description) {
    descriptionText.textContent = description;
    descriptionDiv.style.display = 'block';
  } else {
    descriptionDiv.style.display = 'none';
  }
}

/**
 * Parse a cron expression and return a human-readable description
 */
function parseCronExpression(cronExpr) {
  const parts = cronExpr.split(/\s+/);
  if (parts.length !== 5) {
    return 'Invalid cron expression (must have 5 fields)';
  }

  const [minute, hour, day, month, weekday] = parts;

  // Build description
  let desc = 'Runs ';

  // Check for every minute
  if (minute === '*' && hour === '*' && day === '*' && month === '*' && weekday === '*') {
    return 'Runs every minute';
  }

  // Check for specific intervals
  if (minute.startsWith('*/')) {
    const interval = minute.substring(2);
    desc += `every ${interval} minutes`;
  } else if (minute === '0' && hour.startsWith('*/')) {
    const interval = hour.substring(2);
    desc += `every ${interval} hours`;
  } else if (minute === '0' && hour === '0' && day.startsWith('*/')) {
    const interval = day.substring(2);
    desc += `every ${interval} days`;
  } else if (minute !== '*' && hour !== '*') {
    // Specific time
    const h = hour === '*' ? 'every hour' : `at ${hour.padStart(2, '0')}:${minute.padStart(2, '0')}`;
    desc += h;
  } else if (minute !== '*') {
    desc += `at minute ${minute}`;
  } else if (hour !== '*') {
    desc += `at hour ${hour}`;
  } else {
    desc += 'at a specific time';
  }

  // Add day/month/weekday constraints
  const constraints = [];

  if (weekday !== '*') {
    const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
    const dayNames = weekday.split(',').map(d => days[parseInt(d)] || `day ${d}`);
    constraints.push(`on ${dayNames.join(', ')}`);
  }

  if (day !== '*' && weekday === '*') {
    if (day.includes(',')) {
      constraints.push(`on days ${day}`);
    } else {
      constraints.push(`on day ${day} of the month`);
    }
  }

  if (month !== '*') {
    const months = ['', 'January', 'February', 'March', 'April', 'May', 'June',
                    'July', 'August', 'September', 'October', 'November', 'December'];
    const monthNames = month.split(',').map(m => months[parseInt(m)] || `month ${m}`);
    constraints.push(`in ${monthNames.join(', ')}`);
  }

  if (constraints.length > 0) {
    desc += ' ' + constraints.join(' ');
  }

  return desc;
}

// =============================================================================
// Store Node Functions
// =============================================================================

/**
 * Show the add store node modal
 */
async function showAddStoreNodeModal() {
  const modal = new bootstrap.Modal(document.getElementById('addStoreNodeModal'));

  // Reset form
  document.getElementById('storeNodeForm').reset();
  document.getElementById('store-auto-create-dir').checked = true;

  modal.show();
}

/**
 * Submit the store node form
 */
async function submitStoreNode() {
  const name = document.getElementById('store-name').value.trim();
  const baseDir = document.getElementById('store-base-dir').value.trim();
  const format = document.getElementById('store-format').value;
  const writeMode = document.getElementById('store-write-mode').value;
  const autoCreateDir = document.getElementById('store-auto-create-dir').checked;

  if (!name || !baseDir) {
    alert('Please fill in all required fields');
    return;
  }

  const studioId = window.currentStudioId || (window.agentCanvas && window.agentCanvas.studioId);

  if (!studioId) {
    alert('Error: Workspace ID not found. Please refresh the page.');
    console.error('Cannot create store node: studioId is missing');
    return;
  }

  console.log('Using studioId:', studioId);

  try {
    // Calculate center of visible viewport
    let x = 400, y = 400;
    if (window.agentCanvas) {
      const canvas = window.agentCanvas;
      const scale = canvas.state?.scale || canvas.scale || 1;
      const offsetX = canvas.state?.offsetX || canvas.offsetX || 0;
      const offsetY = canvas.state?.offsetY || canvas.offsetY || 0;
      const width = canvas.width || 800;
      const height = canvas.height || 600;
      x = (width / 2 - offsetX) / scale + (Math.random() - 0.5) * 100;
      y = (height / 2 - offsetY) / scale + (Math.random() - 0.5) * 100;
    }

    const response = await fetch(`/api/studios/${studioId}/canvas/store-nodes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: name,
        base_dir: baseDir,
        format: format,
        write_mode: writeMode,
        auto_create_dir: autoCreateDir,
        x: x,
        y: y
      })
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(`Failed to create store node: ${errorText}`);
    }

    const result = await response.json();
    console.log('Store node created:', result);

    // Close modal
    const modal = bootstrap.Modal.getInstance(document.getElementById('addStoreNodeModal'));
    modal.hide();

    // Refresh canvas to show the new store node
    if (window.agentCanvas && window.agentCanvas.init) {
      await window.agentCanvas.init();
    }

    // Show success message
    console.log('Store node created successfully!');
  } catch (error) {
    console.error('Error creating store node:', error);
    alert(`Error creating store node: ${error.message}`);
  }
}

// Export functions to window
window.showAddSchedulerNodeModal = showAddSchedulerNodeModal;
window.updateScheduleInputs = updateScheduleInputs;
window.toggleEndDate = toggleEndDate;
window.submitSchedulerNode = submitSchedulerNode;
window.setCronPreset = setCronPreset;
window.toggleCronBuilder = toggleCronBuilder;
window.buildCronFromFields = buildCronFromFields;
window.updateCronDescription = updateCronDescription;
window.showAddStoreNodeModal = showAddStoreNodeModal;
window.submitStoreNode = submitStoreNode;

// ========== SAVE WORKFLOW MODAL ==========

// State for workflow saving
let pendingWorkflowData = null;

/**
 * Maximum number of nodes allowed in a workflow
 */
const MAX_WORKFLOW_NODES = 20;

/**
 * Show the save workflow modal with selected nodes data
 * @param {Object} selectionData - Data about selected nodes from the canvas
 */
function showSaveWorkflowModal(selectionData) {
  // Validate selection
  if (!selectionData || !selectionData.nodes || selectionData.nodes.length === 0) {
    alert('No nodes selected. Please select at least one node to create a workflow.');
    return;
  }

  if (selectionData.nodes.length > MAX_WORKFLOW_NODES) {
    alert(`Too many nodes selected. Maximum ${MAX_WORKFLOW_NODES} nodes allowed per workflow. You have ${selectionData.nodes.length} selected.`);
    return;
  }

  // Store the selection data for later
  pendingWorkflowData = selectionData;

  // Reset form
  const form = document.getElementById('save-workflow-form');
  if (form) {
    form.reset();
  }

  // Hide previous errors
  const errorDiv = document.getElementById('workflow-save-error');
  if (errorDiv) {
    errorDiv.style.display = 'none';
  }

  // Remove any previous validation styling
  const nameInput = document.getElementById('workflow-name');
  if (nameInput) {
    nameInput.classList.remove('is-invalid');
  }

  // Update selection info
  const selectionInfo = document.getElementById('workflow-selection-info');
  const nodeCountSpan = document.getElementById('workflow-node-count');
  const agentInfoSpan = document.getElementById('workflow-agent-info');

  if (selectionInfo && nodeCountSpan) {
    selectionInfo.style.display = 'block';
    nodeCountSpan.textContent = selectionData.nodes.length;

    // Count node types
    const typeCounts = {};
    selectionData.nodes.forEach(node => {
      typeCounts[node.type] = (typeCounts[node.type] || 0) + 1;
    });

    const typeInfo = Object.entries(typeCounts)
      .map(([type, count]) => `${count} ${type}${count > 1 ? 's' : ''}`)
      .join(', ');

    agentInfoSpan.textContent = `(${typeInfo})`;
  }

  // Show modal
  const modal = new bootstrap.Modal(document.getElementById('saveWorkflowModal'));
  modal.show();

  // Focus on name input after modal is shown
  document.getElementById('saveWorkflowModal').addEventListener('shown.bs.modal', function() {
    document.getElementById('workflow-name').focus();
  }, { once: true });
}

/**
 * Collect selected nodes data from the canvas for workflow creation
 * @returns {Object} Selection data including nodes, connections, and layout
 */
function collectWorkflowSelectionData() {
  if (!window.agentCanvas || !window.agentCanvas.state) {
    console.error('Canvas not available');
    return null;
  }

  const state = window.agentCanvas.state;
  const selectedNodes = state.getSelectedNodes();

  if (!selectedNodes || selectedNodes.length === 0) {
    return null;
  }

  // Convert selected nodes to workflow format
  const nodes = [];
  const selectedIds = new Set(selectedNodes.map(n => n.id));

  // Calculate center of selection for relative positioning
  let sumX = 0, sumY = 0;
  selectedNodes.forEach(sel => {
    const node = sel.node;
    sumX += node.x || 0;
    sumY += node.y || 0;
  });
  const centerX = sumX / selectedNodes.length;
  const centerY = sumY / selectedNodes.length;

  // Process each selected node
  selectedNodes.forEach(sel => {
    const node = sel.node;
    const workflowNode = {
      id: sel.id,
      type: sel.type,
      config: extractNodeConfig(node, sel.type),
      relative_x: (node.x || 0) - centerX,
      relative_y: (node.y || 0) - centerY
    };
    nodes.push(workflowNode);
  });

  // Find connections between selected nodes
  const internalConnections = [];
  // TODO: Collect actual connections when connection data is available
  // For now, connections would need to be inferred from task assignments

  // Calculate layout dimensions
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
  nodes.forEach(n => {
    minX = Math.min(minX, n.relative_x);
    maxX = Math.max(maxX, n.relative_x);
    minY = Math.min(minY, n.relative_y);
    maxY = Math.max(maxY, n.relative_y);
  });

  const layout = {
    width: maxX - minX + 200, // Add padding
    height: maxY - minY + 200,
    node_positions: {}
  };

  nodes.forEach(n => {
    layout.node_positions[n.id] = { x: n.relative_x, y: n.relative_y };
  });

  return {
    nodes,
    internal_connections: internalConnections,
    input_ports: [],
    output_ports: [],
    layout
  };
}

/**
 * Extract configuration from a node based on its type
 * @param {Object} node - The node object
 * @param {string} type - The node type
 * @returns {Object} Configuration object
 */
function extractNodeConfig(node, type) {
  switch (type) {
    case 'task':
      return {
        description: node.description || '',
        to: node.to || 'unassigned',
        from: node.from || 'user',
        priority: node.priority || 0,
        status: node.status || 'pending'
      };
    case 'agent':
      return {
        name: node.name || node.nodeId || '',
        type: node.type || 'tool-calling',
        model: node.model || ''
      };
    case 'scheduler':
      return {
        name: node.name || '',
        schedule_type: node.schedule_type || 'cron',
        cron_expression: node.cron_expression || '',
        enabled: node.enabled !== false
      };
    case 'store':
      return {
        name: node.name || '',
        store_type: node.store_type || 'file',
        file_path: node.file_path || ''
      };
    case 'attachment':
      return {
        title: node.title || '',
        type: node.type || 'note',
        body: node.body || '',
        link_url: node.link_url || ''
      };
    default:
      return { ...node };
  }
}

/**
 * Submit the save workflow form
 */
async function submitSaveWorkflow() {
  const nameInput = document.getElementById('workflow-name');
  const descriptionInput = document.getElementById('workflow-description');
  const categorySelect = document.getElementById('workflow-category');
  const errorDiv = document.getElementById('workflow-save-error');
  const saveBtn = document.getElementById('save-workflow-btn');

  // Clear previous errors
  errorDiv.style.display = 'none';
  nameInput.classList.remove('is-invalid');

  // Validate name
  const name = nameInput.value.trim();
  if (!name) {
    nameInput.classList.add('is-invalid');
    nameInput.focus();
    return;
  }

  // Ensure we have workflow data
  if (!pendingWorkflowData || !pendingWorkflowData.nodes || pendingWorkflowData.nodes.length === 0) {
    showWorkflowError('No nodes data available. Please try again.');
    return;
  }

  // Disable button and show loading state
  saveBtn.disabled = true;
  saveBtn.innerHTML = `
    <span class="spinner-border spinner-border-sm me-1" role="status" aria-hidden="true"></span>
    Saving...
  `;

  try {
    const requestBody = {
      name: name,
      description: descriptionInput.value.trim(),
      category: categorySelect.value,
      nodes: pendingWorkflowData.nodes,
      internal_connections: pendingWorkflowData.internal_connections || [],
      input_ports: pendingWorkflowData.input_ports || [],
      output_ports: pendingWorkflowData.output_ports || [],
      layout: pendingWorkflowData.layout || { width: 800, height: 600, node_positions: {} }
    };

    console.log('Saving workflow:', requestBody);

    const response = await fetch('/api/workflows', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(requestBody)
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save workflow');
    }

    const result = await response.json();
    console.log('Workflow saved:', result);

    // Close modal
    const modal = bootstrap.Modal.getInstance(document.getElementById('saveWorkflowModal'));
    modal.hide();

    // Clear pending data
    pendingWorkflowData = null;

    // Clear selection on canvas
    if (window.agentCanvas && window.agentCanvas.state) {
      window.agentCanvas.state.clearSelection();
      window.agentCanvas.draw();
    }

    // Show success notification
    if (window.agentCanvas && window.agentCanvas.showNotification) {
      window.agentCanvas.showNotification(`Workflow "${name}" saved successfully!`, 'success');
    } else {
      alert(`Workflow "${name}" saved successfully!`);
    }

  } catch (error) {
    console.error('Error saving workflow:', error);
    showWorkflowError(error.message);
  } finally {
    // Re-enable button
    saveBtn.disabled = false;
    saveBtn.innerHTML = `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3M19,19H5V5H16.17L19,7.83V19M12,12C10.34,12 9,13.34 9,15C9,16.66 10.34,18 12,18C13.66,18 15,16.66 15,15C15,13.34 13.66,12 12,12M6,6H15V10H6V6Z"/>
      </svg>
      Save Workflow
    `;
  }
}

/**
 * Show error message in the workflow modal
 * @param {string} message - Error message to display
 */
function showWorkflowError(message) {
  const errorDiv = document.getElementById('workflow-save-error');
  if (errorDiv) {
    errorDiv.textContent = message;
    errorDiv.style.display = 'block';
  }
}

// Export workflow functions to window
window.showSaveWorkflowModal = showSaveWorkflowModal;
window.submitSaveWorkflow = submitSaveWorkflow;
window.collectWorkflowSelectionData = collectWorkflowSelectionData;
