import { apiPut } from './agent-canvas-api.js';

/**
 * AgentCanvasLayoutManager
 * Manages canvas layout operations including auto-layout, zoom-to-fit, and layout persistence
 */
export class AgentCanvasLayoutManager {
  /**
   * @param {AgentCanvasState} state - Shared state object
   * @param {AgentCanvas} parent - Parent AgentCanvas instance
   */
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Auto-layout tasks in a hierarchical flow (top to bottom)
   */
  autoLayoutTasks() {
    if (!this.state.tasks || this.state.tasks.length === 0) return;

    // Calculate dependency levels (topological sort)
    const levels = this.calculateTaskLevels();

    // Get canvas dimensions
    const canvasWidth = this.parent.width / this.state.scale;
    const canvasHeight = this.parent.height / this.state.scale;

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
          const agent = this.state.agents.find(a => a.name === agentName);
          if (agent) {
            agent.x = agentColumnX;
            agent.y = task.y; // Align agent with its task
          }
        }
      });
    });

    // Auto-zoom to fit all content
    this.zoomToFitContent();

    this.parent.draw();
    this.parent.showNotification('✨ Tasks auto-arranged', 'success');

    // Save the new layout
    this.saveLayout();
  }

  /**
   * Zoom and pan to fit all tasks and agents in view
   */
  zoomToFitContent() {
    if ((!this.state.tasks || this.state.tasks.length === 0) && (!this.state.agents || this.state.agents.length === 0)) {
      return;
    }

    // Calculate bounding box of all content
    let minX = Infinity, maxX = -Infinity;
    let minY = Infinity, maxY = -Infinity;

    // Include tasks
    this.state.tasks.forEach(task => {
      const taskWidth = 180;
      const taskHeight = 100;
      minX = Math.min(minX, task.x - taskWidth / 2);
      maxX = Math.max(maxX, task.x + taskWidth / 2);
      minY = Math.min(minY, task.y - taskHeight / 2);
      maxY = Math.max(maxY, task.y + taskHeight / 2);
    });

    // Include agents
    this.state.agents.forEach(agent => {
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
    const scaleX = this.parent.width / (contentWidth + padding * 2);
    const scaleY = this.parent.height / (contentHeight + padding * 2);
    const newScale = Math.min(scaleX, scaleY, 1.0); // Don't zoom in beyond 100%

    // Clamp scale to reasonable limits
    this.state.scale = Math.max(0.3, Math.min(1.0, newScale));

    // Calculate offset to center content
    this.state.offsetX = (this.parent.width / 2) - (contentCenterX * this.state.scale);
    this.state.offsetY = (this.parent.height / 2) - (contentCenterY * this.state.scale);
  }

  /**
   * Zoom to fit all agents in viewport
   */
  zoomToFit() {
    if (this.state.agents.length === 0) {
      // No agents, just reset to default
      this.state.offsetX = 0;
      this.state.offsetY = 0;
      this.state.scale = 1;
      this.parent.draw();
      return;
    }

    // Find bounding box of all agents
    let minX = Infinity, minY = Infinity;
    let maxX = -Infinity, maxY = -Infinity;

    this.state.agents.forEach(agent => {
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
    const scaleX = (this.parent.width - 2 * padding) / contentWidth;
    const scaleY = (this.parent.height - 2 * padding) / contentHeight;
    const newScale = Math.min(scaleX, scaleY, 2); // Max zoom of 2x

    // Center the content
    const centerX = (minX + maxX) / 2;
    const centerY = (minY + maxY) / 2;

    this.state.scale = newScale;
    this.state.offsetX = this.parent.width / 2 - centerX * newScale;
    this.state.offsetY = this.parent.height / 2 - centerY * newScale;

    this.parent.draw();
    console.log('🎯 Zoomed to fit all agents');
  }

  /**
   * Calculate task dependency levels using topological sort
   * @returns {Array<Array>} Array of task groups by level
   */
  calculateTaskLevels() {
    const levels = [];
    const visited = new Set();
    const taskMap = new Map(this.state.tasks.map(t => [t.id, t]));

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
    this.state.tasks.forEach(task => getLevel(task));

    // Group tasks by level
    const maxLevel = Math.max(...this.state.tasks.map(t => t.level || 0));
    for (let i = 0; i <= maxLevel; i++) {
      levels[i] = this.state.tasks.filter(t => (t.level || 0) === i);
    }

    return levels;
  }

  /**
   * Save the current layout (positions and zoom) to the server
   */
  async saveLayout() {
    if (!this.state.studioId) {
      console.log('❌ Cannot save layout: no studioId');
      return;
    }

    try {
      // Keep combiner input ports in sync with actual connections before persisting
      this.state.combinerNodes.forEach(node => this.parent.cleanupCombinerInputPorts(node));

      // Collect task positions
      const taskPositions = {};
      this.state.tasks.forEach(task => {
        console.log(`  📍 Task ${task.id}: (${task.x}, ${task.y})`);
        taskPositions[task.id] = { x: task.x, y: task.y };
      });

      // Collect agent positions
      const agentPositions = {};
      this.state.agents.forEach(agent => {
        console.log(`  📍 Agent ${agent.name}: (${agent.x}, ${agent.y})`);
        agentPositions[agent.name] = { x: agent.x, y: agent.y };
      });

      // Collect combiner nodes
      const combinerNodes = this.state.combinerNodes.map(node => ({
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
      const workflowConnections = this.state.connections.map(conn => ({
        id: conn.id,
        from: conn.from,
        fromPort: conn.fromPort,
        to: conn.to,
        toPort: conn.toPort,
        color: conn.color,
        animated: conn.animated
      }));

      console.log(`💾 Saving layout for workspace ${this.state.studioId}`);
      console.log(`  Tasks: ${Object.keys(taskPositions).length}, Agents: ${Object.keys(agentPositions).length}, Combiners: ${combinerNodes.length}, Connections: ${workflowConnections.length}`);
      console.log(`  Scale: ${this.state.scale}, Offset: (${this.state.offsetX}, ${this.state.offsetY})`);
      console.log(`  Task positions:`, taskPositions);
      console.log(`  Agent positions:`, agentPositions);

      await apiPut('/api/orchestration/workspace/layout', {
        workspace_id: this.state.studioId,
        task_positions: taskPositions,
        agent_positions: agentPositions,
        combiner_nodes: combinerNodes,
        workflow_connections: workflowConnections,
        scale: this.state.scale,
        offset_x: this.state.offsetX,
        offset_y: this.state.offsetY,
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
    if (!this.state.studio) {
      console.log('❌ No studio object, cannot load layout');
      return;
    }

    if (!this.state.studio.layout) {
      console.log('❌ No layout saved for this workspace');
      return;
    }

    const layout = this.state.studio.layout;
    console.log('📂 Loading layout:', layout);

    let tasksRestored = 0;
    let agentsRestored = 0;
    let combinersRestored = 0;
    let connectionsRestored = 0;

    // Restore task positions
    if (layout.task_positions) {
      this.state.tasks.forEach(task => {
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
      this.state.agents.forEach(agent => {
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
      this.state.combinerNodes = layout.combiner_nodes.map(node => ({
        ...node,
        width: node.width || 120,
        height: node.height || 80,
        inputPorts: node.inputPorts || [],
        outputPort: node.outputPort || { id: 'output', x: 0, y: 40 }
      }));
      combinersRestored = this.state.combinerNodes.length;
    }

    // Restore workflow connections
    if (layout.workflow_connections && Array.isArray(layout.workflow_connections)) {
      this.state.connections = layout.workflow_connections;
      // Ensure combiner port state matches restored connections
      this.state.connections.forEach(conn => {
        const targetNode = this.parent.getNodeById(conn.to);
        if (targetNode && targetNode.type === 'combiner' && conn.toPort && conn.toPort.startsWith('input')) {
          this.parent.ensureCombinerInputPort(targetNode.node, conn.toPort);
        }
      });
      connectionsRestored = this.state.connections.length;
    }

    // Remove stale combiner input ports so only active connections are shown
    if (this.state.combinerNodes.length > 0) {
      this.state.combinerNodes.forEach(node => this.parent.cleanupCombinerInputPorts(node));
    }

    // Skip restoring zoom and pan - will be set by zoomToFit() in init
    // This prevents loading extreme zoom values that break the view
    // if (layout.scale) {
    //   this.state.scale = layout.scale;
    //   console.log(`  Restoring scale: ${layout.scale}`);
    // }
    // if (layout.offset_x !== undefined) {
    //   this.state.offsetX = layout.offset_x;
    //   console.log(`  Restoring offsetX: ${layout.offset_x}`);
    // }
    // if (layout.offset_y !== undefined) {
    //   this.state.offsetY = layout.offset_y;
    //   console.log(`  Restoring offsetY: ${layout.offset_y}`);
    // }
    console.log('  Skipping zoom/pan restore - will use zoomToFit() instead');

    console.log(`📂 Layout loaded successfully (${tasksRestored} tasks, ${agentsRestored} agents, ${combinersRestored} combiners, ${connectionsRestored} connections)`);
    this.parent.draw();
  }
}
