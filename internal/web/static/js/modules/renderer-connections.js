/**
 * Renderer Connections
 *
 * Handles all connection and flow rendering:
 * - Agent connections
 * - Result connections
 * - Chain connections  
 * - Workflow connections
 * - Dragging connections
 * - Particle effects
 */

export class RendererConnections {
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

}
