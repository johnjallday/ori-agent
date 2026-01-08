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
    const hasAgent = task.to; // Task has an assigned agent
    const canExecute = hasAgent && (task.status === 'pending' || task.status === 'assigned');
    const isCompleted = task.status === 'completed';
    const hasResult = task.result && isCompleted;
    const hasInputTasks = task.input_task_ids && task.input_task_ids.length > 0;
    const hasCombinationMode = task.result_combination_mode && task.result_combination_mode !== 'default';
    const hasSchedule = task.schedule && task.schedule_enabled;

    // For tasks without agents, show checkbox for manual completion
    const checkboxHTML = !hasAgent ? `
      <input type="checkbox" class="form-check-input me-2"
             ${isCompleted ? 'checked' : ''}
             onclick="event.stopPropagation(); workspaceDashboard.toggleTaskComplete('${task.id}', this.checked)"
             style="cursor: pointer; width: 20px; height: 20px;">
    ` : '';

    // Schedule indicator
    const scheduleIndicator = hasSchedule ? `
      <span style="color: #f59e0b;" title="Scheduled: ${task.schedule_name || task.schedule.type}${task.next_run ? ' - Next: ' + new Date(task.next_run).toLocaleString() : ''}">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
          <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
        </svg>
        ${task.schedule_name || task.schedule.type}
      </span>
    ` : '';

    // Agent info (only if task has agents assigned)
    const agentInfoHTML = hasAgent ? `
      <div class="d-flex gap-3 text-muted small flex-wrap">
        ${task.from ? `<span>From: ${this.escapeHtml(task.from)}</span>` : ''}
        <span>To: ${this.escapeHtml(task.to)}</span>
        ${task.priority ? `<span>Priority: ${task.priority}</span>` : ''}
        ${hasInputTasks ? `<span style="color: #9b59b6;" title="Uses results from ${task.input_task_ids.length} task(s)">🔗 ${task.input_task_ids.length} input(s)</span>` : ''}
        ${hasCombinationMode ? `<span style="color: #e67e22;" title="Combination mode: ${task.result_combination_mode}">⚙️ ${this.escapeHtml(task.result_combination_mode)}</span>` : ''}
        ${scheduleIndicator}
      </div>
    ` : `
      <div class="d-flex gap-3 text-muted small">
        ${task.priority ? `<span>Priority: ${task.priority}</span>` : ''}
        ${task.details ? `<span>${this.escapeHtml(task.details.substring(0, 50))}${task.details.length > 50 ? '...' : ''}</span>` : ''}
      </div>
    `;

    return `
      <div class="task-item modern-card p-3 mb-2" data-task-id="${task.id}" style="position: relative; cursor: pointer; ${isCompleted && !hasAgent ? 'opacity: 0.7;' : ''}" onclick="workspaceDashboard.showTaskDetails('${task.id}')">
        ${hasResult ? `
          <span class="position-absolute top-0 end-0 m-2" title="This task has a result that can be used in other tasks" style="cursor: help;">
            📊
          </span>
        ` : ''}
        <div class="d-flex justify-content-between align-items-start" onclick="event.stopPropagation();">
          <div class="flex-grow-1">
            <div class="d-flex align-items-center gap-2 mb-2">
              ${checkboxHTML}
              ${hasAgent ? statusIcon : ''}
              <h6 class="mb-0" style="color: var(--text-primary); ${isCompleted && !hasAgent ? 'text-decoration: line-through;' : ''}">${this.escapeHtml(task.description)}</h6>
            </div>
            ${agentInfoHTML}
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
    const from = document.getElementById('task-from')?.value || '';
    const to = document.getElementById('task-to')?.value || '';
    const description = document.getElementById('task-description').value;
    const details = document.getElementById('task-details')?.value || '';
    const priority = parseInt(document.getElementById('task-priority').value) || 0;

    // Get selected input task IDs
    const inputTasksSelect = document.getElementById('task-input-tasks');
    const inputTaskIds = inputTasksSelect ? Array.from(inputTasksSelect.selectedOptions).map(opt => opt.value) : [];

    // Get combination mode and instruction
    const combinationMode = document.getElementById('task-combination-mode')?.value || 'default';
    const combinationInstruction = document.getElementById('task-combination-instruction')?.value || '';

    // Get schedule fields
    const scheduleEnabled = document.getElementById('task-schedule-enabled')?.checked || false;
    const scheduleName = document.getElementById('task-schedule-name')?.value || '';
    const scheduleType = document.getElementById('task-schedule-type')?.value || 'daily';

    // Only description is required - from/to are optional for simple tasks
    if (!description) {
      alert('Please enter a task description');
      return;
    }

    // Build request body
    const requestBody = {
      workspace_id: this.workspaceId,
      description: description,
      priority: priority,
    };

    // Add schedule if enabled
    if (scheduleEnabled) {
      requestBody.schedule_enabled = true;
      if (scheduleName) requestBody.schedule_name = scheduleName;

      // Build schedule config based on type
      const schedule = { type: scheduleType };

      switch (scheduleType) {
        case 'daily':
          schedule.time_of_day = document.getElementById('task-schedule-time')?.value || '09:00';
          break;
        case 'weekly':
          schedule.time_of_day = document.getElementById('task-schedule-time')?.value || '09:00';
          schedule.day_of_week = parseInt(document.getElementById('task-schedule-day')?.value) || 0;
          break;
        case 'interval':
          const intervalValue = parseInt(document.getElementById('task-schedule-interval-value')?.value) || 1;
          const intervalUnit = document.getElementById('task-schedule-interval-unit')?.value || 'h';
          // Convert to nanoseconds (Go time.Duration)
          if (intervalUnit === 'm') {
            schedule.interval = intervalValue * 60 * 1000000000; // minutes to nanoseconds
          } else {
            schedule.interval = intervalValue * 60 * 60 * 1000000000; // hours to nanoseconds
          }
          break;
        case 'once':
          const datetime = document.getElementById('task-schedule-datetime')?.value;
          if (datetime) {
            schedule.execute_at = new Date(datetime).toISOString();
          }
          break;
      }

      requestBody.schedule = schedule;
    }

    // Add optional fields
    if (from) requestBody.from = from;
    if (to) requestBody.to = to;
    if (details) requestBody.details = details;

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
      const params = new URLSearchParams({ id: taskId });
      if (this.parent?.workspaceId) {
        params.append('workspace_id', this.parent.workspaceId);
      }

      const response = await fetch(`/api/orchestration/tasks?${params.toString()}`, {
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

  async toggleTaskComplete(taskId, completed) {
    try {
      if (completed) {
        // Mark as completed via the complete endpoint
        const response = await fetch(`/api/orchestration/tasks/${taskId}/complete`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
        });

        if (!response.ok) {
          const error = await response.text();
          throw new Error(error || 'Failed to complete task');
        }
      } else {
        // Mark as pending via the update endpoint
        const response = await fetch(`/api/orchestration/tasks/${taskId}`, {
          method: 'PUT',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            task_id: taskId,
            status: 'pending',
          }),
        });

        if (!response.ok) {
          const error = await response.text();
          throw new Error(error || 'Failed to update task');
        }
      }

      // Reload tasks to show updated status
      await this.loadTasks();
      this.renderTaskList();
    } catch (error) {
      console.error('Error toggling task completion:', error);
      this.showToast('❌ Failed to update task: ' + error.message, 'error');
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

      // Update template hints
      this.updateTemplateHints(inputTasksSelect.selectedOptions.length);
    });

    // Show/hide custom instruction field based on combination mode
    combinationModeSelect.addEventListener('change', () => {
      const isCustomMode = combinationModeSelect.value === 'custom';
      const hasInputTasks = inputTasksSelect.selectedOptions.length > 0;
      combinationInstructionContainer.style.display = (isCustomMode && hasInputTasks) ? 'block' : 'none';
    });
  }

  updateTemplateHints(inputCount) {
    const hintsContainer = document.getElementById('template-hints-container');
    const hintsContent = document.getElementById('template-hints-content');

    if (!hintsContainer || !hintsContent) {
      return;
    }

    // Hide hints if no input tasks selected
    if (inputCount === 0) {
      hintsContainer.style.display = 'none';
      return;
    }

    // Show hints and populate content
    hintsContainer.style.display = 'block';

    let hintsHTML = '';

    // Shortcuts (always available when there are inputs)
    hintsHTML += '<small class="d-block" style="font-family: monospace; color: #495057;"><code>{previous}</code> or <code>{result}</code> - most recent input</small>';

    // Numbered placeholders
    for (let i = 1; i <= inputCount; i++) {
      hintsHTML += `<small class="d-block" style="font-family: monospace; color: #495057;"><code>{input${i}}</code> - input #${i}</small>`;
    }

    hintsContent.innerHTML = hintsHTML;
  }

  toggleScheduleFields() {
    const checkbox = document.getElementById('task-schedule-enabled');
    const container = document.getElementById('schedule-fields-container');
    if (checkbox && container) {
      container.style.display = checkbox.checked ? 'block' : 'none';
    }
  }

  updateScheduleTypeFields() {
    const scheduleType = document.getElementById('task-schedule-type')?.value || 'daily';

    // Get field containers
    const timeField = document.getElementById('schedule-time-field');
    const dayField = document.getElementById('schedule-day-field');
    const intervalField = document.getElementById('schedule-interval-field');
    const onceField = document.getElementById('schedule-once-field');

    // Hide all first
    if (timeField) timeField.style.display = 'none';
    if (dayField) dayField.style.display = 'none';
    if (intervalField) intervalField.style.display = 'none';
    if (onceField) onceField.style.display = 'none';

    // Show relevant fields based on type
    switch (scheduleType) {
      case 'daily':
        if (timeField) timeField.style.display = 'block';
        break;
      case 'weekly':
        if (timeField) timeField.style.display = 'block';
        if (dayField) dayField.style.display = 'block';
        break;
      case 'interval':
        if (intervalField) intervalField.style.display = 'block';
        break;
      case 'once':
        if (onceField) onceField.style.display = 'block';
        break;
    }
  }

}
