(function() {
  'use strict';

  const STORAGE_KEY = 'oriWorkspaceHubSelectedId';

  const hubEl = document.getElementById('workspaceHub');
  if (!hubEl) return;

  const elements = {
    workspaceSelect: document.getElementById('hubWorkspaceSelect'),
    workspaceBrowseBtn: document.getElementById('hubWorkspaceBrowseBtn'),
    workspaceMeta: document.getElementById('hubWorkspaceMeta'),
    workspaceStatus: document.getElementById('hubWorkspaceStatus'),
    workspaceUpdated: document.getElementById('hubWorkspaceUpdated'),
    workspaceAgents: document.getElementById('hubWorkspaceAgents'),
    workspaceDescription: document.getElementById('hubWorkspaceDescription'),
    workspaceOpenBtn: document.getElementById('hubOpenWorkspaceBtn'),
    workspaceCanvasBtn: document.getElementById('hubOpenCanvasBtn'),
    newTaskBtn: document.getElementById('hubNewTaskBtn'),
    addTaskBtn: document.getElementById('hubAddTaskBtn'),
    refreshTasksBtn: document.getElementById('hubRefreshTasksBtn'),
    tasksList: document.getElementById('hubTasksList'),
    tasksSubtitle: document.getElementById('hubTasksSubtitle'),
    statCompleted: document.getElementById('hubStatCompleted'),
    statInProgress: document.getElementById('hubStatInProgress'),
    statScheduled: document.getElementById('hubStatScheduled'),
    statFailed: document.getElementById('hubStatFailed'),
    schedulesList: document.getElementById('hubSchedulesList'),
    schedulesBtn: document.getElementById('hubSchedulesBtn'),
    viewSchedulesBtn: document.getElementById('hubViewSchedulesBtn'),
    openChatBtn: document.getElementById('hubOpenChatBtn'),
    launcher: document.getElementById('workspaceLauncher'),
    launcherGrid: document.getElementById('launcherGrid'),
    launcherEmpty: document.getElementById('launcherEmptyState'),
    launcherRefreshBtn: document.getElementById('launcherRefreshBtn'),
    loadingOverlay: document.getElementById('workspaceHubLoading')
  };

  const state = {
    workspaces: [],
    workspaceMap: new Map(),
    selectedId: null,
    tasks: [],
    stats: null
  };

  function escapeHtml(text) {
    return String(text || '').replace(/[&<>"]/g, (ch) => ({
      '&': '&amp;',
      '<': '&lt;',
      '>': '&gt;',
      '"': '&quot;'
    }[ch]));
  }

  function formatDate(value) {
    if (!value) return '--';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '--';
    return date.toLocaleString();
  }

  function setState(nextState) {
    hubEl.dataset.state = nextState;
    if (elements.loadingOverlay) {
      elements.loadingOverlay.style.display = nextState === 'loading' ? 'flex' : 'none';
    }
  }

  function flattenWorkspaces(workspaces, depth = 0, path = []) {
    const rows = [];
    workspaces.forEach((workspace) => {
      const currentPath = [...path, workspace.name || 'Untitled'];
      rows.push({
        ...workspace,
        depth,
        path: currentPath.join(' / ')
      });

      if (workspace.children && workspace.children.length > 0) {
        rows.push(...flattenWorkspaces(workspace.children, depth + 1, currentPath));
      }
    });
    return rows;
  }

  function populateWorkspaceSelect(flattened) {
    if (!elements.workspaceSelect) return;

    const options = ['<option value="">Select a workspace...</option>'];
    flattened.forEach((workspace) => {
      const indent = workspace.depth > 0 ? `${'--'.repeat(workspace.depth)} ` : '';
      const label = `${indent}${escapeHtml(workspace.name || 'Untitled Workspace')}`;
      options.push(`<option value="${escapeHtml(workspace.id)}">${label}</option>`);
    });

    elements.workspaceSelect.innerHTML = options.join('');
  }

  function renderLauncher(flattened) {
    if (!elements.launcherGrid || !elements.launcherEmpty) return;

    if (flattened.length === 0) {
      elements.launcherGrid.innerHTML = '';
      elements.launcherEmpty.style.display = 'flex';
      return;
    }

    elements.launcherEmpty.style.display = 'none';

    const cards = flattened.map((workspace) => {
      const description = workspace.description || 'No description yet.';
      const status = workspace.status || 'active';
      const statusLabel = escapeHtml(status.replace('_', ' '));
      const accentStyle = workspace.color ? `style="border-color: ${escapeHtml(workspace.color)}"` : '';

      return `
        <button class="launcher-card-item" data-workspace-id="${escapeHtml(workspace.id)}" ${accentStyle}>
          <div class="launcher-card-title">${escapeHtml(workspace.name || 'Untitled Workspace')}</div>
          <div class="launcher-card-path">${escapeHtml(workspace.path)}</div>
          <div class="launcher-card-description">${escapeHtml(description)}</div>
          <div class="launcher-card-meta">
            <span class="launcher-card-status status-${escapeHtml(status)}">${statusLabel}</span>
            <span>${workspace.session_count || 0} sessions</span>
          </div>
        </button>
      `;
    });

    elements.launcherGrid.innerHTML = cards.join('');

    elements.launcherGrid.querySelectorAll('[data-workspace-id]').forEach((card) => {
      card.addEventListener('click', () => {
        const workspaceId = card.dataset.workspaceId;
        selectWorkspace(workspaceId, { focus: true });
      });
    });
  }

  function renderWorkspaceSummary(workspace) {
    if (!workspace) return;

    if (elements.workspaceMeta) {
      const description = workspace.description ? ` ${workspace.description}` : '';
      elements.workspaceMeta.textContent = `${workspace.name || 'Workspace'}${description ? ` - ${description}` : ''}`;
    }

    if (elements.workspaceStatus) {
      elements.workspaceStatus.textContent = workspace.status || 'active';
    }

    if (elements.workspaceUpdated) {
      elements.workspaceUpdated.textContent = formatDate(workspace.updated_at || workspace.created_at);
    }

    if (elements.workspaceAgents) {
      const agentCount = (workspace.agent_instances && workspace.agent_instances.length) || (workspace.agents && workspace.agents.length) || 0;
      elements.workspaceAgents.textContent = agentCount ? `${agentCount} agents` : 'No agents yet';
    }

    if (elements.workspaceDescription) {
      elements.workspaceDescription.textContent = workspace.description || 'No description';
    }

    if (elements.workspaceOpenBtn) {
      elements.workspaceOpenBtn.href = `/workspaces/${encodeURIComponent(workspace.id)}`;
    }

    if (elements.workspaceCanvasBtn) {
      elements.workspaceCanvasBtn.href = `/workspaces/${encodeURIComponent(workspace.id)}/canvas`;
    }
  }

  function clearWorkspaceSummary() {
    if (elements.workspaceMeta) {
      elements.workspaceMeta.textContent = 'Select a workspace to see tasks and schedules.';
    }

    if (elements.workspaceStatus) elements.workspaceStatus.textContent = '--';
    if (elements.workspaceUpdated) elements.workspaceUpdated.textContent = '--';
    if (elements.workspaceAgents) elements.workspaceAgents.textContent = '--';
    if (elements.workspaceDescription) elements.workspaceDescription.textContent = '--';

    if (elements.workspaceOpenBtn) elements.workspaceOpenBtn.removeAttribute('href');
    if (elements.workspaceCanvasBtn) elements.workspaceCanvasBtn.removeAttribute('href');
  }

  function computeStats(tasks) {
    const stats = {
      completed: 0,
      in_progress: 0,
      failed: 0,
      scheduled: 0
    };

    tasks.forEach((task) => {
      const status = task.status || 'pending';
      if (status === 'completed') stats.completed += 1;
      if (status === 'in_progress') stats.in_progress += 1;
      if (status === 'failed') stats.failed += 1;
      if (task.schedule_enabled) stats.scheduled += 1;
    });

    return stats;
  }

  function renderStats(stats) {
    if (!stats) return;
    if (elements.statCompleted) elements.statCompleted.textContent = stats.completed || 0;
    if (elements.statInProgress) elements.statInProgress.textContent = stats.in_progress || 0;
    if (elements.statFailed) elements.statFailed.textContent = stats.failed || 0;
    if (elements.statScheduled) elements.statScheduled.textContent = stats.scheduled || 0;
  }

  function renderTasksList(tasks) {
    if (!elements.tasksList) return;

    if (!tasks || tasks.length === 0) {
      elements.tasksList.innerHTML = '<div class="hub-empty">No tasks yet. Create the first one to get started.</div>';
      if (elements.tasksSubtitle) {
        elements.tasksSubtitle.textContent = 'No tasks created yet.';
      }
      return;
    }

    if (elements.tasksSubtitle) {
      elements.tasksSubtitle.textContent = `${tasks.length} task${tasks.length === 1 ? '' : 's'} queued for this workspace.`;
    }

    const items = tasks.map((task) => {
      const status = task.status || 'pending';
      const scheduleLabel = task.schedule_enabled ? `Next run: ${formatDate(task.next_run)}` : 'Not scheduled';
      const assignment = task.to || 'unassigned';

      return `
        <div class="hub-task-card" data-task-id="${escapeHtml(task.id)}">
          <div class="hub-task-header">
            <div class="hub-task-title">${escapeHtml(task.name || task.description || task.id)}</div>
            <span class="hub-task-status status-${escapeHtml(status)}">${escapeHtml(status.replace('_', ' '))}</span>
          </div>
          <div class="hub-task-meta">
            <span>${escapeHtml(assignment)}</span>
            <span>${escapeHtml(scheduleLabel)}</span>
          </div>
          <div class="hub-task-actions">
            <button class="modern-btn modern-btn-secondary" data-action="edit">Edit</button>
            <button class="modern-btn modern-btn-secondary" data-action="chat">Open Chat</button>
          </div>
        </div>
      `;
    });

    elements.tasksList.innerHTML = items.join('');

    elements.tasksList.querySelectorAll('.hub-task-card').forEach((card) => {
      const taskId = card.dataset.taskId;
      const task = tasks.find((item) => item.id === taskId);

      card.querySelectorAll('[data-action="edit"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (window.taskModalController && task) {
            window.taskModalController.openForEdit(task, () => loadWorkspaceTasks(state.selectedId));
          }
        });
      });

      card.querySelectorAll('[data-action="chat"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          openChatForWorkspace(state.selectedId);
        });
      });
    });
  }

  function renderSchedules(tasks) {
    if (!elements.schedulesList) return;

    const scheduled = (tasks || []).filter((task) => task.schedule_enabled);

    if (scheduled.length === 0) {
      elements.schedulesList.innerHTML = '<div class="hub-empty">No scheduled tasks yet.</div>';
      return;
    }

    const sorted = scheduled.sort((a, b) => {
      const aTime = a.next_run ? new Date(a.next_run).getTime() : Number.MAX_SAFE_INTEGER;
      const bTime = b.next_run ? new Date(b.next_run).getTime() : Number.MAX_SAFE_INTEGER;
      return aTime - bTime;
    });

    const items = sorted.slice(0, 5).map((task) => {
      const nextRun = task.next_run ? formatDate(task.next_run) : 'Not scheduled';
      return `
        <div class="hub-schedule-item">
          <div>
            <div class="hub-schedule-title">${escapeHtml(task.name || task.description || task.id)}</div>
            <div class="hub-schedule-subtitle">${escapeHtml(nextRun)}</div>
          </div>
          <span class="hub-schedule-status">${escapeHtml(task.status || 'pending')}</span>
        </div>
      `;
    });

    elements.schedulesList.innerHTML = items.join('');
  }

  async function loadWorkspaceTasks(workspaceId) {
    if (!workspaceId) return;

    if (elements.tasksList) {
      elements.tasksList.innerHTML = '<div class="hub-loading">Loading tasks...</div>';
    }

    try {
      const response = await fetch(`/api/orchestration/tasks?studio_id=${encodeURIComponent(workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load tasks');

      const data = await response.json();
      state.tasks = data.tasks || [];
      const computed = computeStats(state.tasks);
      state.stats = { ...computed, ...(data.stats || {}) };
      if (state.stats.scheduled === undefined) state.stats.scheduled = computed.scheduled;

      renderStats(state.stats);
      renderTasksList(state.tasks);
      renderSchedules(state.tasks);
    } catch (error) {
      console.error('Workspace hub failed to load tasks:', error);
      if (elements.tasksList) {
        elements.tasksList.innerHTML = '<div class="hub-empty">Unable to load tasks right now.</div>';
      }
      if (elements.schedulesList) {
        elements.schedulesList.innerHTML = '<div class="hub-empty">Unable to load schedules right now.</div>';
      }
    }
  }

  function selectWorkspace(workspaceId, { focus = false } = {}) {
    const workspace = state.workspaceMap.get(workspaceId);
    if (!workspace) return;

    state.selectedId = workspaceId;
    sessionStorage.setItem(STORAGE_KEY, workspaceId);

    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = workspaceId;
    }

    renderWorkspaceSummary(workspace);
    setState('selected');
    loadWorkspaceTasks(workspaceId);

    if (focus && elements.workspaceSelect) {
      elements.workspaceSelect.blur();
    }
  }

  function showLauncher() {
    setState('launcher');
    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = '';
    }
    state.selectedId = null;
    sessionStorage.removeItem(STORAGE_KEY);
    clearWorkspaceSummary();
    renderStats({ completed: 0, in_progress: 0, failed: 0, scheduled: 0 });
    if (elements.tasksList) {
      elements.tasksList.innerHTML = '<div class=\"hub-empty\">Select a workspace to view tasks.</div>';
    }
    if (elements.tasksSubtitle) {
      elements.tasksSubtitle.textContent = 'Select a workspace to see task activity.';
    }
    if (elements.schedulesList) {
      elements.schedulesList.innerHTML = '<div class=\"hub-empty\">Select a workspace to view schedules.</div>';
    }
  }

  async function loadWorkspaces() {
    setState('loading');

    try {
      const response = await fetch('/api/workspaces?tree=true');
      if (!response.ok) throw new Error('Failed to load workspaces');

      const data = await response.json();
      state.workspaces = data.folders || [];

      const flattened = flattenWorkspaces(state.workspaces);
      state.workspaceMap = new Map(flattened.map((workspace) => [workspace.id, workspace]));

      populateWorkspaceSelect(flattened);
      renderLauncher(flattened);

      const saved = sessionStorage.getItem(STORAGE_KEY);
      if (saved && state.workspaceMap.has(saved)) {
        selectWorkspace(saved);
        return;
      }

      showLauncher();
    } catch (error) {
      console.error('Workspace hub failed to load workspaces:', error);
      showLauncher();
    }
  }

  function openChatForWorkspace(workspaceId) {
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }

    if (workspaceId && window.sessionManager && typeof window.sessionManager.getActiveSessionId === 'function') {
      const activeSession = window.sessionManager.getActiveSessionId();
      if (!activeSession && typeof window.sessionManager.showCreateChatModalForWorkspace === 'function') {
        window.sessionManager.showCreateChatModalForWorkspace(workspaceId);
      }
    }
  }

  function openSchedulePanel() {
    if (!state.selectedId) return;
    if (window.sessionManager && typeof window.sessionManager.openScheduledTasksPanel === 'function') {
      window.sessionManager.openScheduledTasksPanel(state.selectedId);
    }
  }

  function openTaskModal() {
    if (!state.selectedId) return;
    if (window.taskModalController) {
      window.taskModalController.openForCreate(state.selectedId, '', () => loadWorkspaceTasks(state.selectedId));
    }
  }

  function bindEvents() {
    if (elements.workspaceSelect) {
      elements.workspaceSelect.addEventListener('change', (event) => {
        const workspaceId = event.target.value;
        if (workspaceId) {
          selectWorkspace(workspaceId, { focus: true });
        } else {
          showLauncher();
        }
      });
    }

    if (elements.workspaceBrowseBtn) {
      elements.workspaceBrowseBtn.addEventListener('click', () => showLauncher());
    }

    if (elements.newTaskBtn) {
      elements.newTaskBtn.addEventListener('click', openTaskModal);
    }

    if (elements.addTaskBtn) {
      elements.addTaskBtn.addEventListener('click', openTaskModal);
    }

    if (elements.refreshTasksBtn) {
      elements.refreshTasksBtn.addEventListener('click', () => {
        if (state.selectedId) {
          loadWorkspaceTasks(state.selectedId);
        }
      });
    }

    if (elements.schedulesBtn) {
      elements.schedulesBtn.addEventListener('click', openSchedulePanel);
    }

    if (elements.viewSchedulesBtn) {
      elements.viewSchedulesBtn.addEventListener('click', openSchedulePanel);
    }

    if (elements.openChatBtn) {
      elements.openChatBtn.addEventListener('click', () => openChatForWorkspace(state.selectedId));
    }

    if (elements.launcherRefreshBtn) {
      elements.launcherRefreshBtn.addEventListener('click', () => loadWorkspaces());
    }
  }

  bindEvents();
  loadWorkspaces();
})();
