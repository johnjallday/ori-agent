/**
 * task-modal-controller.js
 *
 * Shared controller for the task modal used across the application.
 * Handles creating, editing, and scheduling tasks from any context.
 */

class TaskModalController {
  constructor() {
    this.editingTaskId = null;
    this.workspaceId = null;
    this.onSaveCallback = null;
    this.initialized = false;
  }

  /**
   * Initialize the modal controller and bind event handlers
   */
  init() {
    if (this.initialized) return;

    // Close button handlers
    document.getElementById('taskModalClose')?.addEventListener('click', () => this.close());
    document.getElementById('taskModalCancel')?.addEventListener('click', () => this.close());
    document.querySelector('.task-modal-backdrop')?.addEventListener('click', () => this.close());

    // Save button
    document.getElementById('taskModalSave')?.addEventListener('click', () => this.save());

    // Schedule fields toggle
    document.getElementById('taskModalScheduleEnabled')?.addEventListener('change', (e) => {
      const scheduleFields = document.getElementById('taskModalScheduleFields');
      if (scheduleFields) {
        scheduleFields.style.display = e.target.checked ? 'block' : 'none';
      }
    });

    // Schedule type change handler
    document.getElementById('taskModalScheduleType')?.addEventListener('change', () => {
      this.updateScheduleTypeFields();
    });

    // Auto-save fields toggle
    document.getElementById('taskModalAutoSaveEnabled')?.addEventListener('change', (e) => {
      const autoSaveFields = document.getElementById('taskModalAutoSaveFields');
      if (autoSaveFields) {
        autoSaveFields.style.display = e.target.checked ? 'block' : 'none';
      }
    });

    // Auto-save target change handler
    document.getElementById('taskModalAutoSaveTarget')?.addEventListener('change', () => {
      this.updateAutoSaveTargetFields();
    });

    // Escape key handler
    document.getElementById('taskModal')?.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        this.close();
      }
    });

    this.initialized = true;
  }

  /**
   * Open the modal for creating a new task
   * @param {string} workspaceId - The workspace ID to create the task in
   * @param {string} prefillTitle - Optional title to prefill
   * @param {function} onSave - Optional callback after successful save
   */
  openForCreate(workspaceId, prefillTitle = '', onSave = null) {
    this.init();
    this.editingTaskId = null;
    this.workspaceId = workspaceId;
    this.onSaveCallback = onSave;

    const modal = document.getElementById('taskModal');
    if (!modal) return;

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      modalTitle.textContent = 'Create Task';
    }

    // Clear/prefill form
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    const priorityInput = document.getElementById('taskModalPriority');
    const assignmentInput = document.getElementById('taskModalAssignment');

    if (descriptionInput) descriptionInput.value = prefillTitle;
    if (detailsInput) detailsInput.value = '';
    if (priorityInput) priorityInput.value = '3';

    // Populate agent assignment dropdown
    this.populateAgentDropdown(workspaceId);
    if (assignmentInput) assignmentInput.value = '';

    // Reset schedule fields
    this.resetScheduleFields();

    // Reset auto-save fields
    this.resetAutoSaveFields();

    // Show modal
    modal.style.display = 'flex';

    // Focus title input
    setTimeout(() => {
      descriptionInput?.focus();
    }, 100);
  }

  /**
   * Open the modal for editing an existing task
   * @param {object} task - The task object to edit
   * @param {function} onSave - Optional callback after successful save
   */
  openForEdit(task, onSave = null) {
    this.init();
    this.editingTaskId = task.id;
    this.workspaceId = task.workspace_id || task.studio_id;
    this.onSaveCallback = onSave;

    const modal = document.getElementById('taskModal');
    if (!modal) return;

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      modalTitle.textContent = 'Edit Task';
    }

    // Populate agent assignment dropdown first
    this.populateAgentDropdown(this.workspaceId, task);

    // Set priority
    const priorityInput = document.getElementById('taskModalPriority');
    if (priorityInput) {
      priorityInput.value = task.priority || '3';
    }

    // Populate form fields
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    if (descriptionInput) descriptionInput.value = task.description || '';
    if (detailsInput) detailsInput.value = task.details || '';

    // Populate schedule fields
    this.populateScheduleFields(task);

    // Populate auto-save fields
    this.populateAutoSaveFields(task);

    // Show modal
    modal.style.display = 'flex';

    // Focus title input
    setTimeout(() => {
      descriptionInput?.focus();
    }, 100);
  }

  /**
   * Close the modal
   */
  close() {
    const modal = document.getElementById('taskModal');
    if (modal) {
      modal.style.display = 'none';
    }
    this.editingTaskId = null;
    this.workspaceId = null;
    this.onSaveCallback = null;
  }

  /**
   * Save the task (create or update)
   */
  async save() {
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    const priorityInput = document.getElementById('taskModalPriority');
    const assignmentInput = document.getElementById('taskModalAssignment');

    const description = descriptionInput?.value?.trim();
    const details = detailsInput?.value?.trim() || '';
    const priority = parseInt(priorityInput?.value || '3', 10);
    const assignment = assignmentInput?.value || '';

    if (!description) {
      this.showToast('Task title is required', 'error');
      descriptionInput?.focus();
      return;
    }

    if (!this.workspaceId) {
      this.showToast('No workspace selected', 'error');
      return;
    }

    // Parse assignment to get 'to' and 'assigned_node_id'
    let to = '';
    let assignedNodeId = '';
    if (assignment && assignment.startsWith('node:')) {
      assignedNodeId = assignment.slice('node:'.length);
      // Derive agent name from node ID (e.g., "agent-name-node-1" -> "agent-name")
      const match = assignedNodeId.match(/^(.+)-node-\d+$/);
      to = match ? match[1] : assignedNodeId;
    }

    try {
      // Get schedule data
      const scheduleData = this.getScheduleData();

      // Get auto-save data
      const autoSaveData = this.getAutoSaveData();

      if (this.editingTaskId) {
        // Update existing task
        const response = await fetch(`/api/orchestration/tasks/${this.editingTaskId}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            task_id: this.editingTaskId,
            description,
            details,
            to: to || undefined,
            assigned_node_id: assignedNodeId || undefined,
            ...scheduleData,
            ...autoSaveData
          })
        });

        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || 'Failed to update task');
        }
        this.showToast('Task updated', 'success');
      } else {
        // Create new task
        const response = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            studio_id: this.workspaceId,
            description,
            details,
            priority,
            to: to || undefined,
            assigned_node_id: assignedNodeId || undefined,
            ...scheduleData,
            ...autoSaveData
          })
        });

        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || 'Failed to create task');
        }
        this.showToast('Task created', 'success');
      }

      this.close();

      // Call the callback if provided
      if (this.onSaveCallback) {
        this.onSaveCallback();
      }
    } catch (error) {
      console.error('Failed to save task:', error);
      this.showToast(error.message || 'Failed to save task', 'error');
    }
  }

  /**
   * Update schedule type fields visibility
   */
  updateScheduleTypeFields() {
    const scheduleType = document.getElementById('taskModalScheduleType')?.value || 'interval';

    const timeField = document.getElementById('taskModalScheduleTimeField');
    const dayField = document.getElementById('taskModalScheduleDayField');
    const intervalField = document.getElementById('taskModalScheduleIntervalField');
    const onceField = document.getElementById('taskModalScheduleOnceField');

    // Hide all
    if (timeField) timeField.style.display = 'none';
    if (dayField) dayField.style.display = 'none';
    if (intervalField) intervalField.style.display = 'none';
    if (onceField) onceField.style.display = 'none';

    // Show relevant fields
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

  /**
   * Reset schedule fields to defaults
   */
  resetScheduleFields() {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    const scheduleFields = document.getElementById('taskModalScheduleFields');
    const scheduleName = document.getElementById('taskModalScheduleName');
    const scheduleType = document.getElementById('taskModalScheduleType');
    const scheduleTime = document.getElementById('taskModalScheduleTime');
    const scheduleDay = document.getElementById('taskModalScheduleDay');
    const scheduleIntervalValue = document.getElementById('taskModalScheduleIntervalValue');
    const scheduleIntervalUnit = document.getElementById('taskModalScheduleIntervalUnit');
    const scheduleDatetime = document.getElementById('taskModalScheduleDatetime');

    if (enabledCheckbox) enabledCheckbox.checked = false;
    if (scheduleFields) scheduleFields.style.display = 'none';
    if (scheduleName) scheduleName.value = '';
    if (scheduleType) scheduleType.value = 'interval';
    if (scheduleTime) scheduleTime.value = '09:00';
    if (scheduleDay) scheduleDay.value = '1';
    if (scheduleIntervalValue) scheduleIntervalValue.value = '1';
    if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
    if (scheduleDatetime) scheduleDatetime.value = '';

    this.updateScheduleTypeFields();
  }

  /**
   * Populate schedule fields from task
   */
  populateScheduleFields(task) {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    const scheduleFields = document.getElementById('taskModalScheduleFields');
    const scheduleName = document.getElementById('taskModalScheduleName');
    const scheduleType = document.getElementById('taskModalScheduleType');
    const scheduleTime = document.getElementById('taskModalScheduleTime');
    const scheduleDay = document.getElementById('taskModalScheduleDay');
    const scheduleIntervalValue = document.getElementById('taskModalScheduleIntervalValue');
    const scheduleIntervalUnit = document.getElementById('taskModalScheduleIntervalUnit');
    const scheduleDatetime = document.getElementById('taskModalScheduleDatetime');

    if (task.schedule_enabled && task.schedule) {
      if (enabledCheckbox) enabledCheckbox.checked = true;
      if (scheduleFields) scheduleFields.style.display = 'block';
      if (scheduleName) scheduleName.value = task.schedule_name || '';
      if (scheduleType) scheduleType.value = task.schedule.type || 'interval';

      // Populate type-specific fields
      const schedule = task.schedule;
      if (schedule.time && scheduleTime) scheduleTime.value = schedule.time;
      if (schedule.time_of_day && scheduleTime) scheduleTime.value = schedule.time_of_day;
      if (schedule.day_of_week != null && scheduleDay) scheduleDay.value = schedule.day_of_week;

      if (schedule.interval_minutes) {
        const minutes = schedule.interval_minutes;
        if (minutes >= 1440 && minutes % 1440 === 0) {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes / 1440;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'days';
        } else if (minutes >= 60 && minutes % 60 === 0) {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes / 60;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
        } else {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'minutes';
        }
      }

      if (schedule.run_at && scheduleDatetime) {
        const date = new Date(schedule.run_at);
        scheduleDatetime.value = date.toISOString().slice(0, 16);
      }
      if (schedule.execute_at && scheduleDatetime) {
        const date = new Date(schedule.execute_at);
        scheduleDatetime.value = date.toISOString().slice(0, 16);
      }

      this.updateScheduleTypeFields();
    } else {
      this.resetScheduleFields();
    }
  }

  /**
   * Get schedule data from modal fields
   */
  getScheduleData() {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    if (!enabledCheckbox?.checked) {
      return { schedule_enabled: false };
    }

    const scheduleType = document.getElementById('taskModalScheduleType')?.value || 'interval';
    const scheduleName = document.getElementById('taskModalScheduleName')?.value || '';

    const schedule = { type: scheduleType };

    switch (scheduleType) {
      case 'daily':
        schedule.time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
        break;
      case 'weekly':
        schedule.time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
        schedule.day_of_week = document.getElementById('taskModalScheduleDay')?.value || 'monday';
        break;
      case 'interval':
        const intervalValue = parseInt(document.getElementById('taskModalScheduleIntervalValue')?.value || '1', 10);
        const intervalUnit = document.getElementById('taskModalScheduleIntervalUnit')?.value || 'hours';
        let intervalMinutes = intervalValue;
        if (intervalUnit === 'hours') {
          intervalMinutes = intervalValue * 60;
        } else if (intervalUnit === 'days') {
          intervalMinutes = intervalValue * 1440;
        }
        schedule.interval_minutes = intervalMinutes;
        break;
      case 'once':
        const datetime = document.getElementById('taskModalScheduleDatetime')?.value;
        if (datetime) {
          schedule.run_at = new Date(datetime).toISOString();
        }
        break;
    }

    return {
      schedule,
      schedule_enabled: true,
      schedule_name: scheduleName
    };
  }

  /**
   * Update auto-save target fields visibility
   */
  updateAutoSaveTargetFields() {
    const target = document.getElementById('taskModalAutoSaveTarget')?.value || 'default';

    const storeNodeField = document.getElementById('taskModalAutoSaveStoreNodeField');
    const pathField = document.getElementById('taskModalAutoSavePathField');

    // Hide all
    if (storeNodeField) storeNodeField.style.display = 'none';
    if (pathField) pathField.style.display = 'none';

    // Show relevant fields
    switch (target) {
      case 'store':
        if (storeNodeField) storeNodeField.style.display = 'block';
        break;
      case 'custom':
        if (pathField) pathField.style.display = 'block';
        break;
      // 'default' shows no additional fields
    }
  }

  /**
   * Reset auto-save fields to defaults
   */
  resetAutoSaveFields() {
    const enabledCheckbox = document.getElementById('taskModalAutoSaveEnabled');
    const autoSaveFields = document.getElementById('taskModalAutoSaveFields');
    const targetSelect = document.getElementById('taskModalAutoSaveTarget');
    const storeNodeSelect = document.getElementById('taskModalAutoSaveStoreNode');
    const pathInput = document.getElementById('taskModalAutoSavePath');
    const formatSelect = document.getElementById('taskModalAutoSaveFormat');

    if (enabledCheckbox) enabledCheckbox.checked = false;
    if (autoSaveFields) autoSaveFields.style.display = 'none';
    if (targetSelect) targetSelect.value = 'default';
    if (storeNodeSelect) storeNodeSelect.innerHTML = '';
    if (pathInput) pathInput.value = '';
    if (formatSelect) formatSelect.value = 'text';

    this.updateAutoSaveTargetFields();
  }

  /**
   * Populate auto-save fields from task
   */
  populateAutoSaveFields(task) {
    const enabledCheckbox = document.getElementById('taskModalAutoSaveEnabled');
    const autoSaveFields = document.getElementById('taskModalAutoSaveFields');
    const targetSelect = document.getElementById('taskModalAutoSaveTarget');
    const pathInput = document.getElementById('taskModalAutoSavePath');
    const formatSelect = document.getElementById('taskModalAutoSaveFormat');

    if (task.result_storage?.enabled) {
      if (enabledCheckbox) enabledCheckbox.checked = true;
      if (autoSaveFields) autoSaveFields.style.display = 'block';

      // Determine target type
      if (task.result_storage.store_node_id) {
        if (targetSelect) targetSelect.value = 'store';
        // TODO: Populate and select store node
      } else if (task.result_storage.file_path) {
        if (targetSelect) targetSelect.value = 'custom';
        if (pathInput) pathInput.value = task.result_storage.file_path;
      } else {
        if (targetSelect) targetSelect.value = 'default';
      }

      if (formatSelect && task.result_storage.format) {
        formatSelect.value = task.result_storage.format;
      }

      this.updateAutoSaveTargetFields();
    } else {
      this.resetAutoSaveFields();
    }
  }

  /**
   * Get auto-save data from modal fields
   */
  getAutoSaveData() {
    const enabledCheckbox = document.getElementById('taskModalAutoSaveEnabled');
    if (!enabledCheckbox?.checked) {
      return { result_storage: null };
    }

    const target = document.getElementById('taskModalAutoSaveTarget')?.value || 'default';
    const format = document.getElementById('taskModalAutoSaveFormat')?.value || 'text';

    const resultStorage = {
      enabled: true,
      format: format
    };

    switch (target) {
      case 'store':
        const storeNodeId = document.getElementById('taskModalAutoSaveStoreNode')?.value;
        if (storeNodeId) {
          resultStorage.store_node_id = storeNodeId;
        }
        break;
      case 'custom':
        const filePath = document.getElementById('taskModalAutoSavePath')?.value?.trim();
        if (filePath) {
          resultStorage.file_path = filePath;
        }
        break;
      // 'default' uses workspace output folder (no additional config needed)
    }

    return { result_storage: resultStorage };
  }

  /**
   * Populate the agent assignment dropdown with all available agents
   * @param {string} workspaceId - The workspace ID (for context, not used for filtering)
   * @param {object} task - Optional task to set current assignment
   */
  async populateAgentDropdown(workspaceId, task = null) {
    const selectEl = document.getElementById('taskModalAssignment');
    if (!selectEl) return;

    // Start with default option
    const options = [{ label: '-- No agent (manual task) --', value: '' }];

    try {
      // Fetch all available agents (not workspace-specific)
      const response = await fetch('/api/agents/dashboard/list');
      if (response.ok) {
        const data = await response.json();
        const agents = data.agents || [];

        agents.forEach(agent => {
          // Use agent name as node ID (agent-name-node-1 format)
          const nodeId = `${agent.name}-node-1`;
          options.push({
            label: agent.name,
            value: `node:${nodeId}`
          });
        });
      }
    } catch (err) {
      console.error('Failed to load agents:', err);
    }

    // Build options HTML
    selectEl.innerHTML = options
      .map(o => `<option value="${o.value}">${o.label}</option>`)
      .join('');

    // Set current value if editing
    if (task?.assigned_node_id) {
      selectEl.value = `node:${task.assigned_node_id}`;
    } else if (task?.to) {
      // Try to match by agent name
      const nodeId = `${task.to}-node-1`;
      selectEl.value = `node:${nodeId}`;
    } else {
      selectEl.value = '';
    }
  }

  /**
   * Show a toast notification
   */
  showToast(message, type = 'info') {
    // Try to use existing toast mechanism if available
    if (window.sessionManager?.showToast) {
      window.sessionManager.showToast(message, type);
    } else if (window.workspaceDashboard?.showToast) {
      window.workspaceDashboard.showToast('', message, type);
    } else {
      // Fallback to alert
      alert(message);
    }
  }
}

// Create global instance
window.taskModalController = new TaskModalController();

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  window.taskModalController.init();
});
