/**
 * Dashboard Tasks Module
 * Handles task list rendering, task CRUD operations, and task details
 */

export class DashboardTasks {
  constructor(parent) {
    this.parent = parent;
  }

  renderTaskList() {
    const tasks = this.data.tasks || [];

    if (tasks.length === 0) {
      return `
        <div class="text-center py-4">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" style="color: var(--text-muted); opacity: 0.5;">
            <path d="M19,3H14.82C14.4,1.84 13.3,1 12,1C10.7,1 9.6,1.84 9.18,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5A2,2 0 0,0 19,3M12,3A1,1 0 0,1 13,4A1,1 0 0,1 12,5A1,1 0 0,1 11,4A1,1 0 0,1 12,3Z"/>
          </svg>
          <p class="text-muted mt-2">No tasks yet</p>
        </div>
      `;
    }

    return `
      <div class="task-list">
        ${tasks.map(task => this.renderTask(task)).join('')}
      </div>
    `;
  }

  renderTask(task) {
    const statusBadge = this.getStatusBadgeClass(task.status);
    const statusIcon = this.getStatusIcon(task.status);
    const canExecute = task.status === 'pending' || task.status === 'assigned';
    const hasResult = task.result && task.status === 'completed';
    const hasInputTasks = task.input_task_ids && task.input_task_ids.length > 0;
    const hasCombinationMode = task.result_combination_mode && task.result_combination_mode !== 'default';

    return `
      <div class="task-item modern-card p-3 mb-2" data-task-id="${task.id}" style="position: relative; cursor: pointer;" onclick="workspaceDashboard.showTaskDetails('${task.id}')">
        ${hasResult ? `
          <span class="position-absolute top-0 end-0 m-2" title="This task has a result that can be used in other tasks" style="cursor: help;">
            📊
          </span>
        ` : ''}
        <div class="d-flex justify-content-between align-items-start" onclick="event.stopPropagation();">
          <div class="flex-grow-1">
            <div class="d-flex align-items-center gap-2 mb-2">
              ${statusIcon}
              <h6 class="mb-0" style="color: var(--text-primary);">${this.escapeHtml(task.description)}</h6>
            </div>
            <div class="d-flex gap-3 text-muted small">
              <span>From: ${this.escapeHtml(task.from)}</span>
              <span>To: ${this.escapeHtml(task.to)}</span>
              ${task.priority ? `<span>Priority: ${task.priority}</span>` : ''}
              ${hasInputTasks ? `<span style="color: #9b59b6;" title="Uses results from ${task.input_task_ids.length} task(s)">🔗 ${task.input_task_ids.length} input(s)</span>` : ''}
              ${hasCombinationMode ? `<span style="color: #e67e22;" title="Combination mode: ${task.result_combination_mode}">⚙️ ${this.escapeHtml(task.result_combination_mode)}</span>` : ''}
            </div>
            ${task.result ? `
              <div class="alert alert-success mt-2 mb-0 py-2" style="font-size: 0.85rem;">
                <strong>Result:</strong>
                ${task.result.length > 300 ? `
                  <br>
                  <pre style="white-space: pre-wrap; margin-bottom: 0; font-size: 0.85rem; max-height: 150px; overflow: hidden;">${this.escapeHtml(task.result.substring(0, 300))}...</pre>
                  <button class="btn btn-sm btn-outline-success mt-2" onclick="workspaceDashboard.showTaskDetails('${task.id}')">
                    View Full Result
                  </button>
                ` : `
                  <br>
                  <pre style="white-space: pre-wrap; margin-bottom: 0; font-size: 0.85rem;">${this.escapeHtml(task.result)}</pre>
                `}
              </div>
            ` : ''}
            ${task.error ? `
              <div class="alert alert-danger mt-2 mb-0 py-2" style="font-size: 0.85rem;">
                <strong>Error:</strong> ${this.escapeHtml(task.error)}
              </div>
            ` : ''}
          </div>
          <div class="d-flex align-items-start gap-2">
            ${canExecute ? `
              <button class="modern-btn modern-btn-primary modern-btn-sm" onclick="workspaceDashboard.executeTask('${task.id}')">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M8,5.14V19.14L19,12.14L8,5.14Z"/>
                </svg>
                Execute Now
              </button>
            ` : ''}
            <button class="modern-btn modern-btn-danger modern-btn-sm" onclick="workspaceDashboard.deleteTask('${task.id}')" title="Delete task">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
              </svg>
            </button>
            <span class="modern-badge ${statusBadge}">
              ${this.escapeHtml(task.status)}
            </span>
          </div>
        </div>
      </div>
    `;
  }

  renderCompletedTaskOptions() {
    const tasks = this.data.tasks || [];
    const completedTasks = tasks.filter(task => task.status === 'completed' && task.result);

    if (completedTasks.length === 0) {
      return '<option disabled>No completed tasks with results available</option>';
    }

    return completedTasks.map(task => {
      const truncatedDesc = task.description.length > 50
        ? task.description.substring(0, 47) + '...'
        : task.description;
      return `<option value="${task.id}">${this.escapeHtml(truncatedDesc)} (${task.from} → ${task.to})</option>`;
    }).join('');
  }

  showCreateTaskForm() {
    const form = document.getElementById('create-task-form');
    if (form) {
      form.style.display = 'block';
    }
  }

  hideCreateTaskForm() {
    const form = document.getElementById('create-task-form');
    if (form) {
      form.style.display = 'none';
      // Reset form
      document.getElementById('task-form').reset();
    }
  }

  async createTask() {
    const from = document.getElementById('task-from').value;
    const to = document.getElementById('task-to').value;
    const description = document.getElementById('task-description').value;
    const priority = parseInt(document.getElementById('task-priority').value) || 0;

    // Get selected input task IDs
    const inputTasksSelect = document.getElementById('task-input-tasks');
    const inputTaskIds = Array.from(inputTasksSelect.selectedOptions).map(opt => opt.value);

    // Get combination mode and instruction
    const combinationMode = document.getElementById('task-combination-mode')?.value || 'default';
    const combinationInstruction = document.getElementById('task-combination-instruction')?.value || '';

    if (!from || !to || !description) {
      alert('Please fill in all required fields');
      return;
    }

    // Build request body
    const requestBody = {
      workspace_id: this.workspaceId,
      from: from,
      to: to,
      description: description,
      priority: priority,
    };

    // Add input_task_ids if any are selected
    if (inputTaskIds.length > 0) {
      requestBody.input_task_ids = inputTaskIds;

      // Add combination mode if input tasks are selected
      if (combinationMode && combinationMode !== 'default') {
        requestBody.result_combination_mode = combinationMode;
      }

      // Add custom instruction if mode is custom
      if (combinationMode === 'custom' && combinationInstruction) {
        requestBody.combination_instruction = combinationInstruction;
      }
    }

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(requestBody),
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to create task');
      }

      const result = await response.json();

      // Hide form and reload tasks
      this.hideCreateTaskForm();
      await this.loadTasks();
      this.renderTaskList();

      // Show success notification
      this.showToast('✅ Task created successfully!', 'success');
    } catch (error) {
      console.error('Error creating task:', error);
      this.showToast('❌ Failed to create task: ' + error.message, 'error');
    }
  }

  async executeTask(taskId) {
    if (!confirm('Execute this task now?')) {
      return;
    }

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          task_id: taskId,
        }),
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to execute task');
      }

      const result = await response.json();

      // Reload tasks to show updated status
      await this.loadTasks();
      this.renderTaskList();

      // Show success notification
      this.showToast('✅ Task execution started!', 'success');
    } catch (error) {
      console.error('Error executing task:', error);
      this.showToast('❌ Failed to execute task: ' + error.message, 'error');
    }
  }

  async deleteTask(taskId) {
    if (!confirm('Are you sure you want to delete this task? This action cannot be undone.')) {
      return;
    }

    try {
      const response = await fetch(`/api/orchestration/tasks?id=${taskId}`, {
        method: 'DELETE',
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to delete task');
      }

      // Reload tasks to show updated list
      await this.loadTasks();
      this.renderTaskList();

      // Show success notification
      this.showToast('Task Deleted', '✅ Task deleted successfully', 'success');
    } catch (error) {
      console.error('Error deleting task:', error);
      this.showToast('Delete Failed', '❌ Failed to delete task: ' + error.message, 'error');
    }
  }

  showTaskDetails(taskId) {
    const task = this.data.tasks.find(t => t.id === taskId);
    if (!task) {
      console.error('Task not found:', taskId);
      return;
    }

    // Get input tasks if any
    let inputTasksHTML = '';
    if (task.input_task_ids && task.input_task_ids.length > 0) {
      const inputTasks = task.input_task_ids.map(id => this.data.tasks.find(t => t.id === id)).filter(Boolean);
      if (inputTasks.length > 0) {
        inputTasksHTML = `
          <h6>Input Tasks:</h6>
          <div class="mb-3">
            ${inputTasks.map(it => `
              <div class="card mb-2" style="background-color: var(--bg-secondary);">
                <div class="card-body py-2">
                  <div class="d-flex justify-content-between align-items-start">
                    <div>
                      <strong>${this.escapeHtml(it.description.substring(0, 60))}${it.description.length > 60 ? '...' : ''}</strong>
                      <br>
                      <small class="text-muted">${it.from} → ${it.to}</small>
                    </div>
                    <button class="btn btn-sm btn-outline-primary" onclick="workspaceDashboard.showTaskDetails('${it.id}')">
                      View
                    </button>
                  </div>
                  ${it.result ? `
                    <div class="mt-2">
                      <small class="text-muted">Result:</small>
                      <pre style="font-size: 0.75rem; max-height: 100px; overflow-y: auto; background: var(--surface-color); padding: 8px; border-radius: 4px;">${this.escapeHtml(it.result.substring(0, 200))}${it.result.length > 200 ? '...' : ''}</pre>
                    </div>
                  ` : ''}
                </div>
              </div>
            `).join('')}
          </div>
        `;
      }
    }

    // Create modal HTML
    const modalHTML = `
      <div class="modal fade" id="taskDetailsModal" tabindex="-1" aria-labelledby="taskDetailsModalLabel" aria-hidden="true">
        <div class="modal-dialog modal-lg">
          <div class="modal-content" style="background-color: var(--surface-color); color: var(--text-primary);">
            <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
              <h5 class="modal-title" id="taskDetailsModalLabel">Task Details</h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body">
              <h6>Description:</h6>
              <p>${this.escapeHtml(task.description)}</p>

              <h6>Status:</h6>
              <p><span class="modern-badge ${this.getStatusBadgeClass(task.status)}">${this.escapeHtml(task.status)}</span></p>

              <h6>Details:</h6>
              <ul>
                <li><strong>From:</strong> ${this.escapeHtml(task.from)}</li>
                <li><strong>To:</strong> ${this.escapeHtml(task.to)}</li>
                <li><strong>Priority:</strong> ${task.priority || 'N/A'}</li>
                <li><strong>Created:</strong> ${new Date(task.created_at).toLocaleString()}</li>
                ${task.completed_at ? `<li><strong>Completed:</strong> ${new Date(task.completed_at).toLocaleString()}</li>` : ''}
              </ul>

              ${inputTasksHTML}

              ${task.result ? `
                <h6>Result:</h6>
                <div class="alert alert-success">
                  <div class="d-flex justify-content-between align-items-center mb-2">
                    <strong>Task Output:</strong>
                    <button class="btn btn-sm btn-outline-success" onclick="navigator.clipboard.writeText(\`${this.escapeHtml(task.result).replace(/`/g, '\\`')}\`).then(() => alert('Result copied to clipboard!'))">
                      📋 Copy
                    </button>
                  </div>
                  <pre style="white-space: pre-wrap; margin-bottom: 0; max-height: 400px; overflow-y: auto;">${this.escapeHtml(task.result)}</pre>
                </div>
              ` : ''}

              ${task.error ? `
                <h6>Error:</h6>
                <div class="alert alert-danger">
                  <pre style="white-space: pre-wrap; margin-bottom: 0;">${this.escapeHtml(task.error)}</pre>
                </div>
              ` : ''}
            </div>
            <div class="modal-footer" style="border-top: 1px solid var(--border-color);">
              ${task.result && task.status === 'completed' ? `
                <button type="button" class="btn btn-primary" onclick="workspaceDashboard.useTaskResultInNewTask('${task.id}')" data-bs-dismiss="modal">
                  ✨ Use Result in New Task
                </button>
              ` : ''}
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
            </div>
          </div>
        </div>
      </div>
    `;

    // Remove any existing modal
    const existingModal = document.getElementById('taskDetailsModal');
    if (existingModal) {
      existingModal.remove();
    }

    // Append modal to body
    document.body.insertAdjacentHTML('beforeend', modalHTML);

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('taskDetailsModal'));
    modal.show();

    // Clean up modal after it's hidden
    document.getElementById('taskDetailsModal').addEventListener('hidden.bs.modal', function () {
      this.remove();
    });
  }

  useTaskResultInNewTask(taskId) {
    // Show the create task form
    this.showCreateTaskForm();

    // Pre-select the task in the input tasks dropdown
    setTimeout(() => {
      const inputTasksSelect = document.getElementById('task-input-tasks');
      if (inputTasksSelect) {
        // Find and select the option with matching task ID
        for (let option of inputTasksSelect.options) {
          if (option.value === taskId) {
            option.selected = true;
            break;
          }
        }

        // Add a helpful placeholder text
        const descriptionField = document.getElementById('task-description');
        if (descriptionField && !descriptionField.value) {
          descriptionField.placeholder = 'Describe how to process the result from the selected task...';
          descriptionField.focus();
        }
      }
    }, 100);
  }

  initializeCombinationModeControls() {
    const inputTasksSelect = document.getElementById('task-input-tasks');
    const combinationModeContainer = document.getElementById('combination-mode-container');
    const combinationModeSelect = document.getElementById('task-combination-mode');
    const combinationInstructionContainer = document.getElementById('combination-instruction-container');

    if (!inputTasksSelect || !combinationModeContainer || !combinationModeSelect || !combinationInstructionContainer) {
      return;
    }

    // Show/hide combination mode when input tasks selection changes
    inputTasksSelect.addEventListener('change', () => {
      const hasInputTasks = inputTasksSelect.selectedOptions.length > 0;
      combinationModeContainer.style.display = hasInputTasks ? 'block' : 'none';

      // Also hide instruction container if no input tasks
      if (!hasInputTasks) {
        combinationInstructionContainer.style.display = 'none';
      }
    });

    // Show/hide custom instruction field based on combination mode
    combinationModeSelect.addEventListener('change', () => {
      const isCustomMode = combinationModeSelect.value === 'custom';
      const hasInputTasks = inputTasksSelect.selectedOptions.length > 0;
      combinationInstructionContainer.style.display = (isCustomMode && hasInputTasks) ? 'block' : 'none';
    });
  }

}
