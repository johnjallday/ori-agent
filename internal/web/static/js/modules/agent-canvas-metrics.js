/**
 * AgentCanvasMetrics - Metrics and statistics module
 * Handles workspace metrics and agent statistics updates
 */
export class AgentCanvasMetrics {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Update workspace metrics (task counts, completion rates, etc.)
   */
  updateMetrics() {
    if (!this.parent.onMetricsUpdate) return;

    const completed = this.parent.tasks.filter(t => t.status === 'completed').length;
    const inProgress = this.parent.tasks.filter(t => t.status === 'in_progress').length;

    this.parent.onMetricsUpdate({
      total: this.parent.tasks.length,
      completed: completed,
      inProgress: inProgress
    });
  }

  /**
   * Update agent statistics from server
   */
  updateAgentStats(agentStats) {
    // Update agent status and stats from server
    for (const agentName in agentStats) {
      const agent = this.parent.agents.find(a => a.name === agentName);
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
