/**
 * AgentCanvasEventHandler - Event handling and stream connection module
 * Handles SSE connections, event processing, and state updates
 */
import { connectProgressStream } from './agent-canvas-events.js';

export class AgentCanvasEventHandler {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Connect to server-sent events stream for real-time updates
   */
  connectEventStream() {
    if (this.parent.eventSource) {
      this.parent.eventSource.close();
    }

    // Toast notifications array
    this.parent.notifications_array = this.parent.notifications_array || [];

    this.parent.eventSource = connectProgressStream(this.parent.studioId, {
      onInitial: (data) => {
        console.log('📊 Initial progress state:', data);
        this.processWorkspacePayload(data, { setTasks: true, source: 'initial' });
      },
      onWorkspaceProgress: (data) => {
        this.processWorkspacePayload(data, { setTasks: false, source: 'workspace.progress' });
      },
      onTaskEvent: (type, data) => {
        const evt = { type, data };
        this.handleTaskEvent(evt);
        const taskDesc = data.data?.description || 'Task';
        if (type === 'task.completed') {
          this.parent.notifications.showNotification(`✓ ${taskDesc} completed`, 'success');
        } else if (type === 'task.failed') {
          const error = data.data?.error || 'Unknown error';
          this.parent.notifications.showNotification(`✗ ${taskDesc} failed: ${error}`, 'error');
        } else if (type === 'task.started') {
          this.parent.notifications.showNotification(`${taskDesc} started`, 'info');
        } else if (type === 'task.created') {
          this.parent.notifications.showNotification('Task created', 'info');
        }
        this.parent.timeline.addTimelineEvent(evt);
      },
      onTaskThinking: (data) => {
        this.parent.notifications.addExecutionLog(data.data.task_id, 'thinking', data.data.message || 'Analyzing task...');
        this.parent.timeline.addTimelineEvent({ type: 'task.thinking', data });
      },
      onTaskToolCall: (data) => {
        const toolName = data.data.tool_name || 'Unknown tool';
        this.parent.notifications.addExecutionLog(data.data.task_id, 'tool_call', `Calling tool: ${toolName}`);
        this.parent.timeline.addTimelineEvent({ type: 'task.tool_call', data });
      },
      onTaskToolSuccess: (data) => {
        this.parent.notifications.addExecutionLog(data.data.task_id, 'tool_success', data.data.message || 'Tool succeeded');
        this.parent.timeline.addTimelineEvent({ type: 'task.tool_success', data });
      },
      onTaskToolError: (data) => {
        this.parent.notifications.addExecutionLog(data.data.task_id, 'tool_error', data.data.message || 'Tool failed');
        this.parent.timeline.addTimelineEvent({ type: 'task.tool_error', data });
      },
      onTaskProgress: (data) => {
        this.parent.notifications.addExecutionLog(data.data.task_id, 'progress', data.data.message || 'Task progress update');
        this.parent.timeline.addTimelineEvent({ type: 'task.progress', data });
      },
      onAttachmentEvent: (type, data) => {
        const evt = { type, data };
        this.handleAttachmentEvent(evt);
      },
      onError: (error) => {
        console.error('EventSource error:', error);
        setTimeout(() => {
          if (this.parent.eventSource && this.parent.eventSource.readyState === EventSource.CLOSED) {
            this.connectEventStream();
          }
        }, 5000);
      }
    });

    console.log('🔄 Connected to progress stream');
    // Emit a synthetic workspace snapshot immediately so UI reacts without waiting for server tick
    this.emitImmediateWorkspaceProgress();
  }

  /**
   * Process workspace progress payloads from SSE or initial fetch.
   */
  processWorkspacePayload(data, { setTasks = false, source = 'workspace.progress' } = {}) {
    if (!data) return;

    const payloadForLog = {
      ...data,
      type: data.type || source || 'workspace.progress',
      timestamp: data.timestamp || new Date().toISOString()
    };
    console.log('📊 Workspace progress update:', payloadForLog);

    if (data.workspace_progress) {
      this.parent.workspaceProgress = data.workspace_progress;
    }
    if (data.agent_stats) {
      this.updateAgentStats(data.agent_stats);
    }

    if (data.tasks && Array.isArray(data.tasks)) {
      const tasks = setTasks
        ? this.normalizeTasksForState(data.tasks)
        : data.tasks.map(task => this.normalizeTaskWithPosition({ ...task }));

      if (setTasks) {
        this.state.setTasks(tasks);
      }
      this.updateAgentResultsFromTasks(tasks);
    }

    if (data.attachments && Array.isArray(data.attachments)) {
      const attachments = this.normalizeAttachmentsForState(data.attachments);
      this.state.setAttachments(attachments);
    }

    this.parent.draw();
  }

  /**
   * Normalize task fields and ensure a sane position.
   */
  normalizeTaskWithPosition(task) {
    if (!task) return task;

    this.ensureTaskPosition(task);
    if (task.to === 'unassigned' || !task.to) {
      task.status = 'pending';
      task.result = null;
      task.error = null;
      task.progress = 0;
      task.completed_at = null;
      task.started_at = null;
    }
    return task;
  }

  /**
   * Normalize tasks while preserving known positions and assignments from state.
   */
  normalizeTasksForState(tasks) {
    const existingPositions = {};
    this.state.tasks.forEach(t => {
      if (t.x !== null && t.y !== null) {
        existingPositions[t.id] = { x: t.x, y: t.y };
      }
    });

    return tasks.map(task => {
      const existing = existingPositions[task.id];
      const mapped = {
        ...task,
        x: existing ? existing.x : (task.x ?? null),
        y: existing ? existing.y : (task.y ?? null)
      };

      // Preserve assigned node hint if present on local task
      const local = this.state.tasks.find(t => t.id === task.id);
      if (local && local.assigned_node_id && !mapped.assigned_node_id) {
        mapped.assigned_node_id = local.assigned_node_id;
      }

      return this.normalizeTaskWithPosition(mapped);
    });
  }

  /**
   * Normalize attachments while preserving known positions from state.
   */
  normalizeAttachmentsForState(attachments) {
    const existingPositions = {};
    this.state.attachments.forEach(a => {
      if (a.x !== null && a.y !== null) {
        existingPositions[a.id] = { x: a.x, y: a.y };
      }
    });

    return attachments.map(att => {
      const existing = existingPositions[att.id];
      const mapped = {
        ...att,
        file: att.file || att.file_meta,
        x: existing ? existing.x : (att.x ?? null),
        y: existing ? existing.y : (att.y ?? null)
      };
      return this.normalizeAttachmentWithPosition(mapped);
    });
  }

  /**
   * Normalize a single attachment and ensure position.
   */
  normalizeAttachmentWithPosition(att) {
    if (!att) return att;
    this.ensureAttachmentPosition(att);
    return att;
  }

  /**
   * Immediately feed the current state back through the workspace processor so
   * the canvas updates without waiting for the first SSE tick.
   */
  emitImmediateWorkspaceProgress() {
    const snapshot = {
      type: 'workspace.progress',
      workspace_id: this.parent.studioId,
      workspace_progress: this.parent.workspaceProgress || null,
      agent_stats: (this.parent.studio && this.parent.studio.agent_stats) || null,
      tasks: this.state.tasks,
      attachments: this.state.attachments
    };
    this.processWorkspacePayload(snapshot, { setTasks: false, source: 'client.snapshot' });
  }

  /**
   * Handle task status change events
   */
  handleTaskEvent(eventData) {
    const taskId = eventData.data.task_id;
    const task = this.state.tasks.find(t => t.id === taskId);

    if (task) {
      // Update existing task
      if (eventData.type === 'task.started') {
        task.status = 'in_progress';
        task.started_at = new Date().toISOString();
      } else if (eventData.type === 'task.completed') {
        task.status = 'completed';
        task.completed_at = new Date().toISOString();

        // Store result on task if available
        if (eventData.data.result) {
          task.result = eventData.data.result;

          // Update the agent's lastResult
          this.updateAgentResultsFromTasks([task]);
        }
      } else if (eventData.type === 'task.failed') {
        task.status = 'failed';
        task.error = eventData.data.error;
      }

      // Update chains when task status changes
      this.parent.animation.updateChains();
      this.parent.draw();
    }
  }

  /**
   * Handle attachment events from SSE.
   */
  handleAttachmentEvent(eventData) {
    const payload = eventData.data?.attachment;
    const id = eventData.data?.attachment_id || payload?.id;

    if (eventData.type === 'attachment.deleted') {
      this.state.removeAttachment(id);
      this.parent.draw();
      return;
    }

    if (!payload) {
      return;
    }

    const normalized = this.normalizeAttachmentWithPosition({ ...payload, file: payload.file || payload.file_meta });
    const idx = this.state.attachments.findIndex(a => a.id === normalized.id);
    if (idx === -1) {
      this.state.attachments.push(normalized);
    } else {
      this.state.attachments[idx] = normalized;
    }
    this.parent.draw();
  }

  /**
   * Generic event handler for workspace events
   */
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
    if (this.parent.onTimelineEvent) {
      this.parent.onTimelineEvent(event);
    }

  }

  /**
   * Add a new task to canvas
   */
  addTask(taskData) {
    const task = {
      ...taskData,
      x: taskData.x ?? null,
      y: taskData.y ?? null,
      status: taskData.status || 'pending'
    };
    this.ensureTaskPosition(task);
    this.state.addTask(task);
    this.parent.draw();
  }

  /**
   * Update task status
   */
  updateTaskStatus(taskId, status) {
    const task = this.state.tasks.find(t => t.id === taskId);
    if (task) {
      task.status = status;
      this.parent.draw();
    }
  }

  /**
   * Set agent status
   */
  setAgentStatus(agentName, status) {
    const agent = this.state.agents.find(a => a.name === agentName);
    if (agent) {
      agent.status = status;
      this.parent.draw();
    }
  }

  /**
   * Update agents' lastResult fields based on completed task results.
   * Uses latest timestamp (completed_at > updated_at > created_at) per agent.
   */
  updateAgentResultsFromTasks(tasks = this.state.tasks) {
    if (!Array.isArray(tasks) || tasks.length === 0) return;

    const latestByAgent = {};
    tasks.forEach(t => {
      if (!t || !t.to || t.to === 'unassigned' || !t.result) return;
      const agent = this.resolveAgentForTask(t);
      if (!agent) return;
      const agentKey = agent.nodeId || agent.name;
      const completed = t.completed_at ? new Date(t.completed_at).getTime() : 0;
      const updated = t.updated_at ? new Date(t.updated_at).getTime() : 0;
      const created = t.created_at ? new Date(t.created_at).getTime() : 0;
      const ts = Math.max(completed, updated, created);
      if (!latestByAgent[agentKey] || ts > latestByAgent[agentKey].ts) {
        latestByAgent[agentKey] = { ts, result: t.result };
      }
    });

    Object.entries(latestByAgent).forEach(([agentKey, info]) => {
      const agent = this.state.agents.find(a => (a.nodeId || a.name) === agentKey);
      if (agent) {
        agent.lastResult = info.result;
      }
    });
  }

  /**
   * Resolve the specific agent instance for a task (prefers assigned_node_id, falls back to name).
   */
  resolveAgentForTask(task) {
    if (!task || !task.to || task.to === 'unassigned') return null;
    // Prefer explicit assignment to a specific node id
    if (task.assigned_node_id) {
      const matchByNode = this.state.agents.find(a =>
        a.nodeId === task.assigned_node_id || a.id === task.assigned_node_id);
      if (matchByNode) return matchByNode;
    }

    // Fallback: pick the closest agent with matching name to the task position
    const candidates = this.state.agents.filter(a => a.name === task.to);
    if (candidates.length === 0) return null;
    if (candidates.length === 1 || task.x == null || task.y == null) return candidates[0];

    let best = candidates[0];
    let bestDist = Infinity;
    candidates.forEach(a => {
      const d = Math.hypot((a.x || 0) - task.x, (a.y || 0) - task.y);
      if (d < bestDist) {
        bestDist = d;
        best = a;
      }
    });
    return best;
  }

  /**
   * Ensure a task has a sensible on-screen position near the current viewport.
   */
  ensureTaskPosition(task) {
    if (!task) return;

    const centerX = (this.parent.width / 2 - this.parent.offsetX) / this.parent.scale;
    const centerY = (this.parent.height / 2 - this.parent.offsetY) / this.parent.scale;

    // Define a generous viewport bounds to detect off-screen items
    const halfW = (this.parent.width / this.parent.scale) / 2;
    const halfH = (this.parent.height / this.parent.scale) / 2;
    const left = centerX - halfW * 1.5;
    const right = centerX + halfW * 1.5;
    const top = centerY - halfH * 1.5;
    const bottom = centerY + halfH * 1.5;

    const needsPlacement =
      task.x == null || task.y == null || task.x < left || task.x > right || task.y < top || task.y > bottom;

    if (!needsPlacement) return;

    // Spread new/off-screen tasks around the center to reduce overlap
    const jitterX = (Math.random() - 0.5) * 120;
    const jitterY = (Math.random() - 0.5) * 120;

    task.x = centerX + jitterX;
    task.y = centerY + jitterY;
  }

  /**
   * Ensure an attachment has a sensible on-screen position near the viewport.
   */
  ensureAttachmentPosition(att) {
    if (!att) return;

    const centerX = (this.parent.width / 2 - this.parent.offsetX) / this.parent.scale;
    const centerY = (this.parent.height / 2 - this.parent.offsetY) / this.parent.scale;

    const halfW = (this.parent.width / this.parent.scale) / 2;
    const halfH = (this.parent.height / this.parent.scale) / 2;
    const left = centerX - halfW * 1.5;
    const right = centerX + halfW * 1.5;
    const top = centerY - halfH * 1.5;
    const bottom = centerY + halfH * 1.5;

    const needsPlacement =
      att.x == null || att.y == null || att.x < left || att.x > right || att.y < top || att.y > bottom;

    if (!needsPlacement) return;

    const jitterX = (Math.random() - 0.5) * 100;
    const jitterY = (Math.random() - 0.5) * 100;

    att.x = centerX + jitterX;
    att.y = centerY + jitterY;
  }

  /**
   * Add a message to canvas
   */
  addMessage(messageData) {
    this.parent.messages.push(messageData);
    this.parent.draw();
  }

  /**
   * Set workspace mission
   */
  setMission(missionText) {
    this.parent.mission = missionText;
    this.parent.draw();
  }

  /**
   * Update agent statistics from server
   */
  updateAgentStats(agentStats) {
    // Update agent status and stats from server
    for (const agentName in agentStats) {
      const agent = this.state.agents.find(a => a.name === agentName);
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
    this.parent.animation.updateChains();
  }
}
