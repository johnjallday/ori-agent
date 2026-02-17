/**
 * Workspace Hub Tasks Management
 * Handles task CRUD operations, rendering, and workflows.
 *
 * @module workspace-hub-tasks
 */
(function() {
  'use strict';

  const { formatDate, buildTaskHierarchy, getDisplayStatus, getDisplayResult, computeTaskStats } = window.WorkspaceHubUtils;

  /**
   * Get subtasks for a parent task ID
   * @param {string} taskId - Parent task ID
   * @returns {Array} Array of subtasks
   */
  function getSubtasksForParent(taskId) {
    if (!taskId) return [];
    const state = window.WorkspaceHubState.getState();
    const hierarchy = state.taskHierarchy || buildTaskHierarchy(state.tasks || []);
    state.taskHierarchy = hierarchy;
    return hierarchy.subtasksByParent.get(taskId) || [];
  }

  /**
   * Delete a task
   * @param {string} taskId - Task ID to delete
   */
  async function deleteTask(taskId) {
    const state = window.WorkspaceHubState.getState();

    try {
      const subtasks = getSubtasksForParent(taskId);
      const isParent = subtasks.length > 0;

      if (isParent) {
        const choice = await window.WorkspaceHubModals.showParentDeletePrompt({
          title: 'Delete Workflow',
          message: `This workflow has ${subtasks.length} subtask${subtasks.length === 1 ? '' : 's'}. What would you like to do?`
        });

        if (!choice) return;

        if (choice === 'delete_all') {
          for (const subtask of subtasks) {
            const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(subtask.id)}`, {
              method: 'DELETE'
            });
            if (!response.ok) {
              throw new Error('Failed to delete a subtask');
            }
          }
        } else if (choice === 'ungroup') {
          for (const subtask of subtasks) {
            const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(subtask.id)}`, {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                parent_task_id: '',
                subtask_index: 0
              })
            });
            if (!response.ok) {
              throw new Error('Failed to ungroup a subtask');
            }
          }
        }
      } else {
        const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
          title: 'Delete Task',
          message: 'Are you sure you want to delete this task? This action cannot be undone.'
        });
        if (!confirmed) return;
      }

      const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete task');

      if (window.Toast) window.Toast.success('Task deleted');
      await loadTasks(state.selectedId);
    } catch (error) {
      console.error('Failed to delete task:', error);
      if (window.Toast) window.Toast.error('Failed to delete task');
    }
  }

  /**
   * Execute a task
   * @param {string} taskId - Task ID to execute
   */
  async function executeTask(taskId) {
    const state = window.WorkspaceHubState.getState();
    const task = state.tasks.find((item) => item.id === taskId);
    if (!task) return;

    const subtasks = getSubtasksForParent(taskId);
    const isParent = subtasks.length > 0;

    if (isParent) {
      const hasUnassigned = subtasks.some((subtask) => !subtask.to || subtask.to === 'unassigned');
      if (hasUnassigned) {
        if (window.Toast) {
          window.Toast.error('Assign agents to all subtasks before executing this workflow.');
        } else {
          alert('Assign agents to all subtasks before executing this workflow.');
        }
        return;
      }

      const hasRunning = subtasks.some((subtask) => subtask.status === 'in_progress');
      if (hasRunning) {
        if (window.Toast) {
          window.Toast.error('A subtask is already running.');
        } else {
          alert('A subtask is already running.');
        }
        return;
      }
    } else {
      const assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
      if (!assignedAgent) {
        if (window.Toast) {
          window.Toast.error('Assign an agent before executing this task.');
        } else {
          alert('Assign an agent before executing this task.');
        }
        return;
      }
    }

    const confirmMessage = isParent
      ? `Execute this workflow (${subtasks.length} step${subtasks.length === 1 ? '' : 's'}) now?`
      : 'Execute this task now?';
    const confirmed = confirm(confirmMessage);
    if (!confirmed) return;

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: taskId })
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to execute task');
      }

      if (window.Toast) window.Toast.success('Task started');
      await loadTasks(state.selectedId);
      pollTaskCompletion(taskId);
    } catch (error) {
      console.error('Failed to execute task:', error);
      if (window.Toast) {
        window.Toast.error('Failed to execute task');
      } else {
        alert('Failed to execute task');
      }
    }
  }

  /**
   * Poll for task completion
   * @param {string} taskId - Task ID to poll
   * @param {number} maxAttempts - Maximum poll attempts
   * @param {number} intervalMs - Poll interval in ms
   */
  async function pollTaskCompletion(taskId, maxAttempts = 36, intervalMs = 5000) {
    const state = window.WorkspaceHubState.getState();
    let attempts = 0;

    const poll = async () => {
      attempts++;
      if (attempts > maxAttempts) return;

      try {
        const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`);
        if (!response.ok) {
          setTimeout(poll, intervalMs);
          return;
        }

        const task = await response.json();
        const status = task.status;
        if (status === 'completed' || status === 'failed' || status === 'cancelled' || status === 'timeout') {
          await loadTasks(state.selectedId);
          return;
        }
      } catch (error) {
        console.error('Failed to poll task status:', error);
      }

      setTimeout(poll, intervalMs);
    };

    setTimeout(poll, intervalMs);
  }

  /**
   * Export workflow task to JSON file
   * @param {Object} task - Task object
   * @param {string} taskId - Task ID
   */
  async function exportWorkflowTask(task, taskId) {
    const subtasks = getSubtasksForParent(taskId);

    const workflowExport = {
      name: task.name || task.description || 'Exported Workflow',
      description: task.description || '',
      category: 'imported',
      source: 'workspace',
      exported_at: new Date().toISOString(),
      steps: subtasks.map((subtask, index) => ({
        step_number: subtask.subtask_index || index + 1,
        name: subtask.name || subtask.description || `Step ${index + 1}`,
        description: subtask.description || '',
        assigned_to: subtask.to || 'unassigned',
        dependencies: subtask.depends_on || []
      }))
    };

    const blob = new Blob([JSON.stringify(workflowExport, null, 2)], { type: 'application/json' });
    const url = window.URL.createObjectURL(blob);
    const a = document.createElement('a');
    const safeName = (task.name || 'workflow').replace(/[^a-zA-Z0-9]/g, '-').toLowerCase();
    a.href = url;
    a.download = `workflow-${safeName}.json`;
    document.body.appendChild(a);
    a.click();
    window.URL.revokeObjectURL(url);
    document.body.removeChild(a);

    if (window.Toast) {
      window.Toast.success('Workflow exported');
    }
  }

  /**
   * Bulk delete tasks
   */
  async function bulkDeleteTasks() {
    const state = window.WorkspaceHubState.getState();
    const ids = Array.from(state.selectedItems.tasks);
    if (ids.length === 0) return;

    const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
      title: `Delete ${ids.length} Task${ids.length > 1 ? 's' : ''}`,
      message: `Are you sure you want to delete ${ids.length} task${ids.length > 1 ? 's' : ''}? This action cannot be undone.`
    });
    if (!confirmed) return;

    try {
      const response = await fetch('/api/orchestration/tasks/bulk', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_ids: ids, workspace_id: state.selectedId })
      });
      if (!response.ok) throw new Error('Failed to delete tasks');

      const result = await response.json();
      if (window.Toast) {
        window.Toast.success(`Deleted ${result.success_count} task${result.success_count !== 1 ? 's' : ''}`);
      }

      window.WorkspaceHubSelection.toggleSelectionMode('tasks', () => renderTasksList(state.tasks));
      await loadTasks(state.selectedId);
    } catch (error) {
      console.error('Failed to bulk delete tasks:', error);
      if (window.Toast) window.Toast.error('Failed to delete tasks');
    }
  }

  /**
   * Load tasks for a workspace
   * @param {string} workspaceId - Workspace ID
   */
  async function loadTasks(workspaceId) {
    if (!workspaceId) return;

    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    // Cancel any pending request
    let controller = window.WorkspaceHubState.getTasksAbortController();
    if (controller) {
      controller.abort();
    }
    controller = new AbortController();
    window.WorkspaceHubState.setTasksAbortController(controller);

    if (elements.tasksList) {
      elements.tasksList.innerHTML = '<div class="hub-loading">Loading tasks...</div>';
    }

    try {
      const response = await fetch(`/api/orchestration/tasks?studio_id=${encodeURIComponent(workspaceId)}`, {
        signal: controller.signal
      });
      if (!response.ok) throw new Error('Failed to load tasks');

      const data = await response.json();

      // Only update if this workspace is still selected
      if (state.selectedId !== workspaceId) return;

      state.tasks = data.tasks || [];
      const computed = computeTaskStats(state.tasks);
      state.stats = { ...computed, ...(data.stats || {}) };
      if (state.stats.scheduled === undefined) state.stats.scheduled = computed.scheduled;

      renderStats(state.stats);
      renderTasksList(state.tasks);
      renderSchedules(state.tasks);
    } catch (error) {
      if (error.name === 'AbortError') return;

      console.error('Workspace hub failed to load tasks:', error);
      if (state.selectedId !== workspaceId) return;

      if (elements.tasksList) {
        elements.tasksList.innerHTML = '<div class="hub-empty">Unable to load tasks right now.</div>';
      }
      if (elements.schedulesList) {
        elements.schedulesList.innerHTML = '<div class="hub-empty">Unable to load schedules right now.</div>';
      }
    }
  }

  /**
   * Render task stats
   * @param {Object} stats - Stats object
   */
  function renderStats(stats) {
    const elements = window.WorkspaceHubState.getElements();
    if (!stats) return;
    if (elements.statCompleted) elements.statCompleted.textContent = stats.completed || 0;
    if (elements.statInProgress) elements.statInProgress.textContent = stats.in_progress || 0;
    if (elements.statFailed) elements.statFailed.textContent = stats.failed || 0;
    if (elements.statScheduled) elements.statScheduled.textContent = stats.scheduled || 0;
  }

  /**
   * Render schedules list
   * @param {Array} tasks - Tasks array
   */
  function renderSchedules(tasks) {
    const elements = window.WorkspaceHubState.getElements();
    if (!elements.schedulesList) return;

    const scheduled = (tasks || []).filter((task) => task.schedule_enabled);

    if (scheduled.length === 0) {
      elements.schedulesList.innerHTML = '<div class="hub-empty">No scheduled tasks yet.</div>';
      return;
    }

    const sorted = scheduled.sort((a, b) => {
      const aTime = a.next_run ? new Date(a.next_run).getTime() : Number.MAX_SAFE_INTEGER;
      const bTime = b.next_run ? new Date(b.next_run).getTime() : Number.MAX_SAFE_INTEGER;
      return aTime - bTime;
    });

    const items = sorted.slice(0, 5).map((task) => {
      const nextRun = task.next_run ? formatDate(task.next_run) : 'Not scheduled';
      return `
        <div class="hub-schedule-item">
          <div>
            <div class="hub-schedule-title">${escapeHtml(task.name || task.description || task.id)}</div>
            <div class="hub-schedule-subtitle">${escapeHtml(nextRun)}</div>
          </div>
          <span class="hub-schedule-status">${escapeHtml(task.status || 'pending')}</span>
        </div>
      `;
    });

    elements.schedulesList.innerHTML = items.join('');
  }

  /**
   * Render tasks list
   * @param {Array} tasks - Tasks array
   */
  function renderTasksList(tasks) {
    const elements = window.WorkspaceHubState.getElements();
    const state = window.WorkspaceHubState.getState();

    if (!elements.tasksList) return;

    if (!tasks || tasks.length === 0) {
      elements.tasksList.innerHTML = '<div class="hub-empty">No tasks yet. Create the first one to get started.</div>';
      if (elements.tasksSubtitle) {
        elements.tasksSubtitle.textContent = 'No tasks created yet.';
      }
      return;
    }

    state.taskHierarchy = buildTaskHierarchy(tasks);
    const { rootTasks, subtasksByParent, taskById } = state.taskHierarchy;
    const parentCount = rootTasks.filter((task) => subtasksByParent.has(task.id)).length;
    const subtaskCount = Array.from(subtasksByParent.values()).reduce((total, list) => total + list.length, 0);
    const standaloneCount = rootTasks.length - parentCount;

    if (elements.tasksSubtitle) {
      const parts = [];
      if (parentCount) parts.push(`${parentCount} workflow${parentCount === 1 ? '' : 's'}`);
      if (standaloneCount) parts.push(`${standaloneCount} task${standaloneCount === 1 ? '' : 's'}`);
      if (subtaskCount) parts.push(`${subtaskCount} subtask${subtaskCount === 1 ? '' : 's'}`);
      elements.tasksSubtitle.textContent = parts.length > 0
        ? `${parts.join(' | ')} in this workspace.`
        : `${tasks.length} task${tasks.length === 1 ? '' : 's'} queued for this workspace.`;
    }

    const _inSelectionMode = window.WorkspaceHubSelection.isSelectionModeEnabled('tasks');
    const selectedSet = state.selectedItems.tasks;

    const renderTaskCard = (task, { isParent = false, isSubtask = false, subtasks = [], stepNumber = null, parentId = '' } = {}) => {
      const status = isParent ? getDisplayStatus(task, subtasks) : (task.status || 'pending');
      const statusLabel = status.replace('_', ' ');
      const scheduleLabel = isParent
        ? 'Workflow container'
        : task.schedule_enabled ? `Next run: ${formatDate(task.next_run)}` : 'Not scheduled';
      const assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
      const assignment = isParent ? `${subtasks.length} step${subtasks.length === 1 ? '' : 's'}` : (assignedAgent || 'unassigned');
      const inputCount = Array.isArray(task.input_task_ids) ? task.input_task_ids.length : 0;
      const inputBadge = inputCount > 0
        ? `<span class="hub-task-inputs" title="Uses results from ${inputCount} task${inputCount === 1 ? '' : 's'}">Inputs: ${inputCount}</span>`
        : '';
      const isSelected = selectedSet.has(task.id);
      const hasUnassignedSubtasks = isParent && subtasks.some((s) => !s.to || s.to === 'unassigned');
      const hasRunningSubtasks = isParent && subtasks.some((s) => s.status === 'in_progress');
      const canExecute = isParent
        ? subtasks.length > 0 && !hasUnassignedSubtasks && !hasRunningSubtasks
        : Boolean(assignedAgent) && status !== 'in_progress';
      const executeLabel = status === 'completed' || status === 'failed' ? 'Re-run' : (isParent ? 'Run All' : 'Execute');
      const executeTitle = isParent
        ? hasUnassignedSubtasks ? 'Assign agents to all subtasks before executing'
          : hasRunningSubtasks ? 'A subtask is already running'
            : executeLabel === 'Re-run' ? 'Re-run workflow' : 'Execute workflow now'
        : !assignedAgent ? 'Assign an agent before executing'
          : status === 'in_progress' ? 'Task is already running'
            : executeLabel === 'Re-run' ? 'Re-execute task' : 'Execute task now';
      const resultData = getDisplayResult(task, isParent ? subtasks : null);
      const stepBadge = isSubtask
        ? `<span class="hub-task-step">Step ${escapeHtml(stepNumber || '')}</span>`
        : isParent ? '<span class="hub-task-badge">Workflow</span>' : '';
      const toggleButton = isParent
        ? `<button class="hub-task-toggle" data-action="toggle-subtasks" aria-label="Toggle subtasks">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path d="M7,10L12,15L17,10H7Z"/>
            </svg>
          </button>`
        : '';

      return `
        <div class="hub-task-card${isSelected ? ' selected' : ''}${isParent ? ' hub-task-parent' : ''}${isSubtask ? ' hub-task-subtask' : ''}" data-task-id="${escapeHtml(task.id)}"${parentId ? ` data-parent-id="${escapeHtml(parentId)}"` : ''}>
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select task">
          </div>
          <div class="hub-task-content">
            <div class="hub-task-header">
              <div class="hub-task-title">
                ${toggleButton}
                ${stepBadge}
                <span>${escapeHtml(task.name || task.description || task.id)}</span>
                ${inputBadge}
              </div>
              <span class="hub-task-status status-${escapeHtml(status)}">${escapeHtml(statusLabel)}</span>
            </div>
            <div class="hub-task-meta">
              <span>${escapeHtml(assignment)}</span>
              <span>${escapeHtml(scheduleLabel)}</span>
            </div>
            <div class="hub-task-actions">
              <button class="modern-btn modern-btn-secondary" data-action="edit">Edit</button>
              ${isParent ? '<button class="modern-btn modern-btn-secondary" data-action="view-canvas" title="View workflow in canvas">Canvas</button>' : ''}
              ${isParent ? '<button class="modern-btn modern-btn-secondary" data-action="save-workflow" title="Save to Workflows library">Save</button>' : ''}
              ${isParent ? '<button class="modern-btn modern-btn-secondary" data-action="export" title="Export workflow to file">Export</button>' : ''}
              <button class="modern-btn modern-btn-primary" data-action="execute" ${canExecute ? '' : 'disabled'} title="${escapeHtml(executeTitle)}">${escapeHtml(executeLabel)}</button>
            </div>
            ${resultData ? `
            <details class="hub-task-result">
              <summary>${escapeHtml(resultData.label)}</summary>
              <pre>${escapeHtml(resultData.text)}</pre>
            </details>
            ` : ''}
          </div>
          <button class="hub-item-delete-btn" data-action="delete" title="Delete task">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    };

    const items = rootTasks.map((task) => {
      const subtasks = subtasksByParent.get(task.id) || [];
      const isParent = subtasks.length > 0;
      const parentCard = renderTaskCard(task, { isParent, subtasks });

      if (!isParent) return parentCard;

      const subtaskCards = subtasks.map((subtask, index) => {
        const stepNumber = subtask.subtask_index || index + 1;
        return renderTaskCard(subtask, { isSubtask: true, stepNumber, parentId: task.id });
      });

      return `
        <div class="hub-task-group" data-parent-id="${escapeHtml(task.id)}">
          ${parentCard}
          <div class="hub-subtask-list">
            ${subtaskCards.join('')}
          </div>
        </div>
      `;
    });

    elements.tasksList.innerHTML = items.join('');
    bindTaskCardEvents(taskById);
  }

  /**
   * Bind event handlers to task cards
   * @param {Map} taskById - Map of task ID to task object
   */
  function bindTaskCardEvents(taskById) {
    const elements = window.WorkspaceHubState.getElements();
    const state = window.WorkspaceHubState.getState();
    const inSelectionMode = window.WorkspaceHubSelection.isSelectionModeEnabled('tasks');

    elements.tasksList.querySelectorAll('.hub-task-card').forEach((card) => {
      const taskId = card.dataset.taskId;
      const task = taskById.get(taskId);

      // Handle checkbox click in selection mode
      const checkbox = card.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.addEventListener('change', (event) => {
          event.stopPropagation();
          window.WorkspaceHubSelection.toggleItemSelection('tasks', taskId);
        });
      }

      // Handle card click in selection mode
      card.addEventListener('click', (event) => {
        if (inSelectionMode && !event.target.closest('button') && !event.target.closest('input')) {
          window.WorkspaceHubSelection.toggleItemSelection('tasks', taskId);
        }
      });

      // Delete button
      card.querySelectorAll('[data-action="delete"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          deleteTask(taskId);
        });
      });

      card.querySelectorAll('[data-action="toggle-subtasks"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          const group = card.closest('.hub-task-group');
          if (group) group.classList.toggle('is-collapsed');
        });
      });

      card.querySelectorAll('[data-action="edit"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (window.taskModalController && task) {
            window.taskModalController.openForEdit(task, () => loadTasks(state.selectedId));
          }
        });
      });

      card.querySelectorAll('[data-action="execute"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (task && !btn.disabled) executeTask(taskId);
        });
      });

      card.querySelectorAll('[data-action="export"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (task) exportWorkflowTask(task, taskId);
        });
      });

      card.querySelectorAll('[data-action="save-workflow"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (task) saveTaskAsWorkflow(task, taskId);
        });
      });

      card.querySelectorAll('[data-action="view-canvas"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (task && state.selectedId) {
            window.location.href = `/workspaces/${state.selectedId}/canvas?workflow=${taskId}`;
          }
        });
      });
    });
  }

  /**
   * Save task as workflow to library
   * @param {Object} task - Task object
   * @param {string} taskId - Task ID
   */
  function saveTaskAsWorkflow(task, taskId) {
    const subtasks = getSubtasksForParent(taskId);

    if (subtasks.length === 0) {
      if (window.Toast) window.Toast.error('Workflow must have at least one step');
      return;
    }

    openSaveWorkflowModal(task, taskId, subtasks);
  }

  /**
   * Open save workflow modal
   * @param {Object} task - Task object
   * @param {string} taskId - Task ID
   * @param {Array} subtasks - Subtasks array
   */
  function openSaveWorkflowModal(task, taskId, subtasks) {
    let modal = document.getElementById('hubSaveWorkflowModal');
    if (!modal) {
      modal = document.createElement('div');
      modal.id = 'hubSaveWorkflowModal';
      modal.className = 'modal fade';
      modal.tabIndex = -1;
      modal.innerHTML = `
        <div class="modal-dialog">
          <div class="modal-content">
            <div class="modal-header">
              <h5 class="modal-title" style="color: var(--text-primary);">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3M19,19H5V5H16.17L19,7.83V19M12,12C10.34,12 9,13.34 9,15C9,16.66 10.34,18 12,18C13.66,18 15,16.66 15,15C15,13.34 13.66,12 12,12M6,6H15V10H6V6Z"/>
                </svg>
                Save to Workflow Library
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
            </div>
            <div class="modal-body">
              <div class="mb-3">
                <label class="form-label" style="color: var(--text-primary);">Workflow Name</label>
                <input type="text" id="hubSaveWorkflowName" class="form-control" placeholder="Enter workflow name">
              </div>
              <div class="mb-3">
                <label class="form-label" style="color: var(--text-primary);">Description</label>
                <textarea id="hubSaveWorkflowDesc" class="form-control" rows="3" placeholder="Describe what this workflow does..."></textarea>
              </div>
              <div class="mb-3">
                <label class="form-label" style="color: var(--text-primary);">Category</label>
                <input type="text" id="hubSaveWorkflowCategory" class="form-control" placeholder="e.g., automation, data, reports">
              </div>
              <div class="modern-card p-3" style="background: var(--bg-secondary);">
                <div class="d-flex align-items-center gap-2 mb-2">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" style="color: var(--text-secondary);">
                    <path d="M3,4H7V8H3V4M9,5V7H21V5H9M3,10H7V14H3V10M9,11V13H21V11H9M3,16H7V20H3V16M9,17V19H21V17H9"/>
                  </svg>
                  <span style="color: var(--text-secondary); font-size: 0.85rem;"><strong id="hubSaveWorkflowStepCount">0</strong> steps will be saved</span>
                </div>
                <div id="hubSaveWorkflowSteps" style="font-size: 0.8rem; color: var(--text-muted); max-height: 100px; overflow-y: auto;"></div>
              </div>
              <div id="hubSaveWorkflowError" class="alert alert-danger mt-3" style="display: none;"></div>
            </div>
            <div class="modal-footer">
              <button type="button" class="modern-btn modern-btn-secondary" data-bs-dismiss="modal">Cancel</button>
              <button type="button" id="hubConfirmSaveWorkflowBtn" class="modern-btn modern-btn-primary">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3M19,19H5V5H16.17L19,7.83V19M12,12C10.34,12 9,13.34 9,15C9,16.66 10.34,18 12,18C13.66,18 15,16.66 15,15C15,13.34 13.66,12 12,12M6,6H15V10H6V6Z"/>
                </svg>
                Save to Library
              </button>
            </div>
          </div>
        </div>
      `;
      document.body.appendChild(modal);
    }

    const nameInput = modal.querySelector('#hubSaveWorkflowName');
    const descInput = modal.querySelector('#hubSaveWorkflowDesc');
    const categoryInput = modal.querySelector('#hubSaveWorkflowCategory');
    const stepCountEl = modal.querySelector('#hubSaveWorkflowStepCount');
    const stepsEl = modal.querySelector('#hubSaveWorkflowSteps');
    const errorEl = modal.querySelector('#hubSaveWorkflowError');
    const confirmBtn = modal.querySelector('#hubConfirmSaveWorkflowBtn');

    nameInput.value = task.name || task.description || '';
    descInput.value = task.description || '';
    categoryInput.value = 'workspace';
    stepCountEl.textContent = subtasks.length;
    stepsEl.innerHTML = subtasks.map((s, i) =>
      `<div>${i + 1}. ${escapeHtml(s.name || s.description || 'Unnamed step')}</div>`
    ).join('');
    errorEl.style.display = 'none';

    const newConfirmBtn = confirmBtn.cloneNode(true);
    confirmBtn.parentNode.replaceChild(newConfirmBtn, confirmBtn);

    newConfirmBtn.addEventListener('click', async () => {
      const name = nameInput.value.trim();
      if (!name) {
        errorEl.textContent = 'Please enter a workflow name';
        errorEl.style.display = 'block';
        return;
      }

      newConfirmBtn.disabled = true;
      newConfirmBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Saving...';
      errorEl.style.display = 'none';

      try {
        const workflowData = {
          name: name,
          description: descInput.value.trim(),
          category: categoryInput.value.trim() || 'workspace',
          nodes: subtasks.map((subtask, index) => ({
            id: subtask.id || `node-${index}`,
            type: 'task',
            config: {
              name: subtask.name || subtask.description || `Step ${index + 1}`,
              description: subtask.description || '',
              to: subtask.to || 'unassigned'
            },
            relative_x: 0,
            relative_y: index * 100
          })),
          internal_connections: [],
          layout: { width: 300, height: subtasks.length * 100 + 100, node_positions: {} }
        };

        const response = await fetch('/api/workflows', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(workflowData)
        });

        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || 'Failed to save workflow');
        }

        bootstrap.Modal.getInstance(modal).hide();
        if (window.Toast) window.Toast.success(`Workflow "${name}" saved to library`);
      } catch (error) {
        console.error('Failed to save workflow:', error);
        errorEl.textContent = 'Failed to save: ' + error.message;
        errorEl.style.display = 'block';
      } finally {
        newConfirmBtn.disabled = false;
        newConfirmBtn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3M19,19H5V5H16.17L19,7.83V19M12,12C10.34,12 9,13.34 9,15C9,16.66 10.34,18 12,18C13.66,18 15,16.66 15,15C15,13.34 13.66,12 12,12M6,6H15V10H6V6Z"/></svg> Save to Library';
      }
    });

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();
    modal.addEventListener('shown.bs.modal', () => nameInput.focus(), { once: true });
  }

  // Expose tasks manager globally
  window.WorkspaceHubTasks = {
    getSubtasksForParent,
    deleteTask,
    executeTask,
    pollTaskCompletion,
    exportWorkflowTask,
    bulkDeleteTasks,
    loadTasks,
    renderStats,
    renderSchedules,
    renderTasksList,
    saveTaskAsWorkflow
  };
})();
