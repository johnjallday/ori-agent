/**
 * Workspace Hub Sessions Management
 * Handles chat session CRUD operations and rendering.
 *
 * @module workspace-hub-sessions
 */
(function() {
  'use strict';

  const { formatDate } = window.WorkspaceHubUtils;

  /**
   * Delete a session
   * @param {string} sessionId - Session ID to delete
   */
  async function deleteSession(sessionId) {
    const state = window.WorkspaceHubState.getState();

    const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
      title: 'Delete Chat Session',
      message: 'Are you sure you want to delete this chat session and all its messages? This action cannot be undone.'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete session');

      if (window.Toast) window.Toast.success('Session deleted');
      await loadSessions(state.selectedId);
    } catch (error) {
      console.error('Failed to delete session:', error);
      if (window.Toast) window.Toast.error('Failed to delete session');
    }
  }

  /**
   * Bulk delete sessions
   */
  async function bulkDeleteSessions() {
    const state = window.WorkspaceHubState.getState();
    const ids = Array.from(state.selectedItems.sessions);
    if (ids.length === 0) return;

    const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
      title: `Delete ${ids.length} Session${ids.length > 1 ? 's' : ''}`,
      message: `Are you sure you want to delete ${ids.length} chat session${ids.length > 1 ? 's' : ''} and all their messages? This action cannot be undone.`
    });
    if (!confirmed) return;

    try {
      const response = await fetch('/api/sessions/bulk', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_ids: ids })
      });
      if (!response.ok) throw new Error('Failed to delete sessions');

      const result = await response.json();
      if (window.Toast) {
        window.Toast.success(`Deleted ${result.success_count} session${result.success_count !== 1 ? 's' : ''}`);
      }

      window.WorkspaceHubSelection.toggleSelectionMode('sessions', () => renderSessions(state.sessions));
      await loadSessions(state.selectedId);
    } catch (error) {
      console.error('Failed to bulk delete sessions:', error);
      if (window.Toast) window.Toast.error('Failed to delete sessions');
    }
  }

  /**
   * Open a session
   * @param {string} sessionId - Session ID to open
   */
  function openSession(sessionId) {
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }

    if (window.sessionManager && typeof window.sessionManager.switchToSession === 'function') {
      window.sessionManager.switchToSession(sessionId);
    }
  }

  /**
   * Load sessions for a workspace
   * @param {string} workspaceId - Workspace ID
   */
  async function loadSessions(workspaceId) {
    if (!workspaceId) return;

    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (elements.sessionsList) {
      elements.sessionsList.innerHTML = '<div class="hub-loading">Loading sessions...</div>';
    }

    try {
      const response = await fetch(`/api/sessions?folder_id=${encodeURIComponent(workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load sessions');

      const data = await response.json();
      state.sessions = data.sessions || [];
      renderSessions(state.sessions);
    } catch (error) {
      console.error('Workspace hub failed to load sessions:', error);
      if (elements.sessionsList) {
        elements.sessionsList.innerHTML = '<div class="hub-empty">Unable to load sessions.</div>';
      }
    }
  }

  /**
   * Render sessions list
   * @param {Array} sessions - Sessions array
   */
  function renderSessions(sessions) {
    const elements = window.WorkspaceHubState.getElements();
    const state = window.WorkspaceHubState.getState();

    if (!elements.sessionsList) return;

    if (!sessions || sessions.length === 0) {
      elements.sessionsList.innerHTML = '<div class="hub-empty">No chat sessions yet.</div>';
      return;
    }

    const inSelectionMode = window.WorkspaceHubSelection.isSelectionModeEnabled('sessions');
    const selectedSet = state.selectedItems.sessions;

    const items = sessions.map((session) => {
      const title = session.title || session.name || 'Untitled Chat';
      const agent = session.agent_name || 'default';
      const updated = formatDate(session.updated_at || session.created_at);
      const messageCount = session.message_count || 0;
      const isSelected = selectedSet.has(session.id);

      return `
        <div class="hub-session-item${isSelected ? ' selected' : ''}" data-session-id="${escapeHtml(session.id)}">
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select session">
          </div>
          <div class="hub-session-info">
            <div class="hub-session-title">${escapeHtml(title)}</div>
            <div class="hub-session-meta">
              <span class="hub-session-agent">${escapeHtml(agent)}</span>
              <span>${messageCount} message${messageCount === 1 ? '' : 's'}</span>
              <span>${escapeHtml(updated)}</span>
            </div>
          </div>
          <button class="modern-btn modern-btn-secondary hub-session-open" data-action="open">Open</button>
          <button class="hub-item-delete-btn" data-action="delete" title="Delete session">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    elements.sessionsList.innerHTML = items.join('');
    bindSessionEvents();
  }

  /**
   * Bind event handlers to session items
   */
  function bindSessionEvents() {
    const elements = window.WorkspaceHubState.getElements();
    const inSelectionMode = window.WorkspaceHubSelection.isSelectionModeEnabled('sessions');

    elements.sessionsList.querySelectorAll('.hub-session-item').forEach((item) => {
      const sessionId = item.dataset.sessionId;

      const checkbox = item.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.addEventListener('change', (event) => {
          event.stopPropagation();
          window.WorkspaceHubSelection.toggleItemSelection('sessions', sessionId);
        });
      }

      item.querySelector('[data-action="delete"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        deleteSession(sessionId);
      });

      item.querySelector('[data-action="open"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        openSession(sessionId);
      });

      item.addEventListener('click', (event) => {
        if (inSelectionMode && !event.target.closest('button') && !event.target.closest('input')) {
          window.WorkspaceHubSelection.toggleItemSelection('sessions', sessionId);
        } else if (!inSelectionMode && !event.target.closest('button')) {
          openSession(sessionId);
        }
      });
    });
  }

  // Expose sessions manager globally
  window.WorkspaceHubSessions = {
    deleteSession,
    bulkDeleteSessions,
    openSession,
    loadSessions,
    renderSessions
  };
})();
