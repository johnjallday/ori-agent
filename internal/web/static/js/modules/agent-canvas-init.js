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
    console.log('📐 Canvas resized:', rect.width, 'x', rect.height);
    // Don't call draw() here - let caller decide when to redraw
  }

  /**
   * Initialize canvas with studio data
   */
  async init() {
    console.log('🚀 AgentCanvas.init() called');
    try {
      // First, resize canvas to set width/height properties
      this.resize();
      console.log('AgentCanvas.init() - canvas resized, width:', this.parent.width, 'height:', this.parent.height);

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
        const tasks = this.parent.studio.tasks.map(task => {
          // If task doesn't have position, set to null so it will be calculated in drawTaskFlows
          const normalized = {
            ...task,
            x: task.x ?? null,
            y: task.y ?? null,
            combiner_type: task.combiner_type,
            combinerType: task.combiner_type,
            combiner_node_id: task.combiner_node_id,
            combinerNodeID: task.combiner_node_id
          };
          if (normalized.to === 'unassigned' || !normalized.to) {
            normalized.status = 'pending';
            normalized.result = null;
            normalized.error = null;
            normalized.progress = 0;
            normalized.completed_at = null;
            normalized.started_at = null;
          }
          return normalized;
        });
        // Ensure tasks are placed in the current viewport if missing or off-screen
        if (this.parent.eventHandler && typeof this.parent.eventHandler.ensureTaskPosition === 'function') {
          tasks.forEach(t => this.parent.eventHandler.ensureTaskPosition(t));
        }
        // Preserve assigned_node_id hints from existing state (if any)
        tasks.forEach(t => {
          const existing = this.state.tasks.find(et => et.id === t.id);
          if (existing && existing.assigned_node_id) {
            t.assigned_node_id = existing.assigned_node_id;
          }
        });
        this.state.setTasks(tasks);
        // Seed agent lastResult based on any completed tasks with results
        if (this.parent.eventHandler && typeof this.parent.eventHandler.updateAgentResultsFromTasks === 'function') {
          this.parent.eventHandler.updateAgentResultsFromTasks(tasks);
        }
      }

      // Load attachments from studio
      if (this.parent.studio.attachments) {
        const attachments = this.parent.studio.attachments.map(att => ({
          ...att,
          file: att.file || att.file_meta,
          x: att.x ?? null,
          y: att.y ?? null
        }));

        // Ensure positions exist
        if (this.parent.eventHandler && typeof this.parent.eventHandler.ensureAttachmentPosition === 'function') {
          attachments.forEach(a => this.parent.eventHandler.ensureAttachmentPosition(a));
        }

        this.state.setAttachments(attachments);
      }

      // Load scheduler nodes from studio (canvas-based scheduled tasks only)
      if (this.parent.studio.scheduled_tasks) {
        // Filter to only include canvas-based scheduler nodes (those with canvas_node_id)
        const schedulerNodes = this.parent.studio.scheduled_tasks
          .filter(task => task.canvas_node_id && task.canvas_node_id !== '')
          .map(task => ({
            ...task,
            x: task.x ?? 300,  // Default position if not set
            y: task.y ?? 300
          }));

        console.log('Loaded scheduler nodes:', schedulerNodes.length);
        this.state.setSchedulerNodes(schedulerNodes);
      }

      // Initialize agent positions
      this.initializeAgents();

      // Load saved layout (positions only)
      this.parent.layout.loadLayout();

      // Sync combiner nodes with their tasks
      this.syncCombinerTasks();

      // Immediately reset zoom to fit all content (ignore saved zoom values)
      this.parent.layout.zoomToFitContent();

      // Detect and initialize chains
      this.parent.animation.updateChains();

      // Fetch initial progress data immediately before connecting to SSE
      await this.fetchInitialProgressData();

      // Connect to real-time events
      this.parent.eventHandler.connectEventStream();

      // Start animation loop
      this.parent.animation.startAnimation();

      // Update canvas info
      document.getElementById('canvas-info').textContent =
        `Studio: ${this.parent.studio.name || this.parent.studioId} | Agents: ${this.state.agents.length} | Tasks: ${this.state.tasks.length} | Attachments: ${this.state.attachments.length} | Schedulers: ${this.state.schedulerNodes.length}`;


    } catch (error) {
      console.error('Failed to initialize canvas:', error);
      document.getElementById('canvas-info').textContent = 'Error loading studio';
    }
  }

  /**
   * Initialize agent positions and stats from studio data
   */
  initializeAgents() {
    if (!this.parent.studio) return;

    const agentStats = this.parent.studio.agent_stats || {};

    // Use AgentInstances if available (new stable system)
    if (this.parent.studio.agent_instances && this.parent.studio.agent_instances.length > 0) {
      const agentCount = this.parent.studio.agent_instances.length;
      const centerY = this.parent.height * 0.6;
      const spacing = Math.min(150, (this.parent.width * 0.8) / Math.max(agentCount - 1, 1));
      const totalWidth = spacing * (agentCount - 1);
      const startX = (this.parent.width - totalWidth) / 2;

      const agents = this.parent.studio.agent_instances.map((instance, index) => {
        const stats = agentStats[instance.name] || {
          status: 'idle',
          current_tasks: [],
          queued_tasks: [],
          completed_tasks: 0,
          failed_tasks: 0,
          total_executions: 0
        };

        return {
          name: instance.name,
          id: instance.id,                    // Stable UUID
          nodeId: instance.node_id,            // Stable node ID (e.g., "default-node-1")
          instanceNumber: instance.instance_number,
          x: startX + (index * spacing),
          y: centerY,
          width: 120,
          height: 70,
          color: this.parent.helpers.getAgentColor(index),
          status: stats.status,
          currentTasks: stats.current_tasks || [],
          queuedTasks: stats.queued_tasks || [],
          completedTasks: stats.completed_tasks || 0,
          failedTasks: stats.failed_tasks || 0,
          totalExecutions: stats.total_executions || 0,
          tasks: [],
          pulsePhase: Math.random() * Math.PI * 2
        };
      });
      this.state.setAgents(agents);
      return;
    }

    // FALLBACK: Legacy system (agent names only, no stable instances)
    if (!this.parent.studio.agents || this.parent.studio.agents.length === 0) return;

    const agentCount = this.parent.studio.agents.length;
    const centerY = this.parent.height * 0.6;
    const spacing = Math.min(150, (this.parent.width * 0.8) / Math.max(agentCount - 1, 1));
    const totalWidth = spacing * (agentCount - 1);
    const startX = (this.parent.width - totalWidth) / 2;

    const instanceCounters = {};

    const agents = this.parent.studio.agents.map((agentName, index) => {
      if (!instanceCounters[agentName]) {
        instanceCounters[agentName] = 0;
      }
      instanceCounters[agentName]++;
      const instanceNumber = instanceCounters[agentName];
      const nodeId = `${agentName}-node-${instanceNumber}`;

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
        nodeId: nodeId,
        instanceNumber: instanceNumber,
        x: startX + (index * spacing),
        y: centerY,
        width: 120,
        height: 70,
        color: this.parent.helpers.getAgentColor(index),
        status: stats.status,
        currentTasks: stats.current_tasks || [],
        queuedTasks: stats.queued_tasks || [],
        completedTasks: stats.completed_tasks || 0,
        failedTasks: stats.failed_tasks || 0,
        totalExecutions: stats.total_executions || 0,
        tasks: [],
        pulsePhase: Math.random() * Math.PI * 2
      };
    });
    this.state.setAgents(agents);

    // Load tasks if available
    if (this.parent.studio.tasks) {
      // Preserve existing positions when updating tasks
      const existingPositions = {};
      this.state.tasks.forEach(t => {
        if (t.x !== null && t.y !== null) {
          existingPositions[t.id] = { x: t.x, y: t.y };
        }
      });

      const tasks = this.parent.studio.tasks.map(task => {
        const existing = existingPositions[task.id];
        return {
          id: task.id,
          from: task.from,
          to: task.to,
          description: task.description,
          status: task.status,
          progress: 0,
          x: existing ? existing.x : null,
          y: existing ? existing.y : null,
          combiner_type: task.combiner_type,
          combinerType: task.combiner_type,
          combiner_node_id: task.combiner_node_id,
          combinerNodeID: task.combiner_node_id
        };
      });
      this.state.setTasks(tasks);
    }
  }

  /**
   * Sync combiner nodes with their backend tasks
   * Links combiner nodes to their corresponding tasks by combiner_node_id
   */
  syncCombinerTasks() {
    if (!this.state.combinerNodes || this.state.combinerNodes.length === 0) {
      return;
    }

    if (!this.state.tasks || this.state.tasks.length === 0) {
      return;
    }

    // For each combiner node, find its corresponding task
    this.state.combinerNodes.forEach(combiner => {
      // Find task with matching combiner_node_id
      const task = this.state.tasks.find(t =>
        t.combiner_node_id === combiner.id || t.combinerNodeID === combiner.id
      );

      if (task) {
        combiner.taskId = task.id;
        console.log(`🔗 Linked combiner node ${combiner.id} to task ${task.id}`);
      } else {
        console.log(`⚠️ No task found for combiner node ${combiner.id}`);
      }
    });
  }

  /**
   * Fetch initial progress data immediately after initialization
   * This ensures the canvas is fully functional without waiting for SSE
   */
  async fetchInitialProgressData() {
    try {
      console.log('📊 Fetching initial progress data for studio:', this.parent.studioId);

      // Fetch fresh studio data with progress
      const response = await fetch(`/api/studios/${this.parent.studioId}`, {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' }
      });

      if (!response.ok) {
        console.warn('Failed to fetch initial progress data:', response.statusText);
        return;
      }

      const data = await response.json();
      console.log('📊 Initial progress data fetched:', data);

      if (this.parent.eventHandler &&
          typeof this.parent.eventHandler.processWorkspacePayload === 'function') {
        this.parent.eventHandler.processWorkspacePayload(
          { ...data, type: data.type || 'workspace.progress' },
          { setTasks: true, source: 'initial.fetch' }
        );
        console.log('✅ Initial progress data processed successfully');
        return;
      }

      // Fallback if event handler is unavailable
      if (data.workspace_progress) {
        this.parent.workspaceProgress = data.workspace_progress;
        console.log('✅ Updated workspace progress:', data.workspace_progress);
      }

      if (data.agent_stats && this.parent.eventHandler &&
          typeof this.parent.eventHandler.updateAgentStats === 'function') {
        this.parent.eventHandler.updateAgentStats(data.agent_stats);
        console.log('✅ Updated agent stats');
      }

      if (data.tasks) {
        // Preserve existing positions
        const existingPositions = {};
        this.state.tasks.forEach(t => {
          if (t.x !== null && t.y !== null) {
            existingPositions[t.id] = { x: t.x, y: t.y };
          }
        });

        const tasks = data.tasks.map(task => {
          const existing = existingPositions[task.id];
          const mapped = {
            ...task,
            x: existing ? existing.x : (task.x ?? null),
            y: existing ? existing.y : (task.y ?? null)
          };

          // Ensure task has a position
          if (this.parent.eventHandler &&
              typeof this.parent.eventHandler.ensureTaskPosition === 'function') {
            this.parent.eventHandler.ensureTaskPosition(mapped);
          }

          return mapped;
        });

        // Normalize unassigned tasks
        tasks.forEach(t => {
          if (t.to === 'unassigned' || !t.to) {
            t.status = 'pending';
            t.result = null;
            t.error = null;
            t.progress = 0;
            t.completed_at = null;
            t.started_at = null;
          }
        });

        this.state.setTasks(tasks);

        // Update agent results from tasks
        if (this.parent.eventHandler &&
            typeof this.parent.eventHandler.updateAgentResultsFromTasks === 'function') {
          this.parent.eventHandler.updateAgentResultsFromTasks(tasks);
        }

        console.log('✅ Updated tasks:', tasks.length);
      }

      // Redraw canvas with updated data
      this.parent.draw();
      console.log('✅ Initial progress data processed successfully');

    } catch (error) {
      console.error('❌ Failed to fetch initial progress data:', error);
    }
  }
}
