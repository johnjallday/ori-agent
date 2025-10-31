// Workspace monitoring module for multi-agent orchestration
export class WorkspaceManager {
    constructor() {
        this.currentWorkspaceId = null;
        this.updateInterval = null;
        this.workspaces = new Map();
        this.eventSource = null;
    }

    /**
     * Fetches all workspaces from the server
     */
    async fetchWorkspaces() {
        try {
            const response = await fetch('/api/orchestration/workspace');
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            const data = await response.json();

            // Extract workspaces array from response object
            const workspaces = data.workspaces || data;

            // Update local cache
            if (Array.isArray(workspaces)) {
                workspaces.forEach(ws => {
                    this.workspaces.set(ws.id, ws);
                });
            }

            return workspaces;
        } catch (error) {
            console.error('Failed to fetch workspaces:', error);
            return [];
        }
    }

    /**
     * Fetches workflow status for a specific workspace
     */
    async fetchWorkflowStatus(workspaceId) {
        try {
            const response = await fetch(`/api/orchestration/workflow/status?workspace_id=${workspaceId}`);
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            return await response.json();
        } catch (error) {
            console.error('Failed to fetch workflow status:', error);
            return null;
        }
    }

    /**
     * Fetches tasks for a specific workspace
     */
    async fetchTasks(workspaceId) {
        try {
            const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            return await response.json();
        } catch (error) {
            console.error('Failed to fetch tasks:', error);
            return [];
        }
    }

    /**
     * Starts monitoring a workspace with periodic updates
     */
    startMonitoring(workspaceId, callback, intervalMs = 2000) {
        this.currentWorkspaceId = workspaceId;
        this.stopMonitoring(); // Stop any existing monitoring

        const update = async () => {
            const status = await this.fetchWorkflowStatus(workspaceId);
            if (status && callback) {
                callback(status);
            }
        };

        // Initial update
        update();

        // Set up periodic updates
        this.updateInterval = setInterval(update, intervalMs);
    }

    /**
     * Stops monitoring the current workspace
     */
    stopMonitoring() {
        if (this.updateInterval) {
            clearInterval(this.updateInterval);
            this.updateInterval = null;
        }
        if (this.eventSource) {
            this.eventSource.close();
            this.eventSource = null;
        }
    }

    /**
     * Starts monitoring with real-time SSE updates
     */
    startMonitoringSSE(workspaceId, callback) {
        this.currentWorkspaceId = workspaceId;
        this.stopMonitoring(); // Stop any existing monitoring

        const url = `/api/orchestration/workflow/stream?workspace_id=${workspaceId}`;
        this.eventSource = new EventSource(url);

        this.eventSource.onmessage = (event) => {
            try {
                const status = JSON.parse(event.data);
                if (callback) {
                    callback(status);
                }
            } catch (error) {
                console.error('Failed to parse SSE data:', error);
            }
        };

        this.eventSource.addEventListener('complete', (event) => {
            console.log('Workflow completed:', event.data);
            this.stopMonitoring();
            if (callback) {
                // Fetch final status
                this.fetchWorkflowStatus(workspaceId).then(status => {
                    if (status) {
                        callback(status);
                    }
                });
            }
        });

        this.eventSource.addEventListener('error', (event) => {
            console.error('SSE error:', event);
            if (callback) {
                callback({ error: 'Connection lost' });
            }
        });

        this.eventSource.onerror = (error) => {
            console.error('SSE connection error:', error);
            this.stopMonitoring();
            // Fall back to polling
            this.startMonitoring(workspaceId, callback, 3000);
        };
    }

    /**
     * Renders workspace list to a container element
     */
    renderWorkspaceList(containerEl, workspaces) {
        if (!containerEl) return;

        containerEl.innerHTML = '';

        if (!workspaces || workspaces.length === 0) {
            containerEl.innerHTML = '<p class="text-muted">No active workspaces</p>';
            return;
        }

        const listEl = document.createElement('div');
        listEl.className = 'workspace-list';

        workspaces.forEach(ws => {
            const itemEl = document.createElement('div');
            itemEl.className = 'workspace-item';
            itemEl.innerHTML = `
                <div class="workspace-header">
                    <span class="workspace-name">${this.escapeHtml(ws.name)}</span>
                    <span class="workspace-status badge bg-${this.getStatusColor(ws.status)}">${ws.status}</span>
                </div>
                <div class="workspace-meta">
                    <small class="text-muted">
                        ${ws.agents?.length || 0} agents |
                        Created: ${new Date(ws.created_at).toLocaleString()}
                    </small>
                </div>
            `;

            itemEl.addEventListener('click', () => this.selectWorkspace(ws.id));
            listEl.appendChild(itemEl);
        });

        containerEl.appendChild(listEl);
    }

    /**
     * Renders workflow status with progress information
     */
    renderWorkflowStatus(containerEl, status) {
        if (!containerEl || !status) return;

        const progress = Math.round(status.progress * 100);
        const phaseEmoji = this.getPhaseEmoji(status.phase);

        containerEl.innerHTML = `
            <div class="workflow-status">
                <div class="status-header">
                    <h6>${phaseEmoji} ${status.phase}</h6>
                    <span class="progress-text">${progress}%</span>
                </div>

                <div class="progress mb-3">
                    <div class="progress-bar ${this.getPhaseColor(status.phase)}"
                         role="progressbar"
                         style="width: ${progress}%"
                         aria-valuenow="${progress}"
                         aria-valuemin="0"
                         aria-valuemax="100">
                    </div>
                </div>

                <div class="task-summary">
                    <h6>Tasks</h6>
                    <div class="task-list">
                        ${this.renderTaskList(status.tasks)}
                    </div>
                </div>

                <div class="workflow-meta mt-3">
                    <small class="text-muted">
                        Started: ${new Date(status.start_time).toLocaleString()}<br>
                        Last updated: ${new Date(status.updated_at).toLocaleString()}
                    </small>
                </div>
            </div>
        `;
    }

    /**
     * Renders task list
     */
    renderTaskList(tasks) {
        if (!tasks || Object.keys(tasks).length === 0) {
            return '<p class="text-muted">No tasks</p>';
        }

        return Object.entries(tasks).map(([taskId, task]) => `
            <div class="task-item">
                <div class="d-flex justify-content-between align-items-center">
                    <span class="task-agent">${this.escapeHtml(task.agent)}</span>
                    <span class="task-status badge bg-${this.getTaskStatusColor(task.status)}">${task.status}</span>
                </div>
                <small class="task-desc text-muted">${this.escapeHtml(task.description)}</small>
            </div>
        `).join('');
    }

    /**
     * Helper: Get status badge color
     */
    getStatusColor(status) {
        const colors = {
            'active': 'primary',
            'completed': 'success',
            'failed': 'danger',
            'cancelled': 'secondary'
        };
        return colors[status] || 'secondary';
    }

    /**
     * Helper: Get task status color
     */
    getTaskStatusColor(status) {
        const colors = {
            'pending': 'secondary',
            'assigned': 'info',
            'in_progress': 'warning',
            'completed': 'success',
            'failed': 'danger',
            'cancelled': 'secondary',
            'timeout': 'danger'
        };
        return colors[status] || 'secondary';
    }

    /**
     * Helper: Get phase emoji
     */
    getPhaseEmoji(phase) {
        const emojis = {
            'initializing': '🔄',
            'executing': '⚡',
            'finalizing': '🏁',
            'completed': '✅'
        };
        return emojis[phase] || '📊';
    }

    /**
     * Helper: Get phase color class
     */
    getPhaseColor(phase) {
        const colors = {
            'initializing': 'bg-info',
            'executing': 'bg-warning',
            'finalizing': 'bg-primary',
            'completed': 'bg-success'
        };
        return colors[phase] || 'bg-secondary';
    }

    /**
     * Helper: Escape HTML to prevent XSS
     */
    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    /**
     * Selects a workspace for monitoring
     */
    selectWorkspace(workspaceId) {
        const event = new CustomEvent('workspace-selected', {
            detail: { workspaceId }
        });
        window.dispatchEvent(event);
    }
}

// Create singleton instance
export const workspaceManager = new WorkspaceManager();
