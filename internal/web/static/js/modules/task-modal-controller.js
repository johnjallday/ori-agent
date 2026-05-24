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
    this.initialFormState = null;
    this.initialized = false;
    this.isSaving = false;
    this.lastFocusedElement = null;
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
    this.currentResultText = '';
    this.currentResultSourceTaskId = '';
    this.currentResultNextSteps = [];
    this.currentResultFollowUpPending = false;
    this.currentMissingMCPRequirement = null;
    this.mcpRequirementPending = false;
    this.outputContractSuggestionCache = new Map();
    this.outputContractSuggestionRequestKey = '';
    this.outputContractEdited = false;
    this.outputContractSource = 'manual';
    this.outputContractEditTelemetrySent = false;
    this.outputSpecDraft = null;
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
    document.getElementById('taskModal')?.addEventListener('keydown', (e) => this.handleModalKeydown(e));

    // Save button
    document.getElementById('taskModalSave')?.addEventListener('click', () => this.save());
    document.getElementById('taskModalMCPRequirementAdd')?.addEventListener('click', () => {
      void this.addRequiredMCPConnector();
    });

    // Schedule fields toggle
    document.getElementById('taskModalScheduleEnabled')?.addEventListener('change', (e) => {
      const scheduleFields = document.getElementById('taskModalScheduleFields');
      if (scheduleFields) {
        scheduleFields.style.display = e.target.checked ? 'block' : 'none';
      }
      this.updateSchedulePreview();
    });

    // Schedule type change handler
    document.getElementById('taskModalScheduleType')?.addEventListener('change', () => {
      this.updateScheduleTypeFields();
      this.updateSchedulePreview();
    });

    // Live preview triggers — any field that affects the schedule
    // summary should refresh the preview text.
    [
      'taskModalScheduleTime',
      'taskModalScheduleIntervalValue',
      'taskModalScheduleIntervalUnit',
      'taskModalScheduleDatetime',
      'taskModalScheduleCron'
    ].forEach((id) => {
      document.getElementById(id)?.addEventListener('input', () => this.updateSchedulePreview());
      document.getElementById(id)?.addEventListener('change', () => this.updateSchedulePreview());
    });

    // Day-of-week pills + presets
    const dayRow = document.getElementById('taskModalScheduleDayRow');
    if (dayRow) {
      dayRow.addEventListener('change', () => this.updateSchedulePreview());
    }
    document.querySelectorAll('[data-day-preset]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const preset = btn.getAttribute('data-day-preset') || '';
        const presets = {
          weekdays: [1, 2, 3, 4, 5],
          weekend: [0, 6],
          all: [0, 1, 2, 3, 4, 5, 6]
        };
        this.setSelectedScheduleDays(presets[preset] || []);
        this.updateSchedulePreview();
      });
    });

    // Auto-save fields toggle
    document.getElementById('taskModalAutoSaveEnabled')?.addEventListener('change', (e) => {
      const autoSaveFields = document.getElementById('taskModalAutoSaveFields');
      if (autoSaveFields) {
        autoSaveFields.style.display = e.target.checked ? 'block' : 'none';
      }
      this.updateAutoSaveWriteModeFields();
    });

    // Auto-save target change handler
    document.getElementById('taskModalAutoSaveTarget')?.addEventListener('change', () => {
      this.updateAutoSaveTargetFields();
    });
    document.getElementById('taskModalAutoSaveWriteMode')?.addEventListener('change', () => {
      this.updateAutoSaveWriteModeFields();
    });
    document.getElementById('taskModalOutputContractAddColumn')?.addEventListener('click', () => {
      this.markOutputContractEdited();
      this.addOutputContractRow();
      this.updateOutputContractEmptyState();
    });
    document.getElementById('taskModalOutputContractSuggest')?.addEventListener('click', () => {
      void this.regenerateOutputContractSuggestion();
    });
    document.getElementById('taskModalOutputContractRows')?.addEventListener('click', (event) => {
      const removeButton = event.target?.closest?.('[data-output-contract-remove]');
      if (!removeButton) return;
      this.markOutputContractEdited();
      removeButton.closest('.task-modal-output-contract-row')?.remove();
      this.updateOutputContractEmptyState();
    });
    document.getElementById('taskModalOutputContractRows')?.addEventListener('input', () => {
      this.markOutputContractEdited();
      this.clearOutputContractError();
    });
    document.getElementById('taskModalOutputContractRows')?.addEventListener('change', () => {
      this.markOutputContractEdited();
      this.clearOutputContractError();
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

  isModalOpen() {
    const modal = document.getElementById('taskModal');
    return Boolean(modal && modal.style.display !== 'none' && modal.style.display !== '');
  }

  getModalStateSnapshot() {
    const readValue = (id) => document.getElementById(id)?.value || '';
    const readChecked = (id) => Boolean(document.getElementById(id)?.checked);
    const taskMode = document.querySelector('input[name="taskConfigMode"]:checked')?.value || 'manual';
    const normalizePendingItem = (item) => {
      if (item === null || item === undefined) return '';
      if (typeof item === 'string' || typeof item === 'number' || typeof item === 'boolean') {
        return String(item);
      }
      if (typeof item === 'object') {
        return String(item.path || item.name || item.id || '');
      }
      return String(item);
    };

    const subtasks = Array.from(document.querySelectorAll('#taskModalSubtaskList .task-modal-subtask-card')).map((card) => {
      const inputSelect = card.querySelector('.task-modal-subtask-inputs');
      let inputRefs = this.getSelectedInputRefs(inputSelect);
      if (inputRefs.length === 0 && inputSelect?.dataset.selectedInputs) {
        try {
          inputRefs = JSON.parse(inputSelect.dataset.selectedInputs) || [];
        } catch (_error) {
          inputRefs = [];
        }
      }

      return {
        id: card.dataset.subtaskId || '',
        description: card.querySelector('.task-modal-subtask-title')?.value || '',
        details: card.querySelector('.task-modal-subtask-details')?.value || '',
        assignment: card.querySelector('.task-modal-subtask-assignment')?.value || '',
        inputTaskIds: inputRefs
      };
    });

    const scheduleEnabled = readChecked('taskModalScheduleEnabled');
    const autoSaveEnabled = readChecked('taskModalAutoSaveEnabled');

    return JSON.stringify({
      editingTaskId: this.editingTaskId || '',
      workspaceId: this.workspaceId || '',
      taskMode,
      autoMode: this.autoMode,
      manual: {
        description: readValue('taskModalDescription'),
        details: readValue('taskModalDetails'),
        priority: readValue('taskModalPriority'),
        assignment: readValue('taskModalAssignment'),
        inputTaskIds: this.getSelectedInputRefs(document.getElementById('taskModalInputTasks'))
      },
      auto: {
        description: readValue('taskAutoDescription'),
        editDescription: readValue('taskAutoEditDescription')
      },
      schedule: {
        enabled: scheduleEnabled,
        name: scheduleEnabled ? readValue('taskModalScheduleName') : '',
        type: scheduleEnabled ? readValue('taskModalScheduleType') : '',
        time: scheduleEnabled ? readValue('taskModalScheduleTime') : '',
        day: scheduleEnabled ? readValue('taskModalScheduleDay') : '',
        intervalValue: scheduleEnabled ? readValue('taskModalScheduleIntervalValue') : '',
        intervalUnit: scheduleEnabled ? readValue('taskModalScheduleIntervalUnit') : '',
        runAt: scheduleEnabled ? readValue('taskModalScheduleDatetime') : ''
      },
      autoSave: {
        enabled: autoSaveEnabled,
        writeMode: autoSaveEnabled ? readValue('taskModalAutoSaveWriteMode') : '',
        target: autoSaveEnabled ? readValue('taskModalAutoSaveTarget') : '',
        storeNode: autoSaveEnabled ? readValue('taskModalAutoSaveStoreNode') : '',
        path: autoSaveEnabled ? readValue('taskModalAutoSavePath') : '',
        format: autoSaveEnabled ? readValue('taskModalAutoSaveFormat') : '',
        outputContract: autoSaveEnabled ? this.getOutputContractRows() : []
      },
      files: this.pendingFiles.map((file) => ({
        name: file.name,
        size: file.size,
        lastModified: file.lastModified
      })),
      filePaths: [...this.pendingFilePaths],
      directories: this.pendingDirectories.map(normalizePendingItem),
      subtasks
    });
  }

  captureInitialFormState() {
    this.initialFormState = this.getModalStateSnapshot();
  }

  hasUnsavedChanges() {
    if (!this.isModalOpen() || !this.initialFormState) {
      return false;
    }
    return this.getModalStateSnapshot() !== this.initialFormState;
  }

  async finalizeSuccessfulSave(eventName, eventPayload = {}) {
    const modalContext = {
      editingTaskId: this.editingTaskId,
      onSaveCallback: this.onSaveCallback,
      workspaceId: this.workspaceId
    };

    this.close({ force: true });

    if (window.EventBus && eventName) {
      const payload = { ...eventPayload };
      if (!payload.workspaceId && modalContext.workspaceId) {
        payload.workspaceId = modalContext.workspaceId;
      }
      if (eventName === 'task:updated' && !payload.taskId && modalContext.editingTaskId) {
        payload.taskId = modalContext.editingTaskId;
      }
      EventBus.emit(eventName, payload);
    }

    if (typeof modalContext.onSaveCallback === 'function') {
      await modalContext.onSaveCallback();
    }
  }

  getModalRoot() {
    return document.getElementById('taskModal');
  }

  getModalContent() {
    return this.getModalRoot()?.querySelector('.task-modal-content') || null;
  }

  getFocusableModalElements() {
    const content = this.getModalContent();
    if (!content) return [];

    return Array.from(content.querySelectorAll(
      'button:not([disabled]), [href], input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )).filter((element) => !element.hidden && element.getAttribute('aria-hidden') !== 'true' && element.offsetParent !== null);
  }

  focusModalElement(preferredElement = null) {
    const preferredVisible = preferredElement &&
      typeof preferredElement.focus === 'function' &&
      !preferredElement.hidden &&
      preferredElement.offsetParent !== null;

    if (preferredVisible) {
      preferredElement.focus();
      if (typeof preferredElement.select === 'function' && preferredElement.tagName === 'INPUT') {
        preferredElement.select();
      }
      return;
    }

    const [firstFocusable] = this.getFocusableModalElements();
    if (firstFocusable) {
      firstFocusable.focus();
      return;
    }

    this.getModalContent()?.focus();
  }

  showModal(preferredFocusElement = null) {
    const modal = this.getModalRoot();
    if (!modal) return;

    const activeElement = document.activeElement;
    this.lastFocusedElement = activeElement instanceof HTMLElement ? activeElement : null;

    modal.style.display = 'flex';
    modal.setAttribute('aria-hidden', 'false');
    document.body.classList.add('task-modal-open');

    window.requestAnimationFrame(() => {
      this.focusModalElement(preferredFocusElement);
    });
  }

  restoreFocus() {
    const previousElement = this.lastFocusedElement;
    this.lastFocusedElement = null;

    if (!previousElement || !document.contains(previousElement) || typeof previousElement.focus !== 'function') {
      return;
    }

    window.requestAnimationFrame(() => {
      previousElement.focus();
    });
  }

  handleModalKeydown(event) {
    if (!this.isModalOpen() || event.key !== 'Tab') {
      return;
    }

    const focusable = this.getFocusableModalElements();
    if (focusable.length === 0) {
      event.preventDefault();
      this.getModalContent()?.focus();
      return;
    }

    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const active = document.activeElement;

    if (event.shiftKey) {
      if (active === first || active === this.getModalContent()) {
        event.preventDefault();
        last.focus();
      }
      return;
    }

    if (active === last) {
      event.preventDefault();
      first.focus();
    }
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
  async openForCreate(workspaceId = null, prefillTitle = '', onSave = null, options = {}) {
    this.init();
    const createOptions = options && typeof options === 'object' ? options : {};
    const draftSubtasks = Array.isArray(createOptions.draftSubtasks)
      ? createOptions.draftSubtasks.filter(Boolean)
      : [];
    const draftMainInputRefs = Array.isArray(createOptions.draftMainInputRefs)
      ? createOptions.draftMainInputRefs.filter((value) => String(value || '').trim() !== '')
      : [];
    const draftAssignmentValue = String(createOptions.draftAssignmentValue || '').trim();
    const shouldForceAutoMode = Boolean(createOptions.forceAutoMode);
    const prefillAutoDescription = String(createOptions.prefillAutoDescription || '');
    const shouldForceManualMode = !shouldForceAutoMode && Boolean(
      createOptions.forceManualMode ||
      draftSubtasks.length > 0 ||
      draftMainInputRefs.length > 0 ||
      String(createOptions.prefillDetails || '').trim() ||
      draftAssignmentValue
    );

    this.editingTaskId = null;
    this.workspaceId = workspaceId;
    this.onSaveCallback = onSave;
    this.initialFormState = null;
    this.currentTask = null;
    this.resetResultSection();

    const modal = document.getElementById('taskModal');
    if (!modal) {
      console.error('Task modal markup is missing from the page.');
      this.showToast('Task editor is unavailable on this page.', 'error');
      return;
    }

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      modalTitle.textContent = String(createOptions.modalTitle || '').trim() || (draftSubtasks.length > 0 ? 'Review Workflow Draft' : 'Create Task');
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
    if (this.systemModelConfigured && this.llmAvailable && !shouldForceManualMode) {
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

    // Clear or prefill auto description textarea
    const autoDescription = document.getElementById('taskAutoDescription');
    if (autoDescription) autoDescription.value = prefillAutoDescription;

    // Clear/prefill form
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    const priorityInput = document.getElementById('taskModalPriority');
    const assignmentInput = document.getElementById('taskModalAssignment');

    if (descriptionInput) descriptionInput.value = String(createOptions.prefillTitle || prefillTitle || '');
    if (detailsInput) detailsInput.value = String(createOptions.prefillDetails || '');
    if (priorityInput) priorityInput.value = String(createOptions.draftPriority || '3');

    // Populate agent assignment dropdown
    await this.populateAgentDropdown(workspaceId);
    this.refreshSubtaskAssignmentOptions();
    await this.loadWorkspaceTasks();
    this.populateMainInputTasks();
    if (draftMainInputRefs.length > 0) {
      this.setMainInputTaskRefs(draftMainInputRefs);
    }
    this.refreshSubtaskInputOptions();
    if (assignmentInput) assignmentInput.value = draftAssignmentValue;

    // Reset subtasks
    this.resetSubtasks();
    if (draftSubtasks.length > 0) {
      draftSubtasks.forEach((subtask) => {
        this.addSubtaskRow({
          description: String(subtask.description || '').trim(),
          details: String(subtask.details || '').trim(),
          assignmentValue: String(subtask.assignmentValue || draftAssignmentValue || '').trim(),
          inputTaskIds: Array.isArray(subtask.inputTaskIds) ? subtask.inputTaskIds.filter(Boolean) : [],
          inputCount: Array.isArray(subtask.inputTaskIds) ? subtask.inputTaskIds.filter(Boolean).length : 0
        });
      });
    }

    // Reset schedule fields
    this.resetScheduleFields();

    // Reset auto-save fields
    this.resetAutoSaveFields();

    // Fetch workspace-specific output path
    await this.fetchWorkspaceOutputPath(workspaceId);

    // Reset file attachments
    this.resetFiles();

    // Show modal
    this.showModal(descriptionInput);
    this.captureInitialFormState();
  }

  /**
   * Open the modal for editing an existing task
   * @param {object} task - The task object to edit
   * @param {function} onSave - Optional callback after successful save
   */
  async openForEdit(task, onSave = null) {
    this.init();
    this.editingTaskId = task.id;
    this.workspaceId = task.workspace_id;
    this.onSaveCallback = onSave;
    this.initialFormState = null;
    this.currentTask = task; // Store for auto edit mode
    this.currentResultFollowUpPending = false;

    const modal = document.getElementById('taskModal');
    if (!modal) {
      console.error('Task modal markup is missing from the page.');
      this.showToast('Task editor is unavailable on this page.', 'error');
      return;
    }

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      modalTitle.textContent = 'Task Details';
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
    this.renderResultSection(task);

    // Show modal
    this.showModal(descriptionInput);
    this.captureInitialFormState();
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
  close({ force = false } = {}) {
    if (!force) {
      if (this.isSaving) {
        this.showToast('Please wait for the current save to finish.', 'info');
        return false;
      }
      if (this.hasUnsavedChanges() && !window.confirm('Discard unsaved task changes?')) {
        return false;
      }
    }

    const modal = document.getElementById('taskModal');
    if (modal) {
      modal.style.display = 'none';
      modal.setAttribute('aria-hidden', 'true');
    }
    document.body.classList.remove('task-modal-open');
    this.hideProgress();
    this.isSaving = false;
    this.editingTaskId = null;
    this.workspaceId = null;
    this.onSaveCallback = null;
    this.initialFormState = null;
    this.currentTask = null;
    this.resetResultSection();
    this.restoreFocus();
    return true;
  }

  getResultSectionElements() {
    return {
      section: document.getElementById('taskModalResultSection'),
      meta: document.getElementById('taskModalResultMeta'),
      body: document.getElementById('taskModalResultBody'),
      toolSummary: document.getElementById('taskModalToolSummary'),
      nextSteps: document.getElementById('taskModalResultNextSteps'),
      nextStepsCopy: document.getElementById('taskModalResultNextStepsCopy'),
      nextStepsActions: document.getElementById('taskModalResultNextStepsActions'),
      mcpRequirement: document.getElementById('taskModalMCPRequirement'),
      mcpRequirementServer: document.getElementById('taskModalMCPRequirementServer'),
      mcpRequirementBody: document.getElementById('taskModalMCPRequirementBody'),
      mcpRequirementStatus: document.getElementById('taskModalMCPRequirementStatus'),
      mcpRequirementAdd: document.getElementById('taskModalMCPRequirementAdd')
    };
  }

  resetResultSection() {
    const elements = this.getResultSectionElements();
    this.currentResultText = '';
    this.currentResultSourceTaskId = '';
    this.currentResultNextSteps = [];
    this.currentResultFollowUpPending = false;
    this.currentMissingMCPRequirement = null;
    this.mcpRequirementPending = false;
    if (elements.section) elements.section.style.display = 'none';
    if (elements.meta) elements.meta.textContent = '';
    if (elements.body) elements.body.textContent = '';
    if (elements.toolSummary) {
      elements.toolSummary.style.display = 'none';
      elements.toolSummary.innerHTML = '';
    }
    if (elements.nextSteps) elements.nextSteps.style.display = 'none';
    if (elements.nextStepsCopy) elements.nextStepsCopy.textContent = '';
    if (elements.nextStepsActions) elements.nextStepsActions.innerHTML = '';
    if (elements.mcpRequirement) elements.mcpRequirement.style.display = 'none';
    if (elements.mcpRequirementServer) elements.mcpRequirementServer.textContent = '';
    if (elements.mcpRequirementBody) elements.mcpRequirementBody.textContent = '';
    if (elements.mcpRequirementStatus) {
      elements.mcpRequirementStatus.textContent = '';
      elements.mcpRequirementStatus.style.display = 'none';
    }
    if (elements.mcpRequirementAdd) {
      elements.mcpRequirementAdd.disabled = false;
      elements.mcpRequirementAdd.textContent = 'Add Connector';
    }
  }

  escapeHtml(value) {
    return String(value ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  truncateToolSummaryText(value, maxLength = 180) {
    const text = String(value || '').replace(/\s+/g, ' ').trim();
    if (!text) return '';
    return text.length > maxLength ? `${text.slice(0, maxLength).trim()}...` : text;
  }

  extractToolNameFromTraceEntry(entry) {
    const title = String(entry?.title || '').trim();
    const titleMatch = title.match(/^(?:Calling|Completed|Failed)\s+(.+)$/i);
    if (titleMatch?.[1]) {
      return titleMatch[1].trim();
    }

    const summary = String(entry?.summary || '').trim();
    const summaryMatch = summary.match(/^(?:Calling|Completed|Failed)\s+([^\n]+)/i);
    if (summaryMatch?.[1]) {
      return summaryMatch[1].trim();
    }

    return '';
  }

  getToolKindLabel(toolName, entry) {
    const source = String(entry?.source || '').trim().toLowerCase();
    const name = String(toolName || '').trim().toLowerCase();
    if (source.includes('mcp') || name.startsWith('mcp.') || name.startsWith('mcp:')) {
      return 'MCP';
    }
    const nativeTools = new Set(['web_search', 'web_fetch', 'weather', 'time', 'finance', 'sports', 'air_quality']);
    if (nativeTools.has(name)) {
      return 'Native';
    }
    return 'Tool';
  }

  summarizeToolArguments(detail) {
    const raw = String(detail || '').trim();
    if (!raw) return '';
    try {
      const parsed = JSON.parse(raw);
      const keys = ['query', 'url', 'location', 'ticker', 'city'];
      for (const key of keys) {
        if (parsed && Object.prototype.hasOwnProperty.call(parsed, key)) {
          return `${key}: ${this.truncateToolSummaryText(parsed[key], 140)}`;
        }
      }
    } catch (_error) {
      // Detail is often already a readable string.
    }
    return this.truncateToolSummaryText(raw, 160);
  }

  summarizeToolResult(summary) {
    return this.truncateToolSummaryText(summary, 180);
  }

  getTaskToolUsage(task) {
    const trace = Array.isArray(task?.execution_trace) ? task.execution_trace : [];
    const byName = new Map();

    trace.forEach((entry) => {
      const rawStatus = String(entry?.status || entry?.type || '').trim().toLowerCase();
      const rawTitle = String(entry?.title || '').trim().toLowerCase();
      if (!rawStatus.includes('tool') &&
          !rawTitle.startsWith('calling ') &&
          !rawTitle.startsWith('completed ') &&
          !rawTitle.startsWith('failed ')) {
        return;
      }

      const toolName = this.extractToolNameFromTraceEntry(entry);
      if (!toolName) return;

      if (!byName.has(toolName)) {
        byName.set(toolName, {
          name: toolName,
          kind: this.getToolKindLabel(toolName, entry),
          calls: 0,
          results: 0,
          errors: 0,
          latestArgs: '',
          latestResult: ''
        });
      }

      const item = byName.get(toolName);
      if (rawStatus.includes('call') || rawTitle.startsWith('calling ')) {
        item.calls += 1;
        item.latestArgs = this.summarizeToolArguments(entry?.detail);
      } else if (rawStatus.includes('error') || rawTitle.startsWith('failed ')) {
        item.errors += 1;
        item.latestResult = this.summarizeToolResult(entry?.summary);
      } else if (rawStatus.includes('result') || rawTitle.startsWith('completed ')) {
        item.results += 1;
        item.latestResult = this.summarizeToolResult(entry?.summary);
      }
    });

    return Array.from(byName.values());
  }

  renderTaskToolSummary(task) {
    const elements = this.getResultSectionElements();
    if (!elements.toolSummary) return;

    const tools = this.getTaskToolUsage(task);
    if (tools.length === 0) {
      elements.toolSummary.style.display = 'none';
      elements.toolSummary.innerHTML = '';
      return;
    }

    const totalCalls = tools.reduce((sum, item) => sum + Math.max(item.calls, item.results + item.errors), 0);
    const cards = tools.map((item) => {
      const status = item.errors > 0 ? 'Error' : (item.results > 0 ? 'Completed' : 'Called');
      const callCount = Math.max(item.calls, item.results + item.errors);
      const count = callCount === 1 ? '1 call' : `${callCount} calls`;
      const detail = item.latestArgs || item.latestResult || '';
      return `
        <div style="display: grid; gap: 4px; padding: 9px 10px; border: 1px solid color-mix(in srgb, var(--border-color) 80%, transparent); border-radius: 9px; background: color-mix(in srgb, var(--bg-secondary) 84%, transparent);">
          <div style="display: flex; align-items: center; justify-content: space-between; gap: 8px;">
            <div style="min-width: 0; color: var(--text-primary); font-size: 0.84rem; font-weight: 700; overflow-wrap: anywhere;">${this.escapeHtml(item.name)}</div>
            <div style="flex: 0 0 auto; padding: 2px 7px; border-radius: 999px; border: 1px solid color-mix(in srgb, var(--primary-color) 40%, transparent); background: color-mix(in srgb, var(--primary-color) 13%, transparent); color: var(--primary-color); font-size: 0.66rem; font-weight: 800; letter-spacing: 0.04em; text-transform: uppercase;">${this.escapeHtml(item.kind)}</div>
          </div>
          <div style="font-size: 0.74rem; color: var(--text-secondary); line-height: 1.4;">${this.escapeHtml(status)} • ${this.escapeHtml(count)}</div>
          ${detail ? `<div style="font-size: 0.72rem; color: var(--text-muted); line-height: 1.4; overflow-wrap: anywhere;">${this.escapeHtml(detail)}</div>` : ''}
        </div>
      `;
    }).join('');

    elements.toolSummary.style.display = 'grid';
    elements.toolSummary.innerHTML = `
      <div style="display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 8px;">
        <div style="font-size: 0.7rem; font-weight: 800; letter-spacing: 0.08em; text-transform: uppercase; color: var(--text-secondary);">Tools Used</div>
        <div style="font-size: 0.72rem; color: var(--text-muted);">${this.escapeHtml(tools.length === 1 ? '1 tool' : `${tools.length} tools`)} • ${this.escapeHtml(totalCalls === 1 ? '1 call' : `${totalCalls} calls`)}</div>
      </div>
      <div style="display: grid; gap: 8px; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));">${cards}</div>
    `;
  }

  getTaskDiagnosticText(task, resultText = '') {
    const parts = [resultText, task?.error, task?.last_error];

    const addValue = (value) => {
      const text = String(value || '').trim();
      if (text) parts.push(text);
    };

    if (Array.isArray(task?.execution_trace)) {
      task.execution_trace.forEach((entry) => {
        addValue(entry?.summary);
        addValue(entry?.title);
      });
    }

    if (Array.isArray(task?.execution_history)) {
      task.execution_history.forEach((entry) => {
        addValue(entry?.error);
        addValue(entry?.summary);
      });
    }

    const humanLoop = task?.context?.human_loop;
    if (humanLoop && typeof humanLoop === 'object') {
      addValue(humanLoop.agent_response);
      addValue(humanLoop.reason);
    }

    const retry = task?.context?.execution_retry;
    if (retry && typeof retry === 'object' && Array.isArray(retry.history)) {
      retry.history.forEach((entry) => addValue(entry?.summary));
    }

    return parts.filter(Boolean).join('\n');
  }

  detectMissingMCPRequirement(task, resultText = '') {
    const diagnostic = this.getTaskDiagnosticText(task, resultText);
    if (!diagnostic || !/load\s+MCP\s+template/i.test(diagnostic)) {
      return null;
    }

    const directMatch = diagnostic.match(/load\s+MCP\s+template\s+([^\s:]+)\s+for\s+binding\s+([^:\s]+):\s+server\s+([^\s:]+)\s+not\s+found/i);
    const fallbackMatch = diagnostic.match(/load\s+MCP\s+template\s+([^\s:]+).*?server\s+([^\s:]+)\s+not\s+found/i);
    const serverName = String(directMatch?.[1] || fallbackMatch?.[1] || fallbackMatch?.[2] || '').trim();
    if (!serverName || !/^[a-zA-Z0-9._-]+$/.test(serverName)) {
      return null;
    }

    return {
      serverName,
      bindingId: String(directMatch?.[2] || '').trim()
    };
  }

  renderMCPRequirement(task, resultText = '') {
    const elements = this.getResultSectionElements();
    if (!elements.mcpRequirement) return;

    const requirement = this.detectMissingMCPRequirement(task, resultText);
    this.currentMissingMCPRequirement = requirement;

    if (!requirement) {
      elements.mcpRequirement.style.display = 'none';
      return;
    }

    const serverName = requirement.serverName;
    elements.mcpRequirement.style.display = 'grid';
    if (elements.mcpRequirementServer) {
      elements.mcpRequirementServer.textContent = serverName;
    }
    if (elements.mcpRequirementBody) {
      elements.mcpRequirementBody.textContent = requirement.bindingId
        ? `Workspace binding ${requirement.bindingId} points to ${serverName}, but My Servers does not have that connector yet. Add it from the MCP registry, then retry the task.`
        : `The workspace points to ${serverName}, but My Servers does not have that connector yet. Add it from the MCP registry, then retry the task.`;
    }
    if (elements.mcpRequirementStatus) {
      elements.mcpRequirementStatus.textContent = '';
      elements.mcpRequirementStatus.style.display = 'none';
    }
    if (elements.mcpRequirementAdd) {
      elements.mcpRequirementAdd.disabled = false;
      elements.mcpRequirementAdd.textContent = `Add ${serverName}`;
    }
  }

  setMCPRequirementStatus(message) {
    const status = document.getElementById('taskModalMCPRequirementStatus');
    if (!status) return;
    const text = String(message || '').trim();
    status.textContent = text;
    status.style.display = text ? 'block' : 'none';
  }

  async getConfiguredMCPServers() {
    const response = await fetch('/api/mcp/servers');
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to load MCP servers');
    }
    const data = await response.json();
    return Array.isArray(data?.servers) ? data.servers : [];
  }

  fallbackRegistryEntryForMCPServer(serverName) {
    const normalized = String(serverName || '').trim().toLowerCase();
    if (normalized !== 'fetch') {
      return null;
    }
    return {
      name: 'fetch',
      command: 'uvx',
      args: ['mcp-server-fetch'],
      env: {},
      transport: 'stdio'
    };
  }

  async findRegistryMCPServer(serverName) {
    const normalized = String(serverName || '').trim().toLowerCase();
    if (!normalized) return null;

    try {
      const response = await fetch(`/api/mcp/search?q=${encodeURIComponent(normalized)}`);
      if (response.ok) {
        const entries = await response.json();
        if (Array.isArray(entries)) {
          const exact = entries.find((entry) => String(entry?.name || '').trim().toLowerCase() === normalized);
          if (exact) return exact;
        }
      }
    } catch (error) {
      console.warn('Failed to search MCP registry:', error);
    }

    return this.fallbackRegistryEntryForMCPServer(normalized);
  }

  buildMCPServerConfigFromRegistry(entry, serverName) {
    const name = String(entry?.name || serverName || '').trim();
    const command = String(entry?.command || '').trim();
    if (!name || !command) {
      return null;
    }

    const env = entry?.env && typeof entry.env === 'object' && !Array.isArray(entry.env)
      ? { ...entry.env }
      : {};

    return {
      name,
      command,
      args: Array.isArray(entry?.args) ? entry.args : [],
      env,
      transport: String(entry?.transport || 'stdio').trim() || 'stdio',
      enabled: false
    };
  }

  async addRequiredMCPConnector() {
    const requirement = this.currentMissingMCPRequirement;
    if (!requirement?.serverName || this.mcpRequirementPending) return;

    const serverName = requirement.serverName;
    const button = document.getElementById('taskModalMCPRequirementAdd');
    const originalText = button?.textContent || `Add ${serverName}`;
    this.mcpRequirementPending = true;
    if (button) {
      button.disabled = true;
      button.textContent = `Adding ${serverName}...`;
    }

    try {
      this.setMCPRequirementStatus('Checking My Servers...');
      const configuredServers = await this.getConfiguredMCPServers();
      const alreadyConfigured = configuredServers.some((server) =>
        String(server?.name || '').trim().toLowerCase() === serverName.toLowerCase()
      );
      if (alreadyConfigured) {
        this.setMCPRequirementStatus(`${serverName} is already in My Servers. Retry the task when ready.`);
        if (button) button.textContent = 'Connector available';
        this.showToast(`${serverName} is already available`, 'info');
        return;
      }

      this.setMCPRequirementStatus(`Searching the MCP registry for ${serverName}...`);
      const entry = await this.findRegistryMCPServer(serverName);
      const serverConfig = this.buildMCPServerConfigFromRegistry(entry, serverName);
      if (!serverConfig) {
        throw new Error(`${serverName} was not found in the MCP registry. Add it from MCP settings.`);
      }

      this.setMCPRequirementStatus(`Adding ${serverName} to My Servers...`);
      const response = await fetch('/api/mcp/servers', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(serverConfig)
      });
      if (!response.ok) {
        const text = await response.text();
        if (!/already exists/i.test(text || '')) {
          throw new Error(text || `Failed to add ${serverName}`);
        }
      }

      this.setMCPRequirementStatus(`${serverName} has been added to My Servers. Retry the task to use it.`);
      if (button) button.textContent = 'Connector added';
      this.showToast(`${serverName} connector added`, 'success');
    } catch (error) {
      console.error('Failed to add required MCP connector:', error);
      this.setMCPRequirementStatus(error.message || `Failed to add ${serverName}.`);
      this.showToast(error.message || `Failed to add ${serverName}`, 'error');
      if (button) {
        button.disabled = false;
        button.textContent = originalText;
      }
    } finally {
      this.mcpRequirementPending = false;
    }
  }

  getTaskStatusLabel(status) {
    const normalized = String(status || '').trim().toLowerCase();
    const labels = {
      pending: 'Pending',
      in_progress: 'In Progress',
      waiting_for_choice: 'Waiting for Input',
      completed: 'Completed',
      failed: 'Failed',
      blocked: 'Blocked',
      cancelled: 'Cancelled',
      timeout: 'Timed Out'
    };
    return labels[normalized] || normalized || 'Task';
  }

  resolveTaskResultData(task) {
    if (!task || typeof task !== 'object') return null;

    if (window.workspaceDetail &&
        String(window.workspaceDetail.workspaceId || '').trim() === String(this.workspaceId || '').trim() &&
        typeof window.workspaceDetail.getSubtasksForParent === 'function' &&
        typeof window.workspaceDetail.getDisplayResult === 'function') {
      const subtasks = window.workspaceDetail.getSubtasksForParent(task.id);
      const resultData = window.workspaceDetail.getDisplayResult(task, subtasks);
      if (resultData && resultData.text) {
        return resultData;
      }
    }

    if (task.error) {
      return {
        label: 'Error',
        text: String(task.error),
        sourceTask: task,
        answeredBy: String(task.to || task.from || 'Unknown agent').trim()
      };
    }
    if (task.result) {
      return {
        label: 'Result',
        text: String(task.result),
        sourceTask: task,
        answeredBy: String(task.to || task.from || 'Unknown agent').trim()
      };
    }

    const humanLoop = task?.context?.human_loop;
    const agentResponse = humanLoop && typeof humanLoop === 'object'
      ? String(humanLoop.agent_response || '').trim()
      : '';
    if (agentResponse) {
      return {
        label: 'Agent Output',
        text: agentResponse,
        sourceTask: task,
        answeredBy: String(task.to || task.from || 'Unknown agent').trim()
      };
    }

    return null;
  }

  cleanResultNextStepText(value) {
    return String(value || '')
      .replace(/\[(.*?)\]\((.*?)\)/g, '$1')
      .replace(/[*_`#>]+/g, '')
      .replace(/\s+/g, ' ')
      .trim();
  }

  normalizeResultNextStepToken(value) {
    return this.cleanResultNextStepText(value)
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, ' ')
      .trim();
  }

  buildResultNextStepId(number, label) {
    const base = this.normalizeResultNextStepToken(label)
      .replace(/\s+/g, '-')
      .slice(0, 48) || 'next-step';
    return `task-result-step-${String(number || '').trim() || 'x'}-${base}`;
  }

  extractResultNextSteps(text) {
    const lines = String(text || '').split(/\r?\n/);
    const cues = ['next steps', 'next step', 'would you like me to', 'let me know', 'next steps for you'];
    let cueIndex = -1;

    for (let i = 0; i < lines.length; i += 1) {
      const normalized = this.normalizeResultNextStepToken(lines[i]);
      if (cues.some((cue) => normalized.includes(cue))) {
        cueIndex = i;
        break;
      }
    }

    if (cueIndex === -1) return [];

    const choices = [];
    let started = false;
    for (let i = cueIndex + 1; i < lines.length; i += 1) {
      const rawLine = String(lines[i] || '');
      const match = rawLine.match(/^\s*(\d+)[.)]\s*(.+)$/);
      if (match) {
        const number = String(match[1] || '').trim();
        const label = this.cleanResultNextStepText(match[2]);
        if (!label) continue;
        choices.push({
          id: this.buildResultNextStepId(number, label),
          number,
          label
        });
        started = true;
        if (choices.length >= 5) break;
        continue;
      }

      if (!started) continue;
      if (!rawLine.trim()) continue;
      break;
    }

    return choices;
  }

  renderResultSection(task) {
    const elements = this.getResultSectionElements();
    if (!elements.section || !task) return;

    const resultData = this.resolveTaskResultData(task);
    const status = String(task.status || '').trim().toLowerCase();
    if (!resultData?.text || (status !== 'completed' && status !== 'failed' && status !== 'blocked' && status !== 'timeout' && status !== 'waiting_for_choice')) {
      this.resetResultSection();
      return;
    }

    this.currentResultText = String(resultData.text || '').trim();
    this.currentResultSourceTaskId = String(resultData.sourceTask?.id || task.id || '').trim();
    this.currentResultNextSteps = this.extractResultNextSteps(this.currentResultText);

    elements.section.style.display = 'block';
    if (elements.meta) {
      const answeredBy = String(resultData.answeredBy || task.to || 'Unknown agent').trim() || 'Unknown agent';
      elements.meta.textContent = `${resultData.label} • Answered by ${answeredBy} • ${this.getTaskStatusLabel(task.status)}`;
    }
    if (elements.body) {
      elements.body.textContent = this.currentResultText;
    }

    this.renderMCPRequirement(task, this.currentResultText);
    this.renderTaskToolSummary(task);
    this.renderResultNextStepActions(task);
  }

  renderResultNextStepActions(/* task */) {
    const elements = this.getResultSectionElements();
    if (!elements.nextSteps || !elements.nextStepsCopy || !elements.nextStepsActions) return;

    if (!Array.isArray(this.currentResultNextSteps) || this.currentResultNextSteps.length === 0) {
      elements.nextSteps.style.display = 'none';
      elements.nextStepsCopy.textContent = '';
      elements.nextStepsActions.innerHTML = '';
      return;
    }

    elements.nextSteps.style.display = 'block';
    elements.nextStepsCopy.textContent = 'Choose the next step to create and run a follow-up task linked to this result.';
    elements.nextStepsActions.innerHTML = this.currentResultNextSteps.map((step) => {
      const buttonLabel = this.currentResultFollowUpPending
        ? 'Creating follow-up task...'
        : 'Create follow-up task';
      return `
        <button type="button"
                class="task-modal-btn task-modal-btn-secondary"
                data-task-result-next-step-id="${step.id}"
                style="display: flex; width: 100%; align-items: flex-start; gap: 12px; text-align: left; justify-content: flex-start; padding: 12px 14px; border-radius: 12px;"
                ${this.currentResultFollowUpPending ? 'disabled' : ''}>
          <span style="width: 24px; height: 24px; border-radius: 999px; display: inline-flex; align-items: center; justify-content: center; background: rgba(var(--primary-color-rgb, 255, 138, 86), 0.16); color: var(--primary-color); font-size: 0.72rem; font-weight: 700; flex-shrink: 0; margin-top: 2px;">${step.number || '•'}</span>
          <span style="display: grid; gap: 3px; min-width: 0; flex: 1;">
            <span style="font-size: 0.84rem; font-weight: 600; color: var(--text-primary); white-space: normal; line-height: 1.4;">${step.label}</span>
            <span style="font-size: 0.74rem; color: var(--text-secondary); line-height: 1.4;">${buttonLabel}</span>
          </span>
        </button>
      `;
    }).join('');

    elements.nextStepsActions.querySelectorAll('[data-task-result-next-step-id]').forEach((button) => {
      button.addEventListener('click', () => {
        const nextStepId = String(button.getAttribute('data-task-result-next-step-id') || '').trim();
        if (!nextStepId) return;
        void this.continueFromResult(nextStepId);
      });
    });
  }

  buildFollowUpTitle(task, step) {
    const baseTitle = String(task?.description || task?.name || 'Follow-up task').trim() || 'Follow-up task';
    const stepLabel = this.cleanResultNextStepText(step?.label || '');
    if (!stepLabel) return baseTitle;
    const combined = `${baseTitle} - ${stepLabel}`;
    return combined.length > 160 ? `${combined.slice(0, 157).trim()}...` : combined;
  }

  buildFollowUpDetails(task, sourceTaskId, step) {
    const parts = [];
    const baseTitle = String(task?.description || task?.name || task?.id || 'Completed task').trim();
    const stepLabel = this.cleanResultNextStepText(step?.label || '');
    const stepNumber = String(step?.number || '').trim();
    parts.push(`Follow-up created from completed task: ${baseTitle}`);
    if (stepLabel) {
      parts.push(`Selected next step: ${stepNumber ? `${stepNumber}. ` : ''}${stepLabel}`);
    }
    if (sourceTaskId) {
      parts.push(`Linked input task: ${sourceTaskId}`);
    }
    parts.push('Use the linked task result as the starting context and continue the work from there.');
    return parts.join('\n');
  }

  async createAndRunFollowUpTask(payload) {
    const response = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to create follow-up task');
    }

    const data = await response.json();
    const createdTask = data.task || data;
    if (!createdTask?.id) {
      throw new Error('Follow-up task was created without an id');
    }

    if (typeof this.onSaveCallback === 'function') {
      await this.onSaveCallback();
    }

    if (window.workspaceDetail && typeof window.workspaceDetail.executeTask === 'function') {
      await window.workspaceDetail.executeTask(createdTask.id, { skipConfirm: true });
      return createdTask;
    }

    const executeResponse = await fetch('/api/orchestration/tasks/execute', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ task_id: createdTask.id })
    });
    if (!executeResponse.ok) {
      const text = await executeResponse.text();
      throw new Error(text || 'Failed to start follow-up task');
    }

    return createdTask;
  }

  async continueFromResult(nextStepId) {
    if (this.currentResultFollowUpPending || !this.currentTask) return;

    const task = this.currentTask;
    const nextStep = this.currentResultNextSteps.find((item) => item.id === nextStepId);
    if (!nextStep) return;

    this.currentResultFollowUpPending = true;
    this.renderResultNextStepActions(task);

    try {
      const payload = {
        workspace_id: this.workspaceId,
        description: this.buildFollowUpTitle(task, nextStep),
        details: this.buildFollowUpDetails(task, this.currentResultSourceTaskId || task.id, nextStep),
        to: String(task.to || '').trim() || undefined,
        assigned_node_id: String(task.assigned_node_id || '').trim() || undefined,
        input_task_ids: [this.currentResultSourceTaskId || task.id].filter(Boolean)
      };

      await this.createAndRunFollowUpTask(payload);
      this.showToast('Follow-up task created', 'success');
      this.close({ force: true });
    } catch (error) {
      console.error('Failed to continue from task modal result:', error);
      this.showToast(error.message || 'Failed to continue task', 'error');
    } finally {
      this.currentResultFollowUpPending = false;
      if (this.currentTask) {
        this.renderResultNextStepActions(this.currentTask);
      }
    }
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

    const saveButton = document.getElementById('taskModalSave');
    const saveText = document.getElementById('taskModalSaveText');
    const originalText = saveText?.textContent;
    this.isSaving = true;
    // Reset for this save attempt; _safeAttachUploads pushes per-failure
    // entries during the run, and the success path drains them into a
    // warning toast so partial failures stay visible to the user.
    this._uploadFailures = [];
    if (saveButton) {
      saveButton.disabled = true;
      saveButton.classList.add('is-saving');
    }
    if (saveText) saveText.textContent = this.editingTaskId ? 'Saving...' : 'Creating...';

    try {
      let eventName = this.editingTaskId ? 'task:updated' : 'task:created';
      let eventPayload = {};

      // Get schedule data
      const scheduleData = this.getScheduleData();

      // Get auto-save data
      const autoSaveData = this.getAutoSaveData();
      if (autoSaveData.output_contract_error) {
        this.showToast(autoSaveData.output_contract_error, 'error');
        const firstNameInput = document.querySelector('#taskModalOutputContractRows [data-output-contract-name]');
        firstNameInput?.focus();
        return;
      }

      const createTask = async (payload) => {
        const response = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        });

        if (!response.ok) {
          throw await this._parseTaskApiError(response, 'Failed to create task');
        }

        const createdPayload = await response.json();
        return this.extractTaskFromResponse(createdPayload);
      };

      const createWorkflow = async (parentPayload, subtaskPayloads) => {
        const response = await fetch('/api/orchestration/workflows', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            workspace_id: this.workspaceId,
            parent: parentPayload,
            subtasks: subtaskPayloads
          })
        });

        if (!response.ok) {
          throw await this._parseTaskApiError(response, 'Failed to create workflow');
        }

        return response.json();
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
        const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}&workspace_id=${encodeURIComponent(this.workspaceId)}`, {
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

          await this._safeAttachUploads(this.editingTaskId);

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
            result_storage: null,
            output_contract: { columns: [] }
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
                workspace_id: this.workspaceId,
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
          await this._safeAttachUploads(attachmentTarget);

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
          await this._safeAttachUploads(this.editingTaskId);

          this.showToast('Task updated', 'success');
        }
      } else if (subtasks.length > 0) {
        // Atomic workflow create. Pre-generate UUIDs for parent + every
        // subtask so we can wire `input_task_ids` between siblings up
        // front and post the whole DAG to /api/orchestration/workflows in
        // a single call. The server validates the graph as one batch and
        // rolls back the entire workspace state on any cycle / unknown
        // ref / duplicate ID — no half-created workflow if validation
        // trips on subtask 5.
        const parentId = this._generateTaskId();
        const subtaskIds = subtasks.map((subtask) => subtask.id || this._generateTaskId());
        // Stamp the IDs back onto the in-memory subtask list and the DOM
        // cards so per-subtask error highlighting can map server issues
        // (`issue.task_id`) onto the correct row.
        this._stampSubtaskIds(subtaskIds);

        const subtaskPayloads = subtasks.map((subtask, i) => ({
          id: subtaskIds[i],
          description: subtask.description,
          details: subtask.details,
          priority,
          to: subtask.to || '',
          assigned_node_id: subtask.assigned_node_id || '',
          input_task_ids: this.resolveInputRefs(subtask.input_task_ids, subtaskIds),
          subtask_index: i + 1,
          ...this.getWorkflowAutoSavePayload(autoSaveData, i, subtasks.length, false)
        }));

        await createWorkflow(
          {
            id: parentId,
            description,
            details,
            priority,
            to: to || '',
            assigned_node_id: assignedNodeId || ''
          },
          subtaskPayloads
        );

        // Schedule + auto-save fields the bulk endpoint does not yet model
        // are still applied per-subtask via the existing /tasks PUT path.
        // This keeps the bulk contract small while preserving the modal's
        // existing per-step schedule semantics.
        for (let i = 0; i < subtasks.length; i++) {
          const schedulePayload = this.getWorkflowSchedulePayload(scheduleData, i, subtasks.length, false);
          const autoSavePayload = this.getWorkflowAutoSavePayload(autoSaveData, i, subtasks.length, false);
          const extras = { ...schedulePayload, ...autoSavePayload };
          if (Object.keys(extras).length === 0) continue;
          await updateTask(subtaskIds[i], extras);
        }

        const lastSubtaskId = subtaskIds[subtaskIds.length - 1];
        await this._safeAttachUploads(lastSubtaskId);

        this.showToast('Workflow created', 'success');
      } else {
        const createdTask = await createTask({
          workspace_id: this.workspaceId,
          description,
          details,
          priority,
          to: to || undefined,
          assigned_node_id: assignedNodeId || undefined,
          input_task_ids: mainInputIds,
          ...scheduleData,
          ...autoSaveData
        });

        await this._safeAttachUploads(createdTask?.id);

        eventName = 'task:created';
        eventPayload = { task: createdTask };
        this.showToast('Task created', 'success');
      }

      // Surface partial-success warning before closing the modal — the
      // task itself was saved, but one or more attachment uploads failed.
      // Without this users would only see the success toast and not
      // realize their files weren't attached.
      this._reportUploadFailures();

      // Clear pending files
      this.pendingFiles = [];
      this.pendingFilePaths = [];
      this._clearSubtaskGraphErrors();
      await this.finalizeSuccessfulSave(eventName, eventPayload);
    } catch (error) {
      console.error('Failed to save task:', error);
      const issues = Array.isArray(error?.issues) ? error.issues : [];
      if (issues.length > 0) {
        this._applySubtaskGraphErrors(issues);
        const summary = issues[0]?.message || error.message || 'Workflow has invalid dependencies';
        this.showToast(summary, 'error');
      } else {
        this.showToast(error.message || 'Failed to save task', 'error');
      }
    } finally {
      this.isSaving = false;
      if (saveButton) {
        saveButton.disabled = false;
        saveButton.classList.remove('is-saving');
      }
      if (saveText) saveText.textContent = originalText || 'Save Task';
    }
  }

  /**
   * Run pendingFiles + pendingFilePaths uploads against the given task ID
   * without letting failures abort the surrounding save flow. The task
   * itself is already persisted by the time we get here, so an upload
   * failure must not roll back a successful creation — instead we collect
   * the errors so the final toast can warn the user about partial
   * success ("Task created — 2 attachment(s) failed").
   *
   * Reports through `this._uploadFailures` which is initialized to []
   * before each save and inspected after the try block.
   */
  async _safeAttachUploads(taskId) {
    if (!taskId) return;
    if (!Array.isArray(this._uploadFailures)) this._uploadFailures = [];

    if (this.pendingFiles.length > 0) {
      try {
        await this.uploadFilesToTask(taskId);
      } catch (err) {
        this._uploadFailures.push({
          kind: 'file',
          count: this.pendingFiles.length,
          message: err?.message || 'Failed to upload files'
        });
      }
    }
    if (this.pendingFilePaths.length > 0) {
      try {
        await this.saveFilePathsToTask(taskId);
      } catch (err) {
        this._uploadFailures.push({
          kind: 'path',
          count: this.pendingFilePaths.length,
          message: err?.message || 'Failed to attach file paths'
        });
      }
    }
  }

  /**
   * Render a single warning toast summarizing any attachment upload
   * failures collected by _safeAttachUploads. The task itself was
   * already persisted by this point so this is strictly an extra
   * signal — the success toast still fires alongside.
   */
  _reportUploadFailures() {
    const failures = Array.isArray(this._uploadFailures) ? this._uploadFailures : [];
    if (failures.length === 0) return;

    const counts = failures.reduce((sum, entry) => sum + (Number(entry?.count) || 0), 0);
    const firstMessage = failures[0]?.message || '';
    const summary = counts > 0
      ? `${counts} attachment${counts === 1 ? '' : 's'} failed to upload — task saved without them.${firstMessage ? ` ${firstMessage}` : ''}`
      : `Some attachments failed to upload — task saved without them.${firstMessage ? ` ${firstMessage}` : ''}`;
    this.showToast(summary, 'warning');
    this._uploadFailures = [];
  }

  /**
   * Generate a UUID for client-side task IDs. Falls back to a non-RFC
   * pseudo-UUID on browsers without crypto.randomUUID() — good enough for
   * the in-flight workflow create contract (server validates uniqueness).
   */
  _generateTaskId() {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
      return crypto.randomUUID();
    }
    const part = () => Math.floor(Math.random() * 0xffffffff).toString(16).padStart(8, '0');
    return `${part()}-${part().slice(0, 4)}-${part().slice(0, 4)}-${part().slice(0, 4)}-${part()}${part().slice(0, 4)}`;
  }

  /**
   * Stamp the pre-generated subtask IDs onto the subtask card DOM so a
   * later validation error from the server can be mapped back to the
   * specific row that produced it (highlight + inline message).
   */
  _stampSubtaskIds(ids) {
    const cards = document.querySelectorAll('#taskModalSubtaskList .task-modal-subtask-card');
    cards.forEach((card, i) => {
      if (!ids[i]) return;
      card.dataset.subtaskId = ids[i];
    });
  }

  /**
   * Read a non-2xx response from the task/workflow API and synthesize an
   * Error whose `.issues` carries the structured graph-validation
   * payload when the server provided one. Falls back to the response's
   * text for older / non-JSON error paths.
   */
  async _parseTaskApiError(response, fallbackMessage) {
    let bodyText = '';
    try {
      bodyText = await response.text();
    } catch (_e) {
      bodyText = '';
    }
    let parsed = null;
    if (bodyText) {
      try { parsed = JSON.parse(bodyText); } catch (_e) { parsed = null; }
    }
    const message = (parsed && (parsed.error || parsed.message)) || bodyText || fallbackMessage;
    const error = new Error(message || fallbackMessage);
    if (parsed && Array.isArray(parsed.issues)) {
      error.issues = parsed.issues;
    }
    return error;
  }

  /**
   * Apply per-row error styling + inline messages to subtask cards whose
   * IDs appear in the structured issue list. Issues that name a task ID
   * we don't have a card for fall through silently — the toast still
   * carries the human message.
   */
  _applySubtaskGraphErrors(issues) {
    this._clearSubtaskGraphErrors();
    const byId = new Map();
    document.querySelectorAll('#taskModalSubtaskList .task-modal-subtask-card').forEach((card) => {
      if (card.dataset.subtaskId) byId.set(card.dataset.subtaskId, card);
    });
    issues.forEach((issue) => {
      const card = byId.get(issue?.task_id);
      if (!card) return;
      card.classList.add('task-modal-subtask-card--has-error');
      let messageEl = card.querySelector('.task-modal-subtask-card-error');
      if (!messageEl) {
        messageEl = document.createElement('div');
        messageEl.className = 'task-modal-subtask-card-error';
        card.appendChild(messageEl);
      }
      const existing = messageEl.textContent ? `${messageEl.textContent} ` : '';
      messageEl.textContent = existing + (issue?.message || 'Invalid dependency');
    });
  }

  _clearSubtaskGraphErrors() {
    document.querySelectorAll('#taskModalSubtaskList .task-modal-subtask-card--has-error').forEach((card) => {
      card.classList.remove('task-modal-subtask-card--has-error');
    });
    document.querySelectorAll('#taskModalSubtaskList .task-modal-subtask-card-error').forEach((el) => {
      el.remove();
    });
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
    this.isSaving = true;
    this._uploadFailures = [];
    if (saveButton) {
      saveButton.disabled = true;
      saveButton.classList.add('is-saving');
    }
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
            write_mode: parsed.result_storage.write_mode === 'append' ? 'append' : 'new_file',
            store_node_id: parsed.result_storage.store_node_id || undefined,
            file_path: parsed.result_storage.file_path || undefined
          }
        };
        if (resultStorageData.result_storage.write_mode === 'append') {
          const outputContract = this.normalizeOutputContractPayload(parsed.output_contract);
          if (outputContract) {
            resultStorageData.output_contract = outputContract;
          }
        }
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
            workspace_id: this.workspaceId,
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
              workspace_id: this.workspaceId,
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

        await this._safeAttachUploads(lastCreatedTaskId);
        this._reportUploadFailures();

        // Clear pending files
        this.pendingFiles = [];
        this.pendingFilePaths = [];

        this.showToast(workflowSteps.length > 1 ? 'Workflow created' : 'Task created', 'success');
        await this.finalizeSuccessfulSave('task:created');
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
          workspace_id: this.workspaceId,
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
      await this._safeAttachUploads(createdTask?.id);
      this._reportUploadFailures();

      // Clear pending files
      this.pendingFiles = [];
      this.pendingFilePaths = [];

      this.showToast('Task created', 'success');
      await this.finalizeSuccessfulSave('task:created', { task: createdTask });
    } catch (error) {
      console.error('Failed to save task in auto mode:', error);
      this.showToast(error.message || 'Failed to create task', 'error');
    } finally {
      this.isSaving = false;
      this.hideProgress();
      // Restore button state
      if (saveButton) {
        saveButton.disabled = false;
        saveButton.classList.remove('is-saving');
      }
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
    this.isSaving = true;
    this._uploadFailures = [];
    if (saveButton) {
      saveButton.disabled = true;
      saveButton.classList.add('is-saving');
    }
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
            result_storage: this.currentTask.result_storage || null,
            output_contract: this.currentTask.output_contract || null
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
            write_mode: parsed.result_storage.write_mode === 'append' ? 'append' : 'new_file',
            store_node_id: parsed.result_storage.store_node_id || undefined,
            file_path: parsed.result_storage.file_path || undefined
          }
        };
        if (resultStorageData.result_storage.write_mode === 'append') {
          const outputContract = this.normalizeOutputContractPayload(parsed.output_contract);
          if (outputContract) {
            resultStorageData.output_contract = outputContract;
          }
        }
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
      await this._safeAttachUploads(this.editingTaskId);
      this._reportUploadFailures();

      // Clear pending files
      this.pendingFiles = [];
      this.pendingFilePaths = [];

      this.showToast('Task updated', 'success');
      await this.finalizeSuccessfulSave('task:updated');
    } catch (error) {
      console.error('Failed to update task in auto mode:', error);
      this.showToast(error.message || 'Failed to update task', 'error');
    } finally {
      this.isSaving = false;
      this.hideProgress();
      // Restore button state
      if (saveButton) {
        saveButton.disabled = false;
        saveButton.classList.remove('is-saving');
      }
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
    const cronField = document.getElementById('taskModalScheduleCronField');

    // Hide all
    if (timeField) timeField.style.display = 'none';
    if (dayField) dayField.style.display = 'none';
    if (intervalField) intervalField.style.display = 'none';
    if (onceField) onceField.style.display = 'none';
    if (cronField) cronField.style.display = 'none';

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
      case 'cron':
        if (cronField) cronField.style.display = 'block';
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
    const scheduleIntervalValue = document.getElementById('taskModalScheduleIntervalValue');
    const scheduleIntervalUnit = document.getElementById('taskModalScheduleIntervalUnit');
    const scheduleDatetime = document.getElementById('taskModalScheduleDatetime');
    const scheduleCron = document.getElementById('taskModalScheduleCron');

    if (enabledCheckbox) enabledCheckbox.checked = false;
    if (scheduleFields) scheduleFields.style.display = 'none';
    if (scheduleName) scheduleName.value = '';
    if (scheduleType) scheduleType.value = 'interval';
    if (scheduleTime) scheduleTime.value = '09:00';
    this.setSelectedScheduleDays([]);
    if (scheduleIntervalValue) scheduleIntervalValue.value = '1';
    if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
    if (scheduleDatetime) scheduleDatetime.value = '';
    if (scheduleCron) scheduleCron.value = '0 9 * * *';

    this.updateScheduleTypeFields();
    this.updateSchedulePreview();
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
    const scheduleIntervalValue = document.getElementById('taskModalScheduleIntervalValue');
    const scheduleIntervalUnit = document.getElementById('taskModalScheduleIntervalUnit');
    const scheduleDatetime = document.getElementById('taskModalScheduleDatetime');
    const scheduleCron = document.getElementById('taskModalScheduleCron');

    if (task.schedule) {
      if (enabledCheckbox) enabledCheckbox.checked = Boolean(task.schedule_enabled);
      if (scheduleFields) scheduleFields.style.display = 'block';
      if (scheduleName) scheduleName.value = task.schedule_name || '';

      // Detect a multi-day weekly cron and back-convert to the weekly
      // form so the checkbox row stays the canonical UI for that case.
      const schedule = task.schedule;
      const cronWeekly = this.parseWeeklyCron(schedule.type === 'cron' ? schedule.cron_expr : '');
      const inferredType = cronWeekly ? 'weekly' : (schedule.type || 'interval');
      if (scheduleType) scheduleType.value = inferredType;

      // Populate type-specific fields
      if (cronWeekly) {
        if (scheduleTime) scheduleTime.value = cronWeekly.time;
        this.setSelectedScheduleDays(cronWeekly.days);
      } else {
        if (schedule.time && scheduleTime) scheduleTime.value = schedule.time;
        if (schedule.time_of_day && scheduleTime) scheduleTime.value = schedule.time_of_day;
        if (schedule.day_of_week != null) {
          this.setSelectedScheduleDays([Number(schedule.day_of_week)]);
        } else {
          this.setSelectedScheduleDays([]);
        }
      }

      const intervalMinutes = this.getScheduleIntervalMinutes(schedule);
      if (intervalMinutes) {
        const minutes = intervalMinutes;
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
        scheduleDatetime.value = this.formatLocalDatetimeInput(schedule.run_at);
      }
      if (schedule.execute_at && scheduleDatetime) {
        scheduleDatetime.value = this.formatLocalDatetimeInput(schedule.execute_at);
      }
      if (schedule.cron_expr && scheduleCron && !cronWeekly) {
        scheduleCron.value = schedule.cron_expr;
      }

      this.updateScheduleTypeFields();
      this.updateSchedulePreview();
    } else {
      this.resetScheduleFields();
    }
  }

  /**
   * Read currently-checked day-of-week pills, returning an ascending list.
   */
  getSelectedScheduleDays() {
    const row = document.getElementById('taskModalScheduleDayRow');
    if (!row) return [];
    return Array.from(row.querySelectorAll('input[type="checkbox"][data-day-value]'))
      .filter((cb) => cb.checked)
      .map((cb) => Number.parseInt(cb.getAttribute('data-day-value') || '0', 10))
      .sort((a, b) => a - b);
  }

  setSelectedScheduleDays(days) {
    const row = document.getElementById('taskModalScheduleDayRow');
    if (!row) return;
    const set = new Set((days || []).map((d) => Number(d)));
    row.querySelectorAll('input[type="checkbox"][data-day-value]').forEach((cb) => {
      cb.checked = set.has(Number(cb.getAttribute('data-day-value')));
    });
  }

  /**
   * Recognize "M H * * d1,d2,..." cron expressions and translate them back
   * into the weekly multi-day form. Returns null for shapes we don't
   * round-trip — the user will see the raw cron in the cron field.
   */
  parseWeeklyCron(expr) {
    const parts = String(expr || '').trim().split(/\s+/);
    if (parts.length !== 5) return null;
    const [minutePart, hourPart, dom, mon, dow] = parts;
    if (dom !== '*' || mon !== '*') return null;
    if (!/^\d+$/.test(minutePart) || !/^\d+$/.test(hourPart)) return null;
    const days = dow.split(',').map((d) => d.trim()).filter(Boolean);
    if (days.length < 2) return null;
    const numericDays = [];
    for (const d of days) {
      if (!/^[0-6]$/.test(d)) return null;
      numericDays.push(Number(d));
    }
    const minute = Number.parseInt(minutePart, 10);
    const hour = Number.parseInt(hourPart, 10);
    const time = `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`;
    return { days: numericDays, time };
  }

  /**
   * Render a human-readable summary of the current schedule selections —
   * "Every Mon, Wed at 09:00", "Every 30 min", etc. Updates the inline
   * preview line so users get immediate feedback before saving.
   */
  updateSchedulePreview() {
    const previewEl = document.getElementById('taskModalSchedulePreview');
    if (!previewEl) return;

    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    if (!enabledCheckbox?.checked) {
      previewEl.textContent = '';
      previewEl.hidden = true;
      return;
    }

    const scheduleType = document.getElementById('taskModalScheduleType')?.value || 'interval';
    const time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
    const dayLabels = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

    let summary = '';
    switch (scheduleType) {
      case 'daily':
        summary = `Every day at ${time}`;
        break;
      case 'weekly': {
        const days = this.getSelectedScheduleDays();
        if (days.length === 0) {
          summary = 'Pick at least one day';
        } else if (days.length === 7) {
          summary = `Every day at ${time}`;
        } else {
          summary = `Every ${days.map((d) => dayLabels[d] || '?').join(', ')} at ${time}`;
        }
        break;
      }
      case 'interval': {
        const value = Number.parseInt(document.getElementById('taskModalScheduleIntervalValue')?.value || '0', 10);
        const unit = document.getElementById('taskModalScheduleIntervalUnit')?.value || 'hours';
        if (value > 0) {
          summary = `Every ${value} ${unit}`;
        }
        break;
      }
      case 'once': {
        const dt = document.getElementById('taskModalScheduleDatetime')?.value;
        summary = dt ? `Once at ${dt.replace('T', ' ')}` : 'Pick a date and time';
        break;
      }
      case 'cron':
        summary = `Cron: ${document.getElementById('taskModalScheduleCron')?.value?.trim() || '(empty)'}`;
        break;
    }

    if (!summary) {
      previewEl.textContent = '';
      previewEl.hidden = true;
      return;
    }
    previewEl.hidden = false;
    previewEl.textContent = summary;
  }

  /**
   * Get schedule data from modal fields
   */
  getScheduleData() {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    const scheduleFields = document.getElementById('taskModalScheduleFields');
    const scheduleFieldsVisible = scheduleFields && scheduleFields.style.display !== 'none';
    const scheduleEnabled = Boolean(enabledCheckbox?.checked);
    if (!scheduleEnabled && !scheduleFieldsVisible) {
      if (this.currentTask?.schedule) {
        return {
          schedule: this.currentTask.schedule,
          schedule_enabled: false,
          schedule_name: this.currentTask.schedule_name || ''
        };
      }
      return { schedule_enabled: false };
    }

    const scheduleType = document.getElementById('taskModalScheduleType')?.value || 'interval';
    const scheduleName = document.getElementById('taskModalScheduleName')?.value || '';

    const schedule = { type: scheduleType };

    switch (scheduleType) {
      case 'daily':
        schedule.time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
        break;
      case 'weekly': {
        const time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
        const days = this.getSelectedScheduleDays();
        if (days.length <= 1) {
          schedule.time = time;
          schedule.day_of_week = days.length === 1 ? days[0] : 1;
        } else {
          // Backend ScheduleConfig stores a single DayOfWeek int, so a
          // multi-day weekly request is persisted as a cron expression.
          // The user still sees "Weekly" in the form because the cron is
          // round-tripped back into checkboxes by populateScheduleFields.
          const [hourStr, minuteStr] = time.split(':');
          const hour = Number.parseInt(hourStr || '9', 10);
          const minute = Number.parseInt(minuteStr || '0', 10);
          schedule.type = 'cron';
          schedule.cron_expr = `${minute} ${hour} * * ${days.join(',')}`;
        }
        break;
      }
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
          schedule.run_at = datetime;
        }
        break;
      }
      case 'cron':
        schedule.cron_expr = document.getElementById('taskModalScheduleCron')?.value?.trim() || '0 9 * * *';
        break;
    }

    return {
      schedule,
      schedule_enabled: scheduleEnabled,
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

  getScheduleIntervalMinutes(schedule) {
    if (!schedule) return 0;

    const direct = Number(schedule.interval_minutes);
    if (Number.isFinite(direct) && direct > 0) return Math.round(direct);

    const interval = schedule.interval;
    if (typeof interval === 'number' && Number.isFinite(interval) && interval > 0) {
      return interval > 1000000 ? Math.max(1, Math.round(interval / 60000000000)) : Math.round(interval);
    }
    if (typeof interval === 'string') {
      const numeric = Number.parseFloat(interval);
      if (Number.isFinite(numeric) && numeric > 0) {
        return numeric > 1000000 ? Math.max(1, Math.round(numeric / 60000000000)) : Math.round(numeric);
      }
    }

    return 0;
  }

  formatLocalDatetimeInput(value) {
    if (!value) return '';

    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return '';
    }

    const pad = (number) => String(number).padStart(2, '0');
    return [
      date.getFullYear(),
      pad(date.getMonth() + 1),
      pad(date.getDate())
    ].join('-') + `T${pad(date.getHours())}:${pad(date.getMinutes())}`;
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
    const writeModeSelect = document.getElementById('taskModalAutoSaveWriteMode');
    const defaultPathDisplay = document.getElementById('taskModalDefaultOutputPath');

    if (enabledCheckbox) enabledCheckbox.checked = false;
    if (autoSaveFields) autoSaveFields.style.display = 'none';
    if (targetSelect) targetSelect.value = 'default';
    if (storeNodeSelect) storeNodeSelect.innerHTML = '';
    if (pathInput) pathInput.value = '';
    if (formatSelect) formatSelect.value = 'text';
    if (writeModeSelect) writeModeSelect.value = 'new_file';
    this.populateOutputContractRows([]);
    this.outputContractSuggestionCache.clear();
    this.outputContractSuggestionRequestKey = '';

    // Update default path display
    if (defaultPathDisplay && this.defaultOutputDir) {
      defaultPathDisplay.textContent = this.defaultOutputDir;
    }

    this.updateAutoSaveTargetFields();
    this.updateAutoSaveWriteModeFields();
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
    const writeModeSelect = document.getElementById('taskModalAutoSaveWriteMode');

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
      if (writeModeSelect) {
        writeModeSelect.value = task.result_storage.write_mode === 'append' ? 'append' : 'new_file';
      }

      this.populateOutputContractRows(task.output_contract?.columns || [], task.output_contract?.source || 'manual');
      this.updateAutoSaveTargetFields();
      this.updateAutoSaveWriteModeFields();
    } else {
      this.resetAutoSaveFields();
    }
  }

  updateAutoSaveWriteModeFields() {
    const writeMode = document.getElementById('taskModalAutoSaveWriteMode')?.value || 'new_file';
    const formatSelect = document.getElementById('taskModalAutoSaveFormat');
    if (!formatSelect) return;

    const appendMode = writeMode === 'append';
    if (appendMode) {
      formatSelect.value = 'csv';
      if (this.getOutputContractRows().length === 0) {
        void this.ensureOutputContractSuggestion();
      }
    }
    formatSelect.disabled = appendMode;
    formatSelect.title = appendMode ? 'Append mode stores each run as a CSV row.' : '';
    const contractSection = document.getElementById('taskModalOutputContractSection');
    if (contractSection) contractSection.style.display = appendMode ? 'block' : 'none';
    this.updateOutputContractEmptyState();
  }

  getOutputContractRows() {
    return Array.from(document.querySelectorAll('#taskModalOutputContractRows .task-modal-output-contract-row')).map((row) => ({
      name: row.querySelector('[data-output-contract-name]')?.value?.trim() || '',
      type: row.querySelector('[data-output-contract-type]')?.value || 'string',
      required: Boolean(row.querySelector('[data-output-contract-required]')?.checked),
      description: row.querySelector('[data-output-contract-description]')?.value?.trim() || ''
    }));
  }

  populateOutputContractRows(columns = [], source = 'manual') {
    const rows = document.getElementById('taskModalOutputContractRows');
    if (!rows) return;
    rows.innerHTML = '';
    (Array.isArray(columns) ? columns : []).forEach((column) => this.addOutputContractRow(column));
    this.outputContractEdited = false;
    this.outputContractSource = source === 'ai_suggested' || source === 'csv_header' ? source : 'manual';
    this.outputContractEditTelemetrySent = false;
    this.updateOutputContractEmptyState();
    this.clearOutputContractError();
  }

  addOutputContractRow(column = {}) {
    const rows = document.getElementById('taskModalOutputContractRows');
    if (!rows) return;

    const row = document.createElement('div');
    row.className = 'task-modal-output-contract-row';

    const nameInput = document.createElement('input');
    nameInput.type = 'text';
    nameInput.className = 'task-modal-input';
    nameInput.placeholder = 'column_name';
    nameInput.value = column.name || '';
    nameInput.dataset.outputContractName = 'true';
    nameInput.style.fontSize = '0.78rem';

    const typeSelect = document.createElement('select');
    typeSelect.className = 'task-modal-input';
    typeSelect.dataset.outputContractType = 'true';
    typeSelect.style.fontSize = '0.78rem';
    ['string', 'number', 'boolean', 'date'].forEach((type) => {
      const option = document.createElement('option');
      option.value = type;
      option.textContent = type;
      typeSelect.appendChild(option);
    });
    typeSelect.value = ['string', 'number', 'boolean', 'date'].includes(column.type) ? column.type : 'string';

    const requiredLabel = document.createElement('label');
    requiredLabel.style.cssText = 'display: inline-flex; align-items: center; gap: 6px; color: var(--text-secondary); font-size: 0.76rem;';
    const requiredInput = document.createElement('input');
    requiredInput.type = 'checkbox';
    requiredInput.checked = column.required !== false;
    requiredInput.dataset.outputContractRequired = 'true';
    requiredLabel.appendChild(requiredInput);
    requiredLabel.appendChild(document.createTextNode('Required'));

    const descriptionInput = document.createElement('input');
    descriptionInput.type = 'text';
    descriptionInput.className = 'task-modal-input';
    descriptionInput.placeholder = 'description';
    descriptionInput.value = column.description || '';
    descriptionInput.dataset.outputContractDescription = 'true';
    descriptionInput.style.fontSize = '0.78rem';

    const removeButton = document.createElement('button');
    removeButton.type = 'button';
    removeButton.className = 'task-modal-btn task-modal-btn-secondary';
    removeButton.dataset.outputContractRemove = 'true';
    removeButton.title = 'Remove column';
    removeButton.setAttribute('aria-label', 'Remove output contract column');
    removeButton.style.cssText = 'width: 32px; height: 32px; padding: 0; display: inline-flex; align-items: center; justify-content: center;';
    removeButton.textContent = '×';

    row.appendChild(nameInput);
    row.appendChild(typeSelect);
    row.appendChild(requiredLabel);
    row.appendChild(descriptionInput);
    row.appendChild(removeButton);
    rows.appendChild(row);
  }

  updateOutputContractEmptyState() {
    const rows = this.getOutputContractRows();
    const empty = document.getElementById('taskModalOutputContractEmpty');
    if (empty) empty.style.display = rows.length === 0 ? 'block' : 'none';
  }

  markOutputContractEdited() {
    this.outputContractEdited = true;
    this.outputContractSource = 'manual';
    this.setOutputContractStatus('');
    if (!this.outputContractEditTelemetrySent) {
      this.outputContractEditTelemetrySent = true;
      this.trackOutputContractTelemetry('suggestion_edited', {
        source: 'manual',
        column_count: this.getOutputContractRows().filter((row) => row.name).length
      });
    }
  }

  setOutputContractStatus(message, tone = '') {
    const status = document.getElementById('taskModalOutputContractStatus');
    if (!status) return;
    status.textContent = message || '';
    status.style.display = message ? 'block' : 'none';
    status.style.color = tone === 'error' ? '#f87171' : 'var(--text-secondary)';
  }

  showOutputContractError(message) {
    const error = document.getElementById('taskModalOutputContractError');
    if (!error) return;
    error.textContent = message || '';
    error.style.display = message ? 'block' : 'none';
  }

  clearOutputContractError() {
    this.showOutputContractError('');
  }

  getOutputContractSuggestionDraft() {
    const scheduleData = this.getScheduleData();
    const storageTarget = document.getElementById('taskModalAutoSaveTarget')?.value || 'default';
    const storagePath = document.getElementById('taskModalAutoSavePath')?.value?.trim() || '';
    const storeNodeId = document.getElementById('taskModalAutoSaveStoreNode')?.value || '';
    return {
      title: document.getElementById('taskModalDescription')?.value?.trim() || this.currentTask?.description || '',
      details: document.getElementById('taskModalDetails')?.value?.trim() || this.currentTask?.details || '',
      workspace_id: this.workspaceId || '',
      schedule: scheduleData.schedule || null,
      schedule_enabled: Boolean(scheduleData.schedule_enabled),
      schedule_name: scheduleData.schedule_name || '',
      result_storage: {
        enabled: true,
        format: 'csv',
        write_mode: 'append',
        store_node_id: storageTarget === 'store' ? storeNodeId : '',
        file_path: storageTarget === 'custom' ? storagePath : ''
      }
    };
  }

  getOutputContractSuggestionCacheKey(draft = this.getOutputContractSuggestionDraft()) {
    return JSON.stringify({
      title: draft.title || '',
      details: draft.details || '',
      schedule: draft.schedule || null,
      schedule_enabled: Boolean(draft.schedule_enabled),
      schedule_name: draft.schedule_name || '',
      result_storage: draft.result_storage || null
    });
  }

  applyOutputContractSuggestion(suggestion, key, cached = false) {
    const outputSpec = suggestion?.output_spec || (suggestion?.contract ? suggestion : null);
    const contract = outputSpec?.contract || suggestion?.output_contract || suggestion;
    const normalized = this.normalizeOutputContractPayload(contract);
    if (!normalized) return false;
    this.outputSpecDraft = outputSpec ? this.normalizeOutputSpecPayload(outputSpec) : this.buildOutputSpecFromContract(normalized);
    this.populateOutputContractRows(normalized.columns, normalized.source || 'ai_suggested');
    this.outputContractSuggestionRequestKey = key || '';
    this.setOutputContractStatus(cached ? 'Using the cached AI suggestion for this draft.' : 'AI suggestion applied.');
    this.trackOutputContractTelemetry('suggestion_accepted', {
      source: normalized.source || 'ai_suggested',
      column_count: normalized.columns.length
    });
    return true;
  }

  async ensureOutputContractSuggestion({ force = false } = {}) {
    const writeMode = document.getElementById('taskModalAutoSaveWriteMode')?.value || 'new_file';
    if (writeMode !== 'append') return;
    if (!force && this.getOutputContractRows().length > 0) return;

    const draft = this.getOutputContractSuggestionDraft();
    const key = this.getOutputContractSuggestionCacheKey(draft);
    if (!force && this.outputContractSuggestionCache.has(key)) {
      this.applyOutputContractSuggestion(this.outputContractSuggestionCache.get(key), key, true);
      return;
    }
    if (!draft.title && !draft.details) {
      this.populateOutputContractRows(this.suggestOutputContractColumns(), 'manual');
      this.setOutputContractStatus('Add a task title or details to improve the contract suggestion.');
      return;
    }
    if (this.outputContractSuggestionRequestKey === key) return;

    this.outputContractSuggestionRequestKey = key;
    this.setOutputContractStatus('Suggesting columns...');
    const suggestButton = document.getElementById('taskModalOutputContractSuggest');
    if (suggestButton) suggestButton.disabled = true;
    try {
      const response = await fetch('/api/orchestration/tasks/output-spec/suggest', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ...draft,
          task_id: this.editingTaskId || this.currentTask?.id || ''
        })
      });
      if (!response.ok) {
        throw new Error(await response.text() || 'Unable to suggest output contract');
      }
      const data = await response.json();
      const outputSpec = data.output_spec || null;
      const contract = outputSpec?.contract || data.output_contract;
      this.outputContractSuggestionCache.set(key, { output_spec: outputSpec, output_contract: contract });
      if (!this.outputContractEdited || force || this.getOutputContractRows().length === 0) {
        this.applyOutputContractSuggestion({ output_spec: outputSpec, output_contract: contract }, key, false);
      } else {
        this.setOutputContractStatus('AI suggestion is ready. Regenerate to replace your manual edits.');
      }
    } catch (error) {
      console.warn('Output contract suggestion failed:', error);
      if (this.getOutputContractRows().length === 0) {
        this.populateOutputContractRows(this.suggestOutputContractColumns(), 'manual');
        this.outputSpecDraft = null;
      }
      this.setOutputContractStatus('AI suggestion unavailable. You can edit these columns manually.', 'error');
      this.trackOutputContractTelemetry('suggestion_failed', {
        source: 'manual',
        column_count: this.getOutputContractRows().filter((row) => row.name).length
      });
    } finally {
      if (suggestButton) suggestButton.disabled = false;
      if (this.outputContractSuggestionRequestKey === key) {
        this.outputContractSuggestionRequestKey = '';
      }
    }
  }

  async regenerateOutputContractSuggestion() {
    const hasManualRows = this.getOutputContractRows().some((row) => row.name || row.description);
    if (this.outputContractEdited && hasManualRows && !confirm('Replace your unsaved output contract edits with a new suggestion?')) {
      return;
    }
    this.outputContractEdited = false;
    this.trackOutputContractTelemetry('suggestion_regenerated', {
      source: this.outputContractSource || 'manual',
      column_count: this.getOutputContractRows().filter((row) => row.name).length
    });
    await this.ensureOutputContractSuggestion({ force: true });
  }

  trackOutputContractTelemetry(action, extra = {}) {
    const workspaceId = this.workspaceId || this.currentTask?.workspace_id || '';
    if (!workspaceId || typeof fetch !== 'function') return;
    const payload = {
      workspace_id: workspaceId,
      task_id: this.editingTaskId || this.currentTask?.id || '',
      action,
      ...extra
    };
    fetch('/api/orchestration/tasks/output-contract/telemetry', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    }).catch((error) => {
      console.debug('Output contract telemetry unavailable:', error);
    });
  }

  suggestOutputContractColumns() {
    const title = document.getElementById('taskModalDescription')?.value || this.currentTask?.description || '';
    const details = document.getElementById('taskModalDetails')?.value || this.currentTask?.details || '';
    const text = `${title} ${details}`.toLowerCase();
    const base = [
      { name: 'date', type: 'date', required: true, description: 'Run date' },
      { name: 'summary', type: 'string', required: true, description: 'Short result summary' }
    ];
    if (text.includes('pollen')) {
      return [
        { name: 'date', type: 'date', required: true, description: 'Forecast date' },
        { name: 'location', type: 'string', required: true, description: 'City or area' },
        { name: 'pollen_count', type: 'number', required: true, description: 'Reported pollen level' },
        { name: 'category', type: 'string', required: false, description: 'Low, moderate, high, or similar label' },
        { name: 'source', type: 'string', required: false, description: 'Data source' }
      ];
    }
    if (text.includes('weather') || text.includes('temperature')) {
      return [
        { name: 'date', type: 'date', required: true, description: 'Forecast date' },
        { name: 'location', type: 'string', required: true, description: 'City or area' },
        { name: 'temperature', type: 'number', required: false, description: 'Temperature value' },
        { name: 'condition', type: 'string', required: false, description: 'Weather condition' },
        { name: 'source', type: 'string', required: false, description: 'Data source' }
      ];
    }
    if (text.includes('price') || text.includes('stock') || text.includes('crypto')) {
      return [
        { name: 'date', type: 'date', required: true, description: 'Observation date' },
        { name: 'symbol', type: 'string', required: true, description: 'Ticker or asset symbol' },
        { name: 'price', type: 'number', required: true, description: 'Observed price' },
        { name: 'currency', type: 'string', required: false, description: 'Quote currency' },
        { name: 'source', type: 'string', required: false, description: 'Data source' }
      ];
    }
    return base;
  }

  getOutputContractData() {
    const rows = this.getOutputContractRows();
    const seen = new Set();
    const columns = [];
    for (const row of rows) {
      const name = row.name.trim();
      if (!name) {
        return { error: 'Each output contract column needs a name.' };
      }
      const key = name.toLowerCase();
      if (seen.has(key)) {
        return { error: `Duplicate output contract column: ${name}` };
      }
      seen.add(key);
      columns.push({
        name,
        type: ['string', 'number', 'boolean', 'date'].includes(row.type) ? row.type : 'string',
        required: Boolean(row.required),
        description: row.description || undefined
      });
    }
    if (columns.length === 0) {
      return { error: 'Append to CSV requires at least one output contract column.' };
    }
    const outputContract = {
      source: this.outputContractEdited ? 'manual' : (this.outputContractSource || 'manual'),
      columns
    };
    const outputSpec = !this.outputContractEdited && this.outputSpecDraft && this.outputSpecMatchesContract(this.outputSpecDraft, outputContract)
      ? this.normalizeOutputSpecPayload({ ...this.outputSpecDraft, contract: outputContract, source: outputContract.source })
      : this.buildOutputSpecFromContract(outputContract);
    return {
      output_contract: outputContract,
      output_spec: outputSpec
    };
  }

  normalizeOutputContractPayload(contract) {
    const columns = Array.isArray(contract?.columns) ? contract.columns : [];
    const seen = new Set();
    const normalized = [];
    columns.forEach((column) => {
      const name = String(column?.name || '').trim();
      if (!name) return;
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      const type = ['string', 'number', 'boolean', 'date'].includes(column?.type) ? column.type : 'string';
      normalized.push({
        name,
        type,
        required: Boolean(column?.required),
        description: String(column?.description || '').trim() || undefined
      });
    });
    if (normalized.length === 0) return null;
    return {
      source: contract?.source || 'ai_suggested',
      columns: normalized
    };
  }

  normalizeOutputSpecPayload(spec) {
    if (!spec || typeof spec !== 'object') return null;
    const contract = this.normalizeOutputContractPayload(spec.contract);
    if (!contract) return null;
    const schemaFields = Array.isArray(spec.schema?.fields) ? spec.schema.fields : [];
    const normalizedFields = [];
    const seenFields = new Set();
    schemaFields.forEach((field) => {
      const name = String(field?.name || '').trim();
      if (!name) return;
      const key = name.toLowerCase();
      if (seenFields.has(key)) return;
      seenFields.add(key);
      const type = ['string', 'number', 'integer', 'boolean', 'object', 'array'].includes(field?.type) ? field.type : 'string';
      normalizedFields.push({
        name,
        type,
        required: Boolean(field?.required),
        description: String(field?.description || '').trim() || undefined
      });
    });
    const schema = {
      name: String(spec.schema?.name || 'task_result').trim() || 'task_result',
      description: String(spec.schema?.description || '').trim() || undefined,
      strict: spec.schema?.strict !== false,
      fields: normalizedFields.length ? normalizedFields : contract.columns.map((column) => ({
        name: column.name,
        type: column.type === 'number' ? 'number' : column.type === 'boolean' ? 'boolean' : 'string',
        required: Boolean(column.required),
        description: column.description
      }))
    };
    const mappings = Array.isArray(spec.mappings) && spec.mappings.length
      ? spec.mappings.map((mapping) => ({
        schema_field: String(mapping?.schema_field || '').trim(),
        csv_column: String(mapping?.csv_column || '').trim(),
        transform: ['identity', 'json_string'].includes(mapping?.transform) ? mapping.transform : 'identity',
        default_value: String(mapping?.default_value || '').trim() || undefined
      })).filter((mapping) => mapping.schema_field && mapping.csv_column)
      : contract.columns.map((column) => ({
        schema_field: column.name,
        csv_column: column.name,
        transform: 'identity'
      }));
    return {
      source: spec.source || contract.source || 'manual',
      version: spec.version || undefined,
      schema,
      contract,
      mappings,
      metadata_policy: spec.metadata_policy || {
        fields: ['run_id', 'executed_at', 'status', 'duration_ms'].map((name) => ({ name, include: true }))
      }
    };
  }

  buildOutputSpecFromContract(contract) {
    const normalized = this.normalizeOutputContractPayload(contract);
    if (!normalized) return null;
    return this.normalizeOutputSpecPayload({
      source: normalized.source || 'manual',
      schema: {
        name: 'task_result',
        strict: true,
        fields: normalized.columns.map((column) => ({
          name: column.name,
          type: column.type === 'number' ? 'number' : column.type === 'boolean' ? 'boolean' : 'string',
          required: Boolean(column.required),
          description: column.description
        }))
      },
      contract: normalized,
      mappings: normalized.columns.map((column) => ({
        schema_field: column.name,
        csv_column: column.name,
        transform: 'identity'
      }))
    });
  }

  outputSpecMatchesContract(spec, contract) {
    const specColumns = Array.isArray(spec?.contract?.columns) ? spec.contract.columns : [];
    const columns = Array.isArray(contract?.columns) ? contract.columns : [];
    if (specColumns.length !== columns.length) return false;
    return specColumns.every((column, index) => String(column?.name || '').trim().toLowerCase() === String(columns[index]?.name || '').trim().toLowerCase());
  }

  /**
   * Get auto-save data from modal fields
   */
  getAutoSaveData() {
    const enabledCheckbox = document.getElementById('taskModalAutoSaveEnabled');
    if (!enabledCheckbox?.checked) {
      return { result_storage: null, output_contract: { columns: [] } };
    }

    const target = document.getElementById('taskModalAutoSaveTarget')?.value || 'default';
    const format = document.getElementById('taskModalAutoSaveFormat')?.value || 'text';
    const writeMode = document.getElementById('taskModalAutoSaveWriteMode')?.value || 'new_file';
    const appendMode = writeMode === 'append';

    const resultStorage = {
      enabled: true,
      format: appendMode ? 'csv' : format,
      write_mode: appendMode ? 'append' : 'new_file'
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

    const payload = { result_storage: resultStorage };
    if (appendMode) {
      const contractData = this.getOutputContractData();
      if (contractData.error) {
        payload.output_contract_error = contractData.error;
        this.showOutputContractError(contractData.error);
      } else {
        payload.output_contract = contractData.output_contract;
      }
    } else {
      payload.output_contract = { columns: [] };
    }

    return payload;
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
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(this.workspaceId)}`);
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
  /**
   * Read the currently-selected input task refs from a chip container
   * (or, for backward compatibility, a legacy <select multiple>).
   * Chip containers store their selection as JSON in
   * data-selected-inputs so the value is robust to DOM rerenders that
   * recreate the chip buttons.
   */
  getSelectedInputRefs(container) {
    if (!container) return [];
    if (container.tagName === 'SELECT') {
      return Array.from(container.selectedOptions)
        .map((option) => option.value)
        .filter((value) => value && value.trim() !== '');
    }
    try {
      const raw = JSON.parse(container.dataset.selectedInputs || '[]');
      return Array.isArray(raw) ? raw.filter((v) => typeof v === 'string' && v.trim() !== '') : [];
    } catch (_e) {
      return [];
    }
  }

  /**
   * Render a clickable chip widget for selecting input tasks.
   *
   * `groups` is an array of { label?, items: [{value, label}] }. Items
   * inside each group are rendered as togglable chips with a visible
   * "selected" state. When the user clicks a chip the container fires
   * a `change` event so existing subtask-badge / hint code paths keep
   * working without modification.
   */
  renderInputTaskChips(container, groups, selectedRefs, options = {}) {
    if (!container) return;
    container.classList.toggle('is-disabled', Boolean(options.disabled));

    const allItems = groups.reduce((sum, g) => sum + (g?.items?.length || 0), 0);
    const selectedSet = new Set(Array.isArray(selectedRefs) ? selectedRefs : []);
    container.dataset.selectedInputs = JSON.stringify(Array.from(selectedSet));

    if (allItems === 0) {
      container.innerHTML = '';
      return;
    }

    const escapeAttr = (value) => this.escapeHtml(value);
    const sectionsHtml = groups
      .filter((group) => group?.items?.length)
      .map((group) => {
        const labelHtml = group.label
          ? `<div class="task-modal-input-chip-group-label">${escapeAttr(group.label)}</div>`
          : '';
        const chipsHtml = group.items.map((item) => {
          const checked = selectedSet.has(item.value);
          return `
            <button type="button"
                    class="task-modal-input-chip${checked ? ' is-selected' : ''}"
                    data-input-value="${escapeAttr(item.value)}"
                    aria-pressed="${checked ? 'true' : 'false'}"
                    ${options.disabled ? 'disabled' : ''}>
              <span class="task-modal-input-chip-mark" aria-hidden="true"></span>
              <span class="task-modal-input-chip-label">${escapeAttr(item.label)}</span>
            </button>
          `;
        }).join('');
        return `${labelHtml}<div class="task-modal-input-chip-row">${chipsHtml}</div>`;
      }).join('');

    container.innerHTML = sectionsHtml;

    container.querySelectorAll('[data-input-value]').forEach((btn) => {
      btn.addEventListener('click', () => {
        if (btn.disabled) return;
        const value = btn.getAttribute('data-input-value') || '';
        const current = new Set(this.getSelectedInputRefs(container));
        if (current.has(value)) {
          current.delete(value);
        } else {
          current.add(value);
        }
        container.dataset.selectedInputs = JSON.stringify(Array.from(current));
        const isSelected = current.has(value);
        btn.classList.toggle('is-selected', isSelected);
        btn.setAttribute('aria-pressed', isSelected ? 'true' : 'false');
        container.dispatchEvent(new Event('change', { bubbles: true }));
      });
    });
  }

  /**
   * Bind a legacy <select multiple> for click-to-toggle. Chip widgets
   * handle their own clicks; this stays as a no-op for any non-select
   * element so existing callers continue to compile.
   */
  bindMultiSelectToggle(container) {
    if (!container || container.tagName !== 'SELECT') return;
    if (container.dataset.toggleBound === 'true') return;
    container.dataset.toggleBound = 'true';

    container.addEventListener('mousedown', (event) => {
      if (container.disabled) return;
      const option = event.target;
      if (!option || option.tagName !== 'OPTION' || option.disabled) return;
      event.preventDefault();
      option.selected = !option.selected;
      container.dispatchEvent(new Event('change', { bubbles: true }));
    });
  }

  /**
   * Populate the main task input selection list.
   */
  populateMainInputTasks(task = null) {
    const container = document.getElementById('taskModalInputTasks');
    if (!container) return;

    const currentTaskId = this.editingTaskId || '';
    const excludeIds = new Set();
    if (currentTaskId) excludeIds.add(currentTaskId);

    const options = this.getWorkspaceTaskOptions({ excludeIds });
    const selectedRefs = (task?.input_task_ids || []).map((id) => `task:${id}`);

    this.renderInputTaskChips(container, [{ items: options }], selectedRefs);

    const emptyEl = document.getElementById('taskModalInputTasksEmpty');
    if (emptyEl) {
      emptyEl.style.display = options.length > 0 ? 'none' : 'block';
    }
  }

  setMainInputTaskRefs(refs = []) {
    const container = document.getElementById('taskModalInputTasks');
    if (!container) return;
    const normalized = Array.isArray(refs)
      ? refs.map((value) => String(value || '').trim()).filter(Boolean)
      : [];
    container.dataset.selectedInputs = JSON.stringify(normalized);
    const selectedSet = new Set(normalized);
    container.querySelectorAll('[data-input-value]').forEach((btn) => {
      const value = btn.getAttribute('data-input-value') || '';
      const checked = selectedSet.has(value);
      btn.classList.toggle('is-selected', checked);
      btn.setAttribute('aria-pressed', checked ? 'true' : 'false');
    });
  }

  /**
   * Update main input selection state when subtasks exist.
   */
  updateMainInputTasksState() {
    const container = document.getElementById('taskModalInputTasks');
    const noticeEl = document.getElementById('taskModalInputTasksNotice');
    if (!container || !noticeEl) return;

    const list = document.getElementById('taskModalSubtaskList');
    const hasSubtasks = list && list.children.length > 0;
    noticeEl.style.display = hasSubtasks ? 'block' : 'none';

    container.classList.toggle('is-disabled', hasSubtasks);
    container.querySelectorAll('[data-input-value]').forEach((btn) => {
      btn.disabled = hasSubtasks;
    });
    if (hasSubtasks) {
      // Wipe selection — workflow inputs live on each subtask now.
      container.dataset.selectedInputs = JSON.stringify([]);
      container.querySelectorAll('[data-input-value].is-selected').forEach((btn) => {
        btn.classList.remove('is-selected');
        btn.setAttribute('aria-pressed', 'false');
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
      const container = card.querySelector('.task-modal-subtask-inputs');
      if (!container) return;

      let selectedRefs = [];
      try {
        selectedRefs = JSON.parse(container.dataset.selectedInputs || '[]');
      } catch (_e) {
        selectedRefs = [];
      }

      const availableSteps = stepOptions.slice(0, index);
      const groups = [];
      if (availableSteps.length > 0) groups.push({ label: 'Workflow steps', items: availableSteps });
      if (externalOptions.length > 0) groups.push({ label: 'Workspace tasks', items: externalOptions });

      if (groups.length === 0) {
        container.innerHTML = '<div class="task-modal-input-empty">No input tasks available — add another step or another task to the workspace.</div>';
        container.dataset.selectedInputs = JSON.stringify([]);
        this.updateSubtaskInputsBadge(card, 0);
        return;
      }

      // Drop refs that no longer exist in the rendered options. This
      // prevents stale "task:abc" refs from sticking around after the
      // referenced sibling step is removed.
      const validValues = new Set();
      groups.forEach((g) => g.items.forEach((item) => validValues.add(item.value)));
      const filteredRefs = selectedRefs.filter((ref) => validValues.has(ref));

      this.renderInputTaskChips(container, groups, filteredRefs);
      this.updateSubtaskInputsBadge(card, filteredRefs.length);
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
    inputsLabel.textContent = 'Input Tasks';
    const inputsContainer = document.createElement('div');
    inputsContainer.id = `${rowId}-inputs`;
    inputsContainer.className = 'task-modal-input-chips task-modal-subtask-inputs';
    inputsContainer.setAttribute('role', 'group');
    inputsContainer.setAttribute('aria-label', 'Subtask input tasks');
    inputsContainer.dataset.selectedInputs = JSON.stringify(data.inputTaskIds || []);
    inputsField.appendChild(inputsLabel);
    inputsField.appendChild(inputsContainer);

    const inputsHint = document.createElement('div');
    inputsHint.className = 'task-modal-subtask-inputs-hint';
    inputsHint.textContent = 'Click a step or task to feed its result into this step. Reference values with {input1}, {input2}, {previous}, or {result} in the title.';
    inputsField.appendChild(inputsHint);

    inputsContainer.addEventListener('change', () => {
      const selectedRefs = this.getSelectedInputRefs(inputsContainer);
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
      if (scheduleData?.schedule || scheduleData?.schedule_enabled) {
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
      return forUpdate ? { result_storage: null, output_contract: { columns: [] } } : {};
    }
    return forUpdate ? { result_storage: null, output_contract: { columns: [] } } : {};
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
      dropZone.addEventListener('keydown', (e) => {
        if (e.key !== 'Enter' && e.key !== ' ') return;
        e.preventDefault();
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
window.TaskModalController = TaskModalController;
window.taskModalController = new TaskModalController();

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  window.taskModalController.init();
});
