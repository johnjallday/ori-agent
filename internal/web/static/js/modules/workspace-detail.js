/**
 * Workspace Detail Page Module
 * Handles the dedicated workspace detail page with panels for tasks, sessions, files, and notes.
 *
 * @module workspace-detail
 */
/* global escapeHtml */

/**
 * Format a date for display
 * @param {string} dateString - ISO date string
 * @returns {string} Formatted date
 */
function formatDate(dateString) {
  if (!dateString) return '';
  const date = new Date(dateString);
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

/**
 * Get status badge class
 * @param {string} status - Task status
 * @returns {string} CSS class
 */
function getStatusClass(status) {
  const statusMap = {
    pending: 'pending',
    in_progress: 'in_progress',
    completed: 'completed',
    failed: 'failed',
    blocked: 'blocked',
    cancelled: 'pending',
    timeout: 'failed'
  };
  return statusMap[status] || 'pending';
}

/**
 * Get display status text
 * @param {string} status - Task status
 * @returns {string} Display text
 */
function getDisplayStatus(status) {
  const statusMap = {
    pending: 'Pending',
    in_progress: 'In Progress',
    completed: 'Completed',
    failed: 'Failed',
    blocked: 'Blocked',
    cancelled: 'Cancelled',
    timeout: 'Timed Out'
  };
  return statusMap[status] || status;
}

const AGENT_CREATION_SKILL_CATALOG_AGENT = '__ori_agent_create_catalog__';

const TASK_REQUIREMENT_KEYS = Object.freeze({
  BROWSER: 'browser',
  FILESYSTEM: 'filesystem'
});

const TASK_ASSIST_PENDING_SPECIALIST_STORAGE_KEY = 'workspace-detail-task-assist-specialist';

const TASK_ASSIST_TRAVEL_SPECIALISTS = Object.freeze({
  travel_itinerary: Object.freeze({
    key: 'travel_itinerary',
    label: 'Travel Itinerary Planner',
    agentName: 'Travel Itinerary Planner',
    agentType: 'tool-calling',
    description: 'Plans day-by-day travel itineraries with neighborhood guidance, food picks, museum ideas, budget notes, and pacing.',
    systemPrompt: 'You are a travel itinerary planner. Build practical, day-by-day trip plans with realistic pacing, local food and neighborhood recommendations, transit notes, and concise options. Ask clarifying questions when key details are missing and avoid inventing bookings or confirmed reservations.',
    nameTokens: ['travel', 'trip', 'itinerary'],
    scorePhrases: ['day by day', 'day-by-day', 'itinerary', 'trip plan', 'travel plan', 'restaurant', 'restaurants', 'food', 'museum', 'museums', 'nightlife', 'day trip', 'day trips', 'budget breakdown', 'budget', 'accommodation', 'accommodation areas', 'neighborhood', 'neighbourhood']
  }),
  hotel_booking: Object.freeze({
    key: 'hotel_booking',
    label: 'Hotel Booking Agent',
    agentName: 'Hotel Booking Agent',
    agentType: 'tool-calling',
    description: 'Finds and compares hotels by neighborhood, budget, amenities, and travel constraints.',
    systemPrompt: 'You are a hotel booking assistant. Help compare hotel options by neighborhood, budget, amenities, and transport tradeoffs. Ask for missing constraints before recommending stays, and do not invent live prices or confirmed bookings.',
    nameTokens: ['hotel', 'lodging', 'accommodation'],
    scorePhrases: ['hotel', 'hotels', 'stay', 'stays', 'lodging', 'accommodation', 'where to stay', 'book hotel', 'book hotels']
  }),
  flight_booking: Object.freeze({
    key: 'flight_booking',
    label: 'Flight Booking Agent',
    agentName: 'Flight Booking Agent',
    agentType: 'tool-calling',
    description: 'Helps fill booking gaps for flights and longer-distance travel legs with schedule and transfer considerations.',
    systemPrompt: 'You are a flight booking assistant. Help identify missing flight or long-distance travel legs, compare route options, call out tradeoffs, and confirm timing constraints before recommending bookings.',
    nameTokens: ['flight', 'airfare', 'airport'],
    scorePhrases: ['flight', 'flights', 'airfare', 'airport', 'route option', 'route options', 'connection', 'connections', 'transfer', 'transfer timing']
  })
});

export class WorkspaceDetailPage {
  constructor(workspaceId) {
    this.workspaceId = workspaceId;
    this.workspace = null;
    this.tasks = [];
    this.sessions = [];
    this.tasksLoading = false;
    this.sessionsLoading = false;
    this.tasksLoadFailed = false;
    this.sessionsLoadFailed = false;
    this.files = [];
    this.notes = [];
    this.directories = [];
    this.schedules = [];
    this.children = [];
    this.directoryExplorer = {
      directory: null,
      source: 'reference',
      files: [],
      treeRoot: null,
      nodeIndex: new Map(),
      expandedPaths: new Set(),
      selectedPath: '',
      selectedType: '',
      searchQuery: '',
      sortDirection: 'asc',
      fileCache: new Map(),
      previewCache: new Map(),
      previewAbortController: null,
      loadToken: 0
    };

    // Board state
    this.currentView = 'list'; // 'list' or 'board'
    this.boardConfig = null;
    this.agentOptions = null;
    this.agentCatalog = [];
    this.agentIndex = new Map();
    this.agentSkillsCache = new Map();
    this.agentSkillsPromises = new Map();
    this.availableMCPServers = [];
    this.availableMCPServersPromise = null;
    this.availableEmailAccounts = [];
    this.availableEmailAccountsPromise = null;
    this.activeWorkspaceMCPBindingId = '';
    this.activeWorkspaceMCPMode = 'create';
    this.availableSkills = [];
    this.availableSkillsPromise = null;
    this.activeWorkspaceSkillBindingId = '';
    this.activeWorkspaceSkillMode = 'create';
    this.workspaceSettings = null;
    this.workspaceSettingsEffectiveBehavior = null;
    this.workspaceConfigExpanded = true;
    this.workspaceConfigPreferenceLoaded = false;
    this.capabilitySuggestionCatalog = null;
    this.capabilitySuggestionCatalogPromise = null;
    this.flippedAgentCards = new Set();
    this.boardDidDrag = false;
    this.currentTaskResultText = '';
    this.currentTaskResultTaskId = '';
    this.currentTaskResultSourceTaskId = '';
    this.currentTaskResultNextSteps = [];
    this.currentTaskResultFollowUpPending = false;
    this.currentBlockedTask = null;
    this.currentAssistRecommendation = null;
    this.currentAssistSpecialistAction = null;
    this.currentExecutionTaskId = null;
    this.executionMonitorTimer = null;
    this.executionLogKeys = new Set();
    this.executionLastStatus = '';
    this.pendingTaskConfirm = null;
    this.pendingTaskConfirmSelection = { stepThrough: false };
    this.pendingAssistSpecialistResumeChecked = false;

    // DOM elements
    this.elements = {};
    this.fileModalElements = {};
    this.fileModalState = {
      source: 'device',
      selectedItems: [],
      vaults: [],
      selectedVaultId: '',
      vaultStatus: null,
      vaultAttachments: [],
      searchQuery: '',
      loadingVaults: false,
      loadingAttachments: false
    };
  }

  /**
   * Initialize the workspace detail page
   */
  async init() {
    this.cacheElements();
    this.ensureScrollablePanelAccessibility();
    this.refreshHomeAssistantQuickPrompts();
    this.bindEvents();
    this.setupFileModal();
    await this.loadWorkspace();
    await this.loadAgentCatalog();
    await Promise.all([
      this.loadTasks(),
      this.loadSessions(),
      this.loadFiles(),
      this.loadNotes(),
      this.loadDirectories(),
      this.loadSchedules()
    ]);
    this.maybeResumePendingAssistSpecialistHandoff();
    this.activateWorkspace();
    this.setupRealtime();
    this.checkAutoOpenCreateAgent();
  }

  ensureScrollablePanelAccessibility() {
    const panelConfig = [
      { id: 'workspace-detail-agents-panel', label: 'Agents panel content' },
      { id: 'workspace-detail-notes-panel', label: 'Notes panel content' },
      { id: 'workspace-detail-files-panel', label: 'Files panel content' },
      { id: 'workspace-detail-directories-panel', label: 'Directories panel content' },
      { id: 'workspace-detail-schedules-panel', label: 'Schedules panel content' }
    ];

    panelConfig.forEach(({ id, label }) => {
      const panel = document.getElementById(id);
      const body = panel?.querySelector('.workspace-detail-panel-body');
      if (!body) return;

      body.setAttribute('role', 'region');
      body.setAttribute('tabindex', '0');
      body.setAttribute('aria-label', label);
    });
  }

  setHomeAssistantQuickPrompt(button, label, prompt) {
    if (!button) return;
    button.textContent = label;
    button.setAttribute('data-home-prompt', prompt);
    button.setAttribute('title', prompt);
    button.setAttribute('aria-label', label);
  }

  refreshHomeAssistantQuickPrompts() {
    const {
      homeAssistantQuickPlanBtn,
      homeAssistantQuickTasksBtn,
      homeAssistantQuickNotesBtn,
      homeAssistantQuickReviewBtn
    } = this.elements;

    if (!homeAssistantQuickPlanBtn || !homeAssistantQuickTasksBtn || !homeAssistantQuickNotesBtn || !homeAssistantQuickReviewBtn) {
      return;
    }

    const workspaceName = String(this.workspace?.name || '').trim() || 'this workspace';
    const taskCount = Array.isArray(this.tasks) ? this.tasks.length : 0;
    const noteCount = Array.isArray(this.notes) ? this.notes.length : 0;
    const directoryCount = Array.isArray(this.directories) ? this.directories.length : 0;
    const fileCount = Array.isArray(this.files) ? this.files.length : 0;

    this.setHomeAssistantQuickPrompt(
      homeAssistantQuickPlanBtn,
      'Plan Next',
      `Analyze ${workspaceName} and propose the next 3 highest-impact tasks with acceptance criteria.`
    );

    if (taskCount === 0) {
      this.setHomeAssistantQuickPrompt(
        homeAssistantQuickTasksBtn,
        'Bootstrap Tasks',
        `Create the first 5 actionable tasks for ${workspaceName}, each with clear acceptance criteria.`
      );
    } else {
      this.setHomeAssistantQuickPrompt(
        homeAssistantQuickTasksBtn,
        'Unblock Tasks',
        `Review tasks in ${workspaceName}, identify blockers, and suggest the next 3 tasks to move progress forward.`
      );
    }

    if (noteCount === 0) {
      this.setHomeAssistantQuickPrompt(
        homeAssistantQuickNotesBtn,
        'Create Brief',
        `/note # ${workspaceName} Brief\n## Goal\n- \n## Constraints\n- \n## Open Questions\n- `
      );
    } else {
      this.setHomeAssistantQuickPrompt(
        homeAssistantQuickNotesBtn,
        'Summarize Notes',
        `/chat Summarize notes in ${workspaceName} and produce a prioritized execution checklist.`
      );
    }

    if (directoryCount === 0 && fileCount === 0) {
      this.setHomeAssistantQuickPrompt(
        homeAssistantQuickReviewBtn,
        'Gather Context',
        `Tell me what context is missing for ${workspaceName} and ask 3 focused questions before execution.`
      );
    } else {
      this.setHomeAssistantQuickPrompt(
        homeAssistantQuickReviewBtn,
        'Risk Review',
        `Run a risk review for ${workspaceName} using available tasks, notes, files, and directories. Recommend mitigation steps.`
      );
    }
  }

  /**
   * Setup file modal handlers
   */
  setupFileModal() {
    const modal = document.getElementById('hubAddFileModal');
    const dropZone = document.getElementById('hubFileDropZone');
    const fileInput = document.getElementById('hubFileInput');
    const submitBtn = document.getElementById('hubAddFileSubmitBtn');
    const selectedFilesPreview = document.getElementById('hubSelectedFilesPreview');
    const selectedFilesList = document.getElementById('hubSelectedFilesList');
    const titleInput = document.getElementById('hubFileTitle');
    const notesInput = document.getElementById('hubFileNotes');
    const devicePane = document.getElementById('hubFileDevicePane');
    const vaultPane = document.getElementById('hubFileVaultPane');
    const deviceBtn = document.getElementById('hubFileSourceDeviceBtn');
    const vaultBtn = document.getElementById('hubFileSourceVaultBtn');
    const vaultSelect = document.getElementById('hubVaultSelect');
    const vaultRefreshBtn = document.getElementById('hubVaultRefreshBtn');
    const vaultSearchInput = document.getElementById('hubVaultSearchInput');
    const vaultLockedState = document.getElementById('hubVaultLockedState');
    const vaultUnlockPassword = document.getElementById('hubVaultUnlockPassword');
    const vaultUnlockBtn = document.getElementById('hubVaultUnlockBtn');
    const vaultAttachmentList = document.getElementById('hubVaultAttachmentList');

    if (!modal || !dropZone || !fileInput || !submitBtn || !selectedFilesPreview || !selectedFilesList || !devicePane || !vaultPane) {
      return;
    }

    this.fileModalElements = {
      modal,
      dropZone,
      fileInput,
      submitBtn,
      selectedFilesPreview,
      selectedFilesList,
      titleInput,
      notesInput,
      devicePane,
      vaultPane,
      deviceBtn,
      vaultBtn,
      vaultSelect,
      vaultRefreshBtn,
      vaultSearchInput,
      vaultLockedState,
      vaultUnlockPassword,
      vaultUnlockBtn,
      vaultAttachmentList
    };

    deviceBtn?.addEventListener('click', () => {
      this.setFileModalSource('device');
    });

    vaultBtn?.addEventListener('click', async () => {
      await this.setFileModalSource('vault');
    });

    dropZone.addEventListener('click', () => {
      if (this.fileModalState.source === 'device') {
        fileInput.click();
      }
    });

    dropZone.addEventListener('dragover', (event) => {
      if (this.fileModalState.source !== 'device') {
        return;
      }
      event.preventDefault();
      dropZone.classList.add('drag-active');
    });

    dropZone.addEventListener('dragleave', () => {
      dropZone.classList.remove('drag-active');
    });

    dropZone.addEventListener('drop', (event) => {
      if (this.fileModalState.source !== 'device') {
        return;
      }
      event.preventDefault();
      dropZone.classList.remove('drag-active');
      if (event.dataTransfer.files.length > 0) {
        this.fileModalState.selectedItems = Array.from(event.dataTransfer.files).map((file) => ({
          source: 'device',
          name: file.name,
          size: Number(file.size || 0),
          file
        }));
        this.renderFileModalSelectionPreview();
      }
    });

    fileInput.addEventListener('change', () => {
      if (fileInput.files.length > 0) {
        this.fileModalState.selectedItems = Array.from(fileInput.files).map((file) => ({
          source: 'device',
          name: file.name,
          size: Number(file.size || 0),
          file
        }));
        this.renderFileModalSelectionPreview();
      }
    });

    vaultSelect?.addEventListener('change', async () => {
      this.fileModalState.selectedVaultId = String(vaultSelect.value || '').trim();
      this.fileModalState.selectedItems = [];
      this.renderFileModalSelectionPreview();
      await this.loadFileModalVaultAttachments();
    });

    vaultSearchInput?.addEventListener('input', () => {
      this.fileModalState.searchQuery = String(vaultSearchInput.value || '').trim().toLowerCase();
      this.renderFileModalVaultAttachments();
    });

    vaultRefreshBtn?.addEventListener('click', async () => {
      await this.loadFileModalVaults(true);
    });

    vaultUnlockBtn?.addEventListener('click', async () => {
      await this.unlockSelectedVaultForFileModal();
    });

    vaultUnlockPassword?.addEventListener('keydown', async (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        await this.unlockSelectedVaultForFileModal();
      }
    });

    vaultAttachmentList?.addEventListener('click', (event) => {
      const trigger = event.target.closest('[data-vault-attachment-id]');
      if (!trigger) {
        return;
      }

      const attachmentId = String(trigger.getAttribute('data-vault-attachment-id') || '').trim();
      if (attachmentId) {
        this.toggleVaultAttachmentSelection(attachmentId);
      }
    });

    submitBtn.addEventListener('click', async () => {
      await this.uploadSelectedFileModalItems();
    });

    modal.addEventListener('show.bs.modal', async () => {
      this.resetFileModalState();
      this.renderFileModalSource();
      this.renderFileModalSelectionPreview();
    });

    modal.addEventListener('hidden.bs.modal', () => {
      this.resetFileModalState();
    });
  }

  resetFileModalState() {
    this.fileModalState = {
      source: 'device',
      selectedItems: [],
      vaults: [],
      selectedVaultId: '',
      vaultStatus: null,
      vaultAttachments: [],
      searchQuery: '',
      loadingVaults: false,
      loadingAttachments: false
    };

    const {
      fileInput,
      titleInput,
      notesInput,
      vaultSelect,
      vaultSearchInput,
      vaultUnlockPassword
    } = this.fileModalElements;

    if (fileInput) {
      fileInput.value = '';
    }
    if (titleInput) {
      titleInput.value = '';
    }
    if (notesInput) {
      notesInput.value = '';
    }
    if (vaultSelect) {
      vaultSelect.innerHTML = '<option value="">Loading vaults...</option>';
    }
    if (vaultSearchInput) {
      vaultSearchInput.value = '';
      vaultSearchInput.disabled = false;
    }
    if (vaultUnlockPassword) {
      vaultUnlockPassword.value = '';
    }

    this.renderFileModalSource();
    this.renderFileModalSelectionPreview();
    this.renderFileModalVaultAttachments();
  }

  async setFileModalSource(source) {
    const normalized = source === 'vault' ? 'vault' : 'device';
    if (this.fileModalState.source !== normalized) {
      this.fileModalState.source = normalized;
      this.fileModalState.selectedItems = [];
      if (this.fileModalElements.fileInput) {
        this.fileModalElements.fileInput.value = '';
      }
    }

    this.renderFileModalSource();
    this.renderFileModalSelectionPreview();

    if (normalized === 'vault') {
      await this.loadFileModalVaults(false);
    }
  }

  renderFileModalSource() {
    const {
      devicePane,
      vaultPane,
      deviceBtn,
      vaultBtn
    } = this.fileModalElements;

    const usingVault = this.fileModalState.source === 'vault';

    if (devicePane) {
      devicePane.hidden = usingVault;
    }
    if (vaultPane) {
      vaultPane.hidden = !usingVault;
    }

    if (deviceBtn) {
      deviceBtn.classList.toggle('is-active', !usingVault);
      deviceBtn.setAttribute('aria-pressed', usingVault ? 'false' : 'true');
    }
    if (vaultBtn) {
      vaultBtn.classList.toggle('is-active', usingVault);
      vaultBtn.setAttribute('aria-pressed', usingVault ? 'true' : 'false');
    }
  }

  renderFileModalSelectionPreview() {
    const { selectedFilesPreview, selectedFilesList, submitBtn } = this.fileModalElements;
    const items = Array.isArray(this.fileModalState.selectedItems) ? this.fileModalState.selectedItems : [];

    if (!selectedFilesPreview || !selectedFilesList || !submitBtn) {
      return;
    }

    if (!items.length) {
      selectedFilesPreview.style.display = 'none';
      submitBtn.disabled = true;
      return;
    }

    selectedFilesPreview.style.display = 'block';
    submitBtn.disabled = false;

    selectedFilesList.innerHTML = items.map((item, index) => {
      const meta = [];
      if (Number.isFinite(item.size) && item.size > 0) {
        meta.push(this.formatFileSize(item.size));
      }
      if (item.source === 'vault') {
        meta.push(item.recordLabel || 'Vault entry');
      }

      return `
        <div class="d-flex justify-content-between align-items-center p-2" style="background: var(--bg-secondary); border-radius: 8px; gap: 0.75rem;">
          <div style="min-width: 0;">
            <div style="color: var(--text-primary); font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">
              ${this.escapeHtml(item.name || 'Untitled file')}
              <span class="hub-selected-files-source">${item.source === 'vault' ? 'Vault' : 'Device'}</span>
            </div>
            <div style="color: var(--text-secondary); font-size: 0.76rem; margin-top: 2px;">
              ${this.escapeHtml(meta.join(' • '))}
            </div>
          </div>
          <button type="button" class="btn-close btn-sm" onclick="window.workspaceDetail?.removeFileModalSelection(${index})"></button>
        </div>
      `;
    }).join('');
  }

  removeFileModalSelection(index) {
    if (!Array.isArray(this.fileModalState.selectedItems)) {
      return;
    }

    this.fileModalState.selectedItems.splice(index, 1);
    this.renderFileModalSelectionPreview();
    if (this.fileModalState.source === 'vault') {
      this.renderFileModalVaultAttachments();
    }
  }

  async loadFileModalVaults(force = false) {
    const { vaultSelect } = this.fileModalElements;
    if (this.fileModalState.loadingVaults) {
      return;
    }

    if (!force && this.fileModalState.vaults.length) {
      if (!this.fileModalState.selectedVaultId) {
        const storedVaultId = String(window.localStorage?.getItem('ori-selected-vault-id') || '').trim();
        this.fileModalState.selectedVaultId = this.fileModalState.vaults.find((item) => item.id === storedVaultId)?.id || this.fileModalState.vaults[0]?.id || '';
        if (vaultSelect) {
          vaultSelect.value = this.fileModalState.selectedVaultId;
        }
      }
      await this.loadFileModalVaultAttachments();
      return;
    }

    this.fileModalState.loadingVaults = true;
    if (vaultSelect) {
      vaultSelect.innerHTML = '<option value="">Loading vaults...</option>';
      vaultSelect.disabled = true;
    }
    this.renderFileModalVaultAttachments();

    try {
      const response = await fetch('/api/vault/vaults');
      if (!response.ok) {
        throw new Error('Failed to load vaults');
      }

      const data = await response.json();
      this.fileModalState.vaults = Array.isArray(data?.vaults) ? data.vaults : [];

      const storedVaultId = String(window.localStorage?.getItem('ori-selected-vault-id') || '').trim();
      const preferredVaultId = this.fileModalState.selectedVaultId || storedVaultId;
      this.fileModalState.selectedVaultId = this.fileModalState.vaults.find((item) => item.id === preferredVaultId)?.id || this.fileModalState.vaults[0]?.id || '';

      if (vaultSelect) {
        if (!this.fileModalState.vaults.length) {
          vaultSelect.innerHTML = '<option value="">No vaults available</option>';
        } else {
          vaultSelect.innerHTML = this.fileModalState.vaults.map((vault) => {
            const label = `${vault.name || 'Vault'} · ${Number(vault.record_count || 0)} ${Number(vault.record_count || 0) === 1 ? 'entry' : 'entries'}`;
            const selected = vault.id === this.fileModalState.selectedVaultId ? ' selected' : '';
            return `<option value="${this.escapeHtml(vault.id)}"${selected}>${this.escapeHtml(label)}</option>`;
          }).join('');
        }
        vaultSelect.disabled = !this.fileModalState.vaults.length;
      }

      this.fileModalState.loadingVaults = false;
      await this.loadFileModalVaultAttachments();
    } catch (error) {
      console.error('Failed to load vaults for file modal:', error);
      this.fileModalState.vaults = [];
      this.fileModalState.selectedVaultId = '';
      this.fileModalState.vaultStatus = null;
      this.fileModalState.vaultAttachments = [];
      if (vaultSelect) {
        vaultSelect.innerHTML = '<option value="">No vaults available</option>';
        vaultSelect.disabled = true;
      }
      this.renderFileModalVaultAttachments('No vaults available yet. Create and unlock a vault first.');
    } finally {
      this.fileModalState.loadingVaults = false;
    }
  }

  async loadFileModalVaultAttachments() {
    const selectedVaultId = String(this.fileModalState.selectedVaultId || '').trim();
    const { vaultUnlockPassword } = this.fileModalElements;

    this.fileModalState.vaultAttachments = [];
    this.fileModalState.vaultStatus = null;

    if (vaultUnlockPassword) {
      vaultUnlockPassword.value = '';
    }

    if (!selectedVaultId) {
      this.renderFileModalVaultAttachments();
      return;
    }

    this.fileModalState.loadingAttachments = true;
    this.renderFileModalVaultAttachments();

    try {
      const statusResponse = await fetch(`/api/vault/status?vault_id=${encodeURIComponent(selectedVaultId)}`);
      if (!statusResponse.ok) {
        throw new Error('Failed to load vault status');
      }

      const status = await statusResponse.json();
      this.fileModalState.vaultStatus = status;

      if (!status?.available || status?.locked) {
        this.renderFileModalVaultAttachments();
        return;
      }

      const listResponse = await fetch(`/api/vault/records?vault_id=${encodeURIComponent(selectedVaultId)}`);
      if (!listResponse.ok) {
        throw new Error('Failed to load vault records');
      }

      const listData = await listResponse.json();
      const records = Array.isArray(listData?.records) ? listData.records : [];
      const selectedVault = this.getFileModalSelectedVault();
      const recordDetails = await Promise.all(records.map(async (record) => {
        try {
          const response = await fetch(`/api/vault/records/${encodeURIComponent(record.id)}?vault_id=${encodeURIComponent(selectedVaultId)}`);
          if (!response.ok) {
            return null;
          }
          return await response.json();
        } catch (error) {
          console.warn('Failed to load vault record for workspace import:', record?.id, error);
          return null;
        }
      }));

      this.fileModalState.vaultAttachments = recordDetails
        .filter(Boolean)
        .flatMap((record) => this.extractVaultAttachmentsForWorkspaceModal(record, selectedVault?.name || 'Vault'))
        .sort((left, right) => new Date(right.updatedAt || 0) - new Date(left.updatedAt || 0));
    } catch (error) {
      console.error('Failed to load vault attachments for workspace modal:', error);
      this.fileModalState.vaultAttachments = [];
      this.renderFileModalVaultAttachments('Failed to load files from the selected vault.');
      return;
    } finally {
      this.fileModalState.loadingAttachments = false;
    }

    this.renderFileModalVaultAttachments();
  }

  getFileModalSelectedVault() {
    const selectedVaultId = String(this.fileModalState.selectedVaultId || '').trim();
    return this.fileModalState.vaults.find((vault) => vault.id === selectedVaultId) || null;
  }

  extractVaultAttachmentsForWorkspaceModal(record, vaultName) {
    const payload = record?.payload;
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      return [];
    }

    const attachments = Array.isArray(payload.attachments) ? payload.attachments : [];
    return attachments
      .filter((attachment) => attachment && String(attachment.content_base64 || '').trim())
      .map((attachment, index) => ({
        source: 'vault',
        id: `${record.id}:${attachment.id || index}:${attachment.name || 'attachment'}`,
        name: String(attachment.name || `attachment-${index + 1}`),
        size: Number(attachment.size_bytes || 0),
        mimeType: String(attachment.mime_type || 'application/octet-stream'),
        kind: String(attachment.kind || ''),
        contentBase64: String(attachment.content_base64 || ''),
        recordId: String(record.id || ''),
        recordLabel: String(record.label || 'Untitled vault entry'),
        recordType: String(record.type || ''),
        workspaceId: String(record.workspace_id || ''),
        vaultId: String(record.vault_id || this.fileModalState.selectedVaultId || ''),
        vaultName: String(vaultName || 'Vault'),
        updatedAt: record.updated_at || record.created_at || ''
      }));
  }

  renderFileModalVaultAttachments(overrideMessage = '') {
    const { vaultAttachmentList, vaultLockedState, vaultUnlockBtn, vaultSearchInput } = this.fileModalElements;
    if (!vaultAttachmentList || !vaultLockedState) {
      return;
    }

    const state = this.fileModalState;
    const selectedIds = new Set((state.selectedItems || []).map((item) => item.id));

    if (vaultUnlockBtn) {
      vaultUnlockBtn.disabled = !state.selectedVaultId;
    }

    if (vaultSearchInput) {
      vaultSearchInput.disabled = state.loadingVaults || state.loadingAttachments || !state.selectedVaultId || Boolean(state.vaultStatus?.locked);
    }

    vaultLockedState.hidden = true;

    if (overrideMessage) {
      vaultAttachmentList.innerHTML = `<div class="hub-file-vault-empty">${this.escapeHtml(overrideMessage)}</div>`;
      return;
    }

    if (state.loadingVaults) {
      vaultAttachmentList.innerHTML = '<div class="hub-file-vault-empty">Loading vaults...</div>';
      return;
    }

    if (!state.vaults.length) {
      vaultAttachmentList.innerHTML = '<div class="hub-file-vault-empty">No vaults available yet. Create one in Vault first.</div>';
      return;
    }

    if (state.loadingAttachments) {
      vaultAttachmentList.innerHTML = '<div class="hub-file-vault-empty">Loading files from the selected vault...</div>';
      return;
    }

    if (!state.selectedVaultId) {
      vaultAttachmentList.innerHTML = '<div class="hub-file-vault-empty">Select a vault to browse its attached files.</div>';
      return;
    }

    if (!state.vaultStatus?.available) {
      vaultAttachmentList.innerHTML = '<div class="hub-file-vault-empty">That vault is not available right now.</div>';
      return;
    }

    if (state.vaultStatus?.locked) {
      vaultLockedState.hidden = false;
      if (vaultSearchInput) {
        vaultSearchInput.disabled = true;
      }
      vaultAttachmentList.innerHTML = '<div class="hub-file-vault-empty">Unlock the selected vault to browse attached files.</div>';
      return;
    }

    const query = String(state.searchQuery || '').trim();
    const filteredAttachments = state.vaultAttachments.filter((item) => {
      if (!query) {
        return true;
      }

      const haystack = [
        item.name,
        item.recordLabel,
        item.workspaceId,
        item.vaultName
      ].join(' ').toLowerCase();
      return haystack.includes(query);
    });

    if (!filteredAttachments.length) {
      vaultAttachmentList.innerHTML = state.vaultAttachments.length
        ? '<div class="hub-file-vault-empty">No vault files match this search.</div>'
        : '<div class="hub-file-vault-empty">No attached files were found in this vault yet. Add files to a vault entry first.</div>';
      return;
    }

    vaultAttachmentList.innerHTML = filteredAttachments.map((item) => {
      const isSelected = selectedIds.has(item.id);
      const workspaceMeta = item.workspaceId ? `Workspace ${item.workspaceId}` : 'Global entry';
      return `
        <button type="button" class="hub-file-vault-item${isSelected ? ' is-selected' : ''}" data-vault-attachment-id="${this.escapeHtml(item.id)}">
          <span class="hub-file-vault-item-icon">${this.renderFileModalVaultIcon(item)}</span>
          <span class="hub-file-vault-item-main">
            <span class="hub-file-vault-item-name">${this.escapeHtml(item.name)}</span>
            <span class="hub-file-vault-item-meta">${this.escapeHtml(`${item.recordLabel} • ${this.formatFileSize(item.size || 0)}`)}</span>
            <span class="hub-file-vault-item-detail">${this.escapeHtml(`${item.vaultName} • ${workspaceMeta}`)}</span>
          </span>
          <span class="hub-file-vault-item-check" aria-hidden="true"></span>
        </button>
      `;
    }).join('');
  }

  renderFileModalVaultIcon(item) {
    const mimeType = String(item?.mimeType || '');
    const kind = String(item?.kind || '');

    if (kind === 'image' || mimeType.startsWith('image/')) {
      return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M21,19V5A2,2 0 0,0 19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19M8.5,11A1.5,1.5 0 0,1 10,12.5A1.5,1.5 0 0,1 8.5,14A1.5,1.5 0 0,1 7,12.5A1.5,1.5 0 0,1 8.5,11M5,19L8.5,14.5L11,17.5L14.5,13L19,19H5Z"/></svg>';
    }

    return '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13Z"/></svg>';
  }

  toggleVaultAttachmentSelection(attachmentId) {
    const index = this.fileModalState.selectedItems.findIndex((item) => item.id === attachmentId);
    if (index >= 0) {
      this.fileModalState.selectedItems.splice(index, 1);
    } else {
      const attachment = this.fileModalState.vaultAttachments.find((item) => item.id === attachmentId);
      if (!attachment) {
        return;
      }
      this.fileModalState.selectedItems.push({ ...attachment });
    }

    this.renderFileModalSelectionPreview();
    this.renderFileModalVaultAttachments();
  }

  async unlockSelectedVaultForFileModal() {
    const selectedVaultId = String(this.fileModalState.selectedVaultId || '').trim();
    const { vaultUnlockPassword, vaultUnlockBtn } = this.fileModalElements;
    const password = String(vaultUnlockPassword?.value || '').trim();

    if (!selectedVaultId) {
      if (window.Toast) window.Toast.error('Select a vault first');
      return;
    }

    if (!password) {
      if (window.Toast) window.Toast.error('Enter the vault password first');
      return;
    }

    if (vaultUnlockBtn) {
      vaultUnlockBtn.disabled = true;
      vaultUnlockBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Unlocking';
    }

    try {
      const response = await fetch(`/api/vault/unlock?vault_id=${encodeURIComponent(selectedVaultId)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          vault_id: selectedVaultId,
          vault_password: password
        })
      });

      const contentType = response.headers.get('content-type') || '';
      const data = contentType.includes('application/json') ? await response.json() : null;
      if (!response.ok) {
        throw new Error(data?.error || 'Failed to unlock vault');
      }

      if (vaultUnlockPassword) {
        vaultUnlockPassword.value = '';
      }

      if (window.Toast) window.Toast.success('Vault unlocked');
      await this.loadFileModalVaultAttachments();
    } catch (error) {
      console.error('Failed to unlock vault for workspace file modal:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to unlock vault');
    } finally {
      if (vaultUnlockBtn) {
        vaultUnlockBtn.disabled = false;
        vaultUnlockBtn.textContent = 'Unlock';
      }
    }
  }

  base64ToBytes(base64Value) {
    const binary = window.atob(String(base64Value || '').trim());
    const length = binary.length;
    const bytes = new Uint8Array(length);
    for (let index = 0; index < length; index += 1) {
      bytes[index] = binary.charCodeAt(index);
    }
    return bytes;
  }

  buildWorkspaceFileFromVaultAttachment(item) {
    const bytes = this.base64ToBytes(item.contentBase64);
    return new File([bytes], item.name || 'vault-file', {
      type: item.mimeType || 'application/octet-stream',
      lastModified: item.updatedAt ? new Date(item.updatedAt).getTime() : Date.now()
    });
  }

  async uploadSelectedFileModalItems() {
    const { submitBtn, titleInput, notesInput } = this.fileModalElements;
    const items = Array.isArray(this.fileModalState.selectedItems) ? this.fileModalState.selectedItems : [];

    if (!items.length) {
      return;
    }

    const title = String(titleInput?.value || '').trim();
    const notes = String(notesInput?.value || '').trim();

    if (submitBtn) {
      submitBtn.disabled = true;
      submitBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Adding...';
    }

    try {
      for (const item of items) {
        const file = item.source === 'vault'
          ? this.buildWorkspaceFileFromVaultAttachment(item)
          : item.file;

        if (!file) {
          throw new Error(`Missing file data for ${item.name || 'selected item'}`);
        }

        const formData = new FormData();
        formData.append('file', file);
        formData.append('workspace_id', this.workspaceId);
        if (title) formData.append('title', title);
        if (notes) formData.append('notes', notes);

        const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`, {
          method: 'POST',
          body: formData
        });

        if (!response.ok) {
          throw new Error(`Failed to add ${item.name || 'file'} to the workspace`);
        }
      }

      if (window.Toast) {
        window.Toast.success(items.length === 1 ? 'File added to workspace' : 'Files added to workspace');
      }
      await this.loadFiles();

      const modal = typeof bootstrap !== 'undefined' ? bootstrap.Modal.getInstance(this.fileModalElements.modal) : null;
      modal?.hide();
    } catch (error) {
      console.error('Workspace file modal upload failed:', error);
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to add file to workspace');
      }
    } finally {
      if (submitBtn) {
        submitBtn.disabled = this.fileModalState.selectedItems.length === 0;
        submitBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M9,16V10H5L12,3L19,10H15V16H9M5,20V18H19V20H5Z"/></svg> Add to Workspace';
      }
    }
  }

  /**
   * Cache DOM elements
   */
  cacheElements() {
    this.elements = {
      // Header elements
      workspaceName: document.getElementById('workspace-name'),
      workspaceDescription: document.getElementById('workspace-description'),
      workspaceBreadcrumb: document.getElementById('workspace-breadcrumb-name'),
      openCanvasBtn: document.getElementById('open-canvas-btn'),

      // Stats
      agentCount: document.getElementById('workspace-agent-count'),
      taskCount: document.getElementById('workspace-task-count'),
      sessionCount: document.getElementById('workspace-session-count'),

      // Quick input
      smartInput: document.getElementById('workspace-detail-input'),
      smartSubmit: document.getElementById('workspace-detail-submit'),

      // Lists
      agentsList: document.getElementById('workspace-detail-agents-list'),
      sessionsList: document.getElementById('workspace-detail-sessions-list'),
      filesList: document.getElementById('workspace-detail-files-list'),
      notesList: document.getElementById('workspace-detail-notes-list'),
      directoriesList: document.getElementById('workspace-detail-directories-list'),
      mcpList: document.getElementById('workspace-detail-mcp-list'),
      settingsSummary: document.getElementById('workspace-detail-settings-summary'),
      settingsManagedSkills: document.getElementById('workspace-detail-settings-managed-skills'),
      skillsList: document.getElementById('workspace-detail-skills-list'),
      schedulesList: document.getElementById('workspace-detail-schedules-list'),
      childrenList: document.getElementById('workspace-detail-children-list'),

      // Panels
      configPanel: document.getElementById('workspace-detail-settings-panel'),
      configContent: document.getElementById('workspace-detail-config-content'),
      configToggleBtn: document.getElementById('workspace-detail-config-toggle'),
      configToggleLabel: document.getElementById('workspace-detail-config-toggle-label'),
      configPresetChip: document.getElementById('workspace-detail-config-preset-chip'),
      configConnectionsChip: document.getElementById('workspace-detail-config-connections-chip'),
      configSkillsChip: document.getElementById('workspace-detail-config-skills-chip'),
      childrenPanel: document.getElementById('workspace-detail-children-panel'),
      childrenCount: document.getElementById('workspace-detail-children-count'),

      // Buttons
      addTaskBtn: document.getElementById('workspace-detail-add-task'),
      addAgentBtn: document.getElementById('workspace-detail-add-agent-btn'),
      refreshTasksBtn: document.getElementById('workspace-detail-refresh-tasks'),
      newSessionBtn: document.getElementById('workspace-detail-new-session'),
      refreshSessionsBtn: document.getElementById('workspace-detail-refresh-sessions'),
      addFileBtn: document.getElementById('workspace-detail-add-file'),
      addNoteBtn: document.getElementById('workspace-detail-add-note'),
      copyNotesBtn: document.getElementById('workspace-detail-copy-notes'),
      addDirectoryBtn: document.getElementById('workspace-detail-add-directory'),
      addMcpBtn: document.getElementById('workspace-detail-add-mcp'),
      refreshMcpBtn: document.getElementById('workspace-detail-refresh-mcp'),
      refreshSettingsBtn: document.getElementById('workspace-detail-refresh-settings'),
      addSkillBtn: document.getElementById('workspace-detail-add-skill'),
      refreshSkillsBtn: document.getElementById('workspace-detail-refresh-skills'),
      viewSchedulesBtn: document.getElementById('workspace-detail-view-schedules'),
      homeAssistantQuickPlanBtn: document.getElementById('homeAssistantQuickPlan'),
      homeAssistantQuickTasksBtn: document.getElementById('homeAssistantQuickTasks'),
      homeAssistantQuickNotesBtn: document.getElementById('homeAssistantQuickNotes'),
      homeAssistantQuickReviewBtn: document.getElementById('homeAssistantQuickReview'),

      // View toggle
      viewListBtn: document.getElementById('workspace-detail-view-list'),
      viewBoardBtn: document.getElementById('workspace-detail-view-board'),

      // Board elements
      agentsPanel: document.getElementById('workspace-detail-agents-panel'),
      tasksBoard: document.getElementById('workspace-detail-tasks-board'),
      boardColumns: document.getElementById('workspace-detail-board-columns'),
      boardEmpty: document.getElementById('workspace-detail-board-empty'),
      boardSetupBtn: document.getElementById('workspace-detail-board-setup'),

      // Result modal
      taskResultModal: document.getElementById('workspace-detail-task-result-modal'),
      taskResultTitle: document.getElementById('workspace-detail-task-result-title'),
      taskResultId: document.getElementById('workspace-detail-task-result-id'),
      taskResultMeta: document.getElementById('workspace-detail-task-result-meta'),
      taskResultBreakdown: document.getElementById('workspace-detail-task-result-breakdown'),
      taskResultBody: document.getElementById('workspace-detail-task-result-body'),
      taskResultNextSteps: document.getElementById('workspace-detail-task-result-next-steps'),
      taskResultNextStepsCopy: document.getElementById('workspace-detail-task-result-next-steps-copy'),
      taskResultNextStepsActions: document.getElementById('workspace-detail-task-result-next-steps-actions'),
      taskResultCopyBtn: document.getElementById('workspace-detail-task-result-copy'),
      taskExecutionModal: document.getElementById('workspace-detail-task-execution-modal'),
      taskExecutionTitle: document.getElementById('workspace-detail-task-execution-title'),
      taskExecutionId: document.getElementById('workspace-detail-task-execution-id'),
      taskExecutionMeta: document.getElementById('workspace-detail-task-execution-meta'),
      taskExecutionStatus: document.getElementById('workspace-detail-task-execution-status'),
      taskExecutionBreakdown: document.getElementById('workspace-detail-task-execution-breakdown'),
      taskExecutionLog: document.getElementById('workspace-detail-task-execution-log'),
      taskExecutionNextStepBtn: document.getElementById('workspace-detail-task-execution-next-step'),
      taskExecutionViewResultBtn: document.getElementById('workspace-detail-task-execution-view-result'),
      taskConfirmModal: document.getElementById('workspace-detail-task-confirm-modal'),
      taskConfirmEyebrow: document.getElementById('workspace-detail-task-confirm-eyebrow'),
      taskConfirmTitle: document.getElementById('workspace-detail-task-confirm-title'),
      taskConfirmMessage: document.getElementById('workspace-detail-task-confirm-message'),
      taskConfirmMeta: document.getElementById('workspace-detail-task-confirm-meta'),
      taskConfirmDetails: document.getElementById('workspace-detail-task-confirm-details'),
      taskConfirmSequence: document.getElementById('workspace-detail-task-confirm-sequence'),
      taskConfirmStepMode: document.getElementById('workspace-detail-task-confirm-step-mode'),
      taskConfirmStepModeInput: document.getElementById('workspace-detail-task-confirm-step-mode-input'),
      taskConfirmCancelBtn: document.getElementById('workspace-detail-task-confirm-cancel'),
      taskConfirmConfirmBtn: document.getElementById('workspace-detail-task-confirm-confirm'),

      // Assist modal
      taskAssistModal: document.getElementById('workspace-detail-task-assist-modal'),
      taskAssistId: document.getElementById('workspace-detail-task-assist-id'),
      taskAssistMeta: document.getElementById('workspace-detail-task-assist-meta'),
      taskAssistReason: document.getElementById('workspace-detail-task-assist-reason'),
      taskAssistQuestion: document.getElementById('workspace-detail-task-assist-question'),
      taskAssistRecommendationWrap: document.getElementById('workspace-detail-task-assist-recommendation-wrap'),
      taskAssistRecommendation: document.getElementById('workspace-detail-task-assist-recommendation'),
      taskAssistChoiceWrap: document.getElementById('workspace-detail-task-assist-choice-wrap'),
      taskAssistChoiceSummary: document.getElementById('workspace-detail-task-assist-choice-summary'),
      taskAssistChoiceList: document.getElementById('workspace-detail-task-assist-choice-list'),
      taskAssistSpecialistWrap: document.getElementById('workspace-detail-task-assist-specialist-wrap'),
      taskAssistSpecialistCopy: document.getElementById('workspace-detail-task-assist-specialist-copy'),
      taskAssistSpecialistActionBtn: document.getElementById('workspace-detail-task-assist-specialist-action'),
      taskAssistAgent: document.getElementById('workspace-detail-task-assist-agent'),
      taskAssistMessage: document.getElementById('workspace-detail-task-assist-message'),
      taskAssistResponseWrap: document.getElementById('workspace-detail-task-assist-response-wrap'),
      taskAssistResponse: document.getElementById('workspace-detail-task-assist-response'),
      taskAssistRetryBtn: document.getElementById('workspace-detail-task-assist-retry'),
      taskAssistContinueBtn: document.getElementById('workspace-detail-task-assist-continue'),
      taskAssistSwitchBtn: document.getElementById('workspace-detail-task-assist-switch'),
      taskAssistFailBtn: document.getElementById('workspace-detail-task-assist-fail'),

      // Add agent modal
      addAgentModal: document.getElementById('workspace-detail-add-agent-modal'),
      addAgentSelect: document.getElementById('workspace-detail-add-agent-select'),
      addAgentEmpty: document.getElementById('workspace-detail-add-agent-empty'),
      addAgentSubmitBtn: document.getElementById('workspace-detail-add-agent-submit'),
      createAgentBtn: document.getElementById('workspace-detail-create-agent-btn'),

      // Workspace MCP modal
      mcpModal: document.getElementById('workspace-detail-mcp-modal'),
      mcpForm: document.getElementById('workspace-detail-mcp-form'),
      mcpModalTitle: document.getElementById('workspace-detail-mcp-modal-title'),
      mcpModalSubtitle: document.getElementById('workspace-detail-mcp-modal-subtitle'),
      mcpServerSelect: document.getElementById('workspace-detail-mcp-server'),
      mcpServerHelp: document.getElementById('workspace-detail-mcp-server-help'),
      mcpAliasInput: document.getElementById('workspace-detail-mcp-alias'),
      mcpEnabledInput: document.getElementById('workspace-detail-mcp-enabled'),
      mcpScopeInput: document.getElementById('workspace-detail-mcp-scope'),
      mcpConfigField: document.getElementById('workspace-detail-mcp-config-field'),
      mcpConfigDetails: document.getElementById('workspace-detail-mcp-config-details'),
      mcpConfigInput: document.getElementById('workspace-detail-mcp-config'),
      mcpEmailFields: document.getElementById('workspace-detail-mcp-email-fields'),
      mcpEmailAccountSelect: document.getElementById('workspace-detail-mcp-email-account'),
      mcpEmailAccountHelp: document.getElementById('workspace-detail-mcp-email-account-help'),
      mcpEmailAccountSummary: document.getElementById('workspace-detail-mcp-email-account-summary'),
      mcpEmailMailboxInput: document.getElementById('workspace-detail-mcp-email-mailboxes'),
      mcpEmailActionRead: document.getElementById('workspace-detail-mcp-email-action-read'),
      mcpEmailActionSearch: document.getElementById('workspace-detail-mcp-email-action-search'),
      mcpEmailActionDraft: document.getElementById('workspace-detail-mcp-email-action-draft'),
      mcpEmailActionSend: document.getElementById('workspace-detail-mcp-email-action-send'),
      mcpEmailSendConfirmWrap: document.getElementById('workspace-detail-mcp-email-send-confirm-wrap'),
      mcpEmailSendConfirmInput: document.getElementById('workspace-detail-mcp-email-send-confirm'),
      mcpAgentOptions: document.getElementById('workspace-detail-mcp-agent-options'),
      mcpAgentAccessSummary: document.getElementById('workspace-detail-mcp-agent-access-summary'),
      mcpSubmitBtn: document.getElementById('workspace-detail-mcp-submit'),
      settingsForm: document.getElementById('workspace-detail-settings-form'),
      settingsPresetInput: document.getElementById('workspace-detail-settings-preset'),
      settingsModeInput: document.getElementById('workspace-detail-settings-mode'),
      settingsConfirmationInput: document.getElementById('workspace-detail-settings-confirmation'),
      settingsPlanEnabledInput: document.getElementById('workspace-detail-settings-plan-enabled'),
      settingsRequireScanInput: document.getElementById('workspace-detail-settings-require-scan'),
      settingsSaveNotesInput: document.getElementById('workspace-detail-settings-save-notes'),
      settingsSyncTasksInput: document.getElementById('workspace-detail-settings-sync-tasks'),
      settingsAskHandoffInput: document.getElementById('workspace-detail-settings-ask-handoff'),
      settingsPlanningFields: document.getElementById('workspace-detail-settings-planning-fields'),
      settingsPlanningModeInput: document.getElementById('workspace-detail-settings-planning-mode'),
      settingsClarificationModeInput: document.getElementById('workspace-detail-settings-clarification-mode'),
      settingsTasksDirInput: document.getElementById('workspace-detail-settings-tasks-dir'),
      settingsExecutionModeInput: document.getElementById('workspace-detail-settings-execution-mode'),
      settingsWritePRDInput: document.getElementById('workspace-detail-settings-write-prd'),
      settingsWriteTaskListInput: document.getElementById('workspace-detail-settings-write-task-list'),
      settingsRequireBranchInput: document.getElementById('workspace-detail-settings-require-branch'),
      settingsSaveBtn: document.getElementById('workspace-detail-settings-save'),
      skillsModal: document.getElementById('workspace-detail-skills-modal'),
      skillsForm: document.getElementById('workspace-detail-skills-form'),
      skillsModalTitle: document.getElementById('workspace-detail-skills-modal-title'),
      skillsModalSubtitle: document.getElementById('workspace-detail-skills-modal-subtitle'),
      skillNameSelect: document.getElementById('workspace-detail-skill-name'),
      skillNameHelp: document.getElementById('workspace-detail-skill-name-help'),
      skillEnabledInput: document.getElementById('workspace-detail-skill-enabled'),
      skillTrustedInput: document.getElementById('workspace-detail-skill-trusted'),
      skillPlanningFields: document.getElementById('workspace-detail-skill-planning-fields'),
      skillPlanningModeInput: document.getElementById('workspace-detail-skill-planning-mode'),
      skillPlanningClarificationModeInput: document.getElementById('workspace-detail-skill-planning-clarification-mode'),
      skillPlanningTasksDirInput: document.getElementById('workspace-detail-skill-planning-tasks-dir'),
      skillPlanningDefaultExecutionInput: document.getElementById('workspace-detail-skill-planning-default-execution'),
      skillPlanningWritePRDInput: document.getElementById('workspace-detail-skill-planning-write-prd'),
      skillPlanningWriteTaskListInput: document.getElementById('workspace-detail-skill-planning-write-task-list'),
      skillPlanningSyncTasksInput: document.getElementById('workspace-detail-skill-planning-sync-tasks'),
      skillPlanningRequireBranchInput: document.getElementById('workspace-detail-skill-planning-require-branch'),
      skillAgentOptions: document.getElementById('workspace-detail-skill-agent-options'),
      skillAgentAccessSummary: document.getElementById('workspace-detail-skill-agent-access-summary'),
      skillSubmitBtn: document.getElementById('workspace-detail-skills-submit'),

      // Directory explorer modal
      directoryExplorerModal: document.getElementById('workspace-directory-explorer-modal'),
      directoryExplorerTitle: document.getElementById('workspace-directory-explorer-title'),
      directoryExplorerSubtitle: document.getElementById('workspace-directory-explorer-subtitle'),
      directoryExplorerSummary: document.getElementById('workspace-directory-explorer-summary'),
      directoryExplorerBreadcrumb: document.getElementById('workspace-directory-explorer-breadcrumb'),
      directoryExplorerSearch: document.getElementById('workspace-directory-explorer-search'),
      directoryExplorerSortBtn: document.getElementById('workspace-directory-explorer-sort'),
      directoryExplorerRefreshBtn: document.getElementById('workspace-directory-explorer-refresh'),
      directoryExplorerTree: document.getElementById('workspace-directory-explorer-tree'),
      directoryExplorerPreview: document.getElementById('workspace-directory-explorer-preview')
    };
  }

  /**
   * Bind event handlers
   */
  bindEvents() {
    // Quick input
    this.elements.smartSubmit?.addEventListener('click', () => this.handleQuickInput());
    this.elements.smartInput?.addEventListener('keypress', (e) => {
      if (e.key === 'Enter') this.handleQuickInput();
    });

    // Task buttons
    this.elements.addTaskBtn?.addEventListener('click', () => this.showAddTaskModal());
    this.elements.addAgentBtn?.addEventListener('click', () => this.openAddAgentModal());
    this.elements.refreshTasksBtn?.addEventListener('click', () => this.loadTasks());

    // View toggle
    this.elements.viewListBtn?.addEventListener('click', () => this.setView('list'));
    this.elements.viewBoardBtn?.addEventListener('click', () => this.setView('board'));

    // Board setup
    this.elements.boardSetupBtn?.addEventListener('click', () => this.setupBoard());

    // Session buttons
    this.elements.newSessionBtn?.addEventListener('click', () => this.createNewSession());
    this.elements.refreshSessionsBtn?.addEventListener('click', () => this.loadSessions());

    // File buttons
    this.elements.addFileBtn?.addEventListener('click', () => this.showFileModal());

    // Note buttons
    this.elements.addNoteBtn?.addEventListener('click', () => this.showNoteModal());
    this.elements.copyNotesBtn?.addEventListener('click', () => this.copyAllNotesToClipboard());

    // Directory buttons
    this.elements.addDirectoryBtn?.addEventListener('click', () => this.showAddDirectoryModal());
    this.elements.addMcpBtn?.addEventListener('click', () => this.openWorkspaceMCPModal());
    this.bindDirectoryExplorerEvents();
    this.elements.refreshMcpBtn?.addEventListener('click', async () => {
      await this.loadAvailableMCPServers(true).catch((error) => {
        console.warn('Failed to refresh MCP connector catalog:', error);
      });
      await this.loadWorkspace();
    });
    this.elements.mcpList?.addEventListener('click', (event) => this.handleWorkspaceMCPListClick(event));
    this.elements.mcpForm?.addEventListener('submit', (event) => {
      event.preventDefault();
      this.submitWorkspaceMCPModal();
    });
    this.elements.mcpServerSelect?.addEventListener('change', () => {
      this.handleWorkspaceMCPServerChange();
    });
    [
      this.elements.mcpEmailActionRead,
      this.elements.mcpEmailActionSearch,
      this.elements.mcpEmailActionDraft,
      this.elements.mcpEmailActionSend
    ].forEach((checkbox) => {
      checkbox?.addEventListener('change', () => this.handleWorkspaceMCPEmailActionChange());
    });
    this.elements.mcpEmailAccountSelect?.addEventListener('change', () => this.updateWorkspaceMCPEmailAccountSummary());
    this.elements.mcpAgentOptions?.addEventListener('change', () => this.updateWorkspaceMCPAgentAccessSummary());
    this.elements.mcpModal?.addEventListener('hidden.bs.modal', () => this.resetWorkspaceMCPModal());

    // Workspace settings
    this.elements.configToggleBtn?.addEventListener('click', () => this.toggleWorkspaceConfigExpanded());
    this.elements.refreshSettingsBtn?.addEventListener('click', () => this.loadWorkspace());
    this.elements.settingsForm?.addEventListener('submit', (event) => {
      event.preventDefault();
      this.saveWorkspaceSettings();
    });
    this.elements.settingsPresetInput?.addEventListener('change', () => this.handleWorkspaceSettingsPresetChange());
    [
      this.elements.settingsModeInput,
      this.elements.settingsConfirmationInput,
      this.elements.settingsPlanEnabledInput,
      this.elements.settingsRequireScanInput,
      this.elements.settingsSaveNotesInput,
      this.elements.settingsSyncTasksInput,
      this.elements.settingsAskHandoffInput,
      this.elements.settingsPlanningModeInput,
      this.elements.settingsClarificationModeInput,
      this.elements.settingsTasksDirInput,
      this.elements.settingsExecutionModeInput,
      this.elements.settingsWritePRDInput,
      this.elements.settingsWriteTaskListInput,
      this.elements.settingsRequireBranchInput
    ].forEach((input) => {
      input?.addEventListener('change', () => this.handleWorkspaceSettingsFieldChange());
    });

    // Skill buttons
    this.elements.addSkillBtn?.addEventListener('click', () => this.openWorkspaceSkillModal());
    this.elements.refreshSkillsBtn?.addEventListener('click', async () => {
      await this.loadAvailableSkills(true).catch((error) => {
        console.warn('Failed to refresh skill catalog:', error);
      });
      await this.loadWorkspace();
    });
    this.elements.skillsList?.addEventListener('click', (event) => this.handleWorkspaceSkillListClick(event));
    this.elements.skillsForm?.addEventListener('submit', (event) => {
      event.preventDefault();
      this.submitWorkspaceSkillModal();
    });
    this.elements.skillNameSelect?.addEventListener('change', () => this.handleWorkspaceSkillSelectionChange());
    this.elements.skillAgentOptions?.addEventListener('change', () => this.updateWorkspaceSkillAgentAccessSummary());
    this.elements.skillsModal?.addEventListener('hidden.bs.modal', () => this.resetWorkspaceSkillModal());
    this.elements.skillsModal?.addEventListener('shown.bs.modal', () => {
      this.applyTopBackdropLayer('workspace-detail-backdrop-skills');
      this.handleWorkspaceSkillSelectionChange();
    });

    // Schedule buttons
    this.elements.viewSchedulesBtn?.addEventListener('click', () => this.showSchedulesModal());
    this.elements.taskResultCopyBtn?.addEventListener('click', () => this.copyCurrentTaskResult());
    this.elements.taskExecutionViewResultBtn?.addEventListener('click', () => {
      if (this.currentExecutionTaskId) {
        this.showTaskResult(this.currentExecutionTaskId, { closeExecutionModal: true });
      }
    });
    this.elements.taskExecutionNextStepBtn?.addEventListener('click', () => this.advanceCurrentExecutionStep());
    this.elements.taskResultModal?.addEventListener('shown.bs.modal', () => {
      this.applyTopBackdropLayer('workspace-detail-backdrop-result');
    });
    this.elements.taskExecutionModal?.addEventListener('shown.bs.modal', () => {
      this.applyTopBackdropLayer('workspace-detail-backdrop-execution');
    });
    this.elements.mcpModal?.addEventListener('shown.bs.modal', () => {
      this.applyTopBackdropLayer('workspace-detail-backdrop-mcp');
    });
    this.elements.taskExecutionModal?.addEventListener('hidden.bs.modal', () => {
      this.stopExecutionMonitor();
      this.currentExecutionTaskId = null;
      this.setExecutionNextStepEnabled(false);
      this.renderTaskExecutionBreakdown(null);
    });
    this.elements.taskConfirmCancelBtn?.addEventListener('click', () => this.handleTaskConfirmChoice(false));
    this.elements.taskConfirmConfirmBtn?.addEventListener('click', () => this.handleTaskConfirmChoice(true));
    this.elements.taskConfirmModal?.addEventListener('hidden.bs.modal', () => this.handleTaskConfirmHidden());
    this.elements.taskAssistRetryBtn?.addEventListener('click', () => this.submitTaskAssist('retry'));
    this.elements.taskAssistContinueBtn?.addEventListener('click', () => this.submitTaskAssist('continue_with_instruction'));
    this.elements.taskAssistSwitchBtn?.addEventListener('click', () => this.submitTaskAssist('switch_agent_retry'));
    this.elements.taskAssistFailBtn?.addEventListener('click', () => this.submitTaskAssist('mark_failed'));
    this.elements.taskAssistSpecialistActionBtn?.addEventListener('click', () => this.handleAssistSpecialistAction());
    this.elements.taskAssistAgent?.addEventListener('change', () => this.updateAssistSwitchButtonState());
    this.elements.addAgentSubmitBtn?.addEventListener('click', () => this.addSelectedAgentToWorkspace());
    this.elements.createAgentBtn?.addEventListener('click', () => this.openCreateAgentFlow());
    this.elements.addAgentModal?.addEventListener('show.bs.modal', () => { this.populateAddAgentOptions(); });

    // Make workspace name and description editable
    this.makeEditable(this.elements.workspaceName, 'name', false);
    this.makeEditable(this.elements.workspaceDescription, 'description', true);

    // Subscribe to EventBus events for auto-refresh
    console.log('[workspace-detail] EventBus available:', !!window.EventBus);
    if (window.EventBus) {
      console.log('[workspace-detail] Registering EventBus listeners');
      EventBus.on('task:created', (data) => {
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          this.loadTasks();
        }
      }, 'workspaceDetail');

      EventBus.on('task:updated', (data) => {
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          this.loadTasks();
        }
      }, 'workspaceDetail');

      EventBus.on('session:created', (data) => {
        if (!data?.folderId || data.folderId === this.workspaceId) {
          this.loadSessions();
        }
      }, 'workspaceDetail');

      EventBus.on('note:created', (data) => {
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          this.loadNotes();
        }
      }, 'workspaceDetail');

      EventBus.on('note:updated', (data) => {
        console.log('[workspace-detail] note:updated received', data?.workspaceId, 'this.workspaceId:', this.workspaceId);
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          console.log('[workspace-detail] calling loadNotes');
          this.loadNotes();
        }
      }, 'workspaceDetail');

      EventBus.on('workspace:files:updated', (data) => {
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          this.loadFiles();
        }
      }, 'workspaceDetail');

      EventBus.on('agent:created', async () => {
        await this.loadAgentCatalog(true);
        await this.populateAddAgentOptions();
      }, 'workspaceDetail');
    }
  }

  bindDirectoryExplorerEvents() {
    this.elements.directoryExplorerRefreshBtn?.addEventListener('click', () => this.loadDirectoryExplorerFiles({ force: true }));

    this.elements.directoryExplorerSortBtn?.addEventListener('click', () => {
      this.directoryExplorer.sortDirection = this.directoryExplorer.sortDirection === 'asc' ? 'desc' : 'asc';
      this.renderDirectoryExplorer();
    });

    this.elements.directoryExplorerSearch?.addEventListener('input', (event) => {
      this.directoryExplorer.searchQuery = (event.target.value || '').trim();
      this.renderDirectoryExplorer();
    });

    this.elements.directoryExplorerTree?.addEventListener('click', (event) => {
      const toggleButton = event.target.closest('[data-action="toggle-folder"]');
      if (toggleButton) {
        const path = this.decodeDataPath(toggleButton.dataset.path);
        this.toggleDirectoryNode(path);
        return;
      }

      const nodeButton = event.target.closest('[data-action="select-node"]');
      if (nodeButton) {
        const path = this.decodeDataPath(nodeButton.dataset.path);
        const type = nodeButton.dataset.type || 'file';
        this.selectDirectoryNode(path, type, { autoExpand: true });
      }
    });

    this.elements.directoryExplorerTree?.addEventListener('keydown', (event) => {
      const nodeButton = event.target.closest('[data-action="select-node"]');
      if (!nodeButton) return;
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;

      const path = this.decodeDataPath(nodeButton.dataset.path);
      const type = nodeButton.dataset.type || 'file';
      if (type !== 'dir') return;

      if (event.key === 'ArrowRight') {
        event.preventDefault();
        this.expandDirectoryNode(path);
      } else if (event.key === 'ArrowLeft') {
        event.preventDefault();
        this.collapseDirectoryNode(path);
      }
    });

    this.elements.directoryExplorerPreview?.addEventListener('click', (event) => {
      const entryButton = event.target.closest('[data-action="select-node"]');
      if (!entryButton) return;
      const path = this.decodeDataPath(entryButton.dataset.path);
      const type = entryButton.dataset.type || 'file';
      this.selectDirectoryNode(path, type, { autoExpand: true });
    });

    this.elements.directoryExplorerBreadcrumb?.addEventListener('click', (event) => {
      const crumb = event.target.closest('[data-action="breadcrumb"]');
      if (!crumb) return;
      const path = this.decodeDataPath(crumb.dataset.path);
      this.selectDirectoryNode(path, 'dir', { autoExpand: true });
    });

    this.elements.directoryExplorerModal?.addEventListener('hidden.bs.modal', () => {
      this.abortDirectoryPreviewRequest();
      this.directoryExplorer.searchQuery = '';
      if (this.elements.directoryExplorerSearch) {
        this.elements.directoryExplorerSearch.value = '';
      }
    });
  }

  /**
   * Update workspace via API
   * @param {Object} updates - Fields to update (name, description, etc.)
   */
  async updateWorkspace(updates) {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updates)
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to update workspace');
    }
    return response.json();
  }

  /**
   * Create inline editable element
   * @param {HTMLElement} element - The element to make editable
   * @param {string} field - Field name ('name' or 'description')
   * @param {boolean} isMultiline - Whether to use textarea
   */
  makeEditable(element, field, isMultiline = false) {
    if (!element) return;

    element.classList.add('is-editable');

    // Find the edit button in the container
    const container = element.closest('.workspace-editable-field');
    const editBtn = container?.querySelector('.workspace-edit-btn');

    const startEdit = () => {
      const currentValue = element.textContent || '';

      // Create input/textarea
      const input = document.createElement(isMultiline ? 'textarea' : 'input');
      input.className = 'workspace-detail-inline-edit';
      input.value = currentValue === 'No description' ? '' : currentValue;
      if (!isMultiline) {
        input.type = 'text';
      } else {
        input.rows = 2;
      }

      // Store original display
      const originalDisplay = element.style.display;

      // Hide text and edit button, show input
      element.style.display = 'none';
      if (editBtn) editBtn.style.display = 'none';
      element.parentNode.insertBefore(input, element.nextSibling);
      input.focus();
      input.select();

      const finishEdit = async (save) => {
        const newValue = input.value.trim();
        input.remove();
        element.style.display = originalDisplay || '';
        if (editBtn) editBtn.style.display = '';

        if (!save || newValue === currentValue) return;

        // For name, don't allow empty
        if (field === 'name' && !newValue) {
          if (window.Toast) window.Toast.error('Name cannot be empty');
          return;
        }

        try {
          await this.updateWorkspace({ [field]: newValue });
          element.textContent = newValue || (field === 'description' ? 'No description' : 'Workspace');

          // Update local workspace object
          if (this.workspace) {
            this.workspace[field] = newValue;
          }

          // Update breadcrumb if name changed
          if (field === 'name' && this.elements.workspaceBreadcrumb) {
            this.elements.workspaceBreadcrumb.textContent = newValue || 'Workspace';
          }

          if (window.Toast) window.Toast.success(`${field === 'name' ? 'Name' : 'Description'} updated`);
        } catch (err) {
          console.error(`Failed to update ${field}:`, err);
          if (window.Toast) window.Toast.error(`Failed to update ${field}`);
        }
      };

      input.addEventListener('blur', () => finishEdit(true));
      input.addEventListener('keydown', (evt) => {
        if (evt.key === 'Enter' && !isMultiline) {
          evt.preventDefault();
          input.blur();
        } else if (evt.key === 'Enter' && isMultiline && evt.ctrlKey) {
          evt.preventDefault();
          input.blur();
        } else if (evt.key === 'Escape') {
          evt.preventDefault();
          finishEdit(false);
        }
      });
    };

    // Add click handler to edit button
    if (editBtn) {
      editBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        startEdit();
      });
    }
  }

  /**
   * Load workspace data
   */
  async loadWorkspace() {
    try {
      const response = await fetch(`/api/orchestration/workspace?id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load workspace');

      this.workspace = await response.json();
      await this.loadAvailableSkills().catch((error) => {
        console.warn('Failed to load skill catalog for workspace detail:', error);
      });
      this.workspaceSettings = this.normalizeWorkspaceSettings(
        this.workspace?.workspace_settings || this.workspace?.shared_data?.workspace_settings || {}
      );
      this.workspaceSettingsEffectiveBehavior = this.normalizeWorkspaceSettingsEffectiveBehavior(
        this.workspace?.workspace_settings_effective_behavior,
        this.workspaceSettings
      );
      if (window.OriAskRouting && typeof window.OriAskRouting.refreshWorkspaceIdentity === 'function') {
        window.OriAskRouting.refreshWorkspaceIdentity({
          workspace_id: this.workspaceId,
          page_path: window.location?.pathname || '',
          surface: window.location?.pathname?.includes('/canvas') ? 'workspace_canvas' : 'workspace_detail',
          origin: 'ask_ori'
        });
      }
      await this.renderWorkspaceInfo();
      this.renderWorkspaceMCPBindings();
      this.renderWorkspaceSettings();
      this.renderWorkspaceSkillBindings();
      this.renderAgentGroups();
      this.refreshHomeAssistantQuickPrompts();
    } catch (error) {
      console.error('Failed to load workspace:', error);
      if (window.Toast) window.Toast.error('Failed to load workspace');
    }
  }

  /**
   * Render workspace information in header
   */
  async renderWorkspaceInfo() {
    if (!this.workspace) return;

    if (this.elements.workspaceName) {
      this.elements.workspaceName.textContent = this.workspace.name || 'Unnamed Workspace';
    }
    if (this.elements.workspaceDescription) {
      if (this.workspace.description) {
        this.elements.workspaceDescription.textContent = this.workspace.description;
        this.elements.workspaceDescription.style.opacity = '1';
      } else {
        this.elements.workspaceDescription.textContent = 'No description';
        this.elements.workspaceDescription.style.opacity = '0.6';
      }
    }
    if (this.elements.workspaceBreadcrumb) {
      this.elements.workspaceBreadcrumb.textContent = this.workspace.name || 'Workspace';
    }
    if (this.elements.openCanvasBtn) {
      this.elements.openCanvasBtn.href = `/workspaces/${this.workspaceId}/canvas`;
    }

    // Update stats
    const agents = this.workspace.agent_instances || [];
    if (this.elements.agentCount) {
      this.elements.agentCount.textContent = agents.length;
    }

    // Load children workspaces from tree API
    await this.loadChildren();
  }

  /**
   * Load children workspaces from the tree API
   */
  async loadChildren() {
    try {
      // Fetch the full workspace tree to find children
      const response = await fetch('/api/workspaces?tree=true');
      if (!response.ok) {
        this.children = [];
        this.renderChildren();
        return;
      }

      const data = await response.json();
      const folders = data.folders || [];

      // Find this workspace in the tree and get its children
      const findWorkspace = (workspaces, targetId) => {
        for (const ws of workspaces) {
          if (ws.id === targetId) {
            return ws;
          }
          if (ws.children && ws.children.length > 0) {
            const found = findWorkspace(ws.children, targetId);
            if (found) return found;
          }
        }
        return null;
      };

      const currentWorkspace = findWorkspace(folders, this.workspaceId);
      this.children = currentWorkspace?.children || [];
      this.renderChildren();
    } catch (error) {
      console.error('Failed to load children workspaces:', error);
      this.children = [];
      this.renderChildren();
    }
  }

  /**
   * Render children workspaces
   */
  renderChildren() {
    // Show/hide the children panel based on whether there are children
    if (this.elements.childrenPanel) {
      if (this.children.length > 0) {
        this.elements.childrenPanel.style.display = '';
      } else {
        this.elements.childrenPanel.style.display = 'none';
        return;
      }
    }

    // Update count badge
    if (this.elements.childrenCount) {
      this.elements.childrenCount.textContent = this.children.length;
    }

    if (!this.elements.childrenList) return;

    this.elements.childrenList.innerHTML = this.children.map(child => {
      const name = child.name || 'Unnamed Workspace';
      const description = child.description || '';
      const colorAccent = child.color ? `border-left: 3px solid ${child.color};` : '';
      const childCount = (child.children || []).length;
      const hasChildren = childCount > 0;

      return `
        <div class="workspace-detail-child-card"
             data-workspace-id="${child.id}"
             role="button"
             tabindex="0"
             aria-label="Open workspace ${this.escapeHtml(name)}"
             onclick="window.location.href='/workspaces/${child.id}'"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.location.href='/workspaces/${child.id}'; }"
             style="${colorAccent}">
          <div class="child-name">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" style="color: ${child.color || 'var(--text-secondary)'}; flex-shrink: 0;">
              ${hasChildren
                ? '<path d="M12,3L2,12H5V20H19V12H22L12,3M12,8.75A2.25,2.25 0 0,1 14.25,11A2.25,2.25 0 0,1 12,13.25A2.25,2.25 0 0,1 9.75,11A2.25,2.25 0 0,1 12,8.75M12,15C13.5,15 16.5,15.75 16.5,17.25V18H7.5V17.25C7.5,15.75 10.5,15 12,15Z"/>'
                : '<path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>'}
            </svg>
            <span>${this.escapeHtml(name)}</span>
          </div>
          ${description ? `<div class="child-desc" title="${this.escapeHtml(description)}">${this.escapeHtml(description)}</div>` : ''}
          <div class="child-meta">
            ${hasChildren ? `<span class="badge bg-secondary" style="font-size: 0.65rem;">${childCount} sub</span>` : ''}
            <span>Click to open</span>
          </div>
        </div>
      `;
    }).join('');
  }

  /**
   * Load tasks for the workspace
   */
  async loadTasks() {
    this.tasksLoading = true;
    this.tasksLoadFailed = false;
    this.renderAgentGroups();

    try {
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load tasks');

      const data = await response.json();
      this.tasks = data.tasks || [];
      if (this.currentView === 'board' && this.boardConfig) {
        this.renderBoard();
      }
      this.tasksLoadFailed = false;
    } catch (error) {
      console.error('Failed to load tasks:', error);
      this.tasks = [];
      this.tasksLoadFailed = true;
    } finally {
      this.tasksLoading = false;
      this.renderTasks();

      if (this.elements.taskCount) {
        this.elements.taskCount.textContent = this.tasks.length;
      }
      this.refreshHomeAssistantQuickPrompts();
    }
  }

  /**
   * Render tasks grouped by agent
   */
  renderTasks() {
    this.renderAgentGroups();
  }

  renderAgentDetailLink(agentName, encodedAgentName) {
    if (!agentName || !encodedAgentName) {
      return `<span>${this.escapeHtml(agentName || '')}</span>`;
    }

    const safeAgentName = this.escapeHtml(agentName);
    const safeHref = `/agents/${encodedAgentName}`;
    return `
      <a href="${safeHref}"
         class="workspace-detail-agent-link"
         title="Open ${safeAgentName} details"
         aria-label="Open ${safeAgentName} details"
         onclick="event.stopPropagation();">
        <span class="workspace-detail-agent-link-label">${safeAgentName}</span>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M14,3H21V10H19V6.41L12.41,13L11,11.59L17.59,5H14V3M5,5H10V7H7V17H17V14H19V19H5V5Z"/>
        </svg>
      </a>
    `;
  }

  isWorkspaceEntryAgent(agentName) {
    const entryAgentName = String(this.workspace?.entry_agent_name || '').trim();
    if (!entryAgentName || !agentName) return false;
    return this.normalizeAgentName(entryAgentName) === this.normalizeAgentName(agentName);
  }

  renderWorkspaceAgentRoleBadge(agentName) {
    if (!this.isWorkspaceEntryAgent(agentName)) return '';
    return '<span class="workspace-detail-agent-role-badge workspace-manager">Workspace Manager</span>';
  }

  renderAgentGroups() {
    if (!this.elements.agentsList) return;

    const groups = this.buildAgentGroups();
    if (groups.length === 0) {
      if (this.tasksLoading) {
        this.elements.agentsList.innerHTML = '<div class="workspace-detail-loading">Loading agents...</div>';
      } else {
        this.elements.agentsList.innerHTML = `
          <div class="workspace-detail-empty">
            No agents yet. Add an agent to this workspace to assign tasks.
            <div class="mt-2">
              <button type="button" class="modern-btn modern-btn-primary modern-btn-sm" onclick="window.workspaceDetail?.openAddAgentModal()">Add Agent</button>
            </div>
          </div>
        `;
      }
      return;
    }

    this.elements.agentsList.innerHTML = groups.map((group) => {
      const taskCount = group.tasks.length;
      const taskLabel = `${taskCount} task${taskCount === 1 ? '' : 's'}`;
      const instanceLabel = group.instanceCount > 1 ? `${group.instanceCount} instances` : '';
      const cardMeta = [instanceLabel, taskLabel].filter(Boolean).join(' · ');
      const capabilityBadges = group.isUnassigned ? '' : this.renderAgentCapabilityBadges(group.name);
      const agentProfile = group.isUnassigned ? null : this.getAgentProfile(group.name);
      const modelLabel = agentProfile?.model
        ? `<span class="workspace-detail-agent-model-badge">${this.escapeHtml(agentProfile.model)}</span>`
        : '';
      const encodedAgentName = encodeURIComponent(group.name);
      const canFlip = !group.isUnassigned;
      const isFlipped = canFlip && this.flippedAgentCards.has(group.key);
      const roleBadge = group.isUnassigned ? '' : this.renderWorkspaceAgentRoleBadge(group.name);
      const removeLabel = group.instanceCount > 1
        ? `Remove all ${group.instanceCount} ${group.name} instances from workspace`
        : `Remove ${group.name} from workspace`;
      const removeButton = group.isWorkspaceAgent && !group.isUnassigned ? `
        <button type="button"
                class="workspace-detail-agent-remove-btn"
                title="${this.escapeHtml(removeLabel)}"
                aria-label="${this.escapeHtml(removeLabel)}"
                onclick="event.stopPropagation(); window.workspaceDetail?.removeAgentFromWorkspace('${encodedAgentName}')">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
          </svg>
        </button>
      ` : '';
      const instanceChip = group.instanceCount > 1
        ? `<span class="workspace-detail-agent-instance-tag">${group.instanceCount}x</span>`
        : '';
      const frontFlipButton = canFlip ? `
        <button type="button"
                class="workspace-detail-agent-flip-btn"
                title="Show ${this.escapeHtml(group.name)} info"
                aria-label="Show ${this.escapeHtml(group.name)} info"
                onclick="event.stopPropagation(); window.workspaceDetail?.toggleAgentCardFlip('${encodedAgentName}')">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
          </svg>
        </button>
      ` : '';
      const taskActionButton = group.isUnassigned ? '' : `
        <button type="button"
                class="workspace-detail-agent-section-btn"
                title="Add task for ${this.escapeHtml(group.name)}"
                aria-label="Add task for ${this.escapeHtml(group.name)}"
                onclick="event.stopPropagation(); window.workspaceDetail?.showAddTaskModalForAgent('${encodedAgentName}')">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
          </svg>
        </button>
      `;
      const backFace = canFlip
        ? this.renderAgentBackFace(group, cardMeta, encodedAgentName)
        : '';
      const flippedClass = isFlipped ? ' is-flipped' : '';

      return `
      <section class="workspace-detail-agent-card${flippedClass}" data-agent-name="${this.escapeHtml(group.name)}" data-agent-key="${this.escapeHtml(group.key)}">
        <div class="workspace-detail-agent-card-flipper">
          <div class="workspace-detail-agent-card-face workspace-detail-agent-card-face-front">
            <div class="workspace-detail-agent-card-header">
              <div class="workspace-detail-agent-card-title">
                ${group.isUnassigned ? `<span>${this.escapeHtml(group.name)}</span>` : this.renderAgentDetailLink(group.name, encodedAgentName)}
                ${roleBadge}
                ${instanceChip}
                ${capabilityBadges}
                ${modelLabel}
              </div>
              <div class="workspace-detail-agent-card-meta-wrap">
                <div class="workspace-detail-agent-card-meta">${cardMeta}</div>
                ${removeButton}
                ${frontFlipButton}
              </div>
            </div>
            <div class="workspace-detail-agent-sections">
              <div class="workspace-detail-agent-section">
                <div class="workspace-detail-agent-section-header">
                  <div class="workspace-detail-agent-section-title">Tasks</div>
                  ${taskActionButton}
                </div>
                <div class="workspace-detail-list workspace-detail-agent-list">
                  ${this.renderAgentTasksContent(group.tasks)}
                </div>
              </div>
            </div>
          </div>
          ${backFace}
        </div>
      </section>
      `;
    }).join('');
  }

  renderAgentBackFace(group, cardMeta, encodedAgentName) {
    const profile = this.getAgentProfile(group.name);
    const levelValue = Number(profile?.evolution?.level ?? profile?.level);
    const level = Number.isFinite(levelValue) ? Math.max(0, Math.floor(levelValue)) : 0;
    const stage = String(profile?.evolution?.stage || profile?.stage || '').trim();
    const levelLabel = stage ? `Lv ${level} · ${stage}` : `Lv ${level}`;
    const roleBadge = this.renderWorkspaceAgentRoleBadge(group.name);

    const skillsState = this.getAgentSkillsState(group.name);
    const skillsMarkup = this.renderAgentSkillsChips(skillsState, profile);

    const mcpServers = this.getEffectiveWorkspaceMCPServerNames(group.name);
    const mcpMarkup = mcpServers.length > 0
      ? mcpServers.map((server) => `<span class="workspace-detail-agent-info-chip mcp">${this.escapeHtml(server)}</span>`).join('')
      : '<span class="workspace-detail-agent-info-empty">No MCP servers attached.</span>';
    const removeLabel = group.instanceCount > 1
      ? `Remove all ${group.instanceCount} ${group.name} instances from workspace`
      : `Remove ${group.name} from workspace`;
    const removeButton = group.isWorkspaceAgent && !group.isUnassigned ? `
      <button type="button"
              class="workspace-detail-agent-remove-btn"
              title="${this.escapeHtml(removeLabel)}"
              aria-label="${this.escapeHtml(removeLabel)}"
              onclick="event.stopPropagation(); window.workspaceDetail?.removeAgentFromWorkspace('${encodedAgentName}')">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
        </svg>
      </button>
    ` : '';

    return `
      <div class="workspace-detail-agent-card-face workspace-detail-agent-card-face-back">
        <div class="workspace-detail-agent-card-header">
          <div class="workspace-detail-agent-card-title">
            ${this.renderAgentDetailLink(group.name, encodedAgentName)}
            ${roleBadge}
            <span class="workspace-detail-agent-info-tag">Agent Info</span>
          </div>
          <div class="workspace-detail-agent-card-meta-wrap">
            <div class="workspace-detail-agent-card-meta">${cardMeta}</div>
            ${removeButton}
            <button type="button"
                    class="workspace-detail-agent-flip-btn"
                    title="Back to tasks and sessions"
                    aria-label="Back to tasks and sessions"
                    onclick="event.stopPropagation(); window.workspaceDetail?.toggleAgentCardFlip('${encodedAgentName}')">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                <path d="M7.41,15.41L12,10.83L16.59,15.41L18,14L12,8L6,14L7.41,15.41Z"/>
              </svg>
            </button>
          </div>
        </div>
        <div class="workspace-detail-agent-info-grid">
          <div class="workspace-detail-agent-info-block">
            <div class="workspace-detail-agent-info-label">Agent Lvl</div>
            <div class="workspace-detail-agent-info-value">${this.escapeHtml(levelLabel)}</div>
          </div>
          <div class="workspace-detail-agent-info-block">
            <div class="workspace-detail-agent-info-label">Skills</div>
            <div class="workspace-detail-agent-chip-list workspace-detail-agent-skills-list" data-agent-skills-key="${this.escapeHtml(group.key)}">${skillsMarkup}</div>
          </div>
          <div class="workspace-detail-agent-info-block">
            <div class="workspace-detail-agent-info-label">MCP Attached</div>
            <div class="workspace-detail-agent-chip-list">${mcpMarkup}</div>
          </div>
        </div>
      </div>
    `;
  }

  getAgentSkillsState(agentName) {
    const key = this.normalizeAgentName(agentName);
    if (!key) return { status: 'idle', skills: [] };
    return this.agentSkillsCache.get(key) || { status: 'idle', skills: [] };
  }

  renderAgentSkillsChips(skillsState, profile = null) {
    const fallbackSkills = [];
    const fallbackSeen = new Set();
    const addFallbackSkill = (value) => {
      const name = String(value || '').trim();
      if (!name) return;
      const key = name.toLowerCase();
      if (fallbackSeen.has(key)) return;
      fallbackSeen.add(key);
      fallbackSkills.push(name);
    };

    if (Array.isArray(profile?.capabilities)) {
      profile.capabilities.forEach(addFallbackSkill);
    }
    if (Array.isArray(profile?.enabledPlugins)) {
      profile.enabledPlugins.forEach(addFallbackSkill);
    }

    if (!skillsState || skillsState.status === 'idle' || skillsState.status === 'loading') {
      if (fallbackSkills.length > 0) {
        return fallbackSkills
          .slice(0, 8)
          .map((skill) => `<span class="workspace-detail-agent-info-chip skill">${this.escapeHtml(skill)}</span>`)
          .join('');
      }
      return '<span class="workspace-detail-agent-info-empty">Loading skills...</span>';
    }

    if (skillsState.status === 'conflict') {
      return '<span class="workspace-detail-agent-info-empty">Skill conflicts detected.</span>';
    }

    if (skillsState.status === 'error') {
      if (fallbackSkills.length > 0) {
        return fallbackSkills
          .slice(0, 8)
          .map((skill) => `<span class="workspace-detail-agent-info-chip skill">${this.escapeHtml(skill)}</span>`)
          .join('');
      }
      return '<span class="workspace-detail-agent-info-empty">Failed to load skills.</span>';
    }

    const enabledSkills = Array.isArray(skillsState.skills)
      ? skillsState.skills.filter((skill) => skill.enabled !== false && skill.name)
      : [];

    if (enabledSkills.length === 0) {
      return '<span class="workspace-detail-agent-info-empty">No enabled skills.</span>';
    }

    const visibleSkills = enabledSkills.slice(0, 8);
    const overflowCount = enabledSkills.length - visibleSkills.length;
    const chips = visibleSkills
      .map((skill) => `<span class="workspace-detail-agent-info-chip skill">${this.escapeHtml(skill.name)}</span>`)
      .join('');

    return overflowCount > 0
      ? `${chips}<span class="workspace-detail-agent-info-chip count">+${overflowCount} more</span>`
      : chips;
  }

  getAgentCardElementByKey(agentKey) {
    if (!this.elements?.agentsList || !agentKey) return null;
    const cards = this.elements.agentsList.querySelectorAll('.workspace-detail-agent-card[data-agent-key]');
    for (const card of cards) {
      if (card.getAttribute('data-agent-key') === agentKey) {
        return card;
      }
    }
    return null;
  }

  refreshAgentCardSkills(agentName) {
    const key = this.normalizeAgentName(agentName);
    if (!key) return false;

    const card = this.getAgentCardElementByKey(key);
    if (!card) return false;

    const profile = this.getAgentProfile(agentName);
    const skillsState = this.getAgentSkillsState(agentName);
    const skillsMarkup = this.renderAgentSkillsChips(skillsState, profile);

    const skillContainers = card.querySelectorAll('.workspace-detail-agent-skills-list[data-agent-skills-key]');
    let updated = false;
    skillContainers.forEach((container) => {
      if (container.getAttribute('data-agent-skills-key') === key) {
        container.innerHTML = skillsMarkup;
        updated = true;
      }
    });

    return updated;
  }

  toggleAgentCardFlip(encodedAgentName = '') {
    let agentName = '';
    try {
      agentName = decodeURIComponent(String(encodedAgentName || ''));
    } catch (error) {
      agentName = String(encodedAgentName || '');
    }

    const key = this.normalizeAgentName(agentName);
    if (!key) return;
    const card = this.getAgentCardElementByKey(key);

    if (this.flippedAgentCards.has(key)) {
      this.flippedAgentCards.delete(key);
      if (card) {
        card.classList.remove('is-flipped');
      } else {
        this.renderAgentGroups();
      }
      return;
    }

    this.flippedAgentCards.add(key);
    if (card) {
      card.classList.add('is-flipped');
    } else {
      this.renderAgentGroups();
    }
    this.ensureAgentSkillsLoaded(agentName);
  }

  async ensureAgentSkillsLoaded(agentName) {
    await this.loadAgentSkills(agentName, { refreshUI: true });
  }

  async loadAgentSkills(agentName, options = {}) {
    const normalizedName = String(agentName || '').trim();
    const key = this.normalizeAgentName(normalizedName);
    if (!normalizedName || !key) return { status: 'idle', skills: [] };

    const existing = this.agentSkillsCache.get(key);
    if (existing?.status === 'loaded' || existing?.status === 'conflict' || existing?.status === 'error') {
      return existing;
    }
    if (this.agentSkillsPromises.has(key)) {
      return this.agentSkillsPromises.get(key);
    }

    this.agentSkillsCache.set(key, { status: 'loading', skills: [] });
    if (options.refreshUI !== false) {
      this.refreshAgentCardSkills(normalizedName);
    }

    const loadPromise = (async () => {
      try {
        const response = await fetch(`/api/skills?agent=${encodeURIComponent(normalizedName)}`);
        if (response.status === 409) {
          this.agentSkillsCache.set(key, { status: 'conflict', skills: [] });
          return this.agentSkillsCache.get(key);
        }
        if (!response.ok) {
          throw new Error(`Failed to load skills (${response.status})`);
        }

        const data = await response.json();
        const skills = Array.isArray(data?.skills)
          ? data.skills
            .map((skill) => ({
              name: String(skill?.name || '').trim(),
              description: String(skill?.description || '').trim(),
              enabled: skill?.enabled !== false,
              requiredMCPServers: Array.isArray(skill?.required_mcp_servers) ? skill.required_mcp_servers.map((value) => String(value || '').trim()).filter(Boolean) : [],
              allowedTools: Array.isArray(skill?.allowed_tools) ? skill.allowed_tools.map((value) => String(value || '').trim()).filter(Boolean) : []
            }))
            .filter((skill) => skill.name)
          : [];

        this.agentSkillsCache.set(key, { status: 'loaded', skills });
      } catch (error) {
        console.error(`Failed to load skills for ${normalizedName}:`, error);
        this.agentSkillsCache.set(key, { status: 'error', skills: [] });
      } finally {
        this.agentSkillsPromises.delete(key);
        if (options.refreshUI !== false) {
          if (!this.refreshAgentCardSkills(normalizedName)) {
            this.renderAgentGroups();
          }
        }
      }

      return this.agentSkillsCache.get(key) || { status: 'error', skills: [] };
    })();

    this.agentSkillsPromises.set(key, loadPromise);
    return loadPromise;
  }

  buildAgentGroups() {
    const groups = [];
    const groupByKey = new Map();

    const ensureGroup = (name, { isWorkspaceAgent = false, isUnassigned = false } = {}) => {
      const normalized = isUnassigned ? '__unassigned__' : this.normalizeAgentName(name);
      if (!normalized) return null;

      let group = groupByKey.get(normalized);
      if (!group) {
        group = {
          key: normalized,
          name: isUnassigned ? 'Unassigned' : String(name || '').trim(),
          isWorkspaceAgent,
          isUnassigned,
          instanceCount: 0,
          tasks: []
        };
        groupByKey.set(normalized, group);
        groups.push(group);
      } else if (isWorkspaceAgent) {
        group.isWorkspaceAgent = true;
      }

      return group;
    };

    if (Array.isArray(this.workspace?.agent_instances) && this.workspace.agent_instances.length > 0) {
      this.workspace.agent_instances.forEach((instance) => {
        const group = ensureGroup(instance?.name, { isWorkspaceAgent: true, isUnassigned: false });
        if (group) {
          group.instanceCount += 1;
        }
      });
    } else {
      this.getWorkspaceAgentNames().forEach((name) => {
        const group = ensureGroup(name, { isWorkspaceAgent: true, isUnassigned: false });
        if (group) {
          group.instanceCount = Math.max(1, group.instanceCount);
        }
      });
    }

    const topLevelTasks = Array.isArray(this.tasks)
      ? this.tasks.filter((task) => !task.parent_task_id)
      : [];

    topLevelTasks.forEach((task) => {
      const assigned = String(task?.to || '').trim();
      if (assigned && assigned !== 'unassigned') {
        ensureGroup(assigned)?.tasks.push(task);
      } else {
        ensureGroup('Unassigned', { isUnassigned: true })?.tasks.push(task);
      }
    });

    const workspaceGroups = [];
    const extraGroups = [];
    const unassignedGroups = [];

    groups.forEach((group) => {
      if (group.isUnassigned) {
        unassignedGroups.push(group);
      } else if (group.isWorkspaceAgent) {
        workspaceGroups.push(group);
      } else {
        extraGroups.push(group);
      }
    });

    extraGroups.sort((a, b) => a.name.localeCompare(b.name));
    return [...workspaceGroups, ...extraGroups, ...unassignedGroups];
  }

  renderAgentTasksContent(tasks) {
    if (this.tasksLoading) {
      return '<div class="workspace-detail-loading workspace-detail-loading-inline">Loading tasks...</div>';
    }
    if (this.tasksLoadFailed) {
      return '<div class="workspace-detail-empty workspace-detail-empty-inline">Failed to load tasks.</div>';
    }
    if (!Array.isArray(tasks) || tasks.length === 0) {
      return '<div class="workspace-detail-empty workspace-detail-empty-inline">No tasks assigned.</div>';
    }
    return tasks.map((task) => this.renderTaskItem(task)).join('');
  }

  renderAgentSessionsContent(sessions) {
    if (this.sessionsLoading) {
      return '<div class="workspace-detail-loading workspace-detail-loading-inline">Loading sessions...</div>';
    }
    if (this.sessionsLoadFailed) {
      return '<div class="workspace-detail-empty workspace-detail-empty-inline">Failed to load sessions.</div>';
    }
    if (!Array.isArray(sessions) || sessions.length === 0) {
      return '<div class="workspace-detail-empty workspace-detail-empty-inline">No sessions yet.</div>';
    }
    return sessions.map((session) => this.renderSessionItem(session)).join('');
  }

  getTaskScheduleIndicatorData(task) {
    if (!task || task.schedule == null) return null;

    const isActive = task.schedule_enabled !== false;
    const scheduleName = typeof task.schedule_name === 'string' ? task.schedule_name.trim() : '';
    const title = scheduleName
      ? `${isActive ? 'Scheduled task' : 'Scheduled task (paused)'}: ${scheduleName}`
      : (isActive ? 'Scheduled task' : 'Scheduled task (paused)');

    return { isActive, title };
  }

  renderTaskScheduleIndicator(task, variant = 'default') {
    const scheduleInfo = this.getTaskScheduleIndicatorData(task);
    if (!scheduleInfo) return '';

    const stateClass = scheduleInfo.isActive ? 'active' : 'paused';
    const variantClass = variant === 'board' ? 'workspace-detail-task-schedule-indicator-board' : '';
    const classes = ['workspace-detail-task-schedule-indicator', stateClass, variantClass]
      .filter(Boolean)
      .join(' ');

    return `
      <span class="${classes}"
            title="${this.escapeHtml(scheduleInfo.title)}"
            aria-label="${this.escapeHtml(scheduleInfo.title)}">
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M12,20A8,8 0 0,1 4,12A8,8 0 0,1 12,4A8,8 0 0,1 20,12A8,8 0 0,1 12,20M12.5,7H11V12.3L15.6,15L16.35,13.77L12.5,11.5V7Z"/>
        </svg>
      </span>
    `;
  }

  renderTaskItem(task) {
    const taskLabel = this.escapeHtml(task.description || task.name || 'Untitled Task');
    const assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
    const subtasks = this.tasks.filter((subtask) => subtask.parent_task_id === task.id);
    const isParent = subtasks.length > 0;
    const statusInfo = this.getTaskStatusPresentation(task);
    const awaitingNextStep = this.isTaskAwaitingNextStep(task);
    const hasUnassignedSubtasks = isParent && subtasks.some((subtask) => !subtask.to || subtask.to === 'unassigned');
    const hasRunningSubtasks = isParent && subtasks.some((subtask) => subtask.status === 'in_progress');
    const canExecute = isParent
      ? subtasks.length > 0 && !hasUnassignedSubtasks && !hasRunningSubtasks
      : task.status !== 'in_progress' || awaitingNextStep;
    const resultData = this.getDisplayResult(task, subtasks);
    const hasResultData = !!resultData;
    const hasAssistData = !!statusInfo.isBlocked;
    const executeTitle = isParent
      ? hasUnassignedSubtasks ? 'Assign agents to all subtasks before executing'
        : hasRunningSubtasks ? 'A subtask is already running'
          : 'Execute workflow now'
      : !assignedAgent ? 'Will auto-assign a workspace agent before execution'
        : awaitingNextStep ? 'Execute the next internal step'
          : task.status === 'in_progress' ? 'Task is already running'
          : 'Execute task now';
    const resultTitle = hasResultData
      ? `View ${resultData.label} from ${resultData.answeredBy || 'Unknown agent'}`
      : '';
    const assistTitle = statusInfo.reason || 'Agent needs your guidance before this task can continue.';
    const scheduleIndicator = this.renderTaskScheduleIndicator(task);
    const taskMetaParts = [];
    if (assignedAgent) {
      taskMetaParts.push(`<span class="workspace-detail-assigned-agent">Assigned to: ${this.escapeHtml(assignedAgent)}${this.renderAgentCapabilityBadges(assignedAgent)}</span>`);
    }
    if (scheduleIndicator) {
      taskMetaParts.push(scheduleIndicator);
    }
    taskMetaParts.push(formatDate(task.created_at));

    return `
      <div class="workspace-detail-item" data-task-id="${task.id}">
        ${hasAssistData ? `
        <button type="button"
                class="workspace-detail-item-result"
                onclick="event.stopPropagation(); window.workspaceDetail?.openTaskAssistModal('${task.id}')"
                title="${this.escapeHtml(assistTitle)}"
                aria-label="Help blocked task ${taskLabel}">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor">
            <path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
          </svg>
        </button>
        ` : hasResultData ? `
        <button type="button"
                class="workspace-detail-item-result"
                onclick="event.stopPropagation(); window.workspaceDetail?.showTaskResult('${task.id}')"
                title="${this.escapeHtml(resultTitle)}"
                aria-label="View result for task ${taskLabel}">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor">
            <path d="M14,17H7V15H14M17,13H7V11H17M17,9H7V7H17M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3Z"/>
          </svg>
        </button>
        ` : ''}
        <button type="button"
                class="workspace-detail-item-run"
                onclick="event.stopPropagation(); window.workspaceDetail?.executeTask('${task.id}')"
                title="${this.escapeHtml(executeTitle)}"
                aria-label="Execute task ${taskLabel}"
                ${canExecute ? '' : 'disabled'}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M8,5.14V19.14L19,12.14L8,5.14Z"/>
          </svg>
        </button>
        <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteTask('${task.id}')" title="Delete task" aria-label="Delete task ${this.escapeHtml(task.description || task.name || 'Untitled Task')}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <div class="workspace-detail-item-content"
             role="button"
             tabindex="0"
             aria-label="Open task ${this.escapeHtml(task.description || task.name || 'Untitled Task')}"
             onclick="window.workspaceDetail?.openTask('${task.id}')"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.openTask('${task.id}'); }">
          <div class="d-flex justify-content-between align-items-start">
            <div class="workspace-detail-item-title">${taskLabel}</div>
            <span class="workspace-detail-task-status ${statusInfo.className}">${statusInfo.label}</span>
          </div>
          <div class="workspace-detail-item-meta">
            ${taskMetaParts.join(' · ')}
          </div>
        </div>
      </div>
    `;
  }

  renderSessionItem(session) {
    return `
      <div class="workspace-detail-item" data-session-id="${session.id}">
        <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteSession('${session.id}')" title="Delete session" aria-label="Delete session ${this.escapeHtml(session.title || session.name || 'Untitled Session')}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <div class="workspace-detail-item-content"
             role="button"
             tabindex="0"
             aria-label="Open session ${this.escapeHtml(session.title || session.name || 'Untitled Session')}"
             onclick="window.workspaceDetail?.openSession('${session.id}')"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.openSession('${session.id}'); }">
          <div class="workspace-detail-item-title">${this.escapeHtml(session.title || session.name || 'Untitled Session')}</div>
          <div class="workspace-detail-item-meta">
            <span class="workspace-detail-session-id" title="${this.escapeHtml(session.id)}">${this.escapeHtml(session.id)}</span>
            ${session.agent_name ? ` · ${this.escapeHtml(session.agent_name)}` : ''}
             · ${formatDate(session.updated_at || session.created_at)}
          </div>
        </div>
      </div>
    `;
  }

  /**
   * Check for ?addAgent=1 query param and open the Create Agent modal
   * pre-filled with workspace manager defaults so the user picks model/provider.
   */
  checkAutoOpenCreateAgent() {
    const params = new URLSearchParams(window.location.search);
    if (params.get('addAgent') !== '1') return;

    // Clean the URL so refresh won't re-trigger
    window.history.replaceState({}, '', window.location.pathname);

    const workspaceName = String(this.workspace?.name || '').trim();
    const agentName = workspaceName
      ? (workspaceName.toLowerCase().endsWith(' manager') ? workspaceName : workspaceName + ' Manager')
      : 'Workspace Manager';
    const systemPrompt = `You are the workspace manager for "${workspaceName || 'this workspace'}". `
      + 'Act as the default front door for the workspace: clarify user intent, answer directly when '
      + 'the request only needs shared context, and break work into tasks for specialists when needed.';

    setTimeout(() => {
      if (typeof window.showAddAgentModal === 'function') {
        window.showAddAgentModal({
          workspaceId: this.workspaceId,
          seedName: agentName,
          seedType: 'workspace-manager',
          seedSystemPrompt: systemPrompt
        });
      }
    }, 300);
  }

  async openAddAgentModal() {
    await this.loadAgentCatalog(true);
    await this.populateAddAgentOptions();

    if (this.elements.addAgentModal && window.bootstrap) {
      const modal = typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.addAgentModal)
        : (bootstrap.Modal.getInstance(this.elements.addAgentModal) || new bootstrap.Modal(this.elements.addAgentModal));
      modal.show();
    }
  }

  isMissingWorkspaceAgentError(message) {
    const normalized = String(message || '').trim().toLowerCase();
    if (!normalized) return false;

    return normalized.includes('no agent is available in this workspace')
      || normalized.includes('no agents available in this workspace')
      || normalized.includes('no agent assigned')
      || normalized.includes('no agent assigned to step')
      || normalized.includes('parent task has no agent');
  }

  async promptAddAgentForExecution(message = 'No agent is available in this workspace. Add one to continue.') {
    if (window.Toast) {
      window.Toast.warning(message);
    }

    await this.openAddAgentModal();

    window.setTimeout(() => {
      if (this.elements.addAgentSelect && !this.elements.addAgentSelect.disabled) {
        this.elements.addAgentSelect.focus();
        return;
      }

      this.elements.createAgentBtn?.focus();
    }, 150);
  }

  getTaskDisplayLabel(task) {
    return String(task?.description || task?.name || task?.id || 'Task').trim() || 'Task';
  }

  resetTaskConfirmDialog() {
    this.pendingTaskConfirmSelection = { stepThrough: false };
    if (this.elements.taskConfirmEyebrow) {
      this.elements.taskConfirmEyebrow.textContent = 'Execution Check';
    }
    if (this.elements.taskConfirmTitle) {
      this.elements.taskConfirmTitle.textContent = 'Confirm this action';
    }
    if (this.elements.taskConfirmMessage) {
      this.elements.taskConfirmMessage.textContent = '';
    }
    if (this.elements.taskConfirmCancelBtn) {
      this.elements.taskConfirmCancelBtn.textContent = 'Cancel';
    }
    if (this.elements.taskConfirmConfirmBtn) {
      this.elements.taskConfirmConfirmBtn.textContent = 'Continue';
    }
    if (this.elements.taskConfirmMeta) {
      this.elements.taskConfirmMeta.innerHTML = '';
    }
    if (this.elements.taskConfirmDetails) {
      this.elements.taskConfirmDetails.innerHTML = '';
      this.elements.taskConfirmDetails.classList.add('d-none');
    }
    if (this.elements.taskConfirmSequence) {
      this.elements.taskConfirmSequence.innerHTML = '';
      this.elements.taskConfirmSequence.classList.add('d-none');
    }
    if (this.elements.taskConfirmStepMode && this.elements.taskConfirmStepModeInput) {
      this.elements.taskConfirmStepMode.classList.add('d-none');
      this.elements.taskConfirmStepModeInput.checked = false;
    }
  }

  renderTaskConfirmMeta(items = []) {
    if (!this.elements.taskConfirmMeta) return;
    this.elements.taskConfirmMeta.innerHTML = '';

    items
      .map((item) => String(item || '').trim())
      .filter(Boolean)
      .forEach((item) => {
        const chip = document.createElement('span');
        chip.className = 'workspace-detail-task-confirm-chip';
        chip.textContent = item;
        this.elements.taskConfirmMeta.appendChild(chip);
      });
  }

  renderTaskConfirmDetails(items = []) {
    if (!this.elements.taskConfirmDetails) return;

    const normalizedItems = items
      .map((item) => String(item || '').trim())
      .filter(Boolean);

    this.elements.taskConfirmDetails.innerHTML = '';
    if (normalizedItems.length === 0) {
      this.elements.taskConfirmDetails.classList.add('d-none');
      return;
    }

    normalizedItems.forEach((item, index) => {
      const row = document.createElement('div');
      row.className = 'workspace-detail-task-confirm-detail';

      const indexBadge = document.createElement('span');
      indexBadge.className = 'workspace-detail-task-confirm-detail-index';
      indexBadge.textContent = String(index + 1);

      const text = document.createElement('div');
      text.className = 'workspace-detail-task-confirm-detail-text';
      text.textContent = item;

      row.appendChild(indexBadge);
      row.appendChild(text);
      this.elements.taskConfirmDetails.appendChild(row);
    });

    this.elements.taskConfirmDetails.classList.remove('d-none');
  }

  normalizeTaskConfirmSequenceItems(items = []) {
    if (!Array.isArray(items)) return [];

    return items
      .map((item) => {
        if (!item || typeof item !== 'object') return null;
        const title = String(item.title || '').trim();
        if (!title) return null;
        return {
          title,
          detail: String(item.detail || '').trim(),
          tag: String(item.tag || '').trim()
        };
      })
      .filter(Boolean)
      .slice(0, 8);
  }

  renderTaskConfirmSequence(items = []) {
    if (!this.elements.taskConfirmSequence) return;

    const normalizedItems = this.normalizeTaskConfirmSequenceItems(items);
    this.elements.taskConfirmSequence.innerHTML = '';
    if (normalizedItems.length === 0) {
      this.elements.taskConfirmSequence.classList.add('d-none');
      return;
    }

    const list = normalizedItems.map((item, index) => {
      const title = this.escapeHtml(item.title);
      const detail = item.detail ? `<div class="workspace-detail-task-confirm-sequence-step-detail">${this.escapeHtml(item.detail)}</div>` : '';
      const tag = item.tag ? `<span class="workspace-detail-task-confirm-sequence-tag">${this.escapeHtml(item.tag)}</span>` : '';
      return `
        <div class="workspace-detail-task-confirm-sequence-step">
          <span class="workspace-detail-task-confirm-sequence-index">${index + 1}</span>
          <div class="workspace-detail-task-confirm-sequence-copy">
            <div class="workspace-detail-task-confirm-sequence-step-title">
              <span>${title}</span>
              ${tag}
            </div>
            ${detail}
          </div>
        </div>
      `;
    }).join('');

    this.elements.taskConfirmSequence.innerHTML = `
      <div class="workspace-detail-task-confirm-sequence-title">Predicted Sequence</div>
      <div class="workspace-detail-task-confirm-sequence-list">${list}</div>
    `;
    this.elements.taskConfirmSequence.classList.remove('d-none');
  }

  getTaskConfirmModalInstance() {
    if (!this.elements.taskConfirmModal || !window.bootstrap) return null;
    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.elements.taskConfirmModal)
      : (bootstrap.Modal.getInstance(this.elements.taskConfirmModal) || new bootstrap.Modal(this.elements.taskConfirmModal));
  }

  renderTaskConfirmStepMode(options = {}) {
    if (!this.elements.taskConfirmStepMode || !this.elements.taskConfirmStepModeInput) return;

    const enabled = options?.allowStepThrough === true;
    if (!enabled) {
      this.elements.taskConfirmStepMode.classList.add('d-none');
      this.elements.taskConfirmStepModeInput.checked = false;
      return;
    }

    this.elements.taskConfirmStepModeInput.checked = options?.defaultStepThrough === true;
    this.elements.taskConfirmStepMode.classList.remove('d-none');
  }

  consumePendingTaskConfirmStepThroughSelection() {
    const selected = this.pendingTaskConfirmSelection?.stepThrough === true;
    this.pendingTaskConfirmSelection = { stepThrough: false };
    return selected;
  }

  handleTaskConfirmChoice(confirmed) {
    this.pendingTaskConfirmSelection = {
      stepThrough: Boolean(this.elements.taskConfirmStepModeInput?.checked)
    };
    if (this.pendingTaskConfirm && !this.pendingTaskConfirm.resolved) {
      this.pendingTaskConfirm.resolved = true;
      this.pendingTaskConfirm.resolve(Boolean(confirmed));
    }

    const modal = this.getTaskConfirmModalInstance();
    modal?.hide();
  }

  handleTaskConfirmHidden() {
    if (this.pendingTaskConfirm && !this.pendingTaskConfirm.resolved) {
      this.pendingTaskConfirm.resolved = true;
      this.pendingTaskConfirm.resolve(false);
    }
    this.pendingTaskConfirm = null;
    this.resetTaskConfirmDialog();
  }

  async showTaskConfirmDialog(options = {}) {
    const title = String(options?.title || 'Confirm this action').trim();
    const message = String(options?.message || '').trim();
    const eyebrow = String(options?.eyebrow || 'Execution Check').trim();
    const confirmLabel = String(options?.confirmLabel || 'Continue').trim();
    const cancelLabel = String(options?.cancelLabel || 'Cancel').trim();
    const metaItems = Array.isArray(options?.metaItems) ? options.metaItems : [];
    const details = Array.isArray(options?.details) ? options.details : [];
    const sequenceItems = this.normalizeTaskConfirmSequenceItems(options?.sequenceItems);
    const allowStepThrough = options?.allowStepThrough === true;
    const defaultStepThrough = options?.defaultStepThrough === true;

    if (!this.elements.taskConfirmModal || !window.bootstrap) {
      const fallbackSequence = sequenceItems.length > 0
        ? `Predicted sequence:\n${sequenceItems.map((item, index) => `${index + 1}. ${item.title}${item.detail ? ` - ${item.detail}` : ''}`).join('\n')}`
        : '';
      const fallbackMode = allowStepThrough ? `Execution mode: ${defaultStepThrough ? 'step through' : 'automatic'}` : '';
      const fallbackText = [message, ...details, fallbackSequence, fallbackMode].filter(Boolean).join('\n\n');
      return confirm([title, fallbackText].filter(Boolean).join('\n\n'));
    }

    if (this.pendingTaskConfirm && !this.pendingTaskConfirm.resolved) {
      this.pendingTaskConfirm.resolved = true;
      this.pendingTaskConfirm.resolve(false);
    }

    this.resetTaskConfirmDialog();

    if (this.elements.taskConfirmEyebrow) {
      this.elements.taskConfirmEyebrow.textContent = eyebrow || 'Execution Check';
    }
    if (this.elements.taskConfirmTitle) {
      this.elements.taskConfirmTitle.textContent = title;
    }
    if (this.elements.taskConfirmMessage) {
      this.elements.taskConfirmMessage.textContent = message;
    }
    if (this.elements.taskConfirmCancelBtn) {
      this.elements.taskConfirmCancelBtn.textContent = cancelLabel || 'Cancel';
    }
    if (this.elements.taskConfirmConfirmBtn) {
      this.elements.taskConfirmConfirmBtn.textContent = confirmLabel || 'Continue';
    }

    this.renderTaskConfirmMeta(metaItems);
    this.renderTaskConfirmDetails(details);
    this.renderTaskConfirmSequence(sequenceItems);
    this.renderTaskConfirmStepMode({ allowStepThrough, defaultStepThrough });

    return new Promise((resolve) => {
      this.pendingTaskConfirm = { resolve, resolved: false };
      const modal = this.getTaskConfirmModalInstance();
      modal?.show();
      window.setTimeout(() => {
        this.elements.taskConfirmConfirmBtn?.focus();
      }, 120);
    });
  }

  isSystemAssistantAgentName(name) {
    const normalized = this.normalizeAgentName(name);
    return normalized === 'ori' || normalized === 'system assistant';
  }

  getTaskAgentSuggestionPrompt(task) {
    if (!task || typeof task !== 'object') return '';

    const summary = String(task.description || task.name || '').trim();
    const details = String(task.details || '').trim();
    return [summary, details].filter(Boolean).join('\n\n');
  }

  async fetchTaskAgentSuggestion(task) {
    const prompt = this.getTaskAgentSuggestionPrompt(task);
    if (!prompt) return null;

    try {
      const response = await fetch('/api/home-assistant/route', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          prompt,
          context: {
            surface: 'workspace_detail',
            page_path: window.location.pathname || `/workspaces/${this.workspaceId}`,
            workspace_id: this.workspaceId,
            origin: 'task_execution'
          }
        })
      });
      if (!response.ok) {
        return null;
      }

      const data = await response.json().catch(() => null);
      return data && typeof data === 'object' ? data : null;
    } catch (error) {
      console.error('Failed to fetch task agent suggestion:', error);
      return null;
    }
  }

  getTaskAgentSuggestionReasonText(routeData) {
    const reasons = Array.isArray(routeData?.reasons)
      ? routeData.reasons.map((reason) => String(reason || '').trim()).filter(Boolean).slice(0, 3)
      : [];
    if (reasons.length === 0) return '';
    return `\n\nWhy this suggestion:\n- ${reasons.join('\n- ')}`;
  }

  isLikelyFilesystemIntent(description) {
    const lower = String(description || '').trim().toLowerCase();
    if (!lower) return false;

    if (this.isReadOnlyFilesystemListingIntent(description)) {
      return false;
    }

    const directPhrases = [
      'move files',
      'copy files',
      'rename files',
      'organize files',
      'organise files',
      'gather files',
      'collect files',
      'sort files',
      'clean up files',
      'into folder',
      'into directory',
      'filesystem',
      'file management'
    ];
    if (directPhrases.some((phrase) => lower.includes(phrase))) {
      return true;
    }

    const actionSignals = ['move', 'copy', 'rename', 'organize', 'organise', 'gather', 'collect', 'sort', 'group', 'archive'];
    const nounSignals = ['file', 'files', 'folder', 'folders', 'directory', 'directories', 'filesystem', 'path', 'paths'];
    const actionCount = actionSignals.filter((signal) => lower.includes(signal)).length;
    const nounCount = nounSignals.filter((signal) => lower.includes(signal)).length;

    return (actionCount > 0 && nounCount > 0) || nounCount > 1;
  }

  isReadOnlyFilesystemListingIntent(description) {
    const lower = String(description || '').trim().toLowerCase();
    if (!lower) return false;

    const mutationPhrases = [
      'move files',
      'copy files',
      'rename files',
      'organize files',
      'organise files',
      'gather files',
      'collect files',
      'sort files',
      'clean up files',
      'into folder',
      'into directory',
      'filesystem',
      'file management'
    ];
    if (mutationPhrases.some((phrase) => lower.includes(phrase))) {
      return false;
    }

    const mutationSignals = ['move', 'copy', 'rename', 'organize', 'organise', 'gather', 'collect', 'sort', 'group', 'archive'];
    const mutationNouns = ['file', 'files', 'folder', 'folders', 'directory', 'directories', 'filesystem', 'path', 'paths'];
    const actionCount = mutationSignals.filter((signal) => lower.includes(signal)).length;
    const nounCount = mutationNouns.filter((signal) => lower.includes(signal)).length;
    if (actionCount > 0 && nounCount > 0) {
      return false;
    }

    const directPhrases = [
      'list files',
      'list the files',
      'list of files',
      'show files',
      'show me the files',
      'file list',
      'what files',
      'which files',
      'folder contents',
      'directory contents',
      'contents of',
      'contents in',
      'what is in',
      "what's in"
    ];
    if (directPhrases.some((phrase) => lower.includes(phrase))) {
      return true;
    }

    const listingSignals = ['list', 'show', 'display'];
    const listingNouns = ['file', 'files', 'folder', 'folders', 'directory', 'directories', 'contents'];
    const listingVerbCount = listingSignals.filter((signal) => lower.includes(signal)).length;
    const listingNounCount = listingNouns.filter((signal) => lower.includes(signal)).length;
    return listingVerbCount > 0 && listingNounCount > 0;
  }

  inferTaskExecutionRequirements(description) {
    const requirements = [];
    if (this.isLikelyBrowserAutomationIntent(description)) {
      requirements.push({
        key: TASK_REQUIREMENT_KEYS.BROWSER,
        label: 'Browser access',
        reason: 'This task appears to require visiting or interacting with webpages.'
      });
    }
    if (this.isLikelyFilesystemIntent(description)) {
      requirements.push({
        key: TASK_REQUIREMENT_KEYS.FILESYSTEM,
        label: 'File management',
        reason: 'This task appears to require gathering, moving, renaming, or organizing files and folders.'
      });
    }
    return requirements;
  }

  getTaskRequirementSignals(requirementKey) {
    switch (String(requirementKey || '').trim().toLowerCase()) {
      case TASK_REQUIREMENT_KEYS.BROWSER:
        return {
          capabilities: ['browser', 'browser_automation', 'web_search', 'web_fetch'],
          plugins: ['browser', 'playwright', 'browserbase', 'puppeteer', 'navigate', 'open_url', 'web_fetch', 'web_search'],
          mcp: ['playwright', 'browserbase', 'puppeteer', 'browser'],
          skills: ['browser', 'web', 'playwright', 'navigate', 'website', 'scrape']
        };
      case TASK_REQUIREMENT_KEYS.FILESYSTEM:
        return {
          capabilities: ['file_operations', 'filesystem', 'storage'],
          plugins: ['filesystem', 'file', 'files', 'folder', 'directory', 'finder', 'shell', 'command', 'os-shell'],
          mcp: ['filesystem', 'files', 'file', 'finder', 'directory'],
          skills: ['file', 'files', 'folder', 'folders', 'directory', 'filesystem', 'finder', 'organize', 'organiser', 'move', 'rename', 'copy']
        };
      default:
        return {
          capabilities: [],
          plugins: [],
          mcp: [],
          skills: []
        };
    }
  }

  getTaskRequirementSeedDefaults(requirements = []) {
    const keys = new Set((Array.isArray(requirements) ? requirements : []).map((requirement) => String(requirement?.key || '').trim().toLowerCase()));
    if (keys.has(TASK_REQUIREMENT_KEYS.FILESYSTEM)) {
      return { name: 'Folder Organizer', type: 'tool-calling' };
    }
    if (keys.has(TASK_REQUIREMENT_KEYS.BROWSER)) {
      return { name: 'Browser Assistant', type: 'tool-calling' };
    }
    return { name: 'Task Assistant', type: 'tool-calling' };
  }

  getTaskRequirementMatches(values, signals) {
    const normalizedValues = Array.isArray(values)
      ? values.map((value) => String(value || '').trim()).filter(Boolean)
      : [];
    const normalizedSignals = Array.isArray(signals)
      ? signals.map((value) => String(value || '').trim().toLowerCase()).filter(Boolean)
      : [];

    const matches = [];
    const seen = new Set();
    normalizedValues.forEach((value) => {
      const lower = value.toLowerCase();
      if (!lower) return;
      if (!normalizedSignals.some((signal) => lower.includes(signal))) return;
      if (seen.has(lower)) return;
      seen.add(lower);
      matches.push(value);
    });

    return matches;
  }

  getTaskRequirementSkillText(skill) {
    if (!skill || typeof skill !== 'object') return '';

    const parts = [
      skill.name,
      skill.description,
      ...(Array.isArray(skill.requiredMCPServers) ? skill.requiredMCPServers : []),
      ...(Array.isArray(skill.allowedTools) ? skill.allowedTools : [])
    ];

    return parts
      .map((value) => String(value || '').trim().toLowerCase())
      .filter(Boolean)
      .join(' ');
  }

  async getEnabledSkillsForAgent(agentName) {
    const state = await this.loadAgentSkills(agentName, { refreshUI: false });
    if (!state || !Array.isArray(state.skills)) return [];
    return state.skills.filter((skill) => skill?.enabled !== false && skill?.name);
  }

  skillSupportsTaskRequirement(skill, requirementKey) {
    if (!skill || skill.enabled === false) return false;

    const signals = this.getTaskRequirementSignals(requirementKey);
    const skillText = this.getTaskRequirementSkillText(skill);
    const mcpMatches = this.getTaskRequirementMatches(skill.requiredMCPServers, signals.mcp);
    if (mcpMatches.length > 0) {
      return true;
    }

    return signals.skills.some((signal) => skillText.includes(signal));
  }

  getAgentSupportForTaskRequirement(profile, skills, requirement) {
    if (!profile || !requirement) {
      return { supported: false, score: 0, reasons: [] };
    }

    const signals = this.getTaskRequirementSignals(requirement.key);
    const reasons = [];
    let score = 0;

    if (requirement.key === TASK_REQUIREMENT_KEYS.BROWSER && this.agentSupportsBrowserAutomation(profile)) {
      score += 3;
      reasons.push('has browser automation support');
    }

    const capabilityMatches = this.getTaskRequirementMatches(profile.capabilities, signals.capabilities);
    if (capabilityMatches.length > 0) {
      score += 1;
      reasons.push(`has capability "${capabilityMatches[0]}"`);
    }

    const pluginMatches = this.getTaskRequirementMatches(profile.enabledPlugins, signals.plugins);
    if (pluginMatches.length > 0) {
      score += 2;
      reasons.push(`has tool support via "${pluginMatches[0]}"`);
    }

    const effectiveMCPServers = this.getEffectiveWorkspaceMCPServerNames(profile.name);
    const mcpMatches = this.getTaskRequirementMatches(effectiveMCPServers, signals.mcp);
    if (mcpMatches.length > 0) {
      score += 3;
      reasons.push(`has MCP support via "${mcpMatches[0]}"`);
    }

    const skillMatches = (Array.isArray(skills) ? skills : [])
      .filter((skill) => this.skillSupportsTaskRequirement(skill, requirement.key))
      .slice(0, 2);
    if (skillMatches.length > 0) {
      score += 4;
      reasons.push(`has skill support via "${skillMatches[0].name}"`);
    }

    const dedupedReasons = [];
    const seenReasons = new Set();
    reasons.forEach((reason) => {
      const normalized = String(reason || '').trim().toLowerCase();
      if (!normalized || seenReasons.has(normalized)) return;
      seenReasons.add(normalized);
      dedupedReasons.push(reason);
    });

    return {
      supported: score > 0,
      score,
      reasons: dedupedReasons.slice(0, 3)
    };
  }

  async evaluateAgentForTaskRequirements(agentName, requirements = []) {
    const profile = this.getAgentProfile(agentName);
    const normalizedRequirements = Array.isArray(requirements) ? requirements.filter(Boolean) : [];
    if (!profile || normalizedRequirements.length === 0) return null;

    const skills = await this.getEnabledSkillsForAgent(agentName);
    const requirementSupport = [];
    let totalScore = 0;
    const reasons = [];
    const seenReasons = new Set();

    for (const requirement of normalizedRequirements) {
      const support = this.getAgentSupportForTaskRequirement(profile, skills, requirement);
      if (!support.supported) {
        return null;
      }
      totalScore += support.score;
      requirementSupport.push({ requirement, support });
      support.reasons.forEach((reason) => {
        const key = String(reason || '').trim().toLowerCase();
        if (!key || seenReasons.has(key)) return;
        seenReasons.add(key);
        reasons.push(reason);
      });
    }

    const inWorkspace = this.getWorkspaceAgentNames()
      .some((name) => this.normalizeAgentName(name) === this.normalizeAgentName(agentName));
    if (inWorkspace) {
      totalScore += 2;
    }
    if (profile.status === 'active') {
      totalScore += 1;
    }

    return {
      agentName: profile.name || agentName,
      inWorkspace,
      score: totalScore,
      reasons: reasons.slice(0, 4),
      requirementSupport
    };
  }

  async findBestAgentForTaskRequirements(requirements = [], options = {}) {
    await this.loadAgentCatalog();

    const exclude = this.normalizeAgentName(options?.excludeAgent || '');
    const workspaceOnly = options?.workspaceOnly === true;
    const candidateNames = workspaceOnly
      ? this.getWorkspaceAgentNames()
      : Array.from(new Set((this.agentCatalog || []).map((profile) => String(profile?.name || '').trim()).filter(Boolean)));

    const evaluations = await Promise.all(candidateNames.map(async (name) => {
      if (this.normalizeAgentName(name) === exclude) return null;
      return this.evaluateAgentForTaskRequirements(name, requirements);
    }));

    const ranked = evaluations
      .filter(Boolean)
      .sort((left, right) => {
        if (right.score !== left.score) {
          return right.score - left.score;
        }
        if (left.inWorkspace !== right.inWorkspace) {
          return left.inWorkspace ? -1 : 1;
        }
        return left.agentName.localeCompare(right.agentName, undefined, { sensitivity: 'base' });
      });

    return ranked[0] || null;
  }

  async loadCapabilitySuggestionCatalog(force = false) {
    if (!force && this.capabilitySuggestionCatalog) {
      return this.capabilitySuggestionCatalog;
    }
    if (!force && this.capabilitySuggestionCatalogPromise) {
      return this.capabilitySuggestionCatalogPromise;
    }

    this.capabilitySuggestionCatalogPromise = (async () => {
      const catalog = {
        mcpServers: [],
        skills: []
      };

      try {
        const [mcpResult, skillsResult] = await Promise.allSettled([
          fetch('/api/mcp/servers'),
          fetch(`/api/skills?agent=${encodeURIComponent(AGENT_CREATION_SKILL_CATALOG_AGENT)}`)
        ]);

        if (mcpResult.status === 'fulfilled' && mcpResult.value.ok) {
          const data = await mcpResult.value.json().catch(() => ({}));
          const servers = Array.isArray(data?.servers) ? data.servers : [];
          catalog.mcpServers = servers
            .map((server) => ({
              name: String(server?.name || '').trim()
            }))
            .filter((server) => server.name);
        }

        if (skillsResult.status === 'fulfilled' && skillsResult.value.ok) {
          const data = await skillsResult.value.json().catch(() => ({}));
          const skills = Array.isArray(data?.skills) ? data.skills : [];
          catalog.skills = skills
            .map((skill) => ({
              name: String(skill?.name || '').trim(),
              description: String(skill?.description || '').trim(),
              requiredMCPServers: Array.isArray(skill?.required_mcp_servers) ? skill.required_mcp_servers.map((value) => String(value || '').trim()).filter(Boolean) : []
            }))
            .filter((skill) => skill.name);
        }
      } catch (error) {
        console.error('Failed to load capability suggestion catalog:', error);
      }

      this.capabilitySuggestionCatalog = catalog;
      this.capabilitySuggestionCatalogPromise = null;
      return catalog;
    })();

    return this.capabilitySuggestionCatalogPromise;
  }

  getCapabilitySuggestionsForRequirements(requirements = [], catalog = null) {
    const sourceCatalog = catalog && typeof catalog === 'object'
      ? catalog
      : { mcpServers: [], skills: [] };
    const requirementKeys = Array.isArray(requirements)
      ? requirements.map((requirement) => String(requirement?.key || '').trim().toLowerCase()).filter(Boolean)
      : [];

    const matchedMCPServers = [];
    const matchedSkills = [];
    const seenMCPServers = new Set();
    const seenSkills = new Set();

    requirementKeys.forEach((requirementKey) => {
      const signals = this.getTaskRequirementSignals(requirementKey);

      (sourceCatalog.mcpServers || []).forEach((server) => {
        const name = String(server?.name || '').trim();
        const normalized = name.toLowerCase();
        if (!name || seenMCPServers.has(normalized)) return;
        if (!signals.mcp.some((signal) => normalized.includes(signal))) return;
        seenMCPServers.add(normalized);
        matchedMCPServers.push(name);
      });

      (sourceCatalog.skills || []).forEach((skill) => {
        const name = String(skill?.name || '').trim();
        const normalized = name.toLowerCase();
        if (!name || seenSkills.has(normalized)) return;
        const skillText = this.getTaskRequirementSkillText(skill);
        const requiredMCPServers = Array.isArray(skill?.requiredMCPServers) ? skill.requiredMCPServers : [];
        const hasRequirementMatch = signals.skills.some((signal) => skillText.includes(signal)) ||
          this.getTaskRequirementMatches(requiredMCPServers, signals.mcp).length > 0;
        if (!hasRequirementMatch) return;
        seenSkills.add(normalized);
        matchedSkills.push(name);
      });
    });

    return {
      mcpServers: matchedMCPServers.slice(0, 3),
      skills: matchedSkills.slice(0, 4)
    };
  }

  buildCapabilityAwareAgentDescription(task, requirements = [], suggestions = {}) {
    const taskLabel = this.getTaskDisplayLabel(task);
    const details = [
      `Create an agent that can complete the task "${taskLabel}".`
    ];

    const requirementKeys = new Set((Array.isArray(requirements) ? requirements : []).map((requirement) => String(requirement?.key || '').trim().toLowerCase()));
    if (requirementKeys.has(TASK_REQUIREMENT_KEYS.FILESYSTEM)) {
      details.push('It should be able to inspect folders, gather related files, and move or organize them safely.');
    }
    if (requirementKeys.has(TASK_REQUIREMENT_KEYS.BROWSER)) {
      details.push('It should be able to open websites and interact with browser pages when needed.');
    }
    if (Array.isArray(suggestions?.mcpServers) && suggestions.mcpServers.length > 0) {
      details.push(`Enable MCP servers such as ${suggestions.mcpServers.join(', ')}.`);
    }
    if (Array.isArray(suggestions?.skills) && suggestions.skills.length > 0) {
      details.push(`Enable relevant skills such as ${suggestions.skills.join(', ')}.`);
    }

    return details.join(' ');
  }

  async suggestCapabilityAwareAgentSetup(task, requirements = []) {
    const normalizedRequirements = Array.isArray(requirements) ? requirements.filter(Boolean) : [];
    if (!task || normalizedRequirements.length === 0) {
      return { handled: false, agentName: '' };
    }

    const taskLabel = this.getTaskDisplayLabel(task);
    const recommendedAgent = await this.findBestAgentForTaskRequirements(normalizedRequirements);
    if (recommendedAgent) {
      const alreadyInWorkspace = recommendedAgent.inWorkspace === true;
      const previewSequence = this.getPredictedTaskExecutionSequence(task, [], {
        assignedAgent: recommendedAgent.agentName
      });
      const confirmed = await this.showTaskConfirmDialog({
        eyebrow: 'Capability Match',
        title: alreadyInWorkspace
          ? `Assign "${recommendedAgent.agentName}" to this task?`
          : `Add "${recommendedAgent.agentName}" and assign it?`,
        message: alreadyInWorkspace
          ? `This task needs ${normalizedRequirements.map((requirement) => requirement.label.toLowerCase()).join(' and ')}. "${recommendedAgent.agentName}" is the best current match.`
          : `This task needs ${normalizedRequirements.map((requirement) => requirement.label.toLowerCase()).join(' and ')}. "${recommendedAgent.agentName}" is the best current match and can be added to this workspace.`,
        confirmLabel: alreadyInWorkspace ? 'Assign Agent' : 'Add and Assign',
        metaItems: [taskLabel, recommendedAgent.agentName, alreadyInWorkspace ? 'Capability match' : 'Add to workspace'],
        details: [
          ...normalizedRequirements.map((requirement) => requirement.reason),
          ...recommendedAgent.reasons
        ].slice(0, 5),
        sequenceItems: previewSequence
      });

      if (!confirmed) {
        return { handled: true, agentName: '' };
      }

      try {
        if (!alreadyInWorkspace) {
          await this.addAgentToWorkspace(recommendedAgent.agentName, { toast: false });
        }
        await this.assignTaskToAgent(task.id, recommendedAgent.agentName);
        task.to = recommendedAgent.agentName;
        this.renderTasks();
        if (window.Toast) {
          window.Toast.success(alreadyInWorkspace
            ? `Assigned "${recommendedAgent.agentName}" to this task.`
            : `Added "${recommendedAgent.agentName}" and assigned it to this task.`);
        }
        return { handled: true, agentName: recommendedAgent.agentName };
      } catch (error) {
        console.error('Failed to apply capability-aware agent setup:', error);
        if (window.Toast) {
          window.Toast.error(error.message || 'Failed to apply suggested agent');
        }
        return { handled: true, agentName: '' };
      }
    }

    const catalog = await this.loadCapabilitySuggestionCatalog();
    const suggestions = this.getCapabilitySuggestionsForRequirements(normalizedRequirements, catalog);
    const defaults = this.getTaskRequirementSeedDefaults(normalizedRequirements);
    const previewSequence = this.getPredictedTaskExecutionSequence(task);
    const createAgent = await this.showTaskConfirmDialog({
      eyebrow: 'Capability Required',
      title: 'Create a capable agent for this task?',
      message: `No assigned agent advertises ${normalizedRequirements.map((requirement) => requirement.label.toLowerCase()).join(' and ')} for "${taskLabel}".`,
      confirmLabel: 'Create Agent',
      cancelLabel: 'Cancel',
      metaItems: [taskLabel, defaults.name, 'Needs MCP or skills'],
      details: [
        ...normalizedRequirements.map((requirement) => requirement.reason),
        suggestions.mcpServers.length > 0 ? `Suggested MCP servers: ${suggestions.mcpServers.join(', ')}` : 'No matching MCP server is configured yet.',
        suggestions.skills.length > 0 ? `Suggested skills: ${suggestions.skills.join(', ')}` : 'No matching reusable skill is available yet.'
      ],
      sequenceItems: previewSequence
    });
    if (!createAgent) {
      return { handled: true, agentName: '' };
    }

    this.openCreateAgentFlow({
      seedName: defaults.name,
      seedType: defaults.type,
      autoDescription: this.buildCapabilityAwareAgentDescription(task, normalizedRequirements, suggestions),
      preferAutoConfig: true,
      workspaceId: this.workspaceId,
      taskId: task.id,
      assignTask: true,
      suggestedMCPServers: suggestions.mcpServers,
      suggestedSkills: suggestions.skills
    });

    return { handled: true, agentName: '' };
  }

  async populateAddAgentOptions() {
    if (!this.elements.addAgentSelect || !this.elements.addAgentSubmitBtn || !this.elements.addAgentEmpty) return;

    if (!Array.isArray(this.agentCatalog) || this.agentCatalog.length === 0) {
      try {
        const response = await fetch('/api/agents');
        if (response.ok) {
          const data = await response.json();
          const baseAgents = Array.isArray(data?.agents) ? data.agents : [];
          this.agentCatalog = baseAgents
            .map((agent) => {
              const name = typeof agent === 'string' ? agent : agent?.name;
              return name ? { name: String(name).trim() } : null;
            })
            .filter((agent) => agent && agent.name);
          this.agentIndex = new Map(this.agentCatalog.map((agent) => [this.normalizeAgentName(agent.name), agent]));
        }
      } catch (error) {
        console.error('Failed to load fallback agent list:', error);
      }
    }

    const workspaceAgents = new Set(this.getWorkspaceAgentNames().map((name) => this.normalizeAgentName(name)));
    const allAgents = Array.isArray(this.agentCatalog) ? this.agentCatalog : [];
    const candidates = allAgents
      .map((agent) => String(agent?.name || '').trim())
      .filter(Boolean)
      .filter((name) => !workspaceAgents.has(this.normalizeAgentName(name)));

    if (candidates.length === 0) {
      const hasCatalogAgents = allAgents.length > 0;
      this.elements.addAgentSelect.innerHTML = hasCatalogAgents
        ? '<option value="">All agents already added</option>'
        : '<option value="">No agents available</option>';
      this.elements.addAgentSelect.disabled = true;
      this.elements.addAgentSubmitBtn.disabled = true;
      this.elements.addAgentEmpty.textContent = hasCatalogAgents
        ? 'All available agents are already in this workspace. Create a new agent to add another profile.'
        : 'No unassigned agents are available. Create a new agent first.';
      this.elements.addAgentEmpty.classList.remove('d-none');
      return;
    }

    this.elements.addAgentSelect.innerHTML = candidates
      .map((name) => `<option value="${this.escapeHtml(name)}">${this.escapeHtml(name)}</option>`)
      .join('');
    this.elements.addAgentSelect.disabled = false;
    this.elements.addAgentSubmitBtn.disabled = false;
    this.elements.addAgentEmpty.classList.add('d-none');
  }

  openCreateAgentFlow(options = {}) {
    if (this.elements.addAgentModal && window.bootstrap) {
      const modal = bootstrap.Modal.getInstance(this.elements.addAgentModal);
      modal?.hide();
    }

    if (typeof window.showAddAgentModal === 'function') {
      window.showAddAgentModal(options);
      return;
    }

    window.location.href = '/agents';
  }

  async addAgentToWorkspace(agentName, options = {}) {
    const normalizedAgentName = String(agentName || '').trim();
    if (!normalizedAgentName) {
      throw new Error('Select an agent first.');
    }

    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/agents`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent_name: normalizedAgentName })
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to add agent to workspace');
    }

    if (options.toast !== false && window.Toast) {
      window.Toast.success(`Added "${normalizedAgentName}" to workspace`);
    }

    if (options.closeModal !== false && this.elements.addAgentModal && window.bootstrap) {
      const modal = bootstrap.Modal.getInstance(this.elements.addAgentModal);
      modal?.hide();
    }

    this.agentOptions = null;
    await Promise.all([
      this.loadWorkspace(),
      this.loadAgentCatalog(true)
    ]);
    this.renderAgentGroups();

    return normalizedAgentName;
  }

  async addSelectedAgentToWorkspace() {
    const agentName = String(this.elements.addAgentSelect?.value || '').trim();
    if (!agentName) {
      if (window.Toast) window.Toast.warning('Select an agent first.');
      return;
    }

    const submitButton = this.elements.addAgentSubmitBtn;
    const originalLabel = submitButton ? submitButton.innerHTML : '';
    if (submitButton) {
      submitButton.disabled = true;
      submitButton.innerHTML = '<span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Adding...';
    }

    try {
      await this.addAgentToWorkspace(agentName);
    } catch (error) {
      console.error('Failed to add agent to workspace:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to add agent');
    } finally {
      if (submitButton) {
        submitButton.disabled = false;
        submitButton.innerHTML = originalLabel;
      }
    }
  }

  getWorkspaceAgentInstances(agentName) {
    const normalized = this.normalizeAgentName(agentName);
    if (!normalized || !this.workspace) return [];

    const instances = Array.isArray(this.workspace.agent_instances)
      ? this.workspace.agent_instances.filter((instance) => this.normalizeAgentName(instance?.name) === normalized)
      : [];
    if (instances.length > 0) {
      return instances;
    }

    const names = Array.isArray(this.workspace.agents) ? this.workspace.agents : [];
    if (names.some((name) => this.normalizeAgentName(name) === normalized)) {
      return [{ id: '', name: String(agentName || '').trim(), instance_number: 1, node_id: '' }];
    }

    return [];
  }

  async removeAgentFromWorkspace(encodedAgentName = '') {
    let agentName = '';
    try {
      agentName = decodeURIComponent(String(encodedAgentName || ''));
    } catch (error) {
      agentName = String(encodedAgentName || '');
    }

    const normalizedAgentName = String(agentName || '').trim();
    const normalizedKey = this.normalizeAgentName(normalizedAgentName);
    if (!normalizedAgentName || !normalizedKey) return;

    const instances = this.getWorkspaceAgentInstances(normalizedAgentName);
    const instanceCount = instances.length > 0 ? instances.length : 1;
    const taskCount = Array.isArray(this.tasks)
      ? this.tasks.filter((task) => this.normalizeAgentName(task?.to) === normalizedKey).length
      : 0;
    const sessionCount = Array.isArray(this.sessions)
      ? this.sessions.filter((session) => this.normalizeAgentName(session?.agent_name) === normalizedKey).length
      : 0;

    const confirmationLines = [`Remove "${normalizedAgentName}" from this workspace?`];
    if (instanceCount > 1) {
      confirmationLines.push(`This will remove all ${instanceCount} instances of this agent from the workspace.`);
    }
    if (taskCount > 0) {
      confirmationLines.push(`${taskCount} assigned task${taskCount === 1 ? '' : 's'} will be moved to Unassigned.`);
    }
    if (sessionCount > 0) {
      confirmationLines.push(`${sessionCount} session${sessionCount === 1 ? '' : 's'} will remain in workspace history.`);
    }

    if (!window.confirm(confirmationLines.join('\n\n'))) {
      return;
    }

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/agents/${encodeURIComponent(normalizedAgentName)}`, {
        method: 'DELETE'
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to remove agent from workspace');
      }

      this.flippedAgentCards.delete(normalizedKey);
      this.agentSkillsCache.delete(normalizedKey);
      this.agentSkillsPromises.delete(normalizedKey);

      await Promise.all([
        this.loadWorkspace(),
        this.loadTasks(),
        this.loadSessions()
      ]);

      if (window.Toast) {
        window.Toast.success(
          instanceCount > 1
            ? `Removed ${instanceCount} "${normalizedAgentName}" instances from workspace`
            : `Removed "${normalizedAgentName}" from workspace`
        );
      }
    } catch (error) {
      console.error('Failed to remove agent from workspace:', error);
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to remove agent from workspace');
      }
    }
  }

  async suggestAgentSetupForTask(task) {
    if (!task || typeof task !== 'object') return '';

    const taskLabel = String(task.description || task.name || task.id || 'this task').trim() || 'this task';
    const prompt = this.getTaskAgentSuggestionPrompt(task);
    const specialistAction = this.maybeBuildTravelAssistSpecialistAction({
      task,
      currentAgent: String(task.to || '').trim()
    });
    if (specialistAction) {
      const specialistSuggestion = await this.suggestTravelSpecialistSetupForTask(task, specialistAction);
      if (specialistSuggestion.handled) {
        return specialistSuggestion.agentName || '';
      }
    }

    const requirements = this.inferTaskExecutionRequirements(prompt);
    if (requirements.length > 0) {
      const capabilitySuggestion = await this.suggestCapabilityAwareAgentSetup(task, requirements);
      if (capabilitySuggestion.handled) {
        return capabilitySuggestion.agentName || '';
      }
    }

    const routeData = await this.fetchTaskAgentSuggestion(task);
    const suggestedAgent = String(routeData?.matched_agent || '').trim();
    const alreadyInWorkspace = suggestedAgent
      ? this.getWorkspaceAgentNames().some((name) => this.normalizeAgentName(name) === this.normalizeAgentName(suggestedAgent))
      : false;

    if (suggestedAgent && !this.isSystemAssistantAgentName(suggestedAgent) && routeData?.requires_creation !== true) {
      const reasonItems = Array.isArray(routeData?.reasons)
        ? routeData.reasons.map((reason) => String(reason || '').trim()).filter(Boolean).slice(0, 3)
        : [];
      const previewSequence = this.getPredictedTaskExecutionSequence(task, [], {
        assignedAgent: suggestedAgent
      });
      const confirmed = await this.showTaskConfirmDialog({
        eyebrow: 'Ori Recommendation',
        title: alreadyInWorkspace ? `Assign "${suggestedAgent}" to this task?` : `Add "${suggestedAgent}" and assign it?`,
        message: alreadyInWorkspace
          ? `Ori matched ${taskLabel} to "${suggestedAgent}" and can assign it in one step.`
          : `Ori matched ${taskLabel} to "${suggestedAgent}" and can add it to this workspace before execution.`,
        confirmLabel: alreadyInWorkspace ? 'Assign Agent' : 'Add and Assign',
        metaItems: [taskLabel, suggestedAgent, alreadyInWorkspace ? 'Ready to assign' : 'Needs workspace add'],
        details: reasonItems,
        sequenceItems: previewSequence
      });

      if (!confirmed) {
        return '';
      }

      try {
        if (!alreadyInWorkspace) {
          await this.addAgentToWorkspace(suggestedAgent, { toast: false });
        }
        await this.assignTaskToAgent(task.id, suggestedAgent);
        task.to = suggestedAgent;
        this.renderTasks();
        if (window.Toast) {
          window.Toast.success(alreadyInWorkspace
            ? `Assigned "${suggestedAgent}" to this task.`
            : `Added "${suggestedAgent}" and assigned it to this task.`);
        }
        return suggestedAgent;
      } catch (error) {
        console.error('Failed to apply suggested agent setup:', error);
        if (window.Toast) {
          window.Toast.error(error.message || 'Failed to apply suggested agent');
        }
        return '';
      }
    }

    const prefersBrowserAgent = this.isLikelyBrowserAutomationIntent(prompt);
    const suggestedName = String(routeData?.suggested_agent_name || '').trim() || (prefersBrowserAgent ? 'Browser Assistant' : 'Task Assistant');
    const suggestedType = String(routeData?.suggested_agent_type || '').trim() || 'tool-calling';
    const reasonText = this.getTaskAgentSuggestionReasonText(routeData);
    if (window.Toast) {
      const message = routeData?.requires_creation === true
        ? `Ori suggests creating "${suggestedName}" for this task.`
        : `No workspace agent matches this task yet. Ori prepared "${suggestedName}" for you.`;
      window.Toast.info(reasonText ? `${message} ${reasonText.replace(/\n+/g, ' ')}` : message);
    }

    this.openCreateAgentFlow({
      seedName: suggestedName,
      seedType: suggestedType,
      autoDescription: prompt || taskLabel,
      preferAutoConfig: true,
      workspaceId: this.workspaceId,
      taskId: task.id,
      assignTask: true
    });
    return '';
  }

  getTaskHumanLoop(task) {
    if (!task || typeof task !== 'object' || !task.context || typeof task.context !== 'object') {
      return null;
    }
    const humanLoop = task.context.human_loop;
    return humanLoop && typeof humanLoop === 'object' ? humanLoop : null;
  }

  getTaskExecutionMode(task) {
    const mode = String(task?.execution_mode || '').trim().toLowerCase();
    return mode === 'step_through' ? 'step_through' : 'auto';
  }

  isTaskAwaitingNextStep(task) {
    return this.getTaskExecutionMode(task) === 'step_through'
      && task?.status === 'in_progress'
      && task?.context?.execution_step_waiting === true;
  }

  getTaskStatusPresentation(task) {
    if (this.isTaskAwaitingNextStep(task)) {
      return {
        className: 'assigned',
        label: 'Next Step Ready',
        isBlocked: false,
        reason: 'This task is paused between internal execution steps.'
      };
    }

    const humanLoop = this.getTaskHumanLoop(task);
    if (humanLoop && String(humanLoop.state || '').toLowerCase() === 'blocked') {
      const reason = String(humanLoop.reason || '').trim();
      return {
        className: 'blocked',
        label: 'Needs Input',
        isBlocked: true,
        reason
      };
    }

    return {
      className: getStatusClass(task?.status),
      label: getDisplayStatus(task?.status),
      isBlocked: false,
      reason: ''
    };
  }

  getDisplayResult(task, subtasks = []) {
    if (!task) return null;
    if (task.error) {
      return {
        label: 'Error',
        text: this.normalizeResultText(task.error),
        sourceTask: task,
        answeredBy: this.getAnsweringAgentLabel(task)
      };
    }
    if (task.result) {
      return {
        label: 'Result',
        text: this.normalizeResultText(task.result),
        sourceTask: task,
        answeredBy: this.getAnsweringAgentLabel(task)
      };
    }

    if (Array.isArray(subtasks) && subtasks.length > 0) {
      const orderedSubtasks = [...subtasks].sort((a, b) => {
        const aIndex = Number.isFinite(a?.subtask_index) && a.subtask_index > 0 ? a.subtask_index : Number.MAX_SAFE_INTEGER;
        const bIndex = Number.isFinite(b?.subtask_index) && b.subtask_index > 0 ? b.subtask_index : Number.MAX_SAFE_INTEGER;
        if (aIndex !== bIndex) return aIndex - bIndex;
        const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
        const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
        return aTime - bTime;
      });

      const lastSubtask = orderedSubtasks[orderedSubtasks.length - 1];
      if (lastSubtask?.error) {
        return {
          label: 'Error (last step)',
          text: this.normalizeResultText(lastSubtask.error),
          sourceTask: lastSubtask,
          answeredBy: this.getAnsweringAgentLabel(lastSubtask)
        };
      }
      if (lastSubtask?.result) {
        return {
          label: 'Result (last step)',
          text: this.normalizeResultText(lastSubtask.result),
          sourceTask: lastSubtask,
          answeredBy: this.getAnsweringAgentLabel(lastSubtask)
        };
      }
    }

    return null;
  }

  getAnsweringAgentLabel(task) {
    if (!task || typeof task !== 'object') return 'Unknown agent';

    const to = String(task.to || '').trim();
    const assignedNodeId = String(task.assigned_node_id || '').trim();
    const from = String(task.from || '').trim();

    if (assignedNodeId) {
      const nodeMatch = assignedNodeId.match(/^(.+)-node-(\d+)$/);
      if (nodeMatch) {
        const nodeAgentName = nodeMatch[1];
        const nodeNumber = nodeMatch[2];
        if (to && to !== 'unassigned' && to !== nodeAgentName) {
          return `${to} (${assignedNodeId})`;
        }
        return `${nodeAgentName} (node ${nodeNumber})`;
      }

      if (to && to !== 'unassigned') {
        return `${to} (${assignedNodeId})`;
      }
      return assignedNodeId;
    }

    if (to && to !== 'unassigned') return to;
    if (from && from !== 'system') return from;
    return 'Unknown agent';
  }

  setTaskModalHeaderId(element, taskId) {
    if (!element) return;
    const value = String(taskId || '').trim();
    element.textContent = value || '--';
    element.title = value || 'Task ID unavailable';
  }

  showTaskResult(taskId, options = {}) {
    const task = this.tasks.find((item) => item.id === taskId);
    if (!task) return;
    const closeExecutionModal = Boolean(options?.closeExecutionModal);

    const subtasks = this.getSubtasksForParent(taskId);
    const isParent = subtasks.length > 0;
    const resultData = this.getDisplayResult(task, subtasks);
    if (!resultData || !resultData.text) {
      if (window.Toast) window.Toast.info('No result captured for this task yet.');
      return;
    }

    const taskName = task.description || task.name || task.id || 'Task Result';
    const statusText = getDisplayStatus(task.status);
    const timestamp = formatDate(task.completed_at || task.updated_at || task.created_at);
    const answeredBy = resultData.answeredBy || 'Unknown agent';
    const metaParts = [`Answered by ${answeredBy}`, `${statusText}`, timestamp];
    if (isParent) {
      metaParts.unshift(`${subtasks.length} step${subtasks.length === 1 ? '' : 's'}`);
    }

    if (this.elements.taskResultTitle) {
      this.elements.taskResultTitle.textContent = taskName;
    }
    this.setTaskModalHeaderId(this.elements.taskResultId, task.id);
    if (this.elements.taskResultMeta) {
      this.elements.taskResultMeta.textContent = `${resultData.label} • ${metaParts.join(' • ')}`;
    }
    this.renderTaskResultBreakdown(task, subtasks);
    if (this.elements.taskResultBody) {
      this.elements.taskResultBody.textContent = String(resultData.text || '');
    }
    this.currentTaskResultText = String(resultData.text || '');
    this.currentTaskResultTaskId = String(task.id || '').trim();
    this.currentTaskResultSourceTaskId = String(resultData.sourceTask?.id || task.id || '').trim();
    this.currentTaskResultFollowUpPending = false;
    this.renderTaskResultNextSteps(task, resultData);

    const openResultModal = () => {
      if (!this.elements.taskResultModal || !window.bootstrap) return;
      const modal = typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.taskResultModal)
        : (bootstrap.Modal.getInstance(this.elements.taskResultModal) || new bootstrap.Modal(this.elements.taskResultModal));
      modal.show();
    };

    if (closeExecutionModal && this.elements.taskExecutionModal && window.bootstrap) {
      const isExecutionModalOpen = this.elements.taskExecutionModal.classList.contains('show');
      if (isExecutionModalOpen) {
        const executionModal = typeof bootstrap.Modal.getOrCreateInstance === 'function'
          ? bootstrap.Modal.getOrCreateInstance(this.elements.taskExecutionModal)
          : (bootstrap.Modal.getInstance(this.elements.taskExecutionModal) || new bootstrap.Modal(this.elements.taskExecutionModal));
        this.elements.taskExecutionModal.addEventListener('hidden.bs.modal', () => {
          openResultModal();
        }, { once: true });
        executionModal.hide();
        return;
      }
    }

    openResultModal();
  }

  renderTaskResultNextSteps(task, resultData) {
    const container = this.elements.taskResultNextSteps;
    const copyEl = this.elements.taskResultNextStepsCopy;
    const actionsEl = this.elements.taskResultNextStepsActions;
    if (!container || !copyEl || !actionsEl) return;

    const text = String(resultData?.text || '').trim();
    const sourceTask = resultData?.sourceTask && typeof resultData.sourceTask === 'object'
      ? resultData.sourceTask
      : task;
    const shouldShow = String(task?.status || '').trim().toLowerCase() === 'completed' && text;
    if (!shouldShow) {
      this.currentTaskResultNextSteps = [];
      container.classList.add('d-none');
      copyEl.textContent = '';
      actionsEl.innerHTML = '';
      return;
    }

    const nextSteps = this.extractTaskResultNextSteps(text);
    this.currentTaskResultNextSteps = nextSteps;

    if (!nextSteps.length) {
      container.classList.add('d-none');
      copyEl.textContent = '';
      actionsEl.innerHTML = '';
      return;
    }

    copyEl.textContent = 'Choose the next step to create and run a follow-up task linked to this result.';
    actionsEl.innerHTML = nextSteps.map((step) => {
      const buttonLabel = this.currentTaskResultFollowUpPending
        ? 'Creating follow-up task...'
        : 'Create follow-up task';
      return `
        <button type="button"
                class="workspace-detail-task-result-next-step-btn"
                data-next-step-id="${this.escapeHtml(step.id)}"
                ${this.currentTaskResultFollowUpPending ? 'disabled' : ''}>
          <span class="workspace-detail-task-result-next-step-index">${this.escapeHtml(step.number || '•')}</span>
          <span class="workspace-detail-task-result-next-step-copy">
            <span class="workspace-detail-task-result-next-step-title">${this.escapeHtml(step.label)}</span>
            <span class="workspace-detail-task-result-next-step-meta">${this.escapeHtml(buttonLabel)}</span>
          </span>
        </button>
      `;
    }).join('');

    actionsEl.querySelectorAll('[data-next-step-id]').forEach((button) => {
      button.addEventListener('click', () => {
        const nextStepId = String(button.getAttribute('data-next-step-id') || '').trim();
        if (!nextStepId) return;
        void this.continueTaskFromResult(task.id, sourceTask.id, nextStepId);
      });
    });

    container.classList.remove('d-none');
  }

  cleanTaskResultNextStepText(value) {
    return String(value || '')
      .replace(/\[(.*?)\]\((.*?)\)/g, '$1')
      .replace(/[*_`#>]+/g, '')
      .replace(/\s+/g, ' ')
      .trim();
  }

  normalizeTaskResultNextStepToken(value) {
    return this.cleanTaskResultNextStepText(value)
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, ' ')
      .trim();
  }

  buildTaskResultNextStepId(number, label) {
    const base = this.normalizeTaskResultNextStepToken(label)
      .replace(/\s+/g, '-')
      .slice(0, 48) || 'next-step';
    return `result-step-${String(number || '').trim() || 'x'}-${base}`;
  }

  extractTaskResultNextSteps(text) {
    const lines = String(text || '').split(/\r?\n/);
    const cues = ['next steps', 'next step', 'would you like me to', 'let me know'];
    let cueIndex = -1;

    for (let i = 0; i < lines.length; i += 1) {
      const normalized = this.normalizeTaskResultNextStepToken(lines[i]);
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
        const label = this.cleanTaskResultNextStepText(match[2]);
        if (!label) continue;
        choices.push({
          id: this.buildTaskResultNextStepId(number, label),
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

  buildTaskResultFollowUpTitle(task, step) {
    const baseTitle = String(task?.description || task?.name || 'Follow-up task').trim() || 'Follow-up task';
    const stepLabel = this.cleanTaskResultNextStepText(step?.label || '');
    if (!stepLabel) return baseTitle;
    const combined = `${baseTitle} - ${stepLabel}`;
    return combined.length > 160 ? `${combined.slice(0, 157).trim()}...` : combined;
  }

  buildTaskResultFollowUpDetails(task, sourceTask, step) {
    const parts = [];
    const baseTitle = String(task?.description || task?.name || task?.id || 'Completed task').trim();
    const sourceTitle = String(sourceTask?.description || sourceTask?.name || sourceTask?.id || '').trim();
    const stepNumber = String(step?.number || '').trim();
    const stepLabel = this.cleanTaskResultNextStepText(step?.label || '');

    parts.push(`Follow-up created from completed task: ${baseTitle}`);
    if (sourceTitle && sourceTitle !== baseTitle) {
      parts.push(`Source result: ${sourceTitle}`);
    }
    if (stepLabel) {
      parts.push(`Selected next step: ${stepNumber ? `${stepNumber}. ` : ''}${stepLabel}`);
    }
    parts.push('Use the linked input task result as the starting context and continue from there.');

    return parts.join('\n');
  }

  async continueTaskFromResult(taskId, sourceTaskId, nextStepId) {
    if (this.currentTaskResultFollowUpPending) return;

    const task = this.tasks.find((item) => item.id === taskId);
    const sourceTask = this.tasks.find((item) => item.id === sourceTaskId) || task;
    const nextStep = this.currentTaskResultNextSteps.find((item) => item.id === nextStepId);
    if (!task || !sourceTask || !nextStep) return;

    this.currentTaskResultFollowUpPending = true;
    this.renderTaskResultNextSteps(task, { text: this.currentTaskResultText, sourceTask });

    try {
      const createdTask = await this.createTask(
        this.buildTaskResultFollowUpTitle(task, nextStep),
        this.buildTaskResultFollowUpDetails(task, sourceTask, nextStep),
        '',
        {
          assignee: String(sourceTask.to || task.to || '').trim(),
          assignedNodeId: String(sourceTask.assigned_node_id || task.assigned_node_id || '').trim(),
          inputTaskIDs: [sourceTask.id],
          successToast: false
        }
      );

      if (!createdTask?.id) {
        throw new Error('Failed to create follow-up task');
      }

      if (window.Toast) {
        window.Toast.success('Follow-up task created');
      }

      if (this.elements.taskResultModal && window.bootstrap) {
        const modal = bootstrap.Modal.getInstance(this.elements.taskResultModal);
        modal?.hide();
      }

      await this.executeTask(createdTask.id, { skipConfirm: true });
    } catch (error) {
      console.error('Failed to continue task from result:', error);
      if (window.Toast) {
        window.Toast.error(error?.message || 'Failed to continue task');
      }
    } finally {
      this.currentTaskResultFollowUpPending = false;
      this.renderTaskResultNextSteps(task, { text: this.currentTaskResultText, sourceTask });
    }
  }

  renderTaskResultBreakdown(task, subtasks = []) {
    this.renderTaskBreakdown(this.elements.taskResultBreakdown, task, subtasks);
  }

  renderTaskExecutionBreakdown(task, subtasks = []) {
    this.renderTaskBreakdown(this.elements.taskExecutionBreakdown, task, subtasks);
  }

  renderTaskBreakdown(container, task, subtasks = []) {
    if (!container) return;
    if (!task) {
      container.innerHTML = '';
      return;
    }

    const steps = this.getTaskResultBreakdownSteps(task, subtasks);
    if (!steps.length) {
      container.innerHTML = '';
      return;
    }

    const stepsHtml = steps.map((step, index) => {
      const statusKey = String(step.status || task.status || 'pending').trim().toLowerCase();
      const statusClass = getStatusClass(statusKey);
      const statusLabel = getDisplayStatus(statusKey);
      const detail = String(step.detail || '').trim();
      const defaultOpen = index < 2 ? ' open' : '';
      const safeTitle = this.escapeHtml(step.title || `Step ${index + 1}`);
      const safeDetail = detail ? this.escapeHtml(detail) : 'No additional detail.';

      return `
        <details class="workspace-detail-task-breakdown-step"${defaultOpen}>
          <summary>
            <span class="workspace-detail-task-breakdown-step-title">
              <span class="workspace-detail-task-breakdown-step-index">${index + 1}</span>
              <span>${safeTitle}</span>
            </span>
            <span class="workspace-detail-task-status ${statusClass}">${statusLabel}</span>
          </summary>
          <div class="workspace-detail-task-breakdown-step-body">${safeDetail}</div>
        </details>
      `;
    }).join('');

    container.innerHTML = `
      <div class="workspace-detail-task-breakdown-title">Execution Breakdown</div>
      ${stepsHtml}
    `;
  }

  normalizeBreakdownField(value) {
    if (value === undefined || value === null) return '';
    if (typeof value === 'string') return value.trim();
    if (typeof value === 'number' || typeof value === 'boolean') return String(value);
    try {
      return JSON.stringify(value, null, 2).trim();
    } catch (_error) {
      return String(value).trim();
    }
  }

  truncateBreakdownText(text, maxLength = 220) {
    const normalized = this.normalizeBreakdownField(text);
    if (!normalized) return '';
    if (normalized.length <= maxLength) return normalized;
    return `${normalized.slice(0, maxLength).trim()}...`;
  }

  buildTaskExecutionDetail(task, fallbackDetail = '') {
    const parts = [];
    const initialDetail = this.normalizeBreakdownField(fallbackDetail);
    if (initialDetail) parts.push(initialDetail);

    const currentStep = this.normalizeBreakdownField(task?.progress?.current_step);
    if (currentStep) {
      parts.push(`Execution: ${currentStep}`);
    }

    const attemptsUsed = Number(task?.context?.execution_retry?.attempts_used || 0);
    const maxAttempts = Number(task?.context?.execution_retry?.max_attempts || task?.context?.execution_max_attempts || 0);
    if (attemptsUsed > 0 && maxAttempts > 0) {
      parts.push(`Attempts: ${attemptsUsed}/${maxAttempts}`);
    }

    const retryFinalOutcome = this.normalizeBreakdownField(task?.context?.execution_retry?.final_outcome);
    if (retryFinalOutcome) {
      parts.push(`Final outcome: ${retryFinalOutcome}`);
    }

    const blockedReason = this.normalizeBreakdownField(task?.context?.human_loop?.reason);
    const blockedQuestion = this.normalizeBreakdownField(task?.context?.human_loop?.question);
    if (blockedReason) {
      parts.push(`Blocked reason: ${this.truncateBreakdownText(blockedReason, 260)}`);
    }
    if (blockedQuestion) {
      parts.push(`Needs input: ${this.truncateBreakdownText(blockedQuestion, 260)}`);
    }

    const errorText = this.normalizeBreakdownField(task?.error);
    if (errorText) {
      parts.push(`Error: ${this.truncateBreakdownText(errorText, 360)}`);
    }

    const resultText = this.normalizeBreakdownField(task?.result);
    if (resultText) {
      parts.push(`Result: ${this.truncateBreakdownText(resultText, 360)}`);
    }

    return parts.join('\n');
  }

  getExecutionStepBreakdownSteps(task) {
    const steps = Array.isArray(task?.execution_steps) ? task.execution_steps : [];
    if (!steps.length) return [];

    return steps.map((step, index) => {
      const detailParts = [];
      const detail = this.normalizeBreakdownField(step?.detail);
      const result = this.normalizeBreakdownField(step?.result);
      const errorText = this.normalizeBreakdownField(step?.error);
      const startedAt = this.normalizeBreakdownField(step?.started_at);
      const completedAt = this.normalizeBreakdownField(step?.completed_at);
      const tag = this.normalizeBreakdownField(step?.tag);

      if (detail) detailParts.push(detail);
      if (tag) detailParts.push(`Type: ${tag}`);
      if (startedAt) detailParts.push(`Started: ${formatDate(startedAt)}`);
      if (completedAt) detailParts.push(`Completed: ${formatDate(completedAt)}`);
      if (result) detailParts.push(`Result: ${this.truncateBreakdownText(result, 360)}`);
      if (errorText) detailParts.push(`Error: ${this.truncateBreakdownText(errorText, 360)}`);

      return {
        title: String(step?.title || `Step ${index + 1}`).trim() || `Step ${index + 1}`,
        status: String(step?.status || 'pending').trim().toLowerCase() || 'pending',
        detail: detailParts.join('\n')
      };
    });
  }

  getRetryHistoryBreakdownSteps(task) {
    const history = Array.isArray(task?.context?.execution_retry?.history)
      ? task.context.execution_retry.history
      : [];
    if (!history.length) return [];

    const outcomeToStatus = (outcome) => {
      const normalized = String(outcome || '').trim().toLowerCase();
      if (normalized === 'success') return 'completed';
      if (normalized === 'error') return 'failed';
      if (normalized === 'needs_input' || normalized === 'blocked') return 'blocked';
      return 'pending';
    };

    return history.map((item, index) => {
      const attemptNumber = Number.isFinite(Number(item?.attempt)) ? Number(item.attempt) : (index + 1);
      const outcome = String(item?.outcome || '').trim().toLowerCase();
      const summary = this.normalizeBreakdownField(item?.summary);
      const createdAt = this.normalizeBreakdownField(item?.created_at);
      const detailParts = [];
      if (summary) detailParts.push(summary);
      if (createdAt) detailParts.push(`Recorded at: ${formatDate(createdAt)}`);
      if (outcome) detailParts.push(`Outcome: ${outcome.replace(/_/g, ' ')}`);

      return {
        title: `Attempt ${attemptNumber}`,
        status: outcomeToStatus(outcome),
        detail: detailParts.join('\n')
      };
    });
  }

  getExecutionHistoryBreakdownSteps(task, options = {}) {
    const history = Array.isArray(task?.execution_history) ? task.execution_history : [];
    if (!history.length) return [];

    const includeLatest = options.includeLatest !== false;
    const relevantHistory = includeLatest ? history : history.slice(0, -1);
    if (!relevantHistory.length) return [];

    const startIndex = Number.isFinite(Number(options.startIndex)) && Number(options.startIndex) > 0
      ? Number(options.startIndex)
      : 1;

    const mapRecordedStatus = (status) => {
      const normalized = String(status || '').trim().toLowerCase();
      if (normalized === 'success') return 'completed';
      if (normalized === 'failed') return 'failed';
      if (normalized === 'blocked') return 'blocked';
      return normalized || 'pending';
    };

    return relevantHistory.map((item, index) => {
      const recordedAt = this.normalizeBreakdownField(item?.executed_at);
      const rawStatus = String(item?.status || '').trim().toLowerCase();
      const summary = this.normalizeBreakdownField(item?.summary);
      const errorText = this.normalizeBreakdownField(item?.error);
      const detailParts = [];

      if (recordedAt) detailParts.push(`Recorded at: ${formatDate(recordedAt)}`);
      if (rawStatus) detailParts.push(`Outcome: ${rawStatus.replace(/_/g, ' ')}`);
      if (summary) {
        detailParts.push(this.truncateBreakdownText(summary, 360));
      } else if (errorText) {
        detailParts.push(this.truncateBreakdownText(errorText, 360));
      }

      return {
        title: `Run ${startIndex + index}`,
        status: mapRecordedStatus(rawStatus),
        detail: detailParts.join('\n')
      };
    });
  }

  async fetchLatestSubtasksForParent(parentTaskID) {
    if (!parentTaskID || !this.workspaceId) return [];
    try {
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) return [];
      const payload = await response.json();
      const tasks = Array.isArray(payload?.tasks) ? payload.tasks : [];
      return tasks.filter((task) => task?.parent_task_id === parentTaskID);
    } catch (error) {
      console.error('Failed to fetch latest subtasks for breakdown:', error);
      return [];
    }
  }

  async refreshExecutionBreakdown(task) {
    if (!task) {
      this.renderTaskExecutionBreakdown(null);
      return;
    }

    let subtasks = this.getSubtasksForParent(task.id);
    this.renderTaskExecutionBreakdown(task, subtasks);

    if (!subtasks.length) return;

    const latestSubtasks = await this.fetchLatestSubtasksForParent(task.id);
    if (!latestSubtasks.length) return;
    this.renderTaskExecutionBreakdown(task, latestSubtasks);
  }

  getTaskResultBreakdownSteps(task, subtasks = []) {
    if (!task) return [];

    const executionSteps = this.getExecutionStepBreakdownSteps(task);
    if (executionSteps.length > 0) {
      return executionSteps;
    }

    const sortedSubtasks = Array.isArray(subtasks) ? [...subtasks].sort((a, b) => {
      const aIndex = Number.isFinite(Number(a?.subtask_index)) ? Number(a.subtask_index) : Number.MAX_SAFE_INTEGER;
      const bIndex = Number.isFinite(Number(b?.subtask_index)) ? Number(b.subtask_index) : Number.MAX_SAFE_INTEGER;
      if (aIndex !== bIndex) return aIndex - bIndex;
      const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
      const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
      return aTime - bTime;
    }) : [];

    if (sortedSubtasks.length > 0) {
      return sortedSubtasks.map((subtask, index) => ({
        title: String(subtask.description || subtask.name || `Step ${index + 1}`).trim(),
        status: String(subtask.status || task.status || 'pending').trim(),
        detail: this.buildTaskExecutionDetail(
          subtask,
          String(subtask.details || '').trim()
        )
      }));
    }

    const retryHistorySteps = this.getRetryHistoryBreakdownSteps(task);
    const historicalRunSteps = this.getExecutionHistoryBreakdownSteps(task, {
      includeLatest: retryHistorySteps.length === 0
    });
    if (historicalRunSteps.length > 0) {
      if (retryHistorySteps.length > 0) {
        const currentRunNumber = historicalRunSteps.length + 1;
        const currentRunSteps = retryHistorySteps.map((step) => ({
          ...step,
          title: `Run ${currentRunNumber} • ${step.title}`
        }));
        return [...historicalRunSteps, ...currentRunSteps];
      }
      return historicalRunSteps;
    }

    if (retryHistorySteps.length > 0) {
      return retryHistorySteps;
    }

    return this.inferSyntheticBreakdownSteps(task);
  }

  getPredictedTaskExecutionSequence(task, subtasks = [], options = {}) {
    if (!task) return [];

    const sortedSubtasks = Array.isArray(subtasks) ? [...subtasks].sort((a, b) => {
      const aIndex = Number.isFinite(Number(a?.subtask_index)) ? Number(a.subtask_index) : Number.MAX_SAFE_INTEGER;
      const bIndex = Number.isFinite(Number(b?.subtask_index)) ? Number(b.subtask_index) : Number.MAX_SAFE_INTEGER;
      if (aIndex !== bIndex) return aIndex - bIndex;
      const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
      const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
      return aTime - bTime;
    }) : [];

    if (sortedSubtasks.length > 0) {
      return sortedSubtasks.map((subtask) => {
        const assignedAgent = String(subtask?.to || '').trim();
        return {
          title: String(subtask?.description || subtask?.name || 'Workflow step').trim() || 'Workflow step',
          detail: assignedAgent && assignedAgent !== 'unassigned'
            ? `Assigned to ${assignedAgent}.`
            : 'Needs an assigned agent before execution.',
          tag: 'Workflow'
        };
      });
    }

    return this.inferPredictedSequenceSteps(task, options);
  }

  inferPredictedSequenceSteps(task, options = {}) {
    const description = String(task?.description || task?.name || '').trim();
    const lower = description.toLowerCase();
    const requirements = this.inferTaskExecutionRequirements(description);
    const hasFilesystem = requirements.some((requirement) => requirement.key === TASK_REQUIREMENT_KEYS.FILESYSTEM);
    const hasBrowser = requirements.some((requirement) => requirement.key === TASK_REQUIREMENT_KEYS.BROWSER);
    const isFilesystemListing = this.isReadOnlyFilesystemListingIntent(description);
    const assignedAgent = String(options?.assignedAgent || '').trim();
    const agentLabel = assignedAgent && assignedAgent !== 'unassigned' ? assignedAgent : 'the selected agent';

    const toStep = (title, detail = '', tag = '') => ({ title, detail, tag });

    if (isFilesystemListing) {
      return [
        toStep('Check allowed filesystem scope', `Confirm which directories ${agentLabel} can access before inspecting folder contents.`, 'Discovery'),
        toStep('Inspect the target directory', 'Locate the requested folder and gather its visible file list.', 'Discovery'),
        toStep('Return the file list', 'Return the concrete file list, or explain clearly if the folder is missing or empty.', 'Summary')
      ];
    }

    if (hasFilesystem) {
      return [
        toStep('Check allowed filesystem scope', `Confirm which directories ${agentLabel} can access before making changes.`, 'Discovery'),
        toStep('Inspect candidate directories', 'Look through likely source folders for DNM-related material.', 'Discovery'),
        toStep('Identify DNM-related files', 'Match files by filename, path, and available task context.', 'Analysis'),
        toStep('Create the DNM folder if needed', 'Prepare the destination folder without overwriting unrelated content.', 'Mutation'),
        toStep('Move or copy matching files', 'Relocate the selected files into the DNM folder safely.', 'Mutation'),
        toStep('Verify final folder contents', 'Confirm the destination contains the expected files and note anything skipped.', 'Verify'),
        toStep('Return a summary', 'List moved files, skipped files, and any follow-up needed.', 'Summary')
      ];
    }

    if (hasBrowser) {
      return [
        toStep('Check available browser capability', `Confirm ${agentLabel} can open and interact with the required site.`, 'Discovery'),
        toStep('Open the target page', 'Navigate to the relevant website or URL for this task.', 'Action'),
        toStep('Inspect the required information or controls', 'Locate the data, form, or interface element needed to complete the task.', 'Analysis'),
        toStep('Perform the requested browser action', 'Carry out the needed interaction or extraction.', 'Action'),
        toStep('Verify the outcome', 'Confirm the page state or extracted result matches the task goal.', 'Verify'),
        toStep('Return a summary', 'Report what was done and any follow-up needed.', 'Summary')
      ];
    }

    if (lower.includes('summarize') || lower.includes('summary') || lower.includes('review')) {
      return [
        toStep('Collect the relevant context', 'Gather the information needed to answer the request.', 'Discovery'),
        toStep('Synthesize the main findings', 'Turn the collected context into a concise result.', 'Analysis'),
        toStep('Return a summary', 'Present the final result with the most relevant details.', 'Summary')
      ];
    }

    const fallbackSteps = this.inferSyntheticBreakdownSteps({ ...task, status: 'pending' });
    return fallbackSteps.map((step, index) => ({
      title: String(step?.title || `Step ${index + 1}`).trim() || `Step ${index + 1}`,
      detail: String(step?.detail || '').trim(),
      tag: index === 0 ? 'Discovery' : (index === fallbackSteps.length - 1 ? 'Summary' : 'Action')
    }));
  }

  inferSyntheticBreakdownSteps(task) {
    const description = String(task?.description || '').trim();
    const lower = description.toLowerCase();

    const toStep = (title, detail = '') => ({ title, detail });

    let baseSteps = [];
    if ((lower.includes('wear') && lower.includes('tomorrow')) || lower.includes('what should i wear')) {
      baseSteps = [
        toStep("Checking tomorrow's weather", 'Collect forecast details such as temperature, rain chance, and wind.'),
        toStep('Recommendation for clothing based on the weather', 'Translate weather conditions into practical outfit guidance.')
      ];
    } else if (lower.includes('weather')) {
      baseSteps = [
        toStep('Checking weather conditions', 'Gather forecast or relevant weather signals.'),
        toStep('Summarizing weather insight', 'Return a concise recommendation tailored to the request.')
      ];
    } else {
      baseSteps = [
        toStep('Understanding the request', 'Clarify intent and constraints from task context.'),
        toStep('Producing final recommendation', 'Generate the final answer with clear reasoning.')
      ];
    }

    const statusSequence = this.getSyntheticStepStatuses(String(task?.status || 'pending'), baseSteps.length);
    return baseSteps.map((step, index) => ({
      ...step,
      status: statusSequence[index] || String(task?.status || 'pending')
    }));
  }

  getSyntheticStepStatuses(status, count) {
    const normalized = String(status || 'pending').trim().toLowerCase();
    if (count <= 0) return [];

    if (normalized === 'completed') {
      return Array.from({ length: count }, () => 'completed');
    }
    if (normalized === 'in_progress') {
      return Array.from({ length: count }, (_value, index) => (index === 0 ? 'in_progress' : 'pending'));
    }
    if (normalized === 'failed' || normalized === 'timeout') {
      return Array.from({ length: count }, (_value, index) => {
        if (index < Math.max(0, count - 1)) return 'completed';
        return 'failed';
      });
    }
    if (normalized === 'blocked') {
      return Array.from({ length: count }, (_value, index) => {
        if (index === 0) return 'completed';
        if (index === 1) return 'blocked';
        return 'pending';
      });
    }
    return Array.from({ length: count }, () => 'pending');
  }

  getAssistStorage() {
    try {
      return window.sessionStorage;
    } catch (_error) {
      return null;
    }
  }

  clearPendingAssistSpecialistHandoff() {
    const storage = this.getAssistStorage();
    if (!storage) return;
    storage.removeItem(TASK_ASSIST_PENDING_SPECIALIST_STORAGE_KEY);
  }

  storePendingAssistSpecialistHandoff(action) {
    const storage = this.getAssistStorage();
    const taskId = String(this.currentBlockedTask?.taskId || '').trim();
    const agentName = String(action?.agentName || '').trim();
    if (!storage || !this.workspaceId || !taskId || !agentName) return;

    storage.setItem(TASK_ASSIST_PENDING_SPECIALIST_STORAGE_KEY, JSON.stringify({
      workspaceId: this.workspaceId,
      taskId,
      agentName,
      createdAt: Date.now()
    }));
  }

  maybeResumePendingAssistSpecialistHandoff() {
    if (this.pendingAssistSpecialistResumeChecked) return;
    this.pendingAssistSpecialistResumeChecked = true;

    const storage = this.getAssistStorage();
    if (!storage) return;

    let payload = null;
    try {
      payload = JSON.parse(storage.getItem(TASK_ASSIST_PENDING_SPECIALIST_STORAGE_KEY) || 'null');
    } catch (_error) {
      payload = null;
    }

    const workspaceId = String(payload?.workspaceId || '').trim();
    const taskId = String(payload?.taskId || '').trim();
    const agentName = String(payload?.agentName || '').trim();
    const createdAt = Number(payload?.createdAt || 0);
    const isExpired = !createdAt || (Date.now() - createdAt) > (15 * 60 * 1000);

    if (!workspaceId || !taskId || !agentName || workspaceId !== this.workspaceId || isExpired) {
      this.clearPendingAssistSpecialistHandoff();
      return;
    }

    const task = this.tasks.find((item) => item?.id === taskId);
    const inWorkspace = this.getWorkspaceAgentNames()
      .some((name) => this.normalizeAgentName(name) === this.normalizeAgentName(agentName));
    if (!task || !inWorkspace) {
      this.clearPendingAssistSpecialistHandoff();
      return;
    }

    this.clearPendingAssistSpecialistHandoff();
    this.openTaskAssistModal(taskId);
    if (this.elements.taskAssistAgent) {
      const hasOption = Array.from(this.elements.taskAssistAgent.options || [])
        .some((option) => this.normalizeAgentName(option?.value || '') === this.normalizeAgentName(agentName));
      if (hasOption) {
        this.elements.taskAssistAgent.value = agentName;
        this.updateAssistSwitchButtonState();
      }
    }
    if (window.Toast) {
      window.Toast.info(`"${agentName}" is ready. Switch this task to it and retry.`);
    }
  }

  findAssistSpecialistAgentName(config, sourceNames, options = {}) {
    if (!config || !Array.isArray(sourceNames)) return '';

    const exactTarget = this.normalizeAgentName(config.agentName);
    const exclude = this.normalizeAgentName(options.excludeAgent || '');
    for (const sourceName of sourceNames) {
      const name = String(sourceName || '').trim();
      if (!name) continue;
      const normalized = this.normalizeAgentName(name);
      if (!normalized || normalized === exclude) continue;
      if (normalized === exactTarget) return name;
    }

    let bestName = '';
    let bestScore = 0;
    sourceNames.forEach((sourceName) => {
      const name = String(sourceName || '').trim();
      if (!name) return;
      const normalized = this.normalizeAgentName(name);
      if (!normalized || normalized === exclude) return;

      let score = 0;
      (config.nameTokens || []).forEach((token) => {
        const normalizedToken = this.normalizeAgentName(token);
        if (normalizedToken && normalized.includes(normalizedToken)) {
          score += normalizedToken.length >= 6 ? 3 : 2;
        }
      });

      if (score > bestScore) {
        bestScore = score;
        bestName = name;
      }
    });

    return bestScore >= 3 ? bestName : '';
  }

  buildAssistSpecialistActionText(action) {
    const agentName = String(action?.agentName || action?.label || '').trim();
    const currentAgent = String(action?.currentAgent || '').trim();
    const currentLabel = currentAgent ? `"${currentAgent}"` : 'the current agent';
    if (!agentName) return '';
    if (action?.kind === 'switch') {
      return `Recommended: Hand this off to "${agentName}" instead of keeping it with ${currentLabel}.`;
    }
    if (action?.kind === 'add_and_switch') {
      return `Recommended: Add "${agentName}" to this workspace and hand the task off there.`;
    }
    if (action?.kind === 'create') {
      return `Recommended: Create "${agentName}" for this workspace, then hand the task off there.`;
    }
    return '';
  }

  isWorkspaceManagerMetaActionText(text = '') {
    const normalized = this.normalizeTaskResultNextStepToken(text);
    if (!normalized) return false;

    const actionMatch = /\b(save|create|add|attach|upload|import|export|move|rename|delete|remove|switch|assign|list|show|open|bind)\b/.test(normalized);
    if (!actionMatch) return false;

    return /\b(note|notes|task|tasks|subtask|subtasks|file|files|pdf|folder|folders|directory|directories|workspace|agent|agents|binding|bindings|canvas)\b/.test(normalized);
  }

  maybeBuildTravelAssistSpecialistAction(assistContext = {}) {
    const currentAgent = String(assistContext.currentAgent || '').trim();
    const parts = [];
    const taskText = String(assistContext.task?.description || assistContext.task?.name || '').trim();
    if (taskText) parts.push(taskText);
    const taskDetails = String(assistContext.task?.details || '').trim();
    if (taskDetails) parts.push(taskDetails);
    const question = String(assistContext.question || '').trim();
    if (question) parts.push(question);
    const response = String(assistContext.response || '').trim();
    if (response) parts.push(response);

    const workflowStep = assistContext.workflowStep;
    if (workflowStep?.stepType === 'ask_choice' && Array.isArray(workflowStep.choices)) {
      workflowStep.choices.forEach((choice) => {
        const label = String(choice?.label || '').trim();
        if (label) parts.push(label);
        const description = String(choice?.description || '').trim();
        if (description) parts.push(description);
      });
    }

    const combined = this.normalizeTaskResultNextStepToken(parts.join(' '));
    if (!combined) return null;
    if (this.isWorkspaceManagerMetaActionText(combined)) return null;

    const travelSignals = ['travel', 'trip', 'itinerary', 'hotel', 'flight', 'vacation', 'day trip', 'day trips', 'museum', 'museums', 'restaurant', 'restaurants', 'nightlife', 'accommodation', 'lodging'];
    const isTravelContext = travelSignals.some((signal) => combined.includes(this.normalizeTaskResultNextStepToken(signal)));
    if (!isTravelContext) return null;

    const scoreConfig = (config) => {
      let score = 0;
      (config.scorePhrases || []).forEach((phrase) => {
        const normalizedPhrase = this.normalizeTaskResultNextStepToken(phrase);
        if (!normalizedPhrase || !combined.includes(normalizedPhrase)) return;
        score += normalizedPhrase.includes(' ') ? 3 : 1;
      });
      return score;
    };

    const configs = Object.values(TASK_ASSIST_TRAVEL_SPECIALISTS);
    const ranked = configs
      .map((config) => ({ config, score: scoreConfig(config) }))
      .filter((entry) => entry.score > 0)
      .sort((a, b) => {
        if (b.score !== a.score) return b.score - a.score;
        const order = ['travel_itinerary', 'hotel_booking', 'flight_booking'];
        return order.indexOf(a.config.key) - order.indexOf(b.config.key);
      });

    const best = ranked[0];
    if (!best?.config) return null;

    const config = best.config;
    const workspaceAgentName = this.findAssistSpecialistAgentName(config, this.getWorkspaceAgentNames(), {
      excludeAgent: currentAgent
    });
    const catalogNames = Array.isArray(this.agentCatalog) ? this.agentCatalog.map((agent) => String(agent?.name || '').trim()).filter(Boolean) : [];
    const catalogAgent = this.findAssistSpecialistAgentName(config, catalogNames, {
      excludeAgent: currentAgent
    });
    const workspaceAgent = workspaceAgentName && catalogAgent &&
      this.normalizeAgentName(workspaceAgentName) === this.normalizeAgentName(catalogAgent)
      ? workspaceAgentName
      : '';
    if (catalogAgent && this.normalizeAgentName(catalogAgent) === this.normalizeAgentName(currentAgent)) {
      return null;
    }

    const agentName = workspaceAgent || catalogAgent || config.agentName;
    const kind = workspaceAgent ? 'switch' : (catalogAgent ? 'add_and_switch' : 'create');
    return {
      ...config,
      kind,
      currentAgent,
      agentName,
      copy: kind === 'switch'
        ? `"${agentName}" already fits this travel-planning follow-up better than ${currentAgent ? `"${currentAgent}"` : 'the current agent'}.`
        : kind === 'add_and_switch'
        ? `"${agentName}" exists already. Add it to this workspace and hand the task off there.`
        : `This workspace does not have a dedicated travel specialist yet. Create "${agentName}" and use it for this follow-up.`,
      buttonLabel: kind === 'switch'
        ? `Switch To ${agentName}`
        : kind === 'add_and_switch'
        ? `Add And Switch To ${agentName}`
        : `Create ${agentName}`
    };
  }

  async suggestTravelSpecialistSetupForTask(task, specialistAction) {
    if (!task || !specialistAction?.agentName) {
      return { handled: false, agentName: '' };
    }

    const taskLabel = this.getTaskDisplayLabel(task);
    const previewSequence = this.getPredictedTaskExecutionSequence(task, [], {
      assignedAgent: specialistAction.agentName
    });

    if (specialistAction.kind === 'switch' || specialistAction.kind === 'add_and_switch') {
      const addToWorkspace = specialistAction.kind === 'add_and_switch';
      const confirmed = await this.showTaskConfirmDialog({
        eyebrow: 'Specialist Match',
        title: addToWorkspace
          ? `Add "${specialistAction.agentName}" and assign it?`
          : `Assign "${specialistAction.agentName}" to this task?`,
        message: specialistAction.copy || this.buildAssistSpecialistActionText(specialistAction),
        confirmLabel: addToWorkspace ? 'Add and Assign' : 'Assign Specialist',
        metaItems: [taskLabel, specialistAction.agentName, addToWorkspace ? 'Add to workspace' : 'Travel specialist'],
        details: [
          String(specialistAction.description || '').trim(),
          'Substantive travel work should start with the specialist rather than the workspace manager.'
        ].filter(Boolean),
        sequenceItems: previewSequence
      });

      if (!confirmed) {
        return { handled: true, agentName: '' };
      }

      try {
        if (addToWorkspace) {
          await this.addAgentToWorkspace(specialistAction.agentName, { toast: false });
        }
        await this.assignTaskToAgent(task.id, specialistAction.agentName);
        task.to = specialistAction.agentName;
        this.renderTasks();
        if (window.Toast) {
          window.Toast.success(addToWorkspace
            ? `Added "${specialistAction.agentName}" and assigned it to this task.`
            : `Assigned "${specialistAction.agentName}" to this task.`);
        }
        return { handled: true, agentName: specialistAction.agentName };
      } catch (error) {
        console.error('Failed to apply travel specialist setup:', error);
        if (window.Toast) {
          window.Toast.error(error.message || 'Failed to assign travel specialist');
        }
        return { handled: true, agentName: '' };
      }
    }

    if (specialistAction.kind === 'create') {
      const confirmed = await this.showTaskConfirmDialog({
        eyebrow: 'Specialist Match',
        title: `Create "${specialistAction.agentName}" for this task?`,
        message: specialistAction.copy || this.buildAssistSpecialistActionText(specialistAction),
        confirmLabel: 'Create Agent',
        cancelLabel: 'Cancel',
        metaItems: [taskLabel, specialistAction.agentName, 'New travel specialist'],
        details: [
          String(specialistAction.description || '').trim(),
          'After creation, Ori will assign this task to the new specialist.'
        ].filter(Boolean),
        sequenceItems: previewSequence
      });

      if (!confirmed) {
        return { handled: true, agentName: '' };
      }

      this.openCreateAgentFlow({
        seedName: specialistAction.agentName,
        seedType: specialistAction.agentType || 'tool-calling',
        seedSystemPrompt: String(specialistAction.systemPrompt || '').trim(),
        autoDescription: String(specialistAction.description || '').trim(),
        preferAutoConfig: true,
        workspaceId: this.workspaceId,
        taskId: task.id,
        assignTask: true
      });
      return { handled: true, agentName: '' };
    }

    return { handled: false, agentName: '' };
  }

  applyAssistWorkflowSpecialistOverrides(workflowStep, specialistAction) {
    const normalizedStep = this.normalizeAssistWorkflowStep(workflowStep);
    if (!normalizedStep || !specialistAction?.agentName) return normalizedStep;

    const replacementName = String(specialistAction.agentName || '').trim();
    if (!replacementName) return normalizedStep;

    const rewriteLabel = (label) => {
      const rawLabel = String(label || '').trim();
      if (!rawLabel) return rawLabel;
      if (!/\bworkspace manager\b/i.test(rawLabel)) return rawLabel;
      if (!/\b(delegate|hand off|handoff|assign|planning task)\b/i.test(rawLabel)) return rawLabel;
      return rawLabel
        .replace(/\bthe workspace manager\b/ig, replacementName)
        .replace(/\bworkspace manager\b/ig, replacementName);
    };

    return {
      ...normalizedStep,
      choices: normalizedStep.choices.map((choice) => ({
        ...choice,
        label: rewriteLabel(choice.label),
        description: rewriteLabel(choice.description)
      }))
    };
  }

  populateAssistAgents(currentAgent = '') {
    if (!this.elements.taskAssistAgent) return;

    const select = this.elements.taskAssistAgent;
    const seen = new Set();
    const options = ['<option value="">Keep current assignment</option>'];

    const addOption = (agentName) => {
      const normalized = String(agentName || '').trim();
      const key = this.normalizeAgentName(normalized);
      if (!normalized || normalized === 'unassigned' || seen.has(key)) return;
      seen.add(key);
      const isCurrent = this.normalizeAgentName(normalized) === this.normalizeAgentName(currentAgent);
      const label = isCurrent ? `${normalized} (current)` : normalized;
      options.push(`<option value="${this.escapeHtml(normalized)}">${this.escapeHtml(label)}</option>`);
    };

    this.getWorkspaceAgentNames().forEach((name) => addOption(name));

    select.innerHTML = options.join('');
  }

  parseAssistActions(value) {
    if (Array.isArray(value)) {
      return value.map((item) => String(item || '').trim().toLowerCase()).filter(Boolean);
    }
    if (typeof value === 'string') {
      return value.split(',').map((item) => item.trim().toLowerCase()).filter(Boolean);
    }
    return [];
  }

  cleanAssistChoiceText(value) {
    return this.cleanTaskResultNextStepText(value).replace(/[?!.,;:]+$/g, '').trim();
  }

  buildAssistChoiceId(number, label) {
    const base = this.normalizeTaskResultNextStepToken(label)
      .replace(/\s+/g, '-')
      .slice(0, 48) || 'choice';
    return `assist-choice-${String(number || '').trim() || 'x'}-${base}`;
  }

  normalizeAssistWorkflowStep(value) {
    if (!value || typeof value !== 'object') return null;

    const stepType = String(value.step_type || value.stepType || '').trim().toLowerCase();
    const rawChoices = Array.isArray(value.choices) ? value.choices : [];
    const choices = rawChoices.map((item, index) => {
      const label = this.cleanAssistChoiceText(item?.label || '');
      if (!label) return null;
      const number = String(item?.number || index + 1).trim();
      return {
        id: String(item?.id || this.buildAssistChoiceId(number, label)).trim(),
        label,
        number,
        description: this.cleanAssistChoiceText(item?.description || '')
      };
    }).filter(Boolean);

    if (stepType !== 'ask_choice' || choices.length === 0) return null;

    let freeTextAllowed = true;
    if (typeof value.free_text_allowed === 'boolean') {
      freeTextAllowed = value.free_text_allowed;
    } else if (typeof value.freeTextAllowed === 'boolean') {
      freeTextAllowed = value.freeTextAllowed;
    }

    return {
      stepType: 'ask_choice',
      title: String(value.title || '').trim() || 'Choose the next step',
      summary: String(value.summary || '').trim(),
      freeTextAllowed,
      choices
    };
  }

  extractAssistEnumeratedChoices(text) {
    const lines = String(text || '').split(/\r?\n/);
    const choices = [];
    let started = false;

    for (let i = 0; i < lines.length; i += 1) {
      const rawLine = String(lines[i] || '');
      const match = rawLine.match(/^\s*(?:[-*]\s*)?(\d+)[.)]\s*(.+)$/);
      if (match) {
        const number = String(match[1] || '').trim();
        const label = this.cleanAssistChoiceText(match[2]);
        if (!label) continue;
        choices.push({
          id: this.buildAssistChoiceId(number, label),
          label,
          number,
          description: ''
        });
        started = true;
        if (choices.length >= 5) break;
        continue;
      }

      if (!started) continue;
      if (!rawLine.trim()) continue;
      break;
    }

    return choices.length >= 2 ? choices : [];
  }

  extractAssistInlineChoices(text) {
    const lines = String(text || '').split(/\r?\n/);
    const patterns = [
      /^(?:want me to|would you like me to|do you want me to|should i)\s+(.+?)(?:,\s*|\s+)or\s+(.+?)\?\s*$/i
    ];

    for (let i = lines.length - 1; i >= 0; i -= 1) {
      const rawLine = this.cleanTaskResultNextStepText(lines[i]);
      if (!rawLine.includes('?')) continue;
      for (const pattern of patterns) {
        const match = rawLine.match(pattern);
        if (!match) continue;

        const first = this.cleanAssistChoiceText(match[1]);
        const second = this.cleanAssistChoiceText(match[2]);
        if (!first || !second || this.normalizeTaskResultNextStepToken(first) === this.normalizeTaskResultNextStepToken(second)) {
          continue;
        }

        return [
          {
            id: this.buildAssistChoiceId('1', first),
            label: first,
            number: '1',
            description: ''
          },
          {
            id: this.buildAssistChoiceId('2', second),
            label: second,
            number: '2',
            description: ''
          }
        ];
      }
    }

    return [];
  }

  buildAssistWorkflowStepFromText(...values) {
    for (const value of values) {
      const enumeratedChoices = this.extractAssistEnumeratedChoices(value);
      if (enumeratedChoices.length > 0) {
        return {
          stepType: 'ask_choice',
          title: 'Choose the next step',
          summary: 'Pick one option below to continue this task.',
          freeTextAllowed: true,
          choices: enumeratedChoices
        };
      }

      const inlineChoices = this.extractAssistInlineChoices(value);
      if (inlineChoices.length > 0) {
        return {
          stepType: 'ask_choice',
          title: 'Choose the next step',
          summary: 'Pick one option below to continue this task.',
          freeTextAllowed: true,
          choices: inlineChoices
        };
      }
    }
    return null;
  }

  getAssistResponseDisplayText(response, question) {
    const rawResponse = String(response || '').trim();
    const rawQuestion = String(question || '').trim();
    if (!rawResponse) return '';
    if (!rawQuestion) return rawResponse;

    const normalize = (value) => this.cleanTaskResultNextStepText(value)
      .replace(/[?!.,;:]+$/g, '')
      .toLowerCase();

    const normalizedQuestion = normalize(rawQuestion);
    if (!normalizedQuestion) return rawResponse;

    const paragraphs = rawResponse
      .split(/\n\s*\n/)
      .map((part) => String(part || '').trim())
      .filter(Boolean);
    if (paragraphs.length > 0 && normalize(paragraphs[paragraphs.length - 1]) === normalizedQuestion) {
      paragraphs.pop();
      return paragraphs.join('\n\n').trim();
    }

    const lines = rawResponse.split(/\r?\n/);
    while (lines.length > 0 && !String(lines[lines.length - 1] || '').trim()) {
      lines.pop();
    }
    if (lines.length > 0 && normalize(lines[lines.length - 1]) === normalizedQuestion) {
      lines.pop();
      while (lines.length > 0 && !String(lines[lines.length - 1] || '').trim()) {
        lines.pop();
      }
      return lines.join('\n').trim();
    }

    return rawResponse;
  }

  setAssistWorkflowStepUI(workflowStep) {
    if (!this.elements.taskAssistChoiceWrap || !this.elements.taskAssistChoiceList) return;

    const normalizedStep = this.normalizeAssistWorkflowStep(workflowStep);
    if (!normalizedStep || normalizedStep.stepType !== 'ask_choice' || normalizedStep.choices.length === 0) {
      this.elements.taskAssistChoiceList.innerHTML = '';
      if (this.elements.taskAssistChoiceSummary) {
        this.elements.taskAssistChoiceSummary.textContent = '';
        this.elements.taskAssistChoiceSummary.classList.add('d-none');
      }
      this.elements.taskAssistChoiceWrap.classList.add('d-none');
      return;
    }

    const selectedChoiceId = String(this.currentBlockedTask?.selectedChoiceId || '').trim();
    this.elements.taskAssistChoiceList.innerHTML = normalizedStep.choices.map((choice) => `
      <button
        type="button"
        class="home-assistant-planning-choice${selectedChoiceId === choice.id ? ' is-selected' : ''}"
        data-assist-choice-id="${this.escapeHtml(choice.id)}"
        aria-pressed="${selectedChoiceId === choice.id ? 'true' : 'false'}"
      >
        <span class="home-assistant-planning-choice-label">${this.escapeHtml(choice.number ? `${choice.number}. ${choice.label}` : choice.label)}</span>
        ${choice.description ? `<span class="home-assistant-planning-choice-hint">${this.escapeHtml(choice.description)}</span>` : ''}
      </button>
    `).join('');

    this.elements.taskAssistChoiceList.querySelectorAll('[data-assist-choice-id]').forEach((button) => {
      button.addEventListener('click', () => {
        const choiceId = String(button.getAttribute('data-assist-choice-id') || '').trim();
        if (!choiceId || !this.currentBlockedTask) return;
        const selectedChoice = normalizedStep.choices.find((choice) => choice.id === choiceId);
        if (!selectedChoice) return;
        this.currentBlockedTask.selectedChoiceId = selectedChoice.id;
        this.currentBlockedTask.selectedChoiceLabel = selectedChoice.label;
        this.currentBlockedTask.selectedChoiceNumber = selectedChoice.number || '';
        this.currentBlockedTask.workflowStep = normalizedStep;
        this.setAssistWorkflowStepUI(normalizedStep);
      });
    });

    if (this.elements.taskAssistChoiceSummary) {
      const summary = String(normalizedStep.summary || '').trim();
      this.elements.taskAssistChoiceSummary.textContent = summary;
      this.elements.taskAssistChoiceSummary.classList.toggle('d-none', !summary);
    }

    this.elements.taskAssistChoiceWrap.classList.remove('d-none');
  }

  getAssistActionButton(action) {
    if (action === 'retry') return this.elements.taskAssistRetryBtn;
    if (action === 'continue_with_instruction') return this.elements.taskAssistContinueBtn;
    if (action === 'switch_agent_retry') return this.elements.taskAssistSwitchBtn;
    if (action === 'mark_failed') return this.elements.taskAssistFailBtn;
    return null;
  }

  updateAssistSwitchButtonState() {
    if (!this.elements.taskAssistSwitchBtn) return;
    const selectedAgent = String(this.elements.taskAssistAgent?.value || '').trim();
    this.elements.taskAssistSwitchBtn.disabled = selectedAgent === '';
  }

  setAssistSpecialistActionUI(action) {
    this.currentAssistSpecialistAction = action || null;

    const wrap = this.elements.taskAssistSpecialistWrap;
    const copy = this.elements.taskAssistSpecialistCopy;
    const button = this.elements.taskAssistSpecialistActionBtn;
    if (!wrap || !copy || !button) return;

    if (!action || !action.buttonLabel) {
      copy.textContent = '';
      copy.classList.add('d-none');
      button.textContent = '';
      wrap.classList.add('d-none');
      return;
    }

    const detail = String(action.copy || '').trim();
    copy.textContent = detail;
    copy.classList.toggle('d-none', !detail);
    button.textContent = action.buttonLabel;
    wrap.classList.remove('d-none');
  }

  async handleAssistSpecialistAction() {
    const action = this.currentAssistSpecialistAction;
    const button = this.elements.taskAssistSpecialistActionBtn;
    if (!action || !button) return;

    const originalText = button.textContent;
    button.disabled = true;

    try {
      if (action.kind === 'switch') {
        if (this.elements.taskAssistAgent) {
          this.elements.taskAssistAgent.value = action.agentName;
          this.updateAssistSwitchButtonState();
        }
        await this.submitTaskAssist('switch_agent_retry');
        return;
      }

      if (action.kind === 'add_and_switch') {
        button.textContent = `Adding ${action.agentName}...`;
        await this.addAgentToWorkspace(action.agentName, {
          toast: false,
          closeModal: false
        });
        this.populateAssistAgents(this.currentBlockedTask?.currentAgent || '');
        if (this.elements.taskAssistAgent) {
          this.elements.taskAssistAgent.value = action.agentName;
          this.updateAssistSwitchButtonState();
        }
        if (window.Toast) {
          window.Toast.success(`Added "${action.agentName}" to this workspace.`);
        }
        await this.submitTaskAssist('switch_agent_retry');
        return;
      }

      if (action.kind === 'create') {
        this.storePendingAssistSpecialistHandoff(action);
        this.openCreateAgentFlow({
          seedName: action.agentName,
          seedType: action.agentType || 'tool-calling',
          seedSystemPrompt: String(action.systemPrompt || '').trim(),
          autoDescription: String(action.description || '').trim(),
          preferAutoConfig: true,
          workspaceId: this.workspaceId
        });
        return;
      }
    } catch (error) {
      console.error('Failed to apply assist specialist action:', error);
      this.clearPendingAssistSpecialistHandoff();
      if (window.Toast) {
        window.Toast.error(error?.message || 'Failed to apply specialist handoff');
      }
    } finally {
      button.disabled = false;
      button.textContent = originalText;
    }
  }

  setAssistRecommendationUI(recommendation) {
    this.currentAssistRecommendation = recommendation || null;

    [
      this.elements.taskAssistRetryBtn,
      this.elements.taskAssistContinueBtn,
      this.elements.taskAssistSwitchBtn,
      this.elements.taskAssistFailBtn
    ].forEach((button) => {
      button?.classList.remove('is-recommended');
    });

    if (this.elements.taskAssistRecommendationWrap && this.elements.taskAssistRecommendation) {
      if (recommendation && recommendation.text) {
        this.elements.taskAssistRecommendation.textContent = recommendation.text;
        this.elements.taskAssistRecommendationWrap.classList.remove('d-none');
      } else {
        this.elements.taskAssistRecommendation.textContent = '';
        this.elements.taskAssistRecommendationWrap.classList.add('d-none');
      }
    }

    this.setAssistSpecialistActionUI(recommendation?.specialistAction || null);

    if (recommendation?.suggestedAgent && this.elements.taskAssistAgent) {
      const hasOption = Array.from(this.elements.taskAssistAgent.options || [])
        .some((option) => String(option?.value || '') === recommendation.suggestedAgent);
      if (hasOption) {
        this.elements.taskAssistAgent.value = recommendation.suggestedAgent;
      }
    }

    const recommendedButton = this.getAssistActionButton(recommendation?.action || '');
    if (recommendedButton) {
      recommendedButton.classList.add('is-recommended');
    }

    this.updateAssistSwitchButtonState();
  }

  determineAssistRecommendation(reasonCode, suggestedActions, currentAgent, workflowStep = null, assistContext = {}) {
    const normalizedCode = String(reasonCode || '').trim().toLowerCase();
    const actions = this.parseAssistActions(suggestedActions);
    const allows = (action) => actions.length === 0 || actions.includes(action);
    const browserRelated = normalizedCode === 'capability_mismatch' ||
      normalizedCode === 'capability_refusal' ||
      normalizedCode.includes('capability');
    const browserAgent = browserRelated ? this.findBestBrowserCapableAgent(currentAgent) : '';
    const specialistAction = this.maybeBuildTravelAssistSpecialistAction({
      ...assistContext,
      currentAgent,
      workflowStep
    });

    if (specialistAction) {
      return {
        action: specialistAction.kind === 'switch' && allows('switch_agent_retry') ? 'switch_agent_retry' : '',
        suggestedAgent: specialistAction.kind === 'switch' ? specialistAction.agentName : '',
        text: this.buildAssistSpecialistActionText(specialistAction),
        specialistAction
      };
    }

    if (workflowStep?.stepType === 'ask_choice' && Array.isArray(workflowStep.choices) && workflowStep.choices.length > 0 &&
        allows('continue_with_instruction')) {
      return {
        action: 'continue_with_instruction',
        text: 'Recommended: Choose one of the suggested next steps below or add your own guidance.'
      };
    }

    if (browserRelated && browserAgent && allows('switch_agent_retry')) {
      return {
        action: 'switch_agent_retry',
        suggestedAgent: browserAgent,
        text: `Recommended: Switch to "${browserAgent}" and retry.`
      };
    }

    if (normalizedCode === 'filesystem_result_unverified' && allows('retry')) {
      return {
        action: 'retry',
        text: 'Recommended: Retry so the agent can verify the folder contents with filesystem tools.'
      };
    }

    if (allows('retry')) {
      return {
        action: 'retry',
        text: 'Recommended: Retry with the current setup.'
      };
    }

    if (allows('continue_with_instruction')) {
      return {
        action: 'continue_with_instruction',
        text: 'Recommended: Add guidance and continue.'
      };
    }

    if (allows('switch_agent_retry') && browserAgent) {
      return {
        action: 'switch_agent_retry',
        suggestedAgent: browserAgent,
        text: `Recommended: Switch to "${browserAgent}" and retry.`
      };
    }

    return null;
  }

  openTaskAssistModal(taskId, eventData = null) {
    const task = this.tasks.find((item) => item.id === taskId);
    if (!task) return;

    const humanLoop = this.getTaskHumanLoop(task) || {};
    const payload = (eventData && typeof eventData === 'object') ? eventData : {};
    const payloadHumanLoop = payload.human_loop && typeof payload.human_loop === 'object'
      ? payload.human_loop
      : {};
    const blockId = String(payload.block_id || humanLoop.block_id || '').trim();
    const reasonCode = String(payload.reason_code || payloadHumanLoop.reason_code || humanLoop.reason_code || '').trim();
    const suggestedActions = payload.suggested_actions || payloadHumanLoop.suggested_actions || humanLoop.suggested_actions;
    const reason = String(payload.reason || payloadHumanLoop.reason || humanLoop.reason || 'The assigned agent needs guidance before it can continue.').trim();
    const question = String(payload.question || payloadHumanLoop.question || humanLoop.question || 'How should I proceed?').trim();
    const response = String(payload.agent_response || payloadHumanLoop.agent_response || humanLoop.agent_response || '').trim();
    const displayResponse = this.getAssistResponseDisplayText(response, question);
    const statusText = getDisplayStatus(task.status);
    const timestamp = formatDate(task.updated_at || task.created_at);

    const currentAgent = String(task.to || '').trim();
    const baseWorkflowStep = this.normalizeAssistWorkflowStep(payload.workflow_step)
      || this.normalizeAssistWorkflowStep(payloadHumanLoop.workflow_step)
      || this.normalizeAssistWorkflowStep(humanLoop.workflow_step)
      || this.normalizeAssistWorkflowStep(task?.context?.planning_workflow_step)
      || this.buildAssistWorkflowStepFromText(question, response);
    const recommendation = this.determineAssistRecommendation(
      reasonCode,
      suggestedActions,
      currentAgent,
      baseWorkflowStep,
      {
        task,
        reason,
        question,
        response
      }
    );
    const workflowStep = this.applyAssistWorkflowSpecialistOverrides(baseWorkflowStep, recommendation?.specialistAction);
    this.currentBlockedTask = {
      taskId,
      blockId,
      currentAgent,
      reasonCode,
      suggestedActions: this.parseAssistActions(suggestedActions),
      workflowStep,
      specialistAction: recommendation?.specialistAction || null,
      selectedChoiceId: '',
      selectedChoiceLabel: '',
      selectedChoiceNumber: ''
    };

    this.setTaskModalHeaderId(this.elements.taskAssistId, task.id);
    if (this.elements.taskAssistMeta) {
      this.elements.taskAssistMeta.textContent = `${task.description || task.name || task.id} • ${statusText} • ${timestamp}`;
    }
    if (this.elements.taskAssistReason) {
      this.elements.taskAssistReason.textContent = reason;
    }
    if (this.elements.taskAssistQuestion) {
      this.elements.taskAssistQuestion.textContent = question;
    }
    if (this.elements.taskAssistMessage) {
      this.elements.taskAssistMessage.value = '';
    }
    if (this.elements.taskAssistResponse && this.elements.taskAssistResponseWrap) {
      if (displayResponse) {
        this.elements.taskAssistResponse.textContent = displayResponse;
        this.elements.taskAssistResponseWrap.classList.remove('d-none');
      } else {
        this.elements.taskAssistResponse.textContent = '';
        this.elements.taskAssistResponseWrap.classList.add('d-none');
      }
    }

    this.populateAssistAgents(currentAgent);
    this.setAssistWorkflowStepUI(workflowStep);
    this.setAssistRecommendationUI(recommendation);

    if (this.elements.taskAssistModal && window.bootstrap) {
      const modal = typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.taskAssistModal)
        : (bootstrap.Modal.getInstance(this.elements.taskAssistModal) || new bootstrap.Modal(this.elements.taskAssistModal));
      modal.show();
    }
  }

  async submitTaskAssist(action) {
    if (!this.currentBlockedTask?.taskId) return;

    const selectedAgent = String(this.elements.taskAssistAgent?.value || '').trim();
    const message = String(this.elements.taskAssistMessage?.value || '').trim();
    const selectedChoiceId = String(this.currentBlockedTask?.selectedChoiceId || '').trim();
    const selectedChoiceLabel = String(this.currentBlockedTask?.selectedChoiceLabel || '').trim();
    const selectedChoiceNumber = String(this.currentBlockedTask?.selectedChoiceNumber || '').trim();
    if (action === 'switch_agent_retry' && !selectedAgent) {
      if (window.Toast) window.Toast.error('Select an agent to switch before retrying.');
      return;
    }
    if (action === 'switch_agent_retry' &&
        this.normalizeAgentName(selectedAgent) === this.normalizeAgentName(this.currentBlockedTask.currentAgent)) {
      if (window.Toast) window.Toast.error('Select a different agent before switching.');
      return;
    }
    if (action === 'continue_with_instruction' &&
        this.currentBlockedTask.workflowStep?.stepType === 'ask_choice' &&
        !selectedChoiceId &&
        !message) {
      if (window.Toast) window.Toast.warning('Choose a next step or add guidance before continuing.');
      return;
    }

    const payload = {
      action,
      block_id: this.currentBlockedTask.blockId || undefined,
      message: message || undefined,
      agent: selectedAgent || undefined,
      choice_id: selectedChoiceId || undefined,
      choice_label: selectedChoiceLabel || undefined,
      choice_number: selectedChoiceNumber || undefined
    };

    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(this.currentBlockedTask.taskId)}/assist`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to submit assistance');
      }

      if (window.Toast) window.Toast.success('Task updated');
      await this.loadTasks();

      if (action !== 'mark_failed' && this.elements.taskAssistModal && window.bootstrap) {
        const modal = bootstrap.Modal.getInstance(this.elements.taskAssistModal);
        modal?.hide();
      }
    } catch (error) {
      console.error('Failed to assist blocked task:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to assist task');
    }
  }

  normalizeResultText(value) {
    if (value === undefined || value === null) return '';
    if (typeof value === 'string') return value;
    if (typeof value === 'number' || typeof value === 'boolean') return String(value);
    try {
      return JSON.stringify(value, null, 2);
    } catch (_error) {
      return String(value);
    }
  }

  getSubtasksForParent(taskId) {
    if (!taskId) return [];
    return this.tasks.filter((task) => task.parent_task_id === taskId);
  }

  normalizeAgentName(name) {
    return String(name || '').trim().toLowerCase();
  }

  getWorkspaceAgentNames() {
    if (!this.workspace) return [];

    const seen = new Set();
    const names = [];
    const add = (name) => {
      const normalized = String(name || '').trim();
      if (!normalized || normalized === 'unassigned') return;
      const key = this.normalizeAgentName(normalized);
      if (seen.has(key)) return;
      seen.add(key);
      names.push(normalized);
    };

    if (Array.isArray(this.workspace.agent_instances)) {
      this.workspace.agent_instances.forEach((instance) => add(instance?.name));
    }
    if (Array.isArray(this.workspace.agents)) {
      this.workspace.agents.forEach((name) => add(name));
    }

    return names;
  }

  getAgentProfile(agentName) {
    const key = this.normalizeAgentName(agentName);
    if (!key || !this.agentIndex) return null;
    return this.agentIndex.get(key) || null;
  }

  normalizeWorkspaceMCPBinding(binding, source = 'workspace') {
    const emailAccount = binding?.email_account && typeof binding.email_account === 'object'
      ? { ...binding.email_account }
      : null;

    return {
      id: String(binding?.id || '').trim(),
      serverName: String(binding?.server_name || binding?.serverName || '').trim(),
      alias: String(binding?.alias || '').trim(),
      enabled: binding?.enabled !== false,
      scope: binding?.scope && typeof binding.scope === 'object' ? { ...binding.scope } : {},
      config: binding?.config && typeof binding.config === 'object' ? { ...binding.config } : {},
      emailAccount,
      emailAccountMissing: binding?.email_account_missing === true || binding?.emailAccountMissing === true,
      source
    };
  }

  getExplicitWorkspaceMCPBindings() {
    if (!this.workspace || !Array.isArray(this.workspace.mcp_bindings)) {
      return [];
    }

    return this.workspace.mcp_bindings
      .map((binding) => this.normalizeWorkspaceMCPBinding(binding, 'workspace'))
      .filter((binding) => binding.id && binding.serverName);
  }

  getWorkspaceMCPBindings(options = {}) {
    if (!this.workspace) return [];

    const includeDisabled = options.includeDisabled === true;
    const explicitBindings = this.getExplicitWorkspaceMCPBindings();
    const explicitFilesystemExists = explicitBindings.some(
      (binding) => binding.serverName.toLowerCase() === 'filesystem'
    );

    const visibleExplicitBindings = includeDisabled
      ? explicitBindings
      : explicitBindings.filter((binding) => binding.enabled);

    const directoryRoots = Array.isArray(this.workspace.directory_references)
      ? this.workspace.directory_references
        .map((reference) => String(reference?.path || '').trim())
        .filter(Boolean)
      : [];

    if (explicitFilesystemExists || directoryRoots.length === 0) {
      return visibleExplicitBindings;
    }

    return [
      ...visibleExplicitBindings,
      {
        id: 'workspace-filesystem',
        serverName: 'filesystem',
        alias: 'workspace_filesystem',
        enabled: true,
        scope: { roots: directoryRoots },
        source: 'synthesized'
      }
    ];
  }

  getWorkspaceExplicitMCPBinding(bindingId) {
    const normalizedBindingId = String(bindingId || '').trim().toLowerCase();
    if (!normalizedBindingId) return null;
    return this.getExplicitWorkspaceMCPBindings().find(
      (binding) => String(binding?.id || '').trim().toLowerCase() === normalizedBindingId
    ) || null;
  }

  getWorkspaceMCPBinding(bindingId, options = {}) {
    const normalizedBindingId = String(bindingId || '').trim().toLowerCase();
    if (!normalizedBindingId) return null;
    return this.getWorkspaceMCPBindings(options).find(
      (binding) => String(binding?.id || '').trim().toLowerCase() === normalizedBindingId
    ) || null;
  }

  isWorkspaceRuntimeMCPServerName(serverName) {
    return String(serverName || '').trim().toLowerCase().startsWith('ws:');
  }

  async loadAvailableMCPServers(force = false) {
    if (!force && Array.isArray(this.availableMCPServers) && this.availableMCPServers.length > 0) {
      return this.availableMCPServers;
    }
    if (!force && this.availableMCPServersPromise) {
      return this.availableMCPServersPromise;
    }

    this.availableMCPServersPromise = (async () => {
      const response = await fetch('/api/mcp/servers');
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to load MCP connectors');
      }

      const data = await response.json();
      const seen = new Set();
      const servers = (Array.isArray(data?.servers) ? data.servers : [])
        .map((server) => ({
          name: String(server?.name || '').trim(),
          enabled: server?.enabled !== false
        }))
        .filter((server) => server.name && server.enabled && !this.isWorkspaceRuntimeMCPServerName(server.name))
        .filter((server) => {
          const key = server.name.toLowerCase();
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })
        .sort((left, right) => left.name.localeCompare(right.name));

      this.availableMCPServers = servers;
      return servers;
    })();

    try {
      return await this.availableMCPServersPromise;
    } finally {
      this.availableMCPServersPromise = null;
    }
  }

  isEmailWorkspaceMCPServerName(serverName) {
    switch (String(serverName || '').trim().toLowerCase()) {
      case 'email':
      case 'gmail':
      case 'microsoft-mail':
      case 'microsoft':
      case 'outlook-mail':
      case 'imap-smtp':
      case 'imap_smtp':
        return true;
      default:
        return false;
    }
  }

  normalizeWorkspaceEmailAccount(account) {
    if (!account || typeof account !== 'object') {
      return null;
    }

    return {
      id: String(account.id || '').trim(),
      vaultId: String(account.vault_id || account.vaultId || '').trim(),
      workspaceId: String(account.workspace_id || account.workspaceId || '').trim(),
      label: String(account.label || '').trim(),
      provider: String(account.provider || '').trim(),
      emailAddress: String(account.email_address || account.emailAddress || '').trim(),
      displayName: String(account.display_name || account.displayName || '').trim(),
      authType: String(account.auth_type || account.authType || '').trim(),
      credentials: account.credentials && typeof account.credentials === 'object'
        ? { ...account.credentials }
        : (account.credentials_status && typeof account.credentials_status === 'object'
          ? { ...account.credentials_status }
          : {})
    };
  }

  async loadAvailableEmailAccounts(force = false) {
    if (!force && Array.isArray(this.availableEmailAccounts) && this.availableEmailAccounts.length > 0) {
      return this.availableEmailAccounts;
    }
    if (!force && this.availableEmailAccountsPromise) {
      return this.availableEmailAccountsPromise;
    }

    this.availableEmailAccountsPromise = (async () => {
      const response = await fetch('/api/vault/email-accounts');
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to load email accounts');
      }

      const data = await response.json();
      const seen = new Set();
      const accounts = (Array.isArray(data?.accounts) ? data.accounts : [])
        .map((account) => this.normalizeWorkspaceEmailAccount(account))
        .filter((account) => account && account.id)
        .filter((account) => {
          const key = account.id.toLowerCase();
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })
        .sort((left, right) => {
          const leftLabel = left.label || left.emailAddress || left.id;
          const rightLabel = right.label || right.emailAddress || right.id;
          return leftLabel.localeCompare(rightLabel);
        });

      this.availableEmailAccounts = accounts;
      return accounts;
    })();

    try {
      return await this.availableEmailAccountsPromise;
    } finally {
      this.availableEmailAccountsPromise = null;
    }
  }

  getVisibleWorkspaceEmailAccounts(existingBinding = null) {
    const workspaceID = String(this.workspaceId || '').trim().toLowerCase();
    const existingAccount = this.normalizeWorkspaceEmailAccount(existingBinding?.emailAccount);
    const existingAccountID = String(existingBinding?.config?.account_id || existingAccount?.id || '').trim().toLowerCase();
    const accounts = Array.isArray(this.availableEmailAccounts) ? [...this.availableEmailAccounts] : [];
    const filtered = accounts.filter((account) => {
      const accountWorkspaceID = String(account.workspaceId || '').trim().toLowerCase();
      return !accountWorkspaceID || accountWorkspaceID === workspaceID;
    });

    if (existingAccount && existingAccount.id && !filtered.some((account) => account.id.toLowerCase() === existingAccount.id.toLowerCase())) {
      filtered.unshift(existingAccount);
    } else if (existingAccountID && !filtered.some((account) => account.id.toLowerCase() === existingAccountID)) {
      filtered.unshift({
        id: String(existingBinding?.config?.account_id || '').trim(),
        label: String(existingBinding?.config?.account_id || 'Unavailable email account'),
        provider: '',
        emailAddress: '',
        authType: '',
        workspaceId: ''
      });
    }

    return filtered;
  }

  setWorkspaceMCPEmailAccountHelp(message, isError = false) {
    if (!this.elements.mcpEmailAccountHelp) return;
    this.elements.mcpEmailAccountHelp.textContent = message;
    this.elements.mcpEmailAccountHelp.classList.toggle('is-error', !!isError);
  }

  populateWorkspaceMCPEmailAccountOptions(selectedAccountID = '', existingBinding = null) {
    if (!this.elements.mcpEmailAccountSelect) return;

    const normalizedSelected = String(selectedAccountID || '').trim();
    const accounts = this.getVisibleWorkspaceEmailAccounts(existingBinding);
    const options = ['<option value="">Select an email account</option>'];

    accounts.forEach((account) => {
      const id = String(account?.id || '').trim();
      if (!id) return;

      const selected = normalizedSelected && id.toLowerCase() === normalizedSelected.toLowerCase() ? ' selected' : '';
      const accountLabel = account.label || account.emailAddress || id;
      const details = [account.provider, account.emailAddress].filter(Boolean).join(' • ');
      const text = details ? `${accountLabel} (${details})` : accountLabel;
      options.push(`<option value="${this.escapeHtml(id)}"${selected}>${this.escapeHtml(text)}</option>`);
    });

    this.elements.mcpEmailAccountSelect.innerHTML = options.join('');
    if (normalizedSelected) {
      this.elements.mcpEmailAccountSelect.value = normalizedSelected;
    }

    if (accounts.length === 0) {
      this.setWorkspaceMCPEmailAccountHelp('No unlocked email accounts are available. Create one in Vault > Email Accounts.', true);
      return;
    }

    this.setWorkspaceMCPEmailAccountHelp('Email accounts come from Vault > Email Accounts. Workspace-scoped accounts must match this workspace.', false);
  }

  getWorkspaceMCPSelectedEmailActions() {
    const mapping = [
      ['read', this.elements.mcpEmailActionRead],
      ['search', this.elements.mcpEmailActionSearch],
      ['draft', this.elements.mcpEmailActionDraft],
      ['send', this.elements.mcpEmailActionSend]
    ];

    return mapping
      .filter(([, element]) => element?.checked)
      .map(([action]) => action);
  }

  setWorkspaceMCPEmailActions(actions = []) {
    const selected = new Set(
      (Array.isArray(actions) ? actions : [])
        .map((action) => String(action || '').trim().toLowerCase())
        .filter(Boolean)
    );

    if (this.elements.mcpEmailActionRead) this.elements.mcpEmailActionRead.checked = selected.has('read');
    if (this.elements.mcpEmailActionSearch) this.elements.mcpEmailActionSearch.checked = selected.has('search');
    if (this.elements.mcpEmailActionDraft) this.elements.mcpEmailActionDraft.checked = selected.has('draft');
    if (this.elements.mcpEmailActionSend) this.elements.mcpEmailActionSend.checked = selected.has('send');
    this.handleWorkspaceMCPEmailActionChange();
  }

  handleWorkspaceMCPEmailActionChange() {
    const selected = this.getWorkspaceMCPSelectedEmailActions();
    const canSend = selected.includes('send');

    if (this.elements.mcpEmailSendConfirmWrap) {
      this.elements.mcpEmailSendConfirmWrap.classList.toggle('d-none', !canSend);
    }
    if (this.elements.mcpEmailSendConfirmInput && canSend && !this.elements.mcpEmailSendConfirmInput.checked) {
      this.elements.mcpEmailSendConfirmInput.checked = true;
    }
  }

  updateWorkspaceMCPEmailAccountSummary(existingBinding = null) {
    if (!this.elements.mcpEmailAccountSummary) return;

    const selectedAccountID = String(this.elements.mcpEmailAccountSelect?.value || '').trim();
    const accounts = this.getVisibleWorkspaceEmailAccounts(existingBinding);
    const account = accounts.find((item) => String(item?.id || '').trim() === selectedAccountID) || this.normalizeWorkspaceEmailAccount(existingBinding?.emailAccount);

    if (!selectedAccountID && !account) {
      this.elements.mcpEmailAccountSummary.textContent = 'Select an email account to review its provider, address, and stored credential status.';
      return;
    }

    if (!account) {
      this.elements.mcpEmailAccountSummary.textContent = 'The saved email account is currently unavailable. Unlock the correct vault or choose another account.';
      return;
    }

    const credentialState = account.credentials || {};
    const stored = [];
    if (credentialState.has_refresh_token) stored.push('refresh token');
    if (credentialState.has_access_token) stored.push('access token');
    if (credentialState.has_password) stored.push(account.authType === 'app_password' ? 'app password' : 'password');
    if (credentialState.has_client_id) stored.push('client id');
    if (credentialState.has_client_secret) stored.push('client secret');

    const summary = [
      account.label || account.emailAddress || account.id,
      account.emailAddress,
      account.provider,
      account.authType
    ].filter(Boolean).join(' • ');

    this.elements.mcpEmailAccountSummary.textContent = stored.length > 0
      ? `${summary}. Stored in vault: ${stored.join(', ')}.`
      : `${summary}. No credential status is currently available.`;
  }

  async syncWorkspaceMCPEmailFields(existingBinding = null) {
    const serverName = String(this.elements.mcpServerSelect?.value || '').trim();
    const isEmailServer = this.isEmailWorkspaceMCPServerName(serverName);

    if (this.elements.mcpEmailFields) {
      this.elements.mcpEmailFields.classList.toggle('d-none', !isEmailServer);
    }
    if (this.elements.mcpConfigDetails) {
      this.elements.mcpConfigDetails.open = !isEmailServer;
    }

    if (!isEmailServer) {
      this.updateWorkspaceMCPEmailAccountSummary();
      return;
    }

    try {
      await this.loadAvailableEmailAccounts(true);
      this.populateWorkspaceMCPEmailAccountOptions(
        existingBinding?.config?.account_id || this.elements.mcpEmailAccountSelect?.value || '',
        existingBinding
      );
    } catch (error) {
      console.error('Failed to load email accounts for MCP modal:', error);
      this.availableEmailAccounts = [];
      this.populateWorkspaceMCPEmailAccountOptions(
        existingBinding?.config?.account_id || this.elements.mcpEmailAccountSelect?.value || '',
        existingBinding
      );
      this.setWorkspaceMCPEmailAccountHelp(error.message || 'Failed to load email accounts', true);
    }

    this.updateWorkspaceMCPEmailAccountSummary(existingBinding);
    this.handleWorkspaceMCPEmailActionChange();
  }

  getWorkspaceAgentAccessEntry(agentInstanceId) {
    const normalizedAgentInstanceId = String(agentInstanceId || '').trim();
    if (!normalizedAgentInstanceId || !this.workspace || !Array.isArray(this.workspace.agent_mcp_access)) {
      return null;
    }

    return this.workspace.agent_mcp_access.find(
      (entry) => String(entry?.agent_instance_id || '').trim() === normalizedAgentInstanceId
    ) || null;
  }

  getWorkspaceMCPAgentNamesForBinding(bindingId) {
    const normalizedBindingId = String(bindingId || '').trim().toLowerCase();
    if (!normalizedBindingId || !this.workspace || !Array.isArray(this.workspace.agent_instances)) {
      return [];
    }

    const accessEntries = Array.isArray(this.workspace.agent_mcp_access)
      ? this.workspace.agent_mcp_access
      : [];

    const names = [];
    const seen = new Set();
    this.workspace.agent_instances.forEach((instance) => {
      const instanceId = String(instance?.id || '').trim();
      const agentName = String(instance?.name || '').trim();
      if (!instanceId || !agentName) return;

      const entry = accessEntries.find((item) => String(item?.agent_instance_id || '').trim() === instanceId);
      let allowed = true;
      if (entry) {
        const enabledIDs = Array.isArray(entry.enabled_binding_ids)
          ? entry.enabled_binding_ids.map((value) => String(value || '').trim().toLowerCase()).filter(Boolean)
          : [];
        allowed = enabledIDs.includes(normalizedBindingId);
      }
      if (!allowed) return;

      const key = this.normalizeAgentName(agentName);
      if (!key || seen.has(key)) return;
      seen.add(key);
      names.push(agentName);
    });
    return names;
  }

  getWorkspaceMCPAgentAccessSelections(bindingId) {
    if (!this.workspace || !Array.isArray(this.workspace.agent_instances)) {
      return [];
    }

    const normalizedBindingId = String(bindingId || '').trim().toLowerCase();
    return this.workspace.agent_instances
      .map((instance) => {
        const instanceId = String(instance?.id || '').trim();
        const instanceName = String(instance?.name || '').trim();
        if (!instanceId || !instanceName) return null;

        const entry = this.getWorkspaceAgentAccessEntry(instanceId);
        const enabledBindingIds = Array.isArray(entry?.enabled_binding_ids)
          ? entry.enabled_binding_ids.map((value) => String(value || '').trim().toLowerCase()).filter(Boolean)
          : [];

        const instanceNumber = Number(instance?.instance_number || 0);
        const nodeID = String(instance?.node_id || '').trim();
        const label = instanceNumber > 1 ? `${instanceName} #${instanceNumber}` : instanceName;
        const meta = nodeID || 'Workspace agent instance';
        const checked = entry
          ? enabledBindingIds.includes(normalizedBindingId)
          : true;

        return {
          id: instanceId,
          label,
          meta,
          checked
        };
      })
      .filter(Boolean);
  }

  summarizeWorkspaceMCPBindingScope(binding) {
    const serverName = String(binding?.serverName || '').trim().toLowerCase();
    const scope = binding?.scope && typeof binding.scope === 'object' ? binding.scope : {};

    if (serverName === 'filesystem') {
      const roots = Array.isArray(scope.roots)
        ? scope.roots.map((value) => String(value || '').trim()).filter(Boolean)
        : [];
      if (roots.length === 0) return 'No roots configured';
      if (roots.length === 1) return `1 root: ${roots[0]}`;
      return `${roots.length} roots`;
    }

    const entries = Object.entries(scope)
      .filter(([key, value]) => String(key || '').trim() && value !== null && value !== undefined);
    if (entries.length === 0) return '';

    const [firstKey, firstValue] = entries[0];
    if (Array.isArray(firstValue)) {
      return `${firstKey}: ${firstValue.length} item${firstValue.length === 1 ? '' : 's'}`;
    }
    if (typeof firstValue === 'object') {
      return `${firstKey}: configured`;
    }
    return `${firstKey}: ${String(firstValue).trim()}`;
  }

  summarizeWorkspaceMCPBindingConfig(binding) {
    const config = binding?.config && typeof binding.config === 'object' ? binding.config : {};
    const serverName = String(binding?.serverName || '').trim().toLowerCase();

    if (this.isEmailWorkspaceMCPServerName(serverName)) {
      const actions = Array.isArray(config.allowed_actions)
        ? config.allowed_actions.map((action) => String(action || '').trim()).filter(Boolean)
        : [];
      const mailboxes = Array.isArray(config.mailboxes)
        ? config.mailboxes.map((value) => String(value || '').trim()).filter(Boolean)
        : [];
      const parts = [];
      if (actions.length > 0) {
        parts.push(actions.join(', '));
      }
      if (mailboxes.length > 0) {
        parts.push(mailboxes.length === 1 ? `1 mailbox` : `${mailboxes.length} mailboxes`);
      }
      if (parts.length > 0) {
        return parts.join(' • ');
      }
    }

    const entries = Object.entries(config)
      .filter(([key, value]) => String(key || '').trim() && value !== null && value !== undefined);
    if (entries.length === 0) return '';

    const [firstKey, firstValue] = entries[0];
    if (Array.isArray(firstValue)) {
      return `${firstKey}: ${firstValue.length} value${firstValue.length === 1 ? '' : 's'}`;
    }
    if (typeof firstValue === 'object') {
      return `${firstKey}: configured`;
    }
    return `${firstKey}: ${String(firstValue).trim()}`;
  }

  describeWorkspaceMCPBinding(binding) {
    const serverName = String(binding?.serverName || '').trim().toLowerCase();
    if (binding?.enabled === false) {
      return 'Saved on this workspace but currently disabled. Re-enable it to materialize at runtime for agent instances.';
    }
    if (this.isEmailWorkspaceMCPServerName(serverName) && binding?.emailAccountMissing) {
      return 'Workspace-scoped email access is configured here, but the referenced vault account is currently unavailable or still locked.';
    }
    if (this.isEmailWorkspaceMCPServerName(serverName)) {
      return 'Workspace-scoped email access backed by a vault account. Policy on this binding limits which mailbox actions agents may perform.';
    }
    if (serverName === 'filesystem' && binding?.source === 'synthesized') {
      return 'Derived from imported workspace directories so filesystem access follows this workspace automatically.';
    }
    if (serverName === 'filesystem') {
      return 'Workspace-scoped filesystem access. Agents only see the roots allowed by this workspace binding.';
    }
    return 'Explicit workspace MCP binding available to agent instances in this workspace.';
  }

  getWorkspaceMCPModalInstance() {
    if (!this.elements.mcpModal || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      return null;
    }

    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.elements.mcpModal)
      : (bootstrap.Modal.getInstance(this.elements.mcpModal) || new bootstrap.Modal(this.elements.mcpModal));
  }

  generateWorkspaceMCPBindingId() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') {
      return window.crypto.randomUUID();
    }
    return `mcp-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  }

  slugifyWorkspaceMCPAlias(serverName) {
    return String(serverName || '')
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '_')
      .replace(/^_+|_+$/g, '');
  }

  setWorkspaceMCPServerHelp(message, isError = false) {
    if (!this.elements.mcpServerHelp) return;
    this.elements.mcpServerHelp.textContent = message;
    this.elements.mcpServerHelp.classList.toggle('is-error', !!isError);
  }

  populateWorkspaceMCPServerOptions(selectedServerName = '') {
    if (!this.elements.mcpServerSelect) return;

    const normalizedSelected = String(selectedServerName || '').trim();
    const availableServers = Array.isArray(this.availableMCPServers) ? [...this.availableMCPServers] : [];
    const selectedExists = normalizedSelected
      ? availableServers.some((server) => String(server?.name || '').trim().toLowerCase() === normalizedSelected.toLowerCase())
      : false;

    if (normalizedSelected && !selectedExists) {
      availableServers.unshift({ name: normalizedSelected, unavailable: true });
    }

    const options = ['<option value="">Select a connector</option>'];
    availableServers.forEach((server) => {
      const name = String(server?.name || '').trim();
      if (!name) return;

      const unavailable = server?.unavailable === true;
      const selected = normalizedSelected && name.toLowerCase() === normalizedSelected.toLowerCase() ? ' selected' : '';
      const label = unavailable ? `${name} (currently not globally enabled)` : name;
      options.push(`<option value="${this.escapeHtml(name)}"${selected}>${this.escapeHtml(label)}</option>`);
    });

    this.elements.mcpServerSelect.innerHTML = options.join('');

    if (normalizedSelected) {
      this.elements.mcpServerSelect.value = normalizedSelected;
    }

    if (availableServers.length === 0) {
      this.setWorkspaceMCPServerHelp('No globally enabled connectors are available yet. Enable an MCP globally first.', true);
      return;
    }

    if (normalizedSelected && !selectedExists) {
      this.setWorkspaceMCPServerHelp(`${normalizedSelected} is not globally enabled right now, but you can still update or remove this workspace binding.`, true);
      return;
    }

    this.setWorkspaceMCPServerHelp('Only globally enabled connectors can be added here.');
  }

  renderWorkspaceMCPAgentOptions(bindingId) {
    if (!this.elements.mcpAgentOptions) return;

    const accessOptions = this.getWorkspaceMCPAgentAccessSelections(bindingId);
    if (accessOptions.length === 0) {
      this.elements.mcpAgentOptions.innerHTML = `
        <div class="workspace-detail-mcp-agent-empty">
          Add one or more agents to this workspace before assigning MCP access.
        </div>
      `;
      this.updateWorkspaceMCPAgentAccessSummary();
      return;
    }

    this.elements.mcpAgentOptions.innerHTML = accessOptions.map((option) => `
      <label class="workspace-detail-mcp-agent-option">
        <input type="checkbox" class="form-check-input workspace-detail-mcp-agent-checkbox" value="${this.escapeHtml(option.id)}"${option.checked ? ' checked' : ''}>
        <span class="workspace-detail-mcp-agent-option-copy">
          <span class="workspace-detail-mcp-agent-option-title">${this.escapeHtml(option.label)}</span>
          <span class="workspace-detail-mcp-agent-option-meta">${this.escapeHtml(option.meta)}</span>
        </span>
      </label>
    `).join('');
    this.updateWorkspaceMCPAgentAccessSummary();
  }

  updateWorkspaceMCPAgentAccessSummary() {
    if (!this.elements.mcpAgentAccessSummary || !this.elements.mcpAgentOptions) return;

    const checkboxes = Array.from(this.elements.mcpAgentOptions.querySelectorAll('.workspace-detail-mcp-agent-checkbox'));
    if (checkboxes.length === 0) {
      this.elements.mcpAgentAccessSummary.textContent = 'No agents';
      return;
    }

    const selectedCount = checkboxes.filter((checkbox) => checkbox.checked).length;
    this.elements.mcpAgentAccessSummary.textContent = `${selectedCount} of ${checkboxes.length} selected`;
  }

  resetWorkspaceMCPModal() {
    this.activeWorkspaceMCPBindingId = '';
    this.activeWorkspaceMCPMode = 'create';

    if (this.elements.mcpForm) {
      this.elements.mcpForm.reset();
    }
    if (this.elements.mcpServerSelect) {
      this.elements.mcpServerSelect.innerHTML = '<option value="">Select a connector</option>';
    }
    if (this.elements.mcpAgentOptions) {
      this.elements.mcpAgentOptions.innerHTML = '<div class="workspace-detail-mcp-agent-empty">No agent instances in this workspace yet.</div>';
    }
    if (this.elements.mcpModalTitle) {
      this.elements.mcpModalTitle.textContent = 'Add Workspace MCP';
    }
    if (this.elements.mcpModalSubtitle) {
      this.elements.mcpModalSubtitle.textContent = 'Bind a globally available MCP connector to this workspace, then decide which agent instances can use it here.';
    }
    if (this.elements.mcpEnabledInput) {
      this.elements.mcpEnabledInput.checked = true;
    }
    if (this.elements.mcpScopeInput) {
      this.elements.mcpScopeInput.value = '';
    }
    if (this.elements.mcpConfigInput) {
      this.elements.mcpConfigInput.value = '';
    }
    if (this.elements.mcpConfigDetails) {
      this.elements.mcpConfigDetails.open = true;
    }
    if (this.elements.mcpAliasInput) {
      this.elements.mcpAliasInput.value = '';
    }
    if (this.elements.mcpEmailFields) {
      this.elements.mcpEmailFields.classList.add('d-none');
    }
    if (this.elements.mcpEmailAccountSelect) {
      this.elements.mcpEmailAccountSelect.innerHTML = '<option value="">Select an email account</option>';
      this.elements.mcpEmailAccountSelect.value = '';
    }
    if (this.elements.mcpEmailMailboxInput) {
      this.elements.mcpEmailMailboxInput.value = '';
    }
    this.setWorkspaceMCPEmailActions(['read', 'search']);
    if (this.elements.mcpEmailSendConfirmInput) {
      this.elements.mcpEmailSendConfirmInput.checked = true;
    }
    this.setWorkspaceMCPEmailAccountHelp('Email accounts come from Vault > Email Accounts.');
    this.updateWorkspaceMCPEmailAccountSummary();
    if (this.elements.mcpSubmitBtn) {
      this.elements.mcpSubmitBtn.disabled = false;
      this.elements.mcpSubmitBtn.textContent = 'Add Binding';
    }
    this.setWorkspaceMCPServerHelp('Only globally enabled connectors can be added here.');
    this.updateWorkspaceMCPAgentAccessSummary();
  }

  async handleWorkspaceMCPServerChange() {
    const serverName = String(this.elements.mcpServerSelect?.value || '').trim();
    if (!serverName) {
      await this.syncWorkspaceMCPEmailFields();
      return;
    }
    if (!this.elements.mcpAliasInput) return;

    if (!this.elements.mcpAliasInput.value.trim()) {
      this.elements.mcpAliasInput.value = this.slugifyWorkspaceMCPAlias(serverName);
    }

    await this.syncWorkspaceMCPEmailFields();
  }

  handleWorkspaceMCPListClick(event) {
    const button = event.target.closest('[data-workspace-mcp-action]');
    if (!button) return;
    event.preventDefault();
    event.stopPropagation();

    const action = String(button.dataset.workspaceMcpAction || '').trim();
    const bindingId = String(button.dataset.bindingId || '').trim();
    if (!bindingId) return;

    if (action === 'edit') {
      this.openWorkspaceMCPModal(bindingId);
      return;
    }

    if (action === 'delete') {
      this.deleteWorkspaceMCPBinding(bindingId);
    }
  }

  async openWorkspaceMCPModal(bindingId = '') {
    const explicitBinding = bindingId ? this.getWorkspaceExplicitMCPBinding(bindingId) : null;
    const existingBinding = explicitBinding || (bindingId ? this.getWorkspaceMCPBinding(bindingId, { includeDisabled: true }) : null);
    if (bindingId && !existingBinding) {
      if (window.Toast) {
        window.Toast.info('That workspace MCP binding is no longer available.');
      }
      return;
    }

    try {
      await this.loadAvailableMCPServers();
    } catch (error) {
      console.error('Failed to load MCP connectors:', error);
      if (!existingBinding) {
        if (window.Toast) window.Toast.error(error.message || 'Failed to load MCP connectors');
        return;
      }
    }

    const isSynthesized = existingBinding?.source === 'synthesized' && !explicitBinding;
    this.activeWorkspaceMCPMode = explicitBinding ? 'edit' : (isSynthesized ? 'customize' : 'create');
    this.activeWorkspaceMCPBindingId = existingBinding?.id || this.generateWorkspaceMCPBindingId();
    this.populateWorkspaceMCPServerOptions(existingBinding?.serverName || '');

    if (this.elements.mcpModalTitle) {
      this.elements.mcpModalTitle.textContent = explicitBinding
        ? 'Edit Workspace MCP'
        : (isSynthesized ? 'Customize Workspace MCP' : 'Add Workspace MCP');
    }
    if (this.elements.mcpModalSubtitle) {
      this.elements.mcpModalSubtitle.textContent = explicitBinding
        ? 'Update this workspace binding, refine its scope, or tighten which agent instances can reach it.'
        : (isSynthesized
          ? 'This binding is currently derived from imported directories. Saving here will create an explicit workspace binding that you can edit directly.'
          : 'Create a new MCP binding for this workspace and decide which agent instances should be able to use it.');
    }
    if (this.elements.mcpAliasInput) {
      this.elements.mcpAliasInput.value = existingBinding?.alias || '';
    }
    if (this.elements.mcpEnabledInput) {
      this.elements.mcpEnabledInput.checked = existingBinding ? existingBinding.enabled !== false : true;
    }
    if (this.elements.mcpScopeInput) {
      const scope = existingBinding?.scope && Object.keys(existingBinding.scope).length > 0
        ? JSON.stringify(existingBinding.scope, null, 2)
        : '';
      this.elements.mcpScopeInput.value = scope;
    }
    if (this.elements.mcpConfigInput) {
      const config = existingBinding?.config && Object.keys(existingBinding.config).length > 0
        ? JSON.stringify(existingBinding.config, null, 2)
        : '';
      this.elements.mcpConfigInput.value = config;
    }
    const emailConfig = existingBinding?.config && typeof existingBinding.config === 'object'
      ? existingBinding.config
      : {};
    if (this.elements.mcpEmailMailboxInput) {
      const mailboxes = Array.isArray(emailConfig.mailboxes)
        ? emailConfig.mailboxes.map((item) => String(item || '').trim()).filter(Boolean)
        : [];
      this.elements.mcpEmailMailboxInput.value = mailboxes.join(', ');
    }
    this.setWorkspaceMCPEmailActions(
      Array.isArray(emailConfig.allowed_actions) && emailConfig.allowed_actions.length > 0
        ? emailConfig.allowed_actions
        : ['read', 'search']
    );
    if (this.elements.mcpEmailSendConfirmInput) {
      this.elements.mcpEmailSendConfirmInput.checked = emailConfig.require_send_confirmation !== false;
    }
    if (this.elements.mcpSubmitBtn) {
      this.elements.mcpSubmitBtn.textContent = explicitBinding
        ? 'Save Changes'
        : (isSynthesized ? 'Customize Binding' : 'Add Binding');
      this.elements.mcpSubmitBtn.disabled = false;
    }

    await this.syncWorkspaceMCPEmailFields(existingBinding);
    this.renderWorkspaceMCPAgentOptions(this.activeWorkspaceMCPBindingId);
    this.getWorkspaceMCPModalInstance()?.show();
  }

  parseWorkspaceMCPScopeValue() {
    const raw = String(this.elements.mcpScopeInput?.value || '').trim();
    if (!raw) return {};

    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch (_error) {
      throw new Error('Scope JSON must be valid JSON');
    }

    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      throw new Error('Scope JSON must be an object');
    }

    return parsed;
  }

  parseWorkspaceMCPConfigValue() {
    const raw = String(this.elements.mcpConfigInput?.value || '').trim();
    if (!raw) return {};

    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch (_error) {
      throw new Error('Config JSON must be valid JSON');
    }

    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      throw new Error('Config JSON must be an object');
    }

    return parsed;
  }

  parseWorkspaceMCPEmailMailboxes() {
    return String(this.elements.mcpEmailMailboxInput?.value || '')
      .split(/[\n,]+/)
      .map((value) => String(value || '').trim())
      .filter(Boolean);
  }

  buildWorkspaceMCPEmailConfig(baseConfig = {}) {
    const config = baseConfig && typeof baseConfig === 'object' ? { ...baseConfig } : {};
    const accountID = String(this.elements.mcpEmailAccountSelect?.value || '').trim();
    const allowedActions = this.getWorkspaceMCPSelectedEmailActions();

    if (!accountID) {
      throw new Error('Choose an email account');
    }
    if (allowedActions.length === 0) {
      throw new Error('Select at least one email action');
    }

    config.account_id = accountID;
    delete config.account_vault_record_id;
    config.allowed_actions = allowedActions;

    const mailboxes = this.parseWorkspaceMCPEmailMailboxes();
    if (mailboxes.length > 0) {
      config.mailboxes = mailboxes;
    } else {
      delete config.mailboxes;
    }

    if (allowedActions.includes('send')) {
      config.require_send_confirmation = this.elements.mcpEmailSendConfirmInput?.checked !== false;
    } else {
      delete config.require_send_confirmation;
    }

    return config;
  }

  getWorkspaceMCPSelectedAgentInstanceIDs() {
    if (!this.elements.mcpAgentOptions) return [];
    return Array.from(this.elements.mcpAgentOptions.querySelectorAll('.workspace-detail-mcp-agent-checkbox:checked'))
      .map((checkbox) => String(checkbox.value || '').trim())
      .filter(Boolean);
  }

  setWorkspaceMCPModalSubmitting(isSubmitting) {
    if (!this.elements.mcpSubmitBtn) return;
    this.elements.mcpSubmitBtn.disabled = isSubmitting;
    this.elements.mcpSubmitBtn.textContent = isSubmitting
      ? (this.activeWorkspaceMCPMode === 'edit'
        ? 'Saving...'
        : (this.activeWorkspaceMCPMode === 'customize' ? 'Customizing...' : 'Adding...'))
      : (this.activeWorkspaceMCPMode === 'edit'
        ? 'Save Changes'
        : (this.activeWorkspaceMCPMode === 'customize' ? 'Customize Binding' : 'Add Binding'));
  }

  async saveWorkspaceMCPBinding(payload) {
    const isEditing = this.activeWorkspaceMCPMode === 'edit';
    const endpoint = isEditing
      ? `/api/workspaces/${encodeURIComponent(this.workspaceId)}/mcp-bindings/${encodeURIComponent(this.activeWorkspaceMCPBindingId)}`
      : `/api/workspaces/${encodeURIComponent(this.workspaceId)}/mcp-bindings`;

    const response = await fetch(endpoint, {
      method: isEditing ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to save workspace MCP binding');
    }

    return response.json();
  }

  async persistWorkspaceMCPAgentAccess(bindingId, selectedAgentInstanceIds) {
    if (!this.workspace || !Array.isArray(this.workspace.agent_instances) || this.workspace.agent_instances.length === 0) {
      return;
    }

    const selectedSet = new Set(selectedAgentInstanceIds.map((value) => String(value || '').trim()).filter(Boolean));
    const effectiveBindingIds = this.getWorkspaceMCPBindings()
      .map((binding) => String(binding?.id || '').trim())
      .filter(Boolean);
    const defaultBindingIds = Array.from(new Set(effectiveBindingIds)).sort();
    const normalizeIDs = (ids) => Array.from(new Set(ids.map((value) => String(value || '').trim()).filter(Boolean))).sort();
    const arraysEqual = (left, right) => (
      left.length === right.length && left.every((value, index) => value === right[index])
    );

    const requests = this.workspace.agent_instances.map(async (instance) => {
      const instanceId = String(instance?.id || '').trim();
      if (!instanceId) return;

      const entry = this.getWorkspaceAgentAccessEntry(instanceId);
      const currentIds = entry
        ? Array.isArray(entry.enabled_binding_ids)
          ? entry.enabled_binding_ids.map((value) => String(value || '').trim()).filter(Boolean)
          : []
        : [...defaultBindingIds];
      const allowedSet = new Set(currentIds);

      if (selectedSet.has(instanceId)) {
        allowedSet.add(bindingId);
      } else {
        allowedSet.delete(bindingId);
      }

      const enabledBindingIDs = normalizeIDs(Array.from(allowedSet));
      const currentNormalized = normalizeIDs(currentIds);

      if (!entry && arraysEqual(enabledBindingIDs, defaultBindingIds)) {
        return;
      }

      if (entry && arraysEqual(enabledBindingIDs, defaultBindingIds)) {
        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(this.workspaceId)}/agent-mcp-access/${encodeURIComponent(instanceId)}`,
          { method: 'DELETE' }
        );

        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || `Failed to clear MCP access rule for ${instanceId}`);
        }
        return;
      }

      if (arraysEqual(enabledBindingIDs, currentNormalized)) {
        return;
      }

      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/agent-mcp-access/${encodeURIComponent(instanceId)}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled_binding_ids: enabledBindingIDs })
        }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `Failed to update MCP access for ${instanceId}`);
      }
    });

    await Promise.all(requests);
  }

  async submitWorkspaceMCPModal() {
    const serverName = String(this.elements.mcpServerSelect?.value || '').trim();
    const alias = String(this.elements.mcpAliasInput?.value || '').trim();

    if (!serverName) {
      this.setWorkspaceMCPServerHelp('Choose a connector before saving this workspace binding.', true);
      if (window.Toast) window.Toast.error('Choose a connector');
      return;
    }

    let scope;
    try {
      scope = this.parseWorkspaceMCPScopeValue();
    } catch (error) {
      this.setWorkspaceMCPServerHelp(error.message || 'Scope JSON is invalid', true);
      if (window.Toast) window.Toast.error(error.message || 'Scope JSON is invalid');
      return;
    }

    let config;
    try {
      config = this.parseWorkspaceMCPConfigValue();
    } catch (error) {
      this.setWorkspaceMCPServerHelp(error.message || 'Config JSON is invalid', true);
      if (window.Toast) window.Toast.error(error.message || 'Config JSON is invalid');
      return;
    }

    if (this.isEmailWorkspaceMCPServerName(serverName)) {
      try {
        config = this.buildWorkspaceMCPEmailConfig(config);
        this.setWorkspaceMCPEmailAccountHelp('Email accounts come from Vault > Email Accounts. Workspace-scoped accounts must match this workspace.', false);
      } catch (error) {
        this.setWorkspaceMCPEmailAccountHelp(error.message || 'Email configuration is invalid', true);
        if (window.Toast) window.Toast.error(error.message || 'Email configuration is invalid');
        return;
      }
    }

    this.setWorkspaceMCPServerHelp('Only globally enabled connectors can be added here.');
    this.setWorkspaceMCPModalSubmitting(true);

    try {
      const enabled = this.elements.mcpEnabledInput?.checked !== false;
      const selectedAgentInstanceIds = this.getWorkspaceMCPSelectedAgentInstanceIDs();
      const payload = {
        server_name: serverName,
        alias,
        enabled,
        scope,
        config
      };

      if (this.activeWorkspaceMCPMode !== 'edit') {
        payload.id = this.activeWorkspaceMCPBindingId;
      }

      await this.saveWorkspaceMCPBinding(payload);
      await this.loadWorkspace();
      await this.persistWorkspaceMCPAgentAccess(this.activeWorkspaceMCPBindingId, selectedAgentInstanceIds);
      await this.loadWorkspace();

      this.getWorkspaceMCPModalInstance()?.hide();
      if (window.Toast) {
        window.Toast.success(
          this.activeWorkspaceMCPMode === 'edit'
            ? 'Workspace MCP updated'
            : (this.activeWorkspaceMCPMode === 'customize' ? 'Workspace MCP customized' : 'Workspace MCP added')
        );
      }
    } catch (error) {
      console.error('Failed to save workspace MCP binding:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to save workspace MCP binding');
    } finally {
      this.setWorkspaceMCPModalSubmitting(false);
    }
  }

  async deleteWorkspaceMCPBinding(bindingId) {
    const binding = this.getWorkspaceExplicitMCPBinding(bindingId);
    if (!binding) {
      if (window.Toast) {
        window.Toast.info('Synthesized bindings follow workspace directories and are removed by changing directory scope.');
      }
      return;
    }

    const label = binding.alias || binding.serverName || binding.id;
    if (!window.confirm(`Remove workspace MCP binding "${label}"?`)) {
      return;
    }

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/mcp-bindings/${encodeURIComponent(bindingId)}`,
        { method: 'DELETE' }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to remove workspace MCP binding');
      }

      await this.loadWorkspace();
      if (window.Toast) window.Toast.success('Workspace MCP removed');
    } catch (error) {
      console.error('Failed to delete workspace MCP binding:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to remove workspace MCP binding');
    }
  }

  renderWorkspaceMCPBindings() {
    if (!this.elements.mcpList) return;

    const bindings = this.getWorkspaceMCPBindings({ includeDisabled: true });
    if (bindings.length === 0) {
      this.elements.mcpList.innerHTML = `
        <div class="workspace-detail-empty">
          No workspace MCP bindings yet.
          <div class="workspace-detail-mcp-empty-note">Import directories to synthesize <code>filesystem</code>, or add an explicit binding with the <strong>+</strong> button.</div>
        </div>
      `;
      this.renderWorkspaceConfigSummary();
      return;
    }

    this.elements.mcpList.innerHTML = bindings.map((binding) => {
      const serverName = String(binding?.serverName || '').trim() || 'unknown';
      const emailServer = this.isEmailWorkspaceMCPServerName(serverName);
      const emailAccount = this.normalizeWorkspaceEmailAccount(binding?.emailAccount);
      const alias = String(binding?.alias || '').trim();
      const source = binding?.source === 'synthesized' ? 'Synthesized' : 'Explicit';
      const isDisabled = binding?.enabled === false;
      const scopeSummary = this.summarizeWorkspaceMCPBindingScope(binding);
      const configSummary = this.summarizeWorkspaceMCPBindingConfig(binding);
      const emailActions = Array.isArray(binding?.config?.allowed_actions)
        ? binding.config.allowed_actions.map((action) => String(action || '').trim()).filter(Boolean)
        : [];
      const agentNames = this.getWorkspaceMCPAgentNamesForBinding(binding.id);
      const accessSummary = isDisabled
        ? 'Disabled for this workspace'
        : agentNames.length > 0
        ? `${agentNames.length} agent${agentNames.length === 1 ? '' : 's'} can use this`
        : (Array.isArray(this.workspace?.agent_instances) && this.workspace.agent_instances.length > 0
          ? 'No agent instances currently have access'
          : 'No agent instances in this workspace');
      const accessLabel = isDisabled
        ? 'Agents: unavailable while disabled'
        : agentNames.length > 0
        ? `Agents: ${agentNames.join(', ')}`
        : 'Agents: none';
      const actions = binding?.source === 'workspace'
        ? `
          <div class="workspace-detail-mcp-card-actions">
            <button type="button" class="workspace-detail-mcp-card-btn" data-workspace-mcp-action="edit" data-binding-id="${this.escapeHtml(binding.id)}" onclick="event.preventDefault(); event.stopPropagation(); window.workspaceDetail?.openWorkspaceMCPModal('${this.escapeHtml(binding.id)}')" title="Edit binding" aria-label="Edit binding ${this.escapeHtml(alias || serverName)}">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
              </svg>
            </button>
            <button type="button" class="workspace-detail-mcp-card-btn is-danger" data-workspace-mcp-action="delete" data-binding-id="${this.escapeHtml(binding.id)}" onclick="event.preventDefault(); event.stopPropagation(); window.workspaceDetail?.deleteWorkspaceMCPBinding('${this.escapeHtml(binding.id)}')" title="Remove binding" aria-label="Remove binding ${this.escapeHtml(alias || serverName)}">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
              </svg>
            </button>
          </div>
        `
        : (binding?.source === 'synthesized'
          ? `
            <div class="workspace-detail-mcp-card-actions">
              <button type="button" class="workspace-detail-mcp-card-btn" data-workspace-mcp-action="edit" data-binding-id="${this.escapeHtml(binding.id)}" onclick="event.preventDefault(); event.stopPropagation(); window.workspaceDetail?.openWorkspaceMCPModal('${this.escapeHtml(binding.id)}')" title="Customize binding" aria-label="Customize binding ${this.escapeHtml(alias || serverName)}">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
                </svg>
              </button>
            </div>
          `
          : '')
        ;

      const chips = [
        `<span class="workspace-detail-mcp-chip source">${this.escapeHtml(source)}</span>`,
        `<span class="workspace-detail-mcp-chip status${isDisabled ? ' is-disabled' : ''}">${isDisabled ? 'Disabled' : 'Enabled'}</span>`,
        alias ? `<span class="workspace-detail-mcp-chip alias">Alias: ${this.escapeHtml(alias)}</span>` : '',
        emailServer && emailAccount?.provider ? `<span class="workspace-detail-mcp-chip provider">${this.escapeHtml(emailAccount.provider)}</span>` : '',
        emailServer && emailAccount?.emailAddress ? `<span class="workspace-detail-mcp-chip email">${this.escapeHtml(emailAccount.emailAddress)}</span>` : '',
        emailServer && emailActions.length > 0 ? `<span class="workspace-detail-mcp-chip policy">${this.escapeHtml(`Actions: ${emailActions.join(', ')}`)}</span>` : '',
        emailServer && binding?.config?.require_send_confirmation === true ? '<span class="workspace-detail-mcp-chip policy">Send confirm</span>' : '',
        emailServer && binding?.emailAccountMissing ? '<span class="workspace-detail-mcp-chip warning">Account unavailable</span>' : '',
        scopeSummary ? `<span class="workspace-detail-mcp-chip scope">${this.escapeHtml(scopeSummary)}</span>` : '',
        configSummary ? `<span class="workspace-detail-mcp-chip scope">Config: ${this.escapeHtml(configSummary)}</span>` : '',
        `<span class="workspace-detail-mcp-chip access">${this.escapeHtml(accessLabel)}</span>`
      ].filter(Boolean).join('');

      return `
        <div class="workspace-detail-mcp-card" data-binding-id="${this.escapeHtml(binding.id)}">
          <div class="workspace-detail-mcp-card-top">
            <div class="workspace-detail-mcp-card-top-main">
              <div class="workspace-detail-mcp-server">
                <span>${this.escapeHtml(serverName)}</span>
                <code>${this.escapeHtml(binding.id)}</code>
              </div>
              <div class="workspace-detail-mcp-meta">${this.escapeHtml(accessSummary)}</div>
            </div>
            ${actions}
          </div>
          <div class="workspace-detail-mcp-description">${this.escapeHtml(this.describeWorkspaceMCPBinding(binding))}</div>
          <div class="workspace-detail-mcp-chip-row">${chips}</div>
        </div>
      `;
    }).join('');
    this.renderWorkspaceConfigSummary();
  }

  // ── Workspace Configuration Methods ─────────────────────────────────

  getWorkspaceConfigStorageKey() {
    return `workspace-detail-config-expanded:${this.workspaceId}`;
  }

  readWorkspaceConfigExpandedPreference() {
    if (typeof window === 'undefined' || !window.localStorage) {
      return null;
    }

    try {
      const stored = window.localStorage.getItem(this.getWorkspaceConfigStorageKey());
      if (stored === 'true') return true;
      if (stored === 'false') return false;
    } catch (error) {
      console.warn('Failed to read workspace configuration preference:', error);
    }
    return null;
  }

  writeWorkspaceConfigExpandedPreference(expanded) {
    if (typeof window === 'undefined' || !window.localStorage) {
      return;
    }

    try {
      window.localStorage.setItem(this.getWorkspaceConfigStorageKey(), expanded ? 'true' : 'false');
    } catch (error) {
      console.warn('Failed to persist workspace configuration preference:', error);
    }
  }

  formatWorkspaceConfigPresetLabel(preset) {
    const value = String(preset || '').trim();
    if (!value) return 'Guided';
    return value
      .split('_')
      .filter(Boolean)
      .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
      .join(' ');
  }

  hasNonDefaultWorkspaceSettings() {
    const current = this.normalizeWorkspaceSettings(this.workspaceSettings || this.getDefaultWorkspaceSettings());
    const defaults = this.normalizeWorkspaceSettings(this.getDefaultWorkspaceSettings());
    return JSON.stringify({
      workflow: current.workflow,
      planning: current.planning
    }) !== JSON.stringify({
      workflow: defaults.workflow,
      planning: defaults.planning
    });
  }

  shouldDefaultExpandWorkspaceConfig() {
    const connectionCount = this.getWorkspaceMCPBindings({ includeDisabled: true }).length;
    const skillCount = this.getWorkspaceSkillBindings({ includeDisabled: true }).length;
    return connectionCount === 0 && skillCount === 0 && !this.hasNonDefaultWorkspaceSettings();
  }

  setWorkspaceConfigExpanded(expanded, options = {}) {
    const nextExpanded = expanded !== false;
    this.workspaceConfigExpanded = nextExpanded;

    if (this.elements.configPanel) {
      this.elements.configPanel.classList.toggle('is-collapsed', !nextExpanded);
    }
    if (this.elements.configContent) {
      this.elements.configContent.hidden = !nextExpanded;
    }
    if (this.elements.configToggleBtn) {
      this.elements.configToggleBtn.setAttribute('aria-expanded', nextExpanded ? 'true' : 'false');
    }
    if (this.elements.configToggleLabel) {
      this.elements.configToggleLabel.textContent = nextExpanded ? 'Hide Configuration' : 'Show Configuration';
    }

    if (options.persist !== false) {
      this.writeWorkspaceConfigExpandedPreference(nextExpanded);
    }
  }

  initializeWorkspaceConfigExpansion() {
    if (this.workspaceConfigPreferenceLoaded) {
      this.setWorkspaceConfigExpanded(this.workspaceConfigExpanded, { persist: false });
      return;
    }

    const storedPreference = this.readWorkspaceConfigExpandedPreference();
    const nextExpanded = storedPreference === null
      ? this.shouldDefaultExpandWorkspaceConfig()
      : storedPreference;

    this.workspaceConfigPreferenceLoaded = true;
    this.setWorkspaceConfigExpanded(nextExpanded, { persist: false });
  }

  toggleWorkspaceConfigExpanded() {
    this.setWorkspaceConfigExpanded(!this.workspaceConfigExpanded);
  }

  renderWorkspaceConfigSummary() {
    const settings = this.normalizeWorkspaceSettings(this.workspaceSettings || this.getDefaultWorkspaceSettings());
    const preset = this.deriveWorkspaceSettingsPreset(settings);
    const connectionCount = this.getWorkspaceMCPBindings({ includeDisabled: true }).length;
    const skillCount = this.getWorkspaceSkillBindings({ includeDisabled: true }).length;

    if (this.elements.configPresetChip) {
      this.elements.configPresetChip.textContent = `Preset: ${this.formatWorkspaceConfigPresetLabel(preset)}`;
    }
    if (this.elements.configConnectionsChip) {
      this.elements.configConnectionsChip.textContent = `Connections: ${connectionCount}`;
    }
    if (this.elements.configSkillsChip) {
      this.elements.configSkillsChip.textContent = `Skills: ${skillCount}`;
    }

    this.initializeWorkspaceConfigExpansion();
  }

  // ── Workspace Settings Methods ───────────────────────────────────────

  getWorkspaceSettingsPresets() {
    return ['minimal', 'guided', 'planner', 'autonomous'];
  }

  buildWorkspaceSettingsPreset(preset = 'guided') {
    const normalizedPreset = this.normalizeWorkspaceSettingsPreset(preset);
    const settings = {
      version: 1,
      preset: normalizedPreset,
      workflow: {
        mode: 'guided',
        require_repo_scan: false,
        save_outputs_as_notes: true,
        sync_plans_to_tasks: false,
        ask_before_specialist_handoff: true,
        confirmation_mode: 'destructive_only'
      },
      planning: {
        enabled: false,
        mode: 'feature',
        write_prd: true,
        write_task_list: true,
        tasks_dir: 'tasks',
        clarification_mode: 'standard',
        default_execution_mode: 'step_through',
        require_branch: true
      }
    };

    switch (normalizedPreset) {
      case 'minimal':
        settings.workflow.mode = 'direct';
        settings.workflow.save_outputs_as_notes = false;
        settings.workflow.ask_before_specialist_handoff = false;
        settings.planning.write_prd = false;
        settings.planning.write_task_list = false;
        break;
      case 'planner':
        settings.workflow.mode = 'plan_then_execute';
        settings.workflow.require_repo_scan = true;
        settings.workflow.sync_plans_to_tasks = true;
        settings.planning.enabled = true;
        break;
      case 'autonomous':
        settings.workflow.mode = 'plan_then_execute';
        settings.workflow.require_repo_scan = true;
        settings.workflow.sync_plans_to_tasks = true;
        settings.workflow.ask_before_specialist_handoff = false;
        settings.workflow.confirmation_mode = 'none';
        settings.planning.enabled = true;
        settings.planning.default_execution_mode = 'auto';
        break;
      default:
        break;
    }

    return settings;
  }

  getDefaultWorkspaceSettings() {
    return this.buildWorkspaceSettingsPreset('guided');
  }

  normalizeWorkspaceSettingsPreset(value) {
    const normalized = String(value || '').trim().toLowerCase();
    if (['minimal', 'guided', 'planner', 'autonomous', 'custom'].includes(normalized)) {
      return normalized;
    }
    return 'guided';
  }

  normalizeWorkspaceSettingsMode(value) {
    const normalized = String(value || '').trim().toLowerCase();
    if (['direct', 'guided', 'plan_then_execute'].includes(normalized)) {
      return normalized;
    }
    return 'guided';
  }

  normalizeWorkspaceSettingsConfirmationMode(value) {
    const normalized = String(value || '').trim().toLowerCase();
    if (['none', 'always', 'destructive_only'].includes(normalized)) {
      return normalized;
    }
    return 'destructive_only';
  }

  normalizeWorkspaceSettings(settings = {}) {
    const raw = settings && typeof settings === 'object' && !Array.isArray(settings) ? settings : {};
    const preset = this.normalizeWorkspaceSettingsPreset(raw.preset);
    const base = this.buildWorkspaceSettingsPreset(preset === 'custom' ? 'guided' : preset);
    const workflow = raw.workflow && typeof raw.workflow === 'object' && !Array.isArray(raw.workflow) ? raw.workflow : {};
    const planning = raw.planning && typeof raw.planning === 'object' && !Array.isArray(raw.planning) ? raw.planning : {};
    const boolOrDefault = (value, fallback) => (typeof value === 'boolean' ? value : fallback);

    return {
      version: Number.isFinite(Number(raw.version)) && Number(raw.version) > 0 ? Number(raw.version) : 1,
      preset,
      workflow: {
        mode: this.normalizeWorkspaceSettingsMode(workflow.mode || base.workflow.mode),
        require_repo_scan: boolOrDefault(workflow.require_repo_scan, base.workflow.require_repo_scan),
        save_outputs_as_notes: boolOrDefault(workflow.save_outputs_as_notes, base.workflow.save_outputs_as_notes),
        sync_plans_to_tasks: boolOrDefault(workflow.sync_plans_to_tasks, base.workflow.sync_plans_to_tasks),
        ask_before_specialist_handoff: boolOrDefault(workflow.ask_before_specialist_handoff, base.workflow.ask_before_specialist_handoff),
        confirmation_mode: this.normalizeWorkspaceSettingsConfirmationMode(workflow.confirmation_mode || base.workflow.confirmation_mode)
      },
      planning: {
        enabled: boolOrDefault(planning.enabled, base.planning.enabled),
        mode: this.normalizeWorkspacePlanningConfig({ mode: planning.mode || base.planning.mode }).mode,
        write_prd: boolOrDefault(planning.write_prd, base.planning.write_prd),
        write_task_list: boolOrDefault(planning.write_task_list, base.planning.write_task_list),
        tasks_dir: String(planning.tasks_dir || base.planning.tasks_dir || '').trim() || 'tasks',
        clarification_mode: this.normalizeWorkspacePlanningConfig({ clarification_mode: planning.clarification_mode || base.planning.clarification_mode }).clarification_mode,
        default_execution_mode: this.normalizeWorkspacePlanningConfig({ default_execution_mode: planning.default_execution_mode || base.planning.default_execution_mode }).default_execution_mode,
        require_branch: boolOrDefault(planning.require_branch, base.planning.require_branch)
      },
      updated_at: typeof raw.updated_at === 'string' ? raw.updated_at : ''
    };
  }

  deriveWorkspaceSettingsPreset(settings = {}) {
    const normalized = this.normalizeWorkspaceSettings(settings);
    const normalizedSignature = JSON.stringify({
      workflow: normalized.workflow,
      planning: normalized.planning
    });

    const matchingPreset = this.getWorkspaceSettingsPresets().find((preset) => {
      const candidate = this.buildWorkspaceSettingsPreset(preset);
      return JSON.stringify({
        workflow: candidate.workflow,
        planning: candidate.planning
      }) === normalizedSignature;
    });

    return matchingPreset || 'custom';
  }

  toWorkspacePlanningSkillConfig(settings = {}) {
    const normalized = this.normalizeWorkspaceSettings(settings);
    return {
      profile_type: 'workspace_planning',
      mode: normalized.planning.mode,
      write_prd: normalized.planning.write_prd,
      write_task_list: normalized.planning.write_task_list,
      tasks_dir: normalized.planning.tasks_dir,
      clarification_mode: normalized.planning.clarification_mode,
      sync_workspace_tasks: normalized.workflow.sync_plans_to_tasks,
      default_execution_mode: normalized.planning.default_execution_mode,
      require_branch: normalized.planning.require_branch
    };
  }

  buildWorkspaceSettingsEffectiveBehavior(settings = {}) {
    const normalized = this.normalizeWorkspaceSettings(settings);
    const effective = {
      workflow: { ...normalized.workflow },
      planning: { ...normalized.planning },
      summary: [
        `Interaction mode: ${normalized.workflow.mode}`,
        `Require repo scan before code work: ${normalized.workflow.require_repo_scan}`,
        `Save useful outputs as workspace notes: ${normalized.workflow.save_outputs_as_notes}`,
        `Sync approved plans to workspace tasks: ${normalized.workflow.sync_plans_to_tasks}`,
        `Ask before specialist handoff: ${normalized.workflow.ask_before_specialist_handoff}`,
        `Confirmation mode: ${normalized.workflow.confirmation_mode}`,
        `Planning profile enabled: ${normalized.planning.enabled}`
      ],
      managed_skills: []
    };

    if (normalized.planning.enabled) {
      effective.managed_skills = [
        {
          skill_name: 'workspace-planning',
          source: 'settings',
          active: true,
          reason: 'planning.enabled',
          config: this.toWorkspacePlanningSkillConfig(normalized)
        }
      ];
    }

    return effective;
  }

  normalizeWorkspaceSettingsEffectiveBehavior(effectiveBehavior, settings = {}) {
    const raw = effectiveBehavior && typeof effectiveBehavior === 'object' && !Array.isArray(effectiveBehavior)
      ? effectiveBehavior
      : null;
    const fallback = this.buildWorkspaceSettingsEffectiveBehavior(settings);
    if (!raw) {
      return fallback;
    }

    return {
      workflow: raw.workflow && typeof raw.workflow === 'object' ? { ...fallback.workflow, ...raw.workflow } : fallback.workflow,
      planning: raw.planning && typeof raw.planning === 'object' ? { ...fallback.planning, ...raw.planning } : fallback.planning,
      summary: Array.isArray(raw.summary) ? raw.summary.map((item) => String(item || '').trim()).filter(Boolean) : fallback.summary,
      managed_skills: Array.isArray(raw.managed_skills) ? raw.managed_skills : fallback.managed_skills
    };
  }

  populateWorkspaceSettingsForm(settings = {}) {
    const normalized = this.normalizeWorkspaceSettings(settings);

    if (this.elements.settingsPresetInput) {
      this.elements.settingsPresetInput.value = normalized.preset;
    }
    if (this.elements.settingsModeInput) {
      this.elements.settingsModeInput.value = normalized.workflow.mode;
    }
    if (this.elements.settingsConfirmationInput) {
      this.elements.settingsConfirmationInput.value = normalized.workflow.confirmation_mode;
    }
    if (this.elements.settingsPlanEnabledInput) {
      this.elements.settingsPlanEnabledInput.checked = normalized.planning.enabled === true;
    }
    if (this.elements.settingsRequireScanInput) {
      this.elements.settingsRequireScanInput.checked = normalized.workflow.require_repo_scan === true;
    }
    if (this.elements.settingsSaveNotesInput) {
      this.elements.settingsSaveNotesInput.checked = normalized.workflow.save_outputs_as_notes !== false;
    }
    if (this.elements.settingsSyncTasksInput) {
      this.elements.settingsSyncTasksInput.checked = normalized.workflow.sync_plans_to_tasks === true;
    }
    if (this.elements.settingsAskHandoffInput) {
      this.elements.settingsAskHandoffInput.checked = normalized.workflow.ask_before_specialist_handoff === true;
    }
    if (this.elements.settingsPlanningModeInput) {
      this.elements.settingsPlanningModeInput.value = normalized.planning.mode;
    }
    if (this.elements.settingsClarificationModeInput) {
      this.elements.settingsClarificationModeInput.value = normalized.planning.clarification_mode;
    }
    if (this.elements.settingsTasksDirInput) {
      this.elements.settingsTasksDirInput.value = normalized.planning.tasks_dir;
    }
    if (this.elements.settingsExecutionModeInput) {
      this.elements.settingsExecutionModeInput.value = normalized.planning.default_execution_mode;
    }
    if (this.elements.settingsWritePRDInput) {
      this.elements.settingsWritePRDInput.checked = normalized.planning.write_prd !== false;
    }
    if (this.elements.settingsWriteTaskListInput) {
      this.elements.settingsWriteTaskListInput.checked = normalized.planning.write_task_list !== false;
    }
    if (this.elements.settingsRequireBranchInput) {
      this.elements.settingsRequireBranchInput.checked = normalized.planning.require_branch !== false;
    }
    if (this.elements.settingsPlanningFields) {
      this.elements.settingsPlanningFields.classList.toggle('d-none', normalized.planning.enabled !== true);
    }
  }

  buildWorkspaceSettingsFromForm() {
    const settings = this.normalizeWorkspaceSettings({
      version: this.workspaceSettings?.version || 1,
      preset: String(this.elements.settingsPresetInput?.value || this.workspaceSettings?.preset || 'guided').trim(),
      workflow: {
        mode: String(this.elements.settingsModeInput?.value || '').trim(),
        require_repo_scan: this.elements.settingsRequireScanInput?.checked === true,
        save_outputs_as_notes: this.elements.settingsSaveNotesInput?.checked !== false,
        sync_plans_to_tasks: this.elements.settingsSyncTasksInput?.checked === true,
        ask_before_specialist_handoff: this.elements.settingsAskHandoffInput?.checked === true,
        confirmation_mode: String(this.elements.settingsConfirmationInput?.value || '').trim()
      },
      planning: {
        enabled: this.elements.settingsPlanEnabledInput?.checked === true,
        mode: String(this.elements.settingsPlanningModeInput?.value || '').trim(),
        write_prd: this.elements.settingsWritePRDInput?.checked !== false,
        write_task_list: this.elements.settingsWriteTaskListInput?.checked !== false,
        tasks_dir: String(this.elements.settingsTasksDirInput?.value || '').trim(),
        clarification_mode: String(this.elements.settingsClarificationModeInput?.value || '').trim(),
        default_execution_mode: String(this.elements.settingsExecutionModeInput?.value || '').trim(),
        require_branch: this.elements.settingsRequireBranchInput?.checked !== false
      }
    });

    settings.preset = this.deriveWorkspaceSettingsPreset(settings);
    return settings;
  }

  renderWorkspaceSettingsSummary(settings = {}, effectiveBehavior = null) {
    const normalized = this.normalizeWorkspaceSettings(settings);
    const effective = this.normalizeWorkspaceSettingsEffectiveBehavior(effectiveBehavior, normalized);

    if (this.elements.settingsSummary) {
      if (effective.summary.length === 0) {
        this.elements.settingsSummary.textContent = 'No effective behavior summary available.';
      } else {
        this.elements.settingsSummary.innerHTML = `
          <ul class="workspace-detail-settings-summary-list">
            ${effective.summary.map((item) => `<li>${this.escapeHtml(item)}</li>`).join('')}
          </ul>
        `;
      }
    }

    if (this.elements.settingsManagedSkills) {
      if (!Array.isArray(effective.managed_skills) || effective.managed_skills.length === 0) {
        this.elements.settingsManagedSkills.textContent = 'No settings-managed skills active.';
      } else {
        this.elements.settingsManagedSkills.innerHTML = effective.managed_skills.map((skill) => {
          const skillName = String(skill?.skill_name || skill?.skillName || '').trim() || 'unknown-skill';
          const source = String(skill?.source || '').trim() || 'settings';
          const reason = String(skill?.reason || '').trim();
          const planningSummary = skillName === 'workspace-planning'
            ? this.getWorkspacePlanningSummary(skill?.config || this.toWorkspacePlanningSkillConfig(normalized))
            : '';
          const detail = [source, reason, planningSummary].filter(Boolean).join(' • ');

          return `
            <div class="workspace-detail-settings-managed-entry">
              <div class="workspace-detail-settings-managed-name">${this.escapeHtml(skillName)}</div>
              <div class="workspace-detail-settings-managed-meta">${this.escapeHtml(detail || 'Managed by workspace settings')}</div>
            </div>
          `;
        }).join('');
      }
    }
  }

  renderWorkspaceSettings() {
    const settings = this.normalizeWorkspaceSettings(this.workspaceSettings || this.getDefaultWorkspaceSettings());
    const effective = this.normalizeWorkspaceSettingsEffectiveBehavior(this.workspaceSettingsEffectiveBehavior, settings);

    this.workspaceSettings = settings;
    this.workspaceSettingsEffectiveBehavior = effective;
    this.populateWorkspaceSettingsForm(settings);
    this.renderWorkspaceSettingsSummary(settings, effective);
    this.renderWorkspaceConfigSummary();
  }

  handleWorkspaceSettingsPresetChange() {
    const preset = this.normalizeWorkspaceSettingsPreset(this.elements.settingsPresetInput?.value || 'guided');
    if (preset === 'custom') {
      this.handleWorkspaceSettingsFieldChange();
      return;
    }

    this.workspaceSettings = this.buildWorkspaceSettingsPreset(preset);
    this.workspaceSettingsEffectiveBehavior = this.buildWorkspaceSettingsEffectiveBehavior(this.workspaceSettings);
    this.renderWorkspaceSettings();
  }

  handleWorkspaceSettingsFieldChange() {
    const settings = this.buildWorkspaceSettingsFromForm();
    this.workspaceSettings = settings;
    this.workspaceSettingsEffectiveBehavior = this.buildWorkspaceSettingsEffectiveBehavior(settings);
    this.renderWorkspaceSettings();
  }

  setWorkspaceSettingsSubmitting(isSubmitting) {
    if (!this.elements.settingsSaveBtn) return;
    this.elements.settingsSaveBtn.disabled = isSubmitting;
    this.elements.settingsSaveBtn.textContent = isSubmitting ? 'Saving...' : 'Save Settings';
  }

  async saveWorkspaceSettings() {
    const payload = this.buildWorkspaceSettingsFromForm();
    this.setWorkspaceSettingsSubmitting(true);

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/settings`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to save workspace settings');
      }

      const data = await response.json();
      this.workspaceSettings = this.normalizeWorkspaceSettings(data?.settings || payload);
      this.workspaceSettingsEffectiveBehavior = this.normalizeWorkspaceSettingsEffectiveBehavior(
        data?.effective_behavior,
        this.workspaceSettings
      );
      this.renderWorkspaceSettings();
      await this.loadWorkspace();

      if (window.Toast) {
        window.Toast.success('Workspace settings saved');
      }
    } catch (error) {
      console.error('Failed to save workspace settings:', error);
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to save workspace settings');
      }
    } finally {
      this.setWorkspaceSettingsSubmitting(false);
    }
  }

  // ── Workspace Skill Binding Methods ──────────────────────────────────

  getWorkspaceSkillBindings(options = {}) {
    if (!this.workspace || !Array.isArray(this.workspace.skill_bindings)) {
      return [];
    }

    const includeDisabled = options.includeDisabled === true;
    return this.workspace.skill_bindings
      .map((binding) => {
        const skillName = String(binding?.skill_name || binding?.skillName || '').trim();
        const config = binding?.config && typeof binding.config === 'object' ? { ...binding.config } : {};
        const skillDefinition = this.getAvailableWorkspaceSkill(skillName);
        return {
          id: String(binding?.id || '').trim(),
          skillName,
          enabled: binding?.enabled !== false,
          trusted: binding?.trusted === true,
          config,
          planningProfile: skillDefinition?.planningProfile === true || this.isWorkspacePlanningConfig(config)
        };
      })
      .filter((binding) => binding.id && binding.skillName)
      .filter((binding) => includeDisabled || binding.enabled);
  }

  getWorkspaceSkillBinding(bindingId) {
    const normalizedBindingId = String(bindingId || '').trim().toLowerCase();
    if (!normalizedBindingId) return null;
    return this.getWorkspaceSkillBindings({ includeDisabled: true }).find(
      (binding) => String(binding?.id || '').trim().toLowerCase() === normalizedBindingId
    ) || null;
  }

  getAvailableWorkspaceSkill(skillName) {
    const normalizedSkillName = String(skillName || '').trim().toLowerCase();
    if (!normalizedSkillName || !Array.isArray(this.availableSkills)) {
      return null;
    }
    return this.availableSkills.find(
      (skill) => String(skill?.name || '').trim().toLowerCase() === normalizedSkillName
    ) || null;
  }

  getDefaultWorkspacePlanningConfig() {
    return {
      profile_type: 'workspace_planning',
      mode: 'feature',
      write_prd: true,
      write_task_list: true,
      tasks_dir: 'tasks',
      clarification_mode: 'standard',
      sync_workspace_tasks: true,
      default_execution_mode: 'step_through',
      require_branch: true
    };
  }

  isWorkspacePlanningConfig(config) {
    if (!config || typeof config !== 'object' || Array.isArray(config)) {
      return false;
    }
    if (String(config.profile_type || '').trim() === 'workspace_planning') {
      return true;
    }
    const planningKeys = [
      'mode',
      'write_prd',
      'write_task_list',
      'tasks_dir',
      'clarification_mode',
      'sync_workspace_tasks',
      'default_execution_mode',
      'require_branch'
    ];
    return planningKeys.some((key) => Object.prototype.hasOwnProperty.call(config, key));
  }

  normalizeWorkspacePlanningConfig(config = {}) {
    const defaults = this.getDefaultWorkspacePlanningConfig();
    const normalized = {
      ...defaults,
      ...(config && typeof config === 'object' && !Array.isArray(config) ? config : {})
    };

    normalized.profile_type = 'workspace_planning';
    normalized.mode = ['feature', 'bugfix', 'refactor', 'investigation'].includes(String(normalized.mode || '').trim())
      ? String(normalized.mode).trim()
      : defaults.mode;
    normalized.tasks_dir = String(normalized.tasks_dir || '').trim() || defaults.tasks_dir;
    normalized.clarification_mode = ['minimal', 'standard', 'deep'].includes(String(normalized.clarification_mode || '').trim())
      ? String(normalized.clarification_mode).trim()
      : defaults.clarification_mode;
    normalized.default_execution_mode = ['auto', 'step_through'].includes(String(normalized.default_execution_mode || '').trim())
      ? String(normalized.default_execution_mode).trim()
      : defaults.default_execution_mode;
    normalized.write_prd = normalized.write_prd !== false;
    normalized.write_task_list = normalized.write_task_list !== false;
    normalized.sync_workspace_tasks = normalized.sync_workspace_tasks !== false;
    normalized.require_branch = normalized.require_branch !== false;
    return normalized;
  }

  getWorkspacePlanningSummary(config = {}) {
    if (!this.isWorkspacePlanningConfig(config)) return '';
    const normalized = this.normalizeWorkspacePlanningConfig(config);
    const outputs = [];
    if (normalized.write_prd) outputs.push('PRD');
    if (normalized.write_task_list) outputs.push('tasks');
    if (outputs.length === 0) outputs.push('no files');
    return `${normalized.mode} planning, ${outputs.join(' + ')}, ${normalized.default_execution_mode}, ${normalized.tasks_dir}`;
  }

  getWorkspaceSkillAgentAccessEntry(agentInstanceId) {
    const normalizedAgentInstanceId = String(agentInstanceId || '').trim();
    if (!normalizedAgentInstanceId || !this.workspace || !Array.isArray(this.workspace.agent_skill_access)) {
      return null;
    }

    return this.workspace.agent_skill_access.find(
      (entry) => String(entry?.agent_instance_id || '').trim() === normalizedAgentInstanceId
    ) || null;
  }

  getWorkspaceSkillAgentNamesForBinding(bindingId) {
    const normalizedBindingId = String(bindingId || '').trim().toLowerCase();
    if (!normalizedBindingId || !this.workspace || !Array.isArray(this.workspace.agent_instances)) {
      return [];
    }

    const accessEntries = Array.isArray(this.workspace.agent_skill_access)
      ? this.workspace.agent_skill_access
      : [];

    const names = [];
    const seen = new Set();
    this.workspace.agent_instances.forEach((instance) => {
      const instanceId = String(instance?.id || '').trim();
      const agentName = String(instance?.name || '').trim();
      if (!instanceId || !agentName) return;

      const entry = accessEntries.find((item) => String(item?.agent_instance_id || '').trim() === instanceId);
      let allowed = true;
      if (entry) {
        const enabledIDs = Array.isArray(entry.enabled_binding_ids)
          ? entry.enabled_binding_ids.map((value) => String(value || '').trim().toLowerCase()).filter(Boolean)
          : [];
        allowed = enabledIDs.includes(normalizedBindingId);
      }
      if (!allowed) return;

      const key = this.normalizeAgentName(agentName);
      if (!key || seen.has(key)) return;
      seen.add(key);
      names.push(agentName);
    });
    return names;
  }

  getWorkspaceSkillAgentAccessSelections(bindingId) {
    if (!this.workspace || !Array.isArray(this.workspace.agent_instances)) {
      return [];
    }

    const normalizedBindingId = String(bindingId || '').trim().toLowerCase();
    return this.workspace.agent_instances
      .map((instance) => {
        const instanceId = String(instance?.id || '').trim();
        const instanceName = String(instance?.name || '').trim();
        if (!instanceId || !instanceName) return null;

        const entry = this.getWorkspaceSkillAgentAccessEntry(instanceId);
        const enabledBindingIds = Array.isArray(entry?.enabled_binding_ids)
          ? entry.enabled_binding_ids.map((value) => String(value || '').trim().toLowerCase()).filter(Boolean)
          : [];

        const instanceNumber = Number(instance?.instance_number || 0);
        const nodeID = String(instance?.node_id || '').trim();
        const label = instanceNumber > 1 ? `${instanceName} #${instanceNumber}` : instanceName;
        const meta = nodeID || 'Workspace agent instance';
        const checked = entry
          ? enabledBindingIds.includes(normalizedBindingId)
          : true;

        return {
          id: instanceId,
          label,
          meta,
          checked
        };
      })
      .filter(Boolean);
  }

  async loadAvailableSkills(force = false) {
    if (!force && Array.isArray(this.availableSkills) && this.availableSkills.length > 0) {
      return this.availableSkills;
    }
    if (!force && this.availableSkillsPromise) {
      return this.availableSkillsPromise;
    }

    this.availableSkillsPromise = (async () => {
      const response = await fetch('/api/skills?agent=default');
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to load skills');
      }

      const data = await response.json();
      const seen = new Set();
      const skillsList = (Array.isArray(data?.skills) ? data.skills : [])
        .map((skill) => ({
          name: String(skill?.name || '').trim(),
          description: String(skill?.description || '').trim(),
          enabled: skill?.enabled !== false,
          planningProfile: skill?.planning_profile === true || skill?.openai_metadata?.planning_profile === true
        }))
        .filter((skill) => skill.name)
        .filter((skill) => {
          const key = skill.name.toLowerCase();
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })
        .sort((left, right) => left.name.localeCompare(right.name));

      this.availableSkills = skillsList;
      return skillsList;
    })();

    try {
      return await this.availableSkillsPromise;
    } finally {
      this.availableSkillsPromise = null;
    }
  }

  getWorkspaceSkillModalInstance() {
    if (!this.elements.skillsModal || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      return null;
    }

    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.elements.skillsModal)
      : (bootstrap.Modal.getInstance(this.elements.skillsModal) || new bootstrap.Modal(this.elements.skillsModal));
  }

  generateWorkspaceSkillBindingId() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') {
      return window.crypto.randomUUID();
    }
    return `skill-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  }

  setWorkspaceSkillNameHelp(message, isError = false) {
    if (!this.elements.skillNameHelp) return;
    this.elements.skillNameHelp.textContent = message;
    this.elements.skillNameHelp.classList.toggle('is-error', !!isError);
  }

  populateWorkspaceSkillOptions(selectedSkillName = '') {
    if (!this.elements.skillNameSelect) return;

    const normalizedSelected = String(selectedSkillName || '').trim();
    const available = Array.isArray(this.availableSkills) ? [...this.availableSkills] : [];
    const selectedExists = normalizedSelected
      ? available.some((skill) => String(skill?.name || '').trim().toLowerCase() === normalizedSelected.toLowerCase())
      : false;

    if (normalizedSelected && !selectedExists) {
      available.unshift({ name: normalizedSelected, unavailable: true });
    }

    const options = ['<option value="">Select a skill</option>'];
    available.forEach((skill) => {
      const name = String(skill?.name || '').trim();
      if (!name) return;

      const unavailable = skill?.unavailable === true;
      const selected = normalizedSelected && name.toLowerCase() === normalizedSelected.toLowerCase() ? ' selected' : '';
      const planning = skill?.planningProfile === true;
      const label = unavailable
        ? `${name} (not currently available)`
        : (planning ? `${name} (planning profile)` : name);
      options.push(`<option value="${this.escapeHtml(name)}"${selected}>${this.escapeHtml(label)}</option>`);
    });

    this.elements.skillNameSelect.innerHTML = options.join('');

    if (normalizedSelected) {
      this.elements.skillNameSelect.value = normalizedSelected;
    }

    if (available.length === 0) {
      this.setWorkspaceSkillNameHelp('No skills are available yet. Create or install skills first.', true);
      return;
    }

    if (normalizedSelected && !selectedExists) {
      this.setWorkspaceSkillNameHelp(`${normalizedSelected} is not currently available, but you can still update or remove this workspace binding.`, true);
      return;
    }

    this.setWorkspaceSkillNameHelp('Choose a skill to bind to this workspace.');
  }

  renderWorkspaceSkillAgentOptions(bindingId) {
    if (!this.elements.skillAgentOptions) return;

    const accessOptions = this.getWorkspaceSkillAgentAccessSelections(bindingId);
    if (accessOptions.length === 0) {
      this.elements.skillAgentOptions.innerHTML = `
        <div class="workspace-detail-mcp-agent-empty">
          Add one or more agents to this workspace before assigning skill access.
        </div>
      `;
      this.updateWorkspaceSkillAgentAccessSummary();
      return;
    }

    this.elements.skillAgentOptions.innerHTML = accessOptions.map((option) => `
      <label class="workspace-detail-mcp-agent-option">
        <input type="checkbox" class="form-check-input workspace-detail-skill-agent-checkbox" value="${this.escapeHtml(option.id)}"${option.checked ? ' checked' : ''}>
        <span class="workspace-detail-mcp-agent-option-copy">
          <span class="workspace-detail-mcp-agent-option-title">${this.escapeHtml(option.label)}</span>
          <span class="workspace-detail-mcp-agent-option-meta">${this.escapeHtml(option.meta)}</span>
        </span>
      </label>
    `).join('');
    this.updateWorkspaceSkillAgentAccessSummary();
  }

  updateWorkspaceSkillAgentAccessSummary() {
    if (!this.elements.skillAgentAccessSummary || !this.elements.skillAgentOptions) return;

    const checkboxes = Array.from(this.elements.skillAgentOptions.querySelectorAll('.workspace-detail-skill-agent-checkbox'));
    if (checkboxes.length === 0) {
      this.elements.skillAgentAccessSummary.textContent = 'No agents';
      return;
    }

    const selectedCount = checkboxes.filter((checkbox) => checkbox.checked).length;
    this.elements.skillAgentAccessSummary.textContent = `${selectedCount} of ${checkboxes.length} selected`;
  }

  populateWorkspacePlanningSettings(config = {}) {
    const normalized = this.normalizeWorkspacePlanningConfig(config);
    if (this.elements.skillPlanningModeInput) {
      this.elements.skillPlanningModeInput.value = normalized.mode;
    }
    if (this.elements.skillPlanningClarificationModeInput) {
      this.elements.skillPlanningClarificationModeInput.value = normalized.clarification_mode;
    }
    if (this.elements.skillPlanningTasksDirInput) {
      this.elements.skillPlanningTasksDirInput.value = normalized.tasks_dir;
    }
    if (this.elements.skillPlanningDefaultExecutionInput) {
      this.elements.skillPlanningDefaultExecutionInput.value = normalized.default_execution_mode;
    }
    if (this.elements.skillPlanningWritePRDInput) {
      this.elements.skillPlanningWritePRDInput.checked = normalized.write_prd !== false;
    }
    if (this.elements.skillPlanningWriteTaskListInput) {
      this.elements.skillPlanningWriteTaskListInput.checked = normalized.write_task_list !== false;
    }
    if (this.elements.skillPlanningSyncTasksInput) {
      this.elements.skillPlanningSyncTasksInput.checked = normalized.sync_workspace_tasks !== false;
    }
    if (this.elements.skillPlanningRequireBranchInput) {
      this.elements.skillPlanningRequireBranchInput.checked = normalized.require_branch !== false;
    }
  }

  shouldShowWorkspacePlanningSettings(selectedSkillName = '', existingConfig = {}) {
    const normalizedSkillName = String(selectedSkillName || '').trim();
    if (normalizedSkillName) {
      const selectedSkill = this.getAvailableWorkspaceSkill(normalizedSkillName);
      if (selectedSkill) {
        return selectedSkill.planningProfile === true;
      }
    }
    return this.isWorkspacePlanningConfig(existingConfig);
  }

  handleWorkspaceSkillSelectionChange() {
    const selectedSkillName = String(this.elements.skillNameSelect?.value || '').trim();
    const existingBinding = this.activeWorkspaceSkillBindingId
      ? this.getWorkspaceSkillBinding(this.activeWorkspaceSkillBindingId)
      : null;
    const shouldShowPlanning = this.shouldShowWorkspacePlanningSettings(selectedSkillName, existingBinding?.config || {});

    if (this.elements.skillPlanningFields) {
      this.elements.skillPlanningFields.classList.toggle('d-none', !shouldShowPlanning);
    }

    if (shouldShowPlanning) {
      const sourceConfig = this.isWorkspacePlanningConfig(existingBinding?.config)
        ? existingBinding.config
        : this.getDefaultWorkspacePlanningConfig();
      this.populateWorkspacePlanningSettings(sourceConfig);
    }
  }

  buildWorkspacePlanningConfig() {
    return this.normalizeWorkspacePlanningConfig({
      profile_type: 'workspace_planning',
      mode: String(this.elements.skillPlanningModeInput?.value || '').trim(),
      write_prd: this.elements.skillPlanningWritePRDInput?.checked !== false,
      write_task_list: this.elements.skillPlanningWriteTaskListInput?.checked !== false,
      tasks_dir: String(this.elements.skillPlanningTasksDirInput?.value || '').trim(),
      clarification_mode: String(this.elements.skillPlanningClarificationModeInput?.value || '').trim(),
      sync_workspace_tasks: this.elements.skillPlanningSyncTasksInput?.checked !== false,
      default_execution_mode: String(this.elements.skillPlanningDefaultExecutionInput?.value || '').trim(),
      require_branch: this.elements.skillPlanningRequireBranchInput?.checked !== false
    });
  }

  resetWorkspaceSkillModal() {
    this.activeWorkspaceSkillBindingId = '';
    this.activeWorkspaceSkillMode = 'create';

    if (this.elements.skillsForm) {
      this.elements.skillsForm.reset();
    }
    if (this.elements.skillNameSelect) {
      this.elements.skillNameSelect.innerHTML = '<option value="">Select a skill</option>';
    }
    if (this.elements.skillAgentOptions) {
      this.elements.skillAgentOptions.innerHTML = '<div class="workspace-detail-mcp-agent-empty">No agent instances in this workspace yet.</div>';
    }
    if (this.elements.skillsModalTitle) {
      this.elements.skillsModalTitle.textContent = 'Add Workspace Skill';
    }
    if (this.elements.skillsModalSubtitle) {
      this.elements.skillsModalSubtitle.textContent = 'Bind a skill to this workspace, then decide which agent instances can use it here.';
    }
    if (this.elements.skillEnabledInput) {
      this.elements.skillEnabledInput.checked = true;
    }
    if (this.elements.skillTrustedInput) {
      this.elements.skillTrustedInput.checked = false;
    }
    this.populateWorkspacePlanningSettings(this.getDefaultWorkspacePlanningConfig());
    if (this.elements.skillPlanningFields) {
      this.elements.skillPlanningFields.classList.add('d-none');
    }
    if (this.elements.skillSubmitBtn) {
      this.elements.skillSubmitBtn.disabled = false;
      this.elements.skillSubmitBtn.textContent = 'Add Binding';
    }
    this.setWorkspaceSkillNameHelp('Choose a skill to bind to this workspace.');
    this.updateWorkspaceSkillAgentAccessSummary();
  }

  handleWorkspaceSkillListClick(event) {
    const button = event.target.closest('[data-workspace-skill-action]');
    if (!button) return;
    event.preventDefault();
    event.stopPropagation();

    const action = String(button.dataset.workspaceSkillAction || '').trim();
    const bindingId = String(button.dataset.bindingId || '').trim();
    if (!bindingId) return;

    if (action === 'edit') {
      this.openWorkspaceSkillModal(bindingId);
      return;
    }

    if (action === 'delete') {
      this.deleteWorkspaceSkillBinding(bindingId);
    }
  }

  async openWorkspaceSkillModal(bindingId = '') {
    const existingBinding = bindingId ? this.getWorkspaceSkillBinding(bindingId) : null;
    if (bindingId && !existingBinding) {
      if (window.Toast) {
        window.Toast.info('That workspace skill binding is no longer available.');
      }
      return;
    }

    try {
      await this.loadAvailableSkills();
    } catch (error) {
      console.error('Failed to load skills:', error);
      if (!existingBinding) {
        if (window.Toast) window.Toast.error(error.message || 'Failed to load skills');
        return;
      }
    }

    this.activeWorkspaceSkillMode = existingBinding ? 'edit' : 'create';
    this.activeWorkspaceSkillBindingId = existingBinding?.id || this.generateWorkspaceSkillBindingId();
    this.populateWorkspaceSkillOptions(existingBinding?.skillName || '');

    if (this.elements.skillsModalTitle) {
      this.elements.skillsModalTitle.textContent = existingBinding
        ? 'Edit Workspace Skill'
        : 'Add Workspace Skill';
    }
    if (this.elements.skillsModalSubtitle) {
      this.elements.skillsModalSubtitle.textContent = existingBinding
        ? 'Update this workspace skill binding or change which agent instances can use it.'
        : 'Bind a skill to this workspace and decide which agent instances should be able to use it.';
    }
    if (this.elements.skillEnabledInput) {
      this.elements.skillEnabledInput.checked = existingBinding ? existingBinding.enabled !== false : true;
    }
    if (this.elements.skillTrustedInput) {
      this.elements.skillTrustedInput.checked = existingBinding ? existingBinding.trusted === true : false;
    }
    this.populateWorkspacePlanningSettings(existingBinding?.config || this.getDefaultWorkspacePlanningConfig());
    if (this.elements.skillSubmitBtn) {
      this.elements.skillSubmitBtn.textContent = existingBinding ? 'Save Changes' : 'Add Binding';
      this.elements.skillSubmitBtn.disabled = false;
    }

    this.handleWorkspaceSkillSelectionChange();
    this.renderWorkspaceSkillAgentOptions(this.activeWorkspaceSkillBindingId);
    this.getWorkspaceSkillModalInstance()?.show();
  }

  getWorkspaceSkillSelectedAgentInstanceIDs() {
    if (!this.elements.skillAgentOptions) return [];
    return Array.from(this.elements.skillAgentOptions.querySelectorAll('.workspace-detail-skill-agent-checkbox:checked'))
      .map((checkbox) => String(checkbox.value || '').trim())
      .filter(Boolean);
  }

  setWorkspaceSkillModalSubmitting(isSubmitting) {
    if (!this.elements.skillSubmitBtn) return;
    this.elements.skillSubmitBtn.disabled = isSubmitting;
    this.elements.skillSubmitBtn.textContent = isSubmitting
      ? (this.activeWorkspaceSkillMode === 'edit' ? 'Saving...' : 'Adding...')
      : (this.activeWorkspaceSkillMode === 'edit' ? 'Save Changes' : 'Add Binding');
  }

  async saveWorkspaceSkillBinding(payload) {
    const isEditing = this.activeWorkspaceSkillMode === 'edit';
    const endpoint = isEditing
      ? `/api/workspaces/${encodeURIComponent(this.workspaceId)}/skill-bindings/${encodeURIComponent(this.activeWorkspaceSkillBindingId)}`
      : `/api/workspaces/${encodeURIComponent(this.workspaceId)}/skill-bindings`;

    const response = await fetch(endpoint, {
      method: isEditing ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to save workspace skill binding');
    }

    return response.json();
  }

  async persistWorkspaceSkillAgentAccess(bindingId, selectedAgentInstanceIds) {
    if (!this.workspace || !Array.isArray(this.workspace.agent_instances) || this.workspace.agent_instances.length === 0) {
      return;
    }

    const selectedSet = new Set(selectedAgentInstanceIds.map((value) => String(value || '').trim()).filter(Boolean));
    const effectiveBindingIds = this.getWorkspaceSkillBindings()
      .map((binding) => String(binding?.id || '').trim())
      .filter(Boolean);
    const defaultBindingIds = Array.from(new Set(effectiveBindingIds)).sort();
    const normalizeIDs = (ids) => Array.from(new Set(ids.map((value) => String(value || '').trim()).filter(Boolean))).sort();
    const arraysEqual = (left, right) => (
      left.length === right.length && left.every((value, index) => value === right[index])
    );

    const requests = this.workspace.agent_instances.map(async (instance) => {
      const instanceId = String(instance?.id || '').trim();
      if (!instanceId) return;

      const entry = this.getWorkspaceSkillAgentAccessEntry(instanceId);
      const currentIds = entry
        ? Array.isArray(entry.enabled_binding_ids)
          ? entry.enabled_binding_ids.map((value) => String(value || '').trim()).filter(Boolean)
          : []
        : [...defaultBindingIds];
      const allowedSet = new Set(currentIds);

      if (selectedSet.has(instanceId)) {
        allowedSet.add(bindingId);
      } else {
        allowedSet.delete(bindingId);
      }

      const enabledBindingIDs = normalizeIDs(Array.from(allowedSet));
      const currentNormalized = normalizeIDs(currentIds);

      if (!entry && arraysEqual(enabledBindingIDs, defaultBindingIds)) {
        return;
      }

      if (entry && arraysEqual(enabledBindingIDs, defaultBindingIds)) {
        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(this.workspaceId)}/agent-skill-access/${encodeURIComponent(instanceId)}`,
          { method: 'DELETE' }
        );

        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || `Failed to clear skill access rule for ${instanceId}`);
        }
        return;
      }

      if (arraysEqual(enabledBindingIDs, currentNormalized)) {
        return;
      }

      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/agent-skill-access/${encodeURIComponent(instanceId)}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled_binding_ids: enabledBindingIDs })
        }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `Failed to update skill access for ${instanceId}`);
      }
    });

    await Promise.all(requests);
  }

  async submitWorkspaceSkillModal() {
    const skillName = String(this.elements.skillNameSelect?.value || '').trim();

    if (!skillName) {
      this.setWorkspaceSkillNameHelp('Choose a skill before saving this workspace binding.', true);
      if (window.Toast) window.Toast.error('Choose a skill');
      return;
    }

    this.setWorkspaceSkillNameHelp('Choose a skill to bind to this workspace.');
    this.setWorkspaceSkillModalSubmitting(true);

    try {
      const selectedSkill = this.getAvailableWorkspaceSkill(skillName);
      const planningProfile = selectedSkill?.planningProfile === true || this.shouldShowWorkspacePlanningSettings(skillName);
      const enabled = this.elements.skillEnabledInput?.checked !== false;
      const trusted = this.elements.skillTrustedInput?.checked === true;
      const selectedAgentInstanceIds = this.getWorkspaceSkillSelectedAgentInstanceIDs();
      const payload = {
        skill_name: skillName,
        enabled,
        trusted,
        config: planningProfile ? this.buildWorkspacePlanningConfig() : {}
      };

      if (this.activeWorkspaceSkillMode !== 'edit') {
        payload.id = this.activeWorkspaceSkillBindingId;
      }

      await this.saveWorkspaceSkillBinding(payload);
      await this.loadWorkspace();
      await this.persistWorkspaceSkillAgentAccess(this.activeWorkspaceSkillBindingId, selectedAgentInstanceIds);
      await this.loadWorkspace();

      this.getWorkspaceSkillModalInstance()?.hide();
      if (window.Toast) {
        window.Toast.success(
          this.activeWorkspaceSkillMode === 'edit'
            ? 'Workspace skill updated'
            : 'Workspace skill added'
        );
      }
    } catch (error) {
      console.error('Failed to save workspace skill binding:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to save workspace skill binding');
    } finally {
      this.setWorkspaceSkillModalSubmitting(false);
    }
  }

  async deleteWorkspaceSkillBinding(bindingId) {
    const binding = this.getWorkspaceSkillBinding(bindingId);
    if (!binding) {
      if (window.Toast) {
        window.Toast.info('That skill binding was not found.');
      }
      return;
    }

    const label = binding.skillName || binding.id;
    if (!window.confirm(`Remove workspace skill binding "${label}"?`)) {
      return;
    }

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/skill-bindings/${encodeURIComponent(bindingId)}`,
        { method: 'DELETE' }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to remove workspace skill binding');
      }

      await this.loadWorkspace();
      if (window.Toast) window.Toast.success('Workspace skill removed');
    } catch (error) {
      console.error('Failed to delete workspace skill binding:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to remove workspace skill binding');
    }
  }

  renderWorkspaceSkillBindings() {
    if (!this.elements.skillsList) return;

    const bindings = this.getWorkspaceSkillBindings({ includeDisabled: true });
    if (bindings.length === 0) {
      this.elements.skillsList.innerHTML = `
        <div class="workspace-detail-empty">
          No workspace skill bindings yet.
          <div class="workspace-detail-mcp-empty-note">Add a skill with the <strong>+</strong> button to make it available to agents in this workspace.</div>
        </div>
      `;
      this.renderWorkspaceConfigSummary();
      return;
    }

    this.elements.skillsList.innerHTML = bindings.map((binding) => {
      const skillName = String(binding?.skillName || '').trim() || 'unknown';
      const isDisabled = binding?.enabled === false;
      const isTrusted = binding?.trusted === true;
      const isPlanning = binding?.planningProfile === true;
      const agentNames = this.getWorkspaceSkillAgentNamesForBinding(binding.id);
      const planningSummary = this.getWorkspacePlanningSummary(binding?.config || {});
      const accessSummary = isDisabled
        ? 'Disabled for this workspace'
        : agentNames.length > 0
        ? `${agentNames.length} agent${agentNames.length === 1 ? '' : 's'} can use this`
        : (Array.isArray(this.workspace?.agent_instances) && this.workspace.agent_instances.length > 0
          ? 'No agent instances currently have access'
          : 'No agent instances in this workspace');
      const accessLabel = isDisabled
        ? 'Agents: unavailable while disabled'
        : agentNames.length > 0
        ? `Agents: ${agentNames.join(', ')}`
        : 'Agents: none';

      const chips = [
        `<span class="workspace-detail-mcp-chip status${isDisabled ? ' is-disabled' : ''}">${isDisabled ? 'Disabled' : 'Enabled'}</span>`,
        isPlanning ? `<span class="workspace-detail-mcp-chip source">Planning</span>` : '',
        isTrusted ? `<span class="workspace-detail-mcp-chip source">Trusted</span>` : '',
        planningSummary ? `<span class="workspace-detail-mcp-chip source">${this.escapeHtml(planningSummary)}</span>` : '',
        `<span class="workspace-detail-mcp-chip access">${this.escapeHtml(accessLabel)}</span>`
      ].filter(Boolean).join('');

      return `
        <div class="workspace-detail-mcp-card" data-skill-binding-id="${this.escapeHtml(binding.id)}">
          <div class="workspace-detail-mcp-card-top">
            <div class="workspace-detail-mcp-card-top-main">
              <div class="workspace-detail-mcp-server">
                <span>${this.escapeHtml(skillName)}</span>
                <code>${this.escapeHtml(binding.id)}</code>
              </div>
              <div class="workspace-detail-mcp-meta">${this.escapeHtml(accessSummary)}</div>
            </div>
            <div class="workspace-detail-mcp-card-actions">
              <button type="button" class="workspace-detail-mcp-card-btn" data-workspace-skill-action="edit" data-binding-id="${this.escapeHtml(binding.id)}" title="Edit binding" aria-label="Edit skill binding ${this.escapeHtml(skillName)}">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
                </svg>
              </button>
              <button type="button" class="workspace-detail-mcp-card-btn is-danger" data-workspace-skill-action="delete" data-binding-id="${this.escapeHtml(binding.id)}" title="Remove binding" aria-label="Remove skill binding ${this.escapeHtml(skillName)}">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
                </svg>
              </button>
            </div>
          </div>
          <div class="workspace-detail-mcp-chip-row">${chips}</div>
        </div>
      `;
    }).join('');
    this.renderWorkspaceConfigSummary();
  }

  getAgentInstanceIdsForName(agentName) {
    const normalized = this.normalizeAgentName(agentName);
    if (!normalized || !this.workspace || !Array.isArray(this.workspace.agent_instances)) {
      return [];
    }

    const ids = [];
    const seen = new Set();
    this.workspace.agent_instances.forEach((instance) => {
      if (this.normalizeAgentName(instance?.name) !== normalized) return;
      const id = String(instance?.id || '').trim();
      if (!id || seen.has(id)) return;
      seen.add(id);
      ids.push(id);
    });
    return ids;
  }

  getEffectiveWorkspaceMCPBindingsForAgent(agentName) {
    const bindings = this.getWorkspaceMCPBindings();
    if (bindings.length === 0) {
      return [];
    }

    const instanceIds = this.getAgentInstanceIdsForName(agentName);
    if (instanceIds.length === 0) {
      return bindings;
    }

    const accessEntries = Array.isArray(this.workspace?.agent_mcp_access)
      ? this.workspace.agent_mcp_access
      : [];

    const allowedByInstance = instanceIds.map((instanceID) => {
      const entry = accessEntries.find((item) => String(item?.agent_instance_id || '').trim() === instanceID);
      if (!entry) {
        return bindings;
      }
      if (!Array.isArray(entry.enabled_binding_ids) || entry.enabled_binding_ids.length === 0) {
        return [];
      }

      const allowedIDs = new Set(
        entry.enabled_binding_ids
          .map((value) => String(value || '').trim().toLowerCase())
          .filter(Boolean)
      );
      return bindings.filter((binding) => allowedIDs.has(String(binding.id || '').trim().toLowerCase()));
    });

    const merged = [];
    const seen = new Set();
    allowedByInstance.flat().forEach((binding) => {
      const key = String(binding?.id || '').trim().toLowerCase() || String(binding?.serverName || '').trim().toLowerCase();
      if (!key || seen.has(key)) return;
      seen.add(key);
      merged.push(binding);
    });
    return merged;
  }

  getEffectiveWorkspaceMCPServerNames(agentName) {
    const names = [];
    const seen = new Set();
    const add = (value) => {
      const name = String(value || '').trim();
      if (!name) return;
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      names.push(name);
    };

    this.getEffectiveWorkspaceMCPBindingsForAgent(agentName).forEach((binding) => add(binding.serverName));

    const profile = this.getAgentProfile(agentName);
    if (Array.isArray(profile?.mcpServers)) {
      profile.mcpServers.forEach(add);
    }

    return names;
  }

  async loadAgentCatalog(force = false) {
    if (!force && this.agentIndex instanceof Map && this.agentIndex.size > 0) {
      return this.agentCatalog;
    }

    const nextCatalog = [];
    const nextIndex = new Map();
    try {
      const response = await fetch('/api/agents/dashboard/list');
      if (response.ok) {
        const data = await response.json();
        const agents = Array.isArray(data?.agents) ? data.agents : [];
        agents.forEach((agent) => {
          const name = String(agent?.name || '').trim();
          if (!name) return;

          const profile = {
            name,
            model: String(agent?.model || '').trim(),
            status: String(agent?.status || '').trim().toLowerCase(),
            capabilities: Array.isArray(agent?.capabilities) ? agent.capabilities.map((value) => String(value || '').trim()).filter(Boolean) : [],
            allowWebSearch: Boolean(agent?.allow_web_search),
            enabledPlugins: Array.isArray(agent?.enabled_plugins) ? agent.enabled_plugins.map((value) => String(value || '').trim()).filter(Boolean) : [],
            mcpServers: Array.isArray(agent?.mcp_servers) ? agent.mcp_servers.map((value) => String(value || '').trim()).filter(Boolean) : [],
            evolution: agent?.evolution && typeof agent.evolution === 'object' ? agent.evolution : null,
            level: Number.isFinite(Number(agent?.evolution?.level)) ? Math.max(0, Math.floor(Number(agent.evolution.level))) : 0,
            stage: String(agent?.evolution?.stage || '').trim()
          };

          nextCatalog.push(profile);
          nextIndex.set(this.normalizeAgentName(name), profile);
        });
      }
    } catch (error) {
      console.error('Failed to load agent catalog:', error);
    }

    this.agentCatalog = nextCatalog;
    this.agentIndex = nextIndex;

    if (!Array.isArray(this.agentOptions) || this.agentOptions.length === 0) {
      this.agentOptions = this.buildAgentOptionsFromCatalog();
    }

    return this.agentCatalog;
  }

  buildAgentOptionsFromCatalog() {
    const options = [{ label: 'Unassigned', value: '' }];
    const seen = new Set();
    this.agentCatalog.forEach((profile) => {
      const name = String(profile?.name || '').trim();
      if (!name) return;
      const key = this.normalizeAgentName(name);
      if (seen.has(key)) return;
      seen.add(key);
      options.push({ label: name, value: `node:${name}-node-1` });
    });
    return options;
  }

  agentSupportsBrowserAutomation(profile) {
    if (!profile || !profile.allowWebSearch) {
      return false;
    }

    const lowerCapabilities = new Set((profile.capabilities || []).map((value) => String(value || '').trim().toLowerCase()).filter(Boolean));
    if (lowerCapabilities.has('browser') || lowerCapabilities.has('browser_automation') || lowerCapabilities.has('web_search') || lowerCapabilities.has('web_fetch')) {
      return true;
    }

    const pluginNames = (profile.enabledPlugins || []).map((value) => String(value || '').trim().toLowerCase()).filter(Boolean);
    for (const name of pluginNames) {
      if (name.startsWith('browser') ||
          name.startsWith('web_fetch') ||
          name.startsWith('web_search') ||
          name === 'navigate' ||
          name === 'open_url' ||
          name.includes('playwright') ||
          name.includes('browserbase') ||
          name.includes('puppeteer')) {
        return true;
      }
    }

    const serverNames = this.getEffectiveWorkspaceMCPServerNames(profile.name)
      .map((value) => String(value || '').trim().toLowerCase())
      .filter(Boolean);
    for (const name of serverNames) {
      if (name.includes('playwright') || name.includes('browserbase') || name.includes('puppeteer') || name.includes('browser')) {
        return true;
      }
    }

    return false;
  }

  agentSupportsFilesystemOperations(profile) {
    const support = this.getAgentSupportForTaskRequirement(profile, [], {
      key: TASK_REQUIREMENT_KEYS.FILESYSTEM
    });
    return support.supported;
  }

  findBestBrowserCapableAgent(excludeAgent = '') {
    const exclude = this.normalizeAgentName(excludeAgent);
    const candidates = this.getWorkspaceAgentNames();
    for (const candidate of candidates) {
      if (this.normalizeAgentName(candidate) === exclude) continue;
      if (this.agentSupportsBrowserAutomation(this.getAgentProfile(candidate))) {
        return candidate;
      }
    }
    return '';
  }

  isLikelyBrowserAutomationIntent(description) {
    const lower = String(description || '').trim().toLowerCase();
    if (!lower) return false;

    const verbs = ['open', 'visit', 'navigate', 'go to', 'browse', 'click', 'fill', 'type', 'extract'];
    const hasVerb = verbs.some((verb) => lower.includes(verb));
    if (!hasVerb) return false;

    if (lower.includes('http://') || lower.includes('https://') || lower.includes('www.')) {
      return true;
    }

    const tokens = lower.split(/\s+/);
    for (const token of tokens) {
      const cleaned = token.replace(/^[\s,.;:!?"'`()[\]{}<>]+|[\s,.;:!?"'`()[\]{}<>]+$/g, '');
      if (!cleaned || cleaned.includes('/') || (cleaned.split('.').length - 1) < 1) continue;
      const parts = cleaned.split('.');
      const tld = parts[parts.length - 1];
      if (tld.length >= 2 && tld.length <= 12) {
        return true;
      }
    }

    return false;
  }

  async getTaskExecutionPreflight(task, assignedAgent) {
    const description = String(task?.description || task?.name || '').trim();
    const specialistAction = this.maybeBuildTravelAssistSpecialistAction({
      task,
      currentAgent: assignedAgent
    });
    if (specialistAction) {
      if (specialistAction.kind === 'switch' || specialistAction.kind === 'add_and_switch') {
        return {
          kind: 'switch_recommended',
          recommendedAgent: specialistAction.agentName,
          addToWorkspace: specialistAction.kind === 'add_and_switch',
          eyebrow: 'Specialist Handoff',
          confirmLabel: specialistAction.kind === 'add_and_switch' ? 'Add and Switch' : 'Switch Agent',
          message: specialistAction.copy || this.buildAssistSpecialistActionText(specialistAction),
          details: [
            String(specialistAction.description || '').trim(),
            'Substantive travel work should start with the specialist rather than the workspace manager.'
          ].filter(Boolean)
        };
      }
      if (specialistAction.kind === 'create') {
        return {
          kind: 'create_recommended',
          eyebrow: 'Specialist Handoff',
          title: `Create "${specialistAction.agentName}" before running?`,
          confirmLabel: 'Create Agent',
          message: specialistAction.copy || this.buildAssistSpecialistActionText(specialistAction),
          details: [
            String(specialistAction.description || '').trim(),
            'Create the travel specialist first, then assign this task there.'
          ].filter(Boolean),
          seedName: specialistAction.agentName,
          seedType: specialistAction.agentType || 'tool-calling',
          seedSystemPrompt: String(specialistAction.systemPrompt || '').trim(),
          autoDescription: String(specialistAction.description || '').trim()
        };
      }
    }

    const requirements = this.inferTaskExecutionRequirements(description);
    if (requirements.length === 0) {
      return { kind: 'none' };
    }

    const currentAgent = String(assignedAgent || '').trim();
    if (!currentAgent) {
      return { kind: 'none' };
    }

    const currentEvaluation = await this.evaluateAgentForTaskRequirements(currentAgent, requirements);
    if (currentEvaluation) {
      return { kind: 'none' };
    }

    const requirementSummary = requirements.map((requirement) => requirement.label.toLowerCase()).join(' and ');
    const recommendedAgent = await this.findBestAgentForTaskRequirements(requirements, { excludeAgent: currentAgent });
    if (recommendedAgent) {
      return {
        kind: 'switch_recommended',
        recommendedAgent: recommendedAgent.agentName,
        message: `"${currentAgent}" likely lacks ${requirementSummary} for this task.`,
        details: [
          ...requirements.map((requirement) => requirement.reason),
          ...recommendedAgent.reasons
        ].slice(0, 5)
      };
    }

    const capabilityCatalog = await this.loadCapabilitySuggestionCatalog();
    const suggestions = this.getCapabilitySuggestionsForRequirements(requirements, capabilityCatalog);
    const defaults = this.getTaskRequirementSeedDefaults(requirements);

    return {
      kind: 'create_recommended',
      message: `This task likely needs ${requirementSummary}, but "${currentAgent}" does not advertise matching MCP servers, tools, or skills.`,
      details: [
        ...requirements.map((requirement) => requirement.reason),
        suggestions.mcpServers.length > 0 ? `Suggested MCP servers: ${suggestions.mcpServers.join(', ')}` : 'No matching MCP server is configured yet.',
        suggestions.skills.length > 0 ? `Suggested skills: ${suggestions.skills.join(', ')}` : 'No matching reusable skill is available yet.'
      ],
      suggestedMCPServers: suggestions.mcpServers,
      suggestedSkills: suggestions.skills,
      seedName: defaults.name,
      seedType: defaults.type,
      autoDescription: this.buildCapabilityAwareAgentDescription(task, requirements, suggestions)
    };
  }

  getAgentCapabilityBadges(agentName) {
    const profile = this.getAgentProfile(agentName);
    if (!profile) return [];

    const badges = [];
    if (this.agentSupportsBrowserAutomation(profile)) {
      badges.push({ key: 'browser', label: 'Browser' });
    }
    if (this.agentSupportsFilesystemOperations(profile)) {
      badges.push({ key: 'files', label: 'Files' });
    }
    if (profile.allowWebSearch) {
      badges.push({ key: 'web', label: 'Web' });
    }
    if (this.getEffectiveWorkspaceMCPServerNames(agentName).length > 0) {
      badges.push({ key: 'mcp', label: 'MCP' });
    }
    if ((profile.enabledPlugins || []).length > 0) {
      badges.push({ key: 'tools', label: 'Tools' });
    }

    return badges.slice(0, 4);
  }

  renderAgentCapabilityBadges(agentName) {
    const badges = this.getAgentCapabilityBadges(agentName);
    if (!badges.length) return '';

    const chips = badges
      .map((badge) => `<span class="workspace-detail-capability-chip ${this.escapeHtml(badge.key)}">${this.escapeHtml(badge.label)}</span>`)
      .join('');

    return `<span class="workspace-detail-agent-capabilities">${chips}</span>`;
  }

  getWorkspaceFallbackAgent(options = {}) {
    const candidates = this.getWorkspaceAgentNames();
    if (candidates.length === 0) return '';

    if (options.preferBrowser === true) {
      const browserCandidate = candidates.find((name) => this.agentSupportsBrowserAutomation(this.getAgentProfile(name)));
      if (browserCandidate) {
        return browserCandidate;
      }
    }

    return candidates[0];
  }

  async assignTaskToAgent(taskId, agentName) {
    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ to: agentName })
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to assign task');
    }
  }

  getTaskExecutionState(task) {
    if (!task || typeof task !== 'object') return 'pending';
    const status = String(task.status || '').trim().toLowerCase();
    const humanLoopState = String(task?.context?.human_loop?.state || '').trim().toLowerCase();
    if (humanLoopState === 'blocked') return 'blocked';
    return status || 'pending';
  }

  setExecutionModalStatus(status) {
    if (!this.elements.taskExecutionStatus) return;
    const safeStatus = String(status || 'pending').trim().toLowerCase();
    this.elements.taskExecutionStatus.className = `workspace-detail-task-status ${getStatusClass(safeStatus)}`;
    this.elements.taskExecutionStatus.textContent = getDisplayStatus(safeStatus);
  }

  clearExecutionLog() {
    this.executionLogKeys = new Set();
    this.executionLastStatus = '';
    if (!this.elements.taskExecutionLog) return;
    this.elements.taskExecutionLog.innerHTML = '<div class="workspace-detail-task-execution-empty">Execution updates will appear here.</div>';
  }

  appendExecutionLog(message, variant = 'info', dedupeKey = '') {
    if (!this.elements.taskExecutionLog || !message) return;
    const key = String(dedupeKey || '').trim();
    if (key) {
      if (this.executionLogKeys.has(key)) return;
      this.executionLogKeys.add(key);
    }

    const empty = this.elements.taskExecutionLog.querySelector('.workspace-detail-task-execution-empty');
    if (empty) empty.remove();

    const entry = document.createElement('div');
    const safeVariant = ['info', 'success', 'warning', 'error'].includes(variant) ? variant : 'info';
    entry.className = `workspace-detail-task-execution-log-entry ${safeVariant}`;

    const timeEl = document.createElement('div');
    timeEl.className = 'workspace-detail-task-execution-log-time';
    timeEl.textContent = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });

    const messageEl = document.createElement('div');
    messageEl.className = 'workspace-detail-task-execution-log-message';
    messageEl.textContent = String(message);

    entry.appendChild(timeEl);
    entry.appendChild(messageEl);
    this.elements.taskExecutionLog.appendChild(entry);
    this.elements.taskExecutionLog.scrollTop = this.elements.taskExecutionLog.scrollHeight;
  }

  setExecutionViewResultEnabled(enabled) {
    if (!this.elements.taskExecutionViewResultBtn) return;
    this.elements.taskExecutionViewResultBtn.disabled = !enabled;
  }

  setExecutionNextStepEnabled(enabled, label = 'Run Next Step') {
    if (!this.elements.taskExecutionNextStepBtn) return;
    this.elements.taskExecutionNextStepBtn.textContent = label;
    this.elements.taskExecutionNextStepBtn.classList.toggle('d-none', !enabled);
    this.elements.taskExecutionNextStepBtn.disabled = !enabled;
  }

  applyTopBackdropLayer(layerClass) {
    const backdrops = Array.from(document.querySelectorAll('.modal-backdrop.show'));
    if (!backdrops.length) return;

    const topBackdrop = backdrops[backdrops.length - 1];
    topBackdrop.classList.remove(
      'workspace-detail-backdrop-mcp',
      'workspace-detail-backdrop-execution',
      'workspace-detail-backdrop-result'
    );
    if (layerClass) {
      topBackdrop.classList.add(layerClass);
    }
  }

  updateTaskExecutionMeta(task) {
    if (!this.elements.taskExecutionMeta || !task) return;
    const answeredBy = this.getAnsweringAgentLabel(task);
    const updatedAt = formatDate(task.updated_at || task.completed_at || task.started_at || task.created_at);
    const retry = task?.context?.execution_retry || {};
    const attemptsUsed = Number(retry.attempts_used || 0);
    const maxAttempts = Number(retry.max_attempts || task?.context?.execution_max_attempts || 0);
    const parts = [answeredBy, updatedAt].filter(Boolean);
    if (this.getTaskExecutionMode(task) === 'step_through') {
      parts.push('Step-through');
    }
    if (attemptsUsed > 0 && maxAttempts > 0) {
      parts.push(`Attempt ${attemptsUsed}/${maxAttempts}`);
    }
    this.elements.taskExecutionMeta.textContent = parts.join(' • ');
  }

  updateTaskExecutionControls(task) {
    const awaitingNextStep = this.isTaskAwaitingNextStep(task);
    this.setExecutionNextStepEnabled(awaitingNextStep, awaitingNextStep ? 'Run Next Step' : 'Run Next Step');
  }

  async advanceCurrentExecutionStep() {
    if (!this.currentExecutionTaskId) return;

    try {
      this.setExecutionNextStepEnabled(false, 'Running...');
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: this.currentExecutionTaskId,
          step_action: 'next'
        })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to execute the next step');
      }
      this.appendExecutionLog('Running the next internal step.', 'info', `${this.currentExecutionTaskId}:next-step`);
      await this.startExecutionMonitor(this.currentExecutionTaskId);
      await this.loadTasks();
    } catch (error) {
      console.error('Failed to advance task step:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to run the next step');
      this.setExecutionNextStepEnabled(true);
    }
  }

  openTaskExecutionModal(task) {
    if (!this.elements.taskExecutionModal || !window.bootstrap || !task) return;
    const taskName = task.description || task.name || task.id || 'Task Execution';
    this.currentExecutionTaskId = task.id;
    if (this.elements.taskExecutionTitle) {
      this.elements.taskExecutionTitle.textContent = taskName;
    }
    this.setTaskModalHeaderId(this.elements.taskExecutionId, task.id);
    this.updateTaskExecutionMeta(task);
    this.setExecutionModalStatus(this.getTaskExecutionState(task));
    this.refreshExecutionBreakdown(task);
    this.updateTaskExecutionControls(task);
    this.setExecutionViewResultEnabled(false);
    this.clearExecutionLog();
    this.appendExecutionLog('Preparing execution...', 'info', `${task.id}:prepare`);

    const modal = typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.elements.taskExecutionModal)
      : (bootstrap.Modal.getInstance(this.elements.taskExecutionModal) || new bootstrap.Modal(this.elements.taskExecutionModal));
    modal.show();
  }

  stopExecutionMonitor() {
    if (this.executionMonitorTimer) {
      clearInterval(this.executionMonitorTimer);
      this.executionMonitorTimer = null;
    }
  }

  async startExecutionMonitor(taskId) {
    this.stopExecutionMonitor();
    if (!taskId) return;
    this.currentExecutionTaskId = taskId;

    const poll = async () => {
      try {
        const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`);
        if (!response.ok) return;

        const task = await response.json();
        if (!task || task.id !== taskId) return;

        const state = this.getTaskExecutionState(task);
        this.updateTaskExecutionMeta(task);
        this.setExecutionModalStatus(state);
        await this.refreshExecutionBreakdown(task);
        this.updateTaskExecutionControls(task);

        if (state !== this.executionLastStatus) {
          this.executionLastStatus = state;
          this.appendExecutionLog(`Status changed to ${getDisplayStatus(state)}.`, state === 'failed' ? 'error' : state === 'blocked' ? 'warning' : state === 'completed' ? 'success' : 'info', `${taskId}:status:${state}:${task.updated_at || ''}`);
        }

        if (this.isTaskAwaitingNextStep(task)) {
          const nextStepIndex = Number(task?.context?.execution_step_waiting_index || 0);
          const nextStep = Array.isArray(task?.execution_steps)
            ? task.execution_steps.find((step) => Number(step?.index) === nextStepIndex)
            : null;
          const nextLabel = nextStep?.title ? ` ${nextStep.title}` : '';
          this.appendExecutionLog(`Paused after the current step. Ready for step ${nextStepIndex || '?'}${nextLabel ? `: ${nextLabel}` : ''}.`, 'info', `${taskId}:waiting:${nextStepIndex}`);
        }

        if (state === 'completed' || state === 'failed' || state === 'cancelled' || state === 'timeout' || state === 'blocked') {
          this.stopExecutionMonitor();
          this.setExecutionViewResultEnabled(Boolean(task.result || task.error));
          if (state === 'completed') {
            this.appendExecutionLog('Execution completed successfully.', 'success', `${taskId}:terminal:completed`);
          } else if (state === 'blocked') {
            this.appendExecutionLog('Execution paused and requires your input.', 'warning', `${taskId}:terminal:blocked`);
          } else {
            this.appendExecutionLog('Execution ended with an issue.', 'error', `${taskId}:terminal:${state}`);
          }
          await this.loadTasks();
        }
      } catch (error) {
        console.error('Failed to monitor task execution:', error);
      }
    };

    await poll();
    this.executionMonitorTimer = setInterval(poll, 3000);
  }

  extractRealtimeTaskPayload(event) {
    const eventData = event && typeof event === 'object' ? (event.data || {}) : {};
    const payload = eventData && typeof eventData.data === 'object' ? eventData.data : eventData;
    const taskId = String(payload?.task_id || eventData?.task_id || '').trim();
    return { taskId, payload };
  }

  handleTaskExecutionRealtimeEvent(event) {
    if (!this.currentExecutionTaskId || !event) return;
    const { taskId, payload } = this.extractRealtimeTaskPayload(event);
    if (!taskId || taskId !== this.currentExecutionTaskId) return;

    switch (event.type) {
      case 'task.started':
        this.setExecutionModalStatus('in_progress');
        this.appendExecutionLog('Agent started executing this task.', 'info', `${taskId}:evt:started:${payload?.updated_at || ''}`);
        break;
      case 'task.progress': {
        this.setExecutionModalStatus('in_progress');
        const progress = payload?.progress || {};
        const currentStep = String(progress?.current_step || '').trim();
        const waiting = payload?.waiting_for_next_step === true;
        if (currentStep) {
          this.appendExecutionLog(currentStep, waiting ? 'success' : 'info', `${taskId}:evt:progress:${currentStep}`);
        }
        break;
      }
      case 'task.thinking':
        this.setExecutionModalStatus('in_progress');
        this.appendExecutionLog(payload?.message || 'Agent is analyzing the task...', 'info');
        break;
      case 'task.tool_call':
        this.appendExecutionLog(`Calling tool: ${payload?.tool_name || 'unknown tool'}`, 'info');
        break;
      case 'task.tool_result': {
        const success = payload?.success !== false;
        const resultPreview = payload?.result_preview ? ` (${payload.result_preview})` : '';
        const message = success
          ? `Tool completed: ${payload?.tool_name || 'tool'}${resultPreview}`
          : `Tool failed: ${payload?.tool_name || 'tool'}${payload?.error ? ` (${payload.error})` : ''}`;
        this.appendExecutionLog(message, success ? 'success' : 'warning');
        break;
      }
      case 'task.blocked':
        this.setExecutionModalStatus('blocked');
        this.appendExecutionLog(payload?.reason || 'Execution paused and requires your input.', 'warning', `${taskId}:evt:blocked:${payload?.updated_at || ''}`);
        this.stopExecutionMonitor();
        this.setExecutionViewResultEnabled(false);
        break;
      case 'task.completed':
        this.setExecutionModalStatus('completed');
        this.appendExecutionLog('Task completed.', 'success', `${taskId}:evt:completed:${payload?.updated_at || ''}`);
        this.stopExecutionMonitor();
        this.setExecutionViewResultEnabled(true);
        break;
      case 'task.failed':
        this.setExecutionModalStatus('failed');
        this.appendExecutionLog(payload?.error || 'Task failed.', 'error', `${taskId}:evt:failed:${payload?.updated_at || ''}`);
        this.stopExecutionMonitor();
        this.setExecutionViewResultEnabled(true);
        break;
      case 'task.resumed':
        this.setExecutionModalStatus('in_progress');
        this.appendExecutionLog('Task resumed after guidance.', 'info', `${taskId}:evt:resumed:${payload?.updated_at || ''}`);
        this.startExecutionMonitor(taskId);
        break;
    }
  }

  async executeTask(taskId, options = {}) {
    await this.loadAgentCatalog();

    const task = this.tasks.find((item) => item.id === taskId);
    if (!task) return;

    const subtasks = this.getSubtasksForParent(taskId);
    const isParent = subtasks.length > 0;
    const awaitingNextStep = !isParent && this.isTaskAwaitingNextStep(task);
    const stepAction = String(options?.stepAction || (awaitingNextStep ? 'next' : '')).trim().toLowerCase();
    const taskDescription = task.description || task.name || '';
    const taskRequirements = !isParent ? this.inferTaskExecutionRequirements(taskDescription) : [];
    const isBrowserIntent = taskRequirements.some((requirement) => requirement.key === TASK_REQUIREMENT_KEYS.BROWSER);

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
    } else if (task.status === 'in_progress' && !awaitingNextStep) {
      if (window.Toast) window.Toast.error('This task is already running.');
      return;
    }

    let assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
    const getExecutionSequencePreview = (agentName = assignedAgent) => this.getPredictedTaskExecutionSequence(
      task,
      isParent ? subtasks : [],
      { assignedAgent: agentName }
    );
    if (!isParent && !assignedAgent && !awaitingNextStep) {
      if (taskRequirements.length > 0) {
        assignedAgent = await this.suggestAgentSetupForTask(task);
        if (!assignedAgent) {
          return;
        }
      } else {
        const fallbackAgent = this.getWorkspaceFallbackAgent({ preferBrowser: isBrowserIntent });
        if (!fallbackAgent) {
          assignedAgent = await this.suggestAgentSetupForTask(task);
          if (!assignedAgent) {
            return;
          }
        } else {
          const assignAndRun = options.skipConfirm === true
            ? true
            : await this.showTaskConfirmDialog({
              eyebrow: 'Assignment Required',
              title: `Execute with "${fallbackAgent}"?`,
              message: `This task is unassigned. I can assign it to "${fallbackAgent}" and start execution immediately.`,
              confirmLabel: 'Assign and Execute',
              metaItems: [this.getTaskDisplayLabel(task), fallbackAgent, 'Workspace agent'],
              details: ['The task will be updated before dispatch.', 'Live execution updates will appear after confirmation.'],
              sequenceItems: getExecutionSequencePreview(fallbackAgent)
            });
          if (!assignAndRun) return;

          try {
            await this.assignTaskToAgent(taskId, fallbackAgent);
            assignedAgent = fallbackAgent;
            task.to = fallbackAgent;
            this.renderTasks();
          } catch (error) {
            console.error('Failed to auto-assign task before execution:', error);
            if (window.Toast) window.Toast.error('Failed to assign task before execution');
            return;
          }
        }
      }

      if (!assignedAgent) {
        return;
      }
    }

    let preflightWarning = '';
    if (!isParent && !awaitingNextStep) {
      const preflight = await this.getTaskExecutionPreflight(task, assignedAgent);
      if (preflight.kind === 'switch_recommended' && preflight.recommendedAgent) {
        const switchNow = await this.showTaskConfirmDialog({
          eyebrow: preflight.eyebrow || 'Capability Check',
          title: `Switch to "${preflight.recommendedAgent}" before running?`,
          message: preflight.message,
          confirmLabel: preflight.confirmLabel || 'Switch Agent',
          metaItems: [this.getTaskDisplayLabel(task), preflight.recommendedAgent, 'Recommended agent'],
          details: Array.isArray(preflight.details) && preflight.details.length > 0
            ? preflight.details
            : [`Switching now avoids a likely pause for "${assignedAgent}".`],
          sequenceItems: getExecutionSequencePreview(preflight.recommendedAgent)
        });
        if (switchNow) {
          try {
            if (preflight.addToWorkspace === true) {
              await this.addAgentToWorkspace(preflight.recommendedAgent, { toast: false });
            }
            await this.assignTaskToAgent(taskId, preflight.recommendedAgent);
            assignedAgent = preflight.recommendedAgent;
            task.to = preflight.recommendedAgent;
            this.renderTasks();
          } catch (error) {
            console.error('Failed to switch task to recommended agent:', error);
            if (window.Toast) window.Toast.error('Failed to switch to recommended agent');
            return;
          }
        } else {
          preflightWarning = `${preflight.message} Continuing may trigger a pause for user input.`;
        }
      } else if (preflight.kind === 'create_recommended') {
        const createAgent = await this.showTaskConfirmDialog({
          eyebrow: preflight.eyebrow || 'Capability Check',
          title: preflight.title || 'Create a capable agent before running?',
          message: preflight.message,
          confirmLabel: preflight.confirmLabel || 'Create Agent',
          cancelLabel: 'Continue Anyway',
          metaItems: [this.getTaskDisplayLabel(task), assignedAgent, 'Current assignment'],
          details: Array.isArray(preflight.details) ? preflight.details : [],
          sequenceItems: getExecutionSequencePreview()
        });
        if (createAgent) {
          this.openCreateAgentFlow({
            seedName: String(preflight.seedName || '').trim(),
            seedType: String(preflight.seedType || '').trim(),
            seedSystemPrompt: String(preflight.seedSystemPrompt || '').trim(),
            autoDescription: String(preflight.autoDescription || '').trim(),
            preferAutoConfig: true,
            workspaceId: this.workspaceId,
            taskId: task.id,
            assignTask: true,
            suggestedMCPServers: Array.isArray(preflight.suggestedMCPServers) ? preflight.suggestedMCPServers : [],
            suggestedSkills: Array.isArray(preflight.suggestedSkills) ? preflight.suggestedSkills : []
          });
          return;
        }
        preflightWarning = `${preflight.message} Continuing may trigger a pause for user input.`;
      } else if (preflight.kind === 'warning') {
        preflightWarning = preflight.message;
      }
    }

    const sequencePreview = getExecutionSequencePreview();
    if (options.skipConfirm !== true) {
      const confirmed = await this.showTaskConfirmDialog({
        eyebrow: stepAction === 'next' ? 'Step Launch' : isParent ? 'Workflow Launch' : 'Task Launch',
        title: stepAction === 'next'
          ? 'Run the next step now?'
          : isParent ? 'Execute this workflow now?' : 'Execute this task now?',
        message: stepAction === 'next'
          ? 'This task is paused between internal execution steps. Ori will run the next step and keep the current plan intact.'
          : isParent
          ? `This workflow will dispatch ${subtasks.length} step${subtasks.length === 1 ? '' : 's'} in sequence.`
          : `This task will run with "${assignedAgent}".`,
        confirmLabel: stepAction === 'next' ? 'Run Next Step' : isParent ? 'Execute Workflow' : 'Execute Task',
        metaItems: stepAction === 'next'
          ? [this.getTaskDisplayLabel(task), assignedAgent, 'Step-through']
          : isParent
          ? [this.getTaskDisplayLabel(task), `${subtasks.length} step${subtasks.length === 1 ? '' : 's'}`]
          : [this.getTaskDisplayLabel(task), assignedAgent, 'Ready to run'],
        details: preflightWarning
          ? [preflightWarning, 'Execution updates will appear in the live log after dispatch.']
          : ['Execution updates will appear in the live log after dispatch.'],
        sequenceItems: sequencePreview,
        allowStepThrough: !isParent && stepAction !== 'next' && sequencePreview.length > 1,
        defaultStepThrough: this.getTaskExecutionMode(task) === 'step_through'
      });
      if (!confirmed) return;
    }

    const selectedStepThrough = !isParent && stepAction !== 'next'
      ? (options.skipConfirm === true
        ? this.getTaskExecutionMode(task) === 'step_through'
        : this.consumePendingTaskConfirmStepThroughSelection())
      : false;
    if (!isParent && stepAction !== 'next') {
      task.execution_mode = selectedStepThrough ? 'step_through' : 'auto';
    }

    this.openTaskExecutionModal(task);

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: taskId,
          step_action: stepAction || undefined,
          execution_mode: isParent ? undefined : (selectedStepThrough ? 'step_through' : 'auto')
        })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to execute task');
      }

      if (window.Toast) window.Toast.success('Task started');
      this.appendExecutionLog('Task dispatched. Waiting for agent updates...', 'info', `${taskId}:dispatched`);
      await this.loadTasks();
      this.startExecutionMonitor(taskId);
    } catch (error) {
      console.error('Failed to execute task:', error);
      const message = error && error.message ? error.message : 'Failed to execute task';
      if (this.isMissingWorkspaceAgentError(message)) {
        this.setExecutionModalStatus('blocked');
        this.appendExecutionLog('No agent is assigned to this workspace yet. Ori is preparing a suggested agent setup.', 'warning', `${taskId}:dispatch-agent-missing`);
        this.setExecutionViewResultEnabled(false);

        if (this.elements.taskExecutionModal && window.bootstrap) {
          const modal = bootstrap.Modal.getInstance(this.elements.taskExecutionModal);
          modal?.hide();
        }

        if (options.skipMissingAgentRecovery === true) {
          if (window.Toast) window.Toast.error(message);
          return;
        }

        const suggestedAgent = await this.suggestAgentSetupForTask(task);
        if (suggestedAgent) {
          await this.executeTask(taskId, {
            skipConfirm: true,
            skipMissingAgentRecovery: true
          });
        }
        return;
      }

      this.setExecutionModalStatus('failed');
      this.appendExecutionLog(message, 'error', `${taskId}:dispatch-error`);
      if (window.Toast) window.Toast.error('Failed to execute task');
    }
  }

  async pollTaskCompletion(taskId, maxAttempts = 36, intervalMs = 5000) {
    let attempts = 0;

    const poll = async () => {
      attempts += 1;
      if (attempts > maxAttempts) return;

      try {
        const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`);
        if (!response.ok) {
          setTimeout(poll, intervalMs);
          return;
        }

        const task = await response.json();
        const status = task && task.status;
        if (status === 'completed' || status === 'failed' || status === 'cancelled' || status === 'timeout') {
          await this.loadTasks();
          return;
        }
      } catch (error) {
        console.error('Failed to poll task status:', error);
      }

      setTimeout(poll, intervalMs);
    };

    setTimeout(poll, intervalMs);
  }

  /**
   * Set the current view (list or board)
   */
  setView(view) {
    this.currentView = view;

    // Update toggle buttons
    if (this.elements.viewListBtn) {
      this.elements.viewListBtn.classList.toggle('is-active', view === 'list');
      this.elements.viewListBtn.setAttribute('aria-selected', (view === 'list').toString());
    }
    if (this.elements.viewBoardBtn) {
      this.elements.viewBoardBtn.classList.toggle('is-active', view === 'board');
      this.elements.viewBoardBtn.setAttribute('aria-selected', (view === 'board').toString());
    }

    // Toggle panel class for expanded board view
    if (this.elements.agentsPanel) {
      this.elements.agentsPanel.classList.toggle('board-view', view === 'board');
    }

    // Show/hide views
    if (this.elements.agentsList) {
      this.elements.agentsList.style.display = view === 'list' ? '' : 'none';
    }
    if (this.elements.tasksBoard) {
      this.elements.tasksBoard.style.display = view === 'board' ? '' : 'none';
    }

    // Load board if switching to board view
    if (view === 'board') {
      this.loadBoard();
    }
  }

  /**
   * Load board configuration and render
   */
  async loadBoard() {
    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/board`);
      if (!response.ok) {
        this.boardConfig = null;
        this.renderBoard();
        return;
      }

      const data = await response.json();
      this.boardConfig = data.board || null;
      await this.ensureAgentOptions();
      this.renderBoard();
    } catch (error) {
      console.error('Failed to load board:', error);
      this.boardConfig = null;
      this.renderBoard();
    }
  }

  async saveBoardConfig(columns) {
    const payload = { columns };
    if (this.boardConfig?.version) {
      payload.version = this.boardConfig.version;
    }

    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/board`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to save board');
    }

    const data = await response.json();
    return data.board || { columns };
  }

  async ensureAgentOptions() {
    if (Array.isArray(this.agentOptions) && this.agentOptions.length > 0) {
      return this.agentOptions;
    }

    await this.loadAgentCatalog();
    if (Array.isArray(this.agentOptions) && this.agentOptions.length > 0) {
      return this.agentOptions;
    }

    const options = [{ label: 'Unassigned', value: '' }];
    this.getWorkspaceAgentNames().forEach((name) => {
      const nodeId = `${name}-node-1`;
      options.push({ label: name, value: `node:${nodeId}` });
    });
    this.agentOptions = options;
    return this.agentOptions;
  }

  getKanbanLabels(task) {
    const ctx = task?.context;
    if (!ctx || typeof ctx !== 'object') return [];
    const raw = ctx.kanban_labels;
    if (Array.isArray(raw)) {
      return raw.map((label) => String(label || '').trim()).filter(Boolean);
    }
    if (typeof raw === 'string') {
      return raw.split(',').map((label) => label.trim()).filter(Boolean);
    }
    return [];
  }

  formatLabelsInput(labels) {
    return (labels || []).join(', ');
  }

  parseLabelsInput(value) {
    if (!value) return [];
    return value.split(',').map((label) => label.trim()).filter(Boolean);
  }

  getKanbanDueDate(task) {
    const ctx = task?.context;
    if (!ctx || typeof ctx !== 'object') return '';
    const raw = ctx.kanban_due_date;
    return typeof raw === 'string' ? raw.trim() : '';
  }

  normalizeDueInput(value) {
    if (!value) return '';
    if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value;
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    return date.toISOString().slice(0, 10);
  }

  formatDueDate(value) {
    if (!value) return '';
    const date = /^\d{4}-\d{2}-\d{2}$/.test(value) ? new Date(`${value}T00:00:00`) : new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleDateString();
  }

  getAssignmentValue(task) {
    if (task?.assigned_node_id) return `node:${task.assigned_node_id}`;
    if (task?.to && task.to !== 'unassigned') return `node:${task.to}-node-1`;
    return '';
  }

  getAssignmentLabel(task) {
    if (task?.assigned_node_id) {
      const match = String(task.assigned_node_id).match(/^(.+)-node-\d+$/);
      return match ? match[1] : task.assigned_node_id;
    }
    if (task?.to && task.to !== 'unassigned') return task.to;
    return 'Unassigned';
  }

  buildAssignmentOptions(selectedValue, selectedLabel) {
    const options = Array.isArray(this.agentOptions) ? this.agentOptions.slice() : [{ label: 'Unassigned', value: '' }];
    if (selectedValue && !options.some((opt) => opt.value === selectedValue)) {
      options.push({ label: selectedLabel || selectedValue, value: selectedValue });
    }
    return options;
  }

  getNextColumnOrder(columns) {
    const maxOrder = (columns || []).reduce((max, col) => {
      const value = Number.isFinite(col?.order) ? col.order : 0;
      return value > max ? value : max;
    }, 0);
    return maxOrder + 1;
  }

  makeColumnId(name, columns) {
    const existing = new Set((columns || []).map((col) => String(col?.id || '').trim()).filter(Boolean));
    const base = String(name || '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/(^-|-$)/g, '') || 'column';
    let id = base;
    let idx = 1;
    while (existing.has(id)) {
      idx += 1;
      id = `${base}-${idx}`;
    }
    return id;
  }

  /**
   * Render the kanban board
   */
  renderBoard() {
    if (!this.elements.boardColumns || !this.elements.boardEmpty) return;

    const columns = this.boardConfig?.columns || [];

    if (columns.length === 0) {
      this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach((card) => {
        if (card._editOutsideHandler) {
          document.removeEventListener('click', card._editOutsideHandler);
          card._editOutsideHandler = null;
        }
      });
      this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add').forEach((addEl) => {
        if (addEl._addOutsideHandler) {
          document.removeEventListener('click', addEl._addOutsideHandler);
          addEl._addOutsideHandler = null;
        }
      });
      this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add-column').forEach((addEl) => {
        if (addEl._addOutsideHandler) {
          document.removeEventListener('click', addEl._addOutsideHandler);
          addEl._addOutsideHandler = null;
        }
      });
      this.elements.boardColumns.innerHTML = '';
      this.elements.boardEmpty.style.display = '';
      return;
    }

    this.elements.boardEmpty.style.display = 'none';

    // Group tasks by column
    const tasksByColumn = this.groupTasksByColumn(columns);

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach((card) => {
      if (card._editOutsideHandler) {
        document.removeEventListener('click', card._editOutsideHandler);
        card._editOutsideHandler = null;
      }
    });
    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add').forEach((addEl) => {
      if (addEl._addOutsideHandler) {
        document.removeEventListener('click', addEl._addOutsideHandler);
        addEl._addOutsideHandler = null;
      }
    });
    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add-column').forEach((addEl) => {
      if (addEl._addOutsideHandler) {
        document.removeEventListener('click', addEl._addOutsideHandler);
        addEl._addOutsideHandler = null;
      }
    });

    const columnsHtml = columns.map(col => {
      const columnTasks = tasksByColumn[col.id] || [];
      return `
        <div class="workspace-detail-board-column" data-column-id="${col.id}">
          <div class="workspace-detail-board-column-header">
            <div class="workspace-detail-board-column-title-wrap">
              <span class="workspace-detail-board-column-title" data-column-id="${this.escapeHtml(col.id)}">${this.escapeHtml(col.name)}</span>
              <button class="workspace-detail-board-column-edit-btn" type="button" title="Edit column name" aria-label="Edit column name">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M5,18.08V19H5.92L14.81,10.11L13.89,9.19L5,18.08M17.71,7.04C18.1,6.65 18.1,6 17.71,5.63L16.37,4.29C16,3.9 15.35,3.9 14.96,4.29L13.13,6.12L14.88,7.87L17.71,7.04Z"/>
                </svg>
              </button>
            </div>
            <span class="workspace-detail-board-column-count">${columnTasks.length}</span>
          </div>
          <div class="workspace-detail-board-column-body" data-column-id="${this.escapeHtml(col.id)}">
            ${columnTasks.map(task => this.renderBoardCard(task)).join('')}
            <div class="workspace-detail-board-add" data-column-id="${this.escapeHtml(col.id)}">
              <button class="workspace-detail-board-add-btn" type="button">+ Add card</button>
              <div class="workspace-detail-board-add-form" hidden>
                <input class="workspace-detail-board-add-input" type="text" placeholder="Task title" />
                <div class="workspace-detail-board-add-actions">
                  <button class="modern-btn modern-btn-secondary workspace-detail-board-add-cancel" type="button">Cancel</button>
                  <button class="modern-btn modern-btn-primary workspace-detail-board-add-submit" type="button">Add</button>
                </div>
              </div>
            </div>
          </div>
        </div>
      `;
    }).join('');

    const addColumnHtml = `
      <div class="workspace-detail-board-add-column">
        <button class="workspace-detail-board-add-column-btn" type="button">+ Add column</button>
        <div class="workspace-detail-board-add-column-form" hidden>
          <input class="workspace-detail-board-add-column-input" type="text" placeholder="Column name" />
          <div class="workspace-detail-board-add-column-actions">
            <button class="modern-btn modern-btn-secondary workspace-detail-board-add-column-cancel" type="button">Cancel</button>
            <button class="modern-btn modern-btn-primary workspace-detail-board-add-column-submit" type="button">Add</button>
          </div>
        </div>
      </div>
    `;

    this.elements.boardColumns.innerHTML = columnsHtml + addColumnHtml;

    this.wireBoardDragAndDrop();
    this.wireBoardCardEditing();
    this.wireBoardCardAdd();
    this.wireBoardColumnAdd();
    this.wireBoardColumnRename();
  }

  /**
   * Render a single board card
   */
  renderBoardCard(task) {
    const labels = this.getKanbanLabels(task);
    const dueDate = this.getKanbanDueDate(task);
    const assignmentValue = this.getAssignmentValue(task);
    const assignmentLabel = this.getAssignmentLabel(task);
    const assignmentOptions = this.buildAssignmentOptions(assignmentValue, assignmentLabel);
    const labelMarkup = labels.length > 0
      ? `<div class="workspace-detail-board-card-labels">${labels.map((label) => `<span class="workspace-detail-board-card-label">${this.escapeHtml(label)}</span>`).join('')}</div>`
      : '';
    const dueMarkup = dueDate ? `<span class="workspace-detail-board-card-due">Due ${this.escapeHtml(this.formatDueDate(dueDate))}</span>` : '';
    const assignedMarkup = assignmentLabel && assignmentLabel !== 'Unassigned'
      ? `<span class="workspace-detail-board-card-assignee">${this.escapeHtml(assignmentLabel)}</span>`
      : '<span class="workspace-detail-board-card-assignee is-muted">Unassigned</span>';
    const scheduleIndicator = this.renderTaskScheduleIndicator(task, 'board');
    const editTitleValue = this.escapeHtml(task.description || task.name || task.id || '');
    const editDetailsValue = this.escapeHtml(task.details || '');
    const editLabelsValue = this.escapeHtml(this.formatLabelsInput(labels));
    const editDueValue = this.escapeHtml(this.normalizeDueInput(dueDate));
    const assignmentOptionsHtml = assignmentOptions
      .map((opt) => {
        const selected = opt.value === assignmentValue ? ' selected' : '';
        return `<option value="${this.escapeHtml(opt.value)}"${selected}>${this.escapeHtml(opt.label)}</option>`;
      })
      .join('');

    return `
      <div class="workspace-detail-board-card" draggable="true" data-task-id="${this.escapeHtml(task.id)}">
        <div class="workspace-detail-board-card-view">
          <div class="workspace-detail-board-card-header">
            <div class="workspace-detail-board-card-title">${this.escapeHtml(task.description || task.name || task.id || 'Untitled')}</div>
            <button class="workspace-detail-board-card-edit-btn" type="button" title="Edit card" aria-label="Edit card">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M5,18.08V19H5.92L14.81,10.11L13.89,9.19L5,18.08M17.71,7.04C18.1,6.65 18.1,6 17.71,5.63L16.37,4.29C16,3.9 15.35,3.9 14.96,4.29L13.13,6.12L14.88,7.87L17.71,7.04Z"/>
              </svg>
            </button>
          </div>
          ${labelMarkup}
          <div class="workspace-detail-board-card-meta">
            ${assignedMarkup}
            ${dueMarkup}
          </div>
          <div class="workspace-detail-board-card-meta workspace-detail-board-card-meta-secondary">
            <span>${getDisplayStatus(task.status)}</span>
            ${scheduleIndicator}
          </div>
        </div>
        <div class="workspace-detail-board-card-edit" hidden>
          <label class="workspace-detail-board-card-edit-label">Title</label>
          <input class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-title" type="text" value="${editTitleValue}" />
          <label class="workspace-detail-board-card-edit-label">Description</label>
          <textarea class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-details" rows="3">${editDetailsValue}</textarea>
          <label class="workspace-detail-board-card-edit-label">Assignee</label>
          <select class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-assignee">
            ${assignmentOptionsHtml}
          </select>
          <label class="workspace-detail-board-card-edit-label">Labels</label>
          <input class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-labels" type="text" value="${editLabelsValue}" placeholder="design, frontend" />
          <label class="workspace-detail-board-card-edit-label">Due date</label>
          <input class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-due" type="date" value="${editDueValue}" />
          <div class="workspace-detail-board-card-edit-actions">
            <button class="modern-btn modern-btn-secondary workspace-detail-board-card-edit-cancel" type="button">Cancel</button>
            <button class="modern-btn modern-btn-primary workspace-detail-board-card-edit-save" type="button">Done</button>
          </div>
        </div>
      </div>
    `;
  }

  /**
   * Group tasks by their kanban column
   */
  groupTasksByColumn(columns) {
    const groups = {};
    columns.forEach(col => { groups[col.id] = []; });

    // Only include top-level tasks
    const topLevelTasks = this.tasks.filter(t => !t.parent_task_id);

    topLevelTasks.forEach(task => {
      const colId = task.context?.kanban_column_id || columns[0]?.id;
      if (groups[colId]) {
        groups[colId].push(task);
      } else if (columns.length > 0) {
        // Fallback to first column
        groups[columns[0].id].push(task);
      }
    });

    return groups;
  }

  parseAssignmentValue(value) {
    let to = '';
    let assignedNodeId = '';
    if (value && value.startsWith('node:')) {
      assignedNodeId = value.slice('node:'.length);
      const match = assignedNodeId.match(/^(.+)-node-\d+$/);
      to = match ? match[1] : assignedNodeId;
    }
    return { to, assignedNodeId };
  }

  getBoardCardEditValues(card) {
    const titleEl = card.querySelector('.workspace-detail-board-card-edit-title');
    const detailsEl = card.querySelector('.workspace-detail-board-card-edit-details');
    const assigneeEl = card.querySelector('.workspace-detail-board-card-edit-assignee');
    const labelsEl = card.querySelector('.workspace-detail-board-card-edit-labels');
    const dueEl = card.querySelector('.workspace-detail-board-card-edit-due');

    const title = titleEl ? titleEl.value.trim() : '';
    const details = detailsEl ? detailsEl.value.trim() : '';
    const assigneeValue = assigneeEl ? assigneeEl.value : '';
    const labels = this.parseLabelsInput(labelsEl ? labelsEl.value : '');
    const dueDate = dueEl ? dueEl.value : '';
    const assignment = this.parseAssignmentValue(assigneeValue);

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

  hasBoardCardChanges(current, original) {
    if (!original) return true;
    if (current.title !== original.title) return true;
    if (current.details !== original.details) return true;
    if (current.assigneeValue !== original.assigneeValue) return true;
    if (current.dueDate !== original.dueDate) return true;
    const currentLabels = (current.labels || []).join('|');
    const originalLabels = (original.labels || []).join('|');
    return currentLabels !== originalLabels;
  }

  enterBoardCardEdit(card) {
    if (!card || card.classList.contains('is-editing')) return;
    const editEl = card.querySelector('.workspace-detail-board-card-edit');
    const viewEl = card.querySelector('.workspace-detail-board-card-view');
    if (!editEl) return;

    card.classList.add('is-editing');
    card.setAttribute('draggable', 'false');
    if (viewEl) viewEl.setAttribute('hidden', '');
    editEl.removeAttribute('hidden');

    card._editOriginal = this.getBoardCardEditValues(card);

    const focusEl = editEl.querySelector('input, textarea, select');
    if (focusEl) {
      focusEl.focus();
      if (focusEl.select) focusEl.select();
    }

    setTimeout(() => {
      const handler = (evt) => {
        if (card.contains(evt.target)) return;
        this.saveBoardCardEdits(card);
      };
      card._editOutsideHandler = handler;
      document.addEventListener('click', handler);
    }, 0);
  }

  exitBoardCardEdit(card, { reset = false } = {}) {
    if (!card || !card.classList.contains('is-editing')) return;
    const editEl = card.querySelector('.workspace-detail-board-card-edit');
    const viewEl = card.querySelector('.workspace-detail-board-card-view');

    if (reset && card._editOriginal) {
      const titleEl = card.querySelector('.workspace-detail-board-card-edit-title');
      const detailsEl = card.querySelector('.workspace-detail-board-card-edit-details');
      const assigneeEl = card.querySelector('.workspace-detail-board-card-edit-assignee');
      const labelsEl = card.querySelector('.workspace-detail-board-card-edit-labels');
      const dueEl = card.querySelector('.workspace-detail-board-card-edit-due');
      if (titleEl) titleEl.value = card._editOriginal.title || '';
      if (detailsEl) detailsEl.value = card._editOriginal.details || '';
      if (assigneeEl) assigneeEl.value = card._editOriginal.assigneeValue || '';
      if (labelsEl) labelsEl.value = this.formatLabelsInput(card._editOriginal.labels || []);
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

  async updateTaskDetails(taskId, updates) {
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
    const idx = this.tasks.findIndex((t) => t && t.id === taskId);
    if (idx >= 0) {
      this.tasks.splice(idx, 1, { ...this.tasks[idx], ...updated });
    }

    this.renderTasks();
    if (this.boardConfig) {
      this.renderBoard();
    }
  }

  async updateTaskKanbanColumn(taskId, columnId) {
    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kanban_column_id: columnId })
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to move task');
    }

    const updated = await response.json();
    const idx = this.tasks.findIndex((t) => t && t.id === taskId);
    if (idx >= 0) {
      const existing = this.tasks[idx];
      const nextContext = { ...(existing.context || {}), ...(updated.context || {}) };
      this.tasks.splice(idx, 1, { ...existing, ...updated, context: nextContext });
    }

    this.renderTasks();
    if (this.boardConfig) {
      this.renderBoard();
    }
  }

  async saveBoardCardEdits(card) {
    if (!card || !card.classList.contains('is-editing')) return;
    if (card.dataset.saving === '1') return;

    const taskId = card.dataset.taskId;
    if (!taskId) return;

    const current = this.getBoardCardEditValues(card);
    const original = card._editOriginal;
    if (!this.hasBoardCardChanges(current, original)) {
      this.exitBoardCardEdit(card);
      return;
    }

    if (!current.title) {
      if (window.Toast) window.Toast.error('Title is required');
      const titleEl = card.querySelector('.workspace-detail-board-card-edit-title');
      if (titleEl) titleEl.focus();
      return;
    }

    card.dataset.saving = '1';
    card.classList.add('is-saving');

    try {
      await this.updateTaskDetails(taskId, current);
      this.exitBoardCardEdit(card);
    } catch (error) {
      console.error('Failed to update task:', error);
      if (window.Toast) window.Toast.error('Failed to update task');
    } finally {
      card.dataset.saving = '';
      card.classList.remove('is-saving');
    }
  }

  wireBoardCardEditing() {
    if (!this.elements.boardColumns) return;

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach((card) => {
      card.addEventListener('click', (e) => {
        if (this.boardDidDrag) return;
        if (card.classList.contains('is-editing')) return;
        if (e.target.closest('.workspace-detail-board-card-edit') || e.target.closest('.workspace-detail-board-card-edit-btn')) return;
        if (e.target.closest('input') || e.target.closest('textarea') || e.target.closest('select') || e.target.closest('button')) return;
        const taskId = card.dataset.taskId;
        if (taskId) this.openTask(taskId);
      });

      const editBtn = card.querySelector('.workspace-detail-board-card-edit-btn');
      if (editBtn) {
        editBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          this.enterBoardCardEdit(card);
        });
      }

      const editContainer = card.querySelector('.workspace-detail-board-card-edit');
      if (editContainer) {
        editContainer.addEventListener('click', (e) => e.stopPropagation());
      }

      const cancelBtn = card.querySelector('.workspace-detail-board-card-edit-cancel');
      if (cancelBtn) {
        cancelBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          this.exitBoardCardEdit(card, { reset: true });
        });
      }

      const saveBtn = card.querySelector('.workspace-detail-board-card-edit-save');
      if (saveBtn) {
        saveBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          this.saveBoardCardEdits(card);
        });
      }

      const editFields = card.querySelectorAll('.workspace-detail-board-card-edit input, .workspace-detail-board-card-edit textarea, .workspace-detail-board-card-edit select');
      editFields.forEach((field) => {
        field.addEventListener('click', (e) => e.stopPropagation());
        field.addEventListener('keydown', (e) => {
          if (e.key === 'Escape') {
            e.preventDefault();
            this.exitBoardCardEdit(card, { reset: true });
            return;
          }
          if (e.key === 'Enter') {
            if (field.tagName === 'TEXTAREA' && !(e.metaKey || e.ctrlKey)) return;
            e.preventDefault();
            this.saveBoardCardEdits(card);
          }
        });
      });
    });
  }

  wireBoardCardAdd() {
    if (!this.elements.boardColumns) return;

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add').forEach((container) => {
      const columnId = container.dataset.columnId || '';
      const button = container.querySelector('.workspace-detail-board-add-btn');
      const form = container.querySelector('.workspace-detail-board-add-form');
      const input = container.querySelector('.workspace-detail-board-add-input');
      const cancelBtn = container.querySelector('.workspace-detail-board-add-cancel');
      const submitBtn = container.querySelector('.workspace-detail-board-add-submit');

      if (!button || !form || !input || !cancelBtn || !submitBtn) return;

      const closeForm = () => {
        form.setAttribute('hidden', '');
        button.removeAttribute('hidden');
        input.value = '';
        if (container._addOutsideHandler) {
          document.removeEventListener('click', container._addOutsideHandler);
          container._addOutsideHandler = null;
        }
      };

      const openForm = () => {
        button.setAttribute('hidden', '');
        form.removeAttribute('hidden');
        input.focus();
        input.select();
        setTimeout(() => {
          const handler = (evt) => {
            if (container.contains(evt.target)) return;
            closeForm();
          };
          container._addOutsideHandler = handler;
          document.addEventListener('click', handler);
        }, 0);
      };

      const submitForm = async () => {
        const title = input.value.trim();
        if (!title) {
          input.focus();
          return;
        }
        submitBtn.disabled = true;
        try {
          await this.createTask(title, '', columnId);
          closeForm();
        } catch (error) {
          console.error('Failed to create task:', error);
          if (window.Toast) window.Toast.error('Failed to create task');
        } finally {
          submitBtn.disabled = false;
        }
      };

      button.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        openForm();
      });

      cancelBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        closeForm();
      });

      submitBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        submitForm();
      });

      input.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
          e.preventDefault();
          closeForm();
          return;
        }
        if (e.key === 'Enter') {
          e.preventDefault();
          submitForm();
        }
      });
    });
  }

  wireBoardColumnAdd() {
    if (!this.elements.boardColumns) return;

    const container = this.elements.boardColumns.querySelector('.workspace-detail-board-add-column');
    if (!container) return;

    const button = container.querySelector('.workspace-detail-board-add-column-btn');
    const form = container.querySelector('.workspace-detail-board-add-column-form');
    const input = container.querySelector('.workspace-detail-board-add-column-input');
    const cancelBtn = container.querySelector('.workspace-detail-board-add-column-cancel');
    const submitBtn = container.querySelector('.workspace-detail-board-add-column-submit');

    if (!button || !form || !input || !cancelBtn || !submitBtn) return;

    const closeForm = () => {
      form.setAttribute('hidden', '');
      button.removeAttribute('hidden');
      input.value = '';
      if (container._addOutsideHandler) {
        document.removeEventListener('click', container._addOutsideHandler);
        container._addOutsideHandler = null;
      }
    };

    const openForm = () => {
      button.setAttribute('hidden', '');
      form.removeAttribute('hidden');
      input.focus();
      input.select();
      setTimeout(() => {
        const handler = (evt) => {
          if (container.contains(evt.target)) return;
          closeForm();
        };
        container._addOutsideHandler = handler;
        document.addEventListener('click', handler);
      }, 0);
    };

    const submitForm = async () => {
      const name = input.value.trim();
      if (!name) {
        input.focus();
        return;
      }
      submitBtn.disabled = true;
      try {
        const columns = Array.isArray(this.boardConfig?.columns) ? this.boardConfig.columns.slice() : [];
        const id = this.makeColumnId(name, columns);
        const order = this.getNextColumnOrder(columns);
        const next = columns.concat({ id, name, order });
        this.boardConfig = await this.saveBoardConfig(next);
        this.renderBoard();
        if (window.Toast) window.Toast.success('Column added');
      } catch (error) {
        console.error('Failed to add column:', error);
        if (window.Toast) window.Toast.error('Failed to add column');
      } finally {
        submitBtn.disabled = false;
      }
    };

    button.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      openForm();
    });

    cancelBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      closeForm();
    });

    submitBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      submitForm();
    });

    input.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        closeForm();
        return;
      }
      if (e.key === 'Enter') {
        e.preventDefault();
        submitForm();
      }
    });
  }

  wireBoardColumnRename() {
    if (!this.elements.boardColumns) return;

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-column-header').forEach((headerEl) => {
      const titleEl = headerEl.querySelector('.workspace-detail-board-column-title');
      const editBtn = headerEl.querySelector('.workspace-detail-board-column-edit-btn');
      const titleWrap = headerEl.querySelector('.workspace-detail-board-column-title-wrap');
      if (!titleEl || !editBtn) return;

      editBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();

        const columnId = titleEl.dataset.columnId || titleEl.closest('.workspace-detail-board-column')?.dataset.columnId;
        if (!columnId || !this.boardConfig?.columns) return;

        const currentName = titleEl.textContent || '';

        const input = document.createElement('input');
        input.type = 'text';
        input.className = 'workspace-detail-board-column-rename-input';
        input.value = currentName;

        titleEl.style.display = 'none';
        editBtn.style.display = 'none';
        if (titleWrap && titleWrap.contains(editBtn)) {
          titleWrap.insertBefore(input, editBtn);
        } else {
          const countEl = headerEl.querySelector('.workspace-detail-board-column-count');
          if (countEl) {
            headerEl.insertBefore(input, countEl);
          } else {
            headerEl.appendChild(input);
          }
        }
        input.focus();
        input.select();

        const finishRename = async () => {
          const newName = input.value.trim();
          input.remove();
          titleEl.style.display = '';
          editBtn.style.display = '';

          if (!newName || newName === currentName) return;

          const columns = (this.boardConfig?.columns || []).map((col) => {
            if (col.id === columnId) {
              return { ...col, name: newName };
            }
            return col;
          });

          try {
            this.boardConfig = await this.saveBoardConfig(columns);
            titleEl.textContent = newName;
            if (window.Toast) window.Toast.success('Column renamed');
          } catch (error) {
            console.error('Failed to rename column:', error);
            titleEl.textContent = currentName;
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
            input.value = currentName;
            input.blur();
          }
        });
      });
    });
  }

  wireBoardDragAndDrop() {
    if (!this.elements.boardColumns) return;

    let dragged = null;
    this.boardDidDrag = false;

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach((card) => {
      card.addEventListener('dragstart', (e) => {
        if (card.classList.contains('is-editing') || e.target.closest('.workspace-detail-board-card-edit')) {
          e.preventDefault();
          return;
        }
        dragged = card;
        this.boardDidDrag = true;
        card.classList.add('is-dragging');
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', card.dataset.taskId);
      });

      card.addEventListener('dragend', () => {
        card.classList.remove('is-dragging');
        dragged = null;
        setTimeout(() => {
          this.boardDidDrag = false;
        }, 0);
      });
    });

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-column-body').forEach((colEl) => {
      colEl.addEventListener('dragover', (e) => {
        e.preventDefault();
        colEl.closest('.workspace-detail-board-column')?.classList.add('is-drag-over');
        e.dataTransfer.dropEffect = 'move';
      });
      colEl.addEventListener('dragleave', () => {
        colEl.closest('.workspace-detail-board-column')?.classList.remove('is-drag-over');
      });
      colEl.addEventListener('drop', async (e) => {
        e.preventDefault();
        const columnId = colEl.closest('.workspace-detail-board-column')?.dataset.columnId || colEl.dataset.columnId;
        colEl.closest('.workspace-detail-board-column')?.classList.remove('is-drag-over');

        const taskId = e.dataTransfer.getData('text/plain') || dragged?.dataset.taskId;
        if (!taskId || !columnId) return;

        try {
          await this.updateTaskKanbanColumn(taskId, columnId);
        } catch (error) {
          console.error('Failed to update kanban column:', error);
          if (window.Toast) window.Toast.error('Failed to move task');
        }
      });
    });
  }

  /**
   * Setup a new board with default columns
   */
  async setupBoard() {
    const defaultColumns = [
      { id: 'backlog', name: 'Backlog' },
      { id: 'in-progress', name: 'In Progress' },
      { id: 'done', name: 'Done' }
    ];

    try {
      this.boardConfig = await this.saveBoardConfig(defaultColumns);
      await this.ensureAgentOptions();
      this.renderBoard();

      if (window.Toast) window.Toast.success('Board created');
    } catch (error) {
      console.error('Failed to setup board:', error);
      if (window.Toast) window.Toast.error('Failed to create board');
    }
  }

  /**
   * Load sessions for the workspace
   */
  async loadSessions() {
    this.sessionsLoading = true;
    this.sessionsLoadFailed = false;
    this.renderSessions();

    try {
      const response = await fetch(`/api/sessions?folder_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load sessions');

      const data = await response.json();
      this.sessions = data.sessions || data || [];
      this.sessionsLoadFailed = false;
    } catch (error) {
      console.error('Failed to load sessions:', error);
      this.sessions = [];
      this.sessionsLoadFailed = true;
    } finally {
      this.sessionsLoading = false;
      this.renderSessions();

      if (this.elements.sessionCount) {
        this.elements.sessionCount.textContent = this.sessions.length;
      }
      this.refreshHomeAssistantQuickPrompts();
    }
  }

  /**
   * Render sessions into the workspace-level sessions panel
   */
  renderSessions() {
    if (!this.elements.sessionsList) return;

    if (this.sessionsLoading) {
      this.elements.sessionsList.innerHTML = '<div class="workspace-detail-loading">Loading sessions...</div>';
      return;
    }

    if (this.sessionsLoadFailed) {
      this.elements.sessionsList.innerHTML = '<div class="workspace-detail-empty">Failed to load sessions.</div>';
      return;
    }

    const sessions = Array.isArray(this.sessions) ? this.sessions : [];
    if (sessions.length === 0) {
      this.elements.sessionsList.innerHTML = '<div class="workspace-detail-empty">No sessions yet.</div>';
      return;
    }

    this.elements.sessionsList.innerHTML = sessions.map((session) => this.renderSessionItem(session)).join('');
  }

  /**
   * Load files for the workspace
   */
  async loadFiles() {
    if (this.elements.filesList) {
      this.elements.filesList.innerHTML = '<div class="workspace-detail-loading">Loading files...</div>';
    }

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) {
        this.files = [];
        this.renderFiles();
        this.refreshHomeAssistantQuickPrompts();
        return;
      }

      const workspace = await response.json();
      // Filter attachments to only include files (not text content)
      this.files = (workspace.attachments || []).filter(a => a.file_meta || a.type === 'image' || a.type === 'other');
      this.renderFiles();
      this.refreshHomeAssistantQuickPrompts();
    } catch (error) {
      console.error('Failed to load files:', error);
      this.files = [];
      this.renderFiles();
      this.refreshHomeAssistantQuickPrompts();
    }
  }

  /**
   * Render files list
   */
  renderFiles() {
    if (!this.elements.filesList) return;

    if (!this.files || this.files.length === 0) {
      this.elements.filesList.innerHTML = '<div class="workspace-detail-empty">No files yet.</div>';
      return;
    }

    this.elements.filesList.innerHTML = this.files.map(file => {
      const title = file.title || (file.file_meta && file.file_meta.name) || 'Untitled File';
      const size = file.file_meta ? file.file_meta.size : null;
      const isMissing = file.file_meta?.status === 'missing';
      const metaParts = [];
      if (size) metaParts.push(this.formatFileSize(size));
      metaParts.push(formatDate(file.created_at));
      return `
        <div class="workspace-detail-item" data-file-id="${file.id}">
          ${isMissing ? `
            <button type="button" class="workspace-detail-item-run" onclick="event.stopPropagation(); window.workspaceDetail?.promptRelinkWorkspaceFile('${file.id}')" title="Choose a replacement file" aria-label="Relink missing file ${this.escapeHtml(title)}">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M10.59,13.41C11,13.8 11,14.44 10.59,14.83C10.2,15.22 9.56,15.22 9.17,14.83C7.22,12.88 7.22,9.71 9.17,7.76L12.71,4.22C14.66,2.27 17.83,2.27 19.78,4.22C21.73,6.17 21.73,9.34 19.78,11.29L18.29,12.78C18.3,11.96 18.17,11.14 17.89,10.36L18.36,9.88C19.54,8.71 19.54,6.81 18.36,5.64C17.19,4.46 15.29,4.46 14.12,5.64L10.59,9.17C9.41,10.34 9.41,12.24 10.59,13.41Z"/>
              </svg>
            </button>
          ` : ''}
          <div>
            <div class="workspace-detail-item-title">${this.escapeHtml(title)}</div>
            <div class="workspace-detail-item-meta">
              ${isMissing ? '<span class="workspace-detail-status-badge is-missing">Missing</span>' : ''}
              ${this.escapeHtml(metaParts.join(' · '))}
            </div>
          </div>
        </div>
      `;
    }).join('');
  }

  promptRelinkWorkspaceFile(fileId) {
    if (!fileId) return;

    const input = document.createElement('input');
    input.type = 'file';
    input.style.display = 'none';
    input.addEventListener('change', async () => {
      const selected = input.files && input.files[0];
      input.remove();
      if (!selected) return;
      await this.relinkWorkspaceFile(fileId, selected);
    }, { once: true });

    document.body.appendChild(input);
    input.click();
  }

  async relinkWorkspaceFile(fileId, file) {
    if (!fileId || !file) return;

    const formData = new FormData();
    formData.append('file', file);

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/attachments/${encodeURIComponent(fileId)}/relink`, {
        method: 'POST',
        body: formData
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(payload.error || payload.message || 'Failed to relink file');
      }

      if (window.Toast) {
        window.Toast.success('File relinked');
      }
      await this.loadFiles();
    } catch (error) {
      console.error('Failed to relink workspace file:', error);
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to relink file');
      }
    }
  }

  /**
   * Load notes for the workspace
   */
  async loadNotes() {
    this.updateCopyNotesButtonState(true);
    if (this.elements.notesList) {
      this.elements.notesList.innerHTML = '<div class="workspace-detail-loading">Loading notes...</div>';
    }

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`);
      if (!response.ok) {
        // Notes endpoint might return workspace notes differently
        this.notes = [];
        this.renderNotes();
        this.refreshHomeAssistantQuickPrompts();
        return;
      }

      const data = await response.json();
      this.notes = data.notes || (Array.isArray(data) ? data : [data]).filter(Boolean);
      this.renderNotes();
      this.refreshHomeAssistantQuickPrompts();
    } catch (error) {
      console.error('Failed to load notes:', error);
      this.notes = [];
      this.renderNotes();
      this.refreshHomeAssistantQuickPrompts();
    }
  }

  /**
   * Render notes list
   */
  renderNotes() {
    if (!this.elements.notesList) return;

    if (this.notes.length === 0) {
      this.elements.notesList.innerHTML = '<div class="workspace-detail-empty">No notes yet.</div>';
      this.updateCopyNotesButtonState(false);
      return;
    }

    this.elements.notesList.innerHTML = this.notes.map(note => {
      const title = note.name || note.title || 'Untitled Note';
      const preview = note.preview || note.content || '';
      return `
      <div class="workspace-detail-item" data-note-id="${note.id}">
        <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteNote('${note.id}')" title="Delete note" aria-label="Delete note ${this.escapeHtml(title)}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <div class="workspace-detail-item-content"
             role="button"
             tabindex="0"
             aria-label="Open note ${this.escapeHtml(title)}"
             onclick="window.workspaceDetail?.editNote('${note.id}')"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.editNote('${note.id}'); }">
          <div class="workspace-detail-item-title">${this.escapeHtml(title)}</div>
          <div class="workspace-detail-item-meta">
            ${preview ? this.escapeHtml(preview.substring(0, 50)) + (preview.length > 50 ? '...' : '') : 'Empty note'}
          </div>
        </div>
      </div>
    `;
    }).join('');
    this.updateCopyNotesButtonState(false);
  }

  /**
   * Activate the workspace on the backend, starting directory watchers
   * and any other context needed for the assistant.
   */
  activateWorkspace() {
    fetch(`/api/orchestration/workspace/activate?id=${encodeURIComponent(this.workspaceId)}`, {
      method: 'POST'
    }).catch(err => console.warn('Failed to activate workspace:', err));
  }

  /**
   * Load directories for the workspace
   */
  async loadDirectories() {
    if (this.elements.directoriesList) {
      this.elements.directoriesList.innerHTML = '<div class="workspace-detail-loading">Loading directories...</div>';
    }

    try {
      // Directories may come from:
      // 1) directory_references (workspace imports / folder picker)
      // 2) legacy attachments with type "directory"
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) {
        this.directories = [];
        this.renderDirectories();
        this.refreshHomeAssistantQuickPrompts();
        return;
      }

      const workspace = await response.json();

      if (this.workspace && workspace && typeof workspace === 'object') {
        this.workspace.directory_references = Array.isArray(workspace.directory_references) ? workspace.directory_references : [];
        this.workspace.mcp_bindings = Array.isArray(workspace.mcp_bindings) ? workspace.mcp_bindings : [];
        this.workspace.agent_mcp_access = Array.isArray(workspace.agent_mcp_access) ? workspace.agent_mcp_access : [];
      }

      const refs = Array.isArray(workspace.directory_references)
        ? workspace.directory_references.map((ref) => ({
          id: ref.id,
          name: ref.name || '',
          path: ref.path || '',
          source: 'reference'
        }))
        : [];

      const attachmentDirs = Array.isArray(workspace.attachments)
        ? workspace.attachments
          .filter((attachment) => attachment && attachment.type === 'directory')
          .map((attachment) => ({
            id: attachment.id,
            name: attachment.title || attachment.name || '',
            path: attachment.path || attachment.body || '',
            source: 'attachment'
          }))
        : [];

      // De-duplicate by id first, then by normalized path for mixed legacy/new sources.
      const seenById = new Set();
      const seenByPath = new Set();
      this.directories = [];
      [...refs, ...attachmentDirs].forEach((dir) => {
        if (!dir || !dir.id) return;
        if (seenById.has(dir.id)) return;

        const normalizedPath = String(dir.path || '').trim().replace(/[\\/]+$/, '').toLowerCase();
        if (normalizedPath && seenByPath.has(normalizedPath)) {
          return;
        }

        seenById.add(dir.id);
        if (normalizedPath) {
          seenByPath.add(normalizedPath);
        }
        this.directories.push(dir);
      });

      this.renderDirectories();
      this.renderWorkspaceMCPBindings();
      this.renderWorkspaceSkillBindings();
      this.renderAgentGroups();
      this.refreshHomeAssistantQuickPrompts();
    } catch (error) {
      console.error('Failed to load directories:', error);
      this.directories = [];
      this.renderDirectories();
      this.refreshHomeAssistantQuickPrompts();
    }
  }

  /**
   * Render directories list
   */
  renderDirectories() {
    if (!this.elements.directoriesList) return;

    if (!this.directories || this.directories.length === 0) {
      this.elements.directoriesList.innerHTML = '<div class="workspace-detail-empty">No directories yet.</div>';
      return;
    }

    this.elements.directoriesList.innerHTML = this.directories.map(dir => {
      const name = dir.title || dir.name || dir.path || 'Unnamed Directory';
      const path = dir.path || '';
      const sourceLabel = dir.source === 'reference' ? 'Reference' : 'Attachment';
      const source = dir.source === 'attachment' ? 'attachment' : 'reference';
      return `
        <div class="workspace-detail-item" data-directory-id="${dir.id}">
          <button type="button" class="workspace-detail-item-run" onclick="event.stopPropagation(); window.workspaceDetail?.openDirectoryExplorer('${dir.id}', '${source}')" title="Explore directory" aria-label="Explore directory ${this.escapeHtml(name)}">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M15,12H16.5L15,13.5L16.42,14.92L20.34,11L16.42,7.08L15,8.5L16.5,10H15V12M19,19H5V5H13V3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V13H19V19Z"/>
            </svg>
          </button>
          <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteDirectory('${dir.id}', '${source}')" title="Remove directory" aria-label="Remove directory ${this.escapeHtml(name)}">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
          <div class="workspace-detail-item-content"
               role="button"
               tabindex="0"
               aria-label="Explore directory ${this.escapeHtml(name)}"
               onclick="window.workspaceDetail?.openDirectoryExplorer('${dir.id}', '${source}')"
               onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.openDirectoryExplorer('${dir.id}', '${source}'); }">
            <div class="workspace-detail-item-title">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-2" style="color: var(--text-secondary);">
                <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
              </svg>
              ${this.escapeHtml(name)}
            </div>
            <div class="workspace-detail-item-meta">${this.escapeHtml(path)}${path ? ' • ' : ''}${this.escapeHtml(sourceLabel)}</div>
          </div>
        </div>
      `;
    }).join('');
  }

  async deleteDirectory(directoryId, source = 'reference') {
    if (!directoryId) return;

    const confirmed = confirm('Remove this directory from the workspace? The folder on disk will not be deleted.');
    if (!confirmed) return;

    const normalizedSource = source === 'attachment' ? 'attachment' : 'reference';
    const endpoint = normalizedSource === 'attachment'
      ? `/api/workspaces/${encodeURIComponent(this.workspaceId)}/attachments/${encodeURIComponent(directoryId)}`
      : `/api/workspaces/${encodeURIComponent(this.workspaceId)}/directories/${encodeURIComponent(directoryId)}`;

    try {
      const response = await fetch(endpoint, { method: 'DELETE' });
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Failed to remove directory');
      }

      if (window.Toast) window.Toast.success('Directory removed');
      await this.loadDirectories();
    } catch (error) {
      console.error('Failed to remove directory:', error);
      if (window.Toast) window.Toast.error('Failed to remove directory');
    }
  }

  async openDirectoryExplorer(directoryId, source = 'reference') {
    if (!directoryId) return;

    const normalizedSource = source === 'attachment' ? 'attachment' : 'reference';
    const directory = this.directories.find((entry) => entry.id === directoryId);
    if (!directory) {
      if (window.Toast) window.Toast.error('Directory not found');
      return;
    }

    const modal = this.getDirectoryExplorerModalInstance();
    if (!modal) {
      if (window.Toast) window.Toast.error('Directory explorer is unavailable');
      return;
    }

    this.abortDirectoryPreviewRequest();
    this.directoryExplorer.directory = directory;
    this.directoryExplorer.source = normalizedSource;
    this.directoryExplorer.searchQuery = '';
    this.directoryExplorer.files = [];
    this.directoryExplorer.treeRoot = null;
    this.directoryExplorer.nodeIndex = new Map();
    this.directoryExplorer.selectedType = '';
    this.directoryExplorer.previewCache = new Map();

    this.loadDirectoryExplorerPersistedState(directory.id);
    if (this.elements.directoryExplorerSearch) {
      this.elements.directoryExplorerSearch.value = '';
    }
    if (this.elements.directoryExplorerTitle) {
      this.elements.directoryExplorerTitle.textContent = directory.name || directory.path || 'Directory Explorer';
    }
    if (this.elements.directoryExplorerSubtitle) {
      this.elements.directoryExplorerSubtitle.textContent = directory.path || '';
    }

    modal.show();
    this.renderDirectoryExplorerLoading();
    await this.loadDirectoryExplorerFiles();
  }

  getDirectoryExplorerModalInstance() {
    if (!this.elements.directoryExplorerModal || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      return null;
    }

    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.elements.directoryExplorerModal)
      : new bootstrap.Modal(this.elements.directoryExplorerModal);
  }

  renderDirectoryExplorerLoading(message = 'Scanning directory...') {
    if (this.elements.directoryExplorerTree) {
      this.elements.directoryExplorerTree.innerHTML = `<div class="workspace-directory-tree-empty">${this.escapeHtml(message)}</div>`;
    }
    if (this.elements.directoryExplorerPreview) {
      this.elements.directoryExplorerPreview.innerHTML = '<div class="workspace-directory-preview-empty">Select a file to preview.</div>';
    }
    if (this.elements.directoryExplorerSummary) {
      this.elements.directoryExplorerSummary.innerHTML = '';
    }
  }

  async loadDirectoryExplorerFiles({ force = false } = {}) {
    const explorer = this.directoryExplorer;
    const currentDirectory = explorer.directory;
    if (!currentDirectory || !currentDirectory.id) return;

    const loadToken = explorer.loadToken + 1;
    explorer.loadToken = loadToken;
    const cacheKey = currentDirectory.id;

    if (explorer.source !== 'reference') {
      if (this.elements.directoryExplorerTree) {
        this.elements.directoryExplorerTree.innerHTML = `
          <div class="workspace-directory-tree-empty">
            Legacy directory attachments cannot be browsed yet. Re-add this folder with the folder picker to enable Finder view.
          </div>
        `;
      }
      if (this.elements.directoryExplorerPreview) {
        this.elements.directoryExplorerPreview.innerHTML = '<div class="workspace-directory-preview-empty">Preview unavailable.</div>';
      }
      return;
    }

    const cached = explorer.fileCache.get(cacheKey);
    if (!force && cached && Array.isArray(cached.files)) {
      explorer.files = cached.files;
      const { root, nodeIndex } = this.buildDirectoryExplorerTree(cached.files);
      explorer.treeRoot = root;
      explorer.nodeIndex = nodeIndex;
      if (explorer.selectedPath && !nodeIndex.has(explorer.selectedPath)) {
        explorer.selectedPath = '';
        explorer.selectedType = '';
      }
      this.selectDefaultDirectoryNode();
      this.renderDirectoryExplorer();
      return;
    }

    this.renderDirectoryExplorerLoading(force ? 'Refreshing directory...' : 'Scanning directory...');

    try {
      const endpoint = `/api/workspaces/${encodeURIComponent(this.workspaceId)}/directories/${encodeURIComponent(currentDirectory.id)}/files`;
      const response = await fetch(endpoint);
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Failed to load directory contents');
      }

      const payload = await response.json();
      if (loadToken !== explorer.loadToken) return;

      const normalizedFiles = (Array.isArray(payload.files) ? payload.files : [])
        .map((item) => {
          const normalizedPath = this.normalizeRelativePath(item?.relative_path || item?.relativePath || '');
          if (!normalizedPath) return null;

          return {
            name: item?.name || normalizedPath.split('/').pop() || normalizedPath,
            path: normalizedPath,
            isDir: Boolean(item?.is_dir),
            size: Number(item?.size) || 0,
            modTime: item?.mod_time || ''
          };
        })
        .filter(Boolean);

      explorer.files = normalizedFiles;
      explorer.fileCache.set(cacheKey, { files: normalizedFiles, loadedAt: Date.now() });

      const { root, nodeIndex } = this.buildDirectoryExplorerTree(normalizedFiles);
      explorer.treeRoot = root;
      explorer.nodeIndex = nodeIndex;

      if (explorer.selectedPath && !nodeIndex.has(explorer.selectedPath)) {
        explorer.selectedPath = '';
        explorer.selectedType = '';
      }

      this.selectDefaultDirectoryNode();
      this.renderDirectoryExplorer();
    } catch (error) {
      if (loadToken !== explorer.loadToken) return;
      console.error('Failed to load directory explorer files:', error);
      if (this.elements.directoryExplorerTree) {
        this.elements.directoryExplorerTree.innerHTML = '<div class="workspace-directory-tree-empty">Failed to load directory. Check the folder path and try again.</div>';
      }
      if (this.elements.directoryExplorerPreview) {
        this.elements.directoryExplorerPreview.innerHTML = '<div class="workspace-directory-preview-empty">No preview available.</div>';
      }
      if (window.Toast) window.Toast.error('Failed to load directory contents');
    }
  }

  selectDefaultDirectoryNode() {
    const explorer = this.directoryExplorer;
    if (explorer.selectedPath && explorer.nodeIndex.has(explorer.selectedPath)) {
      if (!explorer.selectedType) {
        explorer.selectedType = explorer.nodeIndex.get(explorer.selectedPath)?.type || 'file';
      }
      this.ensureDirectoryAncestorsExpanded(explorer.selectedPath, explorer.selectedType);
      return;
    }

    const firstFile = explorer.files.find((entry) => !entry.isDir);
    if (firstFile) {
      explorer.selectedPath = firstFile.path;
      explorer.selectedType = 'file';
      this.ensureDirectoryAncestorsExpanded(firstFile.path, 'file');
      this.persistDirectoryExplorerState();
      return;
    }

    const firstDirectory = explorer.files.find((entry) => entry.isDir);
    if (firstDirectory) {
      explorer.selectedPath = firstDirectory.path;
      explorer.selectedType = 'dir';
      explorer.expandedPaths.add(firstDirectory.path);
      this.ensureDirectoryAncestorsExpanded(firstDirectory.path, 'dir');
      this.persistDirectoryExplorerState();
    }
  }

  buildDirectoryExplorerTree(files) {
    const rootName = this.directoryExplorer.directory?.name || this.directoryExplorer.directory?.path || 'Directory';
    const root = {
      type: 'dir',
      name: rootName,
      path: '',
      size: 0,
      modTime: '',
      children: []
    };
    const nodeIndex = new Map();
    nodeIndex.set('', root);

    const ensureDirectoryNode = (path) => {
      if (nodeIndex.has(path)) {
        return nodeIndex.get(path);
      }

      const normalizedPath = this.normalizeRelativePath(path);
      if (!normalizedPath) return root;

      const slashIndex = normalizedPath.lastIndexOf('/');
      const parentPath = slashIndex >= 0 ? normalizedPath.slice(0, slashIndex) : '';
      const parentNode = ensureDirectoryNode(parentPath);
      const name = normalizedPath.split('/').pop() || normalizedPath;

      const node = {
        type: 'dir',
        name,
        path: normalizedPath,
        size: 0,
        modTime: '',
        children: []
      };
      parentNode.children.push(node);
      nodeIndex.set(normalizedPath, node);
      return node;
    };

    files.forEach((entry) => {
      const normalizedPath = this.normalizeRelativePath(entry.path);
      if (!normalizedPath) return;

      if (entry.isDir) {
        const dirNode = ensureDirectoryNode(normalizedPath);
        dirNode.modTime = entry.modTime || dirNode.modTime;
        return;
      }

      const slashIndex = normalizedPath.lastIndexOf('/');
      const parentPath = slashIndex >= 0 ? normalizedPath.slice(0, slashIndex) : '';
      const parentNode = ensureDirectoryNode(parentPath);

      if (nodeIndex.has(normalizedPath)) {
        return;
      }

      const fileNode = {
        type: 'file',
        name: entry.name || normalizedPath.split('/').pop() || normalizedPath,
        path: normalizedPath,
        size: entry.size || 0,
        modTime: entry.modTime || '',
        children: null
      };
      parentNode.children.push(fileNode);
      nodeIndex.set(normalizedPath, fileNode);
    });

    return { root, nodeIndex };
  }

  renderDirectoryExplorer() {
    const explorer = this.directoryExplorer;
    const directory = explorer.directory;
    if (!directory) return;

    if (this.elements.directoryExplorerTitle) {
      this.elements.directoryExplorerTitle.textContent = directory.name || directory.path || 'Directory Explorer';
    }
    if (this.elements.directoryExplorerSubtitle) {
      this.elements.directoryExplorerSubtitle.textContent = directory.path || '';
    }
    if (this.elements.directoryExplorerSortBtn) {
      const direction = explorer.sortDirection === 'desc' ? 'Z-A' : 'A-Z';
      this.elements.directoryExplorerSortBtn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <path d="M7,3H9V17H12L8,21L4,17H7V3M14,7V5H20V7H14M14,11V9H18V11H14M14,15V13H20V15H14M14,19V17H18V19H14Z"/>
        </svg>
        ${direction}
      `;
    }

    this.renderDirectoryExplorerSummary();
    this.renderDirectoryExplorerBreadcrumb();

    const query = explorer.searchQuery.toLowerCase();
    const hasSearch = query.length > 0;
    const sourceRoot = explorer.treeRoot;

    if (!sourceRoot || !Array.isArray(sourceRoot.children) || sourceRoot.children.length === 0) {
      if (this.elements.directoryExplorerTree) {
        this.elements.directoryExplorerTree.innerHTML = '<div class="workspace-directory-tree-empty">This directory is empty.</div>';
      }
      if (this.elements.directoryExplorerPreview) {
        this.elements.directoryExplorerPreview.innerHTML = '<div class="workspace-directory-preview-empty">No files to preview yet.</div>';
      }
      return;
    }

    const renderRoot = hasSearch ? this.filterDirectoryTree(sourceRoot, query) : sourceRoot;
    const treeChildren = renderRoot?.children || [];

    if (this.elements.directoryExplorerTree) {
      if (treeChildren.length === 0) {
        this.elements.directoryExplorerTree.innerHTML = '<div class="workspace-directory-tree-empty">No matches for this search.</div>';
      } else {
        this.elements.directoryExplorerTree.innerHTML = `
          <div class="workspace-directory-tree-scroll">
            ${this.renderDirectoryTreeChildren(treeChildren, 0, hasSearch)}
          </div>
        `;
      }
    }

    this.renderDirectoryPreview();
  }

  renderDirectoryExplorerSummary() {
    if (!this.elements.directoryExplorerSummary) return;

    const files = this.directoryExplorer.files || [];
    const folderCount = files.filter((entry) => entry.isDir).length;
    const fileCount = files.length - folderCount;
    const selectedNode = this.directoryExplorer.nodeIndex.get(this.directoryExplorer.selectedPath);
    const selectedLabel = selectedNode
      ? `${selectedNode.type === 'dir' ? 'Folder' : 'File'} selected`
      : 'No selection';

    this.elements.directoryExplorerSummary.innerHTML = `
      <span class="workspace-directory-pill">${fileCount} file${fileCount === 1 ? '' : 's'}</span>
      <span class="workspace-directory-pill">${folderCount} folder${folderCount === 1 ? '' : 's'}</span>
      <span class="workspace-directory-pill is-muted">${this.escapeHtml(selectedLabel)}</span>
    `;
  }

  renderDirectoryExplorerBreadcrumb() {
    if (!this.elements.directoryExplorerBreadcrumb) return;

    const directory = this.directoryExplorer.directory;
    if (!directory) {
      this.elements.directoryExplorerBreadcrumb.innerHTML = '';
      return;
    }

    const selectedPath = this.directoryExplorer.selectedPath || '';
    const selectedType = this.directoryExplorer.selectedType || '';
    const segments = selectedPath ? selectedPath.split('/') : [];
    const crumbs = [
      { label: directory.name || 'Root', path: '', clickable: true, active: !selectedPath }
    ];

    let cursor = '';
    segments.forEach((segment, index) => {
      cursor = cursor ? `${cursor}/${segment}` : segment;
      const isLast = index === segments.length - 1;
      const canClick = !isLast || selectedType === 'file';
      crumbs.push({
        label: segment,
        path: cursor,
        clickable: canClick,
        active: isLast
      });
    });

    this.elements.directoryExplorerBreadcrumb.innerHTML = crumbs.map((crumb, index) => {
      const escapedLabel = this.escapeHtml(crumb.label || 'Root');
      const separator = index === 0 ? '' : '<span class="workspace-directory-crumb-separator">/</span>';
      if (!crumb.clickable || crumb.active) {
        return `${separator}<span class="workspace-directory-crumb is-active">${escapedLabel}</span>`;
      }

      return `
        ${separator}
        <button type="button"
                class="workspace-directory-crumb"
                data-action="breadcrumb"
                data-path="${this.encodeDataPath(crumb.path)}">
          ${escapedLabel}
        </button>
      `;
    }).join('');
  }

  filterDirectoryTree(node, query) {
    const isMatch = String(node.name || '').toLowerCase().includes(query)
      || String(node.path || '').toLowerCase().includes(query);

    if (node.type === 'file') {
      return isMatch ? { ...node } : null;
    }

    const filteredChildren = (node.children || [])
      .map((child) => this.filterDirectoryTree(child, query))
      .filter(Boolean);

    if (node.path === '' || isMatch || filteredChildren.length > 0) {
      return {
        ...node,
        children: filteredChildren
      };
    }

    return null;
  }

  renderDirectoryTreeChildren(children, depth, forceExpanded) {
    const sortedChildren = this.getSortedDirectoryChildren(children);
    return sortedChildren.map((node) => this.renderDirectoryTreeNode(node, depth, forceExpanded)).join('');
  }

  renderDirectoryTreeNode(node, depth, forceExpanded) {
    const encodedPath = this.encodeDataPath(node.path);
    const isDirectory = node.type === 'dir';
    const isExpanded = isDirectory && (forceExpanded || this.directoryExplorer.expandedPaths.has(node.path));
    const isSelected = this.directoryExplorer.selectedPath === node.path;
    const sizeText = !isDirectory && Number.isFinite(node.size) ? this.formatFileSize(node.size) : '';
    const modifiedText = node.modTime ? formatDate(node.modTime) : '';
    const metaText = isDirectory
      ? `${(node.children || []).length} item${(node.children || []).length === 1 ? '' : 's'}`
      : [sizeText, modifiedText].filter(Boolean).join(' · ');

    const icon = isDirectory
      ? '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/></svg>'
      : '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13Z"/></svg>';

    const toggleButton = isDirectory
      ? `
        <button type="button"
                class="workspace-directory-tree-toggle ${isExpanded ? 'is-expanded' : ''}"
                data-action="toggle-folder"
                data-path="${encodedPath}"
                aria-label="${isExpanded ? 'Collapse folder' : 'Expand folder'}">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M8.59,16.59L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.59Z"/>
          </svg>
        </button>
      `
      : '<span class="workspace-directory-tree-spacer"></span>';

    const childrenHtml = isDirectory && isExpanded && Array.isArray(node.children) && node.children.length > 0
      ? `<div class="workspace-directory-tree-children">${this.renderDirectoryTreeChildren(node.children, depth + 1, forceExpanded)}</div>`
      : '';

    return `
      <div class="workspace-directory-tree-node ${isSelected ? 'is-selected' : ''}">
        <div class="workspace-directory-tree-row" style="--tree-depth:${depth};">
          ${toggleButton}
          <button type="button"
                  class="workspace-directory-tree-main"
                  data-action="select-node"
                  data-path="${encodedPath}"
                  data-type="${isDirectory ? 'dir' : 'file'}">
            <span class="workspace-directory-tree-icon">${icon}</span>
            <span class="workspace-directory-tree-label">${this.escapeHtml(node.name || node.path || 'Untitled')}</span>
            <span class="workspace-directory-tree-meta">${this.escapeHtml(metaText)}</span>
          </button>
        </div>
        ${childrenHtml}
      </div>
    `;
  }

  getSortedDirectoryChildren(children) {
    const direction = this.directoryExplorer.sortDirection === 'desc' ? -1 : 1;
    return [...children].sort((a, b) => {
      if (a.type !== b.type) {
        return a.type === 'dir' ? -1 : 1;
      }
      return a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true }) * direction;
    });
  }

  toggleDirectoryNode(path) {
    if (!path) return;
    if (!this.directoryExplorer.nodeIndex.has(path)) return;
    if (this.directoryExplorer.expandedPaths.has(path)) {
      this.directoryExplorer.expandedPaths.delete(path);
    } else {
      this.directoryExplorer.expandedPaths.add(path);
    }
    this.persistDirectoryExplorerState();
    this.renderDirectoryExplorer();
  }

  expandDirectoryNode(path) {
    if (!path) return;
    if (!this.directoryExplorer.nodeIndex.has(path)) return;
    this.directoryExplorer.expandedPaths.add(path);
    this.persistDirectoryExplorerState();
    this.renderDirectoryExplorer();
  }

  collapseDirectoryNode(path) {
    if (!path) return;
    this.directoryExplorer.expandedPaths.delete(path);
    this.persistDirectoryExplorerState();
    this.renderDirectoryExplorer();
  }

  selectDirectoryNode(path, type, { autoExpand = false } = {}) {
    const normalizedPath = this.normalizeRelativePath(path);
    const node = this.directoryExplorer.nodeIndex.get(normalizedPath);
    if (!node) return;

    this.directoryExplorer.selectedPath = normalizedPath;
    this.directoryExplorer.selectedType = type === 'dir' ? 'dir' : node.type;

    if (autoExpand && this.directoryExplorer.selectedType === 'dir') {
      this.directoryExplorer.expandedPaths.add(normalizedPath);
    }

    this.ensureDirectoryAncestorsExpanded(normalizedPath, this.directoryExplorer.selectedType);
    this.persistDirectoryExplorerState();
    this.renderDirectoryExplorer();
  }

  ensureDirectoryAncestorsExpanded(path, type) {
    const normalizedPath = this.normalizeRelativePath(path);
    if (!normalizedPath) return;

    const parts = normalizedPath.split('/');
    const limit = type === 'dir' ? parts.length : parts.length - 1;
    let current = '';

    for (let i = 0; i < limit; i += 1) {
      current = current ? `${current}/${parts[i]}` : parts[i];
      this.directoryExplorer.expandedPaths.add(current);
    }
  }

  renderDirectoryPreview() {
    const previewEl = this.elements.directoryExplorerPreview;
    if (!previewEl) return;

    const selectedPath = this.directoryExplorer.selectedPath;
    if (!selectedPath) {
      previewEl.innerHTML = '<div class="workspace-directory-preview-empty">Select a file or folder to inspect.</div>';
      return;
    }

    const node = this.directoryExplorer.nodeIndex.get(selectedPath);
    if (!node) {
      previewEl.innerHTML = '<div class="workspace-directory-preview-empty">Select a valid entry to preview.</div>';
      return;
    }

    if (node.type === 'dir') {
      this.renderDirectoryFolderPreview(node);
      return;
    }

    void this.renderDirectoryFilePreview(node);
  }

  renderDirectoryFolderPreview(node) {
    const previewEl = this.elements.directoryExplorerPreview;
    if (!previewEl) return;

    const stats = this.collectDirectoryStats(node);
    const childItems = this.getSortedDirectoryChildren(node.children || []).slice(0, 16);

    previewEl.innerHTML = `
      <div class="workspace-directory-preview-header">
        <div class="workspace-directory-preview-title">${this.escapeHtml(node.name || 'Folder')}</div>
        <div class="workspace-directory-preview-subtitle">${this.escapeHtml(node.path || '/')}</div>
      </div>
      <div class="workspace-directory-preview-stats">
        <span class="workspace-directory-pill">${stats.files} file${stats.files === 1 ? '' : 's'}</span>
        <span class="workspace-directory-pill">${stats.folders} folder${stats.folders === 1 ? '' : 's'}</span>
      </div>
      <div class="workspace-directory-preview-directory-list">
        ${childItems.length === 0
      ? '<div class="workspace-directory-preview-empty-inline">Folder is empty.</div>'
      : childItems.map((child) => `
            <button type="button"
                    class="workspace-directory-preview-entry"
                    data-action="select-node"
                    data-path="${this.encodeDataPath(child.path)}"
                    data-type="${child.type}">
              <span>${this.escapeHtml(child.name)}</span>
              <span>${this.escapeHtml(child.type === 'dir' ? 'Folder' : this.formatFileSize(child.size || 0))}</span>
            </button>
          `).join('')}
      </div>
    `;
  }

  async renderDirectoryFilePreview(node) {
    const previewEl = this.elements.directoryExplorerPreview;
    if (!previewEl) return;

    const directoryId = this.directoryExplorer.directory?.id;
    if (!directoryId) return;

    const endpoint = this.getDirectoryFileEndpoint(directoryId, node.path);
    previewEl.innerHTML = `
      <div class="workspace-directory-preview-header">
        <div class="workspace-directory-preview-title">${this.escapeHtml(node.name || 'File')}</div>
        <div class="workspace-directory-preview-subtitle">${this.escapeHtml(node.path || '')}</div>
      </div>
      <div class="workspace-directory-preview-loading">Loading preview...</div>
    `;

    try {
      const preview = await this.loadDirectoryFilePreview(node.path);
      if (this.directoryExplorer.selectedPath !== node.path) return;

      const metadata = [
        preview.contentType || 'Unknown type',
        this.formatFileSize(preview.size || node.size || 0),
        node.modTime ? formatDate(node.modTime) : ''
      ].filter(Boolean).join(' · ');

      if (!preview.text) {
        previewEl.innerHTML = `
          <div class="workspace-directory-preview-header">
            <div class="workspace-directory-preview-title">${this.escapeHtml(node.name || 'File')}</div>
            <div class="workspace-directory-preview-subtitle">${this.escapeHtml(node.path || '')}</div>
          </div>
          <div class="workspace-directory-preview-stats">
            <span class="workspace-directory-pill">${this.escapeHtml(metadata)}</span>
            <a href="${endpoint}" target="_blank" rel="noopener noreferrer" class="workspace-directory-preview-open-link">Open raw</a>
          </div>
          <div class="workspace-directory-preview-empty-inline">
            ${preview.tooLarge ? 'File is too large for inline preview.' : 'Binary file preview is unavailable.'}
          </div>
        `;
        return;
      }

      previewEl.innerHTML = `
        <div class="workspace-directory-preview-header">
          <div class="workspace-directory-preview-title">${this.escapeHtml(node.name || 'File')}</div>
          <div class="workspace-directory-preview-subtitle">${this.escapeHtml(node.path || '')}</div>
        </div>
        <div class="workspace-directory-preview-stats">
          <span class="workspace-directory-pill">${this.escapeHtml(metadata)}</span>
          <a href="${endpoint}" target="_blank" rel="noopener noreferrer" class="workspace-directory-preview-open-link">Open raw</a>
        </div>
        <pre class="workspace-directory-preview-code">${this.escapeHtml(preview.text)}</pre>
        ${preview.truncated ? '<div class="workspace-directory-preview-note">Preview truncated for readability.</div>' : ''}
      `;
    } catch (error) {
      if (error?.name === 'AbortError') return;
      console.error('Failed to render directory file preview:', error);
      previewEl.innerHTML = `
        <div class="workspace-directory-preview-empty-inline">
          Failed to load file preview.
        </div>
      `;
    }
  }

  collectDirectoryStats(node) {
    if (!node || node.type !== 'dir') {
      return { files: 0, folders: 0 };
    }

    const stats = { files: 0, folders: 0 };
    const walk = (current) => {
      (current.children || []).forEach((child) => {
        if (child.type === 'dir') {
          stats.folders += 1;
          walk(child);
        } else {
          stats.files += 1;
        }
      });
    };
    walk(node);
    return stats;
  }

  async loadDirectoryFilePreview(relativePath) {
    const directoryId = this.directoryExplorer.directory?.id;
    if (!directoryId) {
      throw new Error('No directory selected');
    }

    const normalizedPath = this.normalizeRelativePath(relativePath);
    const cacheKey = `${directoryId}:${normalizedPath}`;
    const cached = this.directoryExplorer.previewCache.get(cacheKey);
    if (cached) {
      return cached;
    }

    const endpoint = this.getDirectoryFileEndpoint(directoryId, normalizedPath);
    this.abortDirectoryPreviewRequest();
    const controller = new AbortController();
    this.directoryExplorer.previewAbortController = controller;

    const response = await fetch(endpoint, { signal: controller.signal });
    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to fetch file content');
    }

    const contentType = (response.headers.get('content-type') || '').split(';')[0].trim();
    const blob = await response.blob();
    const preview = {
      text: '',
      contentType,
      size: blob.size,
      truncated: false,
      tooLarge: false
    };

    const MAX_PREVIEW_BYTES = 1_500_000;
    const MAX_PREVIEW_CHARS = 30_000;

    if (this.isTextPreviewable(normalizedPath, contentType)) {
      if (blob.size <= MAX_PREVIEW_BYTES) {
        let text = await blob.text();
        if (text.length > MAX_PREVIEW_CHARS) {
          text = text.slice(0, MAX_PREVIEW_CHARS);
          preview.truncated = true;
        }
        preview.text = text;
      } else {
        preview.tooLarge = true;
      }
    }

    this.directoryExplorer.previewCache.set(cacheKey, preview);
    return preview;
  }

  isTextPreviewable(relativePath, contentType) {
    const lowerPath = relativePath.toLowerCase();
    const textExtensions = [
      '.txt', '.md', '.markdown', '.json', '.yaml', '.yml', '.xml', '.csv', '.ts', '.tsx',
      '.js', '.jsx', '.cjs', '.mjs', '.go', '.py', '.java', '.rb', '.php', '.sh', '.zsh',
      '.bash', '.html', '.htm', '.css', '.scss', '.sass', '.less', '.sql', '.toml', '.ini',
      '.env', '.gitignore', '.dockerfile', '.makefile', '.conf', '.log'
    ];

    if (textExtensions.some((extension) => lowerPath.endsWith(extension))) {
      return true;
    }

    return contentType.startsWith('text/')
      || contentType.includes('json')
      || contentType.includes('xml')
      || contentType.includes('javascript')
      || contentType.includes('yaml');
  }

  getDirectoryFileEndpoint(directoryId, relativePath) {
    const normalizedPath = this.normalizeRelativePath(relativePath);
    const encodedPath = normalizedPath
      .split('/')
      .filter(Boolean)
      .map((part) => encodeURIComponent(part))
      .join('/');

    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/directories/${encodeURIComponent(directoryId)}/files/${encodedPath}`;
  }

  normalizeRelativePath(path) {
    if (!path) return '';
    return String(path)
      .trim()
      .replace(/\\/g, '/')
      .replace(/^\.\/+/, '')
      .replace(/^\/+/, '')
      .replace(/\/+/g, '/');
  }

  abortDirectoryPreviewRequest() {
    if (this.directoryExplorer.previewAbortController) {
      this.directoryExplorer.previewAbortController.abort();
      this.directoryExplorer.previewAbortController = null;
    }
  }

  getDirectoryExplorerStateStorageKey(directoryId) {
    return `workspace-directory-explorer:${this.workspaceId}:${directoryId}`;
  }

  loadDirectoryExplorerPersistedState(directoryId) {
    this.directoryExplorer.expandedPaths = new Set();
    this.directoryExplorer.selectedPath = '';

    if (!directoryId || typeof localStorage === 'undefined') return;

    try {
      const raw = localStorage.getItem(this.getDirectoryExplorerStateStorageKey(directoryId));
      if (!raw) return;

      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed.expanded_paths)) {
        this.directoryExplorer.expandedPaths = new Set(
          parsed.expanded_paths.map((path) => this.normalizeRelativePath(path)).filter(Boolean)
        );
      }
      if (typeof parsed.selected_path === 'string') {
        this.directoryExplorer.selectedPath = this.normalizeRelativePath(parsed.selected_path);
      }
      if (parsed.selected_type === 'dir' || parsed.selected_type === 'file') {
        this.directoryExplorer.selectedType = parsed.selected_type;
      }
    } catch (error) {
      console.warn('Failed to restore directory explorer state:', error);
    }
  }

  persistDirectoryExplorerState() {
    const directoryId = this.directoryExplorer.directory?.id;
    if (!directoryId || typeof localStorage === 'undefined') return;

    try {
      const payload = {
        expanded_paths: Array.from(this.directoryExplorer.expandedPaths),
        selected_path: this.directoryExplorer.selectedPath || '',
        selected_type: this.directoryExplorer.selectedType || ''
      };
      localStorage.setItem(this.getDirectoryExplorerStateStorageKey(directoryId), JSON.stringify(payload));
    } catch (error) {
      console.warn('Failed to persist directory explorer state:', error);
    }
  }

  encodeDataPath(path) {
    return encodeURIComponent(path || '');
  }

  decodeDataPath(path) {
    try {
      return decodeURIComponent(path || '');
    } catch {
      return path || '';
    }
  }

  /**
   * Show add directory modal - launches the folder picker
   */
  async showAddDirectoryModal() {
    const button = this.elements.addDirectoryBtn;
    if (!button) return;

    // Show loading state on button
    const originalHTML = button.innerHTML;
    button.disabled = true;
    button.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';

    try {
      const response = await fetch('/api/launch-folder-picker', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId
        })
      });

      const result = await response.json();

      if (result.success) {
        if (window.Toast) window.Toast.info('Folder picker opened. Select a folder to add it.');
        // The folder picker will add the directory and trigger a reload via realtime events
        // Schedule a reload in case realtime doesn't trigger
        setTimeout(() => this.loadDirectories(), 2000);
      } else {
        throw new Error(result.error || 'Failed to open folder picker');
      }
    } catch (error) {
      console.error('Failed to launch folder picker:', error);
      if (window.Toast) window.Toast.error('Failed to open folder picker');
    } finally {
      button.disabled = false;
      button.innerHTML = originalHTML;
    }
  }

  /**
   * Add a directory reference
   */
  async addDirectory(path) {
    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/attachments`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'directory',
          path: path,
          title: path.split('/').pop() || path
        })
      });

      if (!response.ok) throw new Error('Failed to add directory');

      if (window.Toast) window.Toast.success('Directory added');
      await this.loadDirectories();
    } catch (error) {
      console.error('Failed to add directory:', error);
      if (window.Toast) window.Toast.error('Failed to add directory');
    }
  }

  /**
   * Load scheduled tasks for the workspace
   * Schedules are tasks that have a schedule field set
   */
  async loadSchedules() {
    if (this.elements.schedulesList) {
      this.elements.schedulesList.innerHTML = '<div class="workspace-detail-loading">Loading schedules...</div>';
    }

    try {
      // Schedules are stored as tasks with schedule field
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) {
        this.schedules = [];
        this.renderSchedules();
        return;
      }

      const data = await response.json();
      const allTasks = data.tasks || [];

      // Filter tasks that have schedules
      this.schedules = allTasks
        .filter(task => task.schedule != null)
        .map(task => ({
          id: task.id,
          name: task.schedule_name || task.description,
          description: task.description,
          enabled: task.schedule_enabled,
          schedule: task.schedule,
          next_run: task.next_run,
          last_run: task.last_run,
          schedule_type: task.schedule ? 'cron' : 'interval'
        }));

      this.renderSchedules();
    } catch (error) {
      console.error('Failed to load schedules:', error);
      this.schedules = [];
      this.renderSchedules();
    }
  }

  /**
   * Render schedules list
   */
  renderSchedules() {
    if (!this.elements.schedulesList) return;

    if (!this.schedules || this.schedules.length === 0) {
      this.elements.schedulesList.innerHTML = '<div class="workspace-detail-empty">No scheduled tasks yet.</div>';
      return;
    }

    this.elements.schedulesList.innerHTML = this.schedules.map(schedule => {
      const name = schedule.name || schedule.task_description || 'Unnamed Schedule';
      const status = schedule.enabled ? 'Active' : 'Paused';
      const statusClass = schedule.enabled ? 'completed' : 'pending';
      return `
        <div class="workspace-detail-item"
             data-schedule-id="${schedule.id}"
             role="button"
             tabindex="0"
             aria-label="Open schedule ${this.escapeHtml(name)}"
             onclick="window.workspaceDetail?.openSchedule('${schedule.id}')"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.openSchedule('${schedule.id}'); }">
          <div class="d-flex justify-content-between align-items-start">
            <div class="workspace-detail-item-title">${this.escapeHtml(name)}</div>
            <span class="workspace-detail-task-status ${statusClass}">${status}</span>
          </div>
          <div class="workspace-detail-item-meta">
            ${schedule.schedule_type || 'interval'} · Next: ${schedule.next_run ? formatDate(schedule.next_run) : 'N/A'}
          </div>
        </div>
      `;
    }).join('');
  }

  /**
   * Show schedules modal
   */
  showSchedulesModal() {
    // Use the session manager's scheduled tasks panel if available
    if (window.sessionManager && typeof window.sessionManager.openScheduledTasksPanel === 'function') {
      window.sessionManager.openScheduledTasksPanel(this.workspaceId);
    } else {
      // Just reload the schedules list
      this.loadSchedules();
    }
  }

  /**
   * Open a schedule for viewing/editing
   */
  openSchedule(scheduleId) {
    if (window.sessionManager && typeof window.sessionManager.showScheduledTaskDetails === 'function') {
      const schedule = this.schedules.find(s => s.id === scheduleId);
      if (schedule) {
        window.sessionManager.showScheduledTaskDetails(schedule);
      }
    }
  }

  /**
   * Setup real-time updates
   */
  setupRealtime() {
    // Use existing realtime system if available
    if (window.workspaceRealtime && typeof window.workspaceRealtime.subscribeToWorkspace === 'function') {
      window.workspaceRealtime.subscribeToWorkspace(this.workspaceId, (event) => {
        this.handleRealtimeEvent(event);
      });
    }
  }

  /**
   * Handle real-time events
   */
  handleRealtimeEvent(event) {
    this.handleTaskExecutionRealtimeEvent(event);

    switch (event.type) {
      case 'task_created':
      case 'task_updated':
      case 'task_deleted':
      case 'task.created':
      case 'task.assigned':
      case 'task.started':
      case 'task.progress':
      case 'task.completed':
      case 'task.failed':
      case 'task.deleted':
        this.loadTasks();
        break;
      case 'task.thinking':
      case 'task.tool_call':
      case 'task.tool_result':
        break;
      case 'task.blocked': {
        this.loadTasks();
        const payload = event?.data?.data || event?.data || {};
        const taskId = payload.task_id || event?.data?.task_id;
        if (taskId) {
          this.openTaskAssistModal(taskId, payload);
        }
        break;
      }
      case 'task.resumed':
        this.loadTasks();
        if (this.elements.taskAssistModal && window.bootstrap) {
          const modal = bootstrap.Modal.getInstance(this.elements.taskAssistModal);
          modal?.hide();
        }
        break;
      case 'session_created':
      case 'session_updated':
      case 'session_deleted':
        this.loadSessions();
        break;
      case 'file_created':
      case 'file_deleted':
        this.loadFiles();
        break;
      case 'note_updated':
      case 'note_created':
      case 'note_deleted':
        this.loadNotes();
        break;
      case 'directory_added':
      case 'directory_removed':
      case 'attachment_created':
      case 'attachment_deleted':
        this.loadDirectories();
        this.loadFiles();
        break;
      case 'schedule_created':
      case 'schedule_updated':
      case 'schedule_deleted':
        this.loadSchedules();
        break;
      case 'workspace.updated': {
        const action = event?.data?.action || '';
        if (typeof action === 'string' && action.startsWith('directory_')) {
          this.loadWorkspace();
          this.loadDirectories();
          this.loadFiles();
          return;
        }
        if (typeof action === 'string' && (action.startsWith('mcp_') || action.startsWith('agent_mcp_access_'))) {
          this.loadWorkspace();
        }
        break;
      }
    }
  }

  /**
   * Handle quick input submission
   */
  async handleQuickInput() {
    const input = this.elements.smartInput?.value?.trim();
    if (!input) return;

    let handled = null;

    // Check for commands
    if (input.startsWith('/task ')) {
      const taskName = input.substring(6).trim();
      handled = await this.createTask(taskName, '', '', {
        requireConfirmation: true,
        source: 'assistant'
      });
    } else if (input.startsWith('/chat ') || input.startsWith('/c ')) {
      const message = input.replace(/^\/(chat|c)\s+/, '').trim();
      handled = await this.createSessionWithMessage(message);
    } else if (input.startsWith('/note ')) {
      const noteContent = input.substring(6).trim();
      handled = await this.createNote('Quick Note', noteContent);
    } else {
      // Default: continue Assistant chat in this workspace
      handled = await this.createSessionWithMessage(input);
    }

    if (handled && this.elements.smartInput) {
      this.elements.smartInput.value = '';
    }
  }

  /**
   * Create a new task
   */
  async createTask(name, description = '', columnId = '', options = {}) {
    const normalizedName = String(name || '').trim();
    const normalizedDescription = String(description || '').trim();
    if (!normalizedName) return null;

    if (options.requireConfirmation) {
      const metaItems = ['Assistant', 'Task'];
      const details = [normalizedName];
      if (normalizedDescription) {
        details.push(normalizedDescription);
      }
      if (options.assignee) {
        metaItems.push(String(options.assignee));
        details.push(`Assigned to: ${options.assignee}`);
      }
      if (options.scheduleSummary) {
        details.push(`Schedule: ${options.scheduleSummary}`);
      }
      if (options.executionMode) {
        details.push(`Execution: ${options.executionMode}`);
      }

      const confirmed = await this.showTaskConfirmDialog({
        eyebrow: 'Assistant Task',
        title: 'Create this task?',
        message: 'Assistant wants to add this task to the workspace.',
        confirmLabel: 'Create Task',
        cancelLabel: 'Cancel',
        metaItems,
        details
      });

      if (!confirmed) {
        if (options.cancelToast !== false && window.Toast) {
          window.Toast.info('Task creation cancelled');
        }
        return false;
      }
    }

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
          description: normalizedName, // Task API uses description as the main field
          details: normalizedDescription,
          status: 'pending',
          to: String(options.assignee || '').trim() || undefined,
          assigned_node_id: String(options.assignedNodeId || '').trim() || undefined,
          input_task_ids: Array.isArray(options.inputTaskIDs) ? options.inputTaskIDs.filter(Boolean) : undefined,
          parent_task_id: String(options.parentTaskID || '').trim() || undefined,
          subtask_index: Number.isFinite(Number(options.subtaskIndex)) ? Number(options.subtaskIndex) : undefined
        })
      });

      if (!response.ok) throw new Error('Failed to create task');

      const data = await response.json();
      const createdTask = data.task || data;

      if (columnId && createdTask?.id) {
        await this.updateTaskKanbanColumn(createdTask.id, columnId);
        await this.loadTasks();
      } else {
        await this.loadTasks();
      }

      if (options.successToast !== false && window.Toast) window.Toast.success('Task created');
      return createdTask;
    } catch (error) {
      console.error('Failed to create task:', error);
      if (window.Toast) window.Toast.error('Failed to create task');
      return null;
    }
  }

  /**
   * Show add task modal
   */
  showAddTaskModal() {
    // Use existing task modal controller
    if (window.taskModalController && typeof window.taskModalController.openForCreate === 'function') {
      window.taskModalController.openForCreate(this.workspaceId, '', () => this.loadTasks());
    } else {
      // Fallback to prompt
      const name = prompt('Enter task name:');
      if (name) this.createTask(name);
    }
  }

  showAddTaskModalForAgent(encodedAgentName = '') {
    // Keep current behavior (workspace task modal), but route from per-agent section actions.
    // Future enhancement can preselect agent assignment in the task modal.
    void encodedAgentName;
    this.showAddTaskModal();
  }

  /**
   * Open a task for editing
   */
  openTask(taskId) {
    // Use existing task modal controller
    const task = this.tasks.find(t => t.id === taskId);
    if (task) {
      const statusInfo = this.getTaskStatusPresentation(task);
      if (statusInfo.isBlocked) {
        this.openTaskAssistModal(taskId);
        return;
      }
    }

    if (window.taskModalController && typeof window.taskModalController.openForEdit === 'function') {
      if (task) {
        window.taskModalController.openForEdit(task, () => this.loadTasks());
      }
    }
  }

  /**
   * Delete a task
   */
  async deleteTask(taskId) {
    if (!confirm('Are you sure you want to delete this task?')) return;

    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete task');

      if (window.Toast) window.Toast.success('Task deleted');
      await this.loadTasks();
    } catch (error) {
      console.error('Failed to delete task:', error);
      if (window.Toast) window.Toast.error('Failed to delete task');
    }
  }

  /**
   * Delete a session
   */
  async deleteSession(sessionId) {
    if (!confirm('Are you sure you want to delete this session?')) return;

    try {
      const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete session');

      if (window.Toast) window.Toast.success('Session deleted');
      await this.loadSessions();
    } catch (error) {
      console.error('Failed to delete session:', error);
      if (window.Toast) window.Toast.error('Failed to delete session');
    }
  }

  /**
   * Delete a note
   */
  async deleteNote(noteId) {
    if (!confirm('Are you sure you want to delete this note?')) return;

    try {
      const response = await fetch(`/api/notes/${encodeURIComponent(noteId)}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete note');

      if (window.Toast) window.Toast.success('Note deleted');
      await this.loadNotes();
    } catch (error) {
      console.error('Failed to delete note:', error);
      if (window.Toast) window.Toast.error('Failed to delete note');
    }
  }

  /**
   * Create a new session using the existing chat modal
   */
  createNewSession() {
    this.createSimpleSession();
  }

  createNewSessionForAgent(encodedAgentName = '') {
    let agentName = '';
    try {
      agentName = decodeURIComponent(String(encodedAgentName || '')).trim();
    } catch (_error) {
      agentName = String(encodedAgentName || '').trim();
    }

    if (agentName && window.sessionManager && typeof window.sessionManager.createSessionWithAgentInFolder === 'function') {
      window.sessionManager.createSessionWithAgentInFolder(agentName, this.workspaceId);
      return;
    }

    this.createNewSession();
  }

  /**
   * Fallback simple session creation
   */
  async createSimpleSession(openChat = true) {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          folder_id: this.workspaceId,
          title: 'Assistant'
        })
      });

      if (!response.ok) throw new Error('Failed to create session');

      const payload = await response.json();
      const session = payload?.session || payload;
      if (!session?.id) throw new Error('Invalid session response');
      if (window.Toast) window.Toast.success('Assistant session created');
      await this.loadSessions();

      // Open the session
      this.openSession(session.id, openChat);
      return session;
    } catch (error) {
      console.error('Failed to create session:', error);
      if (window.Toast) window.Toast.error('Failed to create session');
      return null;
    }
  }

  /**
   * Create session with initial message
   */
  async createSessionWithMessage(message) {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          folder_id: this.workspaceId,
          title: message.substring(0, 50)
        })
      });

      if (!response.ok) throw new Error('Failed to create session');

      const payload = await response.json();
      const session = payload?.session || payload;
      if (!session?.id) throw new Error('Invalid session response');
      await this.loadSessions();
      this.openSession(session.id);
      if (message && typeof window.sendMessageToChat === 'function') {
        setTimeout(() => window.sendMessageToChat(message), 150);
      }
      return session;
    } catch (error) {
      console.error('Failed to create session:', error);
      if (window.Toast) window.Toast.error('Failed to create chat');
      return null;
    }
  }

  /**
   * Open a session
   */
  openSession(sessionId, openChat = true) {
    // Open chat panel if available
    if (openChat && window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }
    if (window.sessionManager && typeof window.sessionManager.switchToSession === 'function') {
      window.sessionManager.switchToSession(sessionId, openChat);
    }
  }

  /**
   * Show file upload modal using hub's file modal
   */
  showFileModal() {
    // Use the hub file modal if available
    const hubFileModal = document.getElementById('hubAddFileModal');
    if (hubFileModal && typeof bootstrap !== 'undefined') {
      // Set up the workspace ID for the file upload
      window.currentWorkspaceId = this.workspaceId;
      const modal = new bootstrap.Modal(hubFileModal);
      modal.show();
    } else {
      // Fallback to file input click
      const fileInput = document.createElement('input');
      fileInput.type = 'file';
      fileInput.multiple = true;
      fileInput.onchange = () => this.uploadFiles(fileInput.files);
      fileInput.click();
    }
  }

  /**
   * Upload files
   */
  async uploadFiles(files) {
    if (!files || files.length === 0) return;

    for (const file of files) {
      const formData = new FormData();
      formData.append('file', file);
      formData.append('workspace_id', this.workspaceId);

      try {
        const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`, {
          method: 'POST',
          body: formData
        });

        if (!response.ok) throw new Error('Failed to upload file');
      } catch (error) {
        console.error('Failed to upload file:', error);
        if (window.Toast) window.Toast.error(`Failed to upload ${file.name}`);
        return;
      }
    }

    if (window.Toast) window.Toast.success('File(s) uploaded');
    await this.loadFiles();
  }

  /**
   * Show note modal using sessionManager
   */
  showNoteModal(note = null) {
    if (note) {
      // Edit existing note
      if (window.sessionManager && typeof window.sessionManager.openNoteEditorModal === 'function') {
        window.sessionManager.openNoteEditorModal(note);
      }
    } else {
      // Create new note
      if (window.sessionManager && typeof window.sessionManager.openNoteCreateModal === 'function') {
        window.sessionManager.openNoteCreateModal(this.workspaceId);
      }
    }
  }

  /**
   * Edit a note
   */
  editNote(noteId) {
    const note = this.notes.find(n => n.id === noteId);
    if (note) {
      this.showNoteModal(note);
    }
  }

  /**
   * Create a quick note from content (for quick input)
   */
  async createNote(title, content, noteId = null) {
    try {
      const url = noteId
        ? `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes/${encodeURIComponent(noteId)}`
        : `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`;

      const response = await fetch(url, {
        method: noteId ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: title, content })
      });

      if (!response.ok) throw new Error('Failed to save note');

      if (window.Toast) window.Toast.success(noteId ? 'Note updated' : 'Note created');
      await this.loadNotes();
      return true;
    } catch (error) {
      console.error('Failed to save note:', error);
      if (window.Toast) window.Toast.error('Failed to save note');
      return false;
    }
  }

  updateCopyNotesButtonState(isBusy = false) {
    const button = this.elements.copyNotesBtn;
    if (!button) return;

    const hasNotes = Array.isArray(this.notes) && this.notes.length > 0;
    button.disabled = Boolean(isBusy) || !hasNotes;
    if (isBusy) {
      button.title = 'Copying notes...';
      return;
    }
    button.title = hasNotes ? 'Copy all note contents' : 'No notes to copy';
  }

  async writeClipboardText(text) {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      await navigator.clipboard.writeText(text);
      return;
    }

    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.top = '-9999px';
    textarea.style.left = '-9999px';
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();

    const copied = document.execCommand('copy');
    document.body.removeChild(textarea);
    if (!copied) throw new Error('Copy command failed');
  }

  async copyAllNotesToClipboard() {
    if (!this.notes || this.notes.length === 0) {
      this.updateCopyNotesButtonState(false);
      if (typeof window.notifyToast === 'function') {
        window.notifyToast('No notes to copy', 'error');
      } else if (window.Toast) {
        window.Toast.error('No notes to copy');
      }
      return;
    }

    this.updateCopyNotesButtonState(true);
    try {
      const sections = await Promise.all(this.notes.map(async (note, index) => {
        const noteId = String(note?.id || '').trim();
        const title = String(note?.name || note?.title || `Note ${index + 1}`).trim() || `Note ${index + 1}`;
        let content = '';

        if (noteId) {
          try {
            const response = await fetch(`/api/notes/${encodeURIComponent(noteId)}`);
            if (response.ok) {
              const detail = await response.json();
              content = String(detail?.content || detail?.preview || '').trim();
            }
          } catch (error) {
            console.error('Failed to load note content for copy:', error);
          }
        }

        if (!content) {
          content = String(note?.content || note?.preview || '').trim();
        }

        return `# ${title}\n${content || '(empty note)'}`;
      }));

      await this.writeClipboardText(sections.join('\n\n---\n\n'));
      if (typeof window.notifyToast === 'function') {
        window.notifyToast(`Copied ${this.notes.length} note${this.notes.length === 1 ? '' : 's'} to clipboard`, 'success');
      } else if (window.Toast) {
        window.Toast.success(`Copied ${this.notes.length} note${this.notes.length === 1 ? '' : 's'} to clipboard`);
      }
    } catch (error) {
      console.error('Failed to copy notes:', error);
      if (typeof window.notifyToast === 'function') {
        window.notifyToast('Failed to copy notes', 'error');
      } else if (window.Toast) {
        window.Toast.error('Failed to copy notes');
      }
    } finally {
      this.updateCopyNotesButtonState(false);
    }
  }

  async copyCurrentTaskResult() {
    if (!this.currentTaskResultText) {
      if (window.Toast) window.Toast.info('No result to copy.');
      return;
    }

    try {
      await this.writeClipboardText(this.currentTaskResultText);
      if (window.Toast) window.Toast.success('Result copied to clipboard');
    } catch (error) {
      console.error('Failed to copy task result:', error);
      if (window.Toast) window.Toast.error('Failed to copy result');
    }
  }

  /**
   * Escape HTML to prevent XSS
   */
  escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  /**
   * Format file size
   */
  formatFileSize(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
  }
}

// Make available globally for onclick handlers
window.workspaceDetail = null;
