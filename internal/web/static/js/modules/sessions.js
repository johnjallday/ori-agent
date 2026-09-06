// Session Sidebar Module
// Handles session management, folders, tags, search, and UI interactions

const sessionManager = {
  // State
  sessions: [],
  folders: [],
  tags: [],
  activeSessionId: null,
  activeFolder: null,
  searchQuery: '',
  sortBy: 'updated_at',
  filterAgent: null,
  filterTags: [],
  isLoading: false,
  selectedSessionIds: [],
  lastSelectedIndex: null,
  folderTreeWasCollapsedOnDrag: false,
  dragHintTimer: null,
  sessionsByFolder: new Map(),
  collapsedFolderIds: new Set(),
  agentModelCache: new Map(),
  sessionTaskCounts: new Map(), // Cache of task counts per session: sessionId -> {total, pending, completed}
  editAgentOriginalName: '',
  editAgentCurrentProvider: '',
  editAgentSelectedTags: [],
  editAgentMCPServers: [],
  editAgentModalInitialized: false,
  editAgentModelOptionsLoaded: false,
  isCreatingFolder: false,
  importModeEnabled: false,
  importAllowDuplicate: false,
  importDuplicateWorkspaceId: '',
  importDuplicateWorkspaceSlug: '',
  importDuplicateWorkspaceName: '',
  importEntryPoint: 'workspace_hub_create',
  workspacePostCreateAction: '',
  // True while the open Create Workspace modal was launched from the Workspace
  // Map's Build mode, which changes only where the flow returns to (#292 FR-53).
  workspaceMapOrigin: false,

  // Create-workspace "Starting point" template currently picked in the modal.
  // Populated when the modal opens (defaults to the first/Blank template).
  workspaceTemplate: null,
  templateAgentPlan: null,
  templateAgentPlanError: '',
  templateAgentPlanRequestId: 0,
  templateAgentPlanTimer: null,
  // Saved-agent state lives in the team draft, exposed here as read-only views so
  // there is exactly one source of truth. Mutations go through the draft API
  // (addSavedAgent / removeSavedAgent / setExplicitPrimary / setSavedRoster*),
  // never by assigning to these.
  get existingAgentRoster() {
    const draft = this.ensureWorkspaceTeamDraft();
    return draft ? draft.savedRoster.agents : [];
  },
  get existingAgentRosterError() {
    const draft = this.ensureWorkspaceTeamDraft();
    return draft ? draft.savedRoster.error : '';
  },
  get existingAgentSelections() {
    const draft = this.ensureWorkspaceTeamDraft();
    return draft ? draft.savedSelections : [];
  },
  // The user's EXPLICIT primary choice, which is empty while the primary is being
  // derived. Use resolvedWorkspacePrimaryName() for the effective primary.
  get existingAgentPrimaryName() {
    const draft = this.ensureWorkspaceTeamDraft();
    return draft ? draft.explicitPrimary : '';
  },
  existingAgentRosterLoaded: false,
  existingAgentRosterLoading: false,
  // True once the user manually changes "Agent behavior", so selecting a
  // Starting point no longer overwrites their choice. Reset on modal open.
  behaviorOverridden: false,

  // Create-workspace wizard step (1 = Blueprint, 2 = Details, 3 = Team,
  // 4 = Review). Import mode remains a single-step details layout and never
  // enters Team or Review.
  wizardStep: 1,
  // Authoritative local team draft. Held here, never inferred from rendered
  // controls, and converted to request fields only at final submission.
  teamDraft: null,
  workspaceAgentSetupIndex: null,
  workspaceAgentSetupOpener: null,
  workspaceAgentSetupForm: null,
  workspaceAgentSetupCloseOptions: null,
  workspaceAgentSetupCompletion: null,
  workspaceAgentSetupFocusField: '',

  // Auto mode state
  chatAutoMode: false,
  chatLlmAvailable: false,
  chatSystemModelConfigured: false,
  autoModeSessionIds: new Set(), // Sessions that need auto-classification
  chatPendingFiles: [], // Files to attach when creating chat

  // Tab ID for multi-tab support
  tabId: null,

  // Generate or retrieve unique tab ID from sessionStorage
  generateTabId() {
    let tabId = sessionStorage.getItem('oriTabId');
    if (!tabId) {
      tabId = 'tab_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
      sessionStorage.setItem('oriTabId', tabId);
    }
    this.tabId = tabId;
    return tabId;
  },

  // Get the active session ID for this tab
  getActiveSessionId() {
    return this.activeSessionId;
  },

  // Ensure chat panel is visible when switching/creating sessions
  openChatPanelIfAvailable() {
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }
  },

  // Get headers for API requests that need session context
  getSessionHeaders() {
    const headers = {};
    if (this.activeSessionId) {
      headers['X-Session-ID'] = this.activeSessionId;
    }
    return headers;
  },

  // Debounce timer for search
  searchDebounceTimer: null,

  // Polling interval for session updates (in ms)
  pollInterval: 30000,
  pollTimer: null,

  // Virtual scroll state
  virtualScrollEnabled: false,
  itemHeight: 70,
  visibleCount: 20,
  scrollOffset: 0,

  // Context menu state
  contextMenuTarget: null,
  contextMenuType: null,

  // Initialize the session sidebar
  async init() {
    // Generate unique tab ID for multi-tab support
    this.generateTabId();

    this.loadCollapsedFolders();
    this.bindEvents();
    this.bindNoteEvents();
    this.setupKeyboardShortcuts();
    this.restoreSidebarState();
    this.initChatAgentBar();
    this.initMainTaskPanel();
    this.initScheduledTasksModal();
    if (!(await this.canHydrateWorkspaceData())) {
      this.sessions = [];
      this.folders = [];
      this.updateSessionsEmptyState();
      return;
    }
    await this.loadSessions();
    await this.loadFolders({ bootstrap: true });
    await this.loadTags();

    // Try to restore an active session. An empty workspace launcher owns its
    // inline empty state; opening Create Workspace requires an explicit click.
    const restored = await this.restoreActiveSession();
    if (!restored && this.sessions.length > 0) {
      // No saved session but sessions exist - use the first one
      // Pass false to not auto-open the chat panel on page load
      await this.switchToSession(this.sessions[0].id, false);
    }

    // Start polling for session updates
    this.startPolling();

    // Stop polling when tab is hidden, resume when visible
    document.addEventListener('visibilitychange', () => {
      if (document.hidden) {
        this.stopPolling();
      } else {
        this.startPolling();
      }
    });
  },

  async canHydrateWorkspaceData({ force = false } = {}) {
    try {
      const gate = this.onboardingGate || window.OriOnboardingGate;
      if (gate?.loadOnboardingStatus && gate?.onboardingGateDecision) {
        const status = await gate.loadOnboardingStatus({ force });
        return gate.onboardingGateDecision(status).allowWorkspaceHydration === true;
      }

      // Non-Home pages may not load the module gate. Use the same window-level
      // promise key so a later module import adopts this request rather than
      // issuing a second one.
      const key = '__oriOnboardingStatusPromise';
      if (force) delete window[key];
      if (!window[key]) {
        window[key] = Promise.resolve()
          .then(async () => {
            const response = await fetch('/api/onboarding/status');
            if (!response.ok) throw new Error(`HTTP ${response.status}`);
            const status = await response.json();
            if (typeof status?.needs_onboarding !== 'boolean') {
              throw new Error('Invalid onboarding status');
            }
            return status;
          })
          .catch(error => ({
            needs_onboarding: null,
            onboarding_status_unavailable: true,
            onboarding_status_error: String(error?.message || error || 'Unknown error').slice(
              0,
              200
            )
          }));
      }
      const status = await window[key];
      return status?.needs_onboarding === false;
    } catch (error) {
      console.error('Failed to resolve onboarding workspace gate:', error);
      return false;
    }
  },

  // Bind all event listeners
  bindEvents() {
    // New chat button - show modal
    document.getElementById('newChatBtn')?.addEventListener('click', e => {
      e.preventDefault();
      e.stopPropagation();
      this.showCreateChatModal();
    });

    // New note button - open note modal (workspace selectable)
    document.getElementById('newNoteBtn')?.addEventListener('click', e => {
      e.preventDefault();
      e.stopPropagation();
      this.openNoteCreateModal(this.activeFolder || null);
    });

    // New task button - open task modal (workspace can be selected in modal if not active)
    document.getElementById('newTaskBtn')?.addEventListener('click', e => {
      e.preventDefault();
      e.stopPropagation();
      // Open task modal - if no active folder, user can select workspace in the modal
      this.openTaskModalForWorkspace(this.activeFolder || null);
    });

    document
      .getElementById('createFirstSessionBtn')
      ?.addEventListener('click', () => this.handleEmptyStateAction());

    // Create chat modal - create button
    document
      .getElementById('createChatBtn')
      ?.addEventListener('click', () => this.handleCreateChatFromModal());

    // Toggle sidebar (from inside session sidebar)
    document
      .getElementById('toggleSessionSidebarBtn')
      ?.addEventListener('click', () => this.toggleSidebar());

    // Toggle sidebar (from navbar)
    document
      .getElementById('sessionsToggle')
      ?.addEventListener('click', () => this.toggleSidebar());

    // Search input
    const searchInput = document.getElementById('sessionSearchInput');
    searchInput?.addEventListener('input', e => this.handleSearchInput(e.target.value));

    const clearSearch = document.getElementById('clearSessionSearch');
    clearSearch?.addEventListener('click', () => this.clearSearch());

    // Sort dropdown
    document.getElementById('sortDropdownBtn')?.addEventListener('click', e => {
      e.stopPropagation();
      this.toggleDropdown('sortDropdown');
    });

    // Filter dropdown
    document.getElementById('filterDropdownBtn')?.addEventListener('click', e => {
      e.stopPropagation();
      this.toggleDropdown('filterDropdown');
    });

    // Sort options
    document.querySelectorAll('#sortDropdown .session-dropdown-item').forEach(item => {
      item.addEventListener('click', e => {
        const sort = e.target.dataset.sort;
        this.setSortOrder(sort);
        this.closeDropdowns();
      });
    });

    // Folder tree toggle
    document
      .getElementById('folderTreeToggle')
      ?.addEventListener('click', () => this.toggleFolderTree());

    // Add folder button
    document.getElementById('addFolderBtn')?.addEventListener('click', e => {
      e.stopPropagation();
      this.showAddWorkspaceModal();
    });

    // Create folder button
    document
      .getElementById('createFolderBtn')
      ?.addEventListener('click', () => this.createFolder());

    // Wizard navigation (Choose Blueprint → Details → Review).
    document
      .getElementById('wizardNextBtn')
      ?.addEventListener('click', () => this.goToWizardStep(this.wizardStep + 1));
    document
      .getElementById('wizardBackBtn')
      ?.addEventListener('click', () => this.goToWizardStep(this.wizardStep - 1));
    document
      .getElementById('wizardEditBlueprintBtn')
      ?.addEventListener('click', () => this.goToWizardStep(1));
    // Double-clicking a blueprint still skips only to the Details step.
    document
      .getElementById('addFolderModal')
      ?.addEventListener('workspace-template-advance', () => {
        if (!this.importModeEnabled) this.goToWizardStep(2);
      });

    // Name field: live folder-slug preview, clear the inline error on edit, and
    // Enter to advance, or to create once the wizard is on Review.
    const workspaceNameInput = document.getElementById('folderNameInput');
    workspaceNameInput?.addEventListener('input', () => {
      this.clearWorkspaceNameError();
      this.updateWorkspaceNameHint();
      // The final CTA names the workspace, so it tracks the name as it is typed.
      this.refreshWorkspaceCreateCta();
    });
    workspaceNameInput?.addEventListener('keydown', event => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      if (this.importModeEnabled || this.wizardStep === this.wizardStepCount) {
        this.createFolder();
      } else {
        this.goToWizardStep(this.wizardStep + 1);
      }
    });

    // Agent behavior (formerly "Workspace preset"): a manual change marks the
    // value as overridden so picking a Template won't clobber it.
    document.getElementById('folderPresetSelect')?.addEventListener('change', () => {
      this.behaviorOverridden = true;
      this.updateBehaviorHint();
    });

    // Unified Template picker selection (dispatched by ProjectTemplateCard):
    // prefill name/description (never clobbering typed input) and apply the
    // template's Agent-behavior default.
    document
      .getElementById('addFolderModal')
      ?.addEventListener('workspace-template-selected', event => {
        const template = event?.detail?.template || null;
        this.closeWorkspaceAgentSetup({ restoreFocus: false, silent: true });
        this.workspaceTemplate = template;
        this.prefillTemplateValue(
          document.getElementById('folderNameInput'),
          template?.name || '',
          'autofillName'
        );
        this.prefillTemplateValue(
          document.getElementById('folderDescriptionInput'),
          template?.description || '',
          'autofillDescription'
        );
        this.updateWorkspaceNameHint();
        this.applyTemplateBehavior(template);
        this.updateWizardRecap(template);
        void this.refreshTemplateAgentPlan();
        this.refreshWorkspaceReview();
      });

    document.getElementById('templateAgentReviewToggle')?.addEventListener('change', event => {
      this.syncIncludeBlueprintTeam(Boolean(event?.currentTarget?.checked));
      this.updateTemplateAgentReviewDisabledState();
      this.refreshWorkspaceReview();
      // Excluding the blueprint team can hand the primary slot to a saved agent.
      this.announceResolvedPrimary();
    });

    document.getElementById('assistantProgramCreateName')?.addEventListener('input', event => {
      this.updateAssistantProgramHireDraft({ name: event.currentTarget?.value || '' });
    });
    document.getElementById('assistantProgramCreateProvider')?.addEventListener('change', event => {
      this.updateAssistantProgramHireDraft({
        provider: event.currentTarget?.value || '',
        model: ''
      });
    });
    document.getElementById('assistantProgramCreateModel')?.addEventListener('change', event => {
      this.updateAssistantProgramHireDraft({ model: event.currentTarget?.value || '' });
    });

    document.getElementById('addExistingAgentBtn')?.addEventListener('click', () => {
      void this.openExistingAgentRoster();
    });
    document.getElementById('existingAgentRosterSearch')?.addEventListener('input', () => {
      this.renderExistingAgentRoster();
    });
    document.getElementById('existingAgentRosterList')?.addEventListener('click', event => {
      const button = event.target.closest('[data-existing-agent-add]');
      if (button) this.addExistingAgent(button.dataset.existingAgentAdd || '');
    });
    // Roster actions are delegated: the list re-renders on every draft change,
    // so per-row listeners would have to be rebound each time.
    document.getElementById('workspaceTeamBatchActions')?.addEventListener('click', event => {
      if (event.target.closest('[data-team-accept-all]')) this.acceptAllWorkspaceAgentSetups();
      if (event.target.closest('[data-team-undo-batch]')) this.undoBatchWorkspaceAgentSetups();
    });
    document.getElementById('workspaceTeamRoster')?.addEventListener('click', event => {
      const retry = event.target.closest('[data-team-agent-retry]');
      if (retry) {
        const index = Number.parseInt(retry.dataset.teamAgentRetry || '', 10);
        if (Number.isInteger(index)) void this.retryWorkspaceAgentCreation(index);
        return;
      }
      const reset = event.target.closest('[data-team-agent-reset]');
      if (reset) {
        const index = Number.parseInt(reset.dataset.teamAgentReset || '', 10);
        if (Number.isInteger(index)) this.resetWorkspaceAgentSetup(index);
        return;
      }
      const setup = event.target.closest('[data-team-agent-setup]');
      if (setup) {
        const index = Number.parseInt(setup.dataset.teamAgentSetup || '', 10);
        if (Number.isInteger(index)) this.openWorkspaceAgentSetup(index, setup);
        return;
      }
      const remove = event.target.closest('[data-existing-agent-remove]');
      if (remove) this.removeExistingAgent(remove.dataset.existingAgentRemove || '');
      const primary = event.target.closest('[data-existing-agent-primary]');
      if (primary) this.requestExistingAgentPrimary(primary.dataset.existingAgentPrimary || '');
    });
    const addAgentDraftModal = document.getElementById('addAgentModal');
    const submitWorkspaceAgentDraft = event => {
      if (addAgentDraftModal?.dataset.agentCreateMode !== 'workspace-draft') return;
      event.preventDefault();
      event.stopImmediatePropagation();
      this.saveWorkspaceAgentSetup();
    };
    // Capture before agents.js's standalone handlers so draft mode can never
    // fall through to POST /api/agents.
    document
      .getElementById('createAgentBtn')
      ?.addEventListener('click', submitWorkspaceAgentDraft, true);
    document
      .getElementById('addAgentForm')
      ?.addEventListener('submit', submitWorkspaceAgentDraft, true);
    addAgentDraftModal?.addEventListener(
      'click',
      event => {
        if (
          addAgentDraftModal.dataset.agentCreateMode !== 'workspace-draft' ||
          !event.target.closest('[data-bs-dismiss="modal"]')
        )
          return;
        event.preventDefault();
        event.stopImmediatePropagation();
        this.closeWorkspaceAgentSetup({ announce: true });
      },
      true
    );
    addAgentDraftModal?.addEventListener('hidden.bs.modal', () => {
      if (addAgentDraftModal.dataset.agentCreateMode === 'workspace-draft') {
        this.finishWorkspaceAgentSetupModal();
      }
    });
    document.getElementById('workspaceReviewSummary')?.addEventListener('click', event => {
      const edit = event.target.closest('[data-wizard-edit-step]');
      if (edit) this.goToWizardStep(Number(edit.dataset.wizardEditStep));
    });

    document.getElementById('projectTemplatePathInput')?.addEventListener('input', () => {
      this.scheduleTemplateAgentPlanRefresh();
    });

    const importToggle = document.getElementById('folderImportToggle');
    importToggle?.addEventListener('change', event => {
      const checked = Boolean(event?.currentTarget?.checked);
      this.setImportModeEnabled(checked);
      if (!checked) {
        this.importAllowDuplicate = false;
      }
    });

    const importPathInput = document.getElementById('folderImportPathInput');
    importPathInput?.addEventListener('input', event => {
      this.importAllowDuplicate = false;
      this.clearImportDuplicateWarning();
      this.prefillWorkspaceNameFromImportPath(event?.target?.value || '');
    });
    importPathInput?.addEventListener('blur', event => {
      void this.checkImportDuplicate(event?.target?.value || '');
    });

    const importBrowseBtn = document.getElementById('folderImportBrowseBtn');
    importBrowseBtn?.addEventListener('click', () => {
      void this.browseImportFolderPath();
    });

    const openExistingBtn = document.getElementById('folderImportOpenExistingBtn');
    openExistingBtn?.addEventListener('click', async () => {
      if (!this.importDuplicateWorkspaceId) {
        return;
      }
      this.emitImportDuplicateActionTelemetry('suggestion_accepted', importPathInput?.value || '');
      try {
        const postCreate = await this.applyWorkspacePostCreateAction(
          this.importDuplicateWorkspaceId,
          this.importDuplicateWorkspaceSlug
        );
        const destination = postCreate.destination;
        if (!destination) throw new Error('Workspace slug is unavailable; refresh and try again.');
        const modal = bootstrap.Modal.getInstance(addFolderModal);
        modal?.hide();
        this.resetAddWorkspaceModalForm();
        window.location.href = destination;
      } catch (error) {
        this.showToast(
          error && error.message ? error.message : 'Could not use this workspace as Personal HQ',
          'error'
        );
      }
    });

    const proceedDuplicateBtn = document.getElementById('folderImportProceedDuplicateBtn');
    proceedDuplicateBtn?.addEventListener('click', () => {
      this.importAllowDuplicate = true;
      this.emitImportDuplicateActionTelemetry('override_confirmed', importPathInput?.value || '');
      this.showToast('Duplicate override enabled. Click Import Folder to continue.', 'warning');
    });

    // Folder color options
    document.querySelectorAll('.folder-color-btn').forEach(btn => {
      btn.addEventListener('click', e => {
        document.querySelectorAll('.folder-color-btn').forEach(b => b.classList.remove('active'));
        e.target.classList.add('active');
        this.updateBehaviorHint();
      });
    });

    // Group/Color/Tags live inside the Advanced disclosure now; refresh the
    // collapsed summary hint when they change or when Advanced is toggled shut.
    document.getElementById('folderParentSelect')?.addEventListener('change', () => {
      this.updateBehaviorHint();
    });
    document.getElementById('folderAdvancedDisclosure')?.addEventListener('toggle', () => {
      this.updateBehaviorHint();
    });

    const addFolderModal = document.getElementById('addFolderModal');
    // Resuming after the sibling Agent modal is not a fresh Create Workspace
    // open. Stop the normal show listeners (catalog reload, form reset, review
    // reset) without cancelling Bootstrap's show itself, or they would erase
    // the draft we suspended the modal to preserve.
    addFolderModal?.addEventListener(
      'show.bs.modal',
      event => {
        if (addFolderModal.dataset.resumingFromAgentSetup !== 'true') return;
        delete addFolderModal.dataset.resumingFromAgentSetup;
        event.stopImmediatePropagation();
      },
      true
    );
    addFolderModal?.addEventListener('show.bs.modal', event => {
      const trigger = event?.relatedTarget || null;
      const pendingImportMode = String(addFolderModal.dataset.pendingImportMode || '') === 'true';
      const triggerImportMode = String(trigger?.dataset?.workspaceImportMode || '') === 'true';
      const importMode = trigger ? triggerImportMode : pendingImportMode;
      const entryPoint = String(
        trigger?.dataset?.workspaceEntryPoint || addFolderModal.dataset.pendingEntryPoint || ''
      ).trim();
      const postCreateAction = String(
        trigger?.dataset?.workspacePostCreateAction ||
          addFolderModal.dataset.pendingPostCreateAction ||
          ''
      ).trim();

      const pendingBlueprint = String(addFolderModal.dataset.pendingBlueprint || '').trim();

      this.resetAddWorkspaceModalForm({ preserveAskOri: true });
      this.importEntryPoint =
        entryPoint || (importMode ? 'workspace_hub_import' : 'workspace_hub_create');
      this.workspacePostCreateAction = postCreateAction;
      this.workspaceMapOrigin = String(addFolderModal.dataset.pendingMapOrigin || '') === 'true';
      if (importMode) {
        this.setImportModeEnabled(true);
      } else if (pendingBlueprint) {
        this.preselectBlueprintCard(pendingBlueprint);
      }

      delete addFolderModal.dataset.pendingImportMode;
      delete addFolderModal.dataset.pendingEntryPoint;
      delete addFolderModal.dataset.pendingPostCreateAction;
      delete addFolderModal.dataset.pendingBlueprint;
      delete addFolderModal.dataset.pendingMapOrigin;
    });

    // Closing or cancelling discards the team draft immediately rather than
    // leaving it to be overwritten on the next open, and invalidates any plan
    // request still in flight so a late response cannot repopulate a closed
    // wizard. Nothing was persisted, so nothing needs undoing (FR13).
    addFolderModal?.addEventListener('hidden.bs.modal', () => {
      // Agent setup reuses the sibling Create New Agent modal. Suspending this
      // modal is navigation inside one draft, not cancellation of the draft.
      if (addFolderModal.dataset.suspendedForAgentSetup === 'true') return;
      this.discardWorkspaceTeamDraft();
      // Closing without creating leaves the map exactly as it was: no workspace,
      // no position record, no lingering placement mode (#292 FR-54). A pending
      // coordinate that a successful create already consumed is gone by now, so
      // this only ever clears an abandoned one.
      this.workspaceMapOrigin = false;
      if (window.OriWorkspaceMap && typeof window.OriWorkspaceMap.cancelBuild === 'function') {
        window.OriWorkspaceMap.cancelBuild();
      }
    });

    // Close dropdowns when clicking outside
    document.addEventListener('click', () => this.closeDropdowns());

    // Context menu handlers
    document.addEventListener('click', () => this.hideContextMenus());

    // Session context menu actions
    document.querySelectorAll('#sessionContextMenu .session-context-item').forEach(item => {
      item.addEventListener('click', e => {
        const action = e.currentTarget.dataset.action;
        this.handleSessionContextAction(action);
      });
    });

    // Folder context menu actions
    document.querySelectorAll('#folderContextMenu .session-context-item').forEach(item => {
      item.addEventListener('click', e => {
        e.stopPropagation();
        const action = e.currentTarget.dataset.action;
        this.handleFolderContextAction(action);
      });
    });

    // Resize handle
    this.setupResizeHandle();

    // Session list scroll (for virtual scroll)
    document
      .getElementById('sessionListContainer')
      ?.addEventListener('scroll', () => this.handleScroll());

    // Session files panel toggle
    document.getElementById('sessionFilesToggle')?.addEventListener('click', () => {
      const content = document.getElementById('sessionFilesContent');
      const icon = document.querySelector('#sessionFilesToggle .folder-expand-icon');
      if (content && icon) {
        content.classList.toggle('collapsed');
        icon.classList.toggle('expanded');
      }
    });

    // Open folder button (uses FileManager if available)
    document.getElementById('openFolderBtn')?.addEventListener('click', () => {
      if (window.sessionFileManager) {
        window.sessionFileManager.openFolder();
      }
    });

    // Chat mode toggle - Manual
    document.getElementById('chatConfigModeManual')?.addEventListener('change', function () {
      if (this.checked) {
        sessionManager.handleChatModeChange('manual');
      }
    });

    // Chat mode toggle - Auto
    document.getElementById('chatConfigModeAuto')?.addEventListener('change', async function () {
      if (this.checked) {
        await sessionManager.checkChatLlmAvailability();
        sessionManager.handleChatModeChange('auto');
      }
    });
  },

  // Setup keyboard shortcuts
  setupKeyboardShortcuts() {
    document.addEventListener('keydown', e => {
      // Ctrl/Cmd + N - New session
      if ((e.ctrlKey || e.metaKey) && e.key === 'n') {
        e.preventDefault();
        this.createNewSession();
      }

      // Ctrl/Cmd + K - Focus search
      if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
        e.preventDefault();
        this.focusSearch();
      }

      // Escape - Close context menus and dropdowns
      if (e.key === 'Escape') {
        this.hideContextMenus();
        this.closeDropdowns();
      }
    });
  },

  // Load sessions from API
  // Store for note search results
  noteSearchResults: [],

  async loadSessions() {
    this.isLoading = true;
    this.renderLoadingState();

    try {
      const params = new URLSearchParams();
      if (this.searchQuery) params.set('query', this.searchQuery);
      if (this.sortBy) params.set('sort', this.sortBy);
      if (this.filterAgent) params.set('agent', this.filterAgent);
      if (this.activeFolder) params.set('folder', this.activeFolder);
      if (this.filterTags.length > 0) params.set('tags', this.filterTags.join(','));

      const response = await fetch(`/api/sessions?${params.toString()}`);
      if (!response.ok) throw new Error('Failed to load sessions');

      const data = await response.json();
      this.sessions = data.sessions || [];
      this.reconcileSelection();

      // Also search notes if there's a search query
      if (this.searchQuery && this.searchQuery.trim()) {
        await this.searchNotes(this.searchQuery);
      } else {
        this.noteSearchResults = [];
      }

      this.renderSessions();

      // Load task counts for all sessions (async, updates badges when ready)
      this.loadSessionTaskCounts();
    } catch (error) {
      console.error('Failed to load sessions:', error);
      this.sessions = [];
      this.noteSearchResults = [];
      this.clearSelection();
      this.renderEmptyState();
    } finally {
      this.isLoading = false;
    }
  },

  // Load task counts for all visible sessions (from workspace orchestration API)
  async loadSessionTaskCounts() {
    if (this.sessions.length === 0) return;

    // Group sessions by workspace to avoid duplicate API calls
    const workspaceStats = new Map();
    const workspaceIds = [...new Set(this.sessions.map(s => s.folder_id).filter(Boolean))];

    // Fetch stats for each unique workspace
    const fetchPromises = workspaceIds.map(async workspaceId => {
      try {
        const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
        if (response.ok) {
          const data = await response.json();
          workspaceStats.set(workspaceId, data.stats || { total: 0, pending: 0, completed: 0 });
        }
      } catch (error) {
        console.error(`Failed to load task counts for workspace ${workspaceId}:`, error);
      }
    });

    await Promise.all(fetchPromises);

    // Apply workspace stats to sessions
    this.sessions.forEach(session => {
      if (session.folder_id) {
        const stats = workspaceStats.get(session.folder_id);
        if (stats) {
          this.sessionTaskCounts.set(session.id, stats);
        }
      }
    });

    this.updateSessionTaskBadges();
  },

  // Update task count badges on session items
  updateSessionTaskBadges() {
    document.querySelectorAll('.session-item').forEach(item => {
      const sessionId = item.dataset.sessionId;
      const counts = this.sessionTaskCounts.get(sessionId);

      let badge = item.querySelector('.session-task-count-badge');

      if (!counts || counts.total === 0) {
        // Remove badge if no tasks
        if (badge) badge.remove();
        return;
      }

      // Create badge if it doesn't exist
      if (!badge) {
        badge = document.createElement('span');
        badge.className = 'session-task-count-badge';
        const header = item.querySelector('.session-item-header');
        if (header) {
          // Insert before time
          const timeSpan = header.querySelector('.session-time');
          header.insertBefore(badge, timeSpan);
        }
      }

      // Update badge content and style
      if (counts.pending > 0) {
        badge.textContent = counts.pending;
        badge.classList.remove('complete');
        badge.title = `${counts.pending} pending task${counts.pending > 1 ? 's' : ''}`;
      } else {
        badge.textContent = '✓';
        badge.classList.add('complete');
        badge.title = `All ${counts.completed} tasks complete`;
      }
    });
  },

  // Search notes
  async searchNotes(query) {
    try {
      const response = await fetch(`/api/notes/search?q=${encodeURIComponent(query)}`);
      if (!response.ok) throw new Error('Failed to search notes');
      const data = await response.json();
      this.noteSearchResults = data.notes || [];
    } catch (error) {
      console.error('Failed to search notes:', error);
      this.noteSearchResults = [];
    }
  },

  // Load workspaces (folders) from API
  async loadFolders({ bootstrap = false } = {}) {
    if (!(await this.canHydrateWorkspaceData())) {
      this.folders = [];
      this.updateSessionsEmptyState();
      return;
    }
    try {
      const gate = this.onboardingGate || window.OriOnboardingGate;
      let data;
      if (bootstrap && gate?.loadInitialWorkspaceTree) {
        data = await gate.loadInitialWorkspaceTree();
      } else if (bootstrap) {
        // sessions.js is a classic deferred script and can reach DOMContentLoaded
        // before the Home ES module publishes its helper. Adopt the same global
        // promise key so whichever side starts first still owns one request.
        const key = '__oriInitialWorkspaceTreePromise';
        if (!window[key]) {
          window[key] = Promise.resolve()
            .then(async () => {
              const response = await fetch('/api/workspaces?tree=true');
              if (!response.ok) throw new Error('Failed to load workspaces');
              const payload = await response.json();
              if (!payload || !Array.isArray(payload.folders)) {
                throw new Error('Workspace tree response is invalid');
              }
              return payload;
            })
            .catch(error => {
              delete window[key];
              throw error;
            });
        }
        data = await window[key];
      } else {
        const response = await fetch('/api/workspaces?tree=true');
        if (!response.ok) throw new Error('Failed to load workspaces');
        data = await response.json();
      }
      this.folders = data.folders || [];
      this.updateSessionsEmptyState();

      // Load notes for all folders
      await this.loadAllFolderNotes(this.folders);

      // Load tasks for all workspaces
      await this.loadAllWorkspaceTasks(this.folders);

      // Load scheduled tasks for all workspaces
      await this.loadAllWorkspaceScheduledTasks(this.folders);

      this.renderFolderTree();
    } catch (error) {
      console.error('Failed to load folders:', error);
      this.folders = [];
      this.updateSessionsEmptyState();
    }
  },

  // Load notes for all folders recursively
  async loadAllFolderNotes(folders) {
    for (const folder of folders) {
      await this.loadFolderNotes(folder.id);
      if (folder.children && folder.children.length > 0) {
        await this.loadAllFolderNotes(folder.children);
      }
    }
  },

  // Load all tags from API. The session filter list stays sessions-only;
  // the API returns {name, usage_count} objects, which we flatten to names.
  async loadTags() {
    try {
      const response = await fetch('/api/tags');
      if (!response.ok) throw new Error('Failed to load tags');

      const data = await response.json();
      this.tags = (data.tags || [])
        .map(tag => (typeof tag === 'string' ? tag : tag?.name))
        .filter(Boolean);
      this.renderTagFilters();
    } catch (error) {
      console.error('Failed to load tags:', error);
      this.tags = [];
    }
  },

  // Render sessions list
  renderSessions() {
    const container = document.getElementById('sessionsList');
    const loadingState = document.getElementById('sessionsLoading');

    if (!container) return;

    if (loadingState) loadingState.style.display = 'none';

    const hasNotes = this.noteSearchResults.length > 0;
    const hasSessions = this.sessions.length > 0;
    const hasWorkspaces = this.folders.length > 0;
    const shouldShowEmpty = !hasSessions && !hasNotes;

    if (shouldShowEmpty) {
      container.innerHTML = '';
    }

    this.updateSessionsEmptyState();

    if (shouldShowEmpty && !hasWorkspaces) {
      return;
    }

    const folderGroups = new Map();
    this.sessions.forEach(session => {
      const folderId = session.folder_id || '';
      if (!folderGroups.has(folderId)) {
        folderGroups.set(folderId, []);
      }
      folderGroups.get(folderId).push(session);
    });
    this.sessionsByFolder = folderGroups;

    const rootSessions = folderGroups.get('') || [];

    // Render note search results first (if searching)
    const noteResultsHtml = this.searchQuery && hasNotes ? this.renderNoteSearchResults() : '';

    // Render root sessions with "No Folder" header
    container.innerHTML = `
      ${noteResultsHtml}
      ${rootSessions.length > 0 ? this.renderRootHeader() : ''}
      ${rootSessions.map(session => this.renderSessionItem(session)).join('')}
    `;

    container.onclick = e => {
      if (e.target === container) {
        this.clearSelection();
      }
    };

    this.bindSessionItemEvents(container);
    this.renderFolderTree();
  },

  // Render note search results
  renderNoteSearchResults() {
    if (this.noteSearchResults.length === 0) return '';

    return `
      <div class="search-notes-section">
        <div class="search-notes-header">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13"/>
          </svg>
          <span>Notes (${this.noteSearchResults.length})</span>
        </div>
        <div class="search-notes-list">
          ${this.noteSearchResults
            .map(
              note => `
            <div class="search-note-result" data-note-id="${note.id}">
              <svg class="search-note-result-icon" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13"/>
              </svg>
              <div class="search-note-result-content">
                <div class="search-note-result-title">${this.escapeHtml(note.name)}</div>
                ${note.folder_name ? `<div class="search-note-result-folder">${this.escapeHtml(note.folder_name)}</div>` : ''}
                ${note.snippets && note.snippets.length > 0 ? `<div class="search-note-result-snippet">${note.snippets[0]}</div>` : ''}
              </div>
            </div>
          `
            )
            .join('')}
        </div>
      </div>
    `;
  },

  // Render a single session item
  renderSessionItem(session) {
    const isActive = session.id === this.activeSessionId;
    const isSelected = this.selectedSessionIds.includes(session.id);
    const timeAgo = this.formatTimeAgo(session.updated_at);
    const preview = session.preview || 'No messages yet';
    const tags = session.tags || [];
    const agentName = session.agent_name || '';
    const modeLabel = agentName || 'Assistant';
    const badgeTitle = agentName ? `Direct agent chat: ${agentName}` : 'Assistant session';

    return `
      <div class="session-item ${isActive ? 'active' : ''} ${isSelected ? 'selected' : ''}" data-session-id="${session.id}">
        <div class="session-item-header">
          <svg class="session-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2Z"/>
          </svg>
          <span class="session-title">${this.escapeHtml(session.title || 'New Session')}</span>
          <span class="session-agent-badge" title="${this.escapeHtml(badgeTitle)}">${this.escapeHtml(modeLabel)}</span>
          <span class="session-time">${timeAgo}</span>
        </div>
        <div class="session-item-footer">
          <span class="session-preview">${this.escapeHtml(preview)}</span>
          ${
            tags.length > 0
              ? `
            <div class="session-tags">
              ${tags
                .slice(0, 3)
                .map(
                  tag => `
                <span class="session-tag" data-color="${this.getTagColor(tag)}">${this.escapeHtml(tag)}</span>
              `
                )
                .join('')}
              ${tags.length > 3 ? `<span class="session-tag" data-color="0">+${tags.length - 3}</span>` : ''}
            </div>
          `
              : ''
          }
        </div>
      </div>
    `;
  },

  // Render folder tree
  renderFolderTree() {
    const container = document.getElementById('folderTree');
    if (!container) return;

    if (this.folders.length === 0) {
      container.innerHTML = '<div class="text-center text-muted small py-2">No workspaces</div>';
      return;
    }

    container.innerHTML = this.renderFolderItems(this.folders);

    // Bind folder events
    container.querySelectorAll('.folder-item').forEach(item => {
      const folderId = item.dataset.folderId;

      // Click to collapse/expand folder sessions
      item.addEventListener('click', e => {
        e.stopPropagation();
        this.toggleFolderSessions(folderId);
      });

      // Right-click context menu
      item.addEventListener('contextmenu', e => {
        e.preventDefault();
        this.showFolderContextMenu(e, folderId);
      });

      // Drop target
      item.addEventListener('dragover', e => {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        item.classList.add('drag-over');
      });

      item.addEventListener('dragenter', () => {
        item.classList.add('drag-over');
      });

      item.addEventListener('dragleave', () => {
        item.classList.remove('drag-over');
      });

      item.addEventListener('drop', async e => {
        e.preventDefault();
        e.stopPropagation();
        item.classList.remove('drag-over');

        // Check if files are being dropped from OS
        const files = e.dataTransfer.files;
        if (files && files.length > 0) {
          const folderName = item.querySelector('.folder-name')?.textContent?.trim();
          for (const file of files) {
            await this.createFileReferenceNote(folderId, file);
          }
          item.classList.add('drop-success');
          setTimeout(() => item.classList.remove('drop-success'), 700);
          const fileText =
            files.length === 1 ? 'file reference' : `${files.length} file references`;
          this.showToast(`Created ${fileText} in ${folderName}`, 'success');
          return;
        }

        // Check if it's a note being dropped
        const noteId = e.dataTransfer.getData('application/x-ori-note-id');
        const sourceFolderId = e.dataTransfer.getData('application/x-ori-note-folder');
        if (noteId && sourceFolderId !== folderId) {
          const moved = await this.moveNoteToFolder(noteId, folderId);
          if (moved) {
            item.classList.add('drop-success');
            setTimeout(() => item.classList.remove('drop-success'), 700);
            const folderName = item.querySelector('.folder-name')?.textContent?.trim();
            this.showToast(`Note moved to ${folderName}`, 'success');
          }
          return;
        }

        // Otherwise check for sessions
        const sessionIds = this.getDraggedSessionIds(e);
        if (sessionIds.length > 0) {
          const moved = await this.moveSessionsToFolder(sessionIds, folderId);
          if (moved) {
            item.classList.add('drop-success');
            setTimeout(() => item.classList.remove('drop-success'), 700);
            const folderName = item.querySelector('.folder-name')?.textContent?.trim();
            this.flashDragHint(this.formatMoveHint(sessionIds.length, folderName));
          }
        }
      });
    });

    // Toggle nested sessions visibility
    container.querySelectorAll('.folder-collapse-btn').forEach(button => {
      const folderId = button.dataset.folderId;
      button.addEventListener('click', e => {
        e.stopPropagation();
        this.toggleFolderSessions(folderId);
      });
    });

    this.bindSessionItemEvents(container);

    // Bind task section events
    container.querySelectorAll('.folder-tasks-header').forEach(header => {
      header.addEventListener('click', e => {
        if (e.target.closest('.folder-tasks-add-btn')) return; // Don't open on add click
        e.stopPropagation();
        const workspaceId = header.dataset.workspaceId;
        this.openWorkspaceTaskPanel(workspaceId);
      });
    });

    container.querySelectorAll('.folder-tasks-add-btn').forEach(btn => {
      btn.addEventListener('click', e => {
        e.stopPropagation();
        const workspaceId = btn.dataset.workspaceId;
        this.openTaskModalForWorkspace(workspaceId);
      });
    });

    // Bind scheduled tasks section events
    container.querySelectorAll('.folder-schedules-header').forEach(header => {
      header.addEventListener('click', e => {
        e.stopPropagation();
        const workspaceId = header.dataset.workspaceId;
        this.openScheduledTasksPanel(workspaceId);
      });
    });
  },

  // Render folder items recursively
  renderFolderItems(folders, depth = 0) {
    return folders
      .map(folder => {
        const isActive = folder.id === this.activeFolder;
        const colorStyle = folder.color ? `color: ${folder.color};` : '';
        const children = folder.children || [];
        const folderSessions = this.sessionsByFolder?.get(folder.id) || [];
        const isCollapsed = this.collapsedFolderIds.has(folder.id);

        const hasAccent = Boolean(folder.color);
        const accentStyles = hasAccent
          ? `data-has-accent="true" style="--folder-accent-bg: ${this.hexToRgba(folder.color, 0.12)}; --folder-accent-bg-hover: ${this.hexToRgba(folder.color, 0.18)}; --folder-accent-border: ${this.hexToRgba(folder.color, 0.35)};"`
          : ``;
        // Get notes for this folder
        const folderNotes = this.notesByFolder?.get(folder.id) || [];
        // Get tasks for this workspace
        const workspaceTasks = this.tasksByWorkspace?.get(folder.id) || [];
        const taskCount = workspaceTasks.length;
        const hasContent = folderSessions.length > 0 || folderNotes.length > 0;

        // Tasks section - clickable header that opens the task modal
        const tasksHtml =
          taskCount > 0
            ? `
          <div class="folder-tasks-wrapper ${isCollapsed ? 'collapsed' : ''}">
            <div class="folder-tasks-header" data-workspace-id="${folder.id}">
              <svg class="folder-tasks-header-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5A2,2 0 0,0 19,3M19,19H5V5H19V19M17,17H7V7H17V17M9,9V15H15V9H9Z"/>
              </svg>
              <span class="folder-tasks-header-text">Tasks</span>
              <span class="folder-tasks-count">${taskCount}</span>
              <button class="folder-tasks-add-btn" data-workspace-id="${folder.id}" title="Add task">
                <svg viewBox="0 0 24 24" fill="currentColor" width="14" height="14">
                  <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
                </svg>
              </button>
            </div>
          </div>
        `
            : '';

        // Get scheduled tasks for this workspace
        const workspaceScheduledTasks = this.scheduledTasksByWorkspace?.get(folder.id) || [];
        const scheduledTaskCount = workspaceScheduledTasks.length;
        const enabledScheduleCount = workspaceScheduledTasks.filter(st => st.enabled).length;

        // Schedules section - clickable header that opens the scheduled tasks panel
        const schedulesHtml =
          scheduledTaskCount > 0
            ? `
          <div class="folder-schedules-wrapper ${isCollapsed ? 'collapsed' : ''}">
            <div class="folder-schedules-header" data-workspace-id="${folder.id}">
              <svg class="folder-schedules-header-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
              </svg>
              <span class="folder-schedules-header-text">Schedules</span>
              <span class="folder-schedules-count">${enabledScheduleCount}/${scheduledTaskCount}</span>
            </div>
          </div>
        `
            : '';

        const sessionsHtml =
          folderSessions.length > 0
            ? `
          <div class="folder-sessions ${isCollapsed ? 'collapsed' : ''}" ${accentStyles}>
            ${folderSessions.map(session => this.renderSessionItem(session)).join('')}
          </div>
        `
            : '';

        const notesHtml =
          folderNotes.length > 0
            ? `
          <div class="folder-notes-section ${isCollapsed ? 'collapsed' : ''}">
            ${folderNotes
              .map(
                note => `
              <div class="folder-note-item" data-note-id="${note.id}" data-folder-id="${folder.id}" draggable="true">
                <svg class="folder-note-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13"/>
                </svg>
                <span class="folder-note-name">${this.escapeHtml(note.name)}</span>
              </div>
            `
              )
              .join('')}
          </div>
        `
            : '';

        const folderTitle = folder.description ? this.escapeHtml(folder.description) : folder.name;
        return `
        <div class="folder-item ${isActive ? 'active' : ''} ${isCollapsed ? 'collapsed' : ''}" data-folder-id="${folder.id}" style="padding-left: ${8 + depth * 12}px;" title="${folderTitle}">
          ${
            hasContent
              ? `
            <button class="folder-collapse-btn" data-folder-id="${folder.id}" title="${isCollapsed ? 'Show content' : 'Hide content'}">
              <svg viewBox="0 0 24 24" fill="currentColor">
                <path d="M8.59,16.58L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.58Z"/>
              </svg>
            </button>
          `
              : '<span class="folder-collapse-spacer"></span>'
          }
          <svg class="folder-icon" viewBox="0 0 24 24" fill="currentColor" style="${colorStyle}">
            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
          </svg>
          <span class="folder-name">${this.escapeHtml(folder.name)}</span>
          ${folder.session_count > 0 ? `<span class="folder-count">${folder.session_count}</span>` : ''}
        </div>
        ${notesHtml}
        ${sessionsHtml}
        ${tasksHtml}
        ${schedulesHtml}
        ${children.length > 0 ? `<div class="folder-children">${this.renderFolderItems(children, depth + 1)}</div>` : ''}
      `;
      })
      .join('');
  },

  // Bind events for session items in a container
  bindSessionItemEvents(container) {
    container.querySelectorAll('.session-item').forEach(item => {
      const sessionId = item.dataset.sessionId;

      // Click to switch session or multi-select
      item.addEventListener('click', e => this.handleSessionClick(e, sessionId));

      // Right-click context menu
      item.addEventListener('contextmenu', e => {
        e.preventDefault();
        if (!this.selectedSessionIds.includes(sessionId)) {
          this.setSelection([sessionId], this.getSessionIndex(sessionId));
        }
        this.showSessionContextMenu(e, sessionId);
      });

      // Drag and drop
      item.setAttribute('draggable', 'true');
      item.addEventListener('dragstart', e => this.handleDragStart(e, sessionId));
      item.addEventListener('dragend', () => this.handleDragEnd());
    });
  },

  // Render "No Folder" header for root sessions
  renderRootHeader() {
    return `
      <div class="session-folder-header">
        <svg class="session-folder-icon" viewBox="0 0 24 24" fill="currentColor">
          <path d="M19,20H5A2,2 0 0,1 3,18V6A2,2 0 0,1 5,4H9L11,6H19A2,2 0 0,1 21,8V18A2,2 0 0,1 19,20M5,18H19V10H5V18M5,8H9L7,6H5V8Z"/>
        </svg>
        <span class="session-folder-name">No Workspace</span>
      </div>
    `;
  },

  // Toggle visibility of sessions under a folder
  toggleFolderSessions(folderId) {
    if (!folderId) return;
    if (this.collapsedFolderIds.has(folderId)) {
      this.collapsedFolderIds.delete(folderId);
    } else {
      this.collapsedFolderIds.add(folderId);
    }
    this.saveCollapsedFolders();
    this.renderFolderTree();
  },

  // Load collapsed folder state from localStorage
  loadCollapsedFolders() {
    const raw = localStorage.getItem('sessionFolderCollapsed');
    if (!raw) return;
    try {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed)) {
        this.collapsedFolderIds = new Set(parsed);
      }
    } catch (error) {
      console.warn('Failed to load collapsed folders', error);
    }
  },

  // Persist collapsed folder state
  saveCollapsedFolders() {
    localStorage.setItem(
      'sessionFolderCollapsed',
      JSON.stringify(Array.from(this.collapsedFolderIds))
    );
  },

  // Update empty state copy and visibility
  updateSessionsEmptyState() {
    const emptyState = document.getElementById('sessionsEmpty');
    if (!emptyState) return;

    const emptyText = document.getElementById('sessionsEmptyText');
    const emptyActionLabel = document.getElementById('sessionsEmptyActionLabel');
    const hasSessions = this.sessions.length > 0;
    const hasNotes = this.noteSearchResults.length > 0;
    const hasWorkspaces = this.folders.length > 0;
    const shouldShow = !hasSessions && !hasNotes;

    if (!shouldShow) {
      emptyState.style.display = 'none';
      return;
    }

    if (hasWorkspaces) {
      if (emptyText) emptyText.textContent = 'No sessions yet';
      if (emptyActionLabel) emptyActionLabel.textContent = 'New Chat';
    } else {
      if (emptyText) emptyText.textContent = 'No workspaces yet';
      if (emptyActionLabel) emptyActionLabel.textContent = 'Create Workspace';
    }

    emptyState.style.display = 'flex';
  },

  // Handle empty state action button
  handleEmptyStateAction() {
    if (this.folders.length === 0) {
      this.showAddWorkspaceModal();
      return;
    }
    this.showCreateChatModal();
  },

  // Render loading state
  renderLoadingState() {
    const loadingState = document.getElementById('sessionsLoading');
    const emptyState = document.getElementById('sessionsEmpty');
    const container = document.getElementById('sessionsList');

    if (loadingState) loadingState.style.display = 'flex';
    if (emptyState) emptyState.style.display = 'none';
    if (container) container.innerHTML = '';
  },

  // Render empty state
  renderEmptyState() {
    const loadingState = document.getElementById('sessionsLoading');

    if (loadingState) loadingState.style.display = 'none';
    this.updateSessionsEmptyState();
  },

  // Render tag filters
  renderTagFilters() {
    const container = document.getElementById('tagFilters');
    if (!container) return;

    if (this.tags.length === 0) {
      container.innerHTML = '<div class="text-muted small px-3 py-1">No tags yet</div>';
      return;
    }

    container.innerHTML = this.tags
      .map(
        tag => `
      <button class="session-dropdown-item ${this.filterTags.includes(tag) ? 'active' : ''}" data-tag="${this.escapeHtml(tag)}">
        <span class="session-tag" data-color="${this.getTagColor(tag)}" style="margin-right: 6px;">${this.escapeHtml(tag)}</span>
      </button>
    `
      )
      .join('');

    container.querySelectorAll('.session-dropdown-item').forEach(item => {
      item.addEventListener('click', e => {
        const tag = e.currentTarget.dataset.tag;
        this.toggleTagFilter(tag);
      });
    });
  },

  // Check LLM availability for auto mode
  async checkChatLlmAvailability() {
    try {
      const response = await fetch('/api/agents/auto-config/availability');
      const data = await response.json();
      this.chatLlmAvailable = data.available;
      this.chatSystemModelConfigured = data.system_model_configured || false;
      return data;
    } catch (error) {
      console.error('Failed to check LLM availability:', error);
      this.chatLlmAvailable = false;
      this.chatSystemModelConfigured = false;
      return { available: false, system_model_configured: false };
    }
  },

  // Handle chat mode toggle change. workspaceId, when provided by the caller,
  // is the source of truth for workspace context (modal setup runs before the
  // workspace <select> is populated); omit it to fall back to the live select.
  handleChatModeChange(mode, workspaceId) {
    const manualSection = document.getElementById('chatManualSection');
    const autoSection = document.getElementById('chatAutoSection');
    const llmWarning = document.getElementById('chatLlmNotAvailableWarning');
    const llmWarningMessage = document.getElementById('chatLlmWarningMessage');
    const createBtnText = document.getElementById('createChatBtnText');

    this.chatAutoMode = mode === 'auto';

    if (mode === 'auto') {
      // Inside a workspace, "auto" means the workspace entry agent — not the
      // generic system assistant — so label it as a plain chat. Prefer the
      // explicit workspaceId; fall back to the live select for user toggles.
      const inWorkspace =
        workspaceId !== undefined
          ? Boolean(String(workspaceId).trim())
          : Boolean(document.getElementById('chatWorkspaceSelect')?.value);
      if (this.chatLlmAvailable) {
        if (manualSection) manualSection.classList.add('d-none');
        if (autoSection) autoSection.classList.remove('d-none');
        if (llmWarning) llmWarning.classList.add('d-none');
        if (createBtnText)
          createBtnText.textContent = inWorkspace ? 'Start Chat' : 'Start Assistant';
      } else {
        // LLM not available - show warning with action button
        if (manualSection) manualSection.classList.add('d-none');
        if (autoSection) autoSection.classList.add('d-none');
        if (llmWarning) llmWarning.classList.remove('d-none');
        if (createBtnText) createBtnText.textContent = 'Go to Settings';
        if (llmWarningMessage) {
          if (!this.chatSystemModelConfigured) {
            llmWarningMessage.textContent =
              'Assistant mode requires a System Model to be configured.';
          } else {
            llmWarningMessage.textContent =
              'Assistant mode requires an LLM provider. Please set up an API key or install Ollama.';
          }
        }
      }
    } else {
      // Direct agent chat mode
      if (manualSection) manualSection.classList.remove('d-none');
      if (autoSection) autoSection.classList.add('d-none');
      if (llmWarning) llmWarning.classList.add('d-none');
      if (createBtnText) createBtnText.textContent = 'Start Direct Chat';
    }
  },

  populateChatAgentSelect(agentSelect, agents, emptyLabel = 'No direct-chat agents available') {
    if (!agentSelect) return;

    const normalizedAgents = Array.isArray(agents) ? agents : [];
    const availableAgents = normalizedAgents.filter(
      agent => String(agent?.status || '') !== 'disabled'
    );
    if (availableAgents.length === 0) {
      agentSelect.innerHTML = `<option value="">${this.escapeHtml(emptyLabel)}</option>`;
      agentSelect.disabled = true;
      return;
    }

    agentSelect.innerHTML = availableAgents
      .map(
        agent =>
          `<option value="${this.escapeHtml(agent.name)}">${this.escapeHtml(agent.name)}</option>`
      )
      .join('');
    agentSelect.disabled = false;
  },

  updateAssistantModeText(workspaceId) {
    const autoModeText = document.getElementById('chatAutoModeText');
    if (!autoModeText) return;

    if (workspaceId) {
      autoModeText.textContent =
        "You'll chat with this workspace's Commander, which has the workspace context by default. Switch to Direct agent chat to talk to a different workspace agent.";
      return;
    }

    autoModeText.textContent =
      'Assistant starts as the system assistant. Pick a workspace to keep context scoped, or continue here before deciding where the work belongs.';
  },

  buildAssistantSessionTitle(initialMessage) {
    const message = String(initialMessage || '').trim();
    if (!message) return 'Assistant';
    return message.length > 50 ? `${message.slice(0, 47)}...` : message;
  },

  // Show create chat modal with workspace and agent selection
  async showCreateChatModal() {
    try {
      // Fetch agents
      const agents = await this.fetchAgents();

      // Check LLM availability to determine default mode
      await this.checkChatLlmAvailability();

      // Default to Auto mode if System Model is configured, otherwise Manual
      // (no workspace pre-selected here, so pass '' explicitly).
      if (this.chatSystemModelConfigured && this.chatLlmAvailable) {
        this.chatAutoMode = true;
        const autoRadio = document.getElementById('chatConfigModeAuto');
        if (autoRadio) autoRadio.checked = true;
        this.handleChatModeChange('auto', '');
      } else {
        this.chatAutoMode = false;
        const manualRadio = document.getElementById('chatConfigModeManual');
        if (manualRadio) manualRadio.checked = true;
        this.handleChatModeChange('manual', '');
      }

      // Clear initial message textareas
      const autoMessageInput = document.getElementById('chatAutoMessage');
      if (autoMessageInput) autoMessageInput.value = '';
      const manualMessageInput = document.getElementById('chatManualMessage');
      if (manualMessageInput) manualMessageInput.value = '';

      // Reset file attachments
      this.chatPendingFiles = [];
      this.updateChatFilesPreview();
      this.bindChatFileEvents();

      // Populate workspace dropdown
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      if (workspaceSelect) {
        workspaceSelect.innerHTML = '<option value="">No workspace (root)</option>';
        const appendWorkspaceOptions = (folders, depth = 0) => {
          (folders || []).forEach(folder => {
            if (!folder || !folder.id) return;
            const indent = depth > 0 ? `${'--'.repeat(depth)} ` : '';
            if (String(folder.kind || '').trim() !== 'group') {
              workspaceSelect.innerHTML += `<option value="${folder.id}">${this.escapeHtml(indent + (folder.name || 'Unnamed Workspace'))}</option>`;
            }
            appendWorkspaceOptions(folder.children || [], depth + 1);
          });
        };
        appendWorkspaceOptions(this.folders);
      }

      this.updateAssistantModeText('');

      const agentSelect = document.getElementById('chatAgentSelect');
      this.populateChatAgentSelect(agentSelect, agents, 'No direct-chat agents available');

      // Show the modal
      const modal = document.getElementById('createChatModal');
      if (modal) {
        if (typeof bootstrap !== 'undefined' && typeof bootstrap.Modal !== 'undefined') {
          const bsModal = new bootstrap.Modal(modal);
          bsModal.show();
        } else {
          // Fallback: show modal manually
          modal.classList.add('show');
          modal.style.display = 'block';
          document.body.classList.add('modal-open');
        }
      }
    } catch (error) {
      console.error('Failed to show create chat modal:', error);
    }
  },

  // Show create chat modal with workspace pre-selected
  async showCreateChatModalForWorkspace(workspaceId) {
    try {
      // Prefer agents configured in this workspace; fall back to global agents
      const agents = await this.fetchWorkspaceAgents(workspaceId);

      // Check LLM availability to determine default mode
      await this.checkChatLlmAvailability();

      // Default to Auto mode if System Model is configured, otherwise Manual.
      // Pass workspaceId explicitly: this runs before the workspace <select>
      // is populated, so a DOM read here would be stale.
      if (this.chatSystemModelConfigured && this.chatLlmAvailable) {
        this.chatAutoMode = true;
        const autoRadio = document.getElementById('chatConfigModeAuto');
        if (autoRadio) autoRadio.checked = true;
        this.handleChatModeChange('auto', workspaceId);
      } else {
        this.chatAutoMode = false;
        const manualRadio = document.getElementById('chatConfigModeManual');
        if (manualRadio) manualRadio.checked = true;
        this.handleChatModeChange('manual', workspaceId);
      }

      // Clear initial message textareas
      const autoMessageInput = document.getElementById('chatAutoMessage');
      if (autoMessageInput) autoMessageInput.value = '';
      const manualMessageInput = document.getElementById('chatManualMessage');
      if (manualMessageInput) manualMessageInput.value = '';

      // Reset file attachments
      this.chatPendingFiles = [];
      this.updateChatFilesPreview();
      this.bindChatFileEvents();

      // Populate workspace dropdown
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      if (workspaceSelect) {
        workspaceSelect.innerHTML = '<option value="">No workspace (root)</option>';
        const appendWorkspaceOptions = (folders, depth = 0) => {
          (folders || []).forEach(folder => {
            if (!folder || !folder.id) return;
            const indent = depth > 0 ? `${'--'.repeat(depth)} ` : '';
            if (String(folder.kind || '').trim() !== 'group') {
              const selected = folder.id === workspaceId ? ' selected' : '';
              workspaceSelect.innerHTML += `<option value="${folder.id}"${selected}>${this.escapeHtml(indent + (folder.name || 'Unnamed Workspace'))}</option>`;
            }
            appendWorkspaceOptions(folder.children || [], depth + 1);
          });
        };
        appendWorkspaceOptions(this.folders);
      }

      this.updateAssistantModeText(workspaceId);

      const agentSelect = document.getElementById('chatAgentSelect');
      this.populateChatAgentSelect(
        agentSelect,
        agents,
        workspaceId ? 'No direct-chat agents in this workspace' : 'No direct-chat agents available'
      );

      // Show the modal
      const modal = document.getElementById('createChatModal');
      if (modal) {
        if (typeof bootstrap !== 'undefined' && typeof bootstrap.Modal !== 'undefined') {
          const bsModal = new bootstrap.Modal(modal);
          bsModal.show();
        } else {
          // Fallback: show modal manually
          modal.classList.add('show');
          modal.style.display = 'block';
          document.body.classList.add('modal-open');
        }
      }
    } catch (error) {
      console.error('Failed to show create chat modal:', error);
    }
  },

  // Handle create chat from modal
  async handleCreateChatFromModal() {
    // If auto mode selected but LLM not available, redirect to settings
    if (this.chatAutoMode && !this.chatLlmAvailable) {
      // Close the modal first
      const modal = document.getElementById('createChatModal');
      if (modal) {
        const bsModal = bootstrap.Modal.getInstance(modal);
        bsModal?.hide();
      }
      // Redirect to settings
      window.location.href = '/settings';
      return;
    }

    // Get initial messages before closing modal
    const autoMessageInput = document.getElementById('chatAutoMessage');
    const autoInitialMessage = this.chatAutoMode ? autoMessageInput?.value?.trim() || '' : '';
    const manualMessageInput = document.getElementById('chatManualMessage');
    const manualInitialMessage = !this.chatAutoMode ? manualMessageInput?.value?.trim() || '' : '';

    // Validate message in auto mode
    if (this.chatAutoMode && this.chatLlmAvailable && !autoInitialMessage) {
      if (window.Toast) {
        Toast.warning('Please enter a message to start the chat');
      }
      autoMessageInput?.focus();
      return;
    }

    // Close the modal
    const modal = document.getElementById('createChatModal');
    if (modal) {
      const bsModal = bootstrap.Modal.getInstance(modal);
      bsModal?.hide();
    }

    if (this.chatAutoMode && this.chatLlmAvailable) {
      // Check if workspace was pre-selected
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      const preSelectedWorkspace = workspaceSelect?.value || '';
      const session = await this.createAssistantSession(
        preSelectedWorkspace,
        this.buildAssistantSessionTitle(autoInitialMessage)
      );

      if (session && session.id) {
        if (!preSelectedWorkspace) {
          this.autoModeSessionIds.add(session.id);
        }

        // Upload pending files first
        if (this.chatPendingFiles.length > 0) {
          await this.uploadChatPendingFiles(session.id);
        }

        // Send the initial message after a brief delay to ensure UI is ready
        if (autoInitialMessage && window.sendMessageToChat) {
          setTimeout(() => {
            window.sendMessageToChat(autoInitialMessage);
          }, 100);
        }
      }
    } else {
      // Manual mode - use selected workspace and agent
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      const agentSelect = document.getElementById('chatAgentSelect');

      const workspaceId = workspaceSelect?.value || '';
      const agentName = agentSelect?.value;

      if (!agentName) {
        if (window.Toast) {
          Toast.warning(
            workspaceId
              ? "No direct-chat agent is available in this workspace. Add an agent, or switch to auto mode to use the workspace's Commander."
              : 'No direct-chat agent is available. Add an agent or use Assistant.'
          );
        }
        return;
      }

      // Create the session
      let session;
      if (workspaceId) {
        session = await this.createSessionWithAgentInFolder(agentName, workspaceId);
      } else {
        session = await this.createSessionWithAgent(agentName);
      }

      // Upload pending files
      if (session && session.id && this.chatPendingFiles.length > 0) {
        await this.uploadChatPendingFiles(session.id);
      }

      // Send optional initial message for manual mode
      if (session && session.id && manualInitialMessage && window.sendMessageToChat) {
        setTimeout(() => {
          window.sendMessageToChat(manualInitialMessage);
        }, 100);
      }
    }

    // Clear pending files
    this.chatPendingFiles = [];
  },

  // Create new session - shows agent picker dialog first (legacy)
  async createNewSession() {
    // Now redirects to the modal
    this.showCreateChatModal();
  },

  // =============================================================================
  // Chat Modal File Attachment Functions
  // =============================================================================

  bindChatFileEvents() {
    const dropZone = document.getElementById('chatFileDropZone');
    const fileInput = document.getElementById('chatFileInput');

    if (dropZone) {
      // Remove old event listeners by cloning the element
      const newDropZone = dropZone.cloneNode(true);
      dropZone.parentNode.replaceChild(newDropZone, dropZone);

      newDropZone.addEventListener('click', () => {
        document.getElementById('chatFileInput')?.click();
      });
      newDropZone.addEventListener('dragover', e => {
        e.preventDefault();
        e.stopPropagation();
        newDropZone.classList.add('drag-active');
      });
      newDropZone.addEventListener('dragleave', e => {
        e.preventDefault();
        e.stopPropagation();
        newDropZone.classList.remove('drag-active');
      });
      newDropZone.addEventListener('drop', e => {
        e.preventDefault();
        e.stopPropagation();
        newDropZone.classList.remove('drag-active');
        const files = e.dataTransfer?.files;
        if (files && files.length > 0) {
          this.addChatFiles(Array.from(files));
        }
      });
    }

    if (fileInput) {
      const newFileInput = fileInput.cloneNode(true);
      fileInput.parentNode.replaceChild(newFileInput, fileInput);

      newFileInput.addEventListener('change', e => {
        const files = e.target?.files;
        if (files && files.length > 0) {
          this.addChatFiles(Array.from(files));
          e.target.value = '';
        }
      });
    }
  },

  addChatFiles(files) {
    const maxSize = 10 * 1024 * 1024; // 10MB

    files.forEach(file => {
      if (file.size > maxSize) {
        if (window.Toast) {
          Toast.warning(`${file.name} exceeds 10MB limit`);
        }
        return;
      }
      // Avoid duplicates
      if (!this.chatPendingFiles.some(f => f.name === file.name && f.size === file.size)) {
        this.chatPendingFiles.push(file);
      }
    });

    this.updateChatFilesPreview();
  },

  updateChatFilesPreview() {
    const container = document.getElementById('chatSelectedFiles');
    if (!container) return;

    if (this.chatPendingFiles.length === 0) {
      container.style.display = 'none';
      container.innerHTML = '';
      return;
    }

    container.style.display = 'block';

    const formatSize = bytes => {
      if (!bytes) return '';
      if (bytes < 1024) return bytes + ' B';
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
      return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    };

    const items = this.chatPendingFiles.map(
      (file, index) => `
      <div class="chat-selected-file-item" data-index="${index}">
        <span class="chat-file-name">${this.escapeHtml(file.name)}</span>
        <span class="chat-file-size">${formatSize(file.size)}</span>
        <button type="button" class="chat-file-remove" data-index="${index}" title="Remove">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
          </svg>
        </button>
      </div>
    `
    );

    container.innerHTML = items.join('');

    // Bind remove buttons
    container.querySelectorAll('.chat-file-remove').forEach(btn => {
      btn.addEventListener('click', e => {
        e.stopPropagation();
        const index = parseInt(btn.dataset.index, 10);
        this.chatPendingFiles.splice(index, 1);
        this.updateChatFilesPreview();
      });
    });
  },

  async uploadChatPendingFiles(sessionId) {
    if (!sessionId || this.chatPendingFiles.length === 0) return;

    for (const file of this.chatPendingFiles) {
      try {
        await this.uploadFileToSession(sessionId, file);
      } catch (error) {
        console.error('Failed to upload file:', file.name, error);
        if (window.Toast) {
          Toast.error(`Failed to upload ${file.name}`);
        }
      }
    }
  },

  async uploadFileToSession(sessionId, file) {
    const formData = new FormData();
    formData.append('file', file);

    const response = await fetch(`/api/sessions/${sessionId}/files`, {
      method: 'POST',
      body: formData
    });

    if (!response.ok) {
      throw new Error(`Upload failed: ${response.status}`);
    }

    return response.json();
  },

  normalizeAgentList(rawAgents) {
    const source = Array.isArray(rawAgents) ? rawAgents : [];
    const result = [];
    const seen = new Set();

    source.forEach(agent => {
      const isString = typeof agent === 'string';
      const name = String(isString ? agent : agent?.name || '').trim();
      if (!name) return;

      const key = name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);

      result.push({
        name,
        model: isString ? '' : String(agent?.model || '').trim(),
        description: isString ? '' : String(agent?.description || '').trim(),
        status: isString ? '' : String(agent?.status || '').trim()
      });
    });

    return result;
  },

  // Fetch available global agents
  async fetchAgents() {
    try {
      const response = await fetch('/api/agents');
      if (response.ok) {
        const data = await response.json();
        const agents = this.normalizeAgentList(data.agents);
        if (agents.length > 0) return agents;
      }

      // Fallback endpoint used by dashboard/workspace pages
      const fallback = await fetch('/api/agents/dashboard/list');
      if (!fallback.ok) return [];
      const fallbackData = await fallback.json();
      return this.normalizeAgentList(fallbackData.agents);
    } catch (error) {
      console.error('Failed to fetch agents:', error);
      return [];
    }
  },

  populateWorkspaceEntryAgentSelect() {
    // Entry agent is always auto-created as workspace manager — no UI needed.
  },

  async populateWorkspaceEntryAgentSelectFromAgents() {
    // Entry agent is always auto-created as workspace manager — no UI needed.
  },

  // Fetch agents scoped to a workspace; falls back to global agents
  async fetchWorkspaceAgents(workspaceId) {
    const globalAgents = await this.fetchAgents();
    if (!workspaceId) return globalAgents;

    try {
      const response = await fetch(
        `/api/orchestration/workspace?id=${encodeURIComponent(workspaceId)}`
      );
      if (!response.ok) return globalAgents;

      const workspace = await response.json();
      const names = [];
      const seen = new Set();

      const addName = value => {
        const name = String(value || '').trim();
        if (!name) return;
        const key = name.toLowerCase();
        if (seen.has(key)) return;
        seen.add(key);
        names.push(name);
      };

      if (Array.isArray(workspace?.agent_instances)) {
        workspace.agent_instances.forEach(instance => addName(instance?.name));
      }
      if (Array.isArray(workspace?.agents)) {
        workspace.agents.forEach(name => addName(name));
      }

      if (names.length === 0) return globalAgents;

      const globalByName = new Map(
        globalAgents.map(agent => [String(agent.name || '').toLowerCase(), agent])
      );
      return names.map(
        name => globalByName.get(name.toLowerCase()) || { name, model: '', description: '' }
      );
    } catch (error) {
      console.error('Failed to fetch workspace agents:', error);
      return globalAgents;
    }
  },

  // Show agent picker dialog
  showAgentPickerDialog(agents) {
    // Remove existing modal if any
    const existingModal = document.getElementById('agentPickerModal');
    if (existingModal) existingModal.remove();

    const currentAgentName = this.getActiveSession()?.agent_name || '';

    const modalHtml = `
      <div class="modal fade" id="agentPickerModal" tabindex="-1">
        <div class="modal-dialog modal-dialog-centered">
          <div class="modal-content bg-dark text-light">
            <div class="modal-header border-secondary">
              <h5 class="modal-title">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M12,2A2,2 0 0,1 14,4C14,4.74 13.6,5.39 13,5.73V7H14A7,7 0 0,1 21,14H22A1,1 0 0,1 23,15V18A1,1 0 0,1 22,19H21V20A2,2 0 0,1 19,22H5A2,2 0 0,1 3,20V19H2A1,1 0 0,1 1,18V15A1,1 0 0,1 2,14H3A7,7 0 0,1 10,7H11V5.73C10.4,5.39 10,4.74 10,4A2,2 0 0,1 12,2M7.5,13A2.5,2.5 0 0,0 5,15.5A2.5,2.5 0 0,0 7.5,18A2.5,2.5 0 0,0 10,15.5A2.5,2.5 0 0,0 7.5,13M16.5,13A2.5,2.5 0 0,0 14,15.5A2.5,2.5 0 0,0 16.5,18A2.5,2.5 0 0,0 19,15.5A2.5,2.5 0 0,0 16.5,13Z"/>
                </svg>
                Select Agent for New Session
              </h5>
              <button type="button" class="btn-close btn-close-white" data-bs-dismiss="modal"></button>
            </div>
            <div class="modal-body">
              <div class="list-group list-group-flush">
                ${agents
                  .map(
                    agent => `
                  <button type="button" class="list-group-item list-group-item-action bg-dark text-light border-secondary agent-picker-item ${agent.name === currentAgentName ? 'active' : ''}"
                          data-agent-name="${this.escapeHtml(agent.name)}">
                    <div class="d-flex justify-content-between align-items-center">
                      <div>
                        <strong>${this.escapeHtml(agent.name)}</strong>
                        ${agent.model ? `<small class="text-muted ms-2">${this.escapeHtml(agent.model)}</small>` : ''}
                      </div>
                      ${agent.name === currentAgentName ? '<span class="badge bg-primary">Pinned</span>' : ''}
                    </div>
                    ${agent.description ? `<small class="text-muted d-block mt-1">${this.escapeHtml(agent.description)}</small>` : ''}
                  </button>
                `
                  )
                  .join('')}
              </div>
            </div>
            <div class="modal-footer border-secondary">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>
            </div>
          </div>
        </div>
      </div>
    `;

    document.body.insertAdjacentHTML('beforeend', modalHtml);

    const modal = new bootstrap.Modal(document.getElementById('agentPickerModal'));

    // Handle agent selection
    document.querySelectorAll('.agent-picker-item').forEach(item => {
      item.addEventListener('click', async () => {
        const agentName = item.dataset.agentName;
        modal.hide();
        await this.createSessionWithAgent(agentName);
      });
    });

    // Cleanup on modal hidden
    document.getElementById('agentPickerModal').addEventListener('hidden.bs.modal', () => {
      document.getElementById('agentPickerModal')?.remove();
    });

    modal.show();
  },

  // Create session with specific agent
  async createSessionWithAgent(agentName, openChat = true) {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: 'New Session',
          agent_name: agentName
        })
      });

      if (!response.ok) {
        const errorBody = await response.json().catch(() => null);
        throw new Error(errorBody?.message || errorBody?.error || 'Failed to create session');
      }

      const data = await response.json();
      if (data.session) {
        this.sessions.unshift(data.session);
        this.activeSessionId = data.session.id;
        this.saveActiveSession();
        this.renderSessions();

        // Update the combined chat info bar with session title and agent
        this.updateChatInfoBar(data.session.title || 'New Session', agentName);

        this.updateCurrentAgent(agentName);

        // Clear chat area for new session
        if (typeof clearChatHistory === 'function') {
          clearChatHistory();
        }

        if (openChat) this.openChatPanelIfAvailable();

        // Emit event for workspace hub to refresh
        if (window.EventBus) {
          EventBus.emit('session:created', { session: data.session, folderId: null });
        }

        return data.session;
      }
      return null;
    } catch (error) {
      console.error('Failed to create session:', error);
      return null;
    }
  },

  async createAssistantSession(folderId = '', title = 'Assistant', openChat = true) {
    try {
      const payload = {
        title: title || 'Assistant'
      };
      if (folderId) {
        payload.folder_id = folderId;
      }

      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });

      if (!response.ok) throw new Error('Failed to create assistant session');

      const data = await response.json();
      if (data.session) {
        this.sessions.unshift(data.session);
        this.activeSessionId = data.session.id;
        this.saveActiveSession();
        this.renderSessions();

        this.updateCurrentAgent(data.session.agent_name || '');

        if (typeof clearChatHistory === 'function') {
          clearChatHistory();
        }

        if (openChat) this.openChatPanelIfAvailable();

        if (window.EventBus) {
          EventBus.emit('session:created', { session: data.session, folderId: folderId || null });
        }

        return data.session;
      }
      return null;
    } catch (error) {
      console.error('Failed to create assistant session:', error);
      return null;
    }
  },

  // Create a new session in a specific folder/workspace
  async createNewSessionInFolder(folderId) {
    return this.createAssistantSession(folderId, 'Assistant');
  },

  // Show agent picker dialog for folder context
  showAgentPickerDialogForFolder(agents, folderId) {
    // Remove existing modal if any
    const existingModal = document.getElementById('agentPickerModal');
    if (existingModal) existingModal.remove();

    const modal = document.createElement('div');
    modal.id = 'agentPickerModal';
    modal.className = 'modal fade show';
    modal.style.display = 'block';
    modal.style.backgroundColor = 'rgba(0,0,0,0.5)';
    modal.innerHTML = `
      <div class="modal-dialog modal-dialog-centered modal-sm">
        <div class="modal-content" style="background: var(--bg-primary); border: 1px solid var(--border-color);">
          <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
            <h6 class="modal-title" style="color: var(--text-primary);">Select Agent</h6>
            <button type="button" class="btn-close" data-dismiss="modal"></button>
          </div>
          <div class="modal-body">
            <div class="agent-picker-list">
              ${agents
                .map(
                  agent => `
                <button class="agent-picker-item modern-btn modern-btn-secondary w-100 mb-2 text-start" data-agent="${agent.name}">
                  <span class="agent-name">${this.escapeHtml(agent.name)}</span>
                  ${agent.description ? `<small class="text-muted d-block">${this.escapeHtml(agent.description)}</small>` : ''}
                </button>
              `
                )
                .join('')}
            </div>
          </div>
        </div>
      </div>
    `;

    document.body.appendChild(modal);

    // Handle agent selection
    modal.querySelectorAll('.agent-picker-item').forEach(btn => {
      btn.addEventListener('click', () => {
        const agentName = btn.dataset.agent;
        modal.remove();
        this.createSessionWithAgentInFolder(agentName, folderId);
      });
    });

    // Handle close
    modal.querySelector('.btn-close').addEventListener('click', () => modal.remove());
    modal.addEventListener('click', e => {
      if (e.target === modal) modal.remove();
    });
  },

  // Create session with agent in a specific folder
  async createSessionWithAgentInFolder(agentName, folderId, openChat = true) {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: 'New Session',
          agent_name: agentName,
          folder_id: folderId
        })
      });

      if (!response.ok) {
        const errorBody = await response.json().catch(() => null);
        throw new Error(errorBody?.message || errorBody?.error || 'Failed to create session');
      }

      const data = await response.json();
      if (data.session) {
        this.sessions.unshift(data.session);
        this.activeSessionId = data.session.id;
        this.saveActiveSession();
        this.renderSessions();

        // Update the combined chat info bar with session title and agent
        this.updateChatInfoBar(data.session.title || 'New Session', agentName);

        this.updateCurrentAgent(agentName);

        // Clear chat area for new session
        if (typeof clearChatHistory === 'function') {
          clearChatHistory();
        }

        if (openChat) this.openChatPanelIfAvailable();

        // Emit event for workspace hub to refresh
        if (window.EventBus) {
          EventBus.emit('session:created', { session: data.session, folderId });
        }

        return data.session;
      }
      return null;
    } catch (error) {
      console.error('Failed to create session in folder:', error);
      return null;
    }
  },

  // Switch to a session
  // openChat: whether to open the chat panel (default true for user-initiated, false for init)
  async switchToSession(sessionId, openChat = true) {
    const chatArea = document.getElementById('chatArea');
    const hasRenderedMessages = chatArea && chatArea.children.length > 0;

    // If clicking on already-active session, just open the chat panel
    if (sessionId === this.activeSessionId && hasRenderedMessages) {
      if (openChat) this.openChatPanelIfAvailable();
      return;
    }

    try {
      // Load full session with messages
      const response = await fetch(`/api/sessions/${sessionId}`);
      if (!response.ok) throw new Error('Failed to load session');

      const session = await response.json();

      this.activeSessionId = sessionId;
      this.saveActiveSession();
      this.renderSessions();

      this.updateCurrentAgent(session.agent_name || '');

      // Restore messages to chat area
      this.restoreSessionMessages(session);

      if (openChat) this.openChatPanelIfAvailable();

      // Initialize/update session file manager
      this.initializeSessionFiles(sessionId);

      // Load tasks for this session
      await this.loadSessionTasks();
    } catch (error) {
      console.error('Failed to switch session:', error);
    }
  },

  // Initialize session files panel
  initializeSessionFiles(sessionId) {
    const panel = document.getElementById('sessionFilesPanel');
    const dropzone = document.getElementById('fileDropzoneCompact');
    const fileList = document.getElementById('fileListContainer');

    if (panel && sessionId) {
      // Show the panel
      panel.style.display = 'block';

      // Update session ID in data attributes
      if (dropzone) dropzone.dataset.sessionId = sessionId;
      if (fileList) fileList.dataset.sessionId = sessionId;

      // Initialize or update FileManager
      if (window.FileManager) {
        if (window.sessionFileManager) {
          window.sessionFileManager.disconnect();
        }
        window.sessionFileManager = new FileManager(sessionId);
      }
    } else if (panel) {
      // Hide panel when no session
      panel.style.display = 'none';
    }
  },

  // Update the execution-agent display for the active session.
  updateCurrentAgent(agentName) {
    const normalizedAgentName = String(agentName || '').trim();

    const session = this.getActiveSession();
    this.updateChatInfoBar(session?.title, normalizedAgentName);

    this.updateChatModelForAgent(normalizedAgentName);

    // Update the navbar display (secondary/legacy)
    const agentElement = document.querySelector('#currentAgentDisplay span.fw-medium');
    if (agentElement) {
      agentElement.textContent = normalizedAgentName || 'Assistant';
    }

    // Also update any agent display in the header
    const agentHeader = document.querySelector('.agent-name');
    if (agentHeader) {
      agentHeader.textContent = normalizedAgentName || 'Assistant';
    }

    if (typeof window.refreshAgentDisplay === 'function') {
      window.refreshAgentDisplay();
    }

    if (typeof loadAgentsForSidebar === 'function') {
      loadAgentsForSidebar();
    }
  },

  // Update the combined chat info bar (session + agent)
  updateChatInfoBar(sessionTitle, agentName) {
    const infoBar = document.getElementById('chatInfoBar');
    const sessionTitleEl = document.getElementById('chatSessionTitle');
    const agentNameEl = document.getElementById('chatAgentName');
    const editAgentBtn = document.getElementById('chatEditAgentBtn');
    const navbarAgentDisplay = document.getElementById('currentAgentDisplay');

    if (!infoBar) return;

    // Update session ID in header
    const sessionIdEl = document.getElementById('chatPanelSessionId');
    if (sessionIdEl) {
      if (this.activeSessionId) {
        sessionIdEl.textContent = this.activeSessionId;
        sessionIdEl.style.display = '';
      } else {
        sessionIdEl.style.display = 'none';
      }
    }

    // Update session title
    if (sessionTitleEl && sessionTitle) {
      sessionTitleEl.textContent = sessionTitle;
    }

    const hasSessionContext = Boolean(this.activeSessionId || sessionTitle);
    const displayAgentName = agentName || (hasSessionContext ? 'Assistant' : '');

    // Update agent name
    if (agentNameEl) {
      agentNameEl.textContent = displayAgentName;
    }
    if (typeof window.refreshChatWebSearchToggle === 'function') {
      window.refreshChatWebSearchToggle(agentName || 'Ask Ori');
    }

    if (editAgentBtn) {
      if (agentName) {
        editAgentBtn.dataset.agentName = agentName;
        editAgentBtn.classList.remove('disabled');
        editAgentBtn.removeAttribute('aria-disabled');
        editAgentBtn.removeAttribute('tabindex');
        editAgentBtn.disabled = false;
      } else {
        editAgentBtn.dataset.agentName = '';
        editAgentBtn.classList.add('disabled');
        editAgentBtn.setAttribute('aria-disabled', 'true');
        editAgentBtn.setAttribute('tabindex', '-1');
        editAgentBtn.disabled = true;
      }
    }

    // Show bar if we have either session or agent
    if (sessionTitle || displayAgentName) {
      infoBar.style.display = 'block';
      // Hide navbar agent display when chat info bar is visible
      if (navbarAgentDisplay) {
        navbarAgentDisplay.style.display = 'none';
      }
    } else {
      infoBar.style.display = 'none';
      // Show navbar agent display as fallback
      if (navbarAgentDisplay) {
        navbarAgentDisplay.style.display = '';
      }
    }
  },

  // Update model display in chat info bar
  updateChatModelName(modelName) {
    const modelInfo = document.getElementById('chatModelInfo');
    const modelNameEl = document.getElementById('chatModelName');
    if (!modelInfo || !modelNameEl) return;

    if (modelName) {
      modelNameEl.textContent = modelName;
      modelNameEl.title = modelName;
      modelInfo.style.display = 'flex';
    } else {
      modelNameEl.textContent = '';
      modelNameEl.title = '';
      modelInfo.style.display = 'none';
    }
  },

  // Fetch and update the model for the active execution agent
  async updateChatModelForAgent(agentName) {
    if (!agentName) {
      this.updateChatModelName('');
      return;
    }

    const cachedModel = this.agentModelCache.get(agentName);
    if (cachedModel) {
      this.updateChatModelName(cachedModel);
      return;
    }

    this.updateChatModelName('');

    try {
      const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`);
      if (!response.ok) {
        this.updateChatModelName('');
        return;
      }

      const data = await response.json();
      const modelName = data.model || '';
      if (modelName) {
        this.agentModelCache.set(agentName, modelName);
      }

      const activeAgentName = this.getActiveSession()?.agent_name || agentName;
      if (activeAgentName !== agentName) return;

      this.updateChatModelName(modelName);
    } catch (error) {
      console.error('Failed to load agent model:', error);
      this.updateChatModelName('');
    }
  },

  // Initialize chat agent bar button handler
  initChatAgentBar() {
    // Hide navbar agent display - it's replaced by the chat agent bar
    const navbarAgentDisplay = document.getElementById('currentAgentDisplay');
    if (navbarAgentDisplay) {
      navbarAgentDisplay.style.display = 'none';
    }

    const changeBtn = document.getElementById('chatChangeAgentBtn');
    if (changeBtn) {
      changeBtn.style.display = 'none';
    }

    const editBtn = document.getElementById('chatEditAgentBtn');
    if (editBtn && !editBtn.dataset.bound) {
      editBtn.dataset.bound = 'true';
      editBtn.addEventListener('click', event => {
        event.preventDefault();
        const agentName = editBtn.dataset.agentName || this.getActiveSession()?.agent_name;
        if (!agentName) {
          if (window.Toast) {
            Toast.warning('Select an agent before editing.', { title: 'No Agent Selected' });
          }
          return;
        }
        this.showEditAgentModal(agentName);
      });
    }

    this.initEditAgentModal();
  },

  initEditAgentModal() {
    if (this.editAgentModalInitialized) return;
    const modalEl = document.getElementById('editAgentModal');
    if (!modalEl) return;

    this.editAgentModalInitialized = true;

    modalEl.addEventListener('hidden.bs.modal', () => {
      this.resetEditAgentModal();
    });

    const form = document.getElementById('editAgentForm');
    if (form) {
      form.addEventListener('submit', event => event.preventDefault());
    }

    const saveBtn = document.getElementById('editAgentSaveBtn');
    if (saveBtn) {
      saveBtn.addEventListener('click', () => this.saveEditAgentChanges());
    }

    const tagsInput = document.getElementById('editAgentTagsInput');
    if (tagsInput) {
      tagsInput.addEventListener('keydown', event => {
        if (event.key === 'Enter' && tagsInput.value.trim()) {
          event.preventDefault();
          this.addEditAgentTag(tagsInput.value.trim());
          tagsInput.value = '';
        } else if (
          event.key === 'Backspace' &&
          !tagsInput.value &&
          this.editAgentSelectedTags.length > 0
        ) {
          this.removeEditAgentTag(
            this.editAgentSelectedTags[this.editAgentSelectedTags.length - 1]
          );
        }
      });
    }

    const tagsContainer = document.getElementById('editAgentTagsContainer');
    if (tagsContainer && tagsInput) {
      tagsContainer.addEventListener('click', event => {
        if (event.target === tagsContainer) {
          tagsInput.focus();
        }
      });
    }

    // Listen for Type changes to update Model options
    const typeSelect = document.getElementById('editAgentTypeSelect');
    if (typeSelect) {
      typeSelect.addEventListener('change', event => {
        const modelSelect = document.getElementById('editAgentModelSelect');
        const currentModel = modelSelect?.value;
        this.updateEditAgentModelOptions(event.target.value, currentModel);
      });
    }
  },

  async showEditAgentModal(agentName) {
    const modalEl = document.getElementById('editAgentModal');
    if (!modalEl) return;

    this.initEditAgentModal();
    this.editAgentOriginalName = agentName;
    this.editAgentSelectedTags = [];
    this.editAgentMCPServers = [];
    this.clearEditAgentMessages();
    this.renderEditAgentMCPServers([], { workspaceScoped: true });
    this.setEditAgentLoading(true, 'Loading agent...');
    this.setEditAgentFormEnabled(false);

    const modal = bootstrap.Modal.getInstance(modalEl) || new bootstrap.Modal(modalEl);
    modal.show();

    try {
      await this.ensureEditAgentModelOptions();
      await this.loadEditAgentDetails(agentName);
    } catch (error) {
      console.error('Failed to load agent details:', error);
      this.showEditAgentError(error.message || 'Failed to load agent details');
    } finally {
      this.setEditAgentLoading(false);
      this.setEditAgentFormEnabled(true);
    }
  },

  async loadEditAgentDetails(agentName) {
    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/detail`);
    if (!response.ok) {
      if (response.status === 404) {
        throw new Error('Agent not found');
      }
      throw new Error('Failed to load agent details');
    }

    const agent = await response.json();
    this.populateEditAgentForm(agent);
  },

  populateEditAgentForm(agent) {
    this.editAgentCurrentProvider = String(agent.provider || '').trim();

    const nameInput = document.getElementById('editAgentNameInput');
    if (nameInput) nameInput.value = agent.name || '';

    const typeSelect = document.getElementById('editAgentTypeSelect');
    if (typeSelect) {
      this.ensureEditAgentSelectOption(typeSelect, agent.type);
      typeSelect.value = agent.type || typeSelect.value;
    }

    const roleSelect = document.getElementById('editAgentRoleSelect');
    if (roleSelect) {
      this.ensureEditAgentSelectOption(roleSelect, agent.role);
      roleSelect.value = agent.role || roleSelect.value;
    }

    // Update model options based on agent type, then set the current model
    const agentType = agent.type || 'tool-calling';
    this.updateEditAgentModelOptions(agentType, agent.model);

    const descriptionInput = document.getElementById('editAgentDescription');
    if (descriptionInput) {
      descriptionInput.value = agent.metadata?.description || '';
    }

    this.mountEditAgentAppearance(agent);

    const favoriteToggle = document.getElementById('editAgentFavoriteToggle');
    if (favoriteToggle) {
      favoriteToggle.checked = Boolean(agent.metadata?.favorite);
    }

    this.editAgentSelectedTags = agent.metadata?.tags || [];
    this.renderEditAgentTags();
    this.renderEditAgentMCPServers([], { workspaceScoped: true });
  },

  normalizeEditAgentMCPServers(servers, enabledOnly = false) {
    if (!Array.isArray(servers)) return [];

    const seen = new Set();
    const normalized = [];

    servers.forEach(server => {
      let record = null;

      if (typeof server === 'string') {
        const name = server.trim();
        if (name) {
          record = {
            name,
            status: 'configured',
            tool_count: 0,
            enabled: true
          };
        }
      } else if (server && typeof server === 'object') {
        const name = typeof server.name === 'string' ? server.name.trim() : '';
        if (name) {
          const enabled = server.enabled !== false;
          const statusRaw =
            typeof server.status === 'string' ? server.status.trim().toLowerCase() : '';
          const toolCount = Number.isFinite(server.tool_count)
            ? server.tool_count
            : Number.isFinite(server.toolCount)
              ? server.toolCount
              : 0;

          record = {
            name,
            status: statusRaw || (enabled ? 'configured' : 'stopped'),
            tool_count: toolCount,
            enabled
          };
        }
      }

      if (!record) return;
      if (enabledOnly && !record.enabled) return;

      const key = record.name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      normalized.push(record);
    });

    return normalized;
  },

  getEditAgentMCPStatusClass(status) {
    const normalized = String(status || '').toLowerCase();
    if (normalized === 'running') return 'running';
    if (normalized === 'starting' || normalized === 'warming') return 'starting';
    if (normalized === 'error' || normalized === 'failed') return 'error';
    if (normalized === 'stopped' || normalized === 'disabled') return 'stopped';
    return 'configured';
  },

  renderEditAgentMCPServers(servers, options = {}) {
    const container = document.getElementById('editAgentMCPList');
    if (!container) return;

    if (options.workspaceScoped) {
      container.innerHTML =
        '<span class="agent-edit-mcp-empty">Workspace-scoped. Configure MCP bindings from the target workspace.</span>';
      return;
    }

    if (options.loading) {
      container.innerHTML = '<span class="agent-edit-mcp-empty">Loading MCP servers...</span>';
      return;
    }

    if (options.error) {
      container.innerHTML =
        '<span class="agent-edit-mcp-error">Could not load MCP server status.</span>';
      return;
    }

    const enabledServers = this.normalizeEditAgentMCPServers(servers, true);
    if (enabledServers.length === 0) {
      container.innerHTML =
        '<span class="agent-edit-mcp-empty">No MCP preferences saved for this agent.</span>';
      return;
    }

    container.innerHTML = enabledServers
      .map(server => {
        const statusClass = this.getEditAgentMCPStatusClass(server.status);
        const statusLabel =
          statusClass === 'configured' ? 'configured' : server.status || 'configured';
        const toolCount = Number.isFinite(server.tool_count) ? server.tool_count : 0;
        const toolText =
          toolCount > 0 ? `${toolCount} tool${toolCount === 1 ? '' : 's'}` : 'tool count unknown';

        return `
        <span class="agent-edit-mcp-pill">
          <span class="agent-edit-mcp-name">${this.escapeHtml(server.name)}</span>
          <span class="agent-edit-mcp-meta">
            <span class="agent-edit-mcp-status ${statusClass}">${this.escapeHtml(statusLabel)}</span>
            <span>${this.escapeHtml(toolText)}</span>
          </span>
        </span>
      `;
      })
      .join('');
  },

  async ensureEditAgentModelOptions() {
    if (this.editAgentModelOptionsLoaded) return;

    // Load and cache providers data
    if (typeof loadAvailableProviders === 'function') {
      this.editAgentProvidersData = await loadAvailableProviders();
    }

    // Set default fallback models if no providers loaded
    if (!this.editAgentProvidersData || this.editAgentProvidersData.length === 0) {
      this.editAgentProvidersData = [
        {
          name: 'default',
          display_name: 'Default',
          models: [
            { value: 'gpt-5', label: 'gpt-5', type: 'research' },
            { value: 'gpt-5-mini', label: 'gpt-5-mini', type: 'general' },
            { value: 'gpt-5-nano', label: 'gpt-5-nano', type: 'tool-calling' },
            { value: 'gpt-4o', label: 'gpt-4o', type: 'general' },
            { value: 'gpt-4o-mini', label: 'gpt-4o-mini', type: 'tool-calling' },
            { value: 'claude-3-5-sonnet-20241022', label: 'claude-3-5-sonnet', type: 'general' },
            { value: 'claude-3-haiku-20240307', label: 'claude-3-haiku', type: 'tool-calling' },
            { value: 'gemini-2.5-flash', label: 'gemini-2.5-flash', type: 'tool-calling' },
            { value: 'gemini-2.5-pro', label: 'gemini-2.5-pro', type: 'research' },
            { value: 'llama3.2', label: 'llama3.2', type: 'tool-calling' },
            { value: 'mistral', label: 'mistral', type: 'tool-calling' },
            { value: 'codellama', label: 'codellama', type: 'general' }
          ]
        }
      ];
    }

    this.editAgentModelOptionsLoaded = true;
  },

  updateEditAgentModelOptions(agentType, currentModel = null) {
    const select = document.getElementById('editAgentModelSelect');
    if (!select) return;

    // Clear existing options
    select.innerHTML = '<option value="">Select a model...</option>';

    if (!this.editAgentProvidersData) return;

    // Group models by provider
    this.editAgentProvidersData.forEach(provider => {
      if (!provider.models || provider.models.length === 0) return;

      // Filter models by agent type
      const filteredModels = provider.models.filter(model => {
        // If model has no type, include it for all agent types
        if (!model.type) return true;
        return model.type === agentType;
      });

      if (filteredModels.length === 0) return;

      // Create optgroup for this provider
      const optgroup = document.createElement('optgroup');
      optgroup.label = provider.display_name || provider.name;

      filteredModels.forEach(model => {
        const option = document.createElement('option');
        option.value = model.value;
        option.textContent = model.label || model.value;
        option.setAttribute('data-provider', model.provider || provider.name || '');
        if (model.value === currentModel) {
          option.selected = true;
        }
        optgroup.appendChild(option);
      });

      select.appendChild(optgroup);
    });

    // If current model wasn't found in filtered list, add it as a custom option
    if (currentModel && select.value !== currentModel) {
      const customOption = document.createElement('option');
      customOption.value = currentModel;
      customOption.textContent = `${currentModel} (current)`;
      if (this.editAgentCurrentProvider) {
        customOption.setAttribute('data-provider', this.editAgentCurrentProvider);
      }
      customOption.selected = true;
      select.insertBefore(customOption, select.firstChild.nextSibling);
    }
  },

  ensureEditAgentSelectOption(selectEl, value) {
    if (!selectEl || !value) return;
    const exists = Array.from(selectEl.options).some(option => option.value === value);
    if (exists) return;
    const option = document.createElement('option');
    option.value = value;
    option.textContent = value;
    selectEl.appendChild(option);
  },

  // The Edit Agent modal mounts the same shared editor the Agents page does, so
  // an appearance changed from a chat session is the same operation with the
  // same guards — not a second, weaker write path (FR-26/FR-90).
  //
  // It saves on its own, immediately, through its adapter; the modal's Save
  // button continues to own only the non-appearance fields.
  mountEditAgentAppearance(agent) {
    const host = document.getElementById('editAgentAppearanceHost');
    if (!host || !window.AgentAppearanceEditor) return;
    if (this.editAgentAppearanceEditor?.destroy) this.editAgentAppearanceEditor.destroy();

    const name = agent.name || '';
    const uploadURL = `/api/agents/${encodeURIComponent(name)}/appearance/upload`;
    const request = (url, init) =>
      fetch(url, init).then(async response => {
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(data.message || data.error || 'That change could not be saved.');
        }
        // Keep the modal's own copy in step so a later Save does not send a
        // stale appearance alongside the fields it does own (FR-66).
        if (data.appearance) agent.appearance = data.appearance;
        this.refreshAgentAppearanceCaches?.(name, data.appearance);
        return data.appearance;
      });

    this.editAgentAppearanceEditor = window.AgentAppearanceEditor.create({
      host,
      idPrefix: 'editAgentAppearance',
      mode: 'edit',
      appearance: agent.appearance,
      agent: { name, source: agent.source || 'user', role: agent.role || '' },
      adapter: {
        saveAppearance: patch =>
          request(`/api/agents?name=${encodeURIComponent(name)}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ appearance: patch })
          }),
        uploadImage: file => {
          const form = new FormData();
          form.append('image', file);
          return request(uploadURL, { method: 'POST', body: form });
        },
        removeImage: () => request(uploadURL, { method: 'DELETE' })
      }
    });
  },

  renderEditAgentTags() {
    const container = document.getElementById('editAgentTagsContainer');
    const input = document.getElementById('editAgentTagsInput');
    if (!container || !input) return;

    container.querySelectorAll('.agent-edit-tag').forEach(tag => tag.remove());

    this.editAgentSelectedTags.forEach(tag => {
      const tagEl = document.createElement('span');
      tagEl.className = 'agent-edit-tag';

      const label = document.createElement('span');
      label.textContent = tag;

      const removeBtn = document.createElement('button');
      removeBtn.type = 'button';
      removeBtn.className = 'agent-edit-tag-remove';
      removeBtn.setAttribute('aria-label', `Remove tag ${tag}`);
      removeBtn.textContent = 'x';
      removeBtn.addEventListener('click', event => {
        event.stopPropagation();
        this.removeEditAgentTag(tag);
      });

      tagEl.appendChild(label);
      tagEl.appendChild(removeBtn);
      container.insertBefore(tagEl, input);
    });
  },

  addEditAgentTag(tag) {
    const cleanTag = tag.trim();
    if (!cleanTag) return;
    if (!this.editAgentSelectedTags.includes(cleanTag)) {
      this.editAgentSelectedTags.push(cleanTag);
      this.renderEditAgentTags();
    }
  },

  removeEditAgentTag(tag) {
    this.editAgentSelectedTags = this.editAgentSelectedTags.filter(item => item !== tag);
    this.renderEditAgentTags();
  },

  async saveEditAgentChanges() {
    const nameInput = document.getElementById('editAgentNameInput');
    const typeSelect = document.getElementById('editAgentTypeSelect');
    const roleSelect = document.getElementById('editAgentRoleSelect');
    const modelSelect = document.getElementById('editAgentModelSelect');
    const descriptionInput = document.getElementById('editAgentDescription');
    const favoriteToggle = document.getElementById('editAgentFavoriteToggle');

    const newName = nameInput?.value.trim();
    const type = typeSelect?.value;
    const role = roleSelect?.value;
    const model = modelSelect?.value;
    const selectedModelOption = modelSelect?.selectedOptions?.[0] || null;
    const selectedProvider = String(
      selectedModelOption?.getAttribute('data-provider') || this.editAgentCurrentProvider || ''
    ).trim();
    const validProviders = new Set([
      'openai',
      'codex',
      'claude_code',
      'claude',
      'gemini',
      'ollama',
      'lmstudio',
      'mlx_lm'
    ]);
    const resolvedProvider = validProviders.has(selectedProvider)
      ? selectedProvider
      : String(this.editAgentCurrentProvider || '').trim();

    if (!newName) {
      this.showEditAgentError('Name is required.');
      nameInput?.focus();
      return;
    }
    if (!type || !role) {
      this.showEditAgentError('Type and role are required.');
      return;
    }
    if (!model) {
      this.showEditAgentError('Model is required.');
      modelSelect?.focus();
      return;
    }

    const payload = {
      name: newName,
      type,
      role,
      model,
      description: descriptionInput?.value.trim() || '',
      // Appearance is deliberately absent: the shared editor saves each change
      // on its own the moment it happens, so including it here would give one
      // setting two write paths that could disagree about what is staged
      // (FR-41).
      tags: this.editAgentSelectedTags,
      favorite: Boolean(favoriteToggle?.checked)
    };
    if (resolvedProvider && validProviders.has(resolvedProvider)) {
      payload.llm_provider = resolvedProvider;
    }

    const saveBtn = document.getElementById('editAgentSaveBtn');
    const originalText = saveBtn?.innerHTML;
    if (saveBtn) {
      saveBtn.disabled = true;
      saveBtn.innerHTML =
        '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Saving...';
    }

    try {
      const response = await fetch(
        `/api/agents/${encodeURIComponent(this.editAgentOriginalName)}`,
        {
          method: 'PATCH',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify(payload)
        }
      );

      if (!response.ok) {
        let errorMessage = 'Failed to update agent.';
        try {
          const errData = await response.json();
          errorMessage = errData.error || errData.message || errorMessage;
        } catch {
          // ignore parse errors
        }
        throw new Error(errorMessage);
      }

      const data = await response.json();
      const updatedName = data.name || newName || this.editAgentOriginalName;
      const previousName = this.editAgentOriginalName;
      this.editAgentOriginalName = updatedName;
      this.editAgentCurrentProvider = resolvedProvider || this.editAgentCurrentProvider;

      this.sessions.forEach(session => {
        if (session.agent_name === previousName) {
          session.agent_name = updatedName;
        }
      });

      this.agentModelCache.delete(previousName);
      if (model) {
        this.agentModelCache.set(updatedName, model);
      }

      this.updateCurrentAgent(updatedName);
      this.renderSessions();
      this.showEditAgentSuccess('Agent updated successfully.');

      const modalEl = document.getElementById('editAgentModal');
      const modal = modalEl ? bootstrap.Modal.getInstance(modalEl) : null;
      setTimeout(() => {
        modal?.hide();
      }, 500);
    } catch (error) {
      console.error('Failed to update agent:', error);
      this.showEditAgentError(error.message || 'Failed to update agent.');
    } finally {
      if (saveBtn) {
        saveBtn.disabled = false;
        saveBtn.innerHTML = originalText;
      }
    }
  },

  setEditAgentLoading(show, message) {
    const loadingEl = document.getElementById('editAgentModalLoading');
    const contentEl = document.getElementById('editAgentModalContent');
    if (loadingEl) {
      loadingEl.classList.toggle('d-none', !show);
      const textEl = loadingEl.querySelector('p');
      if (textEl && message) {
        textEl.textContent = message;
      }
    }
    if (contentEl) {
      contentEl.classList.toggle('d-none', show);
    }
  },

  setEditAgentFormEnabled(enabled) {
    const form = document.getElementById('editAgentForm');
    if (form) {
      form.querySelectorAll('input, select, textarea, button').forEach(element => {
        element.disabled = !enabled;
      });
    }
    const saveBtn = document.getElementById('editAgentSaveBtn');
    if (saveBtn) {
      saveBtn.disabled = !enabled;
    }
  },

  showEditAgentError(message) {
    const errorEl = document.getElementById('editAgentError');
    const successEl = document.getElementById('editAgentSuccess');
    if (errorEl) {
      errorEl.textContent = message;
      errorEl.classList.remove('d-none');
    }
    if (successEl) {
      successEl.classList.add('d-none');
    }
  },

  showEditAgentSuccess(message) {
    const errorEl = document.getElementById('editAgentError');
    const successEl = document.getElementById('editAgentSuccess');
    if (successEl) {
      successEl.textContent = message;
      successEl.classList.remove('d-none');
    }
    if (errorEl) {
      errorEl.classList.add('d-none');
    }
  },

  clearEditAgentMessages() {
    const errorEl = document.getElementById('editAgentError');
    const successEl = document.getElementById('editAgentSuccess');
    if (errorEl) {
      errorEl.textContent = '';
      errorEl.classList.add('d-none');
    }
    if (successEl) {
      successEl.classList.add('d-none');
    }
  },

  resetEditAgentModal() {
    this.editAgentOriginalName = '';
    this.editAgentCurrentProvider = '';
    this.editAgentSelectedTags = [];
    this.editAgentMCPServers = [];
    const form = document.getElementById('editAgentForm');
    if (form) {
      form.reset();
    }
    // Releases the editor's object URL and drops its listeners before the next
    // agent mounts a fresh one.
    if (this.editAgentAppearanceEditor?.destroy) this.editAgentAppearanceEditor.destroy();
    this.editAgentAppearanceEditor = null;
    const appearanceHost = document.getElementById('editAgentAppearanceHost');
    if (appearanceHost) appearanceHost.innerHTML = '';
    this.renderEditAgentTags();
    this.renderEditAgentMCPServers([]);
    this.clearEditAgentMessages();
    this.setEditAgentLoading(false);
    this.setEditAgentFormEnabled(true);
  },

  // Restore session messages to chat area
  restoreSessionMessages(session) {
    const chatArea = document.getElementById('chatArea');
    if (!chatArea) return;

    chatArea.innerHTML = '';

    const messages = session.messages || [];
    const persistedMessages = [];
    messages.forEach(msg => {
      const isUser = msg.role === 'user';
      persistedMessages.push({
        content: msg.content,
        isUser,
        timestamp: msg.created_at || new Date().toISOString()
      });
      if (typeof appendMessageToUI === 'function') {
        appendMessageToUI(msg.content, isUser);
      }
    });

    if (typeof window.replaceChatHistoryMessages === 'function') {
      window.replaceChatHistoryMessages(persistedMessages);
    }
  },

  // Delete session
  async deleteSession(sessionId) {
    if (!confirm('Are you sure you want to delete this session?')) return false;

    try {
      const response = await fetch(`/api/sessions/${sessionId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete session');

      const deletedSession = this.sessions.find(s => s.id === sessionId) || null;

      // Remove from local state
      this.sessions = this.sessions.filter(s => s.id !== sessionId);

      // If deleted session was active, switch to first session or create new
      if (sessionId === this.activeSessionId) {
        if (this.sessions.length > 0) {
          this.switchToSession(this.sessions[0].id);
        } else {
          this.activeSessionId = null;
          this.saveActiveSession();
          if (typeof clearChatHistory === 'function') {
            clearChatHistory();
          }
          this.updateChatInfoBar('', '');
          this.updateChatModelName('');
        }
      }

      this.renderSessions();
      if (window.EventBus) {
        EventBus.emit('session:deleted', { sessionId, session: deletedSession });
      }
      return true;
    } catch (error) {
      console.error('Failed to delete session:', error);
      return false;
    }
  },

  // Rename session
  async renameSession(sessionId, newTitle) {
    try {
      const response = await fetch(`/api/sessions/${sessionId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: newTitle })
      });

      if (!response.ok) throw new Error('Failed to rename session');

      // Update local state
      const session = this.sessions.find(s => s.id === sessionId);
      if (session) {
        session.title = newTitle;
        this.renderSessions();
        if (sessionId === this.activeSessionId) {
          this.updateChatInfoBar(newTitle, session.agent_name);
        }
      }
    } catch (error) {
      console.error('Failed to rename session:', error);
    }
  },

  // Move session to folder
  async moveSessionToFolder(sessionId, folderId) {
    return this.moveSessionsToFolder([sessionId], folderId);
  },

  // Move multiple sessions to folder
  async moveSessionsToFolder(sessionIds, folderId) {
    const ids = Array.from(new Set(sessionIds)).filter(Boolean);
    if (ids.length === 0) return;

    try {
      await Promise.all(
        ids.map(async id => {
          const response = await fetch(`/api/sessions/${id}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ folder_id: folderId })
          });

          if (!response.ok) throw new Error('Failed to move session');
        })
      );

      // Refresh sessions and folders
      await this.loadSessions();
      await this.loadFolders();
      return true;
    } catch (error) {
      console.error('Failed to move sessions:', error);
      this.flashDragHint('Failed to move sessions');
      return false;
    }
  },

  // Update session tags
  async updateSessionTags(sessionId, tags) {
    try {
      const response = await fetch(`/api/sessions/${sessionId}/tags`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tags })
      });

      if (!response.ok) throw new Error('Failed to update tags');

      // Update local state
      const session = this.sessions.find(s => s.id === sessionId);
      if (session) {
        session.tags = tags;
        this.renderSessions();
      }

      // Refresh tag list
      await this.loadTags();
    } catch (error) {
      console.error('Failed to update tags:', error);
    }
  },

  getWorkspaceBootstrapFromModal() {
    const description = String(
      document.getElementById('folderDescriptionInput')?.value || ''
    ).trim();
    const systems = String(document.getElementById('folderSystemsInput')?.value || '').trim();
    const context = String(document.getElementById('folderContextInput')?.value || '').trim();
    const systemsList = systems
      ? systems
          .split(/[\n,;]+/)
          .map(value => value.trim())
          .filter(Boolean)
      : [];

    return {
      hasAny: Boolean(description || systems || context),
      description,
      goal: description,
      capabilities: '',
      systems,
      systemsList,
      context
    };
  },

  extractFolderNameFromPath(pathValue) {
    const trimmed = String(pathValue || '')
      .trim()
      .replace(/[\\/]+$/, '');
    if (!trimmed) return '';
    const parts = trimmed.split(/[\\/]/);
    return parts[parts.length - 1] || '';
  },

  prefillWorkspaceNameFromImportPath(pathValue) {
    const nameInput = document.getElementById('folderNameInput');
    if (!nameInput) return;

    const folderName = this.extractFolderNameFromPath(pathValue);
    if (!folderName) return;

    const currentName = nameInput.value.trim();
    const previousAutoName = nameInput.dataset.autofillName || '';
    if (!currentName || currentName === previousAutoName) {
      nameInput.value = folderName;
      nameInput.dataset.autofillName = folderName;
    }
  },

  isPersonalHQImport() {
    return this.importModeEnabled && this.workspacePostCreateAction === 'designate_personal_hq';
  },

  setImportModeEnabled(enabled) {
    this.importModeEnabled = Boolean(enabled);
    const personalHQImport = this.isPersonalHQImport();
    const modal = document.getElementById('addFolderModal');
    if (modal) {
      modal.dataset.importMode = this.importModeEnabled ? 'true' : 'false';
    }

    const importToggle = document.getElementById('folderImportToggle');
    if (importToggle) {
      importToggle.checked = this.importModeEnabled;
    }

    const title = document.getElementById('folderModalTitle');
    if (title) {
      title.textContent = personalHQImport
        ? 'Import HQ'
        : this.importModeEnabled
          ? 'Import Folder'
          : 'Create Workspace';
    }

    const importCardTitle = document.getElementById('folderImportCardTitle');
    if (importCardTitle) {
      importCardTitle.textContent = personalHQImport ? 'Import Personal HQ' : 'Import Folder';
    }

    const importHelp = document.getElementById('folderImportHelp');
    if (importHelp) {
      importHelp.textContent = personalHQImport
        ? 'Restore an Ori workspace folder and make it your Personal HQ.'
        : 'Link a local folder so this workspace starts with real project context.';
    }

    const importPathHelp = document.getElementById('folderImportPathHelp');
    if (importPathHelp) {
      importPathHelp.textContent = personalHQImport
        ? 'Choose an Ori workspace folder. Ori will restore or import it, then designate it as your Personal HQ.'
        : 'Enter an absolute path to import. Workspace name defaults to the folder name, and you can edit it.';
    }

    const openExistingBtn = document.getElementById('folderImportOpenExistingBtn');
    if (openExistingBtn) {
      openExistingBtn.textContent = personalHQImport ? 'Use as HQ' : 'Open Existing';
    }

    const card = document.getElementById('folderImportCard');
    if (card) {
      card.hidden = !this.importModeEnabled;
    }

    const section = document.getElementById('folderImportSection');
    if (section) {
      section.hidden = !this.importModeEnabled;
    }

    const createBtn = document.getElementById('createFolderBtn');
    if (createBtn && !this.isCreatingFolder) {
      createBtn.textContent = personalHQImport
        ? 'Import HQ'
        : this.importModeEnabled
          ? 'Import Folder'
          : 'Create Workspace';
    }

    if (!this.importModeEnabled) {
      this.importAllowDuplicate = false;
      this.clearImportDuplicateWarning();
      this.scheduleTemplateAgentPlanRefresh();
    } else {
      this.resetTemplateAgentReview();
      this.resetExistingAgentTeam();
    }
    window.ProjectTemplateCard?.syncState?.();
    this.refreshWizardChrome();
  },

  clearImportDuplicateWarning() {
    const warning = document.getElementById('folderImportDuplicateWarning');
    const text = document.getElementById('folderImportDuplicateText');
    if (warning) warning.style.display = 'none';
    if (text) text.textContent = '';
    this.importDuplicateWorkspaceId = '';
    this.importDuplicateWorkspaceSlug = '';
    this.importDuplicateWorkspaceName = '';
  },

  showImportDuplicateWarning(duplicate) {
    const warning = document.getElementById('folderImportDuplicateWarning');
    const text = document.getElementById('folderImportDuplicateText');
    if (!warning || !text || !duplicate) return;

    const workspaceName = duplicate.workspace_name || duplicate.workspace_id || 'this workspace';
    text.textContent = `This folder is already linked to "${workspaceName}".`;
    warning.style.display = 'block';

    this.importDuplicateWorkspaceId = duplicate.workspace_id || '';
    this.importDuplicateWorkspaceSlug = duplicate.workspace_slug || '';
    this.importDuplicateWorkspaceName = duplicate.workspace_name || '';
  },

  async checkImportDuplicate(pathValue) {
    const path = String(pathValue || '').trim();
    if (!path || !this.importModeEnabled) {
      this.clearImportDuplicateWarning();
      return;
    }

    try {
      const response = await fetch(`/api/workspaces/import/check?path=${encodeURIComponent(path)}`);
      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success) {
        this.clearImportDuplicateWarning();
        return;
      }

      if (result.duplicate && result.duplicate.found) {
        this.showImportDuplicateWarning(result.duplicate);
      } else {
        this.clearImportDuplicateWarning();
      }
    } catch (error) {
      console.error('Failed to check import duplicate:', error);
      this.clearImportDuplicateWarning();
    }
  },

  setImportBrowseLoading(isLoading) {
    const browseBtn = document.getElementById('folderImportBrowseBtn');
    if (!browseBtn) return;

    if (isLoading) {
      browseBtn.disabled = true;
      browseBtn.dataset.originalText = browseBtn.textContent || 'Browse';
      browseBtn.textContent = 'Selecting...';
      return;
    }

    browseBtn.disabled = false;
    browseBtn.textContent = browseBtn.dataset.originalText || 'Browse';
  },

  async browseImportFolderPath() {
    const importPathInput = document.getElementById('folderImportPathInput');
    if (!importPathInput) return;

    this.setImportBrowseLoading(true);
    try {
      const response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: this.isPersonalHQImport()
            ? 'Select Personal HQ Folder'
            : 'Select Folder to Import as Workspace'
        })
      });

      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success) {
        this.showToast(result.error || 'Failed to open folder picker', 'error');
        return;
      }

      if (!result.selected || !result.path) {
        return;
      }

      importPathInput.value = result.path;
      this.importAllowDuplicate = false;
      this.clearImportDuplicateWarning();
      this.prefillWorkspaceNameFromImportPath(result.path);
      await this.checkImportDuplicate(result.path);
    } catch (error) {
      console.error('Failed to browse import path:', error);
      this.showToast('Failed to open folder picker', 'error');
    } finally {
      this.setImportBrowseLoading(false);
      importPathInput.focus();
    }
  },

  emitImportDuplicateActionTelemetry(action, pathValue) {
    const normalizedAction = String(action || '').trim();
    if (!normalizedAction) return;

    const payload = {
      action: normalizedAction,
      workspace_id: this.importDuplicateWorkspaceId || '',
      entry_point: this.importEntryPoint || 'workspace_hub_create',
      path: String(pathValue || '').trim()
    };

    const endpoint = '/api/workspaces/import/duplicate-action';
    const body = JSON.stringify(payload);
    try {
      if (navigator.sendBeacon) {
        const data = new Blob([body], { type: 'application/json' });
        navigator.sendBeacon(endpoint, data);
        return;
      }
    } catch (error) {
      console.debug('sendBeacon failed for import duplicate telemetry:', error);
    }

    fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
      keepalive: true
    }).catch(error => {
      console.debug('Failed to send import duplicate telemetry:', error);
    });
  },

  async applyWorkspacePostCreateAction(workspaceId, workspaceSlug = '') {
    const id = String(workspaceId || '').trim();
    const slug = String(workspaceSlug || '').trim();
    const action = String(this.workspacePostCreateAction || '').trim();
    const workspaceDestination = slug ? `/workspaces/${encodeURIComponent(slug)}` : '';
    if (!action) {
      return { applied: false, destination: workspaceDestination };
    }
    if (!id) {
      throw new Error('Workspace imported, but Ori could not identify it as Personal HQ.');
    }
    if (action !== 'designate_personal_hq') {
      throw new Error(`Unsupported workspace follow-up action: ${action}`);
    }

    const replaceResponse = await fetch('/api/personal-hq/replace', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workspace_id: id })
    });
    const replaceResult = await replaceResponse.json().catch(() => ({}));
    if (!replaceResponse.ok || replaceResult.error) {
      throw new Error(replaceResult.error || 'Could not designate the imported workspace as HQ.');
    }

    const stateResponse = await fetch('/api/personal-hq/onboarding-state', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ state: 'completed' })
    });
    const stateResult = await stateResponse.json().catch(() => ({}));
    if (!stateResponse.ok || stateResult.error) {
      throw new Error(
        stateResult.error || 'Personal HQ was designated, but setup could not finish.'
      );
    }

    return {
      applied: true,
      destination: '/workspaces?view=map&focus=personal-hq'
    };
  },

  resetAddWorkspaceModalForm(options = {}) {
    const { preserveAskOri = false } = options;
    // Every open starts on step 1; setImportModeEnabled(false) below re-renders
    // the wizard chrome, and an import open flips it to the single-step layout.
    this.wizardStep = 1;
    const modalElement = document.getElementById('addFolderModal');
    const nameInput = document.getElementById('folderNameInput');
    const descriptionInput = document.getElementById('folderDescriptionInput');
    const primaryGoalInput = document.getElementById('folderPrimaryGoalInput');
    const systemsInput = document.getElementById('folderSystemsInput');
    const contextInput = document.getElementById('folderContextInput');
    const parentSelect = document.getElementById('folderParentSelect');
    const keepSeedValues = Boolean(
      preserveAskOri &&
      modalElement &&
      String(modalElement.dataset.askOriPostCreate || '') === 'open_workspace_dashboard'
    );

    // Every open starts from a clean team draft; nothing staged in a previous
    // (possibly cancelled) session may leak into this one.
    this.clearWorkspaceTeamBlockAttention();
    this.discardWorkspaceTeamDraft();
    this.clearWorkspaceNameError();
    this.clearWorkspaceCreateError();
    if (!keepSeedValues) {
      if (nameInput) nameInput.value = '';
      if (nameInput) nameInput.dataset.autofillName = '';
      if (descriptionInput) descriptionInput.value = '';
      if (descriptionInput) descriptionInput.dataset.autofillDescription = '';
      if (primaryGoalInput) primaryGoalInput.value = '';
      if (systemsInput) systemsInput.value = '';
      if (contextInput) contextInput.value = '';
    }
    if (parentSelect) {
      const groupOptions = window.WorkspaceGroupOptions;
      const groups = groupOptions.collectWorkspaceGroupOptions(this.folders || []);
      parentSelect.innerHTML = groupOptions.renderWorkspaceParentOptions(groups);
      parentSelect.value = '';
      groupOptions.setWorkspaceParentSelectState(parentSelect, groups.length);
    }
    document
      .querySelectorAll('#addFolderModal .folder-color-btn')
      .forEach(btn => btn.classList.remove('active'));
    const defaultColorBtn =
      document.querySelector('#addFolderModal .folder-color-btn[data-color=""]') ||
      document.querySelector('#addFolderModal .folder-color-btn');
    if (defaultColorBtn) {
      defaultColorBtn.classList.add('active');
    }

    const importToggle = document.getElementById('folderImportToggle');
    const importPathInput = document.getElementById('folderImportPathInput');
    if (importToggle) importToggle.checked = false;
    if (importPathInput) importPathInput.value = '';
    this.importEntryPoint = 'workspace_hub_create';
    this.workspacePostCreateAction = '';
    this.importAllowDuplicate = false;
    this.setImportBrowseLoading(false);
    this.setImportModeEnabled(false);
    this.clearImportDuplicateWarning();
    if (window.WorkspaceTagsCard) window.WorkspaceTagsCard.reset();
    // Reset Agent behavior: clear any manual override and collapse the Advanced
    // disclosure before re-rendering the grid (which re-applies the default).
    this.behaviorOverridden = false;
    const advancedDisclosure = document.getElementById('folderAdvancedDisclosure');
    if (advancedDisclosure) advancedDisclosure.open = false;
    // Reset the unified Template picker back to Blank. Its reset emits a
    // selection event that re-applies the (general) behavior default and clears
    // any auto-filled name/description. A safety hint update covers the case
    // where the picker module has not loaded yet.
    this.workspaceTemplate = null;
    window.ProjectTemplateCard?.reset?.();
    this.resetTemplateAgentReview();
    this.resetExistingAgentTeam();
    this.updateBehaviorHint();
    // Refresh the folder-slug preview now that the name value is settled (the
    // ProjectTemplateCard reset above may have re-prefilled it).
    this.updateWorkspaceNameHint();
  },

  // Returns the wizard's team draft, creating it on first use. The helper module
  // is loaded ahead of this file on every surface that renders the modal, but a
  // missing global must degrade to null rather than throw mid-render.
  ensureWorkspaceTeamDraft() {
    const api = window.CreateWorkspaceTeamDraft;
    if (!api) return null;
    if (!this.teamDraft) this.teamDraft = api.createDraft();
    return this.teamDraft;
  },

  // Local-only discard: clears every staged team edit and invalidates in-flight
  // plan requests. No agent was persisted while editing, so there is nothing to
  // roll back (FR13, FR67).
  discardWorkspaceTeamDraft() {
    const api = window.CreateWorkspaceTeamDraft;
    if (api && this.teamDraft) api.resetDraft(this.teamDraft);
    else this.teamDraft = api ? api.createDraft() : null;
    // Forget what was last announced so a reopened wizard announces its primary
    // afresh rather than staying silent because the name happens to repeat.
    this.announcedPrimaryName = '';
    this.templateAgentPlanRequestId += 1;
    if (this.templateAgentPlanTimer) {
      clearTimeout(this.templateAgentPlanTimer);
      this.templateAgentPlanTimer = null;
    }
  },

  resetTemplateAgentReview() {
    this.templateAgentPlan = null;
    this.templateAgentPlanError = '';
    const draft = this.ensureWorkspaceTeamDraft();
    if (draft) window.CreateWorkspaceTeamDraft.clearPlan(draft);
    this.templateAgentPlanRequestId += 1;
    if (this.templateAgentPlanTimer) {
      clearTimeout(this.templateAgentPlanTimer);
      this.templateAgentPlanTimer = null;
    }
    this.clearTemplateAgentReviewCard();
    this.refreshWorkspaceReview();
  },

  // Restores the Advanced include-blueprint-team default. The roster itself is
  // rendered from the draft, so there is no separate DOM state to clear —
  // re-deriving is what empties it.
  //
  // Kept separate from resetTemplateAgentReview because "this blueprint declares
  // no agents" and "there is no blueprint to check" are different states that
  // must read differently on Blueprint: the first is a confirmed empty team, the
  // second has nothing to say at all. Collapsing them would make a no-agent
  // blueprint show no summary instead of saying so.
  clearTemplateAgentReviewCard() {
    const toggle = document.getElementById('templateAgentReviewToggle');
    if (toggle) toggle.checked = true;
    this.syncIncludeBlueprintTeam(true);
    this.closeTeamCustomizeEditors();
  },

  scheduleTemplateAgentPlanRefresh() {
    if (this.templateAgentPlanTimer) {
      clearTimeout(this.templateAgentPlanTimer);
    }
    this.templateAgentPlanTimer = setTimeout(() => {
      this.templateAgentPlanTimer = null;
      void this.refreshTemplateAgentPlan();
    }, 180);
  },

  // Stable identity for the currently selected blueprint. The draft compares it
  // to decide whether staged overrides belong to the blueprint still on screen:
  // a change discards them (FR21), while a retry of the same blueprint keeps them.
  currentBlueprintKey() {
    const fields = window.ProjectTemplateCard?.getPayloadFields?.() || {};
    const templateId = String(fields.template_id || '').trim();
    const templatePath = String(fields.template_path || '').trim();
    if (templatePath) return `path:${templatePath}`;
    if (templateId) return `template:${templateId}`;
    return window.ProjectTemplateCard?.getSelectedTemplate?.()?.blank ? 'blank' : '';
  },

  async refreshTemplateAgentPlan() {
    const fields = window.ProjectTemplateCard?.getPayloadFields?.() || {};
    const templateId = String(fields.template_id || '').trim();
    const templatePath = String(fields.template_path || '').trim();
    // Blank ships a synthetic single-agent roster (Workspace Manager). It has no
    // template_id/path, so signal it explicitly — but an ad-hoc folder override
    // (template_path) takes precedence and is no longer "blank".
    const isBlank =
      Boolean(window.ProjectTemplateCard?.getSelectedTemplate?.()?.blank) &&
      !templateId &&
      !templatePath;
    const requestId = ++this.templateAgentPlanRequestId;
    const blueprintKey = this.currentBlueprintKey();
    const draft = this.ensureWorkspaceTeamDraft();
    const api = window.CreateWorkspaceTeamDraft;

    if (this.importModeEnabled || (!templateId && !templatePath && !isBlank)) {
      this.resetTemplateAgentReview();
      return;
    }

    if (draft && api) api.setPlanLoading(draft, blueprintKey);
    this.setTemplateAgentReviewLoading();
    try {
      const response = await fetch('/api/workspaces/template-agent-plan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          template_id: templateId || undefined,
          template_path: templatePath || undefined,
          blank: isBlank || undefined
        })
      });
      const data = await response.json().catch(() => ({}));
      if (requestId !== this.templateAgentPlanRequestId) return;
      if (!response.ok || data.error) {
        this.renderTemplateAgentPlanError(data.error || 'Could not load blueprint agents.');
        return;
      }
      this.templateAgentPlan = data;
      if (draft && api) api.setPlanReady(draft, blueprintKey, data);
      await this.ensureEditAgentModelOptions();
      if (requestId !== this.templateAgentPlanRequestId) return;
      this.renderTemplateAgentPlan(data);
    } catch (error) {
      if (requestId !== this.templateAgentPlanRequestId) return;
      console.error('Failed to load template agent plan:', error);
      this.renderTemplateAgentPlanError('Could not load blueprint agents.');
    }
  },

  setTemplateAgentReviewLoading() {
    this.templateAgentPlan = null;
    this.templateAgentPlanError = '';
    this.closeTeamCustomizeEditors();
    this.refreshWorkspaceReview();
  },

  renderTemplateAgentPlanError(message) {
    this.templateAgentPlan = null;
    this.templateAgentPlanError = message || 'Could not load blueprint agents.';
    const errorDraft = this.ensureWorkspaceTeamDraft();
    if (errorDraft) {
      window.CreateWorkspaceTeamDraft.setPlanError(
        errorDraft,
        this.currentBlueprintKey(),
        this.templateAgentPlanError
      );
    }
    this.closeTeamCustomizeEditors();
    this.refreshWorkspaceReview();
  },

  renderTemplateAgentPlan(plan) {
    const agents = Array.isArray(plan?.agents) ? plan.agents : [];
    if (!plan?.has_agents || agents.length === 0) {
      // A blueprint that declares no agents is a CONFIRMED empty team, not an
      // absent one: reset the include-team default but keep the plan's ready
      // status so Blueprint can say so plainly.
      this.clearTemplateAgentReviewCard();
      this.refreshWorkspaceReview();
      return;
    }
    const toggle = document.getElementById('templateAgentReviewToggle');
    if (toggle) toggle.checked = this.teamView()?.includeBlueprintTeam !== false;
    this.closeTeamCustomizeEditors();
    this.refreshWorkspaceReview();
  },

  // One roster row. `entry` is a derived roster member — blueprint or saved —
  // so both sources render through the same markup and the same rules.
  renderWorkspaceTeamRow(entry, index) {
    const isPrimary = entry.designation === 'primary';
    const isBlueprint = entry.source === 'blueprint';
    const isAssistantRole = entry.source === 'assistant-program';
    const rowId = isBlueprint
      ? `team-agent-${entry.templateAgentIndex}`
      : isAssistantRole
        ? `team-assistant-${this.escapeHtml(entry.assistantRoleId || index)}`
        : `team-saved-${index}`;
    // Designation and lifecycle are both plain text, never colour or icon alone.
    const badge = isPrimary
      ? '<span class="workspace-team-badge is-primary">Primary</span>'
      : '<span class="workspace-team-badge">Specialist</span>';
    const readinessBadge =
      isBlueprint && entry.setupState === 'needsSetup'
        ? '<span class="workspace-team-readiness-badge is-missing" title="Agent setup is required">Missing</span>'
        : '';
    const meta = (
      isAssistantRole
        ? [entry.name !== entry.role ? entry.role : '', entry.modelLabel]
        : [entry.role, entry.modelLabel, entry.modelSourceLabel]
    )
      .filter(Boolean)
      .join(' · ');

    const actions = [];
    if (entry.creationFailure) {
      actions.push(
        `<button type="button" id="team-agent-retry-${entry.templateAgentIndex}" class="workspace-wizard-inline-action" data-team-agent-retry="${entry.templateAgentIndex}">Retry</button>`
      );
    } else if (entry.customizable) {
      actions.push(
        `<button type="button" id="team-agent-setup-${entry.templateAgentIndex}" class="workspace-wizard-inline-action" data-team-agent-setup="${entry.templateAgentIndex}" aria-controls="addAgentModal">${this.escapeHtml(entry.actionLabel || 'Set up agent')}</button>`
      );
    }
    if (entry.isCustomized && entry.setupAcknowledged) {
      actions.push(
        `<button type="button" class="workspace-wizard-inline-action" data-team-agent-reset="${entry.templateAgentIndex}">Reset to recommended</button>`
      );
    }
    if (entry.canMakePrimary) {
      actions.push(
        `<button type="button" class="workspace-wizard-inline-action" data-existing-agent-primary="${this.escapeHtml(entry.name)}">Make Primary</button>`
      );
    }
    if (entry.removable) {
      actions.push(
        `<button type="button" class="workspace-wizard-inline-action" data-existing-agent-remove="${this.escapeHtml(entry.name)}">Remove</button>`
      );
    }

    return `
      <li class="workspace-team-row${isPrimary ? ' is-primary' : ''}"
          id="${rowId}"
          data-agent-key="${this.escapeHtml(entry.key)}"
          ${isBlueprint ? `data-template-agent-index="${entry.templateAgentIndex}"` : ''}
          ${isAssistantRole ? `data-assistant-role="${this.escapeHtml(entry.assistantRoleId || '')}"` : ''}>
        <div class="workspace-team-row-main">
          ${this.renderAgentAvatar(entry.identity, 'workspace-agent-avatar')}
          <div class="workspace-team-row-copy">
            <strong>${this.escapeHtml(entry.name)}${badge}${readinessBadge}</strong>
            <span class="workspace-team-row-lifecycle">${this.escapeHtml(entry.statusLabel || entry.lifecycleLabel)}</span>
            ${isBlueprint ? `<small class="workspace-team-row-state">${this.escapeHtml(entry.sourceLabel)} · ${this.escapeHtml(entry.readinessLabel)} · ${this.escapeHtml(entry.futureActionLabel)}</small>` : ''}
            ${entry.description ? `<small>${this.escapeHtml(entry.description)}</small>` : ''}
            <small>${this.escapeHtml(meta)}</small>
          </div>
          <div class="workspace-team-row-actions">${actions.join('')}</div>
        </div>
      </li>`;
  },

  // The Team step's single roster: blueprint agents and manually added saved
  // agents in one list, primary first, rendered entirely from the derived team
  // view so it cannot disagree with Review or with the request payload.
  renderWorkspaceTeamRoster() {
    const list = document.getElementById('workspaceTeamRoster');
    const note = document.getElementById('workspaceTeamModelNote');
    const advanced = document.getElementById('workspaceTeamAdvanced');
    if (!list) return;

    const view = this.teamView();
    if (!view) {
      list.innerHTML = '';
      return;
    }

    list.innerHTML = view.roster
      .map((entry, index) => this.renderWorkspaceTeamRow(entry, index))
      .join('');
    if (note) {
      note.textContent = view.roster.some(entry => entry.inheritsModel)
        ? view.inheritedModelNote
        : '';
      note.hidden = !note.textContent;
    }
    // Advanced team options only make sense while a blueprint contributes a team.
    if (advanced) {
      advanced.hidden = view.isAssistantProgram || view.blueprintSummary.count === 0;
    }
  },

  renderWorkspaceTeamBatchActions() {
    const host = document.getElementById('workspaceTeamBatchActions');
    const batch = this.teamView()?.batchSetup;
    if (!host || !batch) return;
    const controls = [];
    if (batch.canAcceptAll) {
      controls.push(
        `<button type="button" class="btn btn-sm btn-outline-primary" data-team-accept-all>Use recommended setup for all ${batch.pendingCount}</button>`
      );
    }
    if (batch.acceptedCount > 0) {
      controls.push(
        `<span>${batch.acceptedCount} recommended setup${batch.acceptedCount === 1 ? '' : 's'} accepted.</span>`,
        '<button type="button" class="workspace-wizard-inline-action" data-team-undo-batch>Undo batch setup</button>'
      );
    }
    host.innerHTML = controls.join('');
    host.hidden = controls.length === 0;
  },

  acceptAllWorkspaceAgentSetups() {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    const accepted = api?.acceptAllRecommended?.(draft) || 0;
    if (accepted === 0) return;
    this.refreshWorkspaceReview();
    this.announceWorkspaceTeamChange(
      `${accepted} recommended agent setup${accepted === 1 ? '' : 's'} accepted.`
    );
    this.showToast(
      `${accepted} agent${accepted === 1 ? '' : 's'} added to the team draft. ${accepted === 1 ? 'It will' : 'They will'} be created with the workspace.`,
      'success'
    );
    document.querySelector('[data-team-undo-batch]')?.focus();
  },

  undoBatchWorkspaceAgentSetups() {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    const undone = api?.undoBatchRecommended?.(draft) || 0;
    if (undone === 0) return;
    this.refreshWorkspaceReview();
    this.announceWorkspaceTeamChange(
      `${undone} batch acceptance${undone === 1 ? '' : 's'} undone. Individual setup changes were kept.`
    );
    document.querySelector('[data-team-accept-all]')?.focus();
  },

  // Renders classified issues, blockers before advisories, each with the concrete
  // recovery actions its classification implies.
  //
  // Team and Review render the SAME issues from the same classification, so the
  // receipt can never look calmer than the step it summarizes.
  renderWorkspaceTeamIssues(hostId = 'workspaceTeamIssues') {
    const host = document.getElementById(hostId);
    if (!host) return;
    const view = this.teamView();
    if (!view) {
      host.innerHTML = '';
      return;
    }

    const ordered = [
      ...view.issues.filter(issue => issue.severity === 'blocking'),
      ...view.issues.filter(issue => issue.severity === 'loading'),
      ...view.issues.filter(issue => issue.severity === 'advisory')
    ];
    host.innerHTML = ordered
      .map(issue => {
        const actions = (issue.recovery || [])
          .map(action => {
            const label = this.teamRecoveryLabel(action);
            return label
              ? `<button type="button" class="workspace-wizard-inline-action" data-team-recovery="${this.escapeHtml(action)}">${this.escapeHtml(label)}</button>`
              : '';
          })
          .filter(Boolean)
          .join('');
        // Blockers are focus targets: attempting to continue past one moves
        // focus here so the reason is announced rather than the click seeming
        // to do nothing.
        return `
        <div class="workspace-team-issue is-${this.escapeHtml(issue.severity)}" data-issue-id="${this.escapeHtml(issue.id)}"${issue.severity === 'blocking' ? ' tabindex="-1"' : ''}>
          <span class="workspace-team-issue-label">${this.escapeHtml(this.teamIssueLabel(issue.severity))}</span>
          <span class="workspace-team-issue-text">${this.escapeHtml(issue.message)}</span>
          ${actions ? `<span class="workspace-team-issue-actions">${actions}</span>` : ''}
        </div>`;
      })
      .join('');

    host.querySelectorAll('[data-team-recovery]').forEach(button => {
      button.addEventListener('click', () =>
        this.runTeamRecovery(button.dataset.teamRecovery || '')
      );
    });
  },

  teamIssueLabel(severity) {
    switch (severity) {
      case 'blocking':
        return 'Needs attention';
      case 'loading':
        return 'Checking';
      default:
        return 'Note';
    }
  },

  teamRecoveryLabel(action) {
    switch (action) {
      case 'retry-plan':
        return 'Retry';
      case 'edit-blueprint':
        return 'Edit blueprint';
      case 'exclude-blueprint-team':
        return 'Create without blueprint team';
      case 'retry-saved-roster':
        return 'Retry';
      case 'review-template-agent-setup':
        return 'Review setup';
      case 'confirm-fresh-plan':
        return 'Confirm updated plan';
      case 'retry-creation':
        return 'Retry creation';
      default:
        return '';
    }
  },

  runTeamRecovery(action) {
    switch (action) {
      case 'retry-plan':
        void this.refreshTemplateAgentPlan();
        break;
      case 'edit-blueprint':
        this.goToWizardStep(1);
        break;
      case 'exclude-blueprint-team': {
        const toggle = document.getElementById('templateAgentReviewToggle');
        if (toggle) toggle.checked = false;
        this.syncIncludeBlueprintTeam(false);
        this.refreshWorkspaceReview();
        this.announceResolvedPrimary();
        break;
      }
      case 'retry-saved-roster':
        void this.openExistingAgentRoster();
        break;
      case 'review-template-agent-setup': {
        const index = this.teamView()?.blockingIssues.find(
          issue => issue.id === 'template-agent-setup-required'
        )?.templateAgentIndex;
        const opener = document.getElementById(`team-agent-setup-${index}`);
        if (Number.isInteger(index)) this.openWorkspaceAgentSetup(index, opener);
        break;
      }
      case 'retry-creation': {
        const index = this.teamView()?.blockingIssues.find(
          issue => issue.id === 'template-agent-creation-failed'
        )?.templateAgentIndex;
        if (Number.isInteger(index)) void this.retryWorkspaceAgentCreation(index);
        break;
      }
      case 'confirm-fresh-plan': {
        const api = window.CreateWorkspaceTeamDraft;
        const draft = this.ensureWorkspaceTeamDraft();
        if (api?.confirmFreshPlan?.(draft)) {
          this.refreshWorkspaceReview();
          this.announceWorkspaceTeamChange('Updated blueprint agent plan confirmed.');
          document.getElementById('wizardNextBtn')?.focus();
        } else {
          const index = this.teamView()?.blockingIssues.find(
            issue => issue.id === 'template-agent-setup-required'
          )?.templateAgentIndex;
          document.getElementById(`team-agent-setup-${index}`)?.focus();
        }
        break;
      }
      default:
        break;
    }
  },

  workspaceAgentSetupSummaryRows(entry) {
    const setup = entry?.recommended || entry || {};
    const tools = setup.tools && typeof setup.tools === 'object' ? setup.tools : {};
    const values = key =>
      (Array.isArray(tools[key]) ? tools[key] : [])
        .map(value => String(value || '').trim())
        .filter(Boolean);
    return [
      ['Role', setup.role || 'General'],
      ['Reasoning', setup.reasoningEffort || 'Resolved by the selected model'],
      ['Skills', values('skills').join(', ') || 'None'],
      ['MCP servers', values('mcp_servers').join(', ') || 'None'],
      ['Plugins', values('plugins').join(', ') || 'None']
    ];
  },

  openWorkspaceAgentSetup(index, opener) {
    const api = window.CreateWorkspaceTeamDraft;
    const formApi = window.AgentCreateForm;
    const draft = this.ensureWorkspaceTeamDraft();
    const entry = this.teamView()?.roster.find(item => item.templateAgentIndex === index);
    const workspaceModalElement = document.getElementById('addFolderModal');
    const agentModalElement = document.getElementById('addAgentModal');
    const host = document.getElementById('agentCreateFormHost');
    if (
      !api ||
      !formApi ||
      !draft ||
      !entry ||
      !workspaceModalElement ||
      !agentModalElement ||
      !host
    )
      return;

    this.workspaceAgentSetupIndex = index;
    this.workspaceAgentSetupOpener = opener || document.activeElement;
    this.workspaceAgentSetupCloseOptions = null;
    this.workspaceAgentSetupCompletion = null;
    this.workspaceAgentSetupFocusField = '';

    agentModalElement.dataset.agentCreateMode = 'workspace-draft';
    agentModalElement.classList.add('is-workspace-agent-draft');
    const title = document.getElementById('addAgentModalTitleText');
    if (title) title.textContent = entry.actionLabel || 'Set up agent';

    const context = document.getElementById('agentCreateDraftContext');
    const contextTitle = document.getElementById('agentCreateDraftContextTitle');
    const contextText = document.getElementById('agentCreateDraftContextText');
    if (context) context.hidden = false;
    if (contextTitle) contextTitle.textContent = `${entry.originalName} from the blueprint`;
    const portrait = document.getElementById('agentCreateDraftPortrait');
    if (portrait)
      portrait.innerHTML = this.renderAgentAvatar(entry.identity, 'workspace-agent-avatar');
    if (contextText) {
      contextText.textContent = `${entry.originalName} is proposed by ${this.teamView()?.blueprintSummary.templateName || 'this blueprint'}. Changes stay in this workspace draft until final creation.`;
    }
    const summary = document.getElementById('agentCreateDraftSummary');
    if (summary) {
      summary.innerHTML = this.workspaceAgentSetupSummaryRows(entry)
        .map(
          ([label, value]) =>
            `<div><dt>${this.escapeHtml(label)}</dt><dd>${this.escapeHtml(value)}</dd></div>`
        )
        .join('');
    }
    const error = document.getElementById('agentCreateDraftError');
    if (error) {
      error.hidden = true;
      error.textContent = '';
    }

    const recommended = entry.recommended || entry;
    const setupValues =
      entry.lifecycle === 'reuse' && !entry.planChanged
        ? {
            name: `${entry.originalName} copy`,
            type: recommended.type,
            model: recommended.model,
            provider: recommended.provider,
            reasoningEffort: recommended.reasoningEffort,
            systemPrompt: recommended.systemPrompt
          }
        : {
            name: entry.name,
            type: entry.type,
            model: entry.model,
            provider: entry.provider,
            reasoningEffort: entry.reasoningEffort,
            systemPrompt: entry.systemPrompt
          };
    this.workspaceAgentSetupForm = formApi.mount(host, {
      idPrefix: 'agent',
      profile: formApi.PROFILE_TEMPLATE,
      providers: Array.isArray(this.editAgentProvidersData) ? this.editAgentProvidersData : [],
      values: setupValues
    });

    const createButton = document.getElementById('createAgentBtn');
    if (createButton) {
      if (!createButton.dataset.workspaceDraftOriginalHtml) {
        createButton.dataset.workspaceDraftOriginalHtml = createButton.innerHTML;
      }
      createButton.textContent = entry.setupAcknowledged ? 'Save setup' : 'Add to team';
      createButton.disabled = false;
    }

    const showAgentModal = () => {
      agentModalElement.addEventListener(
        'shown.bs.modal',
        () => this.workspaceAgentSetupForm?.focus(this.workspaceAgentSetupFocusField || 'name'),
        { once: true }
      );
      bootstrap.Modal.getOrCreateInstance(agentModalElement).show();
    };
    workspaceModalElement.dataset.suspendedForAgentSetup = 'true';
    if (workspaceModalElement.classList.contains('show')) {
      workspaceModalElement.addEventListener('hidden.bs.modal', showAgentModal, { once: true });
      bootstrap.Modal.getOrCreateInstance(workspaceModalElement).hide();
    } else {
      showAgentModal();
    }
  },

  closeWorkspaceAgentSetup(options = {}) {
    if (!Number.isInteger(this.workspaceAgentSetupIndex)) return;
    const modalElement = document.getElementById('addAgentModal');
    if (modalElement?.dataset.workspaceDraftClosing === 'true') return;
    if (modalElement) modalElement.dataset.workspaceDraftClosing = 'true';
    this.workspaceAgentSetupCloseOptions = {
      announce: options.announce !== false && !options.silent,
      restoreFocus: options.restoreFocus !== false
    };
    if (modalElement?.classList.contains('show')) {
      const modal = bootstrap.Modal.getOrCreateInstance(modalElement);
      modal.hide();
      // Bootstrap ignores hide() while its opening transition is in flight.
      // Queue one retry rather than leaving a fast Save/Cancel stranded.
      if (modalElement.classList.contains('show')) {
        modalElement.addEventListener('shown.bs.modal', () => modal.hide(), { once: true });
      }
    } else {
      this.finishWorkspaceAgentSetupModal();
    }
  },

  finishWorkspaceAgentSetupModal() {
    const agentModalElement = document.getElementById('addAgentModal');
    if (agentModalElement?.dataset.agentCreateMode !== 'workspace-draft') return;

    const setupIndex = this.workspaceAgentSetupIndex;
    const opener = this.workspaceAgentSetupOpener;
    const completion = this.workspaceAgentSetupCompletion;
    const closeOptions = this.workspaceAgentSetupCloseOptions || {
      announce: true,
      restoreFocus: true
    };

    agentModalElement.classList.remove('is-workspace-agent-draft');
    delete agentModalElement.dataset.agentCreateMode;
    delete agentModalElement.dataset.workspaceDraftClosing;
    const title = document.getElementById('addAgentModalTitleText');
    if (title) title.textContent = 'Create New Agent';
    const context = document.getElementById('agentCreateDraftContext');
    if (context) context.hidden = true;
    const error = document.getElementById('agentCreateDraftError');
    if (error) {
      error.hidden = true;
      error.textContent = '';
    }
    const createButton = document.getElementById('createAgentBtn');
    if (createButton?.dataset.workspaceDraftOriginalHtml) {
      createButton.innerHTML = createButton.dataset.workspaceDraftOriginalHtml;
      delete createButton.dataset.workspaceDraftOriginalHtml;
      createButton.disabled = false;
    }
    window.resetStandaloneAgentCreateForm?.();

    this.workspaceAgentSetupIndex = null;
    this.workspaceAgentSetupOpener = null;
    this.workspaceAgentSetupForm = null;
    this.workspaceAgentSetupCloseOptions = null;
    this.workspaceAgentSetupCompletion = null;
    this.workspaceAgentSetupFocusField = '';

    const workspaceModalElement = document.getElementById('addFolderModal');
    if (workspaceModalElement) delete workspaceModalElement.dataset.suspendedForAgentSetup;
    this.refreshWorkspaceReview();

    const afterResume = () => {
      if (completion) {
        this.announceWorkspaceTeamChange(completion.announcement);
        this.showToast(completion.toast, 'success');
      } else if (closeOptions.announce) {
        this.announceWorkspaceTeamChange('Returned to the team without saving setup changes.');
      }
      if (closeOptions.restoreFocus) {
        const currentOpener = document.getElementById(`team-agent-setup-${setupIndex}`) || opener;
        currentOpener?.focus?.();
      }
    };
    if (workspaceModalElement) {
      workspaceModalElement.dataset.resumingFromAgentSetup = 'true';
      workspaceModalElement.addEventListener('shown.bs.modal', afterResume, { once: true });
      bootstrap.Modal.getOrCreateInstance(workspaceModalElement).show();
    } else {
      afterResume();
    }
  },

  saveWorkspaceAgentSetup() {
    const index = this.workspaceAgentSetupIndex;
    const form = this.workspaceAgentSetupForm;
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    const planAgent = this.planAgentAt(index);
    const draftEntry = this.teamView()?.roster.find(item => item.templateAgentIndex === index);
    if (!Number.isInteger(index) || !form || !api || !draft || !planAgent || !draftEntry) return;

    const result = form.extract();
    if (!result.valid) {
      form.focus(Object.keys(result.errors)[0] || 'name');
      return;
    }
    const values = result.values;
    const originalName = String(planAgent.name || '').trim();
    const isReuse = String(planAgent.action || '').toLowerCase() === 'reuse';
    if (
      draftEntry.staleOccupiedName &&
      api.agentKey(values.name) === api.agentKey(draftEntry.staleOccupiedName)
    ) {
      const error = document.getElementById('agentCreateDraftError');
      if (error) {
        error.textContent = `“${draftEntry.staleOccupiedName}” now belongs to a saved agent. Choose a different name for this new definition.`;
        error.hidden = false;
      }
      form.focus('name');
      return;
    }
    if (
      isReuse &&
      !draftEntry.planChanged &&
      api.agentKey(values.name) === api.agentKey(originalName)
    ) {
      const error = document.getElementById('agentCreateDraftError');
      if (error) {
        error.textContent = `Give this copy a different name from “${originalName}”. The saved agent is reused as-is and never modified.`;
        error.hidden = false;
      }
      form.focus('name');
      return;
    }

    const collision = (this.teamView()?.roster || []).some(
      entry => entry.templateAgentIndex !== index && entry.key === api.agentKey(values.name)
    );
    if (collision) {
      const error = document.getElementById('agentCreateDraftError');
      if (error) {
        error.textContent = `Another agent in this team is already called “${values.name}”. Choose another name.`;
        error.hidden = false;
      }
      form.focus('name');
      return;
    }

    const saveButton = document.getElementById('createAgentBtn');
    if (saveButton?.disabled) return;
    if (saveButton) saveButton.disabled = true;
    api.saveSetup(draft, index, values);
    const name = values.name;
    this.workspaceAgentSetupCompletion = {
      announcement:
        isReuse && api.agentKey(name) !== api.agentKey(originalName)
          ? `${name} is staged as a new agent; ${originalName} remains unchanged.`
          : `${name} setup saved for this workspace.`,
      toast: draftEntry.setupAcknowledged
        ? `${name} setup updated in the workspace draft.`
        : `${name} added to the team draft. It will be created with the workspace.`
    };
    this.closeWorkspaceAgentSetup({ restoreFocus: true, silent: true });
  },

  async retryWorkspaceAgentCreation(index) {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    if (!api?.clearCreationFailure?.(draft, index)) return;
    this.refreshWorkspaceReview();
    this.announceWorkspaceTeamChange('Retrying workspace and agent creation.');
    this.goToWizardStep(4);
    await this.createFolder();
  },

  resetWorkspaceAgentSetup(index) {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    const entry = this.teamView()?.roster.find(item => item.templateAgentIndex === index);
    if (!api || !draft || !entry || !api.resetToRecommended(draft, index)) return;
    this.refreshWorkspaceReview();
    this.announceWorkspaceTeamChange(`${entry.originalName} reset to the recommended setup.`);
    document.getElementById(`team-agent-setup-${index}`)?.focus();
  },

  // The blueprint plan entry a roster index refers to, before any staged
  // customization is applied.
  planAgentAt(index) {
    const agents = Array.isArray(this.templateAgentPlan?.agents)
      ? this.templateAgentPlan.agents
      : [];
    return Number.isInteger(index) && agents[index] ? agents[index] : null;
  },

  closeTeamCustomizeEditors() {
    this.closeWorkspaceAgentSetup({ restoreFocus: false, silent: true });
  },

  // Clears the picker's own UI. The selections themselves live in the team draft
  // and are cleared by discardWorkspaceTeamDraft(), which every modal open and
  // close already calls — so this never has to reach into draft state.
  resetExistingAgentTeam() {
    // The Your Agents picker is now permanent inline markup inside the Team step
    // rather than a panel toggled open, so a reset clears its query but never
    // hides it.
    const search = document.getElementById('existingAgentRosterSearch');
    if (search) search.value = '';
    this.closeTeamCustomizeEditors();
    this.refreshWorkspaceReview();
  },

  existingAgentKey(name) {
    return String(name || '')
      .trim()
      .toLocaleLowerCase();
  },

  findExistingRosterAgent(name) {
    const key = this.existingAgentKey(name);
    return (
      this.existingAgentRoster.find(agent => this.existingAgentKey(agent?.name) === key) || null
    );
  },

  isExistingAgentAttachable(agent) {
    return Boolean(agent?.name) && String(agent?.source || 'user').toLowerCase() !== 'cli';
  },

  // Whether the selected blueprint already contributes this agent.
  //
  // Delegates to the draft rather than re-deciding here: the draft is what
  // actually refuses the add, so if the picker judged it differently it would
  // show an enabled Add button that silently does nothing.
  templateAgentIsAlreadyIncluded(name) {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    return Boolean(draft && api && api.isBlueprintOwned(draft, name));
  },

  // Reloads the inline Your Agents picker. The panel is always present on Team,
  // so this only refetches and re-renders — it never reveals a hidden surface.
  async openExistingAgentRoster() {
    if (this.existingAgentRosterLoading) return;
    this.existingAgentRosterLoaded = false;
    await this.loadExistingAgentRoster();
  },

  // Loading Your Agents is independent of the blueprint team: a failure here is
  // advisory, so an already-valid preconfigured team stays reviewable and
  // creatable while the picker offers Retry.
  // The character catalog does NOT fetch on its own — a page that never opens
  // this wizard should pay nothing for it, so the request is made here, the one
  // place the Team step loads agent data. Without this call every chosen
  // character silently renders as the generated fallback: wired-looking, wrong.
  //
  // The fetch is deliberately not awaited. The roster paints on the data it
  // already has and portraits swap in when the catalog lands; a catalog that is
  // slow or never arrives just leaves every agent on its deterministic art.
  watchCharacterCatalog() {
    if (this.characterCatalogWatched) return;
    const catalog = window.CharacterCatalog;
    if (!catalog || typeof catalog.onChange !== 'function') return;
    this.characterCatalogWatched = true;
    catalog.onChange(() => {
      this.renderExistingAgentRoster();
      this.renderWorkspaceTeamRoster();
    });
    catalog.load();
  },

  async loadExistingAgentRoster() {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    this.watchCharacterCatalog();
    this.existingAgentRosterLoading = true;
    if (draft && api) api.setSavedRosterLoading(draft);
    this.renderExistingAgentRoster();
    try {
      const response = await fetch('/api/agents/dashboard/list?sort_by=name&order=asc');
      const data = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(data?.error || `Request failed (${response.status})`);
      const items = Array.isArray(data) ? data : Array.isArray(data?.agents) ? data.agents : [];
      if (draft && api) {
        api.setSavedRosterReady(
          draft,
          items.filter(agent => String(agent?.name || '').trim())
        );
      }
      this.existingAgentRosterLoaded = true;
    } catch (error) {
      console.warn('Failed to load existing agent roster:', error);
      if (draft && api) {
        api.setSavedRosterError(
          draft,
          'Your saved agents could not be loaded. You can still create the included workspace setup.'
        );
      }
    } finally {
      this.existingAgentRosterLoading = false;
      this.renderExistingAgentRoster();
      this.refreshWorkspaceReview();
    }
  },

  renderExistingAgentRoster() {
    const list = document.getElementById('existingAgentRosterList');
    const status = document.getElementById('existingAgentRosterStatus');
    if (!list || !status) return;
    if (this.existingAgentRosterLoading) {
      status.textContent = 'Loading your saved agents…';
      list.innerHTML = '';
      return;
    }
    if (this.existingAgentRosterError) {
      status.innerHTML = `${this.escapeHtml(this.existingAgentRosterError)} <button type="button" class="workspace-wizard-inline-action" data-existing-agent-retry>Retry</button>`;
      list.innerHTML = '';
      status.querySelector('[data-existing-agent-retry]')?.addEventListener('click', () => {
        this.existingAgentRosterLoaded = false;
        void this.loadExistingAgentRoster();
      });
      return;
    }
    const query = String(document.getElementById('existingAgentRosterSearch')?.value || '')
      .trim()
      .toLowerCase();
    const matches = this.existingAgentRoster.filter(agent => {
      const haystack =
        `${agent?.name || ''} ${agent?.role || ''} ${agent?.model || ''}`.toLowerCase();
      return !query || haystack.includes(query);
    });
    status.textContent = matches.length
      ? `${matches.length} saved agent${matches.length === 1 ? '' : 's'}`
      : 'No matching saved agents.';
    // No drag affordance: with the drop zone gone there is nothing to drop onto,
    // and a draggable card would advertise an interaction that does nothing. Add
    // is a button, so mouse and keyboard take the identical path.
    list.innerHTML = matches.map(agent => this.renderExistingAgentRosterCard(agent)).join('');
  },

  // The picker cards and the Team roster must show one agent one way, so both
  // project identity through the draft module's builder rather than each
  // reading the raw record its own way.
  // Create Workspace reviews agents; it does not define their appearance.
  //
  // Its team step picks existing agents and names blueprint ones, so every
  // avatar here is a *preview* and goes through the shared renderer (FR-88). It
  // deliberately mounts no Appearance editor: a blueprint agent has no editable
  // definition until it exists, and adding a second creation form here is
  // exactly what FR-45's "never add a second creation form" rules out. The
  // agent's appearance is edited afterwards, from Agents or its detail page.
  existingAgentIdentity(name, agent) {
    const api = window.CreateWorkspaceTeamDraft;
    return api && typeof api.identityFrom === 'function' ? api.identityFrom(name, agent) : { name };
  },

  renderExistingAgentRosterCard(agent) {
    const name = String(agent?.name || '').trim();
    const key = this.existingAgentKey(name);
    const selected = this.existingAgentSelections.some(
      selectedName => this.existingAgentKey(selectedName) === key
    );
    const alreadyIncluded = this.templateAgentIsAlreadyIncluded(name);
    const attachable = this.isExistingAgentAttachable(agent);
    const disabled = selected || alreadyIncluded || !attachable;
    const reason = selected
      ? 'Added to this workspace'
      : alreadyIncluded
        ? 'Already included by this blueprint'
        : !attachable
          ? 'Built-in CLI agents cannot be attached'
          : '';
    const workspaceCount = Number(agent?.workspace_count) || 0;
    const label = selected
      ? 'Added'
      : alreadyIncluded
        ? 'Included'
        : attachable
          ? 'Add'
          : 'Unavailable';
    // A disabled control's own label cannot explain itself, so the reason goes
    // into the accessible name too — not only into the visible small print.
    const accessibleName = reason
      ? `${label} — ${name}: ${reason}`
      : `Add ${name} to this workspace`;
    return `
      <article class="workspace-existing-agent-card" role="listitem" data-existing-agent-name="${this.escapeHtml(name)}">
        ${this.renderAgentAvatar(this.existingAgentIdentity(name, agent), 'workspace-agent-avatar')}
        <div class="workspace-existing-agent-card-copy">
          <strong>${this.escapeHtml(name)}</strong>
          <span>${this.escapeHtml(agent?.model || 'Uses saved agent model')} · ${workspaceCount === 1 ? '1 workspace' : `${workspaceCount} workspaces`}</span>
          ${reason ? `<small>${this.escapeHtml(reason)}</small>` : ''}
        </div>
        <button type="button" class="modern-btn modern-btn-secondary" data-existing-agent-add="${this.escapeHtml(name)}" ${disabled ? 'disabled' : ''} aria-label="${this.escapeHtml(accessibleName)}">${label}</button>
      </article>`;
  },

  // Create Workspace renders agent identities through the one shared renderer,
  // so an agent whose character the user chose on the Agents page has that same
  // face here — the wizard is where a team is picked, and a faceless roster is
  // exactly where a chosen identity being invisible hurts most.
  //
  // `identity` is the projection built by CreateWorkspaceTeamDraft.identityFrom;
  // a bare name string is still accepted for callers that have nothing else.
  // The caller's className keeps its own geometry by overriding --aa-size, so
  // the shared art never dictates this modal's layout.
  renderAgentAvatar(identity, className = '') {
    const input = typeof identity === 'string' ? { name: identity } : identity || {};
    // Both are deferred scripts. If either is missing the roster still needs a
    // tile in the same box, so fall back to the initials this used to draw.
    if (!window.AgentAvatar) {
      const initials =
        String(input.name || '')
          .split(/\s+/)
          .filter(Boolean)
          .slice(0, 2)
          .map(part => part[0])
          .join('')
          .toUpperCase() || 'A';
      return `<span class="${className}" aria-hidden="true">${this.escapeHtml(initials)}</span>`;
    }
    return window.AgentAvatar.markup(
      {
        name: input.name || '',
        source: input.source || 'user',
        role: input.role || '',
        // The canonical object is handed over whole. Nothing here re-derives an
        // active source from the fields that happen to be set (FR-81/FR-82).
        appearance: input.appearance || null,
        // The catalog lookup is synchronous and returns null until the fetch
        // lands; the resolver treats that as a missing character and falls
        // back, so a slow catalog never delays rendering the roster (FR-103).
        character:
          input.characterId && window.CharacterCatalog
            ? window.CharacterCatalog.get(input.characterId)
            : null
      },
      { size: 54, className }
    );
  },

  // Returns the derived team view, or null when the draft helper is unavailable.
  teamView() {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    return draft && api ? api.derive(draft) : null;
  },

  updateAssistantProgramHireDraft(fields) {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    if (!draft || !api) return;
    api.setAssistantHire(draft, fields || {});
    this.refreshWorkspaceReview();
  },

  renderAssistantProgramCreate(view) {
    const panel = document.getElementById('workspaceAssistantProgramCreate');
    const picker = document.getElementById('existingAgentRosterPanel');
    const layout = document.getElementById('workspaceTeamLayout');
    const title = document.getElementById('wizardStep3Title');
    const description = document.getElementById('wizardStep3Description');
    const advanced = document.getElementById('workspaceTeamAdvanced');
    const program = view?.assistantProgram;
    const hire = view?.assistantHire;
    const active = Boolean(view?.isAssistantProgram && program && hire);

    if (panel) panel.hidden = !active;
    if (picker) picker.hidden = active;
    layout?.classList.toggle('is-assistant-program', active);
    if (advanced && active) advanced.hidden = true;
    if (!active) {
      if (title) title.textContent = 'Build your workspace team';
      if (description) {
        description.textContent =
          'Confirm who will work in this workspace, and add saved agents if you need more.';
      }
      return;
    }

    if (title) {
      title.textContent = program.existingHired
        ? 'Connect your shared assistant team'
        : program.hireTitle || 'Hire your assistant';
    }
    if (description) {
      description.textContent = program.existingHired
        ? `This workspace will link to the existing ${program.stationName || 'assistant'} roster.`
        : program.homeAlreadyStaffed
          ? 'Reuse your group coordinator and review new roles for this workspace. Their access stays separate.'
          : program.roles.some(role => role.scope)
            ? 'Review group coordination and project roles separately. Group coordination does not grant project access.'
            : program.hireDescription || 'Name the primary assistant and review the shared roster.';
    }

    const nameInput = document.getElementById('assistantProgramCreateName');
    if (nameInput) {
      if (document.activeElement !== nameInput) nameInput.value = hire.name || '';
      nameInput.disabled = program.existingHired || program.homeAlreadyStaffed;
    }

    const providerSelect = document.getElementById('assistantProgramCreateProvider');
    if (providerSelect) {
      providerSelect.replaceChildren();
      const defaultOption = document.createElement('option');
      defaultOption.value = '';
      defaultOption.textContent = 'Use Ori default';
      providerSelect.append(defaultOption);
      for (const provider of Array.isArray(this.editAgentProvidersData)
        ? this.editAgentProvidersData
        : []) {
        const name = String(provider?.name || '').trim();
        if (
          !name ||
          name === 'default' ||
          Array.from(providerSelect.options).some(option => option.value === name)
        ) {
          continue;
        }
        const option = document.createElement('option');
        option.value = name;
        option.textContent = provider.display_name || name;
        providerSelect.append(option);
      }
      if (
        hire.provider &&
        !Array.from(providerSelect.options).some(option => option.value === hire.provider)
      ) {
        const current = document.createElement('option');
        current.value = hire.provider;
        current.textContent = `${hire.provider} (current)`;
        providerSelect.append(current);
      }
      providerSelect.value = hire.provider || '';
      providerSelect.disabled = program.existingHired;
    }

    const modelSelect = document.getElementById('assistantProgramCreateModel');
    if (modelSelect) {
      modelSelect.replaceChildren();
      const defaultModel = document.createElement('option');
      defaultModel.value = '';
      defaultModel.textContent = hire.provider ? 'Use provider default' : 'Use Ori default';
      modelSelect.append(defaultModel);
      const provider = (
        Array.isArray(this.editAgentProvidersData) ? this.editAgentProvidersData : []
      ).find(candidate => String(candidate?.name || '').trim() === hire.provider);
      for (const model of Array.isArray(provider?.models) ? provider.models : []) {
        const value = String(model?.value || model?.id || '').trim();
        if (!value || Array.from(modelSelect.options).some(option => option.value === value)) {
          continue;
        }
        const option = document.createElement('option');
        option.value = value;
        option.textContent = model.label || value;
        modelSelect.append(option);
      }
      if (
        hire.model &&
        !Array.from(modelSelect.options).some(option => option.value === hire.model)
      ) {
        const current = document.createElement('option');
        current.value = hire.model;
        current.textContent = `${hire.model} (current)`;
        modelSelect.append(current);
      }
      modelSelect.value = hire.model || '';
      modelSelect.disabled = program.existingHired || !hire.provider;
    }

    const stages = document.getElementById('assistantProgramCreateStages');
    if (stages) {
      stages.replaceChildren();
      for (const stage of program.stages || []) {
        const item = document.createElement('li');
        item.className = 'workspace-assistant-program-stage';
        const copy = document.createElement('div');
        const heading = document.createElement('strong');
        heading.textContent = stage.label;
        const body = document.createElement('span');
        body.textContent = stage.description;
        copy.append(heading, body);
        item.append(copy);
        stages.append(item);
      }
    }
  },

  addExistingAgent(name, options = {}) {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    if (!draft || !api) return;
    // The draft owns the rules: unknown, non-attachable, already-selected, and
    // blueprint-included names are all refused here rather than at each caller.
    if (!api.addSavedAgent(draft, name)) return;
    const canonicalName = String(api.findSavedAgent(draft, name)?.name || name).trim();
    this.renderExistingAgentRoster();
    this.refreshWorkspaceReview();
    // The roster itself is not a live region — it re-renders wholesale — so the
    // change is announced here, once, in words.
    if (!options.silent) {
      this.announceWorkspaceTeamChange(
        `${canonicalName} added. ${this.workspaceTeamSummaryText(this.teamView())}`
      );
      this.showToast(`${canonicalName} added to the workspace draft.`, 'success');
    }
    this.announceResolvedPrimary();
  },

  removeExistingAgent(name) {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    if (!draft || !api) return;
    if (!api.removeSavedAgent(draft, name)) return;
    this.renderExistingAgentRoster();
    this.refreshWorkspaceReview();
    this.announceWorkspaceTeamChange(
      `${String(name).trim()} removed. ${this.workspaceTeamSummaryText(this.teamView())}`
    );
    // Removing a member can hand the primary slot to someone else; say so.
    this.announceResolvedPrimary();
  },

  requestExistingAgentPrimary(name) {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    if (!draft || !api) return;
    if (!api.setExplicitPrimary(draft, name)) return;
    this.refreshWorkspaceReview();
    const canonicalName = String(api.findSavedAgent(draft, name)?.name || name).trim();
    this.announceWorkspaceTeamChange(
      `${canonicalName} is now this workspace's primary agent. The previous primary stays attached as a specialist.`
    );
  },

  // Announces the effective primary after a roster change, but only when it
  // actually moved, so repeated edits don't repeat the same message.
  announceResolvedPrimary() {
    const view = this.teamView();
    const primary = view ? view.primaryName : '';
    if (this.announcedPrimaryName === primary) return;
    this.announcedPrimaryName = primary;
    if (!primary) return;
    this.announceWorkspaceTeamChange(`${primary} is now this workspace's primary agent.`);
  },
  announcedPrimaryName: '',

  // Mirrors the Advanced include-blueprint-team checkbox into the draft. The
  // checkbox is the input; the draft is what every derived view reads, so they
  // must not be allowed to drift.
  syncIncludeBlueprintTeam(included) {
    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    if (draft && api) api.setIncludeBlueprintTeam(draft, included);
  },

  includedTemplateAgents() {
    const included = Boolean(document.getElementById('templateAgentReviewToggle')?.checked);
    return included && Array.isArray(this.templateAgentPlan?.agents)
      ? this.templateAgentPlan.agents
      : [];
  },

  includedTemplateHasEntryAgent() {
    return this.includedTemplateAgents().some(agent => Boolean(agent?.entry_point));
  },

  // The EFFECTIVE primary: an explicit choice, else the blueprint's declared
  // entry agent, else the first selected saved agent. Derived, never stored.
  resolvedWorkspacePrimaryName() {
    const view = this.teamView();
    return view ? view.primaryName : '';
  },

  // Blueprint's read-only agent summary. It reports what the selected blueprint
  // will contribute — how many agents, their names, and which one it declares as
  // primary — and nothing else: every agent action lives on Team.
  //
  // The four states are deliberately distinct sentences rather than shades of one
  // message. "Checking" must never be mistakable for "this blueprint has no
  // agents", and neither may be mistaken for "we could not check".
  renderBlueprintAgentSummary() {
    const section = document.getElementById('blueprintAgentSummary');
    const text = document.getElementById('blueprintAgentSummaryText');
    const hint = document.getElementById('blueprintAgentSummaryHint');
    if (!section || !text || !hint) return;

    const api = window.CreateWorkspaceTeamDraft;
    const draft = this.ensureWorkspaceTeamDraft();
    const summary = draft && api ? api.derive(draft).blueprintSummary : null;

    // Import mode and "no blueprint resolved yet" have nothing to summarize.
    if (this.importModeEnabled || !summary || summary.status === 'idle') {
      section.hidden = true;
      text.textContent = '';
      return;
    }

    section.hidden = false;
    section.classList.remove('is-loading', 'is-empty', 'is-error');
    hint.hidden = false;

    if (summary.status === 'loading') {
      section.classList.add('is-loading');
      text.textContent = 'Checking this blueprint’s agents…';
      return;
    }
    if (summary.status === 'error') {
      section.classList.add('is-error');
      text.textContent =
        'This blueprint’s agents could not be checked. You can retry in step 3, or continue without its team.';
      return;
    }
    if (summary.count === 0) {
      section.classList.add('is-empty');
      text.textContent =
        'This blueprint includes no agents. You can add saved agents to the team in step 3.';
      return;
    }

    // Names carry the primary marker inline so the sentence is complete on its
    // own — no chip, colour, or icon is load-bearing.
    const primaryKey = api.agentKey(summary.declaredPrimary);
    const names = summary.names.map(agentName =>
      api.agentKey(agentName) === primaryKey ? `${agentName} (primary)` : agentName
    );
    text.textContent = `Includes ${summary.count} agent${summary.count === 1 ? '' : 's'}: ${names.join(', ')}.`;
  },

  refreshWorkspaceReview() {
    const summary = document.getElementById('workspaceReviewSummary');
    const heading = document.getElementById('workspaceTeamHeading');
    const teamSummary = document.getElementById('workspaceTeamSummary');
    const selectedTemplate = window.ProjectTemplateCard?.getSelectedTemplate?.();
    const name = String(document.getElementById('folderNameInput')?.value || '').trim();
    const view = this.teamView();
    const roster = view ? view.roster : [];

    this.renderAssistantProgramCreate(view);
    if (heading) {
      // "Workspace Assistant" is a retired product label (Issue #350); this
      // heading describes the roster, so it says what the roster is.
      heading.textContent = view?.isAssistantProgram
        ? view.assistantProgram.stationName || 'Shared assistant team'
        : roster.length === 1
          ? 'Entry Assistant'
          : 'Workspace Team';
    }
    if (teamSummary) teamSummary.textContent = this.workspaceTeamSummaryText(view);
    if (summary) summary.innerHTML = this.renderWorkspaceReceipt(view, selectedTemplate, name);
    this.renderSetupPreview(selectedTemplate);
    this.renderWorkspaceTeamIssues();
    this.renderWorkspaceTeamIssues('workspaceReviewIssues');
    this.renderWorkspaceTeamBatchActions();
    this.renderWorkspaceTeamRoster();
    this.renderBlueprintAgentSummary();
    window.SetupWorkspaceCreator?.refreshReview();
  },

  // Review's receipt: what will exist, stated once, with a route back to the step
  // that owns each value. Deliberately read-only — every control that could
  // change something lives on the step it belongs to.
  renderWorkspaceReceipt(view, selectedTemplate, name) {
    const blueprint =
      selectedTemplate?.blank || !selectedTemplate?.name
        ? 'Blank workspace'
        : selectedTemplate.name;
    const slug = name ? this.slugifyWorkspaceName(name) : '';
    // The workspace name is the primary value; the blueprint is where it came
    // from, so it reads as provenance rather than a second, equal identity.
    const provenance = [
      `Based on ${this.escapeHtml(blueprint)}`,
      slug ? `Folder: ${this.escapeHtml(slug)}` : ''
    ]
      .filter(Boolean)
      .join(' · ');

    const owner = this.workspaceReceiptOwnerLine(selectedTemplate);
    const sessionRecovery = this.workspaceReceiptSessionRecoveryLine();

    const identity = `
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <strong class="workspace-review-identity-name">${this.escapeHtml(name || 'Untitled workspace')}</strong>
          <span class="workspace-review-card-meta">${provenance}</span>
          ${owner ? `<span class="workspace-review-card-meta">${this.escapeHtml(owner)}</span>` : ''}
          ${sessionRecovery ? `<span class="workspace-review-card-note">${this.escapeHtml(sessionRecovery)}</span>` : ''}
        </div>
        <div class="workspace-review-card-actions">
          <button type="button" class="workspace-wizard-inline-action" data-wizard-edit-step="2">Edit</button>
          <button type="button" class="workspace-wizard-inline-action" data-wizard-edit-step="1">Change blueprint</button>
        </div>
      </div>`;

    const choices = this.workspaceReviewDetailChoices();
    const details = choices.length
      ? `
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">Details</span>
          <span class="workspace-review-card-meta">${this.escapeHtml(choices.join(' · '))}</span>
        </div>
        <div class="workspace-review-card-actions">
          <button type="button" class="workspace-wizard-inline-action" data-wizard-edit-step="2">Edit</button>
        </div>
      </div>`
      : '';

    return identity + details + this.renderWorkspaceReceiptTeam(view);
  },

  // Names who owns the selected blueprint and, where one is tracked, its
  // version — so Review states provenance precisely rather than leaving
  // "Based on X" to imply the workspace's own template is doing the work.
  // '' for Blank and for an ad-hoc folder path, which own nothing to name.
  workspaceReceiptOwnerLine(template) {
    if (!template || template.blank) return '';
    const owner = template.plugin_owner;
    if (owner && owner.plugin_id) {
      const version = String(owner.plugin_version || '').trim();
      return version
        ? `Owner: ${owner.plugin_id} plugin (v${version})`
        : `Owner: ${owner.plugin_id} plugin`;
    }
    if (template.builtin) {
      const version = Number(template.builtin_version) || 0;
      return version > 0 ? `Owner: Built-in blueprint (v${version})` : 'Owner: Built-in blueprint';
    }
    return 'Owner: Your template';
  },

  // States which plugin dependency this wizard session explicitly installed
  // or enabled, when the selected blueprint has one. Read from the picker's
  // own session log rather than re-derived from current readiness: a plugin
  // that is now ready looks identical whether it was already there or the
  // user just switched it on a moment ago, and that distinction is the one
  // this line exists to preserve.
  workspaceReceiptSessionRecoveryLine() {
    const record = window.ProjectTemplateCard?.getSelectedSessionRecovery?.();
    if (!record || !record.pluginName) return '';
    const verb = record.action === 'install_plugin' ? 'Installed and enabled' : 'Enabled';
    return record.completed
      ? `${verb} ${record.pluginName} during this session.`
      : `Started enabling ${record.pluginName} this session — not finished yet.`;
  },

  // Names the primary and accounts for the specialists — the two facts that
  // decide whether this is the team the user meant. Never the roster editor.
  renderWorkspaceReceiptTeam(view) {
    const roster = view ? view.roster : [];
    const specialists = roster.filter(entry => entry.designation === 'specialist');
    const primary = roster.find(entry => entry.designation === 'primary');

    const lines = [];
    if (primary) {
      lines.push(`${primary.name} · Primary`);
    } else {
      lines.push('No primary agent');
    }
    if (specialists.length) {
      // Name them while the list is short enough to be worth reading; past that
      // the count is the useful fact.
      lines.push(
        specialists.length <= 3
          ? `${specialists.length} specialist${specialists.length === 1 ? '' : 's'}: ${specialists.map(entry => entry.name).join(', ')}`
          : `${specialists.length} specialists`
      );
    }
    if (!view?.isAssistantProgram) {
      const savedCount = roster.filter(entry => entry.lifecycle === 'reuse').length;
      const copyCount = roster.filter(entry => entry.lifecycle === 'customized-copy').length;
      const newCount = roster.filter(entry => entry.lifecycle === 'create').length;
      lines.push(
        `${newCount} new · ${savedCount} saved · ${copyCount} customized cop${copyCount === 1 ? 'y' : 'ies'}`
      );
      roster.forEach(entry => {
        lines.push(
          [
            entry.name,
            entry.designation === 'primary' ? 'Primary' : 'Specialist',
            entry.statusLabel || entry.lifecycleLabel,
            entry.modelLabel,
            entry.modelSourceLabel
          ]
            .filter(Boolean)
            .join(' · ')
        );
      });
    }
    lines.push(this.workspaceTeamSummaryText(view));

    return `
      <div class="workspace-review-card">
        <div class="workspace-review-card-main">
          <span class="workspace-review-card-label">Team</span>
          ${lines.map(line => `<span class="workspace-review-card-meta">${this.escapeHtml(line)}</span>`).join('')}
        </div>
        <div class="workspace-review-card-actions">
          <button type="button" class="workspace-wizard-inline-action" data-wizard-edit-step="3">Edit team</button>
        </div>
      </div>`;
  },

  // Only choices that materially change what gets created, and only when they
  // differ from the default. A receipt listing every default would bury the few
  // things the user actually decided.
  workspaceReviewDetailChoices() {
    const choices = [];

    const parentSelect = document.getElementById('folderParentSelect');
    if (parentSelect && parentSelect.value) {
      const label = parentSelect.options[parentSelect.selectedIndex]?.textContent?.trim();
      if (label) choices.push(`Group: ${label}`);
    }

    if (window.ProjectTemplateCard?.shouldOpenAfterCreate?.()) {
      choices.push('Opens the project after creation');
    }

    const profile = document.getElementById('folderPresetSelect')?.value || 'general';
    if (profile !== 'general') {
      choices.push(`Agent behavior: ${this.behaviorProfileLabel(profile)}`);
    }

    const templatePath = String(
      document.getElementById('projectTemplatePathInput')?.value || ''
    ).trim();
    if (templatePath) choices.push(`Folder template: ${templatePath}`);

    const tagCount = window.WorkspaceTagsCard?.getPayloadFields?.().tags?.length || 0;
    if (tagCount > 0) choices.push(`${tagCount} tag${tagCount === 1 ? '' : 's'}`);

    return choices;
  },

  // Counts what the request will actually do, in future tense. Grouped by
  // lifecycle rather than by source, because "will be created" versus "will be
  // attached" is the distinction that matters before the workspace exists.
  workspaceTeamSummaryText(view) {
    const roster = view ? view.roster : [];
    // The consequence is spelled out by the advisory issue below the summary;
    // repeating it here would say the same thing twice in two shapes.
    if (roster.length === 0) return 'No agent will be attached to this workspace.';
    if (view?.isAssistantProgram) {
      if (view.assistantProgram.roles.some(role => role.scope)) {
        const groupRoles = roster.filter(entry => entry.assistantScope === 'home');
        const projectRoles = roster.filter(entry => entry.assistantScope === 'project');
        return `${groupRoles.length} group role${groupRoles.length === 1 ? '' : 's'} and ${projectRoles.length} new project-only role${projectRoles.length === 1 ? '' : 's'}. Existing group roles stay in the group; access stays separate.`;
      }
      const action = view.assistantProgram.existingHired
        ? 'linked to this workspace'
        : 'created and linked';
      return `${roster.length} shared assistant role${roster.length === 1 ? '' : 's'} will be ${action}.`;
    }
    const created = roster.filter(
      entry => entry.lifecycle === 'create' || entry.lifecycle === 'customized-copy'
    ).length;
    const attached = roster.length - created;
    const parts = [];
    if (attached) {
      parts.push(`${attached} saved agent${attached === 1 ? '' : 's'} will be attached`);
    }
    if (created) {
      parts.push(
        `${created} new reusable agent${created === 1 ? '' : 's'} will be created and attached`
      );
    }
    return parts.join(' · ');
  },

  // Converts the reviewed request into an authoritative success summary. A
  // successful strict create means every `create` expectation became a new
  // reusable definition and every `reuse`/existing selection was attached.
  // Keep this aggregate so a large blueprint produces one useful toast instead
  // of a burst of per-agent notifications.
  workspaceTeamSuccessParts(teamPayload) {
    const expectations = Array.isArray(teamPayload?.template_agent_review?.expectations)
      ? teamPayload.template_agent_review.expectations
      : [];
    const created = [];
    const attached = [];
    const seenCreated = new Set();
    const seenAttached = new Set();
    const addName = (target, seen, rawName) => {
      const name = String(rawName || '').trim();
      const key = name.toLocaleLowerCase();
      if (!name || seen.has(key)) return;
      seen.add(key);
      target.push(name);
    };

    for (const expectation of expectations) {
      if (expectation?.action === 'create') {
        addName(created, seenCreated, expectation.name);
      } else if (expectation?.action === 'reuse') {
        addName(attached, seenAttached, expectation.name);
      }
    }
    for (const name of teamPayload?.existing_agent_names || []) {
      addName(attached, seenAttached, name);
    }

    const parts = [];
    if (created.length === 1) parts.push(`${created[0]} created and added`);
    else if (created.length > 1) parts.push(`${created.length} agents created and added`);
    if (attached.length === 1) parts.push(`${attached[0]} added`);
    else if (attached.length > 1) parts.push(`${attached.length} saved agents added`);
    return parts;
  },

  /**
   * Renders the blueprint's setup requirements in the review step.
   *
   * Disclosure only. It reads the template's already-normalized manifest and
   * writes text: no path is expanded, no picker opens, no connector is
   * authorized, no plugin is installed or enabled, and no watcher or schedule
   * is created. Everything it names happens later, in the workspace's own
   * Setup Wizard, after the user has a workspace to set up.
   */
  renderSetupPreview(template) {
    const panel = document.getElementById('workspaceSetupPreview');
    const list = document.getElementById('workspaceSetupPreviewList');
    const note = document.getElementById('workspaceSetupPreviewNote');
    const eyebrow = document.getElementById('workspaceSetupPreviewEyebrow');
    const title = document.getElementById('workspaceSetupPreviewTitle');
    if (!panel || !list || !note) return;

    const runtime = template?.runtime_requirements || null;
    const runtimeInvalid = String(template?.runtime_requirements_error || '').trim();
    const steps = template?.setup_wizard?.steps || [];
    const wizardInvalid = String(template?.setup_wizard_error || '').trim();
    list.textContent = '';

    const renderInvalid = (kind, diagnostic) => {
      panel.hidden = false;
      if (eyebrow) eyebrow.textContent = 'Blueprint needs attention';
      if (title) title.textContent = `${kind} cannot be read`;
      const item = document.createElement('li');
      item.className = 'workspace-setup-preview-item is-error';
      item.textContent = `This blueprint's ${kind.toLowerCase()} cannot be read, so a workspace cannot be created from it: ${diagnostic}`;
      list.appendChild(item);
      note.textContent = 'Fix the blueprint\u2019s template.json, then try again.';
    };

    if (runtimeInvalid) {
      renderInvalid('Runtime requirements', runtimeInvalid);
      return;
    }
    if (wizardInvalid) {
      renderInvalid('Setup', wizardInvalid);
      return;
    }

    if (runtime) {
      const modes = Array.isArray(runtime.operating_modes) ? runtime.operating_modes : [];
      const requirements = Array.isArray(runtime.requirements) ? runtime.requirements : [];
      const requirementsByKey = new Map(
        requirements.map(requirement => [
          String(requirement?.key || '')
            .trim()
            .toLowerCase(),
          requirement
        ])
      );

      panel.hidden = false;
      if (eyebrow) eyebrow.textContent = 'Supported operating modes';
      if (title) title.textContent = 'What works now and what needs setup';

      if (!modes.length) {
        const item = document.createElement('li');
        item.className = 'workspace-setup-preview-item is-error';
        item.textContent =
          'This blueprint declares a runtime contract with no usable operating modes.';
        list.appendChild(item);
        note.textContent = 'Fix the blueprint\u2019s template.json, then try again.';
        return;
      }

      const immediateMode = modes.find(
        mode => !Array.isArray(mode?.requires) || mode.requires.length === 0
      );
      const sharedImmediate = String(immediateMode?.description || '').trim();

      modes.forEach(mode => {
        const references = Array.isArray(mode?.requires) ? mode.requires : [];
        const item = document.createElement('li');
        item.className = 'workspace-setup-preview-item workspace-runtime-mode-preview';

        const header = document.createElement('div');
        header.className = 'workspace-runtime-mode-head';
        const label = document.createElement('span');
        label.className = 'workspace-setup-preview-label';
        label.textContent = String(mode?.label || mode?.id || 'Operating mode').trim();
        header.appendChild(label);
        const badge = document.createElement('span');
        badge.className = 'workspace-setup-preview-badge';
        badge.textContent = 'Mode';
        header.appendChild(badge);
        item.appendChild(header);

        const immediateLabel = document.createElement('span');
        immediateLabel.className = 'workspace-runtime-preview-kicker';
        immediateLabel.textContent = 'Works immediately';
        item.appendChild(immediateLabel);
        const immediate = document.createElement('span');
        immediate.className = 'workspace-setup-preview-detail';
        immediate.textContent =
          references.length && sharedImmediate
            ? `Project-file work remains available: ${sharedImmediate}`
            : String(mode?.description || '').trim();
        item.appendChild(immediate);

        const setupLabel = document.createElement('span');
        setupLabel.className = 'workspace-runtime-preview-kicker';
        setupLabel.textContent = 'Setup after creation';
        item.appendChild(setupLabel);

        if (!references.length) {
          const none = document.createElement('span');
          none.className = 'workspace-setup-preview-detail';
          none.textContent = 'No additional runtime setup.';
          item.appendChild(none);
        } else {
          const requirementList = document.createElement('ul');
          requirementList.className = 'workspace-runtime-requirement-list';
          references.forEach(rawKey => {
            const key = String(rawKey || '')
              .trim()
              .toLowerCase();
            const requirement = requirementsByKey.get(key);
            const requirementItem = document.createElement('li');
            requirementItem.className = 'workspace-runtime-requirement';
            const requirementLabel = document.createElement('span');
            requirementLabel.className = 'workspace-runtime-requirement-label';
            requirementLabel.textContent = String(
              requirement?.label || key || 'Requirement'
            ).trim();
            requirementItem.appendChild(requirementLabel);
            const description = String(requirement?.description || '').trim();
            if (description) {
              const requirementDetail = document.createElement('span');
              requirementDetail.className = 'workspace-setup-preview-detail';
              requirementDetail.textContent = description;
              requirementItem.appendChild(requirementDetail);
            }
            const disclosure = String(requirement?.disclosure || '').trim();
            if (disclosure) {
              const requirementDisclosure = document.createElement('span');
              requirementDisclosure.className = 'workspace-runtime-requirement-disclosure';
              requirementDisclosure.textContent = disclosure;
              requirementItem.appendChild(requirementDisclosure);
            }
            requirementList.appendChild(requirementItem);
          });
          item.appendChild(requirementList);
        }
        list.appendChild(item);
      });
      note.textContent =
        'This is a preview only. Nothing is checked, connected, installed, launched, tested, or granted during creation.';
      return;
    }

    if (!steps.length) {
      // A blueprint with neither contract nor wizard shows no empty setup panel.
      panel.hidden = true;
      note.textContent = '';
      return;
    }

    panel.hidden = false;
    if (eyebrow) eyebrow.textContent = 'After you create it';
    if (title) title.textContent = 'Setup this blueprint will ask for';
    steps.forEach(step => {
      const item = document.createElement('li');
      item.className = 'workspace-setup-preview-item';
      const label = document.createElement('span');
      label.className = 'workspace-setup-preview-label';
      label.textContent = step?.title || this.setupPreviewFallbackLabel(step);
      item.appendChild(label);
      const badge = document.createElement('span');
      badge.className = step?.required
        ? 'workspace-setup-preview-badge is-required'
        : 'workspace-setup-preview-badge';
      badge.textContent = step?.required ? 'Required' : 'Optional';
      item.appendChild(badge);
      const detail = String(step?.description || '').trim();
      if (detail) {
        const description = document.createElement('span');
        description.className = 'workspace-setup-preview-detail';
        description.textContent = detail;
        item.appendChild(description);
      }
      list.appendChild(item);
    });
    note.textContent =
      'Setup continues after you create the workspace \u2014 nothing here is connected, installed, or granted yet.';
  },

  setupPreviewFallbackLabel(step) {
    switch (String(step?.kind || '')) {
      case 'directory':
        return 'Choose a folder';
      case 'automation_review':
        return 'Review automation';
      case 'capability_connect':
        return 'Connect a service';
      case 'capability_configure':
        return 'Configure a service';
      case 'account_link':
        return 'Link an account';
      case 'plugin_readiness':
        return 'Prepare a plugin';
      case 'readiness':
        return 'Readiness check';
      case 'runtime_mode':
        return 'Choose an operating mode';
      case 'runtime_readiness':
        return 'Check a runtime requirement';
      case 'summary':
        return 'Summary';
      default:
        return 'Setup step';
    }
  },

  // Clears the short-lived visual guide used after Team refuses to advance.
  // The class is state-neutral: it identifies where to act without changing the
  // blocker, button availability, or draft.
  clearWorkspaceTeamBlockAttention() {
    if (this.workspaceTeamBlockAttentionTimer) {
      clearTimeout(this.workspaceTeamBlockAttentionTimer);
      this.workspaceTeamBlockAttentionTimer = null;
    }
    document
      .querySelectorAll('#wizardStep3 .is-blocking-attention')
      .forEach(element => element.classList.remove('is-blocking-attention'));
  },

  // When Review is blocked, bring the exact corrective control into view and
  // briefly pulse it together with its roster row. Focus and the live-region
  // message provide the same direction without motion; reduced-motion users
  // receive a static outline instead of the pulse.
  signalWorkspaceTeamBlock(target, message) {
    if (!target) return;
    this.clearWorkspaceTeamBlockAttention();

    const owner = target.closest?.('.workspace-team-row, .workspace-team-issue');
    const attentionTargets = Array.from(new Set([target, owner].filter(Boolean)));
    // Force style recalculation so clicking Review repeatedly restarts the cue.
    void target.offsetWidth;
    attentionTargets.forEach(element => element.classList.add('is-blocking-attention'));

    const reduceMotion = Boolean(window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches);
    target.focus({ preventScroll: true });
    target.scrollIntoView?.({
      behavior: reduceMotion ? 'auto' : 'smooth',
      block: 'center',
      inline: 'nearest'
    });
    this.announceWorkspaceTeamChange(
      `${message || 'This team needs attention before review.'} Focus moved to the required control.`
    );

    this.workspaceTeamBlockAttentionTimer = setTimeout(() => {
      this.workspaceTeamBlockAttentionTimer = null;
      attentionTargets.forEach(element => element.classList.remove('is-blocking-attention'));
    }, 2000);
  },
  workspaceTeamBlockAttentionTimer: null,

  // Announces a team change without moving focus.
  //
  // One interaction can produce several changes — adding an agent can also hand
  // over the primary slot — so messages are collected and flushed together.
  // Writing them one after another would overwrite the first before a screen
  // reader ever read it.
  announceWorkspaceTeamChange(message) {
    const region = document.getElementById('workspaceTeamLiveRegion');
    if (!region || !message) return;
    this.pendingTeamAnnouncements = this.pendingTeamAnnouncements || [];
    this.pendingTeamAnnouncements.push(String(message).trim());
    if (this.teamAnnouncementTimer) return;
    this.teamAnnouncementTimer = setTimeout(() => {
      this.teamAnnouncementTimer = null;
      const text = (this.pendingTeamAnnouncements || []).filter(Boolean).join(' ');
      this.pendingTeamAnnouncements = [];
      region.textContent = text;
    }, 0);
  },
  pendingTeamAnnouncements: null,
  teamAnnouncementTimer: null,

  // Excluding the blueprint team removes its entries from the roster entirely
  // rather than greying them out, so there is nothing left to disable — the
  // re-render is the state change.
  updateTemplateAgentReviewDisabledState() {
    this.closeTeamCustomizeEditors();
  },

  // Fills a create-modal input from a template default while tracking the
  // filled value in a dataset attribute. This lets a later Starting-point
  // switch replace a value the modal auto-filled, while never clobbering input
  // the user typed themselves. An empty value (e.g. Blank) clears a previously
  // auto-filled field but leaves typed input alone.
  prefillTemplateValue(input, value, autofillAttr) {
    if (!input) return;
    const next = value || '';
    const prevAuto = input.dataset[autofillAttr] || '';
    if (input.value === '' || input.value === prevAuto) {
      input.value = next;
      input.dataset[autofillAttr] = next;
    }
  },

  // Friendly label for an Agent behavior profile value.
  behaviorProfileLabel(profile) {
    switch (profile) {
      case 'research':
        return 'Research';
      case 'software_project':
        return 'Software Project';
      case 'general':
      default:
        return 'General';
    }
  },

  // Refreshes the collapsed "Agent behavior: X" hint from the select value,
  // appending any non-default Group/Color/Tags picks so the collapsed Advanced
  // section reflects what's set inside it.
  updateBehaviorHint() {
    const hint = document.getElementById('folderBehaviorHint');
    if (!hint) return;
    const profile = document.getElementById('folderPresetSelect')?.value || 'general';
    const parts = [`Agent behavior: ${this.behaviorProfileLabel(profile)}`];

    const parentSelect = document.getElementById('folderParentSelect');
    if (parentSelect && parentSelect.value) {
      const label = parentSelect.options[parentSelect.selectedIndex]?.textContent?.trim();
      if (label) parts.push(`Group: ${label}`);
    }

    const activeColor = document.querySelector('#addFolderModal .folder-color-btn.active');
    if (activeColor && String(activeColor.dataset.color || '').trim()) {
      parts.push('Color set');
    }

    const tagCount = window.WorkspaceTagsCard?.getPayloadFields?.().tags?.length || 0;
    if (tagCount > 0) parts.push(`${tagCount} tag${tagCount === 1 ? '' : 's'}`);

    hint.textContent = parts.join(' · ');
  },

  // ----- Create-workspace wizard (Blueprint → Details → Team → Review) -----

  // Total Create-mode steps. Import mode is deliberately outside this state
  // machine: it renders the Details layout only and never reaches Team/Review.
  wizardStepCount: 4,

  // Renders the wizard chrome for the current mode + step. Import remains a
  // single-step workflow and never exposes Create-only progress or review UI.
  refreshWizardChrome() {
    const importMode = Boolean(this.importModeEnabled);
    const step = importMode ? 2 : this.wizardStep;

    const sections = [1, 2, 3, 4].map(index => document.getElementById(`wizardStep${index}`));
    const stepper = document.getElementById('wizardStepper');
    const backBtn = document.getElementById('wizardBackBtn');
    const nextBtn = document.getElementById('wizardNextBtn');
    const createBtn = document.getElementById('createFolderBtn');

    this.updateWorkspaceNameHint();

    sections.forEach((section, offset) => {
      if (!section) return;
      const sectionStep = offset + 1;
      // Import mode shows only the Details layout; every Create-only step stays
      // hidden so its controls are unreachable rather than merely off-screen.
      section.hidden = sectionStep === 2 ? step !== 2 : importMode || step !== sectionStep;
      section.setAttribute('aria-hidden', String(section.hidden));
    });
    if (stepper) {
      stepper.hidden = importMode;
      stepper.setAttribute('aria-hidden', String(importMode));
    }
    stepper?.querySelectorAll('.workspace-create-step').forEach(el => {
      const current = !importMode && String(el.dataset.step || '') === String(step);
      el.classList.toggle('is-active', current);
      if (current) el.setAttribute('aria-current', 'step');
      else el.removeAttribute('aria-current');
    });

    const onStep1 = !importMode && step === 1;
    const onFinalStep = !importMode && step === this.wizardStepCount;
    if (nextBtn) {
      nextBtn.hidden = importMode || onFinalStep;
      // Continue through Blueprint and Details; the last hop names its target so
      // the user knows the next screen confirms rather than configures.
      nextBtn.textContent = step === 3 ? 'Review →' : 'Continue →';
    }
    if (backBtn) backBtn.hidden = importMode || onStep1;
    if (createBtn) {
      // The final create action exists only on Review (or in import mode).
      createBtn.hidden = !importMode && !onFinalStep;
      if (!this.isCreatingFolder) {
        createBtn.textContent = importMode
          ? this.isPersonalHQImport()
            ? 'Import HQ'
            : 'Import Folder'
          : this.workspaceCreateCtaLabel();
        // A known blocker means no trustworthy request can be built yet, so the
        // final action is unavailable until it is resolved. An unresolved
        // blueprint dependency counts: the server would refuse the request, and
        // offering Create anyway turns a knowable state into a failed attempt.
        createBtn.disabled =
          !importMode &&
          (this.hasBlockingTeamIssue() ||
            this.blueprintSelectionBlocked() ||
            window.SetupWorkspaceCreator?.canSubmit() === false);
      }
    }
    if (onFinalStep) this.renderReviewReadiness();
    // The step-2 recap names the chosen blueprint; import mode has no blueprint.
    const recap = document.getElementById('wizardStep2Recap');
    if (recap) recap.hidden = importMode;
    if (!importMode && step === 3) {
      this.refreshWorkspaceReview();
      // Assistant programs own one declaration-defined shared roster; attaching
      // an unrelated saved agent here would violate that stable identity.
      if (
        !this.teamView()?.isAssistantProgram &&
        !this.existingAgentRosterLoaded &&
        !this.existingAgentRosterLoading
      ) {
        void this.loadExistingAgentRoster();
      }
    }
    if (onFinalStep) this.refreshWorkspaceReview();
  },

  // Whether the selected blueprint's dependencies are unresolved. Blank, an
  // ad-hoc template folder, and import mode all report false: they depend on
  // nothing the catalog can fail to provide.
  blueprintSelectionBlocked() {
    if (this.importModeEnabled) return false;
    return Boolean(window.ProjectTemplateCard?.isSelectionBlocked?.());
  },

  // Whether the team draft currently carries an issue that prevents building a
  // valid create request. Advisory issues (an intentionally agent-less team, a
  // picker that failed to load) deliberately do not count.
  hasBlockingTeamIssue() {
    const view = this.teamView();
    return Boolean(view && view.blockingIssues.length > 0);
  },

  // Names the workspace being created rather than the blueprint it came from, so
  // the button says what will exist afterwards. Falls back to a generic label
  // until the name is set.
  workspaceCreateCtaLabel() {
    if (window.SetupWorkspaceCreator?.hasPending()) return 'Retry Confirmed Change';
    const name = String(document.getElementById('folderNameInput')?.value || '').trim();
    return name ? `Create “${name}”` : 'Create Workspace';
  },

  // Keeps the create button's label in step with the workspace name without
  // re-rendering the whole wizard on every keystroke.
  refreshWorkspaceCreateCta() {
    const createBtn = document.getElementById('createFolderBtn');
    if (!createBtn || this.isCreatingFolder || this.importModeEnabled) return;
    createBtn.textContent = this.workspaceCreateCtaLabel();
  },

  // Shows the folder the current name will map to on disk, so the name→folder
  // relationship (and slug conflicts) aren't a surprise at create time. Skipped
  // in import mode, where the folder name comes from the imported path.
  updateWorkspaceNameHint() {
    const hint = document.getElementById('workspaceNameHint');
    if (!hint) return;
    if (hint.classList.contains('is-error')) return; // an active error owns the slot
    const name = document.getElementById('folderNameInput')?.value.trim() || '';
    if (this.importModeEnabled || !name) {
      hint.textContent = '';
      hint.hidden = true;
      return;
    }
    hint.textContent = `Folder: ${this.slugifyWorkspaceName(name)}`;
    hint.hidden = false;
  },

  // Returns an actionable message when the workspace identity cannot produce a
  // valid create request yet, or '' when it can.
  //
  // The duplicate-slug check is a best-effort courtesy: it compares against the
  // slugs of workspaces this client already knows about, so the user hears about
  // an obvious collision on Details instead of at the very end. It cannot be
  // authoritative — a folder can exist on disk with no workspace record — so the
  // server's create-time 409 remains the real gate and still offers its
  // suggested slug.
  workspaceIdentityProblem() {
    const name = String(document.getElementById('folderNameInput')?.value || '').trim();
    if (!name) return 'Workspace name is required';

    const slug = this.slugifyWorkspaceName(name);
    const taken = (this.folders || []).some(
      folder =>
        String(folder?.folder_slug || '')
          .trim()
          .toLowerCase() === slug
    );
    if (taken) {
      return `Another workspace already uses the folder “${slug}”. Choose a different name.`;
    }
    return '';
  },

  setWorkspaceNameError(message) {
    const hint = document.getElementById('workspaceNameHint');
    const input = document.getElementById('folderNameInput');
    input?.classList.add('is-invalid');
    if (hint) {
      hint.textContent = message;
      hint.hidden = false;
      hint.classList.add('is-error');
    }
  },

  clearWorkspaceNameError() {
    const hint = document.getElementById('workspaceNameHint');
    document.getElementById('folderNameInput')?.classList.remove('is-invalid');
    hint?.classList.remove('is-error');
  },

  // Sets the step-2 recap line from the selected
  // blueprint, falling back to a Blank-workspace label.
  updateWizardRecap(template) {
    const nameEl = document.getElementById('wizardStep2RecapName');
    const iconEl = document.getElementById('wizardStep2RecapIcon');
    const isBlank = !template || template.blank || !template.id;
    if (nameEl) nameEl.textContent = isBlank ? 'Blank workspace' : template.name || template.id;
    if (iconEl) iconEl.textContent = template && template.icon ? template.icon : '✍';
  },

  // Moves the wizard to a step and re-renders chrome. Scrolls the modal body to
  // the top and moves focus to the new step heading — or, when a step's own
  // validation refuses the move, to the control that has to be fixed first.
  goToWizardStep(step) {
    const targetStep = Math.max(
      window.SetupWorkspaceCreator?.isActive() ? 2 : 1,
      Math.min(this.wizardStepCount, Number(step) || 1)
    );
    // Leaving Blueprint requires a blueprint that can actually produce a
    // workspace. A blueprint whose plugin is missing, disabled, or unusable
    // here would otherwise be carried silently through Details and Team, and
    // the user would meet the problem for the first time at Create — after
    // naming the workspace and building its roster.
    //
    // The blueprint stays selected either way: this refuses to move on, it does
    // not undo the choice.
    if (!this.importModeEnabled && targetStep > 1 && this.wizardStep === 1) {
      if (this.blueprintSelectionBlocked()) {
        this.refreshWizardChrome();
        window.ProjectTemplateCard?.announceBlocked?.();
        window.ProjectTemplateCard?.focusReadiness?.();
        return;
      }
    }
    // Leaving Details requires a workspace name and a folder slug that isn't
    // already taken: without them neither the Team roster nor the Review receipt
    // can describe a workspace that could exist.
    if (!this.importModeEnabled && targetStep >= 3) {
      const problem = this.workspaceIdentityProblem();
      if (problem) {
        this.wizardStep = 2;
        this.refreshWizardChrome();
        this.setWorkspaceNameError(problem);
        document.getElementById('folderNameInput')?.focus();
        return;
      }
    }
    // Team refuses to hand off to Review while the resulting roster cannot be
    // resolved: Review would otherwise present a receipt for a team nobody can
    // see, and Create would build a request the server will reject.
    if (!this.importModeEnabled && targetStep > 3 && this.wizardStep === 3) {
      if (this.hasBlockingTeamIssue()) {
        this.refreshWizardChrome();
        const blockers = this.teamView()?.blockingIssues || [];
        const assistantNameIssue = blockers.find(
          issue => issue.id === 'assistant-name' || issue.anchor === 'assistantProgramCreateName'
        );
        const setupIssue = blockers.find(issue => issue.id === 'template-agent-setup-required');
        const changedIssue = blockers.find(issue => issue.id === 'template-agent-plan-changed');
        const creationIssue = blockers.find(issue => issue.id === 'template-agent-creation-failed');
        const activeIssue =
          assistantNameIssue || changedIssue || creationIssue || setupIssue || blockers[0];
        let target = null;
        if (assistantNameIssue) target = document.getElementById('assistantProgramCreateName');
        else if (changedIssue && Number.isInteger(changedIssue.templateAgentIndex)) {
          target = document.getElementById(`team-agent-setup-${changedIssue.templateAgentIndex}`);
        } else if (creationIssue) {
          target = document.getElementById(`team-agent-retry-${creationIssue.templateAgentIndex}`);
        } else if (setupIssue) {
          target = document.getElementById(`team-agent-setup-${setupIssue.templateAgentIndex}`);
        } else {
          target = document.querySelector('#workspaceTeamIssues .workspace-team-issue.is-blocking');
        }
        this.signalWorkspaceTeamBlock(target, activeIssue?.message);
        return;
      }
    }
    // A create failure belongs to the attempt that produced it. Leaving Review to
    // go and fix something ends that attempt, so the message does not linger and
    // become stale advice about a problem the user has already addressed.
    if (this.wizardStep === this.wizardStepCount && targetStep !== this.wizardStepCount) {
      this.clearWorkspaceCreateError();
    }
    this.wizardStep = targetStep;
    this.refreshWizardChrome();
    const body = document.querySelector('#addFolderModal .modal-body');
    if (body) body.scrollTop = 0;
    const heading = document.getElementById(`wizardStep${this.wizardStep}Title`);
    if (heading) heading.focus();
    else if (this.wizardStep === 2) document.getElementById('folderNameInput')?.focus();
  },

  // Applies a Template's default Agent behavior to the select, unless the user
  // has manually overridden it. Always refreshes the hint. Accepts the API
  // shape (behavior_profile); tolerates the legacy camelCase too.
  applyTemplateBehavior(template) {
    const select = document.getElementById('folderPresetSelect');
    const profile = template && (template.behavior_profile || template.behaviorProfile);
    if (select && !this.behaviorOverridden && profile) {
      select.value = profile;
    }
    this.updateBehaviorHint();
  },

  async createWorkspaceSeedTask(workspaceId, taskConfig) {
    const payload = {
      workspace_id: workspaceId,
      description: taskConfig.description,
      details: taskConfig.details || '',
      priority: Number(taskConfig.priority) || 1
    };

    if (taskConfig.to) {
      payload.to = taskConfig.to;
    }

    if (taskConfig.schedule) {
      payload.schedule = taskConfig.schedule;
      payload.schedule_enabled = Boolean(taskConfig.schedule_enabled);
      payload.schedule_name = taskConfig.schedule_name || '';
      // No hard-coded assignee fallback: the backend defaults an unassigned task
      // to the workspace coordinator (entry agent) before schedule validation.
    }

    const response = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to create setup task');
    }
  },

  async createWorkspaceSeedNote(workspaceId, noteConfig) {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: noteConfig.name || 'Starter Note',
        content: noteConfig.content || ''
      })
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to create setup note');
    }
  },

  projectOpenNoticeStorageKey(workspaceId) {
    return `oriProjectOpenNotice:${String(workspaceId || '').trim()}`;
  },

  rememberProjectOpenFailure(workspaceId, error) {
    const id = String(workspaceId || '').trim();
    if (!id) return;
    const detail = error && error.message ? String(error.message).trim() : '';
    const message = detail
      ? `Workspace created, but the project could not be opened. Use Open Project to try again. ${detail}`
      : 'Workspace created, but the project could not be opened. Use Open Project to try again.';
    try {
      sessionStorage.setItem(
        this.projectOpenNoticeStorageKey(id),
        JSON.stringify({ workspace_id: id, message })
      );
    } catch (storageError) {
      console.warn('Failed to preserve project-open notice:', storageError);
    }
  },

  async openProjectAfterWorkspaceCreate(workspaceId) {
    const id = String(workspaceId || '').trim();
    if (!id) return;
    const response = await fetch(`/api/workspaces/${encodeURIComponent(id)}/project/open`, {
      method: 'POST'
    });
    const result = await response.json().catch(() => ({}));
    if (!response.ok || result.error) {
      throw new Error(
        result.error ||
          result.message ||
          'The system default application did not accept the project file.'
      );
    }
  },

  async hireAssistantProgramAfterCreate(workspaceId, hire) {
    const id = String(workspaceId || '').trim();
    if (!id || !hire) return null;
    const base = `/api/workspaces/${encodeURIComponent(id)}/assistant-program`;
    const stateResponse = await fetch(base, { headers: { Accept: 'application/json' } });
    let state = await stateResponse.json().catch(() => ({}));
    if (!stateResponse.ok || state.error) {
      throw new Error(state.error || 'The shared assistant station could not be prepared.');
    }
    if (!state.available && state.activation_needed) {
      const activateResponse = await fetch(`${base}/activate`, {
        method: 'POST',
        headers: { Accept: 'application/json' }
      });
      state = await activateResponse.json().catch(() => ({}));
      if (!activateResponse.ok || state.error) {
        throw new Error(state.error || 'The shared assistant station could not be activated.');
      }
    }
    if (!state.available) {
      throw new Error('The shared assistant station could not be prepared.');
    }
    // For scoped programs this flag describes the Home, not the new project's
    // independently reviewed roles. The owner idempotently reuses complete scopes.
    if (state.hired && Number(state.declaration?.schema_version || 1) < 2) return state;

    const hireResponse = await fetch(`${base}/hire`, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: String(hire.name || '').trim(),
        provider: String(hire.provider || '').trim(),
        model: String(hire.model || '').trim(),
        version: Number(state.state_revision || 0)
      })
    });
    const result = await hireResponse.json().catch(() => ({}));
    if (!hireResponse.ok || result.error) {
      throw new Error(result.error || 'The shared assistant roster could not be hired.');
    }
    return result;
  },

  // Create folder
  async createFolder() {
    if (this.isCreatingFolder) return;

    const nameInput = document.getElementById('folderNameInput');
    const descriptionInput = document.getElementById('folderDescriptionInput');
    const parentSelect = document.getElementById('folderParentSelect');
    const modalElement = document.getElementById('addFolderModal');
    const colorBtn = document.querySelector('#addFolderModal .folder-color-btn.active');
    const createBtn = document.getElementById('createFolderBtn');
    const importToggle = document.getElementById('folderImportToggle');
    const importPathInput = document.getElementById('folderImportPathInput');
    const workspaceBootstrap = this.getWorkspaceBootstrapFromModal();

    const importEnabled = this.importModeEnabled || Boolean(importToggle?.checked);
    // Snapshot the confirmed Team receipt before the final readiness refresh.
    // Re-populating the blueprint catalog can emit a same-selection loading
    // event while its fresh plan request is in flight; that must not erase the
    // roster the user just reviewed.
    const confirmedTeamView = importEnabled ? null : this.teamView();
    const confirmedTeamPayload = confirmedTeamView?.payload || {};
    const assistantRosterAlreadyHired = Boolean(confirmedTeamView?.assistantProgram?.existingHired);
    const openProjectAfterCreate = Boolean(
      !importEnabled && window.ProjectTemplateCard?.shouldOpenAfterCreate?.()
    );
    const importPath = importPathInput?.value?.trim() || '';
    const name = nameInput?.value.trim() || '';
    const description = descriptionInput?.value.trim() || '';
    if (!name && !importEnabled) {
      // Inline error on the field (which is now the first thing on step 1) rather
      // than a toast that fires only after the user reaches the end.
      this.setWorkspaceNameError('Workspace name is required');
      nameInput?.focus();
      return;
    }
    if (!importEnabled && (!confirmedTeamView || !confirmedTeamView.canContinueFromTeam)) {
      this.goToWizardStep(3);
      this.refreshWorkspaceReview();
      const blocking = this.teamView()?.blockingIssues[0];
      const owningControl = blocking?.anchor ? document.getElementById(blocking.anchor) : null;
      if (owningControl) owningControl.focus();
      else
        document.querySelector('#workspaceTeamIssues .workspace-team-issue.is-blocking')?.focus();
      return;
    }
    if (!importEnabled) {
      // Re-read the blueprint's dependency state immediately before creating.
      // The catalog on screen can be minutes old, and a plugin disabled in
      // another tab would otherwise turn a considered Create into a server
      // rejection. Rendering the current state here is cheaper than that, and
      // the server still has the final say either way.
      const current = await window.ProjectTemplateCard?.recheckSelection?.();
      if (current && current.state !== 'ready') {
        this.renderReviewReadiness();
        this.showWorkspaceCreateError(current.summary || 'This blueprint is not ready.', current);
        return;
      }
    }
    if (importEnabled && !importPath) {
      this.showToast('Please enter or browse for a folder path to import', 'warning');
      return;
    }
    // Description is optional: a workspace can start with just a name, and Ori
    // can still review the setup later. It only enriches the setup review.

    const parentId = parentSelect?.value?.trim() || '';
    const color = colorBtn?.dataset.color || '';
    const originalCreateLabel = createBtn ? createBtn.textContent : '';

    // A fresh attempt starts without the previous attempt's failure on screen.
    this.clearWorkspaceCreateError();
    this.isCreatingFolder = true;
    window.SetupWorkspaceCreator?.setSubmitting(true);
    if (createBtn) {
      createBtn.disabled = true;
      createBtn.textContent = importEnabled ? 'Importing...' : 'Creating...';
    }

    try {
      const payload = {
        name,
        description,
        parent_id: parentId,
        color
      };
      if (workspaceBootstrap.hasAny) {
        payload.workspace_bootstrap = {
          goal: workspaceBootstrap.description || workspaceBootstrap.goal,
          systems: workspaceBootstrap.systems,
          context: workspaceBootstrap.context
        };
      }
      // Agent behavior profile (mapped from the Starting point or manually
      // overridden). Sent for both create and import; the backend maps it via
      // workspacesettings.ProfileDefaults.
      payload.workspace_preset =
        document.getElementById('folderPresetSelect')?.value?.trim() || 'general';
      const buildSlugConflictMessage = conflict => {
        const requestedSlug =
          typeof conflict?.requested_slug === 'string' ? conflict.requested_slug.trim() : '';
        const suggestedSlug =
          typeof conflict?.suggested_slug === 'string' ? conflict.suggested_slug.trim() : '';
        const location =
          typeof conflict?.location === 'string'
            ? conflict.location.trim().replace(/[\\/]+$/, '')
            : '';
        const suggestedPath = location && suggestedSlug ? `${location}/${suggestedSlug}` : '';
        const parts = [
          `A workspace folder named "${requestedSlug || 'this workspace'}" already exists on disk.`
        ];
        if (suggestedSlug) {
          parts.push(`Create this workspace with the folder name "${suggestedSlug}" instead?`);
        }
        if (suggestedPath) {
          parts.push(`Folder: ${suggestedPath}`);
        }
        return parts.join('\n\n');
      };
      let endpoint = '/api/workspaces';
      let assistantHireConfig = null;
      if (importEnabled) {
        endpoint = '/api/workspaces/import';
        payload.path = importPath;
        payload.allow_duplicate = Boolean(this.importAllowDuplicate);
        payload.entry_point = this.importEntryPoint || 'workspace_hub_create';
      } else {
        if (window.ProjectTemplateCard) {
          // Optional project scaffolding from the template picker
          // (template_id/template_path). The scaffolded project folder name
          // defaults to the workspace name server-side.
          const templateFields = window.ProjectTemplateCard.getPayloadFields();
          Object.assign(payload, templateFields);
          // Blank blueprint (no template_id/path, no ad-hoc folder override):
          // tell the backend to seed the synthetic single-agent roster.
          if (
            window.ProjectTemplateCard.getSelectedTemplate?.()?.blank &&
            !templateFields.template_id &&
            !templateFields.template_path
          ) {
            payload.blank = true;
          }
        }
        // The whole team composition is converted from the draft here, at submit
        // time, and nowhere else. Because it comes from the same derived view the
        // Team and Review steps rendered, the request cannot describe a different
        // team than the one the user just confirmed: a selection the blueprint
        // also contributes is sent once, the order matches what Team showed,
        // staged customizations ride the existing override field, and
        // entry_agent_name is set only when the resolved primary is genuinely one
        // of the saved selections — the server's requirement for that field.
        const teamPayload = confirmedTeamPayload;
        if (teamPayload.assistant_hire) {
          assistantHireConfig = { ...teamPayload.assistant_hire };
        }
        if (typeof teamPayload.create_template_agents === 'boolean') {
          payload.create_template_agents = teamPayload.create_template_agents;
        }
        if (Array.isArray(teamPayload.template_agent_overrides)) {
          payload.template_agent_overrides = teamPayload.template_agent_overrides;
        }
        if (teamPayload.template_agent_review) {
          payload.template_agent_review = {
            version: teamPayload.template_agent_review.version,
            plan_revision: teamPayload.template_agent_review.plan_revision,
            expectations: teamPayload.template_agent_review.expectations.map(expectation => ({
              ...expectation
            }))
          };
        }
        if (Array.isArray(teamPayload.existing_agent_names)) {
          payload.existing_agent_names = [...teamPayload.existing_agent_names];
        }
        if (teamPayload.entry_agent_name) {
          payload.entry_agent_name = teamPayload.entry_agent_name;
        }
        if (window.WorkspaceTagsCard) {
          Object.assign(payload, window.WorkspaceTagsCard.getPayloadFields());
        }
      }

      const requestPayload = { ...payload };
      let response;
      let result = {};

      while (true) {
        response = window.SetupWorkspaceCreator?.isActive()
          ? await window.SetupWorkspaceCreator.submit(requestPayload)
          : await fetch(endpoint, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(requestPayload)
            });

        try {
          result = await response.json();
        } catch (parseErr) {
          result = {};
        }

        if (response.status === 409 && importEnabled && result.duplicate) {
          this.showImportDuplicateWarning(result.duplicate);
          this.showToast(
            'This folder is already imported. Open the existing workspace or click "Import Anyway".',
            'warning'
          );
          return;
        }

        if (response.status === 409 && !importEnabled && result.conflict?.type === 'folder_slug') {
          const suggestedSlug =
            typeof result.conflict.suggested_slug === 'string'
              ? result.conflict.suggested_slug.trim()
              : '';
          if (!suggestedSlug) {
            throw new Error(result.error || 'Failed to create workspace');
          }

          const confirmed = window.confirm(buildSlugConflictMessage(result.conflict));
          if (!confirmed) {
            return;
          }

          requestPayload.folder_slug = suggestedSlug;
          continue;
        }

        if (
          response.status === 409 &&
          !importEnabled &&
          result.conflict?.type === 'template_agent_plan' &&
          result.template_agent_plan
        ) {
          const api = window.CreateWorkspaceTeamDraft;
          const draft = this.ensureWorkspaceTeamDraft();
          const blueprintKey = draft?.plan?.blueprintKey || '';
          this.templateAgentPlan = result.template_agent_plan;
          api?.setPlanReady?.(draft, blueprintKey, result.template_agent_plan);
          api?.markPlanConflict?.(
            draft,
            'Blueprint changed—review the updated agent plan before creating this workspace. Nothing was created.',
            result.conflict
          );
          this.goToWizardStep(3);
          this.refreshWorkspaceReview();
          this.announceWorkspaceTeamChange(
            'Blueprint changed. Review the updated agent plan before creating this workspace.'
          );
          document.querySelector('[data-issue-id="template-agent-plan-changed"]')?.focus();
          return;
        }

        if (
          response.status === 400 &&
          !importEnabled &&
          result.conflict?.type === 'template_agent_override'
        ) {
          const index = Number(result.conflict?.index);
          const serverField = String(result.conflict?.field || '').trim();
          const field = serverField === 'system_prompt' ? 'systemPrompt' : serverField;
          this.goToWizardStep(3);
          this.refreshWorkspaceReview();
          const opener = document.getElementById(`team-agent-setup-${index}`);
          if (Number.isInteger(index)) this.openWorkspaceAgentSetup(index, opener);
          const message = String(result.error || 'Review this agent setup and try again.').trim();
          if (this.workspaceAgentSetupForm && field) {
            this.workspaceAgentSetupFocusField = field;
            this.workspaceAgentSetupForm.setError?.(field, message);
            this.workspaceAgentSetupForm.focus?.(field);
          } else {
            const error = document.getElementById('agentCreateDraftError');
            if (error) {
              error.textContent = message;
              error.hidden = false;
              error.focus?.();
            }
          }
          this.announceWorkspaceTeamChange(
            'An agent setup needs attention before this workspace can be created.'
          );
          return;
        }

        if (
          response.status === 400 &&
          !importEnabled &&
          result.conflict?.type === 'template_agent_review_invalid'
        ) {
          this.showWorkspaceCreateError(
            `The reviewed agent setup is invalid. ${String(result.error || '').trim()}`.trim()
          );
          return;
        }

        if (
          response.status >= 500 &&
          !importEnabled &&
          result.conflict?.type === 'template_agent_create'
        ) {
          const cleanup = Array.isArray(result.cleanup_errors)
            ? ` Cleanup also needs attention: ${result.cleanup_errors.join('; ')}.`
            : '';
          const failureIndex = Number(result.conflict?.index);
          const failureName = String(result.conflict?.name || '').trim();
          const failureMessage = `${String(result.error || 'An agent could not be created.').trim()}${cleanup}`;
          const api = window.CreateWorkspaceTeamDraft;
          const draft = this.ensureWorkspaceTeamDraft();
          api?.markCreationFailure?.(draft, failureIndex, failureName, failureMessage);
          this.goToWizardStep(3);
          this.refreshWorkspaceReview();
          this.announceWorkspaceTeamChange(
            `${failureName || 'Agent'} creation failed. Nothing was created; setup was preserved for retry.`
          );
          document.getElementById(`team-agent-retry-${failureIndex}`)?.focus();
          return;
        }

        if (response.status === 409 && !importEnabled && result.readiness) {
          // Blueprint readiness gate: the server re-derived the blueprint's
          // dependency state at create time and refused. It is rendered inline
          // with its own recovery controls rather than as a toast — a toast
          // disappears, cannot be focused, and offers nothing to act on, which
          // is exactly wrong for a state the user can fix without leaving the
          // wizard.
          const message = String(result.error || '').trim() || 'This blueprint is not ready.';
          this.showWorkspaceCreateError(message, result.readiness);
          // Re-read the catalog so the card, the briefing, and Review all agree
          // with the state the server just reported.
          void window.ProjectTemplateCard?.populate?.({ preserveSelection: true });
          return;
        }

        break;
      }

      if (!response.ok || result.error) {
        const fallbackMessage = importEnabled
          ? 'Failed to import folder as workspace'
          : 'Failed to create workspace';
        throw new Error(result.error || fallbackMessage);
      }

      const teamSuccessParts = importEnabled
        ? []
        : this.workspaceTeamSuccessParts(confirmedTeamPayload);
      const hasReviewedTemplateAgentOutcome = Boolean(
        confirmedTeamPayload?.template_agent_review?.expectations?.length
      );

      // The workspace exists even when project-template instantiation failed;
      // surface the non-fatal warning.
      if (typeof result.project_warning === 'string' && result.project_warning) {
        this.showToast(result.project_warning, 'warning');
      }
      // Seeded template agents that could not be created surface here (non-fatal).
      if (Array.isArray(result.agent_warnings)) {
        result.agent_warnings
          .filter(msg => typeof msg === 'string' && msg)
          .forEach(msg => this.showToast(msg, 'warning'));
      }
      // A roster entry matched an existing agent by name; the existing
      // definition was reused instead of the template's (PRD FR7).
      if (Array.isArray(result.agent_reuse_notices) && !hasReviewedTemplateAgentOutcome) {
        result.agent_reuse_notices
          .filter(msg => typeof msg === 'string' && msg)
          .forEach(msg => this.showToast(msg, 'info'));
      }
      if (window.ProjectTemplateCard) window.ProjectTemplateCard.reset();
      if (window.WorkspaceTagsCard) window.WorkspaceTagsCard.reset();
      window.OriTagInput?.clearTagPoolCache?.();

      const createdWorkspaceId =
        result && result.folder && result.folder.id ? String(result.folder.id) : '';
      const createdWorkspaceSlug =
        result && result.folder && result.folder.folder_slug
          ? String(result.folder.folder_slug)
          : '';
      const askOriSeedNoteRaw = modalElement
        ? String(modalElement.dataset.askOriSeedNote || '')
        : '';
      const askOriSeedTaskRaw = modalElement
        ? String(modalElement.dataset.askOriSeedTask || '')
        : '';
      if (modalElement) {
        delete modalElement.dataset.askOriPostCreate;
        delete modalElement.dataset.askOriSeedNote;
        delete modalElement.dataset.askOriSeedTask;
      }

      let askOriSeedNote = null;
      let askOriSeedTask = null;
      if (askOriSeedNoteRaw) {
        try {
          askOriSeedNote = JSON.parse(askOriSeedNoteRaw);
        } catch (error) {
          console.warn('Failed to parse Assistant seed note:', error);
        }
      }
      if (askOriSeedTaskRaw) {
        try {
          askOriSeedTask = JSON.parse(askOriSeedTaskRaw);
        } catch (error) {
          console.warn('Failed to parse Assistant seed task:', error);
        }
      }
      // Add-ons (MCPs/skills/plugins) are no longer applied at create time —
      // the user adds them later via the workspace-page "Find tools" panel.
      const bootstrapApplyResult = {
        invitedAgents: 0,
        boundMCPs: 0,
        attachedSkills: 0,
        addedPlugins: 0,
        failures: []
      };

      const askOriSeedResult = {
        notesCreated: 0,
        tasksCreated: 0,
        errors: []
      };

      if (createdWorkspaceId && askOriSeedNote) {
        try {
          await this.createWorkspaceSeedNote(createdWorkspaceId, askOriSeedNote);
          askOriSeedResult.notesCreated += 1;
        } catch (error) {
          askOriSeedResult.errors.push(error);
        }
      }
      if (createdWorkspaceId && askOriSeedTask) {
        try {
          await this.createWorkspaceSeedTask(createdWorkspaceId, askOriSeedTask);
          askOriSeedResult.tasksCreated += 1;
        } catch (error) {
          askOriSeedResult.errors.push(error);
        }
      }

      let assistantHireResult = null;
      let assistantHireError = null;
      if (createdWorkspaceId && assistantHireConfig) {
        try {
          assistantHireResult = await this.hireAssistantProgramAfterCreate(
            createdWorkspaceId,
            assistantHireConfig
          );
        } catch (error) {
          assistantHireError = error;
        }
      }

      // Starter tasks are seeded server-side during workspace creation
      // (assigned to the entry agent); the create response reports how many.
      const seededStarterTasks = Number(result?.seeded_starter_tasks) || 0;
      let postCreateResult = { applied: false, destination: '' };
      let postCreateError = null;
      if (createdWorkspaceId && this.workspacePostCreateAction) {
        try {
          postCreateResult = await this.applyWorkspacePostCreateAction(
            createdWorkspaceId,
            createdWorkspaceSlug
          );
        } catch (error) {
          postCreateError = error;
        }
      }

      if (assistantHireError && createdWorkspaceSlug) {
        // Producer/Team Home is optional on success. It becomes the destination
        // only when the create-time hire did not finish and recovery is needed.
        postCreateResult.destination = `/workspaces/${encodeURIComponent(createdWorkspaceSlug)}/assistant`;
      }

      if (postCreateResult.applied) {
        this.showToast('Personal HQ imported successfully', 'success');
      } else if (
        bootstrapApplyResult.invitedAgents > 0 ||
        bootstrapApplyResult.boundMCPs > 0 ||
        bootstrapApplyResult.attachedSkills > 0 ||
        bootstrapApplyResult.addedPlugins > 0 ||
        askOriSeedResult.tasksCreated > 0 ||
        askOriSeedResult.notesCreated > 0 ||
        seededStarterTasks > 0 ||
        Array.isArray(assistantHireResult?.roster) ||
        teamSuccessParts.length > 0
      ) {
        const summaryParts = [...teamSuccessParts];
        if (bootstrapApplyResult.invitedAgents > 0)
          summaryParts.push(
            `${bootstrapApplyResult.invitedAgents} agent${bootstrapApplyResult.invitedAgents === 1 ? '' : 's'} invited`
          );
        if (bootstrapApplyResult.boundMCPs > 0)
          summaryParts.push(
            `${bootstrapApplyResult.boundMCPs} MCP${bootstrapApplyResult.boundMCPs === 1 ? '' : 's'} bound`
          );
        if (bootstrapApplyResult.attachedSkills > 0)
          summaryParts.push(
            `${bootstrapApplyResult.attachedSkills} skill${bootstrapApplyResult.attachedSkills === 1 ? '' : 's'} attached`
          );
        if (bootstrapApplyResult.addedPlugins > 0)
          summaryParts.push(
            `${bootstrapApplyResult.addedPlugins} plugin${bootstrapApplyResult.addedPlugins === 1 ? '' : 's'} added`
          );
        if (askOriSeedResult.tasksCreated > 0)
          summaryParts.push(`${askOriSeedResult.tasksCreated} Assistant task`);
        if (askOriSeedResult.notesCreated > 0)
          summaryParts.push(`${askOriSeedResult.notesCreated} Assistant note`);
        if (seededStarterTasks > 0)
          summaryParts.push(
            `${seededStarterTasks} starter task${seededStarterTasks === 1 ? '' : 's'} added`
          );
        const hiredRoles = Array.isArray(assistantHireResult?.roster)
          ? assistantHireResult.roster.length
          : 0;
        if (hiredRoles > 0) {
          summaryParts.push(
            `${hiredRoles} shared assistant role${hiredRoles === 1 ? '' : 's'} ${assistantRosterAlreadyHired ? 'linked' : 'hired'}`
          );
        }
        const summaryText = summaryParts.join(', ');
        if (askOriSeedResult.errors.length > 0 || bootstrapApplyResult.failures.length > 0) {
          const firstFailure = bootstrapApplyResult.failures[0];
          this.showToast(
            `${importEnabled ? 'Workspace imported' : 'Workspace created'} with partial setup (${summaryText}).${firstFailure ? ` ${firstFailure}` : ''}`,
            'warning'
          );
        } else {
          this.showToast(
            `${importEnabled ? 'Workspace imported' : 'Workspace created'} with setup (${summaryText}).`,
            'success'
          );
        }
      } else {
        this.showToast(
          importEnabled ? 'Workspace imported successfully' : 'Workspace created successfully',
          'success'
        );
      }

      if (postCreateError) {
        this.showToast(
          `Workspace imported, but Personal HQ setup did not finish. ${postCreateError.message || ''}`.trim(),
          'warning'
        );
      }
      if (assistantHireError) {
        this.showToast(
          `Workspace created, but its assistant team still needs attention. ${assistantHireError.message || ''}`.trim(),
          'warning'
        );
      }

      if (createdWorkspaceId && openProjectAfterCreate) {
        try {
          await this.openProjectAfterWorkspaceCreate(createdWorkspaceId);
        } catch (error) {
          console.warn('Workspace created but project open failed:', error);
          this.rememberProjectOpenFailure(createdWorkspaceId, error);
        }
      }

      // Close only after all of the confirmed workspace/team changes finish.
      window.SetupWorkspaceCreator?.setSubmitting(false);
      const modal = bootstrap.Modal.getInstance(modalElement);
      modal?.hide();
      this.resetAddWorkspaceModalForm();

      // A Map-origin create returns to the map (#292 FR-53). The user asked for
      // a workspace at a point they chose; navigating into it would take them
      // away from the arrangement they were building. The coordinate is saved
      // by the map, which owns it — and saving it is deliberately not allowed to
      // fail the create: a workspace that exists stays created even if its
      // position does not save (FR-56).
      const mapOrigin = this.workspaceMapOrigin;
      this.workspaceMapOrigin = false;
      if (createdWorkspaceId && mapOrigin) {
        if (window.OriWorkspaceMap && typeof window.OriWorkspaceMap.completeBuild === 'function') {
          try {
            await window.OriWorkspaceMap.completeBuild(createdWorkspaceId);
          } catch (error) {
            console.warn('Workspace created but its map position did not save:', error);
          }
        }
        await this.loadFolders();
        if (window.WorkspaceHub && typeof window.WorkspaceHub.loadWorkspaces === 'function') {
          await window.WorkspaceHub.loadWorkspaces();
        }
        if (window.OriHomeCockpit && typeof window.OriHomeCockpit.refreshQuietly === 'function') {
          await window.OriHomeCockpit.refreshQuietly();
          if (typeof window.OriHomeCockpit.select === 'function') {
            window.OriHomeCockpit.select(createdWorkspaceId);
          }
        }
        return;
      }

      // Navigate to the new workspace once the reviewed setup has been applied
      if (createdWorkspaceId && (postCreateResult.destination || createdWorkspaceSlug)) {
        window.location.href =
          postCreateResult.destination || `/workspaces/${encodeURIComponent(createdWorkspaceSlug)}`;
        return;
      }

      // Refresh folders. When the post-create contract did NOT navigate away
      // (the branch above), both Map and Tree must still pick the new workspace
      // up, and it becomes the selected item so its context is in the rail
      // (PRD FR108). The cockpit owns that refresh; the launcher keeps its own.
      await this.loadFolders();
      if (window.WorkspaceHub && typeof window.WorkspaceHub.loadWorkspaces === 'function') {
        await window.WorkspaceHub.loadWorkspaces();
      }
      if (window.OriHomeCockpit && typeof window.OriHomeCockpit.refreshQuietly === 'function') {
        await window.OriHomeCockpit.refreshQuietly();
        if (createdWorkspaceId && typeof window.OriHomeCockpit.select === 'function') {
          window.OriHomeCockpit.select(createdWorkspaceId);
        }
      }
    } catch (error) {
      console.error('Failed to create folder:', error);
      const message = error && error.message ? error.message : 'Failed to create workspace';
      this.showToast(message, 'error');
      // The modal stays open and the draft is untouched, so the user can fix the
      // problem and resubmit rather than rebuilding the team from scratch.
      if (!importEnabled) this.showWorkspaceCreateError(message);
    } finally {
      this.isCreatingFolder = false;
      window.SetupWorkspaceCreator?.setSubmitting(false);
      if (createBtn) {
        createBtn.disabled = window.SetupWorkspaceCreator?.canSubmit() === false;
        createBtn.textContent = window.SetupWorkspaceCreator?.hasPending()
          ? 'Retry Confirmed Change'
          : originalCreateLabel || 'Create';
      }
    }
  },

  // Restates the selected blueprint's readiness on Review.
  //
  // Step 1 already refuses to advance past a known blocker, so in the ordinary
  // case this renders nothing. It exists for the state that changed underneath
  // the draft — a plugin disabled in another tab, a plugin store that stopped
  // answering — where the receipt would otherwise look calmer than reality.
  renderReviewReadiness() {
    const host = document.getElementById('workspaceReviewReadiness');
    if (!host) return;
    host.textContent = '';
    if (this.importModeEnabled) return;
    const readiness = window.ProjectTemplateCard?.getSelectedReadiness?.();
    if (!readiness || readiness.state === 'ready') return;
    const template = window.ProjectTemplateCard?.getSelectedTemplate?.();
    const panel = window.BlueprintReadiness?.renderPanel(readiness, {
      blueprintName: template?.name || template?.id || '',
      // Recovery belongs to the step that owns the blueprint. Routing back
      // keeps one place responsible for the choice and its repair, rather than
      // scattering half a recovery flow across the receipt.
      onAction: () => this.goToWizardStep(1)
    });
    if (panel) host.appendChild(panel);
  },

  // Renders the server's own failure message on Review with a route back to the
  // step that can fix it, then moves focus there so it is announced and
  // immediately actionable.
  //
  // A refusal carrying a readiness projection renders that projection with its
  // real recovery controls. Reducing "the plugin this blueprint needs is
  // disabled" to a sentence and two Edit links would hand the user a problem
  // with no way to act on it.
  showWorkspaceCreateError(message, readiness, options = {}) {
    const host = document.getElementById('workspaceReviewError');
    if (!host) return;
    host.innerHTML = '';

    const normalized = readiness ? window.BlueprintReadiness?.normalize(readiness) : null;
    if (normalized && normalized.state !== 'ready') {
      const template = window.ProjectTemplateCard?.getSelectedTemplate?.();
      const panel = window.BlueprintReadiness?.renderPanel(normalized, {
        blueprintName: template?.name || template?.id || '',
        onAction: () => this.goToWizardStep(1)
      });
      if (panel) host.appendChild(panel);
    } else {
      const text = document.createElement('p');
      text.className = 'workspace-review-error-text';
      text.textContent = message;
      host.appendChild(text);
    }

    const actions = document.createElement('div');
    actions.className = 'workspace-review-error-actions';
    if (options.retry) {
      const retry = document.createElement('button');
      retry.type = 'button';
      retry.className = 'btn btn-sm btn-outline-primary';
      retry.textContent = 'Retry creation';
      retry.addEventListener('click', () => this.createFolder());
      actions.appendChild(retry);
    }
    [
      { step: 1, label: 'Edit blueprint' },
      { step: 2, label: 'Edit details' },
      { step: 3, label: 'Edit team' }
    ].forEach(({ step, label }) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'workspace-wizard-inline-action';
      button.textContent = label;
      button.addEventListener('click', () => this.goToWizardStep(step));
      actions.appendChild(button);
    });
    host.appendChild(actions);

    host.hidden = false;
    host.focus();
  },

  clearWorkspaceCreateError() {
    const host = document.getElementById('workspaceReviewError');
    if (!host) return;
    host.hidden = true;
    host.innerHTML = '';
  },

  // Delete folder
  async deleteFolder(folderId) {
    if (!confirm('Are you sure you want to delete this workspace? Sessions will be moved to root.'))
      return;

    try {
      const response = await fetch(`/api/workspaces/${folderId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete workspace');

      // Clear active folder if deleted
      if (this.activeFolder === folderId) {
        this.activeFolder = null;
      }

      // Refresh
      await this.loadFolders();
      await this.loadSessions();
    } catch (error) {
      console.error('Failed to delete folder:', error);
    }
  },

  // Handle search input
  handleSearchInput(query) {
    this.searchQuery = query;

    // Show/hide clear button
    const clearBtn = document.getElementById('clearSessionSearch');
    if (clearBtn) {
      clearBtn.style.display = query ? 'flex' : 'none';
    }

    // Debounce search
    if (this.searchDebounceTimer) {
      clearTimeout(this.searchDebounceTimer);
    }

    this.searchDebounceTimer = setTimeout(() => {
      this.loadSessions();
    }, 300);
  },

  // Clear search
  clearSearch() {
    const input = document.getElementById('sessionSearchInput');
    if (input) input.value = '';
    this.handleSearchInput('');
  },

  // Focus search input
  focusSearch() {
    const input = document.getElementById('sessionSearchInput');
    if (input) {
      // On mobile, show sidebar if hidden
      const sidebar = document.getElementById('sessionSidebar');
      if (sidebar && window.innerWidth < 992 && !sidebar.classList.contains('mobile-open')) {
        sidebar.classList.add('mobile-open');
      }
      input.focus();
    }
  },

  // Set sort order
  setSortOrder(sort) {
    this.sortBy = sort;

    // Update UI
    const label = document.getElementById('sortLabel');
    if (label) {
      const sortLabels = { updated_at: 'Recent', created_at: 'Created', title: 'Title' };
      label.textContent = sortLabels[sort] || 'Recent';
    }

    // Update active state
    document.querySelectorAll('#sortDropdown .session-dropdown-item').forEach(item => {
      item.classList.toggle('active', item.dataset.sort === sort);
    });

    this.loadSessions();
  },

  // Filter by folder
  filterByFolder(folderId) {
    if (this.activeFolder === folderId) {
      this.activeFolder = null; // Toggle off
    } else {
      this.activeFolder = folderId;
    }

    // Update folder UI
    document.querySelectorAll('.folder-item').forEach(item => {
      item.classList.toggle('active', item.dataset.folderId === this.activeFolder);
    });

    this.loadSessions();
  },

  // Toggle tag filter
  toggleTagFilter(tag) {
    const index = this.filterTags.indexOf(tag);
    if (index >= 0) {
      this.filterTags.splice(index, 1);
    } else {
      this.filterTags.push(tag);
    }

    // Update filter label
    const label = document.getElementById('filterLabel');
    if (label) {
      label.textContent = this.filterTags.length > 0 ? `${this.filterTags.length} tags` : 'All';
    }

    this.renderTagFilters();
    this.loadSessions();
  },

  // Toggle folder tree visibility
  toggleFolderTree(forceOpen = null) {
    const header = document.getElementById('folderTreeToggle');
    const tree = document.getElementById('folderTree');

    if (header && tree) {
      if (forceOpen === true) {
        header.classList.add('expanded');
        tree.style.display = 'block';
        return;
      }
      if (forceOpen === false) {
        header.classList.remove('expanded');
        tree.style.display = 'none';
        return;
      }

      header.classList.toggle('expanded');
      tree.style.display = tree.style.display === 'none' ? 'block' : 'none';
    }
  },

  // Toggle dropdown
  toggleDropdown(dropdownId) {
    const dropdown = document.getElementById(dropdownId);
    if (dropdown) {
      const isVisible = dropdown.style.display !== 'none';
      this.closeDropdowns();
      if (!isVisible) {
        dropdown.style.display = 'block';
      }
    }
  },

  // Close all dropdowns
  closeDropdowns() {
    document.querySelectorAll('.session-dropdown-menu').forEach(menu => {
      menu.style.display = 'none';
    });
  },

  // Restore sidebar state from localStorage
  restoreSidebarState() {
    const sidebar = document.getElementById('sessionSidebar');
    if (!sidebar) return;

    // Default to collapsed (true) if no preference saved
    const isCollapsed = localStorage.getItem('sessionSidebarCollapsed') !== 'false';
    if (isCollapsed) {
      sidebar.classList.add('collapsed');
    } else {
      sidebar.classList.remove('collapsed');
    }
  },

  // Toggle sidebar visibility
  toggleSidebar() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar) {
      sidebar.classList.toggle('collapsed');
      // Save preference
      localStorage.setItem('sessionSidebarCollapsed', sidebar.classList.contains('collapsed'));
    }
  },

  // Close sidebar
  closeSidebar() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar) {
      sidebar.classList.add('collapsed');
      localStorage.setItem('sessionSidebarCollapsed', 'true');
    }
  },

  // Open sidebar
  openSidebar() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar) {
      sidebar.classList.remove('collapsed');
      localStorage.setItem('sessionSidebarCollapsed', 'false');
    }
  },

  // Show session context menu
  showSessionContextMenu(e, sessionId) {
    this.contextMenuTarget = sessionId;
    this.contextMenuType = 'session';

    const menu = document.getElementById('sessionContextMenu');
    if (menu) {
      menu.style.display = 'block';
      menu.style.left = `${e.pageX}px`;
      menu.style.top = `${e.pageY}px`;

      // Adjust if menu goes off screen
      const rect = menu.getBoundingClientRect();
      if (rect.right > window.innerWidth) {
        menu.style.left = `${e.pageX - rect.width}px`;
      }
      if (rect.bottom > window.innerHeight) {
        menu.style.top = `${e.pageY - rect.height}px`;
      }
    }
  },

  // Show folder context menu
  showFolderContextMenu(e, folderId) {
    this.contextMenuTarget = folderId;
    this.contextMenuType = 'folder';

    const menu = document.getElementById('folderContextMenu');
    if (menu) {
      menu.style.display = 'block';
      menu.style.left = `${e.pageX}px`;
      menu.style.top = `${e.pageY}px`;
    }
  },

  // Hide all context menus
  hideContextMenus() {
    const sessionMenu = document.getElementById('sessionContextMenu');
    const folderMenu = document.getElementById('folderContextMenu');
    const noteMenu = document.getElementById('noteContextMenu');
    if (sessionMenu) sessionMenu.style.display = 'none';
    if (folderMenu) folderMenu.style.display = 'none';
    if (noteMenu) noteMenu.style.display = 'none';
    this.contextMenuTarget = null;
    this.contextMenuType = null;
  },

  // Handle session context menu action
  handleSessionContextAction(action) {
    const sessionId = this.contextMenuTarget;
    if (!sessionId) return;

    this.hideContextMenus();

    switch (action) {
      case 'info':
        this.showSessionInfoModal(sessionId);
        break;
      case 'rename':
        this.startInlineRename(sessionId);
        break;
      case 'move':
        this.showMoveToFolderModal(sessionId);
        break;
      case 'tags':
        this.showEditTagsModal(sessionId);
        break;
      case 'delete':
        this.deleteSession(sessionId);
        break;
    }
  },

  // Handle folder context menu action
  handleFolderContextAction(action) {
    const folderId = this.contextMenuTarget;
    if (!folderId) return;

    this.hideContextMenus();

    switch (action) {
      case 'new-session':
        this.showCreateChatModalForWorkspace(folderId);
        break;
      case 'new-note':
        this.createNewNoteForFolder(folderId);
        break;
      case 'new-task':
        this.openTaskModalForWorkspace(folderId);
        break;
      case 'paste-note':
        this.pasteNoteToFolder(folderId);
        break;
      case 'rename':
        this.startFolderRename(folderId);
        break;
      case 'color':
        this.showFolderColorPicker(folderId);
        break;
      case 'delete':
        this.deleteFolder(folderId);
        break;
    }
  },

  // Start inline rename for session
  startInlineRename(sessionId) {
    const sessionItem = document.querySelector(`.session-item[data-session-id="${sessionId}"]`);
    if (!sessionItem) return;

    const titleSpan = sessionItem.querySelector('.session-title');
    if (!titleSpan) return;

    const currentTitle = titleSpan.textContent;

    // Create input
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'session-title-input';
    input.value = currentTitle;

    // Replace span with input
    titleSpan.replaceWith(input);
    input.focus();
    input.select();

    // Handle save
    const saveRename = () => {
      const newTitle = input.value.trim() || currentTitle;
      input.replaceWith(titleSpan);
      if (newTitle !== currentTitle) {
        this.renameSession(sessionId, newTitle);
      }
    };

    input.addEventListener('blur', saveRename);
    input.addEventListener('keydown', e => {
      if (e.key === 'Enter') {
        e.preventDefault();
        input.blur();
      } else if (e.key === 'Escape') {
        input.value = currentTitle;
        input.blur();
      }
    });
  },

  // Start inline rename for folder
  startFolderRename(folderId) {
    const folderItem = document.querySelector(`.folder-item[data-folder-id="${folderId}"]`);
    if (!folderItem) return;

    const nameSpan = folderItem.querySelector('.folder-name');
    if (!nameSpan) return;

    const currentName = nameSpan.textContent;

    // Create input
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'folder-name-input';
    input.value = currentName;

    // Replace span with input
    nameSpan.replaceWith(input);
    input.focus();
    input.select();

    // Handle save
    const saveRename = async () => {
      const newName = input.value.trim() || currentName;
      input.replaceWith(nameSpan);
      if (newName !== currentName) {
        await this.renameFolder(folderId, newName);
      }
    };

    input.addEventListener('blur', saveRename);
    input.addEventListener('keydown', e => {
      if (e.key === 'Enter') {
        e.preventDefault();
        input.blur();
      } else if (e.key === 'Escape') {
        input.value = currentName;
        input.blur();
      }
    });
  },

  // Mirrors internal/workspace.Slugify so the folder-name preview and the
  // rename "folder saved as" check match what the server writes to disk:
  // lowercase, strip accents, non-alphanumeric to hyphens, collapse/trim
  // hyphens, cap at 64 chars, "untitled" for empty input.
  slugifyWorkspaceName(value) {
    const s = String(value || '').trim();
    if (!s) return 'untitled';
    let slug = s
      .normalize('NFD')
      .replace(/\p{Mn}/gu, '')
      .toLowerCase()
      .replace(/[^a-z0-9-]+/g, '-')
      .replace(/-{2,}/g, '-')
      .replace(/^-+|-+$/g, '');
    if (slug.length > 64) slug = slug.slice(0, 64).replace(/-+$/g, '');
    return slug || 'untitled';
  },

  buildWorkspaceSlugConflictMessage(conflict) {
    const requestedSlug =
      typeof conflict?.requested_slug === 'string' ? conflict.requested_slug.trim() : '';
    const suggestedSlug =
      typeof conflict?.suggested_slug === 'string' ? conflict.suggested_slug.trim() : '';
    const location =
      typeof conflict?.location === 'string' ? conflict.location.trim().replace(/[\\/]+$/, '') : '';
    const suggestedPath = location && suggestedSlug ? `${location}/${suggestedSlug}` : '';

    const parts = [
      `A workspace folder named "${requestedSlug || 'this workspace'}" already exists on disk.`
    ];
    if (suggestedSlug) {
      parts.push(`Rename this workspace with the folder name "${suggestedSlug}" instead?`);
    }
    if (suggestedPath) {
      parts.push(`Folder: ${suggestedPath}`);
    }
    return parts.join('\n\n');
  },

  // Rename folder via API
  async renameFolder(folderId, newName, folderSlug = '') {
    try {
      const payload = { name: newName };
      if (folderSlug) {
        payload.folder_slug = folderSlug;
      }

      const response = await fetch(`/api/workspaces/${folderId}/rename`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      const result = await response.json().catch(() => ({}));
      if (response.status === 409 && result?.conflict?.type === 'folder_slug') {
        const suggestedSlug =
          typeof result.conflict.suggested_slug === 'string'
            ? result.conflict.suggested_slug.trim()
            : '';
        if (
          suggestedSlug &&
          window.confirm(this.buildWorkspaceSlugConflictMessage(result.conflict))
        ) {
          return this.renameFolder(folderId, newName, suggestedSlug);
        }
        const cancelled = new Error(result?.error || 'Workspace rename cancelled');
        cancelled.cancelled = true;
        throw cancelled;
      }

      if (!response.ok) {
        throw new Error(result?.error || result?.message || 'Failed to rename workspace');
      }

      // Update local data
      const folder = this.findFolderById(folderId, this.folders);
      const updatedFolder = result?.folder || result?.workspace || null;
      if (folder) {
        if (updatedFolder && typeof updatedFolder === 'object') {
          Object.assign(folder, updatedFolder);
        } else {
          folder.name = newName;
        }
      }

      // Re-render
      this.renderFolderTree();
      const appliedSlug = String(updatedFolder?.folder_slug || '').trim();
      const expectedSlug = this.slugifyWorkspaceName(newName);
      this.showToast(
        appliedSlug && expectedSlug && appliedSlug !== expectedSlug
          ? `Workspace renamed. Folder saved as "${appliedSlug}".`
          : 'Workspace renamed',
        'success'
      );
    } catch (error) {
      console.error('Failed to rename folder:', error);
      if (!error?.cancelled) {
        this.showToast(error.message || 'Failed to rename workspace', 'error');
      }
      // Reload to restore correct state
      await this.loadFolders();
    }
  },

  // Show folder color picker
  showFolderColorPicker(folderId) {
    const folder = this.findFolderById(folderId, this.folders);
    if (!folder) return;

    const folderItem = document.querySelector(`.folder-item[data-folder-id="${folderId}"]`);
    if (!folderItem) return;

    // Create color picker popup
    const popup = document.createElement('div');
    popup.className = 'folder-color-picker-popup';
    popup.innerHTML = `
      <div class="folder-color-options">
        <button class="folder-color-option" data-color="" title="Default">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <button class="folder-color-option" data-color="#ef4444" style="background: #ef4444;" title="Red"></button>
        <button class="folder-color-option" data-color="#f97316" style="background: #f97316;" title="Orange"></button>
        <button class="folder-color-option" data-color="#eab308" style="background: #eab308;" title="Yellow"></button>
        <button class="folder-color-option" data-color="#22c55e" style="background: #22c55e;" title="Green"></button>
        <button class="folder-color-option" data-color="#3b82f6" style="background: #3b82f6;" title="Blue"></button>
        <button class="folder-color-option" data-color="#8b5cf6" style="background: #8b5cf6;" title="Purple"></button>
        <button class="folder-color-option" data-color="#ec4899" style="background: #ec4899;" title="Pink"></button>
      </div>
    `;

    // Position popup near the folder item
    const rect = folderItem.getBoundingClientRect();
    popup.style.top = `${rect.bottom + 4}px`;
    popup.style.left = `${rect.left}px`;

    document.body.appendChild(popup);

    // Handle color selection
    popup.querySelectorAll('.folder-color-option').forEach(btn => {
      btn.addEventListener('click', async () => {
        const color = btn.dataset.color;
        await this.setFolderColor(folderId, color);
        popup.remove();
      });
    });

    // Close on click outside
    const closePopup = e => {
      if (!popup.contains(e.target)) {
        popup.remove();
        document.removeEventListener('click', closePopup);
      }
    };
    setTimeout(() => document.addEventListener('click', closePopup), 0);
  },

  // Set folder color via API
  async setFolderColor(folderId, color) {
    try {
      const response = await fetch(`/api/workspaces/${folderId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ color: color })
      });

      if (!response.ok) throw new Error('Failed to update workspace color');

      // Update local data
      const folder = this.findFolderById(folderId, this.folders);
      if (folder) {
        folder.color = color;
      }

      // Re-render
      this.renderFolderTree();
    } catch (error) {
      console.error('Failed to set folder color:', error);
      this.showToast('Failed to update color', 'error');
    }
  },

  // Show move to folder modal
  showMoveToFolderModal(sessionId) {
    const container = document.getElementById('moveToFolderList');
    if (!container) return;

    // Find current folder
    const session = this.sessions.find(s => s.id === sessionId);
    const currentFolderId = session?.folder_id;
    const assignableFolders = [];
    const collectAssignableFolders = (folders, depth = 0) => {
      (folders || []).forEach(folder => {
        if (!folder || !folder.id) return;
        if (String(folder.kind || '').trim() !== 'group') {
          assignableFolders.push({
            id: folder.id,
            name: folder.name,
            color: folder.color,
            depth
          });
        }
        collectAssignableFolders(folder.children || [], depth + 1);
      });
    };
    collectAssignableFolders(this.folders);

    // Render folder options
    container.innerHTML = `
      <div class="move-folder-item ${!currentFolderId ? 'selected' : ''}" data-folder-id="">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M19,20H5A2,2 0 0,1 3,18V6A2,2 0 0,1 5,4H9L11,6H19A2,2 0 0,1 21,8V18A2,2 0 0,1 19,20M5,18H19V10H5V18M5,8H9L7,6H5V8Z"/>
        </svg>
        No Workspace
      </div>
      ${assignableFolders
        .map(
          folder => `
        <div class="move-folder-item ${folder.id === currentFolderId ? 'selected' : ''}" data-folder-id="${folder.id}">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="${folder.color || 'currentColor'}" class="me-2">
            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
          </svg>
          ${this.escapeHtml(`${folder.depth > 0 ? `${'--'.repeat(folder.depth)} ` : ''}${folder.name}`)}
        </div>
      `
        )
        .join('')}
    `;

    // Bind click handlers
    container.querySelectorAll('.move-folder-item').forEach(item => {
      item.addEventListener('click', async () => {
        const folderId = item.dataset.folderId;
        await this.moveSessionToFolder(sessionId, folderId);

        const modal = bootstrap.Modal.getInstance(document.getElementById('moveToFolderModal'));
        modal?.hide();
      });
    });

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('moveToFolderModal'));
    modal.show();
  },

  // Show edit tags modal (shared tag input widget with unified-pool suggestions)
  showEditTagsModal(sessionId) {
    const session = this.sessions.find(s => s.id === sessionId);
    if (!session) return;

    const currentTags = session.tags || [];
    const mount = document.getElementById('sessionTagsMount');
    if (mount && window.OriTagInput?.createTagInput) {
      if (!this.sessionTagsWidget) {
        this.sessionTagsWidget = window.OriTagInput.createTagInput({
          container: mount,
          initialTags: currentTags
        });
      } else {
        this.sessionTagsWidget.setTags(currentTags);
      }
      void this.sessionTagsWidget.refreshPool?.();
    }

    // Setup save button (Save commits, Cancel discards)
    const saveBtn = document.getElementById('saveTagsBtn');
    if (saveBtn) {
      saveBtn.onclick = async () => {
        const tags = this.sessionTagsWidget ? this.sessionTagsWidget.getTags() : currentTags;
        await this.updateSessionTags(sessionId, tags);
        window.OriTagInput?.clearTagPoolCache?.();

        const modal = bootstrap.Modal.getInstance(document.getElementById('editTagsModal'));
        modal?.hide();
      };
    }

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('editTagsModal'));
    modal.show();
  },

  // Show add folder modal
  showAddWorkspaceModal(options = {}) {
    const modalElement = document.getElementById('addFolderModal');
    if (!modalElement) return;

    modalElement.dataset.pendingImportMode = options.importMode ? 'true' : 'false';
    modalElement.dataset.pendingEntryPoint = String(
      options.entryPoint || (options.importMode ? 'workspace_hub_import' : 'workspace_hub_create')
    );
    // Map-origin creation (#292 FR-51). The Workspace Map holds the coordinate
    // the user chose; this flag only records that the create came from there, so
    // a successful create returns to the map instead of navigating into the new
    // workspace. There is no second creation form — this is the same modal.
    if (options.mapOrigin) {
      modalElement.dataset.pendingMapOrigin = 'true';
    } else {
      delete modalElement.dataset.pendingMapOrigin;
    }
    if (options.blueprint) {
      modalElement.dataset.pendingBlueprint = String(options.blueprint);
    } else {
      delete modalElement.dataset.pendingBlueprint;
    }

    const modal = new bootstrap.Modal(modalElement);
    modal.show();
  },

  // preselectBlueprintCard selects the blueprint card matching templateId, so a
  // deep-linked Construct flow (e.g. the HQ email station's "Set up Email Ops"
  // CTA) lands on the right blueprint. The picker renders asynchronously and can
  // re-render to its Blank default after the modal opens, wiping an early click;
  // so this re-clicks until the selection actually sticks (verified against
  // ProjectTemplateCard.getSelectedTemplate) or a short budget elapses. Quietly
  // gives up if the card never appears.
  preselectBlueprintCard(templateId) {
    const id = String(templateId || '').trim();
    if (!id) return;
    let attempts = 0;
    const step = () => {
      const picker = typeof window !== 'undefined' ? window.ProjectTemplateCard : null;
      const selected = picker && picker.getSelectedTemplate ? picker.getSelectedTemplate() : null;
      const selectedId = selected ? String(selected.id || '') : '';
      if (selectedId === id || selectedId.endsWith(`:${id}`)) return; // stuck — done
      // A blueprint published by a plugin carries a namespaced template ID
      // ("plugin:<plugin>:<blueprint>"), so a deep link naming the bare
      // blueprint ID also matches the card whose ID ends with it.
      const card =
        document.querySelector(
          `.workspace-template-card[data-template-id="${id}"], .workspace-template-row[data-template-id="${id}"]`
        ) ||
        document.querySelector(
          `.workspace-template-card[data-template-id$=":${id}"], .workspace-template-row[data-template-id$=":${id}"]`
        );
      if (card) card.click();
      if (attempts++ < 40) setTimeout(step, 75);
    };
    step();
  },

  // Show import workspace modal
  showImportWorkspaceModal() {
    this.showAddWorkspaceModal({ importMode: true, entryPoint: 'workspace_hub_import' });
  },

  // Show session info modal
  showSessionInfoModal(sessionId) {
    const session = this.sessions.find(s => s.id === sessionId);
    if (!session) return;

    const infoBody = document.getElementById('sessionInfoBody');
    if (!infoBody) return;

    const folderName = this.getFolderName(session.folder_id);
    const tags = session.tags || [];
    const createdAt = this.formatDateTime(session.created_at);
    const updatedAt = this.formatDateTime(session.updated_at);

    infoBody.innerHTML = `
      <div class="session-info-row">
        <span class="session-info-label">Title</span>
        <span class="session-info-value">${this.escapeHtml(session.title || 'New Session')}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Agent</span>
        <div class="session-info-value d-flex align-items-center gap-2">
          <span id="sessionInfoAgentName">${this.escapeHtml(session.agent_name || 'Unknown')}</span>
          <button type="button" class="btn btn-sm btn-outline-primary" id="changeSessionAgentBtn" data-session-id="${session.id}">
            Change
          </button>
        </div>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Workspace</span>
        <span class="session-info-value">${this.escapeHtml(folderName)}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Messages</span>
        <span class="session-info-value">${session.message_count ?? 0}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Created</span>
        <span class="session-info-value">${this.escapeHtml(createdAt)}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Updated</span>
        <span class="session-info-value">${this.escapeHtml(updatedAt)}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Tags</span>
        <span class="session-info-value">${tags.length > 0 ? tags.map(tag => this.escapeHtml(tag)).join(', ') : 'None'}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">ID</span>
        <span class="session-info-value session-info-mono">${this.escapeHtml(session.id)}</span>
      </div>
    `;

    // Bind change agent button
    document.getElementById('changeSessionAgentBtn')?.addEventListener('click', async () => {
      const sessionId = document.getElementById('changeSessionAgentBtn').dataset.sessionId;
      // Close the info modal first
      bootstrap.Modal.getInstance(document.getElementById('sessionInfoModal'))?.hide();
      // Show agent picker for changing
      await this.showChangeAgentDialog(sessionId);
    });

    const modal = new bootstrap.Modal(document.getElementById('sessionInfoModal'));
    modal.show();
  },

  // Show dialog to change session's agent
  async showChangeAgentDialog(sessionId) {
    const session = this.sessions.find(s => s.id === sessionId);
    if (!session) return;

    const agents = await this.fetchAgents();
    if (!agents || agents.length === 0) return;

    // Remove existing modal if any
    const existingModal = document.getElementById('changeAgentModal');
    if (existingModal) existingModal.remove();

    const modalHtml = `
      <div class="modal fade" id="changeAgentModal" tabindex="-1" style="z-index: 10700;">
        <div class="modal-dialog modal-dialog-centered">
          <div class="modal-content bg-dark text-light">
            <div class="modal-header border-secondary">
              <h5 class="modal-title">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M12,2A2,2 0 0,1 14,4C14,4.74 13.6,5.39 13,5.73V7H14A7,7 0 0,1 21,14H22A1,1 0 0,1 23,15V18A1,1 0 0,1 22,19H21V20A2,2 0 0,1 19,22H5A2,2 0 0,1 3,20V19H2A1,1 0 0,1 1,18V15A1,1 0 0,1 2,14H3A7,7 0 0,1 10,7H11V5.73C10.4,5.39 10,4.74 10,4A2,2 0 0,1 12,2M7.5,13A2.5,2.5 0 0,0 5,15.5A2.5,2.5 0 0,0 7.5,18A2.5,2.5 0 0,0 10,15.5A2.5,2.5 0 0,0 7.5,13M16.5,13A2.5,2.5 0 0,0 14,15.5A2.5,2.5 0 0,0 16.5,18A2.5,2.5 0 0,0 19,15.5A2.5,2.5 0 0,0 16.5,13Z"/>
                </svg>
                Change Session Agent
              </h5>
              <button type="button" class="btn-close btn-close-white" data-bs-dismiss="modal"></button>
            </div>
            <div class="modal-body">
              <p class="text-muted small mb-3">Select a new agent for this session. Future messages will use the selected agent.</p>
              <div class="list-group list-group-flush">
                ${agents
                  .map(
                    agent => `
                  <button type="button" class="list-group-item list-group-item-action bg-dark text-light border-secondary change-agent-item ${agent.name === session.agent_name ? 'active' : ''}"
                          data-agent-name="${this.escapeHtml(agent.name)}">
                    <div class="d-flex justify-content-between align-items-center">
                      <div>
                        <strong>${this.escapeHtml(agent.name)}</strong>
                        ${agent.model ? `<small class="text-muted ms-2">${this.escapeHtml(agent.model)}</small>` : ''}
                      </div>
                      ${agent.name === session.agent_name ? '<span class="badge bg-success">Current</span>' : ''}
                    </div>
                  </button>
                `
                  )
                  .join('')}
              </div>
            </div>
            <div class="modal-footer border-secondary">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>
            </div>
          </div>
        </div>
      </div>
    `;

    document.body.insertAdjacentHTML('beforeend', modalHtml);

    const modal = new bootstrap.Modal(document.getElementById('changeAgentModal'));

    // Handle agent selection
    document.querySelectorAll('.change-agent-item').forEach(item => {
      item.addEventListener('click', async () => {
        const newAgentName = item.dataset.agentName;
        if (newAgentName === session.agent_name) {
          modal.hide();
          return;
        }

        modal.hide();
        await this.changeSessionAgent(sessionId, newAgentName);
      });
    });

    // Cleanup on modal hidden
    document.getElementById('changeAgentModal').addEventListener('hidden.bs.modal', () => {
      document.getElementById('changeAgentModal')?.remove();
    });

    modal.show();
  },

  // Change a session's agent
  async changeSessionAgent(sessionId, newAgentName) {
    try {
      const response = await fetch(`/api/sessions/${sessionId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agent_name: newAgentName })
      });

      if (!response.ok) throw new Error('Failed to update session');

      const data = await response.json();
      if (data.session) {
        // Update local session data
        const index = this.sessions.findIndex(s => s.id === sessionId);
        if (index >= 0) {
          this.sessions[index] = data.session;
        }

        // Re-render sessions
        this.renderSessions();

        // If this is the active session, update the agent display
        if (sessionId === this.activeSessionId) {
          this.updateCurrentAgent(newAgentName);
        }
      }
    } catch (error) {
      console.error('Failed to change session agent:', error);
      alert('Failed to change session agent');
    }
  },

  // Update folder drag hint text
  setDragHint(text) {
    const hint = document.getElementById('folderDragHint');
    if (!hint) return;
    if (!text) {
      hint.style.display = 'none';
      hint.textContent = '';
      return;
    }
    hint.textContent = text;
    hint.style.display = 'inline-flex';
  },

  // Flash drag hint for a short duration
  flashDragHint(text) {
    this.setDragHint(text);
    clearTimeout(this.dragHintTimer);
    this.dragHintTimer = setTimeout(() => this.setDragHint(''), 1600);
  },

  // Build hint text for a move action
  formatMoveHint(count, folderName = null) {
    const label = count === 1 ? 'session' : 'sessions';
    if (!folderName) return `Drop to move ${count} ${label}`;
    return `Moved ${count} ${label} to ${folderName}`;
  },

  // Setup resize handle
  setupResizeHandle() {
    const handle = document.getElementById('sessionSidebarResizeHandle');
    const sidebar = document.getElementById('sessionSidebar');
    if (!handle || !sidebar) return;

    let isResizing = false;
    let startX = 0;
    let startWidth = 0;

    handle.addEventListener('mousedown', e => {
      isResizing = true;
      startX = e.clientX;
      startWidth = sidebar.offsetWidth;
      handle.classList.add('resizing');
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    });

    document.addEventListener('mousemove', e => {
      if (!isResizing) return;

      const width = startWidth + (e.clientX - startX);
      const clampedWidth = Math.max(200, Math.min(400, width));

      document.documentElement.style.setProperty('--session-sidebar-width', `${clampedWidth}px`);
      localStorage.setItem('sessionSidebarWidth', clampedWidth);
    });

    document.addEventListener('mouseup', () => {
      if (isResizing) {
        isResizing = false;
        handle.classList.remove('resizing');
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
      }
    });

    // Restore saved width
    const savedWidth = localStorage.getItem('sessionSidebarWidth');
    if (savedWidth) {
      document.documentElement.style.setProperty('--session-sidebar-width', `${savedWidth}px`);
    }
  },

  // Handle drag start
  handleDragStart(e, sessionId) {
    if (!this.selectedSessionIds.includes(sessionId)) {
      this.setSelection([sessionId], this.getSessionIndex(sessionId));
    }
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData(
      'application/x-ori-session-ids',
      JSON.stringify(this.selectedSessionIds)
    );
    e.dataTransfer.setData('text/plain', String(sessionId));
    e.currentTarget.classList.add('dragging');
    document.getElementById('folderTreeSection')?.classList.add('dragging');
    this.setDragHint(this.formatMoveHint(this.selectedSessionIds.length));
    const tree = document.getElementById('folderTree');
    const isCollapsed = tree && tree.style.display === 'none';
    this.folderTreeWasCollapsedOnDrag = isCollapsed;
    if (isCollapsed) {
      this.toggleFolderTree(true);
    }
  },

  // Handle drag end
  handleDragEnd() {
    document.querySelectorAll('.session-item').forEach(item => {
      item.classList.remove('dragging');
    });
    document.querySelectorAll('.folder-item').forEach(item => {
      item.classList.remove('drag-over');
    });
    document.getElementById('folderTreeSection')?.classList.remove('dragging');
    this.setDragHint('');
    if (this.folderTreeWasCollapsedOnDrag) {
      this.toggleFolderTree(false);
      this.folderTreeWasCollapsedOnDrag = false;
    }
  },

  // Handle session click with multi-select support
  handleSessionClick(e, sessionId) {
    const clickedIndex = this.getSessionIndex(sessionId);

    if (e.shiftKey && this.lastSelectedIndex !== null && clickedIndex !== -1) {
      this.selectRange(this.lastSelectedIndex, clickedIndex, e.ctrlKey || e.metaKey);
      return;
    }

    if (e.ctrlKey || e.metaKey) {
      this.toggleSelection(sessionId, clickedIndex);
      return;
    }

    this.setSelection([sessionId], clickedIndex);
    this.switchToSession(sessionId);
  },

  // Keep selection aligned to current session list
  reconcileSelection() {
    const sessionIds = new Set(this.sessions.map(session => session.id));
    this.selectedSessionIds = this.selectedSessionIds.filter(id => sessionIds.has(id));
    if (this.selectedSessionIds.length === 0) {
      this.lastSelectedIndex = null;
      return;
    }
    const lastId = this.selectedSessionIds[this.selectedSessionIds.length - 1];
    this.lastSelectedIndex = this.getSessionIndex(lastId);
  },

  // Update selection UI
  updateSelectionUI() {
    const selected = new Set(this.selectedSessionIds);
    document.querySelectorAll('.session-item').forEach(item => {
      item.classList.toggle('selected', selected.has(item.dataset.sessionId));
    });
  },

  // Clear selection
  clearSelection() {
    this.selectedSessionIds = [];
    this.lastSelectedIndex = null;
    this.updateSelectionUI();
  },

  // Set selection explicitly
  setSelection(sessionIds, lastIndex) {
    this.selectedSessionIds = Array.from(new Set(sessionIds));
    this.lastSelectedIndex = Number.isInteger(lastIndex) ? lastIndex : null;
    this.updateSelectionUI();
  },

  // Toggle a single session in selection
  toggleSelection(sessionId, index) {
    const existingIndex = this.selectedSessionIds.indexOf(sessionId);
    if (existingIndex >= 0) {
      this.selectedSessionIds.splice(existingIndex, 1);
      if (this.selectedSessionIds.length === 0) {
        this.lastSelectedIndex = null;
      }
    } else {
      this.selectedSessionIds.push(sessionId);
      this.lastSelectedIndex = Number.isInteger(index) ? index : this.lastSelectedIndex;
    }
    this.updateSelectionUI();
  },

  // Select a range between two indices
  selectRange(fromIndex, toIndex, additive) {
    if (fromIndex === null || toIndex === null) return;
    const start = Math.min(fromIndex, toIndex);
    const end = Math.max(fromIndex, toIndex);
    const rangeIds = this.sessions.slice(start, end + 1).map(session => session.id);
    if (additive) {
      const combined = new Set([...this.selectedSessionIds, ...rangeIds]);
      this.setSelection(Array.from(combined), toIndex);
      return;
    }
    this.setSelection(rangeIds, toIndex);
  },

  // Get session index by id
  getSessionIndex(sessionId) {
    return this.sessions.findIndex(session => session.id === sessionId);
  },

  // Get dragged session IDs from data transfer
  getDraggedSessionIds(e) {
    const payload = e.dataTransfer.getData('application/x-ori-session-ids');
    if (payload) {
      try {
        const parsed = JSON.parse(payload);
        if (Array.isArray(parsed)) {
          return parsed;
        }
      } catch (error) {
        console.warn('Failed to parse dragged session ids', error);
      }
    }
    const single = e.dataTransfer.getData('text/plain');
    return single ? [single] : [];
  },

  // Handle scroll (for virtual scrolling)
  handleScroll() {
    if (!this.virtualScrollEnabled) return;
    // Virtual scroll implementation would go here
  },

  // Save active session to localStorage (tab-specific for multi-tab support)
  saveActiveSession() {
    const tabKey = `activeSessionId_${this.tabId}`;
    if (this.activeSessionId) {
      // Use sessionStorage for tab-specific isolation
      sessionStorage.setItem(tabKey, this.activeSessionId);
      // Also save to localStorage for backward compatibility
      localStorage.setItem('activeSessionId', this.activeSessionId);
    } else {
      sessionStorage.removeItem(tabKey);
      localStorage.removeItem('activeSessionId');
    }
  },

  // Get the active session object
  getActiveSession() {
    if (!this.activeSessionId) return null;
    return this.sessions.find(s => s.id === this.activeSessionId) || null;
  },

  // Restore active session from localStorage
  // Returns true if session was restored, false otherwise
  async restoreActiveSession() {
    // Use tab-specific key for multi-tab support
    const tabKey = `activeSessionId_${this.tabId}`;
    let savedId = sessionStorage.getItem(tabKey);

    // Fallback to legacy localStorage for backward compatibility
    if (!savedId) {
      savedId = localStorage.getItem('activeSessionId');
    }

    const session = savedId ? this.sessions.find(s => s.id === savedId) : null;
    if (session) {
      this.activeSessionId = savedId;
      this.renderSessions();

      // Update the combined chat info bar with session title and agent
      this.updateChatInfoBar(session.title || 'New Session', session.agent_name);

      this.updateCurrentAgent(session.agent_name || '');

      // Initialize session files panel
      this.initializeSessionFiles(savedId);

      // Load tasks for the restored session
      await this.loadSessionTasks();

      return true;
    }
    return false;
  },

  // Refresh the active session's data (called after chat messages)
  async refreshActiveSession() {
    if (!this.activeSessionId) return;

    try {
      // Just update the session's updated_at and preview in the list
      const response = await fetch(`/api/sessions/${this.activeSessionId}`);
      if (!response.ok) return;

      const session = await response.json();

      // Update the session in our local list
      const index = this.sessions.findIndex(s => s.id === this.activeSessionId);
      if (index >= 0) {
        this.sessions[index] = session;
        // Move to top if sorted by updated_at
        if (this.sortBy === 'updated_at' && index > 0) {
          this.sessions.splice(index, 1);
          this.sessions.unshift(session);
        }
        this.renderSessions();
      }
    } catch (error) {
      console.error('Failed to refresh active session:', error);
    }
  },

  // Start polling for session list updates
  startPolling() {
    if (this.pollTimer) return;

    this.pollTimer = setInterval(() => {
      this.loadSessions();
    }, this.pollInterval);
  },

  // Stop polling
  stopPolling() {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  },

  // Called when a message is sent to refresh the session list
  onMessageSent() {
    // Debounce to avoid too frequent updates
    if (this.messageDebounceTimer) {
      clearTimeout(this.messageDebounceTimer);
    }
    this.messageDebounceTimer = setTimeout(() => {
      this.refreshActiveSession();
      // Check if active session needs auto-classification
      this.checkAutoClassification();
    }, 500);
  },

  // Track message count for auto-mode sessions
  autoModeMessageCounts: new Map(),

  // Check if the active session needs auto-classification
  async checkAutoClassification() {
    const sessionId = this.activeSessionId;
    if (!sessionId) return;

    // Only check sessions that are in auto mode
    if (!this.autoModeSessionIds.has(sessionId)) return;

    // Increment message count for this session
    const currentCount = (this.autoModeMessageCounts.get(sessionId) || 0) + 1;
    this.autoModeMessageCounts.set(sessionId, currentCount);

    // Trigger classification after 3 message exchanges (6 messages: 3 user + 3 assistant)
    // This gives the AI enough context to understand the conversation
    if (currentCount >= 3) {
      await this.triggerAutoClassification(sessionId);
    }
  },

  // Trigger auto-classification for a session
  async triggerAutoClassification(sessionId) {
    try {
      const response = await fetch('/api/sessions/auto-classify', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sessionId })
      });

      if (!response.ok) {
        console.error('Auto-classify failed:', response.statusText);
        return;
      }

      const result = await response.json();

      if (result.applied) {
        // Remove from auto-mode tracking
        this.autoModeSessionIds.delete(sessionId);
        this.autoModeMessageCounts.delete(sessionId);

        // Refresh session list to show updated workspace/agent
        await this.loadSessions();

        // Show a notification
        if (window.Toast) {
          let message = 'Chat auto-assigned';
          if (result.workspace_name) {
            message += ` to workspace "${result.workspace_name}"`;
          }
          if (result.agent_name) {
            message += result.workspace_name
              ? ` with agent "${result.agent_name}"`
              : ` to agent "${result.agent_name}"`;
          }
          Toast.success(message);
        }

        // Update the chat info bar if the agent changed
        if (result.agent_name) {
          const session = this.sessions.find(s => s.id === sessionId);
          if (session) {
            this.updateChatInfoBar(session.title || 'Chat', result.agent_name);
            this.updateCurrentAgent(result.agent_name);
          }
        }
      } else if (result.reasoning) {
        // After 3 attempts without a match, stop trying
        const count = this.autoModeMessageCounts.get(sessionId) || 0;
        if (count >= 6) {
          this.autoModeSessionIds.delete(sessionId);
          this.autoModeMessageCounts.delete(sessionId);
        }
      }
    } catch (error) {
      console.error('Auto-classify error:', error);
    }
  },

  // Format time ago
  formatTimeAgo(dateString) {
    if (!dateString) return '';

    const date = new Date(dateString);
    const now = new Date();
    const seconds = Math.floor((now - date) / 1000);

    if (seconds < 60) return 'now';
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
    if (seconds < 604800) return `${Math.floor(seconds / 86400)}d`;
    if (seconds < 2592000) return `${Math.floor(seconds / 604800)}w`;

    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  },

  // Get tag color based on hash
  getTagColor(tag) {
    let hash = 0;
    for (let i = 0; i < tag.length; i++) {
      hash = tag.charCodeAt(i) + ((hash << 5) - hash);
    }
    return Math.abs(hash) % 8;
  },

  // Escape HTML
  escapeHtml(text) {
    return window.NoteEditor?.escapeHtml(text) ?? String(text ?? '');
  },

  // Convert hex color to rgba string
  hexToRgba(hex, alpha) {
    if (!hex || typeof hex !== 'string') return '';
    const cleaned = hex.replace('#', '').trim();
    if (cleaned.length !== 6) return '';
    const r = parseInt(cleaned.slice(0, 2), 16);
    const g = parseInt(cleaned.slice(2, 4), 16);
    const b = parseInt(cleaned.slice(4, 6), 16);
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
  },

  // Format a readable date/time
  formatDateTime(dateString) {
    if (!dateString) return '';
    const date = new Date(dateString);
    return date.toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: 'numeric',
      minute: '2-digit'
    });
  },

  // Find folder name by id
  getFolderName(folderId) {
    if (!folderId) return 'No Workspace';
    const folder = this.findFolderById(folderId, this.folders);
    return folder?.name || 'Unknown';
  },

  // Recursive folder lookup
  findFolderById(folderId, folders) {
    for (const folder of folders || []) {
      if (folder.id === folderId) return folder;
      const match = this.findFolderById(folderId, folder.children || []);
      if (match) return match;
    }
    return null;
  },

  // ============================================================================
  // Utilities
  // ============================================================================

  // Show toast notification
  showToast(message, type = 'info') {
    const normalizedType = (type || 'info').toLowerCase();
    const toastType =
      normalizedType === 'danger'
        ? 'error'
        : normalizedType === 'warn'
          ? 'warning'
          : normalizedType;

    if (typeof window.notifyToast === 'function') {
      window.notifyToast(message, toastType);
      return;
    }

    if (window.Toast) {
      const toastFn = window.Toast[toastType];
      if (typeof toastFn === 'function') {
        toastFn(message);
        return;
      }
      if (typeof window.Toast.show === 'function') {
        window.Toast.show(message, toastType);
        return;
      }
    }

    if (typeof window.showToast === 'function') {
      window.showToast(message, type);
      return;
    }
  },

  // ============================================================================
  // Folder Notes
  // ============================================================================

  // Notes state (keyed by folder ID)
  notesByFolder: new Map(),
  currentNote: null,
  noteModalWorkspaceId: null,
  isNotePreviewMode: false,
  // The note editor's four controllers (history / autosave / toc / live)
  // live in a single mount bundle from note-editor.js. Lazily built via
  // _ensureMount the first time anything needs them. Backward-compat
  // getters below (noteHistory, noteAutoSave, noteToc, noteLive) keep
  // existing call sites working.
  noteMount: null,
  noteModalHideInProgress: false,
  noteModalAllowHide: false,
  noteAIGeneratedDraft: null,
  get noteLive() {
    return this._ensureMount()?.live ?? null;
  },
  set noteLive(_v) {
    /* no-op — mount owns the live instance */
  },
  // Back-compat: the original sessionManager exposed `noteLiveState` as a
  // bare object. That alias still resolves to the controller's state.
  get noteLiveState() {
    return this._ensureLive()?.state;
  },
  // Field-level back-compat. Callers that read/write `this.noteLive*` keep
  // working — the getters/setters target the controller's state.
  get noteLiveActiveLineIndex() {
    return this._ensureLive()?.state.activeLineIndex ?? null;
  },
  set noteLiveActiveLineIndex(v) {
    const s = this._ensureLive()?.state;
    if (s) s.activeLineIndex = v;
  },
  get noteLiveActiveRange() {
    return this._ensureLive()?.state.activeRange ?? null;
  },
  set noteLiveActiveRange(v) {
    const s = this._ensureLive()?.state;
    if (s) s.activeRange = v;
  },
  get noteLiveSelectionAnchorIndex() {
    return this._ensureLive()?.state.selectionAnchorIndex ?? null;
  },
  set noteLiveSelectionAnchorIndex(v) {
    const s = this._ensureLive()?.state;
    if (s) s.selectionAnchorIndex = v;
  },
  get noteLiveSelectionFocusIndex() {
    return this._ensureLive()?.state.selectionFocusIndex ?? null;
  },
  set noteLiveSelectionFocusIndex(v) {
    const s = this._ensureLive()?.state;
    if (s) s.selectionFocusIndex = v;
  },
  get noteLivePointerDown() {
    return this._ensureLive()?.state.pointerDown ?? null;
  },
  set noteLivePointerDown(v) {
    const s = this._ensureLive()?.state;
    if (s) s.pointerDown = v;
  },
  get noteLiveCollapsedHeadings() {
    const s = this._ensureLive()?.state;
    return s ? s.collapsedHeadings : new Set();
  },
  set noteLiveCollapsedHeadings(v) {
    const s = this._ensureLive()?.state;
    if (s) s.collapsedHeadings = v;
  },

  // _ensureMount builds the four-controller bundle on first access. The
  // mount factory wires history/autosave/toc/live to a shared sub-host.
  _ensureMount() {
    if (this.noteMount) return this.noteMount;
    if (!window.NoteEditor) return null;
    // mount() owns the render path; sessionManager.renderNoteLiveEditor
    // delegates to bundle.render. The aiAssist sub-config wires the AI
    // selection sidebar in the same call (modal-show check stays here via
    // _readNoteSelection).
    this.noteMount = window.NoteEditor.mount({
      getContent: () => this.getNoteContentValue(),
      setContent: v => this.setNoteContentValue(v),
      getContentLines: () => this.getNoteContentLines(),
      setContentLines: lines => this.setNoteContentLines(lines),
      isPreviewMode: () => this.isNotePreviewMode,
      onAutosaveFlush: () => this.autoSaveNote(),
      aiAssist: {
        readSelection: () => this._readNoteSelection(),
        showToast: (msg, kind) => this.showToast?.(msg, kind)
      }
    });
    window.NoteWikilinks?.setWorkspaceContext(
      () => this.noteModalWorkspaceId || this.currentNote?.workspace_id || null
    );
    // Left-rail Notes tab: lazy-init the list. The rail is always shown when
    // the editor is in preview mode, so the user can switch to Notes even
    // when this note has no headings.
    window.NoteRailNotes?.initRail({
      workspaceIdResolver: () =>
        this.noteModalWorkspaceId || this.currentNote?.workspace_id || null,
      activeNoteId: this.currentNote?.id || null
    });
    return this.noteMount;
  },

  _ensureLive() {
    return this._ensureMount()?.live ?? null;
  },
  // Compat alias kept for any sites that still spell it _ensureLiveState.
  _ensureLiveState() {
    return this._ensureLive()?.state;
  },
  // History / autosave / toc all live in the mount bundle. Backward-compat
  // getters/setters keep call sites that read `this.note*` working.
  get noteHistory() {
    return this._ensureMount()?.history ?? null;
  },
  set noteHistory(_v) {
    /* no-op — mount owns it */
  },
  get noteAutoSave() {
    return this._ensureMount()?.autosave ?? null;
  },
  set noteAutoSave(_v) {
    /* no-op — mount owns it */
  },
  get noteToc() {
    return this._ensureMount()?.toc ?? null;
  },
  set noteToc(_v) {
    /* no-op — mount owns it */
  },

  // Load notes for a folder
  async loadFolderNotes(folderId) {
    try {
      const response = await fetch(`/api/workspaces/${folderId}/notes`);
      if (!response.ok) throw new Error('Failed to load notes');
      const data = await response.json();
      this.notesByFolder.set(folderId, data.notes || []);
      return data.notes || [];
    } catch (error) {
      console.error('Failed to load folder notes:', error);
      return [];
    }
  },

  // Create a file reference note from a dropped file
  async createFileReferenceNote(folderId, file) {
    const name = `📎 ${file.name}`;
    const sizeKB = (file.size / 1024).toFixed(1);
    const sizeStr =
      file.size > 1024 * 1024 ? `${(file.size / (1024 * 1024)).toFixed(1)} MB` : `${sizeKB} KB`;

    // Upload file to server
    let serverPath = '';
    try {
      const base64 = await this.fileToBase64(file);
      const response = await fetch('/api/files/upload', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          filename: file.name,
          content: base64,
          mime_type: file.type || 'application/octet-stream'
        })
      });
      const result = await response.json();
      if (result.error) {
        throw new Error(result.error);
      }
      serverPath = result.path;
    } catch (e) {
      console.error('Failed to upload file:', e);
      this.showToast('Failed to upload file', 'error');
      return null;
    }

    let content = `**File:** ${file.name}\n**Size:** ${sizeStr}\n**Type:** ${file.type || 'unknown'}\n**Path:** ${serverPath}`;

    const textExtensions = [
      'txt',
      'md',
      'json',
      'xml',
      'html',
      'css',
      'js',
      'ts',
      'csv',
      'yaml',
      'yml'
    ];
    const ext = file.name.split('.').pop()?.toLowerCase();

    // For text files, include readable preview
    if (textExtensions.includes(ext) && file.size < 50 * 1024) {
      // Under 50KB
      try {
        const text = await file.text();
        const preview = text.length > 2000 ? text.substring(0, 2000) + '\n...' : text;
        content += `\n\n---\n\n\`\`\`${ext}\n${preview}\n\`\`\``;
      } catch (e) {
        console.warn('Could not read file content:', e);
      }
    }

    // Store server path for later retrieval
    content += `\n\n<!-- FILE_PATH:${serverPath} -->`;

    return this.createNote(folderId, name, content);
  },

  // Convert file to base64
  fileToBase64(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        const base64 = reader.result.split(',')[1];
        resolve(base64);
      };
      reader.onerror = reject;
      reader.readAsDataURL(file);
    });
  },

  // Create a new note in a folder
  async createNote(folderId, name = 'Untitled Note', content = '') {
    try {
      const response = await fetch(`/api/workspaces/${folderId}/notes`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, content })
      });
      if (!response.ok) throw new Error('Failed to create note');
      const data = await response.json();

      // Add to local cache
      const notes = this.notesByFolder.get(folderId) || [];
      notes.unshift(data.note);
      this.notesByFolder.set(folderId, notes);

      // Refresh folder tree to show new note
      this.renderFolderTree();

      // Emit event for workspace hub to refresh
      if (window.EventBus) {
        EventBus.emit('note:created', { note: data.note, workspaceId: folderId });
      }

      return data.note;
    } catch (error) {
      console.error('Failed to create note:', error);
      this.showToast('Failed to create note', 'error');
      return null;
    }
  },

  // Update a note
  async updateNote(noteId, updates) {
    try {
      const response = await fetch(`/api/notes/${noteId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(updates)
      });
      if (!response.ok) throw new Error('Failed to update note');
      const data = await response.json();

      // Update local cache
      for (const [, notes] of this.notesByFolder) {
        const index = notes.findIndex(n => n.id === noteId);
        if (index !== -1) {
          notes[index] = { ...notes[index], ...data.note };
          break;
        }
      }

      // Emit event for workspace hub to refresh
      if (window.EventBus) {
        const workspaceId = data.note?.workspace_id || data.note?.folder_id;
        EventBus.emit('note:updated', { note: data.note, workspaceId });
      }

      return data.note;
    } catch (error) {
      console.error('Failed to update note:', error);
      this.showToast('Failed to save note', 'error');
      return null;
    }
  },

  // Delete a note
  async deleteNote(noteId) {
    try {
      const response = await fetch(`/api/notes/${noteId}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete note');

      // Remove from local cache
      for (const [, notes] of this.notesByFolder) {
        const index = notes.findIndex(n => n.id === noteId);
        if (index !== -1) {
          notes.splice(index, 1);
          break;
        }
      }

      // Refresh folder tree
      this.renderFolderTree();

      this.showToast('Note deleted', 'success');
    } catch (error) {
      console.error('Failed to delete note:', error);
      this.showToast('Failed to delete note', 'error');
    }
  },

  // Move a note to a different folder
  async moveNoteToFolder(noteId, targetFolderId) {
    try {
      const response = await fetch(`/api/notes/${noteId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ folder_id: targetFolderId })
      });
      if (!response.ok) throw new Error('Failed to move note');

      // Refresh folder data
      await this.loadFolders();
      return true;
    } catch (error) {
      console.error('Failed to move note:', error);
      this.showToast('Failed to move note', 'error');
      return false;
    }
  },

  // Copy note to clipboard
  copyNoteToClipboard(noteId, folderId) {
    this.clipboardNote = { noteId, folderId };
    this.clipboardAction = 'copy';
    this.showToast('Note copied', 'info');
  },

  // Cut note to clipboard
  cutNoteToClipboard(noteId, folderId) {
    this.clipboardNote = { noteId, folderId };
    this.clipboardAction = 'cut';
    this.showToast('Note cut', 'info');
  },

  // Paste note from clipboard to folder
  async pasteNoteToFolder(targetFolderId) {
    if (!this.clipboardNote) {
      this.showToast('Nothing to paste', 'error');
      return;
    }

    const { noteId, folderId: sourceFolderId } = this.clipboardNote;

    if (this.clipboardAction === 'cut') {
      // Move the note
      if (sourceFolderId !== targetFolderId) {
        await this.moveNoteToFolder(noteId, targetFolderId);
      }
      this.clipboardNote = null;
      this.clipboardAction = null;
    } else {
      // Copy - create a duplicate
      try {
        // Get the original note
        const note = await this.getNote(noteId);
        if (!note) throw new Error('Note not found');

        // Create a copy in the target folder
        await this.createNote(targetFolderId, `${note.name} (copy)`, note.content);
        this.showToast('Note pasted', 'success');
      } catch (error) {
        console.error('Failed to paste note:', error);
        this.showToast('Failed to paste note', 'error');
      }
    }
  },

  // Get a note by ID
  async getNote(noteId) {
    try {
      const response = await fetch(`/api/notes/${noteId}`);
      if (!response.ok) throw new Error('Failed to get note');
      return await response.json();
    } catch (error) {
      console.error('Failed to get note:', error);
      return null;
    }
  },

  // Render notes in a folder's tree item
  renderFolderNotes(folderId) {
    const notes = this.notesByFolder.get(folderId) || [];
    if (notes.length === 0) return '';

    return `
      <div class="folder-notes-section">
        ${notes
          .map(
            note => `
          <div class="folder-note-item" data-note-id="${note.id}" data-folder-id="${folderId}" draggable="true">
            <svg class="folder-note-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
              <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13"/>
            </svg>
            <span class="folder-note-name">${this.escapeHtml(note.name)}</span>
          </div>
        `
          )
          .join('')}
      </div>
    `;
  },

  // Clipboard for copy/paste
  clipboardNote: null,
  clipboardAction: null, // 'copy' or 'cut'

  // Bind note events
  bindNoteEvents() {
    // Note click to open editor (folder tree items). Routes via openNote so
    // it respects the user's notes_open_behavior preference.
    document.addEventListener('click', e => {
      const noteItem = e.target.closest('.folder-note-item');
      if (noteItem) {
        e.preventDefault();
        e.stopPropagation();
        const noteId = noteItem.dataset.noteId;
        this.openNote(noteId);
      }

      // Search result note click
      const searchNoteItem = e.target.closest('.search-note-result');
      if (searchNoteItem) {
        e.preventDefault();
        e.stopPropagation();
        const noteId = searchNoteItem.dataset.noteId;
        this.openNote(noteId);
      }
    });

    // Note context menu
    document.addEventListener('contextmenu', e => {
      const noteItem = e.target.closest('.folder-note-item');
      if (noteItem) {
        e.preventDefault();
        e.stopPropagation();
        const noteId = noteItem.dataset.noteId;
        const folderId = noteItem.dataset.folderId;
        this.showNoteContextMenu(e, noteId, folderId);
      }
    });

    // Note context menu actions
    document.querySelectorAll('#noteContextMenu .session-context-item').forEach(item => {
      item.addEventListener('click', e => {
        e.stopPropagation();
        const action = e.currentTarget.dataset.action;
        this.handleNoteContextAction(action);
      });
    });

    // Note editor save button
    document.getElementById('saveNoteBtn')?.addEventListener('click', () => this.saveCurrentNote());
    document
      .getElementById('noteCopyBtn')
      ?.addEventListener('click', () => this.copyCurrentNoteContent());
    document
      .getElementById('noteOpenInPageBtn')
      ?.addEventListener('click', () => this.openCurrentNoteAsPage());

    // Auto-save: listen for input changes on note title and content
    document
      .getElementById('noteNameInput')
      ?.addEventListener('input', () => this.scheduleNoteAutoSave());
    document.getElementById('noteContentInput')?.addEventListener('input', () => {
      this.scheduleNoteAutoSave();
      if (this.isNotePreviewMode) {
        this.refreshNotePreview();
        this._scheduleNoteTocRebuild();
      }
    });

    // Auto-save before modal close (if there are unsaved changes). The hide
    // event is cancellable; hidden.bs.modal is too late to protect edits.
    document.getElementById('noteEditorModal')?.addEventListener('hide.bs.modal', event => {
      this.handleNoteModalBeforeHide(event);
    });
    document.getElementById('noteEditorModal')?.addEventListener('hidden.bs.modal', () => {
      this.handleNoteModalHidden();
    });

    // Note workspace selector
    document.getElementById('noteWorkspaceChange')?.addEventListener('click', e => {
      e.preventDefault();
      e.stopPropagation();
      this.showNoteWorkspaceSelector();
    });

    document.getElementById('noteWorkspaceSelect')?.addEventListener('change', e => {
      const workspaceId = e.target.value;
      if (!workspaceId) {
        this.noteModalWorkspaceId = null;
        return;
      }
      this.noteModalWorkspaceId = workspaceId;
      this.showNoteWorkspaceBadge(workspaceId, true);
    });

    // Note preview toggle
    document
      .getElementById('notePreviewToggle')
      ?.addEventListener('click', () => this.toggleNotePreview());

    // Note AI generation toggle
    window.NoteEditor?.bindGenerateToggleButton?.();

    // Rail collapse toggles (TOC + AI Assist). The buttons stay hidden until
    // the corresponding feature (tasks 3.0 / 4.0) reveals them.
    document
      .getElementById('noteTocToggle')
      ?.addEventListener('click', () => this.toggleNoteTocRail());
    document
      .getElementById('noteAssistToggle')
      ?.addEventListener('click', () => this.toggleNoteAssistRail());

    // Note AI generation buttons
    document
      .getElementById('noteAIGenerateBtn')
      ?.addEventListener('click', () => this.generateNoteWithAI());
    document
      .getElementById('noteAICancelBtn')
      ?.addEventListener('click', () => this.hideNoteAIPanel());
    document
      .getElementById('noteAIReplaceBtn')
      ?.addEventListener('click', () => this.applyGeneratedNoteDraft('replace'));
    document
      .getElementById('noteAIAppendBtn')
      ?.addEventListener('click', () => this.applyGeneratedNoteDraft('append'));
    document
      .getElementById('noteAIInsertBtn')
      ?.addEventListener('click', () => this.applyGeneratedNoteDraft('insert'));

    // Note drag start
    document.addEventListener('dragstart', e => {
      const noteItem = e.target.closest('.folder-note-item');
      if (noteItem) {
        const noteId = noteItem.dataset.noteId;
        const folderId = noteItem.dataset.folderId;
        e.dataTransfer.effectAllowed = 'copyMove';
        e.dataTransfer.setData('application/x-ori-note-id', noteId);
        e.dataTransfer.setData('application/x-ori-note-folder', folderId);
        e.dataTransfer.setData('text/plain', noteId);
        noteItem.classList.add('dragging');
      }
    });

    // Note drag end
    document.addEventListener('dragend', e => {
      const noteItem = e.target.closest('.folder-note-item');
      if (noteItem) {
        noteItem.classList.remove('dragging');
        document.querySelectorAll('.folder-item').forEach(f => f.classList.remove('drag-over'));
      }
    });
  },

  // Show note context menu
  showNoteContextMenu(event, noteId, folderId) {
    this.hideContextMenus();
    this.contextMenuTarget = noteId;
    this.contextMenuFolderId = folderId;
    this.contextMenuType = 'note';

    const menu = document.getElementById('noteContextMenu');
    if (!menu) return;

    menu.style.display = 'block';
    menu.style.left = `${event.clientX}px`;
    menu.style.top = `${event.clientY}px`;

    // Ensure menu stays in viewport
    const rect = menu.getBoundingClientRect();
    if (rect.right > window.innerWidth) {
      menu.style.left = `${window.innerWidth - rect.width - 10}px`;
    }
    if (rect.bottom > window.innerHeight) {
      menu.style.top = `${window.innerHeight - rect.height - 10}px`;
    }
  },

  // Handle note context menu action
  handleNoteContextAction(action) {
    const noteId = this.contextMenuTarget;
    const folderId = this.contextMenuFolderId;
    this.hideContextMenus();

    switch (action) {
      case 'edit':
        this.openNote(noteId);
        break;
      case 'attach':
        this.attachNoteFileToChat(noteId);
        break;
      case 'rename':
        this.promptRenameNote(noteId);
        break;
      case 'copy':
        this.copyNoteToClipboard(noteId, folderId);
        break;
      case 'cut':
        this.cutNoteToClipboard(noteId, folderId);
        break;
      case 'delete':
        this.confirmDeleteNote(noteId);
        break;
    }
  },

  // Attach file from note to chat
  async attachNoteFileToChat(noteId) {
    const note = await this.getNote(noteId);
    if (!note || !note.content) {
      this.showToast('No file data found in this note', 'error');
      return;
    }

    // Try new format first: FILE_PATH (server storage)
    const filePathMatch = note.content.match(/<!-- FILE_PATH:(.+?) -->/);
    if (filePathMatch) {
      const [, serverPath] = filePathMatch;
      await this.attachFileFromServer(serverPath);
      return;
    }

    // Fall back to old format: FILE_DATA (base64 in note)
    const fileDataMatch = note.content.match(/<!-- FILE_DATA:(.+?):([^:]*):([A-Za-z0-9+/=]+) -->/);
    if (fileDataMatch) {
      const [, fileName, mimeType] = fileDataMatch;
      this.attachFileDirectly(fileName, mimeType, fileDataMatch[3]);
      return;
    }

    this.showToast(
      'No attached file found in this note. Re-drop the file to enable this feature.',
      'error'
    );
  },

  // Attach file from server storage
  async attachFileFromServer(serverPath) {
    try {
      const response = await fetch(`/api/files/content?path=${encodeURIComponent(serverPath)}`);
      const result = await response.json();

      if (result.error) {
        this.showToast('Failed to load file: ' + result.error, 'error');
        return;
      }

      if (typeof window.addFileToUpload === 'function') {
        const success = window.addFileToUpload({
          name: result.filename,
          type: result.mime_type || 'application/octet-stream',
          size: Math.round(result.content.length * 0.75),
          content: result.content
        });
        if (success) {
          this.showToast(`Attached "${result.filename}" to chat`, 'success');
        } else {
          this.showToast('Chat input not available on this page', 'error');
        }
      } else {
        this.showToast('Chat not available', 'error');
      }
    } catch (e) {
      console.error('Failed to fetch file:', e);
      this.showToast('Failed to load file', 'error');
    }
  },

  // Attach file directly from base64 content (legacy format)
  attachFileDirectly(fileName, mimeType, base64Content) {
    if (typeof window.addFileToUpload === 'function') {
      const success = window.addFileToUpload({
        name: fileName,
        type: mimeType || 'application/octet-stream',
        size: Math.round(base64Content.length * 0.75),
        content: base64Content
      });
      if (success) {
        this.showToast(`Attached "${fileName}" to chat`, 'success');
      } else {
        this.showToast('Chat input not available on this page', 'error');
      }
    } else {
      this.showToast('Chat not available', 'error');
    }
  },

  // Open note editor modal for a new note
  openNoteCreateModal(workspaceId = null) {
    // Reset auto-save state when opening a new note
    this.resetNoteAutoSaveState();

    this.currentNote = null;
    this.noteModalWorkspaceId = workspaceId;
    this.isNotePreviewMode = false;
    this.noteAIGeneratedDraft = null;
    // New note has no ID yet — keep the backlinks section hidden.
    window.NoteBacklinks?.clearBacklinks();

    const modal = document.getElementById('noteEditorModal');
    if (!modal) return;

    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const lastSaved = document.getElementById('noteLastSaved');
    const saveBtn = document.getElementById('saveNoteBtn');

    if (nameInput) nameInput.value = '';
    if (contentInput) {
      contentInput.value = '';
    }
    this.resetNoteHistory();
    this.setNotePreviewMode(true);
    if (lastSaved) lastSaved.textContent = '';
    if (saveBtn) {
      saveBtn.textContent = 'Create note';
      saveBtn.hidden = false;
      saveBtn.disabled = false;
    }
    this.hideNoteVaultReferenceBadge();
    this._applyNoteRailState();
    this._initNoteAIAssist();
    this._setNoteAIAgentDefault(workspaceId).then(() => {
      window.NoteAIAssist?.onNoteOpened({
        noteId: null,
        workspaceId: workspaceId || null,
        agentId: this._resolveWorkspaceAgentId()
      });
    });

    if (workspaceId) {
      this.showNoteWorkspaceBadge(workspaceId, true);
    } else {
      this.showNoteWorkspaceSelector();
    }

    // Reset AI panel state
    this.hideNoteAIPanel();
    this.loadNoteAIAgents();

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();

    setTimeout(() => {
      nameInput?.focus();
    }, 100);
  },

  showNoteWorkspaceBadge(workspaceId, allowChange = false) {
    const badge = document.getElementById('noteWorkspaceBadge');
    const nameSpan = document.getElementById('noteWorkspaceName');
    const selector = document.getElementById('noteWorkspaceSelector');
    const changeBtn = document.getElementById('noteWorkspaceChange');

    if (selector) selector.style.display = 'none';
    if (!badge || !nameSpan) return;

    if (!workspaceId) {
      badge.style.display = 'none';
      return;
    }

    const folder = this.findFolderById(workspaceId, this.folders);
    nameSpan.textContent = folder?.name || 'Unknown Workspace';
    badge.style.display = 'block';
    if (changeBtn) changeBtn.style.display = allowChange ? 'inline-flex' : 'none';
  },

  normalizeNoteVaultReference(ref) {
    return window.NoteEditor?.normalizeVaultReference(ref) ?? null;
  },
  showNoteVaultReferenceBadge(ref) {
    window.NoteEditor?.showVaultReferenceBadge(ref);
  },
  hideNoteVaultReferenceBadge() {
    window.NoteEditor?.hideVaultReferenceBadge();
  },

  showNoteWorkspaceSelector() {
    const badge = document.getElementById('noteWorkspaceBadge');
    const selector = document.getElementById('noteWorkspaceSelector');
    if (badge) badge.style.display = 'none';
    if (selector) selector.style.display = 'block';
    this.populateNoteWorkspaceDropdown();
  },

  populateNoteWorkspaceDropdown() {
    const select = document.getElementById('noteWorkspaceSelect');
    if (!select) return;

    let options = '<option value="">-- Select a workspace --</option>';
    const appendOptions = (folders, depth = 0) => {
      (folders || []).forEach(folder => {
        if (!folder || !folder.id) return;
        if (String(folder.kind || '').trim() !== 'group') {
          const indent = depth > 0 ? `${'-- '.repeat(depth)}` : '';
          const label = `${indent}${folder.name || 'Unnamed Workspace'}`;
          const selected = folder.id === this.noteModalWorkspaceId ? ' selected' : '';
          options += `<option value="${this.escapeHtml(folder.id)}"${selected}>${this.escapeHtml(label)}</option>`;
        }
        if (folder.children && folder.children.length > 0) {
          appendOptions(folder.children, depth + 1);
        }
      });
    };

    appendOptions(this.folders);
    select.innerHTML = options;
  },

  // Open note editor modal with a note object
  // If note doesn't have full content, fetches it first
  async openNoteEditorModal(note) {
    if (!note || !note.id) return;

    // If note only has preview, fetch full content
    let fullNote = note;
    if (note.preview !== undefined && note.content === undefined) {
      fullNote = await this.getNote(note.id);
      if (!fullNote) return;
    }

    return this._openNoteEditorWithNote(fullNote);
  },

  // Open note editor modal by ID
  // Opens a note in live-preview mode and scrolls to a specific heading by
  // text. Used by the global search palette (⌘K) when the user picks a
  // heading result. Falls back to plain openNoteEditor if the heading can't
  // be located in the loaded source.
  async openNoteEditorWithHeading(noteId, headingText) {
    await this.openNoteEditor(noteId);
    if (!this.isNotePreviewMode) this.setNotePreviewMode(true);
    const source = this.getNoteContentValue();
    const lines = source.split('\n');
    let cursor = 0;
    for (const line of lines) {
      if (/^#{1,6}\s+/.test(line) && line.includes(headingText)) {
        // Wait one frame for live-preview render to settle, then scroll.
        requestAnimationFrame(() => this._scrollNoteToHeading(cursor));
        return;
      }
      cursor += line.length + 1;
    }
  },

  async openNoteEditor(noteId) {
    // Cross-tab presence: if this note is already open in another tab (page
    // surface), let the user decide whether to open a duplicate copy here.
    // The check is fast (~150ms timeout) and degrades gracefully when
    // BroadcastChannel is unsupported.
    try {
      const elsewhere = await window.NotePresence?.isOpenElsewhere?.(noteId);
      if (elsewhere?.open && elsewhere.surface === 'page') {
        const proceed = window.confirm(
          'This note is already open in another browser tab. Open it here too?'
        );
        if (!proceed) return;
      }
    } catch (_) {
      /* non-fatal */
    }

    const note = await this.getNote(noteId);
    if (!note) return;

    return this._openNoteEditorWithNote(note);
  },

  _notePageURL(note, hash = '') {
    const noteId = note?.id || '';
    if (!noteId) return '';
    const path = `/notes/${encodeURIComponent(noteId)}`;
    if (!hash) return path;
    const value = String(hash);
    if (!value || value === '#') return path;
    return value.startsWith('#') ? `${path}${value}` : `${path}#${encodeURIComponent(value)}`;
  },

  // openNote consults the user's `notes_open_behavior` preference and routes
  // the click to either the modal (default), the focused note page, or a new
  // browser tab. Centralized so every entry point routes through the same
  // logic.
  //
  // Pass `{ force: 'modal'|'page'|'page-new-tab' }` to override the preference
  // for affordances like "Open in page" / "Open in modal" buttons.
  async openNote(noteId, options = {}) {
    if (!noteId) return;
    const behavior = options.force || this._readNotesOpenBehavior();
    if (behavior === 'page') {
      const note = await this.getNote(noteId);
      const url = this._notePageURL(note);
      if (!url) {
        this.showToast('Could not open this note as a page', 'error');
        return;
      }
      window.location.href = url;
      return;
    }
    if (behavior === 'page-new-tab') {
      const note = await this.getNote(noteId);
      const url = this._notePageURL(note);
      if (!url) {
        this.showToast('Could not open this note as a page', 'error');
        return;
      }
      window.open(url, '_blank', 'noopener');
      return;
    }
    return this.openNoteEditor(noteId);
  },

  // _readNotesOpenBehavior pulls from the localStorage mirror written by the
  // Settings page. Falls back to "modal" when unset or invalid.
  _readNotesOpenBehavior() {
    try {
      const v = localStorage.getItem('note.openBehavior');
      if (v === 'modal' || v === 'page' || v === 'page-new-tab') return v;
    } catch (_) {}
    return 'modal';
  },

  // Internal: Open note editor with a full note object
  _openNoteEditorWithNote(note) {
    // Reset auto-save state when opening a note
    this.resetNoteAutoSaveState();

    this.currentNote = note;
    this.noteModalWorkspaceId = note.workspace_id || note.folder_id || null;
    this.isNotePreviewMode = false;
    this.noteAIGeneratedDraft = null;

    const modal = document.getElementById('noteEditorModal');
    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const lastSaved = document.getElementById('noteLastSaved');
    const saveBtn = document.getElementById('saveNoteBtn');

    if (nameInput) nameInput.value = note.name;
    if (contentInput) {
      contentInput.value = note.content || '';
    }
    this.resetNoteHistory();
    this._applyNoteRailState();
    this._initNoteAIAssist();
    this._setNoteAIAgentDefault(this.noteModalWorkspaceId).then(() => {
      window.NoteAIAssist?.onNoteOpened({
        noteId: note.id,
        workspaceId: this.noteModalWorkspaceId,
        agentId: this._resolveWorkspaceAgentId()
      });
    });
    // Backlinks: notes referencing this one via [[wikilinks]]. Hidden when none.
    window.NoteBacklinks?.loadBacklinksFor(note.id);
    // Left-rail Notes tab: keep the active highlight in sync.
    window.NoteRailNotes?.setActiveNoteId(note.id);
    this.setNotePreviewMode(true);
    if (lastSaved) {
      lastSaved.textContent = `Last saved: ${this.formatDateTime(note.updated_at)}`;
    }
    if (saveBtn) {
      saveBtn.textContent = 'Save now';
      saveBtn.hidden = true;
      saveBtn.disabled = false;
    }

    if (this.noteModalWorkspaceId) {
      this.showNoteWorkspaceBadge(this.noteModalWorkspaceId, false);
    } else {
      this.showNoteWorkspaceSelector();
    }
    this.showNoteVaultReferenceBadge(note.vault_reference);

    // Reset AI panel state
    this.hideNoteAIPanel();
    this.loadNoteAIAgents();

    const bsModal = new bootstrap.Modal(modal);
    // Re-render once the modal finishes animating in, then verify the
    // preview actually painted. If it ended up empty for any reason (a
    // render-path bug, a CSS regression, an empty note), fall back to
    // showing the textarea so the user can at least see + edit the
    // Markdown source.
    const onShown = () => {
      modal.removeEventListener('shown.bs.modal', onShown);
      this.renderNoteLiveEditor();
      requestAnimationFrame(() => this._verifyNotePreviewVisible());
    };
    modal.addEventListener('shown.bs.modal', onShown);
    bsModal.show();
  },

  // _verifyNotePreviewVisible inspects the preview pane after the modal is
  // shown. If the textarea has content but the preview rendered nothing (an
  // unexpected state), exit preview mode so the textarea becomes visible —
  // the user gets a working editor instead of staring at a blank dark area.
  _verifyNotePreviewVisible() {
    const contentInput = document.getElementById('noteContentInput');
    const previewContent = document.getElementById('notePreviewContent');
    if (!contentInput || !previewContent) return;
    const hasContent = (contentInput.value || '').trim().length > 0;
    const previewEmpty = !previewContent.innerHTML || previewContent.innerHTML.trim() === '';
    if (hasContent && previewEmpty) {
      // Diagnostic info — share this output to narrow down the render-path bug.
      const bundle = this.noteMount;
      console.warn('[note-modal] preview empty; diag:', {
        noteEditorLoaded: !!window.NoteEditor,
        bundleExists: !!bundle,
        bundleKeys: bundle ? Object.keys(bundle) : null,
        bundleLiveType: bundle ? typeof bundle.live : 'no-bundle',
        bundleRenderType: bundle ? typeof bundle.render : 'no-bundle',
        isPreviewMode: this.isNotePreviewMode,
        textareaLen: (contentInput.value || '').length,
        previewDisplay: previewContent.style.display,
        previewInnerHTMLLen: previewContent.innerHTML.length
      });
      this.setNotePreviewMode(false);
    }
  },

  // Create new note for folder (called from folder context menu)
  createNewNoteForFolder(folderId) {
    this.openNoteCreateModal(folderId);
  },

  async writeTextToClipboard(text) {
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
  },

  async copyCurrentNoteContent() {
    const contentInput = document.getElementById('noteContentInput');
    const noteContent = String(contentInput?.value || '');
    if (!noteContent.trim()) {
      this.showToast('Note is empty', 'info');
      return;
    }

    try {
      await this.writeTextToClipboard(noteContent);
      this.showToast('Note content copied', 'success');
    } catch (error) {
      console.error('Failed to copy note content:', error);
      this.showToast('Failed to copy note', 'error');
    }
  },

  // Navigate to the workspace notes page for the currently-open note. If the
  // note hasn't been saved yet (no id), flush autosave first so the URL has
  // something to resolve. Saves the modal's pending edits before navigating so
  // they aren't lost.
  async openCurrentNoteAsPage() {
    // Trigger immediate save so unsaved edits land before navigation.
    const saved = await this.noteAutoSave?.flushImmediate?.();
    if (saved === false) {
      this.showToast('Save failed. Retry before opening this note as a page.', 'error');
      return;
    }

    if (!this.currentNote?.id) {
      this.showToast('Save the note first to get a shareable URL', 'warning');
      return;
    }
    const url = this._notePageURL(this.currentNote);
    if (!url) {
      this.showToast('Could not open this note as a page', 'error');
      return;
    }
    window.location.href = url;
  },

  // Save current note
  async saveCurrentNote() {
    // Cancel any pending auto-save (we're saving now).
    this.noteAutoSave?.cancel();

    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const noteName = nameInput?.value?.trim() || 'Untitled Note';
    const noteContent = contentInput?.value || '';

    this.updateNoteSaveStatus('saving');

    if (!this.currentNote) {
      const workspaceId = this.noteModalWorkspaceId;
      if (!workspaceId) {
        this.showToast('Please select a workspace first', 'warning');
        this.showNoteWorkspaceSelector();
        this.updateNoteSaveStatus('unsaved');
        return;
      }

      const created = await this.createNote(workspaceId, noteName, noteContent);
      if (created) {
        this.currentNote = created;
        this.noteAutoSave?.markClean();
        this.showToast('Note created', 'success');
        this.showNoteWorkspaceBadge(workspaceId, false);
        window.NoteBacklinks?.announceNoteSaved?.(created.id, noteContent.includes('[['));
        const lastSaved = document.getElementById('noteLastSaved');
        if (lastSaved)
          lastSaved.textContent = `Last saved: ${this.formatDateTime(created.updated_at || created.created_at)}`;
      } else {
        this.updateNoteSaveStatus('error');
      }
      return;
    }

    const updates = {
      name: noteName,
      content: noteContent
    };

    const updated = await this.updateNote(this.currentNote.id, updates);
    if (updated) {
      this.currentNote = { ...this.currentNote, ...updated };
      this.noteAutoSave?.markClean();
      this.showToast('Note saved', 'success');
      const lastSaved = document.getElementById('noteLastSaved');
      if (lastSaved)
        lastSaved.textContent = `Last saved: ${this.formatDateTime(updated.updated_at || this.currentNote.updated_at)}`;

      // Refresh folder tree to show updated note name
      this.renderFolderTree();
    } else {
      this.updateNoteSaveStatus('error');
    }
  },

  // =============================================================================
  // Note Auto-Save
  // =============================================================================

  // Schedule auto-save with debounce
  _ensureNoteAutoSave() {
    return this._ensureMount()?.autosave ?? null;
  },

  scheduleNoteAutoSave() {
    this._ensureNoteAutoSave()?.schedule();
  },

  // Performs one autosave attempt. Called by the timer or by handleNoteModalClose.
  async autoSaveNote() {
    const timer = this._ensureNoteAutoSave();
    if (!timer || !timer.isDirty()) return true;

    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const noteName = nameInput?.value?.trim() || 'Untitled Note';
    const noteContent = contentInput?.value || '';

    try {
      if (this.currentNote?.id) {
        const updates = { name: noteName, content: noteContent };
        const updated = await this.updateNote(this.currentNote.id, updates);
        if (updated) {
          this.currentNote = { ...this.currentNote, ...updated };
          this.renderFolderTree();
          // Notify other tabs so any open Backlinks panel can re-fetch.
          window.NoteBacklinks?.announceNoteSaved?.(
            this.currentNote.id,
            noteContent.includes('[[')
          );
          return true;
        } else {
          return false;
        }
      } else {
        const workspaceId = this.noteModalWorkspaceId;
        if (!workspaceId) {
          // Can't auto-create without workspace; revert to unsaved state.
          window.NoteEditor.updateSaveStatus('unsaved');
          return false;
        }
        const created = await this.createNote(workspaceId, noteName, noteContent);
        if (created) {
          this.currentNote = created;
          const saveBtn = document.getElementById('saveNoteBtn');
          if (saveBtn) saveBtn.textContent = 'Save now';
          window.NoteBacklinks?.announceNoteSaved?.(created.id, noteContent.includes('[['));
          return true;
        } else {
          return false;
        }
      }
    } catch (error) {
      console.error('Auto-save failed:', error);
      return false;
    }
  },

  syncNoteSaveNowButton(status) {
    const saveBtn = document.getElementById('saveNoteBtn');
    if (!saveBtn) return;
    const isCreate = !this.currentNote?.id;
    saveBtn.textContent = isCreate ? 'Create note' : status === 'error' ? 'Retry save' : 'Save now';
    saveBtn.hidden =
      !isCreate && !(status === 'unsaved' || status === 'error' || status === 'saving');
    saveBtn.disabled = status === 'saving';
  },

  updateNoteSaveStatus(status) {
    window.NoteEditor?.updateSaveStatus(status);
    this.syncNoteSaveNowButton(status);
  },

  handleNoteModalBeforeHide(event) {
    const timer = this.noteAutoSave;
    if (this.noteModalAllowHide || !timer?.isDirty?.()) {
      this.noteModalAllowHide = false;
      return;
    }

    event.preventDefault();
    if (this.noteModalHideInProgress) return;
    this.noteModalHideInProgress = true;

    timer
      .flushImmediate()
      .then(saved => {
        if (saved === false) {
          this.showToast('Save failed. Retry save before closing this note.', 'error');
          this.updateNoteSaveStatus('error');
          return;
        }
        this.noteModalAllowHide = true;
        const modal = bootstrap.Modal.getInstance(document.getElementById('noteEditorModal'));
        modal?.hide();
      })
      .catch(error => {
        console.error('Auto-save before modal close failed:', error);
        this.showToast('Save failed. Retry save before closing this note.', 'error');
        this.updateNoteSaveStatus('error');
      })
      .finally(() => {
        this.noteModalHideInProgress = false;
      });
  },

  handleNoteModalHidden() {
    this.noteModalAllowHide = false;
    this.noteModalHideInProgress = false;
  },

  resetNoteAutoSaveState() {
    this._ensureNoteAutoSave()?.reset();
  },

  // =============================================================================
  // Note Editor Rails (TOC + AI Assist)
  // =============================================================================
  // Both rails are hidden by default. Tasks 3.0 (TOC) and 4.0 (AI Assist) call
  // showNoteTocRail() / showNoteAssistRail() once they have content to display.
  // The collapse toggle is per-rail and persisted in localStorage.

  _applyNoteRailState() {
    window.NoteEditor?.applyAllRailState();
  },
  _applyNoteRailCollapsed(rail) {
    window.NoteEditor?.applyRailCollapsed(rail);
  },
  toggleNoteTocRail() {
    window.NoteEditor?.toggleRail('toc');
  },
  toggleNoteAssistRail() {
    window.NoteEditor?.toggleRail('assist');
  },
  showNoteTocRail() {
    window.NoteEditor?.showRail('toc');
  },
  hideNoteTocRail() {
    window.NoteEditor?.hideRail('toc');
  },
  showNoteAssistRail() {
    window.NoteEditor?.showRail('assist');
  },
  hideNoteAssistRail() {
    window.NoteEditor?.hideRail('assist');
  },

  // =============================================================================
  // Note AI Assist (selection action bar + sidebar wiring)
  // =============================================================================

  // AI Assist is now wired through the mount() call (see _ensureMount).
  // _initNoteAIAssist stays as a no-op for any historical callers; the
  // first time `_ensureMount` runs, NoteAIAssist is hooked up.
  _initNoteAIAssist() {
    this._ensureMount();
  },

  async _setNoteAIAgentDefault(workspaceId) {
    return window.NoteEditor?.applyAgentDefaultForWorkspace(workspaceId);
  },

  _wireNoteAIAgentChange() {
    window.NoteEditor?.wireAgentChangeHandler();
  },
  _resolveWorkspaceAgentId() {
    return window.NoteEditor?.getSelectedAgentId() ?? null;
  },
  _wireNoteSelectionTracking() {
    window.NoteEditor?.wireSelectionTracking({
      onChange: () => window.NoteAIAssist?.onSelectionChanged(this._readNoteSelection())
    });
  },

  // Reads the current text selection in the note editor (modal or page). The
  // modal-show check is local because the page surface won't have it.
  _readNoteSelection() {
    const modal = document.getElementById('noteEditorModal');
    if (!modal || !modal.classList.contains('show')) return null;
    return (
      window.NoteEditor?.readSelection({
        getContent: () => this.getNoteContentValue(),
        isPreviewMode: () => this.isNotePreviewMode
      }) ?? null
    );
  },

  // =============================================================================
  // Note TOC (live-preview only)
  // =============================================================================
  // Builds an outline from the rendered Markdown headings via NoteTOC.buildOutline,
  // renders it into the left rail, syncs the active-section indicator on scroll,
  // and supports drag-reorder of sections in the underlying Markdown source.

  // Debounced wrapper called from the input listener — TOC rebuild is cheap
  // but we still avoid running on every keystroke.
  _ensureNoteToc() {
    return this._ensureMount()?.toc ?? null;
  },

  _scheduleNoteTocRebuild() {
    this._ensureNoteToc()?.scheduleRebuild();
  },
  _renderNoteTocOutline() {
    this._ensureNoteToc()?.rebuild();
  },
  _scrollNoteToHeading(position) {
    window.NoteEditor?.scrollToHeadingPosition(this.getNoteContentValue(), position);
  },
  _findRenderedHeadingByPosition(position) {
    return (
      window.NoteEditor?.findRenderedHeadingByPosition(this.getNoteContentValue(), position) ?? null
    );
  },
  _attachNoteTocActiveObserver() {
    this._ensureNoteToc()?.attachObserver();
  },
  _teardownNoteTocActiveObserver() {
    this.noteToc?.detachObserver();
  },
  _setActiveTocEntry(lineIndex) {
    window.NoteEditor?.setActiveTocEntry(lineIndex, this.getNoteContentValue());
  },

  // =============================================================================
  // Note AI Generation
  // =============================================================================

  _openGeneratePanelByDefault() {
    window.NoteEditor?.openGeneratePanelByDefault();
  },
  toggleNoteAIPanel() {
    window.NoteEditor?.toggleGeneratePanel();
  },
  hideNoteAIPanel() {
    this.noteAIGeneratedDraft = null;
    window.NoteEditor?.closeGeneratePanel();
  },

  // Load agents for AI generation dropdown. Pass the current note's workspace
  // so the dropdown filters to that workspace's bound agents instead of every
  // agent in the system.
  async loadNoteAIAgents() {
    const workspaceId =
      this.noteModalWorkspaceId ||
      this.currentNote?.workspace_id ||
      this.currentNote?.folder_id ||
      '';
    return window.NoteEditor?.loadAgentsIntoDropdown(workspaceId);
  },

  // Submit the unified Ask AI panel. When the panel has a selection attached
  // (set by openGeneratePanel(selection) — e.g. via the inline Ask AI… button
  // or Cmd+J), the prompt is dispatched as a suggestion card through
  // NoteAIAssist. Otherwise it falls back to the cold-start /api/notes/generate
  // flow that produces a whole-note draft.
  async generateNoteWithAI() {
    const promptInput = document.getElementById('noteAIPromptInput');
    const prompt = promptInput?.value?.trim() || '';
    if (!prompt) {
      window.NoteEditor?.setGenerateError?.('Please enter a prompt.');
      return;
    }

    const panelSelection = window.NoteEditor?.getPanelSelection?.();
    if (panelSelection && panelSelection.text) {
      const ok = window.NoteAIAssist?.dispatchAsk?.(panelSelection, prompt);
      if (!ok) {
        window.NoteEditor?.setGenerateError?.(
          'Could not dispatch — select a workspace agent first.'
        );
        return;
      }
      window.NoteEditor?.closeGeneratePanel?.();
      return;
    }

    const agentSelect = document.getElementById('noteAIAgentSelect');
    const agentId = agentSelect?.value || '';
    const workspaceId =
      this.noteModalWorkspaceId ||
      this.currentNote?.workspace_id ||
      this.currentNote?.folder_id ||
      '';

    window.NoteEditor?.setGenerateError?.('');
    window.NoteEditor?.setGenerateStatus?.('');
    window.NoteEditor?.clearGenerateDraft?.();
    window.NoteEditor?.setGenerateBusy?.(true);

    try {
      const response = await fetch('/api/notes/generate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          prompt: prompt,
          workspace_id: workspaceId,
          agent_id: agentId
        })
      });

      if (!response.ok) {
        let payload = null;
        try {
          payload = await response.json();
        } catch (_) {}
        throw new Error(payload?.error || 'Failed to generate note');
      }

      const result = await response.json();
      this.noteAIGeneratedDraft = {
        title: result.title || '',
        content: result.content || ''
      };
      window.NoteEditor?.setGenerateDraft?.(this.noteAIGeneratedDraft);
      this.showToast('AI draft generated', 'success');
    } catch (error) {
      console.error('Note AI generation failed:', error);
      window.NoteEditor?.setGenerateError?.(
        error.message || 'Failed to generate note content. Please try again.'
      );
    } finally {
      window.NoteEditor?.setGenerateBusy?.(false);
    }
  },

  applyGeneratedNoteDraft(mode = 'replace') {
    const draft = this.noteAIGeneratedDraft;
    if (!draft?.content) {
      window.NoteEditor?.setGenerateError?.('Generate a draft before applying it.');
      return;
    }

    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    if (!contentInput) return;

    const currentContent = contentInput.value || '';
    const draftContent = String(draft.content || '').trim();
    const draftTitle = String(draft.title || '').trim();
    let nextContent = draftContent;

    this.pushNoteUndoState();

    if (mode === 'append') {
      nextContent = currentContent.trim()
        ? `${currentContent.replace(/\s+$/, '')}\n\n${draftContent}`
        : draftContent;
    } else if (mode === 'insert') {
      const start = Number.isInteger(contentInput.selectionStart)
        ? contentInput.selectionStart
        : currentContent.length;
      const end = Number.isInteger(contentInput.selectionEnd) ? contentInput.selectionEnd : start;
      nextContent = `${currentContent.slice(0, start)}${draftContent}${currentContent.slice(end)}`;
      const cursor = start + draftContent.length;
      const restoreCursor = () => {
        try {
          contentInput.focus();
          contentInput.setSelectionRange(cursor, cursor);
        } catch (_) {}
      };
      if (typeof requestAnimationFrame === 'function') requestAnimationFrame(restoreCursor);
      else restoreCursor();
    } else if (draftTitle && nameInput) {
      nameInput.value = draftTitle;
    }

    if (mode !== 'replace' && draftTitle && nameInput && !nameInput.value.trim()) {
      nameInput.value = draftTitle;
    }
    contentInput.value = nextContent;

    if (this.isNotePreviewMode) {
      this.setNotePreviewMode(true);
    }
    this.scheduleNoteAutoSave();
    this._scheduleNoteTocRebuild();
    this.noteAIGeneratedDraft = null;
    this.hideNoteAIPanel();
    this.showToast('AI draft applied', 'success');
  },

  // Expand @notename references in a message with note content
  // Returns the message with note references expanded
  async expandNoteReferences(message) {
    // Get current session's folder
    const session = this.getActiveSession();
    if (!session?.folder_id) {
      return message; // No folder, return as-is
    }

    // Get notes for this folder
    const notes = this.notesByFolder.get(session.folder_id) || [];
    if (notes.length === 0) {
      return message; // No notes, return as-is
    }

    // Find @notename patterns (matches @word or @"phrase with spaces")
    const noteRefPattern = /@(\w+)|@"([^"]+)"/g;
    let expandedMessage = message;
    let match;

    while ((match = noteRefPattern.exec(message)) !== null) {
      const noteName = match[1] || match[2]; // Either simple word or quoted phrase
      const fullMatch = match[0];

      // Find note by name (case-insensitive)
      const note = notes.find(n => n.name.toLowerCase() === noteName.toLowerCase());

      if (note) {
        // Fetch full note content if we only have preview
        let noteContent = note.content;
        if (!noteContent) {
          try {
            const response = await fetch(`/api/notes/${note.id}`);
            if (response.ok) {
              const fullNote = await response.json();
              noteContent = fullNote.content || '';
            }
          } catch (e) {
            console.error('Failed to fetch note content:', e);
          }
        }

        if (noteContent) {
          // Replace the reference with the note content (formatted for clarity)
          expandedMessage = expandedMessage.replace(
            fullMatch,
            `\n\n---\n📝 **Note: ${note.name}**\n\n${noteContent}\n\n---\n\n`
          );
        }
      }
    }

    return expandedMessage;
  },

  // Set preview mode for the note editor
  setNotePreviewMode(enabled) {
    this.isNotePreviewMode = Boolean(enabled);
    const editorContainer = document.querySelector('.note-editor-container');
    const contentInput = document.getElementById('noteContentInput');
    const previewContent = document.getElementById('notePreviewContent');
    const previewToggle = document.getElementById('notePreviewToggle');

    if (this.isNotePreviewMode) {
      editorContainer?.classList.add('is-previewing');
      if (contentInput) contentInput.style.display = 'none';
      if (previewContent) {
        previewContent.style.display = 'block';
        this.renderNoteLiveEditor();
      }
      if (previewToggle) {
        previewToggle.classList.add('active');
        previewToggle.setAttribute('aria-pressed', 'true');
        previewToggle.title = 'Use plain Markdown editor';
      }
      this._renderNoteTocOutline();
    } else {
      // Optional chaining — _ensureLiveState() can be undefined if the
      // mount bundle never initialized (which is exactly what _verify…
      // calls into us for as a fallback).
      this._ensureLiveState()?.reset();
      editorContainer?.classList.remove('is-previewing');
      if (contentInput) contentInput.style.display = 'block';
      if (previewContent) {
        previewContent.style.display = 'none';
        previewContent.innerHTML = '';
        previewContent.onmousedown = null;
        previewContent.onmouseup = null;
        previewContent.onclick = null;
        previewContent.onkeydown = null;
        previewContent.oninput = null;
        previewContent.onchange = null;
        previewContent.onpaste = null;
        previewContent.oncut = null;
        previewContent.onfocusout = null;
      }
      if (previewToggle) {
        previewToggle.classList.remove('active');
        previewToggle.setAttribute('aria-pressed', 'false');
        previewToggle.title = 'Show live preview';
      }
      this.hideNoteTocRail();
      this._teardownNoteTocActiveObserver();
    }
  },

  resetNoteHistory() {
    this._ensureMount()?.history?.reset();
    this.noteLiveCollapsedHeadings = new Set();
  },

  getNoteContentValue() {
    return window.NoteEditor?.getContentValue() ?? '';
  },
  setNoteContentValue(value) {
    window.NoteEditor?.setContentValue(value);
  },

  pushNoteUndoState() {
    if (!this.noteHistory) this.resetNoteHistory();
    if (!this.noteHistory) return;
    this.noteHistory.push(this.getNoteContentValue());
  },

  applyNoteHistoryState(value, options = {}) {
    if (this.noteHistory) this.noteHistory.applying = true;
    this.setNoteContentValue(value);
    this._ensureLiveState().reset();
    this.clearNoteLiveSelection();
    this.scheduleNoteAutoSave();
    if (this.noteHistory) this.noteHistory.applying = false;

    if (this.isNotePreviewMode) {
      this.renderNoteLiveEditor();
      const lines = this.getNoteContentLines();
      const focusIndex = Math.max(0, Math.min(options.focusLineIndex ?? 0, lines.length - 1));
      const focusLine = document.querySelector(
        `.note-live-line-rendered[data-line-index="${focusIndex}"]`
      );
      focusLine?.focus({ preventScroll: true });
    }
  },

  undoNoteEdit() {
    if (!this.noteHistory) return false;
    const previous = this.noteHistory.undo(this.getNoteContentValue());
    if (previous == null) return false;
    this.applyNoteHistoryState(previous);
    return true;
  },

  redoNoteEdit() {
    if (!this.noteHistory) return false;
    const next = this.noteHistory.redo(this.getNoteContentValue());
    if (next == null) return false;
    this.applyNoteHistoryState(next);
    return true;
  },

  isNoteUndoShortcut(event) {
    return window.NoteEditor?.isUndoShortcut(event) ?? false;
  },
  isNoteRedoShortcut(event) {
    return window.NoteEditor?.isRedoShortcut(event) ?? false;
  },

  handleNoteHistoryShortcut(event) {
    if (this.isNoteUndoShortcut(event)) {
      if (this.undoNoteEdit()) {
        event.preventDefault();
        return true;
      }
      return false;
    }

    if (this.isNoteRedoShortcut(event)) {
      if (this.redoNoteEdit()) {
        event.preventDefault();
        return true;
      }
      return false;
    }

    return false;
  },

  refreshNotePreview() {
    this.renderNoteLiveEditor();
  },

  getNoteContentLines() {
    return window.NoteEditor?.getContentLines() ?? [''];
  },
  setNoteContentLines(lines) {
    window.NoteEditor?.setContentLines(lines);
  },

  // The following four helpers were moved to note-editor.js as the first slice
  // of the v2 task 1.0 extraction. The thin delegators stay here so existing
  // callers (this.noteHeadingLevel(...), etc.) keep working untouched.
  noteHeadingLevel(line) {
    return window.NoteEditor?.parseHeadingLevel(line) ?? 0;
  },
  noteLineKindClass(line) {
    return window.NoteEditor?.lineKindClass(line) ?? '';
  },
  parseNoteTaskLine(line) {
    return window.NoteEditor?.parseTaskLine(line) ?? null;
  },
  normalizeCompactTaskListMarkdown(text) {
    return window.NoteEditor?.normalizeCompactTaskListMarkdown(text) ?? String(text || '');
  },

  pruneNoteCollapsedHeadings(lines) {
    window.NoteEditor?.pruneCollapsedHeadings(lines, this.noteLiveCollapsedHeadings);
  },

  renderNoteLiveEditor(options = {}) {
    this._ensureMount()?.render(options);
  },

  renderNoteLiveInputLine(line, index) {
    return window.NoteEditor?.renderEditingLine(line, index) ?? '';
  },
  renderNoteLiveRangeInput(markdown, startIndex, endIndex) {
    return window.NoteEditor?.renderEditingRange(markdown, startIndex, endIndex) ?? '';
  },

  renderNoteLiveRenderedLine(line, index) {
    const collapsed = this.noteLiveCollapsedHeadings.has(index);
    return window.NoteEditor?.renderRenderedLine(line, index, collapsed) ?? '';
  },
  renderNoteHeadingLine(line, index) {
    const collapsed = this.noteLiveCollapsedHeadings.has(index);
    return window.NoteEditor?.renderHeadingLine(line, index, collapsed) ?? '';
  },
  renderNoteTaskLine(line, index) {
    return window.NoteEditor?.renderTaskLine(line, index) ?? '';
  },

  bindNoteLiveEditorEvents(previewContent) {
    this._ensureLive()?.bindEvents(previewContent);
  },

  noteLiveSelectionContains(container, node) {
    return window.NoteEditor?.selectionContains(container, node) ?? false;
  },
  hasNoteLiveTextSelection(container) {
    return window.NoteEditor?.hasTextSelectionInside(container) ?? false;
  },
  didNoteLivePointerDrag(event) {
    return window.NoteEditor?.pointerDragged(this.noteLivePointerDown, event) ?? false;
  },

  clearNoteLiveSelection() {
    window.NoteEditor?.clearWindowSelection();
    this.noteLiveSelectionFocusIndex = null;
  },

  selectNoteLiveLineRange(anchorIndex, focusIndex) {
    this._ensureLive()?.selectLineRange(anchorIndex, focusIndex);
  },

  getNoteLiveSelectedLineRange(container) {
    return window.NoteEditor?.getSelectedLineRange(container) ?? null;
  },

  isNoteLivePrintableKey(event) {
    return window.NoteEditor?.isPrintableKey(event) ?? false;
  },

  toggleNoteHeadingFold(lineIndex) {
    this._ensureLive()?.toggleHeadingFold(lineIndex);
    // Restore focus to the fold button so keyboard users keep their place.
    document
      .querySelector(`.note-heading-fold[data-line-index="${lineIndex}"]`)
      ?.focus({ preventScroll: true });
  },

  toggleNoteTaskLine(lineIndex, checked) {
    this._ensureLive()?.toggleTaskLine(lineIndex, checked);
    document
      .querySelector(`.note-task-checkbox[data-line-index="${lineIndex}"]`)
      ?.focus({ preventScroll: true });
  },

  deleteNoteLiveLineRange(range) {
    this._ensureLive()?.deleteRange(range);
  },
  replaceNoteLiveLineRange(range, replacement) {
    this._ensureLive()?.replaceRange(range, replacement);
  },
  editNoteLiveLineRange(range) {
    this._ensureLive()?.editRange(range);
  },
  activateNoteLiveLine(lineIndex, cursorPosition = null) {
    this._ensureLive()?.activate(lineIndex, cursorPosition);
  },

  handleNoteLiveInputChange(input) {
    this._ensureLive()?.handleInputChange(input);
  },
  handleNoteLiveRangeInputChange(input) {
    this._ensureLive()?.handleRangeInputChange(input);
  },
  handleNoteLiveRangeInputKeydown(event, input) {
    this._ensureLive()?.handleRangeInputKeydown(event, input);
  },
  handleNoteLiveInputKeydown(event, input) {
    this._ensureLive()?.handleInputKeydown(event, input);
  },
  resizeNoteLiveInput(input) {
    window.NoteEditor?.resizeLiveInput(input);
  },

  renderMarkdownLine(line) {
    return window.NoteEditor?.renderMarkdownLine(line) ?? '';
  },
  renderInlineMarkdown(text) {
    return window.NoteEditor?.renderInlineMarkdown(text) ?? '';
  },

  // Toggle preview mode
  toggleNotePreview() {
    this.setNotePreviewMode(!this.isNotePreviewMode);
  },

  renderMarkdown(text) {
    return window.NoteEditor?.renderMarkdown(text) ?? '';
  },

  // Prompt to rename note
  async promptRenameNote(noteId) {
    // Find the note
    let note = null;
    for (const [, notes] of this.notesByFolder) {
      note = notes.find(n => n.id === noteId);
      if (note) break;
    }
    if (!note) return;

    const newName = prompt('Enter new note name:', note.name);
    if (newName && newName.trim() && newName !== note.name) {
      await this.updateNote(noteId, { name: newName.trim() });
      this.renderFolderTree();
    }
  },

  // Confirm delete note
  async confirmDeleteNote(noteId) {
    if (confirm('Are you sure you want to delete this note?')) {
      await this.deleteNote(noteId);
    }
  },

  // =============================================================================
  // Workspace Tasks
  // =============================================================================

  // Task state
  workspaceTasks: [],
  taskCounts: { total: 0, pending: 0, completed: 0 },
  tasksByWorkspace: new Map(), // Tasks keyed by workspace (folder) ID
  scheduledTasksByWorkspace: new Map(), // Scheduled tasks keyed by workspace (folder) ID
  currentScheduledTaskWorkspaceId: null, // Workspace context for scheduled task operations

  // Load tasks for current session (from workspace/orchestration)
  async loadSessionTasks() {
    if (!this.activeSessionId) {
      this.workspaceTasks = [];
      this.taskCounts = { total: 0, pending: 0, completed: 0 };
      return;
    }

    try {
      // Get the workspace ID from the active session
      const activeSession = this.sessions.find(s => s.id === this.activeSessionId);
      if (!activeSession || !activeSession.folder_id) {
        this.workspaceTasks = [];
        this.taskCounts = { total: 0, pending: 0, completed: 0 };
        return;
      }

      const workspaceId = activeSession.folder_id;
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
      if (!response.ok) throw new Error('Failed to load tasks');

      const data = await response.json();
      this.workspaceTasks = data.tasks || [];
      this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };

      // Update the counts cache and badges
      this.sessionTaskCounts.set(this.activeSessionId, this.taskCounts);
      this.updateSessionTaskBadges();
      this.updateChatTaskBadge();

      // Update main panel if open
      if (this.mainTaskPanelOpen) {
        this.renderMainTaskPanel();
      }

      // Update workspace tasks in sidebar
      this.tasksByWorkspace.set(workspaceId, this.workspaceTasks);
      this.renderFolderTree();
    } catch (error) {
      console.error('Failed to load session tasks:', error);
      this.workspaceTasks = [];
      this.taskCounts = { total: 0, pending: 0, completed: 0 };
      this.updateChatTaskBadge();
    }
  },

  // Load tasks for a workspace (folder) using orchestration API
  async loadWorkspaceTasks(workspaceId) {
    try {
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
      if (!response.ok) throw new Error('Failed to load workspace tasks');
      const data = await response.json();
      this.tasksByWorkspace.set(workspaceId, data.tasks || []);
      return data.tasks || [];
    } catch (error) {
      console.error('Failed to load workspace tasks:', error);
      return [];
    }
  },

  // Load tasks for all workspaces recursively
  async loadAllWorkspaceTasks(folders) {
    for (const folder of folders) {
      await this.loadWorkspaceTasks(folder.id);
      if (folder.children && folder.children.length > 0) {
        await this.loadAllWorkspaceTasks(folder.children);
      }
    }
  },

  // Load scheduled tasks for a workspace (folder) using orchestration API
  // Now returns tasks that have schedules (not legacy ScheduledTask entities)
  async loadWorkspaceScheduledTasks(workspaceId) {
    try {
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
      if (!response.ok) throw new Error('Failed to load workspace tasks');
      const data = await response.json();
      const allTasks = data.tasks || [];
      // Filter tasks that have schedules - normalize to consistent format
      const scheduledTasks = allTasks
        .filter(task => task.schedule != null)
        .map(task => ({
          id: task.id,
          name: task.schedule_name || task.description,
          description: task.description,
          enabled: task.schedule_enabled,
          schedule: task.schedule,
          next_run: task.next_run,
          last_run: task.last_run,
          execution_count: task.execution_count,
          failure_count: task.failure_count,
          execution_history: task.execution_history,
          // Keep reference to original task
          task_id: task.id,
          from: task.from,
          to: task.to
        }));
      this.scheduledTasksByWorkspace.set(workspaceId, scheduledTasks);
      return scheduledTasks;
    } catch (error) {
      console.error('Failed to load workspace scheduled tasks:', error);
      return [];
    }
  },

  // Load scheduled tasks for all workspaces recursively
  async loadAllWorkspaceScheduledTasks(folders) {
    for (const folder of folders) {
      await this.loadWorkspaceScheduledTasks(folder.id);
      if (folder.children && folder.children.length > 0) {
        await this.loadAllWorkspaceScheduledTasks(folder.children);
      }
    }
  },

  // Add task to workspace from sidebar using orchestration API
  async addWorkspaceTask(workspaceId) {
    // Prompt for task description
    const description = prompt('Enter task description:');
    if (!description || !description.trim()) return;

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: workspaceId,
          description: description.trim(),
          priority: 3
        })
      });

      if (!response.ok) throw new Error('Failed to create task');

      // Reload workspace tasks
      await this.loadWorkspaceTasks(workspaceId);
      this.renderFolderTree();
      this.showToast('Task created', 'success');

      // Also reload session tasks if active session is in this workspace
      const activeSession = this.sessions.find(s => s.id === this.activeSessionId);
      if (activeSession && activeSession.folder_id === workspaceId) {
        await this.loadSessionTasks();
      }
    } catch (error) {
      console.error('Failed to create task:', error);
      this.showToast('Failed to create task', 'error');
    }
  },

  // Toggle task completion using orchestration API
  async toggleTaskComplete(taskId) {
    const task = this.workspaceTasks.find(t => t.id === taskId);
    if (!task) return;

    try {
      if (task.status === 'completed') {
        // Uncomplete - update status back to pending
        const response = await fetch('/api/orchestration/tasks', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ task_id: taskId, status: 'pending' })
        });
        if (!response.ok) throw new Error('Failed to update task');
      } else {
        // Complete using the complete endpoint
        const response = await fetch(`/api/orchestration/tasks/${taskId}/complete`, {
          method: 'POST'
        });
        if (!response.ok) throw new Error('Failed to complete task');
      }

      // Reload tasks - use workspace ID if no active session
      if (this.activeSessionId) {
        await this.loadSessionTasks();
      } else if (this.currentTaskWorkspaceId) {
        const tasksResponse = await fetch(
          `/api/orchestration/tasks?workspace_id=${this.currentTaskWorkspaceId}`
        );
        if (tasksResponse.ok) {
          const data = await tasksResponse.json();
          this.workspaceTasks = data.tasks || [];
          this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };
        }
        this.renderMainTaskPanel();
      }
    } catch (error) {
      console.error('Failed to toggle task:', error);
      this.showToast('Failed to update task', 'error');
    }
  },

  // Delete task using orchestration API
  async deleteTask(taskId) {
    if (!confirm('Delete this task?')) return;

    try {
      const response = await fetch(`/api/orchestration/tasks?id=${taskId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete task');

      await this.loadSessionTasks();
      this.showToast('Task deleted', 'success');
    } catch (error) {
      console.error('Failed to delete task:', error);
      this.showToast('Failed to delete task', 'error');
    }
  },

  // ===== Main Task Panel (Chat Area) =====

  mainTaskPanelOpen: false,

  // Initialize main task panel events
  initMainTaskPanel() {
    // Tasks button in chat header
    document
      .getElementById('chatTasksBtn')
      ?.addEventListener('click', () => this.toggleMainTaskPanel());

    // Close button
    document
      .getElementById('mainTaskPanelClose')
      ?.addEventListener('click', () => this.closeMainTaskPanel());

    // Backdrop click to close
    document
      .getElementById('mainTaskModalBackdrop')
      ?.addEventListener('click', () => this.closeMainTaskPanel());

    // Add task button - opens modal
    document
      .getElementById('mainTaskAddBtn')
      ?.addEventListener('click', () => this.openTaskModal());

    // Input enter key - opens modal with prefilled title
    document.getElementById('mainTaskInput')?.addEventListener('keydown', e => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const input = document.getElementById('mainTaskInput');
        const title = input?.value?.trim() || '';
        this.openTaskModal(null, title);
        if (input) input.value = '';
      }
    });

    // Shared task modal controller owns these bindings when present.
    const hasSharedTaskController = Boolean(
      window.taskModalController && typeof window.taskModalController.openForCreate === 'function'
    );
    if (!hasSharedTaskController) {
      // Task modal event handlers
      document
        .getElementById('taskModalClose')
        ?.addEventListener('click', () => this.closeTaskModal());
      document
        .getElementById('taskModalCancel')
        ?.addEventListener('click', () => this.closeTaskModal());
      document
        .getElementById('taskModalSave')
        ?.addEventListener('click', () => this.saveTaskFromModal());
      document
        .querySelector('.task-modal-backdrop')
        ?.addEventListener('click', () => this.closeTaskModal());

      // Schedule fields toggle
      document.getElementById('taskModalScheduleEnabled')?.addEventListener('change', e => {
        const scheduleFields = document.getElementById('taskModalScheduleFields');
        if (scheduleFields) {
          scheduleFields.style.display = e.target.checked ? 'block' : 'none';
        }
      });

      // Schedule type change handler
      document.getElementById('taskModalScheduleType')?.addEventListener('change', () => {
        this.updateTaskModalScheduleTypeFields();
      });

      // Modal escape key for task modal
      document.getElementById('taskModal')?.addEventListener('keydown', e => {
        if (e.key === 'Escape') {
          this.closeTaskModal();
        }
      });
    }

    // Escape key for main task modal
    document.getElementById('mainTaskModal')?.addEventListener('keydown', e => {
      if (e.key === 'Escape') {
        this.closeMainTaskPanel();
      }
    });
  },

  // Toggle main task panel
  toggleMainTaskPanel() {
    if (this.mainTaskPanelOpen) {
      this.closeMainTaskPanel();
    } else {
      this.openMainTaskPanel();
    }
  },

  // Open main task modal
  openMainTaskPanel() {
    const modal = document.getElementById('mainTaskModal');
    // Allow opening if we have either an active session or a workspace ID (from sidebar click)
    if (!modal || (!this.activeSessionId && !this.currentTaskWorkspaceId)) return;

    modal.style.display = 'flex';
    this.mainTaskPanelOpen = true;

    // Update button state
    document.getElementById('chatTasksBtn')?.classList.add('active');

    // Render tasks
    this.renderMainTaskPanel();

    // Focus input
    setTimeout(() => {
      document.getElementById('mainTaskInput')?.focus();
    }, 100);

    // Prevent body scrolling when modal is open
    document.body.style.overflow = 'hidden';
  },

  // Close main task modal
  closeMainTaskPanel() {
    const modal = document.getElementById('mainTaskModal');
    if (!modal) return;

    modal.style.display = 'none';
    this.mainTaskPanelOpen = false;

    // Update button state
    document.getElementById('chatTasksBtn')?.classList.remove('active');

    // Restore body scrolling
    document.body.style.overflow = '';
  },

  // Open workspace task panel from sidebar
  // With unified task system, tasks are stored in workspaces (not sessions)
  // so we can view tasks without needing a session
  async openWorkspaceTaskPanel(workspaceId) {
    try {
      // Load tasks directly from workspace using orchestration API
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
      if (!response.ok) throw new Error('Failed to load workspace tasks');

      const data = await response.json();
      this.workspaceTasks = data.tasks || [];
      this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };

      // Store workspace ID for task operations
      this.currentTaskWorkspaceId = workspaceId;

      // Open main task panel (works without active session)
      this.openMainTaskPanel();
    } catch (error) {
      console.error('Failed to open workspace tasks:', error);
      this.showToast('Failed to open workspace tasks', 'error');
    }
  },

  // =============================================================================
  // Scheduled Tasks Panel Functions
  // =============================================================================

  // Open scheduled tasks panel for a workspace
  async openScheduledTasksPanel(workspaceId) {
    try {
      // Load tasks with schedules (not legacy ScheduledTask entities)
      const scheduledTasks = await this.loadWorkspaceScheduledTasks(workspaceId);

      this.currentScheduledTaskWorkspaceId = workspaceId;
      this.showScheduledTasksModal(scheduledTasks, workspaceId);
    } catch (error) {
      console.error('Failed to open scheduled tasks:', error);
      this.showToast('Failed to load scheduled tasks', 'error');
    }
  },

  // Show scheduled tasks modal
  showScheduledTasksModal(scheduledTasks, workspaceId) {
    const modal = document.getElementById('scheduledTasksModal');
    if (!modal) return;

    const folder = this.findFolderById(workspaceId, this.folders);
    const workspaceName = folder ? folder.name : 'Workspace';

    // Set title
    const titleEl = document.getElementById('scheduledTasksModalTitle');
    if (titleEl) {
      titleEl.innerHTML = `
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
        </svg>
        Schedules - ${this.escapeHtml(workspaceName)}
      `;
    }

    // Render list
    this.renderScheduledTasksList(scheduledTasks);

    // Show modal
    modal.style.display = 'flex';
    document.body.style.overflow = 'hidden';
  },

  // Render scheduled tasks list
  renderScheduledTasksList(scheduledTasks) {
    const listContainer = document.getElementById('scheduledTasksList');
    const emptyState = document.getElementById('scheduledTasksEmpty');

    if (!listContainer) return;

    listContainer.innerHTML = '';

    if (scheduledTasks.length === 0) {
      if (emptyState) emptyState.style.display = 'flex';
      return;
    }

    if (emptyState) emptyState.style.display = 'none';

    scheduledTasks.forEach(st => {
      const itemEl = document.createElement('div');
      itemEl.className = 'scheduled-task-item';
      itemEl.dataset.scheduleId = st.id;

      const scheduleDesc = this.getScheduleDescription(st.schedule);
      const nextRun = st.next_run ? new Date(st.next_run).toLocaleString() : 'Not scheduled';
      const statusClass = st.enabled ? 'enabled' : 'disabled';

      itemEl.innerHTML = `
        <div class="scheduled-task-status-indicator ${statusClass}"></div>
        <div class="scheduled-task-content">
          <div class="scheduled-task-name">${this.escapeHtml(st.name)}</div>
          <div class="scheduled-task-desc">${this.escapeHtml(st.description || '')}</div>
          <div class="scheduled-task-meta">
            <span class="scheduled-task-schedule">${scheduleDesc}</span>
            ${st.enabled ? `<span class="scheduled-task-next">Next: ${nextRun}</span>` : ''}
          </div>
        </div>
        <div class="scheduled-task-actions">
          <button class="scheduled-task-toggle" title="${st.enabled ? 'Disable' : 'Enable'}">
            ${st.enabled ? '⏸' : '▶'}
          </button>
          <button class="scheduled-task-trigger" title="Trigger Now" ${!st.enabled ? 'disabled' : ''}>
            ⚡
          </button>
          <button class="scheduled-task-delete" title="Delete Schedule">
            🗑
          </button>
        </div>
      `;

      // Bind click for details
      itemEl.addEventListener('click', e => {
        if (!e.target.closest('button')) {
          this.showScheduledTaskDetails(st);
        }
      });

      // Toggle button
      itemEl.querySelector('.scheduled-task-toggle')?.addEventListener('click', e => {
        e.stopPropagation();
        this.toggleScheduledTaskEnabled(st.id, !st.enabled);
      });

      // Trigger button
      itemEl.querySelector('.scheduled-task-trigger')?.addEventListener('click', e => {
        e.stopPropagation();
        this.triggerScheduledTask(st.id);
      });

      // Delete button
      itemEl.querySelector('.scheduled-task-delete')?.addEventListener('click', e => {
        e.stopPropagation();
        this.deleteScheduledTask(st.id);
      });

      listContainer.appendChild(itemEl);
    });
  },

  // Get human-readable schedule description
  getScheduleDescription(schedule) {
    if (!schedule) return 'No schedule';

    switch (schedule.type) {
      case 'daily':
        return `Daily at ${schedule.time_of_day || '00:00'}`;
      case 'weekly': {
        const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
        return `Weekly on ${days[schedule.day_of_week || 0]} at ${schedule.time_of_day || '00:00'}`;
      }
      case 'interval': {
        // Interval is in nanoseconds
        const totalSeconds = Math.floor((schedule.interval || 0) / 1000000000);
        const hours = Math.floor(totalSeconds / 3600);
        const minutes = Math.floor((totalSeconds % 3600) / 60);
        if (hours > 0) return `Every ${hours} hour${hours > 1 ? 's' : ''}`;
        return `Every ${minutes} minute${minutes > 1 ? 's' : ''}`;
      }
      case 'once':
        return schedule.execute_at
          ? `Once at ${new Date(schedule.execute_at).toLocaleString()}`
          : 'Once';
      case 'cron':
        return `Cron: ${schedule.cron_expr || 'custom'}`;
      default:
        return 'Custom schedule';
    }
  },

  // Show scheduled task details modal
  showScheduledTaskDetails(st) {
    const modal = document.getElementById('scheduledTaskDetailModal');
    if (!modal) return;

    // Populate details
    document.getElementById('scheduleDetailName').textContent = st.name;
    document.getElementById('scheduleDetailDesc').textContent = st.description || 'No description';

    const statusEl = document.getElementById('scheduleDetailStatus');
    statusEl.textContent = st.enabled ? 'Enabled' : 'Disabled';
    statusEl.className =
      'schedule-detail-badge ' + (st.enabled ? 'badge-enabled' : 'badge-disabled');

    document.getElementById('scheduleDetailSchedule').textContent = this.getScheduleDescription(
      st.schedule
    );
    document.getElementById('scheduleDetailNextRun').textContent = st.next_run
      ? new Date(st.next_run).toLocaleString()
      : 'Not scheduled';
    document.getElementById('scheduleDetailLastRun').textContent = st.last_run
      ? new Date(st.last_run).toLocaleString()
      : 'Never';
    document.getElementById('scheduleDetailExecutions').textContent =
      `${st.execution_count || 0} total, ${st.failure_count || 0} failures`;

    // Render execution history
    this.renderExecutionHistory(st.execution_history || []);

    // Store current schedule ID for actions
    modal.dataset.scheduleId = st.id;
    modal.dataset.enabled = st.enabled;

    // Update toggle button text
    const toggleBtn = document.getElementById('scheduleDetailToggleBtn');
    if (toggleBtn) toggleBtn.textContent = st.enabled ? 'Disable' : 'Enable';

    modal.style.display = 'flex';
  },

  // Render execution history
  renderExecutionHistory(history) {
    const container = document.getElementById('scheduleDetailHistory');
    if (!container) return;

    if (!history || history.length === 0) {
      container.innerHTML = '<div class="history-empty">No execution history</div>';
      return;
    }

    // Show last 10 executions (reversed to show most recent first)
    const recentHistory = history.slice().reverse().slice(0, 10);

    container.innerHTML = recentHistory
      .map(
        exec => `
      <div class="history-item ${exec.status}">
        <span class="history-time">${new Date(exec.executed_at).toLocaleString()}</span>
        <span class="history-status">${exec.status === 'success' ? '✓' : '✗'}</span>
        ${exec.error ? `<span class="history-error" title="${this.escapeHtml(exec.error)}">${this.escapeHtml(exec.error.substring(0, 30))}...</span>` : ''}
      </div>
    `
      )
      .join('');
  },

  // Toggle scheduled task enabled/disabled
  // Now updates the task's schedule_enabled field
  async toggleScheduledTaskEnabled(taskId, enable) {
    const action = enable ? 'enable' : 'disable';
    try {
      const response = await fetch(`/api/orchestration/tasks/${taskId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: taskId,
          schedule_enabled: enable
        })
      });

      if (!response.ok) throw new Error(`Failed to ${action} task schedule`);

      // Reload and refresh
      if (this.currentScheduledTaskWorkspaceId) {
        await this.loadWorkspaceScheduledTasks(this.currentScheduledTaskWorkspaceId);
        const tasks =
          this.scheduledTasksByWorkspace.get(this.currentScheduledTaskWorkspaceId) || [];
        this.renderScheduledTasksList(tasks);
        this.renderFolderTree();
      }

      this.showToast(`Schedule ${enable ? 'enabled' : 'disabled'}`, 'success');
    } catch (error) {
      console.error(`Failed to ${action} task schedule:`, error);
      this.showToast(`Failed to ${action} schedule`, 'error');
    }
  },

  // Trigger scheduled task manually (execute the task now)
  async triggerScheduledTask(taskId) {
    if (!confirm('Execute this scheduled task now?')) return;

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: taskId })
      });

      if (!response.ok) throw new Error('Failed to trigger scheduled task');

      const data = await response.json();
      this.showToast(`Task triggered! ID: ${data.task_id?.substring(0, 8)}...`, 'success');
    } catch (error) {
      console.error('Failed to trigger scheduled task:', error);
      this.showToast('Failed to trigger schedule', 'error');
    }
  },

  // Delete/clear schedule from a task
  async deleteScheduledTask(taskId) {
    if (
      !confirm(
        'Are you sure you want to delete this schedule? The task itself will remain but will no longer run on a schedule.'
      )
    )
      return;

    try {
      const response = await fetch(`/api/orchestration/tasks/${taskId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: taskId,
          schedule_enabled: false,
          schedule: null
        })
      });

      if (!response.ok) throw new Error('Failed to delete schedule');

      // Close the detail modal
      this.closeScheduledTaskDetailModal();

      // Reload and refresh the scheduled tasks list
      if (this.currentScheduledTaskWorkspaceId) {
        await this.loadWorkspaceScheduledTasks(this.currentScheduledTaskWorkspaceId);
        const tasks =
          this.scheduledTasksByWorkspace.get(this.currentScheduledTaskWorkspaceId) || [];
        this.renderScheduledTasksList(tasks);
        this.renderFolderTree();
      }

      this.showToast('Schedule deleted', 'success');
    } catch (error) {
      console.error('Failed to delete schedule:', error);
      this.showToast('Failed to delete schedule', 'error');
    }
  },

  // Close scheduled tasks modal
  closeScheduledTasksModal() {
    const modal = document.getElementById('scheduledTasksModal');
    if (modal) {
      modal.style.display = 'none';
      document.body.style.overflow = '';
    }
  },

  // Close scheduled task detail modal
  closeScheduledTaskDetailModal() {
    const modal = document.getElementById('scheduledTaskDetailModal');
    if (modal) modal.style.display = 'none';
  },

  // Initialize scheduled tasks modal events
  initScheduledTasksModal() {
    // Close buttons
    document
      .getElementById('scheduledTasksModalClose')
      ?.addEventListener('click', () => this.closeScheduledTasksModal());
    document
      .getElementById('scheduledTasksModalBackdrop')
      ?.addEventListener('click', () => this.closeScheduledTasksModal());

    // Detail modal close
    document
      .getElementById('scheduleDetailClose')
      ?.addEventListener('click', () => this.closeScheduledTaskDetailModal());
    document
      .getElementById('scheduleDetailBackdrop')
      ?.addEventListener('click', () => this.closeScheduledTaskDetailModal());

    // Detail modal actions
    document.getElementById('scheduleDetailToggleBtn')?.addEventListener('click', () => {
      const modal = document.getElementById('scheduledTaskDetailModal');
      if (modal) {
        const scheduleId = modal.dataset.scheduleId;
        const currentlyEnabled = modal.dataset.enabled === 'true';
        this.toggleScheduledTaskEnabled(scheduleId, !currentlyEnabled);
        this.closeScheduledTaskDetailModal();
      }
    });

    document.getElementById('scheduleDetailTriggerBtn')?.addEventListener('click', () => {
      const modal = document.getElementById('scheduledTaskDetailModal');
      if (modal) {
        const scheduleId = modal.dataset.scheduleId;
        this.triggerScheduledTask(scheduleId);
      }
    });

    document.getElementById('scheduleDetailDeleteBtn')?.addEventListener('click', () => {
      const modal = document.getElementById('scheduledTaskDetailModal');
      if (modal) {
        const scheduleId = modal.dataset.scheduleId;
        this.deleteScheduledTask(scheduleId);
      }
    });

    // Escape key handlers (document-level for reliable closing)
    document.addEventListener('keydown', e => {
      if (e.key === 'Escape') {
        const detailModal = document.getElementById('scheduledTaskDetailModal');
        const listModal = document.getElementById('scheduledTasksModal');
        // Close detail modal first if visible, then list modal
        if (
          detailModal &&
          detailModal.style.display !== 'none' &&
          detailModal.style.display !== ''
        ) {
          this.closeScheduledTaskDetailModal();
        } else if (
          listModal &&
          listModal.style.display !== 'none' &&
          listModal.style.display !== ''
        ) {
          this.closeScheduledTasksModal();
        }
      }
    });
  },

  // Submit task from main panel using orchestration API
  async submitMainTask() {
    const input = document.getElementById('mainTaskInput');
    const description = input?.value?.trim();
    if (!description) return;

    try {
      // Get workspace ID from active session or stored workspace context
      let workspaceId = this.currentTaskWorkspaceId;
      if (!workspaceId && this.activeSessionId) {
        const activeSession = this.sessions.find(s => s.id === this.activeSessionId);
        workspaceId = activeSession?.folder_id;
      }

      if (!workspaceId) {
        this.showToast('No workspace context available', 'error');
        return;
      }

      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: workspaceId,
          description,
          priority: 3
        })
      });

      if (!response.ok) throw new Error('Failed to create task');

      input.value = '';

      // Reload tasks for the workspace
      const tasksResponse = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
      if (tasksResponse.ok) {
        const data = await tasksResponse.json();
        this.workspaceTasks = data.tasks || [];
        this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };
      }

      this.renderMainTaskPanel();
      this.showToast('Task created', 'success');
    } catch (error) {
      console.error('Failed to create task:', error);
      this.showToast('Failed to create task', 'error');
    }
  },

  // Render main task panel
  renderMainTaskPanel() {
    const listContainer = document.getElementById('mainTaskList');
    const emptyState = document.getElementById('mainTaskEmpty');
    const chatBadge = document.getElementById('chatTasksBadge');

    if (!listContainer) return;

    // Update chat button badge
    if (chatBadge) {
      if (this.taskCounts.total === 0) {
        chatBadge.classList.add('d-none');
      } else {
        chatBadge.classList.remove('d-none');
        if (this.taskCounts.pending > 0) {
          chatBadge.textContent = this.taskCounts.pending;
          chatBadge.classList.remove('complete');
        } else {
          chatBadge.textContent = '✓';
          chatBadge.classList.add('complete');
        }
      }
    }

    // Clear existing items
    listContainer.innerHTML = '';

    if (this.workspaceTasks.length === 0) {
      if (emptyState) emptyState.style.display = 'flex';
      return;
    }

    if (emptyState) emptyState.style.display = 'none';

    // Sort: pending first, then by creation date
    const sortedTasks = [...this.workspaceTasks].sort((a, b) => {
      const aCompleted = a.status === 'completed' ? 1 : 0;
      const bCompleted = b.status === 'completed' ? 1 : 0;
      if (aCompleted !== bCompleted) return aCompleted - bCompleted;
      return new Date(b.created_at) - new Date(a.created_at);
    });

    sortedTasks.forEach(task => {
      const isCompleted = task.status === 'completed';
      const isWorkspaceTask = !task.session_id && task.workspace_id;
      const createdAt = this.formatTimeAgo(task.created_at);

      const taskEl = document.createElement('div');
      taskEl.className = `main-task-item ${isCompleted ? 'completed' : ''}`;
      taskEl.dataset.taskId = task.id;
      const hasDetails = task.details && task.details.trim().length > 0;
      taskEl.innerHTML = `
        <input type="checkbox" class="main-task-checkbox" ${isCompleted ? 'checked' : ''}>
        <div class="main-task-content">
          <div class="main-task-description">${this.escapeHtml(task.description)}</div>
          ${hasDetails ? `<div class="main-task-details">${this.escapeHtml(task.details)}</div>` : ''}
          <div class="main-task-meta">
            ${isWorkspaceTask ? '<span class="main-task-workspace-badge">Workspace</span>' : ''}
            <span>${createdAt}</span>
          </div>
        </div>
        <button class="main-task-edit" title="Edit task">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
          </svg>
        </button>
        <button class="main-task-execute" title="Execute task">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M8,5.14V19.14L19,12.14L8,5.14Z"/>
          </svg>
        </button>
        <button class="main-task-delete" title="Delete task">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
      `;

      // Checkbox change
      taskEl.querySelector('.main-task-checkbox')?.addEventListener('change', () => {
        this.toggleTaskComplete(task.id);
      });

      // Edit click
      taskEl.querySelector('.main-task-edit')?.addEventListener('click', e => {
        e.stopPropagation();
        this.openTaskModal(task.id);
      });

      // Execute click
      taskEl.querySelector('.main-task-execute')?.addEventListener('click', e => {
        e.stopPropagation();
        this.executeTask(task.id);
      });

      // Delete click
      taskEl.querySelector('.main-task-delete')?.addEventListener('click', e => {
        e.stopPropagation();
        this.deleteMainTask(task.id);
      });

      listContainer.appendChild(taskEl);
    });
  },

  // Delete task from main panel (with confirmation) using orchestration API
  async deleteMainTask(taskId) {
    if (!confirm('Delete this task?')) return;

    try {
      const response = await fetch(`/api/orchestration/tasks?id=${taskId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete task');

      // Reload tasks - use workspace ID if no active session
      if (this.activeSessionId) {
        await this.loadSessionTasks();
      } else if (this.currentTaskWorkspaceId) {
        const tasksResponse = await fetch(
          `/api/orchestration/tasks?workspace_id=${this.currentTaskWorkspaceId}`
        );
        if (tasksResponse.ok) {
          const data = await tasksResponse.json();
          this.workspaceTasks = data.tasks || [];
          this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };
        }
      }
      this.renderMainTaskPanel();
      this.showToast('Task deleted', 'success');
    } catch (error) {
      console.error('Failed to delete task:', error);
      this.showToast('Failed to delete task', 'error');
    }
  },

  // Execute task - send to chat as a prompt using orchestration API
  async executeTask(taskId) {
    const task = this.workspaceTasks.find(t => t.id === taskId);
    if (!task) return;

    try {
      // Mark task as in_progress using orchestration API
      await fetch('/api/orchestration/tasks', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: taskId, status: 'in_progress' })
      });

      // Close task panel
      this.closeMainTaskPanel();

      // Build prompt from description and details
      let prompt = task.description;
      if (task.details && task.details.trim()) {
        prompt += '\n\n' + task.details.trim();
      }

      // Send task to chat
      if (window.sendMessageToChat) {
        window.sendMessageToChat(prompt);
      }

      // Reload tasks to reflect status change
      await this.loadSessionTasks();
    } catch (error) {
      console.error('Failed to execute task:', error);
      this.showToast('Failed to execute task', 'error');
    }
  },

  // Track which task is being edited (null for new task)
  editingTaskId: null,
  taskModalWorkspaceId: null, // For workspace-level tasks

  // Open task modal for creating or editing
  openTaskModal(taskId = null, prefillTitle = '') {
    // Clear workspace context when opening for session task
    this.taskModalWorkspaceId = null;
    this.editingTaskId = taskId;

    // Get workspace ID from active session or stored workspace context (from sidebar click)
    let workspaceId = this.currentTaskWorkspaceId;
    if (!workspaceId && this.activeSessionId) {
      const session = this.sessions.find(s => s.id === this.activeSessionId);
      workspaceId = session?.folder_id;
    }

    // Helper to reload tasks after modal save
    const reloadTasks = async () => {
      if (this.activeSessionId) {
        await this.loadSessionTasks();
      } else if (workspaceId) {
        const tasksResponse = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
        if (tasksResponse.ok) {
          const data = await tasksResponse.json();
          this.workspaceTasks = data.tasks || [];
          this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };
        }
      }
      this.renderMainTaskPanel();
    };

    // Use shared task modal controller if available
    if (window.taskModalController && workspaceId) {
      if (taskId) {
        // Editing existing task
        const task = this.workspaceTasks.find(t => t.id === taskId);
        if (task) {
          window.taskModalController.openForEdit(task, reloadTasks);
        }
      } else {
        // Creating new task
        window.taskModalController.openForCreate(workspaceId, prefillTitle, reloadTasks);
      }
      return;
    }

    // Fallback to legacy implementation
    const modal = document.getElementById('taskModal');
    if (!modal) return;

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      modalTitle.textContent = taskId ? 'Edit Task' : 'Add Task';
    }

    // Get form elements
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');

    if (taskId) {
      // Editing existing task - populate fields
      const task = this.workspaceTasks.find(t => t.id === taskId);
      if (task) {
        if (descriptionInput) descriptionInput.value = task.description || '';
        if (detailsInput) detailsInput.value = task.details || '';
        // Populate schedule fields if task has schedule
        this.populateTaskModalScheduleFields(task);
      } else {
        this.resetTaskModalScheduleFields();
      }
    } else {
      // New task - clear or prefill
      if (descriptionInput) descriptionInput.value = prefillTitle;
      if (detailsInput) detailsInput.value = '';
      // Reset schedule fields for new task
      this.resetTaskModalScheduleFields();
    }

    // Show modal
    modal.style.display = 'flex';

    // Focus title input
    setTimeout(() => {
      descriptionInput?.focus();
    }, 100);
  },

  // Close task modal
  closeTaskModal() {
    const modal = document.getElementById('taskModal');
    if (modal) {
      modal.style.display = 'none';
    }
    this.editingTaskId = null;
    this.taskModalWorkspaceId = null;
  },

  // Update schedule type fields visibility based on selected schedule type
  updateTaskModalScheduleTypeFields() {
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
  },

  // Reset schedule fields in task modal
  resetTaskModalScheduleFields() {
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
    if (scheduleDay) scheduleDay.value = 'monday';
    if (scheduleIntervalValue) scheduleIntervalValue.value = '1';
    if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
    if (scheduleDatetime) scheduleDatetime.value = '';

    this.updateTaskModalScheduleTypeFields();
  },

  // Populate schedule fields from existing task
  populateTaskModalScheduleFields(task) {
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
      if (schedule.day_of_week && scheduleDay) scheduleDay.value = schedule.day_of_week;
      if (schedule.interval_minutes) {
        // Convert minutes to value + unit
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
        // Convert ISO datetime to local datetime-local format
        const date = new Date(schedule.run_at);
        scheduleDatetime.value = date.toISOString().slice(0, 16);
      }

      this.updateTaskModalScheduleTypeFields();
    } else {
      this.resetTaskModalScheduleFields();
    }
  },

  // Get schedule data from modal fields
  getScheduleDataFromModal() {
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
        // Combine value and unit into interval_minutes
        const intervalValue = parseInt(
          document.getElementById('taskModalScheduleIntervalValue')?.value || '1',
          10
        );
        const intervalUnit =
          document.getElementById('taskModalScheduleIntervalUnit')?.value || 'hours';
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
  },

  // Open task modal for workspace-level task
  openTaskModalForWorkspace(workspaceId) {
    this.editingTaskId = null;
    this.taskModalWorkspaceId = workspaceId;

    // Use shared task modal controller if available
    if (window.taskModalController) {
      window.taskModalController.openForCreate(workspaceId, '', async () => {
        await this.loadFolders();
        this.renderFolders();
      });
      return;
    }

    // Fallback to legacy implementation
    const modal = document.getElementById('taskModal');
    if (!modal) return;

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      const folder = this.folders.find(f => f.id === workspaceId);
      const workspaceName = folder ? folder.name : 'Workspace';
      modalTitle.textContent = `Add Task to ${workspaceName}`;
    }

    // Clear form
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    if (descriptionInput) descriptionInput.value = '';
    if (detailsInput) detailsInput.value = '';

    // Reset schedule fields for new task
    this.resetTaskModalScheduleFields();

    // Show modal
    modal.style.display = 'flex';

    // Focus title input
    setTimeout(() => {
      descriptionInput?.focus();
    }, 100);
  },

  // Save task from modal (create or update)
  async saveTaskFromModal() {
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');

    const description = descriptionInput?.value?.trim();
    const details = detailsInput?.value?.trim() || '';

    if (!description) {
      this.showToast('Task title is required', 'error');
      descriptionInput?.focus();
      return;
    }

    try {
      // Determine workspace ID - either from modal context or from active session
      let workspaceId = this.taskModalWorkspaceId;
      if (!workspaceId && this.activeSessionId) {
        // Get workspace ID from active session
        const session = this.sessions.find(s => s.id === this.activeSessionId);
        workspaceId = session?.folder_id;
      }

      if (!workspaceId) {
        this.showToast('No workspace selected', 'error');
        return;
      }

      // Get schedule data from modal
      const scheduleData = this.getScheduleDataFromModal();

      if (this.editingTaskId) {
        // Update existing task via orchestration API
        const response = await fetch(`/api/orchestration/tasks/${this.editingTaskId}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            task_id: this.editingTaskId,
            description,
            details,
            ...scheduleData
          })
        });

        if (!response.ok) throw new Error('Failed to update task');
        this.showToast('Task updated', 'success');
      } else {
        // Create new task via orchestration API (simple task - no agent)
        const response = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            workspace_id: workspaceId,
            description,
            details,
            priority: 3,
            ...scheduleData
          })
        });

        if (!response.ok) throw new Error('Failed to create task');
        this.showToast('Task created', 'success');
      }

      this.closeTaskModal();

      // Refresh data
      if (this.taskModalWorkspaceId) {
        await this.loadFolders();
        this.renderFolders();
      } else {
        await this.loadSessionTasks();
        this.renderMainTaskPanel();
      }
    } catch (error) {
      console.error('Failed to save task:', error);
      this.showToast('Failed to save task', 'error');
    }
  },

  // Update chat badge when tasks change (called from loadSessionTasks)
  updateChatTaskBadge() {
    const chatBadge = document.getElementById('chatTasksBadge');
    if (!chatBadge) return;

    if (this.taskCounts.total === 0) {
      chatBadge.classList.add('d-none');
    } else {
      chatBadge.classList.remove('d-none');
      if (this.taskCounts.pending > 0) {
        chatBadge.textContent = this.taskCounts.pending;
        chatBadge.classList.remove('complete');
      } else {
        chatBadge.textContent = '✓';
        chatBadge.classList.add('complete');
      }
    }
  }
};

// Export for use in other modules
window.sessionManager = sessionManager;

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  // Initialize if session sidebar OR the extracted modals exist
  if (document.getElementById('sessionSidebar') || document.getElementById('createChatModal')) {
    sessionManager.init();
  }

  // ?open=<noteId> opens the note's modal after navigation. One-shot:
  // scrubbed from history once the modal opens.
  try {
    const params = new URLSearchParams(window.location.search);
    const openNoteId = params.get('open');
    if (openNoteId) {
      // Strip from the URL before opening to keep refresh idempotent.
      params.delete('open');
      const qs = params.toString();
      const cleanUrl = window.location.pathname + (qs ? `?${qs}` : '') + window.location.hash;
      window.history.replaceState(null, '', cleanUrl);
      sessionManager.openNoteEditor(openNoteId);
    }
  } catch (_) {
    /* non-fatal */
  }

  // One-shot onboarding/deep links open the existing workspace wizard. The
  // canonical destination is Home; the legacy /workspaces route preserves the
  // query during its redirect. Scrub launch params so refresh cannot re-open.
  try {
    const params = new URLSearchParams(window.location.search);
    const openCreate = params.get('create') === '1';
    const openImport = params.get('import') === '1';
    if (openCreate || openImport) {
      const blueprint = String(params.get('blueprint') || '').trim();
      params.delete('create');
      params.delete('import');
      params.delete('blueprint');
      const qs = params.toString();
      const cleanUrl = window.location.pathname + (qs ? `?${qs}` : '') + window.location.hash;
      window.history.replaceState(null, '', cleanUrl);
      // Defer a frame so init()'s show.bs.modal handler is bound first.
      requestAnimationFrame(() =>
        sessionManager.showAddWorkspaceModal({
          entryPoint: openImport
            ? 'onboarding_import'
            : blueprint
              ? `${blueprint.replace(/-/g, '_')}_cta`
              : 'home_first_run',
          blueprint: blueprint && !openImport ? blueprint : undefined,
          importMode: openImport
        })
      );
    }
  } catch (_) {
    /* non-fatal */
  }
});
