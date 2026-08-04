/**
 * workspace-realtime.js
 *
 * Real-time workspace updates using Server-Sent Events (SSE)
 * Handles live event streaming, notifications, and UI updates
 */

class WorkspaceRealtime {
  constructor() {
    this.eventSources = new Map(); // workspaceId -> EventSource
    this.eventListeners = new Map(); // workspaceId -> Set of callbacks
    this.notificationSource = null;
    this.notificationCallbacks = new Set();
    this.reconnectAttempts = new Map();
    this.maxReconnectAttempts = 5;
    this.reconnectDelay = 2000;
  }

  /**
   * Subscribe to workspace events
   * @param {string} workspaceId - The workspace ID to monitor
   * @param {Function} callback - Callback function to handle events
   * @returns {Function} Unsubscribe function
   */
  subscribeToWorkspace(workspaceId, callback) {
    // Add callback to listeners
    if (!this.eventListeners.has(workspaceId)) {
      this.eventListeners.set(workspaceId, new Set());
    }
    this.eventListeners.get(workspaceId).add(callback);

    // Create EventSource if not exists
    if (!this.eventSources.has(workspaceId)) {
      this.connectToWorkspace(workspaceId);
    }

    // Return unsubscribe function
    return () => {
      const listeners = this.eventListeners.get(workspaceId);
      if (listeners) {
        listeners.delete(callback);

        // If no more listeners, close the connection
        if (listeners.size === 0) {
          this.disconnectFromWorkspace(workspaceId);
        }
      }
    };
  }

  /**
   * Connect to workspace event stream
   */
  connectToWorkspace(workspaceId) {
    const url = `/api/orchestration/workflow/stream?workspace_id=${encodeURIComponent(workspaceId)}`;
    const eventSource = new EventSource(url);

    eventSource.addEventListener('open', () => {
      this.reconnectAttempts.set(workspaceId, 0);
      this.notifyListeners(workspaceId, {
        type: 'connection.opened',
        workspaceId: workspaceId
      });
    });

    eventSource.addEventListener('error', e => {
      console.error(`❌ Error in workspace ${workspaceId} event stream:`, e);

      // Handle reconnection
      const attempts = this.reconnectAttempts.get(workspaceId) || 0;
      if (attempts < this.maxReconnectAttempts) {
        this.reconnectAttempts.set(workspaceId, attempts + 1);

        setTimeout(
          () => {
            if (
              this.eventListeners.has(workspaceId) &&
              this.eventListeners.get(workspaceId).size > 0
            ) {
              this.disconnectFromWorkspace(workspaceId);
              this.connectToWorkspace(workspaceId);
            }
          },
          this.reconnectDelay * Math.pow(2, attempts)
        ); // Exponential backoff
      } else {
        console.error(`❌ Max reconnection attempts reached for workspace ${workspaceId}`);
        this.notifyListeners(workspaceId, {
          type: 'connection.error',
          workspaceId: workspaceId,
          message: 'Failed to reconnect to workspace event stream'
        });
      }
    });

    // Listen for status updates
    eventSource.addEventListener('status', e => {
      try {
        const data = JSON.parse(e.data);
        this.notifyListeners(workspaceId, {
          type: 'workspace.status',
          workspaceId: workspaceId,
          data: data
        });
      } catch (err) {
        console.error('Error parsing status event:', err);
      }
    });

    // Listen for task events
    eventSource.addEventListener('task.created', e => {
      this.handleTaskEvent(workspaceId, 'task.created', e);
    });

    eventSource.addEventListener('task.assigned', e => {
      this.handleTaskEvent(workspaceId, 'task.assigned', e);
    });

    eventSource.addEventListener('task.started', e => {
      this.handleTaskEvent(workspaceId, 'task.started', e);
    });

    eventSource.addEventListener('task.thinking', e => {
      this.handleTaskEvent(workspaceId, 'task.thinking', e);
    });

    eventSource.addEventListener('task.tool_call', e => {
      this.handleTaskEvent(workspaceId, 'task.tool_call', e);
    });

    eventSource.addEventListener('task.tool_result', e => {
      this.handleTaskEvent(workspaceId, 'task.tool_result', e);
    });

    eventSource.addEventListener('task.completed', e => {
      this.handleTaskEvent(workspaceId, 'task.completed', e);
    });

    eventSource.addEventListener('task.failed', e => {
      this.handleTaskEvent(workspaceId, 'task.failed', e);
    });

    eventSource.addEventListener('task.blocked', e => {
      this.handleTaskEvent(workspaceId, 'task.blocked', e);
    });

    eventSource.addEventListener('task.resumed', e => {
      this.handleTaskEvent(workspaceId, 'task.resumed', e);
    });

    eventSource.addEventListener('task.deleted', e => {
      this.handleTaskEvent(workspaceId, 'task.deleted', e);
    });

    // Backlog lifecycle events (PRD workspace-backlog FR31, 47, 54) — Backlog
    // items are Task records, so these reuse handleTaskEvent's plumbing.
    eventSource.addEventListener('task.backlog.captured', e => {
      this.handleTaskEvent(workspaceId, 'task.backlog.captured', e);
    });

    eventSource.addEventListener('task.backlog.updated', e => {
      this.handleTaskEvent(workspaceId, 'task.backlog.updated', e);
    });

    eventSource.addEventListener('task.backlog.reordered', e => {
      this.handleTaskEvent(workspaceId, 'task.backlog.reordered', e);
    });

    eventSource.addEventListener('task.backlog.promoted', e => {
      this.handleTaskEvent(workspaceId, 'task.backlog.promoted', e);
    });

    // Listen for workspace events
    eventSource.addEventListener('workspace.updated', e => {
      this.handleWorkspaceEvent(workspaceId, 'workspace.updated', e);
    });

    eventSource.addEventListener('workspace.updated', e => {
      this.handleWorkspaceEvent(workspaceId, 'workspace.updated', e);
    });

    eventSource.addEventListener('workspace.completed', e => {
      this.handleWorkspaceEvent(workspaceId, 'workspace.completed', e);
      // Auto-disconnect when workspace completes
      setTimeout(() => this.disconnectFromWorkspace(workspaceId), 1000);
    });

    // Listen for workflow events
    eventSource.addEventListener('workflow.started', e => {
      this.handleWorkflowEvent(workspaceId, 'workflow.started', e);
    });

    eventSource.addEventListener('workflow.completed', e => {
      this.handleWorkflowEvent(workspaceId, 'workflow.completed', e);
    });

    eventSource.addEventListener('step.started', e => {
      this.handleWorkflowEvent(workspaceId, 'step.started', e);
    });

    eventSource.addEventListener('step.completed', e => {
      this.handleWorkflowEvent(workspaceId, 'step.completed', e);
    });

    // Store the EventSource
    this.eventSources.set(workspaceId, eventSource);
  }

  /**
   * Disconnect from workspace event stream
   */
  disconnectFromWorkspace(workspaceId) {
    const eventSource = this.eventSources.get(workspaceId);
    if (eventSource) {
      eventSource.close();
      this.eventSources.delete(workspaceId);
      this.reconnectAttempts.delete(workspaceId);
    }
  }

  /**
   * Handle task events
   */
  handleTaskEvent(workspaceId, eventType, e) {
    try {
      const data = JSON.parse(e.data);
      // A completed task may have bumped the executing agent's evolution
      // stage (PRD FR15/FR19) — check here so the toast fires from the Map
      // view's normal task flow, not only while the roster page is open.
      if (eventType === 'task.completed' && data && data.agent && window.StageUpToast) {
        window.StageUpToast.checkByFetch(data.agent);
      }
      this.notifyListeners(workspaceId, {
        type: eventType,
        workspaceId: workspaceId,
        data: data
      });
    } catch (err) {
      console.error(`Error parsing ${eventType} event:`, err);
    }
  }

  /**
   * Handle workspace events
   */
  handleWorkspaceEvent(workspaceId, eventType, e) {
    try {
      const data = JSON.parse(e.data);
      this.notifyListeners(workspaceId, {
        type: eventType,
        workspaceId: workspaceId,
        data: data
      });
    } catch (err) {
      console.error(`Error parsing ${eventType} event:`, err);
    }
  }

  /**
   * Handle workflow events
   */
  handleWorkflowEvent(workspaceId, eventType, e) {
    try {
      const data = JSON.parse(e.data);
      this.notifyListeners(workspaceId, {
        type: eventType,
        workspaceId: workspaceId,
        data: data
      });
    } catch (err) {
      console.error(`Error parsing ${eventType} event:`, err);
    }
  }

  /**
   * Notify all listeners for a workspace
   */
  notifyListeners(workspaceId, event) {
    const listeners = this.eventListeners.get(workspaceId);
    if (listeners) {
      listeners.forEach(callback => {
        try {
          callback(event);
        } catch (err) {
          console.error('Error in event listener:', err);
        }
      });
    }
  }

  /**
   * Subscribe to notifications for an agent
   * @param {string} agentName - The agent name
   * @param {Function} callback - Callback function to handle notifications
   * @returns {Function} Unsubscribe function
   */
  subscribeToNotifications(agentName, callback) {
    this.notificationCallbacks.add(callback);

    // Create notification stream if not exists
    if (!this.notificationSource) {
      this.connectToNotifications(agentName);
    }

    // Return unsubscribe function
    return () => {
      this.notificationCallbacks.delete(callback);

      // If no more listeners, close the connection
      if (this.notificationCallbacks.size === 0) {
        this.disconnectFromNotifications();
      }
    };
  }

  /**
   * Connect to notification stream
   */
  connectToNotifications(agentName) {
    const url = `/api/orchestration/notifications/stream?agent=${encodeURIComponent(agentName)}`;
    const eventSource = new EventSource(url);

    eventSource.addEventListener('open', () => {});

    eventSource.addEventListener('error', e => {
      console.error('❌ Error in notification stream:', e);
    });

    // Listen for initial unread notifications
    eventSource.addEventListener('initial', e => {
      try {
        const data = JSON.parse(e.data);
        this.notificationCallbacks.forEach(callback => {
          callback({
            type: 'notifications.initial',
            data: data
          });
        });
      } catch (err) {
        console.error('Error parsing initial notifications:', err);
      }
    });

    // Listen for new notifications
    eventSource.addEventListener('notification', e => {
      try {
        const notification = JSON.parse(e.data);
        this.notificationCallbacks.forEach(callback => {
          callback({
            type: 'notification.new',
            data: notification
          });
        });
      } catch (err) {
        console.error('Error parsing notification:', err);
      }
    });

    this.notificationSource = eventSource;
  }

  /**
   * Disconnect from notification stream
   */
  disconnectFromNotifications() {
    if (this.notificationSource) {
      this.notificationSource.close();
      this.notificationSource = null;
    }
  }

  /**
   * Disconnect all event sources
   */
  disconnectAll() {
    // Close all workspace connections
    this.eventSources.forEach((eventSource, workspaceId) => {
      this.disconnectFromWorkspace(workspaceId);
    });

    // Close notification connection
    this.disconnectFromNotifications();

    // Clear all listeners
    this.eventListeners.clear();
    this.notificationCallbacks.clear();
  }

  /**
   * Get connection status for a workspace
   */
  isConnected(workspaceId) {
    const eventSource = this.eventSources.get(workspaceId);
    return eventSource && eventSource.readyState === EventSource.OPEN;
  }

  /**
   * Get all active connections
   */
  getActiveConnections() {
    const connections = [];
    this.eventSources.forEach((eventSource, workspaceId) => {
      connections.push({
        workspaceId: workspaceId,
        state: eventSource.readyState,
        connected: eventSource.readyState === EventSource.OPEN
      });
    });
    return connections;
  }
}

// Create global instance
window.workspaceRealtime = new WorkspaceRealtime();

// Cleanup on page unload
window.addEventListener('beforeunload', () => {
  window.workspaceRealtime.disconnectAll();
});
