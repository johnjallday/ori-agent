/**
 * Workspace Hub Kanban Board
 * UI-only kanban columns backed by task.context.kanban_column_id
 */
(function() {
  'use strict';

  const hubEl = document.getElementById('workspaceHub');
  if (!hubEl) return;

  const {
    getTaskKanbanColumnId,
    groupTasksByKanbanColumn,
    collectWorkspaceDescendantIds,
    formatDate
  } = window.WorkspaceHubUtils;

  let activeDetailsTask = null;

  function getElements() {
    return window.WorkspaceHubState.getElements();
  }

  function getState() {
    return window.WorkspaceHubState.getState();
  }

  function setActiveView(view) {
    const elements = getElements();
    const isBoard = view === 'board';

    if (elements.viewListBtn) {
      elements.viewListBtn.classList.toggle('is-active', !isBoard);
      elements.viewListBtn.setAttribute('aria-selected', (!isBoard).toString());
    }
    if (elements.viewBoardBtn) {
      elements.viewBoardBtn.classList.toggle('is-active', isBoard);
      elements.viewBoardBtn.setAttribute('aria-selected', isBoard.toString());
    }

    const bento = document.querySelector('#workspaceHubBody .hub-bento-grid');
    if (bento) {
      bento.hidden = isBoard;
    }

    if (elements.boardContainer) {
      elements.boardContainer.hidden = !isBoard;
    }

    const state = getState();
    state.board.scope = 'workspace';
  }

  function setBoardLoading(loading) {
    const state = getState();
    state.board.isLoading = loading;
    const elements = getElements();
    if (elements.boardLoading) {
      elements.boardLoading.hidden = !loading;
    }
  }

  function escapeHtml(value) {
    if (typeof window.escapeHtml === 'function') return window.escapeHtml(value);
    return String(value || '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function getTaskTitle(task) {
    return task?.description || task?.name || task?.id || 'Untitled';
  }

  function normalizeKanbanLabels(raw) {
    if (Array.isArray(raw)) {
      return raw.map((label) => String(label || '').trim()).filter(Boolean);
    }
    if (typeof raw === 'string') {
      return raw.split(',').map((label) => label.trim()).filter(Boolean);
    }
    return [];
  }

  function getTaskKanbanLabels(task) {
    const ctx = task?.context;
    if (!ctx || typeof ctx !== 'object') return [];
    return normalizeKanbanLabels(ctx.kanban_labels);
  }

  function formatLabelsInput(labels) {
    return (labels || []).join(', ');
  }

  function parseLabelsInput(value) {
    return normalizeKanbanLabels(value || '');
  }

  function getTaskDueDate(task) {
    const ctx = task?.context;
    if (!ctx || typeof ctx !== 'object') return '';
    const raw = ctx.kanban_due_date;
    return typeof raw === 'string' ? raw.trim() : '';
  }

  function normalizeDueInput(value) {
    if (!value) return '';
    if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value;
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    return date.toISOString().slice(0, 10);
  }

  function formatDueDate(value) {
    if (!value) return '';
    const date = /^\d{4}-\d{2}-\d{2}$/.test(value) ? new Date(`${value}T00:00:00`) : new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleDateString();
  }

  function getAssignmentValue(task) {
    if (task?.assigned_node_id) return `node:${task.assigned_node_id}`;
    if (task?.to && task.to !== 'unassigned') return `node:${task.to}-node-1`;
    return '';
  }

  function getAssignmentLabel(task) {
    if (task?.assigned_node_id) {
      const match = String(task.assigned_node_id).match(/^(.+)-node-\d+$/);
      return match ? match[1] : task.assigned_node_id;
    }
    if (task?.to && task.to !== 'unassigned') return task.to;
    return 'Unassigned';
  }

  function buildAssignmentOptions(selectedValue, selectedLabel) {
    const state = getState();
    const base = Array.isArray(state.board.agentOptions) ? state.board.agentOptions.slice() : [];
    if (!base.some((opt) => opt.value === '')) {
      base.unshift({ label: 'Unassigned', value: '' });
    }
    if (selectedValue && !base.some((opt) => opt.value === selectedValue)) {
      base.push({ label: selectedLabel || selectedValue, value: selectedValue });
    }
    return base;
  }

  async function fetchBoardConfig(workspaceId) {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/board`);
    if (!response.ok) {
      throw new Error('Failed to load board');
    }
    const data = await response.json();
    const board = data.board || {};
    const columns = Array.isArray(board.columns) ? board.columns : [];
    return { version: board.version || 1, columns };
  }

  async function saveBoardConfig(workspaceId, config) {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/board`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(config)
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to save board');
    }
    const data = await response.json();
    const board = data.board || {};
    const columns = Array.isArray(board.columns) ? board.columns : [];
    return { version: board.version || 1, columns };
  }

  async function fetchTasksForWorkspace(workspaceId) {
    const response = await fetch(`/api/orchestration/tasks?studio_id=${encodeURIComponent(workspaceId)}`);
    if (!response.ok) {
      throw new Error('Failed to load tasks');
    }
    const data = await response.json();
    return Array.isArray(data.tasks) ? data.tasks : [];
  }

  async function fetchAgentOptions() {
    const options = [{ label: 'Unassigned', value: '' }];
    try {
      const response = await fetch('/api/agents/dashboard/list');
      if (!response.ok) return options;
      const data = await response.json();
      const agents = data.agents || [];
      agents.forEach((agent) => {
        if (!agent || !agent.name) return;
        const nodeId = `${agent.name}-node-1`;
        options.push({ label: agent.name, value: `node:${nodeId}` });
      });
    } catch (err) {
      console.error('Failed to load agents:', err);
    }
    return options;
  }

  async function ensureAgentOptions() {
    const state = getState();
    if (Array.isArray(state.board.agentOptions) && state.board.agentOptions.length > 0) {
      return state.board.agentOptions;
    }
    const options = await fetchAgentOptions();
    state.board.agentOptions = options;
    return options;
  }

  function getWorkspaceDescendantIds(workspaceId) {
    const state = getState();
    return collectWorkspaceDescendantIds(state.workspaces || [], workspaceId, { includeRoot: false });
  }

  async function loadBoard(workspaceId) {
    if (!workspaceId) return;

    const state = getState();
    const elements = getElements();

    state.board.workspaceId = workspaceId;
    setBoardLoading(true);

    try {
      const [boardConfig] = await Promise.all([
        fetchBoardConfig(workspaceId),
        ensureAgentOptions()
      ]);
      state.board.config = boardConfig;
      state.board.columns = boardConfig.columns || [];

      const workspace = state.workspaceMap.get(workspaceId);
      const descendantIds = workspace && Array.isArray(workspace.children) && workspace.children.length > 0
        ? getWorkspaceDescendantIds(workspaceId)
        : [];

      const idsToLoad = [workspaceId, ...descendantIds];
      const taskLists = await Promise.all(
        idsToLoad.map((id) =>
          fetchTasksForWorkspace(id).then((tasks) =>
            tasks.map((task) => ({ ...task, __workspace_id: id }))
          )
        )
      );
      const tasks = taskLists.flat();

      state.board.tasks = tasks;

      renderBoard();

      if (elements.boardEmpty) {
        elements.boardEmpty.hidden = state.board.columns.length > 0;
      }
    } catch (err) {
      console.error('Failed to load board:', err);
      if (elements.boardColumns) {
        elements.boardColumns.innerHTML = '<div class="hub-empty">Unable to load board right now.</div>';
      }
    } finally {
      setBoardLoading(false);
    }
  }

  function getOrderedColumns(columns) {
    return (columns || [])
      .slice()
      .sort((a, b) => (a.order || 0) - (b.order || 0));
  }

  function renderBoard() {
    const state = getState();
    const elements = getElements();
    if (!elements.boardColumns) return;

    const columns = getOrderedColumns(state.board.columns);
    const groups = groupTasksByKanbanColumn(state.board.tasks || [], columns, 'backlog');

    const totalTasks = (state.board.tasks || []).length;
    if (elements.boardTaskCount) {
      elements.boardTaskCount.textContent = `${totalTasks} task${totalTasks === 1 ? '' : 's'}`;
    }

    const html = columns.map((col) => {
      const tasks = groups.get(col.id) || [];
      const cards = tasks.map((task) => renderCard(task, col.id)).join('');
      return `
        <div class="hub-board-column" data-column-id="${escapeHtml(col.id)}">
          <div class="hub-column-header">
            <div class="hub-column-title-wrap">
              <div class="hub-column-title" data-column-id="${escapeHtml(col.id)}">
                <span class="hub-column-title-text">${escapeHtml(col.name || col.id)}</span>
              </div>
              <button class="hub-column-edit-btn" type="button" title="Edit column name" aria-label="Edit column name">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M5,18.08V19H5.92L14.81,10.11L13.89,9.19L5,18.08M17.71,7.04C18.1,6.65 18.1,6 17.71,5.63L16.37,4.29C16,3.9 15.35,3.9 14.96,4.29L13.13,6.12L14.88,7.87L17.71,7.04Z"/>
                </svg>
              </button>
            </div>
            <span class="hub-column-count">${tasks.length}</span>
          </div>
          <div class="hub-column-cards" data-column-id="${escapeHtml(col.id)}">
            ${cards || '<div class="hub-empty" style="padding: 0.5rem;">No tasks</div>'}
          </div>
        </div>
      `;
    }).join('');

    elements.boardColumns.querySelectorAll('.hub-board-card').forEach((card) => {
      if (card._editOutsideHandler) {
        document.removeEventListener('click', card._editOutsideHandler);
        card._editOutsideHandler = null;
      }
    });
    elements.boardColumns.innerHTML = html;
    wireDragAndDrop();
    wireCardEditing();
    wireColumnRename();
  }

  function renderCard(task, columnId) {
    const title = getTaskTitle(task);
    const created = task.created_at ? formatDate(task.created_at) : '--';
    const status = task.status || 'pending';
    const priority = Number.isFinite(task.priority) ? task.priority : 5;
    const labels = getTaskKanbanLabels(task);
    const dueDate = getTaskDueDate(task);
    const assignmentValue = getAssignmentValue(task);
    const assignmentLabel = getAssignmentLabel(task);
    const assignmentOptions = buildAssignmentOptions(assignmentValue, assignmentLabel);
    const labelMarkup = labels.length > 0
      ? `<div class="hub-card-labels">${labels.map((label) => `<span class="hub-card-label">${escapeHtml(label)}</span>`).join('')}</div>`
      : '';
    const dueMarkup = dueDate ? `<span class="hub-card-due">Due ${escapeHtml(formatDueDate(dueDate))}</span>` : '';
    const assignedMarkup = assignmentLabel && assignmentLabel !== 'Unassigned'
      ? `<span class="hub-card-assignee">${escapeHtml(assignmentLabel)}</span>`
      : '<span class="hub-card-assignee is-muted">Unassigned</span>';
    const editTitleValue = escapeHtml(getTaskTitle(task));
    const editDetailsValue = escapeHtml(task.details || '');
    const editLabelsValue = escapeHtml(formatLabelsInput(labels));
    const editDueValue = escapeHtml(normalizeDueInput(dueDate));
    const assignmentOptionsHtml = assignmentOptions
      .map((opt) => {
        const selected = opt.value === assignmentValue ? ' selected' : '';
        return `<option value="${escapeHtml(opt.value)}"${selected}>${escapeHtml(opt.label)}</option>`;
      })
      .join('');

    return `
      <div class="hub-board-card" draggable="true" data-task-id="${escapeHtml(task.id)}" data-column-id="${escapeHtml(columnId)}" data-workspace-id="${escapeHtml(task.__workspace_id || task.studio_id || task.workspace_id || '')}">
        <span class="hub-card-priority priority-${escapeHtml(priority)}"></span>
        <div class="hub-card-view">
          <div class="hub-card-header">
            <div class="hub-card-title">${escapeHtml(title)}</div>
            <button class="hub-card-edit-btn" type="button" title="Edit card" aria-label="Edit card">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M5,18.08V19H5.92L14.81,10.11L13.89,9.19L5,18.08M17.71,7.04C18.1,6.65 18.1,6 17.71,5.63L16.37,4.29C16,3.9 15.35,3.9 14.96,4.29L13.13,6.12L14.88,7.87L17.71,7.04Z"/>
              </svg>
            </button>
          </div>
          ${labelMarkup}
          <div class="hub-card-meta">
            ${assignedMarkup}
            ${dueMarkup}
          </div>
          <div class="hub-card-meta hub-card-meta-secondary">
            <span>${escapeHtml(status)}</span>
            <span>${escapeHtml(created)}</span>
          </div>
        </div>
        <div class="hub-card-edit" hidden>
          <label class="hub-card-edit-label">Title</label>
          <input class="hub-card-edit-input hub-card-edit-title" type="text" value="${editTitleValue}" />
          <label class="hub-card-edit-label">Description</label>
          <textarea class="hub-card-edit-input hub-card-edit-details" rows="3">${editDetailsValue}</textarea>
          <label class="hub-card-edit-label">Assignee</label>
          <select class="hub-card-edit-input hub-card-edit-assignee">
            ${assignmentOptionsHtml}
          </select>
          <label class="hub-card-edit-label">Labels</label>
          <input class="hub-card-edit-input hub-card-edit-labels" type="text" value="${editLabelsValue}" placeholder="design, frontend" />
          <label class="hub-card-edit-label">Due date</label>
          <input class="hub-card-edit-input hub-card-edit-due" type="date" value="${editDueValue}" />
          <div class="hub-card-edit-actions">
            <button class="modern-btn modern-btn-secondary hub-card-edit-cancel" type="button">Cancel</button>
            <button class="modern-btn modern-btn-primary hub-card-edit-save" type="button">Done</button>
          </div>
        </div>
      </div>
    `;
  }

  function wireDragAndDrop() {
    const elements = getElements();
    if (!elements.boardColumns) return;

    let dragged = null;
    let didDrag = false;

    elements.boardColumns.querySelectorAll('.hub-board-card').forEach((card) => {
      card.addEventListener('dragstart', (e) => {
        if (card.classList.contains('is-editing') || e.target.closest('.hub-card-edit')) {
          e.preventDefault();
          return;
        }
        dragged = card;
        didDrag = true;
        card.classList.add('is-dragging');
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', card.dataset.taskId);
      });
      card.addEventListener('dragend', () => {
        card.classList.remove('is-dragging');
        dragged = null;
        // A drag often triggers a click on drop; suppress that.
        setTimeout(() => {
          didDrag = false;
        }, 0);
      });

      card.addEventListener('click', (e) => {
        if (didDrag) return;
        if (e.target.closest('.hub-card-edit') || e.target.closest('.hub-card-edit-btn')) return;
        if (e.target.closest('button') || e.target.closest('a') || e.target.closest('input') || e.target.closest('textarea') || e.target.closest('select')) return;

        const state = getState();
        const taskId = card.dataset.taskId;
        if (!taskId) return;

        const task = (state.board.tasks || []).find((t) => t && t.id === taskId);
        if (!task) return;

        openTaskDetailsModal(task);
      });
    });

    elements.boardColumns.querySelectorAll('.hub-column-cards').forEach((colEl) => {
      colEl.addEventListener('dragover', (e) => {
        e.preventDefault();
        colEl.closest('.hub-board-column')?.classList.add('is-drag-over');
        e.dataTransfer.dropEffect = 'move';
      });
      colEl.addEventListener('dragleave', () => {
        colEl.closest('.hub-board-column')?.classList.remove('is-drag-over');
      });
      colEl.addEventListener('drop', async (e) => {
        e.preventDefault();
        const columnId = colEl.dataset.columnId;
        colEl.closest('.hub-board-column')?.classList.remove('is-drag-over');

        const taskId = e.dataTransfer.getData('text/plain') || dragged?.dataset.taskId;
        if (!taskId || !columnId) return;

        await updateTaskKanbanColumn(taskId, columnId);
      });
    });
  }

  function parseAssignmentValue(value) {
    let to = '';
    let assignedNodeId = '';
    if (value && value.startsWith('node:')) {
      assignedNodeId = value.slice('node:'.length);
      const match = assignedNodeId.match(/^(.+)-node-\d+$/);
      to = match ? match[1] : assignedNodeId;
    }
    return { to, assignedNodeId };
  }

  function getCardEditValues(card) {
    const titleEl = card.querySelector('.hub-card-edit-title');
    const detailsEl = card.querySelector('.hub-card-edit-details');
    const assigneeEl = card.querySelector('.hub-card-edit-assignee');
    const labelsEl = card.querySelector('.hub-card-edit-labels');
    const dueEl = card.querySelector('.hub-card-edit-due');

    const title = titleEl ? titleEl.value.trim() : '';
    const details = detailsEl ? detailsEl.value.trim() : '';
    const assigneeValue = assigneeEl ? assigneeEl.value : '';
    const labels = parseLabelsInput(labelsEl ? labelsEl.value : '');
    const dueDate = dueEl ? dueEl.value : '';
    const assignment = parseAssignmentValue(assigneeValue);

    return {
      title,
      details,
      assigneeValue,
      labels,
      dueDate,
      to: assignment.to,
      assignedNodeId: assignment.assignedNodeId
    };
  }

  function hasCardChanges(current, original) {
    if (!original) return true;
    if (current.title !== original.title) return true;
    if (current.details !== original.details) return true;
    if (current.assigneeValue !== original.assigneeValue) return true;
    if (current.dueDate !== original.dueDate) return true;
    const currentLabels = (current.labels || []).join('|');
    const originalLabels = (original.labels || []).join('|');
    return currentLabels !== originalLabels;
  }

  function enterCardEdit(card) {
    if (!card || card.classList.contains('is-editing')) return;
    const editEl = card.querySelector('.hub-card-edit');
    const viewEl = card.querySelector('.hub-card-view');
    if (!editEl) return;

    card.classList.add('is-editing');
    card.setAttribute('draggable', 'false');
    if (viewEl) viewEl.setAttribute('hidden', '');
    editEl.removeAttribute('hidden');

    card._editOriginal = getCardEditValues(card);

    const focusEl = editEl.querySelector('input, textarea, select');
    if (focusEl) {
      focusEl.focus();
      if (focusEl.select) focusEl.select();
    }

    setTimeout(() => {
      const handler = (evt) => {
        if (card.contains(evt.target)) return;
        saveCardEdits(card);
      };
      card._editOutsideHandler = handler;
      document.addEventListener('click', handler);
    }, 0);
  }

  function exitCardEdit(card, { reset = false } = {}) {
    if (!card || !card.classList.contains('is-editing')) return;
    const editEl = card.querySelector('.hub-card-edit');
    const viewEl = card.querySelector('.hub-card-view');

    if (reset && card._editOriginal) {
      const titleEl = card.querySelector('.hub-card-edit-title');
      const detailsEl = card.querySelector('.hub-card-edit-details');
      const assigneeEl = card.querySelector('.hub-card-edit-assignee');
      const labelsEl = card.querySelector('.hub-card-edit-labels');
      const dueEl = card.querySelector('.hub-card-edit-due');
      if (titleEl) titleEl.value = card._editOriginal.title || '';
      if (detailsEl) detailsEl.value = card._editOriginal.details || '';
      if (assigneeEl) assigneeEl.value = card._editOriginal.assigneeValue || '';
      if (labelsEl) labelsEl.value = formatLabelsInput(card._editOriginal.labels || []);
      if (dueEl) dueEl.value = card._editOriginal.dueDate || '';
    }

    card.classList.remove('is-editing');
    card.setAttribute('draggable', 'true');
    if (editEl) editEl.setAttribute('hidden', '');
    if (viewEl) viewEl.removeAttribute('hidden');

    if (card._editOutsideHandler) {
      document.removeEventListener('click', card._editOutsideHandler);
      card._editOutsideHandler = null;
    }
    card._editOriginal = null;
  }

  async function updateTaskDetails(taskId, updates) {
    const state = getState();
    const payload = {
      description: updates.title,
      details: updates.details,
      to: updates.to,
      assigned_node_id: updates.assignedNodeId,
      kanban_labels: updates.labels,
      kanban_due_date: updates.dueDate
    };

    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to update task');
    }

    const updated = await response.json();
    const boardIdx = (state.board.tasks || []).findIndex((t) => t && t.id === taskId);
    if (boardIdx >= 0) {
      const existing = state.board.tasks[boardIdx];
      state.board.tasks.splice(boardIdx, 1, { ...existing, ...updated, __workspace_id: existing.__workspace_id });
    }

    const listIdx = (state.tasks || []).findIndex((t) => t && t.id === taskId);
    if (listIdx >= 0) {
      const existing = state.tasks[listIdx];
      state.tasks.splice(listIdx, 1, { ...existing, ...updated });
      if (window.WorkspaceHubTasks && typeof window.WorkspaceHubTasks.renderTasksList === 'function') {
        window.WorkspaceHubTasks.renderTasksList(state.tasks);
      }
    }

    renderBoard();
  }

  async function saveCardEdits(card) {
    if (!card || !card.classList.contains('is-editing')) return;
    if (card.dataset.saving === '1') return;

    const taskId = card.dataset.taskId;
    if (!taskId) return;

    const current = getCardEditValues(card);
    const original = card._editOriginal;
    if (!hasCardChanges(current, original)) {
      exitCardEdit(card);
      return;
    }

    if (!current.title) {
      if (window.Toast) window.Toast.error('Title is required');
      const titleEl = card.querySelector('.hub-card-edit-title');
      if (titleEl) titleEl.focus();
      return;
    }

    card.dataset.saving = '1';
    card.classList.add('is-saving');

    try {
      await updateTaskDetails(taskId, current);
      exitCardEdit(card);
    } catch (err) {
      console.error('Failed to update task:', err);
      if (window.Toast) window.Toast.error('Failed to update task');
    } finally {
      card.dataset.saving = '';
      card.classList.remove('is-saving');
    }
  }

  function wireCardEditing() {
    const elements = getElements();
    if (!elements.boardColumns) return;

    elements.boardColumns.querySelectorAll('.hub-board-card').forEach((card) => {
      const editBtn = card.querySelector('.hub-card-edit-btn');
      if (editBtn) {
        editBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          enterCardEdit(card);
        });
      }

      const cancelBtn = card.querySelector('.hub-card-edit-cancel');
      if (cancelBtn) {
        cancelBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          exitCardEdit(card, { reset: true });
        });
      }

      const saveBtn = card.querySelector('.hub-card-edit-save');
      if (saveBtn) {
        saveBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          saveCardEdits(card);
        });
      }

      const inputs = card.querySelectorAll('.hub-card-edit input, .hub-card-edit textarea, .hub-card-edit select');
      inputs.forEach((input) => {
        input.addEventListener('keydown', (e) => {
          if (e.key === 'Escape') {
            e.preventDefault();
            exitCardEdit(card, { reset: true });
            return;
          }

          if (e.key === 'Enter') {
            if (input.tagName === 'TEXTAREA' && !(e.metaKey || e.ctrlKey)) return;
            e.preventDefault();
            saveCardEdits(card);
          }
        });
      });
    });
  }

  function wireColumnRename() {
    const elements = getElements();
    if (!elements.boardColumns) return;

    elements.boardColumns.querySelectorAll('.hub-column-header').forEach((headerEl) => {
      const titleEl = headerEl.querySelector('.hub-column-title');
      const textEl = titleEl ? titleEl.querySelector('.hub-column-title-text') : null;
      const editBtn = headerEl.querySelector('.hub-column-edit-btn');
      if (!titleEl || !textEl || !editBtn) return;

      editBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();

        const columnId = titleEl.dataset.columnId;
        if (!columnId) return;

        const currentName = textEl.textContent;

        // Create input field
        const input = document.createElement('input');
        input.type = 'text';
        input.className = 'hub-column-rename-input';
        input.value = currentName;

        // Replace text with input
        textEl.style.display = 'none';
        editBtn.style.display = 'none';
        titleEl.appendChild(input);
        input.focus();
        input.select();

        const finishRename = async () => {
          const newName = input.value.trim();
          input.remove();
          textEl.style.display = '';
          editBtn.style.display = '';

          if (!newName || newName === currentName) return;

          // Update the column name
          const state = getState();
          const workspaceId = state.selectedId;
          if (!workspaceId) return;

          const columns = (state.board.columns || []).map((col) => {
            if (col.id === columnId) {
              return { ...col, name: newName };
            }
            return col;
          });

          try {
            const saved = await saveBoardConfig(workspaceId, {
              version: state.board.config?.version || 1,
              columns
            });
            state.board.config = saved;
            state.board.columns = saved.columns;
            textEl.textContent = newName;
            if (window.Toast) window.Toast.success('Column renamed');
          } catch (err) {
            console.error('Failed to rename column:', err);
            if (window.Toast) window.Toast.error('Failed to rename column');
          }
        };

        input.addEventListener('blur', finishRename);
        input.addEventListener('keydown', (evt) => {
          if (evt.key === 'Enter') {
            evt.preventDefault();
            input.blur();
          } else if (evt.key === 'Escape') {
            evt.preventDefault();
            input.value = currentName; // Reset to original
            input.blur();
          }
        });
      });
    });
  }

  function openTaskDetailsModal(task) {
    const elements = getElements();
    if (!elements.taskDetailsModal || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      return;
    }

    activeDetailsTask = task;

    const title = task.name || task.description || task.id;
    const description = task.description || '--';
    const details = task.details || '--';
    const status = task.status || 'pending';
    const assigned = task.to && task.to !== 'unassigned' ? task.to : '--';
    const created = task.created_at ? formatDate(task.created_at) : '--';
    const updated = task.updated_at ? formatDate(task.updated_at) : '--';

    if (elements.taskDetailsTitle) elements.taskDetailsTitle.textContent = title;
    if (elements.taskDetailsDescription) elements.taskDetailsDescription.textContent = description;
    if (elements.taskDetailsId) elements.taskDetailsId.textContent = task.id || '--';
    if (elements.taskDetailsStatus) elements.taskDetailsStatus.textContent = status;
    if (elements.taskDetailsAssignedTo) elements.taskDetailsAssignedTo.textContent = assigned;
    if (elements.taskDetailsCreated) elements.taskDetailsCreated.textContent = created;
    if (elements.taskDetailsUpdated) elements.taskDetailsUpdated.textContent = updated;
    if (elements.taskDetailsText) elements.taskDetailsText.textContent = details;

    if (elements.taskDetailsRunBtn) {
      const canRun = status !== 'in_progress';
      elements.taskDetailsRunBtn.disabled = !canRun;
      elements.taskDetailsRunBtn.title = canRun ? 'Run task' : 'Task is already running';
    }

    const hasResult = !!task.result;
    const hasError = !!task.error;

    if (elements.taskDetailsResultSection) {
      elements.taskDetailsResultSection.style.display = hasResult ? '' : 'none';
    }
    if (elements.taskDetailsResult) {
      elements.taskDetailsResult.textContent = task.result || '--';
    }
    if (elements.taskDetailsResultBadge) {
      elements.taskDetailsResultBadge.textContent = hasResult ? 'Success' : '';
    }

    if (elements.taskDetailsErrorSection) {
      elements.taskDetailsErrorSection.style.display = hasError ? '' : 'none';
    }
    if (elements.taskDetailsError) {
      elements.taskDetailsError.textContent = task.error || '--';
    }

    const modal = bootstrap.Modal.getInstance(elements.taskDetailsModal) || new bootstrap.Modal(elements.taskDetailsModal);
    modal.show();
  }

  function getSubtasksForParent(taskId) {
    const state = getState();
    return (state.board.tasks || []).filter((t) => t && t.parent_task_id === taskId);
  }

  async function executeTaskFromModal(task) {
    const state = getState();
    if (!task || !task.id) return;

    const subtasks = getSubtasksForParent(task.id);
    const isParent = subtasks.length > 0;

    if (isParent) {
      const hasUnassigned = subtasks.some((subtask) => !subtask.to || subtask.to === 'unassigned');
      if (hasUnassigned) {
        if (window.Toast) window.Toast.error('Assign agents to all subtasks before executing this workflow.');
        return;
      }
      const hasRunning = subtasks.some((subtask) => subtask.status === 'in_progress');
      if (hasRunning) {
        if (window.Toast) window.Toast.error('A subtask is already running.');
        return;
      }
    } else {
      const assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
      if (!assignedAgent) {
        if (window.Toast) window.Toast.error('Assign an agent before executing this task.');
        return;
      }
    }

    const confirmMessage = isParent
      ? `Execute this workflow (${subtasks.length} step${subtasks.length === 1 ? '' : 's'}) now?`
      : 'Execute this task now?';
    if (!confirm(confirmMessage)) return;

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: task.id })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to execute task');
      }

      if (window.Toast) window.Toast.success('Task started');

      // Refresh board and the main list (if applicable)
      if (state.selectedId) {
        if (window.WorkspaceHubTasks && typeof window.WorkspaceHubTasks.loadTasks === 'function') {
          window.WorkspaceHubTasks.loadTasks(state.selectedId);
        }
        await loadBoard(state.selectedId);
      }

      pollTaskCompletionAndRefresh(task.id);
    } catch (err) {
      console.error('Failed to execute task:', err);
      if (window.Toast) window.Toast.error('Failed to execute task');
    }
  }

  async function pollTaskCompletionAndRefresh(taskId, maxAttempts = 36, intervalMs = 5000) {
    const state = getState();
    let attempts = 0;

    const poll = async () => {
      attempts += 1;
      if (attempts > maxAttempts) return;

      try {
        const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`);
        if (!response.ok) throw new Error('Failed to fetch task');
        const data = await response.json();
        const status = data && data.status ? data.status : '';

        if (status && status !== 'in_progress' && status !== 'assigned') {
          if (state.selectedId) {
            await loadBoard(state.selectedId);
          }
          return;
        }
      } catch (err) {
        // ignore transient errors
      }

      setTimeout(poll, intervalMs);
    };

    setTimeout(poll, intervalMs);
  }

  function wireTaskDetailsModal() {
    const elements = getElements();
    if (!elements.taskDetailsModal) return;

    if (elements.taskDetailsCopyIdBtn) {
      elements.taskDetailsCopyIdBtn.addEventListener('click', async () => {
        const taskId = activeDetailsTask?.id;
        if (!taskId) return;
        try {
          await navigator.clipboard.writeText(taskId);
          if (window.Toast) window.Toast.success('Task ID copied');
        } catch (err) {
          console.error('Failed to copy task id:', err);
          if (window.Toast) window.Toast.error('Failed to copy');
        }
      });
    }

    if (elements.taskDetailsEditBtn) {
      elements.taskDetailsEditBtn.addEventListener('click', async () => {
        const task = activeDetailsTask;
        if (!task) return;

        const modal = bootstrap.Modal.getInstance(elements.taskDetailsModal);
        if (modal) modal.hide();

        if (window.taskModalController && typeof window.taskModalController.openForEdit === 'function') {
          const state = getState();
          window.taskModalController.openForEdit(task, async () => {
            if (state.selectedId) {
              if (window.WorkspaceHubTasks && typeof window.WorkspaceHubTasks.loadTasks === 'function') {
                window.WorkspaceHubTasks.loadTasks(state.selectedId);
              }
              await loadBoard(state.selectedId);
            }
          });
        }
      });
    }

    if (elements.taskDetailsRunBtn) {
      elements.taskDetailsRunBtn.addEventListener('click', async () => {
        const task = activeDetailsTask;
        if (!task) return;
        await executeTaskFromModal(task);
      });
    }
  }

  async function updateTaskKanbanColumn(taskId, columnId) {
    const state = getState();
    const selectedWorkspaceId = state.selectedId;
    if (!selectedWorkspaceId) return;

    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ kanban_column_id: columnId })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to move task');
      }

      // Optimistic local update
      const idx = (state.board.tasks || []).findIndex((t) => t.id === taskId);
      if (idx >= 0) {
        const task = state.board.tasks[idx];
        const next = { ...task, context: { ...(task.context || {}), kanban_column_id: columnId } };
        state.board.tasks.splice(idx, 1, next);
        renderBoard();
      } else {
        await loadBoard(selectedWorkspaceId);
      }
    } catch (err) {
      console.error('Failed to update kanban column:', err);
      if (window.Toast) window.Toast.error('Failed to move task');
    }
  }

  function openColumnsModal() {
    const elements = getElements();
    if (!elements.boardColumnsModal) return;
    const modal = new bootstrap.Modal(elements.boardColumnsModal);
    renderColumnsEditor();
    modal.show();
  }

  function renderColumnsEditor() {
    const state = getState();
    const elements = getElements();
    if (!elements.boardColumnsList) return;

    const cols = getOrderedColumns(state.board.columns);
    const html = cols.map((col) => {
      return `
        <div class="hub-column-editor-item" draggable="true" data-column-id="${escapeHtml(col.id)}">
          <span class="hub-column-drag-handle" title="Drag to reorder">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
              <path d="M9,3A2,2 0 0,1 11,5A2,2 0 0,1 9,7A2,2 0 0,1 7,5A2,2 0 0,1 9,3M9,10A2,2 0 0,1 11,12A2,2 0 0,1 9,14A2,2 0 0,1 7,12A2,2 0 0,1 9,10M9,17A2,2 0 0,1 11,19A2,2 0 0,1 9,21A2,2 0 0,1 7,19A2,2 0 0,1 9,17M15,3A2,2 0 0,1 17,5A2,2 0 0,1 15,7A2,2 0 0,1 13,5A2,2 0 0,1 15,3M15,10A2,2 0 0,1 17,12A2,2 0 0,1 15,14A2,2 0 0,1 13,12A2,2 0 0,1 15,10M15,17A2,2 0 0,1 17,19A2,2 0 0,1 15,21A2,2 0 0,1 13,19A2,2 0 0,1 15,17Z"/>
            </svg>
          </span>
          <input class="hub-column-name-input" type="text" value="${escapeHtml(col.name)}" data-column-id="${escapeHtml(col.id)}" />
          <button class="hub-column-delete-btn" type="button" data-column-id="${escapeHtml(col.id)}" title="Delete column">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    }).join('');

    elements.boardColumnsList.innerHTML = html;

    // Delete handlers
    elements.boardColumnsList.querySelectorAll('.hub-column-delete-btn').forEach((btn) => {
      btn.addEventListener('click', () => {
        const id = btn.dataset.columnId;
        const next = (state.board.columns || []).filter((c) => c.id !== id);
        state.board.columns = next;
        renderColumnsEditor();
      });
    });

    wireColumnReorder();
  }

  function wireColumnReorder() {
    const state = getState();
    const elements = getElements();
    if (!elements.boardColumnsList) return;

    let draggedId = null;

    elements.boardColumnsList.querySelectorAll('.hub-column-editor-item').forEach((item) => {
      item.addEventListener('dragstart', (e) => {
        draggedId = item.dataset.columnId;
        item.classList.add('is-dragging');
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', draggedId);
      });
      item.addEventListener('dragend', () => {
        item.classList.remove('is-dragging');
        draggedId = null;
      });
      item.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
      });
      item.addEventListener('drop', (e) => {
        e.preventDefault();
        const targetId = item.dataset.columnId;
        const sourceId = e.dataTransfer.getData('text/plain') || draggedId;
        if (!sourceId || !targetId || sourceId === targetId) return;

        const cols = getOrderedColumns(state.board.columns);
        const fromIdx = cols.findIndex((c) => c.id === sourceId);
        const toIdx = cols.findIndex((c) => c.id === targetId);
        if (fromIdx < 0 || toIdx < 0) return;

        const next = cols.slice();
        const [moved] = next.splice(fromIdx, 1);
        next.splice(toIdx, 0, moved);
        state.board.columns = next.map((c, idx) => ({ ...c, order: idx + 1 }));
        renderColumnsEditor();
      });
    });
  }

  function addColumn() {
    const state = getState();
    const existing = new Set((state.board.columns || []).map((c) => c.id));

    let base = 'new_column';
    let id = base;
    let i = 1;
    while (existing.has(id)) {
      i += 1;
      id = `${base}_${i}`;
    }

    const next = (state.board.columns || []).slice();
    next.push({ id, name: 'New Column', order: next.length + 1 });
    state.board.columns = next;
    renderColumnsEditor();
  }

  async function saveColumns() {
    const state = getState();
    const workspaceId = state.selectedId;
    if (!workspaceId) return;

    const elements = getElements();
    const list = elements.boardColumnsList;
    if (!list) return;

    const nameInputs = Array.from(list.querySelectorAll('.hub-column-name-input'));
    const next = getOrderedColumns(state.board.columns).map((col, idx) => {
      const input = nameInputs.find((it) => it.dataset.columnId === col.id);
      const name = (input ? input.value : col.name) || col.name;
      return { id: col.id, name: String(name).trim() || col.id, order: idx + 1 };
    });

    try {
      const saved = await saveBoardConfig(workspaceId, { version: state.board.config?.version || 1, columns: next });
      state.board.config = saved;
      state.board.columns = saved.columns;
      if (window.Toast) window.Toast.success('Board updated');
      renderBoard();

      const modalEl = elements.boardColumnsModal;
      const modal = modalEl ? bootstrap.Modal.getInstance(modalEl) : null;
      if (modal) modal.hide();
    } catch (err) {
      console.error('Failed to save columns:', err);
      if (window.Toast) window.Toast.error('Failed to save columns');
    }
  }

  function isBoardActive() {
    const elements = getElements();
    return !!elements.viewBoardBtn && elements.viewBoardBtn.classList.contains('is-active');
  }

  function wireEvents() {
    const elements = getElements();
    const state = getState();

    if (elements.viewListBtn) {
      elements.viewListBtn.addEventListener('click', () => setActiveView('list'));
    }

    if (elements.viewBoardBtn) {
      elements.viewBoardBtn.addEventListener('click', async () => {
        if (!state.selectedId) {
          if (window.Toast) window.Toast.error('Select a workspace first');
          return;
        }
        setActiveView('board');
        await loadBoard(state.selectedId);
      });
    }

    if (elements.boardRefreshBtn) {
      elements.boardRefreshBtn.addEventListener('click', async () => {
        if (!state.selectedId) return;
        await loadBoard(state.selectedId);
      });
    }

    if (elements.boardEditColumnsBtn) {
      elements.boardEditColumnsBtn.addEventListener('click', () => openColumnsModal());
    }

    if (elements.boardSetupBtn) {
      elements.boardSetupBtn.addEventListener('click', () => openColumnsModal());
    }

    if (elements.boardAddColumnBtn) {
      elements.boardAddColumnBtn.addEventListener('click', () => addColumn());
    }

    if (elements.boardSaveColumnsBtn) {
      elements.boardSaveColumnsBtn.addEventListener('click', () => saveColumns());
    }

    // no-op; selection hook is in workspace-hub.js
  }

  // Public API for other modules
  window.WorkspaceHubBoard = {
    setActiveView,
    loadBoard
  };

  // Wire events after other hub modules initialized elements.
  document.addEventListener('DOMContentLoaded', () => {
    setActiveView('list');
    wireTaskDetailsModal();
    wireEvents();
  });
})();
