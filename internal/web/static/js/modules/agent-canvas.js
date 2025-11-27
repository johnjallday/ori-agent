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

    // Setup canvas
    this.resize();
    window.addEventListener('resize', () => this.resize());

    // Mouse interactions
    this.canvas.addEventListener('mousedown', (e) => this.onMouseDown(e));
    this.canvas.addEventListener('mousemove', (e) => this.onMouseMove(e));
    this.canvas.addEventListener('mouseup', () => this.onMouseUp());
    this.canvas.addEventListener('mouseleave', () => this.onMouseUp());
    this.canvas.addEventListener('wheel', (e) => this.onWheel(e));
    this.canvas.addEventListener('click', (e) => this.onClick(e));
    this.canvas.addEventListener('contextmenu', (e) => this.onContextMenu(e));

    // Keyboard interactions
    window.addEventListener('keydown', (e) => this.onKeyDown(e));
    window.addEventListener('keyup', (e) => this.onKeyUp(e));

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

  onKeyDown(e) {
    // Track modifier keys
    if (e.key === ' ') {
      this.spacePressed = true;
      if (!this.isDragging) {
        this.canvas.style.cursor = 'grab';
      }
    }
    if (e.ctrlKey || e.metaKey) {
      this.ctrlPressed = true;
    }

    // H key - Toggle help overlay
    if (e.key === 'h' || e.key === 'H') {
      if (!this.forms.createTaskDescriptionFocused) {
        e.preventDefault();
        this.toggleHelpOverlay();
        return;
      }
    }

    // Handle text input when description field is focused
    if (this.forms.createTaskDescriptionFocused) {
      if (e.key === 'Escape' || e.key === 'Esc') {
        // ESC closes the entire form when description is focused
        e.preventDefault();
        this.forms.hideCreateTaskForm();
        return;
      } else if (e.key === 'Enter') {
        // Finish typing, unfocus field
        this.forms.createTaskDescriptionFocused = false;
        this.canvas.style.cursor = 'default';
        this.draw();
        return;
      } else if (e.key === 'Backspace') {
        // Remove last character
        e.preventDefault();
        if (!this.forms.createTaskDescription) {
          this.forms.createTaskDescription = '';
        }
        this.forms.createTaskDescription = this.forms.createTaskDescription.slice(0, -1);
        this.draw();
        return;
      } else if (e.key.length === 1) {
        // Add character to description
        e.preventDefault();
        if (!this.forms.createTaskDescription) {
          this.forms.createTaskDescription = '';
        }
        this.forms.createTaskDescription += e.key;
        this.draw();
        return;
      }
      return; // Consume all other keys when focused
    }

    // ESC key - close forms or cancel connection/assignment modes
    if (e.key === 'Escape' || e.key === 'Esc') {
      if (this.helpOverlayVisible) {
        // Close help overlay
        e.preventDefault();
        this.helpOverlayVisible = false;
        this.draw();
        return;
      } else if (this.contextMenuVisible) {
        // Close context menu
        e.preventDefault();
        this.contextMenuVisible = false;
        this.contextMenuAgent = null;
        this.contextMenuItems = [];
        this.draw();
        return;
      } else if (this.forms.addAgentFormVisible) {
        // Close the add agent form
        e.preventDefault();
        this.forms.hideAddAgentForm();
      } else if (this.forms.createTaskFormVisible) {
        // Close the create task form
        e.preventDefault();
        this.forms.hideCreateTaskForm();
      } else if (this.assignmentMode) {
        this.assignmentMode = false;
        this.assignmentSourceTask = null;
        this.assignmentMouseX = 0;
        this.assignmentMouseY = 0;
        this.canvas.style.cursor = 'grab';
        this.draw();
        console.log('Assignment mode cancelled');
      } else if (this.combinerAssignMode) {
        this.combinerAssignMode = false;
        this.combinerAssignmentSource = null;
        this.canvas.style.cursor = 'grab';
        this.draw();
        console.log('Combiner assignment cancelled');
      }
    }

    // ESC also cancels connection dragging (e.g., from combiner/agent ports)
    if ((e.key === 'Escape' || e.key === 'Esc') && this.isDraggingConnection) {
      e.preventDefault();
      this.isDraggingConnection = false;
      this.connectionDragStart = null;
      this.canvas.style.cursor = 'grab';
      this.draw();
      return;
    }
  }

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

  /**
   * Detect and update active task chains
   */
  updateChains() {
    if (!this.tasks || this.tasks.length === 0) {
      this.activeChains = [];
      return;
    }

    const chains = [];

    // Find all tasks that are part of chains (have input_task_ids)
    this.tasks.forEach(task => {
      if (task.input_task_ids && task.input_task_ids.length > 0) {
        // This task depends on other tasks - it's part of a chain
        task.input_task_ids.forEach(inputTaskId => {
          const inputTask = this.tasks.find(t => t.id === inputTaskId);
          if (inputTask) {
            // Found a link in the chain
            chains.push({
              from: inputTask,
              to: task,
              active: task.status === 'in_progress' || task.status === 'pending',
              completed: task.status === 'completed',
              failed: task.status === 'failed'
            });
          }
        });
      }
    });

    this.activeChains = chains;
  }

  /**
   * Create chain particles for active chains
   */
  createChainParticle(fromTask, toTask) {
    if (!fromTask || !toTask || fromTask.x == null || toTask.x == null) return;

    const particle = {
      x: fromTask.x,
      y: fromTask.y,
      targetX: toTask.x,
      targetY: toTask.y,
      progress: 0,
      speed: 0.01 + Math.random() * 0.01,
      alpha: 1,
      size: 4,
      color: toTask.status === 'in_progress' ? '#3b82f6' : '#6b7280'
    };

    this.chainParticles.push(particle);
  }

  /**
   * Update chain particles animation
   */
  updateChainParticles() {
    // Update existing particles
    this.chainParticles = this.chainParticles.filter(p => {
      p.progress += p.speed;
      p.x = p.x + (p.targetX - p.x) * p.progress;
      p.y = p.y + (p.targetY - p.y) * p.progress;
      p.alpha = 1 - p.progress;
      return p.progress < 1;
    });

    // Generate new particles for active chains
    this.activeChains.forEach(chain => {
      if (chain.active && !chain.completed && Math.random() < 0.1) {
        this.createChainParticle(chain.from, chain.to);
      }
    });
  }

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

  addTimelineEvent(eventData) {
    // Add event to timeline (prepend to show newest first)
    this.timelineEvents.unshift({
      id: eventData.id || Date.now() + Math.random(),
      type: eventData.type,
      timestamp: eventData.timestamp || new Date().toISOString(),
      data: eventData.data || {},
      source: eventData.source || 'system'
    });

    // Limit timeline events to max
    if (this.timelineEvents.length > this.timelineMaxEvents) {
      this.timelineEvents = this.timelineEvents.slice(0, this.timelineMaxEvents);
    }

    this.draw();
  }

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

  toggleTimeline() {
    this.timelineVisible = !this.timelineVisible;
    this.timelinePanelAnimating = true;
    this.animateTimelinePanel(this.timelineVisible);
  }

  animateTimelinePanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.timelinePanelWidth = Math.min(
          this.timelinePanelWidth + speed,
          this.timelinePanelTargetWidth
        );

        if (this.timelinePanelWidth >= this.timelinePanelTargetWidth) {
          this.timelinePanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.timelinePanelWidth = Math.max(this.timelinePanelWidth - speed, 0);

        if (this.timelinePanelWidth <= 0) {
          this.timelinePanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      }

      this.draw();
    };

    requestAnimationFrame(animate);
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

  createTaskParticles(task) {
    const fromAgent = this.agents.find(a => a.name === task.from);
    const toAgent = this.agents.find(a => a.name === task.to);

    if (fromAgent && toAgent) {
      for (let i = 0; i < 20; i++) {
        this.particles.push({
          x: fromAgent.x,
          y: fromAgent.y,
          targetX: toAgent.x,
          targetY: toAgent.y,
          progress: 0,
          speed: 0.01 + Math.random() * 0.02,
          size: 2 + Math.random() * 3,
          color: fromAgent.color,
          alpha: 1
        });
      }
    }
  }

  startAnimation() {
    const animate = () => {
      this.update();
      this.draw();
      this.animationFrame = requestAnimationFrame(animate);
    };
    animate();
  }

  update() {
    // Skip updates if animation is paused
    if (this.animationPaused) return;

    // Update task progress
    this.tasks.forEach(task => {
      if (task.status === 'in_progress' && task.progress < 100) {
        task.progress += 0.5;
      }
    });

    // Update particles
    this.particles = this.particles.filter(p => {
      p.progress += p.speed;
      p.x = p.x + (p.targetX - p.x) * p.progress;
      p.y = p.y + (p.targetY - p.y) * p.progress;
      p.alpha = 1 - p.progress;
      return p.progress < 1;
    });

    // Update chain particles
    this.updateChainParticles();

    // Update agent pulse
    this.agents.forEach(agent => {
      agent.pulsePhase += 0.05;
    });
  }

  draw() {
    // Clear canvas with selected background color
    this.ctx.fillStyle = this.backgroundColor;
    this.ctx.fillRect(0, 0, this.width, this.height);

    this.ctx.save();
    this.ctx.translate(this.offsetX, this.offsetY);
    this.ctx.scale(this.scale, this.scale);

    // Draw connections between agents (disabled)
    // this.drawConnections();

    // Draw chain connections (highlighted paths for active chains)
    this.drawChainConnections();

    // Draw task flows
    this.drawTaskFlows();

    // Draw particles
    this.drawParticles();

    // Draw chain particles
    this.drawChainParticles();

    // Draw agents
    this.drawAgents();

    // Normalize combiner inputs before drawing connections/nodes
    if (this.combinerNodes.length) {
      this.combinerNodes.forEach(node => this.cleanupCombinerInputPorts(node, true));
    }

    // Draw workflow connections (between agents and combiners)
    this.drawWorkflowConnections();

    // Draw combiner nodes
    this.drawCombinerNodes();

    // Draw dragging connection line (if dragging)
    if (this.isDraggingConnection && this.connectionDragStart) {
      this.drawDraggingConnection();
    }

    this.ctx.restore();

    // Draw mission OUTSIDE the transform context (so it stays fixed at top)
    this.drawMission();

    // Draw expanded task panel OUTSIDE the transform context (fixed position)
    if (this.expandedPanelWidth > 0) {
      this.drawExpandedTaskPanel();
    }

    // Draw expanded agent panel OUTSIDE the transform context (fixed position)
    if (this.expandedAgentPanelWidth > 0) {
      this.drawExpandedAgentPanel();
    }

    // Draw expanded combiner panel OUTSIDE the transform context (fixed position)
    if (this.expandedCombinerPanelWidth > 0) {
      this.drawExpandedCombinerPanel();
    }

    // Draw assignment line
    if (this.assignmentMode && this.assignmentSourceTask) {
      this.drawAssignmentLine();
    }

    // Draw create task button (always visible)
    this.drawCreateTaskButton();

    // Draw add agent button (always visible)
    this.drawAddAgentButton();

    // Draw timeline panel (fixed position)
    if (this.timelinePanelWidth > 0) {
      this.drawTimelinePanel();
    }

    // Draw timeline toggle button (always visible)
    this.drawTimelineToggleButton();

    // Draw auto-layout button (always visible)
    this.drawAutoLayoutButton();

    // Draw save layout button (always visible)
    this.drawSaveLayoutButton();

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
    this.drawNotifications();

    // Draw context menu (if visible)
    if (this.contextMenuVisible) {
      this.drawContextMenu();
    }

    // Draw help overlay (if visible)
    if (this.helpOverlayVisible) {
      this.drawHelpOverlay();
    }
  }

  drawConnections() {
    this.ctx.strokeStyle = 'rgba(0,0,0,0.05)';
    this.ctx.lineWidth = 1;

    for (let i = 0; i < this.agents.length; i++) {
      for (let j = i + 1; j < this.agents.length; j++) {
        this.ctx.beginPath();
        this.ctx.moveTo(this.agents[i].x, this.agents[i].y);
        this.ctx.lineTo(this.agents[j].x, this.agents[j].y);
        this.ctx.stroke();
      }
    }
  }

  /**
   * Draw an arrow from (x1, y1) to (x2, y2)
   */
  drawArrow(x1, y1, x2, y2, color, lineWidth = 2, filled = true) {
    const headLength = 20; // Length of arrow head
    const headAngle = Math.PI / 6; // Angle of arrow head (30 degrees)

    // Calculate angle
    const angle = Math.atan2(y2 - y1, x2 - x1);

    // Draw the line (respects current dash pattern)
    this.ctx.beginPath();
    this.ctx.moveTo(x1, y1);
    this.ctx.lineTo(x2, y2);
    this.ctx.strokeStyle = color;
    this.ctx.lineWidth = lineWidth;
    this.ctx.stroke();

    // Save current dash pattern
    const currentDash = this.ctx.getLineDash();

    // Draw the arrow head (always solid for visibility)
    this.ctx.setLineDash([]);

    if (filled) {
      // Filled arrowhead
      this.ctx.beginPath();
      this.ctx.moveTo(x2, y2);
      this.ctx.lineTo(
        x2 - headLength * Math.cos(angle - headAngle),
        y2 - headLength * Math.sin(angle - headAngle)
      );
      this.ctx.lineTo(
        x2 - headLength * Math.cos(angle + headAngle),
        y2 - headLength * Math.sin(angle + headAngle)
      );
      this.ctx.closePath();
      this.ctx.fillStyle = color;
      this.ctx.fill();
    } else {
      // Outlined arrowhead
      this.ctx.beginPath();
      this.ctx.moveTo(x2, y2);
      this.ctx.lineTo(
        x2 - headLength * Math.cos(angle - headAngle),
        y2 - headLength * Math.sin(angle - headAngle)
      );
      this.ctx.moveTo(x2, y2);
      this.ctx.lineTo(
        x2 - headLength * Math.cos(angle + headAngle),
        y2 - headLength * Math.sin(angle + headAngle)
      );
      this.ctx.strokeStyle = color;
      this.ctx.lineWidth = lineWidth + 1;
      this.ctx.stroke();
    }

    // Restore dash pattern
    this.ctx.setLineDash(currentDash);
  }

  drawTaskFlows() {
    if (!this.tasks || this.tasks.length === 0) return;

    this.tasks.forEach((task, index) => {
      const fromAgent = this.agents.find(a => a.name === task.from);
      const toAgent = this.agents.find(a => a.name === task.to);

      // Handle unassigned tasks (to: "unassigned")
      const isUnassigned = task.to === 'unassigned';

      // Skip if target agent not found AND not unassigned
      if (!toAgent && !isUnassigned) return;

      // Handle system/user-created tasks (no from agent)
      const isSystemTask = !fromAgent || task.from === 'system' || task.from === 'user';

      // Calculate default position if task doesn't have one
      if (task.x == null || task.y == null) {  // Use == to catch both null and undefined
        if (isUnassigned) {
          // Position unassigned tasks in the top-left area
          const offsetX = 100 + (index % 3) * 180;
          const offsetY = 100 + (Math.floor(index / 3) % 3) * 80;
          task.x = offsetX;
          task.y = offsetY;
        } else if (isSystemTask) {
          // Position near the target agent
          const offsetX = 100 + (index % 3) * 50;
          const offsetY = -100 + (Math.floor(index / 3) % 3) * 70;
          task.x = toAgent.x + offsetX;
          task.y = toAgent.y + offsetY;
        } else {
          // Position task card between agents, but higher up to avoid overlap
          const midX = (fromAgent.x + toAgent.x) / 2;
          const midY = (fromAgent.y + toAgent.y) / 2;

          // Move task cards up by 80 pixels to avoid overlapping with agent nodes
          const cardOffsetY = -80;

          // Offset multiple tasks slightly if they share the same from/to agents
          const offsetY = (index % 3 - 1) * 70 + cardOffsetY;

          task.x = midX;
          task.y = midY + offsetY;
        }
      }

      // Draw connection line from sender to task (if not a system task)
      if (!isSystemTask && fromAgent) {
        const color = fromAgent.color + 'DD'; // More opaque (87% opacity)
        this.ctx.setLineDash([5, 5]);
        // Calculate shortened end point to avoid hiding arrowhead behind task card
        const angle = Math.atan2(task.y - fromAgent.y, task.x - fromAgent.x);
        const agentHalfW = (fromAgent.width || 120) / 2;
        const agentHalfH = (fromAgent.height || 70) / 2;
        const agentRadius = Math.hypot(agentHalfW, agentHalfH);
        const taskCardRadius = 80; // Approximate diagonal of task card
        const x1 = fromAgent.x + agentRadius * Math.cos(angle);
        const y1 = fromAgent.y + agentRadius * Math.sin(angle);
        const x2 = task.x - taskCardRadius * Math.cos(angle);
        const y2 = task.y - taskCardRadius * Math.sin(angle);
        this.drawArrow(x1, y1, x2, y2, color, 3);
        this.ctx.setLineDash([]);
      }

      // Draw connection line from task to receiver (skip for unassigned tasks)
      if (toAgent && !isUnassigned) {
        const color = toAgent.color + 'DD'; // More opaque (87% opacity)
        this.ctx.setLineDash([5, 5]);
        // Calculate shortened end point to avoid hiding arrowhead behind agent circle
        const angle = Math.atan2(toAgent.y - task.y, toAgent.x - task.x);
        const taskCardRadius = 80; // Approximate diagonal of task card
        const agentHalfW = (toAgent.width || 120) / 2;
        const agentHalfH = (toAgent.height || 70) / 2;
        const agentRadius = Math.hypot(agentHalfW, agentHalfH);
        const x1 = task.x + taskCardRadius * Math.cos(angle);
        const y1 = task.y + taskCardRadius * Math.sin(angle);
        const x2 = toAgent.x - agentRadius * Math.cos(angle);
        const y2 = toAgent.y - agentRadius * Math.sin(angle);
        this.drawArrow(x1, y1, x2, y2, color, 4);
        this.ctx.setLineDash([]);
      }

      // Draw task card
      const cardWidth = 160;
      const cardHeight = 60;
      const cardX = task.x - cardWidth / 2;
      const cardY = task.y - cardHeight / 2;

      // Store card bounds for hit testing
      task.cardBounds = { x: cardX, y: cardY, width: cardWidth, height: cardHeight };

      // Card background
      this.ctx.save();
      this.ctx.fillStyle = '#ffffff';
      this.ctx.shadowColor = 'rgba(0,0,0,0.15)';
      this.ctx.shadowBlur = 10;
      this.ctx.shadowOffsetY = 2;
      this.roundRect(cardX, cardY, cardWidth, cardHeight, 6);
      this.ctx.fill();
      this.ctx.restore();

      // Card border with status color
      let borderColor = '#6c757d'; // default gray
      if (task.status === 'pending') borderColor = '#ffc107'; // yellow
      else if (task.status === 'in_progress') borderColor = '#0d6efd'; // blue
      else if (task.status === 'completed') borderColor = '#198754'; // green
      else if (task.status === 'failed') borderColor = '#dc3545'; // red

      this.ctx.strokeStyle = borderColor;
      this.ctx.lineWidth = 2;
      this.ctx.beginPath();
      this.roundRect(cardX, cardY, cardWidth, cardHeight, 6);
      this.ctx.stroke();

      // Task description (use built-in maxWidth for reliable clipping)
      this.ctx.fillStyle = '#212529';
      this.ctx.font = 'bold 11px system-ui';
      const maxTextWidth = cardWidth - 32; // Reserve space for padding and delete button
      const description = task.description || 'Task';

      // Use canvas maxWidth parameter for automatic text truncation
      this.ctx.save();
      this.ctx.fillText(description, cardX + 8, cardY + 18, maxTextWidth);
      this.ctx.restore();

      // Task status - show connected node if unassigned, otherwise show from → to
      this.ctx.fillStyle = '#6c757d';
      this.ctx.font = '9px system-ui';

      let statusText;
      if (isUnassigned) {
        // Find output connection from this task (task is the source)
        const outputConn = this.connections.find(c => c.from === task.id);

        if (outputConn) {
          // Get the connected node (where the task output goes)
          const connectedNode = this.getNodeById(outputConn.to);

          if (connectedNode) {
            const nodeName = connectedNode.node.name || connectedNode.node.id || 'Unknown';
            statusText = `→ ${nodeName}`;
          } else {
            statusText = '⚠️ UNASSIGNED';
          }
        } else {
          statusText = '⚠️ UNASSIGNED';
        }
      } else {
        statusText = `${task.from} → ${task.to}`;
      }

      const maxStatusWidth = cardWidth - 16;

      // Use canvas maxWidth parameter for automatic text truncation
      this.ctx.save();
      this.ctx.fillText(statusText, cardX + 8, cardY + 34, maxStatusWidth);
      this.ctx.restore();

      // Status badge (left aligned)
      this.ctx.fillStyle = borderColor;
      this.ctx.font = 'bold 8px system-ui';
      const badge = (task.status || 'pending').toUpperCase();
      const badgeWidth = this.ctx.measureText(badge).width + 8;
      this.ctx.fillRect(cardX + 8, cardY + 40, badgeWidth, 12);
      this.ctx.fillStyle = '#ffffff';
      this.ctx.fillText(badge, cardX + 12, cardY + 49);

      // Delete button (always visible, top-right corner)
      const deleteBtnSize = 18;
      const deleteBtnX = cardX + cardWidth - deleteBtnSize - 4;
      const deleteBtnY = cardY + 4;

      // Store delete button bounds for click detection
      task.deleteBtnBounds = { x: deleteBtnX, y: deleteBtnY, width: deleteBtnSize, height: deleteBtnSize };

      // Delete button background
      this.ctx.fillStyle = '#dc3545';
      this.ctx.beginPath();
      this.ctx.arc(deleteBtnX + deleteBtnSize / 2, deleteBtnY + deleteBtnSize / 2, deleteBtnSize / 2, 0, Math.PI * 2);
      this.ctx.fill();

      // Delete button "X"
      this.ctx.strokeStyle = '#ffffff';
      this.ctx.lineWidth = 2;
      this.ctx.lineCap = 'round';
      const xOffset = 5;
      this.ctx.beginPath();
      this.ctx.moveTo(deleteBtnX + xOffset, deleteBtnY + xOffset);
      this.ctx.lineTo(deleteBtnX + deleteBtnSize - xOffset, deleteBtnY + deleteBtnSize - xOffset);
      this.ctx.moveTo(deleteBtnX + deleteBtnSize - xOffset, deleteBtnY + xOffset);
      this.ctx.lineTo(deleteBtnX + xOffset, deleteBtnY + deleteBtnSize - xOffset);
      this.ctx.stroke();

      // Clear bounds if task doesn't have result
      task.connectionIndicatorBounds = null;

      // Output port for connecting task to combiner nodes
      const outputPortRadius = 6;
      const outputPortX = task.x;
      const outputPortY = cardY + cardHeight + 5;

      // Draw output port circle
      this.ctx.fillStyle = '#6366f1'; // Indigo color
      this.ctx.strokeStyle = '#ffffff';
      this.ctx.lineWidth = 2;
      this.ctx.beginPath();
      this.ctx.arc(outputPortX, outputPortY, outputPortRadius, 0, Math.PI * 2);
      this.ctx.fill();
      this.ctx.stroke();

      // Store port bounds for connection detection
      task.outputPortBounds = {
        x: outputPortX - outputPortRadius,
        y: outputPortY - outputPortRadius,
        width: outputPortRadius * 2,
        height: outputPortRadius * 2
      };

      // Check if this task outputs to a combiner (if so, hide RUN button - combiner will execute it)
      const outputConn = this.connections.find(c => c.from === task.id);
      const outputsToCombiner = outputConn ? this.getNodeById(outputConn.to)?.type === 'combiner' : false;

      // Execute button for pending tasks (hide if outputs to combiner)
      if (task.status === 'pending' && !outputsToCombiner) {
        const btnWidth = 50;
        const btnHeight = 14;
        const btnX = cardX + cardWidth - btnWidth - 6;
        const btnY = cardY + 40;

        // Store button bounds for click detection
        task.executeBtnBounds = { x: btnX, y: btnY, width: btnWidth, height: btnHeight };

        // Button background
        this.ctx.fillStyle = '#28a745';
        this.roundRect(btnX, btnY, btnWidth, btnHeight, 3);
        this.ctx.fill();

        // Button text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 8px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText('▶ RUN', btnX + btnWidth / 2, btnY + 10);
        this.ctx.textAlign = 'left';
      }

      // Rerun button for completed or failed tasks
      if (task.status === 'completed' || task.status === 'failed') {
        const rerunBtnWidth = 50;
        const rerunBtnHeight = 14;
        const rerunBtnX = cardX + cardWidth - rerunBtnWidth - 6;
        const rerunBtnY = cardY + 40;

        // Store button bounds for click detection
        task.rerunBtnBounds = { x: rerunBtnX, y: rerunBtnY, width: rerunBtnWidth, height: rerunBtnHeight };

        // Button background (orange for rerun)
        this.ctx.fillStyle = task.status === 'failed' ? '#dc3545' : '#fd7e14';
        this.roundRect(rerunBtnX, rerunBtnY, rerunBtnWidth, rerunBtnHeight, 3);
        this.ctx.fill();

        // Button text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 8px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText('↻ RERUN', rerunBtnX + rerunBtnWidth / 2, rerunBtnY + 10);
        this.ctx.textAlign = 'left';
      }

      // Assign button (for all tasks except completed)
      if (task.status !== 'completed') {
        const assignBtnWidth = 50;
        const assignBtnHeight = 14;
        const assignBtnX = cardX + 6;
        const assignBtnY = cardY + 40;

        // Store button bounds for click detection
        task.assignBtnBounds = { x: assignBtnX, y: assignBtnY, width: assignBtnWidth, height: assignBtnHeight };

        // Button background (highlight if in assignment mode for this task)
        const isActiveAssignment = this.assignmentMode && this.assignmentSourceTask && this.assignmentSourceTask.id === task.id;
        this.ctx.fillStyle = isActiveAssignment ? '#fd7e14' : '#6c757d';
        this.roundRect(assignBtnX, assignBtnY, assignBtnWidth, assignBtnHeight, 3);
        this.ctx.fill();

        // Button text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 8px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText('➜ ASSIGN', assignBtnX + assignBtnWidth / 2, assignBtnY + 10);
        this.ctx.textAlign = 'left';
      }

      // View Log button (show if task has execution logs)
      const hasLogs = this.executionLogs[task.id] && this.executionLogs[task.id].length > 0;
      if (hasLogs || task.status === 'in_progress' || task.status === 'completed' || task.status === 'failed') {
        const logBtnWidth = 50;
        const logBtnHeight = 14;
        const logBtnX = cardX + 60; // After ASSIGN button
        const logBtnY = cardY + 40;

        // Store button bounds for click detection
        task.viewLogBtnBounds = { x: logBtnX, y: logBtnY, width: logBtnWidth, height: logBtnHeight };

        // Button background
        const logColor = hasLogs ? '#17a2b8' : '#adb5bd'; // Teal if logs exist, gray otherwise
        this.ctx.fillStyle = logColor;
        this.roundRect(logBtnX, logBtnY, logBtnWidth, logBtnHeight, 3);
        this.ctx.fill();

        // Button text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 8px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText('📋 LOG', logBtnX + logBtnWidth / 2, logBtnY + 10);
        this.ctx.textAlign = 'left';
      }

      // Progress bar for in_progress tasks
      if (task.status === 'in_progress') {
        // Calculate elapsed time
        let elapsedMs = 0;
        if (task.started_at) {
          elapsedMs = Date.now() - new Date(task.started_at).getTime();
        }

        // Use progress data if available, otherwise show indeterminate progress
        const hasProgress = task.progress && task.progress.percentage !== undefined;
        const percentage = hasProgress ? task.progress.percentage : 0;

        // Progress bar position (bottom of card)
        const progressBarY = cardY + cardHeight - 18;
        const progressBarWidth = cardWidth - 16;
        const progressBarHeight = 4;

        // Progress bar background
        this.ctx.fillStyle = '#e5e7eb';
        this.ctx.fillRect(cardX + 8, progressBarY, progressBarWidth, progressBarHeight);

        if (hasProgress) {
          // Determinate progress bar
          const fillWidth = (progressBarWidth * percentage) / 100;
          this.ctx.fillStyle = '#3b82f6';
          this.ctx.fillRect(cardX + 8, progressBarY, fillWidth, progressBarHeight);

          // Percentage text
          this.ctx.fillStyle = '#3b82f6';
          this.ctx.font = 'bold 8px system-ui';
          this.ctx.fillText(`${percentage}%`, cardX + 8, progressBarY - 2);
        } else {
          // Indeterminate progress - animated bar
          const animOffset = (Date.now() / 20) % progressBarWidth;
          const barWidth = progressBarWidth * 0.3;

          this.ctx.fillStyle = '#3b82f6';
          this.ctx.fillRect(cardX + 8 + animOffset - barWidth, progressBarY, barWidth, progressBarHeight);
        }

        // Elapsed time (right side)
        const elapsedSeconds = Math.floor(elapsedMs / 1000);
        const minutes = Math.floor(elapsedSeconds / 60);
        const seconds = elapsedSeconds % 60;
        const timeText = minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;

        this.ctx.fillStyle = '#6b7280';
        this.ctx.font = '8px system-ui';
        this.ctx.textAlign = 'right';
        this.ctx.fillText(`⏱️ ${timeText}`, cardX + cardWidth - 8, progressBarY - 2);
        this.ctx.textAlign = 'left';

        // Current step (if available)
        if (task.progress && task.progress.current_step) {
          this.ctx.fillStyle = '#6b7280';
          this.ctx.font = '7px system-ui';
          const stepText = task.progress.current_step.substring(0, 20) + (task.progress.current_step.length > 20 ? '...' : '');
          this.ctx.fillText(stepText, cardX + 8, cardY + cardHeight - 4);
        }
      }
    });

    // (Result-to-task connections hidden for clarity)
  }

  /**
   * Draw connections from completed tasks to tasks that use their results
   */
  drawResultConnections() {
    if (!this.tasks || this.tasks.length === 0) return;

    this.tasks.forEach(task => {
      // Check if this task has input tasks
      if (!task.input_task_ids || task.input_task_ids.length === 0) return;

      // Draw connection from each input task to this task
      task.input_task_ids.forEach(inputTaskId => {
        const inputTask = this.tasks.find(t => t.id === inputTaskId);
        if (!inputTask || !inputTask.x || !inputTask.y) return;

        // Draw a more prominent line with glow effect to indicate result flow
        this.ctx.save();

        // Offset arrow so the head doesn't sit on top of the task card
        const angle = Math.atan2(task.y - inputTask.y, task.x - inputTask.x);
        const startOffset = 30; // move start off input task center a bit
        const endOffset = 80;   // stop before target card center
        const startX = inputTask.x + startOffset * Math.cos(angle);
        const startY = inputTask.y + startOffset * Math.sin(angle);
        const endX = task.x - endOffset * Math.cos(angle);
        const endY = task.y - endOffset * Math.sin(angle);

        // Draw softened line (no arrowhead) for result flow
        this.ctx.strokeStyle = 'rgba(155, 89, 182, 0.35)';
        this.ctx.lineWidth = 2;
        this.ctx.setLineDash([6, 10]);
        this.ctx.beginPath();
        this.ctx.moveTo(startX, startY);
        this.ctx.lineTo(endX, endY);
        this.ctx.stroke();
        this.ctx.setLineDash([]);
        this.ctx.restore();
      });
    });
  }

  drawParticles() {
    this.particles.forEach(p => {
      this.ctx.fillStyle = p.color + Math.floor(p.alpha * 255).toString(16).padStart(2, '0');
      this.ctx.beginPath();
      this.ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2);
      this.ctx.fill();
    });
  }

  /**
   * Draw highlighted connection paths for active chains
   */
  drawChainConnections() {
    if (!this.activeChains || this.activeChains.length === 0) return;

    this.activeChains.forEach(chain => {
      if (!chain.from || !chain.to || chain.from.x == null || chain.to.x == null) return;

      const fromX = chain.from.x;
      const fromY = chain.from.y;
      const toX = chain.to.x;
      const toY = chain.to.y;

      // Determine color based on chain state
      let color, width, glow;
      if (chain.failed) {
        color = '#ef4444';
        width = 3;
        glow = 8;
      } else if (chain.completed) {
        color = '#10b981';
        width = 3;
        glow = 6;
      } else if (chain.active) {
        color = '#3b82f6';
        width = 4;
        glow = 10;
      } else {
        color = '#6b7280';
        width = 2;
        glow = 0;
      }

      // Draw glowing line
      if (glow > 0) {
        this.ctx.shadowColor = color;
        this.ctx.shadowBlur = glow;
      }

      this.ctx.strokeStyle = color;
      this.ctx.lineWidth = width;
      this.ctx.lineCap = 'round';

      // Draw curved line
      const midX = (fromX + toX) / 2;
      const midY = (fromY + toY) / 2;
      const dx = toX - fromX;
      const dy = toY - fromY;
      const dist = Math.sqrt(dx * dx + dy * dy);
      const controlOffset = dist * 0.2;

      // Perpendicular offset for curve
      const perpX = -dy / dist * controlOffset;
      const perpY = dx / dist * controlOffset;

      this.ctx.beginPath();
      this.ctx.moveTo(fromX, fromY);
      this.ctx.quadraticCurveTo(
        midX + perpX,
        midY + perpY,
        toX,
        toY
      );
      this.ctx.stroke();

      this.ctx.shadowColor = 'transparent';
      this.ctx.shadowBlur = 0;

      // Draw arrow head at destination
      const angle = Math.atan2(toY - (midY + perpY), toX - (midX + perpX));
      const arrowSize = 10;

      this.ctx.fillStyle = color;
      this.ctx.beginPath();
      this.ctx.moveTo(toX, toY);
      this.ctx.lineTo(
        toX - arrowSize * Math.cos(angle - Math.PI / 6),
        toY - arrowSize * Math.sin(angle - Math.PI / 6)
      );
      this.ctx.lineTo(
        toX - arrowSize * Math.cos(angle + Math.PI / 6),
        toY - arrowSize * Math.sin(angle + Math.PI / 6)
      );
      this.ctx.closePath();
      this.ctx.fill();

      // Draw chain progress indicator for active chains
      if (chain.active && !chain.completed) {
        this.ctx.fillStyle = color;
        this.ctx.font = 'bold 10px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.textBaseline = 'middle';
        this.ctx.fillText('⚡', midX + perpX, midY + perpY);
      }
    });
  }

  /**
   * Draw chain particles
   */
  drawChainParticles() {
    if (!this.chainParticles || this.chainParticles.length === 0) return;

    this.chainParticles.forEach(p => {
      const alphaHex = Math.floor(p.alpha * 255).toString(16).padStart(2, '0');
      this.ctx.fillStyle = p.color + alphaHex;

      // Add glow effect
      this.ctx.shadowColor = p.color;
      this.ctx.shadowBlur = 8;

      this.ctx.beginPath();
      this.ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2);
      this.ctx.fill();

      this.ctx.shadowColor = 'transparent';
      this.ctx.shadowBlur = 0;
    });
  }

  drawAgents() {
    this.agents.forEach(agent => {
      const halfWidth = (agent.width || 120) / 2;
      const halfHeight = (agent.height || 70) / 2;

      // Draw enhanced pulse effect for active/busy agents
      if (agent.status === 'active' || agent.status === 'busy') {
        const grow = 10 + 6 * Math.sin(agent.pulsePhase);
        const glowAlpha = 0.22 + 0.18 * Math.sin(agent.pulsePhase);
        const glowColor = agent.status === 'active'
          ? `rgba(16, 185, 129, ${glowAlpha})`
          : `rgba(245, 158, 11, ${glowAlpha})`;

        this.ctx.save();
        this.ctx.fillStyle = glowColor;
        this.ctx.shadowColor = glowColor;
        this.ctx.shadowBlur = 12;
        this.roundRect(
          agent.x - halfWidth - grow,
          agent.y - halfHeight - grow,
          (halfWidth * 2) + grow * 2,
          (halfHeight * 2) + grow * 2,
          14
        );
        this.ctx.fill();
        this.ctx.restore();
      }

      // Draw agent rectangle
      this.ctx.fillStyle = agent.color;
      this.ctx.shadowColor = 'rgba(0,0,0,0.12)';
      this.ctx.shadowBlur = 10;
      this.roundRect(agent.x - halfWidth, agent.y - halfHeight, halfWidth * 2, halfHeight * 2, 12);
      this.ctx.fill();
      this.ctx.shadowColor = 'transparent';

      // Draw workflow ports to support incoming/outgoing connections
      // Inputs point into the agent (down), outputs point outward (down from the node)
      this.drawPort(agent.x, agent.y - halfHeight - 10, 'input', agent.color, 'down');
      this.drawPort(agent.x, agent.y + halfHeight + 10, 'output', agent.color, 'down');

      // Draw status indicator
      let statusColor;
      switch (agent.status) {
        case 'active': statusColor = '#10b981'; break;  // Green - actively executing
        case 'busy': statusColor = '#f59e0b'; break;    // Orange - has queued tasks
        case 'error': statusColor = '#ef4444'; break;   // Red - error state
        case 'queued': statusColor = '#3b82f6'; break;  // Blue - tasks queued
        default: statusColor = '#6b7280';               // Gray - idle
      }
      this.ctx.fillStyle = statusColor;
      this.ctx.beginPath();
      this.ctx.arc(agent.x + halfWidth - 10, agent.y - halfHeight + 10, 6, 0, Math.PI * 2);
      this.ctx.fill();

      // Draw agent name
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.textAlign = 'center';
      this.ctx.textBaseline = 'middle';

      // If there's a result, move name up to make room
      const nameY = agent.lastResult ? agent.y - 15 : agent.y;
      this.ctx.fillText(agent.name, agent.x, nameY);

      // Draw last result (if available) - PROMINENT DISPLAY
      if (agent.lastResult) {
        // Result background container
        const resultText = agent.lastResult.toString();
        const maxWidth = 150;

        // Measure text to determine background size
        this.ctx.font = 'bold 13px system-ui';
        let displayText = resultText;
        let metrics = this.ctx.measureText(displayText);

        // Truncate if needed
        if (metrics.width > maxWidth) {
          while (metrics.width > maxWidth && displayText.length > 3) {
            displayText = resultText.substring(0, displayText.length - 4) + '...';
            metrics = this.ctx.measureText(displayText);
          }
        }

        // Draw result container
        const padding = 8;
        const resultBoxWidth = metrics.width + padding * 2;
        const resultBoxHeight = 24;
        const resultBoxX = agent.x - resultBoxWidth / 2;
        const resultBoxY = agent.y + 5;

        // Background with gradient
        const gradient = this.ctx.createLinearGradient(
          resultBoxX, resultBoxY,
          resultBoxX, resultBoxY + resultBoxHeight
        );
        gradient.addColorStop(0, 'rgba(16, 185, 129, 0.9)'); // Success green
        gradient.addColorStop(1, 'rgba(5, 150, 105, 0.9)');

        this.ctx.save();
        this.ctx.fillStyle = gradient;
        this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
        this.ctx.shadowBlur = 8;
        this.ctx.shadowOffsetY = 2;
        this.roundRect(resultBoxX, resultBoxY, resultBoxWidth, resultBoxHeight, 4);
        this.ctx.fill();
        this.ctx.restore();

        // Result text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 13px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.textBaseline = 'middle';
        this.ctx.fillText(displayText, agent.x, resultBoxY + resultBoxHeight / 2);
      }

      // Draw task count badge
      const currentTaskCount = agent.currentTasks?.length || 0;
      const queuedTaskCount = agent.queuedTasks?.length || 0;
      const totalTaskCount = currentTaskCount + queuedTaskCount;

      if (totalTaskCount > 0) {
        // Badge background
        const badgeX = agent.x + ((agent.width || 120) / 2) - 5;
        const badgeY = agent.y + ((agent.height || 70) / 2) - 5;
        const badgeRadius = 12;

        this.ctx.fillStyle = statusColor;
        this.ctx.beginPath();
        this.ctx.arc(badgeX, badgeY, badgeRadius, 0, Math.PI * 2);
        this.ctx.fill();

        // Badge border
        this.ctx.strokeStyle = '#ffffff';
        this.ctx.lineWidth = 2;
        this.ctx.stroke();

        // Badge text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 10px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.textBaseline = 'middle';
        this.ctx.fillText(totalTaskCount.toString(), badgeX, badgeY);

        // Task breakdown below agent
        this.ctx.font = '10px system-ui';
        this.ctx.fillStyle = '#9ca3af';
        let taskText = '';
        if (currentTaskCount > 0 && queuedTaskCount > 0) {
          taskText = `${currentTaskCount} running, ${queuedTaskCount} queued`;
        } else if (currentTaskCount > 0) {
          taskText = `${currentTaskCount} running`;
        } else if (queuedTaskCount > 0) {
          taskText = `${queuedTaskCount} queued`;
        }
        if (taskText) {
          this.ctx.fillText(taskText, agent.x, agent.y + ((agent.height || 70) / 2) + 15);
        }
      }
    });
  }

  drawWorkspaceProgress() {
    if (!this.workspaceProgress || this.workspaceProgress.total_tasks === 0) return;

    const panelWidth = Math.min(600, this.width * 0.8);
    const panelHeight = 95;
    const panelX = 100; // Move right to avoid overlapping with studio title
    const panelY = 100; // Move down to avoid studio title overlay
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
    this.ctx.strokeStyle = 'rgba(16, 185, 129, 0.6)'; // Green border
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.1)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 2;

    this.roundRect(panelX, panelY, panelWidth, panelHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();

    this.ctx.shadowColor = 'transparent';

    // Title
    this.ctx.fillStyle = '#10b981';
    this.ctx.font = 'bold 11px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';
    this.ctx.fillText('📊 WORKSPACE PROGRESS', panelX + padding, panelY + padding);

    // Task status text
    const statsY = panelY + padding + 18;
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '10px system-ui';
    let statusText = `${this.workspaceProgress.completed_tasks}/${this.workspaceProgress.total_tasks} tasks complete | ${this.workspaceProgress.in_progress_tasks} running | ${this.workspaceProgress.pending_tasks} pending`;
    if (this.workspaceProgress.failed_tasks > 0) {
      statusText += ` | ${this.workspaceProgress.failed_tasks} failed`;
    }
    this.ctx.fillText(statusText, panelX + padding, statsY);

    // Progress bar
    const progressBarY = panelY + padding + 36;
    const progressBarWidth = panelWidth - padding * 2;
    const progressBarHeight = 16; // Slightly taller for better visibility

    // Background
    this.ctx.fillStyle = '#e5e7eb';
    this.roundRect(panelX + padding, progressBarY, progressBarWidth, progressBarHeight, 6);
    this.ctx.fill();

    // Progress fill
    const fillWidth = (progressBarWidth * this.workspaceProgress.percentage) / 100;
    if (fillWidth > 0) {
      const gradient = this.ctx.createLinearGradient(panelX + padding, progressBarY, panelX + padding + fillWidth, progressBarY);
      gradient.addColorStop(0, '#10b981');
      gradient.addColorStop(1, '#059669');
      this.ctx.fillStyle = gradient;
      this.roundRect(panelX + padding, progressBarY, fillWidth, progressBarHeight, 6);
      this.ctx.fill();
    }

    // Percentage text on progress bar - smaller font
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 9px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText(`${this.workspaceProgress.percentage}%`, panelX + padding + progressBarWidth / 2, progressBarY + progressBarHeight / 2);

    // Bottom row: Agent status and estimated time
    const bottomY = panelY + padding + 58;
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';

    // Agent status
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '10px system-ui';
    const agentText = `Agents: ${this.workspaceProgress.active_agents} active | ${this.workspaceProgress.idle_agents} idle`;
    this.ctx.fillText(agentText, panelX + padding, bottomY);

    // Estimated time remaining
    if (this.workspaceProgress.remaining_time_ms && this.workspaceProgress.remaining_time_ms > 0) {
      this.ctx.textAlign = 'right';
      const minutes = Math.ceil(this.workspaceProgress.remaining_time_ms / 60000);
      const seconds = Math.ceil((this.workspaceProgress.remaining_time_ms % 60000) / 1000);
      let timeText = '';
      if (minutes > 0) {
        timeText = `Est. ${minutes}m ${seconds}s remaining`;
      } else {
        timeText = `Est. ${seconds}s remaining`;
      }
      this.ctx.fillText(timeText, panelX + panelWidth - padding, bottomY);
    }

    this.ctx.restore();
  }

  drawMission() {
    if (!this.mission) return;

    // Calculate center of canvas in world coordinates
    const centerX = this.width / 2;
    const centerY = this.height / 2;

    // Draw mission background box
    this.ctx.save();

    // Measure text to size the box appropriately
    this.ctx.font = 'bold 18px system-ui';
    const maxWidth = this.width * 0.6; // Max 60% of canvas width
    const lines = this.wrapText(this.mission, maxWidth);
    const lineHeight = 26;
    const totalHeight = lines.length * lineHeight + 30;
    const boxWidth = Math.min(maxWidth + 40, this.width * 0.7);
    const boxHeight = totalHeight;

    // Position at top center
    const boxX = centerX - boxWidth / 2;
    const boxY = 40;

    // Draw semi-transparent background with border
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
    this.ctx.strokeStyle = 'rgba(59, 130, 246, 0.8)'; // Primary blue
    this.ctx.lineWidth = 3;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 10;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 2;

    // Rounded rectangle
    this.roundRect(boxX, boxY, boxWidth, boxHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();

    this.ctx.shadowColor = 'transparent';

    // Draw "MISSION" label
    this.ctx.fillStyle = '#3b82f6';
    this.ctx.font = 'bold 12px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';
    this.ctx.fillText('🎯 MISSION', boxX + 20, boxY + 12);

    // Draw mission text
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = '16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';

    lines.forEach((line, i) => {
      this.ctx.fillText(line, boxX + 20, boxY + 40 + i * lineHeight);
    });

    this.ctx.restore();
  }

  // Helper function to wrap text
  wrapText(text, maxWidth) {
    const words = text.split(' ');
    const lines = [];
    let currentLine = words[0];

    this.ctx.font = '16px system-ui';

    for (let i = 1; i < words.length; i++) {
      const testLine = currentLine + ' ' + words[i];
      const metrics = this.ctx.measureText(testLine);

      if (metrics.width > maxWidth - 40) {
        lines.push(currentLine);
        currentLine = words[i];
      } else {
        currentLine = testLine;
      }
    }
    lines.push(currentLine);
    return lines;
  }

  drawExpandedTaskPanel() {
    if (!this.expandedTask) return;

    const panelX = this.width - this.expandedPanelWidth;
    const panelY = 0;
    const panelHeight = this.height;

    // Draw panel background with shadow
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.expandedPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.expandedPanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const padding = 20;
    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.expandedPanelWidth - padding, currentY + 20);
    currentY += 40;

    // Task title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Task Details', contentX, currentY);
    currentY += 30;

    // Status badge
    let statusColor = '#6b7280';
    if (this.expandedTask.status === 'completed') statusColor = '#10b981';
    else if (this.expandedTask.status === 'in_progress') statusColor = '#3b82f6';
    else if (this.expandedTask.status === 'failed') statusColor = '#ef4444';
    else if (this.expandedTask.status === 'pending') statusColor = '#f59e0b';

    this.ctx.fillStyle = statusColor;
    this.ctx.font = 'bold 10px system-ui';
    const statusText = (this.expandedTask.status || 'pending').toUpperCase();
    const statusWidth = this.ctx.measureText(statusText).width + 12;
    this.roundRect(contentX, currentY, statusWidth, 18, 9);
    this.ctx.fill();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.fillText(statusText, contentX + 6, currentY + 13);
    currentY += 30;

    // Description
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText('Description:', contentX, currentY);
    currentY += 20;

    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = '13px system-ui';
    const descLines = this.wrapText(this.expandedTask.description || '', this.expandedPanelWidth - padding * 2);
    descLines.forEach(line => {
      this.ctx.fillText(line, contentX, currentY);
      currentY += 18;
    });
    currentY += 15;

    // Agents
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText(`From: ${this.expandedTask.from}  →  To: ${this.expandedTask.to}`, contentX, currentY);
    currentY += 25;

    // Progress section (for in_progress tasks)
    if (this.expandedTask.status === 'in_progress') {
      this.ctx.fillStyle = '#3b82f6';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('⏳ Progress', contentX, currentY);
      currentY += 25;

      // Calculate elapsed time
      let elapsedMs = 0;
      if (this.expandedTask.started_at) {
        elapsedMs = Date.now() - new Date(this.expandedTask.started_at).getTime();
      }

      // Progress box
      const progressBoxHeight = 100;
      this.ctx.fillStyle = '#eff6ff';
      this.ctx.strokeStyle = '#3b82f6';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, currentY, this.expandedPanelWidth - padding * 2, progressBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      let progressY = currentY + 20;

      // Percentage or indeterminate
      const hasProgress = this.expandedTask.progress && this.expandedTask.progress.percentage !== undefined;
      if (hasProgress) {
        const percentage = this.expandedTask.progress.percentage;

        // Progress bar
        const barWidth = this.expandedPanelWidth - padding * 2 - 40;
        const barHeight = 12;
        const barX = contentX + 20;

        this.ctx.fillStyle = '#dbeafe';
        this.roundRect(barX, progressY, barWidth, barHeight, 6);
        this.ctx.fill();

        const fillWidth = (barWidth * percentage) / 100;
        this.ctx.fillStyle = '#3b82f6';
        this.roundRect(barX, progressY, fillWidth, barHeight, 6);
        this.ctx.fill();

        // Percentage text
        this.ctx.fillStyle = '#1e40af';
        this.ctx.font = 'bold 14px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText(`${percentage}%`, barX + barWidth / 2, progressY + barHeight + 18);
        this.ctx.textAlign = 'left';

        progressY += 40;

        // Current step
        if (this.expandedTask.progress.current_step) {
          this.ctx.fillStyle = '#1e3a8a';
          this.ctx.font = '11px system-ui';
          const stepLines = this.wrapText(this.expandedTask.progress.current_step, this.expandedPanelWidth - padding * 2 - 40);
          stepLines.forEach(line => {
            this.ctx.fillText(line, contentX + 20, progressY);
            progressY += 14;
          });
        }
      } else {
        // No specific progress - show elapsed time only
        this.ctx.fillStyle = '#1e3a8a';
        this.ctx.font = '13px system-ui';
        this.ctx.fillText('Running...', contentX + 20, progressY);
      }

      // Elapsed time
      const elapsedSeconds = Math.floor(elapsedMs / 1000);
      const minutes = Math.floor(elapsedSeconds / 60);
      const seconds = elapsedSeconds % 60;
      const timeText = minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;

      this.ctx.fillStyle = '#6b7280';
      this.ctx.font = '11px system-ui';
      this.ctx.fillText(`Elapsed: ${timeText}`, contentX + 20, currentY + progressBoxHeight - 10);

      currentY += progressBoxHeight + 20;
    }

    // Separator line
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.expandedPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Result section
    if (this.expandedTask.result) {
      this.ctx.fillStyle = '#059669';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📊 Result', contentX, currentY);

      // Copy button
      const copyButtonWidth = 80;
      const copyButtonHeight = 24;
      const copyButtonX = panelX + this.expandedPanelWidth - padding - copyButtonWidth;
      const copyButtonY = currentY - 18;

      // Store bounds for click detection
      this.copyButtonBounds = {
        x: copyButtonX,
        y: copyButtonY,
        width: copyButtonWidth,
        height: copyButtonHeight
      };

      // Button background
      if (this.copyButtonState === 'copied') {
        this.ctx.fillStyle = '#10b981';
      } else if (this.copyButtonState === 'hover') {
        this.ctx.fillStyle = '#059669';
      } else {
        this.ctx.fillStyle = '#047857';
      }
      this.ctx.strokeStyle = '#065f46';
      this.ctx.lineWidth = 1.5;
      this.roundRect(copyButtonX, copyButtonY, copyButtonWidth, copyButtonHeight, 4);
      this.ctx.fill();
      this.ctx.stroke();

      // Button text
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 11px system-ui';
      this.ctx.textAlign = 'center';
      const buttonText = this.copyButtonState === 'copied' ? '✓ Copied!' : '📋 Copy';
      this.ctx.fillText(buttonText, copyButtonX + copyButtonWidth / 2, copyButtonY + copyButtonHeight / 2 + 4);
      this.ctx.textAlign = 'left';

      currentY += 25;

      // Result background box
      const resultBoxY = currentY;
      const resultBoxHeight = Math.min(300, panelHeight - currentY - padding);
      const resultBoxWidth = this.expandedPanelWidth - padding * 2;

      // Store bounds for scroll detection
      this.resultBoxBounds = {
        x: panelX + padding,
        y: resultBoxY,
        width: resultBoxWidth,
        height: resultBoxHeight
      };

      this.ctx.fillStyle = '#f0fdf4';
      this.ctx.strokeStyle = '#10b981';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, resultBoxY, resultBoxWidth, resultBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      // Result text with scrolling
      this.ctx.fillStyle = '#065f46';
      this.ctx.font = '11px monospace';
      const resultLines = this.wrapText(this.expandedTask.result, resultBoxWidth - 40); // Extra padding for scrollbar
      const lineHeight = 14;
      const visibleLines = Math.floor((resultBoxHeight - 20) / lineHeight);
      const totalLines = resultLines.length;

      // Clamp scroll offset
      const maxScroll = Math.max(0, totalLines - visibleLines);
      this.resultScrollOffset = Math.max(0, Math.min(this.resultScrollOffset, maxScroll));

      // Enable clipping to prevent text overflow
      this.ctx.save();
      this.ctx.beginPath();
      this.ctx.rect(contentX + 5, resultBoxY + 5, resultBoxWidth - 10, resultBoxHeight - 10);
      this.ctx.clip();

      // Render visible lines based on scroll offset
      const startLine = Math.floor(this.resultScrollOffset);
      const endLine = Math.min(startLine + visibleLines + 1, totalLines);

      resultLines.slice(startLine, endLine).forEach((line, i) => {
        const yPos = resultBoxY + 15 + (i * lineHeight) - ((this.resultScrollOffset - startLine) * lineHeight);
        this.ctx.fillText(line, contentX + 10, yPos);
      });

      this.ctx.restore();

      // Draw scrollbar if content is scrollable
      if (totalLines > visibleLines) {
        const scrollbarWidth = 8;
        const scrollbarHeight = (visibleLines / totalLines) * (resultBoxHeight - 20);
        const scrollbarY = resultBoxY + 10 + (this.resultScrollOffset / maxScroll) * (resultBoxHeight - 20 - scrollbarHeight);

        // Scrollbar track
        this.ctx.fillStyle = 'rgba(16, 185, 129, 0.1)';
        this.ctx.fillRect(contentX + resultBoxWidth - scrollbarWidth - 5, resultBoxY + 10, scrollbarWidth, resultBoxHeight - 20);

        // Scrollbar thumb
        this.ctx.fillStyle = 'rgba(16, 185, 129, 0.5)';
        this.ctx.fillRect(contentX + resultBoxWidth - scrollbarWidth - 5, scrollbarY, scrollbarWidth, scrollbarHeight);
      }
    } else if (this.expandedTask.error) {
      this.resultBoxBounds = null;
      this.copyButtonBounds = null;
      this.ctx.fillStyle = '#dc2626';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('❌ Error', contentX, currentY);
      currentY += 25;

      this.ctx.fillStyle = '#7f1d1d';
      this.ctx.font = '11px monospace';
      const errorLines = this.wrapText(this.expandedTask.error, this.expandedPanelWidth - padding * 2);
      errorLines.slice(0, 10).forEach(line => {
        this.ctx.fillText(line, contentX, currentY);
        currentY += 14;
      });
    } else {
      this.resultBoxBounds = null;
      this.copyButtonBounds = null;
      this.ctx.fillStyle = '#9ca3af';
      this.ctx.font = 'italic 12px system-ui';
      this.ctx.fillText('No result yet', contentX, currentY);
    }

    // Connection-to-agent flow removed; keep bounds null
    this.connectButtonBounds = null;

    this.ctx.restore();
  }

  drawExpandedAgentPanel() {
    if (!this.expandedAgent) return;

    const panelX = this.width - this.expandedAgentPanelWidth;
    const panelY = 0;
    const panelHeight = this.height;

    // Draw panel background with shadow
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.expandedAgentPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.expandedAgentPanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const padding = 20;
    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button (fixed, no scroll)
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.expandedAgentPanelWidth - padding, currentY + 20);
    currentY += 40;

    // Agent title (fixed, no scroll)
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Agent Details', contentX, currentY);
    currentY += 30;

    // Start scrollable content area
    const scrollableStartY = currentY;
    const scrollableHeight = panelHeight - scrollableStartY;

    // Enable clipping for scrollable area
    this.ctx.save();
    this.ctx.beginPath();
    this.ctx.rect(panelX, scrollableStartY, this.expandedAgentPanelWidth, scrollableHeight);
    this.ctx.clip();

    // Apply scroll offset
    currentY -= this.agentPanelScrollOffset;

    // Status badge
    let statusColor = '#6b7280';
    if (this.expandedAgent.status === 'active') statusColor = '#10b981';
    else if (this.expandedAgent.status === 'busy') statusColor = '#f59e0b';

    this.ctx.fillStyle = statusColor;
    this.ctx.font = 'bold 10px system-ui';
    const statusText = (this.expandedAgent.status || 'idle').toUpperCase();
    const statusWidth = this.ctx.measureText(statusText).width + 12;
    this.roundRect(contentX, currentY, statusWidth, 18, 9);
    this.ctx.fill();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.fillText(statusText, contentX + 6, currentY + 13);
    currentY += 30;

    // Agent name
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText('Name:', contentX, currentY);
    currentY += 18;

    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText(this.expandedAgent.name, contentX, currentY);
    currentY += 25;

    // Agent color indicator
    this.ctx.fillStyle = this.expandedAgent.color;
    this.roundRect(contentX, currentY, 30, 30, 15);
    this.ctx.fill();
    currentY += 40;

    // Last Result section (if available)
    if (this.expandedAgent.lastResult) {
      this.ctx.fillStyle = '#10b981'; // Green
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📊 Last Result', contentX, currentY);
      currentY += 20;

      // Result box
      const resultBoxWidth = this.expandedAgentPanelWidth - padding * 2;
      const resultText = this.expandedAgent.lastResult.toString();

      // Wrap text for long results
      const maxLineLength = 40;
      const resultLines = this.wrapText(resultText, resultBoxWidth - 20);
      const resultBoxHeight = Math.max(60, resultLines.length * 18 + 20);

      // Background gradient
      const gradient = this.ctx.createLinearGradient(
        contentX, currentY,
        contentX, currentY + resultBoxHeight
      );
      gradient.addColorStop(0, 'rgba(16, 185, 129, 0.1)');
      gradient.addColorStop(1, 'rgba(5, 150, 105, 0.15)');

      this.ctx.fillStyle = gradient;
      this.ctx.strokeStyle = '#10b981';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, currentY, resultBoxWidth, resultBoxHeight, 8);
      this.ctx.fill();
      this.ctx.stroke();

      // Result text
      this.ctx.fillStyle = '#065f46'; // Dark green
      this.ctx.font = 'bold 14px monospace';
      resultLines.forEach((line, index) => {
        this.ctx.fillText(line, contentX + 10, currentY + 20 + index * 18);
      });

      currentY += resultBoxHeight + 20;
    }

    // Activity Statistics section
    this.ctx.fillStyle = '#3b82f6';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText('📊 Activity Statistics', contentX, currentY);
    currentY += 20;

    // Statistics grid
    const stats = [
      { label: 'Current Tasks', value: this.expandedAgent.currentTasks?.length || 0, color: '#10b981' },
      { label: 'Queued Tasks', value: this.expandedAgent.queuedTasks?.length || 0, color: '#3b82f6' },
      { label: 'Completed', value: this.expandedAgent.completedTasks || 0, color: '#6b7280' },
      { label: 'Failed', value: this.expandedAgent.failedTasks || 0, color: '#ef4444' },
    ];

    stats.forEach((stat, index) => {
      // Stat box
      const statBoxWidth = (this.expandedAgentPanelWidth - padding * 2 - 10) / 2;
      const statBoxHeight = 50;
      const statBoxX = contentX + (index % 2) * (statBoxWidth + 10);
      const statBoxY = currentY + Math.floor(index / 2) * (statBoxHeight + 10);

      // Background
      this.ctx.fillStyle = '#f9fafb';
      this.ctx.strokeStyle = stat.color;
      this.ctx.lineWidth = 2;
      this.roundRect(statBoxX, statBoxY, statBoxWidth, statBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      // Value (large)
      this.ctx.fillStyle = stat.color;
      this.ctx.font = 'bold 24px system-ui';
      this.ctx.textAlign = 'center';
      this.ctx.fillText(stat.value.toString(), statBoxX + statBoxWidth / 2, statBoxY + 22);

      // Label (small)
      this.ctx.fillStyle = '#6b7280';
      this.ctx.font = '10px system-ui';
      this.ctx.fillText(stat.label, statBoxX + statBoxWidth / 2, statBoxY + 40);
    });

    this.ctx.textAlign = 'left'; // Reset
    currentY += 120;

    // Total executions
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '11px system-ui';
    const totalExec = this.expandedAgent.totalExecutions || 0;
    this.ctx.fillText(`Total Executions: ${totalExec}`, contentX, currentY);
    currentY += 25;

    // Separator line
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.expandedAgentPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Enabled Tools section
    if (this.expandedAgent.config && this.expandedAgent.config.enabled_plugins) {
      this.ctx.fillStyle = '#7c3aed';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('🔧 Enabled Tools', contentX, currentY);
      currentY += 20;

      const plugins = this.expandedAgent.config.enabled_plugins;
      if (plugins.length > 0) {
        plugins.forEach(plugin => {
          // Plugin badge
          this.ctx.fillStyle = '#ede9fe';
          this.ctx.strokeStyle = '#7c3aed';
          this.ctx.lineWidth = 1;
          const pluginText = plugin.length > 20 ? plugin.substring(0, 17) + '...' : plugin;
          const badgeWidth = this.ctx.measureText(pluginText).width + 16;
          this.roundRect(contentX, currentY, badgeWidth, 22, 11);
          this.ctx.fill();
          this.ctx.stroke();

          this.ctx.fillStyle = '#5b21b6';
          this.ctx.font = '11px system-ui';
          this.ctx.fillText(pluginText, contentX + 8, currentY + 15);

          currentY += 28;
        });
        currentY += 10;
      } else {
        this.ctx.fillStyle = '#9ca3af';
        this.ctx.font = 'italic 11px system-ui';
        this.ctx.fillText('No tools enabled', contentX, currentY);
        currentY += 25;
      }

      // Separator
      this.ctx.strokeStyle = '#e5e7eb';
      this.ctx.lineWidth = 1;
      this.ctx.beginPath();
      this.ctx.moveTo(contentX, currentY);
      this.ctx.lineTo(panelX + this.expandedAgentPanelWidth - padding, currentY);
      this.ctx.stroke();
      currentY += 20;
    }

    // System Prompt section
    if (this.expandedAgent.config && this.expandedAgent.config.system_prompt) {
      this.ctx.fillStyle = '#ea580c';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('💬 System Prompt', contentX, currentY);
      currentY += 20;

      // System prompt box
      const promptBoxY = currentY;

      // Calculate height based on actual content (now showing ALL lines)
      this.ctx.fillStyle = '#7c2d12';
      this.ctx.font = '10px system-ui';
      const promptLines = this.wrapText(this.expandedAgent.config.system_prompt, this.expandedAgentPanelWidth - padding * 2 - 20);
      const lineHeight = 13;
      const promptBoxHeight = Math.max(60, 15 + (promptLines.length * lineHeight) + 15); // top padding + lines + bottom padding

      // Draw box
      this.ctx.fillStyle = '#fff7ed';
      this.ctx.strokeStyle = '#ea580c';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, promptBoxY, this.expandedAgentPanelWidth - padding * 2, promptBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      // System prompt text (show ALL lines now)
      this.ctx.fillStyle = '#7c2d12';
      this.ctx.font = '10px system-ui';
      promptLines.forEach((line, i) => {
        this.ctx.fillText(line, contentX + 10, promptBoxY + 15 + i * lineHeight);
      });

      currentY += promptBoxHeight + 15;

      // Separator
      this.ctx.strokeStyle = '#e5e7eb';
      this.ctx.lineWidth = 1;
      this.ctx.beginPath();
      this.ctx.moveTo(contentX, currentY);
      this.ctx.lineTo(panelX + this.expandedAgentPanelWidth - padding, currentY);
      this.ctx.stroke();
      currentY += 20;
    }

    // Task count
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText('Active Tasks:', contentX, currentY);
    currentY += 18;

    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 14px system-ui';
    const taskCount = this.expandedAgent.tasks ? this.expandedAgent.tasks.length : 0;
    this.ctx.fillText(`${taskCount} task${taskCount !== 1 ? 's' : ''}`, contentX, currentY);
    currentY += 25;

    // Tasks list
    if (taskCount > 0) {
      currentY += 10;
      this.ctx.fillStyle = '#059669';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📋 Tasks', contentX, currentY);
      currentY += 20;

      // List tasks
      const maxTasksToShow = 5;
      const tasksToShow = this.expandedAgent.tasks.slice(0, maxTasksToShow);

      tasksToShow.forEach((taskId, index) => {
        // Find the task details
        const task = this.tasks.find(t => t.id === taskId);
        if (task) {
          // Task background
          const taskBoxY = currentY;
          const taskBoxHeight = 45;
          this.ctx.fillStyle = '#f0fdf4';
          this.ctx.strokeStyle = '#10b981';
          this.ctx.lineWidth = 1;
          this.roundRect(contentX, taskBoxY, this.expandedAgentPanelWidth - padding * 2, taskBoxHeight, 6);
          this.ctx.fill();
          this.ctx.stroke();

          // Task description (truncated)
          this.ctx.fillStyle = '#065f46';
          this.ctx.font = '11px system-ui';
          const desc = task.description.length > 35 ? task.description.substring(0, 32) + '...' : task.description;
          this.ctx.fillText(desc, contentX + 8, taskBoxY + 15);

          // Task status
          this.ctx.fillStyle = '#6b7280';
          this.ctx.font = '9px system-ui';
          this.ctx.fillText(`Status: ${task.status}`, contentX + 8, taskBoxY + 32);

          currentY += taskBoxHeight + 8;
        }
      });

      if (this.expandedAgent.tasks.length > maxTasksToShow) {
        this.ctx.fillStyle = '#6b7280';
        this.ctx.font = 'italic 10px system-ui';
        this.ctx.fillText(`... and ${this.expandedAgent.tasks.length - maxTasksToShow} more`, contentX, currentY + 5);
        currentY += 20;
      }
    }

    // Calculate total content height
    // Note: currentY has scroll offset applied (subtracted), so add it back to get actual unscrolled content height
    const totalContentHeight = currentY + this.agentPanelScrollOffset - scrollableStartY + 20; // +20 for bottom padding

    // Restore clipping context
    this.ctx.restore();

    // Calculate scroll parameters
    const maxScroll = Math.max(0, totalContentHeight - scrollableHeight);
    this.agentPanelMaxScroll = maxScroll; // Store for wheel event handler

    // Clamp scroll offset
    this.agentPanelScrollOffset = Math.max(0, Math.min(this.agentPanelScrollOffset, maxScroll));

    // Draw scrollbar if content is scrollable
    if (maxScroll > 0) {
      const scrollbarWidth = 6;
      const scrollbarX = panelX + this.expandedAgentPanelWidth - padding / 2 - scrollbarWidth;
      const scrollbarHeight = Math.max(30, (scrollableHeight / totalContentHeight) * scrollableHeight);
      const scrollbarY = scrollableStartY + (this.agentPanelScrollOffset / maxScroll) * (scrollableHeight - scrollbarHeight);

      this.ctx.fillStyle = 'rgba(0, 0, 0, 0.2)';
      this.roundRect(scrollbarX, scrollbarY, scrollbarWidth, scrollbarHeight, 3);
      this.ctx.fill();
    }

    this.ctx.restore();
  }

  drawExpandedCombinerPanel() {
    if (!this.expandedCombiner) return;

    const panelX = this.width - this.expandedCombinerPanelWidth;
    const panelY = 0;
    const panelHeight = this.height;
    const padding = 20;

    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.25)';
    this.ctx.shadowBlur = 18;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.expandedCombinerPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    if (this.expandedCombinerPanelWidth < 80) {
      this.ctx.restore();
      return;
    }

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.expandedCombinerPanelWidth - padding, padding + 20);

    // Title
    let currentY = padding + 50;
    this.ctx.textAlign = 'left';
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.fillText(`${this.expandedCombiner.name} Node`, panelX + padding, currentY);
    currentY += 26;

    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText(`Mode: ${this.expandedCombiner.resultCombinationMode || 'merge'}`, panelX + padding, currentY);
    currentY += 22;

    // Inputs section
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.fillText('Inputs', panelX + padding, currentY);
    currentY += 18;

    const inputConnections = this.connections.filter(c => c.to === this.expandedCombiner.id);
    if (inputConnections.length === 0) {
      this.ctx.fillStyle = '#9ca3af';
      this.ctx.font = '12px system-ui';
      this.ctx.fillText('No inputs connected', panelX + padding, currentY);
      currentY += 22;
    } else {
      inputConnections.forEach(conn => {
        const source = this.getNodeById(conn.from);
        this.ctx.fillStyle = '#2563eb';
        this.ctx.font = 'bold 12px system-ui';
        this.ctx.fillText(source?.node?.description || source?.node?.name || conn.from, panelX + padding, currentY);
        currentY += 16;
      });
    }

    currentY += 10;
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(panelX + padding, currentY);
    this.ctx.lineTo(panelX + this.expandedCombinerPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Combined result
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.fillText('Combined Output', panelX + padding, currentY);
    currentY += 18;

    const combinedText = this.buildCombinerResultPreview(this.expandedCombiner);
    const textLines = this.wrapText(combinedText || 'No results yet', this.expandedCombinerPanelWidth - padding * 2);

    this.ctx.fillStyle = combinedText ? '#111827' : '#9ca3af';
    this.ctx.font = '12px system-ui';
    textLines.slice(0, 12).forEach(line => {
      this.ctx.fillText(line, panelX + padding, currentY);
      currentY += 16;
    });

    this.ctx.restore();
  }

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
  roundRect(x, y, width, height, radius) {
    this.ctx.beginPath();
    this.ctx.moveTo(x + radius, y);
    this.ctx.lineTo(x + width - radius, y);
    this.ctx.quadraticCurveTo(x + width, y, x + width, y + radius);
    this.ctx.lineTo(x + width, y + height - radius);
    this.ctx.quadraticCurveTo(x + width, y + height, x + width - radius, y + height);
    this.ctx.lineTo(x + radius, y + height);
    this.ctx.quadraticCurveTo(x, y + height, x, y + height - radius);
    this.ctx.lineTo(x, y + radius);
    this.ctx.quadraticCurveTo(x, y, x + radius, y);
    this.ctx.closePath();
  }

  // Mouse interaction handlers
  onMouseDown(e) {
    const rect = this.canvas.getBoundingClientRect();
    const screenX = e.clientX - rect.left;
    const screenY = e.clientY - rect.top;

    // Convert screen coordinates to canvas coordinates
    const x = (e.clientX - rect.left - this.offsetX) / this.scale;
    const y = (e.clientY - rect.top - this.offsetY) / this.scale;

    // Handle help overlay clicks (highest priority - modal overlay)
    if (this.helpOverlayVisible) {
      // Close help overlay on any click
      this.helpOverlayVisible = false;
      this.draw();
      return;
    }

    // Handle context menu clicks (screen coordinates)
    if (this.contextMenuVisible && this.contextMenuItems) {
      for (const item of this.contextMenuItems) {
        if (screenX >= item.x && screenX <= item.x + item.width &&
            screenY >= item.y && screenY <= item.y + item.height) {
          // Handle menu item click
          this.handleContextMenuAction(item.action, item.agent);
          this.contextMenuVisible = false;
          this.contextMenuAgent = null;
          this.contextMenuItems = [];
          this.draw();
          return;
        }
      }
      // Clicked outside menu - close it
      this.contextMenuVisible = false;
      this.contextMenuAgent = null;
      this.contextMenuItems = [];
      this.draw();
      return;
    }

    // If in assignment mode, prioritize assignment clicks over manual port wiring
    // Check ports if not in assignment mode, or treat combiner ports as clicks during assignment
    const clickedPort = this.getPortAtPosition(x, y);
    if (clickedPort) {
      if (this.assignmentMode && this.assignmentSourceTask) {
        const target = this.getNodeById(clickedPort.nodeId);
        if (target && target.type === 'combiner') {
          e.stopPropagation();
          e.preventDefault();
          console.log('Assigning task to combiner via port click:', target.node.id);
          this.assignTaskToCombiner(target.node);
          return;
        }
        // Otherwise ignore port clicks while assigning
      } else {
        e.stopPropagation();
        e.preventDefault();
        this.isDraggingConnection = true;
        this.connectionDragStart = clickedPort;
        this.canvas.style.cursor = 'crosshair';
        console.log(`🔗 Started dragging connection from ${clickedPort.nodeId}.${clickedPort.portId}`);
        return;
      }
    }

    // Check if clicking on a combiner node
    for (const combiner of this.combinerNodes) {
      // Check delete button first (higher priority)
      if (combiner.deleteButtonBounds) {
        const bounds = combiner.deleteButtonBounds;
        if (x >= bounds.x && x <= bounds.x + bounds.width &&
            y >= bounds.y && y <= bounds.y + bounds.height) {
          // Delete this combiner
          e.stopPropagation();
          e.preventDefault();
          this.deleteCombinerNode(combiner.id);
          this.showNotification('Combiner node deleted', 'success');
          return;
        }
      }

      // Check RUN button
      if (combiner.runButtonBounds) {
        const b = combiner.runButtonBounds;
        if (x >= b.x && x <= b.x + b.width &&
            y >= b.y && y <= b.y + b.height) {
          e.stopPropagation();
          e.preventDefault();
          this.executeCombiner(combiner);
          return;
        }
      }

      // Check assign output button
      if (combiner.assignButtonBounds) {
        const b = combiner.assignButtonBounds;
        if (x >= b.x && x <= b.x + b.width &&
            y >= b.y && y <= b.y + b.height) {
          e.stopPropagation();
          e.preventDefault();
          if (this.combinerAssignMode && this.combinerAssignmentSource && this.combinerAssignmentSource.id === combiner.id) {
            this.combinerAssignMode = false;
            this.combinerAssignmentSource = null;
            this.canvas.style.cursor = 'grab';
            this.draw();
            this.showNotification('Combiner assignment cancelled', 'info');
          } else {
            this.combinerAssignMode = true;
            this.combinerAssignmentSource = combiner;
            this.canvas.style.cursor = 'crosshair';
            this.draw();
            this.showNotification('Click an agent to route Merge output', 'info');
          }
          return;
        }
      }

      // Check if clicking on combiner body
      if (x >= combiner.x && x <= combiner.x + combiner.width &&
          y >= combiner.y && y <= combiner.y + combiner.height) {

        // Check if in assignment mode first (higher priority than dragging)
        if (this.assignmentMode && this.assignmentSourceTask) {
          e.stopPropagation();
          e.preventDefault();
          console.log('Assigning task to combiner in mousedown:', combiner.id);
          this.assignTaskToCombiner(combiner);
          return;
        }

        // If not assigning, auto-connect from the last dragged connection start
        if (this.connectionDragStart) {
          e.stopPropagation();
          e.preventDefault();
          const portId = `input-${Math.max(combiner.inputPorts.length, 0)}`;
          this.ensureCombinerInputPort(combiner, portId);
          this.createConnection(
            this.connectionDragStart.nodeId,
            this.connectionDragStart.portId,
            combiner.id,
            portId
          );
          this.connectionDragStart = null;
          this.isDraggingConnection = false;
          this.canvas.style.cursor = 'grab';
          this.draw();
          return;
        }

        // Otherwise, start dragging this combiner
        e.stopPropagation();
        e.preventDefault();
        this.isDraggingCombiner = true;
        this.draggedCombiner = combiner;
        this.dragStartX = x;
        this.dragStartY = y;
        this.canvas.style.cursor = 'move';
        return;
      }
    }

    // Check if Space is pressed for pan mode
    if (this.spacePressed) {
      // Space+Drag to pan
      this.isDragging = true;
      this.dragStartX = screenX - this.offsetX;
      this.dragStartY = screenY - this.offsetY;
      this.canvas.style.cursor = 'grabbing';
      return;
    }

    // Check if clicking on a task card first (tasks are drawn on top)
    if (this.tasks && this.tasks.length > 0) {
      for (let i = this.tasks.length - 1; i >= 0; i--) {  // Check in reverse order (top to bottom)
        const task = this.tasks[i];
        if (task && task.x != null && task.y != null) {  // Use != to catch both null and undefined
          // Use a larger hit area around the task center
          const cardWidth = 160;
          const cardHeight = 60;
          const cardX = task.x - cardWidth / 2;
          const cardY = task.y - cardHeight / 2;

          if (x >= cardX && x <= cardX + cardWidth &&
              y >= cardY && y <= cardY + cardHeight) {
            // Start dragging this task
            e.stopPropagation();
            e.preventDefault();
            this.isDraggingTask = true;
            this.draggedTask = task;
            this.dragStartX = x;
            this.dragStartY = y;
            this.canvas.style.cursor = 'move';
            return;
          }
        }
      }
    }

    // Check if clicking on an agent (rectangle hitbox)
    for (const agent of this.agents) {
      const halfWidth = (agent.width || 120) / 2;
      const halfHeight = (agent.height || 70) / 2;
      if (x >= agent.x - halfWidth && x <= agent.x + halfWidth &&
          y >= agent.y - halfHeight && y <= agent.y + halfHeight) {
        // Start dragging this agent
        this.isDraggingAgent = true;
        this.draggedAgent = agent;
        this.dragStartX = x;
        this.dragStartY = y;
        this.canvas.style.cursor = 'move';
        return;
      }
    }

    // Otherwise, start canvas panning
    this.isDragging = true;
    this.dragStartX = e.clientX - rect.left - this.offsetX;
    this.dragStartY = e.clientY - rect.top - this.offsetY;
    this.canvas.style.cursor = 'grabbing';
  }

  onMouseMove(e) {
    const rect = this.canvas.getBoundingClientRect();

    // Track mouse position for context menu hover effects
    this.lastMouseX = e.clientX - rect.left;
    this.lastMouseY = e.clientY - rect.top;

    // If context menu is visible, redraw to update hover effects
    if (this.contextMenuVisible) {
      this.draw();
    }

    // Handle connection dragging
    if (this.isDraggingConnection) {
      this.draw();
      return;
    }

    // Handle combiner node dragging
    if (this.isDraggingCombiner && this.draggedCombiner) {
      const x = (e.clientX - rect.left - this.offsetX) / this.scale;
      const y = (e.clientY - rect.top - this.offsetY) / this.scale;
      this.draggedCombiner.x = x;
      this.draggedCombiner.y = y;
      this.draw();
      return;
    }

    // Track mouse position for assignment mode
    if (this.assignmentMode && this.assignmentSourceTask) {
      const x = (e.clientX - rect.left - this.offsetX) / this.scale;
      const y = (e.clientY - rect.top - this.offsetY) / this.scale;
      this.assignmentMouseX = x;
      this.assignmentMouseY = y;
      this.draw();
      return;
    }

    if (this.isDraggingTask && this.draggedTask) {
      // Drag the task
      const x = (e.clientX - rect.left - this.offsetX) / this.scale;
      const y = (e.clientY - rect.top - this.offsetY) / this.scale;

      this.draggedTask.x = x;
      this.draggedTask.y = y;
      this.draw();
      return;
    }

    if (this.isDraggingAgent && this.draggedAgent) {
      // Drag the agent
      const x = (e.clientX - rect.left - this.offsetX) / this.scale;
      const y = (e.clientY - rect.top - this.offsetY) / this.scale;

      this.draggedAgent.x = x;
      this.draggedAgent.y = y;
      this.draw();
      return;
    }

    if (this.isDragging) {
      // Pan the canvas
      this.offsetX = (e.clientX - rect.left) - this.dragStartX;
      this.offsetY = (e.clientY - rect.top) - this.dragStartY;
      this.draw();
      return;
    }

    // Check hover over copy button (screen coordinates, not scaled)
    if (this.copyButtonBounds) {
      const mouseX = e.clientX - rect.left;
      const mouseY = e.clientY - rect.top;
      const bounds = this.copyButtonBounds;

      const isHovering = mouseX >= bounds.x && mouseX <= bounds.x + bounds.width &&
                        mouseY >= bounds.y && mouseY <= bounds.y + bounds.height;

      const prevState = this.copyButtonState;
      if (isHovering && this.copyButtonState === 'idle') {
        this.copyButtonState = 'hover';
        this.canvas.style.cursor = 'pointer';
        this.draw();
      } else if (!isHovering && this.copyButtonState === 'hover') {
        this.copyButtonState = 'idle';
        this.canvas.style.cursor = 'grab';
        this.draw();
      }
    }
  }

  onMouseUp(e) {
    const wasDraggingAgent = this.isDraggingAgent;
    const wasDraggingTask = this.isDraggingTask;
    const wasDraggingConnection = this.isDraggingConnection;
    const wasDraggingCombiner = this.isDraggingCombiner;

    // Handle connection drop
    if (wasDraggingConnection && this.connectionDragStart) {
      const rect = this.canvas.getBoundingClientRect();
      const x = (e.clientX - rect.left - this.offsetX) / this.scale;
      const y = (e.clientY - rect.top - this.offsetY) / this.scale;

      // Find port at drop position
      const targetPort = this.getPortAtPosition(x, y);
      let resolvedPort = targetPort;

      // Fallback: if no explicit port hit but dropped on an agent body, treat as input port
      if (!resolvedPort) {
        for (const agent of this.agents) {
          const halfWidth = (agent.width || 120) / 2;
          const halfHeight = (agent.height || 70) / 2;
          if (x >= agent.x - halfWidth && x <= agent.x + halfWidth &&
              y >= agent.y - halfHeight && y <= agent.y + halfHeight) {
            resolvedPort = {
              nodeId: agent.name,
              nodeType: 'agent',
              portId: 'input',
              type: 'input'
            };
            break;
          }
        }
      }

      // Fallback: if no port but dropped on a combiner body, attach to a new input port
      if (!resolvedPort) {
        for (const combiner of this.combinerNodes) {
          if (x >= combiner.x && x <= combiner.x + combiner.width &&
              y >= combiner.y && y <= combiner.y + combiner.height) {
            const nextIndex = Math.max(combiner.inputPorts.length, 0);
            const portId = `input-${nextIndex}`;
            this.ensureCombinerInputPort(combiner, portId);
            resolvedPort = {
              nodeId: combiner.id,
              nodeType: 'combiner',
              portId: portId,
              type: 'input'
            };
            break;
          }
        }
      }

      // Fallback: snap to nearest agent input port within a generous radius
      if (!resolvedPort && this.agents.length > 0) {
        let closest = null;
        let closestDist = Infinity;
        this.agents.forEach(agent => {
          const halfHeight = (agent.height || 70) / 2;
          const portX = agent.x;
          const portY = agent.y - halfHeight - 10;
          const dist = Math.hypot(portX - x, portY - y);
          if (dist < closestDist) {
            closestDist = dist;
            closest = { nodeId: agent.name, nodeType: 'agent', portId: 'input', type: 'input', x: portX, y: portY };
          }
        });
        // Accept if within 80px to make drops forgiving
        if (closest && closestDist <= 80) {
          resolvedPort = closest;
        }
      }

      if (resolvedPort && resolvedPort.nodeId !== this.connectionDragStart.nodeId) {
        // Create connection
        this.createConnection(
          this.connectionDragStart.nodeId,
          this.connectionDragStart.portId,
          resolvedPort.nodeId,
          resolvedPort.portId
        );
        this.showNotification('Connection created', 'success');
      }

      // Clear connection drag state
      this.isDraggingConnection = false;
      this.connectionDragStart = null;
      this.canvas.style.cursor = 'grab';
      this.draw();
      return;
    }

    this.isDragging = false;
    this.isDraggingAgent = false;
    this.draggedAgent = null;
    this.isDraggingTask = false;
    this.isDraggingCombiner = false;
    this.draggedCombiner = null;
    this.draggedTask = null;

    // Save layout if we were dragging something
    if (wasDraggingAgent || wasDraggingTask || wasDraggingCombiner) {
      this.saveLayout();
    }

    // Preserve cursor state for assignment/connection modes
    if (this.assignmentMode || this.combinerAssignMode) {
      this.canvas.style.cursor = 'crosshair';
    } else {
      this.canvas.style.cursor = 'grab';
    }
  }

  onWheel(e) {
    e.preventDefault();

    const rect = this.canvas.getBoundingClientRect();
    const mouseX = e.clientX - rect.left;
    const mouseY = e.clientY - rect.top;

    // Check if mouse is over agent panel for scrolling
    if (this.expandedAgentPanelWidth > 0 && this.expandedAgent) {
      const panelX = this.width - this.expandedAgentPanelWidth;
      const panelY = 0;
      const panelWidth = this.expandedAgentPanelWidth;
      const panelHeight = this.height;

      if (mouseX >= panelX && mouseX <= panelX + panelWidth &&
          mouseY >= panelY && mouseY <= panelY + panelHeight) {
        // Scroll the agent panel content
        const scrollAmount = e.deltaY > 0 ? 20 : -20; // Scroll 20 pixels at a time

        this.agentPanelScrollOffset += scrollAmount;
        this.agentPanelScrollOffset = Math.max(0, Math.min(this.agentPanelMaxScroll, this.agentPanelScrollOffset));

        this.draw();
        return;
      }
    }

    // Check if mouse is over result box for scrolling
    if (this.resultBoxBounds && this.expandedTask) {
      const bounds = this.resultBoxBounds;
      if (mouseX >= bounds.x && mouseX <= bounds.x + bounds.width &&
          mouseY >= bounds.y && mouseY <= bounds.y + bounds.height) {
        // Scroll the result content
        const scrollAmount = e.deltaY > 0 ? 3 : -3; // Scroll 3 lines at a time
        this.resultScrollOffset += scrollAmount;
        this.draw();
        return;
      }
    }

    // Otherwise, zoom the canvas relative to mouse position
    const delta = e.deltaY > 0 ? 0.9 : 1.1;
    const oldScale = this.scale;
    const newScale = Math.max(0.5, Math.min(2, oldScale * delta));

    // Calculate the point in canvas coordinates before zoom
    const canvasX = (mouseX - this.offsetX) / oldScale;
    const canvasY = (mouseY - this.offsetY) / oldScale;

    // Update scale
    this.scale = newScale;

    // Adjust offset so the point under the mouse stays in the same screen position
    this.offsetX = mouseX - canvasX * newScale;
    this.offsetY = mouseY - canvasY * newScale;

    this.draw();
  }

  onClick(e) {
    // Ignore clicks during drag operations
    if (this.isDragging || this.isDraggingAgent || this.isDraggingTask) return;

    const rect = this.canvas.getBoundingClientRect();
    // Screen coordinates (for UI elements like the panel)
    const screenX = e.clientX - rect.left;
    const screenY = e.clientY - rect.top;

    // Check for clicks on add agent form (highest priority when visible)
    if (this.forms.addAgentFormVisible) {
      // Check close button
      if (this.forms.addAgentCloseButtonBounds) {
        const btn = this.forms.addAgentCloseButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.forms.hideAddAgentForm();
          return;
        }
      }

      // Check submit button
      if (this.forms.addAgentSubmitButtonBounds) {
        const btn = this.forms.addAgentSubmitButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.forms.submitAddAgentForm();
          return;
        }
      }

      // Check agent selection buttons
      if (this.forms.agentAddSelectionBounds) {
        for (const bounds of this.forms.agentAddSelectionBounds) {
          if (bounds && screenX >= bounds.x && screenX <= bounds.x + bounds.width &&
              screenY >= bounds.y && screenY <= bounds.y + bounds.height) {
            this.forms.selectedAgentToAdd = bounds.agentName;
            this.draw();
            return;
          }
        }
      }

      // Click outside form - close it
      if (this.forms.addAgentFormBounds) {
        const form = this.forms.addAgentFormBounds;
        if (screenX < form.x || screenX > form.x + form.width ||
            screenY < form.y || screenY > form.y + form.height) {
          this.forms.hideAddAgentForm();
          return;
        }
      }

      // Click inside form but not on any interactive element - do nothing
      return;
    }

    // Check for clicks on create task form (highest priority when visible)
    if (this.forms.createTaskFormVisible) {
      // Check close button
      if (this.forms.createTaskCloseButtonBounds) {
        const btn = this.forms.createTaskCloseButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.forms.hideCreateTaskForm();
          return;
        }
      }


      // Check submit button
      if (this.forms.createTaskSubmitButtonBounds) {
        const btn = this.forms.createTaskSubmitButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.forms.submitCreateTaskForm();
          return;
        }
      }

      // Check checkbox
      if (this.forms.createTaskCheckboxBounds) {
        const cb = this.forms.createTaskCheckboxBounds;
        if (screenX >= cb.x && screenX <= cb.x + cb.width &&
            screenY >= cb.y && screenY <= cb.y + cb.height) {
          this.forms.createTaskAssignToAgent = !this.forms.createTaskAssignToAgent;
          if (!this.forms.createTaskAssignToAgent) {
            this.forms.selectedAgentForTask = null;
          }
          this.draw();
          return;
        }
      }

      // Check agent selection buttons
      if (this.forms.createTaskAssignToAgent && this.forms.agentSelectionBounds) {
        for (const bounds of this.forms.agentSelectionBounds) {
          if (bounds && screenX >= bounds.x && screenX <= bounds.x + bounds.width &&
              screenY >= bounds.y && screenY <= bounds.y + bounds.height) {
            this.forms.selectedAgentForTask = bounds.agentName;
            this.draw();
            return;
          }
        }
      }

      // Check description field - enable direct typing
      if (this.forms.createTaskDescriptionBounds) {
        const input = this.forms.createTaskDescriptionBounds;
        if (screenX >= input.x && screenX <= input.x + input.width &&
            screenY >= input.y && screenY <= input.y + input.height) {
          this.forms.createTaskDescriptionFocused = true;
          this.canvas.style.cursor = 'text';
          this.draw();
          return;
        } else if (this.forms.createTaskDescriptionFocused) {
          // Clicked somewhere else in the form, unfocus description field
          this.forms.createTaskDescriptionFocused = false;
          this.canvas.style.cursor = 'default';
          this.draw();
        }
      }

      // Click outside form - close it
      if (this.forms.createTaskFormBounds) {
        const form = this.forms.createTaskFormBounds;
        if (screenX < form.x || screenX > form.x + form.width ||
            screenY < form.y || screenY > form.y + form.height) {
          this.forms.hideCreateTaskForm();
          return;
        }
      }

      // Click inside form but not on any interactive element - do nothing
      return;
    }

    // Check for click on "Create Task" button
    if (this.createTaskButtonBounds) {
      const btn = this.createTaskButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.forms.showCreateTaskForm();
        return;
      }
    }

    // Check for click on "Add Agent" button
    if (this.addAgentButtonBounds) {
      const btn = this.addAgentButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.forms.showAddAgentForm();
        return;
      }
    }

    // Check for click on "Timeline" toggle button
    if (this.timelineToggleBounds) {
      const btn = this.timelineToggleBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.toggleTimeline();
        return;
      }
    }

    // Check for click on "Auto-Layout" button
    if (this.autoLayoutButtonBounds) {
      const btn = this.autoLayoutButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.autoLayoutTasks();
        return;
      }
    }

    // Check for click on "Save Layout" button
    if (this.saveLayoutButtonBounds) {
      const btn = this.saveLayoutButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.saveLayout();
        this.showNotification('💾 Layout saved', 'success');
        return;
      }
    }

    // Check for click on timeline panel close button
    if (this.timelinePanelWidth > 0) {
      const panelX = this.width - this.timelinePanelWidth;
      const closeButtonX = panelX + this.timelinePanelWidth - 30;
      const closeButtonY = 15;
      const closeButtonSize = 30;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.toggleTimeline();
        return;
      }
    }

    // Check if click is on close button of expanded agent panel
    if (this.expandedAgentPanelWidth > 0) {
      const panelX = this.width - this.expandedAgentPanelWidth;
      const closeButtonX = panelX + this.expandedAgentPanelWidth - 40;
      const closeButtonY = 30;
      const closeButtonSize = 40;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.closeAgentPanel();
        return;
      }

      // If clicking anywhere on the agent panel, don't process other clicks
      if (screenX >= panelX) {
        return;
      }
    }

    // Check if click is on close button of expanded combiner panel
    if (this.expandedCombinerPanelWidth > 0) {
      const panelX = this.width - this.expandedCombinerPanelWidth;
      const closeButtonX = panelX + this.expandedCombinerPanelWidth - 40;
      const closeButtonY = 30;
      const closeButtonSize = 40;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.closeCombinerPanel();
        return;
      }

      // If clicking anywhere on the combiner panel, don't process other clicks
      if (screenX >= panelX) {
        return;
      }
    }

    // Check if click is on close button of expanded task panel
    if (this.expandedPanelWidth > 0) {
      const panelX = this.width - this.expandedPanelWidth;
      const closeButtonX = panelX + this.expandedPanelWidth - 40;
      const closeButtonY = 30;
      const closeButtonSize = 40;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.closeTaskPanel();
        return;
      }

      // Check if click is on copy button
      if (this.copyButtonBounds) {
        const btn = this.copyButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.copyResultToClipboard();
          return;
        }
      }

      // If clicking anywhere on the panel, don't process other clicks
      if (screenX >= panelX) {
        return;
      }
    }

    // Convert screen coordinates to canvas coordinates
    const x = (e.clientX - rect.left - this.offsetX) / this.scale;
    const y = (e.clientY - rect.top - this.offsetY) / this.scale;

    // Check if click is on any task first (tasks are on top)
    for (let i = this.tasks.length - 1; i >= 0; i--) {
      const task = this.tasks[i];
      if (task && task.x != null && task.y != null) {
        const cardWidth = 160;
        const cardHeight = 60;
        const cardX = task.x - cardWidth / 2;
        const cardY = task.y - cardHeight / 2;

        // Check if click is on delete button first
        if (task.deleteBtnBounds) {
          const btn = task.deleteBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Delete button clicked
            this.deleteTask(task);
            return;
          }
        }

        // Check if click is on execute button
        if (task.executeBtnBounds && task.status === 'pending') {
          const btn = task.executeBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Execute button clicked
            this.executeTask(task);
            return;
          }
        }

        // Check if click is on rerun button
        if (task.rerunBtnBounds && (task.status === 'completed' || task.status === 'failed')) {
          const btn = task.rerunBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Rerun button clicked
            this.rerunTask(task);
            return;
          }
        }

        // Check if click is on assign button
        if (task.assignBtnBounds && task.status !== 'completed') {
          const btn = task.assignBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Assign button clicked - toggle assignment mode
            this.toggleAssignmentMode(task);
            return;
          }
        }

        // Check if click is on view log button
        if (task.viewLogBtnBounds) {
          const btn = task.viewLogBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // View log button clicked - show execution log modal
            this.showExecutionLog(task);
            return;
          }
        }

        if (x >= cardX && x <= cardX + cardWidth &&
            y >= cardY && y <= cardY + cardHeight) {
          // Task clicked - expand/collapse panel
          this.toggleTaskPanel(task);
          return;
        }
      }
    }

    // Check if click is on any agent
    for (const agent of this.agents) {
      const halfWidth = (agent.width || 120) / 2;
      const halfHeight = (agent.height || 70) / 2;
      if (x >= agent.x - halfWidth && x <= agent.x + halfWidth &&
          y >= agent.y - halfHeight && y <= agent.y + halfHeight) {
        // Agent clicked
        console.log('Agent clicked:', agent.name, 'assignmentMode:', this.assignmentMode, 'combinerAssignMode:', this.combinerAssignMode);
        if (this.assignmentMode && this.assignmentSourceTask) {
          // In assignment mode - assign task to agent
          console.log('Assigning task to agent:', agent.name);
          this.assignTaskToAgent(agent);
          return;
        } else if (this.combinerAssignMode && this.combinerAssignmentSource) {
          // Wire combiner output to this agent
          this.createConnection(this.combinerAssignmentSource.id, 'output', agent.name, 'input');
          this.combinerAssignMode = false;
          this.combinerAssignmentSource = null;
          this.canvas.style.cursor = 'grab';
          this.draw();
          this.saveLayout();
          this.showNotification(`Combiner output connected to ${agent.name}`, 'success');
          return;
        } else {
          // Toggle agent panel
          this.toggleAgentPanel(agent);
        }
        return;
      }
    }

    // Check combiner node clicks (for task assignment)
    for (const combiner of this.combinerNodes) {
      if (x >= combiner.x && x <= combiner.x + combiner.width &&
          y >= combiner.y && y <= combiner.y + combiner.height) {
        // Combiner clicked
        console.log('Combiner clicked:', combiner.id, 'assignmentMode:', this.assignmentMode, 'combinerAssignMode:', this.combinerAssignMode);
        if (this.assignmentMode && this.assignmentSourceTask) {
          // In assignment mode - assign task to combiner
          console.log('Assigning task to combiner:', combiner.id);
          this.assignTaskToCombiner(combiner);
          return;
        }

        // Toggle combiner detail panel
        this.toggleCombinerPanel(combiner);
        return;
      }
    }

    // Click on empty space - close expanded panels
    if (this.expandedTask) {
      this.closeTaskPanel();
    }
    if (this.expandedAgent) {
      this.closeAgentPanel();
    }
    if (this.expandedCombiner) {
      this.closeCombinerPanel();
    }
  }

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

  drawAssignmentLine() {
    // Draw line from task to cursor
    this.ctx.save();
    this.ctx.translate(this.offsetX, this.offsetY);
    this.ctx.scale(this.scale, this.scale);

    // Only draw when we have a source task
    if (this.assignmentSourceTask) {
      // Draw line
      this.ctx.strokeStyle = '#fd7e14';
      this.ctx.lineWidth = 3;
      this.ctx.setLineDash([10, 5]);
      this.ctx.beginPath();
      this.ctx.moveTo(this.assignmentSourceTask.x, this.assignmentSourceTask.y);
      this.ctx.lineTo(this.assignmentMouseX, this.assignmentMouseY);
      this.ctx.stroke();
      this.ctx.setLineDash([]);

      // Draw arrow at cursor
      const angle = Math.atan2(
        this.assignmentMouseY - this.assignmentSourceTask.y,
        this.assignmentMouseX - this.assignmentSourceTask.x
      );
      const arrowSize = 15;
      this.ctx.fillStyle = '#fd7e14';
      this.ctx.beginPath();
      this.ctx.moveTo(this.assignmentMouseX, this.assignmentMouseY);
      this.ctx.lineTo(
        this.assignmentMouseX - arrowSize * Math.cos(angle - Math.PI / 6),
        this.assignmentMouseY - arrowSize * Math.sin(angle - Math.PI / 6)
      );
      this.ctx.lineTo(
        this.assignmentMouseX - arrowSize * Math.cos(angle + Math.PI / 6),
        this.assignmentMouseY - arrowSize * Math.sin(angle + Math.PI / 6)
      );
      this.ctx.closePath();
      this.ctx.fill();
    }

    this.ctx.restore();
  }

  /**
   * Auto-layout tasks in a hierarchical flow (top to bottom)
   */
  autoLayoutTasks() {
    if (!this.tasks || this.tasks.length === 0) return;

    // Calculate dependency levels (topological sort)
    const levels = this.calculateTaskLevels();

    // Get canvas dimensions
    const canvasWidth = this.width / this.scale;
    const canvasHeight = this.height / this.scale;

    // Vertical flow layout: tasks on the left, agents on the right
    const taskColumnX = 300; // X position for tasks (left side)
    const agentColumnX = 700; // X position for agents (right side)
    const verticalSpacing = 250; // Space between task levels
    const startY = 150; // Start position from top

    // Position tasks level by level (vertically)
    levels.forEach((taskGroup, levelIndex) => {
      const baseY = startY + (levelIndex * verticalSpacing);

      taskGroup.forEach((task, taskIndex) => {
        // Tasks in same level: stack vertically with slight offset
        const yOffset = taskIndex * 100; // Multiple tasks in same level
        task.x = taskColumnX;
        task.y = baseY + yOffset;

        // Position the agent for this task to the right
        const agentName = task.to;
        if (agentName) {
          const agent = this.agents.find(a => a.name === agentName);
          if (agent) {
            agent.x = agentColumnX;
            agent.y = task.y; // Align agent with its task
          }
        }
      });
    });

    // Auto-zoom to fit all content
    this.zoomToFitContent();

    this.draw();
    this.showNotification('✨ Tasks auto-arranged', 'success');

    // Save the new layout
    this.saveLayout();
  }

  /**
   * Zoom and pan to fit all tasks and agents in view
   */
  zoomToFitContent() {
    if ((!this.tasks || this.tasks.length === 0) && (!this.agents || this.agents.length === 0)) {
      return;
    }

    // Calculate bounding box of all content
    let minX = Infinity, maxX = -Infinity;
    let minY = Infinity, maxY = -Infinity;

    // Include tasks
    this.tasks.forEach(task => {
      const taskWidth = 180;
      const taskHeight = 100;
      minX = Math.min(minX, task.x - taskWidth / 2);
      maxX = Math.max(maxX, task.x + taskWidth / 2);
      minY = Math.min(minY, task.y - taskHeight / 2);
      maxY = Math.max(maxY, task.y + taskHeight / 2);
    });

    // Include agents
    this.agents.forEach(agent => {
      const halfW = (agent.width || 120) / 2;
      const halfH = (agent.height || 70) / 2;
      minX = Math.min(minX, agent.x - halfW);
      maxX = Math.max(maxX, agent.x + halfW);
      minY = Math.min(minY, agent.y - halfH);
      maxY = Math.max(maxY, agent.y + halfH);
    });

    // Calculate content dimensions
    const contentWidth = maxX - minX;
    const contentHeight = maxY - minY;
    const contentCenterX = (minX + maxX) / 2;
    const contentCenterY = (minY + maxY) / 2;

    // Calculate required scale to fit content with padding
    const padding = 100; // Padding around content
    const scaleX = this.width / (contentWidth + padding * 2);
    const scaleY = this.height / (contentHeight + padding * 2);
    const newScale = Math.min(scaleX, scaleY, 1.0); // Don't zoom in beyond 100%

    // Clamp scale to reasonable limits
    this.scale = Math.max(0.3, Math.min(1.0, newScale));

    // Calculate offset to center content
    this.offsetX = (this.width / 2) - (contentCenterX * this.scale);
    this.offsetY = (this.height / 2) - (contentCenterY * this.scale);
  }

  /**
   * Arrange agents at the bottom of the canvas
   */
  arrangeAgentsAtBottom(canvasWidth, canvasHeight, tasksBottomY) {
    if (!this.agents || this.agents.length === 0) return;

    const agentSpacing = 200; // Space between agents
    const bottomMargin = 150; // Space from bottom

    // Calculate horizontal positions
    const totalWidth = (this.agents.length - 1) * agentSpacing;
    const startX = (canvasWidth / 2) - (totalWidth / 2);
    const y = Math.max(tasksBottomY + 150, canvasHeight - bottomMargin); // Below tasks or at bottom

    this.agents.forEach((agent, index) => {
      const x = startX + (index * agentSpacing);
      agent.x = x;
      agent.y = y;
    });
  }

  /**
   * Calculate task dependency levels using topological sort
   */
  calculateTaskLevels() {
    const levels = [];
    const visited = new Set();
    const taskMap = new Map(this.tasks.map(t => [t.id, t]));

    // Helper to calculate task level recursively
    const getLevel = (task) => {
      if (visited.has(task.id)) {
        return task.level || 0;
      }

      visited.add(task.id);

      // If task has input tasks, its level is max(input levels) + 1
      if (task.input_task_ids && task.input_task_ids.length > 0) {
        const inputLevels = task.input_task_ids
          .map(id => taskMap.get(id))
          .filter(t => t)
          .map(t => getLevel(t));

        task.level = Math.max(...inputLevels, 0) + 1;
      } else {
        task.level = 0;
      }

      return task.level;
    };

    // Calculate levels for all tasks
    this.tasks.forEach(task => getLevel(task));

    // Group tasks by level
    const maxLevel = Math.max(...this.tasks.map(t => t.level || 0));
    for (let i = 0; i <= maxLevel; i++) {
      levels[i] = this.tasks.filter(t => (t.level || 0) === i);
    }

    return levels;
  }

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
  drawCreateTaskButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.width - buttonWidth - 20;
    const buttonY = 20;

    // Store button bounds for click detection
    this.createTaskButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Draw button background
    this.ctx.fillStyle = '#3b82f6';
    this.ctx.strokeStyle = '#1e40af';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('+ Create Task', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  /**
   * Draw the floating "Add Agent" button to the left of Create Task button
   */
  drawAddAgentButton() {
    const buttonWidth = 130;
    const buttonHeight = 40;
    const buttonX = this.width - 140 - 20 - buttonWidth - 10; // Left of Create Task button
    const buttonY = 20;

    // Store button bounds for click detection
    this.addAgentButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Draw button background
    this.ctx.fillStyle = '#10b981'; // Green color for add agent
    this.ctx.strokeStyle = '#059669';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('+ Add Agent', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  /**
   * Draw toast notifications
   */
  drawNotifications() {
    if (!this.notifications || this.notifications.length === 0) return;

    const notificationWidth = 320;
    const notificationHeight = 70;
    const padding = 15;
    const spacing = 10;

    this.ctx.save();

    this.notifications.forEach((notification, index) => {
      const x = this.width - notificationWidth - 20;
      const y = this.height - (notificationHeight + spacing) * (index + 1) - 80;

      // Background color based on type
      const colors = {
        'info': { bg: '#3b82f6', border: '#1e40af' },
        'success': { bg: '#10b981', border: '#059669' },
        'warning': { bg: '#f59e0b', border: '#d97706' },
        'error': { bg: '#ef4444', border: '#dc2626' }
      };
      const color = colors[notification.type] || colors['info'];

      // Background
      this.ctx.fillStyle = color.bg;
      this.ctx.strokeStyle = color.border;
      this.ctx.lineWidth = 2;
      this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
      this.ctx.shadowBlur = 12;
      this.ctx.shadowOffsetY = 4;
      this.roundRect(x, y, notificationWidth, notificationHeight, 8);
      this.ctx.fill();
      this.ctx.stroke();
      this.ctx.shadowColor = 'transparent';

      // Icon
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = '20px system-ui';
      const icons = {
        'info': 'ℹ️',
        'success': '✓',
        'warning': '⚠️',
        'error': '✗'
      };
      const icon = icons[notification.type] || 'ℹ️';
      this.ctx.fillText(icon, x + padding, y + 28);

      // Message
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = '13px system-ui';
      this.ctx.textAlign = 'left';
      this.ctx.textBaseline = 'top';

      // Wrap text if too long
      const maxWidth = notificationWidth - padding * 2 - 35;
      const lines = this.wrapText(notification.message, maxWidth);
      lines.slice(0, 2).forEach((line, i) => {
        this.ctx.fillText(line, x + padding + 30, y + padding + i * 18);
      });

      // Close button
      this.ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
      this.ctx.font = 'bold 16px system-ui';
      this.ctx.textAlign = 'right';
      this.ctx.fillText('×', x + notificationWidth - padding, y + padding + 5);

      // Store bounds for click detection
      notification.closeBounds = {
        x: x + notificationWidth - padding - 20,
        y: y + padding - 5,
        width: 25,
        height: 25
      };
    });

    this.ctx.restore();
  }

  /**
   * Draw timeline toggle button
   */
  drawTimelineToggleButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.width - buttonWidth - 20;
    const buttonY = 70; // Below create task button

    // Store button bounds for click detection
    this.timelineToggleBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Button background - different color if timeline is open
    this.ctx.fillStyle = this.timelineVisible ? '#059669' : '#6b7280';
    this.ctx.strokeStyle = this.timelineVisible ? '#047857' : '#4b5563';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    const text = this.timelineVisible ? '📋 Hide Timeline' : '📋 Timeline';
    this.ctx.fillText(text, buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  /**
   * Draw auto-layout button
   */
  drawAutoLayoutButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.width - buttonWidth - 20;
    const buttonY = 120; // Below timeline button

    // Store button bounds for click detection
    this.autoLayoutButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Button background
    this.ctx.fillStyle = '#8b5cf6'; // Purple to match connection theme
    this.ctx.strokeStyle = '#7c3aed';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('⚡ Auto-Layout', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  /**
   * Draw save layout button
   */
  drawSaveLayoutButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.width - buttonWidth - 20;
    const buttonY = 170; // Below auto-layout button

    // Store button bounds for click detection
    this.saveLayoutButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Button background
    this.ctx.fillStyle = '#10b981'; // Green for save action
    this.ctx.strokeStyle = '#059669';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 8;
    this.ctx.shadowOffsetY = 2;
    this.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Button text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('💾 Save Layout', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  /**
   * Draw timeline panel
   */
  drawTimelinePanel() {
    if (!this.timelineEvents || this.timelineEvents.length === 0) {
      // Show empty state
      this.drawEmptyTimeline();
      return;
    }

    const panelX = this.width - this.timelinePanelWidth;
    const panelY = 0;
    const panelHeight = this.height;
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.timelinePanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.timelinePanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.timelinePanelWidth - padding, currentY + 20);
    currentY += 40;

    // Title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Activity Timeline', contentX, currentY);
    currentY += 10;

    // Event count
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '11px system-ui';
    this.ctx.fillText(`${this.timelineEvents.length} recent events`, contentX, currentY);
    currentY += 25;

    // Separator
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.timelinePanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 15;

    // Draw events
    const maxVisibleEvents = Math.floor((panelHeight - currentY - 20) / 70);
    const visibleEvents = this.timelineEvents.slice(0, maxVisibleEvents);

    visibleEvents.forEach((event, index) => {
      this.drawTimelineEvent(event, contentX, currentY, this.timelinePanelWidth - padding * 2);
      currentY += 70;
    });

    this.ctx.restore();
  }

  /**
   * Draw empty timeline state
   */
  drawEmptyTimeline() {
    const panelX = this.width - this.timelinePanelWidth;
    const panelY = 0;
    const panelHeight = this.height;
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.timelinePanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    if (this.timelinePanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.timelinePanelWidth - padding, currentY + 20);
    currentY += 40;

    // Title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Activity Timeline', contentX, currentY);
    currentY += 60;

    // Empty state message
    this.ctx.fillStyle = '#9ca3af';
    this.ctx.font = '13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.fillText('No activity yet', panelX + this.timelinePanelWidth / 2, currentY);
    this.ctx.fillText('Events will appear here', panelX + this.timelinePanelWidth / 2, currentY + 20);

    this.ctx.restore();
  }

  /**
   * Draw a single timeline event
   */
  drawTimelineEvent(event, x, y, width) {
    const icon = this.getEventIcon(event.type);
    const message = this.getEventMessage(event);
    const time = new Date(event.timestamp).toLocaleTimeString();

    // Icon
    this.ctx.font = '18px system-ui';
    this.ctx.fillStyle = this.getEventColor(event.type);
    this.ctx.fillText(icon, x, y + 14);

    // Time
    this.ctx.fillStyle = '#9ca3af';
    this.ctx.font = '10px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText(time, x + 30, y + 6);

    // Message
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = '12px system-ui';
    const lines = this.wrapText(message, width - 35);
    lines.slice(0, 2).forEach((line, i) => {
      this.ctx.fillText(line, x + 30, y + 20 + i * 16);
    });

    // Agent name (if available)
    if (event.data.agent) {
      this.ctx.fillStyle = '#6b7280';
      this.ctx.font = '10px system-ui';
      this.ctx.fillText(`Agent: ${event.data.agent}`, x + 30, y + 54);
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

  /**
   * Save the current layout (positions and zoom) to the server
   */
  async saveLayout() {
    if (!this.studioId) {
      console.log('❌ Cannot save layout: no studioId');
      return;
    }

    try {
      // Keep combiner input ports in sync with actual connections before persisting
      this.combinerNodes.forEach(node => this.cleanupCombinerInputPorts(node));

      // Collect task positions
      const taskPositions = {};
      this.tasks.forEach(task => {
        console.log(`  📍 Task ${task.id}: (${task.x}, ${task.y})`);
        taskPositions[task.id] = { x: task.x, y: task.y };
      });

      // Collect agent positions
      const agentPositions = {};
      this.agents.forEach(agent => {
        console.log(`  📍 Agent ${agent.name}: (${agent.x}, ${agent.y})`);
        agentPositions[agent.name] = { x: agent.x, y: agent.y };
      });

      // Collect combiner nodes
      const combinerNodes = this.combinerNodes.map(node => ({
        id: node.id,
        type: node.type,
        combinerType: node.combinerType,
        name: node.name,
        icon: node.icon,
        color: node.color,
        description: node.description,
        x: node.x,
        y: node.y,
        width: node.width,
        height: node.height,
        inputPorts: node.inputPorts || [],
        outputPort: node.outputPort || { id: 'output' },
        resultCombinationMode: node.resultCombinationMode,
        customInstruction: node.customInstruction,
        config: node.config || {},
        taskId: node.taskId // Include taskId for backend task association
      }));

      // Collect workflow connections (agents/tasks/combiners)
      const workflowConnections = this.connections.map(conn => ({
        id: conn.id,
        from: conn.from,
        fromPort: conn.fromPort,
        to: conn.to,
        toPort: conn.toPort,
        color: conn.color,
        animated: conn.animated
      }));

      console.log(`💾 Saving layout for workspace ${this.studioId}`);
      console.log(`  Tasks: ${Object.keys(taskPositions).length}, Agents: ${Object.keys(agentPositions).length}, Combiners: ${combinerNodes.length}, Connections: ${workflowConnections.length}`);
      console.log(`  Scale: ${this.scale}, Offset: (${this.offsetX}, ${this.offsetY})`);
      console.log(`  Task positions:`, taskPositions);
      console.log(`  Agent positions:`, agentPositions);

      await apiPut('/api/orchestration/workspace/layout', {
        workspace_id: this.studioId,
        task_positions: taskPositions,
        agent_positions: agentPositions,
        combiner_nodes: combinerNodes,
        workflow_connections: workflowConnections,
        scale: this.scale,
        offset_x: this.offsetX,
        offset_y: this.offsetY,
      });

      console.log('✅ Layout saved successfully');
    } catch (error) {
      console.error('❌ Error saving layout:', error);
    }
  }

  /**
   * Load the saved layout from the server
   */
  loadLayout() {
    if (!this.studio) {
      console.log('❌ No studio object, cannot load layout');
      return;
    }

    if (!this.studio.layout) {
      console.log('❌ No layout saved for this workspace');
      return;
    }

    const layout = this.studio.layout;
    console.log('📂 Loading layout:', layout);

    let tasksRestored = 0;
    let agentsRestored = 0;
    let combinersRestored = 0;
    let connectionsRestored = 0;

    // Restore task positions
    if (layout.task_positions) {
      this.tasks.forEach(task => {
        const savedPos = layout.task_positions[task.id];
        if (savedPos) {
          console.log(`  Restoring task ${task.id} to (${savedPos.x}, ${savedPos.y})`);
          task.x = savedPos.x;
          task.y = savedPos.y;
          tasksRestored++;
        }
      });
    }

    // Restore agent positions
    if (layout.agent_positions) {
      this.agents.forEach(agent => {
        const savedPos = layout.agent_positions[agent.name];
        if (savedPos) {
          console.log(`  Restoring agent ${agent.name} to (${savedPos.x}, ${savedPos.y})`);
          agent.x = savedPos.x;
          agent.y = savedPos.y;
          agentsRestored++;
        }
      });
    }

    // Restore combiner nodes
    if (layout.combiner_nodes && Array.isArray(layout.combiner_nodes)) {
      this.combinerNodes = layout.combiner_nodes.map(node => ({
        ...node,
        width: node.width || 120,
        height: node.height || 80,
        inputPorts: node.inputPorts || [],
        outputPort: node.outputPort || { id: 'output', x: 0, y: 40 }
      }));
      combinersRestored = this.combinerNodes.length;
    }

    // Restore workflow connections
    if (layout.workflow_connections && Array.isArray(layout.workflow_connections)) {
      this.connections = layout.workflow_connections;
      // Ensure combiner port state matches restored connections
      this.connections.forEach(conn => {
        const targetNode = this.getNodeById(conn.to);
        if (targetNode && targetNode.type === 'combiner' && conn.toPort && conn.toPort.startsWith('input')) {
          this.ensureCombinerInputPort(targetNode.node, conn.toPort);
        }
      });
      connectionsRestored = this.connections.length;
    }

    // Remove stale combiner input ports so only active connections are shown
    if (this.combinerNodes.length > 0) {
      this.combinerNodes.forEach(node => this.cleanupCombinerInputPorts(node));
    }

    // Restore zoom and pan
    if (layout.scale) {
      this.scale = layout.scale;
      console.log(`  Restoring scale: ${layout.scale}`);
    }
    if (layout.offset_x !== undefined) {
      this.offsetX = layout.offset_x;
      console.log(`  Restoring offsetX: ${layout.offset_x}`);
    }
    if (layout.offset_y !== undefined) {
      this.offsetY = layout.offset_y;
      console.log(`  Restoring offsetY: ${layout.offset_y}`);
    }

    console.log(`📂 Layout loaded successfully (${tasksRestored} tasks, ${agentsRestored} agents, ${combinersRestored} combiners, ${connectionsRestored} connections)`);
    this.draw();
  }

  // === NEW FEATURES ===

  // Keyboard shortcut: onKeyUp handler
  onKeyUp(e) {
    if (e.key === ' ') {
      this.spacePressed = false;
      if (!this.isDragging) {
        this.canvas.style.cursor = 'grab';
      }
    }
    if (!e.ctrlKey && !e.metaKey) {
      this.ctrlPressed = false;
    }
  }

  // Zoom to fit all agents in viewport
  zoomToFit() {
    if (this.agents.length === 0) {
      // No agents, just reset to default
      this.offsetX = 0;
      this.offsetY = 0;
      this.scale = 1;
      this.draw();
      return;
    }

    // Find bounding box of all agents
    let minX = Infinity, minY = Infinity;
    let maxX = -Infinity, maxY = -Infinity;

    this.agents.forEach(agent => {
      const halfW = (agent.width || 120) / 2;
      const halfH = (agent.height || 70) / 2;
      minX = Math.min(minX, agent.x - halfW);
      minY = Math.min(minY, agent.y - halfH);
      maxX = Math.max(maxX, agent.x + halfW);
      maxY = Math.max(maxY, agent.y + halfH);
    });

    const contentWidth = maxX - minX;
    const contentHeight = maxY - minY;
    const padding = 100; // Padding around edges

    // Calculate scale to fit content
    const scaleX = (this.width - 2 * padding) / contentWidth;
    const scaleY = (this.height - 2 * padding) / contentHeight;
    const newScale = Math.min(scaleX, scaleY, 2); // Max zoom of 2x

    // Center the content
    const centerX = (minX + maxX) / 2;
    const centerY = (minY + maxY) / 2;

    this.scale = newScale;
    this.offsetX = this.width / 2 - centerX * newScale;
    this.offsetY = this.height / 2 - centerY * newScale;

    this.draw();
    console.log('🎯 Zoomed to fit all agents');
  }

  // Context menu for agents
  onContextMenu(e) {
    e.preventDefault();

    const rect = this.canvas.getBoundingClientRect();
    const screenX = e.clientX - rect.left;
    const screenY = e.clientY - rect.top;

    // Convert to canvas coordinates
    const canvasX = (screenX - this.offsetX) / this.scale;
    const canvasY = (screenY - this.offsetY) / this.scale;

    // Check if clicking on a connection (highest priority for context menu)
    const clickedConnection = this.getConnectionAtPosition(canvasX, canvasY);
    if (clickedConnection) {
      // Confirm and delete connection
      if (confirm('Delete this connection?')) {
        this.deleteConnection(clickedConnection.id);
        this.showNotification('Connection deleted', 'success');
      }
      return;
    }

    // Check if clicking on an agent
    const clickedAgent = this.agents.find(agent => {
      const halfWidth = (agent.width || 120) / 2;
      const halfHeight = (agent.height || 70) / 2;
      return canvasX >= agent.x - halfWidth && canvasX <= agent.x + halfWidth &&
             canvasY >= agent.y - halfHeight && canvasY <= agent.y + halfHeight;
    });

    if (clickedAgent) {
      this.contextMenuVisible = true;
      this.contextMenuAgent = clickedAgent;
      this.contextMenuX = screenX;
      this.contextMenuY = screenY;
      this.draw();
    }
  }

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
  drawContextMenu() {
    if (!this.contextMenuAgent) return;

    const menuWidth = 200;
    const menuHeight = 140;
    const padding = 10;
    const itemHeight = 35;

    // Position menu (ensure it stays within canvas bounds)
    let x = this.contextMenuX;
    let y = this.contextMenuY;
    if (x + menuWidth > this.width) x = this.width - menuWidth - 10;
    if (y + menuHeight > this.height) y = this.height - menuHeight - 10;

    // Draw menu background (glassmorphism effect)
    this.ctx.save();
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
    this.ctx.strokeStyle = 'rgba(0, 0, 0, 0.1)';
    this.ctx.lineWidth = 1;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 4;

    // Rounded rectangle for menu
    this.ctx.beginPath();
    const radius = 8;
    this.ctx.moveTo(x + radius, y);
    this.ctx.lineTo(x + menuWidth - radius, y);
    this.ctx.arcTo(x + menuWidth, y, x + menuWidth, y + radius, radius);
    this.ctx.lineTo(x + menuWidth, y + menuHeight - radius);
    this.ctx.arcTo(x + menuWidth, y + menuHeight, x + menuWidth - radius, y + menuHeight, radius);
    this.ctx.lineTo(x + radius, y + menuHeight);
    this.ctx.arcTo(x, y + menuHeight, x, y + menuHeight - radius, radius);
    this.ctx.lineTo(x, y + radius);
    this.ctx.arcTo(x, y, x + radius, y, radius);
    this.ctx.closePath();
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.restore();

    // Menu title
    this.ctx.save();
    this.ctx.fillStyle = '#1e293b';
    this.ctx.font = 'bold 13px Inter, sans-serif';
    this.ctx.fillText(this.contextMenuAgent.name, x + padding, y + padding + 12);
    this.ctx.restore();

    // Draw separator line
    this.ctx.save();
    this.ctx.strokeStyle = 'rgba(0, 0, 0, 0.1)';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(x + padding, y + padding + 20);
    this.ctx.lineTo(x + menuWidth - padding, y + padding + 20);
    this.ctx.stroke();
    this.ctx.restore();

    // Menu items
    const items = [
      { icon: '👁️', label: 'View Details', action: 'view' },
      { icon: '📋', label: 'Assign Task', action: 'assign' },
      { icon: '🗑️', label: 'Remove', action: 'remove' }
    ];

    this.ctx.save();
    this.ctx.font = '13px Inter, sans-serif';
    items.forEach((item, i) => {
      const itemY = y + padding + 30 + (i * itemHeight);

      // Check if mouse is hovering over this item
      const mouseX = this.lastMouseX || 0;
      const mouseY = this.lastMouseY || 0;
      const isHovered = mouseX >= x && mouseX <= x + menuWidth &&
                       mouseY >= itemY && mouseY <= itemY + itemHeight;

      // Draw hover background
      if (isHovered) {
        this.ctx.fillStyle = 'rgba(29, 78, 216, 0.1)';
        this.ctx.fillRect(x + 5, itemY, menuWidth - 10, itemHeight);
      }

      // Draw icon
      this.ctx.fillStyle = '#475569';
      this.ctx.fillText(item.icon, x + padding + 5, itemY + 22);

      // Draw label
      this.ctx.fillStyle = item.action === 'remove' ? '#dc2626' : '#1e293b';
      this.ctx.fillText(item.label, x + padding + 30, itemY + 22);

      // Store item bounds for click detection
      if (!this.contextMenuItems) this.contextMenuItems = [];
      this.contextMenuItems[i] = {
        x, y: itemY, width: menuWidth, height: itemHeight,
        action: item.action, agent: this.contextMenuAgent
      };
    });
    this.ctx.restore();
  }

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
  drawHelpOverlay() {
    const overlayWidth = 400;
    const overlayHeight = 450;
    const x = (this.width - overlayWidth) / 2;
    const y = (this.height - overlayHeight) / 2;
    const padding = 20;

    // Draw semi-transparent backdrop
    this.ctx.save();
    this.ctx.fillStyle = 'rgba(0, 0, 0, 0.5)';
    this.ctx.fillRect(0, 0, this.width, this.height);
    this.ctx.restore();

    // Draw overlay background
    this.ctx.save();
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.98)';
    this.ctx.strokeStyle = 'rgba(0, 0, 0, 0.1)';
    this.ctx.lineWidth = 1;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 30;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 8;

    // Rounded rectangle
    const radius = 12;
    this.ctx.beginPath();
    this.ctx.moveTo(x + radius, y);
    this.ctx.lineTo(x + overlayWidth - radius, y);
    this.ctx.arcTo(x + overlayWidth, y, x + overlayWidth, y + radius, radius);
    this.ctx.lineTo(x + overlayWidth, y + overlayHeight - radius);
    this.ctx.arcTo(x + overlayWidth, y + overlayHeight, x + overlayWidth - radius, y + overlayHeight, radius);
    this.ctx.lineTo(x + radius, y + overlayHeight);
    this.ctx.arcTo(x, y + overlayHeight, x, y + overlayHeight - radius, radius);
    this.ctx.lineTo(x, y + radius);
    this.ctx.arcTo(x, y, x + radius, y, radius);
    this.ctx.closePath();
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.restore();

    // Title
    this.ctx.save();
    this.ctx.fillStyle = '#1e293b';
    this.ctx.font = 'bold 20px Inter, sans-serif';
    this.ctx.fillText('⌨️ Keyboard Shortcuts', x + padding, y + padding + 20);
    this.ctx.restore();

    // Close hint
    this.ctx.save();
    this.ctx.fillStyle = '#64748b';
    this.ctx.font = '12px Inter, sans-serif';
    this.ctx.fillText('Press H or ESC to close', x + padding, y + padding + 45);
    this.ctx.restore();

    // Shortcuts list
    const shortcuts = [
      { section: 'Navigation', items: [] },
      { key: 'Space + Drag', desc: 'Pan canvas' },
      { key: 'Mouse Wheel', desc: 'Zoom in/out' },
      { key: 'Ctrl + Wheel', desc: 'Precise zoom' },
      { key: 'R', desc: 'Reset view (zoom to fit)' },
      { section: 'Agents', items: [] },
      { key: 'Click Agent', desc: 'Select agent' },
      { key: 'Right-click', desc: 'Agent quick actions' },
      { key: 'Drag Agent', desc: 'Move agent position' },
      { section: 'Tasks', items: [] },
      { key: 'Click Task', desc: 'View task details' },
      { key: 'Drag Task', desc: 'Assign to agent' },
      { section: 'General', items: [] },
      { key: 'H', desc: 'Toggle this help' },
      { key: 'ESC', desc: 'Cancel/Close' }
    ];

    let currentY = y + padding + 70;
    const lineHeight = 28;
    const sectionSpacing = 10;

    this.ctx.save();
    shortcuts.forEach(item => {
      if (item.section) {
        // Section header
        this.ctx.fillStyle = '#1e293b';
        this.ctx.font = 'bold 14px Inter, sans-serif';
        this.ctx.fillText(item.section, x + padding, currentY);
        currentY += lineHeight + sectionSpacing;
      } else {
        // Shortcut item
        // Draw key badge
        const keyWidth = this.ctx.measureText(item.key).width + 16;
        this.ctx.fillStyle = 'rgba(29, 78, 216, 0.1)';
        this.ctx.strokeStyle = 'rgba(29, 78, 216, 0.3)';
        this.ctx.lineWidth = 1;
        const badgeRadius = 4;
        const badgeX = x + padding;
        const badgeY = currentY - 18;
        const badgeHeight = 24;
        this.ctx.beginPath();
        this.ctx.moveTo(badgeX + badgeRadius, badgeY);
        this.ctx.lineTo(badgeX + keyWidth - badgeRadius, badgeY);
        this.ctx.arcTo(badgeX + keyWidth, badgeY, badgeX + keyWidth, badgeY + badgeRadius, badgeRadius);
        this.ctx.lineTo(badgeX + keyWidth, badgeY + badgeHeight - badgeRadius);
        this.ctx.arcTo(badgeX + keyWidth, badgeY + badgeHeight, badgeX + keyWidth - badgeRadius, badgeY + badgeHeight, badgeRadius);
        this.ctx.lineTo(badgeX + badgeRadius, badgeY + badgeHeight);
        this.ctx.arcTo(badgeX, badgeY + badgeHeight, badgeX, badgeY + badgeHeight - badgeRadius, badgeRadius);
        this.ctx.lineTo(badgeX, badgeY + badgeRadius);
        this.ctx.arcTo(badgeX, badgeY, badgeX + badgeRadius, badgeY, badgeRadius);
        this.ctx.closePath();
        this.ctx.fill();
        this.ctx.stroke();

        // Draw key text
        this.ctx.fillStyle = '#1d4ed8';
        this.ctx.font = 'bold 12px Inter, monospace';
        this.ctx.fillText(item.key, badgeX + 8, currentY);

        // Draw description
        this.ctx.fillStyle = '#475569';
        this.ctx.font = '13px Inter, sans-serif';
        this.ctx.fillText(item.desc, badgeX + keyWidth + 15, currentY);

        currentY += lineHeight;
      }
    });
    this.ctx.restore();
  }

  // ==================== COMBINER NODE RENDERING ====================

  /**
   * Draw all workflow connections
   */
  drawWorkflowConnections() {
    // Get mouse position in canvas coordinates for hover detection
    const rect = this.canvas.getBoundingClientRect();
    const mouseCanvasX = this.lastMouseX ? (this.lastMouseX - this.offsetX) / this.scale : -9999;
    const mouseCanvasY = this.lastMouseY ? (this.lastMouseY - this.offsetY) / this.scale : -9999;

    this.connections.forEach(conn => {
      const fromPos = this.getPortPosition(conn.from, conn.fromPort);
      const toPos = this.getPortPosition(conn.to, conn.toPort);

      if (!fromPos || !toPos) return;

      // Convert back to canvas coordinates
      const fromX = (fromPos.x - this.offsetX) / this.scale;
      const fromY = (fromPos.y - this.offsetY) / this.scale;
      const toX = (toPos.x - this.offsetX) / this.scale;
      const toY = (toPos.y - this.offsetY) / this.scale;

      // Check if mouse is hovering over this connection
      const hoveredConn = this.getConnectionAtPosition(mouseCanvasX, mouseCanvasY, 15);
      const isHovered = hoveredConn && hoveredConn.id === conn.id;

      // Draw bezier curve connection
      this.ctx.save();
      this.ctx.strokeStyle = isHovered ? '#ff6b6b' : conn.color; // Red on hover
      this.ctx.lineWidth = isHovered ? 5 : 3; // Thicker on hover
      this.ctx.lineCap = 'round';

      // Add glow effect (stronger on hover)
      this.ctx.shadowColor = isHovered ? '#ff6b6b' : conn.color;
      this.ctx.shadowBlur = isHovered ? 15 : 10;

      this.ctx.beginPath();
      this.ctx.moveTo(fromX, fromY);

      // Bezier curve for smooth connection
      const controlOffset = Math.abs(toY - fromY) / 2;
      this.ctx.bezierCurveTo(
        fromX, fromY + controlOffset,
        toX, toY - controlOffset,
        toX, toY
      );

      this.ctx.stroke();
      this.ctx.restore();

      // Draw arrow at destination
      const arrowSize = isHovered ? 10 : 8; // Larger arrow on hover
      const angle = Math.atan2(toY - fromY, toX - fromX);
      this.ctx.save();
      this.ctx.fillStyle = isHovered ? '#ff6b6b' : conn.color;
      this.ctx.beginPath();
      this.ctx.moveTo(toX, toY);
      this.ctx.lineTo(
        toX - arrowSize * Math.cos(angle - Math.PI / 6),
        toY - arrowSize * Math.sin(angle - Math.PI / 6)
      );
      this.ctx.lineTo(
        toX - arrowSize * Math.cos(angle + Math.PI / 6),
        toY - arrowSize * Math.sin(angle + Math.PI / 6)
      );
      this.ctx.closePath();
      this.ctx.fill();
      this.ctx.restore();

      // Draw delete icon on hover
      if (isHovered) {
        // Calculate midpoint of connection for delete button
        const midX = (fromX + toX) / 2;
        const midY = (fromY + toY) / 2;

        // Draw delete button circle
        this.ctx.save();
        this.ctx.fillStyle = '#dc3545';
        this.ctx.strokeStyle = '#ffffff';
        this.ctx.lineWidth = 2;
        this.ctx.beginPath();
        this.ctx.arc(midX, midY, 12, 0, Math.PI * 2);
        this.ctx.fill();
        this.ctx.stroke();

        // Draw X icon
        this.ctx.strokeStyle = '#ffffff';
        this.ctx.lineWidth = 2;
        this.ctx.lineCap = 'round';
        this.ctx.beginPath();
        this.ctx.moveTo(midX - 5, midY - 5);
        this.ctx.lineTo(midX + 5, midY + 5);
        this.ctx.moveTo(midX + 5, midY - 5);
        this.ctx.lineTo(midX - 5, midY + 5);
        this.ctx.stroke();
        this.ctx.restore();

        // Show tooltip
        this.ctx.save();
        this.ctx.font = '11px system-ui';
        this.ctx.fillStyle = 'rgba(0, 0, 0, 0.8)';
        this.ctx.textAlign = 'center';
        const tooltipText = 'Right-click to delete';
        const textWidth = this.ctx.measureText(tooltipText).width;
        this.ctx.fillRect(midX - textWidth / 2 - 6, midY - 30, textWidth + 12, 18);
        this.ctx.fillStyle = '#ffffff';
        this.ctx.fillText(tooltipText, midX, midY - 18);
        this.ctx.restore();
      }
    });
  }

  /**
   * Draw all combiner nodes
   */
  drawCombinerNodes() {
    this.combinerNodes.forEach(node => {
      this.ctx.save();

      // Draw diamond/rectangle shape
      const x = node.x;
      const y = node.y;
      const w = node.width;
      const h = node.height;

      // Background with gradient
      const gradient = this.ctx.createLinearGradient(x, y, x, y + h);
      gradient.addColorStop(0, node.color);
      gradient.addColorStop(1, this.lightenColor(node.color, 20));

      this.ctx.fillStyle = gradient;
      this.ctx.strokeStyle = this.darkenColor(node.color, 20);
      this.ctx.lineWidth = 2;

      // Draw rounded rectangle
      const radius = 8;
      this.ctx.beginPath();
      this.ctx.moveTo(x + radius, y);
      this.ctx.lineTo(x + w - radius, y);
      this.ctx.arcTo(x + w, y, x + w, y + radius, radius);
      this.ctx.lineTo(x + w, y + h - radius);
      this.ctx.arcTo(x + w, y + h, x + w - radius, y + h, radius);
      this.ctx.lineTo(x + radius, y + h);
      this.ctx.arcTo(x, y + h, x, y + h - radius, radius);
      this.ctx.lineTo(x, y + radius);
      this.ctx.arcTo(x, y, x + radius, y, radius);
      this.ctx.closePath();
      this.ctx.fill();
      this.ctx.stroke();

      // Draw icon
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = '24px Arial';
      this.ctx.textAlign = 'center';
      this.ctx.textBaseline = 'middle';
      this.ctx.fillText(node.icon, x + w / 2, y + h / 2 - 10);

      // Draw label
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 12px Inter, sans-serif';
      this.ctx.fillText(node.name.toUpperCase(), x + w / 2, y + h / 2 + 15);

      // Draw input ports (top)
      const numInputs = Math.max(node.inputPorts.length, 1); // At least 1 input port when empty
      const portSpacing = w / (numInputs + 1);
      for (let i = 0; i < numInputs; i++) {
        const portX = x + portSpacing * (i + 1);
        const portY = y - 5;
        this.drawPort(portX, portY, 'input', node.color, 'down');
      }

      // Draw output port (bottom)
      const outputX = x + w / 2;
      const outputY = y + h + 5;
      this.drawPort(outputX, outputY, 'output', node.color);

      // Draw delete button (always visible in top-right corner)
      const deleteButtonSize = 20;
      const deleteButtonX = x + w - deleteButtonSize / 2;
      const deleteButtonY = y - deleteButtonSize / 2;

      // Store bounds for click detection
      if (!node.deleteButtonBounds) node.deleteButtonBounds = {};
      node.deleteButtonBounds = {
        x: deleteButtonX - deleteButtonSize / 2,
        y: deleteButtonY - deleteButtonSize / 2,
        width: deleteButtonSize,
        height: deleteButtonSize
      };

      // Draw delete button background
      this.ctx.fillStyle = '#ef4444';
      this.ctx.beginPath();
      this.ctx.arc(deleteButtonX, deleteButtonY, deleteButtonSize / 2, 0, Math.PI * 2);
      this.ctx.fill();

      // Draw X icon
      this.ctx.strokeStyle = '#ffffff';
      this.ctx.lineWidth = 2;
      this.ctx.lineCap = 'round';
      const crossSize = 6;
      this.ctx.beginPath();
      this.ctx.moveTo(deleteButtonX - crossSize / 2, deleteButtonY - crossSize / 2);
      this.ctx.lineTo(deleteButtonX + crossSize / 2, deleteButtonY + crossSize / 2);
      this.ctx.moveTo(deleteButtonX + crossSize / 2, deleteButtonY - crossSize / 2);
      this.ctx.lineTo(deleteButtonX - crossSize / 2, deleteButtonY + crossSize / 2);
      this.ctx.stroke();

      // RUN button (bottom-left corner)
      const runButtonWidth = 50;
      const runButtonHeight = 18;
      const runX = x + 8;
      const runY = y + h - runButtonHeight - 6;

      node.runButtonBounds = {
        x: runX,
        y: runY,
        width: runButtonWidth,
        height: runButtonHeight
      };

      this.ctx.fillStyle = '#10b981';
      this.ctx.strokeStyle = '#059669';
      this.ctx.lineWidth = 1.5;
      this.roundRect(runX, runY, runButtonWidth, runButtonHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 10px Inter, sans-serif';
      this.ctx.textAlign = 'center';
      this.ctx.textBaseline = 'middle';
      this.ctx.fillText('▶ RUN', runX + runButtonWidth / 2, runY + runButtonHeight / 2);

      // Assign output button (bottom-right corner)
      const assignButtonWidth = 60;
      const assignButtonHeight = 18;
      const assignX = x + w - assignButtonWidth - 8;
      const assignY = y + h - assignButtonHeight - 6;

      node.assignButtonBounds = {
        x: assignX,
        y: assignY,
        width: assignButtonWidth,
        height: assignButtonHeight
      };

      this.ctx.fillStyle = '#3b82f6';
      this.ctx.strokeStyle = '#1d4ed8';
      this.ctx.lineWidth = 1.5;
      this.roundRect(assignX, assignY, assignButtonWidth, assignButtonHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 10px Inter, sans-serif';
      this.ctx.textAlign = 'center';
      this.ctx.textBaseline = 'middle';
      this.ctx.fillText('Assign', assignX + assignButtonWidth / 2, assignY + assignButtonHeight / 2);

      this.ctx.restore();
    });
  }

  /**
   * Draw a connection port
   */
  drawPort(x, y, type, color, orientation = 'auto') {
    this.ctx.save();
    const size = 10;
    const isInput = type === 'input';

    // Determine direction
    let pointUp = true;
    if (orientation === 'down') {
      pointUp = false;
    } else if (orientation === 'up') {
      pointUp = true;
    } else {
      // auto: input points up, output points down
      pointUp = isInput;
    }

    // Triangles: input points upward, output points downward
    const points = pointUp
      ? [
          { x: x, y: y - size },      // top
          { x: x - size, y: y + size },
          { x: x + size, y: y + size },
        ]
      : [
          { x: x, y: y + size },      // bottom
          { x: x - size, y: y - size },
          { x: x + size, y: y - size },
        ];

    // Outer triangle
    this.ctx.beginPath();
    this.ctx.moveTo(points[0].x, points[0].y);
    this.ctx.lineTo(points[1].x, points[1].y);
    this.ctx.lineTo(points[2].x, points[2].y);
    this.ctx.closePath();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.strokeStyle = color;
    this.ctx.lineWidth = 2;
    this.ctx.fill();
    this.ctx.stroke();

    // Inner accent
    const innerShrink = 4;
    const innerPoints = points.map(p => ({
      x: x + (p.x - x) * ((size - innerShrink) / size),
      y: y + (p.y - y) * ((size - innerShrink) / size),
    }));
    this.ctx.beginPath();
    this.ctx.moveTo(innerPoints[0].x, innerPoints[0].y);
    this.ctx.lineTo(innerPoints[1].x, innerPoints[1].y);
    this.ctx.lineTo(innerPoints[2].x, innerPoints[2].y);
    this.ctx.closePath();
    this.ctx.fillStyle = color;
    this.ctx.fill();

    this.ctx.restore();
  }

  /**
   * Draw connection being dragged
   */
  drawDraggingConnection() {
    if (!this.connectionDragStart) return;

    const fromPos = this.getPortPosition(
      this.connectionDragStart.nodeId,
      this.connectionDragStart.portId
    );

    if (!fromPos) return;

    const fromX = (fromPos.x - this.offsetX) / this.scale;
    const fromY = (fromPos.y - this.offsetY) / this.scale;

    // Mouse position in canvas coordinates
    const rect = this.canvas.getBoundingClientRect();
    const mouseX = (this.lastMouseX - this.offsetX) / this.scale;
    const mouseY = (this.lastMouseY - this.offsetY) / this.scale;

    this.ctx.save();
    this.ctx.strokeStyle = '#6366f1';
    this.ctx.lineWidth = 3;
    this.ctx.setLineDash([5, 5]);
    this.ctx.lineCap = 'round';

    this.ctx.beginPath();
    this.ctx.moveTo(fromX, fromY);
    this.ctx.lineTo(mouseX, mouseY);
    this.ctx.stroke();

    this.ctx.restore();
  }

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
