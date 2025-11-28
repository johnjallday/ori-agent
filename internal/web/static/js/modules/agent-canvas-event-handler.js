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
        if (data.workspace_progress) {
          this.parent.workspaceProgress = data.workspace_progress;
        }
        if (data.agent_stats) {
          this.parent.metrics.updateAgentStats(data.agent_stats);
        }
        if (data.tasks) {
          const existingPositions = {};
          this.parent.tasks.forEach(t => {
            if (t.x !== null && t.y !== null) {
              existingPositions[t.id] = { x: t.x, y: t.y };
            }
          });

          this.parent.tasks = data.tasks.map(task => {
            const existing = existingPositions[task.id];
            return {
              ...task,
              x: existing ? existing.x : (task.x ?? null),
              y: existing ? existing.y : (task.y ?? null)
            };
          });
        }
        this.parent.draw();
      },
      onWorkspaceProgress: (data) => {
        console.log('📊 Workspace progress update:', data);
        if (data.workspace_progress) {
          this.parent.workspaceProgress = data.workspace_progress;
        }
        if (data.agent_stats) {
          this.parent.metrics.updateAgentStats(data.agent_stats);
        }
        this.parent.draw();
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
  }

  /**
   * Handle task status change events
   */
  handleTaskEvent(eventData) {
    const taskId = eventData.data.task_id;
    const task = this.parent.tasks.find(t => t.id === taskId);

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
          if (task.to) {
            const agent = this.parent.agents.find(a => a.name === task.to);
            if (agent) {
              agent.lastResult = eventData.data.result;
              console.log(`✅ Updated lastResult for agent ${agent.name}:`, eventData.data.result);
            }
          }
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

    // Update metrics after any task-related event
    if (event.type.includes('task')) {
      this.parent.metrics.updateMetrics();
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
    this.parent.tasks.push(task);
    this.parent.draw();
  }

  /**
   * Update task status
   */
  updateTaskStatus(taskId, status) {
    const task = this.parent.tasks.find(t => t.id === taskId);
    if (task) {
      task.status = status;
      this.parent.draw();
    }
  }

  /**
   * Set agent status
   */
  setAgentStatus(agentName, status) {
    const agent = this.parent.agents.find(a => a.name === agentName);
    if (agent) {
      agent.status = status;
      this.parent.draw();
    }
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
}
