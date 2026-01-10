/**
 * Agent Canvas State Management
 *
 * Centralized state container for the AgentCanvas system.
 * All state properties are managed through this class with:
 * - Getters for read access
 * - Setters for controlled mutations
 * - Event bus for inter-module communication
 *
 * @module agent-canvas-state
 */

/**
 * Event bus for inter-module communication
 */
class EventBus {
  constructor() {
    this.listeners = new Map();
  }

  /**
   * Subscribe to an event
   * @param {string} event - Event name
   * @param {Function} callback - Event handler
   */
  on(event, callback) {
    if (!this.listeners.has(event)) {
      this.listeners.set(event, []);
    }
    this.listeners.get(event).push(callback);
  }

  /**
   * Unsubscribe from an event
   * @param {string} event - Event name
   * @param {Function} callback - Event handler to remove
   */
  off(event, callback) {
    if (!this.listeners.has(event)) return;
    const callbacks = this.listeners.get(event);
    const index = callbacks.indexOf(callback);
    if (index > -1) {
      callbacks.splice(index, 1);
    }
  }

  /**
   * Emit an event
   * @param {string} event - Event name
   * @param {*} data - Event data
   */
  emit(event, data) {
    if (!this.listeners.has(event)) return;
    const callbacks = this.listeners.get(event);
    callbacks.forEach(callback => {
      try {
        callback(data);
      } catch (error) {
        console.error(`Error in event handler for "${event}":`, error);
      }
    });
  }

  /**
   * Clear all listeners
   */
  clear() {
    this.listeners.clear();
  }
}

/**
 * Event type constants
 */
export const EVENT_TYPES = {
  // Agent events
  AGENT_MOVED: 'agent.moved',
  AGENT_SELECTED: 'agent.selected',
  AGENT_STATUS_CHANGED: 'agent.status.changed',
  AGENT_STATS_UPDATED: 'agent.stats.updated',

  // Task events
  TASK_CREATED: 'task.created',
  TASK_UPDATED: 'task.updated',
  TASK_MOVED: 'task.moved',
  TASK_STATUS_CHANGED: 'task.status.changed',
  TASK_SELECTED: 'task.selected',

  // Attachment events
  ATTACHMENT_CREATED: 'attachment.created',
  ATTACHMENT_UPDATED: 'attachment.updated',
  ATTACHMENT_MOVED: 'attachment.moved',
  ATTACHMENT_DELETED: 'attachment.deleted',

  // Store Node events
  STORE_NODE_CREATED: 'store_node.created',
  STORE_NODE_UPDATED: 'store_node.updated',
  STORE_NODE_MOVED: 'store_node.moved',
  STORE_NODE_DELETED: 'store_node.deleted',

  // Canvas events
  CANVAS_PANNED: 'canvas.panned',
  CANVAS_ZOOMED: 'canvas.zoomed',
  CANVAS_RESIZED: 'canvas.resized',

  // Panel events
  PANEL_OPENED: 'panel.opened',
  PANEL_CLOSED: 'panel.closed',

  // Timeline events
  TIMELINE_EVENT_ADDED: 'timeline.event.added',
  TIMELINE_TOGGLED: 'timeline.toggled',

  // Combiner events
  COMBINER_CREATED: 'combiner.created',
  COMBINER_DELETED: 'combiner.deleted',
  COMBINER_MOVED: 'combiner.moved',
  COMBINER_CONNECTED: 'combiner.connected',

  // Mode changes
  MODE_CHANGED: 'mode.changed',

  // Data updates
  DATA_LOADED: 'data.loaded',
  STATE_RESET: 'state.reset',

  // Multi-selection events
  SELECTION_CHANGED: 'selection.changed',
  SELECTION_CLEARED: 'selection.cleared',
};

/**
 * AgentCanvasState - Centralized state management for AgentCanvas
 */
export class AgentCanvasState {
  constructor() {
    this.eventBus = new EventBus();
    this.initialize();
  }

  /**
   * Initialize all state properties
   */
  initialize() {
    // Core Canvas State
    this.canvas = null;
    this.ctx = null;
    this.studioId = null;
    this.studio = null;
    this.agents = [];
    this.tasks = [];
    this.attachments = [];
    this.storeNodes = [];  // Store nodes (file storage nodes)

    // Data & Communication
    this.messages = [];
    this.mission = null;
    this.eventSource = null;

    // Transform State
    this.offsetX = 0;
    this.offsetY = 0;
    this.scale = 1;

    // Drag State - Canvas Pan
    this.isDragging = false;
    this.dragStartX = 0;
    this.dragStartY = 0;
    this.spacePressed = false;

    // Drag State - Agent
    this.isDraggingAgent = false;
    this.draggedAgent = null;

    // Drag State - Task
    this.isDraggingTask = false;
    this.draggedTask = null;

    // Drag State - Attachment
    this.isDraggingAttachment = false;
    this.draggedAttachment = null;

    // Drag State - Connection
    this.isDraggingConnection = false;
    this.draggedConnection = null;
    this.connectionDragStart = null;

    // Drag State - Combiner
    this.isDraggingCombiner = false;
    this.draggedCombiner = null;
    this.selectedCombiner = null;
    this.hoveredCombiner = null;

    // Context Menu State
    this.contextMenuVisible = false;
    this.contextMenuAgent = null;
    this.contextMenuX = 0;
    this.contextMenuY = 0;

    // Help Overlay State
    this.helpOverlayVisible = false;

    // Animation State
    this.animationFrame = null;
    this.animationPaused = false;
    this.particles = [];

    // Visual Appearance
    const isDark = document.documentElement.getAttribute('data-bs-theme') === 'dark';
    this.backgroundColor = isDark ? '#1e293b' : '#f1f5f9';

    // Expanded Task Panel State
    this.expandedTask = null;
    this.expandedPanelWidth = 0;
    this.expandedPanelTargetWidth = 400;
    this.expandedPanelAnimating = false;
    this.resultScrollOffset = 0;
    this.resultBoxBounds = null;
    this.copyButtonBounds = null;
    this.copyButtonState = 'idle'; // 'idle', 'hover', 'copied'

    // Expanded Agent Panel State
    this.expandedAgent = null;
    this.expandedAgentPanelWidth = 0;
    this.expandedAgentPanelTargetWidth = 400;
    this.expandedAgentPanelAnimating = false;
    this.agentPanelScrollOffset = 0;
    this.agentPanelMaxScroll = 0;

    // Expanded Combiner Panel State
    this.expandedCombiner = null;
    this.expandedCombinerPanelWidth = 0;
    this.expandedCombinerPanelTargetWidth = 360;
    this.expandedCombinerPanelAnimating = false;

    // Connection Mode State (task-to-task)
    this.connectionMode = false;
    this.connectionSourceTask = null;
    this.highlightedAgent = null;

    // Task-to-Agent Assignment Mode State
    this.assignmentMode = false;
    this.assignmentSourceTask = null;
    this.assignmentMouseX = 0;
    this.assignmentMouseY = 0;

    // Combiner Output Assignment Mode
    this.combinerAssignMode = false;
    this.combinerAssignmentSource = null;

    // Create Task Mode State
    this.createTaskMode = false;

    // Forms Module (will be set by main canvas)
    this.forms = null;

    // Timeline Panel State
    this.timelineVisible = false;
    this.timelinePanelWidth = 0;
    this.timelinePanelTargetWidth = 350;
    this.timelinePanelAnimating = false;
    this.timelineEvents = [];
    this.timelineScrollOffset = 0;
    this.timelineMaxEvents = 50;

    // Attachment connect mode
    this.pendingAttachmentSource = null;

    // Chain Visualization State
    this.activeChains = [];
    this.chainParticles = [];

    // Combiner Nodes State
    this.combinerNodes = [];
    this.connections = [];

    // Execution Logs State
    this.executionLogs = {}; // { taskId: [{ type, message, timestamp }] }

    // Keyboard State
    this.ctrlPressed = false;
    this.shiftPressed = false;

    // Multi-Selection State
    this.selectedNodes = new Map();  // Map<id, {id: string, type: string, node: object}>
    this.firstSelectedId = null;     // Track the first selected node
    this.multiSelectContextMenu = false;
    this.multiSelectMenuX = 0;
    this.multiSelectMenuY = 0;
    this.multiSelectMenuItems = [];  // For click detection

    // Marquee Selection State (Shift+Drag)
    this.isMarqueeSelecting = false;
    this.marqueeStartX = 0;          // World coordinates
    this.marqueeStartY = 0;
    this.marqueeEndX = 0;
    this.marqueeEndY = 0;

    // Callbacks (set by parent)
    this.onAgentClick = null;
    this.onTimelineEvent = null;
  }

  /**
   * Reset state to initial values
   */
  reset() {
    this.initialize();
    this.eventBus.emit(EVENT_TYPES.STATE_RESET);
  }

  // ==================== CANVAS SETUP ====================

  /**
   * Set canvas and context
   */
  setCanvas(canvas, ctx) {
    this.canvas = canvas;
    this.ctx = ctx;
  }

  /**
   * Set studio ID
   */
  setStudioId(studioId) {
    this.studioId = studioId;
  }

  /**
   * Set studio data
   */
  setStudio(studio) {
    this.studio = studio;
    this.eventBus.emit(EVENT_TYPES.DATA_LOADED, { studio });
  }

  // ==================== AGENTS ====================

  /**
   * Set agents array
   */
  setAgents(agents) {
    this.agents = agents;
  }

  /**
   * Add agent
   */
  addAgent(agent) {
    this.agents.push(agent);
  }

  /**
   * Get agent by name
   */
  getAgent(name) {
    return this.agents.find(a => a.name === name);
  }

  /**
   * Update agent position
   */
  updateAgentPosition(agent, x, y) {
    agent.x = x;
    agent.y = y;
    this.eventBus.emit(EVENT_TYPES.AGENT_MOVED, { agent, x, y });
  }

  /**
   * Set agent status
   */
  setAgentStatus(agentName, status) {
    const agent = this.getAgent(agentName);
    if (agent) {
      agent.status = status;
      this.eventBus.emit(EVENT_TYPES.AGENT_STATUS_CHANGED, { agent, status });
    }
  }

  // ==================== TASKS ====================

  /**
   * Set tasks array
   */
  setTasks(tasks) {
    this.tasks = tasks;
  }

  /**
   * Add task
   */
  addTask(task) {
    this.tasks.push(task);
    this.eventBus.emit(EVENT_TYPES.TASK_CREATED, { task });
  }

  /**
   * Get task by ID
   */
  getTask(taskId) {
    return this.tasks.find(t => t.id === taskId);
  }

  /**
   * Update task position
   */
  updateTaskPosition(task, x, y) {
    task.x = x;
    task.y = y;
    this.eventBus.emit(EVENT_TYPES.TASK_MOVED, { task, x, y });
  }

  /**
   * Update task status
   */
  updateTaskStatus(taskId, status) {
    const task = this.getTask(taskId);
    if (task) {
      task.status = status;
      this.eventBus.emit(EVENT_TYPES.TASK_STATUS_CHANGED, { task, status });
    }
  }

  // ==================== ATTACHMENTS ====================

  /**
   * Set attachments array
   */
  setAttachments(attachments) {
    this.attachments = attachments || [];
  }

  /**
   * Add attachment
   */
  addAttachment(attachment) {
    this.attachments.push(attachment);
    this.eventBus.emit(EVENT_TYPES.ATTACHMENT_CREATED, { attachment });
  }

  /**
   * Get attachment by ID
   */
  getAttachment(attachmentId) {
    return this.attachments.find(a => a.id === attachmentId);
  }

  /**
   * Update attachment position
   */
  updateAttachmentPosition(attachment, x, y) {
    attachment.x = x;
    attachment.y = y;
    this.eventBus.emit(EVENT_TYPES.ATTACHMENT_MOVED, { attachment, x, y });
  }

  // ========================================================================
  // Store Node Management
  // ========================================================================

  /**
   * Set store nodes
   */
  setStoreNodes(storeNodes) {
    this.storeNodes = storeNodes || [];
  }

  /**
   * Remove store node by ID
   */
  removeStoreNode(storeNodeId) {
    const index = this.storeNodes.findIndex(s => s.id === storeNodeId);
    if (index !== -1) {
      const storeNode = this.storeNodes[index];
      this.storeNodes.splice(index, 1);
      this.eventBus.emit(EVENT_TYPES.STORE_NODE_DELETED, { storeNode });
    }
  }

  /**
   * Remove attachment by ID
   */
  removeAttachment(attachmentId) {
    const idx = this.attachments.findIndex(a => a.id === attachmentId);
    if (idx !== -1) {
      this.attachments.splice(idx, 1);
      this.eventBus.emit(EVENT_TYPES.ATTACHMENT_DELETED, { attachmentId });
    }
  }

  // ==================== TRANSFORM ====================

  /**
   * Set canvas offset (pan)
   */
  setOffset(x, y) {
    this.offsetX = x;
    this.offsetY = y;
    this.eventBus.emit(EVENT_TYPES.CANVAS_PANNED, { offsetX: x, offsetY: y });
  }

  /**
   * Set canvas scale (zoom)
   */
  setScale(scale) {
    this.scale = Math.max(0.1, Math.min(5, scale)); // Clamp between 0.1 and 5
    this.eventBus.emit(EVENT_TYPES.CANVAS_ZOOMED, { scale: this.scale });
  }

  // ==================== DRAG STATES ====================

  /**
   * Start canvas dragging
   */
  startCanvasDrag(startX, startY) {
    this.isDragging = true;
    this.dragStartX = startX;
    this.dragStartY = startY;
  }

  /**
   * End canvas dragging
   */
  endCanvasDrag() {
    this.isDragging = false;
  }

  /**
   * Start agent dragging
   */
  startAgentDrag(agent) {
    this.isDraggingAgent = true;
    this.draggedAgent = agent;
  }

  /**
   * End agent dragging
   */
  endAgentDrag() {
    this.isDraggingAgent = false;
    this.draggedAgent = null;
  }

  /**
   * Start task dragging
   */
  startTaskDrag(task) {
    this.isDraggingTask = true;
    this.draggedTask = task;
  }

  /**
   * End task dragging
   */
  endTaskDrag() {
    this.isDraggingTask = false;
    this.draggedTask = null;
  }

  /**
   * Start connection dragging
   */
  startConnectionDrag(connection, startPort) {
    this.isDraggingConnection = true;
    this.draggedConnection = connection;
    this.connectionDragStart = startPort;
  }

  /**
   * End connection dragging
   */
  endConnectionDrag() {
    this.isDraggingConnection = false;
    this.draggedConnection = null;
    this.connectionDragStart = null;
  }

  /**
   * Start combiner dragging
   */
  startCombinerDrag(combiner) {
    this.isDraggingCombiner = true;
    this.draggedCombiner = combiner;
  }

  /**
   * End combiner dragging
   */
  endCombinerDrag() {
    this.isDraggingCombiner = false;
    this.draggedCombiner = null;
  }

  // ==================== MODES ====================

  /**
   * Set assignment mode
   */
  setAssignmentMode(enabled, sourceTask = null) {
    this.assignmentMode = enabled;
    this.assignmentSourceTask = sourceTask;
    this.eventBus.emit(EVENT_TYPES.MODE_CHANGED, { mode: 'assignment', enabled, sourceTask });
  }

  /**
   * Set combiner assignment mode
   */
  setCombinerAssignMode(enabled, source = null) {
    this.combinerAssignMode = enabled;
    this.combinerAssignmentSource = source;
    this.eventBus.emit(EVENT_TYPES.MODE_CHANGED, { mode: 'combinerAssign', enabled, source });
  }

  /**
   * Set connection mode
   */
  setConnectionMode(enabled, sourceTask = null) {
    this.connectionMode = enabled;
    this.connectionSourceTask = sourceTask;
    this.eventBus.emit(EVENT_TYPES.MODE_CHANGED, { mode: 'connection', enabled, sourceTask });
  }

  /**
   * Set create task mode
   */
  setCreateTaskMode(enabled) {
    this.createTaskMode = enabled;
    this.eventBus.emit(EVENT_TYPES.MODE_CHANGED, { mode: 'createTask', enabled });
  }

  // ==================== PANELS ====================

  /**
   * Set expanded task panel
   */
  setExpandedTask(task) {
    this.expandedTask = task;
    if (task) {
      this.eventBus.emit(EVENT_TYPES.PANEL_OPENED, { type: 'task', task });
    } else {
      this.eventBus.emit(EVENT_TYPES.PANEL_CLOSED, { type: 'task' });
    }
  }

  /**
   * Set expanded agent panel
   */
  setExpandedAgent(agent) {
    this.expandedAgent = agent;
    if (agent) {
      this.eventBus.emit(EVENT_TYPES.PANEL_OPENED, { type: 'agent', agent });
    } else {
      this.eventBus.emit(EVENT_TYPES.PANEL_CLOSED, { type: 'agent' });
    }
  }

  /**
   * Set expanded combiner panel
   */
  setExpandedCombiner(combiner) {
    this.expandedCombiner = combiner;
    if (combiner) {
      this.eventBus.emit(EVENT_TYPES.PANEL_OPENED, { type: 'combiner', combiner });
    } else {
      this.eventBus.emit(EVENT_TYPES.PANEL_CLOSED, { type: 'combiner' });
    }
  }

  /**
   * Set timeline visibility
   */
  setTimelineVisible(visible) {
    this.timelineVisible = visible;
    this.eventBus.emit(EVENT_TYPES.TIMELINE_TOGGLED, { visible });
  }

  // ==================== TIMELINE ====================

  /**
   * Add timeline event
   */
  addTimelineEvent(event) {
    this.timelineEvents.unshift(event);
    if (this.timelineEvents.length > this.timelineMaxEvents) {
      this.timelineEvents = this.timelineEvents.slice(0, this.timelineMaxEvents);
    }
    this.eventBus.emit(EVENT_TYPES.TIMELINE_EVENT_ADDED, { event });
  }

  // ==================== EXECUTION LOGS ====================

  /**
   * Add execution log
   */
  addExecutionLog(taskId, type, message) {
    if (!this.executionLogs[taskId]) {
      this.executionLogs[taskId] = [];
    }
    this.executionLogs[taskId].push({
      type,
      message,
      timestamp: new Date()
    });

    // Limit to 50 entries per task
    if (this.executionLogs[taskId].length > 50) {
      this.executionLogs[taskId] = this.executionLogs[taskId].slice(-50);
    }
  }

  /**
   * Get execution logs for task
   */
  getExecutionLogs(taskId) {
    return this.executionLogs[taskId] || [];
  }

  // ==================== COMBINER NODES ====================

  /**
   * Add combiner node
   */
  addCombinerNode(combiner) {
    this.combinerNodes.push(combiner);
    this.eventBus.emit(EVENT_TYPES.COMBINER_CREATED, { combiner });
  }

  /**
   * Remove combiner node
   */
  removeCombinerNode(combiner) {
    const index = this.combinerNodes.indexOf(combiner);
    if (index > -1) {
      this.combinerNodes.splice(index, 1);
      this.eventBus.emit(EVENT_TYPES.COMBINER_DELETED, { combiner });
    }
  }

  /**
   * Add connection
   */
  addConnection(connection) {
    this.connections.push(connection);
    this.eventBus.emit(EVENT_TYPES.COMBINER_CONNECTED, { connection });
  }

  /**
   * Remove connection
   */
  removeConnection(connection) {
    const index = this.connections.indexOf(connection);
    if (index > -1) {
      this.connections.splice(index, 1);
    }
  }

  // ==================== EVENT BUS ====================

  /**
   * Subscribe to event
   */
  on(event, callback) {
    this.eventBus.on(event, callback);
  }

  /**
   * Unsubscribe from event
   */
  off(event, callback) {
    this.eventBus.off(event, callback);
  }

  /**
   * Emit event
   */
  emit(event, data) {
    this.eventBus.emit(event, data);
  }

  // ==================== MULTI-SELECTION ====================

  /**
   * Select a node (add to selection)
   * @param {string} id - Node ID
   * @param {string} type - Node type ('agent'|'task'|'store'|'attachment'|'combiner')
   * @param {object} node - Node object reference
   */
  selectNode(id, type, node) {
    if (!this.selectedNodes.has(id)) {
      // Track first selected node
      if (this.selectedNodes.size === 0) {
        this.firstSelectedId = id;
      }
      this.selectedNodes.set(id, { id, type, node });
      this.eventBus.emit(EVENT_TYPES.SELECTION_CHANGED, {
        action: 'add',
        node: { id, type, node },
        count: this.selectedNodes.size
      });
    }
  }

  /**
   * Deselect a node (remove from selection)
   * @param {string} id - Node ID
   */
  deselectNode(id) {
    if (this.selectedNodes.has(id)) {
      const removed = this.selectedNodes.get(id);
      this.selectedNodes.delete(id);

      // If we removed the first selected node, update to the next one
      if (this.firstSelectedId === id) {
        const remaining = this.selectedNodes.keys().next();
        this.firstSelectedId = remaining.done ? null : remaining.value;
      }

      this.eventBus.emit(EVENT_TYPES.SELECTION_CHANGED, {
        action: 'remove',
        node: removed,
        count: this.selectedNodes.size
      });
    }
  }

  /**
   * Toggle node selection (for Shift+Click)
   * @param {string} id - Node ID
   * @param {string} type - Node type
   * @param {object} node - Node object reference
   */
  toggleNodeSelection(id, type, node) {
    if (this.selectedNodes.has(id)) {
      this.deselectNode(id);
    } else {
      this.selectNode(id, type, node);
    }
  }

  /**
   * Clear all selections
   */
  clearSelection() {
    if (this.selectedNodes.size > 0) {
      this.selectedNodes.clear();
      this.firstSelectedId = null;
      this.multiSelectContextMenu = false;
      this.eventBus.emit(EVENT_TYPES.SELECTION_CLEARED);
    }
  }

  /**
   * Check if a node is selected
   * @param {string} id - Node ID
   * @returns {boolean}
   */
  isNodeSelected(id) {
    return this.selectedNodes.has(id);
  }

  /**
   * Check if a node is the first (primary) selected node
   * @param {string} id - Node ID
   * @returns {boolean}
   */
  isFirstSelected(id) {
    return this.firstSelectedId === id;
  }

  /**
   * Get all selected nodes as array
   * @returns {Array<{type: string, node: object}>}
   */
  getSelectedNodes() {
    return Array.from(this.selectedNodes.values());
  }

  /**
   * Get selected nodes by type
   * @param {string} [type] - Optional node type to filter by. If omitted, returns all nodes grouped by type.
   * @returns {Array<{id: string, type: string, node: object}>|Object} Array if type specified, object with type keys otherwise
   */
  getSelectedNodesByType(type) {
    const nodes = this.getSelectedNodes();

    if (type) {
      // Filter by specific type
      return nodes.filter(n => n.type === type);
    }

    // Group all nodes by type
    const grouped = {};
    for (const { id, type: nodeType, node } of nodes) {
      if (!grouped[nodeType]) {
        grouped[nodeType] = [];
      }
      grouped[nodeType].push({ id, node });
    }
    return grouped;
  }

  /**
   * Check if multiple nodes are selected
   * @returns {boolean}
   */
  hasMultipleSelected() {
    return this.selectedNodes.size > 1;
  }

  /**
   * Get selection count
   * @returns {number}
   */
  getSelectionCount() {
    return this.selectedNodes.size;
  }

  /**
   * Show multi-select context menu
   * @param {number} x - Screen X position
   * @param {number} y - Screen Y position
   */
  showMultiSelectContextMenu(x, y) {
    this.multiSelectContextMenu = true;
    this.multiSelectMenuX = x;
    this.multiSelectMenuY = y;
  }

  /**
   * Hide multi-select context menu
   */
  hideMultiSelectContextMenu() {
    this.multiSelectContextMenu = false;
    this.multiSelectMenuItems = [];
  }

  // ==================== CLEANUP ====================

  /**
   * Cleanup state and event listeners
   */
  cleanup() {
    this.eventBus.clear();
    this.reset();
  }
}
