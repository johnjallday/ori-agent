/*
 * workspace-command.js — Workspace Command view (interior "tactical" reskin)
 *
 * An opt-in tactical layout on the workspace detail page, beside the existing
 * detailed view. It reuses the live WorkspaceDetailPage instance (data + helpers
 * like buildAgentGroups / isWorkspaceEntryAgent) and renders into
 * #workspaceCommandView — no backend, no rewrite of the detailed page.
 *
 * Phase 0.0 = inert scaffolding. mount()/unmount() are safe no-ops; the command
 * bar, garrison, quest logs, and right rail land in Phase 1-3.
 */
export class WorkspaceCommandView {
  /**
   * @param {object} page - the live WorkspaceDetailPage instance (window.workspaceDetail).
   */
  constructor(page) {
    this.page = page || null;
    this.container = document.getElementById('workspaceCommandView');
    this.detailedView = document.getElementById('workspace-detail-view');
    this.active = false;
  }

  /** Render/refresh the command view into its container. Inert until Phase 1. */
  mount() {
    if (!this.container) return;
    // Intentionally renders nothing yet.
  }

  /** Tear down the command view. Inert until Phase 1. */
  unmount() {
    if (!this.container) return;
  }
}
