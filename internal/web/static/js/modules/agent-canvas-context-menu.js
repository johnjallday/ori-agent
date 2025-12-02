import { apiDelete } from './agent-canvas-api.js';

/**
 * AgentCanvasContextMenu - Context menu and assignment mode module
 * Handles right-click context menus and task assignment mode
 */
export class AgentCanvasContextMenu {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Toggle assignment mode for connecting tasks to agents/combiners
   */
  toggleAssignmentMode(task) {
    console.log('toggleAssignmentMode called for task:', task.id);
    if (this.parent.assignmentMode && this.parent.assignmentSourceTask && this.parent.assignmentSourceTask.id === task.id) {
      // Cancel assignment mode
      console.log('Exiting assignment mode');
      this.parent.assignmentMode = false;
      this.parent.assignmentSourceTask = null;
      this.parent.assignmentMouseX = 0;
      this.parent.assignmentMouseY = 0;
      this.parent.canvas.style.cursor = 'grab';
    } else {
      // Enter assignment mode
      console.log('Entering assignment mode for task:', task.id);
      this.parent.assignmentMode = true;
      this.parent.assignmentSourceTask = task;
      this.parent.canvas.style.cursor = 'crosshair';
    }
    this.parent.draw();
  }

  /**
   * Handle context menu actions for agents
   */
  handleContextMenuAction(action, agent) {
    console.log(`🎯 Context menu action: ${action} for agent ${agent.name}`);

    switch (action) {
      case 'view':
        // View agent details - expand agent panel
        if (this.parent.expandedAgentPanelWidth === 0) {
          this.parent.expandedAgentPanelWidth = 1;
          this.parent.expandedAgentPanelTarget = 350;
        }
        this.parent.selectedAgent = agent;
        this.parent.draw();
        break;

      case 'assign':
        // Assign task to agent - show task creation form
        this.parent.forms.showCreateTaskForm(agent.x, agent.y);
        this.parent.forms.createTaskTargetAgent = agent.name;
        this.parent.draw();
        break;

      case 'remove':
        // Remove agent (with confirmation)
        const displayName = agent.instanceNumber ? `${agent.name} #${agent.instanceNumber}` : agent.name;
        if (confirm(`Remove agent "${displayName}"?`)) {
          // Call backend to remove agent from studio, sending instance number if available
          const agentId = agent.instanceNumber ? `${agent.name}:${agent.instanceNumber}` : agent.name;
          apiDelete(`/api/studios/${encodeURIComponent(this.parent.studioId)}/agents/${encodeURIComponent(agentId)}`)
            .then(() => {
              // Remove only THIS specific agent node from local state (by nodeId)
              const filteredAgents = this.state.agents.filter(a => a.nodeId !== agent.nodeId);
              this.state.setAgents(filteredAgents);

              // Unassign tasks targeting THIS specific agent node
              const updatedTasks = this.state.tasks.map(t => {
                // Only unassign if task was assigned to this specific node instance
                if (t.assigned_node_id === agent.nodeId) {
                  return { ...t, to: 'unassigned', assigned_node_id: '' };
                }
                return t;
              });
              this.state.setTasks(updatedTasks);

              // Remove any workflow connections involving this agent
              this.parent.connections = this.parent.connections.filter(c =>
                c.from !== agent.name && c.to !== agent.name
              );

              this.parent.notifications.showNotification('Agent removed', 'success');
              this.parent.draw();
              this.parent.layout.saveLayout();
            })
            .catch(err => {
              console.error('Failed to remove agent:', err);
              this.parent.notifications.showNotification(`Failed to remove agent: ${err.message}`, 'error');
            });
        }
        break;

      default:
        console.warn(`Unknown context menu action: ${action}`);
    }
  }
}
