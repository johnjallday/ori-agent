/**
 * Agent Canvas Renderer (Orchestrator)
 *
 * Main rendering orchestrator that delegates to specialized renderer modules.
 * This class coordinates all drawing operations and maintains the main draw loop.
 */

import { RendererPrimitives } from './renderer-primitives.js';
import { RendererConnections } from './renderer-connections.js';
import { RendererNodes } from './renderer-nodes.js';
import { RendererPanels } from './renderer-panels.js';
import { RendererUI } from './renderer-ui.js';

export class AgentCanvasRenderer {
  /**
   * Create a renderer instance
   * @param {CanvasRenderingContext2D} ctx - Canvas 2D context
   * @param {AgentCanvasState} state - State module with all canvas data
   * @param {HTMLCanvasElement} canvas - Canvas element reference
   * @param {AgentCanvas} parent - Parent AgentCanvas instance for helper methods
   */
  constructor(ctx, state, canvas, parent) {
    this.ctx = ctx;
    this.state = state;
    this.canvas = canvas;
    this.parent = parent;

    // Initialize specialized renderers
    this.primitives = new RendererPrimitives(ctx);
    this.connections = new RendererConnections(ctx, state, canvas, parent, this.primitives);
    this.nodes = new RendererNodes(ctx, state, canvas, parent, this.primitives);
    this.panels = new RendererPanels(ctx, state, canvas, parent, this.primitives);
    this.ui = new RendererUI(ctx, state, canvas, parent, this.primitives);
  }

  // ==================== DELEGATION METHODS ====================
  // These methods delegate to the appropriate specialized renderer

  // Primitives
  roundRect(x, y, width, height, radius) {
    return this.primitives.roundRect(x, y, width, height, radius);
  }

  wrapText(text, maxWidth) {
    return this.primitives.wrapText(text, maxWidth);
  }

  drawArrow(x1, y1, x2, y2, color, lineWidth = 2, filled = true) {
    return this.primitives.drawArrow(x1, y1, x2, y2, color, lineWidth, filled);
  }

  drawPort(x, y, type, color, orientation = 'auto') {
    return this.primitives.drawPort(x, y, type, color, orientation);
  }

  // Connections
  drawConnections() {
    return this.connections.drawConnections();
  }

  drawResultConnections() {
    return this.connections.drawResultConnections();
  }

  drawParticles() {
    return this.connections.drawParticles();
  }

  drawChainConnections() {
    return this.connections.drawChainConnections();
  }

  drawChainParticles() {
    return this.connections.drawChainParticles();
  }

  drawAssignmentLine() {
    return this.connections.drawAssignmentLine();
  }

  drawWorkflowConnections() {
    return this.connections.drawWorkflowConnections();
  }

  drawDraggingConnection() {
    return this.connections.drawDraggingConnection();
  }

  // Nodes
  drawTaskFlows() {
    return this.nodes.drawTaskFlows();
  }

  drawAgents() {
    return this.nodes.drawAgents();
  }

  drawCombinerNodes() {
    return this.nodes.drawCombinerNodes();
  }

  // Panels
  drawExpandedTaskPanel() {
    return this.panels.drawExpandedTaskPanel();
  }

  drawExpandedAgentPanel() {
    return this.panels.drawExpandedAgentPanel();
  }

  drawExpandedCombinerPanel() {
    return this.panels.drawExpandedCombinerPanel();
  }

  drawTimelinePanel() {
    return this.panels.drawTimelinePanel();
  }

  drawEmptyTimeline() {
    return this.panels.drawEmptyTimeline();
  }

  drawTimelineEvent(event, x, y, width) {
    return this.panels.drawTimelineEvent(event, x, y, width);
  }

  // UI
  drawWorkspaceProgress() {
    return this.ui.drawWorkspaceProgress();
  }

  drawMission() {
    return this.ui.drawMission();
  }

  drawCreateTaskButton() {
    return this.ui.drawCreateTaskButton();
  }

  drawAddAgentButton() {
    return this.ui.drawAddAgentButton();
  }

  drawNotifications() {
    return this.ui.drawNotifications();
  }

  drawTimelineToggleButton() {
    return this.ui.drawTimelineToggleButton();
  }

  drawAutoLayoutButton() {
    return this.ui.drawAutoLayoutButton();
  }

  drawSaveLayoutButton() {
    return this.ui.drawSaveLayoutButton();
  }

  drawContextMenu() {
    return this.ui.drawContextMenu();
  }

  drawHelpOverlay() {
    return this.ui.drawHelpOverlay();
  }
}
