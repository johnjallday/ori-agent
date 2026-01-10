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
   * Calculate the content bounds from all positioned nodes
   * Returns { minX, maxX, minY, maxY, centerX, centerY } or null if no positioned content
   */
  getContentBounds() {
    let minX = Infinity, maxX = -Infinity;
    let minY = Infinity, maxY = -Infinity;
    let hasContent = false;

    // Include agents
    this.state.agents.forEach(agent => {
      if (agent.x != null && agent.y != null) {
        minX = Math.min(minX, agent.x);
        maxX = Math.max(maxX, agent.x);
        minY = Math.min(minY, agent.y);
        maxY = Math.max(maxY, agent.y);
        hasContent = true;
      }
    });

    // Include tasks
    this.state.tasks.forEach(task => {
      if (task.x != null && task.y != null) {
        minX = Math.min(minX, task.x);
        maxX = Math.max(maxX, task.x);
        minY = Math.min(minY, task.y);
        maxY = Math.max(maxY, task.y);
        hasContent = true;
      }
    });

    // Include attachments
    this.state.attachments.forEach(att => {
      if (att.x != null && att.y != null) {
        minX = Math.min(minX, att.x);
        maxX = Math.max(maxX, att.x);
        minY = Math.min(minY, att.y);
        maxY = Math.max(maxY, att.y);
        hasContent = true;
      }
    });

    // Include store nodes
    this.state.storeNodes.forEach(s => {
      if (s.x != null && s.y != null) {
        minX = Math.min(minX, s.x);
        maxX = Math.max(maxX, s.x);
        minY = Math.min(minY, s.y);
        maxY = Math.max(maxY, s.y);
        hasContent = true;
      }
    });

    // Include combiner nodes
    this.state.combinerNodes.forEach(c => {
      if (c.x != null && c.y != null) {
        minX = Math.min(minX, c.x);
        maxX = Math.max(maxX, c.x);
        minY = Math.min(minY, c.y);
        maxY = Math.max(maxY, c.y);
        hasContent = true;
      }
    });

    if (!hasContent) return null;

    return {
      minX, maxX, minY, maxY,
      centerX: (minX + maxX) / 2,
      centerY: (minY + maxY) / 2
    };
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
   * Zoom and pan to fit all content in view
   */
  zoomToFitContent() {
    // Check if there's any content to fit
    const hasContent =
      (this.state.tasks && this.state.tasks.length > 0) ||
      (this.state.agents && this.state.agents.length > 0) ||
      (this.state.attachments && this.state.attachments.length > 0) ||
      (this.state.storeNodes && this.state.storeNodes.length > 0) ||
      (this.state.combinerNodes && this.state.combinerNodes.length > 0);

    if (!hasContent) {
      return;
    }

    // Calculate bounding box of all content
    let minX = Infinity, maxX = -Infinity;
    let minY = Infinity, maxY = -Infinity;

    // Include tasks
    this.state.tasks.forEach(task => {
      if (task.x == null || task.y == null) return;
      const taskWidth = 180;
      const taskHeight = 100;
      minX = Math.min(minX, task.x - taskWidth / 2);
      maxX = Math.max(maxX, task.x + taskWidth / 2);
      minY = Math.min(minY, task.y - taskHeight / 2);
      maxY = Math.max(maxY, task.y + taskHeight / 2);
    });

    // Include agents
    this.state.agents.forEach(agent => {
      if (agent.x == null || agent.y == null) return;
      const halfW = (agent.width || 120) / 2;
      const halfH = (agent.height || 70) / 2;
      minX = Math.min(minX, agent.x - halfW);
      maxX = Math.max(maxX, agent.x + halfW);
      minY = Math.min(minY, agent.y - halfH);
      maxY = Math.max(maxY, agent.y + halfH);
    });

    // Include attachments
    this.state.attachments.forEach(att => {
      if (att.x == null || att.y == null) return;
      const cardWidth = 160;
      const cardHeight = 100;
      minX = Math.min(minX, att.x - cardWidth / 2);
      maxX = Math.max(maxX, att.x + cardWidth / 2);
      minY = Math.min(minY, att.y - cardHeight / 2);
      maxY = Math.max(maxY, att.y + cardHeight / 2);
    });

    // Include store nodes
    this.state.storeNodes.forEach(s => {
      if (s.x == null || s.y == null) return;
      const nodeWidth = 160;
      const nodeHeight = 100;
      minX = Math.min(minX, s.x - nodeWidth / 2);
      maxX = Math.max(maxX, s.x + nodeWidth / 2);
      minY = Math.min(minY, s.y - nodeHeight / 2);
      maxY = Math.max(maxY, s.y + nodeHeight / 2);
    });

    // Include combiner nodes
    this.state.combinerNodes.forEach(c => {
      if (c.x == null || c.y == null) return;
      const nodeWidth = c.width || 120;
      const nodeHeight = c.height || 80;
      minX = Math.min(minX, c.x - nodeWidth / 2);
      maxX = Math.max(maxX, c.x + nodeWidth / 2);
      minY = Math.min(minY, c.y - nodeHeight / 2);
      maxY = Math.max(maxY, c.y + nodeHeight / 2);
    });

    // If no valid positions found, return early
    if (minX === Infinity || maxX === -Infinity) {
      return;
    }

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
        const key = agent.nodeId || agent.name;
        console.log(`  📍 Agent ${key}: (${agent.x}, ${agent.y})`);
        agentPositions[key] = { x: agent.x, y: agent.y };
      });

      // Collect attachment positions
      const attachmentPositions = {};
      this.state.attachments.forEach(att => {
        if (att.x == null || att.y == null) return;
        attachmentPositions[att.id] = { x: att.x, y: att.y };
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

      // Collect store node positions
      const storePositions = {};
      this.state.storeNodes.forEach(s => {
        if (s.x == null || s.y == null) return;
        const key = s.canvas_node_id || s.id;
        storePositions[key] = { x: s.x, y: s.y };
      });

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
      console.log(`  Tasks: ${Object.keys(taskPositions).length}, Agents: ${Object.keys(agentPositions).length}, Attachments: ${Object.keys(attachmentPositions).length}, Combiners: ${combinerNodes.length}, Stores: ${Object.keys(storePositions).length}, Connections: ${workflowConnections.length}`);
      console.log(`  Scale: ${this.state.scale}, Offset: (${this.state.offsetX}, ${this.state.offsetY})`);
      console.log(`  Task positions:`, taskPositions);
      console.log(`  Agent positions:`, agentPositions);

      await apiPut('/api/orchestration/workspace/layout', {
        workspace_id: this.state.studioId,
        task_positions: taskPositions,
        agent_positions: agentPositions,
        attachment_positions: attachmentPositions,
        store_positions: storePositions,
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
      const tasksWithPositions = [];
      const tasksWithoutPositions = [];

      this.state.tasks.forEach(task => {
        const savedPos = layout.task_positions[task.id];
        if (savedPos) {
          console.log(`  Restoring task ${task.id} to (${savedPos.x}, ${savedPos.y})`);
          task.x = savedPos.x;
          task.y = savedPos.y;
          tasksRestored++;
          tasksWithPositions.push(task);
        } else {
          tasksWithoutPositions.push(task);
        }
      });

      // Position new tasks (without saved positions) near existing content
      if (tasksWithoutPositions.length > 0) {
        let baseX, baseY;
        const spacing = 200;

        if (tasksWithPositions.length > 0) {
          // Position near existing tasks
          let maxX = -Infinity;
          let totalY = 0;
          tasksWithPositions.forEach(task => {
            maxX = Math.max(maxX, task.x);
            totalY += task.y;
          });
          baseX = maxX;
          baseY = totalY / tasksWithPositions.length;
        } else {
          // Fallback: position near general content cluster
          const bounds = this.getContentBounds();
          if (bounds) {
            baseX = bounds.maxX;
            baseY = bounds.centerY;
          } else {
            // No content at all, use center of canvas
            baseX = this.parent.width / 2;
            baseY = this.parent.height / 2;
          }
        }

        tasksWithoutPositions.forEach((task, index) => {
          task.x = baseX + spacing * (index + 1);
          task.y = baseY;
          console.log(`  Positioning new task ${task.id} at (${task.x}, ${task.y})`);
        });
      }
    }

    // Restore agent positions
    if (layout.agent_positions) {
      const agentsWithPositions = [];
      const agentsWithoutPositions = [];

      this.state.agents.forEach(agent => {
        const key = agent.nodeId || agent.name;
        const savedPos = layout.agent_positions[key] || layout.agent_positions[agent.name];
        if (savedPos) {
          console.log(`  Restoring agent ${key} to (${savedPos.x}, ${savedPos.y})`);
          agent.x = savedPos.x;
          agent.y = savedPos.y;
          agentsRestored++;
          agentsWithPositions.push(agent);
        } else {
          agentsWithoutPositions.push(agent);
        }
      });

      // Position new agents (without saved positions) near existing content
      if (agentsWithoutPositions.length > 0) {
        let baseX, baseY;
        const spacing = 150;

        if (agentsWithPositions.length > 0) {
          // Position near existing agents
          let maxX = -Infinity;
          let totalY = 0;
          agentsWithPositions.forEach(agent => {
            maxX = Math.max(maxX, agent.x);
            totalY += agent.y;
          });
          baseX = maxX;
          baseY = totalY / agentsWithPositions.length;
        } else {
          // Fallback: position near general content cluster
          const bounds = this.getContentBounds();
          if (bounds) {
            baseX = bounds.maxX;
            baseY = bounds.centerY;
          } else {
            // No content at all, use center of canvas
            baseX = this.parent.width / 2;
            baseY = this.parent.height / 2;
          }
        }

        agentsWithoutPositions.forEach((agent, index) => {
          agent.x = baseX + spacing * (index + 1);
          agent.y = baseY;
          console.log(`  Positioning new agent ${agent.nodeId || agent.name} at (${agent.x}, ${agent.y})`);
        });
      }
    }

    // Restore attachment positions
    if (layout.attachment_positions) {
      const attachmentsWithPositions = [];
      const attachmentsWithoutPositions = [];

      this.state.attachments.forEach(att => {
        const savedPos = layout.attachment_positions[att.id];
        if (savedPos) {
          att.x = savedPos.x;
          att.y = savedPos.y;
          attachmentsWithPositions.push(att);
        } else {
          attachmentsWithoutPositions.push(att);
        }
      });

      // Position new attachments near existing content
      if (attachmentsWithoutPositions.length > 0) {
        let baseX, baseY;
        const spacing = 180;

        if (attachmentsWithPositions.length > 0) {
          // Position near existing attachments
          let maxX = -Infinity;
          let totalY = 0;
          attachmentsWithPositions.forEach(att => {
            maxX = Math.max(maxX, att.x);
            totalY += att.y;
          });
          baseX = maxX;
          baseY = totalY / attachmentsWithPositions.length;
        } else {
          // Fallback: position near general content cluster
          const bounds = this.getContentBounds();
          if (bounds) {
            baseX = bounds.maxX;
            baseY = bounds.centerY;
          } else {
            // No content at all, use center of canvas
            baseX = this.parent.width / 2;
            baseY = this.parent.height / 2;
          }
        }

        attachmentsWithoutPositions.forEach((att, index) => {
          att.x = baseX + spacing * (index + 1);
          att.y = baseY;
          console.log(`  Positioning new attachment ${att.id} at (${att.x}, ${att.y})`);
        });
      }
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

    // Restore store node positions
    if (layout.store_positions) {
      const storesWithPositions = [];
      const storesWithoutPositions = [];

      this.state.storeNodes.forEach(s => {
        const key = s.canvas_node_id || s.id;
        const savedPos = layout.store_positions[key];
        if (savedPos) {
          s.x = savedPos.x;
          s.y = savedPos.y;
          storesWithPositions.push(s);
        } else {
          storesWithoutPositions.push(s);
        }
      });

      // Position new store nodes near existing content
      if (storesWithoutPositions.length > 0) {
        let baseX, baseY;
        const spacing = 180;

        if (storesWithPositions.length > 0) {
          // Position near existing stores
          let maxX = -Infinity;
          let totalY = 0;
          storesWithPositions.forEach(s => {
            maxX = Math.max(maxX, s.x);
            totalY += s.y;
          });
          baseX = maxX;
          baseY = totalY / storesWithPositions.length;
        } else {
          // Fallback: position near general content cluster
          const bounds = this.getContentBounds();
          if (bounds) {
            baseX = bounds.maxX;
            baseY = bounds.centerY;
          } else {
            // No content at all, use center of canvas
            baseX = this.parent.width / 2;
            baseY = this.parent.height / 2;
          }
        }

        storesWithoutPositions.forEach((s, index) => {
          s.x = baseX + spacing * (index + 1);
          s.y = baseY;
          console.log(`  Positioning new store ${s.canvas_node_id || s.id} at (${s.x}, ${s.y})`);
        });
      }
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
