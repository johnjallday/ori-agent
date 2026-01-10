import { AgentCanvasForms } from './agent-canvas-forms.js';
import { apiGet, apiPost, apiPut, apiPatch, apiDelete } from './agent-canvas-api.js';
import { connectProgressStream } from './agent-canvas-events.js';
import { executeTask as tasksExecuteTask, rerunTask as tasksRerunTask, linkTaskResult as tasksLinkTaskResult } from './agent-canvas-tasks.js';
import { AgentCanvasState, EVENT_TYPES } from './agent-canvas-state.js';
import { AgentCanvasRenderer } from './agent-canvas-renderer.js';
import { AgentCanvasInteractionHandler } from './agent-canvas-interactions.js';
import { AgentCanvasLayoutManager } from './agent-canvas-layout.js';
import { AgentCanvasAnimationController } from './agent-canvas-animation.js';
import { AgentCanvasTimelineManager } from './agent-canvas-timeline.js';
import { AgentCanvasHelpers } from './agent-canvas-helpers.js';
import { AgentCanvasPanelManager } from './agent-canvas-panels.js';
import { AgentCanvasNotifications } from './agent-canvas-notifications.js';
import { AgentCanvasContextMenu } from './agent-canvas-context-menu.js';
import { AgentCanvasEventHandler } from './agent-canvas-event-handler.js';
import { AgentCanvasInitialization } from './agent-canvas-init.js';

/**
 * AgentCanvas - Visual canvas for real-time agent collaboration
 * Displays agents as nodes with tasks flowing between them
 */
class AgentCanvas {
  constructor(canvasId, studioId) {
    console.log('🎨 AgentCanvas constructor called', { canvasId, studioId });

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

    // Initialize helpers module
    this.helpers = new AgentCanvasHelpers(this.state, this);

    // Initialize panel manager
    this.panels = new AgentCanvasPanelManager(this.state, this);

    // Initialize notifications module
    this.notifications = new AgentCanvasNotifications(this.state, this);


    // Initialize context menu module
    this.contextMenu = new AgentCanvasContextMenu(this.state, this);

    // Initialize event handler module
    this.eventHandler = new AgentCanvasEventHandler(this.state, this);

    // Initialize initialization module
    this.initModule = new AgentCanvasInitialization(this.state, this);

    // Mouse event listeners - delegate to interaction handler
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
    this.state.on(EVENT_TYPES.ATTACHMENT_MOVED, () => this.draw());
    this.state.on(EVENT_TYPES.CANVAS_PANNED, () => this.draw());
    this.state.on(EVENT_TYPES.CANVAS_ZOOMED, () => this.draw());

    // Start periodic countdown timer for scheduler nodes (update every 10 seconds)
    this.countdownTimer = setInterval(() => {
      if (this.state.schedulerNodes && this.state.schedulerNodes.length > 0) {
        this.draw(); // Redraw to update countdown displays
      }
    }, 10000); // 10 seconds

    // Window resize listener
    window.addEventListener('resize', () => {
      this.initModule.resize();
      this.draw();
    });

    // ResizeObserver for container changes (e.g., sidebar toggle)
    // This handles cases where the canvas container size changes without a window resize
    this.resizeObserver = new ResizeObserver(() => {
      this.initModule.resize();
      this.draw();
    });
    this.resizeObserver.observe(this.canvas.parentElement);
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

  get attachments() { return this.state.attachments; }
  set attachments(value) { this.state.setAttachments(value); }

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

  get isDraggingAttachment() { return this.state.isDraggingAttachment; }
  set isDraggingAttachment(value) { this.state.isDraggingAttachment = value; }

  get draggedTask() { return this.state.draggedTask; }
  set draggedTask(value) { this.state.draggedTask = value; }

  get draggedAttachment() { return this.state.draggedAttachment; }
  set draggedAttachment(value) { this.state.draggedAttachment = value; }

  get isDraggingConnection() { return this.state.isDraggingConnection; }
  set isDraggingConnection(value) { this.state.isDraggingConnection = value; }

  get draggedConnection() { return this.state.draggedConnection; }
  set draggedConnection(value) { this.state.draggedConnection = value; }

  get connectionDragStart() { return this.state.connectionDragStart; }
  set connectionDragStart(value) { this.state.connectionDragStart = value; }


  get dragStartX() { return this.state.dragStartX; }
  set dragStartX(value) { this.state.dragStartX = value; }

  get dragStartY() { return this.state.dragStartY; }
  set dragStartY(value) { this.state.dragStartY = value; }

  // Keyboard state
  get spacePressed() { return this.state.spacePressed; }
  set spacePressed(value) { this.state.spacePressed = value; }

  get ctrlPressed() { return this.state.ctrlPressed; }
  set ctrlPressed(value) { this.state.ctrlPressed = value; }

  // Selection
  get selectedNodes() { return this.state.getSelectedNodes(); }
  get selectionCount() { return this.state.getSelectionCount(); }
  get hasMultipleSelected() { return this.state.hasMultipleSelected(); }
  clearSelection() { this.state.clearSelection(); this.draw(); }

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

  // Connections
  get connections() { return this.state.connections; }
  set connections(value) { this.state.connections = value; }

  // Execution logs
  get executionLogs() { return this.state.executionLogs; }
  set executionLogs(value) { this.state.executionLogs = value; }

  // Callbacks
  get onAgentClick() { return this.state.onAgentClick; }
  set onAgentClick(value) { this.state.onAgentClick = value; }


  get onTimelineEvent() { return this.state.onTimelineEvent; }
  set onTimelineEvent(value) { this.state.onTimelineEvent = value; }

  // ==================== METHODS ====================









  // Initialization methods delegated to initialization module
  init() { return this.initModule.init(); }
  resize() { return this.initModule.resize(); }
  initializeAgents() { return this.initModule.initializeAgents(); }

  // Animation methods delegated to animation module
  updateChains() { return this.animation.updateChains(); }
  createChainParticle(fromTask, toTask) { return this.animation.createChainParticle(fromTask, toTask); }
  updateChainParticles() { return this.animation.updateChainParticles(); }



  // Timeline methods delegated to timeline module
  addTimelineEvent(eventData) { return this.timeline.addTimelineEvent(eventData); }
  toggleTimeline() { return this.timeline.toggleTimeline(); }
  animateTimelinePanel(expanding) { return this.timeline.animateTimelinePanel(expanding); }








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

    // Draw scheduler/task connections
    this.renderer.drawConnections();

    // Draw chain connections (highlighted paths for active chains)
    this.renderer.drawChainConnections();

    // Draw task flows
    this.renderer.drawTaskFlows();

    // Draw attachments
    this.renderer.drawAttachments();

    // Draw scheduler nodes
    this.renderer.drawSchedulerNodes();

    // Draw store nodes
    this.renderer.drawStoreNodes();

    // Draw agent-to-store connections
    this.renderer.drawAgentToStoreConnections();

    // Draw task input connections (task-to-task and task-to-merge)
    this.renderer.drawResultConnections();

    // Draw particles
    // this.renderer.drawParticles();

    // Draw chain particles (disabled - grey shooting animation)
    // this.renderer.drawChainParticles();

    // Draw agents
    this.renderer.drawAgents();

    // Draw workflow connections
    this.renderer.drawWorkflowConnections();

    // Draw dragging connection line (if dragging)
    if (this.isDraggingConnection && this.connectionDragStart) {
      this.renderer.drawDraggingConnection();
    }

    this.ctx.restore();

    // Draw marquee selection rectangle (if selecting)
    this.renderer.drawMarqueeSelection();

    // Draw mission OUTSIDE the transform context (so it stays fixed at top)
    this.renderer.drawMission();

    // Draw expanded task panel OUTSIDE the transform context (fixed position)
    if (this.state.expandedPanelWidth > 0) {
      this.renderer.drawExpandedTaskPanel();
    }

    // Draw expanded agent panel OUTSIDE the transform context (fixed position)
    if (this.state.expandedAgentPanelWidth > 0) {
      this.renderer.drawExpandedAgentPanel();
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

    // Draw multi-select context menu (if visible)
    if (this.state.multiSelectContextMenu) {
      this.renderer.drawMultiSelectContextMenu();
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





  // Helper function to draw rounded rectangle

  // Mouse interaction handlers












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



  // Layout methods delegated to layout module
  autoLayoutTasks() { return this.layout.autoLayoutTasks(); }

  zoomToFitContent() { return this.layout.zoomToFitContent(); }


  calculateTaskLevels() { return this.layout.calculateTaskLevels(); }

  // Helper methods delegated to helpers module
  getAgentColor(index) { return this.helpers.getAgentColor(index); }
  getNodeById(nodeId) { return this.helpers.getNodeById(nodeId); }
  getPortPosition(nodeId, portId) { return this.helpers.getPortPosition(nodeId, portId); }
  getPortAtPosition(x, y) { return this.helpers.getPortAtPosition(x, y); }
  getConnectionAtPosition(x, y, threshold = 10) { return this.helpers.getConnectionAtPosition(x, y, threshold); }
  getLatestTaskForAgent(agentName) { return this.helpers.getLatestTaskForAgent(agentName); }
  lightenColor(color, percent) { return this.helpers.lightenColor(color, percent); }
  darkenColor(color, percent) { return this.helpers.darkenColor(color, percent); }

  // Notification methods delegated to notifications module
  showNotification(message, type = 'info') { return this.notifications.showNotification(message, type); }
  dismissNotification(id) { return this.notifications.dismissNotification(id); }
  addExecutionLog(taskId, type, message) { return this.notifications.addExecutionLog(taskId, type, message); }
  showExecutionLog(task) { return this.notifications.showExecutionLog(task); }
  getEventIcon(type) { return this.notifications.getEventIcon(type); }
  getEventColor(type) { return this.notifications.getEventColor(type); }
  getEventMessage(event) { return this.notifications.getEventMessage(event); }

  // Panel methods delegated to panels module
  toggleTaskPanel(task) { return this.panels.toggleTaskPanel(task); }
  closeTaskPanel() { return this.panels.closeTaskPanel(); }
  animatePanel(expanding) { return this.panels.animatePanel(expanding); }
  toggleAgentPanel(agent) { return this.panels.toggleAgentPanel(agent); }
  closeAgentPanel() { return this.panels.closeAgentPanel(); }
  animateAgentPanel(expanding) { return this.panels.animateAgentPanel(expanding); }
  toggleHelpOverlay() { return this.panels.toggleHelpOverlay(); }

  // Context menu methods delegated to context menu module
  toggleAssignmentMode(task) { return this.contextMenu.toggleAssignmentMode(task); }
  handleContextMenuAction(action, agent) { return this.contextMenu.handleContextMenuAction(action, agent); }
  handleMultiSelectAction(action) { return this.contextMenu.handleMultiSelectAction(action); }

  // Event handler methods delegated to event handler module
  connectEventStream() { return this.eventHandler.connectEventStream(); }
  handleTaskEvent(eventData) { return this.eventHandler.handleTaskEvent(eventData); }
  handleEvent(event) { return this.eventHandler.handleEvent(event); }
  addTask(taskData) { return this.eventHandler.addTask(taskData); }
  updateTaskStatus(taskId, status) { return this.eventHandler.updateTaskStatus(taskId, status); }
  setAgentStatus(agentName, status) { return this.eventHandler.setAgentStatus(agentName, status); }
  addMessage(messageData) { return this.eventHandler.addMessage(messageData); }
  setMission(missionText) { return this.eventHandler.setMission(missionText); }

  async assignTaskToAgent(agent) {
    // Update task assignment via API
    try {
      const result = await apiPut(`/api/orchestration/tasks`, {
        task_id: this.assignmentSourceTask.id,
        to: agent.name,
        assigned_node_id: agent.nodeId || agent.name
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
        task.assigned_node_id = agent.nodeId || agent.name; // track which instance was chosen
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

  async linkTaskResult(sourceTaskId, targetTaskId) {
    return tasksLinkTaskResult(this, sourceTaskId, targetTaskId);
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

  /**
   * Get color for event type
   */

  /**
   * Get formatted message for event
   */

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
      const params = new URLSearchParams({ id: task.id });
      if (this.studioId) {
        params.append('workspace_id', this.studioId);
      }

      await apiDelete(`/api/orchestration/tasks?${params.toString()}`);

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
      // Reload to ensure consistency
      setTimeout(() => this.init(), 500);

    } catch (error) {
      console.error('❌ Error deleting task:', error);
      alert('Failed to delete task: ' + error.message);
    }
  }

  /**
   * Remove an agent from the studio
   */
  async removeAgentFromStudio(nodeId, instanceNumber) {
    // Extract agent name from nodeId (e.g., "default-node-2" -> "default")
    const agentName = nodeId.split('-node-')[0];

    if (!confirm(`Remove agent "${agentName} #${instanceNumber}" from this studio?\n\nThis will only remove it from the canvas, not delete the agent.`)) {
      return;
    }

    try {
      // Use name:instanceNumber format for specific instance removal
      const agentIdentifier = `${agentName}:${instanceNumber}`;
      const response = await fetch(`/api/studios/${this.studioId}/agents/${agentIdentifier}`, {
        method: 'DELETE'
      });

      if (response.ok) {
        console.log('✅ Agent instance removed from studio:', agentIdentifier);

        // Remove only this specific agent instance from local state
        const agents = this.state.agents.filter(a => a.nodeId !== nodeId);
        this.state.setAgents(agents);

        // Unassign only tasks that were specifically assigned to this agent instance (by nodeId)
        // Do NOT match by agent name alone - that would unassign tasks from other instances with the same name
        const affectedTasks = [];
        const updatedTasks = this.state.tasks.map(t => {
          if (!t) return t;
          const targetMatches = t.assigned_node_id === nodeId;
          if (targetMatches) {
            affectedTasks.push(t.id);
            return {
              ...t,
              to: 'unassigned',
              assigned_node_id: '',
              status: 'pending',
              progress: 0,
              result: null,
              error: null,
              started_at: null,
              completed_at: null
            };
          }
          return t;
        });
        if (affectedTasks.length > 0) {
          this.state.setTasks(updatedTasks);

          // Persist unassignment in the backend (fire-and-forget)
          affectedTasks.forEach(taskId => {
            fetch('/api/orchestration/tasks', {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                task_id: taskId,
                to: 'unassigned',
                assigned_node_id: '',
                status: 'pending',
                result: null,
                error: null
              })
            }).catch(err => console.error('Failed to unassign task after agent removal:', err));
          });
        }

        // Redraw canvas
        this.draw();

        // Show notification
        const unassignNote = affectedTasks.length > 0 ? ` and unassigned ${affectedTasks.length} task(s)` : '';
        this.showNotification(`Agent "${agentName}" removed${unassignNote}`, 'success');

        // Reload to ensure consistency
        setTimeout(() => this.init(), 500);
      } else {
        alert('Failed to remove agent from studio');
      }
    } catch (error) {
      console.error('❌ Error removing agent:', error);
      alert('Failed to remove agent: ' + error.message);
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
   * Delete a connection by id (utility for context menu)
   */
  deleteConnection(connectionId) {
    if (!connectionId) return;
    const idx = this.state.connections.findIndex(c => c.id === connectionId);
    if (idx !== -1) {
      const conn = this.state.connections[idx];

      // If this is a scheduler→task connection, clear the scheduler's target_task_id
      const fromNode = this.helpers.getNodeById(conn.from);
      if (fromNode?.type === 'scheduler') {
        this.updateSchedulerTargetTask(fromNode.node.id, null).catch(err => {
          console.error('Failed to clear scheduler target task', err);
        });
      }

      this.state.connections.splice(idx, 1);
      this.saveLayout();
      this.draw();
    }
  }

  /**
   * Delete an attachment node
   */
  async deleteAttachment(attachment) {
    if (!attachment || !attachment.id || !this.studioId) {
      alert('Cannot delete attachment: missing ID or workspace');
      return;
    }
    try {
      await apiDelete(`/api/studios/${this.studioId}/attachments/${attachment.id}`);
      this.state.removeAttachment(attachment.id);
      this.saveLayout();
      this.draw();
      this.notifications?.showNotification?.('Attachment deleted', 'success');
    } catch (err) {
      console.error('Failed to delete attachment', err);
      alert('Failed to delete attachment: ' + (err?.message || err));
    }
  }

  /**
   * Delete a scheduler node
   */
  async deleteSchedulerNode(schedulerNode) {
    const nodeId = schedulerNode?.canvas_node_id;
    if (!schedulerNode || !nodeId || !this.studioId) {
      alert('Cannot delete scheduler node: missing ID or workspace');
      return;
    }
    try {
      await apiDelete(`/api/orchestration/workspaces/${this.studioId}/scheduler-nodes/${nodeId}?studio_id=${this.studioId}`);
      this.state.removeSchedulerNode(schedulerNode.id);
      this.saveLayout();
      this.draw();
      this.notifications?.showNotification?.('Scheduler node deleted', 'success');
    } catch (err) {
      console.error('Failed to delete scheduler node', err);
      alert('Failed to delete scheduler node: ' + (err?.message || err));
    }
  }

  /**
   * Delete a store node
   */
  async deleteStoreNode(storeNode) {
    const nodeId = storeNode?.canvas_node_id;
    if (!storeNode || !nodeId || !this.studioId) {
      alert('Cannot delete store node: missing ID or workspace');
      return;
    }
    try {
      await apiDelete(`/api/studios/${this.studioId}/canvas/store-nodes/${nodeId}`);
      this.state.removeStoreNode(storeNode.id);
      this.saveLayout();
      this.draw();
      this.notifications?.showNotification?.('Store node deleted', 'success');
    } catch (err) {
      console.error('Failed to delete store node', err);
      alert('Failed to delete store node: ' + (err?.message || err));
    }
  }

  /**
   * Toggle store node assignment mode
   */
  toggleStoreAssignmentMode(storeNode) {
    if (this.state.storeAssignmentMode &&
        this.state.storeAssignmentSource?.canvas_node_id === storeNode.canvas_node_id) {
      // Cancel if clicking same store node
      this.state.storeAssignmentMode = false;
      this.state.storeAssignmentSource = null;
      this.state.storeAssignmentMouseX = 0;
      this.state.storeAssignmentMouseY = 0;
      this.canvas.style.cursor = 'grab';
      console.log('Store assignment mode cancelled');
    } else {
      // Enter assignment mode
      this.state.storeAssignmentMode = true;
      this.state.storeAssignmentSource = storeNode;
      this.canvas.style.cursor = 'crosshair';
      console.log('Store assignment mode activated for:', storeNode.name);
    }
    this.draw();
  }

  /**
   * Assign agent to store node
   */
  async assignAgentToStore(agent) {
    const storeNode = this.state.storeAssignmentSource;
    if (!storeNode || !agent) return;

    try {
      // Update the store node's agent_node_id
      await apiPatch(`/api/studios/${this.studioId}/canvas/store-nodes/${storeNode.canvas_node_id}`, {
        agent_node_id: agent.nodeId || agent.id
      });

      // Update local state
      storeNode.agent_node_id = agent.nodeId || agent.id;

      // Exit assignment mode
      this.state.storeAssignmentMode = false;
      this.state.storeAssignmentSource = null;
      this.canvas.style.cursor = 'grab';

      this.saveLayout();
      this.draw();
      this.notifications?.showNotification?.(`Agent "${agent.name}" assigned to store "${storeNode.name}"`, 'success');
    } catch (err) {
      console.error('Failed to assign agent to store', err);
      alert('Failed to assign agent: ' + (err?.message || err));
    }
  }

  /**
   * Manually trigger a scheduler node
   */
  async triggerSchedulerNode(schedulerNode) {
    const nodeId = schedulerNode?.canvas_node_id;
    if (!schedulerNode || !nodeId || !this.studioId) {
      alert('Cannot trigger scheduler node: missing ID or workspace');
      return;
    }

    // Prevent multiple simultaneous triggers
    if (this._triggering === nodeId) {
      console.warn('Trigger already in progress for node:', nodeId);
      return;
    }

    this._triggering = nodeId;
    console.log('🎯 Triggering scheduler node:', nodeId);

    try {
      await apiPost(`/api/orchestration/workspaces/${this.studioId}/scheduler-nodes/${nodeId}/trigger?studio_id=${this.studioId}`, {});
      this.notifications?.showNotification?.('Scheduler node triggered', 'success');
    } catch (err) {
      console.error('Failed to trigger scheduler node', err);
      alert('Failed to trigger scheduler node: ' + (err?.message || err));
    } finally {
      // Clear the lock after a short delay to prevent accidental rapid clicks
      setTimeout(() => {
        this._triggering = null;
      }, 1000);
    }
  }

  /**
   * Toggle scheduler enabled/paused state
   */
  async toggleSchedulerEnabled(schedulerNode) {
    const nodeId = schedulerNode?.canvas_node_id;
    if (!schedulerNode || !nodeId || !this.studioId) {
      alert('Cannot toggle scheduler: missing ID or workspace');
      return;
    }

    const newEnabled = schedulerNode.enabled === false ? true : false;
    const action = newEnabled ? 'resume' : 'pause';

    console.log(`⏯️ ${action} scheduler node:`, nodeId);

    try {
      await apiPatch(`/api/orchestration/workspaces/${this.studioId}/scheduler-nodes/${nodeId}?studio_id=${this.studioId}`, {
        enabled: newEnabled
      });

      // Update local state
      schedulerNode.enabled = newEnabled;

      this.notifications?.showNotification?.(`Scheduler ${newEnabled ? 'resumed' : 'paused'}`, 'success');
      this.draw();
    } catch (err) {
      console.error('Failed to toggle scheduler enabled state', err);
      alert('Failed to toggle scheduler: ' + (err?.message || err));
    }
  }

  /**
   * Toggle scheduler assignment mode
   */
  toggleSchedulerAssignmentMode(schedulerNode) {
    if (this.state.schedulerAssignmentMode &&
        this.state.schedulerAssignmentSource?.canvas_node_id === schedulerNode.canvas_node_id) {
      // Cancel if clicking same scheduler
      this.state.schedulerAssignmentMode = false;
      this.state.schedulerAssignmentSource = null;
      this.state.schedulerAssignmentMouseX = 0;
      this.state.schedulerAssignmentMouseY = 0;
      this.canvas.style.cursor = 'grab';
      console.log('Scheduler assignment mode cancelled');
    } else {
      // Enter assignment mode
      this.state.schedulerAssignmentMode = true;
      this.state.schedulerAssignmentSource = schedulerNode;
      this.canvas.style.cursor = 'crosshair';
      console.log('Scheduler assignment mode activated for:', schedulerNode.name);
    }
    this.draw();
  }

  /**
   * Assign scheduler node to a task
   */
  async assignSchedulerToTask(task) {
    const schedulerNode = this.state.schedulerAssignmentSource;
    if (!schedulerNode || !task) return;

    try {
      await this.updateSchedulerTargetTask(schedulerNode.canvas_node_id, task.id);

      // Add a visual connection if it doesn't exist
      const schedulerNodeId = schedulerNode.canvas_node_id || schedulerNode.id;
      const existing = this.state.connections.find(conn =>
        conn.from === schedulerNodeId && conn.to === task.id
      );
      if (!existing) {
        const conn = {
          id: `conn-${Date.now()}-${Math.random().toString(16).slice(2, 8)}`,
          from: schedulerNodeId,
          fromPort: 'out',
          to: task.id,
          toPort: 'in',
          color: '#cbd5e1',
          animated: false
        };
        this.state.connections.push(conn);
        this.saveLayout();
        this.draw();
      }

      this.notifications?.showNotification?.(`Scheduler linked to task "${task.description || task.id}"`, 'success');
      console.log('✅ Scheduler linked to task:', task.id);
    } catch (err) {
      console.error('Failed to link scheduler to task', err);
      alert('Failed to link scheduler to task: ' + (err?.message || err));
    }
  }

  /**
   * Update scheduler node's target task ID
   */
  async updateSchedulerTargetTask(schedulerNodeId, targetTaskId) {
    if (!schedulerNodeId || !this.studioId) {
      throw new Error('Cannot update scheduler: missing ID or workspace');
    }
    try {
      await apiPut(`/api/orchestration/workspaces/${this.studioId}/scheduler-nodes/${schedulerNodeId}?studio_id=${this.studioId}`, {
        target_task_id: targetTaskId
      });
      // Update local state (search by canvas_node_id)
      const scheduler = this.state.schedulerNodes.find(s => s.canvas_node_id === schedulerNodeId);
      if (scheduler) {
        scheduler.target_task_id = targetTaskId;
      }
    } catch (err) {
      console.error('Failed to update scheduler target task', err);
      throw err;
    }
  }

  /**
   * Find the most recent task associated with an agent so combiners can treat
   * direct agent connections as inputs.
   */

  /**
   * Execute a combiner node - sets up and executes the output task with merged inputs
   */
  /**
   * Execute a combiner node - executes the combiner's internal task with merged inputs
   */
  async executeCombiner(combiner) {
    return combinerExecute(this, combiner);
  }



  destroy() {
    if (this.animationFrame) {
      cancelAnimationFrame(this.animationFrame);
    }
    if (this.eventSource) {
      this.eventSource.close();
    }
    if (this.countdownTimer) {
      clearInterval(this.countdownTimer);
    }
    if (this.resizeObserver) {
      this.resizeObserver.disconnect();
    }
  }

  async saveLayout() { return this.layout.saveLayout(); }

  loadLayout() { return this.layout.loadLayout(); }

  // === NEW FEATURES ===

  // Context menu for agents

  // Toggle help overlay

  // Draw context menu for agent quick actions

  // Handle context menu action

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

  /**
   * Helper: Darken a hex color
   */

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
    }
  };

  /**
   * Create a new combiner task
   */
  async createCombinerNode(type, x, y) {
    const combinerType = AgentCanvas.COMBINER_TYPES[type.toUpperCase()];
    if (!combinerType) {
      console.error(`Unknown combiner type: ${type}`);
      return null;
    }

    // Create backend task for this combiner
    try {
      const response = await apiPost(`/api/studios/${this.studioId}/tasks`, {
        description: `${combinerType.name}: ${combinerType.description}`,
        from: '',
        to: '',
        priority: 0,
        combiner_type: combinerType.id,
        result_combination_mode: combinerType.resultCombinationMode,
        combination_instruction: combinerType.customInstruction || ''
      });

      if (response.task) {
        const task = response.task;
        console.log(`✅ Created combiner task ${task.id}`);

        // Add task to state with position
        const newTask = {
          ...task,
          x: x,
          y: y,
          combiner_type: task.combiner_type,
          combinerType: task.combiner_type
        };

        const tasks = [...this.state.tasks, newTask];
        this.state.setTasks(tasks);

        // Save layout to persist position
        await this.saveLayout();

        // Redraw canvas
        this.draw();

        return newTask;
      }
    } catch (error) {
      console.error('Failed to create combiner task:', error);
      return null;
    }
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

  /**
   * Create a connection between two nodes (agent/combiner to agent/combiner)
   */
  createConnection(fromNodeId, fromPort, toNodeId, toPort) {
    const fromNode = this.helpers.getNodeById(fromNodeId);
    const toNode = this.helpers.getNodeById(toNodeId);

    if (!fromNode || !toNode) {
      console.warn('Cannot create connection: missing node', { fromNodeId, toNodeId });
      return null;
    }

    // Enforce attachment direction: attachments can only connect outward to tasks/agents
    if (fromNode.type === 'attachment' && !(toNode.type === 'task' || toNode.type === 'agent')) {
      this.notifications?.showNotification?.('Attachments can only connect to tasks or agents', 'warning');
      return null;
    }
    if (toNode.type === 'attachment') {
      this.notifications?.showNotification?.('Attachments cannot receive connections', 'warning');
      return null;
    }

    // Enforce scheduler direction: schedulers can only connect to tasks
    if (fromNode.type === 'scheduler' && toNode.type !== 'task') {
      this.notifications?.showNotification?.('Scheduler nodes can only connect to tasks', 'warning');
      return null;
    }
    if (toNode.type === 'scheduler') {
      this.notifications?.showNotification?.('Scheduler nodes cannot receive connections', 'warning');
      return null;
    }

    // Avoid duplicate connections - check for any connection between same nodes
    // This prevents creating multiple connections from same source to same destination
    const existing = this.state.connections.find(conn =>
      conn.from === fromNodeId &&
      conn.to === toNodeId
    );
    if (existing) {
      console.log('Connection already exists between these nodes', { fromNodeId, toNodeId });
      this.notifications?.showNotification?.('A connection already exists between these nodes', 'info');
      return existing;
    }

    const conn = {
      id: `conn-${Date.now()}-${Math.random().toString(16).slice(2, 8)}`,
      from: fromNodeId,
      fromPort,
      to: toNodeId,
      toPort,
      color: '#cbd5e1',
      animated: false
    };

    this.state.connections.push(conn);
    this.saveLayout();

    // If this is a scheduler→task connection, update the scheduler's target_task_id
    if (fromNode.type === 'scheduler' && toNode.type === 'task') {
      const schedulerNodeId = fromNode.node.canvas_node_id || fromNode.node.id;
      this.updateSchedulerTargetTask(schedulerNodeId, toNode.node.id).catch(err => {
        console.error('Failed to update scheduler target task', err);
        this.notifications?.showNotification?.('Failed to link scheduler to task', 'error');
      });
    }

    this.draw();
    return conn;
  }

  /**
   * Get node by ID (searches both agents and combiners)
   */

  /**
   * Get port position in screen coordinates
   */

  /**
   * Delete a combiner node and its connections
   */

  /**
   * Delete a connection
   */

  /**
   * Remove unused input ports from a combiner node
   */

  /**
   * Get connection near a given position (for click detection)
   */

  /**
   * Get port at a given canvas position (for click detection)
   */

  /**
   * Instantiate a workflow from the workflow panel
   * Creates all nodes from the workflow template on the canvas
   */
  async instantiateWorkflow(workflow) {
    if (!workflow || !workflow.nodes || workflow.nodes.length === 0) {
      this.notifications?.showNotification?.('Workflow has no nodes', 'warning');
      return;
    }

    console.log('📋 Instantiating workflow:', workflow.name);

    // Calculate viewport center for positioning
    const canvasRect = this.canvas.getBoundingClientRect();
    const viewportCenterX = (canvasRect.width / 2 - this.offsetX) / this.scale;
    const viewportCenterY = (canvasRect.height / 2 - this.offsetY) / this.scale;

    const createdNodes = [];
    const nodeIdMap = {}; // Map old node IDs to new node IDs

    // Create nodes
    for (const node of workflow.nodes) {
      try {
        const x = viewportCenterX + (node.relative_x || 0);
        const y = viewportCenterY + (node.relative_y || 0);

        let createdNode = null;

        switch (node.type) {
          case 'task':
            createdNode = await this.createWorkflowTaskNode(node, x, y);
            break;
          case 'agent':
            createdNode = await this.createWorkflowAgentNode(node, x, y);
            break;
          case 'scheduler':
            createdNode = await this.createWorkflowSchedulerNode(node, x, y);
            break;
          case 'store':
            createdNode = await this.createWorkflowStoreNode(node, x, y);
            break;
          case 'attachment':
            createdNode = await this.createWorkflowAttachmentNode(node, x, y);
            break;
          default:
            console.warn(`Unknown node type: ${node.type}`);
        }

        if (createdNode) {
          nodeIdMap[node.id] = createdNode.id;
          createdNodes.push({ original: node, created: createdNode });
        }
      } catch (error) {
        console.error(`Error creating ${node.type} node:`, error);
      }
    }

    // Create connections between newly created nodes
    if (workflow.internal_connections && workflow.internal_connections.length > 0) {
      for (const conn of workflow.internal_connections) {
        const newFromId = nodeIdMap[conn.from_node];
        const newToId = nodeIdMap[conn.to_node];

        if (newFromId && newToId) {
          this.createConnection(newFromId, conn.from_port || 'out', newToId, conn.to_port || 'in');
        }
      }
    }

    // Reload workspace to ensure state consistency
    await this.initModule.init();

    // Highlight newly created nodes temporarily
    this.highlightNewNodes(createdNodes.map(n => n.created.id));

    this.notifications?.showNotification?.(
      `Added ${createdNodes.length} node${createdNodes.length !== 1 ? 's' : ''} from "${workflow.name}"`,
      'success'
    );

    return createdNodes;
  }

  /**
   * Create a task node from workflow template
   */
  async createWorkflowTaskNode(node, x, y) {
    const config = node.config || {};
    const response = await apiPost(`/api/studios/${this.studioId}/tasks`, {
      description: config.description || 'New Task',
      to: config.to || 'unassigned',
      from: config.from || 'user',
      priority: config.priority || 0
    });

    if (response && response.task) {
      const task = response.task;
      task.x = x;
      task.y = y;

      // Add to state
      const tasks = [...this.state.tasks, task];
      this.state.setTasks(tasks);

      return task;
    }

    throw new Error('Failed to create task');
  }

  /**
   * Create an agent node from workflow template
   */
  async createWorkflowAgentNode(node, x, y) {
    const config = node.config || {};
    const agentName = config.name || node.id;

    const response = await apiPost('/api/orchestration/workspace/agents', {
      studio_id: this.studioId,
      agent_name: agentName
    });

    if (response) {
      // The response contains the agent info; we need to add position
      return {
        id: response.agent_id || agentName,
        name: agentName,
        x: x,
        y: y
      };
    }

    throw new Error('Failed to add agent');
  }

  /**
   * Create a scheduler node from workflow template
   */
  async createWorkflowSchedulerNode(node, x, y) {
    const config = node.config || {};

    const response = await apiPost(`/api/orchestration/workspaces/${this.studioId}/scheduler-nodes`, {
      name: config.name || 'New Scheduler',
      schedule_type: config.schedule_type || 'cron',
      cron_expression: config.cron_expression || '0 * * * *',
      enabled: config.enabled !== false,
      x: x,
      y: y
    });

    if (response && response.scheduler_node) {
      return response.scheduler_node;
    }

    throw new Error('Failed to create scheduler');
  }

  /**
   * Create a store node from workflow template
   */
  async createWorkflowStoreNode(node, x, y) {
    const config = node.config || {};

    const response = await apiPost(`/api/studios/${this.studioId}/canvas/store-nodes`, {
      name: config.name || 'New Store',
      base_dir: config.base_dir || config.file_path || '',
      format: config.format || 'json',
      write_mode: config.write_mode || 'overwrite',
      auto_create_dir: config.auto_create_dir !== false,
      x: x,
      y: y
    });

    if (response) {
      return response;
    }

    throw new Error('Failed to create store');
  }

  /**
   * Create an attachment node from workflow template
   */
  async createWorkflowAttachmentNode(node, x, y) {
    const config = node.config || {};

    const response = await apiPost(`/api/studios/${this.studioId}/attachments`, {
      title: config.title || 'New Attachment',
      type: config.type || 'note',
      body: config.body || '',
      link_url: config.link_url || ''
    });

    if (response && response.attachment) {
      const attachment = response.attachment;
      attachment.x = x;
      attachment.y = y;
      return attachment;
    }

    throw new Error('Failed to create attachment');
  }

  /**
   * Temporarily highlight newly created nodes
   */
  highlightNewNodes(nodeIds) {
    if (!nodeIds || nodeIds.length === 0) return;

    // Store highlight state
    this.state.highlightedNewNodes = new Set(nodeIds);

    // Clear highlight after 3 seconds
    setTimeout(() => {
      this.state.highlightedNewNodes = null;
      this.draw();
    }, 3000);

    this.draw();
  }

  /**
   * Export canvas as PNG image
   */
  exportCanvas() {
    // Create a temporary canvas with white background
    const tempCanvas = document.createElement('canvas');
    tempCanvas.width = this.canvas.width;
    tempCanvas.height = this.canvas.height;
    const tempCtx = tempCanvas.getContext('2d');

    // Fill with white background
    tempCtx.fillStyle = '#ffffff';
    tempCtx.fillRect(0, 0, tempCanvas.width, tempCanvas.height);

    // Draw the current canvas on top
    tempCtx.drawImage(this.canvas, 0, 0);

    // Create download link
    tempCanvas.toBlob((blob) => {
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      const timestamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, -5);
      link.download = `agent-canvas-${timestamp}.png`;
      link.href = url;
      link.click();
      URL.revokeObjectURL(url);
    });
  }
}

// Make it globally accessible
window.AgentCanvas = AgentCanvas;

// Export canvas function - accessible globally for button onclick
window.exportCanvas = function() {
  if (window.currentCanvas) {
    window.currentCanvas.exportCanvas();
  } else {
    console.warn('No canvas instance available to export');
  }
};
