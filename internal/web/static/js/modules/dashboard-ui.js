/**
 * Dashboard UI Module
 * Handles notifications, toasts, status badges, and UI utilities
 */

export class DashboardUI {
  constructor(parent) {
    this.parent = parent;
  }

  showConnectionStatus(status, message) {
    const statusBadge = document.getElementById('connection-status');
    if (statusBadge) {
      if (status === 'connected') {
        statusBadge.innerHTML = '<span class="status-indicator status-online"></span> Live';
        statusBadge.className = 'modern-badge badge-success';
      } else if (status === 'error') {
        statusBadge.innerHTML = '<span class="status-indicator status-offline"></span> Disconnected';
        statusBadge.className = 'modern-badge badge-danger';
        if (message) {
          this.showToast('Connection Error', message, 'error');
        }
      }
    }
  }

  showTaskNotification(event) {
    let message = '';
    let type = 'info';

    switch (event.type) {
      case 'task.started':
        message = `Task started: ${event.data?.data?.description || 'Unknown task'}`;
        type = 'info';
        break;
      case 'task.completed':
        message = `Task completed: ${event.data?.data?.description || 'Unknown task'}`;
        type = 'success';
        break;
      case 'task.failed':
        message = `Task failed: ${event.data?.data?.description || 'Unknown task'}`;
        type = 'error';
        break;
    }

    if (message) {
      this.showToast('Task Update', message, type);
    }
  }

  showToast(title, message, type = 'info') {
    // Simple toast implementation
    const toast = document.createElement('div');
    toast.className = `toast-notification toast-${type}`;
    toast.style.cssText = `
      position: fixed;
      top: 20px;
      right: 20px;
      background: var(--surface-color);
      border-left: 4px solid var(--${type === 'error' ? 'danger' : type === 'success' ? 'success' : 'info'}-color);
      padding: 1rem;
      border-radius: var(--radius-md);
      box-shadow: var(--shadow-lg);
      z-index: 9999;
      min-width: 300px;
      animation: slideIn 0.3s ease-out;
    `;

    toast.innerHTML = `
      <div style="color: var(--text-primary); font-weight: 600; margin-bottom: 0.25rem;">${this.escapeHtml(title)}</div>
      <div style="color: var(--text-secondary); font-size: 0.9rem;">${this.escapeHtml(message)}</div>
    `;

    document.body.appendChild(toast);

    setTimeout(() => {
      toast.style.animation = 'slideOut 0.3s ease-in';
      setTimeout(() => toast.remove(), 300);
    }, 4000);
  }

  getStatusBadgeClass(status) {
    switch (status) {
      case 'active':
      case 'in_progress':
        return 'badge-info';
      case 'completed':
        return 'badge-success';
      case 'failed':
        return 'badge-danger';
      case 'pending':
        return 'badge-secondary';
      default:
        return 'badge-secondary';
    }
  }

  getStatusIcon(status) {
    const iconColor = status === 'completed' ? 'var(--success-color)' :
                     status === 'failed' ? 'var(--danger-color)' :
                     status === 'in_progress' ? 'var(--info-color)' :
                     'var(--text-muted)';

    let path = '';
    switch (status) {
      case 'completed':
        path = 'M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z';
        break;
      case 'failed':
        path = 'M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z';
        break;
      case 'in_progress':
        path = 'M12,4V2A10,10 0 0,0 2,12H4A8,8 0 0,1 12,4Z';
        break;
      default:
        path = 'M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2M12,20C7.59,20 4,16.41 4,12C4,7.59 7.59,4 12,4C16.41,4 20,7.59 20,12C20,16.41 16.41,20 12,20Z';
    }

    return `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="${iconColor}">
        <path d="${path}"/>
      </svg>
    `;
  }

  switchTab(tab) {
    this.activeTab = tab;
    this.render();
  }

  startPolling() {
    this.refreshInterval = setInterval(() => {
      this.refresh();
    }, 5000); // Poll every 5 seconds
  }

  stopPolling() {
    if (this.refreshInterval) {
      clearInterval(this.refreshInterval);
      this.refreshInterval = null;
    }
  }

  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

}
