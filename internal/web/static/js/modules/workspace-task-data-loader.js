import { fetchRelatedPlan } from './workspace-related-plan.js';

function throwIfAborted(signal) {
  if (signal?.aborted) {
    throw signal.reason || new DOMException('This operation was aborted.', 'AbortError');
  }
}

function workspaceRunTimestamp(run) {
  const raw = run?.finished_at || run?.started_at || run?.created_at || '';
  const parsed = new Date(raw).getTime();
  return Number.isFinite(parsed) ? parsed : 0;
}

function taskSnapshot(workspace, tasks, task, workspaceRuns) {
  const runId = String(task?.current_run_id || '').trim();
  return {
    workspace,
    tasks,
    task,
    workspaceRuns,
    currentRun: runId ? workspaceRuns.find(run => run?.id === runId) || null : null
  };
}

// This loader owns only transport and snapshot assembly. Every call supplies
// its identity, cancellation signal, and (for refreshes) fallback snapshot.
// It never retains page state, publishes results, renders, or cancels another
// request. The page controller owns request ordering and teardown.
export function createTaskPageDataLoader(fetchImpl = (...args) => fetch(...args)) {
  const workspaceURL = workspaceId => `/api/workspaces/${encodeURIComponent(workspaceId)}`;

  async function fetchWorkspace({ workspaceId, signal }) {
    const response = await fetchImpl(workspaceURL(workspaceId), { signal });
    if (!response.ok) throw new Error('Failed to load workspace details.');
    return response.json();
  }

  async function fetchTask({ taskId, signal }) {
    const response = await fetchImpl(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`, {
      signal
    });
    if (response.status === 404) return null;
    if (!response.ok) throw new Error('Failed to load task details.');
    return response.json();
  }

  async function fetchAgents({ signal }) {
    const response = await fetchImpl('/api/agents', { signal });
    if (!response.ok) throw new Error('Failed to load agent list.');
    const payload = await response.json();
    return Array.isArray(payload?.agents) ? payload.agents : [];
  }

  async function fetchTaskEvents({ workspaceId, taskId, signal }) {
    const params = new URLSearchParams({
      workspace_id: workspaceId,
      task_id: taskId,
      limit: '200'
    });
    const response = await fetchImpl(`/api/orchestration/events?${params.toString()}`, { signal });
    if (!response.ok) return [];
    const payload = await response.json().catch(() => ({}));
    return Array.isArray(payload?.events) ? payload.events : [];
  }

  // Show where the workspace's default output folder actually writes.
  async function fetchWorkspaceOutputDir({ workspaceId, signal }) {
    const response = await fetchImpl(`${workspaceURL(workspaceId)}/output-dir`, { signal });
    if (!response.ok) return '';
    const payload = await response.json();
    return String(payload?.output_dir || '').trim();
  }

  async function fetchCurrentRun({ workspaceId, signal }, runId) {
    const response = await fetchImpl(
      `${workspaceURL(workspaceId)}/runs/${encodeURIComponent(runId)}`,
      { signal }
    );
    if (response.status === 404) return null;
    if (!response.ok) throw new Error('Failed to load latest workspace run.');
    return response.json();
  }

  async function fetchWorkspaceRunsForTask(request, task) {
    const runIds = new Set();
    const currentRunId = String(task?.current_run_id || '').trim();
    if (currentRunId) runIds.add(currentRunId);
    for (const entry of Array.isArray(task?.execution_history) ? task.execution_history : []) {
      const runId = String(entry?.run_id || '').trim();
      if (runId) runIds.add(runId);
    }
    const runs = await Promise.all(
      [...runIds].map(runId => fetchCurrentRun(request, runId).catch(() => null))
    );
    return runs
      .filter(Boolean)
      .sort((left, right) => workspaceRunTimestamp(right) - workspaceRunTimestamp(left));
  }

  return {
    async loadSnapshot({ workspaceId, taskId, signal }) {
      throwIfAborted(signal);
      const request = { workspaceId, taskId, signal };
      const [workspace, taskResponse, agents, taskEvents, outputDir] = await Promise.all([
        fetchWorkspace(request),
        fetchTask(request),
        fetchAgents(request).catch(() => []),
        fetchTaskEvents(request).catch(() => []),
        fetchWorkspaceOutputDir(request).catch(() => '')
      ]);
      throwIfAborted(signal);
      let tasks = Array.isArray(workspace?.tasks) ? workspace.tasks : [];
      const workspaceTask = tasks.find(item => String(item?.id || '') === taskId) || null;
      const candidate = taskResponse || workspaceTask;
      const task =
        candidate && String(candidate.workspace_id || workspaceId) === workspaceId
          ? candidate
          : null;
      if (task && !workspaceTask) tasks = [task];
      const runs = task ? await fetchWorkspaceRunsForTask(request, task).catch(() => []) : [];
      throwIfAborted(signal);
      return {
        ...taskSnapshot(workspace || null, tasks, task, runs),
        workspaceOutputDir: outputDir || '',
        availableAgents: Array.isArray(agents) ? agents : [],
        taskEvents: Array.isArray(taskEvents) ? taskEvents : []
      };
    },

    async refreshSnapshot({ workspaceId, taskId, signal, previousSnapshot = {} }) {
      throwIfAborted(signal);
      const request = { workspaceId, taskId, signal };
      const [workspace, taskResponse] = await Promise.all([
        fetchWorkspace(request),
        fetchTask(request).catch(() => null)
      ]);
      throwIfAborted(signal);
      const tasks = Array.isArray(workspace?.tasks)
        ? workspace.tasks
        : previousSnapshot.tasks || [];
      const workspaceTask = tasks.find(item => String(item?.id || '') === taskId);
      const task = taskResponse || workspaceTask || previousSnapshot.task || null;
      const runs = await fetchWorkspaceRunsForTask(request, task).catch(() => []);
      throwIfAborted(signal);
      return taskSnapshot(workspace || previousSnapshot.workspace || null, tasks, task, runs);
    },

    async loadRelatedPlan({ workspaceId, taskId, signal }) {
      if (signal?.aborted) return null;
      return fetchRelatedPlan(workspaceId, 'task', taskId, url => fetchImpl(url, { signal }));
    }
  };
}
