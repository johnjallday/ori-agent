/**
 * Workspace Hub State Management
 * Centralized state container for the workspace hub.
 *
 * @module workspace-hub-state
 */
(function() {
  'use strict';

  const STORAGE_KEY = 'oriWorkspaceHubSelectedId';

  /**
   * DOM element references - populated by init()
   */
  const elements = {};

  /**
   * Central state object
   */
  const state = {
    workspaces: [],
    workspaceMap: new Map(),
    selectedId: null,
    launcherSelectionMode: false,
    selectedWorkspaces: new Set(),
    launcherCollapsedGroups: new Set(),
    tasks: [],
    board: {
      workspaceId: null,
      config: null,
      columns: [],
      tasks: [],
      scope: 'workspace',
      isLoading: false
    },
    stats: null,
    taskHierarchy: null,
    sessions: [],
    notes: [],
    files: [],
    directories: [],
    smartInput: null,
    smartInputCancelled: false,
    selectionMode: { tasks: false, sessions: false, notes: false, files: false },
    selectedItems: { tasks: new Set(), sessions: new Set(), notes: new Set(), files: new Set() },
    pendingFiles: []
  };

  /**
   * Realtime subscription state
   */
  let workspaceRealtimeUnsub = null;
  let workspaceRealtimeTimer = null;
  let tasksAbortController = null;

  /**
   * Initialize DOM element references
   * @param {Object} elementMap - Map of element IDs to cache
   */
  function initElements(elementMap) {
    Object.assign(elements, elementMap);
  }

  /**
   * Get DOM elements
   * @returns {Object} Element references
   */
  function getElements() {
    return elements;
  }

  /**
   * Get current state
   * @returns {Object} State object
   */
  function getState() {
    return state;
  }

  /**
   * Get storage key
   * @returns {string} Storage key for selected workspace ID
   */
  function getStorageKey() {
    return STORAGE_KEY;
  }

  /**
   * Set UI state (loading, launcher, selected)
   * @param {string} nextState - State name
   */
  function setUIState(nextState) {
    const hubEl = document.getElementById('workspaceHub');
    if (hubEl) {
      hubEl.dataset.state = nextState;
    }
    if (elements.loadingOverlay) {
      elements.loadingOverlay.style.display = nextState === 'loading' ? 'flex' : 'none';
    }
  }

  /**
   * Get realtime subscription unsub function
   * @returns {Function|null} Unsubscribe function
   */
  function getRealtimeUnsub() {
    return workspaceRealtimeUnsub;
  }

  /**
   * Set realtime subscription unsub function
   * @param {Function|null} unsub - Unsubscribe function
   */
  function setRealtimeUnsub(unsub) {
    workspaceRealtimeUnsub = unsub;
  }

  /**
   * Get realtime timer ID
   * @returns {number|null} Timer ID
   */
  function getRealtimeTimer() {
    return workspaceRealtimeTimer;
  }

  /**
   * Set realtime timer ID
   * @param {number|null} timer - Timer ID
   */
  function setRealtimeTimer(timer) {
    workspaceRealtimeTimer = timer;
  }

  /**
   * Get tasks abort controller
   * @returns {AbortController|null} Abort controller
   */
  function getTasksAbortController() {
    return tasksAbortController;
  }

  /**
   * Set tasks abort controller
   * @param {AbortController|null} controller - Abort controller
   */
  function setTasksAbortController(controller) {
    tasksAbortController = controller;
  }

  /**
   * Stop realtime subscription and timers
   */
  function stopRealtime() {
    if (workspaceRealtimeUnsub) {
      workspaceRealtimeUnsub();
      workspaceRealtimeUnsub = null;
    }
    if (workspaceRealtimeTimer) {
      clearTimeout(workspaceRealtimeTimer);
      workspaceRealtimeTimer = null;
    }
  }

  /**
   * Check if an event type should trigger a refresh
   * @param {string} type - Event type
   * @returns {boolean} Whether to refresh
   */
  function shouldRefreshForEvent(type) {
    if (!type) return false;
    if (type === 'workspace.status') return false;
    return type.startsWith('task.') ||
      type.startsWith('workflow.') ||
      type.startsWith('step.') ||
      type === 'workspace.updated' ||
      type === 'workspace.completed';
  }

  // Expose state manager globally
  window.WorkspaceHubState = {
    initElements,
    getElements,
    getState,
    getStorageKey,
    setUIState,
    getRealtimeUnsub,
    setRealtimeUnsub,
    getRealtimeTimer,
    setRealtimeTimer,
    getTasksAbortController,
    setTasksAbortController,
    stopRealtime,
    shouldRefreshForEvent
  };
})();
