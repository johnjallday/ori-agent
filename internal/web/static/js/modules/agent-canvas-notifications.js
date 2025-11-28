/**
 * AgentCanvasNotifications - Notifications and execution logs module
 * Handles toast notifications, execution logs, and event formatting
 */
export class AgentCanvasNotifications {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Show a toast notification
   */
  showNotification(message, type = 'info') {
    const notification = {
      id: Date.now() + Math.random(),
      message,
      type, // 'info', 'success', 'warning', 'error'
      timestamp: Date.now()
    };

    this.parent.notifications.push(notification);

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
      this.dismissNotification(notification.id);
    }, 5000);

    this.parent.draw();
  }

  /**
   * Dismiss a notification
   */
  dismissNotification(id) {
    this.parent.notifications = this.parent.notifications.filter(n => n.id !== id);
    this.parent.draw();
  }

  /**
   * Add an execution log entry for a task
   */
  addExecutionLog(taskId, type, message) {
    if (!this.parent.executionLogs[taskId]) {
      this.parent.executionLogs[taskId] = [];
    }

    this.parent.executionLogs[taskId].push({
      type,
      message,
      timestamp: new Date()
    });

    // Limit logs per task to 50 entries
    if (this.parent.executionLogs[taskId].length > 50) {
      this.parent.executionLogs[taskId] = this.parent.executionLogs[taskId].slice(-50);
    }

    this.parent.draw();
  }

  /**
   * Show execution log modal for a task
   */
  showExecutionLog(task) {
    const logs = this.parent.executionLogs[task.id] || [];

    if (logs.length === 0) {
      this.showNotification('No execution log available for this task', 'info');
      return;
    }

    // Create modal HTML
    let logsHTML = '<div style="max-height: 400px; overflow-y: auto;">';

    logs.forEach((log, index) => {
      const time = log.timestamp.toLocaleTimeString();
      let icon = '•';
      let color = '#6c757d';

      switch (log.type) {
        case 'thinking':
          icon = '🧠';
          color = '#17a2b8';
          break;
        case 'tool_call':
          icon = '🔧';
          color = '#ffc107';
          break;
        case 'tool_success':
          icon = '✓';
          color = '#28a745';
          break;
        case 'tool_error':
          icon = '✗';
          color = '#dc3545';
          break;
      }

      logsHTML += `
        <div style="padding: 8px; border-left: 3px solid ${color}; margin-bottom: 8px; background-color: #f8f9fa;">
          <div style="font-size: 11px; color: #6c757d; margin-bottom: 4px;">
            <strong>${time}</strong>
          </div>
          <div style="font-size: 12px; color: #212529;">
            <span style="margin-right: 4px;">${icon}</span>
            ${log.message}
          </div>
        </div>
      `;
    });

    logsHTML += '</div>';

    // Show modal using Bootstrap modal if available, or alert as fallback
    if (typeof bootstrap !== 'undefined' && bootstrap.Modal) {
      // Create modal element
      const modalDiv = document.createElement('div');
      modalDiv.innerHTML = `
        <div class="modal fade" id="executionLogModal" tabindex="-1">
          <div class="modal-dialog modal-lg">
            <div class="modal-content">
              <div class="modal-header">
                <h5 class="modal-title">Execution Log: ${task.description || task.id}</h5>
                <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
              </div>
              <div class="modal-body">
                ${logsHTML}
              </div>
              <div class="modal-footer">
                <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
              </div>
            </div>
          </div>
        </div>
      `;
      document.body.appendChild(modalDiv);

      const modal = new bootstrap.Modal(document.getElementById('executionLogModal'));
      modal.show();

      // Clean up after modal is hidden
      document.getElementById('executionLogModal').addEventListener('hidden.bs.modal', () => {
        modalDiv.remove();
      });
    } else {
      // Fallback: show in alert
      const logText = logs.map(log => `[${log.timestamp.toLocaleTimeString()}] ${log.message}`).join('\n');
      alert(`Execution Log:\n\n${logText}`);
    }
  }

  /**
   * Get icon for event type
   */
  getEventIcon(type) {
    const icons = {
      'task.created': '📋',
      'task.started': '⏳',
      'task.completed': '✓',
      'task.failed': '❌',
      'task.timeout': '⏰',
      'task.deleted': '🗑️',
      'workspace.progress': '📊',
      'agent.active': '🔥',
      'agent.idle': '💤',
      'workflow.started': '🔗',
      'workflow.completed': '✅',
      'workflow.failed': '💥'
    };
    return icons[type] || '•';
  }

  /**
   * Get color for event type
   */
  getEventColor(type) {
    if (type.includes('failed') || type.includes('error')) return '#ef4444';
    if (type.includes('completed')) return '#10b981';
    if (type.includes('started')) return '#3b82f6';
    if (type.includes('deleted')) return '#6b7280';
    return '#6b7280';
  }

  /**
   * Get formatted message for event
   */
  getEventMessage(event) {
    const desc = event.data.description || event.data.task_id || '';
    const truncDesc = desc.length > 40 ? desc.substring(0, 37) + '...' : desc;

    switch (event.type) {
      case 'task.created':
        return `Task created: ${truncDesc}`;
      case 'task.started':
        return `Task started: ${truncDesc}`;
      case 'task.completed':
        return `Task completed: ${truncDesc}`;
      case 'task.failed':
        return `Task failed: ${truncDesc}`;
      case 'task.deleted':
        return `Task deleted: ${truncDesc}`;
      case 'workspace.progress':
        return 'Workspace progress updated';
      case 'agent.active':
        return `Agent ${event.data.agent} is now active`;
      case 'agent.idle':
        return `Agent ${event.data.agent} is now idle`;
      default:
        return event.type.replace('.', ' ').replace(/_/g, ' ');
    }
  }
}
