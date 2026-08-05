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
    // Close agent panel if open (immediately, no animation)
    if (this.state.expandedAgent) {
      this.state.expandedAgentPanelWidth = 0;
      this.state.expandedAgent = null;
      this.state.agentPanelScrollOffset = 0;
      this.state.agentPanelMaxScroll = 0;
    }
    // Close combiner panel if open (immediately, no animation)
    if (this.state.expandedCombiner) {
      this.state.expandedCombinerPanelWidth = 0;
      this.state.expandedCombiner = null;
    }

    if (this.state.expandedTask && this.state.expandedTask.id === task.id) {
      // Clicking the same task - close panel
      this.closeTaskPanel();
    } else {
      // Expand panel for this task
      this.state.expandedTask = task;
      this.parent.resultScrollOffset = 0; // Reset scroll when opening a new task
      this.parent.copyButtonState = 'idle'; // Reset copy button state
      this.state.expandedPanelAnimating = true;
      this.animatePanel(true);
    }
  }

  /**
   * Close task panel with animation
   */
  closeTaskPanel() {
    this.state.expandedPanelAnimating = true;
    this.animatePanel(false);
  }

  /**
   * Animate task panel opening/closing
   */
  animatePanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.state.expandedPanelWidth = Math.min(
          this.state.expandedPanelWidth + speed,
          this.state.expandedPanelTargetWidth
        );

        this.parent.draw(); // Redraw canvas to show animation

        if (this.state.expandedPanelWidth >= this.state.expandedPanelTargetWidth) {
          this.state.expandedPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.state.expandedPanelWidth = Math.max(this.state.expandedPanelWidth - speed, 0);

        this.parent.draw(); // Redraw canvas to show animation

        if (this.state.expandedPanelWidth <= 0) {
          this.state.expandedPanelAnimating = false;
          this.state.expandedTask = null;
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
    if (this.state.expandedTask) {
      this.state.expandedPanelWidth = 0;
      this.state.expandedTask = null;
      this.parent.resultScrollOffset = 0;
    }
    if (this.state.expandedCombiner) {
      this.state.expandedCombinerPanelWidth = 0;
      this.state.expandedCombiner = null;
    }

    if (this.state.expandedAgent && this.state.expandedAgent.name === agent.name) {
      this.closeAgentPanel();
    } else {
      this.state.agentPanelScrollOffset = 0;
      this.state.agentPanelMaxScroll = 0;

      try {
        const configResponse = await apiGet(`/api/agents/${agent.name}`);
        if (configResponse.ok) {
          const agentConfig = await configResponse.json();
          this.state.expandedAgent = {
            ...agent,
            config: agentConfig
          };
        } else {
          this.state.expandedAgent = {
            ...agent,
            config: null
          };
        }
      } catch {
        this.state.expandedAgent = {
          ...agent,
          config: null
        };
      }

      this.state.expandedAgentPanelAnimating = true;
      this.animateAgentPanel(true);
    }
  }

  /**
   * Close agent panel with animation
   */
  closeAgentPanel() {
    this.state.expandedAgentPanelAnimating = true;
    this.animateAgentPanel(false);
  }

  /**
   * Animate agent panel opening/closing
   */
  animateAgentPanel(expanding) {
    const animate = () => {
      const speed = 30;

      if (expanding) {
        this.state.expandedAgentPanelWidth = Math.min(
          this.state.expandedAgentPanelWidth + speed,
          this.state.expandedAgentPanelTargetWidth
        );

        this.parent.draw();

        if (this.state.expandedAgentPanelWidth >= this.state.expandedAgentPanelTargetWidth) {
          this.state.expandedAgentPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.state.expandedAgentPanelWidth = Math.max(
          this.state.expandedAgentPanelWidth - speed,
          0
        );

        this.parent.draw();

        if (this.state.expandedAgentPanelWidth <= 0) {
          this.state.expandedAgentPanelAnimating = false;
          this.state.expandedAgent = null;
          this.state.agentPanelScrollOffset = 0;
          this.state.agentPanelMaxScroll = 0;
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
    if (this.state.expandedTask) this.closeTaskPanel();
    if (this.state.expandedAgent) this.closeAgentPanel();

    if (this.state.expandedCombiner && this.state.expandedCombiner.id === combiner.id) {
      this.closeCombinerPanel();
      return;
    }

    this.state.expandedCombiner = combiner;
    this.state.expandedCombinerPanelAnimating = true;
    this.animateCombinerPanel(true);
  }

  /**
   * Close combiner panel with animation
   */
  closeCombinerPanel() {
    this.state.expandedCombinerPanelAnimating = true;
    this.animateCombinerPanel(false);
  }

  /**
   * Animate combiner panel opening/closing
   */
  animateCombinerPanel(expanding) {
    const animate = () => {
      const speed = 30;
      if (expanding) {
        this.state.expandedCombinerPanelWidth = Math.min(
          this.state.expandedCombinerPanelWidth + speed,
          this.state.expandedCombinerPanelTargetWidth
        );
        this.parent.draw(); // Redraw canvas to show animation
        if (this.state.expandedCombinerPanelWidth >= this.state.expandedCombinerPanelTargetWidth) {
          this.state.expandedCombinerPanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.state.expandedCombinerPanelWidth = Math.max(
          this.state.expandedCombinerPanelWidth - speed,
          0
        );
        this.parent.draw(); // Redraw canvas to show animation
        if (this.state.expandedCombinerPanelWidth <= 0) {
          this.state.expandedCombinerPanelAnimating = false;
          this.state.expandedCombiner = null;
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
    this.parent.helpOverlayVisible = !this.parent.helpOverlayVisible;
    this.parent.draw();
  }
}
