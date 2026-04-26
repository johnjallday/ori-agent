console.log('[workspace-hub.js] FILE LOADED');
/**
 * Workspace Hub - Main Coordinator
 * Orchestrates workspace hub sub-modules for task, session, note, and file management.
 *
 * Dependencies (load in this order):
 * - workspace-hub-utils.js
 * - workspace-hub-state.js
 * - workspace-hub-modals.js
 * - workspace-hub-selection.js
 * - workspace-hub-tasks.js
 * - workspace-hub-sessions.js
 * - workspace-hub-notes.js
 * - workspace-hub-files.js
 * - workspace-hub-smart-input.js
 * - workspace-hub.js (this file)
 *
 * @module workspace-hub
 */
(function() {
  'use strict';

  const hubEl = document.getElementById('workspaceHub');
  const OVERVIEW_EXPANDED_STORAGE_KEY = 'oriWorkspaceHubOverviewExpanded';
  const LAUNCHER_TAB_STORAGE_KEY = 'oriWorkspaceHubLauncherTab';
  console.log('[workspace-hub] hubEl exists:', !!hubEl);
  if (!hubEl) return;

  // Initialize DOM element references
  const elements = {
    headerTitle: document.querySelector('.workspace-hub-header .hub-title-text'),
    workspaceSelect: document.getElementById('hubWorkspaceSelect'),
    workspaceBrowseBtn: document.getElementById('hubWorkspaceBrowseBtn'),
    workspaceStatus: document.getElementById('hubWorkspaceStatus'),
    workspaceUpdated: document.getElementById('hubWorkspaceUpdated'),
    workspaceAgents: document.getElementById('hubWorkspaceAgents'),
    workspaceDescription: document.getElementById('hubWorkspaceDescription'),
    workspaceCanvasBtn: document.getElementById('hubOpenCanvasBtn'),
    workspaceMoveBtn: document.getElementById('hubWorkspaceMoveBtn'),
    workspaceMoveModal: document.getElementById('hubWorkspaceMoveModal'),
    workspaceParentSelect: document.getElementById('hubWorkspaceParentSelect'),
    workspaceMoveSaveBtn: document.getElementById('hubWorkspaceMoveSaveBtn'),
    viewListBtn: document.getElementById('hubViewList'),
    viewBoardBtn: document.getElementById('hubViewBoard'),
    boardContainer: document.getElementById('hubBoardContainer'),
    boardColumns: document.getElementById('hubBoardColumns'),
    boardScroll: document.getElementById('hubBoardScroll'),
    boardEmpty: document.getElementById('hubBoardEmpty'),
    boardLoading: document.getElementById('hubBoardLoading'),
    boardTaskCount: document.getElementById('hubBoardTaskCount'),
    boardEditColumnsBtn: document.getElementById('hubBoardEditColumnsBtn'),
    boardRefreshBtn: document.getElementById('hubBoardRefreshBtn'),
    boardSetupBtn: document.getElementById('hubBoardSetupBtn'),
    boardColumnsModal: document.getElementById('hubBoardColumnsModal'),
    boardColumnsList: document.getElementById('hubColumnsList'),
    boardAddColumnBtn: document.getElementById('hubAddColumnBtn'),
    boardSaveColumnsBtn: document.getElementById('hubSaveColumnsBtn'),

    taskDetailsModal: document.getElementById('hubTaskDetailsModal'),
    taskDetailsTitle: document.getElementById('hubTaskDetailsTitle'),
    taskDetailsDescription: document.getElementById('hubTaskDetailsDescription'),
    taskDetailsHeaderId: document.getElementById('hubTaskDetailsHeaderId'),
    taskDetailsId: document.getElementById('hubTaskDetailsId'),
    taskDetailsStatus: document.getElementById('hubTaskDetailsStatus'),
    taskDetailsAssignedTo: document.getElementById('hubTaskDetailsAssignedTo'),
    taskDetailsCreated: document.getElementById('hubTaskDetailsCreated'),
    taskDetailsUpdated: document.getElementById('hubTaskDetailsUpdated'),
    taskDetailsText: document.getElementById('hubTaskDetailsText'),
    taskDetailsResultSection: document.getElementById('hubTaskDetailsResultSection'),
    taskDetailsResult: document.getElementById('hubTaskDetailsResult'),
    taskDetailsResultBadge: document.getElementById('hubTaskDetailsResultBadge'),
    taskDetailsErrorSection: document.getElementById('hubTaskDetailsErrorSection'),
    taskDetailsError: document.getElementById('hubTaskDetailsError'),
    taskDetailsCopyIdBtn: document.getElementById('hubTaskDetailsCopyIdBtn'),
    taskDetailsRunBtn: document.getElementById('hubTaskDetailsRunBtn'),
    taskDetailsEditBtn: document.getElementById('hubTaskDetailsEditBtn'),
    addTaskBtn: document.getElementById('hubAddTaskBtn'),
    importWorkflowBtn: document.getElementById('hubImportWorkflowBtn'),
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
    launcherWorkspaceRootPath: document.getElementById('launcherWorkspaceRootPath'),
    launcherWorkspaceRootSummary: document.getElementById('launcherWorkspaceRootSummary'),
    launcherWorkspaceRootMeta: document.getElementById('launcherWorkspaceRootMeta'),
    launcherWorkspaceRootBadge: document.getElementById('launcherWorkspaceRootBadge'),
    launcherWorkspaceRootEditBtn: document.getElementById('launcherWorkspaceRootEditBtn'),
    launcherWorkspaceRootEditor: document.getElementById('launcherWorkspaceRootEditor'),
    launcherWorkspaceRootInput: document.getElementById('launcherWorkspaceRootInput'),
    launcherWorkspaceRootBrowseBtn: document.getElementById('launcherWorkspaceRootBrowseBtn'),
    launcherWorkspaceRootSaveBtn: document.getElementById('launcherWorkspaceRootSaveBtn'),
    launcherWorkspaceRootResetBtn: document.getElementById('launcherWorkspaceRootResetBtn'),
    launcherWorkspaceRootCancelBtn: document.getElementById('launcherWorkspaceRootCancelBtn'),
    launcherRefreshBtn: document.getElementById('launcherRefreshBtn'),
    launcherSelectModeBtn: document.getElementById('launcherSelectModeBtn'),
    launcherGroupSelectedBtn: document.getElementById('launcherGroupSelectedBtn'),
    launcherDeleteSelectedBtn: document.getElementById('launcherDeleteSelectedBtn'),
    launcherCancelSelectionBtn: document.getElementById('launcherCancelSelectionBtn'),
    launcherSelectionCount: document.getElementById('launcherSelectionCount'),
    launcherGroupModal: document.getElementById('launcherGroupModal'),
    launcherGroupNameInput: document.getElementById('launcherGroupNameInput'),
    launcherGroupDescriptionInput: document.getElementById('launcherGroupDescriptionInput'),
    launcherCreateGroupBtn: document.getElementById('launcherCreateGroupBtn'),
    launcherSelectionBar: document.getElementById('launcherSelectionBar'),
    launcherRootDropZone: document.getElementById('launcherRootDropZone'),
    launcherDeleteGroupModal: document.getElementById('launcherDeleteGroupModal'),
    launcherDeleteGroupName: document.getElementById('launcherDeleteGroupName'),
    launcherDeleteGroupCount: document.getElementById('launcherDeleteGroupCount'),
    launcherDeleteGroupOnlyBtn: document.getElementById('launcherDeleteGroupOnlyBtn'),
    launcherDeleteGroupAllBtn: document.getElementById('launcherDeleteGroupAllBtn'),
    launcherTabWorkspaces: document.getElementById('launcherTabWorkspaces'),
    launcherTabSummary: document.getElementById('launcherTabSummary'),
    launcherWorkspacesPanel: document.getElementById('launcherWorkspacesPanel'),
    launcherSummaryPanel: document.getElementById('launcherSummaryPanel'),
    launcherTaskOverview: document.getElementById('launcherTaskOverview'),
    launcherOverviewUpdatedAt: document.getElementById('launcherOverviewUpdatedAt'),
    launcherOverviewOpen: document.getElementById('launcherOverviewOpen'),
    launcherOverviewPending: document.getElementById('launcherOverviewPending'),
    launcherOverviewInProgress: document.getElementById('launcherOverviewInProgress'),
    launcherOverviewAttention: document.getElementById('launcherOverviewAttention'),
    launcherOverviewScheduled: document.getElementById('launcherOverviewScheduled'),
    launcherOverviewOpenSessions: document.getElementById('launcherOverviewOpenSessions'),
    launcherOverviewByWorkspace: document.getElementById('launcherOverviewByWorkspace'),
    launcherOverviewTopTasks: document.getElementById('launcherOverviewTopTasks'),
    launcherOverviewScheduledTasks: document.getElementById('launcherOverviewScheduledTasks'),
    launcherOverviewOpenSessionsList: document.getElementById('launcherOverviewOpenSessionsList'),
    launcherOverviewDetails: document.getElementById('launcherOverviewDetails'),
    launcherOverviewToggleBtn: document.getElementById('launcherOverviewToggleBtn'),
    loadingOverlay: document.getElementById('workspaceHubLoading'),
    sessionsList: document.getElementById('hubSessionsList'),
    newSessionBtn: document.getElementById('hubNewSessionBtn'),
    notesList: document.getElementById('hubNotesList'),
    copyNotesBtn: document.getElementById('hubCopyNotesBtn'),
    newNoteBtn: document.getElementById('hubNewNoteBtn'),
    filesList: document.getElementById('hubFilesList'),
    addFileBtn: document.getElementById('hubAddFileBtn'),
    directoriesList: document.getElementById('hubDirectoriesList'),
    addDirectoryPanelBtn: document.getElementById('hubAddDirectoryPanelBtn'),
    smartInputCard: document.getElementById('hubSmartInput'),
    smartInputField: document.getElementById('hubSmartInputField'),
    smartInputSubmit: document.getElementById('hubSmartInputSubmit'),
    smartInputStatus: document.getElementById('hubSmartInputStatus'),
    smartInputPrompt: document.getElementById('hubSmartInputPrompt'),
    smartInputPromptHint: document.getElementById('hubSmartInputPromptHint'),
    smartInputPromptTask: document.getElementById('hubSmartInputPromptTask'),
    smartInputPromptChat: document.getElementById('hubSmartInputPromptChat'),
    smartInputPromptCancel: document.getElementById('hubSmartInputPromptCancel'),
    smartInputAttachBtn: document.getElementById('hubSmartInputAttachBtn'),
    smartInputProgressModal: document.getElementById('hubSmartInputProgressModal'),
    smartInputProgressHeadline: document.getElementById('hubSmartInputProgressHeadline'),
    smartInputProgressMessage: document.getElementById('hubSmartInputProgressMessage'),
    smartInputProgressSteps: document.getElementById('hubSmartInputProgressSteps'),
    smartInputCancelBtn: document.getElementById('hubSmartInputCancelBtn'),
    // Selection mode elements
    tasksPanel: document.getElementById('hubTasksPanel'),
    sessionsPanel: document.getElementById('hubSessionsPanel'),
    notesPanel: document.getElementById('hubNotesPanel'),
    filesPanel: document.getElementById('hubFilesPanel'),
    directoriesPanel: document.getElementById('hubDirectoriesPanel'),
    selectTasksBtn: document.getElementById('hubSelectTasksBtn'),
    bulkDeleteTasksBtn: document.getElementById('hubBulkDeleteTasksBtn'),
    selectSessionsBtn: document.getElementById('hubSelectSessionsBtn'),
    bulkDeleteSessionsBtn: document.getElementById('hubBulkDeleteSessionsBtn'),
    selectNotesBtn: document.getElementById('hubSelectNotesBtn'),
    bulkDeleteNotesBtn: document.getElementById('hubBulkDeleteNotesBtn'),
    selectFilesBtn: document.getElementById('hubSelectFilesBtn'),
    bulkTrashFilesBtn: document.getElementById('hubBulkTrashFilesBtn'),
    // Delete confirmation modal
    deleteConfirmModal: document.getElementById('hubDeleteConfirmModal'),
    deleteConfirmTitle: document.getElementById('hubDeleteConfirmTitle'),
    deleteConfirmBody: document.getElementById('hubDeleteConfirmBody'),
    deleteConfirmBtn: document.getElementById('hubDeleteConfirmBtn'),
    parentDeleteModal: document.getElementById('hubParentDeleteModal'),
    parentDeleteTitle: document.getElementById('hubParentDeleteTitle'),
    parentDeleteBody: document.getElementById('hubParentDeleteBody'),
    parentDeleteUngroupBtn: document.getElementById('hubParentDeleteUngroupBtn'),
    parentDeleteAllBtn: document.getElementById('hubParentDeleteAllBtn'),
    executionConfirmModal: document.getElementById('hubExecutionConfirmModal'),
    executionConfirmEyebrow: document.getElementById('hubExecutionConfirmEyebrow'),
    executionConfirmTitle: document.getElementById('hubExecutionConfirmTitle'),
    executionConfirmMessage: document.getElementById('hubExecutionConfirmMessage'),
    executionConfirmMeta: document.getElementById('hubExecutionConfirmMeta'),
    executionConfirmDetails: document.getElementById('hubExecutionConfirmDetails'),
    executionConfirmCancelBtn: document.getElementById('hubExecutionConfirmCancelBtn'),
    executionConfirmConfirmBtn: document.getElementById('hubExecutionConfirmConfirmBtn'),
    // Add file modal
    addFileModal: document.getElementById('hubAddFileModal'),
    fileDropZone: document.getElementById('hubFileDropZone'),
    fileInput: document.getElementById('hubFileInput'),
    selectedFilesPreview: document.getElementById('hubSelectedFilesPreview'),
    selectedFilesList: document.getElementById('hubSelectedFilesList'),
    fileTitle: document.getElementById('hubFileTitle'),
    fileNotes: document.getElementById('hubFileNotes'),
    fileUploadProgress: document.getElementById('hubFileUploadProgress'),
    fileUploadPercent: document.getElementById('hubFileUploadPercent'),
    fileUploadProgressBar: document.getElementById('hubFileUploadProgressBar'),
    addFileSubmitBtn: document.getElementById('hubAddFileSubmitBtn')
  };

  // Initialize state module with elements
  window.WorkspaceHubState.initElements(elements);

  const { formatDate, flattenWorkspaces, collectWorkspaceDescendantIds } = window.WorkspaceHubUtils;

  let pendingDeleteGroupId = null;
  let launcherOverviewRequestSeq = 0;
  let launcherOverviewRefreshTimer = null;
  let launcherTaskBadgeByWorkspace = new Map();
  let launcherActiveTab = 'workspaces';
  let launcherWorkspaceRootState = null;
  let launcherWorkspaceRootEditorOpen = false;

  /**
   * Schedule a workspace tasks refresh (debounced)
   * @param {number} delayMs - Delay in milliseconds
   */
  function scheduleWorkspaceTasksRefresh(delayMs = 500) {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedId) return;

    let timer = window.WorkspaceHubState.getRealtimeTimer();
    if (timer) {
      clearTimeout(timer);
    }
    timer = setTimeout(() => {
      window.WorkspaceHubState.setRealtimeTimer(null);
      if (state.selectedId) {
        window.WorkspaceHubTasks.loadTasks(state.selectedId);
      }
    }, delayMs);
    window.WorkspaceHubState.setRealtimeTimer(timer);
  }

  function normalizeOverviewStatus(status) {
    const value = String(status || 'pending').trim().toLowerCase();
    if (value === 'assigned') return 'pending';
    return value;
  }

  function statusPriorityForOverview(status) {
    switch (status) {
      case 'failed':
      case 'timeout':
        return 0;
      case 'in_progress':
        return 1;
      case 'pending':
        return 2;
      default:
        return 3;
    }
  }

  function formatOverviewStatus(status) {
    return String(status || 'pending').replace(/_/g, ' ');
  }

  function formatOverviewSchedule(task) {
    if (!task || typeof task !== 'object') return 'Scheduled';
    if (task.schedule_summary) return String(task.schedule_summary);
    if (task.schedule_expression) return String(task.schedule_expression);

    if (task.schedule && typeof task.schedule === 'object') {
      if (task.schedule.description) return String(task.schedule.description);
      if (task.schedule.expression) return String(task.schedule.expression);
      if (task.schedule.cron) return String(task.schedule.cron);

      const parts = [];
      if (task.schedule.type) parts.push(String(task.schedule.type).replace(/_/g, ' '));
      if (task.schedule.value) parts.push(String(task.schedule.value));
      if (task.schedule.time) parts.push(`at ${task.schedule.time}`);
      if (task.schedule.day_of_week) parts.push(`on ${task.schedule.day_of_week}`);
      if (parts.length > 0) return parts.join(' | ');
    }

    if (task.schedule_type) return String(task.schedule_type).replace(/_/g, ' ');
    return 'Scheduled';
  }

  function truncateText(value, limit = 90) {
    const text = String(value || '').trim();
    if (!text) return '';
    if (text.length <= limit) return text;
    return `${text.slice(0, Math.max(0, limit - 1)).trim()}...`;
  }

  function formatOverviewTimestamp(date = new Date()) {
    try {
      return date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
    } catch (err) {
      return '--';
    }
  }

  function updateLauncherOverviewMetrics(metrics) {
    if (elements.launcherOverviewOpen) elements.launcherOverviewOpen.textContent = String(metrics.open || 0);
    if (elements.launcherOverviewPending) elements.launcherOverviewPending.textContent = String(metrics.pending || 0);
    if (elements.launcherOverviewInProgress) elements.launcherOverviewInProgress.textContent = String(metrics.inProgress || 0);
    if (elements.launcherOverviewAttention) elements.launcherOverviewAttention.textContent = String(metrics.needsAttention || 0);
    if (elements.launcherOverviewScheduled) elements.launcherOverviewScheduled.textContent = String(metrics.scheduled || 0);
    if (elements.launcherOverviewOpenSessions) elements.launcherOverviewOpenSessions.textContent = String(metrics.openSessions || 0);
  }

  function renderLauncherTaskBadge(workspaceID) {
    if (!workspaceID) return '';
    const counts = launcherTaskBadgeByWorkspace.get(workspaceID);
    if (!counts || counts.open <= 0) return '';

    const titleParts = [`${counts.open} open task${counts.open === 1 ? '' : 's'}`];
    if (counts.inProgress > 0) titleParts.push(`${counts.inProgress} in progress`);
    if (counts.needsAttention > 0) titleParts.push(`${counts.needsAttention} need attention`);
    const title = titleParts.join(' | ');
    const badgeClass = counts.needsAttention > 0
      ? 'launcher-task-badge is-attention'
      : 'launcher-task-badge';

    return `<span class="${badgeClass}" title="${escapeHtml(title)}" aria-label="${escapeHtml(title)}">${escapeHtml(String(counts.open))}</span>`;
  }

  function normalizeWorkspaceFolderPath(value) {
    return String(value || '').trim().replace(/[\\/]+$/, '');
  }

  function collectWorkspaceLinkedDirectories(workspace) {
    const linkedDirectories = [];
    const seenPaths = new Set();

    function addDirectory(pathValue, fallbackName) {
      const normalizedPath = normalizeWorkspaceFolderPath(pathValue);
      const normalizedName = String(fallbackName || '').trim();
      const dedupeKey = normalizedPath
        ? normalizedPath.toLowerCase()
        : normalizedName.toLowerCase();

      if (!dedupeKey || seenPaths.has(dedupeKey)) return;

      seenPaths.add(dedupeKey);
      linkedDirectories.push({
        path: normalizedPath,
        name: normalizedName
      });
    }

    if (Array.isArray(workspace?.directory_references)) {
      workspace.directory_references.forEach((ref) => {
        if (!ref || typeof ref !== 'object') return;
        addDirectory(ref.path, ref.name);
      });
    }

    if (Array.isArray(workspace?.attachments)) {
      workspace.attachments.forEach((attachment) => {
        if (!attachment || typeof attachment !== 'object') return;
        if (String(attachment.type || '').trim().toLowerCase() !== 'directory') return;
        addDirectory(attachment.path || attachment.body, attachment.title || attachment.name);
      });
    }

    const importedFolderPath = workspace?.shared_data?.folder_import?.path;
    if (typeof importedFolderPath === 'string' && importedFolderPath.trim()) {
      addDirectory(importedFolderPath, 'Imported folder');
    }

    return linkedDirectories;
  }

  function getWorkspaceFolderDisplay(workspace) {
    const linkedDirectories = collectWorkspaceLinkedDirectories(workspace);
    if (linkedDirectories.length === 0) {
      return {
        linked: false,
        badgeLabel: 'No folder linked',
        badgeClass: 'is-unlinked',
        detail: 'No local folder attached.',
        detailTitle: 'This workspace is not linked to a local folder.',
        ariaLabel: 'no linked folder'
      };
    }

    const directoryCount = linkedDirectories.length;
    const primaryPath = linkedDirectories[0].path || linkedDirectories[0].name || 'Linked folder';
    const extraCount = directoryCount - 1;
    const allPaths = linkedDirectories
      .map((directory) => directory.path || directory.name)
      .filter(Boolean);

    return {
      linked: true,
      badgeLabel: directoryCount === 1 ? 'Linked folder' : `${directoryCount} folders linked`,
      badgeClass: 'is-linked',
      detail: extraCount > 0 ? `${primaryPath} (+${extraCount} more)` : primaryPath,
      detailTitle: allPaths.join(' | ') || primaryPath,
      ariaLabel: directoryCount === 1 ? 'linked folder' : `${directoryCount} linked folders`
    };
  }

  function setLauncherWorkspaceRootDisplay(data) {
    if (!elements.launcherWorkspaceRootPath || !elements.launcherWorkspaceRootSummary || !elements.launcherWorkspaceRootMeta || !elements.launcherWorkspaceRootBadge) {
      return;
    }

    elements.launcherWorkspaceRootPath.textContent = data.path;
    elements.launcherWorkspaceRootPath.title = data.path;
    elements.launcherWorkspaceRootSummary.textContent = data.summary;
    elements.launcherWorkspaceRootMeta.textContent = data.meta;
    elements.launcherWorkspaceRootBadge.textContent = data.badgeLabel;
    elements.launcherWorkspaceRootBadge.className = `launcher-workspace-root-badge ${data.badgeClass || 'is-loading'}`;
  }

  function getLauncherWorkspaceRootDraftValue() {
    const configuredRoot = String(launcherWorkspaceRootState?.workspace_root || '').trim();
    const effectiveRoot = String(launcherWorkspaceRootState?.effective_workspace_root || '').trim();
    return configuredRoot || effectiveRoot;
  }

  function setLauncherWorkspaceRootButtonLoading(button, isLoading, loadingLabel) {
    if (!button) return;

    if (isLoading) {
      button.dataset.originalLabel = button.textContent || '';
      button.disabled = true;
      button.textContent = loadingLabel;
      return;
    }

    button.disabled = false;
    if (button.dataset.originalLabel) {
      button.textContent = button.dataset.originalLabel;
    }
  }

  function syncLauncherWorkspaceRootEditorControls() {
    const hasCustomRoot = Boolean(String(launcherWorkspaceRootState?.workspace_root || '').trim());
    const defaultRoot = String(launcherWorkspaceRootState?.default_workspace_root || '').trim();

    if (elements.launcherWorkspaceRootEditBtn) {
      elements.launcherWorkspaceRootEditBtn.textContent = launcherWorkspaceRootEditorOpen ? 'Close' : 'Edit';
      elements.launcherWorkspaceRootEditBtn.setAttribute('aria-expanded', launcherWorkspaceRootEditorOpen ? 'true' : 'false');
    }

    if (elements.launcherWorkspaceRootInput) {
      elements.launcherWorkspaceRootInput.placeholder = defaultRoot || '/absolute/path/to/workspaces';
    }

    if (elements.launcherWorkspaceRootResetBtn) {
      elements.launcherWorkspaceRootResetBtn.disabled = !hasCustomRoot;
      elements.launcherWorkspaceRootResetBtn.textContent = hasCustomRoot ? 'Clear Custom' : 'Using Default';
    }
  }

  function setLauncherWorkspaceRootEditorOpen(isOpen, options = {}) {
    launcherWorkspaceRootEditorOpen = !!isOpen;

    if (elements.launcherWorkspaceRootEditor) {
      elements.launcherWorkspaceRootEditor.hidden = !launcherWorkspaceRootEditorOpen;
    }

    if (launcherWorkspaceRootEditorOpen && elements.launcherWorkspaceRootInput && options.preserveDraft !== true) {
      elements.launcherWorkspaceRootInput.value = getLauncherWorkspaceRootDraftValue();
    }

    syncLauncherWorkspaceRootEditorControls();

    if (launcherWorkspaceRootEditorOpen && options.focusInput && elements.launcherWorkspaceRootInput) {
      elements.launcherWorkspaceRootInput.focus();
      elements.launcherWorkspaceRootInput.select();
    }
  }

  function renderLauncherWorkspaceRootLoading() {
    launcherWorkspaceRootState = null;
    setLauncherWorkspaceRootDisplay({
      path: 'Fetching current workspace location...',
      summary: 'Loading workspace directory...',
      meta: 'New workspaces will be created here.',
      badgeLabel: 'Loading',
      badgeClass: 'is-loading'
    });
    if (!launcherWorkspaceRootEditorOpen && elements.launcherWorkspaceRootInput) {
      elements.launcherWorkspaceRootInput.value = '';
    }
    syncLauncherWorkspaceRootEditorControls();
  }

  function renderLauncherWorkspaceRootError() {
    launcherWorkspaceRootState = null;
    setLauncherWorkspaceRootDisplay({
      path: 'Workspace directory unavailable',
      summary: 'Unable to load the default workspace directory right now.',
      meta: 'Open Settings to confirm the configured workspace location.',
      badgeLabel: 'Unavailable',
      badgeClass: 'is-error'
    });
    syncLauncherWorkspaceRootEditorControls();
  }

  function applyLauncherWorkspaceRootState(workspaceRootState) {
    launcherWorkspaceRootState = workspaceRootState || {};
    const source = String(workspaceRootState?.source || 'default').trim().toLowerCase();
    const effectiveRoot = String(workspaceRootState?.effective_workspace_root || '').trim();
    const fallbackRoot = String(workspaceRootState?.default_workspace_root || '').trim();
    const visiblePath = effectiveRoot || fallbackRoot || 'Workspace directory unavailable';

    let badgeLabel = 'Built-in default';
    let badgeClass = 'is-default';
    let summary = 'Using the built-in workspace location for new workspaces.';
    let meta = 'This is where new workspaces will be created unless you override it.';

    if (source === 'settings') {
      badgeLabel = 'Custom';
      badgeClass = 'is-settings';
      summary = 'A custom workspace directory is active.';
      meta = 'New workspaces will be created in the location saved in Settings.';
    } else if (source === 'environment') {
      badgeLabel = 'WORKSPACE_DIR';
      badgeClass = 'is-environment';
      summary = 'Using the workspace directory from WORKSPACE_DIR.';
      meta = 'Save a custom directory in Settings if you want to override the environment default.';
    }

    setLauncherWorkspaceRootDisplay({
      path: visiblePath,
      summary,
      meta,
      badgeLabel,
      badgeClass
    });

    if (!launcherWorkspaceRootEditorOpen && elements.launcherWorkspaceRootInput) {
      elements.launcherWorkspaceRootInput.value = getLauncherWorkspaceRootDraftValue();
    }
    syncLauncherWorkspaceRootEditorControls();
  }

  async function saveLauncherWorkspaceRoot(workspaceRoot) {
    const response = await fetch('/api/settings/workspace-root', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ workspace_root: workspaceRoot })
    });

    if (!response.ok) {
      throw new Error(await response.text() || 'Failed to save workspace directory');
    }

    const data = await response.json();
    applyLauncherWorkspaceRootState(data);
    return data;
  }

  async function browseLauncherWorkspaceRoot() {
    const response = await fetch('/api/folder-picker/select-path', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: 'Select Default Workspace Directory'
      })
    });

    const result = await response.json().catch(() => ({}));
    if (!response.ok || !result.success) {
      throw new Error(result.error || 'Failed to open folder picker');
    }

    if (result.selected && result.path && elements.launcherWorkspaceRootInput) {
      elements.launcherWorkspaceRootInput.value = result.path;
      elements.launcherWorkspaceRootInput.focus();
    }
  }

  async function loadLauncherWorkspaceRoot() {
    if (!elements.launcherWorkspaceRootPath) {
      return;
    }

    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 8000);

    try {
      const response = await fetch('/api/settings/workspace-root', {
        signal: controller.signal
      });

      clearTimeout(timeoutId);

      if (!response.ok) {
        throw new Error(`Failed to load workspace directory (${response.status})`);
      }

      const data = await response.json();
      applyLauncherWorkspaceRootState(data);
    } catch (error) {
      clearTimeout(timeoutId);
      console.error('Failed to load launcher workspace directory:', error);
      renderLauncherWorkspaceRootError();
    }
  }

  function setLauncherOverviewExpanded(expanded) {
    if (!elements.launcherOverviewDetails || !elements.launcherOverviewToggleBtn) return;

    const isExpanded = !!expanded;
    elements.launcherOverviewDetails.hidden = !isExpanded;
    elements.launcherOverviewToggleBtn.setAttribute('aria-expanded', isExpanded ? 'true' : 'false');
    elements.launcherOverviewToggleBtn.textContent = isExpanded ? 'Hide Details' : 'Show Details';

    try {
      sessionStorage.setItem(OVERVIEW_EXPANDED_STORAGE_KEY, isExpanded ? '1' : '0');
    } catch (err) {
      // no-op: storage may be unavailable in some contexts
    }
  }

  function initLauncherOverviewExpandedState() {
    let expanded = false;
    try {
      expanded = sessionStorage.getItem(OVERVIEW_EXPANDED_STORAGE_KEY) === '1';
    } catch (err) {
      expanded = false;
    }
    setLauncherOverviewExpanded(expanded);
  }

  function persistLauncherTab(tabName) {
    try {
      sessionStorage.setItem(LAUNCHER_TAB_STORAGE_KEY, tabName);
    } catch (err) {
      // no-op: storage may be unavailable
    }
  }

  function setLauncherTab(tabName, options = {}) {
    const refreshSummary = options.refreshSummary !== false;
    const force = options.force === true;
    const nextTab = tabName === 'summary' ? 'summary' : 'workspaces';
    const tabChanged = launcherActiveTab !== nextTab;
    launcherActiveTab = nextTab;
    if (tabChanged || force) {
      persistLauncherTab(nextTab);
    }

    if (elements.launcherTabWorkspaces) {
      const isActive = nextTab === 'workspaces';
      elements.launcherTabWorkspaces.classList.toggle('is-active', isActive);
      elements.launcherTabWorkspaces.setAttribute('aria-selected', isActive ? 'true' : 'false');
    }
    if (elements.launcherTabSummary) {
      const isActive = nextTab === 'summary';
      elements.launcherTabSummary.classList.toggle('is-active', isActive);
      elements.launcherTabSummary.setAttribute('aria-selected', isActive ? 'true' : 'false');
    }
    if (elements.launcherWorkspacesPanel) {
      elements.launcherWorkspacesPanel.hidden = nextTab !== 'workspaces';
    }
    if (elements.launcherSummaryPanel) {
      elements.launcherSummaryPanel.hidden = nextTab !== 'summary';
    }

    if (nextTab === 'summary') {
      const state = window.WorkspaceHubState.getState();
      if (state.launcherSelectionMode) {
        setLauncherSelectionMode(false);
      }
      if (elements.launcherOverviewDetails && elements.launcherOverviewDetails.hidden) {
        setLauncherOverviewExpanded(true);
      }
      if (refreshSummary && (tabChanged || force)) {
        const flattened = flattenWorkspaces(state.workspaces || []).filter(isConcreteWorkspace);
        void refreshLauncherTaskOverview(flattened);
      }
    }
  }

  function initLauncherTabState() {
    let saved = '';
    try {
      saved = sessionStorage.getItem(LAUNCHER_TAB_STORAGE_KEY) || '';
    } catch (err) {
      saved = '';
    }
    setLauncherTab(saved === 'summary' ? 'summary' : 'workspaces', { force: true });
  }

  function navigateToWorkspace(workspaceId) {
    if (!workspaceId) return;
    window.location.href = `/workspaces/${encodeURIComponent(workspaceId)}`;
  }

  function normalizeWorkspaceKind(value) {
    return String(value || '').trim() === 'group' ? 'group' : 'workspace';
  }

  function isGroupWorkspace(workspace) {
    return normalizeWorkspaceKind(workspace && workspace.kind) === 'group';
  }

  function isConcreteWorkspace(workspace) {
    return !isGroupWorkspace(workspace);
  }

  function navigateToSession(sessionId) {
    if (!sessionId) return;
    window.location.href = `/chat/${encodeURIComponent(sessionId)}`;
  }

  function bindLauncherOverviewLinks() {
    if (elements.launcherOverviewByWorkspace) {
      elements.launcherOverviewByWorkspace.querySelectorAll('[data-overview-workspace]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.preventDefault();
          navigateToWorkspace(btn.getAttribute('data-overview-workspace'));
        });
      });
    }

    if (elements.launcherOverviewTopTasks) {
      elements.launcherOverviewTopTasks.querySelectorAll('[data-overview-workspace]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.preventDefault();
          navigateToWorkspace(btn.getAttribute('data-overview-workspace'));
        });
      });
    }

    if (elements.launcherOverviewScheduledTasks) {
      elements.launcherOverviewScheduledTasks.querySelectorAll('[data-overview-workspace]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.preventDefault();
          navigateToWorkspace(btn.getAttribute('data-overview-workspace'));
        });
      });
    }

    if (elements.launcherOverviewOpenSessionsList) {
      elements.launcherOverviewOpenSessionsList.querySelectorAll('[data-overview-session]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.preventDefault();
          navigateToSession(btn.getAttribute('data-overview-session'));
        });
      });
    }
  }

  function renderLauncherOverviewLoading() {
    if (elements.launcherOverviewUpdatedAt) {
      elements.launcherOverviewUpdatedAt.textContent = 'Refreshing...';
    }
    if (elements.launcherOverviewByWorkspace) {
      elements.launcherOverviewByWorkspace.innerHTML = '<div class="hub-loading">Refreshing workspace overview...</div>';
    }
    if (elements.launcherOverviewTopTasks) {
      elements.launcherOverviewTopTasks.innerHTML = '<div class="hub-loading">Refreshing ongoing tasks...</div>';
    }
    if (elements.launcherOverviewScheduledTasks) {
      elements.launcherOverviewScheduledTasks.innerHTML = '<div class="hub-loading">Refreshing scheduled tasks...</div>';
    }
    if (elements.launcherOverviewOpenSessionsList) {
      elements.launcherOverviewOpenSessionsList.innerHTML = '<div class="hub-loading">Refreshing open sessions...</div>';
    }
  }

  async function mapWithConcurrency(items, concurrency, mapper) {
    const list = Array.isArray(items) ? items : [];
    if (list.length === 0) return [];

    const results = new Array(list.length);
    const workerCount = Math.max(1, Math.min(concurrency || 1, list.length));
    let nextIndex = 0;

    async function worker() {
      while (nextIndex < list.length) {
        const current = nextIndex;
        nextIndex += 1;
        results[current] = await mapper(list[current], current);
      }
    }

    await Promise.all(Array.from({ length: workerCount }, () => worker()));
    return results;
  }

  async function fetchWorkspaceOverviewData(workspaceId) {
    const safeWorkspaceId = String(workspaceId || '').trim();
    if (!safeWorkspaceId) {
      return {
        tasks: [],
        sessions: [],
        taskError: new Error('Missing workspace ID for overview fetch'),
        sessionError: new Error('Missing workspace ID for overview fetch')
      };
    }

    const [taskResponse, sessionResponse] = await Promise.allSettled([
      fetch(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(safeWorkspaceId)}`),
      fetch(`/api/sessions?folder_id=${encodeURIComponent(safeWorkspaceId)}&sort=updated_desc&limit=50`)
    ]);

    const payload = {
      tasks: [],
      sessions: [],
      taskError: null,
      sessionError: null
    };

    if (taskResponse.status === 'fulfilled') {
      if (!taskResponse.value.ok) {
        payload.taskError = new Error(`Task request failed (${taskResponse.value.status})`);
      } else {
        try {
          const taskData = await taskResponse.value.json();
          payload.tasks = Array.isArray(taskData.tasks) ? taskData.tasks : [];
        } catch (err) {
          payload.taskError = err;
        }
      }
    } else {
      payload.taskError = taskResponse.reason || new Error('Task request failed');
    }

    if (sessionResponse.status === 'fulfilled') {
      if (!sessionResponse.value.ok) {
        payload.sessionError = new Error(`Session request failed (${sessionResponse.value.status})`);
      } else {
        try {
          const sessionData = await sessionResponse.value.json();
          payload.sessions = Array.isArray(sessionData.sessions) ? sessionData.sessions : [];
        } catch (err) {
          payload.sessionError = err;
        }
      }
    } else {
      payload.sessionError = sessionResponse.reason || new Error('Session request failed');
    }

    return payload;
  }

  async function refreshLauncherTaskOverview(flattened) {
    if (!elements.launcherTaskOverview) return;

    const workspaces = (flattened || []).filter((workspace) => workspace && workspace.id);
    if (workspaces.length === 0) {
      launcherTaskBadgeByWorkspace = new Map();
      updateLauncherOverviewMetrics({
        open: 0,
        pending: 0,
        inProgress: 0,
        needsAttention: 0,
        scheduled: 0,
        openSessions: 0
      });
      if (elements.launcherOverviewUpdatedAt) {
        elements.launcherOverviewUpdatedAt.textContent = 'No workspaces';
      }
      if (elements.launcherOverviewByWorkspace) {
        elements.launcherOverviewByWorkspace.innerHTML = '<div class="launcher-overview-empty">Create a workspace to track tasks.</div>';
      }
      if (elements.launcherOverviewTopTasks) {
        elements.launcherOverviewTopTasks.innerHTML = '<div class="launcher-overview-empty">No ongoing tasks yet.</div>';
      }
      if (elements.launcherOverviewScheduledTasks) {
        elements.launcherOverviewScheduledTasks.innerHTML = '<div class="launcher-overview-empty">No scheduled tasks yet.</div>';
      }
      if (elements.launcherOverviewOpenSessionsList) {
        elements.launcherOverviewOpenSessionsList.innerHTML = '<div class="launcher-overview-empty">No open sessions yet.</div>';
      }
      if (hubEl.dataset.state === 'launcher') {
        const state = window.WorkspaceHubState.getState();
        renderLauncher(flattenWorkspaces(state.workspaces || []));
      }
      return;
    }

    const requestSeq = ++launcherOverviewRequestSeq;
    renderLauncherOverviewLoading();

    const overviewResults = await mapWithConcurrency(workspaces, 6, async (workspace) => {
      try {
        const payload = await fetchWorkspaceOverviewData(workspace.id);
        return {
          workspace,
          tasks: payload.tasks,
          sessions: payload.sessions,
          taskError: payload.taskError,
          sessionError: payload.sessionError
        };
      } catch (err) {
        console.error('Failed to load workspace overview:', workspace.id, err);
        return {
          workspace,
          tasks: [],
          sessions: [],
          taskError: err,
          sessionError: err
        };
      }
    });

    if (requestSeq !== launcherOverviewRequestSeq) {
      return;
    }

    const totals = {
      open: 0,
      pending: 0,
      inProgress: 0,
      needsAttention: 0,
      scheduled: 0,
      openSessions: 0
    };
    const workspaceSummaries = [];
    const aggregateTasks = [];
    const scheduledTasks = [];
    const openSessions = [];
    let failedTaskFetches = 0;
    let failedSessionFetches = 0;

    overviewResults.forEach((result) => {
      const workspace = result.workspace;
      const tasks = Array.isArray(result.tasks) ? result.tasks : [];
      const sessions = Array.isArray(result.sessions) ? result.sessions : [];
      if (result.taskError) {
        failedTaskFetches += 1;
        console.error('Failed to load workspace tasks for overview:', workspace.id, result.taskError);
      }
      if (result.sessionError) {
        failedSessionFetches += 1;
        console.error('Failed to load workspace sessions for overview:', workspace.id, result.sessionError);
      }

      const summary = {
        id: workspace.id,
        name: workspace.name || 'Untitled Workspace',
        path: workspace.path || workspace.name || workspace.id,
        open: 0,
        pending: 0,
        inProgress: 0,
        needsAttention: 0,
        scheduled: 0,
        openSessions: 0
      };
      const workspaceSessionCount = Number(workspace.session_count);
      summary.openSessions = Number.isFinite(workspaceSessionCount) && workspaceSessionCount >= 0
        ? workspaceSessionCount
        : sessions.length;
      totals.openSessions += summary.openSessions;

      sessions.forEach((chatSession) => {
        openSessions.push({
          id: chatSession.id || '',
          title: chatSession.title || chatSession.name || 'Untitled chat',
          workspaceId: workspace.id,
          workspaceName: workspace.name || 'Untitled Workspace',
          agentName: chatSession.agent_name || 'default',
          messageCount: Number(chatSession.message_count) || 0,
          updatedAt: chatSession.updated_at || chatSession.created_at || ''
        });
      });

      tasks.forEach((task) => {
        const status = normalizeOverviewStatus(task.status);
        const hasSchedule = Boolean(task.schedule_enabled || task.schedule || task.next_run || task.schedule_type || task.schedule_expression);
        if (task.schedule_enabled) {
          summary.scheduled += 1;
          totals.scheduled += 1;
        }

        if (hasSchedule) {
          scheduledTasks.push({
            id: task.id || '',
            description: task.description || task.name || task.id || 'Untitled task',
            workspaceId: workspace.id,
            workspaceName: workspace.name || 'Untitled Workspace',
            nextRun: task.next_run || '',
            scheduleText: formatOverviewSchedule(task),
            enabled: Boolean(task.schedule_enabled)
          });
        }

        if (status === 'completed' || status === 'cancelled') {
          return;
        }

        if (status === 'in_progress') {
          summary.inProgress += 1;
          summary.open += 1;
          totals.inProgress += 1;
          totals.open += 1;
        } else if (status === 'failed' || status === 'timeout') {
          summary.needsAttention += 1;
          summary.open += 1;
          totals.needsAttention += 1;
          totals.open += 1;
        } else {
          summary.pending += 1;
          summary.open += 1;
          totals.pending += 1;
          totals.open += 1;
        }

        aggregateTasks.push({
          id: task.id || '',
          description: task.description || task.name || task.id || 'Untitled task',
          status,
          priority: Number(task.priority) || 0,
          createdAt: task.created_at,
          workspaceId: workspace.id,
          workspaceName: workspace.name || 'Untitled Workspace'
        });
      });

      workspaceSummaries.push(summary);
    });

    workspaceSummaries.sort((a, b) => {
      if (b.open !== a.open) return b.open - a.open;
      if (b.needsAttention !== a.needsAttention) return b.needsAttention - a.needsAttention;
      if (b.inProgress !== a.inProgress) return b.inProgress - a.inProgress;
      return a.name.localeCompare(b.name);
    });

    launcherTaskBadgeByWorkspace = new Map(
      workspaceSummaries.map((summary) => [summary.id, {
        open: summary.open,
        pending: summary.pending,
        inProgress: summary.inProgress,
        needsAttention: summary.needsAttention,
        scheduled: summary.scheduled
      }])
    );

    aggregateTasks.sort((a, b) => {
      const statusDiff = statusPriorityForOverview(a.status) - statusPriorityForOverview(b.status);
      if (statusDiff !== 0) return statusDiff;
      if (b.priority !== a.priority) return b.priority - a.priority;
      const aTime = a.createdAt ? new Date(a.createdAt).getTime() : Number.MAX_SAFE_INTEGER;
      const bTime = b.createdAt ? new Date(b.createdAt).getTime() : Number.MAX_SAFE_INTEGER;
      if (aTime !== bTime) return aTime - bTime;
      return String(a.description).localeCompare(String(b.description));
    });

    scheduledTasks.sort((a, b) => {
      if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
      const aTime = a.nextRun ? new Date(a.nextRun).getTime() : Number.MAX_SAFE_INTEGER;
      const bTime = b.nextRun ? new Date(b.nextRun).getTime() : Number.MAX_SAFE_INTEGER;
      if (aTime !== bTime) return aTime - bTime;
      const wsCmp = String(a.workspaceName).localeCompare(String(b.workspaceName));
      if (wsCmp !== 0) return wsCmp;
      return String(a.description).localeCompare(String(b.description));
    });

    openSessions.sort((a, b) => {
      const aTimeRaw = a.updatedAt ? new Date(a.updatedAt).getTime() : 0;
      const bTimeRaw = b.updatedAt ? new Date(b.updatedAt).getTime() : 0;
      const aTime = Number.isFinite(aTimeRaw) ? aTimeRaw : 0;
      const bTime = Number.isFinite(bTimeRaw) ? bTimeRaw : 0;
      if (aTime !== bTime) return bTime - aTime;
      const wsCmp = String(a.workspaceName).localeCompare(String(b.workspaceName));
      if (wsCmp !== 0) return wsCmp;
      return String(a.title).localeCompare(String(b.title));
    });

    updateLauncherOverviewMetrics(totals);
    if (elements.launcherOverviewUpdatedAt) {
      const unavailable = [];
      if (failedTaskFetches > 0) {
        unavailable.push(`${failedTaskFetches} task feed${failedTaskFetches === 1 ? '' : 's'}`);
      }
      if (failedSessionFetches > 0) {
        unavailable.push(`${failedSessionFetches} session feed${failedSessionFetches === 1 ? '' : 's'}`);
      }
      const suffix = unavailable.length > 0 ? ` | ${unavailable.join(', ')} unavailable` : '';
      elements.launcherOverviewUpdatedAt.textContent = `Updated ${formatOverviewTimestamp()}${suffix}`;
    }

    if (elements.launcherOverviewByWorkspace) {
      const rows = workspaceSummaries.filter((item) => item.open > 0 || item.openSessions > 0);
      if (rows.length === 0) {
        elements.launcherOverviewByWorkspace.innerHTML = '<div class="launcher-overview-empty">No open tasks or sessions across workspaces.</div>';
      } else {
        elements.launcherOverviewByWorkspace.innerHTML = rows.map((item) => {
          const metaParts = [];
          metaParts.push(`${item.open} open`);
          if (item.inProgress > 0) metaParts.push(`${item.inProgress} active`);
          if (item.needsAttention > 0) metaParts.push(`${item.needsAttention} attention`);
          if (item.openSessions > 0) metaParts.push(`${item.openSessions} sessions`);
          return `
            <button type="button" class="launcher-overview-workspace" data-overview-workspace="${escapeHtml(item.id)}" title="${escapeHtml(item.path)}">
              <span class="launcher-overview-workspace-name">${escapeHtml(item.name)}</span>
              <span class="launcher-overview-workspace-meta">${escapeHtml(metaParts.join(' | '))}</span>
            </button>
          `;
        }).join('');
      }
    }

    if (elements.launcherOverviewTopTasks) {
      const topTasks = aggregateTasks.slice(0, 12);
      if (topTasks.length === 0) {
        elements.launcherOverviewTopTasks.innerHTML = '<div class="launcher-overview-empty">No ongoing tasks to show.</div>';
      } else {
        elements.launcherOverviewTopTasks.innerHTML = topTasks.map((task) => `
          <div class="launcher-overview-task">
            <div class="launcher-overview-task-title">${escapeHtml(truncateText(task.description, 96) || 'Untitled task')}</div>
            <div class="launcher-overview-task-meta">${escapeHtml(task.workspaceName)} | ${escapeHtml(formatOverviewStatus(task.status))}</div>
            <button type="button" class="launcher-overview-task-link" data-overview-workspace="${escapeHtml(task.workspaceId)}">Open workspace</button>
          </div>
        `).join('');
      }
    }

    if (elements.launcherOverviewScheduledTasks) {
      const upcoming = scheduledTasks.slice(0, 20);
      if (upcoming.length === 0) {
        elements.launcherOverviewScheduledTasks.innerHTML = '<div class="launcher-overview-empty">No scheduled tasks to show.</div>';
      } else {
        elements.launcherOverviewScheduledTasks.innerHTML = upcoming.map((task) => {
          const nextRun = task.nextRun ? formatDate(task.nextRun) : 'No next run';
          const statusLabel = task.enabled ? 'enabled' : 'disabled';
          return `
            <div class="launcher-overview-task">
              <div class="launcher-overview-task-title">${escapeHtml(truncateText(task.description, 96) || 'Untitled task')}</div>
              <div class="launcher-overview-task-meta">${escapeHtml(task.workspaceName)} | ${escapeHtml(statusLabel)}</div>
              <div class="launcher-overview-task-meta">${escapeHtml(task.scheduleText)} | Next: ${escapeHtml(nextRun)}</div>
              <button type="button" class="launcher-overview-task-link" data-overview-workspace="${escapeHtml(task.workspaceId)}">Open workspace</button>
            </div>
          `;
        }).join('');
      }
    }

    if (elements.launcherOverviewOpenSessionsList) {
      const recentSessions = openSessions.slice(0, 20);
      if (recentSessions.length === 0) {
        elements.launcherOverviewOpenSessionsList.innerHTML = '<div class="launcher-overview-empty">No open sessions to show.</div>';
      } else {
        elements.launcherOverviewOpenSessionsList.innerHTML = recentSessions.map((chatSession) => {
          const updated = chatSession.updatedAt ? formatDate(chatSession.updatedAt) : 'No activity';
          const hasSessionId = Boolean(chatSession.id);
          const messageLabel = `${chatSession.messageCount} message${chatSession.messageCount === 1 ? '' : 's'}`;
          return `
            <div class="launcher-overview-task">
              <div class="launcher-overview-task-title">${escapeHtml(truncateText(chatSession.title, 96) || 'Untitled chat')}</div>
              <div class="launcher-overview-task-meta">${escapeHtml(chatSession.workspaceName)} | ${escapeHtml(chatSession.agentName)}</div>
              <div class="launcher-overview-task-meta">${escapeHtml(messageLabel)} | ${escapeHtml(updated)}</div>
              <button type="button" class="launcher-overview-task-link" data-overview-session="${escapeHtml(chatSession.id)}" ${hasSessionId ? '' : 'disabled'}>Open chat</button>
            </div>
          `;
        }).join('');
      }
    }

    if (hubEl.dataset.state === 'launcher') {
      const state = window.WorkspaceHubState.getState();
      renderLauncher(flattenWorkspaces(state.workspaces || []));
    }

    bindLauncherOverviewLinks();
  }

  function scheduleLauncherOverviewRefresh(delayMs = 700) {
    if (launcherOverviewRefreshTimer) {
      clearTimeout(launcherOverviewRefreshTimer);
    }
    launcherOverviewRefreshTimer = setTimeout(() => {
      launcherOverviewRefreshTimer = null;
      const state = window.WorkspaceHubState.getState();
      const flattened = flattenWorkspaces(state.workspaces || []).filter(isConcreteWorkspace);
      void refreshLauncherTaskOverview(flattened);
    }, delayMs);
  }

  /**
   * Populate workspace select dropdown
   * @param {Array} flattened - Flattened workspace list
   */
  function populateWorkspaceSelect(flattened) {
    if (!elements.workspaceSelect) return;

    const options = ['<option value="">Select a workspace...</option>'];
    flattened.filter(isConcreteWorkspace).forEach((workspace) => {
      const indent = workspace.depth > 0 ? `${'--'.repeat(workspace.depth)} ` : '';
      const label = `${indent}${escapeHtml(workspace.name || 'Untitled Workspace')}`;
      options.push(`<option value="${escapeHtml(workspace.id)}">${label}</option>`);
    });

    elements.workspaceSelect.innerHTML = options.join('');
  }

  /**
   * Render launcher grid
   * @param {Array} flattened - Flattened workspace list
   */
  function renderLauncher(flattened) {
    if (!elements.launcherGrid || !elements.launcherEmpty) return;

    const state = window.WorkspaceHubState.getState();

    if (flattened.length === 0) {
      elements.launcherGrid.innerHTML = '';
      elements.launcherEmpty.style.display = 'flex';
      return;
    }

    elements.launcherEmpty.style.display = 'none';

    const selectionMode = !!state.launcherSelectionMode;
    const selectedSet = state.selectedWorkspaces || new Set();

    const flattenedMap = new Map((flattened || []).map((ws) => [ws.id, ws]));

    function renderWorkspaceCard(workspace) {
      const row = flattenedMap.get(workspace.id) || workspace;
      const description = row.description || 'No description yet.';
      const status = row.status || 'active';
      const statusLabel = escapeHtml(String(status).replace('_', ' '));
      const accentStyle = row.color ? `style="border-color: ${escapeHtml(row.color)}"` : '';
      const hasChildren = Array.isArray(row.children) && row.children.length > 0;
      const deleteTitle = hasChildren ? 'Delete group' : 'Delete workspace';
      const taskBadge = renderLauncherTaskBadge(row.id);
      const folderDisplay = getWorkspaceFolderDisplay(row);
      const parentGroup = row.parent_id ? flattenedMap.get(row.parent_id) : null;
      const parentGroupChip = parentGroup && isGroupWorkspace(parentGroup)
        ? `<span class="launcher-card-parent-chip">In ${escapeHtml(parentGroup.name || 'Group')}</span>`
        : '';
      const folderState = `
          <div class="launcher-card-folder-state ${folderDisplay.badgeClass}">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path d="M10,4H4A2,2 0 0,0 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8A2,2 0 0,0 20,6H12L10,4Z"/>
            </svg>
            <span>${escapeHtml(folderDisplay.badgeLabel)}</span>
          </div>
      `;

      const checked = selectedSet.has(row.id);
      const checkbox = selectionMode ? `
          <label class="launcher-card-checkbox" aria-label="Select workspace ${escapeHtml(row.name || row.id)}">
            <input type="checkbox" data-workspace-checkbox="${escapeHtml(row.id)}" ${checked ? 'checked' : ''} />
            <span class="launcher-card-checkmark" aria-hidden="true"></span>
          </label>
      ` : '';

      const deleteButton = `
        <button class="launcher-card-delete" type="button" draggable="false" data-workspace-delete="${escapeHtml(row.id)}" title="${deleteTitle}" aria-label="${deleteTitle}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
      `;

      return `
        <div class="launcher-card-item" role="button" tabindex="0" draggable="true" data-workspace-id="${escapeHtml(row.id)}" data-workspace-kind="workspace" data-select-mode="${selectionMode ? '1' : '0'}" data-folder-linked="${folderDisplay.linked ? '1' : '0'}" ${accentStyle} aria-label="Open workspace ${escapeHtml(row.name || 'Untitled Workspace')} with ${escapeHtml(folderDisplay.ariaLabel)}">
          ${checkbox}
          ${deleteButton}
          <div class="launcher-card-title-row">
            <div class="launcher-card-title">${escapeHtml(row.name || 'Untitled Workspace')}</div>
            ${taskBadge}
          </div>
          ${parentGroupChip}
          ${folderState}
          <div class="launcher-card-path${folderDisplay.linked ? '' : ' is-empty'}" title="${escapeHtml(folderDisplay.detailTitle)}">${escapeHtml(folderDisplay.detail)}</div>
          <div class="launcher-card-description">${escapeHtml(description)}</div>
          <div class="launcher-card-meta">
            <span class="launcher-card-status status-${escapeHtml(status)}">${statusLabel}</span>
            <span>${row.session_count || 0} sessions</span>
          </div>
        </div>
      `;
    }

    function renderGroupSection(workspace) {
      const row = flattenedMap.get(workspace.id) || workspace;
      const childCount = Array.isArray(workspace.children) ? workspace.children.length : 0;
      const isCollapsed = state.launcherCollapsedGroups && state.launcherCollapsedGroups.has(row.id);
      const hasChildren = Array.isArray(workspace.children) && workspace.children.length > 0;
      const previewNames = (workspace.children || [])
        .slice(0, 3)
        .map((child) => escapeHtml(child && (child.name || child.id) || 'Untitled Workspace'))
        .join(' · ');
      const previewSuffix = childCount > 3 ? ` +${childCount - 3} more` : '';
      const groupDescription = row.description || 'Organization-only group';

      const toggleBtn = `
        <button class="launcher-group-toggle ${isCollapsed ? 'is-collapsed' : ''}" type="button" data-group-toggle="${escapeHtml(row.id)}" aria-label="${isCollapsed ? 'Expand' : 'Collapse'} group" aria-expanded="${isCollapsed ? 'false' : 'true'}" title="${isCollapsed ? 'Expand' : 'Collapse'}">
          <svg class="launcher-group-toggle-icon" width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M7,10L12,15L17,10H7Z"/>
          </svg>
        </button>
      `;

      const groupHeader = `
        <div class="launcher-card-item launcher-group-header" role="button" tabindex="0" draggable="true" data-workspace-id="${escapeHtml(row.id)}" data-workspace-kind="group" data-select-mode="${selectionMode ? '1' : '0'}" aria-label="${isCollapsed ? 'Expand' : 'Collapse'} group ${escapeHtml(row.name || 'Group')}" aria-expanded="${isCollapsed ? 'false' : 'true'}">
          <button class="launcher-card-delete" type="button" draggable="false" data-workspace-delete="${escapeHtml(row.id)}" title="Delete group" aria-label="Delete group">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
          ${toggleBtn}
          <div class="launcher-card-title-row">
            <div class="launcher-card-title">${escapeHtml(row.name || 'Group')}</div>
            <span class="launcher-group-kind-pill">Group</span>
          </div>
          <div class="launcher-card-path">Contains ${childCount} workspace${childCount === 1 ? '' : 's'}</div>
          <div class="launcher-card-description">${escapeHtml(groupDescription)}</div>
          <div class="launcher-group-preview">${previewNames ? `${previewNames}${escapeHtml(previewSuffix)}` : 'Drop workspaces here to organize related work.'}</div>
        </div>
      `;

      const childrenHtml = (workspace.children || []).map((child) => {
        // child is already a tree node; ensure we render nested cards.
        if (child && (isGroupWorkspace(child) || (Array.isArray(child.children) && child.children.length > 0))) {
          return renderGroupSection(child);
        }
        return renderWorkspaceCard(child);
      }).join('');
      const emptyHint = hasChildren ? '' : '<div class="launcher-group-empty">Drop workspaces here</div>';

      return `
        <div class="launcher-group${isCollapsed ? ' is-collapsed' : ''}">
          ${groupHeader}
          <div class="launcher-grid launcher-group-grid${hasChildren ? '' : ' is-empty'}" ${isCollapsed ? 'hidden' : ''} data-group-children="${escapeHtml(row.id)}">
            ${childrenHtml || emptyHint}
          </div>
        </div>
      `;
    }

    const tree = Array.isArray(state.workspaces) ? state.workspaces : [];
    const html = tree.map((ws) => {
      if (ws && (isGroupWorkspace(ws) || (Array.isArray(ws.children) && ws.children.length > 0))) {
        return renderGroupSection(ws);
      }
      return renderWorkspaceCard(ws);
    }).join('');

    elements.launcherGrid.innerHTML = html;

    elements.launcherGrid.querySelectorAll('[data-workspace-id]').forEach((card) => {
      card.addEventListener('click', (e) => {
        const workspaceId = card.dataset.workspaceId;
        const workspaceKind = normalizeWorkspaceKind(card.dataset.workspaceKind);
        const isSelectMode = card.dataset.selectMode === '1';

        // Cmd/Ctrl+click for quick multi-select (even outside selection mode)
        if (e.metaKey || e.ctrlKey) {
          if (workspaceKind === 'group') {
            e.preventDefault();
            toggleGroupCollapsed(workspaceId);
            return;
          }
          e.preventDefault();
          // Auto-enable selection mode if not already enabled
          if (!window.WorkspaceHubState.getState().launcherSelectionMode) {
            setLauncherSelectionMode(true);
          }
          toggleLauncherWorkspaceSelection(workspaceId);
          return;
        }

        if (isSelectMode) {
          if (workspaceKind === 'group') {
            e.preventDefault();
            toggleGroupCollapsed(workspaceId);
            return;
          }
          e.preventDefault();
          toggleLauncherWorkspaceSelection(workspaceId);
          return;
        }

        if (workspaceKind === 'group') {
          e.preventDefault();
          toggleGroupCollapsed(workspaceId);
          return;
        }

        navigateToWorkspace(workspaceId);
      });

      card.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          card.click();
        }
      });
    });

    elements.launcherGrid.querySelectorAll('[data-workspace-checkbox]').forEach((cb) => {
      cb.addEventListener('click', (e) => e.stopPropagation());
      cb.addEventListener('change', (e) => {
        const id = e.target.getAttribute('data-workspace-checkbox');
        toggleLauncherWorkspaceSelection(id, { force: e.target.checked });
      });
    });

    elements.launcherGrid.querySelectorAll('[data-group-toggle]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const id = btn.getAttribute('data-group-toggle');
        toggleGroupCollapsed(id);
      });
    });

    elements.launcherGrid.querySelectorAll('[data-group-children]').forEach((grid) => {
      grid.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.stopPropagation();
        grid.classList.add('is-drag-over');
        e.dataTransfer.dropEffect = 'move';
      });
      grid.addEventListener('dragleave', () => {
        grid.classList.remove('is-drag-over');
      });
      grid.addEventListener('drop', (e) => {
        e.preventDefault();
        e.stopPropagation();
        grid.classList.remove('is-drag-over');
        const draggedId = e.dataTransfer.getData('text/plain');
        const targetId = grid.getAttribute('data-group-children');
        if (draggedId && targetId && draggedId !== targetId) {
          reorderWorkspace(draggedId, targetId, '');
        }
      });
    });

    elements.launcherGrid.querySelectorAll('[data-workspace-delete]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const id = btn.getAttribute('data-workspace-delete');
        confirmDeleteWorkspace(id);
      });
    });

    updateLauncherSelectionUI();
    bindLauncherDragEvents();
    animateLauncherGroupReveal();
  }

  function animateLauncherGroupReveal() {
    const state = window.WorkspaceHubState.getState();
    const revealIds = state.launcherJustExpandedGroups;
    if (!revealIds || revealIds.size === 0) return;

    const ids = Array.from(revealIds);
    state.launcherJustExpandedGroups = new Set();

    ids.forEach((id) => {
      const safeId = (window.CSS && CSS.escape) ? CSS.escape(id) : id;
      const header = elements.launcherGrid.querySelector(`.launcher-group-header[data-workspace-id="${safeId}"]`);
      const grid = elements.launcherGrid.querySelector(`[data-group-children="${safeId}"]`);
      if (header) header.classList.add('is-revealed');
      if (grid) grid.classList.add('is-revealed');

      window.setTimeout(() => {
        if (header) header.classList.remove('is-revealed');
        if (grid) grid.classList.remove('is-revealed');
      }, 520);
    });
  }

  function bindLauncherDragEvents() {
    const items = elements.launcherGrid.querySelectorAll('.launcher-card-item');
    const rootDrop = elements.launcherRootDropZone;
    const rootGrid = elements.launcherGrid;

    const isRootGridTarget = (eventTarget) => {
      if (!eventTarget) return false;
      if (!rootGrid || rootGrid !== eventTarget && !rootGrid.contains(eventTarget)) return false;
      if (eventTarget.closest('.launcher-card-item')) return false;
      if (eventTarget.closest('.launcher-group-grid')) return false;
      return true;
    };

    if (rootDrop) {
      rootDrop.addEventListener('dragover', (e) => {
        e.preventDefault();
        rootDrop.classList.add('is-drag-over');
        e.dataTransfer.dropEffect = 'move';
      });
      rootDrop.addEventListener('dragleave', () => {
        rootDrop.classList.remove('is-drag-over');
      });
      rootDrop.addEventListener('drop', (e) => {
        e.preventDefault();
        e.stopPropagation();
        rootDrop.classList.remove('is-drag-over');
        const draggedId = e.dataTransfer.getData('text/plain');
        if (draggedId) {
          reorderWorkspace(draggedId, '', '');
        }
      });
    }

    if (rootGrid) {
      rootGrid.addEventListener('dragover', (e) => {
        if (!isRootGridTarget(e.target)) return;
        e.preventDefault();
        rootGrid.classList.add('is-drag-over-root');
        e.dataTransfer.dropEffect = 'move';
      });
      rootGrid.addEventListener('dragleave', (e) => {
        if (!isRootGridTarget(e.target)) return;
        rootGrid.classList.remove('is-drag-over-root');
      });
      rootGrid.addEventListener('drop', (e) => {
        if (!isRootGridTarget(e.target)) return;
        e.preventDefault();
        e.stopPropagation();
        rootGrid.classList.remove('is-drag-over-root');
        const draggedId = e.dataTransfer.getData('text/plain');
        if (draggedId) {
          reorderWorkspace(draggedId, '', '');
        }
      });
    }

    items.forEach(item => {
      item.addEventListener('dragstart', (e) => {
        // Only allow dragging if NOT in selection mode
        const state = window.WorkspaceHubState.getState();
        if (state.launcherSelectionMode) {
          e.preventDefault();
          return;
        }
        e.dataTransfer.setData('text/plain', e.currentTarget.dataset.workspaceId);
        e.currentTarget.classList.add('is-dragging');
        e.dataTransfer.effectAllowed = 'move';
        if (rootDrop) {
          rootDrop.hidden = false;
          rootDrop.classList.add('is-active');
        }
      });

      item.addEventListener('dragend', (e) => {
        e.currentTarget.classList.remove('is-dragging');
        // Clean up any remaining drag-over classes
        items.forEach(i => i.classList.remove('is-drag-over'));
        if (rootDrop) {
          rootDrop.classList.remove('is-drag-over');
          rootDrop.classList.remove('is-active');
          rootDrop.hidden = true;
        }
        if (rootGrid) {
          rootGrid.classList.remove('is-drag-over-root');
        }
      });

      item.addEventListener('dragover', (e) => {
        e.preventDefault(); // Allow drop
        const draggedId = document.querySelector('.launcher-card-item.is-dragging')?.dataset.workspaceId;
        const targetId = e.currentTarget.dataset.workspaceId;
        
        // Don't highlight if dragging over itself
        if (draggedId === targetId) return;
        
        e.dataTransfer.dropEffect = 'move';
        e.currentTarget.classList.add('is-drag-over');
      });

      item.addEventListener('dragleave', (e) => {
        e.currentTarget.classList.remove('is-drag-over');
      });

      item.addEventListener('drop', (e) => {
        e.preventDefault();
        e.currentTarget.classList.remove('is-drag-over');

        const draggedId = e.dataTransfer.getData('text/plain');
        const targetId = e.currentTarget.dataset.workspaceId;

        if (!draggedId || !targetId || draggedId === targetId) return;

        const targetParentId = getWorkspaceParentId(targetId);
        reorderWorkspace(draggedId, targetParentId, targetId);
      });
    });
  }

  async function reorderWorkspace(draggedId, targetParentId, insertBeforeId = '') {
    const state = window.WorkspaceHubState.getState();
    if (!draggedId) return;
    if (targetParentId === undefined || targetParentId === null) return;
    if (draggedId === targetParentId) return;

    if (targetParentId) {
      const descendants = collectWorkspaceDescendantIds(state.workspaces || [], draggedId, { includeRoot: false });
      if (descendants.includes(targetParentId)) {
        if (window.Toast) window.Toast.error('Cannot move a workspace into its own descendant.');
        return;
      }
    }

    // Keep the target group expanded so the moved workspace stays visible after reload.
    if (targetParentId) {
      if (state.launcherCollapsedGroups) {
        const next = new Set(state.launcherCollapsedGroups);
        next.delete(targetParentId);
        state.launcherCollapsedGroups = next;
      }
      if (!state.launcherJustExpandedGroups) state.launcherJustExpandedGroups = new Set();
      state.launcherJustExpandedGroups.add(targetParentId);
    }

    const sourceParentId = getWorkspaceParentId(draggedId);
    const updates = {};

    const targetSiblings = getSiblingIds(targetParentId).filter((id) => id !== draggedId);
    const insertIndex = insertBeforeId ? targetSiblings.indexOf(insertBeforeId) : -1;
    if (insertIndex >= 0) {
      targetSiblings.splice(insertIndex, 0, draggedId);
    } else {
      targetSiblings.push(draggedId);
    }
    buildOrderUpdates(targetParentId, targetSiblings, updates, draggedId);

    if (sourceParentId !== targetParentId) {
      const sourceSiblings = getSiblingIds(sourceParentId).filter((id) => id !== draggedId);
      buildOrderUpdates(sourceParentId, sourceSiblings, updates, '');
    }

    try {
      await persistWorkspaceOrder(updates);

      if (window.Toast) {
        if (sourceParentId === targetParentId) {
          window.Toast.success('Workspace order updated');
        } else if (targetParentId) {
          const parentLabel = getWorkspaceLabel(targetParentId);
          window.Toast.success(`Workspace moved to "${parentLabel}"`);
        } else {
          window.Toast.success('Workspace moved to top level');
        }
      }

      await loadWorkspaces();
    } catch (err) {
      console.error('Failed to reorder workspace:', err);
      if (window.Toast) window.Toast.error('Failed to reorder workspace: ' + err.message);
    }
  }

  function toggleGroupCollapsed(workspaceId) {
    const state = window.WorkspaceHubState.getState();
    if (!state.launcherCollapsedGroups) state.launcherCollapsedGroups = new Set();

    const next = new Set(state.launcherCollapsedGroups);
    if (next.has(workspaceId)) next.delete(workspaceId);
    else next.add(workspaceId);
    state.launcherCollapsedGroups = next;

    const flattened = flattenWorkspaces(state.workspaces || []);
    renderLauncher(flattened);
  }

  function setLauncherSelectionMode(enabled) {
    const state = window.WorkspaceHubState.getState();
    state.launcherSelectionMode = !!enabled;
    if (!state.launcherSelectionMode) {
      state.selectedWorkspaces = new Set();
    }

    hubEl.dataset.launcherSelect = state.launcherSelectionMode ? 'true' : 'false';

    if (elements.launcherSelectionBar) {
      elements.launcherSelectionBar.hidden = !state.launcherSelectionMode;
    }
    if (elements.launcherSelectModeBtn) {
      elements.launcherSelectModeBtn.classList.toggle('is-active', state.launcherSelectionMode);
    }

    // Re-render launcher to show/hide checkboxes
    const flattened = flattenWorkspaces(state.workspaces || []);
    renderLauncher(flattened);
  }

  function toggleLauncherWorkspaceSelection(workspaceId, { force } = {}) {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedWorkspaces) state.selectedWorkspaces = new Set();

    const next = new Set(state.selectedWorkspaces);
    const shouldSelect = typeof force === 'boolean' ? force : !next.has(workspaceId);
    if (shouldSelect) next.add(workspaceId);
    else next.delete(workspaceId);

    state.selectedWorkspaces = next;

    // Visual update
    const card = elements.launcherGrid.querySelector(`.launcher-card-item[data-workspace-id="${workspaceId}"]`);
    if (card) {
      const checkbox = card.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.checked = shouldSelect;
      }
    }

    updateLauncherSelectionUI();
  }

  function updateLauncherSelectionUI() {
    const state = window.WorkspaceHubState.getState();
    const selectedCount = state.selectedWorkspaces ? state.selectedWorkspaces.size : 0;

    if (elements.launcherSelectionCount) {
      elements.launcherSelectionCount.textContent = `${selectedCount} selected`;
    }

    if (elements.launcherGroupSelectedBtn) {
      elements.launcherGroupSelectedBtn.disabled = selectedCount === 0;
    }
    if (elements.launcherDeleteSelectedBtn) {
      elements.launcherDeleteSelectedBtn.disabled = selectedCount === 0;
    }
  }

  function getWorkspaceLabel(workspaceId) {
    const state = window.WorkspaceHubState.getState();
    const workspace = state.workspaceMap.get(workspaceId);
    return workspace?.name || 'workspace';
  }

  function getWorkspaceParentId(workspaceId) {
    const state = window.WorkspaceHubState.getState();
    const workspace = state.workspaceMap.get(workspaceId);
    return workspace?.parent_id || '';
  }

  function getSiblingIds(parentId) {
    const state = window.WorkspaceHubState.getState();
    if (!parentId) {
      return (state.workspaces || []).map((ws) => ws.id).filter(Boolean);
    }

    const stack = [...(state.workspaces || [])];
    while (stack.length) {
      const node = stack.pop();
      if (!node) continue;
      if (node.id === parentId) {
        return (node.children || []).map((child) => child.id).filter(Boolean);
      }
      if (node.children && node.children.length > 0) {
        stack.push(...node.children);
      }
    }

    return [];
  }

  function buildOrderUpdates(parentId, orderedIds, updates, movedId) {
    orderedIds.forEach((id, index) => {
      if (!updates[id]) updates[id] = {};
      updates[id].order_index = index + 1;
      if (movedId && id === movedId) {
        updates[id].parent_id = parentId;
      }
    });
  }

  async function persistWorkspaceOrder(updates) {
    const entries = Object.entries(updates);
    if (entries.length === 0) return;

    const responses = await Promise.all(entries.map(([id, payload]) => (
      fetch(`/api/workspaces/${encodeURIComponent(id)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      })
    )));

    const failed = responses.find((res) => !res.ok);
    if (failed) {
      const text = await failed.text();
      throw new Error(text || 'Failed to reorder workspaces');
    }
  }

  function workspaceHasChildren(workspaceId) {
    const state = window.WorkspaceHubState.getState();
    const workspace = state.workspaceMap.get(workspaceId);
    return Array.isArray(workspace?.children) && workspace.children.length > 0;
  }

  function workspaceIsGroup(workspaceId) {
    const state = window.WorkspaceHubState.getState();
    return isGroupWorkspace(state.workspaceMap.get(workspaceId));
  }

  async function deleteWorkspacesByIds(ids) {
    const state = window.WorkspaceHubState.getState();
    if (!ids || ids.length === 0) return;

    try {
      for (const id of ids) {
        const response = await fetch(`/api/workspaces/${encodeURIComponent(id)}?confirm=true`, { method: 'DELETE' });
        if (!response.ok && response.status !== 404) {
          const text = await response.text();
          throw new Error(text || 'Failed to delete workspace');
        }
      }

      if (window.Toast) window.Toast.success(ids.length > 1 ? 'Workspaces deleted' : 'Workspace deleted');

      state.selectedWorkspaces = new Set();
      setLauncherSelectionMode(false);
      await loadWorkspaces();
    } catch (err) {
      console.error('Failed to delete workspaces:', err);
      if (window.Toast) window.Toast.error('Failed to delete workspaces');
    }
  }

  function openDeleteGroupModal(workspaceId) {
    const state = window.WorkspaceHubState.getState();
    const workspace = state.workspaceMap.get(workspaceId);
    const descendants = collectWorkspaceDescendantIds(state.workspaces || [], workspaceId, { includeRoot: false });
    const childCount = descendants.length;

    if (!elements.launcherDeleteGroupModal || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      const deleteAll = confirm(`"${workspace?.name || 'Group'}" has ${childCount} sub-workspace(s).\n\nClick OK to delete the group and all contents (workspace folders and files will be permanently removed from disk).\nClick Cancel to delete only the parent group.`);
      if (deleteAll) {
        void deleteGroupAndContents(workspaceId);
      } else {
        void deleteWorkspacesByIds([workspaceId]);
      }
      return;
    }

    pendingDeleteGroupId = workspaceId;
    if (elements.launcherDeleteGroupName) {
      elements.launcherDeleteGroupName.textContent = workspace?.name || 'Group';
    }
    if (elements.launcherDeleteGroupCount) {
      elements.launcherDeleteGroupCount.textContent = String(childCount);
    }

    const modal = bootstrap.Modal.getInstance(elements.launcherDeleteGroupModal) || new bootstrap.Modal(elements.launcherDeleteGroupModal);
    modal.show();
  }

  async function deleteGroupAndContents(workspaceId) {
    const state = window.WorkspaceHubState.getState();
    const ids = collectWorkspaceDescendantIds(state.workspaces || [], workspaceId, { includeRoot: true });
    const ordered = ids.slice().reverse();
    await deleteWorkspacesByIds(ordered);
  }

  function handleDeleteGroupChoice(includeChildren) {
    const workspaceId = pendingDeleteGroupId;
    pendingDeleteGroupId = null;
    if (!workspaceId) return;

    if (elements.launcherDeleteGroupModal && typeof bootstrap !== 'undefined' && bootstrap.Modal) {
      const modal = bootstrap.Modal.getInstance(elements.launcherDeleteGroupModal);
      if (modal) modal.hide();
    }

    if (includeChildren) {
      void deleteGroupAndContents(workspaceId);
    } else {
      void deleteWorkspacesByIds([workspaceId]);
    }
  }

  function confirmDeleteWorkspace(workspaceId) {
    if (!workspaceId) return;

    if (workspaceHasChildren(workspaceId) || workspaceIsGroup(workspaceId)) {
      openDeleteGroupModal(workspaceId);
      return;
    }

    const label = getWorkspaceLabel(workspaceId);
    if (!confirm(`Delete "${label}"?\n\nThis will permanently remove the workspace folder and all its contents (files, notes, etc.) from disk. This action cannot be undone.`)) return;

    void deleteWorkspacesByIds([workspaceId]);
  }

  async function deleteSelectedWorkspaces() {
    const state = window.WorkspaceHubState.getState();
    const selected = Array.from(state.selectedWorkspaces || []);
    if (selected.length === 0) return;

    if (!confirm(`Delete ${selected.length} workspace(s)?\n\nThis will permanently remove the workspace folders and all their contents (files, notes, etc.) from disk. This action cannot be undone.`)) {
      return;
    }

    await deleteWorkspacesByIds(selected);
  }

  async function createGroupFromSelection() {
    const state = window.WorkspaceHubState.getState();
    const selected = Array.from(state.selectedWorkspaces || []);
    if (selected.length === 0) return;

    const name = (elements.launcherGroupNameInput?.value || '').trim();
    const description = (elements.launcherGroupDescriptionInput?.value || '').trim();
    if (!name) {
      if (window.Toast) window.Toast.error('Group name is required');
      return;
    }

    try {
      const createRes = await fetch('/api/workspaces', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, description, kind: 'group' })
      });
      if (!createRes.ok) {
        const text = await createRes.text();
        throw new Error(text || 'Failed to create group');
      }
      const created = await createRes.json();
      const groupId = created?.folder?.id;
      if (!groupId) throw new Error('Failed to create group');

      const moveResults = await Promise.all(selected.map((id) => fetch(`/api/workspaces/${encodeURIComponent(id)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ parent_id: groupId })
      })));

      const failed = moveResults.find((r) => !r.ok);
      if (failed) {
        const text = await failed.text();
        throw new Error(text || 'Failed to move one or more workspaces');
      }

      if (window.Toast) window.Toast.success('Group created. No workspace folder was created.');

      // Close modal
      if (elements.launcherGroupModal && typeof bootstrap !== 'undefined' && bootstrap.Modal) {
        const modal = bootstrap.Modal.getInstance(elements.launcherGroupModal);
        if (modal) modal.hide();
      }
      if (elements.launcherGroupNameInput) elements.launcherGroupNameInput.value = '';
      if (elements.launcherGroupDescriptionInput) elements.launcherGroupDescriptionInput.value = '';

      // Reset selection + refresh
      setLauncherSelectionMode(false);
      await loadWorkspaces();
    } catch (err) {
      console.error('Failed to create group:', err);
      if (window.Toast) window.Toast.error('Failed to create group');
    }
  }

  /**
   * Update workspace via API
   * @param {string} workspaceId - Workspace ID
   * @param {Object} updates - Fields to update (name, description, etc.)
   */
  async function updateWorkspace(workspaceId, updates) {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}`, {
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

  function buildWorkspaceSlugConflictMessage(conflict) {
    const requestedSlug = typeof conflict?.requested_slug === 'string' ? conflict.requested_slug.trim() : '';
    const suggestedSlug = typeof conflict?.suggested_slug === 'string' ? conflict.suggested_slug.trim() : '';
    const location = typeof conflict?.location === 'string' ? conflict.location.trim().replace(/[\\/]+$/, '') : '';
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

  async function renameWorkspace(workspaceId, newName, folderSlug = '') {
    const payload = { name: newName };
    if (folderSlug) {
      payload.folder_slug = folderSlug;
    }

    const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/rename`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    const result = await response.json().catch(() => ({}));
    if (response.status === 409 && result?.conflict?.type === 'folder_slug') {
      const suggestedSlug = typeof result.conflict.suggested_slug === 'string'
        ? result.conflict.suggested_slug.trim()
        : '';
      if (suggestedSlug && window.confirm(buildWorkspaceSlugConflictMessage(result.conflict))) {
        return renameWorkspace(workspaceId, newName, suggestedSlug);
      }
      const cancelled = new Error(result?.error || 'Workspace rename cancelled');
      cancelled.cancelled = true;
      throw cancelled;
    }

    if (!response.ok) {
      throw new Error(result?.error || result?.message || 'Failed to rename workspace');
    }

    return result;
  }

  /**
   * Create inline editable element
   * @param {HTMLElement} element - The element to make editable
   * @param {string} field - Field name ('name' or 'description')
   * @param {boolean} isMultiline - Whether to use textarea
   */
  function makeEditable(element, field, isMultiline = false) {
    if (!element) return;

    element.classList.add('is-editable');
    element.title = 'Double-click to edit';

    element.addEventListener('dblclick', (e) => {
      e.preventDefault();
      e.stopPropagation();

      const state = window.WorkspaceHubState.getState();
      if (!state.selectedId) return;

      // Get actual value from workspace, not display text (which may be placeholder)
      const workspace = state.workspaceMap.get(state.selectedId);
      const currentValue = workspace ? (workspace[field] || '') : '';

      // Create input/textarea
      const input = document.createElement(isMultiline ? 'textarea' : 'input');
      input.className = 'hub-inline-edit-input';
      input.value = currentValue;
      if (!isMultiline) {
        input.type = 'text';
      } else {
        input.rows = 2;
      }

      // Store original display
      const originalDisplay = element.style.display;

      // Hide text, show input
      element.style.display = 'none';
      element.parentNode.insertBefore(input, element.nextSibling);
      input.focus();
      input.select();

      const finishEdit = async (save) => {
        const newValue = input.value.trim();
        input.remove();
        element.style.display = originalDisplay || '';

        if (!save || newValue === currentValue) return;

        // For name, don't allow empty
        if (field === 'name' && !newValue) {
          if (window.Toast) window.Toast.error('Name cannot be empty');
          return;
        }

        try {
          const result = field === 'name'
            ? await renameWorkspace(state.selectedId, newValue)
            : await updateWorkspace(state.selectedId, { [field]: newValue });

          const workspace = state.workspaceMap.get(state.selectedId);
          const updatedWorkspace = result?.folder || result?.workspace || null;
          if (workspace) {
            if (updatedWorkspace && typeof updatedWorkspace === 'object') {
              Object.assign(workspace, updatedWorkspace);
            } else {
              workspace[field] = newValue;
            }
          }

          if (workspace) {
            renderWorkspaceSummary(workspace);
          }
          renderLauncher(flattenWorkspaces(state.workspaces || []));

          if (window.Toast) {
            if (field === 'name') {
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
        } catch (err) {
          console.error(`Failed to update ${field}:`, err);
          if (!err?.cancelled && window.Toast) window.Toast.error(err.message || `Failed to update ${field}`);
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
    });
  }

  /**
   * Render workspace summary header
   * @param {Object} workspace - Workspace object
   */
  function renderWorkspaceSummary(workspace) {
    if (!workspace) return;

    if (elements.headerTitle) {
      elements.headerTitle.textContent = workspace.name || 'Workspace';
    }

    if (elements.workspaceDescription) {
      if (workspace.description) {
        elements.workspaceDescription.textContent = workspace.description;
        elements.workspaceDescription.style.display = 'block';
        elements.workspaceDescription.style.opacity = '1';
      } else {
        elements.workspaceDescription.textContent = 'No description - double-click to add';
        elements.workspaceDescription.style.display = 'block';
        elements.workspaceDescription.style.opacity = '0.6';
      }
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

    if (elements.workspaceCanvasBtn) {
      elements.workspaceCanvasBtn.href = `/workspaces/${encodeURIComponent(workspace.id)}/canvas`;
    }
  }

  /**
   * Clear workspace summary header
   */
  function clearWorkspaceSummary() {
    if (elements.headerTitle) {
      elements.headerTitle.textContent = '';
    }

    if (elements.workspaceStatus) elements.workspaceStatus.textContent = '--';
    if (elements.workspaceUpdated) elements.workspaceUpdated.textContent = '--';
    if (elements.workspaceAgents) elements.workspaceAgents.textContent = '--';
    if (elements.workspaceDescription) {
      elements.workspaceDescription.textContent = '';
      elements.workspaceDescription.style.display = 'none';
    }

    if (elements.workspaceCanvasBtn) elements.workspaceCanvasBtn.removeAttribute('href');
  }

  function populateWorkspaceParentSelect(selectedWorkspaceId) {
    if (!elements.workspaceParentSelect) return;
    const state = window.WorkspaceHubState.getState();

    const excluded = new Set();
    if (selectedWorkspaceId) {
      excluded.add(selectedWorkspaceId);
      const descendants = collectWorkspaceDescendantIds(state.workspaces || [], selectedWorkspaceId, { includeRoot: false });
      descendants.forEach((id) => excluded.add(id));
    }

    const flattened = flattenWorkspaces(state.workspaces || []);
    const options = ['<option value="">No group</option>'];
    flattened.forEach((ws) => {
      if (!ws || !ws.id) return;
      if (excluded.has(ws.id)) return;
      if (!isGroupWorkspace(ws)) return;

      const indent = ws.depth > 0 ? `${'--'.repeat(ws.depth)} ` : '';
      const label = `${indent}${escapeHtml(ws.name || ws.id)}`;
      options.push(`<option value="${escapeHtml(ws.id)}">${label}</option>`);
    });

    elements.workspaceParentSelect.innerHTML = options.join('');
  }

  function openWorkspaceMoveModal() {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedId) return;
    if (!elements.workspaceMoveModal || !elements.workspaceParentSelect) return;
    if (typeof bootstrap === 'undefined' || !bootstrap.Modal) return;

    const ws = state.workspaceMap.get(state.selectedId);
    populateWorkspaceParentSelect(state.selectedId);
    elements.workspaceParentSelect.value = (ws && ws.parent_id) ? ws.parent_id : '';

    const modal = bootstrap.Modal.getInstance(elements.workspaceMoveModal) || new bootstrap.Modal(elements.workspaceMoveModal);
    modal.show();
  }

  async function saveWorkspaceParent() {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedId) return;
    if (!elements.workspaceParentSelect) return;

    const parentId = (elements.workspaceParentSelect.value || '').trim();
    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(state.selectedId)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ parent_id: parentId })
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to update workspace group');
      }

      if (window.Toast) window.Toast.success('Workspace updated');

      // Keep selected workspace after reload
      sessionStorage.setItem(window.WorkspaceHubState.getStorageKey(), state.selectedId);
      await loadWorkspaces();

      if (elements.workspaceMoveModal && typeof bootstrap !== 'undefined' && bootstrap.Modal) {
        const modal = bootstrap.Modal.getInstance(elements.workspaceMoveModal);
        if (modal) modal.hide();
      }
    } catch (err) {
      console.error('Failed to update workspace parent:', err);
      if (window.Toast) window.Toast.error('Failed to update workspace');
    }
  }

  /**
   * Select a workspace
   * @param {string} workspaceId - Workspace ID to select
   * @param {Object} options - Options
   * @param {boolean} options.focus - Whether to blur select after selection
   */
  function _selectWorkspace(workspaceId, { focus = false } = {}) {
    const state = window.WorkspaceHubState.getState();
    const workspace = state.workspaceMap.get(workspaceId);
    if (!workspace) return;

    // Leaving launcher selection mode
    if (state.launcherSelectionMode) {
      setLauncherSelectionMode(false);
    }

    window.WorkspaceHubState.stopRealtime();
    state.selectedId = workspaceId;
    sessionStorage.setItem(window.WorkspaceHubState.getStorageKey(), workspaceId);

    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = workspaceId;
    }

    renderWorkspaceSummary(workspace);
    window.WorkspaceHubState.setUIState('selected');
    window.WorkspaceHubSmartInput.setEnabled(true);
    window.WorkspaceHubSmartInput.resetPrompt();
    window.WorkspaceHubSmartInput.setStatus('', { busy: false });
    window.WorkspaceHubTasks.loadTasks(workspaceId);
    window.WorkspaceHubSessions.loadSessions(workspaceId);
    window.WorkspaceHubNotes.loadNotes(workspaceId);
    window.WorkspaceHubFiles.loadFiles(workspaceId);

    if (elements.viewBoardBtn && elements.viewBoardBtn.classList.contains('is-active')) {
      if (window.WorkspaceHubBoard && typeof window.WorkspaceHubBoard.loadBoard === 'function') {
        window.WorkspaceHubBoard.loadBoard(workspaceId);
      }
    }

    if (window.workspaceRealtime && typeof window.workspaceRealtime.subscribeToWorkspace === 'function') {
      const unsub = window.workspaceRealtime.subscribeToWorkspace(workspaceId, (event) => {
        if (!event || event.workspaceId !== state.selectedId) return;
        if (window.WorkspaceHubState.shouldRefreshForEvent(event.type)) {
          scheduleWorkspaceTasksRefresh();
        }
        if (event.type === 'workspace.updated' && event.data && typeof event.data === 'object') {
          const action = event.data.action || '';
          if (action.startsWith('directory_')) {
            window.WorkspaceHubFiles.loadFiles(state.selectedId);
          }
        }
      });
      window.WorkspaceHubState.setRealtimeUnsub(unsub);
    }

    if (focus && elements.workspaceSelect) {
      elements.workspaceSelect.blur();
    }
  }

  /**
   * Show the launcher view
   */
  function showLauncher() {
    const state = window.WorkspaceHubState.getState();

    if (state.launcherSelectionMode) {
      setLauncherSelectionMode(false);
    }

    window.WorkspaceHubState.stopRealtime();
    const controller = window.WorkspaceHubState.getTasksAbortController();
    if (controller) {
      controller.abort();
      window.WorkspaceHubState.setTasksAbortController(null);
    }
    window.WorkspaceHubState.setUIState('launcher');
    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = '';
    }
    state.selectedId = null;
    window.WorkspaceHubSmartInput.setEnabled(false);
    window.WorkspaceHubSmartInput.resetPrompt();
    window.WorkspaceHubSmartInput.clearField();
    window.WorkspaceHubSmartInput.setStatus('Select a workspace to use quick add.', { busy: false });
    sessionStorage.removeItem(window.WorkspaceHubState.getStorageKey());
    clearWorkspaceSummary();
    window.WorkspaceHubTasks.renderStats({ completed: 0, in_progress: 0, failed: 0, scheduled: 0 });
    if (elements.tasksList) {
      elements.tasksList.innerHTML = '<div class="hub-empty">Select a workspace to view tasks.</div>';
    }
    if (elements.tasksSubtitle) {
      elements.tasksSubtitle.textContent = 'Select a workspace to see task activity.';
    }
    if (elements.schedulesList) {
      elements.schedulesList.innerHTML = '<div class="hub-empty">Select a workspace to view schedules.</div>';
    }
    if (elements.sessionsList) {
      elements.sessionsList.innerHTML = '<div class="hub-empty">Select a workspace to view sessions.</div>';
    }
    if (elements.notesList) {
      elements.notesList.innerHTML = '<div class="hub-empty">Select a workspace to view notes.</div>';
    }
    if (elements.copyNotesBtn) {
      elements.copyNotesBtn.disabled = true;
      elements.copyNotesBtn.title = 'No notes to copy';
    }
    if (elements.filesList) {
      elements.filesList.innerHTML = '<div class="hub-empty">Select a workspace to view files.</div>';
    }
    if (elements.directoriesList) {
      elements.directoriesList.innerHTML = '<div class="hub-empty">Select a workspace to view directories.</div>';
    }

    const flattened = flattenWorkspaces(state.workspaces || []).filter(isConcreteWorkspace);
    setLauncherTab(launcherActiveTab, { refreshSummary: false, force: true });
    void refreshLauncherTaskOverview(flattened);
  }

  /**
   * Load all workspaces
   */
  async function loadWorkspaces() {
    const state = window.WorkspaceHubState.getState();

    window.WorkspaceHubState.setUIState('loading');
    renderLauncherWorkspaceRootLoading();
    void loadLauncherWorkspaceRoot();

    try {
      const response = await fetch('/api/workspaces?tree=true');
      if (!response.ok) throw new Error('Failed to load workspaces');

      const data = await response.json();
      state.workspaces = data.folders || [];

      const flattened = flattenWorkspaces(state.workspaces);
      state.workspaceMap = new Map(flattened.map((workspace) => [workspace.id, workspace]));

      populateWorkspaceSelect(flattened);
      renderLauncher(flattened);

      // Always show the launcher - workspace details are now on separate pages
      showLauncher();
    } catch (error) {
      console.error('Workspace hub failed to load workspaces:', error);
      showLauncher();
    }
  }

  /**
   * Open schedule panel
   */
  function openSchedulePanel() {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedId) return;
    if (window.sessionManager && typeof window.sessionManager.openScheduledTasksPanel === 'function') {
      window.sessionManager.openScheduledTasksPanel(state.selectedId);
    }
  }

  /**
   * Open task creation modal
   */
  function openTaskModal() {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedId) return;
    if (window.taskModalController) {
      window.taskModalController.openForCreate(state.selectedId, '', () => window.WorkspaceHubTasks.loadTasks(state.selectedId));
    }
  }

  /**
   * Open import workflow modal
   */
  function openImportWorkflowModal() {
    const state = window.WorkspaceHubState.getState();

    if (!state.selectedId) {
      if (window.Toast) window.Toast.error('Please select a workspace first');
      return;
    }

    let modal = document.getElementById('hubImportWorkflowModal');
    if (!modal) {
      modal = document.createElement('div');
      modal.id = 'hubImportWorkflowModal';
      modal.className = 'modal fade';
      modal.tabIndex = -1;
      modal.innerHTML = `
        <div class="modal-dialog">
          <div class="modal-content">
            <div class="modal-header">
              <h5 class="modal-title" style="color: var(--text-primary);">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13.5,16V19H10.5V16H8L12,12L16,16H13.5M13,9V3.5L18.5,9H13Z"/>
                </svg>
                Import Workflow
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
            </div>
            <div class="modal-body">
              <p style="color: var(--text-secondary);">Select a workflow JSON file to import as tasks:</p>
              <div class="mb-3">
                <input type="file" id="hubImportWorkflowFile" class="form-control" accept=".json,application/json">
              </div>
              <div id="hubImportWorkflowInfo" style="display: none;" class="modern-card p-3 mb-3">
                <strong id="hubImportWorkflowName" style="color: var(--text-primary);"></strong>
                <p id="hubImportWorkflowSteps" class="mb-0 mt-1 small" style="color: var(--text-muted);"></p>
              </div>
              <div id="hubImportWorkflowError" class="alert alert-danger" style="display: none;"></div>
            </div>
            <div class="modal-footer">
              <button type="button" class="modern-btn modern-btn-secondary" data-bs-dismiss="modal">Cancel</button>
              <button type="button" id="hubConfirmImportWorkflowBtn" class="modern-btn modern-btn-primary" disabled>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13.5,16V19H10.5V16H8L12,12L16,16H13.5M13,9V3.5L18.5,9H13Z"/>
                </svg>
                Import
              </button>
            </div>
          </div>
        </div>
      `;
      document.body.appendChild(modal);

      const fileInput = modal.querySelector('#hubImportWorkflowFile');
      const confirmBtn = modal.querySelector('#hubConfirmImportWorkflowBtn');
      const infoEl = modal.querySelector('#hubImportWorkflowInfo');
      const nameEl = modal.querySelector('#hubImportWorkflowName');
      const stepsEl = modal.querySelector('#hubImportWorkflowSteps');
      const errorEl = modal.querySelector('#hubImportWorkflowError');

      let selectedWorkflowData = null;

      fileInput.addEventListener('change', (e) => {
        const file = e.target.files[0];
        if (!file) {
          selectedWorkflowData = null;
          infoEl.style.display = 'none';
          confirmBtn.disabled = true;
          return;
        }

        const reader = new FileReader();
        reader.onload = (event) => {
          try {
            const data = JSON.parse(event.target.result);
            selectedWorkflowData = data;
            nameEl.textContent = data.name || 'Unnamed Workflow';
            const stepCount = data.steps ? data.steps.length : 0;
            stepsEl.textContent = `${stepCount} step${stepCount !== 1 ? 's' : ''}`;
            infoEl.style.display = 'block';
            errorEl.style.display = 'none';
            confirmBtn.disabled = false;
          } catch (err) {
            selectedWorkflowData = null;
            errorEl.textContent = 'Invalid JSON file';
            errorEl.style.display = 'block';
            infoEl.style.display = 'none';
            confirmBtn.disabled = true;
          }
        };
        reader.readAsText(file);
      });

      confirmBtn.addEventListener('click', async () => {
        if (!selectedWorkflowData || !state.selectedId) return;

        confirmBtn.disabled = true;
        confirmBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Importing...';

        try {
          await importWorkflowAsTask(selectedWorkflowData);
          bootstrap.Modal.getInstance(modal).hide();
          if (window.Toast) window.Toast.success('Workflow imported as tasks');
          window.WorkspaceHubTasks.loadTasks(state.selectedId);
        } catch (err) {
          errorEl.textContent = 'Failed to import: ' + err.message;
          errorEl.style.display = 'block';
        } finally {
          confirmBtn.disabled = false;
          confirmBtn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13.5,16V19H10.5V16H8L12,12L16,16H13.5M13,9V3.5L18.5,9H13Z"/></svg> Import';
        }
      });
    }

    // Reset modal state
    const fileInput = modal.querySelector('#hubImportWorkflowFile');
    const infoEl = modal.querySelector('#hubImportWorkflowInfo');
    const errorEl = modal.querySelector('#hubImportWorkflowError');
    const confirmBtn = modal.querySelector('#hubConfirmImportWorkflowBtn');

    fileInput.value = '';
    infoEl.style.display = 'none';
    errorEl.style.display = 'none';
    confirmBtn.disabled = true;

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();
  }

  /**
   * Import a workflow as tasks
   * @param {Object} workflowData - Workflow data
   * @returns {Promise<string>} Parent task ID
   */
  async function importWorkflowAsTask(workflowData) {
    const state = window.WorkspaceHubState.getState();

    const parentTask = {
      workspace_id: state.selectedId,
      name: workflowData.name || 'Imported Workflow',
      description: workflowData.description || '',
      status: 'pending'
    };

    const parentResponse = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(parentTask)
    });

    if (!parentResponse.ok) {
      throw new Error('Failed to create workflow task');
    }

    const parentResult = await parentResponse.json();
    const parentId = parentResult.id;

    if (workflowData.steps && workflowData.steps.length > 0) {
      for (let i = 0; i < workflowData.steps.length; i++) {
        const step = workflowData.steps[i];
        const subtask = {
          workspace_id: state.selectedId,
          parent_id: parentId,
          name: step.name || `Step ${i + 1}`,
          description: step.description || '',
          to: step.assigned_to || 'unassigned',
          subtask_index: step.step_number || i + 1,
          status: 'pending'
        };

        const subtaskResponse = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(subtask)
        });

        if (!subtaskResponse.ok) {
          console.error('Failed to create subtask', step);
        }
      }
    }

    return parentId;
  }

  /**
   * Bind all event handlers
   */
  function bindEvents() {
    const state = window.WorkspaceHubState.getState();

    // Workspace selection via dropdown removed; use launcher cards instead.

    if (elements.workspaceBrowseBtn) {
      elements.workspaceBrowseBtn.addEventListener('click', () => showLauncher());
    }

    if (elements.workspaceMoveBtn) {
      elements.workspaceMoveBtn.addEventListener('click', () => openWorkspaceMoveModal());
    }

    if (elements.workspaceMoveSaveBtn) {
      elements.workspaceMoveSaveBtn.addEventListener('click', () => saveWorkspaceParent());
    }

    if (elements.addTaskBtn) {
      elements.addTaskBtn.addEventListener('click', openTaskModal);
    }

    if (elements.refreshTasksBtn) {
      elements.refreshTasksBtn.addEventListener('click', () => {
        if (state.selectedId) {
          window.WorkspaceHubTasks.loadTasks(state.selectedId);
        }
      });
    }

    if (elements.importWorkflowBtn) {
      elements.importWorkflowBtn.addEventListener('click', openImportWorkflowModal);
    }

    if (elements.viewSchedulesBtn) {
      elements.viewSchedulesBtn.addEventListener('click', openSchedulePanel);
    }

    if (elements.launcherRefreshBtn) {
      elements.launcherRefreshBtn.addEventListener('click', () => loadWorkspaces());
    }

    if (elements.launcherWorkspaceRootEditBtn) {
      elements.launcherWorkspaceRootEditBtn.addEventListener('click', () => {
        setLauncherWorkspaceRootEditorOpen(!launcherWorkspaceRootEditorOpen, { focusInput: !launcherWorkspaceRootEditorOpen });
      });
    }

    if (elements.launcherWorkspaceRootCancelBtn) {
      elements.launcherWorkspaceRootCancelBtn.addEventListener('click', () => {
        setLauncherWorkspaceRootEditorOpen(false);
      });
    }

    if (elements.launcherWorkspaceRootBrowseBtn) {
      elements.launcherWorkspaceRootBrowseBtn.addEventListener('click', async () => {
        setLauncherWorkspaceRootButtonLoading(elements.launcherWorkspaceRootBrowseBtn, true, 'Selecting...');
        try {
          await browseLauncherWorkspaceRoot();
        } catch (error) {
          console.error('Failed to browse workspace directory:', error);
          if (window.Toast) window.Toast.error('Failed to open folder picker: ' + error.message);
        } finally {
          setLauncherWorkspaceRootButtonLoading(elements.launcherWorkspaceRootBrowseBtn, false);
        }
      });
    }

    const handleWorkspaceRootSave = async () => {
      const nextValue = String(elements.launcherWorkspaceRootInput?.value || '').trim();
      setLauncherWorkspaceRootButtonLoading(elements.launcherWorkspaceRootSaveBtn, true, 'Saving...');
      try {
        await saveLauncherWorkspaceRoot(nextValue);
        setLauncherWorkspaceRootEditorOpen(false);
        if (window.Toast) window.Toast.success('Workspace directory saved.');
      } catch (error) {
        console.error('Failed to save workspace directory:', error);
        if (window.Toast) window.Toast.error('Failed to save workspace directory: ' + error.message);
      } finally {
        setLauncherWorkspaceRootButtonLoading(elements.launcherWorkspaceRootSaveBtn, false);
      }
    };

    if (elements.launcherWorkspaceRootSaveBtn) {
      elements.launcherWorkspaceRootSaveBtn.addEventListener('click', handleWorkspaceRootSave);
    }

    if (elements.launcherWorkspaceRootInput) {
      elements.launcherWorkspaceRootInput.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
          event.preventDefault();
          void handleWorkspaceRootSave();
        }
      });
    }

    if (elements.launcherWorkspaceRootResetBtn) {
      elements.launcherWorkspaceRootResetBtn.addEventListener('click', async () => {
        if (!String(launcherWorkspaceRootState?.workspace_root || '').trim()) {
          return;
        }

        setLauncherWorkspaceRootButtonLoading(elements.launcherWorkspaceRootResetBtn, true, 'Clearing...');
        try {
          await saveLauncherWorkspaceRoot('');
          setLauncherWorkspaceRootEditorOpen(false);
          if (window.Toast) window.Toast.success('Custom workspace directory cleared.');
        } catch (error) {
          console.error('Failed to clear workspace directory:', error);
          if (window.Toast) window.Toast.error('Failed to clear workspace directory: ' + error.message);
        } finally {
          setLauncherWorkspaceRootButtonLoading(elements.launcherWorkspaceRootResetBtn, false);
          syncLauncherWorkspaceRootEditorControls();
        }
      });
    }

    if (elements.launcherOverviewToggleBtn && elements.launcherOverviewDetails) {
      elements.launcherOverviewToggleBtn.addEventListener('click', () => {
        setLauncherOverviewExpanded(elements.launcherOverviewDetails.hidden);
      });
    }

    if (elements.launcherTabWorkspaces) {
      elements.launcherTabWorkspaces.addEventListener('click', () => setLauncherTab('workspaces'));
    }

    if (elements.launcherTabSummary) {
      elements.launcherTabSummary.addEventListener('click', () => setLauncherTab('summary'));
    }

    if (elements.launcherSelectModeBtn) {
      elements.launcherSelectModeBtn.addEventListener('click', () => {
        const state = window.WorkspaceHubState.getState();
        setLauncherSelectionMode(!state.launcherSelectionMode);
      });
    }

    if (elements.launcherCancelSelectionBtn) {
      elements.launcherCancelSelectionBtn.addEventListener('click', () => setLauncherSelectionMode(false));
    }

    if (elements.launcherGroupSelectedBtn && elements.launcherGroupModal) {
      elements.launcherGroupSelectedBtn.addEventListener('click', () => {
        if (typeof bootstrap === 'undefined' || !bootstrap.Modal) return;
        const modal = bootstrap.Modal.getInstance(elements.launcherGroupModal) || new bootstrap.Modal(elements.launcherGroupModal);
        if (elements.launcherGroupNameInput) elements.launcherGroupNameInput.value = '';
        if (elements.launcherGroupDescriptionInput) elements.launcherGroupDescriptionInput.value = '';
        modal.show();
      });
    }

    if (elements.launcherCreateGroupBtn) {
      elements.launcherCreateGroupBtn.addEventListener('click', () => createGroupFromSelection());
    }

    if (elements.launcherDeleteSelectedBtn) {
      elements.launcherDeleteSelectedBtn.addEventListener('click', () => deleteSelectedWorkspaces());
    }

    if (elements.launcherDeleteGroupOnlyBtn) {
      elements.launcherDeleteGroupOnlyBtn.addEventListener('click', () => handleDeleteGroupChoice(false));
    }

    if (elements.launcherDeleteGroupAllBtn) {
      elements.launcherDeleteGroupAllBtn.addEventListener('click', () => handleDeleteGroupChoice(true));
    }

    if (elements.newSessionBtn) {
      elements.newSessionBtn.addEventListener('click', () => {
        if (!state.selectedId) return;
        if (window.sessionManager && typeof window.sessionManager.createAssistantSession === 'function') {
          window.sessionManager.createAssistantSession(state.selectedId, 'Assistant');
        } else if (window.sessionManager && typeof window.sessionManager.showCreateChatModalForWorkspace === 'function') {
          window.sessionManager.showCreateChatModalForWorkspace(state.selectedId);
        }
      });
    }

    if (elements.newNoteBtn) {
      elements.newNoteBtn.addEventListener('click', window.WorkspaceHubNotes.createNewNote);
    }
    if (elements.copyNotesBtn) {
      elements.copyNotesBtn.addEventListener('click', window.WorkspaceHubNotes.copyAllNotesToClipboard);
    }

    if (elements.addFileBtn) {
      elements.addFileBtn.addEventListener('click', () => {
        if (state.selectedId) {
          window.WorkspaceHubFiles.openAddFileModal();
        }
      });
    }

    // Selection mode buttons
    if (elements.selectTasksBtn) {
      elements.selectTasksBtn.addEventListener('click', () =>
        window.WorkspaceHubSelection.toggleSelectionMode('tasks', () =>
          window.WorkspaceHubTasks.renderTasksList(state.tasks)));
    }
    if (elements.bulkDeleteTasksBtn) {
      elements.bulkDeleteTasksBtn.addEventListener('click', window.WorkspaceHubTasks.bulkDeleteTasks);
    }

    if (elements.selectSessionsBtn) {
      elements.selectSessionsBtn.addEventListener('click', () =>
        window.WorkspaceHubSelection.toggleSelectionMode('sessions', () =>
          window.WorkspaceHubSessions.renderSessions(state.sessions)));
    }
    if (elements.bulkDeleteSessionsBtn) {
      elements.bulkDeleteSessionsBtn.addEventListener('click', window.WorkspaceHubSessions.bulkDeleteSessions);
    }

    if (elements.selectNotesBtn) {
      elements.selectNotesBtn.addEventListener('click', () =>
        window.WorkspaceHubSelection.toggleSelectionMode('notes', () =>
          window.WorkspaceHubNotes.renderNotes(state.notes)));
    }
    if (elements.bulkDeleteNotesBtn) {
      elements.bulkDeleteNotesBtn.addEventListener('click', window.WorkspaceHubNotes.bulkDeleteNotes);
    }

    if (elements.selectFilesBtn) {
      elements.selectFilesBtn.addEventListener('click', () =>
        window.WorkspaceHubSelection.toggleSelectionMode('files', () =>
          window.WorkspaceHubFiles.renderFiles(state.files)));
    }
    if (elements.bulkTrashFilesBtn) {
      elements.bulkTrashFilesBtn.addEventListener('click', window.WorkspaceHubFiles.bulkMoveFilesToTrash);
    }

    // Initialize sub-module event bindings
    window.WorkspaceHubModals.bindModalEvents();
    window.WorkspaceHubSmartInput.bindEvents();
    window.WorkspaceHubFiles.bindFileUploadEvents();

    // Make workspace title and description editable
    makeEditable(elements.headerTitle, 'name', false);
    makeEditable(elements.workspaceDescription, 'description', true);
  }

  // Subscribe to global events
  console.log('[workspace-hub] EventBus available:', !!window.EventBus);
  if (window.EventBus) {
    console.log('[workspace-hub] Registering EventBus listeners');
    EventBus.on('workspace:files:updated', (data) => {
      const state = window.WorkspaceHubState.getState();
      if (!data?.workspaceId || data.workspaceId !== state.selectedId) return;
      window.WorkspaceHubFiles.loadFiles(state.selectedId);
    }, 'workspaceHub');

    // Refresh tasks when a task is created or updated
    EventBus.on('task:created', (data) => {
      const state = window.WorkspaceHubState.getState();
      if (hubEl.dataset.state === 'launcher') {
        scheduleLauncherOverviewRefresh();
      }
      if (!state.selectedId) return;
      if (!data?.workspaceId || data.workspaceId === state.selectedId) {
        window.WorkspaceHubTasks.loadTasks(state.selectedId);
      }
    }, 'workspaceHub');

    EventBus.on('task:updated', (data) => {
      const state = window.WorkspaceHubState.getState();
      if (hubEl.dataset.state === 'launcher') {
        scheduleLauncherOverviewRefresh();
      }
      if (!state.selectedId) return;
      if (!data?.workspaceId || data.workspaceId === state.selectedId) {
        window.WorkspaceHubTasks.loadTasks(state.selectedId);
      }
    }, 'workspaceHub');

    // Refresh sessions when a session is created
    EventBus.on('session:created', (data) => {
      const state = window.WorkspaceHubState.getState();
      if (!state.selectedId) return;
      if (!data?.folderId || data.folderId === state.selectedId) {
        window.WorkspaceHubSessions.loadSessions(state.selectedId);
      }
    }, 'workspaceHub');

    // Refresh notes when a note is created
    EventBus.on('note:created', (data) => {
      const state = window.WorkspaceHubState.getState();
      if (!state.selectedId) return;
      if (!data?.workspaceId || data.workspaceId === state.selectedId) {
        window.WorkspaceHubNotes.loadNotes(state.selectedId);
      }
    }, 'workspaceHub');

    // Refresh notes when a note is updated
    EventBus.on('note:updated', (data) => {
      const state = window.WorkspaceHubState.getState();
      console.log('[note:updated] received, selectedId:', state.selectedId, 'eventWorkspaceId:', data?.workspaceId);
      if (!state.selectedId) return;
      if (!data?.workspaceId || data.workspaceId === state.selectedId) {
        console.log('[note:updated] calling loadNotes');
        window.WorkspaceHubNotes.loadNotes(state.selectedId);
      }
    }, 'workspaceHub');
  }

  // Initialize
  bindEvents();
  setLauncherWorkspaceRootEditorOpen(false);
  initLauncherOverviewExpandedState();
  initLauncherTabState();

  // Keyboard shortcuts for workspace selection
  document.addEventListener('keydown', (e) => {
    // Cmd/Ctrl+G to group selected workspaces
    if ((e.metaKey || e.ctrlKey) && e.key === 'g') {
      const state = window.WorkspaceHubState.getState();
      if (state.launcherSelectionMode && state.selectedWorkspaces && state.selectedWorkspaces.size > 0) {
        e.preventDefault();
        // Open group modal
        if (elements.launcherGroupModal && typeof bootstrap !== 'undefined' && bootstrap.Modal) {
          const modal = bootstrap.Modal.getInstance(elements.launcherGroupModal) || new bootstrap.Modal(elements.launcherGroupModal);
          if (elements.launcherGroupNameInput) elements.launcherGroupNameInput.value = '';
          if (elements.launcherGroupDescriptionInput) elements.launcherGroupDescriptionInput.value = '';
          modal.show();
        }
      }
    }

    // Escape to exit selection mode
    if (e.key === 'Escape') {
      const state = window.WorkspaceHubState.getState();
      if (state.launcherSelectionMode) {
        e.preventDefault();
        setLauncherSelectionMode(false);
      }
    }
  });

  window.WorkspaceHub = window.WorkspaceHub || {};
  window.WorkspaceHub.loadWorkspaces = loadWorkspaces;

  if (!hubEl.dataset.launcherSelect) {
    hubEl.dataset.launcherSelect = 'false';
  }

  loadWorkspaces();
})();
