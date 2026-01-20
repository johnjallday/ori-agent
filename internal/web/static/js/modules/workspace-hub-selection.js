/**
 * Workspace Hub Selection Mode
 * Handles multi-select mode for bulk operations on tasks, sessions, notes, and files.
 *
 * @module workspace-hub-selection
 */
(function() {
  'use strict';

  /**
   * Toggle selection mode for a panel type
   * @param {string} panelType - 'tasks', 'sessions', 'notes', or 'files'
   * @param {Function} renderCallback - Function to re-render the panel
   */
  function toggleSelectionMode(panelType, renderCallback) {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    const isEnabled = !state.selectionMode[panelType];
    state.selectionMode[panelType] = isEnabled;
    state.selectedItems[panelType].clear();

    const panelMap = {
      tasks: elements.tasksPanel,
      sessions: elements.sessionsPanel,
      notes: elements.notesPanel,
      files: elements.filesPanel
    };

    const panel = panelMap[panelType];
    if (panel) {
      panel.classList.toggle('selection-mode', isEnabled);
    }

    updateBulkActionsVisibility(panelType);

    if (renderCallback) {
      renderCallback();
    }
  }

  /**
   * Toggle item selection
   * @param {string} panelType - 'tasks', 'sessions', 'notes', or 'files'
   * @param {string} itemId - Item ID to toggle
   */
  function toggleItemSelection(panelType, itemId) {
    const state = window.WorkspaceHubState.getState();
    const selectedSet = state.selectedItems[panelType];

    if (selectedSet.has(itemId)) {
      selectedSet.delete(itemId);
    } else {
      selectedSet.add(itemId);
    }

    // Update UI for selected item
    const selector = {
      tasks: '.hub-task-card',
      sessions: '.hub-session-item',
      notes: '.hub-note-item',
      files: '.hub-file-item'
    }[panelType];

    const idAttr = {
      tasks: 'data-task-id',
      sessions: 'data-session-id',
      notes: 'data-note-id',
      files: 'data-file-id'
    }[panelType];

    const item = document.querySelector(`${selector}[${idAttr}="${itemId}"]`);
    if (item) {
      item.classList.toggle('selected', selectedSet.has(itemId));
      const checkbox = item.querySelector('input[type="checkbox"]');
      if (checkbox) checkbox.checked = selectedSet.has(itemId);
    }

    updateBulkActionsVisibility(panelType);
  }

  /**
   * Update bulk action button visibility
   * @param {string} panelType - Panel type
   */
  function updateBulkActionsVisibility(panelType) {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    const count = state.selectedItems[panelType].size;
    const btnMap = {
      tasks: elements.bulkDeleteTasksBtn,
      sessions: elements.bulkDeleteSessionsBtn,
      notes: elements.bulkDeleteNotesBtn,
      files: elements.bulkTrashFilesBtn
    };

    const btn = btnMap[panelType];
    if (btn) {
      btn.style.display = count > 0 ? 'inline-flex' : 'none';
    }
  }

  /**
   * Check if selection mode is enabled for a panel type
   * @param {string} panelType - Panel type
   * @returns {boolean} True if selection mode is enabled
   */
  function isSelectionModeEnabled(panelType) {
    const state = window.WorkspaceHubState.getState();
    return state.selectionMode[panelType];
  }

  /**
   * Get selected item IDs for a panel type
   * @param {string} panelType - Panel type
   * @returns {Array} Array of selected IDs
   */
  function getSelectedIds(panelType) {
    const state = window.WorkspaceHubState.getState();
    return Array.from(state.selectedItems[panelType]);
  }

  /**
   * Check if an item is selected
   * @param {string} panelType - Panel type
   * @param {string} itemId - Item ID
   * @returns {boolean} True if selected
   */
  function isItemSelected(panelType, itemId) {
    const state = window.WorkspaceHubState.getState();
    return state.selectedItems[panelType].has(itemId);
  }

  // Expose selection manager globally
  window.WorkspaceHubSelection = {
    toggleSelectionMode,
    toggleItemSelection,
    updateBulkActionsVisibility,
    isSelectionModeEnabled,
    getSelectedIds,
    isItemSelected
  };
})();
