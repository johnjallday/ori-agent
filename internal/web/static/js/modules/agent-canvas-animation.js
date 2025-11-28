/**
 * AgentCanvasAnimationController
 * Manages canvas animations including particles, chain particles, and animation loop
 */
export class AgentCanvasAnimationController {
  /**
   * @param {AgentCanvasState} state - Shared state object
   * @param {AgentCanvas} parent - Parent AgentCanvas instance
   */
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Start the animation loop
   */
  startAnimation() {
    const animate = () => {
      this.update();
      this.parent.draw();
      this.state.animationFrame = requestAnimationFrame(animate);
    };
    animate();
  }

  /**
   * Update animation state (called every frame)
   */
  update() {
    // Skip updates if animation is paused
    if (this.state.animationPaused) return;

    // Update task progress
    this.state.tasks.forEach(task => {
      if (task.status === 'in_progress' && task.progress < 100) {
        task.progress += 0.5;
      }
    });

    // Update particles
    this.state.particles = this.state.particles.filter(p => {
      p.progress += p.speed;
      p.x = p.x + (p.targetX - p.x) * p.progress;
      p.y = p.y + (p.targetY - p.y) * p.progress;
      p.alpha = 1 - p.progress;
      return p.progress < 1;
    });

    // Update chain particles
    this.updateChainParticles();

    // Update agent pulse
    this.state.agents.forEach(agent => {
      agent.pulsePhase += 0.05;
    });
  }

  /**
   * Create particle effects for task assignments
   * @param {Object} task - Task object with from/to agent assignments
   */
  createTaskParticles(task) {
    const fromAgent = this.state.agents.find(a => a.name === task.from);
    const toAgent = this.state.agents.find(a => a.name === task.to);

    if (fromAgent && toAgent) {
      for (let i = 0; i < 20; i++) {
        this.state.particles.push({
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

  /**
   * Detect and update active task chains
   */
  updateChains() {
    if (!this.state.tasks || this.state.tasks.length === 0) {
      this.state.activeChains = [];
      return;
    }

    const chains = [];

    // Find all tasks that are part of chains (have input_task_ids)
    this.state.tasks.forEach(task => {
      if (task.input_task_ids && task.input_task_ids.length > 0) {
        // This task depends on other tasks - it's part of a chain
        task.input_task_ids.forEach(inputTaskId => {
          const inputTask = this.state.tasks.find(t => t.id === inputTaskId);
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

    this.state.activeChains = chains;
  }

  /**
   * Create chain particles for active chains
   * @param {Object} fromTask - Source task
   * @param {Object} toTask - Target task
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

    this.state.chainParticles.push(particle);
  }

  /**
   * Update chain particles animation
   */
  updateChainParticles() {
    // Update existing particles
    this.state.chainParticles = this.state.chainParticles.filter(p => {
      p.progress += p.speed;
      p.x = p.x + (p.targetX - p.x) * p.progress;
      p.y = p.y + (p.targetY - p.y) * p.progress;
      p.alpha = 1 - p.progress;
      return p.progress < 1;
    });

    // Generate new particles for active chains
    this.state.activeChains.forEach(chain => {
      if (chain.active && !chain.completed && Math.random() < 0.1) {
        this.createChainParticle(chain.from, chain.to);
      }
    });
  }
}
