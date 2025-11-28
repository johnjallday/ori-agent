/**
 * AgentCanvasInitialization - Initialization and setup module
 * Handles canvas setup, data loading, and agent positioning
 */
import { apiGet } from './agent-canvas-api.js';

export class AgentCanvasInitialization {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Resize canvas to match container
   */
  resize() {
    const rect = this.parent.canvas.getBoundingClientRect();
    this.parent.canvas.width = rect.width * window.devicePixelRatio;
    this.parent.canvas.height = rect.height * window.devicePixelRatio;
    this.parent.ctx.scale(window.devicePixelRatio, window.devicePixelRatio);
    this.parent.width = rect.width;
    this.parent.height = rect.height;
    this.parent.draw();
  }

  /**
   * Initialize canvas with studio data
   */
  async init() {
    try {
      console.log('AgentCanvas.init() - studioId:', this.parent.studioId);

      // Load studio data
      this.parent.studio = await apiGet(`/api/studios/${this.parent.studioId}`);

      console.log('AgentCanvas.init() - studio data loaded:', this.parent.studio);

      // Load workspace progress
      this.parent.workspaceProgress = this.parent.studio.workspace_progress || {
        total_tasks: 0,
        completed_tasks: 0,
        in_progress_tasks: 0,
        pending_tasks: 0,
        failed_tasks: 0,
        percentage: 0,
        active_agents: 0,
        idle_agents: 0,
        total_agents: 0
      };

      // Load mission from shared data if it exists
      if (this.parent.studio.shared_data && this.parent.studio.shared_data.mission) {
        this.parent.mission = this.parent.studio.shared_data.mission;
      }

      // Load tasks from studio
      if (this.parent.studio.tasks) {
        this.parent.tasks = this.parent.studio.tasks.map(task => {
          // If task doesn't have position, set to null so it will be calculated in drawTaskFlows
          return {
            ...task,
            x: task.x ?? null,
            y: task.y ?? null
          };
        });
      }

      // Initialize agent positions
      this.initializeAgents();

      // Load saved layout (positions and zoom)
      // TEMPORARILY DISABLED to fix zoom issue - will re-enable after testing
      // this.parent.layout.loadLayout();

      // Detect and initialize chains
      this.parent.animation.updateChains();

      // Connect to real-time events
      this.parent.eventHandler.connectEventStream();

      // Start animation loop
      this.parent.animation.startAnimation();

      // Update canvas info
      document.getElementById('canvas-info').textContent =
        `Studio: ${this.parent.studio.name || this.parent.studioId} | Agents: ${this.parent.agents.length}`;

      // Initialize metrics
      this.parent.metrics.updateMetrics();

      // Always reset view to fit content (overrides any bad saved layout)
      setTimeout(() => {
        this.parent.layout.zoomToFit();
      }, 100);

    } catch (error) {
      console.error('Failed to initialize canvas:', error);
      document.getElementById('canvas-info').textContent = 'Error loading studio';
    }
  }

  /**
   * Initialize agent positions and stats from studio data
   */
  initializeAgents() {
    if (!this.parent.studio || !this.parent.studio.agents) return;

    const agentCount = this.parent.studio.agents.length;
    const centerY = this.parent.height * 0.6; // Position lower to avoid mission box
    const spacing = Math.min(150, (this.parent.width * 0.8) / Math.max(agentCount - 1, 1));
    const totalWidth = spacing * (agentCount - 1);
    const startX = (this.parent.width - totalWidth) / 2;

    // Get agent stats from studio data
    const agentStats = this.parent.studio.agent_stats || {};

    this.parent.agents = this.parent.studio.agents.map((agentName, index) => {
      // Get stats for this agent
      const stats = agentStats[agentName] || {
        status: 'idle',
        current_tasks: [],
        queued_tasks: [],
        completed_tasks: 0,
        failed_tasks: 0,
        total_executions: 0
      };

      return {
        name: agentName,
        x: startX + (index * spacing),
        y: centerY,
        width: 120,
        height: 70,
        color: this.parent.helpers.getAgentColor(index),
        status: stats.status, // Use status from backend
        currentTasks: stats.current_tasks || [],
        queuedTasks: stats.queued_tasks || [],
        completedTasks: stats.completed_tasks || 0,
        failedTasks: stats.failed_tasks || 0,
        totalExecutions: stats.total_executions || 0,
        tasks: [], // Legacy field, keep for compatibility
        pulsePhase: Math.random() * Math.PI * 2
      };
    });

    // Load tasks if available
    if (this.parent.studio.tasks) {
      // Preserve existing positions when updating tasks
      const existingPositions = {};
      this.parent.tasks.forEach(t => {
        if (t.x !== null && t.y !== null) {
          existingPositions[t.id] = { x: t.x, y: t.y };
        }
      });

      this.parent.tasks = this.parent.studio.tasks.map(task => {
        const existing = existingPositions[task.id];
        return {
          id: task.id,
          from: task.from,
          to: task.to,
          description: task.description,
          status: task.status,
          progress: 0,
          x: existing ? existing.x : null,
          y: existing ? existing.y : null
        };
      });
    }
  }
}
