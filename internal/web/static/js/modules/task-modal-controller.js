/**
 * task-modal-controller.js
 *
 * Shared controller for the task modal used across the application.
 * Handles creating, editing, and scheduling tasks from any context.
 */

class TaskModalController {
  constructor() {
    this.editingTaskId = null;
    this.workspaceId = null;
    this.onSaveCallback = null;
    this.initialized = false;
    this.defaultOutputDir = null;
    this.homeDir = null;
    // Auto mode state
    this.autoMode = false;
    this.llmAvailable = false;
    this.systemModelConfigured = false;
    this.progressSteps = ['parse', 'prepare', 'apply'];
    // File attachment state
    this.pendingFiles = [];
    this.pendingFilePaths = []; // File path references (no upload, backend reads directly)
    this.pendingDirectories = [];
    // Subtask state
    this.subtaskCounter = 0;
    this.subtasksToDelete = new Set();
    this.loadedSubtasks = [];
    this.subtaskSectionDisabled = false;
    this.workspaceTasks = [];
    // Current task being edited (for auto edit mode)
    this.currentTask = null;
  }

  /**
   * Initialize the modal controller and bind event handlers
   */
  init() {
    if (this.initialized) return;

    // Close button handlers
    document.getElementById('taskModalClose')?.addEventListener('click', () => this.close());
    document.getElementById('taskModalCancel')?.addEventListener('click', () => this.close());
    document.querySelector('.task-modal-backdrop')?.addEventListener('click', () => this.close());

    // Save button
    document.getElementById('taskModalSave')?.addEventListener('click', () => this.save());

    // Schedule fields toggle
    document.getElementById('taskModalScheduleEnabled')?.addEventListener('change', (e) => {
      const scheduleFields = document.getElementById('taskModalScheduleFields');
      if (scheduleFields) {
        scheduleFields.style.display = e.target.checked ? 'block' : 'none';
      }
    });

    // Schedule type change handler
    document.getElementById('taskModalScheduleType')?.addEventListener('change', () => {
      this.updateScheduleTypeFields();
    });

    // Auto-save fields toggle
    document.getElementById('taskModalAutoSaveEnabled')?.addEventListener('change', (e) => {
      const autoSaveFields = document.getElementById('taskModalAutoSaveFields');
      if (autoSaveFields) {
        autoSaveFields.style.display = e.target.checked ? 'block' : 'none';
      }
    });

    // Auto-save target change handler
    document.getElementById('taskModalAutoSaveTarget')?.addEventListener('change', () => {
      this.updateAutoSaveTargetFields();
    });

    // Escape key handler (document-level for reliable closing)
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        const modal = document.getElementById('taskModal');
        if (modal && modal.style.display !== 'none' && modal.style.display !== '') {
          this.close();
        }
      }
    });

    // Browse button handler
    document.getElementById('taskModalBrowseBtn')?.addEventListener('click', () => {
      this.handleBrowseClick();
    });

    // Mode toggle handlers
    document.getElementById('taskConfigModeManual')?.addEventListener('change', (e) => {
      if (e.target.checked) {
        this.handleModeChange('manual');
      }
    });

    document.getElementById('taskConfigModeAuto')?.addEventListener('change', async (e) => {
      if (e.target.checked) {
        await this.checkLlmAvailability();
        this.handleModeChange('auto');
      }
    });

    // Workspace selector handlers
    document.getElementById('taskModalChangeWorkspace')?.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.showWorkspaceSelector();
    });

    document.getElementById('taskModalWorkspaceSelect')?.addEventListener('change', async (e) => {
      const newWorkspaceId = e.target.value;
      if (newWorkspaceId) {
        this.workspaceId = newWorkspaceId;
        this.showWorkspaceBadge(newWorkspaceId);
        // Re-populate agent dropdown for new workspace
        await this.populateAgentDropdown(newWorkspaceId);
        this.refreshSubtaskAssignmentOptions();
        await this.loadWorkspaceTasks();
        this.populateMainInputTasks(this.currentTask);
        this.refreshSubtaskInputOptions();
      }
    });

    // Fetch system paths
    this.fetchSystemPaths();

    // File attachment handlers
    this.bindFileEvents();
    // Subtask handlers
    this.bindSubtaskEvents();

    this.initialized = true;
  }

  /**
   * Check LLM availability for auto mode
   */
  async checkLlmAvailability() {
    try {
      const response = await fetch('/api/agents/auto-config/availability');
      const data = await response.json();
      this.llmAvailable = data.available;
      this.systemModelConfigured = data.system_model_configured || false;
      return data;
    } catch (error) {
      console.error('Failed to check LLM availability:', error);
      this.llmAvailable = false;
      this.systemModelConfigured = false;
      return { available: false, system_model_configured: false };
    }
  }

  /**
   * Handle mode toggle change
   */
  handleModeChange(mode) {
    const manualSection = document.getElementById('taskManualSection');
    const autoSection = document.getElementById('taskAutoSection');
    const autoCreateSection = document.getElementById('taskAutoCreateSection');
    const autoEditSection = document.getElementById('taskAutoEditSection');
    const llmWarning = document.getElementById('taskLlmNotAvailableWarning');
    const llmWarningMessage = document.getElementById('taskLlmWarningMessage');
    const saveButtonText = document.getElementById('taskModalSaveText');

    this.autoMode = (mode === 'auto');

    if (mode === 'auto') {
      if (this.llmAvailable) {
        if (manualSection) manualSection.style.display = 'none';
        if (autoSection) autoSection.style.display = 'block';
        if (llmWarning) llmWarning.style.display = 'none';

        // Show appropriate auto subsection based on edit vs create
        if (this.editingTaskId) {
          if (autoCreateSection) autoCreateSection.style.display = 'none';
          if (autoEditSection) autoEditSection.style.display = 'block';
          if (saveButtonText) saveButtonText.textContent = 'Update Task';
        } else {
          if (autoCreateSection) autoCreateSection.style.display = 'block';
          if (autoEditSection) autoEditSection.style.display = 'none';
          if (saveButtonText) saveButtonText.textContent = 'Create Task';
        }
      } else {
        // LLM not available - show warning
        if (manualSection) manualSection.style.display = 'none';
        if (autoSection) autoSection.style.display = 'none';
        if (llmWarning) llmWarning.style.display = 'block';
        if (saveButtonText) saveButtonText.textContent = 'Go to Settings';
        if (llmWarningMessage) {
          if (!this.systemModelConfigured) {
            llmWarningMessage.textContent = 'Auto mode requires a System Model to be configured.';
          } else {
            llmWarningMessage.textContent = 'Auto mode requires an LLM provider. Please set up an API key or install Ollama.';
          }
        }
      }
    } else {
      // Manual mode
      if (manualSection) manualSection.style.display = 'block';
      if (autoSection) autoSection.style.display = 'none';
      if (llmWarning) llmWarning.style.display = 'none';
      if (saveButtonText) saveButtonText.textContent = this.editingTaskId ? 'Save Task' : 'Save Task';
    }
  }

  /**
   * Fetch system paths including default output directory
   */
  async fetchSystemPaths() {
    try {
      const response = await fetch('/api/settings/system-paths');
      if (response.ok) {
        const data = await response.json();
        this.defaultOutputDir = data.default_output_dir;
        this.homeDir = data.home_dir;

        // Update the display (will be overwritten by workspace-specific path if available)
        this.updateOutputPathDisplay(this.defaultOutputDir);
      }
    } catch (err) {
      console.error('Failed to fetch system paths:', err);
    }
  }

  /**
   * Fetch workspace-specific output path
   */
  async fetchWorkspaceOutputPath(workspaceId) {
    if (!workspaceId) {
      // No workspace - show global default
      this.updateOutputPathDisplay(this.defaultOutputDir || 'outputs/');
      return;
    }

    try {
      const response = await fetch(`/api/workspaces/${workspaceId}`);
      if (response.ok) {
        const workspace = await response.json();
        // Construct the workspace output path
        const workspaceName = workspace.name || 'workspace';
        const outputPath = workspace.output_dir || `workspaces/${workspaceName}/outputs/`;
        this.updateOutputPathDisplay(outputPath);
      } else {
        this.updateOutputPathDisplay(this.defaultOutputDir || 'outputs/');
      }
    } catch (err) {
      console.error('Failed to fetch workspace output path:', err);
      this.updateOutputPathDisplay(this.defaultOutputDir || 'outputs/');
    }
  }

  /**
   * Update the output path display element
   */
  updateOutputPathDisplay(path) {
    const pathDisplay = document.getElementById('taskModalDefaultOutputPath');
    if (pathDisplay) {
      pathDisplay.textContent = path || 'outputs/';
    }
  }

  /**
   * Handle browse button click - opens folder picker
   */
  async handleBrowseClick() {
    if (!this.workspaceId) {
      this.showToast('Please select a workspace first', 'warning');
      return;
    }

    const btn = document.getElementById('taskModalBrowseBtn');
    const originalHtml = btn?.innerHTML;
    if (btn) {
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
    }

    try {
      const response = await fetch('/api/launch-folder-picker', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workspace_id: this.workspaceId })
      });
      const result = await response.json();

      if (result.success) {
        this.showToast('Folder picker opened. Select a folder to add it as a directory reference.', 'info');
      } else {
        this.showToast(result.error || 'Failed to open folder picker', 'error');
      }
    } catch (error) {
      console.error('Failed to open folder picker:', error);
      this.showToast('Failed to open folder picker', 'error');
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.innerHTML = originalHtml;
      }
    }
  }

  /**
   * Show workspace badge with workspace name
   * @param {string} workspaceId - The workspace ID to display
   */
  async showWorkspaceBadge(workspaceId) {
    const badge = document.getElementById('taskModalWorkspaceBadge');
    const nameSpan = document.getElementById('taskModalWorkspaceName');
    const selector = document.getElementById('taskModalWorkspaceSelector');

    if (!badge || !nameSpan) return;

    // Hide selector when showing badge
    if (selector) selector.style.display = 'none';

    if (!workspaceId) {
      badge.style.display = 'none';
      return;
    }

    try {
      // Fetch workspace details
      const response = await fetch(`/api/workspaces/${workspaceId}`);
      if (response.ok) {
        const workspace = await response.json();
        nameSpan.textContent = workspace.name || 'Unknown Workspace';
        badge.style.display = 'block';
      } else {
        // Fallback: try to get from sessionManager if available
        if (window.sessionManager?.folders) {
          const folder = window.sessionManager.folders.find(f => f.id === workspaceId);
          if (folder) {
            nameSpan.textContent = folder.name;
            badge.style.display = 'block';
            return;
          }
        }
        badge.style.display = 'none';
      }
    } catch (err) {
      console.error('Failed to fetch workspace name:', err);
      // Fallback: try to get from sessionManager if available
      if (window.sessionManager?.folders) {
        const folder = window.sessionManager.folders.find(f => f.id === workspaceId);
        if (folder) {
          nameSpan.textContent = folder.name;
          badge.style.display = 'block';
          return;
        }
      }
      badge.style.display = 'none';
    }
  }

  /**
   * Show workspace selector dropdown
   */
  showWorkspaceSelector() {
    const badge = document.getElementById('taskModalWorkspaceBadge');
    const selector = document.getElementById('taskModalWorkspaceSelector');

    if (badge) badge.style.display = 'none';
    if (selector) selector.style.display = 'block';

    // Populate the dropdown
    this.populateWorkspaceDropdown();
  }

  /**
   * Populate workspace dropdown with available workspaces
   */
  async populateWorkspaceDropdown() {
    const select = document.getElementById('taskModalWorkspaceSelect');
    if (!select) return;

    // Start with default option
    let options = '<option value="">-- Select a workspace --</option>';

    try {
      // Fetch all workspaces
      const response = await fetch('/api/workspaces');
      if (response.ok) {
        const data = await response.json();
        const workspaces = data.folders || data.workspaces || [];

        workspaces.forEach(ws => {
          const selected = ws.id === this.workspaceId ? 'selected' : '';
          options += `<option value="${ws.id}" ${selected}>${ws.name || 'Unnamed Workspace'}</option>`;
        });
      }
    } catch (err) {
      console.error('Failed to load workspaces:', err);
    }

    // Fallback: try to get from sessionManager
    if (options === '<option value="">-- Select a workspace --</option>' && window.sessionManager?.folders) {
      window.sessionManager.folders.forEach(folder => {
        const selected = folder.id === this.workspaceId ? 'selected' : '';
        options += `<option value="${folder.id}" ${selected}>${folder.name || 'Unnamed Workspace'}</option>`;
      });
    }

    select.innerHTML = options;
  }

  /**
   * Open the modal for creating a new task
   * @param {string} workspaceId - The workspace ID to create the task in (optional - if not provided, shows selector)
   * @param {string} prefillTitle - Optional title to prefill
   * @param {function} onSave - Optional callback after successful save
   */
  async openForCreate(workspaceId = null, prefillTitle = '', onSave = null) {
    this.init();
    this.editingTaskId = null;
    this.workspaceId = workspaceId;
    this.onSaveCallback = onSave;
    this.currentTask = null;

    const modal = document.getElementById('taskModal');
    if (!modal) return;

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      modalTitle.textContent = 'Create Task';
    }

    // Show workspace badge or selector based on whether workspaceId is provided
    if (workspaceId) {
      await this.showWorkspaceBadge(workspaceId);
    } else {
      // No workspace pre-selected - show the selector
      this.showWorkspaceSelector();
    }

    // Check LLM availability to determine default mode
    await this.checkLlmAvailability();

    // Default to Auto mode if System Model is configured, otherwise Manual
    const manualRadio = document.getElementById('taskConfigModeManual');
    const autoRadio = document.getElementById('taskConfigModeAuto');
    if (this.systemModelConfigured && this.llmAvailable) {
      this.autoMode = true;
      if (autoRadio) autoRadio.checked = true;
      if (manualRadio) manualRadio.checked = false;
      this.handleModeChange('auto');
    } else {
      this.autoMode = false;
      if (manualRadio) manualRadio.checked = true;
      if (autoRadio) autoRadio.checked = false;
      this.handleModeChange('manual');
    }

    // Clear auto description textarea
    const autoDescription = document.getElementById('taskAutoDescription');
    if (autoDescription) autoDescription.value = '';

    // Clear/prefill form
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    const priorityInput = document.getElementById('taskModalPriority');
    const assignmentInput = document.getElementById('taskModalAssignment');

    if (descriptionInput) descriptionInput.value = prefillTitle;
    if (detailsInput) detailsInput.value = '';
    if (priorityInput) priorityInput.value = '3';

    // Populate agent assignment dropdown
    await this.populateAgentDropdown(workspaceId);
    this.refreshSubtaskAssignmentOptions();
    await this.loadWorkspaceTasks();
    this.populateMainInputTasks();
    this.refreshSubtaskInputOptions();
    if (assignmentInput) assignmentInput.value = '';

    // Reset subtasks
    this.resetSubtasks();

    // Reset schedule fields
    this.resetScheduleFields();

    // Reset auto-save fields
    this.resetAutoSaveFields();

    // Fetch workspace-specific output path
    await this.fetchWorkspaceOutputPath(workspaceId);

    // Reset file attachments
    this.resetFiles();

    // Show modal
    modal.style.display = 'flex';

    // Focus title input
    setTimeout(() => {
      descriptionInput?.focus();
    }, 100);
  }

  /**
   * Open the modal for editing an existing task
   * @param {object} task - The task object to edit
   * @param {function} onSave - Optional callback after successful save
   */
  async openForEdit(task, onSave = null) {
    this.init();
    this.editingTaskId = task.id;
    this.workspaceId = task.workspace_id || task.studio_id;
    this.onSaveCallback = onSave;
    this.currentTask = task; // Store for auto edit mode

    const modal = document.getElementById('taskModal');
    if (!modal) return;

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      modalTitle.textContent = 'Edit Task';
    }

    // Show workspace badge
    await this.showWorkspaceBadge(this.workspaceId);

    // Check LLM availability
    await this.checkLlmAvailability();

    // Default to Manual mode for editing (user can switch to Auto)
    this.autoMode = false;
    const manualRadio = document.getElementById('taskConfigModeManual');
    const autoRadio = document.getElementById('taskConfigModeAuto');
    if (manualRadio) manualRadio.checked = true;
    if (autoRadio) autoRadio.checked = false;
    this.handleModeChange('manual');

    // Populate auto edit section with current task details
    this.populateAutoEditSection(task);

    // Clear auto edit description
    const autoEditDescription = document.getElementById('taskAutoEditDescription');
    if (autoEditDescription) autoEditDescription.value = '';

    // Populate agent assignment dropdown first
    await this.populateAgentDropdown(this.workspaceId, task);
    this.refreshSubtaskAssignmentOptions();
    await this.loadWorkspaceTasks();

    // Reset subtasks
    this.resetSubtasks();
    const isSubtask = Boolean(task.parent_task_id && String(task.parent_task_id).trim() !== '');
    if (isSubtask) {
      this.setSubtaskSectionDisabled(true, 'This task is already part of a workflow. Subtasks can only be added to top-level tasks.');
    }

    // Set priority
    const priorityInput = document.getElementById('taskModalPriority');
    if (priorityInput) {
      priorityInput.value = task.priority || '3';
    }

    // Populate form fields
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    if (descriptionInput) descriptionInput.value = task.description || '';
    if (detailsInput) detailsInput.value = task.details || '';

    let loadedSubtasks = [];
    if (!isSubtask) {
      loadedSubtasks = await this.loadSubtasks(task.id);
      this.loadedSubtasks = loadedSubtasks;
      loadedSubtasks.forEach((subtask) => {
        const inputRefs = this.mapInputTaskIdsToRefs(subtask.input_task_ids || [], loadedSubtasks);
        const inputCount = inputRefs.length;
        this.addSubtaskRow({
          id: subtask.id,
          description: subtask.description || '',
          details: subtask.details || '',
          assignmentValue: this.getAssignmentValueFromTask(subtask),
          inputCount: inputCount,
          inputTaskIds: inputRefs
        });
      });
    }

    // Populate schedule and auto-save fields
    if (!isSubtask && loadedSubtasks.length > 0) {
      this.populateScheduleFields(loadedSubtasks[0]);
      this.populateAutoSaveFields(loadedSubtasks[loadedSubtasks.length - 1]);
    } else {
      this.populateScheduleFields(task);
      this.populateAutoSaveFields(task);
    }

    this.populateMainInputTasks(task);
    this.refreshSubtaskInputOptions();

    // Fetch workspace-specific output path
    await this.fetchWorkspaceOutputPath(this.workspaceId);

    // Reset file attachments (for edit mode, we start fresh)
    this.resetFiles();

    // Show modal
    modal.style.display = 'flex';

    // Focus title input
    setTimeout(() => {
      descriptionInput?.focus();
    }, 100);
  }

  /**
   * Populate the auto edit section with current task details
   */
  populateAutoEditSection(task) {
    const titleEl = document.getElementById('taskAutoEditCurrentTitle');
    const detailsEl = document.getElementById('taskAutoEditCurrentDetails');
    const metaEl = document.getElementById('taskAutoEditCurrentMeta');

    if (titleEl) {
      titleEl.textContent = task.description || 'Untitled Task';
    }

    if (detailsEl) {
      detailsEl.textContent = task.details || '(No details)';
      detailsEl.style.display = task.details ? 'block' : 'none';
    }

    if (metaEl) {
      const metaParts = [];

      // Priority
      const priorityLabels = { 1: 'Highest', 2: 'High', 3: 'Medium', 4: 'Low', 5: 'Lowest' };
      if (task.priority) {
        metaParts.push(`Priority: ${priorityLabels[task.priority] || task.priority}`);
      }

      // Agent assignment
      if (task.to) {
        metaParts.push(`Agent: ${task.to}`);
      }

      // Schedule
      if (task.schedule_enabled && task.schedule) {
        const scheduleType = task.schedule.type || 'unknown';
        metaParts.push(`Schedule: ${scheduleType}`);
      }

      metaEl.textContent = metaParts.join(' • ');
      metaEl.style.display = metaParts.length > 0 ? 'block' : 'none';
    }
  }

  /**
   * Close the modal
   */
  close() {
    const modal = document.getElementById('taskModal');
    if (modal) {
      modal.style.display = 'none';
    }
    this.hideProgress();
    this.editingTaskId = null;
    this.workspaceId = null;
    this.onSaveCallback = null;
    this.currentTask = null;
  }

  getProgressElements() {
    return {
      container: document.getElementById('taskModalProgress'),
      headline: document.getElementById('taskModalProgressHeadline'),
      message: document.getElementById('taskModalProgressMessage'),
      steps: document.getElementById('taskModalProgressSteps')
    };
  }

  updateProgress(step, { headline, message } = {}) {
    const elements = this.getProgressElements();
    if (!elements.container) return;

    if (elements.headline && headline) {
      elements.headline.textContent = headline;
    }
    if (elements.message && message) {
      elements.message.textContent = message;
    }

    if (!elements.steps) return;
    const stepIndex = this.progressSteps.indexOf(step);
    const items = Array.from(elements.steps.querySelectorAll('li'));
    items.forEach((item) => {
      const itemStep = item.dataset.step;
      const itemIndex = this.progressSteps.indexOf(itemStep);
      item.classList.remove('is-active', 'is-complete');
      if (itemIndex === -1 || stepIndex === -1) return;
      if (itemIndex < stepIndex) item.classList.add('is-complete');
      if (itemIndex === stepIndex) item.classList.add('is-active');
    });
  }

  showProgress(step, { headline, message } = {}) {
    const elements = this.getProgressElements();
    if (!elements.container) return;
    elements.container.hidden = false;
    this.updateProgress(step, { headline, message });
  }

  hideProgress() {
    const elements = this.getProgressElements();
    if (!elements.container) return;
    elements.container.hidden = true;
  }

  /**
   * Save the task (create or update)
   */
  async save() {
    // Handle auto mode - redirect to settings if LLM not available
    if (this.autoMode && !this.llmAvailable) {
      window.location.href = '/settings';
      return;
    }

    // Handle auto mode - parse natural language description (create)
    if (this.autoMode && !this.editingTaskId) {
      await this.saveAutoMode();
      return;
    }

    // Handle auto mode - edit with natural language
    if (this.autoMode && this.editingTaskId) {
      await this.saveAutoEditMode();
      return;
    }

    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    const priorityInput = document.getElementById('taskModalPriority');
    const assignmentInput = document.getElementById('taskModalAssignment');

    const description = descriptionInput?.value?.trim();
    const details = detailsInput?.value?.trim() || '';
    const priority = parseInt(priorityInput?.value || '3', 10);
    const assignment = assignmentInput?.value || '';

    if (!description) {
      this.showToast('Task title is required', 'error');
      descriptionInput?.focus();
      return;
    }

    if (!this.workspaceId) {
      this.showToast('Please select a workspace for this task', 'error');
      // Show the workspace selector if it's hidden
      this.showWorkspaceSelector();
      return;
    }

    const assignmentData = this.parseAssignmentValue(assignment);
    const to = assignmentData.to || '';
    const assignedNodeId = assignmentData.assignedNodeId || '';

    const mainInputRefs = this.getSelectedInputRefs(document.getElementById('taskModalInputTasks'));
    const mainInputIds = this.resolveInputRefs(mainInputRefs);

    const { subtasks, invalidInput } = this.collectSubtasks();
    if (invalidInput) {
      this.showToast('Subtask title is required', 'error');
      invalidInput.focus();
      return;
    }

    const canManageSubtasks = !(this.currentTask?.parent_task_id && String(this.currentTask.parent_task_id).trim() !== '');
    if (!canManageSubtasks && subtasks.length > 0) {
      this.showToast('Subtasks can only be added to top-level tasks', 'error');
      return;
    }

    try {
      // Get schedule data
      const scheduleData = this.getScheduleData();

      // Get auto-save data
      const autoSaveData = this.getAutoSaveData();

      const createTask = async (payload) => {
        const response = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        });

        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || 'Failed to create task');
        }

        const createdPayload = await response.json();
        return this.extractTaskFromResponse(createdPayload);
      };

      const updateTask = async (taskId, payload) => {
        const response = await fetch(`/api/orchestration/tasks/${taskId}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            task_id: taskId,
            ...payload
          })
        });

        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || 'Failed to update task');
        }
      };

      const deleteTask = async (taskId) => {
        if (!taskId) return;
        const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}&studio_id=${encodeURIComponent(this.workspaceId)}`, {
          method: 'DELETE'
        });
        if (!response.ok) {
          const errText = await response.text();
          throw new Error(errText || 'Failed to delete subtask');
        }
      };

      const hasExistingSubtasks = Array.isArray(this.loadedSubtasks) && this.loadedSubtasks.length > 0;
      const hasSubtaskDeletes = this.subtasksToDelete && this.subtasksToDelete.size > 0;
      const isWorkflow = canManageSubtasks && (subtasks.length > 0 || hasExistingSubtasks || hasSubtaskDeletes);

      if (subtasks.length > 0 && scheduleData.schedule_enabled) {
        const firstSubtask = subtasks[0];
        if (!firstSubtask?.to || firstSubtask.to === 'unassigned') {
          this.showToast('Assign an agent to the first subtask before enabling a schedule', 'error');
          return;
        }
      }

      if (this.editingTaskId) {
        if (isWorkflow && subtasks.length === 0) {
          for (const subtaskId of this.subtasksToDelete) {
            await deleteTask(subtaskId);
          }

          await updateTask(this.editingTaskId, {
            description,
            details,
            to: to || undefined,
            assigned_node_id: assignedNodeId || undefined,
            input_task_ids: mainInputIds,
            ...scheduleData,
            ...autoSaveData
          });

          if (this.pendingFiles.length > 0) {
            await this.uploadFilesToTask(this.editingTaskId);
          }
          if (this.pendingFilePaths.length > 0) {
            await this.saveFilePathsToTask(this.editingTaskId);
          }

          this.showToast('Task updated', 'success');
        } else if (isWorkflow) {
          await updateTask(this.editingTaskId, {
            description,
            details,
            to: to || undefined,
            assigned_node_id: assignedNodeId || undefined,
            input_task_ids: [],
            schedule: null,
            schedule_enabled: false,
            schedule_name: '',
            result_storage: null
          });

          const totalSubtasks = subtasks.length;
          let lastSubtaskId = totalSubtasks > 0 ? (subtasks[totalSubtasks - 1].id || '') : '';
          const stepIdsByIndex = subtasks.map((subtask) => subtask.id || '');

          for (let i = 0; i < totalSubtasks; i++) {
            const subtask = subtasks[i];
            const inputTaskIds = this.resolveInputRefs(subtask.input_task_ids, stepIdsByIndex);
            if (subtask.id) {
              await updateTask(subtask.id, {
                description: subtask.description,
                details: subtask.details,
                to: subtask.to || undefined,
                assigned_node_id: subtask.assigned_node_id || undefined,
                input_task_ids: inputTaskIds,
                parent_task_id: this.editingTaskId,
                subtask_index: i + 1,
                ...this.getWorkflowSchedulePayload(scheduleData, i, totalSubtasks, true),
                ...this.getWorkflowAutoSavePayload(autoSaveData, i, totalSubtasks, true)
              });
            } else {
              const createdSubtask = await createTask({
                studio_id: this.workspaceId,
                description: subtask.description,
                details: subtask.details,
                priority,
                to: subtask.to || undefined,
                assigned_node_id: subtask.assigned_node_id || undefined,
                input_task_ids: inputTaskIds,
                parent_task_id: this.editingTaskId,
                subtask_index: i + 1,
                ...this.getWorkflowSchedulePayload(scheduleData, i, totalSubtasks, false),
                ...this.getWorkflowAutoSavePayload(autoSaveData, i, totalSubtasks, false)
              });
              if (createdSubtask?.id && i === totalSubtasks - 1) {
                lastSubtaskId = createdSubtask.id;
              }
              if (createdSubtask?.id) {
                stepIdsByIndex[i] = createdSubtask.id;
              }
            }
          }

          for (const subtaskId of this.subtasksToDelete) {
            await deleteTask(subtaskId);
          }

          const attachmentTarget = lastSubtaskId || this.editingTaskId;
          if (this.pendingFiles.length > 0 && attachmentTarget) {
            await this.uploadFilesToTask(attachmentTarget);
          }
          if (this.pendingFilePaths.length > 0 && attachmentTarget) {
            await this.saveFilePathsToTask(attachmentTarget);
          }

          this.showToast('Workflow updated', 'success');
        } else {
          await updateTask(this.editingTaskId, {
            description,
            details,
            to: to || undefined,
            assigned_node_id: assignedNodeId || undefined,
            input_task_ids: mainInputIds,
            ...scheduleData,
            ...autoSaveData
          });

          // Upload files to existing task
          if (this.pendingFiles.length > 0) {
            await this.uploadFilesToTask(this.editingTaskId);
          }
          if (this.pendingFilePaths.length > 0) {
            await this.saveFilePathsToTask(this.editingTaskId);
          }

          this.showToast('Task updated', 'success');
        }
      } else if (subtasks.length > 0) {
        const parentTask = await createTask({
          studio_id: this.workspaceId,
          description,
          details,
          priority,
          to: to || undefined,
          assigned_node_id: assignedNodeId || undefined
        });

        if (!parentTask?.id) {
          throw new Error('Parent task created but ID is missing');
        }

        let lastSubtaskId = '';
        const stepIdsByIndex = subtasks.map(() => '');
        for (let i = 0; i < subtasks.length; i++) {
          const subtask = subtasks[i];
          const inputTaskIds = this.resolveInputRefs(subtask.input_task_ids, stepIdsByIndex);
          const createdSubtask = await createTask({
            studio_id: this.workspaceId,
            description: subtask.description,
            details: subtask.details,
            priority,
            to: subtask.to || undefined,
            assigned_node_id: subtask.assigned_node_id || undefined,
            input_task_ids: inputTaskIds,
            parent_task_id: parentTask.id,
            subtask_index: i + 1,
            ...this.getWorkflowSchedulePayload(scheduleData, i, subtasks.length, false),
            ...this.getWorkflowAutoSavePayload(autoSaveData, i, subtasks.length, false)
          });
          if (createdSubtask?.id) {
            lastSubtaskId = createdSubtask.id;
            stepIdsByIndex[i] = createdSubtask.id;
          }
        }

        if (lastSubtaskId && this.pendingFiles.length > 0) {
          await this.uploadFilesToTask(lastSubtaskId);
        }
        if (lastSubtaskId && this.pendingFilePaths.length > 0) {
          await this.saveFilePathsToTask(lastSubtaskId);
        }

        this.showToast('Workflow created', 'success');
      } else {
        const createdTask = await createTask({
          studio_id: this.workspaceId,
          description,
          details,
          priority,
          to: to || undefined,
          assigned_node_id: assignedNodeId || undefined,
          input_task_ids: mainInputIds,
          ...scheduleData,
          ...autoSaveData
        });

        if (createdTask?.id && this.pendingFiles.length > 0) {
          await this.uploadFilesToTask(createdTask.id);
        }
        if (createdTask?.id && this.pendingFilePaths.length > 0) {
          await this.saveFilePathsToTask(createdTask.id);
        }

        this.showToast('Task created', 'success');
      }

      // Clear pending files
      this.pendingFiles = [];
      this.pendingFilePaths = [];

      this.close();

      // Emit event for workspace hub to refresh
      if (window.EventBus) {
        EventBus.emit('task:created', { workspaceId: this.workspaceId });
      }

      // Call the callback if provided
      if (this.onSaveCallback) {
        this.onSaveCallback();
      }
    } catch (error) {
      console.error('Failed to save task:', error);
      this.showToast(error.message || 'Failed to save task', 'error');
    }
  }

  /**
   * Save task in auto mode - parse natural language and create task
   */
  async saveAutoMode() {
    const autoDescriptionInput = document.getElementById('taskAutoDescription');
    const description = autoDescriptionInput?.value?.trim();

    if (!description) {
      this.showToast('Please describe the task you want to create', 'error');
      autoDescriptionInput?.focus();
      return;
    }

    if (!this.workspaceId) {
      this.showToast('Please select a workspace for this task', 'error');
      // Show the workspace selector if it's hidden
      this.showWorkspaceSelector();
      return;
    }

    this.showProgress('parse', {
      headline: 'Parsing request',
      message: 'Analyzing your task description.'
    });

    // Show loading state
    const saveButton = document.getElementById('taskModalSave');
    const saveText = document.getElementById('taskModalSaveText');
    const originalText = saveText?.textContent;
    if (saveButton) saveButton.disabled = true;
    if (saveText) saveText.textContent = 'Parsing...';

    try {
      // Call auto-parse endpoint
      const parseResponse = await fetch('/api/orchestration/tasks/auto-parse', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          description: description,
          workspace_id: this.workspaceId
        })
      });

      if (!parseResponse.ok) {
        const errText = await parseResponse.text();
        throw new Error(errText || 'Failed to parse task description');
      }

      const parsed = await parseResponse.json();

      this.updateProgress('prepare', {
        headline: 'Planning tasks',
        message: 'Preparing the task details.'
      });

      // Build schedule data from parsed response
      let scheduleData = { schedule_enabled: false };
      if (parsed.schedule_enabled && parsed.schedule) {
        // Convert LLM response field names to backend expected names
        const schedule = { ...parsed.schedule };

        // Convert once_at to run_at (backend expects run_at for "once" type)
        if (schedule.once_at && !schedule.run_at) {
          schedule.run_at = schedule.once_at;
          delete schedule.once_at;
        }

        scheduleData = {
          schedule: schedule,
          schedule_enabled: true,
          schedule_name: parsed.schedule_name || ''
        };
      }

      // Build result storage data from parsed response
      let resultStorageData = {};
      if (parsed.result_storage && parsed.result_storage.enabled) {
        resultStorageData = {
          result_storage: {
            enabled: true,
            format: parsed.result_storage.format || 'text',
            store_node_id: parsed.result_storage.store_node_id || undefined,
            file_path: parsed.result_storage.file_path || undefined
          }
        };
      }

      const workflowSteps = Array.isArray(parsed.tasks) ? parsed.tasks.filter(Boolean) : [];
      if (workflowSteps.length > 0) {
        this.updateProgress('apply', {
          headline: 'Creating tasks',
          message: 'Saving the workflow to your workspace.'
        });
        const parentTitle = parsed.title || workflowSteps[0]?.title || 'New Workflow';
        const parentDetails = parsed.details || '';
        const parentPriority = parsed.priority || 3;

        const parentResponse = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            studio_id: this.workspaceId,
            description: parentTitle,
            details: parentDetails,
            priority: parentPriority
          })
        });

        if (!parentResponse.ok) {
          const errText = await parentResponse.text();
          throw new Error(errText || 'Failed to create parent task');
        }

        const parentPayload = await parentResponse.json();
        const parentTask = this.extractTaskFromResponse(parentPayload);
        const parentTaskId = parentTask?.id;

        if (!parentTaskId) {
          throw new Error('Parent task created but ID is missing');
        }

        const stepIdToTaskId = new Map();
        let lastCreatedTaskId = '';

        for (let i = 0; i < workflowSteps.length; i++) {
          const step = workflowSteps[i] || {};
          const stepId = step.id || `step-${i + 1}`;
          const stepTitle = step.title || step.description || parsed.title || `Task ${i + 1}`;
          const stepDetails = step.details || '';
          const stepPriority = Number.isInteger(step.priority) ? step.priority : (parsed.priority || 3);

          let to = '';
          let assignedNodeId = '';
          if (step.agent_name) {
            assignedNodeId = `${step.agent_name}-node-1`;
            to = step.agent_name;
          }

          let dependsOn = Array.isArray(step.depends_on) ? step.depends_on : [];
          if (dependsOn.length === 0 && i > 0) {
            const fallbackId = workflowSteps[i - 1]?.id || `step-${i}`;
            dependsOn = [fallbackId];
          }
          const inputTaskIds = dependsOn.map((id) => stepIdToTaskId.get(id)).filter(Boolean);

          const stepScheduleData = i === 0 ? scheduleData : { schedule_enabled: false };
          const stepResultStorageData = i === workflowSteps.length - 1 ? resultStorageData : {};

          const createResponse = await fetch('/api/orchestration/tasks', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              studio_id: this.workspaceId,
              description: stepTitle,
              details: stepDetails,
              priority: stepPriority,
              to: to || undefined,
              assigned_node_id: assignedNodeId || undefined,
              input_task_ids: inputTaskIds,
              parent_task_id: parentTaskId,
              subtask_index: i + 1,
              ...stepScheduleData,
              ...stepResultStorageData
            })
          });

          if (!createResponse.ok) {
            const errText = await createResponse.text();
            throw new Error(errText || 'Failed to create task');
          }

          const createdPayload = await createResponse.json();
          const createdTask = this.extractTaskFromResponse(createdPayload);
          if (createdTask?.id) {
            stepIdToTaskId.set(stepId, createdTask.id);
            lastCreatedTaskId = createdTask.id;
          }
        }

        if (lastCreatedTaskId && this.pendingFiles.length > 0) {
          await this.uploadFilesToTask(lastCreatedTaskId);
        }
        if (lastCreatedTaskId && this.pendingFilePaths.length > 0) {
          await this.saveFilePathsToTask(lastCreatedTaskId);
        }

        // Clear pending files
        this.pendingFiles = [];
        this.pendingFilePaths = [];

        this.showToast(workflowSteps.length > 1 ? 'Workflow created' : 'Task created', 'success');
        this.close();

        // Emit event for workspace hub to refresh
        if (window.EventBus) {
          EventBus.emit('task:created', { workspaceId: this.workspaceId });
        }

        if (this.onSaveCallback) {
          this.onSaveCallback();
        }
        return;
      }

      // Build task data from parsed response
      this.updateProgress('apply', {
        headline: 'Creating task',
        message: 'Saving the task to your workspace.'
      });
      let to = '';
      let assignedNodeId = '';
      if (parsed.agent_name) {
        assignedNodeId = `${parsed.agent_name}-node-1`;
        to = parsed.agent_name;
      }

      // Create the task
      const createResponse = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          studio_id: this.workspaceId,
          description: parsed.title,
          details: parsed.details || '',
          priority: parsed.priority || 3,
          to: to || undefined,
          assigned_node_id: assignedNodeId || undefined,
          ...scheduleData,
          ...resultStorageData
        })
      });

      if (!createResponse.ok) {
        const errText = await createResponse.text();
        throw new Error(errText || 'Failed to create task');
      }

      // Upload files to newly created task
      const createdPayload = await createResponse.json();
      const createdTask = this.extractTaskFromResponse(createdPayload);
      if (createdTask?.id && this.pendingFiles.length > 0) {
        await this.uploadFilesToTask(createdTask.id);
      }
      if (createdTask?.id && this.pendingFilePaths.length > 0) {
        await this.saveFilePathsToTask(createdTask.id);
      }

      // Clear pending files
      this.pendingFiles = [];
      this.pendingFilePaths = [];

      this.showToast('Task created', 'success');
      this.close();

      // Emit event for workspace hub to refresh
      if (window.EventBus) {
        EventBus.emit('task:created', { task: createdTask, workspaceId: this.workspaceId });
      }

      // Call the callback if provided
      if (this.onSaveCallback) {
        this.onSaveCallback();
      }
    } catch (error) {
      console.error('Failed to save task in auto mode:', error);
      this.showToast(error.message || 'Failed to create task', 'error');
    } finally {
      this.hideProgress();
      // Restore button state
      if (saveButton) saveButton.disabled = false;
      if (saveText) saveText.textContent = originalText || 'Create Task';
    }
  }

  /**
   * Save task in auto edit mode - parse modification instructions and update task
   */
  async saveAutoEditMode() {
    const autoEditDescriptionInput = document.getElementById('taskAutoEditDescription');
    const modificationDescription = autoEditDescriptionInput?.value?.trim();

    if (!modificationDescription) {
      this.showToast('Please describe how you want to modify the task', 'error');
      autoEditDescriptionInput?.focus();
      return;
    }

    if (!this.currentTask || !this.editingTaskId) {
      this.showToast('No task selected for editing', 'error');
      return;
    }

    this.showProgress('parse', {
      headline: 'Parsing changes',
      message: 'Analyzing your update request.'
    });

    // Show loading state
    const saveButton = document.getElementById('taskModalSave');
    const saveText = document.getElementById('taskModalSaveText');
    const originalText = saveText?.textContent;
    if (saveButton) saveButton.disabled = true;
    if (saveText) saveText.textContent = 'Updating...';

    try {
      // Call auto-edit endpoint with current task and modification request
      const parseResponse = await fetch('/api/orchestration/tasks/auto-edit', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: this.editingTaskId,
          current_task: {
            description: this.currentTask.description,
            details: this.currentTask.details || '',
            priority: this.currentTask.priority || 3,
            to: this.currentTask.to || '',
            schedule_enabled: this.currentTask.schedule_enabled || false,
            schedule: this.currentTask.schedule || null,
            schedule_name: this.currentTask.schedule_name || '',
            result_storage: this.currentTask.result_storage || null
          },
          modification: modificationDescription,
          workspace_id: this.workspaceId
        })
      });

      if (!parseResponse.ok) {
        const errText = await parseResponse.text();
        throw new Error(errText || 'Failed to parse modification');
      }

      const parsed = await parseResponse.json();

      this.updateProgress('prepare', {
        headline: 'Planning updates',
        message: 'Reviewing the changes.'
      });

      // Build update data from parsed response
      let to = '';
      let assignedNodeId = '';
      if (parsed.agent_name) {
        assignedNodeId = `${parsed.agent_name}-node-1`;
        to = parsed.agent_name;
      }

      // Build schedule data
      let scheduleData = { schedule_enabled: false };
      if (parsed.schedule_enabled && parsed.schedule) {
        const schedule = { ...parsed.schedule };
        if (schedule.once_at && !schedule.run_at) {
          schedule.run_at = schedule.once_at;
          delete schedule.once_at;
        }
        scheduleData = {
          schedule: schedule,
          schedule_enabled: true,
          schedule_name: parsed.schedule_name || ''
        };
      }

      // Build result storage data
      let resultStorageData = {};
      if (parsed.result_storage && parsed.result_storage.enabled) {
        resultStorageData = {
          result_storage: {
            enabled: true,
            format: parsed.result_storage.format || 'text',
            store_node_id: parsed.result_storage.store_node_id || undefined,
            file_path: parsed.result_storage.file_path || undefined
          }
        };
      }

      this.updateProgress('apply', {
        headline: 'Updating task',
        message: 'Saving your changes.'
      });

      // Update the task
      const updateResponse = await fetch(`/api/orchestration/tasks/${this.editingTaskId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: this.editingTaskId,
          description: parsed.title || this.currentTask.description,
          details: parsed.details !== undefined ? parsed.details : (this.currentTask.details || ''),
          priority: parsed.priority || this.currentTask.priority || 3,
          to: to || this.currentTask.to || undefined,
          assigned_node_id: assignedNodeId || undefined,
          ...scheduleData,
          ...resultStorageData
        })
      });

      if (!updateResponse.ok) {
        const errText = await updateResponse.text();
        throw new Error(errText || 'Failed to update task');
      }

      // Upload files if any
      if (this.pendingFiles.length > 0) {
        await this.uploadFilesToTask(this.editingTaskId);
      }
      if (this.pendingFilePaths.length > 0) {
        await this.saveFilePathsToTask(this.editingTaskId);
      }

      // Clear pending files
      this.pendingFiles = [];
      this.pendingFilePaths = [];

      this.showToast('Task updated', 'success');
      this.close();

      // Emit event for workspace hub to refresh
      if (window.EventBus) {
        EventBus.emit('task:updated', { taskId: this.editingTaskId, workspaceId: this.workspaceId });
      }

      // Call the callback if provided
      if (this.onSaveCallback) {
        this.onSaveCallback();
      }
    } catch (error) {
      console.error('Failed to update task in auto mode:', error);
      this.showToast(error.message || 'Failed to update task', 'error');
    } finally {
      this.hideProgress();
      // Restore button state
      if (saveButton) saveButton.disabled = false;
      if (saveText) saveText.textContent = originalText || 'Update Task';
    }
  }

  /**
   * Update schedule type fields visibility
   */
  updateScheduleTypeFields() {
    const scheduleType = document.getElementById('taskModalScheduleType')?.value || 'interval';

    const timeField = document.getElementById('taskModalScheduleTimeField');
    const dayField = document.getElementById('taskModalScheduleDayField');
    const intervalField = document.getElementById('taskModalScheduleIntervalField');
    const onceField = document.getElementById('taskModalScheduleOnceField');

    // Hide all
    if (timeField) timeField.style.display = 'none';
    if (dayField) dayField.style.display = 'none';
    if (intervalField) intervalField.style.display = 'none';
    if (onceField) onceField.style.display = 'none';

    // Show relevant fields
    switch (scheduleType) {
      case 'daily':
        if (timeField) timeField.style.display = 'block';
        break;
      case 'weekly':
        if (timeField) timeField.style.display = 'block';
        if (dayField) dayField.style.display = 'block';
        break;
      case 'interval':
        if (intervalField) intervalField.style.display = 'block';
        break;
      case 'once':
        if (onceField) onceField.style.display = 'block';
        break;
    }
  }

  /**
   * Reset schedule fields to defaults
   */
  resetScheduleFields() {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    const scheduleFields = document.getElementById('taskModalScheduleFields');
    const scheduleName = document.getElementById('taskModalScheduleName');
    const scheduleType = document.getElementById('taskModalScheduleType');
    const scheduleTime = document.getElementById('taskModalScheduleTime');
    const scheduleDay = document.getElementById('taskModalScheduleDay');
    const scheduleIntervalValue = document.getElementById('taskModalScheduleIntervalValue');
    const scheduleIntervalUnit = document.getElementById('taskModalScheduleIntervalUnit');
    const scheduleDatetime = document.getElementById('taskModalScheduleDatetime');

    if (enabledCheckbox) enabledCheckbox.checked = false;
    if (scheduleFields) scheduleFields.style.display = 'none';
    if (scheduleName) scheduleName.value = '';
    if (scheduleType) scheduleType.value = 'interval';
    if (scheduleTime) scheduleTime.value = '09:00';
    if (scheduleDay) scheduleDay.value = '1';
    if (scheduleIntervalValue) scheduleIntervalValue.value = '1';
    if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
    if (scheduleDatetime) scheduleDatetime.value = '';

    this.updateScheduleTypeFields();
  }

  /**
   * Populate schedule fields from task
   */
  populateScheduleFields(task) {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    const scheduleFields = document.getElementById('taskModalScheduleFields');
    const scheduleName = document.getElementById('taskModalScheduleName');
    const scheduleType = document.getElementById('taskModalScheduleType');
    const scheduleTime = document.getElementById('taskModalScheduleTime');
    const scheduleDay = document.getElementById('taskModalScheduleDay');
    const scheduleIntervalValue = document.getElementById('taskModalScheduleIntervalValue');
    const scheduleIntervalUnit = document.getElementById('taskModalScheduleIntervalUnit');
    const scheduleDatetime = document.getElementById('taskModalScheduleDatetime');

    if (task.schedule_enabled && task.schedule) {
      if (enabledCheckbox) enabledCheckbox.checked = true;
      if (scheduleFields) scheduleFields.style.display = 'block';
      if (scheduleName) scheduleName.value = task.schedule_name || '';
      if (scheduleType) scheduleType.value = task.schedule.type || 'interval';

      // Populate type-specific fields
      const schedule = task.schedule;
      if (schedule.time && scheduleTime) scheduleTime.value = schedule.time;
      if (schedule.time_of_day && scheduleTime) scheduleTime.value = schedule.time_of_day;
      if (schedule.day_of_week != null && scheduleDay) scheduleDay.value = schedule.day_of_week;

      if (schedule.interval_minutes) {
        const minutes = schedule.interval_minutes;
        if (minutes >= 1440 && minutes % 1440 === 0) {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes / 1440;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'days';
        } else if (minutes >= 60 && minutes % 60 === 0) {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes / 60;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
        } else {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'minutes';
        }
      }

      if (schedule.run_at && scheduleDatetime) {
        const date = new Date(schedule.run_at);
        scheduleDatetime.value = date.toISOString().slice(0, 16);
      }
      if (schedule.execute_at && scheduleDatetime) {
        const date = new Date(schedule.execute_at);
        scheduleDatetime.value = date.toISOString().slice(0, 16);
      }

      this.updateScheduleTypeFields();
    } else {
      this.resetScheduleFields();
    }
  }

  /**
   * Get schedule data from modal fields
   */
  getScheduleData() {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    if (!enabledCheckbox?.checked) {
      return { schedule_enabled: false };
    }

    const scheduleType = document.getElementById('taskModalScheduleType')?.value || 'interval';
    const scheduleName = document.getElementById('taskModalScheduleName')?.value || '';

    const schedule = { type: scheduleType };

    switch (scheduleType) {
      case 'daily':
        schedule.time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
        break;
      case 'weekly':
        schedule.time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
        schedule.day_of_week = document.getElementById('taskModalScheduleDay')?.value || 'monday';
        break;
      case 'interval': {
        const intervalValue = parseInt(document.getElementById('taskModalScheduleIntervalValue')?.value || '1', 10);
        const intervalUnit = document.getElementById('taskModalScheduleIntervalUnit')?.value || 'hours';
        let intervalMinutes = intervalValue;
        if (intervalUnit === 'hours') {
          intervalMinutes = intervalValue * 60;
        } else if (intervalUnit === 'days') {
          intervalMinutes = intervalValue * 1440;
        }
        schedule.interval_minutes = intervalMinutes;
        break;
      }
      case 'once': {
        const datetime = document.getElementById('taskModalScheduleDatetime')?.value;
        if (datetime) {
          schedule.run_at = new Date(datetime).toISOString();
        }
        break;
      }
    }

    return {
      schedule,
      schedule_enabled: true,
      schedule_name: scheduleName
    };
  }

  /**
   * Update auto-save target fields visibility
   */
  updateAutoSaveTargetFields() {
    const target = document.getElementById('taskModalAutoSaveTarget')?.value || 'default';

    const storeNodeField = document.getElementById('taskModalAutoSaveStoreNodeField');
    const pathField = document.getElementById('taskModalAutoSavePathField');
    const defaultPathDisplay = document.getElementById('taskModalAutoSaveDefaultPathDisplay');

    // Hide all
    if (storeNodeField) storeNodeField.style.display = 'none';
    if (pathField) pathField.style.display = 'none';
    if (defaultPathDisplay) defaultPathDisplay.style.display = 'none';

    // Show relevant fields
    switch (target) {
      case 'store':
        if (storeNodeField) storeNodeField.style.display = 'block';
        break;
      case 'custom':
        if (pathField) pathField.style.display = 'block';
        break;
      case 'default':
        if (defaultPathDisplay) defaultPathDisplay.style.display = 'block';
        break;
    }
  }

  /**
   * Reset auto-save fields to defaults
   */
  resetAutoSaveFields() {
    const enabledCheckbox = document.getElementById('taskModalAutoSaveEnabled');
    const autoSaveFields = document.getElementById('taskModalAutoSaveFields');
    const targetSelect = document.getElementById('taskModalAutoSaveTarget');
    const storeNodeSelect = document.getElementById('taskModalAutoSaveStoreNode');
    const pathInput = document.getElementById('taskModalAutoSavePath');
    const formatSelect = document.getElementById('taskModalAutoSaveFormat');
    const defaultPathDisplay = document.getElementById('taskModalDefaultOutputPath');

    if (enabledCheckbox) enabledCheckbox.checked = false;
    if (autoSaveFields) autoSaveFields.style.display = 'none';
    if (targetSelect) targetSelect.value = 'default';
    if (storeNodeSelect) storeNodeSelect.innerHTML = '';
    if (pathInput) pathInput.value = '';
    if (formatSelect) formatSelect.value = 'text';

    // Update default path display
    if (defaultPathDisplay && this.defaultOutputDir) {
      defaultPathDisplay.textContent = this.defaultOutputDir;
    }

    this.updateAutoSaveTargetFields();
  }

  /**
   * Populate auto-save fields from task
   */
  populateAutoSaveFields(task) {
    const enabledCheckbox = document.getElementById('taskModalAutoSaveEnabled');
    const autoSaveFields = document.getElementById('taskModalAutoSaveFields');
    const targetSelect = document.getElementById('taskModalAutoSaveTarget');
    const pathInput = document.getElementById('taskModalAutoSavePath');
    const formatSelect = document.getElementById('taskModalAutoSaveFormat');

    if (task.result_storage?.enabled) {
      if (enabledCheckbox) enabledCheckbox.checked = true;
      if (autoSaveFields) autoSaveFields.style.display = 'block';

      // Determine target type
      if (task.result_storage.store_node_id) {
        if (targetSelect) targetSelect.value = 'store';
        // TODO: Populate and select store node
      } else if (task.result_storage.file_path) {
        if (targetSelect) targetSelect.value = 'custom';
        if (pathInput) pathInput.value = task.result_storage.file_path;
      } else {
        if (targetSelect) targetSelect.value = 'default';
      }

      if (formatSelect && task.result_storage.format) {
        formatSelect.value = task.result_storage.format;
      }

      this.updateAutoSaveTargetFields();
    } else {
      this.resetAutoSaveFields();
    }
  }

  /**
   * Get auto-save data from modal fields
   */
  getAutoSaveData() {
    const enabledCheckbox = document.getElementById('taskModalAutoSaveEnabled');
    if (!enabledCheckbox?.checked) {
      return { result_storage: null };
    }

    const target = document.getElementById('taskModalAutoSaveTarget')?.value || 'default';
    const format = document.getElementById('taskModalAutoSaveFormat')?.value || 'text';

    const resultStorage = {
      enabled: true,
      format: format
    };

    switch (target) {
      case 'store': {
        const storeNodeId = document.getElementById('taskModalAutoSaveStoreNode')?.value;
        if (storeNodeId) {
          resultStorage.store_node_id = storeNodeId;
        }
        break;
      }
      case 'custom': {
        const filePath = document.getElementById('taskModalAutoSavePath')?.value?.trim();
        if (filePath) {
          resultStorage.file_path = filePath;
        }
        break;
      }
      // 'default' uses workspace output folder (no additional config needed)
    }

    return { result_storage: resultStorage };
  }

  /**
   * Load tasks for the current workspace (used for input connections).
   */
  async loadWorkspaceTasks() {
    if (!this.workspaceId) {
      this.workspaceTasks = [];
      return [];
    }

    try {
      const response = await fetch(`/api/orchestration/tasks?studio_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) {
        throw new Error('Failed to load tasks');
      }
      const data = await response.json();
      this.workspaceTasks = data.tasks || [];
      return this.workspaceTasks;
    } catch (error) {
      console.error('Failed to load workspace tasks:', error);
      this.workspaceTasks = [];
      return [];
    }
  }

  /**
   * Format a task label for input selection.
   */
  formatTaskOptionLabel(task) {
    if (!task) return '';
    const title = task.description || task.name || task.id || 'Untitled Task';
    const idSuffix = task.id ? task.id.slice(0, 6) : '';
    return idSuffix ? `${title} (${idSuffix})` : title;
  }

  /**
   * Build workspace task options for input selection.
   */
  getWorkspaceTaskOptions({ excludeIds = new Set() } = {}) {
    const tasks = Array.isArray(this.workspaceTasks) ? this.workspaceTasks : [];
    return tasks
      .filter((task) => task && task.id && !excludeIds.has(task.id))
      .map((task) => ({
        value: `task:${task.id}`,
        label: this.formatTaskOptionLabel(task)
      }));
  }

  /**
   * Resolve input references to task IDs.
   */
  resolveInputRefs(inputRefs, stepIdsByIndex = []) {
    const resolved = [];
    const seen = new Set();

    (inputRefs || []).forEach((ref) => {
      if (!ref) return;
      let taskId = '';
      if (ref.startsWith('step:')) {
        const index = parseInt(ref.slice('step:'.length), 10) - 1;
        taskId = stepIdsByIndex[index] || '';
      } else if (ref.startsWith('task:')) {
        taskId = ref.slice('task:'.length);
      } else {
        taskId = ref;
      }
      if (taskId && !seen.has(taskId)) {
        seen.add(taskId);
        resolved.push(taskId);
      }
    });

    return resolved;
  }

  /**
   * Map stored input task IDs to input reference values.
   */
  mapInputTaskIdsToRefs(inputTaskIds, workflowSubtasks = []) {
    const refs = [];
    if (!Array.isArray(inputTaskIds) || inputTaskIds.length === 0) {
      return refs;
    }

    const idToStep = new Map();
    workflowSubtasks.forEach((subtask, index) => {
      if (subtask?.id) {
        idToStep.set(subtask.id, index + 1);
      }
    });

    inputTaskIds.forEach((taskId) => {
      const stepIndex = idToStep.get(taskId);
      if (stepIndex) {
        refs.push(`step:${stepIndex}`);
      } else if (taskId) {
        refs.push(`task:${taskId}`);
      }
    });

    return refs;
  }

  /**
   * Get selected input references from a select element.
   */
  getSelectedInputRefs(selectEl) {
    if (!selectEl) return [];
    return Array.from(selectEl.selectedOptions)
      .map((option) => option.value)
      .filter((value) => value && value.trim() !== '');
  }

  /**
   * Enable click-to-toggle behavior for multi-select inputs.
   */
  bindMultiSelectToggle(selectEl) {
    if (!selectEl || selectEl.dataset.toggleBound === 'true') return;
    selectEl.dataset.toggleBound = 'true';

    selectEl.addEventListener('mousedown', (event) => {
      if (selectEl.disabled) return;
      const option = event.target;
      if (!option || option.tagName !== 'OPTION' || option.disabled) return;
      event.preventDefault();
      option.selected = !option.selected;
      selectEl.dispatchEvent(new Event('change', { bubbles: true }));
    });
  }

  /**
   * Populate the main task input selection list.
   */
  populateMainInputTasks(task = null) {
    const selectEl = document.getElementById('taskModalInputTasks');
    if (!selectEl) return;

    const currentTaskId = this.editingTaskId || '';
    const excludeIds = new Set();
    if (currentTaskId) excludeIds.add(currentTaskId);

    const options = this.getWorkspaceTaskOptions({ excludeIds });
    const selectedRefs = (task?.input_task_ids || []).map((id) => `task:${id}`);

    selectEl.innerHTML = '';
    options.forEach((opt) => {
      const optionEl = document.createElement('option');
      optionEl.value = opt.value;
      optionEl.textContent = opt.label;
      optionEl.selected = selectedRefs.includes(opt.value);
      selectEl.appendChild(optionEl);
    });
    this.bindMultiSelectToggle(selectEl);
    if (selectedRefs.length === 0) {
      selectEl.selectedIndex = -1;
    }

    const emptyEl = document.getElementById('taskModalInputTasksEmpty');
    if (emptyEl) {
      emptyEl.style.display = options.length > 0 ? 'none' : 'block';
    }
  }

  /**
   * Update main input selection state when subtasks exist.
   */
  updateMainInputTasksState() {
    const selectEl = document.getElementById('taskModalInputTasks');
    const noticeEl = document.getElementById('taskModalInputTasksNotice');
    if (!selectEl || !noticeEl) return;

    const list = document.getElementById('taskModalSubtaskList');
    const hasSubtasks = list && list.children.length > 0;
    selectEl.disabled = hasSubtasks;
    noticeEl.style.display = hasSubtasks ? 'block' : 'none';

    if (hasSubtasks) {
      Array.from(selectEl.options).forEach((option) => {
        option.selected = false;
      });
    }
  }

  /**
   * Refresh input options for all subtask input selects.
   */
  refreshSubtaskInputOptions() {
    const list = document.getElementById('taskModalSubtaskList');
    if (!list) return;

    const cards = Array.from(list.querySelectorAll('.task-modal-subtask-card'));
    if (cards.length === 0) return;

    const stepOptions = cards.map((card, index) => {
      const title = card.querySelector('.task-modal-subtask-title')?.value?.trim();
      const label = title ? `Step ${index + 1}: ${title}` : `Step ${index + 1}`;
      return { value: `step:${index + 1}`, label };
    });

    const workflowTaskIds = new Set();
    if (Array.isArray(this.loadedSubtasks)) {
      this.loadedSubtasks.forEach((task) => {
        if (task?.id) workflowTaskIds.add(task.id);
      });
    }
    if (this.editingTaskId) {
      workflowTaskIds.add(this.editingTaskId);
    }

    const externalOptions = this.getWorkspaceTaskOptions({ excludeIds: workflowTaskIds });

    cards.forEach((card, index) => {
      const selectEl = card.querySelector('.task-modal-subtask-inputs');
      if (!selectEl) return;

      let selectedRefs = [];
      if (selectEl.dataset.selectedInputs) {
        try {
          selectedRefs = JSON.parse(selectEl.dataset.selectedInputs) || [];
        } catch (error) {
          selectedRefs = [];
        }
      } else {
        selectedRefs = this.getSelectedInputRefs(selectEl);
      }

      selectEl.innerHTML = '';

      const availableSteps = stepOptions.slice(0, index);
      if (availableSteps.length > 0) {
        const stepGroup = document.createElement('optgroup');
        stepGroup.label = 'Workflow steps';
        availableSteps.forEach((opt) => {
          const optionEl = document.createElement('option');
          optionEl.value = opt.value;
          optionEl.textContent = opt.label;
          stepGroup.appendChild(optionEl);
        });
        selectEl.appendChild(stepGroup);
      }

      if (externalOptions.length > 0) {
        const taskGroup = document.createElement('optgroup');
        taskGroup.label = 'Workspace tasks';
        externalOptions.forEach((opt) => {
          const optionEl = document.createElement('option');
          optionEl.value = opt.value;
          optionEl.textContent = opt.label;
          taskGroup.appendChild(optionEl);
        });
        selectEl.appendChild(taskGroup);
      }

      if (availableSteps.length === 0 && externalOptions.length === 0) {
        const optionEl = document.createElement('option');
        optionEl.value = '';
        optionEl.textContent = '-- No input tasks available --';
        optionEl.disabled = true;
        selectEl.appendChild(optionEl);
      }

      const appliedSelections = [];
      Array.from(selectEl.options).forEach((option) => {
        if (selectedRefs.includes(option.value)) {
          option.selected = true;
          appliedSelections.push(option.value);
        }
      });
      if (appliedSelections.length === 0) {
        selectEl.selectedIndex = -1;
      }
      selectEl.dataset.selectedInputs = JSON.stringify(appliedSelections);

      this.updateSubtaskInputsBadge(card, appliedSelections.length);
    });

    this.updateMainInputTasksState();
  }

  /**
   * Update the inputs badge for a subtask card.
   */
  updateSubtaskInputsBadge(card, count) {
    if (!card) return;
    const badge = card.querySelector('.task-modal-subtask-inputs-badge');
    if (!badge) return;
    if (count > 0) {
      badge.textContent = `Inputs: ${count}`;
      badge.style.display = 'inline-flex';
    } else {
      badge.style.display = 'none';
    }
  }

  /**
   * Parse assignment select value into to + assigned node id.
   */
  parseAssignmentValue(assignment) {
    let to = '';
    let assignedNodeId = '';
    if (assignment && assignment.startsWith('node:')) {
      assignedNodeId = assignment.slice('node:'.length);
      const match = assignedNodeId.match(/^(.+)-node-\d+$/);
      to = match ? match[1] : assignedNodeId;
    }
    return { to, assignedNodeId };
  }

  /**
   * Get assignment select options from the main dropdown.
   */
  getAssignmentOptions() {
    const selectEl = document.getElementById('taskModalAssignment');
    if (!selectEl) return [];
    return Array.from(selectEl.options).map((opt) => ({
      value: opt.value,
      label: opt.textContent || ''
    }));
  }

  /**
   * Get assignment value for an existing task.
   */
  getAssignmentValueFromTask(task) {
    if (task?.assigned_node_id) {
      return `node:${task.assigned_node_id}`;
    }
    if (task?.to) {
      return `node:${task.to}-node-1`;
    }
    return '';
  }

  /**
   * Refresh assignment dropdowns for all subtask rows.
   */
  refreshSubtaskAssignmentOptions() {
    const options = this.getAssignmentOptions();
    if (options.length === 0) return;

    document.querySelectorAll('.task-modal-subtask-assignment').forEach((select) => {
      const currentValue = select.value;
      select.innerHTML = '';
      options.forEach((opt) => {
        const optionEl = document.createElement('option');
        optionEl.value = opt.value;
        optionEl.textContent = opt.label;
        select.appendChild(optionEl);
      });
      if (currentValue) {
        select.value = currentValue;
      }
    });
  }

  /**
   * Bind subtask events.
   */
  bindSubtaskEvents() {
    const addBtn = document.getElementById('taskModalAddSubtask');
    if (addBtn) {
      addBtn.addEventListener('click', () => {
        if (this.subtaskSectionDisabled) return;
        this.addSubtaskRow();
      });
    }

    const list = document.getElementById('taskModalSubtaskList');
    if (list) {
      list.addEventListener('click', (event) => {
        const removeBtn = event.target.closest('[data-action="remove-subtask"]');
        if (!removeBtn) return;
        const card = removeBtn.closest('.task-modal-subtask-card');
        if (!card) return;
        const subtaskId = card.dataset.subtaskId;
        if (subtaskId) {
          this.subtasksToDelete.add(subtaskId);
        }
        card.remove();
        this.updateSubtaskSteps();
        this.updateSubtaskEmptyState();
        this.updateSubtaskHint();
        this.refreshSubtaskInputOptions();
      });
    }
  }

  /**
   * Reset subtask state and UI.
   */
  resetSubtasks() {
    this.subtaskCounter = 0;
    this.subtasksToDelete = new Set();
    this.loadedSubtasks = [];
    const list = document.getElementById('taskModalSubtaskList');
    if (list) list.innerHTML = '';
    this.setSubtaskSectionDisabled(false);
    this.updateSubtaskSteps();
    this.updateSubtaskEmptyState();
    this.updateSubtaskHint();
    this.refreshSubtaskInputOptions();
  }

  /**
   * Disable or enable the subtask section.
   */
  setSubtaskSectionDisabled(disabled, message = '') {
    this.subtaskSectionDisabled = disabled;
    const list = document.getElementById('taskModalSubtaskList');
    const empty = document.getElementById('taskModalSubtaskEmpty');
    const addBtn = document.getElementById('taskModalAddSubtask');
    const disabledEl = document.getElementById('taskModalSubtaskDisabled');

    if (disabledEl) {
      disabledEl.style.display = disabled ? 'block' : 'none';
      if (disabled && message) disabledEl.textContent = message;
    }
    if (list) list.style.display = disabled ? 'none' : 'flex';
    if (empty) empty.style.display = disabled ? 'none' : (list && list.children.length > 0 ? 'none' : 'block');
    if (addBtn) addBtn.disabled = disabled;
    this.updateSubtaskHint();
  }

  /**
   * Update empty state for subtasks.
   */
  updateSubtaskEmptyState() {
    const list = document.getElementById('taskModalSubtaskList');
    const empty = document.getElementById('taskModalSubtaskEmpty');
    if (!list || !empty) return;
    if (this.subtaskSectionDisabled) {
      empty.style.display = 'none';
      this.updateMainInputTasksState();
      return;
    }
    empty.style.display = list.children.length > 0 ? 'none' : 'block';
    this.updateMainInputTasksState();
  }

  /**
   * Update subtask hint text.
   */
  updateSubtaskHint() {
    const hint = document.getElementById('taskModalSubtaskHint');
    const list = document.getElementById('taskModalSubtaskList');
    if (!hint) return;
    if (this.subtaskSectionDisabled) {
      hint.textContent = '';
      return;
    }
    const hasSubtasks = list && list.children.length > 0;
    hint.textContent = hasSubtasks
      ? 'Schedules run the first step. Auto-save stores the final step.'
      : 'Add steps to turn this task into a workflow.';
  }

  /**
   * Update subtask step labels.
   */
  updateSubtaskSteps() {
    const list = document.getElementById('taskModalSubtaskList');
    if (!list) return;
    Array.from(list.children).forEach((card, index) => {
      const step = card.querySelector('.task-modal-subtask-step');
      if (step) step.textContent = `Step ${index + 1}`;
    });
  }

  /**
   * Add a subtask row to the UI.
   */
  addSubtaskRow(data = {}) {
    const list = document.getElementById('taskModalSubtaskList');
    if (!list || this.subtaskSectionDisabled) return;

    this.subtaskCounter += 1;
    const rowId = `taskModalSubtask${this.subtaskCounter}`;
    const assignmentValue = data.assignmentValue || '';

    const card = document.createElement('div');
    card.className = 'task-modal-subtask-card';
    if (data.id) {
      card.dataset.subtaskId = data.id;
    }

    const topRow = document.createElement('div');
    topRow.className = 'task-modal-subtask-top';

    const stepLabel = document.createElement('span');
    stepLabel.className = 'task-modal-subtask-step';
    stepLabel.textContent = 'Step';

    const inputsBadge = document.createElement('span');
    inputsBadge.className = 'task-modal-subtask-inputs-badge';
    const inputCount = Number.isFinite(data.inputCount) ? data.inputCount : 0;
    if (inputCount > 0) {
      inputsBadge.textContent = `Inputs: ${inputCount}`;
      inputsBadge.title = `Uses results from ${inputCount} task${inputCount === 1 ? '' : 's'}`;
    } else {
      inputsBadge.style.display = 'none';
    }

    const actions = document.createElement('div');
    actions.className = 'task-modal-subtask-actions';

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.className = 'task-modal-subtask-remove';
    removeBtn.setAttribute('data-action', 'remove-subtask');
    removeBtn.setAttribute('title', 'Remove subtask');
    removeBtn.innerHTML = `
      <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
        <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
      </svg>
    `;

    actions.appendChild(removeBtn);
    topRow.appendChild(stepLabel);
    topRow.appendChild(inputsBadge);
    topRow.appendChild(actions);
    card.appendChild(topRow);

    const titleField = document.createElement('div');
    titleField.className = 'task-modal-field';
    const titleLabel = document.createElement('label');
    titleLabel.className = 'task-modal-label';
    titleLabel.setAttribute('for', `${rowId}-title`);
    titleLabel.textContent = 'Title';
    const titleInput = document.createElement('input');
    titleInput.type = 'text';
    titleInput.id = `${rowId}-title`;
    titleInput.className = 'task-modal-input task-modal-subtask-title';
    titleInput.placeholder = 'Describe this step...';
    titleInput.value = data.description || '';
    titleField.appendChild(titleLabel);
    titleField.appendChild(titleInput);

    const row = document.createElement('div');
    row.className = 'task-modal-subtask-row';

    row.appendChild(titleField);

    const assignField = document.createElement('div');
    assignField.className = 'task-modal-field';
    const assignLabel = document.createElement('label');
    assignLabel.className = 'task-modal-label';
    assignLabel.setAttribute('for', `${rowId}-assignment`);
    assignLabel.textContent = 'Assign to Agent';
    const assignmentSelect = document.createElement('select');
    assignmentSelect.id = `${rowId}-assignment`;
    assignmentSelect.className = 'task-modal-input task-modal-subtask-assignment';

    const options = this.getAssignmentOptions();
    if (options.length === 0) {
      const optionEl = document.createElement('option');
      optionEl.value = '';
      optionEl.textContent = '-- No agent (manual task) --';
      assignmentSelect.appendChild(optionEl);
    } else {
      options.forEach((opt) => {
        const optionEl = document.createElement('option');
        optionEl.value = opt.value;
        optionEl.textContent = opt.label;
        assignmentSelect.appendChild(optionEl);
      });
    }
    if (assignmentValue) {
      assignmentSelect.value = assignmentValue;
    }

    assignField.appendChild(assignLabel);
    assignField.appendChild(assignmentSelect);
    row.appendChild(assignField);

    const detailsField = document.createElement('div');
    detailsField.className = 'task-modal-field';
    const detailsLabel = document.createElement('label');
    detailsLabel.className = 'task-modal-label';
    detailsLabel.setAttribute('for', `${rowId}-details`);
    detailsLabel.textContent = 'Details';
    const detailsInput = document.createElement('textarea');
    detailsInput.id = `${rowId}-details`;
    detailsInput.className = 'task-modal-textarea task-modal-subtask-details';
    detailsInput.rows = 2;
    detailsInput.placeholder = 'Add optional context or instructions...';
    detailsInput.value = data.details || '';
    detailsField.appendChild(detailsLabel);
    detailsField.appendChild(detailsInput);

    const inputsField = document.createElement('div');
    inputsField.className = 'task-modal-field';
    const inputsLabel = document.createElement('label');
    inputsLabel.className = 'task-modal-label';
    inputsLabel.setAttribute('for', `${rowId}-inputs`);
    inputsLabel.textContent = 'Input Tasks';
    const inputsSelect = document.createElement('select');
    inputsSelect.id = `${rowId}-inputs`;
    inputsSelect.className = 'task-modal-input task-modal-subtask-inputs';
    inputsSelect.multiple = true;
    inputsSelect.size = 3;
    inputsSelect.dataset.selectedInputs = JSON.stringify(data.inputTaskIds || []);
    this.bindMultiSelectToggle(inputsSelect);
    inputsField.appendChild(inputsLabel);
    inputsField.appendChild(inputsSelect);

    const inputsHint = document.createElement('div');
    inputsHint.className = 'task-modal-subtask-inputs-hint';
    inputsHint.textContent = 'Use {input1}, {input2}, {previous}, or {result} in the task title.';
    inputsField.appendChild(inputsHint);

    inputsSelect.addEventListener('change', () => {
      const selectedRefs = this.getSelectedInputRefs(inputsSelect);
      inputsSelect.dataset.selectedInputs = JSON.stringify(selectedRefs);
      this.updateSubtaskInputsBadge(card, selectedRefs.length);
    });

    titleInput.addEventListener('input', () => {
      this.refreshSubtaskInputOptions();
    });

    card.appendChild(row);
    card.appendChild(detailsField);
    card.appendChild(inputsField);

    list.appendChild(card);
    this.updateSubtaskSteps();
    this.updateSubtaskEmptyState();
    this.updateSubtaskHint();
    this.refreshSubtaskInputOptions();

    if (!data.description) {
      titleInput.focus();
    }
  }

  /**
   * Collect subtask data from the form.
   */
  collectSubtasks() {
    const list = document.getElementById('taskModalSubtaskList');
    if (!list) return { subtasks: [] };

    const cards = Array.from(list.querySelectorAll('.task-modal-subtask-card'));
    const subtasks = [];
    let invalidInput = null;

    for (const card of cards) {
      const titleInput = card.querySelector('.task-modal-subtask-title');
      const detailsInput = card.querySelector('.task-modal-subtask-details');
      const assignmentSelect = card.querySelector('.task-modal-subtask-assignment');
      const inputsSelect = card.querySelector('.task-modal-subtask-inputs');

      const description = titleInput?.value?.trim() || '';
      const details = detailsInput?.value?.trim() || '';
      const assignment = assignmentSelect?.value || '';
      const inputRefs = this.getSelectedInputRefs(inputsSelect);

      if (!description) {
        invalidInput = titleInput || detailsInput || assignmentSelect;
        break;
      }

      const assignmentData = this.parseAssignmentValue(assignment);
      subtasks.push({
        id: card.dataset.subtaskId || '',
        description,
        details,
        to: assignmentData.to || '',
        assigned_node_id: assignmentData.assignedNodeId || '',
        input_task_ids: inputRefs
      });
    }

    return { subtasks, invalidInput };
  }

  /**
   * Resolve schedule payload for workflow subtasks.
   */
  getWorkflowSchedulePayload(scheduleData, index, total, forUpdate) {
    if (total <= 0) return {};
    const isFirst = index === 0;
    if (isFirst) {
      if (scheduleData?.schedule_enabled) {
        return scheduleData;
      }
      return forUpdate ? { schedule: null, schedule_enabled: false, schedule_name: '' } : {};
    }
    return forUpdate ? { schedule: null, schedule_enabled: false, schedule_name: '' } : {};
  }

  /**
   * Resolve result storage payload for workflow subtasks.
   */
  getWorkflowAutoSavePayload(autoSaveData, index, total, forUpdate) {
    if (total <= 0) return {};
    const isLast = index === total - 1;
    const enabled = autoSaveData?.result_storage?.enabled;
    if (isLast) {
      if (enabled) {
        return autoSaveData;
      }
      return forUpdate ? { result_storage: null } : {};
    }
    return forUpdate ? { result_storage: null } : {};
  }

  /**
   * Load subtasks for a parent task.
   */
  async loadSubtasks(parentTaskId) {
    if (!parentTaskId || !this.workspaceId) return [];

    try {
      let tasks = Array.isArray(this.workspaceTasks) ? this.workspaceTasks : [];
      if (tasks.length === 0) {
        tasks = await this.loadWorkspaceTasks();
      }
      const subtasks = tasks.filter((task) => task.parent_task_id === parentTaskId);

      subtasks.sort((a, b) => {
        const aIndex = Number.isFinite(a.subtask_index) && a.subtask_index > 0 ? a.subtask_index : Number.MAX_SAFE_INTEGER;
        const bIndex = Number.isFinite(b.subtask_index) && b.subtask_index > 0 ? b.subtask_index : Number.MAX_SAFE_INTEGER;
        if (aIndex !== bIndex) return aIndex - bIndex;
        const aTime = a.created_at ? new Date(a.created_at).getTime() : 0;
        const bTime = b.created_at ? new Date(b.created_at).getTime() : 0;
        if (aTime !== bTime) return aTime - bTime;
        return String(a.id || '').localeCompare(String(b.id || ''));
      });

      return subtasks;
    } catch (error) {
      console.error('Failed to load subtasks:', error);
      return [];
    }
  }

  /**
   * Populate the agent assignment dropdown with all available agents
   * @param {string} workspaceId - The workspace ID (for context, not used for filtering)
   * @param {object} task - Optional task to set current assignment
   */
  async populateAgentDropdown(workspaceId, task = null) {
    const selectEl = document.getElementById('taskModalAssignment');
    if (!selectEl) return;

    // Start with default option
    const options = [{ label: '-- No agent (manual task) --', value: '' }];

    try {
      // Fetch all available agents (not workspace-specific)
      const response = await fetch('/api/agents/dashboard/list');
      if (response.ok) {
        const data = await response.json();
        const agents = data.agents || [];

        agents.forEach(agent => {
          // Use agent name as node ID (agent-name-node-1 format)
          const nodeId = `${agent.name}-node-1`;
          options.push({
            label: agent.name,
            value: `node:${nodeId}`
          });
        });
      }
    } catch (err) {
      console.error('Failed to load agents:', err);
    }

    // Build options HTML
    selectEl.innerHTML = options
      .map(o => `<option value="${o.value}">${o.label}</option>`)
      .join('');

    // Set current value if editing
    if (task?.assigned_node_id) {
      selectEl.value = `node:${task.assigned_node_id}`;
    } else if (task?.to) {
      // Try to match by agent name
      const nodeId = `${task.to}-node-1`;
      selectEl.value = `node:${nodeId}`;
    } else {
      selectEl.value = '';
    }
  }

  /**
   * Show a toast notification
   */
  showToast(message, type = 'info') {
    // Try to use existing toast mechanism if available
    if (window.sessionManager?.showToast) {
      window.sessionManager.showToast(message, type);
    } else if (window.Toast) {
      const normalizedType = (type || 'info').toLowerCase();
      const toastType = normalizedType === 'danger' ? 'error' : normalizedType === 'warn' ? 'warning' : normalizedType;
      const toastFn = window.Toast[toastType];
      if (typeof toastFn === 'function') {
        toastFn(message);
      } else if (typeof window.Toast.show === 'function') {
        window.Toast.show(message, toastType);
      } else {
        alert(message);
      }
    } else if (window.workspaceDashboard?.showToast) {
      window.workspaceDashboard.showToast('', message, type);
    } else {
      // Fallback to alert
      alert(message);
    }
  }

  // =============================================================================
  // File Attachment Methods
  // =============================================================================

  /**
   * Bind file drag-and-drop and input events
   */
  bindFileEvents() {
    const dropZone = document.getElementById('taskModalFileDropZone');
    const fileInput = document.getElementById('taskModalFileInput');

    if (dropZone) {
      dropZone.addEventListener('click', () => {
        document.getElementById('taskModalFileInput')?.click();
      });
      dropZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.stopPropagation();
        dropZone.classList.add('drag-active');
      });
      dropZone.addEventListener('dragleave', (e) => {
        e.preventDefault();
        e.stopPropagation();
        dropZone.classList.remove('drag-active');
      });
      dropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        e.stopPropagation();
        dropZone.classList.remove('drag-active');
        const files = e.dataTransfer?.files;
        if (files && files.length > 0) {
          this.addFiles(Array.from(files));
        }
      });
    }

    if (fileInput) {
      fileInput.addEventListener('change', (e) => {
        const files = e.target?.files;
        if (files && files.length > 0) {
          this.addFiles(Array.from(files));
          e.target.value = '';
        }
      });
    }

    // Directory button
    const directoryBtn = document.getElementById('taskModalAddDirectoryBtn');
    if (directoryBtn) {
      directoryBtn.addEventListener('click', () => this.openFolderPicker());
    }

    // File path button and input
    const filePathBtn = document.getElementById('taskModalAddFilePathBtn');
    const filePathInput = document.getElementById('taskModalFilePathInput');
    const filePathText = document.getElementById('taskModalFilePathText');
    const filePathConfirm = document.getElementById('taskModalAddFilePathConfirm');
    const filePathCancel = document.getElementById('taskModalAddFilePathCancel');

    if (filePathBtn && filePathInput) {
      filePathBtn.addEventListener('click', () => {
        filePathInput.style.display = 'block';
        filePathText?.focus();
      });
    }

    if (filePathConfirm && filePathText) {
      filePathConfirm.addEventListener('click', () => {
        this.addFilePath(filePathText.value);
        filePathText.value = '';
        filePathInput.style.display = 'none';
      });
    }

    if (filePathCancel && filePathInput) {
      filePathCancel.addEventListener('click', () => {
        filePathText.value = '';
        filePathInput.style.display = 'none';
      });
    }

    // Allow Enter key to confirm file path
    if (filePathText) {
      filePathText.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          this.addFilePath(filePathText.value);
          filePathText.value = '';
          filePathInput.style.display = 'none';
        } else if (e.key === 'Escape') {
          filePathText.value = '';
          filePathInput.style.display = 'none';
        }
      });
    }
  }

  /**
   * Open folder picker to add a directory reference
   */
  async openFolderPicker() {
    if (!this.workspaceId) {
      this.showToast('Please select a workspace first', 'warning');
      return;
    }

    const btn = document.getElementById('taskModalAddDirectoryBtn');
    const originalHtml = btn?.innerHTML;
    if (btn) {
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
    }

    try {
      const response = await fetch('/api/launch-folder-picker', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workspace_id: this.workspaceId })
      });
      const result = await response.json();

      if (result.success) {
        this.showToast('Folder picker opened. Select a folder to add it.', 'info');
      } else {
        this.showToast(result.error || 'Failed to open folder picker', 'error');
      }
    } catch (error) {
      console.error('Failed to open folder picker:', error);
      this.showToast('Failed to open folder picker', 'error');
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.innerHTML = originalHtml;
      }
    }
  }

  /**
   * Add files to pending list
   */
  addFiles(files) {
    const maxSize = 100 * 1024 * 1024; // 100MB

    files.forEach((file) => {
      if (file.size > maxSize) {
        this.showToast(`${file.name} exceeds 100MB limit`, 'warning');
        return;
      }
      // Avoid duplicates
      if (!this.pendingFiles.some((f) => f.name === file.name && f.size === file.size)) {
        this.pendingFiles.push(file);
      }
    });

    this.updateFilesPreview();
  }

  /**
   * Add a file path reference (no upload, backend reads directly)
   */
  addFilePath(path) {
    if (!path || !path.trim()) return;

    const trimmedPath = path.trim();

    // Avoid duplicates
    if (this.pendingFilePaths.includes(trimmedPath)) {
      this.showToast('File path already added', 'warning');
      return;
    }

    this.pendingFilePaths.push(trimmedPath);
    this.updateFilesPreview();
  }

  /**
   * Update file preview display
   */
  updateFilesPreview() {
    const container = document.getElementById('taskModalSelectedFiles');
    if (!container) return;

    const hasFiles = this.pendingFiles.length > 0;
    const hasFilePaths = this.pendingFilePaths.length > 0;

    if (!hasFiles && !hasFilePaths) {
      container.style.display = 'none';
      container.innerHTML = '';
      return;
    }

    container.style.display = 'block';

    const formatSize = (bytes) => {
      if (!bytes) return '';
      if (bytes < 1024) return bytes + ' B';
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
      return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    };

    const escapeHtml = (text) => {
      const div = document.createElement('div');
      div.textContent = text;
      return div.innerHTML;
    };

    // Uploaded files
    const fileItems = this.pendingFiles.map((file, index) => `
      <div class="task-selected-file-item" data-index="${index}" data-type="file">
        <span class="task-file-name">${escapeHtml(file.name)}</span>
        <span class="task-file-size">${formatSize(file.size)}</span>
        <button type="button" class="task-file-remove" data-index="${index}" data-type="file" title="Remove">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
          </svg>
        </button>
      </div>
    `);

    // File path references
    const pathItems = this.pendingFilePaths.map((path, index) => {
      const fileName = path.split('/').pop() || path;
      return `
        <div class="task-selected-file-item task-file-path-item" data-index="${index}" data-type="path">
          <span class="task-file-name" title="${escapeHtml(path)}">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" style="margin-right: 4px; opacity: 0.6;">
              <path d="M16,9V7H12V5H10V7H6V9H10V17H6V19H10V21H12V19H16V17H12V9H16Z"/>
            </svg>
            ${escapeHtml(fileName)}
          </span>
          <span class="task-file-size" style="color: var(--text-tertiary); font-style: italic;">path ref</span>
          <button type="button" class="task-file-remove" data-index="${index}" data-type="path" title="Remove">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    container.innerHTML = [...fileItems, ...pathItems].join('');

    // Bind remove buttons
    container.querySelectorAll('.task-file-remove').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const index = parseInt(btn.dataset.index, 10);
        const type = btn.dataset.type;
        if (type === 'path') {
          this.pendingFilePaths.splice(index, 1);
        } else {
          this.pendingFiles.splice(index, 1);
        }
        this.updateFilesPreview();
      });
    });
  }

  /**
   * Extract a task object from API responses with different shapes.
   */
  extractTaskFromResponse(payload) {
    if (!payload) return null;
    if (payload.task) return payload.task;
    if (payload.id) return payload;
    return null;
  }

  /**
   * Reset file attachments
   */
  resetFiles() {
    this.pendingFiles = [];
    this.pendingFilePaths = [];
    this.pendingDirectories = [];
    this.updateFilesPreview();
    // Hide file path input if visible
    const filePathInput = document.getElementById('taskModalFilePathInput');
    if (filePathInput) filePathInput.style.display = 'none';
  }

  /**
   * Upload pending files to a task
   */
  async uploadFilesToTask(taskId) {
    if (!taskId || this.pendingFiles.length === 0) return;

    for (const file of this.pendingFiles) {
      try {
        const formData = new FormData();
        formData.append('file', file);

        const response = await fetch(`/api/orchestration/tasks/${taskId}/files`, {
          method: 'POST',
          body: formData
        });

        if (!response.ok) {
          throw new Error(`Upload failed: ${response.status}`);
        }
      } catch (error) {
        console.error('Failed to upload file:', file.name, error);
        this.showToast(`Failed to upload ${file.name}`, 'error');
      }
    }
  }

  /**
   * Save file path references to a task
   */
  async saveFilePathsToTask(taskId) {
    if (!taskId || this.pendingFilePaths.length === 0) return;

    try {
      const response = await fetch(`/api/orchestration/tasks/${taskId}/file-paths`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ file_paths: this.pendingFilePaths })
      });

      if (!response.ok) {
        throw new Error(`Failed to save file paths: ${response.status}`);
      }
    } catch (error) {
      console.error('Failed to save file paths:', error);
      this.showToast('Failed to save file path references', 'error');
    }
  }
}

// Create global instance
window.taskModalController = new TaskModalController();

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  window.taskModalController.init();
});
