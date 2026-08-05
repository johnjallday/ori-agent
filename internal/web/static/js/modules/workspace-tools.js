/**
 * Workspace "Find tools" panel controller.
 *
 * Post-create add-on search: lets the user search the marketplaces/registry for
 * MCPs, skills, and plugins matching the workspace's description and add them to
 * the current workspace. This is the relocated, non-blocking home of what used
 * to be the create-modal "Review Setup" step — it is plain marketplace search,
 * not an AI review.
 *
 * Reuses WorkspaceBootstrapReview.mountWorkspaceToolsPanel(); no marketplace
 * calls are made until the user clicks "Find tools".
 */
(function () {
  function workspaceIdFromPath() {
    const match = window.location.pathname.match(/\/workspaces\/([^/]+)/);
    return match ? decodeURIComponent(match[1]) : '';
  }

  async function fetchWorkspaceInput(workspaceId) {
    try {
      const response = await fetch(
        `/api/orchestration/workspace?id=${encodeURIComponent(workspaceId)}`
      );
      if (!response.ok) return {};
      const ws = await response.json();
      const bootstrap = (ws && ws.shared_data && ws.shared_data.workspace_bootstrap) || {};
      return {
        description: ws?.description || bootstrap.goal || '',
        goal: bootstrap.goal || ws?.description || '',
        systems: bootstrap.systems || '',
        context: bootstrap.context || ''
      };
    } catch (_error) {
      return {};
    }
  }

  document.addEventListener('DOMContentLoaded', () => {
    const container = document.getElementById('workspace-tools-panel-host');
    const trigger = document.getElementById('workspace-tools-find-btn');
    const review = window.WorkspaceBootstrapReview;
    if (
      !container ||
      !trigger ||
      !review ||
      typeof review.mountWorkspaceToolsPanel !== 'function'
    ) {
      return;
    }
    const workspaceId = workspaceIdFromPath();
    if (!workspaceId) return;

    let panel = null;
    let inputCache = null;
    const originalLabel = trigger.textContent;

    trigger.addEventListener('click', async () => {
      trigger.disabled = true;
      trigger.textContent = 'Searching…';
      try {
        // Lazily fetch the workspace's description the first time only.
        if (!inputCache) {
          inputCache = await fetchWorkspaceInput(workspaceId);
        }
        if (!panel) {
          panel = review.mountWorkspaceToolsPanel(container, {
            workspaceId,
            getInput: () => inputCache
          });
          container.hidden = false;
        }
        await panel.search();
        trigger.textContent = 'Search again';
      } finally {
        trigger.disabled = false;
        if (trigger.textContent === 'Searching…') trigger.textContent = originalLabel;
      }
    });
  });
})();
