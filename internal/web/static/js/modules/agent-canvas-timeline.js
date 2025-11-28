/**
 * AgentCanvasTimelineManager
 * Manages timeline panel display, events, and animations
 */
export class AgentCanvasTimelineManager {
  /**
   * @param {AgentCanvasState} state - Shared state object
   * @param {AgentCanvas} parent - Parent AgentCanvas instance
   */
  constructor(state, parent) {
    this.state = state;
    this.parent = parent;
  }

  /**
   * Toggle timeline panel visibility
   */
  toggleTimeline() {
    this.state.timelineVisible = !this.state.timelineVisible;
    this.state.timelinePanelAnimating = true;
    this.animateTimelinePanel(this.state.timelineVisible);
  }

  /**
   * Animate timeline panel expansion/collapse
   * @param {boolean} expanding - Whether panel is expanding (true) or collapsing (false)
   */
  animateTimelinePanel(expanding) {
    const animate = () => {
      const speed = 30; // pixels per frame

      if (expanding) {
        this.state.timelinePanelWidth = Math.min(
          this.state.timelinePanelWidth + speed,
          this.state.timelinePanelTargetWidth
        );

        if (this.state.timelinePanelWidth >= this.state.timelinePanelTargetWidth) {
          this.state.timelinePanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      } else {
        this.state.timelinePanelWidth = Math.max(this.state.timelinePanelWidth - speed, 0);

        if (this.state.timelinePanelWidth <= 0) {
          this.state.timelinePanelAnimating = false;
        } else {
          requestAnimationFrame(animate);
        }
      }

      this.parent.draw();
    };

    requestAnimationFrame(animate);
  }

  /**
   * Add an event to the timeline
   * @param {Object} eventData - Event data object
   * @param {string} eventData.type - Event type
   * @param {string} [eventData.id] - Event ID (auto-generated if not provided)
   * @param {string} [eventData.timestamp] - ISO timestamp (auto-generated if not provided)
   * @param {Object} [eventData.data] - Event data payload
   * @param {string} [eventData.source] - Event source (defaults to 'system')
   */
  addTimelineEvent(eventData) {
    // Add event to timeline (prepend to show newest first)
    this.state.timelineEvents.unshift({
      id: eventData.id || Date.now() + Math.random(),
      type: eventData.type,
      timestamp: eventData.timestamp || new Date().toISOString(),
      data: eventData.data || {},
      source: eventData.source || 'system'
    });

    // Limit timeline events to max
    if (this.state.timelineEvents.length > this.state.maxTimelineEvents) {
      this.state.timelineEvents = this.state.timelineEvents.slice(0, this.state.maxTimelineEvents);
    }

    this.parent.draw();
  }
}
