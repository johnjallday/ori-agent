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
    this.resultScrollOffset = 0; // Scroll offset for result content
    this.resultBoxBounds = null; // Bounds of result box for scroll detection
    this.copyButtonBounds = null; // Bounds of copy button for click detection
    this.copyButtonState = 'idle'; // 'idle', 'hover', 'copied'

    // Expanded agent panel state
    this.expandedAgent = null;
    this.expandedAgentPanelWidth = 0;
    this.expandedAgentPanelTargetWidth = 400;
    this.expandedAgentPanelAnimating = false;

    // Connection mode state (task-to-task)
    this.connectionMode = false;
    this.connectionSourceTask = null;
    this.highlightedAgent = null;

    // Task-to-Agent assignment mode state
    this.assignmentMode = false;
    this.assignmentSourceTask = null;
    this.assignmentMouseX = 0;
    this.assignmentMouseY = 0;

    // Create task mode state
    this.createTaskMode = false;
    this.createTaskFormVisible = false;

    // Timeline panel state
    this.timelineVisible = false;
    this.timelinePanelWidth = 0;
    this.timelinePanelTargetWidth = 350;
    this.timelinePanelAnimating = false;
    this.timelineEvents = [];
    this.timelineScrollOffset = 0;
    this.timelineMaxEvents = 50;

    // Chain visualization state
    this.activeChains = []; // Array of active chain objects
    this.chainParticles = []; // Particles flowing along chain paths

    // Execution logs state (task ID -> array of log entries)
    this.executionLogs = {}; // { taskId: [{ type: 'thinking'|'tool_call'|'tool_success'|'tool_error', message: string, timestamp: Date }] }

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

    // Keyboard interactions
    window.addEventListener('keydown', (e) => this.onKeyDown(e));
  }

  onKeyDown(e) {
    // ESC key - cancel connection/assignment modes
    if (e.key === 'Escape' || e.key === 'Esc') {
      if (this.connectionMode) {
        this.connectionMode = false;
        this.connectionSourceTask = null;
        this.canvas.style.cursor = 'grab';
        this.draw();
        console.log('Connection mode cancelled');
      } else if (this.assignmentMode) {
        this.assignmentMode = false;
        this.assignmentSourceTask = null;
        this.canvas.style.cursor = 'grab';
        this.draw();
        console.log('Assignment mode cancelled');
      }
    }
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
      console.log('AgentCanvas.init() - studioId:', this.studioId);

      // Load studio data
      const response = await fetch(`/api/studios/${this.studioId}`);
      this.studio = await response.json();

      console.log('AgentCanvas.init() - studio data loaded:', this.studio);

      // Load workspace progress
      this.workspaceProgress = this.studio.workspace_progress || {
        total_tasks: 0,
        completed_tasks: 0,
        in_progress_tasks: 0,
        pending_tasks: 0,
        failed_tasks: 0,
        percentage: 0,
        active_agents: 0,
        idle_agents: 0,
        total_agents: 0
      };

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

      // Detect and initialize chains
      this.updateChains();

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

    // Get agent stats from studio data
    const agentStats = this.studio.agent_stats || {};

    this.agents = this.studio.agents.map((agentName, index) => {
      // Get stats for this agent
      const stats = agentStats[agentName] || {
        status: 'idle',
        current_tasks: [],
        queued_tasks: [],
        completed_tasks: 0,
        failed_tasks: 0,
        total_executions: 0
      };

      return {
        name: agentName,
        x: startX + (index * spacing),
        y: centerY,
        radius: 40,
        color: this.getAgentColor(index),
        status: stats.status, // Use status from backend
        currentTasks: stats.current_tasks || [],
        queuedTasks: stats.queued_tasks || [],
        completedTasks: stats.completed_tasks || 0,
        failedTasks: stats.failed_tasks || 0,
        totalExecutions: stats.total_executions || 0,
        tasks: [], // Legacy field, keep for compatibility
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

    // Toast notifications array
    this.notifications = this.notifications || [];

    // Connect to progress stream
    this.eventSource = new EventSource(`/api/orchestration/progress/stream?workspace_id=${this.studioId}`);

    // Handle initial state
    this.eventSource.addEventListener('initial', (event) => {
      try {
        const data = JSON.parse(event.data);
        console.log('📊 Initial progress state:', data);

        if (data.workspace_progress) {
          this.workspaceProgress = data.workspace_progress;
        }
        if (data.agent_stats) {
          this.updateAgentStats(data.agent_stats);
        }
        if (data.tasks) {
          this.tasks = data.tasks.map(task => ({
            ...task,
            x: task.x ?? null,
            y: task.y ?? null
          }));
        }
        this.draw();
      } catch (error) {
        console.error('Failed to parse initial event:', error);
      }
    });

    // Handle workspace progress updates
    this.eventSource.addEventListener('workspace.progress', (event) => {
      try {
        const data = JSON.parse(event.data);
        console.log('📊 Workspace progress update:', data);

        if (data.workspace_progress) {
          this.workspaceProgress = data.workspace_progress;
        }
        if (data.agent_stats) {
          this.updateAgentStats(data.agent_stats);
        }
        this.draw();
      } catch (error) {
        console.error('Failed to parse workspace progress:', error);
      }
    });

    // Handle task events
    this.eventSource.addEventListener('task.created', (event) => {
      const data = JSON.parse(event.data);
      this.handleTaskEvent(data);
      this.showNotification('Task created', 'info');
      this.addTimelineEvent(data);
    });

    this.eventSource.addEventListener('task.started', (event) => {
      const data = JSON.parse(event.data);
      this.handleTaskEvent(data);
      const taskDesc = data.data.description || 'Task';
      this.showNotification(`${taskDesc} started`, 'info');
      this.addTimelineEvent(data);
    });

    this.eventSource.addEventListener('task.completed', (event) => {
      const data = JSON.parse(event.data);
      this.handleTaskEvent(data);
      const taskDesc = data.data.description || 'Task';
      this.showNotification(`✓ ${taskDesc} completed`, 'success');
      this.addTimelineEvent(data);
    });

    this.eventSource.addEventListener('task.failed', (event) => {
      const data = JSON.parse(event.data);
      this.handleTaskEvent(data);
      const taskDesc = data.data.description || 'Task';
      const error = data.data.error || 'Unknown error';
      this.showNotification(`✗ ${taskDesc} failed: ${error}`, 'error');
      this.addTimelineEvent(data);
    });

    // Task execution detail events
    this.eventSource.addEventListener('task.thinking', (event) => {
      const data = JSON.parse(event.data);
      this.addExecutionLog(data.data.task_id, 'thinking', data.data.message || 'Analyzing task...');
      this.addTimelineEvent(data);
    });

    this.eventSource.addEventListener('task.tool_call', (event) => {
      const data = JSON.parse(event.data);
      const toolName = data.data.tool_name || 'Unknown tool';
      this.addExecutionLog(data.data.task_id, 'tool_call', `Calling tool: ${toolName}`);
      this.addTimelineEvent(data);
    });

    this.eventSource.addEventListener('task.tool_result', (event) => {
      const data = JSON.parse(event.data);
      const toolName = data.data.tool_name || 'Unknown tool';
      const success = data.data.success !== false;
      const message = success
        ? `✓ Tool ${toolName} completed`
        : `✗ Tool ${toolName} failed: ${data.data.error}`;
      this.addExecutionLog(data.data.task_id, success ? 'tool_success' : 'tool_error', message);
      this.addTimelineEvent(data);
    });

    // Generic message handler for other events
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

    console.log('🔄 Connected to progress stream');
  }

  handleTaskEvent(eventData) {
    const taskId = eventData.data.task_id;
    const task = this.tasks.find(t => t.id === taskId);

    if (task) {
      // Update existing task
      if (eventData.type === 'task.started') {
        task.status = 'in_progress';
        task.started_at = new Date().toISOString();
      } else if (eventData.type === 'task.completed') {
        task.status = 'completed';
        task.completed_at = new Date().toISOString();
      } else if (eventData.type === 'task.failed') {
        task.status = 'failed';
        task.error = eventData.data.error;
      }

      // Update chains when task status changes
      this.updateChains();
      this.draw();
    }
  }

  updateAgentStats(agentStats) {
    // Update agent status and stats from server
    for (const agentName in agentStats) {
      const agent = this.agents.find(a => a.name === agentName);
      if (agent) {
        const stats = agentStats[agentName];
        agent.status = stats.status;
        agent.currentTasks = stats.current_tasks || [];
        agent.queuedTasks = stats.queued_tasks || [];
        agent.completedTasks = stats.completed_tasks || 0;
        agent.failedTasks = stats.failed_tasks || 0;
        agent.totalExecutions = stats.total_executions || 0;
      }
    }

    // Update chains when agent stats change
    this.updateChains();
  }

  /**
   * Detect and update active task chains
   */
  updateChains() {
    if (!this.tasks || this.tasks.length === 0) {
      this.activeChains = [];
      return;
    }

    const chains = [];

    // Find all tasks that are part of chains (have input_task_ids)
    this.tasks.forEach(task => {
      if (task.input_task_ids && task.input_task_ids.length > 0) {
        // This task depends on other tasks - it's part of a chain
        task.input_task_ids.forEach(inputTaskId => {
          const inputTask = this.tasks.find(t => t.id === inputTaskId);
          if (inputTask) {
            // Found a link in the chain
            chains.push({
              from: inputTask,
              to: task,
              active: task.status === 'in_progress' || task.status === 'pending',
              completed: task.status === 'completed',
              failed: task.status === 'failed'
            });
          }
        });
      }
    });

    this.activeChains = chains;
  }

  /**
   * Create chain particles for active chains
   */
  createChainParticle(fromTask, toTask) {
    if (!fromTask || !toTask || fromTask.x == null || toTask.x == null) return;

    const particle = {
      x: fromTask.x,
      y: fromTask.y,
      targetX: toTask.x,
      targetY: toTask.y,
      progress: 0,
      speed: 0.01 + Math.random() * 0.01,
      alpha: 1,
      size: 4,
      color: toTask.status === 'in_progress' ? '#3b82f6' : '#6b7280'
    };

    this.chainParticles.push(particle);
  }

  /**
   * Update chain particles animation
   */
  updateChainParticles() {
    // Update existing particles
    this.chainParticles = this.chainParticles.filter(p => {
      p.progress += p.speed;
      p.x = p.x + (p.targetX - p.x) * p.progress;
      p.y = p.y + (p.targetY - p.y) * p.progress;
      p.alpha = 1 - p.progress;
      return p.progress < 1;
    });

    // Generate new particles for active chains
    this.activeChains.forEach(chain => {
      if (chain.active && !chain.completed && Math.random() < 0.1) {
        this.createChainParticle(chain.from, chain.to);
      }
    });
  }

  showNotification(message, type = 'info') {
    const notification = {
      id: Date.now() + Math.random(),
      message,
      type, // 'info', 'success', 'warning', 'error'
      timestamp: Date.now()
    };

    this.notifications.push(notification);

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
      this.dismissNotification(notification.id);
    }, 5000);

    this.draw();
  }

  dismissNotification(id) {
    this.notifications = this.notifications.filter(n => n.id !== id);
    this.draw();
  }

  addTimelineEvent(eventData) {
    // Add event to timeline (prepend to show newest first)
    this.timelineEvents.unshift({
      id: eventData.id || Date.now() + Math.random(),
      type: eventData.type,
      timestamp: eventData.timestamp || new Date().toISOString(),
      data: eventData.data || {},
      source: eventData.source || 'system'
    });

    // Limit timeline events to max
    if (this.timelineEvents.length > this.timelineMaxEvents) {
      this.timelineEvents = this.timelineEvents.slice(0, this.timelineMaxEvents);
    }

    this.draw();
  }

  addExecutionLog(taskId, type, message) {
    if (!this.executionLogs[taskId]) {
      this.executionLogs[taskId] = [];
    }

    this.executionLogs[taskId].push({
      type,
      message,
      timestamp: new Date()
    });

    // Limit logs per task to 50 entries
    if (this.executionLogs[taskId].length > 50) {
      this.executionLogs[taskId] = this.executionLogs[taskId].slice(-50);
    }

    this.draw();
  }

  toggleTimeline() {
    this.timelineVisible = !this.timelineVisible;
    this.timelinePanelAnimating = true;
    this.animateTimelinePanel(this.timelineVisible);
  }

  animateTimelinePanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.timelinePanelWidth = Math.min(
          this.timelinePanelWidth + speed,
          this.timelinePanelTargetWidth
        );

        if (this.timelinePanelWidth >= this.timelinePanelTargetWidth) {
          this.timelinePanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.timelinePanelWidth = Math.max(this.timelinePanelWidth - speed, 0);

        if (this.timelinePanelWidth <= 0) {
          this.timelinePanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      }

      this.draw();
    };

    requestAnimationFrame(animate);
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

    // Update chain particles
    this.updateChainParticles();

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

    // Draw chain connections (highlighted paths for active chains)
    this.drawChainConnections();

    // Draw task flows
    this.drawTaskFlows();

    // Draw particles
    this.drawParticles();

    // Draw chain particles
    this.drawChainParticles();

    // Draw agents
    this.drawAgents();

    this.ctx.restore();

    // Draw workspace progress OUTSIDE the transform context (so it stays fixed at top)
    this.drawWorkspaceProgress();

    // Draw mission OUTSIDE the transform context (so it stays fixed at top)
    this.drawMission();

    // Draw expanded task panel OUTSIDE the transform context (fixed position)
    if (this.expandedPanelWidth > 0) {
      this.drawExpandedTaskPanel();
    }

    // Draw expanded agent panel OUTSIDE the transform context (fixed position)
    if (this.expandedAgentPanelWidth > 0) {
      this.drawExpandedAgentPanel();
    }

    // Draw connection mode indicator
    if (this.connectionMode) {
      this.drawConnectionModeIndicator();
    }

    // Draw assignment line
    if (this.assignmentMode && this.assignmentSourceTask) {
      this.drawAssignmentLine();
    }

    // Draw create task form
    if (this.createTaskFormVisible) {
      this.drawCreateTaskForm();
    }

    // Draw create task button (always visible)
    this.drawCreateTaskButton();

    // Draw timeline panel (fixed position)
    if (this.timelinePanelWidth > 0) {
      this.drawTimelinePanel();
    }

    // Draw timeline toggle button (always visible)
    this.drawTimelineToggleButton();

    // Draw auto-layout button (always visible)
    this.drawAutoLayoutButton();

    // Draw toast notifications (always on top)
    this.drawNotifications();
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
        this.ctx.strokeStyle = fromAgent.color + '40';
        this.ctx.lineWidth = 2;
        this.ctx.setLineDash([5, 5]);
        this.ctx.beginPath();
        this.ctx.moveTo(fromAgent.x, fromAgent.y);
        this.ctx.lineTo(task.x, task.y);
        this.ctx.stroke();
        this.ctx.setLineDash([]);
      }

      // Draw connection line from task to receiver (skip for unassigned tasks)
      if (toAgent && !isUnassigned) {
        this.ctx.strokeStyle = toAgent.color + '40';
        this.ctx.lineWidth = 2;
        this.ctx.setLineDash([5, 5]);
        this.ctx.beginPath();
        this.ctx.moveTo(task.x, task.y);
        this.ctx.lineTo(toAgent.x, toAgent.y);
        this.ctx.stroke();
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

      // Task description (truncated)
      this.ctx.fillStyle = '#212529';
      this.ctx.font = 'bold 11px system-ui';
      const maxWidth = cardWidth - 16;
      let description = task.description || 'Task';
      if (description.length > 25) {
        description = description.substring(0, 22) + '...';
      }
      this.ctx.fillText(description, cardX + 8, cardY + 18);

      // Task status - show "UNASSIGNED" label for unassigned tasks
      this.ctx.fillStyle = '#6c757d';
      this.ctx.font = '9px system-ui';
      const statusText = isUnassigned ? '⚠️ UNASSIGNED' : `${task.from} → ${task.to}`;
      this.ctx.fillText(statusText, cardX + 8, cardY + 34);

      // Status badge
      this.ctx.fillStyle = borderColor;
      this.ctx.font = 'bold 8px system-ui';
      const badge = (task.status || 'pending').toUpperCase();
      const badgeWidth = this.ctx.measureText(badge).width + 8;
      this.ctx.fillRect(cardX + 8, cardY + 40, badgeWidth, 12);
      this.ctx.fillStyle = '#ffffff';
      this.ctx.fillText(badge, cardX + 12, cardY + 49);

      // Input indicator badge (if task receives input from other tasks)
      // Position at top-left corner for better visibility
      if (task.input_task_ids && task.input_task_ids.length > 0) {
        const inputBadgeX = cardX + 8;
        const inputBadgeY = cardY + 4;
        const inputBadgeText = `🔗 ${task.input_task_ids.length}`;
        this.ctx.font = 'bold 8px system-ui';
        const inputBadgeWidth = this.ctx.measureText(inputBadgeText).width + 8;
        const inputBadgeHeight = 14;

        // Background with rounded corners
        this.ctx.fillStyle = '#9b59b6'; // Purple to match connection lines
        this.ctx.strokeStyle = '#ffffff';
        this.ctx.lineWidth = 1.5;
        this.roundRect(inputBadgeX, inputBadgeY, inputBadgeWidth, inputBadgeHeight, 7);
        this.ctx.fill();
        this.ctx.stroke();

        // Text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 8px system-ui';
        this.ctx.textBaseline = 'middle';
        this.ctx.fillText(inputBadgeText, inputBadgeX + 4, inputBadgeY + inputBadgeHeight / 2);
        this.ctx.textBaseline = 'alphabetic';
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

      // "Can Connect" indicator for completed tasks with results
      if (task.status === 'completed' && task.result) {
        const indicatorSize = 24;
        const indicatorX = cardX + cardWidth - indicatorSize - 4;
        const indicatorY = cardY + cardHeight - indicatorSize - 4;

        // Store bounds for click detection
        task.connectionIndicatorBounds = {
          x: indicatorX,
          y: indicatorY,
          width: indicatorSize,
          height: indicatorSize
        };

        // Pulsing glow effect
        const pulsePhase = (Date.now() % 2000) / 2000; // 0 to 1
        const glowIntensity = 0.3 + 0.2 * Math.sin(pulsePhase * Math.PI * 2);

        this.ctx.save();
        this.ctx.globalAlpha = glowIntensity;
        this.ctx.fillStyle = '#9b59b6';
        this.ctx.beginPath();
        this.ctx.arc(indicatorX + indicatorSize / 2, indicatorY + indicatorSize / 2, indicatorSize / 2 + 3, 0, Math.PI * 2);
        this.ctx.fill();
        this.ctx.restore();

        // Main indicator background
        this.ctx.fillStyle = '#9b59b6';
        this.ctx.strokeStyle = '#ffffff';
        this.ctx.lineWidth = 2;
        this.ctx.beginPath();
        this.ctx.arc(indicatorX + indicatorSize / 2, indicatorY + indicatorSize / 2, indicatorSize / 2, 0, Math.PI * 2);
        this.ctx.fill();
        this.ctx.stroke();

        // Link icon
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 12px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.textBaseline = 'middle';
        this.ctx.fillText('🔗', indicatorX + indicatorSize / 2, indicatorY + indicatorSize / 2);
        this.ctx.textAlign = 'left';
        this.ctx.textBaseline = 'alphabetic';
      } else {
        // Clear bounds if task doesn't have result
        task.connectionIndicatorBounds = null;
      }

      // Execute button for pending tasks
      if (task.status === 'pending') {
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

      // Assign button (for all tasks except completed)
      if (task.status !== 'completed') {
        const assignBtnWidth = 50;
        const assignBtnHeight = 14;
        const assignBtnX = cardX + 6;
        const assignBtnY = cardY + 40;

        // Store button bounds for click detection
        task.assignBtnBounds = { x: assignBtnX, y: assignBtnY, width: assignBtnWidth, height: assignBtnHeight };

        // Button background (highlight if in assignment mode for this task)
        const isActiveAssignment = this.assignmentMode && this.assignmentSourceTask && this.assignmentSourceTask.id === task.id;
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
      const hasLogs = this.executionLogs[task.id] && this.executionLogs[task.id].length > 0;
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

        // Draw a more prominent line with glow effect to indicate result flow
        this.ctx.save();

        // Glow effect for visibility
        this.ctx.strokeStyle = '#9b59b6'; // Purple for result connections
        this.ctx.lineWidth = 6;
        this.ctx.globalAlpha = 0.3;
        this.ctx.setLineDash([12, 6]);
        this.ctx.beginPath();
        this.ctx.moveTo(inputTask.x, inputTask.y);
        this.ctx.lineTo(task.x, task.y);
        this.ctx.stroke();

        // Main line (more prominent)
        this.ctx.globalAlpha = 1.0;
        this.ctx.lineWidth = 3;
        this.ctx.strokeStyle = '#9b59b6';
        this.ctx.beginPath();
        this.ctx.moveTo(inputTask.x, inputTask.y);
        this.ctx.lineTo(task.x, task.y);
        this.ctx.stroke();
        this.ctx.setLineDash([]);
        this.ctx.restore();

        // Draw an arrow at the midpoint (larger and more visible)
        const midX = (inputTask.x + task.x) / 2;
        const midY = (inputTask.y + task.y) / 2;
        const angle = Math.atan2(task.y - inputTask.y, task.x - inputTask.x);

        // Draw arrow head with white border
        this.ctx.fillStyle = '#9b59b6';
        this.ctx.strokeStyle = '#ffffff';
        this.ctx.lineWidth = 2;
        const arrowSize = 12;
        this.ctx.beginPath();
        this.ctx.moveTo(midX + Math.cos(angle) * arrowSize, midY + Math.sin(angle) * arrowSize);
        this.ctx.lineTo(midX - Math.cos(angle - Math.PI / 6) * arrowSize, midY - Math.sin(angle - Math.PI / 6) * arrowSize);
        this.ctx.lineTo(midX - Math.cos(angle + Math.PI / 6) * arrowSize, midY - Math.sin(angle + Math.PI / 6) * arrowSize);
        this.ctx.closePath();
        this.ctx.fill();
        this.ctx.stroke();

        // Draw a more prominent label with background
        const labelText = '📊 Result';
        this.ctx.font = 'bold 10px system-ui';
        const labelWidth = this.ctx.measureText(labelText).width + 8;
        const labelX = midX - labelWidth / 2;
        const labelY = midY - 25;

        // Label background
        this.ctx.fillStyle = '#9b59b6';
        this.ctx.strokeStyle = '#ffffff';
        this.ctx.lineWidth = 2;
        this.roundRect(labelX, labelY, labelWidth, 16, 8);
        this.ctx.fill();
        this.ctx.stroke();

        // Label text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.textAlign = 'center';
        this.ctx.textBaseline = 'middle';
        this.ctx.fillText(labelText, midX, labelY + 8);
        this.ctx.textAlign = 'left';
        this.ctx.textBaseline = 'alphabetic';
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

  /**
   * Draw highlighted connection paths for active chains
   */
  drawChainConnections() {
    if (!this.activeChains || this.activeChains.length === 0) return;

    this.activeChains.forEach(chain => {
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
    if (!this.chainParticles || this.chainParticles.length === 0) return;

    this.chainParticles.forEach(p => {
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

      // Draw enhanced pulse effect for active/busy agents
      if (agent.status === 'active' || agent.status === 'busy') {
        const pulseSize = agent.radius + 15 * Math.sin(agent.pulsePhase);
        const pulseAlpha = 0.3 + 0.2 * Math.sin(agent.pulsePhase);

        // Outer pulse ring
        this.ctx.strokeStyle = agent.status === 'active' ? `rgba(16, 185, 129, ${pulseAlpha})` : `rgba(245, 158, 11, ${pulseAlpha})`;
        this.ctx.lineWidth = 3;
        this.ctx.beginPath();
        this.ctx.arc(agent.x, agent.y, pulseSize, 0, Math.PI * 2);
        this.ctx.stroke();

        // Inner glow
        this.ctx.fillStyle = agent.status === 'active' ? `rgba(16, 185, 129, ${pulseAlpha * 0.3})` : `rgba(245, 158, 11, ${pulseAlpha * 0.3})`;
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
        case 'active': statusColor = '#10b981'; break;  // Green - actively executing
        case 'busy': statusColor = '#f59e0b'; break;    // Orange - has queued tasks
        case 'error': statusColor = '#ef4444'; break;   // Red - error state
        case 'queued': statusColor = '#3b82f6'; break;  // Blue - tasks queued
        default: statusColor = '#6b7280';               // Gray - idle
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

      // Draw task count badge
      const currentTaskCount = agent.currentTasks?.length || 0;
      const queuedTaskCount = agent.queuedTasks?.length || 0;
      const totalTaskCount = currentTaskCount + queuedTaskCount;

      if (totalTaskCount > 0) {
        // Badge background
        const badgeX = agent.x + agent.radius - 5;
        const badgeY = agent.y + agent.radius - 5;
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
          this.ctx.fillText(taskText, agent.x, agent.y + agent.radius + 15);
        }
      }
    });
  }

  drawWorkspaceProgress() {
    if (!this.workspaceProgress || this.workspaceProgress.total_tasks === 0) return;

    const panelWidth = Math.min(600, this.width * 0.8);
    const panelHeight = 100;
    const panelX = 20;
    const panelY = 20;
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
    this.ctx.font = 'bold 12px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'top';
    this.ctx.fillText('📊 WORKSPACE PROGRESS', panelX + padding, panelY + padding);

    // Task status text
    const statsY = panelY + padding + 20;
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = '11px system-ui';
    let statusText = `${this.workspaceProgress.completed_tasks}/${this.workspaceProgress.total_tasks} tasks complete | ${this.workspaceProgress.in_progress_tasks} running | ${this.workspaceProgress.pending_tasks} pending`;
    if (this.workspaceProgress.failed_tasks > 0) {
      statusText += ` | ${this.workspaceProgress.failed_tasks} failed`;
    }
    this.ctx.fillText(statusText, panelX + padding, statsY);

    // Progress bar
    const progressBarY = panelY + padding + 38;
    const progressBarWidth = panelWidth - padding * 2;
    const progressBarHeight = 12;

    // Background
    this.ctx.fillStyle = '#e5e7eb';
    this.roundRect(panelX + padding, progressBarY, progressBarWidth, progressBarHeight, 6);
    this.ctx.fill();

    // Progress fill
    const fillWidth = (progressBarWidth * this.workspaceProgress.percentage) / 100;
    if (fillWidth > 0) {
      const gradient = this.ctx.createLinearGradient(panelX + padding, progressBarY, panelX + padding + fillWidth, progressBarY);
      gradient.addColorStop(0, '#10b981');
      gradient.addColorStop(1, '#059669');
      this.ctx.fillStyle = gradient;
      this.roundRect(panelX + padding, progressBarY, fillWidth, progressBarHeight, 6);
      this.ctx.fill();
    }

    // Percentage text on progress bar
    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 10px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.fillText(`${this.workspaceProgress.percentage}%`, panelX + padding + progressBarWidth / 2, progressBarY + 9);

    // Bottom row: Agent status and estimated time
    const bottomY = panelY + padding + 60;
    this.ctx.textAlign = 'left';

    // Agent status
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '10px system-ui';
    const agentText = `Agents: ${this.workspaceProgress.active_agents} active | ${this.workspaceProgress.idle_agents} idle`;
    this.ctx.fillText(agentText, panelX + padding, bottomY);

    // Estimated time remaining
    if (this.workspaceProgress.remaining_time_ms && this.workspaceProgress.remaining_time_ms > 0) {
      this.ctx.textAlign = 'right';
      const minutes = Math.ceil(this.workspaceProgress.remaining_time_ms / 60000);
      const seconds = Math.ceil((this.workspaceProgress.remaining_time_ms % 60000) / 1000);
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

    // Progress section (for in_progress tasks)
    if (this.expandedTask.status === 'in_progress') {
      this.ctx.fillStyle = '#3b82f6';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('⏳ Progress', contentX, currentY);
      currentY += 25;

      // Calculate elapsed time
      let elapsedMs = 0;
      if (this.expandedTask.started_at) {
        elapsedMs = Date.now() - new Date(this.expandedTask.started_at).getTime();
      }

      // Progress box
      const progressBoxHeight = 100;
      this.ctx.fillStyle = '#eff6ff';
      this.ctx.strokeStyle = '#3b82f6';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, currentY, this.expandedPanelWidth - padding * 2, progressBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      let progressY = currentY + 20;

      // Percentage or indeterminate
      const hasProgress = this.expandedTask.progress && this.expandedTask.progress.percentage !== undefined;
      if (hasProgress) {
        const percentage = this.expandedTask.progress.percentage;

        // Progress bar
        const barWidth = this.expandedPanelWidth - padding * 2 - 40;
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
        if (this.expandedTask.progress.current_step) {
          this.ctx.fillStyle = '#1e3a8a';
          this.ctx.font = '11px system-ui';
          const stepLines = this.wrapText(this.expandedTask.progress.current_step, this.expandedPanelWidth - padding * 2 - 40);
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
    this.ctx.lineTo(panelX + this.expandedPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Result section
    if (this.expandedTask.result) {
      this.ctx.fillStyle = '#059669';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('📊 Result', contentX, currentY);

      // Copy button
      const copyButtonWidth = 80;
      const copyButtonHeight = 24;
      const copyButtonX = panelX + this.expandedPanelWidth - padding - copyButtonWidth;
      const copyButtonY = currentY - 18;

      // Store bounds for click detection
      this.copyButtonBounds = {
        x: copyButtonX,
        y: copyButtonY,
        width: copyButtonWidth,
        height: copyButtonHeight
      };

      // Button background
      if (this.copyButtonState === 'copied') {
        this.ctx.fillStyle = '#10b981';
      } else if (this.copyButtonState === 'hover') {
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
      const buttonText = this.copyButtonState === 'copied' ? '✓ Copied!' : '📋 Copy';
      this.ctx.fillText(buttonText, copyButtonX + copyButtonWidth / 2, copyButtonY + copyButtonHeight / 2 + 4);
      this.ctx.textAlign = 'left';

      currentY += 25;

      // Result background box
      const resultBoxY = currentY;
      const resultBoxHeight = Math.min(300, panelHeight - currentY - padding);
      const resultBoxWidth = this.expandedPanelWidth - padding * 2;

      // Store bounds for scroll detection
      this.resultBoxBounds = {
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
      const resultLines = this.wrapText(this.expandedTask.result, resultBoxWidth - 40); // Extra padding for scrollbar
      const lineHeight = 14;
      const visibleLines = Math.floor((resultBoxHeight - 20) / lineHeight);
      const totalLines = resultLines.length;

      // Clamp scroll offset
      const maxScroll = Math.max(0, totalLines - visibleLines);
      this.resultScrollOffset = Math.max(0, Math.min(this.resultScrollOffset, maxScroll));

      // Enable clipping to prevent text overflow
      this.ctx.save();
      this.ctx.beginPath();
      this.ctx.rect(contentX + 5, resultBoxY + 5, resultBoxWidth - 10, resultBoxHeight - 10);
      this.ctx.clip();

      // Render visible lines based on scroll offset
      const startLine = Math.floor(this.resultScrollOffset);
      const endLine = Math.min(startLine + visibleLines + 1, totalLines);

      resultLines.slice(startLine, endLine).forEach((line, i) => {
        const yPos = resultBoxY + 15 + (i * lineHeight) - ((this.resultScrollOffset - startLine) * lineHeight);
        this.ctx.fillText(line, contentX + 10, yPos);
      });

      this.ctx.restore();

      // Draw scrollbar if content is scrollable
      if (totalLines > visibleLines) {
        const scrollbarWidth = 8;
        const scrollbarHeight = (visibleLines / totalLines) * (resultBoxHeight - 20);
        const scrollbarY = resultBoxY + 10 + (this.resultScrollOffset / maxScroll) * (resultBoxHeight - 20 - scrollbarHeight);

        // Scrollbar track
        this.ctx.fillStyle = 'rgba(16, 185, 129, 0.1)';
        this.ctx.fillRect(contentX + resultBoxWidth - scrollbarWidth - 5, resultBoxY + 10, scrollbarWidth, resultBoxHeight - 20);

        // Scrollbar thumb
        this.ctx.fillStyle = 'rgba(16, 185, 129, 0.5)';
        this.ctx.fillRect(contentX + resultBoxWidth - scrollbarWidth - 5, scrollbarY, scrollbarWidth, scrollbarHeight);
      }
    } else if (this.expandedTask.error) {
      this.resultBoxBounds = null;
      this.copyButtonBounds = null;
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
      this.resultBoxBounds = null;
      this.copyButtonBounds = null;
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
    const boxWidth = 450;
    const boxHeight = 80;

    // Draw semi-transparent background
    this.ctx.fillStyle = 'rgba(155, 89, 182, 0.95)'; // Purple to match connection theme
    this.ctx.strokeStyle = '#8e44ad';
    this.ctx.lineWidth = 3;
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
    this.ctx.fillText(`🔗 Connecting: "${this.connectionSourceTask.description.substring(0, 30)}..."`, centerX, centerY + 20);
    this.ctx.font = '13px system-ui';
    this.ctx.fillText('Click a task to link it  •  Click an agent to create new task', centerX, centerY + 45);
    this.ctx.font = '11px system-ui';
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
    this.ctx.fillText('Press ESC to cancel', centerX, centerY + 65);
  }

  drawExpandedAgentPanel() {
    if (!this.expandedAgent) return;

    const panelX = this.width - this.expandedAgentPanelWidth;
    const panelY = 0;
    const panelHeight = this.height;

    // Draw panel background with shadow
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.expandedAgentPanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.expandedAgentPanelWidth < 100) {
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
    this.ctx.fillText('×', panelX + this.expandedAgentPanelWidth - padding, currentY + 20);
    currentY += 40;

    // Agent title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 16px system-ui';
    this.ctx.textAlign = 'left';
    this.ctx.fillText('Agent Details', contentX, currentY);
    currentY += 30;

    // Status badge
    let statusColor = '#6b7280';
    if (this.expandedAgent.status === 'active') statusColor = '#10b981';
    else if (this.expandedAgent.status === 'busy') statusColor = '#f59e0b';

    this.ctx.fillStyle = statusColor;
    this.ctx.font = 'bold 10px system-ui';
    const statusText = (this.expandedAgent.status || 'idle').toUpperCase();
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
    this.ctx.fillText(this.expandedAgent.name, contentX, currentY);
    currentY += 25;

    // Agent color indicator
    this.ctx.fillStyle = this.expandedAgent.color;
    this.roundRect(contentX, currentY, 30, 30, 15);
    this.ctx.fill();
    currentY += 40;

    // Activity Statistics section
    this.ctx.fillStyle = '#3b82f6';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText('📊 Activity Statistics', contentX, currentY);
    currentY += 20;

    // Statistics grid
    const stats = [
      { label: 'Current Tasks', value: this.expandedAgent.currentTasks?.length || 0, color: '#10b981' },
      { label: 'Queued Tasks', value: this.expandedAgent.queuedTasks?.length || 0, color: '#3b82f6' },
      { label: 'Completed', value: this.expandedAgent.completedTasks || 0, color: '#6b7280' },
      { label: 'Failed', value: this.expandedAgent.failedTasks || 0, color: '#ef4444' },
    ];

    stats.forEach((stat, index) => {
      // Stat box
      const statBoxWidth = (this.expandedAgentPanelWidth - padding * 2 - 10) / 2;
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
    const totalExec = this.expandedAgent.totalExecutions || 0;
    this.ctx.fillText(`Total Executions: ${totalExec}`, contentX, currentY);
    currentY += 25;

    // Separator line
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.expandedAgentPanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 20;

    // Enabled Tools section
    if (this.expandedAgent.config && this.expandedAgent.config.enabled_plugins) {
      this.ctx.fillStyle = '#7c3aed';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('🔧 Enabled Tools', contentX, currentY);
      currentY += 20;

      const plugins = this.expandedAgent.config.enabled_plugins;
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
      this.ctx.lineTo(panelX + this.expandedAgentPanelWidth - padding, currentY);
      this.ctx.stroke();
      currentY += 20;
    }

    // System Prompt section
    if (this.expandedAgent.config && this.expandedAgent.config.system_prompt) {
      this.ctx.fillStyle = '#ea580c';
      this.ctx.font = 'bold 14px system-ui';
      this.ctx.fillText('💬 System Prompt', contentX, currentY);
      currentY += 20;

      // System prompt box
      const promptBoxY = currentY;
      const promptBoxHeight = 120;
      this.ctx.fillStyle = '#fff7ed';
      this.ctx.strokeStyle = '#ea580c';
      this.ctx.lineWidth = 2;
      this.roundRect(contentX, promptBoxY, this.expandedAgentPanelWidth - padding * 2, promptBoxHeight, 6);
      this.ctx.fill();
      this.ctx.stroke();

      // System prompt text
      this.ctx.fillStyle = '#7c2d12';
      this.ctx.font = '10px system-ui';
      const promptLines = this.wrapText(this.expandedAgent.config.system_prompt, this.expandedAgentPanelWidth - padding * 2 - 20);
      const maxPromptLines = 8;

      promptLines.slice(0, maxPromptLines).forEach((line, i) => {
        this.ctx.fillText(line, contentX + 10, promptBoxY + 15 + i * 13);
      });

      if (promptLines.length > maxPromptLines) {
        this.ctx.fillStyle = '#6b7280';
        this.ctx.font = 'italic 9px system-ui';
        this.ctx.fillText('... (view agent settings for full prompt)', contentX + 10, promptBoxY + promptBoxHeight - 10);
      }

      currentY += promptBoxHeight + 15;

      // Separator
      this.ctx.strokeStyle = '#e5e7eb';
      this.ctx.lineWidth = 1;
      this.ctx.beginPath();
      this.ctx.moveTo(contentX, currentY);
      this.ctx.lineTo(panelX + this.expandedAgentPanelWidth - padding, currentY);
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
    const taskCount = this.expandedAgent.tasks ? this.expandedAgent.tasks.length : 0;
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
      const tasksToShow = this.expandedAgent.tasks.slice(0, maxTasksToShow);

      tasksToShow.forEach((taskId, index) => {
        // Find the task details
        const task = this.tasks.find(t => t.id === taskId);
        if (task) {
          // Task background
          const taskBoxY = currentY;
          const taskBoxHeight = 45;
          this.ctx.fillStyle = '#f0fdf4';
          this.ctx.strokeStyle = '#10b981';
          this.ctx.lineWidth = 1;
          this.roundRect(contentX, taskBoxY, this.expandedAgentPanelWidth - padding * 2, taskBoxHeight, 6);
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

      if (this.expandedAgent.tasks.length > maxTasksToShow) {
        this.ctx.fillStyle = '#6b7280';
        this.ctx.font = 'italic 10px system-ui';
        this.ctx.fillText(`... and ${this.expandedAgent.tasks.length - maxTasksToShow} more`, contentX, currentY + 5);
      }
    }

    this.ctx.restore();
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

    // Track mouse position for assignment mode
    if (this.assignmentMode && this.assignmentSourceTask) {
      const x = (e.clientX - rect.left - this.offsetX) / this.scale;
      const y = (e.clientY - rect.top - this.offsetY) / this.scale;
      this.assignmentMouseX = x;
      this.assignmentMouseY = y;
      this.draw();
      return;
    }

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
      return;
    }

    // Check hover over copy button (screen coordinates, not scaled)
    if (this.copyButtonBounds) {
      const mouseX = e.clientX - rect.left;
      const mouseY = e.clientY - rect.top;
      const bounds = this.copyButtonBounds;

      const isHovering = mouseX >= bounds.x && mouseX <= bounds.x + bounds.width &&
                        mouseY >= bounds.y && mouseY <= bounds.y + bounds.height;

      const prevState = this.copyButtonState;
      if (isHovering && this.copyButtonState === 'idle') {
        this.copyButtonState = 'hover';
        this.canvas.style.cursor = 'pointer';
        this.draw();
      } else if (!isHovering && this.copyButtonState === 'hover') {
        this.copyButtonState = 'idle';
        this.canvas.style.cursor = 'grab';
        this.draw();
      }
    }
  }

  onMouseUp() {
    this.isDragging = false;
    this.isDraggingAgent = false;
    this.draggedAgent = null;
    this.isDraggingTask = false;
    this.draggedTask = null;

    // Preserve cursor state for assignment/connection modes
    if (this.assignmentMode) {
      this.canvas.style.cursor = 'crosshair';
    } else if (this.connectionMode) {
      this.canvas.style.cursor = 'crosshair';
    } else {
      this.canvas.style.cursor = 'grab';
    }
  }

  onWheel(e) {
    e.preventDefault();

    // Check if mouse is over result box for scrolling
    if (this.resultBoxBounds && this.expandedTask) {
      const rect = this.canvas.getBoundingClientRect();
      const mouseX = e.clientX - rect.left;
      const mouseY = e.clientY - rect.top;

      const bounds = this.resultBoxBounds;
      if (mouseX >= bounds.x && mouseX <= bounds.x + bounds.width &&
          mouseY >= bounds.y && mouseY <= bounds.y + bounds.height) {
        // Scroll the result content
        const scrollAmount = e.deltaY > 0 ? 3 : -3; // Scroll 3 lines at a time
        this.resultScrollOffset += scrollAmount;
        this.draw();
        return;
      }
    }

    // Otherwise, zoom the canvas
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

    // Check for clicks on create task form (highest priority when visible)
    if (this.createTaskFormVisible) {
      // Check close button
      if (this.createTaskCloseButtonBounds) {
        const btn = this.createTaskCloseButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.hideCreateTaskForm();
          return;
        }
      }

      // Check cancel button
      if (this.createTaskCancelButtonBounds) {
        const btn = this.createTaskCancelButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.hideCreateTaskForm();
          return;
        }
      }

      // Check submit button
      if (this.createTaskSubmitButtonBounds) {
        const btn = this.createTaskSubmitButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.submitCreateTaskForm();
          return;
        }
      }

      // Check checkbox
      if (this.createTaskCheckboxBounds) {
        const cb = this.createTaskCheckboxBounds;
        if (screenX >= cb.x && screenX <= cb.x + cb.width &&
            screenY >= cb.y && screenY <= cb.y + cb.height) {
          this.createTaskAssignToAgent = !this.createTaskAssignToAgent;
          if (!this.createTaskAssignToAgent) {
            this.selectedAgentForTask = null;
          }
          this.draw();
          return;
        }
      }

      // Check agent selection buttons
      if (this.createTaskAssignToAgent && this.agentSelectionBounds) {
        for (const bounds of this.agentSelectionBounds) {
          if (bounds && screenX >= bounds.x && screenX <= bounds.x + bounds.width &&
              screenY >= bounds.y && screenY <= bounds.y + bounds.height) {
            this.selectedAgentForTask = bounds.agentName;
            this.draw();
            return;
          }
        }
      }

      // Check description field - show browser prompt for text input
      if (this.createTaskDescriptionBounds) {
        const input = this.createTaskDescriptionBounds;
        if (screenX >= input.x && screenX <= input.x + input.width &&
            screenY >= input.y && screenY <= input.y + input.height) {
          const description = prompt('Enter task description:', this.createTaskDescription || '');
          if (description !== null) {
            this.createTaskDescription = description;
            this.draw();
          }
          return;
        }
      }

      // Click outside form - close it
      if (this.createTaskFormBounds) {
        const form = this.createTaskFormBounds;
        if (screenX < form.x || screenX > form.x + form.width ||
            screenY < form.y || screenY > form.y + form.height) {
          this.hideCreateTaskForm();
          return;
        }
      }

      // Click inside form but not on any interactive element - do nothing
      return;
    }

    // Check for click on "Create Task" button
    if (this.createTaskButtonBounds) {
      const btn = this.createTaskButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.showCreateTaskForm();
        return;
      }
    }

    // Check for click on "Timeline" toggle button
    if (this.timelineToggleBounds) {
      const btn = this.timelineToggleBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.toggleTimeline();
        return;
      }
    }

    // Check for click on "Auto-Layout" button
    if (this.autoLayoutButtonBounds) {
      const btn = this.autoLayoutButtonBounds;
      if (screenX >= btn.x && screenX <= btn.x + btn.width &&
          screenY >= btn.y && screenY <= btn.y + btn.height) {
        this.autoLayoutTasks();
        return;
      }
    }

    // Check for click on timeline panel close button
    if (this.timelinePanelWidth > 0) {
      const panelX = this.width - this.timelinePanelWidth;
      const closeButtonX = panelX + this.timelinePanelWidth - 30;
      const closeButtonY = 15;
      const closeButtonSize = 30;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.toggleTimeline();
        return;
      }
    }

    // Check if click is on close button of expanded agent panel
    if (this.expandedAgentPanelWidth > 0) {
      const panelX = this.width - this.expandedAgentPanelWidth;
      const closeButtonX = panelX + this.expandedAgentPanelWidth - 40;
      const closeButtonY = 30;
      const closeButtonSize = 40;

      if (screenX >= closeButtonX && screenX <= closeButtonX + closeButtonSize &&
          screenY >= closeButtonY && screenY <= closeButtonY + closeButtonSize) {
        this.closeAgentPanel();
        return;
      }

      // If clicking anywhere on the agent panel, don't process other clicks
      if (screenX >= panelX) {
        return;
      }
    }

    // Check if click is on close button of expanded task panel
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

      // Check if click is on copy button
      if (this.copyButtonBounds) {
        const btn = this.copyButtonBounds;
        if (screenX >= btn.x && screenX <= btn.x + btn.width &&
            screenY >= btn.y && screenY <= btn.y + btn.height) {
          this.copyResultToClipboard();
          return;
        }
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

        // Check if click is on connection indicator (purple pulsing icon)
        if (task.connectionIndicatorBounds && task.status === 'completed' && task.result) {
          const btn = task.connectionIndicatorBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Connection indicator clicked - enter connection mode
            this.connectionMode = true;
            this.connectionSourceTask = task;
            this.canvas.style.cursor = 'crosshair';
            this.draw();
            return;
          }
        }

        // Check if click is on delete button first
        if (task.deleteBtnBounds) {
          const btn = task.deleteBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Delete button clicked
            this.deleteTask(task);
            return;
          }
        }

        // Check if click is on execute button
        if (task.executeBtnBounds && task.status === 'pending') {
          const btn = task.executeBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Execute button clicked
            this.executeTask(task);
            return;
          }
        }

        // Check if click is on assign button
        if (task.assignBtnBounds && task.status !== 'completed') {
          const btn = task.assignBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // Assign button clicked - toggle assignment mode
            this.toggleAssignmentMode(task);
            return;
          }
        }

        // Check if click is on view log button
        if (task.viewLogBtnBounds) {
          const btn = task.viewLogBtnBounds;
          if (x >= btn.x && x <= btn.x + btn.width &&
              y >= btn.y && y <= btn.y + btn.height) {
            // View log button clicked - show execution log modal
            this.showExecutionLog(task);
            return;
          }
        }

        if (x >= cardX && x <= cardX + cardWidth &&
            y >= cardY && y <= cardY + cardHeight) {
          // Check if in connection mode
          if (this.connectionMode && this.connectionSourceTask) {
            // In connection mode - link this task to receive input from source task
            // Don't link to itself
            if (task.id === this.connectionSourceTask.id) {
              alert('⚠️ Cannot connect a task to itself. Please select a different task.');
              return;
            }
            // Connect this task to receive input
            this.connectToExistingTask(task);
            return;
          }

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
        console.log('Agent clicked:', agent.name, 'assignmentMode:', this.assignmentMode);
        if (this.assignmentMode && this.assignmentSourceTask) {
          // In assignment mode - assign task to agent
          console.log('Assigning task to agent:', agent.name);
          this.assignTaskToAgent(agent);
          return;
        } else if (this.connectionMode && this.connectionSourceTask) {
          // In connection mode - create task with result linked
          this.createConnectedTask(agent);
          return;
        } else {
          // Toggle agent panel
          this.toggleAgentPanel(agent);
        }
        return;
      }
    }

    // Click on empty space - close expanded panels
    if (this.expandedTask) {
      this.closeTaskPanel();
    }
    if (this.expandedAgent) {
      this.closeAgentPanel();
    }
  }

  toggleTaskPanel(task) {
    if (this.expandedTask && this.expandedTask.id === task.id) {
      // Clicking the same task - close panel
      this.closeTaskPanel();
    } else {
      // Expand panel for this task
      this.expandedTask = task;
      this.resultScrollOffset = 0; // Reset scroll when opening a new task
      this.copyButtonState = 'idle'; // Reset copy button state
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
          this.resultScrollOffset = 0; // Reset scroll when closing panel
        } else {
          requestAnimationFrame(animate);
        }
      }
    };

    animate();
  }

  async toggleAgentPanel(agent) {
    // Close task panel if open
    if (this.expandedTask) {
      this.closeTaskPanel();
    }

    if (this.expandedAgent && this.expandedAgent.name === agent.name) {
      // Clicking the same agent - close panel
      this.closeAgentPanel();
    } else {
      // Fetch agent configuration before expanding (optional - doesn't block panel)
      try {
        const configResponse = await fetch(`/api/agents/${agent.name}`);
        if (configResponse.ok) {
          const agentConfig = await configResponse.json();
          // Merge config data with agent
          this.expandedAgent = {
            ...agent,
            config: agentConfig
          };
        } else {
          // Use agent without detailed config if fetch fails (workspace agents may not be in global store)
          console.log(`Agent ${agent.name} config not found in global store - using workspace data`);
          this.expandedAgent = {
            ...agent,
            config: null
          };
        }
      } catch (error) {
        console.log('Using workspace agent data without global config:', error.message);
        this.expandedAgent = {
          ...agent,
          config: null
        };
      }

      this.expandedAgentPanelAnimating = true;
      this.animateAgentPanel(true);
    }
  }

  closeAgentPanel() {
    this.expandedAgentPanelAnimating = true;
    this.animateAgentPanel(false);
  }

  animateAgentPanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.expandedAgentPanelWidth = Math.min(
          this.expandedAgentPanelWidth + speed,
          this.expandedAgentPanelTargetWidth
        );

        if (this.expandedAgentPanelWidth >= this.expandedAgentPanelTargetWidth) {
          this.expandedAgentPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.expandedAgentPanelWidth = Math.max(this.expandedAgentPanelWidth - speed, 0);

        if (this.expandedAgentPanelWidth <= 0) {
          this.expandedAgentPanelAnimating = false;
          this.expandedAgent = null;
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

  async copyResultToClipboard() {
    if (!this.expandedTask || !this.expandedTask.result) {
      return;
    }

    try {
      // Use the Clipboard API to copy text
      await navigator.clipboard.writeText(this.expandedTask.result);

      // Update button state to show success
      this.copyButtonState = 'copied';
      this.draw();

      // Reset button state after 2 seconds
      setTimeout(() => {
        if (this.copyButtonState === 'copied') {
          this.copyButtonState = 'idle';
          this.draw();
        }
      }, 2000);

      console.log('✓ Result copied to clipboard');
    } catch (error) {
      console.error('Failed to copy to clipboard:', error);
      // Fallback: show error notification
      alert('Failed to copy to clipboard. Please try selecting and copying the text manually.');
    }
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
          studio_id: this.studioId,  // Backend expects 'studio_id', not 'workspace_id'
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

  async connectToExistingTask(targetTask) {
    // Connect an existing task to receive input from the source task
    try {
      // Add the source task ID to the target task's input_task_ids
      const currentInputs = targetTask.input_task_ids || [];

      // Check if already connected
      if (currentInputs.includes(this.connectionSourceTask.id)) {
        alert(`⚠️ This task is already connected to "${this.connectionSourceTask.description}"`);
        this.connectionMode = false;
        this.connectionSourceTask = null;
        this.canvas.style.cursor = 'grab';
        this.draw();
        return;
      }

      const updatedInputs = [...currentInputs, this.connectionSourceTask.id];

      // Update task via API
      const response = await fetch('/api/orchestration/tasks/update', {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          task_id: targetTask.id,
          input_task_ids: updatedInputs,
        }),
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(`Failed to connect task: ${errorText}`);
      }

      console.log(`✅ Connected task ${targetTask.id} to receive input from ${this.connectionSourceTask.id}`);

      // Exit connection mode
      this.connectionMode = false;
      this.connectionSourceTask = null;
      this.canvas.style.cursor = 'grab';

      // Reload studio data to show new connection
      await this.init();

      // Show success message
      alert(`✅ Task connected!\n\n"${targetTask.description}" will now receive the result from "${this.connectionSourceTask.description}"`);
    } catch (error) {
      console.error('❌ Error connecting task:', error);
      alert('Failed to connect task: ' + error.message);
      this.connectionMode = false;
      this.connectionSourceTask = null;
      this.canvas.style.cursor = 'grab';
      this.draw();
    }
  }

  toggleAssignmentMode(task) {
    console.log('toggleAssignmentMode called for task:', task.id);
    if (this.assignmentMode && this.assignmentSourceTask && this.assignmentSourceTask.id === task.id) {
      // Cancel assignment mode
      console.log('Exiting assignment mode');
      this.assignmentMode = false;
      this.assignmentSourceTask = null;
      this.assignmentMouseX = 0;
      this.assignmentMouseY = 0;
      this.canvas.style.cursor = 'grab';
    } else {
      // Enter assignment mode
      console.log('Entering assignment mode for task:', task.id);
      this.assignmentMode = true;
      this.assignmentSourceTask = task;
      this.canvas.style.cursor = 'crosshair';
    }
    this.draw();
  }

  drawAssignmentLine() {
    // Draw line from task to cursor
    this.ctx.save();
    this.ctx.translate(this.offsetX, this.offsetY);
    this.ctx.scale(this.scale, this.scale);

    // Draw line
    this.ctx.strokeStyle = '#fd7e14';
    this.ctx.lineWidth = 3;
    this.ctx.setLineDash([10, 5]);
    this.ctx.beginPath();
    this.ctx.moveTo(this.assignmentSourceTask.x, this.assignmentSourceTask.y);
    this.ctx.lineTo(this.assignmentMouseX, this.assignmentMouseY);
    this.ctx.stroke();
    this.ctx.setLineDash([]);

    // Draw arrow at cursor
    const angle = Math.atan2(
      this.assignmentMouseY - this.assignmentSourceTask.y,
      this.assignmentMouseX - this.assignmentSourceTask.x
    );
    const arrowSize = 15;
    this.ctx.fillStyle = '#fd7e14';
    this.ctx.beginPath();
    this.ctx.moveTo(this.assignmentMouseX, this.assignmentMouseY);
    this.ctx.lineTo(
      this.assignmentMouseX - arrowSize * Math.cos(angle - Math.PI / 6),
      this.assignmentMouseY - arrowSize * Math.sin(angle - Math.PI / 6)
    );
    this.ctx.lineTo(
      this.assignmentMouseX - arrowSize * Math.cos(angle + Math.PI / 6),
      this.assignmentMouseY - arrowSize * Math.sin(angle + Math.PI / 6)
    );
    this.ctx.closePath();
    this.ctx.fill();

    this.ctx.restore();
  }

  /**
   * Auto-layout tasks in a hierarchical flow (top to bottom)
   */
  autoLayoutTasks() {
    if (!this.tasks || this.tasks.length === 0) return;

    // Calculate dependency levels (topological sort)
    const levels = this.calculateTaskLevels();

    // Position tasks in hierarchical layout
    const startY = 100;
    const levelSpacing = 180;
    const taskSpacing = 220;

    levels.forEach((taskGroup, levelIndex) => {
      const y = startY + (levelIndex * levelSpacing);
      const totalWidth = taskGroup.length * taskSpacing;
      const startX = (this.width / this.scale) / 2 - totalWidth / 2;

      taskGroup.forEach((task, taskIndex) => {
        const x = startX + (taskIndex * taskSpacing);
        task.x = x;
        task.y = y;
      });
    });

    this.draw();
    this.showNotification('✨ Tasks auto-arranged', 'success');
  }

  /**
   * Calculate task dependency levels using topological sort
   */
  calculateTaskLevels() {
    const levels = [];
    const visited = new Set();
    const taskMap = new Map(this.tasks.map(t => [t.id, t]));

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
    this.tasks.forEach(task => getLevel(task));

    // Group tasks by level
    const maxLevel = Math.max(...this.tasks.map(t => t.level || 0));
    for (let i = 0; i <= maxLevel; i++) {
      levels[i] = this.tasks.filter(t => (t.level || 0) === i);
    }

    return levels;
  }

  async assignTaskToAgent(agent) {
    // Update task assignment via API
    try {
      const response = await fetch(`/api/orchestration/tasks`, {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          task_id: this.assignmentSourceTask.id,
          to: agent.name
        }),
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(`Failed to assign task: ${errorText}`);
      }

      const result = await response.json();
      console.log('✅ Task assigned:', result);

      // Exit assignment mode
      this.assignmentMode = false;
      this.assignmentSourceTask = null;
      this.assignmentMouseX = 0;
      this.assignmentMouseY = 0;
      this.canvas.style.cursor = 'grab';

      // Update task locally
      const task = this.tasks.find(t => t.id === result.id);
      if (task) {
        task.to = agent.name;
      }

      this.draw();

      // Show success notification
      this.addNotification(`✅ Task assigned to ${agent.name}`, 'success');
    } catch (error) {
      console.error('❌ Error assigning task:', error);
      this.addNotification('Failed to assign task: ' + error.message, 'error');
      this.assignmentMode = false;
      this.assignmentSourceTask = null;
      this.assignmentMouseX = 0;
      this.assignmentMouseY = 0;
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

  /**
   * Draw the floating "Create Task" button in the top-right corner
   */
  drawCreateTaskButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.width - buttonWidth - 20;
    const buttonY = 20;

    // Store button bounds for click detection
    this.createTaskButtonBounds = {
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

  /**
   * Draw toast notifications
   */
  drawNotifications() {
    if (!this.notifications || this.notifications.length === 0) return;

    const notificationWidth = 320;
    const notificationHeight = 70;
    const padding = 15;
    const spacing = 10;

    this.ctx.save();

    this.notifications.forEach((notification, index) => {
      const x = this.width - notificationWidth - 20;
      const y = this.height - (notificationHeight + spacing) * (index + 1) - 80;

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

  /**
   * Draw timeline toggle button
   */
  drawTimelineToggleButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.width - buttonWidth - 20;
    const buttonY = 70; // Below create task button

    // Store button bounds for click detection
    this.timelineToggleBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Button background - different color if timeline is open
    this.ctx.fillStyle = this.timelineVisible ? '#059669' : '#6b7280';
    this.ctx.strokeStyle = this.timelineVisible ? '#047857' : '#4b5563';
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
    const text = this.timelineVisible ? '📋 Hide Timeline' : '📋 Timeline';
    this.ctx.fillText(text, buttonX + buttonWidth / 2, buttonY + buttonHeight / 2);
    this.ctx.textAlign = 'left';
    this.ctx.textBaseline = 'alphabetic';
  }

  /**
   * Draw auto-layout button
   */
  drawAutoLayoutButton() {
    const buttonWidth = 140;
    const buttonHeight = 40;
    const buttonX = this.width - buttonWidth - 20;
    const buttonY = 120; // Below timeline button

    // Store button bounds for click detection
    this.autoLayoutButtonBounds = {
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

  /**
   * Draw timeline panel
   */
  drawTimelinePanel() {
    if (!this.timelineEvents || this.timelineEvents.length === 0) {
      // Show empty state
      this.drawEmptyTimeline();
      return;
    }

    const panelX = this.width - this.timelinePanelWidth;
    const panelY = 0;
    const panelHeight = this.height;
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.timelinePanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    // Only draw content if panel is mostly visible
    if (this.timelinePanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.timelinePanelWidth - padding, currentY + 20);
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
    this.ctx.fillText(`${this.timelineEvents.length} recent events`, contentX, currentY);
    currentY += 25;

    // Separator
    this.ctx.strokeStyle = '#e5e7eb';
    this.ctx.lineWidth = 1;
    this.ctx.beginPath();
    this.ctx.moveTo(contentX, currentY);
    this.ctx.lineTo(panelX + this.timelinePanelWidth - padding, currentY);
    this.ctx.stroke();
    currentY += 15;

    // Draw events
    const maxVisibleEvents = Math.floor((panelHeight - currentY - 20) / 70);
    const visibleEvents = this.timelineEvents.slice(0, maxVisibleEvents);

    visibleEvents.forEach((event, index) => {
      this.drawTimelineEvent(event, contentX, currentY, this.timelinePanelWidth - padding * 2);
      currentY += 70;
    });

    this.ctx.restore();
  }

  /**
   * Draw empty timeline state
   */
  drawEmptyTimeline() {
    const panelX = this.width - this.timelinePanelWidth;
    const panelY = 0;
    const panelHeight = this.height;
    const padding = 15;

    this.ctx.save();

    // Panel background
    this.ctx.fillStyle = '#ffffff';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.ctx.shadowOffsetX = -5;
    this.ctx.fillRect(panelX, panelY, this.timelinePanelWidth, panelHeight);
    this.ctx.shadowColor = 'transparent';

    if (this.timelinePanelWidth < 100) {
      this.ctx.restore();
      return;
    }

    const contentX = panelX + padding;
    let currentY = padding + 10;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', panelX + this.timelinePanelWidth - padding, currentY + 20);
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
    this.ctx.fillText('No activity yet', panelX + this.timelinePanelWidth / 2, currentY);
    this.ctx.fillText('Events will appear here', panelX + this.timelinePanelWidth / 2, currentY + 20);

    this.ctx.restore();
  }

  /**
   * Draw a single timeline event
   */
  drawTimelineEvent(event, x, y, width) {
    const icon = this.getEventIcon(event.type);
    const message = this.getEventMessage(event);
    const time = new Date(event.timestamp).toLocaleTimeString();

    // Icon
    this.ctx.font = '18px system-ui';
    this.ctx.fillStyle = this.getEventColor(event.type);
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

  /**
   * Get icon for event type
   */
  getEventIcon(type) {
    const icons = {
      'task.created': '📋',
      'task.started': '⏳',
      'task.completed': '✓',
      'task.failed': '❌',
      'task.timeout': '⏰',
      'task.deleted': '🗑️',
      'workspace.progress': '📊',
      'agent.active': '🔥',
      'agent.idle': '💤',
      'workflow.started': '🔗',
      'workflow.completed': '✅',
      'workflow.failed': '💥'
    };
    return icons[type] || '•';
  }

  /**
   * Get color for event type
   */
  getEventColor(type) {
    if (type.includes('failed') || type.includes('error')) return '#ef4444';
    if (type.includes('completed')) return '#10b981';
    if (type.includes('started')) return '#3b82f6';
    if (type.includes('deleted')) return '#6b7280';
    return '#6b7280';
  }

  /**
   * Get formatted message for event
   */
  getEventMessage(event) {
    const desc = event.data.description || event.data.task_id || '';
    const truncDesc = desc.length > 40 ? desc.substring(0, 37) + '...' : desc;

    switch (event.type) {
      case 'task.created':
        return `Task created: ${truncDesc}`;
      case 'task.started':
        return `Task started: ${truncDesc}`;
      case 'task.completed':
        return `Task completed: ${truncDesc}`;
      case 'task.failed':
        return `Task failed: ${truncDesc}`;
      case 'task.deleted':
        return `Task deleted: ${truncDesc}`;
      case 'workspace.progress':
        return 'Workspace progress updated';
      case 'agent.active':
        return `Agent ${event.data.agent} is now active`;
      case 'agent.idle':
        return `Agent ${event.data.agent} is now idle`;
      default:
        return event.type.replace('.', ' ').replace(/_/g, ' ');
    }
  }

  /**
   * Draw the create task form modal
   */
  drawCreateTaskForm() {
    const formWidth = 400;
    const formHeight = 420;
    const formX = (this.width - formWidth) / 2;
    const formY = (this.height - formHeight) / 2;

    // Semi-transparent backdrop
    this.ctx.fillStyle = 'rgba(0, 0, 0, 0.5)';
    this.ctx.fillRect(0, 0, this.width, this.height);

    // Form background
    this.ctx.save();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.strokeStyle = '#3b82f6';
    this.ctx.lineWidth = 3;
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.3)';
    this.ctx.shadowBlur = 20;
    this.roundRect(formX, formY, formWidth, formHeight, 12);
    this.ctx.fill();
    this.ctx.stroke();
    this.ctx.shadowColor = 'transparent';

    // Store form bounds for interaction
    this.createTaskFormBounds = {
      x: formX,
      y: formY,
      width: formWidth,
      height: formHeight
    };

    const padding = 20;
    let currentY = formY + padding;

    // Title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 20px system-ui';
    this.ctx.fillText('Create New Task', formX + padding, currentY + 20);
    currentY += 50;

    // Close button
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = 'bold 24px system-ui';
    this.ctx.textAlign = 'right';
    this.ctx.fillText('×', formX + formWidth - padding, formY + padding + 20);
    this.ctx.textAlign = 'left';

    // Store close button bounds
    this.createTaskCloseButtonBounds = {
      x: formX + formWidth - padding - 30,
      y: formY + padding,
      width: 30,
      height: 30
    };

    // Note about canvas-based forms
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '12px system-ui';
    const noteText = 'Note: Please use the dashboard below to create tasks with more options.';
    const noteLines = this.wrapText(noteText, formWidth - padding * 2);
    noteLines.forEach((line, i) => {
      this.ctx.fillText(line, formX + padding, currentY + i * 16);
    });
    currentY += noteLines.length * 16 + 20;

    // Quick task section
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText('Quick Task (Unassigned)', formX + padding, currentY);
    currentY += 25;

    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '12px system-ui';
    this.ctx.fillText('Creates a task without assigning to a specific agent.', formX + padding, currentY);
    currentY += 25;

    // Description field label
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = 'bold 12px system-ui';
    this.ctx.fillText('Task Description:', formX + padding, currentY);
    currentY += 20;

    // Description field background
    const inputHeight = 80;
    this.ctx.fillStyle = '#f3f4f6';
    this.ctx.strokeStyle = '#d1d5db';
    this.ctx.lineWidth = 1;
    this.roundRect(formX + padding, currentY, formWidth - padding * 2, inputHeight, 6);
    this.ctx.fill();
    this.ctx.stroke();

    // Placeholder text (if no description entered yet)
    if (!this.createTaskDescription || this.createTaskDescription.trim() === '') {
      this.ctx.fillStyle = '#9ca3af';
      this.ctx.font = 'italic 12px system-ui';
      this.ctx.fillText('Enter task description...', formX + padding + 10, currentY + 20);
    } else {
      // Show entered text
      this.ctx.fillStyle = '#1f2937';
      this.ctx.font = '12px system-ui';
      const descLines = this.wrapText(this.createTaskDescription, formWidth - padding * 2 - 20);
      descLines.slice(0, 5).forEach((line, i) => {
        this.ctx.fillText(line, formX + padding + 10, currentY + 18 + i * 15);
      });
    }

    // Store description input bounds
    this.createTaskDescriptionBounds = {
      x: formX + padding,
      y: currentY,
      width: formWidth - padding * 2,
      height: inputHeight
    };
    currentY += inputHeight + 20;

    // Assign to agent checkbox section
    this.ctx.fillStyle = '#4b5563';
    this.ctx.font = 'bold 12px system-ui';

    // Checkbox
    const checkboxSize = 16;
    const checkboxX = formX + padding;
    const checkboxY = currentY;

    this.ctx.strokeStyle = '#d1d5db';
    this.ctx.lineWidth = 2;
    this.ctx.strokeRect(checkboxX, checkboxY, checkboxSize, checkboxSize);

    if (this.createTaskAssignToAgent) {
      // Draw checkmark
      this.ctx.fillStyle = '#3b82f6';
      this.ctx.fillRect(checkboxX + 2, checkboxY + 2, checkboxSize - 4, checkboxSize - 4);
    }

    // Store checkbox bounds
    this.createTaskCheckboxBounds = {
      x: checkboxX,
      y: checkboxY,
      width: checkboxSize,
      height: checkboxSize
    };

    this.ctx.fillStyle = '#4b5563';
    this.ctx.fillText('Assign to specific agent', checkboxX + checkboxSize + 10, checkboxY + 12);
    currentY += 30;

    // Agent selection (if checkbox is checked)
    if (this.createTaskAssignToAgent && this.agents && this.agents.length > 0) {
      this.ctx.fillStyle = '#4b5563';
      this.ctx.font = '11px system-ui';
      this.ctx.fillText('Select agent:', formX + padding, currentY);
      currentY += 18;

      // Draw agent selection buttons
      const agentButtonHeight = 30;
      this.agents.forEach((agent, index) => {
        const isSelected = this.selectedAgentForTask === agent.name;

        this.ctx.fillStyle = isSelected ? '#3b82f6' : '#f3f4f6';
        this.ctx.strokeStyle = isSelected ? '#1e40af' : '#d1d5db';
        this.ctx.lineWidth = 2;
        this.roundRect(formX + padding, currentY, formWidth - padding * 2, agentButtonHeight, 6);
        this.ctx.fill();
        this.ctx.stroke();

        this.ctx.fillStyle = isSelected ? '#ffffff' : '#1f2937';
        this.ctx.font = '12px system-ui';
        this.ctx.fillText(agent.name, formX + padding + 10, currentY + 19);

        // Store agent button bounds
        if (!this.agentSelectionBounds) this.agentSelectionBounds = [];
        this.agentSelectionBounds[index] = {
          x: formX + padding,
          y: currentY,
          width: formWidth - padding * 2,
          height: agentButtonHeight,
          agentName: agent.name
        };

        currentY += agentButtonHeight + 5;
      });
      currentY += 10;
    }

    // Create button
    const buttonWidth = 120;
    const buttonHeight = 36;
    const buttonX = formX + formWidth - padding - buttonWidth - 100;
    const buttonY = formY + formHeight - padding - buttonHeight - 10;

    this.ctx.fillStyle = '#3b82f6';
    this.ctx.strokeStyle = '#1e40af';
    this.ctx.lineWidth = 2;
    this.roundRect(buttonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();

    this.ctx.fillStyle = '#ffffff';
    this.ctx.font = 'bold 13px system-ui';
    this.ctx.textAlign = 'center';
    this.ctx.fillText('Create Task', buttonX + buttonWidth / 2, buttonY + buttonHeight / 2 + 1);
    this.ctx.textAlign = 'left';

    // Store create button bounds
    this.createTaskSubmitButtonBounds = {
      x: buttonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    // Cancel button
    const cancelButtonX = buttonX + buttonWidth + 10;
    this.ctx.fillStyle = '#6b7280';
    this.ctx.strokeStyle = '#4b5563';
    this.roundRect(cancelButtonX, buttonY, buttonWidth, buttonHeight, 8);
    this.ctx.fill();
    this.ctx.stroke();

    this.ctx.fillStyle = '#ffffff';
    this.ctx.textAlign = 'center';
    this.ctx.fillText('Cancel', cancelButtonX + buttonWidth / 2, buttonY + buttonHeight / 2 + 1);
    this.ctx.textAlign = 'left';

    // Store cancel button bounds
    this.createTaskCancelButtonBounds = {
      x: cancelButtonX,
      y: buttonY,
      width: buttonWidth,
      height: buttonHeight
    };

    this.ctx.restore();
  }

  /**
   * Show the create task form
   */
  showCreateTaskForm() {
    this.createTaskFormVisible = true;
    this.createTaskDescription = '';
    this.createTaskAssignToAgent = false;
    this.selectedAgentForTask = null;
    this.agentSelectionBounds = [];
    this.draw();
  }

  /**
   * Hide the create task form
   */
  hideCreateTaskForm() {
    this.createTaskFormVisible = false;
    this.createTaskDescription = '';
    this.createTaskAssignToAgent = false;
    this.selectedAgentForTask = null;
    this.agentSelectionBounds = [];
    this.draw();
  }

  /**
   * Submit the create task form
   */
  async submitCreateTaskForm() {
    if (!this.createTaskDescription || this.createTaskDescription.trim() === '') {
      alert('Please enter a task description');
      return;
    }

    // Verify we have a workspace ID
    if (!this.studioId) {
      alert('Error: Workspace ID not found. Please refresh the page and try again.');
      console.error('Canvas studioId is not set:', this.studioId);
      return;
    }

    const requestBody = {
      studio_id: this.studioId,  // Backend expects 'studio_id', not 'workspace_id'
      from: 'user',
      description: this.createTaskDescription.trim(),
      priority: 0,
    };

    // Add 'to' field if agent is selected, otherwise use "unassigned"
    if (this.createTaskAssignToAgent && this.selectedAgentForTask) {
      requestBody.to = this.selectedAgentForTask;
    } else {
      // For unassigned tasks, use "unassigned" (allowed as special value in backend)
      requestBody.to = 'unassigned';
    }

    console.log('Creating task with request body:', requestBody);

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(requestBody),
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to create task');
      }

      const result = await response.json();
      console.log('✅ Task created:', result);

      // Hide form
      this.hideCreateTaskForm();

      // Reload studio data to show new task
      await this.init();

      // Show success message
      alert(`✅ Task created successfully!`);
    } catch (error) {
      console.error('❌ Error creating task:', error);
      alert('Failed to create task: ' + error.message);
    }
  }

  /**
   * Delete a task
   */
  async deleteTask(task) {
    if (!task || !task.id) {
      console.error('Invalid task:', task);
      return;
    }

    // Confirm deletion
    const confirmed = confirm(`Are you sure you want to delete this task?\n\n"${task.description || 'Task'}"\n\nThis action cannot be undone.`);
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(task.id)}`, {
        method: 'DELETE',
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to delete task');
      }

      console.log('✅ Task deleted:', task.id);

      // Remove task from local array
      const index = this.tasks.findIndex(t => t.id === task.id);
      if (index !== -1) {
        this.tasks.splice(index, 1);
      }

      // Close task panel if it was open
      if (this.expandedTask && this.expandedTask.id === task.id) {
        this.closeTaskPanel();
      }

      // Redraw canvas
      this.draw();

      // Update metrics
      this.updateMetrics();

      // Reload to ensure consistency
      setTimeout(() => this.init(), 500);

    } catch (error) {
      console.error('❌ Error deleting task:', error);
      alert('Failed to delete task: ' + error.message);
    }
  }

  /**
   * Execute a task manually
   */
  async executeTask(task) {
    if (!task || !task.id) {
      console.error('Invalid task:', task);
      return;
    }

    // For unassigned tasks, ask user to select an agent
    if (task.to === 'unassigned') {
      if (!this.agents || this.agents.length === 0) {
        alert('No agents available. Please add agents to the workspace first.');
        return;
      }

      // Show agent selection prompt
      let agentOptions = this.agents.map((a, i) => `${i + 1}. ${a.name}`).join('\n');
      const selection = prompt(`This task is unassigned. Select an agent to execute it:\n\n${agentOptions}\n\nEnter agent number (1-${this.agents.length}):`);

      if (!selection) return; // User cancelled

      const agentIndex = parseInt(selection) - 1;
      if (agentIndex < 0 || agentIndex >= this.agents.length) {
        alert('Invalid agent selection');
        return;
      }

      const selectedAgent = this.agents[agentIndex];

      // Update task's 'to' field before executing
      try {
        const updateResponse = await fetch(`/api/orchestration/tasks/${task.id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            to: selectedAgent.name,
            status: 'pending'
          })
        });

        if (!updateResponse.ok) {
          throw new Error('Failed to assign task to agent');
        }

        // Update local task object
        task.to = selectedAgent.name;
      } catch (error) {
        console.error('Error assigning task:', error);
        alert('Failed to assign task: ' + error.message);
        return;
      }
    }

    // Execute the task
    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: task.id })
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to execute task');
      }

      const result = await response.json();
      console.log('✅ Task execution started:', result);

      // Update task status locally
      task.status = 'in_progress';
      this.draw();

      // Reload after a short delay to get updated status
      setTimeout(() => this.init(), 1000);

    } catch (error) {
      console.error('❌ Error executing task:', error);
      alert('Failed to execute task: ' + error.message);
    }
  }

  showExecutionLog(task) {
    const logs = this.executionLogs[task.id] || [];

    if (logs.length === 0) {
      this.addNotification('No execution log available for this task', 'info');
      return;
    }

    // Create modal HTML
    let logsHTML = '<div style="max-height: 400px; overflow-y: auto;">';

    logs.forEach((log, index) => {
      const time = log.timestamp.toLocaleTimeString();
      let icon = '•';
      let color = '#6c757d';

      switch (log.type) {
        case 'thinking':
          icon = '🧠';
          color = '#17a2b8';
          break;
        case 'tool_call':
          icon = '🔧';
          color = '#ffc107';
          break;
        case 'tool_success':
          icon = '✓';
          color = '#28a745';
          break;
        case 'tool_error':
          icon = '✗';
          color = '#dc3545';
          break;
      }

      logsHTML += `
        <div style="padding: 8px; border-left: 3px solid ${color}; margin-bottom: 8px; background-color: #f8f9fa;">
          <div style="font-size: 11px; color: #6c757d; margin-bottom: 4px;">
            <strong>${time}</strong>
          </div>
          <div style="font-size: 12px; color: #212529;">
            <span style="margin-right: 4px;">${icon}</span>
            ${log.message}
          </div>
        </div>
      `;
    });

    logsHTML += '</div>';

    // Show modal using Bootstrap modal if available, or alert as fallback
    if (typeof bootstrap !== 'undefined' && bootstrap.Modal) {
      // Create modal element
      const modalDiv = document.createElement('div');
      modalDiv.innerHTML = `
        <div class="modal fade" id="executionLogModal" tabindex="-1">
          <div class="modal-dialog modal-lg">
            <div class="modal-content">
              <div class="modal-header">
                <h5 class="modal-title">Execution Log: ${task.description || task.id}</h5>
                <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
              </div>
              <div class="modal-body">
                ${logsHTML}
              </div>
              <div class="modal-footer">
                <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
              </div>
            </div>
          </div>
        </div>
      `;
      document.body.appendChild(modalDiv);

      const modal = new bootstrap.Modal(document.getElementById('executionLogModal'));
      modal.show();

      // Clean up after modal is hidden
      document.getElementById('executionLogModal').addEventListener('hidden.bs.modal', () => {
        modalDiv.remove();
      });
    } else {
      // Fallback: show in alert
      const logText = logs.map(log => `[${log.timestamp.toLocaleTimeString()}] ${log.message}`).join('\n');
      alert(`Execution Log:\n\n${logText}`);
    }
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
