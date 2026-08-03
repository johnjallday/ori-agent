/**
 * Activity Feed Module
 * Real-time SSE-powered activity stream showing agent actions
 */

class ActivityFeed {
  constructor() {
    this.container = null;
    this.streamElement = null;
    this.statusElement = null;
    this.eventSource = null;
    this.events = [];
    this.maxEvents = 100;
    this.reconnectAttempts = 0;
    this.maxReconnectAttempts = 5;
    this.reconnectDelay = 1000;
    this.currentWorkspaceId = null;
  }

  /**
   * Initialize the activity feed
   */
  init() {
    this.container = document.getElementById('activityFeedPanel');
    this.streamElement = document.getElementById('activityStream');
    this.statusElement = document.getElementById('sseStatus');

    if (!this.container || !this.streamElement) {
      console.warn('ActivityFeed: Required elements not found');
      return;
    }

    this.setupEventListeners();
    this.loadWorkspaceContext().then(() => {
      if (this.currentWorkspaceId) {
        this.connect();
      } else {
        this.updateStatus('disconnected');
      }
    });
  }

  /**
   * Load current workspace context
   */
  async loadWorkspaceContext() {
    try {
      const urlParams = new URLSearchParams(window.location.search);
      this.currentWorkspaceId =
        urlParams.get('workspace') || sessionStorage.getItem('currentWorkspaceId');

      if (!this.currentWorkspaceId) {
        const response = await fetch('/api/workspaces');
        if (response.ok) {
          const data = await response.json();
          const workspaces = data.workspaces || data.folders || (Array.isArray(data) ? data : []);
          if (workspaces.length > 0) {
            this.currentWorkspaceId = workspaces[0].id;
            sessionStorage.setItem('currentWorkspaceId', this.currentWorkspaceId);
          }
        }
      }
    } catch (error) {
      console.error('ActivityFeed: Failed to load workspace context', error);
    }
  }

  /**
   * Set up event listeners
   */
  setupEventListeners() {
    // Clear button
    const clearBtn = document.getElementById('clearActivityBtn');
    if (clearBtn) {
      clearBtn.addEventListener('click', () => this.clear());
    }
  }

  /**
   * Connect to SSE stream
   */
  connect() {
    if (this.eventSource) {
      this.eventSource.close();
    }

    if (!this.currentWorkspaceId) {
      console.warn('ActivityFeed: No workspace ID, cannot connect');
      this.updateStatus('disconnected');
      return;
    }

    this.updateStatus('connecting');

    try {
      const url = `/api/orchestration/progress/stream?workspace_id=${encodeURIComponent(this.currentWorkspaceId)}`;
      this.eventSource = new EventSource(url);

      this.eventSource.onopen = () => {
        this.reconnectAttempts = 0;
        this.updateStatus('connected');
      };

      this.eventSource.onmessage = e => {
        try {
          const data = JSON.parse(e.data);
          this.handleEvent(data);
        } catch (error) {
          console.error('ActivityFeed: Failed to parse event', error);
        }
      };

      this.eventSource.onerror = error => {
        console.error('ActivityFeed: SSE error', error);
        this.updateStatus('disconnected');
        this.eventSource.close();
        this.scheduleReconnect();
      };

      // Handle specific event types
      [
        'task_started',
        'task_completed',
        'task_failed',
        'tool_call',
        'thinking',
        'progress'
      ].forEach(type => {
        this.eventSource.addEventListener(type, e => {
          try {
            const data = JSON.parse(e.data);
            data.type = type;
            this.handleEvent(data);
          } catch (error) {
            console.error(`ActivityFeed: Failed to parse ${type} event`, error);
          }
        });
      });
    } catch (error) {
      console.error('ActivityFeed: Failed to create EventSource', error);
      this.updateStatus('disconnected');
      this.scheduleReconnect();
    }
  }

  /**
   * Schedule reconnection attempt
   */
  scheduleReconnect() {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      console.warn('ActivityFeed: Max reconnect attempts reached');
      return;
    }

    this.reconnectAttempts++;
    const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1);

    setTimeout(() => this.connect(), delay);
  }

  /**
   * Handle incoming event
   * @param {Object} event - Event data
   */
  handleEvent(event) {
    if (!event) return;

    // Add timestamp if not present
    if (!event.timestamp) {
      event.timestamp = new Date().toISOString();
    }

    // Add to events array
    this.events.unshift(event);
    if (this.events.length > this.maxEvents) {
      this.events.pop();
    }

    // Render the new event
    this.addEvent(event);

    // Dispatch global event for other components
    window.dispatchEvent(new CustomEvent('sseEvent', { detail: event }));
  }

  /**
   * Add event to the feed display
   * @param {Object} event - Event data
   */
  addEvent(event) {
    if (!this.streamElement) return;

    // Remove empty state if present
    const emptyState = this.streamElement.querySelector('.activity-empty');
    if (emptyState) {
      emptyState.remove();
    }

    // Create event element
    const eventHtml = this.renderEvent(event);
    const tempDiv = document.createElement('div');
    tempDiv.innerHTML = eventHtml;
    const eventElement = tempDiv.firstElementChild;

    // Add to top of feed
    this.streamElement.insertBefore(eventElement, this.streamElement.firstChild);

    // Remove excess events from DOM
    while (this.streamElement.children.length > this.maxEvents) {
      this.streamElement.removeChild(this.streamElement.lastChild);
    }
  }

  /**
   * Render a single event
   * @param {Object} event - Event data
   * @returns {string} HTML string
   */
  renderEvent(event) {
    const icon = this.getEventIcon(event.type);
    const title = this.getEventTitle(event);
    const detail = this.getEventDetail(event);
    const time = this.formatTime(event.timestamp);

    return `
      <div class="activity-event" data-task-id="${event.task_id || ''}">
        <div class="activity-event-icon ${event.type || 'default'}">
          ${icon}
        </div>
        <div class="activity-event-content">
          <div class="activity-event-title">${this.escapeHtml(title)}</div>
          ${detail ? `<div class="activity-event-detail">${this.escapeHtml(detail)}</div>` : ''}
        </div>
        <div class="activity-event-time">${time}</div>
      </div>
    `;
  }

  /**
   * Get icon SVG for event type
   * @param {string} type - Event type
   * @returns {string} SVG icon HTML
   */
  getEventIcon(type) {
    const icons = {
      task_started:
        '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8,5.14V19.14L19,12.14L8,5.14Z"/></svg>',
      task_completed:
        '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/></svg>',
      task_failed:
        '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/></svg>',
      tool_call:
        '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M20.5,11H19V7C19,5.89 18.1,5 17,5H13V3.5A2.5,2.5 0 0,0 10.5,1A2.5,2.5 0 0,0 8,3.5V5H4C2.89,5 2,5.89 2,7V10.8H3.5C5,10.8 6.2,12 6.2,13.5C6.2,15 5,16.2 3.5,16.2H2V20C2,21.11 2.89,22 4,22H7.8V20.5C7.8,19 9,17.8 10.5,17.8C12,17.8 13.2,19 13.2,20.5V22H17C18.11,22 19,21.11 19,20V16H20.5A2.5,2.5 0 0,0 23,13.5A2.5,2.5 0 0,0 20.5,11Z"/></svg>',
      thinking:
        '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2M12,4A8,8 0 0,1 20,12A8,8 0 0,1 12,20A8,8 0 0,1 4,12A8,8 0 0,1 12,4M12,6A6,6 0 0,0 6,12A6,6 0 0,0 12,18A6,6 0 0,0 18,12A6,6 0 0,0 12,6M12,8A4,4 0 0,1 16,12A4,4 0 0,1 12,16A4,4 0 0,1 8,12A4,4 0 0,1 12,8Z"/></svg>',
      progress:
        '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M13,2.03V2.05L13,4.05C17.39,4.59 20.5,8.58 19.96,12.97C19.5,16.61 16.64,19.5 13,19.93V21.93C18.5,21.38 22.5,16.5 21.95,11C21.5,6.25 17.73,2.5 13,2.03M11,2.06C9.05,2.25 7.19,3 5.67,4.26L7.1,5.74C8.22,4.84 9.57,4.26 11,4.06V2.06M4.26,5.67C3,7.19 2.25,9.04 2.05,11H4.05C4.24,9.58 4.8,8.23 5.69,7.1L4.26,5.67M2.06,13C2.26,14.96 3.03,16.81 4.27,18.33L5.69,16.9C4.81,15.77 4.24,14.42 4.06,13H2.06M7.1,18.37L5.67,19.74C7.18,21 9.04,21.79 11,22V20C9.58,19.82 8.23,19.25 7.1,18.37M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/></svg>'
    };
    return (
      icons[type] ||
      '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2Z"/></svg>'
    );
  }

  /**
   * Get event title
   * @param {Object} event - Event data
   * @returns {string} Title
   */
  getEventTitle(event) {
    const titles = {
      task_started: 'Task Started',
      task_completed: 'Task Completed',
      task_failed: 'Task Failed',
      tool_call: 'Tool Call',
      thinking: 'Processing',
      progress: 'Progress Update'
    };

    let title = titles[event.type] || 'Event';

    if (event.agent_name) {
      title += ` - ${event.agent_name}`;
    }

    return title;
  }

  /**
   * Get event detail text
   * @param {Object} event - Event data
   * @returns {string} Detail text
   */
  getEventDetail(event) {
    if (event.message) return event.message;
    if (event.description) return event.description;
    if (event.tool_name) return `Using ${event.tool_name}`;
    if (event.result) return event.result.substring(0, 100);
    return '';
  }

  /**
   * Format timestamp
   * @param {string} timestamp - ISO timestamp
   * @returns {string} Formatted time
   */
  formatTime(timestamp) {
    if (!timestamp) return '';
    const date = new Date(timestamp);
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }

  /**
   * Update connection status display
   * @param {string} status - 'connecting', 'connected', 'disconnected'
   */
  updateStatus(status) {
    if (!this.statusElement) return;

    const statusText = this.statusElement.querySelector('.status-text');
    if (statusText) {
      const labels = {
        connecting: 'Connecting...',
        connected: 'Live',
        disconnected: 'Disconnected'
      };
      statusText.textContent = labels[status] || status;
    }

    this.statusElement.classList.remove('connecting', 'connected', 'disconnected');
    this.statusElement.classList.add(status);
  }

  /**
   * Clear the activity feed
   */
  clear() {
    this.events = [];
    if (this.streamElement) {
      this.streamElement.innerHTML = `
        <div class="activity-empty">
          <svg width="32" height="32" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2M12,4A8,8 0 0,1 20,12A8,8 0 0,1 12,20A8,8 0 0,1 4,12A8,8 0 0,1 12,4M12,6A6,6 0 0,0 6,12A6,6 0 0,0 12,18A6,6 0 0,0 18,12A6,6 0 0,0 12,6Z"/>
          </svg>
          <p>Waiting for agent activity...</p>
        </div>
      `;
    }
  }

  /**
   * Disconnect from SSE stream
   */
  disconnect() {
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
    this.updateStatus('disconnected');
  }

  /**
   * Link event to task (make clickable)
   * @param {Object} event - Event data
   */
  linkToTask(event) {
    if (event.task_id && window.taskQueue) {
      window.taskQueue.expandTask(event.task_id);
    }
  }

  /**
   * Escape HTML
   * @param {string} str - String to escape
   * @returns {string} Escaped string
   */
  escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
  }
}

// Create global instance
window.activityFeed = new ActivityFeed();

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  window.activityFeed.init();
});

// Clean up on page unload
window.addEventListener('beforeunload', () => {
  if (window.activityFeed) {
    window.activityFeed.disconnect();
  }
});
