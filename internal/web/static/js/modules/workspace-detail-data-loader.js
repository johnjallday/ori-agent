/**
 * Transport and snapshot projection for workspace-detail Tasks and Backlog reads.
 *
 * The controller owns request ordering, cancellation, publication, and rendering.
 * This loader retains no page state: every call receives its workspace UUID,
 * signal, and (for Backlog) the current descendant filter explicitly.
 */
export function createWorkspaceDetailDataLoader(fetchImpl = (...args) => fetch(...args)) {
  async function loadJSON(url, signal, failureMessage) {
    const response = await fetchImpl(url, { signal });
    if (!response.ok) throw new Error(failureMessage);
    return response.json();
  }

  return {
    async loadTasks({ workspaceId, signal }) {
      const data = await loadJSON(
        `/api/orchestration/tasks?workspace_id=${encodeURIComponent(workspaceId)}`,
        signal,
        'Failed to load tasks'
      );
      return { tasks: data.tasks || [] };
    },

    async loadBacklog({ workspaceId, signal, includeDescendants = false }) {
      const params = new URLSearchParams({ workspace_id: workspaceId });
      if (includeDescendants) params.set('include_descendants', 'true');
      const data = await loadJSON(
        `/api/orchestration/backlog?${params.toString()}`,
        signal,
        'Failed to load backlog'
      );
      return {
        items: Array.isArray(data.items) ? data.items : [],
        sync: data.sync || null
      };
    }
  };
}
