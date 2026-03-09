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
  let executionConfirmResolve = null;

  function resetExecutionConfirmModal() {
    const elements = window.WorkspaceHubState.getElements();
    if (elements.executionConfirmEyebrow) {
      elements.executionConfirmEyebrow.textContent = 'Execution Check';
    }
    if (elements.executionConfirmTitle) {
      elements.executionConfirmTitle.textContent = 'Confirm this action';
    }
    if (elements.executionConfirmMessage) {
      elements.executionConfirmMessage.textContent = '';
    }
    if (elements.executionConfirmCancelBtn) {
      elements.executionConfirmCancelBtn.textContent = 'Cancel';
    }
    if (elements.executionConfirmConfirmBtn) {
      elements.executionConfirmConfirmBtn.textContent = 'Continue';
    }
    if (elements.executionConfirmMeta) {
      elements.executionConfirmMeta.innerHTML = '';
    }
    if (elements.executionConfirmDetails) {
      elements.executionConfirmDetails.innerHTML = '';
      elements.executionConfirmDetails.classList.add('d-none');
    }
  }

  function renderExecutionConfirmMeta(items) {
    const elements = window.WorkspaceHubState.getElements();
    if (!elements.executionConfirmMeta) return;

    elements.executionConfirmMeta.innerHTML = '';
    (Array.isArray(items) ? items : [])
      .map((item) => String(item || '').trim())
      .filter(Boolean)
      .forEach((item) => {
        const chip = document.createElement('span');
        chip.className = 'hub-execution-confirm-chip';
        chip.textContent = item;
        elements.executionConfirmMeta.appendChild(chip);
      });
  }

  function renderExecutionConfirmDetails(items) {
    const elements = window.WorkspaceHubState.getElements();
    if (!elements.executionConfirmDetails) return;

    const normalizedItems = (Array.isArray(items) ? items : [])
      .map((item) => String(item || '').trim())
      .filter(Boolean);

    elements.executionConfirmDetails.innerHTML = '';
    if (normalizedItems.length === 0) {
      elements.executionConfirmDetails.classList.add('d-none');
      return;
    }

    normalizedItems.forEach((item, index) => {
      const row = document.createElement('div');
      row.className = 'hub-execution-confirm-detail';

      const badge = document.createElement('span');
      badge.className = 'hub-execution-confirm-detail-index';
      badge.textContent = String(index + 1);

      const text = document.createElement('div');
      text.className = 'hub-execution-confirm-detail-text';
      text.textContent = item;

      row.appendChild(badge);
      row.appendChild(text);
      elements.executionConfirmDetails.appendChild(row);
    });

    elements.executionConfirmDetails.classList.remove('d-none');
  }

  function getExecutionConfirmModal() {
    const elements = window.WorkspaceHubState.getElements();
    if (!elements.executionConfirmModal || !window.bootstrap) return null;
    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(elements.executionConfirmModal)
      : (bootstrap.Modal.getInstance(elements.executionConfirmModal) || new bootstrap.Modal(elements.executionConfirmModal));
  }

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

  function showExecutionConfirm(options) {
    const elements = window.WorkspaceHubState.getElements();
    const title = String(options?.title || 'Confirm this action').trim();
    const message = String(options?.message || '').trim();
    const eyebrow = String(options?.eyebrow || 'Execution Check').trim();
    const confirmLabel = String(options?.confirmLabel || 'Continue').trim();
    const cancelLabel = String(options?.cancelLabel || 'Cancel').trim();
    const metaItems = Array.isArray(options?.metaItems) ? options.metaItems : [];
    const details = Array.isArray(options?.details) ? options.details : [];

    if (!elements.executionConfirmModal || !window.bootstrap) {
      const fallbackText = [message, ...details].filter(Boolean).join('\n\n');
      return Promise.resolve(window.confirm([title, fallbackText].filter(Boolean).join('\n\n')));
    }

    if (executionConfirmResolve) {
      executionConfirmResolve(false);
      executionConfirmResolve = null;
    }

    resetExecutionConfirmModal();

    if (elements.executionConfirmEyebrow) {
      elements.executionConfirmEyebrow.textContent = eyebrow || 'Execution Check';
    }
    if (elements.executionConfirmTitle) {
      elements.executionConfirmTitle.textContent = title;
    }
    if (elements.executionConfirmMessage) {
      elements.executionConfirmMessage.textContent = message;
    }
    if (elements.executionConfirmCancelBtn) {
      elements.executionConfirmCancelBtn.textContent = cancelLabel || 'Cancel';
    }
    if (elements.executionConfirmConfirmBtn) {
      elements.executionConfirmConfirmBtn.textContent = confirmLabel || 'Continue';
    }

    renderExecutionConfirmMeta(metaItems);
    renderExecutionConfirmDetails(details);

    return new Promise((resolve) => {
      executionConfirmResolve = resolve;
      const modal = getExecutionConfirmModal();
      modal?.show();
      window.setTimeout(() => {
        elements.executionConfirmConfirmBtn?.focus();
      }, 120);
    });
  }

  function handleExecutionConfirm(confirmed) {
    if (executionConfirmResolve) {
      const resolve = executionConfirmResolve;
      executionConfirmResolve = null;
      resolve(Boolean(confirmed));
    }
    const modal = getExecutionConfirmModal();
    if (modal) modal.hide();
  }

  function handleExecutionConfirmCancel() {
    handleExecutionConfirm(false);
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

    if (elements.executionConfirmCancelBtn) {
      elements.executionConfirmCancelBtn.addEventListener('click', handleExecutionConfirmCancel);
    }
    if (elements.executionConfirmConfirmBtn) {
      elements.executionConfirmConfirmBtn.addEventListener('click', () => handleExecutionConfirm(true));
    }
    if (elements.executionConfirmModal) {
      elements.executionConfirmModal.addEventListener('hidden.bs.modal', () => {
        if (executionConfirmResolve) {
          const resolve = executionConfirmResolve;
          executionConfirmResolve = null;
          resolve(false);
        }
        resetExecutionConfirmModal();
      });
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
    showExecutionConfirm,
    bindModalEvents
  };
})();
