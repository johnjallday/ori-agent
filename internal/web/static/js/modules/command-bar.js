/**
 * Command Bar Module
 * Handles task dispatch input, agent selection, and command parsing
 */

class CommandBar {
  constructor() {
    this.input = null;
    this.agentSelect = null;
    this.dispatchBtn = null;
    this.currentWorkspaceId = null;
    this.commandHistory = [];
    this.historyIndex = -1;
  }

  /**
   * Initialize the command bar
   */
  init() {
    this.input = document.getElementById('commandInput');
    this.agentSelect = document.getElementById('commandAgentSelect');
    this.dispatchBtn = document.getElementById('commandDispatchBtn');

    if (!this.input || !this.agentSelect || !this.dispatchBtn) {
      console.warn('CommandBar: Required elements not found');
      return;
    }

    this.setupEventListeners();
    this.loadAgents();
    this.loadWorkspaceContext();

    // Global keyboard shortcut
    document.addEventListener('keydown', e => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
        e.preventDefault();
        this.focus();
      }
    });
  }

  /**
   * Set up event listeners
   */
  setupEventListeners() {
    // Input handling
    this.input.addEventListener('keydown', e => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        this.dispatch();
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        this.navigateHistory(-1);
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        this.navigateHistory(1);
      }
    });

    // Dispatch button
    this.dispatchBtn.addEventListener('click', () => this.dispatch());

    // Agent selection change
    this.agentSelect.addEventListener('change', () => {
      this.input.focus();
    });
  }

  /**
   * Focus the command input
   */
  focus() {
    this.input.focus();
    this.input.select();
  }

  /**
   * Load available agents into the selector
   */
  async loadAgents() {
    try {
      const response = await fetch('/api/agents');
      if (!response.ok) throw new Error('Failed to fetch agents');

      const data = await response.json();
      const agents = data.agents || [];

      // Clear existing options except auto-assign
      while (this.agentSelect.options.length > 1) {
        this.agentSelect.remove(1);
      }

      // Add agent options
      agents.forEach(agent => {
        const option = document.createElement('option');
        option.value = agent.name;
        option.textContent = agent.name;
        this.agentSelect.appendChild(option);
      });

      const sessionAgent = window.sessionManager?.getActiveSession?.()?.agent_name;
      // The retired name is still accepted so a not-yet-migrated record resolves
      // (Issue #350 FR57); the canonical one is preferred.
      const assistantAgent =
        agents.find(agent => agent.name === 'Ask Ori')?.name ||
        agents.find(agent => agent.name === 'Workspace Manager')?.name ||
        '';
      if (sessionAgent) {
        this.agentSelect.value = sessionAgent;
      } else if (assistantAgent) {
        this.agentSelect.value = assistantAgent;
      }
    } catch (error) {
      console.error('CommandBar: Failed to load agents', error);
    }
  }

  /**
   * Load current workspace context
   */
  async loadWorkspaceContext() {
    try {
      // Try to get current workspace from session storage or URL
      const urlParams = new URLSearchParams(window.location.search);
      this.currentWorkspaceId =
        urlParams.get('workspace') || sessionStorage.getItem('currentWorkspaceId');

      if (!this.currentWorkspaceId) {
        // Get first available workspace
        const response = await fetch('/api/workspaces');
        if (response.ok) {
          const data = await response.json();
          const workspaces = data.workspaces || data.folders || (Array.isArray(data) ? data : []);
          // Prefer a concrete workspace as the implicit command context; only
          // fall back to a group when nothing else exists.
          const preferred =
            workspaces.find(ws => String(ws.kind || '').toLowerCase() !== 'group') || workspaces[0];
          if (preferred) {
            this.currentWorkspaceId = preferred.id;
            sessionStorage.setItem('currentWorkspaceId', this.currentWorkspaceId);
          }
        }
      }
    } catch (error) {
      console.error('CommandBar: Failed to load workspace context', error);
    }
  }

  /**
   * Parse command for special directives
   * @param {string} text - Command text
   * @returns {Object} Parsed command info
   */
  parseCommand(text) {
    const result = {
      description: text,
      agent: this.agentSelect.value || null,
      schedule: null,
      priority: 3 // Default priority (1=highest, 5=lowest)
    };

    // Parse /to:agent directive
    const toMatch = text.match(/\/to:(\S+)/);
    if (toMatch) {
      result.agent = toMatch[1];
      result.description = text.replace(/\/to:\S+\s*/, '').trim();
    }

    // Parse /schedule directive
    const scheduleMatch = text.match(/\/schedule\s+(.+?)(?:\/|$)/);
    if (scheduleMatch) {
      result.schedule = scheduleMatch[1].trim();
      result.description = text.replace(/\/schedule\s+.+?(?:\/|$)/, '').trim();
    }

    // Parse /priority directive (1-5 or keywords)
    const priorityMatch = text.match(/\/priority:(\S+)/i);
    if (priorityMatch) {
      const priorityValue = priorityMatch[1].toLowerCase();
      // Map keywords to numbers
      const priorityMap = {
        urgent: 1,
        highest: 1,
        high: 2,
        normal: 3,
        low: 4,
        lowest: 5
      };
      result.priority = priorityMap[priorityValue] || parseInt(priorityValue, 10) || 3;
      result.description = text.replace(/\/priority:\S+\s*/, '').trim();
    }

    return result;
  }

  /**
   * Dispatch the current command as a task
   */
  async dispatch() {
    const text = this.input.value.trim();
    if (!text) return;

    // Parse the command
    const command = this.parseCommand(text);

    if (!command.description) {
      this.showFeedback('error', 'Please enter a task description');
      return;
    }

    // Save to history
    this.commandHistory.unshift(text);
    if (this.commandHistory.length > 50) {
      this.commandHistory.pop();
    }
    this.historyIndex = -1;

    // Show loading state
    this.showFeedback('loading', 'Dispatching task...');
    this.dispatchBtn.disabled = true;

    try {
      // Create the task
      const taskData = {
        description: command.description,
        to: command.agent || '',
        workspace_id: this.currentWorkspaceId,
        priority: command.priority
      };

      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(taskData)
      });

      if (!response.ok) {
        const error = await response.json();
        throw new Error(error.message || 'Failed to create task');
      }

      const task = await response.json();

      // Clear input
      this.input.value = '';

      // Show success
      this.showFeedback('success', 'Task dispatched!');

      // Auto-execute if no schedule
      if (!command.schedule) {
        this.executeTask(task.id);
      }

      // Notify task queue to refresh
      if (window.taskQueue) {
        window.taskQueue.loadTasks();
      }

      // Emit event for other components
      window.dispatchEvent(new CustomEvent('taskCreated', { detail: task }));
    } catch (error) {
      console.error('CommandBar: Failed to dispatch task', error);
      this.showFeedback('error', error.message);
    } finally {
      this.dispatchBtn.disabled = false;
    }
  }

  /**
   * Execute a task immediately
   * @param {string} taskId - Task ID to execute
   */
  async executeTask(taskId) {
    try {
      await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: taskId })
      });
    } catch (error) {
      console.error('CommandBar: Failed to execute task', error);
    }
  }

  /**
   * Navigate through command history
   * @param {number} direction - -1 for older, 1 for newer
   */
  navigateHistory(direction) {
    if (this.commandHistory.length === 0) return;

    this.historyIndex += direction;

    if (this.historyIndex < -1) {
      this.historyIndex = -1;
    } else if (this.historyIndex >= this.commandHistory.length) {
      this.historyIndex = this.commandHistory.length - 1;
    }

    if (this.historyIndex === -1) {
      this.input.value = '';
    } else {
      this.input.value = this.commandHistory[this.historyIndex];
    }
  }

  /**
   * Show feedback to user
   * @param {string} type - 'loading', 'success', 'error'
   * @param {string} message - Feedback message
   */
  showFeedback(type, message) {
    // Use toast if available
    if (window.showToast) {
      const toastType = type === 'loading' ? 'info' : type;
      window.showToast(message, toastType);
    }
  }
}

// Create global instance
window.commandBar = new CommandBar();

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  window.commandBar.init();
});
