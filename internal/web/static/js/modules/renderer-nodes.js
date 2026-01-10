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

  /**
   * Draw selection highlight around a node
   * @param {number} x - X position of node
   * @param {number} y - Y position of node
   * @param {number} width - Width of node
   * @param {number} height - Height of node
   * @param {number} radius - Corner radius (default 8)
   * @param {boolean} isPrimary - Whether this is the first/primary selected node
   */
  drawSelectionHighlight(x, y, width, height, radius = 8, isPrimary = false) {
    const padding = 4;
    const borderWidth = isPrimary ? 4 : 3;

    this.ctx.save();
    // Primary selection: bright cyan/teal, Secondary: indigo
    this.ctx.strokeStyle = isPrimary ? '#06b6d4' : '#4f46e5'; // Cyan-500 vs Indigo-600
    this.ctx.lineWidth = borderWidth;
    this.ctx.shadowColor = isPrimary ? 'rgba(6, 182, 212, 0.5)' : 'rgba(79, 70, 229, 0.4)';
    this.ctx.shadowBlur = isPrimary ? 12 : 8;
    this.ctx.shadowOffsetX = 0;
    this.ctx.shadowOffsetY = 0;

    this.primitives.roundRect(
      x - padding,
      y - padding,
      width + padding * 2,
      height + padding * 2,
      radius + 2
    );
    this.ctx.stroke();
    this.ctx.restore();
  }

  drawTaskFlows() {
    if (!this.state.tasks || this.state.tasks.length === 0) return;

    this.state.tasks.forEach((task, index) => {
      const fromCandidates = this.state.agents.filter(a => a.name === task.from);
      const toCandidates = this.state.agents.filter(a => a.name === task.to);

      const fromAgent = fromCandidates[0];

      // Resolve target agent instance (prefer assigned_node_id, then assignedNodeId, then first matching agent)
      // IMPORTANT: Never use proximity - always use the assigned agent to avoid arrow snapping to wrong agent
      let toAgent = null;
      const assignedNodeId = task.assigned_node_id || task.assignedNodeId;
      if (assignedNodeId && toCandidates.length) {
        toAgent = toCandidates.find(a => a.nodeId === assignedNodeId || a.id === assignedNodeId) || null;
      }
      // Fallback: if assigned_node_id didn't match OR wasn't specified, use first agent with matching name
      // Do NOT use proximity - that causes the arrow to snap to wrong agents when dragging
      if (!toAgent && toCandidates.length) {
        toAgent = toCandidates[0];
      }

      // Handle unassigned tasks (to: "unassigned" or empty string)
      const isUnassigned = task.to === 'unassigned' || task.to === '' || !task.to;

      // If the task points to an agent that no longer exists on the canvas, treat it like unassigned
      // so it remains visible and can be reassigned.
      const isOrphaned = !toAgent && !isUnassigned;
      const treatAsUnassigned = isUnassigned || isOrphaned;

      // Handle system/user-created tasks (no from agent or empty from field)
      const isSystemTask = !fromAgent || task.from === 'system' || task.from === 'user' || task.from === '' || !task.from;

      // Calculate default position if task doesn't have one
      if (task.x == null || task.y == null) {  // Use == to catch both null and undefined
        // Visible center of current viewport in canvas coordinates
        const viewCenterX = (this.parent.width / 2 - this.state.offsetX) / this.state.scale;
        const viewCenterY = (this.parent.height / 2 - this.state.offsetY) / this.state.scale;

        if (treatAsUnassigned) {
            // Position unassigned tasks near the current viewport center
          const offsetX = (index % 2 === 0 ? -80 : 80);
          const offsetY = (Math.floor(index / 2) % 3 - 1) * 60;
          task.x = viewCenterX + offsetX;
          task.y = viewCenterY + offsetY;
        } else if (isSystemTask) {
          // Position near the target agent, but if off-screen, fall back toward view center
          const offsetX = 100 + (index % 3) * 50;
          const offsetY = -100 + (Math.floor(index / 3) % 3) * 70;
          const candidateX = toAgent ? toAgent.x + offsetX : viewCenterX;
          const candidateY = toAgent ? toAgent.y + offsetY : viewCenterY;
          task.x = candidateX;
          task.y = candidateY;
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
        this.primitives.drawArrow(x1, y1, x2, y2, color, 3);
        this.ctx.setLineDash([]);
      }

      // Draw connection line from task to receiver (skip for unassigned/orphaned tasks)
      if (toAgent && !treatAsUnassigned) {
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
        this.primitives.drawArrow(x1, y1, x2, y2, color, 4);
        this.ctx.setLineDash([]);
      }

      // Draw task card
      const cardWidth = 160;
      const cardHeight = 60;
      const cardX = task.x - cardWidth / 2;
      const cardY = task.y - cardHeight / 2;

      // Store card bounds for hit testing and port position calculations
      task.cardBounds = { x: cardX, y: cardY, width: cardWidth, height: cardHeight };
      // Also keep 'bounds' for backward compatibility
      task.bounds = task.cardBounds;

      // Draw selection highlight if this task is selected
      if (this.state.isNodeSelected(task.id)) {
        const isPrimary = this.state.isFirstSelected(task.id);
        this.drawSelectionHighlight(cardX, cardY, cardWidth, cardHeight, 6, isPrimary);
      }

      // Card background
      this.ctx.save();
      this.ctx.fillStyle = '#ffffff';
      this.ctx.shadowColor = 'rgba(0,0,0,0.15)';
      this.ctx.shadowBlur = 10;
      this.ctx.shadowOffsetY = 2;
      this.primitives.roundRect(cardX, cardY, cardWidth, cardHeight, 6);
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
      this.primitives.roundRect(cardX, cardY, cardWidth, cardHeight, 6);
      this.ctx.stroke();

      // Task description - manually truncate with ellipsis to prevent overflow
      this.ctx.fillStyle = '#212529';
      this.ctx.font = 'bold 11px system-ui';
      const maxTextWidth = cardWidth - 32; // Reserve space for padding and delete button
      let description = task.description || 'Task';

      // Manually truncate text if it exceeds maxWidth
      let textWidth = this.ctx.measureText(description).width;
      if (textWidth > maxTextWidth) {
        while (textWidth > maxTextWidth && description.length > 3) {
          description = description.substring(0, description.length - 1);
          textWidth = this.ctx.measureText(description + '...').width;
        }
        description = description + '...';
      }

      this.ctx.save();
      this.ctx.fillText(description, cardX + 8, cardY + 18);
      this.ctx.restore();

      // Task status - show connected node if unassigned, otherwise show from → to
      this.ctx.fillStyle = '#6c757d';
      this.ctx.font = '9px system-ui';

      let statusText;
      // Check if this is a simple manual task (no from AND no to)
      const isSimpleTask = isSystemTask && isUnassigned;

      if (treatAsUnassigned) {
        // Find output connection from this task (task is the source)
        const outputConn = this.state.connections.find(c => c.from === task.id);

        if (outputConn) {
          // Get the connected node (where the task output goes)
          const connectedNode = this.parent.getNodeById(outputConn.to);

          if (connectedNode) {
            const nodeName = connectedNode.node.name || connectedNode.node.id || 'Unknown';
            statusText = `→ ${nodeName}`;
          } else if (isSimpleTask) {
            statusText = '📝 Manual task';
          } else {
            statusText = isOrphaned ? `⚠️ MISSING: ${task.to}` : '⚠️ UNASSIGNED';
          }
        } else if (isSimpleTask) {
          statusText = '📝 Manual task';
        } else {
          statusText = isOrphaned ? `⚠️ MISSING: ${task.to}` : '⚠️ UNASSIGNED';
        }
      } else {
        // Find the assigned agent node to show instance number
        let toDisplay = task.to;
        if (task.to && this.state.agents) {
          const assignedNodeId = task.assigned_node_id || task.assignedNodeId;
          let agentNode = null;

          // First, try to find by nodeId
          if (assignedNodeId) {
            agentNode = this.state.agents.find(a => a.nodeId === assignedNodeId);
          }

          // If no nodeId or not found, try to match by agent name
          if (!agentNode) {
            const matchingAgents = this.state.agents.filter(a => a.name === task.to);
            if (matchingAgents.length > 0) {
              // Use the first matching agent (sorted by instance number)
              matchingAgents.sort((a, b) => (a.instanceNumber || 0) - (b.instanceNumber || 0));
              agentNode = matchingAgents[0];
            }
          }

          // If we found an agent node with instance number, use it
          if (agentNode && agentNode.instanceNumber) {
            toDisplay = `${agentNode.name} #${agentNode.instanceNumber}`;
          }
        }

        statusText = `${task.from} → ${toDisplay}`;
      }

      const maxStatusWidth = cardWidth - 16;

      // Manually truncate status text if it exceeds maxWidth
      let statusTextWidth = this.ctx.measureText(statusText).width;
      if (statusTextWidth > maxStatusWidth) {
        while (statusTextWidth > maxStatusWidth && statusText.length > 3) {
          statusText = statusText.substring(0, statusText.length - 1);
          statusTextWidth = this.ctx.measureText(statusText + '...').width;
        }
        statusText = statusText + '...';
      }

      this.ctx.save();
      this.ctx.fillText(statusText, cardX + 8, cardY + 34);
      this.ctx.restore();

      // Status badge (left aligned)
      this.ctx.fillStyle = borderColor;
      this.ctx.font = 'bold 8px system-ui';
      const badge = (task.status || 'pending').toUpperCase();
      const badgeWidth = this.ctx.measureText(badge).width + 8;
      this.ctx.fillRect(cardX + 8, cardY + 40, badgeWidth, 12);
      this.ctx.fillStyle = '#ffffff';
      this.ctx.fillText(badge, cardX + 12, cardY + 49);

      // Schedule indicator (show clock icon if task has a schedule)
      if (task.schedule && task.schedule_enabled) {
        const scheduleIconX = cardX + badgeWidth + 14;
        const scheduleIconY = cardY + 46;
        const iconRadius = 5;

        // Clock circle
        this.ctx.strokeStyle = '#8b5cf6'; // Purple for scheduled
        this.ctx.lineWidth = 1.5;
        this.ctx.beginPath();
        this.ctx.arc(scheduleIconX, scheduleIconY, iconRadius, 0, Math.PI * 2);
        this.ctx.stroke();

        // Clock hands
        this.ctx.beginPath();
        this.ctx.moveTo(scheduleIconX, scheduleIconY);
        this.ctx.lineTo(scheduleIconX, scheduleIconY - 3);
        this.ctx.stroke();
        this.ctx.beginPath();
        this.ctx.moveTo(scheduleIconX, scheduleIconY);
        this.ctx.lineTo(scheduleIconX + 2, scheduleIconY + 1);
        this.ctx.stroke();

        // Next run indicator (small text)
        if (task.next_run) {
          const nextRun = new Date(task.next_run);
          const now = new Date();
          const diff = nextRun - now;
          let timeText;
          if (diff < 0) {
            timeText = 'Due';
          } else if (diff < 60000) {
            timeText = `${Math.floor(diff / 1000)}s`;
          } else if (diff < 3600000) {
            timeText = `${Math.floor(diff / 60000)}m`;
          } else if (diff < 86400000) {
            timeText = `${Math.floor(diff / 3600000)}h`;
          } else {
            timeText = `${Math.floor(diff / 86400000)}d`;
          }
          this.ctx.fillStyle = '#8b5cf6';
          this.ctx.font = '7px system-ui';
          this.ctx.fillText(timeText, scheduleIconX + iconRadius + 2, scheduleIconY + 2);
        }
      }

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

      // Skip action controls when task is unassigned to avoid clutter
      const showActions = !treatAsUnassigned;

      // Execute button for pending tasks (hide if outputs to combiner)
      if (showActions && task.status === 'pending' && !outputsToCombiner) {
        const btnWidth = 50;
        const btnHeight = 14;
        const btnX = cardX + cardWidth - btnWidth - 6;
        const btnY = cardY + 40;

        // Store button bounds for click detection
        task.executeBtnBounds = { x: btnX, y: btnY, width: btnWidth, height: btnHeight };

        // Button background
        this.ctx.fillStyle = '#28a745';
        this.primitives.roundRect(btnX, btnY, btnWidth, btnHeight, 3);
        this.ctx.fill();

        // Button text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 8px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText('▶ RUN', btnX + btnWidth / 2, btnY + 10);
        this.ctx.textAlign = 'left';
      }

      // Rerun button for completed or failed tasks
      if (showActions && (task.status === 'completed' || task.status === 'failed')) {
        const rerunBtnWidth = 50;
        const rerunBtnHeight = 14;
        const rerunBtnX = cardX + cardWidth - rerunBtnWidth - 6;
        const rerunBtnY = cardY + 40;

        // Store button bounds for click detection
        task.rerunBtnBounds = { x: rerunBtnX, y: rerunBtnY, width: rerunBtnWidth, height: rerunBtnHeight };

        // Button background (orange for rerun)
        this.ctx.fillStyle = task.status === 'failed' ? '#dc3545' : '#fd7e14';
        this.primitives.roundRect(rerunBtnX, rerunBtnY, rerunBtnWidth, rerunBtnHeight, 3);
        this.ctx.fill();

        // Button text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 8px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText('↻ RERUN', rerunBtnX + rerunBtnWidth / 2, rerunBtnY + 10);
        this.ctx.textAlign = 'left';
      }

      // Assign button (show for unassigned tasks even if completed)
      const isUnassignedTask = treatAsUnassigned;
      const showAssignButton = isUnassignedTask || task.status !== 'completed';
      if (showAssignButton) {
        const assignBtnWidth = 50;
        const assignBtnHeight = 14;
        const assignBtnX = cardX + 6;
        const assignBtnY = cardY + 40;

        // Store button bounds for click detection
        task.assignBtnBounds = { x: assignBtnX, y: assignBtnY, width: assignBtnWidth, height: assignBtnHeight };

        // Button background (highlight if in assignment mode for this task)
        const isActiveAssignment = this.state.assignmentMode && this.state.assignmentSourceTask && this.state.assignmentSourceTask.id === task.id;
        this.ctx.fillStyle = isActiveAssignment ? '#fd7e14' : '#6c757d';
        this.primitives.roundRect(assignBtnX, assignBtnY, assignBtnWidth, assignBtnHeight, 3);
        this.ctx.fill();

        // Button text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 8px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText('➜ ASSIGN', assignBtnX + assignBtnWidth / 2, assignBtnY + 10);
        this.ctx.textAlign = 'left';
      }

      // Log button removed - was non-functional and confusing for users

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
        this.primitives.roundRect(
          agent.x - halfWidth - grow,
          agent.y - halfHeight - grow,
          (halfWidth * 2) + grow * 2,
          (halfHeight * 2) + grow * 2,
          14
        );
        this.ctx.fill();
        this.ctx.restore();
      }

      // Draw selection highlight if this agent is selected
      const agentId = agent.nodeId || agent.name;
      if (this.state.isNodeSelected(agentId)) {
        const isPrimary = this.state.isFirstSelected(agentId);
        this.drawSelectionHighlight(
          agent.x - halfWidth,
          agent.y - halfHeight,
          halfWidth * 2,
          halfHeight * 2,
          12,
          isPrimary
        );
      }

      // Draw agent rectangle
      this.ctx.fillStyle = agent.color;
      this.ctx.shadowColor = 'rgba(0,0,0,0.12)';
      this.ctx.shadowBlur = 10;
      this.primitives.roundRect(agent.x - halfWidth, agent.y - halfHeight, halfWidth * 2, halfHeight * 2, 12);
      this.ctx.fill();
      this.ctx.shadowColor = 'transparent';

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
      // Position on top-left to leave room for delete button on the right
      this.ctx.arc(agent.x - halfWidth + 12, agent.y - halfHeight + 12, 6, 0, Math.PI * 2);
      this.ctx.fill();

      // Draw agent name with instance badge
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.textAlign = 'center';
      this.ctx.textBaseline = 'middle';

      // If there's a result, move name up to make room
      const nameY = agent.lastResult ? agent.y - 15 : agent.y;

      // Display name with instance number badge (e.g., "default #1")
      const displayName = agent.instanceNumber ? `${agent.name} #${agent.instanceNumber}` : agent.name;
      this.ctx.fillText(displayName, agent.x, nameY);

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
        this.primitives.roundRect(resultBoxX, resultBoxY, resultBoxWidth, resultBoxHeight, 4);
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

      // Draw delete button (X) in top-right corner to match task nodes
      const deleteSize = 22;
      const deleteX = agent.x + halfWidth - deleteSize - 5;
      const deleteY = agent.y - halfHeight + 5;

      // Delete button background (red circle)
      this.ctx.fillStyle = 'rgba(239, 68, 68, 0.95)';
      this.ctx.beginPath();
      this.ctx.arc(deleteX + deleteSize / 2, deleteY + deleteSize / 2, deleteSize / 2, 0, Math.PI * 2);
      this.ctx.fill();

      // Delete button X (white)
      this.ctx.strokeStyle = '#ffffff';
      this.ctx.lineWidth = 2.5;
      this.ctx.lineCap = 'round';
      this.ctx.beginPath();
      this.ctx.moveTo(deleteX + 6, deleteY + 6);
      this.ctx.lineTo(deleteX + deleteSize - 6, deleteY + deleteSize - 6);
      this.ctx.moveTo(deleteX + deleteSize - 6, deleteY + 6);
      this.ctx.lineTo(deleteX + 6, deleteY + deleteSize - 6);
      this.ctx.stroke();

      // Store delete button bounds for click detection
      agent.deleteButton = {
        x: deleteX,
        y: deleteY,
        width: deleteSize,
        height: deleteSize
      };
    });
  }

  /**
   * Draw attachment nodes (notes/files/links)
   */
  drawAttachments() {
    if (!this.state.attachments || this.state.attachments.length === 0) return;

    this.state.attachments.forEach((attachment) => {
      if (attachment.x == null || attachment.y == null) return;

      const cardWidth = 160;
      const cardHeight = 70;
      const cardX = attachment.x - cardWidth / 2;
      const cardY = attachment.y - cardHeight / 2;

      // Store bounds for hit testing/ports
      attachment.cardBounds = { x: cardX, y: cardY, width: cardWidth, height: cardHeight };

      // Draw selection highlight if this attachment is selected
      if (this.state.isNodeSelected(attachment.id)) {
        const isPrimary = this.state.isFirstSelected(attachment.id);
        this.drawSelectionHighlight(cardX, cardY, cardWidth, cardHeight, 8, isPrimary);
      }

      const baseColor = attachment.color || '#e2e8f0';
      const icon = attachment.type === 'image' ? '🖼️' : attachment.type === 'doc' ? '📄' : '📎';

      // Background
      this.ctx.save();
      this.ctx.fillStyle = '#ffffff';
      this.ctx.shadowColor = 'rgba(0,0,0,0.1)';
      this.ctx.shadowBlur = 8;
      this.ctx.shadowOffsetY = 2;
      this.primitives.roundRect(cardX, cardY, cardWidth, cardHeight, 8);
      this.ctx.fill();
      this.ctx.restore();

      // Border
      this.ctx.strokeStyle = baseColor;
      this.ctx.lineWidth = 2;
      this.ctx.beginPath();
      this.primitives.roundRect(cardX, cardY, cardWidth, cardHeight, 8);
      this.ctx.stroke();

      // Title
      this.ctx.fillStyle = '#0f172a';
      this.ctx.font = 'bold 12px system-ui';
      this.ctx.fillText(`${icon} ${attachment.title || 'Attachment'}`, cardX + 10, cardY + 20);

      // Badge
      this.ctx.fillStyle = '#64748b';
      this.ctx.font = '10px system-ui';
      const fileMeta = attachment.file || attachment.file_meta;
      const badge = attachment.link_url ? 'Link' : (fileMeta?.name ? fileMeta.name : attachment.type || 'note');
      this.ctx.fillText(badge, cardX + 10, cardY + 38);

      // Body preview
      if (attachment.body) {
        const preview = attachment.body.length > 50 ? `${attachment.body.slice(0, 47)}...` : attachment.body;
        this.ctx.fillStyle = '#475569';
        this.ctx.font = '10px system-ui';
        this.ctx.fillText(preview, cardX + 10, cardY + 55);
      }

      // Attach button (bottom right)
      const attachWidth = 58;
      const attachHeight = 20;
      const attachX = cardX + cardWidth - attachWidth - 10;
      const attachY = cardY + cardHeight - attachHeight - 10;
      this.ctx.fillStyle = '#2563eb';
      this.primitives.roundRect(attachX, attachY, attachWidth, attachHeight, 6);
      this.ctx.fill();
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = '10px system-ui';
      this.ctx.fillText('Attach', attachX + 10, attachY + 14);

      attachment.attachButton = {
        x: attachX,
        y: attachY,
        width: attachWidth,
        height: attachHeight
      };

      // Delete button (top-right)
      const deleteSize = 18;
      const deleteX = cardX + cardWidth - deleteSize - 6;
      const deleteY = cardY + 6;
      this.ctx.fillStyle = 'rgba(239, 68, 68, 0.95)';
      this.ctx.beginPath();
      this.ctx.arc(deleteX + deleteSize / 2, deleteY + deleteSize / 2, deleteSize / 2, 0, Math.PI * 2);
      this.ctx.fill();

      this.ctx.strokeStyle = '#ffffff';
      this.ctx.lineWidth = 2;
      this.ctx.beginPath();
      this.ctx.moveTo(deleteX + 5, deleteY + 5);
      this.ctx.lineTo(deleteX + deleteSize - 5, deleteY + deleteSize - 5);
      this.ctx.moveTo(deleteX + deleteSize - 5, deleteY + 5);
      this.ctx.lineTo(deleteX + 5, deleteY + deleteSize - 5);
      this.ctx.stroke();

      attachment.deleteButton = {
        x: deleteX,
        y: deleteY,
        width: deleteSize,
        height: deleteSize
      };

      // Output port indicator
      this.ctx.fillStyle = baseColor;
      this.ctx.beginPath();
      this.ctx.arc(attachment.x, cardY + cardHeight + 5, 6, 0, Math.PI * 2);
      this.ctx.fill();
      this.ctx.strokeStyle = '#ffffff';
      this.ctx.lineWidth = 1;
      this.ctx.stroke();
    });
  }

  /**
   * Draw store nodes
   */
  drawStoreNodes() {
    if (!this.state.storeNodes || this.state.storeNodes.length === 0) return;

    this.state.storeNodes.forEach((storeNode) => {
      if (storeNode.x == null || storeNode.y == null) return;

      const cardWidth = 180;
      const cardHeight = 110;
      const cardX = storeNode.x - cardWidth / 2;
      const cardY = storeNode.y - cardHeight / 2;

      // Store bounds for hit testing
      storeNode.cardBounds = { x: cardX, y: cardY, width: cardWidth, height: cardHeight };

      // Draw selection highlight if this store node is selected
      if (this.state.isNodeSelected(storeNode.id)) {
        const isPrimary = this.state.isFirstSelected(storeNode.id);
        this.drawSelectionHighlight(cardX, cardY, cardWidth, cardHeight, 8, isPrimary);
      }

      // Base color (teal/cyan for storage)
      const baseColor = '#14b8a6';

      // Background
      this.ctx.save();
      this.ctx.fillStyle = '#ffffff';
      this.ctx.shadowColor = 'rgba(0,0,0,0.1)';
      this.ctx.shadowBlur = 8;
      this.ctx.shadowOffsetY = 2;
      this.primitives.roundRect(cardX, cardY, cardWidth, cardHeight, 8);
      this.ctx.fill();
      this.ctx.restore();

      // Border
      this.ctx.strokeStyle = baseColor;
      this.ctx.lineWidth = 3;
      this.ctx.beginPath();
      this.primitives.roundRect(cardX, cardY, cardWidth, cardHeight, 8);
      this.ctx.stroke();

      // Storage icon (disk/database icon)
      const iconSize = 20;
      const iconX = cardX + 10;
      const iconY = cardY + 16;

      this.ctx.strokeStyle = baseColor;
      this.ctx.fillStyle = baseColor;
      this.ctx.lineWidth = 2;

      // Draw disk icon
      this.ctx.beginPath();
      this.ctx.arc(iconX + iconSize / 2, iconY, iconSize / 3, 0, Math.PI * 2);
      this.ctx.fill();

      // Draw disk platter lines
      this.ctx.strokeStyle = '#ffffff';
      this.ctx.lineWidth = 1.5;
      this.ctx.beginPath();
      this.ctx.moveTo(iconX + 5, iconY - 3);
      this.ctx.lineTo(iconX + 15, iconY + 3);
      this.ctx.stroke();

      // Title
      this.ctx.fillStyle = '#0f172a';
      this.ctx.font = 'bold 12px system-ui';
      const title = storeNode.name || 'Store';
      this.ctx.fillText(title, iconX + iconSize + 10, iconY + 4);

      // Format badge
      const formatText = storeNode.format || 'json';
      const badgeX = cardX + cardWidth - 60;
      const badgeY = cardY + 8;
      this.ctx.fillStyle = baseColor + '20'; // 20% opacity background
      this.primitives.roundRect(badgeX, badgeY, 50, 18, 4);
      this.ctx.fill();
      this.ctx.fillStyle = baseColor;
      this.ctx.font = '10px system-ui';
      this.ctx.fillText(formatText.toUpperCase(), badgeX + 8, badgeY + 12);

      // Write mode
      this.ctx.fillStyle = '#475569';
      this.ctx.font = '11px system-ui';
      const writeModeText = storeNode.write_mode === 'append' ? 'Append mode' : 'Overwrite mode';
      this.ctx.fillText(writeModeText, cardX + 10, cardY + 45);

      // Directory path (truncated if too long)
      this.ctx.fillStyle = '#64748b';
      this.ctx.font = '10px system-ui';
      let basedir = storeNode.base_dir || '/path/to/store';
      if (basedir.length > 25) {
        basedir = '...' + basedir.slice(-22);
      }
      this.ctx.fillText(basedir, cardX + 10, cardY + 62);

      // Stats
      const writeCount = storeNode.write_count || 0;
      this.ctx.fillStyle = '#10b981';
      this.ctx.font = 'bold 10px system-ui';
      this.ctx.fillText(`${writeCount} writes`, cardX + 10, cardY + 78);

      // Last error (if any)
      if (storeNode.last_error && storeNode.last_error !== '') {
        this.ctx.fillStyle = '#ef4444';
        this.ctx.font = '9px system-ui';
        this.ctx.fillText('⚠ Error', cardX + 10, cardY + 92);
      }

      // Assign Agent button (bottom left)
      const assignBtnWidth = 70;
      const assignBtnHeight = 16;
      const assignBtnX = cardX + 10;
      const assignBtnY = cardY + cardHeight - assignBtnHeight - 6;

      // Check if in assignment mode for this store
      const isActiveAssignment = this.state.storeAssignmentMode &&
                                  this.state.storeAssignmentSource &&
                                  this.state.storeAssignmentSource.canvas_node_id === storeNode.canvas_node_id;

      this.ctx.fillStyle = isActiveAssignment ? '#fd7e14' : '#6c757d';
      this.primitives.roundRect(assignBtnX, assignBtnY, assignBtnWidth, assignBtnHeight, 3);
      this.ctx.fill();

      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 9px system-ui';
      this.ctx.textAlign = 'center';
      this.ctx.fillText('⇄ ASSIGN', assignBtnX + assignBtnWidth / 2, assignBtnY + 11);
      this.ctx.textAlign = 'left';

      storeNode.assignBtnBounds = {
        x: assignBtnX,
        y: assignBtnY,
        width: assignBtnWidth,
        height: assignBtnHeight
      };

      // Delete button (top-right corner)
      const deleteSize = 18;
      const deleteX = cardX + cardWidth - deleteSize - 6;
      const deleteY = cardY + 6;
      this.ctx.fillStyle = 'rgba(239, 68, 68, 0.95)';
      this.ctx.beginPath();
      this.ctx.arc(deleteX + deleteSize / 2, deleteY + deleteSize / 2, deleteSize / 2, 0, Math.PI * 2);
      this.ctx.fill();

      this.ctx.strokeStyle = '#ffffff';
      this.ctx.lineWidth = 2;
      this.ctx.beginPath();
      this.ctx.moveTo(deleteX + 5, deleteY + 5);
      this.ctx.lineTo(deleteX + deleteSize - 5, deleteY + deleteSize - 5);
      this.ctx.moveTo(deleteX + deleteSize - 5, deleteY + 5);
      this.ctx.lineTo(deleteX + 5, deleteY + deleteSize - 5);
      this.ctx.stroke();

      storeNode.deleteButton = {
        x: deleteX,
        y: deleteY,
        width: deleteSize,
        height: deleteSize
      };

      // Input port (for receiving data)
      const inputPortSize = 10;
      const inputPortX = cardX - inputPortSize / 2;
      const inputPortY = cardY + cardHeight / 2 - inputPortSize / 2;

      this.ctx.fillStyle = baseColor;
      this.ctx.beginPath();
      this.ctx.arc(inputPortX + inputPortSize / 2, inputPortY + inputPortSize / 2, inputPortSize / 2, 0, Math.PI * 2);
      this.ctx.fill();

      // Store port bounds for connections
      storeNode.inPort = {
        x: inputPortX + inputPortSize / 2,
        y: inputPortY + inputPortSize / 2
      };
    });
  }

  /**
   * Get human-readable schedule summary
   */
  getScheduleSummary(config) {
    if (!config) return 'No schedule';

    switch (config.schedule_type) {
      case 'interval':
        return `Every ${config.interval_minutes || 60} minutes`;

      case 'daily':
        const dailyHour = (config.hour || 9).toString().padStart(2, '0');
        const dailyMinute = (config.minute || 0).toString().padStart(2, '0');
        return `Daily at ${dailyHour}:${dailyMinute}`;

      case 'weekly':
        const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
        const day = days[config.day_of_week || 1];
        const weeklyHour = (config.hour || 9).toString().padStart(2, '0');
        const weeklyMinute = (config.minute || 0).toString().padStart(2, '0');
        return `${day} at ${weeklyHour}:${weeklyMinute}`;

      case 'cron':
        return `Cron: ${config.cron_expr || '0 9 * * *'}`;

      case 'relative_delay':
        const delay = config.delay_duration || 5;
        const once = config.trigger_once ? ' (once)' : '';
        return `${delay}m delay${once}`;

      default:
        return config.schedule_type || 'Unknown';
    }
  }
}
