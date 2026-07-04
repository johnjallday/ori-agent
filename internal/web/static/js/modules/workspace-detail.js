/**
 * Workspace Detail Page Module
 * Handles the dedicated workspace detail page with panels for tasks, sessions, files, and notes.
 *
 * @module workspace-detail
 */
/* global escapeHtml */

import { WorkspaceDirectoryExplorer } from './workspace-detail-directory-explorer.js';
import { WorkspaceMCPManager } from './workspace-detail-mcp.js';
import { WorkspaceNativeMCPManager } from './workspace-native-mcp.js';
import { WorkspaceSkillsManager } from './workspace-detail-skills.js';
import { WorkspacePluginsManager } from './workspace-detail-plugins.js';
import { WorkspaceMemoryManager } from './workspace-detail-memory.js';
import { WorkspaceFileModalManager } from './workspace-detail-file-modal.js';
import { WorkspaceMembersPanel } from './workspace-detail-members.js';
import { TemplateOnboardingPanel } from './template-onboarding.js';

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
    waiting_for_choice: 'blocked',
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
    waiting_for_choice: 'Waiting for Choice',
    cancelled: 'Cancelled',
    timeout: 'Timed Out'
  };
  return statusMap[status] || status;
}

function buildWorkspaceSlugConflictMessage(conflict) {
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
}

function slugifyWorkspaceName(value) {
  return String(value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-');
}

const AGENT_CREATION_SKILL_CATALOG_AGENT = '__ori_agent_create_catalog__';

const TASK_REQUIREMENT_KEYS = Object.freeze({
  BROWSER: 'browser',
  FILESYSTEM: 'filesystem'
});

const TASK_ASSIST_PENDING_SPECIALIST_STORAGE_KEY = 'workspace-detail-task-assist-specialist';
const ENTRY_AGENT_PROMPT_DISMISSED_STORAGE_PREFIX =
  'workspace-detail-entry-agent-prompt-dismissed:';

const TASK_ASSIST_TRAVEL_SPECIALISTS = Object.freeze({
  travel_itinerary: Object.freeze({
    key: 'travel_itinerary',
    label: 'Travel Itinerary Planner',
    agentName: 'Travel Itinerary Planner',
    agentType: 'tool-calling',
    description:
      'Plans day-by-day travel itineraries with neighborhood guidance, food picks, museum ideas, budget notes, and pacing.',
    systemPrompt:
      'You are a travel itinerary planner. Build practical, day-by-day trip plans with realistic pacing, local food and neighborhood recommendations, transit notes, and concise options. Ask clarifying questions when key details are missing and avoid inventing bookings or confirmed reservations.',
    nameTokens: ['travel', 'trip', 'itinerary'],
    scorePhrases: [
      'day by day',
      'day-by-day',
      'itinerary',
      'trip plan',
      'travel plan',
      'restaurant',
      'restaurants',
      'food',
      'museum',
      'museums',
      'nightlife',
      'day trip',
      'day trips',
      'budget breakdown',
      'budget',
      'accommodation',
      'accommodation areas',
      'neighborhood',
      'neighbourhood'
    ]
  }),
  hotel_booking: Object.freeze({
    key: 'hotel_booking',
    label: 'Hotel Booking Agent',
    agentName: 'Hotel Booking Agent',
    agentType: 'tool-calling',
    description:
      'Finds and compares hotels by neighborhood, budget, amenities, and travel constraints.',
    systemPrompt:
      'You are a hotel booking assistant. Help compare hotel options by neighborhood, budget, amenities, and transport tradeoffs. Ask for missing constraints before recommending stays, and do not invent live prices or confirmed bookings.',
    nameTokens: ['hotel', 'lodging', 'accommodation'],
    scorePhrases: [
      'hotel',
      'hotels',
      'stay',
      'stays',
      'lodging',
      'accommodation',
      'where to stay',
      'book hotel',
      'book hotels'
    ]
  }),
  flight_booking: Object.freeze({
    key: 'flight_booking',
    label: 'Flight Booking Agent',
    agentName: 'Flight Booking Agent',
    agentType: 'tool-calling',
    description:
      'Helps fill booking gaps for flights and longer-distance travel legs with schedule and transfer considerations.',
    systemPrompt:
      'You are a flight booking assistant. Help identify missing flight or long-distance travel legs, compare route options, call out tradeoffs, and confirm timing constraints before recommending bookings.',
    nameTokens: ['flight', 'airfare', 'airport'],
    scorePhrases: [
      'flight',
      'flights',
      'airfare',
      'airport',
      'route option',
      'route options',
      'connection',
      'connections',
      'transfer',
      'transfer timing'
    ]
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
    // IDs of notes currently checked for bulk copy/delete actions.
    this.selectedNoteIds = new Set();
    this.directories = [];
    this.schedules = [];
    this.children = [];
    this.directoryExplorer = new WorkspaceDirectoryExplorer(this);
    // Group-only Members panel + header identity; no-op for concrete workspaces.
    this.membersPanel = new WorkspaceMembersPanel(workspaceId);

    // Board state
    this.currentView = 'list'; // 'list' or 'board'
    this.boardConfig = null;
    this.agentOptions = null;
    this.agentCatalog = [];
    this.agentIndex = new Map();
    this.providerCatalog = [];
    this.providerCatalogPromise = null;
    this.agentSkillsCache = new Map();
    this.agentSkillsPromises = new Map();
    this.mcpManager = new WorkspaceMCPManager(this);
    this.nativeMCPManager = new WorkspaceNativeMCPManager(this);
    this.skillsManager = new WorkspaceSkillsManager(this);
    this.pluginsManager = new WorkspacePluginsManager(this);
    this.memoryManager = new WorkspaceMemoryManager(this);
    this.templateOnboardingPanel = new TemplateOnboardingPanel({
      workspaceId: this.workspaceId,
      mountId: 'workspace-template-onboarding',
      onRefresh: () => this.loadWorkspace()
    });
    this.workspaceSettings = null;
    this.workspaceSettingsEffectiveBehavior = null;
    this.workspaceTaskMarkdownStatus = null;
    this.workspaceConfigExpanded = false;
    // Per-task activity tracking: taskId -> { at: number, label: string }.
    // Fed by realtime task.* events; surfaced inline next to the status pill
    // on running tasks so users can see "Awaiting model response · 4s ago"
    // instead of an opaque "In Progress" with no signal of forward motion.
    this._taskActivity = new Map();
    this._taskActivityTickHandle = null;
    this.agentCatalogLoaded = false;
    this.agentCatalogLoadFailed = false;
    this.workspaceAgentSnapshots = new Set();
    // Workspace-local agent profiles (model/provider/type) keyed by normalized
    // name. These agents live in the workspace's config.json, not the global
    // agent catalog, so getAgentProfile() falls back to this map for them.
    this.workspaceAgentProfiles = new Map();
    this.filesLoaded = false;
    this.filesLoadFailed = false;
    this.workspaceHealthCheckRunning = false;
    this.uiSmokeTestRunning = false;
    this.uiSmokeTestResult = null;
    this.capabilitySuggestionCatalog = null;
    this.capabilitySuggestionCatalogPromise = null;
    this.flippedAgentCards = new Set();
    this.collapsedWorkflows = new Set();
    this.boardDidDrag = false;
    this.currentTaskResultText = '';
    this.currentTaskResultTaskId = '';
    this.currentTaskResultSourceTaskId = '';
    this.currentTaskResultNextSteps = [];
    this.currentTaskResultFollowUpPending = false;
    this.currentTaskResultPromotionPending = false;
    this.currentTaskResultPromotionSubmitting = false;
    this.currentTaskResultPromotionDraft = null;
    this.currentTaskResultPromotionContext = null;
    this.currentBlockedTask = null;
    this.currentAssistRecommendation = null;
    this.currentAssistSpecialistAction = null;
    this.taskAssistResponseExpanded = false;
    this.currentExecutionTaskId = null;
    this.executionMonitorTimer = null;
    this.executionLogKeys = new Set();
    this.executionLastStatus = '';
    this.pendingTaskConfirm = null;
    this.pendingTaskConfirmSelection = { stepThrough: false };
    this.pendingAssistSpecialistResumeChecked = false;
    this.activeAgentModelEdit = null;
    this.projectTemplates = [];
    this.projectTemplatesRoot = '';
    this.projectTemplateSubmitting = false;
    this.workspaceTagDraft = [];
    this.workspaceTagsSaving = false;

    // DOM elements
    this.elements = {};
    this.fileModalManager = new WorkspaceFileModalManager(this);
  }

  /**
   * Initialize the workspace detail page
   */
  async init() {
    this.cacheElements();
    this.ensureScrollablePanelAccessibility();
    this.refreshHomeAssistantQuickPrompts();
    this.bindEvents();
    this.fileModalManager.setupFileModal();
    this.setupFilesPanelVaultDrop();
    this.setupNotesPanelVaultDrop();
    this.setupPageDragAndDrop();
    await this.loadWorkspace();
    await this.templateOnboardingPanel.init();
    await this.loadAgentCatalog();
    await this.loadWorkspaceAgentSnapshots();
    await Promise.all([
      this.loadTasks(),
      this.loadSessions(),
      this.loadFiles(),
      this.loadNotes(),
      this.loadDirectories(),
      this.loadSchedules()
    ]);
    const restoredBlockedTask = this.restoreTaskAssistPageFromRoute();
    if (!restoredBlockedTask) {
      this.maybeResumePendingAssistSpecialistHandoff();
    }
    this.activateWorkspace();
    this.setupRealtime();
    if (restoredBlockedTask) {
      this.scheduleTaskAssistScrollReset(this.elements.taskAssistPage);
      window.setTimeout(
        () => this.scheduleTaskAssistScrollReset(this.elements.taskAssistPage),
        360
      );
    }
    if (!restoredBlockedTask && !this.checkAutoOpenCreateAgent()) {
      await this.maybePromptForMissingEntryAgent();
    }
  }

  ensureScrollablePanelAccessibility() {
    const panelConfig = [
      { id: 'workspace-detail-agents-panel', label: 'Agents panel content' },
      { id: 'workspace-detail-notes-panel', label: 'Notes panel content' },
      { id: 'workspace-detail-files-panel', label: 'Files panel content' },
      { id: 'workspace-detail-directories-panel', label: 'Directories panel content' },
      { id: 'workspace-detail-schedules-panel', label: 'Schedules panel content' },
      { id: 'workspace-detail-members-panel', label: 'Members panel content' }
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

    if (
      !homeAssistantQuickPlanBtn ||
      !homeAssistantQuickTasksBtn ||
      !homeAssistantQuickNotesBtn ||
      !homeAssistantQuickReviewBtn
    ) {
      return;
    }

    const workspaceName = String(this.workspace?.name || '').trim() || 'this workspace';
    const taskCount = Array.isArray(this.tasks) ? this.tasks.length : 0;
    const noteCount = Array.isArray(this.notes) ? this.notes.length : 0;
    const directoryCount =
      (Array.isArray(this.directories) ? this.directories.length : 0) +
      (this.workspaceHasProject() ? 1 : 0);
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
        'Create Description',
        `/note # Workspace Description\n## Description\n- \n## Apps and Systems\n- \n## Key Files or Context\n- \n## Special Capabilities or Workflows\n- `
      );
    } else {
      this.setHomeAssistantQuickPrompt(
        homeAssistantQuickNotesBtn,
        'Summarize Notes',
        `/ask Summarize notes in ${workspaceName} and produce a prioritized execution checklist.`
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

  setupFilesPanelVaultDrop() {
    const panel = document.getElementById('workspace-detail-files-panel');
    if (!panel || panel.dataset.vaultDropBound === 'true') {
      return;
    }
    panel.dataset.vaultDropBound = 'true';

    const hasVaultDrag = event => {
      if (window.__oriVaultDragAttachment || window.__oriVaultDragRecord) {
        return true;
      }
      const types = event.dataTransfer?.types;
      if (!types) return false;
      const list = Array.from(types);
      return (
        list.includes('application/x-ori-vault-attachment') ||
        list.includes('application/x-ori-vault-record')
      );
    };

    panel.addEventListener('dragenter', event => {
      if (!hasVaultDrag(event)) return;
      event.preventDefault();
      panel.classList.add('is-vault-drop-target');
    });

    panel.addEventListener('dragover', event => {
      if (!hasVaultDrag(event)) return;
      event.preventDefault();
      if (event.dataTransfer) {
        event.dataTransfer.dropEffect = 'copy';
      }
      panel.classList.add('is-vault-drop-target');
    });

    panel.addEventListener('dragleave', event => {
      if (event.target !== panel && panel.contains(event.relatedTarget)) {
        return;
      }
      panel.classList.remove('is-vault-drop-target');
    });

    panel.addEventListener('drop', async event => {
      const attachmentPayload = window.__oriVaultDragAttachment;
      const recordPayload = window.__oriVaultDragRecord;
      if (!attachmentPayload && !recordPayload) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      panel.classList.remove('is-vault-drop-target');
      window.__oriVaultDragAttachment = null;
      window.__oriVaultDragRecord = null;
      delete document.body.dataset.vaultAttachmentDragging;
      delete document.body.dataset.vaultRecordDragging;

      if (attachmentPayload) {
        await this.attachVaultAttachmentToFiles(attachmentPayload);
        return;
      }

      await this.attachVaultRecordToFiles(recordPayload);
    });
  }

  /**
   * Let the user drop files anywhere on the workspace page to upload them into
   * this workspace's files (saved to the workspace files root). Only reacts to
   * OS file drags ("Files" data type); internal vault/record drags use custom
   * MIME types and are handled by their own panel drop zones.
   */
  setupPageDragAndDrop() {
    if (document.body.dataset.workspaceDetailDropBound === 'true') {
      return;
    }
    document.body.dataset.workspaceDetailDropBound = 'true';

    const overlay = document.createElement('div');
    overlay.id = 'workspace-detail-drop-overlay';
    overlay.className = 'workspace-detail-drop-overlay';
    overlay.setAttribute('aria-hidden', 'true');
    overlay.innerHTML = `
      <div class="workspace-detail-drop-overlay-card">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path>
          <polyline points="7 9 12 4 17 9"></polyline>
          <line x1="12" y1="4" x2="12" y2="16"></line>
        </svg>
        <div class="workspace-detail-drop-overlay-title">Drop files to add to this workspace</div>
        <div class="workspace-detail-drop-overlay-subtitle">They'll be saved to this workspace's files</div>
      </div>`;
    document.body.appendChild(overlay);

    let dragDepth = 0;

    const isFileDrag = event => {
      const types = event.dataTransfer?.types;
      return Boolean(types) && Array.from(types).includes('Files');
    };
    const modalOpen = () => Boolean(document.querySelector('.modal.show'));
    const handledByDropZone = event =>
      Boolean(event.target?.closest?.('.modal, #hubFileDropZone, .is-vault-drop-target'));
    const hideOverlay = () => {
      dragDepth = 0;
      overlay.classList.remove('is-active');
    };

    document.addEventListener('dragenter', event => {
      if (!isFileDrag(event) || modalOpen()) return;
      event.preventDefault();
      dragDepth += 1;
      overlay.classList.add('is-active');
    });

    document.addEventListener('dragover', event => {
      if (!isFileDrag(event) || modalOpen()) return;
      event.preventDefault();
      if (event.dataTransfer) {
        event.dataTransfer.dropEffect = 'copy';
      }
    });

    document.addEventListener('dragleave', event => {
      if (!isFileDrag(event)) return;
      dragDepth -= 1;
      if (dragDepth <= 0) {
        hideOverlay();
      }
    });

    document.addEventListener('drop', event => {
      if (!isFileDrag(event)) return;
      // Never let the browser navigate to a dropped file.
      event.preventDefault();
      hideOverlay();
      // Dedicated drop zones (add-file modal, vault panels) own their drops.
      if (handledByDropZone(event) || modalOpen()) {
        return;
      }
      const files = Array.from(event.dataTransfer?.files || []);
      if (files.length > 0) {
        void this.uploadDroppedFiles(files);
      }
    });

    window.addEventListener('dragend', hideOverlay);
  }

  /**
   * Upload files dropped onto the page into the workspace files root.
   * @param {File[]} files
   */
  async uploadDroppedFiles(files) {
    if (!this.workspaceId || !Array.isArray(files) || files.length === 0) {
      return;
    }

    const maxSize = 10 * 1024 * 1024; // 10MB, matches the add-file modal
    const valid = [];
    for (const file of files) {
      const hasExtension = file.name.includes('.');
      const isLikelyFolder =
        (!file.type && file.size === 0) ||
        (!file.type && !hasExtension && file.size < 4096);
      if (isLikelyFolder) {
        if (window.Toast) {
          window.Toast.info('To add a folder, use the Linked Folders panel instead.', {
            title: 'Folder detected'
          });
        }
        continue;
      }
      if (file.size > maxSize) {
        if (window.Toast) {
          window.Toast.warning(`${file.name} exceeds the 10MB limit`);
        }
        continue;
      }
      valid.push(file);
    }

    if (valid.length === 0) {
      return;
    }

    let successCount = 0;
    for (const file of valid) {
      try {
        const formData = new FormData();
        formData.append('file', file);
        formData.append('workspace_id', this.workspaceId);
        // Dropped files land in the workspace files root (no folder_path).

        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`,
          {
            method: 'POST',
            body: formData
          }
        );

        if (!response.ok) {
          const payload = await response.json().catch(() => ({}));
          throw new Error(payload.error || payload.message || 'Upload failed');
        }
        successCount += 1;
      } catch (error) {
        console.error('Failed to upload dropped file:', file.name, error);
        if (window.Toast) {
          window.Toast.error(`Failed to add ${file.name}`);
        }
      }
    }

    if (successCount > 0) {
      if (window.Toast) {
        window.Toast.success(
          successCount === 1
            ? 'File added to workspace'
            : `${successCount} files added to workspace`
        );
      }
      await this.loadFiles();
      if (window.EventBus) {
        window.EventBus.emit('workspace:files:updated', { workspaceId: this.workspaceId });
      }
    }
  }

  setupNotesPanelVaultDrop() {
    const panel = document.getElementById('workspace-detail-notes-panel');
    if (!panel || panel.dataset.vaultDropBound === 'true') {
      return;
    }
    panel.dataset.vaultDropBound = 'true';

    const hasVaultRecordDrag = event => {
      if (window.__oriVaultDragRecord) {
        return true;
      }
      const types = event.dataTransfer?.types;
      if (!types) return false;
      return Array.from(types).includes('application/x-ori-vault-record');
    };

    panel.addEventListener('dragenter', event => {
      if (!hasVaultRecordDrag(event)) return;
      event.preventDefault();
      panel.classList.add('is-vault-drop-target');
    });

    panel.addEventListener('dragover', event => {
      if (!hasVaultRecordDrag(event)) return;
      event.preventDefault();
      if (event.dataTransfer) {
        event.dataTransfer.dropEffect = 'copy';
      }
      panel.classList.add('is-vault-drop-target');
    });

    panel.addEventListener('dragleave', event => {
      if (event.target !== panel && panel.contains(event.relatedTarget)) {
        return;
      }
      panel.classList.remove('is-vault-drop-target');
    });

    panel.addEventListener('drop', async event => {
      const recordPayload = window.__oriVaultDragRecord;
      if (!recordPayload) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      panel.classList.remove('is-vault-drop-target');
      window.__oriVaultDragRecord = null;
      window.__oriVaultDragAttachment = null;
      delete document.body.dataset.vaultAttachmentDragging;
      delete document.body.dataset.vaultRecordDragging;
      await this.attachVaultRecordToNotes(recordPayload);
    });
  }

  normalizeVaultReference(ref) {
    if (!ref || typeof ref !== 'object') return null;
    const normalized = {
      source_kind: String(ref.source_kind || ref.sourceKind || '').trim(),
      vault_id: String(ref.vault_id || ref.vaultId || '').trim(),
      vault_name: String(ref.vault_name || ref.vaultName || '').trim(),
      record_id: String(ref.record_id || ref.recordId || '').trim(),
      record_label: String(ref.record_label || ref.recordLabel || '').trim(),
      record_type: String(ref.record_type || ref.recordType || '').trim(),
      attachment_id: String(ref.attachment_id || ref.attachmentId || '').trim(),
      attachment_name: String(ref.attachment_name || ref.attachmentName || '').trim(),
      payload_key: String(ref.payload_key || ref.payloadKey || '').trim(),
      imported_at: String(ref.imported_at || ref.importedAt || '').trim(),
      last_synced_at: String(ref.last_synced_at || ref.lastSyncedAt || '').trim()
    };
    if (!normalized.record_id) return null;
    return normalized;
  }

  buildVaultReferenceFromItem(item, sourceKind, overrides = {}) {
    const base = {
      source_kind: sourceKind || item?.sourceKind || item?.source_kind || '',
      vault_id: item?.vaultId || item?.vault_id || '',
      vault_name: item?.vaultName || item?.vault_name || '',
      record_id: item?.recordId || item?.record_id || '',
      record_label: item?.recordLabel || item?.record_label || '',
      record_type: item?.recordType || item?.record_type || '',
      attachment_id: item?.attachmentId || item?.attachment_id || '',
      attachment_name: item?.name || item?.attachmentName || item?.attachment_name || '',
      imported_at: new Date().toISOString()
    };
    return this.normalizeVaultReference({ ...base, ...overrides });
  }

  buildVaultReferenceFromRecord(record, sourceKind, overrides = {}) {
    const base = {
      source_kind: sourceKind || '',
      vault_id: record?.vaultId || record?.vault_id || '',
      vault_name: record?.vaultName || record?.vault_name || '',
      record_id: record?.recordId || record?.record_id || record?.id || '',
      record_label: record?.recordLabel || record?.record_label || record?.label || '',
      record_type: record?.recordType || record?.record_type || record?.type || '',
      imported_at: new Date().toISOString()
    };
    return this.normalizeVaultReference({ ...base, ...overrides });
  }

  vaultReferenceDisplayName(ref) {
    const normalized = this.normalizeVaultReference(ref);
    if (!normalized) return '';
    const record = normalized.record_label || 'Vault entry';
    const vault = normalized.vault_name || 'Private vault';
    return `${record} · ${vault}`;
  }

  renderVaultReferenceBadge(ref) {
    const normalized = this.normalizeVaultReference(ref);
    if (!normalized) return '';
    const label = this.vaultReferenceDisplayName(normalized);
    return `
      <span class="workspace-detail-vault-reference-badge" title="${this.escapeHtml(label)}">Private Vault Reference</span>
      <span class="workspace-detail-vault-reference-label">${this.escapeHtml(label)}</span>
    `;
  }

  vaultReferenceNotes(ref) {
    const normalized = this.normalizeVaultReference(ref);
    if (!normalized) return '';
    return `Private Vault Reference: ${this.vaultReferenceDisplayName(normalized)}`;
  }

  extractNoteContentFromVaultRecord(record) {
    if (!record || !record.payload || typeof record.payload !== 'object') {
      return { content: '', payloadKey: '' };
    }
    const payload = record.payload;
    for (const key of ['note', 'content', 'text', 'body', 'value']) {
      if (typeof payload[key] === 'string' && payload[key].trim()) {
        return { content: payload[key], payloadKey: key };
      }
    }
    try {
      return { content: '```json\n' + JSON.stringify(payload, null, 2) + '\n```', payloadKey: '' };
    } catch (_) {
      return { content: '', payloadKey: '' };
    }
  }

  async attachVaultRecordToNotes(record) {
    if (!record) return;

    if (!record.payloadRevealed) {
      if (window.Toast) {
        window.Toast.warning(
          `Reveal "${record.recordLabel || 'this entry'}" first so its note content can be copied.`
        );
      }
      return;
    }

    const extracted = this.extractNoteContentFromVaultRecord(record);
    if (!extracted.content) {
      if (window.Toast) {
        window.Toast.warning(
          `"${record.recordLabel || 'This entry'}" has no readable note content to copy.`
        );
      }
      return;
    }

    const title = record.recordLabel || 'Vault note';
    const vaultReference = this.buildVaultReferenceFromRecord(record, 'note', {
      payload_key: extracted.payloadKey
    });
    const ok = await this.createNote(title, extracted.content, null, { vaultReference });
    if (!ok) {
      return;
    }
  }

  async attachVaultRecordToFiles(record) {
    if (!record) return;

    if (!record.attachments || record.attachments.length === 0) {
      const message = record.payloadRevealed
        ? `"${record.recordLabel || 'This entry'}" has no file attachments. Drop it on the Notes panel to copy its text content instead.`
        : `Reveal "${record.recordLabel || 'this entry'}" first. Drop on Files for attached files, or on Notes for note text.`;
      if (window.Toast) {
        window.Toast.warning(message);
      }
      return;
    }

    let succeeded = 0;
    for (const attachment of record.attachments) {
      try {
        await this.attachVaultAttachmentToFiles(
          {
            ...attachment,
            attachmentId: attachment.attachmentId || attachment.id,
            recordLabel: record.recordLabel,
            recordId: record.recordId,
            recordType: record.recordType,
            vaultId: record.vaultId,
            vaultName: record.vaultName
          },
          { silent: true }
        );
        succeeded += 1;
      } catch (error) {
        console.error('Failed to attach vault record attachment:', error);
      }
    }

    if (succeeded > 0 && window.Toast) {
      window.Toast.success(
        succeeded === 1
          ? `Added 1 file from "${record.recordLabel}"`
          : `Added ${succeeded} files from "${record.recordLabel}"`
      );
    } else if (succeeded === 0 && window.Toast) {
      window.Toast.error('Failed to attach files from the vault entry');
    }
  }

  async attachVaultAttachmentToFiles(item, options = {}) {
    const silent = Boolean(options.silent);

    if (!item || !item.contentBase64) {
      if (!silent && window.Toast) {
        window.Toast.error('Reveal the vault payload before dragging an attachment.');
      }
      if (silent) {
        throw new Error('Missing decoded vault attachment content');
      }
      return;
    }

    try {
      const file = this.buildWorkspaceFileFromVaultAttachment(item);
      const formData = new FormData();
      formData.append('file', file);
      formData.append('workspace_id', this.workspaceId);
      const folderPath = this.getSelectedUploadFolderPath();
      if (folderPath) formData.append('folder_path', folderPath);
      const vaultReference = this.buildVaultReferenceFromItem(item, 'file');
      if (vaultReference) {
        formData.append('vault_reference', JSON.stringify(vaultReference));
        formData.append('notes', this.vaultReferenceNotes(vaultReference));
      } else if (item.recordLabel) {
        formData.append('notes', `From vault entry: ${item.recordLabel}`);
      }

      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`,
        {
          method: 'POST',
          body: formData
        }
      );

      if (!response.ok) {
        throw new Error(`Failed to add ${item.name || 'vault file'} to the workspace`);
      }

      if (!silent && window.Toast) {
        window.Toast.success(`Added ${item.name || 'vault file'} to workspace`);
      }
      await this.loadFiles();
    } catch (error) {
      console.error('Failed to attach vault file via drag-and-drop:', error);
      if (silent) {
        throw error;
      }
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to attach vault file to workspace');
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


  /**
   * Cache DOM elements
   */
  cacheElements() {
    this.elements = {
      // Header elements
      workspaceName: document.getElementById('workspace-name'),
      workspaceDescription: document.getElementById('workspace-description'),
      workspaceTagsContainer: document.getElementById('workspace-tags-container'),
      workspaceTagsList: document.getElementById('workspace-tags-list'),
      workspaceTagsEditBtn: document.getElementById('workspace-tags-edit-btn'),
      workspaceTagsEditor: document.getElementById('workspace-tags-editor'),
      workspaceTagsEditorMount: document.getElementById('workspace-tags-editor-mount'),
      workspaceTagsSaveBtn: document.getElementById('workspace-tags-save-btn'),
      workspaceTagsCancelBtn: document.getElementById('workspace-tags-cancel-btn'),
      workspaceTagsError: document.getElementById('workspace-tags-error'),
      workspaceBreadcrumb: document.getElementById('workspace-breadcrumb-name'),
      openCanvasBtn: document.getElementById('open-canvas-btn'),
      openDiagnosticsBtn: document.getElementById('open-diagnostics-btn'),
      openWorkflowsBtn: document.getElementById('open-workflows-btn'),
      workflowLinksPanel: document.getElementById('workspace-detail-workflow-links-panel'),
      workflowLinksList: document.getElementById('workspace-detail-workflow-links-list'),
      workflowLinksCount: document.getElementById('workspace-detail-workflow-links-count'),
      healthPanel: document.getElementById('workspace-detail-health-panel'),
      healthSummary: document.getElementById('workspace-detail-health-summary'),
      healthBadge: document.getElementById('workspace-detail-health-badge'),
      healthMeta: document.getElementById('workspace-detail-health-meta'),
      healthList: document.getElementById('workspace-detail-health-list'),
      healthRefreshBtn: document.getElementById('workspace-detail-health-refresh'),
      healthRefreshLabel: document.getElementById('workspace-detail-health-refresh-label'),
      uiSmokeTestBtn: document.getElementById('workspace-detail-ui-smoke-test'),
      uiSmokeTestLabel: document.getElementById('workspace-detail-ui-smoke-test-label'),
      uiSmokeTestResult: document.getElementById('workspace-detail-ui-smoke-result'),

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
      filesPanel: document.getElementById('workspace-detail-files-panel'),
      filesList: document.getElementById('workspace-detail-files-list'),
      browseFilesBtn: document.getElementById('workspace-detail-browse-files'),
      notesList: document.getElementById('workspace-detail-notes-list'),
      directoriesList: document.getElementById('workspace-detail-directories-list'),
      mcpList: document.getElementById('workspace-detail-mcp-list'),
      settingsSummary: document.getElementById('workspace-detail-settings-summary'),
      settingsManagedSkills: document.getElementById('workspace-detail-settings-managed-skills'),
      intentForm: document.getElementById('workspace-detail-intent-form'),
      intentDescriptionInput: document.getElementById('workspace-detail-intent-description'),
      intentSystemsInput: document.getElementById('workspace-detail-intent-systems'),
      intentCapabilitiesInput: document.getElementById('workspace-detail-intent-capabilities'),
      intentContextInput: document.getElementById('workspace-detail-intent-context'),
      intentSummary: document.getElementById('workspace-detail-intent-summary'),
      intentStatus: document.getElementById('workspace-detail-intent-status'),
      intentSaveBtn: document.getElementById('workspace-detail-intent-save'),
      skillsList: document.getElementById('workspace-detail-skills-list'),
      pluginsList: document.getElementById('workspace-detail-plugins-list'),
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
      configReferenceChip: document.getElementById('workspace-detail-config-reference-chip'),
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
      copyAllNotesBtn: document.getElementById('workspace-detail-copy-all-notes'),
      selectAllNotesBtn: document.getElementById('workspace-detail-select-all-notes'),
      deleteSelectedNotesBtn: document.getElementById('workspace-detail-delete-selected-notes'),
      notesActions: document.getElementById('workspace-detail-notes-actions'),
      viewAllNotesLink: document.getElementById('workspace-detail-view-all-notes'),
      createProjectBtn: document.getElementById('workspace-detail-create-project'),
      addDirectoryBtn: document.getElementById('workspace-detail-add-directory'),
      addMcpBtn: document.getElementById('workspace-detail-add-mcp'),
      refreshMcpBtn: document.getElementById('workspace-detail-refresh-mcp'),
      refreshSettingsBtn: document.getElementById('workspace-detail-refresh-settings'),
      addSkillBtn: document.getElementById('workspace-detail-add-skill'),
      refreshSkillsBtn: document.getElementById('workspace-detail-refresh-skills'),
      refreshPluginsBtn: document.getElementById('workspace-detail-refresh-plugins'),
      viewSchedulesBtn: document.getElementById('workspace-detail-view-schedules'),
      homeAssistantQuickPlanBtn: document.getElementById('homeAssistantQuickPlan'),
      homeAssistantQuickTasksBtn: document.getElementById('homeAssistantQuickTasks'),
      homeAssistantQuickNotesBtn: document.getElementById('homeAssistantQuickNotes'),
      homeAssistantQuickReviewBtn: document.getElementById('homeAssistantQuickReview'),
      homeAssistantConfigureTaskBtn: document.getElementById('homeAssistantConfigureTaskBtn'),
      homeAssistantInput: document.getElementById('homeAssistantInput'),

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
      taskResultNextStepsCopy: document.getElementById(
        'workspace-detail-task-result-next-steps-copy'
      ),
      taskResultNextStepsActions: document.getElementById(
        'workspace-detail-task-result-next-steps-actions'
      ),
      taskResultCopyBtn: document.getElementById('workspace-detail-task-result-copy'),
      taskResultPromoteModal: document.getElementById(
        'workspace-detail-task-result-promote-modal'
      ),
      taskResultPromoteTitleInput: document.getElementById(
        'workspace-detail-task-result-promote-title'
      ),
      taskResultPromoteMeta: document.getElementById('workspace-detail-task-result-promote-meta'),
      taskResultPromoteGroups: document.getElementById(
        'workspace-detail-task-result-promote-groups'
      ),
      taskResultPromoteSubmitBtn: document.getElementById(
        'workspace-detail-task-result-promote-submit'
      ),
      taskExecutionModal: document.getElementById('workspace-detail-task-execution-modal'),
      taskExecutionTitle: document.getElementById('workspace-detail-task-execution-title'),
      taskExecutionId: document.getElementById('workspace-detail-task-execution-id'),
      taskExecutionMeta: document.getElementById('workspace-detail-task-execution-meta'),
      taskExecutionStatus: document.getElementById('workspace-detail-task-execution-status'),
      taskExecutionBreakdown: document.getElementById('workspace-detail-task-execution-breakdown'),
      taskExecutionLog: document.getElementById('workspace-detail-task-execution-log'),
      taskExecutionNextStepBtn: document.getElementById(
        'workspace-detail-task-execution-next-step'
      ),
      taskExecutionViewResultBtn: document.getElementById(
        'workspace-detail-task-execution-view-result'
      ),
      taskConfirmModal: document.getElementById('workspace-detail-task-confirm-modal'),
      taskConfirmEyebrow: document.getElementById('workspace-detail-task-confirm-eyebrow'),
      taskConfirmTitle: document.getElementById('workspace-detail-task-confirm-title'),
      taskConfirmMessage: document.getElementById('workspace-detail-task-confirm-message'),
      taskConfirmMeta: document.getElementById('workspace-detail-task-confirm-meta'),
      taskConfirmDetails: document.getElementById('workspace-detail-task-confirm-details'),
      taskConfirmSequence: document.getElementById('workspace-detail-task-confirm-sequence'),
      taskConfirmStepMode: document.getElementById('workspace-detail-task-confirm-step-mode'),
      taskConfirmStepModeInput: document.getElementById(
        'workspace-detail-task-confirm-step-mode-input'
      ),
      taskConfirmCancelBtn: document.getElementById('workspace-detail-task-confirm-cancel'),
      taskConfirmCloseBtn: document.getElementById('workspace-detail-task-confirm-close'),
      taskConfirmConfirmBtn: document.getElementById('workspace-detail-task-confirm-confirm'),

      // Project template modal
      projectTemplateModal: document.getElementById('workspace-detail-project-template-modal'),
      projectTemplateForm: document.getElementById('workspace-detail-project-template-form'),
      projectTemplateSelect: document.getElementById('workspace-detail-project-template-select'),
      projectTemplateDescription: document.getElementById(
        'workspace-detail-project-template-description'
      ),
      projectTemplateRoot: document.getElementById('workspace-detail-project-template-root'),
      projectTemplateRefreshBtn: document.getElementById(
        'workspace-detail-project-template-refresh'
      ),
      projectTemplatePathInput: document.getElementById('workspace-detail-project-template-path'),
      projectTemplateBrowseBtn: document.getElementById(
        'workspace-detail-project-template-browse'
      ),
      projectNameInput: document.getElementById('workspace-detail-project-name'),
      projectTemplateSubmitBtn: document.getElementById(
        'workspace-detail-project-template-submit'
      ),
      projectTemplateError: document.getElementById('workspace-detail-project-template-error'),

      // Assist modal
      workspaceDetailView: document.getElementById('workspace-detail-view'),
      taskAssistPage: document.getElementById('workspace-detail-task-assist-page'),
      taskAssistBackBtn: document.getElementById('workspace-detail-task-assist-back'),
      taskAssistId: document.getElementById('workspace-detail-task-assist-id'),
      taskAssistAgentName: document.getElementById('workspace-detail-task-assist-agent-name'),
      taskAssistMeta: document.getElementById('workspace-detail-task-assist-meta'),
      taskAssistReason: document.getElementById('workspace-detail-task-assist-reason'),
      taskAssistContextScroll: document.getElementById(
        'workspace-detail-task-assist-context-scroll'
      ),
      taskAssistAnswerScroll: document.getElementById('workspace-detail-task-assist-answer-scroll'),
      taskAssistSummaryWrap: document.getElementById('workspace-detail-task-assist-summary-wrap'),
      taskAssistSummaryKnown: document.getElementById('workspace-detail-task-assist-summary-known'),
      taskAssistSummaryNeeds: document.getElementById('workspace-detail-task-assist-summary-needs'),
      taskAssistSummaryNext: document.getElementById('workspace-detail-task-assist-summary-next'),
      taskAssistQuestionWrap: document.getElementById('workspace-detail-task-assist-question-wrap'),
      taskAssistQuestion: document.getElementById('workspace-detail-task-assist-question'),
      taskAssistFormWrap: document.getElementById('workspace-detail-task-assist-form-wrap'),
      taskAssistFormSummary: document.getElementById('workspace-detail-task-assist-form-summary'),
      taskAssistFormFields: document.getElementById('workspace-detail-task-assist-form-fields'),
      taskAssistRecommendationWrap: document.getElementById(
        'workspace-detail-task-assist-recommendation-wrap'
      ),
      taskAssistRecommendation: document.getElementById(
        'workspace-detail-task-assist-recommendation'
      ),
      taskAssistChoiceWrap: document.getElementById('workspace-detail-task-assist-choice-wrap'),
      taskAssistChoiceSummary: document.getElementById(
        'workspace-detail-task-assist-choice-summary'
      ),
      taskAssistChoiceList: document.getElementById('workspace-detail-task-assist-choice-list'),
      taskAssistSpecialistWrap: document.getElementById(
        'workspace-detail-task-assist-specialist-wrap'
      ),
      taskAssistSpecialistCopy: document.getElementById(
        'workspace-detail-task-assist-specialist-copy'
      ),
      taskAssistSpecialistActionBtn: document.getElementById(
        'workspace-detail-task-assist-specialist-action'
      ),
      taskAssistAgent: document.getElementById('workspace-detail-task-assist-agent'),
      taskAssistMessage: document.getElementById('workspace-detail-task-assist-message'),
      taskAssistResponseWrap: document.getElementById('workspace-detail-task-assist-response-wrap'),
      taskAssistResponsePreview: document.getElementById(
        'workspace-detail-task-assist-response-preview'
      ),
      taskAssistResponseToggle: document.getElementById(
        'workspace-detail-task-assist-response-toggle'
      ),
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
      agentModelModal: document.getElementById('workspace-detail-agent-model-modal'),
      agentModelTitle: document.getElementById('workspace-detail-agent-model-title'),
      agentModelAgentName: document.getElementById('workspace-detail-agent-model-agent-name'),
      agentModelCurrent: document.getElementById('workspace-detail-agent-model-current'),
      agentModelSelect: document.getElementById('workspace-detail-agent-model-select'),
      agentModelHelp: document.getElementById('workspace-detail-agent-model-help'),
      agentModelSubmitBtn: document.getElementById('workspace-detail-agent-model-submit'),

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
      mcpEmailSendConfirmWrap: document.getElementById(
        'workspace-detail-mcp-email-send-confirm-wrap'
      ),
      mcpEmailSendConfirmInput: document.getElementById('workspace-detail-mcp-email-send-confirm'),
      mcpAgentOptions: document.getElementById('workspace-detail-mcp-agent-options'),
      mcpAgentAccessSummary: document.getElementById('workspace-detail-mcp-agent-access-summary'),
      mcpSubmitBtn: document.getElementById('workspace-detail-mcp-submit'),
      settingsForm: document.getElementById('workspace-detail-settings-form'),
      settingsProfileInput: document.getElementById('workspace-detail-settings-profile'),
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
      settingsClarificationModeInput: document.getElementById(
        'workspace-detail-settings-clarification-mode'
      ),
      settingsTasksDirInput: document.getElementById('workspace-detail-settings-tasks-dir'),
      settingsExecutionModeInput: document.getElementById(
        'workspace-detail-settings-execution-mode'
      ),
      settingsWritePRDInput: document.getElementById('workspace-detail-settings-write-prd'),
      settingsWriteTaskListInput: document.getElementById(
        'workspace-detail-settings-write-task-list'
      ),
      settingsRequireBranchInput: document.getElementById(
        'workspace-detail-settings-require-branch'
      ),
      settingsTaskMarkdownEnabledInput: document.getElementById(
        'workspace-detail-settings-task-markdown-enabled'
      ),
      settingsTaskMarkdownPathInput: document.getElementById(
        'workspace-detail-settings-task-markdown-path'
      ),
      settingsTaskMarkdownAgentViewsInput: document.getElementById(
        'workspace-detail-settings-task-markdown-agent-views'
      ),
      settingsTaskMarkdownRefreshBtn: document.getElementById(
        'workspace-detail-settings-task-markdown-refresh'
      ),
      settingsTaskMarkdownStatus: document.getElementById(
        'workspace-detail-settings-task-markdown-status'
      ),
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
      skillPlanningClarificationModeInput: document.getElementById(
        'workspace-detail-skill-planning-clarification-mode'
      ),
      skillPlanningTasksDirInput: document.getElementById(
        'workspace-detail-skill-planning-tasks-dir'
      ),
      skillPlanningDefaultExecutionInput: document.getElementById(
        'workspace-detail-skill-planning-default-execution'
      ),
      skillPlanningWritePRDInput: document.getElementById(
        'workspace-detail-skill-planning-write-prd'
      ),
      skillPlanningWriteTaskListInput: document.getElementById(
        'workspace-detail-skill-planning-write-task-list'
      ),
      skillPlanningSyncTasksInput: document.getElementById(
        'workspace-detail-skill-planning-sync-tasks'
      ),
      skillPlanningRequireBranchInput: document.getElementById(
        'workspace-detail-skill-planning-require-branch'
      ),
      skillAgentOptions: document.getElementById('workspace-detail-skill-agent-options'),
      skillAgentAccessSummary: document.getElementById(
        'workspace-detail-skill-agent-access-summary'
      ),
      skillSubmitBtn: document.getElementById('workspace-detail-skills-submit'),

      // Directory explorer modal
      directoryExplorerModal: document.getElementById('workspace-directory-explorer-modal'),
      directoryExplorerTitle: document.getElementById('workspace-directory-explorer-title'),
      directoryExplorerSubtitle: document.getElementById('workspace-directory-explorer-subtitle'),
      directoryExplorerSummary: document.getElementById('workspace-directory-explorer-summary'),
      directoryExplorerBreadcrumb: document.getElementById(
        'workspace-directory-explorer-breadcrumb'
      ),
      directoryExplorerSearch: document.getElementById('workspace-directory-explorer-search'),
      directoryExplorerSortBtn: document.getElementById('workspace-directory-explorer-sort'),
      directoryExplorerRefreshBtn: document.getElementById('workspace-directory-explorer-refresh'),
      directoryExplorerCreateFolderBtn: document.getElementById(
        'workspace-directory-explorer-create-folder'
      ),
      directoryExplorerRenameFolderBtn: document.getElementById(
        'workspace-directory-explorer-rename-folder'
      ),
      directoryExplorerDeleteFolderBtn: document.getElementById(
        'workspace-directory-explorer-delete-folder'
      ),
      directoryExplorerTree: document.getElementById('workspace-directory-explorer-tree'),
      directoryExplorerPreview: document.getElementById('workspace-directory-explorer-preview'),
      fileFolderPath: document.getElementById('hubFileFolderPath'),
      fileFolderSelect: document.getElementById('hubFileFolderSelect'),
      createUploadFolderBtn: document.getElementById('hubCreateUploadFolderBtn')
    };
  }

  /**
   * Bind event handlers
   */
  bindEvents() {
    // Quick input
    this.elements.smartSubmit?.addEventListener('click', () => this.handleQuickInput());
    this.elements.smartInput?.addEventListener('keypress', e => {
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
    this.elements.addFileBtn?.addEventListener('click', event => {
      event.stopPropagation();
      this.showFileModal();
    });
    this.elements.browseFilesBtn?.addEventListener('click', event => {
      event.stopPropagation();
      this.openWorkspaceFilesExplorer();
    });
    this.elements.filesPanel?.addEventListener('click', event => {
      if (event.target.closest('button, a, input, select, textarea')) {
        return;
      }

      this.openWorkspaceFilesExplorer();
    });
    this.elements.filesPanel?.addEventListener('keydown', event => {
      if (event.target.closest('button, a, input, select, textarea')) {
        return;
      }

      if (event.key !== 'Enter' && event.key !== ' ') {
        return;
      }

      event.preventDefault();
      this.openWorkspaceFilesExplorer();
    });

    // Note buttons
    this.elements.addNoteBtn?.addEventListener('click', () => this.showNoteModal());
    this.elements.selectAllNotesBtn?.addEventListener('click', () => this.toggleSelectAllNotes());
    this.elements.copyAllNotesBtn?.addEventListener('click', () => this.copySelectedNotesToClipboard());
    this.elements.deleteSelectedNotesBtn?.addEventListener('click', () => this.deleteSelectedNotes());

    // "View all" -> workspace notes app. The href is set here (not in the
    // template) because the workspace ID isn't known at server-render time.
    if (this.elements.viewAllNotesLink && this.workspaceId) {
      this.elements.viewAllNotesLink.href = `/workspaces/${encodeURIComponent(this.workspaceId)}/notes`;
    }

    // "Open task editor" icon button: opens the task modal in auto mode with whatever
    // text is currently in the entry-prompt input, so users can review/configure before saving.
    this.elements.homeAssistantConfigureTaskBtn?.addEventListener('click', () => {
      if (!window.taskModalController || typeof window.taskModalController.openForCreate !== 'function') {
        console.warn('Task modal controller not available');
        return;
      }
      const input = this.elements.homeAssistantInput;
      const description = String(input?.value || '').trim();
      window.taskModalController.openForCreate(this.workspaceId, description, () => {
        if (input) input.value = '';
        this.loadTasks?.();
        this.loadSchedules?.();
      }, {
        forceAutoMode: true,
        prefillAutoDescription: description,
      });
    });

    // Quick "Create Description" button: route multiline /note templates to the note modal
    // instead of stuffing markdown (with newlines) into the single-line entry input where it
    // collapses to garbage.
    this.elements.homeAssistantQuickNotesBtn?.addEventListener('click', (event) => {
      const button = event.currentTarget;
      const prompt = button?.getAttribute('data-home-prompt') || '';
      if (!prompt.startsWith('/note ') || !prompt.includes('\n')) return;
      event.stopImmediatePropagation();
      event.preventDefault();
      const body = prompt.slice('/note '.length);
      const lines = body.split('\n');
      let title = '';
      let content = body;
      if (lines[0]?.startsWith('# ')) {
        title = lines[0].slice(2).trim();
        content = lines.slice(1).join('\n').replace(/^\n+/, '');
      }
      this.showNoteModal();
      window.requestAnimationFrame(() => {
        const titleInput = document.getElementById('noteNameInput');
        const contentInput = document.getElementById('noteContentInput');
        if (titleInput && title) titleInput.value = title;
        if (contentInput) contentInput.value = content;
      });
    }, true);

    // Directory buttons
    this.elements.createProjectBtn?.addEventListener('click', event => {
      event.stopPropagation();
      this.showProjectTemplateModal(this.elements.createProjectBtn);
    });
    this.elements.addDirectoryBtn?.addEventListener('click', event => {
      event.stopPropagation();
      this.showAddDirectoryModal(this.elements.addDirectoryBtn);
    });
    this.elements.projectTemplateForm?.addEventListener('submit', event => {
      event.preventDefault();
      this.createWorkspaceProject();
    });
    this.elements.projectTemplateSelect?.addEventListener('change', () => {
      if (this.elements.projectTemplateSelect?.value && this.elements.projectTemplatePathInput) {
        this.elements.projectTemplatePathInput.value = '';
      }
      this.updateProjectTemplateModalState();
    });
    this.elements.projectTemplatePathInput?.addEventListener('input', () => {
      if (this.elements.projectTemplatePathInput?.value.trim() && this.elements.projectTemplateSelect) {
        this.elements.projectTemplateSelect.value = '';
      }
      this.updateProjectTemplateModalState();
    });
    this.elements.projectTemplateBrowseBtn?.addEventListener('click', () =>
      this.browseProjectTemplateFolder()
    );
    this.elements.projectTemplateRefreshBtn?.addEventListener('click', () =>
      this.populateProjectTemplateOptions({ force: true })
    );
    this.elements.addMcpBtn?.addEventListener('click', () => this.openWorkspaceMCPModal());
    this.bindDirectoryExplorerEvents();
    this.elements.refreshMcpBtn?.addEventListener('click', async () => {
      await this.loadAvailableMCPServers(true).catch(error => {
        console.warn('Failed to refresh MCP connector catalog:', error);
      });
      await this.loadWorkspace();
    });
    this.mcpManager.bindEvents();
    this.nativeMCPManager.bindEvents();
    this.nativeMCPManager.load();

    // Workspace settings
    this.elements.configToggleBtn?.addEventListener('click', () =>
      this.toggleWorkspaceConfigExpanded()
    );
    this.elements.refreshSettingsBtn?.addEventListener('click', () => this.loadWorkspace());
    this.elements.healthRefreshBtn?.addEventListener('click', () => this.refreshWorkspaceHealth());
    this.elements.uiSmokeTestBtn?.addEventListener('click', () => this.runUISmokeTest());
    this.elements.intentForm?.addEventListener('submit', event => {
      event.preventDefault();
      this.saveWorkspaceIntent();
    });
    this.elements.settingsForm?.addEventListener('submit', event => {
      event.preventDefault();
      this.saveWorkspaceSettings();
    });
    this.elements.settingsProfileInput?.addEventListener('change', () =>
      this.handleWorkspaceSettingsProfileChange()
    );
    this.elements.settingsPresetInput?.addEventListener('change', () =>
      this.handleWorkspaceSettingsPresetChange()
    );
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
      this.elements.settingsRequireBranchInput,
      this.elements.settingsTaskMarkdownEnabledInput,
      this.elements.settingsTaskMarkdownPathInput,
      this.elements.settingsTaskMarkdownAgentViewsInput
    ].forEach(input => {
      input?.addEventListener('change', () => this.handleWorkspaceSettingsFieldChange());
    });
    this.elements.settingsTaskMarkdownPathInput?.addEventListener('input', () =>
      this.handleWorkspaceSettingsFieldChange()
    );
    this.elements.settingsTaskMarkdownRefreshBtn?.addEventListener('click', () =>
      this.importWorkspaceTaskMarkdownNow()
    );

    // Skill buttons
    this.elements.addSkillBtn?.addEventListener('click', () => this.openWorkspaceSkillModal());
    this.elements.refreshSkillsBtn?.addEventListener('click', async () => {
      await this.loadAvailableSkills(true).catch(error => {
        console.warn('Failed to refresh skill catalog:', error);
      });
      await this.loadWorkspace();
    });
    this.skillsManager.bindEvents();
    this.pluginsManager.bindEvents();
    this.memoryManager.bindEvents();
    this.elements.skillsModal?.addEventListener('shown.bs.modal', () => {
      this.applyTopBackdropLayer('workspace-detail-backdrop-skills');
    });

    // Schedule buttons
    this.elements.viewSchedulesBtn?.addEventListener('click', () => this.showSchedulesModal());
    this.elements.taskResultCopyBtn?.addEventListener('click', () => this.copyCurrentTaskResult());
    this.elements.taskResultPromoteSubmitBtn?.addEventListener('click', () =>
      this.submitTaskResultPromotion()
    );
    this.elements.taskExecutionViewResultBtn?.addEventListener('click', () => {
      if (this.currentExecutionTaskId) {
        this.showTaskResult(this.currentExecutionTaskId, { closeExecutionModal: true });
      }
    });
    this.elements.taskExecutionNextStepBtn?.addEventListener('click', () =>
      this.advanceCurrentExecutionStep()
    );
    this.elements.taskResultModal?.addEventListener('shown.bs.modal', () => {
      this.applyTopBackdropLayer('workspace-detail-backdrop-result');
    });
    this.elements.taskResultPromoteModal?.addEventListener('shown.bs.modal', () => {
      this.applyTopBackdropLayer('workspace-detail-backdrop-result-promote');
    });
    this.elements.taskResultPromoteModal?.addEventListener('hidden.bs.modal', () => {
      if (this.currentTaskResultPromotionSubmitting) return;
      this.currentTaskResultPromotionDraft = null;
      this.currentTaskResultPromotionContext = null;
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
    this.elements.taskConfirmCancelBtn?.addEventListener('click', () =>
      this.handleTaskConfirmChoice(false)
    );
    this.elements.taskConfirmConfirmBtn?.addEventListener('click', () =>
      this.handleTaskConfirmChoice(true)
    );
    this.elements.taskConfirmModal?.addEventListener('hidden.bs.modal', () =>
      this.handleTaskConfirmHidden()
    );
    this.elements.taskAssistBackBtn?.addEventListener('click', () =>
      this.closeTaskAssistPage({ replaceRoute: true })
    );
    this.elements.taskAssistRetryBtn?.addEventListener('click', () =>
      this.submitTaskAssist('retry')
    );
    this.elements.taskAssistContinueBtn?.addEventListener('click', () =>
      this.submitTaskAssist('continue_with_instruction')
    );
    this.elements.taskAssistSwitchBtn?.addEventListener('click', () =>
      this.submitTaskAssist('switch_agent_retry')
    );
    this.elements.taskAssistFailBtn?.addEventListener('click', () =>
      this.submitTaskAssist('mark_failed')
    );
    this.elements.taskAssistSpecialistActionBtn?.addEventListener('click', () =>
      this.handleAssistSpecialistAction()
    );
    this.elements.taskAssistResponseToggle?.addEventListener('click', () =>
      this.toggleAssistResponseExpanded()
    );
    this.elements.taskAssistAgent?.addEventListener('change', () =>
      this.updateAssistSwitchButtonState()
    );
    window.addEventListener('popstate', () => this.restoreTaskAssistPageFromRoute());
    this.elements.addAgentSubmitBtn?.addEventListener('click', () =>
      this.addSelectedAgentToWorkspace()
    );
    this.elements.createAgentBtn?.addEventListener('click', () => this.openCreateAgentFlow());
    this.elements.addAgentModal?.addEventListener('show.bs.modal', () => {
      this.populateAddAgentOptions();
    });
    this.elements.agentModelSelect?.addEventListener('change', () =>
      this.updateAgentModelSelectionSummary()
    );
    this.elements.agentModelSubmitBtn?.addEventListener('click', () =>
      this.submitAgentModelChange()
    );
    this.elements.agentModelModal?.addEventListener('hidden.bs.modal', () =>
      this.resetAgentModelModal()
    );

    // Make workspace name and description editable
    this.makeEditable(this.elements.workspaceName, 'name', false);
    this.makeEditable(this.elements.workspaceDescription, 'description', true);
    this.elements.workspaceTagsEditBtn?.addEventListener('click', () =>
      this.openWorkspaceTagsEditor()
    );
    this.elements.workspaceTagsSaveBtn?.addEventListener('click', () => this.saveWorkspaceTags());
    this.elements.workspaceTagsCancelBtn?.addEventListener('click', () =>
      this.closeWorkspaceTagsEditor()
    );
    // Esc anywhere inside the editor closes it (the tag widget swallows Esc
    // itself when it is only dismissing its suggestion dropdown).
    this.elements.workspaceTagsEditor?.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        event.preventDefault();
        this.closeWorkspaceTagsEditor();
      }
    });

    // Subscribe to EventBus events for auto-refresh
    console.log('[workspace-detail] EventBus available:', !!window.EventBus);
    if (window.EventBus) {
      console.log('[workspace-detail] Registering EventBus listeners');
      EventBus.on(
        'task:created',
        data => {
          if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
            this.loadTasks();
          }
        },
        'workspaceDetail'
      );

      EventBus.on(
        'task:updated',
        data => {
          if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
            this.loadTasks();
          }
        },
        'workspaceDetail'
      );

      EventBus.on(
        'session:created',
        data => {
          if (!data?.folderId || data.folderId === this.workspaceId) {
            this.loadSessions();
          }
        },
        'workspaceDetail'
      );

      EventBus.on(
        'note:created',
        data => {
          if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
            this.loadNotes();
          }
        },
        'workspaceDetail'
      );

      EventBus.on(
        'note:updated',
        data => {
          console.log(
            '[workspace-detail] note:updated received',
            data?.workspaceId,
            'this.workspaceId:',
            this.workspaceId
          );
          if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
            console.log('[workspace-detail] calling loadNotes');
            this.loadNotes();
          }
        },
        'workspaceDetail'
      );

      EventBus.on(
        'workspace:files:updated',
        data => {
          if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
            this.loadFiles();
          }
        },
        'workspaceDetail'
      );

      EventBus.on(
        'agent:created',
        async () => {
          await this.loadAgentCatalog(true);
          await this.populateAddAgentOptions();
        },
        'workspaceDetail'
      );
    }
  }

  bindDirectoryExplorerEvents() {
    this.directoryExplorer.bindEvents();
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

  async renameWorkspace(newName, folderSlug = '') {
    const payload = { name: newName };
    if (folderSlug) {
      payload.folder_slug = folderSlug;
    }

    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/rename`, {
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
      if (suggestedSlug && window.confirm(buildWorkspaceSlugConflictMessage(result.conflict))) {
        return this.renameWorkspace(newName, suggestedSlug);
      }
      const cancelled = new Error(result?.error || 'Workspace rename cancelled');
      cancelled.cancelled = true;
      throw cancelled;
    }

    if (!response.ok) {
      const message = result?.error || result?.message || 'Failed to rename workspace';
      throw new Error(message);
    }

    return result;
  }

  async saveWorkspaceIdentityField(field, value, options = {}) {
    const key = String(field || '').trim();
    if (key !== 'name' && key !== 'description') return { changed: false };

    const newValue = String(value || '').trim();
    const hasCurrent = Object.prototype.hasOwnProperty.call(options, 'currentValue');
    const currentValue = hasCurrent ? String(options.currentValue || '').trim() : null;
    if (hasCurrent && newValue === currentValue) return { changed: false };

    if (key === 'name' && !newValue) {
      if (window.Toast) window.Toast.error('Name cannot be empty');
      return { changed: false, invalid: true };
    }

    try {
      let result;
      if (key === 'name') {
        result = await this.renameWorkspace(newValue);
      } else {
        result = await this.updateWorkspace({ [key]: newValue });
      }

      const updatedWorkspace = result?.folder || result?.workspace || null;
      if (updatedWorkspace && typeof updatedWorkspace === 'object') {
        this.workspace = {
          ...(this.workspace || {}),
          ...updatedWorkspace
        };
      } else if (this.workspace) {
        this.workspace[key] = newValue;
        if (key === 'description') {
          this.workspace.shared_data =
            this.workspace.shared_data && typeof this.workspace.shared_data === 'object'
              ? this.workspace.shared_data
              : {};
          this.workspace.shared_data.workspace_bootstrap = {
            ...(this.workspace.shared_data.workspace_bootstrap || {}),
            goal: newValue
          };
        }
      }

      await this.renderWorkspaceInfo();
      if (key === 'description') {
        await this.loadNotes();
      }

      if (window.Toast) {
        if (key === 'name') {
          const appliedSlug = String(updatedWorkspace?.folder_slug || '').trim();
          const expectedSlug = slugifyWorkspaceName(newValue);
          window.Toast.success(
            appliedSlug && expectedSlug && appliedSlug !== expectedSlug
              ? `Workspace renamed. Folder saved as "${appliedSlug}".`
              : 'Workspace renamed'
          );
        } else {
          window.Toast.success('Description updated');
        }
      }

      window.workspaceCommand?.refresh();
      return { changed: true, workspace: this.workspace };
    } catch (err) {
      console.error(`Failed to update ${key}:`, err);
      if (!err?.cancelled && window.Toast)
        window.Toast.error(err.message || `Failed to update ${key}`);
      return { changed: false, error: err };
    }
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

      const finishEdit = async save => {
        const newValue = input.value.trim();
        input.remove();
        element.style.display = originalDisplay || '';
        if (editBtn) editBtn.style.display = '';

        if (!save || newValue === currentValue) return;

        await this.saveWorkspaceIdentityField(field, newValue, { currentValue });
      };

      input.addEventListener('blur', () => finishEdit(true));
      input.addEventListener('keydown', evt => {
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
      editBtn.addEventListener('click', e => {
        e.preventDefault();
        e.stopPropagation();
        startEdit();
      });
    }
  }

  getWorkspaceTags(workspace = this.workspace) {
    if (!workspace || !Array.isArray(workspace.tags)) return [];
    return workspace.tags
      .map(tag => String(tag || '').trim())
      .filter(Boolean);
  }

  normalizeWorkspaceTagValue(value) {
    return String(value || '').trim().toLowerCase();
  }

  normalizeWorkspaceTagList(tags) {
    const normalized = [];
    const seen = new Set();
    (Array.isArray(tags) ? tags : []).forEach(tag => {
      const value = this.normalizeWorkspaceTagValue(tag);
      if (!value || seen.has(value)) return;
      seen.add(value);
      normalized.push(value);
    });

    const overlong = normalized.find(tag => Array.from(tag).length > 64);
    if (overlong) {
      return { tags: normalized, error: `"${overlong}" exceeds the 64 character limit.` };
    }
    if (normalized.length > 20) {
      return { tags: normalized, error: 'Workspaces can have at most 20 tags.' };
    }
    return { tags: normalized, error: '' };
  }

  renderWorkspaceTags() {
    const container = this.elements.workspaceTagsContainer;
    const list = this.elements.workspaceTagsList;
    if (!container || !list) return;

    const tags = this.getWorkspaceTags();
    list.innerHTML = tags.length
      ? tags
          .map(
            tag =>
              `<span class="workspace-detail-tag-chip" title="${this.escapeAttribute(tag)}">${this.escapeHtml(tag)}</span>`
          )
          .join('')
      : '<span class="workspace-detail-tag-empty">No tags</span>';

    container.hidden = false;
  }

  openWorkspaceTagsEditor() {
    this.workspaceTagDraft = this.getWorkspaceTags();
    this.setWorkspaceTagsError('');
    this.ensureWorkspaceTagInput();
    this.workspaceTagInput?.setTags(this.workspaceTagDraft);
    if (this.elements.workspaceTagsEditor) {
      this.elements.workspaceTagsEditor.hidden = false;
    }
    this.workspaceTagInput?.focus();
  }

  // Lazily mount the shared tag input widget (suggestions from the unified
  // tag pool) into the editor. Falls back to a no-op when the module failed
  // to load — Save then just submits the unchanged draft.
  ensureWorkspaceTagInput() {
    if (this.workspaceTagInput || !this.elements.workspaceTagsEditorMount) return;
    if (!window.OriTagInput?.createTagInput) return;
    this.workspaceTagInput = window.OriTagInput.createTagInput({
      container: this.elements.workspaceTagsEditorMount,
      initialTags: this.workspaceTagDraft,
      onChange: tags => {
        this.workspaceTagDraft = tags;
        this.setWorkspaceTagsError('');
      }
    });
  }

  closeWorkspaceTagsEditor() {
    this.workspaceTagDraft = this.getWorkspaceTags();
    this.setWorkspaceTagsError('');
    this.workspaceTagInput?.setTags(this.workspaceTagDraft);
    if (this.elements.workspaceTagsEditor) {
      this.elements.workspaceTagsEditor.hidden = true;
    }
  }

  setWorkspaceTagsError(message) {
    const errorEl = this.elements.workspaceTagsError;
    if (!errorEl) return;
    const text = String(message || '').trim();
    errorEl.textContent = text;
    errorEl.hidden = text === '';
  }

  async saveWorkspaceTagList(tags) {
    const result = this.normalizeWorkspaceTagList(tags);
    if (result.error) {
      const validationError = new Error(result.error);
      validationError.validation = true;
      throw validationError;
    }

    const response = await this.updateWorkspace({ tags: result.tags });
    const updatedWorkspace = response?.folder || response?.workspace || {};
    const updatedTags = Array.isArray(updatedWorkspace.tags) ? updatedWorkspace.tags : result.tags;
    this.workspace = {
      ...(this.workspace || {}),
      ...updatedWorkspace,
      tags: updatedTags
    };
    this.renderWorkspaceTags();
    // New tags should show up in suggestions everywhere right away.
    window.OriTagInput?.clearTagPoolCache?.();
    window.workspaceCommand?.refresh();
    if (window.Toast) window.Toast.success('Tags updated');
    return updatedTags;
  }

  async saveWorkspaceTags() {
    if (this.workspaceTagsSaving) return;

    const draft = this.workspaceTagInput ? this.workspaceTagInput.getTags() : this.workspaceTagDraft;

    this.workspaceTagsSaving = true;
    if (this.elements.workspaceTagsSaveBtn) {
      this.elements.workspaceTagsSaveBtn.disabled = true;
    }

    try {
      await this.saveWorkspaceTagList(draft);
      this.closeWorkspaceTagsEditor();
    } catch (error) {
      console.error('Failed to update workspace tags:', error);
      this.setWorkspaceTagsError(error.message || 'Failed to update tags');
    } finally {
      this.workspaceTagsSaving = false;
      if (this.elements.workspaceTagsSaveBtn) {
        this.elements.workspaceTagsSaveBtn.disabled = false;
      }
    }
  }

  /**
   * Load workspace data
   */
  async loadWorkspace() {
    try {
      const response = await fetch(
        `/api/orchestration/workspace?id=${encodeURIComponent(this.workspaceId)}`
      );
      if (!response.ok) throw new Error('Failed to load workspace');

      this.workspace = await response.json();
      await this.loadAvailableSkills().catch(error => {
        console.warn('Failed to load skill catalog for workspace detail:', error);
      });
      this.workspaceSettings = this.normalizeWorkspaceSettings(
        this.workspace?.workspace_settings || this.workspace?.shared_data?.workspace_settings || {}
      );
      this.workspaceSettingsEffectiveBehavior = this.normalizeWorkspaceSettingsEffectiveBehavior(
        this.workspace?.workspace_settings_effective_behavior,
        this.workspaceSettings
      );
      this.workspaceTaskMarkdownStatus =
        this.workspace?.task_markdown_status &&
        typeof this.workspace.task_markdown_status === 'object'
          ? this.workspace.task_markdown_status
          : null;
      if (
        window.OriAskRouting &&
        typeof window.OriAskRouting.refreshWorkspaceIdentity === 'function'
      ) {
        window.OriAskRouting.refreshWorkspaceIdentity({
          workspace_id: this.workspaceId,
          page_path: window.location?.pathname || '',
          surface: window.location?.pathname?.includes('/canvas')
            ? 'workspace_canvas'
            : 'workspace_detail',
          origin: 'ask_ori'
        });
      }
      await this.renderWorkspaceInfo();
      this.syncProjectActionState();
      this.renderWorkspaceMCPBindings();
      this.renderWorkspaceSettings();
      this.renderWorkspaceSkillBindings();
      this.renderWorkspacePluginBindings();
      this.renderAgentGroups();
      this.refreshHomeAssistantQuickPrompts();
      this.renderWorkspaceHealth();
      await this.membersPanel.syncWorkspace(this.workspace);
      // Keep the opt-in Command view in sync once data is loaded/refreshed.
      window.workspaceCommand?.refresh();
    } catch (error) {
      console.error('Failed to load workspace:', error);
      if (window.Toast) window.Toast.error('Failed to load workspace');
      this.renderWorkspaceHealth();
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
    window.hubSupportChat?.setSubtitle?.(this.workspace.name || '');
    if (this.elements.workspaceDescription) {
      if (this.workspace.description) {
        this.elements.workspaceDescription.textContent = this.workspace.description;
        this.elements.workspaceDescription.style.opacity = '1';
      } else {
        this.elements.workspaceDescription.textContent = 'No description';
        this.elements.workspaceDescription.style.opacity = '0.6';
      }
    }
    this.renderWorkspaceTags();
    if (this.elements.workspaceBreadcrumb) {
      this.elements.workspaceBreadcrumb.textContent = this.workspace.name || 'Workspace';
    }
    if (this.elements.openCanvasBtn) {
      this.elements.openCanvasBtn.href = `/workspaces/${this.workspaceId}/canvas`;
    }
    if (this.elements.openDiagnosticsBtn) {
      this.elements.openDiagnosticsBtn.href = `/workspaces/${this.workspaceId}/diagnostics`;
    }

    this.renderWorkspaceWorkflowLinks();
    this.renderWorkspaceIntent();

    // Update stats
    const agents = this.workspace.agent_instances || [];
    if (this.elements.agentCount) {
      this.elements.agentCount.textContent = agents.length;
      this.elements.agentCount.setAttribute('aria-busy', 'false');
      this.elements.agentCount.setAttribute('aria-label', `${agents.length} agents`);
    }

    // Load children workspaces from tree API
    await this.loadChildren();
    this.renderWorkspaceHealth();
  }

  getWorkspaceHealthAgentReferences() {
    if (!this.workspace) return [];

    const seen = new Map();
    const add = (name, { isEntryAgent = false } = {}) => {
      const trimmed = String(name || '').trim();
      if (!trimmed || trimmed.toLowerCase() === 'unassigned') return;
      const key = this.normalizeAgentName(trimmed);
      if (!key) return;

      const existing = seen.get(key);
      if (existing) {
        if (isEntryAgent) {
          existing.isEntryAgent = true;
        }
        return;
      }

      seen.set(key, { name: trimmed, isEntryAgent: Boolean(isEntryAgent) });
    };

    const sharedEntryAgentName = String(this.workspace.shared_data?.entry_agent_name || '').trim();
    add(sharedEntryAgentName, { isEntryAgent: true });
    add(this.workspace.entry_agent_name, { isEntryAgent: true });
    if (Array.isArray(this.workspace.agent_instances)) {
      this.workspace.agent_instances.forEach(instance =>
        add(instance?.name, {
          isEntryAgent: Boolean(instance?.entry_point || instance?.entryPoint)
        })
      );
    }
    if (Array.isArray(this.workspace.agents)) {
      this.workspace.agents.forEach(name => add(name));
    }

    return Array.from(seen.values());
  }

  hasWorkspaceEntryAgentReference() {
    if (!this.workspace) return false;

    if (String(this.workspace.entry_agent_name || '').trim()) return true;
    if (String(this.workspace.shared_data?.entry_agent_name || '').trim()) return true;

    if (Array.isArray(this.workspace.agent_instances)) {
      return this.workspace.agent_instances.some(
        instance =>
          Boolean(instance?.entry_point || instance?.entryPoint) &&
          String(instance?.name || '').trim()
      );
    }

    return false;
  }

  collectWorkspaceHealthIssues() {
    const issues = [];
    const references = this.getWorkspaceHealthAgentReferences();

    if (!this.hasWorkspaceEntryAgentReference()) {
      const defaults = this.buildWorkspaceEntryAgentDefaults();
      const workspaceName = String(this.workspace?.name || '').trim() || 'This workspace';
      issues.push({
        category: 'agent',
        severity: 'error',
        title: 'Entry agent is missing',
        description:
          'This workspace has no entry agent assigned. Create one so chats, routing, and task orchestration have a default manager.',
        action: 'entry_agent',
        actionLabel: 'Create Entry Agent',
        agentName: defaults.seedName || 'Workspace Manager',
        meta: [workspaceName, 'No entry agent']
      });
    }

    if (this.agentCatalogLoadFailed) {
      issues.push({
        category: 'verification',
        severity: 'warning',
        title: 'Agent catalog could not be verified',
        description:
          'The workspace health check could not compare workspace agents against the current runnable agent catalog.',
        meta: ['Agent verification unavailable']
      });
    } else {
      references.forEach(reference => {
        // Resolves to a global runnable agent definition — healthy.
        if (this.getAgentProfile(reference.name)) return;
        // Workspace-local agents (e.g. the auto-created "<Workspace> Manager"
        // entry agent) live as a workspace snapshot rather than in the global
        // agent catalog. The runtime resolver loads them from that snapshot
        // with precedence over global agents, so a snapshot-backed reference
        // is runnable, not broken — treat it as healthy.
        if (this.hasWorkspaceAgentSnapshot(reference.name)) return;
        // Referenced but resolvable nowhere (no global definition and no
        // workspace snapshot) — genuinely missing.
        issues.push({
          category: 'agent',
          severity: 'error',
          title: reference.isEntryAgent ? 'Entry agent is missing' : 'Workspace agent is missing',
          description: reference.isEntryAgent
            ? `"${reference.name}" is still assigned as the workspace entry agent, but the runnable agent definition no longer exists.`
            : `"${reference.name}" is still linked to this workspace, but the runnable agent definition no longer exists.`,
          action: reference.isEntryAgent ? 'entry_agent' : 'agent',
          actionLabel: reference.isEntryAgent ? 'Create Entry Agent' : 'Recreate Agent',
          agentName: reference.name,
          meta: [reference.name, reference.isEntryAgent ? 'Entry agent' : 'Workspace member']
        });
      });
    }

    if (this.filesLoadFailed) {
      issues.push({
        category: 'verification',
        severity: 'warning',
        title: 'Linked files could not be verified',
        description:
          'The workspace health check could not load the current file attachments, so broken file links could not be confirmed.',
        meta: ['File verification unavailable']
      });
    } else if (Array.isArray(this.files)) {
      this.files.forEach(file => {
        const meta = file?.file_meta && typeof file.file_meta === 'object' ? file.file_meta : null;
        if (!meta) return;

        const title =
          String(file?.title || meta?.name || 'Untitled File').trim() || 'Untitled File';
        const hasLinkTarget = [meta.relative_path, meta.original_path, meta.url].some(value =>
          String(value || '').trim()
        );
        const pathLabel = String(meta.relative_path || meta.original_path || meta.url || '').trim();

        if (
          String(meta.status || '')
            .trim()
            .toLowerCase() === 'missing'
        ) {
          issues.push({
            category: 'file',
            severity: 'warning',
            title: 'Linked file is missing',
            description: `"${title}" no longer resolves to a file on disk. Relink it so the workspace can use it again.`,
            action: 'file',
            actionLabel: 'Relink File',
            fileId: String(file?.id || '').trim(),
            meta: [title, pathLabel || 'Missing file']
          });
          return;
        }

        if (!hasLinkTarget) {
          issues.push({
            category: 'file',
            severity: 'warning',
            title: 'File link is incomplete',
            description: `"${title}" has file metadata but no valid link target. Relink it to restore a usable file reference.`,
            action: 'file',
            actionLabel: 'Relink File',
            fileId: String(file?.id || '').trim(),
            meta: [title, 'No file path stored']
          });
        }
      });
    }

    return issues;
  }

  summarizeWorkspaceHealth() {
    const waitingOnSources = !this.workspace || !this.agentCatalogLoaded || !this.filesLoaded;
    if (this.workspaceHealthCheckRunning || waitingOnSources) {
      return {
        status: 'checking',
        badge: 'Checking',
        summary: 'Verifying workspace agent references and linked files...',
        meta: [
          !this.agentCatalogLoaded ? 'Agents: checking' : 'Agents: ready',
          !this.filesLoaded ? 'Files: checking' : 'Files: ready'
        ],
        issues: []
      };
    }

    const issues = this.collectWorkspaceHealthIssues();
    const errorCount = issues.filter(issue => issue.severity === 'error').length;
    const agentIssueCount = issues.filter(issue => issue.category === 'agent').length;
    const fileIssueCount = issues.filter(issue => issue.category === 'file').length;
    const verificationIssueCount = issues.filter(issue => issue.category === 'verification').length;

    if (issues.length === 0) {
      return {
        status: 'healthy',
        badge: 'Healthy',
        summary: 'No missing agents or broken file links were detected in this workspace.',
        meta: ['Agents: healthy', 'Files: healthy'],
        issues: []
      };
    }

    const parts = [];
    if (agentIssueCount > 0) {
      parts.push(`${agentIssueCount} missing agent${agentIssueCount === 1 ? '' : 's'}`);
    }
    if (fileIssueCount > 0) {
      parts.push(`${fileIssueCount} linked file issue${fileIssueCount === 1 ? '' : 's'}`);
    }
    if (verificationIssueCount > 0) {
      parts.push(
        `${verificationIssueCount} verification warning${verificationIssueCount === 1 ? '' : 's'}`
      );
    }
    const attentionVerb = issues.length === 1 ? 'needs' : 'need';

    return {
      status: errorCount > 0 ? 'error' : 'warning',
      badge: errorCount > 0 ? 'Needs Attention' : 'Warnings',
      summary: `${parts.join(' and ')} ${attentionVerb} attention before this workspace is fully reliable.`,
      meta: [
        agentIssueCount > 0
          ? `Agents: ${agentIssueCount} issue${agentIssueCount === 1 ? '' : 's'}`
          : 'Agents: healthy',
        fileIssueCount > 0
          ? `Files: ${fileIssueCount} issue${fileIssueCount === 1 ? '' : 's'}`
          : 'Files: healthy',
        verificationIssueCount > 0
          ? `Checks: ${verificationIssueCount} unavailable`
          : 'Checks: complete'
      ],
      issues
    };
  }

  renderWorkspaceHealthEmptyState() {
    return `
      <div class="workspace-detail-health-empty">
        <div class="workspace-detail-health-empty-icon">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
            <path d="M9,16.17L4.83,12L3.41,13.41L9,19L21,7L19.59,5.59L9,16.17Z"/>
          </svg>
        </div>
        <div>
          <div class="workspace-detail-health-empty-title">Workspace looks healthy</div>
          <p class="workspace-detail-health-empty-copy">All workspace agents resolve to runnable definitions, and every linked file that was checked still points at a usable file target.</p>
        </div>
      </div>
    `;
  }

  renderWorkspaceHealthIssue(issue) {
    const severity = issue?.severity === 'error' ? 'error' : 'warning';
    const iconPath =
      severity === 'error'
        ? '<path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>'
        : '<path d="M13,14H11V9H13M13,18H11V16H13M1,21H23L12,2"/>';
    const meta = Array.isArray(issue?.meta) ? issue.meta.filter(Boolean) : [];
    let actionMarkup = '';

    if (issue?.action === 'entry_agent' && issue?.agentName) {
      actionMarkup = `
        <button type="button"
                class="workspace-detail-panel-btn workspace-detail-health-action"
                onclick="window.workspaceDetail?.openWorkspaceHealthAgentRecovery('${encodeURIComponent(issue.agentName)}', 'entry')">
          ${this.escapeHtml(issue.actionLabel || 'Create Entry Agent')}
        </button>
      `;
    } else if (issue?.action === 'agent' && issue?.agentName) {
      actionMarkup = `
        <button type="button"
                class="workspace-detail-panel-btn workspace-detail-health-action"
                onclick="window.workspaceDetail?.openWorkspaceHealthAgentRecovery('${encodeURIComponent(issue.agentName)}', 'agent')">
          ${this.escapeHtml(issue.actionLabel || 'Recreate Agent')}
        </button>
      `;
    } else if (issue?.action === 'file' && issue?.fileId) {
      actionMarkup = `
        <button type="button"
                class="workspace-detail-panel-btn workspace-detail-health-action"
                onclick="window.workspaceDetail?.openWorkspaceHealthFileRecovery('${this.escapeHtml(issue.fileId)}')">
          ${this.escapeHtml(issue.actionLabel || 'Relink File')}
        </button>
      `;
    }

    return `
      <div class="workspace-detail-health-issue is-${severity}">
        <div class="workspace-detail-health-issue-icon">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">${iconPath}</svg>
        </div>
        <div>
          <div class="workspace-detail-health-issue-title">${this.escapeHtml(issue?.title || 'Workspace issue')}</div>
          <p class="workspace-detail-health-issue-copy">${this.escapeHtml(issue?.description || '')}</p>
          ${
            meta.length > 0
              ? `
            <div class="workspace-detail-health-issue-meta">
              ${meta.map(item => `<span class="workspace-detail-health-issue-pill">${this.escapeHtml(item)}</span>`).join('')}
            </div>
          `
              : ''
          }
        </div>
        ${actionMarkup}
      </div>
    `;
  }

  renderWorkspaceHealth() {
    if (
      !this.elements.healthPanel ||
      !this.elements.healthSummary ||
      !this.elements.healthBadge ||
      !this.elements.healthMeta ||
      !this.elements.healthList
    ) {
      return;
    }

    const health = this.summarizeWorkspaceHealth();
    this.elements.healthSummary.textContent = health.summary;
    this.elements.healthBadge.textContent = health.badge;
    this.elements.healthBadge.className = `workspace-detail-health-badge is-${health.status}`;
    this.elements.healthMeta.innerHTML = Array.isArray(health.meta)
      ? health.meta
          .map(item => `<span class="workspace-detail-health-chip">${this.escapeHtml(item)}</span>`)
          .join('')
      : '';

    if (!Array.isArray(health.issues) || health.issues.length === 0) {
      this.elements.healthList.classList.add('is-empty');
      this.elements.healthList.innerHTML =
        health.status === 'healthy'
          ? this.renderWorkspaceHealthEmptyState()
          : '<div class="workspace-detail-empty">Checking workspace health...</div>';
    } else {
      this.elements.healthList.classList.remove('is-empty');
      this.elements.healthList.innerHTML = health.issues
        .map(issue => this.renderWorkspaceHealthIssue(issue))
        .join('');
    }

    if (this.elements.healthRefreshBtn) {
      this.elements.healthRefreshBtn.disabled = this.workspaceHealthCheckRunning;
    }
    if (this.elements.healthRefreshLabel) {
      this.elements.healthRefreshLabel.textContent = this.workspaceHealthCheckRunning
        ? 'Running...'
        : 'Run Health Check';
    }
    this.renderUISmokeTestResult();
  }

  renderUISmokeTestResult() {
    if (this.elements.uiSmokeTestBtn) {
      this.elements.uiSmokeTestBtn.disabled = this.uiSmokeTestRunning;
    }
    if (this.elements.uiSmokeTestLabel) {
      this.elements.uiSmokeTestLabel.textContent = this.uiSmokeTestRunning
        ? 'Running...'
        : 'Run UI Smoke Test';
    }

    const resultEl = this.elements.uiSmokeTestResult;
    if (!resultEl) return;

    if (this.uiSmokeTestRunning) {
      resultEl.className = 'workspace-detail-ui-smoke-result';
      resultEl.innerHTML = `
        <div class="workspace-detail-ui-smoke-header">
          <div>
            <div class="workspace-detail-ui-smoke-title">UI smoke test running</div>
            <div class="workspace-detail-ui-smoke-summary">Checking the current server routes, scripts, styles, and this workspace page without opening another port.</div>
          </div>
          <span class="workspace-detail-ui-smoke-status">Running</span>
        </div>
      `;
      return;
    }

    const result = this.uiSmokeTestResult;
    if (!result) {
      resultEl.classList.add('d-none');
      resultEl.innerHTML = '';
      return;
    }

    const status = String(result.status || '').toLowerCase() === 'passed' ? 'passed' : 'failed';
    const checks = Array.isArray(result.checks) ? result.checks : [];
    resultEl.className = `workspace-detail-ui-smoke-result is-${status}`;
    resultEl.innerHTML = `
      <div class="workspace-detail-ui-smoke-header">
        <div>
          <div class="workspace-detail-ui-smoke-title">UI smoke test ${status === 'passed' ? 'passed' : 'failed'}</div>
          <div class="workspace-detail-ui-smoke-summary">${this.escapeHtml(result.summary || '')}</div>
        </div>
        <span class="workspace-detail-ui-smoke-status is-${status}">${this.escapeHtml(status)}</span>
      </div>
      <div class="workspace-detail-ui-smoke-checks">
        ${checks.map(check => this.renderUISmokeTestCheck(check)).join('')}
      </div>
    `;
  }

  renderUISmokeTestCheck(check) {
    const status = String(check?.status || '').toLowerCase() === 'passed' ? 'passed' : 'failed';
    const icon = status === 'passed' ? '&#10003;' : '!';
    const statusCode = Number(check?.status_code) || 0;
    const duration = Number(check?.duration_ms) || 0;
    const meta = [statusCode ? `HTTP ${statusCode}` : '', `${duration}ms`]
      .filter(Boolean)
      .join(' | ');

    return `
      <div class="workspace-detail-ui-smoke-check is-${status}">
        <span class="workspace-detail-ui-smoke-check-icon">${icon}</span>
        <div>
          <div class="workspace-detail-ui-smoke-check-name">${this.escapeHtml(check?.name || 'Smoke check')}</div>
          <div class="workspace-detail-ui-smoke-check-detail">${this.escapeHtml(check?.detail || check?.path || '')}</div>
        </div>
        <div class="workspace-detail-ui-smoke-check-meta">${this.escapeHtml(meta)}</div>
      </div>
    `;
  }

  async runUISmokeTest() {
    if (this.uiSmokeTestRunning) return;

    this.uiSmokeTestRunning = true;
    this.uiSmokeTestResult = null;
    this.renderUISmokeTestResult();

    try {
      const response = await fetch('/api/diagnostics/ui-smoke-test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workspace_id: this.workspaceId })
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(result?.error || 'UI smoke test failed');
      }

      this.uiSmokeTestResult = result;
      if (window.Toast) {
        if (result?.status === 'passed') {
          window.Toast.success('UI smoke test passed');
        } else {
          window.Toast.warning('UI smoke test found issues');
        }
      }
    } catch (error) {
      this.uiSmokeTestResult = {
        status: 'failed',
        summary: error?.message || 'UI smoke test failed',
        checks: []
      };
      if (window.Toast) window.Toast.error(error?.message || 'UI smoke test failed');
    } finally {
      this.uiSmokeTestRunning = false;
      this.renderUISmokeTestResult();
    }
  }

  async refreshWorkspaceHealth() {
    if (this.workspaceHealthCheckRunning) return;

    this.workspaceHealthCheckRunning = true;
    this.agentCatalogLoaded = false;
    this.filesLoaded = false;
    this.agentCatalogLoadFailed = false;
    this.filesLoadFailed = false;
    this.renderWorkspaceHealth();

    try {
      await Promise.all([this.loadWorkspace(), this.loadAgentCatalog(true), this.loadFiles()]);
      // Refresh workspace-local agent snapshots too, so a re-check reflects the
      // current set of snapshot-backed agents rather than the init-time copy.
      await this.loadWorkspaceAgentSnapshots();
    } finally {
      this.workspaceHealthCheckRunning = false;
      this.renderWorkspaceHealth();
    }
  }

  openWorkspaceHealthAgentRecovery(encodedAgentName = '', mode = 'agent') {
    let agentName = '';
    try {
      agentName = decodeURIComponent(String(encodedAgentName || ''));
    } catch (_error) {
      agentName = String(encodedAgentName || '');
    }

    const normalizedName = String(agentName || '').trim();
    if (!normalizedName) return;

    if (mode === 'entry') {
      this.openWorkspaceEntryAgentCreateFlow({ seedName: normalizedName });
      return;
    }

    this.openCreateAgentFlow({
      seedName: normalizedName,
      seedType: normalizedName.toLowerCase().includes('manager') ? 'orchestration' : 'tool-calling'
    });
  }

  openWorkspaceHealthFileRecovery(fileId = '') {
    const normalizedFileId = String(fileId || '').trim();
    if (!normalizedFileId) return;
    this.promptRelinkWorkspaceFile(normalizedFileId);
  }

  getWorkspaceIntentBootstrap() {
    const raw = this.workspace?.shared_data?.workspace_bootstrap;
    if (!raw || typeof raw !== 'object') {
      return {};
    }
    return raw;
  }

  getWorkspaceIntentState() {
    const bootstrap = this.getWorkspaceIntentBootstrap();
    const description = String(this.workspace?.description || bootstrap.goal || '').trim();
    return {
      description,
      systems: String(bootstrap.systems || '').trim(),
      capabilities: String(bootstrap.capabilities || '').trim(),
      context: String(bootstrap.context || '').trim()
    };
  }

  getWorkspaceIntentFromForm() {
    return {
      description: String(this.elements.intentDescriptionInput?.value || '').trim(),
      systems: String(this.elements.intentSystemsInput?.value || '').trim(),
      capabilities: String(this.elements.intentCapabilitiesInput?.value || '').trim(),
      context: String(this.elements.intentContextInput?.value || '').trim()
    };
  }

  renderWorkspaceIntent() {
    const intent = this.getWorkspaceIntentState();
    if (this.elements.intentDescriptionInput)
      this.elements.intentDescriptionInput.value = intent.description;
    if (this.elements.intentSystemsInput) this.elements.intentSystemsInput.value = intent.systems;
    if (this.elements.intentCapabilitiesInput)
      this.elements.intentCapabilitiesInput.value = intent.capabilities;
    if (this.elements.intentContextInput) this.elements.intentContextInput.value = intent.context;
    if (this.elements.intentSummary) {
      this.elements.intentSummary.textContent = intent.description
        ? 'Saving this intent updates the workspace description and setup metadata Ori uses for planning, routing, and setup review.'
        : 'Add a workspace description first so Ori has a stable source of truth for planning and setup review.';
    }
  }

  setWorkspaceIntentStatus(message = '', tone = '') {
    if (!this.elements.intentStatus) return;
    this.elements.intentStatus.textContent = String(message || '').trim();
    this.elements.intentStatus.style.color =
      tone === 'error'
        ? 'var(--danger-color, #ef4444)'
        : tone === 'success'
          ? 'var(--success-color, #22c55e)'
          : 'var(--text-secondary)';
  }

  setWorkspaceIntentBusy(isBusy, mode = 'save') {
    if (mode === 'save' && this.elements.intentSaveBtn) {
      this.elements.intentSaveBtn.disabled = Boolean(isBusy);
      this.elements.intentSaveBtn.textContent = isBusy ? 'Saving...' : 'Save Intent';
    }
  }

  buildWorkspaceIntentPayload() {
    const intent = this.getWorkspaceIntentFromForm();
    return {
      description: intent.description,
      workspace_bootstrap: {
        goal: intent.description,
        systems: intent.systems,
        capabilities: intent.capabilities,
        context: intent.context
      }
    };
  }

  async saveWorkspaceIntent(options = {}) {
    const { silent = false, reloadNotes = true } = options;
    const payload = this.buildWorkspaceIntentPayload();
    if (!payload.description) {
      this.setWorkspaceIntentStatus('Workspace description is required.', 'error');
      this.elements.intentDescriptionInput?.focus();
      return false;
    }

    this.setWorkspaceIntentBusy(true, 'save');
    this.setWorkspaceIntentStatus('Saving workspace intent...');

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to save workspace intent');
      }

      const data = await response.json().catch(() => ({}));
      const updatedWorkspace = data?.folder || data?.workspace || null;
      if (updatedWorkspace && typeof updatedWorkspace === 'object') {
        this.workspace = {
          ...(this.workspace || {}),
          ...updatedWorkspace
        };
      } else if (!this.workspace) {
        this.workspace = {};
      }

      if (this.workspace) {
        this.workspace.description = payload.description;
        this.workspace.shared_data =
          this.workspace.shared_data && typeof this.workspace.shared_data === 'object'
            ? this.workspace.shared_data
            : {};
        this.workspace.shared_data.workspace_bootstrap = payload.workspace_bootstrap;
      }

      await this.renderWorkspaceInfo();
      if (reloadNotes) {
        await this.loadNotes();
      }

      this.setWorkspaceIntentStatus('Workspace intent saved and canonical note synced.', 'success');
      if (!silent && window.Toast) {
        window.Toast.success('Workspace intent saved');
      }
      return true;
    } catch (error) {
      console.error('Failed to save workspace intent:', error);
      this.setWorkspaceIntentStatus(error.message || 'Failed to save workspace intent', 'error');
      if (!silent && window.Toast) {
        window.Toast.error(error.message || 'Failed to save workspace intent');
      }
      return false;
    } finally {
      this.setWorkspaceIntentBusy(false, 'save');
    }
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
    // Groups manage members through the dedicated Members panel; the
    // read-only child cards would duplicate it.
    const isGroup = String(this.workspace?.kind || '').trim().toLowerCase() === 'group';

    // Show/hide the children panel based on whether there are children
    if (this.elements.childrenPanel) {
      if (this.children.length > 0 && !isGroup) {
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

    this.elements.childrenList.innerHTML = this.children
      .map(child => {
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
              ${
                hasChildren
                  ? '<path d="M12,3L2,12H5V20H19V12H22L12,3M12,8.75A2.25,2.25 0 0,1 14.25,11A2.25,2.25 0 0,1 12,13.25A2.25,2.25 0 0,1 9.75,11A2.25,2.25 0 0,1 12,8.75M12,15C13.5,15 16.5,15.75 16.5,17.25V18H7.5V17.25C7.5,15.75 10.5,15 12,15Z"/>'
                  : '<path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>'
              }
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
      })
      .join('');
  }

  /**
   * Load tasks for the workspace
   */
  async loadTasks() {
    this.tasksLoading = true;
    this.tasksLoadFailed = false;
    this.renderAgentGroups();

    try {
      const response = await fetch(
        `/api/orchestration/tasks?workspace_id=${encodeURIComponent(this.workspaceId)}`
      );
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
      this.renderWorkspaceConfigSummary();
      this.renderWorkspaceWorkflowLinks();
      this.restoreTaskAssistPageFromRoute();

      if (this.elements.taskCount) {
        this.elements.taskCount.textContent = this.tasks.length;
        this.elements.taskCount.setAttribute('aria-busy', 'false');
        this.elements.taskCount.setAttribute('aria-label', `${this.tasks.length} tasks`);
      }
      this.refreshHomeAssistantQuickPrompts();
      // Keep the opt-in Command view (and its open stat manager) in sync after
      // task mutations, which reload tasks without a full workspace reload.
      window.workspaceCommand?.refresh();
    }
  }

  /**
   * Render tasks grouped by agent
   */
  renderTasks() {
    this.syncTasksTagFilter();
    this.renderAgentGroups();
    this.refreshTaskActivityBadges();
  }

  // Mounts the shared tag filter bar above the task/agent groups and keeps
  // its available tags in sync. The bar hides itself while no task has tags.
  syncTasksTagFilter() {
    const mount = document.getElementById('workspace-detail-tasks-tag-filter');
    if (!mount || !window.OriTagFilterBar?.createTagFilterBar) return;
    if (!this.tasksTagFilterBar) {
      this.tasksTagFilterBar = window.OriTagFilterBar.createTagFilterBar({
        container: mount,
        onChange: () => this.renderTasks()
      });
    }
    this.tasksTagFilterBar.setAvailableTags(window.OriTagFilterBar.collectTags(this.tasks || []));
  }

  // refreshTaskActivityBadges is called after renderTasks() (which rebuilds
  // task DOM nodes from scratch and would otherwise blow away inline activity
  // text). It also drops activity entries for tasks that have left the
  // in_progress state in the meantime.
  refreshTaskActivityBadges() {
    if (this._taskActivity.size === 0) return;
    const runningIds = new Set();
    for (const t of (this.tasks || [])) {
      if (!t) continue;
      if (String(t.status || '').trim().toLowerCase() === 'in_progress') {
        runningIds.add(t.id);
      }
    }
    for (const taskId of Array.from(this._taskActivity.keys())) {
      if (!runningIds.has(taskId)) {
        this._taskActivity.delete(taskId);
        continue;
      }
      this.patchTaskActivityBadge(taskId);
    }
    if (this._taskActivity.size === 0) {
      this.stopTaskActivityTick();
    } else {
      this.ensureTaskActivityTick();
    }
  }

  renderAgentDetailLink(agentName, encodedAgentName) {
    if (!agentName || !encodedAgentName) {
      return `<span>${this.escapeHtml(agentName || '')}</span>`;
    }

    const safeAgentName = this.escapeHtml(agentName);
    const target = this.getAgentDetailTarget(agentName);
    if (!target.interactive) {
      const title = target.title || `${agentName} is managed inside this workspace`;
      return `
      <span class="workspace-detail-agent-link is-static"
            title="${this.escapeAttribute(title)}"
            aria-label="${this.escapeAttribute(target.ariaLabel || title)}">
        <span class="workspace-detail-agent-link-label">${safeAgentName}</span>
      </span>
    `;
    }

    const safeHref = this.escapeAttribute(target.href);
    const safeTitle = this.escapeAttribute(target.title || `Open ${agentName} details`);
    const safeAriaLabel = this.escapeAttribute(target.ariaLabel || target.title || `Open ${agentName} details`);
    return `
      <a href="${safeHref}"
         class="workspace-detail-agent-link"
         data-agent-detail-kind="${this.escapeAttribute(target.kind || 'global')}"
         title="${safeTitle}"
         aria-label="${safeAriaLabel}"
         onclick="event.stopPropagation();">
        <span class="workspace-detail-agent-link-label">${safeAgentName}</span>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M14,3H21V10H19V6.41L12.41,13L11,11.59L17.59,5H14V3M5,5H10V7H7V17H17V14H19V19H5V5Z"/>
        </svg>
      </a>
    `;
  }

  buildWorkspaceAgentRecoveryURL(agentName) {
    const workspaceId = String(this.workspaceId || this.workspace?.id || '').trim();
    if (!workspaceId) return '';

    const params = new URLSearchParams();
    params.set('addAgent', '1');
    const normalizedName = String(agentName || '').trim();
    if (normalizedName) {
      params.set('seedAgentName', normalizedName);
    }
    return `/workspaces/${encodeURIComponent(workspaceId)}?${params.toString()}`;
  }

  getAgentDetailTarget(agentName) {
    const normalizedName = String(agentName || '').trim();
    if (!normalizedName) {
      return {
        kind: 'none',
        href: '',
        interactive: false,
        title: '',
        ariaLabel: ''
      };
    }

    const encodedAgentName = encodeURIComponent(normalizedName);
    // Only agents in the global catalog have a real /agents/<name> detail page.
    // Workspace-local agents (entry/manager agents in config.json) must fall
    // through to the workspace-local branch below — otherwise their name links
    // to a 404 /agents/<name> route.
    if (this.getGlobalAgentProfile(normalizedName)) {
      const title = `Open ${normalizedName} details`;
      return {
        kind: 'global',
        href: `/agents/${encodedAgentName}`,
        interactive: true,
        title,
        ariaLabel: title
      };
    }

    if (this.hasWorkspaceAgentSnapshot(normalizedName)) {
      const title = `${normalizedName} is a workspace-local agent. Global agent details are not available.`;
      return {
        kind: 'workspace-local',
        href: '',
        interactive: false,
        title,
        ariaLabel: title
      };
    }

    if (this.isWorkspaceEntryAgent(normalizedName)) {
      const href = this.buildWorkspaceAgentRecoveryURL(normalizedName);
      if (href) {
        const title = `Create entry agent ${normalizedName}`;
        return {
          kind: 'missing-entry',
          href,
          interactive: true,
          title,
          ariaLabel: title
        };
      }
    }

    const workspaceId = String(this.workspaceId || this.workspace?.id || '').trim();
    if (workspaceId) {
      const title = `Open workspace to repair ${normalizedName}`;
      return {
        kind: 'workspace-reference',
        href: `/workspaces/${encodeURIComponent(workspaceId)}`,
        interactive: true,
        title,
        ariaLabel: title
      };
    }

    const title = `${normalizedName} is referenced by this workspace, but no global detail page is available.`;
    return {
      kind: 'workspace-reference',
      href: '',
      interactive: false,
      title,
      ariaLabel: title
    };
  }

  renderAgentIdentityLink(group, avatar, rolePresentation, summaryId) {
    const safeName = this.escapeHtml(group.name);
    const target = this.getAgentDetailTarget(group.name);
    const title = target.title || `${group.name} is managed inside this workspace`;
    const subtitle = rolePresentation?.label || 'Agent';

    if (!target.interactive) {
      return `
            <div class="workspace-detail-agent-identity-link is-static"
                 data-agent-detail-kind="${this.escapeAttribute(target.kind || 'workspace-local')}"
                 title="${this.escapeAttribute(title)}"
                 aria-label="${this.escapeAttribute(target.ariaLabel || title)}"
                 aria-describedby="${summaryId}">
              <span class="workspace-detail-agent-avatar" style="${avatar.style}" aria-hidden="true">${this.escapeHtml(avatar.initials)}</span>
              <span class="workspace-detail-agent-identity-copy">
                <span class="workspace-detail-agent-name">${safeName}</span>
                <span class="workspace-detail-agent-subtitle">${this.escapeHtml(subtitle)}</span>
              </span>
            </div>
          `;
    }

    return `
            <a href="${this.escapeAttribute(target.href)}"
               class="workspace-detail-agent-identity-link"
               data-agent-detail-kind="${this.escapeAttribute(target.kind || 'global')}"
               title="${this.escapeAttribute(title)}"
               aria-label="${this.escapeAttribute(target.ariaLabel || title)}"
               aria-describedby="${summaryId}"
               onclick="event.stopPropagation();">
              <span class="workspace-detail-agent-avatar" style="${avatar.style}" aria-hidden="true">${this.escapeHtml(avatar.initials)}</span>
              <span class="workspace-detail-agent-identity-copy">
                <span class="workspace-detail-agent-name">${safeName}</span>
                <span class="workspace-detail-agent-subtitle">${this.escapeHtml(subtitle)}</span>
              </span>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                <path d="M14,3H21V10H19V6.41L12.41,13L11,11.59L17.59,5H14V3M5,5H10V7H7V17H17V14H19V19H5V5Z"/>
              </svg>
            </a>
          `;
  }

  getTaskTemplateReference(task) {
    const templateRef = task && typeof task === 'object' ? task.template_ref : null;
    if (!templateRef || typeof templateRef !== 'object') {
      return null;
    }

    const templateId = String(templateRef.template_id || '').trim();
    if (!templateId) {
      return null;
    }

    return {
      templateId,
      templateName: String(templateRef.template_name || '').trim(),
      stepId: String(templateRef.step_id || '').trim(),
      stepName: String(templateRef.step_name || '').trim()
    };
  }

  buildBehaviorStudioHref(templateId) {
    const safeTemplateId = String(templateId || '').trim();
    if (!safeTemplateId) {
      return '/workflows';
    }
    return `/workflows?template=${encodeURIComponent(safeTemplateId)}`;
  }

  updateBehaviorStudioEntryLink(workflows = null) {
    const linkEl = this.elements.openWorkflowsBtn;
    if (!linkEl) {
      return;
    }

    const refs = Array.isArray(workflows) ? workflows : this.collectWorkspaceWorkflowReferences();
    if (refs.length === 1) {
      const workflow = refs[0];
      const label = workflow.templateName || workflow.templateId;
      linkEl.href = this.buildBehaviorStudioHref(workflow.templateId);
      linkEl.title = `Open ${label} in Orchestration Skills`;
      linkEl.setAttribute('aria-label', `Open ${label} in Orchestration Skills`);
      return;
    }

    linkEl.href = '/workflows';
    linkEl.title = 'Open Orchestration Skills';
    linkEl.setAttribute('aria-label', 'Open Orchestration Skills');
  }

  renderTaskWorkflowMeta(task, { isParent = false } = {}) {
    const templateRef = this.getTaskTemplateReference(task);
    if (!templateRef) {
      return '';
    }

    const behaviorLabel = this.escapeHtml(templateRef.templateName || templateRef.templateId);
    const stepLabel =
      !isParent && (templateRef.stepName || templateRef.stepId)
        ? ` · ${this.escapeHtml(templateRef.stepName || templateRef.stepId)}`
        : '';
    const safeHref = this.buildBehaviorStudioHref(templateRef.templateId);
    const title = `Open ${templateRef.templateName || templateRef.templateId} in Orchestration Skills`;

    return `
      <a href="${safeHref}"
         class="workspace-detail-workflow-link workspace-detail-workflow-link--inline"
         title="${this.escapeHtml(title)}"
         aria-label="${this.escapeHtml(title)}"
         onclick="event.stopPropagation();">
        <span class="workspace-detail-workflow-link-label">Skill: ${behaviorLabel}${stepLabel}</span>
        <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M14,3H21V10H19V6.41L12.41,13L11,11.59L17.59,5H14V3M5,5H10V7H7V17H17V14H19V19H5V5Z"/>
        </svg>
      </a>
    `;
  }

  collectWorkspaceWorkflowReferences() {
    const workflowRefs = new Map();

    this.tasks.forEach(task => {
      const templateRef = this.getTaskTemplateReference(task);
      if (!templateRef) {
        return;
      }

      const key = templateRef.templateId;
      if (!workflowRefs.has(key)) {
        workflowRefs.set(key, {
          templateId: templateRef.templateId,
          templateName: templateRef.templateName || templateRef.templateId,
          taskIds: new Set(),
          workflowInstanceIds: new Set()
        });
      }

      const entry = workflowRefs.get(key);
      if (templateRef.templateName && !entry.templateName) {
        entry.templateName = templateRef.templateName;
      }
      entry.taskIds.add(String(task.id || '').trim());
      entry.workflowInstanceIds.add(String(task.parent_task_id || task.id || '').trim());
    });

    return Array.from(workflowRefs.values())
      .map(entry => ({
        templateId: entry.templateId,
        templateName: entry.templateName || entry.templateId,
        taskCount: entry.taskIds.size,
        workflowCount: entry.workflowInstanceIds.size
      }))
      .sort((left, right) => left.templateName.localeCompare(right.templateName));
  }

  renderWorkspaceWorkflowLinks() {
    const panel = this.elements.workflowLinksPanel;
    const list = this.elements.workflowLinksList;
    const count = this.elements.workflowLinksCount;
    const workflows = this.collectWorkspaceWorkflowReferences();
    this.updateBehaviorStudioEntryLink(workflows);

    if (!panel || !list || !count) {
      return;
    }
    count.textContent = String(workflows.length);

    if (workflows.length === 0) {
      panel.style.display = 'none';
      list.innerHTML = '';
      return;
    }

    list.innerHTML = workflows
      .map(workflow => {
        const safeHref = this.buildBehaviorStudioHref(workflow.templateId);
        const workflowLabel = this.escapeHtml(workflow.templateName || workflow.templateId);
        const taskLabel = `${workflow.taskCount} task${workflow.taskCount === 1 ? '' : 's'}`;
        const instanceLabel = `${workflow.workflowCount} run${workflow.workflowCount === 1 ? '' : 's'}`;
        const title = `Open ${workflow.templateName || workflow.templateId} in Orchestration Skills`;

        return `
        <a href="${safeHref}"
           class="workspace-detail-workflow-link workspace-detail-workflow-chip"
           title="${this.escapeHtml(title)}"
           aria-label="${this.escapeHtml(title)}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M7,5A2,2 0 0,0 5,7V10A2,2 0 0,0 7,12H10A2,2 0 0,0 12,10V7A2,2 0 0,0 10,5H7M14,5A2,2 0 0,0 12,7V10A2,2 0 0,0 14,12H17A2,2 0 0,0 19,10V7A2,2 0 0,0 17,5H14M7,12A2,2 0 0,0 5,14V17A2,2 0 0,0 7,19H10A2,2 0 0,0 12,17V14A2,2 0 0,0 10,12H7M13,13H19V15H13V13M13,17H19V19H13V17Z"/>
          </svg>
          <span class="workspace-detail-workflow-chip-copy">
            <span class="workspace-detail-workflow-chip-title">${workflowLabel}</span>
            <span class="workspace-detail-workflow-chip-meta">${this.escapeHtml(instanceLabel)} · ${this.escapeHtml(taskLabel)}</span>
          </span>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M14,3H21V10H19V6.41L12.41,13L11,11.59L17.59,5H14V3M5,5H10V7H7V17H17V14H19V19H5V5Z"/>
          </svg>
        </a>
      `;
      })
      .join('');

    panel.style.display = '';
  }

  isWorkspaceEntryAgent(agentName) {
    const key = this.normalizeAgentName(agentName);
    if (!key || !this.workspace) return false;

    const directEntryAgentName = String(this.workspace.entry_agent_name || '').trim();
    if (directEntryAgentName && this.normalizeAgentName(directEntryAgentName) === key) {
      return true;
    }

    const sharedEntryAgentName = String(this.workspace.shared_data?.entry_agent_name || '').trim();
    if (sharedEntryAgentName && this.normalizeAgentName(sharedEntryAgentName) === key) {
      return true;
    }

    if (Array.isArray(this.workspace.agent_instances)) {
      return this.workspace.agent_instances.some(
        instance =>
          Boolean(instance?.entry_point || instance?.entryPoint) &&
          this.normalizeAgentName(instance?.name) === key
      );
    }

    return false;
  }

  renderWorkspaceAgentRoleBadge(agentName) {
    if (!this.isWorkspaceEntryAgent(agentName)) return '';
    return '<span class="workspace-detail-agent-role-badge">Entry Agent</span>';
  }

  getAgentGroupRolePresentation(group) {
    const roles = Array.isArray(group?.roles)
      ? group.roles
          .map(role => String(role || '').trim())
          .filter(Boolean)
      : [];
    const uniqueRoles = [];
    const seen = new Set();
    roles.forEach(role => {
      const key = role.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      uniqueRoles.push(role);
    });

    if (uniqueRoles.length === 0) {
      return { label: 'Agent', detail: 'Agent', roles: [] };
    }
    if (uniqueRoles.length === 1) {
      return { label: uniqueRoles[0], detail: uniqueRoles[0], roles: uniqueRoles };
    }
    return {
      label: 'Multiple roles',
      detail: uniqueRoles.join(', '),
      roles: uniqueRoles
    };
  }

  getAgentAvatarPresentation(agentName) {
    const normalizedName = String(agentName || '').trim();
    const key = this.normalizeAgentName(normalizedName);
    const words = normalizedName
      .split(/[\s._-]+/)
      .map(part => part.trim())
      .filter(Boolean);
    const initials = (words.length > 1 ? `${words[0][0]}${words[words.length - 1][0]}` : words[0]?.slice(0, 2) || 'A')
      .toUpperCase()
      .replace(/[^A-Z0-9]/g, '')
      .slice(0, 2) || 'A';

    let hash = 0;
    for (let index = 0; index < key.length; index += 1) {
      hash = (hash * 31 + key.charCodeAt(index)) >>> 0;
    }

    const hue = hash % 360;
    return {
      initials,
      hue,
      style: `--agent-avatar-hue: ${hue};`,
      label: `${normalizedName || 'Agent'} avatar`
    };
  }

  getAgentModelPresentation(profile) {
    const model = String(profile?.model || '').trim();
    if (!model) {
      return { model: '', tier: '', label: 'Model not set', empty: true };
    }

    const normalized = model.toLowerCase();
    let tier = '';
    if (normalized.includes('opus')) {
      tier = 'High capacity';
    } else if (normalized.includes('sonnet')) {
      tier = 'Balanced';
    } else if (normalized.includes('haiku')) {
      tier = 'Fast';
    }

    return {
      model,
      tier,
      label: tier ? `${model} · ${tier}` : model,
      empty: false
    };
  }

  getAgentRosterStatus(agentName) {
    const key = this.normalizeAgentName(agentName);
    if (!key || !Array.isArray(this.tasks)) {
      return { key: 'idle', label: 'Idle', detail: 'No active tasks' };
    }

    let hasWaiting = false;
    for (const task of this.tasks) {
      if (!task) continue;
      if (this.normalizeAgentName(task.to) !== key) continue;

      const status = String(task.status || '').trim().toLowerCase();
      if (status === 'in_progress') {
        return { key: 'working', label: 'Working', detail: 'Task in progress' };
      }
      if (status === 'waiting_for_choice') {
        hasWaiting = true;
      }
    }

    if (hasWaiting) {
      return { key: 'needs-input', label: 'Needs input', detail: 'Waiting for user input' };
    }
    return { key: 'idle', label: 'Idle', detail: 'No active tasks' };
  }

  getAgentSkillSummary(agentName, limit = 3) {
    const skillNames = this.getEffectiveWorkspaceSkillNamesForAgent(agentName);
    const visible = skillNames.slice(0, limit);
    return {
      names: skillNames,
      visible,
      overflow: Math.max(0, skillNames.length - visible.length),
      count: skillNames.length,
      label: `${skillNames.length} skill${skillNames.length === 1 ? '' : 's'}`
    };
  }

  renderAgentSkillSummary(agentName) {
    const summary = this.getAgentSkillSummary(agentName);
    const key = this.normalizeAgentName(agentName);
    if (summary.count === 0) {
      return `<span class="workspace-detail-agent-skill-summary" data-agent-skill-summary-key="${this.escapeHtml(key)}" aria-label="0 skills"><span class="workspace-detail-agent-skill-empty">No skills</span></span>`;
    }

    const chips = summary.visible
      .map(skill => `<span class="workspace-detail-agent-skill-chip">${this.escapeHtml(skill)}</span>`)
      .join('');
    const overflow =
      summary.overflow > 0
        ? `<span class="workspace-detail-agent-skill-chip overflow">+${summary.overflow}</span>`
        : '';
    return `
      <span class="workspace-detail-agent-skill-summary" data-agent-skill-summary-key="${this.escapeHtml(key)}" aria-label="${this.escapeHtml(summary.label)}">
        ${chips}
        ${overflow}
      </span>
    `;
  }

  renderAgentRosterStatusChip(status) {
    const safeKey = this.escapeHtml(status?.key || 'idle');
    const label = this.escapeHtml(status?.label || 'Idle');
    return `<span class="workspace-detail-agent-status-chip is-${safeKey}">${label}</span>`;
  }

  renderAgentRoleClassChip(rolePresentation) {
    const label = rolePresentation?.label || 'Agent';
    const title = rolePresentation?.detail || label;
    return `<span class="workspace-detail-agent-class-chip" title="${this.escapeHtml(title)}">${this.escapeHtml(label)}</span>`;
  }

  renderAgentModelPowerBadge(agentName, profile, encodedAgentName = '') {
    const presentation = this.getAgentModelPresentation(profile);
    const editable = this.agentAllowsModelEditing(profile);
    const title = presentation.empty ? `Set model for ${agentName}` : `Change model for ${agentName}`;
    const badgeClass = `workspace-detail-agent-model-badge${editable ? ' is-editable' : ''}${presentation.empty ? ' is-empty' : ''}`;
    const modelMarkup = presentation.empty
      ? '<span>Model not set</span>'
      : `
        <span class="workspace-detail-agent-model-name">${this.escapeHtml(presentation.model)}</span>
        ${presentation.tier ? `<span class="workspace-detail-agent-model-tier">${this.escapeHtml(presentation.tier)}</span>` : ''}
      `;

    if (!editable) {
      return `<span class="${badgeClass}" title="${this.escapeHtml(presentation.label)}">${modelMarkup}</span>`;
    }

    return `
      <button type="button"
              class="${badgeClass}"
              title="${this.escapeHtml(title)}"
              aria-label="${this.escapeHtml(title)}"
              onclick="event.stopPropagation(); window.workspaceDetail?.openAgentModelModal('${encodedAgentName}')">
        ${modelMarkup}
        <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M14.06,9.02L14.98,9.94L5.92,19H5V18.08M17.66,3C17.4,3 17.15,3.1 16.95,3.29L15.13,5.11L18.88,8.86L20.7,7.04C21.09,6.65 21.09,6 20.7,5.63L18.37,3.29C18.17,3.1 17.92,3 17.66,3Z"/>
        </svg>
      </button>
    `;
  }

  buildAgentCardSummary(group, rolePresentation, modelPresentation, skillSummary, status) {
    const role = rolePresentation?.detail || rolePresentation?.label || 'Agent';
    const model = modelPresentation?.label || 'Model not set';
    const skills = skillSummary?.label || '0 skills';
    const statusLabel = status?.label || 'Idle';
    const taskCount = Number(group?.tasks?.length || 0);
    const taskLabel = `${taskCount} task${taskCount === 1 ? '' : 's'}`;
    return `${role}. ${model}. ${skills}. ${statusLabel}. ${taskLabel}.`;
  }

  renderAgentGroups() {
    if (!this.elements.agentsList) return;

    const groups = this.buildAgentGroups();
    if (groups.length === 0) {
      if (this.tasksLoading) {
        this.elements.agentsList.innerHTML =
          '<div class="workspace-detail-loading">Loading agents...</div>';
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

    this.elements.agentsList.innerHTML = groups
      .map(group => {
        const taskCount = group.tasks.length;
        const taskLabel = `${taskCount} task${taskCount === 1 ? '' : 's'}`;
        const instanceLabel = group.instanceCount > 1 ? `${group.instanceCount} instances` : '';
        const cardMeta = [instanceLabel, taskLabel].filter(Boolean).join(' · ');
        const capabilityBadges = group.isUnassigned
          ? ''
          : this.renderAgentCapabilityBadges(group.name);
        const agentProfile = group.isUnassigned ? null : this.getAgentProfile(group.name);
        const encodedAgentName = encodeURIComponent(group.name);
        const modelLabel = group.isUnassigned
          ? ''
          : this.renderAgentModelPowerBadge(group.name, agentProfile, encodedAgentName);
        const rolePresentation = group.isUnassigned
          ? { label: 'Unassigned', detail: 'Tasks not assigned to an agent', roles: [] }
          : this.getAgentGroupRolePresentation(group);
        const modelPresentation = group.isUnassigned
          ? { label: '', model: '', tier: '', empty: true }
          : this.getAgentModelPresentation(agentProfile);
        const skillSummary = group.isUnassigned
          ? { count: 0, label: '0 skills', visible: [], overflow: 0, names: [] }
          : this.getAgentSkillSummary(group.name);
        const status = group.isUnassigned
          ? { key: 'idle', label: 'Unassigned', detail: 'Tasks awaiting assignment' }
          : this.getAgentRosterStatus(group.name);
        const avatar = group.isUnassigned
          ? { initials: '?', style: '--agent-avatar-hue: 210;', label: 'Unassigned tasks' }
          : this.getAgentAvatarPresentation(group.name);
        const summaryId = `agent-card-summary-${String(group.key || 'agent').replace(/[^a-z0-9_-]+/gi, '-')}`;
        const cardSummary = group.isUnassigned
          ? `${taskLabel}. Tasks awaiting assignment.`
          : this.buildAgentCardSummary(
              group,
              rolePresentation,
              modelPresentation,
              skillSummary,
              status
            );
        const roleClassChip = group.isUnassigned
          ? ''
          : this.renderAgentRoleClassChip(rolePresentation);
        const skillMarkup = group.isUnassigned ? '' : this.renderAgentSkillSummary(group.name);
        const statusChip = this.renderAgentRosterStatusChip(status);
        const canFlip = !group.isUnassigned;
        const isFlipped = canFlip && this.flippedAgentCards.has(group.key);
        const roleBadge = group.isUnassigned ? '' : this.renderWorkspaceAgentRoleBadge(group.name);
        const removeLabel =
          group.instanceCount > 1
            ? `Remove all ${group.instanceCount} ${group.name} instances from workspace`
            : `Remove ${group.name} from workspace`;
        const removeButton =
          group.isWorkspaceAgent &&
          !group.isUnassigned &&
          !this.isWorkspaceEntryAgent(group.name)
            ? `
        <button type="button"
                class="workspace-detail-agent-remove-btn"
                title="${this.escapeHtml(removeLabel)}"
                aria-label="${this.escapeHtml(removeLabel)}"
                onclick="event.stopPropagation(); window.workspaceDetail?.removeAgentFromWorkspace('${encodedAgentName}')">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
      `
            : '';
        const instanceChip =
          group.instanceCount > 1
            ? `<span class="workspace-detail-agent-instance-tag">${group.instanceCount}x</span>`
            : '';
        const frontFlipButton = canFlip
          ? `
        <button type="button"
                class="workspace-detail-agent-flip-btn"
                title="Show ${this.escapeHtml(group.name)} info"
                aria-label="Show ${this.escapeHtml(group.name)} info"
                onclick="event.stopPropagation(); window.workspaceDetail?.toggleAgentCardFlip('${encodedAgentName}')">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
          </svg>
        </button>
      `
          : '';
        const taskActionButton = group.isUnassigned
          ? ''
          : `
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
        const leaderClass = !group.isUnassigned && this.isWorkspaceEntryAgent(group.name) ? ' is-leader' : '';
        const unassignedClass = group.isUnassigned ? ' is-unassigned' : '';
        const detailLink = group.isUnassigned
          ? `
            <div class="workspace-detail-agent-identity-link is-static">
              <span class="workspace-detail-agent-avatar" style="${avatar.style}" aria-hidden="true">${this.escapeHtml(avatar.initials)}</span>
              <span class="workspace-detail-agent-identity-copy">
                <span class="workspace-detail-agent-name">${this.escapeHtml(group.name)}</span>
                <span class="workspace-detail-agent-subtitle">Awaiting assignment</span>
              </span>
            </div>
          `
          : this.renderAgentIdentityLink(group, avatar, rolePresentation, summaryId);

        return `
      <section class="workspace-detail-agent-card${flippedClass}${leaderClass}${unassignedClass}" data-agent-name="${this.escapeHtml(group.name)}" data-agent-key="${this.escapeHtml(group.key)}">
        <div class="workspace-detail-agent-card-flipper">
          <div class="workspace-detail-agent-card-face workspace-detail-agent-card-face-front">
            <div id="${summaryId}" class="sr-only">${this.escapeHtml(cardSummary)}</div>
            <div class="workspace-detail-agent-card-header">
              <div class="workspace-detail-agent-card-title">
                ${detailLink}
              </div>
              <div class="workspace-detail-agent-card-meta-wrap">
                <div class="workspace-detail-agent-card-meta">${cardMeta}</div>
                ${removeButton}
                ${frontFlipButton}
              </div>
            </div>
            <div class="workspace-detail-agent-roster-panel">
              <div class="workspace-detail-agent-roster-row">
                ${roleClassChip}
                ${roleBadge}
                ${instanceChip}
                ${statusChip}
              </div>
              <div class="workspace-detail-agent-roster-row">
                ${modelLabel}
              </div>
              <div class="workspace-detail-agent-roster-row workspace-detail-agent-skills-row">
                ${skillMarkup}
              </div>
              ${capabilityBadges ? `<div class="workspace-detail-agent-roster-row workspace-detail-agent-capability-row">${capabilityBadges}</div>` : ''}
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
      })
      .join('');
  }

  renderAgentModelBadge(agentName, profile, encodedAgentName = '') {
    const model = String(profile?.model || '').trim();
    const editable = this.agentAllowsModelEditing(profile);
    const label = model || 'Set model';
    const badgeClass = `workspace-detail-agent-model-badge${editable ? ' is-editable' : ''}${model ? '' : ' is-empty'}`;
    const title = model ? `Change model for ${agentName}` : `Set model for ${agentName}`;

    if (!editable) {
      return model ? `<span class="${badgeClass}">${this.escapeHtml(model)}</span>` : '';
    }

    return `
      <button type="button"
              class="${badgeClass}"
              title="${this.escapeHtml(title)}"
              aria-label="${this.escapeHtml(title)}"
              onclick="event.stopPropagation(); window.workspaceDetail?.openAgentModelModal('${encodedAgentName}')">
        <span>${this.escapeHtml(label)}</span>
        <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M14.06,9.02L14.98,9.94L5.92,19H5V18.08M17.66,3C17.4,3 17.15,3.1 16.95,3.29L15.13,5.11L18.88,8.86L20.7,7.04C21.09,6.65 21.09,6 20.7,5.63L18.37,3.29C18.17,3.1 17.92,3 17.66,3Z"/>
        </svg>
      </button>
    `;
  }

  agentAllowsModelEditing(profile) {
    if (!profile) return false;
    return (
      String(profile.source || 'user')
        .trim()
        .toLowerCase() !== 'cli'
    );
  }

  renderAgentBackFace(group, cardMeta, encodedAgentName) {
    const roleBadge = this.renderWorkspaceAgentRoleBadge(group.name);
    const rolePresentation = this.getAgentGroupRolePresentation(group);
    const status = this.getAgentRosterStatus(group.name);

    const skillsMarkup = this.renderAgentWorkspaceSkillChips(group.name);

    const mcpServers = this.getEffectiveWorkspaceMCPServerNames(group.name);
    const mcpMarkup =
      mcpServers.length > 0
        ? mcpServers
            .map(
              server =>
                `<span class="workspace-detail-agent-info-chip mcp">${this.escapeHtml(server)}</span>`
            )
            .join('')
        : '<span class="workspace-detail-agent-info-empty">No MCP servers attached.</span>';
    const removeLabel =
      group.instanceCount > 1
        ? `Remove all ${group.instanceCount} ${group.name} instances from workspace`
        : `Remove ${group.name} from workspace`;
    const removeButton =
      group.isWorkspaceAgent &&
      !group.isUnassigned &&
      !this.isWorkspaceEntryAgent(group.name)
        ? `
      <button type="button"
              class="workspace-detail-agent-remove-btn"
              title="${this.escapeHtml(removeLabel)}"
              aria-label="${this.escapeHtml(removeLabel)}"
              onclick="event.stopPropagation(); window.workspaceDetail?.removeAgentFromWorkspace('${encodedAgentName}')">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
        </svg>
      </button>
    `
        : '';

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
            <div class="workspace-detail-agent-info-label">Status</div>
            <div class="workspace-detail-agent-info-value">${this.escapeHtml(status.label)}</div>
          </div>
          <div class="workspace-detail-agent-info-block">
            <div class="workspace-detail-agent-info-label">Skills</div>
            <div class="workspace-detail-agent-chip-list workspace-detail-agent-skills-list" data-agent-skills-key="${this.escapeHtml(group.key)}">${skillsMarkup}</div>
          </div>
          <div class="workspace-detail-agent-info-block">
            <div class="workspace-detail-agent-info-label">Role</div>
            <div class="workspace-detail-agent-info-value">${this.escapeHtml(rolePresentation.detail)}</div>
          </div>
          <div class="workspace-detail-agent-info-block">
            <div class="workspace-detail-agent-info-label">MCP Attached</div>
            <div class="workspace-detail-agent-chip-list">${mcpMarkup}</div>
          </div>
        </div>
      </div>
    `;
  }

  // Renders the agent info card's SKILLS chips from the skills that are
  // effective for this agent *in this workspace* (workspace skill bindings the
  // agent can use), matching the sibling "MCP Attached" block. This is
  // intentionally workspace-scoped rather than the agent's global skill catalog
  // returned by /api/skills.
  renderAgentWorkspaceSkillChips(agentName) {
    const skillNames = this.getEffectiveWorkspaceSkillNamesForAgent(agentName);
    if (skillNames.length === 0) {
      return '<span class="workspace-detail-agent-info-empty">No workspace skills attached.</span>';
    }
    return skillNames
      .map(
        skill =>
          `<span class="workspace-detail-agent-info-chip skill">${this.escapeHtml(skill)}</span>`
      )
      .join('');
  }

  getAgentCardElementByKey(agentKey) {
    if (!this.elements?.agentsList || !agentKey) return null;
    const cards = this.elements.agentsList.querySelectorAll(
      '.workspace-detail-agent-card[data-agent-key]'
    );
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

    const skillsMarkup = this.renderAgentWorkspaceSkillChips(agentName);

    const skillContainers = card.querySelectorAll(
      '.workspace-detail-agent-skills-list[data-agent-skills-key]'
    );
    let updated = false;
    skillContainers.forEach(container => {
      if (container.getAttribute('data-agent-skills-key') === key) {
        container.innerHTML = skillsMarkup;
        updated = true;
      }
    });

    const summaryMarkup = this.renderAgentSkillSummary(agentName);
    const summaryContainers = card.querySelectorAll(
      '.workspace-detail-agent-skill-summary[data-agent-skill-summary-key]'
    );
    summaryContainers.forEach(container => {
      if (container.getAttribute('data-agent-skill-summary-key') === key) {
        const wrapper = document.createElement('span');
        wrapper.innerHTML = summaryMarkup.trim();
        const replacement = wrapper.firstElementChild;
        if (replacement) {
          container.replaceWith(replacement);
          updated = true;
        }
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
    // The agent info card's SKILLS now renders synchronously from
    // workspace-effective skill bindings (see renderAgentWorkspaceSkillChips),
    // so flipping no longer needs to fetch the agent's global skill catalog.
  }

  async loadAgentSkills(agentName, options = {}) {
    const normalizedName = String(agentName || '').trim();
    const key = this.normalizeAgentName(normalizedName);
    if (!normalizedName || !key) return { status: 'idle', skills: [] };

    const existing = this.agentSkillsCache.get(key);
    if (
      existing?.status === 'loaded' ||
      existing?.status === 'conflict' ||
      existing?.status === 'error'
    ) {
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
              .map(skill => ({
                name: String(skill?.name || '').trim(),
                description: String(skill?.description || '').trim(),
                enabled: skill?.enabled !== false,
                requiredMCPServers: Array.isArray(skill?.required_mcp_servers)
                  ? skill.required_mcp_servers
                      .map(value => String(value || '').trim())
                      .filter(Boolean)
                  : [],
                allowedTools: Array.isArray(skill?.allowed_tools)
                  ? skill.allowed_tools.map(value => String(value || '').trim()).filter(Boolean)
                  : []
              }))
              .filter(skill => skill.name)
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
            roles: [],
            tasks: []
          };
        groupByKey.set(normalized, group);
        groups.push(group);
      } else if (isWorkspaceAgent) {
        group.isWorkspaceAgent = true;
      }

      return group;
    };

    if (
      Array.isArray(this.workspace?.agent_instances) &&
      this.workspace.agent_instances.length > 0
    ) {
      this.workspace.agent_instances.forEach(instance => {
        const group = ensureGroup(instance?.name, { isWorkspaceAgent: true, isUnassigned: false });
        if (group) {
          group.instanceCount += 1;
          const role = String(instance?.role || '').trim();
          if (role) {
            group.roles.push(role);
          }
        }
      });
    } else {
      this.getWorkspaceAgentNames().forEach(name => {
        const group = ensureGroup(name, { isWorkspaceAgent: true, isUnassigned: false });
        if (group) {
          group.instanceCount = Math.max(1, group.instanceCount);
        }
      });
    }

    let topLevelTasks = Array.isArray(this.tasks)
      ? this.tasks.filter(task => !task.parent_task_id)
      : [];

    // Tag filter: keep a parent visible when it or any of its subtasks
    // carries every selected tag.
    const activeTaskTags = this.tasksTagFilterBar ? this.tasksTagFilterBar.getActiveTags() : [];
    if (activeTaskTags.length > 0 && window.OriTagFilterBar) {
      const matchesTags = task =>
        window.OriTagFilterBar.matchesActiveTags(
          Array.isArray(task?.tags) ? task.tags : [],
          activeTaskTags
        );
      topLevelTasks = topLevelTasks.filter(task => {
        if (matchesTags(task)) return true;
        return this.tasks.some(
          subtask => subtask.parent_task_id === task.id && matchesTags(subtask)
        );
      });
    }

    topLevelTasks.forEach(task => {
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

    groups.forEach(group => {
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
    return tasks.map(task => this.renderTaskGroup(task)).join('');
  }

  getSortedSubtasks(parentId) {
    const normalizedParentId = String(parentId || '').trim();
    if (!normalizedParentId || !Array.isArray(this.tasks)) return [];
    return this.tasks
      .filter(item => String(item?.parent_task_id || '').trim() === normalizedParentId)
      .sort((a, b) => {
        const aIndex =
          Number.isFinite(a?.subtask_index) && a.subtask_index > 0
            ? a.subtask_index
            : Number.MAX_SAFE_INTEGER;
        const bIndex =
          Number.isFinite(b?.subtask_index) && b.subtask_index > 0
            ? b.subtask_index
            : Number.MAX_SAFE_INTEGER;
        if (aIndex !== bIndex) return aIndex - bIndex;
        const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
        const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
        if (aTime !== bTime) return aTime - bTime;
        return String(a?.id || '').localeCompare(String(b?.id || ''));
      });
  }

  renderTaskGroup(task) {
    const subtasks = this.getSortedSubtasks(task.id);
    if (subtasks.length === 0) {
      return this.renderTaskItem(task);
    }

    const isCollapsed = this.collapsedWorkflows.has(task.id);
    const groupClasses = ['workspace-detail-workflow-group'];
    if (isCollapsed) groupClasses.push('is-collapsed');
    const subtaskMarkup = subtasks
      .map((subtask, index) => {
        const stepNumber =
          Number.isFinite(subtask?.subtask_index) && subtask.subtask_index > 0
            ? subtask.subtask_index
            : index + 1;
        return this.renderTaskItem(subtask, { isSubtask: true, stepNumber });
      })
      .join('');

    return `
      <div class="${groupClasses.join(' ')}" data-workflow-id="${this.escapeHtml(task.id)}">
        ${this.renderTaskItem(task, { isParent: true, subtaskCount: subtasks.length, collapsed: isCollapsed })}
        <div class="workspace-detail-subtask-list" ${isCollapsed ? 'hidden' : ''}>
          ${subtaskMarkup}
        </div>
      </div>
    `;
  }

  toggleWorkflowExpansion(taskId) {
    const id = String(taskId || '').trim();
    if (!id) return;
    if (this.collapsedWorkflows.has(id)) {
      this.collapsedWorkflows.delete(id);
    } else {
      this.collapsedWorkflows.add(id);
    }
    this.renderAgentGroups();
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
    return sessions.map(session => this.renderSessionItem(session)).join('');
  }

  getTaskScheduleIndicatorData(task) {
    if (!task || task.schedule == null) return null;

    const isActive = task.schedule_enabled !== false;
    const scheduleName = typeof task.schedule_name === 'string' ? task.schedule_name.trim() : '';
    const title = scheduleName
      ? `${isActive ? 'Scheduled task' : 'Scheduled task (paused)'}: ${scheduleName}`
      : isActive
        ? 'Scheduled task'
        : 'Scheduled task (paused)';

    return { isActive, title };
  }

  renderTaskScheduleIndicator(task, variant = 'default') {
    const scheduleInfo = this.getTaskScheduleIndicatorData(task);
    if (!scheduleInfo) return '';

    const stateClass = scheduleInfo.isActive ? 'active' : 'paused';
    const variantClass =
      variant === 'board' ? 'workspace-detail-task-schedule-indicator-board' : '';
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

  renderTaskReferenceURLIndicator(task, variant = 'default') {
    const referenceURL = String(task?.reference_url || '').trim();
    if (!referenceURL) return '';

    const variantClass =
      variant === 'board' ? 'workspace-detail-task-reference-indicator-board' : '';
    const classes = ['workspace-detail-task-reference-indicator', variantClass]
      .filter(Boolean)
      .join(' ');
    const title = `Reference URL: ${referenceURL}`;
    return `
      <a href="${this.escapeHtml(referenceURL)}"
         class="${classes}"
         target="_blank"
         rel="noopener noreferrer"
         title="${this.escapeHtml(title)}"
         aria-label="${this.escapeHtml(title)}"
         onclick="event.stopPropagation();">
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M10.59,13.41C10.21,13.79 10,14.3 10,14.83C10,15.36 10.21,15.87 10.59,16.24C11.36,17.02 12.64,17.02 13.41,16.24L16.95,12.71C17.73,11.93 17.73,10.66 16.95,9.88C16.17,9.1 14.91,9.1 14.12,9.88L13.41,10.59L12,9.17L12.71,8.46C14.27,6.9 16.8,6.9 18.36,8.46C19.92,10.02 19.92,12.55 18.36,14.12L14.83,17.66C13.27,19.22 10.74,19.22 9.17,17.66C7.61,16.1 7.61,13.57 9.17,12L9.88,11.29L11.29,12.71L10.59,13.41M13.41,10.59C13.79,10.21 14,9.7 14,9.17C14,8.64 13.79,8.13 13.41,7.76C12.64,6.98 11.36,6.98 10.59,7.76L7.05,11.29C6.27,12.07 6.27,13.34 7.05,14.12C7.83,14.9 9.09,14.9 9.88,14.12L10.59,13.41L12,14.83L11.29,15.54C9.73,17.1 7.2,17.1 5.64,15.54C4.08,13.98 4.08,11.45 5.64,9.88L9.17,6.34C10.73,4.78 13.26,4.78 14.83,6.34C16.39,7.9 16.39,10.43 14.83,12L14.12,12.71L12.71,11.29L13.41,10.59Z"/>
        </svg>
      </a>
    `;
  }

  // renderTaskAssignmentModeBadge surfaces coordinator-driven assignment so the
  // user can tell a statically-planned or delegated task from an ordinary manual
  // one. Manual and legacy (unlabeled) tasks get no badge to avoid clutter.
  renderTaskAssignmentModeBadge(task) {
    const mode = String(task?.assignment_mode || '').trim();
    const labels = {
      static_plan: 'Coordinator plan',
      dynamic_delegation: 'Delegated'
    };
    const label = labels[mode];
    if (!label) return '';
    const reason = String(task?.assignment_reason || '').trim();
    const assignedBy = String(task?.assigned_by || '').trim();
    const title = reason
      ? `${label}: ${reason}`
      : assignedBy
        ? `${label} (by ${assignedBy})`
        : label;
    return `<span class="workspace-detail-assignment-mode" data-mode="${this.escapeHtml(mode)}" title="${this.escapeHtml(title)}">${this.escapeHtml(label)}</span>`;
  }

  renderTaskItem(task, options = {}) {
    const {
      isSubtask = false,
      stepNumber = null,
      isParent: isParentHint = false,
      subtaskCount = 0,
      collapsed = false
    } = options;
    const taskLabel = this.escapeHtml(task.description || task.name || 'Untitled Task');
    const assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
    const subtasks = isSubtask
      ? []
      : this.tasks.filter(subtask => subtask.parent_task_id === task.id);
    const isParent = isParentHint || subtasks.length > 0;
    const parentSubtaskCount = subtaskCount || subtasks.length;
    const statusInfo = this.getTaskStatusPresentation(task);
    const awaitingNextStep = this.isTaskAwaitingNextStep(task);
    const hasUnassignedSubtasks =
      isParent && subtasks.some(subtask => !subtask.to || subtask.to === 'unassigned');
    const hasRunningSubtasks =
      isParent && subtasks.some(subtask => subtask.status === 'in_progress');
    const canExecute = isParent
      ? parentSubtaskCount > 0 && !hasUnassignedSubtasks && !hasRunningSubtasks
      : !statusInfo.isBlocked && (task.status !== 'in_progress' || awaitingNextStep);
    const resultData = this.getDisplayResult(task, subtasks);
    const hasResultData = !!resultData;
    const hasAssistData = !!statusInfo.isBlocked;
    const taskTerminalState = task.status === 'completed' || task.status === 'failed' || task.status === 'cancelled' || task.status === 'timeout';
    const isRerun = !isParent && taskTerminalState;
    const executeTitle = isParent
      ? hasUnassignedSubtasks
        ? 'Assign agents to all subtasks before executing'
        : hasRunningSubtasks
          ? 'A subtask is already running'
          : 'Execute workflow now'
      : !assignedAgent
        ? (isRerun ? 'Will auto-assign a workspace agent before re-running' : 'Will auto-assign a workspace agent before execution')
        : awaitingNextStep
          ? 'Execute the next internal step'
          : task.status === 'in_progress'
            ? 'Task is already running'
            : isRerun
              ? 'Re-run this task'
              : 'Execute task now';
    const resultTitle = hasResultData
      ? `View ${resultData.label} from ${resultData.answeredBy || 'Unknown agent'}`
      : '';
    const assistTitle =
      statusInfo.reason || 'Agent needs your guidance before this task can continue.';
    const scheduleIndicator = this.renderTaskScheduleIndicator(task);
    const referenceIndicator = this.renderTaskReferenceURLIndicator(task);
    const taskTags = Array.isArray(task.tags)
      ? task.tags.map(tag => String(tag || '').trim()).filter(Boolean)
      : [];
    const taskMetaParts = [];
    if (isParent) {
      const stepsLabel = `${parentSubtaskCount} step${parentSubtaskCount === 1 ? '' : 's'}`;
      taskMetaParts.push(`<span class="workspace-detail-workflow-steps">${stepsLabel}</span>`);
    }
    if (taskTags.length > 0) {
      taskMetaParts.push(
        `<span class="workspace-detail-item-tags">${taskTags
          .map(
            tag =>
              `<span class="workspace-detail-tag-chip" title="${this.escapeAttribute(tag)}">${this.escapeHtml(tag)}</span>`
          )
          .join('')}</span>`
      );
    }
    if (assignedAgent) {
      taskMetaParts.push(
        `<span class="workspace-detail-assigned-agent">Assigned to: ${this.escapeHtml(assignedAgent)}${this.renderAgentCapabilityBadges(assignedAgent)}</span>`
      );
    }
    const assignmentModeBadge = this.renderTaskAssignmentModeBadge(task);
    if (assignmentModeBadge) {
      taskMetaParts.push(assignmentModeBadge);
    }
    if (scheduleIndicator) {
      taskMetaParts.push(scheduleIndicator);
    }
    if (referenceIndicator) {
      taskMetaParts.push(referenceIndicator);
    }
    const workflowMeta = this.renderTaskWorkflowMeta(task, { isParent });
    if (workflowMeta) {
      taskMetaParts.push(workflowMeta);
    }
    taskMetaParts.push(formatDate(task.created_at));

    const itemClasses = ['workspace-detail-item'];
    if (isParent) itemClasses.push('is-workflow-parent');
    if (isSubtask) itemClasses.push('is-workflow-subtask');

    const toggleButton = isParent
      ? `<button type="button"
                 class="workspace-detail-item-toggle"
                 aria-expanded="${collapsed ? 'false' : 'true'}"
                 aria-controls="workspace-detail-subtasks-${this.escapeHtml(task.id)}"
                 title="${collapsed ? 'Show subtasks' : 'Hide subtasks'}"
                 aria-label="${collapsed ? 'Show subtasks' : 'Hide subtasks'}"
                 onclick="event.stopPropagation(); window.workspaceDetail?.toggleWorkflowExpansion('${task.id}')">
           <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
             <path d="M7,10L12,15L17,10H7Z"/>
           </svg>
         </button>`
      : '';

    const parentBadge = isParent
      ? '<span class="workspace-detail-workflow-badge" title="Workflow with subtasks">Workflow</span>'
      : '';
    const stepBadge =
      isSubtask && stepNumber !== null
        ? `<span class="workspace-detail-step-badge" title="Step ${this.escapeHtml(String(stepNumber))}">Step ${this.escapeHtml(String(stepNumber))}</span>`
        : '';

    const cardOpenLabel = `Open task ${this.escapeHtml(task.description || task.name || 'Untitled Task')}`;
    return `
      <div class="${itemClasses.join(' ')}"
           data-task-id="${task.id}"
           role="button"
           tabindex="0"
           aria-label="${cardOpenLabel}"
           onclick="if (event.target.closest('button, a, input, select, textarea, [contenteditable=true]')) return; window.workspaceDetail?.openTask('${task.id}')"
           onkeydown="if ((event.key === 'Enter' || event.key === ' ') && !event.target.closest('button, a, input, select, textarea, [contenteditable=true]')) { event.preventDefault(); window.workspaceDetail?.openTask('${task.id}'); }">
        ${
          hasAssistData
            ? `
        <button type="button"
                class="workspace-detail-item-result"
                onclick="event.stopPropagation(); window.workspaceDetail?.openTask('${task.id}')"
                title="${this.escapeHtml(assistTitle)}"
                aria-label="Help blocked task ${taskLabel}">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor">
            <path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
          </svg>
        </button>
        `
            : hasResultData
              ? `
        <button type="button"
                class="workspace-detail-item-result"
                onclick="event.stopPropagation(); window.workspaceDetail?.showTaskResult('${task.id}')"
                title="${this.escapeHtml(resultTitle)}"
                aria-label="View result for task ${taskLabel}">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor">
            <path d="M14,17H7V15H14M17,13H7V11H17M17,9H7V7H17M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3Z"/>
          </svg>
        </button>
        `
              : ''
        }
        <button type="button"
                class="workspace-detail-item-run${isRerun ? ' is-rerun' : ''}"
                onclick="event.stopPropagation(); window.workspaceDetail?.executeTask('${task.id}')"
                title="${this.escapeHtml(executeTitle)}"
                aria-label="${isRerun ? 'Re-run' : 'Execute'} task ${taskLabel}"
                ${canExecute ? '' : 'disabled'}>
          ${isRerun
            ? `<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                 <path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/>
               </svg>`
            : `<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                 <path d="M8,5.14V19.14L19,12.14L8,5.14Z"/>
               </svg>`}
        </button>
        <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteTask('${task.id}')" title="Delete task" aria-label="Delete task ${this.escapeHtml(task.description || task.name || 'Untitled Task')}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <div class="workspace-detail-item-content">
          <div class="d-flex justify-content-between align-items-start">
            <div class="workspace-detail-item-title">
              ${toggleButton}${stepBadge}${parentBadge}<span class="workspace-detail-item-title-text">${taskLabel}</span>
            </div>
            <div class="workspace-detail-task-status-group">
              <span class="workspace-detail-task-activity" data-task-activity-slot hidden></span>
              <span class="workspace-detail-task-status ${statusInfo.className}">${statusInfo.label}</span>
            </div>
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
    if (params.get('addAgent') !== '1') return false;
    const seedAgentName = String(params.get('seedAgentName') || '').trim();

    // Clean the URL so refresh won't re-trigger, but preserve unrelated params.
    const url = new URL(window.location.href);
    url.searchParams.delete('addAgent');
    url.searchParams.delete('seedAgentName');
    window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`);

    this.openWorkspaceEntryAgentCreateFlow({
      defer: true,
      seedName: seedAgentName
    });
    return true;
  }

  getEntryAgentPromptDismissalStorageKey() {
    return `${ENTRY_AGENT_PROMPT_DISMISSED_STORAGE_PREFIX}${this.workspaceId}`;
  }

  clearEntryAgentPromptDismissal() {
    try {
      window.sessionStorage?.removeItem(this.getEntryAgentPromptDismissalStorageKey());
    } catch (_error) {
      // Ignore storage errors; the prompt can still function without persistence.
    }
  }

  shouldPromptForMissingEntryAgent() {
    const entryAgentName = String(this.workspace?.entry_agent_name || '').trim();
    if (entryAgentName) {
      this.clearEntryAgentPromptDismissal();
      return false;
    }

    try {
      return window.sessionStorage?.getItem(this.getEntryAgentPromptDismissalStorageKey()) !== '1';
    } catch (_error) {
      return true;
    }
  }

  buildWorkspaceEntryAgentDefaults(options = {}) {
    const workspaceName = String(this.workspace?.name || '').trim();
    const fallbackAgentName = workspaceName
      ? workspaceName.toLowerCase().endsWith(' manager')
        ? workspaceName
        : workspaceName + ' Manager'
      : 'Workspace Manager';
    const agentName = String(options?.seedName || '').trim() || fallbackAgentName;
    const systemPrompt =
      `You are the workspace manager for "${workspaceName || 'this workspace'}". ` +
      'Act as the default front door for the workspace: clarify user intent, answer directly when ' +
      'the request only needs shared context, and break work into tasks for specialists when needed.';

    return {
      workspaceId: this.workspaceId,
      seedName: agentName,
      seedType: 'orchestration',
      seedSystemPrompt: systemPrompt,
      suggestedSkills: ['workspace-planning']
    };
  }

  openWorkspaceEntryAgentCreateFlow(options = {}) {
    const defaults = this.buildWorkspaceEntryAgentDefaults(options);
    const defer = options?.defer === true;
    const open = () => this.openCreateAgentFlow(defaults);

    if (defer) {
      window.setTimeout(open, 300);
      return;
    }

    open();
  }

  async maybePromptForMissingEntryAgent() {
    if (!this.shouldPromptForMissingEntryAgent()) return;

    const workspaceName = String(this.workspace?.name || '').trim() || 'this workspace';
    const entryAgentDefaults = this.buildWorkspaceEntryAgentDefaults();
    // An entry agent is required for the workspace to operate, so this prompt is
    // mandatory: there is no "create later" escape and it resolves only when the
    // user proceeds to create one.
    await this.showTaskConfirmDialog({
      eyebrow: 'Workspace Setup',
      title: 'Create an entry agent for this workspace?',
      message: `"${workspaceName}" cannot function until it has an entry agent.`,
      confirmLabel: 'Create Entry Agent',
      mandatory: true,
      metaItems: [workspaceName, entryAgentDefaults.seedName, 'No entry agent'],
      details: [
        'The entry agent is required for this workspace to operate normally.',
        'Chats, routing, and task orchestration depend on having an entry agent.',
        'The first agent you add here will become the workspace entry agent automatically.',
        'You can rename or replace it later.'
      ]
    });

    this.clearEntryAgentPromptDismissal();
    this.openWorkspaceEntryAgentCreateFlow();
  }

  async openAddAgentModal() {
    await this.loadAgentCatalog(true);
    await this.populateAddAgentOptions();

    if (this.elements.addAgentModal && window.bootstrap) {
      const modal =
        typeof bootstrap.Modal.getOrCreateInstance === 'function'
          ? bootstrap.Modal.getOrCreateInstance(this.elements.addAgentModal)
          : bootstrap.Modal.getInstance(this.elements.addAgentModal) ||
            new bootstrap.Modal(this.elements.addAgentModal);
      modal.show();
    }
  }

  isMissingWorkspaceAgentError(message) {
    const normalized = String(message || '')
      .trim()
      .toLowerCase();
    if (!normalized) return false;

    return (
      normalized.includes('no agent is available in this workspace') ||
      normalized.includes('no agents available in this workspace') ||
      normalized.includes('no agent assigned') ||
      normalized.includes('no agent assigned to step') ||
      normalized.includes('parent task has no agent')
    );
  }

  async promptAddAgentForExecution(
    message = 'No agent is available in this workspace. Add one to continue.'
  ) {
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
      this.elements.taskConfirmCancelBtn.classList.remove('d-none');
    }
    if (this.elements.taskConfirmCloseBtn) {
      this.elements.taskConfirmCloseBtn.classList.remove('d-none');
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
      .map(item => String(item || '').trim())
      .filter(Boolean)
      .forEach(item => {
        const chip = document.createElement('span');
        chip.className = 'workspace-detail-task-confirm-chip';
        chip.textContent = item;
        this.elements.taskConfirmMeta.appendChild(chip);
      });
  }

  renderTaskConfirmDetails(items = []) {
    if (!this.elements.taskConfirmDetails) return;

    const normalizedItems = items.map(item => String(item || '').trim()).filter(Boolean);

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
      .map(item => {
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

    const list = normalizedItems
      .map((item, index) => {
        const title = this.escapeHtml(item.title);
        const detail = item.detail
          ? `<div class="workspace-detail-task-confirm-sequence-step-detail">${this.escapeHtml(item.detail)}</div>`
          : '';
        const tag = item.tag
          ? `<span class="workspace-detail-task-confirm-sequence-tag">${this.escapeHtml(item.tag)}</span>`
          : '';
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
      })
      .join('');

    this.elements.taskConfirmSequence.innerHTML = `
      <div class="workspace-detail-task-confirm-sequence-title">Predicted Sequence</div>
      <div class="workspace-detail-task-confirm-sequence-list">${list}</div>
    `;
    this.elements.taskConfirmSequence.classList.remove('d-none');
  }

  getTaskConfirmModalInstance(options = null) {
    if (!this.elements.taskConfirmModal || !window.bootstrap) return null;

    // Bootstrap locks backdrop/keyboard at construction, so when a caller asks
    // for a different dismissibility mode than the active instance, dispose and
    // rebuild it. A mandatory dialog uses a static backdrop and ignores Escape.
    if (options && typeof options.mandatory === 'boolean') {
      if (this.taskConfirmModalMandatory !== options.mandatory) {
        const existing = bootstrap.Modal.getInstance(this.elements.taskConfirmModal);
        if (existing) existing.dispose();
        this.taskConfirmModalMandatory = options.mandatory;
        return new bootstrap.Modal(
          this.elements.taskConfirmModal,
          options.mandatory
            ? { backdrop: 'static', keyboard: false }
            : { backdrop: true, keyboard: true }
        );
      }
    }

    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.elements.taskConfirmModal)
      : bootstrap.Modal.getInstance(this.elements.taskConfirmModal) ||
          new bootstrap.Modal(this.elements.taskConfirmModal);
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
    // A mandatory dialog has no escape hatch: no cancel/close button, and the
    // backdrop/Escape are disabled. The promise can only resolve via confirm.
    const mandatory = options?.mandatory === true;

    if (!this.elements.taskConfirmModal || !window.bootstrap) {
      const fallbackSequence =
        sequenceItems.length > 0
          ? `Predicted sequence:\n${sequenceItems.map((item, index) => `${index + 1}. ${item.title}${item.detail ? ` - ${item.detail}` : ''}`).join('\n')}`
          : '';
      const fallbackMode = allowStepThrough
        ? `Execution mode: ${defaultStepThrough ? 'step through' : 'automatic'}`
        : '';
      const fallbackText = [message, ...details, fallbackSequence, fallbackMode]
        .filter(Boolean)
        .join('\n\n');
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
      this.elements.taskConfirmCancelBtn.classList.toggle('d-none', mandatory);
    }
    if (this.elements.taskConfirmCloseBtn) {
      this.elements.taskConfirmCloseBtn.classList.toggle('d-none', mandatory);
    }
    if (this.elements.taskConfirmConfirmBtn) {
      this.elements.taskConfirmConfirmBtn.textContent = confirmLabel || 'Continue';
    }

    this.renderTaskConfirmMeta(metaItems);
    this.renderTaskConfirmDetails(details);
    this.renderTaskConfirmSequence(sequenceItems);
    this.renderTaskConfirmStepMode({ allowStepThrough, defaultStepThrough });

    return new Promise(resolve => {
      this.pendingTaskConfirm = { resolve, resolved: false };
      const modal = this.getTaskConfirmModalInstance({ mandatory });
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
      ? routeData.reasons
          .map(reason => String(reason || '').trim())
          .filter(Boolean)
          .slice(0, 3)
      : [];
    if (reasons.length === 0) return '';
    return `\n\nWhy this suggestion:\n- ${reasons.join('\n- ')}`;
  }

  isLikelyFilesystemIntent(description) {
    const lower = String(description || '')
      .trim()
      .toLowerCase();
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
    if (directPhrases.some(phrase => lower.includes(phrase))) {
      return true;
    }

    const actionSignals = [
      'move',
      'copy',
      'rename',
      'organize',
      'organise',
      'gather',
      'collect',
      'sort',
      'group',
      'archive'
    ];
    const nounSignals = [
      'file',
      'files',
      'folder',
      'folders',
      'directory',
      'directories',
      'filesystem',
      'path',
      'paths'
    ];
    const actionCount = actionSignals.filter(signal => lower.includes(signal)).length;
    const nounCount = nounSignals.filter(signal => lower.includes(signal)).length;

    return (actionCount > 0 && nounCount > 0) || nounCount > 1;
  }

  isReadOnlyFilesystemListingIntent(description) {
    const lower = String(description || '')
      .trim()
      .toLowerCase();
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
    if (mutationPhrases.some(phrase => lower.includes(phrase))) {
      return false;
    }

    const mutationSignals = [
      'move',
      'copy',
      'rename',
      'organize',
      'organise',
      'gather',
      'collect',
      'sort',
      'group',
      'archive'
    ];
    const mutationNouns = [
      'file',
      'files',
      'folder',
      'folders',
      'directory',
      'directories',
      'filesystem',
      'path',
      'paths'
    ];
    const actionCount = mutationSignals.filter(signal => lower.includes(signal)).length;
    const nounCount = mutationNouns.filter(signal => lower.includes(signal)).length;
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
    if (directPhrases.some(phrase => lower.includes(phrase))) {
      return true;
    }

    const listingSignals = ['list', 'show', 'display'];
    const listingNouns = [
      'file',
      'files',
      'folder',
      'folders',
      'directory',
      'directories',
      'contents'
    ];
    const listingVerbCount = listingSignals.filter(signal => lower.includes(signal)).length;
    const listingNounCount = listingNouns.filter(signal => lower.includes(signal)).length;
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
        reason:
          'This task appears to require gathering, moving, renaming, or organizing files and folders.'
      });
    }
    return requirements;
  }

  getTaskRequirementSignals(requirementKey) {
    switch (
      String(requirementKey || '')
        .trim()
        .toLowerCase()
    ) {
      case TASK_REQUIREMENT_KEYS.BROWSER:
        return {
          capabilities: ['browser', 'browser_automation', 'web_search', 'web_fetch'],
          plugins: [
            'browser',
            'playwright',
            'browserbase',
            'puppeteer',
            'navigate',
            'open_url',
            'web_fetch',
            'web_search'
          ],
          mcp: ['playwright', 'browserbase', 'puppeteer', 'browser'],
          skills: ['browser', 'web', 'playwright', 'navigate', 'website', 'scrape']
        };
      case TASK_REQUIREMENT_KEYS.FILESYSTEM:
        return {
          capabilities: ['file_operations', 'filesystem', 'storage'],
          plugins: [
            'filesystem',
            'file',
            'files',
            'folder',
            'directory',
            'finder',
            'shell',
            'command',
            'os-shell'
          ],
          mcp: ['filesystem', 'files', 'file', 'finder', 'directory'],
          skills: [
            'file',
            'files',
            'folder',
            'folders',
            'directory',
            'filesystem',
            'finder',
            'organize',
            'organiser',
            'move',
            'rename',
            'copy'
          ]
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
    const keys = new Set(
      (Array.isArray(requirements) ? requirements : []).map(requirement =>
        String(requirement?.key || '')
          .trim()
          .toLowerCase()
      )
    );
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
      ? values.map(value => String(value || '').trim()).filter(Boolean)
      : [];
    const normalizedSignals = Array.isArray(signals)
      ? signals
          .map(value =>
            String(value || '')
              .trim()
              .toLowerCase()
          )
          .filter(Boolean)
      : [];

    const matches = [];
    const seen = new Set();
    normalizedValues.forEach(value => {
      const lower = value.toLowerCase();
      if (!lower) return;
      if (!normalizedSignals.some(signal => lower.includes(signal))) return;
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
      .map(value =>
        String(value || '')
          .trim()
          .toLowerCase()
      )
      .filter(Boolean)
      .join(' ');
  }

  async getEnabledSkillsForAgent(agentName) {
    const state = await this.loadAgentSkills(agentName, { refreshUI: false });
    if (!state || !Array.isArray(state.skills)) return [];
    return state.skills.filter(skill => skill?.enabled !== false && skill?.name);
  }

  skillSupportsTaskRequirement(skill, requirementKey) {
    if (!skill || skill.enabled === false) return false;

    const signals = this.getTaskRequirementSignals(requirementKey);
    const skillText = this.getTaskRequirementSkillText(skill);
    const mcpMatches = this.getTaskRequirementMatches(skill.requiredMCPServers, signals.mcp);
    if (mcpMatches.length > 0) {
      return true;
    }

    return signals.skills.some(signal => skillText.includes(signal));
  }

  getAgentSupportForTaskRequirement(profile, skills, requirement) {
    if (!profile || !requirement) {
      return { supported: false, score: 0, reasons: [] };
    }

    const signals = this.getTaskRequirementSignals(requirement.key);
    const reasons = [];
    let score = 0;

    if (
      requirement.key === TASK_REQUIREMENT_KEYS.BROWSER &&
      this.agentSupportsBrowserAutomation(profile)
    ) {
      score += 3;
      reasons.push('has browser automation support');
    }

    const capabilityMatches = this.getTaskRequirementMatches(
      profile.capabilities,
      signals.capabilities
    );
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
      .filter(skill => this.skillSupportsTaskRequirement(skill, requirement.key))
      .slice(0, 2);
    if (skillMatches.length > 0) {
      score += 4;
      reasons.push(`has skill support via "${skillMatches[0].name}"`);
    }

    const dedupedReasons = [];
    const seenReasons = new Set();
    reasons.forEach(reason => {
      const normalized = String(reason || '')
        .trim()
        .toLowerCase();
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
      support.reasons.forEach(reason => {
        const key = String(reason || '')
          .trim()
          .toLowerCase();
        if (!key || seenReasons.has(key)) return;
        seenReasons.add(key);
        reasons.push(reason);
      });
    }

    const inWorkspace = this.getWorkspaceAgentNames().some(
      name => this.normalizeAgentName(name) === this.normalizeAgentName(agentName)
    );
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
      : Array.from(
          new Set(
            (this.agentCatalog || [])
              .map(profile => String(profile?.name || '').trim())
              .filter(Boolean)
          )
        );

    const evaluations = await Promise.all(
      candidateNames.map(async name => {
        if (this.normalizeAgentName(name) === exclude) return null;
        return this.evaluateAgentForTaskRequirements(name, requirements);
      })
    );

    const ranked = evaluations.filter(Boolean).sort((left, right) => {
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
            .map(server => ({
              name: String(server?.name || '').trim()
            }))
            .filter(server => server.name);
        }

        if (skillsResult.status === 'fulfilled' && skillsResult.value.ok) {
          const data = await skillsResult.value.json().catch(() => ({}));
          const skills = Array.isArray(data?.skills) ? data.skills : [];
          catalog.skills = skills
            .map(skill => ({
              name: String(skill?.name || '').trim(),
              description: String(skill?.description || '').trim(),
              requiredMCPServers: Array.isArray(skill?.required_mcp_servers)
                ? skill.required_mcp_servers
                    .map(value => String(value || '').trim())
                    .filter(Boolean)
                : []
            }))
            .filter(skill => skill.name);
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
    const sourceCatalog =
      catalog && typeof catalog === 'object' ? catalog : { mcpServers: [], skills: [] };
    const requirementKeys = Array.isArray(requirements)
      ? requirements
          .map(requirement =>
            String(requirement?.key || '')
              .trim()
              .toLowerCase()
          )
          .filter(Boolean)
      : [];

    const matchedMCPServers = [];
    const matchedSkills = [];
    const seenMCPServers = new Set();
    const seenSkills = new Set();

    requirementKeys.forEach(requirementKey => {
      const signals = this.getTaskRequirementSignals(requirementKey);

      (sourceCatalog.mcpServers || []).forEach(server => {
        const name = String(server?.name || '').trim();
        const normalized = name.toLowerCase();
        if (!name || seenMCPServers.has(normalized)) return;
        if (!signals.mcp.some(signal => normalized.includes(signal))) return;
        seenMCPServers.add(normalized);
        matchedMCPServers.push(name);
      });

      (sourceCatalog.skills || []).forEach(skill => {
        const name = String(skill?.name || '').trim();
        const normalized = name.toLowerCase();
        if (!name || seenSkills.has(normalized)) return;
        const skillText = this.getTaskRequirementSkillText(skill);
        const requiredMCPServers = Array.isArray(skill?.requiredMCPServers)
          ? skill.requiredMCPServers
          : [];
        const hasRequirementMatch =
          signals.skills.some(signal => skillText.includes(signal)) ||
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
    const details = [`Create an agent that can complete the task "${taskLabel}".`];

    const requirementKeys = new Set(
      (Array.isArray(requirements) ? requirements : []).map(requirement =>
        String(requirement?.key || '')
          .trim()
          .toLowerCase()
      )
    );
    if (requirementKeys.has(TASK_REQUIREMENT_KEYS.FILESYSTEM)) {
      details.push(
        'It should be able to inspect folders, gather related files, and move or organize them safely.'
      );
    }
    if (requirementKeys.has(TASK_REQUIREMENT_KEYS.BROWSER)) {
      details.push(
        'It should be able to open websites and interact with browser pages when needed.'
      );
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
          ? `This task needs ${normalizedRequirements.map(requirement => requirement.label.toLowerCase()).join(' and ')}. "${recommendedAgent.agentName}" is the best current match.`
          : `This task needs ${normalizedRequirements.map(requirement => requirement.label.toLowerCase()).join(' and ')}. "${recommendedAgent.agentName}" is the best current match and can be added to this workspace.`,
        confirmLabel: alreadyInWorkspace ? 'Assign Agent' : 'Add and Assign',
        metaItems: [
          taskLabel,
          recommendedAgent.agentName,
          alreadyInWorkspace ? 'Capability match' : 'Add to workspace'
        ],
        details: [
          ...normalizedRequirements.map(requirement => requirement.reason),
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
          window.Toast.success(
            alreadyInWorkspace
              ? `Assigned "${recommendedAgent.agentName}" to this task.`
              : `Added "${recommendedAgent.agentName}" and assigned it to this task.`
          );
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
    const suggestions = this.getCapabilitySuggestionsForRequirements(
      normalizedRequirements,
      catalog
    );
    const defaults = this.getTaskRequirementSeedDefaults(normalizedRequirements);
    const previewSequence = this.getPredictedTaskExecutionSequence(task);
    const createAgent = await this.showTaskConfirmDialog({
      eyebrow: 'Capability Required',
      title: 'Create a capable agent for this task?',
      message: `No assigned agent advertises ${normalizedRequirements.map(requirement => requirement.label.toLowerCase()).join(' and ')} for "${taskLabel}".`,
      confirmLabel: 'Create Agent',
      cancelLabel: 'Cancel',
      metaItems: [taskLabel, defaults.name, 'Needs MCP or skills'],
      details: [
        ...normalizedRequirements.map(requirement => requirement.reason),
        suggestions.mcpServers.length > 0
          ? `Suggested MCP servers: ${suggestions.mcpServers.join(', ')}`
          : 'No matching MCP server is configured yet.',
        suggestions.skills.length > 0
          ? `Suggested skills: ${suggestions.skills.join(', ')}`
          : 'No matching reusable skill is available yet.'
      ],
      sequenceItems: previewSequence
    });
    if (!createAgent) {
      return { handled: true, agentName: '' };
    }

    this.openCreateAgentFlow({
      seedName: defaults.name,
      seedType: defaults.type,
      autoDescription: this.buildCapabilityAwareAgentDescription(
        task,
        normalizedRequirements,
        suggestions
      ),
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
    if (
      !this.elements.addAgentSelect ||
      !this.elements.addAgentSubmitBtn ||
      !this.elements.addAgentEmpty
    )
      return;

    if (!Array.isArray(this.agentCatalog) || this.agentCatalog.length === 0) {
      try {
        const response = await fetch('/api/agents');
        if (response.ok) {
          const data = await response.json();
          const baseAgents = Array.isArray(data?.agents) ? data.agents : [];
          this.agentCatalog = baseAgents
            .map(agent => {
              const name = typeof agent === 'string' ? agent : agent?.name;
              return name ? { name: String(name).trim() } : null;
            })
            .filter(agent => agent && agent.name);
          this.agentIndex = new Map(
            this.agentCatalog.map(agent => [this.normalizeAgentName(agent.name), agent])
          );
        }
      } catch (error) {
        console.error('Failed to load fallback agent list:', error);
      }
    }

    const workspaceAgents = new Set(
      this.getWorkspaceAgentNames().map(name => this.normalizeAgentName(name))
    );
    const allAgents = Array.isArray(this.agentCatalog) ? this.agentCatalog : [];
    const candidates = allAgents
      .map(agent => String(agent?.name || '').trim())
      .filter(Boolean)
      .filter(name => !workspaceAgents.has(this.normalizeAgentName(name)));

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
      .map(name => `<option value="${this.escapeHtml(name)}">${this.escapeHtml(name)}</option>`)
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
    await Promise.all([this.loadWorkspace(), this.loadAgentCatalog(true)]);
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
      submitButton.innerHTML =
        '<span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Adding...';
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
      ? this.workspace.agent_instances.filter(
          instance => this.normalizeAgentName(instance?.name) === normalized
        )
      : [];
    if (instances.length > 0) {
      return instances;
    }

    const names = Array.isArray(this.workspace.agents) ? this.workspace.agents : [];
    if (names.some(name => this.normalizeAgentName(name) === normalized)) {
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

    if (this.isWorkspaceEntryAgent(normalizedAgentName)) {
      window.alert(
        `"${normalizedAgentName}" is the workspace entry agent and can't be removed.`
      );
      return;
    }

    const instances = this.getWorkspaceAgentInstances(normalizedAgentName);
    const instanceCount = instances.length > 0 ? instances.length : 1;
    const taskCount = Array.isArray(this.tasks)
      ? this.tasks.filter(task => this.normalizeAgentName(task?.to) === normalizedKey).length
      : 0;
    const sessionCount = Array.isArray(this.sessions)
      ? this.sessions.filter(
          session => this.normalizeAgentName(session?.agent_name) === normalizedKey
        ).length
      : 0;

    const confirmationLines = [`Remove "${normalizedAgentName}" from this workspace?`];
    if (instanceCount > 1) {
      confirmationLines.push(
        `This will remove all ${instanceCount} instances of this agent from the workspace.`
      );
    }
    if (taskCount > 0) {
      confirmationLines.push(
        `${taskCount} assigned task${taskCount === 1 ? '' : 's'} will be moved to Unassigned.`
      );
    }
    if (sessionCount > 0) {
      confirmationLines.push(
        `${sessionCount} session${sessionCount === 1 ? '' : 's'} will remain in workspace history.`
      );
    }

    if (!window.confirm(confirmationLines.join('\n\n'))) {
      return;
    }

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/agents/${encodeURIComponent(normalizedAgentName)}`,
        {
          method: 'DELETE'
        }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to remove agent from workspace');
      }

      this.flippedAgentCards.delete(normalizedKey);
      this.agentSkillsCache.delete(normalizedKey);
      this.agentSkillsPromises.delete(normalizedKey);

      await Promise.all([this.loadWorkspace(), this.loadTasks(), this.loadSessions()]);

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

    const taskLabel =
      String(task.description || task.name || task.id || 'this task').trim() || 'this task';
    const prompt = this.getTaskAgentSuggestionPrompt(task);
    const specialistAction = this.maybeBuildTravelAssistSpecialistAction({
      task,
      currentAgent: String(task.to || '').trim()
    });
    if (specialistAction) {
      const specialistSuggestion = await this.suggestTravelSpecialistSetupForTask(
        task,
        specialistAction
      );
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
      ? this.getWorkspaceAgentNames().some(
          name => this.normalizeAgentName(name) === this.normalizeAgentName(suggestedAgent)
        )
      : false;

    if (
      suggestedAgent &&
      !this.isSystemAssistantAgentName(suggestedAgent) &&
      routeData?.requires_creation !== true
    ) {
      const reasonItems = Array.isArray(routeData?.reasons)
        ? routeData.reasons
            .map(reason => String(reason || '').trim())
            .filter(Boolean)
            .slice(0, 3)
        : [];
      const previewSequence = this.getPredictedTaskExecutionSequence(task, [], {
        assignedAgent: suggestedAgent
      });
      const confirmed = await this.showTaskConfirmDialog({
        eyebrow: 'Ori Recommendation',
        title: alreadyInWorkspace
          ? `Assign "${suggestedAgent}" to this task?`
          : `Add "${suggestedAgent}" and assign it?`,
        message: alreadyInWorkspace
          ? `Ori matched ${taskLabel} to "${suggestedAgent}" and can assign it in one step.`
          : `Ori matched ${taskLabel} to "${suggestedAgent}" and can add it to this workspace before execution.`,
        confirmLabel: alreadyInWorkspace ? 'Assign Agent' : 'Add and Assign',
        metaItems: [
          taskLabel,
          suggestedAgent,
          alreadyInWorkspace ? 'Ready to assign' : 'Needs workspace add'
        ],
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
          window.Toast.success(
            alreadyInWorkspace
              ? `Assigned "${suggestedAgent}" to this task.`
              : `Added "${suggestedAgent}" and assigned it to this task.`
          );
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
    const suggestedName =
      String(routeData?.suggested_agent_name || '').trim() ||
      (prefersBrowserAgent ? 'Browser Assistant' : 'Task Assistant');
    const suggestedType = String(routeData?.suggested_agent_type || '').trim() || 'tool-calling';
    const reasonText = this.getTaskAgentSuggestionReasonText(routeData);
    if (window.Toast) {
      const message =
        routeData?.requires_creation === true
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
    const mode = String(task?.execution_mode || '')
      .trim()
      .toLowerCase();
    return mode === 'step_through' ? 'step_through' : 'auto';
  }

  isTaskAwaitingNextStep(task) {
    return (
      this.getTaskExecutionMode(task) === 'step_through' &&
      task?.status === 'in_progress' &&
      task?.context?.execution_step_waiting === true
    );
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
    const humanLoopState = String(humanLoop?.state || '').toLowerCase();
    if (humanLoop && (humanLoopState === 'blocked' || humanLoopState === 'waiting_for_choice' || task?.status === 'waiting_for_choice')) {
      const reason = String(humanLoop.reason || '').trim();
      return {
        className: 'blocked',
        label: task?.status === 'waiting_for_choice' || humanLoopState === 'waiting_for_choice' ? 'Waiting for Choice' : 'Needs Input',
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
        const aIndex =
          Number.isFinite(a?.subtask_index) && a.subtask_index > 0
            ? a.subtask_index
            : Number.MAX_SAFE_INTEGER;
        const bIndex =
          Number.isFinite(b?.subtask_index) && b.subtask_index > 0
            ? b.subtask_index
            : Number.MAX_SAFE_INTEGER;
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
    const task = this.tasks.find(item => item.id === taskId);
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
    this.currentTaskResultPromotionPending = false;
    this.currentTaskResultPromotionSubmitting = false;
    this.currentTaskResultPromotionDraft = null;
    this.currentTaskResultPromotionContext = null;
    this.renderTaskResultNextSteps(task, resultData);

    const openResultModal = () => {
      if (!this.elements.taskResultModal || !window.bootstrap) return;
      const modal =
        typeof bootstrap.Modal.getOrCreateInstance === 'function'
          ? bootstrap.Modal.getOrCreateInstance(this.elements.taskResultModal)
          : bootstrap.Modal.getInstance(this.elements.taskResultModal) ||
            new bootstrap.Modal(this.elements.taskResultModal);
      modal.show();
    };

    if (closeExecutionModal && this.elements.taskExecutionModal && window.bootstrap) {
      const isExecutionModalOpen = this.elements.taskExecutionModal.classList.contains('show');
      if (isExecutionModalOpen) {
        const executionModal =
          typeof bootstrap.Modal.getOrCreateInstance === 'function'
            ? bootstrap.Modal.getOrCreateInstance(this.elements.taskExecutionModal)
            : bootstrap.Modal.getInstance(this.elements.taskExecutionModal) ||
              new bootstrap.Modal(this.elements.taskExecutionModal);
        this.elements.taskExecutionModal.addEventListener(
          'hidden.bs.modal',
          () => {
            openResultModal();
          },
          { once: true }
        );
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
    const sourceTask =
      resultData?.sourceTask && typeof resultData.sourceTask === 'object'
        ? resultData.sourceTask
        : task;
    const shouldShow =
      String(task?.status || '')
        .trim()
        .toLowerCase() === 'completed' && text;
    if (!shouldShow) {
      this.currentTaskResultNextSteps = [];
      container.classList.add('d-none');
      copyEl.textContent = '';
      actionsEl.innerHTML = '';
      return;
    }

    const nextSteps = this.extractTaskResultNextSteps(text);
    this.currentTaskResultNextSteps = nextSteps;
    const canPromoteResult = this.canPromoteTaskResultToWorkflow(sourceTask, text);

    if (!nextSteps.length && !canPromoteResult) {
      container.classList.add('d-none');
      copyEl.textContent = '';
      actionsEl.innerHTML = '';
      return;
    }

    if (canPromoteResult && nextSteps.length) {
      copyEl.textContent =
        'Create a workflow from the checklist, or choose a next step for a follow-up task.';
    } else if (canPromoteResult) {
      copyEl.textContent = 'Create a parent task with subtasks from the checklist in this result.';
    } else {
      copyEl.textContent =
        'Choose the next step to create and run a follow-up task linked to this result.';
    }

    const actionHtml = [];
    const promotionBusy =
      this.currentTaskResultPromotionPending || this.currentTaskResultPromotionSubmitting;
    if (canPromoteResult) {
      const promoteDisabled = promotionBusy || this.currentTaskResultFollowUpPending;
      const promoteLabel = this.currentTaskResultPromotionPending
        ? 'Preparing preview...'
        : this.currentTaskResultPromotionSubmitting
          ? 'Creating workflow task...'
          : 'Review and create parent task';
      actionHtml.push(`
        <button type="button"
                class="workspace-detail-task-result-next-step-btn"
                data-promote-task-result="true"
                ${promoteDisabled ? 'disabled' : ''}>
          <span class="workspace-detail-task-result-next-step-index">+</span>
          <span class="workspace-detail-task-result-next-step-copy">
            <span class="workspace-detail-task-result-next-step-title">Create workflow task</span>
            <span class="workspace-detail-task-result-next-step-meta">${this.escapeHtml(promoteLabel)}</span>
          </span>
        </button>
      `);
    }

    nextSteps.forEach(step => {
      const followUpDisabled = this.currentTaskResultFollowUpPending || promotionBusy;
      const buttonLabel = this.currentTaskResultFollowUpPending
        ? 'Creating follow-up task...'
        : 'Create follow-up task';
      actionHtml.push(`
        <button type="button"
                class="workspace-detail-task-result-next-step-btn"
                data-next-step-id="${this.escapeHtml(step.id)}"
                ${followUpDisabled ? 'disabled' : ''}>
          <span class="workspace-detail-task-result-next-step-index">${this.escapeHtml(step.number || '•')}</span>
          <span class="workspace-detail-task-result-next-step-copy">
            <span class="workspace-detail-task-result-next-step-title">${this.escapeHtml(step.label)}</span>
            <span class="workspace-detail-task-result-next-step-meta">${this.escapeHtml(buttonLabel)}</span>
          </span>
        </button>
      `);
    });
    actionsEl.innerHTML = actionHtml.join('');

    const promoteButton = actionsEl.querySelector('[data-promote-task-result]');
    if (promoteButton) {
      promoteButton.addEventListener('click', () => {
        void this.promoteTaskResultToWorkflow(task.id, sourceTask.id);
      });
    }

    actionsEl.querySelectorAll('[data-next-step-id]').forEach(button => {
      button.addEventListener('click', () => {
        const nextStepId = String(button.getAttribute('data-next-step-id') || '').trim();
        if (!nextStepId) return;
        void this.continueTaskFromResult(task.id, sourceTask.id, nextStepId);
      });
    });

    container.classList.remove('d-none');
  }

  canPromoteTaskResultToWorkflow(sourceTask, text) {
    if (!sourceTask || typeof sourceTask !== 'object') return false;
    if (String(sourceTask.status || '').trim().toLowerCase() !== 'completed') return false;
    if (!String(sourceTask.result || '').trim()) return false;
    if (String(sourceTask.error || '').trim()) return false;

    const resultType = String(sourceTask.result_type || '').trim().toLowerCase();
    if (resultType === 'task_list') return true;

    const structuredResult = sourceTask.structured_result;
    if (
      structuredResult &&
      typeof structuredResult === 'object' &&
      Array.isArray(structuredResult.groups) &&
      structuredResult.groups.some(group => Array.isArray(group?.items) && group.items.length > 0)
    ) {
      return true;
    }

    return /^\s*[-*]\s+\[[ xX]\]\s+.+$/m.test(String(text || ''));
  }

  countTaskListResultItems(taskList) {
    if (!taskList || !Array.isArray(taskList.groups)) return 0;
    return taskList.groups.reduce((count, group) => {
      if (!group || !Array.isArray(group.items)) return count;
      return count + group.items.length;
    }, 0);
  }

  formatTaskListResultGroupPreviewTitle(title, groupIndex) {
    const fallbackTitle = `Group ${groupIndex + 1}`;
    const value = String(title || fallbackTitle).trim() || fallbackTitle;
    const normalized = value.toLowerCase();
    if (normalized === 'tasks' || normalized === 'task list') return value;
    if (/^\d+\.0\.?\s+/.test(value)) return value;
    return `${groupIndex + 1}.0 ${value}`;
  }

  async previewTaskListResult(sourceTaskId) {
    const response = await fetch(
      `/api/orchestration/tasks/${encodeURIComponent(sourceTaskId)}/result/preview`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      }
    );
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(payload?.error || payload?.message || 'Failed to preview task result');
    }
    return payload;
  }

  async promoteTaskResultToWorkflow(taskId, sourceTaskId) {
    if (this.currentTaskResultPromotionPending || this.currentTaskResultPromotionSubmitting) return;

    const task = this.tasks.find(item => item.id === taskId);
    const sourceTask = this.tasks.find(item => item.id === sourceTaskId) || task;
    if (!task || !sourceTask) return;

    this.currentTaskResultPromotionPending = true;
    this.renderTaskResultNextSteps(task, { text: this.currentTaskResultText, sourceTask });

    try {
      const preview = await this.previewTaskListResult(sourceTask.id);
      const taskList = preview?.task_list;
      const itemCount = Number(preview?.item_count || this.countTaskListResultItems(taskList));

      if (!taskList || itemCount < 1) {
        throw new Error('This result does not include a task list to promote.');
      }

      this.currentTaskResultPromotionDraft = this.cloneTaskListResult(taskList);
      this.currentTaskResultPromotionContext = { taskId: task.id, sourceTaskId: sourceTask.id };
      this.renderTaskResultPromotionPreview(this.currentTaskResultPromotionDraft);
      this.openTaskResultPromotionModal();
    } catch (error) {
      console.error('Failed to preview task result promotion:', error);
      if (window.Toast) {
        window.Toast.error(error?.message || 'Failed to preview workflow task');
      }
    } finally {
      this.currentTaskResultPromotionPending = false;
      this.renderTaskResultNextSteps(task, { text: this.currentTaskResultText, sourceTask });
    }
  }

  cloneTaskListResult(taskList) {
    if (!taskList || typeof taskList !== 'object') return null;
    try {
      return JSON.parse(JSON.stringify(taskList));
    } catch (_error) {
      return null;
    }
  }

  openTaskResultPromotionModal() {
    if (!this.elements.taskResultPromoteModal || !window.bootstrap) return;
    const modal =
      typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.taskResultPromoteModal)
        : bootstrap.Modal.getInstance(this.elements.taskResultPromoteModal) ||
          new bootstrap.Modal(this.elements.taskResultPromoteModal);
    modal.show();
  }

  setTaskResultPromotionSubmitState(isSubmitting) {
    this.currentTaskResultPromotionSubmitting = Boolean(isSubmitting);
    if (this.elements.taskResultPromoteSubmitBtn) {
      this.elements.taskResultPromoteSubmitBtn.disabled = this.currentTaskResultPromotionSubmitting;
      this.elements.taskResultPromoteSubmitBtn.textContent = this.currentTaskResultPromotionSubmitting
        ? 'Creating...'
        : 'Create';
    }
  }

  renderTaskResultPromotionPreview(taskList) {
    if (
      !taskList ||
      !this.elements.taskResultPromoteTitleInput ||
      !this.elements.taskResultPromoteMeta ||
      !this.elements.taskResultPromoteGroups
    ) {
      return;
    }

    const itemCount = this.countTaskListResultItems(taskList);
    this.elements.taskResultPromoteTitleInput.value = String(
      taskList.parent_title || 'Workflow task'
    ).trim();
    this.elements.taskResultPromoteMeta.textContent = `${itemCount} subtask${
      itemCount === 1 ? '' : 's'
    }`;

    const groups = Array.isArray(taskList.groups) ? taskList.groups : [];
    this.elements.taskResultPromoteGroups.innerHTML = groups
      .map((group, groupIndex) => {
        const title = String(group?.title || `Group ${groupIndex + 1}`).trim();
        const displayTitle = this.formatTaskListResultGroupPreviewTitle(title, groupIndex);
        const items = Array.isArray(group?.items) ? group.items : [];
        const itemsHtml = items
          .map((item, itemIndex) => {
            const itemTitle = String(item?.title || '').trim();
            const assignee = String(item?.assignee || '').trim();
            return `
              <label class="workspace-detail-task-result-promote-item">
                <span class="workspace-detail-task-result-promote-index">${groupIndex + 1}.${itemIndex + 1}</span>
                <input type="text"
                       class="form-control form-control-sm workspace-detail-task-result-promote-input"
                       data-group-index="${groupIndex}"
                       data-item-index="${itemIndex}"
                       value="${this.escapeAttribute(itemTitle)}">
                ${
                  assignee
                    ? `<span class="workspace-detail-task-result-promote-assignee">@${this.escapeHtml(assignee)}</span>`
                    : ''
                }
              </label>
            `;
          })
          .join('');
        return `
          <section class="workspace-detail-task-result-promote-group">
            <div class="workspace-detail-task-result-promote-group-title">${this.escapeHtml(displayTitle)}</div>
            <div class="workspace-detail-task-result-promote-items">${itemsHtml}</div>
          </section>
        `;
      })
      .join('');
  }

  collectTaskResultPromotionDraft() {
    const draft = this.cloneTaskListResult(this.currentTaskResultPromotionDraft);
    if (!draft) return null;

    draft.parent_title = String(this.elements.taskResultPromoteTitleInput?.value || '').trim();
    const inputs = this.elements.taskResultPromoteGroups?.querySelectorAll(
      '[data-group-index][data-item-index]'
    );
    inputs?.forEach(input => {
      const groupIndex = Number(input.getAttribute('data-group-index'));
      const itemIndex = Number(input.getAttribute('data-item-index'));
      if (!Number.isInteger(groupIndex) || !Number.isInteger(itemIndex)) return;
      const group = draft.groups?.[groupIndex];
      const item = group?.items?.[itemIndex];
      if (!item) return;
      item.title = String(input.value || '').trim();
    });
    return draft;
  }

  validateTaskResultPromotionDraft(taskList) {
    if (!taskList || typeof taskList !== 'object') {
      return 'Task list preview is unavailable.';
    }
    if (!String(taskList.parent_title || '').trim()) {
      return 'Parent task title is required.';
    }
    if (this.countTaskListResultItems(taskList) < 1) {
      return 'At least one subtask is required.';
    }
    const groups = Array.isArray(taskList.groups) ? taskList.groups : [];
    for (const group of groups) {
      const items = Array.isArray(group?.items) ? group.items : [];
      for (const item of items) {
        if (!String(item?.title || '').trim()) {
          return 'Every subtask needs a title.';
        }
      }
    }
    return '';
  }

  async submitTaskResultPromotion() {
    if (this.currentTaskResultPromotionSubmitting) return;

    const context = this.currentTaskResultPromotionContext;
    const sourceTaskId = String(context?.sourceTaskId || '').trim();
    if (!sourceTaskId) return;

    const taskList = this.collectTaskResultPromotionDraft();
    const validationError = this.validateTaskResultPromotionDraft(taskList);
    if (validationError) {
      if (window.Toast) window.Toast.error(validationError);
      return;
    }

    this.setTaskResultPromotionSubmitState(true);
    const task = this.tasks.find(item => item.id === context?.taskId);
    const sourceTask = this.tasks.find(item => item.id === sourceTaskId) || task;
    if (task && sourceTask) {
      this.renderTaskResultNextSteps(task, { text: this.currentTaskResultText, sourceTask });
    }

    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(sourceTaskId)}/promote-result`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ task_list: taskList })
        }
      );
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.error || payload?.message || 'Failed to create workflow task');
      }

      const subtaskCount = this.countTaskListResultItems(taskList);
      const subtaskLabel = `${subtaskCount} subtask${subtaskCount === 1 ? '' : 's'}`;
      if (window.Toast) {
        window.Toast.success(`Created workflow task with ${subtaskLabel}`);
      }

      if (this.elements.taskResultPromoteModal && window.bootstrap) {
        bootstrap.Modal.getInstance(this.elements.taskResultPromoteModal)?.hide();
      }
      if (this.elements.taskResultModal && window.bootstrap) {
        bootstrap.Modal.getInstance(this.elements.taskResultModal)?.hide();
      }

      this.currentTaskResultPromotionDraft = null;
      this.currentTaskResultPromotionContext = null;

      await this.loadTasks();
      const parentTaskId = String(payload?.parent_task?.id || '').trim();
      if (parentTaskId) {
        this.openTask(parentTaskId);
      }
    } catch (error) {
      console.error('Failed to promote task result:', error);
      if (window.Toast) {
        window.Toast.error(error?.message || 'Failed to create workflow task');
      }
    } finally {
      this.setTaskResultPromotionSubmitState(false);
      if (task && sourceTask) {
        this.renderTaskResultNextSteps(task, { text: this.currentTaskResultText, sourceTask });
      }
    }
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
    const base =
      this.normalizeTaskResultNextStepToken(label).replace(/\s+/g, '-').slice(0, 48) || 'next-step';
    return `result-step-${String(number || '').trim() || 'x'}-${base}`;
  }

  extractTaskResultNextSteps(text) {
    const lines = String(text || '').split(/\r?\n/);
    const cues = ['next steps', 'next step', 'would you like me to', 'let me know'];
    let cueIndex = -1;

    for (let i = 0; i < lines.length; i += 1) {
      const normalized = this.normalizeTaskResultNextStepToken(lines[i]);
      if (cues.some(cue => normalized.includes(cue))) {
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
    const baseTitle =
      String(task?.description || task?.name || 'Follow-up task').trim() || 'Follow-up task';
    const stepLabel = this.cleanTaskResultNextStepText(step?.label || '');
    if (!stepLabel) return baseTitle;
    const combined = `${baseTitle} - ${stepLabel}`;
    return combined.length > 160 ? `${combined.slice(0, 157).trim()}...` : combined;
  }

  buildTaskResultFollowUpDetails(task, sourceTask, step) {
    const parts = [];
    const baseTitle = String(
      task?.description || task?.name || task?.id || 'Completed task'
    ).trim();
    const sourceTitle = String(
      sourceTask?.description || sourceTask?.name || sourceTask?.id || ''
    ).trim();
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

    const task = this.tasks.find(item => item.id === taskId);
    const sourceTask = this.tasks.find(item => item.id === sourceTaskId) || task;
    const nextStep = this.currentTaskResultNextSteps.find(item => item.id === nextStepId);
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

    const stepsHtml = steps
      .map((step, index) => {
        const statusKey = String(step.status || task.status || 'pending')
          .trim()
          .toLowerCase();
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
      })
      .join('');

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
    const maxAttempts = Number(
      task?.context?.execution_retry?.max_attempts || task?.context?.execution_max_attempts || 0
    );
    if (attemptsUsed > 0 && maxAttempts > 0) {
      parts.push(`Attempts: ${attemptsUsed}/${maxAttempts}`);
    }

    const retryFinalOutcome = this.normalizeBreakdownField(
      task?.context?.execution_retry?.final_outcome
    );
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
        status:
          String(step?.status || 'pending')
            .trim()
            .toLowerCase() || 'pending',
        detail: detailParts.join('\n')
      };
    });
  }

  getRetryHistoryBreakdownSteps(task) {
    const history = Array.isArray(task?.context?.execution_retry?.history)
      ? task.context.execution_retry.history
      : [];
    if (!history.length) return [];

    const outcomeToStatus = outcome => {
      const normalized = String(outcome || '')
        .trim()
        .toLowerCase();
      if (normalized === 'success') return 'completed';
      if (normalized === 'error') return 'failed';
      if (normalized === 'needs_input' || normalized === 'blocked') return 'blocked';
      return 'pending';
    };

    return history.map((item, index) => {
      const attemptNumber = Number.isFinite(Number(item?.attempt))
        ? Number(item.attempt)
        : index + 1;
      const outcome = String(item?.outcome || '')
        .trim()
        .toLowerCase();
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

    const startIndex =
      Number.isFinite(Number(options.startIndex)) && Number(options.startIndex) > 0
        ? Number(options.startIndex)
        : 1;

    const mapRecordedStatus = status => {
      const normalized = String(status || '')
        .trim()
        .toLowerCase();
      if (normalized === 'success') return 'completed';
      if (normalized === 'failed') return 'failed';
      if (normalized === 'blocked') return 'blocked';
      return normalized || 'pending';
    };

    return relevantHistory.map((item, index) => {
      const recordedAt = this.normalizeBreakdownField(item?.executed_at);
      const rawStatus = String(item?.status || '')
        .trim()
        .toLowerCase();
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
      const response = await fetch(
        `/api/orchestration/tasks?workspace_id=${encodeURIComponent(this.workspaceId)}`
      );
      if (!response.ok) return [];
      const payload = await response.json();
      const tasks = Array.isArray(payload?.tasks) ? payload.tasks : [];
      return tasks.filter(task => task?.parent_task_id === parentTaskID);
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

    const sortedSubtasks = Array.isArray(subtasks)
      ? [...subtasks].sort((a, b) => {
          const aIndex = Number.isFinite(Number(a?.subtask_index))
            ? Number(a.subtask_index)
            : Number.MAX_SAFE_INTEGER;
          const bIndex = Number.isFinite(Number(b?.subtask_index))
            ? Number(b.subtask_index)
            : Number.MAX_SAFE_INTEGER;
          if (aIndex !== bIndex) return aIndex - bIndex;
          const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
          const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
          return aTime - bTime;
        })
      : [];

    if (sortedSubtasks.length > 0) {
      return sortedSubtasks.map((subtask, index) => ({
        title: String(subtask.description || subtask.name || `Step ${index + 1}`).trim(),
        status: String(subtask.status || task.status || 'pending').trim(),
        detail: this.buildTaskExecutionDetail(subtask, String(subtask.details || '').trim())
      }));
    }

    const retryHistorySteps = this.getRetryHistoryBreakdownSteps(task);
    const historicalRunSteps = this.getExecutionHistoryBreakdownSteps(task, {
      includeLatest: retryHistorySteps.length === 0
    });
    if (historicalRunSteps.length > 0) {
      if (retryHistorySteps.length > 0) {
        const currentRunNumber = historicalRunSteps.length + 1;
        const currentRunSteps = retryHistorySteps.map(step => ({
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

    const sortedSubtasks = Array.isArray(subtasks)
      ? [...subtasks].sort((a, b) => {
          const aIndex = Number.isFinite(Number(a?.subtask_index))
            ? Number(a.subtask_index)
            : Number.MAX_SAFE_INTEGER;
          const bIndex = Number.isFinite(Number(b?.subtask_index))
            ? Number(b.subtask_index)
            : Number.MAX_SAFE_INTEGER;
          if (aIndex !== bIndex) return aIndex - bIndex;
          const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
          const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
          return aTime - bTime;
        })
      : [];

    if (sortedSubtasks.length > 0) {
      return sortedSubtasks.map(subtask => {
        const assignedAgent = String(subtask?.to || '').trim();
        return {
          title:
            String(subtask?.description || subtask?.name || 'Workflow step').trim() ||
            'Workflow step',
          detail:
            assignedAgent && assignedAgent !== 'unassigned'
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
    const hasFilesystem = requirements.some(
      requirement => requirement.key === TASK_REQUIREMENT_KEYS.FILESYSTEM
    );
    const hasBrowser = requirements.some(
      requirement => requirement.key === TASK_REQUIREMENT_KEYS.BROWSER
    );
    const isFilesystemListing = this.isReadOnlyFilesystemListingIntent(description);
    const assignedAgent = String(options?.assignedAgent || '').trim();
    const agentLabel =
      assignedAgent && assignedAgent !== 'unassigned' ? assignedAgent : 'the selected agent';

    const toStep = (title, detail = '', tag = '') => ({ title, detail, tag });

    if (isFilesystemListing) {
      return [
        toStep(
          'Check allowed filesystem scope',
          `Confirm which directories ${agentLabel} can access before inspecting folder contents.`,
          'Discovery'
        ),
        toStep(
          'Inspect the target directory',
          'Locate the requested folder and gather its visible file list.',
          'Discovery'
        ),
        toStep(
          'Return the file list',
          'Return the concrete file list, or explain clearly if the folder is missing or empty.',
          'Summary'
        )
      ];
    }

    if (hasFilesystem) {
      return [
        toStep(
          'Check allowed filesystem scope',
          `Confirm which directories ${agentLabel} can access before making changes.`,
          'Discovery'
        ),
        toStep(
          'Inspect candidate directories',
          'Look through likely source folders for DNM-related material.',
          'Discovery'
        ),
        toStep(
          'Identify DNM-related files',
          'Match files by filename, path, and available task context.',
          'Analysis'
        ),
        toStep(
          'Create the DNM folder if needed',
          'Prepare the destination folder without overwriting unrelated content.',
          'Mutation'
        ),
        toStep(
          'Move or copy matching files',
          'Relocate the selected files into the DNM folder safely.',
          'Mutation'
        ),
        toStep(
          'Verify final folder contents',
          'Confirm the destination contains the expected files and note anything skipped.',
          'Verify'
        ),
        toStep(
          'Return a summary',
          'List moved files, skipped files, and any follow-up needed.',
          'Summary'
        )
      ];
    }

    if (hasBrowser) {
      return [
        toStep(
          'Check available browser capability',
          `Confirm ${agentLabel} can open and interact with the required site.`,
          'Discovery'
        ),
        toStep(
          'Open the target page',
          'Navigate to the relevant website or URL for this task.',
          'Action'
        ),
        toStep(
          'Inspect the required information or controls',
          'Locate the data, form, or interface element needed to complete the task.',
          'Analysis'
        ),
        toStep(
          'Perform the requested browser action',
          'Carry out the needed interaction or extraction.',
          'Action'
        ),
        toStep(
          'Verify the outcome',
          'Confirm the page state or extracted result matches the task goal.',
          'Verify'
        ),
        toStep('Return a summary', 'Report what was done and any follow-up needed.', 'Summary')
      ];
    }

    if (lower.includes('summarize') || lower.includes('summary') || lower.includes('review')) {
      return [
        toStep(
          'Collect the relevant context',
          'Gather the information needed to answer the request.',
          'Discovery'
        ),
        toStep(
          'Synthesize the main findings',
          'Turn the collected context into a concise result.',
          'Analysis'
        ),
        toStep(
          'Return a summary',
          'Present the final result with the most relevant details.',
          'Summary'
        )
      ];
    }

    const fallbackSteps = this.inferSyntheticBreakdownSteps({ ...task, status: 'pending' });
    return fallbackSteps.map((step, index) => ({
      title: String(step?.title || `Step ${index + 1}`).trim() || `Step ${index + 1}`,
      detail: String(step?.detail || '').trim(),
      tag: index === 0 ? 'Discovery' : index === fallbackSteps.length - 1 ? 'Summary' : 'Action'
    }));
  }

  inferSyntheticBreakdownSteps(task) {
    const description = String(task?.description || '').trim();
    const lower = description.toLowerCase();

    const toStep = (title, detail = '') => ({ title, detail });

    let baseSteps = [];
    if (
      (lower.includes('wear') && lower.includes('tomorrow')) ||
      lower.includes('what should i wear')
    ) {
      baseSteps = [
        toStep(
          "Checking tomorrow's weather",
          'Collect forecast details such as temperature, rain chance, and wind.'
        ),
        toStep(
          'Recommendation for clothing based on the weather',
          'Translate weather conditions into practical outfit guidance.'
        )
      ];
    } else if (lower.includes('weather')) {
      baseSteps = [
        toStep('Checking weather conditions', 'Gather forecast or relevant weather signals.'),
        toStep(
          'Summarizing weather insight',
          'Return a concise recommendation tailored to the request.'
        )
      ];
    } else {
      baseSteps = [
        toStep('Understanding the request', 'Clarify intent and constraints from task context.'),
        toStep('Producing final recommendation', 'Generate the final answer with clear reasoning.')
      ];
    }

    const statusSequence = this.getSyntheticStepStatuses(
      String(task?.status || 'pending'),
      baseSteps.length
    );
    return baseSteps.map((step, index) => ({
      ...step,
      status: statusSequence[index] || String(task?.status || 'pending')
    }));
  }

  getSyntheticStepStatuses(status, count) {
    const normalized = String(status || 'pending')
      .trim()
      .toLowerCase();
    if (count <= 0) return [];

    if (normalized === 'completed') {
      return Array.from({ length: count }, () => 'completed');
    }
    if (normalized === 'in_progress') {
      return Array.from({ length: count }, (_value, index) =>
        index === 0 ? 'in_progress' : 'pending'
      );
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

    storage.setItem(
      TASK_ASSIST_PENDING_SPECIALIST_STORAGE_KEY,
      JSON.stringify({
        workspaceId: this.workspaceId,
        taskId,
        agentName,
        createdAt: Date.now()
      })
    );
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
    const isExpired = !createdAt || Date.now() - createdAt > 15 * 60 * 1000;

    if (!workspaceId || !taskId || !agentName || workspaceId !== this.workspaceId || isExpired) {
      this.clearPendingAssistSpecialistHandoff();
      return;
    }

    const task = this.tasks.find(item => item?.id === taskId);
    const inWorkspace = this.getWorkspaceAgentNames().some(
      name => this.normalizeAgentName(name) === this.normalizeAgentName(agentName)
    );
    if (!task || !inWorkspace) {
      this.clearPendingAssistSpecialistHandoff();
      return;
    }

    this.clearPendingAssistSpecialistHandoff();
    this.openTaskAssistModal(taskId);
    if (this.elements.taskAssistAgent) {
      const hasOption = Array.from(this.elements.taskAssistAgent.options || []).some(
        option =>
          this.normalizeAgentName(option?.value || '') === this.normalizeAgentName(agentName)
      );
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
    sourceNames.forEach(sourceName => {
      const name = String(sourceName || '').trim();
      if (!name) return;
      const normalized = this.normalizeAgentName(name);
      if (!normalized || normalized === exclude) return;

      let score = 0;
      (config.nameTokens || []).forEach(token => {
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

  agentNameMatchesSpecialistConfig(agentName, config) {
    const normalized = this.normalizeAgentName(agentName);
    if (!normalized || !config) return false;
    // Exact match on the canonical specialist name.
    if (normalized === this.normalizeAgentName(config.agentName)) return true;
    // Any domain name token present in the agent name means the agent already
    // covers this specialty (e.g. "Trip Planner" includes the "trip" token), so
    // there's no need to create or switch to a near-duplicate specialist.
    return (config.nameTokens || []).some(token => {
      const normalizedToken = this.normalizeAgentName(token);
      return normalizedToken && normalized.includes(normalizedToken);
    });
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

    const actionMatch =
      /\b(save|create|add|attach|upload|import|export|move|rename|delete|remove|switch|assign|list|show|open|bind)\b/.test(
        normalized
      );
    if (!actionMatch) return false;

    return /\b(note|notes|task|tasks|subtask|subtasks|file|files|pdf|folder|folders|directory|directories|workspace|agent|agents|binding|bindings|canvas)\b/.test(
      normalized
    );
  }

  maybeBuildTravelAssistSpecialistAction(assistContext = {}) {
    const currentAgent = String(assistContext.currentAgent || '').trim();
    const parts = [];
    const taskText = String(
      assistContext.task?.description || assistContext.task?.name || ''
    ).trim();
    if (taskText) parts.push(taskText);
    const taskDetails = String(assistContext.task?.details || '').trim();
    if (taskDetails) parts.push(taskDetails);
    const question = String(assistContext.question || '').trim();
    if (question) parts.push(question);
    const response = String(assistContext.response || '').trim();
    if (response) parts.push(response);

    const workflowStep = assistContext.workflowStep;
    if (workflowStep?.stepType === 'ask_choice' && Array.isArray(workflowStep.choices)) {
      workflowStep.choices.forEach(choice => {
        const label = String(choice?.label || '').trim();
        if (label) parts.push(label);
        const description = String(choice?.description || '').trim();
        if (description) parts.push(description);
      });
    }

    const combined = this.normalizeTaskResultNextStepToken(parts.join(' '));
    if (!combined) return null;
    if (this.isWorkspaceManagerMetaActionText(combined)) return null;

    const travelSignals = [
      'travel',
      'trip',
      'itinerary',
      'hotel',
      'flight',
      'vacation',
      'day trip',
      'day trips',
      'museum',
      'museums',
      'restaurant',
      'restaurants',
      'nightlife',
      'accommodation',
      'lodging'
    ];
    const isTravelContext = travelSignals.some(signal =>
      combined.includes(this.normalizeTaskResultNextStepToken(signal))
    );
    if (!isTravelContext) return null;

    const scoreConfig = config => {
      let score = 0;
      (config.scorePhrases || []).forEach(phrase => {
        const normalizedPhrase = this.normalizeTaskResultNextStepToken(phrase);
        if (!normalizedPhrase || !combined.includes(normalizedPhrase)) return;
        score += normalizedPhrase.includes(' ') ? 3 : 1;
      });
      return score;
    };

    const configs = Object.values(TASK_ASSIST_TRAVEL_SPECIALISTS);
    const ranked = configs
      .map(config => ({ config, score: scoreConfig(config) }))
      .filter(entry => entry.score > 0)
      .sort((a, b) => {
        if (b.score !== a.score) return b.score - a.score;
        const order = ['travel_itinerary', 'hotel_booking', 'flight_booking'];
        return order.indexOf(a.config.key) - order.indexOf(b.config.key);
      });

    const best = ranked[0];
    if (!best?.config) return null;

    const config = best.config;
    // If the task is already assigned to an agent that covers this specialty
    // (e.g. "Trip Planner" for a travel itinerary), don't propose creating or
    // switching to a near-duplicate specialist.
    if (currentAgent && this.agentNameMatchesSpecialistConfig(currentAgent, config)) {
      return null;
    }
    const workspaceAgentName = this.findAssistSpecialistAgentName(
      config,
      this.getWorkspaceAgentNames(),
      {
        excludeAgent: currentAgent
      }
    );
    const catalogNames = Array.isArray(this.agentCatalog)
      ? this.agentCatalog.map(agent => String(agent?.name || '').trim()).filter(Boolean)
      : [];
    const catalogAgent = this.findAssistSpecialistAgentName(config, catalogNames, {
      excludeAgent: currentAgent
    });
    const workspaceAgent =
      workspaceAgentName &&
      catalogAgent &&
      this.normalizeAgentName(workspaceAgentName) === this.normalizeAgentName(catalogAgent)
        ? workspaceAgentName
        : '';

    const agentName = workspaceAgent || catalogAgent || config.agentName;
    const kind = workspaceAgent ? 'switch' : catalogAgent ? 'add_and_switch' : 'create';
    return {
      ...config,
      kind,
      currentAgent,
      agentName,
      copy:
        kind === 'switch'
          ? `"${agentName}" already fits this travel-planning follow-up better than ${currentAgent ? `"${currentAgent}"` : 'the current agent'}.`
          : kind === 'add_and_switch'
            ? `"${agentName}" exists already. Add it to this workspace and hand the task off there.`
            : `This workspace does not have a dedicated travel specialist yet. Create "${agentName}" and use it for this follow-up.`,
      buttonLabel:
        kind === 'switch'
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
        metaItems: [
          taskLabel,
          specialistAction.agentName,
          addToWorkspace ? 'Add to workspace' : 'Travel specialist'
        ],
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
          window.Toast.success(
            addToWorkspace
              ? `Added "${specialistAction.agentName}" and assigned it to this task.`
              : `Assigned "${specialistAction.agentName}" to this task.`
          );
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
    if (normalizedStep.stepType !== 'ask_choice' || !Array.isArray(normalizedStep.choices))
      return normalizedStep;

    const replacementName = String(specialistAction.agentName || '').trim();
    if (!replacementName) return normalizedStep;

    const rewriteLabel = label => {
      const rawLabel = String(label || '').trim();
      if (!rawLabel) return rawLabel;
      if (!/\bworkspace manager\b/i.test(rawLabel)) return rawLabel;
      if (!/\b(delegate|hand off|handoff|assign|planning task)\b/i.test(rawLabel)) return rawLabel;
      return rawLabel
        .replace(/\bthe workspace manager\b/gi, replacementName)
        .replace(/\bworkspace manager\b/gi, replacementName);
    };

    return {
      ...normalizedStep,
      choices: normalizedStep.choices.map(choice => ({
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

    const addOption = agentName => {
      const normalized = String(agentName || '').trim();
      const key = this.normalizeAgentName(normalized);
      if (!normalized || normalized === 'unassigned' || seen.has(key)) return;
      seen.add(key);
      const isCurrent =
        this.normalizeAgentName(normalized) === this.normalizeAgentName(currentAgent);
      const label = isCurrent ? `${normalized} (current)` : normalized;
      options.push(
        `<option value="${this.escapeHtml(normalized)}">${this.escapeHtml(label)}</option>`
      );
    };

    this.getWorkspaceAgentNames().forEach(name => addOption(name));

    select.innerHTML = options.join('');
  }

  parseAssistActions(value) {
    if (Array.isArray(value)) {
      return value
        .map(item =>
          String(item || '')
            .trim()
            .toLowerCase()
        )
        .filter(Boolean);
    }
    if (typeof value === 'string') {
      return value
        .split(',')
        .map(item => item.trim().toLowerCase())
        .filter(Boolean);
    }
    return [];
  }

  cleanAssistChoiceText(value) {
    return this.cleanTaskResultNextStepText(value)
      .replace(/[?!.,;:]+$/g, '')
      .trim();
  }

  buildAssistChoiceId(number, label) {
    const base =
      this.normalizeTaskResultNextStepToken(label).replace(/\s+/g, '-').slice(0, 48) || 'choice';
    return `assist-choice-${String(number || '').trim() || 'x'}-${base}`;
  }

  buildAssistFieldId(label, index) {
    const base =
      this.normalizeTaskResultNextStepToken(label).replace(/\s+/g, '-').slice(0, 48) || 'field';
    return `assist-field-${String(index + 1).trim()}-${base}`;
  }

  getAssistOptionKey(index) {
    const safeIndex = Number.isFinite(index) ? Math.max(0, index) : 0;
    return String.fromCharCode(65 + (safeIndex % 26));
  }

  truncateAssistSummaryText(value, maxLength = 190) {
    const normalized = String(value || '')
      .replace(/\s+/g, ' ')
      .trim();
    if (!normalized) return '';
    if (normalized.length <= maxLength) return normalized;

    const candidate = normalized.slice(0, maxLength - 1);
    const boundary = candidate.lastIndexOf(' ');
    const trimmed =
      boundary >= Math.floor(maxLength * 0.6) ? candidate.slice(0, boundary) : candidate;
    return `${trimmed.trim()}...`;
  }

  extractAssistLeadText(value, maxLength = 190) {
    const raw = String(value || '').trim();
    if (!raw) return '';

    const paragraphs = raw
      .split(/\n\s*\n/)
      .map(part => String(part || '').trim())
      .filter(Boolean);
    const firstChunk = paragraphs[0] || raw;
    const normalized = firstChunk.replace(/\s+/g, ' ').trim();
    if (!normalized) return '';

    const sentences = normalized.split(/(?<=[.!?])\s+/).filter(Boolean);
    const lead = sentences.slice(0, 2).join(' ') || normalized;
    return this.truncateAssistSummaryText(lead, maxLength);
  }

  buildAssistKnownSummary(reason, displayResponse) {
    return (
      this.extractAssistLeadText(displayResponse, 210) ||
      this.truncateAssistSummaryText(reason, 210) ||
      'The task is paused waiting on your input.'
    );
  }

  buildAssistNeedsSummary(workflowStep, question) {
    if (
      workflowStep?.stepType === 'ask_form' &&
      Array.isArray(workflowStep.fields) &&
      workflowStep.fields.length > 0
    ) {
      const labels = workflowStep.fields
        .slice(0, 3)
        .map(field => this.getAssistFormFieldPrompt(field).replace(/[?]+$/g, '').trim())
        .filter(Boolean);
      const count = workflowStep.fields.length;
      const suffix = count > labels.length ? `, and ${count - labels.length} more` : '';
      if (labels.length > 0) {
        return this.truncateAssistSummaryText(
          `Answer ${count} question${count === 1 ? '' : 's'} about ${labels.join('; ')}${suffix}.`,
          210
        );
      }
      return `Answer ${count} question${count === 1 ? '' : 's'} so the task can continue.`;
    }

    if (
      workflowStep?.stepType === 'ask_choice' &&
      Array.isArray(workflowStep.choices) &&
      workflowStep.choices.length > 0
    ) {
      return this.truncateAssistSummaryText(
        `Choose 1 of ${workflowStep.choices.length} suggested next steps to unblock the task.`,
        210
      );
    }

    if (question) {
      return this.truncateAssistSummaryText(question, 210);
    }

    return 'Provide the missing details the agent asked for.';
  }

  buildAssistNextSummary(recommendation, workflowStep, currentAgent = '') {
    const recommendedText = String(recommendation?.text || '')
      .trim()
      .replace(/^Recommended:\s*/i, '');
    if (recommendedText) {
      return this.truncateAssistSummaryText(recommendedText, 210);
    }

    if (workflowStep?.stepType === 'ask_form') {
      return currentAgent
        ? this.truncateAssistSummaryText(
            `After you answer, ${currentAgent} continues the task with your details.`,
            210
          )
        : 'After you answer, the task resumes with your details.';
    }

    if (workflowStep?.stepType === 'ask_choice') {
      return 'After you choose an option, the task resumes using that direction.';
    }

    return 'After you respond, Ori uses that input to continue the task.';
  }

  setAssistSummaryUI(summary) {
    const wrap = this.elements.taskAssistSummaryWrap;
    const known = this.elements.taskAssistSummaryKnown;
    const needs = this.elements.taskAssistSummaryNeeds;
    const next = this.elements.taskAssistSummaryNext;
    if (!wrap || !known || !needs || !next) return;

    const knownText = String(summary?.known || '').trim();
    const needsText = String(summary?.needs || '').trim();
    const nextText = String(summary?.next || '').trim();
    const shouldShow = Boolean(knownText || needsText || nextText);

    known.textContent = knownText || 'No context summary yet.';
    needs.textContent = needsText || 'No decision summary yet.';
    next.textContent = nextText || 'No continuation summary yet.';
    wrap.classList.toggle('d-none', !shouldShow);
  }

  shouldCollapseAssistResponse(fullResponse) {
    const text = String(fullResponse || '').trim();
    if (!text) return false;
    const lineCount = text.split(/\r?\n/).filter(line => String(line || '').trim()).length;
    return text.length > 420 || lineCount > 8;
  }

  buildAssistResponsePreview(fullResponse, displayResponse = '') {
    const source = String(displayResponse || fullResponse || '').trim();
    if (!source) return '';
    return this.truncateAssistSummaryText(source.replace(/\s+/g, ' '), 280);
  }

  renderAssistResponseState() {
    const responseWrap = this.elements.taskAssistResponseWrap;
    const responsePreview = this.elements.taskAssistResponsePreview;
    const responseToggle = this.elements.taskAssistResponseToggle;
    const response = this.elements.taskAssistResponse;
    if (!responseWrap || !response) return;

    const fullText = String(response.textContent || '').trim();
    if (!fullText) {
      responseWrap.classList.add('d-none');
      response.classList.add('d-none');
      responsePreview?.classList.add('d-none');
      responseToggle?.classList.add('d-none');
      return;
    }

    const previewText = String(responsePreview?.textContent || '').trim();
    const collapsible = this.shouldCollapseAssistResponse(fullText) && Boolean(previewText);
    const expanded = collapsible ? this.taskAssistResponseExpanded : true;

    responseWrap.classList.remove('d-none');
    response.classList.toggle('d-none', !expanded);
    responsePreview?.classList.toggle('d-none', expanded || !collapsible);

    if (responseToggle) {
      responseToggle.classList.toggle('d-none', !collapsible);
      responseToggle.textContent = expanded ? 'Hide full request' : 'View full request';
      responseToggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    }
  }

  setAssistResponseUI(fullResponse, displayResponse = '') {
    const response = this.elements.taskAssistResponse;
    const responsePreview = this.elements.taskAssistResponsePreview;
    if (!response) return;

    const fullText = String(fullResponse || displayResponse || '').trim();
    const previewText = this.buildAssistResponsePreview(fullText, displayResponse);
    this.taskAssistResponseExpanded = !this.shouldCollapseAssistResponse(fullText);

    response.textContent = fullText;
    if (responsePreview) {
      responsePreview.textContent = previewText;
    }
    this.renderAssistResponseState();
  }

  toggleAssistResponseExpanded(forceValue = null) {
    const fullText = String(this.elements.taskAssistResponse?.textContent || '').trim();
    if (!this.shouldCollapseAssistResponse(fullText)) return;
    this.taskAssistResponseExpanded =
      typeof forceValue === 'boolean' ? forceValue : !this.taskAssistResponseExpanded;
    this.renderAssistResponseState();
  }

  normalizeAssistFieldType(value) {
    const normalized = String(value || '')
      .trim()
      .toLowerCase();
    if (
      normalized === 'textarea' ||
      normalized === 'select' ||
      normalized === 'number' ||
      normalized === 'boolean'
    ) {
      return normalized;
    }
    return 'text';
  }

  normalizeAssistFieldOptions(options) {
    if (!Array.isArray(options)) return [];
    return options
      .map(option => {
        if (typeof option === 'string') {
          const label = String(option || '').trim();
          if (!label) return null;
          return { value: label, label, description: '', key: '' };
        }

        const label = String(option?.label || option?.value || '').trim();
        const value = String(option?.value || option?.label || '').trim();
        if (!label || !value) return null;
        return {
          value,
          label,
          description: String(option?.description || '').trim(),
          key: String(option?.key || '').trim()
        };
      })
      .filter(Boolean);
  }

  splitAssistOptionEvidence(label) {
    const cleaned = this.cleanAssistChoiceText(label);
    if (!cleaned) {
      return { label: '', description: '' };
    }

    const start = cleaned.lastIndexOf('(');
    const end = cleaned.lastIndexOf(')');
    if (start >= 0 && end > start + 1 && end === cleaned.length - 1) {
      const optionLabel = this.cleanAssistChoiceText(cleaned.slice(0, start));
      const description = this.cleanAssistChoiceText(cleaned.slice(start + 1, end));
      if (optionLabel && description) {
        return {
          label: optionLabel,
          description: /[.!?]$/.test(description) ? description : `${description}.`
        };
      }
    }

    return { label: cleaned, description: '' };
  }

  deriveAssistFieldEvidence(options) {
    if (!Array.isArray(options) || options.length === 0) return '';

    const seen = new Set();
    const evidence = [];
    options.forEach(option => {
      const description = String(option?.description || '').trim();
      if (!description) return;
      const key = description.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      evidence.push(description);
    });

    return evidence.slice(0, 2).join(' ');
  }

  normalizeAssistFieldValues(values) {
    if (!Array.isArray(values)) return {};
    return values.reduce((accumulator, item) => {
      const id = String(item?.id || '').trim();
      const value = String(item?.value || '').trim();
      if (!id || !value) return accumulator;
      accumulator[id] = value;
      return accumulator;
    }, {});
  }

  normalizeAssistWorkflowStep(value) {
    if (!value || typeof value !== 'object') return null;

    const stepType = String(value.step_type || value.stepType || '')
      .trim()
      .toLowerCase();
    let freeTextAllowed = true;
    if (typeof value.free_text_allowed === 'boolean') {
      freeTextAllowed = value.free_text_allowed;
    } else if (typeof value.freeTextAllowed === 'boolean') {
      freeTextAllowed = value.freeTextAllowed;
    }

    if (stepType === 'ask_choice') {
      const rawChoices = Array.isArray(value.choices) ? value.choices : [];
      const choices = rawChoices
        .map((item, index) => {
          const label = this.cleanAssistChoiceText(item?.label || '');
          if (!label) return null;
          const number = String(item?.number || index + 1).trim();
          return {
            id: String(item?.id || this.buildAssistChoiceId(number, label)).trim(),
            label,
            number,
            description: this.cleanAssistChoiceText(item?.description || '')
          };
        })
        .filter(Boolean);

      if (choices.length === 0) return null;

      return {
        stepType: 'ask_choice',
        title: String(value.title || '').trim() || 'Choose the next step',
        summary: String(value.summary || '').trim(),
        freeTextAllowed,
        choices
      };
    }

    if (stepType === 'ask_form') {
      const rawFields = Array.isArray(value.fields) ? value.fields : [];
      const fields = rawFields
        .map((item, index) => {
          const label = String(item?.label || '').trim();
          if (!label) return null;
          return {
            id: String(item?.id || this.buildAssistFieldId(label, index)).trim(),
            label,
            description: String(item?.description || '').trim(),
            evidence: String(item?.evidence || '').trim(),
            type: this.normalizeAssistFieldType(item?.type),
            placeholder: String(item?.placeholder || '').trim(),
            required: item?.required !== false,
            options: this.normalizeAssistFieldOptions(item?.options)
          };
        })
        .filter(Boolean);

      if (fields.length === 0) return null;

      return {
        stepType: 'ask_form',
        title: String(value.title || '').trim() || 'Provide the missing details',
        summary: String(value.summary || '').trim(),
        freeTextAllowed,
        fields
      };
    }

    return null;
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
        if (
          !first ||
          !second ||
          this.normalizeTaskResultNextStepToken(first) ===
            this.normalizeTaskResultNextStepToken(second)
        ) {
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

  extractAssistQuestionBlocks(text) {
    const lines = String(text || '').split(/\r?\n/);
    const blocks = [];

    for (let index = 0; index < lines.length; ) {
      const questionMatch = String(lines[index] || '').match(/^\s*(\d+)[.)]\s*(.+?)\s*$/);
      if (!questionMatch) {
        index += 1;
        continue;
      }

      const question = this.cleanAssistChoiceText(questionMatch[2]);
      if (!question) {
        index += 1;
        continue;
      }

      const options = [];
      let cursor = index + 1;
      for (; cursor < lines.length; cursor += 1) {
        const rawLine = String(lines[cursor] || '');
        if (/^\s*\d+[.)]\s+/.test(rawLine)) {
          break;
        }

        const optionMatch = rawLine.match(/^\s*(?:[-*]\s*)?([A-Z])[.)]\s+(.+)$/);
        if (optionMatch) {
          const splitOption = this.splitAssistOptionEvidence(optionMatch[2]);
          const label = splitOption.label;
          if (!label) continue;
          options.push({
            key: String(optionMatch[1] || '')
              .trim()
              .toUpperCase(),
            label,
            description: splitOption.description
          });
          continue;
        }

        if (!rawLine.trim()) {
          continue;
        }

        if (options.length === 0) {
          break;
        }

        const continuation = this.cleanTaskResultNextStepText(rawLine);
        if (!continuation) {
          continue;
        }

        const lastOption = options[options.length - 1];
        const splitOption = this.splitAssistOptionEvidence(
          `${lastOption.label} ${continuation}`.replace(/\s+/g, ' ').trim()
        );
        lastOption.label = splitOption.label;
        lastOption.description = splitOption.description || lastOption.description;
      }

      if (options.length >= 2) {
        blocks.push({
          number: String(questionMatch[1] || '').trim(),
          question: question.endsWith('?') ? question : `${question}?`,
          options
        });
        index = cursor;
        continue;
      }

      index += 1;
    }

    return blocks;
  }

  extractAssistQuestions(text) {
    const normalized = String(text || '')
      .replace(/\s+/g, ' ')
      .trim();
    if (!normalized || !normalized.includes('?')) {
      return [];
    }

    return normalized
      .split('?')
      .slice(0, -1)
      .map(item => String(item || '').trim())
      .filter(item => item.length >= 5 && item.length <= 220)
      .map(item => `${item}?`);
  }

  extractAssistFieldHint(question, fallback = '') {
    const match = String(question || '').match(/\(([^)]+)\)/);
    if (match && match[1]) {
      return String(match[1] || '').trim();
    }
    return fallback;
  }

  buildAssistFieldFromQuestion(question, index) {
    const rawQuestion = String(question || '').trim();
    const cleanedQuestion = this.cleanAssistChoiceText(rawQuestion);
    if (!cleanedQuestion) return null;

    const lower = cleanedQuestion.toLowerCase();
    const field = {
      id: this.buildAssistFieldId(cleanedQuestion, index),
      label: cleanedQuestion,
      description: rawQuestion,
      evidence: '',
      type: 'text',
      placeholder: '',
      required: true,
      options: []
    };

    if (
      lower.includes('freestanding') &&
      (lower.includes('wall-mounted') || lower.includes('wall mounted'))
    ) {
      field.id = 'mounting_type';
      field.label = 'Mounting type';
      field.type = 'select';
      field.options = [
        { value: 'freestanding', label: 'Freestanding', description: '' },
        { value: 'wall-mounted', label: 'Wall-mounted', description: '' }
      ];
      return field;
    }

    if (
      lower.includes('specific room') ||
      lower.includes('which room') ||
      lower.includes('what room') ||
      lower.includes(' room')
    ) {
      field.id = 'room';
      field.label = 'Room';
      field.placeholder = this.extractAssistFieldHint(
        rawQuestion,
        'Bathroom, kitchen, living room'
      );
      return field;
    }

    if (lower.includes('tools')) {
      field.id = 'available_tools';
      field.label = 'Available tools';
      field.type = 'textarea';
      field.placeholder = this.extractAssistFieldHint(rawQuestion, 'Saw, drill, square');
      return field;
    }

    if (lower.includes('how many') && lower.includes('shel')) {
      field.id = 'shelf_count';
      field.label = 'Shelf count';
      field.type = 'number';
      field.placeholder = '3';
      return field;
    }

    if (lower.includes('material')) {
      field.id = 'material';
      field.label = 'Material';
      field.placeholder = this.extractAssistFieldHint(rawQuestion, 'Plywood, pine, MDF');
      return field;
    }

    if (
      lower.includes("what's it holding") ||
      lower.includes('what is it holding') ||
      lower.includes('what will it hold')
    ) {
      field.id = 'intended_load';
      field.label = 'What it will hold';
      field.placeholder = 'Books, decor, kitchen items';
      return field;
    }

    return field;
  }

  buildAssistFieldFromQuestionBlock(block, index) {
    if (!block || !Array.isArray(block.options) || block.options.length < 2) {
      return null;
    }

    const field = this.buildAssistFieldFromQuestion(block.question, index) || {
      id: this.buildAssistFieldId(block.question, index),
      label: this.cleanAssistChoiceText(block.question),
      description: String(block.question || '').trim(),
      evidence: '',
      type: 'text',
      placeholder: '',
      required: true,
      options: []
    };

    field.type = 'select';
    field.description = String(block.question || '').trim();
    field.evidence = this.deriveAssistFieldEvidence(block.options);
    field.options = block.options
      .map((option, optionIndex) => {
        const label = String(option?.label || '').trim();
        if (!label) return null;
        return {
          value: label,
          label,
          description: String(option?.description || '').trim(),
          key: String(option?.key || this.getAssistOptionKey(optionIndex)).trim()
        };
      })
      .filter(Boolean);
    return field.options.length >= 2 ? field : null;
  }

  buildAssistWorkflowStepFromText(...values) {
    for (const value of values) {
      const questionBlocks = this.extractAssistQuestionBlocks(value);
      if (questionBlocks.length > 0) {
        const fields = questionBlocks
          .map((block, index) => this.buildAssistFieldFromQuestionBlock(block, index))
          .filter(Boolean);
        if (fields.length > 0) {
          return {
            stepType: 'ask_form',
            title: 'Provide the missing details',
            summary: 'Answer the questions below so the task can continue.',
            freeTextAllowed: true,
            fields
          };
        }
      }

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

      const questions = this.extractAssistQuestions(value);
      if (questions.length >= 2) {
        const fields = questions
          .map((question, index) => this.buildAssistFieldFromQuestion(question, index))
          .filter(Boolean);
        if (fields.length > 0) {
          return {
            stepType: 'ask_form',
            title: 'Provide the missing details',
            summary: 'Answer the questions below so the task can continue.',
            freeTextAllowed: true,
            fields
          };
        }
      }
    }
    return null;
  }

  trimAssistQuestionnaireFromResponse(response, workflowStep = null) {
    const text = String(response || '').trim();
    if (!text || workflowStep?.stepType !== 'ask_form') {
      return text;
    }

    const questionBlocks = this.extractAssistQuestionBlocks(text);
    if (questionBlocks.length === 0) {
      return text;
    }

    const lines = text.split(/\r?\n/);
    const startIndex = lines.findIndex(line => /^\s*\d+[.)]\s+/.test(String(line || '')));
    if (startIndex < 0) {
      return text;
    }

    return lines.slice(0, startIndex).join('\n').trim();
  }

  getAssistResponseDisplayText(response, question, workflowStep = null) {
    const rawResponse = String(response || '').trim();
    const rawQuestion = String(question || '').trim();
    if (!rawResponse) return '';
    if (!rawQuestion) return this.trimAssistQuestionnaireFromResponse(rawResponse, workflowStep);

    const normalize = value =>
      this.cleanTaskResultNextStepText(value)
        .replace(/[?!.,;:]+$/g, '')
        .toLowerCase();

    const normalizedQuestion = normalize(rawQuestion);
    if (!normalizedQuestion)
      return this.trimAssistQuestionnaireFromResponse(rawResponse, workflowStep);

    const paragraphs = rawResponse
      .split(/\n\s*\n/)
      .map(part => String(part || '').trim())
      .filter(Boolean);
    if (
      paragraphs.length > 0 &&
      normalize(paragraphs[paragraphs.length - 1]) === normalizedQuestion
    ) {
      paragraphs.pop();
      return this.trimAssistQuestionnaireFromResponse(paragraphs.join('\n\n').trim(), workflowStep);
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
      return this.trimAssistQuestionnaireFromResponse(lines.join('\n').trim(), workflowStep);
    }

    return this.trimAssistQuestionnaireFromResponse(rawResponse, workflowStep);
  }

  setAssistWorkflowStepUI(workflowStep) {
    const normalizedStep = this.normalizeAssistWorkflowStep(workflowStep);
    if (!normalizedStep) {
      this.clearAssistChoiceStepUI();
      this.clearAssistFormStepUI();
      return;
    }

    if (normalizedStep.stepType === 'ask_choice') {
      this.setAssistChoiceStepUI(normalizedStep);
      this.clearAssistFormStepUI();
      return;
    }

    if (normalizedStep.stepType === 'ask_form') {
      this.clearAssistChoiceStepUI();
      this.setAssistFormStepUI(normalizedStep);
      return;
    }

    this.clearAssistChoiceStepUI();
    this.clearAssistFormStepUI();
  }

  clearAssistChoiceStepUI() {
    if (!this.elements.taskAssistChoiceWrap || !this.elements.taskAssistChoiceList) return;

    this.elements.taskAssistChoiceList.innerHTML = '';
    if (this.elements.taskAssistChoiceSummary) {
      this.elements.taskAssistChoiceSummary.textContent = '';
      this.elements.taskAssistChoiceSummary.classList.add('d-none');
    }
    this.elements.taskAssistChoiceWrap.classList.add('d-none');
  }

  setAssistChoiceStepUI(normalizedStep) {
    if (
      !this.elements.taskAssistChoiceWrap ||
      !this.elements.taskAssistChoiceList ||
      !Array.isArray(normalizedStep?.choices)
    )
      return;

    if (normalizedStep.choices.length === 0) {
      this.clearAssistChoiceStepUI();
      return;
    }

    const selectedChoiceId = String(this.currentBlockedTask?.selectedChoiceId || '').trim();
    this.elements.taskAssistChoiceList.innerHTML = normalizedStep.choices
      .map(
        choice => `
      <button
        type="button"
        class="home-assistant-planning-choice${selectedChoiceId === choice.id ? ' is-selected' : ''}"
        data-assist-choice-id="${this.escapeHtml(choice.id)}"
        aria-pressed="${selectedChoiceId === choice.id ? 'true' : 'false'}"
      >
        <span class="home-assistant-planning-choice-label">${this.escapeHtml(choice.number ? `${choice.number}. ${choice.label}` : choice.label)}</span>
        ${choice.description ? `<span class="home-assistant-planning-choice-hint">${this.escapeHtml(choice.description)}</span>` : ''}
      </button>
    `
      )
      .join('');

    this.elements.taskAssistChoiceList
      .querySelectorAll('[data-assist-choice-id]')
      .forEach(button => {
        button.addEventListener('click', () => {
          const choiceId = String(button.getAttribute('data-assist-choice-id') || '').trim();
          if (!choiceId || !this.currentBlockedTask) return;
          const selectedChoice = normalizedStep.choices.find(choice => choice.id === choiceId);
          if (!selectedChoice) return;
          this.currentBlockedTask.selectedChoiceId = selectedChoice.id;
          this.currentBlockedTask.selectedChoiceLabel = selectedChoice.label;
          this.currentBlockedTask.selectedChoiceNumber = selectedChoice.number || '';
          this.currentBlockedTask.workflowStep = normalizedStep;
          this.setAssistChoiceStepUI(normalizedStep);
        });
      });

    if (this.elements.taskAssistChoiceSummary) {
      const summary = String(normalizedStep.summary || '').trim();
      this.elements.taskAssistChoiceSummary.textContent = summary;
      this.elements.taskAssistChoiceSummary.classList.toggle('d-none', !summary);
    }

    this.elements.taskAssistChoiceWrap.classList.remove('d-none');
  }

  clearAssistFormStepUI() {
    if (!this.elements.taskAssistFormWrap || !this.elements.taskAssistFormFields) return;

    this.elements.taskAssistFormFields.innerHTML = '';
    if (this.elements.taskAssistFormSummary) {
      this.elements.taskAssistFormSummary.textContent = '';
      this.elements.taskAssistFormSummary.classList.add('d-none');
    }
    this.elements.taskAssistFormWrap.classList.add('d-none');
  }

  getAssistFormFieldPrompt(field) {
    const description = String(field?.description || '').trim();
    if (description) {
      return description.endsWith('?') ? description : `${description}?`;
    }

    const label = String(field?.label || '').trim();
    if (!label) return '';
    return label.endsWith('?') ? label : `${label}?`;
  }

  setAssistFormFieldValue(fieldId, value) {
    if (!this.currentBlockedTask || !fieldId) return;

    if (
      !this.currentBlockedTask.selectedFieldValues ||
      typeof this.currentBlockedTask.selectedFieldValues !== 'object'
    ) {
      this.currentBlockedTask.selectedFieldValues = {};
    }

    const normalizedValue = String(value || '').trim();
    if (!normalizedValue) {
      delete this.currentBlockedTask.selectedFieldValues[fieldId];
      return;
    }

    this.currentBlockedTask.selectedFieldValues[fieldId] = normalizedValue;
  }

  collectAssistFormFieldValues(step = this.currentBlockedTask?.workflowStep) {
    const normalizedStep = this.normalizeAssistWorkflowStep(step);
    if (
      !normalizedStep ||
      normalizedStep.stepType !== 'ask_form' ||
      !Array.isArray(normalizedStep.fields)
    ) {
      return [];
    }

    const selectedValues =
      this.currentBlockedTask?.selectedFieldValues &&
      typeof this.currentBlockedTask.selectedFieldValues === 'object'
        ? this.currentBlockedTask.selectedFieldValues
        : {};

    return normalizedStep.fields
      .map(field => {
        const id = String(field?.id || '').trim();
        const label = String(field?.label || '').trim();
        const value = String(selectedValues[id] || '').trim();
        if (!id || !value) return null;
        return { id, label, value };
      })
      .filter(Boolean);
  }

  setAssistFormStepUI(normalizedStep) {
    if (
      !this.elements.taskAssistFormWrap ||
      !this.elements.taskAssistFormFields ||
      !Array.isArray(normalizedStep?.fields)
    )
      return;

    if (normalizedStep.fields.length === 0) {
      this.clearAssistFormStepUI();
      return;
    }

    const selectedValues =
      this.currentBlockedTask?.selectedFieldValues &&
      typeof this.currentBlockedTask.selectedFieldValues === 'object'
        ? this.currentBlockedTask.selectedFieldValues
        : {};

    this.elements.taskAssistFormFields.innerHTML = normalizedStep.fields
      .map((field, fieldIndex) => {
        const fieldId = String(field.id || '').trim();
        const label = String(field.label || '').trim();
        const description = String(field.description || '').trim();
        const evidence = String(field.evidence || '').trim();
        const placeholder = String(field.placeholder || '').trim();
        const prompt = this.getAssistFormFieldPrompt(field);
        const requiredMark = field.required ? ' <span aria-hidden="true">*</span>' : '';
        const currentValue = String(selectedValues[fieldId] || '').trim();
        const value = this.escapeHtml(currentValue);
        const hint =
          description && description !== prompt
            ? `<div class="workspace-detail-task-assist-form-hint">${this.escapeHtml(description)}</div>`
            : '';
        const evidenceMarkup = evidence
          ? `<div class="workspace-detail-task-assist-form-evidence">${this.escapeHtml(evidence)}</div>`
          : '';
        const questionIntro = `
        <div class="workspace-detail-task-assist-form-question">
          <span class="workspace-detail-task-assist-form-number">${fieldIndex + 1}</span>
          <div class="workspace-detail-task-assist-form-question-copy">
            <div class="workspace-detail-task-assist-form-prompt">${this.escapeHtml(prompt || label)}</div>
            ${hint}
            ${evidenceMarkup}
          </div>
        </div>
      `;

        if (field.type === 'textarea') {
          return `
          <div class="workspace-detail-task-assist-form-field">
            ${questionIntro}
            <label class="form-label" for="workspace-detail-task-assist-field-${this.escapeHtml(fieldId)}">${this.escapeHtml(label)}${requiredMark}</label>
            <textarea id="workspace-detail-task-assist-field-${this.escapeHtml(fieldId)}" class="form-control" rows="3" data-assist-field-id="${this.escapeHtml(fieldId)}" placeholder="${this.escapeHtml(placeholder)}">${value}</textarea>
          </div>
        `;
        }

        if (field.type === 'select') {
          const options = Array.isArray(field.options) ? field.options : [];
          const optionsMarkup = options
            .map((option, optionIndex) => {
              const optionValue = String(option?.value || '').trim();
              const optionLabel = String(option?.label || option?.value || '').trim();
              const optionDescription = String(option?.description || '').trim();
              const optionKey = String(option?.key || '').trim();
              if (!optionValue || !optionLabel) return '';
              const checked = currentValue === optionValue ? ' checked' : '';
              return `
            <label class="workspace-detail-task-assist-option">
              <input
                class="workspace-detail-task-assist-option-input"
                type="radio"
                name="workspace-detail-task-assist-field-${this.escapeHtml(fieldId)}"
                value="${this.escapeHtml(optionValue)}"
                data-assist-field-id="${this.escapeHtml(fieldId)}"${checked}
              >
              <span class="workspace-detail-task-assist-option-card">
                <span class="workspace-detail-task-assist-option-key">${this.escapeHtml(optionKey || this.getAssistOptionKey(optionIndex))}</span>
                <span class="workspace-detail-task-assist-option-copy">
                  <span class="workspace-detail-task-assist-option-label">${this.escapeHtml(optionLabel)}</span>
                  ${optionDescription ? `<span class="workspace-detail-task-assist-option-description">${this.escapeHtml(optionDescription)}</span>` : ''}
                </span>
              </span>
            </label>
          `;
            })
            .join('');

          return `
          <div class="workspace-detail-task-assist-form-field">
            ${questionIntro}
            <div class="workspace-detail-task-assist-option-group" role="radiogroup" aria-label="${this.escapeHtml(prompt || label)}${field.required ? ' (required)' : ''}">
              ${optionsMarkup}
            </div>
          </div>
        `;
        }

        const inputType = field.type === 'number' ? 'number' : 'text';
        return `
        <div class="workspace-detail-task-assist-form-field">
          ${questionIntro}
          <label class="form-label" for="workspace-detail-task-assist-field-${this.escapeHtml(fieldId)}">${this.escapeHtml(label)}${requiredMark}</label>
          <input id="workspace-detail-task-assist-field-${this.escapeHtml(fieldId)}" class="form-control" type="${inputType}" data-assist-field-id="${this.escapeHtml(fieldId)}" value="${value}" placeholder="${this.escapeHtml(placeholder)}">
        </div>
      `;
      })
      .join('');

    this.elements.taskAssistFormFields
      .querySelectorAll('[data-assist-field-id]')
      .forEach(fieldElement => {
        const syncValue = () => {
          const fieldId = String(fieldElement.getAttribute('data-assist-field-id') || '').trim();
          if (!fieldId) return;
          if (
            fieldElement instanceof HTMLInputElement &&
            fieldElement.type === 'radio' &&
            !fieldElement.checked
          ) {
            return;
          }
          this.setAssistFormFieldValue(fieldId, fieldElement.value);
        };
        fieldElement.addEventListener('input', syncValue);
        fieldElement.addEventListener('change', syncValue);
      });

    if (this.elements.taskAssistFormSummary) {
      const summary = String(normalizedStep.summary || '').trim();
      this.elements.taskAssistFormSummary.textContent = summary;
      this.elements.taskAssistFormSummary.classList.toggle('d-none', !summary);
    }

    this.elements.taskAssistFormWrap.classList.remove('d-none');
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
    ].forEach(button => {
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
      const hasOption = Array.from(this.elements.taskAssistAgent.options || []).some(
        option => String(option?.value || '') === recommendation.suggestedAgent
      );
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

  determineAssistRecommendation(
    reasonCode,
    suggestedActions,
    currentAgent,
    workflowStep = null,
    assistContext = {}
  ) {
    const normalizedCode = String(reasonCode || '')
      .trim()
      .toLowerCase();
    const actions = this.parseAssistActions(suggestedActions);
    const allows = action => actions.length === 0 || actions.includes(action);
    const browserRelated =
      normalizedCode === 'capability_mismatch' ||
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
        action:
          specialistAction.kind === 'switch' && allows('switch_agent_retry')
            ? 'switch_agent_retry'
            : '',
        suggestedAgent: specialistAction.kind === 'switch' ? specialistAction.agentName : '',
        text: this.buildAssistSpecialistActionText(specialistAction),
        specialistAction
      };
    }

    if (
      workflowStep?.stepType === 'ask_choice' &&
      Array.isArray(workflowStep.choices) &&
      workflowStep.choices.length > 0 &&
      allows('continue_with_instruction')
    ) {
      return {
        action: 'continue_with_instruction',
        text: 'Recommended: Choose one of the suggested next steps below or add your own guidance.'
      };
    }

    if (
      workflowStep?.stepType === 'ask_form' &&
      Array.isArray(workflowStep.fields) &&
      workflowStep.fields.length > 0 &&
      allows('continue_with_instruction')
    ) {
      return {
        action: 'continue_with_instruction',
        text: 'Recommended: Answer the questions below and continue.'
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

  getBlockedTaskRouteId() {
    const params = new URLSearchParams(window.location.search);
    return String(params.get('blocked_task') || '').trim();
  }

  updateBlockedTaskRoute(taskId = '', { replace = false } = {}) {
    const nextTaskId = String(taskId || '').trim();
    const url = new URL(window.location.href);

    if (nextTaskId) {
      url.searchParams.set('blocked_task', nextTaskId);
    } else {
      url.searchParams.delete('blocked_task');
    }

    const nextHref = `${url.pathname}${url.search}${url.hash}`;
    const currentHref = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    if (nextHref === currentHref) {
      return;
    }

    const state = {
      ...(window.history.state && typeof window.history.state === 'object'
        ? window.history.state
        : {}),
      blockedTaskId: nextTaskId || null
    };
    if (replace) {
      window.history.replaceState(state, '', nextHref);
    } else {
      window.history.pushState(state, '', nextHref);
    }
  }

  isTaskAssistPageVisible() {
    return Boolean(this.elements.taskAssistPage && !this.elements.taskAssistPage.hidden);
  }

  scrollTaskAssistSurfaceIntoView(target = null) {
    const surface = target || this.elements.taskAssistPage || this.elements.workspaceDetailView;
    if (!surface) {
      window.scrollTo({ top: 0, left: 0, behavior: 'auto' });
      return;
    }

    const top = Math.max(0, window.scrollY + surface.getBoundingClientRect().top - 12);
    window.scrollTo({ top, left: 0, behavior: 'auto' });
  }

  scheduleTaskAssistScrollReset(target = null) {
    const surface = target || this.elements.taskAssistPage;
    if (!surface) return;

    try {
      if (window.history && 'scrollRestoration' in window.history) {
        window.history.scrollRestoration = 'manual';
      }
    } catch (_error) {
      // Ignore browsers that block scroll restoration changes.
    }

    const reset = () => {
      this.scrollTaskAssistSurfaceIntoView(surface);
      document.documentElement.scrollTop = 0;
      document.body.scrollTop = 0;
    };

    reset();
    window.requestAnimationFrame(reset);
    window.setTimeout(reset, 0);
    window.setTimeout(reset, 80);
    window.setTimeout(reset, 220);
  }

  showTaskAssistPage({ taskId = '', updateRoute = true, replaceRoute = false } = {}) {
    if (this.elements.workspaceDetailView) {
      this.elements.workspaceDetailView.hidden = true;
    }
    if (this.elements.taskAssistPage) {
      this.elements.taskAssistPage.hidden = false;
    }
    if (updateRoute) {
      this.updateBlockedTaskRoute(taskId || this.currentBlockedTask?.taskId || '', {
        replace: replaceRoute
      });
    }
    this.scheduleTaskAssistScrollReset(this.elements.taskAssistPage);
  }

  closeTaskAssistPage({ updateRoute = true, replaceRoute = true, preserveState = false } = {}) {
    if (this.elements.workspaceDetailView) {
      this.elements.workspaceDetailView.hidden = false;
    }
    if (this.elements.taskAssistPage) {
      this.elements.taskAssistPage.hidden = true;
    }
    if (updateRoute) {
      this.updateBlockedTaskRoute('', { replace: replaceRoute });
    }
    if (!preserveState) {
      this.currentBlockedTask = null;
      this.currentAssistRecommendation = null;
      this.currentAssistSpecialistAction = null;
    }
    try {
      if (window.history && 'scrollRestoration' in window.history) {
        window.history.scrollRestoration = 'auto';
      }
    } catch (_error) {
      // Ignore browsers that block scroll restoration changes.
    }
    this.scrollTaskAssistSurfaceIntoView(this.elements.workspaceDetailView);
  }

  restoreTaskAssistPageFromRoute() {
    const routeTaskId = this.getBlockedTaskRouteId();
    const activeTaskId = String(this.currentBlockedTask?.taskId || '').trim();

    if (!routeTaskId) {
      if (this.isTaskAssistPageVisible() || activeTaskId) {
        this.closeTaskAssistPage({ updateRoute: false, replaceRoute: true });
      }
      return false;
    }

    const task = this.tasks.find(item => item?.id === routeTaskId);
    if (!task || !this.getTaskStatusPresentation(task).isBlocked) {
      this.closeTaskAssistPage({ updateRoute: true, replaceRoute: true });
      return false;
    }

    if (this.isTaskAssistPageVisible() && activeTaskId === routeTaskId) {
      return true;
    }

    this.openTaskAssistModal(routeTaskId, null, { updateRoute: false, replaceRoute: true });
    return true;
  }

  openTaskAssistModal(taskId, eventData = null, options = {}) {
    const task = this.tasks.find(item => item.id === taskId);
    if (!task) return;

    const humanLoop = this.getTaskHumanLoop(task) || {};
    const payload = eventData && typeof eventData === 'object' ? eventData : {};
    const payloadHumanLoop =
      payload.human_loop && typeof payload.human_loop === 'object' ? payload.human_loop : {};
    const blockId = String(payload.block_id || humanLoop.block_id || '').trim();
    const reasonCode = String(
      payload.reason_code || payloadHumanLoop.reason_code || humanLoop.reason_code || ''
    ).trim();
    const suggestedActions =
      payload.suggested_actions ||
      payloadHumanLoop.suggested_actions ||
      humanLoop.suggested_actions;
    const reason = String(
      payload.reason ||
        payloadHumanLoop.reason ||
        humanLoop.reason ||
        'The assigned agent needs guidance before it can continue.'
    ).trim();
    const question = String(
      payload.question || payloadHumanLoop.question || humanLoop.question || 'How should I proceed?'
    ).trim();
    const response = String(
      payload.agent_response || payloadHumanLoop.agent_response || humanLoop.agent_response || ''
    ).trim();
    const statusText = getDisplayStatus(task.status);
    const timestamp = formatDate(task.updated_at || task.created_at);
    const selectedFieldValues = this.normalizeAssistFieldValues(
      payload.field_values || payloadHumanLoop.field_values || humanLoop.field_values
    );

    const currentAgent = String(task.to || '').trim();
    const baseWorkflowStep =
      this.normalizeAssistWorkflowStep(payload.workflow_step) ||
      this.normalizeAssistWorkflowStep(payloadHumanLoop.workflow_step) ||
      this.normalizeAssistWorkflowStep(humanLoop.workflow_step) ||
      this.normalizeAssistWorkflowStep(task?.context?.planning_workflow_step) ||
      this.buildAssistWorkflowStepFromText(question, response);
    const displayResponse = this.getAssistResponseDisplayText(response, question, baseWorkflowStep);
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
    const workflowStep = this.applyAssistWorkflowSpecialistOverrides(
      baseWorkflowStep,
      recommendation?.specialistAction
    );
    const assistSummary = {
      known: this.buildAssistKnownSummary(reason, displayResponse),
      needs: this.buildAssistNeedsSummary(workflowStep, question),
      next: this.buildAssistNextSummary(recommendation, workflowStep, currentAgent)
    };
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
      selectedChoiceNumber: '',
      selectedFieldValues
    };

    this.setTaskModalHeaderId(this.elements.taskAssistId, task.id);
    if (this.elements.taskAssistAgentName) {
      this.elements.taskAssistAgentName.textContent = currentAgent || 'Unassigned';
      this.elements.taskAssistAgentName.title = currentAgent || 'Unassigned';
    }
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
    this.setAssistResponseUI(response, displayResponse);
    this.setAssistSummaryUI(assistSummary);
    if (this.elements.taskAssistQuestionWrap) {
      const shouldShowQuestion = Boolean(question) && workflowStep?.stepType !== 'ask_form';
      this.elements.taskAssistQuestionWrap.classList.toggle('d-none', !shouldShowQuestion);
    }
    if (this.elements.taskAssistContextScroll) {
      this.elements.taskAssistContextScroll.scrollTop = 0;
    }
    if (this.elements.taskAssistAnswerScroll) {
      this.elements.taskAssistAnswerScroll.scrollTop = 0;
    }

    this.populateAssistAgents(currentAgent);
    this.setAssistWorkflowStepUI(workflowStep);
    this.setAssistRecommendationUI(recommendation);
    this.showTaskAssistPage({
      taskId,
      updateRoute: options.updateRoute !== false,
      replaceRoute: Boolean(options.replaceRoute)
    });
  }

  async submitTaskAssist(action) {
    if (!this.currentBlockedTask?.taskId) return;

    const selectedAgent = String(this.elements.taskAssistAgent?.value || '').trim();
    const message = String(this.elements.taskAssistMessage?.value || '').trim();
    const selectedChoiceId = String(this.currentBlockedTask?.selectedChoiceId || '').trim();
    const selectedChoiceLabel = String(this.currentBlockedTask?.selectedChoiceLabel || '').trim();
    const selectedChoiceNumber = String(this.currentBlockedTask?.selectedChoiceNumber || '').trim();
    const fieldValues = this.collectAssistFormFieldValues();
    if (action === 'switch_agent_retry' && !selectedAgent) {
      if (window.Toast) window.Toast.error('Select an agent to switch before retrying.');
      return;
    }
    if (
      action === 'switch_agent_retry' &&
      this.normalizeAgentName(selectedAgent) ===
        this.normalizeAgentName(this.currentBlockedTask.currentAgent)
    ) {
      if (window.Toast) window.Toast.error('Select a different agent before switching.');
      return;
    }
    if (
      action === 'continue_with_instruction' &&
      this.currentBlockedTask.workflowStep?.stepType === 'ask_choice' &&
      !selectedChoiceId &&
      !message
    ) {
      if (window.Toast)
        window.Toast.warning('Choose a next step or add guidance before continuing.');
      return;
    }
    if (
      action === 'continue_with_instruction' &&
      this.currentBlockedTask.workflowStep?.stepType === 'ask_form'
    ) {
      const requiredFields = Array.isArray(this.currentBlockedTask.workflowStep?.fields)
        ? this.currentBlockedTask.workflowStep.fields.filter(field => field?.required !== false)
        : [];
      const missingRequired = requiredFields.filter(field => {
        const fieldId = String(field?.id || '').trim();
        return !fieldValues.some(item => item.id === fieldId && String(item.value || '').trim());
      });

      if (missingRequired.length > 0) {
        if (window.Toast) window.Toast.warning('Answer the required questions before continuing.');
        return;
      }

      if (fieldValues.length === 0 && !message) {
        if (window.Toast)
          window.Toast.warning('Answer at least one question or add guidance before continuing.');
        return;
      }
    }

    const payload = {
      action,
      block_id: this.currentBlockedTask.blockId || undefined,
      message: message || undefined,
      agent: selectedAgent || undefined,
      choice_id: selectedChoiceId || undefined,
      choice_label: selectedChoiceLabel || undefined,
      choice_number: selectedChoiceNumber || undefined,
      field_values: fieldValues.length > 0 ? fieldValues : undefined
    };

    try {
      const response = await fetch(
        `/api/orchestration/tasks/${encodeURIComponent(this.currentBlockedTask.taskId)}/assist`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        }
      );
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to submit assistance');
      }

      if (window.Toast) window.Toast.success('Task updated');
      this.closeTaskAssistPage({ replaceRoute: true });
      await this.loadTasks();
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
    return this.tasks.filter(task => task.parent_task_id === taskId);
  }

  normalizeAgentName(name) {
    return String(name || '')
      .trim()
      .toLowerCase();
  }

  getWorkspaceAgentNames() {
    if (!this.workspace) return [];

    const seen = new Set();
    const names = [];
    const add = name => {
      const normalized = String(name || '').trim();
      if (!normalized || normalized === 'unassigned') return;
      const key = this.normalizeAgentName(normalized);
      if (seen.has(key)) return;
      seen.add(key);
      names.push(normalized);
    };

    if (Array.isArray(this.workspace.agent_instances)) {
      this.workspace.agent_instances.forEach(instance => add(instance?.name));
    }
    if (Array.isArray(this.workspace.agents)) {
      this.workspace.agents.forEach(name => add(name));
    }

    return names;
  }

  // Global agent catalog lookup only (no workspace-local fallback). Used to
  // decide whether an agent has a real /agents/<name> detail page; workspace-
  // local agents are stored in config.json and have no global detail route.
  getGlobalAgentProfile(agentName) {
    const key = this.normalizeAgentName(agentName);
    if (!key) return null;
    return this.agentIndex instanceof Map ? this.agentIndex.get(key) || null : null;
  }

  getAgentProfile(agentName) {
    const key = this.normalizeAgentName(agentName);
    if (!key) return null;
    const globalProfile = this.agentIndex instanceof Map ? this.agentIndex.get(key) : null;
    if (globalProfile) return globalProfile;
    // Fall back to workspace-local agents (entry/manager agents stored in the
    // workspace's config.json, not the global agent catalog). This is what lets
    // their model badge show the real model and become editable.
    if (this.workspaceAgentProfiles instanceof Map) {
      return this.workspaceAgentProfiles.get(key) || null;
    }
    return null;
  }

  async openAgentModelModal(encodedAgentName = '') {
    let agentName = '';
    try {
      agentName = decodeURIComponent(String(encodedAgentName || ''));
    } catch (_error) {
      agentName = String(encodedAgentName || '');
    }
    agentName = String(agentName || '').trim();
    if (!agentName) return;

    await Promise.all([this.loadAgentCatalog(true), this.loadProviderCatalog()]);

    const profile = this.getAgentProfile(agentName);
    if (!profile) {
      if (window.Toast) window.Toast.error('Failed to load agent settings.');
      return;
    }
    if (!this.agentAllowsModelEditing(profile)) {
      if (window.Toast)
        window.Toast.warning('This agent model cannot be changed from the workspace.');
      return;
    }

    this.activeAgentModelEdit = {
      agentName,
      agentType: String(profile.type || 'general').trim() || 'general',
      currentModel: String(profile.model || '').trim(),
      currentProvider: String(profile.provider || '').trim(),
      // Workspace-local agents persist to the workspace config.json via a
      // workspace-scoped endpoint, and their model picker is not type-filtered.
      isWorkspaceAgent:
        String(profile.source || '').trim().toLowerCase() === 'workspace',
      workspaceId: this.workspace?.id || ''
    };

    if (this.elements.agentModelAgentName) {
      this.elements.agentModelAgentName.textContent = agentName;
    }
    if (this.elements.agentModelTitle) {
      this.elements.agentModelTitle.textContent = `Change Model for ${agentName}`;
    }
    if (this.elements.agentModelCurrent) {
      const currentModelLabel = this.activeAgentModelEdit.currentModel || 'Not set';
      const currentProviderLabel = this.activeAgentModelEdit.currentProvider || 'Unknown provider';
      this.elements.agentModelCurrent.textContent = `${currentModelLabel} via ${currentProviderLabel}`;
    }

    this.populateAgentModelSelect();
    this.updateAgentModelSelectionSummary();

    if (this.elements.agentModelModal && window.bootstrap) {
      const modal =
        typeof bootstrap.Modal.getOrCreateInstance === 'function'
          ? bootstrap.Modal.getOrCreateInstance(this.elements.agentModelModal)
          : bootstrap.Modal.getInstance(this.elements.agentModelModal) ||
            new bootstrap.Modal(this.elements.agentModelModal);
      modal.show();
    }
  }

  populateAgentModelSelect() {
    const select = this.elements.agentModelSelect;
    const editState = this.activeAgentModelEdit;
    if (!select || !editState) return;

    const normalizedType =
      String(editState.agentType || 'general')
        .trim()
        .toLowerCase() || 'general';
    const currentModel = String(editState.currentModel || '').trim();
    const currentProvider = String(editState.currentProvider || '').trim();
    // Workspace-local agents list every available model (no agent-type filter),
    // so users can pick any model/provider including local ones (lmstudio, ollama).
    const skipTypeFilter = Boolean(editState.isWorkspaceAgent);

    select.innerHTML = '';
    let hasOptions = false;
    let selectedFound = false;

    (Array.isArray(this.providerCatalog) ? this.providerCatalog : []).forEach(provider => {
      const group = document.createElement('optgroup');
      group.label = String(provider?.display_name || provider?.name || '').trim() || 'Provider';
      let groupHasOptions = false;

      const models = Array.isArray(provider?.models) ? provider.models : [];
      models.forEach(model => {
        const value = String(model?.value || '').trim();
        if (!value) return;

        const modelType = String(model?.type || '')
          .trim()
          .toLowerCase();
        const include = skipTypeFilter || modelType === normalizedType || value === currentModel;
        if (!include) return;

        const option = document.createElement('option');
        option.value = value;
        option.textContent = String(model?.label || value).trim();
        option.setAttribute(
          'data-provider',
          String(model?.provider || provider?.name || '').trim()
        );
        option.setAttribute('data-model-type', modelType);
        if (value === currentModel) {
          option.selected = true;
          selectedFound = true;
        }
        group.appendChild(option);
        groupHasOptions = true;
      });

      if (groupHasOptions) {
        select.appendChild(group);
        hasOptions = true;
      }
    });

    if (currentModel && !selectedFound) {
      const currentGroup = document.createElement('optgroup');
      currentGroup.label = 'Current Model';
      const option = document.createElement('option');
      option.value = currentModel;
      option.textContent = `${currentModel} (Current)`;
      option.selected = true;
      option.setAttribute('data-provider', currentProvider);
      currentGroup.appendChild(option);
      select.appendChild(currentGroup);
      hasOptions = true;
    }

    if (!hasOptions) {
      select.innerHTML = '<option value="">No compatible models available</option>';
    }

    if (this.elements.agentModelSubmitBtn) {
      this.elements.agentModelSubmitBtn.disabled =
        !hasOptions || !String(select.value || '').trim();
    }
  }

  updateAgentModelSelectionSummary() {
    const select = this.elements.agentModelSelect;
    const help = this.elements.agentModelHelp;
    if (!select || !help) return;

    const editState = this.activeAgentModelEdit;
    const selectedOption = select.selectedOptions?.[0] || null;
    const selectedModel = String(select.value || '').trim();
    const selectedProvider = String(selectedOption?.getAttribute('data-provider') || '').trim();

    if (!editState || !selectedModel) {
      help.textContent = 'No compatible models are currently available for this agent type.';
      return;
    }

    const currentModel = String(editState.currentModel || '').trim();
    const currentProvider = String(editState.currentProvider || '').trim();
    if (selectedModel === currentModel && selectedProvider === currentProvider) {
      help.textContent = 'This agent is already using the selected model.';
      return;
    }

    const providerLabel = selectedProvider || 'the selected provider';
    help.textContent = `Save to switch ${editState.agentName} to ${selectedModel} via ${providerLabel}.`;
  }

  resetAgentModelModal() {
    this.activeAgentModelEdit = null;
    if (this.elements.agentModelAgentName) {
      this.elements.agentModelAgentName.textContent = '--';
    }
    if (this.elements.agentModelTitle) {
      this.elements.agentModelTitle.textContent = 'Change Agent Model';
    }
    if (this.elements.agentModelCurrent) {
      this.elements.agentModelCurrent.textContent = '--';
    }
    if (this.elements.agentModelSelect) {
      this.elements.agentModelSelect.innerHTML = '<option value="">Loading models...</option>';
    }
    if (this.elements.agentModelHelp) {
      this.elements.agentModelHelp.textContent = 'Choose a model to update this workspace agent.';
    }
    if (this.elements.agentModelSubmitBtn) {
      this.elements.agentModelSubmitBtn.disabled = false;
      this.elements.agentModelSubmitBtn.textContent = 'Save Model';
    }
  }

  async submitAgentModelChange() {
    const editState = this.activeAgentModelEdit;
    const select = this.elements.agentModelSelect;
    const submitBtn = this.elements.agentModelSubmitBtn;
    if (!editState || !select || !submitBtn) return;

    const model = String(select.value || '').trim();
    const selectedOption = select.selectedOptions?.[0] || null;
    const provider = String(selectedOption?.getAttribute('data-provider') || '').trim();

    if (!model || !provider) {
      if (window.Toast) window.Toast.warning('Choose a valid model before saving.');
      return;
    }

    submitBtn.disabled = true;
    submitBtn.textContent = 'Saving...';

    try {
      // Workspace-local agents persist to the workspace's config.json via the
      // workspace-scoped endpoint; global agents use the global agent endpoint.
      const endpoint = editState.isWorkspaceAgent
        ? `/api/workspaces/${encodeURIComponent(editState.workspaceId)}/agents/${encodeURIComponent(editState.agentName)}`
        : `/api/agents/${encodeURIComponent(editState.agentName)}`;
      const response = await fetch(endpoint, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          model,
          llm_provider: provider
        })
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Failed to update agent model');
      }

      if (editState.isWorkspaceAgent) {
        // Re-read workspace-local profiles (also re-renders the roster).
        await this.loadWorkspaceAgentSnapshots();
      } else {
        await this.loadAgentCatalog(true);
        this.renderAgentGroups();
      }

      if (window.Toast) {
        window.Toast.success(`Updated ${editState.agentName} to ${model}.`);
      }

      if (this.elements.agentModelModal && window.bootstrap) {
        const modal = bootstrap.Modal.getInstance(this.elements.agentModelModal);
        modal?.hide();
      }
    } catch (error) {
      console.error('Failed to update workspace agent model:', error);
      if (window.Toast) {
        window.Toast.error(error?.message || 'Failed to update agent model');
      }
      submitBtn.disabled = false;
      submitBtn.textContent = 'Save Model';
    }
  }

  // ── Workspace MCP Facades (delegate to mcpManager) ──────────────────
  // Kept on the host so inline onclick handlers (rendered by the manager) and
  // existing call sites in agent rendering don't have to know about the
  // mcpManager indirection.

  openWorkspaceMCPModal(bindingId = '') {
    return this.mcpManager.openWorkspaceMCPModal(bindingId);
  }

  deleteWorkspaceMCPBinding(bindingId) {
    return this.mcpManager.deleteWorkspaceMCPBinding(bindingId);
  }

  loadAvailableMCPServers(force = false) {
    return this.mcpManager.loadAvailableMCPServers(force);
  }

  renderWorkspaceMCPBindings() {
    return this.mcpManager.renderWorkspaceMCPBindings();
  }

  getEffectiveWorkspaceMCPServerNames(agentName) {
    return this.mcpManager.getEffectiveWorkspaceMCPServerNames(agentName);
  }

  getEffectiveWorkspaceSkillNamesForAgent(agentName) {
    return this.skillsManager.getEffectiveWorkspaceSkillNamesForAgent(agentName);
  }

  getWorkspaceMCPBindings(options = {}) {
    return this.mcpManager.getWorkspaceMCPBindings(options);
  }

  // ── Workspace Configuration Methods ─────────────────────────────────

  formatWorkspaceConfigPresetLabel(preset) {
    const value = String(preset || '').trim();
    if (!value) return 'Guided';
    return value
      .split('_')
      .filter(Boolean)
      .map(part => part.charAt(0).toUpperCase() + part.slice(1))
      .join(' ');
  }

  formatWorkspaceSettingsProfileLabel(profile) {
    const normalized = String(profile || '')
      .trim()
      .toLowerCase();
    switch (normalized) {
      case 'research':
        return 'Research';
      case 'software_project':
        return 'Software Project';
      case 'general':
      default:
        return 'General';
    }
  }

  hasNonDefaultWorkspaceSettings() {
    const current = this.normalizeWorkspaceSettings(
      this.workspaceSettings || this.getDefaultWorkspaceSettings()
    );
    const defaults = this.normalizeWorkspaceSettings(this.getDefaultWorkspaceSettings());
    return (
      JSON.stringify({
        workflow: current.workflow,
        planning: current.planning
      }) !==
      JSON.stringify({
        workflow: defaults.workflow,
        planning: defaults.planning
      })
    );
  }

  setWorkspaceConfigExpanded(expanded) {
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
      this.elements.configToggleLabel.textContent = nextExpanded
        ? 'Hide Configuration'
        : 'Show Configuration';
    }
  }

  initializeWorkspaceConfigExpansion() {
    this.setWorkspaceConfigExpanded(false);
  }

  toggleWorkspaceConfigExpanded() {
    this.setWorkspaceConfigExpanded(!this.workspaceConfigExpanded);
  }

  renderWorkspaceConfigSummary() {
    const settings = this.normalizeWorkspaceSettings(
      this.workspaceSettings || this.getDefaultWorkspaceSettings()
    );
    const profile = this.normalizeWorkspaceSettingsProfile(settings.profile);
    const preset = this.deriveWorkspaceSettingsPreset(settings);
    const connectionCount = this.getWorkspaceMCPBindings({ includeDisabled: true }).length;
    const skillCount = this.getWorkspaceSkillBindings({ includeDisabled: true }).length;
    const referenceCount = Array.isArray(this.tasks)
      ? this.tasks.filter(task => String(task?.reference_url || '').trim()).length
      : 0;

    if (this.elements.configPresetChip) {
      this.elements.configPresetChip.textContent = `Workspace: ${this.formatWorkspaceSettingsProfileLabel(profile)} · ${this.formatWorkspaceConfigPresetLabel(preset)}`;
    }
    if (this.elements.configConnectionsChip) {
      this.elements.configConnectionsChip.textContent = `MCP: ${connectionCount}`;
    }
    if (this.elements.configSkillsChip) {
      this.elements.configSkillsChip.textContent = `Skills: ${skillCount}`;
    }
    if (this.elements.configReferenceChip) {
      this.elements.configReferenceChip.hidden = referenceCount === 0;
      this.elements.configReferenceChip.textContent = `Refs: ${referenceCount}`;
    }

    this.initializeWorkspaceConfigExpansion();
  }

  // ── Workspace Settings Methods ───────────────────────────────────────

  getWorkspaceSettingsPresets() {
    return ['minimal', 'guided', 'planner', 'autonomous'];
  }

  getWorkspaceSettingsProfiles() {
    return ['general', 'research', 'software_project'];
  }

  getDefaultWorkspaceSettingsPresetForProfile(profile = 'general') {
    switch (this.normalizeWorkspaceSettingsProfile(profile)) {
      case 'software_project':
        return 'planner';
      case 'research':
        return 'guided';
      case 'general':
      default:
        return 'guided';
    }
  }

  buildWorkspaceSettingsPreset(preset = '', profile = 'general') {
    const normalizedProfile = this.normalizeWorkspaceSettingsProfile(profile);
    const requestedPreset = String(preset || '')
      .trim()
      .toLowerCase();
    const normalizedPreset = requestedPreset
      ? this.normalizeWorkspaceSettingsPreset(requestedPreset)
      : this.getDefaultWorkspaceSettingsPresetForProfile(normalizedProfile);
    const basePreset =
      normalizedPreset === 'custom'
        ? this.getDefaultWorkspaceSettingsPresetForProfile(normalizedProfile)
        : normalizedPreset;
    const settings = {
      version: 1,
      profile: normalizedProfile,
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
      },
      task_markdown: {
        enabled: false,
        path: 'tasks.md',
        generate_agent_views: true
      }
    };

    switch (basePreset) {
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

    switch (normalizedProfile) {
      case 'research':
        settings.planning.mode = 'investigation';
        settings.planning.write_prd = false;
        settings.planning.write_task_list = false;
        settings.planning.clarification_mode = 'deep';
        settings.planning.require_branch = false;
        break;
      case 'software_project':
        settings.planning.mode = 'feature';
        settings.planning.write_prd = true;
        settings.planning.write_task_list = true;
        settings.planning.clarification_mode = 'standard';
        settings.planning.require_branch = true;
        break;
      default:
        break;
    }

    return settings;
  }

  getDefaultWorkspaceSettings() {
    return this.buildWorkspaceSettingsPreset('', 'general');
  }

  normalizeWorkspaceSettingsPreset(value) {
    const normalized = String(value || '')
      .trim()
      .toLowerCase();
    if (['minimal', 'guided', 'planner', 'autonomous', 'custom'].includes(normalized)) {
      return normalized;
    }
    return 'guided';
  }

  normalizeWorkspaceSettingsProfile(value) {
    const normalized = String(value || '')
      .trim()
      .toLowerCase();
    if (this.getWorkspaceSettingsProfiles().includes(normalized)) {
      return normalized;
    }
    return 'general';
  }

  inferWorkspaceSettingsProfile(settings = {}) {
    const raw =
      settings && typeof settings === 'object' && !Array.isArray(settings) ? settings : {};
    const explicit = String(raw.profile || '')
      .trim()
      .toLowerCase();
    if (this.getWorkspaceSettingsProfiles().includes(explicit)) {
      return explicit;
    }

    const preset = this.normalizeWorkspaceSettingsPreset(raw.preset || '');
    if (preset === 'planner' || preset === 'autonomous') {
      return 'software_project';
    }

    const planning =
      raw.planning && typeof raw.planning === 'object' && !Array.isArray(raw.planning)
        ? raw.planning
        : {};
    const planningMode = String(planning.mode || '')
      .trim()
      .toLowerCase();
    const clarificationMode = String(planning.clarification_mode || '')
      .trim()
      .toLowerCase();
    if (planningMode === 'investigation' || clarificationMode === 'deep') {
      return 'research';
    }

    return 'general';
  }

  normalizeWorkspaceSettingsMode(value) {
    const normalized = String(value || '')
      .trim()
      .toLowerCase();
    if (['direct', 'guided', 'plan_then_execute'].includes(normalized)) {
      return normalized;
    }
    return 'guided';
  }

  normalizeWorkspaceSettingsConfirmationMode(value) {
    const normalized = String(value || '')
      .trim()
      .toLowerCase();
    if (['none', 'always', 'destructive_only'].includes(normalized)) {
      return normalized;
    }
    return 'destructive_only';
  }

  normalizeWorkspaceSettings(settings = {}) {
    const raw =
      settings && typeof settings === 'object' && !Array.isArray(settings) ? settings : {};
    const profile = this.inferWorkspaceSettingsProfile(raw);
    const rawPreset = String(raw.preset || '').trim();
    const preset = rawPreset
      ? this.normalizeWorkspaceSettingsPreset(rawPreset)
      : this.getDefaultWorkspaceSettingsPresetForProfile(profile);
    const base = this.buildWorkspaceSettingsPreset(
      preset === 'custom' ? this.getDefaultWorkspaceSettingsPresetForProfile(profile) : preset,
      profile
    );
    const workflow =
      raw.workflow && typeof raw.workflow === 'object' && !Array.isArray(raw.workflow)
        ? raw.workflow
        : {};
    const planning =
      raw.planning && typeof raw.planning === 'object' && !Array.isArray(raw.planning)
        ? raw.planning
        : {};
    const taskMarkdown =
      raw.task_markdown && typeof raw.task_markdown === 'object' && !Array.isArray(raw.task_markdown)
        ? raw.task_markdown
        : {};
    const boolOrDefault = (value, fallback) => (typeof value === 'boolean' ? value : fallback);

    return {
      version:
        Number.isFinite(Number(raw.version)) && Number(raw.version) > 0 ? Number(raw.version) : 1,
      profile,
      preset,
      workflow: {
        mode: this.normalizeWorkspaceSettingsMode(workflow.mode || base.workflow.mode),
        require_repo_scan: boolOrDefault(
          workflow.require_repo_scan,
          base.workflow.require_repo_scan
        ),
        save_outputs_as_notes: boolOrDefault(
          workflow.save_outputs_as_notes,
          base.workflow.save_outputs_as_notes
        ),
        sync_plans_to_tasks: boolOrDefault(
          workflow.sync_plans_to_tasks,
          base.workflow.sync_plans_to_tasks
        ),
        ask_before_specialist_handoff: boolOrDefault(
          workflow.ask_before_specialist_handoff,
          base.workflow.ask_before_specialist_handoff
        ),
        confirmation_mode: this.normalizeWorkspaceSettingsConfirmationMode(
          workflow.confirmation_mode || base.workflow.confirmation_mode
        )
      },
      planning: {
        enabled: boolOrDefault(planning.enabled, base.planning.enabled),
        mode: this.normalizeWorkspacePlanningConfig({ mode: planning.mode || base.planning.mode })
          .mode,
        write_prd: boolOrDefault(planning.write_prd, base.planning.write_prd),
        write_task_list: boolOrDefault(planning.write_task_list, base.planning.write_task_list),
        tasks_dir: String(planning.tasks_dir || base.planning.tasks_dir || '').trim() || 'tasks',
        clarification_mode: this.normalizeWorkspacePlanningConfig({
          clarification_mode: planning.clarification_mode || base.planning.clarification_mode
        }).clarification_mode,
        default_execution_mode: this.normalizeWorkspacePlanningConfig({
          default_execution_mode:
            planning.default_execution_mode || base.planning.default_execution_mode
        }).default_execution_mode,
        require_branch: boolOrDefault(planning.require_branch, base.planning.require_branch)
      },
      task_markdown: {
        enabled: boolOrDefault(taskMarkdown.enabled, base.task_markdown.enabled),
        path:
          String(taskMarkdown.path || base.task_markdown.path || '')
            .trim()
            .replace(/^\/+/, '') || 'tasks.md',
        generate_agent_views: boolOrDefault(
          taskMarkdown.generate_agent_views,
          base.task_markdown.generate_agent_views
        )
      },
      updated_at: typeof raw.updated_at === 'string' ? raw.updated_at : ''
    };
  }

  deriveWorkspaceSettingsPreset(settings = {}) {
    const normalized = this.normalizeWorkspaceSettings(settings);
    const normalizedSignature = JSON.stringify({
      workflow: normalized.workflow,
      planning: normalized.planning,
      task_markdown: normalized.task_markdown
    });

    const matchingPreset = this.getWorkspaceSettingsPresets().find(preset => {
      const candidate = this.buildWorkspaceSettingsPreset(preset, normalized.profile);
      return (
        JSON.stringify({
          workflow: candidate.workflow,
          planning: candidate.planning,
          task_markdown: candidate.task_markdown
        }) === normalizedSignature
      );
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
      task_markdown: { ...normalized.task_markdown },
      summary: this.getWorkspaceSettingsSummaryItems(normalized),
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

  getWorkspaceSettingsSummaryItems(settings = {}) {
    const normalized = this.normalizeWorkspaceSettings(settings);
    const summary = [
      `Workspace preset: ${this.formatWorkspaceSettingsProfileLabel(normalized.profile)}`,
      `Workflow style: ${this.formatWorkspaceConfigPresetLabel(normalized.preset)}`,
      `Interaction mode: ${normalized.workflow.mode}`,
      `Confirmation mode: ${normalized.workflow.confirmation_mode}`,
      `Save useful outputs as workspace notes: ${normalized.workflow.save_outputs_as_notes}`,
      `Ask before specialist handoff: ${normalized.workflow.ask_before_specialist_handoff}`,
      `Structured planning workflow: ${normalized.planning.enabled ? 'enabled' : 'disabled'}`,
      `Markdown task sync: ${normalized.task_markdown.enabled ? 'enabled' : 'disabled'}`
    ];

    if (normalized.planning.enabled) {
      summary.push(`Planning style: ${normalized.planning.mode}`);
    }
    if (normalized.workflow.sync_plans_to_tasks) {
      summary.push('Approved plans sync into workspace tasks');
    }
    if (normalized.workflow.require_repo_scan) {
      summary.push('Repo scan required before code work');
    }
    if (normalized.task_markdown.enabled) {
      summary.push(`Markdown task map: ${normalized.task_markdown.path}`);
    }

    return summary;
  }

  normalizeWorkspaceSettingsEffectiveBehavior(effectiveBehavior, settings = {}) {
    const raw =
      effectiveBehavior &&
      typeof effectiveBehavior === 'object' &&
      !Array.isArray(effectiveBehavior)
        ? effectiveBehavior
        : null;
    const fallback = this.buildWorkspaceSettingsEffectiveBehavior(settings);
    if (!raw) {
      return fallback;
    }

    return {
      workflow:
        raw.workflow && typeof raw.workflow === 'object'
          ? { ...fallback.workflow, ...raw.workflow }
          : fallback.workflow,
      planning:
        raw.planning && typeof raw.planning === 'object'
          ? { ...fallback.planning, ...raw.planning }
          : fallback.planning,
      task_markdown:
        raw.task_markdown && typeof raw.task_markdown === 'object'
          ? { ...fallback.task_markdown, ...raw.task_markdown }
          : fallback.task_markdown,
      summary: this.getWorkspaceSettingsSummaryItems({
        profile: this.normalizeWorkspaceSettingsProfile(
          raw.profile || settings.profile || fallback.profile
        ),
        preset: this.normalizeWorkspaceSettingsPreset(
          raw.preset || settings.preset || fallback.preset
        ),
        workflow:
          raw.workflow && typeof raw.workflow === 'object'
            ? { ...fallback.workflow, ...raw.workflow }
            : fallback.workflow,
        planning:
          raw.planning && typeof raw.planning === 'object'
            ? { ...fallback.planning, ...raw.planning }
            : fallback.planning,
        task_markdown:
          raw.task_markdown && typeof raw.task_markdown === 'object'
            ? { ...fallback.task_markdown, ...raw.task_markdown }
            : fallback.task_markdown
      }),
      managed_skills: Array.isArray(raw.managed_skills)
        ? raw.managed_skills
        : fallback.managed_skills
    };
  }

  populateWorkspaceSettingsForm(settings = {}) {
    const normalized = this.normalizeWorkspaceSettings(settings);

    if (this.elements.settingsProfileInput) {
      this.elements.settingsProfileInput.value = normalized.profile;
    }
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
      this.elements.settingsRequireScanInput.checked =
        normalized.workflow.require_repo_scan === true;
    }
    if (this.elements.settingsSaveNotesInput) {
      this.elements.settingsSaveNotesInput.checked =
        normalized.workflow.save_outputs_as_notes !== false;
    }
    if (this.elements.settingsSyncTasksInput) {
      this.elements.settingsSyncTasksInput.checked =
        normalized.workflow.sync_plans_to_tasks === true;
    }
    if (this.elements.settingsAskHandoffInput) {
      this.elements.settingsAskHandoffInput.checked =
        normalized.workflow.ask_before_specialist_handoff === true;
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
      this.elements.settingsWriteTaskListInput.checked =
        normalized.planning.write_task_list !== false;
    }
    if (this.elements.settingsRequireBranchInput) {
      this.elements.settingsRequireBranchInput.checked =
        normalized.planning.require_branch !== false;
    }
    if (this.elements.settingsTaskMarkdownEnabledInput) {
      this.elements.settingsTaskMarkdownEnabledInput.checked =
        normalized.task_markdown.enabled === true;
    }
    if (this.elements.settingsTaskMarkdownPathInput) {
      this.elements.settingsTaskMarkdownPathInput.value = normalized.task_markdown.path;
    }
    if (this.elements.settingsTaskMarkdownAgentViewsInput) {
      this.elements.settingsTaskMarkdownAgentViewsInput.checked =
        normalized.task_markdown.generate_agent_views !== false;
    }
    if (this.elements.settingsTaskMarkdownStatus) {
      this.elements.settingsTaskMarkdownStatus.textContent = this.getTaskMarkdownStatusText(
        normalized
      );
    }
  }

  getTaskMarkdownStatusText(settings = {}) {
    const normalized = this.normalizeWorkspaceSettings(settings);
    if (!normalized.task_markdown.enabled) {
      return 'Disabled';
    }
    const status =
      this.workspaceTaskMarkdownStatus &&
      typeof this.workspaceTaskMarkdownStatus === 'object' &&
      String(this.workspaceTaskMarkdownStatus.path || '') === normalized.task_markdown.path
        ? this.workspaceTaskMarkdownStatus
        : null;
    if (!status) {
      return `Syncs ${normalized.task_markdown.path}`;
    }
    const state = String(status.status || '').trim();
    const warningCount = Number(status.warning_count || 0);
    const warningCopy =
      warningCount > 0 ? `, ${warningCount} warning${warningCount === 1 ? '' : 's'}` : '';
    if (state === 'ready') {
      const when = status.last_sync_time
        ? `Last sync ${formatDate(status.last_sync_time)}`
        : 'Ready';
      return `${when}${warningCopy}`;
    }
    if (state === 'missing') {
      return `Pending first write${warningCopy}`;
    }
    if (state === 'invalid_path') {
      return `Invalid path${warningCopy}`;
    }
    if (state === 'unavailable') {
      return `Workspace folder unavailable${warningCopy}`;
    }
    return `Syncs ${normalized.task_markdown.path}${warningCopy}`;
  }

  buildWorkspaceSettingsFromForm() {
    const selectedProfile = String(
      this.elements.settingsProfileInput?.value || this.workspaceSettings?.profile || 'general'
    ).trim();
    const settings = this.normalizeWorkspaceSettings({
      version: this.workspaceSettings?.version || 1,
      profile: selectedProfile,
      preset: String(
        this.elements.settingsPresetInput?.value ||
          this.workspaceSettings?.preset ||
          this.getDefaultWorkspaceSettingsPresetForProfile(selectedProfile)
      ).trim(),
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
        clarification_mode: String(
          this.elements.settingsClarificationModeInput?.value || ''
        ).trim(),
        default_execution_mode: String(
          this.elements.settingsExecutionModeInput?.value || ''
        ).trim(),
        require_branch: this.elements.settingsRequireBranchInput?.checked !== false
      },
      task_markdown: {
        enabled: this.elements.settingsTaskMarkdownEnabledInput?.checked === true,
        path: String(this.elements.settingsTaskMarkdownPathInput?.value || '').trim(),
        generate_agent_views:
          this.elements.settingsTaskMarkdownAgentViewsInput?.checked !== false
      }
    });

    settings.preset = this.deriveWorkspaceSettingsPreset(settings);
    return settings;
  }

  renderWorkspaceSettingsSummary(settings = {}, effectiveBehavior = null) {
    const normalized = this.normalizeWorkspaceSettings(settings);
    const effective = this.normalizeWorkspaceSettingsEffectiveBehavior(
      effectiveBehavior,
      normalized
    );

    if (this.elements.settingsSummary) {
      if (effective.summary.length === 0) {
        this.elements.settingsSummary.textContent =
          'No effective manager behavior summary available.';
      } else {
        this.elements.settingsSummary.innerHTML = `
          <ul class="workspace-detail-settings-summary-list">
            ${effective.summary.map(item => `<li>${this.escapeHtml(item)}</li>`).join('')}
          </ul>
        `;
      }
    }

    if (this.elements.settingsManagedSkills) {
      if (!Array.isArray(effective.managed_skills) || effective.managed_skills.length === 0) {
        this.elements.settingsManagedSkills.textContent = 'No settings-managed workflows active.';
      } else {
        this.elements.settingsManagedSkills.innerHTML = effective.managed_skills
          .map(skill => {
            const skillName =
              String(skill?.skill_name || skill?.skillName || '').trim() || 'unknown-skill';
            const displayName =
              skillName === 'workspace-planning' ? 'Structured planning' : skillName;
            const source = String(skill?.source || '').trim() || 'settings';
            const reason = String(skill?.reason || '').trim();
            const planningSummary =
              skillName === 'workspace-planning'
                ? this.getWorkspacePlanningSummary(
                    skill?.config || this.toWorkspacePlanningSkillConfig(normalized)
                  )
                : '';
            const detail =
              skillName === 'workspace-planning'
                ? planningSummary || 'Controlled by workspace settings'
                : [source, reason].filter(Boolean).join(' • ');

            return `
            <div class="workspace-detail-settings-managed-entry">
              <div class="workspace-detail-settings-managed-name">${this.escapeHtml(displayName)}</div>
              <div class="workspace-detail-settings-managed-meta">${this.escapeHtml(detail || 'Managed by workspace settings')}</div>
            </div>
          `;
          })
          .join('');
      }
    }
  }

  renderWorkspaceSettings() {
    const settings = this.normalizeWorkspaceSettings(
      this.workspaceSettings || this.getDefaultWorkspaceSettings()
    );
    const effective = this.normalizeWorkspaceSettingsEffectiveBehavior(
      this.workspaceSettingsEffectiveBehavior,
      settings
    );

    this.workspaceSettings = settings;
    this.workspaceSettingsEffectiveBehavior = effective;
    this.populateWorkspaceSettingsForm(settings);
    this.renderWorkspaceSettingsSummary(settings, effective);
    this.renderWorkspaceConfigSummary();
  }

  handleWorkspaceSettingsPresetChange() {
    const preset = this.normalizeWorkspaceSettingsPreset(
      this.elements.settingsPresetInput?.value || 'guided'
    );
    const profile = this.normalizeWorkspaceSettingsProfile(
      this.elements.settingsProfileInput?.value || this.workspaceSettings?.profile || 'general'
    );
    if (preset === 'custom') {
      this.handleWorkspaceSettingsFieldChange();
      return;
    }

    this.workspaceSettings = this.buildWorkspaceSettingsPreset(preset, profile);
    this.workspaceSettingsEffectiveBehavior = this.buildWorkspaceSettingsEffectiveBehavior(
      this.workspaceSettings
    );
    this.renderWorkspaceSettings();
  }

  handleWorkspaceSettingsProfileChange() {
    const profile = this.normalizeWorkspaceSettingsProfile(
      this.elements.settingsProfileInput?.value || 'general'
    );
    const preset = this.getDefaultWorkspaceSettingsPresetForProfile(profile);

    this.workspaceSettings = this.buildWorkspaceSettingsPreset(preset, profile);
    this.workspaceSettingsEffectiveBehavior = this.buildWorkspaceSettingsEffectiveBehavior(
      this.workspaceSettings
    );
    this.renderWorkspaceSettings();
  }

  handleWorkspaceSettingsFieldChange() {
    const settings = this.buildWorkspaceSettingsFromForm();
    this.workspaceSettings = settings;
    this.workspaceSettingsEffectiveBehavior =
      this.buildWorkspaceSettingsEffectiveBehavior(settings);
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
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/settings`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        }
      );

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
      this.workspaceTaskMarkdownStatus =
        data?.task_markdown_status && typeof data.task_markdown_status === 'object'
          ? data.task_markdown_status
          : null;
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

  async importWorkspaceTaskMarkdownNow() {
    if (this.elements.settingsTaskMarkdownRefreshBtn) {
      this.elements.settingsTaskMarkdownRefreshBtn.disabled = true;
      this.elements.settingsTaskMarkdownRefreshBtn.textContent = 'Importing...';
    }
    if (this.elements.settingsTaskMarkdownStatus) {
      this.elements.settingsTaskMarkdownStatus.textContent = 'Importing Markdown task map...';
    }

    try {
      await this.loadTasks();
      if (this.elements.settingsTaskMarkdownStatus) {
        this.elements.settingsTaskMarkdownStatus.textContent = 'Imported task map and refreshed tasks.';
      }
      if (window.Toast) {
        window.Toast.success('Markdown tasks imported');
      }
    } catch (error) {
      console.error('Failed to import Markdown tasks:', error);
      if (this.elements.settingsTaskMarkdownStatus) {
        this.elements.settingsTaskMarkdownStatus.textContent = 'Import failed';
      }
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to import Markdown tasks');
      }
    } finally {
      if (this.elements.settingsTaskMarkdownRefreshBtn) {
        this.elements.settingsTaskMarkdownRefreshBtn.disabled = false;
        this.elements.settingsTaskMarkdownRefreshBtn.textContent = 'Import Now';
      }
    }
  }

  // ── Workspace Skills Facades (delegate to skillsManager) ─────────────

  openWorkspaceSkillModal(bindingId = '') {
    return this.skillsManager.openWorkspaceSkillModal(bindingId);
  }

  deleteWorkspaceSkillBinding(bindingId) {
    return this.skillsManager.deleteWorkspaceSkillBinding(bindingId);
  }

  loadAvailableSkills(force = false) {
    return this.skillsManager.loadAvailableSkills(force);
  }

  renderWorkspaceSkillBindings() {
    return this.skillsManager.renderWorkspaceSkillBindings();
  }

  getWorkspaceSkillBindings(options = {}) {
    return this.skillsManager.getWorkspaceSkillBindings(options);
  }

  renderWorkspacePluginBindings() {
    return this.pluginsManager.render();
  }

  // Planning-config helpers live with the skills manager since they shape the
  // workspace_planning skill binding, but the host still calls them from
  // workspace-settings normalization and the config summary panel.
  normalizeWorkspacePlanningConfig(config = {}) {
    return this.skillsManager.normalizeWorkspacePlanningConfig(config);
  }

  getWorkspacePlanningSummary(config = {}) {
    return this.skillsManager.getWorkspacePlanningSummary(config);
  }

  getAgentInstanceIdsForName(agentName) {
    const normalized = this.normalizeAgentName(agentName);
    if (!normalized || !this.workspace || !Array.isArray(this.workspace.agent_instances)) {
      return [];
    }

    const ids = [];
    const seen = new Set();
    this.workspace.agent_instances.forEach(instance => {
      if (this.normalizeAgentName(instance?.name) !== normalized) return;
      const id = String(instance?.id || '').trim();
      if (!id || seen.has(id)) return;
      seen.add(id);
      ids.push(id);
    });
    return ids;
  }


  async loadWorkspaceAgentSnapshots() {
    this.workspaceAgentSnapshots = new Set();
    this.workspaceAgentProfiles = new Map();
    const id = this.workspace?.id;
    if (!id) return;
    try {
      // The /agents endpoint returns full profiles (model/provider/type) for
      // every workspace-local agent that has an on-disk snapshot. We derive both
      // the advisory snapshot set and the profile map used by getAgentProfile()
      // from the same response.
      const response = await fetch(`/api/workspaces/${encodeURIComponent(id)}/agents`);
      if (!response.ok) return;
      const data = await response.json();
      const agents = Array.isArray(data?.agents) ? data.agents : [];
      agents.forEach(agent => {
        const name = String(agent?.name || '').trim();
        if (!name) return;
        const key = this.normalizeAgentName(name);
        this.workspaceAgentSnapshots.add(key);
        this.workspaceAgentProfiles.set(key, {
          name,
          type: String(agent?.type || '').trim(),
          model: String(agent?.model || '').trim(),
          provider: String(agent?.provider || '').trim(),
          source: String(agent?.source || 'workspace').trim().toLowerCase() || 'workspace'
        });
      });
    } catch (_err) {
      // Profile info is advisory; failure just hides the model badge / recovery hint.
    }
    this.renderAgentGroups();
  }

  hasWorkspaceAgentSnapshot(agentName) {
    if (!this.workspaceAgentSnapshots || !this.workspaceAgentSnapshots.size) return false;
    const key = String(agentName || '')
      .trim()
      .toLowerCase();
    if (!key) return false;
    return this.workspaceAgentSnapshots.has(key);
  }

  async loadAgentCatalog(force = false) {
    if (!force && this.agentIndex instanceof Map && this.agentIndex.size > 0) {
      this.agentCatalogLoaded = true;
      this.agentCatalogLoadFailed = false;
      this.renderWorkspaceHealth();
      return this.agentCatalog;
    }

    const nextCatalog = [];
    const nextIndex = new Map();
    this.agentCatalogLoaded = false;
    this.agentCatalogLoadFailed = false;
    this.renderWorkspaceHealth();
    try {
      const response = await fetch('/api/agents/dashboard/list');
      if (!response.ok) {
        throw new Error(`Failed to load agent catalog (${response.status})`);
      }

      const data = await response.json();
      const agents = Array.isArray(data?.agents) ? data.agents : [];
      agents.forEach(agent => {
        const name = String(agent?.name || '').trim();
        if (!name) return;

        const profile = {
          name,
          type: String(agent?.type || '').trim(),
          source: String(agent?.source || 'user')
            .trim()
            .toLowerCase(),
          model: String(agent?.model || '').trim(),
          provider: String(agent?.provider || '').trim(),
          status: String(agent?.status || '')
            .trim()
            .toLowerCase(),
          capabilities: Array.isArray(agent?.capabilities)
            ? agent.capabilities.map(value => String(value || '').trim()).filter(Boolean)
            : [],
          allowWebSearch: Boolean(agent?.allow_web_search),
          enabledPlugins: Array.isArray(agent?.enabled_plugins)
            ? agent.enabled_plugins.map(value => String(value || '').trim()).filter(Boolean)
            : [],
          mcpServers: Array.isArray(agent?.mcp_servers)
            ? agent.mcp_servers.map(value => String(value || '').trim()).filter(Boolean)
            : [],
          evolution:
            agent?.evolution && typeof agent.evolution === 'object' ? agent.evolution : null,
          level: Number.isFinite(Number(agent?.evolution?.level))
            ? Math.max(0, Math.floor(Number(agent.evolution.level)))
            : 0,
          stage: String(agent?.evolution?.stage || '').trim()
        };

        nextCatalog.push(profile);
        nextIndex.set(this.normalizeAgentName(name), profile);
      });
    } catch (error) {
      console.error('Failed to load agent catalog:', error);
      this.agentCatalogLoadFailed = true;
    }

    this.agentCatalog = nextCatalog;
    this.agentIndex = nextIndex;
    this.agentCatalogLoaded = true;

    if (!Array.isArray(this.agentOptions) || this.agentOptions.length === 0) {
      this.agentOptions = this.buildAgentOptionsFromCatalog();
    }

    this.renderWorkspaceHealth();
    this.renderAgentGroups();

    return this.agentCatalog;
  }

  async loadProviderCatalog(force = false) {
    if (!force && Array.isArray(this.providerCatalog) && this.providerCatalog.length > 0) {
      return this.providerCatalog;
    }
    if (!force && this.providerCatalogPromise) {
      return this.providerCatalogPromise;
    }

    this.providerCatalogPromise = (async () => {
      try {
        const response = await fetch('/api/providers');
        if (!response.ok) {
          throw new Error('Failed to load provider catalog');
        }

        const data = await response.json();
        this.providerCatalog = Array.isArray(data?.providers) ? data.providers : [];
      } catch (error) {
        console.error('Failed to load provider catalog:', error);
        this.providerCatalog = [];
      } finally {
        this.providerCatalogPromise = null;
      }

      return this.providerCatalog;
    })();

    return this.providerCatalogPromise;
  }

  buildAgentOptionsFromCatalog() {
    const options = [{ label: 'Unassigned', value: '' }];
    const seen = new Set();
    this.agentCatalog.forEach(profile => {
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

    const lowerCapabilities = new Set(
      (profile.capabilities || [])
        .map(value =>
          String(value || '')
            .trim()
            .toLowerCase()
        )
        .filter(Boolean)
    );
    if (
      lowerCapabilities.has('browser') ||
      lowerCapabilities.has('browser_automation') ||
      lowerCapabilities.has('web_search') ||
      lowerCapabilities.has('web_fetch')
    ) {
      return true;
    }

    const pluginNames = (profile.enabledPlugins || [])
      .map(value =>
        String(value || '')
          .trim()
          .toLowerCase()
      )
      .filter(Boolean);
    for (const name of pluginNames) {
      if (
        name.startsWith('browser') ||
        name.startsWith('web_fetch') ||
        name.startsWith('web_search') ||
        name === 'navigate' ||
        name === 'open_url' ||
        name.includes('playwright') ||
        name.includes('browserbase') ||
        name.includes('puppeteer')
      ) {
        return true;
      }
    }

    const serverNames = this.getEffectiveWorkspaceMCPServerNames(profile.name)
      .map(value =>
        String(value || '')
          .trim()
          .toLowerCase()
      )
      .filter(Boolean);
    for (const name of serverNames) {
      if (
        name.includes('playwright') ||
        name.includes('browserbase') ||
        name.includes('puppeteer') ||
        name.includes('browser')
      ) {
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
    const lower = String(description || '')
      .trim()
      .toLowerCase();
    if (!lower) return false;

    const verbs = [
      'open',
      'visit',
      'navigate',
      'go to',
      'browse',
      'click',
      'fill',
      'type',
      'extract'
    ];
    const hasVerb = verbs.some(verb => lower.includes(verb));
    if (!hasVerb) return false;

    if (lower.includes('http://') || lower.includes('https://') || lower.includes('www.')) {
      return true;
    }

    const tokens = lower.split(/\s+/);
    for (const token of tokens) {
      const cleaned = token.replace(/^[\s,.;:!?"'`()[\]{}<>]+|[\s,.;:!?"'`()[\]{}<>]+$/g, '');
      if (!cleaned || cleaned.includes('/') || cleaned.split('.').length - 1 < 1) continue;
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
          confirmLabel:
            specialistAction.kind === 'add_and_switch' ? 'Add and Switch' : 'Switch Agent',
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

    const currentEvaluation = await this.evaluateAgentForTaskRequirements(
      currentAgent,
      requirements
    );
    if (currentEvaluation) {
      return { kind: 'none' };
    }

    const requirementSummary = requirements
      .map(requirement => requirement.label.toLowerCase())
      .join(' and ');
    const recommendedAgent = await this.findBestAgentForTaskRequirements(requirements, {
      excludeAgent: currentAgent
    });
    if (recommendedAgent) {
      return {
        kind: 'switch_recommended',
        recommendedAgent: recommendedAgent.agentName,
        message: `"${currentAgent}" likely lacks ${requirementSummary} for this task.`,
        details: [
          ...requirements.map(requirement => requirement.reason),
          ...recommendedAgent.reasons
        ].slice(0, 5)
      };
    }

    const capabilityCatalog = await this.loadCapabilitySuggestionCatalog();
    const suggestions = this.getCapabilitySuggestionsForRequirements(
      requirements,
      capabilityCatalog
    );
    const defaults = this.getTaskRequirementSeedDefaults(requirements);

    return {
      kind: 'create_recommended',
      message: `This task likely needs ${requirementSummary}, but "${currentAgent}" does not advertise matching MCP servers, tools, or skills.`,
      details: [
        ...requirements.map(requirement => requirement.reason),
        suggestions.mcpServers.length > 0
          ? `Suggested MCP servers: ${suggestions.mcpServers.join(', ')}`
          : 'No matching MCP server is configured yet.',
        suggestions.skills.length > 0
          ? `Suggested skills: ${suggestions.skills.join(', ')}`
          : 'No matching reusable skill is available yet.'
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
      .map(
        badge =>
          `<span class="workspace-detail-capability-chip ${this.escapeHtml(badge.key)}">${this.escapeHtml(badge.label)}</span>`
      )
      .join('');

    return `<span class="workspace-detail-agent-capabilities">${chips}</span>`;
  }

  getWorkspaceFallbackAgent(options = {}) {
    const candidates = this.getWorkspaceAgentNames();
    if (candidates.length === 0) return '';

    if (options.preferBrowser === true) {
      const browserCandidate = candidates.find(name =>
        this.agentSupportsBrowserAutomation(this.getAgentProfile(name))
      );
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
    const status = String(task.status || '')
      .trim()
      .toLowerCase();
    const humanLoopState = String(task?.context?.human_loop?.state || '')
      .trim()
      .toLowerCase();
    if (humanLoopState === 'blocked' || humanLoopState === 'waiting_for_choice' || status === 'waiting_for_choice') return 'waiting_for_choice';
    return status || 'pending';
  }

  setExecutionModalStatus(status) {
    if (!this.elements.taskExecutionStatus) return;
    const safeStatus = String(status || 'pending')
      .trim()
      .toLowerCase();
    this.elements.taskExecutionStatus.className = `workspace-detail-task-status ${getStatusClass(safeStatus)}`;
    this.elements.taskExecutionStatus.textContent = getDisplayStatus(safeStatus);
  }

  clearExecutionLog() {
    this.executionLogKeys = new Set();
    this.executionLastStatus = '';
    if (!this.elements.taskExecutionLog) return;
    this.elements.taskExecutionLog.innerHTML =
      '<div class="workspace-detail-task-execution-empty">Execution updates will appear here.</div>';
  }

  appendExecutionLog(message, variant = 'info', dedupeKey = '') {
    if (!this.elements.taskExecutionLog || !message) return;
    const key = String(dedupeKey || '').trim();
    if (key) {
      if (this.executionLogKeys.has(key)) return;
      this.executionLogKeys.add(key);
    }

    const empty = this.elements.taskExecutionLog.querySelector(
      '.workspace-detail-task-execution-empty'
    );
    if (empty) empty.remove();

    const entry = document.createElement('div');
    const safeVariant = ['info', 'success', 'warning', 'error'].includes(variant)
      ? variant
      : 'info';
    entry.className = `workspace-detail-task-execution-log-entry ${safeVariant}`;

    const timeEl = document.createElement('div');
    timeEl.className = 'workspace-detail-task-execution-log-time';
    timeEl.textContent = new Date().toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit'
    });

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
    const updatedAt = formatDate(
      task.updated_at || task.completed_at || task.started_at || task.created_at
    );
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
    this.setExecutionNextStepEnabled(
      awaitingNextStep,
      awaitingNextStep ? 'Run Next Step' : 'Run Next Step'
    );
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
      this.appendExecutionLog(
        'Running the next internal step.',
        'info',
        `${this.currentExecutionTaskId}:next-step`
      );
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

    const modal =
      typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.taskExecutionModal)
        : bootstrap.Modal.getInstance(this.elements.taskExecutionModal) ||
          new bootstrap.Modal(this.elements.taskExecutionModal);
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
          this.appendExecutionLog(
            `Status changed to ${getDisplayStatus(state)}.`,
            state === 'failed'
              ? 'error'
              : state === 'blocked'
                ? 'warning'
                : state === 'completed'
                  ? 'success'
                  : 'info',
            `${taskId}:status:${state}:${task.updated_at || ''}`
          );
        }

        if (this.isTaskAwaitingNextStep(task)) {
          const nextStepIndex = Number(task?.context?.execution_step_waiting_index || 0);
          const nextStep = Array.isArray(task?.execution_steps)
            ? task.execution_steps.find(step => Number(step?.index) === nextStepIndex)
            : null;
          const nextLabel = nextStep?.title ? ` ${nextStep.title}` : '';
          this.appendExecutionLog(
            `Paused after the current step. Ready for step ${nextStepIndex || '?'}${nextLabel ? `: ${nextLabel}` : ''}.`,
            'info',
            `${taskId}:waiting:${nextStepIndex}`
          );
        }

        if (
          state === 'completed' ||
          state === 'failed' ||
          state === 'cancelled' ||
          state === 'timeout' ||
          state === 'blocked'
        ) {
          this.stopExecutionMonitor();
          this.setExecutionViewResultEnabled(Boolean(task.result || task.error));
          if (state === 'completed') {
            this.appendExecutionLog(
              'Execution completed successfully.',
              'success',
              `${taskId}:terminal:completed`
            );
          } else if (state === 'blocked') {
            this.appendExecutionLog(
              'Execution paused and requires your input.',
              'warning',
              `${taskId}:terminal:blocked`
            );
          } else {
            this.appendExecutionLog(
              'Execution ended with an issue.',
              'error',
              `${taskId}:terminal:${state}`
            );
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
    const eventData = event && typeof event === 'object' ? event.data || {} : {};
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
        this.appendExecutionLog(
          'Agent started executing this task.',
          'info',
          `${taskId}:evt:started:${payload?.updated_at || ''}`
        );
        break;
      case 'task.progress': {
        this.setExecutionModalStatus('in_progress');
        const progress = payload?.progress || {};
        const currentStep = String(progress?.current_step || '').trim();
        const waiting = payload?.waiting_for_next_step === true;
        if (currentStep) {
          this.appendExecutionLog(
            currentStep,
            waiting ? 'success' : 'info',
            `${taskId}:evt:progress:${currentStep}`
          );
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
        this.appendExecutionLog(
          payload?.reason || 'Execution paused and requires your input.',
          'warning',
          `${taskId}:evt:blocked:${payload?.updated_at || ''}`
        );
        this.stopExecutionMonitor();
        this.setExecutionViewResultEnabled(false);
        break;
      case 'task.completed':
        this.setExecutionModalStatus('completed');
        this.appendExecutionLog(
          'Task completed.',
          'success',
          `${taskId}:evt:completed:${payload?.updated_at || ''}`
        );
        this.stopExecutionMonitor();
        this.setExecutionViewResultEnabled(true);
        break;
      case 'task.failed':
        this.setExecutionModalStatus('failed');
        this.appendExecutionLog(
          payload?.error || 'Task failed.',
          'error',
          `${taskId}:evt:failed:${payload?.updated_at || ''}`
        );
        this.stopExecutionMonitor();
        this.setExecutionViewResultEnabled(true);
        break;
      case 'task.resumed':
        this.setExecutionModalStatus('in_progress');
        this.appendExecutionLog(
          'Task resumed after guidance.',
          'info',
          `${taskId}:evt:resumed:${payload?.updated_at || ''}`
        );
        this.startExecutionMonitor(taskId);
        break;
    }
  }

  async executeTask(taskId, options = {}) {
    await this.loadAgentCatalog();

    const task = this.tasks.find(item => item.id === taskId);
    if (!task) return;

    const subtasks = this.getSubtasksForParent(taskId);
    const isParent = subtasks.length > 0;
    const awaitingNextStep = !isParent && this.isTaskAwaitingNextStep(task);
    const stepAction = String(options?.stepAction || (awaitingNextStep ? 'next' : ''))
      .trim()
      .toLowerCase();
    const taskDescription = task.description || task.name || '';
    const taskRequirements = !isParent ? this.inferTaskExecutionRequirements(taskDescription) : [];
    const isBrowserIntent = taskRequirements.some(
      requirement => requirement.key === TASK_REQUIREMENT_KEYS.BROWSER
    );

    if (isParent) {
      const hasUnassigned = subtasks.some(subtask => !subtask.to || subtask.to === 'unassigned');
      if (hasUnassigned) {
        if (window.Toast)
          window.Toast.error('Assign agents to all subtasks before executing this workflow.');
        return;
      }
      const hasRunning = subtasks.some(subtask => subtask.status === 'in_progress');
      if (hasRunning) {
        if (window.Toast) window.Toast.error('A subtask is already running.');
        return;
      }
    } else if (task.status === 'in_progress' && !awaitingNextStep) {
      if (window.Toast) window.Toast.error('This task is already running.');
      return;
    }

    let assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
    const getExecutionSequencePreview = (agentName = assignedAgent) =>
      this.getPredictedTaskExecutionSequence(task, isParent ? subtasks : [], {
        assignedAgent: agentName
      });
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
          const assignAndRun =
            options.skipConfirm === true
              ? true
              : await this.showTaskConfirmDialog({
                  eyebrow: 'Assignment Required',
                  title: `Execute with "${fallbackAgent}"?`,
                  message: `This task is unassigned. I can assign it to "${fallbackAgent}" and start execution immediately.`,
                  confirmLabel: 'Assign and Execute',
                  metaItems: [this.getTaskDisplayLabel(task), fallbackAgent, 'Workspace agent'],
                  details: [
                    'The task will be updated before dispatch.',
                    'Live execution updates will appear after confirmation.'
                  ],
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
          metaItems: [
            this.getTaskDisplayLabel(task),
            preflight.recommendedAgent,
            'Recommended agent'
          ],
          details:
            Array.isArray(preflight.details) && preflight.details.length > 0
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
            suggestedMCPServers: Array.isArray(preflight.suggestedMCPServers)
              ? preflight.suggestedMCPServers
              : [],
            suggestedSkills: Array.isArray(preflight.suggestedSkills)
              ? preflight.suggestedSkills
              : []
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
        eyebrow:
          stepAction === 'next' ? 'Step Launch' : isParent ? 'Workflow Launch' : 'Task Launch',
        title:
          stepAction === 'next'
            ? 'Run the next step now?'
            : isParent
              ? 'Execute this workflow now?'
              : 'Execute this task now?',
        message:
          stepAction === 'next'
            ? 'This task is paused between internal execution steps. Ori will run the next step and keep the current plan intact.'
            : isParent
              ? `This workflow will dispatch ${subtasks.length} step${subtasks.length === 1 ? '' : 's'} in sequence.`
              : `This task will run with "${assignedAgent}".`,
        confirmLabel:
          stepAction === 'next' ? 'Run Next Step' : isParent ? 'Execute Workflow' : 'Execute Task',
        metaItems:
          stepAction === 'next'
            ? [this.getTaskDisplayLabel(task), assignedAgent, 'Step-through']
            : isParent
              ? [
                  this.getTaskDisplayLabel(task),
                  `${subtasks.length} step${subtasks.length === 1 ? '' : 's'}`
                ]
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

    const selectedStepThrough =
      !isParent && stepAction !== 'next'
        ? options.skipConfirm === true
          ? this.getTaskExecutionMode(task) === 'step_through'
          : this.consumePendingTaskConfirmStepThroughSelection()
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
          execution_mode: isParent ? undefined : selectedStepThrough ? 'step_through' : 'auto'
        })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to execute task');
      }

      if (window.Toast) window.Toast.success('Task started');
      this.appendExecutionLog(
        'Task dispatched. Waiting for agent updates...',
        'info',
        `${taskId}:dispatched`
      );
      await this.loadTasks();
      this.startExecutionMonitor(taskId);
    } catch (error) {
      console.error('Failed to execute task:', error);
      const message = error && error.message ? error.message : 'Failed to execute task';
      if (this.isMissingWorkspaceAgentError(message)) {
        this.setExecutionModalStatus('blocked');
        this.appendExecutionLog(
          'No agent is assigned to this workspace yet. Ori is preparing a suggested agent setup.',
          'warning',
          `${taskId}:dispatch-agent-missing`
        );
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
        if (
          status === 'completed' ||
          status === 'failed' ||
          status === 'cancelled' ||
          status === 'timeout'
        ) {
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

  focusSection(sectionKey) {
    const key = String(sectionKey || '').toLowerCase();
    const targets = {
      agents: 'workspace-detail-agents-panel',
      tasks: 'workspace-detail-agents-panel',
      notes: 'workspace-detail-notes-panel',
      sessions: 'workspace-detail-sessions-panel',
      folders: 'workspace-detail-directories-panel',
      schedules: 'workspace-detail-schedules-panel',
      mcp: 'workspace-detail-config-content',
      skills: 'workspace-detail-config-content'
    };
    const targetId = targets[key];
    if (!targetId) return;

    if (key === 'agents') {
      this.setView('list');
    } else if (key === 'tasks') {
      this.setView('board');
    } else if (key === 'mcp' || key === 'skills') {
      this.setWorkspaceConfigExpanded(true);
      const tabId =
        key === 'mcp'
          ? 'workspace-detail-config-mcp-tab'
          : 'workspace-detail-config-skills-tab';
      this.activateWorkspaceConfigTab(tabId);
    }

    const target = document.getElementById(targetId);
    if (!target) return;

    if (typeof target.scrollIntoView === 'function') {
      target.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
    this.focusDeepLinkTarget(target);
  }

  activateWorkspaceConfigTab(tabId) {
    const tabBtn = document.getElementById(tabId);
    if (!tabBtn) return;

    if (typeof tabBtn.click === 'function') {
      tabBtn.click();
      return;
    }
    if (window.bootstrap?.Tab?.getOrCreateInstance) {
      window.bootstrap.Tab.getOrCreateInstance(tabBtn).show();
    }
  }

  focusDeepLinkTarget(target) {
    if (!target || typeof target.focus !== 'function') return;
    if (!target.hasAttribute('tabindex')) {
      target.setAttribute('tabindex', '-1');
    }
    try {
      target.focus({ preventScroll: true });
    } catch {
      target.focus();
    }
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
    this.getWorkspaceAgentNames().forEach(name => {
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
      return raw.map(label => String(label || '').trim()).filter(Boolean);
    }
    if (typeof raw === 'string') {
      return raw
        .split(',')
        .map(label => label.trim())
        .filter(Boolean);
    }
    return [];
  }

  formatLabelsInput(labels) {
    return (labels || []).join(', ');
  }

  parseLabelsInput(value) {
    if (!value) return [];
    return value
      .split(',')
      .map(label => label.trim())
      .filter(Boolean);
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
    const date = /^\d{4}-\d{2}-\d{2}$/.test(value)
      ? new Date(`${value}T00:00:00`)
      : new Date(value);
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
    const options = Array.isArray(this.agentOptions)
      ? this.agentOptions.slice()
      : [{ label: 'Unassigned', value: '' }];
    if (selectedValue && !options.some(opt => opt.value === selectedValue)) {
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
    const existing = new Set(
      (columns || []).map(col => String(col?.id || '').trim()).filter(Boolean)
    );
    const base =
      String(name || '')
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
      this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach(card => {
        if (card._editOutsideHandler) {
          document.removeEventListener('click', card._editOutsideHandler);
          card._editOutsideHandler = null;
        }
      });
      this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add').forEach(addEl => {
        if (addEl._addOutsideHandler) {
          document.removeEventListener('click', addEl._addOutsideHandler);
          addEl._addOutsideHandler = null;
        }
      });
      this.elements.boardColumns
        .querySelectorAll('.workspace-detail-board-add-column')
        .forEach(addEl => {
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

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach(card => {
      if (card._editOutsideHandler) {
        document.removeEventListener('click', card._editOutsideHandler);
        card._editOutsideHandler = null;
      }
    });
    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add').forEach(addEl => {
      if (addEl._addOutsideHandler) {
        document.removeEventListener('click', addEl._addOutsideHandler);
        addEl._addOutsideHandler = null;
      }
    });
    this.elements.boardColumns
      .querySelectorAll('.workspace-detail-board-add-column')
      .forEach(addEl => {
        if (addEl._addOutsideHandler) {
          document.removeEventListener('click', addEl._addOutsideHandler);
          addEl._addOutsideHandler = null;
        }
      });

    const columnsHtml = columns
      .map(col => {
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
      })
      .join('');

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
    const labelMarkup =
      labels.length > 0
        ? `<div class="workspace-detail-board-card-labels">${labels.map(label => `<span class="workspace-detail-board-card-label">${this.escapeHtml(label)}</span>`).join('')}</div>`
        : '';
    const dueMarkup = dueDate
      ? `<span class="workspace-detail-board-card-due">Due ${this.escapeHtml(this.formatDueDate(dueDate))}</span>`
      : '';
    const assignedMarkup =
      assignmentLabel && assignmentLabel !== 'Unassigned'
        ? `<span class="workspace-detail-board-card-assignee">${this.escapeHtml(assignmentLabel)}</span>`
        : '<span class="workspace-detail-board-card-assignee is-muted">Unassigned</span>';
    const scheduleIndicator = this.renderTaskScheduleIndicator(task, 'board');
    const referenceIndicator = this.renderTaskReferenceURLIndicator(task, 'board');
    const editTitleValue = this.escapeHtml(task.description || task.name || task.id || '');
    const editDetailsValue = this.escapeHtml(task.details || '');
    const editLabelsValue = this.escapeHtml(this.formatLabelsInput(labels));
    const editDueValue = this.escapeHtml(this.normalizeDueInput(dueDate));
    const assignmentOptionsHtml = assignmentOptions
      .map(opt => {
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
            ${referenceIndicator}
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
    columns.forEach(col => {
      groups[col.id] = [];
    });

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
      const handler = evt => {
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
    const idx = this.tasks.findIndex(t => t && t.id === taskId);
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
    const idx = this.tasks.findIndex(t => t && t.id === taskId);
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

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach(card => {
      card.addEventListener('click', e => {
        if (this.boardDidDrag) return;
        if (card.classList.contains('is-editing')) return;
        if (
          e.target.closest('.workspace-detail-board-card-edit') ||
          e.target.closest('.workspace-detail-board-card-edit-btn')
        )
          return;
        if (
          e.target.closest('input') ||
          e.target.closest('textarea') ||
          e.target.closest('select') ||
          e.target.closest('button')
        )
          return;
        const taskId = card.dataset.taskId;
        if (taskId) this.openTask(taskId);
      });

      const editBtn = card.querySelector('.workspace-detail-board-card-edit-btn');
      if (editBtn) {
        editBtn.addEventListener('click', e => {
          e.preventDefault();
          e.stopPropagation();
          this.enterBoardCardEdit(card);
        });
      }

      const editContainer = card.querySelector('.workspace-detail-board-card-edit');
      if (editContainer) {
        editContainer.addEventListener('click', e => e.stopPropagation());
      }

      const cancelBtn = card.querySelector('.workspace-detail-board-card-edit-cancel');
      if (cancelBtn) {
        cancelBtn.addEventListener('click', e => {
          e.preventDefault();
          e.stopPropagation();
          this.exitBoardCardEdit(card, { reset: true });
        });
      }

      const saveBtn = card.querySelector('.workspace-detail-board-card-edit-save');
      if (saveBtn) {
        saveBtn.addEventListener('click', e => {
          e.preventDefault();
          e.stopPropagation();
          this.saveBoardCardEdits(card);
        });
      }

      const editFields = card.querySelectorAll(
        '.workspace-detail-board-card-edit input, .workspace-detail-board-card-edit textarea, .workspace-detail-board-card-edit select'
      );
      editFields.forEach(field => {
        field.addEventListener('click', e => e.stopPropagation());
        field.addEventListener('keydown', e => {
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

    this.elements.boardColumns
      .querySelectorAll('.workspace-detail-board-add')
      .forEach(container => {
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
            const handler = evt => {
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

        button.addEventListener('click', e => {
          e.preventDefault();
          e.stopPropagation();
          openForm();
        });

        cancelBtn.addEventListener('click', e => {
          e.preventDefault();
          e.stopPropagation();
          closeForm();
        });

        submitBtn.addEventListener('click', e => {
          e.preventDefault();
          e.stopPropagation();
          submitForm();
        });

        input.addEventListener('keydown', e => {
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

    const container = this.elements.boardColumns.querySelector(
      '.workspace-detail-board-add-column'
    );
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
        const handler = evt => {
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
        const columns = Array.isArray(this.boardConfig?.columns)
          ? this.boardConfig.columns.slice()
          : [];
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

    button.addEventListener('click', e => {
      e.preventDefault();
      e.stopPropagation();
      openForm();
    });

    cancelBtn.addEventListener('click', e => {
      e.preventDefault();
      e.stopPropagation();
      closeForm();
    });

    submitBtn.addEventListener('click', e => {
      e.preventDefault();
      e.stopPropagation();
      submitForm();
    });

    input.addEventListener('keydown', e => {
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

    this.elements.boardColumns
      .querySelectorAll('.workspace-detail-board-column-header')
      .forEach(headerEl => {
        const titleEl = headerEl.querySelector('.workspace-detail-board-column-title');
        const editBtn = headerEl.querySelector('.workspace-detail-board-column-edit-btn');
        const titleWrap = headerEl.querySelector('.workspace-detail-board-column-title-wrap');
        if (!titleEl || !editBtn) return;

        editBtn.addEventListener('click', e => {
          e.preventDefault();
          e.stopPropagation();

          const columnId =
            titleEl.dataset.columnId ||
            titleEl.closest('.workspace-detail-board-column')?.dataset.columnId;
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

            const columns = (this.boardConfig?.columns || []).map(col => {
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
          input.addEventListener('keydown', evt => {
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

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach(card => {
      card.addEventListener('dragstart', e => {
        if (
          card.classList.contains('is-editing') ||
          e.target.closest('.workspace-detail-board-card-edit')
        ) {
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

    this.elements.boardColumns
      .querySelectorAll('.workspace-detail-board-column-body')
      .forEach(colEl => {
        colEl.addEventListener('dragover', e => {
          e.preventDefault();
          colEl.closest('.workspace-detail-board-column')?.classList.add('is-drag-over');
          e.dataTransfer.dropEffect = 'move';
        });
        colEl.addEventListener('dragleave', () => {
          colEl.closest('.workspace-detail-board-column')?.classList.remove('is-drag-over');
        });
        colEl.addEventListener('drop', async e => {
          e.preventDefault();
          const columnId =
            colEl.closest('.workspace-detail-board-column')?.dataset.columnId ||
            colEl.dataset.columnId;
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
      const response = await fetch(
        `/api/sessions?folder_id=${encodeURIComponent(this.workspaceId)}`
      );
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
        this.elements.sessionCount.setAttribute('aria-busy', 'false');
        this.elements.sessionCount.setAttribute('aria-label', `${this.sessions.length} sessions`);
      }
      this.refreshHomeAssistantQuickPrompts();
      window.workspaceCommand?.refresh();
    }
  }

  /**
   * Render sessions into the workspace-level sessions panel
   */
  renderSessions() {
    if (!this.elements.sessionsList) return;

    if (this.sessionsLoading) {
      this.elements.sessionsList.innerHTML =
        '<div class="workspace-detail-loading">Loading sessions...</div>';
      return;
    }

    if (this.sessionsLoadFailed) {
      this.elements.sessionsList.innerHTML =
        '<div class="workspace-detail-empty">Failed to load sessions.</div>';
      return;
    }

    const sessions = Array.isArray(this.sessions) ? this.sessions : [];
    if (sessions.length === 0) {
      this.elements.sessionsList.innerHTML =
        '<div class="workspace-detail-empty">No sessions yet.</div>';
      return;
    }

    this.elements.sessionsList.innerHTML = sessions
      .map(session => this.renderSessionItem(session))
      .join('');
  }

  /**
   * Load files for the workspace
   */
  async loadFiles() {
    if (this.elements.filesList) {
      this.elements.filesList.innerHTML =
        '<div class="workspace-detail-loading">Loading files...</div>';
    }
    this.filesLoaded = false;
    this.filesLoadFailed = false;
    this.renderWorkspaceHealth();

    try {
      await this.syncWorkspaceFilesFromDisk();

      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) {
        this.files = [];
        this.filesLoaded = true;
        this.filesLoadFailed = true;
        this.renderFiles();
        this.refreshHomeAssistantQuickPrompts();
        this.renderWorkspaceHealth();
        return;
      }

      const workspace = await response.json();
      // Filter attachments to only include files (not text content)
      this.files = (workspace.attachments || []).filter(
        a => a.file_meta || a.type === 'image' || a.type === 'other'
      );
      this.filesLoaded = true;
      this.filesLoadFailed = false;
      this.renderFiles();
      this.refreshHomeAssistantQuickPrompts();
      this.renderWorkspaceHealth();
    } catch (error) {
      console.error('Failed to load files:', error);
      this.files = [];
      this.filesLoaded = true;
      this.filesLoadFailed = true;
      this.renderFiles();
      this.refreshHomeAssistantQuickPrompts();
      this.renderWorkspaceHealth();
    }
  }

  async syncWorkspaceFilesFromDisk() {
    if (!this.workspaceId) return;

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/files/tree`,
        { cache: 'no-store' }
      );
      if (!response.ok) {
        console.warn('Workspace file sync failed:', response.status);
      }
    } catch (error) {
      console.warn('Workspace file sync failed:', error);
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

    this.elements.filesList.innerHTML = this.files
      .map(file => {
        const title = file.title || (file.file_meta && file.file_meta.name) || 'Untitled File';
        const size = file.file_meta ? file.file_meta.size : null;
        const isMissing = file.file_meta?.status === 'missing';
        const vaultReference = this.normalizeVaultReference(file.vault_reference);
        const metaParts = [];
        if (size) metaParts.push(this.formatFileSize(size));
        metaParts.push(formatDate(file.created_at));
        const metaText = this.escapeHtml(metaParts.join(' · '));
        return `
	        <div class="workspace-detail-item" data-file-id="${file.id}">
	          ${
              vaultReference
                ? `
	            <button type="button" class="workspace-detail-item-sync" onclick="event.stopPropagation(); window.workspaceDetail?.syncVaultFile('${file.id}', this)" title="Sync to Vault" aria-label="Sync ${this.escapeHtml(title)} to private vault">
	              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
	                <path d="M12,3L18,9H14V14H10V9H6L12,3M5,18H19V20H5V18Z"/>
	              </svg>
	            </button>
	          `
                : ''
            }
	          ${
              isMissing
                ? `
	            <button type="button" class="workspace-detail-item-run${vaultReference ? ' is-shifted' : ''}" onclick="event.stopPropagation(); window.workspaceDetail?.promptRelinkWorkspaceFile('${file.id}')" title="Choose a replacement file" aria-label="Relink missing file ${this.escapeHtml(title)}">
	              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
	                <path d="M10.59,13.41C11,13.8 11,14.44 10.59,14.83C10.2,15.22 9.56,15.22 9.17,14.83C7.22,12.88 7.22,9.71 9.17,7.76L12.71,4.22C14.66,2.27 17.83,2.27 19.78,4.22C21.73,6.17 21.73,9.34 19.78,11.29L18.29,12.78C18.3,11.96 18.17,11.14 17.89,10.36L18.36,9.88C19.54,8.71 19.54,6.81 18.36,5.64C17.19,4.46 15.29,4.46 14.12,5.64L10.59,9.17C9.41,10.34 9.41,12.24 10.59,13.41Z"/>
	              </svg>
            </button>
          `
                : ''
            }
          <div>
	            <div class="workspace-detail-item-title">${this.escapeHtml(title)}</div>
	            <div class="workspace-detail-item-meta">
	              ${isMissing ? '<span class="workspace-detail-status-badge is-missing">Missing</span>' : ''}
	              ${metaText}
	              ${vaultReference ? this.renderVaultReferenceBadge(vaultReference) : ''}
	            </div>
	          </div>
	        </div>
	      `;
      })
      .join('');
  }

  promptRelinkWorkspaceFile(fileId) {
    if (!fileId) return;

    const input = document.createElement('input');
    input.type = 'file';
    input.style.display = 'none';
    input.addEventListener(
      'change',
      async () => {
        const selected = input.files && input.files[0];
        input.remove();
        if (!selected) return;
        await this.relinkWorkspaceFile(fileId, selected);
      },
      { once: true }
    );

    document.body.appendChild(input);
    input.click();
  }

  async relinkWorkspaceFile(fileId, file) {
    if (!fileId || !file) return;

    const formData = new FormData();
    formData.append('file', file);

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/attachments/${encodeURIComponent(fileId)}/relink`,
        {
          method: 'POST',
          body: formData
        }
      );
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

  async responseErrorMessage(response, fallback) {
    const payload = await response.json().catch(() => ({}));
    return payload.error || payload.message || fallback;
  }

  setVaultSyncButtonState(button, isBusy) {
    if (!button) return;
    button.disabled = Boolean(isBusy);
    button.classList.toggle('is-syncing', Boolean(isBusy));
  }

  async syncVaultFile(fileId, button = null) {
    const file = this.files.find(item => item.id === fileId);
    const vaultReference = this.normalizeVaultReference(file?.vault_reference);
    if (!file || !vaultReference) {
      if (window.Toast) window.Toast.error('This file is not linked to a private vault entry.');
      return;
    }

    const fileMeta = file.file_meta || {};
    const fileURL = String(fileMeta.url || '').trim();
    if (!fileURL) {
      if (window.Toast) window.Toast.error('This workspace file is missing a readable file URL.');
      return;
    }

    this.setVaultSyncButtonState(button, true);
    try {
      const fileResponse = await fetch(fileURL);
      if (!fileResponse.ok) {
        throw new Error(
          await this.responseErrorMessage(fileResponse, 'Failed to read workspace file')
        );
      }

      const blob = await fileResponse.blob();
      const filename =
        fileMeta.name || file.title || vaultReference.attachment_name || 'workspace-file';
      const formData = new FormData();
      formData.append('file', blob, filename);
      if (file.type === 'image' || String(fileMeta.mime || '').startsWith('image/')) {
        formData.append('kind', 'image');
      }

      const uploadResponse = await fetch(
        `/api/vault/records/${encodeURIComponent(vaultReference.record_id)}/attachments`,
        {
          method: 'POST',
          body: formData
        }
      );
      if (!uploadResponse.ok) {
        throw new Error(
          await this.responseErrorMessage(uploadResponse, 'Failed to sync file to private vault')
        );
      }

      const uploadPayload = await uploadResponse.json();
      const uploadedAttachment = uploadPayload.attachment || {};
      const previousAttachmentID = vaultReference.attachment_id;
      const nextReference = this.normalizeVaultReference({
        ...vaultReference,
        attachment_id: uploadedAttachment.id || previousAttachmentID,
        attachment_name: uploadedAttachment.name || filename,
        last_synced_at: new Date().toISOString()
      });

      const updateResponse = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/attachments/${encodeURIComponent(fileId)}`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ vault_reference: nextReference })
        }
      );
      if (!updateResponse.ok) {
        throw new Error(
          await this.responseErrorMessage(
            updateResponse,
            'File synced, but workspace reference could not be updated'
          )
        );
      }

      if (
        previousAttachmentID &&
        uploadedAttachment.id &&
        previousAttachmentID !== uploadedAttachment.id
      ) {
        const deleteResponse = await fetch(
          `/api/vault/records/${encodeURIComponent(vaultReference.record_id)}/attachments/${encodeURIComponent(previousAttachmentID)}`,
          {
            method: 'DELETE'
          }
        ).catch(() => null);
        if (deleteResponse && !deleteResponse.ok && window.Toast) {
          window.Toast.warning(
            'Synced a new vault copy, but the previous vault attachment could not be removed.'
          );
        }
      }

      if (window.Toast) window.Toast.success('File synced to private vault');
      await this.loadFiles();
    } catch (error) {
      console.error('Failed to sync workspace file to vault:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to sync file to private vault');
    } finally {
      this.setVaultSyncButtonState(button, false);
    }
  }

  inferVaultPayloadKey(payload, fallback = '') {
    if (fallback) return fallback;
    if (payload && typeof payload === 'object' && !Array.isArray(payload)) {
      for (const key of ['note', 'content', 'text', 'body', 'value']) {
        if (Object.prototype.hasOwnProperty.call(payload, key)) {
          return key;
        }
      }
    }
    return 'note';
  }

  async syncVaultNote(noteId, button = null) {
    const listedNote = this.notes.find(item => item.id === noteId);
    this.setVaultSyncButtonState(button, true);
    try {
      const noteResponse = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes/${encodeURIComponent(noteId)}`
      );
      if (!noteResponse.ok) {
        throw new Error(
          await this.responseErrorMessage(noteResponse, 'Failed to load workspace note')
        );
      }
      const note = await noteResponse.json();
      const vaultReference = this.normalizeVaultReference(
        note.vault_reference || listedNote?.vault_reference
      );
      if (!vaultReference) {
        throw new Error('This note is not linked to a private vault entry.');
      }

      const recordResponse = await fetch(
        `/api/vault/records/${encodeURIComponent(vaultReference.record_id)}`
      );
      if (!recordResponse.ok) {
        throw new Error(
          await this.responseErrorMessage(recordResponse, 'Failed to load private vault entry')
        );
      }
      const record = await recordResponse.json();
      const payload =
        record &&
        record.payload &&
        typeof record.payload === 'object' &&
        !Array.isArray(record.payload)
          ? { ...record.payload }
          : {};
      delete payload.attachments;

      const payloadKey = this.inferVaultPayloadKey(payload, vaultReference.payload_key);
      payload[payloadKey] = String(note.content || '');

      const patchResponse = await fetch(
        `/api/vault/records/${encodeURIComponent(vaultReference.record_id)}`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ payload })
        }
      );
      if (!patchResponse.ok) {
        throw new Error(
          await this.responseErrorMessage(patchResponse, 'Failed to sync note to private vault')
        );
      }

      const nextReference = this.normalizeVaultReference({
        ...vaultReference,
        payload_key: payloadKey,
        last_synced_at: new Date().toISOString()
      });
      const updateResponse = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes/${encodeURIComponent(noteId)}`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ vault_reference: nextReference })
        }
      );
      if (!updateResponse.ok) {
        throw new Error(
          await this.responseErrorMessage(
            updateResponse,
            'Note synced, but workspace reference could not be updated'
          )
        );
      }

      if (window.Toast) window.Toast.success('Note synced to private vault');
      await this.loadNotes();
    } catch (error) {
      console.error('Failed to sync workspace note to vault:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to sync note to private vault');
    } finally {
      this.setVaultSyncButtonState(button, false);
    }
  }

  /**
   * Load notes for the workspace
   */
  async loadNotes() {
    this.updateCopyNotesButtonState(true);
    if (this.elements.notesList) {
      this.elements.notesList.innerHTML =
        '<div class="workspace-detail-loading">Loading notes...</div>';
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
    } finally {
      window.workspaceCommand?.refresh();
    }
  }

  /**
   * Render notes list
   */
  renderNotes() {
    if (!this.elements.notesList) return;

    // Drop selections for notes that no longer exist (e.g. after a reload).
    const existingIds = new Set(this.notes.map(note => String(note.id)));
    this.selectedNoteIds.forEach(id => {
      if (!existingIds.has(id)) this.selectedNoteIds.delete(id);
    });

    if (this.notes.length === 0) {
      this.syncNotesTagFilter();
      this.elements.notesList.innerHTML = '<div class="workspace-detail-empty">No notes yet.</div>';
      this.updateCopyNotesButtonState(false);
      return;
    }

    this.syncNotesTagFilter();
    const activeNoteTags = this.notesTagFilterBar ? this.notesTagFilterBar.getActiveTags() : [];
    const visibleNotes = window.OriTagFilterBar
      ? window.OriTagFilterBar.filterItems(this.notes, activeNoteTags)
      : this.notes;

    if (visibleNotes.length === 0) {
      this.elements.notesList.innerHTML =
        '<div class="workspace-detail-empty">No notes match the selected tags.</div>';
      this.updateCopyNotesButtonState(false);
      return;
    }

    this.elements.notesList.innerHTML = visibleNotes
      .map(note => {
        const title = note.name || note.title || 'Untitled Note';
        const preview = note.preview || note.content || '';
        const noteTags = Array.isArray(note.tags)
          ? note.tags.map(tag => String(tag || '').trim()).filter(Boolean)
          : [];
        const tagChips = noteTags.length
          ? `<div class="workspace-detail-item-tags">${noteTags
              .map(
                tag =>
                  `<span class="workspace-detail-tag-chip" title="${this.escapeAttribute(tag)}">${this.escapeHtml(tag)}</span>`
              )
              .join('')}</div>`
          : '';
        const noteUrl = `/notes/${encodeURIComponent(note.id)}`;
        const vaultReference = this.normalizeVaultReference(note.vault_reference);
        const isSelected = this.selectedNoteIds.has(String(note.id));
        return `
	      <div class="workspace-detail-item workspace-detail-note-item${isSelected ? ' is-selected' : ''}" data-note-id="${note.id}">
	        <label class="workspace-detail-note-select-label" title="Select note" onclick="event.stopPropagation()">
	          <input type="checkbox" class="workspace-detail-note-select" data-note-id="${note.id}" ${isSelected ? 'checked' : ''}
	                 aria-label="Select note ${this.escapeHtml(title)}"
	                 onclick="event.stopPropagation()"
	                 onchange="window.workspaceDetail?.toggleNoteSelection('${note.id}', this.checked)">
	        </label>
	        ${
            vaultReference
              ? `
	        <button type="button" class="workspace-detail-item-sync" onclick="event.stopPropagation(); window.workspaceDetail?.syncVaultNote('${note.id}', this)" title="Sync to Vault" aria-label="Sync note ${this.escapeHtml(title)} to private vault">
	          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
	            <path d="M12,3L18,9H14V14H10V9H6L12,3M5,18H19V20H5V18Z"/>
	          </svg>
	        </button>
	        `
              : ''
          }
	        <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteNote('${note.id}')" title="Delete note" aria-label="Delete note ${this.escapeHtml(title)}">
	          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
	            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <a class="workspace-detail-item-content"
             href="${this.escapeHtml(noteUrl)}"
             aria-label="Open note ${this.escapeHtml(title)}"
             onclick="event.stopPropagation()">
	          <div class="workspace-detail-item-title">${this.escapeHtml(title)}</div>
	          ${tagChips}
	          <div class="workspace-detail-item-meta">
	            ${preview ? this.escapeHtml(preview.substring(0, 50)) + (preview.length > 50 ? '…' : '') : 'Empty note'}
	            ${vaultReference ? this.renderVaultReferenceBadge(vaultReference) : ''}
	          </div>
	        </a>
	      </div>
    `;
      })
      .join('');
    this.updateCopyNotesButtonState(false);
  }

  // Mounts the shared tag filter bar above the notes list and keeps its
  // available tags in sync. The bar hides itself while no note has tags.
  syncNotesTagFilter() {
    const mount = document.getElementById('workspace-detail-notes-tag-filter');
    if (!mount || !window.OriTagFilterBar?.createTagFilterBar) return;
    if (!this.notesTagFilterBar) {
      this.notesTagFilterBar = window.OriTagFilterBar.createTagFilterBar({
        container: mount,
        onChange: () => this.renderNotes()
      });
    }
    this.notesTagFilterBar.setAvailableTags(window.OriTagFilterBar.collectTags(this.notes));
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

  getWorkspaceProjectPath() {
    return String(this.workspace?.project_path || '').trim();
  }

  workspaceHasProject() {
    return this.getWorkspaceProjectPath() !== '';
  }

  getProjectDirectoryId() {
    return String(this.workspace?.shared_data?.project_directory_id || '').trim();
  }

  isProjectDirectory(dir) {
    const projectDirectoryId = this.getProjectDirectoryId();
    return Boolean(projectDirectoryId && dir?.id === projectDirectoryId);
  }

  isGroupWorkspace() {
    return String(this.workspace?.kind || '').trim().toLowerCase() === 'group';
  }

  syncProjectActionState() {
    const button = this.elements.createProjectBtn;
    if (!button) return;

    const disabled = this.isGroupWorkspace() || this.workspaceHasProject();
    button.disabled = disabled;
    button.setAttribute('aria-disabled', disabled ? 'true' : 'false');
    if (this.isGroupWorkspace()) {
      button.title = 'Groups cannot hold projects';
    } else if (this.workspaceHasProject()) {
      button.title = 'This workspace already has a project';
    } else {
      button.title = 'Create project from template';
    }
  }

  getProjectTemplateModalInstance() {
    if (
      !this.elements.projectTemplateModal ||
      typeof bootstrap === 'undefined' ||
      !bootstrap.Modal
    ) {
      return null;
    }
    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.elements.projectTemplateModal)
      : new bootstrap.Modal(this.elements.projectTemplateModal);
  }

  clearProjectTemplateError() {
    const errorEl = this.elements.projectTemplateError;
    if (!errorEl) return;
    errorEl.textContent = '';
    errorEl.hidden = true;
  }

  setProjectTemplateError(message) {
    const text = String(message || '').trim();
    const errorEl = this.elements.projectTemplateError;
    if (!errorEl) {
      if (text && window.Toast) window.Toast.error(text);
      return;
    }
    errorEl.textContent = text;
    errorEl.hidden = !text;
  }

  resetProjectTemplateForm() {
    if (this.elements.projectTemplateSelect) {
      this.elements.projectTemplateSelect.value = '';
    }
    if (this.elements.projectTemplatePathInput) {
      this.elements.projectTemplatePathInput.value = '';
    }
    if (this.elements.projectNameInput) {
      this.elements.projectNameInput.value = '';
      this.elements.projectNameInput.placeholder = this.workspace?.name || 'Uses workspace name';
    }
    this.clearProjectTemplateError();
    this.updateProjectTemplateModalState();
  }

  async showProjectTemplateModal() {
    if (this.isGroupWorkspace()) {
      if (window.Toast) window.Toast.error('Groups cannot hold projects');
      return;
    }
    if (this.workspaceHasProject()) {
      if (window.Toast) window.Toast.info('This workspace already has a project');
      return;
    }

    const modal = this.getProjectTemplateModalInstance();
    if (!modal) {
      if (window.Toast) window.Toast.error('Project template dialog is unavailable');
      return;
    }

    this.resetProjectTemplateForm();
    modal.show();
    await this.populateProjectTemplateOptions();
  }

  async populateProjectTemplateOptions({ force = false } = {}) {
    const select = this.elements.projectTemplateSelect;
    if (!select) return;

    if (this.projectTemplates.length > 0 && !force) {
      this.renderProjectTemplateOptions();
      return;
    }

    select.disabled = true;
    select.innerHTML = '<option value="">Loading templates...</option>';
    this.clearProjectTemplateError();

    try {
      const response = await fetch('/api/project-templates');
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload.error || 'Failed to load project templates');
      }

      this.projectTemplates = Array.isArray(payload.templates) ? payload.templates : [];
      this.projectTemplatesRoot = String(payload.templates_root || '').trim();
      this.renderProjectTemplateOptions();
    } catch (error) {
      console.error('Failed to load project templates:', error);
      this.projectTemplates = [];
      this.renderProjectTemplateOptions();
      this.setProjectTemplateError(error.message || 'Failed to load project templates');
    }
  }

  renderProjectTemplateOptions() {
    const select = this.elements.projectTemplateSelect;
    if (!select) return;

    select.innerHTML = '<option value="">Choose a template</option>';
    for (const template of this.projectTemplates) {
      if (!template || !template.id) continue;
      const option = document.createElement('option');
      option.value = template.id;
      option.textContent = template.name || template.id;
      option.dataset.description = template.description || '';
      select.appendChild(option);
    }
    select.disabled = false;

    if (this.elements.projectTemplateRoot) {
      if (this.projectTemplatesRoot) {
        this.elements.projectTemplateRoot.textContent = `Library: ${this.projectTemplatesRoot}`;
        this.elements.projectTemplateRoot.hidden = false;
      } else {
        this.elements.projectTemplateRoot.textContent = '';
        this.elements.projectTemplateRoot.hidden = true;
      }
    }

    this.updateProjectTemplateModalState();
  }

  updateProjectTemplateModalState() {
    const select = this.elements.projectTemplateSelect;
    const description = this.elements.projectTemplateDescription;
    if (!description) return;

    const selected = select?.selectedOptions?.[0];
    const text = selected?.dataset?.description || '';
    description.textContent = text;
    description.hidden = !text;
  }

  async browseProjectTemplateFolder() {
    const input = this.elements.projectTemplatePathInput;
    const button = this.elements.projectTemplateBrowseBtn;
    if (!input) return;

    const originalText = button?.textContent || 'Choose';
    if (button) {
      button.disabled = true;
      button.textContent = 'Choosing...';
    }

    try {
      const response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Select a Template Folder' })
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success) {
        throw new Error(result.error || 'Failed to open folder picker');
      }
      if (result.selected && result.path) {
        input.value = result.path;
        if (this.elements.projectTemplateSelect) {
          this.elements.projectTemplateSelect.value = '';
        }
        this.updateProjectTemplateModalState();
      }
    } catch (error) {
      console.error('Failed to browse project template folder:', error);
      this.setProjectTemplateError(error.message || 'Failed to open folder picker');
    } finally {
      if (button) {
        button.disabled = false;
        button.textContent = originalText;
      }
    }
  }

  setProjectTemplateSubmitting(isSubmitting) {
    this.projectTemplateSubmitting = Boolean(isSubmitting);
    const submit = this.elements.projectTemplateSubmitBtn;
    if (!submit) return;
    if (this.projectTemplateSubmitting) {
      submit.disabled = true;
      submit.dataset.originalText = submit.textContent || 'Create Project';
      submit.textContent = 'Creating...';
    } else {
      submit.disabled = false;
      submit.textContent = submit.dataset.originalText || 'Create Project';
    }
  }

  async createWorkspaceProject() {
    if (this.projectTemplateSubmitting) return;
    if (this.isGroupWorkspace()) {
      this.setProjectTemplateError('Groups cannot hold projects');
      return;
    }
    if (this.workspaceHasProject()) {
      this.setProjectTemplateError('This workspace already has a project');
      return;
    }

    const templateId = this.elements.projectTemplateSelect?.value?.trim() || '';
    const templatePath = this.elements.projectTemplatePathInput?.value?.trim() || '';
    const projectName = this.elements.projectNameInput?.value?.trim() || '';

    if (templateId && templatePath) {
      this.setProjectTemplateError('Choose a library template or a folder, not both');
      return;
    }
    if (!templateId && !templatePath) {
      this.setProjectTemplateError('Choose a template first');
      return;
    }

    const payload = {};
    if (templateId) {
      payload.template_id = templateId;
    } else {
      payload.template_path = templatePath;
    }
    if (projectName) {
      payload.project_name = projectName;
    }

    this.setProjectTemplateSubmitting(true);
    this.clearProjectTemplateError();
    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/project`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok || result.error) {
        throw new Error(result.error || 'Failed to create project');
      }

      if (result.workspace && typeof result.workspace === 'object') {
        this.workspace = { ...(this.workspace || {}), ...result.workspace };
      }

      const modal = this.getProjectTemplateModalInstance();
      modal?.hide();
      if (window.Toast) {
        window.Toast.success('Project created');
      }
      await this.loadWorkspace();
      await this.loadDirectories();
      await this.loadFiles();
    } catch (error) {
      console.error('Failed to create project:', error);
      this.setProjectTemplateError(error.message || 'Failed to create project');
    } finally {
      this.setProjectTemplateSubmitting(false);
      this.syncProjectActionState();
    }
  }

  /**
   * Load directories for the workspace
   */
  async loadDirectories() {
    if (this.elements.directoriesList) {
      this.elements.directoriesList.innerHTML =
        '<div class="workspace-detail-loading">Loading directories...</div>';
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

      if (workspace && typeof workspace === 'object') {
        if (!this.workspace || typeof this.workspace !== 'object') {
          this.workspace = {};
        }
        this.workspace.directory_references = Array.isArray(workspace.directory_references)
          ? workspace.directory_references
          : [];
        this.workspace.mcp_bindings = Array.isArray(workspace.mcp_bindings)
          ? workspace.mcp_bindings
          : [];
        this.workspace.agent_mcp_access = Array.isArray(workspace.agent_mcp_access)
          ? workspace.agent_mcp_access
          : [];
        this.workspace.primary_directory_id =
          typeof workspace.primary_directory_id === 'string' ? workspace.primary_directory_id : '';
        this.workspace.project_path =
          typeof workspace.project_path === 'string' ? workspace.project_path : this.workspace.project_path || '';
        this.workspace.shared_data =
          workspace.shared_data && typeof workspace.shared_data === 'object'
            ? workspace.shared_data
            : this.workspace.shared_data || {};
      }

      const refs = Array.isArray(workspace.directory_references)
        ? workspace.directory_references.map(ref => ({
            id: ref.id,
            name: ref.name || '',
            path: ref.path || '',
            source: 'reference'
          }))
        : [];

      const attachmentDirs = Array.isArray(workspace.attachments)
        ? workspace.attachments
            .filter(attachment => attachment && attachment.type === 'directory')
            .map(attachment => ({
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
      [...refs, ...attachmentDirs].forEach(dir => {
        if (!dir || !dir.id) return;
        if (seenById.has(dir.id)) return;

        const normalizedPath = String(dir.path || '')
          .trim()
          .replace(/[\\/]+$/, '')
          .toLowerCase();
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
      this.syncProjectActionState();
      this.renderWorkspaceMCPBindings();
      this.renderWorkspaceSkillBindings();
      this.renderAgentGroups();
      this.refreshHomeAssistantQuickPrompts();
    } catch (error) {
      console.error('Failed to load directories:', error);
      this.directories = [];
      this.renderDirectories();
      this.syncProjectActionState();
      this.refreshHomeAssistantQuickPrompts();
    } finally {
      window.workspaceCommand?.refresh();
    }
  }

  /**
   * Render directories list
   */
  renderDirectories() {
    if (!this.elements.directoriesList) return;

    if ((!this.directories || this.directories.length === 0) && this.workspaceHasProject()) {
      this.elements.directoriesList.innerHTML = this.getProjectPathOnlyMarkup();
      return;
    }

    if (!this.directories || this.directories.length === 0) {
      this.elements.directoriesList.innerHTML = this.getUnlinkedProjectEmptyStateMarkup();
      return;
    }

    const primaryDirectoryId = this.getPrimaryDirectoryId();
    this.elements.directoriesList.innerHTML = this.directories
      .map(dir => {
        const name = dir.title || dir.name || dir.path || 'Unnamed Directory';
        const path = dir.path || '';
        const isTemplateProject = this.isProjectDirectory(dir);
        const sourceLabel = isTemplateProject
          ? 'Template project'
          : dir.source === 'reference'
            ? 'Linked folder'
            : 'Legacy attachment';
        const source = dir.source === 'attachment' ? 'attachment' : 'reference';
        const isPrimary = dir.id === primaryDirectoryId;
        const roleLabel = isTemplateProject || isPrimary ? 'Project Folder' : 'Reference';
        const roleClass = isTemplateProject || isPrimary ? 'is-primary' : 'is-reference';
        return `
        <div class="workspace-detail-item${isTemplateProject ? ' workspace-detail-project-directory' : ''}" data-directory-id="${dir.id}">
          ${
            !isTemplateProject
              ? `
          <button type="button" class="workspace-detail-item-change" onclick="event.stopPropagation(); window.workspaceDetail?.promptRelinkDirectory('${dir.id}', '${source}', this)" title="Change linked folder" aria-label="Change linked folder for ${this.escapeHtml(name)}">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M4,7H18.17L15.59,4.41L17,3L22,8L17,13L15.59,11.59L18.17,9H4V7M20,17H5.83L8.41,19.59L7,21L2,16L7,11L8.41,12.41L5.83,15H20V17Z"/>
            </svg>
          </button>
          `
              : ''
          }
          <button type="button" class="workspace-detail-item-run" onclick="event.stopPropagation(); window.workspaceDetail?.openDirectoryExplorer('${dir.id}', '${source}')" title="Explore directory" aria-label="Explore directory ${this.escapeHtml(name)}">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M15,12H16.5L15,13.5L16.42,14.92L20.34,11L16.42,7.08L15,8.5L16.5,10H15V12M19,19H5V5H13V3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V13H19V19Z"/>
            </svg>
          </button>
          ${
            !isTemplateProject
              ? `
          <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteDirectory('${dir.id}', '${source}')" title="Remove directory" aria-label="Remove directory ${this.escapeHtml(name)}">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
          `
              : ''
          }
          <div class="workspace-detail-item-content"
               role="button"
               tabindex="0"
               aria-label="Explore directory ${this.escapeHtml(name)}"
               onclick="window.workspaceDetail?.openDirectoryExplorer('${dir.id}', '${source}')"
               onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.openDirectoryExplorer('${dir.id}', '${source}'); }">
            <div class="workspace-detail-item-title">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="workspace-detail-item-title-icon">
                <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
              </svg>
              <span class="workspace-detail-item-title-text">${this.escapeHtml(name)}</span>
              <span class="workspace-detail-directory-role ${roleClass}">${roleLabel}</span>
            </div>
            <div class="workspace-detail-item-meta">${this.escapeHtml(path)}</div>
            <div class="workspace-detail-item-submeta">
              <span class="workspace-detail-directory-source">${this.escapeHtml(sourceLabel)}</span>
              ${
                !isPrimary && !isTemplateProject
                  ? `
                <button
                  type="button"
                  class="workspace-detail-directory-promote"
                  onclick="event.stopPropagation(); window.workspaceDetail?.setPrimaryDirectory('${dir.id}')"
                >
                  Make Project
                </button>
              `
                  : ''
              }
            </div>
          </div>
        </div>
      `;
      })
      .join('');
  }

  getStoredPrimaryDirectoryId() {
    return typeof this.workspace?.primary_directory_id === 'string'
      ? this.workspace.primary_directory_id.trim()
      : '';
  }

  getPrimaryDirectoryId() {
    const storedPrimaryDirectoryId = this.getStoredPrimaryDirectoryId();
    if (
      storedPrimaryDirectoryId &&
      this.directories.some(dir => dir.id === storedPrimaryDirectoryId)
    ) {
      return storedPrimaryDirectoryId;
    }

    return this.directories[0]?.id || '';
  }

  getProjectPathOnlyMarkup() {
    const projectPath = this.getWorkspaceProjectPath();
    const name = projectPath.split(/[\\/]/).filter(Boolean).pop() || 'Project Folder';
    return `
      <div class="workspace-detail-item workspace-detail-project-path-only">
        <div class="workspace-detail-item-content">
          <div class="workspace-detail-item-title">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="workspace-detail-item-title-icon">
              <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
            </svg>
            <span class="workspace-detail-item-title-text">${this.escapeHtml(name)}</span>
            <span class="workspace-detail-directory-role is-primary">Project Folder</span>
          </div>
          <div class="workspace-detail-item-meta">${this.escapeHtml(projectPath)}</div>
          <div class="workspace-detail-item-submeta">
            <span class="workspace-detail-directory-source">Template project</span>
          </div>
        </div>
      </div>
    `;
  }

  getUnlinkedProjectEmptyStateMarkup() {
    const createProjectAction = this.isGroupWorkspace()
      ? ''
      : `
        <button type="button" class="workspace-detail-empty-action" onclick="window.workspaceDetail?.showProjectTemplateModal(this)">
          Create Project
        </button>
      `;
    return `
      <div class="workspace-detail-link-empty">
        <div class="workspace-detail-link-empty-icon" aria-hidden="true">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H12V18H4V8H20V11H22V8C22,6.89 21.1,6 20,6H12L10,4M19,13V16H16V18H19V21H21V18H24V16H21V13H19Z"/>
          </svg>
        </div>
        <div class="workspace-detail-link-empty-eyebrow">Unlinked Workspace</div>
        <div class="workspace-detail-link-empty-title">No project folder linked yet.</div>
        <p class="workspace-detail-link-empty-copy">Attach the local project folder so this workspace can browse files in context.</p>
        <div class="workspace-detail-empty-actions">
          ${createProjectAction}
          <button type="button" class="workspace-detail-empty-action workspace-detail-empty-action-secondary" onclick="window.workspaceDetail?.showAddDirectoryModal(this)">
            Link Folder
          </button>
        </div>
      </div>
    `;
  }

  async deleteDirectory(directoryId, source = 'reference') {
    if (!directoryId) return;

    const confirmed = confirm(
      'Remove this directory from the workspace? The folder on disk will not be deleted.'
    );
    if (!confirmed) return;

    const normalizedSource = source === 'attachment' ? 'attachment' : 'reference';
    const endpoint =
      normalizedSource === 'attachment'
        ? `/api/workspaces/${encodeURIComponent(this.workspaceId)}/attachments/${encodeURIComponent(directoryId)}`
        : `/api/workspaces/${encodeURIComponent(this.workspaceId)}/directories/${encodeURIComponent(directoryId)}`;

    try {
      const deletedWasStoredPrimary = this.getStoredPrimaryDirectoryId() === directoryId;
      const response = await fetch(endpoint, { method: 'DELETE' });
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Failed to remove directory');
      }

      if (window.Toast) window.Toast.success('Directory removed');
      await this.loadDirectories();
      if (deletedWasStoredPrimary) {
        await this.setPrimaryDirectory(this.directories[0]?.id || '', { silent: true });
      }
    } catch (error) {
      console.error('Failed to remove directory:', error);
      if (window.Toast) window.Toast.error('Failed to remove directory');
    }
  }

  async setPrimaryDirectory(directoryId, { silent = false } = {}) {
    const nextDirectoryId = typeof directoryId === 'string' ? directoryId.trim() : '';
    if (this.getStoredPrimaryDirectoryId() === nextDirectoryId) {
      return;
    }

    try {
      await this.updateWorkspace({ primary_directory_id: nextDirectoryId });
      if (!this.workspace || typeof this.workspace !== 'object') {
        this.workspace = {};
      }
      this.workspace.primary_directory_id = nextDirectoryId;
      this.renderDirectories();
      if (!silent && window.Toast) {
        window.Toast.success(nextDirectoryId ? 'Project folder updated' : 'Project folder cleared');
      }
    } catch (error) {
      console.error('Failed to set primary directory:', error);
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to update project folder');
      }
    }
  }

  async promptRelinkDirectory(directoryId, source = 'reference', triggerButton = null) {
    if (!directoryId) return;

    const directory = this.directories.find(entry => entry.id === directoryId);
    if (!directory) {
      if (window.Toast) window.Toast.error('Directory not found');
      return;
    }

    const normalizedSource = source === 'attachment' ? 'attachment' : 'reference';
    const isPrimaryDirectory = this.getPrimaryDirectoryId() === directoryId;
    const button = triggerButton instanceof HTMLElement ? triggerButton : null;
    const originalButtonHTML = button ? button.innerHTML : '';
    const originalDisabled = button ? button.disabled : false;

    if (button) {
      button.disabled = true;
      button.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
    }

    try {
      const pickerResponse = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
          title: `Select a new folder for ${directory.name || directory.path || 'this workspace'}`
        })
      });

      const pickerResult = await pickerResponse.json().catch(() => ({}));
      if (!pickerResponse.ok || !pickerResult.success) {
        throw new Error(pickerResult.error || 'Failed to open folder picker');
      }

      if (!pickerResult.selected || !pickerResult.path) {
        return;
      }

      const nextPath = String(pickerResult.path || '').trim();
      const currentPath = String(directory.path || '').trim();
      if (nextPath && currentPath && nextPath === currentPath) {
        if (window.Toast) window.Toast.info('Workspace is already linked to that folder');
        return;
      }

      if (normalizedSource === 'attachment') {
        await this.updateLegacyDirectoryAttachment(directoryId, nextPath);
      } else {
        await this.updateDirectoryReference(directoryId, nextPath);
      }

      if (window.Toast) {
        window.Toast.success(
          isPrimaryDirectory ? 'Project folder updated' : 'Linked folder updated'
        );
      }
      await this.loadDirectories();
    } catch (error) {
      console.error('Failed to relink directory:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to change linked folder');
    } finally {
      if (button) {
        button.disabled = originalDisabled;
        button.innerHTML = originalButtonHTML;
      }
    }
  }

  async updateDirectoryReference(directoryId, nextPath) {
    const title = this.getDirectoryDisplayName(nextPath);
    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(this.workspaceId)}/directories/${encodeURIComponent(directoryId)}`,
      {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: title,
          path: nextPath
        })
      }
    );

    if (!response.ok) {
      const payload = await response.json().catch(() => ({}));
      throw new Error(payload.error || payload.message || 'Failed to update linked folder');
    }
  }

  async updateLegacyDirectoryAttachment(directoryId, nextPath) {
    const title = this.getDirectoryDisplayName(nextPath);
    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(this.workspaceId)}/attachments/${encodeURIComponent(directoryId)}`,
      {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title,
          body: nextPath
        })
      }
    );

    if (!response.ok) {
      const payload = await response.json().catch(() => ({}));
      throw new Error(payload.error || payload.message || 'Failed to update linked folder');
    }
  }

  getDirectoryDisplayName(path) {
    if (!path) return 'Project Folder';

    const normalizedPath = String(path).replace(/[\\/]+$/, '');
    const parts = normalizedPath.split(/[\\/]/).filter(Boolean);
    return parts[parts.length - 1] || normalizedPath || 'Project Folder';
  }

  openDirectoryExplorer(directoryId, source = 'reference') {
    return this.directoryExplorer.open(directoryId, source);
  }

  openWorkspaceFilesExplorer() {
    return this.directoryExplorer.open('__workspace_files__', 'owned');
  }

  /**
   * Show add directory modal - launches the folder picker
   */
  async showAddDirectoryModal(triggerButton = null) {
    const buttons = [];
    if (this.elements.addDirectoryBtn) {
      buttons.push(this.elements.addDirectoryBtn);
    }
    if (triggerButton && !buttons.includes(triggerButton)) {
      buttons.push(triggerButton);
    }
    if (buttons.length === 0) return;

    const buttonStates = buttons.map(button => ({
      button,
      disabled: button.disabled,
      innerHTML: button.innerHTML
    }));

    buttons.forEach(button => {
      button.disabled = true;
      if (button.classList.contains('workspace-detail-empty-action')) {
        button.textContent = 'Opening...';
      } else {
        button.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
      }
    });

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
      buttonStates.forEach(({ button, disabled, innerHTML }) => {
        button.disabled = disabled;
        button.innerHTML = innerHTML;
      });
    }
  }

  /**
   * Add a directory reference
   */
  async addDirectory(path) {
    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/attachments`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            type: 'directory',
            path: path,
            title: path.split('/').pop() || path
          })
        }
      );

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
      this.elements.schedulesList.innerHTML =
        '<div class="workspace-detail-loading">Loading schedules...</div>';
    }

    try {
      // Schedules are stored as tasks with schedule field
      const response = await fetch(
        `/api/orchestration/tasks?workspace_id=${encodeURIComponent(this.workspaceId)}`
      );
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
    } finally {
      window.workspaceCommand?.refresh();
    }
  }

  /**
   * Render schedules list
   */
  renderSchedules() {
    if (!this.elements.schedulesList) return;

    if (!this.schedules || this.schedules.length === 0) {
      this.elements.schedulesList.innerHTML =
        '<div class="workspace-detail-empty">No scheduled tasks yet.</div>';
      return;
    }

    this.elements.schedulesList.innerHTML = this.schedules
      .map(schedule => {
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
      })
      .join('');
  }

  /**
   * Show schedules modal
   */
  showSchedulesModal() {
    // Use the session manager's scheduled tasks panel if available
    if (
      window.sessionManager &&
      typeof window.sessionManager.openScheduledTasksPanel === 'function'
    ) {
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
    if (
      window.sessionManager &&
      typeof window.sessionManager.showScheduledTaskDetails === 'function'
    ) {
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
    if (
      window.workspaceRealtime &&
      typeof window.workspaceRealtime.subscribeToWorkspace === 'function'
    ) {
      window.workspaceRealtime.subscribeToWorkspace(this.workspaceId, event => {
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
        this.captureTaskActivity(event);
        this.loadTasks();
        break;
      case 'task.thinking':
      case 'task.tool_call':
      case 'task.tool_result':
      case 'task.heartbeat':
        // Phase events fire frequently — don't refetch tasks for each one.
        // Just patch the inline activity badge for the affected task.
        this.captureTaskActivity(event);
        break;
      case 'task.blocked': {
        this.loadTasks();
        break;
      }
      case 'task.resumed': {
        const payload = event?.data?.data || event?.data || {};
        const resumedTaskId = String(payload.task_id || event?.data?.task_id || '').trim();
        const activeTaskId = String(
          this.currentBlockedTask?.taskId || this.getBlockedTaskRouteId() || ''
        ).trim();
        this.loadTasks();
        if (activeTaskId && (!resumedTaskId || resumedTaskId === activeTaskId)) {
          this.closeTaskAssistPage({ replaceRoute: true });
        }
        break;
      }
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
      case 'project.created':
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
        if (
          typeof action === 'string' &&
          (action.startsWith('mcp_') || action.startsWith('agent_mcp_access_'))
        ) {
          this.loadWorkspace();
        }
        break;
      }
    }
  }

  // captureTaskActivity records the most recent task.* event for a task and
  // patches its inline activity badge in-place. The page deliberately does
  // NOT refetch tasks for thinking/tool/heartbeat events — those fire every
  // few seconds during a run and would thrash the table.
  captureTaskActivity(event) {
    const eventType = String(event?.type || '').trim();
    const payload = event?.data?.data || event?.data || {};
    const taskId = String(payload?.task_id || event?.task_id || '').trim();
    if (!taskId) return;

    if (eventType === 'task.heartbeat') {
      const prev = this._taskActivity.get(taskId);
      this._taskActivity.set(taskId, { at: Date.now(), label: prev?.label || '' });
    } else {
      const label = this.taskActivityLabelFor(eventType, payload);
      if (label === null) return;
      this._taskActivity.set(taskId, { at: Date.now(), label });
    }

    // Drop the entry once the task is no longer in_progress so a re-run
    // starts clean. We check live state to avoid stale labels persisting
    // after a completion event sneaks past the heartbeat goroutine.
    const task = this.tasks?.find?.(t => t?.id === taskId);
    if (task && String(task.status || '').trim().toLowerCase() !== 'in_progress') {
      this._taskActivity.delete(taskId);
    }

    this.patchTaskActivityBadge(taskId);
    this.ensureTaskActivityTick();
  }

  taskActivityLabelFor(eventType, payload) {
    const data = (payload && typeof payload === 'object') ? payload : {};
    switch (eventType) {
      case 'task.thinking': {
        const phase = String(data.phase || '').trim();
        if (phase === 'awaiting_llm') return 'Awaiting model';
        if (phase === 'llm_returned') {
          const calls = Number(data.tool_call_count || 0);
          return calls > 0 ? `Processing ${calls} tool` : 'Processing model';
        }
        if (phase === 'starting') return 'Analyzing';
        return 'Thinking';
      }
      case 'task.tool_call': {
        const tool = String(data.tool_name || '').trim();
        return tool ? `→ ${tool}` : 'Calling tool';
      }
      case 'task.tool_result': {
        const tool = String(data.tool_name || '').trim();
        const success = data.success !== false;
        if (tool) return success ? `${tool} ✓` : `${tool} ✗`;
        return 'Tool finished';
      }
      case 'task.started':
        return 'Started';
      case 'task.resumed':
        return 'Resumed';
      default:
        return null;
    }
  }

  formatTaskActivityAgo(timestampMs) {
    if (!timestampMs) return '';
    const elapsed = Math.max(0, Date.now() - timestampMs);
    const seconds = Math.floor(elapsed / 1000);
    if (seconds < 2) return 'just now';
    if (seconds < 60) return `${seconds}s ago`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes}m ago`;
    const hours = Math.floor(minutes / 60);
    return `${hours}h ago`;
  }

  patchTaskActivityBadge(taskId) {
    if (!taskId) return;
    const item = document.querySelector(`[data-task-id="${CSS.escape(taskId)}"]`);
    if (!item) return;
    const slot = item.querySelector('[data-task-activity-slot]');
    if (!slot) return;
    const activity = this._taskActivity.get(taskId);
    if (!activity) {
      slot.textContent = '';
      slot.hidden = true;
      return;
    }
    const ago = this.formatTaskActivityAgo(activity.at);
    const text = activity.label
      ? (ago ? `${activity.label} · ${ago}` : activity.label)
      : (ago ? `Active ${ago}` : 'Active');
    slot.textContent = text;
    slot.hidden = false;
  }

  ensureTaskActivityTick() {
    if (this._taskActivityTickHandle) return;
    this._taskActivityTickHandle = window.setInterval(() => {
      if (this._taskActivity.size === 0) {
        this.stopTaskActivityTick();
        return;
      }
      for (const taskId of this._taskActivity.keys()) {
        this.patchTaskActivityBadge(taskId);
      }
    }, 2000);
  }

  stopTaskActivityTick() {
    if (this._taskActivityTickHandle) {
      window.clearInterval(this._taskActivityTickHandle);
      this._taskActivityTickHandle = null;
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
    } else if (input.startsWith('/chat ') || input.startsWith('/c ') || input.startsWith('/ask ')) {
      const message = input.replace(/^\/(chat|c|ask)\s+/, '').trim();
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
          input_task_ids: Array.isArray(options.inputTaskIDs)
            ? options.inputTaskIDs.filter(Boolean)
            : undefined,
          parent_task_id: String(options.parentTaskID || '').trim() || undefined,
          subtask_index: Number.isFinite(Number(options.subtaskIndex))
            ? Number(options.subtaskIndex)
            : undefined
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
    if (
      window.taskModalController &&
      typeof window.taskModalController.openForCreate === 'function'
    ) {
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
  buildTaskHref(taskId) {
    const normalizedTaskId = String(taskId || '').trim();
    if (!normalizedTaskId || !this.workspaceId) return '';
    return `/workspaces/${encodeURIComponent(this.workspaceId)}/task/${encodeURIComponent(normalizedTaskId)}`;
  }

  openTask(taskId) {
    const href = this.buildTaskHref(taskId);
    if (href) {
      window.location.href = href;
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

    if (
      agentName &&
      window.sessionManager &&
      typeof window.sessionManager.createSessionWithAgentInFolder === 'function'
    ) {
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
      // Inside a workspace the chat runs as the workspace's entry agent (the
      // backend binds it when agent_name is omitted). Title the session after
      // the entry agent so the UI reflects who you're talking to instead of a
      // generic "Assistant".
      const entryAgentName = String(this.workspace?.entry_agent_name || '').trim();
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          folder_id: this.workspaceId,
          title: entryAgentName || 'Workspace chat'
        })
      });

      if (!response.ok) throw new Error('Failed to create session');

      const payload = await response.json();
      const session = payload?.session || payload;
      if (!session?.id) throw new Error('Invalid session response');
      if (window.Toast) window.Toast.success('Chat session created');
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

    const folderPath = this.getSelectedUploadFolderPath();
    for (const file of files) {
      const formData = new FormData();
      formData.append('file', file);
      formData.append('workspace_id', this.workspaceId);
      if (folderPath) formData.append('folder_path', folderPath);

      try {
        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`,
          {
            method: 'POST',
            body: formData
          }
        );

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

  getSelectedUploadFolderPath() {
    const candidates = [
      this.elements.fileFolderPath,
      document.getElementById('workspaceFileFolderPath'),
      document.getElementById('hubFileFolderPath')
    ];
    for (const candidate of candidates) {
      const value = String(candidate?.value || '').trim();
      if (value) return value;
    }
    return '';
  }

  /**
   * Show note modal using sessionManager
   */
  showNoteModal(note = null) {
    if (note) {
      // Edit existing note
      if (
        window.sessionManager &&
        typeof window.sessionManager.openNoteEditorModal === 'function'
      ) {
        window.sessionManager.openNoteEditorModal(note);
      }
    } else {
      // Create new note
      if (
        window.sessionManager &&
        typeof window.sessionManager.openNoteCreateModal === 'function'
      ) {
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
  async createNote(title, content, noteId = null, options = {}) {
    try {
      const url = noteId
        ? `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes/${encodeURIComponent(noteId)}`
        : `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`;
      const body = { name: title, content };
      const vaultReference = this.normalizeVaultReference(
        options.vaultReference || options.vault_reference
      );
      if (vaultReference) {
        body.vault_reference = vaultReference;
      }

      const response = await fetch(url, {
        method: noteId ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
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
    const hasNotes = Array.isArray(this.notes) && this.notes.length > 0;

    if (this.elements.notesActions) {
      this.elements.notesActions.hidden = !hasNotes;
    }

    this.updateNotesSelectionUI(Boolean(isBusy));
  }

  /**
   * Sync the selection-aware controls (Select all toggle, Copy (N),
   * Delete (N)) and the per-card highlight/checkbox state to the current
   * `selectedNoteIds` set.
   */
  updateNotesSelectionUI(isBusy = false) {
    const notes = Array.isArray(this.notes) ? this.notes : [];
    const total = notes.length;
    const selectedCount = notes.reduce(
      (count, note) => (this.selectedNoteIds.has(String(note.id)) ? count + 1 : count),
      0
    );
    const allSelected = total > 0 && selectedCount === total;

    if (this.elements.selectAllNotesBtn) {
      // Icon-only toggle: keep the stacked-checkbox SVG intact and reflect
      // the on/off state via aria-pressed plus the tooltip/aria-label.
      const btn = this.elements.selectAllNotesBtn;
      btn.disabled = isBusy || total === 0;
      btn.setAttribute('aria-pressed', allSelected ? 'true' : 'false');
      btn.setAttribute('aria-label', allSelected ? 'Deselect all notes' : 'Select all notes');
      btn.title = total === 0
        ? 'No notes to select'
        : allSelected
          ? 'Clear selection'
          : 'Select every note';
    }

    if (this.elements.copyAllNotesBtn) {
      // Icon-only button: keep the copy SVG intact (don't touch textContent)
      // and surface the selected count through the tooltip + aria-label.
      const btn = this.elements.copyAllNotesBtn;
      btn.disabled = isBusy || selectedCount === 0;
      const label = selectedCount > 0
        ? `Copy ${selectedCount} selected note${selectedCount === 1 ? '' : 's'}`
        : 'Copy selected notes';
      btn.title = selectedCount === 0 ? 'Select notes to copy' : label;
      btn.setAttribute('aria-label', label);
    }

    if (this.elements.deleteSelectedNotesBtn) {
      // Icon-only button: keep the trash SVG intact (don't touch textContent)
      // and surface the selected count through the tooltip + aria-label.
      const btn = this.elements.deleteSelectedNotesBtn;
      btn.disabled = isBusy || selectedCount === 0;
      const label = selectedCount > 0
        ? `Delete ${selectedCount} selected note${selectedCount === 1 ? '' : 's'}`
        : 'Delete selected notes';
      btn.title = selectedCount === 0 ? 'Select notes to delete' : label;
      btn.setAttribute('aria-label', label);
    }

    // Keep card highlight + checkbox state in sync without a full re-render.
    if (this.elements.notesList) {
      this.elements.notesList
        .querySelectorAll('.workspace-detail-note-item[data-note-id]')
        .forEach(item => {
          const id = String(item.getAttribute('data-note-id'));
          const selected = this.selectedNoteIds.has(id);
          item.classList.toggle('is-selected', selected);
          const checkbox = item.querySelector('.workspace-detail-note-select');
          if (checkbox) checkbox.checked = selected;
        });
    }
  }

  toggleNoteSelection(noteId, checked) {
    const id = String(noteId || '').trim();
    if (!id) return;
    if (checked) {
      this.selectedNoteIds.add(id);
    } else {
      this.selectedNoteIds.delete(id);
    }
    this.updateNotesSelectionUI();
  }

  toggleSelectAllNotes() {
    const notes = Array.isArray(this.notes) ? this.notes : [];
    if (notes.length === 0) return;
    const allSelected = notes.every(note => this.selectedNoteIds.has(String(note.id)));
    if (allSelected) {
      this.selectedNoteIds.clear();
    } else {
      notes.forEach(note => this.selectedNoteIds.add(String(note.id)));
    }
    this.updateNotesSelectionUI();
  }

  getSelectedNotes() {
    const notes = Array.isArray(this.notes) ? this.notes : [];
    return notes.filter(note => this.selectedNoteIds.has(String(note.id)));
  }

  async copySelectedNotesToClipboard() {
    const selected = this.getSelectedNotes();
    if (selected.length === 0) {
      if (window.Toast) window.Toast.error('No notes selected');
      return;
    }

    this.updateCopyNotesButtonState(true);
    try {
      await this.writeClipboardText(await this.buildAllNotesText(selected));
      const message = `Copied ${selected.length} note${selected.length === 1 ? '' : 's'} to clipboard`;
      if (typeof window.notifyToast === 'function') {
        window.notifyToast(message, 'success');
      } else if (window.Toast) {
        window.Toast.success(message);
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

  async deleteSelectedNotes() {
    const selected = this.getSelectedNotes();
    if (selected.length === 0) {
      if (window.Toast) window.Toast.error('No notes selected');
      return;
    }

    const confirmed = window.confirm(
      `Delete ${selected.length} note${selected.length === 1 ? '' : 's'}? This cannot be undone.`
    );
    if (!confirmed) return;

    this.updateCopyNotesButtonState(true);
    let failures = 0;
    for (const note of selected) {
      try {
        const response = await fetch(`/api/notes/${encodeURIComponent(note.id)}`, {
          method: 'DELETE'
        });
        if (response.ok) {
          this.selectedNoteIds.delete(String(note.id));
        } else {
          failures++;
        }
      } catch (error) {
        console.error('Failed to delete note:', error);
        failures++;
      }
    }

    const deleted = selected.length - failures;
    if (deleted > 0) {
      const message = `Deleted ${deleted} note${deleted === 1 ? '' : 's'}`;
      if (typeof window.notifyToast === 'function') {
        window.notifyToast(message, 'success');
      } else if (window.Toast) {
        window.Toast.success(message);
      }
    }
    if (failures > 0 && window.Toast) {
      window.Toast.error(`Failed to delete ${failures} note${failures === 1 ? '' : 's'}`);
    }

    await this.loadNotes();
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

  async buildAllNotesText(noteList = this.notes) {
    const list = Array.isArray(noteList) ? noteList : [];
    const sections = await Promise.all(
      list.map(async (note, index) => {
        const noteId = String(note?.id || '').trim();
        const title =
          String(note?.name || note?.title || `Note ${index + 1}`).trim() || `Note ${index + 1}`;
        let content = '';

        if (noteId) {
          try {
            const response = await fetch(`/api/notes/${encodeURIComponent(noteId)}`);
            if (response.ok) {
              const detail = await response.json();
              content = String(detail?.content || detail?.preview || '').trim();
            }
          } catch (error) {
            console.error('Failed to load note content:', error);
          }
        }

        if (!content) {
          content = String(note?.content || note?.preview || '').trim();
        }

        return `# ${title}\n${content || '(empty note)'}`;
      })
    );

    return sections.join('\n\n---\n\n');
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

  escapeAttribute(text) {
    return this.escapeHtml(text).replace(/"/g, '&quot;').replace(/'/g, '&#39;');
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
