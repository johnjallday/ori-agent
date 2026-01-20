/**
 * Workspace Hub Panel Expansion
 * Handles expanding smaller panels to a centered overlay view.
 *
 * @module workspace-hub-panels
 */
(function() {
  'use strict';

  let expandedPanel = null;
  let backdrop = null;

  /**
   * Initialize panel expansion functionality
   */
  function init() {
    backdrop = document.getElementById('hubPanelBackdrop');
    if (!backdrop) return;

    const expandablePanels = document.querySelectorAll('.hub-panel.is-expandable');

    expandablePanels.forEach((panel) => {
      // Click on panel to expand
      panel.addEventListener('click', (event) => {
        // Don't expand if clicking on interactive elements
        if (event.target.closest('button, a, input, select, textarea, .hub-item-checkbox')) {
          return;
        }

        // Don't expand if already expanded
        if (panel.classList.contains('is-expanded')) {
          return;
        }

        expandPanel(panel);
      });

      // Close button
      const closeBtn = panel.querySelector('.hub-panel-close');
      if (closeBtn) {
        closeBtn.addEventListener('click', (event) => {
          event.stopPropagation();
          collapsePanel();
        });
      }
    });

    // Click backdrop to close
    backdrop.addEventListener('click', collapsePanel);

    // Escape key to close
    document.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && expandedPanel) {
        collapsePanel();
      }
    });
  }

  /**
   * Expand a panel to center overlay
   * @param {HTMLElement} panel - Panel element to expand
   */
  function expandPanel(panel) {
    if (expandedPanel) {
      collapsePanel();
    }

    expandedPanel = panel;
    panel.classList.add('is-expanded');
    backdrop.classList.add('is-active');
    document.body.style.overflow = 'hidden';
  }

  /**
   * Collapse the currently expanded panel
   */
  function collapsePanel() {
    if (!expandedPanel) return;

    expandedPanel.classList.remove('is-expanded');
    backdrop.classList.remove('is-active');
    document.body.style.overflow = '';
    expandedPanel = null;
  }

  /**
   * Check if a panel is currently expanded
   * @returns {boolean}
   */
  function isExpanded() {
    return expandedPanel !== null;
  }

  /**
   * Get the currently expanded panel
   * @returns {HTMLElement|null}
   */
  function getExpandedPanel() {
    return expandedPanel;
  }

  // Initialize when DOM is ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  // Expose panel manager globally
  window.WorkspaceHubPanels = {
    expandPanel,
    collapsePanel,
    isExpanded,
    getExpandedPanel
  };
})();
