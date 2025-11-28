import { AgentCanvasForms } from './agent-canvas-forms.js';
import { apiGet, apiPost, apiPut, apiDelete } from './agent-canvas-api.js';
import { connectProgressStream } from './agent-canvas-events.js';
import {
  createCombinerTask as combinerCreateTask,
  ensureCombinerTask as combinerEnsureTask,
  executeCombiner as combinerExecute
} from './agent-canvas-combiners.js';
import { executeTask as tasksExecuteTask, rerunTask as tasksRerunTask, assignTaskToCombiner as tasksAssignToCombiner } from './agent-canvas-tasks.js';
import { AgentCanvasState, EVENT_TYPES } from './agent-canvas-state.js';
import { AgentCanvasRenderer } from './agent-canvas-renderer.js';
import { AgentCanvasInteractionHandler } from './agent-canvas-interactions.js';
import { AgentCanvasLayoutManager } from './agent-canvas-layout.js';
import { AgentCanvasAnimationController } from './agent-canvas-animation.js';
import { AgentCanvasTimelineManager } from './agent-canvas-timeline.js';

/**
 * AgentCanvas - Visual canvas for real-time agent collaboration
 * Displays agents as nodes with tasks flowing between them
 */
class AgentCanvas {
  constructor(canvasId, studioId) {
    // Initialize state module (centralized state management)
    this.state = new AgentCanvasState();

    // Set canvas and context in state
    const canvas = document.getElementById(canvasId);
    const ctx = canvas.getContext('2d');
    this.state.setCanvas(canvas, ctx);
    this.state.setStudioId(studioId);

    // Keep canvas and ctx as direct properties for backward compatibility
    this.canvas = canvas;
    this.ctx = ctx;
    this.studioId = studioId;

    // Initialize forms module
    this.state.forms = new AgentCanvasForms(this);
    this.forms = this.state.forms;

    // Initialize renderer module
    this.renderer = new AgentCanvasRenderer(ctx, this.state, canvas, this);

    // Initialize interaction handler module
    this.interactions = new AgentCanvasInteractionHandler(canvas, this.state, this);

    // Initialize layout manager
    this.layout = new AgentCanvasLayoutManager(this.state, this);

    // Initialize animation controller
    this.animation = new AgentCanvasAnimationController(this.state, this);

    // Initialize timeline manager
    this.timeline = new AgentCanvasTimelineManager(this.state, this);

    // Initialize layout manager module
    this.layout = new AgentCanvasLayoutManager(this.state, this);

    // Mouse interactions - delegate to interaction handler
    this.canvas.addEventListener('mousedown', (e) => this.interactions.onMouseDown(e));
    this.canvas.addEventListener('mousemove', (e) => this.interactions.onMouseMove(e));
    this.canvas.addEventListener('mouseup', (e) => this.interactions.onMouseUp(e));
    this.canvas.addEventListener('mouseleave', (e) => this.interactions.onMouseUp(e));
    this.canvas.addEventListener('wheel', (e) => this.interactions.onWheel(e));
    this.canvas.addEventListener('click', (e) => this.interactions.onClick(e));
    this.canvas.addEventListener('contextmenu', (e) => this.interactions.onContextMenu(e));

    // Keyboard interactions - delegate to interaction handler
    window.addEventListener('keydown', (e) => this.interactions.onKeyDown(e));
    window.addEventListener('keyup', (e) => this.interactions.onKeyUp(e));

    // Subscribe to state changes that require redraw
    this.state.on(EVENT_TYPES.AGENT_MOVED, () => this.draw());
    this.state.on(EVENT_TYPES.TASK_MOVED, () => this.draw());
    this.state.on(EVENT_TYPES.CANVAS_PANNED, () => this.draw());
    this.state.on(EVENT_TYPES.CANVAS_ZOOMED, () => this.draw());
  }

  // ==================== PROPERTY ACCESSORS (Backward Compatibility) ====================
  // These getters/setters delegate to the state module for backward compatibility

  // Studio and data
  get studio() { return this.state.studio; }
  set studio(value) { this.state.setStudio(value); }

  get agents() { return this.state.agents; }
  set agents(value) { this.state.setAgents(value); }

  get tasks() { return this.state.tasks; }
  set tasks(value) { this.state.setTasks(value); }

  get messages() { return this.state.messages; }
  set messages(value) { this.state.messages = value; }

  get mission() { return this.state.mission; }
  set mission(value) { this.state.mission = value; }

  get eventSource() { return this.state.eventSource; }
  set eventSource(value) { this.state.eventSource = value; }

  // Transform state
  get offsetX() { return this.state.offsetX; }
  set offsetX(value) { this.state.offsetX = value; }

  get offsetY() { return this.state.offsetY; }
  set offsetY(value) { this.state.offsetY = value; }

  get scale() { return this.state.scale; }
  set scale(value) { this.state.setScale(value); }

  // Drag states
  get isDragging() { return this.state.isDragging; }
  set isDragging(value) { this.state.isDragging = value; }

  get isDraggingAgent() { return this.state.isDraggingAgent; }
  set isDraggingAgent(value) { this.state.isDraggingAgent = value; }

  get draggedAgent() { return this.state.draggedAgent; }
  set draggedAgent(value) { this.state.draggedAgent = value; }

  get isDraggingTask() { return this.state.isDraggingTask; }
  set isDraggingTask(value) { this.state.isDraggingTask = value; }

  get draggedTask() { return this.state.draggedTask; }
  set draggedTask(value) { this.state.draggedTask = value; }

  get isDraggingConnection() { return this.state.isDraggingConnection; }
  set isDraggingConnection(value) { this.state.isDraggingConnection = value; }

  get draggedConnection() { return this.state.draggedConnection; }
  set draggedConnection(value) { this.state.draggedConnection = value; }

  get connectionDragStart() { return this.state.connectionDragStart; }
  set connectionDragStart(value) { this.state.connectionDragStart = value; }

  get isDraggingCombiner() { return this.state.isDraggingCombiner; }
  set isDraggingCombiner(value) { this.state.isDraggingCombiner = value; }

  get draggedCombiner() { return this.state.draggedCombiner; }
  set draggedCombiner(value) { this.state.draggedCombiner = value; }

  get dragStartX() { return this.state.dragStartX; }
  set dragStartX(value) { this.state.dragStartX = value; }

  get dragStartY() { return this.state.dragStartY; }
  set dragStartY(value) { this.state.dragStartY = value; }

  // Keyboard state
  get spacePressed() { return this.state.spacePressed; }
  set spacePressed(value) { this.state.spacePressed = value; }

  get ctrlPressed() { return this.state.ctrlPressed; }
  set ctrlPressed(value) { this.state.ctrlPressed = value; }

  // Context menu
  get contextMenuVisible() { return this.state.contextMenuVisible; }
  set contextMenuVisible(value) { this.state.contextMenuVisible = value; }

  get contextMenuAgent() { return this.state.contextMenuAgent; }
  set contextMenuAgent(value) { this.state.contextMenuAgent = value; }

  get contextMenuX() { return this.state.contextMenuX; }
  set contextMenuX(value) { this.state.contextMenuX = value; }

  get contextMenuY() { return this.state.contextMenuY; }
  set contextMenuY(value) { this.state.contextMenuY = value; }

  // Help overlay
  get helpOverlayVisible() { return this.state.helpOverlayVisible; }
  set helpOverlayVisible(value) { this.state.helpOverlayVisible = value; }

  // Animation
  get animationFrame() { return this.state.animationFrame; }
  set animationFrame(value) { this.state.animationFrame = value; }

  get animationPaused() { return this.state.animationPaused; }
  set animationPaused(value) { this.state.animationPaused = value; }

  get particles() { return this.state.particles; }
  set particles(value) { this.state.particles = value; }

  // Appearance
  get backgroundColor() { return this.state.backgroundColor; }
  set backgroundColor(value) { this.state.backgroundColor = value; }

  // Expanded panels
  get expandedTask() { return this.state.expandedTask; }
  set expandedTask(value) { this.state.expandedTask = value; }

  get expandedPanelWidth() { return this.state.expandedPanelWidth; }
  set expandedPanelWidth(value) { this.state.expandedPanelWidth = value; }

  get expandedPanelTargetWidth() { return this.state.expandedPanelTargetWidth; }
  set expandedPanelTargetWidth(value) { this.state.expandedPanelTargetWidth = value; }

  get expandedPanelAnimating() { return this.state.expandedPanelAnimating; }
  set expandedPanelAnimating(value) { this.state.expandedPanelAnimating = value; }

  get resultScrollOffset() { return this.state.resultScrollOffset; }
  set resultScrollOffset(value) { this.state.resultScrollOffset = value; }

  get resultBoxBounds() { return this.state.resultBoxBounds; }
  set resultBoxBounds(value) { this.state.resultBoxBounds = value; }

  get copyButtonBounds() { return this.state.copyButtonBounds; }
  set copyButtonBounds(value) { this.state.copyButtonBounds = value; }

  get copyButtonState() { return this.state.copyButtonState; }
  set copyButtonState(value) { this.state.copyButtonState = value; }

  get expandedAgent() { return this.state.expandedAgent; }
  set expandedAgent(value) { this.state.expandedAgent = value; }

  get expandedAgentPanelWidth() { return this.state.expandedAgentPanelWidth; }
  set expandedAgentPanelWidth(value) { this.state.expandedAgentPanelWidth = value; }

  get expandedAgentPanelTargetWidth() { return this.state.expandedAgentPanelTargetWidth; }
  set expandedAgentPanelTargetWidth(value) { this.state.expandedAgentPanelTargetWidth = value; }

  get expandedAgentPanelAnimating() { return this.state.expandedAgentPanelAnimating; }
  set expandedAgentPanelAnimating(value) { this.state.expandedAgentPanelAnimating = value; }

  get agentPanelScrollOffset() { return this.state.agentPanelScrollOffset; }
  set agentPanelScrollOffset(value) { this.state.agentPanelScrollOffset = value; }

  get agentPanelMaxScroll() { return this.state.agentPanelMaxScroll; }
  set agentPanelMaxScroll(value) { this.state.agentPanelMaxScroll = value; }

  get expandedCombiner() { return this.state.expandedCombiner; }
  set expandedCombiner(value) { this.state.expandedCombiner = value; }

  get expandedCombinerPanelWidth() { return this.state.expandedCombinerPanelWidth; }
  set expandedCombinerPanelWidth(value) { this.state.expandedCombinerPanelWidth = value; }

  get expandedCombinerPanelTargetWidth() { return this.state.expandedCombinerPanelTargetWidth; }
  set expandedCombinerPanelTargetWidth(value) { this.state.expandedCombinerPanelTargetWidth = value; }

  get expandedCombinerPanelAnimating() { return this.state.expandedCombinerPanelAnimating; }
  set expandedCombinerPanelAnimating(value) { this.state.expandedCombinerPanelAnimating = value; }

  // Modes
  get connectionMode() { return this.state.connectionMode; }
  set connectionMode(value) { this.state.connectionMode = value; }

  get connectionSourceTask() { return this.state.connectionSourceTask; }
  set connectionSourceTask(value) { this.state.connectionSourceTask = value; }

  get highlightedAgent() { return this.state.highlightedAgent; }
  set highlightedAgent(value) { this.state.highlightedAgent = value; }

  get assignmentMode() { return this.state.assignmentMode; }
  set assignmentMode(value) { this.state.assignmentMode = value; }

  get assignmentSourceTask() { return this.state.assignmentSourceTask; }
  set assignmentSourceTask(value) { this.state.assignmentSourceTask = value; }

  get assignmentMouseX() { return this.state.assignmentMouseX; }
  set assignmentMouseX(value) { this.state.assignmentMouseX = value; }

  get assignmentMouseY() { return this.state.assignmentMouseY; }
  set assignmentMouseY(value) { this.state.assignmentMouseY = value; }

  get combinerAssignMode() { return this.state.combinerAssignMode; }
  set combinerAssignMode(value) { this.state.combinerAssignMode = value; }

  get combinerAssignmentSource() { return this.state.combinerAssignmentSource; }
  set combinerAssignmentSource(value) { this.state.combinerAssignmentSource = value; }

  get createTaskMode() { return this.state.createTaskMode; }
  set createTaskMode(value) { this.state.createTaskMode = value; }

  // Timeline
  get timelineVisible() { return this.state.timelineVisible; }
  set timelineVisible(value) { this.state.timelineVisible = value; }

  get timelinePanelWidth() { return this.state.timelinePanelWidth; }
  set timelinePanelWidth(value) { this.state.timelinePanelWidth = value; }

  get timelinePanelTargetWidth() { return this.state.timelinePanelTargetWidth; }
  set timelinePanelTargetWidth(value) { this.state.timelinePanelTargetWidth = value; }

  get timelinePanelAnimating() { return this.state.timelinePanelAnimating; }
  set timelinePanelAnimating(value) { this.state.timelinePanelAnimating = value; }

  get timelineEvents() { return this.state.timelineEvents; }
  set timelineEvents(value) { this.state.timelineEvents = value; }

  get timelineScrollOffset() { return this.state.timelineScrollOffset; }
  set timelineScrollOffset(value) { this.state.timelineScrollOffset = value; }

  get timelineMaxEvents() { return this.state.timelineMaxEvents; }
  set timelineMaxEvents(value) { this.state.timelineMaxEvents = value; }

  // Chains
  get activeChains() { return this.state.activeChains; }
  set activeChains(value) { this.state.activeChains = value; }

  get chainParticles() { return this.state.chainParticles; }
  set chainParticles(value) { this.state.chainParticles = value; }

  // Combiner nodes
  get combinerNodes() { return this.state.combinerNodes; }
  set combinerNodes(value) { this.state.combinerNodes = value; }

  get connections() { return this.state.connections; }
  set connections(value) { this.state.connections = value; }

  get selectedCombiner() { return this.state.selectedCombiner; }
  set selectedCombiner(value) { this.state.selectedCombiner = value; }

  get hoveredCombiner() { return this.state.hoveredCombiner; }
  set hoveredCombiner(value) { this.state.hoveredCombiner = value; }

  // Execution logs
  get executionLogs() { return this.state.executionLogs; }
  set executionLogs(value) { this.state.executionLogs = value; }

  // Callbacks
  get onAgentClick() { return this.state.onAgentClick; }
  set onAgentClick(value) { this.state.onAgentClick = value; }

  get onMetricsUpdate() { return this.state.onMetricsUpdate; }
  set onMetricsUpdate(value) { this.state.onMetricsUpdate = value; }

  get onTimelineEvent() { return this.state.onTimelineEvent; }
  set onTimelineEvent(value) { this.state.onTimelineEvent = value; }

  // ==================== METHODS ====================


  resize() {
    const rect = this.canvas.getBoundingClientRect();
    this.canvas.width = rect.width * window.devicePixelRatio;
    this.canvas.height = rect.height * window.devicePixelRatio;
    this.ctx.scale(window.devicePixelRatio, window.devicePixelRatio);
    this.width = rect.width;
    this.height = rect.height;
    this.draw();
  }

  async init() {
    try {
      console.log('AgentCanvas.init() - studioId:', this.studioId);

      // Load studio data
      this.studio = await apiGet(`/api/studios/${this.studioId}`);

      console.log('AgentCanvas.init() - studio data loaded:', this.studio);

      // Load workspace progress
      this.workspaceProgress = this.studio.workspace_progress || {
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
      if (this.studio.shared_data && this.studio.shared_data.mission) {
        this.mission = this.studio.shared_data.mission;
      }

      // Load tasks from studio
      if (this.studio.tasks) {
        this.tasks = this.studio.tasks.map(task => {
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
      this.loadLayout();

      // Detect and initialize chains
      this.updateChains();

      // Connect to real-time events
      this.connectEventStream();

      // Start animation loop
      this.startAnimation();

      // Update canvas info
      document.getElementById('canvas-info').textContent =
        `Studio: ${this.studio.name || this.studioId} | Agents: ${this.agents.length}`;

      // Initialize metrics
      this.updateMetrics();

    } catch (error) {
      console.error('Failed to initialize canvas:', error);
      document.getElementById('canvas-info').textContent = 'Error loading studio';
    }
  }

  initializeAgents() {
    if (!this.studio || !this.studio.agents) return;

    const agentCount = this.studio.agents.length;
    const centerY = this.height * 0.6; // Position lower to avoid mission box
    const spacing = Math.min(150, (this.width * 0.8) / Math.max(agentCount - 1, 1));
    const totalWidth = spacing * (agentCount - 1);
    const startX = (this.width - totalWidth) / 2;

    // Get agent stats from studio data
    const agentStats = this.studio.agent_stats || {};

    this.agents = this.studio.agents.map((agentName, index) => {
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
        color: this.getAgentColor(index),
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
    if (this.studio.tasks) {
      // Preserve existing positions when updating tasks
      const existingPositions = {};
      this.tasks.forEach(t => {
        if (t.x !== null && t.y !== null) {
          existingPositions[t.id] = { x: t.x, y: t.y };
        }
      });

      this.tasks = this.studio.tasks.map(task => {
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

  getAgentColor(index) {
    const colors = [
      '#3b82f6', // blue
      '#10b981', // green
      '#f59e0b', // amber
      '#ef4444', // red
      '#8b5cf6', // purple
      '#ec4899', // pink
      '#14b8a6', // teal
      '#f97316'  // orange
    ];
    return colors[index % colors.length];
  }

  connectEventStream() {
    if (this.eventSource) {
      this.eventSource.close();
    }

    // Toast notifications array
    this.notifications = this.notifications || [];

    this.eventSource = connectProgressStream(this.studioId, {
      onInitial: (data) => {
        console.log('📊 Initial progress state:', data);
        if (data.workspace_progress) {
          this.workspaceProgress = data.workspace_progress;
        }
        if (data.agent_stats) {
          this.updateAgentStats(data.agent_stats);
        }
        if (data.tasks) {
          const existingPositions = {};
          this.tasks.forEach(t => {
            if (t.x !== null && t.y !== null) {
              existingPositions[t.id] = { x: t.x, y: t.y };
            }
          });

          this.tasks = data.tasks.map(task => {
            const existing = existingPositions[task.id];
            return {
              ...task,
              x: existing ? existing.x : (task.x ?? null),
              y: existing ? existing.y : (task.y ?? null)
            };
          });
        }
        this.draw();
      },
      onWorkspaceProgress: (data) => {
        console.log('📊 Workspace progress update:', data);
        if (data.workspace_progress) {
          this.workspaceProgress = data.workspace_progress;
        }
        if (data.agent_stats) {
          this.updateAgentStats(data.agent_stats);
        }
        this.draw();
      },
      onTaskEvent: (type, data) => {
        const evt = { type, data };
        this.handleTaskEvent(evt);
        const taskDesc = data.data?.description || 'Task';
        if (type === 'task.completed') {
          this.showNotification(`✓ ${taskDesc} completed`, 'success');
        } else if (type === 'task.failed') {
          const error = data.data?.error || 'Unknown error';
          this.showNotification(`✗ ${taskDesc} failed: ${error}`, 'error');
        } else if (type === 'task.started') {
          this.showNotification(`${taskDesc} started`, 'info');
        } else if (type === 'task.created') {
          this.showNotification('Task created', 'info');
        }
        this.addTimelineEvent(evt);
      },
      onTaskThinking: (data) => {
        this.addExecutionLog(data.data.task_id, 'thinking', data.data.message || 'Analyzing task...');
        this.addTimelineEvent({ type: 'task.thinking', data });
      },
      onTaskToolCall: (data) => {
        const toolName = data.data.tool_name || 'Unknown tool';
        this.addExecutionLog(data.data.task_id, 'tool_call', `Calling tool: ${toolName}`);
        this.addTimelineEvent({ type: 'task.tool_call', data });
      },
      onTaskToolSuccess: (data) => {
        this.addExecutionLog(data.data.task_id, 'tool_success', data.data.message || 'Tool succeeded');
        this.addTimelineEvent({ type: 'task.tool_success', data });
      },
      onTaskToolError: (data) => {
        this.addExecutionLog(data.data.task_id, 'tool_error', data.data.message || 'Tool failed');
        this.addTimelineEvent({ type: 'task.tool_error', data });
      },
      onTaskProgress: (data) => {
        this.addExecutionLog(data.data.task_id, 'progress', data.data.message || 'Task progress update');
        this.addTimelineEvent({ type: 'task.progress', data });
      },
      onError: (error) => {
        console.error('EventSource error:', error);
        setTimeout(() => {
          if (this.eventSource && this.eventSource.readyState === EventSource.CLOSED) {
            this.connectEventStream();
          }
        }, 5000);
      }
    });

    console.log('🔄 Connected to progress stream');
  }

  handleTaskEvent(eventData) {
    const taskId = eventData.data.task_id;
    const task = this.tasks.find(t => t.id === taskId);

    if (task) {
      // Update existing task
      if (eventData.type === 'task.started') {
        task.status = 'in_progress';
        task.started_at = new Date().toISOString();
      } else if (eventData.type === 'task.completed') {
        task.status = 'completed';
        task.completed_at = new Date().toISOString();

        // Store result on task if available
        if (eventData.data.result) {
          task.result = eventData.data.result;

          // Update the agent's lastResult
          if (task.to) {
            const agent = this.agents.find(a => a.name === task.to);
            if (agent) {
              agent.lastResult = eventData.data.result;
              console.log(`✅ Updated lastResult for agent ${agent.name}:`, eventData.data.result);
            }
          }
        }
      } else if (eventData.type === 'task.failed') {
        task.status = 'failed';
        task.error = eventData.data.error;
      }

      // Update chains when task status changes
      this.updateChains();
      this.draw();
    }
  }

  updateAgentStats(agentStats) {
    // Update agent status and stats from server
    for (const agentName in agentStats) {
      const agent = this.agents.find(a => a.name === agentName);
      if (agent) {
        const stats = agentStats[agentName];
        agent.status = stats.status;
        agent.currentTasks = stats.current_tasks || [];
        agent.queuedTasks = stats.queued_tasks || [];
        agent.completedTasks = stats.completed_tasks || 0;
        agent.failedTasks = stats.failed_tasks || 0;
        agent.totalExecutions = stats.total_executions || 0;
      }
    }

    // Update chains when agent stats change
    this.updateChains();
  }

  // Animation methods delegated to animation module
  updateChains() { return this.animation.updateChains(); }
  createChainParticle(fromTask, toTask) { return this.animation.createChainParticle(fromTask, toTask); }
  updateChainParticles() { return this.animation.updateChainParticles(); }

  showNotification(message, type = 'info') {
    const notification = {
      id: Date.now() + Math.random(),
      message,
      type, // 'info', 'success', 'warning', 'error'
      timestamp: Date.now()
    };

    this.notifications.push(notification);

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
      this.dismissNotification(notification.id);
    }, 5000);

    this.draw();
  }

  dismissNotification(id) {
    this.notifications = this.notifications.filter(n => n.id !== id);
    this.draw();
  }

  // Timeline methods delegated to timeline module
  addTimelineEvent(eventData) { return this.timeline.addTimelineEvent(eventData); }
  toggleTimeline() { return this.timeline.toggleTimeline(); }
  animateTimelinePanel(expanding) { return this.timeline.animateTimelinePanel(expanding); }

  addExecutionLog(taskId, type, message) {
    if (!this.executionLogs[taskId]) {
      this.executionLogs[taskId] = [];
    }

    this.executionLogs[taskId].push({
      type,
      message,
      timestamp: new Date()
    });

    // Limit logs per task to 50 entries
    if (this.executionLogs[taskId].length > 50) {
      this.executionLogs[taskId] = this.executionLogs[taskId].slice(-50);
    }

    this.draw();
  }

  handleEvent(event) {
    console.log('Canvas event:', event);

    switch (event.type) {
      case 'task.created':
      case 'task_created':
        this.addTask(event.data);
        break;
      case 'task.started':
      case 'task_started':
        this.updateTaskStatus(event.data.task_id, 'in_progress');
        this.setAgentStatus(event.data.assigned_to, 'active');
        break;
      case 'task.completed':
      case 'task_completed':
        this.updateTaskStatus(event.data.task_id, 'completed');
        break;
      case 'message.sent':
      case 'message_sent':
        this.addMessage(event.data);
        break;
      case 'mission_started':
        this.setMission(event.data.mission);
        break;
    }

    // Forward event to timeline callback
    if (this.onTimelineEvent) {
      this.onTimelineEvent(event);
    }

    // Update metrics after any task-related event
    if (event.type.includes('task')) {
      this.updateMetrics();
    }
  }

  addTask(taskData) {
    const task = {
      id: taskData.task_id || taskData.id,
      from: taskData.from || 'orchestrator',
      to: taskData.assigned_to || taskData.to,
      description: taskData.description,
      status: 'pending',
      progress: 0
    };
    this.tasks.push(task);

    // Create particle effect
    this.createTaskParticles(task);

    // Update metrics
    this.updateMetrics();
  }

  updateTaskStatus(taskId, status) {
    const task = this.tasks.find(t => t.id === taskId);
    if (task) {
      task.status = status;
      if (status === 'in_progress') {
        task.progress = 0;
      } else if (status === 'completed') {
        task.progress = 100;
      }
      // Update metrics when task status changes
      this.updateMetrics();
    }
  }

  setAgentStatus(agentName, status) {
    const agent = this.agents.find(a => a.name === agentName);
    if (agent) {
      agent.status = status;
    }
  }

  addMessage(messageData) {
    this.messages.push({
      from: messageData.from,
      to: messageData.to,
      content: messageData.content,
      timestamp: Date.now()
    });

    // Keep only last 50 messages
    if (this.messages.length > 50) {
      this.messages.shift();
    }
  }

  setMission(missionText) {
    this.mission = missionText;
    console.log('Mission set on canvas:', missionText);
  }

  // Animation methods delegated to animation module (continued)
  createTaskParticles(task) { return this.animation.createTaskParticles(task); }
  startAnimation() { return this.animation.startAnimation(); }
  update() { return this.animation.update(); }

  draw() {
    // Clear canvas with selected background color
    this.ctx.fillStyle = this.backgroundColor;
    this.ctx.fillRect(0, 0, this.width, this.height);

    this.ctx.save();
    this.ctx.translate(this.offsetX, this.offsetY);
    this.ctx.scale(this.scale, this.scale);

    // Draw connections between agents (disabled)
    // this.renderer.drawConnections();

    // Draw chain connections (highlighted paths for active chains)
    this.renderer.drawChainConnections();

    // Draw task flows
    this.renderer.drawTaskFlows();

    // Draw particles
    this.renderer.drawParticles();

    // Draw chain particles
    this.renderer.drawChainParticles();

    // Draw agents
    this.renderer.drawAgents();

    // Normalize combiner inputs before drawing connections/nodes
    if (this.combinerNodes.length) {
      this.combinerNodes.forEach(node => this.cleanupCombinerInputPorts(node, true));
    }

    // Draw workflow connections (between agents and combiners)
    this.renderer.drawWorkflowConnections();

    // Draw combiner nodes
    this.renderer.drawCombinerNodes();

    // Draw dragging connection line (if dragging)
    if (this.isDraggingConnection && this.connectionDragStart) {
      this.renderer.drawDraggingConnection();
    }

    this.ctx.restore();

    // Draw mission OUTSIDE the transform context (so it stays fixed at top)
    this.renderer.drawMission();

    // Draw expanded task panel OUTSIDE the transform context (fixed position)
    if (this.expandedPanelWidth > 0) {
      this.renderer.drawExpandedTaskPanel();
    }

    // Draw expanded agent panel OUTSIDE the transform context (fixed position)
    if (this.expandedAgentPanelWidth > 0) {
      this.renderer.drawExpandedAgentPanel();
    }

    // Draw expanded combiner panel OUTSIDE the transform context (fixed position)
    if (this.expandedCombinerPanelWidth > 0) {
      this.renderer.drawExpandedCombinerPanel();
    }

    // Draw assignment line
    if (this.assignmentMode && this.assignmentSourceTask) {
      this.renderer.drawAssignmentLine();
    }

    // Draw create task button (always visible)
    this.renderer.drawCreateTaskButton();

    // Draw add agent button (always visible)
    this.renderer.drawAddAgentButton();

    // Draw timeline panel (fixed position)
    if (this.timelinePanelWidth > 0) {
      this.renderer.drawTimelinePanel();
    }

    // Draw timeline toggle button (always visible)
    this.renderer.drawTimelineToggleButton();

    // Draw auto-layout button (always visible)
    this.renderer.drawAutoLayoutButton();

    // Draw save layout button (always visible)
    this.renderer.drawSaveLayoutButton();

    // Draw modals/forms on top of everything (except notifications)
    // Draw create task form
    if (this.forms.createTaskFormVisible) {
      this.forms.drawCreateTaskForm();
    }

    // Draw add agent form
    if (this.forms.addAgentFormVisible) {
      this.forms.drawAddAgentForm();
    }

    // Draw toast notifications (always on top)
    this.renderer.drawNotifications();

    // Draw context menu (if visible)
    if (this.contextMenuVisible) {
      this.renderer.drawContextMenu();
    }

    // Draw help overlay (if visible)
    if (this.helpOverlayVisible) {
      this.renderer.drawHelpOverlay();
    }
  }


  /**
   * Draw an arrow from (x1, y1) to (x2, y2)
   */


  /**
   * Draw connections from completed tasks to tasks that use their results
   */


  /**
   * Draw highlighted connection paths for active chains
   */

  /**
   * Draw chain particles
   */




  // Helper function to wrap text




  buildCombinerResultPreview(combiner) {
    const inputConns = this.connections.filter(c => c.to === combiner.id);
    if (!inputConns.length) return '';
    const inputs = [];
    inputConns.forEach(conn => {
      const nodeData = this.getNodeById(conn.from);
      if (nodeData?.type === 'task' && nodeData.node?.result) {
        inputs.push(nodeData.node.result);
      }
    });
    if (!inputs.length) return '';

    switch (combiner.resultCombinationMode) {
      case 'append':
        return inputs.join('\n---\n');
      case 'summarize':
      case 'merge':
      default:
        return inputs.map((t, i) => `• Input ${i + 1}: ${t}`).join('\n');
    }
  }

  // Helper function to draw rounded rectangle

  // Mouse interaction handlers





  toggleTaskPanel(task) {
    // Close agent panel if open
    if (this.expandedAgent) {
      this.closeAgentPanel();
    }
    if (this.expandedCombiner) {
      this.closeCombinerPanel();
    }

    if (this.expandedTask && this.expandedTask.id === task.id) {
      // Clicking the same task - close panel
      this.closeTaskPanel();
    } else {
      // Expand panel for this task
      this.expandedTask = task;
      this.resultScrollOffset = 0; // Reset scroll when opening a new task
      this.copyButtonState = 'idle'; // Reset copy button state
      this.expandedPanelAnimating = true;
      this.animatePanel(true);
    }
  }

  closeTaskPanel() {
    this.expandedPanelAnimating = true;
    this.animatePanel(false);
  }

  animatePanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.expandedPanelWidth = Math.min(
          this.expandedPanelWidth + speed,
          this.expandedPanelTargetWidth
        );

        if (this.expandedPanelWidth >= this.expandedPanelTargetWidth) {
          this.expandedPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.expandedPanelWidth = Math.max(this.expandedPanelWidth - speed, 0);

        if (this.expandedPanelWidth <= 0) {
          this.expandedPanelAnimating = false;
          this.expandedTask = null;
          this.resultScrollOffset = 0; // Reset scroll when closing panel
        } else {
          requestAnimationFrame(animate);
        }
      }
    };

    animate();
  }

  async toggleAgentPanel(agent) {
    // Close task panel if open
    if (this.expandedTask) {
      this.closeTaskPanel();
    }
    if (this.expandedCombiner) {
      this.closeCombinerPanel();
    }

    if (this.expandedAgent && this.expandedAgent.name === agent.name) {
      // Clicking the same agent - close panel
      this.closeAgentPanel();
    } else {
      // Reset scroll offset when opening new agent
      this.agentPanelScrollOffset = 0;
      this.agentPanelMaxScroll = 0;

      // Fetch agent configuration before expanding (optional - doesn't block panel)
      try {
        const configResponse = await apiGet(`/api/agents/${agent.name}`);
        if (configResponse.ok) {
          const agentConfig = await configResponse.json();
          // Merge config data with agent
          this.expandedAgent = {
            ...agent,
            config: agentConfig
          };
        } else {
          // Use agent without detailed config if fetch fails (workspace agents may not be in global store)
          console.log(`Agent ${agent.name} config not found in global store - using workspace data`);
          this.expandedAgent = {
            ...agent,
            config: null
          };
        }
      } catch (error) {
        console.log('Using workspace agent data without global config:', error.message);
        this.expandedAgent = {
          ...agent,
          config: null
        };
      }

      this.expandedAgentPanelAnimating = true;
      this.animateAgentPanel(true);
    }
  }

  closeAgentPanel() {
    this.expandedAgentPanelAnimating = true;
    this.animateAgentPanel(false);
  }

  toggleCombinerPanel(combiner) {
    // Close other panels
    if (this.expandedTask) this.closeTaskPanel();
    if (this.expandedAgent) this.closeAgentPanel();

    if (this.expandedCombiner && this.expandedCombiner.id === combiner.id) {
      this.closeCombinerPanel();
      return;
    }

    this.expandedCombiner = combiner;
    this.expandedCombinerPanelAnimating = true;
    this.animateCombinerPanel(true);
  }

  closeCombinerPanel() {
    this.expandedCombinerPanelAnimating = true;
    this.animateCombinerPanel(false);
  }

  animateCombinerPanel(expanding) {
    const animate = () => {
      const speed = 30;
      if (expanding) {
        this.expandedCombinerPanelWidth = Math.min(
          this.expandedCombinerPanelWidth + speed,
          this.expandedCombinerPanelTargetWidth
        );
        if (this.expandedCombinerPanelWidth >= this.expandedCombinerPanelTargetWidth) {
          this.expandedCombinerPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.expandedCombinerPanelWidth = Math.max(this.expandedCombinerPanelWidth - speed, 0);
        if (this.expandedCombinerPanelWidth <= 0) {
          this.expandedCombinerPanelAnimating = false;
          this.expandedCombiner = null;
        } else {
          requestAnimationFrame(animate);
        }
      }
    };
    animate();
  }

  animateAgentPanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.expandedAgentPanelWidth = Math.min(
          this.expandedAgentPanelWidth + speed,
          this.expandedAgentPanelTargetWidth
        );

        if (this.expandedAgentPanelWidth >= this.expandedAgentPanelTargetWidth) {
          this.expandedAgentPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.expandedAgentPanelWidth = Math.max(this.expandedAgentPanelWidth - speed, 0);

        if (this.expandedAgentPanelWidth <= 0) {
          this.expandedAgentPanelAnimating = false;
          this.expandedAgent = null;
          this.agentPanelScrollOffset = 0; // Reset scroll when closing panel
          this.agentPanelMaxScroll = 0; // Reset max scroll when closing panel
        } else {
          requestAnimationFrame(animate);
        }
      }
    };

    animate();
  }

  async copyResultToClipboard() {
    if (!this.expandedTask || !this.expandedTask.result) {
      return;
    }

    try {
      // Use the Clipboard API to copy text
      await navigator.clipboard.writeText(this.expandedTask.result);

      // Update button state to show success
      this.copyButtonState = 'copied';
      this.draw();

      // Reset button state after 2 seconds
      setTimeout(() => {
        if (this.copyButtonState === 'copied') {
          this.copyButtonState = 'idle';
          this.draw();
        }
      }, 2000);

      console.log('✓ Result copied to clipboard');
    } catch (error) {
      console.error('Failed to copy to clipboard:', error);
      // Fallback: show error notification
      alert('Failed to copy to clipboard. Please try selecting and copying the text manually.');
    }
  }

  toggleAssignmentMode(task) {
    console.log('toggleAssignmentMode called for task:', task.id);
    if (this.assignmentMode && this.assignmentSourceTask && this.assignmentSourceTask.id === task.id) {
      // Cancel assignment mode
      console.log('Exiting assignment mode');
      this.assignmentMode = false;
      this.assignmentSourceTask = null;
      this.assignmentMouseX = 0;
      this.assignmentMouseY = 0;
      this.canvas.style.cursor = 'grab';
    } else {
      // Enter assignment mode
      console.log('Entering assignment mode for task:', task.id);
      this.assignmentMode = true;
      this.assignmentSourceTask = task;
      this.canvas.style.cursor = 'crosshair';
    }
    this.draw();
  }


  // Layout methods delegated to layout module
  autoLayoutTasks() { return this.layout.autoLayoutTasks(); }

  zoomToFitContent() { return this.layout.zoomToFitContent(); }


  calculateTaskLevels() { return this.layout.calculateTaskLevels(); }

  async assignTaskToAgent(agent) {
    // Update task assignment via API
    try {
      const result = await apiPut(`/api/orchestration/tasks`, {
        task_id: this.assignmentSourceTask.id,
        to: agent.name
      });
      console.log('✅ Task assigned:', result);

      // Exit assignment mode
      this.assignmentMode = false;
      this.assignmentSourceTask = null;
      this.assignmentMouseX = 0;
      this.assignmentMouseY = 0;
      this.canvas.style.cursor = 'grab';

      // Update task locally
      const task = this.tasks.find(t => t.id === result.id);
      if (task) {
        task.to = agent.name;
      }

      this.draw();

      // Show success notification
      this.addNotification(`✅ Task assigned to ${agent.name}`, 'success');
    } catch (error) {
      console.error('❌ Error assigning task:', error);
      this.addNotification('Failed to assign task: ' + error.message, 'error');
      this.assignmentMode = false;
      this.assignmentSourceTask = null;
      this.assignmentMouseX = 0;
      this.assignmentMouseY = 0;
      this.canvas.style.cursor = 'grab';
      this.draw();
    }
  }

  async assignTaskToCombiner(combiner) {
    return tasksAssignToCombiner(this, combiner);
  }

  updateMetrics() {
    if (!this.onMetricsUpdate) return;

    const completed = this.tasks.filter(t => t.status === 'completed').length;
    const inProgress = this.tasks.filter(t => t.status === 'in_progress').length;

    this.onMetricsUpdate({
      total: this.tasks.length,
      completed: completed,
      inProgress: inProgress
    });
  }

  /**
   * Draw the floating "Create Task" button in the top-right corner
   */

  /**
   * Draw the floating "Add Agent" button to the left of Create Task button
   */

  /**
   * Draw toast notifications
   */

  /**
   * Draw timeline toggle button
   */

  /**
   * Draw auto-layout button
   */

  /**
   * Draw save layout button
   */

  /**
   * Draw timeline panel
   */

  /**
   * Draw empty timeline state
   */

  /**
   * Draw a single timeline event
   */

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

  /**
   * Delete a task
   */
  async deleteTask(task) {
    if (!task || !task.id) {
      console.error('Invalid task:', task);
      return;
    }

    // Confirm deletion
    const confirmed = confirm(`Are you sure you want to delete this task?\n\n"${task.description || 'Task'}"\n\nThis action cannot be undone.`);
    if (!confirmed) return;

    try {
      await apiDelete(`/api/orchestration/tasks?id=${encodeURIComponent(task.id)}`);

      console.log('✅ Task deleted:', task.id);

      // Remove task from local array
      const index = this.tasks.findIndex(t => t.id === task.id);
      if (index !== -1) {
        this.tasks.splice(index, 1);
      }

      // Close task panel if it was open
      if (this.expandedTask && this.expandedTask.id === task.id) {
        this.closeTaskPanel();
      }

      // Redraw canvas
      this.draw();

      // Update metrics
      this.updateMetrics();

      // Reload to ensure consistency
      setTimeout(() => this.init(), 500);

    } catch (error) {
      console.error('❌ Error deleting task:', error);
      alert('Failed to delete task: ' + error.message);
    }
  }

  /**
   * Execute a task manually
   */
  async executeTask(task) {
    return tasksExecuteTask(this, task);
  }

  /**
   * Rerun a completed or failed task
   */
  async rerunTask(task) {
    return tasksRerunTask(this, task);
  }

  /**
   * Find the most recent task associated with an agent so combiners can treat
   * direct agent connections as inputs.
   */
  getLatestTaskForAgent(agentName) {
    if (!agentName || !this.tasks || this.tasks.length === 0) {
      return null;
    }

    const candidates = this.tasks.filter(task =>
      task && (task.to === agentName || task.from === agentName)
    );

    if (candidates.length === 0) {
      return null;
    }

    const getTimestamp = (task) => {
      const value = task.completed_at || task.started_at || task.created_at;
      const parsed = value ? new Date(value).getTime() : 0;
      return Number.isNaN(parsed) ? 0 : parsed;
    };

    const sortByRecency = (a, b) => getTimestamp(b) - getTimestamp(a);

    const completedWithResult = candidates
      .filter(task => task.status === 'completed' && task.result)
      .sort(sortByRecency);
    if (completedWithResult.length > 0) {
      return completedWithResult[0];
    }

    const completed = candidates
      .filter(task => task.status === 'completed')
      .sort(sortByRecency);
    if (completed.length > 0) {
      return completed[0];
    }

    const active = candidates
      .filter(task => task.status === 'in_progress' || task.status === 'assigned')
      .sort(sortByRecency);
    if (active.length > 0) {
      return active[0];
    }

    return candidates.sort(sortByRecency)[0];
  }

  /**
   * Execute a combiner node - sets up and executes the output task with merged inputs
   */
  /**
   * Execute a combiner node - executes the combiner's internal task with merged inputs
   */
  async executeCombiner(combiner) {
    return combinerExecute(this, combiner);
  }


  showExecutionLog(task) {
    const logs = this.executionLogs[task.id] || [];

    if (logs.length === 0) {
      this.addNotification('No execution log available for this task', 'info');
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

  destroy() {
    if (this.animationFrame) {
      cancelAnimationFrame(this.animationFrame);
    }
    if (this.eventSource) {
      this.eventSource.close();
    }
  }

  async saveLayout() { return this.layout.saveLayout(); }

  loadLayout() { return this.layout.loadLayout(); }

  // === NEW FEATURES ===

  zoomToFit() { return this.layout.zoomToFit(); }

  // Context menu for agents

  // Toggle help overlay
  toggleHelpOverlay() {
    if (!this.helpOverlayVisible) {
      this.helpOverlayVisible = true;
      console.log('📖 Showing keyboard shortcuts');
    } else {
      this.helpOverlayVisible = false;
      console.log('📖 Hiding keyboard shortcuts');
    }
    this.draw();
  }

  // Draw context menu for agent quick actions

  // Handle context menu action
  handleContextMenuAction(action, agent) {
    console.log(`🎯 Context menu action: ${action} for agent ${agent.name}`);

    switch (action) {
      case 'view':
        // View agent details - expand agent panel
        if (this.expandedAgentPanelWidth === 0) {
          this.expandedAgentPanelWidth = 1;
          this.expandedAgentPanelTarget = 350;
        }
        this.selectedAgent = agent;
        this.draw();
        break;

      case 'assign':
        // Assign task to agent - show task creation form
        this.forms.showCreateTaskForm(agent.x, agent.y);
        this.forms.createTaskTargetAgent = agent.name;
        this.draw();
        break;

      case 'remove':
        // Remove agent (with confirmation)
        if (confirm(`Remove agent "${agent.name}"?`)) {
          // Call backend to remove agent from studio
          apiDelete(`/api/studios/${encodeURIComponent(this.studioId)}/agents/${encodeURIComponent(agent.name)}`)
            .then(() => {
              // Remove from local state
              this.agents = this.agents.filter(a => a.name !== agent.name);

              // Unassign tasks targeting this agent
              this.tasks = this.tasks.map(t => ({
                ...t,
                to: t.to === agent.name ? 'unassigned' : t.to
              }));

              // Remove any workflow connections involving this agent
              this.connections = this.connections.filter(c =>
                c.from !== agent.name && c.to !== agent.name
              );

              this.showNotification('Agent removed', 'success');
              this.draw();
              this.saveLayout();
            })
            .catch(err => {
              console.error('Failed to remove agent:', err);
              this.addNotification(`Failed to remove agent: ${err.message}`, 'error');
            });
        }
        break;

      default:
        console.warn(`Unknown context menu action: ${action}`);
    }
  }

  // Draw help overlay with keyboard shortcuts

  // ==================== COMBINER NODE RENDERING ====================

  /**
   * Draw all workflow connections
   */

  /**
   * Draw all combiner nodes
   */

  /**
   * Draw a connection port
   */

  /**
   * Draw connection being dragged
   */

  /**
   * Helper: Lighten a hex color
   */
  lightenColor(color, percent) {
    const num = parseInt(color.replace('#', ''), 16);
    const amt = Math.round(2.55 * percent);
    const R = (num >> 16) + amt;
    const G = (num >> 8 & 0x00FF) + amt;
    const B = (num & 0x0000FF) + amt;
    return '#' + (0x1000000 + (R < 255 ? R < 1 ? 0 : R : 255) * 0x10000 +
      (G < 255 ? G < 1 ? 0 : G : 255) * 0x100 +
      (B < 255 ? B < 1 ? 0 : B : 255))
      .toString(16).slice(1);
  }

  /**
   * Helper: Darken a hex color
   */
  darkenColor(color, percent) {
    const num = parseInt(color.replace('#', ''), 16);
    const amt = Math.round(2.55 * percent);
    const R = (num >> 16) - amt;
    const G = (num >> 8 & 0x00FF) - amt;
    const B = (num & 0x0000FF) - amt;
    return '#' + (0x1000000 + (R > 0 ? R : 0) * 0x10000 +
      (G > 0 ? G : 0) * 0x100 +
      (B > 0 ? B : 0))
      .toString(16).slice(1);
  }

  // ==================== COMBINER NODE SYSTEM ====================

  /**
   * Combiner node types and their configurations
   */
  static COMBINER_TYPES = {
    MERGE: {
      id: 'merge',
      name: 'Merge',
      icon: '🔀',
      color: '#8b5cf6',
      description: 'Combine multiple inputs into single context',
      resultCombinationMode: 'merge'
    },
    APPEND: {
      id: 'append',
      name: 'Append',
      icon: '📎',
      color: '#3b82f6',
      description: 'Concatenate outputs sequentially',
      resultCombinationMode: 'append'
    },
    SUMMARIZE: {
      id: 'summarize',
      name: 'Summarize',
      icon: '📝',
      color: '#10b981',
      description: 'Create executive summary of inputs',
      resultCombinationMode: 'summarize'
    },
    COMPARE: {
      id: 'compare',
      name: 'Compare',
      icon: '⚖️',
      color: '#f59e0b',
      description: 'Side-by-side comparison of inputs',
      resultCombinationMode: 'compare'
    },
    VOTE: {
      id: 'vote',
      name: 'Vote',
      icon: '🗳️',
      color: '#ef4444',
      description: 'Select best result via voting',
      resultCombinationMode: 'custom',
      customInstruction: 'Analyze all inputs and select the best result based on quality, accuracy, and completeness.'
    }
  };

  /**
   * Create a new combiner node
   */
  async createCombinerNode(type, x, y) {
    const combinerType = AgentCanvas.COMBINER_TYPES[type.toUpperCase()];
    if (!combinerType) {
      console.error(`Unknown combiner type: ${type}`);
      return null;
    }

    const node = {
      id: `combiner-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`,
      type: 'combiner',
      combinerType: combinerType.id,
      name: combinerType.name,
      icon: combinerType.icon,
      color: combinerType.color,
      description: combinerType.description,
      x: x,
      y: y,
      width: 120,
      height: 80,
      inputPorts: [], // Will be populated when connections are made
      outputPort: { id: 'output', x: 0, y: 40 }, // Relative to node position
      resultCombinationMode: combinerType.resultCombinationMode,
      customInstruction: combinerType.customInstruction || '',
      config: {},
      taskId: null // Will be set after task creation
    };

    this.combinerNodes.push(node);
    console.log(`✨ Created ${node.name} combiner node at (${x}, ${y})`);

    // Create a backend task for this combiner
    await this.createCombinerTask(node);

    return node;
  }

  // Combiner helpers (delegated to combiner module)
  async createCombinerTask(combinerNode) {
    return combinerCreateTask(this, combinerNode);
  }

  async ensureCombinerTask(combiner) {
    return combinerEnsureTask(this, combiner);
  }

  /**
   * Ensure a combiner has a tracked input port entry (used for spacing/persistence)
   */
  ensureCombinerInputPort(combiner, portId) {
    if (!combiner) return;
    combiner.inputPorts = combiner.inputPorts || [];
    if (!combiner.inputPorts.find(p => p.id === portId)) {
      combiner.inputPorts.push({ id: portId });
    }
  }

  /**
   * Create a connection between two nodes (agent/combiner to agent/combiner)
   */
  createConnection(fromNodeId, fromPort, toNodeId, toPort) {
    // Avoid duplicate connections with same endpoints
    const existing = this.connections.find(c =>
      c.from === fromNodeId &&
      c.fromPort === fromPort &&
      c.to === toNodeId &&
      c.toPort === toPort
    );
    if (existing) {
      console.log(`ℹ️ Connection already exists: ${fromNodeId}.${fromPort} → ${toNodeId}.${toPort}`);
      return existing;
    }

    const connection = {
      id: `conn-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`,
      from: fromNodeId,
      fromPort: fromPort,
      to: toNodeId,
      toPort: toPort,
      color: '#6366f1',
      animated: false
    };

    // Track combiner input ports so spacing is stable and persisted
    const targetNode = this.getNodeById(toNodeId);
    if (targetNode && targetNode.type === 'combiner' && toPort && toPort.startsWith('input')) {
      this.ensureCombinerInputPort(targetNode.node, toPort);
    }

    this.connections.push(connection);
    console.log(`🔗 Created connection: ${fromNodeId}.${fromPort} → ${toNodeId}.${toPort}`);
    this.saveLayout();
    return connection;
  }

  /**
   * Get node by ID (searches both agents and combiners)
   */
  getNodeById(nodeId) {
    // Check if it's an agent
    const agent = this.agents.find(a => a.name === nodeId || a.id === nodeId);
    if (agent) return { type: 'agent', node: agent };

    // Check if it's a task
    const task = this.tasks.find(t => t.id === nodeId);
    if (task) return { type: 'task', node: task };

    // Check if it's a combiner
    const combiner = this.combinerNodes.find(c => c.id === nodeId);
    if (combiner) return { type: 'combiner', node: combiner };

    return null;
  }

  /**
   * Get port position in screen coordinates
   */
  getPortPosition(nodeId, portId) {
    const nodeData = this.getNodeById(nodeId);
    if (!nodeData) return null;

    const { type, node } = nodeData;

    if (type === 'agent') {
      const halfHeight = (node.height || 70) / 2;
      if (portId === 'input') {
        return {
          x: node.x * this.scale + this.offsetX,
          y: (node.y - halfHeight - 10) * this.scale + this.offsetY
        };
      }
      // Agents expose output port at bottom by default
      return {
        x: node.x * this.scale + this.offsetX,
        y: (node.y + halfHeight + 10) * this.scale + this.offsetY
      };
    } else if (type === 'task') {
      // Tasks have a single output port at the bottom center
      if (node.cardBounds) {
        return {
          x: node.x * this.scale + this.offsetX,
          y: (node.cardBounds.y + node.cardBounds.height + 5) * this.scale + this.offsetY
        };
      }
      return null;
    } else if (type === 'combiner') {
      // Combiner nodes have multiple input ports at top, one output at bottom
      if (portId === 'output') {
        return {
          x: (node.x + node.width / 2) * this.scale + this.offsetX,
          y: (node.y + node.height + 10) * this.scale + this.offsetY
        };
      } else {
        // Input ports are distributed across the top. Use the provided id to derive index.
        const match = /input-(\d+)/.exec(portId);
        const numericIndex = match ? parseInt(match[1], 10) : -1;
        const inputIndex = node.inputPorts.findIndex(p => p.id === portId);
        const resolvedIndex = numericIndex >= 0 ? numericIndex : inputIndex;

        // Total inputs based on known ports and requested index, minimum 1 for usability
        const totalInputs = Math.max(node.inputPorts.length, resolvedIndex + 1, 1);
        const portSpacing = node.width / (totalInputs + 1);
        return {
          x: (node.x + portSpacing * (resolvedIndex + 1)) * this.scale + this.offsetX,
          y: (node.y - 10) * this.scale + this.offsetY
        };
      }
    }

    return null;
  }

  /**
   * Delete a combiner node and its connections
   */
  deleteCombinerNode(nodeId) {
    // Remove the node
    this.combinerNodes = this.combinerNodes.filter(n => n.id !== nodeId);

    // Remove all connections involving this node
    this.connections = this.connections.filter(c =>
      c.from !== nodeId && c.to !== nodeId
    );

    console.log(`🗑️ Deleted combiner node: ${nodeId}`);
    this.saveLayout();
    this.draw();
  }

  /**
   * Delete a connection
   */
  deleteConnection(connectionId) {
    // Find the connection before deleting to check if it's connected to a combiner
    const connectionToDelete = this.connections.find(c => c.id === connectionId);

    // Remove the connection
    this.connections = this.connections.filter(c => c.id !== connectionId);
    console.log(`🗑️ Deleted connection: ${connectionId}`);

    // Clean up unused combiner input ports
    if (connectionToDelete) {
      const targetNode = this.getNodeById(connectionToDelete.to);
      if (targetNode && targetNode.type === 'combiner') {
        this.cleanupCombinerInputPorts(targetNode.node);
      }
    }

    this.saveLayout();
    this.draw();
  }

  /**
   * Remove unused input ports from a combiner node
   */
  cleanupCombinerInputPorts(combiner, silent = false) {
    if (!combiner || !combiner.inputPorts) return;

    // Get all connections to this combiner
    const connected = this.connections
      .filter(c => c.to === combiner.id && c.toPort && c.toPort.startsWith('input'));

    // Normalize and reindex input ports to remove gaps
    const normalized = connected
      .map(conn => {
        const match = /input-(\d+)/.exec(conn.toPort);
        return { conn, index: match ? parseInt(match[1], 10) : 0 };
      })
      .sort((a, b) => a.index - b.index);

    normalized.forEach(({ conn }, idx) => {
      const targetPortId = `input-${idx}`;
      if (conn.toPort !== targetPortId) {
        conn.toPort = targetPortId;
      }
    });

    combiner.inputPorts = normalized.map((_, idx) => ({ id: `input-${idx}` }));

    if (!silent) {
      console.log(`🧹 Cleaned up combiner ${combiner.name}: ${combiner.inputPorts.length} ports remaining`);
    }
  }

  /**
   * Get connection near a given position (for click detection)
   */
  getConnectionAtPosition(x, y, threshold = 10) {
    for (const conn of this.connections) {
      const fromPos = this.getPortPosition(conn.from, conn.fromPort);
      const toPos = this.getPortPosition(conn.to, conn.toPort);

      if (!fromPos || !toPos) continue;

      // Convert to canvas coordinates
      const fromX = (fromPos.x - this.offsetX) / this.scale;
      const fromY = (fromPos.y - this.offsetY) / this.scale;
      const toX = (toPos.x - this.offsetX) / this.scale;
      const toY = (toPos.y - this.offsetY) / this.scale;

      // Calculate distance from point to line segment
      const dx = toX - fromX;
      const dy = toY - fromY;
      const lengthSquared = dx * dx + dy * dy;

      if (lengthSquared === 0) continue;

      const t = Math.max(0, Math.min(1, ((x - fromX) * dx + (y - fromY) * dy) / lengthSquared));
      const projX = fromX + t * dx;
      const projY = fromY + t * dy;
      const distance = Math.sqrt((x - projX) ** 2 + (y - projY) ** 2);

      if (distance <= threshold) {
        return conn;
      }
    }
    return null;
  }

  /**
   * Get port at a given canvas position (for click detection)
   */
  getPortAtPosition(x, y) {
    const portRadius = 14; // Click detection radius (larger for easier hits with triangles)

    // Check task output ports (NEW - for connecting tasks to combiners)
    for (const task of this.tasks) {
      if (!task.cardBounds) continue;

      // Output port at bottom center of task card
      const portX = task.x;
      const portY = task.cardBounds.y + task.cardBounds.height + 5;
      const dist = Math.sqrt((x - portX) ** 2 + (y - portY) ** 2);
      if (dist <= portRadius) {
        return {
          nodeId: task.id,
          nodeType: 'task',
          portId: 'output',
          type: 'output'
        };
      }
    }

    // Check agent output ports
    for (const agent of this.agents) {
      const portX = agent.x;
      const halfHeight = (agent.height || 70) / 2;
      const portY = agent.y + halfHeight + 10;
      const dist = Math.sqrt((x - portX) ** 2 + (y - portY) ** 2);
      if (dist <= portRadius) {
        return {
          nodeId: agent.name,
          nodeType: 'agent',
          portId: 'output',
          type: 'output'
        };
      }

      // Agent input port (top center) for receiving connections
      const inputPortY = agent.y - halfHeight - 10;
      const inputDist = Math.sqrt((x - portX) ** 2 + (y - inputPortY) ** 2);
      if (inputDist <= portRadius) {
        return {
          nodeId: agent.name,
          nodeType: 'agent',
          portId: 'input',
          type: 'input'
        };
      }
    }

    // Check combiner ports
    for (const combiner of this.combinerNodes) {
      // Check input ports (top)
      const numInputs = Math.max(combiner.inputPorts.length, 2);
      const portSpacing = combiner.width / (numInputs + 1);
      for (let i = 0; i < numInputs; i++) {
        const portX = combiner.x + portSpacing * (i + 1);
        const portY = combiner.y - 5;
        const dist = Math.sqrt((x - portX) ** 2 + (y - portY) ** 2);
        if (dist <= portRadius) {
          return {
            nodeId: combiner.id,
            portId: `input-${i}`,
            type: 'input'
          };
        }
      }

      // Check output port (bottom)
      const outputX = combiner.x + combiner.width / 2;
      const outputY = combiner.y + combiner.height + 5;
      const dist = Math.sqrt((x - outputX) ** 2 + (y - outputY) ** 2);
      if (dist <= portRadius) {
        return {
          nodeId: combiner.id,
          portId: 'output',
          type: 'output'
        };
      }
    }

    return null;
  }
}

// Make it globally accessible
window.AgentCanvas = AgentCanvas;
