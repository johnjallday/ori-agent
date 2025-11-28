/**
 * Dashboard Scheduled Tasks Module
 * Handles scheduled tasks tab, scheduled task CRUD, and schedule management
 */

export class DashboardScheduled {
  constructor(parent) {
    this.parent = parent;
  }

  renderScheduledTasksTab() {
    const ws = this.data.workspace;
    if (!ws) return '<p>Loading...</p>';

    return `
      <div class="modern-card p-4">
        <div class="d-flex justify-content-between align-items-center mb-3">
          <h5 style="color: var(--text-primary);" class="mb-0">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" class="me-2">
              <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
            </svg>
            Scheduled Tasks
          </h5>
          <button class="modern-btn modern-btn-primary modern-btn-sm" onclick="workspaceDashboard.showCreateScheduledTaskForm()">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
              <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
            </svg>
            Create Scheduled Task
          </button>
        </div>

        <!-- Create Scheduled Task Form -->
        <div id="create-scheduled-task-form" style="display: none; background: var(--surface-color); border-radius: var(--radius-md);" class="mb-3 p-3">
          <form id="scheduled-task-form" onsubmit="event.preventDefault(); workspaceDashboard.createScheduledTask();">
            <div class="row">
              <div class="col-md-6 mb-2">
                <label class="form-label small" style="color: var(--text-primary);">Name</label>
                <input type="text" id="st-name" class="form-control form-control-sm" placeholder="e.g., Daily Status Report" required>
              </div>
              <div class="col-md-6 mb-2">
                <label class="form-label small" style="color: var(--text-primary);">Schedule Type</label>
                <select id="st-schedule-type" class="form-control form-control-sm" onchange="workspaceDashboard.updateScheduleFields()" required>
                  <option value="daily">Daily</option>
                  <option value="weekly">Weekly</option>
                  <option value="interval">Interval</option>
                  <option value="once">Once</option>
                </select>
              </div>
            </div>

            <div class="mb-2">
              <label class="form-label small" style="color: var(--text-primary);">Description</label>
              <input type="text" id="st-description" class="form-control form-control-sm" placeholder="What does this scheduled task do?">
            </div>

            <div class="row">
              <div class="col-md-6 mb-2">
                <label class="form-label small" style="color: var(--text-primary);">From (Sender Agent)</label>
                <select id="st-from" class="form-control form-control-sm" required>
                  ${this.data.agents.map(agent => `<option value="${this.escapeHtml(agent)}">${this.escapeHtml(agent)}</option>`).join('')}
                </select>
              </div>
              <div class="col-md-6 mb-2">
                <label class="form-label small" style="color: var(--text-primary);">To (Recipient Agent)</label>
                <select id="st-to" class="form-control form-control-sm" required>
                  ${this.data.agents.map(agent => `<option value="${this.escapeHtml(agent)}">${this.escapeHtml(agent)}</option>`).join('')}
                </select>
              </div>
            </div>

            <div class="mb-2">
              <label class="form-label small" style="color: var(--text-primary);">Task Prompt</label>
              <textarea id="st-prompt" class="form-control form-control-sm" rows="2" placeholder="What should the agent do when this task runs?" required></textarea>
            </div>

            <!-- Schedule-specific fields -->
            <div id="schedule-fields">
              <div id="daily-fields" style="display: block;">
                <div class="mb-2">
                  <label class="form-label small" style="color: var(--text-primary);">Time of Day (24-hour format)</label>
                  <input type="time" id="st-time-daily" class="form-control form-control-sm" value="09:00">
                </div>
              </div>

              <div id="weekly-fields" style="display: none;">
                <div class="row">
                  <div class="col-md-6 mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Day of Week</label>
                    <select id="st-day-of-week" class="form-control form-control-sm">
                      <option value="0">Sunday</option>
                      <option value="1">Monday</option>
                      <option value="2">Tuesday</option>
                      <option value="3">Wednesday</option>
                      <option value="4">Thursday</option>
                      <option value="5">Friday</option>
                      <option value="6">Saturday</option>
                    </select>
                  </div>
                  <div class="col-md-6 mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Time of Day</label>
                    <input type="time" id="st-time-weekly" class="form-control form-control-sm" value="09:00">
                  </div>
                </div>
              </div>

              <div id="interval-fields" style="display: none;">
                <div class="row">
                  <div class="col-md-6 mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Interval Value</label>
                    <input type="number" id="st-interval-value" class="form-control form-control-sm" value="1" min="1">
                  </div>
                  <div class="col-md-6 mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Interval Unit</label>
                    <select id="st-interval-unit" class="form-control form-control-sm">
                      <option value="hours">Hours</option>
                      <option value="minutes">Minutes</option>
                      <option value="days">Days</option>
                    </select>
                  </div>
                </div>
              </div>

              <div id="once-fields" style="display: none;">
                <div class="mb-2">
                  <label class="form-label small" style="color: var(--text-primary);">Execute At</label>
                  <input type="datetime-local" id="st-execute-at" class="form-control form-control-sm">
                </div>
              </div>
            </div>

            <div class="row">
              <div class="col-md-6 mb-3">
                <label class="form-label small" style="color: var(--text-primary);">Priority</label>
                <select id="st-priority" class="form-control form-control-sm">
                  <option value="0">Low</option>
                  <option value="1">Normal</option>
                  <option value="2">High</option>
                  <option value="3">Urgent</option>
                </select>
              </div>
              <div class="col-md-6 mb-3">
                <div class="form-check mt-4">
                  <input class="form-check-input" type="checkbox" id="st-enabled" checked>
                  <label class="form-check-label small" for="st-enabled" style="color: var(--text-primary);">
                    Enabled (start immediately)
                  </label>
                </div>
              </div>
            </div>

            <div class="d-flex gap-2">
              <button type="submit" class="modern-btn modern-btn-primary modern-btn-sm">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M9,20.42L2.79,14.21L5.62,11.38L9,14.77L18.88,4.88L21.71,7.71L9,20.42Z"/>
                </svg>
                Create
              </button>
              <button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" onclick="workspaceDashboard.hideCreateScheduledTaskForm()">Cancel</button>
            </div>
          </form>
        </div>

        <!-- Scheduled Tasks List -->
        <div id="scheduled-tasks-list">
          ${this.renderScheduledTasksList()}
        </div>
      </div>
    `;
  }

  renderScheduledTasksList() {
    if (!this.data.scheduledTasks || this.data.scheduledTasks.length === 0) {
      return `
        <div class="text-center py-4" style="color: var(--text-secondary);">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" style="opacity: 0.3;">
            <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
          </svg>
          <p class="mt-2">No scheduled tasks yet</p>
          <small>Create a scheduled task to automate recurring prompts</small>
        </div>
      `;
    }

    return this.data.scheduledTasks.map(st => this.renderScheduledTask(st)).join('');
  }

  renderScheduledTask(st) {
    const scheduleDesc = this.getScheduleDescription(st.schedule);
    const nextRun = st.next_run ? new Date(st.next_run).toLocaleString() : 'Not scheduled';
    const statusBadge = st.enabled
      ? '<span class="badge bg-success">Enabled</span>'
      : '<span class="badge bg-secondary">Disabled</span>';

    const hasHistory = st.execution_history && st.execution_history.length > 0;
    const successCount = hasHistory ? st.execution_history.filter(e => e.status === 'success').length : 0;
    const failedCount = hasHistory ? st.execution_history.filter(e => e.status === 'failed').length : 0;

    return `
      <div class="modern-card p-3 mb-2" style="border-left: 3px solid ${st.enabled ? 'var(--primary-color)' : '#6c757d'};">
        <div class="d-flex justify-content-between align-items-start">
          <div class="flex-grow-1">
            <h6 style="color: var(--text-primary);" class="mb-1">
              ${this.escapeHtml(st.name)}
              ${statusBadge}
            </h6>
            <p class="small mb-1" style="color: var(--text-secondary);">${this.escapeHtml(st.description || st.prompt)}</p>
            <div class="small" style="color: var(--text-tertiary);">
              <span class="me-3">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
                </svg>
                ${scheduleDesc}
              </span>
              <span class="me-3">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M12,1L8,5H11V14H13V5H16M18,23H6C4.89,23 4,22.1 4,21V9A2,2 0 0,1 6,7H9V9H6V21H18V9H15V7H18A2,2 0 0,1 20,9V21A2,2 0 0,1 18,23Z"/>
                </svg>
                ${this.escapeHtml(st.from)} → ${this.escapeHtml(st.to)}
              </span>
              ${st.next_run ? `
              <span>
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M19,4H18V2H16V4H8V2H6V4H5A2,2 0 0,0 3,6V20A2,2 0 0,0 5,22H19A2,2 0 0,0 21,20V6A2,2 0 0,0 19,4M19,20H5V10H19V20Z"/>
                </svg>
                Next: ${nextRun}
              </span>
              ` : ''}
            </div>
            <div class="small mt-1" style="color: var(--text-tertiary);">
              ${st.execution_count > 0 ? `Executed ${st.execution_count} times` : 'Not yet executed'}
              ${hasHistory ? ` (<span class="text-success">${successCount} ✓</span> / <span class="text-danger">${failedCount} ✗</span>)` : ''}
              ${hasHistory ? `
                <button class="btn btn-link btn-sm p-0 ms-2" style="font-size: 0.75rem;" onclick="workspaceDashboard.toggleHistory('${st.id}')">
                  <span id="history-toggle-${st.id}">Show history ▼</span>
                </button>
              ` : ''}
            </div>
            ${hasHistory ? `
              <div id="history-${st.id}" style="display: none; margin-top: 0.5rem; padding: 0.5rem; background: var(--surface-color); border-radius: 4px;">
                <div class="small fw-bold mb-1" style="color: var(--text-primary);">Execution History (last ${st.execution_history.length})</div>
                ${st.execution_history.slice().reverse().slice(0, 10).map(exec => `
                  <div class="d-flex justify-content-between align-items-center py-1 border-bottom" style="font-size: 0.7rem;">
                    <span style="color: var(--text-secondary);">${new Date(exec.executed_at).toLocaleString()}</span>
                    <span>
                      ${exec.status === 'success'
                        ? `<span class="badge bg-success">✓ Success</span> <a href="#" onclick="workspaceDashboard.switchTab('overview'); return false;" class="text-muted" title="Task ID: ${exec.task_id}">#${exec.task_id.substring(0,8)}</a>`
                        : `<span class="badge bg-danger">✗ Failed</span> <span class="text-danger" style="font-size: 0.65rem;" title="${this.escapeHtml(exec.error || '')}">${this.escapeHtml((exec.error || '').substring(0, 30))}...</span>`
                      }
                    </span>
                  </div>
                `).join('')}
              </div>
            ` : ''}
          </div>
          <div class="d-flex gap-1">
            ${st.enabled ? `
              <button class="btn btn-sm btn-outline-success" onclick="workspaceDashboard.triggerScheduledTask('${st.id}')" title="Trigger Now">
                ▶
              </button>
              <button class="btn btn-sm btn-outline-warning" onclick="workspaceDashboard.toggleScheduledTask('${st.id}', false)" title="Disable">
                ⏸
              </button>
            ` : `
              <button class="btn btn-sm btn-outline-success" onclick="workspaceDashboard.toggleScheduledTask('${st.id}', true)" title="Enable">
                ▶
              </button>
            `}
            <button class="btn btn-sm btn-outline-danger" onclick="workspaceDashboard.deleteScheduledTask('${st.id}')" title="Delete">
              🗑
            </button>
          </div>
        </div>
      </div>
    `;
  }

  getScheduleDescription(schedule) {
    switch (schedule.type) {
      case 'daily':
        return `Daily at ${schedule.time_of_day}`;
      case 'weekly':
        const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
        return `Weekly on ${days[schedule.day_of_week]} at ${schedule.time_of_day}`;
      case 'interval':
        const hours = Math.floor(schedule.interval / 3600000000000);
        const minutes = Math.floor((schedule.interval % 3600000000000) / 60000000000);
        if (hours > 0) return `Every ${hours} hour${hours > 1 ? 's' : ''}`;
        return `Every ${minutes} minute${minutes > 1 ? 's' : ''}`;
      case 'once':
        return `Once at ${new Date(schedule.execute_at).toLocaleString()}`;
      default:
        return 'Custom schedule';
    }
  }

  showCreateScheduledTaskForm() {
    document.getElementById('create-scheduled-task-form').style.display = 'block';
  }

  hideCreateScheduledTaskForm() {
    document.getElementById('create-scheduled-task-form').style.display = 'none';
    document.getElementById('scheduled-task-form').reset();
  }

  updateScheduleFields() {
    const type = document.getElementById('st-schedule-type').value;
    document.getElementById('daily-fields').style.display = type === 'daily' ? 'block' : 'none';
    document.getElementById('weekly-fields').style.display = type === 'weekly' ? 'block' : 'none';
    document.getElementById('interval-fields').style.display = type === 'interval' ? 'block' : 'none';
    document.getElementById('once-fields').style.display = type === 'once' ? 'block' : 'none';
  }

  async createScheduledTask() {
    const type = document.getElementById('st-schedule-type').value;
    const name = document.getElementById('st-name').value;
    const description = document.getElementById('st-description').value;
    const from = document.getElementById('st-from').value;
    const to = document.getElementById('st-to').value;
    const prompt = document.getElementById('st-prompt').value;
    const priority = parseInt(document.getElementById('st-priority').value);
    const enabled = document.getElementById('st-enabled').checked;

    // Build schedule config based on type
    let schedule = { type };

    switch (type) {
      case 'daily':
        schedule.time_of_day = document.getElementById('st-time-daily').value;
        break;
      case 'weekly':
        schedule.time_of_day = document.getElementById('st-time-weekly').value;
        schedule.day_of_week = parseInt(document.getElementById('st-day-of-week').value);
        break;
      case 'interval':
        const value = parseInt(document.getElementById('st-interval-value').value);
        const unit = document.getElementById('st-interval-unit').value;
        let nanoseconds;
        switch (unit) {
          case 'minutes': nanoseconds = value * 60 * 1000000000; break;
          case 'hours': nanoseconds = value * 3600 * 1000000000; break;
          case 'days': nanoseconds = value * 86400 * 1000000000; break;
        }
        schedule.interval = nanoseconds;
        break;
      case 'once':
        schedule.execute_at = document.getElementById('st-execute-at').value;
        break;
    }

    try {
      const response = await fetch('/api/orchestration/scheduled-tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
          name,
          description,
          from,
          to,
          prompt,
          priority,
          schedule,
          enabled
        })
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error);
      }

      await this.loadScheduledTasks();
      this.render();
      this.hideCreateScheduledTaskForm();
      this.showToast('✅ Scheduled task created successfully!', 'success');
    } catch (error) {
      console.error('Error creating scheduled task:', error);
      this.showToast('❌ Failed to create scheduled task: ' + error.message, 'error');
    }
  }

  async toggleScheduledTask(id, enable) {
    const action = enable ? 'enable' : 'disable';
    try {
      const response = await fetch(`/api/orchestration/scheduled-tasks/${id}/${action}`, {
        method: 'POST'
      });

      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }

      await this.loadScheduledTasks();
      this.render();
      this.showToast(`✅ Scheduled task ${enable ? 'enabled' : 'disabled'}!`, 'success');
    } catch (error) {
      console.error('Error toggling scheduled task:', error);
      this.showToast('❌ Failed to toggle scheduled task', 'error');
    }
  }

  async triggerScheduledTask(id) {
    if (!confirm('Trigger this scheduled task now?')) {
      return;
    }

    try {
      const response = await fetch(`/api/orchestration/scheduled-tasks/${id}/trigger`, {
        method: 'POST'
      });

      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }

      const data = await response.json();
      await this.loadTasks();
      this.showToast(`✅ Task triggered! Task ID: ${data.task_id}`, 'success');

      // Switch to overview tab to show the new task
      if (confirm('Task created! Switch to Overview tab to see it?')) {
        this.switchTab('overview');
      }
    } catch (error) {
      console.error('Error triggering scheduled task:', error);
      this.showToast('❌ Failed to trigger scheduled task', 'error');
    }
  }

  async deleteScheduledTask(id) {
    if (!confirm('Are you sure you want to delete this scheduled task? This action cannot be undone.')) {
      return;
    }

    try {
      const response = await fetch(`/api/orchestration/scheduled-tasks/${id}`, {
        method: 'DELETE'
      });

      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }

      await this.loadScheduledTasks();
      this.render();
      this.showToast('✅ Scheduled task deleted!', 'success');
    } catch (error) {
      console.error('Error deleting scheduled task:', error);
      this.showToast('❌ Failed to delete scheduled task', 'error');
    }
  }

  toggleHistory(id) {
    const historyDiv = document.getElementById(`history-${id}`);
    const toggleText = document.getElementById(`history-toggle-${id}`);

    if (historyDiv && toggleText) {
      if (historyDiv.style.display === 'none') {
        historyDiv.style.display = 'block';
        toggleText.textContent = 'Hide history ▲';
      } else {
        historyDiv.style.display = 'none';
        toggleText.textContent = 'Show history ▼';
      }
    }
  }

}
