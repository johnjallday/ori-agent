/**
 * Agent Canvas Renderer
 *
 * Handles all canvas drawing operations for the Agent Canvas.
 * This module is responsible for rendering:
 * - Agents (nodes)
 * - Tasks (cards)
 * - Connections and flows
 * - UI panels and overlays
 * - Notifications and buttons
 * - Workflow combiners
 * - Timeline and context menus
 *
 * All rendering logic is centralized here to separate concerns from
 * the main AgentCanvas class.
 */

export class AgentCanvasRenderer {
  /**
   * Create a renderer instance
   * @param {CanvasRenderingContext2D} ctx - Canvas 2D context
   * @param {AgentCanvasState} state - State module with all canvas data
   * @param {HTMLCanvasElement} canvas - Canvas element reference
   * @param {AgentCanvas} parent - Parent AgentCanvas instance for helper methods
   */
  constructor(ctx, state, canvas, parent) {
    this.ctx = ctx;
    this.state = state;
    this.canvas = canvas;
    this.parent = parent;
  }

  // ==================== CONNECTION RENDERING ====================

  drawConnections() {
    this.ctx.strokeStyle = 'rgba(0,0,0,0.05)';
    this.ctx.lineWidth = 1;

    for (let i = 0; i < this.state.agents.length; i++) {
      for (let j = i + 1; j < this.state.agents.length; j++) {
        this.ctx.beginPath();
        this.ctx.moveTo(this.state.agents[i].x, this.state.agents[i].y);
        this.ctx.lineTo(this.state.agents[j].x, this.state.agents[j].y);
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

  /**
   * Draw connections from completed tasks to tasks that use their results
   */
  drawResultConnections() {
    if (!this.state.tasks || this.state.tasks.length === 0) return;

    this.state.tasks.forEach(task => {
      // Check if this task has input tasks
      if (!task.input_task_ids || task.input_task_ids.length === 0) return;

      // Draw connection from each input task to this task
      task.input_task_ids.forEach(inputTaskId => {
        const inputTask = this.state.tasks.find(t => t.id === inputTaskId);
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
    this.state.particles.forEach(p => {
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
    if (!this.state.activeChains || this.state.activeChains.length === 0) return;

    this.state.activeChains.forEach(chain => {
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
    if (!this.state.chainParticles || this.state.chainParticles.length === 0) return;

    this.state.chainParticles.forEach(p => {
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

  drawAssignmentLine() {
    // Draw line from task to cursor
    this.ctx.save();
    this.ctx.translate(this.state.offsetX, this.state.offsetY);
    this.ctx.scale(this.state.scale, this.state.scale);

    // Only draw when we have a source task
    if (this.state.assignmentSourceTask) {
      // Draw line
      this.ctx.strokeStyle = '#fd7e14';
      this.ctx.lineWidth = 3;
      this.ctx.setLineDash([10, 5]);
      this.ctx.beginPath();
      this.ctx.moveTo(this.state.assignmentSourceTask.x, this.state.assignmentSourceTask.y);
      this.ctx.lineTo(this.state.assignmentMouseX, this.state.assignmentMouseY);
      this.ctx.stroke();
      this.ctx.setLineDash([]);

      // Draw arrow at cursor
      const angle = Math.atan2(
        this.state.assignmentMouseY - this.state.assignmentSourceTask.y,
        this.state.assignmentMouseX - this.state.assignmentSourceTask.x
      );
      const arrowSize = 15;
      this.ctx.fillStyle = '#fd7e14';
      this.ctx.beginPath();
      this.ctx.moveTo(this.state.assignmentMouseX, this.state.assignmentMouseY);
      this.ctx.lineTo(
        this.state.assignmentMouseX - arrowSize * Math.cos(angle - Math.PI / 6),
        this.state.assignmentMouseY - arrowSize * Math.sin(angle - Math.PI / 6)
      );
      this.ctx.lineTo(
        this.state.assignmentMouseX - arrowSize * Math.cos(angle + Math.PI / 6),
        this.state.assignmentMouseY - arrowSize * Math.sin(angle + Math.PI / 6)
      );
      this.ctx.closePath();
      this.ctx.fill();
    }

    this.ctx.restore();
  }

  // ==================== HELPER METHODS ====================

  /**
   * Helper function to draw rounded rectangle
   */
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

  /**
   * Helper function to wrap text
   */
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
}

  // ==================== TASK & AGENT RENDERING ====================
  drawTaskFlows() {
    if (!this.state.tasks || this.state.tasks.length === 0) return;

    this.state.tasks.forEach((task, index) => {
      const fromAgent = this.state.agents.find(a => a.name === task.from);
      const toAgent = this.state.agents.find(a => a.name === task.to);

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
        const outputConn = this.state.connections.find(c => c.from === task.id);

        if (outputConn) {
          // Get the connected node (where the task output goes)
          const connectedNode = this.parent.getNodeById(outputConn.to);

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
      const outputConn = this.state.connections.find(c => c.from === task.id);
      const outputsToCombiner = outputConn ? this.parent.getNodeById(outputConn.to)?.type === 'combiner' : false;

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
        const isActiveAssignment = this.state.assignmentMode && this.state.assignmentSourceTask && this.state.assignmentSourceTask.id === task.id;
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
      const hasLogs = this.state.executionLogs[task.id] && this.state.executionLogs[task.id].length > 0;
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

  drawAgents() {
    this.state.agents.forEach(agent => {
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
    if (!this.state.workspaceProgress || this.state.workspaceProgress.total_tasks === 0) return;

    const panelWidth = Math.min(600, this.state.width * 0.8);
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
    let statusText = `${this.state.workspaceProgress.completed_tasks}/${this.state.workspaceProgress.total_tasks} tasks complete | ${this.state.workspaceProgress.in_progress_tasks} running | ${this.state.workspaceProgress.pending_tasks} pending`;
    if (this.state.workspaceProgress.failed_tasks > 0) {
      statusText += ` | ${this.state.workspaceProgress.failed_tasks} failed`;
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
    const fillWidth = (progressBarWidth * this.state.workspaceProgress.percentage) / 100;
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
    this.ctx.fillText(`${this.state.workspaceProgress.percentage}%`, panelX + padding + progressBarWidth / 2, progressBarY + progressBarHeight / 2);

    // Bottom row: Agent status and estimated time
    const bottomY = panelY + padding + 58;
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';

    // Agent status
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '10px system-ui';
    const agentText = `Agents: ${this.state.workspaceProgress.active_agents} active | ${this.state.workspaceProgress.idle_agents} idle`;
    this.ctx.fillText(agentText, panelX + padding, bottomY);

    // Estimated time remaining
    if (this.state.workspaceProgress.remaining_time_ms && this.state.workspaceProgress.remaining_time_ms > 0) {
      this.ctx.textAlign = 'right';
      const minutes = Math.ceil(this.state.workspaceProgress.remaining_time_ms / 60000);
      const seconds = Math.ceil((this.state.workspaceProgress.remaining_time_ms % 60000) / 1000);
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
    if (!this.state.mission) return;

    // Calculate center of canvas in world coordinates
    const centerX = this.state.width / 2;
    const centerY = this.state.height / 2;

    // Draw mission background box
    this.ctx.save();

    // Measure text to size the box appropriately
    this.ctx.font = 'bold 18px system-ui';
    const maxWidth = this.state.width * 0.6; // Max 60% of canvas width
    const lines = this.wrapText(this.state.mission, maxWidth);
    const lineHeight = 26;
    const totalHeight = lines.length * lineHeight + 30;
    const boxWidth = Math.min(maxWidth + 40, this.state.width * 0.7);
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

  drawExpandedTaskPanel() {
    if (!this.state.expandedTask) return;

    const panelX = this.state.width - this.state.expandedPanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;

    // Draw panel background with shadow
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.expandedPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.state.expandedPanelWidth < 100) {
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
    this.ctx.fillText('×', panelX + this.state.expandedPanelWidth - padding, currentY + 20);
    currentY += 40;

    // Task title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Task Details', contentX, currentY);
    currentY += 30;

    // Status badge
    let statusColor = '#6b7280';
    if (this.state.expandedTask.status === 'completed') statusColor = '#10b981';
    else if (this.state.expandedTask.status === 'in_progress') statusColor = '#3b82f6';
    else if (this.state.expandedTask.status === 'failed') statusColor = '#ef4444';
    else if (this.state.expandedTask.status === 'pending') statusColor = '#f59e0b';

    this.ctx.fillStyle = statusColor;
    this.ctx.font = 'bold 10px system-ui';
    const statusText = (this.state.expandedTask.status || 'pending').toUpperCase();
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
    const descLines = this.wrapText(this.state.expandedTask.description || '', this.state.expandedPanelWidth - padding * 2);
    descLines.forEach(line => {
      this.ctx.fillText(line, contentX, currentY);
      currentY += 18;
    });
    currentY += 15;

    // Agents
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText(`From: ${this.state.expandedTask.from}  →  To: ${this.state.expandedTask.to}`, contentX, currentY);
    currentY += 25;

    // Progress section (for in_progress tasks)
    if (this.state.expandedTask.status === 'in_progress') {
      this.ctx.fillStyle = '#3b82f6';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('⏳ Progress', contentX, currentY);
      currentY += 25;

      // Calculate elapsed time
      let elapsedMs = 0;
      if (this.state.expandedTask.started_at) {
        elapsedMs = Date.now() - new Date(this.state.expandedTask.started_at).getTime();
      }

      // Progress box
      const progressBoxHeight = 100;
      this.ctx.fillStyle = '#eff6ff';
      this.ctx.strokeStyle = '#3b82f6';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, currentY, this.state.expandedPanelWidth - padding * 2, progressBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      let progressY = currentY + 20;

      // Percentage or indeterminate
      const hasProgress = this.state.expandedTask.progress && this.state.expandedTask.progress.percentage !== undefined;
      if (hasProgress) {
        const percentage = this.state.expandedTask.progress.percentage;

        // Progress bar
        const barWidth = this.state.expandedPanelWidth - padding * 2 - 40;
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
        if (this.state.expandedTask.progress.current_step) {
          this.ctx.fillStyle = '#1e3a8a';
          this.ctx.font = '11px system-ui';
          const stepLines = this.wrapText(this.state.expandedTask.progress.current_step, this.state.expandedPanelWidth - padding * 2 - 40);
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
    this.ctx.lineTo(panelX + this.state.expandedPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Result section
    if (this.state.expandedTask.result) {
      this.ctx.fillStyle = '#059669';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📊 Result', contentX, currentY);

      // Copy button
      const copyButtonWidth = 80;
      const copyButtonHeight = 24;
      const copyButtonX = panelX + this.state.expandedPanelWidth - padding - copyButtonWidth;
      const copyButtonY = currentY - 18;

      // Store bounds for click detection
      this.state.copyButtonBounds = {
        x: copyButtonX,
        y: copyButtonY,
        width: copyButtonWidth,
        height: copyButtonHeight
      };

      // Button background
      if (this.state.copyButtonState === 'copied') {
        this.ctx.fillStyle = '#10b981';
      } else if (this.state.copyButtonState === 'hover') {
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
      const buttonText = this.state.copyButtonState === 'copied' ? '✓ Copied!' : '📋 Copy';
      this.ctx.fillText(buttonText, copyButtonX + copyButtonWidth / 2, copyButtonY + copyButtonHeight / 2 + 4);
      this.ctx.textAlign = 'left';

      currentY += 25;

      // Result background box
      const resultBoxY = currentY;
      const resultBoxHeight = Math.min(300, panelHeight - currentY - padding);
      const resultBoxWidth = this.state.expandedPanelWidth - padding * 2;

      // Store bounds for scroll detection
      this.state.resultBoxBounds = {
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
      const resultLines = this.wrapText(this.state.expandedTask.result, resultBoxWidth - 40); // Extra padding for scrollbar
      const lineHeight = 14;
      const visibleLines = Math.floor((resultBoxHeight - 20) / lineHeight);
      const totalLines = resultLines.length;

      // Clamp scroll offset
      const maxScroll = Math.max(0, totalLines - visibleLines);
      this.state.resultScrollOffset = Math.max(0, Math.min(this.state.resultScrollOffset, maxScroll));

      // Enable clipping to prevent text overflow
      this.ctx.save();
      this.ctx.beginPath();
      this.ctx.rect(contentX + 5, resultBoxY + 5, resultBoxWidth - 10, resultBoxHeight - 10);
      this.ctx.clip();

      // Render visible lines based on scroll offset
      const startLine = Math.floor(this.state.resultScrollOffset);
      const endLine = Math.min(startLine + visibleLines + 1, totalLines);

      resultLines.slice(startLine, endLine).forEach((line, i) => {
        const yPos = resultBoxY + 15 + (i * lineHeight) - ((this.state.resultScrollOffset - startLine) * lineHeight);
        this.ctx.fillText(line, contentX + 10, yPos);
      });

      this.ctx.restore();

      // Draw scrollbar if content is scrollable
      if (totalLines > visibleLines) {
        const scrollbarWidth = 8;
        const scrollbarHeight = (visibleLines / totalLines) * (resultBoxHeight - 20);
        const scrollbarY = resultBoxY + 10 + (this.state.resultScrollOffset / maxScroll) * (resultBoxHeight - 20 - scrollbarHeight);

        // Scrollbar track
        this.ctx.fillStyle = 'rgba(16, 185, 129, 0.1)';
        this.ctx.fillRect(contentX + resultBoxWidth - scrollbarWidth - 5, resultBoxY + 10, scrollbarWidth, resultBoxHeight - 20);

        // Scrollbar thumb
        this.ctx.fillStyle = 'rgba(16, 185, 129, 0.5)';
        this.ctx.fillRect(contentX + resultBoxWidth - scrollbarWidth - 5, scrollbarY, scrollbarWidth, scrollbarHeight);
      }
    } else if (this.state.expandedTask.error) {
      this.state.resultBoxBounds = null;
      this.state.copyButtonBounds = null;
      this.ctx.fillStyle = '#dc2626';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('❌ Error', contentX, currentY);
      currentY += 25;

      this.ctx.fillStyle = '#7f1d1d';
      this.ctx.font = '11px monospace';
      const errorLines = this.wrapText(this.state.expandedTask.error, this.state.expandedPanelWidth - padding * 2);
      errorLines.slice(0, 10).forEach(line => {
        this.ctx.fillText(line, contentX, currentY);
        currentY += 14;
      });
    } else {
      this.state.resultBoxBounds = null;
      this.state.copyButtonBounds = null;
      this.ctx.fillStyle = '#9ca3af';
      this.ctx.font = 'italic 12px system-ui';
      this.ctx.fillText('No result yet', contentX, currentY);
    }

    // Connection-to-agent flow removed; keep bounds null
    this.state.connectButtonBounds = null;

    this.ctx.restore();
  }

  drawExpandedAgentPanel() {
    if (!this.state.expandedAgent) return;

    const panelX = this.state.width - this.state.expandedAgentPanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;

    // Draw panel background with shadow
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.expandedAgentPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.state.expandedAgentPanelWidth < 100) {
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
    this.ctx.fillText('×', panelX + this.state.expandedAgentPanelWidth - padding, currentY + 20);
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
    this.ctx.rect(panelX, scrollableStartY, this.state.expandedAgentPanelWidth, scrollableHeight);
    this.ctx.clip();

    // Apply scroll offset
    currentY -= this.state.agentPanelScrollOffset;

    // Status badge
    let statusColor = '#6b7280';
    if (this.state.expandedAgent.status === 'active') statusColor = '#10b981';
    else if (this.state.expandedAgent.status === 'busy') statusColor = '#f59e0b';

    this.ctx.fillStyle = statusColor;
    this.ctx.font = 'bold 10px system-ui';
    const statusText = (this.state.expandedAgent.status || 'idle').toUpperCase();
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
    this.ctx.fillText(this.state.expandedAgent.name, contentX, currentY);
    currentY += 25;

    // Agent color indicator
    this.ctx.fillStyle = this.state.expandedAgent.color;
    this.roundRect(contentX, currentY, 30, 30, 15);
    this.ctx.fill();
    currentY += 40;

    // Last Result section (if available)
    if (this.state.expandedAgent.lastResult) {
      this.ctx.fillStyle = '#10b981'; // Green
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📊 Last Result', contentX, currentY);
      currentY += 20;

      // Result box
      const resultBoxWidth = this.state.expandedAgentPanelWidth - padding * 2;
      const resultText = this.state.expandedAgent.lastResult.toString();

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
      { label: 'Current Tasks', value: this.state.expandedAgent.currentTasks?.length || 0, color: '#10b981' },
      { label: 'Queued Tasks', value: this.state.expandedAgent.queuedTasks?.length || 0, color: '#3b82f6' },
      { label: 'Completed', value: this.state.expandedAgent.completedTasks || 0, color: '#6b7280' },
      { label: 'Failed', value: this.state.expandedAgent.failedTasks || 0, color: '#ef4444' },
    ];

    stats.forEach((stat, index) => {
      // Stat box
      const statBoxWidth = (this.state.expandedAgentPanelWidth - padding * 2 - 10) / 2;
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
    const totalExec = this.state.expandedAgent.totalExecutions || 0;
    this.ctx.fillText(`Total Executions: ${totalExec}`, contentX, currentY);
    currentY += 25;

    // Separator line
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.state.expandedAgentPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Enabled Tools section
    if (this.state.expandedAgent.config && this.state.expandedAgent.config.enabled_plugins) {
      this.ctx.fillStyle = '#7c3aed';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('🔧 Enabled Tools', contentX, currentY);
      currentY += 20;

      const plugins = this.state.expandedAgent.config.enabled_plugins;
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
      this.ctx.lineTo(panelX + this.state.expandedAgentPanelWidth - padding, currentY);
      this.ctx.stroke();
      currentY += 20;
    }

    // System Prompt section
    if (this.state.expandedAgent.config && this.state.expandedAgent.config.system_prompt) {
      this.ctx.fillStyle = '#ea580c';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('💬 System Prompt', contentX, currentY);
      currentY += 20;

      // System prompt box
      const promptBoxY = currentY;

      // Calculate height based on actual content (now showing ALL lines)
      this.ctx.fillStyle = '#7c2d12';
      this.ctx.font = '10px system-ui';
      const promptLines = this.wrapText(this.state.expandedAgent.config.system_prompt, this.state.expandedAgentPanelWidth - padding * 2 - 20);
      const lineHeight = 13;
      const promptBoxHeight = Math.max(60, 15 + (promptLines.length * lineHeight) + 15); // top padding + lines + bottom padding

      // Draw box
      this.ctx.fillStyle = '#fff7ed';
      this.ctx.strokeStyle = '#ea580c';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, promptBoxY, this.state.expandedAgentPanelWidth - padding * 2, promptBoxHeight, 6);
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
      this.ctx.lineTo(panelX + this.state.expandedAgentPanelWidth - padding, currentY);
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
    const taskCount = this.state.expandedAgent.tasks ? this.state.expandedAgent.tasks.length : 0;
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
      const tasksToShow = this.state.expandedAgent.tasks.slice(0, maxTasksToShow);

      tasksToShow.forEach((taskId, index) => {
        // Find the task details
        const task = this.state.tasks.find(t => t.id === taskId);
        if (task) {
          // Task background
          const taskBoxY = currentY;
          const taskBoxHeight = 45;
          this.ctx.fillStyle = '#f0fdf4';
          this.ctx.strokeStyle = '#10b981';
          this.ctx.lineWidth = 1;
          this.roundRect(contentX, taskBoxY, this.state.expandedAgentPanelWidth - padding * 2, taskBoxHeight, 6);
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

      if (this.state.expandedAgent.tasks.length > maxTasksToShow) {
        this.ctx.fillStyle = '#6b7280';
        this.ctx.font = 'italic 10px system-ui';
        this.ctx.fillText(`... and ${this.state.expandedAgent.tasks.length - maxTasksToShow} more`, contentX, currentY + 5);
        currentY += 20;
      }
    }

    // Calculate total content height
    // Note: currentY has scroll offset applied (subtracted), so add it back to get actual unscrolled content height
    const totalContentHeight = currentY + this.state.agentPanelScrollOffset - scrollableStartY + 20; // +20 for bottom padding

    // Restore clipping context
    this.ctx.restore();

    // Calculate scroll parameters
    const maxScroll = Math.max(0, totalContentHeight - scrollableHeight);
    this.state.agentPanelMaxScroll = maxScroll; // Store for wheel event handler

    // Clamp scroll offset
    this.state.agentPanelScrollOffset = Math.max(0, Math.min(this.state.agentPanelScrollOffset, maxScroll));

    // Draw scrollbar if content is scrollable
    if (maxScroll > 0) {
      const scrollbarWidth = 6;
      const scrollbarX = panelX + this.state.expandedAgentPanelWidth - padding / 2 - scrollbarWidth;
      const scrollbarHeight = Math.max(30, (scrollableHeight / totalContentHeight) * scrollableHeight);
      const scrollbarY = scrollableStartY + (this.state.agentPanelScrollOffset / maxScroll) * (scrollableHeight - scrollbarHeight);

      this.ctx.fillStyle = 'rgba(0, 0, 0, 0.2)';
      this.roundRect(scrollbarX, scrollbarY, scrollbarWidth, scrollbarHeight, 3);
      this.ctx.fill();
    }

    this.ctx.restore();
  }

  drawExpandedCombinerPanel() {
    if (!this.state.expandedCombiner) return;

    const panelX = this.state.width - this.state.expandedCombinerPanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;
    const padding = 20;

    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.25)';
    this.ctx.shadowBlur = 18;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.expandedCombinerPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    if (this.state.expandedCombinerPanelWidth < 80) {
      this.ctx.restore();
      return;
    }

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.state.expandedCombinerPanelWidth - padding, padding + 20);

    // Title
    let currentY = padding + 50;
    this.ctx.textAlign = 'left';
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.fillText(`${this.state.expandedCombiner.name} Node`, panelX + padding, currentY);
    currentY += 26;

    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText(`Mode: ${this.state.expandedCombiner.resultCombinationMode || 'merge'}`, panelX + padding, currentY);
    currentY += 22;

    // Inputs section
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.fillText('Inputs', panelX + padding, currentY);
    currentY += 18;

    const inputConnections = this.state.connections.filter(c => c.to === this.state.expandedCombiner.id);
    if (inputConnections.length === 0) {
      this.ctx.fillStyle = '#9ca3af';
      this.ctx.font = '12px system-ui';
      this.ctx.fillText('No inputs connected', panelX + padding, currentY);
      currentY += 22;
    } else {
      inputConnections.forEach(conn => {
        const source = this.parent.getNodeById(conn.from);
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
    this.ctx.lineTo(panelX + this.state.expandedCombinerPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Combined result
    this.ctx.fillStyle = '#111827';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.fillText('Combined Output', panelX + padding, currentY);
    currentY += 18;

    const combinedText = this.parent.buildCombinerResultPreview(this.state.expandedCombiner);
    const textLines = this.wrapText(combinedText || 'No results yet', this.state.expandedCombinerPanelWidth - padding * 2);

    this.ctx.fillStyle = combinedText ? '#111827' : '#9ca3af';
    this.ctx.font = '12px system-ui';
    textLines.slice(0, 12).forEach(line => {
      this.ctx.fillText(line, panelX + padding, currentY);
      currentY += 16;
    });

    this.ctx.restore();
  }

  drawCreateTaskButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.state.width - buttonWidth - 20;
    const buttonY = 20;

    // Store button bounds for click detection
    this.state.createTaskButtonBounds = {
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

  drawAddAgentButton() {
    const buttonWidth = 130;
    const buttonHeight = 40;
    const buttonX = this.state.width - 140 - 20 - buttonWidth - 10; // Left of Create Task button
    const buttonY = 20;

    // Store button bounds for click detection
    this.state.addAgentButtonBounds = {
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

  drawNotifications() {
    if (!this.state.notifications || this.state.notifications.length === 0) return;

    const notificationWidth = 320;
    const notificationHeight = 70;
    const padding = 15;
    const spacing = 10;

    this.ctx.save();

    this.state.notifications.forEach((notification, index) => {
      const x = this.state.width - notificationWidth - 20;
      const y = this.state.height - (notificationHeight + spacing) * (index + 1) - 80;

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

  drawTimelineToggleButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.state.width - buttonWidth - 20;
    const buttonY = 70; // Below create task button

    // Store button bounds for click detection
    this.state.timelineToggleBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Button background - different color if timeline is open
    this.ctx.fillStyle = this.state.timelineVisible ? '#059669' : '#6b7280';
    this.ctx.strokeStyle = this.state.timelineVisible ? '#047857' : '#4b5563';
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
    const text = this.state.timelineVisible ? '📋 Hide Timeline' : '📋 Timeline';
    this.ctx.fillText(text, buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  drawAutoLayoutButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.state.width - buttonWidth - 20;
    const buttonY = 120; // Below timeline button

    // Store button bounds for click detection
    this.state.autoLayoutButtonBounds = {
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

  drawSaveLayoutButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.state.width - buttonWidth - 20;
    const buttonY = 170; // Below auto-layout button

    // Store button bounds for click detection
    this.state.saveLayoutButtonBounds = {
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

  drawTimelinePanel() {
    if (!this.state.timelineEvents || this.state.timelineEvents.length === 0) {
      // Show empty state
      this.drawEmptyTimeline();
      return;
    }

    const panelX = this.state.width - this.state.timelinePanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.timelinePanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.state.timelinePanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.state.timelinePanelWidth - padding, currentY + 20);
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
    this.ctx.fillText(`${this.state.timelineEvents.length} recent events`, contentX, currentY);
    currentY += 25;

    // Separator
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.state.timelinePanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 15;

    // Draw events
    const maxVisibleEvents = Math.floor((panelHeight - currentY - 20) / 70);
    const visibleEvents = this.state.timelineEvents.slice(0, maxVisibleEvents);

    visibleEvents.forEach((event, index) => {
      this.drawTimelineEvent(event, contentX, currentY, this.state.timelinePanelWidth - padding * 2);
      currentY += 70;
    });

    this.ctx.restore();
  }

  drawEmptyTimeline() {
    const panelX = this.state.width - this.state.timelinePanelWidth;
    const panelY = 0;
    const panelHeight = this.state.height;
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.state.timelinePanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    if (this.state.timelinePanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.state.timelinePanelWidth - padding, currentY + 20);
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
    this.ctx.fillText('No activity yet', panelX + this.state.timelinePanelWidth / 2, currentY);
    this.ctx.fillText('Events will appear here', panelX + this.state.timelinePanelWidth / 2, currentY + 20);

    this.ctx.restore();
  }

  drawTimelineEvent(event, x, y, width) {
    const icon = this.parent.getEventIcon(event.type);
    const message = this.parent.getEventMessage(event);
    const time = new Date(event.timestamp).toLocaleTimeString();

    // Icon
    this.ctx.font = '18px system-ui';
    this.ctx.fillStyle = this.parent.getEventColor(event.type);
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

  drawContextMenu() {
    if (!this.state.contextMenuAgent) return;

    const menuWidth = 200;
    const menuHeight = 140;
    const padding = 10;
    const itemHeight = 35;

    // Position menu (ensure it stays within canvas bounds)
    let x = this.state.contextMenuX;
    let y = this.state.contextMenuY;
    if (x + menuWidth > this.state.width) x = this.state.width - menuWidth - 10;
    if (y + menuHeight > this.state.height) y = this.state.height - menuHeight - 10;

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
    this.ctx.fillText(this.state.contextMenuAgent.name, x + padding, y + padding + 12);
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
      const mouseX = this.state.lastMouseX || 0;
      const mouseY = this.state.lastMouseY || 0;
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
      if (!this.state.contextMenuItems) this.state.contextMenuItems = [];
      this.state.contextMenuItems[i] = {
        x, y: itemY, width: menuWidth, height: itemHeight,
        action: item.action, agent: this.state.contextMenuAgent
      };
    });
    this.ctx.restore();
  }

  drawHelpOverlay() {
    const overlayWidth = 400;
    const overlayHeight = 450;
    const x = (this.state.width - overlayWidth) / 2;
    const y = (this.state.height - overlayHeight) / 2;
    const padding = 20;

    // Draw semi-transparent backdrop
    this.ctx.save();
    this.ctx.fillStyle = 'rgba(0, 0, 0, 0.5)';
    this.ctx.fillRect(0, 0, this.state.width, this.state.height);
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

  drawWorkflowConnections() {
    // Get mouse position in canvas coordinates for hover detection
    const rect = this.canvas.getBoundingClientRect();
    const mouseCanvasX = this.state.lastMouseX ? (this.state.lastMouseX - this.state.offsetX) / this.state.scale : -9999;
    const mouseCanvasY = this.state.lastMouseY ? (this.state.lastMouseY - this.state.offsetY) / this.state.scale : -9999;

    this.state.connections.forEach(conn => {
      const fromPos = this.parent.getPortPosition(conn.from, conn.fromPort);
      const toPos = this.parent.getPortPosition(conn.to, conn.toPort);

      if (!fromPos || !toPos) return;

      // Convert back to canvas coordinates
      const fromX = (fromPos.x - this.state.offsetX) / this.state.scale;
      const fromY = (fromPos.y - this.state.offsetY) / this.state.scale;
      const toX = (toPos.x - this.state.offsetX) / this.state.scale;
      const toY = (toPos.y - this.state.offsetY) / this.state.scale;

      // Check if mouse is hovering over this connection
      const hoveredConn = this.parent.getConnectionAtPosition(mouseCanvasX, mouseCanvasY, 15);
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

  drawCombinerNodes() {
    this.state.combinerNodes.forEach(node => {
      this.ctx.save();

      // Draw diamond/rectangle shape
      const x = node.x;
      const y = node.y;
      const w = node.width;
      const h = node.height;

      // Background with gradient
      const gradient = this.ctx.createLinearGradient(x, y, x, y + h);
      gradient.addColorStop(0, node.color);
      gradient.addColorStop(1, this.parent.lightenColor(node.color, 20));

      this.ctx.fillStyle = gradient;
      this.ctx.strokeStyle = this.parent.darkenColor(node.color, 20);
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

  drawDraggingConnection() {
    if (!this.state.connectionDragStart) return;

    const fromPos = this.parent.getPortPosition(
      this.state.connectionDragStart.nodeId,
      this.state.connectionDragStart.portId
    );

    if (!fromPos) return;

    const fromX = (fromPos.x - this.state.offsetX) / this.state.scale;
    const fromY = (fromPos.y - this.state.offsetY) / this.state.scale;

    // Mouse position in canvas coordinates
    const rect = this.canvas.getBoundingClientRect();
    const mouseX = (this.state.lastMouseX - this.state.offsetX) / this.state.scale;
    const mouseY = (this.state.lastMouseY - this.state.offsetY) / this.state.scale;

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
