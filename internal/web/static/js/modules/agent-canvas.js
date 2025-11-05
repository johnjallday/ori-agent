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
    this.dragStartX = 0;
    this.dragStartY = 0;

    // Animation
    this.animationFrame = null;
    this.animationPaused = false;
    this.particles = []; // For visual effects

    // Canvas appearance
    this.backgroundColor = '#e8e8e8'; // Default background color

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
        console.log('Loaded mission from workspace:', this.mission);
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
    this.tasks.forEach(task => {
      const fromAgent = this.agents.find(a => a.name === task.from);
      const toAgent = this.agents.find(a => a.name === task.to);

      if (!fromAgent || !toAgent) return;

      const progress = task.progress / 100;
      const x = fromAgent.x + (toAgent.x - fromAgent.x) * progress;
      const y = fromAgent.y + (toAgent.y - fromAgent.y) * progress;

      // Draw task indicator
      if (task.status === 'in_progress') {
        this.ctx.fillStyle = fromAgent.color;
        this.ctx.beginPath();
        this.ctx.arc(x, y, 8, 0, Math.PI * 2);
        this.ctx.fill();

        // Draw progress line
        this.ctx.strokeStyle = fromAgent.color;
        this.ctx.lineWidth = 3;
        this.ctx.beginPath();
        this.ctx.moveTo(fromAgent.x, fromAgent.y);
        this.ctx.lineTo(x, y);
        this.ctx.stroke();
      }
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
    if (this.isDragging || this.isDraggingAgent) return;

    const rect = this.canvas.getBoundingClientRect();
    // Convert screen coordinates to canvas coordinates
    const x = (e.clientX - rect.left - this.offsetX) / this.scale;
    const y = (e.clientY - rect.top - this.offsetY) / this.scale;

    // Check if click is on any agent
    for (const agent of this.agents) {
      const dist = Math.sqrt((x - agent.x) ** 2 + (y - agent.y) ** 2);
      if (dist <= agent.radius) {
        // Agent clicked
        if (this.onAgentClick) {
          this.onAgentClick(agent);
        }
        return;
      }
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
