/**
 * Workspace Hub Modal Management
 * Handles delete confirmation and parent task deletion modals.
 *
 * @module workspace-hub-modals
 */
(function() {
  'use strict';

  let deleteConfirmResolve = null;
  let parentDeleteResolve = null;

  /**
   * Show delete confirmation modal
   * @param {Object} options - Modal options
   * @param {string} options.title - Modal title
   * @param {string} options.message - Modal message
   * @param {string} options.variant - 'trash' for move to trash styling
   * @returns {Promise<boolean>} Resolves to true if confirmed
   */
  function showDeleteConfirm(options) {
    const elements = window.WorkspaceHubState.getElements();

    return new Promise((resolve) => {
      deleteConfirmResolve = resolve;

      if (elements.deleteConfirmTitle) {
        elements.deleteConfirmTitle.textContent = options.title || 'Confirm Delete';
      }
      if (elements.deleteConfirmBody) {
        elements.deleteConfirmBody.textContent = options.message || 'Are you sure you want to delete the selected items?';
      }
      if (elements.deleteConfirmBtn) {
        elements.deleteConfirmBtn.textContent = options.variant === 'trash' ? 'Move to Trash' : 'Delete';
      }

      if (elements.deleteConfirmModal && window.bootstrap) {
        const modal = new bootstrap.Modal(elements.deleteConfirmModal);
        modal.show();
      }
    });
  }

  /**
   * Handle delete confirmation
   */
  function handleDeleteConfirm() {
    const elements = window.WorkspaceHubState.getElements();

    if (deleteConfirmResolve) {
      deleteConfirmResolve(true);
      deleteConfirmResolve = null;
    }
    if (elements.deleteConfirmModal && window.bootstrap) {
      const modal = bootstrap.Modal.getInstance(elements.deleteConfirmModal);
      if (modal) modal.hide();
    }
  }

  /**
   * Handle delete cancel
   */
  function handleDeleteCancel() {
    if (deleteConfirmResolve) {
      deleteConfirmResolve(false);
      deleteConfirmResolve = null;
    }
  }

  /**
   * Show parent task delete prompt
   * @param {Object} options - Modal options
   * @param {string} options.title - Modal title
   * @param {string} options.message - Modal message
   * @returns {Promise<string|null>} Resolves to 'ungroup', 'delete_all', or null
   */
  function showParentDeletePrompt(options) {
    const elements = window.WorkspaceHubState.getElements();

    return new Promise((resolve) => {
      parentDeleteResolve = resolve;

      if (elements.parentDeleteTitle) {
        elements.parentDeleteTitle.textContent = options.title || 'Delete Workflow';
      }
      if (elements.parentDeleteBody) {
        elements.parentDeleteBody.textContent = options.message || 'This workflow has subtasks. What would you like to do?';
      }

      if (elements.parentDeleteModal && window.bootstrap) {
        const modal = new bootstrap.Modal(elements.parentDeleteModal);
        modal.show();
      }
    });
  }

  /**
   * Handle parent delete choice
   * @param {string} choice - 'ungroup' or 'delete_all'
   */
  function handleParentDeleteChoice(choice) {
    const elements = window.WorkspaceHubState.getElements();

    if (parentDeleteResolve) {
      parentDeleteResolve(choice);
      parentDeleteResolve = null;
    }
    if (elements.parentDeleteModal && window.bootstrap) {
      const modal = bootstrap.Modal.getInstance(elements.parentDeleteModal);
      if (modal) modal.hide();
    }
  }

  /**
   * Handle parent delete cancel
   */
  function handleParentDeleteCancel() {
    if (parentDeleteResolve) {
      parentDeleteResolve(null);
      parentDeleteResolve = null;
    }
  }

  /**
   * Bind modal event handlers
   */
  function bindModalEvents() {
    const elements = window.WorkspaceHubState.getElements();

    if (elements.deleteConfirmBtn) {
      elements.deleteConfirmBtn.addEventListener('click', handleDeleteConfirm);
    }
    if (elements.deleteConfirmModal) {
      elements.deleteConfirmModal.addEventListener('hidden.bs.modal', handleDeleteCancel);
    }

    if (elements.parentDeleteUngroupBtn) {
      elements.parentDeleteUngroupBtn.addEventListener('click', () => handleParentDeleteChoice('ungroup'));
    }
    if (elements.parentDeleteAllBtn) {
      elements.parentDeleteAllBtn.addEventListener('click', () => handleParentDeleteChoice('delete_all'));
    }
    if (elements.parentDeleteModal) {
      elements.parentDeleteModal.addEventListener('hidden.bs.modal', handleParentDeleteCancel);
    }
  }

  // Expose modals manager globally
  window.WorkspaceHubModals = {
    showDeleteConfirm,
    handleDeleteConfirm,
    handleDeleteCancel,
    showParentDeletePrompt,
    handleParentDeleteChoice,
    handleParentDeleteCancel,
    bindModalEvents
  };
})();
