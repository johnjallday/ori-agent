/**
 * Agent Canvas Interaction Handler
 *
 * This module handles all mouse and keyboard interactions for the Agent Canvas.
 * It processes user input events (clicks, drags, keyboard shortcuts) and delegates
 * actions to the parent AgentCanvas instance.
 *
 * @module agent-canvas-interactions
 */

/**
 * Handles all user interactions for the Agent Canvas including mouse events,
 * keyboard shortcuts, and touch gestures.
 */
export class AgentCanvasInteractionHandler {
  /**
   * Create an interaction handler
   * @param {HTMLCanvasElement} canvas - The canvas element
   * @param {Object} state - The canvas state object containing agents, tasks, forms, etc.
   * @param {Object} parent - The parent AgentCanvas instance
   */
  constructor(canvas, state, parent) {
    this.canvas = canvas;
    this.state = state;
    this.parent = parent;
  }

  /**
   * Handle keyboard key down events
   * Manages keyboard shortcuts and text input for forms
   *
   * @param {KeyboardEvent} e - The keyboard event
   */
  onKeyDown(e) {
    // Track modifier keys
    if (e.key === ' ') {
      this.state.spacePressed = true;
      if (!this.state.isDragging) {
        this.canvas.style.cursor = 'grab';
      }
    }
    if (e.ctrlKey || e.metaKey) {
      this.state.ctrlPressed = true;
    }

    // H key - Toggle help overlay
    if (e.key === 'h' || e.key === 'H') {
      if (!this.state.forms.createTaskDescriptionFocused) {
        e.preventDefault();
        this.parent.toggleHelpOverlay();
        return;
      }
    }

    // Handle text input when description field is focused
    if (this.state.forms.createTaskDescriptionFocused) {
      if (e.key === 'Escape' || e.key === 'Esc') {
        // ESC closes the entire form when description is focused
        e.preventDefault();
        this.state.forms.hideCreateTaskForm();
        return;
      } else if (e.key === 'Enter') {
        // Finish typing, unfocus field
        this.state.forms.createTaskDescriptionFocused = false;
        this.canvas.style.cursor = 'default';
        this.parent.draw();
        return;
      } else if (e.key === 'Backspace') {
        // Remove last character
        e.preventDefault();
        if (!this.state.forms.createTaskDescription) {
          this.state.forms.createTaskDescription = '';
        }
        this.state.forms.createTaskDescription = this.state.forms.createTaskDescription.slice(0, -1);
        this.parent.draw();
        return;
      } else if (e.key.length === 1) {
        // Add character to description
        e.preventDefault();
        if (!this.state.forms.createTaskDescription) {
          this.state.forms.createTaskDescription = '';
        }
        this.state.forms.createTaskDescription += e.key;
        this.parent.draw();
        return;
      }
      return; // Consume all other keys when focused
    }

    // ESC key - close forms or cancel connection/assignment modes
    if (e.key === 'Escape' || e.key === 'Esc') {
      if (this.state.helpOverlayVisible) {
        // Close help overlay
        e.preventDefault();
        this.state.helpOverlayVisible = false;
        this.parent.draw();
        return;
      } else if (this.state.contextMenuVisible) {
        // Close context menu
        e.preventDefault();
        this.state.contextMenuVisible = false;
        this.state.contextMenuAgent = null;
        this.state.contextMenuItems = [];
        this.parent.draw();
        return;
      } else if (this.state.forms.addAgentFormVisible) {
        // Close the add agent form
        e.preventDefault();
        this.state.forms.hideAddAgentForm();
      } else if (this.state.forms.createTaskFormVisible) {
        // Close the create task form
        e.preventDefault();
        this.state.forms.hideCreateTaskForm();
      } else if (this.state.assignmentMode) {
        this.state.assignmentMode = false;
        this.state.assignmentSourceTask = null;
        this.state.assignmentMouseX = 0;
        this.state.assignmentMouseY = 0;
        this.canvas.style.cursor = 'grab';
        this.parent.draw();
        console.log('Assignment mode cancelled');
      } else if (this.state.combinerAssignMode) {
        this.state.combinerAssignMode = false;
        this.state.combinerAssignmentSource = null;
        this.canvas.style.cursor = 'grab';
        this.parent.draw();
        console.log('Combiner assignment cancelled');
      }
    }

    // ESC also cancels connection dragging (e.g., from combiner/agent ports)
    if ((e.key === 'Escape' || e.key === 'Esc') && this.state.isDraggingConnection) {
      e.preventDefault();
      this.state.isDraggingConnection = false;
      this.state.connectionDragStart = null;
      this.canvas.style.cursor = 'grab';
      this.parent.draw();
      return;
    }
  }

  /**
   * Handle mouse down events
   * Initiates dragging operations for agents, tasks, combiners, and connections
   *
   * @param {MouseEvent} e - The mouse event
   */
  onMouseDown(e) {
    const rect = this.canvas.getBoundingClientRect();
    const screenX = e.clientX - rect.left;
    const screenY = e.clientY - rect.top;

    // Convert screen coordinates to canvas coordinates
    const x = (e.clientX - rect.left - this.state.offsetX) / this.state.scale;
    const y = (e.clientY - rect.top - this.state.offsetY) / this.state.scale;

    // Handle help overlay clicks (highest priority - modal overlay)
    if (this.state.helpOverlayVisible) {
      // Close help overlay on any click
      this.state.helpOverlayVisible = false;
      this.parent.draw();
      return;
    }

    // Handle context menu clicks (screen coordinates)
    if (this.state.contextMenuVisible && this.state.contextMenuItems) {
      for (const item of this.state.contextMenuItems) {
        if (screenX >= item.x && screenX <= item.x + item.width &&
            screenY >= item.y && screenY <= item.y + item.height) {
          // Handle menu item click
          this.parent.handleContextMenuAction(item.action, item.agent);
          this.state.contextMenuVisible = false;
          this.state.contextMenuAgent = null;
          this.state.contextMenuItems = [];
          this.parent.draw();
          return;
        }
      }
      // Clicked outside menu - close it
      this.state.contextMenuVisible = false;
      this.state.contextMenuAgent = null;
      this.state.contextMenuItems = [];
      this.parent.draw();
      return;
    }

    // If in assignment mode, prioritize assignment clicks over manual port wiring
    // Check ports if not in assignment mode, or treat combiner ports as clicks during assignment
    const clickedPort = this.parent.getPortAtPosition(x, y);
    if (clickedPort) {
      if (this.state.assignmentMode && this.state.assignmentSourceTask) {
        const target = this.parent.getNodeById(clickedPort.nodeId);
        if (target && target.type === 'combiner') {
          e.stopPropagation();
          e.preventDefault();
          console.log('Assigning task to combiner via port click:', target.node.id);
          this.parent.assignTaskToCombiner(target.node);
          return;
        }
        // Otherwise ignore port clicks while assigning
      } else {
        e.stopPropagation();
        e.preventDefault();
        this.state.isDraggingConnection = true;
        this.state.connectionDragStart = clickedPort;
        this.canvas.style.cursor = 'crosshair';
        console.log(`🔗 Started dragging connection from ${clickedPort.nodeId}.${clickedPort.portId}`);
        return;
      }
    }

    // Check if clicking on a combiner node
    for (const combiner of this.state.combinerNodes) {
      // Check delete button first (higher priority)
      if (combiner.deleteButtonBounds) {
        const bounds = combiner.deleteButtonBounds;
        if (x >= bounds.x && x <= bounds.x + bounds.width &&
            y >= bounds.y && y <= bounds.y + bounds.height) {
          // Delete this combiner
          e.stopPropagation();
          e.preventDefault();
          this.parent.deleteCombinerNode(combiner.id);
          this.parent.showNotification('Combiner node deleted', 'success');
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
          this.parent.executeCombiner(combiner);
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
          if (this.state.combinerAssignMode && this.state.combinerAssignmentSource && this.state.combinerAssignmentSource.id === combiner.id) {
            this.state.combinerAssignMode = false;
            this.state.combinerAssignmentSource = null;
            this.canvas.style.cursor = 'grab';
            this.parent.draw();
            this.parent.showNotification('Combiner assignment cancelled', 'info');
          } else {
            this.state.combinerAssignMode = true;
            this.state.combinerAssignmentSource = combiner;
            this.canvas.style.cursor = 'crosshair';
            this.parent.draw();
            this.parent.showNotification('Click an agent to route Merge output', 'info');
          }
          return;
        }
      }

      // Check if clicking on combiner body
      if (x >= combiner.x && x <= combiner.x + combiner.width &&
          y >= combiner.y && y <= combiner.y + combiner.height) {

        // Check if in assignment mode first (higher priority than dragging)
        if (this.state.assignmentMode && this.state.assignmentSourceTask) {
          e.stopPropagation();
          e.preventDefault();
          console.log('Assigning task to combiner in mousedown:', combiner.id);
          this.parent.assignTaskToCombiner(combiner);
          return;
        }

        // If not assigning, auto-connect from the last dragged connection start
        if (this.state.connectionDragStart) {
          e.stopPropagation();
          e.preventDefault();
          const portId = `input-${Math.max(combiner.inputPorts.length, 0)}`;
          this.parent.ensureCombinerInputPort(combiner, portId);
          this.parent.createConnection(
            this.state.connectionDragStart.nodeId,
            this.state.connectionDragStart.portId,
            combiner.id,
            portId
          );
          this.state.connectionDragStart = null;
          this.state.isDraggingConnection = false;
          this.canvas.style.cursor = 'grab';
          this.parent.draw();
          return;
        }

        // Otherwise, start dragging this combiner
        e.stopPropagation();
        e.preventDefault();
        this.state.isDraggingCombiner = true;
        this.state.draggedCombiner = combiner;
        this.state.dragStartX = x;
        this.state.dragStartY = y;
        this.canvas.style.cursor = 'move';
        return;
      }
    }

    // Check if Space is pressed for pan mode
    if (this.state.spacePressed) {
      // Space+Drag to pan
      this.state.isDragging = true;
      this.state.dragStartX = screenX - this.state.offsetX;
      this.state.dragStartY = screenY - this.state.offsetY;
      this.canvas.style.cursor = 'grabbing';
      return;
    }

    // Check if clicking on a task card first (tasks are drawn on top)
    if (this.state.tasks && this.state.tasks.length > 0) {
      for (let i = this.state.tasks.length - 1; i >= 0; i--) {  // Check in reverse order (top to bottom)
        const task = this.state.tasks[i];
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
            this.state.isDraggingTask = true;
            this.state.draggedTask = task;
            this.state.dragStartX = x;
            this.state.dragStartY = y;
            this.canvas.style.cursor = 'move';
            return;
          }
        }
      }
    }

    // Check if clicking on an agent (rectangle hitbox)
    for (const agent of this.state.agents) {
      const halfWidth = (agent.width || 120) / 2;
      const halfHeight = (agent.height || 70) / 2;

      // Check delete button first (higher priority than dragging)
      if (agent.deleteButton) {
        const deleteBtn = agent.deleteButton;
        if (x >= deleteBtn.x && x <= deleteBtn.x + deleteBtn.width &&
            y >= deleteBtn.y && y <= deleteBtn.y + deleteBtn.height) {
          // Remove agent from studio
          e.stopPropagation();
          e.preventDefault();
          this.parent.removeAgentFromStudio(agent.name);
          return;
        }
      }

      if (x >= agent.x - halfWidth && x <= agent.x + halfWidth &&
          y >= agent.y - halfHeight && y <= agent.y + halfHeight) {
        // Start dragging this agent
        this.state.isDraggingAgent = true;
        this.state.draggedAgent = agent;
        this.state.dragStartX = x;
        this.state.dragStartY = y;
        this.canvas.style.cursor = 'move';
        return;
      }
    }

    // Otherwise, start canvas panning
    this.state.isDragging = true;
    this.state.dragStartX = e.clientX - rect.left - this.state.offsetX;
    this.state.dragStartY = e.clientY - rect.top - this.state.offsetY;
    this.canvas.style.cursor = 'grabbing';
  }

  /**
   * Handle mouse move events
   * Updates cursor position, handles dragging of agents/tasks/combiners
   *
   * @param {MouseEvent} e - The mouse event
   */
  onMouseMove(e) {
    const rect = this.canvas.getBoundingClientRect();

    // Track mouse position for context menu hover effects
    this.state.lastMouseX = e.clientX - rect.left;
    this.state.lastMouseY = e.clientY - rect.top;

    // If context menu is visible, redraw to update hover effects
    if (this.state.contextMenuVisible) {
      this.parent.draw();
    }

    // Handle connection dragging
    if (this.state.isDraggingConnection) {
      this.parent.draw();
      return;
    }

    // Handle combiner node dragging
    if (this.state.isDraggingCombiner && this.state.draggedCombiner) {
      const x = (e.clientX - rect.left - this.state.offsetX) / this.state.scale;
      const y = (e.clientY - rect.top - this.state.offsetY) / this.state.scale;
      this.state.draggedCombiner.x = x;
      this.state.draggedCombiner.y = y;
      this.parent.draw();
      return;
    }

    // Track mouse position for assignment mode
    if (this.state.assignmentMode && this.state.assignmentSourceTask) {
      const x = (e.clientX - rect.left - this.state.offsetX) / this.state.scale;
      const y = (e.clientY - rect.top - this.state.offsetY) / this.state.scale;
      this.state.assignmentMouseX = x;
      this.state.assignmentMouseY = y;
      this.parent.draw();
      return;
    }

    if (this.state.isDraggingTask && this.state.draggedTask) {
      // Drag the task
      const x = (e.clientX - rect.left - this.state.offsetX) / this.state.scale;
      const y = (e.clientY - rect.top - this.state.offsetY) / this.state.scale;

      this.state.draggedTask.x = x;
      this.state.draggedTask.y = y;
      this.parent.draw();
      return;
    }

    if (this.state.isDraggingAgent && this.state.draggedAgent) {
      // Drag the agent
      const x = (e.clientX - rect.left - this.state.offsetX) / this.state.scale;
      const y = (e.clientY - rect.top - this.state.offsetY) / this.state.scale;

      this.state.draggedAgent.x = x;
      this.state.draggedAgent.y = y;
      this.parent.draw();
      return;
    }

    if (this.state.isDragging) {
      // Pan the canvas
      this.state.offsetX = (e.clientX - rect.left) - this.state.dragStartX;
      this.state.offsetY = (e.clientY - rect.top) - this.state.dragStartY;
      this.parent.draw();
      return;
    }

    // Check hover over copy button (screen coordinates, not scaled)
    if (this.state.copyButtonBounds) {
      const mouseX = e.clientX - rect.left;
      const mouseY = e.clientY - rect.top;
      const bounds = this.state.copyButtonBounds;

      const isHovering = mouseX >= bounds.x && mouseX <= bounds.x + bounds.width &&
                        mouseY >= bounds.y && mouseY <= bounds.y + bounds.height;

      const prevState = this.state.copyButtonState;
      if (isHovering && this.state.copyButtonState === 'idle') {
        this.state.copyButtonState = 'hover';
        this.canvas.style.cursor = 'pointer';
        this.parent.draw();
      } else if (!isHovering && this.state.copyButtonState === 'hover') {
        this.state.copyButtonState = 'idle';
        this.canvas.style.cursor = 'grab';
        this.parent.draw();
      }
    }
  }

  /**
   * Handle mouse up events
   * Completes dragging operations and creates connections
   *
   * @param {MouseEvent} e - The mouse event
   */
  onMouseUp(e) {
    const wasDraggingAgent = this.state.isDraggingAgent;
    const wasDraggingTask = this.state.isDraggingTask;
    const wasDraggingConnection = this.state.isDraggingConnection;
    const wasDraggingCombiner = this.state.isDraggingCombiner;

    // Handle connection drop
    if (wasDraggingConnection && this.state.connectionDragStart) {
      const rect = this.canvas.getBoundingClientRect();
      const x = (e.clientX - rect.left - this.state.offsetX) / this.state.scale;
      const y = (e.clientY - rect.top - this.state.offsetY) / this.state.scale;

      // Find port at drop position
      const targetPort = this.parent.getPortAtPosition(x, y);
      let resolvedPort = targetPort;

      // Fallback: if no explicit port hit but dropped on an agent body, treat as input port
      if (!resolvedPort) {
        for (const agent of this.state.agents) {
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
        for (const combiner of this.state.combinerNodes) {
          if (x >= combiner.x && x <= combiner.x + combiner.width &&
              y >= combiner.y && y <= combiner.y + combiner.height) {
            const nextIndex = Math.max(combiner.inputPorts.length, 0);
            const portId = `input-${nextIndex}`;
            this.parent.ensureCombinerInputPort(combiner, portId);
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
      if (!resolvedPort && this.state.agents.length > 0) {
        let closest = null;
        let closestDist = Infinity;
        this.state.agents.forEach(agent => {
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

      if (resolvedPort && resolvedPort.nodeId !== this.state.connectionDragStart.nodeId) {
        // Create connection
        this.parent.createConnection(
          this.state.connectionDragStart.nodeId,
          this.state.connectionDragStart.portId,
          resolvedPort.nodeId,
          resolvedPort.portId
        );
        this.parent.showNotification('Connection created', 'success');
      }

      // Clear connection drag state
      this.state.isDraggingConnection = false;
      this.state.connectionDragStart = null;
      this.canvas.style.cursor = 'grab';
      this.parent.draw();
      return;
    }

    this.state.isDragging = false;
    this.state.isDraggingAgent = false;
    this.state.draggedAgent = null;
    this.state.isDraggingTask = false;
    this.state.isDraggingCombiner = false;
    this.state.draggedCombiner = null;
    this.state.draggedTask = null;

    // Save layout if we were dragging something
    if (wasDraggingAgent || wasDraggingTask || wasDraggingCombiner) {
      this.parent.saveLayout();
    }

    // Preserve cursor state for assignment/connection modes
    if (this.state.assignmentMode || this.state.combinerAssignMode) {
      this.canvas.style.cursor = 'crosshair';
    } else {
      this.canvas.style.cursor = 'grab';
    }
  }

  /**
   * Handle mouse wheel events
   * Zooms the canvas or scrolls panels
   *
   * @param {WheelEvent} e - The wheel event
   */
  onWheel(e) {
    e.preventDefault();

    const rect = this.canvas.getBoundingClientRect();
    const mouseX = e.clientX - rect.left;
    const mouseY = e.clientY - rect.top;

    // Check if mouse is over agent panel for scrolling
    if (this.state.expandedAgentPanelWidth > 0 && this.state.expandedAgent) {
      const panelX = this.state.width - this.state.expandedAgentPanelWidth;
      const panelY = 0;
      const panelWidth = this.state.expandedAgentPanelWidth;
      const panelHeight = this.state.height;

      if (mouseX >= panelX && mouseX <= panelX + panelWidth &&
          mouseY >= panelY && mouseY <= panelY + panelHeight) {
        // Scroll the agent panel content
        const scrollAmount = e.deltaY > 0 ? 20 : -20; // Scroll 20 pixels at a time

        this.state.agentPanelScrollOffset += scrollAmount;
        this.state.agentPanelScrollOffset = Math.max(0, Math.min(this.state.agentPanelMaxScroll, this.state.agentPanelScrollOffset));

        this.parent.draw();
        return;
      }
    }

    // Check if mouse is over result box for scrolling
    if (this.state.resultBoxBounds && this.state.expandedTask) {
      const bounds = this.state.resultBoxBounds;
      if (mouseX >= bounds.x && mouseX <= bounds.x + bounds.width &&
          mouseY >= bounds.y && mouseY <= bounds.y + bounds.height) {
        // Scroll the result content
        const scrollAmount = e.deltaY > 0 ? 3 : -3; // Scroll 3 lines at a time
        this.state.resultScrollOffset += scrollAmount;
        this.parent.draw();
        return;
      }
    }

    // Otherwise, zoom the canvas relative to mouse position
    const delta = e.deltaY > 0 ? 0.9 : 1.1;
    const oldScale = this.state.scale;
    const newScale = Math.max(0.5, Math.min(2, oldScale * delta));

    // Calculate the point in canvas coordinates before zoom
    const canvasX = (mouseX - this.state.offsetX) / oldScale;
    const canvasY = (mouseY - this.state.offsetY) / oldScale;

    // Update scale
    this.state.scale = newScale;

    // Adjust offset so the point under the mouse stays in the same screen position
    this.state.offsetX = mouseX - canvasX * newScale;
    this.state.offsetY = mouseY - canvasY * newScale;

    this.parent.draw();
  }

  /**
   * Handle click events
   * Processes clicks on UI buttons, forms, agents, tasks, and panels
   *
   * @param {MouseEvent} e - The mouse event
   */
  onClick(e) {
    console.log('[CANVAS CLICK] onClick called', {
      isDragging: this.state.isDragging,
      isDraggingAgent: this.state.isDraggingAgent,
      isDraggingTask: this.state.isDraggingTask
    });

    // Ignore clicks during drag operations
    if (this.state.isDragging || this.state.isDraggingAgent || this.state.isDraggingTask) {
      console.log('[CANVAS CLICK] Ignoring click - drag operation in progress');
      return;
    }

    const rect = this.canvas.getBoundingClientRect();
    // Screen coordinates (for UI elements like the panel)
    const screenX = e.clientX - rect.left;
    const screenY = e.clientY - rect.top;

    // Check for clicks on add agent form (highest priority when visible)
    if (this.state.forms.addAgentFormVisible) {
      // Check close button
      if (this.state.forms.addAgentCloseButtonBounds) {
        const btn = this.state.forms.addAgentCloseButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.state.forms.hideAddAgentForm();
          return;
        }
      }

      // Check submit button
      if (this.state.forms.addAgentSubmitButtonBounds) {
        const btn = this.state.forms.addAgentSubmitButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.state.forms.submitAddAgentForm();
          return;
        }
      }

      // Check agent selection buttons
      if (this.state.forms.agentAddSelectionBounds) {
        for (const bounds of this.state.forms.agentAddSelectionBounds) {
          if (bounds && screenX >= bounds.x && screenX <= bounds.x + bounds.width &&
              screenY >= bounds.y && screenY <= bounds.y + bounds.height) {
            this.state.forms.selectedAgentToAdd = bounds.agentName;
            this.parent.draw();
            return;
          }
        }
      }

      // Click outside form - close it
      if (this.state.forms.addAgentFormBounds) {
        const form = this.state.forms.addAgentFormBounds;
        if (screenX < form.x || screenX > form.x + form.width ||
            screenY < form.y || screenY > form.y + form.height) {
          this.state.forms.hideAddAgentForm();
          return;
        }
      }

      // Click inside form but not on any interactive element - do nothing
      return;
    }

    // Check for clicks on create task form (highest priority when visible)
    if (this.state.forms.createTaskFormVisible) {
      // Check close button
      if (this.state.forms.createTaskCloseButtonBounds) {
        const btn = this.state.forms.createTaskCloseButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.state.forms.hideCreateTaskForm();
          return;
        }
      }


      // Check submit button
      if (this.state.forms.createTaskSubmitButtonBounds) {
        const btn = this.state.forms.createTaskSubmitButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.state.forms.submitCreateTaskForm();
          return;
        }
      }

      // Check checkbox
      if (this.state.forms.createTaskCheckboxBounds) {
        const cb = this.state.forms.createTaskCheckboxBounds;
        if (screenX >= cb.x && screenX <= cb.x + cb.width &&
            screenY >= cb.y && screenY <= cb.y + cb.height) {
          this.state.forms.createTaskAssignToAgent = !this.state.forms.createTaskAssignToAgent;
          if (!this.state.forms.createTaskAssignToAgent) {
            this.state.forms.selectedAgentForTask = null;
          }
          this.parent.draw();
          return;
        }
      }

      // Check agent selection buttons
      if (this.state.forms.createTaskAssignToAgent && this.state.forms.agentSelectionBounds) {
        for (const bounds of this.state.forms.agentSelectionBounds) {
          if (bounds && screenX >= bounds.x && screenX <= bounds.x + bounds.width &&
              screenY >= bounds.y && screenY <= bounds.y + bounds.height) {
            this.state.forms.selectedAgentForTask = bounds.agentName;
            this.parent.draw();
            return;
          }
        }
      }

      // Check description field - enable direct typing
      if (this.state.forms.createTaskDescriptionBounds) {
        const input = this.state.forms.createTaskDescriptionBounds;
        if (screenX >= input.x && screenX <= input.x + input.width &&
            screenY >= input.y && screenY <= input.y + input.height) {
          this.state.forms.createTaskDescriptionFocused = true;
          this.canvas.style.cursor = 'text';
          this.parent.draw();
          return;
        } else if (this.state.forms.createTaskDescriptionFocused) {
          // Clicked somewhere else in the form, unfocus description field
          this.state.forms.createTaskDescriptionFocused = false;
          this.canvas.style.cursor = 'default';
          this.parent.draw();
        }
      }

      // Click outside form - close it
      if (this.state.forms.createTaskFormBounds) {
        const form = this.state.forms.createTaskFormBounds;
        if (screenX < form.x || screenX > form.x + form.width ||
            screenY < form.y || screenY > form.y + form.height) {
          this.state.forms.hideCreateTaskForm();
          return;
        }
      }

      // Click inside form but not on any interactive element - do nothing
      return;
    }

    // Check for click on "Create Task" button
    if (this.state.createTaskButtonBounds) {
      const btn = this.state.createTaskButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.state.forms.showCreateTaskForm();
        return;
      }
    }

    // Check for click on "Add Agent" button
    if (this.state.addAgentButtonBounds) {
      const btn = this.state.addAgentButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.state.forms.showAddAgentForm();
        return;
      }
    }

    // Check for click on "Timeline" toggle button
    if (this.state.timelineToggleBounds) {
      const btn = this.state.timelineToggleBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.parent.toggleTimeline();
        return;
      }
    }

    // Check for click on "Auto-Layout" button
    if (this.state.autoLayoutButtonBounds) {
      const btn = this.state.autoLayoutButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.parent.autoLayoutTasks();
        return;
      }
    }

    // Check for click on "Save Layout" button
    if (this.state.saveLayoutButtonBounds) {
      const btn = this.state.saveLayoutButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.parent.saveLayout();
        this.parent.showNotification('💾 Layout saved', 'success');
        return;
      }
    }

    // Check for click on timeline panel close button
    if (this.state.timelinePanelWidth > 0) {
      const panelX = this.state.width - this.state.timelinePanelWidth;
      const closeButtonX = panelX + this.state.timelinePanelWidth - 30;
      const closeButtonY = 15;
      const closeButtonSize = 30;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.parent.toggleTimeline();
        return;
      }
    }

    // Check if click is on close button of expanded agent panel
    if (this.state.expandedAgentPanelWidth > 0) {
      const panelX = this.state.width - this.state.expandedAgentPanelWidth;
      const closeButtonX = panelX + this.state.expandedAgentPanelWidth - 40;
      const closeButtonY = 30;
      const closeButtonSize = 40;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.parent.closeAgentPanel();
        return;
      }

      // If clicking anywhere on the agent panel, don't process other clicks
      if (screenX >= panelX) {
        return;
      }
    }

    // Check if click is on close button of expanded combiner panel
    if (this.state.expandedCombinerPanelWidth > 0) {
      const panelX = this.state.width - this.state.expandedCombinerPanelWidth;
      const closeButtonX = panelX + this.state.expandedCombinerPanelWidth - 40;
      const closeButtonY = 30;
      const closeButtonSize = 40;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.parent.closeCombinerPanel();
        return;
      }

      // If clicking anywhere on the combiner panel, don't process other clicks
      if (screenX >= panelX) {
        return;
      }
    }

    // Check if click is on close button of expanded task panel
    if (this.state.expandedPanelWidth > 0) {
      const panelX = this.state.width - this.state.expandedPanelWidth;
      const closeButtonX = panelX + this.state.expandedPanelWidth - 40;
      const closeButtonY = 30;
      const closeButtonSize = 40;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.parent.closeTaskPanel();
        return;
      }

      // Check if click is on copy button
      if (this.state.copyButtonBounds) {
        const btn = this.state.copyButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.parent.copyResultToClipboard();
          return;
        }
      }

      // If clicking anywhere on the panel, don't process other clicks
      if (screenX >= panelX) {
        return;
      }
    }

    // Convert screen coordinates to canvas coordinates
    console.log('[CANVAS CLICK] Converting coords:', {
      clientX: e.clientX,
      clientY: e.clientY,
      'rect.left': rect.left,
      'rect.top': rect.top,
      offsetX: this.state.offsetX,
      offsetY: this.state.offsetY,
      scale: this.state.scale
    });

    const x = (e.clientX - rect.left - this.state.offsetX) / this.state.scale;
    const y = (e.clientY - rect.top - this.state.offsetY) / this.state.scale;

    console.log('[CANVAS CLICK] Converted canvas coords:', { x, y });

    // Check if click is on any task first (tasks are on top)
    for (let i = this.state.tasks.length - 1; i >= 0; i--) {
      const task = this.state.tasks[i];
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
            this.parent.deleteTask(task);
            return;
          }
        }

        // Check if click is on execute button
        if (task.executeBtnBounds && task.status === 'pending') {
          const btn = task.executeBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Execute button clicked
            this.parent.executeTask(task);
            return;
          }
        }

        // Check if click is on rerun button
        if (task.rerunBtnBounds && (task.status === 'completed' || task.status === 'failed')) {
          const btn = task.rerunBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Rerun button clicked
            this.parent.rerunTask(task);
            return;
          }
        }

        // Check if click is on assign button
        if (task.assignBtnBounds && task.status !== 'completed') {
          const btn = task.assignBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Assign button clicked - toggle assignment mode
            this.parent.toggleAssignmentMode(task);
            return;
          }
        }

        // Check if click is on view log button
        if (task.viewLogBtnBounds) {
          const btn = task.viewLogBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // View log button clicked - show execution log modal
            this.parent.showExecutionLog(task);
            return;
          }
        }

        if (x >= cardX && x <= cardX + cardWidth &&
            y >= cardY && y <= cardY + cardHeight) {
          // Task clicked - show details in sidebar
          console.log('[CANVAS CLICK] Task clicked:', task.description, 'showing details');
          if (window.showTaskDetails) {
            window.showTaskDetails(task);
            console.log('[CANVAS CLICK] showTaskDetails called');
          } else {
            console.error('[CANVAS CLICK] window.showTaskDetails is not available!');
          }
          return;
        }
      }
    }

    // Check if click is on any agent
    console.log('[CANVAS CLICK] Checking agents:', this.state.agents.length, 'agents');
    console.log('[CANVAS CLICK] Click coords (canvas):', { x, y });

    for (const agent of this.state.agents) {
      const halfWidth = (agent.width || 120) / 2;
      const halfHeight = (agent.height || 70) / 2;
      const bounds = {
        left: agent.x - halfWidth,
        right: agent.x + halfWidth,
        top: agent.y - halfHeight,
        bottom: agent.y + halfHeight
      };
      const inBounds = x >= bounds.left && x <= bounds.right &&
          y >= bounds.top && y <= bounds.bottom;

      console.log(`[CANVAS CLICK] Agent ${agent.name}:`, {
        position: { x: agent.x, y: agent.y },
        bounds,
        inBounds
      });

      if (inBounds) {
        // Agent clicked
        console.log('[CANVAS CLICK] Agent clicked:', agent.name, 'assignmentMode:', this.state.assignmentMode, 'combinerAssignMode:', this.state.combinerAssignMode);
        if (this.state.assignmentMode && this.state.assignmentSourceTask) {
          // In assignment mode - assign task to agent
          console.log('[CANVAS CLICK] Assigning task to agent:', agent.name);
          this.parent.assignTaskToAgent(agent);
          return;
        } else if (this.state.combinerAssignMode && this.state.combinerAssignmentSource) {
          // Wire combiner output to this agent
          this.parent.createConnection(this.state.combinerAssignmentSource.id, 'output', agent.name, 'input');
          this.state.combinerAssignMode = false;
          this.state.combinerAssignmentSource = null;
          this.canvas.style.cursor = 'grab';
          this.parent.draw();
          this.parent.saveLayout();
          this.parent.showNotification(`Combiner output connected to ${agent.name}`, 'success');
          return;
        } else {
          // Show agent details in sidebar
          console.log('[CANVAS CLICK] Showing agent details in sidebar for:', agent.name);
          if (window.showAgentDetails) {
            window.showAgentDetails(agent);
          }
        }
        return;
      }
    }

    // Check combiner node clicks (for task assignment)
    for (const combiner of this.state.combinerNodes) {
      if (x >= combiner.x && x <= combiner.x + combiner.width &&
          y >= combiner.y && y <= combiner.y + combiner.height) {
        // Combiner clicked
        console.log('Combiner clicked:', combiner.id, 'assignmentMode:', this.state.assignmentMode, 'combinerAssignMode:', this.state.combinerAssignMode);
        if (this.state.assignmentMode && this.state.assignmentSourceTask) {
          // In assignment mode - assign task to combiner
          console.log('Assigning task to combiner:', combiner.id);
          this.parent.assignTaskToCombiner(combiner);
          return;
        }

        // Trigger combiner click callback (shows sidebar details)
        if (window.showCombinerDetails) {
          window.showCombinerDetails(combiner);
        } else if (this.parent.onCombinerClick) {
          this.parent.onCombinerClick(combiner);
        } else {
          // Fallback: toggle combiner detail panel on canvas
          this.parent.toggleCombinerPanel(combiner);
        }
        return;
      }
    }

    // Click on empty space - close expanded panels
    if (this.state.expandedTask) {
      this.parent.closeTaskPanel();
    }
    if (this.state.expandedAgent) {
      this.parent.closeAgentPanel();
    }
    if (this.state.expandedCombiner) {
      this.parent.closeCombinerPanel();
    }
  }

  /**
   * Handle keyboard key up events
   * Releases modifier keys (space, ctrl/cmd)
   *
   * @param {KeyboardEvent} e - The keyboard event
   */
  onKeyUp(e) {
    if (e.key === ' ') {
      this.state.spacePressed = false;
      if (!this.state.isDragging) {
        this.canvas.style.cursor = 'grab';
      }
    }
    if (!e.ctrlKey && !e.metaKey) {
      this.state.ctrlPressed = false;
    }
  }

  /**
   * Handle context menu (right-click) events
   * Shows context menus for agents and connections
   *
   * @param {MouseEvent} e - The mouse event
   */
  onContextMenu(e) {
    e.preventDefault();

    const rect = this.canvas.getBoundingClientRect();
    const screenX = e.clientX - rect.left;
    const screenY = e.clientY - rect.top;

    // Convert to canvas coordinates
    const canvasX = (screenX - this.state.offsetX) / this.state.scale;
    const canvasY = (screenY - this.state.offsetY) / this.state.scale;

    // Check if clicking on a connection (highest priority for context menu)
    const clickedConnection = this.parent.getConnectionAtPosition(canvasX, canvasY);
    if (clickedConnection) {
      // Confirm and delete connection
      if (confirm('Delete this connection?')) {
        this.parent.deleteConnection(clickedConnection.id);
        this.parent.showNotification('Connection deleted', 'success');
      }
      return;
    }

    // Check if clicking on an agent
    const clickedAgent = this.state.agents.find(agent => {
      const halfWidth = (agent.width || 120) / 2;
      const halfHeight = (agent.height || 70) / 2;
      return canvasX >= agent.x - halfWidth && canvasX <= agent.x + halfWidth &&
             canvasY >= agent.y - halfHeight && canvasY <= agent.y + halfHeight;
    });

    if (clickedAgent) {
      this.state.contextMenuVisible = true;
      this.state.contextMenuAgent = clickedAgent;
      this.state.contextMenuX = screenX;
      this.state.contextMenuY = screenY;
      this.parent.draw();
    }
  }
}
