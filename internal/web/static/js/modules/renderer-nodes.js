/**
 * Renderer Nodes
 *
 * Handles rendering of all node types:
 * - Task cards/flows
 * - Agent nodes
 * - Combiner nodes
 */

export class RendererNodes {
  /**
   * @param {CanvasRenderingContext2D} ctx - Canvas 2D context
   * @param {AgentCanvasState} state - Shared state object
   * @param {HTMLCanvasElement} canvas - Canvas element
   * @param {AgentCanvas} parent - Parent AgentCanvas instance
   * @param {RendererPrimitives} primitives - Primitives renderer
   */
  constructor(ctx, state, canvas, parent, primitives) {
    this.ctx = ctx;
    this.state = state;
    this.canvas = canvas;
    this.parent = parent;
    this.primitives = primitives;
  }

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

}
