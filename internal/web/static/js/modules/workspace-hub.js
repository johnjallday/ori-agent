(function() {
  'use strict';

  const STORAGE_KEY = 'oriWorkspaceHubSelectedId';
  const SMART_INPUT_CLASSIFY_ENDPOINT = '/api/smart-input/classify';
  const SMART_INPUT_OVERRIDE_ENDPOINT = '/api/smart-input/override';

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
    workspaceCanvasBtn: document.getElementById('hubOpenCanvasBtn'),
    addTaskBtn: document.getElementById('hubAddTaskBtn'),
    refreshTasksBtn: document.getElementById('hubRefreshTasksBtn'),
    tasksList: document.getElementById('hubTasksList'),
    tasksSubtitle: document.getElementById('hubTasksSubtitle'),
    statCompleted: document.getElementById('hubStatCompleted'),
    statInProgress: document.getElementById('hubStatInProgress'),
    statScheduled: document.getElementById('hubStatScheduled'),
    statFailed: document.getElementById('hubStatFailed'),
    schedulesList: document.getElementById('hubSchedulesList'),
    viewSchedulesBtn: document.getElementById('hubViewSchedulesBtn'),
    launcher: document.getElementById('workspaceLauncher'),
    launcherGrid: document.getElementById('launcherGrid'),
    launcherEmpty: document.getElementById('launcherEmptyState'),
    launcherRefreshBtn: document.getElementById('launcherRefreshBtn'),
    loadingOverlay: document.getElementById('workspaceHubLoading'),
    sessionsList: document.getElementById('hubSessionsList'),
    newSessionBtn: document.getElementById('hubNewSessionBtn'),
    notesList: document.getElementById('hubNotesList'),
    newNoteBtn: document.getElementById('hubNewNoteBtn'),
    filesList: document.getElementById('hubFilesList'),
    addFileBtn: document.getElementById('hubAddFileBtn'),
    smartInputCard: document.getElementById('hubSmartInput'),
    smartInputField: document.getElementById('hubSmartInputField'),
    smartInputSubmit: document.getElementById('hubSmartInputSubmit'),
    smartInputStatus: document.getElementById('hubSmartInputStatus'),
    smartInputPrompt: document.getElementById('hubSmartInputPrompt'),
    smartInputPromptHint: document.getElementById('hubSmartInputPromptHint'),
    smartInputPromptTask: document.getElementById('hubSmartInputPromptTask'),
    smartInputPromptChat: document.getElementById('hubSmartInputPromptChat'),
    smartInputPromptCancel: document.getElementById('hubSmartInputPromptCancel')
  };

  const state = {
    workspaces: [],
    workspaceMap: new Map(),
    selectedId: null,
    tasks: [],
    stats: null,
    sessions: [],
    notes: [],
    files: [],
    smartInput: null
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

  function setSmartInputStatus(message, { busy = false } = {}) {
    if (!elements.smartInputStatus) return;
    elements.smartInputStatus.textContent = message || '';
    elements.smartInputStatus.classList.toggle('is-busy', busy);
  }

  function setSmartInputEnabled(enabled) {
    if (elements.smartInputField) elements.smartInputField.disabled = !enabled;
    if (elements.smartInputSubmit) elements.smartInputSubmit.disabled = !enabled;
    if (elements.smartInputCard) {
      elements.smartInputCard.classList.toggle('is-disabled', !enabled);
    }
  }

  function setSmartInputBusy(isBusy, message) {
    if (elements.smartInputField) elements.smartInputField.disabled = isBusy;
    if (elements.smartInputSubmit) elements.smartInputSubmit.disabled = isBusy;
    if (elements.smartInputCard) {
      elements.smartInputCard.dataset.state = isBusy ? 'deciding' : 'idle';
    }
    if (message !== undefined) {
      setSmartInputStatus(message, { busy: isBusy });
    } else if (isBusy) {
      setSmartInputStatus('Deciding...', { busy: true });
    } else {
      setSmartInputStatus('', { busy: false });
    }
  }

  function setSmartInputDefaultDecision(decision) {
    const isTask = decision === 'task';
    const isChat = decision === 'chat';

    if (elements.smartInputPromptTask) {
      elements.smartInputPromptTask.classList.toggle('is-default', isTask);
    }
    if (elements.smartInputPromptChat) {
      elements.smartInputPromptChat.classList.toggle('is-default', isChat);
    }
    if (elements.smartInputPromptHint) {
      if (isTask) {
        elements.smartInputPromptHint.textContent = 'Suggested: Create Task';
      } else if (isChat) {
        elements.smartInputPromptHint.textContent = 'Suggested: Start Chat';
      } else {
        elements.smartInputPromptHint.textContent = '';
      }
    }
  }

  function hideSmartInputPrompt() {
    if (elements.smartInputPrompt) {
      elements.smartInputPrompt.hidden = true;
    }
  }

  function resetSmartInputPrompt() {
    state.smartInput = null;
    hideSmartInputPrompt();
    setSmartInputDefaultDecision(null);
  }

  function showSmartInputPrompt(payload) {
    if (!elements.smartInputPrompt) return;
    state.smartInput = {
      input: payload.input,
      predictedDecision: payload.decision,
      confidence: payload.confidence || 0,
      method: payload.method || 'fallback'
    };
    setSmartInputDefaultDecision(payload.decision);
    elements.smartInputPrompt.hidden = false;
    setSmartInputStatus(payload.message || 'Choose where to route this.', { busy: false });
  }

  function clearSmartInputField() {
    if (elements.smartInputField) {
      elements.smartInputField.value = '';
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

  function renderSessions(sessions) {
    if (!elements.sessionsList) return;

    if (!sessions || sessions.length === 0) {
      elements.sessionsList.innerHTML = '<div class="hub-empty">No chat sessions yet.</div>';
      return;
    }

    const items = sessions.map((session) => {
      const title = session.title || session.name || 'Untitled Chat';
      const agent = session.agent_name || 'default';
      const updated = formatDate(session.updated_at || session.created_at);
      const messageCount = session.message_count || 0;

      return `
        <div class="hub-session-item" data-session-id="${escapeHtml(session.id)}">
          <div class="hub-session-info">
            <div class="hub-session-title">${escapeHtml(title)}</div>
            <div class="hub-session-meta">
              <span class="hub-session-agent">${escapeHtml(agent)}</span>
              <span>${messageCount} message${messageCount === 1 ? '' : 's'}</span>
              <span>${escapeHtml(updated)}</span>
            </div>
          </div>
          <button class="modern-btn modern-btn-secondary hub-session-open" data-action="open">Open</button>
        </div>
      `;
    });

    elements.sessionsList.innerHTML = items.join('');

    elements.sessionsList.querySelectorAll('.hub-session-item').forEach((item) => {
      const sessionId = item.dataset.sessionId;

      item.querySelector('[data-action="open"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        openSession(sessionId);
      });

      item.addEventListener('click', () => openSession(sessionId));
    });
  }

  async function loadWorkspaceSessions(workspaceId) {
    if (!workspaceId) return;

    if (elements.sessionsList) {
      elements.sessionsList.innerHTML = '<div class="hub-loading">Loading sessions...</div>';
    }

    try {
      const response = await fetch(`/api/sessions?folder_id=${encodeURIComponent(workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load sessions');

      const data = await response.json();
      state.sessions = data.sessions || [];
      renderSessions(state.sessions);
    } catch (error) {
      console.error('Workspace hub failed to load sessions:', error);
      if (elements.sessionsList) {
        elements.sessionsList.innerHTML = '<div class="hub-empty">Unable to load sessions.</div>';
      }
    }
  }

  function openSession(sessionId) {
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }

    if (window.sessionManager && typeof window.sessionManager.switchToSession === 'function') {
      window.sessionManager.switchToSession(sessionId);
    }
  }

  function renderNotes(notes) {
    if (!elements.notesList) return;

    if (!notes || notes.length === 0) {
      elements.notesList.innerHTML = '<div class="hub-empty">No notes yet.</div>';
      return;
    }

    const items = notes.slice(0, 5).map((note) => {
      const title = note.name || 'Untitled Note';
      const updated = formatDate(note.updated_at || note.created_at);
      const preview = note.content ? note.content.substring(0, 80).replace(/\n/g, ' ') : '';

      return `
        <div class="hub-note-item" data-note-id="${escapeHtml(note.id)}">
          <div class="hub-note-info">
            <div class="hub-note-title">${escapeHtml(title)}</div>
            <div class="hub-note-preview">${escapeHtml(preview)}${note.content && note.content.length > 80 ? '...' : ''}</div>
            <div class="hub-note-meta">${escapeHtml(updated)}</div>
          </div>
          <button class="modern-btn modern-btn-secondary hub-note-open" data-action="open">Open</button>
        </div>
      `;
    });

    elements.notesList.innerHTML = items.join('');

    elements.notesList.querySelectorAll('.hub-note-item').forEach((item) => {
      const noteId = item.dataset.noteId;

      item.querySelector('[data-action="open"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        openNote(noteId);
      });

      item.addEventListener('click', () => openNote(noteId));
    });
  }

  async function loadWorkspaceNotes(workspaceId) {
    if (!workspaceId) return;

    if (elements.notesList) {
      elements.notesList.innerHTML = '<div class="hub-loading">Loading notes...</div>';
    }

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`);
      if (!response.ok) throw new Error('Failed to load notes');

      const data = await response.json();
      state.notes = data.notes || [];
      renderNotes(state.notes);
    } catch (error) {
      console.error('Workspace hub failed to load notes:', error);
      if (elements.notesList) {
        elements.notesList.innerHTML = '<div class="hub-empty">Unable to load notes.</div>';
      }
    }
  }

  function openNote(noteId) {
    if (window.sessionManager && typeof window.sessionManager.openNoteEditorModal === 'function') {
      const note = state.notes.find(n => n.id === noteId);
      if (note) {
        window.sessionManager.openNoteEditorModal(note);
      }
    }
  }

  function createNewNote() {
    if (!state.selectedId) return;
    if (window.sessionManager && typeof window.sessionManager.openNoteCreateModal === 'function') {
      window.sessionManager.openNoteCreateModal(state.selectedId);
    }
  }

  function renderFiles(files) {
    if (!elements.filesList) return;

    if (!files || files.length === 0) {
      elements.filesList.innerHTML = '<div class="hub-empty">No files yet.</div>';
      return;
    }

    const getFileIcon = (type, mime) => {
      if (type === 'image' || (mime && mime.startsWith('image/'))) {
        return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8.5,13.5L11,16.5L14.5,12L19,18H5M21,19V5C21,3.89 20.1,3 19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19Z"/></svg>';
      }
      if (type === 'doc' || (mime && (mime.includes('text') || mime.includes('document')))) {
        return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M14,17H7V15H14M17,13H7V11H17M17,9H7V7H17M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3Z"/></svg>';
      }
      return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M13,9V3.5L18.5,9M6,2C4.89,2 4,2.89 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2H6Z"/></svg>';
    };

    const formatFileSize = (bytes) => {
      if (!bytes) return '';
      if (bytes < 1024) return bytes + ' B';
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
      return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    };

    const items = files.slice(0, 5).map((file) => {
      const title = file.title || (file.file_meta && file.file_meta.name) || 'Untitled File';
      const size = file.file_meta ? formatFileSize(file.file_meta.size) : '';
      const icon = getFileIcon(file.type, file.file_meta?.mime);

      return `
        <div class="hub-file-item" data-file-id="${escapeHtml(file.id)}">
          <div class="hub-file-icon">${icon}</div>
          <div class="hub-file-info">
            <div class="hub-file-title">${escapeHtml(title)}</div>
            ${size ? `<div class="hub-file-meta">${escapeHtml(size)}</div>` : ''}
          </div>
        </div>
      `;
    });

    elements.filesList.innerHTML = items.join('');

    elements.filesList.querySelectorAll('.hub-file-item').forEach((item) => {
      item.addEventListener('click', () => {
        const fileId = item.dataset.fileId;
        openFile(fileId);
      });
    });
  }

  function openFile(fileId) {
    // Open the canvas to view/edit the file attachment
    if (state.selectedId) {
      window.location.href = `/workspaces/${encodeURIComponent(state.selectedId)}/canvas`;
    }
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
    setSmartInputEnabled(true);
    resetSmartInputPrompt();
    setSmartInputStatus('', { busy: false });
    loadWorkspaceTasks(workspaceId);
    loadWorkspaceSessions(workspaceId);
    loadWorkspaceNotes(workspaceId);
    loadWorkspaceFiles(workspaceId);

    if (focus && elements.workspaceSelect) {
      elements.workspaceSelect.blur();
    }
  }

  async function loadWorkspaceFiles(workspaceId) {
    if (!workspaceId) return;

    if (elements.filesList) {
      elements.filesList.innerHTML = '<div class="hub-loading">Loading files...</div>';
    }

    try {
      // Fetch full workspace data to get attachments
      const response = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load workspace');

      const workspace = await response.json();
      // Filter attachments that have file metadata (actual files, not just notes)
      state.files = (workspace.attachments || []).filter(a => a.file_meta || a.type === 'image' || a.type === 'other');
      renderFiles(state.files);
    } catch (error) {
      console.error('Workspace hub failed to load files:', error);
      if (elements.filesList) {
        elements.filesList.innerHTML = '<div class="hub-empty">Unable to load files.</div>';
      }
    }
  }

  function showLauncher() {
    setState('launcher');
    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = '';
    }
    state.selectedId = null;
    setSmartInputEnabled(false);
    resetSmartInputPrompt();
    clearSmartInputField();
    setSmartInputStatus('Select a workspace to use quick add.', { busy: false });
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
    if (elements.sessionsList) {
      elements.sessionsList.innerHTML = '<div class=\"hub-empty\">Select a workspace to view sessions.</div>';
    }
    if (elements.notesList) {
      elements.notesList.innerHTML = '<div class="hub-empty">Select a workspace to view notes.</div>';
    }
    if (elements.filesList) {
      elements.filesList.innerHTML = '<div class="hub-empty">Select a workspace to view files.</div>';
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

  async function classifySmartInput(input) {
    const response = await fetch(SMART_INPUT_CLASSIFY_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: state.selectedId,
        input
      })
    });

    if (!response.ok) {
      const errText = await response.text();
      throw new Error(errText || 'Failed to classify input');
    }

    return response.json();
  }

  async function logSmartInputOverride(payload) {
    try {
      await fetch(SMART_INPUT_OVERRIDE_ENDPOINT, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: payload.workspaceId,
          input: payload.input,
          predicted_decision: payload.predictedDecision,
          selected_decision: payload.selectedDecision,
          confidence: payload.confidence,
          method: payload.method
        })
      });
    } catch (error) {
      console.warn('Failed to log smart input override:', error);
    }
  }

  async function createTaskFromSmartInput(input) {
    const response = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        studio_id: state.selectedId,
        description: input,
        details: '',
        priority: 3
      })
    });

    if (!response.ok) {
      const errText = await response.text();
      throw new Error(errText || 'Failed to create task');
    }

    await loadWorkspaceTasks(state.selectedId);
  }

  async function createChatFromSmartInput(input) {
    if (!window.sessionManager || typeof window.sessionManager.fetchAgents !== 'function') {
      throw new Error('Chat manager not available');
    }

    const agents = await window.sessionManager.fetchAgents();
    if (!agents || agents.length === 0) {
      throw new Error('No agents available');
    }

    const agentName = agents[0].name;
    if (typeof window.sessionManager.createSessionWithAgentInFolder === 'function') {
      await window.sessionManager.createSessionWithAgentInFolder(agentName, state.selectedId);
    } else if (typeof window.sessionManager.createSessionWithAgent === 'function') {
      await window.sessionManager.createSessionWithAgent(agentName);
    }

    if (window.sendMessageToChat) {
      setTimeout(() => window.sendMessageToChat(input), 100);
    }

    await loadWorkspaceSessions(state.selectedId);
  }

  async function handleSmartInputDecision(decision, classification = null) {
    const meta = classification || state.smartInput || {};
    const input = meta.input || (elements.smartInputField ? elements.smartInputField.value.trim() : '');
    if (!input) return;

    const predictedDecision = meta.decision || meta.predictedDecision || decision;
    const confidence = meta.confidence || 0;
    const method = meta.method || 'fallback';

    hideSmartInputPrompt();
    setSmartInputBusy(true, decision === 'task' ? 'Creating task...' : 'Starting chat...');

    try {
      if (decision === 'task') {
        await createTaskFromSmartInput(input);
        setSmartInputBusy(false, 'Task created.');
      } else {
        await createChatFromSmartInput(input);
        setSmartInputBusy(false, 'Chat started.');
      }

      clearSmartInputField();
    } catch (error) {
      console.error('Smart input routing failed:', error);
      setSmartInputBusy(false, 'Something went wrong. Try again.');
      if (window.Toast) {
        window.Toast.error(decision === 'task' ? 'Failed to create task' : 'Failed to start chat');
      }
    }

    if (predictedDecision && predictedDecision !== decision) {
      void logSmartInputOverride({
        workspaceId: state.selectedId,
        input,
        predictedDecision,
        selectedDecision: decision,
        confidence,
        method
      });
    }

    state.smartInput = null;
  }

  async function submitSmartInput() {
    if (!elements.smartInputField) return;

    const input = elements.smartInputField.value.trim();
    if (!input) {
      setSmartInputStatus('Type something to get started.', { busy: false });
      return;
    }

    if (!state.selectedId) {
      setSmartInputStatus('Select a workspace first.', { busy: false });
      return;
    }

    resetSmartInputPrompt();
    setSmartInputBusy(true, 'Deciding...');

    let classification;
    try {
      classification = await classifySmartInput(input);
    } catch (error) {
      console.error('Smart input classification failed:', error);
      setSmartInputBusy(false);
      showSmartInputPrompt({
        input,
        decision: 'task',
        confidence: 0,
        method: 'fallback',
        message: 'Could not auto-classify. Choose where to route this.'
      });
      return;
    }

    setSmartInputBusy(false);

    const decision = classification.decision || 'task';
    const payload = {
      input,
      decision,
      confidence: classification.confidence || 0,
      method: classification.method || 'fallback',
      message: classification.message
    };

    if (classification.needs_confirmation) {
      showSmartInputPrompt(payload);
      return;
    }

    await handleSmartInputDecision(decision, payload);
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

    if (elements.viewSchedulesBtn) {
      elements.viewSchedulesBtn.addEventListener('click', openSchedulePanel);
    }

    if (elements.launcherRefreshBtn) {
      elements.launcherRefreshBtn.addEventListener('click', () => loadWorkspaces());
    }

    if (elements.newSessionBtn) {
      elements.newSessionBtn.addEventListener('click', () => {
        if (!state.selectedId) return;
        if (window.sessionManager && typeof window.sessionManager.showCreateChatModalForWorkspace === 'function') {
          window.sessionManager.showCreateChatModalForWorkspace(state.selectedId);
        }
      });
    }

    if (elements.newNoteBtn) {
      elements.newNoteBtn.addEventListener('click', createNewNote);
    }

    if (elements.addFileBtn) {
      elements.addFileBtn.addEventListener('click', () => {
        // Open canvas to add files
        if (state.selectedId) {
          window.location.href = `/workspaces/${encodeURIComponent(state.selectedId)}/canvas`;
        }
      });
    }

    if (elements.smartInputSubmit) {
      elements.smartInputSubmit.addEventListener('click', submitSmartInput);
    }

    if (elements.smartInputField) {
      elements.smartInputField.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
          event.preventDefault();
          submitSmartInput();
        }
      });

      elements.smartInputField.addEventListener('input', () => {
        if (elements.smartInputPrompt && !elements.smartInputPrompt.hidden) {
          resetSmartInputPrompt();
        }
        setSmartInputStatus('', { busy: false });
      });
    }

    if (elements.smartInputPromptTask) {
      elements.smartInputPromptTask.addEventListener('click', () => handleSmartInputDecision('task'));
    }

    if (elements.smartInputPromptChat) {
      elements.smartInputPromptChat.addEventListener('click', () => handleSmartInputDecision('chat'));
    }

    if (elements.smartInputPromptCancel) {
      elements.smartInputPromptCancel.addEventListener('click', () => {
        resetSmartInputPrompt();
        setSmartInputStatus('', { busy: false });
      });
    }
  }

  if (window.EventBus) {
    EventBus.on('workspace:files:updated', (data) => {
      if (!data?.workspaceId || data.workspaceId !== state.selectedId) return;
      loadWorkspaceFiles(state.selectedId);
    }, 'workspaceHub');
  }

  bindEvents();
  loadWorkspaces();
})();
