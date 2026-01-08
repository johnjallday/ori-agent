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
      modalTitle.textContent = 'Add Task';
    }

    // Clear/prefill form
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    if (descriptionInput) descriptionInput.value = prefillTitle;
    if (detailsInput) detailsInput.value = '';

    // Reset schedule fields
    this.resetScheduleFields();

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

    // Populate form fields
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    if (descriptionInput) descriptionInput.value = task.description || '';
    if (detailsInput) detailsInput.value = task.details || '';

    // Populate schedule fields
    this.populateScheduleFields(task);

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

    const description = descriptionInput?.value?.trim();
    const details = detailsInput?.value?.trim() || '';

    if (!description) {
      this.showToast('Task title is required', 'error');
      descriptionInput?.focus();
      return;
    }

    if (!this.workspaceId) {
      this.showToast('No workspace selected', 'error');
      return;
    }

    try {
      // Get schedule data
      const scheduleData = this.getScheduleData();

      if (this.editingTaskId) {
        // Update existing task
        const response = await fetch(`/api/orchestration/tasks/${this.editingTaskId}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            task_id: this.editingTaskId,
            description,
            details,
            ...scheduleData
          })
        });

        if (!response.ok) throw new Error('Failed to update task');
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
            priority: 3,
            ...scheduleData
          })
        });

        if (!response.ok) throw new Error('Failed to create task');
        this.showToast('Task created', 'success');
      }

      this.close();

      // Call the callback if provided
      if (this.onSaveCallback) {
        this.onSaveCallback();
      }
    } catch (error) {
      console.error('Failed to save task:', error);
      this.showToast('Failed to save task', 'error');
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
