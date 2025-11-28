import { apiGet } from './agent-canvas-api.js';

/**
 * AgentCanvasPanelManager - Manages panel UI state and animations
 * Handles task panels, agent panels, and combiner panels
 */
export class AgentCanvasPanelManager {
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Toggle task panel - opens panel for given task or closes if already open
   */
  toggleTaskPanel(task) {
    // Close agent panel if open
    if (this.parent.expandedAgent) {
      this.closeAgentPanel();
    }
    if (this.parent.expandedCombiner) {
      this.closeCombinerPanel();
    }

    if (this.parent.expandedTask && this.parent.expandedTask.id === task.id) {
      // Clicking the same task - close panel
      this.closeTaskPanel();
    } else {
      // Expand panel for this task
      this.parent.expandedTask = task;
      this.parent.resultScrollOffset = 0; // Reset scroll when opening a new task
      this.parent.copyButtonState = 'idle'; // Reset copy button state
      this.parent.expandedPanelAnimating = true;
      this.animatePanel(true);
    }
  }

  /**
   * Close task panel with animation
   */
  closeTaskPanel() {
    this.parent.expandedPanelAnimating = true;
    this.animatePanel(false);
  }

  /**
   * Animate task panel opening/closing
   */
  animatePanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.parent.expandedPanelWidth = Math.min(
          this.parent.expandedPanelWidth + speed,
          this.parent.expandedPanelTargetWidth
        );

        if (this.parent.expandedPanelWidth >= this.parent.expandedPanelTargetWidth) {
          this.parent.expandedPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.parent.expandedPanelWidth = Math.max(this.parent.expandedPanelWidth - speed, 0);

        if (this.parent.expandedPanelWidth <= 0) {
          this.parent.expandedPanelAnimating = false;
          this.parent.expandedTask = null;
          this.parent.resultScrollOffset = 0; // Reset scroll when closing panel
        } else {
          requestAnimationFrame(animate);
        }
      }
    };

    animate();
  }

  /**
   * Toggle agent panel - opens panel for given agent or closes if already open
   */
  async toggleAgentPanel(agent) {
    // Close task panel if open
    if (this.parent.expandedTask) {
      this.closeTaskPanel();
    }
    if (this.parent.expandedCombiner) {
      this.closeCombinerPanel();
    }

    if (this.parent.expandedAgent && this.parent.expandedAgent.name === agent.name) {
      // Clicking the same agent - close panel
      this.closeAgentPanel();
    } else {
      // Reset scroll offset when opening new agent
      this.parent.agentPanelScrollOffset = 0;
      this.parent.agentPanelMaxScroll = 0;

      // Fetch agent configuration before expanding (optional - doesn't block panel)
      try {
        const configResponse = await apiGet(`/api/agents/${agent.name}`);
        if (configResponse.ok) {
          const agentConfig = await configResponse.json();
          // Merge config data with agent
          this.parent.expandedAgent = {
            ...agent,
            config: agentConfig
          };
        } else {
          // Use agent without detailed config if fetch fails (workspace agents may not be in global store)
          console.log(`Agent ${agent.name} config not found in global store - using workspace data`);
          this.parent.expandedAgent = {
            ...agent,
            config: null
          };
        }
      } catch (error) {
        console.log('Using workspace agent data without global config:', error.message);
        this.parent.expandedAgent = {
          ...agent,
          config: null
        };
      }

      this.parent.expandedAgentPanelAnimating = true;
      this.animateAgentPanel(true);
    }
  }

  /**
   * Close agent panel with animation
   */
  closeAgentPanel() {
    this.parent.expandedAgentPanelAnimating = true;
    this.animateAgentPanel(false);
  }

  /**
   * Animate agent panel opening/closing
   */
  animateAgentPanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.parent.expandedAgentPanelWidth = Math.min(
          this.parent.expandedAgentPanelWidth + speed,
          this.parent.expandedAgentPanelTargetWidth
        );

        if (this.parent.expandedAgentPanelWidth >= this.parent.expandedAgentPanelTargetWidth) {
          this.parent.expandedAgentPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.parent.expandedAgentPanelWidth = Math.max(this.parent.expandedAgentPanelWidth - speed, 0);

        if (this.parent.expandedAgentPanelWidth <= 0) {
          this.parent.expandedAgentPanelAnimating = false;
          this.parent.expandedAgent = null;
          this.parent.agentPanelScrollOffset = 0; // Reset scroll when closing panel
          this.parent.agentPanelMaxScroll = 0; // Reset max scroll when closing panel
        } else {
          requestAnimationFrame(animate);
        }
      }
    };

    animate();
  }

  /**
   * Toggle combiner panel - opens panel for given combiner or closes if already open
   */
  toggleCombinerPanel(combiner) {
    // Close other panels
    if (this.parent.expandedTask) this.closeTaskPanel();
    if (this.parent.expandedAgent) this.closeAgentPanel();

    if (this.parent.expandedCombiner && this.parent.expandedCombiner.id === combiner.id) {
      this.closeCombinerPanel();
      return;
    }

    this.parent.expandedCombiner = combiner;
    this.parent.expandedCombinerPanelAnimating = true;
    this.animateCombinerPanel(true);
  }

  /**
   * Close combiner panel with animation
   */
  closeCombinerPanel() {
    this.parent.expandedCombinerPanelAnimating = true;
    this.animateCombinerPanel(false);
  }

  /**
   * Animate combiner panel opening/closing
   */
  animateCombinerPanel(expanding) {
    const animate = () => {
      const speed = 30;
      if (expanding) {
        this.parent.expandedCombinerPanelWidth = Math.min(
          this.parent.expandedCombinerPanelWidth + speed,
          this.parent.expandedCombinerPanelTargetWidth
        );
        if (this.parent.expandedCombinerPanelWidth >= this.parent.expandedCombinerPanelTargetWidth) {
          this.parent.expandedCombinerPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.parent.expandedCombinerPanelWidth = Math.max(this.parent.expandedCombinerPanelWidth - speed, 0);
        if (this.parent.expandedCombinerPanelWidth <= 0) {
          this.parent.expandedCombinerPanelAnimating = false;
          this.parent.expandedCombiner = null;
        } else {
          requestAnimationFrame(animate);
        }
      }
    };
    animate();
  }

  /**
   * Toggle help overlay visibility
   */
  toggleHelpOverlay() {
    if (!this.parent.helpOverlayVisible) {
      this.parent.helpOverlayVisible = true;
      console.log('📖 Showing keyboard shortcuts');
    } else {
      this.parent.helpOverlayVisible = false;
      console.log('📖 Hiding keyboard shortcuts');
    }
    this.parent.draw();
  }
}
