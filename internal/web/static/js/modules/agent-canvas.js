/**
 * AgentCanvas - Visual canvas for real-time agent collaboration
 * Displays agents as nodes with tasks flowing between them
 */
class AgentCanvas {
  constructor(canvasId, studioId) {
    this.canvas = document.getElementById(canvasId);
    this.ctx = this.canvas.getContext('2d');
    this.studioId = studioId;
    this.studio = null;
    this.agents = [];
    this.tasks = [];
    this.messages = [];
    this.mission = null; // Current mission
    this.eventSource = null;

    // Canvas state
    this.offsetX = 0;
    this.offsetY = 0;
    this.scale = 1;
    this.isDragging = false;
    this.isDraggingAgent = false;
    this.draggedAgent = null;
    this.isDraggingTask = false;
    this.draggedTask = null;
    this.dragStartX = 0;
    this.dragStartY = 0;

    // Animation
    this.animationFrame = null;
    this.animationPaused = false;
    this.particles = []; // For visual effects

    // Canvas appearance
    this.backgroundColor = '#e8e8e8'; // Default background color

    // Expanded task panel state
    this.expandedTask = null;
    this.expandedPanelWidth = 0;
    this.expandedPanelTargetWidth = 400;
    this.expandedPanelAnimating = false;

    // Connection mode state
    this.connectionMode = false;
    this.connectionSourceTask = null;
    this.highlightedAgent = null;

    // Callback functions (set by parent)
    this.onAgentClick = null;
    this.onMetricsUpdate = null;
    this.onTimelineEvent = null;

    // Setup canvas
    this.resize();
    window.addEventListener('resize', () => this.resize());

    // Mouse interactions
    this.canvas.addEventListener('mousedown', (e) => this.onMouseDown(e));
    this.canvas.addEventListener('mousemove', (e) => this.onMouseMove(e));
    this.canvas.addEventListener('mouseup', () => this.onMouseUp());
    this.canvas.addEventListener('mouseleave', () => this.onMouseUp());
    this.canvas.addEventListener('wheel', (e) => this.onWheel(e));
    this.canvas.addEventListener('click', (e) => this.onClick(e));
  }

  resize() {
    const rect = this.canvas.getBoundingClientRect();
    this.canvas.width = rect.width * window.devicePixelRatio;
    this.canvas.height = rect.height * window.devicePixelRatio;
    this.ctx.scale(window.devicePixelRatio, window.devicePixelRatio);
    this.width = rect.width;
    this.height = rect.height;
    this.draw();
  }

  async init() {
    try {
      // Load studio data
      const response = await fetch(`/api/studios/${this.studioId}`);
      this.studio = await response.json();

      // Load mission from shared data if it exists
      if (this.studio.shared_data && this.studio.shared_data.mission) {
        this.mission = this.studio.shared_data.mission;
      }

      // Load tasks from studio
      if (this.studio.tasks) {
        this.tasks = this.studio.tasks.map(task => {
          // If task doesn't have position, set to null so it will be calculated in drawTaskFlows
          return {
            ...task,
            x: task.x ?? null,
            y: task.y ?? null
          };
        });
      }

      // Initialize agent positions
      this.initializeAgents();

      // Connect to real-time events
      this.connectEventStream();

      // Start animation loop
      this.startAnimation();

      // Update canvas info
      document.getElementById('canvas-info').textContent =
        `Studio: ${this.studio.name || this.studioId} | Agents: ${this.agents.length}`;

      // Initialize metrics
      this.updateMetrics();

    } catch (error) {
      console.error('Failed to initialize canvas:', error);
      document.getElementById('canvas-info').textContent = 'Error loading studio';
    }
  }

  initializeAgents() {
    if (!this.studio || !this.studio.agents) return;

    const agentCount = this.studio.agents.length;
    const centerY = this.height * 0.6; // Position lower to avoid mission box
    const spacing = Math.min(150, (this.width * 0.8) / Math.max(agentCount - 1, 1));
    const totalWidth = spacing * (agentCount - 1);
    const startX = (this.width - totalWidth) / 2;

    this.agents = this.studio.agents.map((agentName, index) => {
      return {
        name: agentName,
        x: startX + (index * spacing),
        y: centerY,
        radius: 40,
        color: this.getAgentColor(index),
        status: 'idle', // idle, active, busy
        tasks: [],
        pulsePhase: Math.random() * Math.PI * 2
      };
    });

    // Load tasks if available
    if (this.studio.tasks) {
      this.tasks = this.studio.tasks.map(task => ({
        id: task.id,
        from: task.from,
        to: task.to,
        description: task.description,
        status: task.status,
        progress: 0
      }));
    }
  }

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

  connectEventStream() {
    if (this.eventSource) {
      this.eventSource.close();
    }

    this.eventSource = new EventSource(`/api/studios/${this.studioId}/events`);

    this.eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        this.handleEvent(data);
      } catch (error) {
        console.error('Failed to parse event:', error);
      }
    };

    this.eventSource.onerror = (error) => {
      console.error('EventSource error:', error);
      // Attempt to reconnect after 5 seconds
      setTimeout(() => {
        if (this.eventSource && this.eventSource.readyState === EventSource.CLOSED) {
          this.connectEventStream();
        }
      }, 5000);
    };
  }

  handleEvent(event) {
    console.log('Canvas event:', event);

    switch (event.type) {
      case 'task.created':
      case 'task_created':
        this.addTask(event.data);
        break;
      case 'task.started':
      case 'task_started':
        this.updateTaskStatus(event.data.task_id, 'in_progress');
        this.setAgentStatus(event.data.assigned_to, 'active');
        break;
      case 'task.completed':
      case 'task_completed':
        this.updateTaskStatus(event.data.task_id, 'completed');
        break;
      case 'message.sent':
      case 'message_sent':
        this.addMessage(event.data);
        break;
      case 'mission_started':
        this.setMission(event.data.mission);
        break;
    }

    // Forward event to timeline callback
    if (this.onTimelineEvent) {
      this.onTimelineEvent(event);
    }

    // Update metrics after any task-related event
    if (event.type.includes('task')) {
      this.updateMetrics();
    }
  }

  addTask(taskData) {
    const task = {
      id: taskData.task_id || taskData.id,
      from: taskData.from || 'orchestrator',
      to: taskData.assigned_to || taskData.to,
      description: taskData.description,
      status: 'pending',
      progress: 0
    };
    this.tasks.push(task);

    // Create particle effect
    this.createTaskParticles(task);

    // Update metrics
    this.updateMetrics();
  }

  updateTaskStatus(taskId, status) {
    const task = this.tasks.find(t => t.id === taskId);
    if (task) {
      task.status = status;
      if (status === 'in_progress') {
        task.progress = 0;
      } else if (status === 'completed') {
        task.progress = 100;
      }
      // Update metrics when task status changes
      this.updateMetrics();
    }
  }

  setAgentStatus(agentName, status) {
    const agent = this.agents.find(a => a.name === agentName);
    if (agent) {
      agent.status = status;
    }
  }

  addMessage(messageData) {
    this.messages.push({
      from: messageData.from,
      to: messageData.to,
      content: messageData.content,
      timestamp: Date.now()
    });

    // Keep only last 50 messages
    if (this.messages.length > 50) {
      this.messages.shift();
    }
  }

  setMission(missionText) {
    this.mission = missionText;
    console.log('Mission set on canvas:', missionText);
  }

  createTaskParticles(task) {
    const fromAgent = this.agents.find(a => a.name === task.from);
    const toAgent = this.agents.find(a => a.name === task.to);

    if (fromAgent && toAgent) {
      for (let i = 0; i < 20; i++) {
        this.particles.push({
          x: fromAgent.x,
          y: fromAgent.y,
          targetX: toAgent.x,
          targetY: toAgent.y,
          progress: 0,
          speed: 0.01 + Math.random() * 0.02,
          size: 2 + Math.random() * 3,
          color: fromAgent.color,
          alpha: 1
        });
      }
    }
  }

  startAnimation() {
    const animate = () => {
      this.update();
      this.draw();
      this.animationFrame = requestAnimationFrame(animate);
    };
    animate();
  }

  update() {
    // Skip updates if animation is paused
    if (this.animationPaused) return;

    // Update task progress
    this.tasks.forEach(task => {
      if (task.status === 'in_progress' && task.progress < 100) {
        task.progress += 0.5;
      }
    });

    // Update particles
    this.particles = this.particles.filter(p => {
      p.progress += p.speed;
      p.x = p.x + (p.targetX - p.x) * p.progress;
      p.y = p.y + (p.targetY - p.y) * p.progress;
      p.alpha = 1 - p.progress;
      return p.progress < 1;
    });

    // Update agent pulse
    this.agents.forEach(agent => {
      agent.pulsePhase += 0.05;
    });
  }

  draw() {
    // Clear canvas with selected background color
    this.ctx.fillStyle = this.backgroundColor;
    this.ctx.fillRect(0, 0, this.width, this.height);

    this.ctx.save();
    this.ctx.translate(this.offsetX, this.offsetY);
    this.ctx.scale(this.scale, this.scale);

    // Draw connections between agents (disabled)
    // this.drawConnections();

    // Draw task flows
    this.drawTaskFlows();

    // Draw particles
    this.drawParticles();

    // Draw agents
    this.drawAgents();

    this.ctx.restore();

    // Draw mission OUTSIDE the transform context (so it stays fixed at top)
    this.drawMission();

    // Draw expanded task panel OUTSIDE the transform context (fixed position)
    if (this.expandedPanelWidth > 0) {
      this.drawExpandedTaskPanel();
    }

    // Draw connection mode indicator
    if (this.connectionMode) {
      this.drawConnectionModeIndicator();
    }
  }

  drawConnections() {
    this.ctx.strokeStyle = 'rgba(0,0,0,0.05)';
    this.ctx.lineWidth = 1;

    for (let i = 0; i < this.agents.length; i++) {
      for (let j = i + 1; j < this.agents.length; j++) {
        this.ctx.beginPath();
        this.ctx.moveTo(this.agents[i].x, this.agents[i].y);
        this.ctx.lineTo(this.agents[j].x, this.agents[j].y);
        this.ctx.stroke();
      }
    }
  }

  drawTaskFlows() {
    if (!this.tasks || this.tasks.length === 0) return;

    this.tasks.forEach((task, index) => {
      const fromAgent = this.agents.find(a => a.name === task.from);
      const toAgent = this.agents.find(a => a.name === task.to);

      // Skip if target agent not found
      if (!toAgent) return;

      // Handle system/user-created tasks (no from agent)
      const isSystemTask = !fromAgent || task.from === 'system' || task.from === 'user';

      // Calculate default position if task doesn't have one
      if (task.x == null || task.y == null) {  // Use == to catch both null and undefined
        if (isSystemTask) {
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
      if (!isSystemTask) {
        this.ctx.strokeStyle = fromAgent.color + '40';
        this.ctx.lineWidth = 2;
        this.ctx.setLineDash([5, 5]);
        this.ctx.beginPath();
        this.ctx.moveTo(fromAgent.x, fromAgent.y);
        this.ctx.lineTo(task.x, task.y);
        this.ctx.stroke();
        this.ctx.setLineDash([]);
      }

      // Draw connection line from task to receiver
      this.ctx.strokeStyle = toAgent.color + '40';
      this.ctx.lineWidth = 2;
      this.ctx.setLineDash([5, 5]);
      this.ctx.beginPath();
      this.ctx.moveTo(task.x, task.y);
      this.ctx.lineTo(toAgent.x, toAgent.y);
      this.ctx.stroke();
      this.ctx.setLineDash([]);

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

      // Task description (truncated)
      this.ctx.fillStyle = '#212529';
      this.ctx.font = 'bold 11px system-ui';
      const maxWidth = cardWidth - 16;
      let description = task.description || 'Task';
      if (description.length > 25) {
        description = description.substring(0, 22) + '...';
      }
      this.ctx.fillText(description, cardX + 8, cardY + 18);

      // Task status
      this.ctx.fillStyle = '#6c757d';
      this.ctx.font = '9px system-ui';
      const statusText = `${task.from} → ${task.to}`;
      this.ctx.fillText(statusText, cardX + 8, cardY + 34);

      // Status badge
      this.ctx.fillStyle = borderColor;
      this.ctx.font = 'bold 8px system-ui';
      const badge = (task.status || 'pending').toUpperCase();
      const badgeWidth = this.ctx.measureText(badge).width + 8;
      this.ctx.fillRect(cardX + 8, cardY + 40, badgeWidth, 12);
      this.ctx.fillStyle = '#ffffff';
      this.ctx.fillText(badge, cardX + 12, cardY + 49);
    });

    // Draw result-to-task connections for tasks with input_task_ids
    this.drawResultConnections();
  }

  /**
   * Draw connections from completed tasks to tasks that use their results
   */
  drawResultConnections() {
    if (!this.tasks || this.tasks.length === 0) return;

    this.tasks.forEach(task => {
      // Check if this task has input tasks
      if (!task.input_task_ids || task.input_task_ids.length === 0) return;

      // Draw connection from each input task to this task
      task.input_task_ids.forEach(inputTaskId => {
        const inputTask = this.tasks.find(t => t.id === inputTaskId);
        if (!inputTask || !inputTask.x || !inputTask.y) return;

        // Draw a dashed line with a different color to indicate result flow
        this.ctx.strokeStyle = '#9b59b6'; // Purple for result connections
        this.ctx.lineWidth = 2;
        this.ctx.setLineDash([8, 4]);
        this.ctx.beginPath();
        this.ctx.moveTo(inputTask.x, inputTask.y);
        this.ctx.lineTo(task.x, task.y);
        this.ctx.stroke();
        this.ctx.setLineDash([]);

        // Draw an arrow at the midpoint
        const midX = (inputTask.x + task.x) / 2;
        const midY = (inputTask.y + task.y) / 2;
        const angle = Math.atan2(task.y - inputTask.y, task.x - inputTask.x);

        // Draw arrow head
        this.ctx.fillStyle = '#9b59b6';
        this.ctx.beginPath();
        this.ctx.moveTo(midX, midY);
        this.ctx.lineTo(midX - 10 * Math.cos(angle - Math.PI / 6), midY - 10 * Math.sin(angle - Math.PI / 6));
        this.ctx.lineTo(midX - 10 * Math.cos(angle + Math.PI / 6), midY - 10 * Math.sin(angle + Math.PI / 6));
        this.ctx.closePath();
        this.ctx.fill();

        // Draw a small label "RESULT"
        this.ctx.fillStyle = '#9b59b6';
        this.ctx.font = 'bold 9px system-ui';
        this.ctx.fillText('RESULT', midX + 5, midY - 5);
      });
    });
  }

  drawParticles() {
    this.particles.forEach(p => {
      this.ctx.fillStyle = p.color + Math.floor(p.alpha * 255).toString(16).padStart(2, '0');
      this.ctx.beginPath();
      this.ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2);
      this.ctx.fill();
    });
  }

  drawAgents() {
    this.agents.forEach(agent => {
      // Draw connection mode highlight
      if (this.connectionMode) {
        this.ctx.strokeStyle = '#3b82f6';
        this.ctx.lineWidth = 4;
        this.ctx.shadowColor = '#3b82f6';
        this.ctx.shadowBlur = 15;
        this.ctx.beginPath();
        this.ctx.arc(agent.x, agent.y, agent.radius + 8, 0, Math.PI * 2);
        this.ctx.stroke();
        this.ctx.shadowColor = 'transparent';
      }

      // Draw pulse effect for active agents
      if (agent.status === 'active') {
        const pulseSize = agent.radius + 10 * Math.sin(agent.pulsePhase);
        this.ctx.fillStyle = agent.color + '20';
        this.ctx.beginPath();
        this.ctx.arc(agent.x, agent.y, pulseSize, 0, Math.PI * 2);
        this.ctx.fill();
      }

      // Draw agent circle
      this.ctx.fillStyle = agent.color;
      this.ctx.beginPath();
      this.ctx.arc(agent.x, agent.y, agent.radius, 0, Math.PI * 2);
      this.ctx.fill();

      // Draw status indicator
      let statusColor;
      switch (agent.status) {
        case 'active': statusColor = '#10b981'; break;
        case 'busy': statusColor = '#f59e0b'; break;
        default: statusColor = '#6b7280';
      }
      this.ctx.fillStyle = statusColor;
      this.ctx.beginPath();
      this.ctx.arc(agent.x + agent.radius - 10, agent.y - agent.radius + 10, 6, 0, Math.PI * 2);
      this.ctx.fill();

      // Draw agent name
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.textAlign = 'center';
      this.ctx.textBaseline = 'middle';
      this.ctx.fillText(agent.name, agent.x, agent.y);

      // Draw task count
      if (agent.tasks.length > 0) {
        this.ctx.font = '10px system-ui';
        this.ctx.fillText(`${agent.tasks.length} tasks`, agent.x, agent.y + 20);
      }
    });
  }

  drawMission() {
    if (!this.mission) return;

    // Calculate center of canvas in world coordinates
    const centerX = this.width / 2;
    const centerY = this.height / 2;

    // Draw mission background box
    this.ctx.save();

    // Measure text to size the box appropriately
    this.ctx.font = 'bold 18px system-ui';
    const maxWidth = this.width * 0.6; // Max 60% of canvas width
    const lines = this.wrapText(this.mission, maxWidth);
    const lineHeight = 26;
    const totalHeight = lines.length * lineHeight + 30;
    const boxWidth = Math.min(maxWidth + 40, this.width * 0.7);
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

  // Helper function to wrap text
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

  drawExpandedTaskPanel() {
    if (!this.expandedTask) return;

    const panelX = this.width - this.expandedPanelWidth;
    const panelY = 0;
    const panelHeight = this.height;

    // Draw panel background with shadow
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.expandedPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.expandedPanelWidth < 100) {
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
    this.ctx.fillText('×', panelX + this.expandedPanelWidth - padding, currentY + 20);
    currentY += 40;

    // Task title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Task Details', contentX, currentY);
    currentY += 30;

    // Status badge
    let statusColor = '#6b7280';
    if (this.expandedTask.status === 'completed') statusColor = '#10b981';
    else if (this.expandedTask.status === 'in_progress') statusColor = '#3b82f6';
    else if (this.expandedTask.status === 'failed') statusColor = '#ef4444';
    else if (this.expandedTask.status === 'pending') statusColor = '#f59e0b';

    this.ctx.fillStyle = statusColor;
    this.ctx.font = 'bold 10px system-ui';
    const statusText = (this.expandedTask.status || 'pending').toUpperCase();
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
    const descLines = this.wrapText(this.expandedTask.description || '', this.expandedPanelWidth - padding * 2);
    descLines.forEach(line => {
      this.ctx.fillText(line, contentX, currentY);
      currentY += 18;
    });
    currentY += 15;

    // Agents
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText(`From: ${this.expandedTask.from}  →  To: ${this.expandedTask.to}`, contentX, currentY);
    currentY += 25;

    // Separator line
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.expandedPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Result section
    if (this.expandedTask.result) {
      this.ctx.fillStyle = '#059669';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📊 Result', contentX, currentY);
      currentY += 25;

      // Result background box
      const resultBoxY = currentY;
      const resultBoxHeight = Math.min(300, panelHeight - currentY - padding);
      this.ctx.fillStyle = '#f0fdf4';
      this.ctx.strokeStyle = '#10b981';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, resultBoxY, this.expandedPanelWidth - padding * 2, resultBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      // Result text
      this.ctx.fillStyle = '#065f46';
      this.ctx.font = '11px monospace';
      const resultLines = this.wrapText(this.expandedTask.result, this.expandedPanelWidth - padding * 2 - 20);
      const maxLines = Math.floor((resultBoxHeight - 20) / 14);

      resultLines.slice(0, maxLines).forEach((line, i) => {
        this.ctx.fillText(line, contentX + 10, resultBoxY + 15 + i * 14);
      });

      if (resultLines.length > maxLines) {
        this.ctx.fillStyle = '#6b7280';
        this.ctx.font = 'italic 10px system-ui';
        this.ctx.fillText('... (scroll in task list for full result)', contentX + 10, resultBoxY + resultBoxHeight - 10);
      }
    } else if (this.expandedTask.error) {
      this.ctx.fillStyle = '#dc2626';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('❌ Error', contentX, currentY);
      currentY += 25;

      this.ctx.fillStyle = '#7f1d1d';
      this.ctx.font = '11px monospace';
      const errorLines = this.wrapText(this.expandedTask.error, this.expandedPanelWidth - padding * 2);
      errorLines.slice(0, 10).forEach(line => {
        this.ctx.fillText(line, contentX, currentY);
        currentY += 14;
      });
    } else {
      this.ctx.fillStyle = '#9ca3af';
      this.ctx.font = 'italic 12px system-ui';
      this.ctx.fillText('No result yet', contentX, currentY);
    }

    // "Connect Result to Agent" button (only if task has result and is completed)
    if (this.expandedTask.result && this.expandedTask.status === 'completed') {
      const buttonY = panelHeight - 80;
      const buttonWidth = this.expandedPanelWidth - padding * 2;
      const buttonHeight = 40;

      // Store button bounds for click detection
      this.connectButtonBounds = {
        x: panelX + padding,
        y: buttonY,
        width: buttonWidth,
        height: buttonHeight
      };

      // Draw button
      this.ctx.fillStyle = this.connectionMode ? '#dc2626' : '#3b82f6';
      this.ctx.strokeStyle = this.connectionMode ? '#991b1b' : '#1e40af';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, buttonY, buttonWidth, buttonHeight, 8);
      this.ctx.fill();
      this.ctx.stroke();

      // Button text
      this.ctx.fillStyle = '#ffffff';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.textAlign = 'center';
      const buttonText = this.connectionMode ? '✕ Cancel Connection' : '🔗 Connect Result to Agent';
      this.ctx.fillText(buttonText, panelX + this.expandedPanelWidth / 2, buttonY + 25);
      this.ctx.textAlign = 'left';
    } else {
      this.connectButtonBounds = null;
    }

    this.ctx.restore();
  }

  drawConnectionModeIndicator() {
    const centerX = this.width / 2;
    const centerY = 50;
    const boxWidth = 350;
    const boxHeight = 60;

    // Draw semi-transparent background
    this.ctx.fillStyle = 'rgba(59, 130, 246, 0.95)';
    this.ctx.strokeStyle = '#1e40af';
    this.ctx.lineWidth = 2;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 15;
    this.roundRect(centerX - boxWidth / 2, centerY, boxWidth, boxHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Draw text
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.textBaseline = 'middle';
    this.ctx.fillText('🔗 Connection Mode Active', centerX, centerY + 20);
    this.ctx.font = '12px system-ui';
    this.ctx.fillText('Click an agent to create a linked task', centerX, centerY + 40);
  }

  // Helper function to draw rounded rectangle
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

  // Mouse interaction handlers
  onMouseDown(e) {
    const rect = this.canvas.getBoundingClientRect();
    // Convert screen coordinates to canvas coordinates
    const x = (e.clientX - rect.left - this.offsetX) / this.scale;
    const y = (e.clientY - rect.top - this.offsetY) / this.scale;

    // Check if clicking on a task card first (tasks are drawn on top)
    if (this.tasks && this.tasks.length > 0) {
      for (let i = this.tasks.length - 1; i >= 0; i--) {  // Check in reverse order (top to bottom)
        const task = this.tasks[i];
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
            this.isDraggingTask = true;
            this.draggedTask = task;
            this.dragStartX = x;
            this.dragStartY = y;
            this.canvas.style.cursor = 'move';
            return;
          }
        }
      }
    }

    // Check if clicking on an agent
    for (const agent of this.agents) {
      const dist = Math.sqrt((x - agent.x) ** 2 + (y - agent.y) ** 2);
      if (dist <= agent.radius) {
        // Start dragging this agent
        this.isDraggingAgent = true;
        this.draggedAgent = agent;
        this.dragStartX = x;
        this.dragStartY = y;
        this.canvas.style.cursor = 'move';
        return;
      }
    }

    // Otherwise, start canvas panning
    this.isDragging = true;
    this.dragStartX = e.clientX - rect.left - this.offsetX;
    this.dragStartY = e.clientY - rect.top - this.offsetY;
    this.canvas.style.cursor = 'grabbing';
  }

  onMouseMove(e) {
    const rect = this.canvas.getBoundingClientRect();

    if (this.isDraggingTask && this.draggedTask) {
      // Drag the task
      const x = (e.clientX - rect.left - this.offsetX) / this.scale;
      const y = (e.clientY - rect.top - this.offsetY) / this.scale;

      this.draggedTask.x = x;
      this.draggedTask.y = y;
      this.draw();
      return;
    }

    if (this.isDraggingAgent && this.draggedAgent) {
      // Drag the agent
      const x = (e.clientX - rect.left - this.offsetX) / this.scale;
      const y = (e.clientY - rect.top - this.offsetY) / this.scale;

      this.draggedAgent.x = x;
      this.draggedAgent.y = y;
      this.draw();
      return;
    }

    if (this.isDragging) {
      // Pan the canvas
      this.offsetX = (e.clientX - rect.left) - this.dragStartX;
      this.offsetY = (e.clientY - rect.top) - this.dragStartY;
      this.draw();
    }
  }

  onMouseUp() {
    this.isDragging = false;
    this.isDraggingAgent = false;
    this.draggedAgent = null;
    this.isDraggingTask = false;
    this.draggedTask = null;
    this.canvas.style.cursor = 'grab';
  }

  onWheel(e) {
    e.preventDefault();
    const delta = e.deltaY > 0 ? 0.9 : 1.1;
    this.scale *= delta;
    this.scale = Math.max(0.5, Math.min(2, this.scale));
    this.draw();
  }

  onClick(e) {
    // Ignore clicks during drag operations
    if (this.isDragging || this.isDraggingAgent || this.isDraggingTask) return;

    const rect = this.canvas.getBoundingClientRect();
    // Screen coordinates (for UI elements like the panel)
    const screenX = e.clientX - rect.left;
    const screenY = e.clientY - rect.top;

    // Check if click is on close button of expanded panel
    if (this.expandedPanelWidth > 0) {
      const panelX = this.width - this.expandedPanelWidth;
      const closeButtonX = panelX + this.expandedPanelWidth - 40;
      const closeButtonY = 30;
      const closeButtonSize = 40;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.closeTaskPanel();
        return;
      }

      // Check if click is on "Connect Result to Agent" button
      if (this.connectButtonBounds) {
        const btn = this.connectButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.toggleConnectionMode();
          return;
        }
      }

      // If clicking anywhere on the panel, don't process other clicks
      if (screenX >= panelX) {
        return;
      }
    }

    // Convert screen coordinates to canvas coordinates
    const x = (e.clientX - rect.left - this.offsetX) / this.scale;
    const y = (e.clientY - rect.top - this.offsetY) / this.scale;

    // Check if click is on any task first (tasks are on top)
    for (let i = this.tasks.length - 1; i >= 0; i--) {
      const task = this.tasks[i];
      if (task && task.x != null && task.y != null) {
        const cardWidth = 160;
        const cardHeight = 60;
        const cardX = task.x - cardWidth / 2;
        const cardY = task.y - cardHeight / 2;

        if (x >= cardX && x <= cardX + cardWidth &&
            y >= cardY && y <= cardY + cardHeight) {
          // Task clicked - expand/collapse panel
          this.toggleTaskPanel(task);
          return;
        }
      }
    }

    // Check if click is on any agent
    for (const agent of this.agents) {
      const dist = Math.sqrt((x - agent.x) ** 2 + (y - agent.y) ** 2);
      if (dist <= agent.radius) {
        // Agent clicked
        if (this.connectionMode && this.connectionSourceTask) {
          // In connection mode - create task with result linked
          this.createConnectedTask(agent);
          return;
        } else if (this.onAgentClick) {
          this.onAgentClick(agent);
        }
        return;
      }
    }

    // Click on empty space - close expanded panel
    if (this.expandedTask) {
      this.closeTaskPanel();
    }
  }

  toggleTaskPanel(task) {
    if (this.expandedTask && this.expandedTask.id === task.id) {
      // Clicking the same task - close panel
      this.closeTaskPanel();
    } else {
      // Expand panel for this task
      this.expandedTask = task;
      this.expandedPanelAnimating = true;
      this.animatePanel(true);
    }
  }

  closeTaskPanel() {
    this.expandedPanelAnimating = true;
    this.animatePanel(false);
  }

  animatePanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.expandedPanelWidth = Math.min(
          this.expandedPanelWidth + speed,
          this.expandedPanelTargetWidth
        );

        if (this.expandedPanelWidth >= this.expandedPanelTargetWidth) {
          this.expandedPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.expandedPanelWidth = Math.max(this.expandedPanelWidth - speed, 0);

        if (this.expandedPanelWidth <= 0) {
          this.expandedPanelAnimating = false;
          this.expandedTask = null;
        } else {
          requestAnimationFrame(animate);
        }
      }
    };

    animate();
  }

  toggleConnectionMode() {
    if (this.connectionMode) {
      // Cancel connection mode
      this.connectionMode = false;
      this.connectionSourceTask = null;
      this.canvas.style.cursor = 'grab';
    } else {
      // Enter connection mode
      this.connectionMode = true;
      this.connectionSourceTask = this.expandedTask;
      this.canvas.style.cursor = 'crosshair';
    }
    this.draw();
  }

  async createConnectedTask(agent) {
    // Prompt for task description
    const description = prompt(
      `Create task for ${agent.name}\n\nThe result from "${this.connectionSourceTask.description}" will be provided as input.\n\nEnter task description:`,
      `Process the result from the previous task`
    );

    if (!description) {
      // User cancelled
      this.connectionMode = false;
      this.connectionSourceTask = null;
      this.canvas.style.cursor = 'grab';
      this.draw();
      return;
    }

    // Create task via API
    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          workspace_id: this.studioId,
          from: this.connectionSourceTask.to, // From the agent that completed the source task
          to: agent.name,
          description: description,
          input_task_ids: [this.connectionSourceTask.id],
          priority: 0,
        }),
      });

      if (!response.ok) {
        throw new Error('Failed to create task');
      }

      const result = await response.json();
      console.log('✅ Connected task created:', result);

      // Exit connection mode
      this.connectionMode = false;
      this.connectionSourceTask = null;
      this.canvas.style.cursor = 'grab';

      // Reload studio data to show new task
      await this.init();

      // Show success message
      alert(`✅ Task created!\n\n${agent.name} will process the result from the previous task.`);
    } catch (error) {
      console.error('❌ Error creating connected task:', error);
      alert('Failed to create task: ' + error.message);
      this.connectionMode = false;
      this.connectionSourceTask = null;
      this.canvas.style.cursor = 'grab';
      this.draw();
    }
  }

  updateMetrics() {
    if (!this.onMetricsUpdate) return;

    const completed = this.tasks.filter(t => t.status === 'completed').length;
    const inProgress = this.tasks.filter(t => t.status === 'in_progress').length;

    this.onMetricsUpdate({
      total: this.tasks.length,
      completed: completed,
      inProgress: inProgress
    });
  }

  destroy() {
    if (this.animationFrame) {
      cancelAnimationFrame(this.animationFrame);
    }
    if (this.eventSource) {
      this.eventSource.close();
    }
  }
}

// Make it globally accessible
window.AgentCanvas = AgentCanvas;
