/**
 * Workspace Hub Notes Management
 * Handles note CRUD operations and rendering.
 *
 * @module workspace-hub-notes
 */
(function() {
  'use strict';

  const { formatDate } = window.WorkspaceHubUtils;

  /**
   * Delete a note
   * @param {string} noteId - Note ID to delete
   */
  async function deleteNote(noteId) {
    const state = window.WorkspaceHubState.getState();

    const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
      title: 'Delete Note',
      message: 'Are you sure you want to delete this note? This action cannot be undone.'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/notes/${encodeURIComponent(noteId)}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete note');

      if (window.Toast) window.Toast.success('Note deleted');
      await loadNotes(state.selectedId);
    } catch (error) {
      console.error('Failed to delete note:', error);
      if (window.Toast) window.Toast.error('Failed to delete note');
    }
  }

  /**
   * Bulk delete notes
   */
  async function bulkDeleteNotes() {
    const state = window.WorkspaceHubState.getState();
    const ids = Array.from(state.selectedItems.notes);
    if (ids.length === 0) return;

    const confirmed = await window.WorkspaceHubModals.showDeleteConfirm({
      title: `Delete ${ids.length} Note${ids.length > 1 ? 's' : ''}`,
      message: `Are you sure you want to delete ${ids.length} note${ids.length > 1 ? 's' : ''}? This action cannot be undone.`
    });
    if (!confirmed) return;

    try {
      const response = await fetch('/api/notes/bulk', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ note_ids: ids })
      });
      if (!response.ok) throw new Error('Failed to delete notes');

      const result = await response.json();
      if (window.Toast) {
        window.Toast.success(`Deleted ${result.success_count} note${result.success_count !== 1 ? 's' : ''}`);
      }

      window.WorkspaceHubSelection.toggleSelectionMode('notes', () => renderNotes(state.notes));
      await loadNotes(state.selectedId);
    } catch (error) {
      console.error('Failed to bulk delete notes:', error);
      if (window.Toast) window.Toast.error('Failed to delete notes');
    }
  }

  /**
   * Open a note for editing
   * @param {string} noteId - Note ID to open
   */
  function openNote(noteId) {
    // Use the existing openNoteEditor which takes an ID and fetches the note
    if (window.sessionManager && typeof window.sessionManager.openNoteEditor === 'function') {
      window.sessionManager.openNoteEditor(noteId);
    }
  }

  /**
   * Create a new note
   */
  function createNewNote() {
    const state = window.WorkspaceHubState.getState();

    if (!state.selectedId) return;
    if (window.sessionManager && typeof window.sessionManager.openNoteCreateModal === 'function') {
      window.sessionManager.openNoteCreateModal(state.selectedId);
    }
  }

  /**
   * Create a quick note from content
   * @param {string} content - Note content
   */
  async function createQuickNote(content) {
    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (!state.selectedId) return;

    window.WorkspaceHubSmartInput?.setBusy(true, 'Creating note...');

    try {
      const firstLine = content.split('\n')[0];
      const title = firstLine.length > 50 ? firstLine.slice(0, 47) + '...' : firstLine;

      const response = await fetch(`/api/workspaces/${state.selectedId}/notes`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: title || 'Quick Note',
          content: content
        })
      });

      if (!response.ok) {
        throw new Error('Failed to create note');
      }

      if (elements.smartInputField) {
        elements.smartInputField.value = '';
      }
      window.WorkspaceHubSmartInput?.setBusy(false);
      window.WorkspaceHubSmartInput?.setStatus('');

      if (window.Toast) {
        window.Toast.success('Note created');
      }

      await loadNotes(state.selectedId);
    } catch (error) {
      console.error('Failed to create quick note:', error);
      window.WorkspaceHubSmartInput?.setBusy(false);
      window.WorkspaceHubSmartInput?.setStatus('Failed to create note', { busy: false });
      if (window.Toast) {
        window.Toast.error('Failed to create note');
      }
    }
  }

  /**
   * Load notes for a workspace
   * @param {string} workspaceId - Workspace ID
   */
  async function loadNotes(workspaceId) {
    console.log('[loadNotes] called with workspaceId:', workspaceId);
    if (!workspaceId) return;

    const state = window.WorkspaceHubState.getState();
    const elements = window.WorkspaceHubState.getElements();

    if (elements.notesList) {
      elements.notesList.innerHTML = '<div class="hub-loading">Loading notes...</div>';
    }

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`);
      if (!response.ok) throw new Error('Failed to load notes');

      const data = await response.json();
      console.log('[loadNotes] API returned:', data.notes);
      state.notes = data.notes || [];
      renderNotes(state.notes);
    } catch (error) {
      console.error('Workspace hub failed to load notes:', error);
      if (elements.notesList) {
        elements.notesList.innerHTML = '<div class="hub-empty">Unable to load notes.</div>';
      }
    }
  }

  /**
   * Render notes list
   * @param {Array} notes - Notes array
   */
  function renderNotes(notes) {
    const elements = window.WorkspaceHubState.getElements();
    const state = window.WorkspaceHubState.getState();

    console.log('[DEBUG renderNotes] called with', notes?.length, 'notes, element:', elements.notesList);
    if (!elements.notesList) return;

    if (!notes || notes.length === 0) {
      elements.notesList.innerHTML = '<div class="hub-empty">No notes yet.</div>';
      return;
    }

    const _inSelectionMode = window.WorkspaceHubSelection.isSelectionModeEnabled('notes');
    const selectedSet = state.selectedItems.notes;

    const items = notes.slice(0, 5).map((note) => {
      const title = note.name || 'Untitled Note';
      const updated = formatDate(note.updated_at || note.created_at);
      // API returns 'preview' for list items, 'content' for full note
      const notePreview = note.preview || note.content || '';
      const preview = notePreview.substring(0, 80).replace(/\n/g, ' ');
      const isSelected = selectedSet.has(note.id);

      return `
        <div class="hub-note-item${isSelected ? ' selected' : ''}" data-note-id="${escapeHtml(note.id)}">
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select note">
          </div>
          <div class="hub-note-info">
            <div class="hub-note-title">${escapeHtml(title)}</div>
            <div class="hub-note-preview">${preview ? escapeHtml(preview) + (notePreview.length > 80 ? '...' : '') : '<span class="text-muted">Empty note</span>'}</div>
            <div class="hub-note-meta">${escapeHtml(updated)}</div>
          </div>
          <button class="hub-note-delete-btn" data-action="delete" title="Delete note" style="display:inline-flex;padding:6px;border:1px solid #666;background:#333;color:#fff;border-radius:4px;cursor:pointer;margin-left:auto;">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    console.log('[DEBUG renderNotes] HTML:', items.join(''));
    elements.notesList.innerHTML = items.join('');
    console.log('[DEBUG renderNotes] innerHTML set, element now:', elements.notesList.innerHTML);
    bindNoteEvents();
  }

  /**
   * Bind event handlers to note items
   */
  function bindNoteEvents() {
    const elements = window.WorkspaceHubState.getElements();
    const inSelectionMode = window.WorkspaceHubSelection.isSelectionModeEnabled('notes');

    elements.notesList.querySelectorAll('.hub-note-item').forEach((item) => {
      const noteId = item.dataset.noteId;

      const checkbox = item.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.addEventListener('change', (event) => {
          event.stopPropagation();
          window.WorkspaceHubSelection.toggleItemSelection('notes', noteId);
        });
      }

      item.querySelector('[data-action="delete"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        deleteNote(noteId);
      });

      item.querySelector('[data-action="open"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        openNote(noteId);
      });

      item.addEventListener('click', (event) => {
        console.log('[DEBUG click] note item clicked, noteId:', noteId, 'inSelectionMode:', inSelectionMode);
        console.log('[DEBUG click] event.target:', event.target);
        if (inSelectionMode && !event.target.closest('button') && !event.target.closest('input')) {
          window.WorkspaceHubSelection.toggleItemSelection('notes', noteId);
        } else if (!inSelectionMode && !event.target.closest('button')) {
          openNote(noteId);
        }
      });
    });
  }

  // Expose notes manager globally
  window.WorkspaceHubNotes = {
    deleteNote,
    bulkDeleteNotes,
    openNote,
    createNewNote,
    createQuickNote,
    loadNotes,
    renderNotes
  };
})();
