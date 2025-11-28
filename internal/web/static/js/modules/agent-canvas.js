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
import { AgentCanvasHelpers } from './agent-canvas-helpers.js';
import { AgentCanvasCombinerOperations } from './agent-canvas-combiner-ops.js';
import { AgentCanvasPanelManager } from './agent-canvas-panels.js';
import { AgentCanvasNotifications } from './agent-canvas-notifications.js';
import { AgentCanvasMetrics } from './agent-canvas-metrics.js';
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

    // Initialize combiner operations module
    this.combinerOps = new AgentCanvasCombinerOperations(this.state, this);

    // Initialize panel manager
    this.panels = new AgentCanvasPanelManager(this.state, this);

    // Initialize notifications module
    this.notifications = new AgentCanvasNotifications(this.state, this);

    // Initialize metrics module
    this.metrics = new AgentCanvasMetrics(this.state, this);

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

  // Combiner operations delegated to combiner ops module
  ensureCombinerInputPort(combiner, portId) { return this.combinerOps.ensureCombinerInputPort(combiner, portId); }
  createConnection(fromNodeId, fromPort, toNodeId, toPort) { return this.combinerOps.createConnection(fromNodeId, fromPort, toNodeId, toPort); }
  deleteCombinerNode(nodeId) { return this.combinerOps.deleteCombinerNode(nodeId); }
  deleteConnection(connectionId) { return this.combinerOps.deleteConnection(connectionId); }
  cleanupCombinerInputPorts(combiner, silent = false) { return this.combinerOps.cleanupCombinerInputPorts(combiner, silent); }
  buildCombinerResultPreview(combiner) { return this.combinerOps.buildCombinerResultPreview(combiner); }

  // Metrics methods delegated to metrics module
  updateMetrics() { return this.metrics.updateMetrics(); }
  updateAgentStats(agentStats) { return this.metrics.updateAgentStats(agentStats); }

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
  toggleCombinerPanel(combiner) { return this.panels.toggleCombinerPanel(combiner); }
  closeCombinerPanel() { return this.panels.closeCombinerPanel(); }
  animateCombinerPanel(expanding) { return this.panels.animateCombinerPanel(expanding); }
  toggleHelpOverlay() { return this.panels.toggleHelpOverlay(); }

  // Context menu methods delegated to context menu module
  toggleAssignmentMode(task) { return this.contextMenu.toggleAssignmentMode(task); }
  handleContextMenuAction(action, agent) { return this.contextMenu.handleContextMenuAction(action, agent); }

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
  }

  async saveLayout() { return this.layout.saveLayout(); }

  loadLayout() { return this.layout.loadLayout(); }

  // === NEW FEATURES ===

  zoomToFit() { return this.layout.zoomToFit(); }

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

  /**
   * Create a connection between two nodes (agent/combiner to agent/combiner)
   */

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
