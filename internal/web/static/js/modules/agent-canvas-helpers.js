/**
 * Helpers for AgentCanvas shared logic and utilities
 */

/**
 * Deduplicate tasks by id, keeping first occurrence.
 * @param {Array} tasks
 * @returns {Array}
 */
export function dedupeTasksById(tasks) {
  if (!Array.isArray(tasks)) return [];
  const seen = new Set();
  return tasks.filter(task => {
    if (!task || !task.id) return false;
    if (seen.has(task.id)) return false;
    seen.add(task.id);
    return true;
  });
}

/**
 * Remove the combiner's own task from a list of tasks.
 * @param {Array} tasks
 * @param {string} combinerTaskId
 * @returns {Array}
 */
export function removeSelfTask(tasks, combinerTaskId) {
  if (!Array.isArray(tasks)) return [];
  return tasks.filter(task => task && task.id !== combinerTaskId);
}

/**
 * Build a combination instruction that includes available results or descriptions.
 * @param {Array} tasks
 * @returns {string}
 */
export function buildCombinationInstruction(tasks) {
  if (!Array.isArray(tasks) || tasks.length === 0) return '';

  const lines = tasks.map(task => {
    if (task?.result) {
      return `Task ${task.id}: "${task.description || 'task'}" -> Result: ${task.result}`;
    }
    return `Task ${task?.id || 'unknown'}: "${task?.description || 'task'}" (no result yet, use the prompt/description)`;
  });

  return `Combine the following inputs (use description when result is missing):\n- ${lines.join('\n- ')}`;
}

/**
 * AgentCanvasHelpers - Utility methods for canvas operations
 * Contains helper functions for color manipulation, node/port queries, and calculations
 */
export class AgentCanvasHelpers {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
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

  /**
   * Get agent color based on index
   */
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

  /**
   * Get node by ID (searches both agents and combiners)
   */
  getNodeById(nodeId) {
    // Check if it's an agent (support both nodeId and legacy name lookups)
    const agent = this.state.agents.find(a => a.nodeId === nodeId || a.name === nodeId || a.id === nodeId);
    if (agent) return { type: 'agent', node: agent };

    // Check if it's a task
    const task = this.state.tasks.find(t => t.id === nodeId);
    if (task) return { type: 'task', node: task };

    const attachment = this.state.attachments.find(a => a.id === nodeId);
    if (attachment) return { type: 'attachment', node: attachment };

    // Check if it's a scheduler node (search by canvas_node_id or id)
    const schedulerNode = this.state.schedulerNodes.find(s => s.canvas_node_id === nodeId || s.id === nodeId);
    if (schedulerNode) return { type: 'scheduler', node: schedulerNode };

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
          x: node.x * this.parent.scale + this.parent.offsetX,
          y: (node.y - halfHeight - 10) * this.parent.scale + this.parent.offsetY
        };
      }
      // Agents expose output port at bottom by default
      return {
        x: node.x * this.parent.scale + this.parent.offsetX,
        y: (node.y + halfHeight + 10) * this.parent.scale + this.parent.offsetY
      };
    } else if (type === 'task') {
      // Tasks have input ports at top and output port at bottom
      const taskHeight = node.cardBounds ? node.cardBounds.height : 80; // Fallback height if cardBounds not yet calculated
      const taskY = node.cardBounds ? node.cardBounds.y : node.y;

      if (portId === 'output') {
        // Output port at bottom center
        return {
          x: node.x * this.parent.scale + this.parent.offsetX,
          y: (taskY + taskHeight + 5) * this.parent.scale + this.parent.offsetY
        };
      } else if (portId && portId.startsWith('input-')) {
        // Input ports at top center (can support multiple input ports if needed)
        return {
          x: node.x * this.parent.scale + this.parent.offsetX,
          y: (taskY - 10) * this.parent.scale + this.parent.offsetY
        };
      }
      return null;
    } else if (type === 'attachment') {
      // Attachments expose only an output port at the bottom
      const cardHeight = node.cardBounds ? node.cardBounds.height : 70;
      const cardY = node.cardBounds ? node.cardBounds.y : node.y;
      return {
        x: node.x * this.parent.scale + this.parent.offsetX,
        y: (cardY + cardHeight + 5) * this.parent.scale + this.parent.offsetY
      };
    } else if (type === 'scheduler') {
      // Scheduler nodes expose only an output port at the bottom
      const cardHeight = node.cardBounds ? node.cardBounds.height : 90;
      const cardY = node.cardBounds ? node.cardBounds.y : node.y - cardHeight / 2;
      return {
        x: node.x * this.parent.scale + this.parent.offsetX,
        y: (cardY + cardHeight + 5) * this.parent.scale + this.parent.offsetY
      };
    }

    return null;
  }

  /**
   * Get port at a given canvas position (for click detection)
   */
  getPortAtPosition(x, y) {
    const portRadius = 14; // Click detection radius (larger for easier hits with triangles)

    // Check task output ports (NEW - for connecting tasks to combiners)
    for (const task of this.parent.tasks) {
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

      // Input port at top center of task card
      const inputPortY = task.cardBounds.y - 5;
      const inputDist = Math.sqrt((x - portX) ** 2 + (y - inputPortY) ** 2);
      if (inputDist <= portRadius) {
        return {
          nodeId: task.id,
          nodeType: 'task',
          portId: 'input-0',
          type: 'input'
        };
      }

      // If inside task card bounds, treat as an input port (for forgiving drops)
      if (x >= task.cardBounds.x && x <= task.cardBounds.x + task.cardBounds.width &&
          y >= task.cardBounds.y && y <= task.cardBounds.y + task.cardBounds.height) {
        return {
          nodeId: task.id,
          nodeType: 'task',
          portId: 'input-0',
          type: 'input'
        };
      }
    }

    // Check attachment output ports (bottom center)
    for (const att of this.parent.attachments) {
      if (att.x == null || att.y == null) continue;
      const portX = att.x;
      const portY = (att.cardBounds ? att.cardBounds.y + att.cardBounds.height : att.y + 35);
      const dist = Math.sqrt((x - portX) ** 2 + (y - portY) ** 2);
      if (dist <= portRadius) {
        return {
          nodeId: att.id,
          nodeType: 'attachment',
          portId: 'output',
          type: 'output'
        };
      }
    }

    // Check scheduler node output ports (bottom center)
    for (const scheduler of this.parent.schedulerNodes) {
      if (scheduler.x == null || scheduler.y == null) continue;
      const portX = scheduler.x;
      const portY = (scheduler.cardBounds ? scheduler.cardBounds.y + scheduler.cardBounds.height : scheduler.y + 45);
      const dist = Math.sqrt((x - portX) ** 2 + (y - portY) ** 2);
      if (dist <= portRadius) {
        return {
          nodeId: scheduler.canvas_node_id || scheduler.id,
          nodeType: 'scheduler',
          portId: 'output',
          type: 'output'
        };
      }
    }

    // Check agent output ports
    for (const agent of this.parent.agents) {
      const portX = agent.x;
      const halfHeight = (agent.height || 70) / 2;
      const portY = agent.y + halfHeight + 10;
      const dist = Math.sqrt((x - portX) ** 2 + (y - portY) ** 2);
      if (dist <= portRadius) {
        return {
          nodeId: agent.nodeId || agent.name,
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
          nodeId: agent.nodeId || agent.name,
          nodeType: 'agent',
          portId: 'input',
          type: 'input'
        };
      }
    }

    return null;
  }

  /**
   * Get connection near a given position (for click detection)
   */
  getConnectionAtPosition(x, y, threshold = 10) {
    for (const conn of this.parent.connections) {
      const fromPos = this.getPortPosition(conn.from, conn.fromPort);
      const toPos = this.getPortPosition(conn.to, conn.toPort);

      if (!fromPos || !toPos) continue;

      // Convert to canvas coordinates
      const fromX = (fromPos.x - this.parent.offsetX) / this.parent.scale;
      const fromY = (fromPos.y - this.parent.offsetY) / this.parent.scale;
      const toX = (toPos.x - this.parent.offsetX) / this.parent.scale;
      const toY = (toPos.y - this.parent.offsetY) / this.parent.scale;

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
   * Find the most recent task associated with an agent so combiners can treat
   * direct agent connections as inputs.
   */
  getLatestTaskForAgent(agentName) {
    if (!agentName || !this.state.tasks || this.state.tasks.length === 0) {
      return null;
    }

    const candidates = this.state.tasks.filter(task =>
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
}
