/* global escapeHtml */

const escapeTaskHtml = window.escapeHtml || function fallbackEscapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
};

function formatRelativeDate(dateString) {
  if (!dateString) return '—';

  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return '—';

  const now = new Date();
  const diffMs = now - date;
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  return date.toLocaleDateString();
}

function formatDateTime(dateString) {
  if (!dateString) return '—';
  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString([], {
    dateStyle: 'medium',
    timeStyle: 'short'
  });
}

function getStatusClass(status) {
  const normalized = String(status || '').trim().toLowerCase();
  if (normalized === 'completed') return 'completed';
  if (normalized === 'in_progress') return 'in_progress';
  if (normalized === 'blocked') return 'blocked';
  if (normalized === 'failed' || normalized === 'timeout') return 'failed';
  return 'pending';
}

function getDisplayStatus(status) {
  const normalized = String(status || '').trim().toLowerCase();
  const labels = {
    pending: 'Pending',
    assigned: 'Assigned',
    in_progress: 'In Progress',
    completed: 'Completed',
    failed: 'Failed',
    blocked: 'Blocked',
    cancelled: 'Cancelled',
    timeout: 'Timed Out'
  };
  return labels[normalized] || 'Pending';
}

function summarizeText(value, maxLength = 220) {
  const normalized = String(value || '').replace(/\s+/g, ' ').trim();
  if (!normalized) return '';
  if (normalized.length <= maxLength) return normalized;

  const candidate = normalized.slice(0, maxLength - 1);
  const boundary = candidate.lastIndexOf(' ');
  const trimmed = boundary >= Math.floor(maxLength * 0.55)
    ? candidate.slice(0, boundary)
    : candidate;
  return `${trimmed.trim()}...`;
}

function normalizeResultText(value) {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value, null, 2);
  } catch (_error) {
    return String(value);
  }
}

export class WorkspaceTaskPage {
  constructor(workspaceId, taskId) {
    this.workspaceId = workspaceId;
    this.taskId = taskId;
    this.workspace = null;
    this.task = null;
    this.tasks = [];
    this.currentBlockedTask = null;
    this.taskAssistResponseExpanded = false;
    this.workspaceRealtimeUnsubscribe = null;
    this.pendingRefreshTimer = null;
    this.titleEditInProgress = false;
    this.elements = {};
  }

  async init() {
    this.cacheElements();
    this.bindEvents();
    await this.loadData();
    this.setupRealtime();
  }

  cacheElements() {
    this.elements = {
      root: document.getElementById('workspaceTaskPageRoot'),
      alert: document.getElementById('workspace-task-page-alert'),
      loading: document.getElementById('workspace-task-page-loading'),
      empty: document.getElementById('workspace-task-page-empty'),
      content: document.getElementById('workspace-task-page-content'),
      workspaceName: document.getElementById('workspace-task-workspace-name'),
      title: document.getElementById('workspace-task-title'),
      titleEditBtn: document.getElementById('workspace-task-title-edit'),
      breadcrumbTitle: document.getElementById('workspace-task-breadcrumb-title'),
      copyIdBtn: document.getElementById('workspace-task-copy-id'),
      copyLinkBtn: document.getElementById('workspace-task-copy-link'),
      deleteBtn: document.getElementById('workspace-task-delete'),
      subtitle: document.getElementById('workspace-task-subtitle'),
      status: document.getElementById('workspace-task-status'),
      heroActions: document.getElementById('workspace-task-hero-actions'),
      liveBadge: document.getElementById('workspace-task-live-badge'),
      overview: document.getElementById('workspace-task-overview'),
      snapshot: document.getElementById('workspace-task-snapshot'),
      relationshipsCard: document.getElementById('workspace-task-relationships-card'),
      relationships: document.getElementById('workspace-task-relationships'),
      outputCard: document.getElementById('workspace-task-output-card'),
      output: document.getElementById('workspace-task-output'),
      scheduleCard: document.getElementById('workspace-task-schedule-card'),
      schedule: document.getElementById('workspace-task-schedule'),
      stepsCard: document.getElementById('workspace-task-steps-card'),
      steps: document.getElementById('workspace-task-steps'),
      contextCard: document.getElementById('workspace-task-context-card'),
      context: document.getElementById('workspace-task-context'),
      blockedContextCard: document.getElementById('workspace-task-blocked-context-card'),
      blockedReason: document.getElementById('workspace-task-blocked-reason'),
      blockedRequestWrap: document.getElementById('workspace-task-blocked-request-wrap'),
      blockedRequestPreview: document.getElementById('workspace-task-blocked-request-preview'),
      blockedRequestToggle: document.getElementById('workspace-task-blocked-request-toggle'),
      blockedRequest: document.getElementById('workspace-task-blocked-request'),
      assistCard: document.getElementById('workspace-task-assist-card'),
      assistKnown: document.getElementById('workspace-task-assist-known'),
      assistNeeds: document.getElementById('workspace-task-assist-needs'),
      assistNext: document.getElementById('workspace-task-assist-next'),
      assistQuestionWrap: document.getElementById('workspace-task-assist-question-wrap'),
      assistQuestion: document.getElementById('workspace-task-assist-question'),
      assistFormWrap: document.getElementById('workspace-task-assist-form-wrap'),
      assistFormFields: document.getElementById('workspace-task-assist-form-fields'),
      assistMessage: document.getElementById('workspace-task-assist-message'),
      assistAgent: document.getElementById('workspace-task-assist-agent'),
      assistRetryBtn: document.getElementById('workspace-task-assist-retry'),
      assistContinueBtn: document.getElementById('workspace-task-assist-continue'),
      assistSwitchBtn: document.getElementById('workspace-task-assist-switch'),
      assistFailBtn: document.getElementById('workspace-task-assist-fail')
    };
  }

  bindEvents() {
    this.elements.titleEditBtn?.addEventListener('click', () => this.startTitleEdit());
    this.elements.title?.addEventListener('dblclick', () => this.startTitleEdit());
    this.elements.copyIdBtn?.addEventListener('click', () => this.copyToClipboard(this.taskId, 'Task ID copied'));
    this.elements.copyLinkBtn?.addEventListener('click', () => this.copyToClipboard(window.location.href, 'Link copied'));
    this.elements.deleteBtn?.addEventListener('click', () => this.deleteTask());
    this.elements.blockedRequestToggle?.addEventListener('click', () => this.toggleAssistResponseExpanded());
    this.elements.assistRetryBtn?.addEventListener('click', () => this.submitTaskAssist('retry'));
    this.elements.assistContinueBtn?.addEventListener('click', () => this.submitTaskAssist('continue_with_instruction'));
    this.elements.assistSwitchBtn?.addEventListener('click', () => this.submitTaskAssist('switch_agent_retry'));
    this.elements.assistFailBtn?.addEventListener('click', () => this.submitTaskAssist('mark_failed'));
    this.elements.assistAgent?.addEventListener('change', () => this.updateAssistSwitchButtonState());
  }

  async loadData() {
    this.setState('loading');
    this.setAlert('');

    try {
      const [workspace, taskResponse] = await Promise.all([
        this.fetchWorkspace(),
        this.fetchTask()
      ]);

      this.workspace = workspace || null;
      this.tasks = Array.isArray(workspace?.tasks) ? workspace.tasks : [];

      const workspaceTask = this.tasks.find((item) => String(item?.id || '') === this.taskId) || null;
      this.task = taskResponse || workspaceTask;

      if (!this.task || String(this.task.workspace_id || this.workspaceId) !== this.workspaceId) {
        this.setState('empty');
        return;
      }

      if (!workspaceTask) {
        this.tasks = this.task ? [this.task] : [];
      }

      this.render();
      this.setState('content');
    } catch (error) {
      console.error('Failed to load workspace task page:', error);
      this.setAlert(error?.message || 'Failed to load this task page.');
      this.setState('empty');
    }
  }

  async fetchWorkspace() {
    const response = await fetch(`/api/workspaces?id=${encodeURIComponent(this.workspaceId)}`);
    if (!response.ok) {
      throw new Error('Failed to load workspace details.');
    }
    return response.json();
  }

  async fetchTask() {
    const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(this.taskId)}`);
    if (response.status === 404) return null;
    if (!response.ok) {
      throw new Error('Failed to load task details.');
    }
    return response.json();
  }

  setupRealtime() {
    if (this.workspaceRealtimeUnsubscribe || !window.workspaceRealtime || typeof window.workspaceRealtime.subscribeToWorkspace !== 'function') {
      return;
    }

    this.workspaceRealtimeUnsubscribe = window.workspaceRealtime.subscribeToWorkspace(this.workspaceId, (event) => {
      this.handleRealtimeEvent(event);
    });
  }

  handleRealtimeEvent(event) {
    const eventType = String(event?.type || '').trim();
    if (!eventType.startsWith('task.')) {
      return;
    }

    const payload = event?.data?.data || event?.data || {};
    const eventTaskId = String(payload?.task_id || payload?.id || payload?.task?.id || '').trim();
    if (eventTaskId && eventTaskId !== this.taskId) {
      return;
    }

    if (this.elements.root) {
      this.elements.root.classList.remove('workspace-task-page-flash');
      void this.elements.root.offsetWidth;
      this.elements.root.classList.add('workspace-task-page-flash');
    }

    window.clearTimeout(this.pendingRefreshTimer);
    this.pendingRefreshTimer = window.setTimeout(() => {
      this.loadData();
    }, 180);
  }

  setState(state) {
    const isLoading = state === 'loading';
    const isEmpty = state === 'empty';
    const isContent = state === 'content';

    if (this.elements.loading) this.elements.loading.hidden = !isLoading;
    if (this.elements.empty) this.elements.empty.hidden = !isEmpty;
    if (this.elements.content) this.elements.content.hidden = !isContent;
  }

  setAlert(message = '') {
    if (!this.elements.alert) return;
    const normalized = String(message || '').trim();
    this.elements.alert.textContent = normalized;
    this.elements.alert.classList.toggle('d-none', !normalized);
  }

  startTitleEdit() {
    if (this.titleEditInProgress || !this.elements.title) return;

    const titleElement = this.elements.title;
    const actionsContainer = titleElement.parentElement?.querySelector('.workspace-task-page-title-actions');
    const currentValue = this.getTaskDisplayLabel();
    this.titleEditInProgress = true;

    const input = document.createElement('textarea');
    input.className = 'workspace-task-page-title-input';
    input.rows = 1;
    input.value = currentValue;
    input.setAttribute('aria-label', 'Edit task title');

    const editActions = document.createElement('div');
    editActions.className = 'workspace-task-page-title-edit-actions';
    editActions.innerHTML = `
      <button type="button" class="workspace-task-page-edit-save" aria-label="Save title">Save</button>
      <button type="button" class="workspace-task-page-edit-cancel" aria-label="Cancel editing">Cancel</button>
      <span class="workspace-task-page-edit-hint">Enter to save, Esc to cancel</span>
    `;

    const syncHeight = () => {
      input.style.height = 'auto';
      input.style.height = `${Math.max(input.scrollHeight, 70)}px`;
    };

    titleElement.style.display = 'none';
    if (actionsContainer) actionsContainer.style.display = 'none';
    titleElement.insertAdjacentElement('afterend', editActions);
    titleElement.insertAdjacentElement('afterend', input);
    syncHeight();
    input.focus();
    input.select();

    const finishEdit = async (save) => {
      if (!this.titleEditInProgress) return;
      this.titleEditInProgress = false;

      const nextValue = input.value.trim();
      input.remove();
      editActions.remove();
      titleElement.style.display = '';
      if (actionsContainer) actionsContainer.style.display = '';

      if (!save || nextValue === currentValue) {
        return;
      }

      if (!nextValue) {
        this.notify('error', 'Task title cannot be empty.');
        return;
      }

      try {
        await this.updateTaskFields({ description: nextValue });
        this.notify('success', 'Task title updated');
      } catch (error) {
        console.error('Failed to update task title:', error);
        this.notify('error', error?.message || 'Failed to update task title');
      }
    };

    editActions.querySelector('.workspace-task-page-edit-save')?.addEventListener('mousedown', (e) => {
      e.preventDefault();
      finishEdit(true);
    });
    editActions.querySelector('.workspace-task-page-edit-cancel')?.addEventListener('mousedown', (e) => {
      e.preventDefault();
      finishEdit(false);
    });

    input.addEventListener('input', syncHeight);
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        finishEdit(false);
        return;
      }

      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        finishEdit(true);
      }
    });
    input.addEventListener('blur', (e) => {
      if (editActions.contains(e.relatedTarget)) return;
      finishEdit(true);
    });
  }

  async updateTaskFields(patch) {
    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(this.taskId)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to update task');
    }

    const updatedTask = await response.json();
    this.task = updatedTask || this.task;
    if (Array.isArray(this.tasks)) {
      this.tasks = this.tasks.map((task) => (
        String(task?.id || '') === String(this.taskId)
          ? { ...task, ...(updatedTask || {}) }
          : task
      ));
    }
    this.render();
    return updatedTask;
  }

  getTaskDisplayLabel(task = this.task) {
    return String(task?.description || task?.name || 'Untitled Task').trim() || 'Untitled Task';
  }

  escapeHtml(value) {
    return escapeTaskHtml(value);
  }

  getTaskHref(taskId) {
    return `/workspaces/${encodeURIComponent(this.workspaceId)}/task/${encodeURIComponent(taskId)}`;
  }

  getTaskHumanLoop(task = this.task) {
    const humanLoop = task?.context?.human_loop;
    return humanLoop && typeof humanLoop === 'object' ? humanLoop : null;
  }

  getTaskStatusPresentation(task = this.task) {
    const humanLoop = this.getTaskHumanLoop(task);
    const blocked = String(task?.status || '').trim().toLowerCase() === 'blocked' ||
      String(humanLoop?.state || '').trim().toLowerCase() === 'blocked' ||
      Boolean(humanLoop?.reason) ||
      Boolean(humanLoop?.question);

    return {
      isBlocked: blocked,
      label: blocked ? 'Blocked' : getDisplayStatus(task?.status),
      className: blocked ? 'blocked' : getStatusClass(task?.status),
      reason: String(humanLoop?.reason || '').trim()
    };
  }

  normalizeAssistFieldValues(value) {
    const result = {};
    if (!Array.isArray(value)) return result;

    value.forEach((item) => {
      const id = String(item?.id || '').trim();
      const fieldValue = String(item?.value || '').trim();
      if (id && fieldValue) {
        result[id] = fieldValue;
      }
    });

    return result;
  }

  normalizeAssistWorkflowStep(value) {
    if (!value || typeof value !== 'object') return null;

    const stepType = String(value.step_type || value.stepType || '').trim().toLowerCase();
    if (stepType !== 'ask_choice' && stepType !== 'ask_form') return null;

    const normalized = {
      stepType,
      title: String(value.title || '').trim(),
      summary: String(value.summary || '').trim(),
      freeTextAllowed: value.free_text_allowed !== false && value.freeTextAllowed !== false,
      choices: [],
      fields: []
    };

    if (stepType === 'ask_choice' && Array.isArray(value.choices)) {
      normalized.choices = value.choices
        .map((choice, index) => {
          const id = String(choice?.id || '').trim() || `choice-${index + 1}`;
          const label = String(choice?.label || '').trim();
          if (!label) return null;
          return {
            id,
            label,
            description: String(choice?.description || '').trim(),
            number: String(choice?.number || '').trim()
          };
        })
        .filter(Boolean);
    }

    if (stepType === 'ask_form' && Array.isArray(value.fields)) {
      normalized.fields = value.fields
        .map((field, index) => {
          const id = String(field?.id || '').trim() || `field-${index + 1}`;
          const label = String(field?.label || '').trim() || `Question ${index + 1}`;
          const type = String(field?.type || 'text').trim().toLowerCase();
          const options = Array.isArray(field?.options)
            ? field.options.map((option, optionIndex) => ({
              value: String(option?.value || '').trim(),
              label: String(option?.label || option?.value || '').trim(),
              description: String(option?.description || '').trim(),
              key: String(option?.key || '').trim() || String.fromCharCode(65 + (optionIndex % 26))
            })).filter((option) => option.value && option.label)
            : [];

          return {
            id,
            label,
            description: String(field?.description || '').trim(),
            evidence: String(field?.evidence || '').trim(),
            type,
            placeholder: String(field?.placeholder || '').trim(),
            required: field?.required !== false,
            options
          };
        })
        .filter(Boolean);
    }

    return normalized;
  }

  buildBlockedTaskState(task = this.task) {
    const humanLoop = this.getTaskHumanLoop(task) || {};
    return {
      taskId: String(task?.id || '').trim(),
      blockId: String(humanLoop?.block_id || '').trim(),
      currentAgent: String(task?.to || '').trim(),
      reason: String(humanLoop?.reason || 'This task needs your input before it can continue.').trim(),
      question: String(humanLoop?.question || '').trim(),
      response: String(humanLoop?.agent_response || '').trim(),
      workflowStep: this.normalizeAssistWorkflowStep(humanLoop?.workflow_step || task?.context?.planning_workflow_step),
      selectedChoiceId: '',
      selectedChoiceLabel: '',
      selectedChoiceNumber: '',
      selectedFieldValues: this.normalizeAssistFieldValues(humanLoop?.field_values)
    };
  }

  getWorkspaceAgentNames() {
    const names = new Set();

    if (Array.isArray(this.workspace?.agent_instances)) {
      this.workspace.agent_instances.forEach((instance) => {
        const name = String(instance?.role || instance?.name || '').trim();
        if (name) names.add(name);
      });
    }

    if (Array.isArray(this.workspace?.agents)) {
      this.workspace.agents.forEach((name) => {
        const normalized = String(name || '').trim();
        if (normalized) names.add(normalized);
      });
    }

    if (this.task?.to) {
      names.add(String(this.task.to).trim());
    }

    return Array.from(names).filter(Boolean);
  }

  getParentTask() {
    const parentTaskId = String(this.task?.parent_task_id || '').trim();
    if (!parentTaskId) return null;
    return this.tasks.find((item) => String(item?.id || '') === parentTaskId) || null;
  }

  getSubtasks() {
    return this.tasks.filter((item) => String(item?.parent_task_id || '').trim() === String(this.task?.id || ''));
  }

  getInputTasks() {
    const inputIds = Array.isArray(this.task?.input_task_ids) ? this.task.input_task_ids : [];
    if (inputIds.length === 0) return [];

    return inputIds
      .map((taskId) => this.tasks.find((item) => String(item?.id || '') === String(taskId || '').trim()) || null)
      .filter(Boolean);
  }

  render() {
    const statusInfo = this.getTaskStatusPresentation();
    this.currentBlockedTask = statusInfo.isBlocked ? this.buildBlockedTaskState() : null;
    this.taskAssistResponseExpanded = false;

    this.renderHero(statusInfo);
    this.renderHeroActions(statusInfo);
    this.renderOverview();
    this.renderSnapshot(statusInfo);
    this.renderRelationships();
    this.renderOutput();
    this.renderSchedule();
    this.renderExecutionSteps();
    this.renderContext();
    this.renderBlockedState(statusInfo);
  }

  renderHero(statusInfo) {
    const taskTitle = this.getTaskDisplayLabel();
    const detailsSummary = summarizeText(this.task?.details || this.currentBlockedTask?.reason || '', 280);

    if (this.elements.workspaceName) {
      this.elements.workspaceName.textContent = String(this.workspace?.name || 'Workspace').trim() || 'Workspace';
    }
    if (this.elements.title) {
      this.elements.title.textContent = taskTitle;
    }
    document.title = `${taskTitle} - Ori Agent`;
    if (this.elements.subtitle) {
      this.elements.subtitle.textContent = detailsSummary || 'No additional details provided.';
    }
    if (this.elements.breadcrumbTitle) {
      this.elements.breadcrumbTitle.textContent = summarizeText(taskTitle, 40) || 'Task';
    }
    if (this.elements.status) {
      this.elements.status.textContent = statusInfo.label;
      this.elements.status.dataset.state = statusInfo.className;
    }
    if (this.elements.liveBadge) {
      const isLive = statusInfo.className === 'in_progress' && this.workspaceRealtimeUnsubscribe;
      this.elements.liveBadge.hidden = !isLive;
    }
  }

  renderHeroActions(statusInfo) {
    if (!this.elements.heroActions) return;

    const status = String(this.task?.status || '').trim().toLowerCase();
    const hasAgent = Boolean(this.task?.to);
    const buttons = [];

    if (status === 'pending' && hasAgent) {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn workspace-task-page-hero-btn-primary" data-action="execute">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8,5.14V19.14L19,12.14L8,5.14Z"/></svg>Run
      </button>`);
    }

    if (status === 'failed' || status === 'completed') {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn" data-action="execute">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/></svg>Re-run
      </button>`);
    }

    if ((status === 'pending' || status === 'assigned' || status === 'in_progress') && !statusInfo.isBlocked) {
      buttons.push(`<button type="button" class="workspace-task-page-hero-btn" data-action="complete">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/></svg>Mark Complete
      </button>`);
    }

    this.elements.heroActions.innerHTML = buttons.join('');

    this.elements.heroActions.querySelectorAll('[data-action]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const action = btn.dataset.action;
        if (action === 'execute') this.executeTask();
        if (action === 'complete') this.completeTask();
      });
    });
  }

  renderOverview() {
    if (!this.elements.overview) return;

    const progress = this.task?.progress;
    const templateRef = this.task?.template_ref;
    const executionMode = String(this.task?.execution_mode || 'auto').replace(/_/g, ' ');
    const orchestrationMode = String(this.task?.orchestration_mode || '').replace(/_/g, ' ');

    const items = [
      {
        title: 'Requested By',
        value: String(this.task?.from || 'Workspace').trim() || 'Workspace'
      },
      {
        title: 'Assigned To',
        value: String(this.task?.to || 'Unassigned').trim() || 'Unassigned'
      },
      {
        title: 'Execution Mode',
        value: executionMode ? executionMode.replace(/\b\w/g, (char) => char.toUpperCase()) : 'Auto'
      }
    ];

    if (orchestrationMode) {
      items.push({
        title: 'Orchestration',
        value: orchestrationMode.replace(/\b\w/g, (char) => char.toUpperCase())
      });
    }

    if (progress && (progress.current_step || Number.isFinite(progress.percentage))) {
      const progressLabel = [
        Number.isFinite(Number(progress.percentage)) ? `${Number(progress.percentage)}% complete` : '',
        String(progress.current_step || '').trim(),
        Number(progress.total_steps) > 0
          ? `${Number(progress.completed_steps || 0)}/${Number(progress.total_steps)} steps`
          : ''
      ].filter(Boolean).join(' • ');
      items.push({
        title: 'Progress',
        value: progressLabel || 'Progress available'
      });
    }

    if (templateRef?.template_name || templateRef?.step_name) {
      items.push({
        title: 'Template',
        value: [templateRef.template_name, templateRef.step_name].filter(Boolean).join(' / ')
      });
    }

    const detailsValue = String(this.task?.details || '').trim() || 'No extra task details were provided.';
    items.push({
      title: 'Task Details',
      value: detailsValue,
      full: true,
      editable: true
    });

    this.elements.overview.innerHTML = items.map((item) => `
      <article class="workspace-task-overview-item${item.full ? ' full' : ''}">
        <div class="workspace-task-overview-title">
          ${this.escapeHtml(item.title)}
          ${item.editable ? `<button type="button" class="workspace-task-overview-edit-btn" data-edit-field="details" aria-label="Edit details" title="Edit details">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/></svg>
          </button>` : ''}
        </div>
        <div class="workspace-task-overview-value">${this.escapeHtml(item.value)}</div>
      </article>
    `).join('');

    this.elements.overview.querySelectorAll('[data-edit-field="details"]').forEach((btn) => {
      btn.addEventListener('click', () => this.startDetailsEdit(btn));
    });
  }

  startDetailsEdit(triggerBtn) {
    const article = triggerBtn.closest('.workspace-task-overview-item');
    if (!article) return;

    const valueEl = article.querySelector('.workspace-task-overview-value');
    if (!valueEl) return;

    const currentValue = String(this.task?.details || '').trim();
    const textarea = document.createElement('textarea');
    textarea.className = 'form-control workspace-task-overview-edit-textarea';
    textarea.rows = 4;
    textarea.value = currentValue;

    const actions = document.createElement('div');
    actions.className = 'workspace-task-page-title-edit-actions';
    actions.style.marginTop = '0.5rem';
    actions.innerHTML = `
      <button type="button" class="workspace-task-page-edit-save" aria-label="Save details">Save</button>
      <button type="button" class="workspace-task-page-edit-cancel" aria-label="Cancel editing">Cancel</button>
    `;

    valueEl.style.display = 'none';
    triggerBtn.style.display = 'none';
    valueEl.insertAdjacentElement('afterend', actions);
    valueEl.insertAdjacentElement('afterend', textarea);
    textarea.focus();

    const finish = async (save) => {
      const nextValue = textarea.value.trim();
      textarea.remove();
      actions.remove();
      valueEl.style.display = '';
      triggerBtn.style.display = '';

      if (!save || nextValue === currentValue) return;

      try {
        await this.updateTaskFields({ details: nextValue });
        this.notify('success', 'Task details updated');
      } catch (error) {
        this.notify('error', error?.message || 'Failed to update details');
      }
    };

    actions.querySelector('.workspace-task-page-edit-save')?.addEventListener('mousedown', (e) => { e.preventDefault(); finish(true); });
    actions.querySelector('.workspace-task-page-edit-cancel')?.addEventListener('mousedown', (e) => { e.preventDefault(); finish(false); });
    textarea.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') { e.preventDefault(); finish(false); }
    });
    textarea.addEventListener('blur', (e) => {
      if (actions.contains(e.relatedTarget)) return;
      finish(true);
    });
  }

  renderSnapshot(statusInfo) {
    if (!this.elements.snapshot) return;

    const currentAgent = String(this.task?.to || 'Unassigned').trim() || 'Unassigned';
    const statusOptions = ['pending', 'assigned', 'in_progress', 'completed', 'failed', 'blocked', 'cancelled'];
    const agentNames = this.getWorkspaceAgentNames();

    const snapshotItems = [
      ['Created', formatDateTime(this.task?.created_at)],
      ['Started', formatDateTime(this.task?.started_at)],
      ['Completed', formatDateTime(this.task?.completed_at)],
      ['Task Type', this.getSubtasks().length > 0 ? 'Parent Task' : 'Leaf Task']
    ];

    if (this.task?.schedule_name) {
      snapshotItems.push(['Schedule', String(this.task.schedule_name).trim()]);
    }

    const statusSelectHtml = `
      <div class="workspace-task-snapshot-item">
        <span class="workspace-task-snapshot-label">Status</span>
        <select id="workspace-task-snapshot-status" class="workspace-task-snapshot-select workspace-task-page-status-inline" data-state="${this.escapeHtml(statusInfo.className)}">
          ${statusOptions.map((s) => `<option value="${this.escapeHtml(s)}" ${s === (this.task?.status || 'pending') ? 'selected' : ''}>${this.escapeHtml(getDisplayStatus(s))}</option>`).join('')}
        </select>
      </div>
    `;

    const agentSelectHtml = `
      <div class="workspace-task-snapshot-item">
        <span class="workspace-task-snapshot-label">Agent</span>
        <select id="workspace-task-snapshot-agent" class="workspace-task-snapshot-select">
          <option value="" ${!this.task?.to ? 'selected' : ''}>Unassigned</option>
          ${agentNames.map((name) => `<option value="${this.escapeHtml(name)}" ${name === this.task?.to ? 'selected' : ''}>${this.escapeHtml(name)}</option>`).join('')}
        </select>
      </div>
    `;

    const currentPriority = Number(this.task?.priority) || 3;
    const priorityLabels = { 1: '1 - Highest', 2: '2 - High', 3: '3 - Medium', 4: '4 - Low', 5: '5 - Lowest' };
    const prioritySelectHtml = `
      <div class="workspace-task-snapshot-item">
        <span class="workspace-task-snapshot-label">Priority</span>
        <select id="workspace-task-snapshot-priority" class="workspace-task-snapshot-select">
          ${[1, 2, 3, 4, 5].map((p) => `<option value="${p}" ${p === currentPriority ? 'selected' : ''}>${this.escapeHtml(priorityLabels[p])}</option>`).join('')}
        </select>
      </div>
    `;

    const staticItemsHtml = snapshotItems.map(([label, value]) => `
      <div class="workspace-task-snapshot-item">
        <span class="workspace-task-snapshot-label">${this.escapeHtml(label)}</span>
        <span class="workspace-task-snapshot-value">${this.escapeHtml(value || '—')}</span>
      </div>
    `).join('');

    this.elements.snapshot.innerHTML = statusSelectHtml + agentSelectHtml + prioritySelectHtml + staticItemsHtml;

    const statusSelect = document.getElementById('workspace-task-snapshot-status');
    const agentSelect = document.getElementById('workspace-task-snapshot-agent');

    statusSelect?.addEventListener('change', async () => {
      try {
        await this.updateTaskFields({ status: statusSelect.value });
        this.notify('success', 'Status updated');
      } catch (error) {
        this.notify('error', error?.message || 'Failed to update status');
      }
    });

    agentSelect?.addEventListener('change', async () => {
      try {
        await this.updateTaskFields({ to: agentSelect.value || '' });
        this.notify('success', 'Agent updated');
      } catch (error) {
        this.notify('error', error?.message || 'Failed to update agent');
      }
    });

    const prioritySelect = document.getElementById('workspace-task-snapshot-priority');
    prioritySelect?.addEventListener('change', async () => {
      try {
        await this.updateTaskFields({ priority: Number(prioritySelect.value) || 3 });
        this.notify('success', 'Priority updated');
      } catch (error) {
        this.notify('error', error?.message || 'Failed to update priority');
      }
    });
  }

  renderRelationships() {
    if (!this.elements.relationships || !this.elements.relationshipsCard) return;

    const parentTask = this.getParentTask();
    const inputTasks = this.getInputTasks();
    const subtasks = this.getSubtasks();
    const groups = [];

    if (parentTask) {
      groups.push({
        title: 'Parent Task',
        tasks: [parentTask]
      });
    }

    if (inputTasks.length > 0) {
      groups.push({
        title: 'Input Tasks',
        tasks: inputTasks
      });
    }

    if (subtasks.length > 0) {
      groups.push({
        title: 'Subtasks',
        tasks: subtasks
      });
    }

    if (groups.length === 0) {
      this.elements.relationshipsCard.hidden = true;
      this.elements.relationships.innerHTML = '';
      return;
    }

    this.elements.relationshipsCard.hidden = false;
    this.elements.relationships.innerHTML = groups.map((group) => `
      <section class="workspace-task-relationship-group">
        <div class="workspace-task-relationship-title">${this.escapeHtml(group.title)}</div>
        <div class="workspace-task-related-links">
          ${group.tasks.map((task) => `
            <a href="${this.getTaskHref(task.id)}" class="workspace-task-related-link">
              <span class="workspace-task-related-link-title">${this.escapeHtml(this.getTaskDisplayLabel(task))}</span>
              <span class="workspace-task-related-link-meta">${this.escapeHtml(getDisplayStatus(task.status))} • ${this.escapeHtml(String(task.to || 'Unassigned').trim() || 'Unassigned')}</span>
            </a>
          `).join('')}
        </div>
      </section>
    `).join('');
  }

  isStructuredData(text) {
    const trimmed = text.trim();
    if ((trimmed.startsWith('{') && trimmed.endsWith('}')) ||
        (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
      try { JSON.parse(trimmed); return true; } catch (_e) { /* not json */ }
    }
    return false;
  }

  renderMarkdownOrPre(text) {
    if (this.isStructuredData(text)) {
      return `<pre class="workspace-task-page-code-block">${this.escapeHtml(text)}</pre>`;
    }
    if (typeof marked !== 'undefined' && typeof marked.parse === 'function') {
      return `<div class="workspace-task-page-prose">${marked.parse(text)}</div>`;
    }
    return `<pre class="workspace-task-page-code-block">${this.escapeHtml(text)}</pre>`;
  }

  renderOutput() {
    if (!this.elements.output || !this.elements.outputCard) return;

    const result = normalizeResultText(this.task?.result).trim();
    const error = String(this.task?.error || '').trim();

    if (!result && !error) {
      this.elements.outputCard.hidden = true;
      this.elements.output.innerHTML = '';
      return;
    }

    const blocks = [];
    if (result) {
      blocks.push(`
        <div class="workspace-task-page-mini-label">Result</div>
        ${this.renderMarkdownOrPre(result)}
      `);
    }
    if (error) {
      blocks.push(`
        <div class="workspace-task-page-mini-label">Error</div>
        <pre class="workspace-task-page-code-block">${this.escapeHtml(error)}</pre>
      `);
    }

    this.elements.outputCard.hidden = false;
    this.elements.output.innerHTML = blocks.join('');
  }

  renderSchedule() {
    if (!this.elements.schedule || !this.elements.scheduleCard) return;

    const isScheduled = this.task?.schedule_enabled || this.task?.schedule;
    const history = Array.isArray(this.task?.execution_history) ? this.task.execution_history : [];
    const executionCount = Number(this.task?.execution_count) || 0;
    const failureCount = Number(this.task?.failure_count) || 0;

    if (!isScheduled && history.length === 0 && executionCount === 0) {
      this.elements.scheduleCard.hidden = true;
      this.elements.schedule.innerHTML = '';
      return;
    }

    this.elements.scheduleCard.hidden = false;

    const stats = [];
    stats.push({ label: 'Total Runs', value: String(executionCount) });
    stats.push({ label: 'Failures', value: String(failureCount) });
    if (this.task?.next_run) {
      stats.push({ label: 'Next Run', value: formatDateTime(this.task.next_run) });
    }
    if (this.task?.last_run) {
      stats.push({ label: 'Last Run', value: formatDateTime(this.task.last_run) });
    }

    const statsHtml = `
      <div class="workspace-task-schedule-stats">
        ${stats.map((s) => `
          <div class="workspace-task-schedule-stat">
            <div class="workspace-task-schedule-stat-label">${this.escapeHtml(s.label)}</div>
            <div class="workspace-task-schedule-stat-value">${this.escapeHtml(s.value)}</div>
          </div>
        `).join('')}
      </div>
    `;

    let historyHtml = '';
    if (history.length > 0) {
      const recentRuns = history.slice(-10).reverse();
      historyHtml = `
        <div class="workspace-task-page-mini-label">Recent runs</div>
        <div class="workspace-task-schedule-history">
          ${recentRuns.map((run) => {
            const runStatus = String(run?.status || 'completed').trim().toLowerCase();
            const statusClass = getStatusClass(runStatus);
            return `
              <div class="workspace-task-schedule-run">
                <span>${this.escapeHtml(formatDateTime(run?.completed_at || run?.started_at))}</span>
                <span class="workspace-task-schedule-run-status" data-state="${this.escapeHtml(statusClass)}">${this.escapeHtml(getDisplayStatus(runStatus))}</span>
              </div>
            `;
          }).join('')}
        </div>
      `;
    }

    this.elements.schedule.innerHTML = statsHtml + historyHtml;
  }

  renderExecutionSteps() {
    if (!this.elements.steps || !this.elements.stepsCard) return;

    const steps = Array.isArray(this.task?.execution_steps) ? this.task.execution_steps : [];
    if (steps.length === 0) {
      this.elements.stepsCard.hidden = true;
      this.elements.steps.innerHTML = '';
      return;
    }

    this.elements.stepsCard.hidden = false;
    this.elements.steps.innerHTML = steps.map((step, index) => {
      const resultText = String(step?.result || '').trim();
      const errorText = String(step?.error || '').trim();
      const isLongResult = resultText.length > 400;
      const isLongError = errorText.length > 400;

      return `
        <article class="workspace-task-step">
          <div class="workspace-task-step-index">${index + 1}</div>
          <div>
            <div class="workspace-task-step-title-row">
              <span class="workspace-task-step-title">${this.escapeHtml(String(step?.title || `Step ${index + 1}`).trim())}</span>
              ${step?.tag ? `<span class="workspace-task-step-tag">${this.escapeHtml(String(step.tag).trim())}</span>` : ''}
              <span class="workspace-task-step-status" data-state="${this.escapeHtml(getStatusClass(step?.status))}">${this.escapeHtml(getDisplayStatus(step?.status))}</span>
            </div>
            ${step?.detail ? `<div class="workspace-task-step-copy">${this.escapeHtml(String(step.detail).trim())}</div>` : ''}
            ${resultText ? (isLongResult
              ? `<details class="workspace-task-collapsible mt-2"><summary class="workspace-task-collapsible-toggle">Show result</summary><pre class="workspace-task-page-code-block">${this.escapeHtml(resultText)}</pre></details>`
              : `<pre class="workspace-task-page-code-block mt-2">${this.escapeHtml(resultText)}</pre>`)
            : ''}
            ${errorText ? (isLongError
              ? `<details class="workspace-task-collapsible mt-2"><summary class="workspace-task-collapsible-toggle">Show error</summary><pre class="workspace-task-page-code-block">${this.escapeHtml(errorText)}</pre></details>`
              : `<pre class="workspace-task-page-code-block mt-2">${this.escapeHtml(errorText)}</pre>`)
            : ''}
          </div>
        </article>
      `;
    }).join('');
  }

  renderContext() {
    if (!this.elements.context || !this.elements.contextCard) return;

    const context = this.task?.context && typeof this.task.context === 'object'
      ? { ...this.task.context }
      : {};
    delete context.human_loop;

    const contextText = Object.keys(context).length > 0
      ? normalizeResultText(context)
      : '';

    if (!contextText) {
      this.elements.contextCard.hidden = true;
      this.elements.context.innerHTML = '';
      return;
    }

    this.elements.contextCard.hidden = false;
    this.elements.context.innerHTML = `
      <pre class="workspace-task-page-code-block">${this.escapeHtml(contextText)}</pre>
    `;
  }

  renderBlockedState(statusInfo) {
    const blocked = statusInfo.isBlocked && this.currentBlockedTask;

    if (!blocked) {
      if (this.elements.assistCard) this.elements.assistCard.hidden = true;
      if (this.elements.blockedContextCard) this.elements.blockedContextCard.hidden = true;
      return;
    }

    this.renderBlockedContext();
    this.renderAssistCard();
  }

  renderBlockedContext() {
    if (!this.elements.blockedContextCard) return;

    const response = String(this.currentBlockedTask?.response || '').trim();
    const responsePreview = summarizeText(response, 260);

    this.elements.blockedContextCard.hidden = false;
    if (this.elements.blockedReason) {
      this.elements.blockedReason.textContent = this.currentBlockedTask?.reason || 'This task is waiting for your input.';
    }

    if (this.elements.blockedRequestWrap) {
      const hasResponse = Boolean(response);
      this.elements.blockedRequestWrap.classList.toggle('d-none', !hasResponse);
    }
    if (this.elements.blockedRequestPreview) {
      this.elements.blockedRequestPreview.textContent = responsePreview || '';
    }
    if (this.elements.blockedRequest) {
      this.elements.blockedRequest.textContent = response || '';
      this.elements.blockedRequest.classList.toggle('d-none', !this.taskAssistResponseExpanded || !response);
    }
    if (this.elements.blockedRequestToggle) {
      const hasLongResponse = response.length > 0 && response !== responsePreview;
      this.elements.blockedRequestToggle.classList.toggle('d-none', !hasLongResponse);
      this.elements.blockedRequestToggle.textContent = this.taskAssistResponseExpanded ? 'Hide full request' : 'View full request';
      this.elements.blockedRequestToggle.setAttribute('aria-expanded', this.taskAssistResponseExpanded ? 'true' : 'false');
    }
  }

  renderAssistCard() {
    if (!this.elements.assistCard) return;

    const workflowStep = this.currentBlockedTask?.workflowStep || null;
    this.elements.assistCard.hidden = false;

    if (this.elements.assistKnown) {
      this.elements.assistKnown.textContent = summarizeText(
        this.currentBlockedTask?.response || this.currentBlockedTask?.reason || 'The task is paused waiting on your input.',
        190
      ) || 'The task is paused waiting on your input.';
    }
    if (this.elements.assistNeeds) {
      this.elements.assistNeeds.textContent = this.getAssistNeedsSummary(workflowStep);
    }
    if (this.elements.assistNext) {
      this.elements.assistNext.textContent = this.getAssistNextSummary(workflowStep);
    }
    if (this.elements.assistQuestionWrap) {
      const showQuestion = Boolean(this.currentBlockedTask?.question) && workflowStep?.stepType !== 'ask_form';
      this.elements.assistQuestionWrap.classList.toggle('d-none', !showQuestion);
    }
    if (this.elements.assistQuestion) {
      this.elements.assistQuestion.textContent = this.currentBlockedTask?.question || '';
    }

    this.populateAssistAgents(this.currentBlockedTask?.currentAgent || '');
    this.renderWorkflowStepUI(workflowStep);
    this.updateAssistSwitchButtonState();
  }

  getAssistNeedsSummary(workflowStep) {
    if (workflowStep?.stepType === 'ask_form' && Array.isArray(workflowStep.fields) && workflowStep.fields.length > 0) {
      return `Answer ${workflowStep.fields.length} question${workflowStep.fields.length === 1 ? '' : 's'} so the agent can continue.`;
    }
    if (workflowStep?.stepType === 'ask_choice' && Array.isArray(workflowStep.choices) && workflowStep.choices.length > 0) {
      return `Choose 1 of ${workflowStep.choices.length} next-step options or add your own guidance.`;
    }
    if (this.currentBlockedTask?.question) {
      return summarizeText(this.currentBlockedTask.question, 180);
    }
    return 'Add the missing detail the agent asked for.';
  }

  getAssistNextSummary(workflowStep) {
    if (workflowStep?.stepType === 'ask_form') {
      return 'Continue sends your answers and any extra guidance back to the assigned agent.';
    }
    if (workflowStep?.stepType === 'ask_choice') {
      return 'Pick the best path, optionally add guidance, then continue the task.';
    }
    return 'Retry, continue with guidance, switch agents, or mark the task failed.';
  }

  populateAssistAgents(currentAgent = '') {
    if (!this.elements.assistAgent) return;

    const currentNormalized = String(currentAgent || '').trim().toLowerCase();
    const options = ['<option value="">Keep current assignment</option>'];

    this.getWorkspaceAgentNames().forEach((agentName) => {
      const normalized = String(agentName || '').trim();
      if (!normalized || normalized.toLowerCase() === currentNormalized) return;
      options.push(`<option value="${this.escapeHtml(normalized)}">${this.escapeHtml(normalized)}</option>`);
    });

    this.elements.assistAgent.innerHTML = options.join('');
  }

  renderWorkflowStepUI(workflowStep) {
    if (!this.elements.assistFormWrap || !this.elements.assistFormFields) return;

    if (!workflowStep) {
      this.elements.assistFormWrap.classList.add('d-none');
      this.elements.assistFormFields.innerHTML = '';
      return;
    }

    this.elements.assistFormWrap.classList.remove('d-none');

    if (workflowStep.stepType === 'ask_choice') {
      this.renderChoiceWorkflow(workflowStep);
      return;
    }

    if (workflowStep.stepType === 'ask_form') {
      this.renderFormWorkflow(workflowStep);
      return;
    }

    this.elements.assistFormWrap.classList.add('d-none');
    this.elements.assistFormFields.innerHTML = '';
  }

  renderChoiceWorkflow(workflowStep) {
    const selectedChoiceId = String(this.currentBlockedTask?.selectedChoiceId || '').trim();
    this.elements.assistFormFields.innerHTML = `
      <div class="workspace-task-assist-option-group" role="radiogroup" aria-label="Choose a next step">
        ${workflowStep.choices.map((choice, index) => `
          <button
            type="button"
            class="workspace-task-assist-option${selectedChoiceId === choice.id ? ' is-selected' : ''}"
            data-assist-choice-id="${this.escapeHtml(choice.id)}"
            aria-pressed="${selectedChoiceId === choice.id ? 'true' : 'false'}">
            <span class="workspace-task-assist-option-card">
              <span class="workspace-task-assist-option-key">${this.escapeHtml(choice.number || String.fromCharCode(65 + (index % 26)))}</span>
              <span class="workspace-task-assist-option-copy">
                <span class="workspace-task-assist-option-label">${this.escapeHtml(choice.label)}</span>
                ${choice.description ? `<span class="workspace-task-assist-option-description">${this.escapeHtml(choice.description)}</span>` : ''}
              </span>
            </span>
          </button>
        `).join('')}
      </div>
    `;

    this.elements.assistFormFields.querySelectorAll('[data-assist-choice-id]').forEach((button) => {
      button.addEventListener('click', () => {
        const choiceId = String(button.getAttribute('data-assist-choice-id') || '').trim();
        const choice = workflowStep.choices.find((item) => item.id === choiceId);
        if (!choice || !this.currentBlockedTask) return;
        this.currentBlockedTask.selectedChoiceId = choice.id;
        this.currentBlockedTask.selectedChoiceLabel = choice.label;
        this.currentBlockedTask.selectedChoiceNumber = choice.number || '';
        this.renderChoiceWorkflow(workflowStep);
      });
    });
  }

  renderFormWorkflow(workflowStep) {
    const selectedValues = this.currentBlockedTask?.selectedFieldValues || {};

    this.elements.assistFormFields.innerHTML = workflowStep.fields.map((field, index) => {
      const value = String(selectedValues[field.id] || '').trim();
      const requiredMark = field.required ? ' <span aria-hidden="true">*</span>' : '';
      const questionIntro = `
        <div class="workspace-task-assist-field-question">
          <span class="workspace-task-assist-field-number">${index + 1}</span>
          <div>
            <div class="workspace-task-assist-field-prompt">${this.escapeHtml(this.getAssistFieldPrompt(field))}</div>
            ${field.description && this.getAssistFieldPrompt(field) !== field.description
              ? `<div class="workspace-task-assist-field-hint">${this.escapeHtml(field.description)}</div>`
              : ''}
            ${field.evidence ? `<div class="workspace-task-assist-field-evidence">${this.escapeHtml(field.evidence)}</div>` : ''}
          </div>
        </div>
      `;

      if (field.type === 'select' && Array.isArray(field.options) && field.options.length > 0) {
        return `
          <article class="workspace-task-assist-field">
            ${questionIntro}
            <div class="workspace-task-assist-option-group" role="radiogroup" aria-label="${this.escapeHtml(field.label)}">
              ${field.options.map((option) => `
                <label class="workspace-task-assist-option">
                  <input
                    class="workspace-task-assist-option-input"
                    type="radio"
                    name="workspace-task-assist-field-${this.escapeHtml(field.id)}"
                    value="${this.escapeHtml(option.value)}"
                    data-assist-field-id="${this.escapeHtml(field.id)}"
                    ${value === option.value ? 'checked' : ''}>
                  <span class="workspace-task-assist-option-card">
                    <span class="workspace-task-assist-option-key">${this.escapeHtml(option.key || option.value)}</span>
                    <span class="workspace-task-assist-option-copy">
                      <span class="workspace-task-assist-option-label">${this.escapeHtml(option.label)}</span>
                      ${option.description ? `<span class="workspace-task-assist-option-description">${this.escapeHtml(option.description)}</span>` : ''}
                    </span>
                  </span>
                </label>
              `).join('')}
            </div>
          </article>
        `;
      }

      if (field.type === 'textarea') {
        return `
          <article class="workspace-task-assist-field">
            ${questionIntro}
            <label class="form-label" for="workspace-task-field-${this.escapeHtml(field.id)}">${this.escapeHtml(field.label)}${requiredMark}</label>
            <textarea id="workspace-task-field-${this.escapeHtml(field.id)}" class="form-control" rows="3" data-assist-field-id="${this.escapeHtml(field.id)}" placeholder="${this.escapeHtml(field.placeholder || '')}">${this.escapeHtml(value)}</textarea>
          </article>
        `;
      }

      const inputType = field.type === 'number' ? 'number' : 'text';
      return `
        <article class="workspace-task-assist-field">
          ${questionIntro}
          <label class="form-label" for="workspace-task-field-${this.escapeHtml(field.id)}">${this.escapeHtml(field.label)}${requiredMark}</label>
          <input id="workspace-task-field-${this.escapeHtml(field.id)}" class="form-control" type="${inputType}" data-assist-field-id="${this.escapeHtml(field.id)}" value="${this.escapeHtml(value)}" placeholder="${this.escapeHtml(field.placeholder || '')}">
        </article>
      `;
    }).join('');

    this.elements.assistFormFields.querySelectorAll('[data-assist-field-id]').forEach((fieldElement) => {
      const syncValue = () => {
        const fieldId = String(fieldElement.getAttribute('data-assist-field-id') || '').trim();
        if (!fieldId) return;
        if (fieldElement instanceof HTMLInputElement && fieldElement.type === 'radio' && !fieldElement.checked) {
          return;
        }
        this.setAssistFormFieldValue(fieldId, fieldElement.value);
      };

      fieldElement.addEventListener('input', syncValue);
      fieldElement.addEventListener('change', syncValue);
    });
  }

  getAssistFieldPrompt(field) {
    const description = String(field?.description || '').trim();
    if (description) {
      return description.endsWith('?') ? description : `${description}?`;
    }
    const label = String(field?.label || '').trim();
    return label.endsWith('?') ? label : `${label}?`;
  }

  setAssistFormFieldValue(fieldId, value) {
    if (!this.currentBlockedTask || !fieldId) return;

    const normalizedValue = String(value || '').trim();
    if (!this.currentBlockedTask.selectedFieldValues || typeof this.currentBlockedTask.selectedFieldValues !== 'object') {
      this.currentBlockedTask.selectedFieldValues = {};
    }

    if (!normalizedValue) {
      delete this.currentBlockedTask.selectedFieldValues[fieldId];
      return;
    }

    this.currentBlockedTask.selectedFieldValues[fieldId] = normalizedValue;
  }

  collectAssistFormFieldValues() {
    const workflowStep = this.currentBlockedTask?.workflowStep;
    if (!workflowStep || workflowStep.stepType !== 'ask_form' || !Array.isArray(workflowStep.fields)) {
      return [];
    }

    const selectedValues = this.currentBlockedTask?.selectedFieldValues || {};
    return workflowStep.fields
      .map((field) => {
        const value = String(selectedValues[field.id] || '').trim();
        if (!value) return null;
        return {
          id: field.id,
          label: field.label,
          value
        };
      })
      .filter(Boolean);
  }

  updateAssistSwitchButtonState() {
    if (!this.elements.assistSwitchBtn) return;
    const selectedAgent = String(this.elements.assistAgent?.value || '').trim();
    this.elements.assistSwitchBtn.disabled = !selectedAgent;
  }

  toggleAssistResponseExpanded() {
    this.taskAssistResponseExpanded = !this.taskAssistResponseExpanded;
    this.renderBlockedContext();
  }

  setAssistButtonsDisabled(disabled) {
    [
      this.elements.assistRetryBtn,
      this.elements.assistContinueBtn,
      this.elements.assistSwitchBtn,
      this.elements.assistFailBtn
    ].forEach((button) => {
      if (button) button.disabled = disabled;
    });
  }

  async deleteTask() {
    const taskTitle = this.getTaskDisplayLabel();
    if (!confirm(`Delete "${taskTitle}"? This cannot be undone.`)) return;

    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.taskId)}?workspace_id=${encodeURIComponent(this.workspaceId)}`,
        { method: 'DELETE' }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to delete task');
      }
      window.location.href = `/workspaces/${encodeURIComponent(this.workspaceId)}`;
    } catch (error) {
      console.error('Failed to delete task:', error);
      this.notify('error', error?.message || 'Failed to delete task');
    }
  }

  async executeTask() {
    const status = String(this.task?.status || '').trim().toLowerCase();
    const isRerun = status === 'completed' || status === 'failed';
    const label = isRerun ? 'Re-run' : 'Run';

    if (isRerun && !confirm(`${label} this task? Previous results will be replaced.`)) return;

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: this.taskId })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to execute task');
      }
      this.notify('success', `Task ${label.toLowerCase()} started`);
      await this.loadData();
    } catch (error) {
      console.error('Failed to execute task:', error);
      this.notify('error', error?.message || 'Failed to execute task');
    }
  }

  async completeTask() {
    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.taskId)}/complete`,
        { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to complete task');
      }
      this.notify('success', 'Task marked as complete');
      await this.loadData();
    } catch (error) {
      console.error('Failed to complete task:', error);
      this.notify('error', error?.message || 'Failed to complete task');
    }
  }

  async copyToClipboard(text, successMessage = 'Copied') {
    try {
      await navigator.clipboard.writeText(text);
      this.notify('success', successMessage);
    } catch (_error) {
      this.notify('error', 'Failed to copy');
    }
  }

  notify(kind, message) {
    if (window.Toast && typeof window.Toast[kind] === 'function') {
      window.Toast[kind](message);
      return;
    }

    this.setAlert(message);
  }

  async submitTaskAssist(action) {
    if (!this.currentBlockedTask?.taskId) return;

    const selectedAgent = String(this.elements.assistAgent?.value || '').trim();
    const message = String(this.elements.assistMessage?.value || '').trim();
    const workflowStep = this.currentBlockedTask.workflowStep;
    const selectedChoiceId = String(this.currentBlockedTask.selectedChoiceId || '').trim();
    const fieldValues = this.collectAssistFormFieldValues();

    if (action === 'switch_agent_retry' && !selectedAgent) {
      this.notify('warning', 'Select an agent before switching and retrying.');
      return;
    }

    if (action === 'switch_agent_retry' &&
        selectedAgent.toLowerCase() === String(this.currentBlockedTask.currentAgent || '').trim().toLowerCase()) {
      this.notify('warning', 'Choose a different agent before switching.');
      return;
    }

    if (action === 'continue_with_instruction' &&
        workflowStep?.stepType === 'ask_choice' &&
        !selectedChoiceId &&
        !message) {
      this.notify('warning', 'Choose a next step or add guidance before continuing.');
      return;
    }

    if (action === 'continue_with_instruction' &&
        workflowStep?.stepType === 'ask_form') {
      const requiredFields = Array.isArray(workflowStep.fields)
        ? workflowStep.fields.filter((field) => field?.required !== false)
        : [];
      const missingRequired = requiredFields.filter((field) => !fieldValues.some((item) => item.id === field.id));
      if (missingRequired.length > 0) {
        this.notify('warning', 'Answer the required questions before continuing.');
        return;
      }

      if (fieldValues.length === 0 && !message) {
        this.notify('warning', 'Answer at least one question or add guidance before continuing.');
        return;
      }
    }

    const payload = {
      action,
      block_id: this.currentBlockedTask.blockId || undefined,
      message: message || undefined,
      agent: selectedAgent || undefined,
      choice_id: selectedChoiceId || undefined,
      choice_label: this.currentBlockedTask.selectedChoiceLabel || undefined,
      choice_number: this.currentBlockedTask.selectedChoiceNumber || undefined,
      field_values: fieldValues.length > 0 ? fieldValues : undefined
    };

    this.setAssistButtonsDisabled(true);
    this.setAlert('');

    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(this.currentBlockedTask.taskId)}/assist`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to update task');
      }

      this.notify('success', 'Task updated');
      if (this.elements.assistMessage) {
        this.elements.assistMessage.value = '';
      }
      await this.loadData();
    } catch (error) {
      console.error('Failed to submit task assistance:', error);
      this.notify('error', error?.message || 'Failed to update task');
    } finally {
      this.setAssistButtonsDisabled(false);
      this.updateAssistSwitchButtonState();
    }
  }
}
