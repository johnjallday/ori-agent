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

  /**
   * Handle multi-select context menu actions
   */
  handleMultiSelectAction(action) {
    console.log(`🎯 Multi-select action: ${action}`);

    switch (action) {
      case 'workflow':
        this.createWorkflowFromSelection();
        break;

      case 'delete':
        this.bulkDeleteNodes();
        break;

      case 'group':
        this.groupNodes();
        break;

      default:
        console.warn(`Unknown multi-select action: ${action}`);
    }

    // Hide multi-select context menu after action
    this.state.hideMultiSelectContextMenu();
    this.parent.draw();
  }

  /**
   * Create a workflow from selected nodes
   * Placeholder for future workflow creation functionality
   */
  createWorkflowFromSelection() {
    const selectedNodes = this.state.getSelectedNodes();
    const count = selectedNodes.length;

    // For now, show a notification about the planned feature
    this.parent.notifications.showNotification(
      `Workflow creation from ${count} nodes - Coming soon!`,
      'info'
    );

    console.log('📋 Nodes selected for workflow:', selectedNodes);
  }

  /**
   * Delete all selected nodes after confirmation
   */
  async bulkDeleteNodes() {
    const selectedNodes = this.state.getSelectedNodes();
    const count = selectedNodes.length;

    if (count === 0) return;

    // Show confirmation dialog
    const confirmed = confirm(`Delete ${count} selected node${count > 1 ? 's' : ''}?\n\nThis action cannot be undone.`);
    if (!confirmed) return;

    let deletedCount = 0;
    let errorCount = 0;

    // Group nodes by type for batch processing
    const nodesByType = this.state.getSelectedNodesByType();

    // Delete agents
    if (nodesByType.agent && nodesByType.agent.length > 0) {
      for (const { id, node } of nodesByType.agent) {
        try {
          const agentId = node.instanceNumber ? `${node.name}:${node.instanceNumber}` : node.name;
          await apiDelete(`/api/studios/${encodeURIComponent(this.parent.studioId)}/agents/${encodeURIComponent(agentId)}`);

          // Remove from local state
          const filteredAgents = this.state.agents.filter(a => a.nodeId !== node.nodeId);
          this.state.setAgents(filteredAgents);

          // Unassign tasks targeting this agent
          const updatedTasks = this.state.tasks.map(t => {
            if (t.assigned_node_id === node.nodeId) {
              return { ...t, to: 'unassigned', assigned_node_id: '' };
            }
            return t;
          });
          this.state.setTasks(updatedTasks);

          deletedCount++;
        } catch (err) {
          console.error(`Failed to delete agent ${node.name}:`, err);
          errorCount++;
        }
      }
    }

    // Delete tasks
    if (nodesByType.task && nodesByType.task.length > 0) {
      for (const { id, node } of nodesByType.task) {
        try {
          await apiDelete(`/api/studios/${encodeURIComponent(this.parent.studioId)}/tasks/${encodeURIComponent(id)}`);

          // Remove from local state
          const filteredTasks = this.state.tasks.filter(t => t.id !== id);
          this.state.setTasks(filteredTasks);

          deletedCount++;
        } catch (err) {
          console.error(`Failed to delete task ${id}:`, err);
          errorCount++;
        }
      }
    }

    // Delete scheduler nodes
    if (nodesByType.scheduler && nodesByType.scheduler.length > 0) {
      for (const { id, node } of nodesByType.scheduler) {
        try {
          await apiDelete(`/api/studios/${encodeURIComponent(this.parent.studioId)}/schedulers/${encodeURIComponent(id)}`);

          // Remove from local state
          const filteredSchedulers = this.state.schedulerNodes.filter(s => s.id !== id);
          this.state.setSchedulerNodes(filteredSchedulers);

          deletedCount++;
        } catch (err) {
          console.error(`Failed to delete scheduler ${id}:`, err);
          errorCount++;
        }
      }
    }

    // Delete store nodes
    if (nodesByType.store && nodesByType.store.length > 0) {
      for (const { id, node } of nodesByType.store) {
        try {
          await apiDelete(`/api/studios/${encodeURIComponent(this.parent.studioId)}/stores/${encodeURIComponent(id)}`);

          // Remove from local state
          const filteredStores = this.state.storeNodes.filter(s => s.id !== id);
          this.state.setStoreNodes(filteredStores);

          deletedCount++;
        } catch (err) {
          console.error(`Failed to delete store ${id}:`, err);
          errorCount++;
        }
      }
    }

    // Delete attachments
    if (nodesByType.attachment && nodesByType.attachment.length > 0) {
      for (const { id, node } of nodesByType.attachment) {
        try {
          await apiDelete(`/api/studios/${encodeURIComponent(this.parent.studioId)}/attachments/${encodeURIComponent(id)}`);

          // Remove from local state
          const filteredAttachments = this.state.attachments.filter(a => a.id !== id);
          this.state.setAttachments(filteredAttachments);

          deletedCount++;
        } catch (err) {
          console.error(`Failed to delete attachment ${id}:`, err);
          errorCount++;
        }
      }
    }

    // Clear selection after deletion
    this.state.clearSelection();

    // Show result notification
    if (errorCount > 0) {
      this.parent.notifications.showNotification(
        `Deleted ${deletedCount} node${deletedCount !== 1 ? 's' : ''}, ${errorCount} failed`,
        'warning'
      );
    } else {
      this.parent.notifications.showNotification(
        `Deleted ${deletedCount} node${deletedCount !== 1 ? 's' : ''}`,
        'success'
      );
    }

    // Save layout
    this.parent.layout.saveLayout();
  }

  /**
   * Group selected nodes together
   * Placeholder for future grouping functionality
   */
  groupNodes() {
    const selectedNodes = this.state.getSelectedNodes();
    const count = selectedNodes.length;

    // For now, show a notification about the planned feature
    this.parent.notifications.showNotification(
      `Grouping ${count} nodes - Coming soon!`,
      'info'
    );

    console.log('📁 Nodes selected for grouping:', selectedNodes);
  }
}
