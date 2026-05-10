// note-page.js — bootstrap for the dedicated /notes/<id> page.
//
// Loads the note via the API, populates the textarea + title, and calls
// NoteEditor.mount with a host adapter for the page surface. The page reuses
// the editor markup IDs the modal uses (#noteContentInput, #noteNameInput,
// #notePreviewContent, etc.) so the same controllers and event handlers
// work without modification.

// Note ID comes from the URL path: /notes/<uuid>. Reading it client-side
// avoids html/template script-context escaping issues that previously wrapped
// the value in literal quotes.
function readNoteIdFromPath() {
  if (typeof window === 'undefined') return '';
  const parts = window.location.pathname.split('/').filter(Boolean);
  // ['notes', '<uuid>'] — take the segment immediately after 'notes'.
  const idx = parts.indexOf('notes');
  if (idx < 0 || idx + 1 >= parts.length) return '';
  return decodeURIComponent(parts[idx + 1] || '');
}

const NOTE_ID = readNoteIdFromPath();
let currentNote = null;
let bundle = null;

async function fetchNote(noteId) {
  try {
    const resp = await fetch(`/api/notes/${encodeURIComponent(noteId)}`);
    if (!resp.ok) {
      // eslint-disable-next-line no-console
      console.error('Note fetch failed:', resp.status, resp.statusText);
      return null;
    }
    return await resp.json();
  } catch (err) {
    // eslint-disable-next-line no-console
    console.error('Note fetch errored:', err);
    return null;
  }
}

function showLoadError(message) {
  const previewContent = document.getElementById('notePreviewContent');
  if (previewContent) {
    previewContent.innerHTML = `<div class="note-page-error">${
      String(message || 'Could not load this note.')
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    }</div>`;
  }
}

async function fetchWorkspaceName(workspaceId) {
  if (!workspaceId) return null;
  try {
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}`);
    if (!resp.ok) return null;
    const data = await resp.json();
    return data?.name || data?.workspace?.name || null;
  } catch (_) {
    return null;
  }
}

function populateBreadcrumb(note, workspaceName) {
  const link = document.getElementById('notePageWorkspaceLink');
  if (link && note?.workspace_id) {
    link.href = `/workspaces/${encodeURIComponent(note.workspace_id)}`;
    link.textContent = workspaceName || 'Workspace';
  }
}

// Page-specific selection reader. No modal-show check — the page is always
// "open" when this code runs.
function readPageSelection() {
  if (typeof document === 'undefined' || typeof window === 'undefined') return null;
  if (!window.NoteEditor) return null;
  return window.NoteEditor.readSelection({
    getContent: () => document.getElementById('noteContentInput')?.value || '',
    // The page is always in preview mode; no toggle exists.
    isPreviewMode: () => true,
  });
}

async function savePageNote() {
  if (!currentNote?.id) return false;
  const name = document.getElementById('noteNameInput')?.value?.trim() || 'Untitled Note';
  const content = document.getElementById('noteContentInput')?.value || '';
  try {
    const resp = await fetch(`/api/notes/${encodeURIComponent(currentNote.id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, content }),
    });
    if (!resp.ok) return false;
    const data = await resp.json();
    if (data?.note) currentNote = { ...currentNote, ...data.note };
    return true;
  } catch (_) {
    return false;
  }
}

function showToast(msg, kind) {
  if (typeof window === 'undefined') return;
  // Toast helper from /js/modules/toast.js, loaded on the page.
  if (typeof window.showToast === 'function') {
    window.showToast(msg, kind);
  } else {
    // eslint-disable-next-line no-console
    console.log(`[${kind || 'info'}] ${msg}`);
  }
}

async function bootstrap() {
  if (!NOTE_ID) {
    showLoadError('No note ID in URL.');
    showToast('No note ID in URL', 'error');
    return;
  }
  if (!window.NoteEditor) {
    showLoadError('NoteEditor module failed to load.');
    // eslint-disable-next-line no-console
    console.error('NoteEditor module not loaded');
    return;
  }

  // 1. Load the note.
  currentNote = await fetchNote(NOTE_ID);
  if (!currentNote) {
    showLoadError(`Note not found (id: ${NOTE_ID}). The note may have been deleted or you may not have access.`);
    showToast('Note not found', 'error');
    return;
  }

  // 2. Populate the editor inputs + document title.
  const titleInput = document.getElementById('noteNameInput');
  const contentInput = document.getElementById('noteContentInput');
  if (titleInput) titleInput.value = currentNote.name || '';
  if (contentInput) contentInput.value = currentNote.content || '';
  if (currentNote.name) document.title = `${currentNote.name} - Ori Agent`;

  // 3. Fetch the workspace name for the breadcrumb (best effort).
  fetchWorkspaceName(currentNote.workspace_id).then((name) => populateBreadcrumb(currentNote, name));

  // 4. Mount the full editor.
  bundle = window.NoteEditor.mount({
    getContent: () => contentInput?.value || '',
    setContent: (v) => { if (contentInput) contentInput.value = String(v || ''); },
    getContentLines: () => {
      const value = contentInput?.value || '';
      return value.length > 0 ? value.split('\n') : [''];
    },
    setContentLines: (lines) => {
      if (contentInput) contentInput.value = (lines || []).join('\n');
    },
    isPreviewMode: () => true,
    onAutosaveFlush: savePageNote,
    aiAssist: {
      readSelection: readPageSelection,
      showToast,
    },
  });

  // 5. First render. The render call also binds the live-preview events.
  bundle.render();

  // 6. Wire title input → autosave on change.
  titleInput?.addEventListener('input', () => bundle.autosave.schedule());

  // 7. Hash-anchor scroll: if URL has #heading-text, scroll to that heading.
  if (location.hash) {
    const headingText = decodeURIComponent(location.hash.slice(1));
    requestAnimationFrame(() => {
      const source = contentInput?.value || '';
      const lines = source.split('\n');
      let cursor = 0;
      for (const line of lines) {
        if (/^#{1,6}\s+/.test(line) && line.includes(headingText)) {
          window.NoteEditor.scrollToHeadingPosition(source, cursor);
          break;
        }
        cursor += line.length + 1;
      }
    });
  }

  // 8. "Open in modal" button — navigate back to the workspace's notes list
  //    where the modal flow can be triggered. (A future enhancement could open
  //    the modal as an overlay over the page.)
  document.getElementById('noteOpenInModal')?.addEventListener('click', () => {
    if (currentNote?.workspace_id) {
      location.href = `/workspaces/${encodeURIComponent(currentNote.workspace_id)}`;
    }
  });
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', bootstrap);
} else {
  bootstrap();
}
